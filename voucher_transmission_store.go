// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	transmissionStatusPending   = "pending"
	transmissionStatusSucceeded = "succeeded"
	transmissionStatusFailed    = "failed"
	transmissionStatusPermanent = "failed_permanent"
	transmissionStatusAssigned  = "assigned"
)

// VoucherTransmissionRecord represents a single voucher delivery attempt/destination
type VoucherTransmissionRecord struct {
	ID                       int64
	VoucherGUID              string
	FilePath                 string
	DestinationURL           string
	AuthToken                string
	DestinationSource        string
	Mode                     string
	Status                   string
	Attempts                 int
	LastError                string
	SerialNumber             string
	ModelNumber              string
	OwnerKeyFingerprint      string
	OriginalOwnerFingerprint string
	AssignedAt               sql.NullTime
	AssignedToFingerprint    string
	AssignedToDID            string
	AssignedByFingerprint    string
	CreatedAt                time.Time
	UpdatedAt                time.Time
	LastAttemptAt            sql.NullTime
	DeliveredAt              sql.NullTime
	RetryAfter               sql.NullTime
}

// VoucherTransmissionStore handles persistence of voucher push attempts
type VoucherTransmissionStore struct {
	db *sql.DB
}

// NewVoucherTransmissionStore constructs a store backed by the provided DB handle
func NewVoucherTransmissionStore(db *sql.DB) *VoucherTransmissionStore {
	return &VoucherTransmissionStore{db: db}
}

