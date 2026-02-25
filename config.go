// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the voucher manager configuration
type Config struct {
	Debug bool `yaml:"debug"`

	// Server configuration
	Server struct {
		Addr        string `yaml:"addr"`
		ExtAddr     string `yaml:"ext_addr"`
		UseTLS      bool   `yaml:"use_tls"`
		InsecureTLS bool   `yaml:"insecure_tls"`
	} `yaml:"server"`

	// Database configuration
	Database struct {
		Path     string `yaml:"path"`
		Password string `yaml:"password"`
	} `yaml:"database"`

	// Key management configuration
	KeyManagement struct {
		KeyType       string `yaml:"key_type"`        // "ec256", "ec384", "rsa2048", "rsa3072"
		FirstTimeInit bool   `yaml:"first_time_init"` // Generate key on first run
		ImportKeyFile string `yaml:"import_key_file"` // Path to PEM-encoded private key to import
	} `yaml:"key_management"`

	// Voucher receiver configuration (inbound push)
	VoucherReceiver struct {
		Enabled           bool   `yaml:"enabled"`
		Endpoint          string `yaml:"endpoint"`           // HTTP path, e.g., "/api/v1/vouchers"
		GlobalToken       string `yaml:"global_token"`       // Optional bearer token
		ValidateOwnership bool   `yaml:"validate_ownership"` // Validate voucher is signed to our owner key
		RequireAuth       bool   `yaml:"require_auth"`       // Require authentication
	} `yaml:"voucher_receiver"`

	// Voucher signing configuration
	VoucherSigning struct {
		Mode            string        `yaml:"mode"`             // "internal", "external", "hsm"
		ExternalCommand string        `yaml:"external_command"` // For external/hsm mode
		ExternalTimeout time.Duration `yaml:"external_timeout"`
	} `yaml:"voucher_signing"`

	// OVEExtra data configuration
	OVEExtraData struct {
		Enabled         bool          `yaml:"enabled"`
		ExternalCommand string        `yaml:"external_command"`
		Timeout         time.Duration `yaml:"timeout"`
	} `yaml:"ove_extra_data"`

	// Owner signover configuration
	OwnerSignover struct {
		Mode            string        `yaml:"mode"`              // "static" or "dynamic"
		StaticPublicKey string        `yaml:"static_public_key"` // PEM-encoded public key
		StaticDID       string        `yaml:"static_did"`        // DID URI
		ExternalCommand string        `yaml:"external_command"`  // For dynamic mode
		Timeout         time.Duration `yaml:"timeout"`
	} `yaml:"owner_signover"`

	// Voucher file storage
	VoucherFiles struct {
		Directory string `yaml:"directory"`
	} `yaml:"voucher_files"`

	// Destination callback configuration
	DestinationCallback struct {
		Enabled         bool          `yaml:"enabled"`
		ExternalCommand string        `yaml:"external_command"`
		Timeout         time.Duration `yaml:"timeout"`
	} `yaml:"destination_callback"`

	// DID cache configuration
	DIDCache struct {
		Enabled         bool          `yaml:"enabled"`
		RefreshInterval time.Duration `yaml:"refresh_interval"`
		MaxAge          time.Duration `yaml:"max_age"`
		FailureBackoff  time.Duration `yaml:"failure_backoff"`
		PurgeUnused     time.Duration `yaml:"purge_unused"`
		PurgeOnStartup  bool          `yaml:"purge_on_startup"`
	} `yaml:"did_cache"`

	// Push service configuration (outbound)
	PushService struct {
		Enabled            bool          `yaml:"enabled"`
		URL                string        `yaml:"url"`
		AuthToken          string        `yaml:"auth_token"`
		Mode               string        `yaml:"mode"` // "fallback" or "send_always"
		DeleteAfterSuccess bool          `yaml:"delete_after_success"`
		RetryInterval      time.Duration `yaml:"retry_interval"`
		MaxAttempts        int           `yaml:"max_attempts"`
	} `yaml:"push_service"`

	// DID push configuration
	DIDPush struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"did_push"`

	// Retry worker configuration
	RetryWorker struct {
		Enabled       bool          `yaml:"enabled"`
		RetryInterval time.Duration `yaml:"retry_interval"`
		MaxAttempts   int           `yaml:"max_attempts"`
	} `yaml:"retry_worker"`

	// Retention configuration
	Retention struct {
		KeepIndefinitely bool          `yaml:"keep_indefinitely"`
		PurgeAfter       time.Duration `yaml:"purge_after"`
	} `yaml:"retention"`

	// Pull service configuration (inbound pull - serve vouchers to authenticated recipients)
	PullService struct {
		Enabled                bool          `yaml:"enabled"`
		SessionTTL             time.Duration `yaml:"session_ttl"`
		MaxSessions            int           `yaml:"max_sessions"`
		TokenTTL               time.Duration `yaml:"token_ttl"`
		RevealVoucherExistence bool          `yaml:"reveal_voucher_existence"`
	} `yaml:"pull_service"`

	// DID minting configuration
	DIDMinting struct {
		Enabled             bool   `yaml:"enabled"`
		Host                string `yaml:"host"`                  // Hostname for did:web (e.g., "example.com:8080")
		Path                string `yaml:"path"`                  // Optional sub-path for did:web
		VoucherRecipientURL string `yaml:"voucher_recipient_url"` // URL for FDOVoucherRecipient service entry
		VoucherHolderURL    string `yaml:"voucher_holder_url"`    // URL for FDOVoucherHolder service entry (pull)
		ServeDIDDocument    bool   `yaml:"serve_did_document"`    // Serve .well-known/did.json
		ExportDIDURI        bool   `yaml:"export_did_uri"`        // Log the did:web URI on startup
		KeyExportPath       string `yaml:"key_export_path"`       // Save DID-minted private key to PEM file (for pull command)
	} `yaml:"did_minting"`
}

// DefaultConfig returns configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Debug: false,
		Server: struct {
			Addr        string `yaml:"addr"`
			ExtAddr     string `yaml:"ext_addr"`
			UseTLS      bool   `yaml:"use_tls"`
			InsecureTLS bool   `yaml:"insecure_tls"`
		}{
			Addr:        "localhost:8080",
			ExtAddr:     "",
			UseTLS:      false,
			InsecureTLS: false,
		},
		Database: struct {
			Path     string `yaml:"path"`
			Password string `yaml:"password"`
		}{
			Path:     "voucher_manager.db",
			Password: "",
		},
		KeyManagement: struct {
			KeyType       string `yaml:"key_type"`
			FirstTimeInit bool   `yaml:"first_time_init"`
			ImportKeyFile string `yaml:"import_key_file"`
		}{
			KeyType:       "ec384",
			FirstTimeInit: true,
			ImportKeyFile: "",
		},
		VoucherReceiver: struct {
			Enabled           bool   `yaml:"enabled"`
			Endpoint          string `yaml:"endpoint"`
			GlobalToken       string `yaml:"global_token"`
			ValidateOwnership bool   `yaml:"validate_ownership"`
			RequireAuth       bool   `yaml:"require_auth"`
		}{
			Enabled:           true,
			Endpoint:          "/api/v1/vouchers",
			GlobalToken:       "",
			ValidateOwnership: false,
			RequireAuth:       false,
		},
		VoucherSigning: struct {
			Mode            string        `yaml:"mode"`
			ExternalCommand string        `yaml:"external_command"`
			ExternalTimeout time.Duration `yaml:"external_timeout"`
		}{
			Mode:            "internal",
			ExternalCommand: "",
			ExternalTimeout: 30 * time.Second,
		},
		OVEExtraData: struct {
			Enabled         bool          `yaml:"enabled"`
			ExternalCommand string        `yaml:"external_command"`
			Timeout         time.Duration `yaml:"timeout"`
		}{
			Enabled:         false,
			ExternalCommand: "",
			Timeout:         10 * time.Second,
		},
		OwnerSignover: struct {
			Mode            string        `yaml:"mode"`
			StaticPublicKey string        `yaml:"static_public_key"`
			StaticDID       string        `yaml:"static_did"`
			ExternalCommand string        `yaml:"external_command"`
			Timeout         time.Duration `yaml:"timeout"`
		}{
			Mode:            "static",
			StaticPublicKey: "",
			StaticDID:       "",
			ExternalCommand: "",
			Timeout:         10 * time.Second,
		},
		VoucherFiles: struct {
			Directory string `yaml:"directory"`
		}{
			Directory: "data/vouchers",
		},
		DestinationCallback: struct {
			Enabled         bool          `yaml:"enabled"`
			ExternalCommand string        `yaml:"external_command"`
			Timeout         time.Duration `yaml:"timeout"`
		}{
			Enabled:         false,
			ExternalCommand: "",
			Timeout:         10 * time.Second,
		},
		DIDCache: struct {
			Enabled         bool          `yaml:"enabled"`
			RefreshInterval time.Duration `yaml:"refresh_interval"`
			MaxAge          time.Duration `yaml:"max_age"`
			FailureBackoff  time.Duration `yaml:"failure_backoff"`
			PurgeUnused     time.Duration `yaml:"purge_unused"`
			PurgeOnStartup  bool          `yaml:"purge_on_startup"`
		}{
			Enabled:         false,
			RefreshInterval: 1 * time.Hour,
			MaxAge:          24 * time.Hour,
			FailureBackoff:  1 * time.Hour,
			PurgeUnused:     7 * 24 * time.Hour,
			PurgeOnStartup:  false,
		},
		PushService: struct {
			Enabled            bool          `yaml:"enabled"`
			URL                string        `yaml:"url"`
			AuthToken          string        `yaml:"auth_token"`
			Mode               string        `yaml:"mode"`
			DeleteAfterSuccess bool          `yaml:"delete_after_success"`
			RetryInterval      time.Duration `yaml:"retry_interval"`
			MaxAttempts        int           `yaml:"max_attempts"`
		}{
			Enabled:            false,
			URL:                "",
			AuthToken:          "",
			Mode:               "fallback",
			DeleteAfterSuccess: false,
			RetryInterval:      8 * time.Hour,
			MaxAttempts:        5,
		},
		DIDPush: struct {
			Enabled bool `yaml:"enabled"`
		}{
			Enabled: true,
		},
		RetryWorker: struct {
			Enabled       bool          `yaml:"enabled"`
			RetryInterval time.Duration `yaml:"retry_interval"`
			MaxAttempts   int           `yaml:"max_attempts"`
		}{
			Enabled:       true,
			RetryInterval: 8 * time.Hour,
			MaxAttempts:   5,
		},
		Retention: struct {
			KeepIndefinitely bool          `yaml:"keep_indefinitely"`
			PurgeAfter       time.Duration `yaml:"purge_after"`
		}{
			KeepIndefinitely: true,
			PurgeAfter:       0,
		},
		PullService: struct {
			Enabled                bool          `yaml:"enabled"`
			SessionTTL             time.Duration `yaml:"session_ttl"`
			MaxSessions            int           `yaml:"max_sessions"`
			TokenTTL               time.Duration `yaml:"token_ttl"`
			RevealVoucherExistence bool          `yaml:"reveal_voucher_existence"`
		}{
			Enabled:                false,
			SessionTTL:             60 * time.Second,
			MaxSessions:            1000,
			TokenTTL:               1 * time.Hour,
			RevealVoucherExistence: false,
		},
		DIDMinting: struct {
			Enabled             bool   `yaml:"enabled"`
			Host                string `yaml:"host"`
			Path                string `yaml:"path"`
			VoucherRecipientURL string `yaml:"voucher_recipient_url"`
			VoucherHolderURL    string `yaml:"voucher_holder_url"`
			ServeDIDDocument    bool   `yaml:"serve_did_document"`
			ExportDIDURI        bool   `yaml:"export_did_uri"`
			KeyExportPath       string `yaml:"key_export_path"`
		}{
			Enabled:             false,
			Host:                "",
			Path:                "",
			VoucherRecipientURL: "",
			VoucherHolderURL:    "",
			ServeDIDDocument:    true,
			ExportDIDURI:        true,
			KeyExportPath:       "",
		},
	}
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(configPath string) (*Config, error) {
	config := DefaultConfig()

	if configPath == "" {
		configPath = "config.yaml"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return nil, fmt.Errorf("error reading config file %q: %w", configPath, err)
	}

	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("error parsing config file %q: %w", configPath, err)
	}

	return config, nil
}

// SaveConfig saves configuration to a YAML file
func SaveConfig(config *Config, configPath string) error {
	if configPath == "" {
		configPath = "config.yaml"
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("error marshaling config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("error writing config file %q: %w", configPath, err)
	}

	return nil
}
