# Usage / 使用指南

Operational runbook for the push-first runtime (`sre-collector` -> `sre-controller`).

Push-first 运行时的运维手册（`sre-collector` -> `sre-controller`）。

If you only need the shortest path, use this order / 如果只需要最短路径，按此顺序：

1. Start stack / 启动服务
2. Validate core APIs and metrics / 验证核心 API 和指标
3. Run triage workflow (`status` -> `timeseries` -> `top/programs` -> `topology`) / 运行排障工作流
4. Correlate cross-layer bottlenecks via `diagnostics/data-path` and drill down to trends/process ranking / 使用 `diagnostics/data-path` 做跨层关联并下钻到趋势/进程

## SMART START principle / SMART START 原则

Use SMART START as the default incident loop / 将 SMART START 作为默认故障处置循环：

| Letter | Action | Primary endpoint/view |
|---|---|---|
| `S` | Scope incident window and impacted collectors/services / 定义故障时间窗与影响范围 | `/api/v1/status` |
| `M` | Measure control-plane and ingest health first / 先测量控制面与接入健康 | `/api/v1/ingest/status`, `/metrics` |
| `A` | Analyze trend shape (spike vs sustained drift) / 分析趋势形态（尖刺或持续漂移） | `/api/v1/fleet/timeseries` |
| `R` | Rank top offenders by process/workload / 按进程/工作负载排名责任源 | `/api/v1/top/programs`, `/api/v1/k8s/workloads/top` |
| `T` | Trace cross-layer bottlenecks in data path / 沿数据路径跟踪跨层瓶颈 | `/api/v1/diagnostics/data-path`, `/api/v1/diagnostics/kernel-path`, `/api/v1/diagnostics/root-cause`, `/api/v1/diagnostics/workload-path`, `/api/v1/diagnostics/ai-infra-stack`, `Metric Trends` |
| `S` | Validate storage pressure and hot devices/mounts / 验证存储压力与热点设备/挂载 | `/api/v1/fleet/<collector>` |
| `T` | Check topology and blast radius propagation / 检查拓扑与影响扩散 | `/api/v1/topology`, `/api/v1/k8s/nodes/top` |
| `A` | Apply safest reversible action / 执行最安全可逆动作 | AGENT dry-run / runbook actions |
| `R` | Re-measure with same metrics for proof / 用同一指标复测确认效果 | `timeseries + top/programs + /metrics` |
| `T` | Transfer handoff with structured evidence / 用结构化证据交接 | Incident template in this doc |

SMART START command scaffold / SMART START 命令脚手架：

```bash
# S + M
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s http://127.0.0.1:8080/metrics | head
curl -s http://127.0.0.1:8080/metrics | grep -E "collector_probe_source|collector_probe_core_fresh" | head

# A + R
curl -s "http://127.0.0.1:8080/api/v1/fleet/timeseries?collector_id=<collector>&window=1h&limit=360"
curl -s "http://127.0.0.1:8080/api/v1/top/programs?collector_id=<collector>&limit=30"
curl -s "http://127.0.0.1:8080/api/v1/k8s/workloads/top?metric=pressure&limit=30"

# T + S + T
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path?collector_id=<collector>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path?collector_id=<collector>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause?collector_id=<collector>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path?cluster=<cluster>&namespace=<ns>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/ai-infra-stack?collector_id=<collector>&cluster=<cluster>&namespace=<ns>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/rca-packet?collector_id=<collector>&cluster=<cluster>&namespace=<ns>&sort_key=severity&sort_direction=desc"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/rca-packet?collector_id=<collector>&cluster=<cluster>&namespace=<ns>&format=markdown&download=1" -o rca_packet.md
curl -s "http://127.0.0.1:8080/api/v1/fleet/<collector>"
curl -s "http://127.0.0.1:8080/api/v1/topology?collector_id=<collector>"
curl -s "http://127.0.0.1:8080/api/v1/k8s/nodes/top?metric=pressure&limit=20"
```

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

