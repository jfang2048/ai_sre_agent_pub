# AI SRE Agent (v0.3)

## 中文文档

### 1. 项目概述

AI SRE Agent 是一个面向 Linux / GPU AI 基础设施的 push-first 可观测性与运维平台，核心由两个二进制组件组成：

- `sre-collector`：部署在被监控节点，采集主机、进程、GPU、日志等信号，批量推送到控制面。
- `sre-controller`：接收并校验遥测数据，维护集群内存态视图，提供 API、Web UI、Prometheus 指标与诊断能力。

该项目重点解决三个问题：

- 复杂 AI 基础设施中的跨层可观测性（节点、进程、GPU、网络、存储、工作负载）。
- 事件排障中的诊断路径（数据路径、内核路径、根因假设、工作负载扩散）。
- 运维执行中的安全边界（受控执行、审批令牌、dry-run）。

### 2. 核心能力

- Push-first + spool-first 遥测链路，具备重试和失败恢复能力。
- Linux 低层信号采集（`/proc`、`/sys`、PSI、调度/TCP、磁盘与网络关键指标）。
- 可选 C++ probe-core 关键路径采集（`cpp/probe_core`），通过 protobuf IPC 接入 collector。
- GPU 观测（利用率、显存、温度/功耗、健康状态、进程级信号）。
- Kubernetes 只读视图与多节点资源清单联动。
- 诊断 API：`data-path`、`kernel-path`、`root-cause`、`workload-path`、`ai-infra-stack`、`rca-packet`。
- 可选 AGENT 工作流：自然语言查询 + 受保护执行。

### 3. 架构与代码边界

```text
Monitored Host(s)
  └─ sre-collector
       ├─ optional probe-core IPC source
       ├─ collect host/process/gpu/log signals
       ├─ batch telemetry + local spool
       └─ push via gRPC

Control Plane
  └─ sre-controller
       ├─ ingest validation + memory store
       ├─ inventory/k8s/gpu aggregation
       ├─ diagnostics + orchestration modules
       └─ REST APIs + UI + /metrics
```

主要目录：

- Collector：`backend/internal/collector`
- Probe 采集：`backend/internal/probe`
- Ingest 存储：`backend/internal/controller/ingest`
- Orchestration：`backend/internal/controller/orchestration`
- 分析与诊断：`backend/internal/controller/analysis`
- 前端 UI：`frontend/src`
- Python 运行时与分析：`python/sre_agent`
- 跨包测试套件：`tests/`

### 4. 快速开始

#### 4.1 前置条件

- Go `1.25+`
- Node.js `18+`（前端开发/测试）
- Python `3.10+`（Python 模块与测试）
- Linux（推荐，便于完整采集低层信号）

#### 4.2 启动本地栈

```bash
./scripts/run-local.sh
```

健康检查：

```bash
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s http://127.0.0.1:8080/api/v1/fleet
```

#### 4.3 可选模式

```bash
# 多节点本地模拟
./scripts/run-local-multinode.sh --collectors 3

# 启用 AGENT API 与运行时
./scripts/run-local.sh --enable-agent
```

### 5. 典型运维路径与关键行为

推荐使用 `SMART START`（先确认接入与信号，再进入根因下钻）：

1. Scope：确定时间窗和影响范围。
2. Measure：先看控制面和 ingest 状态。
3. Analyze：查看趋势与异常信号。
4. Rank：定位高影响进程/工作负载。
5. Trace：沿数据路径/内核路径追踪根因。

常用接口：

```bash
curl -s "http://127.0.0.1:8080/api/v1/fleet/timeseries?window=30m&limit=180"
curl -s "http://127.0.0.1:8080/api/v1/top/programs?limit=30"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/ai-infra-stack"
```

关键行为说明：

- 非法遥测载荷会在 ingest 层被拒绝，不污染后续有效流。
- 诊断接口提供跨层证据并支持 RCA packet 导出。
- AGENT 执行链路默认受限，可通过审批令牌与 dry-run 控制风险。

### 6. UI 与接口分组

主要 API 分组：

- 基础状态：`/healthz`、`/api/v1/status`、`/api/v1/ingest/status`
- 集群与历史：`/api/v1/fleet`、`/api/v1/metrics/history`
- 排名与归因：`/api/v1/top/programs`
- 诊断：`/api/v1/diagnostics/*`
- 编排：`/api/v1/orchestration/*`
- AGENT：`/api/v1/agent/query`、`/api/v1/agent/execute`

界面示例：

![AI SRE Agent Dashboard](screenshot/screenshot_ui_dashboard_full.png)

![AI SRE Agent Data Path](screenshot/screenshot_ui_data_path_diagnostics.png)

