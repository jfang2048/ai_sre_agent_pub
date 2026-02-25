#!/usr/bin/env bash
set -euo pipefail

# Master Test Script
# Runs all tests and verification checks

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

echo "========================================="
echo "AI SRE Agent - Master Test Suite"
echo "========================================="
echo ""

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

TOTAL_PASS=0
TOTAL_FAIL=0

# Test phase tracker
run_phase() {
    local phase_name="$1"
    local phase_command="$2"

    echo -e "${BLUE}=== $phase_name ===${NC}"
    if eval "$phase_command"; then
        echo -e "${GREEN}✓${NC} $phase_name passed"
        TOTAL_PASS=$((TOTAL_PASS + 1))
        echo ""
        return 0
    else
        echo -e "${RED}✗${NC} $phase_name failed"
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
        echo ""
        return 1
    fi
}

# Phase 1: Build
run_phase "1. Build" "make build > /dev/null 2>&1"

# Phase 2: Go Format Check
run_phase "2. Code Format" "make fmt-check > /dev/null 2>&1"

# Phase 3: Go Vet
run_phase "3. Code Vet" "make vet > /dev/null 2>&1"

# Phase 4: Backend Unit Tests
run_phase "4. Backend Tests" "make test > /tmp/test_backend.log 2>&1"

# Phase 5: Frontend Tests
run_phase "5. Frontend Tests" "cd frontend && npm test -- --watch=false --run > /tmp/test_frontend.log 2>&1"

# Phase 6: Integration Tests
echo "=== 6. Integration Tests ==="
echo "Running integration tests..."
# Run both main package tests and integration tests
GOCACHE="${ROOT_DIR}/.gocache" go -C backend test ./internal/collector/... ./internal/controller/... > /tmp/test_integration.log 2>&1
INTEGRITY_RESULT=$?

# Also run integration tests if available
if [ -d "tests/integration" ]; then
    cd tests/integration && GOCACHE="${ROOT_DIR}/.gocache" go test -v . >> /tmp/test_integration.log 2>&1
    INTEGRITY_RESULT=$?
    cd "${ROOT_DIR}"
fi

if [ $INTEGRITY_RESULT -eq 0 ]; then
    echo -e "${GREEN}✓${NC} Integration tests passed"
    TOTAL_PASS=$((TOTAL_PASS + 1))
else
    echo -e "${RED}✗${NC} Integration tests failed"
    echo "See /tmp/test_integration.log for details"
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
fi
echo ""

# Phase 7: Security Audit
run_phase "7. Security Audit" "go -C backend run ./cmd/security-audit -root ${ROOT_DIR} -format markdown > /tmp/security_audit.md 2>&1"

# Phase 8: Lint
run_phase "8. Linting" "make lint > /tmp/test_lint.log 2>&1"

# Phase 9: Comprehensive System Test
echo -e "${BLUE}=== 9. Comprehensive System Test ===${NC}"
if ./scripts/comprehensive-test.sh > /tmp/test_comprehensive.log 2>&1; then
    echo -e "${GREEN}✓${NC} Comprehensive system test passed"
    TOTAL_PASS=$((TOTAL_PASS + 1))
else
    echo -e "${RED}✗${NC} Comprehensive system test failed"
    echo "See /tmp/test_comprehensive.log for details"
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
fi
echo ""

# Phase 10: Runtime Verification
echo -e "${BLUE}=== 10. Runtime Verification ===${NC}"
if ./scripts/runtime-verify.sh > /tmp/test_runtime.log 2>&1; then
    echo -e "${GREEN}✓${NC} Runtime verification passed"
    TOTAL_PASS=$((TOTAL_PASS + 1))
else
    echo -e "${RED}✗${NC} Runtime verification failed"
    echo "See /tmp/test_runtime.log for details"
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
fi
echo ""

# Phase 11: Documentation
echo -e "${BLUE}=== 11. Documentation Check ===${NC}"
DOC_PASS=0
DOC_FAIL=0

# Check key documentation files
if [ -f "README.md" ] && [ $(wc -l < README.md) -gt 500 ]; then
    echo -e "${GREEN}✓${NC} README.md is comprehensive"
    DOC_PASS=$((DOC_PASS + 1))