### Multi-node local stack / 本地多节点栈

```bash
./scripts/run-local-multinode.sh --collectors 3
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
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s http://127.0.0.1:8080/api/v1/inventory/probes
curl -s http://127.0.0.1:8080/api/v1/fleet
curl -s http://127.0.0.1:8080/metrics | head
```

Open / 打开：

- `http://127.0.0.1:8080/` - Default UI / 默认 UI
- `http://127.0.0.1:8080/ui` - SPA interface / SPA 界面
- `http://127.0.0.1:8080/api/v1/inventory/probes` - Probe inventory / 探针清单
- `http://127.0.0.1:8080/api/v1/k8s/clusters` - K8s cluster snapshots / K8s 集群快照
- `http://127.0.0.1:8080/api/v1/top/programs?limit=50` - Process ranking / 进程排名
- `http://127.0.0.1:8080/api/v1/topology` - Topology view / 拓扑视图

UI regression quick check / UI 回归快速检查：

```bash
cd frontend && npm run test
```

## 5. SMART START execution workflow / SMART START 执行流程

### `S` Scope incident and confirm controller reachability / 定义故障范围并确认控制面可达

```bash
curl -s http://127.0.0.1:8080/api/v1/status
```

Capture `collector_id`, hostnames, and incident window / 记录 `collector_id`、主机名和故障时间窗。

### `M` Measure ingest and control-plane quality / 测量接入与控制面质量

```bash
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s http://127.0.0.1:8080/metrics | head
```

If C++ probe-core is enabled, confirm probe freshness and source selection:

若启用了 C++ probe-core，请确认采集源与新鲜度：

```bash
curl -s http://127.0.0.1:8080/metrics | grep -E "collector_probe_source|collector_probe_core_last_frame_age_seconds|collector_probe_core_fresh" | head -n 20
```

### `A` Analyze trend shape / 分析趋势形态

```bash
curl -s "http://127.0.0.1:8080/api/v1/fleet/timeseries?collector_id=<collector>&window=1h&limit=360"
```

### `R` Rank process and workload ownership / 排名进程与工作负载责任源

```bash
curl -s "http://127.0.0.1:8080/api/v1/top/programs?collector_id=<collector>&limit=30"
curl -s "http://127.0.0.1:8080/api/v1/k8s/workloads/top?metric=pressure&limit=30"
```

### `T` Trace cross-layer bottlenecks / 跟踪跨层瓶颈

```bash
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path?collector_id=<collector>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path?collector_id=<collector>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause?collector_id=<collector>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path?cluster=<cluster>&namespace=<ns>"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/ai-infra-stack?collector_id=<collector>&cluster=<cluster>&namespace=<ns>"
```

For UI workflow / UI 工作流：

