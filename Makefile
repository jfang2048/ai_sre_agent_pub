# SRE Agent Makefile
# ====================
#
# This Makefile builds the two-component AI SRE Agent:
# - sre-collector: Push-first telemetry collector (runs on monitored hosts)
# - sre-controller: Control-side aggregation, analysis, and API server
#
# Terminology:
#   - Collector: The push-first telemetry collector on each monitored host
#   - Controller: The central aggregation server
#   - Agent: The overall AI SRE system (not the host-side collector binary)

.PHONY: all proto proto-check build build-collector build-controller build-probe-core build-probe-core-best-effort \
        run run-collector run-controller run-both run-multinode \
        rag-status rag-query rag-index rag-rebuild rag-update rag-demo \
        test test-controller test-all test-cover test-race test-stability test-screenshot-tools bench \
        eval-fast eval-regression eval-benchmark \
        predictive-test predictive-bench low-overhead-benchmark chaos-test \
        fmt fmt-check vet lint ci verify-version verify-readme-screenshots capture-keys validate-manifests \
        frontend-install frontend-dev frontend-build frontend-lint \
        container-build container-build-controller container-build-collector \
        container-run-controller container-run-collector \
        container-up container-up-tsdb container-up-host-observer container-up-full \
        container-down container-down-tsdb container-down-host-observer container-down-full container-logs container-smoke \
        docker-up docker-up-tsdb docker-up-host-observer docker-down docker-down-tsdb docker-down-host-observer docker-down-full docker-logs docker-build docker-build-controller docker-build-collector docker-run-controller docker-run-collector docker-run-stack docker-stop-stack smoke \
        clean install install-collector-service install-controller-service \
        docker-controller docker \
        security-scan security-audit sbom helm-package helm-smoke predictive-validation multiarch-build python-package-check \
        help

# Build configuration
VERSION_FILE ?= $(CURDIR)/VERSION
VERSION ?= $(shell cat $(VERSION_FILE) 2>/dev/null || echo v0.7)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)"

# Keep build outputs inside the repo so `make clean` works and paths are consistent.
# Use an absolute path because we build from `backend/` (via `go -C backend ...`).
BUILD_DIR := $(CURDIR)/build
PROBE_CORE_GEN_DIR := $(BUILD_DIR)/probe_core_generated

# Some environments end up with an unreadable default GOCACHE (e.g. due to prior sudo runs).
# Pin it to a repo-local cache to keep `make fmt/test/build` reliable.
GO_CACHE ?= $(CURDIR)/.gocache
GO := GOCACHE=$(GO_CACHE) go
RAG_DATASET_PATH ?= $(CURDIR)/dataset
RAG_INDEX_PATH ?= $(CURDIR)/data/agent/rag/index.json
RAG_ENV := SRE_AGENT_RAG_ENABLED=1 SRE_AGENT_RAG_DATASET_PATH=$(RAG_DATASET_PATH) SRE_AGENT_RAG_INDEX_PATH=$(RAG_INDEX_PATH)

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

## build: Build collector/controller binaries and attempt primary probe-core runtime
build: proto-check build-collector build-controller build-probe-core-best-effort
	@echo "✓ Build complete"
	@echo "  Collector:  build/sre-collector"
	@echo "  Controller: build/sre-controller"
	@echo "  Primary C++ probe-core: build/sre-probe-core"

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

## build-probe-core: Build the primary C++ probe-core IPC binary
build-probe-core:
	@mkdir -p $(BUILD_DIR)
	@if command -v pkg-config >/dev/null 2>&1 && pkg-config --exists protobuf zlib; then \
		set -e; \
		GEN_DIR="$(PROBE_CORE_GEN_DIR)"; \
		HEADER_DIR="cpp/probe_core/generated"; \
		SRC_CC="cpp/probe_core/generated/probeipc/v1/probeipc.pb.cc"; \
		if command -v protoc >/dev/null 2>&1; then \
			mkdir -p "$$GEN_DIR"; \
			protoc --proto_path=proto --cpp_out="$$GEN_DIR" proto/probeipc/v1/probeipc.proto; \
			PROBE_HEADER="$$GEN_DIR/probeipc/v1/probeipc.pb.h"; \
			if [ -f "$$PROBE_HEADER" ]; then \
				sed -i 's/^#if PROTOBUF_VERSION != /#if PROTOBUF_VERSION < /' "$$PROBE_HEADER"; \
			fi; \
			HEADER_DIR="$$GEN_DIR"; \
			SRC_CC="$$GEN_DIR/probeipc/v1/probeipc.pb.cc"; \
		fi; \
		g++ -std=c++20 -O2 -Wall -Wextra -pthread \
			$$(pkg-config --cflags protobuf zlib) \
			-I"$$HEADER_DIR" \
			-o $(BUILD_DIR)/sre-probe-core \
			cpp/probe_core/main.cpp cpp/probe_core/subprocess.cpp cpp/probe_core/gpu_nvml.cpp "$$SRC_CC" \
			$$(pkg-config --libs protobuf zlib) -ldl; \
		echo "  → build/sre-probe-core"; \
	else \
		echo "pkg-config protobuf/zlib not found; skipping probe-core build"; \
		exit 1; \
	fi

