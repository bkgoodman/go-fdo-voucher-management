#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 8 Enhanced: BMO Meta-URL Integration with go-fdo-meta-tool
#
# Enhanced version of scenario-8 that specifically tests the new go-fdo-meta-tool
# for creating meta-payloads. This demonstrates that the meta tool produces
# compatible CBOR payloads that work with the FDO onboarding service.
#
# PHASES:
#   1. Bootstrap: build, DI, initial TO2 (credential reuse)
#   2. Re-onboard with BMO inline delivery
#   3. Re-onboard with BMO meta-URL using go-fdo-meta-tool (unsigned)
#   4. Re-onboard with BMO meta-URL using go-fdo-meta-tool (signed)
#   5. Negative: tampered signed meta-payload → expect failure
#   6. Negative: incorrect hash verification → expect failure
#   7. Positive: hash verification with correct file
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
DIR_META_TOOL="/var/bkgdata/go-fdo-meta-tool"

cleanup() {
    phase "Cleanup"
    stop_pid "$FDO_PID" "FDO Server"
    stop_pid "$HTTP_PID" "HTTP File Server"
    kill_ports $PORT_FDO $PORT_HTTP
    cleanup_all_pids
}
trap cleanup EXIT

banner "Scenario 8 Enhanced: BMO Meta-URL with go-fdo-meta-tool"
narrate "Enhanced scenario testing the new go-fdo-meta-tool for creating"
narrate "meta-payloads that work with FDO BMO FSIM delivery."

# ============================================================
phase "Setup: Clean environment"
# ============================================================
kill_ports $PORT_FDO $PORT_HTTP
init_artifact_dir "s8e"

FDO_DB="$ARTIFACT_DIR/fdo.db"
FDO_LOG="$ARTIFACT_DIR/fdo_server.log"
DEVICE_CRED="$ARTIFACT_DIR/cred.bin"
FDO_ADDR="127.0.0.1:$PORT_FDO"
FDO_URL="http://$FDO_ADDR"
META_TOOL="$DIR_META_TOOL/fdo-meta-tool"

# Verify meta tool exists
if [ ! -x "$META_TOOL" ]; then
    log_error "go-fdo-meta-tool not found at $META_TOOL"
    exit 1
fi
log_success "Found meta tool: $META_TOOL"

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
phase "Phase 3: BMO Meta-URL with go-fdo-meta-tool (Unsigned)"
# ============================================================
narrate "Using go-fdo-meta-tool to create an unsigned meta-payload,"
narrate "then serving it via HTTP for meta-URL delivery mode."

META_FILE="$ARTIFACT_DIR/meta-unsigned.cbor"

# Start HTTP file server
(cd "$ARTIFACT_DIR" && python3 -m http.server $PORT_HTTP &>/dev/null) &
HTTP_PID=$!
sleep 1
wait_for_port "$PORT_HTTP" 5 "HTTP file server" || exit 1

IMAGE_URL="http://127.0.0.1:$PORT_HTTP/test_image.bin"
META_URL="http://127.0.0.1:$PORT_HTTP/meta-unsigned.cbor"

# Create unsigned meta-payload with go-fdo-meta-tool
log_info "Creating unsigned meta-payload with go-fdo-meta-tool"
"$META_TOOL" meta create \
    -mime "application/x-raw-disk-image" \
    -url "$IMAGE_URL" \
    -hash-file "$BMO_IMAGE" \
    -name "unsigned-meta-image" \
    -version "1.0" \
    -description "Unsigned meta-payload created by go-fdo-meta-tool" \
    -out "$META_FILE" || {
    log_error "Failed to create unsigned meta-payload with go-fdo-meta-tool"
    exit 1
}
log_success "Created unsigned meta-payload: $META_FILE"

# Verify the meta-payload was created correctly
log_info "Inspecting created meta-payload"
"$META_TOOL" meta inspect -in "$META_FILE" > inspect_unsigned.log 2>&1 || {
    log_error "Failed to inspect unsigned meta-payload"
    exit 1
}
assert_log_contains "inspect_unsigned.log" "application/x-raw-disk-image" "Meta-payload contains correct MIME type"
assert_log_contains "inspect_unsigned.log" "$IMAGE_URL" "Meta-payload contains correct URL"
assert_log_contains "inspect_unsigned.log" "$INLINE_HASH" "Meta-payload contains correct hash"
log_success "Meta-payload inspection passed"

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
log_success "Phase 3 complete: BMO meta-URL unsigned delivery with go-fdo-meta-tool"

stop_pid "$FDO_PID" "FDO Server"
FDO_PID=""
rm -f "$ARTIFACT_DIR"/bmo-* "$ARTIFACT_DIR"/examples/bmo-*

# ============================================================
phase "Phase 4: BMO Meta-URL with go-fdo-meta-tool (Signed)"
# ============================================================
narrate "Using go-fdo-meta-tool to create COSE Sign1 signed meta-payloads."
narrate "This exercises the full signing toolchain: key generation,"
narrate "'create-signed', public key export, and device verification."