- Open `Data Path Diagnostics` page / 打开数据路径诊断页
- Review `AI Infra Stack Layers` first to confirm measured vs missing capability coverage before deep triage / 先看 `AI Infra Stack Layers` 明确能力覆盖和缺口，再深入排查
- Use `Layer Domain Decomposition` to narrow each layer into practical subdomains (for example inter-node fabric vs collective runtime, or error budget vs recovery) before choosing actions / 使用 `Layer Domain Decomposition` 将层级拆到可执行子域（如互联网络 vs 集合通信、误差预算 vs 恢复链路）再决定动作
- Use `Open trends` directly on domain rows to jump from architecture-level domain risk into metric curves without losing context / 在子域行直接点击 `Open trends`，可从架构级风险直接跳转到指标曲线且保留上下文
- In `communication_fabric`, prioritize direct process evidence (`net_queued_bytes`, `net_connections`, `sched_wait_ratio` on collective workers) before relying on proxy-only conclusions / 在 `communication_fabric` 层先看进程级直接证据（集合通信进程的 `net_queued_bytes`、`net_connections`、`sched_wait_ratio`），再使用代理信号下结论
- Use `Incident → Workload → Placement Drilldowns` to walk one finding into affected jobs, placement nodes, and contention evidence in one view / 使用 `Incident → Workload → Placement Drilldowns` 将单条根因串联到受影响作业、放置节点和争用证据
- In incident drilldowns, use `Open contention trend` to jump straight from contention evidence to trend curves (especially for collective-runtime process queueing signals) / 在事件钻取中可使用 `Open contention trend` 从争用证据直接跳到趋势曲线（尤其适用于集合通信进程队列信号）
- Treat `partial` as a concrete coverage gap (scope has mixed signal availability), not as healthy telemetry / 将 `partial` 视为明确覆盖缺口（范围内仅部分有信号），不要按健康信号处理
- In measurement snapshots, treat `proxy` methods as directional hints and prioritize `direct`/`derived` methods when deciding mitigations / 在测量快照中将 `proxy` 作为方向性提示；执行修复决策时优先依据 `direct`/`derived` 信号
- In `orchestration_runtime`, check `tenant_fairness_index` and `tenant_top_share` before declaring a scheduler issue resolved / 在 `orchestration_runtime` 中先看 `tenant_fairness_index` 与 `tenant_top_share`，再判定调度问题是否闭环
- In `compute_virtualization`, review `gpu_slice_density` and `gpu_partition_assignments` when diagnosing shared-device contention / 在 `compute_virtualization` 中排查共享设备争用时，关注 `gpu_slice_density` 与 `gpu_partition_assignments`
- In `reliability_sre`, use `error_budget_remaining`, `mttd_proxy_seconds`, and `mttr_proxy_seconds` as operational proxies, then confirm with incident records / 在 `reliability_sre` 中使用 `error_budget_remaining`、`mttd_proxy_seconds`、`mttr_proxy_seconds` 作为运行时代理，再与事件记录交叉确认
- Use `SRE Reliability Snapshot` in the same page to read availability/latency SLI, error budget, and MTTD/MTTR proxy at a glance before drilling into findings / 在同页先看 `SRE Reliability Snapshot`（可用性/延迟 SLI、误差预算、MTTD/MTTR 代理）再深入根因条目
- Use `Per-Process Resource Breakdown` in the same page to rank ownership across `cpu/memory/network/disk_io/disk/gpu/logs` before remediation / 在同页使用 `Per-Process Resource Breakdown` 对 `cpu/memory/network/disk_io/disk/gpu/logs` 做责任排序后再执行修复
- In `Kubernetes Workload Path Diagnostics`, set `cluster/namespace/service` filters to narrow blast radius / 在 `Kubernetes Workload Path Diagnostics` 中设置 `cluster/namespace/service` 过滤条件以缩小影响范围
- Adjust workload sorting (`severity/overall/coverage/network/storage`, ascending/descending) to prioritize investigation order; sort state is persisted in URL for exact replay / 调整工作负载排序（`severity/overall/coverage/network/storage` 与升序/降序）以确定优先排查顺序；排序状态会写入 URL 以便精确复现
- Use `Copy scope link` to share exact workload filter + sort context in incident handoff / 使用 `Copy scope link` 在故障交接中共享精确过滤与排序上下文
- Use `Copy handoff markdown` to export current workload-path evidence into incident notes / 使用 `Copy handoff markdown` 导出当前工作负载路径证据到故障记录
- Use `Copy RCA packet` to export root-cause findings + workload-path evidence as one structured handoff artifact / 使用 `Copy RCA packet` 一次导出根因结论与工作负载路径证据
- Use `Download RCA packet` when incident workflow needs an attached markdown artifact (file includes root-cause summary, kernel-path snapshot, and resource rankings) / 需要附件时使用 `Download RCA packet` 下载 Markdown 工件（文件包含根因摘要、内核路径快照和资源排名）
- For API-first automation, call `/diagnostics/rca-packet` and store `file_name + markdown + packet_sha256` as immutable incident evidence / 对于 API 自动化流程，调用 `/diagnostics/rca-packet` 并将 `file_name + markdown + packet_sha256` 存档为不可变故障证据
- If `SRE_RCA_PACKET_SIGNING_KEY` is configured, also store `packet_signature + packet_signature_key_id` for tamper-evident handoff audit / 若配置 `SRE_RCA_PACKET_SIGNING_KEY`，同时存档 `packet_signature + packet_signature_key_id` 以支持防篡改审计
- Use `Show details` on a workload row to inspect node-level mapped telemetry and stage bottlenecks / 在工作负载行点击 `Show details` 查看节点级映射遥测与分层瓶颈
- Click `Open trends` on anomaly/pressure rows / 在异常或压力行点击 `Open trends`
- Verify focused curve and per-process breakdown in `Metric Trends` / 在趋势页验证聚焦曲线和进程分解
- Use `/diagnostics/kernel-path` for stage-level Linux stack bottlenecks before mitigation / 执行修复前先看 `/diagnostics/kernel-path` 的 Linux 栈分层瓶颈
- Use `/diagnostics/root-cause` findings as ordered hypotheses before mitigation / 在执行修复前，先使用 `/diagnostics/root-cause` 的有序假设
- In `Cross-Layer Root Cause Findings`, use each finding card’s `Open trends` button to jump directly into the strongest mapped metric for that finding / 在 `Cross-Layer Root Cause Findings` 中可直接点击每个 finding 卡片的 `Open trends`，跳转到该 finding 最相关的指标
- Use `/diagnostics/workload-path` to confirm which workloads/services are spreading pressure across nodes / 使用 `/diagnostics/workload-path` 确认哪些工作负载/服务正在跨节点扩散压力
- If `scheduler_contention_tail_latency` appears, validate `cpu_iowait_percent`, `cpu_pressure_some_avg10`, `procs_running`, and `procs_blocked` in the same window / 若出现 `scheduler_contention_tail_latency`，请在同一时间窗核对 `cpu_iowait_percent`、`cpu_pressure_some_avg10`、`procs_running`、`procs_blocked`
- If `collective_runtime_queueing_contention` appears, inspect process-level `net_queued_bytes`, `net_connections`, and `sched_wait_ratio` before changing collective runtime knobs / 若出现 `collective_runtime_queueing_contention`，先检查进程级 `net_queued_bytes`、`net_connections`、`sched_wait_ratio`，再调整集合通信运行时参数

