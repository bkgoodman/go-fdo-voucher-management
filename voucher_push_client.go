// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// PushError represents an HTTP error from a voucher push attempt.
// It carries the status code and optional Retry-After duration so
// callers can classify transient vs permanent failures.
type PushError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration // parsed from Retry-After header; zero if absent
}

func (e *PushError) Error() string {
	return fmt.Sprintf("voucher push returned %d: %s", e.StatusCode, e.Body)
}

// IsTransient returns true for errors that are worth retrying:
// 429 Too Many Requests, 5xx server errors, and network errors.
// 4xx (except 429) are permanent — the request itself is wrong.
func (e *PushError) IsTransient() bool {
	if e.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return e.StatusCode >= 500
}

// parseRetryAfter parses the Retry-After header value.
// Supports both seconds (integer) and HTTP-date formats.
func parseRetryAfter(val string) time.Duration {
	if val == "" {
		return 0
	}
	if secs, err := strconv.Atoi(val); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(val); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// VoucherPushClient handles HTTP uploads of vouchers to remote owners
type VoucherPushClient struct {
	httpClient *http.Client
}

// NewVoucherPushClient constructs a push client with sensible defaults
func NewVoucherPushClient() *VoucherPushClient {
	return &VoucherPushClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Push attempts to upload the voucher file to the destination URL
func (c *VoucherPushClient) Push(ctx context.Context, dest *VoucherDestination, filePath, serial, model, guid string) error {
	if c == nil {
		return fmt.Errorf("push client not configured")
	}
	if dest == nil || dest.URL == "" {
		return fmt.Errorf("destination missing URL")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open voucher file %s: %w", filePath, err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("voucher", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("failed to create multipart part: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("failed to copy voucher data: %w", err)
	}

	_ = writer.WriteField("serial", serial)
	_ = writer.WriteField("model", model)
	_ = writer.WriteField("guid", guid)

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest.URL, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if dest.Token != "" {
		req.Header.Set("Authorization", "Bearer "+dest.Token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("voucher push request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return &PushError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	return nil
}
