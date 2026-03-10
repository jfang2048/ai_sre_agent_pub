# Tests

## Layout

- `tests/integration/`: Go integration tests.
- `tests/e2e/`: external-stack end-to-end tests (may skip when prerequisites are missing).
- `tests/python/`: Python analysis/runtime tests.
- `tests/ui/`: Playwright browser smoke tests.
- `tests/fixtures/`: shared test data.

Native probe-core coverage is exercised through the Go-side IPC boundary and live-binary tests under `backend/internal/collector/probecore/`. That is the real contract the controller and collector rely on; unbuilt standalone C++ tests are intentionally not kept around.

## Main commands

```bash
# full stability workflow
make test-stability

# backend-only
make test

# Playwright UI smoke (auto-bootstraps local stack)
make test-ui
```
