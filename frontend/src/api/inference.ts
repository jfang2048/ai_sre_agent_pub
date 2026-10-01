import { api } from './client';
import { setQueryParam, toQuerySuffix } from './query';

export interface InferenceMeasurement {
    value: number;
    unit: string;
    observed_at: string;
    window_seconds?: number;
}
export interface MonitoringFinding {
    code: string;
    severity: 'warning' | 'critical';
    summary: string;
    recommendation: string;
    evidence: string[];
}
export interface InferenceModel {
    name: string;
    engine?: string;
    observed_at: string;
    status: 'fresh' | 'stale' | 'unavailable';
    metrics: Record<string, InferenceMeasurement>;
    findings: MonitoringFinding[];
}
export interface InferenceEndpoint {
    collector_id: string;
    hostname: string;
    endpoint: string;
    status: 'fresh' | 'stale' | 'unavailable';
    last_attempt_at?: string;
    last_success_at?: string;
    models: InferenceModel[];
}
export interface InferenceGPU {
    collector_id: string;
    hostname: string;
    gpu_id: string;
    observed_at: string;
    status: 'fresh' | 'stale';
    metrics: Record<string, InferenceMeasurement>;
    findings: MonitoringFinding[];
}
export interface InferenceOverview {
    generated_at: string;
    stale_after_seconds: number;
    capacity_limited: boolean;
    endpoints: InferenceEndpoint[];
    gpus: InferenceGPU[];
}
export async function fetchInferenceOverview(collectorId?: string): Promise<InferenceOverview> {
    const query = new URLSearchParams();
    setQueryParam(query, 'collector_id', collectorId);
    const { data } = await api.get<InferenceOverview>(`/inference/overview${toQuerySuffix(query)}`);
    return data;
}
