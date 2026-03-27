# 架构决策记录

English version: [docs/en/18-architecture-decisions.md](../en/18-architecture-decisions.md)

本页以 ADR（Architecture Decision Record）风格记录当前 `v0.8` 代码库里的关键实现决策。

这不是未来路线图。这里的每个决策都以当前仓库代码为依据，并回链到真正实现它的代码路径。

如果你想先看边界归属，先读 [架构](04-architecture.md)。如果你想先看完整数据流，先读 [Pipeline 深度解析](02-pipeline-deep-dive.md)。如果你想看建立在这些决策之上的受治理 workflow loop，再读 [事故 Agent 运行时](17-incident-agent-runtime.md)。

## 本页内容

- ADR-001：将主机观测与 controller 推理解耦
- ADR-002：以 probe-core 为主源，并保留 compatibility fallback
- ADR-003：在传输前抑制，而不是在中心端重建 delta
- ADR-004：使用磁盘本地 spool，并以 ACK 为提交点
- ADR-005：在 ingest 处重建 controller 自有事实模型
- ADR-006：热路径只保留 trend-safe 的有界历史
- ADR-007：分离单序列趋势逻辑与多变量弱信号相关性
- ADR-008：对混合检索做 gating，并把 incident memory 独立出来
- ADR-009：使用 durable governed workflow runtime，而不是 free-form agent 执行
- ADR-010：从现有 metric history 推导 recurring-burst 上下文
- ADR-011：把 RCA 分析与 validation / action 分开
- 下一步阅读

## ADR-001：将主机观测与 controller 推理解耦

### 状态

已接受，已实现。

### 背景

仓库同时需要：

- 靠近 `/proc`、`/sys`、GPU 和 eBPF 的主机本地证据采集
- 不应该运行在生产节点上的 controller 侧推理、检索和 workflow 治理

如果把两者揉成一个 runtime，就只剩两种坏结果：

- 要么主机承担过多逻辑和持久化；
- 要么 controller 丢失那些只有在节点上才能低成本拿到的短时证据。

### 决策

将主机观测保留在：

- [`../../cpp/probe_core/`](../../cpp/probe_core/)
- [`../../backend/internal/collector/`](../../backend/internal/collector/)

将集中推理与治理保留在：

- [`../../backend/internal/controller/`](../../backend/internal/controller/)

### 实现

这个 split 直接体现在数据契约上：

- native probe IPC：`probeipc.v1.FrameEnvelope` 与 `ProbeBatch`
- collector 到 controller 的 wire contract：`telemetry.v1.TelemetryBatch`
- controller 自有事实模型：`NodeSnapshot`
- controller 自有治理 runtime：`DurableRun`

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 纯 controller 侧 scraping 和推断 | 会丢失短时本地证据，并让采集质量更依赖远端可用性 |
| 主机驻留的重型 agent，同时本地推理和执行 | 扩大主机 blast radius、主机持久化需求和运维复杂度 |
| 先做通用 observability pipeline，再叠加 incident logic | 更宽泛，但不够贴合当前 collector-to-controller 证据路径 |

### 窄范围行业对比

- 相比 OpenTelemetry 风格的通用采集/导出，这个仓库选择了更窄、更明确的 collector-to-controller 契约，因为它需要显式 suppression、spool 和 controller reconstruction 语义。
- 相比 Pixie 或 Cilium 这类更强的常驻 kernel observability 体系，这个仓库刻意保持更小的主机驻留 runtime，并把更多推理留在 controller。

### 后果

收益：

- 在 transport 延迟发生前保住主机本地证据
- 让治理和 action logic 远离生产主机
- controller 状态和 workflow API 更容易集中检查

成本：

- 引入 transport、replay 和 reconstruction 复杂度
- 必须显式处理 source health 和 fallback

### 当前边界

- 这仍是单仓库内的 collector/controller 架构，不是 multi-tenant hosted control plane
- controller 的重启恢复能力依赖本地持久化选择，而不是分布式控制平面后端

## ADR-002：以 probe-core 为主源，并保留 compatibility fallback

### 状态

已接受，已实现。

### 背景

仓库希望在可能时获得更高保真的主机采集，但在 native probe-core 不可用或 stale 时，系统又必须保留退化路径。

### 决策

