# SRE Agent Makefile
# ====================
#
# This Makefile builds the two-component SRE monitoring system:
# - sre-collector: Push-first telemetry collector (runs on monitored hosts)
# - sre-controller: Control-side aggregator and API server
#
# Terminology:
#   - Collector: The push-first telemetry collector on each monitored host
#   - Controller: The central aggregation server
#   - Agent: The overall AI SRE system (not the controlled-side collector)

.PHONY: all proto proto-check build build-collector build-controller build-probe-core \
        run run-collector run-controller run-both run-multinode \
        test test-controller test-all test-cover test-race test-stability test-screenshot-tools bench \
        fmt fmt-check vet lint ci verify-readme-screenshots capture-keys \
        frontend-install frontend-dev frontend-build frontend-lint \
        docker-up docker-down docker-logs docker-build docker-run-stack docker-stop-stack smoke \
        clean install install-collector-service install-controller-service \
        docker-controller docker \
        security-scan security-audit \
        help

# Build configuration
VERSION ?= v0.1
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)"

# Keep build outputs inside the repo so `make clean` works and paths are consistent.
# Use an absolute path because we build from `backend/` (via `go -C backend ...`).
BUILD_DIR := $(CURDIR)/build

# Some environments end up with an unreadable default GOCACHE (e.g. due to prior sudo runs).
# Pin it to a repo-local cache to keep `make fmt/test/build` reliable.
GO_CACHE ?= $(CURDIR)/.gocache
GO := GOCACHE=$(GO_CACHE) go

# Default target
all: build

# =============================================================================
# BUILD
# =============================================================================

## proto: Generate protobuf code
proto:
	@./scripts/gen_proto.sh

## proto-check: Ensure generated protobuf outputs exist (no protoc required)
proto-check:
	@missing=""; \
	for f in backend/pkg/proto/action.pb.go backend/pkg/proto/common.pb.go backend/pkg/telemetry/v1/telemetry.pb.go; do \
		if [ ! -f "$$f" ]; then missing="$$missing $$f"; fi; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "Missing generated protobuf files:$$missing"; \
		echo "Run: make proto (requires protoc + protoc-gen-go + protoc-gen-go-grpc)"; \
		exit 1; \
	fi

## build: Build collector and controller binaries
build: proto-check build-collector build-controller
	@echo "✓ Build complete"
	@echo "  Collector:  build/sre-collector"
	@echo "  Controller: build/sre-controller"
	@echo "  (Optional) C++ probe-core: build/sre-probe-core"
	@echo "  (Optional) C++ proc-metrics: build/proc-metrics"

## build-collector: Build the push-first collector binary
build-collector:
	@echo "Building collector (push-first)..."
	@mkdir -p $(BUILD_DIR) $(GO_CACHE)
	@$(GO) -C backend build $(LDFLAGS) -o $(BUILD_DIR)/sre-collector ./cmd/collector
	@echo "  → build/sre-collector"

## build-controller: Build the controller binary (control-side aggregator)
build-controller:
	@echo "Building controller (control node)..."
	@mkdir -p $(BUILD_DIR) $(GO_CACHE)
	@$(GO) -C backend build $(LDFLAGS) -o $(BUILD_DIR)/sre-controller ./cmd/controller
	@echo "  → build/sre-controller"

## build-proc-metrics: Build optional C++ /proc metrics helper
build-proc-metrics:
	@mkdir -p $(BUILD_DIR)
	@g++ -std=c++17 -O2 -Wall -Wextra -o $(BUILD_DIR)/proc-metrics cpp/proc_metrics/main.cpp

## build-probe-core: Build optional C++ probe-core IPC binary
build-probe-core:
	@mkdir -p $(BUILD_DIR)
	@if command -v pkg-config >/dev/null 2>&1 && pkg-config --exists protobuf zlib; then \
		set -e; \
		g++ -std=c++20 -O2 -Wall -Wextra -pthread \
			$$(pkg-config --cflags protobuf zlib) \
			-Icpp/probe_core/generated \
			-o $(BUILD_DIR)/sre-probe-core \
			cpp/probe_core/main.cpp cpp/probe_core/generated/probeipc/v1/probeipc.pb.cc \
			$$(pkg-config --libs protobuf zlib); \
		echo "  → build/sre-probe-core"; \
	else \
		echo "pkg-config protobuf/zlib not found; skipping probe-core build"; \
		exit 1; \
	fi

