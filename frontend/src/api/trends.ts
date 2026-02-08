import { api } from './client';

export interface FleetNode {
    collector_id: string;
    hostname: string;
    updated_at: string;
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
    latest: number;
    min: number;
    max: number;
    avg: number;
    change_pct: number;
    spike_count: number;
    points: TrendPoint[];
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