### 7. 测试与调试工作流

本项目贯彻 Minimal & Focus 原则。测试套件已剔除低价值的“伪测试”（单纯的类型检查、无意义的 nil 检查），保留针对核心行为与错误路径的高优集成验证。

默认推荐入口：

```bash
make ci
```

分层稳定性工作流（重构/修复时推荐）：

```bash
make test-stability
```

按层运行：

```bash
# Go 全量（backend 模块），提供高信噪比的行为断言
cd backend && go test ./... -count=1 -v

# 外部集成（bufconn）
cd tests/integration && go test -v .

# E2E（依赖本地运行栈；不满足条件时会自动 skip）
cd tests/e2e && go test -v -tags=e2e .

# Python
python3 -m unittest discover -s tests/python -p 'test_*.py'

# Frontend
cd frontend && npm test -- --watch=false
```

测试结构说明见：`tests/README.md`。

### 8. 安全基线与检查流程

默认安全基线：

- `configs/controller.yaml` 默认绑定 `127.0.0.1`（HTTP/gRPC），避免无认证公网暴露。
- `docker-compose.yaml` 与 `deploy/docker-compose.yml` 默认启用 `read_only`、`no-new-privileges`、`cap_drop: [ALL]`，并将端口绑定到 loopback。
- Helm 默认值启用最小权限基线（`pod-security=baseline`、只读 RBAC 动词、`hostPID: false`）。

安全检查入口：

```bash
# 本地友好模式（缺失工具会 skip）
make security-scan

# 严格模式（CI 推荐，缺失工具或发现问题即失败）
SECURITY_SCAN_STRICT=1 make security-scan

# 仅运行内置运行时安全审计
make security-audit
```

结果输出：

- 扫描报告位于 `build/security/`
- 运行时审计输出 `runtime-audit.md` 与 `runtime-audit.json`
- 审计状态为 `pass/warn/fail`，检查编号为 `SEC-RUNTIME-001` ~ `SEC-RUNTIME-007`

更多细节见：`SECURITY.md`、`docs/security/threat-model.md`。

### 9. 关键文档

- 使用流程：`docs/operations/usage.md`
- 测试策略：`docs/operations/testing.md`
- 测试体系设计：`docs/operations/testing_strategy.md`
- RCA Playbook：`docs/operations/rca_playbook.md`
- RDMA/存储排障：`docs/operations/rdma_storage_playbook.md`

### 10. 许可证

本项目使用 MIT 许可证，详见 `LICENSE`。

---

## English Documentation

### 1. Overview

AI SRE Agent is a push-first observability and operations platform for Linux / GPU AI infrastructure. It is centered on two binaries:

- `sre-collector`: runs on monitored nodes, gathers host/process/GPU/log signals, and pushes batches upstream.
- `sre-controller`: validates and ingests telemetry, maintains in-memory fleet state, and exposes APIs, UI, and diagnostics.

The project focuses on three outcomes:

- Cross-layer observability across node, process, GPU, network, storage, and workload views.
- Fast incident triage with explicit diagnostic paths and root-cause drilldowns.
- Safe operational execution with guarded remediation semantics.

### 2. Core Capabilities

- Push-first + spool-first telemetry with retry/recovery behavior.
- Linux low-level signal collection (`/proc`, `/sys`, PSI, scheduler/TCP, disk/network metrics).
- Optional C++ probe-core fast path (`cpp/probe_core`) connected through protobuf IPC.
- GPU visibility (utilization, memory, thermal/power, health, per-process signals).
- Read-only Kubernetes view and inventory-aware multi-node operations.
- Diagnostics APIs: `data-path`, `kernel-path`, `root-cause`, `workload-path`, `ai-infra-stack`, `rca-packet`.
- Optional AGENT flow for natural-language RCA with guarded execution.

### 3. Architecture and Code Boundaries

```text
Monitored Host(s)
  └─ sre-collector
       ├─ optional probe-core IPC source
       ├─ collect host/process/gpu/log signals
       ├─ batch telemetry + local spool
       └─ push via gRPC

Control Plane
  └─ sre-controller
       ├─ ingest validation + memory store
       ├─ inventory/k8s/gpu aggregation
       ├─ diagnostics + orchestration modules
       └─ REST APIs + UI + /metrics
```

Primary directories:

- Collector: `backend/internal/collector`
- Probe collectors: `backend/internal/probe`
- Ingest store: `backend/internal/controller/ingest`
- Orchestration: `backend/internal/controller/orchestration`
- Analysis/diagnostics: `backend/internal/controller/analysis`
- Frontend UI: `frontend/src`
- Python runtime/analysis: `python/sre_agent`
- Cross-package test suites: `tests/`