SIGNER_KEY="$ARTIFACT_DIR/meta-signer.pem"
COSE_KEY="$ARTIFACT_DIR/signer.cbor"
META_SIGNED="$ARTIFACT_DIR/meta-signed.cbor"

# Generate ECDSA P-256 signing key
openssl ecparam -name prime256v1 -genkey -noout -out "$SIGNER_KEY" 2>/dev/null
log_success "Generated signing key: $SIGNER_KEY"

# Create signed meta-payload with go-fdo-meta-tool
log_info "Creating signed meta-payload with go-fdo-meta-tool"
"$META_TOOL" meta create-signed \
    -mime "application/x-raw-disk-image" \
    -url "$IMAGE_URL" \
    -hash-file "$BMO_IMAGE" \
    -name "signed-meta-image" \
    -version "1.0" \
    -description "Signed meta-payload created by go-fdo-meta-tool" \
    -key "$SIGNER_KEY" \
    -out "$META_SIGNED" || {
    log_error "Failed to create signed meta-payload with go-fdo-meta-tool"
    exit 1
}
log_success "Created signed meta-payload: $META_SIGNED"

# Export public key as COSE_Key
"$META_TOOL" meta export-pubkey \
    -key "$SIGNER_KEY" \
    -out "$COSE_KEY" || {
    log_error "Failed to export COSE_Key"
    exit 1
}
log_success "Exported COSE_Key: $COSE_KEY"

# Self-verify with go-fdo-meta-tool
"$META_TOOL" meta verify \
    -in "$META_SIGNED" \
    -key "$SIGNER_KEY" \
    -hash-file "$BMO_IMAGE" > "$ARTIFACT_DIR/verify_signed.log" 2>&1 || {
    log_error "Self-verification of signed meta-payload failed"
    exit 1
}
assert_log_contains "$ARTIFACT_DIR/verify_signed.log" "Signature verified OK" "Signature verification passed"
assert_log_contains "$ARTIFACT_DIR/verify_signed.log" "Hash verification OK" "Hash verification passed"
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
log_success "Phase 4 complete: BMO meta-URL signed delivery with go-fdo-meta-tool"

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

# Verify that go-fdo-meta-tool detects the tampering
log_info "Verifying go-fdo-meta-tool detects tampered signature"
if "$META_TOOL" meta verify -in "$META_SIGNED" -key "$SIGNER_KEY" > "$ARTIFACT_DIR/verify_tampered.log" 2>&1; then
    log_error "go-fdo-meta-tool failed to detect tampered signature"
    exit 1
else
    assert_log_contains "$ARTIFACT_DIR/verify_tampered.log" "signature verification failed" "Meta tool detected tampered signature"
    log_success "go-fdo-meta-tool correctly detected tampered signature"
fi

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

# ============================================================
phase "Phase 6: Negative — Incorrect Hash Verification"
# ============================================================
narrate "SECURITY TEST: Create a meta-payload with incorrect hash"
narrate "and verify that hash verification fails properly."

# Create a different image file
WRONG_IMAGE="$ARTIFACT_DIR/wrong_image.bin"
dd if=/dev/urandom of="$WRONG_IMAGE" bs=1024 count=4 2>/dev/null
# shellcheck disable=SC2034  # WRONG_HASH documents the hash; actual usage is via -hash-file flag
WRONG_HASH=$(sha256sum "$WRONG_IMAGE" | awk '{print $1}')

# Create meta-payload with hash of wrong image but URL to correct image
META_WRONG_HASH="$ARTIFACT_DIR/meta-wrong-hash.cbor"
# First create with wrong hash file, then sign it
"$META_TOOL" meta create \
    -mime "application/x-raw-disk-image" \
    -url "$IMAGE_URL" \
    -hash-file "$WRONG_IMAGE" \
    -name "wrong-hash-image" \
    -out "$META_WRONG_HASH" || {
    log_error "Failed to create meta-payload with wrong hash"
    exit 1
}

# Sign the wrong hash meta-payload so we can test hash verification
"$META_TOOL" meta sign \
    -in "$META_WRONG_HASH" \
    -key "$SIGNER_KEY" \
    -out "$META_WRONG_HASH" || {
    log_error "Failed to sign wrong hash meta-payload"
    exit 1
}

# Verify go-fdo-meta-tool detects hash mismatch
log_info "Verifying go-fdo-meta-tool detects hash mismatch"
if "$META_TOOL" meta verify -in "$META_WRONG_HASH" -key "$SIGNER_KEY" -hash-file "$BMO_IMAGE" > "$ARTIFACT_DIR/verify_wrong_hash.log" 2>&1; then
    log_error "go-fdo-meta-tool failed to detect hash mismatch"
    exit 1
else
    assert_log_contains "$ARTIFACT_DIR/verify_wrong_hash.log" "hash verification failed" "Meta tool detected hash mismatch"
    log_success "go-fdo-meta-tool correctly detected hash mismatch"
fi

# Serve the wrong hash meta-payload
cp "$META_WRONG_HASH" "$ARTIFACT_DIR/meta-wrong-hash.cbor"
META_WRONG_HASH_URL="http://127.0.0.1:$PORT_HTTP/meta-wrong-hash.cbor"

