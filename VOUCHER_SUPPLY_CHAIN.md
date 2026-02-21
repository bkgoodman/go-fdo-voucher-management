# FDO Voucher Supply Chain

This document describes the real-world supply chain scenarios that motivate the design of the FDO Voucher Manager, and how this project fits into the broader FDO ecosystem.

## Background: What Is an Ownership Voucher?

In FIDO Device Onboard (FDO), an **ownership voucher** is a cryptographic document created during device manufacturing. It binds a device's identity to its current owner and enables secure, zero-touch onboarding. When a device is sold or transferred, the voucher is **signed over** (extended) to the new owner's key, forming a chain of custody from the original manufacturer to the final operator.

The FDO specification defines how devices use vouchers during onboarding (the DI, TO1, and TO2 protocols). But the specification is largely silent on how vouchers move *between organizations* before a device ever powers on at its final destination. That is the problem this project addresses.

## The Simple Case

In the simplest deployment, a single organization manufactures devices and operates the onboarding service:

```text
┌──────────────────────┐         ┌──────────────────────┐
│  Manufacturing       │         │  Onboarding          │
│  Station             │────────▶│  Service              │
│  (creates voucher)   │  push   │  (onboards device)   │
└──────────────────────┘         └──────────────────────┘
```

The manufacturing station initializes each device (FDO DI), produces a voucher, signs it over to the onboarding service's key, and pushes it directly. When the device arrives and powers on, the onboarding service already has the voucher and can complete TO1/TO2. Done.

This works when the manufacturer knows exactly who the end user is and where their onboarding service lives at the time of manufacturing. In practice, this is the exception rather than the rule.

## The Real World

### Factories vs. Manufacturers

It is important to distinguish between a **factory** (a physical plant that produces devices) and a **manufacturer** (the organization that sells them). A manufacturer may operate multiple factories across different regions. Each factory has its own manufacturing station producing vouchers, but the manufacturer needs a centralized system to collect, organize, and distribute those vouchers to customers.

```text
┌─────────────┐
│  Factory A   │──┐
│  (Shanghai)  │  │
└─────────────┘  │     ┌──────────────────────┐
                  ├────▶│  Manufacturer's       │
┌─────────────┐  │     │  Voucher Service      │
│  Factory B   │──┘     │  (centralized)        │
│  (Austin)    │        └──────────────────────┘
└─────────────┘
```

### Build-to-Stock vs. Build-to-Order

When devices are **built to order**, the manufacturer knows the customer at manufacturing time and could, in theory, sign vouchers directly to the customer's key. But when devices are **built to stock**, there is no customer yet. Vouchers must be held, managed, and signed over later when a sale is made. A voucher management service handles this lifecycle.

### Resellers and Distributors

Many devices pass through one or more intermediaries before reaching the end user. An OEM might sell to a distributor, who sells to a regional reseller, who sells to the end customer. Each intermediary in the chain may need to receive vouchers, sign them over to the next party, and forward them:

```text
┌──────────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Factory     │────▶│  OEM Voucher │────▶│  Reseller    │────▶│  Customer    │
│  (mfg stn)  │     │  Service     │     │  Voucher Svc │     │  Onboarding  │
└──────────────┘     └──────────────┘     └──────────────┘     └──────────────┘
```

At each hop, the same fundamental operations occur:

1. **Receive** a voucher (via push or pull)
2. **Store** it
3. **Sign it over** to the next owner's key
4. **Transmit** it downstream (via push, or make it available for pull)

### Customer-Operated Voucher Services

Large customers (enterprises, fleet operators) may operate their own voucher management service. They receive vouchers from multiple suppliers and distribute them across their own onboarding infrastructure, which may span multiple sites, regions, or cloud environments:

