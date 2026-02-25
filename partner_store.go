// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Partner represents a trusted partner identity stored in the database.
// Partners may be identified by DID URI, bare public key, or just a push URL.
type Partner struct {
	ID string `json:"id"` // human-readable label, e.g., "acme-mfg"

	// Capabilities — what this partner is authorized to do
	CanSupplyVouchers  bool `json:"can_supply_vouchers"`  // we accept vouchers FROM this partner (upstream)
	CanReceiveVouchers bool `json:"can_receive_vouchers"` // we push vouchers TO this partner (downstream)

	// Identity
	DIDURI    string `json:"did_uri,omitempty"`    // did:web:... or did:key:... (empty for bare-key/URL-only)
	PublicKey string `json:"public_key,omitempty"` // PEM-encoded public key

	// Endpoints
	PushURL   string `json:"push_url,omitempty"`   // FDOVoucherRecipient URL
	PullURL   string `json:"pull_url,omitempty"`   // FDOVoucherHolder URL
	AuthToken string `json:"auth_token,omitempty"` // optional bearer token for push

	// Cached DID Document (for did:web refresh)
	DIDDocument          string `json:"did_document,omitempty"`            // raw JSON
	DIDDocumentFetchedAt int64  `json:"did_document_fetched_at,omitempty"` // Unix timestamp
	DIDDocumentETag      string `json:"did_document_etag,omitempty"`       // HTTP ETag

	// Computed (not stored directly — derived from PublicKey)
	PublicKeyFingerprint string `json:"public_key_fingerprint,omitempty"` // hex SHA-256 of CBOR-encoded key

	Enabled   bool  `json:"enabled"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// PartnerStore manages trusted partner identities in the database.
type PartnerStore struct {
	db *sql.DB
}

// NewPartnerStore creates a new PartnerStore.
func NewPartnerStore(db *sql.DB) *PartnerStore {
	return &PartnerStore{db: db}
}

// Init creates the partners table if it does not exist.
func (s *PartnerStore) Init(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("partner store: database not initialized")
	}

	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS partners (
		id                       TEXT PRIMARY KEY,
		did_uri                  TEXT,
		public_key               TEXT,
		public_key_fingerprint   TEXT,
		push_url                 TEXT,
		pull_url                 TEXT,
		auth_token               TEXT,
		did_document             TEXT,
		did_document_fetched_at  INTEGER,
		did_document_etag        TEXT,
		can_supply_vouchers      INTEGER NOT NULL DEFAULT 0,
		can_receive_vouchers     INTEGER NOT NULL DEFAULT 0,
		enabled                  INTEGER NOT NULL DEFAULT 1,
		created_at               INTEGER NOT NULL,
		updated_at               INTEGER NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("partner store: failed to create table: %w", err)
	}

	// Index on fingerprint for voucher signature verification lookups
	_, err = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_partners_fingerprint ON partners(public_key_fingerprint)`)
	if err != nil {
		return fmt.Errorf("partner store: failed to create fingerprint index: %w", err)
	}

	// Index on DID URI for resolution lookups
	_, err = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_partners_did_uri ON partners(did_uri)`)
	if err != nil {
		return fmt.Errorf("partner store: failed to create DID URI index: %w", err)
	}

	return nil
}

// Add inserts a new partner. The PublicKeyFingerprint is computed automatically
// if PublicKey is set.
func (s *PartnerStore) Add(ctx context.Context, p *Partner) error {
	now := time.Now().Unix()
	p.CreatedAt = now
	p.UpdatedAt = now

	if p.PublicKey != "" {
		fp, err := fingerprintPEM(p.PublicKey)
		if err != nil {
			return fmt.Errorf("partner store: failed to compute fingerprint: %w", err)
		}
		p.PublicKeyFingerprint = fp
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO partners
		(id, did_uri, public_key, public_key_fingerprint, push_url, pull_url, auth_token,
		 did_document, did_document_fetched_at, did_document_etag,
		 can_supply_vouchers, can_receive_vouchers, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, nullStr(p.DIDURI), nullStr(p.PublicKey), nullStr(p.PublicKeyFingerprint),
		nullStr(p.PushURL), nullStr(p.PullURL), nullStr(p.AuthToken),
		nullStr(p.DIDDocument), nullInt(p.DIDDocumentFetchedAt), nullStr(p.DIDDocumentETag),
		boolToInt(p.CanSupplyVouchers), boolToInt(p.CanReceiveVouchers),
		boolToInt(p.Enabled), p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("partner store: failed to insert %q: %w", p.ID, err)
	}

	slog.Info("partner store: added partner", "id", p.ID,
		"can_supply", p.CanSupplyVouchers, "can_receive", p.CanReceiveVouchers,
		"did_uri", p.DIDURI, "has_key", p.PublicKey != "")
	return nil
}

// Get retrieves a partner by ID.
func (s *PartnerStore) Get(ctx context.Context, id string) (*Partner, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, did_uri, public_key, public_key_fingerprint, push_url, pull_url, auth_token,
		did_document, did_document_fetched_at, did_document_etag,
		can_supply_vouchers, can_receive_vouchers, enabled, created_at, updated_at
		FROM partners WHERE id = ?`, id)
	return scanPartner(row)
}