// Init ensures the schema for voucher transmissions exists
func (s *VoucherTransmissionStore) Init(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("transmission store not initialized")
	}

	// Create table (includes owner_key_fingerprint for new installs)
	createTable := `CREATE TABLE IF NOT EXISTS voucher_transmissions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		voucher_guid TEXT NOT NULL,
		file_path TEXT NOT NULL,
		destination_url TEXT,
		auth_token TEXT,
		destination_source TEXT,
		mode TEXT,
		status TEXT NOT NULL DEFAULT 'pending',
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT,
		serial_number TEXT,
		model_number TEXT,
		owner_key_fingerprint TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_attempt_at TIMESTAMP,
		delivered_at TIMESTAMP,
		retry_after TIMESTAMP
	)`
	if _, err := s.db.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("failed to initialize voucher_transmissions schema: %w", err)
	}

	// Migration: add owner_key_fingerprint column to existing tables that lack it.
	// Must run BEFORE creating the index on this column.
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE voucher_transmissions ADD COLUMN owner_key_fingerprint TEXT NOT NULL DEFAULT ''`)

	// Migration: add assignment columns
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE voucher_transmissions ADD COLUMN original_owner_fingerprint TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE voucher_transmissions ADD COLUMN assigned_at TIMESTAMP`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE voucher_transmissions ADD COLUMN assigned_to_fingerprint TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE voucher_transmissions ADD COLUMN assigned_to_did TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE voucher_transmissions ADD COLUMN assigned_by_fingerprint TEXT NOT NULL DEFAULT ''`)

	// Create indexes (safe to run after migration)
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_voucher_transmissions_status_retry ON voucher_transmissions(status, retry_after)`,
		`CREATE INDEX IF NOT EXISTS idx_voucher_transmissions_guid ON voucher_transmissions(voucher_guid)`,
		`CREATE INDEX IF NOT EXISTS idx_voucher_transmissions_destination ON voucher_transmissions(destination_url, status)`,
		`CREATE INDEX IF NOT EXISTS idx_voucher_transmissions_owner_key ON voucher_transmissions(owner_key_fingerprint)`,
	}

	for _, stmt := range indexes {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to initialize voucher_transmissions schema: %w", err)
		}
	}

	// Access grants table: tracks multi-party access to vouchers
	accessGrantsTable := `CREATE TABLE IF NOT EXISTS voucher_access_grants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		voucher_guid TEXT NOT NULL,
		serial_number TEXT NOT NULL DEFAULT '',
		identity_fingerprint TEXT NOT NULL,
		identity_type TEXT NOT NULL,
		access_level TEXT NOT NULL DEFAULT 'full',
		partner_id TEXT NOT NULL DEFAULT '',
		granted_at INTEGER NOT NULL,
		granted_by TEXT NOT NULL DEFAULT '',
		UNIQUE(voucher_guid, identity_fingerprint, identity_type)
	)`
	if _, err := s.db.ExecContext(ctx, accessGrantsTable); err != nil {
		return fmt.Errorf("failed to initialize voucher_access_grants schema: %w", err)
	}

	accessGrantIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_vag_fingerprint ON voucher_access_grants(identity_fingerprint)`,
		`CREATE INDEX IF NOT EXISTS idx_vag_guid ON voucher_access_grants(voucher_guid)`,
		`CREATE INDEX IF NOT EXISTS idx_vag_type ON voucher_access_grants(identity_type)`,
	}
	for _, stmt := range accessGrantIndexes {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create access grant index: %w", err)
		}
	}

	return nil
}

// CreatePending enqueues a voucher for delivery to the specified destination
func (s *VoucherTransmissionStore) CreatePending(ctx context.Context, record *VoucherTransmissionRecord) (int64, error) {
	if record == nil {
		return 0, fmt.Errorf("record cannot be nil")
	}
	if record.Status == "" {
		record.Status = transmissionStatusPending
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO voucher_transmissions (
			voucher_guid,
			file_path,
			destination_url,
			auth_token,
			destination_source,
			mode,
			status,
			attempts,
			last_error,
			serial_number,
			model_number,
			owner_key_fingerprint,
			original_owner_fingerprint,
			assigned_at,
			assigned_to_fingerprint,
			assigned_to_did,
			assigned_by_fingerprint,
			created_at,
			updated_at,
			retry_after
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.VoucherGUID,
		record.FilePath,
		record.DestinationURL,
		record.AuthToken,
		record.DestinationSource,
		record.Mode,
		record.Status,
		record.Attempts,
		record.LastError,
		record.SerialNumber,
		record.ModelNumber,
		record.OwnerKeyFingerprint,
		record.OriginalOwnerFingerprint,
		nullTimeValue(record.AssignedAt),
		record.AssignedToFingerprint,
		record.AssignedToDID,
		record.AssignedByFingerprint,
		record.CreatedAt.UTC(),
		record.UpdatedAt.UTC(),
		nullTimeValue(record.RetryAfter),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert voucher transmission: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	return id, nil
}

// MarkAttempt updates a transmission after an attempt, including retry scheduling
func (s *VoucherTransmissionStore) MarkAttempt(ctx context.Context, id int64, status string, attempts int, retryAfter time.Time, lastError string, delivered bool) error {
	if status == "" {
		status = transmissionStatusPending
	}

	now := time.Now().UTC()
	params := []any{status, attempts, lastError, now, nullTime(retryAfter), now}
	query := `UPDATE voucher_transmissions
		SET status = ?, attempts = ?, last_error = ?, updated_at = ?, retry_after = ?, last_attempt_at = ?`

	if delivered {
		query += `, delivered_at = ?`
		params = append(params, now)
	}

	query += ` WHERE id = ?`
	params = append(params, id)

	if _, err := s.db.ExecContext(ctx, query, params...); err != nil {
		return fmt.Errorf("failed to update voucher transmission %d: %w", id, err)
	}
	return nil
}

// PendingForRetry returns transmissions ready for retry
func (s *VoucherTransmissionStore) PendingForRetry(ctx context.Context, limit int) ([]VoucherTransmissionRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
			serial_number, model_number, owner_key_fingerprint,
			original_owner_fingerprint, assigned_at, assigned_to_fingerprint, assigned_to_did, assigned_by_fingerprint,
			created_at, updated_at, last_attempt_at, delivered_at, retry_after
			FROM voucher_transmissions
			WHERE status = ? AND (retry_after IS NULL OR retry_after <= ?)
			ORDER BY retry_after ASC, created_at ASC
			LIMIT ?`,
		transmissionStatusPending, time.Now().UTC(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending voucher transmissions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanTransmissionRows(rows)
}

