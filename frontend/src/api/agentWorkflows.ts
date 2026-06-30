import { api } from './client';
import { requireTrimmedString, setBooleanParam, setPositiveIntParam, setQueryParam, toQuerySuffix } from './query';

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
    tool_contract?: string;
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

export interface IncidentGroupedSignal {
    signal_id: string;
    signal_type: string;
    source: string;
    scope: string;
    entity: string;
    severity: string;
    score: number;
    summary: string;
    evidence_ids?: string[];
    last_observed?: string;
}

export interface IncidentSynthesis {
    incident_id: string;
    summary: string;
    grouped_signals: IncidentGroupedSignal[];
    impacted_scope: string[];
    time_window: {
        start: string;
        end: string;
    };
    severity: string;
    confidence: number;
    candidate_root_cause_cluster?: string;
    correlation_reasons?: string[];
    top_offenders?: string[];
    timeline_transitions?: string[];
    topology_neighborhood?: string[];
}

export interface AgentPlanStep {
    id: string;
    order: number;
    iteration: number;
    title: string;
    objective: string;
    tool: string;
    required: boolean;
    tool_version?: string;
    status: string;
    result_summary?: string;
    verified: boolean;
    verification_note?: string;
    evidence_ids?: string[];
    superseded_by?: string;
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

export interface AdaptiveProgressAssessment {
    schema_version: string;
    tool_call_id?: string;
    uncertainty_before: number;
    uncertainty_after: number;
    uncertainty_delta: number;
    confidence_before: number;
    confidence_after: number;
    confidence_delta: number;
    contradictions_before: number;
    contradictions_after: number;
    contradiction_delta: number;
    evidence_gaps_before: number;
    evidence_gaps_after: number;
    evidence_gap_coverage_delta: number;
    risk_before: number;
    risk_after: number;
    risk_delta: number;
    action_effect_delta: number;
    progress: boolean;
    plateau: boolean;
    summary: string;
}

export interface AdaptiveRuntimeState {
    schema_version: string;
    runtime_mode: string;
    objective: string;
    subgoals?: string[];
    unresolved_evidence_gaps?: string[];
    scope_hints?: string[];
    confidence_score: number;
    risk_score: number;
    execution_posture: string;
    approval_status: string;
    iteration: number;
    tool_calls: number;
    hypothesis_rewrites: number;
    latest_progress?: AdaptiveProgressAssessment;
    hard_stop: boolean;
    stop_reason?: string;
}

export interface AdaptiveDialogueTurn {
    turn_id: string;
    iteration: number;
    role: string;
    producer: string;
    consumer?: string;
    summary: string;
    inputs?: string[];
    outputs?: string[];
    tool_decision_id?: string;
    artifact_id?: string;
    created_at: string;
}

export interface AdaptiveToolDecision {
    decision_id: string;
    schema_version: string;
    iteration: number;
    tool: string;
    tool_contract: string;
    capability_family: string;
    reason: string;
    evidence_gap_covered?: string[];
    executable: boolean;
    auto_selected: boolean;
    proposal_only?: boolean;
    blocked_reason?: string;
    tool_call_id?: string;
    outcome?: string;
    progress?: AdaptiveProgressAssessment;
    normalized_result?: NormalizedToolResult;
    stop_reason?: string;
}

export interface NormalizedToolResult {
    schema_version: string;
    tool: string;
    tool_call_id?: string;
    summary: string;
    structured_findings?: string[];
    confidence_contribution?: number;
    contradiction_contribution?: number;
    evidence_ids?: string[];
    affected_scope?: string[];
    freshness?: string;
    likely_next_tool_families?: string[];
    likely_next_checks?: string[];
    narrows_hypothesis_space?: boolean;
    remediation_eligibility_delta?: number;
}

export interface AdaptiveArtifact {
    schema_version: string;
    version: string;
    kind: string;
    artifact_id: string;
    run_id: string;
    incident_id?: string;
    correlation_id?: string;
    producer: string;
    consumer?: string;
    status: string;
    iteration?: number;
    replayable: boolean;
    replay_semantics: string;
    summary: string;
    payload?: Record<string, unknown>;
    produced_at: string;
}

export interface RCATimelineEvent {
    timestamp: string;
    phase: string;
    summary: string;
}

export interface RCAStructuredReport {
    incident_summary?: string;
    symptoms: string[];
    timeline: RCATimelineEvent[];
    scope: string[];
    most_likely_cause: string;
    supporting_signals: string[];
    disconfirming_signals: string[];
    confidence: number;
    unresolved_gaps?: string[];
    recommended_next_steps?: string[];
    safe_remediations?: string[];
}

export interface RiskSeriesPoint {
    timestamp: string;
    value: number;
}

export interface RiskSeries {
    key: string;
    display: string;
    unit: string;
    category?: string;
    latest: number;
    baseline: number;
    delta_percent?: number;
    slope_per_minute?: number;
    acceleration: number;
    threshold_breaches?: number;
    persistence_points?: number;
    trend?: string;
    triggered?: boolean;
    forecast?: string;
    forecast_value?: number;
    threshold_value?: number;
    points: RiskSeriesPoint[];
}

export interface TrendAssessment {
    id: string;
    series_key: string;
    display: string;
    category?: string;
    scope: string;
    entity: string;
    trend: string;
    severity: string;
    confidence: number;
    detection_mode?: string;
    latest: number;
    baseline: number;
    delta_percent: number;
    slope_per_minute?: number;
    acceleration?: number;
    threshold_breaches?: number;
    persistence_points?: number;
    threshold_value?: number;
    forecast?: string;
    forecast_value?: number;
    triggered: boolean;
    summary: string;
    operator_hint?: string;
    last_observed_at: string;
}

export interface InvestigationEvent {
    id: string;
    category: string;
    severity: string;
    confidence: number;
    scope: string;
    entity: string;
    title: string;
    symptom: string;
    probable_cause?: string;
    summary: string;
    supporting_signals?: string[];
    evidence?: string[];
    recommended_checks?: string[];
    retrieval_hint?: string;
}

export interface RetrievalDecision {
    phase: string;
    tool: string;
    intent: string;
    query?: string;
    evidence_signals?: string[];
    skipped?: boolean;
    skip_reason?: string;
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
    category?: string;
    priority: string;
    summary: string;
    details?: string;
    scope?: string;
    checks?: string[];
    safe: boolean;
    dry_run_default: boolean;
    requires_approval: boolean;
    approval_reason?: string;
    reversible: boolean;
    rollback_hint?: string;
    rationale?: string;
    expected_impact?: string;
    risk_level?: string;
    confidence?: number;
    evidence_ids?: string[];
    rollback_consideration?: string;
}

export interface ActionPolicyDecision {
    status: string;
    reason: string;
    requires_approval: boolean;
    dry_run_required: boolean;
    rollback_required: boolean;
    missing_conditions?: string[];
}

export interface ProposedAction {
    id: string;
    recommendation_id?: string;
    category?: string;
    risk_reference: string;
    command_preview: string;
    impact_scope: string;
    risk_level: string;
    rationale?: string;
    expected_impact?: string;
    confidence?: number;
    evidence_ids?: string[];
    rollback_plan: string;
    approval_required: boolean;
    approval_reason?: string;
    dry_run_plan?: string;
    audit_intent: string;
    collector_id?: string;
    workflow_id?: string;
    policy: ActionPolicyDecision;
    proposed_at: string;
    status: string;
}

export interface WorkflowInsights {
    enabled: boolean;
    provider: string;
    model: string;
    api_key_env: string;
    api_key_configured: boolean;
    mode: string;
}

export interface RetrievedDocumentEvidence {
    evidence_id: string;
    doc_id: string;
    chunk_id: string;
    title: string;
    source_path: string;
    source_type: string;
    knowledge_type?: string;
    case_type?: string;
    summary?: string;
    snippet: string;
    score: number;
    symptoms?: string[];
    evidence?: string[];
    likely_causes?: string[];
    remediation_steps?: string[];
    commands?: string[];
    signals?: string[];
    section_type?: string;
    tags?: string[];
    metadata?: Record<string, string>;
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
    trend_assessments?: TrendAssessment[];
    investigation_events?: InvestigationEvent[];
    cooccurrences: JointRiskCooccurrence[];
    scope_risks: ScopeRisk[];
    series: RiskSeries[];
    recommendations: WorkflowRecommendation[];
    stages: WorkflowStageResult[];
    tool_calls: WorkflowToolCall[];
    limitations?: string[];
    insights: WorkflowInsights;
    retrieved_docs?: RetrievedDocumentEvidence[];
    retrieved_cases?: RetrievedDocumentEvidence[];
    retrieved_runbooks?: RetrievedDocumentEvidence[];
    similar_incident_patterns?: RetrievedDocumentEvidence[];
    retrieval_summary?: string;
    retrieval_evidence_ids?: string[];
    retrieval_confidence?: number;
    retrieval_decisions?: RetrievalDecision[];
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
    contradicting_evidence_ids?: string[];
    recommended_actions?: string[];
    rollback_strategy?: string;
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
    incident_summary?: string;
    impacted_scope?: string[];
    top_metrics: Record<string, number>;
    trend_assessments?: TrendAssessment[];
    investigation_events?: InvestigationEvent[];
    gpu_summary?: Record<string, number>;
    top_processes: string[];
    kernel_signals: string[];
    trace_summary?: string[];
    recent_deploys?: string[];
    security_findings?: string[];
    topology_summary?: string;
    retrieval_summary?: string;
    retrieval_decisions?: RetrievalDecision[];
}

export interface RCAReport {
    workflow_id: string;
    pipeline_version: string;
    incident_id: string;
    trace_id?: string;
    status: string;
    collector_id?: string;
    trigger: string;
    generated_at: string;
    synthesized_incident: IncidentSynthesis;
    context: RCAContext;
    anomalies: string[];
    correlations: RCACorrelation[];
    hypotheses: RCAHypothesis[];
    evidence: RCAEvidence[];
    recommendations: WorkflowRecommendation[];
    proposed_actions?: ProposedAction[];
    agent_loop: AgentLoopSummary;
    structured_report: RCAStructuredReport;
    stages: WorkflowStageResult[];
    tool_calls: WorkflowToolCall[];
    reproducibility: Record<string, string>;
    unresolved_gaps?: string[];
    limitations?: string[];
    insights: WorkflowInsights;
    retrieved_docs?: RetrievedDocumentEvidence[];
    retrieved_cases?: RetrievedDocumentEvidence[];
    retrieved_runbooks?: RetrievedDocumentEvidence[];
    similar_incident_patterns?: RetrievedDocumentEvidence[];
    retrieval_summary?: string;
    retrieval_evidence_ids?: string[];
    retrieval_confidence?: number;
    adaptive_runtime?: AdaptiveRuntimeState;
    adaptive_dialogue?: AdaptiveDialogueTurn[];
    adaptive_tool_decisions?: AdaptiveToolDecision[];
    adaptive_artifacts?: AdaptiveArtifact[];
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
    trend_assessments?: TrendAssessment[];
    investigation_events?: InvestigationEvent[];
    suggested_investigation_steps: string[];
    correlations: JointRiskCooccurrence[];
    series: RiskSeries[];
    generated_at: string;
    retrieved_docs?: RetrievedDocumentEvidence[];
    retrieved_cases?: RetrievedDocumentEvidence[];
    retrieved_runbooks?: RetrievedDocumentEvidence[];
    similar_incident_patterns?: RetrievedDocumentEvidence[];
    retrieval_summary?: string;
    retrieval_evidence_ids?: string[];
    retrieval_confidence?: number;
    retrieval_decisions?: RetrievalDecision[];
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
    trace_id?: string;
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
    synthesized_incident: IncidentSynthesis;
    symptoms: string[];
    timeline: RCATimelineEvent[];
    evidence: RCAEvidence[];
    hypotheses: RCAHypothesis[];
    recommendations: WorkflowRecommendation[];
    proposed_actions?: ProposedAction[];
    agent_loop: AgentLoopSummary;
    unresolved_gaps?: string[];
}

export interface HypothesisUpdate {
    timestamp: string;
    hypothesis_id: string;
    action: string;
    old_confidence?: number;
    new_confidence: number;
    reason?: string;
}

export interface AgentTrace {
    trace_id: string;
    workflow_type: string;
    collector_id?: string;
    started_at: string;
    completed_at?: string;
    status: string;
    incident?: IncidentSynthesis;
    plan_versions?: AgentPlanRevision[];
    tool_calls?: WorkflowToolCall[];
    hypothesis_updates?: HypothesisUpdate[];
    recommendations?: WorkflowRecommendation[];
    proposed_actions?: ProposedAction[];
    final_risk_score: number;
    summary?: string;
    unresolved_gaps?: string[];
}

export interface AgentTraceResponse {
    trace: AgentTrace;
    timestamp: string;
}

export interface ProposedActionListResponse {
    actions: ProposedAction[];
    count: number;
    timestamp: string;
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
    setQueryParam(params, 'collector_id', query.collectorId, { trim: false });
    setQueryParam(params, 'window', query.window, { trim: false });
    setPositiveIntParam(params, 'limit', query.limit);
    setQueryParam(params, 'trigger', query.trigger, { trim: false });
    setQueryParam(params, 'status', query.status, { trim: false });
    setBooleanParam(params, 'dry_run', query.dryRun);
    setBooleanParam(params, 'refresh', query.refresh);
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
    setPositiveIntParam(params, 'limit', limit);
    setQueryParam(params, 'workflow_id', workflowID);
    const { data } = await api.get<WorkflowAuditResponse>(`/agent/workflow/audit${toQuerySuffix(params)}`);
    return data;
}

export async function fetchWorkflowIncidents(query: WorkflowQuery = {}): Promise<AgentIncidentListResponse> {
    const suffix = buildQueryString(query);
    const { data } = await api.get<AgentIncidentListResponse>(`/agent/workflow/incidents${suffix ? `?${suffix}` : ''}`);
    return data;
}

export async function fetchWorkflowIncidentByID(incidentID: string): Promise<AgentIncidentDetailResponse> {
    const id = requireTrimmedString(incidentID, 'incident id');
    const { data } = await api.get<AgentIncidentDetailResponse>(`/agent/workflow/incidents/${encodeURIComponent(id)}`);
    return data;
}

export async function fetchAgentTrace(traceID: string): Promise<AgentTraceResponse> {
    const id = requireTrimmedString(traceID, 'trace id');
    const { data } = await api.get<AgentTraceResponse>(`/agent/trace/${encodeURIComponent(id)}`);
    return data;
}

export async function fetchProposedActions(limit = 20, status = ''): Promise<ProposedActionListResponse> {
    const params = new URLSearchParams();
    setPositiveIntParam(params, 'limit', limit);
    setQueryParam(params, 'status', status);
    const { data } = await api.get<ProposedActionListResponse>(`/agent/proposed-actions${toQuerySuffix(params)}`);
    return data;
}