// GetByDID retrieves a partner by DID URI.
func (s *PartnerStore) GetByDID(ctx context.Context, didURI string) (*Partner, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, did_uri, public_key, public_key_fingerprint, push_url, pull_url, auth_token,
		did_document, did_document_fetched_at, did_document_etag,
		can_supply_vouchers, can_receive_vouchers, enabled, created_at, updated_at
		FROM partners WHERE did_uri = ?`, didURI)
	return scanPartner(row)
}

// GetByFingerprint retrieves an enabled partner whose public key fingerprint matches.
// This is the primary lookup path for voucher signature verification.
func (s *PartnerStore) GetByFingerprint(ctx context.Context, fingerprint string) (*Partner, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, did_uri, public_key, public_key_fingerprint, push_url, pull_url, auth_token,
		did_document, did_document_fetched_at, did_document_etag,
		can_supply_vouchers, can_receive_vouchers, enabled, created_at, updated_at
		FROM partners WHERE public_key_fingerprint = ? AND enabled = 1`, fingerprint)
	return scanPartner(row)
}

// GetByKeyFingerprint looks up a partner by computing the fingerprint of the
// given crypto.PublicKey. Returns nil, nil if not found.
func (s *PartnerStore) GetByKeyFingerprint(ctx context.Context, pub crypto.PublicKey) (*Partner, error) {
	fp := FingerprintPublicKeyHex(pub)
	if fp == "" {
		return nil, fmt.Errorf("partner store: failed to compute key fingerprint")
	}
	p, err := s.GetByFingerprint(ctx, fp)
	if err != nil {
		return nil, nil //nolint:nilerr // not found is not an error
	}
	return p, nil
}

// List returns all partners, optionally filtered by capability.
// filter can be: "supply" (can_supply_vouchers=1), "receive" (can_receive_vouchers=1), or empty (all).
func (s *PartnerStore) List(ctx context.Context, filter string) ([]*Partner, error) {
	query := `SELECT
		id, did_uri, public_key, public_key_fingerprint, push_url, pull_url, auth_token,
		did_document, did_document_fetched_at, did_document_etag,
		can_supply_vouchers, can_receive_vouchers, enabled, created_at, updated_at
		FROM partners`
	var args []interface{}
	switch filter {
	case "supply":
		query += " WHERE can_supply_vouchers = 1"
	case "receive":
		query += " WHERE can_receive_vouchers = 1"
	case "":
		// no filter
	default:
		return nil, fmt.Errorf("partner store: unknown filter %q (use supply, receive, or empty)", filter)
	}
	query += " ORDER BY id"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("partner store: list failed: %w", err)
	}
	defer rows.Close()

	var partners []*Partner
	for rows.Next() {
		p, err := scanPartnerRow(rows)
		if err != nil {
			return nil, err
		}
		partners = append(partners, p)
	}
	return partners, rows.Err()
}

