#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 11: Voucher Status & Assign API Testing
#
# Tests the voucher status and assign API endpoints:
#   1. Status query by GUID
#   2. Status query by serial number
#   3. Status 404 for non-existent GUID
#   4. Unauthenticated status request (401)
#   5. Assign voucher to a new owner key
#   6. Status reflects assigned state
#   7. Double assign rejection (already_assigned)
#
# PORTS: VM=9903

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_VM=9903
VM_PID=""

cleanup() {
    phase "Cleanup"
    stop_pid "$VM_PID" "Voucher Manager"
    kill_ports $PORT_VM
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 11: Voucher Status & Assign API Testing"
narrate "Tests status queries, assignment, and error handling."

# ============================================================
phase "Setup: Build VM, Start Server, Generate & Push Voucher"
# ============================================================
kill_ports $PORT_VM
init_artifact_dir "s11"

VM_DB="$ARTIFACT_DIR/vm.db"
VM_VOUCHERS="$ARTIFACT_DIR/vm_vouchers"
VM_LOG="$ARTIFACT_DIR/vm.log"
VM_CONFIG="$ARTIFACT_DIR/vm_config.yaml"

mkdir -p "$VM_VOUCHERS"

build_binary "$DIR_VM" "$BIN_VM" "Voucher Manager" || exit 1

# Write config with global_token auth and DID minting enabled
cat > "$VM_CONFIG" << EOF
debug: true

server:
  addr: "127.0.0.1:${PORT_VM}"
  ext_addr: "127.0.0.1:${PORT_VM}"
  use_tls: false

database:
  path: "$VM_DB"
  password: ""

key_management:
  key_type: "ec384"
  first_time_init: true

voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"
  global_token: "test-token-s11"
  validate_ownership: false
  require_auth: true

voucher_signing:
  mode: "internal"

voucher_files:
  directory: "$VM_VOUCHERS"

did_minting:
  enabled: true
  host: "127.0.0.1:${PORT_VM}"
  key_type: "ec384"
  serve_document: true
  first_time_init: true

pull_service:
  enabled: true
  token_ttl: 10m
  session_ttl: 30m

push_service:
  enabled: false

retention:
  keep_indefinitely: true
EOF

start_bg_server "$DIR_VM" "$VM_LOG" "$BIN_VM" server -config "$VM_CONFIG"
VM_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_VM" 30 "Voucher Manager" || exit 1
log_success "Voucher Manager running (port $PORT_VM) with global_token auth"

sleep 2

# Generate a test voucher PEM
VOUCHER_PEM="$ARTIFACT_DIR/test_voucher.pem"
TEST_SERIAL="TEST-SERIAL-S11"
TEST_MODEL="TEST-MODEL"

log_info "Generating test voucher (serial=$TEST_SERIAL)..."
"$BIN_VM" generate voucher -serial "$TEST_SERIAL" -model "$TEST_MODEL" -output "$VOUCHER_PEM"
assert_file_exists "$VOUCHER_PEM" "Test voucher PEM generated"

# Push the voucher to the receiver endpoint via multipart form
log_info "Pushing voucher to receiver endpoint..."
PUSH_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer test-token-s11" \
    -F "voucher=@${VOUCHER_PEM}" \
    -F "serial=${TEST_SERIAL}" \
    -F "model=${TEST_MODEL}" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers" 2>/dev/null)

PUSH_BODY=$(echo "$PUSH_RESPONSE" | sed '$d')
PUSH_HTTP=$(echo "$PUSH_RESPONSE" | tail -1)

assert_equals "200" "$PUSH_HTTP" "Voucher push accepted (HTTP 200)"

# Extract the GUID from the push response
VOUCHER_GUID=$(echo "$PUSH_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('voucher_id',''))" 2>/dev/null || echo "")
assert_not_empty "$VOUCHER_GUID" "Received voucher GUID from push response"
log_info "Voucher GUID: $VOUCHER_GUID"

# Allow pipeline processing to complete
sleep 1

# ============================================================
phase "Test 1: Status by GUID"
# ============================================================
narrate "Query voucher status using the GUID returned from the push."

STATUS_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer test-token-s11" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers/status/${VOUCHER_GUID}" 2>/dev/null)

