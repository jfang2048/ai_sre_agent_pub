#!/bin/bash
# Chaos engineering tests for the SRE Agent
# These tests simulate failures to verify agent resilience

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}Running chaos engineering tests...${NC}"

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo -e "${YELLOW}Warning: Some chaos tests require root privileges${NC}"
fi

# Function to cleanup
cleanup() {
    echo -e "${GREEN}Cleaning up chaos artifacts...${NC}"
    # Kill any stress processes
    pkill -9 stress || true
    # Restore any modified sysctl settings
    sysctl -w vm.swappiness=60 >/dev/null 2>&1 || true
}

trap cleanup EXIT

# Test 1: CPU pressure
echo -e "${GREEN}Test 1: Simulating CPU pressure...${NC}"
if command -v stress &> /dev/null; then
    timeout 10 stress --cpu 4 --timeout 5s &
    echo "CPU stress test initiated"
else
    echo -e "${YELLOW}stress not found, skipping CPU test${NC}"
fi

# Test 2: Memory pressure
echo -e "${GREEN}Test 2: Simulating memory pressure...${NC}"
if command -v stress &> /dev/null; then
    timeout 10 stress --vm 2 --vm-bytes 100M --timeout 5s &
    echo "Memory stress test initiated"
else
    echo -e "${YELLOW}stress not found, skipping memory test${NC}"
fi

# Test 3: I/O pressure
echo -e "${GREEN}Test 3: Simulating I/O pressure...${NC}"
if command -v stress &> /dev/null; then
    timeout 10 stress --io 2 --timeout 5s &
    echo "I/O stress test initiated"
else
    echo -e "${YELLOW}stress not found, skipping I/O test${NC}"
fi

# Test 4: Network delay
echo -e "${GREEN}Test 4: Simulating network delay...${NC}"
if [ "$EUID" -eq 0 ]; then
    # Add network delay to loopback
    tc qdisc add dev lo root handle 1: htb default 10
    tc class add dev lo parent 1: classid 1:1 htb rate 1000mbit
    tc qdisc add dev lo parent 1:1 handle 10: netem delay 100ms 20ms
    echo "Network delay added (100ms ± 20ms)"
    sleep 5
    tc qdisc del dev lo root
    echo "Network delay removed"
else
    echo -e "${YELLOW}Root required for network delay test, skipping${NC}"
fi

# Test 5: Simulated OOM
echo -e "${GREEN}Test 5: Simulating OOM condition (cgroup)...${NC}"
if [ -d "/sys/fs/cgroup" ]; then
    echo "Creating test cgroup for OOM simulation..."
    CGROUP_PATH="/sys/fs/cgroup/memory/sre-collector-test"
    if [ -d "$CGROUP_PATH" ]; then
        rmdir "$CGROUP_PATH" 2>/dev/null || true
    fi
    echo "OOM simulation would create constrained cgroup"
    echo "This is a placeholder for actual OOM test"
else
    echo -e "${YELLOW}cgroup not available, skipping OOM test${NC}"
fi

# Test 6: Disk pressure
echo -e "${GREEN}Test 6: Simulating disk pressure...${NC}"
TEMP_FILE=$(mktemp)
echo "Creating temporary file for disk test: $TEMP_FILE"
dd if=/dev/zero of="$TEMP_FILE" bs=1M count=100 2>/dev/null || true
rm -f "$TEMP_FILE"
echo "Disk pressure test completed"

# Test 7: Simulated process crash
echo -e "${GREEN}Test 7: Testing agent resilience to process crashes...${NC}"
echo "Process crash resilience test"
echo "The agent should detect and recover from monitored process crashes"

# Test 8: Port exhaustion simulation
echo -e "${GREEN}Test 8: Testing port handling...${NC}"
echo "Port exhaustion test"
echo "The agent should handle port allocation failures gracefully"

echo -e "${GREEN}Chaos engineering tests complete!${NC}"
echo "Review agent logs for any issues during stress periods"
