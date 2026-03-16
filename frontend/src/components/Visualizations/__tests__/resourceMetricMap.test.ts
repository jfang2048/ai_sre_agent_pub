import { describe, expect, it } from 'vitest';
import {
    categoryForDataPathResource,
    defaultMetricForDataPathResource,
    metricForDataPathPressureSignal,
    normalizeMetricKeyForTrends,
    resourceCategoryForMetricKey,
} from '../resourceMetricMap';

describe('resourceMetricMap', () => {
    it('normalizes raw node metric keys into trend keys', () => {
        expect(normalizeMetricKeyForTrends('node_disk_request_latency_p99_seconds')).toBe('disk_request_latency_p99_ms');
        expect(normalizeMetricKeyForTrends('node_nvme_utilization_peak_percent')).toBe('disk_utilization_peak_percent');
        expect(normalizeMetricKeyForTrends('node_pressure_io_full_avg10')).toBe('io_pressure_full_avg10');
        expect(normalizeMetricKeyForTrends('node_cpu_iowait_percent')).toBe('cpu_iowait_percent');
        expect(normalizeMetricKeyForTrends('node_pressure_cpu_some_avg10')).toBe('cpu_pressure_some_avg10');
    });

    it('uses heuristics for unknown but recognizable metric names', () => {
        expect(normalizeMetricKeyForTrends('latency_p90')).toBe('disk_request_latency_p90_ms');
        expect(normalizeMetricKeyForTrends('rdma_congestion_events_total')).toBe('network_rx_bytes_per_second');
        expect(normalizeMetricKeyForTrends('disk_queue_saturation')).toBe('disk_total_iops_per_second');
    });

    it('maps metrics to resource categories', () => {
        expect(resourceCategoryForMetricKey('network_rx_bytes_per_second')).toBe('network');
        expect(resourceCategoryForMetricKey('filesystem_space_pressure_percent')).toBe('disk');
        expect(resourceCategoryForMetricKey('cpu_iowait_percent')).toBe('cpu');
        expect(resourceCategoryForMetricKey('cpu_pressure_some_avg10')).toBe('cpu');
        expect(resourceCategoryForMetricKey('totally_unknown_metric')).toBe('cpu');
    });

    it('returns defaults for data-path resources', () => {
        expect(defaultMetricForDataPathResource('network')).toBe('network_rx_bytes_per_second');
        expect(defaultMetricForDataPathResource('storage')).toBe('disk_total_iops_per_second');
        expect(defaultMetricForDataPathResource('probe_core')).toBe('probe_core_last_frame_age_ms');
        expect(defaultMetricForDataPathResource('compute')).toBe('cpu_usage_percent');
    });

    it('maps pressure signals to trend metrics', () => {
        expect(metricForDataPathPressureSignal('network', 'tcp_retransmit_ratio')).toBe('network_tx_bytes_per_second');
        expect(metricForDataPathPressureSignal('storage', 'latency_p99_ms')).toBe('disk_request_latency_p99_ms');
        expect(metricForDataPathPressureSignal('storage', 'node_filesystem_space_pressure_percent')).toBe('filesystem_space_pressure_percent');
        expect(metricForDataPathPressureSignal('probe_core', 'last_frame_age_seconds')).toBe('probe_core_last_frame_age_ms');
        expect(metricForDataPathPressureSignal('probe_core', 'selection_valid')).toBe('probe_core_selection_valid');
    });

    it('derives category for data-path resource and metric', () => {
        expect(categoryForDataPathResource('network', 'network_tx_bytes_per_second')).toBe('network');
        expect(categoryForDataPathResource('storage', 'filesystem_space_pressure_percent')).toBe('disk');
        expect(categoryForDataPathResource('storage', 'disk_total_iops_per_second')).toBe('disk_io');
        expect(categoryForDataPathResource('probe_core', 'probe_core_active')).toBe('network');
        expect(categoryForDataPathResource('compute')).toBe('cpu');
    });
});
