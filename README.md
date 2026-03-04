# FDO Voucher Manager

A voucher management service for FIDO Device Onboard (FDO) supply chains.

## Overview

In FDO, an **ownership voucher** is a cryptographic document created at a manufacturing station that binds a device to its owner. In the simplest case, a factory signs the voucher directly to an end customer's onboarding service and pushes it there. But real-world supply chains are rarely that simple.

Devices often pass through multiple organizations before reaching their final operator. A factory may be one plant among many within a larger manufacturer. Devices may be built to stock, with no known customer at manufacturing time. OEMs sell through distributors and resellers. Large customers receive devices from multiple suppliers and operate onboarding services across many sites. At every step, vouchers must be received, stored, signed over to the next owner's key, and forwarded downstream.

This project implements the **voucher service** role in that chain: a general-purpose intermediary that sits between voucher sources (factories, upstream suppliers) and voucher destinations (customers, downstream resellers, onboarding services). The same service can act as a factory aggregator collecting vouchers from multiple manufacturing stations, an OEM portal signing over to customer keys, a reseller forwarding to buyers, or a customer hub distributing across internal infrastructure.

```text
Factory ──▶ Voucher Service ──▶ Voucher Service ──▶ ... ──▶ Onboarding Service
```

The critical design principle is that **the APIs for sending and receiving vouchers are the same at every hop**. Whether a voucher service is talking to a factory, another reseller, or the final customer, the protocol is uniform. Voucher services may also be operated by third parties or offered as cloud SaaS products; the interfaces are the same regardless of who runs the infrastructure.

For a detailed discussion of the supply chain scenarios, terminology (factory vs. manufacturer, build-to-stock vs. build-to-order), and architectural patterns that motivate this project, see **[VOUCHER_SUPPLY_CHAIN.md](VOUCHER_SUPPLY_CHAIN.md)**.

## Features

- **Voucher Reception**: HTTP server implementing the FDO voucher push protocol (multipart/form-data)
- **Voucher Storage**: Persistent storage in SQLite database and filesystem (GUID-based .fdoov files)
- **Voucher Signing**: Sign vouchers over to a new owner using internal key extension
- **Voucher Transmission**: Push signed vouchers to downstream endpoints with retry logic
- **Background Retry Worker**: Automatic retry of failed transmissions with configurable intervals
- **CLI Commands**: Inspect voucher state and manage authentication tokens
- **Callback Support**: External command callbacks for:
  - OVEExtra data assignment
  - Next owner key resolution (static PEM or dynamic)
  - Transmission destination resolution
- **DID Support**: Resolve did:web URIs for owner keys and transmission endpoints
- **Authentication**: Bearer token authentication with global token and per-token database support

## Building

```bash
go build -o fdo-voucher-manager
```

## Configuration

Copy `config.yaml` and customize for your environment:

```bash
cp config.yaml my-config.yaml
```

Key configuration sections:

- **server**: HTTP server address and TLS settings
- **database**: SQLite database path
- **key_management**: Cryptographic key type and initialization
- **voucher_receiver**: Inbound push protocol endpoint and authentication
- **voucher_signing**: Signing mode (internal/external/hsm)
- **owner_signover**: Next owner key resolution (static/dynamic)
- **push_service**: Outbound transmission endpoint and retry settings
- **pull_service**: FDOKeyAuth protocol and pull API settings
- **did_minting**: DID document generation and serving
- **partners**: Trusted partner bootstrap entries
- **retry_worker**: Background retry loop configuration

For a complete reference of every configuration field, type, default value, and usage notes, see **[CONFIG_REFERENCE.md](CONFIG_REFERENCE.md)**.

## Running

### Server Mode

Start the HTTP server:

```bash
./fdo-voucher-manager server -config config.yaml
```

The server will:
1. Listen on the configured address (default: localhost:8080)
2. Accept vouchers at the configured endpoint (default: /api/v1/vouchers)
3. Store vouchers to database and filesystem
4. Sign vouchers over to next owner (if configured)
5. Queue for transmission (if destination available)
6. Run background retry worker for failed transmissions

### CLI Commands

```
fdo-voucher-manager <subcommand> [options]
```

| Subcommand | Description |
|---|---|
| `server` | Start the HTTP server |
| `vouchers` | Manage voucher transmission records (list, show, retry) |
| `tokens` | Manage receiver authentication tokens (add, list, delete) |
| `partners` | Manage trusted partner identities (add, list, show, remove, export) |
| `pull` | Authenticate and download vouchers from a Holder |
| `fdokeyauth` | FDOKeyAuth handshake only (authentication test) |
| `generate` | Generate test vouchers |
| `keys` | Inspect and export cryptographic keys |
| `help` | Show usage summary |

#### Quick Examples

```bash
# Start server
./fdo-voucher-manager server -config config.yaml

# List pending vouchers
./fdo-voucher-manager vouchers list -status pending

# Pull vouchers from a Holder
./fdo-voucher-manager pull -url http://holder:8083 -key owner.pem -output ./vouchers/

# Add a trusted upstream supplier
./fdo-voucher-manager partners add -id acme-mfg -supply -did "did:web:mfg.acme.com:vouchers"

# Generate a test voucher
./fdo-voucher-manager generate voucher -serial SN-123 -output test.fdoov
```

