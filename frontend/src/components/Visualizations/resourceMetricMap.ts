import type { ResourceCategory } from './ResourceProcessBreakdownPanel';

type DataPathResourceKind = 'network' | 'storage' | 'probe_core' | 'compute';

const TRENDS_METRIC_CATEGORY: Record<string, ResourceCategory> = {
    cpu_usage_percent: 'cpu',
    cpu_iowait_percent: 'cpu',
    cpu_pressure_some_avg10: 'cpu',
    load1: 'cpu',
    fd_usage_percent: 'cpu',
    procs_running: 'cpu',
    procs_blocked: 'cpu',
    memory_used_percent: 'memory',
    network_rx_bytes_per_second: 'network',
    network_tx_bytes_per_second: 'network',
    disk_read_bytes_per_second: 'disk_io',
    disk_write_bytes_per_second: 'disk_io',
    disk_total_iops_per_second: 'disk_io',
    disk_utilization_peak_percent: 'disk_io',
    disk_queue_depth_total: 'disk_io',
    disk_avg_request_latency_ms: 'disk_io',
    disk_request_latency_p50_ms: 'disk_io',
    disk_request_latency_p90_ms: 'disk_io',
    disk_request_latency_p99_ms: 'disk_io',
    io_pressure_some_avg10: 'disk_io',
    io_pressure_full_avg10: 'disk_io',
    filesystem_space_pressure_percent: 'disk',
    filesystem_inode_pressure_percent: 'disk',
    pagecache_dirty_bytes: 'disk',
    pagecache_writeback_bytes: 'disk',
    vm_pgpgin_per_second: 'disk',
    vm_pgpgout_per_second: 'disk',
    vm_dirtied_pages_per_second: 'disk',
    vm_written_pages_per_second: 'disk',
    gpu_utilization_percent: 'gpu',
    gpu_memory_used_mib: 'gpu',
    probe_core_client_available: 'network',
    probe_core_active: 'network',
    probe_core_fresh: 'network',
    probe_core_selection_valid: 'network',
    probe_core_last_frame_age_ms: 'network',
    probe_core_decode_errors_total: 'network',
    probe_core_crc_failures_total: 'network',
    probe_core_restarts_total: 'network',
};

const METRIC_ALIASES: Record<string, string> = {
    node_network_utilization_peak_percent: 'network_rx_bytes_per_second',
    node_network_capacity_utilization_percent: 'network_rx_bytes_per_second',
    node_tcp_retransmits_per_second: 'network_tx_bytes_per_second',
    node_tcp_retransmit_ratio: 'network_tx_bytes_per_second',
    node_softnet_dropped_per_second: 'network_rx_bytes_per_second',
    node_softnet_times_squeezed_per_second: 'network_rx_bytes_per_second',
    node_network_total_drop_per_second: 'network_rx_bytes_per_second',
    node_network_total_errs_per_second: 'network_rx_bytes_per_second',
    node_network_interface_tx_queue_fill_percent: 'network_tx_bytes_per_second',
    node_rdma_errors_per_second: 'network_rx_bytes_per_second',
    node_rdma_congestion_events_per_second: 'network_rx_bytes_per_second',
    node_rdma_port_errors_per_second: 'network_rx_bytes_per_second',
    node_rdma_port_congestion_events_per_second: 'network_rx_bytes_per_second',
    node_rdma_pfc_pause_frames_per_second: 'network_rx_bytes_per_second',
    node_rdma_ecn_marked_ratio: 'network_tx_bytes_per_second',
    node_network_interrupts_per_second: 'network_rx_bytes_per_second',
    node_cpu_iowait_percent: 'cpu_iowait_percent',
    node_pressure_cpu_some_avg10: 'cpu_pressure_some_avg10',
    node_disk_utilization_peak_percent: 'disk_utilization_peak_percent',
    node_disk_queue_depth_total: 'disk_queue_depth_total',
    node_disk_request_latency_p99_seconds: 'disk_request_latency_p99_ms',
    node_pressure_io_full_avg10: 'io_pressure_full_avg10',
    node_filesystem_space_pressure_percent: 'filesystem_space_pressure_percent',
    node_nvme_utilization_peak_percent: 'disk_utilization_peak_percent',
    node_nvme_avg_request_latency_seconds: 'disk_avg_request_latency_ms',
    node_storage_metadata_latency_p99_seconds: 'disk_request_latency_p99_ms',
    node_storage_small_io_ratio: 'disk_total_iops_per_second',
    node_object_storage_get_latency_p99_seconds: 'disk_avg_request_latency_ms',
    node_object_storage_put_latency_p99_seconds: 'disk_avg_request_latency_ms',
    node_checkpoint_write_latency_p99_seconds: 'disk_request_latency_p99_ms',
    node_dataloader_prefetch_stall_ratio: 'io_pressure_full_avg10',
    node_cache_hit_ratio: 'disk_read_bytes_per_second',
    collector_probe_core_client_available: 'probe_core_client_available',
    collector_probe_core_active: 'probe_core_active',
    collector_probe_core_fresh: 'probe_core_fresh',
    collector_probe_core_collector_selection_valid: 'probe_core_selection_valid',
    collector_probe_core_last_frame_age_seconds: 'probe_core_last_frame_age_ms',
    collector_probe_core_decode_errors_total: 'probe_core_decode_errors_total',
    collector_probe_core_crc_failures_total: 'probe_core_crc_failures_total',
    collector_probe_core_restarts_total: 'probe_core_restarts_total',
    rca_net_process_queued_bytes: 'network_tx_bytes_per_second',
    rca_net_process_connections: 'network_rx_bytes_per_second',
    rca_net_connection_queue_bytes: 'network_tx_bytes_per_second',
    rca_cpu_process_sched_wait_ratio: 'cpu_pressure_some_avg10',
    rca_cpu_process_sched_wait_seconds_total: 'cpu_pressure_some_avg10',
    rca_cpu_process_sched_run_seconds_total: 'procs_running',
    tcp_retransmit_ratio: 'network_tx_bytes_per_second',
    softnet_dropped_per_second: 'network_rx_bytes_per_second',
    tx_queue_fill_percent: 'network_tx_bytes_per_second',
    rdma_congestion_per_second: 'network_rx_bytes_per_second',
    rdma_pfc_pause_per_second: 'network_rx_bytes_per_second',
    rdma_ecn_marked_ratio: 'network_tx_bytes_per_second',
    rdma_comm_imbalance_ratio: 'network_rx_bytes_per_second',
};

