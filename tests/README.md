# Tests

## Layout

- `tests/integration/`: Go integration tests.
- `tests/e2e/`: external-stack end-to-end tests (may skip when prerequisites are missing).
- `tests/python/`: Python analysis/runtime tests.
- `tests/ui/`: Playwright browser smoke tests.
- `tests/fixtures/`: shared test data.
- `tests/cpp/`: C++ test coverage.

## Main commands

```bash
# full stability workflow
make test-stability

# backend-only
make test

# Playwright UI smoke (auto-bootstraps local stack)
make test-ui
```
