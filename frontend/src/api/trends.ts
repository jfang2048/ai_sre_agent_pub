import { api } from './client';

export interface FleetNode {
    collector_id: string;
    hostname: string;
    updated_at: string;
}

export interface StorageDeviceSample {
    device: string;
    partition?: string;
    parent_device?: string;
    scope: 'device' | 'partition' | string;
    last_updated_at?: string;
    read_bytes_total?: number;
    write_bytes_total?: number;
    read_bytes_per_second?: number;
    write_bytes_per_second?: number;
    read_iops?: number;
    write_iops?: number;
    iops?: number;
    in_flight_io?: number;
    queue_depth?: number;
    queue_capacity_requests?: number;
    queue_fill_percent?: number;
    inflight_fill_percent?: number;
    utilization_percent?: number;
    avg_read_latency_ms?: number;
    avg_write_latency_ms?: number;
    avg_request_latency_ms?: number;
}

export interface FilesystemSample {
    mountpoint: string;
    device?: string;
    fstype?: string;
    read_only?: boolean;
    size_bytes?: number;
    free_bytes?: number;
    avail_bytes?: number;
    used_bytes?: number;
    used_percent?: number;
    files_total?: number;
    files_free?: number;
    files_used?: number;
    files_used_percent?: number;
    last_updated_at?: string;
}

export interface FleetNodeDetails {
    collector_id: string;
    hostname: string;
    updated_at: string;
    metrics?: Record<string, number>;
    processes?: Array<{
        pid: number;
        name: string;
        cpu_percent: number;
        rss_bytes: number;
        io_read_bps: number;
        io_write_bps: number;
    }>;
    logs?: Array<{
        fingerprint: string;
        count: number;
        example: string;
    }>;
    storage_devices?: Record<string, StorageDeviceSample>;
    storage_partitions?: Record<string, StorageDeviceSample>;
    filesystems?: Record<string, FilesystemSample>;
}

export interface FleetResponse {
    nodes: FleetNode[];
    count: number;
    timestamp: string;
}

export interface TrendPoint {
    timestamp: string;
    value: number;
    is_anomaly?: boolean;
    z_score?: number;
}

export interface TrendSeries {
    key: string;
    display: string;
    unit: 'percent' | 'bytes_per_second' | 'count' | 'mib' | string;
    tier?: string;
    latest: number;
    min: number;
    max: number;
    avg: number;
    change_pct: number;
    spike_count: number;
    trend?: string;
    pattern?: string;
    sustained?: boolean;
    operational_hint?: string;
    points: TrendPoint[];
}

export interface FleetOperationalInsight {
    key: string;
    severity: 'critical' | 'warning' | 'advisory' | string;
    summary: string;
    decision: string;
    evidence?: string[];
}

export interface TelemetryQuality {
    state: 'fresh' | 'delayed' | 'stale' | 'unavailable' | 'degraded' | string;
    partial?: boolean;
    fallback_active?: boolean;
    coverage_percent?: number;
    confidence?: number;
    source_mode?: string;
    runtime_mode?: string;
    latest_collection_at?: string;
    latest_ingest_at?: string;
    query_at?: string;
    freshness_age_seconds?: number;
    ingest_delay_seconds?: number;
    missing_signals?: string[];
    degraded_reasons?: string[];
    quality_hint?: string;
}

export interface FleetTimeseriesResponse {
    collector_id: string;
    hostname: string;
    window: string;
    generated_at: string;
    latest_at?: string;
    sample_count: number;
    numeric_summary: Record<string, number>;
    series: TrendSeries[];
    telemetry_quality: TelemetryQuality;
    operational_insights?: FleetOperationalInsight[];
}

export interface FleetTimeseriesQuery {
    collectorId?: string;
    window?: string;
    limit?: number;
    metrics?: string[];
}

export async function fetchFleetNodes(): Promise<FleetResponse> {
    const { data } = await api.get<FleetResponse>('/fleet');
    return data;
}

export async function fetchFleetTimeseries(query: FleetTimeseriesQuery = {}): Promise<FleetTimeseriesResponse> {
    const params = new URLSearchParams();
    if (query.collectorId) {
        params.set('collector_id', query.collectorId);
    }
    if (query.window) {
        params.set('window', query.window);
    }
    if (query.limit && query.limit > 0) {
        params.set('limit', String(query.limit));
    }
    (query.metrics ?? []).forEach((metric) => {
        if (metric.trim()) {
            params.append('metric', metric.trim());
        }
    });

    const suffix = params.toString();
    const { data } = await api.get<FleetTimeseriesResponse>(`/fleet/timeseries${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchFleetNode(collectorId: string): Promise<FleetNodeDetails> {
    const { data } = await api.get<FleetNodeDetails>(`/fleet/${encodeURIComponent(collectorId)}`);
    return data;
}
