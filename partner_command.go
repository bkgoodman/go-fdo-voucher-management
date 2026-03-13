// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// runPartnersCommand dispatches partner subcommands.
func runPartnersCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager partners [add|list|show|remove|export] [options]\n")
		os.Exit(1)
	}

	command := os.Args[2]

	switch command {
	case "add":
		partnersAddCmd()
	case "list":
		partnersListCmd()
	case "show":
		partnersShowCmd()
	case "remove":
		partnersRemoveCmd()
	case "export":
		partnersExportCmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown partners command: %s\n", command)
		os.Exit(1)
	}
}

func partnersAddCmd() {
	fs := flag.NewFlagSet("partners add", flag.ExitOnError)
	id := fs.String("id", "", "Partner ID (required, e.g., 'acme-mfg')")
	supply := fs.Bool("supply", false, "Partner can supply vouchers to us (upstream)")
	receive := fs.Bool("receive", false, "We can push vouchers to this partner (downstream)")
	didURI := fs.String("did", "", "DID URI (did:web:... or did:key:...)")
	keyFile := fs.String("key", "", "PEM-encoded public key file")
	pushURL := fs.String("push-url", "", "FDOVoucherRecipient push URL")
	pullURL := fs.String("pull-url", "", "FDOVoucherHolder pull URL")
	authToken := fs.String("auth-token", "", "Bearer token for push")
	disabled := fs.Bool("disabled", false, "Add in disabled state")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	_ = fs.Parse(os.Args[3:])

	if *id == "" {
		fmt.Fprintf(os.Stderr, "error: -id is required\n")
		os.Exit(1)
	}

	if !*supply && !*receive {
		fmt.Fprintf(os.Stderr, "error: at least one of -supply or -receive is required\n")
		os.Exit(1)
	}

	// Must have at least one identity or endpoint
	if *didURI == "" && *keyFile == "" && *pushURL == "" {
		fmt.Fprintf(os.Stderr, "error: at least one of -did, -key, or -push-url is required\n")
		os.Exit(1)
	}

	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	db, err := openDatabase(config.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	store := NewPartnerStore(db)
	if err := store.Init(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize partner store: %v\n", err)
		os.Exit(1)
	}

	p := &Partner{
		ID:                 *id,
		CanSupplyVouchers:  *supply,
		CanReceiveVouchers: *receive,
		DIDURI:             *didURI,
		PushURL:            *pushURL,
		PullURL:            *pullURL,
		AuthToken:          *authToken,
		Enabled:            !*disabled,
	}

	// Load public key from file if provided
	if *keyFile != "" {
		pemData, err := os.ReadFile(*keyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading key file: %v\n", err)
			os.Exit(1)
		}
		// Validate the key can be parsed
		if _, err := LoadPublicKeyFromPEM(pemData); err != nil {
			fmt.Fprintf(os.Stderr, "error parsing public key: %v\n", err)
			os.Exit(1)
		}
		p.PublicKey = string(pemData)
	}

	// If DID is provided but no key, try to resolve it
	if *didURI != "" && p.PublicKey == "" {
		didEnabled := config.DIDCache.Enabled || config.DIDPush.Enabled || config.OwnerSignover.StaticDID != ""
		resolver := NewDIDResolver(nil, didEnabled || true) // always enable for CLI
		if !config.Server.UseTLS {
			resolver.SetInsecureHTTP(true)
		}
		key, recipientURL, resolveErr := resolver.ResolveDIDKey(context.Background(), *didURI)
		if resolveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not resolve DID %q: %v (adding without key)\n", *didURI, resolveErr)
		} else {
			pemBytes, pemErr := marshalPublicKeyPEM(key)
			if pemErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not encode resolved key: %v\n", pemErr)
			} else {
				p.PublicKey = string(pemBytes)
			}
			if p.PushURL == "" && recipientURL != "" {
				p.PushURL = recipientURL
			}
		}
	}

	if err := store.Add(context.Background(), p); err != nil {
		fmt.Fprintf(os.Stderr, "failed to add partner: %v\n", err)
		os.Exit(1)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(p)
	} else {
		fmt.Printf("Partner %q added successfully\n", *id)
		fmt.Printf("  Supply:      %v\n", p.CanSupplyVouchers)
		fmt.Printf("  Receive:     %v\n", p.CanReceiveVouchers)
		if p.DIDURI != "" {
			fmt.Printf("  DID:         %s\n", p.DIDURI)
		}
		if p.PublicKeyFingerprint != "" {
			fmt.Printf("  Fingerprint: %s\n", p.PublicKeyFingerprint)
		}
		if p.PushURL != "" {
			fmt.Printf("  Push URL:    %s\n", p.PushURL)
		}
		if p.PullURL != "" {
			fmt.Printf("  Pull URL:    %s\n", p.PullURL)
		}
		fmt.Printf("  Enabled:     %v\n", p.Enabled)
	}
}

