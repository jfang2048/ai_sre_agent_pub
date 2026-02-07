# Architecture (v0.1)

This document reflects the current implementation in this repository.

## System model

The platform is push-first:

- `sre-collector` runs on each monitored host.
- `sre-controller` receives telemetry over gRPC and serves API/UI/Prometheus.

```mermaid
flowchart LR
    subgraph Host
      COL[sre-collector]
      PROC[/proc + /sys/]
      LOGS[log files]
      GPU[nvidia-smi]
      EBPF[eBPF sidecar socket (optional)]
      PROC --> COL
      LOGS --> COL
      GPU --> COL
      EBPF --> COL
    end

    subgraph Controller
      ING[gRPC ingest]
      STORE[in-memory store]
      GPUOBS[gpuobs store]
      API[REST APIs]
      PROM[/metrics/]
      ANA[analysis/agent/incidents (optional)]
    end

    COL --> ING --> STORE
    STORE --> GPUOBS --> API
    STORE --> API
    STORE --> ANA --> API
    STORE --> PROM
```

## Main components

| Component | Current responsibility | Key code paths |
|---|---|---|
| Collector runtime | Collect metrics/processes/logs, spool, push over gRPC | `backend/internal/collector` |
| Probe collectors | `/proc`/kernel/GPU/eBPF collection primitives | `backend/internal/probe` |
| Ingest store | Batch ingest, snapshot APIs, process-resource aggregation | `backend/internal/controller/ingest` |
| Ranking API | Per-process ranking across CPU/GPU/memory/network/disk/disk_io/logs | `backend/internal/controller/top_handlers.go` |
| GPU aggregation | Fleet GPU snapshots/history + K8s shape | `backend/internal/controller/gpuobs` |
| Analysis/agent | Deterministic analysis + optional LLM/RAG + actions | `backend/internal/controller/analysis`, `backend/internal/controller/agent` |
| Incident context orchestration | Alert -> resource/metrics/log/k8s context bundle | `backend/internal/incidents` |

## Data model highlights

A telemetry batch contains:

- Collector identity and version.
- Metric stream (`node_*`, `rca_*`, optional `node_ebpf_*`, optional external/shm metrics).
- Top process samples.
- Log fingerprints.

Controller-derived process ranking state keeps:

- `signal_values` (current),
- `signal_totals`/`category_totals` (overall),
- `signal_frequency`/`category_frequency` (how often),
- `log_errors`/`log_warnings`.

## Resource category semantics

- `disk`: cumulative storage footprint/activity totals.
- `disk_io`: live throughput and syscall/event pressure.

This distinction is exposed in `/api/v1/top/programs` and the UI pages.

## API surface groups

- Core fleet: `/api/v1/fleet`, `/api/v1/top/programs`, `/api/v1/status`.
- GPU: `/api/v1/gpu/nodes`, `/api/v1/k8s/gpu/nodes`.
- Optional analysis: `/api/v1/analysis/*`.
- Optional agent: `/api/v1/agent/*`.
- Optional checks: `/api/v1/checks*`.
- Incident ingestion: `POST /api/v1/incidents/alerts`.
- Ops: `/metrics`, `/healthz`.

## Frontend serving

Controller serves:

- `/ui` and `/` when web assets exist.
- Inline fallback UI when assets are absent.

The ranking UI is page/tab-based to avoid overcrowding and uses `resource_pages` from `/api/v1/top/programs`.
