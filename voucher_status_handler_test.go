// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// testStatusDB creates an in-memory SQLite database for status handler tests.
func testStatusDB(t *testing.T) *sql.DB {
	t.Helper()
	connector, err := (&driver.SQLite{}).OpenConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	return sql.OpenDB(connector)
}

// mockValidator is a simple token validator for testing.
func mockValidator(token string) (*CallerIdentity, error) {
	if token == "valid-token" {
		return &CallerIdentity{
			Fingerprint: "abc123",
			AuthMethod:  AuthMethodBearerToken,
		}, nil
	}
	return nil, fmt.Errorf("invalid token")
}

// newTestStatusStore creates an initialized VoucherTransmissionStore backed by
// an in-memory SQLite database.
func newTestStatusStore(t *testing.T) *VoucherTransmissionStore {
	t.Helper()
	db := testStatusDB(t)
	t.Cleanup(func() { _ = db.Close() })

	store := NewVoucherTransmissionStore(db)
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStatusHandler_ByGUID(t *testing.T) {
	store := newTestStatusStore(t)

	// Insert a transmission record.
	guid := "aabbccdd11223344aabbccdd11223344"
	rec := &VoucherTransmissionRecord{
		VoucherGUID:         guid,
		FilePath:            "/vouchers/test.cbor",
		SerialNumber:        "SN-001",
		Status:              transmissionStatusPending,
		OwnerKeyFingerprint: "abc123",
	}
	_, err := store.CreatePending(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}

	// No auth required (validateToken is nil).
	handler := NewVoucherStatusHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/"+guid, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp VoucherStatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.VoucherID != guid {
		t.Errorf("voucher_id = %q, want %q", resp.VoucherID, guid)
	}
	if resp.Serial != "SN-001" {
		t.Errorf("serial = %q, want %q", resp.Serial, "SN-001")
	}
	// pending maps to "held"
	if resp.Status != "held" {
		t.Errorf("status = %q, want %q", resp.Status, "held")
	}
}

func TestStatusHandler_BySerial(t *testing.T) {
	store := newTestStatusStore(t)

	guid := "11223344aabbccdd11223344aabbccdd"
	serial := "SERIAL-42"
	rec := &VoucherTransmissionRecord{
		VoucherGUID:         guid,
		FilePath:            "/vouchers/serial.cbor",
		SerialNumber:        serial,
		Status:              transmissionStatusSucceeded,
		OwnerKeyFingerprint: "abc123",
	}
	_, err := store.CreatePending(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}

	handler := NewVoucherStatusHandler(store, nil)

	// "SERIAL-42" does not match the GUID pattern, so auto-detection picks serial.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/"+serial, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp VoucherStatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.VoucherID != guid {
		t.Errorf("voucher_id = %q, want %q", resp.VoucherID, guid)
	}
	if resp.Serial != serial {
		t.Errorf("serial = %q, want %q", resp.Serial, serial)
	}
	// succeeded maps to "pushed"
	if resp.Status != "pushed" {
		t.Errorf("status = %q, want %q", resp.Status, "pushed")
	}
}

func TestStatusHandler_NotFound(t *testing.T) {
	store := newTestStatusStore(t)

	handler := NewVoucherStatusHandler(store, nil)

	nonExistentGUID := "00000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/"+nonExistentGUID, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestStatusHandler_Unauthenticated(t *testing.T) {
	store := newTestStatusStore(t)

	// Provide a validateToken function so auth is enforced.
	handler := NewVoucherStatusHandler(store, mockValidator)

	// No Authorization header.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/aabbccdd11223344aabbccdd11223344", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestStatusHandler_AssignedStatus(t *testing.T) {
	store := newTestStatusStore(t)

	guid := "deadbeef12345678deadbeef12345678"
	rec := &VoucherTransmissionRecord{
		VoucherGUID:         guid,
		FilePath:            "/vouchers/assigned.cbor",
		SerialNumber:        "SN-ASSIGN",
		Status:              transmissionStatusPending,
		OwnerKeyFingerprint: "abc123",
	}
	id, err := store.CreatePending(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}

	// Mark the record as assigned.
	if err := store.MarkAssigned(context.Background(), id, "new-owner-fp", "did:web:new-owner.example", "assigner-fp"); err != nil {
		t.Fatal(err)
	}

	handler := NewVoucherStatusHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/"+guid, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp VoucherStatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Status != "assigned" {
		t.Errorf("status = %q, want %q", resp.Status, "assigned")
	}
	if resp.AssignedAt == "" {
		t.Error("assigned_at should be present")
	}
	if resp.AssignedToFingerprint != "new-owner-fp" {
		t.Errorf("assigned_to_fingerprint = %q, want %q", resp.AssignedToFingerprint, "new-owner-fp")
	}
	if resp.AssignedToDID != "did:web:new-owner.example" {
		t.Errorf("assigned_to_did = %q, want %q", resp.AssignedToDID, "did:web:new-owner.example")
	}
	if resp.AssignedByFingerprint != "assigner-fp" {
		t.Errorf("assigned_by_fingerprint = %q, want %q", resp.AssignedByFingerprint, "assigner-fp")
	}
}

func TestMapTransmissionStatus(t *testing.T) {
	tests := []struct {
		dbStatus string
		want     string
	}{
		{transmissionStatusPending, "held"},
		{transmissionStatusSucceeded, "pushed"},
		{transmissionStatusFailed, "failed"},
		{transmissionStatusPermanent, "failed"},
		{transmissionStatusAssigned, "assigned"},
		{"something_unexpected", "unknown"},
		{"", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.dbStatus, func(t *testing.T) {
			got := mapTransmissionStatus(tt.dbStatus)
			if got != tt.want {
				t.Errorf("mapTransmissionStatus(%q) = %q, want %q", tt.dbStatus, got, tt.want)
			}
		})
	}
}
