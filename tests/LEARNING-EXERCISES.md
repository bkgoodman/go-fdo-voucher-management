# Learning Exercises for E2E DID Push-Pull Test

This document provides hands-on exercises to deepen your understanding of FDO voucher supply chains. Each exercise builds on the base test to explore different scenarios and configurations.

## Exercise 1: Reverse the Supply Chain Direction

### Exercise 1 Objective

Reverse the flow so the customer pushes to the manufacturer instead of the manufacturer pushing to the customer.

### Exercise 1 Background

In some supply chains, the customer might initiate voucher transfers (e.g., when a customer wants to return devices or transfer ownership back to the manufacturer).

### Exercise 1 Steps

1. **Copy the base test script**:

```bash
cp tests/test-e2e-did-push-pull.sh tests/exercise-1-reversed.sh
chmod +x tests/exercise-1-reversed.sh
```

1. **Modify the configuration**:
   - Edit `config-e2e-first.yaml` to disable `did_push`
   - Edit `config-e2e-second.yaml` to enable `did_push`
   - Swap the `pull_service` settings

2. **Update the script logic**:
   - Start First first (manufacturer)
   - Fetch First's DID
   - Configure Second with First's DID
   - Start Second (customer)
   - Push voucher to Second
   - Verify Second pushes to First

3. **Key changes needed**:

```bash
# In the script, change the order:
step_start_first || exit 1
run_test "Fetch First's DID" step_fetch_first_did
run_test "Create Second Config with First's DID" step_create_second_config
step_start_second || exit 1
```

### Exercise 1 What You'll Learn

- How DID-based discovery works in both directions
- The flexibility of push vs pull models
- How configuration changes affect supply chain flow

### Exercise 1 Verification

```bash
# Run the modified test
./tests/exercise-1-reversed.sh

# Check that the voucher flows from Second to First
ls -la tests/data/vouchers-e2e-first/
```

## Exercise 2: Add a Distributor Middleman

### Exercise 2 Objective

Create a three-party supply chain: Factory → Distributor → Customer.

### Exercise 2 Background

Many real supply chains include intermediaries like distributors who add value and handle logistics.

### Exercise 2 Steps

1. **Create a third configuration**:

```yaml
# config-e2e-distributor.yaml
debug: true
server:
  addr: localhost:8085
  use_tls: false
database:
  path: tests/data/e2e-distributor.db
key_management:
  key_type: ec384
  first_time_init: true
voucher_receiver:
  enabled: true
  endpoint: /api/v1/vouchers
  require_auth: false
  validate_ownership: false
voucher_signing:
  mode: internal
owner_signover:
  mode: static
  static_did: ""  # Will be set to customer's DID
  static_public_key: ""
voucher_files:
  directory: tests/data/vouchers-e2e-distributor
did_push:
  enabled: true  # Push to customer
pull_service:
  enabled: true  # Allow pull from factory
did_minting:
  enabled: true
  host: "localhost:8085"
  voucher_recipient_url: "http://localhost:8085/api/v1/vouchers"
  serve_did_document: true
  export_did_uri: true
retry_worker:
  enabled: true
  retry_interval: 2s
  max_attempts: 3
retention:
  keep_indefinitely: true
```

1. **Create the test script**:

```bash
cp tests/test-e2e-did-push-pull.sh tests/exercise-2-three-party.sh
```

1. **Modify the script for three instances**:
   - Start Customer (port 8084)
   - Fetch Customer's DID
   - Start Distributor (port 8085) with Customer's DID
   - Fetch Distributor's DID
   - Start Manufacturer (port 8083) with Distributor's DID
   - Push voucher to Manufacturer
   - Verify: Manufacturer → Distributor → Customer

2. **Key variables to add**:

```bash
PORT_DISTRIBUTOR=8085
DISTRIBUTOR_PID=""
DISTRIBUTOR_DID_URI=""
DISTRIBUTOR_VOUCHER_URL=""
```

