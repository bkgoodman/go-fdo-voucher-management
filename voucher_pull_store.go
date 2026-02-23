// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/fido-device-onboard/go-fdo"
	"github.com/fido-device-onboard/go-fdo/cbor"
	"github.com/fido-device-onboard/go-fdo/transfer"
)

// PullVoucherStore adapts the application's file-based voucher storage and
// SQLite transmission store to the transfer.VoucherStore interface, enabling
// the Pull API to serve vouchers that were received via push.
type PullVoucherStore struct {
	fileStore     *VoucherFileStore
	transmitStore *VoucherTransmissionStore
}

// NewPullVoucherStore creates a new adapter.
func NewPullVoucherStore(fileStore *VoucherFileStore, transmitStore *VoucherTransmissionStore) *PullVoucherStore {
	return &PullVoucherStore{
		fileStore:     fileStore,
		transmitStore: transmitStore,
	}
}

// Save persists a voucher (used by pull initiator to store downloaded vouchers).
func (s *PullVoucherStore) Save(ctx context.Context, data *transfer.VoucherData) (string, error) {
	if data == nil || data.Voucher == nil {
		return "", fmt.Errorf("voucher data is nil")
	}
	return s.fileStore.SaveVoucher(data.Voucher)
}

// Load retrieves a voucher by GUID from the file store.
func (s *PullVoucherStore) Load(_ context.Context, guid string) (*transfer.VoucherData, error) {
	path := s.fileStore.FilePathForGUID(guid)
	if path == "" {
		return nil, fmt.Errorf("voucher not found: %s", guid)
	}
	return s.loadVoucherFromFile(path, guid)
}

// GetVoucher retrieves a voucher by GUID, scoped to the given owner key.
// If ownerKeyFingerprint is non-nil, the voucher's DB record must match.
func (s *PullVoucherStore) GetVoucher(ctx context.Context, ownerKeyFingerprint []byte, guid string) (*transfer.VoucherData, error) {
	// Verify ownership via DB record if fingerprint is provided
	if ownerKeyFingerprint != nil {
		fpHex := hex.EncodeToString(ownerKeyFingerprint)
		rec, err := s.transmitStore.FetchLatestByGUID(ctx, guid)
		if err != nil {
			return nil, fmt.Errorf("voucher not found: %s", guid)
		}
		if rec.OwnerKeyFingerprint != fpHex {
			slog.Warn("pull store: owner key mismatch for voucher",
				"guid", guid,
				"expected", fpHex,
				"actual", rec.OwnerKeyFingerprint)
			return nil, fmt.Errorf("voucher not found: %s", guid)
		}
	}

	path := s.fileStore.FilePathForGUID(guid)
	if path == "" {
		return nil, fmt.Errorf("voucher not found: %s", guid)
	}
	return s.loadVoucherFromFile(path, guid)
}

// List returns voucher metadata from the transmission store, scoped to the
// authenticated owner key. Only vouchers whose owner_key_fingerprint matches
// the caller's authenticated key are returned.
func (s *PullVoucherStore) List(ctx context.Context, ownerKeyFingerprint []byte, filter transfer.ListFilter) (*transfer.VoucherListResponse, error) {
	// Use limit+1 to detect has_more
	queryLimit := filter.Limit
	if queryLimit <= 0 {
		queryLimit = 50
	}

	var records []VoucherTransmissionRecord
	var err error
	if ownerKeyFingerprint != nil {
		// Scope to the authenticated owner's vouchers only
		fpHex := hex.EncodeToString(ownerKeyFingerprint)
		records, err = s.transmitStore.ListByOwner(ctx, fpHex, queryLimit+1)
	} else {
		records, err = s.transmitStore.ListTransmissions(ctx, "", "", queryLimit+1)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list transmissions: %w", err)
	}

	// Apply since/until filters
	var filtered []VoucherTransmissionRecord
	for _, rec := range records {
		if filter.Since != nil && rec.CreatedAt.Before(*filter.Since) {
			continue
		}
		if filter.Until != nil && rec.CreatedAt.After(*filter.Until) {
			continue
		}
		filtered = append(filtered, rec)
	}

	hasMore := len(filtered) > queryLimit
	if hasMore {
		filtered = filtered[:queryLimit]
	}

	// Deduplicate by GUID (transmission store may have multiple records per voucher)
	seen := make(map[string]bool)
	var vouchers []transfer.VoucherInfo
	for _, rec := range filtered {
		if seen[rec.VoucherGUID] {
			continue
		}
		seen[rec.VoucherGUID] = true
		vouchers = append(vouchers, transfer.VoucherInfo{
			GUID:         rec.VoucherGUID,
			SerialNumber: rec.SerialNumber,
			ModelNumber:  rec.ModelNumber,
			CreatedAt:    rec.CreatedAt,
		})
	}

	// Build continuation token from the last record's created_at timestamp
	var continuation string
	if hasMore && len(filtered) > 0 {
		last := filtered[len(filtered)-1]
		continuation = last.CreatedAt.UTC().Format(time.RFC3339Nano)
	}

	return &transfer.VoucherListResponse{
		Vouchers:     vouchers,
		Continuation: continuation,
		HasMore:      hasMore,
		TotalCount:   uint(len(vouchers)),
	}, nil
}

// Delete removes a voucher file by GUID.
func (s *PullVoucherStore) Delete(_ context.Context, guid string) error {
	path := s.fileStore.FilePathForGUID(guid)
	if path == "" {
		return fmt.Errorf("voucher not found: %s", guid)
	}
	return os.Remove(path)
}

// loadVoucherFromFile reads and parses a PEM-encoded voucher file.
func (s *PullVoucherStore) loadVoucherFromFile(path, guid string) (*transfer.VoucherData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read voucher file %s: %w", path, err)
	}

	// Strip PEM headers
	content := string(data)
	content = strings.TrimPrefix(content, "-----BEGIN OWNERSHIP VOUCHER-----\n")
	content = strings.TrimSuffix(content, "-----END OWNERSHIP VOUCHER-----\n")
	content = strings.TrimSpace(content)

	raw, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("failed to decode voucher base64: %w", err)
	}

	var ov fdo.Voucher
	if err := cbor.Unmarshal(raw, &ov); err != nil {
		return nil, fmt.Errorf("failed to unmarshal voucher CBOR: %w", err)
	}

	return &transfer.VoucherData{
		VoucherInfo: transfer.VoucherInfo{
			GUID:         guid,
			SerialNumber: "", // not stored in voucher file
			DeviceInfo:   ov.Header.Val.DeviceInfo,
		},
		Voucher: &ov,
		Raw:     raw,
	}, nil
}
