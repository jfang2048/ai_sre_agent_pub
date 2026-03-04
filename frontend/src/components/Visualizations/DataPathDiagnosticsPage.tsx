import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    AlertTriangle,
    ArrowUpDown,
    Boxes,
    Cpu,
    Database,
    Layers,
    Network,
    Radar,
    ServerCrash,
    ShieldAlert,
    Workflow,
} from 'lucide-react';
import { fetchFleetNodes } from '@/api/trends';
import {
    DataPathNodeModel,
    DataPathDiagnosticsResponse,
    fetchAIInfraStackDiagnostics,
    fetchKernelPathDiagnostics,
    fetchRCAPacketExport,
    AIInfraIncidentDrilldown,
    AIInfraLayerDomain,
    AIInfraLayerDiagnostics,
    KernelPathDiagnosticsResponse,
    fetchWorkloadPathDiagnostics,
    WorkloadPathDiagnosticsWorkload,
    ResourceAnomaly,
    ResourcePressureRow,
    fetchDataPathDiagnostics,
    fetchRootCauseDiagnostics,
    KernelPathNodeDiagnostics,
    RootCauseDiagnosticsFinding,
    RootCauseDiagnosticsResponse,
} from '@/api/dataPathDiagnostics';
import type { ProgramStats } from '@/api/topPrograms';
import { formatCount, formatMetricByUnit, formatPercent, formatRate } from './metricFormat';
import type { TrendsNavigationIntentInput } from './trendsIntent';
import { buildProcessFilterHint, formatProcessContext } from './processContext';
import ResourceProcessBreakdownPanel, { type ResourceCategory } from './ResourceProcessBreakdownPanel';
import {
    categoryForDataPathResource,
    defaultMetricForCategory,
    defaultMetricForDataPathResource,
    metricForDataPathPressureSignal,
    normalizeMetricKeyForTrends,
    resourceCategoryForMetricKey,
} from './resourceMetricMap';

function sortByHostname<T extends { hostname?: string; collector_id?: string }>(rows: T[]): T[] {
    return [...rows].sort((a, b) => {
        const left = (a.hostname || a.collector_id || '').toLowerCase();
        const right = (b.hostname || b.collector_id || '').toLowerCase();
        if (left !== right) {
            return left.localeCompare(right);
        }
        return (a.collector_id || '').localeCompare(b.collector_id || '');
    });
}

function severityClass(severity?: string): string {
    switch (severity) {
        case 'critical':
            return 'bg-rose-500/20 text-rose-300 border-rose-500/50';
        case 'degraded':
            return 'bg-amber-500/20 text-amber-300 border-amber-500/50';
        default:
            return 'bg-emerald-500/20 text-emerald-300 border-emerald-500/50';
    }
}

function pressureSignals(row: ResourcePressureRow): string {
    if (!row.signals) {
        return '—';
    }
    const top = Object.entries(row.signals)
        .filter(([, value]) => Number.isFinite(value) && value > 0)
        .sort((a, b) => b[1] - a[1])
        .slice(0, 3)
        .map(([name, value]) => `${name}=${value.toFixed(value >= 100 ? 0 : 2)}`);
    return top.length ? top.join(' · ') : '—';
}

function processHeadline(process: ProgramStats, mode: 'network' | 'storage'): string {
    if (mode === 'network') {
        return `${formatRate(process.net_bytes_per_second)} · ${formatCount(process.net_connections)} conns`;
    }
    const total = (process.disk_read_bps ?? 0) + (process.disk_write_bps ?? 0);
    return `${formatRate(total)} · read ${formatRate(process.disk_read_bps)} · write ${formatRate(process.disk_write_bps)}`;
}

function anomalyTitle(anomaly: ResourceAnomaly): string {
    return `${anomaly.resource.toUpperCase()} ${anomaly.metric}`;
}

function bottleneckIcon(bottleneck: DataPathNodeModel['bottleneck']) {
    switch (bottleneck) {
        case 'network':
            return Network;
        case 'storage':
            return Database;
        default:
            return Cpu;
    }
}

function dominantSignalMetric(row: ResourcePressureRow): string | undefined {
    if (!row.signals) {
        return undefined;
    }
    return Object.entries(row.signals)
        .filter(([, value]) => Number.isFinite(value) && value > 0)
        .sort((a, b) => b[1] - a[1])[0]?.[0];
}

function dataPathResourceKind(resource: string): 'network' | 'storage' | 'probe_core' {
    if (resource.toLowerCase() === 'network') {
        return 'network';
    }
    if (resource.toLowerCase() === 'probe_core') {
        return 'probe_core';
    }
    return 'storage';
}

function formatConfidence(value?: number): string {
    if (!Number.isFinite(value)) {
        return '—';
    }
    return `${(value as number * 100).toFixed(0)}%`;
}

function findingSignals(finding: RootCauseDiagnosticsFinding): string {
    const signals = finding.correlated_signals ?? [];
    if (signals.length === 0) {
        return '—';
    }
    return signals.slice(0, 4).join(' · ');
}

function formatKernelStage(stage?: string): string {
    if (!stage) {
        return '—';
    }
    return stage.replaceAll('_', ' ');
}

function kernelDomainHeadline(node: KernelPathNodeDiagnostics, domain: 'storage' | 'network'): string {
    const view = domain === 'storage' ? node.storage : node.network;
    return `${formatKernelStage(view.top_stage)} · score ${view.score.toFixed(2)} · ${view.severity}`;
}

function firstSignalMatch(signals: Record<string, number> | undefined, preferred: string[]): string | undefined {
    if (!signals) {
        return undefined;
    }
    for (const key of preferred) {
        const value = signals[key];
        if (Number.isFinite(value) && value > 0) {
            return key;
        }
    }
    return Object.entries(signals)
        .filter(([, value]) => Number.isFinite(value) && value > 0)
        .sort((a, b) => b[1] - a[1])[0]?.[0];
}

function workloadMetric(workload: WorkloadPathDiagnosticsWorkload): string {
    const networkSignal = firstSignalMatch(workload.signals, [
        'rdma_congestion_per_second',
        'tcp_retransmit_ratio',
        'softnet_dropped_per_second',
    ]);
    const storageSignal = firstSignalMatch(workload.signals, [
        'disk_latency_p99_ms',
        'checkpoint_write_latency_p99_ms',
        'io_pressure_full_avg10',
        'dataloader_prefetch_stall_ratio',
    ]);
    const computeSignal = firstSignalMatch(workload.signals, [
        'gpu_utilization_percent',
        'cpu_iowait_percent',
        'procs_blocked',
        'cpu_usage_percent',
    ]);

    if (workload.bottleneck === 'network') {
        return metricForDataPathPressureSignal('network', networkSignal);
    }
    if (workload.bottleneck === 'storage') {
        return metricForDataPathPressureSignal('storage', storageSignal);
    }
    return normalizeMetricKeyForTrends(computeSignal) || defaultMetricForDataPathResource('compute');
}

function workloadCategory(workload: WorkloadPathDiagnosticsWorkload, metricKey: string) {
    if (workload.bottleneck === 'network') {
        return categoryForDataPathResource('network', metricKey);
    }
    if (workload.bottleneck === 'storage') {
        return categoryForDataPathResource('storage', metricKey);
    }
    return resourceCategoryForMetricKey(metricKey);
}

function riskLabel(risk: string): string {
    return risk.replaceAll('_', ' ');
}

function aiInfraLayerIcon(layerId: string) {
    switch (layerId) {
        case 'compute_virtualization':
            return Cpu;
        case 'orchestration_runtime':
            return Boxes;
        case 'communication_fabric':
            return Network;
        case 'memory_hierarchy':
            return Database;
        case 'data_pipeline':
            return Workflow;
        case 'execution_optimization':
            return ArrowUpDown;
        case 'reliability_sre':
            return ShieldAlert;
        case 'serving_inference':
            return ServerCrash;
        default:
            return Layers;
    }
}

function aiInfraMeasurementStatusClass(status?: string): string {
    switch (status) {
        case 'measured':
            return 'bg-emerald-500/20 text-emerald-200 border-emerald-500/50';
        case 'partial':
            return 'bg-amber-500/20 text-amber-200 border-amber-500/50';
        default:
            return 'bg-rose-500/20 text-rose-200 border-rose-500/50';
    }
}

function aiInfraMeasurementMethodClass(method?: string): string {
    switch (method) {
        case 'direct':
            return 'bg-cyan-500/20 text-cyan-200 border-cyan-500/50';
        case 'derived':
            return 'bg-indigo-500/20 text-indigo-200 border-indigo-500/50';
        case 'proxy':
            return 'bg-amber-500/20 text-amber-200 border-amber-500/50';
        default:
            return 'bg-rose-500/20 text-rose-200 border-rose-500/50';
    }
}

function aiInfraLayerSignalsSummary(layer: AIInfraLayerDiagnostics, limit = 3): string {
    const entries = Object.entries(layer.signals ?? {})
        .filter(([, value]) => Number.isFinite(value) && value > 0)
        .sort((a, b) => b[1] - a[1])
        .slice(0, limit);
    if (entries.length === 0) {
        return '—';
    }
    return entries
        .map(([key, value]) => `${key}=${value.toFixed(value >= 100 ? 0 : 2)}`)
        .join(' · ');
}

function aiInfraLayerDomainsSummary(layer: AIInfraLayerDiagnostics, limit = 2): string {
    const domains = [...(layer.domains ?? [])]
        .sort((a, b) => b.score - a.score)
        .slice(0, limit);
    if (domains.length === 0) {
        return '—';
    }
    return domains.map((domain) => `${domain.title} ${domain.score.toFixed(1)}`).join(' · ');
}

function aiInfraLayerMethodSummary(layer: AIInfraLayerDiagnostics): string {
    const counts = {
        direct: 0,
        derived: 0,
        proxy: 0,
        missing: 0,
    };
    for (const measurement of (layer.measurements ?? [])) {
        const method = (measurement.method || '').trim().toLowerCase();
        if (method === 'direct') {
            counts.direct += 1;
        } else if (method === 'derived') {
            counts.derived += 1;
        } else if (method === 'proxy') {
            counts.proxy += 1;
        } else if (method === 'missing') {
            counts.missing += 1;
        }
    }
    return `direct ${counts.direct} · derived ${counts.derived} · proxy ${counts.proxy} · missing ${counts.missing}`;
}

function aiInfraFindLayerByID(layers: AIInfraLayerDiagnostics[] | undefined, id: string): AIInfraLayerDiagnostics | undefined {
    if (!layers) {
        return undefined;
    }
    return layers.find((layer) => layer.id === id);
}

function finiteSignalValue(value: number | undefined): number | undefined {
    if (typeof value !== 'number' || Number.isNaN(value) || !Number.isFinite(value)) {
        return undefined;
    }
    return value;
}

function formatDurationSeconds(value?: number): string {
    if (typeof value !== 'number' || Number.isNaN(value) || !Number.isFinite(value) || value < 0) {
        return '—';
    }
    if (value >= 3600) {
        return `${(value / 3600).toFixed(1)}h`;
    }
    if (value >= 60) {
        return `${(value / 60).toFixed(1)}m`;
    }
    return `${value.toFixed(0)}s`;
}

function aiInfraIncidentSignalSummary(drilldown: AIInfraIncidentDrilldown, limit = 3): string {
    const entries = (drilldown.contention ?? [])
        .filter((entry) => Number.isFinite(entry.value))
        .slice(0, limit)
        .map((entry) => `${entry.name}=${entry.value.toFixed(entry.value >= 100 ? 0 : 2)}`);
    return entries.length > 0 ? entries.join(' · ') : '—';
}

function aiInfraIncidentMetricKey(drilldown: AIInfraIncidentDrilldown): string {
    const preferred = [
        'rca_net_process_queued_bytes',
        'rca_net_process_connections',
        'rca_cpu_process_sched_wait_ratio',
        'tcp_retransmit_ratio',
        'rdma_congestion_per_second',
        'tx_queue_fill_percent',
        'disk_latency_p99_ms',
        'io_pressure_full_avg10',
        'cpu_iowait_percent',
    ];
    const observed = (drilldown.contention ?? []).map((entry) => (entry.name || '').trim()).filter(Boolean);
    for (const signal of [...preferred, ...observed]) {
        const metric = normalizeMetricKeyForTrends(signal);
        if (metric) {
            return metric;
        }
    }
    return defaultMetricForCategory(aiInfraLayerCategory('communication_fabric'));
}

function aiInfraIncidentCollectorHint(drilldown: AIInfraIncidentDrilldown): string | undefined {
    const fromContention = (drilldown.contention ?? [])
        .map((entry) => (entry.collector_id || '').trim())
        .find((value) => value.length > 0);
    if (fromContention) {
        return fromContention;
    }
    const fromPlacement = (drilldown.placements ?? [])
        .map((placement) => (placement.collector_id || '').trim())
        .find((value) => value.length > 0);
    return fromPlacement || undefined;
}

