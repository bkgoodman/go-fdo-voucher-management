// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/fido-device-onboard/go-fdo/protocol"
	"github.com/fido-device-onboard/go-fdo/transfer"
)

// setupPullService configures and registers the FDOKeyAuth and Pull API handlers.
// The ownerKey parameter is the server's persistent owner key, used both for
// voucher signing and for FDOKeyAuth Server challenge signing. This ensures a
// Caller can verify the Server's identity against the DID document.
func setupPullService(config *Config, mux *http.ServeMux, ownerKey crypto.Signer, fileStore *VoucherFileStore, transmitStore *VoucherTransmissionStore) {
	if ownerKey == nil {
		slog.Error("pull service: owner key is nil — cannot configure FDOKeyAuth without an owner key")
		return
	}
	serverKey := ownerKey

	sessionStore := transfer.NewSessionStore(
		config.PullService.SessionTTL,
		config.PullService.MaxSessions,
	)

	tokenTTL := config.PullService.TokenTTL
	if tokenTTL == 0 {
		tokenTTL = 1 * time.Hour
	}

	tokenStore := newPullTokenStore(tokenTTL)

	authServer := &transfer.FDOKeyAuthServer{
		ServerKey:              serverKey,
		HashAlg:                protocol.Sha256Hash,
		Sessions:               sessionStore,
		RevealVoucherExistence: config.PullService.RevealVoucherExistence,
		IssueToken: func(callerKey protocol.PublicKey) (string, time.Time, error) {
			return tokenStore.issue(callerKey)
		},
	}

	authServer.RegisterHandlers(mux)
	slog.Info("pull service: FDOKeyAuth endpoints registered",
		"session_ttl", config.PullService.SessionTTL,
		"token_ttl", tokenTTL,
	)

	// Wire the Pull API list/download handlers using the file-based voucher store
	pullStore := NewPullVoucherStore(fileStore, transmitStore)
	pullHolder := &transfer.HTTPPullHolder{
		Store:           pullStore,
		ValidateToken:   tokenStore.validate,
		DefaultPageSize: 50,
	}
	pullHolder.RegisterHandlers(mux)
	slog.Info("pull service: Pull API list/download endpoints registered")
}

// pullTokenStore manages session tokens issued after successful FDOKeyAuth.
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
	// Compute fingerprint from the owner key. FingerprintProtocolKey normalizes
	// via crypto.PublicKey internally, so the result matches FingerprintFDOHex
	// used by the pipeline to store owner_key_fingerprint.
	fingerprint := FingerprintProtocolKey(ownerKey)
	if fingerprint == nil {
		return "", time.Time{}, fmt.Errorf("failed to compute owner key fingerprint")
	}

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

// setupPushReceiverAuth registers FDOKeyAuth handlers on the push receiver
// endpoint so that suppliers can authenticate before pushing vouchers.
// The LookupKey callback validates the caller's key against the partner trust
// store, and IssueToken stores tokens in the same DB-backed token manager
// used for manually-created tokens.
func setupPushReceiverAuth(config *Config, mux *http.ServeMux, ownerKey crypto.Signer, tokenManager *VoucherReceiverTokenManager, partnerStore *PartnerStore) {
	tokenTTL := config.PullService.TokenTTL
	if tokenTTL == 0 {
		tokenTTL = 1 * time.Hour
	}

	// Determine push endpoint root for auth handler registration.
	// Strip trailing slash to get the root path.
	pushRoot := config.VoucherReceiver.Endpoint
	if pushRoot == "" {
		pushRoot = "/api/v1/vouchers"
	}

	authServer := &transfer.FDOKeyAuthServer{
		ServerKey: ownerKey,
		HashAlg:   protocol.Sha256Hash,
		Sessions:  transfer.NewSessionStore(60*time.Second, 100),
		LookupKey: func(callerKey protocol.PublicKey) (int, error) {
			// Check if this key belongs to a trusted supplier
			if partnerStore == nil {
				return 0, nil // no partner store, accept all keys
			}
			// Extract the crypto.PublicKey from the protocol.PublicKey
			cryptoPub, err := callerKey.Public()
			if err != nil {
				return -1, fmt.Errorf("failed to extract public key: %w", err)
			}
			_, trusted := partnerStore.IsTrustedSupplier(context.Background(), cryptoPub)
			if !trusted {
				return -1, nil
			}
			return 0, nil
		},
		RevealVoucherExistence: false,
		IssueToken: func(callerKey protocol.PublicKey) (string, time.Time, error) {
			// Enforce partner trust: only when suppliers are registered.
			// If no suppliers exist, accept all keys (open mode).
			if partnerStore != nil && partnerStore.HasSuppliers(context.Background()) {
				cryptoPub, err := callerKey.Public()
				if err != nil {
					return "", time.Time{}, fmt.Errorf("failed to extract public key: %w", err)
				}
				_, trusted := partnerStore.IsTrustedSupplier(context.Background(), cryptoPub)
				if !trusted {
					return "", time.Time{}, fmt.Errorf("caller key is not a trusted supplier")
				}
			}
			return tokenManager.IssueTokenForKey(callerKey, tokenTTL)
		},
	}

	authServer.RegisterHandlers(mux, pushRoot)

	slog.Info("push receiver: FDOKeyAuth endpoints registered",
		"root", pushRoot,
		"token_ttl", tokenTTL,
	)
}
