# UI Guide

中文版本：[docs/zh/08-ui-guide.md](../zh/08-ui-guide.md)

This page explains how to read the current Web UI as an investigation console instead of a generic dashboard. It uses the actual `v0.7` pages, the refreshed screenshots under [`docs/images/`](../images/), and the code that renders them.

## Why The UI Changed

The controller now does more work before any LLM call:

- it builds single-metric trend assessments
- it fuses weak signals into investigation events
- it records retrieval decisions instead of treating RAG as invisible magic

The UI changed to expose those intermediate artifacts directly, so operators can see:

- symptom vs probable cause
- trend vs weak-signal fusion
- whether retrieval was used, skipped, or suppressed
- what evidence was attached before diagnosis

## Main Screens

### 1. Dashboard

<p align="center">
  <img src="../images/dashboard.png" alt="Dashboard screenshot" width="1000">
</p>

What this page is for:

- quick fleet health view
- recent RCA summaries
- a fast check that ingest, controller analysis, and the UI are all alive

Main code:

- [`frontend/src/App.tsx`](../../frontend/src/App.tsx)
- [`frontend/src/components/Insights/AIInsights.tsx`](../../frontend/src/components/Insights/AIInsights.tsx)
- [`frontend/src/components/Insights/OperationsControlPanel.tsx`](../../frontend/src/components/Insights/OperationsControlPanel.tsx)
- [`backend/internal/controller/agent_integration.go`](../../backend/internal/controller/agent_integration.go)
- [`backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`backend/internal/controller/agent/report_dedupe.go`](../../backend/internal/controller/agent/report_dedupe.go)

How to read it:

- treat it as the entry screen, not the final RCA surface
- if the panel shows deterministic output only, check `analysis_reused_total`, `agent_rag_skipped_context_total`, and stale-telemetry indicators before blaming the prompt
- the top `Control-plane focus` block comes from `/api/v1/agent/status` `control_plane`, not from the raw RCA list

What `Control-plane focus` means:

| Field | Where it comes from | Why it matters |
| --- | --- | --- |
| `Trend watch` | latest `TrendAssessment[]` count from [`workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go) | shows whether the controller is seeing sustained drift before one hard threshold dominates |
| `Weak-signal fusion` | latest `InvestigationEvent[]` count | shows whether several moderate symptoms have already been compressed into one investigation suspicion |
| `Retrieval planning` | latest `RetrievalDecision[]` summary | shows whether retrieval ran, skipped, or was suppressed before prompt assembly |
| `Lead issue` | latest joint-risk or RCA summary | gives one compact operator headline before you open the deeper pages |

The Operations panel `AGENT` continuity card also reads `/api/v1/agent/status` `report_engine.report_suppressed_total` and shows `reports suppressed N`.

How to interpret it:

- high or rising on a stable node is expected
- `0` on a stable node while `suppress_unchanged_reports: true` is set is suspicious
- rising together with `predictive_log_suppressed_total` usually means the legacy report engine is deliberately rate-limiting repeated predictive warnings

### 2. Risk Insights

<p align="center">
  <img src="../images/risk-insights.png" alt="Risk Insights screenshot" width="1000">
</p>

What this page is for:

- proactive weak-signal detection
- “nothing is red yet, but multiple small indicators point in one direction”

Main code:

- [`frontend/src/components/Insights/RiskInsightsPage.tsx`](../../frontend/src/components/Insights/RiskInsightsPage.tsx)
- [`frontend/src/components/Insights/InvestigationPanels.tsx`](../../frontend/src/components/Insights/InvestigationPanels.tsx)
- [`backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)

What the major panels mean:

| Panel | What it shows | Why it matters |
| --- | --- | --- |
| `Lead suspicion` | top ranked latent-risk summary plus first promoted investigation event | compresses “what should I care about first?” |
| `Weak-Signal and Event Summary` | controller-generated `InvestigationEvent[]` | shows multivariate fusion before LLM/RAG |
| `Single-Metric Trend Watch` | controller-generated `TrendAssessment[]` | shows drift, slope, threshold persistence, and forecast hints |
| `Knowledge Retrieval Decisions` | `RetrievalDecision[]` | shows whether retrieval ran, what query was formed, or why it was skipped |
| `Retrieved Supporting Knowledge` | attached RAG hits | exposes runbook/case evidence without hiding it inside the final answer |

Concrete example:

```text
memory_used_mb = 14320
memory_total_mb = 16384
memory_usage_pct = 87.4
service_latency_p95_ms = 312
node_pressure_memory_some_avg10 = 19.8
```

Controller interpretation:

- single-variable trend: memory usage slope stays positive across the recent window
- weak-signal fusion: memory growth + reclaim pressure + service latency are promoted into one investigation event
- retrieval decision: use a memory-pressure runbook query only if the event title and symptom text are specific enough

The same state now appears in `/api/v1/agent/status` as a compact summary:

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

What this page is for:

- correlation-style analysis across several moderate signals
- early warning before one metric alone becomes catastrophic

Main code:

- [`frontend/src/components/Insights/JointRiskPage.tsx`](../../frontend/src/components/Insights/JointRiskPage.tsx)
- [`backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`backend/internal/controller/agentcore/incident_decision.go`](../../backend/internal/controller/agentcore/incident_decision.go)