else
    echo -e "${RED}✗${NC} README.md is missing or too short"
    DOC_FAIL=$((DOC_FAIL + 1))
fi

if [ -f "docs/design/log-pipeline.md" ] && [ $(wc -l < docs/design/log-pipeline.md) -gt 500 ]; then
    echo -e "${GREEN}✓${NC} Log pipeline documentation exists"
    DOC_PASS=$((DOC_PASS + 1))
else
    echo -e "${RED}✗${NC} Log pipeline documentation is missing"
    DOC_FAIL=$((DOC_FAIL + 1))
fi

if [ -f "docs/REFACTORING_ROADMAP.md" ]; then
    echo -e "${GREEN}✓${NC} Refactoring roadmap exists"
    DOC_PASS=$((DOC_PASS + 1))
else
    echo -e "${RED}✗${NC} Refactoring roadmap is missing"
    DOC_FAIL=$((DOC_FAIL + 1))
fi

if [ -f "CONTRIBUTING.md" ]; then
    echo -e "${GREEN}✓${NC} Contributing guide exists"
    DOC_PASS=$((DOC_PASS + 1))
else
    echo -e "${YELLOW}⚠${NC} Contributing guide is missing (recommended)"
fi

if [ $DOC_FAIL -eq 0 ]; then
    TOTAL_PASS=$((TOTAL_PASS + 1))
    echo "Documentation check complete"
else
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
fi
echo ""

# Phase 12: Web Assets
echo -e "${BLUE}=== 12. Web Assets Check ===${NC}"
WEB_PASS=0
WEB_FAIL=0

if [ -f "web/index.html" ]; then
    echo -e "${GREEN}✓${NC} Web UI index.html exists"
    WEB_PASS=$((WEB_PASS + 1))
else
    echo -e "${RED}✗${NC} Web UI index.html missing"
    WEB_FAIL=$((WEB_FAIL + 1))
fi

if [ -d "web/assets" ]; then
    ASSET_COUNT=$(find web/assets -name "*.js" -o -name "*.css" | wc -l)
    if [ "$ASSET_COUNT" -gt 0 ]; then
        echo -e "${GREEN}✓${NC} Web UI assets built ($ASSET_COUNT files)"
        WEB_PASS=$((WEB_PASS + 1))
    else
        echo -e "${RED}✗${NC} Web UI assets directory is empty"
        WEB_FAIL=$((WEB_FAIL + 1))
    fi
else
    echo -e "${RED}✗${NC} Web UI assets directory missing"
    WEB_FAIL=$((WEB_FAIL + 1))
fi

if [ $WEB_FAIL -eq 0 ]; then
    TOTAL_PASS=$((TOTAL_PASS + 1))
    echo "Web assets check complete"
else
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
fi
echo ""

# Final Summary
echo "========================================="
echo "Final Test Summary"
echo "========================================="
echo -e "${GREEN}Passed Phases:${NC} $TOTAL_PASS"
echo -e "${RED}Failed Phases:${NC} $TOTAL_FAIL"
echo ""

if [ $TOTAL_FAIL -eq 0 ]; then
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}✓ ALL TESTS PASSED!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
    echo "The AI SRE Agent system is:"
    echo "  ✓ Building successfully"
    echo "  ✓ Passing all tests"
    echo "  ✓ Properly formatted"
    echo "  ✓ Fully documented"
    echo "  ✓ Ready for deployment"
    echo ""
    echo "To start the system:"
    echo "  ./scripts/run-local.sh"
    echo ""
    echo "To run Docker deployment:"
    echo "  docker-compose up -d"
    echo ""
    echo "To run UI tests (requires running controller):"
    echo "  make test-ui"
    echo ""
    exit 0
else
    echo -e "${RED}=========================================${NC}"
    echo -e "${RED}✗ SOME TESTS FAILED${NC}"
    echo -e "${RED}=========================================${NC}"
    echo ""
    echo "Please review the failures above."
    echo ""
    echo "Test logs are available in /tmp/:"
    echo "  - test_backend.log"
    echo "  - test_frontend.log"
    echo "  - test_integration.log"
    echo "  - test_comprehensive.log"
    echo "  - test_runtime.log"
    echo ""
    exit 1
fi
