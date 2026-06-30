import { FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bot, Brain, FileSearch, GitBranch, Link2, PlayCircle, Shield, Siren, TerminalSquare } from 'lucide-react';
import { api } from '@/api/client';
import KnowledgeEvidencePanel from '@/components/Insights/KnowledgeEvidencePanel';

interface AgentAction {
    id: string;
    type: string;
    node_name?: string;
    namespace?: string;
    name?: string;
    command?: string;
    priority?: string;
    description?: string;
    safe?: boolean;
    requires_approval?: boolean;
}

interface AgentQueryResponse {
    query_id: string;
    node: string;
    summary: string;
    root_cause: string;
    confidence: number;
    findings: string[];
    recommendations: string[];
    actions: AgentAction[];
    provider: string;
    model: string;
    used_fallback: boolean;
    gpu_context: boolean;
    generated_at: string;
    retrieved_docs?: AgentRetrievedDoc[];
    retrieval_summary?: string;
    retrieval_evidence_ids?: string[];
    retrieval_confidence?: number;
    retrieval_intent?: string;
    retrieval_mode?: string;
}

interface AgentRetrievedDoc {
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
    likely_causes?: string[];
    remediation_steps?: string[];
    commands?: string[];
    signals?: string[];
    tags?: string[];
}

interface AgentExecuteResponse {
    query_id?: string;
    result: {
        status: string;
        output?: string;
        error?: string;
        dry_run: boolean;
    };
}

interface IncidentWorkflowStage {
    name: string;
    status: string;
    summary: string;
    generated_at: string;
}

interface IncidentCorrelation {
    id: string;
    summary: string;
    score: number;
    evidence_ids?: string[];
}

interface IncidentDiagnosis {
    probable_root_cause: string;
    alternatives?: string[];
    confidence: number;
    blast_radius?: string[];
}

interface IncidentEvidence {
    id: string;
    kind: string;
    source: string;
    scope?: string;
    summary: string;
    confidence?: number;
}

interface IncidentRecommendation {
    id: string;
    priority: string;
    summary: string;
    details?: string;
    safe: boolean;
    requires_approval: boolean;
    reversible: boolean;
    runbook_id?: string;
}

interface IncidentRunbook {
    id: string;
    title: string;
    url: string;
    source: string;
}

interface IncidentAutomationAction {
    id: string;
    type: string;
    description: string;
    safe: boolean;
    requires_approval: boolean;
    reversible: boolean;
    dry_run_default: boolean;
    guard?: string;
    runbook_url?: string;
    last_status?: string;
    last_message?: string;
    last_executed_at?: string;
}

interface AgentIncidentAssessment {
    alert_id: string;
    incident_id: string;
    service: string;
    severity: string;
    summary: string;
    confidence: number;
    likely_causes: string[];
    recommendations: string[];
    workflow: IncidentWorkflowStage[];
    correlations: IncidentCorrelation[];
    diagnosis: IncidentDiagnosis;
    evidence: IncidentEvidence[];
    next_actions: IncidentRecommendation[];
    runbooks: IncidentRunbook[];
    automation_plan: {
        enabled: boolean;
        mode: string;
        actions: IncidentAutomationAction[];
    };
}

interface AgentIncidentResponse {
    incidents: AgentIncidentAssessment[];
    count: number;
    timestamp: string;
}

interface IncidentActionExecutionResponse {
    result: {
        alert_id: string;
        action_id: string;
        action_type: string;
        status: string;
        message: string;
        dry_run: boolean;
        audit_id?: string;
        rollback_id?: string;
        rollback_state?: string;
        started_at: string;
        completed_at: string;
    };
    timestamp: string;
}

interface IncidentActionRollbackResponse {
    result: {
        alert_id: string;
        action_id: string;
        action_type: string;
        status: string;
        message: string;
        dry_run: boolean;
        rollback_id?: string;
        audit_id?: string;
        started_at: string;
        completed_at: string;
    };
    timestamp: string;
}

interface IncidentActionAuditRecord {
    audit_id: string;
    alert_id: string;
    action_id: string;
    action_type: string;
    status: string;
    message: string;
    dry_run: boolean;
    rollback_id?: string;
    rollback_state?: string;
    executed_at: string;
}

interface LLMIssue {
    title: string;
    severity: string;
    explanation: string;
    evidence: string[];
}

interface LLMHypothesis {
    title: string;
    confidence: number;
    evidence: string[];
    description: string;
}

interface LLMAnalysisResult {
    issues: LLMIssue[];
    joint_risk_reason: string;
    rca_hypotheses?: LLMHypothesis[];
    next_steps: string[];
    confidence: number;
    evidence_cited: string[];
    limitations: string[];
}

