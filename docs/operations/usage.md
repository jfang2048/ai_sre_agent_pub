# Usage / 使用指南

Operational runbook for the push-first runtime (`sre-collector` -> `sre-controller`).

Push-first 运行时的运维手册（`sre-collector` -> `sre-controller`）。

If you only need the shortest path, use this order / 如果只需要最短路径，按此顺序：

1. Start stack / 启动服务
2. Validate core APIs and metrics / 验证核心 API 和指标
3. Run triage workflow (`status` -> `timeseries` -> `top/programs` -> `topology`) / 运行排障工作流

## 1. Mental model / 心智模型

```text
collector -> gRPC ingest (:9090) -> controller -> in-memory store -> APIs/UI/metrics
```

Why this matters / 为什么重要：

- Collector pushes telemetry; controller does not poll every host / Collector 主动推送遥测数据；Controller 不会轮询每个主机
- APIs are served from in-memory state for low-latency incident response / API 从内存状态提供服务，实现低延迟故障响应
- Optional modules (analysis/agent/incidents) do not block core ingest / 可选模块（analysis/agent/incidents）不会阻塞核心采集

## 2. Prerequisites / 前置条件

- Go `1.25+`
- Free ports / 空闲端口：`8080` (HTTP), `9090` (gRPC ingest / gRPC 采集)
- Linux recommended for full `/proc` + kernel signal coverage / 推荐使用 Linux 以获得完整的 `/proc` + 内核信号覆盖
- `nvidia-smi` available if GPU metrics are required / 如需 GPU 指标则需要 `nvidia-smi`

For Docker-only workflows, see `docs/operations/docker_run.md` / 仅 Docker 工作流请参考

## 3. Fast local start / 快速本地启动

### Core stack / 核心服务

```bash
./scripts/run-local.sh
```

### Core stack + AGENT / 核心服务 + AGENT

```bash
./scripts/run-local.sh --enable-agent
```

The script builds backend binaries and web assets first, then starts controller and collector / 该脚本首先构建后端二进制文件和 Web 资源，然后启动 controller 和 collector。

## 4. First 60-second validation / 首次 60 秒验证

Run these checks in order / 按顺序运行这些检查：

```bash
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/fleet
curl -s http://127.0.0.1:8080/metrics | head
```

Open / 打开：

- `http://127.0.0.1:8080/` - Default UI / 默认 UI
- `http://127.0.0.1:8080/ui` - SPA interface / SPA 界面
- `http://127.0.0.1:8080/api/v1/top/programs?limit=50` - Process ranking / 进程排名
- `http://127.0.0.1:8080/api/v1/topology` - Topology view / 拓扑视图

## 5. Incident triage workflow (recommended sequence) / 故障排查工作流（推荐顺序）

### Step 1: Controller health and scope / Controller 健康状态和范围

```bash
curl -s http://127.0.0.1:8080/api/v1/status
```

### Step 2: Trend shape (spike vs sustained drift) / 趋势形状（尖刺 vs 持续漂移）

```bash
curl -s "http://127.0.0.1:8080/api/v1/fleet/timeseries?collector_id=<collector>&window=1h&limit=360"
```

### Step 3: Process attribution / 进程归因

```bash
curl -s "http://127.0.0.1:8080/api/v1/top/programs?collector_id=<collector>&limit=30"
```

### Step 4: Blast radius / 影响范围

```bash
curl -s "http://127.0.0.1:8080/api/v1/topology?collector_id=<collector>"
```

Record these fields for handoff / 记录这些字段用于交接：

- `window` / 时间窗口
- `collector_id` / hostname / 收集器 ID / 主机名
- Top offenders (process + pid + value + share) / Top 违规者（进程 + PID + 值 + 占比）
- Whether impact is single-host or spreading / 影响是单主机还是扩散中

## 6. AGENT setup and usage / AGENT 设置和使用

AGENT can run with OpenAI or a local Ollama model / AGENT 可以使用 OpenAI 或本地 Ollama 模型运行。

Controller reads AGENT settings from environment variables at startup / Controller 在启动时从环境变量读取 AGENT 设置。

### 6.1 Exactly where to set API key/provider / 设置 API Key/提供商的正确位置

Recommended place: `configs/agent.env` / 推荐位置：`configs/agent.env`

Do this once / 执行一次：

```bash
cp configs/agent.env.example configs/agent.env
```

Then edit `configs/agent.env` and set only one provider / 然后编辑 `configs/agent.env` 并仅设置一个提供商：

**OpenAI:**
- `SRE_AGENT_LLM_PROVIDER=openai`
- `SRE_AGENT_LLM_MODEL=gpt-4o-mini`
- `SRE_AGENT_LLM_API_KEY=<your-openai-key>`

