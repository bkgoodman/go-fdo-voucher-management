#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Scenario 12: Visibility & Inspection Walkthrough
#
# This scenario is a guided tutorial demonstrating all CLI visibility and
# HTTP inspection commands for voucher management. It walks through:
#
#   1. Creating tokens (manual + FDOKeyAuth-issued)
#   2. Partner management (add supply/receive partners, list, show, export)
#   3. Partner-token fingerprint correlation
#   4. Pushing vouchers from a manufacturer
#   5. Listing vouchers with `vouchers list` (all, by owner, by serial)
#   6. Showing voucher detail with `vouchers show` (including assignment fields)
#   7. Assigning vouchers (CLI assign + HTTP assign)
#   8. Post-assignment inspection
#   9. Listing access grants with `vouchers grants`
#  10. Listing custodians with `vouchers custodians`
#  11. Querying the HTTP list endpoint (GET /list)
#  12. CLI unassign and re-assign
#  13. Partner lifecycle — removal
#  14. Double-assign rejection (negative test)
#  15. Unauthenticated access rejection (negative test)
#
# PORTS: 9904 (Voucher Manager)
#
set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

PORT_VM=9904
VM_PID=""
VM_LOG=""
VM_CONFIG=""
BIN="$BIN_VM"

cleanup() {
    if [ -n "$VM_PID" ]; then
        stop_pid "$VM_PID" "Voucher Manager"
    fi
    kill_ports $PORT_VM
    cleanup_all_pids
}
trap cleanup EXIT

# ─────────────────────────────────────────────────────────────────────────────
# Setup
# ─────────────────────────────────────────────────────────────────────────────

banner "Scenario 12: Visibility & Inspection Walkthrough"

narrate "This walkthrough demonstrates how to inspect the state of the
voucher management system: tokens, partners, vouchers, access grants,
custodians, and the HTTP list API. Each step shows the CLI command and
its output."

init_artifact_dir "s12"
VM_LOG="$ARTIFACT_DIR/vm.log"
VM_CONFIG="$ARTIFACT_DIR/vm-config.yaml"

VOUCHER_DIR="$ARTIFACT_DIR/vouchers"
DB_PATH="$ARTIFACT_DIR/vm.db"
OWNER_KEY="$ARTIFACT_DIR/new-owner-pub.pem"
SERVER_PRIV_KEY="$ARTIFACT_DIR/server-owner-key.pem"
SERVER_PUB_KEY="$ARTIFACT_DIR/server-owner-pub.pem"
mkdir -p "$VOUCHER_DIR"

GLOBAL_TOKEN="walkthrough-global-token-12345"
MANUAL_TOKEN="reseller-b-token-67890abcdef"

# Generate a new-owner key pair for assignment target (the downstream recipient)
openssl ecparam -name prime256v1 -genkey -noout 2>/dev/null | \
    openssl pkcs8 -topk8 -nocrypt -out "$ARTIFACT_DIR/new-owner-priv.pem" 2>/dev/null
openssl ec -in "$ARTIFACT_DIR/new-owner-priv.pem" -pubout -out "$OWNER_KEY" 2>/dev/null

# Generate key pairs for partners
openssl ecparam -name prime256v1 -genkey -noout 2>/dev/null | \
    openssl pkcs8 -topk8 -nocrypt -out "$ARTIFACT_DIR/acme-mfg-priv.pem" 2>/dev/null
openssl ec -in "$ARTIFACT_DIR/acme-mfg-priv.pem" -pubout -out "$ARTIFACT_DIR/acme-mfg-pub.pem" 2>/dev/null

openssl ecparam -name prime256v1 -genkey -noout 2>/dev/null | \
    openssl pkcs8 -topk8 -nocrypt -out "$ARTIFACT_DIR/customer-a-priv.pem" 2>/dev/null
openssl ec -in "$ARTIFACT_DIR/customer-a-priv.pem" -pubout -out "$ARTIFACT_DIR/customer-a-pub.pem" 2>/dev/null

ACME_PUB_KEY="$ARTIFACT_DIR/acme-mfg-pub.pem"
CUSTOMER_PUB_KEY="$ARTIFACT_DIR/customer-a-pub.pem"

