// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// OwnerKeyResponse is the expected JSON response from owner key service
type OwnerKeyResponse struct {
	OwnerKeyPEM string `json:"owner_key_pem"`
	OwnerDID    string `json:"owner_did"`
	Error       string `json:"error"`
}

// OwnerKeyService handles retrieval of owner keys for voucher sign-over
type OwnerKeyService struct {
	executor *ExternalCommandExecutor
}

// NewOwnerKeyService creates a new owner key service
func NewOwnerKeyService(executor *ExternalCommandExecutor) *OwnerKeyService {
	return &OwnerKeyService{
		executor: executor,
	}
}

// OwnerKeyResult contains the result of owner key resolution
type OwnerKeyResult struct {
	PublicKey any    // The resolved public key
	DIDURL    string // The DID URL if available
}

// GetOwnerKey retrieves an owner key for the given device
func (o *OwnerKeyService) GetOwnerKey(ctx context.Context, serial, model string) (*OwnerKeyResult, error) {
	variables := map[string]string{
		"serialno": serial,
		"model":    model,
		"guid":     "",
	}

	output, err := o.executor.Execute(ctx, variables)
	if err != nil {
		return nil, fmt.Errorf("failed to execute owner key command: %w", err)
	}

	var response OwnerKeyResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return nil, fmt.Errorf("failed to parse owner key response: %w", err)
	}

	if response.Error != "" {
		return nil, fmt.Errorf("owner key service error: %s", response.Error)
	}

	// Handle DID response
	if response.OwnerDID != "" {
		return o.handleDIDResponse(ctx, response.OwnerDID)
	}

	// Handle PEM response
	if response.OwnerKeyPEM == "" {
		return nil, fmt.Errorf("no owner key returned")
	}

	publicKey, err := LoadPublicKeyFromPEM([]byte(response.OwnerKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("failed to parse PEM key: %w", err)
	}

	return &OwnerKeyResult{
		PublicKey: publicKey,
		DIDURL:    "",
	}, nil
}

// handleDIDResponse handles a DID response from the callback
func (o *OwnerKeyService) handleDIDResponse(ctx context.Context, didURI string) (*OwnerKeyResult, error) {
	resolver := NewDIDResolver(nil, true)

	publicKey, didURL, err := resolver.ResolveDIDKey(ctx, didURI)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve DID %s: %w", didURI, err)
	}

	return &OwnerKeyResult{
		PublicKey: publicKey,
		DIDURL:    didURL,
	}, nil
}
