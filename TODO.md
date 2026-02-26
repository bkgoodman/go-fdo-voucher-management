# TODO: Spec Compliance Checklist

Gap analysis comparing `fdo-appnote-voucher-transfer.bs` specification against the current implementation.

**Spec section numbering:**

| § | Section |
|---|---|
| 1 | Introduction |
| 2 | Terminology |
| 3 | Use Cases and Requirements |
| 4 | Transfer Models |
| 5 | Voucher File Format |
| 6 | Service Root URLs |
| 7 | Push API Specification |
| 8 | Pull API Specification |
| 9 | Pull Authentication Protocol (PullAuth) |
| 10 | Security Framework (defense-in-depth; see §12 for core model) |
| 11 | Voucher Sequestering |
| 12 | DID Integration (core security model) |
| 13 | Error Handling and Retry Logic |
| 14 | Security Considerations |
| 15 | Implementation Guidelines |

**Legend:** ✅ Implemented | ⚠️ Partial / Deviation | ❌ Not Implemented (MUST/SHOULD, security-relevant) | � Not Implemented (MAY, defense-in-depth, nice-to-have) | � Spec-optional (noted for awareness)

**Security model note:** The spec's core security model is DID-based mutual authentication (§12). Token-based auth, mTLS, and business logic validation are **defense-in-depth layers** (§10, §12.7) — additive, not required. Items in this TODO are prioritized accordingly.

---

## 1. Voucher File Format (Spec §5)

- ✅ `.fdoov` file extension used throughout (`voucher_file_store.go`, `pull_command.go`)
- ✅ PEM encoding with `-----BEGIN OWNERSHIP VOUCHER-----` / `-----END OWNERSHIP VOUCHER-----`
- ✅ **PEM line wrapping**: All PEM write paths now use the canonical `fdo.FormatVoucherPEM()` / `fdo.FormatVoucherCBORToPEM()` library functions which use `encoding/pem` for proper RFC 7468 line wrapping.
- ✅ `application/x-fdo-voucher` MIME type referenced in spec; used for download Content-Type
- ✅ 10 MB max voucher size (`maxVoucherSize` constant)
- 📋 **Gzip support**: Spec says vouchers MAY be compressed with `Content-Encoding: gzip`. Not implemented in receiver or push client.

## 2. Push API — POST {root} (Spec §7.1)

- ✅ Endpoint path configurable via `voucher_receiver.endpoint` (spec defines all paths as relative to a Service Root URL, §6)
- ✅ `multipart/form-data` with `voucher` file field
- ✅ Optional `serial`, `model` form fields
- ⚠️ **`manufacturer` field**: Spec defines `manufacturer` as an optional form field. Implementation reads it from `r.FormValue("manufacturer")` and logs it, but does not persist it in the transmission record (`VoucherTransmissionRecord` has no `Manufacturer` field).
- 🔲 **`timestamp` form field**: Spec defines optional `timestamp` (ISO 8601) form field. Not read or used.
- ✅ **`X-FDO-Version` header**: Spec RECOMMENDS sending/checking protocol version header. Server sends `X-FDO-Version: 1.0` on all responses via `fdoVersionMiddleware()` in `main.go`.
- 🔲 **`X-FDO-Client-ID` header**: Spec defines optional client identifier header. Not implemented.
- ✅ `200 OK` with JSON response body containing `status`, `voucher_id`, `message`, `timestamp`
- 🔲 **`202 Accepted`**: Spec defines async acceptance. Implementation always returns `200 OK` synchronously (pipeline runs async in goroutine but response is immediate `200`). Consider returning `202` when pipeline is async.
- ✅ `400 Bad Request` for invalid format
- ✅ `401 Unauthorized` for auth failure
- ✅ `409 Conflict` for duplicate voucher (checked via file existence)
- ✅ `413 Payload Too Large` for oversized files
- ✅ **`401` vs `403`**: 401 for missing credentials, 403 for invalid/expired token. Auth returns a three-state result (`authOK`, `authNone`, `authInvalid`).
- 🔲 **`429 Too Many Requests`**: No rate limiting implemented. Defense-in-depth; typically handled by API gateways.
- 🔲 **`503 Service Unavailable`**: No explicit handling.

