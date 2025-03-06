#!/bin/bash
# Compile for Debian and OpenWRT, then package both binaries

# Get git version information
GIT_VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "unknown")
echo "Building version: $GIT_VERSION"

echo "Compiling for OpenWRT."
env GOOS=linux CGO_ENABLED=1 CC=musl-gcc go build -o photonicat2_display_openwrt .
if [ $? -ne 0 ]; then
    echo "Error: OpenWRT build failed"
    exit 1
fi

echo "Compiling for Debian."
go build -o photonicat2_display_debian .
if [ $? -ne 0 ]; then
    echo "Error: Debian build failed"
    exit 1
fi

# Create package directory structure
PACKAGE_DIR="pcat2_display_package"
rm -rf "$PACKAGE_DIR"
mkdir -p "$PACKAGE_DIR/bin"

echo "Git version: $GIT_VERSION" > "$PACKAGE_DIR/VERSION.txt"

# Copy both binaries to the package with quiet option
cp photonicat2_display_openwrt "$PACKAGE_DIR/" 
cp photonicat2_display_debian "$PACKAGE_DIR/"

cp config.json "$PACKAGE_DIR/" 
cp -ar assets "$PACKAGE_DIR/" > /dev/null 2>&1

# Create tarball, current date and time
tar cvfJ photonicat2_display_package_$(date +%Y%m%d-%H%M)_${GIT_VERSION}.tar.xz "$PACKAGE_DIR"

# Clean up
rm -rf "$PACKAGE_DIR"