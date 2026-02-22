# API Reference / API 参考

Controller HTTP base / Controller HTTP 基础：

- API prefix: `http://<controller>:8080/api/v1`
- Ops endpoints / 运维端点: `http://<controller>:8080/metrics`, `/health`, `/healthz`

## Chinese Quick Reference (For Duty SREs) / 中文速查（面向值班 SRE）

### Common troubleshooting endpoints / 常用排障接口

- `GET /status`: Controller runtime status and version / 控制器运行状态与版本
- `GET /ingest/status`: Ingest quality/counters / 接入质量与计数器
- `GET /inventory/probes`: Merged probe inventory / 合并探针清单
- `GET /fleet`: Latest fleet snapshot / 全量节点最新快照
- `GET /fleet/{collector_id}`: Includes per-device/partition/filesystem storage snapshots / 包含按设备/分区/文件系统的存储快照
- `GET /fleet/timeseries`: Single-node timeseries curves + anomaly flags + numeric summary / 单节点时序曲线 + 异常标记 + 数值摘要
- `GET /top/programs`: Process ranking by resource category (supports `collector_id`) / 按资源分类的进程排名（支持 `collector_id`）
- `GET /diagnostics/data-path`: Network + storage + probe-core path ranked pressure and anomaly diagnostics / 网络 + 存储 + probe-core 路径压力排名与异常诊断
- `GET /diagnostics/kernel-path`: Linux kernel storage/network stack-stage diagnostics / Linux 内核存储与网络栈分层诊断
- `GET /diagnostics/root-cause`: Cross-layer RCA findings (compute/network/storage/kernel/probe-core correlation) / 跨层 RCA 结论（计算/网络/存储/内核/probe-core 关联）
- `GET /diagnostics/workload-path`: Kubernetes workload → node → network/storage/kernel mapped diagnostics / Kubernetes 工作负载到节点到网络/存储/内核映射诊断
- `GET /diagnostics/ai-infra-stack`: Layered AI infra diagnostics (`compute -> orchestration -> fabric -> memory -> pipeline -> execution -> reliability -> serving`) / 分层 AI Infra 诊断
- `GET /diagnostics/rca-packet`: Server-side markdown RCA handoff artifact export / 服务端 Markdown RCA 交接工件导出
- `GET /k8s/workloads/top`: Pod/workload ranking / Pod/工作负载排名
- `GET /k8s/nodes/top`: Node ranking / 节点排名
- `GET /topology`: Topology hotspots (`fleet -> host -> process`) / 拓扑热点

### Recommended query sequence / 推荐查询顺序

