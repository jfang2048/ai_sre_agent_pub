import React, { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Cpu, HardDrive, Network, Activity, Gauge, ScrollText } from 'lucide-react';
import { fetchTopPrograms, ProgramStats } from '@/api/topPrograms';
import { formatBytes, formatCount, formatPercent, formatRate } from './metricFormat';
import { buildProcessSearchText, formatProcessContextSuffix } from './processContext';

export type ResourceCategory = 'cpu' | 'memory' | 'network' | 'disk_io' | 'disk' | 'gpu' | 'logs';

const CATEGORY_META: Record<ResourceCategory, { label: string; icon: React.ComponentType<{ className?: string }> }> = {
    cpu: { label: 'CPU', icon: Cpu },
    memory: { label: 'Memory', icon: HardDrive },
    network: { label: 'Network', icon: Network },
    disk_io: { label: 'Disk I/O', icon: Activity },
    disk: { label: 'Disk', icon: HardDrive },
    gpu: { label: 'GPU', icon: Gauge },
    logs: { label: 'Logs', icon: ScrollText },
};

type RankedRow = ProgramStats & {
    rank: number;
    primaryValue: number;
    sharePercent: number;
    valueSource: 'current' | 'total' | 'frequency';
};

interface ResourceProcessBreakdownPanelProps {
    collectorId?: string;
    category: ResourceCategory;
    onCategoryChange: (next: ResourceCategory) => void;
    triggerLabel?: string;
    processFilter?: string;
    onProcessFilterChange?: (next: string) => void;
}

