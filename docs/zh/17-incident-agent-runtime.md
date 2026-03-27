# 事故 Agent 运行时

English version: [docs/en/17-incident-agent-runtime.md](../en/17-incident-agent-runtime.md)

本页解释 controller 侧 incident agent 在当前 `v0.8` 代码里的真实运行方式。

这不是 roadmap，也不是“未来会有的 agent 平台”介绍。它是对当前 runtime 的机制级说明，主要基于：

- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go)
- [`../../backend/internal/controller/agentcore/workflow_tools.go`](../../backend/internal/controller/agentcore/workflow_tools.go)
- [`../../backend/internal/controller/agentcore/workflow_memory.go`](../../backend/internal/controller/agentcore/workflow_memory.go)
- [`../../backend/internal/controller/agentcore/workflow_evidence.go`](../../backend/internal/controller/agentcore/workflow_evidence.go)
- [`../../backend/internal/controller/changeintel/`](../../backend/internal/controller/changeintel/)
- [`../../backend/internal/controller/causalgraph/`](../../backend/internal/controller/causalgraph/)
- [`../../backend/internal/controller/incidentmemory/`](../../backend/internal/controller/incidentmemory/)
- [`../../backend/internal/controller/evaluation/`](../../backend/internal/controller/evaluation/)

如果你还没看过 [Pipeline 深度解析](02-pipeline-deep-dive.md)，先去看 collector 到 controller 的证据路径。如果你想先理解这个 runtime 的状态边界、信任边界和持久化边界，先看 [架构](04-architecture.md)。

## 本页内容

- 为什么需要这套运行时
- 为什么它是受治理的控制环
- 为什么 RCA 现在是双 agent 运行时
- run 生命周期
- durable run 状态模型
- 事件与 step 持久化模型
- tool gateway 行为
- 幂等性与请求去重
- policy 与 approval 检查
- verification loop
- compensation 与 rollback 意图
- change intelligence、causal graph 与 incident memory
- 行为记忆如何进入决策环
- evidence package 与 world model 快照
- evaluation 与 replay hook
- operator 可见面
- 实际边界
- 下一步阅读

## 为什么需要这套运行时

在 durable workflow runtime 之前，controller 仍然可以做 incident 分析，但作为 operational agent 还不够完整：

- controller 重启后，run state 很难重建
- change evidence 更多只是间接线索
- 历史 incident 虽然能作为文本被检索，但还不是带 action outcome 的结构化 operational memory
- policy、approval、verification 和 compensation 分散在不同位置，而不是一个显式状态机

当前 runtime 解决的是一个更窄、但更可信的问题：

> 在不假装“无约束自主自动化是安全的”前提下，把基于证据的 incident 分析变成一个可恢复、可检查、受 policy 治理的控制环。

[`workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go) 里的默认姿态就是这个原则的直接体现：

- `DryRun = true`
- `RequireApproval = true`
- `AllowProfilingExec = false`
- `AllowRemediationExec = false`
- `VerificationWindow = 2m`
- `DegradedModePolicy = deterministic_only`

## 为什么它是受治理的控制环

这个 runtime 不是“LLM 输出 + 若干工具”。

它之所以是 governed control loop，是因为当前代码把下面这些状态都提升成了一等对象：

- workflow request contract
- durable run status
- plan revision 和 step record
- 每次 tool call 的 policy verdict
- 每个 guarded step 的 approval state
- idempotency key 和 action reuse
- step 后验证结果
- compensation / rollback outcome
- evidence package 和 incident-memory write-back

如果没有这些东西，系统只会是 controller API 外面包了一层聊天机器人。有了这些 durable object，它才成为一个边界明确、可事后检查的控制环。

## 为什么 RCA 现在是双 agent 运行时

RCA 路径现在不再是一个既分析又尝试行动的混合循环。

它已经拆成两个明确的 controller 侧角色：

- `AnalysisAgent`：负责 incident synthesis、hypothesis generation、evidence ranking、recommendation 草拟，以及 `AnalysisHandoff` 生成
- `ValidationActionAgent`：负责基于工具的 validation、contradiction search、recommendation check、guarded action planning 和 post-action validation

这样拆是工程上的需要，不是为了“多 agent 更炫”。

- analysis 更依赖跨 telemetry / security / change / incident memory 的相关性和排序
- validation/action 更依赖更准确的 tool 选择、更严格的 budget、更清晰的 policy 边界，以及 before/after 验证

这也是为什么 handoff 要作为结构化对象持久化，而不是留成自由文本。验证侧可以直接从 ranked causes、supporting evidence、weak evidence、recommendations、unresolved gaps、telemetry quality 和 suggested validation targets 开始，而不用重新推一遍整个 incident。

## run 生命周期

Joint-risk 和 RCA 的主入口分别是 [`workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go) 里的 `EvaluateJointRisk` 和 `BuildRCAWorkflow`。

