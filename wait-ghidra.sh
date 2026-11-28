#!/bin/bash
JOB_ID=3d075c0546344784b733bc145fc54c0e
echo "=== Waiting for Ghidra job to complete ==="
for i in {1..60}; do
  STATUS=$(curl -s http://ghidra:9090/v1/jobs/$JOB_ID | grep -o '"status":"[^"]*"' | cut -d'"' -f4)
  echo "Status: $STATUS"
  if [ "$STATUS" = "done" ]; then
    break
  fi
  sleep 5
done

echo "=== Getting functions ==="
curl -s "http://ghidra:9090/v1/results/$JOB_ID/functions?limit=10"
echo

echo "=== Getting attack surface ==="
curl -s "http://ghidra:9090/v1/results/$JOB_ID/security/functions?limit=10"
echo