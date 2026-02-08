# API Reference / API 参考

Controller HTTP base / Controller HTTP 基础：

- API prefix: `http://<controller>:8080/api/v1`
- Ops endpoints / 运维端点: `http://<controller>:8080/metrics`, `/health`, `/healthz`

## Chinese Quick Reference (For Duty SREs) / 中文速查（面向值班 SRE）

### Common troubleshooting endpoints / 常用排障接口

- `GET /status`: Controller runtime status and version / 控制器运行状态与版本
- `GET /fleet`: Latest fleet snapshot / 全量节点最新快照
- `GET /fleet/timeseries`: Single-node timeseries curves + anomaly flags + numeric summary / 单节点时序曲线 + 异常标记 + 数值摘要
- `GET /top/programs`: Process ranking by resource category (supports `collector_id`) / 按资源分类的进程排名（支持 `collector_id`）
- `GET /topology`: Topology hotspots (`fleet -> host -> process`) / 拓扑热点

### Recommended query sequence / 推荐查询顺序

1. `status`: Check overall system availability / 看系统整体可用性
2. `fleet/timeseries`: Determine if it's a spike or sustained anomaly / 判断是突发还是持续异常
3. `top/programs`: Identify responsible processes and their share / 锁定责任进程与占比
4. `topology`: Determine if impact is spreading / 判断影响是否扩散

---

## Version status / 版本状态

`GET /api/v1/status`

Returns controller status including release version / 返回控制器状态包括发布版本。

Example / 示例 (current release line / 当前发布版本):

```json
{
  "version": "v0.2",
  "uptime": "running",
  "total_nodes": 3,
  "healthy_nodes": 3,
  "scrape_interval": "15s",
  "listen_address": ":8080"
}
```

## Core fleet APIs / 核心 Fleet API

| Endpoint | Method | Description / 说明 |
|---|---|---|
| `/fleet` | GET | Fleet snapshot from push-ingested collector data / Push 采集的 collector 数据的集群快照 |
| `/fleet/{collector_id}` | GET | Snapshot for one collector / 单个 collector 的快照 |
| `/fleet/timeseries` | GET | Collector metric history for curve visualization, anomaly hints, and numeric summary / Collector 指标历史，用于曲线可视化、异常提示和数值摘要 |
| `/top/programs` | GET | Per-process cross-resource rankings and `resource_pages` / 每进程跨资源排名和 `resource_pages` |
| `/topology` | GET | Topology graph payload (`fleet -> host -> hottest processes`) / 拓扑图数据负载 |
| `/nodes` | GET, POST | Pull-mode node list management / 拉取模式节点列表管理 |
| `/nodes/{id}` | GET, DELETE | Pull-mode node detail/removal / 拉取模式节点详情/移除 |
| `/metrics` | GET | Pull-mode aggregated node metrics / 拉取模式聚合节点指标 |
| `/metrics/{id}` | GET | Pull-mode metrics for one node / 单节点的拉取模式指标 |
| `/metrics/history` | GET | Historical samples (`node`, `limit` query params) / 历史样本 |
| `/status` | GET | Controller runtime status / Controller 运行时状态 |

### `GET /top/programs` notes / 说明

Query params / 查询参数：

- `limit` (optional / 可选): defaults to `20`, capped at `200` / 默认 `20`，最多 `200`
- `collector_id` (optional / 可选): restrict ranking to one collector (align with curve drill-down) / 限制排名到单个 collector（与曲线钻取对齐）

Response contains / 响应包含：

- `programs`
- `summary`
- `by_category`
- `report`
- `resource_pages`

`resource_pages[category].ranked` is intended for textual RCA drill-down lists / `resource_pages[category].ranked` 用于文本 RCA 钻取列表：

- Already sorted high-to-low by category pressure / 已按类别压力从高到低排序
- Includes exact numeric fields (`cpu_percent`, `memory_bytes`, `disk_*`, `net_*`, `gpu_*`, `log_*`) / 包含精确数值字段
- Can be rendered with per-row share percentage (`row_value / sum(row_values)`) / 可以渲染每行占比百分比

Chinese example / 中文示例（查看某节点 CPU/内存/网络热点进程）：

```bash
curl -s "http://127.0.0.1:8080/api/v1/top/programs?collector_id=<collector>&limit=30"
```

Categories / 类别：

- `cpu`, `gpu`, `memory`, `network`, `disk`, `disk_io`, `logs`

Semantics / 语义：

