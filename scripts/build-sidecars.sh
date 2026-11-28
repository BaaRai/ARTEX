#!/usr/bin/env bash
# Build all sidecar images from source
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ARTEX_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SIDECARS_DIR="$ARTEX_ROOT/sidecars"

echo "=== Building ARTEX sidecars from source ==="

# Build Ghidra headless REST sidecar
echo ""
echo "--- Building ghidra-headless-rest ---"
cd "$SIDECARS_DIR/ghidra-headless-rest"
docker build -t artex-ghidra-headless-rest:local -f webui/Dockerfile .

# Build OpenTRAP sidecar
echo ""
echo "--- Building opentrap sidecar ---"
cd "$ARTEX_ROOT/opentrap-sidecar"
./build-opentrap-sidecar.sh

echo ""
echo "=== All sidecars built successfully ==="
echo "Images:"
docker images | grep -E '(artex-ghidra|artex-opentrap)' || true