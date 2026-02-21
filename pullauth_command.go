// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/fido-device-onboard/go-fdo/did"
	"github.com/fido-device-onboard/go-fdo/transfer"
)

// runPullAuthCommand performs a PullAuth handshake against a remote Holder.
// It uses the local instance's DID-minted owner key to authenticate.
func runPullAuthCommand() {
	fs := flag.NewFlagSet("pullauth", flag.ExitOnError)
	holderURL := fs.String("url", "", "Holder base URL (e.g., http://localhost:8083)")
	keyFile := fs.String("key", "", "PEM-encoded private key file for authentication")
	keyType := fs.String("key-type", "ec384", "Key type to generate if -key not provided (ec256, ec384, rsa2048)")
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	fs.Parse(os.Args[2:])

	if *holderURL == "" {
		fmt.Fprintf(os.Stderr, "error: -url is required\n")
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager pullauth -url <holder-url> [-key <key.pem>]\n")
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Load or generate the owner key
	var ownerKey crypto.Signer
	if *keyFile != "" {
		signer, err := LoadPrivateKeyFromFile(*keyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading key: %v\n", err)
			os.Exit(1)
		}
		ownerKey = signer
	} else {
		// Generate an ephemeral key for testing
		keyCfg := did.DefaultKeyConfig()
		switch *keyType {
		case "ec256":
			keyCfg = did.KeyConfig{Type: "EC", Curve: "P-256"}
		case "ec384":
			keyCfg = did.KeyConfig{Type: "EC", Curve: "P-384"}
		case "rsa2048":
			keyCfg = did.KeyConfig{Type: "RSA", Bits: 2048}
		}
		result, err := did.Mint("localhost", "", "", keyCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error generating key: %v\n", err)
			os.Exit(1)
		}
		ownerKey = result.PrivateKey
		slog.Info("generated ephemeral owner key", "type", *keyType)
	}

	// Create PullAuth client
	client := &transfer.PullAuthClient{
		OwnerKey: ownerKey,
		BaseURL:  *holderURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	slog.Info("starting PullAuth handshake", "holder", *holderURL)

	// Perform the handshake
	result, err := client.Authenticate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "PullAuth failed: %v\n", err)
		os.Exit(1)
	}

	if *jsonOutput {
		out := map[string]interface{}{
			"status":                "authenticated",
			"session_token":         result.SessionToken,
			"token_expires_at":      result.TokenExpiresAt,
			"owner_key_fingerprint": fmt.Sprintf("%x", result.OwnerKeyFingerprint),
			"voucher_count":         result.VoucherCount,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
	} else {
		fmt.Printf("PullAuth succeeded!\n")
		fmt.Printf("  Session Token:    %s\n", result.SessionToken)
		fmt.Printf("  Expires At:       %d\n", result.TokenExpiresAt)
		fmt.Printf("  Key Fingerprint:  %x\n", result.OwnerKeyFingerprint)
		fmt.Printf("  Voucher Count:    %d\n", result.VoucherCount)
	}
}
