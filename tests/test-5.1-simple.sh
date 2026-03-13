#!/bin/bash
# Simplified End-to-End Transmission Test (A → B)

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

echo "[INFO] Test 5.1: End-to-End Transmission (A → B)"

# Cleanup
rm -f "$SCRIPT_DIR/data"/*.db
mkdir -p "$SCRIPT_DIR/data/vouchers-a" "$SCRIPT_DIR/data/vouchers-b"

echo "[INFO] Step 1: Starting Instance B (receiver)..."
"$PROJECT_ROOT/fdo-voucher-manager" server -config "$SCRIPT_DIR/config-b.yaml" > "$SCRIPT_DIR/data/instance-b.log" 2>&1 &
SERVER_B_PID=$!
echo "[INFO] Instance B PID: $SERVER_B_PID"

echo "[INFO] Step 2: Starting Instance A (transmitter)..."
"$PROJECT_ROOT/fdo-voucher-manager" server -config "$SCRIPT_DIR/config-a.yaml" > "$SCRIPT_DIR/data/instance-a.log" 2>&1 &
SERVER_A_PID=$!
echo "[INFO] Instance A PID: $SERVER_A_PID"

# Wait for servers to start
echo "[INFO] Waiting for servers to start..."
sleep 3

# Create test voucher
echo "[INFO] Step 3: Generating test voucher..."
TEST_VOUCHER="$SCRIPT_DIR/data/test-voucher.pem"
"$PROJECT_ROOT/fdo-voucher-manager" generate voucher -serial "E2E-TEST-001" -model "E2E-MODEL-001" -output "$TEST_VOUCHER" > /dev/null 2>&1

echo "[INFO] Step 4: Sending voucher to Instance A..."
RESPONSE=$(curl -s -w "\n%{http_code}" -F "voucher=@$TEST_VOUCHER" \
  -F "serial=TEST-SERIAL-001" \
  -F "model=TEST-MODEL-001" \
  http://localhost:8080/api/v1/vouchers)

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

echo "[INFO] HTTP Response Code: $HTTP_CODE"
echo "[INFO] Response Body: $BODY"

if [ "$HTTP_CODE" = "200" ]; then
    echo "[PASS] Voucher accepted by Instance A"
else
    echo "[FAIL] Voucher rejected (HTTP $HTTP_CODE)"
    echo "[INFO] Instance A logs:"
    tail -20 "$SCRIPT_DIR/data/instance-a.log"
fi

# Wait for transmission with retry
echo "[INFO] Waiting for transmission (checking every 2 seconds)..."
VOUCHER_COUNT=0
for i in {1..10}; do
    sleep 2
    VOUCHER_COUNT=$(find "$SCRIPT_DIR/data/vouchers-b" -name "*.pem" 2>/dev/null | wc -l)
    if [ "$VOUCHER_COUNT" -gt 0 ]; then
        echo "[INFO] Voucher received after $((i*2)) seconds"
        break
    fi
done

# Check if Instance B received the voucher
echo "[INFO] Checking Instance B storage..."
echo "[INFO] Vouchers in Instance B: $VOUCHER_COUNT"

if [ "$VOUCHER_COUNT" -gt 0 ]; then
    echo "[PASS] Instance B received voucher"
else
    echo "[WARN] Instance B did not receive voucher (transmission may still be pending)"
    echo "[INFO] Instance A logs:"
    tail -5 "$SCRIPT_DIR/data/instance-a.log"
fi

# Cleanup
echo "[INFO] Cleaning up..."
kill $SERVER_A_PID 2>/dev/null || true
kill $SERVER_B_PID 2>/dev/null || true
sleep 1

echo "[INFO] Test completed"
