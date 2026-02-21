// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"log/slog"
	"net/http"

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
		)

		docJSON, err := result.DIDDocument.JSON()
		if err == nil {
			slog.Debug("DID minting: DID Document", "document", string(docJSON))
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
