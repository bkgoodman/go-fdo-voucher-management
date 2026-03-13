// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// VoucherListHandler handles GET {root}/list requests.
// Returns vouchers scoped to the authenticated caller's access.
type VoucherListHandler struct {
	transmitStore *VoucherTransmissionStore
	validateToken func(string) (*CallerIdentity, error)
}

// NewVoucherListHandler creates a new list handler.
func NewVoucherListHandler(transmitStore *VoucherTransmissionStore, validateToken func(string) (*CallerIdentity, error)) *VoucherListHandler {
	return &VoucherListHandler{
		transmitStore: transmitStore,
		validateToken: validateToken,
	}
}

// VoucherListItem is a single voucher in the list response.
type VoucherListItem struct {
	VoucherID             string `json:"voucher_id"`
	Serial                string `json:"serial,omitempty"`
	Status                string `json:"status"`
	OwnerKeyFingerprint   string `json:"owner_key_fingerprint,omitempty"`
	CreatedAt             string `json:"created_at,omitempty"`
	AssignedAt            string `json:"assigned_at,omitempty"`
	AssignedToFingerprint string `json:"assigned_to_fingerprint,omitempty"`
	AssignedToDID         string `json:"assigned_to_did,omitempty"`
	AssignedByFingerprint string `json:"assigned_by_fingerprint,omitempty"`
	Destination           string `json:"destination,omitempty"`
	PushAttempts          int    `json:"push_attempts,omitempty"`
}

// VoucherListResponse is the JSON response for the list endpoint.
type VoucherListResponse struct {
	Vouchers  []VoucherListItem `json:"vouchers"`
	Count     int               `json:"count"`
	Timestamp string            `json:"timestamp"`
}

// ServeHTTP handles the list request.
func (h *VoucherListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Authenticate
	caller, err := h.authenticateCaller(r)
	if err != nil {
		h.sendError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// Parse query parameters
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, parseErr := strconv.Atoi(v); parseErr == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}

	// Fetch vouchers scoped to caller's access.
	// The global config token acts as an admin and sees all vouchers.
	ctx := r.Context()
	var records []VoucherTransmissionRecord
	if caller.AuthMethod == AuthMethodGlobalToken {
		records, err = h.transmitStore.ListTransmissions(ctx, "", "", limit)
	} else {
		records, err = h.transmitStore.ListByAccessGrant(ctx, caller.Fingerprint, limit)
	}
	if err != nil {
		slog.Error("list handler: query failed", "error", err)
		h.sendError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Build response
	items := make([]VoucherListItem, 0, len(records))
	for _, rec := range records {
		item := VoucherListItem{
			VoucherID:           rec.VoucherGUID,
			Serial:              rec.SerialNumber,
			Status:              mapTransmissionStatus(rec.Status),
			OwnerKeyFingerprint: rec.OwnerKeyFingerprint,
			Destination:         rec.DestinationURL,
			PushAttempts:        rec.Attempts,
		}
		if !rec.CreatedAt.IsZero() {
			item.CreatedAt = rec.CreatedAt.UTC().Format(time.RFC3339)
		}
		if rec.AssignedAt.Valid {
			item.AssignedAt = rec.AssignedAt.Time.UTC().Format(time.RFC3339)
		}
		if rec.AssignedToFingerprint != "" {
			item.AssignedToFingerprint = rec.AssignedToFingerprint
		}
		if rec.AssignedToDID != "" {
			item.AssignedToDID = rec.AssignedToDID
		}
		if rec.AssignedByFingerprint != "" {
			item.AssignedByFingerprint = rec.AssignedByFingerprint
		}
		items = append(items, item)
	}

	resp := VoucherListResponse{
		Vouchers:  items,
		Count:     len(items),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// authenticateCaller extracts and validates the bearer token from the request.
func (h *VoucherListHandler) authenticateCaller(r *http.Request) (*CallerIdentity, error) {
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
func (h *VoucherListHandler) sendError(w http.ResponseWriter, statusCode int, message string) {
	resp := map[string]string{
		"error":     http.StatusText(statusCode),
		"message":   message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
