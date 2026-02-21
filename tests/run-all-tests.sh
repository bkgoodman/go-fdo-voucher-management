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
    
    log_info "Running Category 2: Ownership Validation Tests"
    if bash "$SCRIPT_DIR/test-ownership-validation.sh"; then
        log_success "Category 2 passed"
    else
        log_error "Category 2 failed"
        ((failed++))
    fi
    
    log_info "Running Category 3: Pull Auth & DID Tests"
    if bash "$SCRIPT_DIR/test-pull-auth-and-did.sh"; then
        log_success "Category 3 passed"
    else
        log_error "Category 3 failed"
        ((failed++))
    fi
    
    log_info "Running Category 4: E2E DID Push-Pull Tests"
    if bash "$SCRIPT_DIR/test-e2e-did-push-pull.sh"; then
        log_success "Category 4 passed"
    else
        log_error "Category 4 failed"
        ((failed++))
    fi
    
    log_info "Running Category 5: Simple Server Tests"
    if bash "$SCRIPT_DIR/test-simple-server.sh"; then
        log_success "Category 5 passed"
    else
        log_error "Category 5 failed"
        ((failed++))
    fi
    
    log_info "Running Category 6: Dual-Instance Transmission Tests"
    if bash "$SCRIPT_DIR/test-5.1-end-to-end-transmission.sh"; then
        log_success "Category 6 passed"
    else
        log_error "Category 6 failed"
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
