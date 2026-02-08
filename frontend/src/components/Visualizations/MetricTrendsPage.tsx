import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    AlertTriangle,
    Activity,
    Cpu,
    HardDrive,
    Network,
    Workflow,
} from 'lucide-react';
import {
    CartesianGrid,
    Line,
    LineChart,
    ResponsiveContainer,
    Tooltip,
    XAxis,
    YAxis,
} from 'recharts';
import { fetchFleetNodes, fetchFleetTimeseries, FleetNode, TrendSeries } from '@/api/trends';
import { formatBytes, formatCount, formatMetricByUnit, formatPercent, formatRate } from './metricFormat';
import ResourceProcessBreakdownPanel, { ResourceCategory } from './ResourceProcessBreakdownPanel';

const WINDOW_OPTIONS = [
    { value: '15m', label: '15m' },
    { value: '30m', label: '30m' },
    { value: '1h', label: '1h' },
    { value: '3h', label: '3h' },
];

const SERIES_COLORS: Record<string, string> = {
    cpu_usage_percent: '#22d3ee',
    memory_used_percent: '#34d399',
    load1: '#f59e0b',
    network_rx_bytes_per_second: '#60a5fa',
    network_tx_bytes_per_second: '#818cf8',
    disk_read_bytes_per_second: '#f97316',
    disk_write_bytes_per_second: '#fb7185',
    procs_running: '#f43f5e',
    procs_blocked: '#ef4444',
    fd_usage_percent: '#eab308',
    gpu_utilization_percent: '#a3e635',
    gpu_memory_used_mib: '#84cc16',
};

type AnomalyRow = {
    seriesKey: string;
    series: string;
    category: ResourceCategory;
    unit: string;
    timestamp: string;
    value: number;
    zScore?: number;
};

function sortNodesByFreshness(nodes: FleetNode[]): FleetNode[] {
    return [...nodes].sort((a, b) => {
        const at = new Date(a.updated_at).getTime();
        const bt = new Date(b.updated_at).getTime();
        return bt - at;
    });
}

