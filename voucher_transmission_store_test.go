// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func testTransmitDB(t *testing.T) *sql.DB {
	t.Helper()
	connector, err := (&driver.SQLite{}).OpenConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	return sql.OpenDB(connector)
}

func TestTransmissionStoreInit(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify voucher_transmissions table exists by querying it
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM voucher_transmissions").Scan(&count); err != nil {
		t.Fatalf("voucher_transmissions table not created: %v", err)
	}

	// Verify voucher_access_grants table exists by querying it
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM voucher_access_grants").Scan(&count); err != nil {
		t.Fatalf("voucher_access_grants table not created: %v", err)
	}

	// Init should be idempotent
	if err := store.Init(ctx); err != nil {
		t.Fatalf("second Init failed: %v", err)
	}
}

func TestCreatePendingAndFetch(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	rec := &VoucherTransmissionRecord{
		VoucherGUID:         "guid-abc-123",
		FilePath:            "/vouchers/abc.cbor",
		DestinationURL:      "https://dest.example.com/push",
		AuthToken:           "tok-secret",
		DestinationSource:   "config",
		Mode:                "push",
		SerialNumber:        "SN-001",
		ModelNumber:         "MDL-X",
		OwnerKeyFingerprint: "fp-owner-1",
	}

	id, err := store.CreatePending(ctx, rec)
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	got, err := store.FetchLatestByGUID(ctx, "guid-abc-123")
	if err != nil {
		t.Fatalf("FetchLatestByGUID failed: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
	if got.VoucherGUID != "guid-abc-123" {
		t.Errorf("VoucherGUID = %q, want %q", got.VoucherGUID, "guid-abc-123")
	}
	if got.FilePath != "/vouchers/abc.cbor" {
		t.Errorf("FilePath = %q, want %q", got.FilePath, "/vouchers/abc.cbor")
	}
	if got.DestinationURL != "https://dest.example.com/push" {
		t.Errorf("DestinationURL = %q", got.DestinationURL)
	}
	if got.AuthToken != "tok-secret" {
		t.Errorf("AuthToken = %q", got.AuthToken)
	}
	if got.Status != transmissionStatusPending {
		t.Errorf("Status = %q, want %q", got.Status, transmissionStatusPending)
	}
	if got.SerialNumber != "SN-001" {
		t.Errorf("SerialNumber = %q, want %q", got.SerialNumber, "SN-001")
	}
	if got.OwnerKeyFingerprint != "fp-owner-1" {
		t.Errorf("OwnerKeyFingerprint = %q, want %q", got.OwnerKeyFingerprint, "fp-owner-1")
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestMarkAssigned(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	id, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:              "guid-assign",
		FilePath:                 "/vouchers/assign.cbor",
		OwnerKeyFingerprint:      "fp-original",
		OriginalOwnerFingerprint: "fp-original",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.MarkAssigned(ctx, id, "fp-new-owner", "did:web:newowner.com", "fp-assigner"); err != nil {
		t.Fatalf("MarkAssigned failed: %v", err)
	}

	got, err := store.FetchByID(ctx, id)
	if err != nil {
		t.Fatalf("FetchByID failed: %v", err)
	}
	if got.Status != transmissionStatusAssigned {
		t.Errorf("Status = %q, want %q", got.Status, transmissionStatusAssigned)
	}
	if got.AssignedToFingerprint != "fp-new-owner" {
		t.Errorf("AssignedToFingerprint = %q, want %q", got.AssignedToFingerprint, "fp-new-owner")
	}
	if got.AssignedToDID != "did:web:newowner.com" {
		t.Errorf("AssignedToDID = %q, want %q", got.AssignedToDID, "did:web:newowner.com")
	}
	if got.AssignedByFingerprint != "fp-assigner" {
		t.Errorf("AssignedByFingerprint = %q, want %q", got.AssignedByFingerprint, "fp-assigner")
	}
	if !got.AssignedAt.Valid {
		t.Error("expected AssignedAt to be set")
	}
}

func TestAccessGrantInsertAndCheck(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Create a transmission so HasAccessByGUID can find it via ownership
	_, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-access",
		FilePath:            "/vouchers/access.cbor",
		OwnerKeyFingerprint: "fp-owner",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Insert an access grant for a different identity
	grant := &AccessGrant{
		VoucherGUID:         "guid-access",
		SerialNumber:        "SN-100",
		IdentityFingerprint: "fp-grantee",
		IdentityType:        "purchaser_token",
		AccessLevel:         "full",
		GrantedBy:           "fp-owner",
	}
	if err := store.InsertAccessGrant(ctx, grant); err != nil {
		t.Fatalf("InsertAccessGrant failed: %v", err)
	}

	// HasAccess should return true for the grantee
	has, err := store.HasAccess(ctx, "guid-access", "fp-grantee")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("HasAccess: expected true for grantee")
	}

	// HasAccess should return false for unknown identity
	has, err = store.HasAccess(ctx, "guid-access", "fp-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("HasAccess: expected false for unknown identity")
	}

	// HasAccessByGUID should return true for the owner (via transmission record)
	has, err = store.HasAccessByGUID(ctx, "guid-access", "fp-owner")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("HasAccessByGUID: expected true for owner")
	}

	// HasAccessByGUID should return true for the grantee (via access grant)
	has, err = store.HasAccessByGUID(ctx, "guid-access", "fp-grantee")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("HasAccessByGUID: expected true for grantee")
	}

	// HasAccessByGUID should return false for unknown identity
	has, err = store.HasAccessByGUID(ctx, "guid-access", "fp-nobody")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("HasAccessByGUID: expected false for unknown identity")
	}

	// Duplicate insert should be silently ignored (INSERT OR IGNORE)
	if err := store.InsertAccessGrant(ctx, grant); err != nil {
		t.Fatalf("duplicate InsertAccessGrant should not fail: %v", err)
	}

	grants, err := store.ListAccessGrants(ctx, "guid-access")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Fatalf("expected 1 grant after duplicate insert, got %d", len(grants))
	}
}

