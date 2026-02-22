# Metrics Reference / 指标参考

This reference reflects the metric families currently produced by the code.

本参考反映了当前代码产生的指标系列。

## Metric producers / 指标生产者

- **Collector (`sre-collector`)**: host/process/log/GPU/eBPF/transport metrics in telemetry batches / 遥测批次中的 主机/进程/日志/GPU/eBPF/传输 指标
- **Controller (`sre-controller`)**: Prometheus aggregation and fleet-level re-export / Prometheus 聚合和集群级重导出

## Naming families / 命名系列

| Prefix/family | Producer | Notes / 说明 |
|---|---|---|
| `node_*` | Collector | Host, process, GPU, kernel, eBPF families / 主机、进程、GPU、内核、eBPF 系列 |
| `rca_*` | Collector | Deep per-process attribution signals (level 5 path) / 深度每进程归因信号（level 5 路径） |
| `probe_core_*` | Collector (via C++ probe-core) | Native low-level telemetry families from C++ probe-core IPC path / 来自 C++ probe-core IPC 路径的原生低层指标 |
| `collector_*` | Collector | Spool, transport, shm stats / 队列、传输、共享内存统计 |
| `libvirt_*` | Collector | Optional virtualization signals / 可选虚拟化信号 |
| `sre_controller_*`, `sre_node_up` | Controller `/metrics` | Controller health and node health / Controller 健康和节点健康 |

## Core host metrics (collector) / 核心主机指标（采集器）

### CPU and load / CPU 和负载

- `node_cpu_usage_percent`
- `node_cpu_seconds_total{mode}`
- `node_load1`, `node_load5`, `node_load15`
- `node_context_switches_total`
- `node_interrupts_total`, `node_softirqs_total`

### Memory / 内存

- `node_memory_Used_bytes`
- `node_vmstat_pswpin`, `node_vmstat_pswpout`
- `node_vmstat_pgfault`, `node_vmstat_pgmajfault`, `node_vmstat_oom_kill`

### Disk and filesystem pressure / 磁盘和文件系统压力

- `node_disk_read_bytes_total`, `node_disk_written_bytes_total`
- `node_disk_read_bytes_per_second`, `node_disk_written_bytes_per_second`
- `node_disk_reads_completed_total`, `node_disk_writes_completed_total`
- `node_disk_reads_per_second`, `node_disk_writes_per_second`, `node_disk_iops_per_second`
- `node_disk_queue_depth`, `node_disk_utilization_percent`
- `node_disk_queue_capacity_requests`, `node_disk_queue_depth_fill_percent`, `node_disk_io_inflight_fill_percent`
- `node_disk_avg_read_latency_seconds`, `node_disk_avg_write_latency_seconds`, `node_disk_avg_request_latency_seconds`
- `node_disk_request_latency_p50_seconds`, `node_disk_request_latency_p90_seconds`, `node_disk_request_latency_p99_seconds`
- `node_disk_request_latency_ops_bucket{le}`, `node_disk_request_latency_ops_total`
- `node_disk_partition_read_bytes_per_second`, `node_disk_partition_written_bytes_per_second`
- `node_disk_partition_reads_per_second`, `node_disk_partition_writes_per_second`, `node_disk_partition_iops_per_second`
- `node_filesystem_used_percent`, `node_filesystem_files_used_percent`
- `node_filesystem_space_pressure_percent`, `node_filesystem_inode_pressure_percent`
- `node_disk_io_now`, `node_disk_io_time_seconds_total`, `node_disk_weighted_io_time_seconds_total`
- `node_nvme_devices`, `node_nvme_total_read_bytes_per_second`, `node_nvme_total_written_bytes_per_second`
- `node_nvme_total_iops_per_second`, `node_nvme_queue_depth_total`, `node_nvme_utilization_peak_percent`, `node_nvme_avg_request_latency_seconds`

Optional distributed storage pipeline metrics / 可选分布式存储流水线指标：

- `node_storage_metadata_ops_per_second`
- `node_storage_metadata_latency_p99_seconds`
- `node_storage_small_io_ratio`
- `node_object_storage_get_latency_p99_seconds`
- `node_object_storage_put_latency_p99_seconds`
- `node_checkpoint_write_latency_p99_seconds`
- `node_dataloader_prefetch_stall_ratio`
- `node_cache_hit_ratio`

