// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
)

// runPullAuthCommand performs a PullAuth handshake against a remote Holder.
// Supports both standard owner-key authentication and delegate-based authentication.
func runPullAuthCommand() {
	fs := flag.NewFlagSet("pullauth", flag.ExitOnError)
	holderURL := fs.String("url", "", "Holder base URL (e.g., http://localhost:8083)")
	keyFile := fs.String("key", "", "PEM-encoded owner private key file (for non-delegate pull)")
	keyType := fs.String("key-type", "ec384", "Key type to generate if -key not provided (ec256, ec384, rsa2048)")
	ownerPubFile := fs.String("owner-pub", "", "PEM-encoded owner public key file (for delegate-based pull)")
	delegateKeyFile := fs.String("delegate-key", "", "PEM-encoded delegate private key file")
	delegateChainFile := fs.String("delegate-chain", "", "PEM-encoded delegate certificate chain file")
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	fs.Parse(os.Args[2:])

	if *holderURL == "" {
		fmt.Fprintf(os.Stderr, "error: -url is required\n")
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager pullauth -url <holder-url> [-key <key.pem>]\n")
		fmt.Fprintf(os.Stderr, "       fdo-voucher-manager pullauth -url <holder-url> -owner-pub <pub.pem> -delegate-key <key.pem> -delegate-chain <chain.pem>\n")
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	client := buildPullAuthClient(*holderURL, *keyFile, *keyType, *ownerPubFile, *delegateKeyFile, *delegateChainFile)

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
