import React, { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    Area,
    AreaChart,
    ResponsiveContainer,
    Tooltip,
    XAxis,
    YAxis,
} from 'recharts';
import { Cpu, HardDrive, Activity, ArrowDownToLine, ArrowUpToLine } from 'lucide-react';
import { fetchControllerStatus } from '@/api/controlPlane';
import { fetchFleetTimeseries, FleetOperationalInsight, TelemetryQuality, TrendSeries } from '@/api/trends';
import { formatBytes, formatMetricByUnit, formatPercent, formatRate } from './metricFormat';

type OverviewCard = {
    key: string;
    label: string;
    color: string;
    unit: string;
    value: number;
    available: boolean;
    subtitle: string;
    icon: React.ComponentType<{ className?: string }>;
    series?: TrendSeries;
};

export default function MetricOverviewPanel() {
    const { data, isLoading, isError } = useQuery({
        queryKey: ['fleet-timeseries', 'overview', '30m'],
        queryFn: () => fetchFleetTimeseries({ window: '30m', limit: 180 }),
        refetchInterval: 5000,
    });
    const { data: controllerStatus } = useQuery({
        queryKey: ['controller-status', 'overview'],
        queryFn: fetchControllerStatus,
        refetchInterval: 15000,
    });

    const cards = useMemo(() => {
        const summary = data?.numeric_summary ?? {};
        const quality = data?.telemetry_quality;
        const series = new Map((data?.series ?? []).map((item) => [item.key, item]));

        const diskRead = summaryValue(summary, 'disk_read_bytes_per_second');
        const diskWrite = summaryValue(summary, 'disk_write_bytes_per_second');
        const cpuUsage = summaryValue(summary, 'cpu_usage_percent');
        const load1 = summaryValue(summary, 'load1');
        const memoryUsedPercent = summaryValue(summary, 'memory_used_percent');
        const memoryUsedBytes = summaryValue(summary, 'memory_used_bytes');
        const memoryTotalBytes = summaryValue(summary, 'memory_total_bytes');
        const networkRX = summaryValue(summary, 'network_rx_bytes_per_second');
        const networkTX = summaryValue(summary, 'network_tx_bytes_per_second');
        const networkTotal = summaryValue(summary, 'network_total_bytes_per_second');
        const procsRunning = summaryValue(summary, 'procs_running');
        const procsBlocked = summaryValue(summary, 'procs_blocked');

        const result: OverviewCard[] = [
            {
                key: 'cpu_usage_percent',
                label: 'CPU Usage',
                color: '#22d3ee',
                unit: 'percent',
                value: cpuUsage.value,
                available: cpuUsage.available,
                subtitle: load1.available ? `Load1 ${Number(load1.value).toFixed(2)}` : unavailableSubtitle(quality),
                icon: Cpu,
                series: series.get('cpu_usage_percent'),
            },
            {
                key: 'memory_used_percent',
                label: 'Memory Usage',
                color: '#34d399',
                unit: 'percent',
                value: memoryUsedPercent.value,
                available: memoryUsedPercent.available,
                subtitle: memoryUsedBytes.available && memoryTotalBytes.available
                    ? `${formatBytes(memoryUsedBytes.value)} / ${formatBytes(memoryTotalBytes.value)}`
                    : unavailableSubtitle(quality),
                icon: HardDrive,
                series: series.get('memory_used_percent'),
            },
            {
                key: 'network_rx_bytes_per_second',
                label: 'Network RX',
                color: '#60a5fa',
                unit: 'bytes_per_second',
                value: networkRX.value,
                available: networkRX.available,
                subtitle: networkTX.available ? `TX ${formatRate(networkTX.value)}` : unavailableSubtitle(quality),
                icon: ArrowDownToLine,
                series: series.get('network_rx_bytes_per_second'),
            },
            {
                key: 'network_tx_bytes_per_second',
                label: 'Network TX',
                color: '#a78bfa',
                unit: 'bytes_per_second',
                value: networkTX.value,
                available: networkTX.available,
                subtitle: networkTotal.available ? `Total ${formatRate(networkTotal.value)}` : unavailableSubtitle(quality),
                icon: ArrowUpToLine,
                series: series.get('network_tx_bytes_per_second'),
            },
            {
                key: 'disk_read_bytes_per_second',
                label: 'Disk Read',
                color: '#f97316',
                unit: 'bytes_per_second',
                value: diskRead.value,
                available: diskRead.available,
                subtitle: diskWrite.available ? `Write ${formatRate(diskWrite.value)}` : unavailableSubtitle(quality),
                icon: ArrowDownToLine,
                series: series.get('disk_read_bytes_per_second'),
            },
            {
                key: 'disk_write_bytes_per_second',
                label: 'Disk Write',
                color: '#fb7185',
                unit: 'bytes_per_second',
                value: diskWrite.value,
                available: diskWrite.available,
                subtitle: diskRead.available ? `Read ${formatRate(diskRead.value)}` : unavailableSubtitle(quality),
                icon: ArrowUpToLine,
                series: series.get('disk_write_bytes_per_second'),
            },
            {
                key: 'procs_running',
                label: 'Running Processes',
                color: '#fb7185',
                unit: 'count',
                value: procsRunning.value,
                available: procsRunning.available,
                subtitle: procsBlocked.available ? `Blocked ${Number(procsBlocked.value).toFixed(0)}` : unavailableSubtitle(quality),
                icon: Activity,
                series: series.get('procs_running'),
            },
        ];

        return result;
    }, [data]);
    const operationalInsights = data?.operational_insights ?? [];
    const telemetryQuality = data?.telemetry_quality;
    const collectorCoverage = controllerStatus?.collector_coverage;

    if (isLoading) {
        return (
            <div className="h-full w-full flex items-center justify-center text-sm text-muted-foreground">
                Loading live metrics...
            </div>
        );
    }

    if (isError || !data) {
        return (
            <div className="h-full w-full flex items-center justify-center text-sm text-rose-300">
                Metric overview unavailable
            </div>
        );
    }

    return (
        <div className="h-full w-full p-2 overflow-auto space-y-3">
            {telemetryQuality && telemetryQuality.state !== 'fresh' && (
                <TelemetryQualityBanner quality={telemetryQuality} />
            )}
            {collectorCoverage && collectorCoverage.state !== 'fresh' && (
                <CollectorCoverageBanner
                    state={collectorCoverage.state}
                    coveragePercent={collectorCoverage.coverage_percent}
                    freshCollectors={collectorCoverage.fresh_collectors}
                    totalCollectors={collectorCoverage.total_collectors}
                    partialCollectors={collectorCoverage.partial_collectors}
                    backlogCollectors={collectorCoverage.backlog_collectors}
                    qualityHint={collectorCoverage.quality_hint}
                />
            )}
            {operationalInsights.length > 0 && (
                <div className="grid grid-cols-1 xl:grid-cols-3 gap-3">
                    {operationalInsights.slice(0, 3).map((insight) => (
                        <OverviewInsightCard key={insight.key} insight={insight} />
                    ))}
                </div>
            )}
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-3">
                {cards.map((card) => {
                    const Icon = card.icon;
                    const points = (card.series?.points ?? []).map((point) => ({
                        t: new Date(point.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
                        value: point.value,
                    }));

                    return (
                        <div key={card.key} className="rounded-lg border border-border bg-background/85 p-3 flex flex-col gap-2">
                            <div className="flex items-start justify-between">
                                <div>
                                    <div className="text-[11px] uppercase tracking-wider text-muted-foreground">{card.label}</div>
                                    <div className="text-xl font-semibold text-foreground">
                                        {card.available
                                            ? card.unit === 'percent'
                                                ? formatPercent(card.value)
                                                : formatMetricByUnit(card.value, card.unit)
                                            : unavailableValueLabel(telemetryQuality)}
                                    </div>
                                    <div className="text-[11px] text-muted-foreground">{card.subtitle}</div>
                                    {card.series?.trend && (
                                        <div className="mt-2 inline-flex rounded-full border border-border px-2 py-0.5 text-[10px] uppercase tracking-wide text-slate-200">
                                            {card.series.trend}
                                        </div>
                                    )}
                                </div>
                                <div className="p-2 rounded-md border border-border" style={{ color: card.color }}>
                                    <Icon className="w-4 h-4" />
                                </div>
                            </div>
                            {card.series?.operational_hint && (
                                <div className="text-[11px] text-muted-foreground min-h-[2rem]">{card.series.operational_hint}</div>
                            )}
                            <div className="h-16">
                                {points.length > 1 ? (
                                    <ResponsiveContainer width="100%" height="100%">
                                        <AreaChart data={points} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
                                            <defs>
                                                <linearGradient id={`overview-${card.key}`} x1="0" y1="0" x2="0" y2="1">
                                                    <stop offset="0%" stopColor={card.color} stopOpacity={0.55} />
                                                    <stop offset="95%" stopColor={card.color} stopOpacity={0.02} />
                                                </linearGradient>
                                            </defs>
                                            <XAxis dataKey="t" hide />
                                            <YAxis hide />
                                            <Tooltip
                                                formatter={(value: number) => formatMetricByUnit(value, card.unit)}
                                                labelStyle={{ color: '#e5e7eb' }}
                                                contentStyle={{ backgroundColor: '#0f172a', border: '1px solid #334155' }}
                                            />
                                            <Area
                                                type="monotone"
                                                dataKey="value"
                                                stroke={card.color}
                                                strokeWidth={2}
                                                fill={`url(#overview-${card.key})`}
                                                dot={false}
                                                isAnimationActive={false}
                                            />
                                        </AreaChart>
                                    </ResponsiveContainer>
                                ) : (
                                    <div className="h-full flex items-center text-xs text-muted-foreground">Awaiting trend samples...</div>
                                )}
                            </div>
                        </div>
                    );
                })}
            </div>
        </div>
    );
}

function summaryValue(summary: Record<string, number>, ...keys: string[]) {
    for (const key of keys) {
        if (Object.prototype.hasOwnProperty.call(summary, key)) {
            return { available: true, value: summary[key] };
        }
    }
    return { available: false, value: 0 };
}

function unavailableSubtitle(quality?: TelemetryQuality): string {
    return quality?.quality_hint || 'No fresh data yet';
}

function unavailableValueLabel(quality?: TelemetryQuality): string {
    switch (quality?.state) {
        case 'stale':
            return 'Stale';
        case 'degraded':
            return 'Degraded';
        case 'delayed':
            return 'Delayed';
        case 'unavailable':
            return 'No data';
        default:
            return 'No data';
    }
}

function TelemetryQualityBanner({ quality }: { quality: TelemetryQuality }) {
    return (
        <div className={`rounded-lg border px-3 py-3 ${qualityBannerClass(quality.state)}`}>
            <div className="flex items-center justify-between gap-2">
                <div className="text-sm font-semibold">Telemetry {quality.state}</div>
                <div className="text-[10px] uppercase tracking-wide">
                    coverage {Math.round(quality.coverage_percent ?? 0)}%
                </div>
            </div>
            <div className="mt-1 text-xs text-muted-foreground">{quality.quality_hint || 'Telemetry is not fully healthy.'}</div>
        </div>
    );
}

function qualityBannerClass(state?: string): string {
    switch (state) {
        case 'stale':
        case 'unavailable':
            return 'border-rose-500/40 bg-rose-500/10';
        case 'degraded':
        case 'delayed':
            return 'border-amber-500/40 bg-amber-500/10';
        default:
            return 'border-cyan-500/30 bg-cyan-500/10';
    }
}

function CollectorCoverageBanner({
    state,
    coveragePercent,
    freshCollectors,
    totalCollectors,
    partialCollectors,
    backlogCollectors,
    qualityHint,
}: {
    state?: string;
    coveragePercent?: number;
    freshCollectors?: number;
    totalCollectors?: number;
    partialCollectors?: number;
    backlogCollectors?: number;
    qualityHint?: string;
}) {
    return (
        <div className={`rounded-lg border px-3 py-3 ${qualityBannerClass(state)}`}>
            <div className="flex items-center justify-between gap-2">
                <div className="text-sm font-semibold">Fleet coverage {state}</div>
                <div className="text-[10px] uppercase tracking-wide">
                    {freshCollectors ?? 0}/{totalCollectors ?? 0} fresh
                </div>
            </div>
            <div className="mt-1 text-xs text-muted-foreground">
                {qualityHint || 'Some collectors are partial, stale, or replaying backlog.'}
            </div>
            <div className="mt-2 text-[11px] text-muted-foreground">
                coverage {Math.round(coveragePercent ?? 0)}% · partial {partialCollectors ?? 0} · backlog {backlogCollectors ?? 0}
            </div>
        </div>
    );
}

function OverviewInsightCard({ insight }: { insight: FleetOperationalInsight }) {
    return (
        <div className={`rounded-lg border px-3 py-3 ${insightClass(insight.severity)}`}>
            <div className="flex items-center justify-between gap-2">
                <div className="text-sm font-semibold">{insight.summary}</div>
                <div className="text-[10px] uppercase tracking-wide">{insight.severity}</div>
            </div>
            <div className="mt-2 text-xs text-muted-foreground">{insight.decision}</div>
        </div>
    );
}

function insightClass(severity: string): string {
    switch (severity) {
        case 'critical':
            return 'border-rose-500/40 bg-rose-500/10';
        case 'warning':
            return 'border-amber-500/40 bg-amber-500/10';
        default:
            return 'border-cyan-500/30 bg-cyan-500/10';
    }
}
