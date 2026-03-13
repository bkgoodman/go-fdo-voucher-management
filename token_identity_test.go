// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import "testing"

func TestCallerIdentity_IsAuthenticated(t *testing.T) {
	tests := []struct {
		name   string
		caller *CallerIdentity
		want   bool
	}{
		{"nil caller", nil, false},
		{"AuthMethodNone", &CallerIdentity{AuthMethod: AuthMethodNone, Fingerprint: "abc123"}, false},
		{"bearer token empty fingerprint", &CallerIdentity{AuthMethod: AuthMethodBearerToken, Fingerprint: ""}, false},
		{"bearer token with fingerprint", &CallerIdentity{AuthMethod: AuthMethodBearerToken, Fingerprint: "abc123"}, true},
		{"fdo key auth with fingerprint", &CallerIdentity{AuthMethod: AuthMethodFDOKeyAuth, Fingerprint: "abc123"}, true},
		{"global token with fingerprint", &CallerIdentity{AuthMethod: AuthMethodGlobalToken, Fingerprint: "abc123"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caller.IsAuthenticated(); got != tt.want {
				t.Errorf("IsAuthenticated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCallerIdentity_CanAssign(t *testing.T) {
	tests := []struct {
		name   string
		caller *CallerIdentity
		want   bool
	}{
		{"authenticated caller", &CallerIdentity{AuthMethod: AuthMethodBearerToken, Fingerprint: "abc123"}, true},
		{"unauthenticated caller", &CallerIdentity{AuthMethod: AuthMethodNone}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caller.CanAssign(); got != tt.want {
				t.Errorf("CanAssign() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCallerIdentity_CanSignOver(t *testing.T) {
	tests := []struct {
		name   string
		caller *CallerIdentity
		want   bool
	}{
		{"authenticated with owner key", &CallerIdentity{AuthMethod: AuthMethodBearerToken, Fingerprint: "abc123", HasOwnerKey: true}, true},
		{"authenticated without owner key", &CallerIdentity{AuthMethod: AuthMethodBearerToken, Fingerprint: "abc123", HasOwnerKey: false}, false},
		{"unauthenticated with owner key", &CallerIdentity{AuthMethod: AuthMethodNone, HasOwnerKey: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caller.CanSignOver(); got != tt.want {
				t.Errorf("CanSignOver() = %v, want %v", got, tt.want)
			}
		})
	}
}
