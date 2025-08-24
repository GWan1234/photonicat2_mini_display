#!/bin/bash

# Unit Test Runner for Photonicat2 Display
# Runs unit tests with different configurations and generates reports

echo "🧪 Photonicat2 Display Unit Test Runner"
echo "======================================="

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to run tests with specific pattern
run_test_category() {
    local category=$1
    local pattern=$2
    echo -e "\n${BLUE}📋 Running $category tests...${NC}"
    if go test -v ./unit_tests/ -run "$pattern" 2>/dev/null; then
        echo -e "${GREEN}✅ $category tests passed${NC}"
    else
        echo -e "${RED}❌ Some $category tests failed${NC}"
    fi
}

# Check if Go is available
if ! command -v go &> /dev/null; then
    echo -e "${RED}❌ Go is not installed or not in PATH${NC}"
    echo -e "${YELLOW}🔄 Switching to binary test runner...${NC}"
    echo -e "This system doesn't have Go installed, but tests can still run using pre-compiled binaries."
    echo ""
    
    # Check if we have the binary runner
    if [ -f "./run_tests_binary.sh" ]; then
        echo -e "${BLUE}🚀 Running binary test runner...${NC}"
        exec ./run_tests_binary.sh
    else
        echo -e "${RED}❌ Binary test runner not found${NC}"
        echo -e "\n${YELLOW}To run tests on this system:${NC}"
        echo -e "1. On a development machine with Go installed:"
        echo -e "   ${BLUE}./compile.sh${NC}  (builds test binaries)"
        echo -e "2. Copy the appropriate test binary to this system:"
        echo -e "   ${BLUE}scp test_runner_openwrt user@device:/${NC}"
        echo -e "3. Run: ${BLUE}./run_tests_binary.sh${NC}"
        exit 1
    fi
fi

echo -e "\n${YELLOW}🏗️  Checking build status...${NC}"
if [ -f "./photonicat2_mini_display" ]; then
    echo -e "${GREEN}✅ Project already built${NC}"
else
    echo -e "${YELLOW}⚠️  Project not built yet. Run ./compile.sh first${NC}"
    echo -e "${BLUE}💡 Building project now...${NC}"
    if go build -o photonicat2_mini_display . &>/dev/null; then
        echo -e "${GREEN}✅ Build successful${NC}"
    else
        echo -e "${RED}❌ Build failed - some tests may not work${NC}"
    fi
fi

# Run all tests
echo -e "\n${BLUE}🧪 Running all unit tests...${NC}"
go test -v . 2>&1 | tee test_results.log

# Run tests by category
echo -e "\n${YELLOW}📊 Running tests by category...${NC}"

run_test_category "Security" "Test.*Valid|Test.*Secure|Test.*Sanitize"
run_test_category "Graphics" "Test.*Draw|Test.*Copy|Test.*Image|Test.*Color"  
run_test_category "Text Processing" "Test.*Text|Test.*Wrap|Test.*CJK"
run_test_category "System" "Test.*Frame|Test.*Config|Test.*Uptime"
run_test_category "Network" "Test.*Speed|Test.*Interface|Test.*Network"

# Show test file organization
echo -e "\n${BLUE}📋 Test File Organization${NC}"
echo "=========================="
echo "📁 Test files in main directory for package access:"
for file in test_*_test.go; do
    if [ -f "$file" ]; then
        count=$(grep -c "^func Test" "$file" 2>/dev/null || echo "0")
        echo "  ✅ $file ($count tests)"
    fi
done

# Generate test coverage if possible
echo -e "\n${YELLOW}📈 Generating test coverage report...${NC}"
if go test -coverprofile=coverage.out . &>/dev/null; then
    if command -v go &> /dev/null; then
        go tool cover -html=coverage.out -o coverage.html &>/dev/null
        echo -e "${GREEN}✅ Coverage report generated: coverage.html${NC}"
    fi
fi

# Summary
echo -e "\n${BLUE}📋 Test Summary${NC}"
echo "==============="
echo "Test results saved to: test_results.log"
if [ -f coverage.html ]; then
    echo "Coverage report saved to: coverage.html"
fi

# Count test results from log
if [ -f test_results.log ]; then
    PASSED=$(grep -c "PASS:" test_results.log || echo "0")
    FAILED=$(grep -c "FAIL:" test_results.log || echo "0")
    echo -e "Tests Passed: ${GREEN}$PASSED${NC}"
    echo -e "Tests Failed: ${RED}$FAILED${NC}"
fi

echo -e "\n${YELLOW}💡 Tip: Run specific tests with:${NC}"
echo "   go test -v -run TestFunctionName"
echo -e "\n${YELLOW}📚 See TESTING.md for detailed testing guide${NC}"