#!/bin/bash
# SPDX-FileCopyrightText: (C) 2026 Dell Technologies
# SPDX-License-Identifier: Apache 2.0
#
# Master runner for FDO full-stack integration super-tests.
# Builds all five binaries, then runs each scenario in sequence.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-supertest.sh
source "$SCRIPT_DIR/lib-supertest.sh"

banner "FDO Full-Stack Integration Super-Test Suite"

narrate "This suite exercises all five FDO applications end-to-end:"
narrate "  Manufacturing Station · Device Client · Voucher Manager"
narrate "  Rendezvous Server · Onboarding Service"
narrate ""
narrate "Scenarios cover: DI, Push, Pull, TO0, TO1, TO2,"
narrate "  DID discovery, PullAuth (owner-key + delegate), and delegation."
echo ""

# ============================================================
# Pre-flight checks
# ============================================================
phase "Pre-flight checks"

for dir in "$DIR_MFG" "$DIR_ENDPOINT" "$DIR_VM" "$DIR_RV" "$DIR_OBS"; do
    if [ ! -d "$dir" ]; then
        log_error "Project directory not found: $dir"
        exit 1
    fi
done
log_success "All project directories found"

if ! command -v go &>/dev/null; then
    log_error "Go is not installed"
    exit 1
fi
log_success "Go is available: $(go version | head -1)"

if ! command -v curl &>/dev/null; then
    log_error "curl is not installed"
    exit 1
fi
log_success "curl is available"

# ============================================================
# Build all binaries
# ============================================================
build_all || exit 1

# ============================================================
# Run scenarios
# ============================================================
TOTAL_PASS=0
TOTAL_FAIL=0
SCENARIO_RESULTS=()

run_scenario() {
    local script="$1"
    local name="$2"

    banner "Scenario: $name"

    if bash "$SCRIPT_DIR/$script"; then
        SCENARIO_RESULTS+=("${GREEN}PASS${NC}  $name")
        ((TOTAL_PASS++))
    else
        SCENARIO_RESULTS+=("${RED}FAIL${NC}  $name")
        ((TOTAL_FAIL++))
    fi
}

run_scenario "scenario-1-direct-onboard.sh"    "1: Direct Onboard (Mfg → OBS → Device)"
run_scenario "scenario-2-full-rv.sh"           "2: Full Rendezvous (Mfg → OBS → RV → Device)"
run_scenario "scenario-3-reseller-push.sh"     "3: Reseller Push (Mfg → VM → OBS → RV → Device)"
run_scenario "scenario-4-reseller-pull.sh"     "4: Reseller Pull (Mfg → VM ← OBS → RV → Device)"
run_scenario "scenario-5-delegation.sh"        "5: Delegate Certs (TO0 + TO2 via delegate)"
run_scenario "scenario-6-did-pull-delegate.sh" "6: DID + PullAuth (owner-key + delegate + isolation)"

# Scenario 7 is an expected-failure canary test (Mfg has no PullAuth support yet).
# Uncomment once go-fdo-di adds PullAuth holder endpoints.
# run_scenario "scenario-7-mfg-pull.sh"        "7: Mfg Pull (Mfg ← VM via PullAuth)"

run_scenario "scenario-8-bmo-meta-url.sh"   "8: BMO Meta-URL (inline + unsigned + signed + negative)"
run_scenario "scenario-8-enhanced-bmo-meta-url.sh" "8E: BMO Meta-URL Enhanced (go-fdo-meta-tool integration)"

# ============================================================
# Final summary
# ============================================================
banner "Super-Test Suite Results"

for result in "${SCENARIO_RESULTS[@]}"; do
    echo -e "  $result"
done

echo ""
echo -e "  ${BOLD}Scenarios passed: $TOTAL_PASS${NC}"
if [ "$TOTAL_FAIL" -gt 0 ]; then
    echo -e "  ${RED}${BOLD}Scenarios failed: $TOTAL_FAIL${NC}"
    echo ""
    exit 1
else
    echo -e "  ${GREEN}${BOLD}Scenarios failed: 0${NC}"
    echo ""
    echo -e "  ${GREEN}${BOLD}All scenarios passed!${NC}"
    exit 0
fi
