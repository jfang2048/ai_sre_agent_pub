import React, { useEffect, useMemo, useState } from 'react';
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
import {
    fetchFleetNode,
    fetchFleetNodes,
    fetchFleetTimeseries,
    FleetNode,
    FilesystemSample,
    StorageDeviceSample,
    TrendSeries,
} from '@/api/trends';
import { formatBytes, formatCount, formatMetricByUnit, formatPercent, formatRate } from './metricFormat';
import ResourceProcessBreakdownPanel, { ResourceCategory } from './ResourceProcessBreakdownPanel';
import type { TrendsNavigationIntent } from './trendsIntent';
import {
    normalizeMetricKeyForTrends,
    resourceCategoryForMetricKey,
} from './resourceMetricMap';

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
    disk_total_iops_per_second: '#e879f9',
    disk_utilization_peak_percent: '#ef4444',
    disk_queue_depth_total: '#facc15',
    disk_avg_request_latency_ms: '#f87171',
    disk_request_latency_p50_ms: '#fb7185',
    disk_request_latency_p90_ms: '#ef4444',
    disk_request_latency_p99_ms: '#b91c1c',
    filesystem_space_pressure_percent: '#fb7185',
    filesystem_inode_pressure_percent: '#e11d48',
    pagecache_dirty_bytes: '#f59e0b',
    pagecache_writeback_bytes: '#a855f7',
    vm_pgpgin_per_second: '#38bdf8',
    vm_pgpgout_per_second: '#60a5fa',
    vm_dirtied_pages_per_second: '#f97316',
    vm_written_pages_per_second: '#fb7185',
    io_pressure_some_avg10: '#ef4444',
    io_pressure_full_avg10: '#dc2626',
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

interface MetricTrendsPageProps {
    navigationIntent?: TrendsNavigationIntent | null;
    onNavigationIntentConsumed?: () => void;
}

function sortNodesByFreshness(nodes: FleetNode[]): FleetNode[] {
    return [...nodes].sort((a, b) => {
        const at = new Date(a.updated_at).getTime();
        const bt = new Date(b.updated_at).getTime();
        return bt - at;
    });
}

