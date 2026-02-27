# FDO Full-Stack Integration Super-Test

End-to-end integration tests exercising all five FDO applications across the complete device lifecycle: manufacturing, voucher transfer, rendezvous, and onboarding.

## Quick Start

```bash
cd /var/bkgdata/go-fdo-voucher-managment/tests/supertest
bash run-all-supertests.sh
```

Or run a single scenario:

```bash
bash scenario-1-direct-onboard.sh
```

## The Five FDO Applications

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  Manufacturing   │    │  Voucher Manager │    │   Onboarding    │
│  Station (DI)    │───▶│  (Reseller/VM)   │───▶│   Service (OBS) │
│  Port: 9x01     │push│  Port: 9x02      │push│   Port: 9x04    │
└─────────────────┘    └──────────────────┘    └────────┬────────┘
        │                       ▲                       │
        │ DI                    │ pull                   │ TO0
        ▼                       │                       ▼
┌─────────────────┐                            ┌─────────────────┐
│  Device/Endpoint │◀──────── TO1 ────────────▶│  Rendezvous     │
│  (Client)        │           TO2             │  Server (RV)    │
│                  │◀─────────────────────────▶│  Port: 9x03     │
└─────────────────┘                            └─────────────────┘
```

## Scenarios

### Scenario 1: Direct Onboard (Baseline)

**Services:** Mfg, OBS, Device | **Transfer:** Push | **Protocols:** DI, TO2

The simplest flow. Device is manufactured, voucher pushed to OBS, device connects directly for TO2. No rendezvous. Establishes the baseline.

### Scenario 2: Full Rendezvous

**Services:** Mfg, OBS, RV, Device | **Transfer:** Push | **Protocols:** DI, TO0, TO1, TO2

Adds the Rendezvous Server. After OBS receives the voucher, it registers a TO0 blob at the RV telling the device where to go. Device does TO1 (discover OBS) then TO2 (onboard).

### Scenario 3: Reseller Push

**Services:** All five | **Transfer:** Push→Push | **Protocols:** DI, TO0, TO1, TO2

Full supply chain via PUSH. Manufacturer pushes voucher to reseller (VM), which signs it over to the final customer (OBS) and pushes it downstream. OBS runs TO0, device does TO1→TO2.

### Scenario 4: Reseller Pull

**Services:** All five | **Transfer:** Push + Pull | **Protocols:** DI, PullAuth, TO0, TO1, TO2

Same supply chain but OBS PULLS vouchers from VM using PullAuth (Type-5 owner-key authentication). Demonstrates environments where the downstream service initiates transfer. Includes negative test for owner-scoped isolation.

### Scenario 5: Delegate Certificates

**Services:** Mfg, OBS, RV, Device | **Transfer:** Push | **Protocols:** DI, delegate TO0, TO1, delegate TO2

OBS creates a delegate certificate with `voucher-claim` permission and uses it for both TO0 (RV registration) and TO2 (device onboarding). The owner's private key is never used directly.

### Scenario 6: DID + PullAuth (Owner-Key + Delegate)

**Services:** All five | **Transfer:** Push + Pull (two modes) | **Protocols:** DI, PullAuth (owner-key + delegate), TO0, TO1, TO2

The most comprehensive scenario. Tests three PullAuth modes against the same Holder:
- **A:** Owner-key pull (standard)
- **B:** Delegate-based pull (delegate cert + owner public key only)
- **C:** Negative isolation (unrelated key → 0 vouchers)

Then completes full onboarding via RV.

## Test Matrix

| Scenario | DI | Push | Pull | TO0 | TO1 | TO2 | Delegate | DID | PullAuth |
|----------|:--:|:----:|:----:|:---:|:---:|:---:|:--------:|:---:|----------|
| 1 Direct | ✓ | ✓ | | | | ✓ | | | — |
| 2 Full RV | ✓ | ✓ | | ✓ | ✓ | ✓ | | | — |
| 3 Push | ✓ | ✓✓ | | ✓ | ✓ | ✓ | | ✓ | — |
| 4 Pull | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | | | Owner-key |
| 5 Delegate | ✓ | ✓ | | ✓* | ✓ | ✓* | ✓ | | — |
| 6 DID+Pull | ✓ | ✓ | ✓✓ | ✓ | ✓ | ✓ | ✓ | ✓ | Both |

## Port Allocation

Each scenario uses isolated ports to avoid conflicts:

| Scenario | Mfg | VM | RV | OBS |
|----------|-----|----|----|-----|
| 1 | 9101 | — | — | 9102 |
| 2 | 9201 | — | 9203 | 9202 |
| 3 | 9301 | 9302 | 9303 | 9304 |
| 4 | 9401 | 9402 | 9403 | 9404 |
| 5 | 9501 | — | 9503 | 9502 |
| 6 | 9601 | 9602 | 9603 | 9604 |

## Artifact Directories

Each scenario stores its artifacts in `/tmp/fdo_supertest_s<N>/`:

- `*.db` — SQLite databases
- `*_vouchers/` — Voucher files (`.fdoov`)
- `*.log` — Server and client logs
- `*_config.yaml` / `*.cfg` — Generated configs
- `cred.bin` — Device credential blob
- `*_owner.pem` — Extracted public keys

Artifacts are cleaned on success, preserved on failure for debugging.

## Troubleshooting

### Stale processes

Each scenario kills any process on its ports before starting. If you see port conflicts, manually check:

```bash
lsof -i tcp:9101-9604
```

### Key mismatches

If TO2 fails with "invalid owner key", ensure:
1. The OBS owner key was correctly extracted and given to the Mfg station
2. If using VM as middleman, the VM's signover target matches the OBS key
3. The voucher's owner key fingerprint matches what the OBS has in its DB

### TO0 not completing

TO0 depends on:
1. RV server being reachable at the address in the voucher's RV entries
2. The OBS having the correct owner key (or delegate) to sign the TO0 blob
3. The RV server's auth mode allowing the upload (use "open" for testing)

Check the OBS log for `TO0 dispatcher` messages.

### Pull returns 0 vouchers

PullAuth is owner-key-scoped. The pulling key's fingerprint must match the `owner_key_fingerprint` stored with the voucher. Verify:
1. The Mfg station signed over to the correct key
2. The VM's signover pipeline ran (check VM log for "sign-over" or "pipeline")
3. The pull key matches what the voucher was signed over to

## FDO Protocol Overview

```
MANUFACTURING (DI)           TRANSFER              ONBOARDING
┌──────────────────┐   ┌────────────────────┐   ┌──────────────────┐
│ Device ──DI──▶   │   │ Push: Mfg→OBS      │   │ TO0: OBS──▶RV    │
│   Mfg Station    │   │ Push: Mfg→VM→OBS   │   │ TO1: Device──▶RV │
│                  │   │ Pull: OBS──▶VM      │   │ TO2: Device──▶OBS│
│ Creates:         │   │                     │   │                  │
│  • Voucher       │   │ Signover chain:     │   │ Proves:          │
│  • Device cred   │   │  Mfg → VM → OBS    │   │  • Ownership     │
│  • RV entries    │   │                     │   │  • Delivers FSIM │
└──────────────────┘   └────────────────────┘   └──────────────────┘
```

- **DI (Device Initialization):** Factory step. Device gets identity + credential.
- **TO0 (Transfer Ownership 0):** Owner registers "blob" at RV saying "send device X to me at address Y"
- **TO1 (Transfer Ownership 1):** Device asks RV "where should I go?" Gets owner's address.
- **TO2 (Transfer Ownership 2):** Device connects to owner. Owner proves identity. FSIM payloads delivered. Ownership transferred.
- **Push:** Upstream entity sends voucher to downstream via HTTP POST.
- **Pull (PullAuth):** Downstream entity authenticates to upstream and downloads vouchers.
- **Delegate:** Certificate signed by owner key, used in place of owner key for specific operations.