1. `status`: Check overall system availability / 看系统整体可用性
2. `fleet/timeseries`: Determine if it's a spike or sustained anomaly / 判断是突发还是持续异常
3. `diagnostics/data-path`: Rank network/storage bottlenecks and anomaly candidates / 排序网络/存储瓶颈与异常候选
4. `diagnostics/kernel-path`: Inspect kernel stack stages (`page_cache_writeback`, `block_layer_device`, `interrupt_napi`, `socket_tcp`, `rdma_fabric`) / 检查内核栈分层
5. `diagnostics/root-cause`: Build cross-layer hypotheses and evidence packs / 生成跨层假设与证据包
6. `diagnostics/workload-path`: Map pressure to workload/service and node spread / 将压力映射到工作负载/服务和节点扩散范围
7. `diagnostics/ai-infra-stack`: Confirm layer-level coverage/risk, `workload -> pod -> node -> device` mappings, and `incident -> workload -> placement -> contention` drilldowns / 确认分层覆盖率与风险、`工作负载 -> Pod -> 节点 -> 设备` 映射，以及 `事件 -> 工作负载 -> 放置 -> 争用` 钻取链路
8. `diagnostics/rca-packet`: Export immutable markdown handoff packet for incident systems / 导出不可变 Markdown 交接包到故障系统
9. `top/programs`: Identify responsible processes and their share / 锁定责任进程与占比
10. `topology`: Determine if impact is spreading / 判断影响是否扩散

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
| `/ingest/status` | GET | Ingest counters and last error/metadata / 接入计数器和最近错误/元数据 |
| `/ingest/schema` | GET | Telemetry payload schema/validation limits / 遥测负载 schema/校验上限 |
| `/inventory/status` | GET | Probe inventory health summary / 探针清单健康摘要 |
| `/inventory/probes` | GET | Probe inventory list (static + telemetry + heartbeat) / 探针清单列表（静态+遥测+心跳） |
| `/inventory/probes/{id}` | GET | One probe inventory record / 单个探针清单记录 |
| `/inventory/heartbeat` | POST | Optional probe registration heartbeat / 可选探针注册心跳 |
| `/fleet/{collector_id}` | GET | Snapshot for one collector / 单个 collector 的快照 |
| `/fleet/timeseries` | GET | Collector metric history for curve visualization, anomaly hints, and numeric summary / Collector 指标历史，用于曲线可视化、异常提示和数值摘要 |
| `/top/programs` | GET | Per-process cross-resource rankings and `resource_pages` / 每进程跨资源排名和 `resource_pages` |
| `/diagnostics/data-path` | GET | Node/cluster network-storage-probe-core pressure ranking + anomaly diagnostics / 节点/集群网络/存储/probe-core 压力排名及异常诊断 |
| `/diagnostics/kernel-path` | GET | Per-node Linux kernel stack-stage diagnostics (`storage + network`) with metric-source provenance / 每节点 Linux 内核分层诊断（存储+网络）及指标来源 |
| `/diagnostics/root-cause` | GET | Cross-layer RCA findings with severity/confidence/evidence/action hints / 含严重度/置信度/证据/行动建议的跨层 RCA 结论 |
| `/diagnostics/workload-path` | GET | Kubernetes workload-level cross-layer diagnostics (`workload -> node -> network/storage/kernel`) with risk flags / Kubernetes 工作负载跨层诊断（工作负载到节点到网络/存储/内核）及风险标记 |
| `/diagnostics/ai-infra-stack` | GET | Layered AI infra model (`compute`, `orchestration`, `fabric`, `memory`, `data pipeline`, `execution`, `reliability`, `serving`) with measurable coverage and ranked entities / 分层 AI Infra 模型（含可测覆盖率与排名实体） |
| `/diagnostics/rca-packet` | GET | Server-generated markdown RCA handoff packet (`root-cause + kernel-path + resource ranking + workload-path`) / 服务端生成 Markdown RCA 交接包（根因+内核路径+资源排名+工作负载路径） |
| `/topology` | GET | Topology graph payload (`fleet -> host -> hottest processes`) / 拓扑图数据负载 |
| `/k8s/status` | GET | Kubernetes integration runtime status / Kubernetes 集成运行状态 |
| `/k8s/clusters` | GET | Cluster snapshot summaries / 集群快照摘要 |
| `/k8s/clusters/{name}` | GET | Detailed cluster snapshot (nodes/workloads/GPU) / 集群详细快照（节点/工作负载/GPU） |
| `/k8s/topology` | GET | Service-to-node/process/GPU graph payload / 服务到节点/进程/GPU 图数据 |
| `/k8s/workloads/top` | GET | Ranked workloads/pods by pressure metric / 按压力指标排名的工作负载/Pod |
| `/k8s/nodes/top` | GET | Ranked nodes by pressure metric / 按压力指标排名的节点 |
| `/orchestration/status` | GET | Orchestration runtime status and counters / 编排运行状态与计数器 |
| `/orchestration/policy` | GET | Active SLO/remediation policy snapshot / 当前 SLO/修复策略快照 |
| `/orchestration/diagnostics` | GET | SLO violations + remediation gate reason breakdown / SLO 违约与修复阻断原因分解 |
| `/orchestration/resources` | GET | Unified pooled resource inventory (CPU/GPU/NPU/memory/network/storage) / 统一资源池视图 |
| `/orchestration/workloads` | GET, POST | List or submit scheduling intents / 查看或提交调度意图 |
| `/orchestration/workloads/{id}` | GET, DELETE | Query or remove one workload / 查询或删除单个工作负载 |
| `/orchestration/workloads/{id}/complete` | POST | Mark a workload as completed / 标记工作负载完成 |
| `/orchestration/routes` | GET | Dynamic multi-model routing plan / 动态多模型路由方案 |
| `/orchestration/reconcile` | POST | Trigger immediate reconcile (schedule + self-heal) / 触发即时重平衡 |
| `/orchestration/events` | GET | Recent self-healing actions / 最近自愈动作 |
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