### Storage Source Map (lowest-level) / 存储指标最低层来源映射

| Metric family | Lowest-level source | Why this source |
|---|---|---|
| Per-disk bytes/ops/time (`node_disk_*`) | `/proc/diskstats` block-layer counters | Kernel-owned canonical device counters; low overhead read path; stable semantics across tools (`iostat`, node exporters). |
| Per-partition throughput/IOPS (`node_disk_partition_*`) | `/proc/diskstats` partition rows | Same block layer source as disks; preserves partition-level attribution without userspace sampling guesswork. |
| Queue depth (`node_disk_queue_depth`) | `/proc/diskstats` weighted I/O time delta / interval | Derived from kernel-maintained weighted queue time; reflects average concurrent queued requests. |
| Queue capacity/fill (`node_disk_queue_capacity_requests`, `node_disk_queue_depth_fill_percent`) | `/sys/block/<dev>/queue/nr_requests` + `/proc/diskstats` | Combines block queue capacity with observed queue depth to estimate queue pressure. |
| Device utilization (`node_disk_utilization_percent`) | `/proc/diskstats` `io_time` delta / interval | Direct busy-time accounting from block layer; minimal overhead and widely accepted utilization definition. |
| Latency averages (`node_disk_avg_*_latency_seconds`) | `/proc/diskstats` read/write ticks + completed I/O deltas | Computed from kernel cumulative latency and operation counters; no syscall tracing required. |
| Latency distribution quantiles (`node_disk_request_latency_p50/p90/p99_seconds`) | Weighted quantiles over per-device latency derived from `/proc/diskstats` deltas | Uses only kernel block counters and request completions; adds low-overhead distribution visibility without per-request tracing. |
| Latency bucketized ops (`node_disk_request_latency_ops_bucket`) | Bucketization of request-weighted per-device latencies from `/proc/diskstats` | Produces a coarse distribution curve suitable for trend UI while keeping overhead bounded to a single pass over device stats. |
| NVMe rollups (`node_nvme_*`) | Filtered aggregation over `/proc/diskstats` device rows with `nvme*` prefix | Preserves fast local-NVMe observability for AI data staging/checkpoint paths while keeping cardinality bounded. |
| Filesystem bytes/inodes (`node_filesystem_*`) | `/proc/self/mountinfo` + `statfs(2)` | `mountinfo` gives authoritative mount identity; `statfs` exposes kernel VFS space/inode accounting. |
| Filesystem pressure rollups (`node_filesystem_space_pressure_percent`, `node_filesystem_inode_pressure_percent`) | Aggregation of per-mount `statfs` metrics | Uses exact mount-level kernel values and keeps UI/reporting compact for hotspot detection. |
| I/O PSI stall (`node_pressure_io_*`) | `/proc/pressure/io` | Native kernel PSI signal for runnable/task stall under I/O pressure; best low-overhead saturation indicator. |
| Page cache & writeback bytes (`node_memory_Dirty_bytes`, `node_memory_Writeback_bytes`) | `/proc/meminfo` | Canonical kernel memory accounting for dirty/writeback cache pages converted to bytes. |
| Page-cache churn / writeback rates (`node_vmstat_pgpg*`, `node_vmstat_nr_dirtied_*`, `node_vmstat_nr_written_*`) | `/proc/vmstat` | Kernel VM subsystem counters with monotonic semantics, ideal for rate derivation. |
| Per-process storage attribution (`rca_io_process_*`) | `/proc/[pid]/io`, `/proc/[pid]/fd` | Lowest-cost kernel per-task IO counters plus FD-level context for ownership mapping. |

Storage trend/UI keys and concrete provenance / 存储趋势与 UI 键的具体来源：

