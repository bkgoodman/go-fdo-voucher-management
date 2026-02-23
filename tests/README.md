# FDO Voucher Manager - Test Suite

This directory contains integration tests for the FDO Voucher Manager, exercising the voucher supply chain operations that the service performs: receiving vouchers from upstream sources, signing them over to new owners, and transmitting them downstream.

The tests simulate real supply chain scenarios by running one or more service instances with different keys and configurations. For example, "Instance A" might represent a manufacturer's voucher service receiving from a factory, while "Instance B" represents a customer's service or a downstream reseller. When a voucher is pushed to Instance A, the test verifies that it is signed over to Instance B's owner key and forwarded automatically — the same flow that would occur between any two organizations in a real deployment.

For background on the supply chain model and terminology, see [VOUCHER_SUPPLY_CHAIN.md](../VOUCHER_SUPPLY_CHAIN.md).

## Quick Start

```bash
# Run all tests
./run-all-tests.sh

# Run a specific test
./test-1.1-receive-valid-voucher.sh
./test-5.1-end-to-end-transmission.sh
./test-e2e-did-push-pull.sh
```

## Learning Resources

### 🎯 **New: Comprehensive E2E Tutorial**

For a complete step-by-step walkthrough of the DID-based push and pull test, see:

- **[TUTORIAL-E2E-DID-PUSH-PULL.md](TUTORIAL-E2E-DID-PULL.md)** - Learn how FDO vouchers flow through supply chains
- **[diagrams/e2e-flow.md](diagrams/e2e-flow.md)** - Visual diagrams of the test flow
- **[CONFIGURATION-GUIDE.md](CONFIGURATION-GUIDE.md)** - Deep dive into configuration options
- **[LEARNING-EXERCISES.md](LEARNING-EXERCISES.md)** - Hands-on exercises to deepen understanding
- **[LEARNING-PATH.md](LEARNING-PATH.md)** - Structured curriculum from beginner to expert

These resources transform the technical test into an educational experience that explains both the "how" and "why" of FDO voucher operations in real supply chains.

## Test Infrastructure

### lib.sh

Common utilities and helper functions used by all tests:

- **Server Management**: `start_server()`, `stop_server()`
- **HTTP Operations**: `send_voucher()`, `query_transmission()`, `list_transmissions()`
- **Key Management**: `export_owner_key()`, `add_token()`
- **Assertions**: `assert_equals()`, `assert_file_exists()`, `assert_http_status()`
- **Logging**: `log_info()`, `log_success()`, `log_error()`, `log_warn()`
- **Environment**: `init_test_env()`, `cleanup_test_env()`

### Configuration Files

**config-a.yaml**: Instance A — simulates an upstream voucher service (e.g., a manufacturer or reseller)

- Server on port 8080
- Signing enabled (internal mode)
- Push service enabled (transmits to Instance B)
- Retry worker enabled

**config-b.yaml**: Instance B — simulates a downstream voucher service or customer (e.g., a buyer or onboarding service)

- Server on port 8081
- Signing enabled (internal mode)
- Push service disabled (receiver only)
- Retry worker disabled

## Test Categories

### Category 1: Basic Reception

Simulates a factory or upstream service pushing a voucher to this service.

#### Test 1.1: Receive Valid Voucher

- Starts a single server instance
- Sends a test voucher via HTTP POST (as a factory would after device initialization)
- Verifies HTTP 200 response
- Verifies voucher stored to filesystem
- Verifies transmission record created in database

### Category 5: Dual-Instance Transmission

Simulates a two-hop supply chain: an upstream voucher service (Instance A) receives a voucher, signs it over to a downstream service's key (Instance B), and pushes it automatically. This is the core supply chain flow — the same pattern whether A is a manufacturer and B is a reseller, or A is a reseller and B is a customer.

#### Test 5.1: End-to-End Transmission (A → B)

- Starts two instances with different owner keys
- Exports Instance B's owner key (simulating a customer sharing their key with a supplier)
- Configures Instance A to sign over to Instance B's key
- Sends voucher to Instance A (simulating a factory push)
- Verifies Instance A signs and transmits to Instance B
- Verifies Instance B receives and stores voucher
- Verifies transmission records in both instances

### Category 6: Advanced DID-Based Supply Chain

#### Test 6.1: End-to-End DID Push + PullAuth

**⭐ Featured Test**: See [TUTORIAL-E2E-DID-PUSH-PULL.md](TUTORIAL-E2E-DID-PUSH-PULL.md) for a comprehensive tutorial.

This advanced test demonstrates modern FDO supply chain operations using DID-based discovery and cryptographic authentication:

- **DID Document Discovery**: Both instances serve DID documents containing public keys and voucher endpoints
- **Automatic DID Resolution**: Manufacturer automatically resolves customer's DID to discover keys and endpoints
- **Cryptographic Sign-Over**: Vouchers are signed over to customer's public key automatically
- **PushAuth Authentication**: Customer authenticates using PullAuth CBOR handshake for secure voucher retrieval
- **Real Supply Chain Model**: Simulates Manufacturer → Customer voucher flow with proper organizational boundaries

**Key Features**:

- Two independent services with distinct cryptographic identities
- DID-based automatic discovery (no manual endpoint configuration)
- Both push (automatic) and pull (on-demand) transfer mechanisms
- Cryptographic authentication using owner keys
- Complete audit trail of voucher ownership chain

