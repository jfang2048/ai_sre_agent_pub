import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    AlertTriangle,
    Activity,
    BrainCircuit,
    Network,
    HardDrive,
    Cpu,
    ArrowUpRight,
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
import {
    fetchAnalysisAnomalies,
    fetchAnalysisCorrelations,
    fetchAnalysisIncidents,
    fetchAnalysisStatus,
    type AnalysisIncidentReport,
} from '@/api/analysis';
import {
    fetchFleetNode,
    fetchFleetNodes,
    fetchFleetTimeseries,
    type TrendSeries,
} from '@/api/trends';
import { formatBytes, formatCount, formatPercent, formatRate } from '@/components/Visualizations/metricFormat';
import { normalizeMetricKeyForTrends, resourceCategoryForMetricKey } from '@/components/Visualizations/resourceMetricMap';
import type { TrendsNavigationIntentInput } from '@/components/Visualizations/trendsIntent';
import K8sDrilldown from '@/components/Insights/K8sDrilldown';

interface IncidentAnalysisPageProps {
    onOpenTrends?: (intent: TrendsNavigationIntentInput) => void;
}

type ChartLine = {
    dataKey: string;
    label: string;
    unit: string;
    color: string;
    series: TrendSeries;
};

const CHART_COLORS = ['#22d3ee', '#34d399', '#f59e0b', '#fb7185', '#a3e635'];

