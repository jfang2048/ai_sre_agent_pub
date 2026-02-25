#!/usr/bin/env bash
set -euo pipefail

# Comprehensive System Test Script
# Tests the entire AI SRE Agent system end-to-end

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

echo "========================================="
echo "AI SRE Agent - Comprehensive System Test"
echo "========================================="
echo ""

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

FAIL_COUNT=0
PASS_COUNT=0

check_result() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $2"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        echo -e "${RED}✗${NC} $2"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

# Test 1: Build Verification
echo "=== 1. Build Verification ==="
make build > /dev/null 2>&1
check_result $? "Build succeeds"
echo ""

# Test 2: Backend Tests
echo "=== 2. Backend Tests ==="
GOCACHE="${ROOT_DIR}/.gocache" go -C backend test ./internal/collector/... ./internal/controller/... > /tmp/backend_test.log 2>&1
check_result $? "Backend tests pass"
if [ $? -ne 0 ]; then
    echo "Backend test output:"
    tail -30 /tmp/backend_test.log
fi
echo ""

# Test 3: Frontend Tests
echo "=== 3. Frontend Tests ==="
cd frontend
npm test -- --watch=false --run > /tmp/frontend_test.log 2>&1
check_result $? "Frontend tests pass"
if [ $? -ne 0 ]; then
    echo "Frontend test output:"
    tail -30 /tmp/frontend_test.log
fi
cd "${ROOT_DIR}"
echo ""

# Test 4: Integration Tests
echo "=== 4. Integration Tests ==="
GOCACHE="${ROOT_DIR}/.gocache" go -C tests/integration test -v . > /tmp/integration_test.log 2>&1
check_result $? "Integration tests pass"
echo ""

# Test 5: Security Audit
echo "=== 5. Security Audit ==="
go -C backend run ./cmd/security-audit -root "${ROOT_DIR}" -format markdown > /tmp/security_audit.md 2>&1
check_result $? "Security audit completes"
echo ""

# Test 6: Code Quality
echo "=== 6. Code Quality Checks ==="
# Format check
unformatted=$(find backend -name '*.go' -not -path '*/vendor/*' -print0 2>/dev/null | xargs -0 gofmt -l 2>/dev/null || true)
if [ -z "$unformatted" ]; then
    check_result 0 "Go format check passes"
else
    check_result 1 "Go format check fails - files need formatting"
    echo "Unformatted files:"
    echo "$unformatted" | head -20
fi

# Vet check
GOCACHE="${ROOT_DIR}/.gocache" go -C backend vet ./... > /dev/null 2>&1
check_result $? "Go vet passes"
echo ""

# Test 7: Configuration Validation
echo "=== 7. Configuration Validation ==="
if [ -f "configs/controller.yaml" ]; then
    check_result 0 "Controller config exists"
else
    check_result 1 "Controller config missing"
fi

if [ -f "configs/collector.yaml" ]; then
    check_result 0 "Collector config exists"
else
    check_result 1 "Collector config missing"
fi
echo ""

# Test 8: Documentation
echo "=== 8. Documentation Checks ==="
if [ -f "README.md" ]; then
    check_result 0 "README.md exists"
    # Check if README has minimum content
    README_LINES=$(wc -l < README.md)
    if [ "$README_LINES" -gt 100 ]; then
        check_result 0 "README.md has substantial content ($README_LINES lines)"
    else
        check_result 1 "README.md too short ($README_LINES lines)"
    fi
else
    check_result 1 "README.md missing"
fi

if [ -f "docs/design/log-pipeline.md" ]; then
    check_result 0 "Log pipeline documentation exists"
else
    check_result 1 "Log pipeline documentation missing"
fi
echo ""

# Test 9: Web Assets
echo "=== 9. Web Assets Check ==="
if [ -f "web/index.html" ]; then
    check_result 0 "Web index.html exists"
    # Check if assets exist
    if [ -d "web/assets" ]; then
        ASSET_COUNT=$(find web/assets -name "*.js" -o -name "*.css" | wc -l)
        if [ "$ASSET_COUNT" -gt 0 ]; then
            check_result 0 "Web assets built ($ASSET_COUNT files)"
        else
            check_result 1 "Web assets missing"
        fi
    else
        check_result 1 "Web assets directory missing"
    fi
else
    check_result 1 "Web index.html missing - run frontend build"
fi
echo ""

# Test 10: Proto Files
echo "=== 10. Proto Files Check ==="
if [ -d "proto" ]; then
    PROTO_COUNT=$(find proto -name "*.proto" | wc -l)
    if [ "$PROTO_COUNT" -gt 0 ]; then
        check_result 0 "Proto files exist ($PROTO_COUNT files)"
    else
        check_result 1 "No proto files found"
    fi
else
    check_result 1 "Proto directory missing"
fi
echo ""

# Test 11: Test Coverage
echo "=== 11. Test Coverage ==="
GOCACHE="${ROOT_DIR}/.gocache" go -C backend test -cover ./internal/controller/... ./internal/collector/... 2>&1 | grep -o 'coverage: [0-9.]*%' | sort -t '%' -k2 -n | tail -1 > /tmp/coverage.txt
if [ -s /tmp/coverage.txt ]; then
    COVERAGE=$(cat /tmp/coverage.txt)
    echo "Coverage: $COVERAGE"
    COVERAGE_NUM=$(echo "$COVERAGE" | grep -o '[0-9.]*' | head -1)
    if [ "$(echo "$COVERAGE_NUM > 50" | bc)" = "1" ]; then
        check_result 0 "Test coverage > 50%"
    else
        check_result 1 "Test coverage < 50%: $COVERAGE"
    fi
else
    check_result 1 "Could not determine test coverage"
fi
echo ""

# Summary
echo "========================================="
echo "Test Summary"
echo "========================================="
echo -e "${GREEN}Passed:${NC} $PASS_COUNT"
echo -e "${RED}Failed:${NC} $FAIL_COUNT"
echo ""

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✓ All tests passed!${NC}"
    echo ""
    echo "The system is ready to run:"
    echo "  ./scripts/run-local.sh"
    echo ""
    exit 0
else
    echo -e "${RED}✗ Some tests failed${NC}"
    echo ""
    echo "Please review the failures above."
    echo ""
    exit 1
fi
