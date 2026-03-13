#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Shared helpers for FDO full-stack integration super-tests.
# Source this file from each scenario script.

set -u

# ============================================================
# Project directories
# ============================================================
DIR_MFG="/home/windsurf/go-fdo-di"
DIR_ENDPOINT="/var/bkgdata/go-fdo-endpoint"
DIR_VM="/var/bkgdata/go-fdo-voucher-managment"
DIR_RV="/var/bkgdata/go-fdo-rendevzous"
DIR_OBS="/var/bkgdata/go-fdo-onboarding-service"

# Binary names (relative to project dirs)
BIN_MFG="$DIR_MFG/fdo-manufacturing-station"
BIN_ENDPOINT="$DIR_ENDPOINT/fdo-client"
BIN_VM="$DIR_VM/fdo-voucher-manager"
BIN_RV="$DIR_RV/fdo-rendezvous"
BIN_OBS="$DIR_OBS/fdo-onboarding-service"

# ============================================================
# Colors
# ============================================================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
NC='\033[0m'

# ============================================================
# Test counters
# ============================================================
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Track PIDs for cleanup
declare -a SUPERTEST_PIDS=()

# ============================================================
# Logging helpers
# ============================================================
banner() {
    echo ""
    echo -e "${BLUE}${BOLD}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}${BOLD}║  $1${NC}"
    echo -e "${BLUE}${BOLD}╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
}

phase() {
    echo ""
    echo -e "${CYAN}━━━ Phase: $1 ━━━${NC}"
    echo ""
}

narrate() {
    echo -e "${MAGENTA}    📖 $1${NC}"
}

show_item() {
    echo -e "${YELLOW}    ➤ $1${NC}"
}

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

# ============================================================
# Assertions
# ============================================================
assert_equals() {
    local expected="$1"
    local actual="$2"
    local message="${3:-Assertion failed}"
    ((TESTS_RUN++))
    if [ "$expected" = "$actual" ]; then
        log_success "$message"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (expected: '$expected', got: '$actual')"
        ((TESTS_FAILED++))
        return 0
    fi
}

assert_not_empty() {
    local value="$1"
    local message="${2:-Value should not be empty}"
    ((TESTS_RUN++))
    if [ -n "$value" ]; then
        log_success "$message"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (value was empty)"
        ((TESTS_FAILED++))
        return 0
    fi
}

assert_file_exists() {
    local file="$1"
    local message="${2:-File should exist: $file}"
    ((TESTS_RUN++))
    if [ -f "$file" ]; then
        log_success "$message"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (not found)"
        ((TESTS_FAILED++))
        return 0
    fi
}

assert_dir_not_empty() {
    local dir="$1"
    local pattern="${2:-*}"
    local message="${3:-Directory should contain files}"
    ((TESTS_RUN++))
    local count
    count=$(find "$dir" -name "$pattern" -type f 2>/dev/null | wc -l)
    if [ "$count" -gt 0 ]; then
        log_success "$message ($count file(s))"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (directory empty or missing)"
        ((TESTS_FAILED++))
        return 0
    fi
}

assert_http_ok() {
    local url="$1"
    local message="${2:-HTTP request should succeed}"
    ((TESTS_RUN++))
    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" "$url" 2>/dev/null)
    if [ "$http_code" = "200" ]; then
        log_success "$message (HTTP $http_code)"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (HTTP $http_code)"
        ((TESTS_FAILED++))
        return 0
    fi
}

assert_log_contains() {
    local logfile="$1"
    local pattern="$2"
    local message="${3:-Log should contain pattern}"
    ((TESTS_RUN++))
    if grep -q "$pattern" "$logfile" 2>/dev/null; then
        log_success "$message"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (pattern '$pattern' not found in $logfile)"
        ((TESTS_FAILED++))
        return 0
    fi
}

assert_count_gt() {
    local actual="$1"
    local threshold="$2"
    local message="${3:-Count should be > $threshold}"
    ((TESTS_RUN++))
    if [ "$actual" -gt "$threshold" ] 2>/dev/null; then
        log_success "$message (got $actual)"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "$message (got $actual, expected > $threshold)"
        ((TESTS_FAILED++))
        return 0
    fi
}

