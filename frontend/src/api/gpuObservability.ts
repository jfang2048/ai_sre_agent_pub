import { api } from './client';
import { setPositiveIntParam, setQueryParam, toQuerySuffix } from './query';

export interface GPUMetricPoint {
    timestamp: string;
    value: number;
}

export interface GPUEventRecord {
    timestamp: string;
    collector_id?: string;
    hostname?: string;
    gpu_index?: string;
    event_type: string;
    severity: string;
    code?: string;
    count?: number;
    source?: string;
    message?: string;
}

export interface GPUProcessRow {
    pid: string;
    name?: string;
    mem_mib?: number;
    util_sm_percent?: number;
    util_mem_percent?: number;
    util_enc_percent?: number;
    util_dec_percent?: number;
    context_active?: number;
    context_type?: string;
}

export interface GPUDevice {
    gpu_index: string;
    uuid?: string;
    name?: string;
    util_sm_percent?: number;
    util_mem_percent?: number;
    util_enc_percent?: number;
    util_dec_percent?: number;
    mem_total_mib?: number;
    mem_used_mib?: number;
    mem_free_mib?: number;
    temp_c?: number;
    power_draw_w?: number;
    power_limit_w?: number;
    pcie_rx_mb_s?: number;
    pcie_tx_mb_s?: number;
    pcie_link_util_percent?: number;
    throttle_active?: number;
    xid_errors_total?: number;
    uvm_faults_total?: number;
    reset_events_total?: number;
    reliability_events_total?: number;
    kernel_hotspot_peak_sm_util_percent?: number;
    kernel_active_contexts?: number;
    processes?: GPUProcessRow[];
}

export interface GPUNode {
    collector_id: string;
    hostname: string;
    labels?: Record<string, string>;
    last_seen: string;
    gpu_count: number;
    gpus: Record<string, GPUDevice>;
    recent_events?: GPUEventRecord[];
}

export interface GPUNodesResponse {
    nodes: GPUNode[];
    count: number;
    timestamp: string;
}

export interface GPUTimelineResponse {
    collector_id: string;
    gpu_id: string;
    metric: string;
    window: string;
    count: number;
    points: GPUMetricPoint[];
    timestamp: string;
}

export interface GPUProcessTimelineResponse {
    collector_id: string;
    gpu_id: string;
    pid: string;
    metric: string;
    window: string;
    count: number;
    points: GPUMetricPoint[];
    timestamp: string;
}

export interface GPUEventsResponse {
    collector_id: string;
    gpu_id?: string;
    severity?: string;
    window: string;
    count: number;
    events: GPUEventRecord[];
    timestamp: string;
}

export interface GPUProcessesResponse {
    collector_id: string;
    gpu_id: string;
    sort_by?: string;
    count: number;
    processes: GPUProcessRow[];
    timestamp: string;
}

export interface GPUCorrelationResponse {
    collector_id: string;
    hostname: string;
    gpu: {
        gpu_count: number;
        avg_util_sm_percent: number;
        memory_pressure_percent: number;
        avg_pcie_link_util_percent: number;
        kernel_hotspot_peak_sm_util: number;
        context_count_total: number;
        throttle_active_devices: number;
        xid_errors_total: number;
        uvm_faults_total: number;
        reset_events_total: number;
    };
    host_pressure: {
        cpu_iowait_percent: number;
        disk_utilization_peak_percent: number;
        disk_latency_p99_ms: number;
        network_utilization_peak_percent: number;
        tcp_retransmit_ratio_percent: number;
    };
    scores: {
        starvation_risk: number;
        communication_risk: number;
        reliability_risk: number;
        overall_risk_percent: number;
    };
    risks: string[];
    timestamp: string;
}

export async function fetchGPUNodes(): Promise<GPUNodesResponse> {
    const { data } = await api.get<GPUNodesResponse>('/gpu/nodes');
    return data;
}

export async function fetchGPUTimeline(params: {
    collectorId: string;
    gpuId: string;
    metric: string;
    window?: string;
    limit?: number;
}): Promise<GPUTimelineResponse> {
    const search = new URLSearchParams();
    setQueryParam(search, 'collector_id', params.collectorId);
    setQueryParam(search, 'gpu_id', params.gpuId);
    setQueryParam(search, 'metric', params.metric);
    setQueryParam(search, 'window', params.window);
    setPositiveIntParam(search, 'limit', params.limit);
    const { data } = await api.get<GPUTimelineResponse>(`/gpu/timeline${toQuerySuffix(search)}`);
    return data;
}

export async function fetchGPUProcessTimeline(params: {
    collectorId: string;
    gpuId: string;
    pid: string;
    metric: string;
    window?: string;
    limit?: number;
}): Promise<GPUProcessTimelineResponse> {
    const search = new URLSearchParams();
    setQueryParam(search, 'collector_id', params.collectorId);
    setQueryParam(search, 'gpu_id', params.gpuId);
    setQueryParam(search, 'pid', params.pid);
    setQueryParam(search, 'metric', params.metric);
    setQueryParam(search, 'window', params.window);
    setPositiveIntParam(search, 'limit', params.limit);
    const { data } = await api.get<GPUProcessTimelineResponse>(`/gpu/process-timeline${toQuerySuffix(search)}`);
    return data;
}

export async function fetchGPUEvents(params: {
    collectorId: string;
    gpuId?: string;
    severity?: string;
    window?: string;
    limit?: number;
}): Promise<GPUEventsResponse> {
    const search = new URLSearchParams();
    setQueryParam(search, 'collector_id', params.collectorId);
    setQueryParam(search, 'gpu_id', params.gpuId);
    setQueryParam(search, 'severity', params.severity);
    setQueryParam(search, 'window', params.window);
    setPositiveIntParam(search, 'limit', params.limit);
    const { data } = await api.get<GPUEventsResponse>(`/gpu/events${toQuerySuffix(search)}`);
    return data;
}

export async function fetchGPUProcesses(params: {
    collectorId: string;
    gpuId: string;
    sortBy?: string;
    limit?: number;
}): Promise<GPUProcessesResponse> {
    const search = new URLSearchParams();
    setQueryParam(search, 'collector_id', params.collectorId);
    setQueryParam(search, 'gpu_id', params.gpuId);
    setQueryParam(search, 'sort_by', params.sortBy);
    setPositiveIntParam(search, 'limit', params.limit);
    const { data } = await api.get<GPUProcessesResponse>(`/gpu/processes${toQuerySuffix(search)}`);
    return data;
}

export async function fetchGPUCorrelation(collectorId: string): Promise<GPUCorrelationResponse> {
    const { data } = await api.get<GPUCorrelationResponse>(`/gpu/correlation?collector_id=${encodeURIComponent(collectorId)}`);
    return data;
}
