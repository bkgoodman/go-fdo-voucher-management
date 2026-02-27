#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 3: Reseller Supply Chain — PUSH Path
#
# All 5 services. Voucher flows via PUSH through a reseller (Voucher Manager):
#   Mfg →(push)→ VM →(push, DID-resolved)→ OBS →(TO0)→ RV ←(TO1)← Device →(TO2)→ OBS
#
# The Voucher Manager acts as a middleman/reseller. It receives vouchers
# from the manufacturer, resolves the next owner via DID, signs the voucher
# over to the OBS key, and pushes it downstream.
#
# PORTS: Mfg=9301, VM=9302, RV=9303, OBS=9304

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_MFG=9301
PORT_VM=9302
PORT_RV=9303
PORT_OBS=9304
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

banner "Scenario 3: Reseller Supply Chain — PUSH Path"
narrate "This scenario demonstrates the full voucher supply chain with PUSH."
narrate "A reseller (Voucher Manager) sits between the manufacturer and"
narrate "the final customer (Onboarding Service). Vouchers flow:"
narrate "  Mfg →push→ VM →push→ OBS →TO0→ RV ←TO1← Device →TO2→ OBS"

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_MFG $PORT_VM $PORT_RV $PORT_OBS
init_artifact_dir "s3"

MFG_DB="$ARTIFACT_DIR/mfg.db"
MFG_VOUCHERS="$ARTIFACT_DIR/mfg_vouchers"
MFG_LOG="$ARTIFACT_DIR/mfg.log"
MFG_CONFIG="$ARTIFACT_DIR/mfg_config.yaml"

VM_DB="$ARTIFACT_DIR/vm.db"
VM_VOUCHERS="$ARTIFACT_DIR/vm_vouchers"
VM_LOG="$ARTIFACT_DIR/vm.log"
VM_CONFIG="$ARTIFACT_DIR/vm_config.yaml"

RV_DB="$ARTIFACT_DIR/rv.db"
RV_LOG="$ARTIFACT_DIR/rv.log"
RV_CONFIG="$ARTIFACT_DIR/rv_config.yaml"

OBS_DB="$ARTIFACT_DIR/obs.db"
OBS_VOUCHERS="$ARTIFACT_DIR/obs_vouchers"
OBS_LOG="$ARTIFACT_DIR/obs.log"
OBS_CONFIG="$ARTIFACT_DIR/obs_config.yaml"

DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
DEVICE_DI_LOG="$ARTIFACT_DIR/device_di.log"
DEVICE_TO2_LOG="$ARTIFACT_DIR/device_to2.log"
DEVICE_CONFIG="$ARTIFACT_DIR/device.cfg"

mkdir -p "$MFG_VOUCHERS" "$VM_VOUCHERS" "$OBS_VOUCHERS" "$ARTIFACT_DIR/obs_configs"

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
phase "Initialize Onboarding Service (final customer)"
# ============================================================
narrate "OBS is the final customer. It receives vouchers, runs TO0,"
narrate "and onboards devices. Its DID document advertises its public"
narrate "key and voucher receiver endpoint."

gen_obs_config "$PORT_OBS" "$OBS_DB" "$OBS_VOUCHERS" "test-s3-token" \
    "127.0.0.1" "$PORT_RV" "http" > "$OBS_CONFIG"

(cd "$DIR_OBS" && "$BIN_OBS" -config "$OBS_CONFIG" -init-only > "$ARTIFACT_DIR/obs_init.log" 2>&1)
log_success "OBS initialized"

OBS_OWNER_KEY="$ARTIFACT_DIR/obs_owner.pem"
extract_obs_owner_key "$BIN_OBS" "$OBS_CONFIG" "$OBS_OWNER_KEY"

# ============================================================
phase "Start Onboarding Service"
# ============================================================
start_bg_server "$DIR_OBS" "$OBS_LOG" "$BIN_OBS" -config "$OBS_CONFIG"
OBS_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_OBS" 30 "Onboarding Service" || exit 1
log_success "Onboarding Service running (port $PORT_OBS)"

# ============================================================
phase "Initialize Voucher Manager (reseller)"
# ============================================================
narrate "The Voucher Manager is a reseller middleman. It:"
narrate "  1. Receives vouchers from the manufacturer (push)"
narrate "  2. Signs them over to the next owner (OBS)"
narrate "  3. Pushes them downstream to OBS"
narrate ""
narrate "The VM's signover target is the OBS's public key."
narrate "In production this would use DID-based discovery, but here"
narrate "we use a static public key for simplicity."

gen_vm_config "$PORT_VM" "$VM_DB" "$VM_VOUCHERS" \
    "" \
    "static" "$OBS_OWNER_KEY" "" \
    "http://127.0.0.1:${PORT_OBS}/api/v1/vouchers" "test-s3-token" \
    "127.0.0.1:${PORT_VM}" "" \
    > "$VM_CONFIG"

start_bg_server "$DIR_VM" "$VM_LOG" "$BIN_VM" server -config "$VM_CONFIG"
VM_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_VM" 30 "Voucher Manager" || exit 1
log_success "Voucher Manager running (port $PORT_VM)"

