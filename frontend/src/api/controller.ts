import { api } from './client';
import { setPositiveIntParam, setQueryParam, toQuerySuffix } from './query';

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
    purpose?: string;
    deterministic: boolean;
    read_only: boolean;
    requires_approval: boolean;
    supports_dry_run: boolean;
    supports_rollback: boolean;
    input_schema: string;
    output_schema: string;
    side_effects?: string;
    safety_class?: string;
    contract?: WorkflowToolContract;
}

export interface WorkflowToolContract {
    schema_version: string;
    tool_name: string;
    version: string;
    purpose: string;
    capability_family: string;
    allowed_stages: string[];
    allowed_runtime_contexts: string[];
    input_schema: string;
    output_schema: string;
    evidence_consumed: string[];
    evidence_produced: string[];
    determinism: string;
    read_only: boolean;
    state_changing: boolean;
    safety_class: string;
    side_effects?: string[];
    risks?: string[];
    timeout_budget: string;
    retry_policy: {
        max_attempts: number;
        retryable: boolean;
        retry_on_transient: boolean;
        retry_on_timeout: boolean;
        retry_requires_fresh_preconditions: boolean;
    };
    approval: {
        required: boolean;
        reason?: string;
        policy_gate?: string;
    };
    cost_class: string;
    confidence_impact: number;
    expected_information_gain: number;
    expected_information_profile?: string;
    eligible_for_auto_selection: boolean;
    preferred_query_hints?: string[];
    freshness_sensitivity?: string;
    scope_sensitivity?: string;
    replay_semantics: string;
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
    setQueryParam(params, 'actor', query.actor);
    setQueryParam(params, 'status', query.status);
    setPositiveIntParam(params, 'limit', query.limit);
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
    setPositiveIntParam(params, 'limit', limit);
    const { data } = await api.get<ControllerRunsResponse>(`/controller/agent/runs${toQuerySuffix(params)}`);
    return data;
}