通过 [`../../backend/internal/collector/source_pipeline.go`](../../backend/internal/collector/source_pipeline.go) 优先使用 probe-core；但在以下情况允许回退到 Go compatibility probe：

- probe-core 启动失败
- 最新 probe-core frame 已经过期
- probe-core 被禁用且允许 fallback

### 实现

关键行为：

- `sourcePipeline.Collect` 优先读取 `primary.Latest(cfg.ProbeCore.StaleAfter)`
- `sourceCollection` 携带 `source`、`compatibilityFallback`、`fallbackReason`、`primaryExpected` 和 `primaryHealthy`
- probe-core 健康度会重新暴露成 collector metric，让 ingest 和后续 controller 逻辑看到的不只是 host metric，还有 source quality

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 只用 probe-core | probe-core 健康时更高保真，但 native path 一旦不可用就会变脆 |
| 只用 compatibility | 更简单，但失去 native framing、module 选择和更高保真采集 |
| 每轮同时合并两条路径 | 增加成本和重复语义，而当前设计看不到清晰收益 |

### 窄范围行业对比

- 这更像 staged degradation，而不是“单一 exporter 永远输出同一种 canonical stream”的模式。
- 它刻意比那类同时运行多条采集管道、再集中 reconcile 的系统简单得多。

### 后果

收益：

- 出现退化时仍能 graceful degrade，而不是硬失败
- 证据路径中会显式带出 source-health marker

成本：

- probe 来源本身又成了一维后续消费者必须理解的状态

### 当前边界

- compatibility fallback 并不等价于 probe-core
- 即使 pipeline 继续存活，native collection stale 或失败仍会降低证据保真度

## ADR-003：在传输前抑制，而不是在中心端重建 delta

### 状态

已接受，已实现。

### 背景

collector 在 steady state 下会携带大量重复的 runtime、hardware、process 和 aux payload 信息。如果每轮全量重发，就会快速拉高：

- 线传输成本
- spool 增长
- collector CPU 和序列化成本
- ingest churn

### 决策

在主机侧、transport 之前执行 suppression，但同时发出显式 marker metric，让 controller 能安全重建语义。

### 实现

主要代码：

- [`../../backend/internal/collector/metric_suppression.go`](../../backend/internal/collector/metric_suppression.go)
- [`../../backend/internal/collector/process_payload_suppression.go`](../../backend/internal/collector/process_payload_suppression.go)
- [`../../backend/internal/collector/aux_sampling.go`](../../backend/internal/collector/aux_sampling.go)

关键 marker：

- `collector_metrics_partial_update`
- `collector_metrics_suppressed_count`
- `collector_process_payload_refreshed`
- `collector_process_payload_suppressed`
- `collector_aux_payload_refreshed`
- `collector_aux_payload_suppressed`

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 永远发送全量 payload | 运行上简单，但在平静 steady state 成本太高 |
| 静默省略而不带 marker | 不安全，因为 controller 无法区分“没变”和“没采到” |
| 完整 delta 协议 | 可以做，但比当前 marker + reconstruction 的方法复杂得多 |

### 窄范围行业对比

- 通用 observability exporter 往往倾向于无状态发射，再由后端去 dedupe 或压缩。这个仓库刻意把一部分工作前移到 edge，因为 collector 最了解哪些主机本地状态其实没变。

### 后果

收益：

- steady-state batch 更小
- collector 和网络成本更有上界
- 后续 controller reconstruction 拥有显式语义

成本：

- ingest 必须正确理解 suppression marker
- partial update 语义比“永远全量发送”复杂

### 当前边界

- marker 一旦出错，会影响所有下游消费者
- process suppression 基于 fingerprint，本质上在强制刷新间隔内是有损的

## ADR-004：使用磁盘本地 spool，并以 ACK 为提交点

### 状态

已接受，已实现。

### 背景

controller 卡顿和网络分区，往往正发生在证据最有价值的时候。direct send 或纯内存重试缓冲，会让采集质量过度依赖远端健康。

### 决策

每个序列化后的 batch 先写入有界本地 spool，只有在 controller 返回匹配 `batch_id` 的 ACK 后才推进提交偏移。

### 实现

主要代码：

- [`../../backend/internal/collector/spool/spool.go`](../../backend/internal/collector/spool/spool.go)
- [`../../backend/internal/collector/transport/client.go`](../../backend/internal/collector/transport/client.go)

