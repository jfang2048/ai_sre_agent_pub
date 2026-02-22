# RCA Playbook

## Objective

Generate a consistent, evidence-based RCA for GPU/AI infra incidents with minimal toil.

## Workflow

1. Define incident window and scope (`service`, `cluster`, `collector_id`).
2. Capture control-plane status and ingest quality.
3. Identify top offenders by process, workload (pod), and node.
4. Correlate with topology and recent orchestration/self-healing actions.
5. Propose reversible remediation and confirm post-action recovery.

## SMART START evidence map

Use the same framework across incidents to reduce noisy handoffs.

| Letter | RCA question | Primary evidence |
|---|---|---|
| `S` | What exactly is impacted and when did it start? | `/api/v1/status`, incident window |
| `M` | Is ingest/control-plane healthy enough to trust data? | `/api/v1/ingest/status`, `/metrics` |
| `A` | Is this a spike, drift, or saturation plateau? | `/api/v1/fleet/timeseries` |
| `R` | Who is driving pressure right now? | `/api/v1/top/programs`, `/api/v1/k8s/workloads/top` |
| `T` | Where is the bottleneck across network/storage/compute? | `/api/v1/diagnostics/data-path`, `/api/v1/diagnostics/kernel-path`, `/api/v1/diagnostics/root-cause`, `/api/v1/diagnostics/workload-path`, `/api/v1/topology` |
| `S` | Is storage path a primary or contributing factor? | `storage_devices`, `filesystems` in `/api/v1/fleet/<collector>` |
| `T` | How far did impact propagate? | `/api/v1/k8s/nodes/top`, `/api/v1/topology` |
| `A` | What is the safest reversible mitigation? | runbook action + guardrails |
| `R` | Did the same metrics improve post-action? | re-run timeseries + rankings |
| `T` | Is handoff complete for next shift/team? | filled RCA template + owner/due date |

## Evidence collection checklist

```bash
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s "http://127.0.0.1:8080/api/v1/fleet/timeseries?window=1h&limit=360"
curl -s "http://127.0.0.1:8080/api/v1/top/programs?limit=50"
curl -s "http://127.0.0.1:8080/api/v1/k8s/workloads/top?metric=pressure&limit=30"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path"
curl -s "http://127.0.0.1:8080/api/v1/k8s/nodes/top?metric=pressure&limit=20"
curl -s http://127.0.0.1:8080/api/v1/topology
curl -s http://127.0.0.1:8080/api/v1/orchestration/events
```

## RCA template

- Incident ID:
- Start / End time:
- Impacted services:
- User impact:
- Primary bottleneck:
- Contributing factors:
- Top process offenders:
- Top workload/pod offenders:
- Top node offenders:
- Mitigation taken:
- Why mitigation worked:
- Follow-up actions (owner + due date):

## Finding quick map

- `network_congestion_training_slowdown`: RDMA/TCP congestion dominates inter-node communication path.
- `storage_latency_gpu_starvation`: storage/data-loader latency suppresses GPU feed rate.
- `scheduler_contention_tail_latency`: run queue + blocked tasks + iowait/PSI indicate CPU scheduling contention.
- `memory_pressure_io_amplification`: dirty/writeback/reclaim pressure amplifies tail I/O.
- `cross_node_communication_imbalance`: per-node communication load skew creates collective stragglers.

## Safe remediation principles

- Prefer reversible actions first (drain traffic, scale out, requeue batch, restart one pod).
- Avoid broad destructive changes during live incidents.
- Require approval for mutating AGENT actions in production.
- Verify improvement with the same metrics used to detect the incident.
