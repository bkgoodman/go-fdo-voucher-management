# Migration Guide: PullAuth → FDOKeyAuth

This document is for applications using the `go-fdo/transfer` library that need to
update to the new FDOKeyAuth protocol (formerly PullAuth).

## What Changed

The authentication protocol was generalized from "PullAuth" (pull-only) to "FDOKeyAuth"
(used by both push and pull APIs). The wire protocol is structurally identical — same
CBOR arrays, same COSE_Sign1 signatures, same 3-message handshake — but with updated
terminology and type tags.

### Wire-Format Changes (Breaking)

| Old | New |
|-----|-----|
| `"PullAuth.Challenge"` (COSE_Sign1 type tag) | `"FDOKeyAuth.Challenge"` |
| `"PullAuth.Prove"` (COSE_Sign1 type tag) | `"FDOKeyAuth.Prove"` |
| `"holder_id"` (CBOR map key in ServerInfo) | `"server_id"` |

**These are wire-breaking changes.** Old clients cannot authenticate with new servers
and vice versa. Since this project is greenfield, no backward-compatibility shim is
provided.

### Go Type Renames

| Old | New |
|-----|-----|
| `PullAuthServer` | `FDOKeyAuthServer` |
| `PullAuthClient` | `FDOKeyAuthClient` |
| `PullAuthHello` | `FDOKeyAuthHello` |
| `PullAuthChallenge` | `FDOKeyAuthChallenge` |
| `PullAuthChallengeSignedPayload` | `FDOKeyAuthChallengeSignedPayload` |
| `PullAuthProve` | `FDOKeyAuthProve` |
| `PullAuthProveSignedPayload` | `FDOKeyAuthProveSignedPayload` |
| `PullAuthResult` | `FDOKeyAuthResult` |
| `PullAuthClientResult` | `FDOKeyAuthClientResult` |
| `HolderInfo` | `ServerInfo` |
| `VoucherLookup` | `KeyLookup` |

### Struct Field Renames

| Old | New | Affected Types |
|-----|-----|----------------|
| `HolderKey` | `ServerKey` | `FDOKeyAuthServer` |
| `LookupVouchers` | `LookupKey` | `FDOKeyAuthServer` |
| `OwnerKey` | `CallerKey` | `FDOKeyAuthClient` |
| `OwnerPublicKey` | `CallerPublicKey` | `FDOKeyAuthClient` |
| `HolderPublicKey` | `ServerPublicKey` | `FDOKeyAuthClient` |
| `NonceRecipient` | `NonceCaller` | Message structs, `Session` |
| `NonceHolder` | `NonceServer` | Message structs, `Session` |
| `HolderSignature` | `ServerSignature` | `FDOKeyAuthChallenge` |
| `RecipientSignature` | `CallerSignature` | `FDOKeyAuthProve` |
| `OwnerKeyFingerprint` | `KeyFingerprint` | `FDOKeyAuthResult`, `FDOKeyAuthClientResult` |
| `HolderID` | `ServerID` | `ServerInfo` |

### New Additions

- **`OIDPermitVoucherUpload`** — New EKU OID (`1.3.6.1.4.1.45724.3.1.6`) for push
  delegation. Analogous to `OIDPermitVoucherClaim` but for push operations.
- **Push-side FDOKeyAuth** — `FDOKeyAuthServer.RegisterHandlers(mux, root)` can now
  be registered on a push endpoint root (e.g., `/api/v1/vouchers`) in addition to
  the pull root. The `KeyLookup` callback is generalized to check any enrolled key,
  not just voucher ownership.

## Migration Checklist

### 1. Find-and-replace type names

```bash
# In your Go files:
sed -i 's/PullAuthServer/FDOKeyAuthServer/g' *.go
sed -i 's/PullAuthClient/FDOKeyAuthClient/g' *.go
sed -i 's/PullAuthHello/FDOKeyAuthHello/g' *.go
sed -i 's/PullAuthChallenge/FDOKeyAuthChallenge/g' *.go
sed -i 's/PullAuthProve/FDOKeyAuthProve/g' *.go
sed -i 's/PullAuthResult/FDOKeyAuthResult/g' *.go
sed -i 's/PullAuthClientResult/FDOKeyAuthClientResult/g' *.go
sed -i 's/HolderInfo/ServerInfo/g' *.go
sed -i 's/VoucherLookup/KeyLookup/g' *.go
```

### 2. Update struct field references

```go
// Before:
server := &transfer.PullAuthServer{
    HolderKey:      holderKey,
    LookupVouchers: myLookup,
}
client := &transfer.PullAuthClient{
    OwnerKey:        ownerKey,
    HolderPublicKey: holderPub,
}

// After:
server := &transfer.FDOKeyAuthServer{
    ServerKey: serverKey,
    LookupKey: myLookup,
}
client := &transfer.FDOKeyAuthClient{
    CallerKey:       callerKey,
    ServerPublicKey: serverPub,
}
```

### 3. Update delegate-based auth fields

```go
// Before:
client := &transfer.PullAuthClient{
    OwnerPublicKey: ownerPub,
    DelegateKey:    delegateKey,
    DelegateChain:  chain,
}

// After:
client := &transfer.FDOKeyAuthClient{
    CallerPublicKey: ownerPub,
    DelegateKey:     delegateKey,
    DelegateChain:   chain,
}
```

### 4. Update result field access

```go
// Before:
result.OwnerKeyFingerprint

// After:
result.KeyFingerprint
```

### 5. Add FDOKeyAuth to push endpoints (new capability)

If you have a push receiver and want to require FDOKeyAuth authentication:

```go
// Create a FDOKeyAuth server for your push endpoint
pushAuthServer := &transfer.FDOKeyAuthServer{
    ServerKey: serverKey,
    Sessions:  transfer.NewSessionStore(60*time.Second, 1000),
    LookupKey: func(callerKey protocol.PublicKey) (int, error) {
        // Check if this key belongs to a trusted supplier
        if isTrustedSupplier(callerKey) {
            return 0, nil
        }
        return -1, nil
    },
    IssueToken: func(callerKey protocol.PublicKey) (string, time.Time, error) {
        return generateToken(callerKey)
    },
}
// Register on your push endpoint root
pushAuthServer.RegisterHandlers(mux, "/api/v1/vouchers")
```

### 6. Add FDOKeyAuth to push client (new capability)

If you push vouchers and the destination requires FDOKeyAuth:

```go
// If no static token is available, authenticate first
authClient := &transfer.FDOKeyAuthClient{
    CallerKey:  supplierKey,
    BaseURL:    destinationURL,
    PathPrefix: "/api/v1/vouchers",
}
result, err := authClient.Authenticate()
if err != nil {
    return err
}
// Use the token for push
dest := transfer.PushDestination{
    URL:   destinationURL,
    Token: result.SessionToken,
}
```

## Config Changes

The `pullauth` CLI subcommand has been renamed to `fdokeyauth`. The old name
`pullauth` is still accepted as an alias for backward compatibility.

### New CLI flags

| Old | New |
|-----|-----|
| `-holder-key` | `-server-key` |

## Future Work

- **External token validation callback** — Allow tokens to come from external
  IdP/OAuth2 services instead of only the built-in DB-backed store.
- **Token caching for push client** — Cache FDOKeyAuth tokens per-destination
  with TTL to avoid re-authenticating on every push attempt.
