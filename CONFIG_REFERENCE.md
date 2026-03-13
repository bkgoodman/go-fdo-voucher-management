# Configuration Reference

Complete reference for `config.yaml` options. For CLI commands, see [CLI_REFERENCE.md](CLI_REFERENCE.md).

The configuration file is YAML format. All durations use Go duration syntax (e.g., `10s`, `1h`, `24h`, `500ms`). Boolean fields default to `false` unless noted. String fields default to `""` (empty) unless noted.

## Quick Example

```yaml
server:
  addr: "localhost:8080"

database:
  path: "voucher_manager.db"

voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"

voucher_signing:
  mode: "internal"

did_minting:
  enabled: true
  host: "vouchers.example.com"
  serve_did_document: true
```

---

## Top-Level

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `debug` | bool | `false` | Enable debug-level logging |

---

## `server`

HTTP server settings.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `addr` | string | `localhost:8080` | Listen address (`host:port`) |
| `ext_addr` | string | `""` | External address if different from `addr` (e.g., behind a load balancer). Used for DID document construction if set. |
| `use_tls` | bool | `false` | Enable TLS. **Note:** Currently not wired — the server always calls `ListenAndServe()`. Intended for reverse-proxy awareness (e.g., DID resolver uses HTTP vs HTTPS). |
| `insecure_tls` | bool | `false` | Skip TLS certificate verification on outbound connections (development only) |

---

## `database`

SQLite database settings. All persistent state (transmissions, tokens, partners, audit log) is stored here.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `path` | string | `voucher_manager.db` | Path to SQLite database file. Created automatically on first run. |
| `password` | string | `""` | Database encryption password (reserved for future use) |

---

## `key_management`

Cryptographic key configuration for voucher signing operations.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `key_type` | string | `ec384` | Key algorithm. Options: `ec256` (P-256), `ec384` (P-384), `rsa2048`, `rsa3072` |
| `first_time_init` | bool | `true` | Auto-generate a signing key on first run if none exists in the database |
| `import_key_file` | string | `""` | Path to PEM-encoded private key to import instead of generating. Supports `PRIVATE KEY` (PKCS8), `RSA PRIVATE KEY` (PKCS1), `EC PRIVATE KEY` formats. |

---

## `voucher_receiver`

Inbound push endpoint — accepts vouchers from upstream suppliers (factories, manufacturers, resellers).

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `true` | Enable the HTTP receiver endpoint |
| `endpoint` | string | `/api/v1/vouchers` | HTTP path for the push endpoint (the Push Service Root) |
| `global_token` | string | `""` | Single bearer token accepted for all requests. If set, this token is always valid regardless of the token database. |
| `validate_ownership` | bool | `false` | Validate that received vouchers are signed to our owner key |
| `require_auth` | bool | `false` | Require `Authorization: Bearer <token>` on all push requests. Tokens are checked against `global_token` and the token database. |
| `require_trusted_manufacturer` | bool | `false` | Reject vouchers unless the manufacturer's signing key matches a trusted partner with `can_supply_vouchers` capability. Requires the partner trust store to be populated. |

### Authentication behavior

- `require_auth: false` — All pushes accepted (open receiver)
- `require_auth: true, global_token: ""` — Only database tokens accepted
- `require_auth: true, global_token: "secret"` — Database tokens OR the global token accepted
- `require_trusted_manufacturer: true` — Voucher signature is verified against the partner trust store regardless of token auth

---

## `voucher_signing`

How vouchers are signed over to a new owner key.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `mode` | string | `internal` | Signing mode. Options: `internal` (use database key), `external` (call external command), `hsm` (reserved) |
| `external_command` | string | `""` | Shell command for external signing. Receives voucher on stdin, returns signed voucher on stdout. |
| `external_timeout` | duration | `30s` | Timeout for external signing command |

---

## `ove_extra_data`

OVEExtra data assignment via external callback. Allows attaching arbitrary metadata to vouchers during the pipeline.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `false` | Enable OVEExtra data assignment |
| `external_command` | string | `""` | Shell command with variable substitution: `{serialno}`, `{model}`. Must output a JSON map. |
| `timeout` | duration | `10s` | Command timeout |

### Example

```yaml
ove_extra_data:
  enabled: true
  external_command: "/opt/scripts/get-extra-data.sh {serialno} {model}"
  timeout: 10s
```

---

## `owner_signover`

Next owner key resolution — determines which public key the voucher is signed over to.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `mode` | string | `static` | Resolution mode. Options: `static` (fixed key or DID), `dynamic` (external callback) |
| `static_public_key` | string | `""` | PEM-encoded public key (inline string). Used when `mode: static`. |
| `static_did` | string | `""` | DID URI (e.g., `did:web:customer.example.com:fdo`). Resolved to extract owner key and push endpoint. Used when `mode: static`. |
| `external_command` | string | `""` | Shell command for dynamic resolution. Receives `{serialno}`, `{model}` variables. Must output JSON with `owner_key_pem` or `owner_did`. |
| `timeout` | duration | `10s` | Command timeout |