关键机制：

- `spool.log` 保存 append-only、带长度前缀的记录
- `spool.offset` 保存已提交读偏移
- `Next()` 读取但不推进
- `Commit(nextOffset)` 只在发送成功且 ACK 校验通过后推进
- 有界 compact 在必须腾空间时淘汰最旧 unread 记录

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 只做 direct stream | transport 故障会立刻变成证据丢失或采集延迟 |
| 只做 RAM buffer | 跨 crash/restart 的 durability 更差，也更难安全地有界 |
| 引入外部消息总线 | 全局语义更强，但会增加运维依赖，并削弱 node-local isolation |

### 窄范围行业对比

- 相比常见只在内存里 retry 的 metrics exporter，这个设计愿意多付一些复杂度来保住短故障下的本地证据。
- 相比完整的分布式日志或 queue 服务，这里又刻意保持 local-first 和 bounded。

### 后果

收益：

- 具备 crash-resistant 的本地 replay 语义
- 采集过程和 controller 卡顿解耦

成本：

- 本地磁盘 IO
- compact 与 corruption-recovery 逻辑
- 长故障下 backlog 有上限

### 当前边界

- 长故障仍会淘汰最旧 unread 数据
- 这不是 exactly-once 的分布式 ingest pipeline

## ADR-005：在 ingest 处重建 controller 自有事实模型

### 状态

已接受，已实现。

### 背景

collector 一旦开始 suppression 或 cadence，不同下游组件就必须有一个统一地点来解释“本轮没有出现的数据”到底意味着：

- 没变
- 被清空
- 丢失了

如果每个消费者自己解释，UI、query-service 和 workflow 一定会分叉。

### 决策

在 ingest 层统一解析 suppression、refreshed-empty 和结构化字段抽取，输出 controller 自有的 `NodeSnapshot`。

### 实现

主要代码：

- [`../../backend/internal/controller/ingest/server.go`](../../backend/internal/controller/ingest/server.go)
- [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go)

例子：

- `collector_metrics_partial_update` 会 carry forward 选中的 collector/runtime/hardware metric 和结构化 runtime 字段
- `collector_aux_payload_refreshed{component=logs|process_fallback}` 且 payload 为空时，会清空旧 logs 或旧 process
- `node_security_finding` 和 `node_ebpf_runtime_event` 会被抽取到结构化字段，而不是只保留在 flat metric map 里

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 让每个 API 自己重建视图 | 语义不一致，复杂度重复 |
| 让 collector 完全 stateful，并直接发送 controller-ready state object | payload 更大，而且把太多解释逻辑放到了主机上 |
| 只存原始 batch，后续按需推导 | 每个消费者都会反复付 reconstruction 成本，且更容易解释不一致 |

### 窄范围行业对比

- 这更像 application-specific ingest normalization layer，而不是“先全存 raw signal，之后再解释”的通用 telemetry backend。

### 后果

收益：

- query、UI 和 workflow 共享同一事实模型
- runtime/security/storage 字段可以显式结构化

成本：

- ingest 变成一个关键语义边界
- marker 语义变化时必须极其谨慎

### 当前边界

- controller 热存储目前仍主要是内存模型加可选 snapshot persistence，不是 append-only evidence journal

## ADR-006：热路径只保留 trend-safe 的有界历史

### 状态

已接受，已实现。

### 背景

controller 需要历史来支持趋势和 predictive 逻辑，但不是每个 metric 都值得进入热历史。无界全量保留会迅速抬高内存、TSDB 和查询成本。

### 决策

只对 trend-safe 指标白名单保留热历史，并可选导出到 TSDB。

### 实现

主要代码：

- [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go)
- [`../../backend/internal/controller/timeseries/service.go`](../../backend/internal/controller/timeseries/service.go)

白名单实现位于 `shouldStoreTrendMetric(...)`。

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 存所有指标和所有 label | 贵、噪声大，而且不符合当前推理模型 |
| controller 完全不保留历史 | 更简单，但趋势和 predictive 能力会弱太多 |
| 把所有历史都外包给外部 TSDB | 可以，但会让当前控制面更不自包含，TSDB 不可用时也更脆弱 |

### 窄范围行业对比

- 相比 TSDB-first 的 observability stack，这个仓库保留的历史面非常窄，因为它优化的是 RCA 和 weak-signal 逻辑，而不是做通用 metrics warehouse。