### Exercise 2 What You'll Learn

- How vouchers flow through multiple intermediaries
- DID resolution in multi-hop scenarios
- Configuration management for complex chains
- Debugging multi-party failures

### Exercise 2 Verification

```bash
# Check all three storage directories
ls -la tests/data/vouchers-e2e-first/
ls -la tests/data/vouchers-e2e-distributor/
ls -la tests/data/vouchers-e2e-second/

# Verify the voucher was signed at each hop
# (You'll need to examine the voucher contents)
```

## Exercise 3: Static Endpoints Without DID Resolution

### Exercise 3 Objective

Configure the system to use static endpoints instead of DID-based discovery.

### Exercise 3 Background

Some organizations prefer static configuration for security or simplicity reasons.

### Exercise 3 Steps

1. **Create static configuration**:

```yaml
# config-e2e-first-static.yaml
# Copy from config-e2e-first.yaml and change:
push_service:
  enabled: true
  url: "http://localhost:8084/api/v1/vouchers"
  mode: push
  retry_interval: 2s
  max_attempts: 3

did_push:
  enabled: false  # Disable DID-based push

owner_signover:
  mode: static
  static_public_key: ""  # Will be set directly
  static_did: ""
```

1. **Extract customer's public key directly**:

```bash
# Add this function to your script:
extract_customer_public_key() {
    local response
    response=$(curl -s "http://localhost:$PORT_SECOND/.well-known/did.json")
    
    # Extract the public key JWK
    local public_key_jwk
    public_key_jwk=$(echo "$response" | python3 -c "
import sys, json
d = json.load(sys.stdin)
vm = d.get('verificationMethod', [{}])[0]
jwk = vm.get('publicKeyJwk', {})
print(json.dumps(jwk))
")
    
    # Convert JWK to PEM (you'll need a conversion tool)
    # For this exercise, we'll just store the JWK
    echo "$public_key_jwk" > "$TEST_DATA_DIR/customer-public-key.jwk"
}
```

1. **Configure static sign-over**:

```bash
# Instead of setting static_did, set static_public_key
sed "s|static_public_key: \"\"|static_public_key: \"$CUSTOMER_PUBLIC_KEY\"|" \
    config-e2e-first-static.yaml > config-e2e-first-live-static.yaml
```

### Exercise 3 What You'll Learn

- Trade-offs between DID discovery and static configuration
- How to extract and use public keys directly
- When static configuration might be preferable

### Exercise 3 Verification

```bash
# Run the static version
./tests/exercise-3-static.sh

# Compare logs - no DID resolution should occur
grep "resolved.*DID" tests/data/first.log || echo "No DID resolution (expected)"
```

## Exercise 4: Failure Scenario Simulation

### Exercise 4 Objective

Simulate various failure scenarios and observe how the system handles them.

### Exercise 4 Background

Understanding failure modes is crucial for production deployments.

### Scenario A: Network Partition

1. **Simulate network failure**:

```bash
# Start both instances normally
# Then block communication between them
sudo iptables -A OUTPUT -d localhost -p tcp --dport 8084 -j DROP
sudo iptables -A INPUT -s localhost -p tcp --sport 8084 -j DROP
```

1. **Push a voucher** and observe retry behavior:

```bash
# Push voucher to First
# Watch the retry attempts
tail -f tests/data/first.log | grep "retry"
```

1. **Restore connectivity**:

```bash
sudo iptables -D OUTPUT -d localhost -p tcp --dport 8084 -j DROP
sudo iptables -D INPUT -s localhost -p tcp --sport 8084 -j DROP
```

### Scenario B: Invalid DID Document

1. **Serve malformed DID**:

```bash
# Temporarily replace the DID document with invalid JSON
echo '{"invalid": "json"' > /tmp/invalid-did.json
# (You'd need to modify the service to serve this)
```

1. **Observe DID resolution failure**:

```bash
# Attempt to resolve the DID
curl -s http://localhost:8084/.well-known/did.json
```