## build-probe-core-best-effort: Try to build the primary probe-core binary without failing the whole local build
build-probe-core-best-effort:
	@$(MAKE) build-probe-core >/dev/null || \
		(echo "! probe-core build skipped; collector will require compatibility fallback until C++ deps are installed" >&2)

# =============================================================================
# RUN (Development)
# =============================================================================

## run: Run collector + controller (for development)
run: run-both

## run-collector: Run the push-first collector (default)
run-collector: build-collector build-probe-core-best-effort
	SRE_COLLECTOR_CONFIG=./configs/collector.yaml ./scripts/run-collector.sh

## run-controller: Run the controller on :8080
run-controller: build-controller
	SRE_CONTROLLER_CONFIG=./configs/controller.yaml SRE_CONTROLLER_TARGETS_FILE=./configs/controller_targets.yaml ./scripts/run-controller.sh

## run-both: Run collector + controller
run-both: build
	@./scripts/run-local.sh

## run-multinode: Run controller + multiple local collectors
run-multinode: build
	@./scripts/run-local-multinode.sh

## rag-status: Print current local RAG index status as JSON
rag-status:
	@mkdir -p $(GO_CACHE)
	@$(RAG_ENV) $(GO) -C backend run ./cmd/ragctl status

## rag-query: Query the local RAG index (`make rag-query QUERY="timeout deployment"`)
rag-query:
	@mkdir -p $(GO_CACHE)
	@$(RAG_ENV) $(GO) -C backend run ./cmd/ragctl query -q "$(QUERY)" -k $(if $(TOPK),$(TOPK),5)

## rag-index: Build or rebuild the local RAG index from dataset/
rag-index: rag-rebuild

## rag-rebuild: Rebuild the local RAG index from scratch
rag-rebuild:
	@mkdir -p $(GO_CACHE)
	@$(RAG_ENV) $(GO) -C backend run ./cmd/ragctl rebuild

## rag-update: Incrementally update the local RAG index
rag-update:
	@mkdir -p $(GO_CACHE)
	@$(RAG_ENV) $(GO) -C backend run ./cmd/ragctl update

## rag-demo: Rebuild the local RAG index, then start the demo stack
rag-demo: build rag-rebuild
	@$(RAG_ENV) SRE_AGENT_RAG_REBUILD_POLICY=if_missing ./scripts/run-local.sh --enable-agent --demo --llm=stub

# =============================================================================
# TEST
# =============================================================================

## test: Run all tests
test:
	@echo "Running tests..."
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -v ./internal/collector/... ./internal/controller/...

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
	@$(GO) -C backend test -cover ./internal/collector/... ./internal/controller/...

## test-race: Run key packages under the race detector
test-race:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -race ./internal/collector/... ./internal/controller/...

## test-stability: Run test-first stability workflow (backend + integration + e2e + python + UI)
test-stability:
	@mkdir -p $(GO_CACHE)
	@echo "Running backend unit/integration tests..."
	@$(GO) -C backend test ./... -count=1
	@echo "Running external integration pipeline tests..."
	@$(GO) -C tests/integration test -mod=mod -v .
	@echo "Running external-stack E2E tests (will skip if preconditions are unmet)..."
	@$(GO) -C tests/e2e test -mod=mod -v -tags=e2e .
	@echo "Running python analysis/runtime tests..."
	@python3 -m unittest discover -s tests/python -p 'test_*.py'
	@echo "Running frontend data-flow tests..."
	@cd frontend && npm test -- --watch=false

## test-ui: Run Chrome headless UI tests with automatic local stack bootstrap
test-ui:
	@./scripts/test/ui-smoke.sh

## test-screenshot-tools: Validate screenshot capture script CLI/config guards
test-screenshot-tools:
	@./scripts/test/screenshot_tooling.sh

## bench: Run benchmarks
bench:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -bench=. ./internal/collector/... ./internal/controller/...