### 后果

收益：

- controller 的内存和 TSDB 成本可预测
- forecasting 和 weak-signal 使用的信号集更干净

成本：

- 新指标不会自动拥有历史
- 一些 operator 问题仍然只能靠 current-state，而不是长时历史分析

### 当前边界

- 某个 metric 即使存在于当前 batch 中，也可能因为不在白名单里而完全不进入 trend 逻辑

## ADR-007：分离单序列趋势逻辑与多变量弱信号相关性

### 状态

已接受，已实现。

### 背景

仓库需要回答两类不同问题：

- 某一个信号族是否在持续恶化？
- 多个强度一般的信号现在是否形成了可信组合？

如果只保留一条评分路径，这两个问题会被混在一起。

### 决策

保持单序列 trend/predictive 逻辑与多变量 weak-signal fusion / RCA correlation 分离。

### 实现

主要代码：

- [`../../backend/internal/controller/predictive/engine.go`](../../backend/internal/controller/predictive/engine.go)
- [`../../backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)
- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)

单序列路径：

- `buildRiskSeries`
- `buildTrendAssessments`
- `predictive.Evaluate`

多变量路径：

- `buildRiskSignals`
- `buildCooccurrences`
- `buildScopeRisks`
- `buildInvestigationEvents`

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 一个不透明 anomaly score | 对外更简单，但更难解释和质疑 |
| 更大的 learned anomaly ensemble | 表达能力更强，但对当前仓库来说太重，也不够可检查 |

### 窄范围行业对比

- 相比 Datadog Watchdog 这类范围更广的 anomaly/correlation 产品，这里的逻辑更小、更确定、更容易检查，代价是自动覆盖面更窄。

### 后果

收益：

- operator 能明确知道证据来自 drift、co-occurrence，还是两者都有
- 两条路径可以分别调参和审查

成本：

- 内部评分路径更多，文档和维护成本更高

### 当前边界

- 短窗口和 heuristic threshold 仍然限制了能够被提前发现的复合 incident 类型

## ADR-008：对混合检索做 gating，并把 incident memory 独立出来

### 状态

已接受，已实现。

### 背景

系统需要外部化知识，但如果每次 query 或 workflow step 都把检索文本无脑附加上去，只会引入噪声并降低 operator 信任。

同时，仓库还需要区分：

- 静态知识和 runbook
- 系统自身记录的历史 incident 与 action outcome

### 决策

对规范化 dataset 采用 gated hybrid retrieval；incident memory 保持独立 durable source，也可被检索，但使用不同排序。

### 实现

主要代码：

- [`../../backend/internal/controller/rag/`](../../backend/internal/controller/rag/)
- [`../../backend/internal/controller/incidentmemory/store.go`](../../backend/internal/controller/incidentmemory/store.go)
- [`../../backend/internal/controller/agentcore/workflow_memory.go`](../../backend/internal/controller/agentcore/workflow_memory.go)

关键行为：

- query-service 通过 `shouldAttachQueryServiceRAG(...)` 决定是否附带 RAG
- 低于 `RAGMinConfidence` 的命中会被抑制
- hybrid retrieval 组合 lexical 和 vector 分数
- incident memory 采用 lexical + heuristic 的确定性排序，考虑 signals、actions、changes、trust、feedback、collector affinity 和 recency

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 永远附带 retrieval context | 太吵，也太容易误导 |
| retrieval 只靠 vector similarity | 更难解释，也不符合当前 local-first index 设计 |
| 把 incident memory 合并进同一 dataset 排序路径 | 会混淆“通用知识”和“这里之前发生过什么” |

### 窄范围行业对比

- 相比“RAG 到处加”的模式，这个仓库把 retrieval 当作条件性证据源。
- 相比 learned memory store，这里的 incident memory 刻意保持 deterministic 和 local-first，好让 operator 能看懂为什么某条历史案例排在前面。

### 后果

收益：

- 返回的检索上下文更小、更相关
- 静态知识与历史 incident 的 provenance 更清楚

成本：

- 某些低置信但可能有用的命中会被丢掉
- 弱或陈旧 dataset 仍然会限制回答质量

### 当前边界

- retrieval 和 memory ranking 本质上都还是 heuristic
- 强语义类比仍然可能被漏掉

## ADR-009：使用 durable governed workflow runtime，而不是 free-form agent 执行

### 状态

已接受，已实现。

### 背景

仓库希望超越被动报告，但生成高风险 action 很容易，安全执行它们却代价极高。

### 决策

把 workflow 执行放在一个 durable、受治理的 runtime 之后，并显式持久化：

- run state
- plan revision
- tool call
- policy
- approval
- idempotency
- verification
- compensation
- evidence package
- memory write-back

### 实现

主要代码：

- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_tools.go`](../../backend/internal/controller/agentcore/workflow_tools.go)
- [`../../backend/internal/controller/agentcore/workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go)
- [`../../backend/internal/controller/agentcore/workflow_evidence.go`](../../backend/internal/controller/agentcore/workflow_evidence.go)
- [`../../backend/internal/controller/agentcore/workflow_memory.go`](../../backend/internal/controller/agentcore/workflow_memory.go)

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 由 free-form prompt 直接决定并执行工具 | 太难审计、回放和安全约束 |
| 只用非持久化的内存 workflow state | 更省事，但 restart 和 postmortem 检查会弱很多 |
| 引入分布式 workflow 引擎 | 规模语义更强，但远超当前仓库期望的运维复杂度 |

### 窄范围行业对比

- 相比 Temporal 风格的 durable workflow engine，这个仓库保留的是更小的 local-first runtime：Bolt 或内存 store、durable run struct 和显式 event log。它获得了 resumability 和 auditability，但并不宣称拥有 Temporal 的分布式保证。

### 后果

收益：

- run 可恢复
- approval 与 verification 边界显式存在
- evidence package 和 incident-memory write-back 成为一等能力

成本：

- 引入更多持久化和状态机代码
- durability 是 local-first，而不是分布式编排语义

### 当前边界

- verification 仍然是 heuristic
- rollback 依赖高质量 rollback command 和安全 action descriptor
- persistence 是 local-first，不是 HA workflow orchestration

## ADR-010：从现有 metric history 推导 recurring-burst 上下文

### 状态

已接受并已实现。

### 背景

workflow 之前有一个反复出现的误报模式：

- 某个服务越过了阈值
- recent baseline 因为 burst 很短，所以仍然显得偏低
- controller 又没有一份持久化的 workload 特定记忆，告诉它“这个服务经常就是这样，而且以前通常没有下游损伤”

典型例子：

- build 服务合法的 CPU / memory 峰值
- backup 或 artifact-upload 任务合法的 IO / network burst
- deployment helper 的短时 log burst
- 某些服务暴露了可选的 latency 指标，此时真正更能说明用户影响的是延迟回归，而不是资源数值本身

新设计同时必须满足几个约束：

- 不把重型 enrichment 推回 collector
- 状态必须有界
- 长窗口指标历史只能有一个事实来源
- suppression 必须能给 operator 一个明确解释

### 决策

把 recurring-burst 判别保留在 controller，但长窗口行为直接来自现有 metric-history 路径，而不是再写一套行为 profile store。

在真正的 workflow 决策路径里使用这层历史上下文，把活跃信号分类为：

- `expected_recurring_burst`
- `suspicious_deviation`
- `correlated_anomaly`
- `confirmed_anomaly`

### 实现

主要代码：

- [`../../backend/internal/controller/agentcore/behavioral_memory.go`](../../backend/internal/controller/agentcore/behavioral_memory.go)
- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)
- [`../../backend/internal/controller/agentcore/evidence_contract.go`](../../backend/internal/controller/agentcore/evidence_contract.go)
- [`../../backend/internal/controller/agentcore/workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go)

