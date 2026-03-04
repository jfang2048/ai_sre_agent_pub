import { api } from './client';

export interface ControllerAuditRecord {
    id: string;
    actor: string;
    action: string;
    resource: string;
    status: string;
    input?: Record<string, string>;
    output?: string;
    evidence?: string[];
    occurred_at: string;
    workflow_id?: string;
    collector_id?: string;
    incident_id?: string;
    approval_gate: boolean;
}

export interface ControllerAuditResponse {
    records: ControllerAuditRecord[];
    count: number;
    timestamp: string;
}

export interface ControllerToolDescriptor {
    name: string;
    version: string;
    description: string;
    deterministic: boolean;
    read_only: boolean;
    requires_approval: boolean;
    supports_dry_run: boolean;
    supports_rollback: boolean;
    input_schema: string;
    output_schema: string;
}

export interface ControllerToolRegistryResponse {
    tools: ControllerToolDescriptor[];
    count: number;
    timestamp: string;
}

export interface ControllerRunRecord {
    run_id: string;
    workflow_type: string;
    status: string;
    collector_id?: string;
    trigger?: string;
    dry_run: boolean;
    requested_at: string;
    started_at?: string;
    completed_at?: string;
    workflow_id?: string;
    incident_id?: string;
    risk_level?: string;
    risk_score?: number;
    summary?: string;
    recommendations?: string[];
    evidence?: string[];
    error_message?: string;
}

export interface ControllerRunsResponse {
    runs: ControllerRunRecord[];
    count: number;
    timestamp: string;
}

export interface ControllerAuditQuery {
    actor?: string;
    status?: string;
    limit?: number;
}

function buildAuditQuery(query: ControllerAuditQuery = {}): string {
    const params = new URLSearchParams();
    if (query.actor?.trim()) {
        params.set('actor', query.actor.trim());
    }
    if (query.status?.trim()) {
        params.set('status', query.status.trim());
    }
    if (typeof query.limit === 'number' && query.limit > 0) {
        params.set('limit', String(query.limit));
    }
    return params.toString();
}

export async function fetchControllerAuditRecords(query: ControllerAuditQuery = {}): Promise<ControllerAuditResponse> {
    const suffix = buildAuditQuery(query);
    const { data } = await api.get<ControllerAuditResponse>(`/controller/audit${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchControllerToolRegistry(): Promise<ControllerToolRegistryResponse> {
    const { data } = await api.get<ControllerToolRegistryResponse>('/controller/tools');
    return data;
}

export async function fetchControllerRuns(limit = 100): Promise<ControllerRunsResponse> {
    const params = new URLSearchParams();
    if (limit > 0) {
        params.set('limit', String(limit));
    }
    const suffix = params.toString();
    const { data } = await api.get<ControllerRunsResponse>(`/controller/agent/runs${suffix ? `?${suffix}` : ''}`);
    return data;
}

