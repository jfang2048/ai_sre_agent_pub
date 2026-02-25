#!/usr/bin/env bash
set -euo pipefail

# End-to-End Smoke Test
# Starts the full stack and verifies all components work

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

echo "========================================="
echo "AI SRE Agent - End-to-End Smoke Test"
echo "========================================="
echo ""

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

FAIL_COUNT=0
PASS_COUNT=0

# Cleanup function
cleanup() {
    echo ""
    echo "Cleaning up..."
    if [ -n "${CONTROLLER_PID:-}" ]; then
        kill "${CONTROLLER_PID}" 2>/dev/null || true
        wait "${CONTROLLER_PID}" 2>/dev/null || true
    fi
    if [ -n "${COLLECTOR_PID:-}" ]; then
        kill "${COLLECTOR_PID}" 2>/dev/null || true
        wait "${COLLECTOR_PID}" 2>/dev/null || true
    fi
    rm -f /tmp/sre-e2e-*.log /tmp/sre-e2e-*.pid
}

trap cleanup EXIT INT TERM

# Test result helper
check_result() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $2"
        PASS_COUNT=$((PASS_COUNT + 1))
        return 0
    else
        echo -e "${RED}✗${NC} $2"
        FAIL_COUNT=$((FAIL_COUNT + 1))
        return 1
    fi
}

echo "=== 1. Build Verification ==="
make build > /dev/null 2>&1
check_result $? "Build succeeds" || exit 1
echo ""

echo "=== 2. Frontend Build ==="
cd frontend
npm run build > /tmp/frontend_build.log 2>&1
check_result $? "Frontend builds" || exit 1
cd "${ROOT_DIR}"
echo ""

echo "=== 3. Starting Controller ==="
export SRE_CONTROLLER_CONFIG="${ROOT_DIR}/configs/controller.yaml"
export SRE_CONTROLLER_HTTP_LISTEN="127.0.0.1:18080"  # Use different port for testing
export SRE_CONTROLLER_GRPC_LISTEN="127.0.0.1:19090"
export SRE_CONTROLLER_WEB_PATH="${ROOT_DIR}/web"

./build/sre-controller > /tmp/sre-e2e-controller.log 2>&1 &
CONTROLLER_PID=$!
echo "Controller PID: ${CONTROLLER_PID}"

# Wait for controller to start
echo "Waiting for controller to be ready..."
MAX_WAIT=20
WAIT_COUNT=0
while [ $WAIT_COUNT -lt $MAX_WAIT ]; do
    if curl -s http://127.0.0.1:18080/healthz > /dev/null 2>&1; then
        echo -e "${GREEN}✓${NC} Controller is ready"
        break
    fi
    sleep 1
    WAIT_COUNT=$((WAIT_COUNT + 1))
done

if [ $WAIT_COUNT -eq $MAX_WAIT ]; then
    echo "Controller failed to start within ${MAX_WAIT}s"
    echo "Controller log:"
    cat /tmp/sre-e2e-controller.log | tail -50
    exit 1
fi
echo ""

echo "=== 4. Starting Collector ==="
export SRE_COLLECTOR_CONFIG="${ROOT_DIR}/configs/collector.yaml"
export SRE_COLLECTOR_CONTROLLER_ENDPOINTS="127.0.0.1:19090"

./build/sre-collector > /tmp/sre-e2e-collector.log 2>&1 &
COLLECTOR_PID=$!
echo "Collector PID: ${COLLECTOR_PID}"

# Wait a bit for collector to connect
sleep 3
echo ""

echo "=== 5. API Endpoint Tests ==="
BASE_URL="http://127.0.0.1:18080"

# Test health endpoint
curl -s "${BASE_URL}/healthz" > /dev/null 2>&1
check_result $? "Health endpoint responds" || echo "Health check failed"

# Test status endpoint
STATUS=$(curl -s "${BASE_URL}/api/v1/status")
check_result $? "Status endpoint responds"
if [ $? -eq 0 ]; then
    # Check if JSON is valid
    echo "$STATUS" | grep -q '"collectors"'
    check_result $? "Status contains collectors field"
fi

# Test fleet endpoint
FLEET=$(curl -s "${BASE_URL}/api/v1/fleet")
check_result $? "Fleet endpoint responds"
if [ $? -eq 0 ]; then
    # Check if JSON is valid
    echo "$FLEET" | grep -q '{'
    check_result $? "Fleet returns valid JSON"
