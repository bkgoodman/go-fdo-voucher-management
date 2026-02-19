// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"errors"
	"fmt"
)

// VoucherDestination represents a resolved delivery target for a voucher
type VoucherDestination struct {
	URL    string
	Token  string
	Source string
	Mode   string
}

// VoucherDestinationResolver resolves voucher delivery targets using callbacks, DIDs, and static config
type VoucherDestinationResolver struct {
	config           *Config
	callbackExecutor *ExternalCommandExecutor
	didResolver      *DIDResolver
}

// NewVoucherDestinationResolver constructs a resolver from config and dependencies
func NewVoucherDestinationResolver(cfg *Config, callbackExec *ExternalCommandExecutor, didResolver *DIDResolver) *VoucherDestinationResolver {
	return &VoucherDestinationResolver{
		config:           cfg,
		callbackExecutor: callbackExec,
		didResolver:      didResolver,
	}
}

// ResolveDestination determines which endpoint should receive a voucher
func (r *VoucherDestinationResolver) ResolveDestination(ctx context.Context, serial, model, guid string, didURL string) (*VoucherDestination, error) {
	if r == nil || r.config == nil {
		return nil, errors.New("destination resolver not configured")
	}

	// 1. Callback
	if r.config.DestinationCallback.Enabled {
		if dest, err := r.resolveViaCallback(ctx, serial, model, guid); err == nil && dest != nil {
			return dest, nil
		}
	}

	// 2. DID default
	if r.config.DIDPush.Enabled && didURL != "" {
		return &VoucherDestination{URL: didURL, Source: "did", Mode: r.config.PushService.Mode}, nil
	}

	// 3. Static fallback
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
