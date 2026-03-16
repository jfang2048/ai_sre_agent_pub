import React, { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, Clock3, Filter, Search, Terminal } from 'lucide-react';
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { fetchFleetNodes } from '@/api/trends';
import { fetchLogs, fetchLogStatus, LogEntry, LogMetricCorrelation } from '@/api/logs';

const WINDOW_OPTIONS = [
    { value: '15m', label: '15m' },
    { value: '30m', label: '30m' },
    { value: '1h', label: '1h' },
    { value: '3h', label: '3h' },
];

const LEVEL_OPTIONS = [
    { value: '', label: 'All levels' },
    { value: 'fatal', label: 'Fatal' },
    { value: 'error', label: 'Error' },
    { value: 'warn', label: 'Warn' },
    { value: 'info', label: 'Info' },
    { value: 'debug', label: 'Debug' },
];

function useDebouncedValue<T>(value: T, delayMs: number): T {
    const [debounced, setDebounced] = useState(value);
    useEffect(() => {
        const timer = setTimeout(() => setDebounced(value), delayMs);
        return () => clearTimeout(timer);
    }, [value, delayMs]);
    return debounced;
}

function levelBadge(level?: string): string {
    switch ((level ?? '').toLowerCase()) {
        case 'fatal':
        case 'error':
            return 'bg-rose-500/15 text-rose-300 border border-rose-500/40';
        case 'warn':
            return 'bg-amber-500/15 text-amber-200 border border-amber-500/40';
        case 'debug':
            return 'bg-sky-500/15 text-sky-200 border border-sky-500/40';
        case 'info':
            return 'bg-emerald-500/15 text-emerald-200 border border-emerald-500/40';
        default:
            return 'bg-muted text-muted-foreground border border-border';
    }
}

function formatTimestamp(value?: string): string {
    if (!value) {
        return '—';
    }
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) {
        return value;
    }
    return parsed.toLocaleTimeString();
}

function formatMetric(metric: LogMetricCorrelation): string {
    return `${metric.metric}: ${metric.uplift_percent.toFixed(1)}%`;
}

