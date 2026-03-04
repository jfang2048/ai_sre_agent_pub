# API Reference (v0.5)

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
| GET | `/api/v1/storage/status` | retention + persistence + federation hints |
| GET, POST, PUT, PATCH | `/api/v1/storage/retention` | update bounded retention parameters |
| GET | `/api/v1/finops/signals` | CPU/memory/GPU waste indicators |
| GET | `/api/v1/fleet` | collector list snapshot |
| GET | `/api/v1/fleet/{collector_id}` | node details (metrics/process/log/storage) |
| GET | `/api/v1/fleet/timeseries` | trend series window |

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
| POST | `/api/v1/agent/query` | NL query -> summary/findings/recommendations/actions |
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

## Security APIs

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/security/findings` | normalized findings (`id/severity/category/scope/evidence/action`) |
| GET | `/api/v1/security/dashboard` | findings + summary + trends payload for UI |
| GET | `/api/v1/security/trends` | trend-only payload |

## API-first controller

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/controller/incidents/intake` | alert/anomaly/manual incident intake |
| GET | `/api/v1/controller/telemetry/metrics` | metrics + history query |
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

- `reports[].context` gathered metrics/process/kernel/log/topology summary
- `reports[].hypotheses[]` ranked root-cause hypotheses with confidence
- `reports[].evidence[]` metric/log/correlation proof rows
- `reports[].recommendations[]` safe-first, guarded remediation/check steps
- `reports[].agent_loop` explicit plan -> act -> verify trace
- `reports[].structured_report` symptoms/timeline/scope/supporting/disconfirming signal packet
- `reports[].tool_calls[]`, `reports[].stages[]`, `reports[].reproducibility` for deterministic replay context

### Workflow audit query params

`GET /api/v1/agent/workflow/audit`

- `limit` optional record cap
- `workflow_id` optional exact workflow filter

## Logs, GPU, Kubernetes, orchestration

| Method | Path |
|---|---|
| GET | `/api/v1/logs/status` |
| GET | `/api/v1/logs/search` |
| POST | `/api/v1/logs/ingest` |
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

## Standby-mode behavior

When `ha.mode=standby`, mutating endpoints return `503` (read-only standby):

- node mutations (`POST /nodes`, `DELETE /nodes/{id}`)
- retention updates (`POST|PUT|PATCH /storage/retention`)
- agent action execution/rollback mutation paths
