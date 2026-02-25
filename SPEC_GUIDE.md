# FDO Voucher Transfer Protocol — Reader's Guide

A quick orientation for people familiar with FDO concepts who want to know what this spec covers and where to look.

## Start Here: Section 12 (DID Integration)

If you read one section, read this one. It answers the question: **"How do two parties set up a secure voucher transfer relationship?"**

The answer is surprisingly simple: **they exchange DIDs** — short strings like `did:web:acme-mfg.com` that can be pasted into a purchase order, typed into a web portal, or included in an ordering system. From those short identifiers, each side can resolve the other's DID document to obtain public keys, service endpoints, and an implicit trust relationship. From that point on, the protocol's own cryptographic mechanisms handle all authentication and authorization — no tokens, no API keys, no passwords.

Section 12 walks through four specific trust cases (push/pull x provider/recipient) and shows why each one is covered by the cryptography already in the protocol. It also covers optional TLS certificate pinning via DID service entries and defines the FDO JSON-LD context for formal DID tooling interoperability.

## Then Go Back to the Foundations

### Sections 3 & 4 — Use Cases and Transfer Models

Two ways to move vouchers: **push** (manufacturer sends them to you) and **pull** (you go get them). Section 3 explains why you'd want each; Section 4 defines the flows.

### Section 5 — Voucher File Format

Standardizes the `.fdoov` file extension, PEM encoding, MIME type (`application/x-fdo-voucher`), and size limits. Short section — just enough to ensure everyone agrees on the wire format.

### Section 6 — Service Root URLs

Defines configurable base URLs for push and pull endpoints. Instead of hardcoding paths like `/api/v1/vouchers`, all API endpoints are relative to a "Service Root URL" that each deployment chooses. This is what gets advertised in DID documents (Section 12).

### Sections 7 & 8 — Push and Pull API

The REST APIs. Section 7 is push (POST a voucher, query status, list, download). Section 8 is pull (list vouchers, download, plus subscription/notification options). Standard REST with pagination, filtering, and the usual HTTP status codes.

### Section 9 — Pull Authentication (PullAuth)

This is the interesting one. Instead of pre-shared tokens, the pull API uses a **TO2-like challenge-response protocol** where your Owner Key IS the credential. If the voucher was signed over to your key, you can prove you hold that key and pull your vouchers — zero provisioning. Also supports **Delegate Certificates** so authorized delegates can pull on behalf of an Owner.

## Sections You Can Skip (Until You Need Them)

- **Section 10 (Security Framework)** — Catalog of security models (tokens, mTLS, voucher signatures, business logic, owner-key auth). Useful reference material, but Section 12 explains that only voucher signatures and PullAuth are core — the rest are optional defense-in-depth layers for API gateways and WAFs.

- **Section 11 (Voucher Sequestering)** — Quarantine workflow for risk-based voucher acceptance. Interesting for high-security deployments; not needed to understand the protocol.

- **Sections 13-15 (Error Handling, Security Considerations, Implementation Guidelines)** — Reference material. Retry strategies, TLS requirements, idempotency, versioning. Consult as needed during implementation.

## One-Sentence Summary

Exchange DIDs once (short strings, not files), then push and pull vouchers over REST with cryptographic authentication baked into the protocol itself.
