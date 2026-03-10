import { api } from './client';
import type { ProgramStats } from './topPrograms';

export interface ResourcePressureRow {
    collector_id: string;
    hostname: string;
    score: number;
    severity: 'healthy' | 'degraded' | 'critical' | string;
    signals?: Record<string, number>;
    factors?: string[];
}

export interface ResourceAnomaly {
    collector_id: string;
    hostname: string;
    resource: 'network' | 'storage' | 'probe_core' | string;
    metric: string;
    value: number;
    baseline: number;
    z_score: number;
    severity: 'healthy' | 'degraded' | 'critical' | string;
}

export interface DataPathNodeModel {
    collector_id: string;
    hostname: string;
    compute_score: number;
    network_score: number;
    storage_score: number;
    overall_score: number;
    severity: 'healthy' | 'degraded' | 'critical' | string;
    bottleneck: 'compute' | 'network' | 'storage' | string;
    bottleneck_tip?: string[];
    runtime_mode?: 'host' | 'namespace' | 'limited' | string;
    runtime_degraded?: boolean;
    runtime_reasons?: string[];
}

export interface DataPathResourceDiagnostics {
    cluster_health_score: number;
    rankings: ResourcePressureRow[];
    anomalies: ResourceAnomaly[];
    top_processes?: ProgramStats[];
}

export interface DataPathDiagnosticsSummary {
    node_count: number;
    network_critical: number;
    network_degraded: number;
    storage_critical: number;
    storage_degraded: number;
    probe_core_critical?: number;
    probe_core_degraded?: number;
    probe_core_fallback_nodes?: number;
    probe_core_invalid_config_nodes?: number;
    runtime_namespace_nodes?: number;
    runtime_limited_nodes?: number;
    runtime_degraded_nodes?: number;
    total_anomalies: number;
    critical_data_paths: number;
}

export interface DataPathDiagnosticsResponse {
    collector_id?: string;
    generated_at: string;
    summary: DataPathDiagnosticsSummary;
    network: DataPathResourceDiagnostics;
    storage: DataPathResourceDiagnostics;
    probe_core?: DataPathResourceDiagnostics;
    data_paths: DataPathNodeModel[];
}

export interface RootCauseDiagnosticsSummary {
    node_count: number;
    finding_count: number;
    critical_findings: number;
    degraded_findings: number;
    top_finding_id?: string;
    top_finding_summary?: string;
}

export interface RootCauseDiagnosticsDataPathJoin {
    network_critical: number;
    storage_critical: number;
    probe_core_critical: number;
    total_anomalies: number;
}

export interface RootCauseDiagnosticsNode {
    collector_id: string;
    hostname: string;
}

export interface RootCauseDiagnosticsEvidence {
    collector_id: string;
    hostname: string;
    signal: string;
    value: number;
    baseline?: number;
    z_score?: number;
    source?: string;
    note?: string;
}

export interface RootCauseDiagnosticsFinding {
    id: string;
    category: string;
    severity: 'healthy' | 'degraded' | 'critical' | string;
    confidence: number;
    title: string;
    hypothesis: string;
    impact: string;
    affected_nodes?: RootCauseDiagnosticsNode[];
    correlated_signals?: string[];
    evidence?: RootCauseDiagnosticsEvidence[];
    actions?: string[];
    metadata?: Record<string, number>;
    tags?: Record<string, string>;
}

export interface RootCauseDiagnosticsResponse {
    collector_id?: string;
    generated_at: string;
    summary: RootCauseDiagnosticsSummary;
    findings: RootCauseDiagnosticsFinding[];
    data_path: RootCauseDiagnosticsDataPathJoin;
}

export interface AIInfraLayerMeasurement {
    name: string;
    metric: string;
    source: string;
    status: 'measured' | 'partial' | 'missing' | string;
    method?: 'direct' | 'derived' | 'proxy' | 'missing' | string;
    note?: string;
}

export interface AIInfraLayerDomain {
    id: string;
    title: string;
    score: number;
    severity: 'healthy' | 'degraded' | 'critical' | string;
    coverage_percent: number;
    signals?: Record<string, number>;
    sources?: Record<string, string>;
    notes?: string[];
}