export default function MetricTrendsPage({
    navigationIntent,
    onNavigationIntentConsumed,
}: MetricTrendsPageProps) {
    const [windowSize, setWindowSize] = useState('1h');
    const [collectorId, setCollectorId] = useState('');
    const [drilldownCategory, setDrilldownCategory] = useState<ResourceCategory>('cpu');
    const [drilldownSource, setDrilldownSource] = useState('');
    const [processFilter, setProcessFilter] = useState('');
    const [focusedMetricKey, setFocusedMetricKey] = useState('');

    const nodesQuery = useQuery({
        queryKey: ['fleet-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const nodes = useMemo(() => sortNodesByFreshness(nodesQuery.data?.nodes ?? []), [nodesQuery.data?.nodes]);
    const activeCollectorId = collectorId || nodes[0]?.collector_id || '';

    const trendsQuery = useQuery({
        queryKey: ['fleet-timeseries', collectorId, windowSize],
        queryFn: () => fetchFleetTimeseries({ collectorId: collectorId || undefined, window: windowSize, limit: 360 }),
        refetchInterval: 5000,
    });

    const nodeQuery = useQuery({
        queryKey: ['fleet-node', activeCollectorId],
        queryFn: () => fetchFleetNode(activeCollectorId),
        enabled: Boolean(activeCollectorId),
        refetchInterval: 5000,
    });

    const trends = trendsQuery.data;
    const summary = trends?.numeric_summary ?? {};
    const storageDevices = useMemo(
        () => rankDevices(Object.values(nodeQuery.data?.storage_devices ?? {})),
        [nodeQuery.data?.storage_devices],
    );
    const storagePartitions = useMemo(
        () => rankPartitions(Object.values(nodeQuery.data?.storage_partitions ?? {})),
        [nodeQuery.data?.storage_partitions],
    );
    const filesystemPressure = useMemo(
        () => rankFilesystems(Object.values(nodeQuery.data?.filesystems ?? {})),
        [nodeQuery.data?.filesystems],
    );

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
                        category: resourceCategoryForMetricKey(series.key),
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

    const orderedSeries = useMemo(() => {
        if (!trends?.series) {
            return [];
        }
        if (!focusedMetricKey) {
            return trends.series;
        }
        return [...trends.series].sort((a, b) => {
            const left = isMetricFocused(a.key, focusedMetricKey) ? 0 : 1;
            const right = isMetricFocused(b.key, focusedMetricKey) ? 0 : 1;
            return left - right;
        });
    }, [focusedMetricKey, trends?.series]);

    const focusedMetricLabel = useMemo(() => {
        if (!focusedMetricKey) {
            return '';
        }
        const match = trends?.series?.find((series) => series.key === focusedMetricKey);
        return match?.display || focusedMetricKey;
    }, [focusedMetricKey, trends?.series]);

    const summaryCards = [
        {
            key: 'cpu',
            label: 'CPU',
            value: formatPercent(summary.cpu_usage_percent),
            detail: `Load1 ${Number(summary.load1 ?? 0).toFixed(2)}`,
            icon: Cpu,
            category: 'cpu' as ResourceCategory,
            metricKey: 'cpu_usage_percent',
        },
        {
            key: 'memory',
            label: 'Memory',
            value: formatPercent(summary.memory_used_percent),
            detail: `${formatBytes(summary.memory_used_bytes)} / ${formatBytes(summary.memory_total_bytes)}`,
            icon: HardDrive,
            category: 'memory' as ResourceCategory,
            metricKey: 'memory_used_percent',
        },
        {
            key: 'network',
            label: 'Network Throughput',
            value: formatRate(summary.network_total_bytes_per_second),
            detail: `RX ${formatRate(summary.network_rx_bytes_per_second)} · TX ${formatRate(summary.network_tx_bytes_per_second)}`,
            icon: Network,
            category: 'network' as ResourceCategory,
            metricKey: 'network_rx_bytes_per_second',
        },
        {
            key: 'disk',
            label: 'Disk Throughput',
            value: formatRate(summary.disk_total_bytes_per_second),
            detail: `R ${formatRate(summary.disk_read_bytes_per_second)} · W ${formatRate(summary.disk_write_bytes_per_second)}`,
            icon: Workflow,
            category: 'disk_io' as ResourceCategory,
            metricKey: 'disk_read_bytes_per_second',
        },
        {
            key: 'disk-iops',
            label: 'Disk IOPS / Queue',
            value: formatCount(summary.disk_total_iops_per_second),
            detail: `Queue ${Number(summary.disk_queue_depth_total ?? 0).toFixed(2)} · Util ${formatPercent(summary.disk_utilization_peak_percent)}`,
            icon: Activity,
            category: 'disk_io' as ResourceCategory,
            metricKey: 'disk_total_iops_per_second',
        },
        {
            key: 'fs-pressure',
            label: 'FS Pressure',
            value: formatPercent(summary.filesystem_space_pressure_percent),
            detail: `Inodes ${formatPercent(summary.filesystem_inode_pressure_percent)} · Lat ${Number(summary.disk_avg_request_latency_ms ?? 0).toFixed(1)} ms`,
            icon: HardDrive,
            category: 'disk' as ResourceCategory,
            metricKey: 'filesystem_space_pressure_percent',
        },
        {
            key: 'procs',
            label: 'Processes',
            value: formatCount(summary.procs_running),
            detail: `Blocked ${formatCount(summary.procs_blocked)}`,
            icon: Activity,
            category: 'cpu' as ResourceCategory,
            metricKey: 'procs_running',
        },
    ];

    useEffect(() => {
        if (!navigationIntent) {
            return;
        }
        const normalizedMetricKey = normalizeMetricKeyForTrends(navigationIntent.metricKey)
            || navigationIntent.metricKey
            || '';
        if (navigationIntent.windowSize?.trim()) {
            setWindowSize(navigationIntent.windowSize.trim());
        }
        if (typeof navigationIntent.collectorId === 'string') {
            setCollectorId(navigationIntent.collectorId);
        }
        if (navigationIntent.category) {
            setDrilldownCategory(navigationIntent.category);
        } else if (normalizedMetricKey) {
            setDrilldownCategory(resourceCategoryForMetricKey(normalizedMetricKey));
        }
        if (typeof navigationIntent.triggerLabel === 'string') {
            setDrilldownSource(navigationIntent.triggerLabel);
        }
        setProcessFilter(navigationIntent.processFilter ?? '');
        setFocusedMetricKey(normalizedMetricKey);
        requestAnimationFrame(() => {
            document.getElementById('resource-breakdown-panel')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
        });
        onNavigationIntentConsumed?.();
    }, [navigationIntent?.requestToken, navigationIntent, onNavigationIntentConsumed]);

    const handleDrilldown = (
        category: ResourceCategory,
        source: string,
        options: { processFilter?: string; metricKey?: string } = {},
    ) => {
        setDrilldownCategory(category);
        setDrilldownSource(source);
        if (typeof options.processFilter === 'string') {
            setProcessFilter(options.processFilter);
        }
        if (typeof options.metricKey === 'string') {
            setFocusedMetricKey(options.metricKey);
        }
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
                    {focusedMetricLabel && (
                        <button
                            type="button"
                            onClick={() => setFocusedMetricKey('')}
                            className="inline-flex items-center rounded border border-cyan-400/40 px-2 py-0.5 text-cyan-200 hover:bg-cyan-500/10"
                        >
                            Focused metric: {focusedMetricLabel} (clear)
                        </button>
                    )}
                </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-7 gap-3">
                {summaryCards.map((card) => {
                    const Icon = card.icon;
                    return (
                        <button
                            key={card.key}
                            type="button"
                            onClick={() => handleDrilldown(card.category, `${card.label} summary`, { metricKey: card.metricKey })}
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
                                    onClick={() => handleDrilldown(row.category, `${row.series} anomaly`, { metricKey: row.seriesKey })}
                                    className="mt-2 text-[11px] px-2 py-1 rounded border border-rose-400/40 text-rose-200 hover:bg-rose-500/10"
                                >
                                    Drill down to process ranking
                                </button>
                            </div>
                        ))}
                    </div>
                )}
            </div>

            <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <div className="flex items-center justify-between mb-3">
                    <div>
                        <div className="text-sm font-semibold">Storage Device / Filesystem Ranking</div>
                        <div className="text-xs text-muted-foreground">
                            Ranked by live pressure so hotspot disks and pressured mounts are visible beside trend curves.
                        </div>
                    </div>
                    <div className="text-xs text-muted-foreground">
                        Source collector: {activeCollectorId || '—'}
                    </div>
                </div>

                {!activeCollectorId ? (
                    <div className="text-sm text-muted-foreground">No collector is currently available.</div>
                ) : nodeQuery.isLoading ? (
                    <div className="text-sm text-muted-foreground">Loading storage ranking...</div>
                ) : nodeQuery.isError ? (
                    <div className="text-sm text-rose-300">Unable to load storage ranking for this collector.</div>
                ) : (
                    <div className="grid grid-cols-1 xl:grid-cols-3 gap-3">
                        <StorageDeviceTable title="Hottest Devices" rows={storageDevices} />
                        <StoragePartitionTable title="Hottest Partitions" rows={storagePartitions} />
                        <FilesystemPressureTable title="Filesystem Pressure" rows={filesystemPressure} />
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
                    {orderedSeries.map((series) => (
                        <MetricCurveCard
                            key={series.key}
                            series={series}
                            highlighted={isMetricFocused(series.key, focusedMetricKey)}
                            onDrillDown={() => handleDrilldown(resourceCategoryForMetricKey(series.key), `${series.display} curve`, { metricKey: series.key })}
                        />
                    ))}
                </div>
            )}

            <ResourceProcessBreakdownPanel
                collectorId={collectorId || undefined}
                category={drilldownCategory}
                onCategoryChange={setDrilldownCategory}
                triggerLabel={drilldownSource}
                processFilter={processFilter}
                onProcessFilterChange={setProcessFilter}
            />
        </div>
    );
}

function MetricCurveCard({
    series,
    onDrillDown,
    highlighted,
}: {
    series: TrendSeries;
    onDrillDown: () => void;
    highlighted?: boolean;
}) {
    const color = SERIES_COLORS[series.key] ?? '#22d3ee';

    const chartData = series.points.map((point) => ({
        ts: point.timestamp,
        t: new Date(point.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
        value: point.value,
        anomalyValue: point.is_anomaly ? point.value : null,
        zScore: point.z_score,
    }));

    return (
        <div className={`rounded-xl border bg-card p-3 md:p-4 shadow-sm ${highlighted ? 'border-cyan-400/60' : 'border-border'}`}>
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
    if (unit === 'bytes') {
        return formatBytes(value);
    }
    if (unit === 'percent') {
        return formatPercent(value, 0);
    }
    if (unit === 'iops') {
        return formatCount(value);
    }
    if (unit === 'milliseconds') {
        return `${value.toFixed(1)}ms`;
    }
    if (unit === 'pages_per_second') {
        return `${formatCount(value)}/s`;
    }
    if (unit === 'count') {
        return formatCount(value);
    }
    return Number.isFinite(value) ? value.toFixed(1) : '—';
}

function isMetricFocused(metricKey: string, focusedMetricKey: string): boolean {
    if (!focusedMetricKey) {
        return false;
    }
    return metricKey === focusedMetricKey;
}

type RankedStorageDevice = StorageDeviceSample & {
    heat_score: number;
    throughput_bps: number;
};

type RankedFilesystem = FilesystemSample & {
    pressure_score: number;
};

function rankDevices(input: StorageDeviceSample[]): RankedStorageDevice[] {
    return input
        .filter((item) => item.scope !== 'partition')
        .map((item) => {
            const throughput = (item.read_bytes_per_second ?? 0) + (item.write_bytes_per_second ?? 0);
            const iops = item.iops ?? ((item.read_iops ?? 0) + (item.write_iops ?? 0));
            const util = item.utilization_percent ?? 0;
            const queueDepth = item.queue_depth ?? 0;
            const latency = item.avg_request_latency_ms
                ?? Math.max(item.avg_read_latency_ms ?? 0, item.avg_write_latency_ms ?? 0);
            const heat = (util * 2.5) + (queueDepth * 18) + (throughput / (1024 * 1024)) + (iops / 500) + latency;
            return { ...item, iops, heat_score: heat, throughput_bps: throughput };
        })
        .sort((a, b) => b.heat_score - a.heat_score)
        .slice(0, 10);
}

function rankPartitions(input: StorageDeviceSample[]): RankedStorageDevice[] {
    return input
        .map((item) => {
            const throughput = (item.read_bytes_per_second ?? 0) + (item.write_bytes_per_second ?? 0);
            const iops = item.iops ?? ((item.read_iops ?? 0) + (item.write_iops ?? 0));
            const latency = item.avg_request_latency_ms
                ?? Math.max(item.avg_read_latency_ms ?? 0, item.avg_write_latency_ms ?? 0);
            const heat = (throughput / (1024 * 1024)) + (iops / 500) + latency;
            return { ...item, iops, heat_score: heat, throughput_bps: throughput };
        })
        .sort((a, b) => b.heat_score - a.heat_score)
        .slice(0, 10);
}

function rankFilesystems(input: FilesystemSample[]): RankedFilesystem[] {
    return input
        .map((item) => {
            const space = item.used_percent ?? 0;
            const inode = item.files_used_percent ?? 0;
            const pressure = (space * 0.7) + (inode * 0.3);
            return { ...item, pressure_score: pressure };
        })
        .sort((a, b) => b.pressure_score - a.pressure_score)
        .slice(0, 10);
}

function StorageDeviceTable({ title, rows }: { title: string; rows: RankedStorageDevice[] }) {
    return (
        <div className="rounded-lg border border-border/70 bg-background/40 p-3">
            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">{title}</div>
            {rows.length === 0 ? (
                <div className="text-xs text-muted-foreground">No device-level metrics available yet.</div>
            ) : (
                <div className="overflow-auto">
                    <table className="w-full text-xs">
                        <thead className="text-muted-foreground border-b border-border/50">
                            <tr>
                                <th className="text-left py-1 pr-2">Device</th>
                                <th className="text-right py-1 pr-2">Throughput</th>
                                <th className="text-right py-1 pr-2">IOPS</th>
                                <th className="text-right py-1 pr-2">Util</th>
                                <th className="text-right py-1 pr-2">QD</th>
                                <th className="text-right py-1">Lat</th>
                            </tr>
                        </thead>
                        <tbody>
                            {rows.map((row) => (
                                <tr key={row.device} className="border-b border-border/40">
                                    <td className="py-1 pr-2 font-medium">{row.device}</td>
                                    <td className="py-1 pr-2 text-right">{formatRate(row.throughput_bps)}</td>
                                    <td className="py-1 pr-2 text-right">{formatCount(row.iops)}</td>
                                    <td className="py-1 pr-2 text-right">{formatPercent(row.utilization_percent)}</td>
                                    <td className="py-1 pr-2 text-right">{Number(row.queue_depth ?? 0).toFixed(2)}</td>
                                    <td className="py-1 text-right">{Number(row.avg_request_latency_ms ?? 0).toFixed(1)} ms</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
        </div>
    );
}

function StoragePartitionTable({ title, rows }: { title: string; rows: RankedStorageDevice[] }) {
    return (
        <div className="rounded-lg border border-border/70 bg-background/40 p-3">
            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">{title}</div>
            {rows.length === 0 ? (
                <div className="text-xs text-muted-foreground">No partition-level metrics available yet.</div>
            ) : (
                <div className="overflow-auto">
                    <table className="w-full text-xs">
                        <thead className="text-muted-foreground border-b border-border/50">
                            <tr>
                                <th className="text-left py-1 pr-2">Partition</th>
                                <th className="text-left py-1 pr-2">Disk</th>
                                <th className="text-right py-1 pr-2">Throughput</th>
                                <th className="text-right py-1 pr-2">Read</th>
                                <th className="text-right py-1 pr-2">Write</th>
                                <th className="text-right py-1">IOPS</th>
                            </tr>
                        </thead>
                        <tbody>
                            {rows.map((row) => (
                                <tr key={row.partition || row.device} className="border-b border-border/40">
                                    <td className="py-1 pr-2 font-medium">{row.partition || row.device}</td>
                                    <td className="py-1 pr-2 text-muted-foreground">{row.parent_device || row.device}</td>
                                    <td className="py-1 pr-2 text-right">{formatRate(row.throughput_bps)}</td>
                                    <td className="py-1 pr-2 text-right">{formatRate(row.read_bytes_per_second)}</td>
                                    <td className="py-1 pr-2 text-right">{formatRate(row.write_bytes_per_second)}</td>
                                    <td className="py-1 text-right">{formatCount(row.iops)}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
        </div>
    );
}

function FilesystemPressureTable({ title, rows }: { title: string; rows: RankedFilesystem[] }) {
    return (
        <div className="rounded-lg border border-border/70 bg-background/40 p-3">
            <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">{title}</div>
            {rows.length === 0 ? (
                <div className="text-xs text-muted-foreground">No filesystem metrics available yet.</div>
            ) : (
                <div className="overflow-auto">
                    <table className="w-full text-xs">
                        <thead className="text-muted-foreground border-b border-border/50">
                            <tr>
                                <th className="text-left py-1 pr-2">Mount</th>
                                <th className="text-right py-1 pr-2">Used</th>
                                <th className="text-right py-1 pr-2">Inodes</th>
                                <th className="text-right py-1 pr-2">Avail</th>
                                <th className="text-left py-1">Type</th>
                            </tr>
                        </thead>
                        <tbody>
                            {rows.map((row) => (
                                <tr key={row.mountpoint} className="border-b border-border/40">
                                    <td className="py-1 pr-2 font-medium">{row.mountpoint}</td>
                                    <td className="py-1 pr-2 text-right">{formatPercent(row.used_percent)}</td>
                                    <td className="py-1 pr-2 text-right">{formatPercent(row.files_used_percent)}</td>
                                    <td className="py-1 pr-2 text-right">{formatBytes(row.avail_bytes)}</td>
                                    <td className="py-1 text-muted-foreground">{row.fstype || '—'}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
        </div>
    );
}
