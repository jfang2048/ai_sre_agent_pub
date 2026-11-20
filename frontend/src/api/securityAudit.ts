import { api } from './client';
import { setQueryParam, toQuerySuffix } from './query';

export type AuditFormat = 'sarif' | 'kubernetes';

export interface AuditScope {
    asset: string;
    window?: string;
}

export interface AuditRecord {
    id: string;
    asset: string;
    source: string;
    kind: string;
    rule: string;
    severity: string;
    observed_at: string;
    tool?: string;
    verb?: string;
    actor?: string;
    resource?: string;
    status?: number;
}

export interface AuditCorrelation {
    id: string;
    rule: string;
    asset: string;
    summary: string;
    evidence_ids: string[];
}

export interface AuditAnomaly {
    asset: string;
    bucket: string;
    observed: number;
    baseline: number;
    score: number;
    samples: number;
    evidence_ids: string[];
}

export interface AuditAnalysis {
    records: AuditRecord[];
    count: number;
    correlations: AuditCorrelation[];
    anomalies: AuditAnomaly[];
}

export interface AuditExplanation {
    mode: string;
    summary: string;
    evidence_ids: string[];
}

export async function fetchAuditAnalysis({ asset, window }: AuditScope): Promise<AuditAnalysis> {
    const params = new URLSearchParams();
    setQueryParam(params, 'asset', asset);
    setQueryParam(params, 'window', window);
    const { data } = await api.get<AuditAnalysis>(`/security/audit${toQuerySuffix(params)}`);
    return data;
}

export async function importAuditReport(input: {
    format: AuditFormat;
    asset: string;
    report: string;
    observed_at?: string;
}): Promise<{ imported: number; updated: number }> {
    const { data } = await api.post<{ imported: number; updated: number }>('/security/audit/import', input);
    return data;
}

export async function explainAudit(scope: AuditScope): Promise<AuditExplanation> {
    const { data } = await api.post<AuditExplanation>('/security/audit/explain', scope, { timeout: 20000 });
    return data;
}
