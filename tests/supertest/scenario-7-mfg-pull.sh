#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 7: VM Pulls Vouchers from Manufacturing Station
#
# Tests the pull path where VM acts as the puller and Mfg acts as
# the holder. This is the reverse of the typical push flow:
#   Mfg (holder) ←(pull/FDOKeyAuth)← VM (puller)
#
# This scenario is expected to FAIL until the Manufacturing Station
# (go-fdo-di) is updated to include FDOKeyAuth holder support and the
# go-fdo library fingerprint normalization fix.
#
# PORTS: Mfg=9701, VM=9702

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_MFG=9701
PORT_VM=9702
MFG_PID=""
VM_PID=""

cleanup() {
    phase "Cleanup"
    stop_pid "$MFG_PID" "Manufacturing Station"
    stop_pid "$VM_PID" "Voucher Manager"
    kill_ports $PORT_MFG $PORT_VM
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 7: VM Pulls Vouchers from Mfg (DI Service)"
narrate "This scenario tests whether the Manufacturing Station can"
narrate "act as a FDOKeyAuth Holder — allowing the Voucher Manager"
narrate "to authenticate and pull vouchers using its owner key."
narrate ""
narrate "EXPECTED: This scenario will FAIL until go-fdo-di adds"
narrate "FDOKeyAuth holder support (and uses the normalized fingerprint"
narrate "library). This is a canary test."

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_MFG $PORT_VM
init_artifact_dir "s7"

MFG_DB="$ARTIFACT_DIR/mfg.db"
MFG_VOUCHERS="$ARTIFACT_DIR/mfg_vouchers"
MFG_LOG="$ARTIFACT_DIR/mfg.log"
MFG_CONFIG="$ARTIFACT_DIR/mfg_config.yaml"

VM_DB="$ARTIFACT_DIR/vm.db"
VM_VOUCHERS="$ARTIFACT_DIR/vm_vouchers"
VM_LOG="$ARTIFACT_DIR/vm.log"
VM_CONFIG="$ARTIFACT_DIR/vm_config.yaml"
VM_OWNER_KEY_EXPORT="$ARTIFACT_DIR/vm_owner_key.pem"

DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
DEVICE_DI_LOG="$ARTIFACT_DIR/device_di.log"
DEVICE_CONFIG="$ARTIFACT_DIR/device.cfg"

mkdir -p "$MFG_VOUCHERS" "$VM_VOUCHERS" "$ARTIFACT_DIR/vm_pulled"

# ============================================================
phase "Initialize & Start Voucher Manager"
# ============================================================
narrate "VM starts first so it can export its DID-minted owner key."
narrate "Mfg will sign vouchers TO VM's key. Then VM will attempt"
narrate "to PULL those vouchers FROM Mfg."

# VM config: no push configured, no signover — VM is the terminal owner.
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

# Wait for DID minting / key export
sleep 2
if [ -f "$VM_OWNER_KEY_EXPORT" ]; then
    log_success "VM exported owner key to $VM_OWNER_KEY_EXPORT"
else
    log_warn "VM owner key export not found yet, waiting..."
    sleep 3
fi

# Extract VM's public key for Mfg signover target
VM_OWNER_PUB="$ARTIFACT_DIR/vm_owner_pub.pem"
openssl pkey -in "$VM_OWNER_KEY_EXPORT" -pubout -out "$VM_OWNER_PUB" 2>/dev/null

# ============================================================
phase "Initialize & Start Manufacturing Station (Holder)"
# ============================================================
narrate "Mfg is configured to sign vouchers over to VM's key."
narrate "Push is DISABLED — Mfg holds the vouchers. VM will"
narrate "attempt to pull them via FDOKeyAuth."

# No push_url, no push_token — Mfg holds vouchers locally
gen_mfg_config "$PORT_MFG" "$MFG_DB" "$MFG_VOUCHERS" \
    "$VM_OWNER_PUB" \
    "" \
    "" \
    "127.0.0.1" "$PORT_MFG" "http" \
    > "$MFG_CONFIG"

(cd "$DIR_MFG" && "$BIN_MFG" -config "$MFG_CONFIG" -init-only > "$ARTIFACT_DIR/mfg_init.log" 2>&1)
start_bg_server "$DIR_MFG" "$MFG_LOG" "$BIN_MFG" -config "$MFG_CONFIG"
MFG_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_MFG" 30 "Manufacturing Station" || exit 1
log_success "Manufacturing Station running (port $PORT_MFG)"

# ============================================================
phase "Device Initialization (DI)"
# ============================================================
narrate "Create a voucher via DI. Mfg signs it over to VM's key"
narrate "and holds it locally (no push configured)."

gen_device_config "$DEVICE_CRED" "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_CONFIG"

rm -f "$DEVICE_CRED"
DI_EXIT=0
(cd "$ARTIFACT_DIR" && "$BIN_ENDPOINT" -config "$DEVICE_CONFIG" -di "http://127.0.0.1:${PORT_MFG}" > "$DEVICE_DI_LOG" 2>&1) || DI_EXIT=$?

assert_equals "0" "$DI_EXIT" "Device DI should succeed"
assert_file_exists "$DEVICE_CRED" "Device credential created"

# Wait for voucher to be written to disk
sleep 2
show_file_listing "$MFG_VOUCHERS" "Mfg Vouchers (held locally)"
assert_dir_not_empty "$MFG_VOUCHERS" "*.fdoov" "Mfg created and holds voucher"

# ============================================================
phase "VM Pulls Vouchers from Mfg (FDOKeyAuth Owner-Key)"
# ============================================================
narrate "VM authenticates to Mfg using FDOKeyAuth (Type-5)."
narrate "VM proves it holds the owner key that the vouchers were"
narrate "signed over to. Mfg should verify this and return vouchers."
narrate ""
narrate ">>> EXPECTED TO FAIL: Mfg (go-fdo-di) does not yet have"
narrate ">>> FDOKeyAuth holder endpoints. The pull command will likely"
narrate ">>> get a 404 or connection error on the FDOKeyAuth endpoint."

PULL_DIR="$ARTIFACT_DIR/vm_pulled"
PULL_LOG="$ARTIFACT_DIR/pull_cmd.log"
PULL_EXIT=0
PULL_OUTPUT=$("$BIN_VM" pull \
    -url "http://127.0.0.1:${PORT_MFG}" \
    -key "$VM_OWNER_KEY_EXPORT" \
    -output "$PULL_DIR" \
    -json 2>"$PULL_LOG") || PULL_EXIT=$?

log_info "Pull exit code: $PULL_EXIT"
if [ -s "$PULL_LOG" ]; then
    log_info "Pull stderr:"
    head -20 "$PULL_LOG" | sed 's/^/    /'
fi
if [ -n "$PULL_OUTPUT" ]; then
    log_info "Pull output:"
    echo "$PULL_OUTPUT" | python3 -m json.tool 2>/dev/null | sed 's/^/    /' || echo "    $PULL_OUTPUT"
fi

# Check pull result
PULL_DOWNLOADED=0
if [ -n "$PULL_OUTPUT" ]; then
    PULL_DOWNLOADED=$(echo "$PULL_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('downloaded',0))" 2>/dev/null || echo "0")
fi

if [ "$PULL_EXIT" -eq 0 ] && [ "$PULL_DOWNLOADED" -gt 0 ]; then
    log_success "VM pulled $PULL_DOWNLOADED voucher(s) from Mfg via FDOKeyAuth"
    ((TESTS_RUN++)); ((TESTS_PASSED++))
    show_file_listing "$PULL_DIR" "VM Pulled Vouchers"
else
    log_error "VM failed to pull vouchers from Mfg (exit=$PULL_EXIT, downloaded=$PULL_DOWNLOADED)"
    log_warn ">>> This is EXPECTED until go-fdo-di adds FDOKeyAuth holder support"
    log_warn ">>> and uses the go-fdo library with fingerprint normalization."
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Verification Summary"
# ============================================================
narrate "Scenario 7: VM pull from Mfg"
narrate ""
narrate "This scenario tests the supply chain path:"
narrate "  Mfg (holder) ←(FDOKeyAuth)← VM (puller)"
narrate ""
if [ "$PULL_EXIT" -eq 0 ] && [ "$PULL_DOWNLOADED" -gt 0 ]; then
    narrate "RESULT: PASS — Mfg supports FDOKeyAuth and VM pulled successfully."
else
    narrate "RESULT: EXPECTED FAILURE — Mfg does not yet support FDOKeyAuth."
    narrate "To fix: Update go-fdo-di to:"
    narrate "  1. Use the go-fdo library with FingerprintProtocolKey normalization"
    narrate "  2. Register FDOKeyAuth handler endpoints"
    narrate "  3. Implement voucher pull store (ListByOwner, GetVoucher)"
fi

print_summary