## eval-fast: Run the cheap golden RAG/AIOps evaluation gate used for regular development
eval-fast:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend run ./cmd/evalctl -scope fast -format text

## eval-regression: Run the broader regression evaluation suite
eval-regression:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend run ./cmd/evalctl -scope regression -format text

## eval-benchmark: Run the largest deterministic benchmark/nightly-style evaluation suite
eval-benchmark:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend run ./cmd/evalctl -scope benchmark -format text

## predictive-test: Run predictive controller and agent tests
predictive-test:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test ./internal/controller/predictive ./internal/controller/agent ./internal/pkg/collections/ring

## predictive-bench: Run focused predictive and ring benchmarks
predictive-bench:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -run '^$$' -bench 'Benchmark(Evaluate|RingPush)' -benchmem ./internal/controller/predictive ./internal/pkg/collections/ring

## low-overhead-benchmark: Run the lightweight benchmark gate for predictive hot paths
low-overhead-benchmark:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C backend test -run '^$$' -bench 'Benchmark(Evaluate|RingPush)' -benchmem -benchtime=200x ./internal/controller/predictive ./internal/pkg/collections/ring

## chaos-test: Run external-stack e2e scenarios that exercise replay/failure handling
chaos-test:
	@mkdir -p $(GO_CACHE)
	@$(GO) -C tests/e2e test -mod=mod -v -tags=e2e .

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

## verify-version: Fail if project-owned files still reference the previous release string
verify-version:
	@stale="$$(rg -n -P 'v0\\.6(?![0-9])|0\\.6\\.0' README.md CHANGELOG.md CONTRIBUTING.md SECURITY.md Makefile docs deploy configs backend/cmd backend/internal python scripts tests/python tests/ui/package.json .env.example dataset/raw/archives docker-compose.yaml || true)"; \
	if [ -n "$$stale" ]; then \
		echo "Found stale previous-release references:"; \
		echo "$$stale"; \
		exit 1; \
	fi

## validate-manifests: Render and lint supported Helm/Kustomize deployment assets
validate-manifests:
	@./scripts/validate-manifests.sh

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

## sbom: Generate a CycloneDX SBOM when syft is installed
sbom:
	@mkdir -p $(BUILD_DIR)
	@if command -v syft >/dev/null 2>&1; then \
		syft packages dir:$(CURDIR) -o cyclonedx-json > $(BUILD_DIR)/sbom.cdx.json; \
		echo "  → $(BUILD_DIR)/sbom.cdx.json"; \
	else \
		echo "syft not found; install syft to generate an SBOM"; \
		exit 1; \
	fi

## helm-package: Package the Helm chart into build/helm
helm-package:
	@mkdir -p $(BUILD_DIR)/helm
	@if command -v helm >/dev/null 2>&1; then \
		helm package deploy/charts/sre-agent --destination $(BUILD_DIR)/helm >/dev/null; \
		echo "  → $(BUILD_DIR)/helm"; \
	else \
		echo "helm not found"; \
		exit 1; \
	fi

## helm-smoke: Lint and template the Helm chart
helm-smoke:
	@if command -v helm >/dev/null 2>&1; then \
		helm lint deploy/charts/sre-agent && helm template ai-sre-agent deploy/charts/sre-agent >/dev/null; \
	else \
		echo "helm not found"; \
		exit 1; \
	fi

## predictive-validation: Run predictive code, version, and packaging checks together
predictive-validation: verify-version predictive-test low-overhead-benchmark helm-smoke

## multiarch-build: Build controller and collector images with docker buildx
multiarch-build:
	@if command -v docker >/dev/null 2>&1 && docker buildx version >/dev/null 2>&1; then \
		docker buildx build --platform $(if $(PLATFORM),$(PLATFORM),linux/amd64) --load -f deploy/docker/Dockerfile.controller -t ai-sre-agent-controller:$(VERSION) . >/dev/null; \
		docker buildx build --platform $(if $(PLATFORM),$(PLATFORM),linux/amd64) --load -f deploy/docker/Dockerfile.collector -t ai-sre-agent-collector:$(VERSION) . >/dev/null; \
	else \
		echo "docker buildx not found"; \
		exit 1; \
	fi

## python-package-check: Validate the optional Python package and tests
python-package-check:
	@python3 -m compileall python/sre_agent >/dev/null
	@python3 -m unittest discover -s tests/python -p 'test_*.py'

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

## container-build: Build the canonical controller and collector images
container-build:
	@./scripts/docker-build.sh

## container-build-controller: Build only the controller image
container-build-controller:
	@./scripts/docker-build-controller.sh