Per-process rows now include optional workload-correlation context fields when available from probe telemetry:

- `workload_class` (for example `training`, `inference`, `system`)
- `job` (best-effort run/job identifier from process args)
- `comm_pattern` (for example `nccl`, `rdma`, `mpi`, `gloo`, `ucx`)
- `pod_uid` (when the process is inside a Kubernetes pod cgroup)

Per-process rows can also include low-level contention fields when probe-core process telemetry is enabled:

- `sched_wait_ratio`, `sched_wait_seconds_total`, `sched_run_seconds_total` (from `/proc/<pid>/schedstat`)
- `block_io_delay_seconds_total`, `block_io_delay_seconds_per_second` (from `/proc/<pid>/stat` delay accounting)

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

### `GET /diagnostics/data-path` notes / 说明

Purpose / 目的：

- Provide ranked node-level pressure for network, storage, and probe-core runtime path / 提供网络、存储与 probe-core 运行路径的节点级压力排名
- Surface anomaly candidates from recent trend history / 从近期趋势历史中给出异常候选
- Provide cross-layer bottleneck hints (`compute|network|storage`) per node / 给出每个节点的跨层瓶颈提示

Query params / 查询参数：

- `collector_id` (optional / 可选): scope diagnostics to one collector / 将诊断范围限制到单个 collector

Response highlights / 响应亮点：

- `network.rankings[]`: sorted network pressure rows / 网络压力排序行
- `storage.rankings[]`: sorted storage pressure rows / 存储压力排序行
- `probe_core.rankings[]`: probe-core reliability pressure rows (client/freshness/fallback/module selection) / probe-core 可靠性压力排序行（客户端/新鲜度/回退/模块选择）
- `network.anomalies[]`, `storage.anomalies[]`, `probe_core.anomalies[]`: z-score based anomalies / 基于 z-score 的异常
- `data_paths[]`: unified per-node compute/network/storage interaction summary / 每节点统一的 compute/network/storage 交互摘要
- `network.top_processes[]`, `storage.top_processes[]`: ranked textual RCA context / 排序后的文本 RCA 上下文
- `summary.probe_core_*`: fleet-level probe-core fallback/invalid-config counters / 集群级 probe-core 回退与配置异常计数

Signal and metric naming notes / 信号与指标命名说明：

- `rankings[].signals` keys are pressure-factor oriented (for example `tcp_retransmit_ratio`, `latency_p99_ms`) and may not match `fleet/timeseries` curve keys 1:1 / `rankings[].signals` 键偏向压力因子，可能与曲线键不完全一致
- `probe_core.rankings[].signals` include `configured`, `active`, `fresh`, `selection_valid`, module counts, and frame/runtime error counters / `probe_core.rankings[].signals` 包含配置/活跃/新鲜度/选择有效性、模块数量与帧错误计数
- `anomalies[].metric` uses raw metric names from ingest history (for example `node_*`) / `anomalies[].metric` 使用接入历史中的原始指标名（例如 `node_*`）
- UI drilldown normalizes them to trend keys before opening `Metric Trends` (for example `node_disk_request_latency_p99_seconds` -> `disk_request_latency_p99_ms`) / UI 在跳转前会归一化到趋势键

Example / 示例：

```bash
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path?collector_id=<collector>"
```

### `GET /diagnostics/root-cause` notes / 说明

Purpose / 目的：

- Produce practical, ranked RCA findings by correlating `data-path` pressure/anomalies with node metrics / 结合数据路径压力、异常与节点指标生成可执行 RCA 结论
- Preserve evidence provenance through per-signal `source` fields (for example `/proc`, `/sys`, probe-core runtime counters) / 通过每个信号的 `source` 保留证据来源（如 `/proc`、`/sys`、probe-core 运行时计数器）
- Prioritize findings by severity and confidence for on-call triage / 按严重度与置信度排序，便于值班优先处置

Query params / 查询参数：

