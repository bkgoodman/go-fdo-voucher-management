// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fido-device-onboard/go-fdo/cbor"
)

// OVEExtraDataService handles fetching and encoding OVEExtra data
type OVEExtraDataService struct {
	enabled  bool
	executor *ExternalCommandExecutor
}

// NewOVEExtraDataService creates a new OVEExtra data service
func NewOVEExtraDataService(enabled bool, externalCommand string, timeout time.Duration) *OVEExtraDataService {
	return &OVEExtraDataService{
		enabled:  enabled,
		executor: NewExternalCommandExecutor(externalCommand, timeout),
	}
}

// GetOVEExtraData fetches OVEExtra data from external script and returns as CBOR-encoded map
func (s *OVEExtraDataService) GetOVEExtraData(ctx context.Context, serial, model string) (map[int][]byte, error) {
	if !s.enabled {
		return nil, nil
	}

	jsonData, err := s.fetchExtraData(ctx, serial, model)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch extra data: %w", err)
	}

	if jsonData == "" {
		return nil, nil
	}

	var rawData map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &rawData); err != nil {
		return nil, fmt.Errorf("failed to parse JSON extra data: %w", err)
	}

	extraData := make(map[int][]byte)
	for key, value := range rawData {
		var keyInt int
		if parsed, err := json.Number(key).Int64(); err == nil {
			keyInt = int(parsed)
		} else {
			keyInt = hashString(key)
		}

		var valueToEncode interface{}
		switch v := value.(type) {
		case float64:
			valueToEncode = fmt.Sprintf("%.6f", v)
		case map[string]interface{}:
			converted := make(map[string]interface{})
			for k, val := range v {
				switch val := val.(type) {
				case float64:
					converted[k] = fmt.Sprintf("%.6f", val)
				default:
					converted[k] = val
				}
			}
			valueToEncode = converted
		default:
			valueToEncode = value
		}

		valueBytes, err := cbor.Marshal(valueToEncode)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal extra data value: %w", err)
		}

		extraData[keyInt] = valueBytes
	}

	return extraData, nil
}

// fetchExtraData calls external script to get JSON data
func (s *OVEExtraDataService) fetchExtraData(ctx context.Context, serial, model string) (string, error) {
	variables := map[string]string{
		"serial": serial,
		"model":  model,
	}
	output, err := s.executor.Execute(ctx, variables)
	if err != nil {
		return "", fmt.Errorf("external command failed: %w", err)
	}

	return output, nil
}

// hashString creates a simple hash from string for OVEExtra key type
func hashString(s string) int {
	hash := 0
	for _, c := range s {
		hash = hash*31 + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return (hash % 1000) + 1
}
