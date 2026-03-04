import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ClipboardList, PlayCircle, ShieldCheck, Wrench } from 'lucide-react';
import { fetchWorkflowAuditRecords } from '@/api/agentWorkflows';
import {
    fetchControllerAuditRecords,
    fetchControllerRuns,
    fetchControllerToolRegistry,
} from '@/api/controller';
import { formatCount } from '@/components/Visualizations/metricFormat';

type AuditSource = 'all' | 'controller' | 'workflow';

function formatTS(value?: string): string {
    if (!value) {
        return 'n/a';
    }
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) {
        return value;
    }
    return parsed.toLocaleString();
}

function statusTone(status: string): string {
    switch (status.toLowerCase()) {
        case 'failed':
        case 'forbidden':
        case 'blocked':
            return 'text-rose-300';
        case 'success':
        case 'approved':
        case 'completed':
        case 'executed':
            return 'text-emerald-300';
        default:
            return 'text-amber-200';
    }
}

export default function AuditLogPage() {
    const [sourceFilter, setSourceFilter] = useState<AuditSource>('all');

    const controllerAuditQuery = useQuery({
        queryKey: ['controller-audit-log'],
        queryFn: () => fetchControllerAuditRecords({ limit: 120 }),
        refetchInterval: 10000,
    });

    const workflowAuditQuery = useQuery({
        queryKey: ['workflow-audit-log'],
        queryFn: () => fetchWorkflowAuditRecords(120),
        refetchInterval: 10000,
    });

    const runsQuery = useQuery({
        queryKey: ['controller-runs'],
        queryFn: () => fetchControllerRuns(60),
        refetchInterval: 10000,
    });

    const toolsQuery = useQuery({
        queryKey: ['controller-tool-registry'],
        queryFn: fetchControllerToolRegistry,
        refetchInterval: 30000,
    });

    const mergedAuditRows = useMemo(() => {
        const controllerRows = (controllerAuditQuery.data?.records ?? []).map((row) => ({
            id: row.id,
            source: 'controller' as const,
            timestamp: row.occurred_at,
            action: row.action,
            status: row.status,
            actor: row.actor,
            summary: row.output || row.resource,
        }));
        const workflowRows = (workflowAuditQuery.data?.records ?? []).map((row) => ({
            id: row.id,
            source: 'workflow' as const,
            timestamp: row.timestamp,
            action: `${row.stage}/${row.action}`,
            status: row.status,
            actor: row.tool || 'workflow',
            summary: row.output_summary || row.workflow_id,
        }));
        const rows = [...controllerRows, ...workflowRows];
        rows.sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime());
        return rows;
    }, [controllerAuditQuery.data?.records, workflowAuditQuery.data?.records]);

    const filteredRows = mergedAuditRows.filter((row) => sourceFilter === 'all' || row.source === sourceFilter);

    return (
        <div className="space-y-4" data-testid="audit-log-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                        <h2 className="text-lg font-semibold flex items-center gap-2">
                            <ClipboardList className="w-5 h-5 text-cyan-300" />
                            Action Audit Log
                        </h2>
                        <p className="text-xs text-muted-foreground mt-1">
                            Unified audit trail for controller actions, workflow tool calls, approvals, and run lifecycle state.
                        </p>
                    </div>
                    <div className="flex items-center gap-2">
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={sourceFilter}
                            onChange={(event) => setSourceFilter(event.target.value as AuditSource)}
                        >
                            <option value="all">All sources</option>
                            <option value="controller">Controller API</option>
                            <option value="workflow">Workflow engine</option>
                        </select>
                    </div>
                </div>
            </section>

            <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <article className="rounded-lg border border-border bg-card p-3">
                    <div className="text-xs text-muted-foreground flex items-center gap-2">
                        <ShieldCheck className="w-4 h-4 text-emerald-300" />
                        Controller Records
                    </div>
                    <div className="text-xl font-semibold mt-1">{formatCount(controllerAuditQuery.data?.count ?? 0)}</div>
                </article>
                <article className="rounded-lg border border-border bg-card p-3">
                    <div className="text-xs text-muted-foreground flex items-center gap-2">
                        <Wrench className="w-4 h-4 text-violet-300" />
                        Workflow Records
                    </div>
                    <div className="text-xl font-semibold mt-1">{formatCount(workflowAuditQuery.data?.count ?? 0)}</div>
                </article>
                <article className="rounded-lg border border-border bg-card p-3">
                    <div className="text-xs text-muted-foreground flex items-center gap-2">
                        <PlayCircle className="w-4 h-4 text-sky-300" />
                        Agent Runs
                    </div>
                    <div className="text-xl font-semibold mt-1">{formatCount(runsQuery.data?.count ?? 0)}</div>
                </article>
                <article className="rounded-lg border border-border bg-card p-3">
                    <div className="text-xs text-muted-foreground flex items-center gap-2">
                        <Wrench className="w-4 h-4 text-amber-300" />
                        Registered Tools
                    </div>
                    <div className="text-xl font-semibold mt-1">{formatCount(toolsQuery.data?.count ?? 0)}</div>
                </article>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Recent Audit Events</h3>
                    {controllerAuditQuery.isLoading || workflowAuditQuery.isLoading ? (
                        <div className="text-sm text-muted-foreground">Loading audit records…</div>
                    ) : filteredRows.length === 0 ? (
                        <div className="text-sm text-muted-foreground">No audit records available.</div>
                    ) : (
                        <div className="space-y-2 max-h-[460px] overflow-auto pr-1">
                            {filteredRows.slice(0, 120).map((row) => (
                                <article key={`${row.source}-${row.id}`} className="rounded border border-border bg-background/50 p-3">
                                    <div className="flex items-center justify-between gap-2">
                                        <div className="text-sm font-medium">{row.action}</div>
                                        <span className={`text-xs font-medium ${statusTone(row.status)}`}>{row.status}</span>
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        {row.source} · actor={row.actor} · {formatTS(row.timestamp)}
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1 line-clamp-2">{row.summary}</div>
                                </article>
                            ))}
                        </div>
                    )}
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Controller Runs</h3>
                    {runsQuery.isLoading ? (
                        <div className="text-sm text-muted-foreground">Loading runs…</div>
                    ) : (runsQuery.data?.runs ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No runs recorded.</div>
                    ) : (
                        <div className="space-y-2 max-h-[220px] overflow-auto pr-1">
                            {(runsQuery.data?.runs ?? []).slice(0, 20).map((run) => (
                                <article key={run.run_id} className="rounded border border-border bg-background/50 p-3">
                                    <div className="flex items-center justify-between gap-2">
                                        <div className="text-sm font-medium">{run.workflow_type} · {run.run_id}</div>
                                        <span className={`text-xs font-medium ${statusTone(run.status)}`}>{run.status}</span>
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        collector={run.collector_id || 'n/a'} · trigger={run.trigger || 'manual'} · dry_run={String(run.dry_run)}
                                    </div>
                                    {run.summary && <div className="text-xs text-muted-foreground mt-1 line-clamp-2">{run.summary}</div>}
                                </article>
                            ))}
                        </div>
                    )}

                    <h3 className="font-semibold mt-5 mb-3">Tool Registry</h3>
                    {toolsQuery.isLoading ? (
                        <div className="text-sm text-muted-foreground">Loading tool registry…</div>
                    ) : (toolsQuery.data?.tools ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No tools registered.</div>
                    ) : (
                        <div className="space-y-2 max-h-[220px] overflow-auto pr-1">
                            {(toolsQuery.data?.tools ?? []).map((tool) => (
                                <article key={`${tool.name}-${tool.version}`} className="rounded border border-border bg-background/50 p-3">
                                    <div className="text-sm font-medium">{tool.name} {tool.version}</div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        deterministic={String(tool.deterministic)} · read_only={String(tool.read_only)} · requires_approval={String(tool.requires_approval)}
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1 line-clamp-2">{tool.description}</div>
                                </article>
                            ))}
                        </div>
                    )}
                </section>
            </div>
        </div>
    );
}