### Resolution priority

1. If `mode: static` and `static_did` is set → resolve DID for key + endpoint
2. If `mode: static` and `static_public_key` is set → use that key directly
3. If `mode: dynamic` → call `external_command`

### Dynamic callback output format

```json
{"owner_key_pem": "-----BEGIN PUBLIC KEY-----\n..."}
```

or

```json
{"owner_did": "did:web:customer.example.com:fdo"}
```

---

## `voucher_files`

Filesystem storage for voucher files.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `directory` | string | `data/vouchers` | Directory for storing `.fdoov` files, organized by GUID |

---

## `destination_callback`

External command for resolving the transmission destination URL per voucher.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `false` | Enable destination callback |
| `external_command` | string | `""` | Shell command with `{serialno}`, `{model}`, `{guid}` variables. Must output an HTTP URL. |
| `timeout` | duration | `10s` | Command timeout |

### Destination resolution priority

The pipeline resolves destinations in this order (first match wins):

1. **Callback** — if `destination_callback.enabled`
2. **Partner** — if a partner with `can_receive_vouchers` matches the voucher's owner key fingerprint
3. **DID** — if `did_push.enabled`, resolves the next owner's DID for an `FDOVoucherRecipient` service entry
4. **Static** — if `push_service.url` is set

---

## `did_cache`

DID document caching and background refresh for partner trust store entries.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `false` | Enable DID document caching and background refresh worker |
| `refresh_interval` | duration | `1h` | How often to re-fetch DID documents for known partners |
| `max_age` | duration | `24h` | Maximum age before a cached DID document is considered stale |
| `failure_backoff` | duration | `1h` | Wait time after a failed DID resolution attempt before retrying |
| `purge_unused` | duration | `168h` (7 days) | Remove cached DID documents not accessed within this period |
| `purge_on_startup` | bool | `false` | Purge stale DID documents on server startup |

---

## `push_service`

Outbound push — transmit signed vouchers to downstream endpoints.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `false` | Enable push transmission |
| `url` | string | `""` | Static fallback push URL. Used when no callback, partner, or DID destination is found. |
| `auth_token` | string | `""` | Bearer token sent with push requests to the static URL |
| `mode` | string | `fallback` | Push mode. `fallback` = use static URL only when other resolvers fail. `send_always` = always push to static URL in addition to resolved destinations. |
| `delete_after_success` | bool | `false` | Delete voucher file from local storage after successful transmission |
| `retry_interval` | duration | `8h` | Base interval between retry attempts (used by push service; see also `retry_worker`) |
| `max_attempts` | int | `5` | Maximum transmission attempts before marking as `failed` |

---

## `did_push`

DID-based push destination resolution. When enabled, the pipeline resolves the next owner's DID to discover the `FDOVoucherRecipient` service URL.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `true` | Enable DID-based destination resolution for push |

---

## `retry_worker`

Background worker that periodically retries failed or pending transmissions.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `true` | Enable the background retry loop |
| `retry_interval` | duration | `8h` | How often to scan for retryable transmissions |
| `max_attempts` | int | `5` | Maximum attempts before marking as permanently `failed` |

The retry worker uses exponential backoff with ±25% jitter. The base delay doubles each attempt, capped at 24 hours. The server's `Retry-After` header is honored if longer than the computed backoff.

---

## `retention`

Voucher retention policy.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `keep_indefinitely` | bool | `true` | Keep all vouchers forever (overrides `purge_after`) |
| `purge_after` | duration | `0` | Delete vouchers older than this duration. Only applies when `keep_indefinitely: false`. |

---

## `pull_service`

Inbound pull — serve vouchers to authenticated recipients via the FDOKeyAuth protocol.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `false` | Enable FDOKeyAuth + Pull API endpoints |
| `session_ttl` | duration | `60s` | FDOKeyAuth session lifetime (time to complete the 3-step handshake) |
| `max_sessions` | int | `1000` | Maximum concurrent FDOKeyAuth sessions |
| `token_ttl` | duration | `1h` | Bearer token lifetime after successful FDOKeyAuth authentication |
| `reveal_voucher_existence` | bool | `false` | If `false`, unauthenticated requests to pull endpoints return generic errors (no information leakage about voucher existence) |

### Security notes

- Vouchers are scoped by owner key fingerprint — a recipient can only see vouchers signed over to their key
- Sessions are single-use (consumed after the Prove step)
- Session IDs are 128-bit cryptographically random

---

## `partners`

Bootstrap partner entries loaded into the trust store on server startup. This is an array of partner definitions. Existing partners (matched by `id`) are skipped — bootstrap is additive and idempotent.