| UI key (`/fleet/timeseries`) | Raw metric(s) | Lowest-level source |
|---|---|---|
| `disk_read_bytes_per_second` | `node_disk_total_read_bytes_per_second` (fallback `node_disk_read_bytes_per_second`) | `/proc/diskstats` |
| `disk_write_bytes_per_second` | `node_disk_total_written_bytes_per_second` (fallback `node_disk_written_bytes_per_second`) | `/proc/diskstats` |
| `disk_total_iops_per_second` | `node_disk_total_iops_per_second` | `/proc/diskstats` |
| `disk_utilization_peak_percent` | `node_disk_utilization_peak_percent` | `/proc/diskstats` (`io_time`) |
| `disk_queue_depth_total` | `node_disk_queue_depth_total` | `/proc/diskstats` (`weighted_io_time`) |
| `disk_avg_request_latency_ms` | `node_disk_avg_request_latency_seconds` | `/proc/diskstats` read/write ticks + completed ops |
| `disk_request_latency_p50_ms` | `node_disk_request_latency_p50_seconds` | `/proc/diskstats` read/write ticks + completed ops (request-weighted quantile) |
| `disk_request_latency_p90_ms` | `node_disk_request_latency_p90_seconds` | `/proc/diskstats` read/write ticks + completed ops (request-weighted quantile) |
| `disk_request_latency_p99_ms` | `node_disk_request_latency_p99_seconds` | `/proc/diskstats` read/write ticks + completed ops (request-weighted quantile) |
| `filesystem_space_pressure_percent` | `node_filesystem_space_pressure_percent` | `statfs(2)` over mounts from `/proc/self/mountinfo` |
| `filesystem_inode_pressure_percent` | `node_filesystem_inode_pressure_percent` | `statfs(2)` inode counters |
| `pagecache_dirty_bytes` | `node_memory_Dirty_bytes` | `/proc/meminfo` |
| `pagecache_writeback_bytes` | `node_memory_Writeback_bytes` | `/proc/meminfo` |
| `vm_pgpgin_per_second` | `node_vmstat_pgpgin_per_second` | `/proc/vmstat` |
| `vm_pgpgout_per_second` | `node_vmstat_pgpgout_per_second` | `/proc/vmstat` |
| `vm_dirtied_pages_per_second` | `node_vmstat_nr_dirtied_per_second` | `/proc/vmstat` |
| `vm_written_pages_per_second` | `node_vmstat_nr_written_per_second` | `/proc/vmstat` |
| `io_pressure_some_avg10` | `node_pressure_io_some_avg10` | `/proc/pressure/io` |
| `io_pressure_full_avg10` | `node_pressure_io_full_avg10` | `/proc/pressure/io` |

Data-path drilldown normalization (UI) / 数据路径钻取归一化（UI）：

When jumping from `/api/v1/diagnostics/data-path` to `Metric Trends`, the UI maps pressure/anomaly keys to trend-series keys so curve focus and process drilldown remain deterministic / 从数据路径诊断跳转到趋势页时，UI 会做键映射。

| Data-path key (examples) | Normalized trend key |
|---|---|
| `node_disk_request_latency_p99_seconds`, `latency_p99_ms` | `disk_request_latency_p99_ms` |
| `node_disk_queue_depth_total`, `queue_depth_total` | `disk_queue_depth_total` |
| `node_disk_utilization_peak_percent`, `node_nvme_utilization_peak_percent`, `utilization_peak_percent` (storage context) | `disk_utilization_peak_percent` |
| `node_nvme_avg_request_latency_seconds`, `avg_request_latency_seconds` | `disk_avg_request_latency_ms` |
| `node_filesystem_space_pressure_percent` | `filesystem_space_pressure_percent` |
| `node_pressure_io_full_avg10`, `io_pressure_full_avg10` | `io_pressure_full_avg10` |
| `node_tcp_retransmit_ratio`, `tcp_retransmit_ratio` | `network_tx_bytes_per_second` |
| `node_rdma_errors_per_second`, `node_softnet_dropped_per_second` | `network_rx_bytes_per_second` |
| `node_rdma_pfc_pause_frames_per_second`, `rdma_pfc_pause_per_second` | `network_rx_bytes_per_second` |
| `node_rdma_ecn_marked_ratio`, `rdma_ecn_marked_ratio` | `network_tx_bytes_per_second` |
| `node_storage_metadata_latency_p99_seconds`, `metadata_latency_p99_ms` | `disk_request_latency_p99_ms` |
| `node_object_storage_get_latency_p99_seconds`, `object_get_latency_p99_ms` | `disk_avg_request_latency_ms` |
| `node_checkpoint_write_latency_p99_seconds`, `checkpoint_write_latency_p99_ms` | `disk_request_latency_p99_ms` |
| `node_dataloader_prefetch_stall_ratio`, `dataloader_prefetch_stall_ratio` | `io_pressure_full_avg10` |

