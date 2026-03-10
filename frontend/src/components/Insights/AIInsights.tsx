import React from 'react';
import { useQuery } from '@tanstack/react-query';
import { formatDistanceToNow } from 'date-fns';
import { Brain, Clock, ShieldAlert, Siren } from 'lucide-react';
import {
    AgentIncidentReport,
    ProposedAction,
    RCAReport,
    fetchProposedActions,
    fetchRCAWorkflowReports,
    fetchWorkflowIncidents,
} from '@/api/agentWorkflows';

function formatRelativeTime(value?: string) {
    if (!value) {
        return 'just now';
    }
    const timestamp = new Date(value);
    if (Number.isNaN(timestamp.getTime())) {
        return 'just now';
    }
    return formatDistanceToNow(timestamp, { addSuffix: true });
}

function reportSummary(report: RCAReport) {
    return (
        report.structured_report?.incident_summary ||
        report.synthesized_incident?.summary ||
        report.context?.incident_summary ||
        'RCA workflow report'
    );
}

function reportSeverity(report: RCAReport) {
    return report.synthesized_incident?.severity || 'info';
}

function severityStyles(severity: string) {
    switch ((severity || '').toLowerCase()) {
    case 'critical':
        return { border: 'border-l-red-500', text: 'text-red-400' };
    case 'high':
        return { border: 'border-l-yellow-500', text: 'text-yellow-400' };
    case 'medium':
        return { border: 'border-l-amber-500', text: 'text-amber-300' };
    default:
        return { border: 'border-l-blue-500', text: 'text-blue-400' };
    }
}

function firstRecommendation(report: RCAReport) {
    return report.recommendations?.[0]?.summary || report.structured_report?.recommended_next_steps?.[0] || '';
}

function incidentCause(incident: AgentIncidentReport) {
    return incident.hypotheses?.[0]?.title || incident.most_likely_cause || '';
}

function actionHeadline(action: ProposedAction) {
    return action.command_preview || action.rationale || action.audit_intent || action.id;
}

