# FDO Voucher Manager - Comprehensive Test Plan

## Overview

This document outlines the test strategy for the FDO Voucher Manager, focusing on:

1. Voucher reception via HTTP push protocol
2. Voucher sign-over to new owner
3. Voucher transmission to downstream endpoints
4. Ownership validation and rejection of unsigned vouchers
5. Dual-instance scenarios proving end-to-end transmission

## Test Categories

### Category 1: Basic Reception (Single Instance)

#### Test 1.1: Receive Valid Voucher

- Start server with auth disabled
- POST voucher to receiver endpoint
- Verify HTTP 200 response
- Verify voucher stored to filesystem
- Verify transmission record created in database

#### Test 1.2: Receive Voucher with Authentication

- Start server with auth enabled
- POST voucher without token → expect 401
- POST voucher with valid token → expect 200
- POST voucher with invalid token → expect 401

#### Test 1.3: Receive Duplicate Voucher

- POST same voucher twice
- First should succeed (200)
- Second should fail with 409 Conflict

#### Test 1.4: Receive Malformed Voucher

- POST invalid CBOR data → expect 400
- POST truncated voucher → expect 400
- POST oversized voucher → expect 413

### Category 2: Voucher Sign-Over

#### Test 2.1: Sign-Over with Static Owner Key

- Configure static owner key in config
- Receive voucher
- Verify voucher extended to new owner
- Verify OVEExtra data added (if callback configured)

#### Test 2.2: Sign-Over with Dynamic Owner Key (Callback)

- Configure dynamic owner key callback
- Receive voucher with serial/model
- Callback returns owner key
- Verify voucher extended with returned key

#### Test 2.3: Sign-Over with OVEExtra Data

- Configure OVEExtra callback
- Receive voucher
- Callback returns JSON extra data
- Verify CBOR-encoded extra data in signed voucher

#### Test 2.4: Sign-Over Disabled

- Configure signing mode as empty/disabled
- Receive voucher
- Verify voucher stored unchanged (no sign-over)

### Category 3: Transmission (Single Instance)

#### Test 3.1: Transmission to Static Endpoint

- Configure static push URL
- Receive voucher
- Verify transmission record created
- Verify retry worker attempts delivery
- Mock endpoint returns 200 → verify succeeded status

#### Test 3.2: Transmission with Bearer Token

- Configure push URL with auth token
- Receive voucher
- Verify Authorization header sent with token
- Mock endpoint validates token

#### Test 3.3: Transmission Retry on Failure

- Configure push URL that returns 500
- Receive voucher
- Verify first attempt fails
- Verify retry_after set correctly
- Manually trigger retry worker
- Verify retry_after updated

#### Test 3.4: Transmission Max Attempts

- Configure max_attempts: 3
- Configure push URL that always fails
- Receive voucher
- Verify 3 attempts made
- Verify status changed to "failed"

#### Test 3.5: Destination Resolution (Callback)

- Configure destination callback
- Receive voucher
- Callback returns URL
- Verify transmission to returned URL

#### Test 3.6: Destination Resolution (DID)

- Configure DID push enabled
- Receive voucher with DID owner key
- Verify DID resolved to voucherRecipientURL
- Verify transmission to DID-resolved endpoint

### Category 4: Ownership Validation

#### Test 4.1: Reject Unsigned Voucher

- Configure validate_ownership: true
- Receive voucher NOT signed to our owner key
- Verify HTTP 403 Forbidden response
- Verify voucher NOT stored

#### Test 4.2: Accept Properly Signed Voucher

- Configure validate_ownership: true
- Receive voucher signed to our owner key
- Verify HTTP 200 response
- Verify voucher stored

#### Test 4.3: Owner Key Export

- Run: `fdo-voucher-manager keys export -output owner.pem`
- Verify PEM file created with public key
- Verify key matches service's owner key

### Category 5: Dual-Instance Transmission (A → B)

#### Test 5.1: End-to-End Transmission

- Start Instance A (port 8080, key A)
- Start Instance B (port 8081, key B)
- Configure A to transmit to B's receiver endpoint
- Generate test voucher signed to key A
- POST to A's receiver
- Verify A signs voucher over to key B
- Verify A transmits to B
- Verify B receives and stores voucher
- Verify B's transmission record shows success

#### Test 5.2: Dual-Instance with Ownership Validation

- Start Instance A (validate_ownership: false)
- Start Instance B (validate_ownership: true)
- Generate voucher signed to key A
- POST to A → A signs over to key B
- A transmits to B
- B validates ownership (signed to key B) → accepts
- Verify B stores voucher

#### Test 5.3: Dual-Instance with Callbacks

- Start Instance A with OVEExtra callback
- Start Instance B with owner key callback
- A receives voucher, calls OVEExtra callback
- A signs to key returned by B's callback
- A transmits to B
- B receives and stores

#### Test 5.4: Chain Transmission (A → B → C)

- Start 3 instances with different keys
- A → B → C transmission chain
- Verify voucher signed at each hop
- Verify final storage at C

### Category 6: Advanced DID-Based Supply Chain

#### Test 6.1: End-to-End DID Push + FDOKeyAuth

