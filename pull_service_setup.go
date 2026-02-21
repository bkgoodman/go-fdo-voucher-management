// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/fido-device-onboard/go-fdo/protocol"
	"github.com/fido-device-onboard/go-fdo/transfer"
)

// setupPullService configures and registers the PullAuth and Pull API handlers.
func setupPullService(config *Config, mux *http.ServeMux, signingService *VoucherSigningService) {
	// Generate an ephemeral Holder key for signing PullAuth challenges.
	// This key is only used for the challenge-response handshake, not for
	// voucher signing. It is regenerated on each server start.
	holderKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		slog.Error("pull service: failed to generate holder key", "error", err)
		return
	}

	sessionStore := transfer.NewSessionStore(
		config.PullService.SessionTTL,
		config.PullService.MaxSessions,
	)

	tokenTTL := config.PullService.TokenTTL
	if tokenTTL == 0 {
		tokenTTL = 1 * time.Hour
	}

	tokenStore := newPullTokenStore(tokenTTL)

	pullAuthServer := &transfer.PullAuthServer{
		HolderKey:              holderKey,
		HashAlg:                protocol.Sha256Hash,
		Sessions:               sessionStore,
		RevealVoucherExistence: config.PullService.RevealVoucherExistence,
		IssueToken: func(ownerKey protocol.PublicKey) (string, time.Time, error) {
			return tokenStore.issue(ownerKey)
		},
	}

	pullAuthServer.RegisterHandlers(mux)
	slog.Info("pull service: PullAuth endpoints registered",
		"session_ttl", config.PullService.SessionTTL,
		"token_ttl", tokenTTL,
	)

	// Note: Pull API list/download handlers (HTTPPullHolder) require a VoucherStore
	// implementation. This will be wired when the full VoucherStore adapter is
	// implemented for the voucher-manager's file-based storage.
	slog.Info("pull service: ready (PullAuth only; list/download requires VoucherStore adapter)")
}

// pullTokenStore manages session tokens issued after successful PullAuth.
type pullTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]*pullToken
	ttl    time.Duration
}

type pullToken struct {
	ownerKeyFingerprint []byte
	expiresAt           time.Time
}

func newPullTokenStore(ttl time.Duration) *pullTokenStore {
	return &pullTokenStore{
		tokens: make(map[string]*pullToken),
		ttl:    ttl,
	}
}

func (s *pullTokenStore) issue(ownerKey protocol.PublicKey) (string, time.Time, error) {
	// Compute fingerprint from the owner key
	pub, err := ownerKey.Public()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to extract public key: %w", err)
	}
	fingerprint := fingerprintKey(pub)

	// Generate random token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate token: %w", err)
	}
	token := fmt.Sprintf("%x", tokenBytes)
	expiresAt := time.Now().Add(s.ttl)

	s.mu.Lock()
	defer s.mu.Unlock()

	// GC expired tokens
	now := time.Now()
	for k, v := range s.tokens {
		if now.After(v.expiresAt) {
			delete(s.tokens, k)
		}
	}

	s.tokens[token] = &pullToken{
		ownerKeyFingerprint: fingerprint,
		expiresAt:           expiresAt,
	}

	return token, expiresAt, nil
}

func (s *pullTokenStore) validate(token string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.tokens[token]
	if !ok {
		return nil, fmt.Errorf("token not found")
	}
	if time.Now().After(t.expiresAt) {
		return nil, fmt.Errorf("token expired")
	}
	return t.ownerKeyFingerprint, nil
}

func fingerprintKey(pub crypto.PublicKey) []byte {
	// Simple fingerprint: SHA-256 of the public key's string representation
	h := sha256.New()
	fmt.Fprintf(h, "%v", pub)
	return h.Sum(nil)
}
