# FDO Voucher Manager

An intermediary voucher management service for FIDO Device Onboard (FDO) that receives ownership vouchers via the push protocol, stores them, signs them over to a new owner, and transmits them downstream.

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
- **retry_worker**: Background retry loop configuration

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

#### List Vouchers

```bash
./fdo-voucher-manager vouchers list -config config.yaml
./fdo-voucher-manager vouchers list -status pending -limit 100
./fdo-voucher-manager vouchers list -guid <guid>
```

#### Show Voucher Details

```bash
./fdo-voucher-manager vouchers show -guid <guid> -config config.yaml
```

#### Retry Transmission

```bash
./fdo-voucher-manager vouchers retry -guid <guid> -config config.yaml
```

#### Manage Authentication Tokens

```bash
# Add token
./fdo-voucher-manager tokens add -token <token> -description "My token" -expires 24

# List tokens
./fdo-voucher-manager tokens list -config config.yaml

# Delete token
./fdo-voucher-manager tokens delete -token <token> -config config.yaml
```

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

## License

SPDX-License-Identifier: Apache 2.0
