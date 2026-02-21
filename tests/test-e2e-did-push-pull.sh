#!/bin/bash
# Test: End-to-End DID Push + PullAuth Pull
#
# Two instances ("First" on :8083, "Second" on :8084) with different owner keys.
#
# Scenario A — DID-based Push:
#   1. Start Second (serves its DID document at /.well-known/did.json)
#   2. Fetch Second's DID URI
#   3. Start First with Second's DID as static_did (owner signover target)
#   4. Push a voucher to First
#   5. First resolves Second's DID → extracts public key + voucher recipient URL
#   6. First signs voucher over to Second's key and pushes to Second's endpoint
#   7. Verify Second received the voucher
#
# Scenario B — PullAuth (Type-5) Pull:
#   8. Second authenticates to First using PullAuth (CBOR handshake)
#   9. Verify PullAuth endpoints respond correctly

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

PORT_FIRST=8083
PORT_SECOND=8084
FIRST_PID=""
SECOND_PID=""

cleanup() {
    if [ -n "$FIRST_PID" ]; then
        stop_server "$FIRST_PID" "first"
    fi
    if [ -n "$SECOND_PID" ]; then
        stop_server "$SECOND_PID" "second"
    fi
    rm -f "$TEST_DATA_DIR/e2e-first.db" "$TEST_DATA_DIR/e2e-first.db-shm" "$TEST_DATA_DIR/e2e-first.db-wal" 2>/dev/null || true
    rm -f "$TEST_DATA_DIR/e2e-second.db" "$TEST_DATA_DIR/e2e-second.db-shm" "$TEST_DATA_DIR/e2e-second.db-wal" 2>/dev/null || true
    rm -rf "$TEST_DATA_DIR/vouchers-e2e-first" "$TEST_DATA_DIR/vouchers-e2e-second" 2>/dev/null || true
    rm -f "$SCRIPT_DIR/config-e2e-first-live.yaml" 2>/dev/null || true
}

trap cleanup EXIT

# ============================================================
log_info "=== End-to-End DID Push + PullAuth Pull Test ==="
# ============================================================

init_test_env
check_binary || exit 1

mkdir -p "$TEST_DATA_DIR/vouchers-e2e-first"
mkdir -p "$TEST_DATA_DIR/vouchers-e2e-second"

# ============================================================
# Step 1: Start Second (serves DID document)
# ============================================================
step_start_second() {
    log_info "Step 1: Starting Second instance (port $PORT_SECOND)..."
    SECOND_PID=$(start_server "$SCRIPT_DIR/config-e2e-second.yaml" "$PORT_SECOND" "second")
    if [ -z "$SECOND_PID" ]; then
        log_error "Failed to start Second instance"
        return 1
    fi
    log_success "Second instance started (PID: $SECOND_PID)"
}