durable 流程是：

```text
StartRunWithRequest
  -> analysis_handoff_finalize
     -> AttachAnalysisHandoff
        -> validation_action_react_loop
           -> RecordValidationLoop / AttachValidationReport
              -> [AttachApproval + SuspendRun] 或 [RecordVerification]
                 -> [必要时 RecordCompensation]
                    -> CompleteRun 或 FailRun
                       -> AttachEvidencePackage
                       -> AppendMemoryRecord
```

durable status 目前有四种：

- `running`
- `suspended`
- `completed`
- `failed`

RCA 控制环里的主执行阶段现在是 `validation_action_react_loop`。旧的 `plan_act_verify_loop` 只保留为兼容回退路径，在 validation agent 被关闭时才使用。

## durable run 状态模型

durable 状态模型定义在 [`workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go)。

### DurableRun

`DurableRun` 是顶层持久化记录。

| 字段组 | 关键字段 | 为什么存在 |
| --- | --- | --- |
| 身份与请求 | `RunID`, `WorkflowType`, `CollectorID`, `Request` | 把每个派生决策绑回原始 workflow request |
| 进度 | `Status`, `CurrentStep`, `CurrentStage`, `LastResumeAt` | 告诉 operator 这个 run 停在了哪里、从哪里恢复 |
| 审计时间线 | `Events` | 每次重要状态变化都会持久化成 `WorkflowEvent` |
| 治理执行 | `ToolCalls`, `PlanRevisions`, `Steps`, `AnalysisHandoff`, `Validation`, `ValidationLoops` | 保留分析阶段先得出了什么、验证/动作阶段实际检查了什么，以及哪些结论被验证或阻断 |
| durable 工件 | `EvidencePackage`, `WorldModel`, `MemoryRecords` | 把 supporting artifact 和主记录分开保存 |
| replay 与终态 | `ReplayCount`, `Result`, `Error` | 支持 replay 记账和 post-run 检查 |
| scratch context | `Context` | 保存挂起原因等运行时元数据 |

### step 级 durable 记录

每个 step 会被保存成 `DurableStepRecord`。

关键字段包括：

- 身份：`StepID`, `Stage`, `Order`, `Iteration`
- 目标动作：`Title`, `Tool`, `Query`, `Required`, `OriginalAction`
- 结果：`Status`, `ResultSummary`, `ErrorMessage`
- 证据：`EvidenceIDs`, `LastToolCallID`
- 治理状态：`Policy`, `Approval`, `Verification`, `Compensation`
- 时间：`StartedAt`, `CompletedAt`

之所以分出 step-level record，是因为一个 run 可能会有多次 plan revision，也可能在同一个 stage 上有多次 tool call。runtime 需要的是 step 状态，而不是只在最后保留一份报告 blob。

### 默认存储

当前代码默认使用：

- BoltDB 保存 run：`data/agent/workflow_runs.db`
- evidence package：`data/agent/workflows/evidence/`
- incident memory：`data/agent/workflows/incident_memory/`
- change intelligence：`data/agent/workflows/changeintel/`

如果 BoltDB 打不开，runtime 会回落到内存版 durable store。这样可以继续运行，但重启后的 durability 会立刻下降。

## 事件与 step 持久化模型

这个持久化模型有一个刻意的“冗余”特征，而且是有用的：

- 结构化最新状态直接保存在 `DurableRun` 上
- 重要状态变化也会追加成 `WorkflowEvent`

这意味着 runtime 同时保留：

- 每个 step、tool call、evidence reference 的最新状态
- 它们是如何走到这个状态的事件轨迹

### 当前会持久化什么

| 变更 | 主要方法 | 实际改变了什么 |
| --- | --- | --- |
| run 开始 | `StartRunWithRequest` | 初始化 `DurableRun` 并记录 `run_started` |
| stage 跳转 | `RecordStepTransition` | 更新 `CurrentStep` 和 `CurrentStage` |
| plan revision | `RecordPlanRevision` | 追加一份完整 `AgentPlanRevision` 快照，并记录 `plan_revision_recorded` |
| tool call | `RecordToolCall` | 追加 `WorkflowToolCall` 并记录 `tool_call_recorded` |
| step 更新 | `RecordStepState` | 按 `StepID` upsert `DurableStepRecord` |
| analysis handoff | `AttachAnalysisHandoff` | 持久化从 `AnalysisAgent` 传给 `ValidationActionAgent` 的结构化 packet |
| validation loop record | `RecordValidationLoop` | 持久化一次有界 ReAct 迭代里的 tool 选择、observation 和 verdict 变化 |
| validation report | `AttachValidationReport` | 持久化每个 target 的 verdict、validated/rejected recommendation、contradiction summary 和 post-action validation |
| policy 挂载 | `AttachPolicy` | 把 policy verdict 写到 step 上 |
| approval 挂载 | `AttachApproval` | 保存 pending 或 resolved approval state |
| verification | `RecordVerification` | 保存 step 后验证结果，并合并 evidence ID |
| compensation | `RecordCompensation` | 保存 rollback 或 skipped-compensation 状态 |
| evidence package | `AttachEvidencePackage` | 记录 artifact 路径 |
| world model | `AttachWorldModel` | 保存 topology、scope 和 recent-change 上下文 |
| memory write-back | `AppendMemoryRecord` | 记录写入的 incident-memory artifact 路径 |
| suspend | `SuspendRun` | 把 run 标记为 suspended，并记录 suspend reason |
| complete / fail | `CompleteRun`, `FailRun` | 保存终态和最终结果或错误 |

### 为什么需要这个模型

runtime 当然可以只保存最终报告，但那会失去很多关键治理信息：

- 哪些 step 被计划过但从未执行
- 哪个 tool call 被强制改写成 dry-run
- 执行是被 policy 挡住，还是在等 approval
- 哪些结论是“已经验证过”的，哪些只是“建议过”
- compensation 是否真的尝试过

## tool gateway 行为

所有受治理的 tool 执行都集中在 [`workflow_tools.go`](../../backend/internal/controller/agentcore/workflow_tools.go) 的 `workflowToolManager.call`。

这就是 workflow tool 的真实执行边界。

### 当前调用顺序

每次 tool call，runtime 会按这个顺序处理：

1. 构造一条 `WorkflowToolCall` 记录，包含 tool 名、actor、stage、collector、dry-run flag、policy version 和 risk tag
2. 用 `validateWorkflowToolRequest` 校验请求结构
3. 通过 `PolicyEngine.Evaluate` 评估 policy
4. 对被 block 的调用立即拒绝
5. 如果请求不是 dry-run 且 policy 要求 approval，就以 `ApprovalState=pending` 停住
6. 如果 policy 要求 dry-run，就把请求改写为 dry-run
7. 生成或复用 idempotency key
8. 检查内存 idempotency cache
9. 对非只读工具，在近期 durable run 中查找是否已有相同 idempotency key 的成功调用
10. 应用 tool-specific timeout
11. 只对 read-only tool 且错误可重试时做 retry
12. 成功结果按 idempotency key 写入 cache
13. 把受治理后的 `WorkflowToolCall` 持久化到 durable run

### 为什么必须集中治理

如果不集中：

- 每个工具都会自己决定 timeout 语义
- approval 逻辑会漂移
- idempotency 语义会不一致
- audit 覆盖会取决于各个工具是否自觉实现

当前设计故意把这个边界做得“朴素而统一”，因为运维环境更怕不可解释，而不是怕代码写得不够聪明。

### validation/action 侧现在能用哪些工具

第二个 agent 复用了同一个 governed gateway，但它现在能用的实际工具面已经比旧 RCA loop 宽很多。

当前的主要类别有：

- 核心可观测性：metrics、logs、eBPF/runtime、topology、GPU、service health
- change 与 config：change query、deployment history、config state、container revision
- 深度诊断：memory pressure、storage health、connectivity、DNS
- workload 与平台：Kubernetes resource identity、process lineage、network blast radius
- 安全与取证：security finding、security graph、runtime/process graph
- 知识与 memory：runbook retrieval、similar-case retrieval、historical incident retrieval、prior action outcome retrieval
- guarded execution：profiling 和 remediation，仍然全部走现有的 policy、approval 和 dry-run 路径

## 幂等性与请求去重

runtime 里其实有两层 dedupe。

### 1. workflow 请求去重

workflow 入口处，`beginWorkflowRun` 会在短 TTL 内去重等价 workflow request。

[`workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go) 里的默认值是：

