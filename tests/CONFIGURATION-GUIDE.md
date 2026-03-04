# Configuration Guide for E2E DID Push-Pull Test

This guide explains the configuration choices in the end-to-end test and how they relate to real-world supply chain scenarios.

## Configuration Files Overview

The test uses two configuration files:

- **config-e2e-first.yaml** - Manufacturer's voucher service (port 8083)
- **config-e2e-second.yaml** - Customer's voucher service (port 8084)

Each configuration represents a different role in the supply chain with specific capabilities enabled or disabled.

## Manufacturer Configuration (First Instance)

```yaml
# config-e2e-first.yaml - Manufacturer Voucher Service
debug: true

server:
  addr: localhost:8083
  use_tls: false

database:
  path: tests/data/e2e-first.db

key_management:
  key_type: ec384
  first_time_init: true

voucher_receiver:
  enabled: true
  endpoint: /api/v1/vouchers
  require_auth: false
  validate_ownership: false

voucher_signing:
  mode: internal

owner_signover:
  mode: static
  static_did: ""  # Dynamically set to customer's DID
  static_public_key: ""

voucher_files:
  directory: tests/data/vouchers-e2e-first

push_service:
  enabled: false
  url: ""
  mode: push
  retry_interval: 2s
  max_attempts: 3

did_push:
  enabled: true

did_cache:
  enabled: false

pull_service:
  enabled: true
  session_ttl: 60s
  max_sessions: 100
  token_ttl: 1h

did_minting:
  enabled: true
  host: "localhost:8083"
  voucher_recipient_url: "http://localhost:8083/api/v1/vouchers"
  serve_did_document: true
  export_did_uri: true

retry_worker:
  enabled: true
  retry_interval: 2s
  max_attempts: 3

retention:
  keep_indefinitely: true
```

### Manufacturer Configuration Explained

#### Manufacturer Core Services

- **voucher_receiver**: `enabled: true` - Accepts vouchers from factories
- **voucher_signing**: `mode: internal` - Can sign vouchers over to new owners
- **did_minting**: `enabled: true` - Creates and serves DID document

#### Manufacturer DID-Based Transfer

- **did_push**: `enabled: true` - Can resolve DIDs and push vouchers
- **owner_signover**: `mode: static` - Uses customer's DID for automatic sign-over
- **static_did**: Set dynamically to customer's DID URI

#### Pull Authentication

- **pull_service**: `enabled: true` - Allows customers to authenticate and pull vouchers
- **session_ttl**: 60s - Authentication sessions expire after 1 minute
- **token_ttl**: 1h - Pull tokens valid for 1 hour

#### Reliability

- **retry_worker**: `enabled: true` - Automatically retries failed transmissions
- **retry_interval**: 2s - Retry every 2 seconds
- **max_attempts**: 3 - Give up after 3 attempts

#### Manufacturer Business Rationale

The manufacturer needs to:

1. **Receive vouchers** from multiple factories
2. **Sign over** vouchers to customer keys
3. **Push automatically** using DID discovery
4. **Allow pull access** for customer-controlled retrieval
5. **Retry failures** to ensure reliable delivery

## Customer Configuration (Second Instance)

```yaml
# config-e2e-second.yaml - Customer Voucher Service
debug: true

server:
  addr: localhost:8084
  use_tls: false

database:
  path: tests/data/e2e-second.db

key_management:
  key_type: ec384
  first_time_init: true

voucher_receiver:
  enabled: true
  endpoint: /api/v1/vouchers
  require_auth: false
  validate_ownership: false

voucher_signing:
  mode: internal

owner_signover:
  mode: static
  static_did: ""
  static_public_key: ""

voucher_files:
  directory: tests/data/vouchers-e2e-second

push_service:
  enabled: false

did_push:
  enabled: false

did_cache:
  enabled: false

pull_service:
  enabled: false

did_minting:
  enabled: true
  host: "localhost:8084"
  voucher_recipient_url: "http://localhost:8084/api/v1/vouchers"
  serve_did_document: true
  export_did_uri: true

retry_worker:
  enabled: false

retention:
  keep_indefinitely: true
```

### Customer Configuration Explained

#### Customer Core Services

- **voucher_receiver**: `enabled: true` - Accepts vouchers from suppliers
- **voucher_signing**: `mode: internal` - Can sign vouchers (for downstream transfer)
- **did_minting**: `enabled: true` - Creates and serves DID document

#### Customer Disabled Features

- **did_push**: `enabled: false` - Doesn't push to downstream (end customer)
- **pull_service**: `enabled: false` - Doesn't allow others to pull from here
- **push_service**: `enabled: false` - No static push configuration
- **retry_worker**: `enabled: false` - No retry needed (receiver only)

#### Customer Business Rationale

The customer needs to:

1. **Receive vouchers** from suppliers
2. **Serve DID document** for discovery by suppliers
3. **Store vouchers** for device onboarding
4. **Not push further** (end of supply chain)
5. **Not allow pull** (security - only receives)

## Key Configuration Differences

| Feature | Manufacturer (First) | Customer (Second) | Why Different |
| --------- | --------------------- | ------------------- | ------------- |
| **did_push** | enabled | disabled | Manufacturer pushes to customer |
| **pull_service** | enabled | disabled | Manufacturer allows customer pull |
| **retry_worker** | enabled | disabled | Manufacturer retries failed pushes |
| **static_did** | Customer's DID | empty | Manufacturer targets customer |
| **Port** | 8083 | 8084 | Network isolation |

## Supply Chain Role Mapping

