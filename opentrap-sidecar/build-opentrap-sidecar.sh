#!/usr/bin/env bash
# Build OpenTRAP Java app and Go REST wrapper sidecar
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ARTEX_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OPENTRAP_SRC="$ARTEX_ROOT/sidecars/opentrap"
BUILD_DIR="$SCRIPT_DIR/build"
DIST_DIR="$SCRIPT_DIR/dist"

echo "=== Building OpenTRAP sidecar ==="

# Clean and prepare
rm -rf "$BUILD_DIR" "$DIST_DIR"
mkdir -p "$BUILD_DIR" "$DIST_DIR"

# Step 1: Fetch dependencies (JDK, ZAP engine, browser)
echo "Fetching dependencies..."
cd "$OPENTRAP_SRC"
./fetch-jdk.sh
./fetch-zap.sh
./fetch-browser.sh

# Step 2: Compile OpenTRAP Java app
echo "Compiling OpenTRAP Java app..."
cd "$OPENTRAP_SRC"
export JAVA_HOME="$OPENTRAP_SRC/jdk"
export PATH="$JAVA_HOME/bin:$PATH"

# Use the existing build.sh but output to our build dir
"$OPENTRAP_SRC/build.sh"

# Copy the built jar to our dist
cp "$OPENTRAP_SRC/dist/opentrap.jar" "$DIST_DIR/"

# Step 3: Build Go REST wrapper
echo "Building Go REST wrapper..."
cd "$SCRIPT_DIR"
go build -o "$DIST_DIR/opentrap-wrapper" ./wrapper

# Step 4: Build Docker image
echo "Building Docker image..."
docker build -t artex-opentrap-sidecar:local -f Dockerfile "$SCRIPT_DIR"

echo "=== OpenTRAP sidecar built successfully ==="