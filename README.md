# AI SRE Agent (v0.2)

AI SRE Agent is a push-first Linux observability platform.

AI SRE Agent 是一个基于 Push-first 模式的 Linux 可观测性平台。

## Architecture Overview / 架构概览

- `sre-collector` - Runs on each host, collects CPU/disk/NIC/GPU/process/log metrics, pushes to controller via gRPC / 运行在每个主机上，采集 CPU/磁盘/网卡/GPU/进程/日志 指标，通过 gRPC 批量推送到 controller
- `sre-controller` - Ingests telemetry, serves API/UI, exposes Prometheus metrics / 接收遥测数据，提供 API/UI 服务，并暴露 Prometheus 指标

Documentation / 文档导航：
- System Architecture / 系统架构：[`docs/design/architecture.md`](docs/design/architecture.md)
- Operations Manual / 运维手册：[`docs/operations/usage.md`](docs/operations/usage.md)

## Quick Start / 快速开始

### Prerequisites / 环境要求

- Go `1.25+`
- Linux host (recommended for full `/proc`, GPU, and eBPF coverage) / Linux 主机（推荐，以支持完整的 `/proc`、GPU 和 eBPF 采集）
- Node.js `18+` (only when modifying frontend / 仅修改前端时需要)
- Docker (optional, for containerized local runs / 可选，用于容器化本地运行)

### One-line Start / 一键启动

```bash
./scripts/run-local.sh
```

### Enable AI Agent / 启用 AI Agent

Enable AGENT query API (LLM + Playbook automation) / 启用 AGENT 查询 API（LLM + Playbook 自动化运维）：

```bash
./scripts/run-local.sh --enable-agent
```

Optional: create `configs/agent.env` to avoid setting env vars manually / 可选配置：创建 `configs/agent.env` 避免每次手动设置环境变量：

```bash
# 1. Copy config template / 复制配置模板
cp configs/agent.env.example configs/agent.env

# 2. Edit config file, add your API Key / 编辑配置文件，填入你的 API Key
# vim configs/agent.env

# 3. Start service / 启动服务
./scripts/run-local.sh --enable-agent
```

### Access URLs / 访问地址

After starting, you can access / 启动后可访问以下地址：

- `http://127.0.0.1:8080/` - Default UI / 默认 UI
- `http://127.0.0.1:8080/ui` - SPA interface (requires built frontend assets / 需先构建前端资源)
- `http://127.0.0.1:8080/api/v1/fleet` - Fleet host snapshot / 集群主机快照
- `http://127.0.0.1:8080/api/v1/top/programs?limit=50` - Process resource ranking / 进程资源排名
- `http://127.0.0.1:8080/metrics` - Prometheus metrics / Prometheus 指标

### Manual Build and Run / 手动编译运行

Build binaries / 编译二进制文件：

```bash
make build
```

Run controller / 运行 controller：

```bash
SRE_CONTROLLER_CONFIG=./configs/controller.yaml ./build/sre-controller
```

Run collector / 运行 collector：

```bash
SRE_COLLECTOR_CONFIG=./configs/collector.yaml ./build/sre-collector \
  --level 5 \
  --metrics-listen :9464
```

## Usage / 使用指南

### API Endpoints / API 接口一览

| Endpoint | Purpose / 用途 |
|---|---|
| `/api/v1/status` | Controller runtime status / Controller 运行状态 |
| `/api/v1/fleet` | Latest fleet telemetry snapshot / 集群主机最新遥测快照 |
| `/api/v1/fleet/{collector_id}` | Single host detailed snapshot / 单个主机详细快照 |
| `/api/v1/fleet/timeseries` | Host timeseries trends + anomaly detection / 主机时序趋势 + 异常检测 |
| `/api/v1/top/programs` | Cross-resource process ranking / 跨资源维度的进程排名 |
| `/api/v1/topology` | Fleet topology and hotspot analysis / 集群拓扑与热点分析 |
| `/api/v1/gpu/nodes` | GPU cluster view / GPU 集群视图 |
| `/api/v1/k8s/gpu/nodes` | Kubernetes-friendly GPU snapshot / Kubernetes 友好的 GPU 快照 |
| `/api/v1/agent/query` | Natural language query (RCA + suggestions) / 自然语言查询（根因分析 + 建议） |
| `/api/v1/agent/execute` | Execute suggested action (by action_id) / 执行建议的操作 |
| `/metrics` | Prometheus scrape endpoint / Prometheus 抓取端点 |
| `/healthz` | Health check endpoint / 健康检查端点 |

