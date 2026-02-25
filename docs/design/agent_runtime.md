# Agent Runtime Design (v0.4)

## Architecture
```mermaid
flowchart LR
    I[ingest.MemoryStore snapshot] --> E[agent.Engine]
    A["analysis.Engine RCA/anomalies"] --> E
    P[playbook policy file] --> E
    R[RAG index optional] --> E
    E --> Q[agentcore QueryService]
    Q --> API["/api/v1/agent/query"]
    Q --> EX["/api/v1/agent/execute"]
    E --> REP["/api/v1/agent/reports*"]
    E --> ACT["/api/v1/agent/actions*"]
```

## Runtime Responsibilities
- `agent.Engine`
  - periodic report generation (`cfg.Interval`)
  - action generation and bounded action/report retention
  - optional persistence under `agent.persist_dir`
- `agentcore.QueryService`
  - query and execute API payload handling
  - rate/circuit/timeout behavior for LLM calls
  - execution guardrails (approval token, dry-run semantics)

## API Interaction
```mermaid
sequenceDiagram
    participant U as User/API client
    participant S as QueryService
    participant E as agent.Engine
    U->>S: POST /api/v1/agent/query
    S->>E: collect context + policies + evidence
    E-->>S: proposed actions/report context
    S-->>U: structured query response

    U->>S: POST /api/v1/agent/execute
    S->>S: validate approval/idempotency
    S-->>U: execution result
```

## Data Inputs
- latest ingest node snapshots and process/log summaries
- analysis alerts/anomalies/RCA outputs when analysis module is enabled
- optional RAG snippets from configured local paths

## Runtime Limits
- report retention (`max_reports`) and action retention (`max_actions`) are bounded.
- LLM usage is optional and disabled by default.
