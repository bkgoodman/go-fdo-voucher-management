// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fido-device-onboard/go-fdo"
)

// VoucherAssignHandler handles POST {root}/assign requests.
// It allows an intermediary (reseller B) to instruct the Holder (manufacturer A)
// to sign vouchers over to a different party (end customer C).
type VoucherAssignHandler struct {
	transmitStore  *VoucherTransmissionStore
	fileStore      *VoucherFileStore
	signingService *VoucherSigningService
	didResolver    *DIDResolver
	validateToken  func(string) (*CallerIdentity, error)
	ownerSigner    crypto.Signer // The Holder's (manufacturer's) signing key
}

// NewVoucherAssignHandler creates a new assign handler.
func NewVoucherAssignHandler(
	transmitStore *VoucherTransmissionStore,
	fileStore *VoucherFileStore,
	signingService *VoucherSigningService,
	didResolver *DIDResolver,
	validateToken func(string) (*CallerIdentity, error),
	ownerSigner crypto.Signer,
) *VoucherAssignHandler {
	return &VoucherAssignHandler{
		transmitStore:  transmitStore,
		fileStore:      fileStore,
		signingService: signingService,
		didResolver:    didResolver,
		validateToken:  validateToken,
		ownerSigner:    ownerSigner,
	}
}

// AssignRequest is the JSON request body for the assign endpoint.
type AssignRequest struct {
	Serials        []string `json:"serials"`
	NewOwnerDID    string   `json:"new_owner_did,omitempty"`
	NewOwnerKey    string   `json:"new_owner_key,omitempty"`
	NewOwnerKeyFmt string   `json:"new_owner_key_format,omitempty"`
	PushURL        string   `json:"push_url,omitempty"`
}

// AssignResponse is the JSON response body for the assign endpoint.
type AssignResponse struct {
	Results   []AssignResult `json:"results"`
	Timestamp string         `json:"timestamp"`
}