start_bg_server "$DIR_GOFDO/examples" "$FDO_LOG" \
    "$FDO_BIN" server -http "$FDO_ADDR" -db "$FDO_DB" \
    -reuse-cred \
    -bmo-meta-url "$META_WRONG_HASH_URL"
FDO_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_FDO" 30 "FDO Server (wrong hash)" || exit 1

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$FDO_BIN" client -blob cred.bin > to2_wrong_hash.log 2>&1) || TO2_EXIT=$?

# Check that hash verification failed
if [ "$TO2_EXIT" -ne 0 ]; then
    log_success "TO2 correctly failed with wrong hash (exit=$TO2_EXIT)"
else
    # Check logs for hash rejection evidence
    if grep -qi "hash.*mismatch\|hash.*invalid\|hash.*failed\|integrity.*check.*failed" \
        "$ARTIFACT_DIR/to2_wrong_hash.log" "$FDO_LOG" 2>/dev/null; then
        log_success "BMO correctly rejected wrong hash (protocol completed, FSIM failed)"
    else
        log_error "TO2 succeeded unexpectedly with wrong hash"
        exit 1
    fi
fi

stop_pid "$FDO_PID" "FDO Server"
FDO_PID=""

# ============================================================
phase "Phase 7: Positive — Correct Hash Verification"
# ============================================================
narrate "POSITIVE TEST: Verify that correct hash verification works"
narrate "when the meta-payload hash matches the actual file."

# Create a new meta-payload and verify hash
META_CORRECT_HASH="$ARTIFACT_DIR/meta-correct-hash.cbor"
"$META_TOOL" meta create-signed \
    -mime "application/x-raw-disk-image" \
    -url "$IMAGE_URL" \
    -hash-file "$BMO_IMAGE" \
    -name "correct-hash-image" \
    -key "$SIGNER_KEY" \
    -out "$META_CORRECT_HASH" || {
    log_error "Failed to create meta-payload with correct hash"
    exit 1
}

# Verify hash matches with go-fdo-meta-tool
"$META_TOOL" meta verify \
    -in "$META_CORRECT_HASH" \
    -key "$SIGNER_KEY" \
    -hash-file "$BMO_IMAGE" > "$ARTIFACT_DIR/verify_correct_hash.log" 2>&1 || {
    log_error "Correct hash verification failed"
    exit 1
}
assert_log_contains "$ARTIFACT_DIR/verify_correct_hash.log" "Hash verification OK" "Correct hash verification passed"
log_success "go-fdo-meta-tool verified correct hash"

# Serve the correct hash meta-payload
cp "$META_CORRECT_HASH" "$ARTIFACT_DIR/meta-correct-hash.cbor"
META_CORRECT_HASH_URL="http://127.0.0.1:$PORT_HTTP/meta-correct-hash.cbor"

start_bg_server "$DIR_GOFDO/examples" "$FDO_LOG" \
    "$FDO_BIN" server -http "$FDO_ADDR" -db "$FDO_DB" \
    -reuse-cred \
    -bmo-meta-url "$META_CORRECT_HASH_URL:$COSE_KEY"
FDO_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_FDO" 30 "FDO Server (correct hash)" || exit 1

TO2_EXIT=0
(cd "$ARTIFACT_DIR" && "$FDO_BIN" client -blob cred.bin > to2_correct_hash.log 2>&1) || TO2_EXIT=$?
assert_equals "0" "$TO2_EXIT" "TO2 with correct hash should succeed"

assert_log_contains "$ARTIFACT_DIR/to2_correct_hash.log" \
    "HandleImage\|Saved image\|fdo.bmo" \
    "Client received BMO image with correct hash verification"
log_success "Phase 7 complete: Correct hash verification"

stop_pid "$FDO_PID" "FDO Server"
FDO_PID=""
stop_pid "$HTTP_PID" "HTTP File Server"
HTTP_PID=""

# ============================================================
phase "Verification Summary"
# ============================================================
narrate "All Enhanced BMO Meta-URL phases completed:"
narrate "  ✓ Phase 1: Bootstrap (DI + initial TO2 with credential reuse)"
narrate "  ✓ Phase 2: BMO inline delivery"
narrate "  ✓ Phase 3: BMO meta-URL unsigned with go-fdo-meta-tool"
narrate "  ✓ Phase 4: BMO meta-URL signed with go-fdo-meta-tool"
narrate "  ✓ Phase 5: Negative test (tampered signature rejected)"
narrate "  ✓ Phase 6: Negative test (wrong hash rejected)"
narrate "  ✓ Phase 7: Positive test (correct hash verification)"

narrate "go-fdo-meta-tool integration verified:"
narrate "  ✓ Creates compatible unsigned meta-payloads"
narrate "  ✓ Creates compatible signed meta-payloads"
narrate "  ✓ Exports COSE_Key for FDO server"
narrate "  ✓ Detects signature tampering"
narrate "  ✓ Detects hash mismatches"
narrate "  ✓ Verifies correct signatures and hashes"

show_file_listing "$ARTIFACT_DIR" "All Scenario 8 Enhanced Artifacts"

print_summary
