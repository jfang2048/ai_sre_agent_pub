import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    BrainCircuit,
    FileSearch,
    ListChecks,
    Workflow,
} from 'lucide-react';
import {
    CartesianGrid,
    Legend,
    Line,
    LineChart,
    ResponsiveContainer,
    Tooltip,
    XAxis,
    YAxis,
} from 'recharts';
import { fetchFleetNodes } from '@/api/trends';
import {
    fetchJointRiskReports,
    fetchRCAWorkflowReports,
    fetchWorkflowAuditRecords,
    type RiskSeries,
} from '@/api/agentWorkflows';
import { formatCount, formatPercent } from '@/components/Visualizations/metricFormat';

const CHART_COLORS = ['#38bdf8', '#34d399', '#f59e0b', '#fb7185', '#a78bfa'];

export default function RCAPage() {
    const [selectedCollector, setSelectedCollector] = useState('');
    const [windowSize, setWindowSize] = useState('45m');

    const nodesQuery = useQuery({
        queryKey: ['rca-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const rcaQuery = useQuery({
        queryKey: ['rca-workflows', selectedCollector, windowSize],
        queryFn: () => fetchRCAWorkflowReports({
            collectorId: selectedCollector || undefined,
            window: windowSize,
            limit: 8,
            refresh: true,
        }),
        refetchInterval: 15000,
    });

    const selectedReport = rcaQuery.data?.reports?.[0];

    const riskSeriesQuery = useQuery({
        queryKey: ['rca-risk-series', selectedCollector, windowSize],
        queryFn: () => fetchJointRiskReports({
            collectorId: selectedCollector || undefined,
            window: windowSize,
            limit: 1,
            refresh: false,
        }),
        refetchInterval: 15000,
    });

    const auditQuery = useQuery({
        queryKey: ['rca-audit', selectedReport?.workflow_id],
        queryFn: () => fetchWorkflowAuditRecords(30, selectedReport?.workflow_id ?? ''),
        enabled: Boolean(selectedReport?.workflow_id),
        refetchInterval: 15000,
    });

    const chartSeries = useMemo(() => {
        const series = riskSeriesQuery.data?.reports?.[0]?.series ?? [];
        const preferred = ['cpu_pressure', 'io_latency', 'retransmit_ratio', 'log_burst'];
        const selected: RiskSeries[] = [];
        for (const key of preferred) {
            const found = series.find((row) => row.key === key);
            if (found) {
                selected.push(found);
            }
        }
        if (selected.length < 3) {
            for (const row of series) {
                if (selected.find((item) => item.key === row.key)) {
                    continue;
                }
                selected.push(row);
                if (selected.length >= 4) {
                    break;
                }
            }
        }
        return selected.map((row, index) => ({
            dataKey: `rca_${index}`,
            label: row.display,
            color: CHART_COLORS[index % CHART_COLORS.length],
            series: row,
        }));
    }, [riskSeriesQuery.data?.reports]);

    const chartData = useMemo(() => {
        if (chartSeries.length === 0) {
            return [];
        }
        const byTs = new Map<string, Record<string, number | string>>();
        for (const item of chartSeries) {
            for (const point of item.series.points) {
                const row = byTs.get(point.timestamp) ?? { timestamp: point.timestamp };
                row[item.dataKey] = point.value;
                byTs.set(point.timestamp, row);
            }
        }
        return Array.from(byTs.values()).sort((a, b) => String(a.timestamp).localeCompare(String(b.timestamp)));
    }, [chartSeries]);

    return (
        <div className="space-y-4" data-testid="rca-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                        <h2 className="text-lg font-semibold flex items-center gap-2">
                            <BrainCircuit className="w-5 h-5 text-cyan-300" />
                            RCA Workflow
                        </h2>
                        <p className="text-xs text-muted-foreground mt-1">
                            Deterministic pipeline: anomaly detection → context gathering → hypotheses → evidence → guarded remediation plan.
                        </p>
                    </div>
                    <div className="flex items-center gap-2">
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={selectedCollector}
                            onChange={(event) => setSelectedCollector(event.target.value)}
                        >
                            <option value="">Latest node</option>
                            {(nodesQuery.data?.nodes ?? []).map((node) => (
                                <option key={node.collector_id} value={node.collector_id}>
                                    {node.hostname || node.collector_id}
                                </option>
                            ))}
                        </select>
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={windowSize}
                            onChange={(event) => setWindowSize(event.target.value)}
                        >
                            <option value="30m">30m</option>
                            <option value="45m">45m</option>
                            <option value="1h">1h</option>
                            <option value="2h">2h</option>
                        </select>
                    </div>
                </div>
            </section>

            <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <SummaryCard label="Incident" value={selectedReport?.incident_id ?? 'n/a'} detail={selectedReport?.status ?? 'n/a'} />
                <SummaryCard label="Trigger" value={selectedReport?.trigger ?? 'n/a'} detail="workflow entrypoint" />
                <SummaryCard label="Hypotheses" value={formatCount(selectedReport?.hypotheses?.length)} detail="ranked root causes" />
                <SummaryCard label="Evidence" value={formatCount(selectedReport?.evidence?.length)} detail="metrics/log/probe context" />
            </div>

            <section className="rounded-lg border border-border bg-card p-4 min-h-[320px]">
                <div className="text-sm font-semibold mb-2 flex items-center gap-2">
                    <Workflow className="w-4 h-4" />
                    RCA Time Series Context
                </div>
                {chartData.length === 0 ? (
                    <div className="text-sm text-muted-foreground">No contextual series available.</div>
                ) : (
                    <div className="h-[280px]">
                        <ResponsiveContainer width="100%" height="100%">
                            <LineChart data={chartData} margin={{ top: 8, right: 20, left: 0, bottom: 0 }}>
                                <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.2)" />
                                <XAxis dataKey="timestamp" hide />
                                <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} />
                                <Tooltip
                                    formatter={(value: number, name: string) => [formatValue(Number(value)), name]}
                                    labelFormatter={(value) => new Date(String(value)).toLocaleTimeString()}
                                />
                                <Legend />
                                {chartSeries.map((item) => (
                                    <Line
                                        key={item.dataKey}
                                        type="monotone"
                                        dataKey={item.dataKey}
                                        name={item.label}
                                        stroke={item.color}
                                        dot={false}
                                        strokeWidth={2}
                                    />
                                ))}
                            </LineChart>
                        </ResponsiveContainer>
                    </div>
                )}
            </section>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Structured RCA Report</h3>
                    {!selectedReport?.structured_report ? (
                        <div className="text-sm text-muted-foreground">No structured report available.</div>
                    ) : (
                        <div className="space-y-2 mb-4">
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">Most likely cause</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    {selectedReport.structured_report.most_likely_cause || 'n/a'} · confidence {formatPercent((selectedReport.structured_report.confidence ?? 0) * 100, 1)}
                                </div>
                            </article>
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">Supporting signals</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    {(selectedReport.structured_report.supporting_signals ?? []).join(' · ') || 'n/a'}
                                </div>
                            </article>
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">Disconfirming signals</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    {(selectedReport.structured_report.disconfirming_signals ?? []).join(' · ') || 'none'}
                                </div>
                            </article>
                        </div>
                    )}

                    <h3 className="font-semibold mb-3 flex items-center gap-2"><FileSearch className="w-4 h-4" /> Ranked Hypotheses</h3>
                    {(selectedReport?.hypotheses ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No hypotheses produced yet.</div>
                    ) : (
                        <div className="space-y-2">
                            {(selectedReport?.hypotheses ?? []).map((hypothesis) => (
                                <article key={hypothesis.id} className="rounded border border-border bg-background/50 p-3">
                                    <div className="flex items-center justify-between gap-2">
                                        <div className="text-sm font-medium">#{hypothesis.rank} {hypothesis.title}</div>
                                        <div className="text-xs text-muted-foreground">{formatPercent(hypothesis.confidence * 100, 0)}</div>
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">{hypothesis.description}</div>
                                    {(hypothesis.evidence_ids ?? []).length > 0 && (
                                        <div className="text-xs text-muted-foreground mt-1">Evidence: {(hypothesis.evidence_ids ?? []).slice(0, 4).join(' · ')}</div>
                                    )}
                                </article>
                            ))}
                        </div>
                    )}
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Plan → Act → Verify Trace</h3>
                    {!selectedReport?.agent_loop ? (
                        <div className="text-sm text-muted-foreground">No agent loop trace available.</div>
                    ) : (
                        <div className="space-y-2 mb-4">
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">
                                    iterations={selectedReport.agent_loop.iterations} · replans={selectedReport.agent_loop.replans}
                                </div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    planned={selectedReport.agent_loop.steps_planned} · executed={selectedReport.agent_loop.steps_executed} · verified={selectedReport.agent_loop.steps_verified} · stop={selectedReport.agent_loop.stop_reason}
                                </div>
                            </article>
                            {(selectedReport.agent_loop.plan_steps ?? []).slice(0, 8).map((step) => (
                                <article key={step.id} className="rounded border border-border bg-background/50 p-3">
                                    <div className="text-sm font-medium">#{step.order} {step.title}</div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        {step.tool} · {step.status} · verified={String(step.verified)}
                                    </div>
                                    {step.verification_note && <div className="text-xs text-muted-foreground mt-1">{step.verification_note}</div>}
                                </article>
                            ))}
                        </div>
                    )}

                    <h3 className="font-semibold mb-3 flex items-center gap-2"><ListChecks className="w-4 h-4" /> Evidence & Recommendations</h3>
                    <div className="space-y-2 mb-4">
                        {(selectedReport?.evidence ?? []).slice(0, 8).map((evidence) => (
                            <article key={evidence.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{evidence.kind}: {evidence.summary}</div>
                                {(evidence.metric_name || evidence.snippet) && (
                                    <div className="text-xs text-muted-foreground mt-1">{evidence.metric_name || evidence.snippet}</div>
                                )}
                            </article>
                        ))}
                    </div>
                    <div className="space-y-2">
                        {(selectedReport?.recommendations ?? []).slice(0, 6).map((rec) => (
                            <article key={rec.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{rec.summary}</div>
                                {rec.details && <div className="text-xs text-muted-foreground mt-1">{rec.details}</div>}
                                <div className="text-[11px] text-muted-foreground mt-1">
                                    safe={String(rec.safe)} · dry_run_default={String(rec.dry_run_default)} · approval={String(rec.requires_approval)}
                                </div>
                            </article>
                        ))}
                    </div>
                </section>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Workflow & Tool Trace</h3>
                    <div className="text-xs text-muted-foreground mb-2">
                        {(selectedReport?.stages ?? []).map((stage) => `${stage.name}:${stage.status}`).join(' → ') || 'No stages yet.'}
                    </div>
                    <div className="space-y-2">
                        {(selectedReport?.tool_calls ?? []).slice(0, 8).map((call) => (
                            <article key={call.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{call.tool} · {call.stage}</div>
                                <div className="text-xs text-muted-foreground">{call.status} · {call.summary || call.error_message || 'n/a'}</div>
                            </article>
                        ))}
                    </div>
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Action Audit</h3>
                    {auditQuery.isLoading && <div className="text-sm text-muted-foreground">Loading audit records…</div>}
                    {!auditQuery.isLoading && (auditQuery.data?.records ?? []).length === 0 && (
                        <div className="text-sm text-muted-foreground">No workflow audit records yet.</div>
                    )}
                    <div className="space-y-2">
                        {(auditQuery.data?.records ?? []).slice(0, 10).map((record) => (
                            <article key={record.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{record.action} ({record.status})</div>
                                <div className="text-xs text-muted-foreground">
                                    {record.stage} · dry_run={String(record.dry_run)} · approval={String(record.requires_approval)}
                                </div>
                            </article>
                        ))}
                    </div>
                </section>
            </div>
        </div>
    );
}

function SummaryCard({ label, value, detail }: { label: string; value: string; detail: string }) {
    return (
        <article className="rounded-lg border border-border bg-card p-3">
            <div className="text-xs text-muted-foreground">{label}</div>
            <div className="mt-2 text-lg font-semibold">{value}</div>
            <div className="text-xs text-muted-foreground">{detail}</div>
        </article>
    );
}

function formatValue(value: number): string {
    if (!Number.isFinite(value)) {
        return '—';
    }
    if (Math.abs(value) >= 1000) {
        return value.toLocaleString(undefined, { maximumFractionDigits: 1 });
    }
    return value.toFixed(2);
}
