# Metrics Reference (v0.6)

## Producer layers

```mermaid
flowchart LR
    A[probe-core host and GPU pipeline] --> B[TelemetryBatch metrics]
    A2[eBPF runtime event pipeline] --> B
    A3[collector security audit] --> B
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
| `collector_*` | collector runtime state, probe-core primary health, compatibility-fallback state |
| `probe_core_*` | primary probe-core internals and source selection |
| `node_ebpf_*` | primary eBPF runtime event counters and summaries |
| `node_security_*` | collector-side normalized security posture/drift counters and structured findings |

## Primary-path source semantics

v0.6 emits explicit source labels and collector runtime markers so operators can verify primary-vs-fallback behavior.

| Metric | Meaning |
|---|---|
| `collector_probe_source{source=probe_core|go}` | host/process telemetry source selected for this batch |
| `collector_primary_ebpf_expected` | eBPF runtime is configured as the primary kernel-event path |
| `collector_primary_ebpf_healthy` | eBPF runtime started and is healthy enough to emit kernel-event telemetry |
| `collector_primary_ebpf_reason{reason=...}` | degraded reason when the primary eBPF runtime is unavailable (`start_failed`, `disabled`, `unavailable`) |
| `collector_primary_probe_core_expected` | probe-core is expected to be the primary host telemetry path |
| `collector_primary_probe_core_healthy` | probe-core is producing fresh frames |
| `collector_compatibility_fallback_active` | the legacy Go host collector is currently active as fallback |
| `collector_compatibility_fallback_reason{reason=...}` | fallback cause (`probe_core_start_failed`, `probe_core_stale`, ...) |
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
- GPU: device inventory, SM/memory utilization, PCIe throughput, BAR1, ECC, and process attribution.
- Logs/eBPF: indexed log signals and primary kernel event summaries.
- Security: `node_security_finding` envelopes plus counters for permission drift, process/port mismatch, sensitive-path access, suspicious outbound, scheduler anomalies, and kernel posture drift.

## GPU primary-path metrics

Probe-core now tries dynamic NVML first and falls back to bounded `nvidia-smi` queries only when NVML is unavailable. That keeps the steady-state path closer to the driver/runtime boundary while still remaining runnable on hosts where NVML cannot be loaded.

| Metric | Source | Purpose | Notes |
|---|---|---|---|
| `probe_core_gpu_collection_source{source=nvml\|nvidia_smi\|unavailable}` | probe-core | shows the actual GPU source path used in the last refresh | value is `1` on success, `0` when GPU sampling could not produce device data |
| `probe_core_gpu_probe_success` | probe-core | coarse GPU sampler health bit | `1` when at least one device sample was collected |
| `node_gpu_count` | probe-core alias | inventory size | feeds controller GPU inventory |
| `node_gpu_info{gpu_id,uuid,name,driver_version,pci_bus_id}` | probe-core alias | stable GPU identity labels | emitted once per device refresh |
| `node_gpu_utilization_sm_percent` | NVML / fallback query | SM busy signal | primary scheduling pressure signal |
| `node_gpu_utilization_memory_percent` | NVML / fallback query | memory-controller pressure | useful for compute-vs-memory bottleneck split |
| `node_gpu_memory_{total,used,free}_mib` | NVML / fallback query | framebuffer pressure | `node_gpu_memory_used_percent` is also emitted |
| `node_gpu_power_{draw,limit}_watts` | NVML / fallback query | power ceiling / saturation context | useful when utilization is low but power throttle is high |
| `node_gpu_pcie_{gen,width,gen_max,width_max}` | NVML / fallback query | current vs max PCIe link state | topology and under-training hints |
| `node_gpu_pcie_{rx,tx}_mb_s` | NVML / fallback query | current PCIe traffic | supports host/network/GPU cross-correlation |
| `node_gpu_pcie_{bandwidth_theoretical,bandwidth_max}_mb_s` | probe-core derived | current/max link capacity estimate | derived from PCIe generation × width |
| `node_gpu_pcie_{rx,tx,link}_utilization_percent` | probe-core derived | PCIe saturation indicator | bounded, easier for Agent/UI consumption than raw MB/s alone |
| `node_gpu_bar1_memory_{total,used,free}_mib` | NVML | BAR1 aperture pressure | useful for driver/runtime memory mapping issues |
| `node_gpu_ecc_{single,double}_bit_errors_total` | NVML | reliability signal | double-bit errors are treated as more severe downstream |
| `node_gpu_{process_count,context_count,kernel_active_contexts}` | NVML | runtime occupancy hints | context counts are currently derived from running compute contexts |
| `node_gpu_process_memory_mib` | NVML / fallback query | per-process GPU memory attribution | keyed by `pid` and GPU labels |
| `node_gpu_process_mem_util_percent` | NVML | process share of device memory | omitted when total memory is unavailable |
| `node_gpu_process_context_active` | NVML / fallback query | active GPU process/context marker | bounded per-process context flag |

## eBPF correlation metrics

The primary Go eBPF runtime no longer exports only raw event envelopes plus a few totals. It now keeps bounded local classifications so the controller and agent can reason about classes of activity without depending on unbounded labels.

| Metric | Purpose | Labels | Why it exists |
|---|---|---|---|
| `node_ebpf_category_events_total` | monotonic event totals by normalized category | `category` | preserves the dominant runtime shape without replaying every raw event |
| `node_ebpf_category_events_rate` | short-window activity rate by category | `category` | useful for burst detection and RCA trend comparisons |
| `node_ebpf_category_bytes_total` | bytes attributed to the category | `category` | lets network/file-heavy behavior stand out from mere event count |
| `node_ebpf_category_bytes_per_second` | byte throughput by category | `category` | easier to correlate with throughput anomalies |
| `node_ebpf_category_latency_seconds_avg` | average observed latency by category | `category` | carries syscall/IO delay shape into the controller |
| `node_ebpf_remote_scope_events_total` | classified remote-endpoint totals | `scope=loopback\|private\|public\|linklocal\|multicast\|unspecified` | keeps outbound behavior bounded and operationally meaningful |
| `node_ebpf_remote_scope_events_rate` | remote-endpoint rate | same as above | makes outbound bursts visible without raw IP cardinality |
| `node_ebpf_sensitive_path_events_total` | sensitive-path access totals | `scope=auth_db\|docker_sock\|ssh\|kubeconfig\|cron\|systemd\|kernel_posture\|kernel_modules\|tmp_exec...` | turns raw file paths into bounded policy-relevant classes |
| `node_ebpf_sensitive_path_events_rate` | sensitive-path access rate | same as above | highlights short-lived spikes |
| `node_ebpf_process_category_events_total` | top per-process category totals | `pid`,`process`,`category` | surfaces which process is dominating a class of behavior |
| `node_ebpf_process_category_events_rate` | top per-process category rate | `pid`,`process`,`category` | supports RCA narrowing without emitting every process/path tuple |

`node_ebpf_runtime_event` still exists for bounded recent-event envelopes. It now also includes `remote_scope` and `path_scope` labels when those classifications are available.