- `collector_id` (optional / 可选): scope RCA to one collector / 将 RCA 范围限制到单个 collector

Response highlights / 响应亮点：

- `summary`: `node_count`, `finding_count`, `critical_findings`, `degraded_findings`, `top_finding_*`
- `data_path`: joined summary counters from `/diagnostics/data-path` (`network_critical`, `storage_critical`, `probe_core_critical`, `total_anomalies`)
- `findings[]`: ordered RCA conclusions
  - `id`, `category`, `severity`, `confidence`
  - `hypothesis`, `impact`, `actions[]`
  - `affected_nodes[]`, `correlated_signals[]`
  - `evidence[]` with `value`, `baseline`, `z_score`, `source`, `note`
- Common finding IDs / 常见 Finding ID:
  - `network_congestion_training_slowdown`
  - `storage_latency_gpu_starvation`
  - `scheduler_contention_tail_latency`
  - `memory_pressure_io_amplification`
  - `collective_runtime_queueing_contention`
  - `cross_node_communication_imbalance`

Example / 示例：

```bash
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause?collector_id=<collector>"
```

### `GET /diagnostics/kernel-path` notes / 说明

Purpose / 目的：

- Expose practical Linux stack-stage bottlenecks for storage and network paths / 暴露存储与网络路径的 Linux 栈分层瓶颈
- Keep each signal linked to concrete kernel/device source (`/proc`, `/sys`, RDMA counters) / 每个信号绑定到具体内核/设备来源
- Support low-level workflows such as sudden I/O latency spike and intermittent network delay triage / 支持 I/O 延迟突增、网络抖动等底层排障

Query params / 查询参数：

- `collector_id` (optional / 可选): scope diagnostics to one collector / 将诊断范围限制到单个 collector

Response highlights / 响应亮点：

- `nodes[].storage.stages[]`: staged storage path diagnostics (`syscall_vfs`, `page_cache_writeback`, `block_layer_device`, `nvme_device`, `remote_object_checkpoint`)
- `page_cache_writeback` stage now includes `cpu_iowait_percent` so iowait spikes can be tied to dirty/writeback pressure / `page_cache_writeback` 阶段包含 `cpu_iowait_percent`，用于将 iowait 抖动关联到脏页/回写压力
- `nodes[].network.stages[]`: staged network path diagnostics (`nic_link`, `interrupt_napi`, `socket_tcp`, `egress_queue`, `rdma_fabric`)
- `stages[].sources`: per-signal measurement source map / 每个信号的采样来源映射
- `summary.top_storage_stage`, `summary.top_network_stage`, `summary.top_bottleneck_key`: cluster-level dominant bottleneck hints / 集群级主导瓶颈提示

Example / 示例：

```bash
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path?collector_id=<collector>"
```

### `GET /diagnostics/workload-path` notes / 说明

Purpose / 目的：

- Bridge cloud-native topology with low-level telemetry by ranking workload bottlenecks / 通过工作负载瓶颈排名将云原生拓扑与底层遥测关联
- Map `workload -> pod -> node -> network/storage/kernel` with telemetry coverage visibility / 提供 `工作负载 -> Pod -> 节点 -> 网络/存储/内核` 映射并显示遥测覆盖率
- Flag practical risks such as GPU starvation, communication imbalance, scheduler contention, and cross-node spread / 标记 GPU 饥饿、通信失衡、调度争用、跨节点扩散等实战风险

Query params / 查询参数：

- `cluster` (optional / 可选): filter one cluster / 限制到单个集群
- `namespace` (optional / 可选): filter one namespace / 限制到单个命名空间
- `service` (optional / 可选): filter one service/workload service name / 限制到单个服务名
- `limit` (optional / 可选): default `30`, max `200` / 默认 `30`，最大 `200`

Response highlights / 响应亮点：

- `workloads[].compute_score|network_score|storage_score|overall_score`: per-workload cross-layer pressure / 工作负载跨层压力
- `workloads[].nodes[]`: per-node mapped diagnostics (collector, bottleneck, kernel stages, signals, sources) / 每节点映射诊断（采集器、瓶颈、内核阶段、信号、来源）
- `workloads[].risks[]`: risk flags (`gpu_starvation_due_to_io_or_network`, `communication_imbalance`, `scheduler_contention`, ...) / 风险标记
- `summary`: critical/degraded counts, telemetry coverage, multi-node spread, top bottleneck / 汇总统计

