import React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { formatDistanceToNow } from 'date-fns';
import { Brain, Clock, ShieldAlert } from 'lucide-react';
import { api } from '@/api/client';

interface AgentAction {
    id: string;
    node_name: string;
    type: string;
    reason: string;
    priority: string;
    safe: boolean;
    status: string;
    note?: string;
    created_at: string;
    updated_at: string;
}

interface AgentLLMInsight {
    summary?: string;
    root_cause?: string;
    confidence?: number;
    recommendations?: string[];
    context_snippets?: string[];
}

interface AgentReport {
    id: string;
    node_name: string;
    generated_at: string;
    summary: string;
    findings: string[];
    forecasts: string[];
    actions: AgentAction[];
    rcas: Array<{ title?: string; summary?: string }>;
    llm?: AgentLLMInsight;
}

interface AgentReportResponse {
    reports: AgentReport[];
    count: number;
    timestamp: string;
}

interface AgentActionResponse {
    actions: AgentAction[];
    count: number;
    timestamp: string;
}

const AIInsightsPanel = () => {
    const queryClient = useQueryClient();

    const { data: reportData, isLoading: reportsLoading, isError: reportsError } = useQuery<AgentReportResponse>({
        queryKey: ['agent-reports-latest'],
        queryFn: async () => (await api.get('/agent/reports/latest?limit=6')).data,
        refetchInterval: 30000,
    });

    const { data: actionData, isLoading: actionsLoading, isError: actionsError } = useQuery<AgentActionResponse>({
        queryKey: ['agent-actions'],
        queryFn: async () => (await api.get('/agent/actions?limit=16')).data,
        refetchInterval: 30000,
    });

    const updateAction = useMutation({
        mutationFn: async ({ id, status, note }: { id: string; status: string; note?: string }) =>
            (await api.patch(`/agent/actions/${id}`, { status, note })).data,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['agent-actions'] });
            queryClient.invalidateQueries({ queryKey: ['agent-reports-latest'] });
        },
    });

    const reports = reportData?.reports ?? [];
    const actions = actionData?.actions ?? [];

    const formatTime = (value?: string) => {
        if (!value) return 'just now';
        try {
            return formatDistanceToNow(new Date(value), { addSuffix: true });
        } catch {
            return 'just now';
        }
    };

    const getSeverity = (report: AgentReport) => {
        const findingText = report.findings?.join(' ').toLowerCase() ?? '';
        if (findingText.includes('high') || findingText.includes('saturation') || findingText.includes('swap')) {
            return 'warning';
        }
        if (findingText.includes('no critical')) {
            return 'info';
        }
        return 'info';
    };

    return (
        <div className="bg-card text-card-foreground rounded-lg shadow-md h-full flex flex-col border border-border">
            <div className="p-4 border-b border-border flex items-center justify-between">
                <h3 className="font-semibold text-lg flex items-center gap-2">
                    <Brain className="w-5 h-5 text-purple-400" />
                    Agent Insights
                </h3>
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <Clock className="w-3 h-3" />
                    {reportsLoading ? 'Syncing…' : 'Live'}
                </div>
            </div>

            <div className="flex-1 overflow-y-auto p-4 space-y-4">
                <div className="space-y-3">
                    <div className="text-xs uppercase tracking-wider text-muted-foreground">Latest reports</div>
                    {reportsError && (
                        <div className="text-sm text-red-400">
                            Agent reports unavailable. Ensure the controller agent is enabled.
                        </div>
                    )}
                    {reportsLoading && (
                        <div className="text-sm text-muted-foreground">Fetching agent reports…</div>
                    )}
                    {!reportsLoading && reports.length === 0 && !reportsError && (
                        <div className="text-sm text-muted-foreground">No agent reports yet.</div>
                    )}
                    {reports.map((report) => {
                        const severity = getSeverity(report);
                        const borderColor = severity === 'warning' ? 'border-l-yellow-500' : 'border-l-blue-500';
                        const textColor = severity === 'warning' ? 'text-yellow-400' : 'text-blue-400';
                        return (
                            <div
                                key={report.id}
                                className={`p-3 rounded-md border-l-4 bg-muted/30 hover:bg-muted/50 transition-colors ${borderColor}`}
                            >
                                <div className="flex justify-between items-start mb-1">
                                    <span className={`text-sm font-medium ${textColor}`}>
                                        {report.summary || 'Agent summary'}
                                    </span>
                                    <span className="text-xs text-muted-foreground">{formatTime(report.generated_at)}</span>
                                </div>
                                <div className="text-xs text-muted-foreground mb-2">Node: {report.node_name}</div>
                                {report.findings?.length > 0 && (
                                    <ul className="text-sm text-gray-300 space-y-1 mb-2">
                                        {report.findings.slice(0, 3).map((finding, idx) => (
                                            <li key={`${report.id}-finding-${idx}`}>• {finding}</li>
                                        ))}
                                    </ul>
                                )}
                                {report.llm?.summary && (
                                    <div className="text-xs text-muted-foreground bg-background/40 border border-border rounded-md p-2">
                                        <div className="flex items-center gap-2 mb-1 text-purple-300">
                                            <ShieldAlert className="w-3 h-3" />
                                            LLM Insight
                                        </div>
                                        <div className="text-sm text-gray-300">{report.llm.summary}</div>
                                    </div>
                                )}
                            </div>
                        );
                    })}
                </div>

                <div className="space-y-3 pt-2">
                    <div className="text-xs uppercase tracking-wider text-muted-foreground">Action queue</div>
                    {actionsError && (
                        <div className="text-sm text-red-400">Action queue unavailable.</div>
                    )}
                    {actionsLoading && (
                        <div className="text-sm text-muted-foreground">Fetching actions…</div>
                    )}
                    {!actionsLoading && actions.length === 0 && !actionsError && (
                        <div className="text-sm text-muted-foreground">No actions proposed.</div>
                    )}
                    {actions.map((action) => (
                        <div key={action.id} className="p-3 rounded-md bg-muted/20 border border-border">
                            <div className="flex items-center justify-between">
                                <div className="text-sm font-medium text-foreground">{action.type}</div>
                                <span className="text-xs px-2 py-1 rounded-full bg-background/60 border border-border text-muted-foreground">
                                    {action.status}
                                </span>
                            </div>
                            <div className="text-xs text-muted-foreground mt-1">
                                {action.node_name} • {action.reason} • {(action.priority || 'normal').toUpperCase()}
                            </div>
                            <div className="flex items-center gap-2 mt-2">
                                {['acknowledged', 'in_progress', 'completed', 'dismissed'].map((status) => (
                                    <button
                                        key={`${action.id}-${status}`}
                                        className="text-xs px-2 py-1 rounded border border-border hover:bg-muted/40 transition-colors"
                                        onClick={() => updateAction.mutate({ id: action.id, status })}
                                        disabled={updateAction.isLoading}
                                    >
                                        {status.replace('_', ' ')}
                                    </button>
                                ))}
                            </div>
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
};

export default AIInsightsPanel;