STATUS_BODY=$(echo "$STATUS_RESPONSE" | sed '$d')
STATUS_HTTP=$(echo "$STATUS_RESPONSE" | tail -1)

assert_equals "200" "$STATUS_HTTP" "Status by GUID returns HTTP 200"

STATUS_VID=$(echo "$STATUS_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('voucher_id',''))" 2>/dev/null || echo "")
STATUS_SERIAL=$(echo "$STATUS_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('serial',''))" 2>/dev/null || echo "")
STATUS_STATUS=$(echo "$STATUS_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null || echo "")

assert_not_empty "$STATUS_VID" "Response contains voucher_id"
assert_equals "$TEST_SERIAL" "$STATUS_SERIAL" "Response serial matches pushed serial"
assert_not_empty "$STATUS_STATUS" "Response contains status field"
log_info "Status response: voucher_id=$STATUS_VID, serial=$STATUS_SERIAL, status=$STATUS_STATUS"

# ============================================================
phase "Test 2: Status by Serial"
# ============================================================
narrate "Query voucher status using the serial number with ?type=serial."

SERIAL_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer test-token-s11" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers/status/${TEST_SERIAL}?type=serial" 2>/dev/null)

SERIAL_BODY=$(echo "$SERIAL_RESPONSE" | sed '$d')
SERIAL_HTTP=$(echo "$SERIAL_RESPONSE" | tail -1)

assert_equals "200" "$SERIAL_HTTP" "Status by serial returns HTTP 200"

SERIAL_VID=$(echo "$SERIAL_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('voucher_id',''))" 2>/dev/null || echo "")
SERIAL_STATUS=$(echo "$SERIAL_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null || echo "")

assert_equals "$VOUCHER_GUID" "$SERIAL_VID" "Serial lookup returns same voucher_id"
assert_not_empty "$SERIAL_STATUS" "Serial lookup returns status"
log_info "Status by serial: voucher_id=$SERIAL_VID, status=$SERIAL_STATUS"

# ============================================================
phase "Test 3: Status Not Found"
# ============================================================
narrate "Query status for a non-existent GUID, expect HTTP 404."

FAKE_GUID="00000000-0000-0000-0000-000000000000"
NOTFOUND_HTTP=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer test-token-s11" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers/status/${FAKE_GUID}" 2>/dev/null)

assert_equals "404" "$NOTFOUND_HTTP" "Non-existent GUID returns HTTP 404"

# ============================================================
phase "Test 4: Unauthenticated Status"
# ============================================================
narrate "Query status without an auth token, expect HTTP 401."

UNAUTH_HTTP=$(curl -s -o /dev/null -w "%{http_code}" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers/status/${VOUCHER_GUID}" 2>/dev/null)

assert_equals "401" "$UNAUTH_HTTP" "Unauthenticated request returns HTTP 401"

# ============================================================
phase "Test 5: Assign Voucher"
# ============================================================
narrate "Generate a new EC P-384 key pair and assign the voucher."

# Generate new owner key
openssl ecparam -genkey -name secp384r1 -noout 2>/dev/null | openssl ec -outform PEM 2>/dev/null > "$ARTIFACT_DIR/new_owner_key.pem"
NEW_OWNER_PUB=$(openssl ec -in "$ARTIFACT_DIR/new_owner_key.pem" -pubout 2>/dev/null)

assert_not_empty "$NEW_OWNER_PUB" "New owner EC P-384 public key generated"
log_info "New owner public key generated (${#NEW_OWNER_PUB} chars)"

# Build the assign JSON payload
ASSIGN_PAYLOAD=$(python3 -c "
import json
print(json.dumps({
    'serials': ['$TEST_SERIAL'],
    'new_owner_key': '''$NEW_OWNER_PUB'''
}))
" 2>/dev/null)

ASSIGN_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer test-token-s11" \
    -H "Content-Type: application/json" \
    -d "$ASSIGN_PAYLOAD" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers/assign" 2>/dev/null)

ASSIGN_BODY=$(echo "$ASSIGN_RESPONSE" | sed '$d')
ASSIGN_HTTP=$(echo "$ASSIGN_RESPONSE" | tail -1)

assert_equals "200" "$ASSIGN_HTTP" "Assign returns HTTP 200"

# Check per-voucher status in results array
ASSIGN_STATUS=$(echo "$ASSIGN_BODY" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r['results'][0]['status'])" 2>/dev/null || echo "")
ASSIGN_VID=$(echo "$ASSIGN_BODY" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r['results'][0].get('voucher_id',''))" 2>/dev/null || echo "")

assert_equals "ok" "$ASSIGN_STATUS" "Per-voucher assign status is 'ok'"
assert_not_empty "$ASSIGN_VID" "Per-voucher result contains voucher_id"
log_info "Assign result: status=$ASSIGN_STATUS, voucher_id=$ASSIGN_VID"

# ============================================================
phase "Test 6: Status Shows Assigned"
# ============================================================
narrate "After assignment, status should reflect 'assigned' with fingerprint."

sleep 1

POST_ASSIGN_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer test-token-s11" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers/status/${VOUCHER_GUID}" 2>/dev/null)

