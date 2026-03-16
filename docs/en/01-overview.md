# Overview

中文版本：[docs/zh/01-overview.md](../zh/01-overview.md)

AI SRE Agent is a push-first observability and guarded-automation project for AI/GPU infrastructure.

This project is easiest to understand if you keep one idea in mind:

> The collector is a low-impact observer on the host. The controller is the heavier control plane that stores, analyzes, retrieves context, and serves people.

In the current control-plane path, the controller does not jump from raw metrics straight into a prompt. It first produces structured intermediate evidence:

- `TrendAssessment[]` for single-metric drift, persistence, and short-horizon forecast hints
- `InvestigationEvent[]` for multivariate weak-signal fusion
- `RetrievalDecision[]` for explicit “why retrieval ran, skipped, or was suppressed” tracking

If you want one document that explains the whole real path from the first collected metric to the final response, read:

- [Pipeline Deep Dive](02-pipeline-deep-dive.md)

If you want focused subsystem explanations after that, read:

- [Collector Queue and Compaction](06-collector-queue-and-compaction.md)
- [Control-Plane Analysis](07-control-plane-analysis.md)

The repository is organized around two maintained runtime roles:

- `collector`
  - runs on or near the observed host
  - gathers host, process, kernel, GPU, and log signals
  - buffers outgoing telemetry in a local spool
- `controller`
  - receives telemetry over gRPC
  - serves the HTTP API and web UI
  - stores current state and optional history
  - runs RAG and agent workflows

The same split is now exposed through explicit deployment modes in config:

- `local-dev`: repo-relative paths and local debugging assumptions
- `standalone`: one controller service plus external collectors
- `cluster-lite`: one controller `Deployment` plus collector `DaemonSet`
- `distributed`: replicated controller plus shared HA/storage backends

Those modes are implemented in:

- [`../../backend/internal/collector/deployment.go`](../../backend/internal/collector/deployment.go)
- [`../../backend/internal/controller/deployment.go`](../../backend/internal/controller/deployment.go)
- [`../../configs/controller.yaml`](../../configs/controller.yaml)
- [`../../configs/collector.yaml`](../../configs/collector.yaml)

## What A New Reader Should Expect

This repo already gives you:

- a collector/controller split runtime
- a real host-observer telemetry path
- controller-side RAG and prompt assembly
- a web UI and HTTP API
- deployment assets for local, Docker, Kubernetes, Helm, and systemd paths

This repo does not automatically give you:

- a hosted control plane
- a managed release service
- a curated production runbook corpus for your environment
- a guarantee that every host can expose full eBPF / probe-core visibility without operator setup

One practical design choice matters more than most first-time readers expect:

- the collector is optimized for a cheap steady state, so unchanged low-churn state, cached helper payloads, and cache-hit compatibility hardware payloads are intentionally suppressed instead of resent every cycle
- the controller is optimized for selective reasoning, so RAG is skipped when telemetry is stale, empty, already reused safely, or too weak to form a meaningful operational retrieval query

## What Exists in This Repo

- `backend/`
  - Go services for collector, controller, ingest, RAG, APIs, and workflows
- `cpp/`
  - native probe-core runtime used by the collector
- `frontend/`
  - web UI
- `configs/`
  - source-mode and container-mode configuration
- `dataset/`
  - seed knowledge base for controller-side RAG
- `deploy/`
  - Docker, Kubernetes, Helm, and systemd deployment assets

## Main Data Flow

```text
probe-core + eBPF -> collector -> local spool -> gRPC ingest -> controller state/history/RAG -> API/UI/agent workflows
```

The collector is the host-local observer. The controller is the control plane.

## Three Concrete Scenarios

### Scenario 1: A GPU node slows down after rollout

What the system is meant to do:

1. collect CPU, memory, disk, network, process, and GPU evidence on the host
2. ship it to the controller without blocking on controller reachability
3. decide whether the telemetry is fresh enough to trust
4. optionally retrieve similar incidents or runbook fragments
5. return a bounded operator-facing explanation

What you would read next:

