# Test-First Debugging Guide

This repository uses a test-first debugging loop to improve stability:

1. Reproduce the issue with an automated test.
2. Confirm the test fails for the right reason.
3. Implement the smallest fix that makes the test pass.
4. Run nearby and broader suites to catch regressions.
5. Keep the new test as a regression guard.

## Test layers and ownership

| Layer | Goal | Primary locations |
|---|---|---|
| Unit tests | Validate deterministic logic and edge cases in isolation | `backend/internal/core`, `backend/internal/collector`, `backend/internal/controller/*`, `frontend/src/**/*.test.ts(x)` |
| Integration tests | Validate multi-component data flow and contracts | `tests/integration/pipeline_test.go` (bufconn in-memory gRPC happy path + invalid-payload recovery), `backend/internal/monitoring/collector_integration_test.go` |
| End-to-end tests | Validate probe -> controller -> API/UI workflow from ingest to rendered outputs | `tests/e2e/data_flow_test.go`, `tests/e2e/agent_test.go`, UI behavior tests under `frontend/src/components/**/__tests__` |

## Run tests

Backend full:

```bash
cd backend
go test ./...
```

Frontend full:

```bash
cd frontend
npm test
```

Layered stability workflow (recommended during refactors/debugging):

```bash
make test-stability
```

Integration Tests:

```bash
cd tests/integration
go test -v .
```

End-to-End Tests:

```bash
cd tests/e2e
go test -v -tags=e2e .
```

Python unit tests:

```bash
python3 -m unittest discover -s tests/python -p 'test_*.py'
```

Python suite layout:
- `tests/python/test_analysis_anomaly_forecast.py`
- `tests/python/test_analysis_correlation_metrics_prediction.py`
- `tests/python/test_llm_clients.py`
- `tests/python/test_runtime_orchestrator.py`
- shared helpers: `tests/python/_bootstrap.py`, `tests/python/_fixtures.py`

Restricted-runner note:
- Socket-binding integration/E2E suites (webhook publish tests and probe-controller workflow tests) automatically `Skip` when local TCP bind is blocked by sandbox policy.
- External integration pipeline tests under `tests/integration` use in-memory gRPC (`bufconn`) and should run even when TCP bind is restricted.
- External-stack E2E tests under `tests/e2e` now preflight the controller and `Skip` immediately when the local stack is unavailable (`connection refused`) or socket access is restricted (`operation not permitted`).
- `scripts/ci.sh` builds frontend assets into `/tmp/ai_sre_agent_frontend_build` by default to avoid dirtying `web/index.html`; override with `SRE_FRONTEND_BUILD_OUTDIR`.

Targeted stability suites:

