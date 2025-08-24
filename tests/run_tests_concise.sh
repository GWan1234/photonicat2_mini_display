#!/bin/bash

# Concise Test Runner for Photonicat2 Display
# Shows only tick/cross per test, detailed logs saved separately

echo "🧪 Photonicat2 Display Test Runner"
echo "=================================="

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

LOG_FILE="test_results.log"

echo -e "\n${BLUE}Running tests...${NC}"
echo "Detailed output saved to: $LOG_FILE"
echo ""

# Run go test and capture output
go test -v 2>&1 > "$LOG_FILE"

# Process the log file to show concise results
PASSED=0
FAILED=0

# Parse test results
current_test=""
while IFS= read -r line; do
    if [[ $line =~ ^===\ RUN[[:space:]]+Test([A-Za-z0-9_]+)$ ]]; then
        TEST_NAME=$(echo "$line" | sed -n 's/=== RUN[[:space:]]*Test\([A-Za-z0-9_]*\)$/\1/p')
        if [[ "$current_test" != "Test${TEST_NAME}" ]]; then
            current_test="Test${TEST_NAME}"
            printf "%-40s " "$current_test:"
        fi
    elif [[ $line =~ ^---\ PASS:.*Test([A-Za-z0-9_]+)\ \( ]]; then
        if [[ -n "$current_test" ]]; then
            echo -e "${GREEN}✓${NC}"
            ((PASSED++))
            current_test=""
        fi
    elif [[ $line =~ ^---\ FAIL:.*Test([A-Za-z0-9_]+)\ \( ]]; then
        if [[ -n "$current_test" ]]; then
            echo -e "${RED}✗${NC}"
            ((FAILED++))
            current_test=""
        fi
    fi
done < "$LOG_FILE"

echo ""
echo -e "${BLUE}📋 Test Summary${NC}"
echo "==============="

TOTAL=$((PASSED + FAILED))
if [ $TOTAL -gt 0 ]; then
    # Calculate success rate using awk for better compatibility
    SUCCESS_RATE=$(awk "BEGIN {printf \"%.1f\", $PASSED * 100 / $TOTAL}")
    echo -e "Tests Passed: ${GREEN}$PASSED${NC}"
    echo -e "Tests Failed: ${RED}$FAILED${NC}"
    echo -e "Success Rate: ${GREEN}${SUCCESS_RATE}%${NC} ($PASSED/$TOTAL)"
else
    echo -e "${RED}No tests found${NC}"
fi

echo -e "Detailed results: ${YELLOW}$LOG_FILE${NC}"

# Show any critical errors briefly
if grep -q "panic:" "$LOG_FILE" 2>/dev/null; then
    echo -e "\n${YELLOW}⚠️  Critical errors detected (see $LOG_FILE for details)${NC}"
fi

echo ""