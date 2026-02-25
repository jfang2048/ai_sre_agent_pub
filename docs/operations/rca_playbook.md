# RCA Playbook (v0.4)

## Goal
Drive incident triage from measured signals to a reproducible root-cause record using existing diagnostics APIs.

## RCA Workflow
```mermaid
flowchart TD
    A[Incident trigger] --> B["/api/v1/status + /ingest/status"]
    B --> C["/api/v1/top/programs"]
    C --> D["/api/v1/diagnostics/data-path"]
    D --> E["/api/v1/diagnostics/kernel-path"]
    E --> F["/api/v1/diagnostics/root-cause"]
    F --> G["/api/v1/diagnostics/rca-packet"]
```

## Step-by-Step
1. Confirm runtime health.
```bash
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/ingest/status
```

2. Identify dominant processes and resource categories.
```bash
curl -sS "http://127.0.0.1:8080/api/v1/top/programs?limit=20"
```

3. Pull diagnostics chain.
```bash
curl -sS "http://127.0.0.1:8080/api/v1/diagnostics/data-path?collector_id=<id>"
curl -sS "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path?collector_id=<id>"
curl -sS "http://127.0.0.1:8080/api/v1/diagnostics/root-cause?collector_id=<id>"
```

4. Generate portable RCA packet.
```bash
curl -sS "http://127.0.0.1:8080/api/v1/diagnostics/rca-packet?collector_id=<id>&format=markdown&download=1"
```

## Evidence Checklist
- incident time window and affected collector IDs
- top resource categories from `/top/programs`
- data-path and kernel-path evidence used by root-cause output
- relevant logs (`/api/v1/logs/search`) and GPU events (`/api/v1/gpu/events`) when applicable

## Safe Remediation Rules
- prefer reversible mitigations first;
- avoid changing multiple subsystems at once;
- record metric impact before and after action;
- for AGENT execution paths, keep approval controls enabled.