// ListDIDWebPartners returns all enabled partners with did:web URIs
// that need periodic DID Document refresh.
func (s *PartnerStore) ListDIDWebPartners(ctx context.Context) ([]*Partner, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, did_uri, public_key, public_key_fingerprint, push_url, pull_url, auth_token,
		did_document, did_document_fetched_at, did_document_etag,
		can_supply_vouchers, can_receive_vouchers, enabled, created_at, updated_at
		FROM partners WHERE did_uri LIKE 'did:web:%' AND enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("partner store: list did:web partners failed: %w", err)
	}
	defer rows.Close()

	var partners []*Partner
	for rows.Next() {
		p, err := scanPartnerRow(rows)
		if err != nil {
			return nil, err
		}
		partners = append(partners, p)
	}
	return partners, rows.Err()
}

// Update modifies an existing partner. UpdatedAt is set automatically.
func (s *PartnerStore) Update(ctx context.Context, p *Partner) error {
	p.UpdatedAt = time.Now().Unix()

	if p.PublicKey != "" {
		fp, err := fingerprintPEM(p.PublicKey)
		if err != nil {
			return fmt.Errorf("partner store: failed to compute fingerprint: %w", err)
		}
		p.PublicKeyFingerprint = fp
	} else {
		p.PublicKeyFingerprint = ""
	}

	_, err := s.db.ExecContext(ctx, `UPDATE partners SET
		did_uri = ?, public_key = ?, public_key_fingerprint = ?,
		push_url = ?, pull_url = ?, auth_token = ?,
		did_document = ?, did_document_fetched_at = ?, did_document_etag = ?,
		can_supply_vouchers = ?, can_receive_vouchers = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		nullStr(p.DIDURI), nullStr(p.PublicKey), nullStr(p.PublicKeyFingerprint),
		nullStr(p.PushURL), nullStr(p.PullURL), nullStr(p.AuthToken),
		nullStr(p.DIDDocument), nullInt(p.DIDDocumentFetchedAt), nullStr(p.DIDDocumentETag),
		boolToInt(p.CanSupplyVouchers), boolToInt(p.CanReceiveVouchers),
		boolToInt(p.Enabled), p.UpdatedAt,
		p.ID,
	)
	if err != nil {
		return fmt.Errorf("partner store: failed to update %q: %w", p.ID, err)
	}
	return nil
}

// UpdateDIDDocument updates the cached DID document and derived fields
// (public key, push/pull URLs) for a partner after a successful DID refresh.
func (s *PartnerStore) UpdateDIDDocument(ctx context.Context, id string, docJSON string, publicKeyPEM string, pushURL string, pullURL string, etag string) error {
	now := time.Now().Unix()

	var fingerprint string
	if publicKeyPEM != "" {
		fp, err := fingerprintPEM(publicKeyPEM)
		if err != nil {
			return fmt.Errorf("partner store: failed to compute fingerprint: %w", err)
		}
		fingerprint = fp
	}

	_, err := s.db.ExecContext(ctx, `UPDATE partners SET
		did_document = ?, did_document_fetched_at = ?, did_document_etag = ?,
		public_key = ?, public_key_fingerprint = ?,
		push_url = ?, pull_url = ?,
		updated_at = ?
		WHERE id = ?`,
		nullStr(docJSON), now, nullStr(etag),
		nullStr(publicKeyPEM), nullStr(fingerprint),
		nullStr(pushURL), nullStr(pullURL),
		now, id,
	)
	if err != nil {
		return fmt.Errorf("partner store: failed to update DID document for %q: %w", id, err)
	}
	return nil
}

// Delete removes a partner by ID.
func (s *PartnerStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM partners WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("partner store: failed to delete %q: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("partner store: partner %q not found", id)
	}
	slog.Info("partner store: deleted partner", "id", id)
	return nil
}

// IsTrustedSupplier checks whether the given public key belongs to an enabled
// partner that is authorized to supply vouchers (upstream manufacturer/peer).
// Returns the partner ID if trusted, or empty string if not.
func (s *PartnerStore) IsTrustedSupplier(ctx context.Context, pub crypto.PublicKey) (string, bool) {
	p, err := s.GetByKeyFingerprint(ctx, pub)
	if err != nil || p == nil {
		return "", false
	}
	if !p.CanSupplyVouchers {
		slog.Debug("partner store: key matched but partner lacks can_supply_vouchers", "id", p.ID)
		return "", false
	}
	return p.ID, true
}

// ExportJSON exports all partners as a JSON array (for backup/migration).
func (s *PartnerStore) ExportJSON(ctx context.Context) ([]byte, error) {
	partners, err := s.List(ctx, "")
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(partners, "", "  ")
}

// --- helpers ---

// scanPartner scans a single row into a Partner struct.
func scanPartner(row *sql.Row) (*Partner, error) {
	var p Partner
	var didURI, publicKey, fingerprint, pushURL, pullURL, authToken sql.NullString
	var didDoc, didETag sql.NullString
	var didDocFetchedAt sql.NullInt64
	var canSupply, canReceive, enabled int

	err := row.Scan(
		&p.ID, &didURI, &publicKey, &fingerprint, &pushURL, &pullURL, &authToken,
		&didDoc, &didDocFetchedAt, &didETag,
		&canSupply, &canReceive, &enabled, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	p.DIDURI = didURI.String
	p.PublicKey = publicKey.String
	p.PublicKeyFingerprint = fingerprint.String
	p.PushURL = pushURL.String
	p.PullURL = pullURL.String
	p.AuthToken = authToken.String
	p.DIDDocument = didDoc.String
	p.DIDDocumentFetchedAt = didDocFetchedAt.Int64
	p.DIDDocumentETag = didETag.String
	p.CanSupplyVouchers = canSupply != 0
	p.CanReceiveVouchers = canReceive != 0
	p.Enabled = enabled != 0

	return &p, nil
}

// scanPartnerRow scans from a sql.Rows iterator.
func scanPartnerRow(rows *sql.Rows) (*Partner, error) {
	var p Partner
	var didURI, publicKey, fingerprint, pushURL, pullURL, authToken sql.NullString
	var didDoc, didETag sql.NullString
	var didDocFetchedAt sql.NullInt64
	var canSupply, canReceive, enabled int

	err := rows.Scan(
		&p.ID, &didURI, &publicKey, &fingerprint, &pushURL, &pullURL, &authToken,
		&didDoc, &didDocFetchedAt, &didETag,
		&canSupply, &canReceive, &enabled, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	p.DIDURI = didURI.String
	p.PublicKey = publicKey.String
	p.PublicKeyFingerprint = fingerprint.String
	p.PushURL = pushURL.String
	p.PullURL = pullURL.String
	p.AuthToken = authToken.String
	p.DIDDocument = didDoc.String
	p.DIDDocumentFetchedAt = didDocFetchedAt.Int64
	p.DIDDocumentETag = didETag.String
	p.CanSupplyVouchers = canSupply != 0
	p.CanReceiveVouchers = canReceive != 0
	p.Enabled = enabled != 0

	return &p, nil
}

// fingerprintPEM computes the hex SHA-256 fingerprint of a PEM-encoded public key.
// This fingerprint matches FingerprintPublicKeyHex (CBOR-based).
func fingerprintPEM(pemStr string) (string, error) {
	pub, err := LoadPublicKeyFromPEM([]byte(pemStr))
	if err != nil {
		return "", err
	}
	fp := FingerprintPublicKeyHex(pub)
	if fp == "" {
		return "", fmt.Errorf("failed to compute fingerprint")
	}
	return fp, nil
}

// fingerprintRawKey computes a hex SHA-256 fingerprint from raw PEM bytes
// for quick comparison. Falls back to raw SHA-256 if PEM parsing fails.
func fingerprintRawKey(pemBytes []byte) string {
	pub, err := LoadPublicKeyFromPEM(pemBytes)
	if err != nil {
		// Fallback: hash the raw bytes
		h := sha256.Sum256(pemBytes)
		return hex.EncodeToString(h[:])
	}
	return FingerprintPublicKeyHex(pub)
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int64) interface{} {
	if n == 0 {
		return nil
	}
	return n
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
