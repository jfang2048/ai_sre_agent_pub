# API Reference (v0.6)

All JSON APIs are under `/api/v1/*`.

## Core service and topology

| Method | Path |
|---|---|
| GET, POST | `/api/v1/nodes` |
| GET, DELETE | `/api/v1/nodes/{id}` |
| GET | `/api/v1/metrics` |
| GET | `/api/v1/metrics/{id}` |
| GET | `/api/v1/metrics/history` |
| GET | `/api/v1/topology` |
| GET | `/api/v1/status` |
| GET | `/api/v1/ha/status` |
| GET | `/healthz` |
| GET | `/metrics` |

## Ingest, storage, and fleet

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/ingest/status` | ingest counters + store/log stats |
| GET | `/api/v1/ingest/schema` | ingest validation contract |
| GET | `/api/v1/storage/status` | retention + persistence + federation hints + TSDB health/mode |
| GET, POST, PUT, PATCH | `/api/v1/storage/retention` | update bounded retention parameters |
| GET | `/api/v1/finops/signals` | CPU/memory/GPU waste indicators |
| GET | `/api/v1/fleet` | collector list snapshot |
| GET | `/api/v1/fleet/{collector_id}` | node details (metrics/process/log/storage/security findings) |
| GET | `/api/v1/fleet/timeseries` | trend series window (TSDB-backed when enabled, memory fallback otherwise) with per-series signal tier/trend semantics plus `operational_insights[]` summarizing multi-signal risk patterns |

## Diagnostics

| Method | Path |
|---|---|
| GET | `/api/v1/diagnostics/data-path` |
| GET | `/api/v1/diagnostics/kernel-path` |
| GET | `/api/v1/diagnostics/root-cause` |
| GET | `/api/v1/diagnostics/workload-path` |
| GET | `/api/v1/diagnostics/rca-packet` |
| GET | `/api/v1/diagnostics/ai-infra-stack` |

## Analysis and incidents

| Method | Path |
|---|---|
| GET | `/api/v1/analysis/status` |
| GET | `/api/v1/analysis/alerts` |
| GET | `/api/v1/analysis/anomalies` |
| GET | `/api/v1/analysis/correlations` |
| GET | `/api/v1/analysis/rca` |
| GET | `/api/v1/analysis/incidents` |
| GET | `/api/v1/analysis/evidence/{node}` |
| POST | `/api/v1/incidents/alerts` |

## Agent workflow

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/agent/query` | NL query -> summary/findings/recommendations/actions + retrieved knowledge evidence |
| POST | `/api/v1/agent/execute` | execute pending query-service action |
| GET | `/api/v1/agent/status` | engine status |
| GET | `/api/v1/agent/reports` | incident-aware reports |
| GET | `/api/v1/agent/reports/latest` | latest report per node |
| GET | `/api/v1/agent/actions` | queued/known actions |
| PATCH, POST | `/api/v1/agent/actions/{id}` | action status/note update |
| GET | `/api/v1/agent/incidents` | workflow assessments |
| GET | `/api/v1/agent/incidents/{alert_id}` | single assessment |
| GET | `/api/v1/agent/incidents/{alert_id}/context` | aggregated context payload |
| POST | `/api/v1/agent/incidents/{alert_id}/actions/{action_id}/execute` | guarded execution |
| POST | `/api/v1/agent/incidents/{alert_id}/actions/{action_id}/rollback` | guarded rollback (reversible actions) |
| GET | `/api/v1/agent/incidents/{alert_id}/actions/audit` | immutable action audit records |
| GET | `/api/v1/agent/potential-risks` | proactive latent-risk findings (ranked evidence + trend/correlation context) |
| GET | `/api/v1/agent/joint-risk` | deterministic joint-risk report (time-series + co-occurrence + scope ranking) |
| GET | `/api/v1/agent/rca` | structured RCA workflow output (hypotheses + evidence + guarded recommendations) |
| GET | `/api/v1/agent/workflow/incidents` | incident workflow list (open/closed, timeline/evidence context) |
| GET | `/api/v1/agent/workflow/incidents/{incident_id}` | single incident workflow report |
| GET | `/api/v1/agent/workflow/audit` | workflow tool/action audit trail |
| GET | `/api/v1/agent/proposed-actions` | proposed action queue with policy/approval metadata |
| GET | `/api/v1/agent/trace/` | recent workflow traces |
| GET | `/api/v1/agent/trace/{trace_id}` | full workflow trace (plans, tool calls, hypothesis updates, recommendations, actions) |