## 3. Push API — GET {root}/status/{identifier} (Spec §7.2)

Spec defines a RECOMMENDED status query endpoint for diagnosing missing-voucher scenarios (e.g., a device arrives but has no voucher — was it never sent, lost, or failed?). The `{identifier}` can be either a voucher GUID or device serial number.

- 🔲 **Not implemented.** The transmission store has the data, but no HTTP handler exposes it.
- 🔲 **GUID lookup**: Not implemented. DB has `voucher_id` (GUID) column, so this is straightforward.
- 🔲 **Serial number lookup**: Not implemented. DB has serial number in transmission records.
- 📋 Spec says RECOMMENDED, not REQUIRED. Implementations that don't support it SHOULD return `501 Not Implemented`.

## 5. Pull API — GET {root} (Spec §8.1)

- ✅ List endpoint implemented (configurable Pull Service Root)
- ✅ **Path alignment**: Spec defines all endpoints relative to a Service Root URL (§6). Push and pull use separate roots.
- ✅ `since` query parameter (RFC 3339)
- ✅ `until` query parameter (RFC 3339)
- ✅ `limit` query parameter with default (50)
- ✅ `continuation` query parameter for pagination
- 📋 `status` query parameter: Parsed by `pull_holder.go:parseListFilter()` but not actually filtered in `voucher_pull_store.go:List()` — the filter is passed through but `Status` field is ignored in the query.
- ✅ **Response fields**: Spec now defines only `voucher_id` as REQUIRED; all other fields (`serial`, `model`, `manufacturer_id`, `status`, `created_at`, `size_bytes`, `checksum`) are OPTIONAL. Implementation returns `guid`, `serial_number`, `model_number`, `device_info`, `created_at` — this is compliant since missing fields are allowed.
- ✅ **`total_count`**: Spec now defines this as OPTIONAL (may be expensive to compute). Implementation returns per-page count, which is acceptable — but could also omit it entirely.
- ✅ **`fields` query parameter**: Spec defines optional field selection (`fields=voucher_id,serial,created_at`). Implemented in `pull_holder.go:parseListFilter()` — unrequested optional fields are zeroed so `omitempty` drops them.
- ✅ `continuation` and `has_more` in response
- ⚠️ **Continuation token security (Spec §8.5)**: Continuation tokens are plaintext RFC3339 timestamps — not cryptographically bound to session, not MAC'd, no expiry enforcement, trivially forgeable. Spec SHOULD requires cryptographic binding.
- ✅ **Pagination signals**: `continuation` and `has_more` implemented. Spec now clarifies that `has_more` is the authoritative signal — `total_count` is optional.

## 6. Pull API — GET {root}/{voucher_id}/download (Spec §8.2)

- ✅ Download endpoint implemented with `/download` suffix (`pull_holder.go:43`)
- ✅ Client sends download requests to `{root}/{guid}/download` (`pull_initiator.go:110`)
- ✅ **Content-Type**: Returns `application/x-fdo-voucher`.
- ✅ **`Content-Disposition` header**: Returns `attachment; filename="{voucher_id}.fdoov"`.
- ✅ **`X-FDO-Checksum` header**: Returns `sha256:{hash}`.
- ✅ **`Content-Length` header**: Set from raw voucher bytes.

## 7. Pull API — Subscription / Notification (Spec §8.3–8.4)

- 🔲 **Long-polling** (`GET {root}/subscribe`): Not implemented.
- 🔲 **Server-Sent Events** (`GET {root}/stream`): Not implemented.
- 📋 These are spec-defined but lower priority for initial implementation.

## 8. PullAuth Protocol (Spec §9)

