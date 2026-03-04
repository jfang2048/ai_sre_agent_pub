# Testing Guide (v0.5)

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

# Chrome/Chromium headless UI smoke tests (auto-build + auto-stack bootstrap)
make test-ui

# Focused joint-risk/RCA browser coverage
cd tests/ui && npx playwright test e2e/risk-rca.spec.ts

# Full stability workflow
make test-stability
```

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