// ListTransmissions returns recent transmissions optionally filtered by status/guid
func (s *VoucherTransmissionStore) ListTransmissions(ctx context.Context, status string, guid string, limit int) ([]VoucherTransmissionRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
		serial_number, model_number, owner_key_fingerprint,
		original_owner_fingerprint, assigned_at, assigned_to_fingerprint, assigned_to_did, assigned_by_fingerprint,
		created_at, updated_at, last_attempt_at, delivered_at, retry_after
		FROM voucher_transmissions`
	var conditions []string
	var args []any
	if status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, status)
	}
	if guid != "" {
		conditions = append(conditions, "voucher_guid = ?")
		args = append(args, guid)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return s.scanTransmissionRows(rows)
}

// FetchLatestByGUID returns the newest transmission for a GUID
func (s *VoucherTransmissionStore) FetchLatestByGUID(ctx context.Context, guid string) (*VoucherTransmissionRecord, error) {
	if guid == "" {
		return nil, fmt.Errorf("guid cannot be empty")
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
		serial_number, model_number, owner_key_fingerprint,
		original_owner_fingerprint, assigned_at, assigned_to_fingerprint, assigned_to_did, assigned_by_fingerprint,
		created_at, updated_at, last_attempt_at, delivered_at, retry_after
		FROM voucher_transmissions WHERE voucher_guid = ? ORDER BY created_at DESC LIMIT 1`, guid)
	var rec VoucherTransmissionRecord
	if err := row.Scan(
		&rec.ID,
		&rec.VoucherGUID,
		&rec.FilePath,
		&rec.DestinationURL,
		&rec.AuthToken,
		&rec.DestinationSource,
		&rec.Mode,
		&rec.Status,
		&rec.Attempts,
		&rec.LastError,
		&rec.SerialNumber,
		&rec.ModelNumber,
		&rec.OwnerKeyFingerprint,
		&rec.OriginalOwnerFingerprint,
		&rec.AssignedAt,
		&rec.AssignedToFingerprint,
		&rec.AssignedToDID,
		&rec.AssignedByFingerprint,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&rec.LastAttemptAt,
		&rec.DeliveredAt,
		&rec.RetryAfter,
	); err != nil {
		return nil, err
	}
	return &rec, nil
}

// FetchByID returns a transmission by its primary key
func (s *VoucherTransmissionStore) FetchByID(ctx context.Context, id int64) (*VoucherTransmissionRecord, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid transmission id %d", id)
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
		serial_number, model_number, owner_key_fingerprint,
		original_owner_fingerprint, assigned_at, assigned_to_fingerprint, assigned_to_did, assigned_by_fingerprint,
		created_at, updated_at, last_attempt_at, delivered_at, retry_after
		FROM voucher_transmissions WHERE id = ?`, id)
	var rec VoucherTransmissionRecord
	if err := row.Scan(
		&rec.ID,
		&rec.VoucherGUID,
		&rec.FilePath,
		&rec.DestinationURL,
		&rec.AuthToken,
		&rec.DestinationSource,
		&rec.Mode,
		&rec.Status,
		&rec.Attempts,
		&rec.LastError,
		&rec.SerialNumber,
		&rec.ModelNumber,
		&rec.OwnerKeyFingerprint,
		&rec.OriginalOwnerFingerprint,
		&rec.AssignedAt,
		&rec.AssignedToFingerprint,
		&rec.AssignedToDID,
		&rec.AssignedByFingerprint,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&rec.LastAttemptAt,
		&rec.DeliveredAt,
		&rec.RetryAfter,
	); err != nil {
		return nil, err
	}
	return &rec, nil
}

// ListByOwner returns transmissions for a specific owner key fingerprint.
func (s *VoucherTransmissionStore) ListByOwner(ctx context.Context, ownerKeyFingerprint string, limit int) ([]VoucherTransmissionRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
			serial_number, model_number, owner_key_fingerprint,
			original_owner_fingerprint, assigned_at, assigned_to_fingerprint, assigned_to_did, assigned_by_fingerprint,
			created_at, updated_at, last_attempt_at, delivered_at, retry_after
			FROM voucher_transmissions
			WHERE owner_key_fingerprint = ?
			ORDER BY created_at DESC
			LIMIT ?`,
		ownerKeyFingerprint, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query transmissions by owner: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanTransmissionRows(rows)
}

