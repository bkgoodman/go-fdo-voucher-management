#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 8: BMO Meta-URL Integration
#
# Tests BMO FSIM delivery modes using the go-fdo example server directly.
# This exercises the meta-payload creation tooling (fdo meta CLI),
# COSE Sign1 signing/verification, and the transparent auto-default
# MetaPayloadVerifier in the device BMO module.
#
# Unlike scenarios 1-7 which use the go-fdo-onboarding-service,
# this scenario uses the go-fdo example server directly because
# BMO FSIM support is in the go-fdo library's example server.
#
# PHASES:
#   1. Bootstrap: build, DI, initial TO2 (credential reuse)
#   2. Re-onboard with BMO inline delivery
#   3. Re-onboard with BMO meta-URL (unsigned)
#   4. Re-onboard with BMO meta-URL (signed)
#   5. Negative: tampered signed meta-payload → expect failure
#
# PORTS: FDO Server=9801, HTTP file server=18082

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_FDO=9801
PORT_HTTP=18082
FDO_PID=""
HTTP_PID=""

DIR_GOFDO="$DIR_VM/go-fdo"

cleanup() {
    phase "Cleanup"
    stop_pid "$FDO_PID" "FDO Server"
    stop_pid "$HTTP_PID" "HTTP File Server"
    kill_ports $PORT_FDO $PORT_HTTP
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 8: BMO Meta-URL Integration"
narrate "This scenario tests BMO (Bare Metal Onboarding) FSIM delivery"
narrate "modes, including meta-URL with COSE Sign1 signed payloads."
narrate "Uses go-fdo example server directly for BMO FSIM support."

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_FDO $PORT_HTTP
init_artifact_dir "s8"

FDO_DB="$ARTIFACT_DIR/fdo.db"
FDO_LOG="$ARTIFACT_DIR/fdo_server.log"
DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
FDO_ADDR="127.0.0.1:$PORT_FDO"
FDO_URL="http://$FDO_ADDR"

# Build go-fdo example binary
log_info "Building go-fdo example binary..."
(cd "$DIR_GOFDO/examples" && go build -o "$ARTIFACT_DIR/fdo" ./cmd) || {
    log_error "Failed to build go-fdo example binary"
    exit 1
}
FDO_BIN="$ARTIFACT_DIR/fdo"
log_success "Built: $FDO_BIN"

# ============================================================
phase "Phase 1: Bootstrap — DI + Initial TO2"
# ============================================================
narrate "Performing initial Device Initialization and Transfer of Ownership"
narrate "with credential reuse enabled, so the device can re-onboard."

# Start FDO server (no BMO, just basic sysconfig)
start_bg_server "$DIR_GOFDO/examples" "$FDO_LOG" \
    "$FDO_BIN" server -http "$FDO_ADDR" -db "$FDO_DB" \
    -reuse-cred
FDO_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_FDO" 30 "FDO Server" || exit 1
log_success "FDO Server running (PID: $FDO_PID)"

# DI
log_info "Running Device Initialization (DI)"
DI_EXIT=0
(cd "$ARTIFACT_DIR" && "$FDO_BIN" client -di "$FDO_URL" -blob cred.bin > di.log 2>&1) || DI_EXIT=$?
assert_equals "0" "$DI_EXIT" "Device DI should succeed"
assert_file_exists "$DEVICE_CRED" "Device credential file created"

# TO2
log_info "Running initial TO2"
TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$FDO_BIN" client -blob cred.bin > to2_initial.log 2>&1) || TO2_EXIT=$?
assert_equals "0" "$TO2_EXIT" "Initial TO2 should succeed"
log_success "Phase 1 complete: DI + TO2 with credential reuse"

stop_pid "$FDO_PID" "FDO Server"
FDO_PID=""

# ============================================================
phase "Phase 2: Re-onboard with BMO Inline"
# ============================================================
narrate "Restarting the server with BMO inline delivery mode."
narrate "The device re-onboards (credential reuse) and receives a boot image."

BMO_IMAGE="$ARTIFACT_DIR/test_image.bin"
dd if=/dev/urandom of="$BMO_IMAGE" bs=1024 count=4 2>/dev/null
INLINE_HASH=$(sha256sum "$BMO_IMAGE" | awk '{print $1}')
log_success "Created test image: $BMO_IMAGE (hash: ${INLINE_HASH:0:16}...)"

start_bg_server "$DIR_GOFDO/examples" "$FDO_LOG" \
    "$FDO_BIN" server -http "$FDO_ADDR" -db "$FDO_DB" \
    -reuse-cred \
    -bmo "application/x-raw-disk-image:$BMO_IMAGE"
FDO_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_FDO" 30 "FDO Server (BMO inline)" || exit 1

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$FDO_BIN" client -blob cred.bin > to2_bmo_inline.log 2>&1) || TO2_EXIT=$?
assert_equals "0" "$TO2_EXIT" "TO2 with BMO inline should succeed"

# Evidence: check client log for BMO delivery
assert_log_contains "$ARTIFACT_DIR/to2_bmo_inline.log" \
    "HandleImage\|Saved image\|fdo.bmo" \
    "Client received BMO image (inline)"
log_success "Phase 2 complete: BMO inline delivery"

stop_pid "$FDO_PID" "FDO Server"
FDO_PID=""
rm -f "$ARTIFACT_DIR"/bmo-* "$ARTIFACT_DIR"/examples/bmo-*

# ============================================================
phase "Phase 3: Re-onboard with BMO Meta-URL (Unsigned)"
# ============================================================
narrate "Using 'fdo meta create' to build an unsigned meta-payload,"
narrate "then serving it via HTTP for meta-URL delivery mode."

META_FILE="$ARTIFACT_DIR/meta-unsigned.cbor"

# Start HTTP file server
(cd "$ARTIFACT_DIR" && python3 -m http.server $PORT_HTTP &>/dev/null) &
HTTP_PID=$!
sleep 1
wait_for_port "$PORT_HTTP" 5 "HTTP file server" || exit 1

IMAGE_URL="http://127.0.0.1:$PORT_HTTP/test_image.bin"
META_URL="http://127.0.0.1:$PORT_HTTP/meta-unsigned.cbor"

# Create unsigned meta-payload with fdo meta CLI
log_info "Creating unsigned meta-payload"
(cd "$DIR_GOFDO/examples" && "$FDO_BIN" meta create \
    -mime "application/x-raw-disk-image" \
    -url "$IMAGE_URL" \
    -hash-file "$BMO_IMAGE" \
    -name "unsigned-meta-image" \
    -out "$META_FILE") || {
    log_error "Failed to create unsigned meta-payload"
    exit 1
}
log_success "Created unsigned meta-payload: $META_FILE"

start_bg_server "$DIR_GOFDO/examples" "$FDO_LOG" \
    "$FDO_BIN" server -http "$FDO_ADDR" -db "$FDO_DB" \
    -reuse-cred \
    -bmo-meta-url "$META_URL"
FDO_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_FDO" 30 "FDO Server (meta-URL unsigned)" || exit 1

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$FDO_BIN" client -blob cred.bin > to2_meta_unsigned.log 2>&1) || TO2_EXIT=$?
assert_equals "0" "$TO2_EXIT" "TO2 with unsigned meta-URL should succeed"

assert_log_contains "$ARTIFACT_DIR/to2_meta_unsigned.log" \
    "HandleImage\|Saved image\|fdo.bmo" \
    "Client received BMO image via unsigned meta-URL"
log_success "Phase 3 complete: BMO meta-URL unsigned delivery"

stop_pid "$FDO_PID" "FDO Server"
FDO_PID=""
rm -f "$ARTIFACT_DIR"/bmo-* "$ARTIFACT_DIR"/examples/bmo-*

# ============================================================
phase "Phase 4: Re-onboard with BMO Meta-URL (Signed)"
# ============================================================
narrate "Now using COSE Sign1 signed meta-payloads."
narrate "This exercises the full signing toolchain: key generation,"
narrate "'fdo meta create-signed', public key export, and the device's"
narrate "transparent auto-default CoseSign1Verifier."

SIGNER_KEY="$ARTIFACT_DIR/meta-signer.pem"
COSE_KEY="$ARTIFACT_DIR/signer.cbor"
META_SIGNED="$ARTIFACT_DIR/meta-signed.cbor"

# Generate ECDSA P-256 signing key
openssl ecparam -name prime256v1 -genkey -noout -out "$SIGNER_KEY" 2>/dev/null
log_success "Generated signing key: $SIGNER_KEY"

# Create signed meta-payload
(cd "$DIR_GOFDO/examples" && "$FDO_BIN" meta create-signed \
    -mime "application/x-raw-disk-image" \
    -url "$IMAGE_URL" \
    -hash-file "$BMO_IMAGE" \
    -name "signed-meta-image" \
    -key "$SIGNER_KEY" \
    -out "$META_SIGNED") || {
    log_error "Failed to create signed meta-payload"
    exit 1
}
log_success "Created signed meta-payload: $META_SIGNED"

# Export public key as COSE_Key
(cd "$DIR_GOFDO/examples" && "$FDO_BIN" meta export-pubkey \
    -key "$SIGNER_KEY" \
    -out "$COSE_KEY") || {
    log_error "Failed to export COSE_Key"
    exit 1
}
log_success "Exported COSE_Key: $COSE_KEY"

# Self-verify
(cd "$DIR_GOFDO/examples" && "$FDO_BIN" meta verify \
    -in "$META_SIGNED" \
    -key "$SIGNER_KEY" \
    -print) || {
    log_error "Self-verification of signed meta-payload failed"
    exit 1
}
log_success "Self-verification passed"

META_SIGNED_URL="http://127.0.0.1:$PORT_HTTP/meta-signed.cbor"
start_bg_server "$DIR_GOFDO/examples" "$FDO_LOG" \
    "$FDO_BIN" server -http "$FDO_ADDR" -db "$FDO_DB" \
    -reuse-cred \
    -bmo-meta-url "$META_SIGNED_URL:$COSE_KEY"
FDO_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_FDO" 30 "FDO Server (meta-URL signed)" || exit 1

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$FDO_BIN" client -blob cred.bin > to2_meta_signed.log 2>&1) || TO2_EXIT=$?
assert_equals "0" "$TO2_EXIT" "TO2 with signed meta-URL should succeed"

assert_log_contains "$ARTIFACT_DIR/to2_meta_signed.log" \
    "HandleImage\|Saved image\|fdo.bmo" \
    "Client received BMO image via signed meta-URL"
log_success "Phase 4 complete: BMO meta-URL signed delivery"

stop_pid "$FDO_PID" "FDO Server"
FDO_PID=""
rm -f "$ARTIFACT_DIR"/bmo-* "$ARTIFACT_DIR"/examples/bmo-*

# ============================================================
phase "Phase 5: Negative — Tampered Signed Meta-Payload"
# ============================================================
narrate "SECURITY TEST: Tamper with the signed meta-payload and verify"
narrate "that the device correctly rejects it (COSE Sign1 verification fails)."

TAMPERED="$ARTIFACT_DIR/meta-signed-tampered.cbor"
cp "$META_SIGNED" "$TAMPERED"
# Flip a byte in the signature region
python3 -c "
data = bytearray(open('$TAMPERED', 'rb').read())
data[-2] ^= 0xFF
open('$TAMPERED', 'wb').write(data)
"
# Replace the served file with the tampered one
cp "$TAMPERED" "$META_SIGNED"
log_success "Tampered with signed meta-payload"

start_bg_server "$DIR_GOFDO/examples" "$FDO_LOG" \
    "$FDO_BIN" server -http "$FDO_ADDR" -db "$FDO_DB" \
    -reuse-cred \
    -bmo-meta-url "$META_SIGNED_URL:$COSE_KEY"
FDO_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_FDO" 30 "FDO Server (tampered)" || exit 1

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$FDO_BIN" client -blob cred.bin > to2_tampered.log 2>&1) || TO2_EXIT=$?

# TO2 protocol may succeed but BMO should fail — check for signature error
if [ "$TO2_EXIT" -ne 0 ]; then
    log_success "TO2 correctly failed with tampered signature (exit=$TO2_EXIT)"
else
    # Check logs for signature rejection evidence
    if grep -qi "signature.*invalid\|signature.*failed\|verification.*error\|verification.*failed" \
        "$ARTIFACT_DIR/to2_tampered.log" "$FDO_LOG" 2>/dev/null; then
        log_success "BMO correctly rejected tampered signature (protocol completed, FSIM failed)"
    else
        log_error "TO2 succeeded unexpectedly with tampered signature"
        exit 1
    fi
fi

stop_pid "$FDO_PID" "FDO Server"
FDO_PID=""
stop_pid "$HTTP_PID" "HTTP File Server"
HTTP_PID=""

# ============================================================
phase "Verification Summary"
# ============================================================
narrate "All BMO Meta-URL phases completed:"
narrate "  ✓ Phase 1: Bootstrap (DI + initial TO2 with credential reuse)"
narrate "  ✓ Phase 2: BMO inline delivery"
narrate "  ✓ Phase 3: BMO meta-URL unsigned"
narrate "  ✓ Phase 4: BMO meta-URL signed (COSE Sign1)"
narrate "  ✓ Phase 5: Negative test (tampered signature rejected)"

show_file_listing "$ARTIFACT_DIR" "All Scenario 8 Artifacts"

print_summary
