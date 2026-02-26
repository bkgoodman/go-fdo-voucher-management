// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fido-device-onboard/go-fdo/did"
)

// setupDIDMinting configures DID document generation and serving.
// It returns the owner crypto.Signer so callers (e.g., the pull service)
// can reuse the same key for holder authentication.
func setupDIDMinting(config *Config, mux *http.ServeMux, signingService *VoucherSigningService) crypto.Signer {
	if config.DIDMinting.Host == "" {
		// Auto-detect host from server address
		config.DIDMinting.Host = config.Server.Addr
		if config.Server.ExtAddr != "" {
			config.DIDMinting.Host = config.Server.ExtAddr
		}
	}

	// Determine the voucher recipient URL
	voucherRecipientURL := config.DIDMinting.VoucherRecipientURL
	if voucherRecipientURL == "" && config.VoucherReceiver.Enabled {
		// Auto-construct from server config
		scheme := "http"
		if config.Server.UseTLS {
			scheme = "https"
		}
		host := config.DIDMinting.Host
		endpoint := config.VoucherReceiver.Endpoint
		voucherRecipientURL = scheme + "://" + host + endpoint
	}

	// Determine the voucher holder URL (for pull endpoint discovery)
	voucherHolderURL := config.DIDMinting.VoucherHolderURL
	if voucherHolderURL == "" && config.PullService.Enabled {
		scheme := "http"
		if config.Server.UseTLS {
			scheme = "https"
		}
		host := config.DIDMinting.Host
		voucherHolderURL = scheme + "://" + host + "/api/v1/pull/vouchers"
	}

	// Load or generate the owner key
	ownerKey, generated, err := loadOrGenerateOwnerKey(config)
	if err != nil {
		slog.Error("DID minting: failed to load/generate owner key", "error", err)
		return nil
	}

	// If we just generated a new key and key_export_path is set, persist it
	if generated && config.DIDMinting.KeyExportPath != "" {
		if err := exportPrivateKey(ownerKey, config.DIDMinting.KeyExportPath); err != nil {
			slog.Error("DID minting: failed to export private key", "path", config.DIDMinting.KeyExportPath, "error", err)
		} else {
			slog.Info("DID minting: private key persisted", "path", config.DIDMinting.KeyExportPath)
		}
	}

	// Build DID document from the (loaded or generated) public key
	didURI := did.WebDID(config.DIDMinting.Host, config.DIDMinting.Path)
	doc, err := did.NewDocument(didURI, ownerKey.Public(), voucherRecipientURL, voucherHolderURL)
	if err != nil {
		slog.Error("DID minting: failed to create DID document", "error", err)
		return nil
	}

	if config.DIDMinting.ExportDIDURI {
		slog.Info("DID minting: DID URI generated",
			"did_uri", didURI,
			"voucher_recipient_url", voucherRecipientURL,
			"voucher_holder_url", voucherHolderURL,
		)

		docJSON, err := doc.JSON()
		if err == nil {
			slog.Debug("DID minting: DID Document", "document", string(docJSON))
		}
	}

	// Serve the DID Document
	if config.DIDMinting.ServeDIDDocument {
		handler, err := did.NewHandler(doc)
		if err != nil {
			slog.Error("DID minting: failed to create handler", "error", err)
			return nil
		}
		handler.RegisterHandlers(mux, config.DIDMinting.Path)
		slog.Info("DID minting: serving DID document",
			"well_known", "/.well-known/did.json",
			"path", config.DIDMinting.Path,
		)
	}

	// Set the signing service's OwnerSigner so voucher extension can sign entries
	if signingService != nil && signingService.OwnerSigner == nil {
		signingService.OwnerSigner = ownerKey
		slog.Info("DID minting: owner signer configured for voucher signing")
	}

	slog.Info("DID minting: setup complete",
		"did_uri", didURI,
		"serving", config.DIDMinting.ServeDIDDocument,
		"key_persistent", !generated || config.DIDMinting.KeyExportPath != "",
	)

	return ownerKey
}