export default function ResourceProcessBreakdownPanel({
    collectorId,
    category,
    onCategoryChange,
    triggerLabel,
    processFilter = '',
    onProcessFilterChange = () => undefined,
}: ResourceProcessBreakdownPanelProps) {
    const query = useQuery({
        queryKey: ['top-programs', collectorId ?? 'fleet', 60],
        queryFn: () => fetchTopPrograms({ limit: 60, collectorId }),
        refetchInterval: 5000,
    });

    const rows = useMemo<RankedRow[]>(() => {
        const ranked = query.data?.resource_pages?.[category]?.ranked ?? query.data?.by_category?.[category] ?? [];
        const filtered = collectorId ? ranked.filter((item) => item.collector_id === collectorId) : ranked;

        const prepared = filtered
            .map((item) => ({
                ...item,
                ...categoryValue(item, category),
            }))
            .filter((item) => item.primaryValue > 0)
            .sort((a, b) => {
                if (b.primaryValue !== a.primaryValue) {
                    return b.primaryValue - a.primaryValue;
                }
                if ((b.score ?? 0) !== (a.score ?? 0)) {
                    return (b.score ?? 0) - (a.score ?? 0);
                }
                return (a.name ?? '').localeCompare(b.name ?? '');
            });

        const normalizedFilter = processFilter.trim().toLowerCase();
        const visibleRows = normalizedFilter
            ? prepared.filter((row) => matchesProcessFilter(row, normalizedFilter))
            : prepared;

        const total = visibleRows.reduce((sum, row) => sum + row.primaryValue, 0);
        return visibleRows.map((row, index) => ({
            ...row,
            rank: index + 1,
            sharePercent: total > 0 ? (row.primaryValue / total) * 100 : 0,
        }));
    }, [category, collectorId, processFilter, query.data?.by_category, query.data?.resource_pages]);

    const noDataDetails = noDataGuidance(category);

    return (
        <div id="resource-breakdown-panel" className="rounded-xl border border-border bg-card p-4 shadow-sm">
            <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-3 mb-3">
                <div>
                    <div className="text-sm font-semibold">Per-Process Resource Breakdown</div>
                    <div className="text-xs text-muted-foreground">
                        Ranked high-to-low with exact values and share percentage to explain curve spikes.
                    </div>
                    {triggerLabel ? (
                        <div className="text-xs text-cyan-300 mt-1">
                            Drill-down source: {triggerLabel}
                        </div>
                    ) : null}
                </div>
                <div className="flex flex-wrap gap-2">
                    {(Object.keys(CATEGORY_META) as ResourceCategory[]).map((key) => {
                        const Icon = CATEGORY_META[key].icon;
                        return (
                            <button
                                key={key}
                                type="button"
                                onClick={() => onCategoryChange(key)}
                                className={`px-3 py-1.5 text-xs rounded-md border flex items-center gap-1.5 transition-colors ${
                                    category === key
                                        ? 'bg-primary/15 border-primary/40 text-primary'
                                        : 'bg-background border-border text-muted-foreground hover:text-foreground'
                                }`}
                            >
                                <Icon className="w-3.5 h-3.5" />
                                {CATEGORY_META[key].label}
                            </button>
                        );
                    })}
                </div>
                <div className="flex items-center gap-2">
                    <input
                        type="text"
                        value={processFilter}
                        onChange={(event) => onProcessFilterChange(event.target.value)}
                        placeholder="Filter process / pid / job / pod"
                        className="w-56 bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground"
                    />
                    {processFilter && (
                        <button
                            type="button"
                            onClick={() => onProcessFilterChange('')}
                            className="text-[11px] px-2 py-1 rounded border border-border text-muted-foreground hover:text-foreground"
                        >
                            Clear
                        </button>
                    )}
                </div>
            </div>

            {query.isLoading ? (
                <div className="text-sm text-foreground/90 py-8 text-center">Loading per-process rankings...</div>
            ) : query.isError ? (
                <div className="text-sm text-rose-300 py-8 text-center">Unable to load process breakdown.</div>
            ) : rows.length === 0 ? (
                <div className="text-sm py-6 px-4 rounded-lg border border-amber-400/30 bg-amber-500/10 text-amber-100">
                    <div className="font-medium mb-1">
                        No ranked process data for {CATEGORY_META[category].label}
                        {collectorId ? ` on collector ${collectorId}` : ''}.
                    </div>
                    <div className="text-xs text-amber-100/90">
                        {noDataDetails}
                    </div>
                </div>
            ) : (
                <div className="overflow-auto">
                    <table className="w-full text-xs">
                        <thead className="sticky top-0 bg-card border-b border-border">
                            <tr className="text-muted-foreground">
                                <th className="text-left py-2 pr-3">#</th>
                                <th className="text-left py-2 pr-3">Process</th>
                                <th className="text-right py-2 pr-3">Value</th>
                                <th className="text-right py-2 pr-3">Share</th>
                                <th className="text-left py-2 pr-3">Details</th>
                                <th className="text-right py-2">Score</th>
                            </tr>
                        </thead>
                        <tbody>
                            {rows.map((row) => (
                                <tr key={`${row.collector_id}-${row.pid || 'name'}-${row.name}-${row.rank}`} className="border-b border-border/60 hover:bg-muted/20">
                                    <td className="py-2 pr-3 text-muted-foreground">{row.rank}</td>
                                    <td className="py-2 pr-3">
                                        <div className="font-medium">{row.name}</div>
                                        <div className="text-[11px] text-muted-foreground">
                                            pid {row.pid || '—'} · {row.hostname || row.collector_id}
                                            {formatProcessContextSuffix(row)}
                                        </div>
                                    </td>
                                    <td className="py-2 pr-3 text-right font-medium">
                                        {formatPrimaryValue(row.primaryValue, category, row.valueSource)}
                                    </td>
                                    <td className="py-2 pr-3 text-right">
                                        {formatPercent(row.sharePercent, 1)}
                                    </td>
                                    <td className="py-2 pr-3 text-[11px] text-muted-foreground">
                                        {formatCategoryDetails(row, category)}
                                        {row.valueSource !== 'current' ? (
                                            <span className="ml-1 text-[10px] text-cyan-300/90">
                                                ({row.valueSource})
                                            </span>
                                        ) : null}
                                    </td>
                                    <td className="py-2 text-right text-primary font-semibold">
                                        {(row.score ?? 0).toFixed(2)}
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
        </div>
    );
}

function noDataGuidance(category: ResourceCategory): string {
    switch (category) {
        case 'network':
            return 'Set SRE_COLLECTOR_LEVEL=5 for deeper per-process attribution, then verify network/BPF telemetry is enabled on the collector host.';
        case 'gpu':
            return 'No active GPU process telemetry was observed. This is expected when the host has no NVIDIA GPU workload or when nvidia-smi/NVML is unavailable in the collector runtime.';
        case 'logs':
            return 'No attributed logs were observed in the current window. This is expected when recent logs are quiet; otherwise configure SRE_COLLECTOR_LOG_PATHS and generate a test warning (for example: logger -p user.warning "sre-agent test warning").';
        default:
            return 'Set SRE_COLLECTOR_LEVEL=5 and confirm this collector is actively reporting process telemetry.';
    }
}

function categoryValue(program: ProgramStats, category: ResourceCategory): {
    primaryValue: number;
    valueSource: 'current' | 'total' | 'frequency';
} {
    const currentValue = primaryMetricValue(program, category);
    if (currentValue > 0) {
        return { primaryValue: currentValue, valueSource: 'current' };
    }

    const categoryTotal = Number(program.category_totals?.[category] ?? 0);
    if (categoryTotal > 0) {
        return { primaryValue: categoryTotal, valueSource: 'total' };
    }

    const categoryFrequency = Number(program.category_frequency?.[category] ?? 0);
    if (categoryFrequency > 0) {
        return { primaryValue: categoryFrequency, valueSource: 'frequency' };
    }

    return { primaryValue: 0, valueSource: 'current' };
}

function primaryMetricValue(program: ProgramStats, category: ResourceCategory): number {
    switch (category) {
        case 'cpu':
            return program.cpu_percent ?? 0;
        case 'memory':
            return program.memory_bytes ?? 0;
        case 'network':
            return program.net_bytes_per_second ?? 0;
        case 'disk_io':
            return (program.disk_read_bps ?? 0) + (program.disk_write_bps ?? 0);
        case 'disk':
            return (program.disk_read_bytes_total ?? 0) + (program.disk_write_bytes_total ?? 0);
        case 'gpu':
            return program.gpu_util_sm_percent ?? 0;
        case 'logs':
            return ((program.log_errors ?? 0) * 2) + (program.log_warnings ?? 0) + (program.log_events ?? 0);
        default:
            return 0;
    }
}

function formatPrimaryValue(
    value: number,
    category: ResourceCategory,
    source: 'current' | 'total' | 'frequency',
): string {
    if (source === 'frequency') {
        return `${value.toFixed(0)} obs`;
    }

    switch (category) {
        case 'cpu':
            return formatPercent(value);
        case 'memory':
            return formatBytes(value);
        case 'network':
            return formatRate(value);
        case 'disk_io':
            return formatRate(value);
        case 'disk':
            return formatBytes(value);
        case 'gpu':
            return `${value.toFixed(1)}% SM`;
        case 'logs':
            return `${value.toFixed(0)} pts`;
        default:
            return formatCount(value);
    }
}

function formatCategoryDetails(program: ProgramStats, category: ResourceCategory): string {
    switch (category) {
        case 'cpu': {
            const scheduler = program.sched_wait_ratio && program.sched_wait_ratio > 0
                ? `sched-wait ${program.sched_wait_ratio.toFixed(2)}x`
                : 'sched-wait —';
            return `${scheduler} · mem ${formatBytes(program.memory_bytes)} · ${formatRate((program.disk_read_bps ?? 0) + (program.disk_write_bps ?? 0))} I/O`;
        }
        case 'memory':
            return `cpu ${formatPercent(program.cpu_percent)} · rss ${formatBytes(program.memory_bytes)}`;
        case 'network':
            return `queued ${formatBytes(program.net_queued_bytes)} · conns ${formatCount(program.net_connections)}`;
        case 'disk_io': {
            const blockDelay = program.block_io_delay_seconds_per_second && program.block_io_delay_seconds_per_second > 0
                ? `${program.block_io_delay_seconds_per_second.toFixed(2)}s/s blk-wait`
                : 'blk-wait —';
            return `read ${formatRate(program.disk_read_bps)} · write ${formatRate(program.disk_write_bps)} · ${blockDelay}`;
        }
        case 'disk':
            return `read ${formatBytes(program.disk_read_bytes_total)} · write ${formatBytes(program.disk_write_bytes_total)}`;
        case 'gpu':
            return `mem ${program.gpu_mem_mib ? `${program.gpu_mem_mib.toFixed(0)} MiB` : '—'} · util ${formatPercent(program.gpu_util_sm_percent)}`;
        case 'logs':
            return `${program.log_events ?? 0} events · ${program.log_errors ?? 0} errors · ${program.log_warnings ?? 0} warnings`;
        default:
            return '—';
    }
}

function matchesProcessFilter(program: ProgramStats, filter: string): boolean {
    if (!filter) {
        return true;
    }
    const haystack = buildProcessSearchText(program);
    return haystack.includes(filter);
}