### Network / 网络

- `node_network_receive_bytes_total`, `node_network_transmit_bytes_total`
- `node_network_receive_bytes_per_second`, `node_network_transmit_bytes_per_second`
- `node_network_total_receive_bytes_per_second`, `node_network_total_transmit_bytes_per_second`
- `node_network_receive_packets_total`, `node_network_transmit_packets_total`
- `node_network_receive_packets_per_second`, `node_network_transmit_packets_per_second`
- `node_network_receive_errs_total`, `node_network_transmit_errs_total`
- `node_network_receive_errs_per_second`, `node_network_transmit_errs_per_second`
- `node_network_receive_drop_total`, `node_network_transmit_drop_total`
- `node_network_receive_drop_per_second`, `node_network_transmit_drop_per_second`
- `node_network_total_errs_per_second`, `node_network_total_drop_per_second`
- `node_network_interface_speed_bits_per_second`, `node_network_interface_utilization_percent`
- `node_network_utilization_peak_percent`, `node_network_capacity_utilization_percent`
- `node_network_interface_interrupts_total`, `node_network_interface_interrupts_per_second`, `node_network_interrupts_per_second`
- `node_softnet_dropped_per_second`, `node_softnet_times_squeezed_per_second`, `node_softnet_drop_ratio`
- `node_tcp_retransmits_per_second`, `node_tcp_retransmit_ratio`
- `node_network_interface_carrier_up`, `node_network_interface_tx_queue_len`, `node_network_interface_tx_queue_fill_percent`

### AI fabric (RDMA/Infiniband/RoCE) / AI 网络 Fabric（RDMA/Infiniband/RoCE）

- `node_rdma_ports`
- `node_rdma_port_state`, `node_rdma_port_phys_state`, `node_rdma_port_link_rate_gbps`
- `node_rdma_port_transmit_words_total`, `node_rdma_port_receive_words_total`
- `node_rdma_port_transmit_bytes_per_second`, `node_rdma_port_receive_bytes_per_second`
- `node_rdma_port_errors_per_second`, `node_rdma_errors_per_second`
- `node_rdma_port_congestion_events_per_second`, `node_rdma_congestion_events_per_second`
- `node_rdma_port_congestion_counter_total{counter=...}` (driver-exposed ECN/CNP/PFC-style counters when available / 当驱动暴露时的 ECN/CNP/PFC 类计数器)
- `node_rdma_pfc_pause_frames_per_second`, `node_rdma_ecn_marked_ratio` (if exported by NIC/driver counters / 如网卡/驱动计数器可提供)

### Network Source Map (lowest-level) / 网络指标最低层来源映射

| Metric family | Lowest-level source | Why this source |
|---|---|---|
| Per-interface bytes/packets/errors/drops (`node_network_*`) | `/proc/net/dev` | Kernel-maintained NIC counters with low collection overhead and stable semantics. |
| TCP retransmission/error ratio (`node_tcp_retransmit_*`) | `/proc/net/snmp` | Canonical TCP stack counters; enables rate + ratio without packet capture. |
| Softnet backlog/drop/squeeze (`node_softnet_*`) | `/proc/net/softnet_stat` | Captures kernel receive-path pressure and budget exhaustion directly. |
| NIC interrupts (`node_network_*interrupts*`) | `/proc/interrupts` + interface matching | Device-level interrupt pressure indicator without intrusive tracing. |
| Link speed/carrier/queues (`node_network_interface_*`) | `/sys/class/net/<iface>/*` | Device/driver-exposed interface state and queue tuning knobs. |
| RDMA transport + errors (`node_rdma_*`) | `/sys/class/infiniband/<dev>/ports/<port>/{state,rate,counters,hw_counters}` | Lowest available kernel/driver RDMA counters for RoCE/IB fabrics. |