controller 现在只额外保留：

- 通过 `MetricHistoryProvider` 读取更长窗口历史
- 如果 provider 接在持久 timeseries 路径后面，就直接复用可选 TSDB 长历史
- 一个有界的内存 cache，用来复用最近几次历史查询

分类逻辑仍然是可解释的。workflow 会把当前 burst 和下面这些上下文做比较：

- `RiskSeries` 已经算好的 short baseline
- 长窗口历史得到的均值和波动
- 简单的小时级 recurrence
- logs、latency、runtime、安全等 corroborating evidence

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 把 `BaselineEngine` 扩展成主 recurring-burst memory | 它更像主机侧 drift 逻辑，而不是 workload 维度的 anomaly discrimination |
| 用 SQLite 或 BoltDB 存行为 profile | 这会为一个已有 metric-history 路径就能解决的问题再造第二套通用历史存储 |
| 为每个信号保存完整长时原始历史 | 更灵活，但会重复 telemetry 并增加读写成本 |
| 在 collector 侧做 learned suppression | 会破坏 collector 热路径成本约束，也让 suppression 更难集中审计 |

### 窄范围行业对比

- 相比“先把所有东西都存下来，再离线分类”的通用 observability backend，这个仓库使用的是更小的 workflow-local memory 层，因为它更看重低运维成本和 local-first 运作。
- 相比更复杂的季节性或 ML anomaly 系统，这个设计刻意偏向透明启发式和有界状态，而不是追求统计复杂度。

