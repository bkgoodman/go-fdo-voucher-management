# End-to-End FDO Voucher Supply Chain Tutorial

This tutorial walks you through running two `fdo-voucher-manager` instances that simulate a manufacturer-to-customer voucher supply chain. You'll execute real commands, observe DID-based discovery, voucher sign-over, push delivery, and PullAuth authentication.

## Prerequisites

- The `fdo-voucher-manager` binary built and available at the project root
- `curl`, `python3`, and `sed` available on your system
- Ports 8083 and 8084 free on localhost

To build the binary (if you haven't already):

```bash
cd /path/to/go-fdo-voucher-managment
go build -o fdo-voucher-manager
```

## Learning Objectives

After completing this tutorial, you will understand:

- How organizations use **owner keys** to establish cryptographic identities
- How **DID documents** serve as digital business cards containing keys and service endpoints
- How **voucher sign-over** enables secure transfer of device ownership
- When to use **push** vs **pull** mechanisms in supply chains
- How **PullAuth** provides cryptographic authentication for voucher retrieval

## Real-World Scenario

Imagine this supply chain:

```text
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Factory       │    │   Manufacturer  │    │   Customer      │
│   (Shanghai)    │───▶│   Voucher Svc   │───▶│   Onboarding    │
│                 │    │   (Global)      │    │   Service       │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

We'll simulate the **Manufacturer → Customer** portion using two local instances:

- **Manufacturer** = `fdo-voucher-manager` on port **8083** (config: `config-e2e-first.yaml`)
- **Customer** = `fdo-voucher-manager` on port **8084** (config: `config-e2e-second.yaml`)

## Key Concepts Explained

### Owner Keys: Your Organization's Cryptographic Identity

Every organization in the supply chain needs its own cryptographic key pair:

```text
Factory Key     Manufacturer Key     Customer Key
    │                 │                   │
    └─ Signs voucher ─┘                   │
                      └─ Signs over ──────┘
```

**Why it matters**: When a voucher is signed to your key, it proves the device is intended for your organization. This prevents unauthorized parties from claiming ownership of devices.

### DID Documents: Digital Business Cards

A DID (Decentralized Identifier) document is like a digital business card that contains:

```json
{
  "id": "did:web:manufacturer.com:vouchers",
  "verificationMethod": [{
    "id": "#owner-key",
    "type": "JsonWebKey",
    "publicKeyJwk": {
      "kty": "EC",
      "crv": "P-384",
      "x": "...",
      "y": "..."
    }
  }],
  "service": [{
    "id": "#voucher-recipient",
    "type": "FDOVoucherRecipient",
    "serviceEndpoint": "https://manufacturer.com/api/v1/vouchers"
  }]
}
```

**Why it matters**: Instead of manually exchanging keys and endpoints, you can discover a trading partner's information automatically by resolving their DID.

### Voucher Sign-Over: Transferring Ownership

When a device moves between organizations, the voucher gets "signed over":

```text
Original Voucher (Factory → Manufacturer)
     │
     └─ Extended with Manufacturer's signature
         │
         └─ Extended with Customer's signature
```

**Why it matters**: This creates an unbroken chain of custody from factory to end customer, proving legitimate ownership at each step.

## Setup

All commands below assume you are in the project root directory:

```bash
cd /path/to/go-fdo-voucher-managment
```

Create the data directories both instances will use for voucher storage:

```bash
mkdir -p tests/data/vouchers-e2e-first
mkdir -p tests/data/vouchers-e2e-second
```

## Step-by-Step Tutorial

### Step 1: Start the Customer Service

The customer needs to be running first so the manufacturer can discover its DID document later.

Under the hood, `fdo-voucher-manager server` generates a new EC/P-384 owner key pair (because `first_time_init: true` in the config), starts an HTTP server, and serves a DID document at `/.well-known/did.json`.

Start the customer instance in the background:

```bash
./fdo-voucher-manager server -config tests/config-e2e-second.yaml \
    > tests/data/second.log 2>&1 &
CUSTOMER_PID=$!
echo "Customer started (PID: $CUSTOMER_PID)"
```

Wait a moment for it to initialize, then verify it's running:

```bash
curl -s http://localhost:8084/.well-known/did.json | python3 -m json.tool
```

You should see a JSON document containing the customer's public key and voucher recipient endpoint.

**Why it matters**: This represents the customer setting up their voucher service to receive devices from suppliers.

### Step 2: Discover the Customer's DID Information

In a real supply chain, the manufacturer discovers the customer's identity by fetching their DID document. Let's do that now:

```bash
# Fetch the customer's DID document
CUSTOMER_DID_DOC=$(curl -s http://localhost:8084/.well-known/did.json)

# Extract the DID URI (e.g., "did:web:localhost:8084")
CUSTOMER_DID_URI=$(echo "$CUSTOMER_DID_DOC" | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
echo "Customer DID URI: $CUSTOMER_DID_URI"

# Extract the voucher recipient endpoint
CUSTOMER_VOUCHER_URL=$(echo "$CUSTOMER_DID_DOC" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for svc in d.get('service', []):
    if svc.get('type') == 'FDOVoucherRecipient':
        print(svc.get('serviceEndpoint', ''))
        break
")
echo "Customer voucher endpoint: $CUSTOMER_VOUCHER_URL"
```

**What you'll see**: The DID URI identifies the customer's service, and the voucher endpoint is where vouchers should be sent. The DID document also contains the customer's public key, which the manufacturer will use for sign-over.

**Why it matters**: In production, this same DID resolution happens automatically — the manufacturer just needs the customer's DID URI and can discover everything else.

### Step 3: Configure the Manufacturer with the Customer's DID

The manufacturer's config template (`config-e2e-first.yaml`) has an empty `static_did` field. We fill it in with the customer's DID URI so the manufacturer knows who to sign vouchers over to:

```bash
sed "s|static_did: \"\"|static_did: \"$CUSTOMER_DID_URI\"|" \
    tests/config-e2e-first.yaml > tests/config-e2e-first-live.yaml
```

Verify the config was updated:

```bash
grep "static_did:" tests/config-e2e-first-live.yaml
```

You should see the customer's DID URI in the output.

**Why it matters**: This is how a manufacturer configures their system to automatically transfer vouchers to a specific customer. In production, this might be set via an admin UI or API rather than `sed`.

### Step 4: Start the Manufacturer Service

Now start the manufacturer instance using the config that knows about the customer:

```bash
./fdo-voucher-manager server -config tests/config-e2e-first-live.yaml \
    > tests/data/first.log 2>&1 &
MANUFACTURER_PID=$!
echo "Manufacturer started (PID: $MANUFACTURER_PID)"
```

Wait a moment, then verify:

```bash
curl -s http://localhost:8083/.well-known/did.json | python3 -m json.tool
```

The manufacturer now has its own, separate DID document with a different key pair.

**Why it matters**: The manufacturer is now ready to receive vouchers from factories and automatically forward them to the customer.

### Step 5: Simulate a Factory Sending a Voucher

A factory manufactures a device and creates a voucher for it. Then it pushes the voucher to the manufacturer's service:

```bash
# Generate a test voucher (simulates factory creating a voucher for a new device)
./fdo-voucher-manager generate voucher \
    -serial "E2E-DID-SERIAL-001" \
    -model "E2E-DID-MODEL-001" \
    -output tests/data/test-voucher-e2e.pem

# Push the voucher to the manufacturer's receiver endpoint (simulates factory → manufacturer)
curl -s -X POST http://localhost:8083/api/v1/vouchers \
    -F "voucher=@tests/data/test-voucher-e2e.pem" \
    -F "serial=E2E-DID-SERIAL-001" \
    -F "model=E2E-DID-MODEL-001"
```

You should get an HTTP 200 response with a JSON body containing a `voucher_id`.

**Why it matters**: This simulates the real flow where factories push vouchers to the manufacturer's centralized service.

### Step 6: Watch the Automatic DID-Based Push

Once the manufacturer receives the voucher, it automatically:

1. Resolves the customer's DID URI to fetch their public key and endpoint
2. Signs the voucher over to the customer's key
3. Pushes the signed voucher to the customer's endpoint

This happens in the background. Wait a few seconds, then check if the customer received it:

```bash
# Wait briefly for the async push to complete
sleep 5

# Check if the customer received a voucher file
ls -la tests/data/vouchers-e2e-second/
```

If you see a file, the push succeeded. You can also inspect the manufacturer's log to see the DID resolution and push:

```bash
# Check for DID resolution
grep "resolved static DID" tests/data/first.log

# Check for push to customer's endpoint
grep "localhost:8084" tests/data/first.log
```

**Why it matters**: This is the core supply chain operation — automatic, secure transfer of device ownership between organizations, driven entirely by DID discovery.

### Step 7: Try PullAuth Authentication (Alternative Transfer)

Push is one way to transfer vouchers. PullAuth lets the *customer* initiate retrieval instead. The customer authenticates to the manufacturer using a cryptographic handshake:

```bash
./fdo-voucher-manager pullauth \
    -url http://localhost:8083 \
    -key-type ec384 \
    -json
```

You should see JSON output containing:

- `status`: `"authenticated"`
- `session_token`: a token the customer can use to pull vouchers
- `owner_key_fingerprint`: identifies which key was used

**Why it matters**: PullAuth gives customers control over when they retrieve vouchers, rather than waiting for push. This is useful for:

- Just-in-time inventory management
- Bandwidth-constrained environments
- Security scenarios where the customer controls the timing

### Step 8: Pull Vouchers (Auth + List + Download)

The `pull` subcommand combines authentication, listing, and downloading into a single operation. It uses **Type-5 (PullAuth) challenge-response authentication** — the customer proves possession of its DID-minted owner key, and the manufacturer only returns vouchers that were signed over to that specific key.

**Configuration context:**

- **Customer config** (`tests/config-e2e-second.yaml`): The `did_minting.key_export_path` setting exports the DID-minted private key to a PEM file so the `pull` command can use it
- **Manufacturer config** (`tests/config-e2e-first.yaml`): The `pull_service.enabled: true` setting activates the Pull API endpoints
- **Owner key**: The customer's DID-minted key (exported to `tests/data/e2e-second-owner-key.pem`) — this is the same key that the manufacturer signed vouchers over TO when it resolved the customer's DID

**List vouchers using the customer's actual owner key:**

```bash
# Use the customer's DID-minted owner key (not an ephemeral key!)
# The manufacturer will only return vouchers whose owner_key_fingerprint
# matches this key's SHA-256 fingerprint.
./fdo-voucher-manager pull \
    -url http://localhost:8083 \
    -key tests/data/e2e-second-owner-key.pem \
    -list \
    -json
```

This outputs a JSON object with `voucher_count`, a `vouchers` array (each with GUID, serial, model, created timestamp), and a `continuation` token if there are more pages. **Only vouchers signed over to this owner key are returned.**

**Download vouchers to a directory:**

```bash
mkdir -p tests/data/pulled-vouchers

./fdo-voucher-manager pull \
    -url http://localhost:8083 \
    -key tests/data/e2e-second-owner-key.pem \
    -output tests/data/pulled-vouchers
```

Each voucher is saved as a `.fdoov` file named by its GUID.

**Pull only vouchers created after a specific time (incremental sync):**

```bash
./fdo-voucher-manager pull \
    -url http://localhost:8083 \
    -key tests/data/e2e-second-owner-key.pem \
    -since "2026-01-01T00:00:00Z" \
    -list
```

**Resume a previous pull using a continuation token:**

```bash
./fdo-voucher-manager pull \
    -url http://localhost:8083 \
    -key tests/data/e2e-second-owner-key.pem \
    -continuation "opaque-token-from-previous-response" \
    -list
```

**Limit page size:**

```bash
./fdo-voucher-manager pull \
    -url http://localhost:8083 \
    -key tests/data/e2e-second-owner-key.pem \
    -limit 10 \
    -list
```

**How owner-key scoping works:**

1. When a voucher is pushed to the manufacturer, the manufacturer extracts the voucher's current `OwnerPublicKey()` and stores its SHA-256 fingerprint in the `owner_key_fingerprint` column of the `voucher_transmissions` database table
2. When a customer authenticates via PullAuth, the manufacturer verifies the customer's signature and issues a session token bound to the customer's key fingerprint
3. When the customer lists or downloads vouchers, the manufacturer filters by `owner_key_fingerprint` — only returning vouchers that belong to this specific owner

**Why it matters**: The pull model supports several real-world scenarios:

- **Incremental sync**: Use `-since` to pull only new vouchers since your last sync
- **Disaster recovery**: Pull all vouchers from the beginning to rebuild state
- **Bandwidth management**: Use `-limit` and `-continuation` to page through large datasets
- **Audit**: Use `-list` to inspect available vouchers before downloading
- **Multi-tenant security**: Each owner only sees vouchers signed over to their key

### Step 9: Verify Independent Identities

Confirm that each instance has its own distinct cryptographic identity:

```bash
# Fetch both DID URIs
MANUFACTURER_DID=$(curl -s http://localhost:8083/.well-known/did.json | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
CUSTOMER_DID=$(curl -s http://localhost:8084/.well-known/did.json | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")

echo "Manufacturer DID: $MANUFACTURER_DID"
echo "Customer DID:     $CUSTOMER_DID"

# They should be different
if [ "$MANUFACTURER_DID" != "$CUSTOMER_DID" ]; then
    echo "PASS: Distinct identities confirmed"
else
    echo "FAIL: DIDs should be different"
fi
```

**Why it matters**: Each organization must maintain its own cryptographic identity. This prevents confusion and ensures proper ownership chains.

### Step 10: Verify Owner-Scoped Pull Isolation

This step proves that the Pull API enforces owner-key scoping. Two different keys authenticate to the same manufacturer — only the key that vouchers were signed over to can see them.

**Pull with the customer's owner key (should see vouchers):**

```bash
./fdo-voucher-manager pull \
    -url http://localhost:8083 \
    -key tests/data/e2e-second-owner-key.pem \
    -list \
    -json
# Expected: voucher_count > 0
```

**Pull with an unrelated ephemeral key (should see zero vouchers):**

```bash
./fdo-voucher-manager pull \
    -url http://localhost:8083 \
    -key-type ec384 \
    -list \
    -json
# Expected: voucher_count = 0
```

The unrelated key can authenticate (PullAuth succeeds for any valid key), but the Pull API returns zero vouchers because no vouchers in the database have an `owner_key_fingerprint` matching this key.

**Why it matters**: This is the multi-tenant security guarantee. In a real deployment, a manufacturer may hold vouchers for many different customers. Each customer can only list and download vouchers that were explicitly signed over to their owner key. This prevents:

- Customer A seeing Customer B's vouchers
- Unauthorized parties enumerating the voucher inventory
- Accidental cross-contamination of device ownership

## Cleanup

When you're done, stop both background processes and remove temporary files:

```bash
# Stop the servers
kill $MANUFACTURER_PID 2>/dev/null
kill $CUSTOMER_PID 2>/dev/null

# Wait a moment for graceful shutdown
sleep 1

# Force-kill if still running
kill -9 $MANUFACTURER_PID 2>/dev/null
kill -9 $CUSTOMER_PID 2>/dev/null

# Remove runtime data
rm -f tests/data/e2e-first.db tests/data/e2e-first.db-shm tests/data/e2e-first.db-wal
rm -f tests/data/e2e-second.db tests/data/e2e-second.db-shm tests/data/e2e-second.db-wal
rm -rf tests/data/vouchers-e2e-first tests/data/vouchers-e2e-second tests/data/pulled-vouchers
rm -f tests/data/test-voucher-e2e.pem
rm -f tests/data/e2e-first-owner-key.pem tests/data/e2e-second-owner-key.pem
rm -f tests/config-e2e-first-live.yaml
rm -f tests/data/first.log tests/data/second.log
```

If you lost track of the PIDs (e.g., you closed your terminal), find and kill straggling processes:

```bash
# Find any running fdo-voucher-manager processes
ps aux | grep fdo-voucher-manager | grep -v grep

# Kill them by PID, or kill all instances:
pkill -f fdo-voucher-manager
```

## Supply Chain Process Mapping

| Tutorial Step | Real-World Equivalent | Business Purpose |
| ----------- | ---------------------- | ------------------ |
| Start Customer | Customer sets up voucher service | Prepare to receive devices from suppliers |
| Discover DID | Manufacturer fetches customer's DID | Enable automatic voucher delivery |
| Configure Manufacturer | Manufacturer adds customer to system | Set up automatic forwarding |
| Start Manufacturer | Manufacturer runs voucher service | Centralized voucher management |
| Generate voucher | Factory manufactures device | Create device ownership record |
| Push to Manufacturer | Factory sends voucher to manufacturer | Aggregate vouchers from multiple factories |
| DID-based push | Manufacturer forwards to customer | Automatic delivery to customer |
| PullAuth | Customer authenticates to manufacturer | Prove ownership key for voucher retrieval |
| Pull Vouchers | Customer lists and downloads vouchers | On-demand voucher retrieval with filtering |

## When to Use Push vs Pull

### Push Model (DID-based)

**Use when**:

- Supplier knows customer's endpoint
- Timely delivery is critical
- Supplier wants to control timing

**Real-world examples**:

- Factory → Manufacturer (immediate aggregation)
- Manufacturer → Customer (just-in-time delivery)

### Pull Model (PullAuth + Pull)

**Use when**:

- Customer controls timing
- Bandwidth/constraints matter
- Customer wants to batch operations
- Incremental sync is needed (pull only new vouchers since last check)

**Key features**:

- **`-since`**: Pull only vouchers created after a given timestamp (ISO 8601). Use this for incremental sync — record the timestamp of your last pull and pass it next time
- **`-continuation`**: Resume a previous paginated pull. The server returns a continuation token when there are more pages; pass it back to get the next page
- **`-limit`**: Control page size for bandwidth management
- **`-until`**: Pull vouchers created before a given timestamp (useful for auditing a time range)

**Real-world examples**:

- Customer pulling from multiple suppliers on a schedule
- Large enterprises with complex inventory management
- Disaster recovery: pull all vouchers from the beginning to rebuild state
- Nightly batch sync using `-since` with the previous sync timestamp

## Troubleshooting Guide

### Common Issues and What They Teach

#### "DID resolution failed"

- **What it means**: Manufacturer can't reach customer's DID document
- **What it teaches**: Network connectivity and DNS resolution in supply chains
- **Fix**: Check that the customer instance is running: `curl -s http://localhost:8084/.well-known/did.json`

#### "Voucher sign-over failed"

- **What it means**: Cryptographic signing operation failed
- **What it teaches**: Key management and certificate validation
- **Fix**: Verify key formats, check certificate chains

#### "Push transmission failed"

- **What it means**: Customer's endpoint not reachable
- **What it teaches**: Service availability and error handling
- **Fix**: Check customer service status, verify the endpoint in the DID document matches

#### Port already in use

- **What it means**: A previous run left a process behind
- **Fix**: `pkill -f fdo-voucher-manager` and try again

### Debug Commands

```bash
# View DID documents
curl -s http://localhost:8083/.well-known/did.json | python3 -m json.tool
curl -s http://localhost:8084/.well-known/did.json | python3 -m json.tool

# Watch service logs in real time
tail -f tests/data/first.log
tail -f tests/data/second.log

# Verify voucher storage
ls -la tests/data/vouchers-e2e-first/
ls -la tests/data/vouchers-e2e-second/
```

## Running the Automated Test

If you'd rather run the entire flow as an automated test instead of step-by-step:

```bash
./tests/test-e2e-did-push-pull.sh
```

This script performs all the steps above plus assertions and cleanup. See [TEST_PLAN.md](TEST_PLAN.md) for details on the full test suite.

## Extension Exercises

Try these modifications to deepen your understanding:

### Exercise 1: Change the Supply Chain Direction

- Swap which instance has `did_push: enabled` and which has it disabled
- What configuration changes are needed?
- How does this model a different business relationship?

### Exercise 2: Add a Middleman

- Add a third instance on port 8085 representing a distributor
- Create a Factory → Distributor → Customer chain
- What additional DID resolutions are needed?

### Exercise 3: Disable DID Resolution

- Set `did_push: false` and use `push_service` with a hardcoded URL instead
- Compare the operational complexity
- When would this be preferable?

### Exercise 4: Simulate Failure Scenarios

- Stop the customer service before the manufacturer pushes
- Observe retry behavior in `tests/data/first.log`
- How does the system handle partial failures?

## Summary

This tutorial demonstrated:

1. **Organizational Identity**: Each party maintains its own keys and DID documents
2. **Automatic Discovery**: DID-based resolution eliminates manual configuration
3. **Secure Transfer**: Cryptographic sign-over maintains chain of custody
4. **Flexible Delivery**: Both push and pull models supported
5. **Real-World Mapping**: Tutorial operations mirror actual supply chain processes

## Next Steps

- Read [VOUCHER_SUPPLY_CHAIN.md](../VOUCHER_SUPPLY_CHAIN.md) for broader context
- Explore the [CONFIGURATION-GUIDE.md](CONFIGURATION-GUIDE.md) to understand all config options
- Try the [LEARNING-EXERCISES.md](LEARNING-EXERCISES.md) for more advanced scenarios
- Follow the [LEARNING-PATH.md](LEARNING-PATH.md) for a structured curriculum
