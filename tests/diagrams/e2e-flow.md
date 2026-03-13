# E2E Test Flow Diagrams

This document contains visual diagrams explaining the end-to-end DID push and pull test flow.

## Network Topology

```text
                    Internet
                       │
        ┌─────────────┼─────────────┐
        │             │             │
   ┌─────────┐  ┌─────────┐  ┌─────────┐
   │ Factory │  │ First   │  │ Second  │
   │ (Test)  │  │ (Mfg)   │  │ (Cust)  │
   │         │  │ :8083   │  │ :8084   │
   └─────────┘  └─────────┘  └─────────┘
        │             │             │
        │             │             │
        └─────────────┼─────────────┘
                      │
               Test Runner
```

### Instance Details

```text
First Instance (Manufacturer)                    Second Instance (Customer)
┌─────────────────────────────┐                ┌─────────────────────────────┐
│ Port: 8083                  │                │ Port: 8084                  │
│ Owner Key: EC/P-384 #1      │                │ Owner Key: EC/P-384 #2      │
│ DID: did:web:localhost:8083 │                │ DID: did:web:localhost:8084 │
│                             │                │                             │
│ Services:                   │                │ Services:                   │
│ - Voucher Receiver          │                │ - Voucher Receiver          │
│ - DID Document Server       │                │ - DID Document Server       │
│ - DID Push (enabled)        │                │ - DID Push (disabled)       │
│ - Pull Service (enabled)    │                │ - Pull Service (disabled)    │
└─────────────────────────────┘                └─────────────────────────────┘
```

## DID Document Structure

Both instances serve DID documents at `/.well-known/did.json`:

```text
DID Document
├── id: "did:web:localhost:PORT"
├── verificationMethod[0]
│   ├── id: "#owner-key"
│   ├── type: "JsonWebKey"
│   └── publicKeyJwk
│       ├── kty: "EC"
│       ├── crv: "P-384"
│       ├── x: "public_key_x_coordinate"
│       └── y: "public_key_y_coordinate"
└── service[0]
    ├── id: "#voucher-recipient"
    ├── type: "FDOVoucherRecipient"
    └── serviceEndpoint: "http://localhost:PORT/api/v1/vouchers"
```

## Complete Data Flow

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                           COMPLETE E2E FLOW                                  │
└─────────────────────────────────────────────────────────────────────────────┘

Step 1: Customer Setup
┌─────────────┐    Serve DID Document    ┌─────────────┐
│   Second    │─────────────────────────▶│ Test Runner │
│  :8084      │   /.well-known/did.json  │             │
└─────────────┘                         └─────────────┘
       │                                        │
       │ Extract DID URI + Endpoint             │
       │                                        │
       ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ SECOND_DID_URI = "did:web:localhost:8084"                  │
│ SECOND_VOUCHER_URL = "http://localhost:8084/api/v1/vouchers" │
└─────────────────────────────────────────────────────────────┘

Step 2: Manufacturer Configuration
┌─────────────┐    Configure with      ┌─────────────┐
│   First     │───────────────────────▶│ Test Runner │
│  :8083      │   Customer's DID       │             │
└─────────────┘                         └─────────────┘
       │                                        │
       │ static_did = "did:web:localhost:8084"  │
       │                                        │
       ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ First configured to automatically sign over to Second's DID │
└─────────────────────────────────────────────────────────────┘

Step 3: Factory Device Manufacturing
┌─────────────┐    Generate Voucher     ┌─────────────┐
│   Factory   │─────────────────────────▶│ Test Runner │
│  (Test)     │                         │             │
└─────────────┘                         └─────────────┘
       │                                        │
       │ Voucher with serial "E2E-DID-SERIAL-001" │
       │                                        │
       ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ Test voucher created for hypothetical device                │
└─────────────────────────────────────────────────────────────┘

Step 4: Factory → Manufacturer Push
┌─────────────┐    POST voucher         ┌─────────────┐
│   Factory   │─────────────────────────▶│   First     │
│  (Test)     │   /api/v1/vouchers      │  :8083      │
└─────────────┘                         └─────────────┘
       │                                        │
       │ HTTP 200 - Voucher accepted            │
       │                                        │
       ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ First stores voucher and prepares for sign-over            │
