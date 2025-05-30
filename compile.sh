#!/bin/bash
# Compile for Debian and OpenWRT (aarch64 when on x86_64), then package both binaries

set -e
export PATH=/usr/local/aarch64-linux-musl-cross/bin:$PATH

# Get git version information
GIT_VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "unknown")
echo "Building version: $GIT_VERSION"

# Detect host architecture
HOST_ARCH=$(uname -m)
echo "Host architecture: $HOST_ARCH"

if [ "$HOST_ARCH" = "x86_64" ]; then
    echo "→ Cross‑compiling for aarch64"
    # when on AMD64 host, force GOARCH=arm64 and use aarch64 cross‑compilers
    BUILD_ENV="GOOS=linux GOARCH=arm64 CGO_ENABLED=1"
    OPENWRT_CC="aarch64-linux-musl-gcc"
    DEBIAN_CC="aarch64-linux-gnu-gcc"
else
    echo "→ Native compile (host is already aarch64 or other)"
    # on aarch64 host, default GOARCH is arm64; on others, you may adjust as needed
    BUILD_ENV="GOOS=linux CGO_ENABLED=1"
    OPENWRT_CC="musl-gcc"
    DEBIAN_CC="gcc"
fi

echo "Compiling for OpenWRT..."
env $BUILD_ENV CC=$OPENWRT_CC go build -o pcat2_mini_display_openwrt .
echo "  ✓ OpenWRT build succeeded"

echo "Compiling for Debian..."
env $BUILD_ENV CC=$DEBIAN_CC go build -o pcat2_mini_display_debian .
echo "  ✓ Debian build succeeded"

# Package up
PACKAGE_DIR="pcat2_mini_display_package"
rm -rf "$PACKAGE_DIR"
mkdir -p "$PACKAGE_DIR"

echo "Git version: $GIT_VERSION" > "$PACKAGE_DIR/VERSION.txt"
cp pcat2_mini_display_openwrt "$PACKAGE_DIR/"
cp pcat2_mini_display_debian "$PACKAGE_DIR/"
cp config.json "$PACKAGE_DIR/"
cp -ar assets "$PACKAGE_DIR/" > /dev/null 2>&1

TAR_NAME="pcat2_mini_display_package_$(date +%Y%m%d-%H%M)_${GIT_VERSION}.tar.xz"
tar cvfJ "$TAR_NAME" "$PACKAGE_DIR"
echo "Created package: $TAR_NAME"

# Clean up
rm -rf "$PACKAGE_DIR"

# If there's a deploy.sh, run it
if [ -f deploy.sh ]; then
    echo "Running deploy.sh..."
    ./deploy.sh
fi