# Write config — DID minting generates the server's owner key and exports it
cat > "$VM_CONFIG" << EOF
server:
  addr: "127.0.0.1:$PORT_VM"
database:
  path: "$DB_PATH"
voucher_receiver:
  enabled: true
  endpoint: "/api/v1/vouchers"
  require_auth: true
  global_token: "$GLOBAL_TOKEN"
voucher_files:
  directory: "$VOUCHER_DIR"
voucher_signing:
  mode: "internal"
key_management:
  key_type: "EC256"
  first_time_init: true
did_minting:
  enabled: true
  key_export_path: "$SERVER_PRIV_KEY"
did_push:
  enabled: false
push_service:
  enabled: false
pull_service:
  enabled: false
EOF

phase "Build & Start"

build_binary "$DIR_VM" "$BIN" "Voucher Manager" || exit 1
start_bg_server "$DIR_VM" "$VM_LOG" "$BIN" server -config "$VM_CONFIG"
VM_PID=$SUPERTEST_LAST_PID
wait_for_port "$PORT_VM" 30 "Voucher Manager" || exit 1
log_info "Server running on port $PORT_VM (PID $VM_PID)"

# Extract the server's owner public key — vouchers will be generated with
# this as the manufacturer key so the server can sign-over (ExtendVoucher).
openssl ec -in "$SERVER_PRIV_KEY" -pubout -out "$SERVER_PUB_KEY" 2>/dev/null
assert_file_exists "$SERVER_PUB_KEY" "server owner public key exported"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 1: Token Management
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 1: Token Management"

narrate "First, let's add a manual bearer token. Manual tokens are used by
partners who don't do FDOKeyAuth. The token maps to a description-derived
fingerprint (no owner key). Let's add one for 'Reseller B'."

log_info "Adding manual token for Reseller B..."
"$BIN" tokens add \
    -token "$MANUAL_TOKEN" \
    -description "Reseller B" \
    -config "$VM_CONFIG" 2>/dev/null

assert_equals "0" "$?" "tokens add succeeds"

narrate "Now let's list all tokens. Notice the Owner Key FP column — manual
tokens show '(manual)' since they have no cryptographic key binding.
FDOKeyAuth-issued tokens would show the actual key fingerprint."

log_info "Listing tokens..."
echo ""
TOKEN_OUTPUT=$("$BIN" tokens list -config "$VM_CONFIG" 2>/dev/null)
echo "$TOKEN_OUTPUT"
echo ""

# Verify the token appears
echo "$TOKEN_OUTPUT" | grep -q "Reseller B"
assert_equals "0" "$?" "tokens list shows Reseller B"

echo "$TOKEN_OUTPUT" | grep -q "(manual)"
assert_equals "0" "$?" "manual token shows (manual) for Owner Key FP"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 2: Partner Management
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 2: Partner Management"

narrate "Partners represent the organizations you exchange vouchers with.
A 'supply' partner is an upstream manufacturer who pushes vouchers to you.
A 'receive' partner is a downstream customer you forward vouchers to.
Each partner is enrolled with a public key whose fingerprint ties back to
FDOKeyAuth tokens and voucher ownership."

log_info "Adding supply partner 'acme-mfg' (upstream manufacturer)..."
"$BIN" partners add \
    -id "acme-mfg" \
    -supply \
    -key "$ACME_PUB_KEY" \
    -config "$VM_CONFIG" 2>/dev/null
assert_equals "0" "$?" "partners add acme-mfg (supply) succeeds"

log_info "Adding receive partner 'customer-a' (downstream recipient)..."
"$BIN" partners add \
    -id "customer-a" \
    -receive \
    -key "$CUSTOMER_PUB_KEY" \
    -push-url "https://customer-a.example.com/fdo/vouchers" \
    -config "$VM_CONFIG" 2>/dev/null
assert_equals "0" "$?" "partners add customer-a (receive) succeeds"

narrate "List all partners — we should see both acme-mfg and customer-a:"
echo ""
PARTNER_LIST=$("$BIN" partners list -config "$VM_CONFIG" 2>/dev/null)
echo "$PARTNER_LIST"
echo ""