└─────────────────────────────────────────────────────────────┘

Step 5: DID Resolution & Sign-Over
┌─────────────┐    Resolve DID           ┌─────────────┐
│   First     │─────────────────────────▶│   Second    │
│  :8083      │   did:web:localhost:8084 │  :8084      │
└─────────────┘                         └─────────────┘
       │                                        │
       │ DID Document with public key + endpoint │
       │                                        │
       ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ First extracts:                                             │
│ - Second's public key (for sign-over)                       │
│ - Second's voucher URL (for push)                          │
└─────────────────────────────────────────────────────────────┘

Step 6: Manufacturer → Customer Push
┌─────────────┐    POST signed voucher   ┌─────────────┐
│   First     │─────────────────────────▶│   Second    │
│  :8083      │   /api/v1/vouchers      │  :8084      │
└─────────────┘                         └─────────────┘
       │                                        │
       │ Voucher signed over to Second's key     │
       │                                        │
       ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ Second receives and stores voucher                           │
└─────────────────────────────────────────────────────────────┘

Step 7: FDOKeyAuth Authentication (Alternative)
┌─────────────┐    FDOKeyAuth handshake     ┌─────────────┐
│   Second    │─────────────────────────▶│   First     │
│  (Client)   │   authenticate           │  :8083      │
└─────────────┘                         └─────────────┘
       │                                        │
       │ 3-message CBOR protocol:              │
       │ Hello → Challenge → Prove → Result     │
       │                                        │
       ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ Second receives session token for voucher retrieval          │
└─────────────────────────────────────────────────────────────┘
```

## FDOKeyAuth Cryptographic Handshake

```text

FDOKeyAuth Protocol Flow (Type-5 Authentication)

┌─────────────┐      1. Hello      ┌─────────────┐
│   Second    │───────────────────▶│   First     │
│ (Requester) │                   │ (Verifier)  │
│             │  {owner_key, nonce}│             │
└─────────────┘                   └─────────────┘
       ▲                                    │
       │                                    │
       │      2. Challenge                   │
       │◀───────────────────────────────────│
       │  {holder_sig, nonce}               │
       │                                    │
       │      3. Prove                      │
       │───────────────────▶                │
       │  {recipient_sig}                    │
       │                                    │
       │      4. Result                     │
       │◀───────────────────────────────────│
       │  {session_token, status}           │
       │                                    │
       ▼                                    ▼
┌─────────────────────────────────────────────────────────────┐
│ Authentication successful - Second can now pull vouchers    │
└─────────────────────────────────────────────────────────────┘
```

## Voucher Sign-Over Cryptography

```text

Voucher Ownership Chain

Original Voucher (Factory → Manufacturer)
┌─────────────────────────────────────────────────────────────┐
│ Device Voucher                                              │
│ ├─ Device GUID                                              │
│ ├─ Serial Number                                            │
│ ├─ Model Number                                             │
│ └─ Factory Signature (proves device authenticity)           │
└─────────────────────────────────────────────────────────────┘
                                │
                                │ Sign-over with Manufacturer's key
                                ▼
Extended Voucher (Manufacturer → Customer)
┌─────────────────────────────────────────────────────────────┐
│ Device Voucher                                              │
│ ├─ Device GUID                                              │
│ ├─ Serial Number                                            │
│ ├─ Model Number                                             │
│ ├─ Factory Signature                                        │
│ └─ Manufacturer Signature (proves transfer to manufacturer)│
                                │
                                │ Sign-over with Customer's key
                                ▼
Final Voucher (Customer → Device)
┌─────────────────────────────────────────────────────────────┐
│ Device Voucher                                              │
│ ├─ Device GUID                                              │
│ ├─ Serial Number                                            │
│ ├─ Model Number                                             │
│ ├─ Factory Signature                                        │
│ ├─ Manufacturer Signature                                   │
│ └─ Customer Signature (proves transfer to customer)         │
└─────────────────────────────────────────────────────────────┘
```

## Configuration Relationships

```text
Configuration Matrix

