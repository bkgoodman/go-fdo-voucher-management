// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

// CallerIdentity represents the authenticated identity of an API caller.
// This abstraction unifies FDOKeyAuth-derived identities (which have a
// cryptographic key fingerprint) with alternative token sources (pre-shared
// tokens bound to a named identity). The spec (section 10) explicitly decouples
// bearer tokens from owner keys: a caller may have an identity without
// possessing an FDO owner key.
type CallerIdentity struct {
	// Fingerprint is the hex-encoded SHA-256 fingerprint that identifies
	// this caller. For FDOKeyAuth callers, this is the CBOR-based key
	// fingerprint (spec section 9.8). For alternative token callers, this is a
	// SHA-256 hash of the identity label, providing a consistent lookup key.
	Fingerprint string

	// IdentityLabel is a human-readable identifier for the caller.
	// For FDOKeyAuth callers, this may be empty or derived from the partner
	// store. For alternative token callers, this is the configured identity
	// label (e.g., "reseller-b", "customer-portal").
	IdentityLabel string

	// AuthMethod indicates how the caller authenticated.
	AuthMethod AuthMethod

	// PartnerID links this identity to a partner in the trust store, if any.
	PartnerID string

	// HasOwnerKey indicates whether this identity is backed by an FDO owner
	// key. When false, the caller authenticated via an alternative token and
	// cannot be a voucher sign-over target (spec section 10 table).
	HasOwnerKey bool
}

// AuthMethod indicates how a caller was authenticated.
type AuthMethod string

const (
	// AuthMethodFDOKeyAuth indicates authentication via the FDOKeyAuth protocol.
	AuthMethodFDOKeyAuth AuthMethod = "fdokeyauth"

	// AuthMethodBearerToken indicates authentication via a pre-shared bearer token.
	AuthMethodBearerToken AuthMethod = "bearer_token"

	// AuthMethodGlobalToken indicates authentication via the global config token.
	AuthMethodGlobalToken AuthMethod = "global_token"

	// AuthMethodNone indicates no authentication (open mode).
	AuthMethodNone AuthMethod = "none"
)

// IsAuthenticated returns true if the caller has a valid identity.
func (c *CallerIdentity) IsAuthenticated() bool {
	return c != nil && c.AuthMethod != AuthMethodNone && c.Fingerprint != ""
}

// CanAssign returns true if the caller's access level permits assignment.
// Assignment requires a valid identity but does NOT require an owner key
// (spec section 10: "Assign voucher" requires identity but not owner key).
func (c *CallerIdentity) CanAssign() bool {
	return c.IsAuthenticated()
}

// CanSignOver returns true if the caller can be a voucher sign-over target.
// This requires possessing an FDO owner key (spec section 10).
func (c *CallerIdentity) CanSignOver() bool {
	return c.IsAuthenticated() && c.HasOwnerKey
}