# ============================================================
# Build helpers
# ============================================================
build_binary() {
    local dir="$1"
    local binary="$2"
    local name="$3"

    log_info "Building $name..."
    if [ -f "$binary" ] && [ ! "$(find "$dir" -name '*.go' -newer "$binary" 2>/dev/null | head -1)" ]; then
        log_info "$name binary is up to date"
        return 0
    fi

    if ! (cd "$dir" && go build -o "$(basename "$binary")" .); then
        log_error "Failed to build $name"
        return 1
    fi
    log_success "$name built"
}

build_all() {
    phase "Building all binaries"
    build_binary "$DIR_MFG"      "$BIN_MFG"      "Manufacturing Station" || return 1
    build_binary "$DIR_ENDPOINT" "$BIN_ENDPOINT"  "Device Client"        || return 1
    build_binary "$DIR_VM"       "$BIN_VM"        "Voucher Manager"      || return 1
    build_binary "$DIR_RV"       "$BIN_RV"        "Rendezvous Server"    || return 1
    build_binary "$DIR_OBS"      "$BIN_OBS"       "Onboarding Service"   || return 1
    log_success "All binaries built"
}

# ============================================================
# Port and process management
# ============================================================
kill_port() {
    local port="$1"
    local pids
    pids=$(lsof -ti "tcp:$port" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        log_warn "Killing stale process(es) on port $port (PIDs: $pids)"
        echo "$pids" | xargs kill 2>/dev/null || true
        sleep 0.5
        echo "$pids" | xargs kill -9 2>/dev/null || true
        sleep 0.3
    fi
}

kill_ports() {
    for port in "$@"; do
        kill_port "$port"
    done
}

wait_for_port() {
    local port="$1"
    local timeout="${2:-30}"
    local label="${3:-server}"
    local elapsed=0

    while [ "$elapsed" -lt "$timeout" ]; do
        if netstat -tlnp 2>/dev/null | grep -q ":${port} "; then
            return 0
        fi
        sleep 0.5
        elapsed=$((elapsed + 1))
    done
    log_error "$label did not start on port $port within ${timeout}s"
    return 1
}

# Start a server in the background. Sets SUPERTEST_LAST_PID.
# Usage: start_bg_server <workdir> <logfile> <binary> <args...>
#   Then read $SUPERTEST_LAST_PID for the PID.
SUPERTEST_LAST_PID=""
start_bg_server() {
    local workdir="$1"
    local logfile="$2"
    local binary="$3"
    shift 3
    (cd "$workdir" && exec "$binary" "$@") > "$logfile" 2>&1 &
    SUPERTEST_LAST_PID=$!
    SUPERTEST_PIDS+=("$SUPERTEST_LAST_PID")
}

stop_pid() {
    local pid="$1"
    local label="${2:-process}"
    if [ -z "$pid" ] || [ "$pid" = "0" ]; then
        return
    fi
    if kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        local i=0
        while [ $i -lt 20 ] && kill -0 "$pid" 2>/dev/null; do
            sleep 0.25
            i=$((i + 1))
        done
        kill -9 "$pid" 2>/dev/null || true
    fi
}

cleanup_all_pids() {
    for pid in "${SUPERTEST_PIDS[@]:-}"; do
        stop_pid "$pid" 2>/dev/null || true
    done
    SUPERTEST_PIDS=()
}

# ============================================================
# Artifact directory management
# ============================================================
init_artifact_dir() {
    local scenario="$1"
    ARTIFACT_DIR="/tmp/fdo_supertest_${scenario}"
    rm -rf "$ARTIFACT_DIR"
    mkdir -p "$ARTIFACT_DIR"
    export ARTIFACT_DIR
    log_info "Artifacts: $ARTIFACT_DIR"
}

# ============================================================
# Config generation helpers
# ============================================================

# Generate Manufacturing Station config
gen_mfg_config() {
    local port="$1"
    local db_path="$2"
    local voucher_dir="$3"
    local owner_pub_key="$4"    # PEM string or file path
    local push_url="${5:-}"
    local push_auth_key="${6:-}"  # Owner key for FDOKeyAuth (replaces static token)
    local signover_key="${7:-}"   # Optional signover key
    local rv_host="${8:-}"
    local rv_port="${9:-}"
    local rv_scheme="${10:-http}"

    local owner_key_block=""
    if [ -f "$owner_pub_key" ]; then
        owner_key_block=$(sed 's/^/      /' "$owner_pub_key")
    else
        # shellcheck disable=SC2001
        owner_key_block=$(echo "$owner_pub_key" | sed 's/^/      /')
    fi

    # Use signover key if provided, otherwise use owner key
    local signover_block=""
    if [ -n "$signover_key" ] && [ -f "$signover_key" ]; then
        signover_block=$(sed 's/^/      /' "$signover_key")
    else
        signover_block="$owner_key_block"
    fi

    local rv_block=""
    if [ -n "$rv_host" ] && [ -n "$rv_port" ]; then
        rv_block="  entries:
    - host: \"$rv_host\"
      port: $rv_port
      scheme: \"$rv_scheme\""
    else
        rv_block="  entries:
    - host: \"127.0.0.1\"
      port: $port
      scheme: \"http\""
    fi

    local push_block=""
    if [ -n "$push_url" ]; then
        if [ -n "$push_auth_key" ] && [ -f "$push_auth_key" ]; then
            # FDOKeyAuth configuration
            local auth_key_block
            auth_key_block=$(sed 's/^/      /' "$push_auth_key")
            push_block="  push_service:
    enabled: true
    url: \"$push_url\"
    mode: \"send_always\"
    retain_files: true
    delete_after_success: false
    retry_interval: \"2s\"
    max_attempts: 5
    owner_key: |
$auth_key_block"
        else
            # Legacy static token configuration
            push_block="  push_service:
    enabled: true
    url: \"$push_url\"
    auth_token: \"$push_auth_key\"
    mode: \"send_always\"
    retain_files: true
    delete_after_success: false
    retry_interval: \"2s\"
    max_attempts: 5"
        fi
    fi

    cat <<EOCFG
debug: true
fdo_version: 200

server:
  addr: "127.0.0.1:$port"
  ext_addr: "127.0.0.1:$port"
  use_tls: false

database:
  path: "$db_path"
  password: ""

manufacturing:
  device_ca_key_type: "ec384"
  owner_key_type: "ec384"
  generate_certificates: true
  first_time_init: true

rendezvous:
$rv_block

voucher_management:
  persist_to_db: true
  voucher_signing:
    mode: "internal"
    first_time_init: true
  save_to_disk:
    directory: "$voucher_dir"
  owner_signover:
    mode: "static"
    static_public_key: |
$signover_block
$push_block
EOCFG
}

# Generate Onboarding Service config
gen_obs_config() {
    local port="$1"
    local db_path="$2"
    local voucher_dir="$3"
    local receiver_token="${4:-test-token}"
    local rv_host="${5:-}"
    local rv_port="${6:-}"
    local rv_scheme="${7:-http}"
    local to0_delegate="${8:-}"
    local onboard_delegate="${9:-}"

    local rv_block=""
    if [ -n "$rv_host" ] && [ -n "$rv_port" ]; then
        rv_block="  entries:
    - host: \"$rv_host\"
      port: $rv_port
      scheme: \"$rv_scheme\""
    else
        rv_block="  entries: []"
    fi

    local to0_block="to0:
  bypass: false
  delay: 0
  replacement_policy: \"allow-any\"
  rv_filter:
    mode: \"allow_all\"
    max_attempts: 5
    retry_interval: 2s"
    if [ -n "$to0_delegate" ]; then
        to0_block="to0:
  delegate: \"$to0_delegate\"
  bypass: false
  delay: 0
  replacement_policy: \"allow-any\"
  rv_filter:
    mode: \"allow_all\"
    max_attempts: 5
    retry_interval: 2s"
    fi

    local delegate_block=""
    if [ -n "$onboard_delegate" ]; then
        delegate_block="delegate:
  onboard: \"$onboard_delegate\""
    fi

    cat <<EOCFG
debug: true
fdo_version: 200

server:
  addr: "127.0.0.1:$port"
  ext_addr: "127.0.0.1:$port"
  use_tls: false

database:
  path: "$db_path"
  password: ""

manufacturing:
  device_ca_key_type: "ec384"
  owner_key_type: "ec384"
  generate_certificates: true
  init_keys_if_missing: true

rendezvous:
$rv_block

$to0_block

$delegate_block

device_storage:
  voucher_dir: "$voucher_dir"
  config_dir: "${ARTIFACT_DIR}/obs_configs"
  delete_after_onboard: false

voucher_management:
  persist_to_db: true
  reuse_credential: true

voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"
  global_token: "$receiver_token"
  validate_ownership: true
  require_auth: true

fsim:
  sysconfig:
    - "hostname=SuperTest-Device"
  command_date: false
EOCFG
}

# Generate Rendezvous Server config
gen_rv_config() {
    local port="$1"
    local db_path="$2"
    local auth_mode="${3:-open}"

    cat <<EOCFG
debug: true

server:
  addr: "127.0.0.1:$port"

database:
  path: "$db_path"

auth:
  mode: "$auth_mode"

rv:
  replacement_policy: "allow-any"
  max_ttl: 4294967295

did:
  refresh_hours: 24
  insecure_http: true

pruning:
  enabled: false
EOCFG
}

# Generate Device/Endpoint config
gen_device_config() {
    local cred_path="$1"
    local di_url="${2:-}"

    cat <<EOCFG
blob_path: "$cred_path"
debug: true
fdo_version: 200

di:
  url: "$di_url"
  key: "ec384"
  key_enc: "x509"

crypto:
  cipher_suite: "A128GCM"
  kex_suite: "ECDH384"

transport:
  insecure_tls: true

operation:
  print_device: false
  rv_only: false

service_info:
  download_dir: ""
  echo_commands: false
  wget_dir: ""
  upload_paths: []
EOCFG
}

# Generate Voucher Manager config
gen_vm_config() {
    local port="$1"
    local db_path="$2"
    local voucher_dir="$3"
    local receiver_token="${4:-}"
    local signover_mode="${5:-static}"
    local signover_key="${6:-}"    # PEM file path or empty
    local signover_did="${7:-}"
    local push_url="${8:-}"
    local push_token="${9:-}"
    local did_host="${10:-}"
    local key_export="${11:-}"

    local signover_block=""
    if [ "$signover_mode" = "static" ] && [ -n "$signover_key" ] && [ -f "$signover_key" ]; then
        local key_indent
        key_indent=$(sed 's/^/    /' "$signover_key")
        signover_block="owner_signover:
  mode: \"static\"
  static_public_key: |
$key_indent"
    elif [ -n "$signover_did" ]; then
        signover_block="owner_signover:
  mode: \"static\"
  static_did: \"$signover_did\""
    else
        signover_block="owner_signover:
  mode: \"static\"
  static_public_key: \"\""
    fi

    local push_block="push_service:
  enabled: false"
    if [ -n "$push_url" ]; then
        push_block="push_service:
  enabled: true
  url: \"$push_url\"
  auth_token: \"$push_token\"
  mode: \"fallback\"
  delete_after_success: false
  retry_interval: 2s
  max_attempts: 5"
    fi

    local did_block=""
    if [ -n "$did_host" ]; then
        did_block="did_minting:
  enabled: true
  host: \"$did_host\"
  key_type: \"ec384\"
  serve_document: true"
        if [ -n "$key_export" ]; then
            did_block="$did_block
  key_export_path: \"$key_export\""
        fi
    fi

    local require_auth="false"
    if [ -n "$receiver_token" ]; then
        require_auth="true"
    fi

    cat <<EOCFG
debug: true

server:
  addr: "127.0.0.1:$port"
  ext_addr: "127.0.0.1:$port"
  use_tls: false

database:
  path: "$db_path"
  password: ""

key_management:
  key_type: "ec384"
  first_time_init: true

voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"
  global_token: "$receiver_token"
  validate_ownership: false
  require_auth: $require_auth

voucher_signing:
  mode: "internal"

$signover_block

voucher_files:
  directory: "$voucher_dir"

$push_block

did_push:
  enabled: true

$did_block

pull_service:
  enabled: true
  session_ttl: 5m
  max_sessions: 100
  token_ttl: 10m

retry_worker:
  enabled: true
  retry_interval: 2s
  max_attempts: 5

retention:
  keep_indefinitely: true
EOCFG
}

# ============================================================
# Show helpers — make test output serve as a tutorial
# ============================================================
show_file_listing() {
    local dir="$1"
    local label="${2:-Files}"
    echo ""
    echo -e "  ${BOLD}$label in $dir:${NC}"
    if [ -d "$dir" ]; then
        find "$dir" -type f | sort | while IFS= read -r f; do
            local sz
            sz=$(stat -c%s "$f" 2>/dev/null || echo "?")
            echo -e "    📄 $(basename "$f")  (${sz} bytes)"
        done
    else
        echo "    (directory not found)"
    fi
    echo ""
}

show_did_document() {
    local url="$1"
    local label="${2:-DID Document}"
    echo ""
    echo -e "  ${BOLD}$label from $url:${NC}"
    local body
    body=$(curl -s "$url" 2>/dev/null)
    if [ -n "$body" ]; then
        echo "$body" | python3 -m json.tool 2>/dev/null | sed 's/^/    /' || echo "    $body"
    else
        echo "    (no response)"
    fi
    echo ""
}

show_rv_blobs() {
    local rv_binary="$1"
    local rv_config="$2"
    local label="${3:-Rendezvous Blob Registry}"
    echo ""
    echo -e "  ${BOLD}$label:${NC}"
    "$rv_binary" -config "$rv_config" -list-blobs 2>/dev/null | sed 's/^/    /' || echo "    (no blobs)"
    echo ""
}

show_key_fingerprint() {
    local keyfile="$1"
    local label="${2:-Key}"
    if [ -f "$keyfile" ]; then
        local fp
        fp=$(openssl pkey -pubin -in "$keyfile" -outform DER 2>/dev/null | sha256sum | cut -d' ' -f1)
        echo -e "    🔑 $label fingerprint: ${fp:0:16}..."
    fi
}

# ============================================================
# Extract owner PRIVATE key from OBS SQLite database as PEM
# ============================================================
extract_obs_private_key() {
    local db_path="$1"
    local output_file="$2"

    if ! command -v sqlite3 &>/dev/null; then
        log_error "sqlite3 not found — cannot extract OBS private key"
        return 1
    fi
    # owner_keys.pkcs8 is raw DER PKCS8 bytes; extract as hex, convert to PEM
    local hex
    # Type 11 = SECP384R1 (EC384), which is used by default in OBS configs
    hex=$(sqlite3 "$db_path" "SELECT hex(pkcs8) FROM owner_keys WHERE type=11 LIMIT 1" 2>/dev/null)
    if [ -z "$hex" ]; then
        # Fallback: try SECP256R1 (type 10), then any key
        hex=$(sqlite3 "$db_path" "SELECT hex(pkcs8) FROM owner_keys WHERE type=10 LIMIT 1" 2>/dev/null)
    fi
    if [ -z "$hex" ]; then
        hex=$(sqlite3 "$db_path" "SELECT hex(pkcs8) FROM owner_keys LIMIT 1" 2>/dev/null)
    fi
    if [ -z "$hex" ]; then
        log_error "No owner key found in OBS database $db_path"
        return 1
    fi
    {
        echo "-----BEGIN PRIVATE KEY-----"
        echo "$hex" | xxd -r -p | base64 | fold -w 64
        echo "-----END PRIVATE KEY-----"
    } > "$output_file"
}

# ============================================================
# Extract owner public key from Onboarding Service
# ============================================================
extract_obs_owner_key() {
    local obs_binary="$1"
    local obs_config="$2"
    local output_file="$3"

    local raw
    raw=$("$obs_binary" -config "$obs_config" -print-owner-key 2>/dev/null)
    # Extract the EC-384 key block
    local key
    key=$(echo "$raw" | sed -n '/--- SECP384R1 ---/,$p' | sed -n '/-----BEGIN PUBLIC KEY-----/,/-----END PUBLIC KEY-----/p')
    if [ -z "$key" ]; then
        # Fallback: grab any public key block
        key=$(echo "$raw" | sed -n '/-----BEGIN PUBLIC KEY-----/,/-----END PUBLIC KEY-----/p' | head -6)
    fi
    if [ -z "$key" ]; then
        log_error "Failed to extract owner public key from OBS"
        return 1
    fi
    echo "$key" > "$output_file"
    log_success "OBS owner public key saved to $output_file"
}

# ============================================================
# Summary
# ============================================================
print_summary() {
    echo ""
    echo -e "${BOLD}══════════════════════════════════════${NC}"
    echo -e "${BOLD} Test Summary${NC}"
    echo -e "${BOLD}══════════════════════════════════════${NC}"
    echo -e "  Total:  $((TESTS_PASSED + TESTS_FAILED))"
    echo -e "  ${GREEN}Passed: $TESTS_PASSED${NC}"
    if [ "$TESTS_FAILED" -gt 0 ]; then
        echo -e "  ${RED}Failed: $TESTS_FAILED${NC}"
        echo ""
        return 1
    else
        echo -e "  ${GREEN}Failed: 0${NC}"
        echo ""
        return 0
    fi
}