For complete API request/response format, see [`docs/reference/api.md`](docs/reference/api.md) / 完整 API 请求/响应格式请参考。

### Incident Triage Flow / 故障排查流程

Recommended troubleshooting sequence / 推荐值班排障顺序：

1. **Check Status / 检查状态** - Call `/api/v1/status` to verify controller health / 调用 `/api/v1/status` 确认 controller 健康状态
2. **View Trends / 查看趋势** - Check `/api/v1/fleet/timeseries` to determine if anomaly is spike or sustained drift / 检查 `/api/v1/fleet/timeseries` 判断异常是瞬时尖刺还是持续漂移
3. **Identify Process / 定位进程** - Use `/api/v1/top/programs` to find top processes by resource / 使用 `/api/v1/top/programs` 找出各资源维度的 Top 进程
4. **Assess Impact / 评估影响** - Use `/api/v1/topology` to assess blast radius / 使用 `/api/v1/topology` 评估影响范围

For detailed RCA, set collector depth / 为了获得更详细的根因分析，建议设置 collector 采集深度：

```bash
SRE_COLLECTOR_LEVEL=5
```

### Deployment Options / 部署方式

**Local Docker Compose / 本地 Docker Compose：**

```bash
docker compose up -d --build
curl -s http://127.0.0.1:8080/healthz
```

**Docker Scripts / Docker 脚本：**

```bash
./scripts/docker-run-stack.sh
./scripts/docker-stop-stack.sh
```

**Kubernetes Deployment / Kubernetes 部署：**

- Manifest directory / 清单目录：`deploy/k8s/push-first/`
- Read `deploy/k8s/push-first/README.md` before applying / 应用前请阅读

### Optional Features / 可选功能

- **eBPF sidecar collection / eBPF 侧车采集**：Configure collector `ebpf.*` settings / 配置 collector 的 `ebpf.*` 相关设置
- **External metrics helper (C++) / 外部指标助手**：Run `make build-proc-metrics` and set `SRE_COLLECTOR_EXT_METRICS_CMD`
- **Agent/analysis endpoints / Agent/分析端点**：Enable `analysis`/`agent` modules in controller config / 在 controller 配置中启用

### AI Agent Workflow / AGENT 工作流

AGENT module is isolated from core data collection, focused on intelligent analysis / AGENT 模块与核心数据采集隔离，专注于智能分析：

1. **Data Read / 数据读取**：Read data from in-memory snapshots (`/fleet`, timeseries, GPU nodes) / 从内存快照读取数据
2. **LLM Analysis / LLM 分析**：Build LLM context based on [`docs/reference/llm_schema.md`](docs/reference/llm_schema.md)
3. **Action Recommendation / 动作推荐**：Match and recommend safe actions from [`configs/agent_playbooks.yaml`](configs/agent_playbooks.yaml)

#### Environment Configuration / 环境配置

```bash
# Required: Enable LLM / 必需：启用 LLM
export SRE_AGENT_LLM_ENABLED=1

# LLM Provider selection / LLM 提供商选择
export SRE_AGENT_LLM_PROVIDER=openai    # or ollama/gemini
export SRE_AGENT_LLM_MODEL=gpt-4o-mini  # or other models
export SRE_AGENT_LLM_API_KEY=<your-key> # API Key

# Recommended: Enable dry-run mode first / 推荐：先开启 dry-run 模式测试
export SRE_AGENT_DRY_RUN=1
```

Or use config file (recommended) / 或使用配置文件方式（推荐）：

```bash
cp configs/agent.env.example configs/agent.env
# Edit configs/agent.env with your settings / 编辑配置文件
./scripts/run-local.sh --enable-agent
```

#### Supported LLM Providers / 支持的 LLM 提供商

