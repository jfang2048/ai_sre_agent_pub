# Metrics Reference

This reference reflects the metric families currently produced by the code.

## Metric producers

- Collector (`sre-collector`): host/process/log/GPU/eBPF/transport metrics in telemetry batches.
- Controller (`sre-controller`): Prometheus aggregation and fleet-level re-export.

## Naming families

| Prefix/family | Producer | Notes |
|---|---|---|
| `node_*` | Collector | Host, process, GPU, kernel, eBPF families |
| `rca_*` | Collector | Deep per-process attribution signals (level 5 path) |
| `collector_*` | Collector | Spool, transport, shm stats |
| `libvirt_*` | Collector | Optional virtualization signals |
| `sre_controller_*`, `sre_node_up` | Controller `/metrics` | Controller health and node health |

## Core host metrics (collector)

### CPU and load

- `node_cpu_usage_percent`
- `node_cpu_seconds_total{mode}`
- `node_load1`, `node_load5`, `node_load15`
- `node_context_switches_total`
- `node_interrupts_total`, `node_softirqs_total`

### Memory

- `node_memory_Used_bytes`
- `node_vmstat_pswpin`, `node_vmstat_pswpout`
- `node_vmstat_pgfault`, `node_vmstat_pgmajfault`, `node_vmstat_oom_kill`

### Disk and filesystem pressure

- `node_disk_read_bytes_total`, `node_disk_written_bytes_total`
- `node_disk_read_bytes_per_second`, `node_disk_written_bytes_per_second`
- `node_disk_reads_completed_total`, `node_disk_writes_completed_total`
- `node_disk_io_now`, `node_disk_io_time_seconds_total`

### Network

- `node_network_receive_bytes_total`, `node_network_transmit_bytes_total`
- `node_network_receive_bytes_per_second`, `node_network_transmit_bytes_per_second`
- `node_network_receive_packets_total`, `node_network_transmit_packets_total`
- `node_network_receive_errs_total`, `node_network_transmit_errs_total`
- `node_network_receive_drop_total`, `node_network_transmit_drop_total`

### Process sampler

- `node_process_cpu_seconds_total`
- `node_process_memory_rss_bytes`, `node_process_memory_vms_bytes`
- `node_process_threads`, `node_process_fds`

## GPU metrics (collector)

Collector emits GPU metrics when `nvidia-smi` is available.

Representative metrics:

- Inventory: `node_gpu_info`, `node_gpu_count`
- Utilization/memory: `node_gpu_utilization_sm_avg_percent`, `node_gpu_memory_used_total_mib`, `node_gpu_memory_total_all_mib`, `node_gpu_memory_used_percent`
- Thermals/power: `node_gpu_temperature_max_celsius`, `node_gpu_power_draw_total_watts`, `node_gpu_power_limit_total_watts`, `node_gpu_power_draw_percent`
- Health: `node_gpu_xid_errors_total`, `node_gpu_throttle_active_any`, `node_gpu_mig_enabled`
- Per-process: `node_gpu_process_total`, `node_gpu_process_count`

## Deep RCA metrics (collector level 5)

Representative families:

- CPU attribution: `rca_cpu_process_*`
- Memory attribution: `rca_memory_process_*`, `rca_memory_region_rss_bytes`
- Disk attribution: `rca_io_process_*`, `rca_io_device_*`
- Network attribution: `rca_net_process_*`, `rca_net_connection_queue_bytes`, `rca_net_interface_*`
- Collection health: `rca_collection_duration_seconds`, `rca_metrics_collected`

## eBPF metrics (optional)

When eBPF reader is enabled (or level 5 auto-path):

- `node_ebpf_events_total`
- `node_ebpf_events_rate`
- `node_ebpf_events_bytes_total`, `node_ebpf_events_bytes_rate`
- `node_ebpf_events_latency_seconds_sum`, `_count`, `_avg`
- `node_ebpf_process_events_total`
- `node_ebpf_gpu_events_total`, `node_ebpf_gpu_bytes_total`, `node_ebpf_gpu_latency_seconds_avg`

## Collector runtime metrics

- `collector_spool_backlog_bytes`
- `collector_spool_size_bytes`
- `collector_transport_send_ms`
- `collector_transport_ack_ms`
- `collector_transport_errors_total`
- `collector_transport_compressed`
- `collector_shm_metrics_read`, `collector_shm_read_errors`, `collector_shm_buffer_capacity_bytes`

## Controller `/metrics`

Controller exposes:

- `sre_controller_nodes_total`
- `sre_controller_nodes_healthy`
- `sre_node_up{node,address}`
- Sanitized pass-through metrics from ingested collector payloads
- GPU re-export subset:
  - `node_gpu_utilization_sm_percent`
  - `node_gpu_memory_used_mib`
  - `node_gpu_memory_total_mib`

## Process ranking signal map (`/api/v1/top/programs`)

Resource categories:

- `cpu`
- `memory`
- `disk`
- `disk_io`
- `network`
- `gpu`
- `logs`

Category semantics:

- `disk`: cumulative storage footprint/activity totals.
- `disk_io`: live throughput and syscall/event pressure.

Typical kernel-level signals used:

- `cpu`: `rca_cpu_process_percent`, `rca_cpu_process_user_percent`, `rca_cpu_process_system_percent`, `rca_cpu_process_wchan`, `rca_cpu_process_syscall`, `node_ebpf_process_events_total`
- `memory`: `rca_memory_process_rss_bytes`, `rca_memory_process_pss_bytes`, `rca_memory_process_swap_bytes`, `rca_memory_process_majflt_total`, `rca_memory_process_oom_score`
- `disk`: `rca_io_process_read_bytes_total`, `rca_io_process_write_bytes_total`, `rca_io_process_file_fd`
- `disk_io`: `rca_io_process_read_bytes_per_second`, `rca_io_process_write_bytes_per_second`, `rca_io_process_read_syscalls_total`, `rca_io_process_write_syscalls_total`
- `network`: `rca_net_process_connections`, `rca_net_process_queued_bytes`, `rca_net_connection_queue_bytes`
- `gpu`: `node_gpu_process_memory_mib`, `node_gpu_process_sm_util_percent`, `node_gpu_process_mem_util_percent`
- `logs`: `log_errors`, `log_warnings` (derived from ingested log fingerprints)

Per-process output fields include current values, cumulative totals, and frequency counters:

- `signal_values`
- `signal_totals`
- `signal_frequency`
- `category_totals`
- `category_frequency`

## Log fingerprint payloads

Log ingestion uses fingerprint records (not raw full log streams):

- `fingerprint`
- `count`
- `example`
- `timestamp_unix_nano`

Controller derives `log_errors` and `log_warnings` per process/service when parsing heuristics can map log lines.