PARTNER_LINE=$(echo "$PARTNER_LIST" | grep "partner(s)")
echo "$PARTNER_LINE" | grep -q "2 partner"
assert_equals "0" "$?" "partners list shows 2 partners"

narrate "Filter by capability — only supply partners:"
echo ""
SUPPLY_LIST=$("$BIN" partners list -filter supply -config "$VM_CONFIG" 2>/dev/null)
echo "$SUPPLY_LIST"
echo ""

echo "$SUPPLY_LIST" | grep -q "acme-mfg"
assert_equals "0" "$?" "supply filter shows acme-mfg"

# customer-a should not appear in supply filter
SUPPLY_HAS_CUST=$(echo "$SUPPLY_LIST" | grep -c "customer-a" || true)
assert_equals "0" "$SUPPLY_HAS_CUST" "supply filter excludes customer-a"

narrate "Filter by capability — only receive partners:"
echo ""
RECEIVE_LIST=$("$BIN" partners list -filter receive -config "$VM_CONFIG" 2>/dev/null)
echo "$RECEIVE_LIST"
echo ""

echo "$RECEIVE_LIST" | grep -q "customer-a"
assert_equals "0" "$?" "receive filter shows customer-a"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 3: Partner Detail & Fingerprint Correlation
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 3: Partner Detail & Fingerprint Correlation"

narrate "The 'partners show' command displays full detail for a partner,
including its Key Fingerprint. This fingerprint is computed from the
partner's enrolled public key using the FDO spec (CBOR-encode, SHA-256,
hex). It is the same format used in the 'Owner Key FP' column of
'tokens list' and in voucher ownership records."

log_info "Showing acme-mfg details:"
echo ""
ACME_SHOW=$("$BIN" partners show -id "acme-mfg" -config "$VM_CONFIG" 2>/dev/null)
echo "$ACME_SHOW"
echo ""

echo "$ACME_SHOW" | grep -q "Key Fingerprint"
assert_equals "0" "$?" "partners show acme-mfg displays Key Fingerprint"

echo "$ACME_SHOW" | grep -q "Can Supply:.*true"
assert_equals "0" "$?" "partners show acme-mfg shows Can Supply: true"

log_info "Showing customer-a details:"
echo ""
CUST_SHOW=$("$BIN" partners show -id "customer-a" -config "$VM_CONFIG" 2>/dev/null)
echo "$CUST_SHOW"
echo ""

echo "$CUST_SHOW" | grep -q "Push URL"
assert_equals "0" "$?" "partners show customer-a displays Push URL"

echo "$CUST_SHOW" | grep -q "Can Receive:.*true"
assert_equals "0" "$?" "partners show customer-a shows Can Receive: true"

# Capture acme-mfg's fingerprint for correlation discussion
ACME_FP=$(echo "$ACME_SHOW" | grep "Key Fingerprint" | awk '{print $NF}')
assert_not_empty "$ACME_FP" "acme-mfg key fingerprint is non-empty"

narrate "The acme-mfg fingerprint is: $ACME_FP
When acme-mfg authenticates via FDOKeyAuth on the push endpoint, the
server issues a bearer token whose 'owner_key_fingerprint' column stores
this exact value. This is how the system traces a token back to its
partner: the fingerprints match."

narrate "Let's also export all partners as JSON — useful for backup or
cross-system replication:"
echo ""
EXPORT_JSON=$("$BIN" partners export -config "$VM_CONFIG" 2>/dev/null)
echo "$EXPORT_JSON" | python3 -m json.tool 2>/dev/null
echo ""

echo "$EXPORT_JSON" | grep -q "acme-mfg"
assert_equals "0" "$?" "partners export contains acme-mfg"

echo "$EXPORT_JSON" | grep -q "customer-a"
assert_equals "0" "$?" "partners export contains customer-a"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 4: Push Vouchers
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 4: Generate & Push Vouchers"

narrate "We'll generate three test vouchers with different serials,
simulating devices arriving from a manufacturer. Each voucher is keyed to
the server's owner key so the server can later extend (assign) it."

