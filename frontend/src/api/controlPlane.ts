import { api } from './client';

export interface HAStatus {
    enabled: boolean;
    mode: string;
    active: boolean;
    read_only: boolean;
    timestamp: string;
    controller?: string;
}

export interface ControllerStatus {
    version: string;
    uptime: string;
    total_nodes: number;
    healthy_nodes: number;
    scrape_interval: string;
    listen_address: string;
    collector_coverage?: {
        state: string;
        total_collectors: number;
        fresh_collectors: number;
        delayed_collectors: number;
        stale_collectors: number;
        degraded_collectors: number;
        partial_collectors: number;
        fallback_collectors: number;
        backlog_collectors: number;
        coverage_percent: number;
        quality_hint?: string;
    };
}

export interface StorageStatus {
    storage: {
        nodes: number;
        history_series: number;
        history_samples: number;
        node_retention: string;
        history_samples_per_node: number;
        max_nodes: number;
        last_persist_error?: string;
        persistence: {
            enabled: boolean;
            path?: string;
            current_db_bytes?: number;
            max_db_bytes?: number;
            sync_interval?: string;
            last_sync_at?: string;
            last_sync_error?: string;
            last_compaction_at?: string;
            compactions?: number;
            compaction_interval?: string;
            compaction_every?: string;
        };
    };
    tsdb?: {
        enabled: boolean;
        provider: string;
        mode: string;
        ready: boolean;
        healthy: boolean;
        fallback_to_memory: boolean;
        fallback_active: boolean;
        manage_bucket: boolean;
        endpoint?: string;
        org?: string;
        bucket?: string;
        measurement?: string;
        retention?: string;
        write_batch_size?: number;
        write_queue_size?: number;
        queue_depth?: number;
        flush_interval?: string;
        query_timeout?: string;
        health_interval?: string;
        backup_directory?: string;
        dropped_batches?: number;
        last_write_at?: string;
        last_write_error?: string;
        last_query_at?: string;
        last_query_error?: string;
        last_health_at?: string;
        last_health_error?: string;
        degraded_reason?: string;
    };
    timestamp: string;
}

export interface StorageRetentionRequest {
    node_retention: string;
    history_samples_per_node: number;
}

export interface AgentStatus {
    status: string;
    reports: number;
    actions: number;
    joint_risk_reports: number;
    potential_risk_findings: number;
    rca_workflow_reports: number;
    timestamp: string;
    report_engine?: {
        enabled: boolean;
        suppress_unchanged_reports: boolean;
        report_refresh_interval: string;
        predictive_log_cooldown: string;
        reports_stored: number;
        actions_stored: number;
        report_suppressed_total: number;
        report_refreshed_total: number;
        predictive_log_suppressed_total: number;
    };
    control_plane?: {
        enabled: boolean;
        joint_risk_reports: number;
        rca_reports: number;
        incidents: number;
        high_risk_reports: number;
        latest_collector_id?: string;
        latest_joint_risk_at?: string;
        latest_rca_at?: string;
        latest_risk_level?: string;
        triggered_trends: number;
        investigation_events: number;
        weak_signal_clusters: number;
        retrieval_decisions: number;
        retrieval_skipped: number;
        recommendation_count: number;
        top_event_title?: string;
        probable_cause?: string;
        latest_incident_summary?: string;
        top_recommendation?: string;
        top_retrieval_intent?: string;
        top_retrieval_query?: string;
        top_retrieval_skip_reason?: string;
    };
    query_service?: {
        enabled: boolean;
        analysis_mode: string;
        provider: string;
        model: string;
        dry_run: boolean;
        require_approval_token: boolean;
        rag_attached: boolean;
        skip_llm_on_stale_telemetry: boolean;
        skip_llm_on_no_telemetry: boolean;
        max_telemetry_age: string;
        metrics: {
            StaleTelemetryTotal: number;
            LLMFailuresTotal: number;
            LLMBypassedStaleTotal: number;
            LLMBypassedEmptyTotal: number;
            FallbackTotal: number;
            ActionsSuppressedTotal: number;
            AnalysisReusedTotal: number;
            RAGSkippedContextTotal: number;
        };
    };
}

export interface FinOpsNodeSignal {
    collector_id: string;
    hostname: string;
    cpu_usage_percent: number;
    memory_usage_percent: number;
    gpu_utilization_percent: number;
    gpu_processes: number;
    idle_cpu_hint: boolean;
    oversized_memory_hint: boolean;
    gpu_waste_hint: boolean;
    potential_waste_score: number;
    recommendations?: string[];
}

export interface FinOpsSignalsResponse {
    summary: {
        nodes_analyzed: number;
        idle_cpu_hints: number;
        oversized_memory_hints: number;
        gpu_waste_hints: number;
        average_waste_score: number;
    };
    nodes: FinOpsNodeSignal[];
    count: number;
    generated_at: string;
}

export async function fetchHAStatus(): Promise<HAStatus> {
    const { data } = await api.get<HAStatus>('/ha/status');
    return data;
}

export async function fetchControllerStatus(): Promise<ControllerStatus> {
    const { data } = await api.get<ControllerStatus>('/status');
    return data;
}

export async function fetchStorageStatus(): Promise<StorageStatus> {
    const { data } = await api.get<StorageStatus>('/storage/status');
    return data;
}

export async function fetchStorageRetention(): Promise<StorageStatus> {
    const { data } = await api.get<StorageStatus>('/storage/retention');
    return data;
}

export async function updateStorageRetention(payload: StorageRetentionRequest): Promise<StorageStatus> {
    const { data } = await api.post<StorageStatus>('/storage/retention', payload);
    return data;
}

export async function fetchFinOpsSignals(): Promise<FinOpsSignalsResponse> {
    const { data } = await api.get<FinOpsSignalsResponse>('/finops/signals');
    return data;
}

export async function fetchAgentStatus(): Promise<AgentStatus> {
    const { data } = await api.get<AgentStatus>('/agent/status');
    return data;
}
