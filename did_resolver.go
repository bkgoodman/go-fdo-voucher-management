// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	fdodid "github.com/fido-device-onboard/go-fdo/did"
	"github.com/mr-tron/base58"
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
		key, err := parseDIDKey(didURI)
		// did:key has no service endpoints, so voucherRecipientURL is always empty
		return key, "", err
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

// parseDIDKey decodes a did:key URI into a crypto.PublicKey.
// Supports P-256 and P-384 keys encoded per the did:key spec v0.9:
//
//	did:key:z<base58btc(multicodec-varint + compressed-ec-point)>
//
// Multicodec varint prefixes:
//   - P-256: 0x80 0x24  (code 0x1200)
//   - P-384: 0x81 0x24  (code 0x1201)
func parseDIDKey(didURI string) (crypto.PublicKey, error) {
	if !strings.HasPrefix(didURI, "did:key:z") {
		return nil, fmt.Errorf("did:key URI must start with 'did:key:z': %q", didURI)
	}

	// Strip "did:key:z" — the "z" is the multibase prefix for base58-btc
	encoded := strings.TrimPrefix(didURI, "did:key:z")

	decoded, err := base58.Decode(encoded)
	if err != nil {
		return nil, fmt.Errorf("did:key: base58 decode failed: %w", err)
	}

	if len(decoded) < 3 {
		return nil, fmt.Errorf("did:key: decoded data too short (%d bytes)", len(decoded))
	}

	// Parse multicodec varint prefix (2 bytes for P-256/P-384)
	var curve elliptic.Curve
	var keyBytes []byte
	switch {
	case decoded[0] == 0x80 && decoded[1] == 0x24:
		// P-256 (multicodec 0x1200)
		curve = elliptic.P256()
		keyBytes = decoded[2:]
	case decoded[0] == 0x81 && decoded[1] == 0x24:
		// P-384 (multicodec 0x1201)
		curve = elliptic.P384()
		keyBytes = decoded[2:]
	default:
		return nil, fmt.Errorf("did:key: unsupported multicodec prefix 0x%02x 0x%02x", decoded[0], decoded[1])
	}

	// Decompress the EC point
	x, y := decompressPoint(curve, keyBytes)
	if x == nil {
		return nil, fmt.Errorf("did:key: failed to decompress EC point for %s", curve.Params().Name)
	}

	pub := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
	if !curve.IsOnCurve(x, y) {
		return nil, fmt.Errorf("did:key: decoded point is not on curve %s", curve.Params().Name)
	}

	return pub, nil
}

// decompressPoint decompresses a SEC1-compressed EC point (33 or 49 bytes)
// into (x, y) coordinates. The first byte is 0x02 (even y) or 0x03 (odd y).
func decompressPoint(curve elliptic.Curve, data []byte) (*big.Int, *big.Int) {
	byteLen := (curve.Params().BitSize + 7) / 8
	if len(data) != 1+byteLen {
		return nil, nil
	}
	if data[0] != 0x02 && data[0] != 0x03 {
		return nil, nil
	}

	// x coordinate
	x := new(big.Int).SetBytes(data[1:])
	p := curve.Params().P

	// y² = x³ + ax + b  (for NIST curves, a = -3)
	// y² = x³ - 3x + b (mod p)
	x3 := new(big.Int).Mul(x, x)
	x3.Mul(x3, x)
	x3.Mod(x3, p)

	threeX := new(big.Int).Mul(big.NewInt(3), x)
	threeX.Mod(threeX, p)

	y2 := new(big.Int).Sub(x3, threeX)
	y2.Add(y2, curve.Params().B)
	y2.Mod(y2, p)

	// y = sqrt(y²) mod p
	y := new(big.Int).ModSqrt(y2, p)
	if y == nil {
		return nil, nil
	}

	// Choose the correct y based on the sign bit
	isOdd := y.Bit(0) == 1
	wantOdd := data[0] == 0x03
	if isOdd != wantOdd {
		y.Sub(p, y)
	}

	return x, y
}