# ============================================================
# Step 2: Fetch Second's DID URI from its DID document
# ============================================================
step_fetch_second_did() {
    log_info "Step 2: Fetching Second's DID document..."

    local response
    response=$(curl -s -w "\n%{http_code}" "http://localhost:$PORT_SECOND/.well-known/did.json")
    local http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status "200" "$http_code" "Second's DID document should be served"

    # Extract the DID URI
    SECOND_DID_URI=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null || echo "")
    assert_not_empty "$SECOND_DID_URI" "Second's DID URI should be present"
    log_info "Second's DID URI: $SECOND_DID_URI"

    # Extract the voucher recipient URL from the service entry
    SECOND_VOUCHER_URL=$(echo "$body" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for svc in d.get('service', []):
    if svc.get('type') == 'FDOVoucherRecipient':
        print(svc.get('serviceEndpoint', ''))
        break
" 2>/dev/null || echo "")
    assert_not_empty "$SECOND_VOUCHER_URL" "Second's voucher recipient URL should be in DID document"
    log_info "Second's voucher recipient URL: $SECOND_VOUCHER_URL"

    # Verify the public key is present
    local key_type
    key_type=$(echo "$body" | python3 -c "
import sys, json
d = json.load(sys.stdin)
vm = d.get('verificationMethod', [{}])[0]
jwk = vm.get('publicKeyJwk', {})
print(jwk.get('kty', '') + '/' + jwk.get('crv', ''))
" 2>/dev/null || echo "")
    assert_equals "EC/P-384" "$key_type" "Second's key should be EC/P-384"
}

# ============================================================
# Step 3: Create modified config for First with Second's DID
# ============================================================
step_create_first_config() {
    log_info "Step 3: Creating First config with Second's DID as static_did..."

    # Copy the template and inject Second's DID URI
    sed "s|static_did: \"\"|static_did: \"$SECOND_DID_URI\"|" \
        "$SCRIPT_DIR/config-e2e-first.yaml" > "$SCRIPT_DIR/config-e2e-first-live.yaml"

    # Verify the config was written correctly
    local did_in_config
    did_in_config=$(grep "static_did:" "$SCRIPT_DIR/config-e2e-first-live.yaml" | head -1)
    log_info "First config owner_signover: $did_in_config"

    assert_not_empty "$did_in_config" "First config should contain static_did"
}

# ============================================================
# Step 4: Start First (with DID-based signover to Second)
# ============================================================
step_start_first() {
    log_info "Step 4: Starting First instance (port $PORT_FIRST)..."
    FIRST_PID=$(start_server "$SCRIPT_DIR/config-e2e-first-live.yaml" "$PORT_FIRST" "first")
    if [ -z "$FIRST_PID" ]; then
        log_error "Failed to start First instance"
        return 1
    fi
    log_success "First instance started (PID: $FIRST_PID)"
}

# ============================================================
# Step 5: Generate and push a voucher to First
# ============================================================
step_push_voucher_to_first() {
    log_info "Step 5: Generating and pushing a test voucher to First..."

    local test_voucher="$TEST_VOUCHERS_DIR/test-voucher-e2e-did.pem"
    "$PROJECT_ROOT/fdo-voucher-manager" generate voucher \
        -serial "E2E-DID-SERIAL-001" \
        -model "E2E-DID-MODEL-001" \
        -output "$test_voucher" > /dev/null 2>&1 || {
        log_error "Failed to generate test voucher"
        return 1
    }
    assert_file_exists "$test_voucher" "Test voucher generated"

    local response
    response=$(send_voucher "$test_voucher" "http://localhost:$PORT_FIRST/api/v1/vouchers" "" "E2E-DID-SERIAL-001" "E2E-DID-MODEL-001")
    local http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status "200" "$http_code" "Voucher accepted by First"

    VOUCHER_GUID=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('voucher_id',''))" 2>/dev/null || echo "")
    if [ -z "$VOUCHER_GUID" ]; then
        VOUCHER_GUID=$(echo "$body" | grep -o '"voucher_id":"[^"]*' | cut -d'"' -f4)
    fi
    log_info "Voucher GUID: $VOUCHER_GUID"
}

# ============================================================
# Step 6: Verify First resolved Second's DID and pushed the voucher
# ============================================================
step_verify_did_push() {
    log_info "Step 6: Waiting for First to push voucher to Second via DID..."

    # Wait for the push to complete (check First's logs and Second's storage)
    local max_wait=15
    local waited=0
    local push_succeeded=false

    while [ $waited -lt $max_wait ]; do
        # Check if Second received a voucher file
        local stored_voucher
        stored_voucher=$(find "$TEST_DATA_DIR/vouchers-e2e-second" -type f 2>/dev/null | head -1)
        if [ -n "$stored_voucher" ]; then
            push_succeeded=true
            break
        fi
        sleep 1
        ((waited++))
    done

    if [ "$push_succeeded" = true ]; then
        log_success "DID-based push succeeded: voucher delivered to Second"
        ((TESTS_PASSED++))
    else
        log_error "DID-based push failed: voucher not found in Second's storage after ${max_wait}s"
        ((TESTS_FAILED++))

        # Dump First's log for debugging
        log_info "First instance log (last 20 lines):"
        tail -20 "$TEST_DATA_DIR/first.log" 2>/dev/null | while IFS= read -r line; do
            log_info "  $line"
        done
    fi

    # Verify First's log shows DID resolution
    if grep -q "resolved static DID" "$TEST_DATA_DIR/first.log" 2>/dev/null; then
        log_success "First's log confirms DID resolution"
        ((TESTS_PASSED++))
    else
        log_error "First's log does not show DID resolution"
        ((TESTS_FAILED++))
    fi

    # Verify First's log shows the push destination matches Second's voucher URL
    if grep -q "localhost:$PORT_SECOND" "$TEST_DATA_DIR/first.log" 2>/dev/null; then
        log_success "First's log shows push to Second's endpoint"
        ((TESTS_PASSED++))
    else
        log_error "First's log does not show push to Second's endpoint"
        ((TESTS_FAILED++))
    fi
}

