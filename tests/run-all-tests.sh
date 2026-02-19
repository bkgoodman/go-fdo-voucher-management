#!/bin/bash
# Master test runner - executes all test categories

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

main() {
    log_info "FDO Voucher Manager - Comprehensive Test Suite"
    echo ""
    
    check_binary || exit 1
    init_test_env
    
    # Run test categories
    local failed=0
    
    log_info "Running Category 1: Basic Reception Tests"
    if bash "$SCRIPT_DIR/test-1.1-receive-valid-voucher.sh"; then
        log_success "Category 1 passed"
    else
        log_error "Category 1 failed"
        ((failed++))
    fi
    
    log_info "Running Category 5: Dual-Instance Transmission Tests"
    if bash "$SCRIPT_DIR/test-5.1-end-to-end-transmission.sh"; then
        log_success "Category 5 passed"
    else
        log_error "Category 5 failed"
        ((failed++))
    fi
    
    cleanup_test_env
    
    echo ""
    if [ $failed -eq 0 ]; then
        log_success "All test categories passed!"
        return 0
    else
        log_error "$failed test category(ies) failed"
        return 1
    fi
}

main "$@"