- `RequestDedupeTTL = 30s`
- `RequestDedupeEntries = 256`

dedupe key 包含：

- workflow type
- collector
- window
- limit
- trigger
- dry-run flag

为什么需要：

- API 页面频繁刷新，不应该立刻启动重复的 RCA 或 joint-risk run
- 相同的 in-flight run 应该串行化，而不是彼此竞争

### 2. tool call 幂等性

到了工具执行阶段，`stableWorkflowToolKey` 会对这些字段做哈希：

- tool name
- workflow
- stage
- collector
- query payload
- dry-run mode

为什么需要：

- 同一 remediation 或 profiling 请求，不应该因为 workflow 重新走到同一个 step 就被重复执行
- 先前已经成功执行过的 guarded action，应当优先从 durable history 复用，而不是盲目重放

限制：

- key 的稳定性取决于请求 payload 是否规范化
- 语义相同但文本不同的请求，仍然会产生不同 key

## policy 与 approval 检查

workflow runtime 会明确区分“只读证据采集”和“有风险的执行”。

### 当前代码里的工具分类

| 工具类型 | 例子 | 安全行为 |
| --- | --- | --- |
| 只读 | metrics、logs、topology、change query、security query、knowledge retrieval、eBPF query、GPU query、security graph、process lineage | 可以 retry；天然支持 dry-run；不需要 approval |
| guarded execution | profiling | 受 policy 控制；根据配置可能需要 dry-run 或 approval |
| approval-gated | remediation | 显式标记为高风险；approval gate 是一等边界 |