// DeleteByID removes a transmission record
func (s *VoucherTransmissionStore) DeleteByID(ctx context.Context, id int64) error {
	if id == 0 {
		return fmt.Errorf("id cannot be zero")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM voucher_transmissions WHERE id = ?`, id)
	return err
}

// scanTransmissionRows scans rows from a voucher_transmissions query into records.
func (s *VoucherTransmissionStore) scanTransmissionRows(rows *sql.Rows) ([]VoucherTransmissionRecord, error) {
	var records []VoucherTransmissionRecord
	for rows.Next() {
		var rec VoucherTransmissionRecord
		if err := rows.Scan(
			&rec.ID,
			&rec.VoucherGUID,
			&rec.FilePath,
			&rec.DestinationURL,
			&rec.AuthToken,
			&rec.DestinationSource,
			&rec.Mode,
			&rec.Status,
			&rec.Attempts,
			&rec.LastError,
			&rec.SerialNumber,
			&rec.ModelNumber,
			&rec.OwnerKeyFingerprint,
			&rec.OriginalOwnerFingerprint,
			&rec.AssignedAt,
			&rec.AssignedToFingerprint,
			&rec.AssignedToDID,
			&rec.AssignedByFingerprint,
			&rec.CreatedAt,
			&rec.UpdatedAt,
			&rec.LastAttemptAt,
			&rec.DeliveredAt,
			&rec.RetryAfter,
		); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

// AccessGrant represents a grant giving an identity access to a voucher.
type AccessGrant struct {
	ID                  int64
	VoucherGUID         string
	SerialNumber        string
	IdentityFingerprint string
	IdentityType        string // "owner_key", "purchaser_token", "custodian"
	AccessLevel         string // "full", "status_only"
	PartnerID           string
	GrantedAt           time.Time
	GrantedBy           string
}

// InsertAccessGrant creates a new access grant. Uses INSERT OR IGNORE to avoid
// duplicates on (voucher_guid, identity_fingerprint, identity_type).
func (s *VoucherTransmissionStore) InsertAccessGrant(ctx context.Context, grant *AccessGrant) error {
	if grant.GrantedAt.IsZero() {
		grant.GrantedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO voucher_access_grants
		(voucher_guid, serial_number, identity_fingerprint, identity_type, access_level, partner_id, granted_at, granted_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		grant.VoucherGUID, grant.SerialNumber, grant.IdentityFingerprint, grant.IdentityType,
		grant.AccessLevel, grant.PartnerID, grant.GrantedAt.UnixMicro(), grant.GrantedBy,
	)
	if err != nil {
		return fmt.Errorf("failed to insert access grant: %w", err)
	}
	return nil
}

// HasAccess checks whether an identity has any access grant for the given voucher.
func (s *VoucherTransmissionStore) HasAccess(ctx context.Context, voucherGUID, identityFingerprint string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM voucher_access_grants
		WHERE voucher_guid = ? AND identity_fingerprint = ?`,
		voucherGUID, identityFingerprint,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check access grant: %w", err)
	}
	return count > 0, nil
}

