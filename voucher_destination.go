// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// VoucherDestination represents a resolved delivery target for a voucher
type VoucherDestination struct {
	URL    string
	Token  string
	Source string
	Mode   string
}

// VoucherDestinationResolver resolves voucher delivery targets using callbacks, DIDs, partners, and static config
type VoucherDestinationResolver struct {
	config           *Config
	callbackExecutor *ExternalCommandExecutor
	didResolver      *DIDResolver
	partnerStore     *PartnerStore
}

// NewVoucherDestinationResolver constructs a resolver from config and dependencies
func NewVoucherDestinationResolver(cfg *Config, callbackExec *ExternalCommandExecutor, didResolver *DIDResolver, partnerStore *PartnerStore) *VoucherDestinationResolver {
	return &VoucherDestinationResolver{
		config:           cfg,
		callbackExecutor: callbackExec,
		didResolver:      didResolver,
		partnerStore:     partnerStore,
	}
}

// ResolveDestination determines which endpoint should receive a voucher.
// ownerKeyFingerprint is the hex SHA-256 of the next owner's key (for partner lookup).
func (r *VoucherDestinationResolver) ResolveDestination(ctx context.Context, serial, model, guid string, didURL string, ownerKeyFingerprint string) (*VoucherDestination, error) {
	if r == nil || r.config == nil {
		return nil, errors.New("destination resolver not configured")
	}

	// 1. Callback
	if r.config.DestinationCallback.Enabled {
		if dest, err := r.resolveViaCallback(ctx, serial, model, guid); err == nil && dest != nil {
			return dest, nil
		}
	}

	// 2. Partner lookup: if the next owner's key fingerprint matches an enrolled
	// partner with a push URL, route the voucher to that partner.
	if r.partnerStore != nil && ownerKeyFingerprint != "" {
		if dest := r.resolveViaPartner(ctx, ownerKeyFingerprint); dest != nil {
			return dest, nil
		}
	}

	// 3. DID default
	if r.config.DIDPush.Enabled && didURL != "" {
		return &VoucherDestination{URL: didURL, Source: "did", Mode: r.config.PushService.Mode}, nil
	}

	// 4. Static fallback
	if r.config.PushService.Enabled && r.config.PushService.URL != "" {
		return &VoucherDestination{
			URL:    r.config.PushService.URL,
			Token:  r.config.PushService.AuthToken,
			Source: "static",
			Mode:   r.config.PushService.Mode,
		}, nil
	}

	return nil, fmt.Errorf("no voucher destination available for GUID %s", guid)
}

// resolveViaPartner looks up an enrolled partner by owner key fingerprint.
// If the partner has a push URL, it is returned as the destination.
func (r *VoucherDestinationResolver) resolveViaPartner(ctx context.Context, ownerKeyFingerprint string) *VoucherDestination {
	p, err := r.partnerStore.GetByFingerprint(ctx, ownerKeyFingerprint)
	if err != nil || p == nil {
		return nil
	}
	if !p.CanReceiveVouchers {
		slog.Debug("destination resolver: partner matched but lacks can_receive_vouchers", "partner", p.ID)
		return nil
	}
	if p.PushURL == "" {
		slog.Debug("destination resolver: partner matched but has no push URL", "partner", p.ID)
		return nil
	}
	slog.Info("destination resolver: routed to partner", "partner", p.ID, "push_url", p.PushURL)
	return &VoucherDestination{
		URL:    p.PushURL,
		Token:  p.AuthToken,
		Source: "partner:" + p.ID,
		Mode:   r.config.PushService.Mode,
	}
}

func (r *VoucherDestinationResolver) resolveViaCallback(ctx context.Context, serial, model, guid string) (*VoucherDestination, error) {
	if r.callbackExecutor == nil {
		return nil, fmt.Errorf("destination callback enabled but executor is nil")
	}

	vars := map[string]string{
		"serialno": serial,
		"model":    model,
		"guid":     guid,
	}

	output, err := r.callbackExecutor.Execute(ctx, vars)
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, fmt.Errorf("destination callback returned empty output")
	}

	return &VoucherDestination{URL: output, Source: "callback", Mode: r.config.PushService.Mode}, nil
}
