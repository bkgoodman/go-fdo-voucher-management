#!/bin/bash
# Simple test to verify server startup works

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

echo "[INFO] Starting simple server test..."

# Start server
echo "[INFO] Starting server on port 8081..."
"$PROJECT_ROOT/fdo-voucher-manager" server -config "$SCRIPT_DIR/config-b.yaml" > /tmp/server.log 2>&1 &
SERVER_PID=$!

echo "[INFO] Server PID: $SERVER_PID"

# Wait for server to start
sleep 2

# Test if server is responding
echo "[INFO] Testing server endpoint..."
if curl -s -f http://localhost:8081/api/v1/vouchers > /dev/null 2>&1; then
    echo "[PASS] Server is responding"
else
    echo "[FAIL] Server is not responding"
    echo "[INFO] Server logs:"
    cat /tmp/server.log
    kill $SERVER_PID 2>/dev/null || true
    exit 1
fi

# Cleanup
echo "[INFO] Stopping server..."
kill $SERVER_PID 2>/dev/null || true
sleep 1

echo "[PASS] Test completed successfully"