POST_ASSIGN_BODY=$(echo "$POST_ASSIGN_RESPONSE" | sed '$d')
POST_ASSIGN_HTTP=$(echo "$POST_ASSIGN_RESPONSE" | tail -1)

assert_equals "200" "$POST_ASSIGN_HTTP" "Post-assign status returns HTTP 200"

POST_STATUS=$(echo "$POST_ASSIGN_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null || echo "")
POST_FINGERPRINT=$(echo "$POST_ASSIGN_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('assigned_to_fingerprint',''))" 2>/dev/null || echo "")

assert_equals "assigned" "$POST_STATUS" "Status is 'assigned' after assignment"
assert_not_empty "$POST_FINGERPRINT" "assigned_to_fingerprint is non-empty"
log_info "Post-assign status: status=$POST_STATUS, assigned_to_fingerprint=$POST_FINGERPRINT"

# ============================================================
phase "Test 7: Double Assign Rejected"
# ============================================================
narrate "Attempting to assign the same voucher again should fail with 'already_assigned'."

# Generate a different new owner key for the second attempt
openssl ecparam -genkey -name secp384r1 -noout 2>/dev/null | openssl ec -outform PEM 2>/dev/null > "$ARTIFACT_DIR/new_owner_key2.pem"
NEW_OWNER_PUB2=$(openssl ec -in "$ARTIFACT_DIR/new_owner_key2.pem" -pubout 2>/dev/null)

DOUBLE_PAYLOAD=$(python3 -c "
import json
print(json.dumps({
    'serials': ['$TEST_SERIAL'],
    'new_owner_key': '''$NEW_OWNER_PUB2'''
}))
" 2>/dev/null)

DOUBLE_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer test-token-s11" \
    -H "Content-Type: application/json" \
    -d "$DOUBLE_PAYLOAD" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers/assign" 2>/dev/null)

DOUBLE_BODY=$(echo "$DOUBLE_RESPONSE" | sed '$d')
DOUBLE_HTTP=$(echo "$DOUBLE_RESPONSE" | tail -1)

# The endpoint returns 200 with per-voucher error status
assert_equals "200" "$DOUBLE_HTTP" "Double assign returns HTTP 200 (per-voucher error)"

DOUBLE_STATUS=$(echo "$DOUBLE_BODY" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r['results'][0]['status'])" 2>/dev/null || echo "")
DOUBLE_ERROR=$(echo "$DOUBLE_BODY" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r['results'][0].get('error_code',''))" 2>/dev/null || echo "")

assert_equals "error" "$DOUBLE_STATUS" "Double assign per-voucher status is 'error'"
assert_equals "already_assigned" "$DOUBLE_ERROR" "Double assign error_code is 'already_assigned'"
log_info "Double assign result: status=$DOUBLE_STATUS, error_code=$DOUBLE_ERROR"

# ============================================================
phase "Summary"
# ============================================================
narrate "Voucher Status & Assign Tests:"
narrate "  Test 1: Status by GUID"
narrate "  Test 2: Status by serial"
narrate "  Test 3: Status not found (404)"
narrate "  Test 4: Unauthenticated status (401)"
narrate "  Test 5: Assign voucher to new owner key"
narrate "  Test 6: Status shows assigned state"
narrate "  Test 7: Double assign rejected"

print_summary
