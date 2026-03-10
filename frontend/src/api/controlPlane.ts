import { api } from './client';

export interface HAStatus {
    enabled: boolean;
    mode: string;
    active: boolean;
    read_only: boolean;
    timestamp: string;
    controller?: string;
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