func TestListByAccessGrant(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Owner1 creates a voucher
	_, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-owner1",
		FilePath:            "/vouchers/owner1.cbor",
		OwnerKeyFingerprint: "fp-owner1",
		SerialNumber:        "SN-O1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Owner2 creates a voucher
	_, err = store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-owner2",
		FilePath:            "/vouchers/owner2.cbor",
		OwnerKeyFingerprint: "fp-owner2",
		SerialNumber:        "SN-O2",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Grant owner2 access to owner1's voucher
	if err := store.InsertAccessGrant(ctx, &AccessGrant{
		VoucherGUID:         "guid-owner1",
		IdentityFingerprint: "fp-owner2",
		IdentityType:        "custodian",
		AccessLevel:         "full",
		GrantedBy:           "fp-owner1",
	}); err != nil {
		t.Fatal(err)
	}

	// ListByAccessGrant for owner2 should return both records:
	// guid-owner2 (direct ownership) + guid-owner1 (via access grant)
	records, err := store.ListByAccessGrant(ctx, "fp-owner2", 50)
	if err != nil {
		t.Fatalf("ListByAccessGrant failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	guids := map[string]bool{}
	for _, r := range records {
		guids[r.VoucherGUID] = true
	}
	if !guids["guid-owner1"] {
		t.Error("expected guid-owner1 in results (via access grant)")
	}
	if !guids["guid-owner2"] {
		t.Error("expected guid-owner2 in results (via ownership)")
	}

	// ListByAccessGrant for owner1 should return only their own voucher
	records, err = store.ListByAccessGrant(ctx, "fp-owner1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record for owner1, got %d", len(records))
	}
	if records[0].VoucherGUID != "guid-owner1" {
		t.Errorf("expected guid-owner1, got %q", records[0].VoucherGUID)
	}
}

func TestFetchBySerial(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Insert two records with the same serial and one with a different serial
	for _, rec := range []*VoucherTransmissionRecord{
		{VoucherGUID: "guid-s1", FilePath: "/v/s1.cbor", SerialNumber: "SN-MATCH", OwnerKeyFingerprint: "fp1"},
		{VoucherGUID: "guid-s2", FilePath: "/v/s2.cbor", SerialNumber: "SN-MATCH", OwnerKeyFingerprint: "fp2"},
		{VoucherGUID: "guid-s3", FilePath: "/v/s3.cbor", SerialNumber: "SN-OTHER", OwnerKeyFingerprint: "fp3"},
	} {
		if _, err := store.CreatePending(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	records, err := store.FetchBySerial(ctx, "SN-MATCH")
	if err != nil {
		t.Fatalf("FetchBySerial failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records for SN-MATCH, got %d", len(records))
	}
	for _, r := range records {
		if r.SerialNumber != "SN-MATCH" {
			t.Errorf("unexpected serial %q in results", r.SerialNumber)
		}
	}

	// Fetch a serial that doesn't exist
	records, err = store.FetchBySerial(ctx, "SN-NONEXISTENT")
	if err != nil {
		t.Fatalf("FetchBySerial for missing serial failed: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for nonexistent serial, got %d", len(records))
	}
}

func TestMarkUnassigned(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Create a record and assign it
	id, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-unassign",
		FilePath:            "/vouchers/unassign.cbor",
		OwnerKeyFingerprint: "fp-orig",
		SerialNumber:        "SN-UN",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAssigned(ctx, id, "fp-new", "did:web:new.com", "fp-assigner"); err != nil {
		t.Fatal(err)
	}

	// Verify it's assigned
	got, err := store.FetchByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != transmissionStatusAssigned {
		t.Fatalf("expected assigned, got %q", got.Status)
	}

	// Unassign
	if err := store.MarkUnassigned(ctx, id, "no_destination"); err != nil {
		t.Fatalf("MarkUnassigned failed: %v", err)
	}

	got, err = store.FetchByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "no_destination" {
		t.Errorf("Status = %q, want %q", got.Status, "no_destination")
	}
	if got.AssignedAt.Valid {
		t.Error("expected AssignedAt to be NULL after unassign")
	}
	if got.AssignedToFingerprint != "" {
		t.Errorf("AssignedToFingerprint = %q, want empty", got.AssignedToFingerprint)
	}
	if got.AssignedToDID != "" {
		t.Errorf("AssignedToDID = %q, want empty", got.AssignedToDID)
	}
	if got.AssignedByFingerprint != "" {
		t.Errorf("AssignedByFingerprint = %q, want empty", got.AssignedByFingerprint)
	}
}

func TestMarkUnassigned_NotAssigned(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Create a pending record (not assigned)
	id, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         "guid-notassigned",
		FilePath:            "/vouchers/notassigned.cbor",
		OwnerKeyFingerprint: "fp-orig",
	})
	if err != nil {
		t.Fatal(err)
	}

	// MarkUnassigned on a non-assigned record should succeed (no-op on status)
	if err := store.MarkUnassigned(ctx, id, "pending"); err != nil {
		t.Fatalf("MarkUnassigned on non-assigned: %v", err)
	}

	got, err := store.FetchByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" {
		t.Errorf("Status = %q, want pending", got.Status)
	}
}

