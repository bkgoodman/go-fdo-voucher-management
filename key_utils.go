// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/fido-device-onboard/go-fdo/did"
	"github.com/fido-device-onboard/go-fdo/protocol"
)

// NOTE: PEM loading functions delegate to the go-fdo library (did package).
// The library handles PUBLIC KEY, RSA PUBLIC KEY, CERTIFICATE, PRIVATE KEY,
// RSA PRIVATE KEY, and EC PRIVATE KEY PEM block types.

// FingerprintPublicKeyHex returns the hex-encoded FDO OwnerKeyFingerprint.
// The key is normalized to X509 encoding before CBOR-marshal and SHA-256 (spec §9.8).
func FingerprintPublicKeyHex(pub crypto.PublicKey) string {
	return did.FingerprintFDOHex(pub)
}

// FingerprintProtocolKey computes the FDO OwnerKeyFingerprint from a
// protocol.PublicKey, normalizing via crypto.PublicKey first. Returns nil on error.
func FingerprintProtocolKey(pub protocol.PublicKey) []byte {
	fp, err := did.FingerprintProtocolKey(pub)
	if err != nil {
		return nil
	}
	return fp
}

// LoadPublicKeyFromPEM loads a public key from PEM format.
func LoadPublicKeyFromPEM(pemData []byte) (crypto.PublicKey, error) {
	return did.LoadPublicKeyPEM(pemData)
}

// LoadPrivateKeyFromPEM loads a private key from PEM format.
func LoadPrivateKeyFromPEM(pemData []byte) (crypto.Signer, error) {
	return did.LoadPrivateKeyPEM(pemData)
}

// LoadPublicKeyFromFile loads a public key from a PEM file
func LoadPublicKeyFromFile(filename string) (crypto.PublicKey, error) {
	if filename == "" {
		return nil, fmt.Errorf("filename not specified")
	}

	pemData, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return LoadPublicKeyFromPEM(pemData)
}

// loadCertChainFromFile loads an X.509 certificate chain from a PEM file.
// Returns certificates in the order they appear in the file.
func loadCertChainFromFile(filename string) ([]*x509.Certificate, error) {
	pemData, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	var chain []*x509.Certificate
	rest := pemData
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse certificate: %w", err)
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("no certificates found in %s", filename)
	}
	return chain, nil
}

// LoadPrivateKeyFromFile loads a private key from a PEM file
func LoadPrivateKeyFromFile(filename string) (crypto.Signer, error) {
	if filename == "" {
		return nil, fmt.Errorf("filename not specified")
	}

	pemData, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return LoadPrivateKeyFromPEM(pemData)
}

// FingerprintStringHex returns the hex-encoded SHA-256 hash of a string.
// This is used to derive a consistent fingerprint for non-key-based identities
// (e.g., bearer token descriptions).
func FingerprintStringHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