### Process sampler / 进程采样器

- `node_process_cpu_seconds_total`
- `node_process_memory_rss_bytes`, `node_process_memory_vms_bytes`
- `node_process_threads`, `node_process_fds`

## C++ probe-core metrics (optional) / C++ probe-core 指标（可选）

When `probe_core.enabled=true`, collector can ingest framed protobuf batches from `sre-probe-core` and emit native `probe_core_*` series together with compatibility `node_*` aliases / 当启用 `probe_core.enabled=true` 时，collector 可以从 `sre-probe-core` 接收 protobuf 帧批次，并同时输出原生 `probe_core_*` 及兼容 `node_*` 别名。

Representative families / 代表性系列：

- `probe_core_cpu_*`, `probe_core_sched_*`
- `probe_core_memory_*`, `probe_core_vm_*`, `probe_core_pressure_*`
- `probe_core_disk_*` (device throughput, queue depth/capacity, await)
- `probe_core_network_*` (per-NIC rates, drops/errors, softnet, retransmits)
- `probe_core_rdma_*` (RDMA port state/rate/counters, congestion, PFC/ECN where available)
- `probe_core_netlink_*`, `probe_core_perf_*`, `probe_core_ebpf_*`
- `probe_core_gpu_*`, `probe_core_process_*`
- `probe_core_backpressure_*`, `probe_core_ipc_compression_enabled`
- `probe_core_collector_module_enabled{module=...}` (module selection visibility / 模块启用可见性)

Measurement notes / 采样说明：

- `probe_core_process_cpu_percent` is computed from `/proc/<pid>/stat` deltas normalized by host CPU jiffies delta in the same sample window.
- `probe_core_process_sched_run_seconds_total` / `probe_core_process_sched_wait_seconds_total` / `probe_core_process_sched_wait_ratio` come from `/proc/<pid>/schedstat` and are exported as RCA aliases `rca_cpu_process_sched_*`.
- `probe_core_process_block_io_delay_seconds_total` and `probe_core_process_block_io_delay_seconds_per_second` come from `/proc/<pid>/stat` field `delayacct_blkio_ticks` (exported as `rca_io_process_block_delay_*`).
- `probe_core_process_socket_connections` counts TCP/TCP6 socket inodes owned by the process (`/proc/<pid>/fd` + `/proc/net/{tcp,tcp6}`) and is exported as `rca_net_process_connections`.
- `probe_core_rdma_*` is collected from `/sys/class/infiniband/<dev>/ports/<port>/{state,rate,counters,hw_counters}`; counter families depend on NIC/driver exposure.

## GPU metrics (collector) / GPU 指标（采集器）

Collector emits GPU metrics when `nvidia-smi` is available / 当 `nvidia-smi` 可用时，采集器发送 GPU 指标。

Representative metrics / 代表性指标：

- **Inventory / 清单**: `node_gpu_info`, `node_gpu_count`
- **Utilization/memory / 利用率/内存**: `node_gpu_utilization_sm_avg_percent`, `node_gpu_memory_used_total_mib`, `node_gpu_memory_total_all_mib`, `node_gpu_memory_used_percent`
- **Thermals/power / 热度/功耗**: `node_gpu_temperature_max_celsius`, `node_gpu_power_draw_total_watts`, `node_gpu_power_limit_total_watts`, `node_gpu_power_draw_percent`
- **Health / 健康**: `node_gpu_xid_errors_total`, `node_gpu_throttle_active_any`, `node_gpu_mig_enabled`
- **Per-process / 每进程**: `node_gpu_process_total`, `node_gpu_process_count`

## Deep RCA metrics (collector level 5) / 深度 RCA 指标（采集器 level 5）

Representative families / 代表性系列：

- **CPU attribution / CPU 归因**: `rca_cpu_process_*`
- **Memory attribution / 内存归因**: `rca_memory_process_*`, `rca_memory_region_rss_bytes`
- **Disk attribution / 磁盘归因**: `rca_io_process_*`, `rca_io_device_*`
- **Network attribution / 网络归因**: `rca_net_process_*`, `rca_net_connection_queue_bytes`, `rca_net_interface_*`
- **Collection health / 采集健康**: `rca_collection_duration_seconds`, `rca_metrics_collected`

