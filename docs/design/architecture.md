# Architecture Specification (v0.4)

## Runtime Components
```mermaid
flowchart LR
    subgraph Host
        P[probe collectors]
        C[sre-collector]
        S[spool]
        P --> C --> S
    end

    subgraph Control
        G[gRPC ingest server]
        M[ingest.MemoryStore]
        L[logindex.Index]
        U[gpuobs.Store]
        H[HTTP server]
        G --> M
        G --> L
        G --> U
        M --> H
        L --> H
        U --> H
    end

    S --> G
```

## Probe-Controller Separation
- Collector owns data capture and reliability of delivery (`spool` + ACK commit).
- Controller owns validation, aggregation, diagnostics, and API exposure.
- Optional modules (`analysis`, `agent`, `orchestration`, `inventory`, `k8s`, `checks`) are mounted on top of ingest state.

## Data Path
```mermaid
flowchart TD
    A[collector batch] --> B[gRPC Push stream]
    B --> C[validateBatch]
    C --> D[StoreBatchMeta]
    C --> E["StoreMetrics/Processes/Logs"]
    E --> F[fleet APIs]
    E --> G[top programs + diagnostics]
    E --> H[log index + gpu processors]
```

## API Exposure Model
- Core endpoints are always registered in `registerHandlers`.
- Optional endpoints are registered only when module manager/engine is initialized.
- Auth middleware wraps the mux when API key is configured.

## Storage Model
- Fleet state: in-memory snapshots keyed by `collector_id`.
- Fleet trends: bounded metric history rings per collector.
- Logs: segmented in-memory index with retention eviction.
- GPU: in-memory aggregate + timeline rings + persisted snapshots/history/events.

## Design Constraints in v0.4
- Memory-first state yields low-latency reads but short retention by default.
- Pull-scrape and push-ingest states coexist; push-ingest is primary for fleet and diagnostics.
