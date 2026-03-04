// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
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

// runPullCommand performs a FDOKeyAuth handshake and then lists/downloads vouchers.
func runPullCommand() {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	holderURL := fs.String("url", "", "Holder base URL (e.g., http://localhost:8083)")
	keyFile := fs.String("key", "", "PEM-encoded owner private key file (for non-delegate pull)")
	keyType := fs.String("key-type", "ec384", "Key type to generate if -key not provided (ec256, ec384, rsa2048)")
	ownerPubFile := fs.String("owner-pub", "", "PEM-encoded owner public key file (for delegate-based pull)")
	delegateKeyFile := fs.String("delegate-key", "", "PEM-encoded delegate private key file")
	delegateChainFile := fs.String("delegate-chain", "", "PEM-encoded delegate certificate chain file")
	sinceStr := fs.String("since", "", "Return vouchers created after this time (ISO 8601 / RFC 3339)")
	untilStr := fs.String("until", "", "Return vouchers created before this time (ISO 8601 / RFC 3339)")
	continuation := fs.String("continuation", "", "Opaque continuation token from a previous pull response")
	limit := fs.Int("limit", 0, "Maximum vouchers to return per page (0 = server default)")
	outputDir := fs.String("output", "", "Directory to save downloaded voucher files (default: print metadata only)")
	jsonOutput := fs.Bool("json", false, "Output result as JSON")
	holderKeyFile := fs.String("holder-key", "", "PEM-encoded Holder public key file (for HolderSignature verification)")
	listOnly := fs.Bool("list", false, "List vouchers only (do not download)")
	fs.Parse(os.Args[2:])

	if *holderURL == "" {
		fmt.Fprintf(os.Stderr, "error: -url is required\n")
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager pull -url <holder-url> [-key <key.pem>] [-since <time>]\n")
		fmt.Fprintf(os.Stderr, "       fdo-voucher-manager pull -url <holder-url> -owner-pub <pub.pem> -delegate-key <key.pem> -delegate-chain <chain.pem>\n")
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	client := buildFDOKeyAuthClient(*holderURL, *keyFile, *keyType, *ownerPubFile, *delegateKeyFile, *delegateChainFile, *holderKeyFile)

	ctx := context.Background()

	// Step 1: Authenticate
	slog.Info("pull: authenticating", "holder", *holderURL)
	authResult, err := client.Authenticate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FDOKeyAuth failed: %v\n", err)
		os.Exit(1)
	}
	slog.Info("pull: authenticated",
		"voucher_count", authResult.VoucherCount,
		"token_expires", authResult.TokenExpiresAt,
	)

	// Build filter from CLI flags
	filter := transfer.ListFilter{
		Continuation: *continuation,
		Limit:        *limit,
	}
	if *sinceStr != "" {
		t, err := time.Parse(time.RFC3339, *sinceStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid -since time %q: %v\n", *sinceStr, err)
			os.Exit(1)
		}
		filter.Since = &t
	}
	if *untilStr != "" {
		t, err := time.Parse(time.RFC3339, *untilStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid -until time %q: %v\n", *untilStr, err)
			os.Exit(1)
		}
		filter.Until = &t
	}

	initiator := &transfer.HTTPPullInitiator{
		Auth: client,
	}

	// Step 2: List vouchers (with pagination)
	var allVouchers []transfer.VoucherInfo
	var lastContinuation string
	pageNum := 0
	for {
		pageNum++
		listResp, err := initiator.ListVouchers(ctx, authResult.SessionToken, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pull: list vouchers failed: %v\n", err)
			os.Exit(1)
		}
		allVouchers = append(allVouchers, listResp.Vouchers...)
		lastContinuation = listResp.Continuation

		slog.Info("pull: listed page",
			"page", pageNum,
			"vouchers_on_page", len(listResp.Vouchers),
			"has_more", listResp.HasMore,
			"total_so_far", len(allVouchers),
		)

		if listResp.Continuation == "" || !listResp.HasMore {
			break
		}
		filter.Continuation = listResp.Continuation
	}

	if *listOnly || *outputDir == "" {
		// Output listing
		if *jsonOutput {
			out := map[string]interface{}{
				"status":        "success",
				"voucher_count": len(allVouchers),
				"vouchers":      allVouchers,
				"continuation":  lastContinuation,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(out)
		} else {
			fmt.Printf("Pull completed: %d voucher(s) found\n", len(allVouchers))
			for _, v := range allVouchers {
				fmt.Printf("  GUID: %s  Serial: %s  Model: %s  Created: %s\n",
					v.GUID, v.SerialNumber, v.ModelNumber, v.CreatedAt.Format(time.RFC3339))
			}
			if lastContinuation != "" {
				fmt.Printf("\nContinuation token (use with -continuation on next pull):\n  %s\n", lastContinuation)
			}
		}
		return
	}

	// Step 3: Download each voucher
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating output directory: %v\n", err)
		os.Exit(1)
	}

	var downloaded int
	for _, vi := range allVouchers {
		data, err := initiator.DownloadVoucher(ctx, authResult.SessionToken, vi.GUID)
		if err != nil {
			slog.Error("pull: failed to download voucher", "guid", vi.GUID, "error", err)
			continue
		}

		filename := fmt.Sprintf("%s/%s.fdoov", *outputDir, vi.GUID)
		if err := os.WriteFile(filename, data.Raw, 0o644); err != nil {
			slog.Error("pull: failed to write voucher file", "guid", vi.GUID, "error", err)
			continue
		}
		downloaded++
		slog.Info("pull: downloaded voucher", "guid", vi.GUID, "path", filename)
	}

	if *jsonOutput {
		out := map[string]interface{}{
			"status":       "success",
			"listed":       len(allVouchers),
			"downloaded":   downloaded,
			"continuation": lastContinuation,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
	} else {
		fmt.Printf("Pull completed: %d listed, %d downloaded to %s\n", len(allVouchers), downloaded, *outputDir)
		if lastContinuation != "" {
			fmt.Printf("Continuation token: %s\n", lastContinuation)
		}
	}
}

// buildFDOKeyAuthClient constructs a FDOKeyAuthClient with either standard owner-key
// authentication or delegate-based authentication.
//
//nolint:gocyclo // CLI helper with multiple validation paths
func buildFDOKeyAuthClient(serverURL, keyFile, keyType, ownerPubFile, delegateKeyFile, delegateChainFile, serverKeyFile string) *transfer.FDOKeyAuthClient {
	client := &transfer.FDOKeyAuthClient{
		BaseURL: serverURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	if delegateKeyFile != "" || delegateChainFile != "" {
		// Delegate-based pull
		if delegateKeyFile == "" || delegateChainFile == "" {
			fmt.Fprintf(os.Stderr, "error: -delegate-key and -delegate-chain must both be set for delegate pull\n")
			os.Exit(1)
		}
		if ownerPubFile == "" && keyFile == "" {
			fmt.Fprintf(os.Stderr, "error: -owner-pub or -key is required for delegate pull\n")
			os.Exit(1)
		}

		if ownerPubFile != "" {
			ownerPub, err := LoadPublicKeyFromFile(ownerPubFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error loading owner public key: %v\n", err)
				os.Exit(1)
			}
			client.CallerPublicKey = ownerPub
		} else {
			ownerPriv, err := LoadPrivateKeyFromFile(keyFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error loading owner key: %v\n", err)
				os.Exit(1)
			}
			client.CallerKey = ownerPriv
		}

		delegateKey, err := LoadPrivateKeyFromFile(delegateKeyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading delegate key: %v\n", err)
			os.Exit(1)
		}
		client.DelegateKey = delegateKey

		delegateChain, err := loadCertChainFromFile(delegateChainFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading delegate chain: %v\n", err)
			os.Exit(1)
		}
		client.DelegateChain = delegateChain

		slog.Info("using delegate-based pull authentication",
			"delegate_chain_len", len(delegateChain),
			"has_owner_pub", ownerPubFile != "",
		)
	} else {
		// Standard pull: owner private key
		client.CallerKey = loadOrGenerateKey(keyFile, keyType)
	}

	// Load Holder public key for HolderSignature verification if provided
	if serverKeyFile != "" {
		serverPub, err := LoadPublicKeyFromFile(serverKeyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading server public key: %v\n", err)
			os.Exit(1)
		}
		client.ServerPublicKey = serverPub
		slog.Info("server signature verification enabled", "key_file", serverKeyFile)
	}

	return client
}

// loadOrGenerateKey loads a private key from file or generates an ephemeral one.
func loadOrGenerateKey(keyFile, keyType string) crypto.Signer {
	if keyFile != "" {
		signer, err := LoadPrivateKeyFromFile(keyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading key: %v\n", err)
			os.Exit(1)
		}
		return signer
	}

	keyCfg := did.DefaultKeyConfig()
	switch keyType {
	case "ec256":
		keyCfg = did.KeyConfig{Type: "EC", Curve: "P-256"}
	case "ec384":
		keyCfg = did.KeyConfig{Type: "EC", Curve: "P-384"}
	case "rsa2048":
		keyCfg = did.KeyConfig{Type: "RSA", Bits: 2048}
	}
	result, err := did.Mint("localhost", "", "", "", keyCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating key: %v\n", err)
		os.Exit(1)
	}
	slog.Info("generated ephemeral owner key", "type", keyType)
	return result.PrivateKey
}