### 8.1 Wire Format

- ✅ CBOR encoding for all PullAuth messages
- ✅ `Content-Type: application/cbor` set on requests and responses
- ✅ **Content-Type validation**: Server rejects PullAuth requests with an explicit wrong `Content-Type` (returns `415 Unsupported Media Type`). Lenient if `Content-Type` is omitted.

### 8.2 PullAuth.Hello (Spec §9.4)

- ✅ POST `{root}/auth/hello`
- ✅ Message structure: `[OwnerKey, DelegateChain, NoncePullRecipient_Prep, ProtocolVersion]`
- ✅ OwnerKey as COSE_Key (via `protocol.PublicKey`)
- ✅ DelegateChain as X5CHAIN or null
- ✅ 16-byte nonce
- ✅ ProtocolVersion = 1

### 8.3 PullAuth.Challenge (Spec §9.4 Response)

- ✅ Response structure: `[SessionId, NoncePullHolder_Prep, NoncePullRecipient, HashPullHello, HolderSignature, HolderInfo]`
- ✅ Session ID (128-bit random)
- ✅ Nonce echo
- ✅ Hash continuity (SHA-256 of Hello CBOR bytes)
- ✅ COSE_Sign1 HolderSignature with correct payload structure including `"PullAuth.Challenge"` type tag
- ✅ HolderInfo (optional, includes `voucher_count`)
- ✅ **HolderInfo structure**: Spec defines it as a CBOR map with optional keys `"holder_id"`, `"voucher_count"`, `"algorithms"`. Fixed: custom `MarshalCBOR`/`UnmarshalCBOR` methods encode as CBOR map with text string keys.

### 8.4 PullAuth.Prove (Spec §9.5)

- ✅ POST `{root}/auth/prove`
- ✅ Message structure: `[SessionId, NoncePullHolder, HashPullChallenge, RecipientSignature]`
- ✅ COSE_Sign1 RecipientSignature with `"PullAuth.Prove"` type tag
- ✅ Hash continuity verification
- ✅ Nonce verification
- ✅ Session single-use (Get removes session)

### 8.5 PullAuth.Result (Spec §9.5 Response)

- ✅ Structure: `[Status, SessionToken, TokenExpiresAt, OwnerKeyFingerprint, VoucherCount]`
- ✅ `Status = "authenticated"`
- ✅ `SessionToken` as tstr for Bearer header use
- ✅ `TokenExpiresAt` as Unix timestamp
- ✅ **OwnerKeyFingerprint**: Spec says "SHA-256 hash of the CBOR-encoded authenticated Owner Key". All three locations (PullAuth server, token store, DB fingerprinting) now use CBOR-based SHA-256 via `FingerprintProtocolKey()` / `FingerprintPublicKey()` in `key_utils.go`. Aligned.
- ✅ `VoucherCount` (optional)

### 8.6 Holder Signature Verification by Recipient (Spec §9.8.3)

- ✅ **Client verifies HolderSignature**: `PullAuthClient.HolderPublicKey` field enables cryptographic verification of the Holder's COSE_Sign1 signature. When set, verifies signature + payload contents (type tag, nonces, hash, owner key). When nil, verification is skipped with a warning. CLI: `-holder-key <file>`.

### 8.7 Delegation Support (Spec §9.6)

- ✅ Delegate chain validation via `fdo.VerifyDelegateChain()`
- ✅ `fdo-ekt-permit-voucher-claim` permission OID (`1.3.6.1.4.1.45724.3.1.5`) checked
- ✅ Delegate key signing in PullAuth.Prove
- ✅ Leaf certificate public key used for signature verification
- ✅ CSR workflow for cross-org delegate issuance (go-fdo delegate commands)

### 8.8 Session Management (Spec §9.8.4)