Example / 示例：

```bash
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path?cluster=<cluster>&namespace=<ns>&service=<svc>&limit=20"
```

### `GET /diagnostics/ai-infra-stack` notes / 说明

Purpose / 目的：

- Expose a practical AI infra stack model with explicit capability layers / 暴露具备明确能力分层的 AI Infra 诊断模型
- Correlate existing diagnostics into one layered payload (`compute`, `orchestration`, `fabric`, `memory`, `pipeline`, `execution`, `reliability`, `serving`) / 将现有诊断聚合为统一分层结果
- Show what is measured vs missing per layer to avoid over-claiming / 显式区分已测量与缺失能力，避免过度宣称

Query params / 查询参数：

- `collector_id` (optional / 可选): scope diagnostics to one collector / 限制到单个 collector
- `cluster` / `namespace` / `service` (optional / 可选): workload scope filters / 工作负载范围过滤
- `workload_limit` (optional / 可选): default `40`, max `200` / 默认 `40`，最大 `200`

Response highlights / 响应亮点：

- `summary`: layer critical/degraded counts, top-risk layer, overall measurement coverage / 分层严重度、主风险层和整体覆盖率
- `summary.incident_drilldowns`: number of generated `incident -> workload -> placement -> contention` chains / 生成的 `事件 -> 工作负载 -> 放置 -> 争用信号` 链路数量
- `summary.measurements_*` + `summary.methods_*`: fleet-level measurement status and provenance mix counters / 集群级测量状态与来源类型计数
- `layers[]`: one row per AI infra capability layer
  - `score`, `severity`, `coverage_percent`
  - `domains[]`: explicit subdomain decomposition (`id`, `title`, `score`, `severity`, `coverage_percent`, `signals`, `sources`) for targeted triage / 显式子域拆解（含分值、覆盖率、信号、来源）便于定向排障
  - `measurements[]` with `status=measured|partial|missing`
    - `measured`: signal coverage is present for the scoped entities / 覆盖范围内信号可测
    - `partial`: signal exists but only for part of the scoped entities / 信号存在但仅覆盖部分范围
    - `missing`: signal is unavailable for the current scope / 当前范围不可测
    - `method=direct|derived|proxy|missing`:
      - `direct`: raw kernel/device/runtime counter source / 直接原始计数器来源
      - `derived`: computed metric from measured counters / 由可测计数器计算得到
      - `proxy`: heuristic or runtime proxy signal / 启发式或运行时代理信号
      - `missing`: signal path unavailable / 信号路径不可用
  - orchestration layer includes tenant-share fairness proxies (`tenant_fairness_index`, `tenant_top_share`) / 编排层包含租户份额公平性代理指标
  - compute layer includes job-slice occupancy proxies from scheduler partitions (`gpu_slice_density`, `gpu_partition_assignments`) / 计算层包含来自调度分片的作业切片占用代理指标
  - communication layer domains include `in_node_interconnect`, `inter_node_fabric`, `collective_runtime` / 通信层子域包含 `in_node_interconnect`、`inter_node_fabric`、`collective_runtime`
  - communication layer now includes direct process-level socket/scheduler measurements (`top_programs.{net_queued_bytes,net_connections}`, `top_programs.sched_wait_ratio`) to support collective-runtime triage / 通信层新增进程级 socket/调度直接测量，支撑集合通信运行时排障
  - memory layer domains include `hbm_to_host_dram`, `page_cache_writeback`, `nvme_distributed_object_tiers` / 内存层子域包含 `hbm_to_host_dram`、`page_cache_writeback`、`nvme_distributed_object_tiers`
  - reliability layer includes SLI/error-budget/timeline proxies (`availability_sli`, `latency_compliance_sli`, `error_budget_remaining`, `mttd_proxy_seconds`, `mttr_proxy_seconds`) / 可靠性层包含 SLI/误差预算/时间线代理指标
  - reliability layer domains include `sli_error_budget`, `fault_tolerance_recovery`, `incident_lifecycle_rca` / 可靠性层子域包含 `sli_error_budget`、`fault_tolerance_recovery`、`incident_lifecycle_rca`
  - serving layer domains include `queueing_tail_latency`, `batching_model_placement`, `kv_cache_pressure` / 推理层子域包含 `queueing_tail_latency`、`batching_model_placement`、`kv_cache_pressure`
  - `ranked_entities[]` for node/workload/process/finding drilldown
  - `top_risks[]`, `observability_gaps[]`, `sources{}`
