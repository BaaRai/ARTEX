#!/bin/bash
echo "=== Testing OpenTRAP replay properly ==="
curl -s -X POST http://opentrap:8081/v1/replay \
  -H 'Content-Type: application/json' \
  -d '{"url":"http://httpbin.org/get","body":"{}"}'
echo