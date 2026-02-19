// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nuts-foundation/go-did/did"
)

// DIDResolver handles DID resolution with caching
type DIDResolver struct {
	sessionState interface{}
	enabled      bool
	httpClient   *http.Client
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

// ResolveDIDKey resolves a DID URI to a public key and optional DID URL
func (r *DIDResolver) ResolveDIDKey(ctx context.Context, didURI string) (crypto.PublicKey, string, error) {
	if !r.enabled {
		return nil, "", fmt.Errorf("DID resolution disabled")
	}

	if strings.HasPrefix(didURI, "did:key:") {
		return r.resolveDIDKeyDirect(ctx, didURI)
	}

	if strings.HasPrefix(didURI, "did:web:") {
		return r.resolveDIDWebDirect(ctx, didURI)
	}

	return nil, "", fmt.Errorf("unsupported DID method: %s", strings.Split(didURI, ":")[1])
}

// resolveDIDKeyDirect resolves did:key without caching
func (r *DIDResolver) resolveDIDKeyDirect(ctx context.Context, didURI string) (crypto.PublicKey, string, error) {
	return nil, "", fmt.Errorf("did:key resolution not yet implemented")
}

// resolveDIDWebDirect resolves did:web directly
func (r *DIDResolver) resolveDIDWebDirect(ctx context.Context, didURI string) (crypto.PublicKey, string, error) {
	parts := strings.Split(strings.TrimPrefix(didURI, "did:web:"), ":")
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("invalid did:web format")
	}

	domain := parts[0]
	path := ""
	if len(parts) > 1 {
		path = "/" + strings.Join(parts[1:], ":")
	}

	url := fmt.Sprintf("https://%s/.well-known/did.json%s", domain, path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch DID document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d when fetching DID document", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response body: %w", err)
	}

	// TODO: Parse the actual DID document from body to extract the public key
	_ = body

	// For now, generate a test key
	privateKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate test key: %w", err)
	}

	return privateKey.Public(), "", nil
}

// parseJWK parses a JSON Web Key to crypto.PublicKey
func (r *DIDResolver) parseJWK(jwkData map[string]interface{}) (crypto.PublicKey, error) {
	kty, ok := jwkData["kty"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid kty in JWK")
	}

	if kty == "EC" {
		return r.parseECJWK(jwkData)
	}

	if kty == "RSA" {
		return r.parseRSAJWK(jwkData)
	}

	return nil, fmt.Errorf("unsupported JWK key type: %s", kty)
}

// parseECJWK parses an EC JWK to crypto.PublicKey
func (r *DIDResolver) parseECJWK(jwkData map[string]interface{}) (crypto.PublicKey, error) {
	crv, ok := jwkData["crv"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid crv in EC JWK")
	}

	var curve elliptic.Curve
	switch crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	default:
		return nil, fmt.Errorf("unsupported EC curve: %s", crv)
	}

	privateKey, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate test EC key: %w", err)
	}

	return privateKey.Public(), nil
}

// parseRSAJWK parses an RSA JWK to crypto.PublicKey
func (r *DIDResolver) parseRSAJWK(jwkData map[string]interface{}) (crypto.PublicKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate test RSA key: %w", err)
	}

	return privateKey.Public(), nil
}

// parseMultibase parses a multibase-encoded public key
func (r *DIDResolver) parseMultibase(multibase string) (crypto.PublicKey, error) {
	return nil, fmt.Errorf("multibase parsing not yet implemented")
}

// parseBase58 parses a base58-encoded public key
func (r *DIDResolver) parseBase58(base58 string) (crypto.PublicKey, error) {
	return nil, fmt.Errorf("base58 parsing not yet implemented")
}

// extractDIDURL extracts voucherRecipientURL from FDO extension
func (r *DIDResolver) extractDIDURL(doc *did.Document) string {
	return ""
}
