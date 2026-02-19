// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"log/slog"
	"time"
)

// VoucherRetryWorker polls the transmission store for pending vouchers and retries delivery
type VoucherRetryWorker struct {
	config      *Config
	store       *VoucherTransmissionStore
	pushService *VoucherPushService
	interval    time.Duration
	batchSize   int
}

// NewVoucherRetryWorker constructs a retry worker using config defaults
func NewVoucherRetryWorker(cfg *Config, store *VoucherTransmissionStore, pushService *VoucherPushService) *VoucherRetryWorker {
	interval := 8 * time.Hour
	batchSize := 10
	if cfg != nil {
		if cfg.RetryWorker.RetryInterval > 0 {
			interval = cfg.RetryWorker.RetryInterval
		} else if cfg.PushService.RetryInterval > 0 {
			interval = cfg.PushService.RetryInterval
		}
		if cfg.RetryWorker.MaxAttempts > 0 {
			batchSize = cfg.RetryWorker.MaxAttempts
		}
	}
	if batchSize < 5 {
		batchSize = 5
	}
	if batchSize > 50 {
		batchSize = 50
	}
	return &VoucherRetryWorker{
		config:      cfg,
		store:       store,
		pushService: pushService,
		interval:    interval,
		batchSize:   batchSize,
	}
}

// Enabled reports whether the worker should run
func (w *VoucherRetryWorker) Enabled() bool {
	if w == nil || w.config == nil {
		return false
	}
	if !w.config.RetryWorker.Enabled {
		return false
	}
	if w.store == nil || w.pushService == nil || !w.pushService.Enabled() {
		return false
	}
	return true
}

// Start launches the retry loop until the provided context is cancelled
func (w *VoucherRetryWorker) Start(ctx context.Context) {
	if !w.Enabled() {
		slog.Info("voucher retry worker disabled")
		return
	}
	go w.run(ctx)
}

func (w *VoucherRetryWorker) run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	slog.Info("voucher retry worker started", "interval", w.interval, "batch", w.batchSize)
	w.processBatch(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("voucher retry worker stopping", "reason", ctx.Err())
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

func (w *VoucherRetryWorker) processBatch(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	records, err := w.store.PendingForRetry(ctx, w.batchSize)
	if err != nil {
		slog.Error("retry worker failed to query transmissions", "error", err)
		return
	}
	if len(records) == 0 {
		slog.Debug("retry worker found no pending transmissions")
		return
	}

	slog.Info("retry worker processing transmissions", "count", len(records))
	for i := range records {
		if ctx.Err() != nil {
			return
		}
		rec := records[i]
		slog.Debug("retry worker attempting voucher", "id", rec.ID, "guid", rec.VoucherGUID, "destination", rec.DestinationURL, "attempts", rec.Attempts+1)
		if err := w.pushService.AttemptRecord(ctx, &rec); err != nil {
			slog.Warn("voucher retry attempt returned error", "id", rec.ID, "guid", rec.VoucherGUID, "error", err)
			continue
		}
		slog.Debug("retry worker attempt completed", "id", rec.ID, "guid", rec.VoucherGUID)
	}
}
