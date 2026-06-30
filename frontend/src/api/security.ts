import { api } from './client';
import { setPositiveIntParam, setQueryParam, toQuerySuffix } from './query';

export type SecuritySeverity = 'critical' | 'high' | 'medium' | 'low';

export interface SecurityFinding {
    id: string;
    severity: SecuritySeverity;
    category: string;
    scope: string;
    collector_id: string;
    summary: string;
    evidence: string[];
    recommended_action: string;
    score: number;
    observed_at: string;
    source: string;
}

export interface SecuritySummary {
    critical: number;
    high: number;
    medium: number;
    low: number;
}

export interface SecurityTrendPoint {
    timestamp: string;
    critical: number;
    high: number;
    medium: number;
    low: number;
    total: number;
}

export interface SecurityDashboardResponse {
    findings: SecurityFinding[];
    summary: SecuritySummary;
    trends: SecurityTrendPoint[];
    count: number;
    timestamp: string;
}

export interface SecurityQuery {
    collectorId?: string;
    window?: string;
    severity?: SecuritySeverity;
    category?: string;
    limit?: number;
}

function buildSecurityQuery(query: SecurityQuery = {}): string {
    const params = new URLSearchParams();
    setQueryParam(params, 'collector_id', query.collectorId);
    setQueryParam(params, 'window', query.window);
    setQueryParam(params, 'severity', query.severity);
    setQueryParam(params, 'category', query.category);
    setPositiveIntParam(params, 'limit', query.limit);
    return toQuerySuffix(params);
}

export async function fetchSecurityDashboard(query: SecurityQuery = {}): Promise<SecurityDashboardResponse> {
    const suffix = buildSecurityQuery(query);
    const { data } = await api.get<SecurityDashboardResponse>(`/security/dashboard${suffix}`);
    return data;
}
