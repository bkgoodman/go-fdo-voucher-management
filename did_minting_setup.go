// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fido-device-onboard/go-fdo/did"
)

// setupDIDMinting configures DID document generation and serving.
func setupDIDMinting(config *Config, mux *http.ServeMux, signingService *VoucherSigningService) {
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

	// Determine key config from the service's key_management settings
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

	// Mint the DID
	result, err := did.Mint(
		config.DIDMinting.Host,
		config.DIDMinting.Path,
		voucherRecipientURL,
		voucherHolderURL,
		keyCfg,
	)
	if err != nil {
		slog.Error("DID minting: failed to mint DID", "error", err)
		return
	}

	if config.DIDMinting.ExportDIDURI {
		slog.Info("DID minting: DID URI generated",
			"did_uri", result.DIDURI,
			"key_type", keyCfg.Type,
			"voucher_recipient_url", voucherRecipientURL,
			"voucher_holder_url", voucherHolderURL,
		)

		docJSON, err := result.DIDDocument.JSON()
		if err == nil {
			slog.Debug("DID minting: DID Document", "document", string(docJSON))
		}
	}

	// Export the private key to a PEM file if configured.
	// This allows the `pull` command to authenticate using the same owner key.
	if config.DIDMinting.KeyExportPath != "" {
		if err := exportPrivateKey(result.PrivateKey, config.DIDMinting.KeyExportPath); err != nil {
			slog.Error("DID minting: failed to export private key", "path", config.DIDMinting.KeyExportPath, "error", err)
		} else {
			slog.Info("DID minting: private key exported", "path", config.DIDMinting.KeyExportPath)
		}
	}

	// Serve the DID Document
	if config.DIDMinting.ServeDIDDocument {
		handler, err := did.NewHandler(result.DIDDocument)
		if err != nil {
			slog.Error("DID minting: failed to create handler", "error", err)
			return
		}
		handler.RegisterHandlers(mux, config.DIDMinting.Path)
		slog.Info("DID minting: serving DID document",
			"well_known", "/.well-known/did.json",
			"path", config.DIDMinting.Path,
		)
	}

	// Set the signing service's OwnerSigner so voucher extension can sign entries
	if signingService != nil && signingService.OwnerSigner == nil {
		signingService.OwnerSigner = result.PrivateKey
		slog.Info("DID minting: owner signer configured for voucher signing")
	}

	slog.Info("DID minting: setup complete",
		"did_uri", result.DIDURI,
		"serving", config.DIDMinting.ServeDIDDocument,
	)
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
