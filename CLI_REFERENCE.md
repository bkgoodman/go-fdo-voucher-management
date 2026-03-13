# CLI Reference

Complete command reference for `fdo-voucher-manager`. For configuration file options, see [CONFIG_REFERENCE.md](CONFIG_REFERENCE.md).

## Global Usage

```
fdo-voucher-manager <subcommand> [options]
```

All subcommands that interact with the database accept `-config <path>` (default: `config.yaml`) to specify the configuration file.

## Subcommands

| Subcommand | Description |
|---|---|
| [`server`](#server) | Start the HTTP server |
| [`vouchers`](#vouchers) | Manage voucher transmission records |
| [`tokens`](#tokens) | Manage receiver authentication tokens |
| [`partners`](#partners) | Manage trusted partner identities |
| [`pull`](#pull) | Authenticate and download vouchers from a Holder |
| [`fdokeyauth`](#fdokeyauth) | FDOKeyAuth handshake only (authentication test) |
| [`generate`](#generate) | Generate test vouchers |
| [`keys`](#keys) | Inspect and export cryptographic keys |
| [`help`](#help) | Show usage summary |

---

## server

Start the HTTP server for receiving, signing, and transmitting vouchers.

```
fdo-voucher-manager server [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-debug` | bool | `false` | Enable debug-level logging |

The server starts all enabled subsystems based on the config file:
- **Voucher receiver** — HTTP endpoint accepting pushed vouchers
- **Pull service** — FDOKeyAuth + Pull API for authenticated voucher retrieval
- **DID minting** — Generates and serves a DID document at `.well-known/did.json`
- **Retry worker** — Background loop retrying failed transmissions
- **Partner DID refresh** — Background worker refreshing cached DID documents

### Example

```bash
./fdo-voucher-manager server -config production.yaml
./fdo-voucher-manager server -config config.yaml -debug
```

---

## vouchers

Manage voucher transmission records stored in the database.

```
fdo-voucher-manager vouchers <command> [options]
```

### vouchers list

List voucher transmission records.

```
fdo-voucher-manager vouchers list [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-status` | string | (all) | Filter by status: `pending`, `succeeded`, `failed`, `assigned` |
| `-guid` | string | (all) | Filter by voucher GUID |
| `-owner` | string | (all) | Filter by owner key fingerprint |
| `-serial` | string | (all) | Filter by serial number |
| `-limit` | int | `50` | Maximum number of results |

Output columns: GUID, Serial, Status, Assigned By, Destination, ID.

### vouchers show

Show detailed information about a specific voucher transmission record.

```
fdo-voucher-manager vouchers show -guid <guid> [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-guid` | string | **(required)** | Voucher GUID to inspect |

Displays: ID, GUID, Serial, Model, Status, Owner Key Fingerprint, Destination URL, Destination Source, Attempts, Last Error, File Path, Created/Updated timestamps, Last Attempt time, Delivered time, Assigned At, Assigned To (fingerprint), Assigned To DID, Assigned By (fingerprint).

### vouchers retry

Manually retry transmission of a specific voucher.

```
fdo-voucher-manager vouchers retry -guid <guid> [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-guid` | string | **(required)** | GUID of the voucher to retry |

### vouchers assign

Assign one or more vouchers to a new owner. This extends the voucher's cryptographic ownership chain to the specified customer key and marks the record as assigned.

```
fdo-voucher-manager vouchers assign [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-serial` | string | `""` | Serial number(s) to assign (comma-separated for batch) |
| `-guid` | string | `""` | Voucher GUID to assign (alternative to `-serial`) |
| `-new-owner-key` | string | `""` | PEM file with the new owner's public key |
| `-new-owner-did` | string | `""` | DID URI of the new owner (resolved automatically) |
| `-json` | bool | `false` | Output results as JSON |

One of `-serial` or `-guid` is required. One of `-new-owner-key` or `-new-owner-did` is required (mutually exclusive).

Assignment is **at-most-once**: a second assignment to an already-assigned voucher is rejected. A pre-assignment backup of the voucher file is saved automatically so that `unassign` can fully revert the operation.

### vouchers unassign

Revert one or more voucher assignments, restoring them to their pre-assignment state. This clears the database metadata, removes access grants, and restores the voucher file to its original (pre-extension) state.

```
fdo-voucher-manager vouchers unassign [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-serial` | string | `""` | Serial number(s) to unassign (comma-separated) |
| `-guid` | string | `""` | Voucher GUID to unassign (alternative to `-serial`) |
| `-json` | bool | `false` | Output results as JSON |

One of `-serial` or `-guid` is required. The voucher must currently be in `assigned` status.

### vouchers grants

List access grants. Access grants track which identities (owner keys, custodians, purchaser tokens) have access to which vouchers.

```
fdo-voucher-manager vouchers grants [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-guid` | string | (all) | Filter by voucher GUID |
| `-type` | string | (all) | Filter by identity type: `owner_key`, `custodian`, `purchaser_token` |
| `-limit` | int | `100` | Maximum number of results |

Output columns: Voucher GUID, Serial, Identity FP, Type, Access, Granted By.

### vouchers custodians

List custodians and their voucher assignments. Custodians are identities that have directed voucher assignments via the assign API.

```
fdo-voucher-manager vouchers custodians [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-fingerprint` | string | (all) | Show vouchers for a specific custodian fingerprint |
| `-limit` | int | `50` | Maximum number of results |

Without `-fingerprint`: lists all custodians with voucher counts and serial numbers.

With `-fingerprint`: lists all vouchers assigned by that custodian, showing GUID, serial, status, assigned-to fingerprint, and assignment time.

### Examples

```bash
# List all pending vouchers
./fdo-voucher-manager vouchers list -status pending

# List vouchers for a specific owner
./fdo-voucher-manager vouchers list -owner abc123def456...

# List vouchers by serial number
./fdo-voucher-manager vouchers list -serial SN-12345

# Show details for a specific voucher (includes assignment info)
./fdo-voucher-manager vouchers show -guid 550e8400-e29b-41d4-a716-446655440000

# Manually retry a failed transmission
./fdo-voucher-manager vouchers retry -guid 550e8400-e29b-41d4-a716-446655440000

# List all access grants
./fdo-voucher-manager vouchers grants

# List only custodian grants
./fdo-voucher-manager vouchers grants -type custodian

# List grants for a specific voucher
./fdo-voucher-manager vouchers grants -guid 550e8400-e29b-41d4-a716-446655440000

# List all custodians with voucher counts
./fdo-voucher-manager vouchers custodians

# Show vouchers assigned by a specific custodian
./fdo-voucher-manager vouchers custodians -fingerprint abc123def456...

# Assign a voucher to a customer's public key
./fdo-voucher-manager vouchers assign -serial SN-12345 -new-owner-key customer-pub.pem

# Assign using a DID URI
./fdo-voucher-manager vouchers assign -serial SN-12345 -new-owner-did did:web:customer.example.com

# Batch assign
./fdo-voucher-manager vouchers assign -serial "SN-001,SN-002,SN-003" -new-owner-key customer-pub.pem

# Revert an assignment
./fdo-voucher-manager vouchers unassign -serial SN-12345

# Assign by GUID instead of serial
./fdo-voucher-manager vouchers assign -guid 550e8400-e29b-41d4-a716-446655440000 \
    -new-owner-key customer-pub.pem
```

---

## tokens

Manage bearer authentication tokens used by the voucher receiver endpoint. These tokens are stored in the database and checked when `voucher_receiver.require_auth` is `true`.

```
fdo-voucher-manager tokens <command> [options]
```

### tokens add

Add a new authentication token.

```
fdo-voucher-manager tokens add -token <value> [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-token` | string | **(required)** | Token value |
| `-description` | string | `""` | Human-readable description |
| `-expires` | int | `0` | Expiration in hours from now (`0` = never expires) |

### tokens list

List all tokens.

```
fdo-voucher-manager tokens list [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |

Output columns: Token (truncated), Description, Owner Key FP, Created, Expires.

### tokens delete

Delete a token.

```
fdo-voucher-manager tokens delete -token <value> [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-token` | string | **(required)** | Token value to delete |

### Examples

```bash
# Add a token valid for 24 hours
./fdo-voucher-manager tokens add -token "my-secret-token-123" -description "Factory A" -expires 24

# Add a non-expiring token
./fdo-voucher-manager tokens add -token "permanent-token" -description "Trusted service"

# List all tokens
./fdo-voucher-manager tokens list

# Delete a token
./fdo-voucher-manager tokens delete -token "my-secret-token-123"
```

---

## partners

Manage trusted partner identities in the partner trust store. Partners represent other organizations or services in the voucher supply chain. Each partner has capability flags controlling what operations are authorized.

```
fdo-voucher-manager partners <command> [options]
```

### Capability Flags

- **`-supply`** — Partner can supply vouchers **to us** (upstream supplier). Their manufacturer key is trusted for voucher signature verification when `require_trusted_manufacturer` is enabled.
- **`-receive`** — We can push vouchers **to this partner** (downstream recipient). Used by the destination resolver to route vouchers.

At least one of `-supply` or `-receive` is required when adding a partner.

### partners add

Add a trusted partner identity.

```
fdo-voucher-manager partners add -id <name> [-supply] [-receive] [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-id` | string | **(required)** | Human-readable partner identifier (e.g., `acme-mfg`) |
| `-supply` | bool | `false` | Partner can supply vouchers to us |
| `-receive` | bool | `false` | We push vouchers to this partner |
| `-did` | string | `""` | DID URI (`did:web:...` or `did:key:...`). If provided, the DID is resolved to extract the public key and push URL. |
| `-key` | string | `""` | Path to PEM-encoded public key file |
| `-push-url` | string | `""` | FDOVoucherRecipient push URL |
| `-pull-url` | string | `""` | FDOVoucherHolder pull URL |
| `-auth-token` | string | `""` | Bearer token for authenticating push requests to this partner |
| `-disabled` | bool | `false` | Add partner in disabled state |
| `-json` | bool | `false` | Output as JSON |

At least one of `-did`, `-key`, or `-push-url` is required.

When `-did` is provided without `-key`, the DID is resolved and the public key is extracted automatically. If resolution fails, the partner is added without a key (with a warning). If `-push-url` is not set and the DID document contains an `FDOVoucherRecipient` service entry, the URL is extracted automatically.

### partners list

List all trusted partners.

```
fdo-voucher-manager partners list [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-filter` | string | (all) | Filter by capability: `supply` or `receive` |
| `-json` | bool | `false` | Output as JSON |

Output columns: ID, Supply, Receive, Enabled, Push URL, DID.

### partners show

Show detailed information about a partner.

```
fdo-voucher-manager partners show -id <name> [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-id` | string | **(required)** | Partner ID |
| `-json` | bool | `false` | Output as JSON |

### partners remove

Remove a partner from the trust store.

```
fdo-voucher-manager partners remove -id <name> [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-id` | string | **(required)** | Partner ID to remove |

### partners export

Export all partners as JSON to stdout.

```
fdo-voucher-manager partners export [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |

### Examples

```bash
# Add a manufacturer (upstream supplier) by DID — resolves key and URL automatically
./fdo-voucher-manager partners add -id acme-mfg -supply \
    -did "did:web:mfg.acme.com:vouchers"

# Add a customer (downstream recipient) by public key file
./fdo-voucher-manager partners add -id customer-a -receive \
    -key customer-a-pub.pem \
    -push-url "https://customer-a.example.com/api/v1/vouchers" \
    -auth-token "secret-push-token"

# Add a bidirectional partner (reseller)
./fdo-voucher-manager partners add -id reseller-b -supply -receive \
    -did "did:web:reseller-b.com:fdo"

# List only suppliers
./fdo-voucher-manager partners list -filter supply

# Show partner details as JSON
./fdo-voucher-manager partners show -id acme-mfg -json

# Remove a partner
./fdo-voucher-manager partners remove -id old-supplier

# Export all partners as JSON (for backup or migration)
./fdo-voucher-manager partners export > partners-backup.json
```

---

## pull

Authenticate with a Holder via the FDOKeyAuth protocol, then list and optionally download vouchers. This is the client-side command for the Pull transfer model.

```
fdo-voucher-manager pull [options]
```

### Authentication Flags

Two authentication modes are supported:

**Standard (owner private key):**

| Flag | Type | Default | Description |
|---|---|---|---|
| `-key` | string | `""` | Path to PEM-encoded owner private key file |
| `-key-type` | string | `ec384` | Key type to generate if `-key` not provided: `ec256`, `ec384`, `rsa2048` |

If `-key` is omitted, an ephemeral key is generated (useful for testing).

**Delegate-based (cross-org or intra-org):**

| Flag | Type | Default | Description |
|---|---|---|---|
| `-owner-pub` | string | `""` | PEM-encoded owner public key file (delegate mode) |
| `-delegate-key` | string | `""` | PEM-encoded delegate private key file |
| `-delegate-chain` | string | `""` | PEM-encoded delegate certificate chain file |

When using delegate mode, either `-owner-pub` or `-key` must identify the owner. `-delegate-key` and `-delegate-chain` must both be provided.

### Connection and Filter Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `-url` | string | **(required)** | Holder base URL (e.g., `http://localhost:8083`) |
| `-since` | string | `""` | Only return vouchers created after this time (RFC 3339) |
| `-until` | string | `""` | Only return vouchers created before this time (RFC 3339) |
| `-continuation` | string | `""` | Continuation token from a previous pull response (for pagination) |
| `-limit` | int | `0` | Max vouchers per page (`0` = server default) |

### Output Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `-output` | string | `""` | Directory to save downloaded `.fdoov` files. If omitted, lists metadata only. |
| `-list` | bool | `false` | List vouchers only, do not download (even if `-output` is set) |
| `-json` | bool | `false` | Output as JSON |
| `-holder-key` | string | `""` | PEM-encoded Holder public key file for verifying the Holder's signature during FDOKeyAuth |

### Examples

```bash
# Pull with owner key — list and download all vouchers
./fdo-voucher-manager pull -url http://holder:8083 \
    -key owner-private.pem -output ./vouchers/

# Pull with owner key — list only (no download)
./fdo-voucher-manager pull -url http://holder:8083 \
    -key owner-private.pem -list

# Pull with time filter
./fdo-voucher-manager pull -url http://holder:8083 \
    -key owner-private.pem \
    -since "2026-01-01T00:00:00Z" \
    -output ./vouchers/

# Paginated pull
./fdo-voucher-manager pull -url http://holder:8083 \
    -key owner-private.pem -limit 10
# Then use the continuation token from the output:
./fdo-voucher-manager pull -url http://holder:8083 \
    -key owner-private.pem -limit 10 \
    -continuation "2026-02-15T10:30:00Z"

# Delegate-based pull (cross-organization)
./fdo-voucher-manager pull -url http://holder:8083 \
    -owner-pub owner-public.pem \
    -delegate-key delegate-private.pem \
    -delegate-chain delegate-chain.pem \
    -output ./vouchers/

# Pull with Holder signature verification
./fdo-voucher-manager pull -url http://holder:8083 \
    -key owner-private.pem \
    -holder-key holder-public.pem \
    -output ./vouchers/
```

---

## fdokeyauth

Perform a FDOKeyAuth handshake only, without listing or downloading vouchers. Useful for testing authentication or obtaining a session token for use with other tools.

```
fdo-voucher-manager fdokeyauth [options]
```

Accepts the same authentication flags as [`pull`](#pull):

| Flag | Type | Default | Description |
|---|---|---|---|
| `-url` | string | **(required)** | Holder base URL |
| `-key` | string | `""` | Owner private key PEM file |
| `-key-type` | string | `ec384` | Key type for ephemeral key generation |
| `-owner-pub` | string | `""` | Owner public key PEM file (delegate mode) |
| `-delegate-key` | string | `""` | Delegate private key PEM file |
| `-delegate-chain` | string | `""` | Delegate certificate chain PEM file |
| `-holder-key` | string | `""` | Holder public key PEM file (for signature verification) |
| `-json` | bool | `false` | Output as JSON |

Output includes: Session Token, Expiration, Owner Key Fingerprint, Voucher Count.

### Examples

```bash
# Test authentication with owner key
./fdo-voucher-manager fdokeyauth -url http://holder:8083 -key owner.pem

# Test delegate authentication, JSON output
./fdo-voucher-manager fdokeyauth -url http://holder:8083 \
    -owner-pub owner-pub.pem \
    -delegate-key delegate.pem \
    -delegate-chain chain.pem \
    -json
```

---

## generate

Generate test vouchers for development and testing.

```
fdo-voucher-manager generate <command> [options]
```

### generate voucher

Create a minimal test voucher in PEM format.

```
fdo-voucher-manager generate voucher [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-serial` | string | `TEST-SERIAL` | Device serial number |
| `-model` | string | `TEST-MODEL` | Device model number |
| `-output` | string | (stdout) | Output file path. If omitted, the PEM voucher is printed to stdout. |
| `-owner-key` | string | `""` | Path to PEM-encoded owner public key file. If provided, the voucher is bound to this key. |

The generated voucher uses minimal valid CBOR structure with:
- Random 16-byte GUID
- `-----BEGIN OWNERSHIP VOUCHER-----` / `-----END OWNERSHIP VOUCHER-----` PEM wrapping
- Zero HMAC and empty entry chain (test-only, not valid for production)

### Examples

```bash
# Generate a test voucher to stdout
./fdo-voucher-manager generate voucher

# Generate with metadata and save to file
./fdo-voucher-manager generate voucher -serial SN-12345 -model IoT-Sensor-v2 \
    -output test-voucher.fdoov

# Generate voucher bound to a specific owner key
./fdo-voucher-manager generate voucher -serial SN-12345 \
    -owner-key customer-pub.pem -output test-voucher.fdoov
```

---

## keys

Inspect and export cryptographic key information.

```
fdo-voucher-manager keys <command> [options]
```

### keys show

Display key management configuration.

```
fdo-voucher-manager keys show [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |

Displays: Key Type, First Time Init flag, Database Path.

### keys export

Export the owner public key to a PEM file.

```
fdo-voucher-manager keys export -output <path> [options]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | string | `config.yaml` | Path to configuration file |
| `-output` | string | **(required)** | Output file path for the PEM-encoded public key |

> **Note:** This command currently writes a placeholder key. See TODO.md for the tracked issue to export the real owner key from the database.

### Examples

```bash
# Show key configuration
./fdo-voucher-manager keys show

# Export owner public key
./fdo-voucher-manager keys export -output owner-pub.pem
```

---

## help

Show the built-in usage summary.

```
fdo-voucher-manager help
```

---

## Common Patterns

### Push Flow Setup

```bash
# 1. Start server with push enabled
./fdo-voucher-manager server -config push-config.yaml

# 2. Add downstream partner
./fdo-voucher-manager partners add -id customer-a -receive \
    -did "did:web:customer.example.com:fdo"

# 3. Monitor transmissions
./fdo-voucher-manager vouchers list -status pending
./fdo-voucher-manager vouchers list -status failed
```

### Pull Flow Setup

```bash
# 1. Start server with pull_service enabled
./fdo-voucher-manager server -config holder-config.yaml

# 2. From the recipient side, authenticate and pull
./fdo-voucher-manager pull -url http://holder:8083 \
    -key owner-private.pem -output ./received-vouchers/
```

### Token-Based Authentication Setup

```bash
# 1. Add tokens for known suppliers
./fdo-voucher-manager tokens add -token "factory-a-token" \
    -description "Factory A" -expires 720

# 2. Start server with require_auth: true
./fdo-voucher-manager server -config config.yaml
```

### Partner Trust Management

```bash
# Add upstream supplier whose voucher signatures we trust
./fdo-voucher-manager partners add -id factory-a -supply \
    -did "did:key:zDn..."

# Add downstream customer we push vouchers to
./fdo-voucher-manager partners add -id customer-b -receive \
    -push-url "https://customer-b.example.com/api/v1/vouchers" \
    -key customer-b-pub.pem

# Verify trust store
./fdo-voucher-manager partners list
./fdo-voucher-manager partners show -id factory-a -json

# Backup/migrate trust store
./fdo-voucher-manager partners export > partners.json
```