// loadOrGenerateOwnerKey resolves the owner key using the following precedence:
//
//  1. import_key_file — Load a pre-existing private key from a PEM file.
//  2. first_time_init + key_export_path — If the export file exists, load it.
//     Otherwise generate a new key (caller will persist it).
//  3. Ephemeral fallback — Generate a fresh key each start (with a warning).
//
// Returns the key, whether it was freshly generated, and any error.
func loadOrGenerateOwnerKey(config *Config) (crypto.Signer, bool, error) {
	// Mode 1: Explicit import from a PEM file
	if config.KeyManagement.ImportKeyFile != "" {
		slog.Info("DID minting: loading owner key from import file", "path", config.KeyManagement.ImportKeyFile)
		key, err := LoadPrivateKeyFromFile(config.KeyManagement.ImportKeyFile)
		if err != nil {
			return nil, false, fmt.Errorf("failed to load import key %q: %w", config.KeyManagement.ImportKeyFile, err)
		}
		slog.Info("DID minting: owner key loaded from import file",
			"path", config.KeyManagement.ImportKeyFile,
			"fingerprint", FingerprintPublicKeyHex(key.Public()),
		)
		return key, false, nil
	}

	// Mode 2: First-time init with persistence via key_export_path
	if config.KeyManagement.FirstTimeInit && config.DIDMinting.KeyExportPath != "" {
		// Try to load an existing persisted key
		key, err := LoadPrivateKeyFromFile(config.DIDMinting.KeyExportPath)
		if err == nil {
			slog.Info("DID minting: owner key loaded from persistent storage",
				"path", config.DIDMinting.KeyExportPath,
				"fingerprint", FingerprintPublicKeyHex(key.Public()),
			)
			return key, false, nil
		}
		if !errors.Is(err, os.ErrNotExist) && !isFileNotFoundError(err) {
			return nil, false, fmt.Errorf("failed to load persisted key %q: %w", config.DIDMinting.KeyExportPath, err)
		}
		// File doesn't exist — generate a new key (caller will save it)
		slog.Info("DID minting: no persisted key found, generating new owner key",
			"path", config.DIDMinting.KeyExportPath,
		)
	}

	// Mode 3: Generate a new key (ephemeral unless caller persists it)
	if !config.KeyManagement.FirstTimeInit || config.DIDMinting.KeyExportPath == "" {
		slog.Warn("DID minting: generating EPHEMERAL owner key — key will NOT survive restart. " +
			"Set key_management.first_time_init=true and did_minting.key_export_path to enable persistence, " +
			"or set key_management.import_key_file to load a pre-generated key.")
	}

	keyCfg := did.DefaultKeyConfig()
	switch config.KeyManagement.KeyType {
	case "ec256":
		keyCfg = did.KeyConfig{Type: "EC", Curve: "P-256"}
	case "ec384":
		keyCfg = did.KeyConfig{Type: "EC", Curve: "P-384"}
	case "rsa2048":
		keyCfg = did.KeyConfig{Type: "RSA", Bits: 2048}
	case "rsa3072":
		keyCfg = did.KeyConfig{Type: "RSA", Bits: 3072}
	}

	result, err := did.Mint(
		config.DIDMinting.Host,
		config.DIDMinting.Path,
		"", "",
		keyCfg,
	)
	if err != nil {
		return nil, false, fmt.Errorf("failed to generate owner key: %w", err)
	}

	slog.Info("DID minting: new owner key generated",
		"key_type", keyCfg.Type,
		"fingerprint", FingerprintPublicKeyHex(result.PrivateKey.Public()),
	)
	return result.PrivateKey, true, nil
}

// isFileNotFoundError checks if an error chain contains a file-not-found condition.
// LoadPrivateKeyFromFile wraps os.ReadFile errors, so we unwrap to check.
func isFileNotFoundError(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return errors.Is(pathErr.Err, os.ErrNotExist)
	}
	return false
}

// exportPrivateKey saves a crypto.Signer's private key to a PEM file.
func exportPrivateKey(signer interface{}, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	derBytes, err := x509.MarshalPKCS8PrivateKey(signer)
	if err != nil {
		return err
	}

	pemBlock := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: derBytes,
	}

	return os.WriteFile(path, pem.EncodeToMemory(pemBlock), 0o600)
}