# =============================================================================
# RUN (Development)
# =============================================================================

## run: Run collector + controller (for development)
run: run-both

## run-collector: Run the push-first collector (default)
run-collector: build-collector
	SRE_COLLECTOR_CONFIG=./configs/collector.yaml ./build/sre-collector

## run-controller: Run the controller on :8080
run-controller: build-controller
	SRE_CONTROLLER_CONFIG=./configs/controller.yaml ./build/sre-controller

## run-both: Run collector + controller
run-both: build
	@./scripts/run-local.sh

## run-multinode: Run controller + multiple local collectors
run-multinode: build
	@./scripts/run-local-multinode.sh

# =============================================================================
# TEST
# =============================================================================

## test: Run all tests
test:
	@echo "Running tests..."
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -v ./internal/probe/... ./internal/controller/...

## test-controller: Run controller tests only
test-controller:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -v ./internal/controller/...

## test-all: Run all backend tests (and compile all packages)
test-all:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test ./...

## test-cover: Run tests with coverage
test-cover:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -cover ./internal/probe/... ./internal/controller/...

## test-race: Run key packages under the race detector
test-race:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -race ./internal/probe ./internal/controller/...

## test-stability: Run test-first stability workflow (backend + integration + e2e + python + UI)
test-stability:
	@mkdir -p $(GO_CACHE)
	@echo "Running backend unit/integration tests..."
	@$(GO) -C backend test ./... -count=1
	@echo "Running external integration pipeline tests..."
	@$(GO) -C tests/integration test -v .
	@echo "Running external-stack E2E tests (will skip if preconditions are unmet)..."
	@$(GO) -C tests/e2e test -v -tags=e2e .
	@echo "Running python analysis/runtime tests..."
	@python3 -m unittest discover -s tests/python -p 'test_*.py'
	@echo "Running frontend data-flow tests..."
	@cd frontend && npm test -- --watch=false

## test-screenshot-tools: Validate screenshot capture script CLI/config guards
test-screenshot-tools:
	@./scripts/test/screenshot_tooling.sh

## bench: Run benchmarks
bench:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -bench=. ./internal/probe/... ./internal/controller/...

# =============================================================================
# QUALITY
# =============================================================================

## fmt: Format code
fmt:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend fmt ./...

## fmt-check: Verify gofmt is clean (CI-friendly)
fmt-check:
	@unformatted="$$(find backend -name '*.go' -not -path '*/vendor/*' -print0 | xargs -0 gofmt -l)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

## vet: Run go vet
vet:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend vet ./...

## lint: Run linters (requires golangci-lint)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		cd backend && golangci-lint run; \
	else \
		echo "golangci-lint not found; falling back to 'make vet'."; \
		$(MAKE) vet; \
	fi

## verify-readme-screenshots: Ensure README screenshot paths exist and are valid PNGs
verify-readme-screenshots:
	@./scripts/verify_readme_screenshots.sh

## capture-keys: List supported screenshot capture keys and breakdown variants
capture-keys:
	@node scripts/capture_ui_screenshots.mjs --list-capture-keys

## ci: Run checks and tests (local CI)
ci:
	@bash scripts/ci.sh

# =============================================================================
# SECURITY
# =============================================================================

## security-scan: Run all security scanners (govulncheck, gosec, pip-audit, bandit, cppcheck, gitleaks, hadolint, yamllint)
security-scan:
	@bash scripts/security-scan.sh

## security-audit: Run the built-in runtime security audit
security-audit:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend run ./cmd/security-audit -root $(CURDIR) -format markdown

# =============================================================================
# CLEAN
# =============================================================================

## clean: Remove build artifacts
clean:
	@rm -rf $(BUILD_DIR)
	@rm -rf .gocache
	@echo "Cleaned build directory"

# =============================================================================
# INSTALL
# =============================================================================

## install: Install binaries to /usr/local/bin
install: build
	@sudo cp build/sre-collector /usr/local/bin/
	@sudo cp build/sre-controller /usr/local/bin/
	@echo "Installed to /usr/local/bin/"

