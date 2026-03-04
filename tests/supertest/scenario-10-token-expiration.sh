#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 10: FDOKeyAuth Token Lifecycle Testing
#
# Tests token lifecycle management in FDOKeyAuth:
#   1. Token issuance via FDOKeyAuth handshake
#   2. Token expiration (short TTL)
#   3. New token issuance after expiry
#   4. Invalid/expired token rejection by pull API
#
# Uses the VM's fdokeyauth CLI command for standalone handshake testing.
# PORTS: VM=9902

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_VM=9902
VM_PID=""

cleanup() {
    phase "Cleanup"
    stop_pid "$VM_PID" "Voucher Manager"
    kill_ports $PORT_VM
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 10: FDOKeyAuth Token Lifecycle Testing"
narrate "Tests token issuance, expiration, refresh, and rejection."

# ============================================================
phase "Setup: VM with Short Token TTL"
# ============================================================
kill_ports $PORT_VM
init_artifact_dir "s10"

VM_DB="$ARTIFACT_DIR/vm.db"
VM_VOUCHERS="$ARTIFACT_DIR/vm_vouchers"
VM_LOG="$ARTIFACT_DIR/vm.log"
VM_CONFIG="$ARTIFACT_DIR/vm_config.yaml"
VM_OWNER_KEY_EXPORT="$ARTIFACT_DIR/vm_owner_key.pem"

mkdir -p "$VM_VOUCHERS"

# Use gen_vm_config but override token_ttl via manual config
# gen_vm_config doesn't expose token_ttl, so write config manually
cat > "$VM_CONFIG" << EOF
debug: true

server:
  addr: "127.0.0.1:${PORT_VM}"
  ext_addr: "127.0.0.1:${PORT_VM}"
  use_tls: false

database:
  path: "$VM_DB"
  password: ""

voucher_file_store:
  directory: "$VM_VOUCHERS"
  enabled: true

voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"

pull_service:
  enabled: true
  token_ttl: 10s
  session_ttl: 30s

did_minting:
  enabled: true
  first_time_init: true
  key_export_path: "$VM_OWNER_KEY_EXPORT"

voucher_signing:
  mode: "internal"
  first_time_init: true
EOF

start_bg_server "$DIR_VM" "$VM_LOG" "$BIN_VM" server -config "$VM_CONFIG"
VM_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_VM" 30 "Voucher Manager" || exit 1
log_success "Voucher Manager running (port $PORT_VM) with 10s token TTL"

sleep 2
assert_file_exists "$VM_OWNER_KEY_EXPORT" "VM exported owner key"

# ============================================================
phase "Test 1: Initial Token Issuance"
# ============================================================
narrate "FDOKeyAuth handshake should issue a token with short TTL."

AUTH_EXIT_1=0
AUTH_OUTPUT_1=$("$BIN_VM" fdokeyauth \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key "$VM_OWNER_KEY_EXPORT" \
    -json 2>/dev/null) || AUTH_EXIT_1=$?

if [ "$AUTH_EXIT_1" -eq 0 ]; then
    log_success "Initial FDOKeyAuth handshake succeeded"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "Initial FDOKeyAuth handshake failed (exit=$AUTH_EXIT_1)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# Extract token
TOKEN_1=$(echo "$AUTH_OUTPUT_1" | python3 -c "import sys,json; print(json.load(sys.stdin).get('session_token',''))" 2>/dev/null || echo "")

if [ -n "$TOKEN_1" ]; then
    log_success "Token was issued (${#TOKEN_1} chars)"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "No token in handshake response"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Test 2: Second Handshake Issues New Token"
# ============================================================
narrate "Each CLI handshake is stateless — should get a new token."

AUTH_OUTPUT_2=$("$BIN_VM" fdokeyauth \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key "$VM_OWNER_KEY_EXPORT" \
    -json 2>/dev/null) || true

TOKEN_2=$(echo "$AUTH_OUTPUT_2" | python3 -c "import sys,json; print(json.load(sys.stdin).get('session_token',''))" 2>/dev/null || echo "")

if [ -n "$TOKEN_2" ]; then
    log_success "Second handshake issued token"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "Second handshake failed to issue token"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Test 3: Valid Token Accepted by Pull API"
# ============================================================
narrate "A freshly issued token should work with the pull API."

VALID_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $TOKEN_2" \
    "http://127.0.0.1:${PORT_VM}/api/v1/pull/vouchers" 2>/dev/null)

VALID_HTTP=$(echo "$VALID_RESPONSE" | tail -1)

if [ "$VALID_HTTP" = "200" ]; then
    log_success "Valid token accepted by pull API (HTTP $VALID_HTTP)"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "Valid token rejected by pull API (HTTP $VALID_HTTP)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Test 4: Token Expiration"
# ============================================================
narrate "Waiting 12 seconds for the 10s TTL token to expire..."

show_item "Sleeping 12 seconds..."
sleep 12

EXPIRED_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $TOKEN_2" \
    "http://127.0.0.1:${PORT_VM}/api/v1/pull/vouchers" 2>/dev/null)

EXPIRED_HTTP=$(echo "$EXPIRED_RESPONSE" | tail -1)

if [ "$EXPIRED_HTTP" = "401" ] || [ "$EXPIRED_HTTP" = "403" ]; then
    log_success "Expired token rejected (HTTP $EXPIRED_HTTP)"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "Expired token was NOT rejected (HTTP $EXPIRED_HTTP)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Test 5: Re-authentication After Expiry"
# ============================================================
narrate "A new handshake after expiry should succeed and issue fresh token."

AUTH_EXIT_3=0
AUTH_OUTPUT_3=$("$BIN_VM" fdokeyauth \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key "$VM_OWNER_KEY_EXPORT" \
    -json 2>/dev/null) || AUTH_EXIT_3=$?

TOKEN_3=$(echo "$AUTH_OUTPUT_3" | python3 -c "import sys,json; print(json.load(sys.stdin).get('session_token',''))" 2>/dev/null || echo "")

if [ "$AUTH_EXIT_3" -eq 0 ] && [ -n "$TOKEN_3" ]; then
    log_success "Re-authentication succeeded with fresh token"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "Re-authentication after expiry failed"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# Verify fresh token works
FRESH_HTTP=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $TOKEN_3" \
    "http://127.0.0.1:${PORT_VM}/api/v1/pull/vouchers" 2>/dev/null)

if [ "$FRESH_HTTP" = "200" ]; then
    log_success "Fresh token accepted by pull API (HTTP $FRESH_HTTP)"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "Fresh token rejected (HTTP $FRESH_HTTP)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Test 6: Fabricated Token Rejection"
# ============================================================
narrate "A completely fabricated token should be rejected."

FAKE_TOKEN="fake-token-$(date +%s)-not-valid"
FAKE_HTTP=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $FAKE_TOKEN" \
    "http://127.0.0.1:${PORT_VM}/api/v1/pull/vouchers" 2>/dev/null)

if [ "$FAKE_HTTP" = "401" ] || [ "$FAKE_HTTP" = "403" ]; then
    log_success "Fabricated token rejected (HTTP $FAKE_HTTP)"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "Fabricated token was NOT rejected (HTTP $FAKE_HTTP)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Summary"
# ============================================================
narrate "Token Lifecycle Tests:"
narrate "  Test 1: Initial token issuance"
narrate "  Test 2: Second handshake issues token"
narrate "  Test 3: Valid token accepted by pull API"
narrate "  Test 4: Expired token rejected"
narrate "  Test 5: Re-authentication after expiry"
narrate "  Test 6: Fabricated token rejected"

print_summary
