#!/usr/bin/env bash
# Generate protobuf files for the SRE Agent

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
PROTO_API_DIR="$PROJECT_ROOT/proto/api/v1"
OUTPUT_DIR="$PROJECT_ROOT/backend/pkg/proto"
TELEMETRY_PROTO_DIR="$PROJECT_ROOT/proto"
TELEMETRY_OUTPUT_DIR="$PROJECT_ROOT/backend/pkg"
CPP_PROBE_OUTPUT_DIR="$PROJECT_ROOT/cpp/probe_core/generated"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Generating protobuf files...${NC}"

# Ensure GOPATH/bin is available for protoc plugins.
GOBIN="$(go env GOPATH)/bin"
export PATH="$PATH:$GOBIN"

# Check if protoc is installed
if ! command -v protoc &> /dev/null; then
    echo -e "${RED}Error: protoc is not installed${NC}"
    echo "Please install protoc: https://grpc.io/docs/protoc-installation/"
    exit 1
fi

# Check if protoc-gen-go is installed
if ! command -v protoc-gen-go &> /dev/null; then
    echo -e "${RED}Error: protoc-gen-go is not installed${NC}"
    echo "Please install protoc-gen-go (ensure GOPATH/bin is in PATH)."
    exit 1
fi

# Check if protoc-gen-go-grpc is installed
if ! command -v protoc-gen-go-grpc &> /dev/null; then
    echo -e "${RED}Error: protoc-gen-go-grpc is not installed${NC}"
    echo "Please install protoc-gen-go-grpc (ensure GOPATH/bin is in PATH)."
    exit 1
fi

# Create output directory
mkdir -p "$OUTPUT_DIR"
mkdir -p "$TELEMETRY_OUTPUT_DIR"
mkdir -p "$CPP_PROBE_OUTPUT_DIR"

# Generate Go code from proto files
for proto_file in "$PROTO_API_DIR"/*.proto; do
    if [ -f "$proto_file" ]; then
        echo -e "${GREEN}Generating: $(basename "$proto_file")${NC}"
        protoc \
            --proto_path="$TELEMETRY_PROTO_DIR" \
            --go_out="$OUTPUT_DIR" \
            --go_opt=paths=source_relative \
            --go-grpc_out="$OUTPUT_DIR" \
            --go-grpc_opt=paths=source_relative \
            "$proto_file"
    fi
done

# Fix go_package subdir outputs that need nested folders.
if [ -f "$OUTPUT_DIR/ai.pb.go" ] || [ -f "$OUTPUT_DIR/ai_grpc.pb.go" ]; then
    mkdir -p "$OUTPUT_DIR/ai"
    [ -f "$OUTPUT_DIR/ai.pb.go" ] && mv "$OUTPUT_DIR/ai.pb.go" "$OUTPUT_DIR/ai/ai.pb.go"
    [ -f "$OUTPUT_DIR/ai_grpc.pb.go" ] && mv "$OUTPUT_DIR/ai_grpc.pb.go" "$OUTPUT_DIR/ai/ai_grpc.pb.go"
fi

if [ -f "$OUTPUT_DIR/metrics.pb.go" ] || [ -f "$OUTPUT_DIR/metrics_grpc.pb.go" ]; then
    mkdir -p "$OUTPUT_DIR/metrics"
    [ -f "$OUTPUT_DIR/metrics.pb.go" ] && mv "$OUTPUT_DIR/metrics.pb.go" "$OUTPUT_DIR/metrics/metrics.pb.go"
    [ -f "$OUTPUT_DIR/metrics_grpc.pb.go" ] && mv "$OUTPUT_DIR/metrics_grpc.pb.go" "$OUTPUT_DIR/metrics/metrics_grpc.pb.go"
fi

# Generate telemetry v1 protos
find "$TELEMETRY_PROTO_DIR" -name "*.proto" | while read -r proto_file; do
    echo -e "${GREEN}Generating: ${proto_file#$TELEMETRY_PROTO_DIR/}${NC}"
    protoc \
        --proto_path="$TELEMETRY_PROTO_DIR" \
        --go_out="$TELEMETRY_OUTPUT_DIR" \
        --go_opt=paths=source_relative \
        --go-grpc_out="$TELEMETRY_OUTPUT_DIR" \
        --go-grpc_opt=paths=source_relative \
        "$proto_file"
done

# Generate C++ IPC proto for probe-core binary
if [ -f "$TELEMETRY_PROTO_DIR/probeipc/v1/probeipc.proto" ]; then
    echo -e "${GREEN}Generating C++: probeipc/v1/probeipc.proto${NC}"
    protoc \
        --proto_path="$TELEMETRY_PROTO_DIR" \
        --cpp_out="$CPP_PROBE_OUTPUT_DIR" \
        "$TELEMETRY_PROTO_DIR/probeipc/v1/probeipc.proto"
    # Relax strict equality check so newer protobuf runtimes can load older gencode.
    PROBE_HEADER="$CPP_PROBE_OUTPUT_DIR/probeipc/v1/probeipc.pb.h"
    if [ -f "$PROBE_HEADER" ]; then
        sed -i 's/^#if PROTOBUF_VERSION != /#if PROTOBUF_VERSION < /' "$PROBE_HEADER"
    fi
fi

echo -e "${GREEN}Protobuf generation complete!${NC}"
