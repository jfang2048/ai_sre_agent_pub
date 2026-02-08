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
- `node_disk_io_now`, `node_disk_io_time_seconds_total`

### Network / 网络

- `node_network_receive_bytes_total`, `node_network_transmit_bytes_total`
- `node_network_receive_bytes_per_second`, `node_network_transmit_bytes_per_second`
- `node_network_receive_packets_total`, `node_network_transmit_packets_total`
- `node_network_receive_errs_total`, `node_network_transmit_errs_total`
- `node_network_receive_drop_total`, `node_network_transmit_drop_total`

### Process sampler / 进程采样器

- `node_process_cpu_seconds_total`
- `node_process_memory_rss_bytes`, `node_process_memory_vms_bytes`
- `node_process_threads`, `node_process_fds`

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
- **`disk`**: `rca_io_process_read_bytes_total`, `rca_io_process_write_bytes_total`, `rca_io_process_file_fd`
- **`disk_io`**: `rca_io_process_read_bytes_per_second`, `rca_io_process_write_bytes_per_second`, `rca_io_process_read_syscalls_total`, `rca_io_process_write_syscalls_total`
- **`network`**: `rca_net_process_connections`, `rca_net_process_queued_bytes`, `rca_net_connection_queue_bytes`
- **`gpu`**: `node_gpu_process_memory_mib`, `node_gpu_process_sm_util_percent`, `node_gpu_process_mem_util_percent`
- **`logs`**: `log_errors`, `log_warnings` (derived from ingested log fingerprints / 从采集的日志指纹派生)

Per-process output fields include current values, cumulative totals, and frequency counters / 每进程输出字段包括当前值、累计总量和频率计数器：

- `signal_values`
- `signal_totals`
- `signal_frequency`
- `category_totals`
- `category_frequency`

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
- `disk_read_bytes_per_second`
- `disk_write_bytes_per_second`
- `procs_running`
- `procs_blocked`
- `fd_usage_percent`
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