interface WorkflowToolCall {
    id: string;
    tool: string;
    stage: string;
    status: string;
    summary?: string;
    started_at: string;
    completed_at: string;
}

interface WorkflowAuditRecord {
    id: string;
    workflow_id: string;
    stage: string;
    action: string;
    status: string;
    output_summary?: string;
    timestamp: string;
}

interface JointRiskReport {
    workflow_id: string;
    risk_score: number;
    risk_level: string;
    summary: string;
    llm_analysis?: LLMAnalysisResult;
    tool_calls: WorkflowToolCall[];
    insights: { enabled: boolean; mode: string; provider: string; model: string };
    retrieved_docs?: WorkflowRetrievedDoc[];
    retrieval_summary?: string;
    retrieval_evidence_ids?: string[];
    retrieval_confidence?: number;
}

interface RCAReport {
    workflow_id: string;
    most_likely_cause?: string;
    llm_analysis?: LLMAnalysisResult;
    tool_calls: WorkflowToolCall[];
    hypotheses: { title: string; confidence: number; description: string; rank: number }[];
    insights: { enabled: boolean; mode: string; provider: string; model: string };
    retrieved_docs?: WorkflowRetrievedDoc[];
    retrieval_summary?: string;
    retrieval_evidence_ids?: string[];
    retrieval_confidence?: number;
}

interface WorkflowRetrievedDoc {
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
    likely_causes?: string[];
    remediation_steps?: string[];
    commands?: string[];
    signals?: string[];
    tags?: string[];
}

const defaultQuery = 'RCA for high GPU utilization spikes on fleet';

type QueuedAgentQuery = {
    query: string;
    requestToken: number;
};

type AgentTabProps = {
    queuedQuery?: QueuedAgentQuery | null;
    onQueuedQueryHandled?: () => void;
};