- `workload_mappings[]`: `workload -> pod -> node -> device` mapping context for incident scoping / 故障定界所需的路径映射
- `incident_drilldowns[]`: explicit troubleshooting chain
  - `finding_*`: root-cause finding metadata / 根因条目元数据
  - `workloads[]`: affected job/workload hops with queue delay + pod failure context / 受影响作业路径及排队/失败上下文
  - `placements[]`: node placement hops with contention signals and reason / 带争用信号与原因的节点放置路径
  - `contention[]`: evidence signals with source attribution / 带来源归因的争用证据信号
  - drilldown matcher now includes `collective_runtime*` categories/IDs as first-class workload mapping domain / 钻取匹配器已将 `collective_runtime*` 类别和 ID 作为一等映射域

Example / 示例：

```bash
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/ai-infra-stack"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/ai-infra-stack?collector_id=<collector>&cluster=<cluster>&namespace=<ns>&service=<svc>&workload_limit=50"
```

### `GET /diagnostics/rca-packet` notes / 说明

Purpose / 目的：

- Produce a server-side, reproducible markdown RCA artifact for incident handoff / 生成服务端可复现的 Markdown RCA 工件用于故障交接
- Bundle cross-layer evidence from `data-path`, `kernel-path`, `root-cause`, and `workload-path` / 打包 `data-path`、`kernel-path`、`root-cause`、`workload-path` 跨层证据
- Preserve sort/filter context in exported packet metadata / 在导出工件中保留排序与过滤上下文

Query params / 查询参数：

- `collector_id` (optional / 可选): scope packet to one collector / 限制到单个 collector
- `cluster` / `namespace` / `service` (optional / 可选): workload scope filters / 工作负载范围过滤
- `sort_key` (optional / 可选): `severity|overall|coverage|network|storage` (default `severity`) / 排序键（默认 `severity`）
- `sort_direction` (optional / 可选): `asc|desc` (default `desc`) / 排序方向（默认 `desc`）
- `workload_limit` (optional / 可选): default `30`, max `200` / 默认 `30`，最大 `200`
- `format` (optional / 可选): `json` (default) or `markdown` / `json`（默认）或 `markdown`
- `download` (optional / 可选): when `format=markdown`, set `1|true|yes|on` to return attachment headers / 当 `format=markdown` 时可设置 `1|true|yes|on` 返回附件头

Response highlights / 响应亮点：

- `file_name`: suggested markdown artifact filename / 建议的 Markdown 文件名
- `markdown`: full handoff packet content / 完整交接包内容
- `packet_sha256`: SHA-256 digest of markdown content / Markdown 内容的 SHA-256 摘要
- `content_bytes`: markdown byte size / Markdown 字节大小
- `packet_signature`: optional HMAC signature when signing key is configured / 配置签名密钥后返回可选 HMAC 签名
- `packet_signature_algorithm`: signature algorithm (currently `hmac-sha256`) / 签名算法（当前为 `hmac-sha256`）
- `packet_signature_key_id`: optional key identifier for key rotation / 可选密钥标识用于密钥轮换
- `summary`: finding/workload/ranking counters for audit pipelines / 供审计流程使用的计数摘要
- `source_metadata`: source endpoint map for evidence provenance / 证据来源接口映射

Markdown response headers (`format=markdown`) / Markdown 响应头：

- `X-AI-SRE-Packet-SHA256`
- `X-AI-SRE-Packet-Bytes`
- `X-AI-SRE-Packet-Signature` (when signing enabled / 启用签名时)
- `X-AI-SRE-Packet-Signature-Algorithm` (when signing enabled / 启用签名时)
- `X-AI-SRE-Packet-Signature-Key-ID` (when signing enabled / 启用签名时)