- `disk`: cumulative storage footprint/activity / 累计存储足迹/活动
- `disk_io`: live throughput and syscall/event pressure / 实时吞吐和 syscall/事件压力

### `GET /fleet/timeseries` notes / 说明

Purpose / 目的：

- Returns bounded server-side history for one collector / 返回单个 collector 的有界服务端历史
- Includes both exact numeric summary fields and curve points per metric / 包含精确的数值摘要字段和每个指标的曲线点
- Flags anomaly-like points (z-score/jump heuristic) for quick visual triage / 标记类似异常的点（z-score/跳跃启发式）用于快速视觉分类

Query params / 查询参数：

- `collector_id` (optional / 可选): collector to query. If omitted, controller picks the most recently updated collector / 要查询的 collector。如果省略，controller 选择最近更新的 collector
- `window` (optional / 可选): lookback duration (`15m`, `30m`, `1h`, `3h`, ...). Defaults to `30m`, capped at `24h` / 回溯时长。默认 `30m`，最多 `24h`
- `limit` (optional / 可选): max sample count. Defaults to `360`, capped at `2000` / 最大样本数。默认 `360`，最多 `2000`
- `metric` (optional, repeatable / 可选，可重复): filter by curve key (e.g. `cpu_usage_percent`, `memory_used_percent`) / 按曲线键过滤

Response highlights / 响应亮点：

- `numeric_summary`: exact latest values for key signals (CPU, memory, network/disk throughput, process pressure) / 关键信号的精确最新值
- `series[]`: curve payloads per metric key / 每个指标键的曲线负载
- `series[].points[]`: timestamp/value points / 时间戳/值点
- `series[].points[].is_anomaly`: true when a point is flagged by anomaly heuristics / 当点被异常启发式标记时为 true

Chinese example / 中文示例（查看 1 小时 CPU 与内存曲线）：

```bash
curl -s "http://127.0.0.1:8080/api/v1/fleet/timeseries?collector_id=<collector>&window=1h&metric=cpu_usage_percent&metric=memory_used_percent"
```

### `GET /topology` notes / 说明

Purpose / 目的：

- Returns graph-ready topology data for the dashboard topology panel / 返回仪表盘拓扑面板的图形就绪拓扑数据
- Connects `fleet -> host -> top process hotspots` with severity/status / 连接 `fleet -> host -> top 进程热点` 及严重性/状态

Query params / 查询参数：

- `collector_id` (optional / 可选): return topology scoped to one collector / 返回限于单个 collector 的拓扑

Response highlights / 响应亮点：

- `summary`: host/process counts and degraded/critical host counts / 主机/进程计数和降级/严重主机计数
- `nodes[]`: node records with `type` (`fleet`, `host`, `process`) and severity / 带有 `type` 和严重性的节点记录
- `links[]`: directional relations (`collector`, `hotspot`) with optional weight / 带有可选权重的方向关系

Chinese example / 中文示例（查看单节点拓扑热点）：

```bash
curl -s "http://127.0.0.1:8080/api/v1/topology?collector_id=<collector>"
```

## GPU APIs / GPU API

Available when controller GPU module is enabled (`gpu.enabled=true`) / 当 controller GPU 模块启用时可用。

| Endpoint | Method | Description / 说明 |
|---|---|---|
| `/gpu/nodes` | GET | Fleet GPU inventory and latest per-device data / 集群 GPU 清单和最新每设备数据 |
| `/gpu/nodes/{collector_id}` | GET | GPU snapshot for one collector / 单个 collector 的 GPU 快照 |
| `/k8s/gpu/nodes` | GET | K8s-friendly compact GPU snapshot list / K8s 友好的紧凑 GPU 快照列表 |

## Analysis APIs / 分析 API

Available when `analysis.enabled=true` / 当 `analysis.enabled=true` 时可用。

| Endpoint | Method | Description / 说明 |
|---|---|---|
| `/analysis/alerts` | GET | Active/inactive analysis alerts / 活跃/非活跃分析告警 |
| `/analysis/anomalies` | GET | Detected anomalies / 检测到的异常 |
| `/analysis/rca` | GET | Root-cause analysis outputs / 根因分析输出 |
| `/analysis/status` | GET | Analysis subsystem status and config / 分析子系统和配置状态 |
| `/analysis/evidence/{node}` | GET | Compact evidence pack for node / 节点的紧凑证据包 |

## Agent APIs / Agent API

Available when `agent.enabled=true` / 当 `agent.enabled=true` 时可用。