# ============================================================
# Step 7: PullAuth — Real CBOR handshake (Type-5 authentication)
# Second uses its owner key to authenticate to First via PullAuth
# ============================================================
step_pullauth_handshake() {
    log_info "Step 7: PullAuth handshake — Second authenticates to First..."

    # Perform a real PullAuth CBOR handshake against First using an ephemeral key.
    # This exercises the full 3-message protocol:
    #   Hello (owner key + nonce) → Challenge (holder sig) → Prove (recipient sig) → Result (token)
    local pullauth_output
    pullauth_output=$("$PROJECT_ROOT/fdo-voucher-manager" pullauth \
        -url "http://localhost:$PORT_FIRST" \
        -key-type ec384 \
        -json 2>/dev/null)
    local pullauth_exit=$?

    if [ $pullauth_exit -eq 0 ]; then
        log_success "PullAuth handshake succeeded (exit 0)"
        ((TESTS_PASSED++))
    else
        log_error "PullAuth handshake failed (exit $pullauth_exit)"
        ((TESTS_FAILED++))
        log_info "PullAuth output: $pullauth_output"
        return
    fi

    # Verify we got a session token
    local session_token
    session_token=$(echo "$pullauth_output" | python3 -c "import sys,json; print(json.load(sys.stdin).get('session_token',''))" 2>/dev/null || echo "")
    assert_not_empty "$session_token" "PullAuth returned a session token"

    # Verify status is authenticated
    local status
    status=$(echo "$pullauth_output" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null || echo "")
    assert_equals "authenticated" "$status" "PullAuth status should be 'authenticated'"

    # Verify we got an owner key fingerprint
    local fingerprint
    fingerprint=$(echo "$pullauth_output" | python3 -c "import sys,json; print(json.load(sys.stdin).get('owner_key_fingerprint',''))" 2>/dev/null || echo "")
    assert_not_empty "$fingerprint" "PullAuth returned an owner key fingerprint"

    log_info "PullAuth result:"
    echo "$pullauth_output" | python3 -m json.tool 2>/dev/null || echo "$pullauth_output"
}

# ============================================================
# Step 8: Verify both DID documents are independently correct
# ============================================================
step_verify_both_dids() {
    log_info "Step 8: Verifying both instances serve distinct DID documents..."

    # First's DID
    local first_did
    first_did=$(curl -s "http://localhost:$PORT_FIRST/.well-known/did.json" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null || echo "")
    assert_not_empty "$first_did" "First's DID URI should be present"

    # Second's DID
    local second_did
    second_did=$(curl -s "http://localhost:$PORT_SECOND/.well-known/did.json" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null || echo "")
    assert_not_empty "$second_did" "Second's DID URI should be present"

    # They should be different
    if [ "$first_did" != "$second_did" ]; then
        log_success "First and Second have distinct DID URIs"
        ((TESTS_PASSED++))
    else
        log_error "First and Second have the same DID URI (should be different)"
        ((TESTS_FAILED++))
    fi

    log_info "First DID:  $first_did"
    log_info "Second DID: $second_did"

    # Verify the public keys are different
    local first_x
    first_x=$(curl -s "http://localhost:$PORT_FIRST/.well-known/did.json" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(d['verificationMethod'][0]['publicKeyJwk']['x'])
" 2>/dev/null || echo "")

    local second_x
    second_x=$(curl -s "http://localhost:$PORT_SECOND/.well-known/did.json" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(d['verificationMethod'][0]['publicKeyJwk']['x'])
" 2>/dev/null || echo "")

    if [ "$first_x" != "$second_x" ]; then
        log_success "First and Second have distinct public keys"
        ((TESTS_PASSED++))
    else
        log_error "First and Second have the same public key (should be different)"
        ((TESTS_FAILED++))
    fi
}

# ============================================================
# Run all steps
# ============================================================
step_start_second || exit 1
run_test "Fetch Second's DID" step_fetch_second_did
run_test "Create First Config with Second's DID" step_create_first_config
step_start_first || exit 1
run_test "Push Voucher to First" step_push_voucher_to_first
run_test "Verify DID-based Push to Second" step_verify_did_push
run_test "PullAuth Handshake (Type-5 Auth)" step_pullauth_handshake
run_test "Verify Both DID Documents" step_verify_both_dids

print_summary
