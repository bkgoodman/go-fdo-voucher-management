// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"time"

	fdo "github.com/fido-device-onboard/go-fdo"
	"github.com/fido-device-onboard/go-fdo/cbor"
	"github.com/fido-device-onboard/go-fdo/protocol"
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
	// Generate random GUID (16 bytes)
	guid := make([]byte, 16)
	if _, err := rand.Read(guid); err != nil {
		return "", fmt.Errorf("failed to generate GUID: %w", err)
	}

	// Create device info string
	deviceInfo := fmt.Sprintf("serial=%s,model=%s", serial, model)

	// Generate the manufacturer key — a real EC P-256 key whose public half
	// goes into the voucher header so the receiver can parse and fingerprint it.
	mfgPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("failed to generate manufacturer key: %w", err)
	}

	// Build a protocol.PublicKey for the manufacturer key (X509 encoding).
	mfgPubKey, err := protocol.NewPublicKey(protocol.Secp256r1KeyType, &mfgPriv.PublicKey, false)
	if err != nil {
		return "", fmt.Errorf("failed to encode manufacturer key: %w", err)
	}

	// If an owner key file was provided, load its PEM-encoded public key.
	// Otherwise the manufacturer key doubles as the owner key (no entries).
	pubKey := mfgPubKey
	if ownerKeyFile != "" {
		keyData, err := os.ReadFile(ownerKeyFile)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("owner key file '%s' not found", ownerKeyFile)
			}
			return "", fmt.Errorf("failed to read owner key file: %w", err)
		}
		block, _ := pem.Decode(keyData)
		if block == nil {
			return "", fmt.Errorf("owner key file contains no PEM block")
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("failed to parse owner public key: %w", err)
		}
		keyType, err := protocol.KeyTypeFromPublicKey(parsed)
		if err != nil {
			return "", fmt.Errorf("unsupported owner key type: %w", err)
		}
		switch typedKey := parsed.(type) {
		case *ecdsa.PublicKey:
			pubKey, err = protocol.NewPublicKey(keyType, typedKey, false)
		case *rsa.PublicKey:
			pubKey, err = protocol.NewPublicKey(keyType, typedKey, false)
		default:
			return "", fmt.Errorf("unsupported owner key type: %T", parsed)
		}
		if err != nil {
			return "", fmt.Errorf("failed to encode owner key: %w", err)
		}
	}

	// CBOR-encode the public key as [type, encoding, body]
	pubKeyCBOR, err := cbor.Marshal(pubKey)
	if err != nil {
		return "", fmt.Errorf("failed to marshal public key: %w", err)
	}

	// Generate a self-signed device certificate using the manufacturer key.
	// ExtendVoucher requires CertChain to not be nil — it uses the device
	// cert's public key to select the hash algorithm.
	devCertTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fdo-test-device"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	devCertDER, err := x509.CreateCertificate(rand.Reader, devCertTemplate, devCertTemplate, &mfgPriv.PublicKey, mfgPriv)
	if err != nil {
		return "", fmt.Errorf("failed to create device certificate: %w", err)
	}
	// CBOR-encode the certificate chain as an array of CBOR byte strings.
	// Each certificate is DER-encoded, then wrapped in a CBOR byte string.
	certChainCBOR, err := cbor.Marshal([][]byte{devCertDER})
	if err != nil {
		return "", fmt.Errorf("failed to marshal cert chain: %w", err)
	}

	// Create minimal voucher header structure
	// VoucherHeader = [version, guid, rvinfo, deviceinfo, pubkey, cert_chain_hash]
	header := []interface{}{
		uint16(101),               // version
		guid,                      // GUID
		[]interface{}{},           // RvInfo (empty array)
		deviceInfo,                // DeviceInfo
		cbor.RawBytes(pubKeyCBOR), // ManufacturerKey (properly encoded)
		nil,                       // CertChainHash (null)
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
		uint16(101),                  // version
		headerBytes,                  // header (as byte string)
		hmacValue,                    // HMAC
		cbor.RawBytes(certChainCBOR), // CertChain (device certificate)
		[]interface{}{},              // Entries (empty array)
	}

	// Encode voucher to CBOR
	voucherBytes, err := cbor.Marshal(voucher)
	if err != nil {
		return "", fmt.Errorf("failed to marshal voucher: %w", err)
	}

	// Wrap in PEM format using the library function
	return string(fdo.FormatVoucherCBORToPEM(voucherBytes)), nil
}
