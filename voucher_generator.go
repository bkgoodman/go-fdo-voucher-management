// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/fido-device-onboard/go-fdo/cbor"
)

// GenerateTestVoucher creates a minimal valid CBOR-encoded test voucher
// If ownerKeyFile is provided, the voucher is signed to that key
// Returns PEM-formatted voucher string
func GenerateTestVoucher(serial, model string) (string, error) {
	return GenerateTestVoucherWithOwner(serial, model, "")
}

// GenerateTestVoucherWithOwner creates a test voucher signed to a specific owner key
// ownerKeyFile should be a PEM-encoded public key file
// If empty, generates a random key
func GenerateTestVoucherWithOwner(serial, model, ownerKeyFile string) (string, error) {
	// Create a minimal voucher structure:
	// [version, header_bstr, hmac, cert_chain, entries]
	// For a test voucher, we use minimal valid values

	// Generate random GUID (16 bytes)
	guid := make([]byte, 16)
	if _, err := rand.Read(guid); err != nil {
		return "", fmt.Errorf("failed to generate GUID: %w", err)
	}

	// Create device info string
	deviceInfo := fmt.Sprintf("serial=%s,model=%s", serial, model)

	// Determine owner public key
	var ownerKeyBytes []byte
	if ownerKeyFile != "" {
		// Read owner key from file
		keyData, err := os.ReadFile(ownerKeyFile)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("owner key file '%s' not found", ownerKeyFile)
			}
			return "", fmt.Errorf("failed to read owner key file: %w", err)
		}
		ownerKeyBytes = keyData
	} else {
		// Generate random key placeholder
		ownerKeyBytes = make([]byte, 0)
	}

	// Create minimal voucher header structure
	// VoucherHeader = [version, guid, rvinfo, deviceinfo, pubkey, cert_chain_hash]
	// PublicKey = [type, encoding, body]
	header := []interface{}{
		uint16(101),                        // version
		guid,                               // GUID
		[]interface{}{},                    // RvInfo (empty array)
		deviceInfo,                         // DeviceInfo
		[]interface{}{1, 1, ownerKeyBytes}, // PublicKey (type, encoding, body)
		nil,                                // CertChainHash (null)
	}

	// Encode header to CBOR
	headerBytes, err := cbor.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %w", err)
	}

	// Create HMAC (minimal - just zeros for test)
	hmacValue := []interface{}{
		uint8(4),         // HMAC type (SHA256)
		make([]byte, 32), // HMAC value (zeros for test)
	}

	// Create minimal voucher structure
	// Voucher = [version, header_bstr, hmac, cert_chain, entries]
	voucher := []interface{}{
		uint16(101),     // version
		headerBytes,     // header (as byte string)
		hmacValue,       // HMAC
		nil,             // CertChain (null)
		[]interface{}{}, // Entries (empty array)
	}

	// Encode voucher to CBOR
	voucherBytes, err := cbor.Marshal(voucher)
	if err != nil {
		return "", fmt.Errorf("failed to marshal voucher: %w", err)
	}

	// Encode as base64
	base64Data := base64.StdEncoding.EncodeToString(voucherBytes)

	// Wrap in PEM format
	var pemBuilder strings.Builder
	pemBuilder.WriteString("-----BEGIN OWNERSHIP VOUCHER-----\n")
	for i := 0; i < len(base64Data); i += 64 {
		end := i + 64
		if end > len(base64Data) {
			end = len(base64Data)
		}
		pemBuilder.WriteString(base64Data[i:end])
		pemBuilder.WriteString("\n")
	}
	pemBuilder.WriteString("-----END OWNERSHIP VOUCHER-----\n")

	return pemBuilder.String(), nil
}