**Ollama (local / 本地):**
- `SRE_AGENT_LLM_PROVIDER=ollama`
- `SRE_AGENT_LLM_MODEL=llama3.1:8b`
- `SRE_AGENT_LLM_BASE_URL=http://127.0.0.1:11434/v1`
- `SRE_AGENT_LLM_API_KEY=` (empty is fine for local / 本地可以为空)

Start with env file loaded automatically / 自动加载环境文件启动：

```bash
./scripts/run-local.sh --enable-agent
```

Use a non-default env file path / 使用非默认环境文件路径：

```bash
./scripts/run-local.sh --enable-agent --agent-env /path/to/agent.env
```

### 6.2 OpenAI quick env (without file) / OpenAI 快速环境配置（无需文件）

```bash
SRE_AGENT_LLM_ENABLED=1 \
SRE_AGENT_LLM_PROVIDER=openai \
SRE_AGENT_LLM_MODEL=gpt-4o-mini \
SRE_AGENT_LLM_API_KEY=<key> \
SRE_AGENT_DRY_RUN=1 \
./scripts/run-local.sh --enable-agent
```

### 6.3 Ollama quick env (without file) / Ollama 快速环境配置（无需文件）

Start local model service first / 首先启动本地模型服务：

```bash
ollama serve
ollama pull llama3.1:8b
```

Then / 然后：

```bash
SRE_AGENT_LLM_ENABLED=1 \
SRE_AGENT_LLM_PROVIDER=ollama \
SRE_AGENT_LLM_MODEL=llama3.1:8b \
SRE_AGENT_LLM_BASE_URL=http://127.0.0.1:11434/v1 \
SRE_AGENT_DRY_RUN=1 \
./scripts/run-local.sh --enable-agent
```

### 6.4 AGENT runtime checks / AGENT 运行时检查

```bash
curl -s http://127.0.0.1:8080/api/v1/agent/status
curl -s http://127.0.0.1:8080/api/v1/agent/reports/latest?limit=3
curl -s http://127.0.0.1:8080/api/v1/agent/actions?limit=5
```

NL query + guarded execution / 自然语言查询 + 受保护执行：

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/agent/query \
  -H 'Content-Type: application/json' \
  -d '{"query":"Analyze GPU SM utilization spike on node-a"}'

curl -s -X POST http://127.0.0.1:8080/api/v1/agent/execute \
  -H 'Content-Type: application/json' \
  -d '{"action_id":"<action-id>","dry_run":true}'
