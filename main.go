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
	"strings"
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
	case "fdokeyauth", "pullauth":
		runFDOKeyAuthCommand()
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
  fdo-voucher-manager fdokeyauth [options]
  fdo-voucher-manager pull [options]
  fdo-voucher-manager partners [command] [options]
  fdo-voucher-manager help

Subcommands:
  server              Start the HTTP server for receiving vouchers
  vouchers            Manage vouchers (list, show, retry, grants, custodians)
  tokens              Manage receiver authentication tokens
  partners            Manage trusted partner identities (add, list, show, remove, export)
  fdokeyauth          Perform FDOKeyAuth handshake only (authentication test)
  pull                Authenticate, list, and download vouchers from a Server
  help                Show this help message

Options for 'server':
  -config string     Path to config file (default: config.yaml)
  -debug             Enable debug logging

Options for 'vouchers list':
  -status string     Filter by status (pending, succeeded, failed, assigned)
  -guid string       Filter by GUID
  -owner string      Filter by owner key fingerprint
  -serial string     Filter by serial number
  -limit int         Maximum results (default: 50)

Options for 'vouchers show':
  -guid string       GUID to show (required)

Options for 'vouchers retry':
  -guid string       GUID to retry (required)

Options for 'vouchers grants':
  -guid string       Filter by voucher GUID
  -type string       Filter by identity type (owner_key, custodian, purchaser_token)
  -limit int         Maximum results (default: 100)

Options for 'vouchers custodians':
  -fingerprint string  Show vouchers for a specific custodian
  -limit int           Maximum results (default: 50)

Options for 'tokens add':
  -token string      Token value (required)
  -description string Token description
  -expires int       Expiration in hours (0 = never)

Options for 'tokens list':
  (no options)

Options for 'tokens delete':
  -token string      Token to delete (required)

Options for 'fdokeyauth':
  -url string        Server base URL (required)
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

	// Build a unified token validator for the status and assign handlers.
	// This checks both global config token and DB-backed tokens.
	validateToken := func(token string) (*CallerIdentity, error) {
		if config.VoucherReceiver.GlobalToken != "" && token == config.VoucherReceiver.GlobalToken {
			return &CallerIdentity{
				Fingerprint:   FingerprintStringHex("global"),
				IdentityLabel: "global",
				AuthMethod:    AuthMethodGlobalToken,
				HasOwnerKey:   false,
			}, nil
		}
		return tokenManager.ValidateTokenToIdentity(context.Background(), token)
	}

	// Create status handler
	statusHandler := NewVoucherStatusHandler(transmitStore, validateToken)

	// Create list handler (scoped to caller's access)
	listHandler := NewVoucherListHandler(transmitStore, validateToken)

	// Setup HTTP server
	mux := http.NewServeMux()
	if config.VoucherReceiver.Enabled {
		mux.Handle(config.VoucherReceiver.Endpoint, receiverHandler)
		slog.Info("voucher receiver endpoint registered", "endpoint", config.VoucherReceiver.Endpoint)

		// Register status and list endpoints under the receiver root
		root := config.VoucherReceiver.Endpoint
		if root == "" {
			root = "/api/v1/vouchers"
		}
		statusPath := root + "/status/"
		mux.Handle(statusPath, statusHandler)
		slog.Info("voucher status endpoint registered", "endpoint", statusPath)

		listPath := root + "/list"
		mux.Handle(listPath, listHandler)
		slog.Info("voucher list endpoint registered", "endpoint", listPath)
	}

	// Setup DID minting and serving (must happen before pull service so owner key is available)
	var ownerKey crypto.Signer
	if config.DIDMinting.Enabled {
		ownerKey = setupDIDMinting(config, mux, signingService)
	}

	// Register the assign handler now that ownerKey is available.
	// The assign endpoint uses the Holder's signing key for voucher extension.
	if config.VoucherReceiver.Enabled {
		assignHandler := NewVoucherAssignHandler(
			transmitStore,
			fileStore,
			signingService,
			didResolver,
			validateToken,
			ownerKey,
		)
		root := config.VoucherReceiver.Endpoint
		if root == "" {
			root = "/api/v1/vouchers"
		}
		assignPath := root + "/assign"
		mux.Handle(assignPath, assignHandler)
		slog.Info("voucher assign endpoint registered", "endpoint", assignPath)
	}

	// Setup pull service (FDOKeyAuth + Pull API) — uses the same owner key as DID identity
	if config.PullService.Enabled {
		if ownerKey == nil {
			slog.Error("pull service requires DID minting to be enabled (owner key needed for FDOKeyAuth)")
		} else {
			setupPullService(config, mux, ownerKey, fileStore, transmitStore)
		}
	}

	// Setup FDOKeyAuth on the push receiver endpoint so suppliers can authenticate
	// before pushing vouchers. Uses the same owner key as the pull service.
	if config.VoucherReceiver.Enabled && ownerKey != nil {
		setupPushReceiverAuth(config, mux, ownerKey, tokenManager, partnerStore)
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
		fmt.Fprintf(os.Stderr, "Usage: fdo-voucher-manager vouchers [list|show|assign|unassign|retry|grants|custodians] [options]\n")
		os.Exit(1)
	}

	command := os.Args[2]

	switch command {
	case "list":
		vouchersListCmd()
	case "show":
		vouchersShowCmd()
	case "assign":
		vouchersAssignCmd()
	case "unassign":
		vouchersUnassignCmd()
	case "retry":
		vouchersRetryCmd()
	case "grants":
		vouchersGrantsCmd()
	case "custodians":
		vouchersCustodiansCmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown vouchers command: %s\n", command)
		os.Exit(1)
	}
}

