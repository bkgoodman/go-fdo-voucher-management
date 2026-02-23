# End-to-End FDO Voucher Supply Chain Tutorial

This tutorial walks you through the `test-e2e-did-push-pull.sh` script, demonstrating how FDO vouchers flow through a realistic supply chain. You'll learn not just **what** happens, but **why** it matters in real-world manufacturing and distribution scenarios.

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
│   Factory       │    │   Manufacturer │    │   Customer      │
│   (Shanghai)    │───▶│   Voucher Svc  │───▶│   Onboarding    │
│                 │    │   (Global)      │    │   Service       │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

Our test simulates the **Manufacturer → Customer** portion:

- **"First" instance** = Manufacturer's voucher service
- **"Second" instance** = Customer's voucher service  
- **Test voucher** = Device manufactured at the factory

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

## Step-by-Step Tutorial

Let's walk through the test script step by step.

### Step 1: Start the Customer Service ("Second")

```bash
# The script starts Second instance on port 8084
SECOND_PID=$(start_server "$SCRIPT_DIR/config-e2e-second.yaml" "$PORT_SECOND" "second")
```

**What happens**:

- Creates a new EC/P-384 key pair for the customer
- Starts a web server on port 8084
- Serves a DID document at `/.well-known/did.json`

**Why it matters**:
This represents the customer setting up their voucher service to receive devices from suppliers.

**Try it yourself**:

```bash
# After the test starts, you can view the DID document:
curl -s http://localhost:8084/.well-known/did.json | python3 -m json.tool
```

### Step 2: Discover Customer's Information

```bash
# Fetch Second's DID document
response=$(curl -s -w "\n%{http_code}" "http://localhost:$PORT_SECOND/.well-known/did.json")

# Extract DID URI and voucher endpoint
SECOND_DID_URI=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
SECOND_VOUCHER_URL=$(echo "$body" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for svc in d.get('service', []):
    if svc.get('type') == 'FDOVoucherRecipient':
        print(svc.get('serviceEndpoint', ''))
        break
")
```

**What happens**:

- Retrieves the customer's DID document
- Extracts their DID URI (like `did:web:localhost:8084`)
- Extracts their voucher recipient endpoint

**Why it matters**:
In a real supply chain, the manufacturer would discover the customer's information the same way, without manual configuration.

### Step 3: Configure Manufacturer for Customer

```bash
# Inject customer's DID into manufacturer's config
sed "s|static_did: \"\"|static_did: \"$SECOND_DID_URI\"|" \
    "$SCRIPT_DIR/config-e2e-first.yaml" > "$SCRIPT_DIR/config-e2e-first-live.yaml"
```

**What happens**:

- Creates a modified configuration for the manufacturer
- Sets `static_did` to the customer's DID URI
- This tells the manufacturer: "When you receive vouchers, sign them over to this DID"

**Why it matters**:
This is how a manufacturer configures their system to automatically transfer vouchers to a specific customer.

### Step 4: Start Manufacturer Service ("First")

```bash
FIRST_PID=$(start_server "$SCRIPT_DIR/config-e2e-first-live.yaml" "$PORT_FIRST" "first")
```

**What happens**:

- Creates manufacturer's own key pair (different from customer's)
- Starts web server on port 8083
- Configured to automatically sign over vouchers to customer's DID

**Why it matters**:
The manufacturer is now ready to receive devices from factories and forward them to the customer.

### Step 5: Simulate Factory Device Manufacturing

```bash
# Generate a test voucher (simulates factory creating voucher for new device)
"$PROJECT_ROOT/fdo-voucher-manager" generate voucher \
    -serial "E2E-DID-SERIAL-001" \
    -model "E2E-DID-MODEL-001" \
    -output "$test_voucher"

# Send to manufacturer (factory pushes to manufacturer's voucher service)
send_voucher "$test_voucher" "http://localhost:$PORT_FIRST/api/v1/vouchers" \
    "" "E2E-DID-SERIAL-001" "E2E-DID-MODEL-001"
```

**What happens**:

- Creates a voucher for a hypothetical device
- Factory pushes this voucher to the manufacturer's service
- Manufacturer receives and stores the voucher

**Why it matters**:
This simulates the real flow where factories push vouchers to the manufacturer's centralized service.

### Step 6: Automatic DID-Based Sign-Over and Push

```bash
# Wait for manufacturer to resolve customer's DID and push voucher
while [ $waited -lt $max_wait ]; do
    stored_voucher=$(find "$TEST_DATA_DIR/vouchers-e2e-second" -type f 2>/dev/null | head -1)
    if [ -n "$stored_voucher" ]; then
        push_succeeded=true
        break
    fi
    sleep 1
    ((waited++))
done
```

**What happens behind the scenes**:

1. Manufacturer receives voucher from factory
2. Resolves customer's DID URI to get their public key and endpoint
3. Signs the voucher over to customer's key
4. Pushes the signed voucher to customer's endpoint
5. Customer receives and stores the voucher

**Why it matters**:
This demonstrates the core supply chain operation: automatic, secure transfer of device ownership between organizations.

**Verify the log**:

```bash
# Check manufacturer's log for DID resolution
grep "resolved static DID" tests/data/first.log

# Check for push to customer's endpoint  
grep "localhost:8084" tests/data/first.log
```

### Step 7: PullAuth Authentication (Alternative Transfer)

