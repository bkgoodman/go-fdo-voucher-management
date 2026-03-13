#!/bin/bash
# Ownership Validation Test
# Tests that the service accepts vouchers signed to its key and rejects others

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

echo "[INFO] Ownership Validation Test"
echo "[INFO] ======================================"

# Cleanup
rm -f "$SCRIPT_DIR/data"/*.db
mkdir -p "$SCRIPT_DIR/data/vouchers-test"

# Step 1: Export service's owner key
echo "[INFO] Step 1: Exporting service owner key..."
SERVICE_KEY="$SCRIPT_DIR/data/service-key.pem"
"$PROJECT_ROOT/fdo-voucher-manager" keys export -config "$SCRIPT_DIR/config-b.yaml" -output "$SERVICE_KEY" > /dev/null 2>&1
echo "[PASS] Service key exported"

# Step 2: Generate a different owner key for testing
echo "[INFO] Step 2: Generating different owner key..."
OTHER_KEY="$SCRIPT_DIR/data/other-key.pem"
"$PROJECT_ROOT/fdo-voucher-manager" generate voucher -serial "OTHER-001" -model "OTHER-MODEL" -output "$OTHER_KEY" > /dev/null 2>&1
# Extract just the key part from the voucher (this is a placeholder - in real scenario would be actual key)
echo "[PASS] Other key generated"

# Step 3: Create config with ownership validation enabled
echo "[INFO] Step 3: Creating config with ownership validation..."
CONFIG_VALIDATE="$SCRIPT_DIR/data/config-validate.yaml"
cp "$SCRIPT_DIR/config-b.yaml" "$CONFIG_VALIDATE"
sed -i 's/validate_ownership: false/validate_ownership: true/' "$CONFIG_VALIDATE"
echo "[PASS] Config created with validation enabled"

# Step 4: Start server with validation enabled
echo "[INFO] Step 4: Starting server with ownership validation..."
"$PROJECT_ROOT/fdo-voucher-manager" server -config "$CONFIG_VALIDATE" > "$SCRIPT_DIR/data/server-validate.log" 2>&1 &
SERVER_PID=$!
echo "[INFO] Server PID: $SERVER_PID"

# Wait for server to start and port to be listening
echo "[INFO] Waiting for server to be ready..."
for _ in {1..30}; do
    if netstat -tlnp 2>/dev/null | grep -q ":8081 "; then
        echo "[PASS] Server ready"
        break
    fi
    sleep 0.5
done

# Step 5: Generate voucher signed to SERVICE key
echo "[INFO] Step 5: Generating voucher signed to service key..."
VOUCHER_VALID="$SCRIPT_DIR/data/voucher-valid.pem"
"$PROJECT_ROOT/fdo-voucher-manager" generate voucher -serial "VALID-001" -model "VALID-MODEL" -owner-key "$SERVICE_KEY" -output "$VOUCHER_VALID" > /dev/null 2>&1
echo "[PASS] Valid voucher generated"

# Step 6: Generate voucher signed to OTHER key
echo "[INFO] Step 6: Generating voucher signed to other key..."
VOUCHER_INVALID="$SCRIPT_DIR/data/voucher-invalid.pem"
"$PROJECT_ROOT/fdo-voucher-manager" generate voucher -serial "INVALID-001" -model "INVALID-MODEL" -output "$VOUCHER_INVALID" > /dev/null 2>&1
echo "[PASS] Invalid voucher generated"

# Step 7: Test accepting valid voucher
echo "[INFO] Step 7: Testing valid voucher (signed to service key)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -F "voucher=@$VOUCHER_VALID" \
  -F "serial=VALID-001" \
  -F "model=VALID-MODEL" \
  http://localhost:8081/api/v1/vouchers)

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" = "200" ]; then
    echo "[PASS] Valid voucher accepted (HTTP 200)"
else
    echo "[FAIL] Valid voucher rejected (HTTP $HTTP_CODE)"
    echo "[INFO] Response: $BODY"
fi

# Step 8: Test rejecting invalid voucher
echo "[INFO] Step 8: Testing invalid voucher (signed to different key)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -F "voucher=@$VOUCHER_INVALID" \
  -F "serial=INVALID-001" \
  -F "model=INVALID-MODEL" \
  http://localhost:8081/api/v1/vouchers)

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" = "403" ] || [ "$HTTP_CODE" = "400" ]; then
    echo "[PASS] Invalid voucher rejected (HTTP $HTTP_CODE)"
else
    echo "[FAIL] Invalid voucher accepted (HTTP $HTTP_CODE)"
    echo "[INFO] Response: $BODY"
fi

# Cleanup
echo "[INFO] Cleaning up..."
kill $SERVER_PID 2>/dev/null || true
sleep 1

echo "[INFO] Test completed"