For the complete command reference with all flags and options, see **[CLI_REFERENCE.md](CLI_REFERENCE.md)**.

**Delegate-based pull** allows entities to pull vouchers without the owner's private key, using a delegate certificate with `voucher-claim` permission. See **[VOUCHER_SUPPLY_CHAIN.md](VOUCHER_SUPPLY_CHAIN.md#intra-organization-distribution-delegate-pull)** for details.

## Voucher Pipeline

When a voucher is received:

1. **Validation**: Parse and validate voucher format
2. **Storage**: Save to filesystem and database
3. **OVEExtra Data** (if configured): Call external callback to get extra data
4. **Owner Key Resolution** (if configured):
   - Static mode: Use configured PEM public key
   - Dynamic mode: Call external callback to get owner key per device
5. **Voucher Signing** (if configured): Sign voucher over to next owner
6. **Destination Resolution** (if configured):
   - Callback mode: Call external callback to get transmission endpoint
   - DID mode: Resolve did:web to get voucherRecipientURL
   - Static mode: Use configured endpoint
7. **Transmission Queuing**: Create transmission record with destination
8. **Background Retry**: Retry worker attempts delivery with exponential backoff

## Transmission States

- **pending**: Awaiting transmission or retry
- **succeeded**: Successfully delivered
- **failed**: Exceeded max attempts
- **no_destination**: Stored but no transmission endpoint available

## External Callbacks

Callbacks are shell commands with variable substitution:

### OVEExtra Data Callback

```bash
ove_extra_data:
  enabled: true
  external_command: "/path/to/script.sh {serialno} {model}"
  timeout: 10s
```

Expected output: JSON map of extra data

### Owner Key Callback

```bash
owner_signover:
  mode: dynamic
  external_command: "/path/to/script.sh {serialno} {model}"
  timeout: 10s
```

Expected output: JSON with either `owner_key_pem` (PEM-encoded public key) or `owner_did` (DID URI)

### Destination Callback

```bash
destination_callback:
  enabled: true
  external_command: "/path/to/script.sh {serialno} {model} {guid}"
  timeout: 10s
```

Expected output: HTTP URL for voucher transmission

## Database Schema

### voucher_transmissions

Tracks voucher transmission attempts:

- `id`: Primary key
- `voucher_guid`: Device GUID
- `file_path`: Path to voucher file
- `destination_url`: Target endpoint
- `auth_token`: Bearer token for authentication
- `destination_source`: Origin of destination (callback/did/static)
- `status`: pending/succeeded/failed
- `attempts`: Number of transmission attempts
- `serial_number`, `model_number`: Device metadata
- `created_at`, `updated_at`: Timestamps
- `retry_after`: Next retry time

### voucher_receiver_tokens

Authentication tokens for voucher reception:

- `token`: Bearer token value
- `description`: Token description
- `expires_at`: Expiration timestamp (NULL = never)
- `created_at`: Creation timestamp

### voucher_receiver_audit

Audit log of received vouchers:

- `guid`: Device GUID
- `serial`, `model`, `manufacturer`: Device metadata
- `source_ip`: Source IP address
- `token_used`: Token used for authentication
- `received_at`: Reception timestamp
- `file_size`: Voucher file size

## Troubleshooting

### Vouchers not being transmitted

1. Check transmission status: `vouchers list -status pending`
2. Check destination resolution: `vouchers show -guid <guid>`
3. Check retry worker is enabled: `retry_worker.enabled: true` in config
4. Check logs for callback errors

### Authentication failures

1. Verify token is valid: `tokens list`
2. Check token expiration
3. Verify `require_auth` setting in config
4. Check Authorization header format: `Bearer <token>`

### Voucher signing failures

1. Verify `owner_signover.mode` is configured
2. Check owner key callback output format
3. Verify next owner key is valid PEM or DID

## Architecture

```
HTTP Request
    ↓
VoucherReceiverHandler (authentication, parsing, storage)
    ↓
VoucherPipeline (orchestration)
    ├→ OVEExtraDataService (callback)
    ├→ OwnerKeyService (callback or static)
    ├→ VoucherSigningService (sign-over)
    ├→ VoucherDestinationResolver (callback, DID, or static)
    └→ VoucherTransmissionStore (database persistence)
    ↓
VoucherRetryWorker (background loop)
    ↓
VoucherPushService (transmission orchestration)
    ↓
VoucherPushClient (HTTP multipart upload)
    ↓
Downstream Endpoint
```

## Production Deployment

For guidance on deploying this service in production — including database choices, persistent storage, key management, TLS, backup/recovery, high availability, monitoring, and a security checklist — see **[PRODUCTION_CONSIDERATIONS.md](PRODUCTION_CONSIDERATIONS.md)**.

For library-level security concerns (certificate validation, revocation checking, protocol security), see **[go-fdo/PRODUCTION_CONSIDERATIONS.md](go-fdo/PRODUCTION_CONSIDERATIONS.md)**.

## License

SPDX-License-Identifier: Apache 2.0