- ✅ Session TTL (configurable, default 60s)
- ✅ Single-use sessions (Get removes)
- ✅ Max concurrent sessions limit (configurable, default 1000)
- ✅ Cryptographically random session IDs (128-bit)
- 📋 **Per-source-IP session limits**: Spec SHOULD; not implemented.
- 📋 **Per-Owner-Key session limits**: Spec SHOULD; not implemented.

## 9. Security Framework (Spec §10) — Defense-in-Depth

The spec's §10 (Security Framework) describes defense-in-depth layers that are **additive** to the core DID-based security model (§12). Token-based auth (Model 1), mTLS (Model 2), and business logic validation (Model 4) are NOT required for secure voucher transfer. Model 3 (voucher signature validation) and Model 5 (Owner-Key-Based Auth / PullAuth) are the core mechanisms, implemented as part of the protocol itself.

### 9.1 Core Protocol Authentication (REQUIRED)

- ✅ Owner-Key-Based Authentication for pull (PullAuth, §9) — Model 5
- ✅ **Voucher signature verification against manufacturer DID keys**: Spec §12.2 Case 2. Fully implemented: `PartnerStore` with trust DB and capability-scoped authorization (`can_supply_vouchers`/`can_receive_vouchers`), `IsTrustedSupplier()` verification in receiver handler (supply-only), `did:key` resolution (P-256/P-384), DID document refresh worker, partner CLI commands (`add/list/show/remove/export` with `-supply`/`-receive` flags), config-file bootstrap (`partners:` section), multi-partner destination resolution via fingerprint lookup (receive-only).
- ✅ Token-based (Bearer) authentication for push — works as defense-in-depth layer

### 9.2 Defense-in-Depth Layers (OPTIONAL per §12.7)

- � **JWT support**: Spec describes JWT format with scoped claims (`scope`, `limits`, etc.) as a defense-in-depth option. Implementation uses opaque bearer tokens (database-stored strings), not JWT. Adequate for current needs.
- � **mTLS support**: Defense-in-depth layer, not core. Not implemented.
- � **API key support**: Not as a separate mechanism — only opaque bearer tokens.

### 9.3 Transport Security

- ⚠️ **TLS**: Config has `use_tls` flag but `runServer()` always calls `ListenAndServe()`, never `ListenAndServeTLS()`. TLS is not actually implemented at the server level (assumed to be handled by reverse proxy).
- � **HSTS headers**: Not set. Typical for reverse-proxy deployments.
- 🔲 **Certificate validation enforcement**: No TLS certificate validation code in the push client. Go's `net/http` validates TLS certs by default; this would be about custom CA pinning.

### 9.4 Rate Limiting (Spec §10.6)

- 📋 **No rate limiting** on any endpoint. Spec SHOULD requires per-manufacturer, per-IP, and burst rate limits with `429` + `Retry-After`. Defense-in-depth; typically handled by API gateways/WAFs in production.

### 9.5 Error Response Format (Spec §10.2)

- ⚠️ **Error format**: Spec defines structured error with `error`, `message`, `error_code`, `timestamp`, `request_id`. PullAuth and Pull API errors include `request_id`. Push receiver errors include `request_id` and `timestamp`. Still missing: `error_code` field across all error responses.

### 9.6 Security Monitoring (Spec §10.7)

- ⚠️ **Partial**: Audit logging exists (voucher_receiver_audit table) but no structured monitoring for auth failures by manufacturer, signature attempts, geographic patterns, or rate limit violations.

## 10. Voucher Sequestering (Spec §11)

- 🔲 **Not implemented.** Spec defines a quarantine/sequestering workflow with risk-based assessment, approval gates, and configurable quarantine durations. Implementation immediately accepts and processes vouchers. This is a business-logic feature, not a protocol security requirement.

## 11. DID Integration (Spec §12) — Core Security Model

This is the **primary security model** for the protocol. Mutual DID exchange is the sole prerequisite for all voucher transfer security (§12.1).

### 11.1 DID Resolution and Document Serving