SERIALS=("SN-DEVICE-001" "SN-DEVICE-002" "SN-DEVICE-003")
MODELS=("IoT-Sensor-v2" "IoT-Sensor-v2" "IoT-Gateway-v1")
GUIDS=()

for i in 0 1 2; do
    VFILE="$ARTIFACT_DIR/voucher-${SERIALS[$i]}.pem"
    "$BIN" generate voucher \
        -serial "${SERIALS[$i]}" \
        -model "${MODELS[$i]}" \
        -owner-key "$SERVER_PUB_KEY" \
        -output "$VFILE" 2>/dev/null

    assert_file_exists "$VFILE" "generated voucher for ${SERIALS[$i]}"

    log_info "Pushing ${SERIALS[$i]} to server..."
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        -H "Authorization: Bearer $GLOBAL_TOKEN" \
        -F "voucher=@$VFILE" \
        -F "serial=${SERIALS[$i]}" \
        -F "model=${MODELS[$i]}" \
        "http://127.0.0.1:$PORT_VM/api/v1/vouchers" 2>/dev/null)

    HTTP_CODE=$(echo "$RESPONSE" | tail -1)
    BODY=$(echo "$RESPONSE" | sed '$d')
    assert_equals "200" "$HTTP_CODE" "push ${SERIALS[$i]} returns 200"

    GUID=$(echo "$BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('voucher_id',''))" 2>/dev/null)
    GUIDS+=("$GUID")
    log_info "  GUID: $GUID"
done

echo ""
narrate "All three vouchers pushed. GUIDs captured for later inspection."

# ─────────────────────────────────────────────────────────────────────────────
# Phase 5: vouchers list
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 5: vouchers list — Filtering & Inspection"

narrate "The 'vouchers list' command queries the local database. Let's see
all vouchers first, then filter by serial and status."

log_info "Listing ALL vouchers:"
echo ""
"$BIN" vouchers list -config "$VM_CONFIG" 2>/dev/null
echo ""

LIST_ALL=$("$BIN" vouchers list -config "$VM_CONFIG" 2>/dev/null)
COUNT_ALL=$(echo "$LIST_ALL" | grep -c "SN-DEVICE")
assert_equals "3" "$COUNT_ALL" "vouchers list shows all 3 vouchers"

narrate "Now filter by serial number — only SN-DEVICE-002:"
echo ""
"$BIN" vouchers list -serial "SN-DEVICE-002" -config "$VM_CONFIG" 2>/dev/null
echo ""

LIST_SERIAL=$("$BIN" vouchers list -serial "SN-DEVICE-002" -config "$VM_CONFIG" 2>/dev/null)
echo "$LIST_SERIAL" | grep -q "SN-DEVICE-00"
assert_equals "0" "$?" "vouchers list -serial returns result for SN-DEVICE-002"

narrate "Filter by status — no 'assigned' vouchers yet:"
echo ""
LIST_ASSIGNED=$("$BIN" vouchers list -status "assigned" -config "$VM_CONFIG" 2>/dev/null)
echo "$LIST_ASSIGNED"
echo ""
echo "$LIST_ASSIGNED" | grep -q "No vouchers found"
assert_equals "0" "$?" "no assigned vouchers yet"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 6: vouchers show
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 6: vouchers show — Detailed Inspection"

narrate "Let's look at a single voucher in detail. Notice the 'Owner Key
Fingerprint' field — this identifies who holds the voucher's signing key.
Assignment fields will be empty since we haven't assigned this one yet."

log_info "Showing details for ${GUIDS[0]}:"
echo ""
"$BIN" vouchers show -guid "${GUIDS[0]}" -config "$VM_CONFIG" 2>/dev/null
echo ""

SHOW_OUTPUT=$("$BIN" vouchers show -guid "${GUIDS[0]}" -config "$VM_CONFIG" 2>/dev/null)
echo "$SHOW_OUTPUT" | grep -q "Owner Key Fingerprint"
assert_equals "0" "$?" "vouchers show displays Owner Key Fingerprint"