### policy 结果

`ActionPolicyDecision` 实际上可以做到四件事：

- allow
- block
- require approval
- require dry-run

### 当前 runtime 的 approval 路径

在 `validation_action_react_loop` 内，当某个 tool call 返回“需要 approval”时：

1. step status 变成 `approval_required`
2. 挂载 `DurableApprovalRecord{State:"pending"}`
3. 通过 `SuspendRun` 把 run 挂起
4. workflow stop reason 设置为 `awaiting approval`

这就是为什么 run 可以 resume，而不是静默失败，也不是悄悄跳过执行。

### 为什么 approval 要和 policy 分开

policy 回答的是：

> 在当前规则下，这一类 action 允许吗？

approval 回答的是：

> 是否有人类批准了这一次具体 action？

这两个问题在运维上不是一回事，所以 runtime 会把它们都持久化。

## verification loop

verification 不是一个“顺便记一下”的注释，它是 runtime 中被持久化的阶段，而且现在由 validation 侧明确负责。

### 动作前的 target validation

`ValidationActionAgent` 面向明确的 target type 工作：

- hypothesis validation
- recommendation validation
- change-correlation validation
- remediation-outcome validation
- contradiction search

每个 target 都会跑一个有界 loop：

1. 看当前 target 和当前 verdict
2. 从 target-aware tool catalog 里选下一个 tool
3. 调用 governed tool gateway
4. 记录简短 observation 和 confidence delta
5. 更新 verdict：`confirmed`、`contradicted`、`partially_supported` 或 `insufficient_evidence`
6. 在 support、contradiction、budget、timeout 或 tool sequence 用尽时停止

