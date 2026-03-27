# 架构

English version: [docs/en/04-architecture.md](../en/04-architecture.md)

本页解释当前 `v0.8` 已经落地在代码里的架构，而不是理想化的架构图。

如果你想先理解这些代码为什么这样拆边界、为什么这样存状态、为什么这样设置安全边界，先读本页。然后再读 [Pipeline 深度解析](02-pipeline-deep-dive.md) 看完整数据路径，再读 [事故 Agent 运行时](17-incident-agent-runtime.md) 看受治理的工作流控制环。

## 本页内容

- 架构命题
- 精确运行时边界
- 主机侧允许持有哪些状态
- 哪些状态必须留在 controller
- 热状态与持久状态
- 连续执行的工作与延后执行的工作
- 信任边界
- 为什么这里选择这些具体技术
- 故障隔离与当前边界
- 下一步阅读

## 架构命题

系统围绕一个操作前提构建：

> 保住短时证据最便宜的地方是主机；分析、关联和治理这些证据最便宜的地方是 controller。

这个前提直接解释了：

- 为什么 [`cpp/probe_core/`](../../cpp/probe_core/) 和 [`../../backend/internal/collector/`](../../backend/internal/collector/) 必须运行在被观测节点上
- 为什么抑制（suppression）要发生在传输（transport）之前
- 为什么 collector 选择有界本地 spool，而不是只靠内存缓冲
- 为什么 controller 要在 [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) 里重建统一的 `NodeSnapshot`
- 为什么热历史路径只保留 trend-safe 指标白名单
- 为什么检索（retrieval）是 gated 的，而不是无条件向 prompt 塞上下文
- 为什么 incident agent 要持久化 durable run、approval、verification、compensation、evidence package 和 incident memory

## 精确运行时边界

