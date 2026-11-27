#!/bin/bash
# Test real sidecar integration

set -e

echo "=== Testing Ghidra real API ==="
curl -s http://ghidra:9090/healthz
echo
curl -s http://ghidra:9090/v1/version
echo
curl -s http://ghidra:9090/v1/capabilities
echo

echo "=== Testing OpenTRAP real API ==="
curl -s http://opentrap:8081/v1/status
echo
curl -s http://opentrap:8081/v1/scope
echo

# Create a simple test binary
echo 'int main() { return 0; }' > /tmp/test.c
gcc -o /tmp/test.bin /tmp/test.c

# Upload to Ghidra
echo "=== Uploading test binary to Ghidra ==="
RESPONSE=$(curl -s -X POST -F 'file=@/tmp/test.bin' http://ghidra:9090/v1/jobs)
echo $RESPONSE
JOB_ID=$(echo $RESPONSE | grep -o '"job_id":"[^"]*"' | cut -d'"' -f4)
echo "Job ID: $JOB_ID"

# Poll for completion
echo "=== Polling for completion ==="
for i in {1..30}; do
  STATUS=$(curl -s http://ghidra:9090/v1/jobs/$JOB_ID | grep -o '"status":"[^"]*"' | cut -d'"' -f4)
  echo "Status: $STATUS"
  if [ "$STATUS" = "done" ]; then
    break
  fi
  sleep 2
done

# Get functions
echo "=== Getting functions ==="
curl -s "http://ghidra:9090/v1/results/$JOB_ID/functions?limit=10"
echo

# Get attack surface
echo "=== Getting attack surface ==="
curl -s "http://ghidra:9090/v1/results/$JOB_ID/security/functions?limit=10"
echo

# Test OpenTRAP replay
echo "=== Testing OpenTRAP replay ==="
curl -s -X POST http://opentrap:8081/v1/replay \
  -H "X-OpenTrap-URL: http://httpbin.org/get" \
  -H "Content-Type: application/json" \
  -d '{"test": "value"}'
echo