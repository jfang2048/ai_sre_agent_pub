# Architecture Decisions

中文版本：[docs/zh/18-architecture-decisions.md](../zh/18-architecture-decisions.md)

This page records the main implementation decisions in the current `v0.8` codebase in an ADR-style format.

It is not a future roadmap. Each decision here is grounded in the current repository and linked back to the code paths that implement it.

Read [Architecture](04-architecture.md) first for ownership boundaries. Read [Pipeline Deep Dive](02-pipeline-deep-dive.md) first for the end-to-end data path. Read [Incident Agent Runtime](17-incident-agent-runtime.md) for the governed workflow loop built on top of these decisions.

## In This Page

- [ADR-001: Split Host Observation From Controller Reasoning](#adr-001-split-host-observation-from-controller-reasoning)
- [ADR-002: Use Probe-Core As Primary Source With Compatibility Fallback](#adr-002-use-probe-core-as-primary-source-with-compatibility-fallback)
- [ADR-003: Suppress Before Transport Instead Of Reconstructing Deltas Centrally](#adr-003-suppress-before-transport-instead-of-reconstructing-deltas-centrally)
- [ADR-004: Use A Disk-Backed Local Spool With ACK-Based Commit](#adr-004-use-a-disk-backed-local-spool-with-ack-based-commit)
- [ADR-005: Reconstruct One Controller-Owned Fact Model At Ingest](#adr-005-reconstruct-one-controller-owned-fact-model-at-ingest)
- [ADR-006: Keep Only Trend-Safe Bounded History In The Hot Path](#adr-006-keep-only-trend-safe-bounded-history-in-the-hot-path)
- [ADR-007: Separate Single-Series Trend Logic From Multivariate Weak-Signal Correlation](#adr-007-separate-single-series-trend-logic-from-multivariate-weak-signal-correlation)
- [ADR-008: Gate Hybrid Retrieval And Keep Incident Memory Separate](#adr-008-gate-hybrid-retrieval-and-keep-incident-memory-separate)
- [ADR-009: Use A Durable Governed Workflow Runtime Instead Of Free-Form Agent Execution](#adr-009-use-a-durable-governed-workflow-runtime-instead-of-free-form-agent-execution)
- [ADR-010: Derive Recurring-Burst Context From Existing Metric History](#adr-010-derive-recurring-burst-context-from-existing-metric-history)
- [ADR-011: Split RCA Analysis From Validation And Action](#adr-011-split-rca-analysis-from-validation-and-action)
- [Read Next](#read-next)

## ADR-001: Split Host Observation From Controller Reasoning

### Status

Accepted and implemented.

### Context

The repo needs both:

- host-local evidence capture close to `/proc`, `/sys`, GPU, and eBPF data
- controller-side reasoning, retrieval, and workflow governance that should not run on production nodes

If those concerns were collapsed into one runtime, either:

- the host would run too much logic and persistence, or
- the controller would lose short-lived evidence that only exists cheaply on the node

### Decision

Keep host observation in:

- [`../../cpp/probe_core/`](../../cpp/probe_core/)
- [`../../backend/internal/collector/`](../../backend/internal/collector/)

Keep centralized reasoning and governance in:

- [`../../backend/internal/controller/`](../../backend/internal/controller/)

### Implementation

The split is visible in the actual contracts:

- native probe IPC: `probeipc.v1.FrameEnvelope` and `ProbeBatch`
- collector-to-controller wire contract: `telemetry.v1.TelemetryBatch`
- controller-owned fact model: `NodeSnapshot`
- controller-owned governed runtime: `DurableRun`

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| pure controller-side scraping and inference | loses short-lived local evidence and makes collection quality depend more heavily on remote availability |
| heavy host-resident agent with local reasoning and action logic | increases host blast radius, host persistence needs, and operational complexity |
| general-purpose observability pipeline first, incident logic later | broader, but less tailored to the current collector-to-controller evidence path |

### Narrow industry comparison

- Compared with OpenTelemetry-style general-purpose collection/export, this repo chooses a narrower and more opinionated collector-to-controller contract because it needs explicit suppression, spool, and controller reconstruction semantics.
- Compared with richer always-on kernel-observability systems such as Pixie or Cilium-family eBPF tooling, this repo intentionally keeps a smaller host-resident runtime and pushes most reasoning back to the controller.

### Consequences

Benefits:

- preserves host-local evidence before transport delay
- keeps governance and action logic off production nodes
- makes controller state and workflow APIs inspectable in one place

Costs:

- introduces transport, replay, and reconstruction complexity
- requires explicit source health and fallback handling

### Current limits

- this is still a single repo’s collector/controller architecture, not a multi-tenant hosted control plane
- controller restart resilience depends on local persistence choices, not a distributed control-plane backend

## ADR-002: Use Probe-Core As Primary Source With Compatibility Fallback

### Status

Accepted and implemented.

### Context

The repo wants higher-fidelity host collection when possible, but it also needs a degraded path when native probe-core is unavailable or stale.

### Decision

Prefer probe-core through [`../../backend/internal/collector/source_pipeline.go`](../../backend/internal/collector/source_pipeline.go), but fall back to the Go compatibility probe when:

- probe-core cannot start
- the latest probe-core frame is stale
- probe-core is disabled and fallback is allowed

### Implementation

Key behavior:

- `sourcePipeline.Collect` prefers `primary.Latest(cfg.ProbeCore.StaleAfter)`
- `sourceCollection` carries `source`, `compatibilityFallback`, `fallbackReason`, `primaryExpected`, and `primaryHealthy`
- probe-core health is surfaced back into collector metrics so ingest and later controller logic can see source quality, not just host metrics

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| probe-core only | more fidelity when healthy, but brittle when the native path is unavailable |
| compatibility only | simpler, but loses native framing, module selection, and higher-fidelity collection behavior |
| merge both every cycle | adds cost and duplicate semantics without a clear gain in the current design |

### Narrow industry comparison

- This is closer to a staged degradation model than to a “single exporter always emits one canonical stream” model.
- It deliberately stays simpler than systems that run multiple simultaneous collection pipelines and reconcile them centrally.

### Consequences

Benefits:

- graceful degradation instead of hard failure
- explicit source-health markers in the evidence path

Costs:

- probe provenance becomes another state dimension later consumers must understand

### Current limits

- compatibility fallback is not feature-equivalent to probe-core
- stale or failed native collection still reduces evidence fidelity even though the pipeline remains alive

## ADR-003: Suppress Before Transport Instead Of Reconstructing Deltas Centrally

### Status

Accepted and implemented.

### Context

Collector steady-state payloads include a lot of repeated runtime, hardware, process, and aux payload information. Re-sending everything every cycle would grow:

- wire cost
- spool growth
- collector CPU and serialization cost
- ingest churn

### Decision

Perform suppression on the host before transport, but emit explicit marker metrics so the controller can reconstruct meaning safely.

### Implementation

Main code:

- [`../../backend/internal/collector/metric_suppression.go`](../../backend/internal/collector/metric_suppression.go)
- [`../../backend/internal/collector/process_payload_suppression.go`](../../backend/internal/collector/process_payload_suppression.go)
- [`../../backend/internal/collector/aux_sampling.go`](../../backend/internal/collector/aux_sampling.go)

Important markers:

- `collector_metrics_partial_update`
- `collector_metrics_suppressed_count`
- `collector_process_payload_refreshed`
- `collector_process_payload_suppressed`
- `collector_aux_payload_refreshed`
- `collector_aux_payload_suppressed`

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| always send full payloads | operationally simple, but too expensive in calm steady state |
| silent omission without markers | unsafe because the controller could not distinguish “unchanged” from “missing” |
| full delta encoding protocol | possible, but more complex than the current marker-plus-reconstruction approach |

### Narrow industry comparison

- General observability exporters often prefer stateless emission and let backends dedupe or compress later. This repo intentionally does some of that work at the edge because the collector already owns the cheapest view of repeated host-local state.

### Consequences

Benefits:

- smaller steady-state batches
- bounded collector and network cost
- explicit semantics for later controller reconstruction

Costs:

- ingest must understand suppression markers correctly
- partial updates increase reasoning complexity at the semantic boundary

### Current limits

- marker bugs would affect every downstream consumer
- process suppression is fingerprint-based and intentionally lossy between forced refreshes

## ADR-004: Use A Disk-Backed Local Spool With ACK-Based Commit

### Status

Accepted and implemented.

### Context

Controller slowness and network partitions often happen exactly when evidence is most valuable. A direct send path or pure in-memory retry buffer would make collection quality too dependent on remote health.

### Decision

Write every serialized batch to a bounded local spool before sending it, and only advance the committed read offset after the controller returns a matching ACK batch ID.

### Implementation

Main code:

- [`../../backend/internal/collector/spool/spool.go`](../../backend/internal/collector/spool/spool.go)
- [`../../backend/internal/collector/transport/client.go`](../../backend/internal/collector/transport/client.go)

Key mechanics:

- `spool.log` stores append-only length-prefixed records
- `spool.offset` stores the committed read offset
- `Next()` reads without advancing
- `Commit(nextOffset)` advances only after successful delivery and ACK validation
- bounded compaction evicts the oldest unread records when the spool must make room

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| direct stream only | transport failure becomes immediate evidence loss or collection delay |
| RAM-only buffering | less durable across crash/restart and harder to bound safely |
| external message bus | stronger global semantics, but adds operational dependency and loses node-local isolation |

### Narrow industry comparison

- Compared with a typical metrics exporter that simply retries in memory, this design spends more complexity on local durability because the repo values preserving recent evidence through short outages.
- Compared with a full distributed log or queue service, this stays intentionally local-first and bounded.

### Consequences

Benefits:

- crash-resistant local replay semantics
- collection stays decoupled from controller slowness

Costs:

- local disk IO
- compaction and corruption-recovery logic
- finite backlog under long outages

### Current limits

- long outages still evict the oldest unread data
- this is not an exactly-once distributed ingestion pipeline

## ADR-005: Reconstruct One Controller-Owned Fact Model At Ingest

### Status

Accepted and implemented.

### Context

Once the collector suppresses or paces some payloads, the controller needs one place to decide whether omitted data means:

- unchanged
- cleared
- missing

If every downstream consumer implemented its own interpretation, the UI, query-service, and workflows would disagree.

### Decision

Resolve suppression, refreshed-empty, and structured field extraction once in ingest and expose one controller-owned `NodeSnapshot` fact model.

### Implementation

Main code:

- [`../../backend/internal/controller/ingest/server.go`](../../backend/internal/controller/ingest/server.go)
- [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go)

Examples:

- `collector_metrics_partial_update` carries forward selected collector/runtime/hardware metrics and structured runtime state
- `collector_aux_payload_refreshed{component=logs|process_fallback}` with an empty payload clears the previous logs or processes
- `node_security_finding` and `node_ebpf_runtime_event` are captured into structured fields instead of only flat metric maps

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| let every API reconstruct its own view | inconsistent semantics and duplicated complexity |
| keep the collector fully stateful and send a controller-ready state object | larger payloads and too much interpretation on the host |
| store raw batches only and derive later | later consumers would repeatedly pay reconstruction cost and risk inconsistent interpretation |

### Narrow industry comparison

- This is closer to an application-specific ingest normalization layer than to a generic telemetry backend that stores raw signals first and interprets them later.

### Consequences

Benefits:

- one shared fact model for queries, UI, and workflows
- explicit structured runtime/security/storage fields

Costs:

- ingest becomes a central semantic boundary
- changes in marker semantics must be handled very carefully

### Current limits

- the controller hot store is still mostly in-memory with optional snapshot persistence, not an append-only evidence journal

## ADR-006: Keep Only Trend-Safe Bounded History In The Hot Path

### Status

Accepted and implemented.

### Context

The controller needs history for trend and predictive logic, but not every metric needs or deserves hot-path retention. Full unbounded retention would raise memory, TSDB, and query cost quickly.

### Decision

Retain only a whitelist of trend-safe metrics in hot history and optional TSDB export.

### Implementation

Main code:

- [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go)
- [`../../backend/internal/controller/timeseries/service.go`](../../backend/internal/controller/timeseries/service.go)

The whitelist is implemented in `shouldStoreTrendMetric(...)`.

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| store every metric and every label | expensive, noisy, and unnecessary for the current reasoning model |
| keep no history in the controller | simpler, but weakens trend and predictive analysis too much |
| outsource all history to an external TSDB | possible, but makes the current control plane less self-contained and more brittle when the TSDB is unavailable |

### Narrow industry comparison

- Compared with TSDB-first observability stacks, this repo keeps a much narrower history surface because it is optimized for RCA and weak-signal logic, not for being a general-purpose metrics warehouse.

### Consequences

Benefits:

- predictable controller memory and TSDB cost
- cleaner signal set for forecasting and weak-signal logic

Costs:

- new metrics are not automatically historical
- some operator questions still require current-state inspection rather than historical analysis

### Current limits

- a metric can be present and useful in the current batch yet still be invisible to trend logic if it is outside the whitelist

## ADR-007: Separate Single-Series Trend Logic From Multivariate Weak-Signal Correlation

### Status

Accepted and implemented.

### Context

The repo needs to answer two different questions:

- is one signal family worsening over time?
- do several individually modest signals now form a credible cluster?

One scoring path would blur those questions together.

### Decision

Keep single-series trend/predictive logic separate from multivariate weak-signal fusion and RCA correlation.

### Implementation

Main code:

- [`../../backend/internal/controller/predictive/engine.go`](../../backend/internal/controller/predictive/engine.go)
- [`../../backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)
- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)

Single-series path:

- `buildRiskSeries`
- `buildTrendAssessments`
- `predictive.Evaluate`

Multivariate path:

- `buildRiskSignals`
- `buildCooccurrences`
- `buildScopeRisks`
- `buildInvestigationEvents`

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| one opaque anomaly score | simpler externally, but harder to explain and challenge |
| larger learned anomaly ensemble | more expressive, but heavier and less inspectable in the current repo |

### Narrow industry comparison

- This is much simpler than Datadog Watchdog-style broad anomaly and correlation products. The repo keeps the logic small, deterministic, and inspectable, at the cost of less broad automatic coverage.

### Consequences

Benefits:

- operators can see whether evidence came from drift, co-occurrence, or both
- easier to tune and review each path independently

Costs:

- more internal scoring paths to document and maintain

### Current limits

- short windows and heuristic thresholds still limit what compound incidents can be detected early

## ADR-008: Gate Hybrid Retrieval And Keep Incident Memory Separate

### Status

Accepted and implemented.

### Context

The system needs externalized knowledge, but blindly attaching retrieved text to every query or workflow step would add noise and reduce operator trust.

The repo also needs to distinguish:

- static knowledge and runbooks
- prior incidents and action outcomes recorded by this system

### Decision

Use gated hybrid retrieval for normalized dataset knowledge, and keep incident memory as a separate durable source that is also retrievable but scored differently.

### Implementation

Main code:

- [`../../backend/internal/controller/rag/`](../../backend/internal/controller/rag/)
- [`../../backend/internal/controller/incidentmemory/store.go`](../../backend/internal/controller/incidentmemory/store.go)
- [`../../backend/internal/controller/agentcore/workflow_memory.go`](../../backend/internal/controller/agentcore/workflow_memory.go)

Important behavior:

- query-service uses `shouldAttachQueryServiceRAG(...)`
- low-confidence RAG hits below `RAGMinConfidence` are suppressed
- hybrid retrieval combines lexical and vector scoring
- incident memory uses deterministic lexical-plus-heuristic ranking over signals, actions, changes, trust, feedback, collector affinity, and recency

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| always append retrieval context | too noisy and too likely to mislead |
| retrieval as vector-only similarity | harder to explain and less aligned with current local-first index design |
| merge incident memory into the same dataset ranking path | would blur “general guidance” with “what previously happened here” |

### Narrow industry comparison

- Compared with broad “RAG everywhere” patterns, this repo treats retrieval as a conditional evidence source.
- Compared with a learned memory store, incident memory here stays deterministic and local-first so operators can inspect why one prior case outranked another.

### Consequences

Benefits:

- smaller and more relevant retrieved context
- clearer provenance between static knowledge and prior local incidents

Costs:

- useful low-confidence hits may be dropped
- weak or stale datasets still limit answer quality

### Current limits

- both retrieval and memory ranking are still heuristic
- stronger semantic analogies can still be missed

## ADR-009: Use A Durable Governed Workflow Runtime Instead Of Free-Form Agent Execution

### Status

Accepted and implemented.

### Context

The repo wants to move beyond passive reporting, but unsafe action generation is easy and unsafe action execution is expensive.

### Decision

Keep workflow execution behind a durable governed runtime with explicit:

- run state
- plan revisions
- tool calls
- policy
- approval
- idempotency
- verification
- compensation
- evidence packaging
- memory write-back

### Implementation

Main code:

- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_tools.go`](../../backend/internal/controller/agentcore/workflow_tools.go)
- [`../../backend/internal/controller/agentcore/workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go)
- [`../../backend/internal/controller/agentcore/workflow_evidence.go`](../../backend/internal/controller/agentcore/workflow_evidence.go)
- [`../../backend/internal/controller/agentcore/workflow_memory.go`](../../backend/internal/controller/agentcore/workflow_memory.go)

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| free-form prompt decides and executes tools directly | too hard to audit, replay, or constrain safely |
| non-durable in-memory workflow state only | cheaper, but restart and postmortem inspection become much weaker |
| distributed workflow engine | stronger at scale, but well beyond the intended operational complexity of the current repo |

### Narrow industry comparison

- Compared with Temporal-style durable workflow engines, this repo keeps a much smaller local-first runtime: Bolt or memory store, durable run structs, and explicit event logging. It gets resumability and auditability without claiming Temporal’s distributed guarantees.

### Consequences

Benefits:

- resumable runs
- explicit approval and verification boundaries
- evidence packages and incident-memory write-back

Costs:

- more persistence and state-machine code
- local-first durability rather than distributed orchestration semantics

### Current limits

- verification is still heuristic
- rollback depends on having good rollback commands and safe action descriptors
- persistence is local-first, not HA workflow orchestration

## ADR-010: Derive Recurring-Burst Context From Existing Metric History

### Status

Accepted and implemented.

### Context

The workflow had a recurring false-positive failure mode:

- a service crossed a threshold
- the recent baseline was lower because the burst was short
- the controller had no persistent workload-specific memory saying that this same service often behaves this way

Examples:

- build services with legitimate CPU and memory spikes
- backup or artifact-upload jobs with legitimate IO/network spikes
- deployment helpers with temporary log bursts
- services that sometimes emit optional latency metrics where the latency regression, not the resource magnitude, is the stronger sign of real user impact

The new design needed to respect several constraints:

- no heavy collector-side enrichment
- bounded state
- one source of truth for long-window metric data
- explicit operator-facing explanations for suppression

### Decision

Keep recurring-burst discrimination on the controller, but derive long-window behavior from the existing metric-history path instead of writing a second behavior store.

Use that memory in the actual workflow decision path to classify active signals as:

- `expected_recurring_burst`
- `suspicious_deviation`
- `correlated_anomaly`
- `confirmed_anomaly`

### Implementation

Main code:

- [`../../backend/internal/controller/agentcore/behavioral_memory.go`](../../backend/internal/controller/agentcore/behavioral_memory.go)
- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)
- [`../../backend/internal/controller/agentcore/evidence_contract.go`](../../backend/internal/controller/agentcore/evidence_contract.go)
- [`../../backend/internal/controller/agentcore/workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go)

The controller now keeps only:

- longer-window reads through `MetricHistoryProvider`
- an optional TSDB-backed durable history path behind that provider
- a bounded in-memory cache for recently fetched history windows

Classification is still explainable. The workflow compares the current burst against:

- the short baseline already computed from `RiskSeries`
- the longer-window mean and spread from history
- a simple hour-of-day recurrence check
- corroborating log, latency, runtime, and security evidence

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| extend `BaselineEngine` as the main recurring-burst memory | it is host-centric drift logic, not workload-centric anomaly discrimination |
| SQLite or BoltDB for behavior profiles | it would create a second general-purpose history store for a problem the existing metric-history path already solves |
| raw long-term history store for every signal | more flexible, but duplicates telemetry and increases read/write cost |
| collector-side learned suppression | violates the collector hot-path cost constraint and makes suppression harder to audit centrally |

### Narrow industry comparison

- Compared with generic observability backends that store everything first and classify later, this repo uses a smaller workflow-local memory layer because it wants low operational overhead and local-first operation.
- Compared with richer seasonality or ML anomaly systems, this design intentionally favors transparent heuristics and bounded state over statistical sophistication.

### Consequences

Benefits:

- fewer false positives from known bursty workloads
- workload-aware suppression without mutating collector behavior
- explicit evidence when the controller suppresses or boosts a signal

Costs:

- the quality of suppression still depends on the quality of the existing long-window history
- identity quality now matters more because unstable service naming fragments memory
- the temporal model is intentionally coarse

### Current limits

- trace behavior is not yet baselined in the same way
- fleet-level peer comparison is not yet implemented
- there is no dedicated durable suppression ledger; if the metric-history path is sparse, the workflow stays conservative

## ADR-011: Split RCA Analysis From Validation And Action

### Status

Accepted and implemented.

### Context

The durable runtime already had governed tools, approval, verification, and replay, but RCA still read too much like one agentic blob:

- analysis and hypothesis generation
- recommendation drafting
- tool-driven follow-up checks
- guarded execution planning

That made the runtime harder to audit because it blurred two different questions:

- what the analysis phase believed first
- what later tool-driven validation confirmed or contradicted

### Decision

Keep one durable runtime, one policy layer, and one governed tool gateway, but split the RCA path into two explicit roles:

- `AnalysisAgent` produces a structured `AnalysisHandoff`
- `ValidationActionAgent` consumes that handoff and runs a bounded ReAct-style validation/action loop

### Implementation

Main code:

- [`../../backend/internal/controller/agentcore/analysis_handoff.go`](../../backend/internal/controller/agentcore/analysis_handoff.go)
- [`../../backend/internal/controller/agentcore/validation_agent.go`](../../backend/internal/controller/agentcore/validation_agent.go)
- [`../../backend/internal/controller/agentcore/validation_types.go`](../../backend/internal/controller/agentcore/validation_types.go)
- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go)

The handoff persists:

- incident summary
- ranked hypotheses and suspected causes
- supporting, weak, and contradicting evidence IDs
- recommendations
- telemetry quality
- unresolved gaps
- suggested validation targets

The validation side persists:

- per-target verdicts
- per-iteration loop records
- tool sequence and observation summaries
- validated and rejected recommendation IDs
- contradiction summary
- post-action validation outcome

### Alternatives considered

| Alternative | Why not chosen here |
| --- | --- |
| keep one mixed RCA loop | harder to audit and easier to blur analysis with action |
| build a separate second runtime | unnecessary infrastructure and duplicate governance paths |
| make the second agent prompt-only | too opaque; tool choice and stop conditions needed explicit code paths |

### Consequences

Benefits:

- cleaner separation between reasoning and validation
- richer second-agent tool selection without weakening governance
- better replay and incident reporting because the handoff and validation state are durable

Costs:

- more persisted workflow state
- a larger tool catalog to test and document

### Current limits

- the validation loop is bounded and conservative by design; it is not an open-ended autonomous agent
- guarded execution still stays approval-first and dry-run-first
- post-action validation is richer than before, but still falls back to deterministic joint-risk checks when deeper evidence is unavailable

## Read Next

- [Architecture](04-architecture.md)
- [Pipeline Deep Dive](02-pipeline-deep-dive.md)
- [Incident Agent Runtime](17-incident-agent-runtime.md)
