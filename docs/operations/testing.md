# Testing Guide (v0.7)

## 中文说明

- 这份文档按“验证层次”组织命令，而不是按语言或目录组织。真正需要回答的问题通常是: 我是在做快速回归、容器烟测、UI 闭环验证，还是 ingest/retention 压力验证?
- `fmt/vet/test` 回答的是基本正确性有没有被破坏；container smoke 回答的是支持的运行方式能不能真实启动；UI 和 runtime sanity checks 回答的是用户真正看到的控制面闭环是否还在。
- 把这些命令放在一个矩阵里，是为了让改动和验证成本匹配。不是每次都要跑最重的流程，但每类风险都应该有对应的最小验证面。

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

建议的中文使用顺序:

- 改轻量逻辑或非运行时代码时，先跑 fast local checks。
- 改 API、workflow、ingest、storage 时，补 container smoke 和 runtime sanity checks。
- 改 UI、页面、交互或前后端契约时，补 `make test-ui`。
- 改 retention、性能或高量写入路径时，再进入 soak/bench 级别测试。这样成本和风险才匹配。

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
