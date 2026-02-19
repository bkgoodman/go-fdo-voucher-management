#!/bin/bash
# Common test utilities and helper functions

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
TEST_DATA_DIR="$SCRIPT_DIR/data"
TEST_KEYS_DIR="$SCRIPT_DIR/keys"
TEST_VOUCHERS_DIR="$SCRIPT_DIR/vouchers"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Initialize test environment
init_test_env() {
    mkdir -p "$TEST_DATA_DIR"
    mkdir -p "$TEST_KEYS_DIR"
    mkdir -p "$TEST_VOUCHERS_DIR"
    mkdir -p "$TEST_DATA_DIR/vouchers-a"
    mkdir -p "$TEST_DATA_DIR/vouchers-b"
}

# Clean up test environment
cleanup_test_env() {
    rm -f "$TEST_DATA_DIR"/*.db 2>/dev/null || true
    rm -f "$TEST_DATA_DIR"/*.db-* 2>/dev/null || true
    rm -rf "$TEST_DATA_DIR/vouchers-a"/* 2>/dev/null || true
    rm -rf "$TEST_DATA_DIR/vouchers-b"/* 2>/dev/null || true
}

# Log functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $*"
}

log_error() {
    echo -e "${RED}[FAIL]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

# Test assertion functions
assert_equals() {
    local expected="$1"
    local actual="$2"
    local message="${3:-Assertion failed}"
    
    if [ "$expected" = "$actual" ]; then
        log_success "$message"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (expected: '$expected', got: '$actual')"
        ((TESTS_FAILED++))
        return 1
    fi
}

assert_not_empty() {
    local value="$1"
    local message="${2:-Value should not be empty}"
    
    if [ -n "$value" ]; then
        log_success "$message"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message"
        ((TESTS_FAILED++))
        return 1
    fi
}

assert_file_exists() {
    local file="$1"
    local message="${2:-File should exist: $file}"
    
    if [ -f "$file" ]; then
        log_success "$message"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message"
        ((TESTS_FAILED++))
        return 1
    fi
}

assert_http_status() {
    local expected="$1"
    local actual="$2"
    local message="${3:-HTTP status assertion}"
    
    if [ "$expected" = "$actual" ]; then
        log_success "$message (status: $actual)"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (expected: $expected, got: $actual)"
        ((TESTS_FAILED++))
        return 1
    fi
}

# Start server in background
start_server() {
    local config="$1"
    local port="${2:-8080}"
    local instance_name="${3:-server}"
    
    log_info "Starting $instance_name on port $port..." >&2
    
    "$PROJECT_ROOT/fdo-voucher-manager" server -config "$config" > "$TEST_DATA_DIR/${instance_name}.log" 2>&1 &
    local pid=$!
    
    # Wait for server to be ready - check if process is still running and port is listening
    local max_attempts=60
    local attempt=0
    while [ $attempt -lt $max_attempts ]; do
        # Check if process is still alive
        if ! kill -0 $pid 2>/dev/null; then
            log_error "Failed to start $instance_name - process died" >&2
            cat "$TEST_DATA_DIR/${instance_name}.log" 2>/dev/null | tail -5 | while read line; do log_error "$line" >&2; done
            return 1
        fi
        
        # Check if port is listening (more reliable than curl)
        if netstat -tlnp 2>/dev/null | grep -q ":$port "; then
            # Give it a moment to fully initialize
            sleep 0.2
            log_success "$instance_name started (PID: $pid)" >&2
            echo "$pid"
            return 0
        fi
        
        sleep 0.5
        ((attempt++))
    done
    
    log_error "Failed to start $instance_name - timeout waiting for server" >&2
    cat "$TEST_DATA_DIR/${instance_name}.log" 2>/dev/null | tail -10 | while read line; do log_error "$line" >&2; done
    kill $pid 2>/dev/null || true
    return 1
}

# Stop server
stop_server() {
    local pid="$1"
    local instance_name="${2:-server}"
    
    if [ -z "$pid" ]; then
        return 0
    fi
    
    log_info "Stopping $instance_name (PID: $pid)..."
    kill $pid 2>/dev/null || true
    
    # Wait for graceful shutdown
    local max_attempts=10
    local attempt=0
    while [ $attempt -lt $max_attempts ] && kill -0 $pid 2>/dev/null; do
        sleep 0.5
        ((attempt++))
    done
    
    # Force kill if still running
    kill -9 $pid 2>/dev/null || true
    log_success "$instance_name stopped"
}

# Send voucher to endpoint
send_voucher() {
    local voucher_file="$1"
    local endpoint="$2"
    local token="${3:-}"
    local serial="${4:-TEST-SERIAL}"
    local model="${5:-TEST-MODEL}"
    
    local curl_opts=(-s -w "\n%{http_code}")
    
    if [ -n "$token" ]; then
        curl_opts+=(-H "Authorization: Bearer $token")
    fi
    
    curl_opts+=(-F "voucher=@$voucher_file")
    curl_opts+=(-F "serial=$serial")
    curl_opts+=(-F "model=$model")
    
    local response
    response=$(curl "${curl_opts[@]}" "$endpoint")
    
    # Last line is HTTP status code
    local http_code=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)
    
    echo "$body"
    echo "$http_code"
}

# Query transmission status
query_transmission() {
    local guid="$1"
    local config="$2"
    
    "$PROJECT_ROOT/fdo-voucher-manager" vouchers show -guid "$guid" -config "$config" 2>/dev/null || echo ""
}

# List transmissions
list_transmissions() {
    local config="$1"
    local status="${2:-}"
    
    local opts=(-config "$config")
    if [ -n "$status" ]; then
        opts+=(-status "$status")
    fi
    
    "$PROJECT_ROOT/fdo-voucher-manager" vouchers list "${opts[@]}" 2>/dev/null || echo ""
}

# Export owner key
export_owner_key() {
    local config="$1"
    local output_file="$2"
    
    "$PROJECT_ROOT/fdo-voucher-manager" keys export -config "$config" -output "$output_file" 2>/dev/null
}

# Add authentication token
add_token() {
    local config="$1"
    local token="$2"
    local description="${3:-Test token}"
    
    "$PROJECT_ROOT/fdo-voucher-manager" tokens add -config "$config" -token "$token" -description "$description" 2>/dev/null
}

# Print test summary
print_summary() {
    echo ""
    echo "======================================"
    echo "Test Summary"
    echo "======================================"
    echo "Total tests: $((TESTS_PASSED + TESTS_FAILED))"
    echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
    if [ $TESTS_FAILED -gt 0 ]; then
        echo -e "${RED}Failed: $TESTS_FAILED${NC}"
        return 1
    else
        echo -e "${GREEN}Failed: 0${NC}"
        return 0
    fi
}

# Run test function
run_test() {
    local test_name="$1"
    local test_func="$2"
    
    echo ""
    echo "=========================================="
    echo "Running: $test_name"
    echo "=========================================="
    
    ((TESTS_RUN++))
    
    if $test_func; then
        log_success "Test completed: $test_name"
    else
        log_error "Test failed: $test_name"
    fi
}

# Wait for condition with timeout
wait_for() {
    local condition="$1"
    local timeout="${2:-30}"
    local message="${3:-Waiting for condition}"
    
    log_info "$message..."
    
    local start=$(date +%s)
    while true; do
        if eval "$condition"; then
            return 0
        fi
        
        local now=$(date +%s)
        if [ $((now - start)) -gt $timeout ]; then
            log_error "Timeout waiting for: $message"
            return 1
        fi
        
        sleep 1
    done
}

# Check if binary exists
check_binary() {
    if [ ! -f "$PROJECT_ROOT/fdo-voucher-manager" ]; then
        log_error "Binary not found: $PROJECT_ROOT/fdo-voucher-manager"
        log_info "Building project..."
        cd "$PROJECT_ROOT"
        go build -o fdo-voucher-manager || {
            log_error "Failed to build project"
            return 1
        }
    fi
    return 0
}

export -f log_info log_success log_error log_warn
export -f assert_equals assert_not_empty assert_file_exists assert_http_status
export -f start_server stop_server send_voucher query_transmission list_transmissions
export -f export_owner_key add_token print_summary run_test wait_for check_binary
export -f init_test_env cleanup_test_env
export SCRIPT_DIR PROJECT_ROOT TEST_DATA_DIR TEST_KEYS_DIR TEST_VOUCHERS_DIR
export RED GREEN YELLOW BLUE NC