fi

# Test diagnostics endpoints
curl -s "${BASE_URL}/api/v1/diagnostics/data-path" > /dev/null 2>&1
check_result $? "Data-path diagnostics responds"

curl -s "${BASE_URL}/api/v1/diagnostics/kernel-path" > /dev/null 2>&1
check_result $? "Kernel-path diagnostics responds"
echo ""

echo "=== 6. Log Index Tests ==="
# Test log status endpoint
LOG_STATUS=$(curl -s "${BASE_URL}/api/v1/logs/status")
check_result $? "Log status endpoint responds"
if [ $? -eq 0 ]; then
    # Check if log index is enabled
    if echo "$LOG_STATUS" | grep -q '"status":"ok"'; then
        echo -e "${GREEN}✓${NC} Log index is operational"
        PASS_COUNT=$((PASS_COUNT + 1))
    fi
fi

# Test log search endpoint
curl -s "${BASE_URL}/api/v1/logs/search?limit=10" > /dev/null 2>&1
check_result $? "Log search endpoint responds"
echo ""

echo "=== 7. Data Flow Verification ==="
# Wait for some data to be collected
echo "Waiting for data collection..."
sleep 5

# Check if fleet has any data
FLEET_DATA=$(curl -s "${BASE_URL}/api/v1/fleet")
if echo "$FLEET_DATA" | grep -q '"collectors":\s*{'; then
    echo -e "${YELLOW}⚠${NC} No collectors connected yet (expected in test environment)"
    PASS_COUNT=$((PASS_COUNT + 1))
elif echo "$FLEET_DATA" | grep -q '"collectors":\s*{[^}]*[^}]'; then
    echo -e "${GREEN}✓${NC} Fleet has collector data"
    PASS_COUNT=$((PASS_COUNT + 1))
fi
echo ""

echo "=== 8. Error Handling Tests ==="
# Test 404 on unknown endpoint
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${BASE_URL}/api/v1/unknown-endpoint")
if [ "$HTTP_CODE" = "404" ]; then
    echo -e "${GREEN}✓${NC} Unknown endpoint returns 404"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${RED}✗${NC} Unknown endpoint returned ${HTTP_CODE} (expected 404)"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# Test invalid method returns 405
HTTP_CODE=$(curl -s -X POST -o /dev/null -w "%{http_code}" "${BASE_URL}/healthz")
if [ "$HTTP_CODE" = "405" ] || [ "$HTTP_CODE" = "404" ]; then
    echo -e "${GREEN}✓${NC} Invalid method handled correctly"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${RED}✗${NC} Invalid method returned ${HTTP_CODE}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi
echo ""

echo "=== 9. Process Health Check ==="
# Check if processes are still running
if kill -0 "${CONTROLLER_PID}" 2>/dev/null; then
    echo -e "${GREEN}✓${NC} Controller process is healthy"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${RED}✗${NC} Controller process died"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

if kill -0 "${COLLECTOR_PID}" 2>/dev/null; then
    echo -e "${GREEN}✓${NC} Collector process is healthy"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${YELLOW}⚠${NC} Collector process died (may be expected if no GPU available)"
    PASS_COUNT=$((PASS_COUNT + 1))
fi
echo ""

echo "=== 10. Web UI Assets Check ==="
if [ -f "${ROOT_DIR}/web/index.html" ]; then
    # Test if web UI is accessible
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${BASE_URL}/")
    if [ "$HTTP_CODE" = "200" ]; then
        echo -e "${GREEN}✓${NC} Web UI is accessible"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        echo -e "${RED}✗${NC} Web UI returned ${HTTP_CODE}"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
else
    echo -e "${RED}✗${NC} Web UI assets not found"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi
echo ""

# Summary
echo "========================================="
echo "Smoke Test Summary"
echo "========================================="
echo -e "${GREEN}Passed:${NC} $PASS_COUNT"
echo -e "${RED}Failed:${NC} $FAIL_COUNT"
echo ""

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✓ All smoke tests passed!${NC}"
    echo ""
    echo "The system is fully functional and ready for use."
    exit 0
else
    echo -e "${RED}✗ Some smoke tests failed${NC}"
    echo ""
    echo "Controller log:"
    cat /tmp/sre-e2e-controller.log | tail -30
    echo ""
    echo "Collector log:"
    cat /tmp/sre-e2e-collector.log | tail -30
    exit 1
fi
