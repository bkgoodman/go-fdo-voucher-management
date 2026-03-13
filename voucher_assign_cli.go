// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/fido-device-onboard/go-fdo"
)

// vouchersAssignCmd implements `vouchers assign` — assign one or more vouchers
// to a new owner from the command line. This is the CLI equivalent of
// POST {root}/assign.
func vouchersAssignCmd() {
	fs := flag.NewFlagSet("vouchers assign", flag.ExitOnError)
	serial := fs.String("serial", "", "Serial number(s) to assign (comma-separated for batch)")
	guid := fs.String("guid", "", "Voucher GUID to assign (alternative to -serial)")
	newOwnerKeyFile := fs.String("new-owner-key", "", "PEM file with the new owner's public key")
	newOwnerDID := fs.String("new-owner-did", "", "DID URI of the new owner (alternative to -new-owner-key)")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	jsonOutput := fs.Bool("json", false, "Output results as JSON")
	fs.Parse(os.Args[3:])

	// Validate flags
	if *serial == "" && *guid == "" {
		fmt.Fprintf(os.Stderr, "error: one of -serial or -guid is required\n")
		os.Exit(1)
	}
	if *newOwnerKeyFile == "" && *newOwnerDID == "" {
		fmt.Fprintf(os.Stderr, "error: one of -new-owner-key or -new-owner-did is required\n")
		os.Exit(1)
	}
	if *newOwnerKeyFile != "" && *newOwnerDID != "" {
		fmt.Fprintf(os.Stderr, "error: -new-owner-key and -new-owner-did are mutually exclusive\n")
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

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize store: %v\n", err)
		os.Exit(1)
	}

	fileStore := NewVoucherFileStore(config.VoucherFiles.Directory)

	// Load the owner signer (needed for ExtendVoucher)
	ownerSigner, err := loadOwnerSignerForCLI(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load owner signing key: %v\n", err)
		os.Exit(1)
	}

	// Resolve the new owner's public key
	var newOwnerKey crypto.PublicKey
	var resolvedDID string
	if *newOwnerDID != "" {
		resolvedDID = *newOwnerDID
		resolver := NewDIDResolver(nil, true)
		if !config.Server.UseTLS {
			resolver.SetInsecureHTTP(true)
		}
		key, _, resolveErr := resolver.ResolveDIDKey(ctx, *newOwnerDID)
		if resolveErr != nil {
			fmt.Fprintf(os.Stderr, "DID resolution failed: %v\n", resolveErr)
			os.Exit(1)
		}
		newOwnerKey = key
	} else {
		pemData, readErr := os.ReadFile(*newOwnerKeyFile)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "failed to read key file %q: %v\n", *newOwnerKeyFile, readErr)
			os.Exit(1)
		}
		key, parseErr := LoadPublicKeyFromPEM(pemData)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "failed to parse public key: %v\n", parseErr)
			os.Exit(1)
		}
		newOwnerKey = key
	}

	newOwnerFingerprint := FingerprintPublicKeyHex(newOwnerKey)

	// Determine which serials to process
	var serials []string
	if *guid != "" {
		rec, fetchErr := store.FetchLatestByGUID(ctx, *guid)
		if fetchErr != nil || rec == nil {
			fmt.Fprintf(os.Stderr, "voucher not found for GUID %s\n", *guid)
			os.Exit(1)
		}
		serials = []string{rec.SerialNumber}
	} else {
		for _, s := range strings.Split(*serial, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				serials = append(serials, s)
			}
		}
	}

	if len(serials) == 0 {
		fmt.Fprintf(os.Stderr, "error: no serials to assign\n")
		os.Exit(1)
	}

	// The CLI operator is the admin/owner of this tool — use a synthetic identity
	callerFP := FingerprintStringHex("cli-operator")

	// Process each serial
	type result struct {
		Serial    string `json:"serial"`
		VoucherID string `json:"voucher_id,omitempty"`
		Status    string `json:"status"`
		Message   string `json:"message,omitempty"`
	}
	var results []result
	anyError := false

	for _, s := range serials {
		r := assignVoucherCLI(ctx, store, fileStore, ownerSigner, s, newOwnerKey, newOwnerFingerprint, resolvedDID, callerFP)
		results = append(results, r)
		if r.Status != "ok" {
			anyError = true
		}
	}

	if *jsonOutput {
		printJSON(results)
	} else {
		for _, r := range results {
			if r.Status == "ok" {
				fmt.Printf("  %s  %s  assigned  %s\n", r.VoucherID, r.Serial, r.Message)
			} else {
				fmt.Printf("  %s  %s  ERROR  %s\n", r.VoucherID, r.Serial, r.Message)
			}
		}
		okCount := 0
		for _, r := range results {
			if r.Status == "ok" {
				okCount++
			}
		}
		fmt.Printf("\n%d of %d voucher(s) assigned\n", okCount, len(results))
	}

	if anyError {
		os.Exit(1)
	}
}