### 后果

收益：

- 已知 bursty workload 的误报更少
- 可以在不改变 collector 行为的前提下做 workload-aware suppression
- 当 controller 降权或升权某个信号时，会留下显式 evidence

成本：

- suppression 的质量仍然依赖现有长窗口历史本身的质量
- workload 身份质量现在更重要，因为不稳定的 service naming 会打碎记忆
- 时间模型故意保持粗粒度

### 当前边界

- trace 行为还没有用同样深度做 baseline
- fleet-level peer comparison 还没实现
- 没有单独的持久 suppression ledger；如果 metric-history 路径很稀疏，workflow 会保持保守

## ADR-011：把 RCA 分析与 validation / action 分开

### 状态

已接受并已实现。

### 背景

durable runtime 原本已经有 governed tool、approval、verification 和 replay，但 RCA 读起来仍然太像一个混合式 agent：

- analysis 与 hypothesis generation
- recommendation drafting
- tool-driven follow-up check
- guarded execution planning

这样会让 runtime 更难审计，因为两个问题混在了一起：

- analysis 阶段一开始相信什么
- 后续 tool-driven validation 到底确认了什么、反驳了什么

### 决策

保留同一个 durable runtime、同一个 policy 层、同一个 governed tool gateway，但把 RCA 路径拆成两个明确角色：

- `AnalysisAgent` 负责产出结构化 `AnalysisHandoff`
- `ValidationActionAgent` 负责消费 handoff，并跑一个有界的 ReAct-style validation/action loop

### 实现

主要代码：

- [`../../backend/internal/controller/agentcore/analysis_handoff.go`](../../backend/internal/controller/agentcore/analysis_handoff.go)
- [`../../backend/internal/controller/agentcore/validation_agent.go`](../../backend/internal/controller/agentcore/validation_agent.go)
- [`../../backend/internal/controller/agentcore/validation_types.go`](../../backend/internal/controller/agentcore/validation_types.go)
- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go)

handoff 会持久化：

- incident summary
- ranked hypotheses 与 suspected causes
- supporting / weak / contradicting evidence IDs
- recommendations
- telemetry quality
- unresolved gaps
- suggested validation targets

validation 侧会持久化：

- per-target verdict
- per-iteration loop record
- tool sequence 与 observation summary
- validated / rejected recommendation IDs
- contradiction summary
- post-action validation outcome

### 考虑过但未采用的方案

| 方案 | 为什么这里不选 |
| --- | --- |
| 保留一个混合 RCA loop | 更难审计，也更容易把 analysis 和 action 混在一起 |
| 单独再造第二套 runtime | 会引入没必要的基础设施和重复治理路径 |
| 把第二个 agent 做成纯 prompt 驱动 | 太不透明；tool 选择和 stop condition 需要显式代码路径 |

### 后果

收益：

- reasoning 和 validation 的边界更清楚
- 可以在不削弱治理边界的前提下扩充第二个 agent 的工具面
- handoff 与 validation state 进入 durable report 后，replay 和 incident 审计都更清楚

成本：

- workflow 持久化状态更多
- tool catalog 更大，需要额外测试和文档

### 当前边界

- validation loop 按设计就是有界且保守的，不是开放式 autonomous agent
- guarded execution 仍然保持 approval-first、dry-run-first
- post-action validation 比以前丰富，但在更深证据不可用时仍会回退到 deterministic joint-risk check

## 下一步阅读

- [架构](04-architecture.md)
- [Pipeline 深度解析](02-pipeline-deep-dive.md)
- [事故 Agent 运行时](17-incident-agent-runtime.md)
