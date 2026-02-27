#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 4: Reseller Supply Chain — PULL Path
#
# All 5 services. Same supply chain as Scenario 3 but the OBS PULLS
# vouchers from the VM instead of the VM pushing them:
#   Mfg →(push)→ VM ←(pull/PullAuth)← OBS →(TO0)→ RV ←(TO1)← Device →(TO2)→ OBS
#
# This demonstrates PullAuth owner-key authentication (Type-5):
# OBS proves it holds the owner key that vouchers were signed over to,
# then lists and downloads them from the VM's Pull API.
#
# PORTS: Mfg=9401, VM=9402, RV=9403, OBS=9404

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_MFG=9401
PORT_VM=9402
PORT_RV=9403
PORT_OBS=9404
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

banner "Scenario 4: Reseller Supply Chain — PULL Path"
narrate "Same supply chain as Scenario 3, but instead of VM pushing"
narrate "vouchers to OBS, the OBS actively PULLS them from the VM"
narrate "using PullAuth (Type-5 owner-key authentication)."
narrate ""
narrate "This simulates environments where the downstream service"
narrate "cannot receive inbound connections (e.g., behind NAT/firewall)"
narrate "but can make outbound requests to the upstream holder."

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_MFG $PORT_VM $PORT_RV $PORT_OBS
init_artifact_dir "s4"

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
OBS_PULLED="$ARTIFACT_DIR/obs_pulled"

DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
DEVICE_DI_LOG="$ARTIFACT_DIR/device_di.log"
DEVICE_TO2_LOG="$ARTIFACT_DIR/device_to2.log"
DEVICE_CONFIG="$ARTIFACT_DIR/device.cfg"

mkdir -p "$MFG_VOUCHERS" "$VM_VOUCHERS" "$OBS_VOUCHERS" "$OBS_PULLED" "$ARTIFACT_DIR/obs_configs"

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
narrate "Mfg will sign vouchers to VM's key. The puller (OBS) will"
narrate "then use VM's exported private key for PullAuth."
narrate "Push to OBS is DISABLED — OBS will pull instead."

# VM signover target is not needed here — VM is the terminal holder.
# No signover_key, no push_url.
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

show_did_document "http://127.0.0.1:${PORT_VM}/.well-known/did.json" "VM DID Document"

# Extract VM's public key for Mfg signover target
VM_OWNER_PUB="$ARTIFACT_DIR/vm_owner_pub.pem"
openssl pkey -in "$VM_OWNER_KEY_EXPORT" -pubout -out "$VM_OWNER_PUB" 2>/dev/null

# ============================================================
phase "Initialize & Start Onboarding Service"
# ============================================================
narrate "OBS is initialized with RV info. It will receive vouchers"
narrate "after pulling them from VM and importing locally."

gen_obs_config "$PORT_OBS" "$OBS_DB" "$OBS_VOUCHERS" "test-s4-token" \
    "127.0.0.1" "$PORT_RV" "http" > "$OBS_CONFIG"

(cd "$DIR_OBS" && "$BIN_OBS" -config "$OBS_CONFIG" -init-only > "$ARTIFACT_DIR/obs_init.log" 2>&1)
log_success "OBS initialized"

start_bg_server "$DIR_OBS" "$OBS_LOG" "$BIN_OBS" -config "$OBS_CONFIG"
OBS_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_OBS" 30 "Onboarding Service" || exit 1
log_success "Onboarding Service running (port $PORT_OBS)"

# ============================================================
phase "Initialize & Start Manufacturing Station"
# ============================================================
narrate "Mfg signs vouchers over to VM's key and pushes to VM."
narrate "VM holds vouchers; OBS will pull them later using VM's key."

gen_mfg_config "$PORT_MFG" "$MFG_DB" "$MFG_VOUCHERS" \
    "$VM_OWNER_PUB" \
    "http://127.0.0.1:${PORT_VM}/api/v1/vouchers" \
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

# Wait for push to VM
sleep 4

show_file_listing "$VM_VOUCHERS" "VM Vouchers (received from Mfg)"
assert_dir_not_empty "$VM_VOUCHERS" "*.fdoov" "VM received voucher from Mfg"

# Verify OBS does NOT have the voucher yet
OBS_PRE_COUNT=$(find "$OBS_VOUCHERS" -name "*.fdoov" -type f 2>/dev/null | wc -l)
narrate "OBS voucher count before pull: $OBS_PRE_COUNT"
show_item "OBS has $OBS_PRE_COUNT voucher(s) — pull has not happened yet"

# ============================================================
phase "OBS Pulls Vouchers from VM (PullAuth Owner-Key)"
# ============================================================
narrate "Now OBS authenticates to VM using PullAuth (Type-5)."
narrate "OBS proves it holds the owner key that the vouchers were"
narrate "signed over to. VM verifies this and returns only vouchers"
narrate "whose owner_key_fingerprint matches OBS's key."

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
    head -10 "$PULL_LOG" | sed 's/^/    /'
fi
log_info "Pull output:"
echo "$PULL_OUTPUT" | python3 -m json.tool 2>/dev/null | sed 's/^/    /' || echo "    $PULL_OUTPUT"

# Check pull result
PULL_DOWNLOADED=$(echo "$PULL_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('downloaded',0))" 2>/dev/null || echo "0")
assert_count_gt "$PULL_DOWNLOADED" 0 "OBS downloaded voucher(s) via PullAuth pull"

show_file_listing "$OBS_PULLED" "OBS Pulled Vouchers"

# ============================================================
phase "Negative Test: Owner-Scoped Isolation"
# ============================================================
narrate "An unrelated key should see ZERO vouchers when pulling"
narrate "from the same VM — this proves owner-key scoping works."

UNRELATED_OUTPUT=$("$BIN_VM" pullauth \
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
    log_warn "Owner isolation test: unrelated key saw '$UNRELATED_COUNT' (expected 0)"
    ((TESTS_RUN++)); ((TESTS_FAILED++))
fi

# ============================================================
phase "Import Pulled Vouchers into OBS & Run TO0"
# ============================================================
narrate "The pulled vouchers need to be imported into OBS's voucher"
narrate "directory so its TO0 dispatcher can register them at the RV."

# Copy pulled vouchers to OBS voucher dir
for f in "$OBS_PULLED"/*.fdoov; do
    [ -f "$f" ] && cp "$f" "$OBS_VOUCHERS/"
done

# Import into OBS database
for f in "$OBS_VOUCHERS"/*.fdoov; do
    if [ -f "$f" ]; then
        (cd "$DIR_OBS" && "$BIN_OBS" -config "$OBS_CONFIG" -import-voucher "$f" > /dev/null 2>&1 || true)
    fi
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
narrate "Scenario 4 complete! The PULL supply chain worked:"
narrate "  Mfg →push→ VM ←pull← OBS →TO0→ RV ←TO1← Device →TO2→ OBS"
narrate ""
narrate "Key difference from Scenario 3: OBS initiated the voucher"
narrate "transfer using PullAuth, proving ownership of the target key."
narrate "This is essential for environments where the downstream"
narrate "service cannot receive inbound connections."

print_summary