这个 loop 只持久化简短 reasoning artifact，不保存隐藏 chain-of-thought。

### deterministic 有用性检查

旧的 deterministic 检查仍然保留，主要作为 fallback，也用于具体 tool payload 的验证。

例如：

- `ToolMetrics` 要求有节点并且至少有三条 history sample
- `ToolLogs` 在存在 log-burst 假设时，要求返回实际匹配日志
- `ToolChangeQuery` 要求至少有一条相关 change
- `ToolEBPFQuery` 要求存在 runtime event 或 syscall statistics

不管结果如何，runtime 都会记录 `DurableVerificationRecord`。

### remediation 与 post-action validation

对 remediation，runtime 现在会分两层处理：

1. validation 侧先判断 recommendation 本身是否有足够支持，值得保留
2. 如果真的执行了 guarded remediation，`post_action_validation` 会单独写一份 `PostActionValidationSummary`
3. deterministic fallback 仍然会调用 `verifyRemediationEffect`
4. 最终报告会把 validation verdict 和 post-action verdict 并列保留

当前默认值：

- `VerificationWindow = 2m`
- `MediumRiskThreshold = 0.45`

它带来的价值：

- runtime 能区分“动作执行过了”和“动作确实改善了环境”
- incident memory 可以优先记录 verified success，而不是所有尝试过的动作

限制：

- 现在的 verification 函数刻意保持简单
- 它还不会比较丰富的 before/after world model，只会使用新的 joint-risk 分数和 supporting note

## compensation 与 rollback 意图

compensation 由 `stepCompensate` 处理。

runtime 不会假装 rollback 一定可行。它目前会显式记录三种状态：

- `executed`
- `failed`
- `skipped`

### 当前 compensation 的工作方式

只有当下面条件同时成立时，runtime 才会尝试 `ExecuteRollback`：

- `AutoRollbackOnVerificationFailure` 被启用
- 有可用的 `PlaybookRunner`
- step 上存在 `OriginalAction`
- `OriginalAction.RollbackCommand` 已填充

否则，它会把 compensation 记成 `skipped`，原因是 `no rollback command`。

### 为什么 compensation 必须显式持久化

如果 rollback 意图只隐含在日志里，operator 就无法区分：

- rollback 是否真的尝试过
- rollback 是尝试了但失败了
- 还是根本没有安全可执行的 rollback

当前 runtime 把这个差异 durable 化了。

## change intelligence、causal graph 与 incident memory

这三个子系统让 workflow 超出了“趋势 + RAG 包装器”的范畴。

### change intelligence

`changeintel.Store` 会持久化规范化的 `ChangeEvent` JSON 工件，并按照以下维度做关联：

- collector 或 node scope
- incident summary 文本
- incident window
- scope hint

当前评分权重是显式写死的：

- 时间邻近度：55%
- 范围重叠：30%
- 语义重叠：15%

为什么需要它：

- 很多 incident 直接由 deployment、config、driver 或 feature flag 变化触发
- runtime 需要一层一等证据，而不是把这些东西淹没在日志片段里

### causal graph

`causalgraph.Analyze` 不是 learned graph model，它是 typed ranking pass。

它会从证据构图，然后：

- 提升更像上游原因的节点
- 对 `change` 节点做强 boost
- 给予 runtime 与 security 节点高于普通 symptom 节点的因果权重
- 计算 shortest-path cause path 与 impact path

为什么需要它：

- operator 需要“更像原因的排序”，而不是平铺的 finding 列表

### incident memory

workflow memory bridge 会把 `WorkflowMemoryRecord` 写入 incident-memory store。

当前写回点包括：

- 通过 `recordSuccessfulRemediation` 写入成功 remediation
- 通过 `persistRCAArtifacts` 写入完成的 RCA

记录字段包括：

- root cause
- verification summary
- causal path
- impact scope
- signals
- actions
- evidence ID
- hypotheses
- action outcome
- operator feedback（如果存在）

为什么需要它：

- 静态 runbook 回答的是“通常建议怎么做”
- incident memory 回答的是“这里之前发生过什么、什么动作被验证过有效”

## 行为记忆如何进入决策环