export default function IncidentAnalysisPage({ onOpenTrends }: IncidentAnalysisPageProps) {
    const [selectedNode, setSelectedNode] = useState('');
    const [windowSize, setWindowSize] = useState('1h');

    const nodesQuery = useQuery({
        queryKey: ['fleet-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const incidentsQuery = useQuery({
        queryKey: ['analysis-incidents', selectedNode],
        queryFn: () => fetchAnalysisIncidents({ node: selectedNode || undefined, limit: 25 }),
        refetchInterval: 15000,
    });

    const anomaliesQuery = useQuery({
        queryKey: ['analysis-anomalies'],
        queryFn: fetchAnalysisAnomalies,
        refetchInterval: 15000,
    });

    const statusQuery = useQuery({
        queryKey: ['analysis-status'],
        queryFn: fetchAnalysisStatus,
        refetchInterval: 15000,
    });

    const activeNode = selectedNode
        || incidentsQuery.data?.incidents?.[0]?.node_name
        || nodesQuery.data?.nodes?.[0]?.collector_id
        || '';

    const correlationsQuery = useQuery({
        queryKey: ['analysis-correlations', activeNode],
        queryFn: () => fetchAnalysisCorrelations({ node: activeNode || undefined }),
        enabled: Boolean(activeNode),
        refetchInterval: 15000,
    });

    const trendsQuery = useQuery({
        queryKey: ['analysis-trends', activeNode, windowSize],
        queryFn: () => fetchFleetTimeseries({
            collectorId: activeNode || undefined,
            window: windowSize,
            limit: 240,
        }),
        enabled: Boolean(activeNode),
        refetchInterval: 10000,
    });

    const nodeDetailsQuery = useQuery({
        queryKey: ['analysis-node-details', activeNode],
        queryFn: () => fetchFleetNode(activeNode),
        enabled: Boolean(activeNode),
        refetchInterval: 10000,
    });

    const incidents = incidentsQuery.data?.incidents ?? [];
    const selectedIncident = incidents[0];

    const chartLines = useMemo<ChartLine[]>(() => {
        const series = trendsQuery.data?.series ?? [];
        if (series.length === 0) {
            return [];
        }
        const preferred = [
            'cpu_usage_percent',
            'memory_used_percent',
            'network_total_bytes_per_second',
            'disk_utilization_peak_percent',
            'gpu_utilization_percent',
        ];
        const selected: TrendSeries[] = [];
        for (const key of preferred) {
            const match = series.find((item) => item.key === key);
            if (match) {
                selected.push(match);
            }
        }
        if (selected.length < 4) {
            for (const item of series) {
                if (selected.find((s) => s.key === item.key)) {
                    continue;
                }
                selected.push(item);
                if (selected.length >= 5) {
                    break;
                }
            }
        }
        return selected.slice(0, 5).map((item, index) => ({
            dataKey: `metric_${index}`,
            label: item.display,
            unit: item.unit,
            color: CHART_COLORS[index % CHART_COLORS.length],
            series: item,
        }));
    }, [trendsQuery.data?.series]);

    const chartData = useMemo(() => {
        if (chartLines.length === 0) {
            return [];
        }
        const byTimestamp = new Map<string, Record<string, number | string>>();
        for (const line of chartLines) {
            for (const point of line.series.points) {
                const current = byTimestamp.get(point.timestamp) ?? { timestamp: point.timestamp };
                current[line.dataKey] = point.value;
                byTimestamp.set(point.timestamp, current);
            }
        }
        const rows = Array.from(byTimestamp.values()).sort((a, b) => {
            return String(a.timestamp).localeCompare(String(b.timestamp));
        });
        if (rows.length > 120) {
            return rows.filter((_, idx) => idx % Math.ceil(rows.length / 120) === 0);
        }
        return rows;
    }, [chartLines]);

    const topProcesses = useMemo(() => {
        const rows = nodeDetailsQuery.data?.processes ?? [];
        return [...rows]
            .sort((a, b) => (b.cpu_percent || 0) - (a.cpu_percent || 0))
            .slice(0, 10);
    }, [nodeDetailsQuery.data?.processes]);

    const ebpfSignals = useMemo(() => {
        const anomalySignals = (anomaliesQuery.data?.anomalies ?? [])
            .filter((item) => isKernelSignalMetric(item.metric_name))
            .slice(0, 8)
            .map((item) => ({
                metric: item.metric_name,
                value: item.current_value,
                expected: item.expected_value,
                source: `anomaly:${item.direction}`,
            }));

        const liveSignals = Object.entries(nodeDetailsQuery.data?.metrics ?? {})
            .filter(([metric]) => isKernelSignalMetric(metric))
            .slice(0, 8)
            .map(([metric, value]) => ({
                metric,
                value,
                expected: undefined as number | undefined,
                source: 'fleet_live',
            }));

        return [...anomalySignals, ...liveSignals].slice(0, 12);
    }, [anomaliesQuery.data?.anomalies, nodeDetailsQuery.data?.metrics]);

    const correlationGraphRows = useMemo(() => {
        return (correlationsQuery.data?.correlations ?? []).slice(0, 12).map((row) => ({
            key: `${row.metric_a}-${row.metric_b}-${row.entity_a || row.node_name}-${row.entity_b || row.node_name}`,
            left: row.entity_a || row.node_name,
            right: row.entity_b || row.node_name,
            metricA: row.metric_a,
            metricB: row.metric_b,
            coefficient: row.coefficient,
            scope: row.scope || 'node',
        }));
    }, [correlationsQuery.data?.correlations]);

    const openIncidentTrends = (incident: AnalysisIncidentReport) => {
        if (!onOpenTrends) {
            return;
        }
        const metricKey = normalizeMetricKeyForTrends(incident.primary_metric);
        onOpenTrends({
            collectorId: incident.node_name,
            metricKey,
            category: resourceCategoryForMetricKey(metricKey),
            triggerLabel: `${incident.classification} incident`,
            windowSize: '1h',
        });
    };

    return (
        <div className="space-y-4">
            <div className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center gap-3 justify-between">
                    <div>
                        <div className="text-lg font-semibold flex items-center gap-2">
                            <BrainCircuit className="w-5 h-5 text-cyan-300" />
                            Incident / Analysis
                        </div>
                        <div className="text-xs text-muted-foreground mt-1">
                            Correlated incidents, anomaly evidence, and deterministic root-cause summaries.
                        </div>
                    </div>
                    <div className="flex items-center gap-2">
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={selectedNode}
                            onChange={(event) => setSelectedNode(event.target.value)}
                        >
                            <option value="">All Nodes</option>
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
                            <option value="1h">1h</option>
                            <option value="3h">3h</option>
                        </select>
                    </div>
                </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <SummaryCard icon={AlertTriangle} label="Critical Alerts" value={formatCount(statusQuery.data?.summary?.critical)} detail="active" />
                <SummaryCard icon={Activity} label="Anomalies" value={formatCount(statusQuery.data?.summary?.anomalies)} detail="statistical + trend" />
                <SummaryCard icon={Network} label="Correlations" value={formatCount(statusQuery.data?.summary?.correlations)} detail="cross-signal" />
                <SummaryCard icon={BrainCircuit} label="Incident Reports" value={formatCount(statusQuery.data?.summary?.incidents)} detail="root cause summaries" />
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-3 gap-4">
                <section className="xl:col-span-2 rounded-lg border border-border bg-card p-4 space-y-4">
                    <div className="flex items-center justify-between">
                        <h3 className="font-semibold">Incident Summaries</h3>
                        <div className="text-xs text-muted-foreground">
                            {incidentsQuery.isLoading ? 'Loading incidents...' : `${incidents.length} incidents`}
                        </div>
                    </div>

                    {incidents.length === 0 && (
                        <div className="text-sm text-muted-foreground">No active incidents from the analysis engine.</div>
                    )}

                    <div className="space-y-3">
                        {incidents.map((incident) => (
                            <article key={incident.id} className="rounded border border-border bg-background/40 p-3 space-y-2">
                                <div className="flex flex-wrap items-center gap-2 justify-between">
                                    <div className="font-medium">
                                        {incident.what_happened}
                                    </div>
                                    <div className="flex items-center gap-2">
                                        <span className={`text-xs px-2 py-0.5 rounded border ${incident.severity === 'critical' ? 'border-rose-400 text-rose-300' : 'border-amber-300 text-amber-200'}`}>
                                            {incident.severity}
                                        </span>
                                        <span className="text-xs px-2 py-0.5 rounded border border-border text-muted-foreground">
                                            {incident.classification}
                                        </span>
                                    </div>
                                </div>
                                <div className="text-sm text-muted-foreground">
                                    Node: <span className="text-foreground">{incident.node_name}</span> · confidence {formatPercent((incident.confidence ?? 0) * 100, 0)}
                                </div>
                                <div className="text-sm">
                                    <span className="text-muted-foreground">Probable cause:</span> {incident.probable_cause}
                                </div>
                                {(incident.impacted_components?.length ?? 0) > 0 && (
                                    <div className="flex flex-wrap gap-1">
                                        {incident.impacted_components?.slice(0, 8).map((component) => (
                                            <span key={`${incident.id}-${component}`} className="text-xs px-2 py-0.5 rounded bg-muted/40 border border-border">
                                                {component}
                                            </span>
                                        ))}
                                    </div>
                                )}
                                {(incident.supporting_signals?.length ?? 0) > 0 && (
                                    <div className="text-xs text-muted-foreground space-y-1">
                                        {incident.supporting_signals?.slice(0, 4).map((signal, idx) => (
                                            <div key={`${incident.id}-sig-${idx}`}>
                                                [{signal.source}] {signal.metric || signal.signal}
                                                {typeof signal.value === 'number' ? `=${signal.value.toFixed(2)}` : ''}
                                                {signal.details ? ` · ${signal.details}` : ''}
                                            </div>
                                        ))}
                                    </div>
                                )}
                                <div className="flex flex-wrap gap-2 pt-1">
                                    <button
                                        type="button"
                                        className="text-xs px-2 py-1 rounded border border-border hover:bg-muted/50"
                                        onClick={() => openIncidentTrends(incident)}
                                    >
                                        Open trends
                                    </button>
                                    {incident.log_query && (
                                        <a
                                            className="text-xs px-2 py-1 rounded border border-border hover:bg-muted/50 inline-flex items-center gap-1"
                                            href={incident.log_query}
                                            target="_blank"
                                            rel="noreferrer"
                                        >
                                            Raw logs <ArrowUpRight className="w-3 h-3" />
                                        </a>
                                    )}
                                </div>
                            </article>
                        ))}
                    </div>
                </section>

                <section className="rounded-lg border border-border bg-card p-4 space-y-3">
                    <h3 className="font-semibold">Anomaly Stream</h3>
                    <div className="space-y-2 max-h-[520px] overflow-auto">
                        {(anomaliesQuery.data?.anomalies ?? []).slice(0, 14).map((anomaly, idx) => (
                            <div key={`${anomaly.node_name}-${anomaly.metric_name}-${idx}`} className="rounded border border-border p-2 bg-background/30">
                                <div className="text-xs font-medium">{anomaly.metric_name}</div>
                                <div className="text-xs text-muted-foreground">
                                    {anomaly.node_name} · score {anomaly.score.toFixed(2)} · {anomaly.direction}
                                </div>
                                <div className="text-xs text-muted-foreground">
                                    current {anomaly.current_value.toFixed(3)} / expected {anomaly.expected_value.toFixed(3)}
                                </div>
                            </div>
                        ))}
                        {anomaliesQuery.data?.anomalies?.length === 0 && (
                            <div className="text-sm text-muted-foreground">No current anomalies.</div>
                        )}
                    </div>
                </section>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4 space-y-3">
                    <div className="flex items-center justify-between">
                        <h3 className="font-semibold">Time-Series Context</h3>
                        <div className="text-xs text-muted-foreground">{activeNode || 'No node selected'}</div>
                    </div>
                    <div className="h-72">
                        {chartData.length > 0 ? (
                            <ResponsiveContainer width="100%" height="100%">
                                <LineChart data={chartData}>
                                    <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.2)" />
                                    <XAxis dataKey="timestamp" tick={{ fontSize: 11 }} minTickGap={28} />
                                    <YAxis tick={{ fontSize: 11 }} />
                                    <Tooltip />
                                    <Legend />
                                    {chartLines.map((line) => (
                                        <Line
                                            key={line.dataKey}
                                            type="monotone"
                                            dataKey={line.dataKey}
                                            stroke={line.color}
                                            dot={false}
                                            name={line.label}
                                            strokeWidth={2}
                                        />
                                    ))}
                                </LineChart>
                            </ResponsiveContainer>
                        ) : (
                            <div className="h-full flex items-center justify-center text-sm text-muted-foreground">
                                No trend data available for selected node.
                            </div>
                        )}
                    </div>
                </section>

                <section className="rounded-lg border border-border bg-card p-4 space-y-3">
                    <h3 className="font-semibold">Top Processes (CPU-ranked)</h3>
                    <div className="overflow-auto max-h-72">
                        <table className="min-w-full text-sm">
                            <thead className="text-xs text-muted-foreground">
                                <tr>
                                    <th className="text-left py-1">Process</th>
                                    <th className="text-right py-1">CPU</th>
                                    <th className="text-right py-1">RSS</th>
                                    <th className="text-right py-1">Read</th>
                                    <th className="text-right py-1">Write</th>
                                </tr>
                            </thead>
                            <tbody>
                                {topProcesses.map((row) => (
                                    <tr key={`${row.pid}-${row.name}`} className="border-t border-border/60">
                                        <td className="py-1">{row.name || row.pid}</td>
                                        <td className="py-1 text-right">{formatPercent(row.cpu_percent)}</td>
                                        <td className="py-1 text-right">{formatBytes(row.rss_bytes)}</td>
                                        <td className="py-1 text-right">{formatRate(row.io_read_bps)}</td>
                                        <td className="py-1 text-right">{formatRate(row.io_write_bps)}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                        {topProcesses.length === 0 && (
                            <div className="text-sm text-muted-foreground py-4">No process samples available.</div>
                        )}
                    </div>
                    <div className="rounded border border-border bg-background/30 p-2 text-xs text-muted-foreground">
                        <div className="font-medium text-foreground mb-1">Correlation highlights</div>
                        {(correlationsQuery.data?.correlations ?? []).slice(0, 4).map((item, index) => (
                            <div key={`${item.metric_a}-${item.metric_b}-${index}`}>
                                {item.metric_a} ↔ {item.metric_b} ({item.coefficient.toFixed(2)}) [{item.scope || 'node'}]
                            </div>
                        ))}
                        {(correlationsQuery.data?.correlations?.length ?? 0) === 0 && (
                            <div>No strong cross-metric correlations in current window.</div>
                        )}
                    </div>
                </section>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4 space-y-3">
                    <h3 className="font-semibold">Correlation Graph</h3>
                    <div className="text-xs text-muted-foreground">
                        Multi-signal edges across node/process/pod scopes from the latest analysis cycle.
                    </div>
                    <div className="space-y-2 max-h-72 overflow-auto">
                        {correlationGraphRows.map((edge) => (
                            <article key={edge.key} className="rounded border border-border px-2 py-1.5 text-xs">
                                <div className="font-medium">
                                    {edge.left} → {edge.right}
                                </div>
                                <div className="text-muted-foreground">
                                    {edge.metricA} ↔ {edge.metricB}
                                </div>
                                <div className="text-muted-foreground">
                                    coeff {edge.coefficient.toFixed(2)} · scope {edge.scope}
                                </div>
                            </article>
                        ))}
                        {correlationGraphRows.length === 0 && (
                            <div className="text-sm text-muted-foreground">No correlation edges for current scope.</div>
                        )}
                    </div>
                </section>

                <section className="rounded-lg border border-border bg-card p-4 space-y-3">
                    <h3 className="font-semibold">Kernel / eBPF RCA Signals</h3>
                    <div className="text-xs text-muted-foreground">
                        Network flow, syscall latency, scheduler contention, IO latency, and process attribution signals.
                    </div>
                    <div className="space-y-2 max-h-72 overflow-auto">
                        {ebpfSignals.map((signal) => (
                            <article key={`${signal.source}-${signal.metric}`} className="rounded border border-border px-2 py-1.5 text-xs">
                                <div className="font-medium">{signal.metric}</div>
                                <div className="text-muted-foreground">
                                    value {formatMetricValue(signal.value)}
                                    {typeof signal.expected === 'number' ? ` / expected ${formatMetricValue(signal.expected)}` : ''}
                                </div>
                                <div className="text-muted-foreground">source {signal.source}</div>
                            </article>
                        ))}
                        {ebpfSignals.length === 0 && (
                            <div className="text-sm text-muted-foreground">
                                No kernel/eBPF-prefixed signals in current node window.
                            </div>
                        )}
                    </div>
                </section>
            </div>

            <section className="rounded-lg border border-border bg-card p-3">
                <div className="mb-2">
                    <h3 className="font-semibold">Kubernetes Topology Drilldown</h3>
                    <div className="text-xs text-muted-foreground">
                        Pod and node hotspot ranking for pressure, restarts, pending, and GPU usage.
                    </div>
                </div>
                <div className="h-[360px] overflow-hidden rounded border border-border bg-background/40">
                    <K8sDrilldown />
                </div>
            </section>

            {selectedIncident && (
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-2">Focused Incident Summary</h3>
                    <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-sm">
                        <SummaryValue icon={Cpu} label="Primary Metric" value={selectedIncident.primary_metric || '—'} />
                        <SummaryValue icon={HardDrive} label="Window" value={`${selectedIncident.window_start} → ${selectedIncident.window_end}`} />
                        <SummaryValue icon={Network} label="Related Alerts" value={formatCount(selectedIncident.related_alert_ids?.length)} />
                    </div>
                </section>
            )}
        </div>
    );
}

function isKernelSignalMetric(metric: string): boolean {
    const normalized = metric.toLowerCase();
    return normalized.includes('ebpf')
        || normalized.includes('syscall')
        || normalized.includes('sched')
        || normalized.includes('io_latency')
        || normalized.includes('network_flow')
        || normalized.startsWith('rca_');
}

function formatMetricValue(value?: number): string {
    if (typeof value !== 'number' || Number.isNaN(value)) {
        return '—';
    }
    if (Math.abs(value) >= 100) {
        return value.toFixed(1);
    }
    if (Math.abs(value) >= 1) {
        return value.toFixed(3);
    }
    return value.toExponential(2);
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
        <div className="rounded-lg border border-border bg-card p-3">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Icon className="w-4 h-4" />
                {label}
            </div>
            <div className="text-xl font-semibold mt-1">{value}</div>
            <div className="text-xs text-muted-foreground">{detail}</div>
        </div>
    );
}

function SummaryValue({
    icon: Icon,
    label,
    value,
}: {
    icon: React.ComponentType<{ className?: string }>;
    label: string;
    value: string;
}) {
    return (
        <div className="rounded border border-border bg-background/30 p-2">
            <div className="text-xs text-muted-foreground flex items-center gap-1">
                <Icon className="w-3 h-3" />
                {label}
            </div>
            <div className="mt-1 break-all">{value}</div>
        </div>
    );
}
