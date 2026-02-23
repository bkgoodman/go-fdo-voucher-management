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
)

// VoucherTransmissionRecord represents a single voucher delivery attempt/destination
type VoucherTransmissionRecord struct {
	ID                  int64
	VoucherGUID         string
	FilePath            string
	DestinationURL      string
	AuthToken           string
	DestinationSource   string
	Mode                string
	Status              string
	Attempts            int
	LastError           string
	SerialNumber        string
	ModelNumber         string
	OwnerKeyFingerprint string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastAttemptAt       sql.NullTime
	DeliveredAt         sql.NullTime
	RetryAfter          sql.NullTime
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
			created_at,
			updated_at,
			retry_after
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
	defer rows.Close()

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

// ListTransmissions returns recent transmissions optionally filtered by status/guid
func (s *VoucherTransmissionStore) ListTransmissions(ctx context.Context, status string, guid string, limit int) ([]VoucherTransmissionRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
		serial_number, model_number, owner_key_fingerprint,
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
	defer rows.Close()

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

// FetchLatestByGUID returns the newest transmission for a GUID
func (s *VoucherTransmissionStore) FetchLatestByGUID(ctx context.Context, guid string) (*VoucherTransmissionRecord, error) {
	if guid == "" {
		return nil, fmt.Errorf("guid cannot be empty")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
		serial_number, model_number, owner_key_fingerprint,
		created_at, updated_at, last_attempt_at, delivered_at, retry_after
		FROM voucher_transmissions WHERE voucher_guid = ? ORDER BY created_at DESC LIMIT 1`, guid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if rows.Next() {
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

	return nil, sql.ErrNoRows
}

// FetchByID returns a transmission by its primary key
func (s *VoucherTransmissionStore) FetchByID(ctx context.Context, id int64) (*VoucherTransmissionRecord, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid transmission id %d", id)
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, voucher_guid, file_path, destination_url, auth_token, destination_source, mode, status, attempts, last_error,
		serial_number, model_number, owner_key_fingerprint,
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
	defer rows.Close()

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

// DeleteByID removes a transmission record
func (s *VoucherTransmissionStore) DeleteByID(ctx context.Context, id int64) error {
	if id == 0 {
		return fmt.Errorf("id cannot be zero")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM voucher_transmissions WHERE id = ?`, id)
	return err
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