function AgentTab({ queuedQuery, onQueuedQueryHandled }: AgentTabProps) {
    const queryClient = useQueryClient();
    const [query, setQuery] = useState(defaultQuery);
    const [node, setNode] = useState('');
    const [response, setResponse] = useState<AgentQueryResponse | null>(null);
    const [executionNote, setExecutionNote] = useState<string>('');
    const [approvalToken, setApprovalToken] = useState('');
    const [selectedIncidentID, setSelectedIncidentID] = useState('');
    const handledQueuedQueryRef = useRef(0);

    const queryMutation = useMutation({
        mutationFn: async (input: { query: string; node?: string }) => {
            const payload: Record<string, unknown> = { query: input.query.trim() };
            if (input.node?.trim()) {
                payload.node = input.node.trim();
            }
            const result = await api.post<AgentQueryResponse>('/agent/query', payload);
            return result.data;
        },
        onSuccess: (data) => {
            setResponse(data);
            setExecutionNote('');
        },
        onError: (error: unknown) => {
            setExecutionNote(formatError(error));
        },
    });

    const executeMutation = useMutation({
        mutationFn: async (actionID: string) => {
            const result = await api.post<AgentExecuteResponse>('/agent/execute', { action_id: actionID });
            return result.data;
        },
        onSuccess: (data, actionID) => {
            const suffix = data.result.output ? `: ${data.result.output}` : '';
            setExecutionNote(`Action ${actionID} ${data.result.status}${suffix}`);
        },
        onError: (error: unknown) => {
            setExecutionNote(formatError(error));
        },
    });

    const incidentsQuery = useQuery<AgentIncidentResponse>({
        queryKey: ['agent-incidents'],
        queryFn: async () => (await api.get('/agent/incidents?limit=12')).data,
        refetchInterval: 15000,
    });

    const incidentAuditQuery = useQuery<{ records: IncidentActionAuditRecord[]; count: number }>({
        queryKey: ['agent-incident-audit', selectedIncidentID],
        queryFn: async () => (await api.get(`/agent/incidents/${encodeURIComponent(selectedIncidentID)}/actions/audit?limit=25`)).data,
        enabled: Boolean(selectedIncidentID),
        refetchInterval: 12000,
        retry: false,
    });

    const incidentActionMutation = useMutation({
        mutationFn: async ({ alertID, action }: { alertID: string; action: IncidentAutomationAction }) => {
            const payload: Record<string, unknown> = {};
            if (action.requires_approval) {
                if (approvalToken.trim()) {
                    payload.approval_token = approvalToken.trim();
                    payload.dry_run = false;
                } else {
                    payload.dry_run = true;
                }
            }
            const result = await api.post<IncidentActionExecutionResponse>(
                `/agent/incidents/${encodeURIComponent(alertID)}/actions/${encodeURIComponent(action.id)}/execute`,
                payload
            );
            return result.data.result;
        },
        onSuccess: (result) => {
            setExecutionNote(`Incident action ${result.action_id} ${result.status}: ${result.message}`);
            queryClient.invalidateQueries({ queryKey: ['agent-incidents'] });
            queryClient.invalidateQueries({ queryKey: ['agent-incident-audit'] });
        },
        onError: (error: unknown) => {
            setExecutionNote(formatError(error));
        },
    });

    const incidentRollbackMutation = useMutation({
        mutationFn: async ({ alertID, action }: { alertID: string; action: IncidentAutomationAction }) => {
            const payload: Record<string, unknown> = { dry_run: true };
            if (action.requires_approval && approvalToken.trim()) {
                payload.approval_token = approvalToken.trim();
                payload.dry_run = false;
            }
            const result = await api.post<IncidentActionRollbackResponse>(
                `/agent/incidents/${encodeURIComponent(alertID)}/actions/${encodeURIComponent(action.id)}/rollback`,
                payload
            );
            return result.data.result;
        },
        onSuccess: (result) => {
            setExecutionNote(`Rollback ${result.action_id} ${result.status}: ${result.message}`);
            queryClient.invalidateQueries({ queryKey: ['agent-incidents'] });
            queryClient.invalidateQueries({ queryKey: ['agent-incident-audit'] });
        },
        onError: (error: unknown) => {
            setExecutionNote(formatError(error));
        },
    });

    const sortedActions = useMemo(() => {
        const actions = response?.actions ?? [];
        return [...actions].sort((a, b) => priorityScore(a.priority) - priorityScore(b.priority));
    }, [response?.actions]);

    const incidents = incidentsQuery.data?.incidents ?? [];
    useEffect(() => {
        if (!selectedIncidentID && incidents.length > 0) {
            setSelectedIncidentID(incidents[0].alert_id);
        }
    }, [selectedIncidentID, incidents]);

    useEffect(() => {
        if (!queuedQuery || handledQueuedQueryRef.current === queuedQuery.requestToken) {
            return;
        }
        handledQueuedQueryRef.current = queuedQuery.requestToken;
        setQuery(queuedQuery.query);
        setNode('');
        setResponse(null);
        setExecutionNote('');
        queryMutation.mutate({ query: queuedQuery.query });
        onQueuedQueryHandled?.();
    }, [onQueuedQueryHandled, queryMutation, queuedQuery]);

    const selectedIncident = useMemo(() => {
        if (incidents.length === 0) {
            return null;
        }
        return incidents.find((incident) => incident.alert_id === selectedIncidentID) ?? incidents[0];
    }, [incidents, selectedIncidentID]);

    const auditRecords = incidentAuditQuery.data?.records ?? [];

    const jointRiskQuery = useQuery<{ reports: JointRiskReport[] }>({
        queryKey: ['agent-joint-risk'],
        queryFn: async () => (await api.get('/agent/joint-risk?limit=1')).data,
        refetchInterval: 20000,
        retry: false,
    });

    const rcaQuery = useQuery<{ reports: RCAReport[] }>({
        queryKey: ['agent-rca'],
        queryFn: async () => (await api.get('/agent/rca?limit=1')).data,
        refetchInterval: 20000,
        retry: false,
    });

    const latestJointRisk = jointRiskQuery.data?.reports?.[0];
    const latestRCA = rcaQuery.data?.reports?.[0];
    const activeAnalysis = latestRCA?.llm_analysis ?? latestJointRisk?.llm_analysis;
    const activeToolCalls = latestRCA?.tool_calls ?? latestJointRisk?.tool_calls ?? [];
    const activeInsights = latestRCA?.insights ?? latestJointRisk?.insights;
    const activeKnowledgeDocs = latestRCA?.retrieved_docs ?? latestJointRisk?.retrieved_docs ?? [];
    const activeKnowledgeSummary = latestRCA?.retrieval_summary ?? latestJointRisk?.retrieval_summary;
    const activeKnowledgeEvidenceIDs = latestRCA?.retrieval_evidence_ids ?? latestJointRisk?.retrieval_evidence_ids ?? [];
    const activeKnowledgeConfidence = latestRCA?.retrieval_confidence ?? latestJointRisk?.retrieval_confidence;

    const submit = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!query.trim() || queryMutation.isLoading) {
            return;
        }
        queryMutation.mutate({ query, node });
    };

    return (
        <div className="h-full flex flex-col gap-4">
            <section className="rounded-xl border border-border bg-card p-4 shadow-lg">
                <div className="flex items-center gap-2 mb-3">
                    <Bot className="w-5 h-5 text-primary" />
                    <h2 className="text-base font-semibold">AGENT Query</h2>
                    <span className="text-xs text-muted-foreground">NL to RCA + Playbooks</span>
                </div>
                <form onSubmit={submit} className="space-y-3">
                    <textarea
                        value={query}
                        onChange={(event) => setQuery(event.target.value)}
                        className="w-full min-h-[96px] rounded-lg border border-border bg-background px-3 py-2 text-sm"
                        placeholder="Analyze GPU SM utilization spike on node-a and suggest safe remediations."
                    />
                    <div className="flex flex-col md:flex-row gap-2">
                        <input
                            value={node}
                            onChange={(event) => setNode(event.target.value)}
                            className="flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm"
                            placeholder="Optional node/collector filter"
                        />
                        <button
                            type="submit"
                            className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
                            disabled={queryMutation.isLoading || !query.trim()}
                        >
                            {queryMutation.isLoading ? 'Analyzing…' : 'Run AGENT'}
                        </button>
                    </div>
                </form>
            </section>

            {executionNote && (
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                    {executionNote}
                </div>
            )}

            {response && (
                <section className="rounded-xl border border-border bg-card p-4 shadow-lg overflow-auto">
                    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground mb-2">
                        <span>provider: {response.provider}</span>
                        <span>model: {response.model}</span>
                        <span>confidence: {(response.confidence * 100).toFixed(0)}%</span>
                        <span>gpu-context: {response.gpu_context ? 'yes' : 'no'}</span>
                        {response.retrieval_intent && <span>rag-intent: {response.retrieval_intent}</span>}
                        {response.retrieval_mode && <span>rag-mode: {response.retrieval_mode}</span>}
                        {response.used_fallback && <span>fallback: deterministic</span>}
                    </div>
                    <h3 className="text-lg font-semibold">{response.summary}</h3>
                    <p className="text-sm text-muted-foreground mt-2">{response.root_cause}</p>

                    <div className="grid md:grid-cols-2 gap-4 mt-4">
                        <div className="rounded-lg border border-border bg-background/40 p-3">
                            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">Findings</div>
                            <ul className="space-y-1 text-sm">
                                {response.findings.map((finding) => (
                                    <li key={finding}>• {finding}</li>
                                ))}
                            </ul>
                        </div>
                        <div className="rounded-lg border border-border bg-background/40 p-3">
                            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">Recommendations</div>
                            <ul className="space-y-1 text-sm">
                                {response.recommendations.map((item) => (
                                    <li key={item}>• {item}</li>
                                ))}
                            </ul>
                        </div>
                    </div>

                    <div className="mt-4">
                        <KnowledgeEvidencePanel
                            title="Query Knowledge Evidence"
                            summary={response.retrieval_summary}
                            confidence={response.retrieval_confidence}
                            evidenceIDs={response.retrieval_evidence_ids}
                            docs={(response.retrieved_docs ?? []).map((doc) => ({
                                id: doc.chunk_id,
                                title: doc.title,
                                source_path: doc.source_path,
                                source_type: doc.source_type,
                                knowledge_type: doc.knowledge_type,
                                case_type: doc.case_type,
                                summary: doc.summary,
                                snippet: doc.snippet,
                                score: doc.score,
                                likely_causes: doc.likely_causes,
                                remediation_steps: doc.remediation_steps,
                                commands: doc.commands,
                                signals: doc.signals,
                                tags: doc.tags,
                            }))}
                            emptyText="This AGENT query did not retrieve supporting knowledge snippets."
                            testId="agent-query-knowledge-evidence"
                        />
                    </div>

                    <div className="mt-4">
                        <div className="flex items-center gap-2 mb-2">
                            <TerminalSquare className="w-4 h-4 text-primary" />
                            <div className="text-xs uppercase tracking-wide text-muted-foreground">Proposed actions</div>
                        </div>
                        <div className="space-y-2">
                            {sortedActions.length === 0 && (
                                <div className="text-sm text-muted-foreground">No actions proposed for this query.</div>
                            )}
                            {sortedActions.map((action) => (
                                <div key={action.id} className="rounded-lg border border-border bg-background/40 p-3">
                                    <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-2">
                                        <div className="text-sm font-medium">{action.type}</div>
                                        <div className="flex items-center gap-2 text-xs text-muted-foreground">
                                            {action.safe ? <Shield className="w-3 h-3" /> : <span>unsafe</span>}
                                            <span>{(action.priority ?? 'P3').toUpperCase()}</span>
                                        </div>
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        {action.description ?? 'No description'}
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        {[action.namespace, action.name, action.command].filter(Boolean).join(' • ')}
                                    </div>
                                    <button
                                        type="button"
                                        className="mt-2 inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted/60 disabled:opacity-60"
                                        disabled={executeMutation.isLoading}
                                        onClick={() => executeMutation.mutate(action.id)}
                                    >
                                        <PlayCircle className="w-3 h-3" />
                                        Execute
                                    </button>
                                </div>
                            ))}
                        </div>
                    </div>
                </section>
            )}

            {activeAnalysis && (
                <section className="rounded-xl border border-border bg-card p-4 shadow-lg overflow-auto" data-testid="agent-analysis">
                    <div className="flex items-center gap-2 mb-3">
                        <Brain className="w-5 h-5 text-violet-400" />
                        <h2 className="text-base font-semibold">Agent Analysis</h2>
                        {activeInsights && (
                            <span className="text-xs text-muted-foreground">
                                {activeInsights.mode} · {activeInsights.provider}/{activeInsights.model}
                            </span>
                        )}
                        <span className={`ml-auto text-xs px-2 py-0.5 rounded-full ${activeAnalysis.confidence >= 0.7 ? 'bg-emerald-900/50 text-emerald-300' : activeAnalysis.confidence >= 0.45 ? 'bg-amber-900/50 text-amber-300' : 'bg-red-900/50 text-red-300'}`}>
                            confidence {(activeAnalysis.confidence * 100).toFixed(0)}%
                        </span>
                    </div>

                    {activeAnalysis.joint_risk_reason && (
                        <div className="rounded-lg border border-border bg-background/40 p-3 mb-3">
                            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-1">Joint Risk Analysis</div>
                            <div className="text-sm">{activeAnalysis.joint_risk_reason}</div>
                        </div>
                    )}

                    <div className="grid md:grid-cols-2 gap-3 mb-3">
                        <div className="rounded-lg border border-border bg-background/40 p-3">
                            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">Identified Issues</div>
                            <div className="space-y-2">
                                {activeAnalysis.issues.map((issue, idx) => (
                                    <div key={idx} className="text-sm">
                                        <div className="flex items-center gap-2">
                                            <span className={`text-xs px-1.5 py-0.5 rounded ${issue.severity === 'critical' || issue.severity === 'high' ? 'bg-red-900/50 text-red-300' : issue.severity === 'medium' ? 'bg-amber-900/50 text-amber-300' : 'bg-slate-700/50 text-slate-300'}`}>
                                                {issue.severity}
                                            </span>
                                            <span className="font-medium">{issue.title}</span>
                                        </div>
                                        <div className="text-xs text-muted-foreground mt-1">{issue.explanation}</div>
                                        <div className="text-xs text-muted-foreground mt-0.5">
                                            evidence: {issue.evidence.join(', ')}
                                        </div>
                                    </div>
                                ))}
                            </div>
                        </div>

                        <div className="rounded-lg border border-border bg-background/40 p-3">
                            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">Next Investigation Steps</div>
                            <ul className="space-y-1 text-sm">
                                {activeAnalysis.next_steps.map((step, idx) => (
                                    <li key={idx}>• {step}</li>
                                ))}
                            </ul>
                            {activeAnalysis.limitations.length > 0 && (
                                <div className="mt-3">
                                    <div className="text-xs uppercase tracking-wide text-muted-foreground mb-1">Limitations</div>
                                    <ul className="space-y-1 text-xs text-muted-foreground">
                                        {activeAnalysis.limitations.map((lim, idx) => (
                                            <li key={idx}>⚠ {lim}</li>
                                        ))}
                                    </ul>
                                </div>
                            )}
                        </div>
                    </div>

                    {activeAnalysis.rca_hypotheses && activeAnalysis.rca_hypotheses.length > 0 && (
                        <div className="rounded-lg border border-border bg-background/40 p-3 mb-3">
                            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">RCA Hypotheses</div>
                            <div className="space-y-2">
                                {activeAnalysis.rca_hypotheses.map((hyp, idx) => (
                                    <div key={idx} className="flex items-start gap-2 text-sm">
                                        <span className="text-xs font-mono text-muted-foreground mt-0.5">#{idx + 1}</span>
                                        <div className="flex-1">
                                            <div className="flex items-center gap-2">
                                                <span className="font-medium">{hyp.title}</span>
                                                <span className={`text-xs px-1.5 py-0.5 rounded ${hyp.confidence >= 0.7 ? 'bg-emerald-900/50 text-emerald-300' : 'bg-amber-900/50 text-amber-300'}`}>
                                                    {(hyp.confidence * 100).toFixed(0)}%
                                                </span>
                                            </div>
                                            <div className="text-xs text-muted-foreground">{hyp.description}</div>
                                        </div>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    <div className="rounded-lg border border-border bg-background/40 p-3">
                        <div className="text-xs uppercase tracking-wide text-muted-foreground mb-1">Evidence Cited</div>
                        <div className="text-xs text-muted-foreground">
                            {activeAnalysis.evidence_cited.join(' · ')}
                        </div>
                    </div>
                </section>
            )}

            {(activeKnowledgeDocs.length > 0 || activeKnowledgeSummary) && (
                <KnowledgeEvidencePanel
                    title="Workflow Knowledge Evidence"
                    summary={activeKnowledgeSummary}
                    confidence={activeKnowledgeConfidence}
                    evidenceIDs={activeKnowledgeEvidenceIDs}
                    docs={activeKnowledgeDocs.map((doc) => ({
                        id: doc.evidence_id,
                        title: doc.title,
                        source_path: doc.source_path,
                        source_type: doc.source_type,
                        knowledge_type: doc.knowledge_type,
                        case_type: doc.case_type,
                        summary: doc.summary,
                        snippet: doc.snippet,
                        score: doc.score,
                        likely_causes: doc.likely_causes,
                        remediation_steps: doc.remediation_steps,
                        commands: doc.commands,
                        signals: doc.signals,
                        tags: doc.tags,
                    }))}
                    emptyText="No workflow knowledge evidence available."
                    testId="agent-workflow-knowledge-evidence"
                />
            )}

            {activeToolCalls.length > 0 && (
                <section className="rounded-xl border border-border bg-card p-4 shadow-lg overflow-auto" data-testid="agent-trace">
                    <div className="flex items-center gap-2 mb-3">
                        <TerminalSquare className="w-5 h-5 text-cyan-400" />
                        <h2 className="text-base font-semibold">Agent Trace</h2>
                        <span className="text-xs text-muted-foreground">{activeToolCalls.length} tool calls</span>
                    </div>
                    <div className="space-y-1 max-h-64 overflow-auto">
                        {activeToolCalls.map((tc) => (
                            <div key={tc.id} className="flex items-center gap-2 text-xs rounded border border-border bg-background/40 px-2 py-1">
                                <span className={`w-2 h-2 rounded-full ${tc.status === 'success' ? 'bg-emerald-400' : tc.status === 'failed' ? 'bg-red-400' : 'bg-amber-400'}`} />
                                <span className="font-mono text-muted-foreground">{tc.tool}</span>
                                <span className="text-muted-foreground">·</span>
                                <span>{tc.stage}</span>
                                {tc.summary && (
                                    <>
                                        <span className="text-muted-foreground">·</span>
                                        <span className="text-muted-foreground truncate max-w-xs">{tc.summary}</span>
                                    </>
                                )}
                            </div>
                        ))}
                    </div>
                </section>
            )}

            <section className="rounded-xl border border-border bg-card p-4 shadow-lg overflow-auto" data-testid="agent-incident-workflow">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="flex items-center gap-2">
                        <Siren className="w-5 h-5 text-amber-300" />
                        <h2 className="text-base font-semibold">Incident Workflow</h2>
                        <span className="text-xs text-muted-foreground">incident_intake → context_gathering → hypothesis_generation → evidence_collection → recommendation_generation → guarded_execution</span>
                    </div>
                    <div className="flex items-center gap-2">
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-xs"
                            value={selectedIncident?.alert_id ?? ''}
                            onChange={(event) => setSelectedIncidentID(event.target.value)}
                            disabled={incidents.length === 0}
                        >
                            {incidents.map((incident) => (
                                <option key={incident.alert_id} value={incident.alert_id}>
                                    {incident.alert_id} • {incident.service}
                                </option>
                            ))}
                        </select>
                        <input
                            value={approvalToken}
                            onChange={(event) => setApprovalToken(event.target.value)}
                            placeholder="Optional approval token"
                            className="rounded border border-border bg-background px-2 py-1 text-xs w-44"
                        />
                    </div>
                </div>

                {incidentsQuery.isLoading && (
                    <div className="mt-4 text-sm text-muted-foreground">Loading incident workflow output…</div>
                )}
                {incidentsQuery.isError && (
                    <div className="mt-4 text-sm text-red-400">Agent incident workflow unavailable.</div>
                )}
                {!incidentsQuery.isLoading && !incidentsQuery.isError && incidents.length === 0 && (
                    <div className="mt-4 text-sm text-muted-foreground">No incident workflow outputs yet.</div>
                )}

                {selectedIncident && (
                    <div className="mt-4 space-y-4">
                        <div className="rounded-lg border border-border bg-background/40 p-3">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                                <div className="text-sm font-medium">{selectedIncident.summary}</div>
                                <div className="text-xs text-muted-foreground">
                                    severity {selectedIncident.severity} • confidence {(selectedIncident.confidence * 100).toFixed(0)}%
                                </div>
                            </div>
                            <div className="mt-2 text-sm">
                                <span className="text-muted-foreground">Probable root cause:</span>{' '}
                                {selectedIncident.diagnosis?.probable_root_cause || 'n/a'}
                            </div>
                        </div>

                        <div className="grid lg:grid-cols-2 gap-3">
                            <div className="rounded-lg border border-border bg-background/40 p-3">
                                <div className="flex items-center gap-1 text-xs uppercase tracking-wide text-muted-foreground mb-2">
                                    <GitBranch className="w-3 h-3" />
                                    Workflow Stages
                                </div>
                                <div className="space-y-1 text-sm">
                                    {selectedIncident.workflow?.map((stage) => (
                                        <div key={stage.name} className="flex items-center justify-between gap-2">
                                            <span>{stage.name}</span>
                                            <span className={`text-xs ${stage.status === 'completed' ? 'text-emerald-300' : 'text-amber-300'}`}>
                                                {stage.status}
                                            </span>
                                        </div>
                                    ))}
                                </div>
                            </div>

                            <div className="rounded-lg border border-border bg-background/40 p-3">
                                <div className="flex items-center gap-1 text-xs uppercase tracking-wide text-muted-foreground mb-2">
                                    <FileSearch className="w-3 h-3" />
                                    Correlations
                                </div>
                                <div className="space-y-2">
                                    {(selectedIncident.correlations ?? []).slice(0, 4).map((correlation) => (
                                        <div key={correlation.id} className="text-sm">
                                            {correlation.summary}
                                            <div className="text-xs text-muted-foreground">
                                                score {(correlation.score * 100).toFixed(0)}%
                                            </div>
                                        </div>
                                    ))}
                                    {(selectedIncident.correlations ?? []).length === 0 && (
                                        <div className="text-sm text-muted-foreground">No strong cross-signal correlations.</div>
                                    )}
                                </div>
                            </div>
                        </div>

                        <div className="grid lg:grid-cols-2 gap-3">
                            <div className="rounded-lg border border-border bg-background/40 p-3">
                                <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">Evidence</div>
                                <div className="space-y-2 text-sm">
                                    {(selectedIncident.evidence ?? []).slice(0, 6).map((item) => (
                                        <div key={item.id}>
                                            <span className="text-muted-foreground">[{item.kind}]</span> {item.summary}
                                        </div>
                                    ))}
                                    {(selectedIncident.evidence ?? []).length === 0 && (
                                        <div className="text-sm text-muted-foreground">No evidence attached.</div>
                                    )}
                                </div>
                            </div>

                            <div className="rounded-lg border border-border bg-background/40 p-3">
                                <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">Recommended next steps</div>
                                <div className="space-y-2">
                                    {(selectedIncident.next_actions ?? []).map((step) => (
                                        <div key={step.id} className="text-sm">
                                            {step.summary}
                                            <div className="text-xs text-muted-foreground">
                                                {step.priority} • {step.safe ? 'safe' : 'guarded'} • {step.reversible ? 'reversible' : 'non-reversible'}
                                            </div>
                                        </div>
                                    ))}
                                </div>
                            </div>
                        </div>

                        <div className="rounded-lg border border-border bg-background/40 p-3">
                            <div className="flex items-center gap-1 text-xs uppercase tracking-wide text-muted-foreground mb-2">
                                <Link2 className="w-3 h-3" />
                                Runbooks
                            </div>
                            <div className="space-y-1 text-sm">
                                {(selectedIncident.runbooks ?? []).map((runbook) => (
                                    <div key={runbook.id} className="flex flex-wrap items-center gap-2">
                                        <span>{runbook.title}</span>
                                        {runbook.url.startsWith('http://') || runbook.url.startsWith('https://') ? (
                                            <a className="text-xs text-cyan-300 hover:underline" href={runbook.url} target="_blank" rel="noreferrer">
                                                {runbook.url}
                                            </a>
                                        ) : (
                                            <code className="text-xs text-muted-foreground">{runbook.url}</code>
                                        )}
                                    </div>
                                ))}
                                {(selectedIncident.runbooks ?? []).length === 0 && (
                                    <div className="text-sm text-muted-foreground">No explicit runbook links.</div>
                                )}
                            </div>
                        </div>

                        <div className="rounded-lg border border-border bg-background/40 p-3">
                            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">
                                Guarded automation ({selectedIncident.automation_plan?.mode ?? 'disabled'})
                            </div>
                            <div className="space-y-2">
                                {(selectedIncident.automation_plan?.actions ?? []).map((action) => (
                                    <div key={action.id} className="rounded border border-border px-3 py-2">
                                        <div className="flex flex-wrap items-center justify-between gap-2">
                                            <div className="text-sm font-medium">{action.type}</div>
                                            <div className="text-xs text-muted-foreground">
                                                {action.safe ? 'safe' : 'guarded'} • {action.dry_run_default ? 'dry-run default' : 'direct'}
                                            </div>
                                        </div>
                                        <div className="text-xs text-muted-foreground mt-1">{action.description}</div>
                                        {action.last_status && (
                                            <div className="text-xs text-muted-foreground mt-1">
                                                last: {action.last_status} {action.last_message ? `• ${action.last_message}` : ''}
                                            </div>
                                        )}
                                        <button
                                            type="button"
                                            className="mt-2 inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted/60 disabled:opacity-60"
                                            disabled={incidentActionMutation.isLoading || incidentRollbackMutation.isLoading}
                                            onClick={() => incidentActionMutation.mutate({ alertID: selectedIncident.alert_id, action })}
                                        >
                                            <PlayCircle className="w-3 h-3" />
                                            Execute
                                        </button>
                                        {action.reversible && (
                                            <button
                                                type="button"
                                                className="mt-2 ml-2 inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted/60 disabled:opacity-60"
                                                disabled={incidentActionMutation.isLoading || incidentRollbackMutation.isLoading}
                                                onClick={() => incidentRollbackMutation.mutate({ alertID: selectedIncident.alert_id, action })}
                                            >
                                                Rollback
                                            </button>
                                        )}
                                    </div>
                                ))}
                                {(selectedIncident.automation_plan?.actions ?? []).length === 0 && (
                                    <div className="text-sm text-muted-foreground">No automation actions proposed.</div>
                                )}
                            </div>
                        </div>

                        <div className="rounded-lg border border-border bg-background/40 p-3" data-testid="agent-action-audit">
                            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">
                                Action audit trail
                            </div>
                            <div className="space-y-2 max-h-52 overflow-auto">
                                {auditRecords.map((record) => (
                                    <div key={record.audit_id} className="rounded border border-border px-2 py-1 text-xs">
                                        <div className="font-medium">{record.action_type}</div>
                                        <div className="text-muted-foreground">
                                            {record.status} {record.dry_run ? '(dry-run)' : ''} · {record.executed_at}
                                        </div>
                                        <div className="text-muted-foreground">{record.message}</div>
                                        {record.rollback_id && (
                                            <div className="text-muted-foreground">
                                                rollback {record.rollback_id} · {record.rollback_state || 'prepared'}
                                            </div>
                                        )}
                                    </div>
                                ))}
                                {auditRecords.length === 0 && (
                                    <div className="text-sm text-muted-foreground">
                                        No action audit records yet for this incident.
                                    </div>
                                )}
                                {incidentAuditQuery.isError && (
                                    <div className="text-sm text-red-400">
                                        Audit trail endpoint unavailable.
                                    </div>
                                )}
                            </div>
                        </div>
                    </div>
                )}
            </section>
        </div>
    );
}

function priorityScore(priority?: string): number {
    const normalized = (priority ?? '').toUpperCase();
    switch (normalized) {
        case 'P0':
            return 0;
        case 'P1':
            return 1;
        case 'P2':
            return 2;
        default:
            return 3;
    }
}

function formatError(error: unknown): string {
    if (typeof error !== 'object' || error === null) {
        return 'AGENT request failed';
    }
    const maybeError = error as { response?: { data?: unknown } };
    if (typeof maybeError.response?.data === 'string') {
        return maybeError.response.data;
    }
    if (typeof maybeError.response?.data === 'object' && maybeError.response?.data !== null) {
        const payload = maybeError.response.data as { error?: string };
        if (payload.error) {
            return payload.error;
        }
    }
    return 'AGENT request failed';
}

export default AgentTab;
