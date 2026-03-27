# UI 指南

English version: [docs/en/08-ui-guide.md](../en/08-ui-guide.md)

本页解释应该如何把当前 Web UI 当成“调查控制台”来阅读，而不是普通监控面板。内容基于 `v0.8` 的真实页面、[`docs/images/`](../images/) 里的最新截图，以及渲染这些页面的实际代码。

## 为什么 UI 需要这样改

controller 现在在任何 LLM 调用之前，会先做更多控制面分析：

- 构建单指标趋势判断
- 把多个弱信号融合成调查事件
- 记录 retrieval decision，而不是把 RAG 当成不可见黑盒

UI 的变化，就是把这些中间证据直接呈现出来，让操作员能看到：

- 症状和 probable cause 的区别
- 单指标趋势和多信号融合的区别
- retrieval 到底是用了、跳过了，还是因为置信度太低被抑制了
- 最终诊断前到底附带了哪些证据

## 主要页面

### 1. Dashboard

<p align="center">
  <img src="../images/dashboard.png" alt="Dashboard screenshot" width="1000">
</p>

这个页面的作用：

- 快速看 fleet 当前状态
- 看最新 RCA 摘要
- 快速确认 ingest、controller 分析和 UI 是否都活着

主要代码：

- [`frontend/src/App.tsx`](../../frontend/src/App.tsx)
- [`frontend/src/components/Insights/AIInsights.tsx`](../../frontend/src/components/Insights/AIInsights.tsx)
- [`frontend/src/components/Insights/OperationsControlPanel.tsx`](../../frontend/src/components/Insights/OperationsControlPanel.tsx)
- [`backend/internal/controller/agent_integration.go`](../../backend/internal/controller/agent_integration.go)
- [`backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`backend/internal/controller/agent/report_dedupe.go`](../../backend/internal/controller/agent/report_dedupe.go)

阅读方式：

- 把它当入口页，不要把它当最终 RCA 页面
- 如果这里只有 deterministic 输出，先看 `analysis_reused_total`、`agent_rag_skipped_context_total` 和 telemetry stale 指标，而不是先怀疑 prompt
- 顶部的 `Control-plane focus` 区块来自 `/api/v1/agent/status` 的 `control_plane`，不是直接从 RCA 列表拼出来的

`Control-plane focus` 各字段的含义：

| 字段 | 数据来源 | 为什么重要 |
| --- | --- | --- |
| `Trend watch` | [`workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go) 生成的最新 `TrendAssessment[]` 数量 | 说明 controller 是否已经看到持续漂移，而不是只看到瞬时越阈 |
| `Weak-signal fusion` | 最新 `InvestigationEvent[]` 数量 | 说明多个中等强度症状是否已被融合成一条调查怀疑 |
| `Retrieval planning` | 最新 `RetrievalDecision[]` 摘要 | 说明 controller 在 prompt 前是执行、跳过还是抑制了 retrieval |
| `Lead issue` | 最新 joint-risk 或 RCA 摘要 | 让操作员在进入深页前先看到一条紧凑 headline |

Operations 面板里的 `AGENT` continuity card 还会读取 `/api/v1/agent/status` 的 `report_engine.report_suppressed_total`，并显示 `reports suppressed N`。

这个数字可以这样解读：

- 在稳定节点上持续增长通常是正常现象
- 如果 `suppress_unchanged_reports: true` 已开启，但稳定节点上长期是 `0`，就值得排查
- 如果它和 `predictive_log_suppressed_total` 一起增长，通常表示 legacy report 引擎正在刻意抑制重复 predictive warning

### 2. Risk Insights

<p align="center">
  <img src="../images/risk-insights.png" alt="Risk Insights screenshot" width="1000">
</p>

这个页面的作用：

- 主动发现弱信号风险
- “还没有一个指标完全爆红，但多个小信号已经在同一方向恶化”

主要代码：

