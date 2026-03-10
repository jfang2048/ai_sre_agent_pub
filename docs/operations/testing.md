# Testing Guide (v0.6)

## Command matrix

```bash
# Fast local checks
make fmt-check
make vet
make test

# Backend full package compile+test
make test-all

# Frontend unit tests
cd frontend && npm test -- --watch=false

# Frontend production build
cd frontend && npm run build

# Supported deployment assets render cleanly
make validate-manifests

# Canonical container runtime smoke test
make container-smoke

# Role-specific container bring-up
make container-run-controller
make container-run-collector

# Chrome/Chromium headless UI smoke tests (auto-build + auto-stack bootstrap)
make test-ui

# Full source-mode stability workflow
make test-stability
```

## Container runtime validation

```bash
# Build canonical images
make container-build

# Start controller + collector
make container-up

# Add TSDB overlay
make container-up-tsdb

# Add host-observer collector overlay
make container-up-host-observer

# Full stack
make container-up-full
```

The canonical smoke path exercises:

- controller health endpoint
- controller status/fleet/storage APIs
- controller inventory APIs
- RAG status endpoint
- agent joint-risk endpoint
- UI root page

In bridge-restricted environments, `scripts/docker-smoke.sh` falls back to host networking in the plain-docker path.

## Scale/retention-focused coverage

```bash
# Targeted ingest storage soak and scale tests
cd backend && go test ./internal/controller/ingest -run "Soak|Persistence|Retention" -count=1

# Benchmarks (includes high-volume ingest benchmark)
cd backend && go test ./internal/controller/ingest -bench=. -run '^$'
```

## Probe-core performance check

```bash
./scripts/benchmark_probe_core.sh 20 200
```

This compares kernel-first (`--host-mode auto`) against `/proc` primary (`--host-mode proc`).

Probe-core correctness is validated through the shipped IPC boundary, not through a parallel fake harness. The relevant tests live under `backend/internal/collector/probecore/`, including the live-binary smoke that verifies the actual emitted metric surface when `build/sre-probe-core` is present.

## Runtime sanity checks

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/ha/status
curl -sS http://127.0.0.1:8080/api/v1/storage/status
curl -sS http://127.0.0.1:8080/api/v1/finops/signals
curl -sS http://127.0.0.1:8080/api/v1/analysis/status
curl -sS http://127.0.0.1:8080/api/v1/analysis/incidents?limit=5
curl -sS http://127.0.0.1:8080/api/v1/agent/joint-risk?limit=3
curl -sS http://127.0.0.1:8080/api/v1/agent/rca?limit=3
curl -sS http://127.0.0.1:8080/api/v1/agent/workflow/audit?limit=20
```