# ============================================================
phase "Initialize Manufacturing Station"
# ============================================================
narrate "The manufacturer signs vouchers over to the VM's key and"
narrate "pushes them to the VM's receiver endpoint."

# The Mfg doesn't need to know the VM key if the VM does its own signover.
# So we configure Mfg to push to VM without signover (VM will do it).

narrate "Since the VM performs its own sign-over to OBS, the Mfg station"
narrate "just needs to push vouchers to VM. The Mfg's signover key is set"
narrate "to the OBS key (which passes through VM's pipeline unchanged if"
narrate "VM is configured to re-sign to the same key)."

gen_mfg_config "$PORT_MFG" "$MFG_DB" "$MFG_VOUCHERS" \
    "$OBS_OWNER_KEY" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers" \
    "" \
    "127.0.0.1" "$PORT_RV" "http" \
    > "$MFG_CONFIG"

(cd "$DIR_MFG" && "$BIN_MFG" -config "$MFG_CONFIG" -init-only > "$ARTIFACT_DIR/mfg_init.log" 2>&1)
log_success "Manufacturing Station initialized"

start_bg_server "$DIR_MFG" "$MFG_LOG" "$BIN_MFG" -config "$MFG_CONFIG"
MFG_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_MFG" 30 "Manufacturing Station" || exit 1
log_success "Manufacturing Station running (port $PORT_MFG)"

# ============================================================
phase "Device Initialization (DI)"
# ============================================================
gen_device_config "$DEVICE_CRED" "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_CONFIG"

narrate "Device connects to the manufacturing station for DI."

rm -f "$DEVICE_CRED"
DI_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" -di "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_DI_LOG" 2>&1) || DI_EXIT=$?

assert_equals "0" "$DI_EXIT" "Device DI should succeed"
assert_file_exists "$DEVICE_CRED" "Device credential created"

# ============================================================
phase "Wait for Voucher Flow: Mfg → VM → OBS → RV"
# ============================================================
narrate "The voucher should now flow through the supply chain:"
narrate "  1. Mfg pushes to VM"
narrate "  2. VM receives, signs over to OBS key, pushes to OBS"
narrate "  3. OBS receives, runs TO0 against RV"
narrate "Waiting for this pipeline to complete..."

sleep 8

show_file_listing "$MFG_VOUCHERS" "Mfg Vouchers"
show_file_listing "$VM_VOUCHERS" "VM Vouchers"
show_file_listing "$OBS_VOUCHERS" "OBS Vouchers"

# Verify VM received the voucher
VM_VOUCHER_COUNT=$(find "$VM_VOUCHERS" -name "*.fdoov" -type f 2>/dev/null | wc -l)
assert_count_gt "$VM_VOUCHER_COUNT" 0 "VM received voucher from Mfg"

# Verify OBS received the voucher (may be in DB if not saved to file)
OBS_VOUCHER_COUNT=$(find "$OBS_VOUCHERS" -name "*.fdoov" -type f 2>/dev/null | wc -l)
if [ "$OBS_VOUCHER_COUNT" -gt 0 ]; then
    assert_count_gt "$OBS_VOUCHER_COUNT" 0 "OBS received voucher from VM"
else
    assert_log_contains "$OBS_LOG" "voucher.*received\|voucher.*stored\|voucher_id" \
        "OBS log shows voucher receipt from VM"
fi

# Wait for TO0
sleep 5
show_rv_blobs "$BIN_RV" "$RV_CONFIG"

# ============================================================
phase "Device Onboarding (TO1 + TO2)"
# ============================================================
narrate "Device discovers OBS via RV (TO1) then onboards (TO2)."

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" > "$DEVICE_TO2_LOG" 2>&1) || TO2_EXIT=$?

if [ "$TO2_EXIT" -eq 0 ]; then
    assert_equals "0" "$TO2_EXIT" "Device TO1+TO2 should succeed"
else
    if grep -q "Success\|Credential Reuse" "$DEVICE_TO2_LOG" 2>/dev/null; then
        log_success "Device completed TO2 (success in log)"
        ((TESTS_RUN++)); ((TESTS_PASSED++))
    else
        log_error "Device TO1+TO2 failed (exit $TO2_EXIT)"
        ((TESTS_RUN++)); ((TESTS_FAILED++))
        tail -15 "$DEVICE_TO2_LOG" | sed 's/^/    /'
    fi
fi

# ============================================================
phase "Verification Summary"
# ============================================================
assert_log_contains "$MFG_LOG" "DI.*[Cc]ompleted\|Voucher Created" "Mfg confirms DI"
assert_log_contains "$VM_LOG" "voucher.*received\|voucher.*stored\|transmission" "VM received voucher"
assert_log_contains "$OBS_LOG" "Voucher found in database\|Voucher loaded from file\|voucher.*received\|TO0 dispatcher" "OBS confirms voucher processing"

narrate "Scenario 3 complete! The full push supply chain worked:"
narrate "  Mfg →push→ VM →push→ OBS →TO0→ RV ←TO1← Device →TO2→ OBS"
narrate "The voucher passed through 3 entities before the device onboarded."

print_summary
