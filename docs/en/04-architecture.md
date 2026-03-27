# Architecture

中文版本：[docs/zh/04-architecture.md](../zh/04-architecture.md)

This page explains the current `v0.8` architecture as implemented, not as an aspirational diagram.

Read this first if you want the boundary decisions behind the code. Then read [Pipeline Deep Dive](02-pipeline-deep-dive.md) for the full data path and [Incident Agent Runtime](17-incident-agent-runtime.md) for the governed workflow loop.

## In This Page

- [Architectural Thesis](#architectural-thesis)
- [Exact Runtime Boundary](#exact-runtime-boundary)
- [What Is Allowed To Be Stateful On The Host](#what-is-allowed-to-be-stateful-on-the-host)
- [What Must Remain On The Controller](#what-must-remain-on-the-controller)
- [Hot State Versus Durable State](#hot-state-versus-durable-state)
- [Continuous Work Versus Deferred Work](#continuous-work-versus-deferred-work)
- [Trust Boundaries](#trust-boundaries)
- [Why These Exact Techniques Exist](#why-these-exact-techniques-exist)
- [Failure Containment And Limits](#failure-containment-and-limits)
- [Read Next](#read-next)

## Architectural Thesis

The system is built around one operational premise:

> the cheapest place to preserve short-lived evidence is the host; the cheapest place to analyze, correlate, and govern that evidence is the controller.

That premise explains:

- why `cpp/probe_core/` and `backend/internal/collector/` run on the observed machine
- why suppression happens before transport
- why the collector writes to a bounded local spool instead of relying on RAM-only buffering
- why the controller rebuilds one normalized `NodeSnapshot` fact model in `backend/internal/controller/ingest/store.go`
- why only a whitelist of trend-safe metrics is retained in the hot history path
- why recurring-burst memory is learned on the controller instead of in the collector hot loop
- why retrieval is gated instead of always stuffing context into prompts
- why the incident agent persists durable runs, approvals, verification records, compensation records, evidence packages, and incident memory

## Exact Runtime Boundary

| Boundary | Main code | Owns | Explicitly does not own | Why the split exists |
| --- | --- | --- | --- | --- |
| host observer | [`../../cpp/probe_core/`](../../cpp/probe_core/), [`../../backend/internal/collector/`](../../backend/internal/collector/) | sampling, source selection, local pacing, suppression, aux caches, spool, retry/replay, host-local protection | RCA synthesis, long-lived fleet history, RAG, change correlation, causal ranking, workflow orchestration | the host can observe `/proc`, `/sys`, GPU, and eBPF state cheaply, but it should not run the heavy control plane |
| controller fact plane | [`../../backend/internal/controller/ingest/`](../../backend/internal/controller/ingest/), [`../../backend/internal/controller/logindex/`](../../backend/internal/controller/logindex/), [`../../backend/internal/controller/gpuobs/`](../../backend/internal/controller/gpuobs/) | validation, dedupe, `NodeSnapshot`, bounded trend history, log indexing, GPU summaries, API-facing hot state | host-local collection and delivery guarantees | all later APIs and workflows need one shared interpretation of current telemetry |
| controller reasoning plane | [`../../backend/internal/controller/predictive/`](../../backend/internal/controller/predictive/), [`../../backend/internal/controller/changeintel/`](../../backend/internal/controller/changeintel/), [`../../backend/internal/controller/causalgraph/`](../../backend/internal/controller/causalgraph/), [`../../backend/internal/controller/rag/`](../../backend/internal/controller/rag/) | trend scoring, weak-signal fusion, change correlation, retrieval, causal ranking, prior-incident lookup | raw host sampling and low-level buffering | these steps are more expensive, easier to inspect centrally, and should work from normalized evidence |
| governed incident runtime | [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go), [`../../backend/internal/controller/agentcore/workflow_tools.go`](../../backend/internal/controller/agentcore/workflow_tools.go), [`../../backend/internal/controller/agentcore/workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go) | durable runs, plan steps, tool calls, policy, approval, verification, compensation, evidence packages, incident memory write-back | direct host sampling, ungoverned autonomous execution | action is the highest-risk layer, so it is isolated behind durable state and explicit safety checks |

### Why the governed runtime is now split into two agents

Inside the governed incident runtime, RCA now has two explicit roles instead of one mixed control loop:

- `AnalysisAgent`: turns current evidence into incident synthesis, hypotheses, ranked causes, recommendations, and a structured `AnalysisHandoff`
- `ValidationActionAgent`: consumes that handoff, selects richer tools by target type, searches for contradiction, validates recommendations, and owns guarded execution planning plus post-action validation

That split exists because the two jobs have different failure modes:

- analysis needs broad evidence correlation and explanation quality
- validation and action need sharper tool choice, budgets, policy boundaries, and before/after verification

Keeping them separate makes the final report easier to audit. Operators can see what the analysis believed first, then what the validation/action side confirmed, contradicted, or left unresolved.

## What Is Allowed To Be Stateful On The Host

The host side is allowed to keep only state that directly protects collection quality or host cost.

| Host state | Where it lives | Why it is allowed | Why it stays bounded |
| --- | --- | --- | --- |
| latest probe-core frame | `backend/internal/collector/probecore/client.go` | the collector needs a recent `ProbeBatch` without re-reading the subprocess stream | only the latest frame snapshot is retained |
| probe-core in-process queue and writer buffers | `cpp/probe_core/main.cpp` | probe modules sample at different cadences and need decoupled serialization | queue depth is bounded; oldest frames can be dropped under pressure |
| aux collection caches | `backend/internal/collector/aux_sampling.go` | process fallback, logs, and external helpers are intentionally slower than the main loop | each cache stores only the latest payload and last collection time |
| suppression fingerprints | `backend/internal/collector/metric_suppression.go`, `backend/internal/collector/process_payload_suppression.go` | the collector must know whether low-churn metrics or process payloads changed | state is only the last emitted fingerprint/value plus timestamp |
| local spool | `backend/internal/collector/spool/spool.go` | transport stalls must not immediately become evidence loss | the spool has a configured max size and evicts oldest unread records when full |
| transport connection cache | `backend/internal/collector/transport/client.go` | gRPC/TLS setup should not be paid on every send | endpoint connections are reused, not unboundedly accumulated |
| protection governor sample history | `backend/internal/collector/protection.go` | self CPU/RSS and backlog must influence pacing | only the most recent self-sample basis is kept |

What is intentionally not allowed to become a host-local database:

- long-lived per-node trend history
- cross-node or fleet state
- RAG index state
- change intelligence records
- incident memory
- durable workflow runs

Those stay on the controller because they need shared semantics, central inspection, and durable APIs.

## What Must Remain On The Controller

The controller owns any state that is reused across APIs, workflows, or time windows.

| Controller state | Main type or store | Why it must be central |
| --- | --- | --- |
| current normalized node view | `NodeSnapshot` in [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) | prompts, UI, joint-risk, RCA, and workflow tools must all read the same reconstructed state |
| bounded hot history | `ring.Ring[MetricHistorySample]` in [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) | trend and predictive logic need a controller-owned historical window |
| optional long-window metric store | `timeseries.Service` in [`../../backend/internal/controller/timeseries/service.go`](../../backend/internal/controller/timeseries/service.go) | history queries may outlive controller restarts and should not live on the host |
| searchable log index | `logindex.Index` | log evidence is reused across query-service and workflow tools |
| behavioral baseline cache | bounded in-memory cache in [`../../backend/internal/controller/agentcore/behavioral_memory.go`](../../backend/internal/controller/agentcore/behavioral_memory.go) | recurring-burst checks may ask the same long-window history question several times during one workflow burst; caching keeps that cheap without creating a second source of truth |
| change records | JSON files under `data/agent/workflows/changeintel/` by default | change correlation is cross-batch, sometimes cross-run, and should survive process restarts |
| incident memory | JSON files under `data/agent/workflows/incident_memory/` by default | prior incidents only help if later workflows can retrieve them |
| durable workflow runs | `DurableRun` in `data/agent/workflow_runs.db` by default | governed execution requires resumable and auditable run state |
| workflow evidence packages | JSON files under `data/agent/workflows/evidence/` by default | operators need a reconstruction artifact of what the workflow actually saw |
| RAG index | `index.json` plus optional vector backend sync state under `data/agent/rag/` | normalization, chunking, and search should be shared across queries and workflows |

### Why recurring-burst logic stays on the controller

The false-positive problem that motivated this feature is not a host-sampling problem. It is a classification problem:

- a short-lived build or backup spike can look severe in one window
- deciding whether that spike is normal requires historical context across many workflow evaluations

Putting that logic on the controller instead of the host keeps the collector cheap:

- no extra host-local disk writes
- no new collector locks or background aggregation loops
- no collector-side schema or retention management
- no duplicate long-term behavioral store to explain and operate

The workflow still needs history, but it now derives that history from the existing metric-history provider and optional TSDB-backed queries. The only extra state is a small in-memory cache. Suppression still happens only in controller-side workflow decisions, not in raw host telemetry emission. That is the correct tradeoff in this repo because the collector exists to preserve evidence, not to make cross-run incident decisions.

## Hot State Versus Durable State

The implementation uses different storage classes on purpose. Not everything needs durable persistence, and not everything is safe to recompute after restart.

| State class | Examples | Implementation | Why this class was chosen | Limitation |
| --- | --- | --- | --- | --- |
| hot in-memory only | live `NodeSnapshot`, `ProcessResources`, `ProcessGraphSnapshot`, recent `MetricHistorySample` rings, query-service caches | `MemoryStore`, in-process indexes and caches | cheap reads for every API and workflow step | controller restart clears it unless persistence is enabled for ingest snapshots |
| hot with optional persistence | ingest snapshot and history | background persistence in [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) | keeps fast in-memory semantics while allowing restart recovery | persistence is snapshot-style, not an append-only event log |
| durable local file or BoltDB | spool files, workflow runs, evidence packages, changeintel, incident memory, RAG index | `spool.log`, `spool.offset`, Bolt buckets, JSON artifacts | enough durability for local-first operations without bringing in a distributed backend | not a multi-controller distributed coordination system |
| recomputable derived state | trend assessments, weak-signal clusters, causal ranking, retrieval results | rebuilt from hot state, history, knowledge base, and workflow inputs | avoids storing every derived view forever | results depend on the retained upstream evidence and may change as code evolves |

### Durable Defaults

These default paths come from the current code:

- workflow store: `data/agent/workflow_runs.db`
- workflow data root: `data/agent/workflows`
- workflow evidence packages: `data/agent/workflows/evidence/`
- incident memory: `data/agent/workflows/incident_memory/`
- change intelligence: `data/agent/workflows/changeintel/`
- RAG index: `data/agent/rag/index.json`

## Continuous Work Versus Deferred Work

One of the key architectural choices in this repo is deciding what is cheap enough to do every collection cycle and what should be deferred until an operator or workflow asks.

| Work class | Current implementation | Why it can run continuously | Why not everything is continuous |
| --- | --- | --- | --- |
| cheap continuous collection | host metrics, storage/network summaries, GPU summaries, collector self-metrics | these are first-line health signals and are needed to detect deterioration early | even these are still cadence-controlled and can back off under protection pressure |
| bounded continuous reconstruction | ingest validation, dedupe, carry-forward, `NodeSnapshot` rebuild, trend-history append | later controller paths need this state immediately | the controller only keeps a curated subset of history and structured views |
| deferred helper collection | process fallback, logs, external commands | these are useful but more expensive, so `aux_sampling.go` caches them and marks refresh vs suppression | forcing them every cycle would increase steady-state cost on the host |
| deferred long-window persistence | timeseries export | long retention is useful but not needed for every controller decision | write queues can fill, and only whitelisted metrics are exported |
| deferred recurring-burst lookup | long-window history reads plus a short in-memory cache in [`../../backend/internal/controller/agentcore/behavioral_memory.go`](../../backend/internal/controller/agentcore/behavioral_memory.go) | the extra context is only needed during workflow evaluation, not on every ingest append | keeping this out of the collector and ingest hot path avoids extra steady-state cost and cardinality growth |
| deferred retrieval | RAG and incident-memory query | static knowledge only helps when the query or findings are specific enough | generic context stuffing adds noise and operator mistrust |
| deferred execution | profiling and remediation tools | action is the highest-risk step and must pass policy and approval checks | pretending that all actions are safe to auto-run would be operationally dishonest |

## Trust Boundaries

The system does not treat every input as equally trustworthy.

```text
raw host evidence
  -> normalized controller evidence
     -> derived signals and correlations
        -> retrieved knowledge and prior incidents
           -> LLM reasoning
              -> tool execution
```

### 1. Raw host evidence

Examples:

- `ProbeBatch` frames from probe-core
- compatibility probe metrics
- collector-side `node_security_finding` and `node_ebpf_runtime_event`
- log fingerprints and process samples

Trust model:

- closest to the machine and highest-fidelity
- still subject to collection blind spots, capability limits, suppression, and dropped frames
- not yet shaped for downstream reasoning

### 2. Normalized controller evidence

Examples:

- `TelemetryBatch`
- `NodeSnapshot`
- `ProcessResources`
- `StorageDevices`, `Filesystems`, `RuntimeSecurityEvents`, `SecurityFindings`

Trust model:

- the controller treats this as the shared fact model
- this is where partial-update, cleared-state, and structured-field semantics are resolved
- mistakes here affect every downstream consumer, which is why this layer is intentionally explicit

### 3. Derived signals and correlations

Examples:

- `TrendAssessment`
- predictive `Finding`
- weak-signal cooccurrences
- change correlations
- causal graph cause and impact paths

Trust model:

- deterministic and inspectable, but still heuristic
- stronger than raw thresholding, weaker than ground truth
- should influence confidence, not be mistaken for certainty

### 4. Retrieved knowledge and prior incidents

Examples:

- `SearchHit`
- workflow memory matches surfaced as `incident_memory`

Trust model:

- retrieval output is treated as supporting evidence, not as an execution instruction
- low-confidence retrieval is explicitly suppressed in the query-service path
- usefulness depends on the quality of the local dataset and recorded incident outcomes

### 5. LLM reasoning

Examples:

- `QueryResponse`
- LLM sections of joint-risk and RCA reports

Trust model:

- only as good as the upstream evidence bundle
- bounded by telemetry-quality ceilings, strict output schemas, and deterministic fallbacks
- never allowed to bypass policy or approval for unsafe actions

### 6. Tool execution

Examples:

- profiling
- remediation
- rollback

Trust model:

- least trusted layer by default
- governed by `workflow_tools.go`, policy, approval, idempotency, verification, and compensation records
- this is why the runtime is a governed control loop rather than a “chatbot that can shell out”

## Why These Exact Techniques Exist

The codebase repeatedly chooses smaller, inspectable mechanisms over broader but less bounded ones.

| Technique | Engineering reason | Operational reason | Safety reason | Cost | Limitation |
| --- | --- | --- | --- | --- | --- |
| collector/controller split | host-local capture and central reasoning have different cost profiles | short-lived evidence survives even if the controller is briefly unhealthy | risky reasoning and action do not run on production hosts | two runtimes instead of one | requires a transport boundary and state reconstruction |
| suppression before transport | repeated runtime/hardware/process payloads are easiest to detect near the source | lowers bandwidth, spool growth, and controller churn during calm periods | explicit marker metrics preserve meaning better than silent omission | ingest must understand suppression semantics | partial updates are more complex than “always send everything” |
| disk-backed spool instead of RAM-only buffering | ACK-based commit and replay are easier to make crash-tolerant on disk | short controller/network outages do not immediately drop evidence | the collector can keep sampling while delivery lags | local disk IO and compaction logic | long outages can evict the oldest unread data |
| bounded hot history instead of full raw retention | trend logic only needs a curated metric subset | predictable memory and TSDB cost on the controller | easier to explain what drives forecasts | non-whitelisted metrics are not retained for long-window analysis | adding a new metric to the collector does not automatically make it forecastable |
| separate single-variable trend and multivariate weak-signal analysis | the feature engineering and output contract differ | operators need to distinguish “one metric is worsening” from “several weak signals cohere” | reduces hidden coupling inside one opaque anomaly score | more code paths and explanations | some incidents still span both paths imperfectly |
| gated retrieval instead of unconditional prompt stuffing | retrieval becomes more relevant when queries and findings are specific | reduces operator noise and generic suggestions | weak hits are less likely to sway the answer path | some potentially useful low-confidence hints are dropped | strong telemetry with a weak dataset still yields weak retrieval |
| durable governed workflow runtime instead of ad hoc tool calls | policy, approval, verification, and audit need a shared state model | operators can inspect runs, resume them, and replay them | unsafe execution cannot hide behind prompt text | persistence, evidence packaging, and step bookkeeping add complexity | local-first durability is not a distributed workflow platform |

## Failure Containment And Limits

The architecture is designed so one bad layer does not automatically invalidate the others.

| Failure | What contains it today | What still degrades |
| --- | --- | --- |
| probe-core unavailable or stale | `source_pipeline.go` can activate compatibility fallback when configured | fidelity drops; some native signals are absent |
| collector pressure or spool growth | `protection.go` can slow cadence and shed optional work | logs, security, external helpers, or process fallback can become sparser |
| transport/controller outage | spool replay and ACK-based commit | long outages still evict oldest unread records |
| partial collector updates | ingest carry-forward semantics in `StoreMetrics` and aux refresh markers | marker bugs would affect every downstream consumer |
| timeseries backend failure | `timeseries.Service` can fall back to in-memory history | long-window retention is reduced |
| weak or noisy knowledge base | retrieval gating and confidence suppression | answers become more generic |
| failed or unsafe action | approval gate, dry-run, verification, compensation records | the runtime still depends on correct rollback metadata and policies |

## Read Next

- [Pipeline Deep Dive](02-pipeline-deep-dive.md): exact host-to-operator data flow, stage by stage
- [Incident Agent Runtime](17-incident-agent-runtime.md): durable runs, tool gateway, approval, verification, compensation
- [Architecture Decisions](18-architecture-decisions.md): ADR-style rationale for the collector/controller split, suppression, spool, bounded history, retrieval gates, and workflow governance
- [Control-Plane Analysis](07-control-plane-analysis.md): how the controller reasoning layers fit together
- [Data Flow](05-data-flow.md): broader system-level diagrams