export interface AIInfraRankedEntity {
    kind: string;
    id: string;
    label: string;
    score: number;
    severity?: 'healthy' | 'degraded' | 'critical' | string;
    detail?: string;
}

export interface AIInfraLayerDiagnostics {
    id: string;
    title: string;
    scope: string;
    score: number;
    severity: 'healthy' | 'degraded' | 'critical' | string;
    coverage_percent: number;
    signals?: Record<string, number>;
    top_risks?: string[];
    sources?: Record<string, string>;
    measurements?: AIInfraLayerMeasurement[];
    domains?: AIInfraLayerDomain[];
    ranked_entities?: AIInfraRankedEntity[];
    troubleshooting?: string[];
    observability_gaps?: string[];
}

export interface AIInfraStackSummary {
    node_count: number;
    workload_count: number;
    layer_count: number;
    critical_layers: number;
    degraded_layers: number;
    top_layer_id?: string;
    top_layer_title?: string;
    top_risk?: string;
    coverage_percent: number;
    root_cause_findings: number;
    critical_findings: number;
    degraded_findings: number;
    communication_skews: number;
    incident_drilldowns?: number;
    measurements_measured?: number;
    measurements_partial?: number;
    measurements_missing?: number;
    methods_direct?: number;
    methods_derived?: number;
    methods_proxy?: number;
    methods_missing?: number;
}

export interface AIInfraWorkloadMapping {
    cluster: string;
    namespace: string;
    kind: string;
    name: string;
    service: string;
    path: string;
    pods_running: number;
    pods_pending: number;
    pods_failed: number;
    gpu_requests?: number;
    gpu_limits?: number;
    node_count: number;
    resolved_nodes: number;
    nodes?: string[];
    risk_flags?: string[];
    bottleneck?: string;
}

export interface AIInfraIncidentWorkloadHop {
    id: string;
    cluster?: string;
    namespace?: string;
    kind?: string;
    name?: string;
    service?: string;
    severity?: 'healthy' | 'degraded' | 'critical' | string;
    bottleneck?: 'compute' | 'network' | 'storage' | string;
    queue_delay_seconds?: number;
    pods_pending?: number;
    pods_failed?: number;
    node_count?: number;
    resolved_nodes?: number;
    gpu_requests?: number;
    risks?: string[];
    reason?: string;
}

export interface AIInfraIncidentPlacementHop {
    workload_id?: string;
    node_id?: string;
    collector_id?: string;
    hostname?: string;
    cluster?: string;
    zone?: string;
    score: number;
    severity?: 'healthy' | 'degraded' | 'critical' | string;
    queue_delay_seconds?: number;
    signals?: Record<string, number>;
    reason?: string;
}

export interface AIInfraIncidentSignal {
    name: string;
    value: number;
    source?: string;
    collector_id?: string;
    hostname?: string;
}

export interface AIInfraIncidentDrilldown {
    finding_id: string;
    finding_title: string;
    category: string;
    severity: 'healthy' | 'degraded' | 'critical' | string;
    confidence: number;
    workflow: string;
    affected_nodes?: string[];
    workloads?: AIInfraIncidentWorkloadHop[];
    placements?: AIInfraIncidentPlacementHop[];
    contention?: AIInfraIncidentSignal[];
    triage?: string[];
}

export interface AIInfraStackDiagnosticsResponse {
    collector_id?: string;
    cluster?: string;
    namespace?: string;
    service?: string;
    generated_at: string;
    summary: AIInfraStackSummary;
    layers: AIInfraLayerDiagnostics[];
    workload_mappings?: AIInfraWorkloadMapping[];
    incident_drilldowns?: AIInfraIncidentDrilldown[];
}

export interface KernelPathStageDiagnostics {
    name: string;
    score: number;
    severity: 'healthy' | 'degraded' | 'critical' | string;
    signals?: Record<string, number>;
    sources?: Record<string, string>;
    notes?: string[];
}

export interface KernelPathDomainDiagnostics {
    score: number;
    severity: 'healthy' | 'degraded' | 'critical' | string;
    top_stage?: string;
    stages: KernelPathStageDiagnostics[];
}

