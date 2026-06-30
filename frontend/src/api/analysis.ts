import { api } from './client';
import { setPositiveIntParam, setQueryParam, toQuerySuffix } from './query';

export type AnalysisSeverity = 'critical' | 'warning' | 'info' | string;

export interface AnalysisAnomaly {
    node_name: string;
    metric_name: string;
    score: number;
    direction: string;
    current_value: number;
    expected_value: number;
    detected_at: string;
    reason: string;
}

export interface AnalysisCorrelation {
    node_name: string;
    metric_a: string;
    metric_b: string;
    coefficient: number;
    direction: string;
    lag: number;
    sample_count: number;
    scope?: string;
    entity_a?: string;
    entity_b?: string;
    detected_at: string;
}

export interface AnalysisSignalEvidence {
    source: string;
    signal: string;
    metric?: string;
    value?: number;
    expected?: number;
    trend?: string;
    details?: string;
}

export interface AnalysisIncidentReport {
    id: string;
    node_name: string;
    classification: string;
    severity: AnalysisSeverity;
    status: string;
    what_happened: string;
    probable_cause: string;
    confidence: number;
    impacted_components?: string[];
    supporting_signals?: AnalysisSignalEvidence[];
    correlated_metrics?: AnalysisCorrelation[];
    related_alert_ids?: string[];
    related_rca_id?: string;
    suggested_actions?: string[];
    primary_metric?: string;
    log_query?: string;
    window_start: string;
    window_end: string;
    generated_at: string;
}

export interface AnalysisStatus {
    status: string;
    config: {
        threshold_alerts: boolean;
        anomaly_detection: boolean;
        ml_anomaly_detection?: boolean;
        ml_method?: string;
        ml_score_threshold?: number;
        ml_seasonal_period?: number;
        correlation_analysis: boolean;
        cross_node_correlation?: boolean;
        llm_enabled: boolean;
        interval: string;
    };
    summary: {
        total_alerts: number;
        critical: number;
        warning: number;
        anomalies: number;
        rca_count: number;
        correlations?: number;
        incidents?: number;
    };
    timestamp: string;
}

export interface AnalysisIncidentsResponse {
    incidents: AnalysisIncidentReport[];
    count: number;
    classification_count?: Record<string, number>;
    timestamp: string;
}

export interface AnalysisAnomaliesResponse {
    anomalies: AnalysisAnomaly[];
    count: number;
    timestamp: string;
}

export interface AnalysisCorrelationsResponse {
    correlations: AnalysisCorrelation[];
    count: number;
    timestamp: string;
}

export async function fetchAnalysisStatus(): Promise<AnalysisStatus> {
    const { data } = await api.get<AnalysisStatus>('/analysis/status');
    return data;
}

export async function fetchAnalysisIncidents(params: { node?: string; limit?: number } = {}): Promise<AnalysisIncidentsResponse> {
    const query = new URLSearchParams();
    setQueryParam(query, 'collector_id', params.node);
    setPositiveIntParam(query, 'limit', params.limit);
    const { data } = await api.get<AnalysisIncidentsResponse>(`/analysis/incidents${toQuerySuffix(query)}`);
    return data;
}

export async function fetchAnalysisAnomalies(): Promise<AnalysisAnomaliesResponse> {
    const { data } = await api.get<AnalysisAnomaliesResponse>('/analysis/anomalies');
    return data;
}

export async function fetchAnalysisCorrelations(params: { node?: string } = {}): Promise<AnalysisCorrelationsResponse> {
    const query = new URLSearchParams();
    setQueryParam(query, 'collector_id', params.node);
    const { data } = await api.get<AnalysisCorrelationsResponse>(`/analysis/correlations${toQuerySuffix(query)}`);
    return data;
}
