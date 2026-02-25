// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"time"
)

// VoucherPushService orchestrates destination resolution, persistence, and delivery attempts
type VoucherPushService struct {
	config   *Config
	store    *VoucherTransmissionStore
	resolver *VoucherDestinationResolver
	client   *VoucherPushClient
}

// NewVoucherPushService constructs a push service instance
func NewVoucherPushService(cfg *Config, store *VoucherTransmissionStore, resolver *VoucherDestinationResolver, client *VoucherPushClient) *VoucherPushService {
	return &VoucherPushService{
		config:   cfg,
		store:    store,
		resolver: resolver,
		client:   client,
	}
}

// Enabled reports whether any push mechanism is configured
func (s *VoucherPushService) Enabled() bool {
	if s == nil || s.config == nil {
		return false
	}
	return s.config.DestinationCallback.Enabled || s.config.DIDPush.Enabled || (s.config.PushService.Enabled && s.config.PushService.URL != "")
}

// ProcessVoucher resolves a destination, records a transmission row, and performs an initial push attempt
func (s *VoucherPushService) ProcessVoucher(ctx context.Context, serial, model, guid, filePath, didURL string) error {
	if !s.Enabled() {
		return nil
	}
	if s.store == nil || s.resolver == nil || s.client == nil {
		return fmt.Errorf("voucher push service is not fully configured")
	}

	dest, err := s.resolver.ResolveDestination(ctx, serial, model, guid, didURL)
	if err != nil {
		return fmt.Errorf("failed to resolve voucher destination: %w", err)
	}

	record := &VoucherTransmissionRecord{
		VoucherGUID:       guid,
		FilePath:          filePath,
		DestinationURL:    dest.URL,
		AuthToken:         dest.Token,
		DestinationSource: dest.Source,
		Mode:              dest.Mode,
		Status:            transmissionStatusPending,
		SerialNumber:      serial,
		ModelNumber:       model,
	}

	id, err := s.store.CreatePending(ctx, record)
	if err != nil {
		return fmt.Errorf("failed to persist voucher transmission metadata: %w", err)
	}
	record.ID = id
	slog.Info("voucher transmission queued",
		"guid", guid,
		"destination", dest.URL,
		"source", dest.Source,
		"file", filePath,
	)

	return s.AttemptRecord(ctx, record)
}

// AttemptRecord replays delivery for an existing transmission record
func (s *VoucherPushService) AttemptRecord(ctx context.Context, rec *VoucherTransmissionRecord) error {
	if s == nil || s.client == nil || s.store == nil {
		return fmt.Errorf("voucher push service not fully configured")
	}
	if rec == nil {
		return fmt.Errorf("transmission record cannot be nil")
	}
	if rec.FilePath == "" {
		return fmt.Errorf("transmission record missing voucher file path")
	}
	if rec.DestinationURL == "" {
		return fmt.Errorf("transmission record missing destination URL")
	}

	rec.Attempts++
	dest := &VoucherDestination{
		URL:    rec.DestinationURL,
		Token:  rec.AuthToken,
		Source: rec.DestinationSource,
		Mode:   rec.Mode,
	}

	start := time.Now()
	err := s.client.Push(ctx, dest, rec.FilePath, rec.SerialNumber, rec.ModelNumber, rec.VoucherGUID)
	if err != nil {
		// Classify error: permanent 4xx (except 429) → fail immediately.
		// Transient (5xx, 429, network) → retry with exponential backoff.
		var pushErr *PushError
		permanent := false
		var serverRetryAfter time.Duration
		if errors.As(err, &pushErr) {
			permanent = !pushErr.IsTransient()
			serverRetryAfter = pushErr.RetryAfter
		}

		status := transmissionStatusPending
		retryAfter := time.Time{}
		if permanent {
			status = transmissionStatusFailed
			slog.Warn("voucher transmission permanently failed (non-retryable HTTP status)",
				"guid", rec.VoucherGUID,
				"destination", rec.DestinationURL,
				"attempts", rec.Attempts,
				"error", err,
			)
		} else if rec.Attempts >= s.maxAttempts() {
			status = transmissionStatusFailed
		} else {
			// Compute next retry time: exponential backoff with ±25% jitter,
			// but honor Retry-After from the server if it's longer.
			backoff := backoffDuration(rec.Attempts, s.retryInterval())
			if serverRetryAfter > backoff {
				backoff = serverRetryAfter
			}
			retryAfter = time.Now().UTC().Add(backoff)
		}

		if markErr := s.store.MarkAttempt(ctx, rec.ID, status, rec.Attempts, retryAfter, err.Error(), false); markErr != nil {
			slog.Error("failed to update transmission after error", "guid", rec.VoucherGUID, "error", markErr)
		}
		if !permanent {
			slog.Warn("voucher transmission attempt failed",
				"guid", rec.VoucherGUID,
				"destination", rec.DestinationURL,
				"attempts", rec.Attempts,
				"status", status,
				"retry_after", retryAfter,
				"duration", time.Since(start),
				"error", err,
			)
		}
		if status == transmissionStatusFailed {
			return fmt.Errorf("voucher push failed after %d attempts: %w", rec.Attempts, err)
		}
		return err
	}

	if err := s.store.MarkAttempt(ctx, rec.ID, transmissionStatusSucceeded, rec.Attempts, time.Time{}, "", true); err != nil {
		return fmt.Errorf("failed to update voucher transmission (success): %w", err)
	}
	if s.config.PushService.DeleteAfterSuccess {
		if err := os.Remove(rec.FilePath); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to delete voucher file after success", "path", rec.FilePath, "error", err)
		}
	}
	slog.Info("voucher transmission delivered",
		"guid", rec.VoucherGUID,
		"destination", rec.DestinationURL,
		"attempts", rec.Attempts,
		"duration", time.Since(start),
	)
	return nil
}

func (s *VoucherPushService) retryInterval() time.Duration {
	if s == nil || s.config == nil {
		return 8 * time.Hour
	}
	if s.config.PushService.RetryInterval > 0 {
		return s.config.PushService.RetryInterval
	}
	if s.config.RetryWorker.RetryInterval > 0 {
		return s.config.RetryWorker.RetryInterval
	}
	return 8 * time.Hour
}

func (s *VoucherPushService) maxAttempts() int {
	if s == nil || s.config == nil {
		return 5
	}
	if s.config.PushService.MaxAttempts > 0 {
		return s.config.PushService.MaxAttempts
	}
	if s.config.RetryWorker.MaxAttempts > 0 {
		return s.config.RetryWorker.MaxAttempts
	}
	return 5
}

// backoffDuration computes exponential backoff with ±25% jitter.
// Base delay doubles each attempt: base, 2*base, 4*base, 8*base, ...
// Capped at 24 hours.
func backoffDuration(attempt int, base time.Duration) time.Duration {
	if base <= 0 {
		base = time.Minute
	}
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 20 {
		shift = 20
	}
	d := base * (1 << shift)
	maxBackoff := 24 * time.Hour
	if d > maxBackoff {
		d = maxBackoff
	}
	// Apply ±25% jitter
	jitter := float64(d) * 0.25 * (2*rand.Float64() - 1) //nolint:gosec // jitter doesn't need crypto rand
	return d + time.Duration(jitter)
}
