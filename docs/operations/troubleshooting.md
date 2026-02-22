# Troubleshooting Guide

Operate in `SMART START` order to avoid blind mitigation:
`status -> ingest -> timeseries -> top/programs -> diagnostics/data-path -> diagnostics/kernel-path -> diagnostics/root-cause -> diagnostics/workload-path -> topology`.

## Fast health sweep

```bash
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s http://127.0.0.1:8080/api/v1/inventory/status
```

## Common issues

### `fleet` is empty

Checks:

```bash
curl -s http://127.0.0.1:8080/api/v1/ingest/status
```

- `batches_total` not increasing: collector cannot push to gRPC ingest.
- `rejected_total` increasing: payload validation failure; inspect `last_error` and `GET /api/v1/ingest/schema`.

### Probe inventory is stale

Checks:

```bash
curl -s http://127.0.0.1:8080/api/v1/inventory/probes
```

- `healthy=false` with old `last_seen`: collector stopped or network path broken.
- static probe exists but no telemetry source: collector ID/name mismatch.

Optional heartbeat registration:

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/inventory/heartbeat \
  -H 'Content-Type: application/json' \
  -d '{"probe_id":"node-a","hostname":"node-a"}'
```

### K8s APIs return empty/disabled

Checks:

```bash
curl -s http://127.0.0.1:8080/api/v1/k8s/status
```

- `enabled=false`: set `kubernetes.enabled=true`.
- refresh failures: verify kubeconfig/context permissions and network reachability.
- no workloads: check namespace/label selector scope and pod visibility.

### High latency / pressure triage

Suggested order:

1. `GET /api/v1/fleet/timeseries` for shape (spike vs sustained).
2. `GET /api/v1/top/programs` for process-level ownership.
3. `GET /api/v1/k8s/workloads/top` for pod/workload-level ownership.
4. `GET /api/v1/diagnostics/data-path` and `GET /api/v1/diagnostics/kernel-path` for cross-layer + kernel-stage bottlenecks.
5. `GET /api/v1/diagnostics/root-cause` for ranked hypotheses and evidence.
6. `GET /api/v1/diagnostics/workload-path` to map pressure into workload/service spread across nodes.
7. If RCA returns `scheduler_contention_tail_latency`, confirm `cpu_iowait_percent`, `cpu_pressure_some_avg10`, `procs_running`, and `procs_blocked` in `GET /api/v1/fleet/timeseries`.
8. `GET /api/v1/k8s/nodes/top` and `GET /api/v1/topology` for blast radius.
