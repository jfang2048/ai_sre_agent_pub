import React, { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    AlertTriangle,
    BarChart3,
    Link2,
    SearchCheck,
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
import {
    fetchPotentialRiskFindings,
    type RiskSeries,
} from '@/api/agentWorkflows';
import { formatCount, formatPercent } from '@/components/Visualizations/metricFormat';

const CHART_COLORS = ['#22d3ee', '#34d399', '#f59e0b', '#fb7185', '#a78bfa'];

export default function RiskInsightsPage() {
    const [selectedCollector, setSelectedCollector] = useState('');
    const [windowSize, setWindowSize] = useState('45m');
    const [selectedFindingID, setSelectedFindingID] = useState('');

    const nodesQuery = useQuery({
        queryKey: ['risk-insights-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const findingsQuery = useQuery({
        queryKey: ['risk-insights-findings', selectedCollector, windowSize],
        queryFn: () =>
            fetchPotentialRiskFindings({
                collectorId: selectedCollector || undefined,
                window: windowSize,
                limit: 24,
                refresh: true,
            }),
        refetchInterval: 15000,
    });

    const findings = findingsQuery.data?.findings ?? [];
    useEffect(() => {
        if (findings.length === 0) {
            setSelectedFindingID('');
            return;
        }
        if (!selectedFindingID || !findings.some((row) => row.id === selectedFindingID)) {
            setSelectedFindingID(findings[0].id);
        }
    }, [findings, selectedFindingID]);

    const selectedFinding = findings.find((row) => row.id === selectedFindingID) ?? findings[0];
    const topFinding = findings[0];

    const chartSeries = useMemo(() => {
        const series = selectedFinding?.series ?? [];
        const preferred = ['cpu_pressure', 'memory_pressure', 'io_latency', 'retransmit_ratio', 'log_burst'];
        const selected: RiskSeries[] = [];
        for (const key of preferred) {
            const found = series.find((item) => item.key === key);
            if (found) {
                selected.push(found);
            }
        }
        if (selected.length < 4) {
            for (const item of series) {
                if (selected.find((row) => row.key === item.key)) {
                    continue;
                }
                selected.push(item);
                if (selected.length >= 4) {
                    break;
                }
            }
        }
        return selected.slice(0, 4).map((item, index) => ({
            dataKey: `risk_${index}`,
            label: item.display,
            color: CHART_COLORS[index % CHART_COLORS.length],
            series: item,
        }));
    }, [selectedFinding]);

    const chartData = useMemo(() => {
        if (chartSeries.length === 0) {
            return [];
        }
        const byTimestamp = new Map<string, Record<string, number | string>>();
        for (const item of chartSeries) {
            for (const point of item.series.points) {
                const row = byTimestamp.get(point.timestamp) ?? { timestamp: point.timestamp };
                row[item.dataKey] = point.value;
                byTimestamp.set(point.timestamp, row);
            }
        }
        return Array.from(byTimestamp.values()).sort((a, b) => String(a.timestamp).localeCompare(String(b.timestamp)));
    }, [chartSeries]);

    return (
        <div className="space-y-4" data-testid="risk-insights-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                        <h2 className="text-lg font-semibold flex items-center gap-2">
                            <AlertTriangle className="w-5 h-5 text-amber-300" />
                            Risk Insights
                        </h2>
                        <p className="text-xs text-muted-foreground mt-1">
                            Proactive latent-risk detection from historical metrics and logs with multi-signal correlation.
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
                <SummaryCard icon={ShieldAlert} label="Findings" value={formatCount(findings.length)} detail="ranked latent risks" />
                <SummaryCard icon={BarChart3} label="Top Confidence" value={formatPercent((topFinding?.confidence_score ?? 0) * 100, 1)} detail={topFinding?.scope ?? 'n/a'} />
                <SummaryCard icon={Link2} label="Correlations" value={formatCount(selectedFinding?.correlations?.length)} detail="co-occurring signals" />
                <SummaryCard icon={SearchCheck} label="Evidence Signals" value={formatCount(selectedFinding?.contributing_signals?.length)} detail="active low-severity inputs" />
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Ranked Potential Risks</h3>
                    {findings.length === 0 && <div className="text-sm text-muted-foreground">No potential risks generated yet.</div>}
                    <div className="space-y-2">
                        {findings.map((finding, index) => (
                            <button
                                key={finding.id}
                                type="button"
                                onClick={() => setSelectedFindingID(finding.id)}
                                className={`w-full text-left rounded border p-3 transition-colors ${
                                    finding.id === selectedFinding?.id
                                        ? 'border-amber-300 bg-amber-300/10'
                                        : 'border-border bg-background/50 hover:bg-muted/40'
                                }`}
                            >
                                <div className="flex items-center justify-between gap-2">
                                    <div className="text-sm font-medium">
                                        #{index + 1} {finding.scope}
                                    </div>
                                    <div className="text-xs text-muted-foreground">
                                        {formatPercent(finding.confidence_score * 100, 1)}
                                    </div>
                                </div>
                                <div className="text-xs text-muted-foreground mt-1 line-clamp-2">{finding.risk_summary}</div>
                                <div className="text-[11px] text-muted-foreground mt-1">
                                    window={finding.time_window} · collector={finding.collector_id || 'latest'}
                                </div>
                            </button>
                        ))}
                    </div>
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Evidence Breakdown</h3>
                    {!selectedFinding && <div className="text-sm text-muted-foreground">Select a risk finding to inspect evidence.</div>}
                    <div className="space-y-2">
                        {(selectedFinding?.contributing_signals ?? []).slice(0, 10).map((signal) => (
                            <article key={`${signal.name}-${signal.entity}`} className="rounded border border-border bg-background/50 p-3">
                                <div className="flex items-center justify-between gap-2">
                                    <div className="text-sm font-medium">{signal.name}</div>
                                    <div className="text-xs text-muted-foreground">{signal.scope}/{signal.entity}</div>
                                </div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    current={formatNumber(signal.current)} baseline={formatNumber(signal.baseline)} delta={formatPercent(signal.delta_percent, 1)} score={formatPercent(signal.score * 100, 1)}
                                </div>
                                {(signal.evidence ?? []).length > 0 && (
                                    <div className="text-xs text-muted-foreground mt-1 line-clamp-2">
                                        {(signal.evidence ?? []).join(' · ')}
                                    </div>
                                )}
                            </article>
                        ))}
                    </div>
                </section>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4 min-h-[320px]">
                    <h3 className="font-semibold mb-2">Trend Graph</h3>
                    {chartData.length === 0 ? (
                        <div className="text-sm text-muted-foreground">No trend series available.</div>
                    ) : (
                        <div className="h-[280px]">
                            <ResponsiveContainer width="100%" height="100%">
                                <LineChart data={chartData} margin={{ top: 8, right: 20, left: 0, bottom: 0 }}>
                                    <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.2)" />
                                    <XAxis dataKey="timestamp" hide />
                                    <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} />
                                    <Tooltip
                                        formatter={(value: number, name: string) => [formatNumber(Number(value)), name]}
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

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Correlation Details</h3>
                    {(selectedFinding?.correlations ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No correlation groups in this window.</div>
                    ) : (
                        <div className="space-y-2">
                            {(selectedFinding?.correlations ?? []).map((row) => (
                                <article key={row.id} className="rounded border border-border bg-background/50 p-3">
                                    <div className="text-sm font-medium">{row.signals.join(' + ')}</div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        {row.scope}/{row.entity} · window={row.window} · corr={formatPercent(row.correlation * 100, 0)} · combined={formatPercent(row.combined_score * 100, 1)}
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">{row.actionable_cause}</div>
                                </article>
                            ))}
                        </div>
                    )}
                </section>
            </div>

            <section className="rounded-lg border border-border bg-card p-4">
                <h3 className="font-semibold mb-2">Suggested Investigation Steps</h3>
                {(selectedFinding?.suggested_investigation_steps ?? []).length === 0 ? (
                    <div className="text-sm text-muted-foreground">No investigation steps generated.</div>
                ) : (
                    <div className="space-y-2">
                        {(selectedFinding?.suggested_investigation_steps ?? []).slice(0, 8).map((step) => (
                            <article key={step} className="rounded border border-border bg-background/50 p-2 text-sm text-muted-foreground">
                                {step}
                            </article>
                        ))}
                    </div>
                )}
            </section>
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

function formatNumber(value: number): string {
    if (!Number.isFinite(value)) {
        return '—';
    }
    if (Math.abs(value) >= 1000) {
        return value.toLocaleString(undefined, { maximumFractionDigits: 1 });
    }
    return value.toFixed(3);
}