- [Data flow](05-data-flow.md)
- [Metrics and signals](13-metrics-and-signals.md)
- [Prompts and customization](12-prompts-and-customization.md)

### Scenario 2: You want to replace the dataset with your own runbooks

What the system is meant to do:

1. discover dataset files under `dataset_path`
2. normalize and classify them
3. chunk them into retrieval units
4. rebuild or update the local index

What you would read next:

- [Dataset and RAG](11-dataset-and-rag.md)
- [Core files](10-core-files.md)

### Scenario 3: You want to know why the agent skipped the LLM

What the system is meant to do:

1. inspect telemetry freshness and coverage
2. bypass expensive reasoning if the evidence is stale or absent
3. return deterministic fallback instead of pretending confidence

What you would read next:

- [Data flow](05-data-flow.md)
- [Deployment](15-deployment.md)
- [FAQ](16-faq.md)

## One Concrete Example

If a GPU training node slows down after a rollout, the intended reading of the system is:

1. [`../../cpp/probe_core/main.cpp`](../../cpp/probe_core/main.cpp) and the collector capture host, process, and GPU pressure.
2. [`../../backend/internal/controller/ingest/server.go`](../../backend/internal/controller/ingest/server.go) accepts the batch and writes it into the controller store.
3. [`../../backend/internal/controller/telemetry_quality.go`](../../backend/internal/controller/telemetry_quality.go) decides whether the evidence is fresh and complete enough to trust.
4. [`../../backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go) turns that state into trend assessments, investigation events, and retrieval decisions.
5. [`../../backend/internal/controller/rag/service.go`](../../backend/internal/controller/rag/service.go) can retrieve similar incidents or runbook fragments from [`../../dataset/`](../../dataset/).
6. [`../../backend/internal/controller/agentcore/prompts.go`](../../backend/internal/controller/agentcore/prompts.go) and [`../../backend/internal/controller/agent/engine.go`](../../backend/internal/controller/agent/engine.go) turn that evidence into an operator-facing answer or report.

If the same operator keeps asking the same question while the compact evidence stays unchanged, [`../../backend/internal/controller/agentcore/agent.go`](../../backend/internal/controller/agentcore/agent.go) can now reuse the recent successful analysis and skip both retrieval and the model call.

That single example is why the repo is split into collector, controller, dataset/RAG, and UI instead of one large binary.

## Recommended Reading Order

1. [Getting started](03-getting-started.md)
2. [Architecture](04-architecture.md)
3. [Pipeline Deep Dive](02-pipeline-deep-dive.md)
4. [Data flow](05-data-flow.md)
5. [Collector Queue and Compaction](06-collector-queue-and-compaction.md)
6. [Control-Plane Analysis](07-control-plane-analysis.md)
7. [UI guide](08-ui-guide.md)
8. [Codebase map](09-codebase-map.md)
9. [Core files](10-core-files.md)
10. [Dataset and RAG](11-dataset-and-rag.md)
11. [Prompts and customization](12-prompts-and-customization.md)
12. [Metrics and signals](13-metrics-and-signals.md)
13. [Hardware considerations](14-hardware-considerations.md)
14. [Deployment](15-deployment.md)
15. [FAQ](16-faq.md)

## If You Only Have Five Minutes

Read these pages in order:

1. [Getting started](03-getting-started.md) for how the repo actually runs
2. [Architecture](04-architecture.md) for the runtime split
3. [Pipeline Deep Dive](02-pipeline-deep-dive.md) for the full code-grounded first-sample-to-final-answer story
4. [Data flow](05-data-flow.md) for concrete metric-to-answer walkthroughs
5. [UI guide](08-ui-guide.md) for how those same intermediate artifacts appear in the investigation console

## Detailed Reference

Use the existing deeper docs when you need file-by-file or API-level detail:

- [Operations usage](../operations/usage.md)
- [Configuration reference](../operations/configuration.md)
- [Architecture notes](../design/architecture.md)
- [API reference](../reference/api.md)
- [Metrics reference](../reference/metrics.md)
- [RAG reference](../reference/rag_knowledge_engine.md)
- [LLM schema reference](../reference/llm_schema.md)
