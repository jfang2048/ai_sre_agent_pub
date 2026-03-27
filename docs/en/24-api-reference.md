# API Reference (v0.8)

All JSON APIs are served under `/api/v1/*`.

This reference is organized by control-plane responsibility rather than by handler package because most readers want to know what surface to call, not where the HTTP function lives.

## Service Health And Topology

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/healthz` | liveness-style health |
| GET | `/readyz` | readiness-style health with startup checks |
| GET | `/metrics` | Prometheus metrics |
| GET | `/api/v1/status` | controller runtime summary, deployment metadata, collector coverage |
| GET | `/api/v1/ha/status` | HA status surface |
| GET | `/api/v1/topology` | topology view |

## Ingest, Storage, And Fleet

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/ingest/status` | ingest counters plus store/log stats |
| GET | `/api/v1/ingest/schema` | ingest validation contract |
| GET | `/api/v1/storage/status` | retention, persistence, TSDB health/mode |
| GET, POST, PUT, PATCH | `/api/v1/storage/retention` | retention updates |
| GET | `/api/v1/fleet` | fleet snapshot |
| GET | `/api/v1/fleet/{collector_id}` | per-node detail view |
| GET | `/api/v1/fleet/timeseries` | chart-friendly timeseries plus telemetry-quality context |

## Analysis And Incident Surfaces

| Method | Path |
| --- | --- |
| GET | `/api/v1/analysis/status` |
| GET | `/api/v1/analysis/alerts` |
| GET | `/api/v1/analysis/anomalies` |
| GET | `/api/v1/analysis/correlations` |
| GET | `/api/v1/analysis/rca` |
| GET | `/api/v1/analysis/incidents` |
| GET | `/api/v1/analysis/evidence/{node}` |
| POST | `/api/v1/incidents/alerts` |

## Agent Query And Report Surfaces

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/api/v1/agent/query` | query-service path with deterministic fallback and optional retrieval |
| POST | `/api/v1/agent/execute` | execute pending query-service action |
| GET | `/api/v1/agent/status` | query-service, control-plane, report-engine, and workflow summary counters |
| GET | `/api/v1/agent/reports` | incident-aware reports |
| GET | `/api/v1/agent/reports/latest` | latest report per node |
| GET | `/api/v1/agent/actions` | known queued actions |
| PATCH, POST | `/api/v1/agent/actions/{id}` | action status or note update |
| GET | `/api/v1/agent/proposed-actions` | proposed action queue with policy and approval metadata |
| GET | `/api/v1/agent/trace/` | recent workflow traces |
| GET | `/api/v1/agent/trace/{trace_id}` | full trace view |

## Incident Workflow Surfaces

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/agent/joint-risk` | deterministic joint-risk report |
| GET | `/api/v1/agent/potential-risks` | proactive latent-risk findings |
| GET | `/api/v1/agent/rca` | structured RCA workflow output |
| GET | `/api/v1/agent/workflow/audit` | workflow tool/action audit trail |
| GET | `/api/v1/agent/workflow/runs` | durable workflow run list |
| GET | `/api/v1/agent/workflow/runs/{run_id}` | one durable run record |
| GET | `/api/v1/agent/workflow/evidence/{run_id}` | generated evidence package |
| GET | `/api/v1/agent/workflow/incidents` | workflow incident list |
| GET | `/api/v1/agent/workflow/incidents/{incident_id}` | one workflow incident report |

## RAG And Knowledge

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/rag/status` | index readiness, dataset path, counts, type stats |
| POST | `/api/v1/rag/query` | local-first retrieval query |
| POST | `/api/v1/rag/index/rebuild` | full rebuild |
| POST | `/api/v1/rag/reindex` | rebuild alias |
| POST | `/api/v1/rag/index/update` | incremental refresh |
| GET | `/api/v1/rag/doc/{id}` | normalized document and related chunks |

## Security Surfaces

| Method | Path |
| --- | --- |
| GET | `/api/v1/security/findings` |
| GET | `/api/v1/security/dashboard` |
| GET | `/api/v1/security/trends` |

## Workflow Response Surfaces Worth Knowing

The workflow APIs now expose more than a summary and recommendation list.

Important RCA fields include:

- `suspected_root_cause_entity`
- `causal_path`
- `impact_path`
- `impact_scope`
- `uncertainty`
- `evidence_provenance`
- `change_links`
- `adaptive_baselines`
- `behavioral_assessments`
- `incident_memory_matches`

Important joint-risk and trend fields now also include:

- `behavioral_classification`
- `suppression_factor`
- `suppression_reason`
- `original_score`

Behavioral-assessment objects are exposed so operators can see why the workflow suppressed or escalated a signal instead of guessing from the final score alone.

Important workflow-run fields include:

- run status
- durable step records
- tool calls
- policy records
- approval records
- verification records
- compensation records
- evidence-package reference
- world-model snapshot
- memory-record references

## Why These Fields Exist

These fields exist because the repo is now trying to answer operational questions that a simple RCA summary cannot answer:

- what changed recently?
- what is likely cause versus downstream symptom?
- what prior incidents looked similar?
- which bursts were suppressed because they matched learned workload behavior?
- what did the workflow execute or skip?
- what policy blocked or approved a step?
- what evidence package supports the final conclusion?

## Query Parameters To Know

Common workflow query parameters:

- `collector_id`
- `window`
- `limit`
- `dry_run`
- `refresh`
- `trigger` on the RCA path

These parameters matter because the APIs expose both stored history and optionally refreshed workflow runs.

## Practical Validation Calls

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/api/v1/status
curl -fsS http://127.0.0.1:8080/api/v1/rag/status
curl -fsS "http://127.0.0.1:8080/api/v1/agent/joint-risk?limit=3"
curl -fsS "http://127.0.0.1:8080/api/v1/agent/rca?limit=3"
curl -fsS "http://127.0.0.1:8080/api/v1/agent/workflow/runs?limit=5"
```

## Boundaries

This reference is not a generated OpenAPI document. It is a code-grounded operator reference to the repository’s maintained surfaces.