### `S` Validate storage hotspots and queue pressure / 验证存储热点与队列压力

```bash
curl -s "http://127.0.0.1:8080/api/v1/fleet/<collector>"
```

Read `storage_devices`, `storage_partitions`, `filesystems` for hottest disks, busiest partitions, and mount pressure / 通过 `storage_devices`、`storage_partitions`、`filesystems` 查看最热磁盘、最忙分区和挂载压力。

### `T` Check topology blast radius / 检查拓扑影响范围

```bash
curl -s "http://127.0.0.1:8080/api/v1/topology?collector_id=<collector>"
curl -s "http://127.0.0.1:8080/api/v1/k8s/nodes/top?metric=pressure&limit=20"
```

### `A` Apply safe action first / 先执行安全可逆动作

- Keep `SRE_AGENT_DRY_RUN=1` until evidence is complete / 在证据完整前保持 `SRE_AGENT_DRY_RUN=1`
- Execute the smallest reversible action first / 优先执行最小可逆动作

### `R` Re-measure impact / 复测动作影响

Re-run `timeseries`, `top/programs`, and `/metrics` with the same window / 使用相同时间窗重新执行 `timeseries`、`top/programs` 与 `/metrics`。

### `T` Transfer handoff evidence / 交接结构化证据

Record these fields for handoff / 记录这些字段用于交接：

