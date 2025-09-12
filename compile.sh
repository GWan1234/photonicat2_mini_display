#!/bin/bash
# Compile for Debian and OpenWRT (aarch64 when on x86_64), then package both binaries

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}PhotoniCat2 Display Build Script${NC}"
echo "======================================"

# Function to check if command exists
check_command() {
    if command -v "$1" >/dev/null 2>&1; then
        echo -e "  ✓ ${GREEN}$1 found${NC}"
        return 0
    else
        echo -e "  ✗ ${RED}$1 not found${NC}"
        return 1
    fi
}

# Function to check if file exists
check_file() {
    if [ -f "$1" ]; then
        echo -e "  ✓ ${GREEN}$1 exists${NC}"
        return 0
    else
        echo -e "  ✗ ${RED}$1 not found${NC}"
        return 1
    fi
}

# Function to check if directory exists
check_directory() {
    if [ -d "$1" ]; then
        echo -e "  ✓ ${GREEN}$1 exists${NC}"
        return 0
    else
        echo -e "  ✗ ${RED}$1 not found${NC}"
        return 1
    fi
}

echo -e "\n${YELLOW}1. Checking host system...${NC}"

# Detect host architecture
HOST_ARCH=$(uname -m)
echo "Host architecture: $HOST_ARCH"

# Check if we're on x86_64 (required for cross-compilation)
if [ "$HOST_ARCH" != "x86_64" ]; then
    echo -e "${YELLOW}Warning: This script is optimized for x86_64 hosts doing cross-compilation.${NC}"
    echo -e "${YELLOW}You're on $HOST_ARCH - compilation may work but paths might need adjustment.${NC}"
fi

echo -e "\n${YELLOW}2. Checking required tools...${NC}"

# Check basic tools
missing_tools=0

if ! check_command "go"; then
    echo -e "${RED}Go is required for compilation.${NC}"
    echo -e "Install with: ${BLUE}wget https://go.dev/dl/go1.21.0.linux-amd64.tar.gz && sudo tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz${NC}"
    echo -e "Then add to PATH: ${BLUE}export PATH=\$PATH:/usr/local/go/bin${NC}"
    missing_tools=1
else
    go_version=$(go version 2>/dev/null || echo "unknown")
    echo "    Go version: $go_version"
fi

if ! check_command "git"; then
    echo -e "${RED}Git is required for version information.${NC}"
    echo -e "Install with: ${BLUE}sudo apt install git${NC}"
    missing_tools=1
fi

# Check cross-compilation tools for x86_64 hosts
if [ "$HOST_ARCH" = "x86_64" ]; then
    echo -e "\n${YELLOW}3. Checking cross-compilation tools...${NC}"
    
    # Check for aarch64-linux-gnu-gcc (Debian cross-compiler)
    if ! check_command "aarch64-linux-gnu-gcc"; then
        echo -e "${RED}aarch64-linux-gnu-gcc is required for Debian builds.${NC}"
        echo -e "Install with: ${BLUE}sudo apt install gcc-aarch64-linux-gnu${NC}"
        missing_tools=1
    fi
    
    # Check for musl cross-compiler directory
    if ! check_directory "/usr/local/aarch64-linux-musl-cross"; then
        echo -e "${RED}aarch64-linux-musl-cross toolchain is required for OpenWRT builds.${NC}"
        echo -e "Install with:"
        echo -e "  ${BLUE}wget https://musl.cc/aarch64-linux-musl-cross.tgz${NC}"
        echo -e "  ${BLUE}sudo tar -C /usr/local -xzf aarch64-linux-musl-cross.tgz${NC}"
        missing_tools=1
    else
        # Check for the specific compiler in the toolchain
        if ! check_file "/usr/local/aarch64-linux-musl-cross/bin/aarch64-linux-musl-gcc"; then
            echo -e "${RED}aarch64-linux-musl-gcc not found in toolchain.${NC}"
            echo -e "The toolchain may be incomplete. Try reinstalling:"
            echo -e "  ${BLUE}sudo rm -rf /usr/local/aarch64-linux-musl-cross${NC}"
            echo -e "  ${BLUE}wget https://musl.cc/aarch64-linux-musl-cross.tgz${NC}"
            echo -e "  ${BLUE}sudo tar -C /usr/local -xzf aarch64-linux-musl-cross.tgz${NC}"
            missing_tools=1
        fi
    fi
    
    # Additional tools that might be needed
    if ! check_command "pkg-config"; then
        echo -e "${YELLOW}pkg-config recommended for build process.${NC}"
        echo -e "Install with: ${BLUE}sudo apt install pkg-config${NC}"
    fi
else
    echo -e "\n${YELLOW}3. Native compilation mode (non-x86_64 host)${NC}"
    
    # Check for native gcc
    if ! check_command "gcc"; then
        echo -e "${RED}gcc is required for native compilation.${NC}"
        echo -e "Install with: ${BLUE}sudo apt install gcc${NC}"
        missing_tools=1
    fi
    
    # Check for musl-gcc if available
    if ! check_command "musl-gcc"; then
        echo -e "${YELLOW}musl-gcc not found. OpenWRT build might fail.${NC}"
        echo -e "Install with: ${BLUE}sudo apt install musl-tools${NC}"
    fi
fi

echo -e "\n${YELLOW}4. Checking project files...${NC}"

# Check required project files
if ! check_file "go.mod"; then
    echo -e "${RED}go.mod not found. Make sure you're in the project root directory.${NC}"
    missing_tools=1
fi

if ! check_file "main.go"; then
    echo -e "${RED}main.go not found. Make sure you're in the project root directory.${NC}"
    missing_tools=1
