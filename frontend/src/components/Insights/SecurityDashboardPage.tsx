import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, Shield, ShieldAlert, ShieldCheck } from 'lucide-react';
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
import { fetchSecurityDashboard, type SecurityFinding } from '@/api/security';
import { formatCount, formatPercent } from '@/components/Visualizations/metricFormat';

function severityBadge(severity: string): string {
    switch (severity.toLowerCase()) {
        case 'critical':
            return 'border-rose-500/40 bg-rose-500/15 text-rose-200';
        case 'high':
            return 'border-orange-500/40 bg-orange-500/15 text-orange-200';
        case 'medium':
            return 'border-amber-500/40 bg-amber-500/15 text-amber-200';
        default:
            return 'border-sky-500/40 bg-sky-500/15 text-sky-200';
    }
}

function findingHeadline(finding?: SecurityFinding): string {
    if (!finding) {
        return 'No findings';
    }
    return `${finding.severity.toUpperCase()} · ${finding.category}`;
}

function formatTS(value?: string): string {
    if (!value) {
        return 'n/a';
    }
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) {
        return value;
    }
    return parsed.toLocaleTimeString();
}

function SecuritySummaryCard({
    icon: Icon,
    label,
    value,
    tone,
}: {
    icon: React.ComponentType<{ className?: string }>;
    label: string;
    value: string;
    tone: string;
}) {
    return (
        <article className="rounded-lg border border-border bg-card p-3">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Icon className={`w-4 h-4 ${tone}`} />
                {label}
            </div>
            <div className="text-xl font-semibold mt-1">{value}</div>
        </article>
    );
}