Partners can also be managed at runtime via the `partners` CLI command (see [CLI_REFERENCE.md](CLI_REFERENCE.md#partners)).

```yaml
partners:

  - id: "acme-mfg"
    can_supply: true
    did: "did:web:mfg.acme.com:vouchers"

  - id: "customer-a"
    can_receive: true
    key_file: "/etc/fdo/keys/customer-a-pub.pem"
    push_url: "https://customer-a.example.com/api/v1/vouchers"
    auth_token: "push-secret"
```

### Partner fields

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `id` | string | **(required)** | Unique partner identifier |
| `can_supply` | bool | `false` | Partner can supply vouchers to us (upstream). Their manufacturer key is trusted for signature verification. |
| `can_receive` | bool | `false` | We push vouchers to this partner (downstream). Used by the destination resolver. |
| `did` | string | `""` | DID URI (`did:web:...` or `did:key:...`) |
| `key_file` | string | `""` | Path to PEM-encoded public key file |
| `push_url` | string | `""` | FDOVoucherRecipient push URL |
| `pull_url` | string | `""` | FDOVoucherHolder pull URL |
| `auth_token` | string | `""` | Bearer token for push requests to this partner |
| `enabled` | bool | `true` | Whether this partner is active. Omit or set to `true` for enabled. |

At least one of `can_supply` or `can_receive` should be set (otherwise the partner has no authorization).

---

## `did_minting`

DID document generation and serving. Creates a `did:web` identity for this service, including an owner key and service endpoints.

| Field | Type | Default | Description |
| ------- | ------ | --------- | ------------- |
| `enabled` | bool | `false` | Enable DID minting |
| `host` | string | `""` | Hostname for `did:web` URI (e.g., `vouchers.example.com` or `localhost:8080`) |
| `path` | string | `""` | Optional sub-path for `did:web` (e.g., `fdo` → `did:web:example.com:fdo`) |
| `voucher_recipient_url` | string | `""` | URL for the `FDOVoucherRecipient` service entry in the DID document (push endpoint). If empty, no push service entry is emitted. |
| `voucher_holder_url` | string | `""` | URL for the `FDOVoucherHolder` service entry in the DID document (pull endpoint). If empty but `pull_service.enabled`, auto-constructed from server address. |
| `serve_did_document` | bool | `true` | Serve the DID document at `/.well-known/did.json` |
| `export_did_uri` | bool | `true` | Log the `did:web:...` URI on server startup |
| `key_export_path` | string | `""` | Save the DID-minted private key to a PEM file (PKCS8 format). Useful for the `pull` command which needs the owner key file. |

### Example: Full DID setup

```yaml
did_minting:
  enabled: true
  host: "vouchers.example.com"
  voucher_recipient_url: "https://vouchers.example.com/api/v1/vouchers"
  voucher_holder_url: "https://vouchers.example.com/api/v1/pull/vouchers"
  serve_did_document: true
  export_did_uri: true
  key_export_path: "data/owner-key.pem"
```

This generates a DID document at `https://vouchers.example.com/.well-known/did.json` with:

- Owner public key (from the generated or imported key)
- `FDOVoucherRecipient` service entry (push endpoint)
- `FDOVoucherHolder` service entry (pull endpoint)
- FDO JSON-LD context

---

## Configuration Recipes

### Manufacturer (receive from factories, push to customers)

```yaml
voucher_receiver:
  enabled: true
  require_auth: true
  require_trusted_manufacturer: true

voucher_signing:
  mode: internal

owner_signover:
  mode: static
  static_did: "did:web:customer.example.com:fdo"

did_push:
  enabled: true

pull_service:
  enabled: true

retry_worker:
  enabled: true

did_minting:
  enabled: true
  host: "mfg.example.com"
  voucher_recipient_url: "https://mfg.example.com/api/v1/vouchers"
  serve_did_document: true

partners:

  - id: "factory-a"
    can_supply: true
    did: "did:key:z6Mk..."
```

### End Customer (receive only)

```yaml
voucher_receiver:
  enabled: true
  require_auth: true

push_service:
  enabled: false

did_push:
  enabled: false

pull_service:
  enabled: false

retry_worker:
  enabled: false

did_minting:
  enabled: true
  host: "customer.example.com"
  voucher_recipient_url: "https://customer.example.com/api/v1/vouchers"
  serve_did_document: true
```

### Reseller (bidirectional)

```yaml
voucher_receiver:
  enabled: true
  require_trusted_manufacturer: true

voucher_signing:
  mode: internal

owner_signover:
  mode: dynamic
  external_command: "/opt/scripts/resolve-buyer.sh {serialno} {model}"

did_push:
  enabled: true

pull_service:
  enabled: true

retry_worker:
  enabled: true

partners:

  - id: "upstream-oem"
    can_supply: true
    did: "did:web:oem.example.com:fdo"

  - id: "buyer-corp"
    can_receive: true
    push_url: "https://buyer.example.com/api/v1/vouchers"
    key_file: "/etc/fdo/keys/buyer-pub.pem"
```