## eBPF metrics (optional) / eBPF 指标（可选）

When eBPF reader is enabled (or level 5 auto-path) / 当 eBPF 读取器启用时（或 level 5 自动路径）：

- `node_ebpf_events_total`
- `node_ebpf_events_rate`
- `node_ebpf_events_bytes_total`, `node_ebpf_events_bytes_rate`
- `node_ebpf_events_latency_seconds_sum`, `_count`, `_avg`
- `node_ebpf_process_events_total`
- `node_ebpf_gpu_events_total`, `node_ebpf_gpu_bytes_total`, `node_ebpf_gpu_latency_seconds_avg`

## Collector runtime metrics / 采集器运行时指标

- `collector_spool_backlog_bytes`
- `collector_spool_size_bytes`
- `collector_transport_send_ms`
- `collector_transport_ack_ms`
- `collector_transport_errors_total`
- `collector_transport_compressed`
- `collector_shm_metrics_read`, `collector_shm_read_errors`, `collector_shm_buffer_capacity_bytes`
- `collector_probe_source{source=go|probe_core}`
- `collector_probe_core_client_available`
- `collector_probe_core_active`
- `collector_probe_core_collector_selection_valid` (`0` means malformed/unsupported explicit `--collectors` selection)
- `collector_probe_core_collector_module_requested{module=...}`
- `collector_probe_core_collector_module_active{module=...}`
- `collector_probe_core_frames_received_total`
- `collector_probe_core_decode_errors_total`
- `collector_probe_core_crc_failures_total`
- `collector_probe_core_restarts_total`
- `collector_probe_core_last_sequence`
- `collector_probe_core_last_frame_age_seconds`
- `collector_probe_core_fresh`
- `collector_probe_core_last_error{error=...}`

## Controller `/metrics` / Controller 指标

Controller exposes / Controller 暴露：

- `sre_controller_nodes_total`
- `sre_controller_nodes_healthy`
- `sre_node_up{node,address}`
- Sanitized pass-through metrics from ingested collector payloads / 来自采集器负载的净化透传指标
- **GPU re-export subset / GPU 重导出子集**:
  - `node_gpu_utilization_sm_percent`
  - `node_gpu_memory_used_mib`
  - `node_gpu_memory_total_mib`
- **AGENT query/action counters / AGENT 查询与动作计数器**:
  - `agent_queries_total`
  - `agent_queries_success_total`
  - `agent_queries_failure_total`
  - `agent_queries_rate_limited_total`
  - `agent_queries_busy_rejected_total`
  - `agent_queries_stale_telemetry_total`
  - `agent_llm_calls_total`
  - `agent_llm_failures_total`
  - `agent_llm_bypassed_stale_total`
  - `agent_llm_bypassed_empty_total`
  - `agent_fallback_total`
  - `agent_actions_suppressed_total`
  - `agent_actions_executed_total`
  - `agent_actions_failure_total`
  - `agent_events_published_total`
  - `agent_events_publish_fail_total`
  - `agent_action_approval_required_total`
  - `agent_action_approval_rejected_total`
  - `agent_pending_actions_expired_total`
  - `agent_pending_actions_pruned_total`
  - `gpu_analysis_duration_seconds_total`
- **Orchestration counters / 编排计数器**:
  - `sre_orchestrator_reconciles_total`
  - `sre_orchestrator_scheduling_attempts_total`
  - `sre_orchestrator_scheduling_failures_total`
  - `sre_orchestrator_batch_deferrals_total`
  - `sre_orchestrator_self_heal_actions_total`
  - `sre_orchestrator_route_updates_total`
  - `sre_orchestrator_slo_violations_total`
  - `sre_orchestrator_slo_violations_active`
  - `sre_orchestrator_remediation_attempts_total`
  - `sre_orchestrator_remediation_actions_total`
  - `sre_orchestrator_remediation_blocked_total`
  - `sre_orchestrator_queue_depth`
  - `sre_orchestrator_running_workloads`
  - `sre_orchestrator_failed_workloads`
  - `sre_orchestrator_assignments_total`
