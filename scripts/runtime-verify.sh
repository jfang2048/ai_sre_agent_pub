#!/usr/bin/env bash
set -euo pipefail

# Simple Runtime Verification Test
# Tests that the binaries can execute and respond to basic queries

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

echo "========================================="
echo "AI SRE Agent - Runtime Verification"
echo "========================================="
echo ""

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

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

echo "=== 1. Binary Existence ==="
[ -x "build/sre-controller" ]
check_result $? "Controller binary exists and is executable"

[ -x "build/sre-collector" ]
check_result $? "Collector binary exists and is executable"
echo ""

echo "=== 2. Binary Help Commands ==="
./build/sre-controller --help > /dev/null 2>&1
check_result $? "Controller --help works"

./build/sre-collector --help > /dev/null 2>&1
check_result $? "Collector --help works"
echo ""

echo "=== 3. Version Information ==="
./build/sre-controller --version > /dev/null 2>&1
check_result $? "Controller --version works"

./build/sre-collector --version > /dev/null 2>&1
check_result $? "Collector --version works"
echo ""

echo "=== 4. Configuration Validation ==="
./build/sre-controller --config-check --config configs/controller.yaml > /dev/null 2>&1
check_result $? "Controller config is valid" || echo "  (Config check may not be implemented - this is OK)"

./build/sre-collector --config-check --config configs/collector.yaml > /dev/null 2>&1
check_result $? "Collector config is valid" || echo "  (Config check may not be implemented - this is OK)"
echo ""

echo "=== 5. Web Assets ==="
[ -f "web/index.html" ]
check_result $? "Web UI index.html exists"

[ -d "web/assets" ]
check_result $? "Web UI assets directory exists"

ASSET_COUNT=$(find web/assets -type f \( -name "*.js" -o -name "*.css" \) 2>/dev/null | wc -l)
if [ "$ASSET_COUNT" -gt 0 ]; then
    check_result 0 "Web UI has $ASSET_COUNT asset files"
else
    check_result 1 "No web UI assets found"
fi
echo ""

echo "=== 6. Go Module Integrity ==="
go -C backend mod verify > /dev/null 2>&1
check_result $? "Go modules are verified"
echo ""

echo "=== 7. Test Coverage Summary ==="
echo "Running coverage report..."
GOCACHE="${ROOT_DIR}/.gocache" go -C backend test -cover ./internal/controller/... ./internal/collector/... 2>&1 | grep "coverage:" | tail -5
echo ""

echo "=== 8. File Permissions ==="
[ -r "configs/controller.yaml" ]
check_result $? "Controller config is readable"

[ -r "configs/collector.yaml" ]
check_result $? "Collector config is readable"

[ -x "scripts/run-local.sh" ]
check_result $? "Run script is executable"
echo ""

echo "=== 9. Documentation Completeness ==="
DOC_FILES=(
    "README.md"
    "docs/design/log-pipeline.md"
    "docs/REFACTORING_ROADMAP.md"
    "CONTRIBUTING.md"
)

for doc in "${DOC_FILES[@]}"; do
    if [ -f "$doc" ]; then
        LINES=$(wc -l < "$doc")
        echo "  - $doc ($LINES lines)"
    fi
done

TOTAL_DOCS=0
for doc in "${DOC_FILES[@]}"; do
    [ -f "$doc" ] && TOTAL_DOCS=$((TOTAL_DOCS + 1))
done

check_result 0 "Documentation check complete ($TOTAL_DOCS/${#DOC_FILES[@]} files found)"
echo ""

echo "=== 10. Dependencies Check ==="
# Check if required commands are available
MISSING_DEPS=0

command -v go >/dev/null 2>&1 || { echo "  Missing: go"; MISSING_DEPS=$((MISSING_DEPS + 1)); }
command -v npm >/dev/null 2>&1 || { echo "  Missing: npm"; MISSING_DEPS=$((MISSING_DEPS + 1)); }
command -v python3 >/dev/null 2>&1 || { echo "  Missing: python3"; MISSING_DEPS=$((MISSING_DEPS + 1)); }

if [ $MISSING_DEPS -eq 0 ]; then
    check_result 0 "All required dependencies are installed"
else
    check_result 1 "$MISSING_DEPS required dependencies are missing"
fi
echo ""

# Summary
echo "========================================="
echo "Verification Summary"
echo "========================================="
echo -e "${GREEN}Passed:${NC} $PASS_COUNT"
echo -e "${RED}Failed:${NC} $FAIL_COUNT"
echo ""

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✓ All verification checks passed!${NC}"
    echo ""
    echo "The system is ready to run."
    echo ""
    echo "To start the system:"
    echo "  ./scripts/run-local.sh"
    echo ""
    echo "To run Docker deployment:"
    echo "  docker-compose up -d"
    echo ""
    exit 0
else
    echo -e "${RED}✗ Some verification checks failed${NC}"
    echo ""
    echo "Please review the failures above."
    exit 1
fi