### Scenario C: Key Mismatch

1. **Configure wrong public key**:

```bash
# Use First's own key instead of Second's for sign-over
# This should cause signature validation to fail
```

### Exercise 4 What You'll Learn

- Retry behavior and backoff strategies
- How different failure types are handled
- Debugging techniques for complex failures
- The importance of proper error handling

### Exercise 4 Verification

```bash
# Check retry attempts
grep "retry" tests/data/first.log | wc -l

# Check error messages
grep -i "error\|fail" tests/data/first.log
```

## Exercise 5: Performance and Scaling

### Exercise 5 Objective

Test the system under load and observe performance characteristics.

### Exercise 5 Background

Understanding performance limits helps with capacity planning.

### Exercise 5 Steps

1. **Generate multiple vouchers**:

```bash
# Create a script to generate many vouchers
for i in {1..100}; do
    "$PROJECT_ROOT/fdo-voucher-manager" generate voucher \
        -serial "PERF-SERIAL-$i" \
        -model "PERF-MODEL-001" \
        -output "$TEST_DATA_DIR/voucher-$i.pem"
done
```

1. **Push vouchers in parallel**:

```bash
# Push all vouchers concurrently
for voucher in "$TEST_DATA_DIR"/voucher-*.pem; do
    send_voucher "$voucher" "http://localhost:8083/api/v1/vouchers" "" "PERF-SERIAL-$i" "PERF-MODEL-001" &
done
wait
```

1. **Measure performance**:

```bash
# Time the entire operation
time ./tests/exercise-5-performance.sh

# Check resource usage
ps aux | grep fdo-voucher-manager
```

### Exercise 5 What You'll Learn

- System throughput limits
- Resource utilization patterns
- Bottleneck identification
- Scaling considerations

### Exercise 5 Verification

```bash
# Count successful deliveries
ls -la tests/data/vouchers-e2e-second/ | wc -l

# Check for any failures
grep -i "error\|fail" tests/data/*.log
```

## Exercise 6: Security Hardening

### Exercise 6 Objective

Implement security best practices and verify their effectiveness.

### Exercise 6 Background

Production systems need robust security controls.

### Exercise 6 Steps

1. **Enable authentication**:

```yaml
# In both configs:
voucher_receiver:
  require_auth: true
  validate_ownership: true
```

1. **Generate and configure tokens**:

```bash
# Create authentication tokens
"$PROJECT_ROOT/fdo-voucher-manager" tokens add \
    -token "factory-token-123" \
    -description "Factory authentication"

"$PROJECT_ROOT/fdo-voucher-manager" tokens add \
    -token "customer-token-456" \
    -description "Customer authentication"
```

1. **Test with and without tokens**:

```bash
# Try without token (should fail)
send_voucher "$voucher" "http://localhost:8083/api/v1/vouchers" "" "serial" "model"

# Try with token (should succeed)
send_voucher "$voucher" "http://localhost:8083/api/v1/vouchers" "factory-token-123" "serial" "model"
```

1. **Enable TLS** (if certificates are available):

```yaml
server:
  use_tls: true
  cert_file: "/path/to/cert.pem"
  key_file: "/path/to/key.pem"
```

### Exercise 6 What You'll Learn

- How authentication controls work
- Token management best practices
- TLS configuration requirements
- Security testing methodologies

### Exercise 6 Verification

```bash
# Test authentication failures
curl -i -X POST http://localhost:8083/api/v1/vouchers \
    -F "voucher=@test-voucher.pem"
# Should return 401 Unauthorized

# Test with valid token
curl -i -X POST http://localhost:8083/api/v1/vouchers \
    -H "Authorization: Bearer factory-token-123" \
    -F "voucher=@test-voucher.pem"
# Should return 200 OK
```

## Exercise 7: Custom Callback Integration

### Exercise 7 Objective

Implement custom callbacks for dynamic owner resolution and extra data.