- **Ingest quality counters / 接入质量计数器**:
  - `sre_ingest_batches_total`
  - `sre_ingest_rejected_total`
  - `sre_ingest_metrics_points_total`
  - `sre_ingest_process_samples_total`
  - `sre_ingest_log_fingerprints_total`
- **Probe inventory counters / 探针清单计数器**:
  - `sre_inventory_probes_total`
  - `sre_inventory_probes_healthy`
- **Kubernetes integration counters / Kubernetes 集成计数器**:
  - `sre_k8s_refresh_total`
  - `sre_k8s_refresh_failed_total`
  - `sre_k8s_clusters_configured`
  - `sre_k8s_clusters_healthy`
  - `sre_k8s_nodes_total`
  - `sre_k8s_workloads_total`

`agent_events_published_total` / `agent_events_publish_fail_total` aggregate delivery outcome across configured event sinks (generic webhook, Slack webhook, PagerDuty Events API) / 聚合已配置事件 sink（通用 webhook、Slack webhook、PagerDuty Events API）的投递结果。

## Process ranking signal map (`/api/v1/top/programs`) / 进程排名信号映射

Resource categories / 资源类别：

- `cpu`
- `memory`
- `disk`
- `disk_io`
- `network`
- `gpu`
- `logs`

Category semantics / 类别语义：

- `disk`: cumulative storage footprint/activity totals / 累计存储足迹/活动总量
- `disk_io`: live throughput and syscall/event pressure / 实时吞吐和 syscall/事件压力

Typical kernel-level signals used / 使用的典型内核级信号：

- **`cpu`**: `rca_cpu_process_percent`, `rca_cpu_process_user_percent`, `rca_cpu_process_system_percent`, `rca_cpu_process_wchan`, `rca_cpu_process_syscall`, `node_ebpf_process_events_total`
- **`memory`**: `rca_memory_process_rss_bytes`, `rca_memory_process_pss_bytes`, `rca_memory_process_swap_bytes`, `rca_memory_process_majflt_total`, `rca_memory_process_oom_score`
- **`disk`**: `rca_io_process_read_bytes_total`, `rca_io_process_write_bytes_total`, `rca_io_process_read_chars_total`, `rca_io_process_write_chars_total`, `rca_io_process_cancelled_write_bytes_total`, `rca_io_process_file_fd`
- **`disk_io`**: `rca_io_process_read_bytes_per_second`, `rca_io_process_write_bytes_per_second`, `rca_io_process_bytes_per_second`, `rca_io_process_read_syscalls_total`, `rca_io_process_write_syscalls_total`, `node_disk_utilization_percent`, `node_disk_queue_depth`, `node_disk_avg_request_latency_seconds`, `node_disk_request_latency_p99_seconds`, `node_nvme_total_iops_per_second`
- **`network`**: `rca_net_process_connections`, `rca_net_process_queued_bytes`, `rca_net_connection_queue_bytes`, `node_tcp_retransmit_ratio`, `node_softnet_dropped_per_second`, `node_rdma_errors_per_second`
- **`gpu`**: `node_gpu_process_memory_mib`, `node_gpu_process_sm_util_percent`, `node_gpu_process_mem_util_percent`
- **`logs`**: `log_errors`, `log_warnings` (derived from ingested log fingerprints / 从采集的日志指纹派生)

Per-process output fields include current values, cumulative totals, and frequency counters / 每进程输出字段包括当前值、累计总量和频率计数器：

- `signal_values`
- `signal_totals`
- `signal_frequency`
- `category_totals`
- `category_frequency`

Optional per-process workload-correlation fields (from probe metric labels) / 可选每进程工作负载关联字段（来自 probe 指标标签）：

- `workload_class`
- `job`
- `comm_pattern`
- `pod_uid`

For UI drill-down panels / 用于 UI 钻取面板：

- Use `resource_pages[category].ranked` from `/api/v1/top/programs` for sorted per-resource process lists / 使用来自 `/api/v1/top/programs` 的 `resource_pages[category].ranked` 获取排序的每资源进程列表
- Compute row share as `primary_value / sum(primary_value)` to display percentage contribution / 计算行占比为 `primary_value / sum(primary_value)` 以显示百分比贡献
- Use `collector_id` query filter to align process ranking with the collector selected in curve pages / 使用 `collector_id` 查询过滤器使进程排名与曲线页面中选择的 collector 对齐