## container-build-collector: Build only the collector image
container-build-collector:
	@./scripts/docker-build-collector.sh

## container-run-controller: Run only the controller container
container-run-controller:
	@./scripts/docker-run-controller.sh

## container-run-collector: Run only the collector container
container-run-collector:
	@./scripts/docker-run-collector.sh

## container-up: Start the canonical controller + collector compose stack
container-up:
	@./scripts/docker-run-stack.sh

## container-up-tsdb: Start the canonical stack plus the InfluxDB overlay
container-up-tsdb:
	@./scripts/docker-run-stack.sh --tsdb

## container-up-host-observer: Start controller + collector with host-observer capabilities
container-up-host-observer:
	@./scripts/docker-run-stack.sh --host-observer

## container-up-full: Start controller + collector + TSDB + host-observer overlay
container-up-full:
	@./scripts/docker-run-stack.sh --tsdb --host-observer

## container-down: Stop the canonical controller + collector stack
container-down:
	@./scripts/docker-stop-stack.sh

## container-down-tsdb: Stop the canonical stack plus the InfluxDB overlay
container-down-tsdb:
	@./scripts/docker-stop-stack.sh --tsdb

## container-down-host-observer: Stop the stack started with the host-observer overlay
container-down-host-observer:
	@./scripts/docker-stop-stack.sh --host-observer

## container-down-full: Stop the full stack (TSDB + host-observer overlay)
container-down-full:
	@./scripts/docker-stop-stack.sh --tsdb --host-observer

## container-logs: Tail controller and collector logs from the canonical stack
container-logs:
	@if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
		COMPOSE_PROJECT_NAME=$${COMPOSE_PROJECT_NAME:-ai-sre-agent} docker compose -f docker-compose.yaml logs -f controller collector; \
	elif command -v docker >/dev/null 2>&1; then \
		docker logs -f ai-sre-agent-controller & \
		controller_pid=$$!; \
		docker logs -f ai-sre-agent-collector & \
		collector_pid=$$!; \
		trap 'kill $$controller_pid $$collector_pid >/dev/null 2>&1 || true' INT TERM EXIT; \
		wait $$controller_pid $$collector_pid; \
	else \
		echo "docker is not available"; \
		exit 1; \
	fi

## container-smoke: Build and validate the canonical compose stack
container-smoke:
	@./scripts/docker-smoke.sh

## docker-controller: Build the canonical controller + collector images
docker-controller:
	@./scripts/docker-build.sh

## docker: Build the canonical controller + collector images
docker: docker-controller

## docker-build: Build collector+controller images with plain docker build
docker-build: container-build

## docker-build-controller: Build only the controller image
docker-build-controller: container-build-controller

## docker-build-collector: Build only the collector image
docker-build-collector: container-build-collector

## docker-run-controller: Run only the controller container
docker-run-controller: container-run-controller

## docker-run-collector: Run only the collector container
docker-run-collector: container-run-collector

## docker-run-stack: Start the canonical compose stack
docker-run-stack: container-up

## docker-stop-stack: Stop the canonical compose stack; pass PRUNE=1 to remove volumes
docker-stop-stack:
	@if [ "$(PRUNE)" = "1" ]; then \
		./scripts/docker-stop-stack.sh --prune; \
	else \
		./scripts/docker-stop-stack.sh; \
	fi

## docker-up: Build and start controller + collector with docker compose
docker-up: container-up

## docker-up-tsdb: Build and start controller + collector + InfluxDB compose overlay
docker-up-tsdb: container-up-tsdb

## docker-up-host-observer: Build and start controller + collector with host-observer overlay
docker-up-host-observer: container-up-host-observer

## docker-down: Stop docker compose stack
docker-down: container-down

## docker-down-tsdb: Stop controller + collector + InfluxDB compose overlay
docker-down-tsdb: container-down-tsdb

## docker-down-host-observer: Stop controller + collector with host-observer overlay
docker-down-host-observer: container-down-host-observer

## docker-down-full: Stop the full stack (TSDB + host-observer overlay)
docker-down-full: container-down-full

## docker-logs: Tail controller logs
docker-logs: container-logs

## smoke: Canonical container-first smoke test
smoke: container-smoke

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
	@echo "  make run-controller Run controller from source mode"
	@echo "  make run-collector Run collector from source mode"
	@echo "  make container-run-controller Run only the controller container"
	@echo "  make container-run-collector Run only the collector container"
	@echo "  make test         Run all tests"
	@echo ""
	@echo "Available targets:"
	@grep -E '^##' Makefile | sed 's/## /  /'
