// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/fido-device-onboard/go-fdo"
	"github.com/fido-device-onboard/go-fdo/protocol"
	"github.com/fido-device-onboard/go-fdo/transfer"
)

// PushError is an alias for the library's transfer.PushError.
// It carries the HTTP status code and optional Retry-After duration
// so callers can classify transient vs permanent failures.
type PushError = transfer.PushError

// VoucherPushClient handles HTTP uploads of vouchers to remote owners.
// It delegates to the library's transfer.HTTPPushSender for the HTTP mechanics.
// When OwnerKey is set and the destination has no static token, the client
// performs an FDOKeyAuth handshake to obtain a bearer token before pushing.
type VoucherPushClient struct {
	sender *transfer.HTTPPushSender

	// OwnerKey is used for FDOKeyAuth when no static token is available.
	// If nil, FDOKeyAuth fallback is disabled.
	OwnerKey crypto.Signer

	// tokenCache caches FDOKeyAuth tokens per destination URL.
	tokenMu    sync.RWMutex
	tokenCache map[string]*cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// NewVoucherPushClient constructs a push client with sensible defaults.
func NewVoucherPushClient() *VoucherPushClient {
	return &VoucherPushClient{
		sender:     transfer.NewHTTPPushSender(),
		tokenCache: make(map[string]*cachedToken),
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

	token := dest.Token

	// If no static token, try FDOKeyAuth handshake
	if token == "" && c.OwnerKey != nil {
		var err2 error
		token, err2 = c.getOrRefreshToken(dest.URL)
		if err2 != nil {
			slog.Warn("push: FDOKeyAuth handshake failed, pushing without token",
				"destination", dest.URL,
				"error", err2,
			)
			// Fall through — some receivers may not require auth
		}
	}

	td := transfer.PushDestination{
		URL:   dest.URL,
		Token: token,
	}

	return c.sender.Push(ctx, td, data)
}

// getOrRefreshToken returns a cached token for the destination if still valid,
// otherwise performs an FDOKeyAuth handshake to obtain a new one.
func (c *VoucherPushClient) getOrRefreshToken(destURL string) (string, error) {
	// Check cache first
	c.tokenMu.RLock()
	if ct, ok := c.tokenCache[destURL]; ok && time.Now().Before(ct.expiresAt.Add(-30*time.Second)) {
		c.tokenMu.RUnlock()
		return ct.token, nil
	}
	c.tokenMu.RUnlock()

	// Perform FDOKeyAuth handshake
	// Parse the destination URL to extract base URL and path prefix
	parsedURL, err := url.Parse(destURL)
	if err != nil {
		return "", fmt.Errorf("invalid destination URL %s: %w", destURL, err)
	}

	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
	pathPrefix := parsedURL.Path

	client := &transfer.FDOKeyAuthClient{
		CallerKey:  c.OwnerKey,
		HashAlg:    protocol.Sha256Hash,
		BaseURL:    baseURL,
		PathPrefix: pathPrefix,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}

	result, err := client.Authenticate()
	if err != nil {
		return "", fmt.Errorf("FDOKeyAuth handshake with %s failed: %w", destURL, err)
	}

	// Cache the token
	expiresAt := time.Unix(int64(result.TokenExpiresAt), 0)
	c.tokenMu.Lock()
	c.tokenCache[destURL] = &cachedToken{
		token:     result.SessionToken,
		expiresAt: expiresAt,
	}
	c.tokenMu.Unlock()

	slog.Info("push: FDOKeyAuth token obtained",
		"destination", destURL,
		"expires_at", expiresAt,
	)

	return result.SessionToken, nil
}