### Chinese interpretation guide (process ranking) / 中文判读建议（进程排名）

- `signal_values` better for "current hotspots" (e.g. current CPU percentage) / `signal_values` 更适合看"当前热点"（例如当前 CPU 百分比）
- `signal_totals` better for "cumulative impact" (e.g. cumulative read/write volume) / `signal_totals` 更适合看"累计影响"（例如累计读写量）
- `category_frequency` helps identify "recurring issues" rather than single spikes / `category_frequency` 可以辅助识别"高频反复问题"而不是单次尖刺
- Recommended to preserve in troubleshooting records / 建议在排障记录中同时保留：
  - Current value (value) / 当前值
  - Share percentage / 占比
  - collector / hostname / pid

## Log fingerprint payloads / 日志指纹负载

Log ingestion uses fingerprint records (not raw full log streams) / 日志接入使用指纹记录（而非原始完整日志流）：

- `fingerprint`
- `count`
- `example`
- `timestamp_unix_nano`

Controller derives `log_errors` and `log_warnings` per process/service when parsing heuristics can map log lines / 当解析启发式可以映射日志行时，Controller 派生每个进程/服务的 `log_errors` 和 `log_warnings`。

## UI trend curve keys (`/api/v1/fleet/timeseries`) / UI 趋势曲线键

The trend API exposes normalized curve keys used by the web UI / 趋势 API 暴露 Web UI 使用的标准化曲线键：

- `cpu_usage_percent`
- `memory_used_percent`
- `load1`
- `network_rx_bytes_per_second`
- `network_tx_bytes_per_second`
- `network_utilization_peak_percent`
- `network_capacity_utilization_percent`
- `tcp_retransmits_per_second`
- `tcp_retransmit_ratio`
- `softnet_dropped_per_second`
- `rdma_errors_per_second`
- `rdma_congestion_events_per_second`
- `disk_read_bytes_per_second`
- `disk_write_bytes_per_second`
- `disk_total_iops_per_second`
- `disk_utilization_peak_percent`
- `disk_queue_depth_total`
- `disk_avg_request_latency_ms`
- `disk_request_latency_p50_ms`
- `disk_request_latency_p90_ms`
- `disk_request_latency_p99_ms`
- `nvme_total_iops_per_second`
- `nvme_queue_depth_total`
- `nvme_utilization_peak_percent`
- `nvme_avg_request_latency_ms`
- `filesystem_space_pressure_percent`
- `filesystem_inode_pressure_percent`
- `pagecache_dirty_bytes`
- `pagecache_writeback_bytes`
- `vm_pgpgin_per_second`
- `vm_pgpgout_per_second`
- `vm_dirtied_pages_per_second`
- `vm_written_pages_per_second`
- `io_pressure_some_avg10`
- `io_pressure_full_avg10`
- `procs_running`
- `procs_blocked`
- `fd_usage_percent`
- `numa_locality_ratio_percent`
- `gpu_utilization_percent` (when GPU metrics exist / 当 GPU 指标存在时)
- `gpu_memory_used_mib` (when GPU metrics exist / 当 GPU 指标存在时)

Each key maps to raw collector metrics and is returned with / 每个键映射到原始采集器指标并返回：

- exact latest numeric value (`numeric_summary`) / 精确的 最新数值
- historical points for curve rendering (`series[].points`) / 用于曲线渲染的历史点
- anomaly hint markers (`is_anomaly`, `z_score`) / 异常提示标记

### Chinese interpretation guide (curves + values + text) / 中文判读建议（曲线 + 数值 + 文本）

1. First check `numeric_summary` to determine if thresholds are triggered / 先看 `numeric_summary` 判断是否触发阈值
2. Then check `series[].points` to determine anomaly pattern (spike/sustained rise/oscillation) / 再看 `series[].points` 判断异常形态（突刺/持续抬升/震荡）
3. Finally use `/top/programs?collector_id=...` to identify responsible processes and confirm share / 最后用 `/top/programs?collector_id=...` 找责任进程并确认占比
