// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"net/http"
	"testing"
	"time"
)

func TestPushError_IsTransient(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		transient bool
	}{
		{"400 Bad Request", 400, false},
		{"401 Unauthorized", 401, false},
		{"403 Forbidden", 403, false},
		{"404 Not Found", 404, false},
		{"409 Conflict", 409, false},
		{"429 Too Many Requests", 429, true},
		{"500 Internal Server Error", 500, true},
		{"502 Bad Gateway", 502, true},
		{"503 Service Unavailable", 503, true},
		{"504 Gateway Timeout", 504, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &PushError{StatusCode: tt.status, Body: "test"}
			if got := e.IsTransient(); got != tt.transient {
				t.Errorf("PushError{%d}.IsTransient() = %v, want %v", tt.status, got, tt.transient)
			}
		})
	}
}

func TestBackoffDuration(t *testing.T) {
	base := time.Minute

	// Attempt 1: ~1min (±25%)
	d1 := backoffDuration(1, base)
	if d1 < 45*time.Second || d1 > 75*time.Second {
		t.Errorf("attempt 1: got %v, want ~1min (±25%%)", d1)
	}

	// Attempt 2: ~2min (±25%)
	d2 := backoffDuration(2, base)
	if d2 < 90*time.Second || d2 > 150*time.Second {
		t.Errorf("attempt 2: got %v, want ~2min (±25%%)", d2)
	}

	// Attempt 3: ~4min (±25%)
	d3 := backoffDuration(3, base)
	if d3 < 3*time.Minute || d3 > 5*time.Minute {
		t.Errorf("attempt 3: got %v, want ~4min (±25%%)", d3)
	}

	// Very high attempt: should cap at 24h (±25%)
	dMax := backoffDuration(100, base)
	maxCap := 24 * time.Hour
	if dMax < time.Duration(float64(maxCap)*0.75) || dMax > time.Duration(float64(maxCap)*1.25) {
		t.Errorf("attempt 100: got %v, want ~24h (±25%%)", dMax)
	}
}

func TestParseRetryAfter(t *testing.T) {
	// Seconds format
	d := parseRetryAfter("120")
	if d != 120*time.Second {
		t.Errorf("parseRetryAfter(\"120\") = %v, want 2m0s", d)
	}

	// Empty
	if d := parseRetryAfter(""); d != 0 {
		t.Errorf("parseRetryAfter(\"\") = %v, want 0", d)
	}

	// HTTP-date format (future time)
	future := time.Now().Add(60 * time.Second).UTC().Format(http.TimeFormat)
	d = parseRetryAfter(future)
	if d < 50*time.Second || d > 70*time.Second {
		t.Errorf("parseRetryAfter(future) = %v, want ~60s", d)
	}

	// Invalid
	if d := parseRetryAfter("garbage"); d != 0 {
		t.Errorf("parseRetryAfter(\"garbage\") = %v, want 0", d)
	}
}
