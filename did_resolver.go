// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fido-device-onboard/go-fdo/did"
)

// DIDResolver handles DID resolution with an enabled/disabled gate.
// It delegates all resolution logic to the library's did.Resolver.
type DIDResolver struct {
	resolver *did.Resolver
	enabled  bool
}

// NewDIDResolver creates a new DID resolver.
func NewDIDResolver(_ interface{}, enabled bool) *DIDResolver {
	return &DIDResolver{
		resolver: &did.Resolver{
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		},
		enabled: enabled,
	}
}

// SetInsecureHTTP enables HTTP (instead of HTTPS) for did:web resolution.
// This is for local development/testing only.
func (r *DIDResolver) SetInsecureHTTP(insecure bool) {
	r.resolver.InsecureHTTP = insecure
}

// ResolveDIDKey resolves a DID URI to a public key and optional voucher recipient URL.
func (r *DIDResolver) ResolveDIDKey(ctx context.Context, didURI string) (crypto.PublicKey, string, error) {
	if !r.enabled {
		return nil, "", fmt.Errorf("DID resolution disabled")
	}

	result, err := r.resolver.Resolve(ctx, didURI)
	if err != nil {
		return nil, "", err
	}

	slog.Info("DID resolver: resolved DID",
		"did", didURI,
		"voucher_recipient_url", result.VoucherRecipientURL,
	)

	return result.PublicKey, result.VoucherRecipientURL, nil
}

// WebDIDToURL converts a did:web URI to the document URL, respecting InsecureHTTP.
func (r *DIDResolver) WebDIDToURL(didURI string) (string, error) {
	docURL, err := did.WebDIDToURL(didURI)
	if err != nil {
		return "", err
	}
	if r.resolver.InsecureHTTP {
		docURL = strings.Replace(docURL, "https://", "http://", 1)
	}
	return docURL, nil
}

// HTTPClient returns the underlying HTTP client for direct document fetching.
func (r *DIDResolver) HTTPClient() *http.Client {
	if r.resolver.HTTPClient != nil {
		return r.resolver.HTTPClient
	}
	return http.DefaultClient
}