- Start Instance A (Manufacturer, port 8083) with DID document serving
- Start Instance B (Customer, port 8084) with DID document serving
- Fetch B's DID document containing public key and voucher endpoint
- Configure A with B's DID as static_did target
- Generate test voucher (simulates factory device manufacturing)
- POST voucher to A (factory → manufacturer push)
- Verify A resolves B's DID automatically
- Verify A signs voucher over to B's public key
- Verify A pushes signed voucher to B's endpoint
- Verify B receives and stores voucher
- Perform FDOKeyAuth handshake (B authenticates to A)
- Verify both instances serve distinct DID documents
- Verify cryptographic independence (different keys, different DIDs)

**Key Features Tested**:

- DID document discovery and resolution
- Automatic endpoint discovery via DID
- Cryptographic sign-over using DID-resolved keys
- Push transmission with DID-based targeting
- FDOKeyAuth authentication for secure voucher retrieval
- Independent organizational identities
- Complete supply chain simulation

**Learning Resources**:

- [TUTORIAL-E2E-DID-PUSH-PULL.md](TUTORIAL-E2E-DID-PULL.md) - Comprehensive walkthrough
- [diagrams/e2e-flow.md](diagrams/e2e-flow.md) - Visual diagrams
- [CONFIGURATION-GUIDE.md](CONFIGURATION-GUIDE.md) - Configuration details
- [LEARNING-EXERCISES.md](LEARNING-EXERCISES.md) - Hands-on extensions

### Category 7: CLI Commands

#### Test 7.1: List Vouchers

- Receive multiple vouchers
- Run: `fdo-voucher-manager vouchers list`
- Verify all vouchers listed
- Test filters: `-status`, `-guid`, `-limit`

#### Test 7.2: Show Voucher Details

- Receive voucher
- Run: `fdo-voucher-manager vouchers show -guid <guid>`
- Verify all details displayed

#### Test 7.3: Retry Transmission

- Receive voucher, transmission fails
- Run: `fdo-voucher-manager vouchers retry -guid <guid>`
- Verify retry initiated
- Verify status updated

#### Test 7.4: Token Management

- Add token: `tokens add -token abc123 -description "test"`
- List tokens: `tokens list`
- Delete token: `tokens delete -token abc123`
- Verify token operations work

## Test Utilities

### Voucher Generation

#### Option 1: Static Test Voucher

- Pre-generated test voucher in PEM format
- Signed to known test key
- Used for all reception tests

#### Option 2: CLI Voucher Generator

```bash
fdo-voucher-manager vouchers generate \
  --guid <guid> \
  --serial <serial> \
  --model <model> \
  --owner-key <pem-file> \
  --output voucher.pem
```

#### Option 3: Go-FDO Library Integration

- Use go-fdo library to synthesize vouchers
- Allows dynamic voucher generation with custom keys
- Useful for ownership validation tests

### Mock HTTP Endpoints

#### **Mock Receiver** (for transmission tests)

- Simple HTTP server that accepts multipart vouchers
- Configurable response codes (200, 500, etc.)
- Logs received vouchers for verification

#### **Mock Callback Server** (for callback tests)

- Returns JSON responses for callbacks
- Configurable delays/failures
- Logs callback invocations

## Test Configuration

### Instance A Config (tests/config-a.yaml)

```yaml
server:
  addr: localhost:8080
database:
  path: tests/data/instance-a.db
voucher_receiver:
  enabled: true
  endpoint: /api/v1/vouchers
  require_auth: false
voucher_signing:
  mode: internal
owner_signover:
  mode: static
  static_public_key: <key-b-pem>
push_service:
  enabled: true
  url: http://localhost:8081/api/v1/vouchers
  mode: push
retry_worker:
  enabled: true
  retry_interval: 5s
```

### Instance B Config (tests/config-b.yaml)

```yaml
server:
  addr: localhost:8081
database:
  path: tests/data/instance-b.db
voucher_receiver:
  enabled: true
  endpoint: /api/v1/vouchers
  require_auth: false
  validate_ownership: true
voucher_signing:
  mode: internal
push_service:
  enabled: false
```

## Test Execution

### Run All Tests

```bash
./tests/run-all-tests.sh
```

### Run Category

```bash
./tests/run-category.sh 1  # Category 1: Basic Reception
./tests/run-category.sh 5  # Category 5: Dual-Instance
```

### Run Single Test

```bash
./tests/test-1.1-receive-valid-voucher.sh
./tests/test-5.1-end-to-end-transmission.sh
```

## Success Criteria

- All Category 1 tests pass (reception works)
- All Category 2 tests pass (sign-over works)
- All Category 3 tests pass (transmission works)
- All Category 5 tests pass (dual-instance works)
- All Category 6 tests pass (CLI works)
- Categories 4 & 5 pass (ownership validation works)

## Test Data

- `tests/vouchers/` - Pre-generated test vouchers
- `tests/keys/` - Test key pairs (key-a.pem, key-b.pem, etc.)
- `tests/data/` - Runtime databases and files
- `tests/scripts/` - Helper scripts (mock servers, callbacks)
