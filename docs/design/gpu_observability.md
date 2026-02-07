# GPU Observability Design

This document describes the controller-side GPU aggregation module used in the current codebase.

## Scope

GPU observability in this repository provides:

- Fleet-wide GPU inventory/snapshot APIs.
- Per-node GPU history persistence.
- K8s-friendly snapshot shaping.
- Limited Prometheus re-export for stable cardinality.

## Data source

Collector emits `node_gpu_*` metrics when `nvidia-smi` is available and returns data.

Controller ingest pipeline:

1. gRPC ingest receives telemetry batches.
2. `gpuobs.Store` extracts GPU metrics.
3. Store updates in-memory snapshots and persists periodic history.

## In-memory model

Primary keys:

- Node: `collector_id`
- Device: `gpu_id`
- Process (per device): `pid`

The store keeps bounded per-process details (`max_processes_per_gpu`) to avoid unbounded growth.

## Persistence model

Default path: `./data/gpu`

- Latest snapshots: `./data/gpu/snapshots/<collector_id>.json`
- Daily history: `./data/gpu/history/<collector_id>-YYYY-MM-DD.jsonl`

Retention is time-based (`gpu.retention` in controller config).

## API outputs

Base prefix: `/api/v1`

- `GET /gpu/nodes`
- `GET /gpu/nodes/{collector_id}`
- `GET /k8s/gpu/nodes`

`/k8s/gpu/nodes` is intentionally compact for scheduler/controller consumption.

## Prometheus re-export

Controller `/metrics` re-exports a constrained subset:

- `node_gpu_utilization_sm_percent`
- `node_gpu_memory_used_mib`
- `node_gpu_memory_total_mib`

Labels include node and GPU identity when available.

## Performance design

Current implementation minimizes overhead by:

- Avoiding per-metric map allocations in hot parsing paths.
- Updating per-process structures incrementally and sorting only on output paths.
- Persisting snapshots/history in buffered, batched patterns.
- Exporting only required fields for Prometheus scrape paths.

## Integration notes

- This module complements (not replaces) NVIDIA device plugin/GPU Operator.
- Typical K8s pipelines:
  - scrape `/metrics` into Prometheus/Adapter, or
  - poll `/api/v1/k8s/gpu/nodes` from a custom controller.
