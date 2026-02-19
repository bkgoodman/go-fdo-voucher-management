// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/fido-device-onboard/go-fdo"
)

// VoucherSigningService handles voucher signing operations
type VoucherSigningService struct {
	mode     string
	executor *ExternalCommandExecutor
}

// NewVoucherSigningService creates a new voucher signing service
func NewVoucherSigningService(mode string, externalCommand string, timeout time.Duration) *VoucherSigningService {
	return &VoucherSigningService{
		mode:     mode,
		executor: NewExternalCommandExecutor(externalCommand, timeout),
	}
}

// SignVoucher signs a voucher based on the configured mode
func (s *VoucherSigningService) SignVoucher(ctx context.Context, voucher *fdo.Voucher, nextOwner crypto.PublicKey, serial, model string, extraData map[int][]byte) (*fdo.Voucher, error) {
	if s.mode == "" || s.mode == "internal" {
		return s.signVoucherInternal(ctx, voucher, nextOwner, extraData)
	}

	return nil, fmt.Errorf("unsupported voucher signing mode: %s", s.mode)
}

// signVoucherInternal extends voucher to next owner without a private key
func (s *VoucherSigningService) signVoucherInternal(ctx context.Context, voucher *fdo.Voucher, nextOwner crypto.PublicKey, extraData map[int][]byte) (*fdo.Voucher, error) {
	if nextOwner == nil {
		return voucher, nil
	}

	var extendedVoucher *fdo.Voucher
	var err error

	switch key := nextOwner.(type) {
	case *ecdsa.PublicKey:
		extendedVoucher, err = fdo.ExtendVoucher(voucher, nil, key, extraData)
	case *rsa.PublicKey:
		extendedVoucher, err = fdo.ExtendVoucher(voucher, nil, key, extraData)
	case []*x509.Certificate:
		extendedVoucher, err = fdo.ExtendVoucher(voucher, nil, key, extraData)
	default:
		return nil, fmt.Errorf("unsupported nextOwner key type: %T", nextOwner)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to extend voucher: %w", err)
	}

	return extendedVoucher, nil
}