| Endpoint | Method | Description / 说明 |
|---|---|---|
| `/agent/reports` | GET | Reports across nodes / 跨节点报告 |
| `/agent/reports/{node}` | GET | Reports for one node / 单个节点的报告 |
| `/agent/reports/latest` | GET | Latest report per node / 每个节点的最新报告 |
| `/agent/reports/{node}/latest` | GET | Latest report for one node / 单个节点的最新报告 |
| `/agent/status` | GET | Agent status summary / Agent 状态摘要 |
| `/agent/query` | POST | NL query over fleet/telemetry/GPU context / 基于 fleet/遥测/GPU 上下文的自然语言查询 |
| `/agent/execute` | POST | Execute one proposed action by `action_id` / 通过 `action_id` 执行一个建议的操作 |
| `/agent/actions` | GET | Action list (`node`, `limit` query params) / 操作列表 |
| `/agent/actions/{id}` | POST, PATCH | Update action status/note / 更新操作状态/注释 |
| `/agent/incidents` | GET | Incident assessments / 事件评估 |
| `/agent/incidents/{id}` | GET | One incident assessment / 单个事件评估 |
| `/agent/incidents/{id}/context` | GET | Context bundle for one incident / 单个事件的上下文包 |

Agent API notes / Agent API 说明：

- `limit` is optional for list endpoints. If provided, it must be a non-negative integer / `limit` 对于列表端点是可选的。如果提供，必须是非负整数
- `/agent/reports` and `/agent/actions` return newest entries first / `/agent/reports` 和 `/agent/actions` 首先返回最新条目
- `POST /agent/query` payload / `POST /agent/query` 负载：
  - `query` (required / 必需): natural language question (e.g. RCA on high CPU/GPU) / 自然语言问题（如高 CPU/GPU 的 RCA）
  - `node` (optional / 可选): collector ID or hostname hint / collector ID 或主机名提示
- `POST /agent/execute` payload / `POST /agent/execute` 负载：
  - `action_id` (required / 必需): action identifier returned by `/agent/query` / `/agent/query` 返回的操作标识符
  - `dry_run` (optional / 可选): override dry-run mode per request / 每个请求覆盖 dry-run 模式
- `POST`/`PATCH /agent/actions/{id}` accepts JSON body fields / 接受 JSON 主体字段：
  - `status` (optional / 可选)
  - `note` (optional / 可选)
- At least one of `status` or `note` must be provided / `status` 或 `note` 至少需要提供一个
- Supported action statuses / 支持的操作状态：`proposed`、`acknowledged`、`in_progress`、`completed`、`dismissed`、`accepted`、`rejected`、`canceled`

Example action update / 操作更新示例：

```bash
curl -X PATCH http://<controller>:8080/api/v1/agent/actions/<id> \
  -H 'Content-Type: application/json' \
  -d '{"status":"in_progress","note":"owner assigned"}'
```

Example AGENT query / AGENT 查询示例：

```bash
curl -X POST http://<controller>:8080/api/v1/agent/query \
  -H 'Content-Type: application/json' \
  -d '{"query":"RCA for high GPU utilization on fleet"}'
```

## Incident ingestion API / 事件接入 API

Available when incidents coordinator is enabled (`incidents.enabled=true`) / 当 incidents 协调器启用时可用。

| Endpoint | Method | Description / 说明 |
|---|---|---|
| `/incidents/alerts` | POST | Push external alert and trigger context aggregation / 推送外部告警并触发上下文聚合 |

Expected payload fields / 预期负载字段：

- `id`, `title`, `service`, `severity`
- `starts_at`, `ends_at`
- `labels`, `annotations`

## External checks APIs / 外部检查 API

Available when `checks.enabled=true` / 当 `checks.enabled=true` 时可用。

| Endpoint | Method | Description / 说明 |
|---|---|---|
| `/checks` | GET | Latest check results / 最新检查结果 |
| `/checks/history` | GET | Check history / 检查历史 |

## Ops endpoints / 运维端点

| Endpoint | Method | Description / 说明 |
|---|---|---|
| `/health` | GET | Health probe / 健康检查 |
| `/healthz` | GET | Health probe / 健康检查 |
| `/metrics` | GET | Prometheus exposition / Prometheus 指标暴露 |

## Authentication / 认证

When controller auth is enabled (`auth.enabled=true` and API key env set), include / 当 controller 认证启用时，包含：

```text
Authorization: Bearer <api-key>
```

Default API key environment variable: `SRE_AGENT_CONTROLLER_API_KEY` / 默认 API 密钥环境变量。
