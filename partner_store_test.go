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
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	store := NewPartnerStore(db)

	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Add a partner with DID
	p := &Partner{
		ID:                 "acme-mfg",
		CanSupplyVouchers:  true,
		CanReceiveVouchers: false,
		DIDURI:             "did:web:acme.example.com",
		PushURL:            "https://acme.example.com/api/v1/vouchers",
		Enabled:            true,
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
		ID:                 "legacy-partner",
		CanSupplyVouchers:  true,
		CanReceiveVouchers: true,
		PublicKey:          testECPublicKeyPEM,
		Enabled:            true,
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

	// List by capability: supply
	suppliers, err := store.List(ctx, "supply")
	if err != nil {
		t.Fatal(err)
	}
	if len(suppliers) != 2 {
		t.Fatalf("List(supply) returned %d, want 2", len(suppliers))
	}

	// List by capability: receive
	receivers, err := store.List(ctx, "receive")
	if err != nil {
		t.Fatal(err)
	}
	if len(receivers) != 1 {
		t.Fatalf("List(receive) returned %d, want 1", len(receivers))
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
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Add did:web partner
	if err := store.Add(ctx, &Partner{ID: "web1", DIDURI: "did:web:a.com", CanSupplyVouchers: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Add did:key partner (should NOT appear)
	if err := store.Add(ctx, &Partner{ID: "key1", DIDURI: "did:key:z6Mk...", CanSupplyVouchers: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Add bare-key partner (should NOT appear)
	if err := store.Add(ctx, &Partner{ID: "bare1", CanSupplyVouchers: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Add disabled did:web partner (should NOT appear)
	if err := store.Add(ctx, &Partner{ID: "web2", DIDURI: "did:web:b.com", CanSupplyVouchers: true, Enabled: false}); err != nil {
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
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	if err := store.Add(ctx, &Partner{
		ID:                "refresh-test",
		DIDURI:            "did:web:example.com",
		CanSupplyVouchers: true,
		Enabled:           true,
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

func TestBootstrapPartners(t *testing.T) {
	db := testDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	trueVal := true
	falseVal := false
	config := &Config{}
	config.Partners = []PartnerConfig{
		{ID: "mfg-1", CanSupply: &trueVal, PushURL: "https://mfg1.example.com/push", Enabled: &trueVal},
		{ID: "mfg-2", CanReceive: &trueVal, PushURL: "https://mfg2.example.com/push"}, // enabled defaults to true
		{ID: "disabled-1", CanSupply: &trueVal, Enabled: &falseVal},                   // explicitly disabled
		{ID: "", CanSupply: &trueVal}, // empty ID — should be skipped
	}

	bootstrapPartners(ctx, config, store)

	// Should have 3 partners (empty ID skipped)
	all, err := store.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 partners after bootstrap, got %d", len(all))
	}

	// Verify mfg-1
	p1, _ := store.Get(ctx, "mfg-1")
	if !p1.CanSupplyVouchers {
		t.Error("mfg-1 should have can_supply")
	}
	if p1.CanReceiveVouchers {
		t.Error("mfg-1 should NOT have can_receive")
	}
	if !p1.Enabled {
		t.Error("mfg-1 should be enabled")
	}

	// Verify mfg-2 defaults
	p2, _ := store.Get(ctx, "mfg-2")
	if !p2.Enabled {
		t.Error("mfg-2 should default to enabled")
	}
	if !p2.CanReceiveVouchers {
		t.Error("mfg-2 should have can_receive")
	}

	// Verify disabled-1
	p3, _ := store.Get(ctx, "disabled-1")
	if p3.Enabled {
		t.Error("disabled-1 should be disabled")
	}

	// Idempotency: running bootstrap again should NOT duplicate
	bootstrapPartners(ctx, config, store)
	all2, _ := store.List(ctx, "")
	if len(all2) != 3 {
		t.Fatalf("expected 3 partners after second bootstrap (idempotent), got %d", len(all2))
	}
}

func TestResolveViaPartner(t *testing.T) {
	db := testDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Add a partner with push URL, receive capability, and a known public key
	if err := store.Add(ctx, &Partner{
		ID:                 "dest-partner",
		CanReceiveVouchers: true,
		PublicKey:          testECPublicKeyPEM,
		PushURL:            "https://dest.example.com/vouchers",
		AuthToken:          "secret-token",
		Enabled:            true,
	}); err != nil {
		t.Fatal(err)
	}

	// Get the fingerprint
	p, _ := store.Get(ctx, "dest-partner")
	fp := p.PublicKeyFingerprint

	cfg := DefaultConfig()
	cfg.PushService.Mode = "fallback"
	resolver := NewVoucherDestinationResolver(cfg, nil, nil, store)

	// Should resolve via partner when fingerprint matches
	dest, err := resolver.ResolveDestination(ctx, "serial", "model", "guid-1", "", fp)
	if err != nil {
		t.Fatalf("ResolveDestination failed: %v", err)
	}
	if dest.URL != "https://dest.example.com/vouchers" {
		t.Errorf("dest URL = %q, want partner push URL", dest.URL)
	}
	if dest.Token != "secret-token" {
		t.Errorf("dest Token = %q, want partner auth token", dest.Token)
	}
	if dest.Source != "partner:dest-partner" {
		t.Errorf("dest Source = %q, want partner:dest-partner", dest.Source)
	}

	// Should NOT resolve when fingerprint doesn't match
	_, err = resolver.ResolveDestination(ctx, "serial", "model", "guid-2", "", "deadbeef")
	if err == nil {
		t.Error("expected error for unknown fingerprint with no other destination configured")
	}
}

func TestResolveViaPartner_NoPushURL(t *testing.T) {
	db := testDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Partner without push URL but with receive capability
	if err := store.Add(ctx, &Partner{
		ID:                 "no-push",
		CanReceiveVouchers: true,
		PublicKey:          testECPublicKeyPEM,
		Enabled:            true,
	}); err != nil {
		t.Fatal(err)
	}

	p, _ := store.Get(ctx, "no-push")
	fp := p.PublicKeyFingerprint

	cfg := DefaultConfig()
	resolver := NewVoucherDestinationResolver(cfg, nil, nil, store)

	// Should NOT resolve via partner (no push URL), and no other dest configured → error
	_, err := resolver.ResolveDestination(ctx, "serial", "model", "guid-3", "", fp)
	if err == nil {
		t.Error("expected error for partner without push URL and no other destination")
	}
}

func TestIsTrustedSupplier_EnforcesCapability(t *testing.T) {
	db := testDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Partner with key but only receive capability (not supply)
	if err := store.Add(ctx, &Partner{
		ID:                 "receive-only",
		CanSupplyVouchers:  false,
		CanReceiveVouchers: true,
		PublicKey:          testECPublicKeyPEM,
		Enabled:            true,
	}); err != nil {
		t.Fatal(err)
	}

	pub, err := LoadPublicKeyFromPEM([]byte(testECPublicKeyPEM))
	if err != nil {
		t.Fatal(err)
	}

	// IsTrustedSupplier should reject — partner lacks can_supply_vouchers
	_, trusted := store.IsTrustedSupplier(ctx, pub)
	if trusted {
		t.Error("IsTrustedSupplier should reject partner without can_supply_vouchers")
	}
}

func TestResolveViaPartner_EnforcesReceiveCapability(t *testing.T) {
	db := testDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	store := NewPartnerStore(db)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Partner with push URL but only supply capability (not receive)
	if err := store.Add(ctx, &Partner{
		ID:                 "supply-only",
		CanSupplyVouchers:  true,
		CanReceiveVouchers: false,
		PublicKey:          testECPublicKeyPEM,
		PushURL:            "https://supply-only.example.com/vouchers",
		Enabled:            true,
	}); err != nil {
		t.Fatal(err)
	}

	p, _ := store.Get(ctx, "supply-only")
	fp := p.PublicKeyFingerprint

	cfg := DefaultConfig()
	resolver := NewVoucherDestinationResolver(cfg, nil, nil, store)

	// Should NOT resolve — partner has push URL but lacks can_receive_vouchers
	_, err := resolver.ResolveDestination(ctx, "serial", "model", "guid-cap", "", fp)
	if err == nil {
		t.Error("expected error: partner lacks can_receive_vouchers, should not be routed to")
	}
}

// A valid EC P-256 public key PEM for testing.
const testECPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE0/DWCvxfP6qa8WRbMwjpWBHFerbq
E+ukFJxQXWwfpp6JMzsqTvUXqhVAVNvgh/VHbglkKiZDkAdvuQIfTuTIKQ==
-----END PUBLIC KEY-----`
