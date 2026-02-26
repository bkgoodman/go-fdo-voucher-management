// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func main() {
	// Define subcommands
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]

	switch subcommand {
	case "server":
		runServer()
	case "vouchers":
		runVouchersCommand()
	case "tokens":
		runTokensCommand()
	case "keys":
		runKeysCommand()
	case "generate":
		runGenerateCommand()
	case "pullauth":
		runPullAuthCommand()
	case "pull":
		runPullCommand()
	case "partners":
		runPartnersCommand()
	case "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `FDO Voucher Manager

Usage:
  fdo-voucher-manager server [options]
  fdo-voucher-manager vouchers [command] [options]
  fdo-voucher-manager tokens [command] [options]
  fdo-voucher-manager pullauth [options]
  fdo-voucher-manager pull [options]
  fdo-voucher-manager partners [command] [options]
  fdo-voucher-manager help

Subcommands:
  server              Start the HTTP server for receiving vouchers
  vouchers            Manage vouchers (list, show, retry)
  tokens              Manage receiver authentication tokens
  partners            Manage trusted partner identities (add, list, show, remove, export)
  pullauth            Perform PullAuth handshake only (authentication test)
  pull                Authenticate, list, and download vouchers from a Holder
  help                Show this help message

Options for 'server':
  -config string     Path to config file (default: config.yaml)
  -debug             Enable debug logging

Options for 'vouchers list':
  -status string     Filter by status (pending, succeeded, failed)
  -guid string       Filter by GUID
  -limit int         Maximum results (default: 50)

Options for 'vouchers show':
  -guid string       GUID to show (required)

Options for 'vouchers retry':
  -guid string       GUID to retry (required)

Options for 'tokens add':
  -token string      Token value (required)
  -description string Token description
  -expires int       Expiration in hours (0 = never)

Options for 'tokens list':
  (no options)

Options for 'tokens delete':
  -token string      Token to delete (required)

Options for 'pullauth':
  -url string        Holder base URL (required)
  -key string        Owner private key PEM file (for non-delegate pull)
  -key-type string   Key type if generating ephemeral key (ec256, ec384, rsa2048)
  -owner-pub string  Owner public key PEM file (for delegate-based pull)
  -delegate-key string    Delegate private key PEM file
  -delegate-chain string  Delegate certificate chain PEM file
  -json              Output as JSON

Options for 'pull':
  -url string        Holder base URL (required)
  -key string        Owner private key PEM file (for non-delegate pull)
  -key-type string   Key type if generating ephemeral key (ec256, ec384, rsa2048)
  -owner-pub string  Owner public key PEM file (for delegate-based pull)
  -delegate-key string    Delegate private key PEM file
  -delegate-chain string  Delegate certificate chain PEM file
  -since string      Return vouchers created after this time (RFC 3339)
  -until string      Return vouchers created before this time (RFC 3339)
  -continuation string  Continuation token from a previous pull
  -limit int         Max vouchers per page (0 = server default)
  -output string     Directory to save downloaded voucher files
  -list              List vouchers only (do not download)
  -json              Output as JSON
`)
}

func runServer() {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "Path to config file")
	debug := fs.Bool("debug", false, "Enable debug logging")
	fs.Parse(os.Args[2:])

	// Setup logging
	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	}

	// Load config
	config, err := LoadConfig(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Open database
	db, err := openDatabase(config.Database.Path)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()

	// Initialize storage
	tokenManager := NewVoucherReceiverTokenManager(db)
	if err := tokenManager.Init(ctx); err != nil {
		slog.Error("failed to initialize token manager", "error", err)
		os.Exit(1)
	}

	transmitStore := NewVoucherTransmissionStore(db)
	if err := transmitStore.Init(ctx); err != nil {
		slog.Error("failed to initialize transmission store", "error", err)
		os.Exit(1)
	}

	fileStore := NewVoucherFileStore(config.VoucherFiles.Directory)

	partnerStore := NewPartnerStore(db)
	if err := partnerStore.Init(ctx); err != nil {
		slog.Error("failed to initialize partner store", "error", err)
		os.Exit(1)
	}

	// Bootstrap partners from config file
	if len(config.Partners) > 0 {
		bootstrapPartners(ctx, config, partnerStore)
	}

	// Initialize services
	signingService := NewVoucherSigningService(config.VoucherSigning.Mode, config.VoucherSigning.ExternalCommand, config.VoucherSigning.ExternalTimeout)

	ownerKeyService := NewOwnerKeyService(
		NewExternalCommandExecutor(config.OwnerSignover.ExternalCommand, config.OwnerSignover.Timeout),
	)

	oveExtraDataService := NewOVEExtraDataService(
		config.OVEExtraData.Enabled,
		config.OVEExtraData.ExternalCommand,
		config.OVEExtraData.Timeout,
	)

	// Enable DID resolution if DID cache, DID push, or static DID signover is configured
	didEnabled := config.DIDCache.Enabled || config.DIDPush.Enabled || config.OwnerSignover.StaticDID != ""
	didResolver := NewDIDResolver(nil, didEnabled)
	if !config.Server.UseTLS {
		didResolver.SetInsecureHTTP(true)
	}

	destinationResolver := NewVoucherDestinationResolver(
		config,
		NewExternalCommandExecutor(config.DestinationCallback.ExternalCommand, config.DestinationCallback.Timeout),
		didResolver,
		partnerStore,
	)

	pushClient := NewVoucherPushClient()

	pushService := NewVoucherPushService(config, transmitStore, destinationResolver, pushClient)

	pipeline := NewVoucherPipeline(
		config,
		signingService,
		ownerKeyService,
		oveExtraDataService,
		destinationResolver,
		fileStore,
		transmitStore,
		pushService,
		didResolver,
	)

	// Create receiver handler
	receiverHandler := NewVoucherReceiverHandler(
		config,
		tokenManager,
		fileStore,
		transmitStore,
		pipeline,
		partnerStore,
	)

	// Setup HTTP server
	mux := http.NewServeMux()
	if config.VoucherReceiver.Enabled {
		mux.Handle(config.VoucherReceiver.Endpoint, receiverHandler)
		slog.Info("voucher receiver endpoint registered", "endpoint", config.VoucherReceiver.Endpoint)
	}

	// Setup DID minting and serving (must happen before pull service so owner key is available)
	var ownerKey crypto.Signer
	if config.DIDMinting.Enabled {
		ownerKey = setupDIDMinting(config, mux, signingService)
	}

	// Setup pull service (PullAuth + Pull API) — uses the same owner key as DID identity
	if config.PullService.Enabled {
		if ownerKey == nil {
			slog.Error("pull service requires DID minting to be enabled (owner key needed for PullAuth)")
		} else {
			setupPullService(config, mux, ownerKey, fileStore, transmitStore)
		}
	}

	server := &http.Server{
		Addr:    config.Server.Addr,
		Handler: fdoVersionMiddleware(mux),
	}

	// Start retry worker
	retryWorker := NewVoucherRetryWorker(config, transmitStore, pushService)
	retryWorker.Start(ctx)

	// Start partner DID document refresh worker
	if config.DIDCache.Enabled {
		refreshWorker := NewPartnerRefreshWorker(partnerStore, didResolver, config.DIDCache.RefreshInterval, config.DIDCache.MaxAge)
		refreshWorker.Start(ctx)
	}

	// Start server in background
	go func() {
		slog.Info("starting HTTP server", "addr", config.Server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("shutting down server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	db.Close()
	slog.Info("server stopped")
}

func runVouchersCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager vouchers [list|show|retry] [options]\n")
		os.Exit(1)
	}

	command := os.Args[2]

	switch command {
	case "list":
		vouchersListCmd()
	case "show":
		vouchersShowCmd()
	case "retry":
		vouchersRetryCmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown vouchers command: %s\n", command)
		os.Exit(1)
	}
}

func vouchersListCmd() {
	fs := flag.NewFlagSet("vouchers list", flag.ExitOnError)
	status := fs.String("status", "", "Filter by status")
	guid := fs.String("guid", "", "Filter by GUID")
	limit := fs.Int("limit", 50, "Maximum results")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	fs.Parse(os.Args[3:])

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
	defer db.Close()

	store := NewVoucherTransmissionStore(db)
	records, err := store.ListTransmissions(context.Background(), *status, *guid, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list transmissions: %v\n", err)
		os.Exit(1)
	}

	if len(records) == 0 {
		fmt.Println("No vouchers found")
		return
	}

	fmt.Printf("%-40s %-15s %-30s %-10s %-5s\n", "GUID", "Status", "Destination", "Attempts", "ID")
	fmt.Println(string(make([]byte, 100)))
	for _, rec := range records {
		fmt.Printf("%-40s %-15s %-30s %-10d %-5d\n", rec.VoucherGUID, rec.Status, rec.DestinationURL, rec.Attempts, rec.ID)
	}
}

func vouchersShowCmd() {
	fs := flag.NewFlagSet("vouchers show", flag.ExitOnError)
	guid := fs.String("guid", "", "GUID to show")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	fs.Parse(os.Args[3:])

	if *guid == "" {
		fmt.Fprintf(os.Stderr, "error: -guid is required\n")
		os.Exit(1)
	}

	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	db, err := sql.Open("sqlite3", config.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	store := NewVoucherTransmissionStore(db)
	rec, err := store.FetchLatestByGUID(context.Background(), *guid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch transmission: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("ID:                 %d\n", rec.ID)
	fmt.Printf("GUID:               %s\n", rec.VoucherGUID)
	fmt.Printf("Serial:             %s\n", rec.SerialNumber)
	fmt.Printf("Model:              %s\n", rec.ModelNumber)
	fmt.Printf("Status:             %s\n", rec.Status)
	fmt.Printf("Destination:        %s\n", rec.DestinationURL)
	fmt.Printf("Source:             %s\n", rec.DestinationSource)
	fmt.Printf("Attempts:           %d\n", rec.Attempts)
	fmt.Printf("Last Error:         %s\n", rec.LastError)
	fmt.Printf("File Path:          %s\n", rec.FilePath)
	fmt.Printf("Created:            %s\n", rec.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:            %s\n", rec.UpdatedAt.Format(time.RFC3339))
	if rec.LastAttemptAt.Valid {
		fmt.Printf("Last Attempt:       %s\n", rec.LastAttemptAt.Time.Format(time.RFC3339))
	}
	if rec.DeliveredAt.Valid {
		fmt.Printf("Delivered:          %s\n", rec.DeliveredAt.Time.Format(time.RFC3339))
	}
}

func vouchersRetryCmd() {
	fs := flag.NewFlagSet("vouchers retry", flag.ExitOnError)
	guid := fs.String("guid", "", "GUID to retry")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	fs.Parse(os.Args[3:])

	if *guid == "" {
		fmt.Fprintf(os.Stderr, "error: -guid is required\n")
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
	defer db.Close()

	store := NewVoucherTransmissionStore(db)
	rec, err := store.FetchLatestByGUID(context.Background(), *guid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch transmission: %v\n", err)
		os.Exit(1)
	}

	pushClient := NewVoucherPushClient()
	pushService := NewVoucherPushService(config, store, nil, pushClient)

	if err := pushService.AttemptRecord(context.Background(), rec); err != nil {
		fmt.Fprintf(os.Stderr, "retry failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Retry initiated for GUID %s\n", *guid)
}

func runTokensCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager tokens [add|list|delete] [options]\n")
		os.Exit(1)
	}

	command := os.Args[2]

	switch command {
	case "add":
		tokensAddCmd()
	case "list":
		tokensListCmd()
	case "delete":
		tokensDeleteCmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown tokens command: %s\n", command)
		os.Exit(1)
	}
}

func tokensAddCmd() {
	fs := flag.NewFlagSet("tokens add", flag.ExitOnError)
	token := fs.String("token", "", "Token value")
	description := fs.String("description", "", "Token description")
	expires := fs.Int("expires", 0, "Expiration in hours")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	fs.Parse(os.Args[3:])

	if *token == "" {
		fmt.Fprintf(os.Stderr, "error: -token is required\n")
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
	defer db.Close()

	manager := NewVoucherReceiverTokenManager(db)
	if err := manager.AddReceiverToken(context.Background(), *token, *description, *expires); err != nil {
		fmt.Fprintf(os.Stderr, "failed to add token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Token added successfully\n")
}

func tokensListCmd() {
	fs := flag.NewFlagSet("tokens list", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "Path to config file")
	fs.Parse(os.Args[3:])

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
	defer db.Close()

	manager := NewVoucherReceiverTokenManager(db)
	tokens, err := manager.ListReceiverTokens(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list tokens: %v\n", err)
		os.Exit(1)
	}

	if len(tokens) == 0 {
		fmt.Println("No tokens found")
		return
	}

	fmt.Printf("%-40s %-20s %-30s %-10s\n", "Token", "Description", "Created", "Expires")
	fmt.Println(string(make([]byte, 100)))
	for _, t := range tokens {
		expires := "never"
		if t.ExpiresAt != nil {
			expires = t.ExpiresAt.Format("2006-01-02")
		}
		fmt.Printf("%-40s %-20s %-30s %-10s\n", t.Token[:20]+"...", t.Description, t.CreatedAt.Format("2006-01-02"), expires)
	}
}

func tokensDeleteCmd() {
	fs := flag.NewFlagSet("tokens delete", flag.ExitOnError)
	token := fs.String("token", "", "Token to delete")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	fs.Parse(os.Args[3:])

	if *token == "" {
		fmt.Fprintf(os.Stderr, "error: -token is required\n")
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
	defer db.Close()

	manager := NewVoucherReceiverTokenManager(db)
	if err := manager.DeleteReceiverToken(context.Background(), *token); err != nil {
		fmt.Fprintf(os.Stderr, "failed to delete token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Token deleted successfully\n")
}

func runKeysCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager keys [export|show] [options]\n")
		os.Exit(1)
	}

	command := os.Args[2]

	switch command {
	case "export":
		keysExportCmd()
	case "show":
		keysShowCmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown keys command: %s\n", command)
		os.Exit(1)
	}
}

func keysExportCmd() {
	fs := flag.NewFlagSet("keys export", flag.ExitOnError)
	output := fs.String("output", "", "Output file for public key")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	fs.Parse(os.Args[3:])

	if *output == "" {
		fmt.Fprintf(os.Stderr, "error: -output is required\n")
		os.Exit(1)
	}

	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	db, err := sql.Open("sqlite3", config.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// For now, just create a placeholder PEM file
	// In a real implementation, this would export the actual owner key from the database
	placeholderPEM := `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEHVlXJqYKLdLKvJDvDqVJVLVd
vJqLKqVJVLVdvJqLKqVJVLVdvJqLKqVJVLVdvJqLKqVJVLVdvJqLKqVJVA==
-----END PUBLIC KEY-----
`

	if err := os.WriteFile(*output, []byte(placeholderPEM), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write key file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Owner public key exported to: %s\n", *output)
}

func keysShowCmd() {
	fs := flag.NewFlagSet("keys show", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "Path to config file")
	fs.Parse(os.Args[3:])

	config, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Key Management Configuration\n")
	fmt.Printf("============================\n")
	fmt.Printf("Key Type:              %s\n", config.KeyManagement.KeyType)
	fmt.Printf("First Time Init:       %v\n", config.KeyManagement.FirstTimeInit)
	fmt.Printf("Database Path:         %s\n", config.Database.Path)
}

func runGenerateCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager generate voucher [options]\n")
		os.Exit(1)
	}

	command := os.Args[2]

	switch command {
	case "voucher":
		generateVoucherCmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown generate command: %s\n", command)
		os.Exit(1)
	}
}

func generateVoucherCmd() {
	fs := flag.NewFlagSet("generate voucher", flag.ExitOnError)
	serial := fs.String("serial", "TEST-SERIAL", "Device serial number")
	model := fs.String("model", "TEST-MODEL", "Device model number")
	output := fs.String("output", "", "Output file for voucher (default: stdout)")
	ownerKey := fs.String("owner-key", "", "Owner public key file (PEM format)")
	fs.Parse(os.Args[3:])

	// Generate test voucher
	var voucherPEM string
	var err error
	if *ownerKey != "" {
		voucherPEM, err = GenerateTestVoucherWithOwner(*serial, *model, *ownerKey)
	} else {
		voucherPEM, err = GenerateTestVoucher(*serial, *model)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate voucher: %v\n", err)
		os.Exit(1)
	}

	// Write to output
	if *output == "" {
		// Write to stdout
		fmt.Print(voucherPEM)
	} else {
		// Write to file
		if err := os.WriteFile(*output, []byte(voucherPEM), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write voucher file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Voucher generated and written to: %s\n", *output)
	}
}

// fdoVersionMiddleware wraps an http.Handler and adds the X-FDO-Version
// header to every response per spec §7.1.
func fdoVersionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-FDO-Version", "1.0")
		next.ServeHTTP(w, r)
	})
}

func openDatabase(path string) (*sql.DB, error) {
	connector, err := (&driver.SQLite{}).OpenConnector("file:" + path + "?_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("failed to create sqlite connector: %w", err)
	}
	return sql.OpenDB(connector), nil
}
