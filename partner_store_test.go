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

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	connector, err := (&driver.SQLite{}).OpenConnector("file::memory:?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	return sql.OpenDB(connector)
}

func TestPartnerStore_CRUD(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewPartnerStore(db)

	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Add a partner with DID
	p := &Partner{
		ID:      "acme-mfg",
		Role:    "manufacturer",
		DIDURI:  "did:web:acme.example.com",
		PushURL: "https://acme.example.com/api/v1/vouchers",
		Enabled: true,
	}
	if err := store.Add(ctx, p); err != nil {
		t.Fatal(err)
	}

	// Get by ID
	got, err := store.Get(ctx, "acme-mfg")
	if err != nil {
		t.Fatal(err)
	}
	if got.DIDURI != "did:web:acme.example.com" {
		t.Errorf("DIDURI = %q, want %q", got.DIDURI, "did:web:acme.example.com")
	}
	if got.PushURL != "https://acme.example.com/api/v1/vouchers" {
		t.Errorf("PushURL = %q", got.PushURL)
	}
	if !got.Enabled {
		t.Error("expected enabled")
	}
	if got.CreatedAt == 0 {
		t.Error("expected non-zero CreatedAt")
	}

	// Get by DID
	got2, err := store.GetByDID(ctx, "did:web:acme.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got2.ID != "acme-mfg" {
		t.Errorf("GetByDID ID = %q, want %q", got2.ID, "acme-mfg")
	}

	// Add a bare-key partner
	p2 := &Partner{
		ID:        "legacy-partner",
		Role:      "peer",
		PublicKey: testECPublicKeyPEM,
		Enabled:   true,
	}
	if err := store.Add(ctx, p2); err != nil {
		t.Fatal(err)
	}

	// Verify fingerprint was computed
	got3, err := store.Get(ctx, "legacy-partner")
	if err != nil {
		t.Fatal(err)
	}
	if got3.PublicKeyFingerprint == "" {
		t.Error("expected non-empty fingerprint for bare-key partner")
	}

	// Get by fingerprint
	got4, err := store.GetByFingerprint(ctx, got3.PublicKeyFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if got4.ID != "legacy-partner" {
		t.Errorf("GetByFingerprint ID = %q, want %q", got4.ID, "legacy-partner")
	}

	// List all
	all, err := store.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("List() returned %d, want 2", len(all))
	}

	// List by role
	mfgs, err := store.List(ctx, "manufacturer")
	if err != nil {
		t.Fatal(err)
	}
	if len(mfgs) != 1 {
		t.Fatalf("List(manufacturer) returned %d, want 1", len(mfgs))
	}

	// Update
	got.PushURL = "https://acme.example.com/v2/vouchers"
	if err := store.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	got5, _ := store.Get(ctx, "acme-mfg")
	if got5.PushURL != "https://acme.example.com/v2/vouchers" {
		t.Errorf("after update PushURL = %q", got5.PushURL)
	}

	// Delete
	if err := store.Delete(ctx, "acme-mfg"); err != nil {
		t.Fatal(err)
	}
	_, err = store.Get(ctx, "acme-mfg")
	if err == nil {
		t.Error("expected error after delete")
	}

	// Delete nonexistent
	err = store.Delete(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent partner")
	}
}

func TestPartnerStore_ListDIDWebPartners(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Add did:web partner
	if err := store.Add(ctx, &Partner{ID: "web1", DIDURI: "did:web:a.com", Role: "peer", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Add did:key partner (should NOT appear)
	if err := store.Add(ctx, &Partner{ID: "key1", DIDURI: "did:key:z6Mk...", Role: "peer", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Add bare-key partner (should NOT appear)
	if err := store.Add(ctx, &Partner{ID: "bare1", Role: "peer", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Add disabled did:web partner (should NOT appear)
	if err := store.Add(ctx, &Partner{ID: "web2", DIDURI: "did:web:b.com", Role: "peer", Enabled: false}); err != nil {
		t.Fatal(err)
	}

	webPartners, err := store.ListDIDWebPartners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(webPartners) != 1 {
		t.Fatalf("ListDIDWebPartners() returned %d, want 1", len(webPartners))
	}
	if webPartners[0].ID != "web1" {
		t.Errorf("expected web1, got %q", webPartners[0].ID)
	}
}

func TestPartnerStore_UpdateDIDDocument(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	if err := store.Add(ctx, &Partner{
		ID:      "refresh-test",
		DIDURI:  "did:web:example.com",
		Role:    "peer",
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate DID document refresh
	err := store.UpdateDIDDocument(ctx, "refresh-test",
		`{"id":"did:web:example.com"}`,
		testECPublicKeyPEM,
		"https://example.com/push",
		"https://example.com/pull",
		`"etag-123"`,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, "refresh-test")
	if got.DIDDocument == "" {
		t.Error("expected non-empty DIDDocument after refresh")
	}
	if got.PublicKey == "" {
		t.Error("expected non-empty PublicKey after refresh")
	}
	if got.PublicKeyFingerprint == "" {
		t.Error("expected non-empty fingerprint after refresh")
	}
	if got.PushURL != "https://example.com/push" {
		t.Errorf("PushURL = %q after refresh", got.PushURL)
	}
	if got.PullURL != "https://example.com/pull" {
		t.Errorf("PullURL = %q after refresh", got.PullURL)
	}
	if got.DIDDocumentFetchedAt == 0 {
		t.Error("expected non-zero DIDDocumentFetchedAt")
	}
}

// A valid EC P-256 public key PEM for testing.
const testECPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE0/DWCvxfP6qa8WRbMwjpWBHFerbq
E+ukFJxQXWwfpp6JMzsqTvUXqhVAVNvgh/VHbglkKiZDkAdvuQIfTuTIKQ==
-----END PUBLIC KEY-----`