Feature                First (Manufacturer)    Second (Customer)    Purpose
─────────────────────────────────────────────────────────────────────────────
Server Port           8083                    8084                Network isolation
Owner Key Generation  enabled                 enabled             Independent identities
DID Document Serving  enabled                 enabled             Discovery protocol
Voucher Receiver      enabled                 enabled             Accept vouchers
Voucher Signing       internal                internal            Sign vouchers
Owner Sign-over       static (Second's DID)   static (empty)     Transfer direction
DID Push              enabled                 disabled            Push capability
Pull Service          enabled                 disabled            Pull capability
Retry Worker          enabled                 disabled            Reliability
```

## Error Scenarios and Recovery

```text
Common Failure Points and Recovery

┌─────────────────┐    Network Failure    ┌─────────────────┐
│   First         │◀─────────────────────│   Second        │
│   :8083         │                      │   :8084         │
└─────────────────┘                      └─────────────────┘
        │                                        │
        │ Push fails                             │
        │                                        │
        ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ Retry Worker (First)                                        │
│ - Wait 2 seconds                                            │
│ - Retry up to 3 times                                       │
│ - Exponential backoff                                       │
│ - Log failures for debugging                                │
└─────────────────────────────────────────────────────────────┘

┌─────────────────┐    DID Resolution     ┌─────────────────┐
│   First         │◀─────────────────────│   Second        │
│   :8083         │    Failure            │   :8084         │
└─────────────────┘                      └─────────────────┘
        │                                        │
        │ Cannot resolve DID                     │
        │                                        │
        ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ Error Handling                                              │
│ - Log resolution failure                                    │
│ - Mark voucher as failed                                    │
│ - Manual intervention required                              │
│ - Check network connectivity                                │
└─────────────────────────────────────────────────────────────┘

┌─────────────────┐    FDOKeyAuth           ┌─────────────────┐
│   Second        │    Authentication    │   First         │
│   (Client)      │◀─────────────────────│   :8083         │
└─────────────────┘    Failure           └─────────────────┘
        │                                        │
        │ Authentication fails                  │
        │                                        │
        ▼                                        ▼
┌─────────────────────────────────────────────────────────────┐
│ Security Response                                           │
│ - No session token issued                                   │
│ - Detailed error logged                                     │
│ - Client must retry with proper credentials                 │
│ - Potential security incident logged                       │
└─────────────────────────────────────────────────────────────┘
```

## Supply Chain Mapping

```text

Real-World Supply Chain Mapping

Test Component           Real-World Equivalent          Business Function
─────────────────────────────────────────────────────────────────────────────
Test Voucher Generation  Factory Manufacturing Station   Create device vouchers
First Instance (:8083)   Manufacturer Voucher Service    Aggregate & distribute
Second Instance (:8084)  Customer Voucher Service        Receive & manage
DID Document             Partner Information System      Exchange contact info
DID Resolution           Business Partner Discovery     Find partner endpoints
Voucher Sign-over        Ownership Transfer Process      Transfer device rights
Push Transmission        Automated Delivery             Proactive voucher send
FDOKeyAuth Authentication  Secure Customer Portal         Authenticated access
```

## Timeline Visualization

```text
Execution Timeline (seconds)

0s    5s    10s   15s   20s   25s   30s   35s   40s
│     │     │     │     │     │     │     │     │
│     ├─ Start Second (Customer)
│     │
│     ├─ Fetch DID Document
│     │
│     ├─ Configure First with Customer DID
│     │
│     ├─ Start First (Manufacturer)
│     │
│     ├─ Generate Test Voucher
│     │
│     ├─ Push to First (Factory → Mfg)
│     │
│     ├─ DID Resolution (Mfg → Customer)
│     │
│     ├─ Sign-over & Push (Mfg → Customer)
│     │
│     ├─ Verify Delivery
│     │
│     ├─ FDOKeyAuth Handshake
│     │
│     └─ Verify Independent Identities
```

These diagrams help visualize the complex interactions between components in the FDO voucher supply chain test.