func partnersListCmd() {
	fs := flag.NewFlagSet("partners list", flag.ExitOnError)
	filter := fs.String("filter", "", "Filter by capability: supply, receive (empty = all)")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	_ = fs.Parse(os.Args[3:])

	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	db, err := openDatabase(config.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	store := NewPartnerStore(db)
	if err := store.Init(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize partner store: %v\n", err)
		os.Exit(1)
	}

	partners, err := store.List(context.Background(), *filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list partners: %v\n", err)
		os.Exit(1)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(partners)
		return
	}

	if len(partners) == 0 {
		fmt.Println("No partners found")
		return
	}

	fmt.Printf("%-20s %-8s %-8s %-8s %-30s %-30s\n", "ID", "Supply", "Receive", "Enabled", "Push URL", "DID")
	fmt.Println(strings.Repeat("-", 104))
	for _, p := range partners {
		enabled := "yes"
		if !p.Enabled {
			enabled = "no"
		}
		supplyStr := "-"
		if p.CanSupplyVouchers {
			supplyStr = "yes"
		}
		receiveStr := "-"
		if p.CanReceiveVouchers {
			receiveStr = "yes"
		}
		pushURL := p.PushURL
		if len(pushURL) > 30 {
			pushURL = pushURL[:27] + "..."
		}
		didURI := p.DIDURI
		if len(didURI) > 30 {
			didURI = didURI[:27] + "..."
		}
		fmt.Printf("%-20s %-8s %-8s %-8s %-30s %-30s\n", p.ID, supplyStr, receiveStr, enabled, pushURL, didURI)
	}
	fmt.Printf("\n%d partner(s)\n", len(partners))
}

func partnersShowCmd() {
	fs := flag.NewFlagSet("partners show", flag.ExitOnError)
	id := fs.String("id", "", "Partner ID (required)")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	_ = fs.Parse(os.Args[3:])

	if *id == "" {
		fmt.Fprintf(os.Stderr, "error: -id is required\n")
		os.Exit(1)
	}

	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	db, err := openDatabase(config.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	store := NewPartnerStore(db)
	if err := store.Init(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize partner store: %v\n", err)
		os.Exit(1)
	}

	p, err := store.Get(context.Background(), *id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "partner not found: %v\n", err)
		os.Exit(1)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(p)
		return
	}

	fmt.Printf("ID:              %s\n", p.ID)
	fmt.Printf("Can Supply:      %v\n", p.CanSupplyVouchers)
	fmt.Printf("Can Receive:     %v\n", p.CanReceiveVouchers)
	fmt.Printf("Enabled:         %v\n", p.Enabled)
	if p.DIDURI != "" {
		fmt.Printf("DID:             %s\n", p.DIDURI)
	}
	if p.PublicKeyFingerprint != "" {
		fmt.Printf("Key Fingerprint: %s\n", p.PublicKeyFingerprint)
	}
	if p.PublicKey != "" {
		fmt.Printf("Public Key:      (set, %d bytes PEM)\n", len(p.PublicKey))
	}
	if p.PushURL != "" {
		fmt.Printf("Push URL:        %s\n", p.PushURL)
	}
	if p.PullURL != "" {
		fmt.Printf("Pull URL:        %s\n", p.PullURL)
	}
	if p.AuthToken != "" {
		fmt.Printf("Auth Token:      %s...\n", p.AuthToken[:min(8, len(p.AuthToken))])
	}
	if p.DIDDocumentFetchedAt > 0 {
		fmt.Printf("DID Doc Fetched: %s\n", time.Unix(p.DIDDocumentFetchedAt, 0).Format(time.RFC3339))
	}
	fmt.Printf("Created:         %s\n", time.Unix(p.CreatedAt, 0).Format(time.RFC3339))
	fmt.Printf("Updated:         %s\n", time.Unix(p.UpdatedAt, 0).Format(time.RFC3339))
}

func partnersRemoveCmd() {
	fs := flag.NewFlagSet("partners remove", flag.ExitOnError)
	id := fs.String("id", "", "Partner ID to remove (required)")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	_ = fs.Parse(os.Args[3:])

	if *id == "" {
		fmt.Fprintf(os.Stderr, "error: -id is required\n")
		os.Exit(1)
	}

	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	db, err := openDatabase(config.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	store := NewPartnerStore(db)
	if err := store.Init(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize partner store: %v\n", err)
		os.Exit(1)
	}

	if err := store.Delete(context.Background(), *id); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove partner: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Partner %q removed\n", *id)
}

func partnersExportCmd() {
	fs := flag.NewFlagSet("partners export", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "Path to config file")
	_ = fs.Parse(os.Args[3:])

	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	db, err := openDatabase(config.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	store := NewPartnerStore(db)
	if err := store.Init(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize partner store: %v\n", err)
		os.Exit(1)
	}

	data, err := store.ExportJSON(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to export partners: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(data))
}

// bootstrapPartners loads partners from the config file and ensures they exist
// in the database. Existing partners (by ID) are skipped — config bootstrap is
// additive and idempotent. This runs on every server start.
func bootstrapPartners(ctx context.Context, config *Config, store *PartnerStore) {
	for _, pc := range config.Partners {
		if pc.ID == "" {
			slog.Warn("partner bootstrap: skipping entry with empty id")
			continue
		}

		// Check if partner already exists
		existing, _ := store.Get(ctx, pc.ID)
		if existing != nil {
			slog.Debug("partner bootstrap: partner already exists, skipping", "id", pc.ID)
			continue
		}

		enabled := true
		if pc.Enabled != nil {
			enabled = *pc.Enabled
		}

		canSupply := false
		if pc.CanSupply != nil {
			canSupply = *pc.CanSupply
		}
		canReceive := false
		if pc.CanReceive != nil {
			canReceive = *pc.CanReceive
		}

		p := &Partner{
			ID:                 pc.ID,
			CanSupplyVouchers:  canSupply,
			CanReceiveVouchers: canReceive,
			DIDURI:             pc.DID,
			PushURL:            pc.PushURL,
			PullURL:            pc.PullURL,
			AuthToken:          pc.AuthToken,
			Enabled:            enabled,
		}

		// Load public key from file if specified
		if pc.KeyFile != "" {
			pemData, err := os.ReadFile(pc.KeyFile)
			if err != nil {
				slog.Warn("partner bootstrap: failed to read key file", "id", pc.ID, "file", pc.KeyFile, "error", err)
			} else {
				if _, err := LoadPublicKeyFromPEM(pemData); err != nil {
					slog.Warn("partner bootstrap: failed to parse key file", "id", pc.ID, "file", pc.KeyFile, "error", err)
				} else {
					p.PublicKey = string(pemData)
				}
			}
		}

		if err := store.Add(ctx, p); err != nil {
			slog.Warn("partner bootstrap: failed to add partner", "id", pc.ID, "error", err)
			continue
		}
		slog.Info("partner bootstrap: enrolled partner from config", "id", pc.ID,
			"can_supply", canSupply, "can_receive", canReceive, "did", pc.DID)
	}
}

// marshalPublicKeyPEM encodes a crypto.PublicKey as PEM.
func marshalPublicKeyPEM(pub interface{}) ([]byte, error) {
	derBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: derBytes,
	}), nil
}
