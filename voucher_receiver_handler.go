// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fido-device-onboard/go-fdo"
	"github.com/fido-device-onboard/go-fdo/cbor"
)

const maxVoucherSize = 10 * 1024 * 1024

// VoucherReceiverHandler handles HTTP requests for receiving vouchers
type VoucherReceiverHandler struct {
	config        *Config
	tokenManager  *VoucherReceiverTokenManager
	fileStore     *VoucherFileStore
	transmitStore *VoucherTransmissionStore
	pipeline      *VoucherPipeline
}

// NewVoucherReceiverHandler creates a new voucher receiver handler
func NewVoucherReceiverHandler(
	config *Config,
	tokenManager *VoucherReceiverTokenManager,
	fileStore *VoucherFileStore,
	transmitStore *VoucherTransmissionStore,
	pipeline *VoucherPipeline,
) *VoucherReceiverHandler {
	return &VoucherReceiverHandler{
		config:        config,
		tokenManager:  tokenManager,
		fileStore:     fileStore,
		transmitStore: transmitStore,
		pipeline:      pipeline,
	}
}

// VoucherResponse is the JSON response structure
type VoucherResponse struct {
	Status    string `json:"status"`
	VoucherID string `json:"voucher_id,omitempty"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// ServeHTTP handles the HTTP request
func (h *VoucherReceiverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sourceIP := h.getSourceIP(r)
	tokenUsed, authenticated := h.authenticate(ctx, r)
	if !authenticated {
		slog.Warn("voucher receiver: authentication failed", "source_ip", sourceIP)
		h.sendError(w, http.StatusUnauthorized, "authentication required or invalid token")
		return
	}

	if err := r.ParseMultipartForm(maxVoucherSize); err != nil {
		slog.Warn("voucher receiver: failed to parse multipart form", "error", err, "source_ip", sourceIP)
		h.sendError(w, http.StatusBadRequest, "failed to parse multipart data")
		return
	}

	file, header, err := r.FormFile("voucher")
	if err != nil {
		slog.Warn("voucher receiver: voucher file missing", "error", err, "source_ip", sourceIP)
		h.sendError(w, http.StatusBadRequest, "voucher file missing")
		return
	}
	defer file.Close()

	if header.Size > maxVoucherSize {
		slog.Warn("voucher receiver: voucher file too large", "size", header.Size, "source_ip", sourceIP)
		h.sendError(w, http.StatusRequestEntityTooLarge, "voucher file exceeds size limit")
		return
	}

	voucherData, err := io.ReadAll(io.LimitReader(file, maxVoucherSize))
	if err != nil {
		slog.Error("voucher receiver: failed to read voucher file", "error", err, "source_ip", sourceIP)
		h.sendError(w, http.StatusInternalServerError, "failed to read voucher file")
		return
	}

	voucher, err := h.parseVoucher(voucherData)
	if err != nil {
		slog.Warn("voucher receiver: failed to parse voucher", "error", err, "source_ip", sourceIP)
		h.sendError(w, http.StatusBadRequest, fmt.Sprintf("invalid voucher format: %v", err))
		return
	}

	guid := voucher.Header.Val.GUID
	guidStr := hex.EncodeToString(guid[:])

	serial := r.FormValue("serial")
	model := r.FormValue("model")
	manufacturer := r.FormValue("manufacturer")

	// Extract the current owner public key fingerprint from the voucher.
	// This identifies which owner the voucher is signed over to, and is used
	// to scope Pull API access so owners can only list/download their own vouchers.
	ownerKeyFP := extractOwnerKeyFingerprint(voucher)

	slog.Info("voucher receiver: received voucher",
		"guid", guidStr,
		"serial", serial,
		"model", model,
		"manufacturer", manufacturer,
		"owner_key_fingerprint", ownerKeyFP,
		"source_ip", sourceIP,
		"size", header.Size)

	voucherPath := h.fileStore.FilePathForGUID(guidStr)
	if _, err := os.Stat(voucherPath); err == nil {
		slog.Warn("voucher receiver: voucher already exists", "guid", guidStr, "source_ip", sourceIP)
		h.sendError(w, http.StatusConflict, "voucher already exists for this device")
		return
	}

	if err := h.saveVoucher(voucherPath, voucherData); err != nil {
		slog.Error("voucher receiver: failed to save voucher", "guid", guidStr, "error", err)
		h.sendError(w, http.StatusInternalServerError, "failed to save voucher")
		return
	}

	if err := h.tokenManager.LogReceivedVoucher(ctx, guid[:], serial, model, manufacturer, sourceIP, tokenUsed, header.Size); err != nil {
		slog.Error("voucher receiver: failed to log audit entry", "guid", guidStr, "error", err)
	}

	slog.Info("voucher receiver: voucher accepted and stored",
		"guid", guidStr,
		"path", voucherPath,
		"source_ip", sourceIP)

	// Trigger the pipeline asynchronously to sign-over and push
	if h.pipeline != nil {
		go func() {
			if err := h.pipeline.ProcessVoucher(context.Background(), voucher, serial, model, guidStr, voucherPath, ownerKeyFP); err != nil {
				slog.Error("voucher receiver: pipeline processing failed", "guid", guidStr, "error", err)
			}
		}()
	}

	h.sendSuccess(w, guidStr, "Voucher accepted and stored")
}

// authenticate checks if the request is authenticated
func (h *VoucherReceiverHandler) authenticate(ctx context.Context, r *http.Request) (string, bool) {
	if !h.config.VoucherReceiver.RequireAuth {
		return "", true
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", false
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	token := parts[1]

	if h.config.VoucherReceiver.GlobalToken != "" && token == h.config.VoucherReceiver.GlobalToken {
		return "global", true
	}

	valid, err := h.tokenManager.ValidateReceiverToken(ctx, token)
	if err != nil {
		slog.Error("voucher receiver: token validation error", "error", err)
		return "", false
	}

	if valid {
		return token, true
	}

	return "", false
}

// getSourceIP extracts the source IP from the request
func (h *VoucherReceiverHandler) getSourceIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// parseVoucher parses a voucher from PEM or raw CBOR data
func (h *VoucherReceiverHandler) parseVoucher(data []byte) (*fdo.Voucher, error) {
	pemData := string(data)
	if strings.Contains(pemData, "-----BEGIN OWNERSHIP VOUCHER-----") {
		start := strings.Index(pemData, "-----BEGIN OWNERSHIP VOUCHER-----")
		end := strings.Index(pemData, "-----END OWNERSHIP VOUCHER-----")
		if start == -1 || end == -1 {
			return nil, fmt.Errorf("invalid PEM format")
		}

		start += len("-----BEGIN OWNERSHIP VOUCHER-----")
		base64Data := strings.TrimSpace(pemData[start:end])
		base64Data = strings.ReplaceAll(base64Data, "\n", "")
		base64Data = strings.ReplaceAll(base64Data, "\r", "")

		cborData, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64: %w", err)
		}
		data = cborData
	}

	var voucher fdo.Voucher
	if err := cbor.Unmarshal(data, &voucher); err != nil {
		return nil, fmt.Errorf("failed to unmarshal voucher: %w", err)
	}

	return &voucher, nil
}

// saveVoucher saves the voucher to disk in PEM format
func (h *VoucherReceiverHandler) saveVoucher(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if strings.Contains(string(data), "-----BEGIN OWNERSHIP VOUCHER-----") {
		return os.WriteFile(path, data, 0644)
	}

	base64Data := base64.StdEncoding.EncodeToString(data)

	var pemBuilder strings.Builder
	pemBuilder.WriteString("-----BEGIN OWNERSHIP VOUCHER-----\n")
	for i := 0; i < len(base64Data); i += 64 {
		end := i + 64
		if end > len(base64Data) {
			end = len(base64Data)
		}
		pemBuilder.WriteString(base64Data[i:end])
		pemBuilder.WriteString("\n")
	}
	pemBuilder.WriteString("-----END OWNERSHIP VOUCHER-----\n")

	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, []byte(pemBuilder.String()), 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempPath, path); err != nil {
		if removeErr := os.Remove(tempPath); removeErr != nil {
			slog.Warn("failed to remove temp file", "path", tempPath, "error", removeErr)
		}
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// sendSuccess sends a successful JSON response
func (h *VoucherReceiverHandler) sendSuccess(w http.ResponseWriter, voucherID, message string) {
	resp := VoucherResponse{
		Status:    "accepted",
		VoucherID: voucherID,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode success response", "error", err)
	}
}

// extractOwnerKeyFingerprint extracts the current owner's public key from the
// voucher and returns its SHA-256 fingerprint as a hex string. This fingerprint
// is used to scope Pull API access: only the authenticated owner can list/download
// vouchers signed over to their key.
func extractOwnerKeyFingerprint(voucher *fdo.Voucher) string {
	ownerKey, err := voucher.OwnerPublicKey()
	if err != nil || ownerKey == nil {
		return ""
	}
	return FingerprintPublicKeyHex(ownerKey)
}

// sendError sends an error JSON response
func (h *VoucherReceiverHandler) sendError(w http.ResponseWriter, statusCode int, message string) {
	resp := VoucherResponse{
		Status:    "error",
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode error response", "error", err)
	}
}
