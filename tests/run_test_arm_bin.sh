#!/bin/bash

# Binary Test Runner for Photonicat2 Display (No Go Required)
# Runs pre-compiled test binaries on target systems without Go installed

echo "🧪 Photonicat2 Display Binary Test Runner"
echo "=========================================="

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Detect system architecture
ARCH=$(uname -m)
echo "System architecture: $ARCH"

# Function to run test binary with specific pattern
run_binary_test_category() {
    local category=$1
    local pattern=$2
    echo -e "\n${BLUE}📋 Running $category tests...${NC}"
    if [ -f "$TEST_BINARY" ]; then
        if $TEST_BINARY -test.run "$pattern" -test.v 2>/dev/null; then
            echo -e "${GREEN}✅ $category tests completed${NC}"
        else
            echo -e "${RED}❌ Some $category tests failed or no tests matched${NC}"
        fi
    else
        echo -e "${RED}❌ Test binary not found: $TEST_BINARY${NC}"
        return 1
    fi
}

# Determine which test binary to use
TEST_BINARY=""
if [ -f "./test_runner_openwrt" ]; then
    TEST_BINARY="./test_runner_openwrt"
    echo -e "${BLUE}Using OpenWRT test binary${NC}"
elif [ -f "./test_runner_debian" ]; then
    TEST_BINARY="./test_runner_debian"
    echo -e "${BLUE}Using Debian test binary${NC}"
elif [ -f "./test_runner" ]; then
    TEST_BINARY="./test_runner"
    echo -e "${BLUE}Using host test binary${NC}"
else
    echo -e "${RED}❌ No test binary found!${NC}"
    echo -e "\n${YELLOW}Available test binaries should be:${NC}"
    echo -e "  - test_runner_openwrt (for OpenWRT aarch64)"
    echo -e "  - test_runner_debian (for Debian aarch64)"
    echo -e "  - test_runner (for host system)"
    echo -e "\n${YELLOW}💡 Build test binaries with:${NC}"
    echo -e "  On development machine: ${BLUE}./compile.sh${NC}"
    echo -e "  Then copy appropriate binary to this system"
    exit 1
fi

# Check if test binary is executable
if [ ! -x "$TEST_BINARY" ]; then
    echo -e "${YELLOW}Making test binary executable...${NC}"
    chmod +x "$TEST_BINARY"
fi

echo -e "\n${BLUE}🧪 Running all unit tests...${NC}"
echo "Test binary: $TEST_BINARY"

# Run all tests and capture output, process for concise display
echo "Running tests..."
$TEST_BINARY -test.v 2>&1 | tee test_results.log | while read line; do
    if [[ $line =~ ^===\ RUN.*Test([A-Za-z0-9_]+) ]]; then
        TEST_NAME=$(echo "$line" | sed -n 's/=== RUN[[:space:]]*Test\([A-Za-z0-9_]*\).*/\1/p')
        printf "%-40s " "Test${TEST_NAME}:"
    elif [[ $line =~ ^---\ PASS:.*Test([A-Za-z0-9_]+) ]]; then
        echo -e "${GREEN}✓${NC}"
    elif [[ $line =~ ^---\ FAIL:.*Test([A-Za-z0-9_]+) ]]; then
        echo -e "${RED}✗${NC}"
    fi
done

echo -e "\n${GREEN}✅ Test execution completed${NC}"


# Show system information
echo -e "\n${BLUE}📋 System Information${NC}"
echo "======================"
echo "Architecture: $(uname -m)"
echo "OS: $(uname -s)"
echo "Kernel: $(uname -r)"
echo "Test binary: $TEST_BINARY"
if [ -f "./photonicat2_mini_display" ]; then
    echo -e "Main binary: ${GREEN}✅ photonicat2_mini_display found${NC}"
elif [ -f "./pcat2_mini_display_openwrt" ]; then
    echo -e "Main binary: ${GREEN}✅ pcat2_mini_display_openwrt found${NC}"
elif [ -f "./pcat2_mini_display_debian" ]; then
    echo -e "Main binary: ${GREEN}✅ pcat2_mini_display_debian found${NC}"
else
    echo -e "Main binary: ${RED}❌ No main binary found${NC}"
fi

# Summary
echo -e "\n${BLUE}📋 Test Summary${NC}"
echo "==============="
echo "Test results saved to: test_results.log"

# Count test results from log if it exists
if [ -f test_results.log ]; then
    PASSED=$(grep -c "PASS:" test_results.log 2>/dev/null || echo "0")
    FAILED=$(grep -c "FAIL:" test_results.log 2>/dev/null || echo "0")
    TOTAL=$((PASSED + FAILED))
    
    if [ $TOTAL -gt 0 ]; then
        SUCCESS_RATE=$(echo "scale=1; $PASSED * 100 / $TOTAL" | bc 2>/dev/null || echo "0")
        echo -e "Tests Passed: ${GREEN}$PASSED${NC}"
        echo -e "Tests Failed: ${RED}$FAILED${NC}"
        echo -e "Success Rate: ${GREEN}${SUCCESS_RATE}%${NC} ($PASSED/$TOTAL)"
    else
        echo -e "No tests found in results"
    fi
    
    # Show any panic or error messages briefly
    if grep -q "panic\|fatal" test_results.log 2>/dev/null; then
        echo -e "\n${YELLOW}⚠️  Critical errors detected (details in test_results.log)${NC}"
    fi
fi

echo -e "\n${YELLOW}💡 Usage Notes:${NC}"
echo "  - This runner works without Go installed"
echo "  - Test binaries are cross-compiled on development machine"
echo "  - Some tests may fail due to missing system resources (expected)"
echo "  - Check test_results.log for detailed output"

echo -e "\n${YELLOW}📚 For development with Go installed, use:${NC}"
echo -e "  ${BLUE}./run_tests.sh${NC} (requires Go environment)"