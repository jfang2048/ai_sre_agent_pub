import { api } from './client';

export interface WorkflowStageResult {
    name: string;
    status: string;
    summary: string;
    started_at: string;
    completed_at: string;
}

export interface WorkflowToolCall {
    id: string;
    tool: string;
    tool_version?: string;
    stage: string;
    collector_id?: string;
    window?: string;
    query?: Record<string, string>;
    status: string;
    summary?: string;
    started_at: string;
    completed_at: string;
    error_message?: string;
}

export interface AgentPlanStep {
    id: string;
    order: number;
    iteration: number;
    title: string;
    objective: string;
    tool: string;
    tool_version?: string;
    status: string;
    result_summary?: string;
    verified: boolean;
    verification_note?: string;
    evidence_ids?: string[];
    started_at?: string;
    completed_at?: string;
}

export interface AgentPlanRevision {
    iteration: number;
    reason: string;
    created_at: string;
    steps: AgentPlanStep[];
}

export interface AgentLoopSummary {
    mode: string;
    iterations: number;
    replans: number;
    steps_planned: number;
    steps_executed: number;
    steps_verified: number;
    completed: boolean;
    stop_reason: string;
    plan_steps: AgentPlanStep[];
    plan_revisions: AgentPlanRevision[];
}

export interface RCATimelineEvent {
    timestamp: string;
    phase: string;
    summary: string;
}

export interface RCAStructuredReport {
    symptoms: string[];
    timeline: RCATimelineEvent[];
    scope: string[];
    most_likely_cause: string;
    supporting_signals: string[];
    disconfirming_signals: string[];
    confidence: number;
}

export interface RiskSeriesPoint {
    timestamp: string;
    value: number;
}

export interface RiskSeries {
    key: string;
    display: string;
    unit: string;
    latest: number;
    baseline: number;
    acceleration: number;
    points: RiskSeriesPoint[];
}

export interface JointRiskSignal {
    id: string;
    name: string;
    scope: string;
    entity: string;
    severity: string;
    weight: number;
    current: number;
    baseline: number;
    delta_percent: number;
    acceleration: number;
    score: number;
    triggered: boolean;
    evidence?: string[];
    last_observed_at: string;
}

export interface JointRiskCooccurrence {
    id: string;
    scope: string;
    entity: string;
    window: string;
    signals: string[];
    correlation: number;
    combined_score: number;
    explanation: string;
    actionable_cause: string;
}

export interface ScopeRisk {
    scope: string;
    entity: string;
    score: number;
    top_signals?: string[];
    explanation: string;
}

export interface WorkflowRecommendation {
    id: string;
    priority: string;
    summary: string;
    details?: string;
    checks?: string[];
    safe: boolean;
    dry_run_default: boolean;
    requires_approval: boolean;
    reversible: boolean;
    rollback_hint?: string;
}

export interface WorkflowInsights {
    enabled: boolean;
    provider: string;
    model: string;
    api_key_env: string;
    api_key_configured: boolean;
    mode: string;
}

export interface JointRiskReport {
    workflow_id: string;
    pipeline_version: string;
    collector_id?: string;
    scope: string;
    window: string;
    generated_at: string;
    risk_score: number;
    risk_level: string;
    summary: string;
    actionable_why: string;
    signals: JointRiskSignal[];
    cooccurrences: JointRiskCooccurrence[];
    scope_risks: ScopeRisk[];
    series: RiskSeries[];
    recommendations: WorkflowRecommendation[];
    stages: WorkflowStageResult[];
    tool_calls: WorkflowToolCall[];
    limitations?: string[];
    insights: WorkflowInsights;
}

export interface RCAEvidence {
    id: string;
    kind: string;
    source: string;
    scope?: string;
    entity?: string;
    summary: string;
    metric_name?: string;
    value?: number;
    baseline?: number;
    delta?: number;
    snippet?: string;
    timestamp?: string;
}

export interface RCAHypothesis {
    id: string;
    rank: number;
    title: string;
    confidence: number;
    description: string;
    evidence_ids?: string[];
}

export interface RCACorrelation {
    id: string;
    scope: string;
    entity: string;
    signals: string[];
    coefficient: number;
    summary: string;
}

export interface RCAContext {
    collector_id?: string;
    window: string;
    top_metrics: Record<string, number>;
    top_processes: string[];
    kernel_signals: string[];
    recent_deploys?: string[];
    security_findings?: string[];
    topology_summary?: string;
}

export interface RCAReport {
    workflow_id: string;
    pipeline_version: string;
    incident_id: string;
    status: string;
    collector_id?: string;
    trigger: string;
    generated_at: string;
    context: RCAContext;
    anomalies: string[];
    correlations: RCACorrelation[];
    hypotheses: RCAHypothesis[];
    evidence: RCAEvidence[];
    recommendations: WorkflowRecommendation[];
    agent_loop: AgentLoopSummary;
    structured_report: RCAStructuredReport;
    stages: WorkflowStageResult[];
    tool_calls: WorkflowToolCall[];
    reproducibility: Record<string, string>;
    limitations?: string[];
    insights: WorkflowInsights;
}

