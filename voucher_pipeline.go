// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"fmt"
	"log/slog"

	"github.com/fido-device-onboard/go-fdo"
)

// VoucherPipeline orchestrates the receive → sign-over → store → push workflow
type VoucherPipeline struct {
	config              *Config
	signingService      *VoucherSigningService
	ownerKeyService     *OwnerKeyService
	oveExtraDataService *OVEExtraDataService
	destinationResolver *VoucherDestinationResolver
	fileStore           *VoucherFileStore
	transmissionStore   *VoucherTransmissionStore
	pushService         *VoucherPushService
	didResolver         *DIDResolver
}

// NewVoucherPipeline creates a new pipeline
func NewVoucherPipeline(
	config *Config,
	signingService *VoucherSigningService,
	ownerKeyService *OwnerKeyService,
	oveExtraDataService *OVEExtraDataService,
	destinationResolver *VoucherDestinationResolver,
	fileStore *VoucherFileStore,
	transmissionStore *VoucherTransmissionStore,
	pushService *VoucherPushService,
	didResolver *DIDResolver,
) *VoucherPipeline {
	return &VoucherPipeline{
		config:              config,
		signingService:      signingService,
		ownerKeyService:     ownerKeyService,
		oveExtraDataService: oveExtraDataService,
		destinationResolver: destinationResolver,
		fileStore:           fileStore,
		transmissionStore:   transmissionStore,
		pushService:         pushService,
		didResolver:         didResolver,
	}
}

