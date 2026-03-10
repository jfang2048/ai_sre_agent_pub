import { api } from './client';

export interface LogEntry {
    id: number;
    timestamp: string;
    collector_id?: string;
    hostname?: string;
    service?: string;
    process?: string;
    pid?: string;
    level?: string;
    source?: string;
    message: string;
    fingerprint?: string;
    count?: number;
    labels?: Record<string, string>;
    metrics?: Record<string, number>;
}

export interface LogCountBucket {
    value: string;
    count: number;
}

export interface LogTimelineBucket {
    start: string;
    end: string;
    total: number;
    errors: number;
    warnings: number;
}

export interface LogMetricCorrelation {
    metric: string;
    samples: number;
    error_samples: number;
    baseline_avg: number;
    error_avg: number;
    uplift_percent: number;
    abs_uplift_score: number;
}

export interface LogSearchResult {
    generated_at: string;
    query: {
        text?: string;
        collector_id?: string;
        hostname?: string;
        service?: string;
        process?: string;
        pid?: string;
        level?: string;
        source?: string;
        since?: string;
        until?: string;
        limit?: number;
        offset?: number;
        sort?: string;
        min_count?: number;
    };
    total: number;
    returned: number;
    entries: LogEntry[];
    level_counts: LogCountBucket[];
    service_counts: LogCountBucket[];
    collector_counts: LogCountBucket[];
    timeline: LogTimelineBucket[];
    highlights: LogEntry[];
    metric_correlated: LogMetricCorrelation[];
}

export interface LogSearchQuery {
    text?: string;
    collectorId?: string;
    service?: string;
    process?: string;
    pid?: string;
    level?: string;
    source?: string;
    since?: string;
    until?: string;
    window?: string;
    limit?: number;
    offset?: number;
    sort?: 'asc' | 'desc';
    minCount?: number;
}

export interface LogIndexStats {
    retention: string;
    segment_duration: string;
    segments: number;
    entries: number;
    oldest_entry_at?: string;
    latest_entry_at?: string;
    ingested_events: number;
    ingested_lines: number;
    dropped_events: number;
    queries_total: number;
    last_query_at?: string;
    current_entry_bytes: number;
}

export interface LogStatusResponse {
    status: string;
    stats: LogIndexStats;
    timestamp: string;
}

export async function fetchLogs(query: LogSearchQuery = {}): Promise<LogSearchResult> {
    const params = new URLSearchParams();
    if (query.text?.trim()) params.set('q', query.text.trim());
    if (query.collectorId?.trim()) params.set('collector_id', query.collectorId.trim());
    if (query.service?.trim()) params.set('service', query.service.trim());
    if (query.process?.trim()) params.set('process', query.process.trim());
    if (query.pid?.trim()) params.set('pid', query.pid.trim());
    if (query.level?.trim()) params.set('level', query.level.trim());
    if (query.source?.trim()) params.set('source', query.source.trim());
    if (query.since?.trim()) params.set('since', query.since.trim());
    if (query.until?.trim()) params.set('until', query.until.trim());
    if (query.window?.trim()) params.set('window', query.window.trim());
    if (typeof query.limit === 'number' && query.limit > 0) params.set('limit', String(query.limit));
    if (typeof query.offset === 'number' && query.offset >= 0) params.set('offset', String(query.offset));
    if (query.sort) params.set('sort', query.sort);
    if (typeof query.minCount === 'number' && query.minCount > 0) params.set('min_count', String(query.minCount));

    const suffix = params.toString();
    const { data } = await api.get<LogSearchResult>(`/logs/search${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchLogStatus(): Promise<LogStatusResponse> {
    const { data } = await api.get<LogStatusResponse>('/logs/status');
    return data;
}
