# FDO Voucher Supply Chain — Quick Start Guide

A hands-on primer for working with FDO voucher supply chains. Skim what you need, skip what you don't — this isn't a course, just a map of the territory.

## What's Covered

- **Voucher basics** — what they are and how they flow
- **Supply chain scenarios** — push, pull, DID-based discovery
- **Hands-on examples** — run real tests, poke at real configs
- **Troubleshooting** — common gotchas and debug tips

## Getting Started

### Prerequisites

- Comfortable with a terminal and HTTP basics
- Go 1.19+ installed
- `curl`, `jq`, `sqlite3` available
- Crypto knowledge helps but isn't required

### Setup

```bash
git clone <repository-url>
cd go-fdo-voucher-management
go build -o fdo-voucher-manager

cd tests
chmod +x *.sh
./run-all-tests.sh  # Sanity check
```

## 1. The Basics

Start here if you're new to FDO vouchers.

**Read**: [VOUCHER_SUPPLY_CHAIN.md](../VOUCHER_SUPPLY_CHAIN.md) for the big picture, then [tests/README.md](README.md) for test suite layout.

**Try it**:

```bash
# Run a basic reception test
./tests/test-1.1-receive-valid-voucher.sh

# See what got stored
ls -la tests/data/vouchers-a/
```

**Key takeaways**: What ownership vouchers are, who the supply chain participants are, how vouchers get stored.

---

## 2. Core Supply Chain Operations

How vouchers actually move between organizations.

**Read**: [TEST_PLAN.md](TEST_PLAN.md) — focus on Categories 1-5.

**Try it**:

```bash
# Run the end-to-end transmission test
./tests/test-5.1-end-to-end-transmission.sh

# Trace the flow:
# Factory → Instance A (manufacturer)
# Instance A signs over → Instance B (customer)
# Instance B receives voucher
```

**Key takeaways**: How sign-over works, push transmission mechanics, ownership validation.

---

## 3. DID-Based Discovery

The modern approach — services find each other automatically via DIDs.

**Read**: [TUTORIAL-E2E-DID-PUSH-PULL.md](TUTORIAL-E2E-DID-PUSH-PULL.md) walks through everything step by step. [diagrams/e2e-flow.md](diagrams/e2e-flow.md) has visual overviews.

**Try it**:

```bash
# Run the full DID-based push+pull test
./tests/test-e2e-did-push-pull.sh
```

**Topics covered**: DID document resolution, automatic endpoint config, PullAuth authentication, push vs pull models.

---

## 4. Experiments & Exercises

Once you're comfortable with the basics, try these variations. See [LEARNING-EXERCISES.md](LEARNING-EXERCISES.md) for detailed walkthroughs.

- **Reverse the flow** — customer pushes to manufacturer
- **Three-party chain** — Factory → Distributor → Customer
- **Static endpoints** — skip DID discovery, use hardcoded URLs
- **Break things** — simulate network failures, key mismatches
- **Load test** — push many vouchers, see what happens
- **Lock it down** — auth tokens, TLS, ownership validation

---

## Quick Reference

| Topic | Where to Look |
| ----- | ------------- |
| Supply chain concepts | [VOUCHER_SUPPLY_CHAIN.md](../VOUCHER_SUPPLY_CHAIN.md) |
| Test suite overview | [tests/README.md](README.md) |
| DID push+pull tutorial | [TUTORIAL-E2E-DID-PUSH-PULL.md](TUTORIAL-E2E-DID-PUSH-PULL.md) |
| Configuration options | [CONFIGURATION-GUIDE.md](CONFIGURATION-GUIDE.md) |
| Hands-on exercises | [LEARNING-EXERCISES.md](LEARNING-EXERCISES.md) |
| Flow diagrams | [diagrams/e2e-flow.md](diagrams/e2e-flow.md) |

## Troubleshooting

- **Common issues**: See the debugging section in [LEARNING-EXERCISES.md](LEARNING-EXERCISES.md)
- **Debug commands**: Diagnostic helpers are listed in the exercises
- **Logs**: Check `tests/data/*.log` when things go sideways

## Getting Help

- **Issues**: Report bugs via GitHub issues
- **Discussions**: Questions and ideas welcome
- **PRs**: Improvements always appreciated