| Provider | Config / 配置值 | Description / 说明 |
|---|---|---|
| OpenAI | `openai` | Requires API Key / 需 API Key |
| Ollama (local / 本地) | `ollama` | Requires `ollama serve` running locally / 需本地运行 ollama serve |
| Gemini | `gemini` | Requires Google API Key / 需 Google API Key |

#### Query and Execute / 查询与执行

**Natural Language Query / 自然语言查询：**

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/agent/query \
  -H 'Content-Type: application/json' \
  -d '{"query":"Analyze GPU utilization spike on fleet"}'
```

**Execute Suggested Action / 执行建议的操作：**

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/agent/execute \
  -H 'Content-Type: application/json' \
  -d '{"action_id":"<action-id>"}'
```

#### Playbook Configuration Example / Playbook 配置示例

Playbooks define trigger conditions and corresponding automated actions, located at `configs/agent_playbooks.yaml` / Playbook 定义了触发条件和对应的自动化动作：

```yaml
version: v1
playbooks:
  - id: high-gpu-sm
    summary: High GPU SM utilization / GPU SM 利用率持续飙升
    severity: P1
    conditions:
      - metric: node_gpu_utilization_sm_avg_percent
        op: ">="
        threshold: 90
    actions:
      - id: restart-hot-pod
        type: restart_pod
        namespace: ml-platform
        name: trainer-hot-0
        priority: P1
        safe: true
        description: Restart hottest GPU training pod / 重启最热的 GPU 训练 Pod
```

## Contributing / 开发指南

Adopt test-first workflow, keep docs in sync with code / 采用测试优先的工作流，保持文档与代码同步：

```bash
make fmt-check   # Code format check / 代码格式检查
make vet         # Static analysis / 静态分析
make test-all    # Run all tests / 运行所有测试
npm -C frontend test  # Frontend tests / 前端测试
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for contribution standards and PR flow / 贡献规范和 PR 流程详见。

## Documentation Map / 文档导航

| Doc / 文档 | Path / 路径 | Description / 说明 |
|---|---|---|
| System Architecture / 系统架构 | [`docs/design/architecture.md`](docs/design/architecture.md) | Overall architecture design / 整体架构设计 |
| GPU Observability / GPU 可观测性 | [`docs/design/gpu_observability.md`](docs/design/gpu_observability.md) | GPU monitoring design / GPU 监控设计 |
| Runtime Config / 运行时配置 | [`docs/operations/configuration.md`](docs/operations/configuration.md) | Configuration reference / 配置项详解 |
| Operations Manual / 运维手册 | [`docs/operations/usage.md`](docs/operations/usage.md) | Day-to-day operations / 日常运维操作 |
| Docker Guide / Docker 手册 | [`docs/operations/docker_run.md`](docs/operations/docker_run.md) | Docker deployment / Docker 部署指南 |
| API Reference / API 参考 | [`docs/reference/api.md`](docs/reference/api.md) | API documentation / API 接口文档 |
| Metrics Reference / 指标参考 | [`docs/reference/metrics.md`](docs/reference/metrics.md) | Metrics description / 指标说明 |
| LLM Schema | [`docs/reference/llm_schema.md`](docs/reference/llm_schema.md) | Agent analysis schema / Agent 分析模式 |
| Release Checklist / 发布检查 | [`docs/checklist.md`](docs/checklist.md) | Pre-release checklist / 发布前检查清单 |

## UI Preview / UI 界面预览

![AI SRE Agent UI](screenshot/screenshot_ui_dashboard_full.png)

---

## Quick Reference / 快速参考

### System Mode / 系统模式

The system uses **Push-first** mode by default: `sre-collector` actively pushes data to `sre-controller` / 系统默认采用 **Push-first** 模式：`sre-collector` 主动推送数据到 `sre-controller`。

### Quick Start / 快速启动

```bash
./scripts/run-local.sh
```

### Common Duty Endpoints / 常用值班接口

- `/api/v1/status` - Service status / 服务状态
- `/api/v1/fleet/timeseries` - Trend analysis / 趋势分析
- `/api/v1/top/programs` - Process ranking / 进程排名
- `/api/v1/topology` - Topology impact / 拓扑影响面

### Recommended Triage Sequence / 推荐排障顺序

Check status → Review trends → Identify processes → Assess impact / 先看状态 → 再看趋势 → 再定位进程 → 最后评估影响面
