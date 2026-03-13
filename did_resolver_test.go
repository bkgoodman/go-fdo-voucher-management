// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"testing"

	"github.com/fido-device-onboard/go-fdo/did"
)

func TestParseDIDKey_P256(t *testing.T) {
	// Test vector from did:key spec v0.9 §Test Vectors > P-256
	didURI := "did:key:zDnaerx9CtbPJ1q36T5Ln5wYt3MQYeGRG5ehnPAmxcf5mDZpv"

	key, err := did.ParseDIDKey(didURI)
	if err != nil {
		t.Fatalf("ParseDIDKey failed: %v", err)
	}

	ecKey, ok := key.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("expected *ecdsa.PublicKey, got %T", key)
	}
	if ecKey.Curve != elliptic.P256() {
		t.Errorf("expected P-256 curve, got %s", ecKey.Curve.Params().Name)
	}
	if !ecKey.IsOnCurve(ecKey.X, ecKey.Y) {
		t.Error("key is not on curve")
	}
}

func TestParseDIDKey_P384(t *testing.T) {
	// Test vector from did:key spec v0.9 §Test Vectors > P-384
	didURI := "did:key:z82LkvCwHNreneWpsgPEbV3gu1C6NFJEBg4srfJ5gdxEsMGRJUz2sG9FE42shbn2xkZJh54"

	key, err := did.ParseDIDKey(didURI)
	if err != nil {
		t.Fatalf("ParseDIDKey failed: %v", err)
	}

	ecKey, ok := key.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("expected *ecdsa.PublicKey, got %T", key)
	}
	if ecKey.Curve != elliptic.P384() {
		t.Errorf("expected P-384 curve, got %s", ecKey.Curve.Params().Name)
	}
	if !ecKey.IsOnCurve(ecKey.X, ecKey.Y) {
		t.Error("key is not on curve")
	}
}

func TestParseDIDKey_Invalid(t *testing.T) {
	tests := []struct {
		name string
		uri  string
	}{
		{"missing z prefix", "did:key:abc123"},
		{"empty after z", "did:key:z"},
		{"not did:key", "did:web:example.com"},
		{"invalid base58", "did:key:z!!!invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := did.ParseDIDKey(tt.uri)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseDIDKey_RoundTrip(t *testing.T) {
	// Parse the same did:key twice and verify deterministic output.
	didURI := "did:key:zDnaerx9CtbPJ1q36T5Ln5wYt3MQYeGRG5ehnPAmxcf5mDZpv"

	key1, err := did.ParseDIDKey(didURI)
	if err != nil {
		t.Fatal(err)
	}

	key2, err := did.ParseDIDKey(didURI)
	if err != nil {
		t.Fatal(err)
	}

	ec1 := key1.(*ecdsa.PublicKey)
	ec2 := key2.(*ecdsa.PublicKey)

	if ec1.X.Cmp(ec2.X) != 0 || ec1.Y.Cmp(ec2.Y) != 0 {
		t.Error("same DID should produce same key")
	}
}
