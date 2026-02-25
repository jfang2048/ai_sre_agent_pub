# Iteration Progress (v0.4)

## Completed Work
- Standardized Markdown documentation structure across root/docs/tests/deploy trees.
- Rewrote `README.md` into strict bilingual format:
  - Chinese detailed section first
  - English complete section second
- Added implementation-backed Mermaid diagrams for architecture, data flow, probe-controller interaction, and log indexing.
- Normalized technical terminology to runtime code naming (collector, controller, ingest, logindex, gpuobs, agentcore).

## Implementation Areas Covered
```mermaid
flowchart LR
    A[collector runtime] --> B[transport + spool]
    B --> C[controller ingest]
    C --> D[log index]
    C --> E[gpu store]
    C --> F[diagnostics + optional modules]
    F --> G[security audit docs]
```

## Documentation Coverage Map
```mermaid
flowchart TD
    A[Root docs] --> A1["README/SECURITY/CONTRIBUTING"]
    B[Design docs] --> B1["architecture/log/GPU/agent"]
    C[Operations docs] --> C1["quickstart/config/usage/testing/playbooks"]
    D[Reference docs] --> D1["api/metrics/llm schema"]
    E["Deploy/tests docs"] --> E1["k8s README + tests README"]
```

## Documentation Outputs Updated
- Root docs: contribution, security, status, audit, progress files.
- Design docs: architecture, GPU, agent runtime, log subsystem.
- Operations docs: quickstart, config, usage, testing, RCA and RDMA playbooks.
- Reference docs: API, metrics, LLM schema.
- Deployment docs: Kubernetes push-first manifests guide.
- Test docs: suite and UI test guidance.

## Verification Actions
- Checked handler registration paths for API accuracy.
- Checked config defaults from `DefaultConfig` implementations.
- Checked ingest/log/GPU hard limits from source constants.