```

Safe execution guidance / 安全执行指南：

- Keep `SRE_AGENT_DRY_RUN=1` unless you explicitly want runtime mutation / 除非明确需要运行时变更，否则保持 `SRE_AGENT_DRY_RUN=1`
- Execute safe, reversible actions first / 首先执行安全、可逆的操作
- Validate post-action impact through `timeseries`, `top/programs`, and `/metrics` / 通过 `timeseries`、`top/programs` 和 `/metrics` 验证操作后影响

## 7. Data quality and signal depth / 数据质量和信号深度

### 7.1 Ranking depth / 排名深度

For full per-process attribution / 获得完整的每进程归因：

```bash
SRE_COLLECTOR_LEVEL=5
```

### 7.2 Logs ranking quality / 日志排名质量

```bash
SRE_COLLECTOR_LOG_PATHS=/var/log/syslog,/var/log/messages
```

### 7.3 Disk vs Disk I/O interpretation / Disk 与 Disk I/O 解读

- `disk`: cumulative storage footprint/activity totals / 累计存储足迹/活动总量
- `disk_io`: live throughput and syscall/event pressure / 实时吞吐和 _syscall_ /事件压力

Use / 使用：

- `disk` for "who consumed the most over time" / "谁消耗最多"
- `disk_io` for "who is hottest right now" / "谁当前最热"

### 7.4 Why some rankings are empty / 为什么某些排名为空

- GPU ranking empty: no active GPU workload or no `nvidia-smi` / GPU 排名为空：没有活跃 GPU 工作负载或没有 `nvidia-smi`
- Logs ranking empty: `SRE_COLLECTOR_LOG_PATHS` not set or no mappable process labels / 日志排名为空：未设置 `SRE_COLLECTOR_LOG_PATHS` 或没有可映射的进程标签
- Deep network/resource ranking sparse: collector level below `5` / 深度网络/资源排名稀疏：collector 级别低于 `5`

## 8. UI reading strategy / UI 阅读策略

Use views together, not in isolation / 结合使用视图，而非孤立查看：

- `Dashboard`: exact current numeric state / 精确的当前数值状态
- `Metric Trends`: shape and anomaly timing / 形状和异常时间
- `Per-Process Resource Breakdown`: offender list with value/share / 违规者列表及值/占比

Suggested pattern / 建议模式：

1. Detect symptom on `Dashboard` / 在 `Dashboard` 上检测症状
2. Confirm trend behavior on `Metric Trends` / 在 `Metric Trends` 上确认趋势行为
3. Pivot to ranked processes for ownership and action / 转到排名进程以确定责任和采取行动

## 9. Manual and multi-node startup / 手动和多节点启动

### 9.1 Manual local startup / 手动本地启动

Build / 构建：

```bash
make build
```

Controller / 控制器：

```bash
SRE_CONTROLLER_CONFIG=./configs/controller.yaml ./build/sre-controller
```

Collector / 采集器：

```bash
SRE_COLLECTOR_CONFIG=./configs/collector.yaml ./build/sre-collector
```

### 9.2 Multi-node pattern / 多节点模式

Controller host / Controller 主机：

```bash
SRE_CONTROLLER_CONFIG=/etc/sre-controller/config.yaml \
SRE_CONTROLLER_HTTP_LISTEN=0.0.0.0:8080 \
SRE_CONTROLLER_GRPC_LISTEN=0.0.0.0:9090 \
./build/sre-controller
```

Collector hosts / Collector 主机：

```bash
SRE_COLLECTOR_CONFIG=/etc/sre-collector/config.yaml \
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=<controller_host>:9090 \
./build/sre-collector
```

## 10. Optional capabilities / 可选功能

### eBPF sidecar reader / eBPF 侧车读取器

```bash
SRE_COLLECTOR_EBPF_ENABLED=1 \
SRE_COLLECTOR_EBPF_SOCKET_PATH=/var/run/sre_collector_ebpf.sock \
SRE_COLLECTOR_EBPF_CATEGORIES=sched,io,net,mem,gpu,security,syscall \
./build/sre-collector
```

### External metrics command bridge / 外部指标命令桥接

```bash
SRE_COLLECTOR_EXT_METRICS_CMD="./build/proc-metrics" \
SRE_COLLECTOR_EXT_METRICS_TIMEOUT=500ms \
./build/sre-collector
```

## 11. Troubleshooting map / 故障排查地图

### `fleet` empty / `fleet` 为空

- Check collector -> controller gRPC reachability / 检查 collector -> controller gRPC 连通性
- Check collector logs for transport/spool errors / 检查 collector 日志中的传输/队列错误
- Check controller `grpc_listen` value and port conflicts / 检查 controller `grpc_listen` 值和端口冲突

### UI blank or missing data / UI 空白或缺失数据

- Ensure controller can serve valid web assets at `web.path` / 确保 controller 可以在 `web.path` 提供有效的 Web 资源
- Rebuild frontend assets: `npm -C frontend run build` / 重新构建前端资源
- Reload and verify `/api/v1/fleet` returns data / 重新加载并验证 `/api/v1/fleet` 返回数据

### GPU data missing / GPU 数据缺失

- Confirm `nvidia-smi` works on collector host / 确认 `nvidia-smi` 在 collector 主机上可用
- Confirm GPU workload is active and collector is pushing / 确认 GPU 工作负载活跃且 collector 正在推送

### eBPF data missing / eBPF 数据缺失

- Confirm sidecar socket path matches collector config / 确认侧车 socket 路径与 collector 配置匹配
- Confirm configured categories and sidecar permissions / 确认配置的类别和侧车权限

### AGENT not using expected provider / AGENT 未使用预期的提供商

- Check `provider`, `model`, `used_fallback` fields in `/api/v1/agent/query` response / 检查 `/api/v1/agent/query` 响应中的 `provider`、`model`、`used_fallback` 字段
- Verify env values seen by controller process / 验证 controller 进程看到的环境变量值

## 12. Related docs / 相关文档

- Config reference: `docs/operations/configuration.md` / 配置参考
- API contract: `docs/reference/api.md` / API 契约
- Architecture: `docs/design/architecture.md` / 架构
- GPU design: `docs/design/gpu_observability.md` / GPU 设计

---

## Chinese Quick Reference / 中文速查

### Shortest path / 最短路径

1. Start / 启动：`./scripts/run-local.sh`
2. Validate / 校验：`/healthz`、`/api/v1/status`、`/api/v1/fleet`
3. Triage order / 排障顺序：`status -> fleet/timeseries -> top/programs -> topology`

### AGENT configuration / AGENT 配置

Recommended (one-time setup / 推荐方式（一次配置））：

```bash
cp configs/agent.env.example configs/agent.env
# Edit / 编辑后：
./scripts/run-local.sh --enable-agent
```

### Duty interpretation template / 值班判读模板

- Symptom / 现象：`<资源>`在`<时间段>`出现`<尖刺/持续增长>`
- Evidence / 证据：`数值` + `趋势` + `Top进程`
- Impact scope / 影响范围：`单机/多机`
- Conclusion / 结论：`疑似根因`
- Action / 动作：`限流/重启/扩容/回滚`