## install-collector-service: Install collector systemd service
install-collector-service: build-collector
	@sudo cp build/sre-collector /usr/local/bin/
	@sudo cp deploy/systemd/sre-collector.service /etc/systemd/system/ || true
	@sudo systemctl daemon-reload
	@echo "Installed sre-collector.service"
	@echo "Run: sudo systemctl enable --now sre-collector"

## install-controller-service: Install controller systemd service
install-controller-service: build-controller
	@sudo cp build/sre-controller /usr/local/bin/
	@sudo mkdir -p /etc/sre-controller
	@sudo cp configs/controller.yaml /etc/sre-controller/config.yaml
	@sudo cp -r web /var/lib/sre-controller/web
	@sudo cp deploy/systemd/sre-controller.service /etc/systemd/system/
	@sudo systemctl daemon-reload
	@echo "Installed sre-controller.service"
	@echo "Run: sudo systemctl enable --now sre-controller"

# =============================================================================
# DOCKER
# =============================================================================

## docker-controller: Build controller Docker image
docker-controller:
	docker build -t sre-controller:$(VERSION) -f deploy/docker/Dockerfile --target controller .

## docker: Build controller Docker image
docker: docker-controller

## docker-build: Build collector+controller images with plain docker build
docker-build:
	@./scripts/docker-build.sh

## docker-run-stack: Run controller+collector with plain docker run (no compose)
docker-run-stack:
	@./scripts/docker-run-stack.sh

## docker-stop-stack: Stop plain docker-run stack; pass PRUNE=1 to remove volumes/network
docker-stop-stack:
	@if [ "$(PRUNE)" = "1" ]; then \
		./scripts/docker-stop-stack.sh --prune; \
	else \
		./scripts/docker-stop-stack.sh; \
	fi

## docker-up: Build and start controller + collector with docker compose
docker-up:
	@if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
		docker compose up -d --build; \
	elif command -v docker-compose >/dev/null 2>&1; then \
		docker-compose up -d --build; \
	else \
		echo "docker compose not found (need 'docker compose' plugin or 'docker-compose')"; \
		exit 1; \
	fi

## docker-down: Stop docker compose stack
docker-down:
	@if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
		docker compose down; \
	elif command -v docker-compose >/dev/null 2>&1; then \
		docker-compose down; \
	else \
		echo "docker compose not found (need 'docker compose' plugin or 'docker-compose')"; \
		exit 1; \
	fi

## docker-logs: Tail controller logs
docker-logs:
	@if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
		docker compose logs -f controller; \
	elif command -v docker-compose >/dev/null 2>&1; then \
		docker-compose logs -f controller; \
	else \
		echo "docker compose not found (need 'docker compose' plugin or 'docker-compose')"; \
		exit 1; \
	fi

## smoke: Local smoke test (build + start + basic API checks)
smoke:
	@./scripts/smoke-local.sh

# =============================================================================
# FRONTEND
# =============================================================================

## frontend-install: Install frontend dependencies
frontend-install:
	@if command -v npm >/dev/null 2>&1; then \
		npm -C frontend install; \
	else \
		echo "npm not found"; \
		exit 1; \
	fi

## frontend-dev: Run the dashboard dev server
frontend-dev:
	@if command -v npm >/dev/null 2>&1; then \
		npm -C frontend run dev; \
	else \
		echo "npm not found"; \
		exit 1; \
	fi

## frontend-build: Build the dashboard
frontend-build:
	@if command -v npm >/dev/null 2>&1; then \
		npm -C frontend run build; \
	else \
		echo "npm not found"; \
		exit 1; \
	fi

## frontend-lint: Lint the dashboard sources
frontend-lint:
	@if command -v npm >/dev/null 2>&1; then \
		npm -C frontend run lint; \
	else \
		echo "npm not found"; \
		exit 1; \
	fi

# =============================================================================
# HELP
# =============================================================================

## help: Show this help
help:
	@echo "SRE Agent Build System"
	@echo "======================"
	@echo ""
	@echo "Components:"
	@echo "  sre-collector   Push-first telemetry collector (runs on each monitored host)"
	@echo "  sre-controller  Central aggregator and API server"
	@echo ""
	@echo "Quick Start:"
	@echo "  make build        Build both components"
	@echo "  make run-collector Run collector on :9090"
	@echo "  make run-controller Run controller on :8080"
	@echo "  make test         Run all tests"
	@echo ""
	@echo "Available targets:"
	@grep -E '^##' Makefile | sed 's/## /  /'