- `window` / 时间窗口
- `collector_id` / hostname / 收集器 ID / 主机名
- Top offenders (process + pid + value + share) / Top 违规者（进程 + PID + 值 + 占比）
- Whether impact is single-host or spreading / 影响是单主机还是扩散中
- Action taken + post-action metrics deltas / 已执行动作 + 动作后指标变化
- Attach `Copy RCA packet` output or uploaded `Download RCA packet` file in incident notes (includes scope link, ranked findings, kernel/resource snapshot, and workload evidence) / 在故障记录中附上 `Copy RCA packet` 输出或上传 `Download RCA packet` 文件（包含范围链接、排序结论、内核/资源快照和工作负载证据）

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
QUERY_JSON=$(curl -s -X POST http://127.0.0.1:8080/api/v1/agent/query \
  -H 'Content-Type: application/json' \
  -d '{"query":"Analyze GPU SM utilization spike on node-a"}')

echo "$QUERY_JSON" | jq '.explainability'

ACTION_ID=$(echo "$QUERY_JSON" | jq -r '.actions[0].id')
APPROVAL_TOKEN=$(echo "$QUERY_JSON" | jq -r '.actions[0].approval_token')

curl -s -X POST http://127.0.0.1:8080/api/v1/agent/execute \
  -H 'Content-Type: application/json' \
  -d "{\"action_id\":\"${ACTION_ID}\",\"approval_token\":\"${APPROVAL_TOKEN}\"}"