| 边界 | 主要代码 | 拥有什么 | 明确不拥有什么 | 为什么这样拆 |
| --- | --- | --- | --- | --- |
| 主机观测层 | [`../../cpp/probe_core/`](../../cpp/probe_core/), [`../../backend/internal/collector/`](../../backend/internal/collector/) | 采样、source 选择、本地 cadence、抑制、aux 缓存、spool、重试/回放、主机自保护 | RCA 合成、长时历史、RAG、变更关联、因果排序、workflow 编排 | 主机能最低成本读取 `/proc`、`/sys`、GPU 和 eBPF 状态，但不应该承担重型控制平面 |
| controller 事实层 | [`../../backend/internal/controller/ingest/`](../../backend/internal/controller/ingest/), [`../../backend/internal/controller/logindex/`](../../backend/internal/controller/logindex/), [`../../backend/internal/controller/gpuobs/`](../../backend/internal/controller/gpuobs/) | 校验、去重、`NodeSnapshot`、有界趋势历史、日志索引、GPU 摘要、面向 API 的热状态 | 主机本地采集和投递保证 | 后续所有 API、UI 和 workflow 都必须共享同一个“当前事实面” |
| controller 推理层 | [`../../backend/internal/controller/predictive/`](../../backend/internal/controller/predictive/), [`../../backend/internal/controller/changeintel/`](../../backend/internal/controller/changeintel/), [`../../backend/internal/controller/causalgraph/`](../../backend/internal/controller/causalgraph/), [`../../backend/internal/controller/rag/`](../../backend/internal/controller/rag/) | 趋势评分、弱信号融合、变更关联、检索、因果排序、历史 incident 查找 | 原始主机采样和底层缓冲 | 这些步骤更贵，更适合集中观察和调试，而且它们必须建立在规范化证据之上 |
| 受治理的 incident runtime | [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go), [`../../backend/internal/controller/agentcore/workflow_tools.go`](../../backend/internal/controller/agentcore/workflow_tools.go), [`../../backend/internal/controller/agentcore/workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go) | durable run、plan step、tool call、policy、approval、verification、compensation、evidence package、incident memory write-back | 直接主机采样、无治理的自主执行 | 行动是最高风险层，必须被 durable state 和显式安全检查隔离出来 |

### 为什么受治理 runtime 现在拆成两个 agent

在这个 governed incident runtime 内部，RCA 已经不是一个混合式控制环，而是两个明确角色：

- `AnalysisAgent`：把当前证据整理成 incident synthesis、hypothesis、ranked causes、recommendation，以及结构化 `AnalysisHandoff`
- `ValidationActionAgent`：消费这个 handoff，按 target type 选择更丰富的 tool，做 contradiction search、recommendation validation，并负责 guarded execution planning 与 post-action validation

这样拆不是为了“多 agent 更高级”，而是因为两个阶段的失败模式不同：

- analysis 更依赖跨证据相关性和解释质量
- validation/action 更依赖正确的 tool 选择、预算控制、policy 边界和 before/after 验证

拆开之后，最终报告也更容易审计。operator 可以先看到分析阶段最初相信什么，再看到验证/动作阶段究竟确认了什么、否定了什么、还剩什么不确定性。

## 为什么这种边界划分在运维上更可靠

这套边界不是为了画出更好看的模块图，而是为了把不同故障模式隔离开。

如果 collector、事实模型、RCA、retrieval、tool execution 混成一层，值班时很难判断：

- 是采样没抓到证据
- 还是 ingest 没正确续接状态
- 还是趋势逻辑误判了 steady-state 抖动
- 还是 retrieval 把无关 runbook 混进来了
- 还是 workflow 被 policy 或 approval 阻断了

当前的拆法让每一层只承担一种主要责任：

- collector 负责“证据不要丢”
- ingest 负责“事实不要乱”
- analysis 负责“把症状压成更小的结构化证据”
- retrieval 负责“只有在值得时才补上下文”
- workflow runtime 负责“行动必须可治理、可验证、可回放”

这就是为什么这个仓库虽然链路比“抓点日志直接问模型”更长，但长期更容易维护，也更适合在生产环境里定位问题。

## 主机侧允许持有哪些状态

主机侧只允许保留那些直接用于保护采集质量或控制主机成本的状态。

| 主机侧状态 | 存放位置 | 为什么允许存在 | 为什么必须有界 |
| --- | --- | --- | --- |
| 最新 probe-core frame | `backend/internal/collector/probecore/client.go` | collector 需要拿到最近一次 `ProbeBatch`，而不是每次都重新解析子进程流 | 只保留最新一帧快照 |
| probe-core 进程内队列和 writer buffer | `cpp/probe_core/main.cpp` | 不同 probe 模块 cadence 不同，需要和串行化过程解耦 | 队列深度有界，压力下允许丢弃旧 frame |
| aux 采集缓存 | `backend/internal/collector/aux_sampling.go` | process fallback、logs 和 external helper 比主循环慢，故意不能每轮都重刷 | 每种 helper 只缓存最新 payload 和上次采集时间 |
| 抑制 fingerprint | `backend/internal/collector/metric_suppression.go`, `backend/internal/collector/process_payload_suppression.go` | collector 必须知道低变化指标或 process payload 是否真的变了 | 只保存上一次发出的 fingerprint/value 和 timestamp |
| 本地 spool | `backend/internal/collector/spool/spool.go` | transport 卡顿不能立刻变成证据丢失 | spool 有最大大小，满了会淘汰最旧 unread 记录 |
| transport 连接缓存 | `backend/internal/collector/transport/client.go` | 不能每次发送都重建 gRPC/TLS 连接 | 连接复用而不是无限累积 |
| protection governor 样本基线 | `backend/internal/collector/protection.go` | collector 自身 CPU/RSS 和 backlog 必须反向影响 cadence 和 shed 策略 | 只保留最近一段自观测样本基础 |

明确不允许在主机侧演变成“本地数据库”的状态包括：

- 长时 per-node 趋势历史
- 跨节点或 fleet 状态
- RAG 索引
- change intelligence 记录
- incident memory
- durable workflow run

这些状态必须留在 controller，因为它们需要共享语义、集中检查和持久化 API。

## 哪些状态必须留在 controller

controller 负责那些会被多个 API、workflow 或时间窗口复用的状态。

| controller 状态 | 主要类型或存储 | 为什么必须集中 |
| --- | --- | --- |
| 当前规范化节点视图 | [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) 中的 `NodeSnapshot` | prompt、UI、joint-risk、RCA 和 workflow tool 必须读取同一个重建后的节点状态 |
| 有界热历史 | [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) 中的 `ring.Ring[MetricHistorySample]` | 趋势和 predictive 逻辑需要 controller 持有的历史窗口 |
| 可选长窗口 metric store | [`../../backend/internal/controller/timeseries/service.go`](../../backend/internal/controller/timeseries/service.go) 中的 `timeseries.Service` | 历史查询可能需要跨 controller 重启存在，不能放在主机上 |
| 可检索日志索引 | `logindex.Index` | 日志证据会被 query-service 和 workflow tool 反复复用 |
| 行为基线缓存 | [`../../backend/internal/controller/agentcore/behavioral_memory.go`](../../backend/internal/controller/agentcore/behavioral_memory.go) 里的有界内存 cache | recurring-burst 判别会在一个 workflow burst 内多次读取同一段长窗口历史；cache 只负责省一次查询，不会变成第二个事实源 |
| 变更记录 | 默认写在 `data/agent/workflows/changeintel/` 的 JSON 文件 | 变更关联是跨 batch、甚至跨 run 的，必须能跨重启保留 |
| incident memory | 默认写在 `data/agent/workflows/incident_memory/` 的 JSON 文件 | 过去的 incident 只有能被后续 workflow 找回时才有意义 |
| durable workflow run | 默认写在 `data/agent/workflow_runs.db` 中的 `DurableRun` | 受治理执行要求 run state 可恢复、可审计 |
| workflow evidence package | 默认写在 `data/agent/workflows/evidence/` 的 JSON 工件 | operator 需要重建 workflow 当时真正看到了什么 |
| RAG 索引 | `data/agent/rag/` 下的 `index.json` 和可选 vector sync 状态 | 规范化、chunking 和 search 需要被 query 和 workflow 共享 |

### 为什么 recurring-burst 判别留在 controller

这次功能要解决的误报问题，并不是主机采样问题，而是分类问题：

- 一个 build、backup 或 deploy 相关 burst 在单个时间窗里看起来可能非常严重
- 只有跨多个 workflow evaluation 的历史上下文，才能判断它是不是“经常这样但通常没有下游损伤”

把这层逻辑放在 controller，而不是主机侧，有三个直接收益：

- 不给 collector 增加新的本地磁盘写入
- 不在 collector 热路径引入新锁、新后台聚合循环或 retention 管理
- suppression 决策能直接复用 workflow 已有的 logs、runtime、安全和 topology 旁证

代价也很明确：

- suppression 只发生在 controller 侧 workflow 决策里，不会改变原始 telemetry 发射
- 长窗口历史仍然要查，但它直接来自现有 metric-history provider 和可选 TSDB，而不是再造一套 profile store

这个取舍在当前仓库里是对的，因为 collector 的职责是保住证据，而不是替 controller 做跨 run 的 incident 判别。

## 热状态与持久状态

当前实现刻意使用了不同的状态类别。不是所有东西都值得持久化，也不是所有东西都适合重启后重算。

| 状态类别 | 例子 | 当前实现 | 为什么这样选 | 限制 |
| --- | --- | --- | --- | --- |
| 纯热内存状态 | 活跃 `NodeSnapshot`、`ProcessResources`、`ProcessGraphSnapshot`、最近 `MetricHistorySample` ring、query-service cache | `MemoryStore` 和进程内 index/cache | 每个 API 和 workflow step 都需要快速读取 | controller 重启会清空，除非启用了 ingest snapshot 持久化 |
| 热状态 + 可选持久化 | ingest snapshot 和 history | [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) 的后台快照持久化 | 保留内存语义的同时，允许重启恢复 | 这是 snapshot 风格，不是 append-only event log |
| 持久本地文件或 BoltDB | spool、workflow run、evidence package、changeintel、incident memory、RAG index | `spool.log`、`spool.offset`、Bolt bucket、JSON 工件 | 为 local-first 场景提供足够 durability，而不引入分布式后端 | 不是 multi-controller 分布式协调系统 |
| 可重算派生状态 | trend assessment、weak-signal cluster、causal ranking、retrieval result | 从热状态、历史、知识库和 workflow 输入重建 | 避免把所有派生视图永久存下来 | 结果依赖上游保留的证据，代码变化后也可能变化 |

### 默认持久化路径

这些默认路径来自当前代码：

- workflow store: `data/agent/workflow_runs.db`
- workflow data root: `data/agent/workflows`
- workflow evidence package: `data/agent/workflows/evidence/`
- incident memory: `data/agent/workflows/incident_memory/`
- change intelligence: `data/agent/workflows/changeintel/`
- RAG index: `data/agent/rag/index.json`

## 连续执行的工作与延后执行的工作

这个仓库最关键的架构选择之一，是明确什么工作足够便宜，可以每个采集周期都做；什么工作更贵，必须等 operator 或 workflow 真的需要时再做。

| 工作类别 | 当前实现 | 为什么可以连续做 | 为什么不是所有工作都连续做 |
| --- | --- | --- | --- |
| 便宜的连续采集 | host metrics、storage/network 摘要、GPU 摘要、collector 自身指标 | 这些是第一层健康信号，必须尽早发现恶化 | 即使这些也仍受 cadence 控制，protection 压力下还会退让 |
| 有界的连续重建 | ingest 校验、去重、carry-forward、`NodeSnapshot` rebuild、趋势历史追加 | 后续 controller 路径都立即依赖这些状态 | controller 只保留经过筛选的历史和结构化视图 |
| 延后 helper 采集 | process fallback、logs、external command | 这些有用但更贵，所以 `aux_sampling.go` 会缓存并标记 refresh/suppressed | 如果强制每轮都采，会显著抬高主机 steady-state 成本 |
| 延后长窗口持久化 | timeseries export | 长时保留有价值，但不是每个 controller 决策都需要 | write queue 会满，而且只有白名单指标会导出 |
| 延后 recurring-burst 历史查询 | [`../../backend/internal/controller/agentcore/behavioral_memory.go`](../../backend/internal/controller/agentcore/behavioral_memory.go) 里的长窗口历史读取和短期 cache | 这层上下文只在 workflow evaluation 时需要，而不是每次 ingest append 都需要 | 把这件事留在 collector 和 ingest 热路径之外，能避免 steady-state 成本和 cardinality 膨胀 |
| 延后检索 | RAG 和 incident-memory query | 静态知识只有在 query 或 findings 足够具体时才真正有帮助 | 无条件“塞上下文”只会增加噪声和 operator 不信任 |
| 延后执行 | profiling 和 remediation tool | action 是最高风险步骤，必须先通过 policy 和 approval | 假装所有 action 都适合自动执行，在运维上是不诚实的 |

## 信任边界

系统不会把每一层输入都视为同等可信。

```text
原始主机证据
  -> controller 规范化证据
     -> 派生信号与相关性
        -> 检索到的知识与历史 incident
           -> LLM 推理
              -> 工具执行
```

### 1. 原始主机证据

例子：

- probe-core 输出的 `ProbeBatch`
- compatibility probe 指标
- collector 侧的 `node_security_finding` 和 `node_ebpf_runtime_event`
- log fingerprint 和 process sample

信任模型：

- 离机器最近，保真度最高
- 但仍会受到 blind spot、能力缺失、suppression 和 frame drop 影响
- 还没有被整理成下游推理所需的结构

### 2. controller 规范化证据

例子：

- `TelemetryBatch`
- `NodeSnapshot`
- `ProcessResources`
- `StorageDevices`、`Filesystems`、`RuntimeSecurityEvents`、`SecurityFindings`

信任模型：

- controller 把这一层视为共享事实模型
- partial-update、cleared-state 和结构化字段的语义都在这里被解析
- 一旦这里出错，会影响所有下游消费者，所以这层必须显式而稳定

### 3. 派生信号与相关性

例子：

- `TrendAssessment`
- predictive `Finding`
- weak-signal cooccurrence
- change correlation
- causal graph 的 cause / impact path

信任模型：

- 确定性、可检查，但本质仍是 heuristic
- 比原始阈值判断强，但不是 ground truth
- 应该影响 confidence，而不是被误当成确定事实

### 4. 检索到的知识与历史 incident

例子：

- `SearchHit`
- workflow 中以 `incident_memory` 暴露的历史匹配

信任模型：

- retrieval 只被当作支持性证据，不是执行指令
- query-service 会显式抑制低于 `rag_min_confidence` 的命中
- 实际价值取决于本地 dataset 质量和 incident outcome 记录质量

### 5. LLM 推理

例子：

- `QueryResponse`
- joint-risk 与 RCA 报告中的 LLM 段落

信任模型：

- 上限由上游 evidence bundle 决定
- 会受到 telemetry-quality ceiling、严格 schema 和 deterministic fallback 的约束
- 永远不能绕过 policy 或 approval 直接执行高风险 action

### 6. 工具执行

例子：

- profiling
- remediation
- rollback

信任模型：

- 默认最不可信的一层
- 必须经过 `workflow_tools.go`、policy、approval、idempotency、verification 和 compensation record
- 这也是为什么该 runtime 是受治理的控制环，而不是“能调用 shell 的聊天机器人”

## 为什么这里选择这些具体技术

这个代码库反复选择的是规模更小、可检查的机制，而不是更宽泛但更难界定的机制。

| 技术选择 | 工程原因 | 运维原因 | 安全原因 | 成本 | 限制 |
| --- | --- | --- | --- | --- | --- |
| collector/controller split | 主机采集和集中推理的成本模型根本不同 | 即使 controller 短时不健康，主机侧证据仍可保留 | 高风险推理和 action 不会跑在生产主机上 | 要维护两个 runtime | 必须跨 transport 重建状态 |
| transport 前抑制 | 重复 runtime/hardware/process payload 最容易在源头识别 | 降低 calm period 的带宽、spool 增长和 controller churn | 显式 marker 比静默省略更安全 | ingest 必须理解 suppression 语义 | 比“永远全量发送”更复杂 |
| 用磁盘 spool 而不是纯内存缓冲 | ACK commit 和 replay 更容易做 crash-tolerant | controller/network 短故障不会立刻造成证据丢失 | collector 可以继续采样，而不是被 delivery 热路径卡住 | 引入本地磁盘 IO 和 compact 逻辑 | 长时间故障仍会淘汰最旧 unread 数据 |
| 用有界热历史而不是完整原始保留 | 趋势逻辑只需要一小组经过筛选的指标 | controller 的内存和 TSDB 成本可预测 | 更容易解释 forecast 的依据 | 白名单外指标不会被长时保留 | 新指标不会自动变成 forecastable |
| 分离单变量趋势与多变量弱信号 | 特征工程和输出契约不同 | operator 必须能区分“一个指标恶化”和“多个弱信号形成组合” | 避免所有东西都藏进一个不透明分数 | 代码路径更多 | 有些 incident 仍然会跨越两条路径 |
| gated retrieval 而非无条件 prompt stuffing | query 越具体，retrieval 越相关 | 减少噪声和泛化建议 | 弱命中不容易误导回答路径 | 有些低置信但可能有用的提示会被丢掉 | telemetry 很强但 dataset 很弱时，retrieval 仍然弱 |
| durable governed workflow runtime 而非临时工具调用 | policy、approval、verification 和 audit 需要共享状态模型 | operator 可以检查 run、resume run，并做 replay | 高风险执行不能藏在 prompt 文本后面 | 引入持久化、evidence packaging 和 step bookkeeping | local-first durability 不是分布式 workflow 平台 |

## 常见替代做法为什么这里没选

下面这些替代方案都“看起来更简单”，但在当前仓库的约束下其实更难维护：

| 替代方案 | 为什么看起来诱人 | 这里为什么没选 |
| --- | --- | --- |
| 在节点上做更多推理 | 距离证据最近，似乎能更快 | 会把采集器变成高风险控制平面，主机成本和状态复杂度一起上升 |
| 所有 batch 都直接进 TSDB，再从 TSDB 推理 | 历史统一、查询方便 | TSDB 不是当前事实模型，partial update、suppression 语义和结构化 runtime/security 信号都需要先重建 controller 事实层 |
| 把所有检索都做成默认动作 | 看起来知识更全 | 很容易让 prompt 充满无关 runbook，反而降低回答质量和 operator 信任 |
| 只保留最终 RCA 文本，不保留中间 run 状态 | 存储更省，界面更简单 | 无法审计 policy、approval、verification，也无法重放或定位 workflow 为什么停住 |
| 在 collector 侧为每个 workload 维护完整行为 profile | 可以更早做 suppression | 会让采集热路径承担额外状态、持久化和 retention 管理，不符合 collector 的职责边界 |

## 故障隔离与当前边界

架构的目标之一，是让某一层失效时，不会立刻让其他层一起失效。

| 故障 | 当前如何隔离 | 仍会退化什么 |
| --- | --- | --- |
| probe-core 不可用或 stale | `source_pipeline.go` 可按配置切到 compatibility fallback | 保真度下降；部分 native signal 消失 |
| collector 压力或 spool 增长 | `protection.go` 可降低 cadence 并 shed optional work | logs、安全、external helper 或 process fallback 会变稀疏 |
| transport/controller 故障 | spool replay 和 ACK-based commit | 长时间故障仍会淘汰最旧 unread 记录 |
| collector 只发 partial update | `StoreMetrics` 的 carry-forward 语义和 aux refresh marker | marker 语义一旦出错，会影响所有下游消费者 |
| timeseries backend 失败 | `timeseries.Service` 可回落到内存历史 | 长窗口 retention 深度下降 |
| 知识库弱或噪声大 | retrieval gating 和 confidence suppression | 回答会更泛化 |
| action 失败或不安全 | approval gate、dry-run、verification、compensation record | runtime 仍然依赖正确的 rollback metadata 和 policy |

## 下一步阅读

- [Pipeline 深度解析](02-pipeline-deep-dive.md)：从主机到 operator 的逐阶段数据路径
- [事故 Agent 运行时](17-incident-agent-runtime.md)：durable run、tool gateway、approval、verification、compensation
- [架构决策记录](18-architecture-decisions.md)：collector/controller split、suppression、spool、有界历史、retrieval gate 与 workflow governance 的 ADR 风格说明
- [控制平面分析](07-control-plane-analysis.md)：controller 内部推理层如何组合
- [数据流](05-data-flow.md)：更宽视角的系统级图示
