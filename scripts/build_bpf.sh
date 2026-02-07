#!/bin/bash
# Build eBPF programs for the SRE Agent

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
BPF_DIR="$PROJECT_ROOT/sdk/bpf"
OUTPUT_DIR="$PROJECT_ROOT/sdk/build/bpf"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Building eBPF programs...${NC}"

# Check if clang is installed
if ! command -v clang &> /dev/null; then
    echo -e "${RED}Error: clang is not installed${NC}"
    echo "Please install clang: apt-get install clang (Ubuntu/Debian) or yum install clang (RHEL/CentOS)"
    exit 1
fi

# Check kernel headers
if [ ! -d "/lib/modules/$(uname -r)/build" ]; then
    echo -e "${YELLOW}Warning: Kernel headers not found for $(uname -r)${NC}"
    echo "eBPF programs may not compile correctly. Install kernel headers:"
    echo "  apt-get install linux-headers-$(uname -r) (Ubuntu/Debian)"
    echo "  yum install kernel-devel (RHEL/CentOS)"
fi

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Kernel version
KERNEL_VERSION=$(uname -r | cut -d'.' -f1-2)
echo "Kernel version: $KERNEL_VERSION"

# Common clang flags for eBPF
CLANG_FLAGS="-O2 -g -target bpf -D__TARGET_ARCH_x86"
CLANG_FLAGS="$CLANG_FLAGS -Wall -Wextra"

# Include paths
INCLUDE_PATHS="-I/usr/include/bpf -I/usr/include/$(uname -m)-linux-gnu"

# Build each .bpf.c file
for bpf_file in "$BPF_DIR"/*.bpf.c; do
    if [ -f "$bpf_file" ]; then
        filename=$(basename "$bpf_file" .bpf.c)
        echo -e "${GREEN}Building: $filename${NC}"

        clang $CLANG_FLAGS $INCLUDE_PATHS -c "$bpf_file" -o "$OUTPUT_DIR/${filename}.o"

        if [ $? -eq 0 ]; then
            echo -e "${GREEN}✓ Built: ${filename}.o${NC}"
        else
            echo -e "${RED}✗ Failed: ${filename}${NC}"
            exit 1
        fi
    fi
done

# Generate skeleton files if bpftool is available
if command -v bpftool &> /dev/null; then
    echo -e "${GREEN}Generating skeleton files...${NC}"
    for bpf_file in "$BPF_DIR"/*.bpf.c; do
        if [ -f "$bpf_file" ]; then
            filename=$(basename "$bpf_file" .bpf.c)
            bpftool bpf gen skeleton "$OUTPUT_DIR/${filename}.o" name "${filename}" > "$OUTPUT_DIR/${filename}_skel.h"
        fi
    done
else
    echo -e "${YELLOW}bpftool not found, skipping skeleton generation${NC}"
fi

echo -e "${GREEN}eBPF build complete!${NC}"
