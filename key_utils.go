// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/fido-device-onboard/go-fdo/cbor"
	"github.com/fido-device-onboard/go-fdo/protocol"
)

// FingerprintPublicKey computes a deterministic SHA-256 fingerprint of a public
// key by converting to protocol.PublicKey and CBOR-encoding it. Returns raw
// 32 bytes. This matches the spec definition of OwnerKeyFingerprint (SHA-256
// of the CBOR-encoded Owner Key) and the PullAuth server's fingerprint.
func FingerprintPublicKey(pub crypto.PublicKey) []byte {
	protoKey, err := publicKeyToProtocol(pub)
	if err != nil {
		return nil
	}
	return FingerprintProtocolKey(protoKey)
}

// FingerprintProtocolKey computes a SHA-256 fingerprint of a CBOR-encoded
// protocol.PublicKey. This is the canonical fingerprinting method used for
// owner-key scoping in the Pull API, matching the spec §9.8 definition.
func FingerprintProtocolKey(pub protocol.PublicKey) []byte {
	data, err := cbor.Marshal(pub)
	if err != nil {
		return nil
	}
	h := sha256.Sum256(data)
	return h[:]
}

// FingerprintPublicKeyHex returns the hex-encoded SHA-256 fingerprint of a
// CBOR-encoded public key.
func FingerprintPublicKeyHex(pub crypto.PublicKey) string {
	fp := FingerprintPublicKey(pub)
	if fp == nil {
		return ""
	}
	return hex.EncodeToString(fp)
}

// protocolPublicKeyToCrypto converts a protocol.PublicKey to crypto.PublicKey
func protocolPublicKeyToCrypto(protocolPubKey *protocol.PublicKey) (crypto.PublicKey, error) {
	return protocolPubKey.Public()
}

// publicKeyToProtocol converts a crypto public key to protocol.PublicKey
func publicKeyToProtocol(pubKey interface{}) (protocol.PublicKey, error) {
	switch key := pubKey.(type) {
	case *ecdsa.PublicKey:
		derBytes, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			return protocol.PublicKey{}, fmt.Errorf("failed to marshal ECDSA public key: %w", err)
		}
		cborEncoded, err := cbor.Marshal(derBytes)
		if err != nil {
			return protocol.PublicKey{}, fmt.Errorf("failed to CBOR-encode ECDSA public key: %w", err)
		}

		var keyType protocol.KeyType
		switch key.Curve {
		case elliptic.P256():
			keyType = protocol.Secp256r1KeyType
		case elliptic.P384():
			keyType = protocol.Secp384r1KeyType
		default:
			return protocol.PublicKey{}, fmt.Errorf("unsupported ECDSA curve: %s", key.Curve)
		}

		return protocol.PublicKey{
			Type:     keyType,
			Encoding: protocol.X509KeyEnc,
			Body:     cborEncoded,
		}, nil

	case *rsa.PublicKey:
		derBytes, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			return protocol.PublicKey{}, fmt.Errorf("failed to marshal RSA public key: %w", err)
		}
		cborEncoded, err := cbor.Marshal(derBytes)
		if err != nil {
			return protocol.PublicKey{}, fmt.Errorf("failed to CBOR-encode RSA public key: %w", err)
		}

		var keyType protocol.KeyType
		switch key.Size() {
		case 2048:
			keyType = protocol.Rsa2048RestrKeyType
		case 3072:
			keyType = protocol.RsaPkcsKeyType
		default:
			return protocol.PublicKey{}, fmt.Errorf("unsupported RSA key size: %d", key.Size())
		}

		return protocol.PublicKey{
			Type:     keyType,
			Encoding: protocol.X509KeyEnc,
			Body:     cborEncoded,
		}, nil

	default:
		return protocol.PublicKey{}, fmt.Errorf("unsupported public key type: %T", pubKey)
	}
}

// LoadPublicKeyFromPEM loads a public key from PEM format
func LoadPublicKeyFromPEM(pemData []byte) (crypto.PublicKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	switch block.Type {
	case "PUBLIC KEY":
		return x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse certificate: %w", err)
		}
		return cert.PublicKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}

// LoadPrivateKeyFromPEM loads a private key from PEM format
func LoadPrivateKeyFromPEM(pemData []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	switch block.Type {
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKCS8 private key: %w", err)
		}
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("key is not a signer")
		}
		return signer, nil
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKCS1 private key: %w", err)
		}
		return key, nil
	case "EC PRIVATE KEY":
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse EC private key: %w", err)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}

// LoadPublicKeyFromFile loads a public key from a PEM file
func LoadPublicKeyFromFile(filename string) (crypto.PublicKey, error) {
	if filename == "" {
		return nil, fmt.Errorf("filename not specified")
	}

	pemData, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return LoadPublicKeyFromPEM(pemData)
}

// loadCertChainFromFile loads an X.509 certificate chain from a PEM file.
// Returns certificates in the order they appear in the file.
func loadCertChainFromFile(filename string) ([]*x509.Certificate, error) {
	pemData, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	var chain []*x509.Certificate
	rest := pemData
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse certificate: %w", err)
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("no certificates found in %s", filename)
	}
	return chain, nil
}

// LoadPrivateKeyFromFile loads a private key from a PEM file
func LoadPrivateKeyFromFile(filename string) (crypto.Signer, error) {
	if filename == "" {
		return nil, fmt.Errorf("filename not specified")
	}

	pemData, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return LoadPrivateKeyFromPEM(pemData)
}
