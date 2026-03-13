# Voucher Assignment: Design & Architecture

## The Problem in One Sentence

A **Custodian** — an entity in the supply chain that owns a device in business terms but does NOT hold an FDO Owner Key — needs to instruct its **Supplier** (the Key Holder) to extend the voucher's cryptographic ownership chain directly to the Custodian's **Customer**.

This is the "bypass" model: the Custodian never appears in the voucher chain. The Supplier signs the voucher over to someone the Custodian designates, on the Custodian's behalf.

## Definitions

| Term | Meaning | Has FDO Key? | In Voucher Chain? |
|------|---------|:------------:|:-----------------:|
| **Key Holder** | Entity whose private key corresponds to the public key at the tip of the voucher's ownership chain. Can cryptographically extend the chain. | Yes | Yes |
| **Custodian** | Entity that owns a device in business terms (purchased it, is responsible for it). Authenticated via API token. May or may not have an FDO Owner Key. | Maybe | Not necessarily |
| **Customer** | The downstream party the Custodian designates to receive cryptographic ownership. | Yes (must) | Yes (after extension) |

**Key insight:** In FDO, "Owner Key" and "device owner" are NOT the same thing. A reseller can own 10,000 devices and have zero FDO keys. They're still the owner. They still get to say who the vouchers should be signed over to.

## The Operation: Assignment

**Assignment** is the act of a Custodian directing the Key Holder to extend a voucher's ownership chain to a designated Customer's public key.

- The Custodian authenticates via API token (not FDO key)
- The Key Holder performs the cryptographic sign-over (`ExtendVoucher`)
- The Customer's public key appears as the new chain tip
- The Custodian never appears in the voucher chain

**Reassignment** is what we are *preventing*: attempting to direct a second, different extension of the same voucher after an assignment has already been executed. A Custodian gets one shot per voucher.

### Assignment is Not Limited to This API

The assignment API defined here is a **convenience**, not the exclusive mechanism. The fundamental prerequisites for assignment are:

1. The **Key Holder** (e.g., "A") can positively identify the **Custodian** (e.g., "B") — through *some* means — and understands "B" to be the current owner (lowercase "o") of the physical device.
2. The **Custodian** can convey the new assignee ("C")'s public key to the Key Holder.

These prerequisites can be satisfied through any channel: an existing web portal, a manual process, even email. The API we define here simply provides a programmatic, auditable way to do it. But the trust relationship between Key Holder and Custodian — the Key Holder's confidence that this request really comes from the entity that owns the device — is established **out-of-band** and is the Key Holder's responsibility to verify.

This means:

- The assignment API does not *create* trust between A and B. It *consumes* pre-existing trust.
- Different deployments may authenticate Custodians differently (API tokens, mTLS, SAML federation, manual approval queues) depending on their threat model.
- The at-most-once guard (preventing reassignment) applies regardless of channel. Whether B calls the API or sends an email, once A has extended the chain on B's behalf, A should not do it again for the same voucher without explicit intervention.

### Why "Assignment" and Not "Assignment"

The operation itself is an assignment — the Custodian is assigning who should receive ownership for the first time. "Assignment" implies changing an existing assignment, which is the failure case we reject. The API should name the happy path, not the error path.

The FDO spec uses "sign over" / "sign-over" for the cryptographic act and "voucher transfer" for the delivery. Neither term captures the business-level directive from a non-FDO-key-holding Custodian. "Assignment" fills this gap.

## Supply Chain Scenarios

### Scenario A: Reseller with FDO Key (Standard Transfer)

```
Manufacturer(K_A) --sign-over--> Reseller(K_B) --sign-over--> Customer(K_C)
Voucher chain: [K_A→K_B, K_B→K_C]
```

No assignment needed. Both parties hold keys and extend the chain themselves. This is the normal push/pull model.

### Scenario B: Reseller WITHOUT FDO Key (Assignment)

```
Manufacturer(K_A) holds voucher
Reseller(no key) is Custodian — purchased device, has API token
Reseller tells Manufacturer: "assign to Customer(K_C)"
Manufacturer extends: K_A → K_C
Voucher chain: [K_A→K_C]
```

The Reseller is the Custodian. The Manufacturer is the Key Holder. The Customer is the designee. The Reseller never appears in the chain.

### Scenario C: Multi-Hop Custodianship (Chained Assignment)

