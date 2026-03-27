# Overview

中文版本：[docs/zh/01-overview.md](../zh/01-overview.md)

AI SRE Agent is a push-first observability and evidence-grounded incident agent for Linux, GPU, and AI infrastructure.

The shortest accurate summary of the repository is:

> the collector preserves short-lived host evidence at low steady-state cost; the controller turns that evidence into normalized state, bounded history, retrieval, RCA, and governed workflow output.

That sentence matters because it explains the design boundary. This project is not built around "ask an LLM about a dashboard." It is built around an evidence pipeline that can still function when retrieval or LLM paths are unavailable.

## What Problem The Repository Is Solving

Modern infra teams face three recurring operational problems:

1. the first useful evidence is often node-local and easy to miss
2. the relevant evidence is cross-layer, not only metrics or only logs
3. actionability requires governance, not only explanation

Before a design like this:

- pull scraping could miss brief onset evidence
- GPU, runtime, process, storage, and network signals lived in different tools
- retrieval and LLM usage had weak boundaries
- action suggestions were difficult to audit or approve safely

The repository addresses those problems by splitting responsibilities cleanly:

- the collector does cheap local observation plus bounded replay
- ingest rebuilds a controller-owned fact model
- analysis turns raw telemetry into structured intermediate evidence
- retrieval is explicit and selective
- the workflow runtime handles policy, verification, compensation, and audit

## One-Screen Mental Model

```mermaid
flowchart LR
    A[Host evidence] --> B[Collector]
    B --> C[Ingest and hot state]
    C --> D[Structured evidence]
    D --> E[Retrieval and memory]
    E --> F[RCA and workflow]
    F --> G[Governed operator output]
```

## What The Current Codebase Contains

| Area | Main path | What it does |
| --- | --- | --- |
| host collection | [`../../backend/internal/collector/`](../../backend/internal/collector/), [`../../cpp/probe_core/`](../../cpp/probe_core/) | host, process, GPU, runtime, and compatibility telemetry collection |
| ingest and hot state | [`../../backend/internal/controller/ingest/`](../../backend/internal/controller/ingest/) | batch validation, state reconstruction, bounded history retention |
| analysis and workflows | [`../../backend/internal/controller/agentcore/`](../../backend/internal/controller/agentcore/) | trend analysis, weak-signal fusion, RCA, recommendations, governed workflows |
| retrieval | [`../../backend/internal/controller/rag/`](../../backend/internal/controller/rag/) | local-first knowledge ingestion, indexing, and retrieval |
| incident memory | [`../../backend/internal/controller/incidentmemory/`](../../backend/internal/controller/incidentmemory/) | durable storage and retrieval of prior incident outcomes |
| change and causality | [`../../backend/internal/controller/changeintel/`](../../backend/internal/controller/changeintel/), [`../../backend/internal/controller/causalgraph/`](../../backend/internal/controller/causalgraph/) | change correlation and cause-vs-symptom ranking |
| evaluation | [`../../backend/internal/controller/eval/`](../../backend/internal/controller/eval/), [`../../backend/internal/controller/evaluation/`](../../backend/internal/controller/evaluation/) | golden-case evaluation and replay stability |

## Why The Collector / Controller Split Exists

The split is not cosmetic.

If collection, storage, reasoning, UI, and automation all ran in one place:

- the observed host would pay more overhead
- transport failure would be harder to isolate from collection failure
- short-lived host evidence would be more fragile
- the reasoning path would be harder to scale and audit

The current split manages those tradeoffs explicitly:

| Runtime role | Primary responsibility | Constraint it manages |
| --- | --- | --- |
| collector | preserve host-local evidence and push it cheaply | do not become part of the incident |
| controller | normalize, retain, analyze, retrieve, explain, and govern | keep heavier state and reasoning off the observed host |

## Why The Controller Does Not Jump Straight To An LLM

The controller first creates intermediate evidence objects such as:

- `TrendAssessment[]`
- `InvestigationEvent[]`
- `RetrievalDecision[]`
- RCA hypotheses and evidence rows
- change links
- adaptive baseline insights
- causal-path output

This design exists because raw telemetry is not a safe reasoning surface:

- it is noisy
- it is larger than the final prompt budget
- it mixes transient symptoms with persistent state
- it is hard to audit after the fact

By converting telemetry into bounded, typed evidence first, the repository gains:

- cheaper reasoning
- more legible UI output
- selective retrieval
- deterministic fallback when model paths are unavailable

## What v0.8 Changes In Practice

The main change in the current repository state is that the incident path is now documented as a real workflow runtime, not as an implied future direction.

That includes:

- durable workflow state
- change intelligence
- causal graph reasoning
- incident-memory write-back and retrieval
- governed execution with approval and verification
- replay-oriented evaluation

See [Incident Agent Runtime](17-incident-agent-runtime.md) for the runtime-level explanation.

## Three Ways To Read The Project

### Engineering narrative

This is a bounded evidence system.

It is stronger than naive LLM-based ops tooling because it:

- preserves onset evidence locally
- normalizes data before reasoning
- gates retrieval instead of always attaching it
- keeps deterministic fallbacks
- centralizes workflow governance

### Product narrative

This is an operations product for environments where:

- infrastructure is expensive
- incidents are cross-layer
- operator time is scarce
- automation must fail safely

Its value is reducing uncertainty, not pretending to eliminate it.

### Research narrative

This is an implementation-oriented AIOps architecture that makes weak-signal analysis, retrieval gating, deterministic fallbacks, and governed action concrete enough to inspect in code.

## Recommended Reading Order

1. [Architecture](04-architecture.md)
2. [Pipeline Deep Dive](02-pipeline-deep-dive.md)
3. [Control-Plane Analysis](07-control-plane-analysis.md)
4. [Incident Agent Runtime](17-incident-agent-runtime.md)
5. [Testing and Evaluation](19-testing-and-evaluation.md)

## If You Need One Concrete Example

For a rollout-linked GPU slowdown, the current intended path is:

1. the collector captures host, process, GPU, and log evidence
2. ingest rebuilds hot state and metric history
3. analysis creates trend and weak-signal evidence
4. change intelligence scores recent rollout/config/runtime changes
5. RCA builds hypotheses, evidence, and change links
6. causal graph ranking separates likely causes from symptoms
7. incident memory and static knowledge can both be retrieved
8. the workflow runtime records policy, verification, and evidence artifacts

That is the architectural thesis of the current repository in one incident.