export interface KernelPathNodeDiagnostics {
    collector_id: string;
    hostname: string;
    storage: KernelPathDomainDiagnostics;
    network: KernelPathDomainDiagnostics;
    overall_severity: 'healthy' | 'degraded' | 'critical' | string;
    bottlenecks?: string[];
}

export interface KernelPathDiagnosticsSummary {
    node_count: number;
    critical_nodes: number;
    degraded_nodes: number;
    top_storage_stage?: string;
    top_network_stage?: string;
    top_bottleneck_key?: string;
}

export interface KernelPathDiagnosticsResponse {
    collector_id?: string;
    generated_at: string;
    summary: KernelPathDiagnosticsSummary;
    nodes: KernelPathNodeDiagnostics[];
}

export interface WorkloadPathNodeDiagnostics {
    node_name: string;
    collector_id?: string;
    hostname?: string;
    telemetry_available: boolean;
    compute_score: number;
    network_score: number;
    storage_score: number;
    overall_score: number;
    severity: 'healthy' | 'degraded' | 'critical' | 'unknown' | string;
    bottleneck?: string;
    top_storage_stage?: string;
    top_network_stage?: string;
    signals?: Record<string, number>;
    sources?: Record<string, string>;
    reasons?: string[];
}

export interface WorkloadPathDiagnosticsWorkload {
    cluster: string;
    namespace: string;
    kind: string;
    name: string;
    service: string;
    pods_total: number;
    pods_running: number;
    pods_pending: number;
    pods_failed: number;
    container_restarts: number;
    gpu_requests?: number;
    gpu_limits?: number;
    node_count: number;
    resolved_nodes: number;
    telemetry_coverage_percent: number;
    compute_score: number;
    network_score: number;
    storage_score: number;
    overall_score: number;
    severity: 'healthy' | 'degraded' | 'critical' | 'unknown' | string;
    bottleneck: 'compute' | 'network' | 'storage' | string;
    top_storage_stage?: string;
    top_network_stage?: string;
    signals?: Record<string, number>;
    sources?: Record<string, string>;
    risks?: string[];
    reasons?: string[];
    nodes?: WorkloadPathNodeDiagnostics[];
}

export interface WorkloadPathDiagnosticsSummary {
    workload_count: number;
    critical_workloads: number;
    degraded_workloads: number;
    telemetry_covered_workloads: number;
    multi_node_workloads: number;
    gpu_starvation_risk_workloads: number;
    communication_imbalance_workloads: number;
    top_bottleneck?: string;
}

export interface WorkloadPathDiagnosticsResponse {
    cluster?: string;
    namespace?: string;
    service?: string;
    generated_at: string;
    summary: WorkloadPathDiagnosticsSummary;
    workloads: WorkloadPathDiagnosticsWorkload[];
}

export interface RCAPacketExportSummary {
    root_cause_findings: number;
    critical_findings: number;
    degraded_findings: number;
    kernel_nodes: number;
    workloads: number;
    network_ranked: number;
    storage_ranked: number;
    probe_core_ranked: number;
}

export interface RCAPacketExportSourceMetadata {
    data_path_endpoint: string;
    kernel_path_endpoint: string;
    root_cause_endpoint: string;
    workload_path_endpoint: string;
}

export interface RCAPacketExportResponse {
    collector_id?: string;
    cluster?: string;
    namespace?: string;
    service?: string;
    sort_key: string;
    sort_direction: string;
    format: string;
    workload_limit: number;
    generated_at: string;
    file_name: string;
    markdown: string;
    packet_sha256: string;
    content_bytes: number;
    packet_signature?: string;
    packet_signature_algorithm?: string;
    packet_signature_key_id?: string;
    summary: RCAPacketExportSummary;
    source_metadata: RCAPacketExportSourceMetadata;
}

export interface DataPathDiagnosticsQuery {
    collectorId?: string;
}

export interface WorkloadPathDiagnosticsQuery {
    cluster?: string;
    namespace?: string;
    service?: string;
    limit?: number;
}

export interface AIInfraStackDiagnosticsQuery {
    collectorId?: string;
    cluster?: string;
    namespace?: string;
    service?: string;
    workloadLimit?: number;
}

export interface RCAPacketExportQuery {
    collectorId?: string;
    cluster?: string;
    namespace?: string;
    service?: string;
    sortKey?: 'severity' | 'overall' | 'coverage' | 'network' | 'storage' | string;
    sortDirection?: 'asc' | 'desc' | string;
    workloadLimit?: number;
}