func vouchersListCmd() {
	fs := flag.NewFlagSet("vouchers list", flag.ExitOnError)
	status := fs.String("status", "", "Filter by status")
	guid := fs.String("guid", "", "Filter by GUID")
	owner := fs.String("owner", "", "Filter by owner key fingerprint")
	serial := fs.String("serial", "", "Filter by serial number")
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

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)

	var records []VoucherTransmissionRecord
	switch {
	case *owner != "":
		records, err = store.ListByOwner(ctx, *owner, *limit)
	case *serial != "":
		records, err = store.FetchBySerial(ctx, *serial)
	default:
		records, err = store.ListTransmissions(ctx, *status, *guid, *limit)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list transmissions: %v\n", err)
		os.Exit(1)
	}

	if len(records) == 0 {
		fmt.Println("No vouchers found")
		return
	}

	fmt.Printf("%-36s %-12s %-15s %-12s %-30s %s\n", "GUID", "Serial", "Status", "Assigned By", "Destination", "ID")
	fmt.Println(strings.Repeat("-", 120))
	for _, rec := range records {
		serial := rec.SerialNumber
		if len(serial) > 12 {
			serial = serial[:12]
		}
		assignedBy := rec.AssignedByFingerprint
		if len(assignedBy) > 12 {
			assignedBy = assignedBy[:12]
		}
		dest := rec.DestinationURL
		if len(dest) > 30 {
			dest = dest[:30]
		}
		fmt.Printf("%-36s %-12s %-15s %-12s %-30s %d\n", rec.VoucherGUID, serial, rec.Status, assignedBy, dest, rec.ID)
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

	fmt.Printf("ID:                    %d\n", rec.ID)
	fmt.Printf("GUID:                  %s\n", rec.VoucherGUID)
	fmt.Printf("Serial:                %s\n", rec.SerialNumber)
	fmt.Printf("Model:                 %s\n", rec.ModelNumber)
	fmt.Printf("Status:                %s\n", rec.Status)
	fmt.Printf("Owner Key Fingerprint: %s\n", rec.OwnerKeyFingerprint)
	fmt.Printf("Destination:           %s\n", rec.DestinationURL)
	fmt.Printf("Source:                %s\n", rec.DestinationSource)
	fmt.Printf("Attempts:              %d\n", rec.Attempts)
	fmt.Printf("Last Error:            %s\n", rec.LastError)
	fmt.Printf("File Path:             %s\n", rec.FilePath)
	fmt.Printf("Created:               %s\n", rec.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:               %s\n", rec.UpdatedAt.Format(time.RFC3339))
	if rec.LastAttemptAt.Valid {
		fmt.Printf("Last Attempt:          %s\n", rec.LastAttemptAt.Time.Format(time.RFC3339))
	}
	if rec.DeliveredAt.Valid {
		fmt.Printf("Delivered:             %s\n", rec.DeliveredAt.Time.Format(time.RFC3339))
	}
	if rec.AssignedAt.Valid {
		fmt.Printf("Assigned At:           %s\n", rec.AssignedAt.Time.Format(time.RFC3339))
	}
	if rec.AssignedToFingerprint != "" {
		fmt.Printf("Assigned To:           %s\n", rec.AssignedToFingerprint)
	}
	if rec.AssignedToDID != "" {
		fmt.Printf("Assigned To DID:       %s\n", rec.AssignedToDID)
	}
	if rec.AssignedByFingerprint != "" {
		fmt.Printf("Assigned By:           %s\n", rec.AssignedByFingerprint)
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

func vouchersGrantsCmd() {
	fs := flag.NewFlagSet("vouchers grants", flag.ExitOnError)
	guid := fs.String("guid", "", "Filter by voucher GUID")
	identityType := fs.String("type", "", "Filter by identity type (owner_key, custodian, purchaser_token)")
	limit := fs.Int("limit", 100, "Maximum results")
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

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize store: %v\n", err)
		os.Exit(1)
	}

	var grants []AccessGrant
	if *guid != "" {
		grants, err = store.ListAccessGrants(ctx, *guid)
	} else {
		grants, err = store.ListAllAccessGrants(ctx, *identityType, *limit)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list grants: %v\n", err)
		os.Exit(1)
	}

	if len(grants) == 0 {
		fmt.Println("No access grants found")
		return
	}

	fmt.Printf("%-36s %-12s %-16s %-16s %-12s %s\n", "Voucher GUID", "Serial", "Identity FP", "Type", "Access", "Granted By")
	fmt.Println(strings.Repeat("-", 110))
	for _, g := range grants {
		fp := g.IdentityFingerprint
		if len(fp) > 16 {
			fp = fp[:16]
		}
		serial := g.SerialNumber
		if len(serial) > 12 {
			serial = serial[:12]
		}
		grantedBy := g.GrantedBy
		if len(grantedBy) > 16 {
			grantedBy = grantedBy[:16]
		}
		fmt.Printf("%-36s %-12s %-16s %-16s %-12s %s\n", g.VoucherGUID, serial, fp, g.IdentityType, g.AccessLevel, grantedBy)
	}
}

func vouchersCustodiansCmd() {
	fs := flag.NewFlagSet("vouchers custodians", flag.ExitOnError)
	fingerprint := fs.String("fingerprint", "", "Show vouchers for a specific custodian fingerprint")
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

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize store: %v\n", err)
		os.Exit(1)
	}

	if *fingerprint != "" {
		// Show vouchers for a specific custodian
		records, err := store.ListByCustodian(ctx, *fingerprint, *limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list vouchers for custodian: %v\n", err)
			os.Exit(1)
		}
		if len(records) == 0 {
			fmt.Printf("No vouchers found for custodian %s\n", *fingerprint)
			return
		}
		fmt.Printf("Vouchers assigned by custodian: %s\n\n", *fingerprint)
		fmt.Printf("%-36s %-12s %-15s %-30s %s\n", "GUID", "Serial", "Status", "Assigned To", "Assigned At")
		fmt.Println(strings.Repeat("-", 110))
		for _, rec := range records {
			serial := rec.SerialNumber
			if len(serial) > 12 {
				serial = serial[:12]
			}
			assignedTo := rec.AssignedToFingerprint
			if len(assignedTo) > 30 {
				assignedTo = assignedTo[:30]
			}
			assignedAt := ""
			if rec.AssignedAt.Valid {
				assignedAt = rec.AssignedAt.Time.Format("2006-01-02 15:04")
			}
			fmt.Printf("%-36s %-12s %-15s %-30s %s\n", rec.VoucherGUID, serial, rec.Status, assignedTo, assignedAt)
		}
	} else {
		// List all custodians with summary
		summaries, err := store.ListCustodians(ctx, *limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list custodians: %v\n", err)
			os.Exit(1)
		}
		if len(summaries) == 0 {
			fmt.Println("No custodians found")
			return
		}
		fmt.Printf("%-40s %-10s %s\n", "Custodian Fingerprint", "Vouchers", "Serials")
		fmt.Println(strings.Repeat("-", 90))
		for _, s := range summaries {
			serials := s.SerialList
			if len(serials) > 40 {
				serials = serials[:40] + "..."
			}
			fmt.Printf("%-40s %-10d %s\n", s.Fingerprint, s.VoucherCount, serials)
		}
	}
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

	fmt.Printf("%-25s %-20s %-16s %-12s %s\n", "Token", "Description", "Owner Key FP", "Created", "Expires")
	fmt.Println(strings.Repeat("-", 100))
	for _, t := range tokens {
		expires := "never"
		if t.ExpiresAt != nil {
			expires = t.ExpiresAt.Format("2006-01-02")
		}
		tokenPreview := t.Token
		if len(tokenPreview) > 20 {
			tokenPreview = tokenPreview[:20] + "..."
		}
		fp := t.OwnerKeyFingerprint
		if len(fp) > 16 {
			fp = fp[:16]
		}
		if fp == "" {
			fp = "(manual)"
		}
		fmt.Printf("%-25s %-20s %-16s %-12s %s\n", tokenPreview, t.Description, fp, t.CreatedAt.Format("2006-01-02"), expires)
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
