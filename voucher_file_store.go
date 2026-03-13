// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fido-device-onboard/go-fdo"
)

// VoucherFileStore manages voucher artifacts saved to disk for later transmission
type VoucherFileStore struct {
	directory string
}

// NewVoucherFileStore constructs a store rooted at the configured directory
func NewVoucherFileStore(directory string) *VoucherFileStore {
	if directory == "" {
		directory = filepath.Join("data", "vouchers")
	}

	return &VoucherFileStore{directory: directory}
}

// Directory returns the backing directory for voucher files
func (s *VoucherFileStore) Directory() string {
	if s == nil {
		return ""
	}
	return s.directory
}

// FilePathForGUID resolves the expected path for a voucher GUID
func (s *VoucherFileStore) FilePathForGUID(guid string) string {
	if s == nil || s.directory == "" || guid == "" {
		return ""
	}

	guid = strings.ToLower(guid)
	return filepath.Join(s.directory, fmt.Sprintf("%s.fdoov", guid))
}

// SaveVoucher persists the provided voucher to disk using its GUID for the filename
func (s *VoucherFileStore) SaveVoucher(ov *fdo.Voucher) (string, error) {
	if s == nil || s.directory == "" {
		return "", fmt.Errorf("voucher file store not configured")
	}
	if ov == nil {
		return "", fmt.Errorf("voucher cannot be nil")
	}

	if err := os.MkdirAll(s.directory, 0o755); err != nil {
		return "", fmt.Errorf("failed to create voucher directory: %w", err)
	}

	guid := fmt.Sprintf("%x", ov.Header.Val.GUID[:])
	path := s.FilePathForGUID(guid)
	if path == "" {
		return "", fmt.Errorf("unable to derive voucher path for guid %s", guid)
	}

	contents, err := fdo.FormatVoucherPEM(ov)
	if err != nil {
		return "", fmt.Errorf("failed to format voucher for disk: %w", err)
	}

	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return "", fmt.Errorf("failed to write voucher file %s: %w", path, err)
	}

	return path, nil
}

// BackupVoucher creates a pre-assignment backup of the voucher file so it can
// be restored if the assignment is later reverted (unassign). The backup is
// stored alongside the original with a ".preassign" suffix.
func (s *VoucherFileStore) BackupVoucher(guid string) error {
	src := s.FilePathForGUID(guid)
	if src == "" {
		return fmt.Errorf("unable to derive voucher path for guid %s", guid)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read voucher for backup: %w", err)
	}

	dst := src + ".preassign"
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("failed to write voucher backup: %w", err)
	}

	return nil
}

// RestoreVoucher replaces the voucher file with its pre-assignment backup,
// effectively reverting the cryptographic extension that occurred during
// assignment. The backup file is removed after a successful restore.
func (s *VoucherFileStore) RestoreVoucher(guid string) error {
	path := s.FilePathForGUID(guid)
	if path == "" {
		return fmt.Errorf("unable to derive voucher path for guid %s", guid)
	}

	backup := path + ".preassign"
	data, err := os.ReadFile(backup)
	if err != nil {
		return fmt.Errorf("no pre-assignment backup found for guid %s: %w", guid, err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to restore voucher from backup: %w", err)
	}

	// Clean up the backup (best-effort; non-fatal if removal fails)
	_ = os.Remove(backup)

	return nil
}