```bash
# Customer authenticates to manufacturer using PullAuth
pullauth_output=$("$PROJECT_ROOT/fdo-voucher-manager" pullauth \
    -url "http://localhost:$PORT_FIRST" \
    -key-type ec384 \
    -json)
```

**What happens**:

- Customer initiates cryptographic authentication to manufacturer
- Uses their owner key to prove identity
- Manufacturer verifies and returns a session token
- Customer can now pull vouchers on demand

**Why it matters**:
PullAuth gives customers control over when they retrieve vouchers, rather than waiting for push. This is useful for:

- Just-in-time inventory management
- Bandwidth-constrained environments  
- Security scenarios where customer controls the timing

### Step 8: Verify Independent Identities

```bash
# Confirm both instances have different keys and DIDs
first_did=$(curl -s "http://localhost:$PORT_FIRST/.well-known/did.json" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
second_did=$(curl -s "http://localhost:$PORT_SECOND/.well-known/did.json" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
```

**What happens**:

- Verifies manufacturer and customer have different DID URIs
- Confirms they have different public keys
- Ensures proper separation of identities

**Why it matters**:
Each organization must maintain its own cryptographic identity. This prevents confusion and ensures proper ownership chains.

## Supply Chain Process Mapping

Let's map our test to real supply chain operations:

| Test Step | Real-World Equivalent | Business Purpose |
| ----------- | ---------------------- | ------------------ |
| Start "Second" | Customer sets up voucher service | Prepare to receive devices from suppliers |
| Discover DID | Customer shares DID with manufacturer | Enable automatic voucher delivery |
| Configure "First" | Manufacturer adds customer to system | Set up automatic forwarding |
| Start "First" | Manufacturer runs voucher service | Centralized voucher management |
| Generate voucher | Factory manufactures device | Create device ownership record |
| Push to "First" | Factory sends voucher to manufacturer | Aggregate vouchers from multiple factories |
| DID-based push | Manufacturer forwards to customer | Automatic delivery to customer |
| PullAuth | Customer pulls additional vouchers | On-demand voucher retrieval |

## When to Use Push vs Pull

### Push Model (DID-based)

**Use when**:

- Supplier knows customer's endpoint
- Timely delivery is critical
- Supplier wants to control timing

**Real-world examples**:

- Factory → Manufacturer (immediate aggregation)
- Manufacturer → Customer (just-in-time delivery)

### Pull Model (PullAuth)

**Use when**:

- Customer controls timing
- Bandwidth/constraints matter
- Customer wants to batch operations

**Real-world examples**:

- Customer pulling from multiple suppliers
- Large enterprises with complex inventory management
- Situations requiring audit trails before retrieval

## Troubleshooting Guide

### Common Issues and What They Teach

#### "DID resolution failed"

- **What it means**: Manufacturer can't reach customer's DID document
- **What it teaches**: Network connectivity and DNS resolution in supply chains
- **Fix**: Check network connectivity, DNS configuration

#### "Voucher sign-over failed"  

- **What it means**: Cryptographic signing operation failed
- **What it teaches**: Key management and certificate validation
- **Fix**: Verify key formats, check certificate chains

#### "Push transmission failed"

- **What it means**: Customer's endpoint not reachable
- **What it teaches**: Service availability and error handling
- **Fix**: Check customer service status, retry logic

### Debug Commands

```bash
# View DID documents
curl -s http://localhost:8083/.well-known/did.json | python3 -m json.tool
curl -s http://localhost:8084/.well-known/did.json | python3 -m json.tool

# Check service logs
tail -f tests/data/first.log
tail -f tests/data/second.log

# Verify voucher storage
ls -la tests/data/vouchers-e2e-first/
ls -la tests/data/vouchers-e2e-second/

# Test DID resolution manually
"$PROJECT_ROOT/fdo-voucher-manager" did resolve -did "did:web:localhost:8084"
```

## Extension Exercises

Try these modifications to deepen your understanding:

### Exercise 1: Change the Supply Chain Direction

- Modify the script so "Second" pushes to "First" instead
- What configuration changes are needed?
- How does this model a different business relationship?

### Exercise 2: Add a Middleman

- Add a third instance representing a distributor
- Create a Factory → Distributor → Customer chain
- What additional DID resolutions are needed?

### Exercise 3: Disable DID Resolution

- Configure static endpoints instead of DID-based discovery
- Compare the operational complexity
- When would this be preferable?

### Exercise 4: Simulate Failure Scenarios

- Take down the customer service during push
- Observe retry behavior
- How does the system handle partial failures?

## Summary

This tutorial demonstrated:

1. **Organizational Identity**: Each party maintains its own keys and DID documents
2. **Automatic Discovery**: DID-based resolution eliminates manual configuration
3. **Secure Transfer**: Cryptographic sign-over maintains chain of custody
4. **Flexible Delivery**: Both push and pull models supported
5. **Real-World Mapping**: Test operations mirror actual supply chain processes

The `test-e2e-did-push-pull.sh` script is more than a test—it's a complete reference implementation of how FDO vouchers flow through modern supply chains. By understanding each step, you're equipped to design and deploy voucher services for any organization in the FDO ecosystem.

## Next Steps

- Read [VOUCHER_SUPPLY_CHAIN.md](../VOUCHER_SUPPLY_CHAIN.md) for broader context
- Explore other test scripts to see different scenarios
- Try the extension exercises to customize for your use case
- Review the configuration files to understand deployment options