### 4. Quick Start

#### 4.1 Prerequisites

- Go `1.25+`
- Node.js `18+` (frontend development/testing)
- Python `3.10+` (Python modules/tests)
- Linux recommended for full low-level signal coverage

#### 4.2 Run local stack

```bash
./scripts/run-local.sh
```

Health checks:

```bash
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s http://127.0.0.1:8080/api/v1/fleet
```

#### 4.3 Optional modes

```bash
# Local multi-node simulation
./scripts/run-local-multinode.sh --collectors 3

# Enable AGENT APIs/runtime
./scripts/run-local.sh --enable-agent
```

### 5. Operational Workflow and Key Behaviors

Recommended `SMART START` path (validate ingest first, then drill down):

1. Scope: define incident window and impact scope.
2. Measure: verify control-plane and ingest health.
3. Analyze: inspect trends and anomaly signals.
4. Rank: identify top offending processes/workloads.
5. Trace: follow data-path/kernel-path/root-cause links.

Common endpoints:

```bash
curl -s "http://127.0.0.1:8080/api/v1/fleet/timeseries?window=30m&limit=180"
curl -s "http://127.0.0.1:8080/api/v1/top/programs?limit=30"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/ai-infra-stack"
```

Key behaviors:

- Invalid telemetry is rejected at ingest and does not poison subsequent valid streams.
- Diagnostics APIs provide cross-layer evidence and support RCA packet export.
- AGENT execution is guarded by approval-token and dry-run controls.

### 6. UI and API Groups

Main API groups:

- Basic status: `/healthz`, `/api/v1/status`, `/api/v1/ingest/status`
- Fleet/history: `/api/v1/fleet`, `/api/v1/metrics/history`
- Ranking/attribution: `/api/v1/top/programs`
- Diagnostics: `/api/v1/diagnostics/*`
- Orchestration: `/api/v1/orchestration/*`
- AGENT: `/api/v1/agent/query`, `/api/v1/agent/execute`

UI examples:

![AI SRE Agent Dashboard](screenshot/screenshot_ui_dashboard_full.png)

![AI SRE Agent Data Path](screenshot/screenshot_ui_data_path_diagnostics.png)

### 7. Testing and Debugging Workflow

The project follows the "Minimal & Focus" principle. The Go test suite has been heavily pruned to remove low-value pseudo-tests (e.g. superficial type padding, meaningless nil-checks), retaining high-signal assertions that evaluate real behavioral flows and error edge cases.

Recommended default gate:

```bash
make ci
```

Layered stability workflow (recommended for refactor/debug loops):

```bash
make test-stability
```

Run by layer:

```bash
# Go full backend module tests (high signal-to-noise ratio)
cd backend && go test ./... -count=1 -v

# External integration (bufconn)
cd tests/integration && go test -v .

# E2E (requires local stack; auto-skips when preconditions are missing)
cd tests/e2e && go test -v -tags=e2e .

# Python
python3 -m unittest discover -s tests/python -p 'test_*.py'

# Frontend
cd frontend && npm test -- --watch=false
```

See `tests/README.md` for test suite layout.

### 8. Security Baseline and Checks

Default security baseline:

- `configs/controller.yaml` binds to `127.0.0.1` by default (HTTP/gRPC) to avoid unauthenticated public exposure.
- `docker-compose.yaml` and `deploy/docker-compose.yml` default to `read_only`, `no-new-privileges`, `cap_drop: [ALL]`, with loopback-only published ports.
- Helm defaults enforce least-privilege posture (`pod-security=baseline`, read-only RBAC verbs, `hostPID: false`).

Security entry points:

```bash
# Local-friendly mode (missing tools are skipped)
make security-scan

# Strict mode (recommended in CI; missing tools/findings fail the run)
SECURITY_SCAN_STRICT=1 make security-scan

# Built-in runtime security audit only
make security-audit
```

Outputs:

- Scan artifacts are written to `build/security/`
- Runtime audit emits `runtime-audit.md` and `runtime-audit.json`
- Audit statuses are `pass/warn/fail`, with checks `SEC-RUNTIME-001` to `SEC-RUNTIME-007`

See `SECURITY.md` and `docs/security/threat-model.md` for details.

### 9. Key Docs

- Usage workflow: `docs/operations/usage.md`
- Testing workflow: `docs/operations/testing.md`
- Testing architecture: `docs/operations/testing_strategy.md`
- RCA playbook: `docs/operations/rca_playbook.md`
- RDMA/storage playbook: `docs/operations/rdma_storage_playbook.md`

### 10. License

MIT License. See `LICENSE`.
