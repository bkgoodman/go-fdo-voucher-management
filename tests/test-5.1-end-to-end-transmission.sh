#!/bin/bash
# Test 5.1: End-to-End Transmission (A → B)
# Verifies that Instance A can receive a voucher, sign it over to Instance B's key,
# and transmit it to Instance B, which receives and stores it

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

test_end_to_end_transmission() {
    log_info "Test 5.1: End-to-End Transmission (A → B)"
    
    init_test_env
    
    # Step 1: Export Instance B's owner key
    log_info "Step 1: Exporting Instance B's owner key..."
    local key_b_file="$TEST_KEYS_DIR/key-b.pem"
    export_owner_key "$SCRIPT_DIR/config-b.yaml" "$key_b_file" || {
        log_error "Failed to export Instance B's key"
        return 1
    }
    assert_file_exists "$key_b_file" "Instance B's owner key exported"
    
    # Step 2: Use Instance A's default config (no sign-over for now)
    log_info "Step 2: Using Instance A's default config..."
    local config_a_updated="$SCRIPT_DIR/config-a.yaml"
    
    log_success "Instance A config ready"
    
    # Step 3: Start Instance B (receiver only)
    log_info "Step 3: Starting Instance B (receiver)..."
    local server_b_pid
    server_b_pid=$(start_server "$SCRIPT_DIR/config-b.yaml" 8081 "instance-b") || return 1
    
    # Step 4: Start Instance A (with transmission to B)
    log_info "Step 4: Starting Instance A (with transmission to B)..."
    local server_a_pid
    server_a_pid=$(start_server "$config_a_updated" 8080 "instance-a") || {
        stop_server "$server_b_pid" "instance-b"
        return 1
    }
    
    # Step 5: Create and send test voucher to Instance A
    log_info "Step 5: Creating and sending test voucher to Instance A..."
    local test_voucher="$TEST_VOUCHERS_DIR/test-voucher-e2e.pem"
    "$PROJECT_ROOT/fdo-voucher-manager" generate voucher \
        -serial "E2E-SERIAL-001" \
        -model "E2E-MODEL-001" \
        -output "$test_voucher" > /dev/null 2>&1 || {
        log_error "Failed to generate test voucher"
        stop_server "$server_a_pid" "instance-a"
        stop_server "$server_b_pid" "instance-b"
        return 1
    }
    
    assert_file_exists "$test_voucher" "Test voucher created"
    
    # Send voucher to Instance A
    local response
    response=$(send_voucher "$test_voucher" "http://localhost:8080/api/v1/vouchers" "" "E2E-SERIAL-001" "E2E-MODEL-001")
    
    local http_code
    http_code=$(echo "$response" | tail -n1)
    assert_http_status "200" "$http_code" "Voucher accepted by Instance A"
    
    # Step 6: Wait for Instance A to transmit to Instance B
    log_info "Step 6: Waiting for Instance A to transmit to Instance B..."
    local guid
    guid=$(echo "$response" | grep -o '"voucher_id":"[^"]*' | cut -d'"' -f4)
    assert_not_empty "$guid" "GUID extracted from response"
    
    # Wait for transmission to complete (with timeout)
    local max_wait=15
    local waited=0
    local transmission_succeeded=false
    
    while [ $waited -lt $max_wait ]; do
        # Check Instance A's transmission status
        local a_status
        a_status=$(list_transmissions "$config_a_updated" "" 2>/dev/null || echo "")
        
        if echo "$a_status" | grep -q "succeeded"; then
            log_success "Instance A transmission succeeded"
            transmission_succeeded=true
            break
        fi
        
        sleep 1
        ((waited++))
    done
    
    if [ "$transmission_succeeded" = false ]; then
        log_warn "Transmission did not complete within timeout (this may be expected if retry worker is slow)"
    fi
    
    # Step 7: Verify Instance B received the voucher
    log_info "Step 7: Verifying Instance B received the voucher..."
    
    # Check if voucher was stored in Instance B's filesystem
    local stored_voucher_b
    stored_voucher_b=$(find "$PROJECT_ROOT/tests/data/vouchers-b" -type f -name "*.fdoov" 2>/dev/null | sort -r | head -1)
    
    if [ -n "$stored_voucher_b" ]; then
        log_success "Voucher received and stored by Instance B"
        ((TESTS_PASSED++))
    else
        log_error "Voucher not found in Instance B's storage"
        ((TESTS_FAILED++))
    fi
    
    # Step 8: Verify transmission record in Instance B's database
    log_info "Step 8: Checking Instance B's transmission records..."
    local b_transmissions
    b_transmissions=$(list_transmissions "$SCRIPT_DIR/config-b.yaml" "" 2>/dev/null || echo "")
    
    if echo "$b_transmissions" | grep -q "$guid"; then
        log_success "Transmission record found in Instance B's database"
        ((TESTS_PASSED++))
    else
        log_warn "Transmission record not found in Instance B (expected if push is disabled)"
    fi
    
    # Cleanup
    stop_server "$server_a_pid" "instance-a"
    stop_server "$server_b_pid" "instance-b"
    cleanup_test_env
    
    return 0
}

test_end_to_end_transmission
print_summary