runtime 现在还多了一层和 incident memory 不同、但互补的判别逻辑：workload recurring-burst 判别。

它和 incident memory 不是一回事：

- incident memory 保存过去 incident 的动作与结果
- recurring-burst 判别读取更长窗口的 metric history，用来判断当前 burst 是否更像历史上健康的重复行为

### 为什么需要它

在这个功能加入之前，workflow 已经能解释：

- 当前值和 baseline 的差距
- 趋势方向
- 多个弱信号是否同时出现

但它仍然缺少一个实际值班中很重要的记忆：

- 这个服务是不是已经很多次出现过同样的短时 burst，而且过去并没有伴随错误、日志异常或延迟回归？

这个缺口会给 build job、artifact upload、deployment helper、startup warmup 等 workload 造成持续误报。

### 它运行在哪里

集成点在 [`workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go) 的 `workflowState.refreshDerivedEvidence()`：

1. 构建 `RiskSeries`
2. 调用 `BehavioralMemoryStore.Evaluate(...)`
3. 把结果送进 `buildRiskSignals(...)`
4. 再把同一结果送进 `buildTrendAssessments(...)`
5. 最后通过 `buildBehavioralAssessmentEvidence(...)` 生成显式 evidence record

### 实际存在什么状态

这一层不会再持久化第二份 workload 长期历史。

它现在只依赖：

- 现有的 metric-history provider 负责长窗口读取
- 如果 provider 连到了持久 timeseries 路径，就直接复用可选 TSDB 长历史
- 一个有界的内存 cache，避免短时间内重复查询同一段历史

这样能坚持长期指标历史只有一个事实来源，也避免再维护一个行为 profile 数据库。

### 决策如何改变

workflow 现在会把每个活跃信号分类成：

- `expected_recurring_burst`
- `suspicious_deviation`
- `correlated_anomaly`
- `confirmed_anomaly`

这些分类会直接影响 runtime 输出：

- 已知 recurring burst 会降分，必要时直接取消触发
- `correlated_anomaly` 会保留触发，并明确说明这是“熟悉的 burst 叠加了额外损伤证据”，而不是直接把所有相关性都夸大成硬故障
- `confirmed_anomaly` 会提高置信度
- 当可选的 `service_latency_p95_ms` / `service_latency_p99_ms` 指标存在时，service-latency 回归可以给原本偏弱的资源尖峰补足 corroboration
- 历史不足时会继续保守地留在 `suspicious_deviation`
- suppression 理由会保留在 evidence 和 operator-facing summary 里

### 为什么这部分必须写进 runtime 文档

因为它已经直接改变了 incident 结论和审计轨迹：

- `JointRiskAssessment` 现在包含 `behavioral_assessments`
- RCA 上下文和最终 workflow report 也包含 `behavioral_assessments`
- evidence package 现在会写出 `behavioral_memory_decision` 记录

这样 suppression 路径就是可检查的，而不是藏在一段看不见的内部启发式里。

## evidence package 与 world model 快照

runtime 会通过 [`workflow_evidence.go`](../../backend/internal/controller/agentcore/workflow_evidence.go) 写一个 JSON evidence package。

每个 package 包含：

- 持久化后的 `DurableRun`
- 可选 `AgentTrace`
- 最近的 `WorkflowAuditRecord`
- 最终 report payload
- `GeneratedAt`

这个 package 会被 `DurableRun.EvidencePackage` 引用，并通过下面 API 暴露：

- `GET /api/v1/agent/workflow/evidence/{run_id}`

### world model

`AttachWorldModel` 目前会保存一个 `DurableWorldModel`，包含：

- `Summary`
- `Scope`
- `DownstreamNodes`
- `RecentChanges`
- `Topology`

它故意和最终 RCA 报告分开，因为它描述的是“这次 run 当时认为环境长什么样”，而不仅仅是最后给出的结论。

## evaluation 与 replay hook

仓库里有显式 replay hook，因为受治理的 workflow 必须可回归测试。

### runtime 级 replay

`ReplayRun` 会增加 `ReplayCount` 并记录 `run_replayed`。

这只是轻量 bookkeeping，不是一个完整的确定性重执行引擎。

### evaluation 包装层

`evaluation.RunReplay` 会运行两遍 golden evaluation，并比较以下漂移：

- root-cause accuracy
- recommendation safety
- governance coverage
- verification coverage
- durable run coverage
- evidence package coverage
- memory write-back coverage
- retrieval metrics

为什么这很重要：

- runtime 的评估不只是“有没有返回结果”
- 还包括“它是否像控制环宣称的那样，真的持久化 run、verification、evidence 和 memory”

## operator 可见面

主要只读 API：

- `GET /api/v1/agent/joint-risk`
- `GET /api/v1/agent/rca`
- `GET /api/v1/agent/workflow/audit`
- `GET /api/v1/agent/workflow/runs`
- `GET /api/v1/agent/workflow/runs/{run_id}`
- `GET /api/v1/agent/workflow/evidence/{run_id}`
- `GET /api/v1/agent/workflow/incidents`

controller 暴露的 approval / action API 包括：

- `POST /api/v1/agent/incidents/{incident_id}/actions/{action_id}/approve`
- `POST /api/v1/agent/incidents/{incident_id}/actions/{action_id}/execute`
- `POST /api/v1/agent/incidents/{incident_id}/actions/{action_id}/rollback`

这些 surface 很重要，因为它们让 runtime 作为一个 API-backed control loop 可被检查，而不仅仅藏在 Go 代码内部。

现在 joint-risk、RCA 和 evidence package 输出还会显式带上 `behavioral_assessments`，所以 operator 可以直接看到某个 burst 是被降权、保守保留，还是因为 cross-signal corroboration 被重新升级。

## operator 读一个 run 时应先看什么

当一个 run 看起来“不对”时，最有效的阅读顺序通常不是先看最后那段 RCA 文本，而是按下面的检查顺序走：

1. 先看 `Status`、`CurrentStage`、`CurrentStep`。这能先分清 run 是完成了、挂起了，还是在中途失败了。
2. 再看 `PlanRevisions` 和 `Steps`。这一步判断 runtime 原本打算做什么，以及实际执行到了哪里。
3. 然后看 `ToolCalls` 上的 policy verdict、dry-run 改写、approval state 和 idempotency reuse。这样能知道“为什么没执行”或“为什么复用了旧结果”。
4. 如果结论来自 remediation，再看 `Verification` 和 `Compensation`。动作执行过并不等于问题真的缓解了。
5. 最后再看 `EvidencePackage`、`WorldModel`、`behavioral_assessments` 和 incident-memory write-back。这里才是把本次 run 放回上下文里理解的地方。

按这个顺序读，operator 更容易分辨问题到底出在：

- 证据不足
- policy/approval 阻断
- step 设计不合理
- verification 失败
- 还是行为记忆把一个看起来严重的 burst 判成了“历史上经常发生、且通常健康”的模式

这也是为什么 runtime 文档不能只解释“有哪些 API”，还必须解释“如何读这些 durable object”。

## 实际边界

当前实现比“只会生成报告的流水线”强得多，但仍然有清晰边界：

- durability 是 local-first，不是分布式 workflow 服务
- verification 是 heuristic 的，目前主要复用新的 joint-risk 评估，而不是 richer world-state comparator
- change intelligence 是仓库内局部启发式实现，不是 CMDB 或企业变更系统
- incident memory 的质量依赖 workflow 真正写回 verified outcome
- idempotency 是 payload-based 的，不理解语义等价
- rollback 质量依赖 rollback command 和 action descriptor 本身是否可用
- approval 虽然是显式的，但仍然依赖周边 operator 流程和 token 管理

这些都是当前代码的真实边界，不是“文档还没写好”的问题。

## 下一步阅读

- [Pipeline 深度解析](02-pipeline-deep-dive.md)：进入这个 runtime 之前，证据在 collector/controller 路径上怎样流动
- [架构](04-architecture.md)：状态归属、信任边界，以及热状态与持久状态的设计
- [架构决策记录](18-architecture-decisions.md)：为什么这里选择 durable governed runtime 以及相关控制平面技术
- [测试与评估](19-testing-and-evaluation.md)：runtime 背后的 golden 与 replay 评估面
