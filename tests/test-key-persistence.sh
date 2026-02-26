#!/bin/bash
# Test: Owner Key Persistence
#
# Verifies that the owner key survives server restarts when
# key_management.first_time_init=true and did_minting.key_export_path is set.
#
# Tests:
#   1. First start generates a key and persists it to disk
#   2. Second start loads the same key (fingerprint matches)
#   3. DID document public key matches across restarts
#   4. import_key_file mode loads a pre-generated key
#   5. Negative: missing import_key_file fails gracefully

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

PORT=8087
SERVER_PID=""
TMPDIR_TEST=""

cleanup() {
    if [ -n "$SERVER_PID" ]; then
        stop_server "$SERVER_PID" "key-persist"
    fi
    # Kill anything left on the port
    stale_pid=$(lsof -ti "tcp:$PORT" 2>/dev/null || true)
    if [ -n "$stale_pid" ]; then
        kill "$stale_pid" 2>/dev/null || true
        sleep 0.3
        kill -9 "$stale_pid" 2>/dev/null || true
    fi
    if [ -n "$TMPDIR_TEST" ]; then
        rm -rf "$TMPDIR_TEST"
    fi
}

trap cleanup EXIT

log_info "=== Owner Key Persistence Test ==="

check_binary || exit 1
init_test_env

TMPDIR_TEST=$(mktemp -d)
KEY_FILE="$TMPDIR_TEST/owner-key.pem"
DB_FILE="$TMPDIR_TEST/persist-test.db"
VOUCHERS_DIR="$TMPDIR_TEST/vouchers"
CONFIG_FILE="$TMPDIR_TEST/config-persist.yaml"

mkdir -p "$VOUCHERS_DIR"

# ============================================================
# Helper: write config with first_time_init + key_export_path
# ============================================================
write_persist_config() {
    cat > "$CONFIG_FILE" <<EOF
debug: true
server:
  addr: "localhost:$PORT"
  ext_addr: "localhost:$PORT"
database:
  path: "$DB_FILE"
key_management:
  key_type: "ec384"
  first_time_init: true
  import_key_file: ""
voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"
voucher_files:
  directory: "$VOUCHERS_DIR"
did_minting:
  enabled: true
  host: "localhost:$PORT"
  serve_did_document: true
  export_did_uri: true
  key_export_path: "$KEY_FILE"
pull_service:
  enabled: false
EOF
}

# ============================================================
# Helper: extract public key fingerprint from DID document
# ============================================================
get_did_fingerprint() {
    local port="$1"
    # Fetch DID document and extract the public key JWK → compute a simple hash
    # We use the raw JSON for comparison since the key material is deterministic
    curl -s "http://localhost:${port}/.well-known/did.json" 2>/dev/null
}

# ============================================================
# Test 1: First start generates and persists a key
# ============================================================
step_first_start() {
    log_info "--- Test 1: First start generates and persists key ---"

    write_persist_config

    # Ensure no key file exists
    rm -f "$KEY_FILE"

    SERVER_PID=$(start_server "$CONFIG_FILE" "$PORT" "key-persist")
    if [ -z "$SERVER_PID" ]; then
        log_error "Failed to start server"
        return 1
    fi

    # Key file should now exist
    assert_file_exists "$KEY_FILE" "Owner key PEM file created on first start"

    # Grab the DID document for later comparison
    DID_DOC_1=$(get_did_fingerprint "$PORT")
    assert_not_empty "$DID_DOC_1" "DID document served on first start"

    # Check server log for "private key persisted"
    if grep -q "private key persisted" "$TEST_DATA_DIR/key-persist.log" 2>/dev/null; then
        log_success "Server log confirms key was persisted"
        ((TESTS_PASSED++))
    else
        log_error "Server log missing 'private key persisted' message"
        ((TESTS_FAILED++))
    fi

    # Stop server
    stop_server "$SERVER_PID" "key-persist"
    SERVER_PID=""
    sleep 1
}

# ============================================================
# Test 2: Second start loads the same key
# ============================================================
step_second_start() {
    log_info "--- Test 2: Second start loads persisted key ---"

    # Start server again (same config, key file exists)
    SERVER_PID=$(start_server "$CONFIG_FILE" "$PORT" "key-persist")
    if [ -z "$SERVER_PID" ]; then
        log_error "Failed to start server on second run"
        return 1
    fi

    # Grab DID document
    DID_DOC_2=$(get_did_fingerprint "$PORT")
    assert_not_empty "$DID_DOC_2" "DID document served on second start"

    # DID documents should be identical (same key → same DID doc)
    assert_equals "$DID_DOC_1" "$DID_DOC_2" "DID document identical across restarts (key persisted)"

    # Check server log for "loaded from persistent storage"
    if grep -q "loaded from persistent storage" "$TEST_DATA_DIR/key-persist.log" 2>/dev/null; then
        log_success "Server log confirms key was loaded from disk"
        ((TESTS_PASSED++))
    else
        log_error "Server log missing 'loaded from persistent storage' message"
        ((TESTS_FAILED++))
    fi

    # Should NOT see "generating EPHEMERAL"
    if grep -q "EPHEMERAL" "$TEST_DATA_DIR/key-persist.log" 2>/dev/null; then
        log_error "Server generated ephemeral key despite first_time_init + key_export_path"
        ((TESTS_FAILED++))
    else
        log_success "No ephemeral key warning on second start"
        ((TESTS_PASSED++))
    fi

    stop_server "$SERVER_PID" "key-persist"
    SERVER_PID=""
    sleep 1
}