What to look at first:

1. `Control-plane verdict`
2. `Investigation Events`
3. `Trend Watch`
4. `Ranked Signals`
5. `Knowledge Retrieval Decisions`

Concrete example:

```text
cpu_iowait_pct = 28.4
disk_await_ms = 41.7
disk_queue_depth = 13
service_latency_p95_ms = 312
```

Interpretation:

- no single metric has to be “fatal”
- the control plane can still promote a disk-contention event because iowait, queue depth, and latency move together
- RAG is then queried from the event summary, not from the full raw metric dump

### 4. RCA Workflow

<p align="center">
  <img src="../images/rca.png" alt="RCA Workflow screenshot" width="1000">
</p>

What this page is for:

- the deepest investigation view in the current repo
- structured incident summary, supporting signals, hypotheses, recommendations, retrieval decisions, and knowledge evidence

Main code:

- [`frontend/src/components/Insights/RCAPage.tsx`](../../frontend/src/components/Insights/RCAPage.tsx)
- [`backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`backend/internal/controller/agentcore/workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go)
- [`backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)

How to read the page:

| Section | Reading guidance |
| --- | --- |
| `Investigation headline` | start here for one-line incident framing |
| `Investigation Events` | read as probable-cause candidates promoted before prompt assembly |
| `Trend and Forecast Watch` | read as per-metric deterioration evidence |
| `Structured RCA Report` | read as the controller’s operator-facing output contract |
| `Knowledge Retrieval Decisions` | confirm whether runbooks/cases were used, skipped, or suppressed |
| `RCA Knowledge Evidence` | inspect the exact docs that influenced the report |

## One End-To-End Example

The example below is illustrative, but every structure matches the real code path.

### Raw incoming telemetry

```text
memory_used_mb = 14320
memory_total_mb = 16384
memory_usage_pct = 87.4
cpu_iowait_pct = 28.4
disk_await_ms = 41.7
nic_rx_drops = 134
service_latency_p95_ms = 312
```

### Normalized controller-side representation

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

### Control-plane eventization

Illustrative derived artifacts from [`workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go):

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

### Retrieval decision

Illustrative controller retrieval planning:

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

### Retrieved knowledge

Illustrative hit shape from the current RAG service:

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

### What the UI shows

- `Risk Insights` shows the weak-signal event and trend card first
- `Joint Risk` shows the correlation-style verdict and the same event family when several signals co-occur
- `RCA Workflow` shows the event, trend, retrieval decision, retrieved doc, and final structured report together

That is the main UI design change in this phase: the operator can now inspect the evidence ladder instead of only the final text answer.

## Screenshot Refresh Workflow

The repository now has a concrete capture flow for these docs:

```bash
./scripts/run-local.sh --enable-agent --demo --llm=stub
google-chrome-stable --headless=new --disable-gpu --remote-debugging-port=9224 --no-sandbox about:blank
CAPTURE_WARMUP_MS=15000 CAPTURE_LIVE_WAIT_MS=30000 CAPTURE_STABILIZE_MS=12000 UI_URL=http://127.0.0.1:8080 node scripts/capture_readme_screenshots.mjs
```

Why those waits exist:

- `CAPTURE_WARMUP_MS=15000` gives the controller demo data and first UI queries time to settle
- `CAPTURE_STABILIZE_MS=12000` adds an explicit post-ready delay on each captured page so the screenshots reflect loaded investigation data rather than a transient loading state

Main capture script:

- [`scripts/capture_readme_screenshots.mjs`](../../scripts/capture_readme_screenshots.mjs)

## Limits

- some pages still depend on the demo dataset or live telemetry being present; the UI does not invent missing evidence
- retrieval decisions are heuristic and evidence-driven, not a learned incident router
- the UI can explain why retrieval was skipped or suppressed, but it cannot make a weak dataset strong by itself

## See Also

- [Architecture](04-architecture.md)
- [Data Flow](05-data-flow.md)
- [Dataset and RAG](11-dataset-and-rag.md)
- [Prompts and Customization](12-prompts-and-customization.md)
- [Core Files](10-core-files.md)
