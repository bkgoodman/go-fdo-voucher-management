// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// VoucherReceiverTokenManager manages authentication tokens for the voucher receiver
type VoucherReceiverTokenManager struct {
	db *sql.DB
}

// NewVoucherReceiverTokenManager creates a new token manager
func NewVoucherReceiverTokenManager(db *sql.DB) *VoucherReceiverTokenManager {
	return &VoucherReceiverTokenManager{db: db}
}

// Init ensures the schema for receiver tokens exists
func (m *VoucherReceiverTokenManager) Init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS voucher_receiver_tokens (
			token TEXT PRIMARY KEY,
			description TEXT,
			expires_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_receiver_tokens_expires ON voucher_receiver_tokens(expires_at)`,
		`CREATE TABLE IF NOT EXISTS voucher_receiver_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			guid BLOB NOT NULL,
			serial TEXT,
			model TEXT,
			manufacturer TEXT,
			source_ip TEXT,
			token_used TEXT,
			received_at INTEGER NOT NULL,
			file_size INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS idx_receiver_audit_guid ON voucher_receiver_audit(guid)`,
		`CREATE INDEX IF NOT EXISTS idx_receiver_audit_received ON voucher_receiver_audit(received_at)`,
	}

	for _, stmt := range stmts {
		if _, err := m.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to initialize receiver schema: %w", err)
		}
	}

	return nil
}

// AddReceiverToken adds a new authentication token to the database
func (m *VoucherReceiverTokenManager) AddReceiverToken(ctx context.Context, token, description string, expiresHours int) error {
	now := time.Now().UnixMicro()
	var expiresAt *int64

	if expiresHours > 0 {
		expiry := time.Now().Add(time.Duration(expiresHours) * time.Hour).UnixMicro()
		expiresAt = &expiry
	}

	_, err := m.db.ExecContext(ctx, `
		INSERT INTO voucher_receiver_tokens (token, description, expires_at, created_at)
		VALUES (?, ?, ?, ?)
	`, token, description, expiresAt, now)

	if err != nil {
		return fmt.Errorf("failed to add receiver token: %w", err)
	}

	return nil
}

// ValidateReceiverToken checks if a token exists and is not expired
func (m *VoucherReceiverTokenManager) ValidateReceiverToken(ctx context.Context, token string) (bool, error) {
	now := time.Now().UnixMicro()

	var count int
	err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM voucher_receiver_tokens
		WHERE token = ?
		AND (expires_at IS NULL OR expires_at > ?)
	`, token, now).Scan(&count)

	if err != nil {
		return false, fmt.Errorf("failed to validate token: %w", err)
	}

	return count > 0, nil
}

// DeleteReceiverToken removes a token from the database
func (m *VoucherReceiverTokenManager) DeleteReceiverToken(ctx context.Context, token string) error {
	result, err := m.db.ExecContext(ctx, `
		DELETE FROM voucher_receiver_tokens
		WHERE token = ?
	`, token)

	if err != nil {
		return fmt.Errorf("failed to delete receiver token: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("token not found")
	}

	return nil
}

// ReceiverTokenInfo contains information about a token
type ReceiverTokenInfo struct {
	Token       string
	Description string
	ExpiresAt   *time.Time
	CreatedAt   time.Time
	IsExpired   bool
}

// ListReceiverTokens returns all tokens with their information
func (m *VoucherReceiverTokenManager) ListReceiverTokens(ctx context.Context) ([]ReceiverTokenInfo, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT token, description, expires_at, created_at
		FROM voucher_receiver_tokens
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list tokens: %w", err)
	}
	defer rows.Close()

	var tokens []ReceiverTokenInfo
	now := time.Now()

	for rows.Next() {
		var token ReceiverTokenInfo
		var expiresAtMicro *int64
		var createdAtMicro int64

		err := rows.Scan(&token.Token, &token.Description, &expiresAtMicro, &createdAtMicro)
		if err != nil {
			return nil, fmt.Errorf("failed to scan token row: %w", err)
		}

		token.CreatedAt = time.UnixMicro(createdAtMicro)

		if expiresAtMicro != nil {
			expiresAt := time.UnixMicro(*expiresAtMicro)
			token.ExpiresAt = &expiresAt
			token.IsExpired = expiresAt.Before(now)
		}

		tokens = append(tokens, token)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating token rows: %w", err)
	}

	return tokens, nil
}

// LogReceivedVoucher logs a received voucher to the audit table
func (m *VoucherReceiverTokenManager) LogReceivedVoucher(ctx context.Context, guid []byte, serial, model, manufacturer, sourceIP, tokenUsed string, fileSize int64) error {
	now := time.Now().UnixMicro()

	_, err := m.db.ExecContext(ctx, `
		INSERT INTO voucher_receiver_audit (guid, serial, model, manufacturer, source_ip, token_used, received_at, file_size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, guid, serial, model, manufacturer, sourceIP, tokenUsed, now, fileSize)

	if err != nil {
		return fmt.Errorf("failed to log received voucher: %w", err)
	}

	return nil
}