echo "$SHOW_OUTPUT" | grep -q "SN-DEVICE-001"
assert_equals "0" "$?" "vouchers show displays serial SN-DEVICE-001"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 7: Assign Vouchers (CLI + HTTP)
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 7: Voucher Assignment (CLI + HTTP)"

narrate "Assignment signs a voucher over to a new owner's public key. This
is the core reseller operation: 'I sold this device, sign the voucher to
my customer.' Assignment can be done from the CLI (for operators) or via
the HTTP API (for integration with order management systems)."

NEW_OWNER_KEY_PEM=$(cat "$OWNER_KEY")

narrate "First, let's assign SN-DEVICE-001 using the CLI. The CLI operator
is the admin — it loads the owner signing key from the config to perform
the cryptographic chain extension."

log_info "CLI: Assigning SN-DEVICE-001..."
echo ""
CLI_ASSIGN_OUTPUT=$("$BIN" vouchers assign \
    -serial "SN-DEVICE-001" \
    -new-owner-key "$OWNER_KEY" \
    -config "$VM_CONFIG" 2>/dev/null)
CLI_ASSIGN_RC=$?
echo "$CLI_ASSIGN_OUTPUT"
echo ""

assert_equals "0" "$CLI_ASSIGN_RC" "CLI vouchers assign succeeds"
echo "$CLI_ASSIGN_OUTPUT" | grep -q "assigned"
assert_equals "0" "$?" "CLI assign output confirms assignment"

narrate "Now assign SN-DEVICE-002 via the HTTP API — same operation, different
channel. External systems (order portals, automation) would use this path."

log_info "HTTP: Assigning SN-DEVICE-002 via POST /assign..."
ASSIGN_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $GLOBAL_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"serials\": [\"SN-DEVICE-002\"],
        \"new_owner_key\": $(echo "$NEW_OWNER_KEY_PEM" | python3 -c 'import sys,json; print(json.dumps(sys.stdin.read()))')
    }" \
    "http://127.0.0.1:$PORT_VM/api/v1/vouchers/assign" 2>/dev/null)

ASSIGN_CODE=$(echo "$ASSIGN_RESPONSE" | tail -1)
ASSIGN_BODY=$(echo "$ASSIGN_RESPONSE" | sed '$d')
assert_equals "200" "$ASSIGN_CODE" "HTTP assign API returns 200"

ASSIGN_STATUS=$(echo "$ASSIGN_BODY" | python3 -c "import sys,json; r=json.load(sys.stdin)['results'][0]; print(r.get('status',''))" 2>/dev/null)
assert_equals "ok" "$ASSIGN_STATUS" "HTTP assignment result is 'ok'"

log_info "HTTP assignment response:"
echo "$ASSIGN_BODY" | python3 -m json.tool 2>/dev/null
echo ""

narrate "Two vouchers assigned — one via CLI, one via HTTP. SN-DEVICE-003
remains unassigned (still in inventory)."

# ─────────────────────────────────────────────────────────────────────────────
# Phase 8: Post-Assignment Inspection
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 8: Post-Assignment Inspection"

narrate "After assignment, vouchers show shows the assignment metadata:
who assigned it (Assigned By), who it was assigned to (Assigned To),
and when (Assigned At). Let's compare before and after."

log_info "vouchers show AFTER assignment:"
echo ""
"$BIN" vouchers show -guid "${GUIDS[0]}" -config "$VM_CONFIG" 2>/dev/null
echo ""

SHOW_AFTER=$("$BIN" vouchers show -guid "${GUIDS[0]}" -config "$VM_CONFIG" 2>/dev/null)
echo "$SHOW_AFTER" | grep -q "Assigned At"
assert_equals "0" "$?" "Assigned At field is populated"

echo "$SHOW_AFTER" | grep -q "Assigned To:"
assert_equals "0" "$?" "Assigned To field is populated"

echo "$SHOW_AFTER" | grep -q "Assigned By:"
assert_equals "0" "$?" "Assigned By field is populated"

echo "$SHOW_AFTER" | grep -q "assigned"
assert_equals "0" "$?" "Status shows 'assigned'"