function aiInfraPlacementSignalsSummary(signals: Record<string, number> | undefined, limit = 2): string {
    if (!signals) {
        return '—';
    }
    const entries = Object.entries(signals)
        .filter(([, value]) => Number.isFinite(value) && value > 0)
        .sort((a, b) => b[1] - a[1])
        .slice(0, limit)
        .map(([key, value]) => `${key}=${value.toFixed(value >= 100 ? 0 : 2)}`);
    return entries.length > 0 ? entries.join(' · ') : '—';
}

function aiInfraPlacementMetricKey(signals: Record<string, number> | undefined): string {
    const signalKey = firstSignalMatch(signals, [
        'rdma_congestion_per_second',
        'tcp_retransmit_ratio',
        'softnet_dropped_per_second',
        'disk_latency_p99_ms',
        'io_pressure_full_avg10',
        'gpu_utilization_percent',
        'cpu_iowait_percent',
        'estimated_latency_ms',
        'reserved_network_mbps',
        'reserved_storage_iops',
    ]);
    return normalizeMetricKeyForTrends(signalKey) || defaultMetricForDataPathResource('compute');
}

function rootCauseFindingMetricKey(finding: RootCauseDiagnosticsFinding): string {
    const category = (finding.category || '').toLowerCase();
    const id = (finding.id || '').toLowerCase();
    const preferredSignals = (finding.correlated_signals ?? []).map((signal) => signal.trim()).filter(Boolean);

    if (category.includes('collective') || id.includes('collective')) {
        for (const signal of ['rca_net_process_queued_bytes', 'rca_net_process_connections', 'rca_cpu_process_sched_wait_ratio', ...preferredSignals]) {
            const metric = normalizeMetricKeyForTrends(signal);
            if (metric) {
                return metric;
            }
        }
        return defaultMetricForDataPathResource('network');
    }

    if (category.includes('network') || id.includes('network') || id.includes('communication')) {
        for (const signal of preferredSignals) {
            const metric = normalizeMetricKeyForTrends(signal);
            if (metric) {
                return metric;
            }
        }
        return defaultMetricForDataPathResource('network');
    }

    if (category.includes('storage') || id.includes('storage') || id.includes('checkpoint')) {
        for (const signal of preferredSignals) {
            const metric = normalizeMetricKeyForTrends(signal);
            if (metric) {
                return metric;
            }
        }
        return defaultMetricForDataPathResource('storage');
    }

    if (category.includes('probe') || category.includes('observability') || id.includes('probe_core')) {
        return defaultMetricForDataPathResource('probe_core');
    }

    for (const signal of preferredSignals) {
        const metric = normalizeMetricKeyForTrends(signal);
        if (metric) {
            return metric;
        }
    }
    return defaultMetricForDataPathResource('compute');
}

function rootCauseFindingCollectorHint(finding: RootCauseDiagnosticsFinding): string | undefined {
    const collector = (finding.affected_nodes ?? [])
        .map((node) => (node.collector_id || '').trim())
        .find((value) => value.length > 0);
    return collector || undefined;
}

function aiInfraLayerCategory(layerId: string): ResourceCategory {
    switch (layerId) {
        case 'compute_virtualization':
            return 'gpu';
        case 'orchestration_runtime':
            return 'cpu';
        case 'communication_fabric':
            return 'network';
        case 'memory_hierarchy':
            return 'memory';
        case 'data_pipeline':
            return 'disk_io';
        case 'execution_optimization':
            return 'cpu';
        case 'reliability_sre':
            return 'cpu';
        case 'serving_inference':
            return 'network';
        default:
            return 'cpu';
    }
}

function aiInfraDomainPreferredSignals(domainId: string): string[] {
    switch (domainId) {
        case 'device_topology_occupancy':
            return ['avg_gpu_util_percent', 'gpu_low_with_path_pressure_nodes', 'gpu_busy_nodes'];
        case 'gpu_sharing_and_slices':
            return ['gpu_slice_density', 'gpu_partition_assignments', 'gpu_time_slice_proxy_workloads'];
        case 'heterogeneous_accelerator_coverage':
            return ['tpu_npu_signals_present', 'gpu_util_nodes'];
        case 'scheduler_queue':
            return ['avg_queue_delay_seconds', 'queue_depth', 'deferred_workloads'];
        case 'placement_fairness':
            return ['tenant_fairness_index', 'tenant_top_share', 'pending_pods'];
        case 'incident_runtime_linkage':
            return ['incident_linked_findings', 'slo_violations_active', 'preemption_like_events'];
        case 'in_node_interconnect':
            return ['gpu_pcie_observed_devices', 'nvlink_signal_nodes', 'nvswitch_signal_nodes'];
        case 'inter_node_fabric':
            return ['rdma_congested_nodes', 'retransmit_hot_nodes', 'network_avg_score', 'tx_queue_signal_nodes'];
        case 'collective_runtime':
            return ['collective_sync_cost_proxy', 'communication_imbalance_workloads', 'collective_pattern_processes'];
        case 'hbm_to_host_dram':
            return ['hbm_pressure_devices', 'avg_host_memory_used_percent', 'high_dram_nodes'];
        case 'page_cache_writeback':
            return ['io_pressure_nodes', 'page_cache_pressure_nodes', 'swap_pressure_nodes'];
        case 'nvme_distributed_object_tiers':
            return ['tier_stall_nodes', 'nvme_metric_nodes', 'object_metric_nodes'];
        case 'sli_error_budget':
            return ['error_budget_burn_rate', 'error_budget_remaining', 'latency_compliance_sli'];
        case 'fault_tolerance_recovery':
            return ['failed_workloads', 'container_restarts_total', 'checkpoint_risk_nodes'];
        case 'incident_lifecycle_rca':
            return ['critical_findings', 'mttd_proxy_seconds', 'mttr_proxy_seconds', 'incident_timeline_events'];
        case 'queueing_tail_latency':
            return ['avg_route_latency_ms', 'avg_realtime_queue_delay_sec', 'realtime_queued_workloads'];
        case 'batching_model_placement':
            return ['batch_coverage_gap', 'model_placement_gap', 'batch_size_samples'];
        case 'kv_cache_pressure':
            return ['kv_cache_utilization_avg', 'kv_cache_pressure_nodes', 'kv_cache_signal_nodes'];
        default:
            return [];
    }
}

function aiInfraDomainMetricKey(layer: AIInfraLayerDiagnostics, domain: AIInfraLayerDomain): string {
    const preferred = aiInfraDomainPreferredSignals(domain.id);
    const signal = firstSignalMatch(domain.signals, preferred) || firstSignalMatch(layer.signals, preferred);
    const normalized = normalizeMetricKeyForTrends(signal);
    if (normalized) {
        return normalized;
    }
    return defaultMetricForCategory(aiInfraLayerCategory(layer.id));
}

function aiInfraLayerCollectorHint(layer: AIInfraLayerDiagnostics): string | undefined {
    const node = (layer.ranked_entities ?? []).find((entity) => entity.kind === 'node' && entity.id);
    const collector = (node?.id || '').trim();
    return collector || undefined;
}

function hasHttpStatus(error: unknown, status: number): boolean {
    if (typeof error !== 'object' || error === null) {
        return false;
    }
    const response = (error as { response?: { status?: number } }).response;
    return response?.status === status;
}

interface WorkloadFilters {
    cluster: string;
    namespace: string;
    service: string;
}

const DEFAULT_WORKLOAD_SORT_KEY: WorkloadSortKey = 'severity';
const DEFAULT_WORKLOAD_SORT_DIRECTION: SortDirection = 'desc';
const WORKLOAD_SORT_KEYS: WorkloadSortKey[] = ['severity', 'overall', 'coverage', 'network', 'storage'];
const WORKLOAD_SORT_DIRECTIONS: SortDirection[] = ['asc', 'desc'];

const WORKLOAD_FILTER_QUERY_KEYS = {
    cluster: 'workload_cluster',
    namespace: 'workload_namespace',
    service: 'workload_service',
} as const;

const WORKLOAD_SORT_QUERY_KEYS = {
    key: 'workload_sort_key',
    direction: 'workload_sort_direction',
} as const;

interface WorkloadUrlState {
    filters: WorkloadFilters;
    sortKey: WorkloadSortKey;
    sortDirection: SortDirection;
}

function asWorkloadSortKey(value: string | null): WorkloadSortKey | undefined {
    if (!value) {
        return undefined;
    }
    const normalized = value.trim();
    if (!normalized) {
        return undefined;
    }
    return WORKLOAD_SORT_KEYS.find((sortKey) => sortKey === normalized);
}

function asSortDirection(value: string | null): SortDirection | undefined {
    if (!value) {
        return undefined;
    }
    const normalized = value.trim();
    if (!normalized) {
        return undefined;
    }
    return WORKLOAD_SORT_DIRECTIONS.find((direction) => direction === normalized);
}

function parseWorkloadStateFromUrl(): WorkloadUrlState {
    if (typeof window === 'undefined') {
        return {
            filters: { cluster: '', namespace: '', service: '' },
            sortKey: DEFAULT_WORKLOAD_SORT_KEY,
            sortDirection: DEFAULT_WORKLOAD_SORT_DIRECTION,
        };
    }
    const params = new URLSearchParams(window.location.search);
    return {
        filters: {
            cluster: params.get(WORKLOAD_FILTER_QUERY_KEYS.cluster)?.trim() || '',
            namespace: params.get(WORKLOAD_FILTER_QUERY_KEYS.namespace)?.trim() || '',
            service: params.get(WORKLOAD_FILTER_QUERY_KEYS.service)?.trim() || '',
        },
        sortKey: asWorkloadSortKey(params.get(WORKLOAD_SORT_QUERY_KEYS.key)) || DEFAULT_WORKLOAD_SORT_KEY,
        sortDirection: asSortDirection(params.get(WORKLOAD_SORT_QUERY_KEYS.direction)) || DEFAULT_WORKLOAD_SORT_DIRECTION,
    };
}

function writeWorkloadStateToUrl(filters: WorkloadFilters, sortKey: WorkloadSortKey, sortDirection: SortDirection): void {
    if (typeof window === 'undefined') {
        return;
    }
    const url = new URL(window.location.href);
    const filterUpdates: Array<[string, string]> = [
        [WORKLOAD_FILTER_QUERY_KEYS.cluster, filters.cluster],
        [WORKLOAD_FILTER_QUERY_KEYS.namespace, filters.namespace],
        [WORKLOAD_FILTER_QUERY_KEYS.service, filters.service],
    ];
    for (const [key, value] of filterUpdates) {
        if (value) {
            url.searchParams.set(key, value);
        } else {
            url.searchParams.delete(key);
        }
    }
    if (sortKey === DEFAULT_WORKLOAD_SORT_KEY) {
        url.searchParams.delete(WORKLOAD_SORT_QUERY_KEYS.key);
    } else {
        url.searchParams.set(WORKLOAD_SORT_QUERY_KEYS.key, sortKey);
    }
    if (sortDirection === DEFAULT_WORKLOAD_SORT_DIRECTION) {
        url.searchParams.delete(WORKLOAD_SORT_QUERY_KEYS.direction);
    } else {
        url.searchParams.set(WORKLOAD_SORT_QUERY_KEYS.direction, sortDirection);
    }
    const nextQuery = url.searchParams.toString();
    const nextUrl = `${url.pathname}${nextQuery ? `?${nextQuery}` : ''}${url.hash}`;
    window.history.replaceState({}, '', nextUrl);
}

function workloadIdentity(workload: WorkloadPathDiagnosticsWorkload): string {
    return `${workload.cluster}/${workload.namespace}/${workload.kind}/${workload.name}`;
}

function compactSignalSummary(signals: Record<string, number> | undefined, limit = 4): string {
    if (!signals) {
        return '—';
    }
    const top = Object.entries(signals)
        .filter(([, value]) => Number.isFinite(value) && value > 0)
        .sort((a, b) => b[1] - a[1])
        .slice(0, limit)
        .map(([name, value]) => `${name}=${value.toFixed(value >= 100 ? 0 : 2)}`);
    return top.length > 0 ? top.join(' · ') : '—';
}

function topSignalKeys(signals: Record<string, number> | undefined, limit = 4): string[] {
    if (!signals) {
        return [];
    }
    return Object.entries(signals)
        .filter(([, value]) => Number.isFinite(value) && value > 0)
        .sort((a, b) => b[1] - a[1])
        .slice(0, limit)
        .map(([key]) => key);
}

function compactSignalSourceSummary(
    signals: Record<string, number> | undefined,
    sources: Record<string, string> | undefined,
    limit = 3,
): string {
    if (!signals || !sources) {
        return '—';
    }
    const lines = topSignalKeys(signals, limit)
        .map((key) => `${key}←${sources[key] || 'unknown source'}`);
    return lines.length > 0 ? lines.join(' · ') : '—';
}

type WorkloadSortKey = 'severity' | 'overall' | 'coverage' | 'network' | 'storage';
type SortDirection = 'asc' | 'desc';

const WORKLOAD_SORT_LABEL: Record<WorkloadSortKey, string> = {
    severity: 'severity',
    overall: 'overall score',
    coverage: 'telemetry coverage',
    network: 'network score',
    storage: 'storage score',
};

