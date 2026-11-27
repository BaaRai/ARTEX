#!/usr/bin/env bash
# Build everything: sidecars + ARTEX
set -euo pipefail

ARTEX_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "=== Building Ghidra sidecar ==="
cd "$ARTEX_ROOT/sidecars/ghidra-headless-rest"
docker build -t artex-ghidra-headless-rest:local -f webui/Dockerfile .

echo ""
echo "=== Building OpenTRAP sidecar ==="
cd "$ARTEX_ROOT/opentrap-sidecar"
docker build -t artex-opentrap-sidecar:local .

echo ""
echo "=== Building ARTEX ==="
cd "$ARTEX_ROOT"
docker build -t artex:local .

echo ""
echo "=== All images built ==="
docker images | grep -E '(artex-ghidra|artex-opentrap|artex:local)' || true