**Learning Outcomes**:

- Understand how DIDs replace manual key/endpoint exchange
- Learn the difference between push and pull voucher transfer models
- See how cryptographic authentication secures voucher retrieval
- Experience a complete supply chain simulation

## Test Data

Tests use the following directory structure:

```text
tests/
├── data/                    # Runtime test data
│   ├── instance-a.db       # Instance A database
│   ├── instance-b.db       # Instance B database
│   ├── vouchers-a/         # Instance A voucher storage
│   ├── vouchers-b/         # Instance B voucher storage
│   └── *.log               # Server logs
├── keys/                    # Test key files
│   ├── key-a.pem           # Instance A public key
│   └── key-b.pem           # Instance B public key
└── vouchers/                # Test voucher files
    └── test-voucher-*.pem   # Generated test vouchers
```

## Running Tests

### Single Test

```bash
./test-1.1-receive-valid-voucher.sh
```

Output:

```terminal
[INFO] Test 1.1: Receive Valid Voucher
[INFO] Starting test-server on port 8080...
[PASS] test-server started (PID: 12345)
[PASS] Test voucher created
[INFO] Sending voucher to receiver endpoint...
[PASS] Voucher received successfully (status: 200)
[PASS] Voucher stored to filesystem
[PASS] GUID extracted from response
[PASS] Transmission record found in database
[PASS] Test completed: Test 1.1: Receive Valid Voucher

======================================
Test Summary
======================================
Total tests: 5
Passed: 5
Failed: 0
```

### All Tests

```bash
./run-all-tests.sh
```

## Test Assertions

Tests use the following assertion functions:

```bash
# Equality assertion
assert_equals "expected" "actual" "message"

# Non-empty assertion
assert_not_empty "value" "message"

# File existence assertion
assert_file_exists "/path/to/file" "message"

# HTTP status assertion
assert_http_status "200" "actual_code" "message"
```

## Troubleshooting

### Port Already in Use

If tests fail with "Address already in use", kill existing processes:

```bash
pkill -f "fdo-voucher-manager server"
```

### Database Locked

If tests fail with "database is locked", ensure no other instances are running:

```bash
rm -f tests/data/*.db
```

### Server Startup Timeout

If server fails to start, check logs:

```bash
cat tests/data/instance-a.log
cat tests/data/instance-b.log
```

### Voucher Transmission Not Completing

The retry worker has a 5-second interval. If transmission doesn't complete within the test timeout:

1. Check server logs for errors
2. Verify network connectivity between instances
3. Increase timeout in test script if needed

## Extending Tests

### Adding a New Test

1. Create `test-X.Y-description.sh`:

```bash
#!/bin/bash
set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

test_my_feature() {
    log_info "Test X.Y: My Feature"
    init_test_env
    
    # Test logic here
    local server_pid=$(start_server "$SCRIPT_DIR/config-a.yaml" 8080 "server")
    
    # Assertions
    assert_equals "expected" "actual" "message"
    
    stop_server "$server_pid" "server"
    cleanup_test_env
    return 0
}

test_my_feature
print_summary
```

1. Make executable:

```bash
chmod +x test-X.Y-description.sh
```

1. Add to `run-all-tests.sh`:

```bash
if bash "$SCRIPT_DIR/test-X.Y-description.sh"; then
    log_success "Test X.Y passed"
else
    log_error "Test X.Y failed"
    ((failed++))
fi
```

### Adding Helper Functions

Add new functions to `lib.sh`:

```bash
my_helper_function() {
    local arg1="$1"
    # Implementation
}

export -f my_helper_function
```

## Test Coverage

### Implemented

- ✅ Basic voucher reception
- ✅ Dual-instance transmission (A → B)
- ✅ Server startup and shutdown
- ✅ HTTP multipart form submission
- ✅ Database record verification
- ✅ Filesystem storage verification

### Planned

- ⏳ Authentication token validation
- ⏳ Malformed voucher rejection
- ⏳ Duplicate voucher detection
- ⏳ Ownership validation
- ⏳ Owner key export
- ⏳ Callback execution
- ⏳ DID resolution
- ⏳ Retry logic
- ⏳ CLI commands (list, show, retry, tokens)
- ⏳ Three-instance chain transmission (A → B → C)

## Notes

- Tests use real HTTP servers, not mocks
- Tests clean up after themselves (databases, files, processes)
- Tests are idempotent (can be run multiple times)
- Tests use localhost only (no network dependencies)
- Tests timeout after 30 seconds by default
- Server startup timeout is 30 seconds
- Retry worker interval is 5 seconds in test configs

## Performance

Typical test execution times:

- Test 1.1 (basic reception): ~3-5 seconds
- Test 5.1 (dual-instance): ~8-12 seconds
- Full suite: ~15-20 seconds

## Debugging

Enable verbose output by modifying test scripts:

```bash
set -x  # Print all commands
```

Check server logs:

```bash
tail -f tests/data/instance-a.log
tail -f tests/data/instance-b.log
```

Query database directly:

```bash
sqlite3 tests/data/instance-a.db "SELECT * FROM voucher_transmissions;"
```

List stored vouchers:

```bash
ls -la tests/data/vouchers-a/
ls -la tests/data/vouchers-b/
```