// assignVoucherCLI performs a single voucher assignment from the CLI.
func assignVoucherCLI(
	ctx context.Context,
	store *VoucherTransmissionStore,
	fileStore *VoucherFileStore,
	ownerSigner crypto.Signer,
	serial string,
	newOwnerKey crypto.PublicKey,
	newOwnerFingerprint, newOwnerDID, callerFingerprint string,
) struct {
	Serial    string `json:"serial"`
	VoucherID string `json:"voucher_id,omitempty"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
} {
	type result = struct {
		Serial    string `json:"serial"`
		VoucherID string `json:"voucher_id,omitempty"`
		Status    string `json:"status"`
		Message   string `json:"message,omitempty"`
	}

	recs, err := store.FetchBySerial(ctx, serial)
	if err != nil || len(recs) == 0 {
		return result{Serial: serial, Status: "error", Message: "voucher not found for serial"}
	}
	rec := &recs[0]

	if rec.Status == transmissionStatusAssigned {
		return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", Message: "already assigned"}
	}
	if rec.Status == transmissionStatusSucceeded {
		return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", Message: "already delivered"}
	}

	voucherPath := fileStore.FilePathForGUID(rec.VoucherGUID)
	if voucherPath == "" {
		return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", Message: "voucher file not found"}
	}

	voucher, parseErr := fdo.ParseVoucherFile(voucherPath)
	if parseErr != nil {
		return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", Message: fmt.Sprintf("failed to parse voucher: %v", parseErr)}
	}

	var assigned *fdo.Voucher
	switch key := newOwnerKey.(type) {
	case *ecdsa.PublicKey:
		assigned, err = fdo.ExtendVoucher(voucher, ownerSigner, key, nil)
	case *rsa.PublicKey:
		assigned, err = fdo.ExtendVoucher(voucher, ownerSigner, key, nil)
	case []*x509.Certificate:
		assigned, err = fdo.ExtendVoucher(voucher, ownerSigner, key, nil)
	default:
		return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", Message: fmt.Sprintf("unsupported key type: %T", newOwnerKey)}
	}
	if err != nil {
		return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", Message: fmt.Sprintf("cryptographic extension failed: %v", err)}
	}

	// Back up the original voucher file so unassign can restore it
	if backupErr := fileStore.BackupVoucher(rec.VoucherGUID); backupErr != nil {
		return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", Message: fmt.Sprintf("failed to backup voucher: %v", backupErr)}
	}

	if _, saveErr := fileStore.SaveVoucher(assigned); saveErr != nil {
		return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "error", Message: fmt.Sprintf("failed to save voucher: %v", saveErr)}
	}

	if markErr := store.MarkAssigned(ctx, rec.ID, newOwnerFingerprint, newOwnerDID, callerFingerprint); markErr != nil {
		slog.Error("assign: failed to mark as assigned", "guid", rec.VoucherGUID, "error", markErr)
	}

	// Insert access grants
	_ = store.InsertAccessGrant(ctx, &AccessGrant{
		VoucherGUID:         rec.VoucherGUID,
		SerialNumber:        serial,
		IdentityFingerprint: callerFingerprint,
		IdentityType:        "custodian",
		AccessLevel:         "full",
		GrantedBy:           "cli",
	})
	_ = store.InsertAccessGrant(ctx, &AccessGrant{
		VoucherGUID:         rec.VoucherGUID,
		SerialNumber:        serial,
		IdentityFingerprint: newOwnerFingerprint,
		IdentityType:        "owner_key",
		AccessLevel:         "full",
		GrantedBy:           "cli",
	})

	slog.Info("assign: voucher assigned via CLI",
		"guid", rec.VoucherGUID,
		"serial", serial,
		"new_owner_fingerprint", newOwnerFingerprint,
	)

	return result{Serial: serial, VoucherID: rec.VoucherGUID, Status: "ok", Message: "assigned to " + newOwnerFingerprint[:16] + "..."}
}

// vouchersUnassignCmd implements `vouchers unassign` — revert one or more
// assignments, restoring vouchers to their pre-assignment state.
func vouchersUnassignCmd() {
	fs := flag.NewFlagSet("vouchers unassign", flag.ExitOnError)
	serial := fs.String("serial", "", "Serial number(s) to unassign (comma-separated)")
	guid := fs.String("guid", "", "Voucher GUID to unassign (alternative to -serial)")
	configPath := fs.String("config", "config.yaml", "Path to config file")
	jsonOutput := fs.Bool("json", false, "Output results as JSON")
	fs.Parse(os.Args[3:])

	if *serial == "" && *guid == "" {
		fmt.Fprintf(os.Stderr, "error: one of -serial or -guid is required\n")
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

	ctx := context.Background()
	store := NewVoucherTransmissionStore(db)
	if err := store.Init(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize store: %v\n", err)
		os.Exit(1)
	}

	// Determine which serials to process
	var serials []string
	if *guid != "" {
		rec, fetchErr := store.FetchLatestByGUID(ctx, *guid)
		if fetchErr != nil || rec == nil {
			fmt.Fprintf(os.Stderr, "voucher not found for GUID %s\n", *guid)
			os.Exit(1)
		}
		serials = []string{rec.SerialNumber}
	} else {
		for _, s := range strings.Split(*serial, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				serials = append(serials, s)
			}
		}
	}

	if len(serials) == 0 {
		fmt.Fprintf(os.Stderr, "error: no serials to unassign\n")
		os.Exit(1)
	}

	fileStore := NewVoucherFileStore(config.VoucherFiles.Directory)

	type result struct {
		Serial    string `json:"serial"`
		VoucherID string `json:"voucher_id,omitempty"`
		Status    string `json:"status"`
		Message   string `json:"message,omitempty"`
	}
	var results []result
	anyError := false

	for _, s := range serials {
		recs, fetchErr := store.FetchBySerial(ctx, s)
		if fetchErr != nil || len(recs) == 0 {
			results = append(results, result{Serial: s, Status: "error", Message: "voucher not found"})
			anyError = true
			continue
		}
		rec := &recs[0]

		if rec.Status != transmissionStatusAssigned {
			results = append(results, result{Serial: s, VoucherID: rec.VoucherGUID, Status: "error", Message: fmt.Sprintf("voucher is not assigned (status: %s)", rec.Status)})
			anyError = true
			continue
		}

		// Determine restore status: if there's a destination URL, restore to pending;
		// otherwise restore to no_destination.
		restoreStatus := "no_destination"
		if rec.DestinationURL != "" {
			restoreStatus = transmissionStatusPending
		}

		if markErr := store.MarkUnassigned(ctx, rec.ID, restoreStatus); markErr != nil {
			results = append(results, result{Serial: s, VoucherID: rec.VoucherGUID, Status: "error", Message: fmt.Sprintf("failed to unassign: %v", markErr)})
			anyError = true
			continue
		}

		// Restore the original voucher file (before cryptographic extension)
		if restoreErr := fileStore.RestoreVoucher(rec.VoucherGUID); restoreErr != nil {
			slog.Warn("unassign: failed to restore voucher backup (voucher may still be extended)", "guid", rec.VoucherGUID, "error", restoreErr)
		}

		// Remove access grants that were created by the assignment
		if grantErr := store.RemoveAccessGrantsForVoucher(ctx, rec.VoucherGUID); grantErr != nil {
			slog.Warn("unassign: failed to remove access grants", "guid", rec.VoucherGUID, "error", grantErr)
		}

		slog.Info("unassign: voucher unassigned via CLI",
			"guid", rec.VoucherGUID,
			"serial", s,
			"restored_status", restoreStatus,
		)

		results = append(results, result{Serial: s, VoucherID: rec.VoucherGUID, Status: "ok", Message: fmt.Sprintf("unassigned, status restored to %s", restoreStatus)})
	}

	if *jsonOutput {
		printJSON(results)
	} else {
		for _, r := range results {
			if r.Status == "ok" {
				fmt.Printf("  %s  %s  %s\n", r.VoucherID, r.Serial, r.Message)
			} else {
				fmt.Printf("  %s  %s  ERROR  %s\n", r.VoucherID, r.Serial, r.Message)
			}
		}
		okCount := 0
		for _, r := range results {
			if r.Status == "ok" {
				okCount++
			}
		}
		fmt.Printf("\n%d of %d voucher(s) unassigned\n", okCount, len(results))
	}

	if anyError {
		os.Exit(1)
	}
}

// loadOwnerSignerForCLI loads the owner's private signing key for CLI operations.
// It follows the same precedence as the server: import_key_file > key_export_path > error.
func loadOwnerSignerForCLI(config *Config) (crypto.Signer, error) {
	// Try import_key_file first
	if config.KeyManagement.ImportKeyFile != "" {
		return LoadPrivateKeyFromFile(config.KeyManagement.ImportKeyFile)
	}

	// Try DID minting key_export_path
	if config.DIDMinting.KeyExportPath != "" {
		return LoadPrivateKeyFromFile(config.DIDMinting.KeyExportPath)
	}

	return nil, fmt.Errorf("no owner signing key configured — set key_management.import_key_file or did_minting.key_export_path in config")
}

// printJSON encodes v as indented JSON to stdout.
func printJSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "json encoding failed: %v\n", err)
		return
	}
	fmt.Println(string(data))
}
