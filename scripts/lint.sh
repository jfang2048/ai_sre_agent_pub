#!/bin/bash
# Run linters for the SRE Agent

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}Running linters...${NC}"

# Run go fmt
echo -e "${GREEN}Checking code formatting...${NC}"
UNFORMATTED=$(gofmt -l .)
if [ -n "$UNFORMATTED" ]; then
    echo -e "${YELLOW}The following files are not formatted:${NC}"
    echo "$UNFORMATTED"
    gofmt -w $UNFORMATTED
else
    echo -e "${GREEN}All files are properly formatted${NC}"
fi

# Run go vet
echo -e "${GREEN}Running go vet...${NC}"
go vet ./...

# Run golangci-lint if available
if command -v golangci-lint &> /dev/null; then
    echo -e "${GREEN}Running golangci-lint...${NC}"
    golangci-lint run
else
    echo -e "${YELLOW}golangci-lint not found, skipping${NC}"
    echo "Install from: https://golangci-lint.run/usage/install/"
fi

# Run staticcheck if available
if command -v staticcheck &> /dev/null; then
    echo -e "${GREEN}Running staticcheck...${NC}"
    staticcheck ./...
else
    echo -e "${YELLOW}staticcheck not found, skipping${NC}"
    echo "Install with: go install honnef.co/go/tools/cmd/staticcheck@latest"
fi

echo -e "${GREEN}Linting complete!${NC}"