```
Manufacturer(K_A) holds voucher
Manufacturer sells device to Distributor (no key) — Distributor becomes Custodian
Distributor sells to Reseller (no key) — Reseller becomes Custodian
Reseller tells Manufacturer: "assign to Customer(K_D)"
Manufacturer extends: K_A → K_D
Voucher chain: [K_A→K_D]
```

Custodianship transferred through the business chain. Only the final Custodian directs the extension. The Key Holder (Manufacturer) is at the chain tip throughout.

### Scenario D: Assignment After Prior Transfer (Multi-Hop with Keys)

```
Manufacturer(K_A) --sign-over--> Reseller_1(K_B)   [chain: K_A→K_B]
Reseller_1 sells to Reseller_2 (no key) — Reseller_2 becomes Custodian
Reseller_2 tells Reseller_1: "assign to Customer(K_D)"
Reseller_1 extends: K_B → K_D                       [chain: K_A→K_B, K_B→K_D]
```

The voucher already has entries (K_A→K_B). That's fine. The at-most-once guard is NOT "has this voucher ever been extended" — it's "has this Custodian already directed an assignment for this voucher."

### Scenario E: Rejected Assignment

```
Same as B. After Manufacturer extends K_A → K_C:
Reseller says: "actually, assign to Customer_2(K_D)" → REJECTED
```

Rejected because the Reseller (Custodian) has already used their one assignment for this voucher. This is reassignment — the thing we prevent.

## Architecture: What Lives Where

### Library (`go-fdo`)

The library has **no concept of Custodian, assignment, or at-most-once guards.** It provides:

- **`ExtendVoucher(v, currentOwnerSigner, nextOwnerKey, extra)`** — Cryptographic chain extension. Works at any chain depth. Requires the caller to hold the private key matching the chain tip. This is the only primitive needed.

The existing `AssignVoucher()` should be **removed**. It was `ExtendVoucher` with a wrong precondition (`len(Entries) == 0`). The library shouldn't enforce business logic about when extension is allowed.

### Application Layer (Voucher Manager)

All assignment business logic lives here:

1. **Custodian tracking** — Who is the business owner of this voucher? Linked to auth tokens, not FDO keys.

2. **Assignment API** — `POST {root}/assign` — Custodian directs the Key Holder to extend the chain to a designated Customer.

3. **At-most-once guard** — Has this Custodian already directed an assignment for this voucher? Tracked in the DB. The `assigned_at`, `assigned_to_fingerprint` columns serve this purpose.

4. **Access grants** — After assignment, both the Custodian and the new Customer get read access (status queries, etc.).

### The Guard Logic

```
Can this assignment proceed?
  1. Is the caller authenticated?                    → 401 if not
  2. Is the caller the Custodian for this voucher?   → 403 if not
  3. Has this voucher already been assigned?          → error: "already_assigned"
  4. Does the Key Holder have the signing key?        → error: "internal_error" if not
  5. ExtendVoucher(voucher, signerKey, newOwnerKey)   → cryptographic extension
  6. Mark as assigned in DB                           → at-most-once recorded
```

Step 3 is the at-most-once guard. It checks the DB, not the voucher's entry count. Steps 1-3 are application concerns. Step 5 is the only library call.

## Custodian Identity

A Custodian is NOT necessarily a first-class stored entity (like Partners). It can be as lightweight as:

- A **fingerprint** derived from their auth token (already implemented as `CallerIdentity.Fingerprint`)
- A **label** from their token description (already implemented as `CallerIdentity.IdentityLabel`)
- A **record** in the voucher transmission table linking `custodian_fingerprint` to the voucher

The system doesn't need a `custodians` table with CRUD operations. The Custodian identity is established at authentication time (from the token) and recorded in the voucher's assignment record.

What matters is that the auth token → Custodian identity mapping is stable, so the at-most-once guard works. If token T1 maps to Custodian fingerprint F1, and F1 already assigned voucher V, then any request with token T1 (or any other token mapping to F1) for voucher V is rejected.

### Relationship to Existing Concepts