const AIInsightsPanel = () => {
    const { data: rcaData, isLoading: rcaLoading, isError: rcaError } = useQuery({
        queryKey: ['agent-rca-dashboard'],
        queryFn: () => fetchRCAWorkflowReports({ limit: 6, refresh: false }),
        refetchInterval: 30000,
    });

    const { data: incidentData, isLoading: incidentsLoading, isError: incidentsError } = useQuery({
        queryKey: ['agent-workflow-incidents-dashboard'],
        queryFn: () => fetchWorkflowIncidents({ limit: 6, refresh: false }),
        refetchInterval: 15000,
    });

    const { data: actionData, isLoading: actionsLoading, isError: actionsError } = useQuery({
        queryKey: ['agent-proposed-actions-dashboard'],
        queryFn: () => fetchProposedActions(12),
        refetchInterval: 30000,
    });

    const reports = rcaData?.reports ?? [];
    const incidents = incidentData?.incidents ?? [];
    const actions = actionData?.actions ?? [];

    return (
        <div className="bg-card text-card-foreground rounded-lg shadow-md h-full flex flex-col border border-border">
            <div className="p-4 border-b border-border flex items-center justify-between">
                <h3 className="font-semibold text-lg flex items-center gap-2">
                    <Brain className="w-5 h-5 text-purple-400" />
                    Agent Insights
                </h3>
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <Clock className="w-3 h-3" />
                    {rcaLoading ? 'Syncing…' : 'Live'}
                </div>
            </div>

            <div className="flex-1 overflow-y-auto p-4 space-y-4">
                <div className="space-y-3">
                    <div className="text-xs uppercase tracking-wider text-muted-foreground">Latest RCA decisions</div>
                    {rcaError && (
                        <div className="text-sm text-red-400">
                            RCA workflow reports unavailable. Ensure the controller workflow engine is enabled.
                        </div>
                    )}
                    {rcaLoading && <div className="text-sm text-muted-foreground">Fetching RCA workflow reports…</div>}
                    {!rcaLoading && reports.length === 0 && !rcaError && (
                        <div className="text-sm text-muted-foreground">No RCA workflow reports yet.</div>
                    )}
                    {reports.map((report) => {
                        const styles = severityStyles(reportSeverity(report));
                        return (
                            <div
                                key={report.workflow_id}
                                className={`p-3 rounded-md border-l-4 bg-muted/30 hover:bg-muted/50 transition-colors ${styles.border}`}
                            >
                                <div className="flex justify-between items-start mb-1 gap-3">
                                    <span className={`text-sm font-medium ${styles.text}`}>{reportSummary(report)}</span>
                                    <span className="text-xs text-muted-foreground">{formatRelativeTime(report.generated_at)}</span>
                                </div>
                                <div className="text-xs text-muted-foreground mb-2">
                                    {report.collector_id || 'fleet'} • {report.synthesized_incident?.severity || 'info'} • confidence {(report.synthesized_incident?.confidence ?? report.structured_report?.confidence ?? 0).toFixed(2)}
                                </div>
                                {report.hypotheses?.[0]?.title && (
                                    <div className="text-sm text-gray-300 mb-1">
                                        Hypothesis: {report.hypotheses[0].title}
                                    </div>
                                )}
                                {firstRecommendation(report) && (
                                    <div className="text-xs text-muted-foreground bg-background/40 border border-border rounded-md p-2">
                                        Next step: {firstRecommendation(report)}
                                    </div>
                                )}
                            </div>
                        );
                    })}
                </div>

                <div className="space-y-3 pt-2">
                    <div className="text-xs uppercase tracking-wider text-muted-foreground flex items-center gap-1">
                        <Siren className="w-3 h-3" />
                        Workflow incidents
                    </div>
                    {incidentsError && <div className="text-sm text-red-400">Workflow incident stream unavailable.</div>}
                    {incidentsLoading && <div className="text-sm text-muted-foreground">Fetching workflow incidents…</div>}
                    {!incidentsLoading && incidents.length === 0 && !incidentsError && (
                        <div className="text-sm text-muted-foreground">No workflow incidents yet.</div>
                    )}
                    {incidents.map((incident) => (
                        <div key={incident.incident_id} className="p-3 rounded-md bg-muted/20 border border-border">
                            <div className="flex items-center justify-between gap-2">
                                <div className="text-sm font-medium text-foreground">{incident.summary}</div>
                                <span className="text-xs px-2 py-0.5 rounded border border-border text-muted-foreground">
                                    {incident.risk_level}
                                </span>
                            </div>
                            <div className="text-xs text-muted-foreground mt-1">
                                {incident.collector_id || 'fleet'} • confidence {(incident.confidence * 100).toFixed(0)}%
                            </div>
                            {incidentCause(incident) && (
                                <div className="text-xs text-muted-foreground mt-1">{incidentCause(incident)}</div>
                            )}
                            {incident.unresolved_gaps?.[0] && (
                                <div className="text-xs text-amber-300 mt-2">Gap: {incident.unresolved_gaps[0]}</div>
                            )}
                        </div>
                    ))}
                </div>

                <div className="space-y-3 pt-2">
                    <div className="text-xs uppercase tracking-wider text-muted-foreground flex items-center gap-1">
                        <ShieldAlert className="w-3 h-3" />
                        Guarded proposed actions
                    </div>
                    {actionsError && <div className="text-sm text-red-400">Proposed action stream unavailable.</div>}
                    {actionsLoading && <div className="text-sm text-muted-foreground">Fetching proposed actions…</div>}
                    {!actionsLoading && actions.length === 0 && !actionsError && (
                        <div className="text-sm text-muted-foreground">No guarded actions proposed.</div>
                    )}
                    {actions.map((action) => (
                        <div key={action.id} className="p-3 rounded-md bg-muted/20 border border-border">
                            <div className="flex items-center justify-between gap-2">
                                <div className="text-sm font-medium text-foreground">{actionHeadline(action)}</div>
                                <span className="text-xs px-2 py-1 rounded-full bg-background/60 border border-border text-muted-foreground">
                                    {action.policy?.status || action.status}
                                </span>
                            </div>
                            <div className="text-xs text-muted-foreground mt-1">
                                {action.collector_id || 'fleet'} • {(action.category || 'uncategorized').replace(/_/g, ' ')} • {(action.risk_level || 'unknown').toUpperCase()}
                            </div>
                            {action.expected_impact && (
                                <div className="text-xs text-muted-foreground mt-1">Impact: {action.expected_impact}</div>
                            )}
                            {action.rollback_plan && (
                                <div className="text-xs text-muted-foreground mt-1">Rollback: {action.rollback_plan}</div>
                            )}
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
};

export default AIInsightsPanel;