Signing env vars / 签名环境变量：

- `SRE_RCA_PACKET_SIGNING_KEY` (required to enable signing / 启用签名必需)
- `SRE_RCA_PACKET_SIGNING_KEY_ID` (optional key id / 可选 key id)

Example / 示例：

```bash
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/rca-packet?collector_id=<collector>&cluster=<cluster>&namespace=<ns>&service=<svc>&sort_key=severity&sort_direction=desc"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/rca-packet?collector_id=<collector>&format=markdown&download=1" -o rca_packet.md
```

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
- Storage-focused fields now include device utilization/queue/latency distribution and filesystem pressure (for example `disk_total_iops_per_second`, `disk_request_latency_p99_ms`, `filesystem_space_pressure_percent`) / 现已包含面向存储的利用率/队列/延迟分布及文件系统压力字段（例如 `disk_total_iops_per_second`、`disk_request_latency_p99_ms`、`filesystem_space_pressure_percent`）
- `series[]`: curve payloads per metric key / 每个指标键的曲线负载
- `series[].points[]`: timestamp/value points / 时间戳/值点
- `series[].points[].is_anomaly`: true when a point is flagged by anomaly heuristics / 当点被异常启发式标记时为 true

`GET /fleet/{collector_id}` storage sections / `GET /fleet/{collector_id}` 存储区块：

- `storage_devices`: per-disk throughput/IOPS/utilization/queue/latency / 每磁盘吞吐/IOPS/利用率/队列/延迟
- `storage_partitions`: per-partition throughput/IOPS / 每分区吞吐/IOPS
- `filesystems`: per-mount space/inode pressure / 每挂载点空间/ inode 压力

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

### Ingest quality and schema / 接入质量与 schema

- `GET /ingest/status`: returns counters (`batches_total`, `rejected_total`, metric/process/log totals), plus recent error metadata.
- `GET /ingest/schema`: returns validation contract limits (`max_metrics_per_batch`, `max_processes_per_batch`, ...).

Examples:

```bash
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s http://127.0.0.1:8080/api/v1/ingest/schema
```

### Probe inventory APIs / 探针清单 API

- `GET /inventory/probes` merges static controller config, push telemetry presence, and optional heartbeat registration.
- `POST /inventory/heartbeat` accepts:
  - `probe_id` (required)
  - `hostname`, `address`, `version`, `labels`, `timestamp` (optional)

Example heartbeat:

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/inventory/heartbeat \
  -H 'Content-Type: application/json' \
  -d '{"probe_id":"node-a","hostname":"node-a","address":"10.0.0.10:9100"}'
```

### Kubernetes integration APIs / Kubernetes 集成 API

Available when `kubernetes.enabled=true`.

- `GET /k8s/clusters`: cluster summaries.
- `GET /k8s/clusters/{name}`: detailed node/workload/GPU snapshot.
- `GET /k8s/workloads/top`: workload ranking (`metric=pressure|cpu|memory|gpu|logs|pending|failed|restarts`, `limit`, `cluster`).
- `GET /k8s/nodes/top`: node ranking (`metric=pressure|cpu|memory|gpu|logs`, `limit`, `cluster`).
- `GET /k8s/topology`: service-map graph (`cluster -> node -> workload -> process -> gpu`).

Examples:

```bash
curl -s http://127.0.0.1:8080/api/v1/k8s/clusters
curl -s "http://127.0.0.1:8080/api/v1/k8s/workloads/top?metric=pressure&limit=20"
curl -s "http://127.0.0.1:8080/api/v1/k8s/nodes/top?metric=gpu&limit=20"
```

## Orchestration APIs / 编排 API

Purpose / 目的：

- Provide a vendor-neutral scheduling control plane for heterogeneous compute pools / 提供面向异构算力池的中立调度控制面
- Separate realtime vs batch scheduling with SLA + deadline awareness / 区分实时与批处理，并结合 SLA/截止时间调度
- Expose routing plans for multi-model serving and cache-aware placement / 暴露多模型服务路由与缓存复用感知的放置结果

Common endpoints / 常用接口：

- `GET /orchestration/status`: reconcile cadence, stale policy, counters
- `GET /orchestration/policy`: active SLO breach/remediation gate settings
- `GET /orchestration/diagnostics`: current SLO-violating workloads and blocked gate reason ranking
- `GET /orchestration/resources`: node-level capacity/reserved/available/unhealthy state
- `POST /orchestration/workloads`: submit intent (class/priority/resources/target_concurrency/max_partitions)
- `POST /orchestration/reconcile`: force a scheduling and self-healing cycle
- `GET /orchestration/routes?service=<name>&model=<id>`: get traffic targets

Example submit / 提交示例：

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/orchestration/workloads \
  -H 'Content-Type: application/json' \
  -d '{
    "service":"chat-gateway",
    "model":"model-a",
    "class":"realtime",
    "priority":"P1",
    "requested":{"cpu_cores":2,"memory_bytes":2147483648},
    "target_concurrency":64,
    "latency_slo_ms":120,
    "max_partitions":2
  }'
```

