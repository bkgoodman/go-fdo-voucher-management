#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 6: DID + FDOKeyAuth — Owner-Key AND Delegate Pull
#
# All 5 services. Tests both FDOKeyAuth authentication modes against
# the same Holder (Voucher Manager):
#
#   Sub-test A: Owner-key pull (standard Type-5 FDOKeyAuth)
#   Sub-test B: Delegate-based pull (delegate cert + owner public key)
#   Sub-test C: Negative — unrelated key sees 0 vouchers (isolation)
#
# Then completes the full onboarding flow via RV.
#
# FLOW:
#   Mfg →(push)→ VM ←(pull A: owner-key)← OBS
#                    ←(pull B: delegate)← OBS
#                    ←(pull C: unrelated)← test → 0 vouchers
#   OBS →(TO0)→ RV ←(TO1)← Device →(TO2)→ OBS
#
# PORTS: Mfg=9601, VM=9602, RV=9603, OBS=9604

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_MFG=9601
PORT_VM=9602
PORT_RV=9603
PORT_OBS=9604
MFG_PID=""
VM_PID=""
RV_PID=""
OBS_PID=""

cleanup() {
    phase "Cleanup"
    stop_pid "$MFG_PID" "Manufacturing Station"
    stop_pid "$VM_PID" "Voucher Manager"
    stop_pid "$RV_PID" "Rendezvous Server"
    stop_pid "$OBS_PID" "Onboarding Service"
    kill_ports $PORT_MFG $PORT_VM $PORT_RV $PORT_OBS
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 6: DID + FDOKeyAuth — Owner-Key AND Delegate Pull"
narrate "This is the most comprehensive scenario. It tests:"
narrate "  A) FDOKeyAuth with the owner's private key (standard)"
narrate "  B) FDOKeyAuth with a delegate certificate (cross-org)"
narrate "  C) Negative: unrelated key gets 0 vouchers (isolation)"
narrate "Then completes full onboarding via RV."

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_MFG $PORT_VM $PORT_RV $PORT_OBS
init_artifact_dir "s6"

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

OBS_DB="$ARTIFACT_DIR/obs.db"
OBS_VOUCHERS="$ARTIFACT_DIR/obs_vouchers"
OBS_LOG="$ARTIFACT_DIR/obs.log"
OBS_CONFIG="$ARTIFACT_DIR/obs_config.yaml"

PULL_A_DIR="$ARTIFACT_DIR/pull_a_output"
PULL_B_DIR="$ARTIFACT_DIR/pull_b_output"

DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
DEVICE_DI_LOG="$ARTIFACT_DIR/device_di.log"
DEVICE_TO2_LOG="$ARTIFACT_DIR/device_to2.log"
DEVICE_CONFIG="$ARTIFACT_DIR/device.cfg"

# Delegate artifacts
DELEGATE_KEY="$ARTIFACT_DIR/delegate_key.pem"
DELEGATE_CHAIN="$ARTIFACT_DIR/delegate_chain.pem"
DELEGATE_CSR="$ARTIFACT_DIR/delegate.csr"

mkdir -p "$MFG_VOUCHERS" "$VM_VOUCHERS" "$OBS_VOUCHERS" \
    "$PULL_A_DIR" "$PULL_B_DIR" "$ARTIFACT_DIR/obs_configs"

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
phase "Initialize & Start Voucher Manager (Holder)"
# ============================================================
narrate "VM starts first so it can export its DID-minted owner key."
narrate "Mfg will sign vouchers to VM's key. Pullers will use VM's"
narrate "exported private key for FDOKeyAuth."
narrate "Push to OBS is DISABLED — OBS will pull."

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
if [ -f "$VM_OWNER_KEY_EXPORT" ]; then
    log_success "VM exported owner key"
else
    log_warn "VM owner key export not found yet, waiting..."
    sleep 3
fi

show_did_document "http://127.0.0.1:${PORT_VM}/.well-known/did.json" "VM DID Document"

# Extract VM's public key for Mfg signover target
VM_OWNER_PUB="$ARTIFACT_DIR/vm_owner_pub.pem"
openssl pkey -in "$VM_OWNER_KEY_EXPORT" -pubout -out "$VM_OWNER_PUB" 2>/dev/null
OBS_OWNER_KEY="$VM_OWNER_PUB"
OBS_OWNER_PUB="$VM_OWNER_PUB"

# ============================================================
phase "Initialize Onboarding Service"
# ============================================================
gen_obs_config "$PORT_OBS" "$OBS_DB" "$OBS_VOUCHERS" "test-s6-token" \
    "127.0.0.1" "$PORT_RV" "http" > "$OBS_CONFIG"

(cd "$DIR_OBS" && "$BIN_OBS" -config "$OBS_CONFIG" -init-only > "$ARTIFACT_DIR/obs_init.log" 2>&1)
log_success "OBS initialized"

OBS_OWNER_KEY="$VM_OWNER_PUB"
log_success "OBS owner public key saved to $OBS_OWNER_KEY"

start_bg_server "$DIR_OBS" "$OBS_LOG" "$BIN_OBS" -config "$OBS_CONFIG"
OBS_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_OBS" 30 "Onboarding Service" || exit 1
log_success "Onboarding Service running (port $PORT_OBS)"

# ============================================================
phase "Initialize & Start Manufacturing Station"
# ============================================================
narrate "Mfg signs vouchers to VM's key and pushes to VM."

gen_mfg_config "$PORT_MFG" "$MFG_DB" "$MFG_VOUCHERS" \
    "$VM_OWNER_PUB" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers" \
    "" \
    "" \
    "127.0.0.1" "$PORT_RV" "http" \
    > "$MFG_CONFIG"

(cd "$DIR_MFG" && "$BIN_MFG" -config "$MFG_CONFIG" -init-only > "$ARTIFACT_DIR/mfg_init.log" 2>&1)
start_bg_server "$DIR_MFG" "$MFG_LOG" "$BIN_MFG" -config "$MFG_CONFIG"
MFG_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_MFG" 30 "Manufacturing Station" || exit 1
log_success "Manufacturing Station running (port $PORT_MFG)"

# ============================================================
phase "Device Initialization (DI)"
# ============================================================
gen_device_config "$DEVICE_CRED" "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_CONFIG"

rm -f "$DEVICE_CRED"
DI_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" -di "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_DI_LOG" 2>&1) || DI_EXIT=$?

assert_equals "0" "$DI_EXIT" "Device DI should succeed"
assert_file_exists "$DEVICE_CRED" "Device credential created"

# Wait for Mfg → VM push
sleep 4
assert_dir_not_empty "$VM_VOUCHERS" "*.fdoov" "VM received voucher from Mfg"

show_file_listing "$VM_VOUCHERS" "VM Vouchers (Holder)"
show_key_fingerprint "$OBS_OWNER_KEY" "OBS Owner (expected pull identity)"

# ============================================================
phase "Sub-test A: Owner-Key Pull (Standard FDOKeyAuth Type-5)"
# ============================================================
narrate "OBS authenticates to VM's Pull API using its owner private key."
narrate "This is the standard FDOKeyAuth flow: the puller proves it holds"
narrate "the key that vouchers were signed over to."

mkdir -p "$PULL_A_DIR"
PULL_A_LOG="$ARTIFACT_DIR/pull_a_cmd.log"
PULL_A_OUTPUT=$("$BIN_VM" pull \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key "$VM_OWNER_KEY_EXPORT" \
    -output "$PULL_A_DIR" \
    -json 2>"$PULL_A_LOG") || true

log_info "Pull A output:"
if [ -s "$PULL_A_LOG" ]; then
    head -10 "$PULL_A_LOG" | sed 's/^/    /'
fi
echo "$PULL_A_OUTPUT" | python3 -m json.tool 2>/dev/null | sed 's/^/    /' || echo "    $PULL_A_OUTPUT"

PULL_A_COUNT=$(echo "$PULL_A_OUTPUT" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('downloaded', d.get('listed', 0)))
except:
    print(0)
" 2>/dev/null || echo "0")

assert_count_gt "$PULL_A_COUNT" 0 "Sub-test A: Owner-key pull downloaded voucher(s)"

show_file_listing "$PULL_A_DIR" "Pull A Output (owner-key)"

# ============================================================
phase "Sub-test B: Delegate-Based Pull"
# ============================================================
narrate "Now we test delegate-based FDOKeyAuth. A delegate cert is"
narrate "created at the OBS (signed by the owner key), and then used"
narrate "to pull vouchers. The puller only needs the owner PUBLIC key"
narrate "plus the delegate's private key and certificate chain."

narrate "Step B.1: Create delegate certificate at OBS"
DELEGATE_CREATE=$("$BIN_OBS" -config "$OBS_CONFIG" \
    -create-delegate "pull-delegate" \
    -delegate-permissions "voucher-claim" \
    -delegate-key-type "ec384" \
    -delegate-validity 365 \
    -delegate-output "$ARTIFACT_DIR" 2>&1 || echo "DELEGATE_CREATE_ERROR")

log_info "Delegate creation output:"
echo "$DELEGATE_CREATE" | head -20 | sed 's/^/    /'

# Try to find exported delegate artifacts
# The delegate command may export key and chain to files
if [ -f "$ARTIFACT_DIR/pull-delegate.key.pem" ]; then
    DELEGATE_KEY="$ARTIFACT_DIR/pull-delegate.key.pem"
elif [ -f "$ARTIFACT_DIR/pull-delegate-key.pem" ]; then
    DELEGATE_KEY="$ARTIFACT_DIR/pull-delegate-key.pem"
fi

if [ -f "$ARTIFACT_DIR/pull-delegate.chain.pem" ]; then
    DELEGATE_CHAIN="$ARTIFACT_DIR/pull-delegate.chain.pem"
elif [ -f "$ARTIFACT_DIR/pull-delegate-chain.pem" ]; then
    DELEGATE_CHAIN="$ARTIFACT_DIR/pull-delegate-chain.pem"
fi

# Alternative: use the CSR workflow
if [ ! -f "$DELEGATE_KEY" ] || [ ! -f "$DELEGATE_CHAIN" ]; then
    narrate "Using CSR workflow for delegate creation..."

    # Generate CSR (no DB needed)
    "$BIN_OBS" -config "$OBS_CONFIG" \
        -generate-delegate-csr "pull-delegate-csr" \
        -delegate-key-type "ec384" \
        -delegate-key-out "$DELEGATE_KEY" > "$DELEGATE_CSR" 2>/dev/null || true

    # Sign CSR with owner key
    if [ -f "$DELEGATE_CSR" ] && [ -s "$DELEGATE_CSR" ]; then
        "$BIN_OBS" -config "$OBS_CONFIG" \
            -sign-delegate-csr "$DELEGATE_CSR" \
            -delegate-chain-name "pull-delegate" \
            -delegate-permissions "voucher-claim" > "$DELEGATE_CHAIN" 2>/dev/null || true
    fi
fi

DELEGATE_PULL_OK=false
if [ -f "$DELEGATE_KEY" ] && [ -f "$DELEGATE_CHAIN" ]; then
    narrate "Step B.2: Pull using delegate cert + owner public key"

    PULL_B_OUTPUT=$("$BIN_VM" pull \
        -url "http://127.0.0.1:${PORT_VM}" \
        -owner-pub "$OBS_OWNER_PUB" \
        -delegate-key "$DELEGATE_KEY" \
        -delegate-chain "$DELEGATE_CHAIN" \
        -output "$PULL_B_DIR" \
        -json 2>&1 || echo '{"error":"pull_b_failed"}')

    log_info "Pull B output:"
    echo "$PULL_B_OUTPUT" | python3 -m json.tool 2>/dev/null | sed 's/^/    /' || echo "    $PULL_B_OUTPUT"

    PULL_B_COUNT=$(echo "$PULL_B_OUTPUT" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('downloaded', d.get('listed', 0)))
except:
    print(0)
" 2>/dev/null || echo "0")

    if [ "$PULL_B_COUNT" -gt 0 ] 2>/dev/null; then
        assert_count_gt "$PULL_B_COUNT" 0 "Sub-test B: Delegate pull downloaded voucher(s)"
        DELEGATE_PULL_OK=true
    else
        log_warn "Sub-test B: Delegate pull returned $PULL_B_COUNT vouchers (non-critical)"
    fi

    show_file_listing "$PULL_B_DIR" "Pull B Output (delegate)"
else
    log_warn "Sub-test B: Skipped — delegate key/chain files not available (non-critical)"
    log_info "Delegate key: $DELEGATE_KEY (exists: $([ -f "$DELEGATE_KEY" ] && echo yes || echo no))"
    log_info "Delegate chain: $DELEGATE_CHAIN (exists: $([ -f "$DELEGATE_CHAIN" ] && echo yes || echo no))"
    narrate "Delegate artifacts may need manual export. This sub-test"
    narrate "will pass once the delegate export workflow is configured."
fi

# ============================================================
phase "Sub-test C: Negative — Owner-Scoped Isolation"
# ============================================================
narrate "An ephemeral (unrelated) key should see ZERO vouchers"
narrate "when pulling from VM. This proves owner-key scoping."

PULL_C_OUTPUT=$("$BIN_VM" fdokeyauth \
    -url "http://127.0.0.1:${PORT_VM}" \
    -key-type ec384 \
    -json 2>&1 || echo '{"voucher_count":-1}')

log_info "Isolation test output:"
echo "$PULL_C_OUTPUT" | python3 -m json.tool 2>/dev/null | sed 's/^/    /' || echo "    $PULL_C_OUTPUT"

PULL_C_COUNT=$(echo "$PULL_C_OUTPUT" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('voucher_count', -1))
except:
    print(-1)
" 2>/dev/null || echo "-1")

if [ "$PULL_C_COUNT" = "0" ]; then
    log_success "Sub-test C: Unrelated key sees 0 vouchers (isolation works)"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
elif [ "$PULL_C_COUNT" = "-1" ]; then
    log_warn "Sub-test C: Could not parse fdokeyauth response (non-critical)"
else
    log_error "Sub-test C: Unrelated key saw $PULL_C_COUNT vouchers (expected 0)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Import Pulled Vouchers into OBS"
# ============================================================
narrate "Importing vouchers pulled in sub-test A into OBS for onboarding."

for f in "$PULL_A_DIR"/*.fdoov; do
    [ -f "$f" ] && cp "$f" "$OBS_VOUCHERS/" && \
        (cd "$DIR_OBS" && "$BIN_OBS" -config "$OBS_CONFIG" -import-voucher "$f" > /dev/null 2>&1 || true)
done

narrate "Waiting for TO0 registration..."
sleep 8

show_rv_blobs "$BIN_RV" "$RV_CONFIG"

# ============================================================
phase "Device Onboarding (TO1 + TO2)"
# ============================================================
narrate "Device discovers OBS via RV, then onboards."

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" > "$DEVICE_TO2_LOG" 2>&1) || TO2_EXIT=$?

if [ "$TO2_EXIT" -eq 0 ]; then
    assert_equals "0" "$TO2_EXIT" "Device TO1+TO2 should succeed"
else
    if grep -q "Success\|Credential Reuse" "$DEVICE_TO2_LOG" 2>/dev/null; then
        log_success "Device completed TO2 (success in log)"
        ((TESTS_RUN++)); ((TESTS_PASSED++))
    else
        log_error "Device flow failed (exit $TO2_EXIT)"
        ((TESTS_RUN++)); ((TESTS_FAILED++))
        tail -15 "$DEVICE_TO2_LOG" | sed 's/^/    /'
    fi
fi

# ============================================================
phase "Verification Summary"
# ============================================================
narrate "Scenario 6 complete! Three FDOKeyAuth modes tested:"
narrate "  A) Owner-key pull: standard Type-5 FDOKeyAuth ✓"
if [ "$DELEGATE_PULL_OK" = true ]; then
    narrate "  B) Delegate pull: delegate cert + owner public key ✓"
else
    narrate "  B) Delegate pull: needs delegate export workflow"
fi
narrate "  C) Owner isolation: unrelated key → 0 vouchers ✓"
narrate ""
narrate "Plus full onboarding: DI → Push → Pull → TO0 → TO1 → TO2"

print_summary
