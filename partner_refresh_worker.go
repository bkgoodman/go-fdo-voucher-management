// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	fdodid "github.com/fido-device-onboard/go-fdo/did"
)

// PartnerRefreshWorker periodically refreshes DID documents for did:web partners.
type PartnerRefreshWorker struct {
	store       *PartnerStore
	didResolver *DIDResolver
	interval    time.Duration
	maxAge      time.Duration
}

// NewPartnerRefreshWorker creates a refresh worker.
// interval is how often to check for stale documents.
// maxAge is how old a cached document can be before it's refreshed.
func NewPartnerRefreshWorker(store *PartnerStore, resolver *DIDResolver, interval, maxAge time.Duration) *PartnerRefreshWorker {
	if interval <= 0 {
		interval = 1 * time.Hour
	}
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	return &PartnerRefreshWorker{
		store:       store,
		didResolver: resolver,
		interval:    interval,
		maxAge:      maxAge,
	}
}

// Start launches the refresh loop in a goroutine. It stops when ctx is cancelled.
func (w *PartnerRefreshWorker) Start(ctx context.Context) {
	go func() {
		slog.Info("partner refresh worker: started", "interval", w.interval, "max_age", w.maxAge)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		// Run once immediately on startup
		w.refreshAll(ctx)

		for {
			select {
			case <-ctx.Done():
				slog.Info("partner refresh worker: stopped")
				return
			case <-ticker.C:
				w.refreshAll(ctx)
			}
		}
	}()
}

// refreshAll fetches all did:web partners and refreshes stale documents.
func (w *PartnerRefreshWorker) refreshAll(ctx context.Context) {
	partners, err := w.store.ListDIDWebPartners(ctx)
	if err != nil {
		slog.Error("partner refresh worker: failed to list did:web partners", "error", err)
		return
	}

	if len(partners) == 0 {
		return
	}

	now := time.Now().Unix()
	refreshed := 0

	for _, p := range partners {
		// Skip if document is still fresh
		if p.DIDDocumentFetchedAt > 0 && (now-p.DIDDocumentFetchedAt) < int64(w.maxAge.Seconds()) {
			continue
		}

		if err := w.refreshPartner(ctx, p); err != nil {
			slog.Warn("partner refresh worker: failed to refresh",
				"partner", p.ID, "did", p.DIDURI, "error", err)
		} else {
			refreshed++
		}
	}

	if refreshed > 0 {
		slog.Info("partner refresh worker: refresh cycle complete", "refreshed", refreshed, "total", len(partners))
	}
}

// refreshPartner fetches a single partner's DID document and updates the store.
func (w *PartnerRefreshWorker) refreshPartner(ctx context.Context, p *Partner) error {
	// Resolve the DID to get the key and service endpoints
	key, recipientURL, err := w.didResolver.ResolveDIDKey(ctx, p.DIDURI)
	if err != nil {
		return err
	}

	// Export key as PEM
	publicKeyPEM, err := fdodid.ExportPublicKeyPEM(key)
	if err != nil {
		return err
	}

	// Re-fetch the raw DID document for caching
	// (ResolveDIDKey already parsed it, but we want the raw JSON too)
	docJSON := ""
	if w.didResolver != nil {
		docURL, urlErr := w.didResolver.WebDIDToURL(p.DIDURI)
		if urlErr == nil {
			rawDoc, fetchErr := w.fetchRawDocument(ctx, docURL)
			if fetchErr == nil {
				docJSON = rawDoc
			}
		}
	}

	// Extract pull URL from DID document if available
	pullURL := ""
	if docJSON != "" {
		var doc fdodid.Document
		if err := json.Unmarshal([]byte(docJSON), &doc); err == nil {
			for _, svc := range doc.Service {
				if svc.Type == fdodid.FDOVoucherHolderServiceType {
					pullURL = svc.ServiceEndpoint
					break
				}
			}
		}
	}

	slog.Info("partner refresh worker: refreshed DID document",
		"partner", p.ID, "did", p.DIDURI,
		"push_url", recipientURL, "pull_url", pullURL,
	)

	return w.store.UpdateDIDDocument(ctx, p.ID, docJSON, string(publicKeyPEM), recipientURL, pullURL, "")
}

// fetchRawDocument fetches the raw DID document JSON from the given URL.
func (w *PartnerRefreshWorker) fetchRawDocument(ctx context.Context, docURL string) (string, error) {
	client := w.didResolver.HTTPClient()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/did+ld+json, application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
