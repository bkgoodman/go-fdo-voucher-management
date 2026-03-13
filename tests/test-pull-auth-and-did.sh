#!/bin/bash
# Test: Pull Authentication and DID Minting
# Tests the FDOKeyAuth protocol endpoints and DID document serving.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

CONFIG="$SCRIPT_DIR/config-pull.yaml"
PORT=8082
SERVER_PID=""

cleanup() {
    if [ -n "$SERVER_PID" ]; then
        stop_server "$SERVER_PID" "pull-test-server"
    fi
    rm -f "$TEST_DATA_DIR/pull-test.db" 2>/dev/null || true
    rm -f "$TEST_DATA_DIR/pull-test.db-shm" 2>/dev/null || true
    rm -f "$TEST_DATA_DIR/pull-test.db-wal" 2>/dev/null || true
    rm -rf "$TEST_DATA_DIR/vouchers-pull" 2>/dev/null || true
}

trap cleanup EXIT

# ============================================================
# Setup
# ============================================================
log_info "=== Pull Auth & DID Minting Test ==="

init_test_env
cleanup_test_env
check_binary || exit 1

mkdir -p "$TEST_DATA_DIR/vouchers-pull"

# Start server with pull service and DID minting enabled
SERVER_PID=$(start_server "$CONFIG" "$PORT" "pull-test-server")
if [ -z "$SERVER_PID" ]; then
    log_error "Failed to start server"
    exit 1
fi

BASE_URL="http://localhost:$PORT"

# ============================================================
# Test 1: DID Document serving at .well-known/did.json
# ============================================================
test_did_document_serving() {
    log_info "Test 1: DID Document serving"

    local response
    response=$(curl -s -w "\n%{http_code}" "$BASE_URL/.well-known/did.json")
    local http_code
    http_code=$(echo "$response" | tail -n1)
    local body
    body=$(echo "$response" | head -n-1)

    assert_http_status "200" "$http_code" "DID document should be served at .well-known/did.json"

    # Verify it's valid JSON with expected fields
    local did_id
    did_id=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null || echo "")
    assert_not_empty "$did_id" "DID document should have an 'id' field"

    # Verify it contains a verification method
    local vm_count
    vm_count=$(echo "$body" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('verificationMethod',[])))" 2>/dev/null || echo "0")
    assert_equals "1" "$vm_count" "DID document should have 1 verification method"

    # Verify it contains the FDOVoucherRecipient service
    local svc_type
    svc_type=$(echo "$body" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('service',[{}])[0].get('type',''))" 2>/dev/null || echo "")
    assert_equals "FDOVoucherRecipient" "$svc_type" "DID document should have FDOVoucherRecipient service"

    log_info "DID Document:"
    echo "$body" | python3 -m json.tool 2>/dev/null || echo "$body"
}

# ============================================================
# Test 2: DID Document content-type
# ============================================================
test_did_content_type() {
    log_info "Test 2: DID Document content-type"

    local content_type
    content_type=$(curl -s -o /dev/null -w "%{content_type}" "$BASE_URL/.well-known/did.json")
    assert_equals "application/did+ld+json" "$content_type" "DID document should have correct content-type"
}

# ============================================================
# Test 3: FDOKeyAuth Hello endpoint exists
# ============================================================
test_fdokeyauth_hello_endpoint() {
    log_info "Test 3: FDOKeyAuth Hello endpoint"

    # Send an empty body - should get 400 (bad request), not 404
    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
        -H "Content-Type: application/cbor" \
        "$BASE_URL/api/v1/pull/auth/hello")

    # 400 means the endpoint exists but rejected our malformed request
    if [ "$http_code" = "400" ] || [ "$http_code" = "200" ]; then
        log_success "FDOKeyAuth Hello endpoint exists (HTTP $http_code)"
        ((TESTS_PASSED++))
    else
        log_error "FDOKeyAuth Hello endpoint not found (HTTP $http_code, expected 400)"
        ((TESTS_FAILED++))
    fi
}

# ============================================================
# Test 4: FDOKeyAuth Prove endpoint exists
# ============================================================
test_fdokeyauth_prove_endpoint() {
    log_info "Test 4: FDOKeyAuth Prove endpoint"

    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
        -H "Content-Type: application/cbor" \
        "$BASE_URL/api/v1/pull/auth/prove")

    if [ "$http_code" = "400" ] || [ "$http_code" = "401" ]; then
        log_success "FDOKeyAuth Prove endpoint exists (HTTP $http_code)"
        ((TESTS_PASSED++))
    else
        log_error "FDOKeyAuth Prove endpoint not found (HTTP $http_code, expected 400 or 401)"
        ((TESTS_FAILED++))
    fi
}

# ============================================================
# Test 5: Voucher receiver still works alongside pull service
# ============================================================
test_voucher_receiver_coexists() {
    log_info "Test 5: Voucher receiver coexists with pull service"

    # The voucher receiver endpoint should still respond
    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
        "$BASE_URL/api/v1/vouchers")

    # 400 means the endpoint exists (bad request because no multipart data)
    if [ "$http_code" = "400" ] || [ "$http_code" = "200" ]; then
        log_success "Voucher receiver endpoint coexists (HTTP $http_code)"
        ((TESTS_PASSED++))
    else
        log_error "Voucher receiver endpoint not responding (HTTP $http_code)"
        ((TESTS_FAILED++))
    fi
}

# ============================================================
# Run all tests
# ============================================================
run_test "DID Document Serving" test_did_document_serving
run_test "DID Content-Type" test_did_content_type
run_test "FDOKeyAuth Hello Endpoint" test_fdokeyauth_hello_endpoint
run_test "FDOKeyAuth Prove Endpoint" test_fdokeyauth_prove_endpoint
run_test "Voucher Receiver Coexists" test_voucher_receiver_coexists

print_summary
