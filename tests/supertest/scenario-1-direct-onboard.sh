#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 1: Direct Onboard (Baseline)
#
# The simplest end-to-end flow using 3 services:
#   Manufacturing Station → (push) → Onboarding Service → Device (direct TO2)
#
# No Rendezvous Server — device connects directly to OBS for TO2.
# This establishes the baseline that all other scenarios build upon.
#
# FLOW:
#   1. Init OBS → extract owner public key
#   2. Init Mfg Station with OBS key + push URL
#   3. Start both servers
#   4. Device DI → Mfg creates voucher → auto-pushes to OBS
#   5. Device TO2 directly against OBS
#
# PORTS: Mfg=9101, OBS=9102

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_MFG=9101
PORT_OBS=9102
MFG_PID=""
OBS_PID=""

cleanup() {
    phase "Cleanup"
    stop_pid "$MFG_PID" "Manufacturing Station"
    stop_pid "$OBS_PID" "Onboarding Service"
    kill_ports $PORT_MFG $PORT_OBS
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 1: Direct Onboard (Baseline)"
narrate "This is the simplest FDO flow: a device is manufactured,"
narrate "its voucher is pushed to the onboarding service, and the"
narrate "device connects directly for ownership transfer (TO2)."

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_MFG $PORT_OBS
init_artifact_dir "s1"

MFG_DB="$ARTIFACT_DIR/mfg.db"
MFG_VOUCHERS="$ARTIFACT_DIR/mfg_vouchers"
MFG_LOG="$ARTIFACT_DIR/mfg.log"
MFG_CONFIG="$ARTIFACT_DIR/mfg_config.yaml"

OBS_DB="$ARTIFACT_DIR/obs.db"
OBS_VOUCHERS="$ARTIFACT_DIR/obs_vouchers"
OBS_LOG="$ARTIFACT_DIR/obs.log"
OBS_CONFIG="$ARTIFACT_DIR/obs_config.yaml"

DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
DEVICE_DI_LOG="$ARTIFACT_DIR/device_di.log"
DEVICE_TO2_LOG="$ARTIFACT_DIR/device_to2.log"
DEVICE_CONFIG="$ARTIFACT_DIR/device.cfg"

mkdir -p "$MFG_VOUCHERS" "$OBS_VOUCHERS" "$ARTIFACT_DIR/obs_configs"

# ============================================================
phase "Initialize Onboarding Service"
# ============================================================
narrate "First we initialize the OBS so it generates its owner keys."
narrate "The owner public key will be given to the manufacturing station"
narrate "so it knows who to sign vouchers over to."

gen_obs_config "$PORT_OBS" "$OBS_DB" "$OBS_VOUCHERS" "test-s1-token" > "$OBS_CONFIG"

(cd "$DIR_OBS" && "$BIN_OBS" -config "$OBS_CONFIG" -init-only > "$ARTIFACT_DIR/obs_init.log" 2>&1)
log_success "OBS initialized"

OBS_OWNER_KEY="$ARTIFACT_DIR/obs_owner.pem"
extract_obs_owner_key "$BIN_OBS" "$OBS_CONFIG" "$OBS_OWNER_KEY"

narrate "Owner public key extracted — this key will be embedded in every"
narrate "voucher so the device knows who its legitimate owner is."
show_key_fingerprint "$OBS_OWNER_KEY" "OBS Owner"

# ============================================================
phase "Initialize Manufacturing Station"
# ============================================================
narrate "The manufacturing station is configured with:"
narrate "  • The OBS owner public key (for voucher sign-over)"
narrate "  • A push URL pointing to the OBS voucher receiver"

gen_mfg_config "$PORT_MFG" "$MFG_DB" "$MFG_VOUCHERS" \
    "$OBS_OWNER_KEY" \
    "http://127.0.0.1:${PORT_OBS}/api/v1/vouchers" \
    "test-s1-token" \
    > "$MFG_CONFIG"

(cd "$DIR_MFG" && "$BIN_MFG" -config "$MFG_CONFIG" -init-only > "$ARTIFACT_DIR/mfg_init.log" 2>&1)
log_success "Manufacturing Station initialized"

# ============================================================
phase "Create Device Configuration"
# ============================================================
gen_device_config "$DEVICE_CRED" "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_CONFIG"
log_success "Device config created"

# ============================================================
phase "Start Services"
# ============================================================
narrate "Starting Manufacturing Station on port $PORT_MFG"
start_bg_server "$DIR_OBS" "$OBS_LOG" "$BIN_OBS" -config "$OBS_CONFIG"
OBS_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_OBS" 30 "Onboarding Service" || exit 1
log_success "Onboarding Service running (PID: $OBS_PID)"

narrate "Starting Manufacturing Station on port $PORT_MFG"
start_bg_server "$DIR_MFG" "$MFG_LOG" "$BIN_MFG" -config "$MFG_CONFIG"
MFG_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_MFG" 30 "Manufacturing Station" || exit 1
log_success "Manufacturing Station running (PID: $MFG_PID)"

# ============================================================
phase "Device Initialization (DI)"
# ============================================================
narrate "The device connects to the manufacturing station for Device"
narrate "Initialization (DI). This is the factory step where the device"
narrate "gets its unique identity and credential blob."

rm -f "$DEVICE_CRED"
DI_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" -di "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_DI_LOG" 2>&1) || DI_EXIT=$?

assert_equals "0" "$DI_EXIT" "Device DI should succeed"
assert_file_exists "$DEVICE_CRED" "Device credential file created"

narrate "DI complete. The device now has a credential blob (cred.bin)"
narrate "and the manufacturing station has created a voucher."

# Wait for voucher push to complete
sleep 3

show_file_listing "$MFG_VOUCHERS" "Manufacturing Vouchers"

# ============================================================
phase "Verify Voucher Push"
# ============================================================
narrate "The manufacturing station should have auto-pushed the voucher"
narrate "to the onboarding service's receiver endpoint."

assert_dir_not_empty "$MFG_VOUCHERS" "*.fdoov" "Mfg station created voucher file(s)"

# Check OBS received it (either via file or DB)
sleep 2
OBS_VOUCHER_COUNT=$(find "$OBS_VOUCHERS" -name "*.fdoov" -type f 2>/dev/null | wc -l)
if [ "$OBS_VOUCHER_COUNT" -gt 0 ]; then
    assert_count_gt "$OBS_VOUCHER_COUNT" 0 "OBS received voucher via push"
else
    # May be in DB only — check log for successful receipt
    assert_log_contains "$OBS_LOG" "voucher.*received\|voucher.*stored\|voucher_id" \
        "OBS log shows voucher receipt"
fi

show_file_listing "$OBS_VOUCHERS" "Onboarding Service Vouchers"

# ============================================================
phase "Device Onboarding (TO2)"
# ============================================================
narrate "Now the device performs Transfer Ownership 2 (TO2)."
narrate "In this baseline scenario, the device connects directly to"
narrate "the onboarding service (skipping rendezvous/TO1)."
narrate "During TO2, the OBS proves it holds the owner key that matches"
narrate "the voucher, and delivers FSIM payloads (sysconfig, etc)."

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" -to2 "http://127.0.0.1:${PORT_OBS}" > "$DEVICE_TO2_LOG" 2>&1) || TO2_EXIT=$?

assert_equals "0" "$TO2_EXIT" "Device TO2 should succeed"

# Check for evidence of successful onboarding
if grep -q "Success" "$DEVICE_TO2_LOG" 2>/dev/null; then
    assert_log_contains "$DEVICE_TO2_LOG" "Success" "Device reports successful onboarding"
elif grep -q "Credential Reuse" "$DEVICE_TO2_LOG" 2>/dev/null; then
    assert_log_contains "$DEVICE_TO2_LOG" "Credential Reuse" "Device completed TO2 with credential reuse"
fi

# ============================================================
phase "Verification Summary"
# ============================================================
narrate "Checking all components for evidence of successful flow."

# MFG log should show DI
assert_log_contains "$MFG_LOG" "DI Completed\|successfully initialized\|voucher transmission delivered" \
    "Mfg log confirms DI completion"

# OBS log should show TO2
assert_log_contains "$OBS_LOG" "Voucher found in database\|Voucher loaded from file\|voucher.*received\|voucher_id" \
    "OBS log confirms voucher processing"

show_file_listing "$ARTIFACT_DIR" "All Scenario 1 Artifacts"

narrate "Scenario 1 complete! The device was manufactured, its voucher"
narrate "pushed to the onboarding service, and the device was onboarded"
narrate "via direct TO2 — the simplest possible FDO flow."

print_summary
