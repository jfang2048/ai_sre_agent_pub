# Metrics Reference (v0.5)

## Producer layers

```mermaid
flowchart LR
    A[probe-core and probe collectors] --> B[TelemetryBatch metrics]
    C[collector runtime counters] --> B
    B --> D[controller ingest]
    D --> E[/api/v1/fleet]
    D --> F[/metrics]
```

## Key metric families

| Prefix | Description |
|---|---|
| `node_*` | host/process/network/disk/gpu telemetry |
| `rca_*` | resource attribution signals used in diagnostics |
| `collector_*` | collector runtime state (spool/transport/probe source) |
| `probe_core_*` | kernel-first probe-core internals and source selection |
| `node_ebpf_*` | optional eBPF event counters |

## Probe-core source semantics

v0.5 emits source labels so operators can verify primary-vs-fallback behavior.

| Metric | Meaning |
|---|---|
| `probe_core_host_collection_source{source=kernel|proc}` | host source selected for sample |
| `probe_core_network_collection_source{source=netlink|proc}` | network source selected |
| `probe_core_disk_collection_source{source=sysfs|proc}` | disk source selected |
| `probe_core_host_kernel_primary_available` | kernel path availability signal |
| `probe_core_sampling_effective_interval_ms` | adaptive effective interval |
| `probe_core_sampling_backoff_events_total` | backoff expansion count |
| `probe_core_process_sampling_interval_samples` | process-sampling cadence |
| `probe_core_netlink_refresh_interval_samples` | netlink dump cadence |
| `probe_core_netlink_refresh_age_samples` | age since last netlink refresh |
| `probe_core_cgroup_refresh_interval_samples` | cgroup file refresh cadence |
| `probe_core_cgroup_refresh_age_samples` | age since last cgroup refresh |

## Domain coverage

- CPU/scheduler: usage, load, runnable/blocked processes.
- Memory: total/used/available plus pressure-related counters.
- Disk/NVMe: throughput, latency, queue depth, utilization.
- Network/TCP/softnet/RDMA: traffic, drops, retransmits, congestion.
- GPU: device utilization/memory/temperature/power + process attribution.
- Logs/eBPF: indexed log signals and optional kernel event summaries.
