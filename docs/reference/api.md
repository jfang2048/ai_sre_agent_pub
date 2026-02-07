# API Reference

Controller HTTP base:

- API prefix: `http://<controller>:8080/api/v1`
- Ops endpoints: `http://<controller>:8080/metrics`, `/health`, `/healthz`

## Version status

`GET /api/v1/status`

Returns controller status including release version.

Example (current release line):

```json
{
  "version": "v0.1",
  "uptime": "running",
  "total_nodes": 3,
  "healthy_nodes": 3,
  "scrape_interval": "15s",
  "listen_address": ":8080"
}
```

## Core fleet APIs

| Endpoint | Method | Description |
|---|---|---|
| `/fleet` | GET | Fleet snapshot from push-ingested collector data |
| `/fleet/{collector_id}` | GET | Snapshot for one collector |
| `/top/programs` | GET | Per-process cross-resource rankings and `resource_pages` |
| `/nodes` | GET, POST | Pull-mode node list management |
| `/nodes/{id}` | GET, DELETE | Pull-mode node detail/removal |
| `/metrics` | GET | Pull-mode aggregated node metrics |
| `/metrics/{id}` | GET | Pull-mode metrics for one node |
| `/metrics/history` | GET | Historical samples (`node`, `limit` query params) |
| `/status` | GET | Controller runtime status |

### `GET /top/programs` notes

Query params:

- `limit` (optional): defaults to `20`, capped at `200`.

Response contains:

- `programs`
- `summary`
- `by_category`
- `report`
- `resource_pages`

Categories:

- `cpu`, `gpu`, `memory`, `network`, `disk`, `disk_io`, `logs`

Semantics:

- `disk`: cumulative storage footprint/activity.
- `disk_io`: live throughput and syscall/event pressure.

## GPU APIs

Available when controller GPU module is enabled (`gpu.enabled=true`).

| Endpoint | Method | Description |
|---|---|---|
| `/gpu/nodes` | GET | Fleet GPU inventory and latest per-device data |
| `/gpu/nodes/{collector_id}` | GET | GPU snapshot for one collector |
| `/k8s/gpu/nodes` | GET | K8s-friendly compact GPU snapshot list |

## Analysis APIs

Available when `analysis.enabled=true`.

| Endpoint | Method | Description |
|---|---|---|
| `/analysis/alerts` | GET | Active/inactive analysis alerts |
| `/analysis/anomalies` | GET | Detected anomalies |
| `/analysis/rca` | GET | Root-cause analysis outputs |
| `/analysis/status` | GET | Analysis subsystem status and config |
| `/analysis/evidence/{node}` | GET | Compact evidence pack for node |

## Agent APIs

Available when `agent.enabled=true`.

| Endpoint | Method | Description |
|---|---|---|
| `/agent/reports` | GET | Reports across nodes |
| `/agent/reports/{node}` | GET | Reports for one node |
| `/agent/reports/latest` | GET | Latest report per node |
| `/agent/reports/{node}/latest` | GET | Latest report for one node |
| `/agent/status` | GET | Agent status summary |
| `/agent/actions` | GET | Action list (`node`, `limit` query params) |
| `/agent/actions/{id}` | POST, PATCH | Update action status/note |
| `/agent/incidents` | GET | Incident assessments |
| `/agent/incidents/{id}` | GET | One incident assessment |
| `/agent/incidents/{id}/context` | GET | Context bundle for one incident |

Agent API notes:

- `limit` is optional for list endpoints. If provided, it must be a non-negative integer.
- `/agent/reports` and `/agent/actions` return newest entries first.
- `POST`/`PATCH /agent/actions/{id}` accepts JSON body fields:
  - `status` (optional)
  - `note` (optional)
- At least one of `status` or `note` must be provided.
- Supported action statuses:
  `proposed`, `acknowledged`, `in_progress`, `completed`, `dismissed`, `accepted`, `rejected`, `canceled`.

Example action update:

```bash
curl -X PATCH http://<controller>:8080/api/v1/agent/actions/<id> \
  -H 'Content-Type: application/json' \
  -d '{"status":"in_progress","note":"owner assigned"}'
```

## Incident ingestion API

Available when incidents coordinator is enabled (`incidents.enabled=true`).

| Endpoint | Method | Description |
|---|---|---|
| `/incidents/alerts` | POST | Push external alert and trigger context aggregation |

Expected payload fields:

- `id`, `title`, `service`, `severity`
- `starts_at`, `ends_at`
- `labels`, `annotations`

## External checks APIs

Available when `checks.enabled=true`.

| Endpoint | Method | Description |
|---|---|---|
| `/checks` | GET | Latest check results |
| `/checks/history` | GET | Check history |

## Ops endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/health` | GET | Health probe |
| `/healthz` | GET | Health probe |
| `/metrics` | GET | Prometheus exposition |

## Authentication

When controller auth is enabled (`auth.enabled=true` and API key env set), include:

```text
Authorization: Bearer <api-key>
```

Default API key environment variable: `SRE_AGENT_CONTROLLER_API_KEY`.