- ✅ `did:web` resolution to extract public key and `FDOVoucherRecipient` service endpoint
- ✅ DID document serving via `.well-known/did.json`
- ✅ DID-based destination resolution in the pipeline

### 11.2 Trust Foundation (Spec §12.1)

- ✅ **Spec complete**: Mutual DID exchange defined as the primary security model. All four push/pull trust cases are covered by DID-conveyed keys and protocol-level cryptography — no token exchange needed.

### 11.3 DID-Based Security Model (Spec §12.2)

- ✅ **Spec complete**: Four trust cases documented (push→recipient, push←provider, pull←holder, pull→recipient).
- ✅ **Case 2 (push←provider) implemented**: Voucher manufacturer key verification via `PartnerStore` + `IsTrustedSupplier()` in receiver handler (enforces `can_supply_vouchers`). `RequireTrustedManufacturer` config flag controls enforcement. Partner CLI commands with capability flags + config-file bootstrap for enrollment. Multi-partner destination resolution via fingerprint lookup (enforces `can_receive_vouchers`).

### 11.4 FDO JSON-LD Context (Spec §12.4)

- ✅ `FDOContextURL` (`https://fidoalliance.org/ns/fdo/v1`) added to `did/document.go`
- ✅ Included in generated DID documents' `@context` array
- ✅ Spec defines the A+D hybrid approach: publish for formal JSON-LD validation, but plain-JSON consumers MAY omit it

### 11.5 DID Document Service Types (Spec §12.5)

- ✅ `FDOVoucherRecipientServiceType` constant and service entry emitted by `NewDocument()`
- ✅ `FDOVoucherHolderServiceType` constant added to `did/document.go`
- ✅ **`FDOVoucherHolder` service entry**: Emitted by `NewDocument()` when `voucherHolderURL` is non-empty. `did_minting_setup.go` auto-constructs the URL from pull service config. Config field `voucher_holder_url` added to `DIDMinting`.

### 11.6 Optional TLS Certificate Authority (Spec §12.5.3)

- ✅ **`tlsCertificateAuthority` in DID service entries**: `Service` struct has `TLSCertificateAuthority string` field (PEM-encoded, `omitempty`). Not yet consumed during DID resolution (parsing/pinning is a separate task).

### 11.7 Defense-in-Depth Extension (Spec §12.7)