export default function MetricTrendsPage() {
    const [windowSize, setWindowSize] = useState('1h');
    const [collectorId, setCollectorId] = useState('');
    const [drilldownCategory, setDrilldownCategory] = useState<ResourceCategory>('cpu');
    const [drilldownSource, setDrilldownSource] = useState('');

    const nodesQuery = useQuery({
        queryKey: ['fleet-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const nodes = useMemo(() => sortNodesByFreshness(nodesQuery.data?.nodes ?? []), [nodesQuery.data?.nodes]);

    const trendsQuery = useQuery({
        queryKey: ['fleet-timeseries', collectorId, windowSize],
        queryFn: () => fetchFleetTimeseries({ collectorId: collectorId || undefined, window: windowSize, limit: 360 }),
        refetchInterval: 5000,
    });

    const trends = trendsQuery.data;
    const summary = trends?.numeric_summary ?? {};

    const anomalies = useMemo<AnomalyRow[]>(() => {
        if (!trends?.series) {
            return [];
        }
        const rows: AnomalyRow[] = [];
        trends.series.forEach((series) => {
            series.points.forEach((point) => {
                if (point.is_anomaly) {
                    rows.push({
                        seriesKey: series.key,
                        series: series.display,
                        category: resourceCategoryForSeriesKey(series.key),
                        unit: series.unit,
                        timestamp: point.timestamp,
                        value: point.value,
                        zScore: point.z_score,
                    });
                }
            });
        });
        return rows
            .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
            .slice(0, 10);
    }, [trends?.series]);

    const summaryCards = [
        {
            key: 'cpu',
            label: 'CPU',
            value: formatPercent(summary.cpu_usage_percent),
            detail: `Load1 ${Number(summary.load1 ?? 0).toFixed(2)}`,
            icon: Cpu,
            category: 'cpu' as ResourceCategory,
        },
        {
            key: 'memory',
            label: 'Memory',
            value: formatPercent(summary.memory_used_percent),
            detail: `${formatBytes(summary.memory_used_bytes)} / ${formatBytes(summary.memory_total_bytes)}`,
            icon: HardDrive,
            category: 'memory' as ResourceCategory,
        },
        {
            key: 'network',
            label: 'Network Throughput',
            value: formatRate(summary.network_total_bytes_per_second),
            detail: `RX ${formatRate(summary.network_rx_bytes_per_second)} · TX ${formatRate(summary.network_tx_bytes_per_second)}`,
            icon: Network,
            category: 'network' as ResourceCategory,
        },
        {
            key: 'disk',
            label: 'Disk Throughput',
            value: formatRate(summary.disk_total_bytes_per_second),
            detail: `R ${formatRate(summary.disk_read_bytes_per_second)} · W ${formatRate(summary.disk_write_bytes_per_second)}`,
            icon: Workflow,
            category: 'disk_io' as ResourceCategory,
        },
        {
            key: 'procs',
            label: 'Processes',
            value: formatCount(summary.procs_running),
            detail: `Blocked ${formatCount(summary.procs_blocked)}`,
            icon: Activity,
            category: 'cpu' as ResourceCategory,
        },
    ];

    const handleDrilldown = (category: ResourceCategory, source: string) => {
        setDrilldownCategory(category);
        setDrilldownSource(source);
        requestAnimationFrame(() => {
            document.getElementById('resource-breakdown-panel')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
        });
    };

    return (
        <div className="h-full overflow-auto p-4 md:p-6 space-y-4">
            <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-3">
                    <div>
                        <div className="text-lg font-semibold">Metric Trends</div>
                        <div className="text-sm text-muted-foreground">
                            ECG-style temporal curves with anomaly markers. Exact numeric cards stay visible for precise readings.
                        </div>
                    </div>
                    <div className="flex flex-wrap gap-2">
                        <select
                            value={collectorId}
                            onChange={(event) => setCollectorId(event.target.value)}
                            className="bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground"
                        >
                            <option value="">Fleet (all collectors)</option>
                            {nodes.map((node) => (
                                <option key={node.collector_id} value={node.collector_id}>
                                    {node.hostname || node.collector_id}
                                </option>
                            ))}
                        </select>
                        <select
                            value={windowSize}
                            onChange={(event) => setWindowSize(event.target.value)}
                            className="bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground"
                        >
                            {WINDOW_OPTIONS.map((option) => (
                                <option key={option.value} value={option.value}>
                                    {option.label}
                                </option>
                            ))}
                        </select>
                    </div>
                </div>
                <div className="mt-3 text-xs text-muted-foreground flex flex-wrap gap-x-4 gap-y-1">
                    <span>Collector: {collectorId ? (trends?.hostname || collectorId) : 'Fleet (all collectors)'}</span>
                    <span>Samples: {trends?.sample_count ?? 0}</span>
                    <span>Updated: {trends?.latest_at ? new Date(trends.latest_at).toLocaleTimeString() : '—'}</span>
                </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-5 gap-3">
                {summaryCards.map((card) => {
                    const Icon = card.icon;
                    return (
                        <button
                            key={card.key}
                            type="button"
                            onClick={() => handleDrilldown(card.category, `${card.label} summary`)}
                            className="rounded-xl border border-border bg-card p-3 text-left hover:border-primary/40 transition-colors shadow-sm"
                        >
                            <div className="flex items-center justify-between mb-1">
                                <div className="text-[11px] uppercase tracking-wider text-muted-foreground">{card.label}</div>
                                <Icon className="w-4 h-4 text-primary" />
                            </div>
                            <div className="text-lg font-semibold">{card.value}</div>
                            <div className="text-xs text-muted-foreground">{card.detail}</div>
                        </button>
                    );
                })}
            </div>

            <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex items-center gap-2 text-sm font-medium mb-3">
                    <AlertTriangle className="w-4 h-4 text-rose-400" />
                    Latest Detected Spikes / Anomalies
                </div>
                {anomalies.length === 0 ? (
                    <div className="text-sm text-muted-foreground">No anomalies detected in the selected window.</div>
                ) : (
                    <div className="grid grid-cols-1 lg:grid-cols-2 gap-2">
                        {anomalies.map((row) => (
                            <div key={`${row.series}-${row.timestamp}-${row.value}`} className="rounded-md border border-rose-500/30 bg-rose-500/5 px-3 py-2">
                                <div className="text-xs text-rose-300">{row.series}</div>
                                <div className="text-sm font-medium">{formatMetricByUnit(row.value, row.unit)}</div>
                                <div className="text-xs text-muted-foreground">
                                    {new Date(row.timestamp).toLocaleString()} {typeof row.zScore === 'number' ? `· z=${row.zScore.toFixed(2)}` : ''}
                                </div>
                                <button
                                    type="button"
                                    onClick={() => handleDrilldown(row.category, `${row.series} anomaly`)}
                                    className="mt-2 text-[11px] px-2 py-1 rounded border border-rose-400/40 text-rose-200 hover:bg-rose-500/10"
                                >
                                    Drill down to process ranking
                                </button>
                            </div>
                        ))}
                    </div>
                )}
            </div>

            {trendsQuery.isLoading ? (
                <div className="rounded-xl border border-border bg-card p-6 text-sm text-muted-foreground">
                    Loading trend curves...
                </div>
            ) : trendsQuery.isError || !trends ? (
                <div className="rounded-xl border border-border bg-card p-6 text-sm text-rose-300">
                    Unable to load time-series data.
                </div>
            ) : (
                <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                    {trends.series.map((series) => (
                        <MetricCurveCard
                            key={series.key}
                            series={series}
                            onDrillDown={() => handleDrilldown(resourceCategoryForSeriesKey(series.key), `${series.display} curve`)}
                        />
                    ))}
                </div>
            )}

            <ResourceProcessBreakdownPanel
                collectorId={collectorId || undefined}
                category={drilldownCategory}
                onCategoryChange={setDrilldownCategory}
                triggerLabel={drilldownSource}
            />
        </div>
    );
}

function MetricCurveCard({ series, onDrillDown }: { series: TrendSeries; onDrillDown: () => void }) {
    const color = SERIES_COLORS[series.key] ?? '#22d3ee';

    const chartData = series.points.map((point) => ({
        ts: point.timestamp,
        t: new Date(point.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
        value: point.value,
        anomalyValue: point.is_anomaly ? point.value : null,
        zScore: point.z_score,
    }));

    return (
        <div className="rounded-xl border border-border bg-card p-3 md:p-4 shadow-sm">
            <div className="flex items-center justify-between mb-2">
                <div>
                    <div className="text-sm font-semibold">{series.display}</div>
                    <div className="text-xs text-muted-foreground">
                        Latest {formatMetricByUnit(series.latest, series.unit)} · Spikes {series.spike_count}
                    </div>
                </div>
                <div className="text-right text-xs text-muted-foreground flex flex-col items-end gap-1">
                    <div>Min {formatMetricByUnit(series.min, series.unit)}</div>
                    <div>Max {formatMetricByUnit(series.max, series.unit)}</div>
                    <div className={series.change_pct >= 0 ? 'text-emerald-300' : 'text-amber-300'}>
                        Δ {series.change_pct >= 0 ? '+' : ''}{series.change_pct.toFixed(1)}%
                    </div>
                    <button
                        type="button"
                        onClick={onDrillDown}
                        className="text-[11px] px-2 py-1 rounded border border-primary/30 text-primary hover:bg-primary/10"
                    >
                        Show processes
                    </button>
                </div>
            </div>
            <div className="h-48">
                <ResponsiveContainer width="100%" height="100%">
                    <LineChart data={chartData} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
                        <CartesianGrid strokeDasharray="2 4" stroke="#334155" opacity={0.35} />
                        <XAxis
                            dataKey="t"
                            minTickGap={26}
                            tick={{ fill: '#94a3b8', fontSize: 11 }}
                            axisLine={{ stroke: '#334155' }}
                            tickLine={{ stroke: '#334155' }}
                        />
                        <YAxis
                            tickFormatter={(value: number) => shortUnit(value, series.unit)}
                            tick={{ fill: '#94a3b8', fontSize: 11 }}
                            axisLine={{ stroke: '#334155' }}
                            tickLine={{ stroke: '#334155' }}
                            width={72}
                        />
                        <Tooltip
                            labelFormatter={(label, payload) => {
                                const ts = payload?.[0]?.payload?.ts as string | undefined;
                                return ts ? new Date(ts).toLocaleString() : String(label);
                            }}
                            formatter={(value: number) => formatMetricByUnit(value, series.unit)}
                            contentStyle={{ backgroundColor: '#0b1120', border: '1px solid #334155' }}
                            labelStyle={{ color: '#e2e8f0' }}
                        />
                        <Line
                            type="monotone"
                            dataKey="value"
                            stroke={color}
                            strokeWidth={2}
                            dot={false}
                            isAnimationActive={false}
                        />
                        <Line
                            type="linear"
                            dataKey="anomalyValue"
                            stroke="transparent"
                            dot={{ r: 3, fill: '#f43f5e', stroke: '#fb7185', strokeWidth: 1 }}
                            isAnimationActive={false}
                            connectNulls={false}
                        />
                    </LineChart>
                </ResponsiveContainer>
            </div>
        </div>
    );
}

function shortUnit(value: number, unit: string): string {
    if (unit === 'bytes_per_second') {
        return formatRate(value).replace('/s', '');
    }
    if (unit === 'percent') {
        return formatPercent(value, 0);
    }
    if (unit === 'count') {
        return formatCount(value);
    }
    return Number.isFinite(value) ? value.toFixed(1) : '—';
}

function resourceCategoryForSeriesKey(key: string): ResourceCategory {
    switch (key) {
        case 'cpu_usage_percent':
        case 'load1':
        case 'fd_usage_percent':
        case 'procs_running':
        case 'procs_blocked':
            return 'cpu';
        case 'memory_used_percent':
            return 'memory';
        case 'network_rx_bytes_per_second':
        case 'network_tx_bytes_per_second':
            return 'network';
        case 'disk_read_bytes_per_second':
        case 'disk_write_bytes_per_second':
            return 'disk_io';
        case 'gpu_utilization_percent':
        case 'gpu_memory_used_mib':
            return 'gpu';
        default:
            return 'cpu';
    }
}