```bash
# Core unit logic
cd backend && go test ./internal/core -count=1

# Core latest-metrics defensive-copy guard
cd backend && go test ./internal/core -run TestGetLatestMetricsReturnsDefensiveLabelCopies -count=1

# Core run-once snapshot defensive-copy guard
cd backend && go test ./internal/core -run TestRunOnceStoresDefensiveMetricsSnapshot -count=1

# Core latest-index/latest-values sync guard
cd backend && go test ./internal/core -run TestSetLatestMetricsRebuildsIndexesAndLatestValues -count=1

# Core latest-metric key deduplication guard
cd backend && go test ./internal/core -run TestSetLatestMetricsDeduplicatesByMetricKeyKeepingLatestValue -count=1

# Core concurrency guard (run with race detector)
cd backend && go test -race ./internal/core -run TestHandleSLOSummaryConcurrentReadWrite -count=1

# Collection + ingest pipeline integration
cd backend && go test ./internal/monitoring ./internal/controller/ingest -count=1

# External integration pipeline (gRPC ingest client -> ingest server -> store)
cd tests/integration && go test -v .

# Integration recovery guard: invalid payload must fail without poisoning next send
cd tests/integration && go test -run TestPipelineIntegrationRecoversAfterInvalidPayload -v .

# Collector nil-safety guard for source payload conversion
cd backend && go test ./internal/monitoring -run TestCollectorCollectOnceSkipsNilMetricEntriesAndLabels -count=1

# Collector nil-point conversion guard
cd backend && go test ./internal/monitoring -run TestCollectorCollectOnceSkipsNilMetricPoints -count=1

# Collector empty-name metric filtering guard
cd backend && go test ./internal/monitoring -run TestCollectorCollectOnceSkipsMetricsWithEmptyName -count=1

# Collector non-finite value filtering guard
cd backend && go test ./internal/monitoring -run TestCollectorCollectOnceSkipsNonFiniteMetricValues -count=1

# Collector empty-point filtering guard
cd backend && go test ./internal/monitoring -run TestCollectorCollectOnceSkipsMetricsWithoutPoints -count=1

# Collector label-key normalization guard
cd backend && go test ./internal/monitoring -run TestCollectorCollectOnceTrimsAndFiltersLabelKeys -count=1

# Ingest validation + recovery for malformed labels
cd backend && go test ./internal/controller/ingest -run TestPushRejectsNilMetricLabelThenAcceptsNextStream -count=1

# Store collector-label sanitization guard
cd backend && go test ./internal/controller/ingest -run TestUpsertCollectorIgnoresNilAndBlankLabels -count=1

# Ingest snapshot defensive-copy guard
cd backend && go test ./internal/controller/ingest -run TestNodeReturnsDefensiveCopiesForProcessesAndLogs -count=1

# Probe -> controller end-to-end workflow
cd backend && go test ./internal/controller -run TestProbeControllerWorkflowE2E -count=1

# Probe -> controller -> top-programs workflow
cd backend && go test ./internal/controller -run TestProbeControllerTopProgramsFlowE2E -count=1

# Collector lifecycle stability (idempotent stop + restart + double-start guard)
cd backend && go test ./internal/monitoring -run 'TestCollector(StopIsIdempotent|CanRestartAfterStop|StartTwiceDoesNotSpawnDuplicateLoops)' -count=1

# Controller ingest recovery after rejected stream
cd backend && go test ./internal/controller -run TestControllerIngestRecoversAfterInvalidStreamE2E -count=1

# Sustained ingest soak-style workflow check
cd backend && go test ./internal/controller -run TestControllerSustainedIngestStatsAndSummaryE2E -count=1

# UI data-flow regression checks
cd frontend && npm test -- MetricOverviewPanel
cd frontend && npm test -- MetricTrendsPage
cd frontend && npm test -- App.test.tsx

# Python analysis/runtime regression checks
python3 -m unittest discover -s tests/python -p 'test_*.py'

# Alerting: fingerprint, severity, ingestion, deduplication, correlation
cd backend && go test ./internal/alerting/ -count=1

# Remediation: safety, permission, rate-limit, cooldown rules
cd backend && go test ./internal/remediation/ -count=1

# AI classifier: rule-based metric/log classification
cd backend && go test ./internal/controller/ai/classifier/ -count=1

# AI suggester: rule-based remediation suggestions
cd backend && go test ./internal/controller/ai/suggester/ -count=1

# FinOps: idle detection, rightsizing, cost analysis
cd backend && go test ./internal/finops/ -count=1

# Change management: registration, approval, canary metrics, lifecycle
cd backend && go test ./internal/change/ -count=1

# Probe filter: EMA smoothing, outlier rejection, label isolation
cd backend && go test -run 'TestLabels|TestNewMetrics|TestApply' ./internal/probe/ -count=1

# Brain predictor: trend analysis, anomaly scoring, prediction combining
cd backend && go test ./internal/brain/predictor/ -count=1

# AI queue: FIFO, bounded drop, close semantics, serialization
cd backend && go test ./internal/controller/ai/queue/ -count=1

# Incident store: CRUD, state transitions, MTTR, search
cd backend && go test -run 'TestAdd|TestGet|TestUpdate|TestDelete|TestPostMortem|TestList|TestSearch|TestRecord|TestAssign|TestCreate|TestCalculate' ./internal/store/ -count=1

# AI API handlers: analyze, results, stats, ingest (httptest)
cd backend && go test -run 'TestHandle|TestConvert' ./internal/controller/ai/ -count=1

# Analysis: trend detection, LLM schema builders, provider helpers
cd backend && go test ./internal/controller/analysis/ -count=1

# Remediation executor: dry-run, live stubs, concurrency, rollback
cd backend && go test ./internal/remediation/ -count=1
```