function workloadSeverityRank(severity: string): number {
    switch (severity) {
        case 'critical':
            return 4;
        case 'degraded':
            return 3;
        case 'healthy':
            return 2;
        case 'unknown':
            return 1;
        default:
            return 0;
    }
}

function workloadSortMetric(workload: WorkloadPathDiagnosticsWorkload, sortKey: WorkloadSortKey): number {
    switch (sortKey) {
        case 'severity':
            return workloadSeverityRank(workload.severity);
        case 'overall':
            return workload.overall_score;
        case 'coverage':
            return workload.telemetry_coverage_percent;
        case 'network':
            return workload.network_score;
        case 'storage':
            return workload.storage_score;
        default:
            return workload.overall_score;
    }
}

function buildWorkloadHandoffMarkdown(
    generatedAt: string | undefined,
    filters: WorkloadFilters,
    sortKey: WorkloadSortKey,
    sortDirection: SortDirection,
    workloads: WorkloadPathDiagnosticsWorkload[],
): string {
    const lines: string[] = [];
    lines.push('# Workload Path Handoff');
    lines.push(`Generated: ${generatedAt || new Date().toISOString()}`);
    lines.push(`Scope: cluster=${filters.cluster || '*'}, namespace=${filters.namespace || '*'}, service=${filters.service || '*'}`);
    lines.push(`Sort: ${WORKLOAD_SORT_LABEL[sortKey]} (${sortDirection})`);
    lines.push(`Workloads: ${workloads.length}`);
    lines.push('');
    if (workloads.length === 0) {
        lines.push('- No workloads in current scope.');
        return lines.join('\n');
    }
    lines.push('## Top Workloads');
    for (const workload of workloads.slice(0, 12)) {
        const topNode = workload.nodes?.[0];
        lines.push(`- ${workload.namespace}/${workload.name} [${workload.severity}] bottleneck=${workload.bottleneck} C/N/S/O=${workload.compute_score.toFixed(2)}/${workload.network_score.toFixed(2)}/${workload.storage_score.toFixed(2)}/${workload.overall_score.toFixed(2)} coverage=${workload.telemetry_coverage_percent.toFixed(0)}% risks=${(workload.risks ?? []).slice(0, 3).join(',') || 'none'}`);
        lines.push(`  - node=${topNode?.hostname || topNode?.node_name || '—'} stages=storage:${workload.top_storage_stage || '—'},network:${workload.top_network_stage || '—'}`);
        lines.push(`  - evidence=${compactSignalSummary(workload.signals, 3)}`);
    }
    return lines.join('\n');
}

function buildResourceRankingLines(
    title: string,
    rows: ResourcePressureRow[] | undefined,
    limit = 5,
): string[] {
    const lines: string[] = [];
    lines.push(`### ${title}`);
    if (!rows || rows.length === 0) {
        lines.push('- No ranked nodes in current window.');
        return lines;
    }
    for (const row of rows.slice(0, limit)) {
        lines.push(`- [${row.severity}] ${row.hostname || row.collector_id} score=${row.score.toFixed(2)} signals=${compactSignalSummary(row.signals, 3)}`);
    }
    return lines;
}

function sanitizeFileToken(value: string): string {
    const normalized = value.trim().toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '');
    return normalized || 'all';
}

function rootCausePacketFilename(generatedAt: string | undefined, filters: WorkloadFilters): string {
    const stamp = (generatedAt || new Date().toISOString())
        .replaceAll(':', '-')
        .replaceAll('.', '-');
    const scope = [
        sanitizeFileToken(filters.cluster || 'all-clusters'),
        sanitizeFileToken(filters.namespace || 'all-namespaces'),
        sanitizeFileToken(filters.service || 'all-services'),
    ].join('_');
    return `ai-sre-rca-packet_${scope}_${stamp}.md`;
}

function buildRootCausePacketMarkdown(
    generatedAt: string | undefined,
    diagnostics: DataPathDiagnosticsResponse | undefined,
    kernelPath: KernelPathDiagnosticsResponse | undefined,
    rootCause: RootCauseDiagnosticsResponse | undefined,
    filters: WorkloadFilters,
    sortKey: WorkloadSortKey,
    sortDirection: SortDirection,
    workloads: WorkloadPathDiagnosticsWorkload[],
    scopeLink?: string,
): string {
    const lines: string[] = [];
    lines.push('# AI SRE RCA Packet');
    lines.push(`Generated: ${generatedAt || new Date().toISOString()}`);
    lines.push(`Scope: cluster=${filters.cluster || '*'}, namespace=${filters.namespace || '*'}, service=${filters.service || '*'}`);
    lines.push(`Sort: ${WORKLOAD_SORT_LABEL[sortKey]} (${sortDirection})`);
    if (scopeLink) {
        lines.push(`Scope link: ${scopeLink}`);
    }
    lines.push('');
    lines.push('## Root Cause Summary');
    lines.push(`Findings: ${rootCause?.summary.finding_count ?? 0} (critical ${rootCause?.summary.critical_findings ?? 0}, degraded ${rootCause?.summary.degraded_findings ?? 0})`);
    lines.push(`Linked anomalies: ${rootCause?.data_path.total_anomalies ?? 0}`);
    if ((rootCause?.findings?.length ?? 0) === 0) {
        lines.push('- No active findings in current window.');
    } else {
        for (const finding of (rootCause?.findings ?? []).slice(0, 6)) {
            const nodes = (finding.affected_nodes ?? [])
                .map((node) => node.hostname || node.collector_id)
                .slice(0, 4)
                .join(', ');
            lines.push(`- [${finding.severity}] ${finding.title} (confidence ${formatConfidence(finding.confidence)})`);
            lines.push(`  - hypothesis: ${finding.hypothesis}`);
            lines.push(`  - impact: ${finding.impact}`);
            lines.push(`  - nodes: ${nodes || '—'}`);
            lines.push(`  - signals: ${findingSignals(finding)}`);
            lines.push(`  - actions: ${(finding.actions ?? []).slice(0, 3).join(' | ') || '—'}`);
        }
    }
    lines.push('');
    lines.push('## Kernel Path Snapshot');
    lines.push(`Top storage stage: ${formatKernelStage(kernelPath?.summary.top_storage_stage)}`);
    lines.push(`Top network stage: ${formatKernelStage(kernelPath?.summary.top_network_stage)}`);
    if ((kernelPath?.nodes?.length ?? 0) === 0) {
        lines.push('- No kernel-path nodes in current window.');
    } else {
        for (const node of (kernelPath?.nodes ?? []).slice(0, 6)) {
            lines.push(`- [${node.overall_severity}] ${node.hostname || node.collector_id} storage=${formatKernelStage(node.storage.top_stage)}(${node.storage.score.toFixed(2)}) network=${formatKernelStage(node.network.top_stage)}(${node.network.score.toFixed(2)}) bottlenecks=${(node.bottlenecks ?? []).slice(0, 2).join(',') || 'none'}`);
        }
    }
    lines.push('');
    lines.push('## Resource Pressure Snapshot');
    lines.push(...buildResourceRankingLines('Network Pressure Ranking', diagnostics?.network.rankings));
    lines.push(...buildResourceRankingLines('Storage Pressure Ranking', diagnostics?.storage.rankings));
    lines.push(...buildResourceRankingLines('Probe-core Reliability Ranking', diagnostics?.probe_core?.rankings));
    lines.push('');
    lines.push(buildWorkloadHandoffMarkdown(generatedAt, filters, sortKey, sortDirection, workloads));
    return lines.join('\n');
}

interface DataPathDiagnosticsPageProps {
    onOpenTrends?: (intent: TrendsNavigationIntentInput) => void;
}