- [`frontend/src/components/Insights/RiskInsightsPage.tsx`](../../frontend/src/components/Insights/RiskInsightsPage.tsx)
- [`frontend/src/components/Insights/InvestigationPanels.tsx`](../../frontend/src/components/Insights/InvestigationPanels.tsx)
- [`backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)

主要面板含义：

| 面板 | 展示什么 | 为什么重要 |
| --- | --- | --- |
| `Lead suspicion` | 排名第一的 latent-risk 摘要 + 第一条 investigation event | 把“先看什么”压缩成一块摘要 |
| `Weak-Signal and Event Summary` | controller 生成的 `InvestigationEvent[]` | 在 LLM/RAG 之前先展示多信号融合结果 |
| `Single-Metric Trend Watch` | controller 生成的 `TrendAssessment[]` | 展示 drift、斜率、持续越阈和 forecast hint |
| `Knowledge Retrieval Decisions` | `RetrievalDecision[]` | 展示 retrieval 是否真的执行、query 是什么、为什么被跳过 |
| `Retrieved Supporting Knowledge` | 实际挂到 finding 上的 RAG 命中文档 | 把 runbook/case 证据显式展示出来，而不是藏在最终答案里 |

具体示例：

```text
memory_used_mb = 14320
memory_total_mb = 16384
memory_usage_pct = 87.4
service_latency_p95_ms = 312
node_pressure_memory_some_avg10 = 19.8
```

controller 的解释方式：

- 单变量趋势：内存使用率在最近窗口内持续上升
- 弱信号融合：内存增长 + reclaim 压力 + 延迟上升，被提升成一条 investigation event
- retrieval decision：只有当事件标题和 symptom 足够具体时，才会去查 memory-pressure 类 runbook

同一份状态现在也会以压缩摘要的形式出现在 `/api/v1/agent/status`：

```json
{
  "control_plane": {
    "triggered_trends": 2,
    "investigation_events": 1,
    "retrieval_decisions": 2,
    "retrieval_skipped": 1,
    "top_event_title": "Memory growth and disk wait are rising together",
    "top_retrieval_intent": "runbook",
    "latest_incident_summary": "checkout latency incident"
  },
  "report_engine": {
    "suppress_unchanged_reports": true,
    "report_refresh_interval": "3m0s",
    "predictive_log_cooldown": "5m0s",
    "report_suppressed_total": 7,
    "predictive_log_suppressed_total": 4
  }
}
```

### 3. Joint Risk

<p align="center">
  <img src="../images/joint-risk.png" alt="Joint Risk screenshot" width="1000">
</p>

这个页面的作用：

- 处理多个中等强度信号共同出现的情况
- 在“还没有一个指标单独爆炸”之前做早期预警

主要代码：

- [`frontend/src/components/Insights/JointRiskPage.tsx`](../../frontend/src/components/Insights/JointRiskPage.tsx)
- [`backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`backend/internal/controller/agentcore/incident_decision.go`](../../backend/internal/controller/agentcore/incident_decision.go)

建议先看：

1. `Control-plane verdict`
2. `Investigation Events`
3. `Trend Watch`
4. `Ranked Signals`
5. `Knowledge Retrieval Decisions`

具体示例：

```text
cpu_iowait_pct = 28.4
disk_await_ms = 41.7
disk_queue_depth = 13
service_latency_p95_ms = 312
```

解读方式：

- 不需要单个指标先达到“灾难级”
- 只要 iowait、queue depth、latency 一起恶化，control plane 也能提升出一条磁盘争用事件
- 后续 RAG query 不是用全量原始 metrics 直接拼，而是从事件摘要出发

### 4. RCA Workflow

<p align="center">
  <img src="../images/rca.png" alt="RCA Workflow screenshot" width="1000">
</p>

这个页面的作用：

- 当前仓库里最完整的调查页面
- 展示结构化 incident 摘要、supporting signals、hypotheses、recommendations、retrieval decision 和 knowledge evidence

主要代码：

- [`frontend/src/components/Insights/RCAPage.tsx`](../../frontend/src/components/Insights/RCAPage.tsx)
- [`backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`backend/internal/controller/agentcore/workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go)
- [`backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)

页面阅读顺序：

| 区块 | 应该怎么读 |
| --- | --- |
| `Investigation headline` | 先看这块，拿到一句话事件结论 |
| `Investigation Events` | 看成 prompt 组装之前就已经被提升的 probable-cause 候选 |
| `Trend and Forecast Watch` | 看成“哪些单个指标确实在持续恶化”的证据 |
| `Structured RCA Report` | 这是 controller 对外的稳定输出契约 |
| `Knowledge Retrieval Decisions` | 确认 runbook/case 到底有没有被用上，还是被跳过/抑制 |
| `RCA Knowledge Evidence` | 直接检查具体用了哪些知识文档 |

## 一条端到端示例

下面这条链路中的数值是说明性的，但结构和代码路径都对应当前实现。

### 原始输入遥测

```text
memory_used_mb = 14320
memory_total_mb = 16384
memory_usage_pct = 87.4
cpu_iowait_pct = 28.4
disk_await_ms = 41.7
nic_rx_drops = 134
service_latency_p95_ms = 312
```

### controller 规范化后的表示

```json
{
  "collector_id": "collector-a",
  "metrics": {
    "node_memory_Used_bytes": 15015608320,
    "node_memory_MemTotal_bytes": 17179869184,
    "node_cpu_iowait_percent": 28.4,
    "node_disk_request_latency_p99_seconds": 0.0417,
    "node_network_receive_drop_total": 134,
    "service_latency_p95_ms": 312
  }
}
```

### control-plane eventization

说明性示例，对应 [`workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go) 的真实结构：