```

Safe execution guidance / 安全执行指南：

- Keep `SRE_AGENT_DRY_RUN=1` unless you explicitly want runtime mutation / 除非明确需要运行时变更，否则保持 `SRE_AGENT_DRY_RUN=1`
- Keep `SRE_AGENT_REQUIRE_APPROVAL_TOKEN=1` for production mutation paths / 生产变更路径建议保持 `SRE_AGENT_REQUIRE_APPROVAL_TOKEN=1`
- Set `SRE_AGENT_MAX_CONCURRENT_QUERIES` to a bounded value to protect controller capacity / 设置有界 `SRE_AGENT_MAX_CONCURRENT_QUERIES` 以保护 controller 容量
- Keep `SRE_AGENT_MAX_TELEMETRY_AGE` bounded (for example `2m`) and default to blocking actions on stale data / 保持有界 `SRE_AGENT_MAX_TELEMETRY_AGE`（例如 `2m`），默认在 stale 数据上阻断动作
- Keep `SRE_AGENT_SKIP_LLM_ON_STALE_TELEMETRY=1` and `SRE_AGENT_SKIP_LLM_ON_NO_TELEMETRY=1` for deterministic degraded behavior / 建议保持 `SRE_AGENT_SKIP_LLM_ON_STALE_TELEMETRY=1` 与 `SRE_AGENT_SKIP_LLM_ON_NO_TELEMETRY=1` 以获得确定性降级行为
- Configure `SRE_AGENT_EVENT_WEBHOOK_URL` (and optional token/timeout) for generic async write-back integration signals / 使用通用异步回写集成信号时配置 `SRE_AGENT_EVENT_WEBHOOK_URL`（及可选 token/timeout）
- Configure `SRE_AGENT_EVENT_SLACK_WEBHOOK_URL` and/or `SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY` for native sink adapters / 使用原生 sink 适配器时配置 `SRE_AGENT_EVENT_SLACK_WEBHOOK_URL` 和/或 `SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY`
- Keep bounded retry settings (`SRE_AGENT_EVENT_PUBLISH_RETRIES`, `SRE_AGENT_EVENT_RETRY_BACKOFF`) to avoid noisy downstream systems / 保持有界重试设置（`SRE_AGENT_EVENT_PUBLISH_RETRIES`、`SRE_AGENT_EVENT_RETRY_BACKOFF`）避免下游系统噪声
- Execute safe, reversible actions first / 首先执行安全、可逆的操作
- Validate post-action impact through `timeseries`, `top/programs`, and `/metrics` / 通过 `timeseries`、`top/programs` 和 `/metrics` 验证操作后影响

## 7. Data quality and signal depth / 数据质量和信号深度

### 7.1 Ranking depth / 排名深度

For full per-process attribution / 获得完整的每进程归因：

```bash
SRE_COLLECTOR_LEVEL=5
```

When `probe_core` is enabled, `/api/v1/top/programs` also carries scheduler and block-I/O contention fields:

- `sched_wait_ratio`, `sched_wait_seconds_total`, `sched_run_seconds_total`
- `block_io_delay_seconds_total`, `block_io_delay_seconds_per_second`

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

- Check `provider`, `model`, `used_fallback`, and `explainability` fields in `/api/v1/agent/query` response / 检查 `/api/v1/agent/query` 响应中的 `provider`、`model`、`used_fallback`、`explainability` 字段
- Check `fallback_reason`, `actions_suppressed`, and `suppression_reason` for policy/debug context / 检查 `fallback_reason`、`actions_suppressed`、`suppression_reason` 获取策略/调试上下文
- Verify env values seen by controller process / 验证 controller 进程看到的环境变量值

### AGENT query returns 503 / AGENT 查询返回 503

- Check whether `agent_queries_busy_rejected_total` is increasing in `/metrics` / 检查 `/metrics` 中 `agent_queries_busy_rejected_total` 是否持续增长
- Tune `SRE_AGENT_MAX_CONCURRENT_QUERIES` with observed CPU/latency budget / 结合 CPU/延迟预算调优 `SRE_AGENT_MAX_CONCURRENT_QUERIES`

### AGENT query returns stale telemetry limitations / AGENT 查询出现 stale 遥测限制

- Check `explainability.data_coverage.telemetry_stale` and `telemetry_age_seconds` / 检查 `explainability.data_coverage.telemetry_stale` 与 `telemetry_age_seconds`
- Check `agent_queries_stale_telemetry_total` trend in `/metrics` / 检查 `/metrics` 中 `agent_queries_stale_telemetry_total` 趋势
- Check `agent_llm_bypassed_stale_total` and `agent_llm_bypassed_empty_total` for bypass frequency / 检查 `agent_llm_bypassed_stale_total` 与 `agent_llm_bypassed_empty_total` 评估绕过频率
- Check `agent_actions_suppressed_total` when actions are intentionally withheld / 当动作被策略抑制时检查 `agent_actions_suppressed_total`
- Reduce ingest lag or adjust `SRE_AGENT_MAX_TELEMETRY_AGE` carefully / 降低接入延迟或谨慎调整 `SRE_AGENT_MAX_TELEMETRY_AGE`

### AGENT webhook events not arriving / AGENT webhook 事件未送达

- Check `SRE_AGENT_EVENT_WEBHOOK_URL` and optional auth token values in controller environment / 检查 controller 环境中的 `SRE_AGENT_EVENT_WEBHOOK_URL` 与可选认证 token
- Check `agent_events_publish_fail_total` and `agent_events_published_total` trend in `/metrics` / 检查 `/metrics` 中 `agent_events_publish_fail_total` 与 `agent_events_published_total` 趋势
- Verify webhook receiver accepts `application/json` POST and returns `2xx` / 确认 webhook 接收端接受 `application/json` POST 且返回 `2xx`
- Verify adapter envs (`SRE_AGENT_EVENT_SLACK_WEBHOOK_URL`, `SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY`) when using native sinks / 使用原生 sink 时检查适配器环境变量（`SRE_AGENT_EVENT_SLACK_WEBHOOK_URL`、`SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY`）
- Tune retry policy if downstream is intermittently unavailable (`SRE_AGENT_EVENT_PUBLISH_RETRIES`, `SRE_AGENT_EVENT_RETRY_BACKOFF`) / 下游间歇不可用时调优重试策略（`SRE_AGENT_EVENT_PUBLISH_RETRIES`、`SRE_AGENT_EVENT_RETRY_BACKOFF`）

## 12. Related docs / 相关文档

- Config reference: `docs/operations/configuration.md` / 配置参考
- RDMA + storage playbook: `docs/operations/rdma_storage_playbook.md` / RDMA 与存储运维手册
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
