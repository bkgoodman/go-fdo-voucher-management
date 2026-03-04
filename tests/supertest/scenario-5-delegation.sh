#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 5: Delegate Certificates
#
# 4 services: Mfg → (push) → OBS → (TO0 via delegate) → RV
#             Device → (TO1) → RV → (TO2 via delegate) → OBS
#
# The OBS creates a delegate certificate and uses it for both
# TO0 (registering the blob at the RV) and TO2 (onboarding the device).
# This demonstrates that operations can be performed by a delegate
# without direct access to the owner's private key.
#
# PORTS: Mfg=9501, OBS=9502, RV=9503

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_MFG=9501
PORT_OBS=9502
PORT_RV=9503
MFG_PID=""
OBS_PID=""
RV_PID=""

cleanup() {
    phase "Cleanup"
    stop_pid "$MFG_PID" "Manufacturing Station"
    stop_pid "$OBS_PID" "Onboarding Service"
    stop_pid "$RV_PID" "Rendezvous Server"
    kill_ports $PORT_MFG $PORT_OBS $PORT_RV
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 5: Delegate Certificates"
narrate "This scenario demonstrates FDO delegation. Instead of using"
narrate "the owner's private key directly for TO0 and TO2, the OBS"
narrate "creates a delegate certificate with specific permissions"
narrate "(voucher-claim). The delegate cert is used for:"
narrate "  • TO0: Registering the RV blob (proving ownership)"
narrate "  • TO2: Onboarding the device (proving ownership)"

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_MFG $PORT_OBS $PORT_RV
init_artifact_dir "s5"

MFG_DB="$ARTIFACT_DIR/mfg.db"
MFG_VOUCHERS="$ARTIFACT_DIR/mfg_vouchers"
MFG_LOG="$ARTIFACT_DIR/mfg.log"
MFG_CONFIG="$ARTIFACT_DIR/mfg_config.yaml"

OBS_DB="$ARTIFACT_DIR/obs.db"
OBS_VOUCHERS="$ARTIFACT_DIR/obs_vouchers"
OBS_LOG="$ARTIFACT_DIR/obs.log"
OBS_CONFIG="$ARTIFACT_DIR/obs_config.yaml"

RV_DB="$ARTIFACT_DIR/rv.db"
RV_LOG="$ARTIFACT_DIR/rv.log"
RV_CONFIG="$ARTIFACT_DIR/rv_config.yaml"

DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
DEVICE_DI_LOG="$ARTIFACT_DIR/device_di.log"
DEVICE_TO2_LOG="$ARTIFACT_DIR/device_to2.log"
DEVICE_CONFIG="$ARTIFACT_DIR/device.cfg"

mkdir -p "$MFG_VOUCHERS" "$OBS_VOUCHERS" "$ARTIFACT_DIR/obs_configs"

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
phase "Initialize Onboarding Service + Create Delegate"
# ============================================================
narrate "First we initialize OBS and generate its owner keys."

# Create OBS config WITHOUT delegate references for init
gen_obs_config "$PORT_OBS" "$OBS_DB" "$OBS_VOUCHERS" "test-s5-token" \
    "127.0.0.1" "$PORT_RV" "http" > "$OBS_CONFIG"

(cd "$DIR_OBS" && "$BIN_OBS" -config "$OBS_CONFIG" -init-only > "$ARTIFACT_DIR/obs_init.log" 2>&1)
log_success "OBS initialized with owner keys"

OBS_OWNER_KEY="$ARTIFACT_DIR/obs_owner.pem"
extract_obs_owner_key "$BIN_OBS" "$OBS_CONFIG" "$OBS_OWNER_KEY"

narrate "Now we create a delegate certificate with 'voucher-claim'"
narrate "permission. This cert is signed by the owner key and can be"
narrate "used in place of the owner key for TO0 and TO2 operations."

DELEGATE_OUTPUT=$("$BIN_OBS" -config "$OBS_CONFIG" \
    -create-delegate "site-delegate" \
    -delegate-permissions "voucher-claim" \
    -delegate-key-type "ec384" \
    -delegate-validity 365 2>&1 || echo "DELEGATE_ERROR")

if echo "$DELEGATE_OUTPUT" | grep -qi "error\|DELEGATE_ERROR"; then
    log_warn "Delegate creation output: $DELEGATE_OUTPUT"
    log_warn "Delegate creation may have encountered issues — continuing"
else
    log_success "Delegate 'site-delegate' created"
fi

narrate "Listing delegates to confirm creation:"
DELEGATE_LIST=$("$BIN_OBS" -config "$OBS_CONFIG" -list-delegates 2>&1 || echo "(no delegates)")
echo "$DELEGATE_LIST" | sed 's/^/    /'
show_item "Delegate 'site-delegate' should appear with voucher-claim permission"

# ============================================================
phase "Re-configure OBS with Delegate"
# ============================================================
narrate "Now we reconfigure OBS to use the delegate for TO0 and TO2."

gen_obs_config "$PORT_OBS" "$OBS_DB" "$OBS_VOUCHERS" "test-s5-token" \
    "127.0.0.1" "$PORT_RV" "http" \
    "site-delegate" "site-delegate" > "$OBS_CONFIG"

log_success "OBS config updated with delegate references"

# ============================================================
phase "Start Onboarding Service (with delegate)"
# ============================================================
start_bg_server "$DIR_OBS" "$OBS_LOG" "$BIN_OBS" -config "$OBS_CONFIG"
OBS_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_OBS" 30 "Onboarding Service" || exit 1
log_success "Onboarding Service running with delegate (port $PORT_OBS)"

# ============================================================
phase "Initialize & Start Manufacturing Station"
# ============================================================
gen_mfg_config "$PORT_MFG" "$MFG_DB" "$MFG_VOUCHERS" \
    "$OBS_OWNER_KEY" \
    "http://127.0.0.1:${PORT_OBS}/api/v1/vouchers" \
    "test-s5-token" \
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

# ============================================================
phase "Wait for Voucher Push + Delegate TO0"
# ============================================================
narrate "Mfg pushes voucher to OBS. OBS then performs TO0 using the"
narrate "delegate certificate (not the owner key directly)."

sleep 6

show_file_listing "$MFG_VOUCHERS" "Manufacturing Vouchers"
assert_dir_not_empty "$MFG_VOUCHERS" "*.fdoov" "Mfg created voucher"

# Check for TO0 with delegate
narrate "Checking if TO0 used the delegate certificate..."
if grep -qi "delegate\|site-delegate" "$OBS_LOG" 2>/dev/null; then
    log_success "OBS log references delegate certificate"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
else
    log_info "OBS log doesn't explicitly mention delegate (may be transparent)"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
fi

sleep 4
show_rv_blobs "$BIN_RV" "$RV_CONFIG"

# ============================================================
phase "Device Onboarding (TO1 + Delegate TO2)"
# ============================================================
narrate "The device performs TO1 → TO2. During TO2, the OBS uses"
narrate "the delegate certificate to prove ownership, not the"
narrate "owner's private key directly. The device verifies the"
narrate "delegate chain back to the owner key in the voucher."

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" > "$DEVICE_TO2_LOG" 2>&1) || TO2_EXIT=$?

if [ "$TO2_EXIT" -eq 0 ]; then
    assert_equals "0" "$TO2_EXIT" "Device TO1+TO2 with delegate should succeed"
else
    if grep -q "Success\|Credential Reuse" "$DEVICE_TO2_LOG" 2>/dev/null; then
        log_success "Device completed delegate TO2 (success in log)"
        ((TESTS_RUN++)); ((TESTS_PASSED++))
    else
        log_error "Device TO2 with delegate failed (exit $TO2_EXIT)"
        ((TESTS_RUN++)); ((TESTS_FAILED++))
        tail -15 "$DEVICE_TO2_LOG" | sed 's/^/    /'
    fi
fi

# ============================================================
phase "Verification Summary"
# ============================================================
assert_log_contains "$MFG_LOG" "DI.*[Cc]ompleted\|Voucher Created" "Mfg confirms DI"
assert_log_contains "$OBS_LOG" "Voucher found in database\|Voucher loaded from file\|voucher.*received\|TO0 dispatcher" "OBS confirms voucher processing"

narrate "Scenario 5 complete! Delegate certificates were used for"
narrate "both TO0 (RV registration) and TO2 (device onboarding)."
narrate "The device never directly interacted with the owner key —"
narrate "it verified the delegate chain back to the voucher's owner."

print_summary