Agent workflow responses may include retrieval fields when RAG is enabled:

- `retrieved_docs[]`
- `retrieval_summary`
- `retrieval_evidence_ids[]`
- `retrieval_confidence`

## Knowledge / RAG

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/rag/status` | index readiness, dataset path, doc/chunk counts, source type stats |
| POST | `/api/v1/rag/query` | hybrid/lexical/vector retrieval over the local knowledge base |
| POST | `/api/v1/rag/index/rebuild` | full re-ingest + re-index from dataset/source paths |
| POST | `/api/v1/rag/index/update` | incremental refresh |
| GET | `/api/v1/rag/doc/{id}` | normalized document + related chunks by document/chunk id |

## Security APIs

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/security/findings` | normalized findings (`id`, `finding_id`, `severity`, `category`, `scope`, `evidence`, `recommended_action`, `source`) |
| GET | `/api/v1/security/dashboard` | findings + summary + trends payload for UI |
| GET | `/api/v1/security/trends` | trend-only payload |

Collector-side security findings are emitted as structured `node_security_finding` envelopes before they reach the controller. The controller persists those envelopes, combines them with eBPF runtime events, log hints, and metric drift, and then exposes a single normalized finding list through the security APIs and the agent workflow.

## API-first controller

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/controller/incidents/intake` | alert/anomaly/manual incident intake |
| GET | `/api/v1/controller/telemetry/metrics` | metrics + history query (TSDB-backed when enabled) |
| GET | `/api/v1/controller/telemetry/logs` | indexed log query/fingerprint fallback |
| GET | `/api/v1/controller/telemetry/security` | security evaluator query |
| GET, POST | `/api/v1/controller/agent/runs` | list/start deterministic agent run |
| GET | `/api/v1/controller/agent/runs/{run_id}` | inspect run status/result |
| POST | `/api/v1/controller/agent/runs/{run_id}/stop` | stop run |
| POST | `/api/v1/controller/actions/dry-run` | guarded action dry-run |
| POST | `/api/v1/controller/actions/approve` | approval gate token validation |
| POST | `/api/v1/controller/actions/execute` | guarded action execute |
| POST | `/api/v1/controller/actions/rollback` | guarded action rollback |
| GET | `/api/v1/controller/audit` | controller-side immutable audit rows |
| GET | `/api/v1/controller/tools` | explicit workflow tool registry |

### Joint-risk query params

`GET /api/v1/agent/joint-risk`

- `collector_id` optional target collector (`collector`, `node` aliases also accepted)
- `window` optional duration (`30m`, `1h`, ...)
- `limit` optional report list cap
- `dry_run` optional bool override for recommendation generation
- `refresh` optional bool (`true` default) to run a fresh workflow cycle before returning history

Response shape:

- `reports[].risk_score`, `reports[].risk_level`, `reports[].actionable_why`
- `reports[].signals[]` weighted low-severity signal entries
- `reports[].cooccurrences[]` correlated A+B(+C) groups inside the same window
- `reports[].scope_risks[]` process/node/pod/service/cluster risk ranking
- `reports[].series[]` chart-ready time-series for UI drilldowns

### Potential-risks query params

`GET /api/v1/agent/potential-risks`

- same query params as joint-risk (`collector_id`, `window`, `limit`, `dry_run`, `refresh`)
- `refresh=true` runs proactive scans for the selected node (or recent fleet nodes if no collector is provided)

Response shape:

- `findings[].risk_summary` risk summary with top data-point references
- `findings[].contributing_signals[]` weak signals with `current`, `baseline`, `delta_percent`, and `score`
- `findings[].time_window`, `findings[].scope`, `findings[].confidence_score`
- `findings[].suggested_investigation_steps[]` deterministic evidence-linked checks
- `findings[].series[]`, `findings[].correlations[]` for trend/correlation drilldown

### RCA workflow query params

`GET /api/v1/agent/rca`

- same query params as joint-risk (`collector_id`, `window`, `limit`, `dry_run`, `refresh`)
- `trigger` optional workflow trigger tag (`anomaly`, `incident_alert`, ...)

Response shape:

- `reports[].trace_id` replayable workflow trace identifier
- `reports[].synthesized_incident` grouped incident object built before RCA
  - `incident_id`, `summary`, `grouped_signals[]`, `impacted_scope[]`, `time_window`, `severity`, `confidence`, `candidate_root_cause_cluster`
- `reports[].context` gathered metrics/process/kernel/log/topology summary
  - includes condensed log clusters, timeline transitions, top offenders, security findings, trace summary, and GPU summary when available
- `reports[].hypotheses[]` ranked root-cause hypotheses with confidence
  - every hypothesis may include `supporting_evidence_ids[]` and `contradicting_evidence_ids[]`
- `reports[].evidence[]` metric/log/correlation proof rows
- `reports[].recommendations[]` categorized, evidence-linked next steps
  - `category` can currently be `immediate_investigation`, `probable_containment`, `medium_term_remediation`, or `structural_prevention`
  - each recommendation includes `rationale`, `expected_impact`, `risk_level`, `confidence`, `evidence_ids[]`, `approval_reason`, `rollback_consideration`
- `reports[].proposed_actions[]` guarded execution candidates derived from recommendations
  - includes `policy.status`, `approval_required`, `dry_run_plan`, `rollback_plan`, `audit_intent`, and evidence linkage
- `reports[].unresolved_gaps[]` explicit evidence gaps that kept the workflow conservative
- `reports[].agent_loop` explicit plan -> act -> verify trace
  - `completed=true` means the required evidence steps verified successfully
  - optional misses such as empty knowledge retrieval do not mark the loop incomplete by themselves
  - `plan_steps[].required` marks steps that block completion
  - `plan_steps[].superseded_by` indicates a failed step was replaced by a narrower or broader retry step
- `reports[].structured_report` explainable RCA packet
  - `incident_summary`, `symptoms`, `impacted_scope`, `timeline`, `ranked_hypotheses`, `supporting_evidence`, `contradicting_evidence`, `confidence_score`, `unresolved_gaps`, `recommended_next_steps`, `safe_remediations`
- `reports[].tool_calls[]`, `reports[].stages[]`, `reports[].reproducibility` for deterministic replay context

### RAG query payload

`POST /api/v1/rag/query`

```json
{
  "query": "deployment timeout cache credentials",
  "top_k": 5
}
```

Response shape:

- `query`
- `hits[].doc_id`
- `hits[].chunk_id`
- `hits[].score`
- `hits[].source_path`
- `hits[].snippet`
- `hits[].metadata`
- `retrieval_mode`
- `latency_ms`

### Workflow audit query params

`GET /api/v1/agent/workflow/audit`

- `limit` optional record cap
- `workflow_id` optional exact workflow filter

### Proposed actions query params

`GET /api/v1/agent/proposed-actions`

- `limit` optional record cap (default `100`)
- `status` optional exact status filter

Response shape:

- `actions[]` newest-first proposed actions
- `actions[].policy.status` explicit guardrail verdict
- `actions[].approval_required`, `actions[].approval_reason`
- `actions[].rollback_plan`, `actions[].dry_run_plan`
- `actions[].evidence_ids[]`, `actions[].confidence`, `actions[].risk_level`

### Agent trace query params

`GET /api/v1/agent/trace/`

- `limit` optional trace cap (default `50`)

Response shape:

- `traces[]` newest-first workflow traces
- each trace includes `incident`, `plan_versions`, `tool_calls`, `hypothesis_updates`, `recommendations`, `proposed_actions`, `stages`, `final_risk_score`, `unresolved_gaps`

`GET /api/v1/agent/trace/{trace_id}`

- returns one `trace`
- `404` when the trace ID is unknown

## Logs, GPU, Kubernetes, orchestration

| Method | Path |
|---|---|
| GET | `/api/v1/logs/status` |
| GET | `/api/v1/logs/search` |
| POST | `/api/v1/logs/ingest` |
| GET | `/api/v1/ebpf/events` |
| GET | `/api/v1/ebpf/summary` |
| GET | `/api/v1/gpu/nodes` |
| GET | `/api/v1/gpu/timeline` |
| GET | `/api/v1/gpu/processes` |
| GET | `/api/v1/gpu/events` |
| GET | `/api/v1/gpu/correlation` |
| GET | `/api/v1/k8s/status` |
| GET | `/api/v1/k8s/workloads/top` |
| GET | `/api/v1/k8s/nodes/top` |
| GET | `/api/v1/orchestration/status` |
| GET | `/api/v1/orchestration/workloads` |

`/api/v1/ebpf/summary` now includes bounded correlation state in addition to raw syscall/process counters:

- `category_counts`
- `remote_scope_counts`
- `sensitive_path_counts`

That payload is designed for controller/UI/agent consumption. It is intentionally bounded and classification-oriented; it is not a raw-event archive.

## Incident action execute payload

`POST /api/v1/agent/incidents/{alert_id}/actions/{action_id}/execute`

```json
{
  "dry_run": true,
  "approval_token": "optional-token"
}
```

- `dry_run` optional; action default applies when omitted.
- `approval_token` is required for non-dry-run execution of approval-gated actions.

Example response:

```json
{
  "result": {
    "alert_id": "alert-1",
    "action_id": "alert-1-check-metrics",
    "action_type": "diagnostic_check_metrics",
    "status": "executed",
    "message": "metric diagnostic complete: metric_scopes=2 symptoms=4",
    "dry_run": false,
    "safe": true,
    "requires_approval": false,
    "reversible": true,
    "audit_id": "audit-alert-1-check-metrics-1730000000000000000",
    "rollback_id": "rb-alert-1-check-metrics-1730000000000000000",
    "rollback_state": "prepared",
    "started_at": "2026-02-26T12:00:00Z",
    "completed_at": "2026-02-26T12:00:00Z"
  },
  "timestamp": "2026-02-26T12:00:00Z"
}
```

Status mapping:

- `200` success
- `404` incident/action not found
- `428` approval token required
- `403` approval token invalid
- `500` internal execution error

## Incident action rollback payload

`POST /api/v1/agent/incidents/{alert_id}/actions/{action_id}/rollback`

```json
{
  "dry_run": true,
  "approval_token": "optional-token",
  "rollback_id": "optional-explicit-target"
}
```

Example response:

```json
{
  "result": {
    "alert_id": "alert-1",
    "action_id": "alert-1-check-metrics",
    "action_type": "diagnostic_check_metrics",
    "status": "rolled_back",
    "message": "rollback completed for diagnostic_check_metrics",
    "dry_run": false,
    "rollback_id": "rb-alert-1-check-metrics-1730000000000000000",
    "audit_id": "audit-alert-1-check-metrics-1730000001000000000",
    "started_at": "2026-02-26T12:05:00Z",
    "completed_at": "2026-02-26T12:05:00Z"
  },
  "timestamp": "2026-02-26T12:05:00Z"
}
```

Rollback status mapping (in addition to execute mappings):

- `409` action is not reversible
- `404` rollback target not found

## Controller tool registry shape

`GET /api/v1/controller/tools`

Each tool entry is explicit and structured. Current fields include:

- `name`
- `purpose`
- `input_schema`
- `output_schema`
- `determinism`
- `read_only`
- `requires_approval`
- `supports_dry_run`
- `supports_rollback`
- `side_effects`
- `safety_class`

## Standby-mode behavior

When `ha.mode=standby`, mutating endpoints return `503` (read-only standby):

- node mutations (`POST /nodes`, `DELETE /nodes/{id}`)
- retention updates (`POST|PUT|PATCH /storage/retention`)
- agent action execution/rollback mutation paths
