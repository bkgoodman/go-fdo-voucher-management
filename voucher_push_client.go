// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fido-device-onboard/go-fdo"
	"github.com/fido-device-onboard/go-fdo/transfer"
)

// PushError is an alias for the library's transfer.PushError.
// It carries the HTTP status code and optional Retry-After duration
// so callers can classify transient vs permanent failures.
type PushError = transfer.PushError

// VoucherPushClient handles HTTP uploads of vouchers to remote owners.
// It delegates to the library's transfer.HTTPPushSender for the HTTP mechanics.
type VoucherPushClient struct {
	sender *transfer.HTTPPushSender
}

// NewVoucherPushClient constructs a push client with sensible defaults.
func NewVoucherPushClient() *VoucherPushClient {
	return &VoucherPushClient{
		sender: transfer.NewHTTPPushSender(),
	}
}

// Push attempts to upload the voucher file to the destination URL.
// It reads the file, parses the voucher, and delegates to the library sender.
func (c *VoucherPushClient) Push(ctx context.Context, dest *VoucherDestination, filePath, serial, model, guid string) error {
	if c == nil {
		return fmt.Errorf("push client not configured")
	}
	if dest == nil || dest.URL == "" {
		return fmt.Errorf("destination missing URL")
	}

	raw, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read voucher file %s: %w", filePath, err)
	}

	// Parse the voucher so the library sender can encode it properly.
	ov, err := fdo.ParseVoucherString(string(raw))
	if err != nil {
		return fmt.Errorf("failed to parse voucher from %s: %w", filePath, err)
	}

	data := &transfer.VoucherData{
		VoucherInfo: transfer.VoucherInfo{
			GUID:         guid,
			SerialNumber: serial,
			ModelNumber:  model,
		},
		Voucher: ov,
	}

	td := transfer.PushDestination{
		URL:   dest.URL,
		Token: dest.Token,
	}

	return c.sender.Push(ctx, td, data)
}