export default function DataPathDiagnosticsPage({ onOpenTrends }: DataPathDiagnosticsPageProps) {
    const initialWorkloadState = useMemo(parseWorkloadStateFromUrl, []);
    const initialWorkloadFilters = initialWorkloadState.filters;
    const [collectorId, setCollectorId] = useState('');
    const [workloadClusterInput, setWorkloadClusterInput] = useState(initialWorkloadFilters.cluster);
    const [workloadNamespaceInput, setWorkloadNamespaceInput] = useState(initialWorkloadFilters.namespace);
    const [workloadServiceInput, setWorkloadServiceInput] = useState(initialWorkloadFilters.service);
    const [workloadCluster, setWorkloadCluster] = useState(initialWorkloadFilters.cluster);
    const [workloadNamespace, setWorkloadNamespace] = useState(initialWorkloadFilters.namespace);
    const [workloadService, setWorkloadService] = useState(initialWorkloadFilters.service);
    const [expandedWorkloads, setExpandedWorkloads] = useState<Record<string, boolean>>({});
    const [workloadScopeLinkStatus, setWorkloadScopeLinkStatus] = useState<'idle' | 'copied' | 'unavailable'>('idle');
    const [workloadHandoffStatus, setWorkloadHandoffStatus] = useState<'idle' | 'copied' | 'unavailable'>('idle');
    const [workloadRcaPacketStatus, setWorkloadRcaPacketStatus] = useState<'idle' | 'copied' | 'downloaded' | 'unavailable'>('idle');
    const [workloadSortKey, setWorkloadSortKey] = useState<WorkloadSortKey>(initialWorkloadState.sortKey);
    const [workloadSortDirection, setWorkloadSortDirection] = useState<SortDirection>(initialWorkloadState.sortDirection);
    const [processCategory, setProcessCategory] = useState<ResourceCategory>('cpu');
    const [processFilter, setProcessFilter] = useState('');
    const canOpenTrends = typeof onOpenTrends === 'function';
    const fleetRefetchInterval = import.meta.env.MODE === 'test' ? false : 30000;
    const diagnosticsRefetchInterval = import.meta.env.MODE === 'test' ? false : 5000;

    const nodesQuery = useQuery({
        queryKey: ['fleet-nodes', 'diagnostics'],
        queryFn: fetchFleetNodes,
        refetchInterval: fleetRefetchInterval,
    });

    const nodes = useMemo(() => sortByHostname(nodesQuery.data?.nodes ?? []), [nodesQuery.data?.nodes]);

    const diagnosticsQuery = useQuery({
        queryKey: ['data-path-diagnostics', collectorId || 'fleet'],
        queryFn: () => fetchDataPathDiagnostics({ collectorId: collectorId || undefined }),
        refetchInterval: diagnosticsRefetchInterval,
    });
    const rootCauseQuery = useQuery({
        queryKey: ['root-cause-diagnostics', collectorId || 'fleet'],
        queryFn: () => fetchRootCauseDiagnostics({ collectorId: collectorId || undefined }),
        refetchInterval: diagnosticsRefetchInterval,
    });
    const kernelPathQuery = useQuery({
        queryKey: ['kernel-path-diagnostics', collectorId || 'fleet'],
        queryFn: () => fetchKernelPathDiagnostics({ collectorId: collectorId || undefined }),
        refetchInterval: diagnosticsRefetchInterval,
    });
    const workloadPathQuery = useQuery({
        queryKey: ['workload-path-diagnostics', workloadCluster || '*', workloadNamespace || '*', workloadService || '*'],
        queryFn: () => fetchWorkloadPathDiagnostics({
            cluster: workloadCluster || undefined,
            namespace: workloadNamespace || undefined,
            service: workloadService || undefined,
            limit: 30,
        }),
        refetchInterval: diagnosticsRefetchInterval,
    });
    const aiInfraStackQuery = useQuery({
        queryKey: ['ai-infra-stack-diagnostics', collectorId || 'fleet', workloadCluster || '*', workloadNamespace || '*', workloadService || '*'],
        queryFn: () => fetchAIInfraStackDiagnostics({
            collectorId: collectorId || undefined,
            cluster: workloadCluster || undefined,
            namespace: workloadNamespace || undefined,
            service: workloadService || undefined,
            workloadLimit: 30,
        }),
        refetchInterval: diagnosticsRefetchInterval,
    });

    const diagnostics = diagnosticsQuery.data;
    const rootCause = rootCauseQuery.data;
    const kernelPath = kernelPathQuery.data;
    const workloadPath = workloadPathQuery.data;
    const aiInfraStack = aiInfraStackQuery.data;
    const aiInfraReliabilityLayer = aiInfraFindLayerByID(aiInfraStack?.layers, 'reliability_sre');
    const aiInfraServingLayer = aiInfraFindLayerByID(aiInfraStack?.layers, 'serving_inference');
    const availabilitySLI = finiteSignalValue(aiInfraReliabilityLayer?.signals?.availability_sli);
    const latencyComplianceSLI = finiteSignalValue(aiInfraReliabilityLayer?.signals?.latency_compliance_sli);
    const errorBudgetRemaining = finiteSignalValue(aiInfraReliabilityLayer?.signals?.error_budget_remaining);
    const mttdProxySeconds = finiteSignalValue(aiInfraReliabilityLayer?.signals?.mttd_proxy_seconds);
    const mttrProxySeconds = finiteSignalValue(aiInfraReliabilityLayer?.signals?.mttr_proxy_seconds);
    const servingQueueDelay = finiteSignalValue(aiInfraServingLayer?.signals?.avg_realtime_queue_delay_sec);
    const servingRouteLatency = finiteSignalValue(aiInfraServingLayer?.signals?.avg_route_latency_ms);
    const servingKVCacheUtil = finiteSignalValue(aiInfraServingLayer?.signals?.kv_cache_utilization_avg);

    const combinedAnomalies = useMemo(() => {
        const network = diagnostics?.network.anomalies ?? [];
        const storage = diagnostics?.storage.anomalies ?? [];
        const probeCore = diagnostics?.probe_core?.anomalies ?? [];
        return [...network, ...storage, ...probeCore]
            .sort((a, b) => (b.z_score ?? 0) - (a.z_score ?? 0))
            .slice(0, 12);
    }, [diagnostics?.network.anomalies, diagnostics?.probe_core?.anomalies, diagnostics?.storage.anomalies]);

    const sortedWorkloads = useMemo(() => {
        const rows = [...(workloadPath?.workloads ?? [])];
        rows.sort((left, right) => {
            const leftMetric = workloadSortMetric(left, workloadSortKey);
            const rightMetric = workloadSortMetric(right, workloadSortKey);
            let cmp = rightMetric - leftMetric;
            if (cmp === 0) {
                cmp = workloadSeverityRank(right.severity) - workloadSeverityRank(left.severity);
            }
            if (cmp === 0) {
                cmp = right.overall_score - left.overall_score;
            }
            if (cmp === 0) {
                cmp = workloadIdentity(left).localeCompare(workloadIdentity(right));
            }
            return workloadSortDirection === 'asc' ? -cmp : cmp;
        });
        return rows;
    }, [workloadPath?.workloads, workloadSortDirection, workloadSortKey]);

    const openTrends = (intent: TrendsNavigationIntentInput) => {
        if (!canOpenTrends) {
            return;
        }
        onOpenTrends(intent);
    };

    const applyWorkloadFilters = () => {
        const filters: WorkloadFilters = {
            cluster: workloadClusterInput.trim(),
            namespace: workloadNamespaceInput.trim(),
            service: workloadServiceInput.trim(),
        };
        setWorkloadCluster(filters.cluster);
        setWorkloadNamespace(filters.namespace);
        setWorkloadService(filters.service);
        setWorkloadScopeLinkStatus('idle');
        setWorkloadHandoffStatus('idle');
        setWorkloadRcaPacketStatus('idle');
        writeWorkloadStateToUrl(filters, workloadSortKey, workloadSortDirection);
    };

    const clearWorkloadFilters = () => {
        setWorkloadClusterInput('');
        setWorkloadNamespaceInput('');
        setWorkloadServiceInput('');
        setWorkloadCluster('');
        setWorkloadNamespace('');
        setWorkloadService('');
        setWorkloadScopeLinkStatus('idle');
        setWorkloadHandoffStatus('idle');
        setWorkloadRcaPacketStatus('idle');
        writeWorkloadStateToUrl({ cluster: '', namespace: '', service: '' }, workloadSortKey, workloadSortDirection);
    };

    const toggleWorkloadDetails = (identity: string) => {
        setExpandedWorkloads((prev) => ({
            ...prev,
            [identity]: !prev[identity],
        }));
    };

    const copyWorkloadScopeLink = async () => {
        writeWorkloadStateToUrl({
            cluster: workloadCluster,
            namespace: workloadNamespace,
            service: workloadService,
        }, workloadSortKey, workloadSortDirection);
        if (typeof window === 'undefined') {
            return;
        }
        const clipboard = window.navigator?.clipboard;
        if (!clipboard?.writeText) {
            setWorkloadScopeLinkStatus('unavailable');
            return;
        }
        try {
            await clipboard.writeText(window.location.href);
            setWorkloadScopeLinkStatus('copied');
        } catch {
            setWorkloadScopeLinkStatus('unavailable');
        }
    };

    const copyWorkloadHandoffMarkdown = async () => {
        const markdown = buildWorkloadHandoffMarkdown(
            workloadPath?.generated_at,
            {
                cluster: workloadCluster,
                namespace: workloadNamespace,
                service: workloadService,
            },
            workloadSortKey,
            workloadSortDirection,
            sortedWorkloads,
        );
        if (typeof window === 'undefined') {
            return;
        }
        const clipboard = window.navigator?.clipboard;
        if (!clipboard?.writeText) {
            setWorkloadHandoffStatus('unavailable');
            return;
        }
        try {
            await clipboard.writeText(markdown);
            setWorkloadHandoffStatus('copied');
        } catch {
            setWorkloadHandoffStatus('unavailable');
        }
    };

    const resolveRootCausePacketArtifact = async (): Promise<{ markdown: string; fileName: string }> => {
        try {
            const exported = await fetchRCAPacketExport({
                collectorId: collectorId || undefined,
                cluster: workloadCluster || undefined,
                namespace: workloadNamespace || undefined,
                service: workloadService || undefined,
                sortKey: workloadSortKey,
                sortDirection: workloadSortDirection,
                workloadLimit: 30,
            });
            if (exported.markdown?.trim()) {
                return {
                    markdown: exported.markdown,
                    fileName: exported.file_name?.trim() || rootCausePacketFilename(exported.generated_at, {
                        cluster: workloadCluster,
                        namespace: workloadNamespace,
                        service: workloadService,
                    }),
                };
            }
        } catch {
            // Fallback to local assembly if backend export endpoint is unavailable.
        }
        const localPacket = buildRootCausePacketMarkdown(
            rootCause?.generated_at || workloadPath?.generated_at,
            diagnostics,
            kernelPath,
            rootCause,
            {
                cluster: workloadCluster,
                namespace: workloadNamespace,
                service: workloadService,
            },
            workloadSortKey,
            workloadSortDirection,
            sortedWorkloads,
            typeof window === 'undefined' ? undefined : window.location.href,
        );
        return {
            markdown: localPacket,
            fileName: rootCausePacketFilename(rootCause?.generated_at || workloadPath?.generated_at, {
                cluster: workloadCluster,
                namespace: workloadNamespace,
                service: workloadService,
            }),
        };
    };

    const copyRootCausePacket = async () => {
        if (typeof window === 'undefined') {
            return;
        }
        writeWorkloadStateToUrl(
            {
                cluster: workloadCluster,
                namespace: workloadNamespace,
                service: workloadService,
            },
            workloadSortKey,
            workloadSortDirection,
        );
        const artifact = await resolveRootCausePacketArtifact();
        const clipboard = window.navigator?.clipboard;
        if (!clipboard?.writeText) {
            setWorkloadRcaPacketStatus('unavailable');
            return;
        }
        try {
            await clipboard.writeText(artifact.markdown);
            setWorkloadRcaPacketStatus('copied');
        } catch {
            setWorkloadRcaPacketStatus('unavailable');
        }
    };

    const downloadRootCausePacket = async () => {
        if (typeof window === 'undefined' || typeof document === 'undefined') {
            return;
        }
        writeWorkloadStateToUrl(
            {
                cluster: workloadCluster,
                namespace: workloadNamespace,
                service: workloadService,
            },
            workloadSortKey,
            workloadSortDirection,
        );
        const artifact = await resolveRootCausePacketArtifact();
        const createObjectUrl = window.URL?.createObjectURL;
        const revokeObjectUrl = window.URL?.revokeObjectURL;
        if (typeof createObjectUrl !== 'function' || typeof revokeObjectUrl !== 'function') {
            setWorkloadRcaPacketStatus('unavailable');
            return;
        }
        const blob = new Blob([artifact.markdown], { type: 'text/markdown;charset=utf-8' });
        const objectUrl = createObjectUrl(blob);
        const anchor = document.createElement('a');
        anchor.href = objectUrl;
        anchor.download = artifact.fileName;
        anchor.rel = 'noopener';
        document.body.appendChild(anchor);
        anchor.click();
        document.body.removeChild(anchor);
        revokeObjectUrl(objectUrl);
        setWorkloadRcaPacketStatus('downloaded');
    };

    return (
        <div className="h-full overflow-auto p-4 md:p-6 space-y-4">
            <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-3">
                    <div>
                        <div className="text-lg font-semibold">Data Path Diagnostics</div>
                        <div className="text-sm text-muted-foreground">
                            Unified compute → network → storage bottleneck ranking with anomaly hints for training and inference clusters.
                        </div>
                    </div>
                    <div className="flex gap-2">
                        <select
                            value={collectorId}
                            onChange={(event) => setCollectorId(event.target.value)}
                            className="bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground"
                        >
                            <option value="">Fleet (all collectors)</option>
                            {nodes.map((node) => (
                                <option key={node.collector_id} value={node.collector_id}>
                                    {node.hostname || node.collector_id}
                                </option>
                            ))}
                        </select>
                    </div>
                </div>
                <div className="mt-3 text-xs text-muted-foreground flex flex-wrap gap-x-4 gap-y-1">
                    <span>Nodes: {diagnostics?.summary.node_count ?? 0}</span>
                    <span>Anomalies: {diagnostics?.summary.total_anomalies ?? 0}</span>
                    <span>Probe-core fallback: {diagnostics?.summary.probe_core_fallback_nodes ?? 0}</span>
                    <span>Updated: {diagnostics?.generated_at ? new Date(diagnostics.generated_at).toLocaleTimeString() : '—'}</span>
                </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-5 gap-3">
                <div className="rounded-xl border border-border bg-card p-3 shadow-sm">
                    <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1 flex items-center gap-1">
                        <Network className="w-3.5 h-3.5 text-cyan-300" /> Network Health
                    </div>
                    <div className="text-xl font-semibold">{formatPercent(diagnostics?.network.cluster_health_score, 1)}</div>
                    <div className="text-xs text-muted-foreground">Critical {diagnostics?.summary.network_critical ?? 0} · Degraded {diagnostics?.summary.network_degraded ?? 0}</div>
                </div>
                <div className="rounded-xl border border-border bg-card p-3 shadow-sm">
                    <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1 flex items-center gap-1">
                        <Database className="w-3.5 h-3.5 text-fuchsia-300" /> Storage Health
                    </div>
                    <div className="text-xl font-semibold">{formatPercent(diagnostics?.storage.cluster_health_score, 1)}</div>
                    <div className="text-xs text-muted-foreground">Critical {diagnostics?.summary.storage_critical ?? 0} · Degraded {diagnostics?.summary.storage_degraded ?? 0}</div>
                </div>
                <div className="rounded-xl border border-border bg-card p-3 shadow-sm">
                    <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1 flex items-center gap-1">
                        <ServerCrash className="w-3.5 h-3.5 text-emerald-300" /> Probe-core Health
                    </div>
                    <div className="text-xl font-semibold">{formatPercent(diagnostics?.probe_core?.cluster_health_score, 1)}</div>
                    <div className="text-xs text-muted-foreground">
                        Critical {diagnostics?.summary.probe_core_critical ?? 0} · Degraded {diagnostics?.summary.probe_core_degraded ?? 0}
                    </div>
                </div>
                <div className="rounded-xl border border-border bg-card p-3 shadow-sm">
                    <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1 flex items-center gap-1">
                        <ShieldAlert className="w-3.5 h-3.5 text-rose-300" /> Anomalies
                    </div>
                    <div className="text-xl font-semibold">{formatCount(diagnostics?.summary.total_anomalies)}</div>
                    <div className="text-xs text-muted-foreground">Top z-score spikes from network + storage + probe-core windows</div>
                </div>
                <div className="rounded-xl border border-border bg-card p-3 shadow-sm">
                    <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1 flex items-center gap-1">
                        <Workflow className="w-3.5 h-3.5 text-amber-300" /> Critical Data Paths
                    </div>
                    <div className="text-xl font-semibold">{formatCount(diagnostics?.summary.critical_data_paths)}</div>
                    <div className="text-xs text-muted-foreground">
                        Fallback {formatCount(diagnostics?.summary.probe_core_fallback_nodes)} · Invalid selection {formatCount(diagnostics?.summary.probe_core_invalid_config_nodes)}
                    </div>
                </div>
            </div>

            <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex items-center gap-2 text-sm font-semibold mb-2">
                    <Radar className="w-4 h-4 text-indigo-300" /> AI Infra Stack Layers
                </div>
                <div className="text-xs text-muted-foreground mb-3">
                    Nodes {aiInfraStack?.summary.node_count ?? 0} · Workloads {aiInfraStack?.summary.workload_count ?? 0} ·
                    Layers critical {aiInfraStack?.summary.critical_layers ?? 0} · degraded {aiInfraStack?.summary.degraded_layers ?? 0} ·
                    Coverage {formatPercent(aiInfraStack?.summary.coverage_percent, 0)} ·
                    Incident drilldowns {aiInfraStack?.summary.incident_drilldowns ?? 0} ·
                    Top layer {aiInfraStack?.summary.top_layer_title || '—'}
                </div>
                <div className="text-[11px] text-muted-foreground mb-3">
                    Measurements: measured {aiInfraStack?.summary.measurements_measured ?? 0} · partial {aiInfraStack?.summary.measurements_partial ?? 0} · missing {aiInfraStack?.summary.measurements_missing ?? 0}
                    {' '}· Methods: direct {aiInfraStack?.summary.methods_direct ?? 0} · derived {aiInfraStack?.summary.methods_derived ?? 0} · proxy {aiInfraStack?.summary.methods_proxy ?? 0} · missing {aiInfraStack?.summary.methods_missing ?? 0}
                </div>
                <div className="text-[11px] text-muted-foreground mb-3">
                    Status legend: `measured` = full scoped coverage · `partial` = mixed/partial scoped coverage · `missing` = unavailable in current scope.
                </div>
                <div className="text-[11px] text-muted-foreground mb-3">
                    Method legend: `direct` = raw source counters · `derived` = computed from measured counters · `proxy` = heuristic/runtime proxy.
                </div>
                {aiInfraStack?.summary.top_risk ? (
                    <div className="text-[11px] text-amber-200 mb-3">Top risk: {aiInfraStack.summary.top_risk}</div>
                ) : null}
                {(aiInfraReliabilityLayer || aiInfraServingLayer) && (
                    <div className="rounded-md border border-border/70 bg-background/35 p-3 mb-3">
                        <div className="text-xs font-medium mb-2">SRE Reliability Snapshot</div>
                        <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-2">
                            <div className="rounded border border-border/60 bg-background/40 p-2">
                                <div className="text-[10px] text-muted-foreground uppercase tracking-wide">Availability SLI</div>
                                <div className="text-sm font-medium">{availabilitySLI !== undefined ? formatPercent(availabilitySLI * 100, 1) : '—'}</div>
                            </div>
                            <div className="rounded border border-border/60 bg-background/40 p-2">
                                <div className="text-[10px] text-muted-foreground uppercase tracking-wide">Latency SLI</div>
                                <div className="text-sm font-medium">{latencyComplianceSLI !== undefined ? formatPercent(latencyComplianceSLI * 100, 1) : '—'}</div>
                            </div>
                            <div className="rounded border border-border/60 bg-background/40 p-2">
                                <div className="text-[10px] text-muted-foreground uppercase tracking-wide">Error Budget</div>
                                <div className="text-sm font-medium">{errorBudgetRemaining !== undefined ? formatPercent(errorBudgetRemaining, 1) : '—'}</div>
                            </div>
                            <div className="rounded border border-border/60 bg-background/40 p-2">
                                <div className="text-[10px] text-muted-foreground uppercase tracking-wide">MTTD Proxy</div>
                                <div className="text-sm font-medium">{formatDurationSeconds(mttdProxySeconds)}</div>
                            </div>
                            <div className="rounded border border-border/60 bg-background/40 p-2">
                                <div className="text-[10px] text-muted-foreground uppercase tracking-wide">MTTR Proxy</div>
                                <div className="text-sm font-medium">{formatDurationSeconds(mttrProxySeconds)}</div>
                            </div>
                            <div className="rounded border border-border/60 bg-background/40 p-2">
                                <div className="text-[10px] text-muted-foreground uppercase tracking-wide">Inference Tail</div>
                                <div className="text-sm font-medium">
                                    {servingRouteLatency !== undefined ? `${servingRouteLatency.toFixed(1)}ms` : '—'}
                                </div>
                                <div className="text-[10px] text-muted-foreground">
                                    queue {formatDurationSeconds(servingQueueDelay)} · KV {servingKVCacheUtil !== undefined ? formatPercent(servingKVCacheUtil, 0) : '—'}
                                </div>
                            </div>
                        </div>
                        <div className="text-[10px] text-muted-foreground mt-2">
                            Source: reliability_sre + serving_inference layer signals (runtime proxies).
                        </div>
                    </div>
                )}
                {aiInfraStackQuery.isLoading ? (
                    <div className="text-sm text-muted-foreground">Loading AI infra stack diagnostics...</div>
                ) : (aiInfraStack?.layers?.length ?? 0) === 0 ? (
                    <div className="text-sm text-muted-foreground">No AI infra stack diagnostics available for the selected scope.</div>
                ) : (
                    <div className="space-y-3">
                        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-3">
                            {(aiInfraStack?.layers ?? []).map((layer) => {
                                const Icon = aiInfraLayerIcon(layer.id);
                                const measured = (layer.measurements ?? []).filter((measurement) => measurement.status === 'measured').length;
                                const partial = (layer.measurements ?? []).filter((measurement) => measurement.status === 'partial').length;
                                const missing = (layer.measurements ?? []).filter((measurement) => measurement.status === 'missing').length;
                                const topEntity = layer.ranked_entities?.[0];
                                return (
                                    <article key={layer.id} className="rounded-md border border-border/80 bg-background/40 p-3 space-y-2">
                                        <div className="flex items-start justify-between gap-2">
                                            <div className="flex items-center gap-2">
                                                <Icon className="w-3.5 h-3.5 text-indigo-200" />
                                                <div className="text-sm font-medium leading-snug">{layer.title}</div>
                                            </div>
                                            <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(layer.severity)}`}>
                                                {layer.severity}
                                            </span>
                                        </div>
                                        <div className="text-[11px] text-muted-foreground">
                                            Score {layer.score.toFixed(2)} · Coverage {formatPercent(layer.coverage_percent, 0)} · Scope {layer.scope}
                                        </div>
                                        <div className="text-[11px] text-muted-foreground">
                                            Metrics: measured {measured} · partial {partial} · missing {missing}
                                        </div>
                                        <div className="text-[11px] text-muted-foreground">
                                            Methods: {aiInfraLayerMethodSummary(layer)}
                                        </div>
                                        <div className="text-[11px] text-muted-foreground">
                                            Domains: {aiInfraLayerDomainsSummary(layer, 2)}
                                        </div>
                                        <div className="text-[11px] text-muted-foreground">Signals: {aiInfraLayerSignalsSummary(layer, 2)}</div>
                                        <div className="text-[11px] text-muted-foreground">
                                            Risk: {(layer.top_risks ?? []).slice(0, 1).join('') || '—'}
                                        </div>
                                        <div className="text-[11px] text-muted-foreground">
                                            Top entity: {topEntity ? `${topEntity.kind}:${topEntity.label} (${topEntity.score.toFixed(2)})` : '—'}
                                        </div>
                                    </article>
                                );
                            })}
                        </div>
                        {(aiInfraStack?.layers ?? []).some((layer) => (layer.measurements?.length ?? 0) > 0) && (
                            <div className="rounded-md border border-border/70 bg-background/35 p-3">
                                <div className="text-xs font-medium mb-2">Measurement Source Snapshot</div>
                                <div className="grid grid-cols-1 xl:grid-cols-2 gap-2">
                                    {(aiInfraStack?.layers ?? []).map((layer) => (
                                        <div key={`measurements-${layer.id}`} className="space-y-1">
                                            <div className="text-[11px] text-muted-foreground">{layer.title}</div>
                                            {(layer.measurements ?? []).slice(0, 3).map((measurement) => (
                                                <div key={`${layer.id}-${measurement.name}`} className="flex items-center justify-between gap-2 text-[11px]">
                                                    <div className="truncate">{measurement.name}</div>
                                                    <div className="inline-flex items-center gap-1">
                                                        <span className={`inline-flex rounded-full border px-1.5 py-0.5 text-[10px] uppercase tracking-wide ${aiInfraMeasurementStatusClass(measurement.status)}`}>
                                                            {measurement.status}
                                                        </span>
                                                        {measurement.method && (
                                                            <span className={`inline-flex rounded-full border px-1.5 py-0.5 text-[10px] uppercase tracking-wide ${aiInfraMeasurementMethodClass(measurement.method)}`}>
                                                                {measurement.method}
                                                            </span>
                                                        )}
                                                    </div>
                                                </div>
                                            ))}
                                        </div>
                                    ))}
                                </div>
                            </div>
                        )}
                        {(aiInfraStack?.layers ?? []).some((layer) => (layer.domains?.length ?? 0) > 0) && (
                            <div className="rounded-md border border-border/70 bg-background/35 p-3">
                                <div className="text-xs font-medium mb-2">Layer Domain Decomposition</div>
                                <div className="grid grid-cols-1 xl:grid-cols-2 gap-2">
                                    {(aiInfraStack?.layers ?? []).map((layer) => (
                                        <div key={`domains-${layer.id}`} className="space-y-1">
                                            <div className="text-[11px] text-muted-foreground">{layer.title}</div>
                                            {(layer.domains ?? []).slice(0, 3).map((domain) => (
                                                <div key={`${layer.id}-${domain.id}`} className="text-[11px] flex items-center justify-between gap-2">
                                                    <div className="truncate">
                                                        {domain.title}
                                                        <div className="text-[10px] text-muted-foreground">
                                                            {aiInfraPlacementSignalsSummary(domain.signals, 2)}
                                                        </div>
                                                    </div>
                                                    <div className="inline-flex items-center gap-2">
                                                        <div className="text-muted-foreground">
                                                            {domain.score.toFixed(2)} · {formatPercent(domain.coverage_percent, 0)}
                                                        </div>
                                                        <button
                                                            type="button"
                                                            onClick={() => {
                                                                const metricKey = aiInfraDomainMetricKey(layer, domain);
                                                                openTrends({
                                                                    collectorId: collectorId || aiInfraLayerCollectorHint(layer),
                                                                    category: resourceCategoryForMetricKey(metricKey),
                                                                    metricKey,
                                                                    triggerLabel: `Layer domain (${layer.id}/${domain.id})`,
                                                                });
                                                            }}
                                                            disabled={!canOpenTrends}
                                                            className="text-[10px] px-2 py-0.5 rounded border border-indigo-400/40 text-indigo-200 hover:bg-indigo-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                                        >
                                                            Open trends
                                                        </button>
                                                    </div>
                                                </div>
                                            ))}
                                        </div>
                                    ))}
                                </div>
                            </div>
                        )}
                        {(aiInfraStack?.workload_mappings?.length ?? 0) > 0 && (
                            <div className="overflow-auto">
                                <table className="w-full text-xs">
                                    <thead className="border-b border-border text-muted-foreground">
                                        <tr>
                                            <th className="text-left py-2 pr-2">Workload Path</th>
                                            <th className="text-left py-2 pr-2">Pods</th>
                                            <th className="text-left py-2 pr-2">Nodes</th>
                                            <th className="text-left py-2 pr-2">Bottleneck</th>
                                            <th className="text-left py-2">Risks</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {(aiInfraStack?.workload_mappings ?? []).slice(0, 8).map((mapping) => (
                                            <tr key={`${mapping.cluster}/${mapping.namespace}/${mapping.kind}/${mapping.name}`} className="border-b border-border/50">
                                                <td className="py-2 pr-2">
                                                    <div className="font-medium">{mapping.namespace}/{mapping.name}</div>
                                                    <div className="text-[11px] text-muted-foreground">{mapping.cluster} · {mapping.kind} · {mapping.path}</div>
                                                </td>
                                                <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                                    running {mapping.pods_running} · pending {mapping.pods_pending} · failed {mapping.pods_failed}
                                                </td>
                                                <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                                    {mapping.resolved_nodes}/{mapping.node_count} mapped
                                                    <div className="truncate">{(mapping.nodes ?? []).slice(0, 2).join(' · ') || '—'}</div>
                                                </td>
                                                <td className="py-2 pr-2 text-[11px] text-muted-foreground">{mapping.bottleneck || '—'}</td>
                                                <td className="py-2 text-[11px] text-muted-foreground">{(mapping.risk_flags ?? []).slice(0, 2).map(riskLabel).join(' · ') || '—'}</td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        )}
                        {(aiInfraStack?.incident_drilldowns?.length ?? 0) > 0 && (
                            <div className="rounded-md border border-border/70 bg-background/35 p-3">
                                <div className="text-xs font-medium mb-2">Incident → Workload → Placement Drilldowns</div>
                                <div className="space-y-3">
                                    {(aiInfraStack?.incident_drilldowns ?? []).slice(0, 4).map((drilldown) => (
                                        <article key={drilldown.finding_id} className="rounded-md border border-border/70 bg-background/50 p-3 space-y-2">
                                            <div className="flex items-start justify-between gap-2">
                                                <div>
                                                    <div className="text-sm font-medium">{drilldown.finding_title}</div>
                                                    <div className="text-[11px] text-muted-foreground">
                                                        {drilldown.workflow} · confidence {formatConfidence(drilldown.confidence)} · category {drilldown.category}
                                                    </div>
                                                </div>
                                                <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(drilldown.severity)}`}>
                                                    {drilldown.severity}
                                                </span>
                                            </div>
                                            <div className="text-[11px] text-muted-foreground">
                                                Affected nodes: {(drilldown.affected_nodes ?? []).slice(0, 4).join(', ') || '—'}
                                            </div>
                                            <div className="text-[11px] text-muted-foreground">
                                                Contention: {aiInfraIncidentSignalSummary(drilldown, 4)}
                                            </div>
                                            <div className="flex justify-end">
                                                <button
                                                    type="button"
                                                    onClick={() => {
                                                        const metricKey = aiInfraIncidentMetricKey(drilldown);
                                                        openTrends({
                                                            collectorId: aiInfraIncidentCollectorHint(drilldown),
                                                            category: resourceCategoryForMetricKey(metricKey),
                                                            metricKey,
                                                            triggerLabel: `Incident contention (${drilldown.finding_id})`,
                                                        });
                                                    }}
                                                    disabled={!canOpenTrends}
                                                    className="text-[10px] px-2 py-0.5 rounded border border-indigo-400/40 text-indigo-200 hover:bg-indigo-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                                >
                                                    Open contention trend
                                                </button>
                                            </div>
                                            {(drilldown.workloads?.length ?? 0) > 0 && (
                                                <div className="space-y-1">
                                                    <div className="text-[11px] font-medium text-muted-foreground">Workload hops</div>
                                                    {(drilldown.workloads ?? []).slice(0, 3).map((workloadHop) => (
                                                        <div key={`${drilldown.finding_id}-${workloadHop.id}`} className="text-[11px] text-muted-foreground">
                                                            {workloadHop.namespace || 'ns'}/{workloadHop.name || workloadHop.id}
                                                            {' '}· {workloadHop.bottleneck || 'unknown'} bottleneck ·
                                                            {' '}queue {Number(workloadHop.queue_delay_seconds ?? 0).toFixed(1)}s ·
                                                            {' '}pending {workloadHop.pods_pending ?? 0} · failed {workloadHop.pods_failed ?? 0}
                                                            {workloadHop.reason ? ` · ${workloadHop.reason}` : ''}
                                                        </div>
                                                    ))}
                                                </div>
                                            )}
                                            {(drilldown.placements?.length ?? 0) > 0 && (
                                                <div className="overflow-auto">
                                                    <table className="w-full text-[11px]">
                                                        <thead className="border-b border-border/60 text-muted-foreground">
                                                            <tr>
                                                                <th className="text-left py-1 pr-2">Placement node</th>
                                                                <th className="text-right py-1 pr-2">Score</th>
                                                                <th className="text-left py-1 pr-2">Signals</th>
                                                                <th className="text-left py-1 pr-2">Reason</th>
                                                                <th className="text-right py-1">Action</th>
                                                            </tr>
                                                        </thead>
                                                        <tbody>
                                                            {(drilldown.placements ?? []).slice(0, 4).map((placement) => {
                                                                const metricKey = aiInfraPlacementMetricKey(placement.signals);
                                                                return (
                                                                    <tr key={`${drilldown.finding_id}-${placement.workload_id}-${placement.node_id}`} className="border-b border-border/40">
                                                                        <td className="py-1 pr-2">
                                                                            <div>{placement.hostname || placement.node_id || '—'}</div>
                                                                            <div className="text-[10px] text-muted-foreground">{placement.workload_id || '—'}</div>
                                                                        </td>
                                                                        <td className="py-1 pr-2 text-right">{placement.score.toFixed(2)}</td>
                                                                        <td className="py-1 pr-2 text-muted-foreground">{aiInfraPlacementSignalsSummary(placement.signals, 2)}</td>
                                                                        <td className="py-1 pr-2 text-muted-foreground">{placement.reason || '—'}</td>
                                                                        <td className="py-1 text-right">
                                                                            <button
                                                                                type="button"
                                                                                onClick={() => {
                                                                                    openTrends({
                                                                                        collectorId: placement.collector_id || undefined,
                                                                                        category: resourceCategoryForMetricKey(metricKey),
                                                                                        metricKey,
                                                                                        triggerLabel: `Incident drilldown (${drilldown.finding_id})`,
                                                                                    });
                                                                                }}
                                                                                disabled={!canOpenTrends}
                                                                                className="text-[10px] px-2 py-0.5 rounded border border-indigo-400/40 text-indigo-200 hover:bg-indigo-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                                                            >
                                                                                Open trends
                                                                            </button>
                                                                        </td>
                                                                    </tr>
                                                                );
                                                            })}
                                                        </tbody>
                                                    </table>
                                                </div>
                                            )}
                                            <div className="text-[11px] text-muted-foreground">
                                                Triage: {(drilldown.triage ?? []).slice(0, 2).join(' | ') || '—'}
                                            </div>
                                        </article>
                                    ))}
                                </div>
                            </div>
                        )}
                    </div>
                )}
            </section>

            <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex items-center gap-2 text-sm font-semibold mb-2">
                    <ShieldAlert className="w-4 h-4 text-amber-300" /> Cross-Layer Root Cause Findings
                </div>
                <div className="text-xs text-muted-foreground mb-3">
                    Critical {rootCause?.summary.critical_findings ?? 0} · Degraded {rootCause?.summary.degraded_findings ?? 0} ·
                    Linked anomalies {rootCause?.data_path.total_anomalies ?? 0}
                </div>
                {rootCauseQuery.isLoading ? (
                    <div className="text-sm text-muted-foreground">Loading root-cause diagnostics...</div>
                ) : (rootCause?.findings?.length ?? 0) === 0 ? (
                    <div className="text-sm text-muted-foreground">No active root-cause findings in the current diagnostics window.</div>
                ) : (
                    <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
                        {(rootCause?.findings ?? []).slice(0, 6).map((finding) => (
                            <article key={finding.id} className="rounded-md border border-border/80 bg-background/40 p-3 space-y-2">
                                <div className="flex items-start justify-between gap-2">
                                    <div className="text-sm font-medium leading-snug">{finding.title}</div>
                                    <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(finding.severity)}`}>
                                        {finding.severity}
                                    </span>
                                </div>
                                <div className="text-[11px] text-muted-foreground">
                                    Confidence {formatConfidence(finding.confidence)} · {finding.category}
                                </div>
                                <div className="text-xs">{finding.hypothesis}</div>
                                <div className="text-[11px] text-muted-foreground">{finding.impact}</div>
                                <div className="text-[11px] text-muted-foreground">
                                    Nodes: {(finding.affected_nodes ?? []).map((node) => node.hostname || node.collector_id).slice(0, 4).join(', ') || '—'}
                                </div>
                                <div className="text-[11px] text-muted-foreground">Signals: {findingSignals(finding)}</div>
                                <div className="text-[11px] text-muted-foreground">
                                    Actions: {(finding.actions ?? []).slice(0, 2).join(' | ') || '—'}
                                </div>
                                <div className="flex justify-end">
                                    <button
                                        type="button"
                                        onClick={() => {
                                            const metricKey = rootCauseFindingMetricKey(finding);
                                            openTrends({
                                                collectorId: rootCauseFindingCollectorHint(finding),
                                                category: resourceCategoryForMetricKey(metricKey),
                                                metricKey,
                                                triggerLabel: `Root cause (${finding.id})`,
                                            });
                                        }}
                                        disabled={!canOpenTrends}
                                        className="text-[10px] px-2 py-0.5 rounded border border-indigo-400/40 text-indigo-200 hover:bg-indigo-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                    >
                                        Open trends
                                    </button>
                                </div>
                            </article>
                        ))}
                    </div>
                )}
            </section>

            <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex items-center gap-2 text-sm font-semibold mb-2">
                    <ArrowUpDown className="w-4 h-4 text-sky-300" /> Linux Kernel Path Diagnostics
                </div>
                <div className="text-xs text-muted-foreground mb-3">
                    Nodes {kernelPath?.summary.node_count ?? 0} · Critical {kernelPath?.summary.critical_nodes ?? 0} ·
                    Degraded {kernelPath?.summary.degraded_nodes ?? 0} · Top storage stage {formatKernelStage(kernelPath?.summary.top_storage_stage)} ·
                    Top network stage {formatKernelStage(kernelPath?.summary.top_network_stage)}
                </div>
                {kernelPathQuery.isLoading ? (
                    <div className="text-sm text-muted-foreground">Loading kernel-path diagnostics...</div>
                ) : (kernelPath?.nodes?.length ?? 0) === 0 ? (
                    <div className="text-sm text-muted-foreground">No kernel-path diagnostics available for the selected scope.</div>
                ) : (
                    <div className="overflow-auto">
                        <table className="w-full text-xs">
                            <thead className="border-b border-border text-muted-foreground">
                                <tr>
                                    <th className="text-left py-2 pr-2">Node</th>
                                    <th className="text-left py-2 pr-2">Storage Path</th>
                                    <th className="text-left py-2 pr-2">Network Path</th>
                                    <th className="text-left py-2 pr-2">Bottlenecks</th>
                                    <th className="text-left py-2">Severity</th>
                                </tr>
                            </thead>
                            <tbody>
                                {(kernelPath?.nodes ?? []).map((node) => (
                                    <tr key={`kernel-path-${node.collector_id}`} className="border-b border-border/50">
                                        <td className="py-2 pr-2">
                                            <div className="font-medium">{node.hostname || node.collector_id}</div>
                                            <div className="text-[11px] text-muted-foreground">{node.collector_id}</div>
                                        </td>
                                        <td className="py-2 pr-2 text-[11px] text-muted-foreground">{kernelDomainHeadline(node, 'storage')}</td>
                                        <td className="py-2 pr-2 text-[11px] text-muted-foreground">{kernelDomainHeadline(node, 'network')}</td>
                                        <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                            {(node.bottlenecks ?? []).join(' · ') || '—'}
                                        </td>
                                        <td className="py-2">
                                            <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(node.overall_severity)}`}>
                                                {node.overall_severity}
                                            </span>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </section>

            <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-3 mb-3">
                    <div>
                        <div className="flex items-center gap-2 text-sm font-semibold">
                            <Boxes className="w-4 h-4 text-blue-300" /> Kubernetes Workload Path Diagnostics
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                            Workload → node → network/storage/kernel mapping with spread and starvation risk flags.
                        </div>
                    </div>
                    <div className="flex flex-wrap gap-2 items-center">
                        <input
                            value={workloadClusterInput}
                            onChange={(event) => setWorkloadClusterInput(event.target.value)}
                            placeholder="Cluster"
                            className="bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground w-32"
                        />
                        <input
                            value={workloadNamespaceInput}
                            onChange={(event) => setWorkloadNamespaceInput(event.target.value)}
                            placeholder="Namespace"
                            className="bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground w-32"
                        />
                        <input
                            value={workloadServiceInput}
                            onChange={(event) => setWorkloadServiceInput(event.target.value)}
                            placeholder="Service"
                            className="bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground w-32"
                        />
                        <select
                            aria-label="Workload sort key"
                            value={workloadSortKey}
                            onChange={(event) => {
                                const nextSortKey = event.target.value as WorkloadSortKey;
                                setWorkloadSortKey(nextSortKey);
                                setWorkloadHandoffStatus('idle');
                                setWorkloadRcaPacketStatus('idle');
                                writeWorkloadStateToUrl(
                                    {
                                        cluster: workloadCluster,
                                        namespace: workloadNamespace,
                                        service: workloadService,
                                    },
                                    nextSortKey,
                                    workloadSortDirection,
                                );
                            }}
                            className="bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground"
                        >
                            <option value="severity">Sort: severity</option>
                            <option value="overall">Sort: overall</option>
                            <option value="coverage">Sort: coverage</option>
                            <option value="network">Sort: network</option>
                            <option value="storage">Sort: storage</option>
                        </select>
                        <button
                            type="button"
                            onClick={() => {
                                const nextSortDirection = workloadSortDirection === 'desc' ? 'asc' : 'desc';
                                setWorkloadSortDirection(nextSortDirection);
                                setWorkloadHandoffStatus('idle');
                                setWorkloadRcaPacketStatus('idle');
                                writeWorkloadStateToUrl(
                                    {
                                        cluster: workloadCluster,
                                        namespace: workloadNamespace,
                                        service: workloadService,
                                    },
                                    workloadSortKey,
                                    nextSortDirection,
                                );
                            }}
                            className="text-[11px] px-2 py-1 rounded border border-border/80 text-muted-foreground hover:bg-muted/20"
                        >
                            {workloadSortDirection === 'desc' ? 'Descending' : 'Ascending'}
                        </button>
                        <button
                            type="button"
                            onClick={applyWorkloadFilters}
                            className="text-[11px] px-2 py-1 rounded border border-blue-400/40 text-blue-200 hover:bg-blue-500/10"
                        >
                            Apply
                        </button>
                        <button
                            type="button"
                            onClick={clearWorkloadFilters}
                            className="text-[11px] px-2 py-1 rounded border border-border/80 text-muted-foreground hover:bg-muted/20"
                        >
                            Reset
                        </button>
                        <button
                            type="button"
                            onClick={copyWorkloadScopeLink}
                            className="text-[11px] px-2 py-1 rounded border border-blue-400/40 text-blue-200 hover:bg-blue-500/10"
                        >
                            Copy scope link
                        </button>
                        <button
                            type="button"
                            onClick={copyWorkloadHandoffMarkdown}
                            className="text-[11px] px-2 py-1 rounded border border-blue-400/40 text-blue-200 hover:bg-blue-500/10"
                        >
                            Copy handoff markdown
                        </button>
                        <button
                            type="button"
                            onClick={copyRootCausePacket}
                            className="text-[11px] px-2 py-1 rounded border border-amber-400/40 text-amber-200 hover:bg-amber-500/10"
                        >
                            Copy RCA packet
                        </button>
                        <button
                            type="button"
                            onClick={downloadRootCausePacket}
                            className="text-[11px] px-2 py-1 rounded border border-amber-400/40 text-amber-200 hover:bg-amber-500/10"
                        >
                            Download RCA packet
                        </button>
                    </div>
                </div>
                <div className="text-xs text-muted-foreground mb-3 flex flex-wrap gap-x-4 gap-y-1">
                    <span>Workloads {workloadPath?.summary.workload_count ?? 0}</span>
                    <span>Critical {workloadPath?.summary.critical_workloads ?? 0}</span>
                    <span>Degraded {workloadPath?.summary.degraded_workloads ?? 0}</span>
                    <span>Telemetry covered {workloadPath?.summary.telemetry_covered_workloads ?? 0}</span>
                    <span>Multi-node {workloadPath?.summary.multi_node_workloads ?? 0}</span>
                    <span>Top bottleneck {workloadPath?.summary.top_bottleneck || '—'}</span>
                    <span>Severity legend critical &gt; degraded &gt; healthy &gt; unknown</span>
                    <span>Ranking {WORKLOAD_SORT_LABEL[workloadSortKey]} ({workloadSortDirection})</span>
                    <span>
                        Scope link {workloadScopeLinkStatus === 'copied'
                            ? 'copied'
                            : workloadScopeLinkStatus === 'unavailable'
                                ? 'clipboard unavailable'
                                : 'ready'}
                    </span>
                    <span>
                        Handoff markdown {workloadHandoffStatus === 'copied'
                            ? 'copied'
                            : workloadHandoffStatus === 'unavailable'
                                ? 'clipboard unavailable'
                                : 'ready'}
                    </span>
                    <span>
                        RCA packet {workloadRcaPacketStatus === 'copied'
                            ? 'copied'
                            : workloadRcaPacketStatus === 'downloaded'
                                ? 'downloaded'
                            : workloadRcaPacketStatus === 'unavailable'
                                ? 'export unavailable'
                                : 'ready'}
                    </span>
                </div>
                {workloadPathQuery.isLoading ? (
                    <div className="text-sm text-muted-foreground">Loading workload-path diagnostics...</div>
                ) : workloadPathQuery.isError ? (
                    <div className="text-sm text-muted-foreground">
                        {hasHttpStatus(workloadPathQuery.error, 503)
                            ? 'Kubernetes integration is disabled; workload-path diagnostics are unavailable.'
                            : 'Failed to load workload-path diagnostics.'}
                    </div>
                ) : (workloadPath?.workloads?.length ?? 0) === 0 ? (
                    <div className="text-sm text-muted-foreground">No workload-path diagnostics match current filters.</div>
                ) : (
                    <div className="overflow-auto">
                        <table className="w-full text-xs">
                            <thead className="border-b border-border text-muted-foreground">
                                <tr>
                                    <th className="text-left py-2 pr-2">Workload</th>
                                    <th className="text-left py-2 pr-2">Pods</th>
                                    <th className="text-left py-2 pr-2">Nodes</th>
                                    <th className="text-right py-2 pr-2">Coverage</th>
                                    <th className="text-right py-2 pr-2">Scores (C/N/S/O)</th>
                                    <th className="text-left py-2 pr-2">Bottleneck</th>
                                    <th className="text-left py-2 pr-2">Top Stages</th>
                                    <th className="text-left py-2 pr-2">Risks</th>
                                    <th className="text-left py-2 pr-2">Evidence</th>
                                    <th className="text-right py-2">Action</th>
                                </tr>
                            </thead>
                            <tbody>
                                {sortedWorkloads.map((workload) => {
                                    const metricKey = workloadMetric(workload);
                                    const category = workloadCategory(workload, metricKey);
                                    const topNode = workload.nodes?.[0];
                                    const identity = workloadIdentity(workload);
                                    const detailsExpanded = Boolean(expandedWorkloads[identity]);
                                    return (
                                        <React.Fragment key={identity}>
                                            <tr className="border-b border-border/50">
                                                <td className="py-2 pr-2">
                                                    <div className="font-medium">{workload.namespace}/{workload.name}</div>
                                                    <div className="text-[11px] text-muted-foreground">{workload.cluster} · {workload.kind} · svc {workload.service || '—'}</div>
                                                </td>
                                                <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                                    {workload.pods_running}/{workload.pods_total} running
                                                    {(workload.pods_pending > 0 || workload.pods_failed > 0) && (
                                                        <div>pending {workload.pods_pending} · failed {workload.pods_failed}</div>
                                                    )}
                                                </td>
                                                <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                                    {workload.resolved_nodes}/{workload.node_count} mapped
                                                    {topNode && <div>{topNode.hostname || topNode.node_name}</div>}
                                                </td>
                                                <td className="py-2 pr-2 text-right">{formatPercent(workload.telemetry_coverage_percent, 0)}</td>
                                                <td className="py-2 pr-2 text-right text-[11px] text-muted-foreground">
                                                    {workload.compute_score.toFixed(2)} / {workload.network_score.toFixed(2)} / {workload.storage_score.toFixed(2)} / {workload.overall_score.toFixed(2)}
                                                </td>
                                                <td className="py-2 pr-2">
                                                    <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(workload.severity)}`}>
                                                        {workload.bottleneck}
                                                    </span>
                                                </td>
                                                <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                                    <div>storage {formatKernelStage(workload.top_storage_stage)}</div>
                                                    <div>network {formatKernelStage(workload.top_network_stage)}</div>
                                                </td>
                                                <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                                    {(workload.risks ?? []).length === 0
                                                        ? '—'
                                                        : (workload.risks ?? []).slice(0, 2).map(riskLabel).join(' · ')}
                                                </td>
                                                <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                                    <div>{compactSignalSummary(workload.signals, 2)}</div>
                                                    <div className="text-[10px] text-muted-foreground/80">{compactSignalSourceSummary(workload.signals, workload.sources, 2)}</div>
                                                </td>
                                                <td className="py-2 text-right">
                                                    <div className="inline-flex gap-2">
                                                        <button
                                                            type="button"
                                                            onClick={() => toggleWorkloadDetails(identity)}
                                                            className="text-[11px] px-2 py-1 rounded border border-border/80 text-muted-foreground hover:bg-muted/20"
                                                        >
                                                            {detailsExpanded ? 'Hide details' : 'Show details'}
                                                        </button>
                                                        <button
                                                            type="button"
                                                            onClick={() => {
                                                                openTrends({
                                                                    collectorId: topNode?.collector_id || undefined,
                                                                    category,
                                                                    metricKey,
                                                                    triggerLabel: `Workload path (${workload.namespace}/${workload.name})`,
                                                                });
                                                            }}
                                                            disabled={!canOpenTrends}
                                                            className="text-[11px] px-2 py-1 rounded border border-blue-400/40 text-blue-200 hover:bg-blue-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                                        >
                                                            Open trends
                                                        </button>
                                                    </div>
                                                </td>
                                            </tr>
                                            {detailsExpanded && (
                                                <tr className="border-b border-border/50 bg-background/30">
                                                    <td colSpan={10} className="py-3 px-2">
                                                        <div className="text-[11px] font-medium mb-2">Per-node path mapping</div>
                                                        {(workload.nodes?.length ?? 0) === 0 ? (
                                                            <div className="text-[11px] text-muted-foreground">No per-node telemetry mapped for this workload.</div>
                                                        ) : (
                                                            <div className="space-y-2">
                                                                {workload.nodes?.slice(0, 8).map((node) => (
                                                                    <div key={`${identity}/${node.node_name}`} className="rounded-md border border-border/70 bg-background/50 p-2">
                                                                        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
                                                                            <span className="font-medium">{node.hostname || node.node_name}</span>
                                                                            <span className="text-muted-foreground">collector {node.collector_id || '—'}</span>
                                                                            <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(node.severity)}`}>
                                                                                {node.severity}
                                                                            </span>
                                                                            <span className="text-muted-foreground">
                                                                                C/N/S/O {node.compute_score.toFixed(2)} / {node.network_score.toFixed(2)} / {node.storage_score.toFixed(2)} / {node.overall_score.toFixed(2)}
                                                                            </span>
                                                                        </div>
                                                                        <div className="mt-1 text-[11px] text-muted-foreground">
                                                                            bottleneck {node.bottleneck || '—'} · storage {formatKernelStage(node.top_storage_stage)} · network {formatKernelStage(node.top_network_stage)}
                                                                        </div>
                                                                        <div className="mt-1 text-[11px] text-muted-foreground">
                                                                            signals {compactSignalSummary(node.signals)}
                                                                        </div>
                                                                        <div className="mt-1 text-[10px] text-muted-foreground/80">
                                                                            sources {compactSignalSourceSummary(node.signals, node.sources)}
                                                                        </div>
                                                                        <div className="mt-1 text-[11px] text-muted-foreground">
                                                                            {(node.reasons ?? []).slice(0, 2).join(' | ') || 'No immediate node-specific hints.'}
                                                                        </div>
                                                                    </div>
                                                                ))}
                                                            </div>
                                                        )}
                                                        <div className="mt-2 text-[11px] text-muted-foreground">
                                                            Workload signals: {compactSignalSummary(workload.signals, 5)}
                                                        </div>
                                                        <div className="mt-1 text-[10px] text-muted-foreground/80">
                                                            Workload signal sources: {compactSignalSourceSummary(workload.signals, workload.sources, 4)}
                                                        </div>
                                                        <div className="mt-1 text-[11px] text-muted-foreground">
                                                            {(workload.reasons ?? []).slice(0, 2).join(' | ') || 'No additional workload-level reasoning.'}
                                                        </div>
                                                    </td>
                                                </tr>
                                            )}
                                        </React.Fragment>
                                    );
                                })}
                            </tbody>
                        </table>
                    </div>
                )}
            </section>

            <ResourceProcessBreakdownPanel
                collectorId={collectorId || undefined}
                category={processCategory}
                onCategoryChange={setProcessCategory}
                triggerLabel="Data Path Diagnostics (cross-resource ownership drilldown)"
                processFilter={processFilter}
                onProcessFilterChange={setProcessFilter}
            />

            <div className="grid grid-cols-1 xl:grid-cols-3 gap-4">
                <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                    <div className="flex items-center gap-2 text-sm font-semibold mb-2">
                        <Network className="w-4 h-4 text-cyan-300" /> Network Pressure Ranking
                    </div>
                    {diagnosticsQuery.isLoading ? (
                        <div className="text-sm text-muted-foreground">Loading network diagnostics...</div>
                    ) : (
                        <div className="overflow-auto">
                            <table className="w-full text-xs">
                                <thead className="border-b border-border text-muted-foreground">
                                    <tr>
                                        <th className="text-left py-2 pr-2">Node</th>
                                        <th className="text-right py-2 pr-2">Score</th>
                                        <th className="text-left py-2 pr-2">Severity</th>
                                        <th className="text-left py-2 pr-2">Key Signals</th>
                                        <th className="text-right py-2">Action</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {(diagnostics?.network.rankings ?? []).map((row) => (
                                        <tr key={`network-${row.collector_id}`} className="border-b border-border/50">
                                            <td className="py-2 pr-2">
                                                <div className="font-medium">{row.hostname || row.collector_id}</div>
                                                <div className="text-[11px] text-muted-foreground">{row.collector_id}</div>
                                            </td>
                                            <td className="py-2 pr-2 text-right font-semibold">{row.score.toFixed(2)}</td>
                                            <td className="py-2 pr-2">
                                                <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(row.severity)}`}>
                                                    {row.severity}
                                                </span>
                                            </td>
                                            <td className="py-2 pr-2 text-[11px] text-muted-foreground">{pressureSignals(row)}</td>
                                            <td className="py-2 text-right">
                                                <button
                                                    type="button"
                                                    onClick={() => {
                                                        const metricKey = metricForDataPathPressureSignal('network', dominantSignalMetric(row));
                                                        openTrends({
                                                            collectorId: row.collector_id,
                                                            category: categoryForDataPathResource('network', metricKey),
                                                            metricKey,
                                                            triggerLabel: `Network pressure ranking (${row.hostname || row.collector_id})`,
                                                        });
                                                    }}
                                                    disabled={!canOpenTrends}
                                                    className="text-[11px] px-2 py-1 rounded border border-cyan-400/40 text-cyan-200 hover:bg-cyan-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                                >
                                                    Open trends
                                                </button>
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                    {(diagnostics?.network.top_processes?.length ?? 0) > 0 && (
                        <div className="mt-3 border-t border-border pt-3">
                            <div className="text-xs font-medium text-muted-foreground mb-2">Top Network Processes</div>
                            <div className="space-y-2">
                                {diagnostics?.network.top_processes?.slice(0, 5).map((process) => {
                                    const context = formatProcessContext(process);
                                    return (
                                        <div key={`network-proc-${process.collector_id}-${process.pid}-${process.name}`} className="rounded-md border border-border/70 bg-background/50 px-3 py-2">
                                            <div className="text-sm font-medium">{process.name}</div>
                                            <div className="text-xs text-muted-foreground">{processHeadline(process, 'network')}</div>
                                            {context && <div className="text-[11px] text-cyan-200/90">{context}</div>}
                                            <button
                                                type="button"
                                                onClick={() => openTrends({
                                                    collectorId: process.collector_id,
                                                    category: 'network',
                                                    processFilter: buildProcessFilterHint(process),
                                                    metricKey: 'network_rx_bytes_per_second',
                                                    triggerLabel: `Network process hotspot (${process.name})`,
                                                })}
                                                disabled={!canOpenTrends}
                                                className="mt-2 text-[11px] px-2 py-1 rounded border border-cyan-400/40 text-cyan-200 hover:bg-cyan-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                            >
                                                Trace in trends
                                            </button>
                                        </div>
                                    );
                                })}
                            </div>
                        </div>
                    )}
                </section>

                <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                    <div className="flex items-center gap-2 text-sm font-semibold mb-2">
                        <Database className="w-4 h-4 text-fuchsia-300" /> Storage Pressure Ranking
                    </div>
                    {diagnosticsQuery.isLoading ? (
                        <div className="text-sm text-muted-foreground">Loading storage diagnostics...</div>
                    ) : (
                        <div className="overflow-auto">
                            <table className="w-full text-xs">
                                <thead className="border-b border-border text-muted-foreground">
                                    <tr>
                                        <th className="text-left py-2 pr-2">Node</th>
                                        <th className="text-right py-2 pr-2">Score</th>
                                        <th className="text-left py-2 pr-2">Severity</th>
                                        <th className="text-left py-2 pr-2">Key Signals</th>
                                        <th className="text-right py-2">Action</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {(diagnostics?.storage.rankings ?? []).map((row) => (
                                        <tr key={`storage-${row.collector_id}`} className="border-b border-border/50">
                                            <td className="py-2 pr-2">
                                                <div className="font-medium">{row.hostname || row.collector_id}</div>
                                                <div className="text-[11px] text-muted-foreground">{row.collector_id}</div>
                                            </td>
                                            <td className="py-2 pr-2 text-right font-semibold">{row.score.toFixed(2)}</td>
                                            <td className="py-2 pr-2">
                                                <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(row.severity)}`}>
                                                    {row.severity}
                                                </span>
                                            </td>
                                            <td className="py-2 pr-2 text-[11px] text-muted-foreground">{pressureSignals(row)}</td>
                                            <td className="py-2 text-right">
                                                <button
                                                    type="button"
                                                    onClick={() => {
                                                        const metric = metricForDataPathPressureSignal('storage', dominantSignalMetric(row));
                                                        openTrends({
                                                            collectorId: row.collector_id,
                                                            category: categoryForDataPathResource('storage', metric),
                                                            metricKey: metric,
                                                            triggerLabel: `Storage pressure ranking (${row.hostname || row.collector_id})`,
                                                        });
                                                    }}
                                                    disabled={!canOpenTrends}
                                                    className="text-[11px] px-2 py-1 rounded border border-fuchsia-400/40 text-fuchsia-200 hover:bg-fuchsia-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                                >
                                                    Open trends
                                                </button>
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                    {(diagnostics?.storage.top_processes?.length ?? 0) > 0 && (
                        <div className="mt-3 border-t border-border pt-3">
                            <div className="text-xs font-medium text-muted-foreground mb-2">Top Storage Processes</div>
                            <div className="space-y-2">
                                {diagnostics?.storage.top_processes?.slice(0, 5).map((process) => {
                                    const context = formatProcessContext(process);
                                    return (
                                        <div key={`storage-proc-${process.collector_id}-${process.pid}-${process.name}`} className="rounded-md border border-border/70 bg-background/50 px-3 py-2">
                                            <div className="text-sm font-medium">{process.name}</div>
                                            <div className="text-xs text-muted-foreground">{processHeadline(process, 'storage')}</div>
                                            {context && <div className="text-[11px] text-fuchsia-200/90">{context}</div>}
                                            <button
                                                type="button"
                                                onClick={() => openTrends({
                                                    collectorId: process.collector_id,
                                                    category: 'disk_io',
                                                    processFilter: buildProcessFilterHint(process),
                                                    metricKey: 'disk_total_iops_per_second',
                                                    triggerLabel: `Storage process hotspot (${process.name})`,
                                                })}
                                                disabled={!canOpenTrends}
                                                className="mt-2 text-[11px] px-2 py-1 rounded border border-fuchsia-400/40 text-fuchsia-200 hover:bg-fuchsia-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                            >
                                                Trace in trends
                                            </button>
                                        </div>
                                    );
                                })}
                            </div>
                        </div>
                    )}
                </section>

                <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                    <div className="flex items-center gap-2 text-sm font-semibold mb-2">
                        <ServerCrash className="w-4 h-4 text-emerald-300" /> Probe-core Reliability Ranking
                    </div>
                    {diagnosticsQuery.isLoading ? (
                        <div className="text-sm text-muted-foreground">Loading probe-core diagnostics...</div>
                    ) : (
                        <div className="overflow-auto">
                            <table className="w-full text-xs">
                                <thead className="border-b border-border text-muted-foreground">
                                    <tr>
                                        <th className="text-left py-2 pr-2">Node</th>
                                        <th className="text-right py-2 pr-2">Score</th>
                                        <th className="text-left py-2 pr-2">Severity</th>
                                        <th className="text-left py-2 pr-2">Runtime Signals</th>
                                        <th className="text-right py-2">Action</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {(diagnostics?.probe_core?.rankings ?? []).map((row) => (
                                        <tr key={`probe-core-${row.collector_id}`} className="border-b border-border/50">
                                            <td className="py-2 pr-2">
                                                <div className="font-medium">{row.hostname || row.collector_id}</div>
                                                <div className="text-[11px] text-muted-foreground">{row.collector_id}</div>
                                            </td>
                                            <td className="py-2 pr-2 text-right font-semibold">{row.score.toFixed(2)}</td>
                                            <td className="py-2 pr-2">
                                                <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(row.severity)}`}>
                                                    {row.severity}
                                                </span>
                                            </td>
                                            <td className="py-2 pr-2 text-[11px] text-muted-foreground">
                                                <div>{pressureSignals(row)}</div>
                                                <div className="text-[10px] text-muted-foreground/80">{(row.factors ?? []).slice(0, 1).join(' ') || '—'}</div>
                                            </td>
                                            <td className="py-2 text-right">
                                                <button
                                                    type="button"
                                                    onClick={() => {
                                                        const metric = metricForDataPathPressureSignal('probe_core', dominantSignalMetric(row));
                                                        openTrends({
                                                            collectorId: row.collector_id,
                                                            category: categoryForDataPathResource('probe_core', metric),
                                                            metricKey: metric,
                                                            triggerLabel: `Probe-core reliability (${row.hostname || row.collector_id})`,
                                                        });
                                                    }}
                                                    disabled={!canOpenTrends}
                                                    className="text-[11px] px-2 py-1 rounded border border-emerald-400/40 text-emerald-200 hover:bg-emerald-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                                >
                                                    Open trends
                                                </button>
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </section>
            </div>

            <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex items-center gap-2 text-sm font-semibold mb-2">
                    <AlertTriangle className="w-4 h-4 text-rose-300" /> Cross-Resource Anomaly Feed
                </div>
                {combinedAnomalies.length === 0 ? (
                    <div className="text-sm text-muted-foreground">No active network/storage/probe-core anomalies in current history window.</div>
                ) : (
                    <div className="grid grid-cols-1 xl:grid-cols-2 gap-2">
                        {combinedAnomalies.map((anomaly, index) => (
                            <div key={`${anomaly.collector_id}-${anomaly.metric}-${index}`} className="rounded-md border border-rose-500/30 bg-rose-500/10 px-3 py-2">
                                <div className="text-xs text-rose-200 font-medium">{anomalyTitle(anomaly)}</div>
                                <div className="text-sm">{formatAnomalyValue(anomaly)}</div>
                                <div className="text-[11px] text-muted-foreground">
                                    baseline {anomaly.baseline.toFixed(3)} · z={anomaly.z_score.toFixed(2)} · {anomaly.hostname || anomaly.collector_id}
                                </div>
                                <button
                                    type="button"
                                    onClick={() => {
                                        const resource = dataPathResourceKind(anomaly.resource);
                                        const metricKey = normalizeMetricKeyForTrends(anomaly.metric)
                                            || defaultMetricForDataPathResource(resource);
                                        openTrends({
                                            collectorId: anomaly.collector_id,
                                            category: categoryForDataPathResource(resource, metricKey),
                                            metricKey,
                                            triggerLabel: `${anomalyTitle(anomaly)} anomaly`,
                                        });
                                    }}
                                    disabled={!canOpenTrends}
                                    className="mt-2 text-[11px] px-2 py-1 rounded border border-rose-400/40 text-rose-200 hover:bg-rose-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                >
                                    Open trends
                                </button>
                            </div>
                        ))}
                    </div>
                )}
            </section>

            <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex items-center gap-2 text-sm font-semibold mb-2">
                    <ArrowUpDown className="w-4 h-4 text-amber-300" /> Unified Data Path Model
                </div>
                <div className="overflow-auto">
                    <table className="w-full text-xs">
                        <thead className="border-b border-border text-muted-foreground">
                            <tr>
                                <th className="text-left py-2 pr-2">Node</th>
                                <th className="text-right py-2 pr-2">Compute</th>
                                <th className="text-right py-2 pr-2">Network</th>
                                <th className="text-right py-2 pr-2">Storage</th>
                                <th className="text-right py-2 pr-2">Overall</th>
                                <th className="text-left py-2 pr-2">Bottleneck</th>
                                <th className="text-left py-2 pr-2">Hint</th>
                                <th className="text-right py-2">Action</th>
                            </tr>
                        </thead>
                        <tbody>
                            {(diagnostics?.data_paths ?? []).map((node) => {
                                const BottleneckIcon = bottleneckIcon(node.bottleneck);
                                return (
                                    <tr key={`path-${node.collector_id}`} className="border-b border-border/50">
                                        <td className="py-2 pr-2">
                                            <div className="font-medium">{node.hostname || node.collector_id}</div>
                                            <div className="text-[11px] text-muted-foreground">{node.collector_id}</div>
                                        </td>
                                        <td className="py-2 pr-2 text-right">{node.compute_score.toFixed(2)}</td>
                                        <td className="py-2 pr-2 text-right">{node.network_score.toFixed(2)}</td>
                                        <td className="py-2 pr-2 text-right">{node.storage_score.toFixed(2)}</td>
                                        <td className="py-2 pr-2 text-right font-semibold">{node.overall_score.toFixed(2)}</td>
                                        <td className="py-2 pr-2">
                                            <span className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wide ${severityClass(node.severity)}`}>
                                                <BottleneckIcon className="w-3 h-3" />
                                                {node.bottleneck}
                                            </span>
                                        </td>
                                        <td className="py-2 pr-2 text-[11px] text-muted-foreground">{(node.bottleneck_tip ?? []).slice(0, 1).join(' ') || '—'}</td>
                                        <td className="py-2 text-right">
                                            <button
                                                type="button"
                                                onClick={() => {
                                                    const resource = node.bottleneck === 'network'
                                                        ? 'network'
                                                        : node.bottleneck === 'storage'
                                                            ? 'storage'
                                                            : 'compute';
                                                    const metricKey = defaultMetricForDataPathResource(resource);
                                                    openTrends({
                                                        collectorId: node.collector_id,
                                                        category: categoryForDataPathResource(resource, metricKey),
                                                        metricKey,
                                                        triggerLabel: `Data path bottleneck (${node.hostname || node.collector_id})`,
                                                    });
                                                }}
                                                disabled={!canOpenTrends}
                                                className="text-[11px] px-2 py-1 rounded border border-amber-400/40 text-amber-200 hover:bg-amber-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                            >
                                                Trace in trends
                                            </button>
                                        </td>
                                    </tr>
                                );
                            })}
                        </tbody>
                    </table>
                </div>
            </section>

            {diagnosticsQuery.isError && (
                <div className="rounded-lg border border-rose-500/40 bg-rose-500/10 p-3 text-sm text-rose-200 flex items-center gap-2">
                    <ServerCrash className="w-4 h-4" />
                    Failed to load data path diagnostics.
                </div>
            )}
        </div>
    );
}

function formatAnomalyValue(anomaly: ResourceAnomaly): string {
    const metricName = anomaly.metric.toLowerCase();
    const value = anomaly.value;
    if (metricName.includes('collector_probe_core_last_frame_age_seconds')) {
        return `${value.toFixed(2)} s`;
    }
    if (metricName.includes('ratio')) {
        return value.toFixed(4);
    }
    if (metricName.includes('bytes_per_second')) {
        return formatRate(value);
    }
    if (metricName.includes('percent')) {
        return formatPercent(value);
    }
    if (metricName.includes('latency') || metricName.includes('_seconds')) {
        return formatMetricByUnit(value * 1000.0, 'milliseconds');
    }
    return formatCount(value);
}