const NETWORK_SIGNAL_TO_TRENDS_METRIC: Record<string, string> = {
    utilization_peak_percent: 'network_rx_bytes_per_second',
    tcp_retransmit_ratio: 'network_tx_bytes_per_second',
    tcp_retransmits_per_second: 'network_tx_bytes_per_second',
    softnet_dropped_per_second: 'network_rx_bytes_per_second',
    softnet_squeezed_per_second: 'network_rx_bytes_per_second',
    total_drop_per_second: 'network_rx_bytes_per_second',
    total_errs_per_second: 'network_rx_bytes_per_second',
    tx_queue_fill_percent: 'network_tx_bytes_per_second',
    rdma_errors_per_second: 'network_rx_bytes_per_second',
    rdma_congestion_per_second: 'network_rx_bytes_per_second',
    rdma_pfc_pause_per_second: 'network_rx_bytes_per_second',
    rdma_ecn_marked_ratio: 'network_tx_bytes_per_second',
    rdma_comm_imbalance_ratio: 'network_rx_bytes_per_second',
    network_interrupts_per_second: 'network_rx_bytes_per_second',
};

const STORAGE_SIGNAL_TO_TRENDS_METRIC: Record<string, string> = {
    utilization_peak_percent: 'disk_utilization_peak_percent',
    queue_depth_total: 'disk_queue_depth_total',
    latency_p99_ms: 'disk_request_latency_p99_ms',
    io_pressure_full_avg10: 'io_pressure_full_avg10',
    filesystem_space_pressure: 'filesystem_space_pressure_percent',
    filesystem_space_pressure_percent: 'filesystem_space_pressure_percent',
    filesystem_inode_pressure: 'filesystem_inode_pressure_percent',
    filesystem_inode_pressure_percent: 'filesystem_inode_pressure_percent',
    nvme_utilization_peak_percent: 'disk_utilization_peak_percent',
    nvme_latency_ms: 'disk_avg_request_latency_ms',
    nvme_avg_request_latency_ms: 'disk_avg_request_latency_ms',
    metadata_ops_per_second: 'disk_total_iops_per_second',
    metadata_latency_p99_ms: 'disk_request_latency_p99_ms',
    small_io_ratio: 'disk_total_iops_per_second',
    object_get_latency_p99_ms: 'disk_avg_request_latency_ms',
    object_put_latency_p99_ms: 'disk_avg_request_latency_ms',
    checkpoint_write_latency_p99_ms: 'disk_request_latency_p99_ms',
    dataloader_prefetch_stall_ratio: 'io_pressure_full_avg10',
    cache_hit_ratio: 'disk_read_bytes_per_second',
};

const PROBE_CORE_SIGNAL_TO_TRENDS_METRIC: Record<string, string> = {
    configured: 'probe_core_client_available',
    client_available: 'probe_core_client_available',
    active: 'probe_core_active',
    fresh: 'probe_core_fresh',
    selection_valid: 'probe_core_selection_valid',
    last_frame_age_seconds: 'probe_core_last_frame_age_ms',
    decode_errors_total: 'probe_core_decode_errors_total',
    crc_failures_total: 'probe_core_crc_failures_total',
    restarts_total: 'probe_core_restarts_total',
    requested_modules_count: 'probe_core_active',
    active_modules_count: 'probe_core_active',
    source_is_probe_core_primary: 'probe_core_active',
};