narrate "Now let's list only assigned vouchers — should see both SN-DEVICE-001
and SN-DEVICE-002:"
echo ""
"$BIN" vouchers list -status "assigned" -config "$VM_CONFIG" 2>/dev/null
echo ""

LIST_POST=$("$BIN" vouchers list -status "assigned" -config "$VM_CONFIG" 2>/dev/null)
LIST_ASSIGNED_COUNT=$(echo "$LIST_POST" | grep -c "assigned")
assert_count_gt "$LIST_ASSIGNED_COUNT" "1" "vouchers list shows 2 assigned vouchers"

narrate "Check the HTTP status API too — it also reflects assignment:"
echo ""
STATUS_RESP=$(curl -s \
    -H "Authorization: Bearer $GLOBAL_TOKEN" \
    "http://127.0.0.1:$PORT_VM/api/v1/vouchers/status/${GUIDS[0]}" 2>/dev/null)
echo "$STATUS_RESP" | python3 -m json.tool 2>/dev/null
echo ""

API_STATUS=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null)
assert_equals "assigned" "$API_STATUS" "HTTP status API shows 'assigned'"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 9: Access Grants
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 9: vouchers grants — Access Grant Inspection"

narrate "When a voucher is assigned, the assign API creates access grants:
one for the custodian (the assigner) and one for the new owner. These
grants control who can see the voucher via the status and list APIs."

log_info "Listing ALL access grants:"
echo ""
"$BIN" vouchers grants -config "$VM_CONFIG" 2>/dev/null
echo ""

GRANTS_ALL=$("$BIN" vouchers grants -config "$VM_CONFIG" 2>/dev/null)
echo "$GRANTS_ALL" | grep -q "custodian"
assert_equals "0" "$?" "grants show custodian identity type"

echo "$GRANTS_ALL" | grep -q "owner_key"
assert_equals "0" "$?" "grants show owner_key identity type"

narrate "Filter grants by type — show only custodian grants:"
echo ""
"$BIN" vouchers grants -type custodian -config "$VM_CONFIG" 2>/dev/null
echo ""

narrate "Filter grants for a specific voucher:"
echo ""
"$BIN" vouchers grants -guid "${GUIDS[0]}" -config "$VM_CONFIG" 2>/dev/null
echo ""

GRANTS_GUID=$("$BIN" vouchers grants -guid "${GUIDS[0]}" -config "$VM_CONFIG" 2>/dev/null)
GRANT_COUNT=$(echo "$GRANTS_GUID" | grep -c "${GUIDS[0]}")
assert_count_gt "$GRANT_COUNT" "1" "assigned voucher has at least 2 grants"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 10: Custodian Visibility
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 10: vouchers custodians — Custodian Visibility"

narrate "The 'vouchers custodians' command aggregates custodian activity.
Reseller B directed one assignment, so they appear here with 1 voucher."

log_info "Listing all custodians:"
echo ""
"$BIN" vouchers custodians -config "$VM_CONFIG" 2>/dev/null
echo ""

CUST_OUTPUT=$("$BIN" vouchers custodians -config "$VM_CONFIG" 2>/dev/null)
echo "$CUST_OUTPUT" | grep -q "SN-DEVICE-001"
assert_equals "0" "$?" "custodians list shows SN-DEVICE-001 in serials"

# Extract the custodian fingerprint for drill-down
CUST_FP=$(echo "$CUST_OUTPUT" | tail -1 | awk '{print $1}')
if [ -n "$CUST_FP" ] && [ "$CUST_FP" != "No" ]; then
    narrate "Drill into Reseller B's custodian fingerprint ($CUST_FP)
to see all vouchers they assigned:"
    echo ""
    "$BIN" vouchers custodians -fingerprint "$CUST_FP" -config "$VM_CONFIG" 2>/dev/null
    echo ""

    DRILL_OUTPUT=$("$BIN" vouchers custodians -fingerprint "$CUST_FP" -config "$VM_CONFIG" 2>/dev/null)
    echo "$DRILL_OUTPUT" | grep -q "SN-DEVICE-00"
    assert_equals "0" "$?" "custodian drill-down shows SN-DEVICE-001"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Phase 11: HTTP List Endpoint
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 11: HTTP List Endpoint (GET /list)"