| Existing Concept | Relationship to Custodian |
|-----------------|--------------------------|
| **Partner** | A Partner with `can_receive_vouchers` *may* also be a Custodian for vouchers assigned to them. But Partners are about DID-based trust for voucher *supply*. Custodians are about business ownership for voucher *assignment*. Distinct concepts. |
| **CallerIdentity** | Already captures auth method, fingerprint, and label. The `CanAssign()` check (renamed from `CanAssign()`) uses this. No structural change needed — just rename. |
| **Access Grants** | After assignment, the Custodian and Customer both get grants. The grant's `IdentityType` changes from `"custodian"` to `"custodian"`. |
| **Owner Key Fingerprint** | The `owner_key_fingerprint` in the transmission record is the Key Holder's fingerprint (chain-tip key). The Custodian's fingerprint is separate — it comes from their auth token. |

## Naming Changes (from "assign" to "assign")

### API
| Current | New |
|---------|-----|
| `POST {root}/assign` | `POST {root}/assign` |
| `AssignRequest` | `AssignRequest` |
| `AssignResponse` | `AssignResponse` |
| `AssignResult` | `AssignResult` |
| Error code: `"already_assigned"` | `"already_assigned"` |

### DB Schema
| Current Column | New Column |
|---------------|------------|
| `assigned_at` | `assigned_at` |
| `assigned_to_fingerprint` | `assigned_to_fingerprint` |
| `assigned_to_did` | `assigned_to_did` |
| `assigned_by_fingerprint` | `assigned_by_fingerprint` |
| Status: `"assigned"` | Status: `"assigned"` |
| `original_owner_fingerprint` | (keep — still meaningful) |

### Code
| Current | New |
|---------|-----|
| `VoucherAssignHandler` | `VoucherAssignHandler` |
| `voucher_assign_handler.go` | `voucher_assign_handler.go` |
| `CanAssign()` | `CanAssign()` |
| `MarkAssigned()` | `MarkAssigned()` |
| `fdo.ExtendVoucher()` | **Remove** — use `fdo.ExtendVoucher()` directly |
| (removed) | **Remove** — library tests not needed (it's just ExtendVoucher) |
| `scenario-11-status-assign.sh` | `scenario-11-status-assign.sh` |
| Access grant type: `"custodian"` | `"custodian"` |

### Status Endpoint
| Current | New |
|---------|-----|
| Status: `"assigned"` | `"assigned"` |
| `assigned_at` | `assigned_at` |
| `assigned_to_fingerprint` | `assigned_to_fingerprint` |
| `assigned_to_did` | `assigned_to_did` |
| `assigned_by_fingerprint` | `assigned_by_fingerprint` |

## Implementation Plan

### Phase 1: Library Cleanup
- Remove `AssignVoucher()` from `go-fdo/voucher.go`
- Remove `go-fdo/voucher_assign_test.go`
- Handler calls `fdo.ExtendVoucher()` directly

### Phase 2: Rename assign → assign (Application Layer)
- Rename files, types, functions, DB columns, API endpoint, error codes, status values
- Update tests, integration script, TODO.md
- All mechanical — no logic changes

### Phase 3: Fix the Guard Logic
- Remove the library-level `len(Entries) > 0` check (gone with Phase 1)
- Ensure the DB-level at-most-once check (`status == assigned`) is the sole guard
- Verify the handler calls `ExtendVoucher` without any chain-depth precondition

### Phase 4: Custodian Tracking (Future)
- Add `custodian_fingerprint` column to transmission records (separate from `owner_key_fingerprint`)
- Track custodianship transfer as a first-class operation
- Link auth tokens to custodian identity more explicitly
- This is optional — the current CallerIdentity + access grants may be sufficient

## Open Questions

1. **Custodianship transfer API**: When device ownership transfers from Distributor to Reseller (Scenario C), how is that recorded? Is it an explicit API call, or does it happen implicitly when the Reseller's token is associated with the voucher?

2. **Multiple Custodians**: Can a voucher have multiple Custodians simultaneously (e.g., a distributor and a reseller both have tokens)? Or is custodianship exclusive?

3. **Custodian + Key Holder overlap**: In Scenario D, Reseller_1 is both the Key Holder (has K_B) and is acting on behalf of Reseller_2 (the Custodian). Does the system need to distinguish "I'm extending my own voucher" from "I'm extending on behalf of a Custodian"?

4. **Spec alignment**: The spec's status vocabulary is `pending`, `processing`, `completed`, `failed`, `unknown`. Should `assigned` map to one of these for spec compliance, or is it an extension? (Likely an extension — the spec doesn't contemplate this scenario.)
