#!/bin/bash
# Test 1.1: Receive Valid Voucher
# Verifies that a valid voucher can be received and stored

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

test_receive_valid_voucher() {
    log_info "Test 1.1: Receive Valid Voucher"
    
    init_test_env
    
    # Start server with auth disabled
    local server_pid
    server_pid=$(start_server "$SCRIPT_DIR/config-a.yaml" 8080 "test-server") || return 1
    
    # Generate a valid test voucher using the voucher generator
    local test_voucher="$TEST_VOUCHERS_DIR/test-voucher.pem"
    "$PROJECT_ROOT/fdo-voucher-manager" generate voucher \
        -serial "TEST-SERIAL-001" \
        -model "TEST-MODEL-001" \
        -output "$test_voucher" > /dev/null 2>&1 || {
        log_error "Failed to generate test voucher"
        return 1
    }
    
    assert_file_exists "$test_voucher" "Test voucher created"
    
    # Send voucher to receiver endpoint
    log_info "Sending voucher to receiver endpoint..."
    local response
    response=$(send_voucher "$test_voucher" "http://localhost:8080/api/v1/vouchers" "" "TEST-SERIAL-001" "TEST-MODEL-001")
    
    local http_code
    http_code=$(echo "$response" | tail -n1)
    local body
    body=$(echo "$response" | head -n-1)
    
    assert_http_status "200" "$http_code" "Voucher received successfully"
    
    # Verify voucher stored to filesystem
    # Note: Server saves vouchers relative to where it's started (project root)
    local stored_voucher
    stored_voucher=$(find "$PROJECT_ROOT/tests/data/vouchers-a" -type f -name "*.fdoov" 2>/dev/null | sort -r | head -1)
    if [ -z "$stored_voucher" ]; then
        log_error "No vouchers found in $PROJECT_ROOT/tests/data/vouchers-a"
        return 1
    fi
    assert_file_exists "$stored_voucher" "Voucher stored to filesystem"
    
    # Verify transmission record created in database
    log_info "Checking transmission record in database..."
    local guid
    guid=$(echo "$body" | grep -o '"voucher_id":"[^"]*' | cut -d'"' -f4)
    assert_not_empty "$guid" "GUID extracted from response"
    
    # Query transmission status
    local transmission_info
    transmission_info=$(list_transmissions "$SCRIPT_DIR/config-a.yaml")
    if echo "$transmission_info" | grep -q "$guid"; then
        log_success "Transmission record found in database"
        ((TESTS_PASSED++))
    else
        log_error "Transmission record not found in database"
        ((TESTS_FAILED++))
    fi
    
    # Cleanup
    stop_server "$server_pid" "test-server"
    cleanup_test_env
    
    return 0
}

test_receive_valid_voucher
print_summary