const CATEGORY_DEFAULT_METRIC: Record<ResourceCategory, string> = {
    cpu: 'cpu_usage_percent',
    memory: 'memory_used_percent',
    network: 'network_rx_bytes_per_second',
    disk_io: 'disk_total_iops_per_second',
    disk: 'filesystem_space_pressure_percent',
    gpu: 'gpu_utilization_percent',
    logs: 'procs_running',
};

export function normalizeMetricKeyForTrends(metricKey?: string): string | undefined {
    const raw = metricKey?.trim().toLowerCase();
    if (!raw) {
        return undefined;
    }
    if (raw in TRENDS_METRIC_CATEGORY) {
        return raw;
    }
    if (raw in METRIC_ALIASES) {
        return METRIC_ALIASES[raw];
    }

    const stripped = raw.startsWith('node_') ? raw.slice(5) : raw;
    if (stripped in TRENDS_METRIC_CATEGORY) {
        return stripped;
    }
    if (stripped in METRIC_ALIASES) {
        return METRIC_ALIASES[stripped];
    }

    if (raw.includes('latency_p99')) {
        return 'disk_request_latency_p99_ms';
    }
    if (raw.includes('latency_p90')) {
        return 'disk_request_latency_p90_ms';
    }
    if (raw.includes('latency_p50')) {
        return 'disk_request_latency_p50_ms';
    }
    if (raw.includes('avg_request_latency')) {
        return 'disk_avg_request_latency_ms';
    }
    if (raw.includes('filesystem_space_pressure')) {
        return 'filesystem_space_pressure_percent';
    }
    if (raw.includes('filesystem_inode_pressure')) {
        return 'filesystem_inode_pressure_percent';
    }
    if (raw.includes('io_full_avg10') || raw.includes('pressure_io_full_avg10')) {
        return 'io_pressure_full_avg10';
    }
    if (raw.includes('io_some_avg10') || raw.includes('pressure_io_some_avg10')) {
        return 'io_pressure_some_avg10';
    }

    if (raw.includes('network') || raw.includes('rdma') || raw.includes('retransmit') || raw.includes('softnet')) {
        return 'network_rx_bytes_per_second';
    }
    if (raw.includes('disk') || raw.includes('nvme')) {
        return 'disk_total_iops_per_second';
    }
    if (raw.includes('cpu')) {
        return 'cpu_usage_percent';
    }
    if (raw.includes('memory')) {
        return 'memory_used_percent';
    }

    return undefined;
}

export function resourceCategoryForMetricKey(metricKey?: string): ResourceCategory {
    const normalized = normalizeMetricKeyForTrends(metricKey);
    if (normalized && normalized in TRENDS_METRIC_CATEGORY) {
        return TRENDS_METRIC_CATEGORY[normalized];
    }
    return 'cpu';
}

export function defaultMetricForCategory(category: ResourceCategory): string {
    return CATEGORY_DEFAULT_METRIC[category] || CATEGORY_DEFAULT_METRIC.cpu;
}

export function defaultMetricForDataPathResource(resource: DataPathResourceKind): string {
    switch (resource) {
        case 'network':
            return CATEGORY_DEFAULT_METRIC.network;
        case 'storage':
            return CATEGORY_DEFAULT_METRIC.disk_io;
        case 'probe_core':
            return 'probe_core_last_frame_age_ms';
        default:
            return CATEGORY_DEFAULT_METRIC.cpu;
    }
}

export function categoryForDataPathResource(resource: DataPathResourceKind, metricKey?: string): ResourceCategory {
    if (resource === 'network') {
        return 'network';
    }
    if (resource === 'probe_core') {
        return 'network';
    }
    if (resource === 'compute') {
        return 'cpu';
    }
    return resourceCategoryForMetricKey(metricKey || defaultMetricForDataPathResource(resource));
}

export function metricForDataPathPressureSignal(resource: 'network' | 'storage' | 'probe_core', signalKey?: string): string {
    const normalizedSignal = signalKey?.trim().toLowerCase() || '';
    if (!normalizedSignal) {
        return defaultMetricForDataPathResource(resource);
    }

    if (resource === 'network') {
        const mapped = NETWORK_SIGNAL_TO_TRENDS_METRIC[normalizedSignal];
        if (mapped) {
            return mapped;
        }
    } else if (resource === 'storage') {
        const mapped = STORAGE_SIGNAL_TO_TRENDS_METRIC[normalizedSignal];
        if (mapped) {
            return mapped;
        }
    } else {
        const mapped = PROBE_CORE_SIGNAL_TO_TRENDS_METRIC[normalizedSignal];
        if (mapped) {
            return mapped;
        }
    }

    return normalizeMetricKeyForTrends(normalizedSignal) || defaultMetricForDataPathResource(resource);
}