function formatMetricValue(value: number): string {
    if (!Number.isFinite(value)) {
        return '—';
    }
    const abs = Math.abs(value);
    if (abs >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(2)}G`;
    if (abs >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
    if (abs >= 1_000) return `${(value / 1_000).toFixed(1)}k`;
    return value.toFixed(2);
}

function summarizeEntryMetrics(entry: LogEntry): string {
    const metrics = Object.entries(entry.metrics ?? {});
    if (metrics.length === 0) {
        return '';
    }
    const top = metrics.slice(0, 2).map(([key, value]) => `${key}=${formatMetricValue(value)}`);
    return top.join(' · ');
}

export default function LogsExplorerPanel() {
    const [collectorId, setCollectorId] = useState('');
    const [serviceFilter, setServiceFilter] = useState('');
    const [queryText, setQueryText] = useState('');
    const [level, setLevel] = useState('');
    const [windowSize, setWindowSize] = useState('30m');

    const debouncedText = useDebouncedValue(queryText, 250);
    const debouncedService = useDebouncedValue(serviceFilter, 250);

    const fleetQuery = useQuery({
        queryKey: ['fleet-nodes', 'logs-explorer'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const statusQuery = useQuery({
        queryKey: ['logs-status'],
        queryFn: fetchLogStatus,
        refetchInterval: 30000,
        retry: 1,
    });

    const logsQuery = useQuery({
        queryKey: ['logs-search', collectorId, debouncedService, debouncedText, level, windowSize],
        queryFn: () => fetchLogs({
            collectorId: collectorId || undefined,
            service: debouncedService || undefined,
            text: debouncedText || undefined,
            level: level || undefined,
            window: windowSize,
            limit: 80,
            sort: 'desc',
        }),
        refetchInterval: 5000,
        retry: 1,
    });

    const timelineData = useMemo(() => {
        return (logsQuery.data?.timeline ?? []).map((bucket) => ({
            time: formatTimestamp(bucket.start),
            total: bucket.total,
            errors: bucket.errors,
            warnings: bucket.warnings,
        }));
    }, [logsQuery.data?.timeline]);

    const topCorrelation = useMemo(() => logsQuery.data?.metric_correlated?.slice(0, 4) ?? [], [logsQuery.data?.metric_correlated]);

    const newestEntryTime = logsQuery.data?.entries?.[0]?.timestamp;
    const status = statusQuery.data?.stats;
    const levelSummary = logsQuery.data?.level_counts ?? [];
    const serviceSummary = logsQuery.data?.service_counts ?? [];
    const entries = logsQuery.data?.entries ?? [];

    return (
        <div className="h-full flex flex-col bg-card/60 backdrop-blur rounded-xl border border-border shadow-lg overflow-hidden">
            <div className="px-4 py-3 border-b border-border flex items-center gap-3">
                <Terminal className="w-4 h-4 text-primary" />
                <div className="flex-1 min-w-0">
                    <div className="text-sm font-semibold">Native Log Explorer</div>
                    <div className="text-xs text-muted-foreground truncate">
                        Ingested {status?.ingested_events ?? 0} events · Retained {status?.entries ?? 0} indexed records
                    </div>
                </div>
                <div className="text-[11px] text-muted-foreground inline-flex items-center gap-1">
                    <Clock3 className="w-3 h-3" />
                    {newestEntryTime ? `Last ${formatTimestamp(newestEntryTime)}` : 'No logs'}
                </div>
            </div>

            <div className="px-4 py-3 border-b border-border/70 bg-background/30 space-y-2">
                <div className="grid grid-cols-1 md:grid-cols-5 gap-2">
                    <label className="md:col-span-2 bg-background border border-border rounded-md px-2 py-1.5 text-xs flex items-center gap-2">
                        <Search className="w-3 h-3 text-muted-foreground" />
                        <input
                            value={queryText}
                            onChange={(event) => setQueryText(event.target.value)}
                            placeholder="search logs, errors, traces"
                            className="bg-transparent outline-none text-foreground w-full"
                        />
                    </label>
                    <input
                        value={serviceFilter}
                        onChange={(event) => setServiceFilter(event.target.value)}
                        placeholder="service filter"
                        className="bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground"
                    />
                    <select
                        value={level}
                        onChange={(event) => setLevel(event.target.value)}
                        className="bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground"
                    >
                        {LEVEL_OPTIONS.map((item) => (
                            <option key={item.value || 'all'} value={item.value}>{item.label}</option>
                        ))}
                    </select>
                    <select
                        value={windowSize}
                        onChange={(event) => setWindowSize(event.target.value)}
                        className="bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground"
                    >
                        {WINDOW_OPTIONS.map((item) => (
                            <option key={item.value} value={item.value}>{item.label}</option>
                        ))}
                    </select>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                    <Filter className="w-3 h-3 text-muted-foreground" />
                    <select
                        value={collectorId}
                        onChange={(event) => setCollectorId(event.target.value)}
                        className="bg-background border border-border rounded-md px-2 py-1 text-xs text-foreground"
                    >
                        <option value="">All collectors</option>
                        {(fleetQuery.data?.nodes ?? []).map((node) => (
                            <option key={node.collector_id} value={node.collector_id}>
                                {node.hostname || node.collector_id}
                            </option>
                        ))}
                    </select>
                    {levelSummary.slice(0, 3).map((bucket) => (
                        <span key={bucket.value} className={`text-[10px] px-2 py-0.5 rounded-full ${levelBadge(bucket.value)}`}>
                            {bucket.value}: {bucket.count}
                        </span>
                    ))}
                    {serviceSummary.length > 0 && (
                        <span className="text-[10px] text-muted-foreground">
                            Top service: {serviceSummary[0].value} ({serviceSummary[0].count})
                        </span>
                    )}
                </div>
            </div>

            {logsQuery.isLoading ? (
                <div className="flex-1 flex items-center justify-center text-sm text-muted-foreground">Loading indexed logs…</div>
            ) : logsQuery.isError ? (
                <div className="flex-1 flex items-center justify-center px-4 text-sm text-rose-300 text-center">
                    Log API unavailable. Ensure controller log indexing is enabled and `/api/v1/logs/search` is reachable.
                </div>
            ) : (
                <div className="flex-1 min-h-0 grid grid-cols-1 xl:grid-cols-[2fr_1fr]">
                    <div className="min-h-0 flex flex-col border-r border-border/60">
                        <div className="h-36 border-b border-border/60 px-3 py-2">
                            {timelineData.length === 0 ? (
                                <div className="h-full flex items-center justify-center text-xs text-muted-foreground">No timeline data in selected window</div>
                            ) : (
                                <ResponsiveContainer width="100%" height="100%">
                                    <LineChart data={timelineData}>
                                        <CartesianGrid strokeDasharray="3 3" stroke="rgba(148,163,184,0.2)" />
                                        <XAxis dataKey="time" tick={{ fontSize: 10 }} minTickGap={24} />
                                        <YAxis tick={{ fontSize: 10 }} allowDecimals={false} width={32} />
                                        <Tooltip contentStyle={{ background: 'rgba(15, 23, 42, 0.92)', border: '1px solid rgba(148,163,184,0.25)', fontSize: 12 }} />
                                        <Line type="monotone" dataKey="total" stroke="#38bdf8" dot={false} strokeWidth={1.8} />
                                        <Line type="monotone" dataKey="errors" stroke="#f43f5e" dot={false} strokeWidth={1.5} />
                                        <Line type="monotone" dataKey="warnings" stroke="#f59e0b" dot={false} strokeWidth={1.5} />
                                    </LineChart>
                                </ResponsiveContainer>
                            )}
                        </div>
                        <div className="flex-1 min-h-0 overflow-auto text-xs">
                            <table className="w-full">
                                <thead className="sticky top-0 bg-card/95 backdrop-blur border-b border-border z-10">
                                    <tr className="text-muted-foreground">
                                        <th className="text-left px-3 py-2">Time</th>
                                        <th className="text-left px-2 py-2">Level</th>
                                        <th className="text-left px-2 py-2">Service / Process</th>
                                        <th className="text-left px-2 py-2">Message</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {entries.map((entry) => (
                                        <tr key={entry.id} className="border-b border-border/50 hover:bg-muted/30 align-top">
                                            <td className="px-3 py-2 whitespace-nowrap text-muted-foreground">{formatTimestamp(entry.timestamp)}</td>
                                            <td className="px-2 py-2">
                                                <span className={`inline-flex px-2 py-0.5 rounded-full text-[10px] uppercase tracking-wide ${levelBadge(entry.level)}`}>
                                                    {entry.level || 'unknown'}
                                                </span>
                                            </td>
                                            <td className="px-2 py-2 text-foreground">
                                                <div className="font-medium">{entry.service || 'unknown'}</div>
                                                <div className="text-[10px] text-muted-foreground">
                                                    {entry.process || 'process?'} {entry.pid ? `(pid ${entry.pid})` : ''}
                                                    {entry.hostname ? ` · ${entry.hostname}` : ''}
                                                </div>
                                            </td>
                                            <td className="px-2 py-2 text-foreground">
                                                <div className="leading-snug break-words">{entry.message}</div>
                                                {(entry.count ?? 1) > 1 && (
                                                    <div className="text-[10px] text-muted-foreground mt-1">count {entry.count}</div>
                                                )}
                                                {summarizeEntryMetrics(entry) && (
                                                    <div className="text-[10px] text-cyan-200/80 mt-1">{summarizeEntryMetrics(entry)}</div>
                                                )}
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                            {entries.length === 0 && (
                                <div className="p-6 text-center text-muted-foreground text-sm">No logs match current filters.</div>
                            )}
                        </div>
                    </div>

                    <div className="min-h-0 flex flex-col">
                        <div className="px-3 py-2 border-b border-border text-xs font-semibold">Error Highlights</div>
                        <div className="px-3 py-2 border-b border-border/60 space-y-2 max-h-48 overflow-auto">
                            {(logsQuery.data?.highlights ?? []).slice(0, 6).map((entry) => (
                                <div key={`highlight-${entry.id}`} className="rounded-md border border-border/60 bg-background/40 p-2">
                                    <div className="flex items-center justify-between gap-2 mb-1">
                                        <span className={`inline-flex px-2 py-0.5 rounded-full text-[10px] ${levelBadge(entry.level)}`}>{entry.level || 'unknown'}</span>
                                        <span className="text-[10px] text-muted-foreground">{formatTimestamp(entry.timestamp)}</span>
                                    </div>
                                    <div className="text-[11px] leading-snug break-words">{entry.message}</div>
                                </div>
                            ))}
                            {(logsQuery.data?.highlights ?? []).length === 0 && (
                                <div className="text-xs text-muted-foreground">No highlighted anomalies in this window.</div>
                            )}
                        </div>

                        <div className="px-3 py-2 border-b border-border text-xs font-semibold flex items-center gap-2">
                            <AlertTriangle className="w-3 h-3 text-amber-300" />
                            Metric Correlation
                        </div>
                        <div className="flex-1 overflow-auto px-3 py-2 space-y-2 text-xs">
                            {topCorrelation.map((item) => (
                                <div key={item.metric} className="rounded-md border border-border/60 bg-background/40 p-2">
                                    <div className="font-medium text-foreground">{formatMetric(item)}</div>
                                    <div className="text-[10px] text-muted-foreground mt-1">
                                        baseline {formatMetricValue(item.baseline_avg)} · error {formatMetricValue(item.error_avg)} · samples {item.error_samples}/{item.samples}
                                    </div>
                                </div>
                            ))}
                            {topCorrelation.length === 0 && (
                                <div className="text-xs text-muted-foreground">Insufficient data to compute correlations.</div>
                            )}
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}