- 🔲 **DID Document `fido-device-onboarding` extension**: Spec defines a simplified informational extension with `additionalAuthentication` and `trusted_manufacturers`. Implementation uses the simpler `fido-device-onboard` service type (via go-fdo's `did.Document`). Not the full spec extension. This is an informational/defense-in-depth extension (§12.7), not required for core security.

### 11.8 Other DID Methods

- ✅ **`did:key` resolution**: Fully implemented in `did_resolver.go` via `parseDIDKey()`. Supports P-256 (`0x8024`) and P-384 (`0x8124`) multicodec prefixes with base58-btc decoding and EC point decompression.

## 12. Error Handling and Retry Logic (Spec §13)

- ✅ Retry with configurable max attempts (default 5)
- ✅ **Backoff strategy**: Exponential backoff with ±25% jitter. Base delay doubles each attempt, capped at 24h. Honors server `Retry-After` header if longer than computed backoff.
- 🔲 **Circuit breaker**: Spec SHOULD temporarily stop sends to consistently failing endpoints. Not implemented.
- 🔲 **Dead letter queue**: Spec SHOULD store failed transfers for manual review. Failed records stay in DB with `failed` status but there's no explicit dead letter / alerting mechanism.
- ✅ **`Retry-After` header handling**: `PushError` captures `Retry-After` from HTTP response. `AttemptRecord` uses it as minimum wait if longer than exponential backoff.
- ✅ **Error classification**: `PushError.IsTransient()` classifies 429 and 5xx as transient (retry), 4xx as permanent (fail immediately). Network errors default to transient.

## 13. Implementation Guidelines (Spec §15)

- ✅ API paths configurable via Service Root URLs (spec §6)
- ✅ **Content negotiation**: Servers return `application/json` for non-file responses and `application/x-fdo-voucher` for voucher downloads.
- ✅ **Idempotency**: POST returns `409 Conflict` for duplicate voucher submissions. Spec says implementations SHOULD use voucher GUID as deduplication key.
- ⚠️ **Pagination**: Continuation tokens are not opaque (plaintext timestamps), don't expire, and `total_count` is per-page not total.

## 14. Code-Level Issues

- [x] **PEM line wrapping**: Fixed — all PEM write paths now use `fdo.FormatVoucherPEM()` / `fdo.FormatVoucherCBORToPEM()`.
- [ ] **`main.go:600-606`**: `keysExportCmd()` writes a hardcoded placeholder PEM key — not a real key export. Should export actual owner key from DB.
- [ ] **`pullauth_server.go:183`**: `session.ChallengeBytes` is set AFTER `s.Sessions.Create(session)` — the session stored in the map may not have `ChallengeBytes` updated since Go maps store copies of structs. This could cause hash continuity verification to fail if `Session` is stored by value. Current code works because `Session` is a pointer (`*Session`), but the comment "We need to re-fetch and update since Create already stored it" suggests uncertainty about this.

---

## Priority Summary

### High Priority — Core DID-Based Security Model Gaps

These are gaps in the spec's **core** security model (§12) — the mechanisms that are REQUIRED for secure voucher transfer.

- [x] **Voucher signature verification against manufacturer DID keys** (§12.2 Case 2) — PartnerStore with capability-scoped trust DB (`can_supply_vouchers`/`can_receive_vouchers`), `did:key` resolution, `IsTrustedSupplier()` on receive, DID doc refresh worker, partner CLI with `-supply`/`-receive` flags, config-file bootstrap, multi-partner destination resolution (receive-only) via fingerprint.
- [x] **Emit `FDOVoucherHolder` DID service entry** — Wired into `NewDocument()`, `Mint()`, and `did_minting_setup.go` with auto-construction from pull service config.
- [x] Return `application/x-fdo-voucher` Content-Type on voucher downloads (§8.2, §15)
- [x] Add `Content-Disposition` header on voucher downloads (§8.2)
- [x] Validate `Content-Type: application/cbor` on PullAuth requests (§9.2 MUST)
- [x] Classify transient vs permanent HTTP errors in retry logic (§13)

### Medium Priority — Spec Compliance (SHOULD items)

- [x] Resolve HolderInfo wire format: CBOR map (spec) vs CBOR array (implementation) — fixed with custom MarshalCBOR/UnmarshalCBOR
- [x] Align OwnerKeyFingerprint algorithm: all locations now use CBOR-based SHA-256 matching spec §9.8
- [x] Implement exponential backoff with jitter for retries (§13)
- [x] Add `Retry-After` header handling in push client (§13)
- [ ] Cryptographically bind continuation tokens to session (§8.5)
- [x] Add `X-FDO-Checksum` header on downloads (§8.2)
- [x] Add `request_id` to error responses (§10.2)
- [x] Distinguish 401 vs 403 error responses (§7.1)
- [x] Add `X-FDO-Version` header support (§7.1) — middleware adds `X-FDO-Version: 1.0` to all responses
- [x] Add HolderSignature verification support in PullAuth client using Holder's DID key (§9.8.3)
- [x] Add `tlsCertificateAuthority` field to `Service` struct in `did/document.go` (§12.5.3)
- [x] Implement `fields` query parameter for Pull API list endpoint (§8.1) — supports voucher_id, serial_number, model_number, device_info, created_at

### Lower Priority — Defense-in-Depth / MAY / Future

These are defense-in-depth layers (§12.7), optional spec features, or future enhancements. Not required for protocol security.

- [ ] Add JWT token support (scoped claims, quotas) — defense-in-depth (§10)
- [ ] Add mTLS authentication support — defense-in-depth (§10)
- [ ] Implement rate limiting with `429` responses — defense-in-depth, typically handled by API gateway
- [ ] Implement `GET {root}/status/{identifier}` endpoint (§7.2, RECOMMENDED)
- [ ] Implement voucher sequestering / quarantine workflow (§11)
- [ ] Implement long-polling endpoint `GET {root}/subscribe` (§8.3)
- [ ] Implement SSE stream endpoint `GET {root}/stream` (§8.4)
- [ ] Add gzip compression support for voucher uploads (§5)
- [x] Implement `did:key` resolution (P-256, P-384 via multicodec + base58-btc)
- [ ] Implement circuit breaker for push retries (§13)
- [ ] Implement dead letter queue / alerting for permanently failed pushes (§13)
- [ ] Add DID document `fido-device-onboarding` informational extension (§12.7)
- [ ] Implement TLS at the server level (not just reverse-proxy)
- [ ] Add HSTS headers
- [ ] Implement `status` filter in pull list query (§8.1)
- [ ] Per-source-IP and per-Owner-Key PullAuth session limits (§9.8.4)
- [ ] Fix `keysExportCmd()` to export real owner key, not placeholder (code issue)
- [ ] Add `manufacturer` field to transmission record persistence (§7.1)
- [ ] Add `timestamp` form field parsing in push receiver (§7.1)
- [ ] Add `202 Accepted` response for async pipeline processing (§7.1)

---

## Quick Wins Action Plan

All original quick-win batches have been completed ✅:

- ~~**Batch 1**: Download Response Headers~~ — Content-Type, Content-Disposition, X-FDO-Checksum, Content-Length
- ~~**Batch 2**: PullAuth Content-Type Validation~~ — 415 for wrong Content-Type
- ~~**Batch 3**: FDOVoucherHolder DID Service Entry~~ — wired into NewDocument(), Mint(), did_minting_setup.go
- ~~**Batch 4**: Error Response Cleanup~~ — request_id in all errors, 401 vs 403 distinction
- ~~**Batch 5**: Retry Logic Hardening~~ — exponential backoff, jitter, Retry-After, transient/permanent classification
- ~~**Batch 6**: tlsCertificateAuthority Struct Field~~ — added to Service struct

### Remaining Work (needs design)

- ~~⚠️ **Partner CLI commands + config bootstrap**~~ — DONE: `partners add/list/show/remove/export` CLI with capability flags (`-supply`, `-receive`). Config bootstrap via `partners:` section (`can_supply`/`can_receive` fields). DID resolution on add.
- ~~⚠️ **Multi-partner destination resolution**~~ — DONE: `VoucherDestinationResolver` queries `PartnerStore` by owner key fingerprint. Only `can_receive_vouchers` partners are routed to. Only `can_supply_vouchers` partners are trusted as voucher sources (`IsTrustedSupplier`). Priority: callback → partner → DID → static.
- ~~🔲 **CLI command reference**~~ — DONE: [CLI_REFERENCE.md](CLI_REFERENCE.md) — complete reference for all subcommands (`server`, `vouchers`, `tokens`, `partners`, `pull`, `pullauth`, `generate`, `keys`), flags, and examples. README updated to reference it.
- ~~🔲 **Configuration reference**~~ — DONE: [CONFIG_REFERENCE.md](CONFIG_REFERENCE.md) — complete reference for all `config.yaml` sections, fields, types, defaults, and usage notes including recipes. README updated to reference it.
- ❌ **Cryptographic continuation tokens** — needs HMAC key management, token format design (§8.5 SHOULD)
- 🔲 **`error_code` in error responses** — all errors have `request_id` but still missing `error_code` field
- 🔲 **`status` filter in pull list query** — parsed but not applied in DB query
- 🔲 **`manufacturer` field persistence** — logged but not stored in VoucherTransmissionRecord
