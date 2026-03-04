import { api } from './client';

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
    if (query.collectorId?.trim()) {
        params.set('collector_id', query.collectorId.trim());
    }
    if (query.window?.trim()) {
        params.set('window', query.window.trim());
    }
    if (query.severity?.trim()) {
        params.set('severity', query.severity.trim());
    }
    if (query.category?.trim()) {
        params.set('category', query.category.trim());
    }
    if (typeof query.limit === 'number' && query.limit > 0) {
        params.set('limit', String(query.limit));
    }
    return params.toString();
}

export async function fetchSecurityDashboard(query: SecurityQuery = {}): Promise<SecurityDashboardResponse> {
    const suffix = buildSecurityQuery(query);
    const { data } = await api.get<SecurityDashboardResponse>(`/security/dashboard${suffix ? `?${suffix}` : ''}`);
    return data;
}

