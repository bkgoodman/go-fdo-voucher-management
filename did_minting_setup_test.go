// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// writeTestKey writes an ECDSA P-384 private key to a PEM file and returns the signer.
func writeTestKey(t *testing.T, path string) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal test key: %v", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(path, pemData, 0o600); err != nil {
		t.Fatalf("failed to write test key: %v", err)
	}
	return key
}

func TestLoadOrGenerateOwnerKey_ImportKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "owner.pem")
	origKey := writeTestKey(t, keyPath)

	config := DefaultConfig()
	config.KeyManagement.ImportKeyFile = keyPath
	config.DIDMinting.Host = "localhost:9999"

	key, generated, err := loadOrGenerateOwnerKey(config)
	if err != nil {
		t.Fatalf("loadOrGenerateOwnerKey: %v", err)
	}
	if generated {
		t.Error("expected generated=false for import mode")
	}

	// Verify it's the same key
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("expected *ecdsa.PrivateKey, got %T", key)
	}
	if !ecKey.PublicKey.Equal(&origKey.PublicKey) {
		t.Error("imported key does not match original")
	}
}

func TestLoadOrGenerateOwnerKey_ImportKeyFile_Missing(t *testing.T) {
	config := DefaultConfig()
	config.KeyManagement.ImportKeyFile = "/nonexistent/path/owner.pem"
	config.DIDMinting.Host = "localhost:9999"

	_, _, err := loadOrGenerateOwnerKey(config)
	if err == nil {
		t.Fatal("expected error for missing import key file")
	}
}

func TestLoadOrGenerateOwnerKey_FirstTimeInit_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	exportPath := filepath.Join(dir, "data", "owner-key.pem")

	config := DefaultConfig()
	config.KeyManagement.FirstTimeInit = true
	config.KeyManagement.ImportKeyFile = ""
	config.DIDMinting.Host = "localhost:9999"
	config.DIDMinting.KeyExportPath = exportPath

	// First call: no file exists, should generate
	key1, generated, err := loadOrGenerateOwnerKey(config)
	if err != nil {
		t.Fatalf("loadOrGenerateOwnerKey (first): %v", err)
	}
	if !generated {
		t.Error("expected generated=true on first run")
	}

	// Simulate what setupDIDMinting does: persist the key
	if err := exportPrivateKey(key1, exportPath); err != nil {
		t.Fatalf("exportPrivateKey: %v", err)
	}

	// Second call: file now exists, should load
	key2, generated2, err := loadOrGenerateOwnerKey(config)
	if err != nil {
		t.Fatalf("loadOrGenerateOwnerKey (second): %v", err)
	}
	if generated2 {
		t.Error("expected generated=false on second run (key should be loaded)")
	}

	// Verify same key
	fp1 := FingerprintPublicKeyHex(key1.Public())
	fp2 := FingerprintPublicKeyHex(key2.Public())
	if fp1 != fp2 {
		t.Errorf("key fingerprints differ: first=%s second=%s", fp1, fp2)
	}
}

func TestLoadOrGenerateOwnerKey_FirstTimeInit_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	exportPath := filepath.Join(dir, "owner-key.pem")
	origKey := writeTestKey(t, exportPath)

	config := DefaultConfig()
	config.KeyManagement.FirstTimeInit = true
	config.KeyManagement.ImportKeyFile = ""
	config.DIDMinting.Host = "localhost:9999"
	config.DIDMinting.KeyExportPath = exportPath

	key, generated, err := loadOrGenerateOwnerKey(config)
	if err != nil {
		t.Fatalf("loadOrGenerateOwnerKey: %v", err)
	}
	if generated {
		t.Error("expected generated=false when file already exists")
	}

	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("expected *ecdsa.PrivateKey, got %T", key)
	}
	if !ecKey.PublicKey.Equal(&origKey.PublicKey) {
		t.Error("loaded key does not match original")
	}
}

func TestLoadOrGenerateOwnerKey_Ephemeral(t *testing.T) {
	config := DefaultConfig()
	config.KeyManagement.FirstTimeInit = false
	config.KeyManagement.ImportKeyFile = ""
	config.DIDMinting.Host = "localhost:9999"
	config.DIDMinting.KeyExportPath = ""

	key, generated, err := loadOrGenerateOwnerKey(config)
	if err != nil {
		t.Fatalf("loadOrGenerateOwnerKey: %v", err)
	}
	if !generated {
		t.Error("expected generated=true for ephemeral mode")
	}
	if key == nil {
		t.Error("expected non-nil key")
	}
}

func TestLoadOrGenerateOwnerKey_ImportTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	importPath := filepath.Join(dir, "import.pem")
	exportPath := filepath.Join(dir, "export.pem")

	importKey := writeTestKey(t, importPath)
	writeTestKey(t, exportPath) // different key at export path

	config := DefaultConfig()
	config.KeyManagement.ImportKeyFile = importPath
	config.KeyManagement.FirstTimeInit = true
	config.DIDMinting.Host = "localhost:9999"
	config.DIDMinting.KeyExportPath = exportPath

	key, generated, err := loadOrGenerateOwnerKey(config)
	if err != nil {
		t.Fatalf("loadOrGenerateOwnerKey: %v", err)
	}
	if generated {
		t.Error("expected generated=false for import mode")
	}

	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("expected *ecdsa.PrivateKey, got %T", key)
	}
	if !ecKey.PublicKey.Equal(&importKey.PublicKey) {
		t.Error("import_key_file should take precedence over first_time_init")
	}
}

func TestExportPrivateKey_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")

	origKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	if err := exportPrivateKey(origKey, keyPath); err != nil {
		t.Fatalf("exportPrivateKey: %v", err)
	}

	loaded, err := LoadPrivateKeyFromFile(keyPath)
	if err != nil {
		t.Fatalf("LoadPrivateKeyFromFile: %v", err)
	}

	ecLoaded, ok := loaded.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("expected *ecdsa.PrivateKey, got %T", loaded)
	}
	if !ecLoaded.PublicKey.Equal(&origKey.PublicKey) {
		t.Error("round-tripped key does not match original")
	}
}
