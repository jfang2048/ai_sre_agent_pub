# RCA Playbook (v0.7)

## Goal
Drive incident triage from measured signals to a reproducible root-cause record using existing diagnostics APIs.

## RCA Workflow
```mermaid
flowchart TD
    A["Incident trigger"] --> B["/api/v1/agent/joint-risk"]
    B --> C["/api/v1/agent/rca"]
    C --> D["/api/v1/agent/workflow/audit"]
    D --> E["/api/v1/analysis/incidents + /analysis/correlations"]
    E --> F["/api/v1/diagnostics/data-path + /diagnostics/kernel-path"]
    F --> G["/api/v1/diagnostics/root-cause + /diagnostics/rca-packet"]
```

## 中文说明

- 这条 RCA 路径不是“多打几个 API 看结果”，而是按从弱信号聚合、到结构化 RCA、到工作流审计、再到 diagnostics 细证据的顺序组织。
- `joint-risk` 放在最前面，是因为系统希望先确认哪些信号在同一时间窗、同一 scope 里共同抬升，再去解释单一根因。先做聚合，再做归因，稳定性更高。
- `workflow/audit` 被放进主流程，不是附属功能。RCA 只有在能回放“看过哪些证据、怎么重排假设、为什么保守或为什么给出动作建议”时，才真正可用。

## Step-by-Step
1. Confirm runtime health.
```bash
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/ingest/status
```

2. Run deterministic joint-risk scoring for the incident window.
```bash
curl -sS "http://127.0.0.1:8080/api/v1/agent/joint-risk?collector_id=<id>&window=45m&limit=3"
```

3. Generate structured RCA workflow output (hypotheses + evidence + guarded recommendations).
```bash
curl -sS "http://127.0.0.1:8080/api/v1/agent/rca?collector_id=<id>&window=45m&limit=3&trigger=incident_alert"
```

4. Inspect workflow/tool audit trail for replayability.
```bash
curl -sS "http://127.0.0.1:8080/api/v1/agent/workflow/audit?limit=50"
```

5. Identify dominant processes and resource categories.
```bash
curl -sS "http://127.0.0.1:8080/api/v1/top/programs?limit=20"
```

6. Pull analysis-layer conclusions first (classification + evidence).
```bash
curl -sS "http://127.0.0.1:8080/api/v1/analysis/status"
curl -sS "http://127.0.0.1:8080/api/v1/analysis/incidents?collector_id=<id>&limit=5"
curl -sS "http://127.0.0.1:8080/api/v1/analysis/correlations?collector_id=<id>"
curl -sS "http://127.0.0.1:8080/api/v1/analysis/anomalies"
```

7. Pull diagnostics chain.
```bash
curl -sS "http://127.0.0.1:8080/api/v1/diagnostics/data-path?collector_id=<id>"
curl -sS "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path?collector_id=<id>"
curl -sS "http://127.0.0.1:8080/api/v1/diagnostics/root-cause?collector_id=<id>"
```

8. Generate portable RCA packet.
```bash
curl -sS "http://127.0.0.1:8080/api/v1/diagnostics/rca-packet?collector_id=<id>&format=markdown&download=1"
```

## Evidence Checklist
- incident time window and affected collector IDs
- analysis incident classification, confidence, and supporting signals
- top resource categories from `/top/programs`
- data-path and kernel-path evidence used by root-cause output
- relevant logs (`/api/v1/logs/search`) and GPU events (`/api/v1/gpu/events`) when applicable

## Safe Remediation Rules
- prefer reversible mitigations first;
- avoid changing multiple subsystems at once;
- record metric impact before and after action;
- for AGENT execution paths, keep approval controls enabled.