// ListAccessGrants returns all access grants for a voucher.
func (s *VoucherTransmissionStore) ListAccessGrants(ctx context.Context, voucherGUID string) ([]AccessGrant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, voucher_guid, serial_number, identity_fingerprint, identity_type, access_level, partner_id, granted_at, granted_by
		FROM voucher_access_grants WHERE voucher_guid = ?
		ORDER BY granted_at ASC`, voucherGUID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list access grants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var grants []AccessGrant
	for rows.Next() {
		var g AccessGrant
		var grantedAtMicro int64
		if err := rows.Scan(&g.ID, &g.VoucherGUID, &g.SerialNumber, &g.IdentityFingerprint,
			&g.IdentityType, &g.AccessLevel, &g.PartnerID, &grantedAtMicro, &g.GrantedBy); err != nil {
			return nil, err
		}
		g.GrantedAt = time.UnixMicro(grantedAtMicro)
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// ListByAccessGrant returns transmission records where the given fingerprint
// has an access grant, in addition to records where owner_key_fingerprint matches directly.
func (s *VoucherTransmissionStore) ListByAccessGrant(ctx context.Context, identityFingerprint string, limit int) ([]VoucherTransmissionRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT t.id, t.voucher_guid, t.file_path, t.destination_url, t.auth_token, t.destination_source, t.mode, t.status, t.attempts, t.last_error,
			t.serial_number, t.model_number, t.owner_key_fingerprint,
			t.original_owner_fingerprint, t.assigned_at, t.assigned_to_fingerprint, t.assigned_to_did, t.assigned_by_fingerprint,
			t.created_at, t.updated_at, t.last_attempt_at, t.delivered_at, t.retry_after
		FROM voucher_transmissions t
		LEFT JOIN voucher_access_grants g ON t.voucher_guid = g.voucher_guid
		WHERE t.owner_key_fingerprint = ? OR g.identity_fingerprint = ?
		ORDER BY t.created_at DESC
		LIMIT ?`,
		identityFingerprint, identityFingerprint, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query transmissions by access grant: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanTransmissionRows(rows)
}

// HasAccessByGUID checks if an identity has access to a specific voucher
// either through direct ownership or an access grant.
func (s *VoucherTransmissionStore) HasAccessByGUID(ctx context.Context, voucherGUID, identityFingerprint string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM voucher_transmissions WHERE voucher_guid = ? AND owner_key_fingerprint = ?
			UNION
			SELECT 1 FROM voucher_access_grants WHERE voucher_guid = ? AND identity_fingerprint = ?
		)`,
		voucherGUID, identityFingerprint, voucherGUID, identityFingerprint,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check access: %w", err)
	}
	return count > 0, nil
}

// FetchBySerial returns transmission records matching a serial number.
func (s *VoucherTransmissionStore) FetchBySerial(ctx context.Context, serial string) ([]VoucherTransmissionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
			serial_number, model_number, owner_key_fingerprint,
			original_owner_fingerprint, assigned_at, assigned_to_fingerprint, assigned_to_did, assigned_by_fingerprint,
			created_at, updated_at, last_attempt_at, delivered_at, retry_after
		FROM voucher_transmissions WHERE serial_number = ?
		ORDER BY created_at DESC`, serial,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query transmissions by serial: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanTransmissionRows(rows)
}

// MarkAssigned updates a transmission record to assigned status.
func (s *VoucherTransmissionStore) MarkAssigned(ctx context.Context, id int64, newOwnerFingerprint, newOwnerDID, assignerFingerprint string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE voucher_transmissions
		SET status = ?, assigned_at = ?, assigned_to_fingerprint = ?, assigned_to_did = ?,
			assigned_by_fingerprint = ?, updated_at = ?
		WHERE id = ?`,
		transmissionStatusAssigned, now, newOwnerFingerprint, newOwnerDID,
		assignerFingerprint, now, id,
	)
	if err != nil {
		return fmt.Errorf("failed to mark transmission as assigned: %w", err)
	}
	return nil
}

// MarkUnassigned reverts a previously assigned voucher back to a non-assigned state.
// It clears the assignment metadata and restores the status to the given fallback
// (typically "pending" or "no_destination" depending on whether a destination exists).
func (s *VoucherTransmissionStore) MarkUnassigned(ctx context.Context, id int64, restoreStatus string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE voucher_transmissions
		SET status = ?, assigned_at = NULL, assigned_to_fingerprint = '', assigned_to_did = '',
			assigned_by_fingerprint = '', updated_at = ?
		WHERE id = ?`,
		restoreStatus, now, id,
	)
	if err != nil {
		return fmt.Errorf("failed to mark transmission as unassigned: %w", err)
	}
	return nil
}