```json
{
  "trend_assessments": [
    {
      "id": "memory_pressure:collector-a",
      "display": "Memory pressure",
      "trend": "rising",
      "delta_percent": 14.2,
      "slope_per_minute": 118.0,
      "threshold_breaches": 3,
      "forecast": "memory pressure likely crosses high-risk threshold within 18m",
      "severity": "high"
    }
  ],
  "investigation_events": [
    {
      "id": "memory_disk_degradation:collector-a",
      "title": "Memory growth and disk wait are rising together",
      "category": "resource_contention",
      "symptom": "memory pressure with worsening storage latency",
      "probable_cause": "memory reclaim and IO contention are amplifying each other",
      "supporting_signals": [
        "node_memory_Used_bytes",
        "node_cpu_iowait_percent",
        "node_disk_request_latency_p99_seconds"
      ],
      "severity": "high"
    }
  ]
}
```

### retrieval decision

说明性的 retrieval 规划结果：

```json
{
  "tool": "runbook_retrieval",
  "intent": "incident_rag",
  "query": "memory growth and disk wait rising together memory reclaim io contention high p95 latency",
  "evidence_signals": [
    "memory_pressure",
    "io_latency",
    "service_latency"
  ],
  "skipped": false
}
```

### 检索到的知识

说明性的 RAG hit 结构：

```json
{
  "title": "Linux memory reclaim and storage latency triage",
  "knowledge_type": "runbook",
  "summary": "Check reclaim activity, hottest disk queue, and workload burst alignment before restarting services.",
  "likely_causes": [
    "memory reclaim thrash",
    "filesystem backlog",
    "burst after rollout"
  ],
  "remediation_steps": [
    "inspect reclaim and cache growth",
    "check disk queue depth",
    "reduce write burst before restart"
  ],
  "score": 0.62
}
```

### UI 最终怎么展示

- `Risk Insights` 会先显示 weak-signal event 和 trend card
- `Joint Risk` 会在多个信号共振时，显示 correlation-style verdict
- `RCA Workflow` 会把 event、trend、retrieval decision、retrieved doc 和最终 structured report 摆在同一条证据链上

这就是这轮 UI 变化的核心：操作员终于能看到“证据是怎么一级一级提升上来的”，而不是只看到最终一句结论。

## 截图刷新流程

仓库现在有一条可复用的截图流程：

```bash
./scripts/run-local.sh --enable-agent --demo --llm=stub
google-chrome-stable --headless=new --disable-gpu --remote-debugging-port=9224 --no-sandbox about:blank
CAPTURE_WARMUP_MS=15000 CAPTURE_LIVE_WAIT_MS=30000 CAPTURE_STABILIZE_MS=12000 UI_URL=http://127.0.0.1:8080 node scripts/capture_readme_screenshots.mjs
```

这些等待参数为什么要存在：

- `CAPTURE_WARMUP_MS=15000` 给 controller demo 数据和首轮 UI 查询预留稳定时间
- `CAPTURE_STABILIZE_MS=12000` 在每个页面 ready 之后再显式等 12 秒，确保截图里是稳定数据，而不是 loading 的瞬时状态

主要截图脚本：

- [`scripts/capture_readme_screenshots.mjs`](../../scripts/capture_readme_screenshots.mjs)

## 当前限制

- 一些页面仍然依赖 demo 数据或真实 telemetry；UI 不会伪造缺失证据
- retrieval decision 仍然是启发式、证据驱动的，不是学习型 incident router
- UI 可以告诉你 retrieval 为什么被跳过或抑制，但它不能替代高质量数据集本身

## 延伸阅读

- [架构](04-architecture.md)
- [数据流](05-data-flow.md)
- [数据集与 RAG](11-dataset-and-rag.md)
- [Prompt 与定制](12-prompts-and-customization.md)
- [核心文件](10-core-files.md)