## Failure triage

- `backend/internal/core` failures usually indicate business-logic regressions; fix logic first, then verify behavior-facing APIs.
- `TestHandleSLOSummaryConcurrentReadWrite` (race) failures indicate unsynchronized shared-state access in SRE API handlers.
- `internal/monitoring` or `internal/controller/ingest` failures usually indicate telemetry schema/transport/processing issues.
- `TestCollectorCollectOnceSkipsNilMetricEntriesAndLabels` failures indicate source payload nil-safety regressions in collector conversion logic.
- `TestCollectorCollectOnceSkipsNilMetricPoints` failures indicate collector conversion panics or value drift when source point arrays contain nil entries.
- `TestCollectorCollectOnceSkipsMetricsWithEmptyName` failures indicate collector conversion is passing invalid empty metric identifiers downstream.
- `TestCollectorCollectOnceSkipsNonFiniteMetricValues` failures indicate collector conversion is forwarding `NaN`/`Inf` values that can break downstream serialization/aggregation.
- `TestCollectorCollectOnceSkipsMetricsWithoutPoints` failures indicate collector conversion is injecting false zero values from point-less metrics.
- `TestCollectorCollectOnceTrimsAndFiltersLabelKeys` failures indicate label-key normalization regressions that can fragment metric identities.
- `TestPushRejectsNilMetricLabelThenAcceptsNextStream` failures indicate ingest validation/recovery regressions for malformed telemetry labels.
- `TestUpsertCollectorIgnoresNilAndBlankLabels` failures indicate store-side metadata ingestion can panic or persist invalid label keys when called without gRPC validation.
- `TestNodeReturnsDefensiveCopiesForProcessesAndLogs` failures indicate store snapshot aliasing (callers mutating in-memory state through returned pointers).
- `TestGetLatestMetricsReturnsDefensiveLabelCopies` failures indicate API callers can mutate agent in-memory metric labels via returned snapshots.
- `TestRunOnceStoresDefensiveMetricsSnapshot` failures indicate one-shot collection results can alias and mutate agent internal latest-metric state.
- `TestSetLatestMetricsRebuildsIndexesAndLatestValues` failures indicate stale key-index caches that can desynchronize stream updates and latest snapshots.
- `TestSetLatestMetricsDeduplicatesByMetricKeyKeepingLatestValue` failures indicate latest-metric snapshots can contain duplicate keys and diverge from stream-update semantics.
- `probe_controller_e2e` failures usually indicate broken probe-controller wiring (gRPC ingest, fleet store, or API handlers).
- `TestPipelineIntegrationRecoversAfterInvalidPayload` failures indicate an invalid ingest stream is contaminating subsequent valid pipeline sends.
- `top-programs` e2e failures usually indicate process/network/log attribution regressions between ingest and ranking APIs.
- collector lifecycle tests usually indicate goroutine/channel lifecycle regressions in start/stop/restart paths.
- sustained ingest e2e failures usually indicate drift in ingest counters, store writes, or fleet summary under repeated batches.
- `App.test.tsx` failures usually indicate diagnostics -> trends navigation intent wiring regressions.
- Frontend data-flow test failures usually indicate API contract drift or rendering assumptions that no longer hold.
- `tests/python/*` failures usually indicate analysis helper API drift (for example correlation/prediction/metrics modules) or local Python import-path/config regressions.
- `skipping due to listen permission error` means environment socket restrictions, not a product logic regression.
- `skipping e2e: controller unavailable or sockets restricted` in `tests/e2e` means the live local stack precondition (`./scripts/run-local.sh --enable-agent`) was not met in that runner.

## Practical policy for refactors

- Do not perform broad refactors without a failing test that captures the bug/risk first.
- Prefer extending existing tests near touched code over creating disconnected test files.
- Treat every fixed production bug as a required regression test addition.
- Keep cross-package suites organized under `tests/`; see `tests/README.md` for canonical suite boundaries and helper locations.