// RemoveAccessGrantsForVoucher removes all access grants for a specific voucher GUID.
func (s *VoucherTransmissionStore) RemoveAccessGrantsForVoucher(ctx context.Context, voucherGUID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM voucher_access_grants WHERE voucher_guid = ?`, voucherGUID)
	if err != nil {
		return fmt.Errorf("failed to remove access grants: %w", err)
	}
	return nil
}

// ListAllAccessGrants returns all access grants, optionally filtered by identity type.
func (s *VoucherTransmissionStore) ListAllAccessGrants(ctx context.Context, identityType string, limit int) ([]AccessGrant, error) {
	if limit <= 0 {
		limit = 100
	}
	var query string
	var args []any
	if identityType != "" {
		query = `SELECT id, voucher_guid, serial_number, identity_fingerprint, identity_type, access_level, partner_id, granted_at, granted_by
			FROM voucher_access_grants WHERE identity_type = ?
			ORDER BY granted_at DESC LIMIT ?`
		args = []any{identityType, limit}
	} else {
		query = `SELECT id, voucher_guid, serial_number, identity_fingerprint, identity_type, access_level, partner_id, granted_at, granted_by
			FROM voucher_access_grants
			ORDER BY granted_at DESC LIMIT ?`
		args = []any{limit}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list access grants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var grants []AccessGrant
	for rows.Next() {
		var g AccessGrant
		var grantedAtMicro int64
		if err := rows.Scan(&g.ID, &g.VoucherGUID, &g.SerialNumber, &g.IdentityFingerprint,
			&g.IdentityType, &g.AccessLevel, &g.PartnerID, &grantedAtMicro, &g.GrantedBy); err != nil {
			return nil, err
		}
		g.GrantedAt = time.UnixMicro(grantedAtMicro)
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// CustodianSummary is a summary of a custodian's voucher assignments.
type CustodianSummary struct {
	Fingerprint  string
	VoucherCount int
	SerialList   string // comma-separated first few serials
}

// ListCustodians returns distinct custodians with voucher counts.
func (s *VoucherTransmissionStore) ListCustodians(ctx context.Context, limit int) ([]CustodianSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.identity_fingerprint,
			COUNT(DISTINCT g.voucher_guid) as voucher_count,
			GROUP_CONCAT(DISTINCT CASE WHEN t.serial_number != '' THEN t.serial_number END) as serials
		FROM voucher_access_grants g
		LEFT JOIN voucher_transmissions t ON g.voucher_guid = t.voucher_guid
		WHERE g.identity_type = 'custodian'
		GROUP BY g.identity_fingerprint
		ORDER BY voucher_count DESC
		LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list custodians: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []CustodianSummary
	for rows.Next() {
		var s CustodianSummary
		var serials sql.NullString
		if err := rows.Scan(&s.Fingerprint, &s.VoucherCount, &serials); err != nil {
			return nil, err
		}
		if serials.Valid {
			s.SerialList = serials.String
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

// ListByCustodian returns voucher transmissions assigned by a specific custodian.
func (s *VoucherTransmissionStore) ListByCustodian(ctx context.Context, custodianFingerprint string, limit int) ([]VoucherTransmissionRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT t.id, t.voucher_guid, t.file_path, t.destination_url, t.auth_token, t.destination_source, t.mode, t.status, t.attempts, t.last_error,
			t.serial_number, t.model_number, t.owner_key_fingerprint,
			t.original_owner_fingerprint, t.assigned_at, t.assigned_to_fingerprint, t.assigned_to_did, t.assigned_by_fingerprint,
			t.created_at, t.updated_at, t.last_attempt_at, t.delivered_at, t.retry_after
		FROM voucher_transmissions t
		JOIN voucher_access_grants g ON t.voucher_guid = g.voucher_guid
		WHERE g.identity_fingerprint = ? AND g.identity_type = 'custodian'
		ORDER BY t.created_at DESC
		LIMIT ?`,
		custodianFingerprint, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query transmissions by custodian: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanTransmissionRows(rows)
}

func nullTimeValue(t sql.NullTime) any {
	if t.Valid {
		return t.Time
	}
	return nil
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
