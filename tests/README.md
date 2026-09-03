# Tests

This directory is split by verification target, not by language or framework.
That is intentional. The cheapest useful check is usually not the biggest suite.

The main implementation surfaces those suites exercise are:

- `backend/internal/collector/`
- `backend/internal/controller/`
- `backend/internal/controller/agentcore/`
- `backend/internal/collector/probecore/`
- `frontend/src/`

## What lives where

- `tests/integration/`: Go integration tests for controller, ingest, workflow, and storage paths.
- `tests/e2e/`: external-stack end-to-end tests. Some cases skip when prerequisites are missing.
- `tests/python/`: analysis/runtime tests for the Python side.
- `tests/ui/`: Playwright checks for the browser surfaces.
- `tests/fixtures/`: shared input data.

Probe-core coverage is exercised through the Go-side IPC boundary and live-binary tests under `backend/internal/collector/probecore/`. That is the real contract.

## How to choose a suite

| Change area                      | Start with                                            | Add when needed                                                                 |
| -------------------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------------- |
| collector or probe-core          | package tests under `backend/internal/collector/...`  | `make test-stability` if replay, spool, or backpressure changed                 |
| controller workflow or agentcore | package tests under `backend/internal/controller/...` | integration tests if persistence, message history, or artifact handling changed |
| UI or browser flow               | targeted Playwright/Vitest tests                      | `make test-ui` for the full browser smoke path                                  |
| deployment or packaging          | integration plus build checks                         | `make helm-smoke`, `make build-probe-core`, or image build checks               |
| analysis/runtime in Python       | `tests/python/`                                       | the broader backend suite if the workflow contract changed                      |

## Main commands

```bash
make test
make test-ui
make test-stability
```

Use the smallest command that covers the failure mode. Do not start with the most expensive path unless the change really touches durability, queueing, or cross-component behavior.

## What a failed test usually means

- **collector tests fail**: payload shape, cadence, replay, suppression, or host-resource assumptions shifted.
- **controller tests fail**: workflow state, artifact history, validation, policy, or storage contract shifted.
- **UI tests fail**: route readiness, API response shape, or browser timing shifted.
- **stability tests fail**: queue growth, replay behavior, or long-run backpressure is not bounded.

## 中文提示

- 日常改动先跑最小的包级测试，再决定要不要加更重的 suite。
- 改 workflow、artifact、replay、queue 相关逻辑时，`make test-stability` 才有意义。
- 改 UI 时，优先跑命中的 Playwright/Vitest 用例，再补 `make test-ui`。