```text
                                          ┌──────────────────┐
                                     ┌───▶│  Onboarding Svc  │
┌──────────────┐     ┌────────────┐  │    │  (Site A)        │
│  Supplier X  │────▶│  Customer  │──┤    └──────────────────┘
└──────────────┘     │  Voucher   │  │    ┌──────────────────┐
┌──────────────┐     │  Service   │──┴───▶│  Onboarding Svc  │
│  Supplier Y  │────▶│            │       │  (Site B)        │
└──────────────┘     └────────────┘       └──────────────────┘
```

### Third-Party and SaaS Voucher Services

Not every organization wants to stand up and operate their own voucher management infrastructure. Voucher services, whether for an OEM, a reseller, or an end customer, may be operated by a third party or offered as a cloud SaaS product. The APIs and protocols are the same regardless of who operates the service.

## The General Pattern

All of these scenarios reduce to the same pattern:

```text
Voucher Source ──▶ Voucher Service ──▶ Voucher Destination
```

Where:

- A **Voucher Source** is anything that produces or holds vouchers (a manufacturing station, another voucher service, etc.)
- A **Voucher Service** receives, stores, signs over, and transmits vouchers
- A **Voucher Destination** is anything that consumes vouchers (an onboarding service, another voucher service, etc.)

The chain can be arbitrarily long:

```text
Mfg Station ──▶ Voucher Svc ──▶ Voucher Svc ──▶ ... ──▶ Onboarding Svc
```

And the critical design insight is that **the code and APIs for sending and receiving vouchers are the same at every hop**. A voucher service doesn't need to know whether it's talking to a factory, another reseller, or the final customer. The protocol is uniform.

## How This Project Fits In

The **FDO Voucher Manager** implements the "Voucher Service" box in the diagrams above. It is a general-purpose intermediary that can play any role in the supply chain:

| Role | Configuration |
|------|---------------|
| **Factory aggregator** | Receives vouchers from one or more manufacturing stations, forwards to OEM headquarters |
| **OEM voucher portal** | Receives from factories, signs over to customer keys, pushes or serves for pull |
| **Reseller service** | Receives from upstream supplier, signs over and forwards to downstream buyer |
| **Customer hub** | Receives from multiple suppliers, distributes to internal onboarding services |
| **SaaS platform** | Multi-tenant voucher management for multiple organizations |

### Transfer Mechanisms

The project supports two complementary transfer models:

- **Push**: The sender initiates transmission. Used when the sender knows the recipient's endpoint (e.g., a factory pushing to a known OEM service).
- **Pull (with PullAuth)**: The recipient initiates retrieval, authenticating with cryptographic proof of ownership. Used when the recipient wants to fetch vouchers on their own schedule (e.g., a customer pulling from a supplier's service).

### Discovery via DID

In many cases, the sender doesn't have a pre-configured endpoint for the recipient. The project supports **did:web** resolution: given a DID URI for a trading partner, the service can resolve it to discover the partner's public key and voucher recipient URL automatically.

## Relationship to go-fdo

This project depends on the [go-fdo](go-fdo/) library, which implements the core FDO protocols (DI, TO1, TO2) and provides the cryptographic primitives for voucher creation, signing, and validation. The `transfer` and `did` packages within go-fdo provide the building blocks for voucher push, pull, and DID resolution. This project assembles those building blocks into a complete, configurable service.

## Summary

| Concept | Description |
|---------|-------------|
| **Factory** | A physical plant with a manufacturing station that initializes devices and creates vouchers |
| **Manufacturer** | An organization (possibly with multiple factories) that sells devices |
| **Voucher Service** | An intermediary that receives, stores, signs over, and transmits vouchers |
| **Onboarding Service** | The final destination that uses vouchers to onboard devices via FDO TO1/TO2 |
| **Push** | Sender-initiated voucher transmission |
| **Pull** | Recipient-initiated voucher retrieval (with PullAuth cryptographic authentication) |
| **Sign-over** | Extending the voucher's ownership chain to a new owner's key |
| **DID** | Decentralized Identifier used to discover a partner's key and voucher endpoint |