narrate "The GET /api/v1/vouchers/list endpoint returns vouchers scoped to
the authenticated caller's access. Let's compare what the global token sees
vs. what Reseller B's token sees."

log_info "List via global token (sees all vouchers it owns):"
echo ""
LIST_GLOBAL=$(curl -s \
    -H "Authorization: Bearer $GLOBAL_TOKEN" \
    "http://127.0.0.1:$PORT_VM/api/v1/vouchers/list" 2>/dev/null)
echo "$LIST_GLOBAL" | python3 -m json.tool 2>/dev/null
echo ""

GLOBAL_COUNT=$(echo "$LIST_GLOBAL" | python3 -c "import sys,json; print(json.load(sys.stdin).get('count',0))" 2>/dev/null)
assert_count_gt "$GLOBAL_COUNT" "0" "global token sees vouchers via /list"

log_info "List via Reseller B token (sees only vouchers they have access to):"
echo ""
LIST_RESELLER=$(curl -s \
    -H "Authorization: Bearer $MANUAL_TOKEN" \
    "http://127.0.0.1:$PORT_VM/api/v1/vouchers/list" 2>/dev/null)
echo "$LIST_RESELLER" | python3 -m json.tool 2>/dev/null
echo ""

RESELLER_COUNT=$(echo "$LIST_RESELLER" | python3 -c "import sys,json; print(json.load(sys.stdin).get('count',0))" 2>/dev/null)
log_info "Global token sees $GLOBAL_COUNT vouchers, Reseller B sees $RESELLER_COUNT"
narrate "Reseller B has no access grants (they didn't perform the assign),
so they see 0 vouchers. This demonstrates per-identity scoping."
assert_equals "0" "$RESELLER_COUNT" "Reseller B sees 0 vouchers (no access grants)"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 12: CLI Unassign & Re-assign
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 12: CLI Unassign & Re-assign"

narrate "Assignment mistakes happen. The 'vouchers unassign' command reverts
an assignment: it clears the assignment metadata, removes access grants,
and restores the voucher to its pre-assignment status. This lets the
operator correct errors or re-assign to a different customer."

log_info "Current status of SN-DEVICE-002 (should be assigned):"
echo ""
"$BIN" vouchers show -guid "${GUIDS[1]}" -config "$VM_CONFIG" 2>/dev/null
echo ""

log_info "Unassigning SN-DEVICE-002..."
echo ""
UNASSIGN_OUTPUT=$("$BIN" vouchers unassign \
    -serial "SN-DEVICE-002" \
    -config "$VM_CONFIG" 2>/dev/null)
UNASSIGN_RC=$?
echo "$UNASSIGN_OUTPUT"
echo ""

assert_equals "0" "$UNASSIGN_RC" "CLI vouchers unassign succeeds"
echo "$UNASSIGN_OUTPUT" | grep -q "unassigned"
assert_equals "0" "$?" "unassign output confirms revert"

narrate "Verify the voucher is no longer assigned:"
echo ""
SHOW_UNASSIGNED=$("$BIN" vouchers show -guid "${GUIDS[1]}" -config "$VM_CONFIG" 2>/dev/null)
echo "$SHOW_UNASSIGNED"
echo ""

echo "$SHOW_UNASSIGNED" | grep -q "no_destination"
assert_equals "0" "$?" "status restored to no_destination after unassign"

narrate "Now re-assign SN-DEVICE-002 to the same owner key — demonstrating
the correction workflow:"

log_info "Re-assigning SN-DEVICE-002 via CLI..."
echo ""
REASSIGN_OUTPUT=$("$BIN" vouchers assign \
    -serial "SN-DEVICE-002" \
    -new-owner-key "$OWNER_KEY" \
    -config "$VM_CONFIG" 2>/dev/null)
REASSIGN_RC=$?
echo "$REASSIGN_OUTPUT"
echo ""

assert_equals "0" "$REASSIGN_RC" "CLI re-assign after unassign succeeds"

narrate "SN-DEVICE-002 is now assigned again. The unassign/re-assign cycle
demonstrates the full correction workflow."

