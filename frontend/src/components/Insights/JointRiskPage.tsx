import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    AlertTriangle,
    Activity,
    Combine,
    ShieldAlert,
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
import { fetchJointRiskReports } from '@/api/agentWorkflows';
import {
    InvestigationEventsPanel,
    RetrievalDecisionPanel,
    TrendWatchPanel,
} from '@/components/Insights/InvestigationPanels';
import { buildPreferredRiskChartSeries, buildRiskChartData } from '@/components/Insights/riskChart';
import KnowledgeEvidencePanel from '@/components/Insights/KnowledgeEvidencePanel';
import { formatCount, formatPercent } from '@/components/Visualizations/metricFormat';

const CHART_COLORS = ['#22d3ee', '#34d399', '#f59e0b', '#fb7185', '#a78bfa'];

export default function JointRiskPage() {
    const [selectedCollector, setSelectedCollector] = useState('');
    const [windowSize, setWindowSize] = useState('45m');

    const nodesQuery = useQuery({
        queryKey: ['joint-risk-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const cachedRiskQuery = useQuery({
        queryKey: ['joint-risk-reports', selectedCollector, windowSize, 'cached'],
        queryFn: () => fetchJointRiskReports({
            collectorId: selectedCollector || undefined,
            window: windowSize,
            limit: 8,
            refresh: false,
        }),
        refetchInterval: 15000,
    });

    const riskQuery = useQuery({
        queryKey: ['joint-risk-reports', selectedCollector, windowSize, 'refresh'],
        queryFn: () => fetchJointRiskReports({
            collectorId: selectedCollector || undefined,
            window: windowSize,
            limit: 8,
            refresh: true,
        }),
        refetchInterval: 60000,
    });

    const reports = riskQuery.data?.reports ?? cachedRiskQuery.data?.reports ?? [];
    const isPrimingReport = reports.length === 0
        && (riskQuery.isLoading || riskQuery.isFetching || cachedRiskQuery.isLoading);
    const reportStatusText = isPrimingReport
        ? 'Generating latest joint-risk report...'
        : riskQuery.isFetching
            ? 'Refreshing joint-risk report...'
            : '';
    const report = reports[0];

    const chartSeries = useMemo(() => {
        return buildPreferredRiskChartSeries(
            report?.series ?? [],
            ['cpu_pressure', 'memory_pressure', 'io_latency', 'retransmit_ratio', 'log_burst'],
            5,
            'risk',
            CHART_COLORS,
        );
    }, [report?.series]);

    const chartData = useMemo(() => buildRiskChartData(chartSeries), [chartSeries]);

    return (
        <div className="space-y-4" data-testid="joint-risk-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                        <h2 className="text-lg font-semibold flex items-center gap-2">
                            <ShieldAlert className="w-5 h-5 text-amber-300" />
                            Joint Risk
                        </h2>
                        <p className="text-xs text-muted-foreground mt-1">
                            Detects when low-severity signals co-occur across process, node, pod, service, and cluster scopes.
                        </p>
                        {reportStatusText && <p className="text-xs text-muted-foreground mt-2">{reportStatusText}</p>}
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
                <SummaryCard icon={ShieldAlert} label="Risk Level" value={isPrimingReport ? 'Loading...' : report?.risk_level?.toUpperCase() ?? 'N/A'} detail={report?.summary ?? 'No report'} />
                <SummaryCard icon={Activity} label="Risk Score" value={isPrimingReport ? 'Loading...' : formatPercent((report?.risk_score ?? 0) * 100, 1)} detail="weighted model" />
                <SummaryCard icon={Combine} label="Co-occurrences" value={isPrimingReport ? 'Loading...' : formatCount(report?.cooccurrences?.length)} detail="signal groups" />
                <SummaryCard icon={AlertTriangle} label="Active Signals" value={isPrimingReport ? 'Loading...' : formatCount((report?.signals ?? []).filter((item) => item.triggered).length)} detail="triggered now" />
            </div>

            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                        <div className="text-sm font-semibold">Control-plane verdict</div>
                        <div className="mt-1 text-sm text-muted-foreground">
                            {isPrimingReport ? 'Generating verdict...' : report?.actionable_why || 'No actionable co-occurrence was promoted.'}
                        </div>
                    </div>
                    {!isPrimingReport && report?.investigation_events?.[0] && (
                        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-100">
                            <div className="font-medium">{report.investigation_events[0].title}</div>
                            {report.investigation_events[0].probable_cause && (
                                <div className="mt-1 text-xs text-amber-50/90">Probable cause: {report.investigation_events[0].probable_cause}</div>
                            )}
                        </div>
                    )}
                </div>
            </section>

            <section className="rounded-lg border border-border bg-card p-4 min-h-[320px]">
                <div className="text-sm font-semibold mb-2">Joint Risk Time Series</div>
                {isPrimingReport ? (
                    <div className="text-sm text-muted-foreground">Generating latest joint-risk trends...</div>
                ) : chartData.length === 0 ? (
                    <div className="text-sm text-muted-foreground">No time-series data yet.</div>
                ) : (
                    <div className="h-[280px]">
                        <ResponsiveContainer width="100%" height="100%">
                            <LineChart data={chartData} margin={{ top: 8, right: 20, left: 0, bottom: 0 }}>
                                <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.2)" />
                                <XAxis dataKey="timestamp" hide />
                                <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} />
                                <Tooltip
                                    formatter={(value: number, name: string) => [formatChartValue(Number(value)), name]}
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
                <InvestigationEventsPanel
                    title="Investigation Events"
                    events={report?.investigation_events}
                    emptyText={isPrimingReport ? 'Generating investigation events...' : 'No ranked investigation events were promoted for this window.'}
                />
                <TrendWatchPanel
                    title="Trend Watch"
                    trends={report?.trend_assessments}
                    emptyText={isPrimingReport ? 'Generating trend watch...' : 'No significant trends were promoted for this window.'}
                />
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Ranked Signals</h3>
                    {isPrimingReport ? (
                        <div className="text-sm text-muted-foreground">Generating ranked signals...</div>
                    ) : (report?.signals ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No risk signals available.</div>
                    ) : (
                        <div className="space-y-2">
                            {(report?.signals ?? []).slice(0, 12).map((signal) => (
                                <article key={signal.id} className="rounded border border-border bg-background/50 p-3 space-y-1">
                                    <div className="flex items-center justify-between gap-2">
                                        <div className="font-medium text-sm">{signal.name}</div>
                                        <span className={`text-[11px] px-2 py-0.5 rounded border ${signal.triggered ? 'border-amber-300 text-amber-200' : 'border-slate-500 text-slate-300'}`}>
                                            {signal.severity}
                                        </span>
                                    </div>
                                    <div className="text-xs text-muted-foreground">
                                        {signal.scope}/{signal.entity} · score {formatPercent(signal.score * 100, 1)} · delta {formatPercent(signal.delta_percent, 1)}
                                    </div>
                                    {(signal.evidence ?? []).length > 0 && (
                                        <div className="text-xs text-muted-foreground line-clamp-2">{signal.evidence?.join(' · ')}</div>
                                    )}
                                </article>
                            ))}
                        </div>
                    )}
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Correlation Drilldowns</h3>
                    {isPrimingReport ? (
                        <div className="text-sm text-muted-foreground">Generating correlation drilldowns...</div>
                    ) : (report?.cooccurrences ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No co-occurrences found in this window.</div>
                    ) : (
                        <div className="space-y-3">
                            {(report?.cooccurrences ?? []).map((row) => (
                                <article key={row.id} className="rounded border border-border bg-background/50 p-3 space-y-1">
                                    <div className="text-sm font-medium">{row.signals.join(' + ')}</div>
                                    <div className="text-xs text-muted-foreground">
                                        {row.scope}/{row.entity} · corr {formatPercent(row.correlation * 100, 0)} · combined {formatPercent(row.combined_score * 100, 1)}
                                    </div>
                                    <div className="text-xs text-muted-foreground">{row.actionable_cause}</div>
                                </article>
                            ))}
                        </div>
                    )}
                </section>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Scope Breakdown</h3>
                    {isPrimingReport ? (
                        <div className="text-sm text-muted-foreground">Generating scope risk breakdown...</div>
                    ) : (report?.scope_risks ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No scope risk breakdown available.</div>
                    ) : (
                        <div className="space-y-2">
                            {(report?.scope_risks ?? []).slice(0, 12).map((scope) => (
                                <article key={`${scope.scope}-${scope.entity}`} className="rounded border border-border bg-background/50 p-3">
                                    <div className="flex items-center justify-between gap-2">
                                        <div className="text-sm font-medium">{scope.scope}/{scope.entity}</div>
                                        <div className="text-xs text-muted-foreground">{formatPercent(scope.score * 100, 1)}</div>
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">{scope.explanation}</div>
                                    {(scope.top_signals ?? []).length > 0 && (
                                        <div className="text-xs text-muted-foreground mt-1">Signals: {(scope.top_signals ?? []).slice(0, 4).join(' · ')}</div>
                                    )}
                                </article>
                            ))}
                        </div>
                    )}
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Recommendations</h3>
                    {isPrimingReport ? (
                        <div className="text-sm text-muted-foreground">Generating recommendations...</div>
                    ) : (report?.recommendations ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No recommendations generated.</div>
                    ) : (
                        <div className="space-y-2">
                            {(report?.recommendations ?? []).slice(0, 10).map((rec) => (
                                <article key={rec.id} className="rounded border border-border bg-background/50 p-3">
                                    <div className="flex items-center justify-between gap-2">
                                        <div className="text-sm font-medium">{rec.summary}</div>
                                        <div className="text-[11px] text-muted-foreground">{rec.priority}</div>
                                    </div>
                                    {rec.details && <div className="text-xs text-muted-foreground mt-1">{rec.details}</div>}
                                    <div className="text-[11px] text-muted-foreground mt-1">
                                        safe={String(rec.safe)} · dry_run_default={String(rec.dry_run_default)} · approval={String(rec.requires_approval)}
                                    </div>
                                </article>
                            ))}
                        </div>
                    )}
                </section>
            </div>

            <section className="rounded-lg border border-border bg-card p-4">
                <h3 className="font-semibold mb-2">Workflow Stages</h3>
                <div className="text-xs text-muted-foreground">
                    {isPrimingReport
                        ? 'Generating workflow stages...'
                        : (report?.stages ?? []).map((stage) => `${stage.name}:${stage.status}`).join(' → ') || 'No workflow stages yet.'}
                </div>
            </section>

            <RetrievalDecisionPanel
                title="Knowledge Retrieval Decisions"
                decisions={report?.retrieval_decisions}
                emptyText={isPrimingReport
                    ? 'Generating retrieval decisions...'
                    : 'No retrieval attempt or skip decision was recorded for this joint-risk report.'}
            />

            <KnowledgeEvidencePanel
                title="Joint Risk Knowledge Evidence"
                summary={report?.retrieval_summary}
                confidence={report?.retrieval_confidence}
                evidenceIDs={report?.retrieval_evidence_ids}
                docs={(report?.retrieved_docs ?? []).map((doc) => ({
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
                emptyText={isPrimingReport
                    ? 'Generating joint-risk knowledge evidence...'
                    : 'No retrieved knowledge evidence was attached to this joint-risk report.'}
                testId="joint-risk-knowledge-evidence"
            />
        </div>
    );
}

function SummaryCard({
    icon: Icon,
    label,
    value,
    detail,
}: {
    icon: React.ComponentType<{ className?: string }>;
    label: string;
    value: string;
    detail: string;
}) {
    return (
        <article className="rounded-lg border border-border bg-card p-3">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Icon className="w-4 h-4" />
                {label}
            </div>
            <div className="mt-2 text-lg font-semibold">{value}</div>
            <div className="text-xs text-muted-foreground">{detail}</div>
        </article>
    );
}

function formatChartValue(value: number): string {
    if (!Number.isFinite(value)) {
        return '—';
    }
    if (Math.abs(value) >= 1000) {
        return value.toLocaleString(undefined, { maximumFractionDigits: 1 });
    }
    return value.toFixed(2);
}