fi

if ! check_file "config.json"; then
    echo -e "${YELLOW}config.json not found. Build will continue but packaging may fail.${NC}"
fi

if ! check_directory "assets"; then
    echo -e "${YELLOW}assets directory not found. Build will continue but packaging may fail.${NC}"
fi

# Summary
echo -e "\n${YELLOW}5. Pre-build summary${NC}"

if [ $missing_tools -eq 1 ]; then
    echo -e "${RED}❌ Missing required tools or dependencies.${NC}"
    echo -e "\n${YELLOW}Quick install for Ubuntu/Debian x86_64:${NC}"
    echo -e "${BLUE}sudo apt update${NC}"
    echo -e "${BLUE}sudo apt install git gcc-aarch64-linux-gnu pkg-config musl-tools${NC}"
    echo -e "${BLUE}wget https://musl.cc/aarch64-linux-musl-cross.tgz${NC}"
    echo -e "${BLUE}sudo tar -C /usr/local -xzf aarch64-linux-musl-cross.tgz${NC}"
    echo -e "\n${YELLOW}For Go installation:${NC}"
    echo -e "${BLUE}wget https://go.dev/dl/go1.21.0.linux-amd64.tar.gz${NC}"
    echo -e "${BLUE}sudo tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz${NC}"
    echo -e "${BLUE}export PATH=\$PATH:/usr/local/go/bin${NC}"
    echo ""
    exit 1
else
    echo -e "${GREEN}✅ All prerequisites satisfied!${NC}"
    echo -e "Proceeding with build...\n"
fi

# Export PATH for musl cross-compiler
export PATH=/usr/local/aarch64-linux-musl-cross/bin:$PATH

# Get git version information
GIT_VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "unknown")
echo "Building version: $GIT_VERSION"
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

# Build cross-compilation targets first if on x86_64
if [ "$HOST_ARCH" = "x86_64" ]; then
    echo -e "\n${YELLOW}6. Cross-compiling for target systems...${NC}"
    
    echo "Compiling for OpenWRT (aarch64)..."
    env $BUILD_ENV CC=$OPENWRT_CC go build -o pcat2_mini_display_openwrt .
    if [ $? -eq 0 ]; then
        echo -e "  ✓ ${GREEN}OpenWRT build succeeded${NC}"
    else
        echo -e "  ✗ ${RED}OpenWRT build failed${NC}"
    fi

    echo "Compiling for Debian (aarch64)..."
    env $BUILD_ENV CC=$DEBIAN_CC go build -o pcat2_mini_display_debian .
    if [ $? -eq 0 ]; then
        echo -e "  ✓ ${GREEN}Debian build succeeded${NC}"
    else
        echo -e "  ✗ ${RED}Debian build failed${NC}"
    fi
else
    echo -e "\n${YELLOW}6. Skipping cross-compilation (not on x86_64 host)${NC}"
    echo -e "  Will build native for $HOST_ARCH"
fi

# Build for host system (x86) last
echo -e "\n${YELLOW}7. Building for host system...${NC}"
echo "Compiling for host ($HOST_ARCH)..."
go build -o photonicat2_mini_display .
if [ $? -eq 0 ]; then
    echo -e "  ✓ ${GREEN}Host build succeeded${NC}"
else
    echo -e "  ✗ ${RED}Host build failed${NC}"
    exit 1
fi

# Build tests to verify they compile and create test binary
echo -e "\n${YELLOW}8. Building test binary for target systems...${NC}"

# Build cross-compiled test binaries first if on x86_64
if [ "$HOST_ARCH" = "x86_64" ]; then
    echo "Compiling tests for OpenWRT (aarch64)..."
    env $BUILD_ENV CC=$OPENWRT_CC go test -c -o test_runner_openwrt .
    if [ $? -eq 0 ]; then
        echo -e "  ✓ ${GREEN}OpenWRT test binary created: test_runner_openwrt${NC}"
    else
        echo -e "  ✗ ${RED}OpenWRT test compilation failed${NC}"
    fi

    echo "Compiling tests for Debian (aarch64)..."
    env $BUILD_ENV CC=$DEBIAN_CC go test -c -o test_runner_debian .
    if [ $? -eq 0 ]; then
        echo -e "  ✓ ${GREEN}Debian test binary created: test_runner_debian${NC}"
    else
        echo -e "  ✗ ${RED}Debian test compilation failed${NC}"
    fi
fi

echo "Compiling tests for host system..."
go test -c -o test_runner .
if [ $? -eq 0 ]; then
    echo -e "  ✓ ${GREEN}Host test binary created: test_runner${NC}"
else
    echo -e "  ✗ ${RED}Test compilation failed${NC}"
    echo -e "${YELLOW}Tests may have compilation issues. Run 'go test .' to see details.${NC}"
fi

echo -e "\n${GREEN}✅ Build process completed!${NC}"
echo -e "\nBuilt binaries:"
ls -la photonicat2_mini_display* test_runner* 2>/dev/null || echo "  No binaries found"
echo -e "\n${BLUE}Usage:${NC}"
echo -e "  Run tests: ${YELLOW}./run_tests.sh${NC}"
echo -e "  Run app:   ${YELLOW}./photonicat2_mini_display${NC}"
echo -e "\n${BLUE}For OpenWRT/Debian deployment:${NC}"
echo -e "  Copy appropriate binaries to target system:"
echo -e "  ${YELLOW}scp pcat2_mini_display_openwrt test_runner_openwrt user@device:/${NC}"

exit 0 #exit, no need to copy around for now.

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