func TestRemoveAccessGrantsForVoucher(t *testing.T) {
	db := testTransmitDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	guid := "guid-remove-grants"

	// Create a transmission and add two grants
	if _, err := store.CreatePending(ctx, &VoucherTransmissionRecord{
		VoucherGUID:         guid,
		FilePath:            "/vouchers/remove.cbor",
		OwnerKeyFingerprint: "fp-owner",
		SerialNumber:        "SN-RG",
	}); err != nil {
		t.Fatal(err)
	}

	for _, fp := range []string{"fp-custodian", "fp-new-owner"} {
		if err := store.InsertAccessGrant(ctx, &AccessGrant{
			VoucherGUID:         guid,
			SerialNumber:        "SN-RG",
			IdentityFingerprint: fp,
			IdentityType:        "custodian",
			AccessLevel:         "full",
			GrantedBy:           "test",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Verify grants exist
	grants, err := store.ListAccessGrants(ctx, guid)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 {
		t.Fatalf("expected 2 grants, got %d", len(grants))
	}

	// Remove all grants
	if err := store.RemoveAccessGrantsForVoucher(ctx, guid); err != nil {
		t.Fatalf("RemoveAccessGrantsForVoucher failed: %v", err)
	}

	// Verify grants are gone
	grants, err = store.ListAccessGrants(ctx, guid)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Errorf("expected 0 grants after removal, got %d", len(grants))
	}

	// Second removal should be a no-op (not an error)
	if err := store.RemoveAccessGrantsForVoucher(ctx, guid); err != nil {
		t.Fatalf("second RemoveAccessGrantsForVoucher should not fail: %v", err)
	}
}