export async function fetchDataPathDiagnostics(query: DataPathDiagnosticsQuery = {}): Promise<DataPathDiagnosticsResponse> {
    const params = new URLSearchParams();
    if (query.collectorId?.trim()) {
        params.set('collector_id', query.collectorId.trim());
    }
    const suffix = params.toString();
    const { data } = await api.get<DataPathDiagnosticsResponse>(`/diagnostics/data-path${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchRootCauseDiagnostics(query: DataPathDiagnosticsQuery = {}): Promise<RootCauseDiagnosticsResponse> {
    const params = new URLSearchParams();
    if (query.collectorId?.trim()) {
        params.set('collector_id', query.collectorId.trim());
    }
    const suffix = params.toString();
    const { data } = await api.get<RootCauseDiagnosticsResponse>(`/diagnostics/root-cause${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchKernelPathDiagnostics(query: DataPathDiagnosticsQuery = {}): Promise<KernelPathDiagnosticsResponse> {
    const params = new URLSearchParams();
    if (query.collectorId?.trim()) {
        params.set('collector_id', query.collectorId.trim());
    }
    const suffix = params.toString();
    const { data } = await api.get<KernelPathDiagnosticsResponse>(`/diagnostics/kernel-path${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchWorkloadPathDiagnostics(query: WorkloadPathDiagnosticsQuery = {}): Promise<WorkloadPathDiagnosticsResponse> {
    const params = new URLSearchParams();
    if (query.cluster?.trim()) {
        params.set('cluster', query.cluster.trim());
    }
    if (query.namespace?.trim()) {
        params.set('namespace', query.namespace.trim());
    }
    if (query.service?.trim()) {
        params.set('service', query.service.trim());
    }
    if (typeof query.limit === 'number' && Number.isFinite(query.limit) && query.limit > 0) {
        params.set('limit', String(Math.floor(query.limit)));
    }
    const suffix = params.toString();
    const { data } = await api.get<WorkloadPathDiagnosticsResponse>(`/diagnostics/workload-path${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchAIInfraStackDiagnostics(query: AIInfraStackDiagnosticsQuery = {}): Promise<AIInfraStackDiagnosticsResponse> {
    const params = new URLSearchParams();
    if (query.collectorId?.trim()) {
        params.set('collector_id', query.collectorId.trim());
    }
    if (query.cluster?.trim()) {
        params.set('cluster', query.cluster.trim());
    }
    if (query.namespace?.trim()) {
        params.set('namespace', query.namespace.trim());
    }
    if (query.service?.trim()) {
        params.set('service', query.service.trim());
    }
    if (typeof query.workloadLimit === 'number' && Number.isFinite(query.workloadLimit) && query.workloadLimit > 0) {
        params.set('workload_limit', String(Math.floor(query.workloadLimit)));
    }
    const suffix = params.toString();
    const { data } = await api.get<AIInfraStackDiagnosticsResponse>(`/diagnostics/ai-infra-stack${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchRCAPacketExport(query: RCAPacketExportQuery = {}): Promise<RCAPacketExportResponse> {
    const params = new URLSearchParams();
    if (query.collectorId?.trim()) {
        params.set('collector_id', query.collectorId.trim());
    }
    if (query.cluster?.trim()) {
        params.set('cluster', query.cluster.trim());
    }
    if (query.namespace?.trim()) {
        params.set('namespace', query.namespace.trim());
    }
    if (query.service?.trim()) {
        params.set('service', query.service.trim());
    }
    if (query.sortKey?.trim()) {
        params.set('sort_key', query.sortKey.trim());
    }
    if (query.sortDirection?.trim()) {
        params.set('sort_direction', query.sortDirection.trim());
    }
    if (typeof query.workloadLimit === 'number' && Number.isFinite(query.workloadLimit) && query.workloadLimit > 0) {
        params.set('workload_limit', String(Math.floor(query.workloadLimit)));
    }
    const suffix = params.toString();
    const { data } = await api.get<RCAPacketExportResponse>(`/diagnostics/rca-packet${suffix ? `?${suffix}` : ''}`);
    return data;
}
