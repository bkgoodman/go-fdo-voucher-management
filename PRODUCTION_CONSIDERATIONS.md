# Production Considerations

This document covers what you need to think about before deploying the FDO Voucher Manager in a production environment. It focuses on **this application** — the voucher service that receives, stores, signs, and transmits ownership vouchers. For library-level concerns (certificate validation, protocol security, revocation checking), see [go-fdo/PRODUCTION_CONSIDERATIONS.md](go-fdo/PRODUCTION_CONSIDERATIONS.md).

## Database

### SQLite Limitations

The application uses SQLite via [ncruces/go-sqlite3](https://github.com/ncruces/go-sqlite3). SQLite is an excellent embedded database, but has characteristics you should understand for production:

- **Single-writer concurrency** — SQLite allows only one writer at a time. Under heavy concurrent push load (many vouchers arriving simultaneously), write contention can cause `SQLITE_BUSY` errors. WAL mode (Write-Ahead Logging) improves this significantly but does not eliminate it.
- **No network access** — SQLite is file-based. You cannot run multiple voucher manager instances against the same database file (no shared-nothing clustering).
- **File size** — A single SQLite file can grow to terabytes, but very large databases with millions of transmission records may benefit from periodic archival.

### Recommendations

- **Enable WAL mode**: Add `?_pragma=journal_mode(wal)` to the database connection string for better read concurrency and crash recovery. (The current code uses `?_pragma=foreign_keys(on)` — WAL should be added.)
- **Regular VACUUM**: Schedule periodic `VACUUM` to reclaim space after bulk deletions (e.g., after purging old transmissions).
- **For high throughput**: Consider migrating to PostgreSQL or MySQL. The application's SQL is straightforward and uses no SQLite-specific features beyond the driver. The `VoucherTransmissionStore`, `VoucherReceiverTokenManager`, and `PartnerStore` would need a different driver but no schema redesign.
- **Connection pooling**: The current code opens a single `*sql.DB` which Go manages as a pool. For SQLite, set `SetMaxOpenConns(1)` to avoid write contention. For PostgreSQL, use a larger pool.

### Schema Migrations

The application uses `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE` for schema creation and migration. This is adequate for initial deployment but does not handle all upgrade paths cleanly:

- **Track schema versions**: Consider adding a `schema_version` table or using a migration tool (e.g., [golang-migrate](https://github.com/golang-migrate/migrate)) for controlled upgrades.
- **Backup before upgrades**: Always back up the database file before deploying a new version.

---

## Persistent Storage

### Voucher Files

Vouchers are stored as `.fdoov` files on the filesystem under `voucher_files.directory` (default: `data/vouchers`). Each voucher is named by its GUID.

**Production requirements:**

- **Durable storage** — Use a filesystem backed by reliable storage (SAN, EBS, persistent volume claim in Kubernetes). Do not use ephemeral or tmpfs storage.
- **Backup** — Voucher files represent cryptographic documents that may be the only copy. Back up the voucher directory alongside the database.
- **Permissions** — The voucher directory should be readable/writable only by the service user. Voucher files contain the device's cryptographic identity.

### Database File

The SQLite database file (`voucher_manager.db` by default) contains:
- All transmission records and their state
- Authentication tokens
- Partner trust store entries (including cached DID documents and public keys)
- Receiver audit log

**This file is critical state.** If lost, the service loses all transmission history, token configuration, and partner enrollments.

**Backup strategy:**

```bash
# SQLite online backup (safe while the server is running)
sqlite3 voucher_manager.db ".backup /backups/voucher_manager_$(date +%Y%m%d).db"
```

- Back up both the database and the voucher file directory together to maintain consistency.
- If using `delete_after_success: true`, voucher files are removed after successful transmission — the database is the only record of what was sent.

---

## Key Management

### Current Behavior

There is **one owner key** that serves three purposes: voucher signing (extending the ownership chain), DID identity (the public key in the DID document), and FDOKeyAuth Holder signing (proving to Recipients that this server is the legitimate Holder). This matches the pattern in the DI project and onboarding service.

The key is loaded via `loadOrGenerateOwnerKey()` in `did_minting_setup.go` with three modes:

1. **`import_key_file`** — Load a pre-existing PEM private key. Use this when you generate the key externally (e.g., via `openssl`) and want full control over key lifecycle.
2. **`first_time_init` + `key_export_path`** (default) — Generate on first run, save to the export path, load on subsequent starts. The simplest production setup.
3. **Ephemeral fallback** — Generate fresh each start. For development/testing only. A loud warning is logged.

The loaded key is shared with:

- `signingService.OwnerSigner` — for voucher extension signing
- `FDOKeyAuthServer.ServerKey` — for FDOKeyAuth challenge signing (same key, not a separate ephemeral one)
- `did.NewDocument()` — for the public key in the DID document

### ⚠️ Production Configuration

**Minimum viable production setup:**

```yaml
key_management:
  key_type: "ec384"
  first_time_init: true    # generate + persist on first run
did_minting:
  enabled: true
  key_export_path: "data/owner-key.pem"   # persisted here
```

This generates the key once and reuses it across restarts. The key file should be:

- **Backed up** — Loss of the key means loss of DID identity and all voucher trust relationships.
- **Access-restricted** — Only the service account should be able to read it (mode `0600`, set automatically).
- **Encrypted at rest** — Use filesystem encryption or a secrets manager.

Alternatively, generate the key externally and import it:

```yaml
key_management:
  import_key_file: "/secure/path/owner-key.pem"
```

### Future: External Key Management (HSM/TPM/KMS)

For high-security deployments, the private key should live in hardware where it can never be extracted. See [EXTERNAL_KEY_MANAGEMENT.md](EXTERNAL_KEY_MANAGEMENT.md) for the design covering:

- **External command callback** — Shell out to an HSM/KMS for each signing operation (Phase 2)
- **PKCS#11 / TPM** — Native hardware key store support (Phase 3)
- **Cloud KMS** — AWS KMS, Azure Key Vault, GCP Cloud KMS, HashiCorp Vault (Phase 4)

See also the [DID Key Minting section of go-fdo/PRODUCTION_CONSIDERATIONS.md](go-fdo/PRODUCTION_CONSIDERATIONS.md#did-key-minting--software-vs-hardware-keys) for library-level HSM/KMS guidance.

### Exported Key Files

If `did_minting.key_export_path` is set, the private key is written to disk as a PKCS8 PEM file (mode `0600`). In production:

- **Restrict access** — Only the service account should be able to read this file.
- **Encrypt at rest** — Use filesystem encryption or a secrets manager. A plaintext PEM on disk is a high-value target.
- **Avoid exporting** — If possible, use an HSM-backed `crypto.Signer` so the private key never touches the filesystem.

---

## TLS and Transport Security

### Current State

The application has a `use_tls` configuration flag but **does not actually implement TLS** — `runServer()` always calls `ListenAndServe()`, never `ListenAndServeTLS()`. TLS is assumed to be handled by a reverse proxy.

### Production Deployment

**Always terminate TLS in front of the voucher manager.** Options:

- **Reverse proxy** (nginx, Caddy, Envoy, HAProxy) — Recommended. Handles TLS termination, certificate renewal (Let's Encrypt), rate limiting, and request logging.
- **Kubernetes Ingress** — Standard approach for containerized deployments.
- **Cloud load balancer** — AWS ALB, GCP HTTPS LB, Azure Application Gateway.

**Important:** The DID document's service URLs (`voucher_recipient_url`, `voucher_holder_url`) should use `https://` in production. Partners resolving your DID will connect to these URLs.

### Outbound TLS

The push client (`VoucherPushClient`) uses Go's default `net/http` client, which validates TLS certificates by default. For push destinations with private CAs:

- Configure custom CA certificates in the system trust store
- Or implement a custom `http.Transport` with the appropriate `RootCAs`

The `insecure_tls` config flag is available for development but **must never be enabled in production**.

---

## Authentication and Secrets

### Bearer Tokens

Authentication tokens are stored in plaintext in the `voucher_receiver_tokens` database table. In production:

- **Use strong tokens** — Generate tokens with sufficient entropy (e.g., `openssl rand -hex 32`).
- **Rotate regularly** — Use token expiration (`-expires` flag) and rotate before expiry.
- **Don't use `global_token` in production** — It's a convenience for development. Use per-partner database tokens instead.
- **Audit token usage** — The `voucher_receiver_audit` table logs which token was used for each reception. Monitor this.

### Partner Auth Tokens

Partners configured with `auth_token` (for push authentication to downstream services) have their tokens stored in plaintext in the `partners` database table. In production:

- Store these tokens in a secrets manager (Vault, AWS Secrets Manager, etc.) and inject them at runtime
- Or encrypt the database at rest

### Pull Service Session Tokens

FDOKeyAuth session tokens are stored in-memory (`pullTokenStore`) and are lost on restart. This is acceptable — clients simply re-authenticate. However:

- **Token TTL** — Default is 1 hour. For high-security environments, reduce this.
- **Session TTL** — Default is 60 seconds for the FDOKeyAuth handshake. This is appropriate.
- **Max sessions** — Default is 1000 concurrent sessions. Size this based on expected concurrent pull clients.

---

## High Availability and Scaling

### Single-Instance Limitations

The current architecture is single-instance:

- SQLite database is file-local
- In-memory FDOKeyAuth session store is not shared
- In-memory pull token store is not shared
- Retry worker runs as a single goroutine
- DID key is generated per-instance

### Scaling Options

**Vertical scaling** (bigger instance) is the simplest path. The application is lightweight and a single instance can handle significant throughput.

**For horizontal scaling**, the following changes would be needed:

| Component | Current | Production Alternative |
|-----------|---------|----------------------|
| Database | SQLite (file-local) | PostgreSQL / MySQL (shared) |
| Session store | In-memory map | Redis / database-backed |
| Token store | In-memory map | Redis / database-backed |
| Retry worker | Single goroutine | Distributed job queue (e.g., database-based with row locking) |
| Owner key | Generated per-start | Shared key from KMS/HSM |
| Voucher files | Local filesystem | Shared filesystem (NFS/EFS) or object storage (S3) |

### Graceful Shutdown

The server handles `SIGINT`/`SIGTERM` with a 10-second shutdown timeout. In production:

- Ensure your orchestrator (systemd, Kubernetes) sends `SIGTERM` and waits at least 10 seconds before `SIGKILL`
- In-flight push transmissions may be interrupted — the retry worker will pick them up on the next cycle

---

## Monitoring and Observability

### Logging

The application uses Go's `slog` structured logger. Key events to monitor:

- **`voucher received`** — Successful voucher reception
- **`voucher transmission delivered`** / **`voucher transmission attempt failed`** — Push outcomes
- **`partner bootstrap`** — Partner enrollment on startup
- **`DID minting`** — DID document generation
- **`FDOKeyAuth`** — Authentication handshake events

Enable debug logging (`-debug` flag or `debug: true` in config) for troubleshooting, but disable it in production — it logs full DID documents and protocol details.

### Health Checking

The application does not currently expose a health endpoint. For production:

- Add an HTTP health check endpoint (e.g., `GET /healthz`) that verifies database connectivity
- Monitor the process with your orchestrator's health checking (systemd watchdog, Kubernetes liveness/readiness probes)

### Metrics to Track

- **Voucher reception rate** — Vouchers received per unit time
- **Transmission success/failure rate** — Push delivery outcomes
- **Retry queue depth** — Number of pending/failed transmissions (`vouchers list -status pending`)
- **FDOKeyAuth authentication rate** — Successful/failed pull authentications
- **Partner DID refresh failures** — Stale DID documents may indicate network issues
- **Database size** — SQLite file growth over time
- **Disk usage** — Voucher file directory growth

---

## Backup and Recovery

### What to Back Up

| Component | Location | Criticality |
|-----------|----------|-------------|
| Database | `database.path` (default: `voucher_manager.db`) | **Critical** — all state |
| Voucher files | `voucher_files.directory` (default: `data/vouchers/`) | **Critical** — source vouchers |
| Configuration | `config.yaml` | Important — reproducible |
| Owner private key | `did_minting.key_export_path` (if set) | **Critical** — identity |
| Partner key files | Referenced by `partners[].key_file` | Important — can be re-enrolled |

### Recovery Procedure

1. **Restore database** — Copy the backup `.db` file to the configured `database.path`
2. **Restore voucher files** — Copy the backup directory to `voucher_files.directory`
3. **Restore owner key** — If using `import_key_file` or `key_export_path`, ensure the key file is in place
4. **Start the server** — Partners bootstrapped from config will be re-enrolled (idempotent). Database-only partners will be restored from the backup.
5. **Verify** — Check `partners list`, `vouchers list`, `tokens list` to confirm state

### Disaster Recovery

- If the owner private key is lost and was not backed up, **all DID-based trust relationships must be re-established**. Partners must re-enroll your new DID.
- If voucher files are lost but the database is intact, transmission records will reference missing files. Mark these as failed and re-request vouchers from upstream.
- If the database is lost but voucher files are intact, you lose transmission state, tokens, and partners. Re-add tokens and partners; voucher files can be re-ingested if needed.

---

## Container Deployment

### Docker / OCI

```dockerfile
FROM golang:1.23 AS build
WORKDIR /src
COPY . .
RUN go build -o /fdo-voucher-manager

FROM gcr.io/distroless/base-debian12
COPY --from=build /fdo-voucher-manager /fdo-voucher-manager
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/fdo-voucher-manager", "server", "-config", "/data/config.yaml"]
```

**Volume mounts:**
- `/data/config.yaml` — Configuration file
- `/data/voucher_manager.db` — Database (must be on a persistent volume)
- `/data/vouchers/` — Voucher file storage (must be on a persistent volume)
- `/data/owner-key.pem` — Owner private key (if using `key_export_path`)

### Kubernetes

- Use a `PersistentVolumeClaim` for the database and voucher files
- Mount secrets (config, owner key) via `Secret` or external secrets operator
- Set resource limits — the application is lightweight (typically <100MB RAM, minimal CPU)
- Use a `Deployment` with `replicas: 1` (scaling requires database migration; see High Availability section)

---

## Security Checklist

Before deploying to production, verify:

- [ ] **TLS termination** configured in front of the service
- [ ] **Owner key** is persistent across restarts (not regenerated)
- [ ] **DID document URLs** use `https://`
- [ ] **`require_auth: true`** on the voucher receiver
- [ ] **`require_trusted_manufacturer: true`** if accepting vouchers from known partners only
- [ ] **`insecure_tls: false`** (or absent)
- [ ] **`global_token`** not used (use per-partner database tokens)
- [ ] **Database file** has restricted permissions (mode `0600`)
- [ ] **Voucher directory** has restricted permissions (mode `0700`)
- [ ] **Key export file** has restricted permissions (mode `0600`)
- [ ] **Backups** configured for database, voucher files, and owner key
- [ ] **Monitoring** in place for transmission failures and auth events
- [ ] **Token rotation** schedule established
- [ ] **DID cache** enabled if using partner DID resolution (`did_cache.enabled: true`)

---

## Related Documentation

- **[CONFIG_REFERENCE.md](CONFIG_REFERENCE.md)** — Complete configuration file reference
- **[CLI_REFERENCE.md](CLI_REFERENCE.md)** — Complete CLI command reference
- **[VOUCHER_SUPPLY_CHAIN.md](VOUCHER_SUPPLY_CHAIN.md)** — Supply chain architecture and scenarios
- **[go-fdo/PRODUCTION_CONSIDERATIONS.md](go-fdo/PRODUCTION_CONSIDERATIONS.md)** — Library-level security concerns (certificates, revocation, protocol security, RV service)
