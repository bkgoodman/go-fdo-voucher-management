// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// VoucherStatusHandler handles GET {root}/status/{identifier} requests.
// It returns the processing status of a voucher, identified by GUID or serial.
type VoucherStatusHandler struct {
	transmitStore *VoucherTransmissionStore
	validateToken func(string) (*CallerIdentity, error)
}

// NewVoucherStatusHandler creates a new status handler.
func NewVoucherStatusHandler(transmitStore *VoucherTransmissionStore, validateToken func(string) (*CallerIdentity, error)) *VoucherStatusHandler {
	return &VoucherStatusHandler{
		transmitStore: transmitStore,
		validateToken: validateToken,
	}
}

// VoucherStatusResponse is the JSON response for the status endpoint.
// Required fields: VoucherID, Serial, Status. All others are optional.
type VoucherStatusResponse struct {
	// Required fields
	VoucherID string `json:"voucher_id"`
	Serial    string `json:"serial"`
	Status    string `json:"status"`

	// Optional fields — omitted when empty/zero
	OwnerKeyFingerprint   string `json:"owner_key_fingerprint,omitempty"`
	CreatedAt             string `json:"created_at,omitempty"`
	SignedOverAt          string `json:"signed_over_at,omitempty"`
	PushedAt              string `json:"pushed_at,omitempty"`
	PushedTo              string `json:"pushed_to,omitempty"`
	PushAttempts          int    `json:"push_attempts,omitempty"`
	LastPushError         string `json:"last_push_error,omitempty"`
	PulledAt              string `json:"pulled_at,omitempty"`
	AssignedAt            string `json:"assigned_at,omitempty"`
	AssignedToFingerprint string `json:"assigned_to_fingerprint,omitempty"`
	AssignedToDID         string `json:"assigned_to_did,omitempty"`
	AssignedByFingerprint string `json:"assigned_by_fingerprint,omitempty"`
	ErrorMessage          string `json:"error_message,omitempty"`
}

// guidPattern matches hex-encoded GUIDs (32 hex chars, optionally with hyphens).
var guidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)

// ServeHTTP handles the status request.
func (h *VoucherStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Authenticate
	caller, err := h.authenticateCaller(r)
	if err != nil {
		h.sendError(w, r, http.StatusUnauthorized, "authentication required")
		return
	}

	// Extract identifier from path: {root}/status/{identifier}
	identifier := extractLastPathSegment(r.URL.Path)
	if identifier == "" {
		h.sendError(w, r, http.StatusBadRequest, "identifier is required")
		return
	}

	// Disambiguate identifier type
	identifierType := r.URL.Query().Get("type")
	isGUID := false
	switch identifierType {
	case "guid":
		isGUID = true
	case "serial":
		isGUID = false
	case "":
		// Auto-detect: if it looks like a GUID, treat as GUID
		isGUID = guidPattern.MatchString(identifier)
	default:
		h.sendError(w, r, http.StatusBadRequest, "invalid type parameter; must be 'guid' or 'serial'")
		return
	}

	// Normalize GUID: strip hyphens for DB lookup
	lookupID := identifier
	if isGUID {
		lookupID = strings.ReplaceAll(identifier, "-", "")
	}

	// Look up the voucher
	ctx := r.Context()
	var rec *VoucherTransmissionRecord
	if isGUID {
		rec, err = h.transmitStore.FetchLatestByGUID(ctx, lookupID)
	} else {
		recs, fetchErr := h.transmitStore.FetchBySerial(ctx, lookupID)
		if fetchErr == nil && len(recs) > 0 {
			rec = &recs[0]
		} else {
			err = fetchErr
			if err == nil {
				err = sql.ErrNoRows
			}
		}
	}

	if err != nil || rec == nil {
		h.sendError(w, r, http.StatusNotFound, "voucher not found")
		return
	}

	// Access scoping: verify caller has access to this voucher.
	// The global config token acts as an admin and bypasses the per-voucher
	// ownership check.
	if caller != nil && caller.AuthMethod != AuthMethodGlobalToken && caller.Fingerprint != "" {
		hasAccess, accessErr := h.transmitStore.HasAccessByGUID(ctx, rec.VoucherGUID, caller.Fingerprint)
		if accessErr != nil {
			slog.Error("status handler: access check failed", "error", accessErr)
			h.sendError(w, r, http.StatusInternalServerError, "internal error")
			return
		}
		if !hasAccess {
			// Also check direct ownership match
			if rec.OwnerKeyFingerprint != caller.Fingerprint {
				h.sendError(w, r, http.StatusNotFound, "voucher not found")
				return
			}
		}
	}

	// Build response
	resp := h.buildStatusResponse(rec)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
		slog.Error("status handler: failed to encode response", "error", encErr)
	}
}

// buildStatusResponse maps internal transmission status to spec status values.
func (h *VoucherStatusHandler) buildStatusResponse(rec *VoucherTransmissionRecord) *VoucherStatusResponse {
	resp := &VoucherStatusResponse{
		VoucherID:           rec.VoucherGUID,
		Serial:              rec.SerialNumber,
		Status:              mapTransmissionStatus(rec.Status),
		OwnerKeyFingerprint: rec.OwnerKeyFingerprint,
		PushAttempts:        rec.Attempts,
		PushedTo:            rec.DestinationURL,
	}

	if !rec.CreatedAt.IsZero() {
		resp.CreatedAt = rec.CreatedAt.UTC().Format(time.RFC3339)
	}
	if rec.DeliveredAt.Valid {
		resp.PushedAt = rec.DeliveredAt.Time.UTC().Format(time.RFC3339)
	}
	if rec.LastError != "" {
		resp.LastPushError = rec.LastError
		if rec.Status == transmissionStatusFailed || rec.Status == transmissionStatusPermanent {
			resp.ErrorMessage = rec.LastError
		}
	}
	if rec.AssignedAt.Valid {
		resp.AssignedAt = rec.AssignedAt.Time.UTC().Format(time.RFC3339)
	}
	if rec.AssignedToFingerprint != "" {
		resp.AssignedToFingerprint = rec.AssignedToFingerprint
	}
	if rec.AssignedToDID != "" {
		resp.AssignedToDID = rec.AssignedToDID
	}
	if rec.AssignedByFingerprint != "" {
		resp.AssignedByFingerprint = rec.AssignedByFingerprint
	}

	return resp
}

// mapTransmissionStatus maps internal DB status values to spec-defined status strings.
func mapTransmissionStatus(dbStatus string) string {
	switch dbStatus {
	case transmissionStatusPending:
		return "held"
	case transmissionStatusSucceeded:
		return "pushed"
	case transmissionStatusFailed, transmissionStatusPermanent:
		return "failed"
	case transmissionStatusAssigned:
		return "assigned"
	default:
		return "unknown"
	}
}

// authenticateCaller extracts and validates the bearer token from the request.
func (h *VoucherStatusHandler) authenticateCaller(r *http.Request) (*CallerIdentity, error) {
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

// extractLastPathSegment returns the last non-empty path segment.
func extractLastPathSegment(path string) string {
	path = strings.TrimRight(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// sendError sends an error JSON response.
func (h *VoucherStatusHandler) sendError(w http.ResponseWriter, _ *http.Request, statusCode int, message string) {
	resp := map[string]string{
		"error":     http.StatusText(statusCode),
		"message":   message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
