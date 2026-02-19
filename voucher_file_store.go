// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fido-device-onboard/go-fdo"
	"github.com/fido-device-onboard/go-fdo/cbor"
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

	contents, err := formatVoucherForDisk(ov)
	if err != nil {
		return "", fmt.Errorf("failed to format voucher for disk: %w", err)
	}

	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return "", fmt.Errorf("failed to write voucher file %s: %w", path, err)
	}

	return path, nil
}

// formatVoucherForDisk formats the voucher in PEM format
func formatVoucherForDisk(ov *fdo.Voucher) (string, error) {
	voucherBytes, err := cbor.Marshal(ov)
	if err != nil {
		return "", fmt.Errorf("failed to marshal voucher: %w", err)
	}

	voucherBase64 := base64.StdEncoding.EncodeToString(voucherBytes)

	var builder strings.Builder
	builder.WriteString("-----BEGIN OWNERSHIP VOUCHER-----\n")
	builder.WriteString(voucherBase64)
	builder.WriteString("\n")
	builder.WriteString("-----END OWNERSHIP VOUCHER-----\n")

	return builder.String(), nil
}