export interface PotentialRiskSignal {
    name: string;
    scope: string;
    entity: string;
    severity: string;
    current: number;
    baseline: number;
    delta_percent: number;
    score: number;
    evidence?: string[];
}

export interface PotentialRiskFinding {
    id: string;
    collector_id?: string;
    risk_summary: string;
    time_window: string;
    scope: string;
    confidence_score: number;
    contributing_signals: PotentialRiskSignal[];
    suggested_investigation_steps: string[];
    correlations: JointRiskCooccurrence[];
    series: RiskSeries[];
    generated_at: string;
}

export interface WorkflowAuditRecord {
    id: string;
    workflow_id: string;
    workflow_type: string;
    stage: string;
    action: string;
    tool?: string;
    collector_id?: string;
    dry_run: boolean;
    requires_approval: boolean;
    approved: boolean;
    status: string;
    input?: Record<string, string>;
    output_summary?: string;
    error_message?: string;
    timestamp: string;
}

export interface JointRiskResponse {
    reports: JointRiskReport[];
    count: number;
    timestamp: string;
}

export interface RCAResponse {
    reports: RCAReport[];
    count: number;
    timestamp: string;
}

export interface WorkflowAuditResponse {
    records: WorkflowAuditRecord[];
    count: number;
    timestamp: string;
}

export interface AgentIncidentReport {
    incident_id: string;
    workflow_id: string;
    status: string;
    source: string;
    collector_id?: string;
    opened_at: string;
    closed_at?: string;
    risk_level: string;
    risk_score: number;
    summary: string;
    most_likely_cause: string;
    confidence: number;
    symptoms: string[];
    timeline: RCATimelineEvent[];
    evidence: RCAEvidence[];
    hypotheses: RCAHypothesis[];
    recommendations: WorkflowRecommendation[];
    agent_loop: AgentLoopSummary;
}

export interface AgentIncidentListResponse {
    incidents: AgentIncidentReport[];
    count: number;
    timestamp: string;
}

export interface AgentIncidentDetailResponse {
    incident: AgentIncidentReport;
    timestamp: string;
}

export interface PotentialRiskResponse {
    findings: PotentialRiskFinding[];
    count: number;
    timestamp: string;
}

export interface WorkflowQuery {
    collectorId?: string;
    window?: string;
    limit?: number;
    trigger?: string;
    status?: string;
    dryRun?: boolean;
    refresh?: boolean;
}

function buildQueryString(query: WorkflowQuery = {}): string {
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
    if (query.trigger) {
        params.set('trigger', query.trigger);
    }
    if (query.status) {
        params.set('status', query.status);
    }
    if (typeof query.dryRun === 'boolean') {
        params.set('dry_run', query.dryRun ? 'true' : 'false');
    }
    if (typeof query.refresh === 'boolean') {
        params.set('refresh', query.refresh ? 'true' : 'false');
    }
    return params.toString();
}

export async function fetchJointRiskReports(query: WorkflowQuery = {}): Promise<JointRiskResponse> {
    const suffix = buildQueryString(query);
    const { data } = await api.get<JointRiskResponse>(`/agent/joint-risk${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchRCAWorkflowReports(query: WorkflowQuery = {}): Promise<RCAResponse> {
    const suffix = buildQueryString(query);
    const { data } = await api.get<RCAResponse>(`/agent/rca${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchPotentialRiskFindings(query: WorkflowQuery = {}): Promise<PotentialRiskResponse> {
    const suffix = buildQueryString(query);
    const { data } = await api.get<PotentialRiskResponse>(`/agent/potential-risks${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchWorkflowAuditRecords(limit = 50, workflowID = ''): Promise<WorkflowAuditResponse> {
    const params = new URLSearchParams();
    if (limit > 0) {
        params.set('limit', String(limit));
    }
    if (workflowID.trim()) {
        params.set('workflow_id', workflowID.trim());
    }
    const suffix = params.toString();
    const { data } = await api.get<WorkflowAuditResponse>(`/agent/workflow/audit${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchWorkflowIncidents(query: WorkflowQuery = {}): Promise<AgentIncidentListResponse> {
    const suffix = buildQueryString(query);
    const { data } = await api.get<AgentIncidentListResponse>(`/agent/workflow/incidents${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchWorkflowIncidentByID(incidentID: string): Promise<AgentIncidentDetailResponse> {
    const id = incidentID.trim();
    if (!id) {
        throw new Error('incident id is required');
    }
    const { data } = await api.get<AgentIncidentDetailResponse>(`/agent/workflow/incidents/${encodeURIComponent(id)}`);
    return data;
}