// ProcessVoucher processes a received voucher through the pipeline.
// ownerKeyFingerprint is the SHA-256 hex fingerprint of the voucher's current
// owner public key, used to scope Pull API access.
func (p *VoucherPipeline) ProcessVoucher(ctx context.Context, voucher *fdo.Voucher, serial, model, guid, filePath, ownerKeyFingerprint string) error {
	slog.Info("pipeline: processing voucher", "guid", guid, "serial", serial, "model", model)

	// Step 1: Get OVEExtra data if configured
	var extraData map[int][]byte
	if p.oveExtraDataService != nil {
		var err error
		extraData, err = p.oveExtraDataService.GetOVEExtraData(ctx, serial, model)
		if err != nil {
			slog.Warn("pipeline: failed to get OVEExtra data", "guid", guid, "error", err)
		}
	}

	// Step 2: Get next owner key if configured
	var nextOwner crypto.PublicKey
	var didURL string
	if p.config.OwnerSignover.Mode != "" {
		switch p.config.OwnerSignover.Mode {
		case "static":
			if p.config.OwnerSignover.StaticDID != "" && p.didResolver != nil {
				// Resolve DID to get public key and voucher recipient URL
				key, recipientURL, err := p.didResolver.ResolveDIDKey(ctx, p.config.OwnerSignover.StaticDID)
				if err != nil {
					slog.Error("pipeline: failed to resolve static DID", "guid", guid, "did", p.config.OwnerSignover.StaticDID, "error", err)
					return fmt.Errorf("failed to resolve static DID: %w", err)
				}
				nextOwner = key
				didURL = recipientURL
				slog.Info("pipeline: resolved static DID", "guid", guid, "did", p.config.OwnerSignover.StaticDID, "voucher_url", didURL)
			} else if p.config.OwnerSignover.StaticPublicKey != "" {
				key, err := LoadPublicKeyFromPEM([]byte(p.config.OwnerSignover.StaticPublicKey))
				if err != nil {
					slog.Error("pipeline: failed to parse static public key", "guid", guid, "error", err)
					return fmt.Errorf("failed to parse static public key: %w", err)
				}
				nextOwner = key
			}
		case "dynamic":
			if p.ownerKeyService != nil {
				result, err := p.ownerKeyService.GetOwnerKey(ctx, serial, model)
				if err != nil {
					slog.Warn("pipeline: failed to get dynamic owner key", "guid", guid, "error", err)
				} else {
					nextOwner = result.PublicKey.(crypto.PublicKey)
					didURL = result.DIDURL
				}
			}
		}
	}

	// If we resolved a next owner key, compute its fingerprint for Pull API scoping.
	// Uses CBOR-based SHA-256 matching FDOKeyAuth.Result's KeyFingerprint (spec §10).
	if nextOwner != nil {
		ownerKeyFingerprint = FingerprintPublicKeyHex(nextOwner)
		slog.Info("pipeline: computed destination owner key fingerprint",
			"guid", guid,
			"owner_key_fingerprint", ownerKeyFingerprint,
		)
	}

	// Step 3: Sign voucher if configured
	if p.config.VoucherSigning.Mode != "" && nextOwner != nil {
		_, err := p.signingService.SignVoucher(ctx, voucher, nextOwner, serial, model, extraData)
		if err != nil {
			slog.Warn("pipeline: voucher signing failed, forwarding original voucher", "guid", guid, "error", err)
		} else {
			slog.Info("pipeline: voucher signed over to next owner", "guid", guid)
		}
	}

	// Step 4: Resolve destination if push is configured
	if p.pushService != nil && p.pushService.Enabled() {
		dest, err := p.destinationResolver.ResolveDestination(ctx, serial, model, guid, didURL, ownerKeyFingerprint)
		if err != nil {
			slog.Warn("pipeline: failed to resolve destination", "guid", guid, "error", err)
			// Store with no destination - will be handled later
			if err := p.storeTransmissionRecord(ctx, guid, filePath, serial, model, "", "", "", ownerKeyFingerprint); err != nil {
				slog.Error("pipeline: failed to store transmission record", "guid", guid, "error", err)
			}
			return nil
		}

		// Create transmission record with destination
		record := &VoucherTransmissionRecord{
			VoucherGUID:         guid,
			FilePath:            filePath,
			DestinationURL:      dest.URL,
			AuthToken:           dest.Token,
			DestinationSource:   dest.Source,
			Mode:                dest.Mode,
			Status:              transmissionStatusPending,
			SerialNumber:        serial,
			ModelNumber:         model,
			OwnerKeyFingerprint: ownerKeyFingerprint,
			Attempts:            0,
		}

		id, err := p.transmissionStore.CreatePending(ctx, record)
		if err != nil {
			slog.Error("pipeline: failed to create transmission record", "guid", guid, "error", err)
			return fmt.Errorf("failed to create transmission record: %w", err)
		}
		record.ID = id

		slog.Info("pipeline: transmission queued", "guid", guid, "destination", dest.URL, "source", dest.Source)

		// Attempt immediate push delivery
		if p.pushService != nil {
			if pushErr := p.pushService.AttemptRecord(ctx, record); pushErr != nil {
				slog.Warn("pipeline: immediate push failed (will retry)", "guid", guid, "error", pushErr)
			}
		}
	} else {
		// No push configured, just store the transmission record with no destination
		if err := p.storeTransmissionRecord(ctx, guid, filePath, serial, model, "", "", "", ownerKeyFingerprint); err != nil {
			slog.Error("pipeline: failed to store transmission record", "guid", guid, "error", err)
		}
	}

	return nil
}

// storeTransmissionRecord stores a transmission record with no destination
func (p *VoucherPipeline) storeTransmissionRecord(ctx context.Context, guid, filePath, serial, model, url, token, source, ownerKeyFingerprint string) error {
	record := &VoucherTransmissionRecord{
		VoucherGUID:         guid,
		FilePath:            filePath,
		DestinationURL:      url,
		AuthToken:           token,
		DestinationSource:   source,
		Status:              transmissionStatusPending,
		SerialNumber:        serial,
		ModelNumber:         model,
		OwnerKeyFingerprint: ownerKeyFingerprint,
		Attempts:            0,
	}

	_, err := p.transmissionStore.CreatePending(ctx, record)
	return err
}
