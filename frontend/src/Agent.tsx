import { FormEvent, useMemo, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Bot, PlayCircle, Shield, TerminalSquare } from 'lucide-react';
import { api } from '@/api/client';

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

const defaultQuery = 'RCA for high GPU utilization spikes on fleet';

function AgentTab() {
    const [query, setQuery] = useState(defaultQuery);
    const [node, setNode] = useState('');
    const [response, setResponse] = useState<AgentQueryResponse | null>(null);
    const [executionNote, setExecutionNote] = useState<string>('');

    const queryMutation = useMutation({
        mutationFn: async () => {
            const payload: Record<string, unknown> = { query };
            if (node.trim()) {
                payload.node = node.trim();
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

    const sortedActions = useMemo(() => {
        const actions = response?.actions ?? [];
        return [...actions].sort((a, b) => priorityScore(a.priority) - priorityScore(b.priority));
    }, [response?.actions]);

    const submit = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!query.trim() || queryMutation.isLoading) {
            return;
        }
        queryMutation.mutate();
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
