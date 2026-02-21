// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	fdodid "github.com/fido-device-onboard/go-fdo/did"
)

// DIDResolver handles DID resolution with caching
type DIDResolver struct {
	sessionState interface{}
	enabled      bool
	httpClient   *http.Client
	// InsecureHTTP allows resolving did:web over HTTP instead of HTTPS.
	// This is for local development/testing only.
	InsecureHTTP bool
}

// NewDIDResolver creates a new DID resolver
func NewDIDResolver(sessionState interface{}, enabled bool) *DIDResolver {
	return &DIDResolver{
		sessionState: sessionState,
		enabled:      enabled,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ResolveDIDKey resolves a DID URI to a public key and optional voucher recipient URL
func (r *DIDResolver) ResolveDIDKey(ctx context.Context, didURI string) (crypto.PublicKey, string, error) {
	if !r.enabled {
		return nil, "", fmt.Errorf("DID resolution disabled")
	}

	if strings.HasPrefix(didURI, "did:web:") {
		return r.resolveDIDWebDirect(ctx, didURI)
	}

	if strings.HasPrefix(didURI, "did:key:") {
		return nil, "", fmt.Errorf("did:key resolution not yet implemented")
	}

	return nil, "", fmt.Errorf("unsupported DID method in %q", didURI)
}

// resolveDIDWebDirect resolves did:web by fetching the DID Document and
// extracting the public key and FDOVoucherRecipient service endpoint.
func (r *DIDResolver) resolveDIDWebDirect(ctx context.Context, didURI string) (crypto.PublicKey, string, error) {
	// Build the document URL from the did:web URI.
	docURL, err := r.webDIDToURL(didURI)
	if err != nil {
		return nil, "", err
	}

	slog.Debug("DID resolver: fetching DID document", "did", didURI, "url", docURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request for %s: %w", docURL, err)
	}
	req.Header.Set("Accept", "application/did+ld+json, application/json")

	client := r.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch DID document from %s: %w", docURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d fetching DID document from %s", resp.StatusCode, docURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", fmt.Errorf("failed to read DID document: %w", err)
	}

	var doc fdodid.Document
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, "", fmt.Errorf("failed to parse DID document: %w", err)
	}

	// Verify the document ID matches the requested DID
	if doc.ID != didURI {
		return nil, "", fmt.Errorf("DID document ID %q does not match requested %q", doc.ID, didURI)
	}

	// Extract public key from first verification method
	if len(doc.VerificationMethod) == 0 {
		return nil, "", fmt.Errorf("DID document has no verification methods")
	}

	vm := doc.VerificationMethod[0]
	if vm.PublicKeyJwk == nil {
		return nil, "", fmt.Errorf("verification method %q has no publicKeyJwk", vm.ID)
	}

	pub, err := fdodid.JWKToPublicKey(vm.PublicKeyJwk)
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse JWK from verification method %q: %w", vm.ID, err)
	}

	// Look for FDOVoucherRecipient service endpoint
	var voucherRecipientURL string
	for _, svc := range doc.Service {
		if svc.Type == fdodid.FDOVoucherRecipientServiceType {
			voucherRecipientURL = svc.ServiceEndpoint
			break
		}
	}

	slog.Info("DID resolver: resolved DID",
		"did", didURI,
		"voucher_recipient_url", voucherRecipientURL,
	)

	return pub, voucherRecipientURL, nil
}

// webDIDToURL converts a did:web URI to the HTTP(S) URL where the DID Document
// should be served. Uses HTTPS by default; uses HTTP if InsecureHTTP is set.
func (r *DIDResolver) webDIDToURL(didURI string) (string, error) {
	if !strings.HasPrefix(didURI, "did:web:") {
		return "", fmt.Errorf("not a did:web URI: %s", didURI)
	}

	specific := strings.TrimPrefix(didURI, "did:web:")
	parts := strings.Split(specific, ":")

	// First part is the host (percent-decoded)
	host, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid did:web host: %w", err)
	}

	scheme := "https"
	if r.InsecureHTTP {
		scheme = "http"
	}

	if len(parts) == 1 {
		return scheme + "://" + host + "/.well-known/did.json", nil
	}

	path := strings.Join(parts[1:], "/")
	return scheme + "://" + host + "/" + path + "/did.json", nil
}