Example reconcile + routes / 手动重平衡与路由示例：

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/orchestration/reconcile
curl -s "http://127.0.0.1:8080/api/v1/orchestration/routes?service=chat-gateway&model=model-a"
curl -s http://127.0.0.1:8080/api/v1/orchestration/policy
curl -s http://127.0.0.1:8080/api/v1/orchestration/diagnostics
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
  - `approval_token` (optional / 可选): required for mutating (non-dry-run) execution when approval policy is enabled / 启用审批策略时，变更型（非 dry-run）执行需要此字段
- `POST /agent/query` response highlights / `POST /agent/query` 响应重点：
  - `actions[].id`: query-scoped action IDs (`<query_id>-aN`) to avoid cross-query collisions / 查询作用域动作 ID（`<query_id>-aN`），避免跨查询冲突
  - `actions[].approval_required`: whether execute requires an approval token / 执行时是否需要审批令牌
  - `actions[].approval_token`: per-action token for guarded execution / 每个动作的受保护执行令牌
  - `actions[].expires_at`: action execution expiry timestamp / 动作执行过期时间
  - `actions_expire_at`: query-level expiry timestamp for pending actions / 查询级待执行动作过期时间
  - `used_fallback` + `fallback_reason`: indicates deterministic fallback and concrete reason / 指示确定性回退及具体原因
  - `actions_suppressed` + `suppression_reason`: indicates policy-based action suppression / 指示基于策略的动作抑制
  - `explainability`: deterministic evidence, limitations, and data coverage / 确定性证据、限制项与数据覆盖摘要
  - `explainability.data_coverage.telemetry_age_seconds` and `telemetry_stale` / 遥测年龄与 stale 状态
- `POST /agent/query` status behavior / 查询状态行为：
  - `429` when request is rate-limited / 请求被限流
  - `503` when in-flight query concurrency is saturated (`ErrBusy`) / 在途查询并发达到上限
  - `504` when LLM timeout is hit / LLM 超时
- Stale telemetry behavior / 遥测 stale 行为：
  - When telemetry age exceeds policy threshold, response marks `telemetry_stale=true` in explainability data / 遥测年龄超过阈值时，在 explainability 中标记 `telemetry_stale=true`
  - By default, stale queries suppress actions (empty `actions`) unless stale-action override is enabled / 默认 stale 查询会抑制动作（`actions` 为空），除非启用 stale 动作覆盖
  - When configured, stale or empty telemetry can bypass LLM calls and return deterministic fallback immediately / 配置启用时，stale 或空遥测可直接跳过 LLM 并返回确定性回退
- `POST /agent/execute` status behavior / 执行状态行为：
  - `404` when `action_id` is unknown / `action_id` 不存在
  - `410` when pending action has expired / 待执行动作已过期
  - `428` when approval token is required but missing / 需要审批令牌但未提供
  - `403` when approval token is invalid / 审批令牌无效
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

Example AGENT execute with approval token / 带审批令牌的执行示例：

```bash
curl -X POST http://<controller>:8080/api/v1/agent/execute \
  -H 'Content-Type: application/json' \
  -d '{"action_id":"<action-id>","approval_token":"<approval-token>"}'
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
