// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVoucherListHandler_Unauthenticated(t *testing.T) {
	store := newTestStatusStore(t)
	handler := NewVoucherListHandler(store, mockValidator)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/vouchers/list", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestVoucherListHandler_Empty(t *testing.T) {
	store := newTestStatusStore(t)
	handler := NewVoucherListHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/vouchers/list", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp VoucherListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 0 {
		t.Errorf("expected 0 vouchers, got %d", resp.Count)
	}
}

func TestVoucherListHandler_ScopedByAccess(t *testing.T) {
	store := newTestStatusStore(t)
	ctx := context.Background()

	// Create two vouchers owned by different keys
	_, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-owner-abc",
		FilePath:            "/v/a.cbor",
		SerialNumber:        "SN-A",
		Status:              transmissionStatusPending,
		OwnerKeyFingerprint: "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-owner-xyz",
		FilePath:            "/v/b.cbor",
		SerialNumber:        "SN-B",
		Status:              transmissionStatusPending,
		OwnerKeyFingerprint: "xyz789",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use mockValidator which returns fingerprint "abc123" for "valid-token"
	handler := NewVoucherListHandler(store, mockValidator)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/vouchers/list", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp VoucherListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	// Should only see the voucher owned by abc123
	if resp.Count != 1 {
		t.Fatalf("expected 1 voucher, got %d", resp.Count)
	}
	if resp.Vouchers[0].VoucherID != "guid-owner-abc" {
		t.Errorf("expected guid-owner-abc, got %s", resp.Vouchers[0].VoucherID)
	}
}

func TestVoucherListHandler_IncludesGrantAccess(t *testing.T) {
	store := newTestStatusStore(t)
	ctx := context.Background()

	// Create a voucher owned by someone else
	_, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-other-owner",
		FilePath:            "/v/c.cbor",
		SerialNumber:        "SN-C",
		Status:              transmissionStatusAssigned,
		OwnerKeyFingerprint: "other-fp",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Grant access to abc123
	if err := store.InsertAccessGrant(ctx, &AccessGrant{
		VoucherGUID:         "guid-other-owner",
		IdentityFingerprint: "abc123",
		IdentityType:        "custodian",
		AccessLevel:         "full",
		GrantedBy:           "other-fp",
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewVoucherListHandler(store, mockValidator)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vouchers/list", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp VoucherListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	// Should see the voucher via access grant
	if resp.Count != 1 {
		t.Fatalf("expected 1 voucher, got %d", resp.Count)
	}
	if resp.Vouchers[0].VoucherID != "guid-other-owner" {
		t.Errorf("expected guid-other-owner, got %s", resp.Vouchers[0].VoucherID)
	}
	if resp.Vouchers[0].Status != "assigned" {
		t.Errorf("expected status 'assigned', got %s", resp.Vouchers[0].Status)
	}
}

func TestListCustodians(t *testing.T) {
	store := newTestStatusStore(t)
	ctx := context.Background()

	// Create vouchers and grants
	_, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-cust-1",
		FilePath:            "/v/1.cbor",
		SerialNumber:        "SN-C1",
		OwnerKeyFingerprint: "owner-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-cust-2",
		FilePath:            "/v/2.cbor",
		SerialNumber:        "SN-C2",
		OwnerKeyFingerprint: "owner-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Custodian A assigned two vouchers
	for _, guid := range []string{"guid-cust-1", "guid-cust-2"} {
		if err := store.InsertAccessGrant(ctx, &AccessGrant{
			VoucherGUID:         guid,
			IdentityFingerprint: "custodian-a-fp",
			IdentityType:        "custodian",
			AccessLevel:         "full",
			GrantedBy:           "assign_api",
		}); err != nil {
			t.Fatal(err)
		}
	}

	summaries, err := store.ListCustodians(ctx, 50)
	if err != nil {
		t.Fatalf("ListCustodians failed: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 custodian, got %d", len(summaries))
	}
	if summaries[0].Fingerprint != "custodian-a-fp" {
		t.Errorf("expected custodian-a-fp, got %s", summaries[0].Fingerprint)
	}
	if summaries[0].VoucherCount != 2 {
		t.Errorf("expected 2 vouchers, got %d", summaries[0].VoucherCount)
	}
}

func TestListByCustodian(t *testing.T) {
	store := newTestStatusStore(t)
	ctx := context.Background()

	_, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-bc-1",
		FilePath:            "/v/bc1.cbor",
		SerialNumber:        "SN-BC1",
		OwnerKeyFingerprint: "owner-x",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.InsertAccessGrant(ctx, &AccessGrant{
		VoucherGUID:         "guid-bc-1",
		IdentityFingerprint: "custodian-b-fp",
		IdentityType:        "custodian",
		AccessLevel:         "full",
		GrantedBy:           "assign_api",
	}); err != nil {
		t.Fatal(err)
	}

	records, err := store.ListByCustodian(ctx, "custodian-b-fp", 50)
	if err != nil {
		t.Fatalf("ListByCustodian failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].VoucherGUID != "guid-bc-1" {
		t.Errorf("expected guid-bc-1, got %s", records[0].VoucherGUID)
	}

	// Unknown custodian should return empty
	records, err = store.ListByCustodian(ctx, "nobody", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for unknown custodian, got %d", len(records))
	}
}

func TestListAllAccessGrants(t *testing.T) {
	store := newTestStatusStore(t)
	ctx := context.Background()

	grants := []AccessGrant{
		{VoucherGUID: "g1", IdentityFingerprint: "fp1", IdentityType: "owner_key", AccessLevel: "full", GrantedBy: "system"},
		{VoucherGUID: "g2", IdentityFingerprint: "fp2", IdentityType: "custodian", AccessLevel: "full", GrantedBy: "assign_api"},
		{VoucherGUID: "g3", IdentityFingerprint: "fp3", IdentityType: "custodian", AccessLevel: "full", GrantedBy: "assign_api"},
	}
	for i := range grants {
		if err := store.InsertAccessGrant(ctx, &grants[i]); err != nil {
			t.Fatal(err)
		}
	}

	// All grants
	all, err := store.ListAllAccessGrants(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 grants, got %d", len(all))
	}

	// Filter by type
	custodians, err := store.ListAllAccessGrants(ctx, "custodian", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(custodians) != 2 {
		t.Fatalf("expected 2 custodian grants, got %d", len(custodians))
	}
}
