# Usage

Operational guide for the current push-first runtime.

## 中文速用

面向当前 Push-first 运行时的中文操作说明。

### 运行链路

```text
collector -> gRPC ingest (:9090) -> controller -> in-memory store -> APIs/UI/metrics
```

### 前置条件

- Go `1.25+`
- 端口可用：`8080`（HTTP）和 `9090`（gRPC）
- 推荐 Linux 环境以获得完整采集信号
- 需要 GPU 指标时，确保存在 `nvidia-smi`

### 本地快速运行（中文）

```bash
./scripts/run-local.sh
```

常用访问地址：

- `http://127.0.0.1:8080/api/v1/fleet`
- `http://127.0.0.1:8080/api/v1/top/programs?limit=50`
- `http://127.0.0.1:8080/ui`（前端资源存在时）

### 手动运行（中文）

构建：

```bash
make build
```

启动 controller：

```bash
SRE_CONTROLLER_CONFIG=./configs/controller.yaml ./build/sre-controller
```

启动 collector：

```bash
SRE_COLLECTOR_CONFIG=./configs/collector.yaml ./build/sre-collector
```

### 数据质量建议（中文）

- 需要更完整的进程级资源归因，建议：`SRE_COLLECTOR_LEVEL=5`
- 需要日志排名，配置：`SRE_COLLECTOR_LOG_PATHS=/var/log/syslog,/var/log/messages`
- `Disk` 看累计活动规模，`Disk I/O` 看实时吞吐压力

### 常见排查（中文）

- `fleet` 为空：检查 collector 到 controller gRPC 连通性与 collector 日志
- API 正常但 UI 不显示：检查 controller 的 `web.path` 是否有前端构建产物
- GPU 缺失：在采集端确认 `nvidia-smi` 可用
- eBPF 缺失：检查 sidecar 是否写入配置的 socket 路径

---

## Mental model

```text
collector -> gRPC ingest (:9090) -> controller -> in-memory store -> APIs/UI/metrics
```

## Prerequisites

- Go `1.25+`
- Free ports: `8080` (HTTP) and `9090` (gRPC)
- Linux recommended for full collector signals
- NVIDIA driver tools (`nvidia-smi`) if you need GPU metrics

## Fast local run

```bash
./scripts/run-local.sh
```

Open:

- `http://127.0.0.1:8080/api/v1/fleet`
- `http://127.0.0.1:8080/api/v1/top/programs?limit=50`
- `http://127.0.0.1:8080/ui` (if frontend assets are present)

## Manual run

Build:

```bash
make build
```

Start controller:

```bash
SRE_CONTROLLER_CONFIG=./configs/controller.yaml ./build/sre-controller
```

Start collector:

```bash
SRE_COLLECTOR_CONFIG=./configs/collector.yaml ./build/sre-collector
```

## Multi-node deployment pattern

Controller host:

```bash
SRE_CONTROLLER_CONFIG=/etc/sre-controller/config.yaml \
SRE_CONTROLLER_HTTP_LISTEN=0.0.0.0:8080 \
SRE_CONTROLLER_GRPC_LISTEN=0.0.0.0:9090 \
./build/sre-controller
```

Each collector host:

```bash
SRE_COLLECTOR_CONFIG=/etc/sre-collector/config.yaml \
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=<controller_host>:9090 \
./build/sre-collector
```

## Ranking depth and data quality

For full per-process rankings (including deep resource attribution), use:

```bash
SRE_COLLECTOR_LEVEL=5
```

Also configure logs if you need logs ranking:

```bash
SRE_COLLECTOR_LOG_PATHS=/var/log/syslog,/var/log/messages
```

## Disk vs Disk I/O

- `Disk`: cumulative storage footprint/activity totals over time.
- `Disk I/O`: live throughput and syscall/event pressure.

Interpretation:

- “Who used the most overall?” -> check `Disk` totals.
- “Who is currently hottest?” -> check `Disk I/O` rates.

## Why rankings may be empty

### No GPU process rankings

- No active GPU processes, or
- `nvidia-smi` unavailable in collector runtime.

### No logs process rankings

- `SRE_COLLECTOR_LOG_PATHS` is not configured, or
- log lines cannot be mapped to process/service identities.

### No network/deep rankings

- Collector level is below `5`, so RCA-style per-process signals are sparse.

## Optional features

### eBPF sidecar reader

```bash
SRE_COLLECTOR_EBPF_ENABLED=1 \
SRE_COLLECTOR_EBPF_SOCKET_PATH=/var/run/sre_collector_ebpf.sock \
SRE_COLLECTOR_EBPF_CATEGORIES=sched,io,net,mem,gpu,security,syscall \
./build/sre-collector
```

### External metrics command

```bash
SRE_COLLECTOR_EXT_METRICS_CMD="./build/proc-metrics" \
SRE_COLLECTOR_EXT_METRICS_TIMEOUT=500ms \
./build/sre-collector
```

### LLM/RAG

```bash
SRE_AGENT_LLM_ENABLED=1 \
SRE_AGENT_LLM_PROVIDER=openai \
SRE_AGENT_LLM_MODEL=gpt-4o-mini \
SRE_AGENT_LLM_API_KEY=<key> \
SRE_AGENT_RAG_ENABLED=1 \
SRE_AGENT_RAG_PATHS="README.md,docs,configs" \
./build/sre-controller
```

## Agent runtime checks

Quick verification flow after enabling `agent.enabled=true`:

```bash
curl -s http://127.0.0.1:8080/api/v1/agent/status
curl -s http://127.0.0.1:8080/api/v1/agent/reports/latest?limit=3
curl -s http://127.0.0.1:8080/api/v1/agent/actions?limit=5
```

Action update contract:

- Endpoint: `PATCH /api/v1/agent/actions/{id}` (also accepts `POST`)
- Body must include at least one of `status` or `note`
- Allowed `status` values:
  `proposed`, `acknowledged`, `in_progress`, `completed`, `dismissed`, `accepted`, `rejected`, `canceled`

## Troubleshooting

- Fleet empty:
  - verify collector can reach controller gRPC endpoint.
  - check collector logs for transport/spool errors.
- API healthy but UI missing:
  - ensure web assets exist at controller `web.path`.
- GPU missing:
  - validate `nvidia-smi` on collector host/container.
- eBPF missing:
  - verify sidecar writes to configured socket path.
- Wrong version shown:
  - current expected version is `v0.1`.

## Related

- Config details: `docs/operations/configuration.md`
- API endpoints: `docs/reference/api.md`
