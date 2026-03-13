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
echo "[INFO] Waiting for server to start..."
sleep 5

# Test if server is responding
echo "[INFO] Testing server endpoint..."
for i in {1..5}; do
    if curl -s http://localhost:8081/api/v1/vouchers > /dev/null 2>&1; then
        echo "[PASS] Server is responding"
        break
    else
        echo "[INFO] Attempt $i: Server not ready yet, waiting..."
        sleep 1
        if [ "$i" -eq 5 ]; then
            echo "[FAIL] Server is not responding after 5 attempts"
            echo "[INFO] Server logs:"
            cat /tmp/server.log
            kill $SERVER_PID 2>/dev/null || true
            exit 1
        fi
    fi
done

# Cleanup
echo "[INFO] Stopping server..."
kill $SERVER_PID 2>/dev/null || true
sleep 1

echo "[PASS] Test completed successfully"