// AssignResult is the per-voucher result in a assign response.
type AssignResult struct {
	Serial    string `json:"serial"`
	VoucherID string `json:"voucher_id,omitempty"`
	Status    string `json:"status"` // "ok", "pending", "error"
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ServeHTTP handles the assign request.
func (h *VoucherAssignHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Authenticate
	caller, err := h.authenticateCaller(r)
	if err != nil {
		h.sendError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if !caller.CanAssign() {
		h.sendError(w, http.StatusForbidden, "caller identity cannot perform assignment")
		return
	}

	// Parse request body
	var req AssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	// Validate request
	if len(req.Serials) == 0 {
		h.sendError(w, http.StatusBadRequest, "serials is required and must not be empty")
		return
	}
	if req.NewOwnerDID == "" && req.NewOwnerKey == "" {
		h.sendError(w, http.StatusBadRequest, "either new_owner_did or new_owner_key is required")
		return
	}
	if req.NewOwnerDID != "" && req.NewOwnerKey != "" {
		h.sendError(w, http.StatusBadRequest, "new_owner_did and new_owner_key are mutually exclusive")
		return
	}

	// Resolve the new owner's public key
	ctx := r.Context()
	var newOwnerKey crypto.PublicKey
	var newOwnerDID string
	if req.NewOwnerDID != "" {
		newOwnerDID = req.NewOwnerDID
		if h.didResolver == nil {
			h.sendError(w, http.StatusBadRequest, "DID resolution not configured")
			return
		}
		key, _, resolveErr := h.didResolver.ResolveDIDKey(ctx, req.NewOwnerDID)
		if resolveErr != nil {
			h.sendError(w, http.StatusBadRequest, fmt.Sprintf("DID resolution failed: %v", resolveErr))
			return
		}
		newOwnerKey = key
	} else {
		key, parseErr := LoadPublicKeyFromPEM([]byte(req.NewOwnerKey))
		if parseErr != nil {
			h.sendError(w, http.StatusBadRequest, fmt.Sprintf("invalid new_owner_key: %v", parseErr))
			return
		}
		newOwnerKey = key
	}

	newOwnerFingerprint := FingerprintPublicKeyHex(newOwnerKey)

	// Process each serial
	var results []AssignResult
	for _, serial := range req.Serials {
		result := h.assignVoucher(ctx, serial, newOwnerKey, newOwnerFingerprint, newOwnerDID, caller)
		results = append(results, result)
	}

	resp := AssignResponse{
		Results:   results,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// assignVoucher processes a single voucher assignment.
func (h *VoucherAssignHandler) assignVoucher(
	ctx context.Context,
	serial string,
	newOwnerKey crypto.PublicKey,
	newOwnerFingerprint, newOwnerDID string,
	caller *CallerIdentity,
) AssignResult {
	// Look up voucher by serial
	recs, err := h.transmitStore.FetchBySerial(ctx, serial)
	if err != nil || len(recs) == 0 {
		return AssignResult{Serial: serial, Status: "error", ErrorCode: "not_found", Message: "voucher not found for serial"}
	}
	rec := &recs[0]

	// Verify caller has access to this voucher.
	// The global config token acts as an admin and bypasses the per-voucher
	// ownership check. Non-global callers must either be the voucher's current
	// owner (matching owner_key_fingerprint) or hold an explicit access grant.
	if caller.AuthMethod != AuthMethodGlobalToken && caller.Fingerprint != "" {
		hasAccess, accessErr := h.transmitStore.HasAccessByGUID(ctx, rec.VoucherGUID, caller.Fingerprint)
		if accessErr != nil || (!hasAccess && rec.OwnerKeyFingerprint != caller.Fingerprint) {
			return AssignResult{Serial: serial, Status: "error", ErrorCode: "not_found", Message: "voucher not found for serial"}
		}
	}

	// Check if already assigned
	if rec.Status == transmissionStatusAssigned {
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: "already_assigned", Message: "voucher has already been assigned"}
	}

	// Check if already signed over (has been pushed/pulled — can't assign after delivery)
	if rec.Status == transmissionStatusSucceeded {
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: "already_signed_over", Message: "voucher has already been delivered"}
	}

	// Load the voucher from file
	voucherPath := h.fileStore.FilePathForGUID(rec.VoucherGUID)
	if voucherPath == "" {
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: "not_found", Message: "voucher file not found"}
	}

	voucher, parseErr := fdo.ParseVoucherFile(voucherPath)
	if parseErr != nil {
		slog.Error("assign: failed to parse voucher file", "guid", rec.VoucherGUID, "error", parseErr)
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: "internal_error", Message: "failed to parse voucher"}
	}

	// Perform the cryptographic assignment
	if h.ownerSigner == nil {
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: "internal_error", Message: "owner signer not configured"}
	}

	var assigned *fdo.Voucher
	switch key := newOwnerKey.(type) {
	case *ecdsa.PublicKey:
		assigned, err = fdo.ExtendVoucher(voucher, h.ownerSigner, key, nil)
	case *rsa.PublicKey:
		assigned, err = fdo.ExtendVoucher(voucher, h.ownerSigner, key, nil)
	case []*x509.Certificate:
		assigned, err = fdo.ExtendVoucher(voucher, h.ownerSigner, key, nil)
	default:
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: "invalid_key", Message: fmt.Sprintf("unsupported key type: %T", newOwnerKey)}
	}

	if err != nil {
		slog.Warn("assign: cryptographic assignment failed", "guid", rec.VoucherGUID, "error", err)
		errCode := "internal_error"
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: errCode, Message: err.Error()}
	}

	// Back up the original voucher file so unassign can restore it
	if backupErr := h.fileStore.BackupVoucher(rec.VoucherGUID); backupErr != nil {
		slog.Error("assign: failed to backup voucher file", "guid", rec.VoucherGUID, "error", backupErr)
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: "internal_error", Message: "failed to backup voucher before assignment"}
	}

	// Save the assigned voucher
	if _, saveErr := h.fileStore.SaveVoucher(assigned); saveErr != nil {
		slog.Error("assign: failed to save assigned voucher", "guid", rec.VoucherGUID, "error", saveErr)
		return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", ErrorCode: "internal_error", Message: "failed to save assigned voucher"}
	}

	// Update the transmission record
	if markErr := h.transmitStore.MarkAssigned(ctx, rec.ID, newOwnerFingerprint, newOwnerDID, caller.Fingerprint); markErr != nil {
		slog.Error("assign: failed to mark as assigned", "guid", rec.VoucherGUID, "error", markErr)
	}

	// Insert access grants: both the assigner (B) and new owner (C) get access
	if caller.Fingerprint != "" {
		_ = h.transmitStore.InsertAccessGrant(ctx, &AccessGrant{
			VoucherGUID:         rec.VoucherGUID,
			SerialNumber:        serial,
			IdentityFingerprint: caller.Fingerprint,
			IdentityType:        "custodian",
			AccessLevel:         "full",
			GrantedBy:           "assign_api",
		})
	}
	_ = h.transmitStore.InsertAccessGrant(ctx, &AccessGrant{
		VoucherGUID:         rec.VoucherGUID,
		SerialNumber:        serial,
		IdentityFingerprint: newOwnerFingerprint,
		IdentityType:        "owner_key",
		AccessLevel:         "full",
		GrantedBy:           "assign_api",
	})

	slog.Info("assign: voucher assigned",
		"guid", rec.VoucherGUID,
		"serial", serial,
		"new_owner_fingerprint", newOwnerFingerprint,
		"new_owner_did", newOwnerDID,
		"assigned_by", caller.Fingerprint,
	)

	return AssignResult{Serial: serial, VoucherID: rec.VoucherGUID, Status: "ok", Message: "voucher assigned successfully"}
}

// authenticateCaller extracts and validates the bearer token from the request.
func (h *VoucherAssignHandler) authenticateCaller(r *http.Request) (*CallerIdentity, error) {
	if h.validateToken == nil {
		return &CallerIdentity{AuthMethod: AuthMethodNone}, nil
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("no authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return nil, fmt.Errorf("invalid authorization header")
	}

	return h.validateToken(parts[1])
}

// sendError sends an error JSON response.
func (h *VoucherAssignHandler) sendError(w http.ResponseWriter, statusCode int, message string) {
	resp := map[string]string{
		"error":     http.StatusText(statusCode),
		"message":   message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
