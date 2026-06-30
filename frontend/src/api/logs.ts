import { api } from './client';
import { setNonNegativeIntParam, setPositiveIntParam, setQueryParam, toQuerySuffix } from './query';

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
    setQueryParam(params, 'q', query.text);
    setQueryParam(params, 'collector_id', query.collectorId);
    setQueryParam(params, 'service', query.service);
    setQueryParam(params, 'process', query.process);
    setQueryParam(params, 'pid', query.pid);
    setQueryParam(params, 'level', query.level);
    setQueryParam(params, 'source', query.source);
    setQueryParam(params, 'since', query.since);
    setQueryParam(params, 'until', query.until);
    setQueryParam(params, 'window', query.window);
    setPositiveIntParam(params, 'limit', query.limit);
    setNonNegativeIntParam(params, 'offset', query.offset);
    setQueryParam(params, 'sort', query.sort);
    setPositiveIntParam(params, 'min_count', query.minCount);

    const { data } = await api.get<LogSearchResult>(`/logs/search${toQuerySuffix(params)}`);
    return data;
}

export async function fetchLogStatus(): Promise<LogStatusResponse> {
    const { data } = await api.get<LogStatusResponse>('/logs/status');
    return data;
}