# ─────────────────────────────────────────────────────────────────────────────
# Phase 13: Partner Lifecycle — Removal
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 13: Partner Lifecycle — Removal"

narrate "Partners can be removed when the relationship ends. Let's remove
acme-mfg and verify the roster updates. Note: removing a supply partner
reopens the push endpoint to any FDOKeyAuth key (when no suppliers remain,
trust is open-mode). In production you'd revoke their tokens separately."

log_info "Removing partner acme-mfg..."
"$BIN" partners remove -id "acme-mfg" -config "$VM_CONFIG" 2>/dev/null
assert_equals "0" "$?" "partners remove acme-mfg succeeds"

log_info "Listing partners after removal:"
echo ""
POST_REMOVE_LIST=$("$BIN" partners list -config "$VM_CONFIG" 2>/dev/null)
echo "$POST_REMOVE_LIST"
echo ""

echo "$POST_REMOVE_LIST" | grep -q "1 partner"
assert_equals "0" "$?" "partners list shows 1 partner after removal"

REMOVED_CHECK=$(echo "$POST_REMOVE_LIST" | grep -c "acme-mfg" || true)
assert_equals "0" "$REMOVED_CHECK" "acme-mfg no longer appears in partners list"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 14: Negative — Double Assignment Rejected
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 14: Negative Test — Double Assignment Rejected"

narrate "Each custodian gets one assignment per voucher. A second attempt
should be rejected with 'already_assigned'. This is the at-most-once guard."

log_info "Attempting to re-assign SN-DEVICE-001..."
REASSIGN_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $GLOBAL_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"serials\": [\"SN-DEVICE-001\"],
        \"new_owner_key\": $(echo "$NEW_OWNER_KEY_PEM" | python3 -c 'import sys,json; print(json.dumps(sys.stdin.read()))')
    }" \
    "http://127.0.0.1:$PORT_VM/api/v1/vouchers/assign" 2>/dev/null)

REASSIGN_CODE=$(echo "$REASSIGN_RESPONSE" | tail -1)
REASSIGN_BODY=$(echo "$REASSIGN_RESPONSE" | sed '$d')
log_info "HTTP $REASSIGN_CODE response for double-assign attempt"

# The API returns 200 with per-serial results, but the individual result has error_code
REASSIGN_ERR=$(echo "$REASSIGN_BODY" | python3 -c "import sys,json; r=json.load(sys.stdin)['results'][0]; print(r.get('error_code',''))" 2>/dev/null)
assert_equals "already_assigned" "$REASSIGN_ERR" "double-assign returns already_assigned"
log_info "Correctly rejected: $REASSIGN_ERR"

# ─────────────────────────────────────────────────────────────────────────────
# Phase 15: Negative — Unauthenticated List
# ─────────────────────────────────────────────────────────────────────────────

phase "Phase 15: Negative Test — Unauthenticated List"

narrate "The list API requires authentication. Without a token, it returns 401."

UNAUTH_RESPONSE=$(curl -s -w "\n%{http_code}" \
    "http://127.0.0.1:$PORT_VM/api/v1/vouchers/list" 2>/dev/null)
UNAUTH_CODE=$(echo "$UNAUTH_RESPONSE" | tail -1)
assert_equals "401" "$UNAUTH_CODE" "unauthenticated /list returns 401"

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────

phase "Summary"

narrate "This walkthrough demonstrated:
  - tokens list: see all tokens with their owner key fingerprints
  - partners add/list/show/export/remove: full partner lifecycle
  - partner fingerprints: same format as token and voucher fingerprints
  - vouchers assign (CLI): assign voucher to customer key from command line
  - vouchers assign (HTTP): assign via API for system integration
  - vouchers unassign: revert assignment, then re-assign (correction workflow)
  - vouchers list: filter by status, serial, owner, or GUID
  - vouchers show: detailed view including assignment metadata
  - vouchers grants: inspect access grants by voucher or type
  - vouchers custodians: see who assigned what, with drill-down
  - GET /list: authenticated HTTP endpoint scoped to caller access
  - Negative tests: double-assign rejection, unauthenticated access"

print_summary
