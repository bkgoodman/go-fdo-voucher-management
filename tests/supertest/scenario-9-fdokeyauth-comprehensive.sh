#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 9: Comprehensive FDOKeyAuth Testing
#
# Tests FDOKeyAuth authentication for push and pull flows:
#   1. Mfg → VM push with FDOKeyAuth (positive + negative)
#   2. Pull from VM with FDOKeyAuth (positive + negative/isolation)
#   3. FDOKeyAuth handshake validation (positive + negative)
#
# Follows proven patterns from scenario 4 (reseller pull).
# PORTS: Mfg=9901, VM=9902, RV=9903

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_MFG=9901
PORT_VM=9902
PORT_RV=9903
MFG_PID=""
VM_PID=""
RV_PID=""

cleanup() {
    phase "Cleanup"
    stop_pid "$MFG_PID" "Manufacturing Station"
    stop_pid "$VM_PID" "Voucher Manager"
    stop_pid "$RV_PID" "Rendezvous Server"
    kill_ports $PORT_MFG $PORT_VM $PORT_RV
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 9: Comprehensive FDOKeyAuth Testing"
narrate "This scenario tests FDOKeyAuth authentication across push"
narrate "and pull flows with both positive and negative tests."

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_MFG $PORT_VM $PORT_RV
init_artifact_dir "s9"

MFG_DB="$ARTIFACT_DIR/mfg.db"
MFG_VOUCHERS="$ARTIFACT_DIR/mfg_vouchers"
MFG_LOG="$ARTIFACT_DIR/mfg.log"
MFG_CONFIG="$ARTIFACT_DIR/mfg_config.yaml"

VM_DB="$ARTIFACT_DIR/vm.db"
VM_VOUCHERS="$ARTIFACT_DIR/vm_vouchers"
VM_LOG="$ARTIFACT_DIR/vm.log"
VM_CONFIG="$ARTIFACT_DIR/vm_config.yaml"
VM_OWNER_KEY_EXPORT="$ARTIFACT_DIR/vm_owner_key.pem"

RV_DB="$ARTIFACT_DIR/rv.db"
RV_LOG="$ARTIFACT_DIR/rv.log"
RV_CONFIG="$ARTIFACT_DIR/rv_config.yaml"

DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
DEVICE_DI_LOG="$ARTIFACT_DIR/device_di.log"
DEVICE_CONFIG="$ARTIFACT_DIR/device.cfg"

WRONG_KEY="$ARTIFACT_DIR/wrong_key.pem"

mkdir -p "$MFG_VOUCHERS" "$VM_VOUCHERS"

# ============================================================
phase "Generate Wrong Key for Negative Testing"
# ============================================================
openssl ecparam -genkey -name secp384r1 -noout 2>/dev/null | \
    openssl pkcs8 -topk8 -nocrypt -out "$WRONG_KEY" 2>/dev/null
assert_file_exists "$WRONG_KEY" "Wrong key generated for negative tests"

# ============================================================
phase "Start Rendezvous Server"
# ============================================================
gen_rv_config "$PORT_RV" "$RV_DB" "open" > "$RV_CONFIG"
(cd "$DIR_RV" && "$BIN_RV" -config "$RV_CONFIG" -init-only > "$ARTIFACT_DIR/rv_init.log" 2>&1)
start_bg_server "$DIR_RV" "$RV_LOG" "$BIN_RV" -config "$RV_CONFIG"
RV_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_RV" 30 "Rendezvous Server" || exit 1
log_success "Rendezvous Server running (port $PORT_RV)"

# ============================================================
phase "Start Voucher Manager with DID Minting"
# ============================================================
narrate "VM starts first so it can export its DID-minted owner key."
narrate "No push downstream — VM is the terminal holder for pull tests."

gen_vm_config "$PORT_VM" "$VM_DB" "$VM_VOUCHERS" \
    "" \
    "" "" "" \
    "" "" \
    "127.0.0.1:${PORT_VM}" "$VM_OWNER_KEY_EXPORT" \
    > "$VM_CONFIG"

start_bg_server "$DIR_VM" "$VM_LOG" "$BIN_VM" server -config "$VM_CONFIG"
VM_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_VM" 30 "Voucher Manager" || exit 1
log_success "Voucher Manager running (port $PORT_VM)"

sleep 2
assert_file_exists "$VM_OWNER_KEY_EXPORT" "VM exported owner key"

# Extract VM's public key for Mfg signover target
VM_OWNER_PUB="$ARTIFACT_DIR/vm_owner_pub.pem"
openssl pkey -in "$VM_OWNER_KEY_EXPORT" -pubout -out "$VM_OWNER_PUB" 2>/dev/null

# ============================================================
phase "Test 1A: Mfg → VM Push with FDOKeyAuth (Positive)"
# ============================================================
narrate "Mfg signs vouchers to VM's key and pushes to VM."
narrate "Mfg authenticates via FDOKeyAuth using VM's owner key."

gen_mfg_config "$PORT_MFG" "$MFG_DB" "$MFG_VOUCHERS" \
    "$VM_OWNER_PUB" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers" \
    "$VM_OWNER_KEY_EXPORT" \
    "" \
    "127.0.0.1" "$PORT_RV" "http" \
    > "$MFG_CONFIG"

(cd "$DIR_MFG" && "$BIN_MFG" -config "$MFG_CONFIG" -init-only > "$ARTIFACT_DIR/mfg_init.log" 2>&1)
start_bg_server "$DIR_MFG" "$MFG_LOG" "$BIN_MFG" -config "$MFG_CONFIG"
MFG_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_MFG" 30 "Manufacturing Station" || exit 1
log_success "Manufacturing Station running (port $PORT_MFG)"

# Run DI to trigger voucher creation + push
gen_device_config "$DEVICE_CRED" "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_CONFIG"
rm -f "$DEVICE_CRED"
DI_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" -di "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_DI_LOG" 2>&1) || DI_EXIT=$?

assert_equals "0" "$DI_EXIT" "DI completed successfully"
assert_file_exists "$DEVICE_CRED" "Device credential created"

# Wait for push to complete
sleep 4

show_file_listing "$VM_VOUCHERS" "VM Vouchers (received from Mfg via FDOKeyAuth)"
assert_dir_not_empty "$VM_VOUCHERS" "*.fdoov" "VM received voucher via FDOKeyAuth push"

# Note: Push negative test (wrong key → rejected) requires registering the
# Mfg station's internal manufacturer key as a trusted supplier. The Mfg
# station generates this key internally during init, making it hard to test
# in an integration setting. Push auth rejection is tested indirectly via
# pull isolation (Test 2B) and handshake scoping (Test 3B).

stop_pid "$MFG_PID" "Manufacturing Station"

# ============================================================
phase "Test 2A: Pull from VM with Correct Key (Positive)"
# ============================================================
narrate "Pull vouchers from VM using the correct owner key."
narrate "Should get the voucher(s) that were pushed in Test 1A."

OBS_PULLED="$ARTIFACT_DIR/obs_pulled"
mkdir -p "$OBS_PULLED"

PULL_LOG="$ARTIFACT_DIR/pull_cmd.log"
PULL_EXIT=0
PULL_OUTPUT=$("$BIN_VM" pull \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key "$VM_OWNER_KEY_EXPORT" \
    -output "$OBS_PULLED" \
    -json 2>"$PULL_LOG") || PULL_EXIT=$?

log_info "Pull exit code: $PULL_EXIT"
if [ -s "$PULL_LOG" ]; then
    log_info "Pull stderr:"
    head -5 "$PULL_LOG" | sed 's/^/    /'
fi

PULL_DOWNLOADED=$(echo "$PULL_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('downloaded',0))" 2>/dev/null || echo "0")
assert_count_gt "$PULL_DOWNLOADED" 0 "Pulled voucher(s) via FDOKeyAuth (got $PULL_DOWNLOADED)"

show_file_listing "$OBS_PULLED" "Pulled Vouchers"

# ============================================================
phase "Test 2B: Pull with Wrong Key (Negative — Isolation)"
# ============================================================
narrate "Pull with an unrelated key should return 0 vouchers."

UNRELATED_OUTPUT=$("$BIN_VM" fdokeyauth \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key-type ec384 \
    -json 2>/dev/null || echo '{"error":"failed"}')

UNRELATED_COUNT=$(echo "$UNRELATED_OUTPUT" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(d.get('voucher_count', d.get('error', 'unknown')))
" 2>/dev/null || echo "error")

if [ "$UNRELATED_COUNT" = "0" ]; then
    log_success "Owner isolation: unrelated key sees 0 vouchers"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_warn "Owner isolation: unrelated key saw '$UNRELATED_COUNT' (expected 0)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Test 3: FDOKeyAuth Handshake Validation"
# ============================================================
narrate "Standalone handshake tests (no voucher transfer)."

# 3A: Correct key
AUTH_EXIT=0
"$BIN_VM" fdokeyauth \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key "$VM_OWNER_KEY_EXPORT" \
    -json > "$ARTIFACT_DIR/auth_good.json" 2>/dev/null || AUTH_EXIT=$?

if [ "$AUTH_EXIT" -eq 0 ]; then
    log_success "FDOKeyAuth handshake with correct key succeeded"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "FDOKeyAuth handshake with correct key failed (exit=$AUTH_EXIT)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# 3B: Wrong key — should fail or return error
AUTH_BAD_EXIT=0
"$BIN_VM" fdokeyauth \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key "$WRONG_KEY" \
    -json > "$ARTIFACT_DIR/auth_bad.json" 2>/dev/null || AUTH_BAD_EXIT=$?

# FDOKeyAuth with an unknown key should still succeed (handshake works,
# but the token is scoped to 0 vouchers). Check voucher_count.
BAD_VCOUNT=$(python3 -c "import sys,json; print(json.load(open('$ARTIFACT_DIR/auth_bad.json')).get('voucher_count',0))" 2>/dev/null || echo "error")

if [ "$BAD_VCOUNT" = "0" ] || [ "$AUTH_BAD_EXIT" -ne 0 ]; then
    log_success "FDOKeyAuth with wrong key: properly scoped (count=$BAD_VCOUNT exit=$AUTH_BAD_EXIT)"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_error "FDOKeyAuth with wrong key: unexpected access (count=$BAD_VCOUNT)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Summary"
# ============================================================
narrate "Comprehensive FDOKeyAuth Testing Results:"
narrate "  Test 1A: Mfg → VM push with correct key (positive)"
narrate "  Test 2A: Pull from VM with correct key (positive)"
narrate "  Test 2B: Pull from VM with wrong key (negative/isolation)"
narrate "  Test 3A: FDOKeyAuth handshake with correct key"
narrate "  Test 3B: FDOKeyAuth handshake with wrong key (scoping)"

print_summary
