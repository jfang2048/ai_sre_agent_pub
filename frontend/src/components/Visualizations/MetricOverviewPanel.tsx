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
import { fetchFleetTimeseries, TrendSeries } from '@/api/trends';
import { formatBytes, formatMetricByUnit, formatPercent, formatRate } from './metricFormat';

type OverviewCard = {
    key: string;
    label: string;
    color: string;
    unit: string;
    value: number;
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

    const cards = useMemo(() => {
        const summary = data?.numeric_summary ?? {};
        const series = new Map((data?.series ?? []).map((item) => [item.key, item]));

        const diskRead = summary.disk_read_bytes_per_second ?? 0;
        const diskWrite = summary.disk_write_bytes_per_second ?? 0;

        const result: OverviewCard[] = [
            {
                key: 'cpu_usage_percent',
                label: 'CPU Usage',
                color: '#22d3ee',
                unit: 'percent',
                value: summary.cpu_usage_percent ?? 0,
                subtitle: `Load1 ${Number(summary.load1 ?? 0).toFixed(2)}`,
                icon: Cpu,
                series: series.get('cpu_usage_percent'),
            },
            {
                key: 'memory_used_percent',
                label: 'Memory Usage',
                color: '#34d399',
                unit: 'percent',
                value: summary.memory_used_percent ?? 0,
                subtitle: `${formatBytes(summary.memory_used_bytes)} / ${formatBytes(summary.memory_total_bytes)}`,
                icon: HardDrive,
                series: series.get('memory_used_percent'),
            },
            {
                key: 'network_rx_bytes_per_second',
                label: 'Network RX',
                color: '#60a5fa',
                unit: 'bytes_per_second',
                value: summary.network_rx_bytes_per_second ?? 0,
                subtitle: `TX ${formatRate(summary.network_tx_bytes_per_second)}`,
                icon: ArrowDownToLine,
                series: series.get('network_rx_bytes_per_second'),
            },
            {
                key: 'network_tx_bytes_per_second',
                label: 'Network TX',
                color: '#a78bfa',
                unit: 'bytes_per_second',
                value: summary.network_tx_bytes_per_second ?? 0,
                subtitle: `Total ${formatRate(summary.network_total_bytes_per_second)}`,
                icon: ArrowUpToLine,
                series: series.get('network_tx_bytes_per_second'),
            },
            {
                key: 'disk_read_bytes_per_second',
                label: 'Disk Read',
                color: '#f97316',
                unit: 'bytes_per_second',
                value: diskRead,
                subtitle: `Write ${formatRate(diskWrite)}`,
                icon: ArrowDownToLine,
                series: series.get('disk_read_bytes_per_second'),
            },
            {
                key: 'disk_write_bytes_per_second',
                label: 'Disk Write',
                color: '#fb7185',
                unit: 'bytes_per_second',
                value: diskWrite,
                subtitle: `Read ${formatRate(diskRead)}`,
                icon: ArrowUpToLine,
                series: series.get('disk_write_bytes_per_second'),
            },
            {
                key: 'procs_running',
                label: 'Running Processes',
                color: '#fb7185',
                unit: 'count',
                value: summary.procs_running ?? 0,
                subtitle: `Blocked ${Number(summary.procs_blocked ?? 0).toFixed(0)}`,
                icon: Activity,
                series: series.get('procs_running'),
            },
        ];

        return result;
    }, [data]);

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
        <div className="h-full w-full p-2 grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-3 overflow-auto">
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
                                    {card.unit === 'percent'
                                        ? formatPercent(card.value)
                                        : formatMetricByUnit(card.value, card.unit)}
                                </div>
                                <div className="text-[11px] text-muted-foreground">{card.subtitle}</div>
                            </div>
                            <div className="p-2 rounded-md border border-border" style={{ color: card.color }}>
                                <Icon className="w-4 h-4" />
                            </div>
                        </div>
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
    );
}