# ============================================================
# Test 3: import_key_file loads a pre-generated key
# ============================================================
step_import_key() {
    log_info "--- Test 3: import_key_file loads a pre-generated key ---"

    # Generate a fresh key externally
    IMPORT_KEY="$TMPDIR_TEST/import-key.pem"
    openssl ecparam -genkey -name secp384r1 -noout 2>/dev/null | \
        openssl pkcs8 -topk8 -nocrypt -out "$IMPORT_KEY" 2>/dev/null

    if [ ! -f "$IMPORT_KEY" ]; then
        log_error "Failed to generate import key with openssl"
        ((TESTS_FAILED++))
        return 1
    fi

    # Write config with import_key_file set
    IMPORT_CONFIG="$TMPDIR_TEST/config-import.yaml"
    IMPORT_DB="$TMPDIR_TEST/import-test.db"
    cat > "$IMPORT_CONFIG" <<EOF
debug: true
server:
  addr: "localhost:$PORT"
  ext_addr: "localhost:$PORT"
database:
  path: "$IMPORT_DB"
key_management:
  key_type: "ec384"
  first_time_init: false
  import_key_file: "$IMPORT_KEY"
voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"
voucher_files:
  directory: "$VOUCHERS_DIR"
did_minting:
  enabled: true
  host: "localhost:$PORT"
  serve_did_document: true
  export_did_uri: true
  key_export_path: ""
pull_service:
  enabled: false
EOF

    SERVER_PID=$(start_server "$IMPORT_CONFIG" "$PORT" "key-persist")
    if [ -z "$SERVER_PID" ]; then
        log_error "Failed to start server with import_key_file"
        return 1
    fi

    # Check server log for "loaded from import file"
    if grep -q "loaded from import file" "$TEST_DATA_DIR/key-persist.log" 2>/dev/null; then
        log_success "Server loaded key from import_key_file"
        ((TESTS_PASSED++))
    else
        log_error "Server log missing 'loaded from import file' message"
        ((TESTS_FAILED++))
    fi

    DID_DOC_IMPORT=$(get_did_fingerprint "$PORT")
    assert_not_empty "$DID_DOC_IMPORT" "DID document served with imported key"

    stop_server "$SERVER_PID" "key-persist"
    SERVER_PID=""
    sleep 1
}

# ============================================================
# Test 4 (Negative): missing import_key_file
# ============================================================
step_missing_import() {
    log_info "--- Test 4 (Negative): missing import_key_file fails ---"

    MISSING_CONFIG="$TMPDIR_TEST/config-missing.yaml"
    MISSING_DB="$TMPDIR_TEST/missing-test.db"
    cat > "$MISSING_CONFIG" <<EOF
debug: true
server:
  addr: "localhost:$PORT"
  ext_addr: "localhost:$PORT"
database:
  path: "$MISSING_DB"
key_management:
  key_type: "ec384"
  first_time_init: false
  import_key_file: "/nonexistent/path/key.pem"
voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"
voucher_files:
  directory: "$VOUCHERS_DIR"
did_minting:
  enabled: true
  host: "localhost:$PORT"
  serve_did_document: true
  export_did_uri: true
  key_export_path: ""
pull_service:
  enabled: false
EOF

    # Start server — it should start but DID minting should fail
    "$PROJECT_ROOT/fdo-voucher-manager" server -config "$MISSING_CONFIG" \
        > "$TEST_DATA_DIR/key-persist.log" 2>&1 &
    local pid=$!
    sleep 3

    # Check if the error was logged
    if grep -q "failed to load import key" "$TEST_DATA_DIR/key-persist.log" 2>/dev/null || \
       grep -q "failed to load/generate owner key" "$TEST_DATA_DIR/key-persist.log" 2>/dev/null; then
        log_success "Server correctly reported error for missing import key"
        ((TESTS_PASSED++))
    else
        log_error "Server did not report error for missing import key"
        ((TESTS_FAILED++))
    fi

    kill "$pid" 2>/dev/null || true
    sleep 0.5
    kill -9 "$pid" 2>/dev/null || true
}

# ============================================================
# Run all steps
# ============================================================

step_first_start
step_second_start
step_import_key
step_missing_import

echo ""
print_summary
