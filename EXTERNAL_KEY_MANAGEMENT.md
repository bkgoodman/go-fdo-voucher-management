# Design: External Key Management Integration

**Status:** Phase 1 (key persistence + holder key unification) — ✅ DONE. Phase 2+ (external KMS/HSM/TPM) — Proposal.
**Priority:** Medium — production can operate with file-based key persistence; external KMS/HSM/TPM is a hardening improvement.
**Related:** [PRODUCTION_CONSIDERATIONS.md](PRODUCTION_CONSIDERATIONS.md#key-management), [go-fdo/PRODUCTION_CONSIDERATIONS.md](go-fdo/PRODUCTION_CONSIDERATIONS.md#did-key-minting--software-vs-hardware-keys)

## Problem Statement

### External Key Management

With file-based persistence, the owner key lives on disk as a plaintext PEM. This is sufficient for many deployments but has limitations:

1. **Key exposure** — The private key material is accessible to application code and on the filesystem. A memory dump, core file, or compromised dependency could exfiltrate it.
2. **No audit trail** — There is no record of what was signed, when, or by whom.
3. **No access control** — Any code path with access to the `crypto.Signer` can sign anything. There are no per-operation authorization checks.

For high-security deployments, the owner key should live in an HSM, TPM, or cloud KMS where the private material never leaves the secure boundary.

## Where the Owner Key Is Used

There is **one owner key** that serves all three purposes:

| Purpose | What it does | Source | Status |
|---------|-------------|--------|--------|
| **Voucher signing** | Signs voucher entries when extending ownership chain (`fdo.ExtendVoucher`) | `loadOrGenerateOwnerKey()` → `signingService.OwnerSigner` | ✅ Persistent |
| **DID identity** | Published in the DID document at `/.well-known/did.json`; partners use it to verify our identity | `loadOrGenerateOwnerKey()` → `did.NewDocument()` | ✅ Persistent |
| **PullAuth Holder signing** | Signs PullAuth Challenge to prove to Recipients that this server is the legitimate Holder | `setupDIDMinting()` → `setupPullService(ownerKey)` | ✅ Unified |

All three are the same logical identity — "I am the owner/holder of these vouchers." This matches the pattern in the DI project (`go-fdo-di`) and onboarding service (`go-fdo-onboarding-service`), where the manufacturer/owner key is generated once on first run and persisted as a credential.

## Design Options

### Option A: External Command Callback (Shell-Out)

**The DI project's approach.** The application shells out to an external command for each signing operation. The command receives a JSON request (containing the digest to sign) and returns a JSON response (containing the signature).

```
Application  ──JSON request──▶  External Command  ──▶  HSM/KMS
             ◀──JSON response──                    ◀──
```

**Pros:**
- Maximum flexibility — any HSM, KMS, cloud service, or custom script
- Language-agnostic — the handler can be a shell script, Python, Go binary, etc.
- Already proven in the DI project (`external_hsm_signer.go`)
- No additional Go dependencies
- Users can adapt to their existing tooling

**Cons:**
- Process-per-sign overhead (fork+exec for every voucher extension)
- JSON serialization/deserialization overhead
- Harder to test (requires external command to exist)
- Security of the command itself (file permissions, argument injection)
- Error handling across process boundaries is fragile

**Wire format (from DI project):**

Request (JSON, written to temp file, path passed as `{requestfile}`):
```json
{
  "digest": "<base64-encoded-digest>",
  "request_id": "req-...",
  "timestamp": "2026-02-26T15:00:00Z",
  "signing_options": {
    "hash": "SHA-384",
    "key_type": "ECDSA-P-384"
  }
}
```

Response (JSON on stdout):
```json
{
  "signature": "<base64-encoded-signature>",
  "request_id": "req-...",
  "hsm_info": { "hsm_id": "...", "key_id": "...", "signing_duration_ms": 42 },
  "error": ""
}
```

### Option B: Go Plugin / Interface

Define a Go interface and allow users to provide an implementation at build time (via Go build tags or plugin loading).

```go
// ExternalSigner provides a crypto.Signer backed by an external key store.
type ExternalSigner interface {
    crypto.Signer
    // KeyID returns the identifier of the key in the external store.
    KeyID() string
}
```

**Pros:**
- Native Go performance, no process overhead
- Type-safe, compile-time checked
- Can leverage existing Go libraries (PKCS#11, AWS SDK, Azure SDK, etc.)

**Cons:**
- Requires users to write Go code and recompile
- Go plugins (`plugin` package) are fragile and Linux-only
- Less accessible to ops teams who prefer scripts

### Option C: Built-In KMS Drivers

Ship built-in support for specific, well-known KMS backends:

- **PKCS#11** (Luna HSM, SoftHSM, Thales, nCipher)
- **AWS KMS** (via `aws-sdk-go-v2`)
- **Azure Key Vault** (via `azidentity` + `azkeys`)
- **GCP Cloud KMS** (via `cloud.google.com/go/kms`)
- **HashiCorp Vault Transit** (via HTTP API)

Each backend would implement `crypto.Signer` internally.

**Pros:**
- Zero configuration complexity for supported backends
- No external commands, no serialization overhead
- Well-tested, official SDKs

**Cons:**
- Adds significant dependencies to the binary
- Maintenance burden for each backend
- Still doesn't cover every possible KMS

### Option D: Hybrid (Recommended)

Combine Options A and C:

1. **Built-in PKCS#11 support** as the primary HSM interface (covers most hardware HSMs and some cloud KMS via PKCS#11 bridges)
2. **External command callback** as the escape hatch for anything else
3. **`import_key_file`** remains as the simplest option (pre-generated key on disk)

This gives three tiers of increasing security:

| Tier | Config | Security | Complexity |
|------|--------|----------|------------|
| **File import** | `key_management.import_key_file: /path/to/key.pem` | Key on disk (encrypted FS recommended) | Minimal |
| **External command** | `key_management.mode: external` + `external_command` | Key in any external system | Medium |
| **PKCS#11** | `key_management.mode: pkcs11` + slot/pin/label config | Key in hardware HSM | Higher |

Cloud KMS support (AWS/Azure/GCP/Vault) could be added incrementally as separate drivers or via the external command path.

## Implementation Plan

### Phase 1: Key Persistence + Holder Key Unification — ✅ DONE

**Goal:** Owner key survives restarts, PullAuth uses the same key.

1. ✅ **Key persistence** — `loadOrGenerateOwnerKey()` in `did_minting_setup.go` implements three modes:
   - `import_key_file` → load from PEM file
   - `first_time_init` + `key_export_path` → generate on first run, save, load on subsequent starts
   - Ephemeral fallback with warning

2. ✅ **Holder key unification** — `setupDIDMinting()` returns `crypto.Signer`, passed to `setupPullService()` as the `HolderKey`.

3. ✅ **DID minting refactor** — `setupDIDMinting()` now separates:
   - Key loading (`loadOrGenerateOwnerKey`) → returns `crypto.Signer`
   - DID document construction (`did.NewDocument()` from public key)
   - DID document serving (HTTP handler)
   - Returns the signer for use by both `signingService.OwnerSigner` and `PullAuthServer.HolderKey`

4. ✅ **Tests:**
   - Unit tests: `TestLoadOrGenerateOwnerKey_*` (7 tests covering import, first-time-init, ephemeral, precedence, round-trip)
   - Integration test: `test-key-persistence.sh` (10 tests: generate+persist, survive restart, import, negative)

### Phase 2: External Command Callback (Future)

**Goal:** Optional external signing for HSM/KMS without requiring the private key in the application.

1. **External command signer** — Port the `ExternalHSMSigner` pattern from the DI project:
   - New file: `external_signer.go`
   - Implements `crypto.Signer` by shelling out to a configured command
   - JSON request/response wire format (digest in, signature out)
   - Config: `key_management.mode: external`, `key_management.external_command`, `key_management.external_timeout`
   - Public key loaded from `key_management.public_key_file` (the external system holds the private key; we only need the public key for DID document construction and voucher headers)

2. **Config additions:**
   ```yaml
   key_management:
     mode: "external"           # new mode (in addition to existing internal/import)
     public_key_file: ""        # PEM public key (private key in HSM)
     external_command: ""       # signing command
     external_timeout: 30s
   ```

3. **Tests:**
   - Unit test: `ExternalSigner` with a mock command that returns a known signature
   - Integration test: End-to-end with a shell script that uses `openssl` to sign
   - Negative test: Command timeout, malformed response, wrong key type

### Phase 3: PKCS#11 / TPM (Future)

**Goal:** Native hardware key store support without external commands.

**PKCS#11** (HSMs: Luna, SoftHSM, Thales, nCipher):

1. Add PKCS#11 `crypto.Signer` implementation (likely via `github.com/miekg/pkcs11` or `github.com/ThalesIgnite/crypto11`)
2. Config: `key_management.mode: pkcs11`, `pkcs11_module`, `pkcs11_slot`, `pkcs11_pin`, `pkcs11_key_label`
3. Key discovery: Find key by label in the HSM slot, extract public key for DID document
4. Build tag to keep PKCS#11 dependency optional: `go build -tags pkcs11`

**TPM** (Trusted Platform Modules):

1. Add TPM 2.0 `crypto.Signer` implementation (via `github.com/google/go-tpm` or `github.com/google/go-tpm-tools`)
2. Config: `key_management.mode: tpm`, `tpm_device` (default `/dev/tpmrm0`), `tpm_handle` or `tpm_key_label`
3. Key can be generated inside the TPM (non-exportable) or loaded as a persistent handle
4. Build tag: `go build -tags tpm`
5. Note: The go-fdo library already has TPM support in `go-fdo/tpm/` — may be able to reuse some of that infrastructure

### Phase 4: Cloud KMS Drivers (Future)

Add built-in drivers for AWS KMS, Azure Key Vault, GCP Cloud KMS, HashiCorp Vault Transit. Each as an optional build tag. Lower priority since these can be accessed via the external command path in the meantime.

## Security Considerations

- **Public key must match private key** — When using external mode, the application loads the public key from a file. If this doesn't match the key in the HSM, voucher signatures will be invalid. Add a startup self-test: sign a test payload, verify with the public key.
- **Command injection** — The external command config must not accept user-controlled input in the command template. Only system-controlled variables (`{requestfile}`, `{requestid}`) should be substituted.
- **Temp file security** — The JSON request file contains the digest (not the private key), but should still be written with restrictive permissions (mode `0600`) and cleaned up after use.
- **Timeout** — External signing commands must have a timeout to prevent indefinite hangs. Default 30 seconds.
- **Audit logging** — Log every external signing invocation with request ID, duration, and outcome (success/failure/timeout). This provides the audit trail that in-memory signing lacks.

## Open Questions

1. **Key rotation workflow?** When the owner key is rotated (new key in HSM), all partners need to re-enroll the new DID. Should the application support serving multiple DID documents during a transition period? Or is this an out-of-band process?

2. **PKCS#11 vs cloud-native?** For cloud deployments, PKCS#11 adds complexity (need a PKCS#11 module/bridge). Cloud-native SDKs are simpler but add dependencies. The external command path covers both without code changes.

3. **Should we support key generation inside the HSM via the CLI?** E.g., `fdo-voucher-manager keys generate -mode pkcs11 -slot 0 -label owner-key`. Or assume the HSM key is pre-provisioned by the security team?

## References

- DI project implementation: `external_hsm_signer.go`, `example_hsm_handler.sh`, `example_hsm_handler.py`
- Go `crypto.Signer` interface: https://pkg.go.dev/crypto#Signer
- go-fdo library's HSM guidance: [go-fdo/PRODUCTION_CONSIDERATIONS.md](go-fdo/PRODUCTION_CONSIDERATIONS.md#did-key-minting--software-vs-hardware-keys)
- `did.NewDocument()`: Constructs a DID document from a public key without generating a new key pair