### Exercise 7 Background

Callbacks enable integration with external systems for dynamic decision making.

### Exercise 7 Steps

1. **Create a callback server**:

```bash
# Create a simple HTTP server for callbacks
cat > callback-server.py << 'EOF'
from http.server import HTTPServer, BaseHTTPRequestHandler
import json

class CallbackHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        content_length = int(self.headers['Content-Length'])
        post_data = self.rfile.read(content_length)
        
        if '/owner-key' in self.path:
            # Return customer's public key
            response = {
                "public_key": "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE..."
            }
        elif '/extra-data' in self.path:
            # Return extra OVE data
            response = {
                "location": "Warehouse-A",
                "priority": "high"
            }
        else:
            self.send_response(404)
            return
            
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(response).encode())

HTTPServer(('localhost', 8086), CallbackHandler).serve_forever()
EOF

python3 callback-server.py &
CALLBACK_PID=$!
```

1. **Configure callbacks**:

```yaml
# In config-e2e-first.yaml:
owner_signover:
  mode: callback
  callback_url: "http://localhost:8086/owner-key"

ove_extra_data_service:
  enabled: true
  callback_url: "http://localhost:8086/extra-data"
```

1. **Test callback integration**:

```bash
# Push a voucher and observe callback calls
tail -f tests/data/first.log | grep "callback"
```

### Exercise 7 What You'll Learn

- How callbacks enable dynamic behavior
- Integration patterns with external systems
- Callback error handling
- Performance considerations

### Exercise 7 Verification

```bash
# Check callback server logs
# Verify vouchers contain extra data
# Test callback failure scenarios
```

## Debugging Tips for All Exercises

### Common Issues and Solutions

1. **Port conflicts**:

```bash
# Check what's using ports
netstat -tlnp | grep :808
# Kill existing processes
pkill -f "fdo-voucher-manager"
```

1. **Database locks**:

```bash
# Remove stale databases
rm -f tests/data/*.db tests/data/*.db-*
```

1. **Permission issues**:

```bash
# Ensure test directory is writable
chmod 755 tests/data/
```

1. **DID document issues**:

```bash
# Validate DID document format
curl -s http://localhost:8084/.well-known/did.json | python3 -m json.tool
```

### Useful Debug Commands

```bash
# Monitor all logs in real-time
tail -f tests/data/*.log

# Check voucher contents
openssl x509 -in tests/data/vouchers-e2e-*/voucher-*.pem -text -noout

# Test DID resolution manually
"$PROJECT_ROOT/fdo-voucher-manager" did resolve -did "did:web:localhost:8084"

# Verify database contents
sqlite3 tests/data/e2e-first.db "SELECT * FROM voucher_transmissions;"

# Test network connectivity
curl -v http://localhost:8084/api/v1/vouchers
```

### Performance Monitoring

```bash
# Monitor resource usage
top -p $(pgrep fdo-voucher-manager)

# Check network connections
netstat -an | grep :808

# Monitor file descriptors
lsof -p $(pgrep fdo-voucher-manager)
```

## Learning Outcomes

After completing these exercises, you should understand:

1. **Supply Chain Flexibility**: How to configure different flow patterns
2. **Failure Handling**: How the system responds to various failure modes
3. **Performance Characteristics**: Throughput limits and resource usage
4. **Security Controls**: Authentication, authorization, and encryption
5. **Integration Patterns**: Callbacks and external system integration
6. **Debugging Techniques**: Systematic troubleshooting approaches

## Next Steps

1. **Combine Exercises**: Try mixing multiple concepts (e.g., three-party chain with callbacks)
2. **Real-World Scenarios**: Model your actual supply chain requirements
3. **Performance Testing**: Scale up to hundreds or thousands of vouchers
4. **Security Auditing**: Implement comprehensive security controls
5. **Production Planning**: Design deployment strategies for production use

These exercises provide hands-on experience with FDO voucher supply chains and prepare you for real-world implementations.
