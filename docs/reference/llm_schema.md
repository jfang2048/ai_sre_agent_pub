# LLM Input Schema (Schema `v1`)

This document describes the JSON schema sent to the LLM path in the current controller implementation.

Important:

- Schema version is `v1`.
- Product release version is `v0.1`.
- They are independent.

## Top-level structure

```json
{
  "schema_version": "v1",
  "generated_at": "2026-02-06T12:00:00Z",
  "node_name": "node-01",
  "metrics": {
    "node_cpu_usage_percent": 82.5,
    "node_memory_Used_bytes": 12300000000
  },
  "trends": {
    "node_cpu_usage_percent": "rising",
    "node_memory_Used_bytes": "stable"
  },
  "alerts": ["alert-node-01-cpu-high"],
  "anomalies": ["CPU usage anomaly detected"],
  "context": "RCA request from controller analysis engine",
  "evidence": {
    "schema_version": "v1",
    "node_name": "node-01",
    "summary": {
      "node_cpu_usage_percent": 82.5
    },
    "top_metrics": [
      { "name": "node_cpu_usage_percent", "value": 82.5 }
    ],
    "gpu": {},
    "network": {},
    "disk": {},
    "memory": {},
    "processes": [
      {
        "pid": 1234,
        "name": "python",
        "cpu_percent": 92,
        "rss_bytes": 123456789,
        "io_read_bps": 0,
        "io_write_bps": 0
      }
    ],
    "logs": [
      { "fingerprint": "abc123", "count": 12, "example": "OOM killed process 1234" }
    ],
    "alerts": ["alert-node-01-cpu-high"],
    "anomalies": ["CPU usage anomaly detected"],
    "context": "RCA request from controller analysis engine"
  }
}
```

## Field reference

| Field | Type | Description |
|---|---|---|
| `schema_version` | string | Fixed schema identifier (`v1`) |
| `generated_at` | string | RFC3339 timestamp |
| `node_name` | string | Node/collector identity |
| `metrics` | object | Numeric metric snapshot |
| `trends` | object or null | Trend labels (`rising`, `falling`, `stable`) |
| `alerts` | array[string] | Alert IDs or alert-like findings |
| `anomalies` | array[string] | Anomaly summaries/forecasts |
| `context` | string | Prompt context text |
| `evidence` | object | Compact evidence pack |

### Evidence pack fields

| Field | Type | Description |
|---|---|---|
| `schema_version` | string | Evidence schema version (`v1`) |
| `node_name` | string | Node/collector identity |
| `summary` | object | Selected high-signal metrics |
| `top_metrics` | array | Top metrics by value |
| `gpu`, `network`, `disk`, `memory` | object | Domain-specific metric subsets |
| `processes` | array | Top process summaries |
| `logs` | array | Log fingerprint summaries |
| `alerts` | array[string] | Related alert IDs |
| `anomalies` | array[string] | Related anomaly text |
| `context` | string | Evidence context text |

## Source locations

- Schema structs/builders: `backend/internal/controller/analysis/schema.go`
- Evidence pack: `backend/internal/controller/analysis/evidence.go`
- LLM client env + provider handling: `backend/internal/controller/analysis/llm_client.go`
