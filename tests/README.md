# Test Suite Layout

This directory contains cross-package test suites that run outside individual Go packages.

## Structure

- `tests/integration/`
  - In-memory gRPC pipeline tests (`bufconn`) for collector -> ingest -> store behavior.
  - Shared gRPC harness helpers are in `helpers_test.go`.
- `tests/e2e/`
  - Live-stack HTTP flow tests for probe-controller-agent workflows.
  - Shared HTTP helpers/preflight checks are in `helpers_test.go`.
- `tests/python/`
  - Python unit tests split by domain:
    - `test_analysis_anomaly_forecast.py`
    - `test_analysis_correlation_metrics_prediction.py`
    - `test_llm_clients.py`
    - `test_runtime_orchestrator.py`
  - Shared bootstrap and fixtures:
    - `_bootstrap.py` for import path setup.
    - `_fixtures.py` for common test datasets.
- `tests/fixtures/`
  - Static logs/config/metrics fixture files used by tests and scripts.

## Run

```bash
make test-stability
```

or per-suite:

```bash
cd tests/integration && go test -v .
cd tests/e2e && go test -v -tags=e2e .
python3 -m unittest discover -s tests/python -p 'test_*.py'
```