export default function SecurityDashboardPage() {
    const [selectedCollector, setSelectedCollector] = useState('');
    const [windowSize, setWindowSize] = useState('45m');
    const [selectedSeverity, setSelectedSeverity] = useState('');
    const [pinnedFindingID, setPinnedFindingID] = useState('');

    const nodesQuery = useQuery({
        queryKey: ['security-dashboard-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const dashboardQuery = useQuery({
        queryKey: ['security-dashboard', selectedCollector, windowSize, selectedSeverity],
        queryFn: () =>
            fetchSecurityDashboard({
                collectorId: selectedCollector || undefined,
                window: windowSize,
                severity: selectedSeverity ? (selectedSeverity as 'critical' | 'high' | 'medium' | 'low') : undefined,
                limit: 250,
            }),
        refetchInterval: 15000,
    });

    const findings = dashboardQuery.data?.findings ?? [];
    const selectedFinding = findings.find((finding) => finding.id === pinnedFindingID) ?? findings[0];

    const trendData = useMemo(() => {
        return (dashboardQuery.data?.trends ?? []).map((point) => ({
            timestamp: point.timestamp,
            total: point.total,
            critical: point.critical,
            high: point.high,
            medium: point.medium,
            low: point.low,
        }));
    }, [dashboardQuery.data?.trends]);

    const topFindings = findings.slice(0, 18);
    const summary = dashboardQuery.data?.summary;

    return (
        <div className="space-y-4" data-testid="security-dashboard-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                        <h2 className="text-lg font-semibold flex items-center gap-2">
                            <Shield className="w-5 h-5 text-emerald-300" />
                            Security Dashboard
                        </h2>
                        <p className="text-xs text-muted-foreground mt-1">
                            Evidence-based security findings correlated from metrics, logs, process/network, and kernel posture checks.
                        </p>
                    </div>
                    <div className="flex items-center gap-2">
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={selectedCollector}
                            onChange={(event) => setSelectedCollector(event.target.value)}
                        >
                            <option value="">Latest collector</option>
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
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={selectedSeverity}
                            onChange={(event) => setSelectedSeverity(event.target.value)}
                        >
                            <option value="">All severities</option>
                            <option value="critical">Critical</option>
                            <option value="high">High</option>
                            <option value="medium">Medium</option>
                            <option value="low">Low</option>
                        </select>
                    </div>
                </div>
            </section>

            <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <SecuritySummaryCard icon={AlertTriangle} label="Critical" value={formatCount(summary?.critical)} tone="text-rose-300" />
                <SecuritySummaryCard icon={ShieldCheck} label="High" value={formatCount(summary?.high)} tone="text-orange-300" />
                <SecuritySummaryCard icon={ShieldAlert} label="Medium" value={formatCount(summary?.medium)} tone="text-amber-300" />
                <SecuritySummaryCard icon={Shield} label="Low" value={formatCount(summary?.low)} tone="text-sky-300" />
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Security Findings</h3>
                    {dashboardQuery.isLoading && <div className="text-sm text-muted-foreground">Loading security findings…</div>}
                    {dashboardQuery.isError && (
                        <div className="text-sm text-rose-300">
                            Security dashboard API is unavailable.
                        </div>
                    )}
                    {topFindings.length === 0 && !dashboardQuery.isLoading && !dashboardQuery.isError && (
                        <div className="text-sm text-muted-foreground">No findings for the selected window.</div>
                    )}
                    <div className="space-y-2">
                        {topFindings.map((finding, index) => (
                            <button
                                key={finding.id}
                                type="button"
                                onClick={() => setPinnedFindingID(finding.id)}
                                className={`w-full rounded border p-3 text-left transition-colors ${
                                    finding.id === selectedFinding?.id
                                        ? 'border-emerald-300 bg-emerald-300/10'
                                        : 'border-border bg-background/50 hover:bg-muted/40'
                                }`}
                            >
                                <div className="flex items-center justify-between gap-2">
                                    <div className="text-sm font-medium">#{index + 1} {findingHeadline(finding)}</div>
                                    <span className={`rounded px-2 py-0.5 text-[10px] border ${severityBadge(finding.severity)}`}>
                                        {finding.severity}
                                    </span>
                                </div>
                                <div className="text-xs text-muted-foreground mt-1 line-clamp-2">{finding.summary}</div>
                                <div className="text-[11px] text-muted-foreground mt-1">
                                    score={formatPercent(finding.score * 100, 1)} · scope={finding.scope} · seen={formatTS(finding.observed_at)}
                                </div>
                            </button>
                        ))}
                    </div>
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Finding Evidence</h3>
                    {!selectedFinding && (
                        <div className="text-sm text-muted-foreground">Select a finding to inspect evidence and recommendations.</div>
                    )}
                    {selectedFinding && (
                        <div className="space-y-3">
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{selectedFinding.summary}</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    collector={selectedFinding.collector_id} · category={selectedFinding.category} · source={selectedFinding.source}
                                </div>
                            </article>
                            <div>
                                <h4 className="text-sm font-semibold mb-1">Evidence</h4>
                                <div className="space-y-1">
                                    {(selectedFinding.evidence ?? []).length === 0 && (
                                        <div className="text-xs text-muted-foreground">No evidence rows available.</div>
                                    )}
                                    {(selectedFinding.evidence ?? []).map((row) => (
                                        <div key={row} className="rounded border border-border bg-background/40 p-2 text-xs text-muted-foreground">
                                            {row}
                                        </div>
                                    ))}
                                </div>
                            </div>
                            <div>
                                <h4 className="text-sm font-semibold mb-1">Recommended Action</h4>
                                <div className="rounded border border-border bg-background/40 p-2 text-xs text-muted-foreground">
                                    {selectedFinding.recommended_action}
                                </div>
                            </div>
                        </div>
                    )}
                </section>
            </div>

            <section className="rounded-lg border border-border bg-card p-4 min-h-[320px]">
                <h3 className="font-semibold mb-2">Security Risk Trend</h3>
                {trendData.length === 0 ? (
                    <div className="text-sm text-muted-foreground">No trend samples for the selected window.</div>
                ) : (
                    <div className="h-[280px]">
                        <ResponsiveContainer width="100%" height="100%">
                            <LineChart data={trendData} margin={{ top: 8, right: 20, left: 0, bottom: 0 }}>
                                <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.2)" />
                                <XAxis dataKey="timestamp" hide />
                                <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} />
                                <Tooltip
                                    formatter={(value: number, name: string) => [formatCount(value), name]}
                                    labelFormatter={(value) => new Date(String(value)).toLocaleTimeString()}
                                />
                                <Legend />
                                <Line type="monotone" dataKey="total" name="Total" stroke="#22d3ee" dot={false} strokeWidth={2.3} />
                                <Line type="monotone" dataKey="critical" name="Critical" stroke="#f43f5e" dot={false} strokeWidth={1.8} />
                                <Line type="monotone" dataKey="high" name="High" stroke="#f97316" dot={false} strokeWidth={1.8} />
                                <Line type="monotone" dataKey="medium" name="Medium" stroke="#f59e0b" dot={false} strokeWidth={1.8} />
                                <Line type="monotone" dataKey="low" name="Low" stroke="#38bdf8" dot={false} strokeWidth={1.8} />
                            </LineChart>
                        </ResponsiveContainer>
                    </div>
                )}
            </section>
        </div>
    );
}