### Manufacturer Role

```text
┌─────────────────────────────────────────────────────────────┐
│                    MANUFACTURER                              │
│                                                             │
│  IN: Vouchers from factories                                │
│  OUT: Signed-over vouchers to customers                     │
│                                                             │
│  Capabilities:                                              │
│  - Aggregate vouchers from multiple sources                │
│  - Sign over to customer keys                               │
│  - Push automatically via DID discovery                    │
│  - Allow authenticated pull access                          │
│  - Retry failed transmissions                               │
└─────────────────────────────────────────────────────────────┘
```

### Customer Role

```text
┌─────────────────────────────────────────────────────────────┐
│                      CUSTOMER                               │
│                                                             │
│  IN: Vouchers from suppliers                                │
│  OUT: Device onboarding (no voucher forwarding)             │
│                                                             │
│  Capabilities:                                              │
│  - Receive vouchers from multiple suppliers                 │
│  - Serve DID for supplier discovery                         │
│  - Store vouchers for device onboarding                     │
│  - End of supply chain (no forwarding)                     │
└─────────────────────────────────────────────────────────────┘
```

## Configuration for Different Supply Chain Roles

### Factory Aggregator

```yaml
# Receives from multiple factories, forwards to OEM
voucher_receiver:
  enabled: true
  require_auth: true  # Factory authentication

did_push:
  enabled: true
  static_did: "did:web:oem.com:vouchers"

pull_service:
  enabled: false  # Factories push, don't pull
```

### OEM Voucher Portal

```yaml
# Receives from factories, serves customers
did_push:
  enabled: true  # Push to customers

pull_service:
  enabled: true  # Allow customer pull

retry_worker:
  enabled: true  # Ensure reliable delivery
```

### Reseller Service

```yaml
# Middle of supply chain
did_push:
  enabled: true  # Push to downstream

pull_service:
  enabled: true  # Allow upstream pull

owner_signover:
  mode: callback  # Dynamic destination resolution
```

### Customer Hub

```yaml
# End customer, multiple suppliers
did_push:
  enabled: false  # No downstream

pull_service:
  enabled: false  # Security - receive only

voucher_receiver:
  validate_ownership: true  # Strict validation
```

## Security Considerations

### Authentication Settings

```yaml
# For receiving from trusted parties
voucher_receiver:
  require_auth: true
  validate_ownership: true

# For public receiving (test/demo)
voucher_receiver:
  require_auth: false
  validate_ownership: false
```

### DID Resolution Security

```yaml
# Cache DIDs to prevent resolution attacks
did_cache:
  enabled: true
  ttl: 1h
  max_size: 1000

# Disable caching for development
did_cache:
  enabled: false
```

### FDOKeyAuth Security

```yaml
# Strict session management
pull_service:
  session_ttl: 30s      # Short sessions
  token_ttl: 15m        # Limited token lifetime
  max_sessions: 10      # Limit concurrent sessions
```

## Performance Tuning

### High-Throughput Manufacturer

```yaml
retry_worker:
  retry_interval: 500ms  # Fast retries
  max_attempts: 5        # More attempts
  worker_count: 10       # Parallel workers

pull_service:
  max_sessions: 1000    # High concurrency
```

### Resource-Constrained Customer

```yaml
voucher_receiver:
  max_concurrent: 10     # Limit concurrent processing

database:
  connection_pool: 5      # Smaller pool
```

## Troubleshooting Configuration Issues

### DID Resolution Failures

```yaml
# Enable DID debugging
debug: true

# Check DID document accessibility
curl -s http://localhost:8084/.well-known/did.json

# Verify DID format
did:web:localhost:8084  # Correct
did:web:localhost:8084/  # Incorrect - trailing slash
```

### Push Transmission Failures

```yaml
# Enable retry debugging
retry_worker:
  debug: true

# Check endpoint reachability
curl -s http://localhost:8084/api/v1/vouchers

# Verify static_did configuration
grep static_did config-e2e-first-live.yaml
```

### FDOKeyAuth Authentication Issues

```yaml
# Check pull service status
curl -s http://localhost:8083/api/v1/pull/status

# Verify key compatibility
key_type: ec384  # Both instances must match
```

## Configuration Validation

### Pre-Flight Checks

```bash
# Validate DID document
curl -s http://localhost:8083/.well-known/did.json | jq .

# Check service endpoints
curl -s http://localhost:8083/api/v1/health
curl -s http://localhost:8084/api/v1/health

# Verify key generation
ls -la tests/data/e2e-first.db
ls -la tests/data/e2e-second.db
```

### Runtime Monitoring

```bash
# Monitor voucher reception
tail -f tests/data/first.log | grep "voucher received"

# Monitor DID resolution
tail -f tests/data/first.log | grep "resolved.*DID"

# Monitor push attempts
tail -f tests/data/first.log | grep "push.*voucher"
```

## Best Practices

### Production Configuration

1. **Enable TLS** for all network communications
2. **Require authentication** for voucher reception
3. **Enable DID caching** for performance and security
4. **Configure retry logic** for reliability
5. **Set appropriate TTLs** for sessions and tokens
6. **Monitor logs** for security and performance

### Development Configuration

1. **Disable TLS** for local testing
2. **Disable authentication** for easier debugging
3. **Enable debug logging** for troubleshooting
4. **Use short TTLs** for quick testing cycles
5. **Keep data in memory** for fast restarts

This configuration guide helps you understand how each setting contributes to the supply chain functionality and how to adapt configurations for different organizational roles.
