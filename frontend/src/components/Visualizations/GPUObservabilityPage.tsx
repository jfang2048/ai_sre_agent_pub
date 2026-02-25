import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    ResponsiveContainer,
    LineChart,
    Line,
    CartesianGrid,
    XAxis,
    YAxis,
    Tooltip,
} from 'recharts';
import {
    fetchGPUCorrelation,
    fetchGPUEvents,
    fetchGPUNodes,
    fetchGPUProcesses,
    fetchGPUProcessTimeline,
    fetchGPUTimeline,
    GPUDevice,
    GPUEventRecord,
    GPUNode,
    GPUProcessRow,
} from '@/api/gpuObservability';

const WINDOW_OPTIONS = ['15m', '30m', '1h', '3h', '6h'];

const DEVICE_METRICS = [
    { value: 'node_gpu_utilization_sm_percent', label: 'SM Util (%)' },
    { value: 'node_gpu_memory_used_mib', label: 'Memory Used (MiB)' },
    { value: 'node_gpu_pcie_link_utilization_percent', label: 'PCIe Link Util (%)' },
    { value: 'node_gpu_power_draw_watts', label: 'Power Draw (W)' },
    { value: 'node_gpu_temperature_celsius', label: 'Temperature (C)' },
    { value: 'node_gpu_xid_errors_total', label: 'Xid Errors (total)' },
];

const PROCESS_METRICS = [
    { value: 'node_gpu_process_sm_util_percent', label: 'Process SM Util (%)' },
    { value: 'node_gpu_process_memory_mib', label: 'Process Memory (MiB)' },
    { value: 'node_gpu_process_mem_util_percent', label: 'Process Mem Util (%)' },
    { value: 'node_gpu_process_encoder_util_percent', label: 'Process Encoder Util (%)' },
    { value: 'node_gpu_process_decoder_util_percent', label: 'Process Decoder Util (%)' },
    { value: 'node_gpu_process_context_active', label: 'Context Active (0/1)' },
];

function formatTime(ts: string): string {
    const d = new Date(ts);
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:${String(d.getSeconds()).padStart(2, '0')}`;
}

function toSeries(points: { timestamp: string; value: number }[]) {
    return points.map((point) => ({
        ...point,
        time: formatTime(point.timestamp),
    }));
}

function severityClass(severity?: string): string {
    switch ((severity || '').toLowerCase()) {
        case 'critical':
            return 'text-rose-300 border-rose-500/50 bg-rose-500/10';
        case 'warning':
            return 'text-amber-200 border-amber-500/50 bg-amber-500/10';
        default:
            return 'text-emerald-200 border-emerald-500/50 bg-emerald-500/10';
    }
}

function formatNumber(v: number | undefined, digits = 1): string {
    if (v === undefined || Number.isNaN(v)) {
        return '—';
    }
    return Number(v).toFixed(digits);
}

function sortCollectorOptions<T extends { last_seen: string }>(nodes: T[]): T[] {
    return [...nodes].sort((a, b) => new Date(b.last_seen).getTime() - new Date(a.last_seen).getTime());
}

export default function GPUObservabilityPage() {
    const [collectorId, setCollectorId] = useState('');
    const [gpuId, setGPUId] = useState('');
    const [pid, setPID] = useState('');
    const [windowSize, setWindowSize] = useState('1h');
    const [deviceMetric, setDeviceMetric] = useState(DEVICE_METRICS[0].value);
    const [processMetric, setProcessMetric] = useState(PROCESS_METRICS[0].value);
    const [processSortBy, setProcessSortBy] = useState('sm_util');

    const nodesQuery = useQuery({
        queryKey: ['gpu-nodes'],
        queryFn: fetchGPUNodes,
        refetchInterval: 10000,
    });

    const nodes = useMemo<GPUNode[]>(() => sortCollectorOptions(nodesQuery.data?.nodes ?? []), [nodesQuery.data?.nodes]);
    const activeCollectorId = collectorId || nodes[0]?.collector_id || '';

    const activeNode = useMemo(() => {
        if (!activeCollectorId) {
            return undefined;
        }
        return nodes.find((node) => node.collector_id === activeCollectorId);
    }, [activeCollectorId, nodes]);

    const gpuOptions = useMemo(() => {
        const map = activeNode?.gpus ?? {};
        return Object.entries(map)
            .map(([id, device]) => ({ id, label: `${id}${device.name ? ` · ${device.name}` : ''}` }))
            .sort((a, b) => a.id.localeCompare(b.id));
    }, [activeNode]);

    const activeGPUId = gpuId || gpuOptions[0]?.id || '';
    const activeDevice: GPUDevice | undefined = activeGPUId ? activeNode?.gpus?.[activeGPUId] : undefined;

    const processesQuery = useQuery({
        queryKey: ['gpu-processes', activeCollectorId, activeGPUId, processSortBy],
        queryFn: () =>
            fetchGPUProcesses({
                collectorId: activeCollectorId,
                gpuId: activeGPUId,
                sortBy: processSortBy,
                limit: 30,
            }),
        enabled: Boolean(activeCollectorId && activeGPUId),
        refetchInterval: 5000,
    });

    const processRows: GPUProcessRow[] = processesQuery.data?.processes ?? [];
    const activePID = pid || processRows[0]?.pid || '';

    const timelineQuery = useQuery({
        queryKey: ['gpu-timeline', activeCollectorId, activeGPUId, deviceMetric, windowSize],
        queryFn: () =>
            fetchGPUTimeline({
                collectorId: activeCollectorId,
                gpuId: activeGPUId,
                metric: deviceMetric,
                window: windowSize,
                limit: 360,
            }),
        enabled: Boolean(activeCollectorId && activeGPUId),
        refetchInterval: 5000,
    });

    const processTimelineQuery = useQuery({
        queryKey: ['gpu-process-timeline', activeCollectorId, activeGPUId, activePID, processMetric, windowSize],
        queryFn: () =>
            fetchGPUProcessTimeline({
                collectorId: activeCollectorId,
                gpuId: activeGPUId,
                pid: activePID,
                metric: processMetric,
                window: windowSize,
                limit: 360,
            }),
        enabled: Boolean(activeCollectorId && activeGPUId && activePID),
        refetchInterval: 5000,
    });

    const eventsQuery = useQuery({
        queryKey: ['gpu-events', activeCollectorId, activeGPUId, windowSize],
        queryFn: () =>
            fetchGPUEvents({
                collectorId: activeCollectorId,
                gpuId: activeGPUId,
                window: windowSize,
                limit: 200,
            }),
        enabled: Boolean(activeCollectorId),
        refetchInterval: 5000,
    });

    const correlationQuery = useQuery({
        queryKey: ['gpu-correlation', activeCollectorId],
        queryFn: () => fetchGPUCorrelation(activeCollectorId),
        enabled: Boolean(activeCollectorId),
        refetchInterval: 5000,
    });

    const deviceSeries = useMemo(() => toSeries(timelineQuery.data?.points ?? []), [timelineQuery.data?.points]);
    const processSeries = useMemo(() => toSeries(processTimelineQuery.data?.points ?? []), [processTimelineQuery.data?.points]);
    const events: GPUEventRecord[] = eventsQuery.data?.events ?? [];

    return (
        <div className="space-y-4" data-testid="gpu-observability-page">
            <div className="rounded-xl border border-border bg-card p-4">
                <div className="text-sm font-semibold">GPU Observability</div>
                <div className="text-xs text-muted-foreground mt-1">Cluster → Node → GPU → Process drilldown with timelines, ranked processes, event timeline, and host correlation.</div>
                <div className="grid grid-cols-1 md:grid-cols-6 gap-3 mt-4 text-xs">
                    <label className="flex flex-col gap-1">
                        <span className="text-muted-foreground">Node</span>
                        <select
                            className="rounded border border-border bg-background px-2 py-1"
                            value={activeCollectorId}
                            onChange={(event) => {
                                setCollectorId(event.target.value);
                                setGPUId('');
                                setPID('');
                            }}
                        >
                            {nodes.map((node) => (
                                <option key={node.collector_id} value={node.collector_id}>
                                    {node.hostname || node.collector_id}
                                </option>
                            ))}
                        </select>
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-muted-foreground">GPU</span>
                        <select
                            className="rounded border border-border bg-background px-2 py-1"
                            value={activeGPUId}
                            onChange={(event) => {
                                setGPUId(event.target.value);
                                setPID('');
                            }}
                        >
                            {gpuOptions.map((gpu) => (
                                <option key={gpu.id} value={gpu.id}>
                                    {gpu.label}
                                </option>
                            ))}
                        </select>
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-muted-foreground">Window</span>
                        <select className="rounded border border-border bg-background px-2 py-1" value={windowSize} onChange={(event) => setWindowSize(event.target.value)}>
                            {WINDOW_OPTIONS.map((option) => (
                                <option key={option} value={option}>
                                    {option}
                                </option>
                            ))}
                        </select>
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-muted-foreground">GPU Metric</span>
                        <select className="rounded border border-border bg-background px-2 py-1" value={deviceMetric} onChange={(event) => setDeviceMetric(event.target.value)}>
                            {DEVICE_METRICS.map((option) => (
                                <option key={option.value} value={option.value}>
                                    {option.label}
                                </option>
                            ))}
                        </select>
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-muted-foreground">Process Metric</span>
                        <select className="rounded border border-border bg-background px-2 py-1" value={processMetric} onChange={(event) => setProcessMetric(event.target.value)}>
                            {PROCESS_METRICS.map((option) => (
                                <option key={option.value} value={option.value}>
                                    {option.label}
                                </option>
                            ))}
                        </select>
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-muted-foreground">Sort Processes</span>
                        <select className="rounded border border-border bg-background px-2 py-1" value={processSortBy} onChange={(event) => setProcessSortBy(event.target.value)}>
                            <option value="sm_util">SM Util</option>
                            <option value="mem_mib">Memory</option>
                            <option value="mem_util">Memory Util</option>
                            <option value="encoder_util">Encoder</option>
                            <option value="decoder_util">Decoder</option>
                            <option value="context_active">Context Active</option>
                        </select>
                    </label>
                </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-4 gap-4">
                <div className="rounded-xl border border-border bg-card p-4">
                    <div className="text-xs uppercase tracking-wide text-muted-foreground">GPU Util</div>
                    <div className="text-2xl font-semibold mt-1">{formatNumber(activeDevice?.util_sm_percent)}%</div>
                    <div className="text-xs text-muted-foreground mt-1">SM utilization</div>
                </div>
                <div className="rounded-xl border border-border bg-card p-4">
                    <div className="text-xs uppercase tracking-wide text-muted-foreground">Memory Pressure</div>
                    <div className="text-2xl font-semibold mt-1">
                        {activeDevice?.mem_total_mib ? formatNumber(((activeDevice.mem_used_mib || 0) / activeDevice.mem_total_mib) * 100) : '—'}%
                    </div>
                    <div className="text-xs text-muted-foreground mt-1">
                        {formatNumber(activeDevice?.mem_used_mib, 0)} / {formatNumber(activeDevice?.mem_total_mib, 0)} MiB
                    </div>
                </div>
                <div className="rounded-xl border border-border bg-card p-4">
                    <div className="text-xs uppercase tracking-wide text-muted-foreground">PCIe Link Util</div>
                    <div className="text-2xl font-semibold mt-1">{formatNumber(activeDevice?.pcie_link_util_percent)}%</div>
                    <div className="text-xs text-muted-foreground mt-1">
                        RX {formatNumber(activeDevice?.pcie_rx_mb_s, 0)} MB/s · TX {formatNumber(activeDevice?.pcie_tx_mb_s, 0)} MB/s
                    </div>
                </div>
                <div className="rounded-xl border border-border bg-card p-4">
                    <div className="text-xs uppercase tracking-wide text-muted-foreground">Reliability</div>
                    <div className="text-2xl font-semibold mt-1">{formatNumber((activeDevice?.xid_errors_total || 0) + (activeDevice?.reset_events_total || 0), 0)}</div>
                    <div className="text-xs text-muted-foreground mt-1">Xid + reset events</div>
                </div>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-xl border border-border bg-card p-4">
                    <div className="text-sm font-semibold">GPU Metric Timeline</div>
                    <div className="h-64 mt-3">
                        <ResponsiveContainer width="100%" height="100%">
                            <LineChart data={deviceSeries}>
                                <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
                                <XAxis dataKey="time" tick={{ fontSize: 11 }} minTickGap={24} />
                                <YAxis tick={{ fontSize: 11 }} />
                                <Tooltip />
                                <Line type="monotone" dataKey="value" stroke="#22d3ee" strokeWidth={2} dot={false} />
                            </LineChart>
                        </ResponsiveContainer>
                    </div>
                    {deviceSeries.length === 0 && <div className="text-xs text-muted-foreground mt-2">No timeline samples yet for this GPU metric.</div>}
                </section>

                <section className="rounded-xl border border-border bg-card p-4">
                    <div className="flex items-center justify-between gap-3">
                        <div>
                            <div className="text-sm font-semibold">Process Metric Timeline</div>
                            <div className="text-xs text-muted-foreground">Per-process GPU activity for selected metric.</div>
                        </div>
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-xs"
                            value={activePID}
                            onChange={(event) => setPID(event.target.value)}
                        >
                            {processRows.map((row) => (
                                <option key={row.pid} value={row.pid}>
                                    {row.name || row.pid} ({row.pid})
                                </option>
                            ))}
                        </select>
                    </div>
                    <div className="h-64 mt-3">
                        <ResponsiveContainer width="100%" height="100%">
                            <LineChart data={processSeries}>
                                <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
                                <XAxis dataKey="time" tick={{ fontSize: 11 }} minTickGap={24} />
                                <YAxis tick={{ fontSize: 11 }} />
                                <Tooltip />
                                <Line type="monotone" dataKey="value" stroke="#f59e0b" strokeWidth={2} dot={false} />
                            </LineChart>
                        </ResponsiveContainer>
                    </div>
                    {processSeries.length === 0 && <div className="text-xs text-muted-foreground mt-2">No process timeline samples yet.</div>}
                </section>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-xl border border-border bg-card p-4">
                    <div className="text-sm font-semibold">Top GPU Processes</div>
                    <div className="text-xs text-muted-foreground mb-3">Ranked by selected mode with context activity and encode/decode utilization.</div>
                    <div className="overflow-auto max-h-72">
                        <table className="w-full text-xs">
                            <thead className="sticky top-0 bg-card border-b border-border">
                                <tr className="text-muted-foreground">
                                    <th className="text-left py-2">Process</th>
                                    <th className="text-right py-2">PID</th>
                                    <th className="text-right py-2">SM</th>
                                    <th className="text-right py-2">Mem</th>
                                    <th className="text-right py-2">Enc/Dec</th>
                                    <th className="text-right py-2">Ctx</th>
                                </tr>
                            </thead>
                            <tbody>
                                {processRows.map((row) => (
                                    <tr key={row.pid} className="border-b border-border/60 hover:bg-muted/30">
                                        <td className="py-2 pr-2">{row.name || 'unknown'}</td>
                                        <td className="py-2 text-right">{row.pid}</td>
                                        <td className="py-2 text-right">{formatNumber(row.util_sm_percent)}%</td>
                                        <td className="py-2 text-right">{formatNumber(row.mem_mib, 0)} MiB</td>
                                        <td className="py-2 text-right">
                                            {formatNumber(row.util_enc_percent)}% / {formatNumber(row.util_dec_percent)}%
                                        </td>
                                        <td className="py-2 text-right">{formatNumber(row.context_active, 0)}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                        {processRows.length === 0 && <div className="text-xs text-muted-foreground py-4">No process-level GPU attribution available yet.</div>}
                    </div>
                </section>

                <section className="rounded-xl border border-border bg-card p-4">
                    <div className="text-sm font-semibold">GPU Event Timeline</div>
                    <div className="text-xs text-muted-foreground mb-3">Xid / UVM / reset / throttle events in time order.</div>
                    <div className="space-y-2 max-h-72 overflow-auto">
                        {events.map((event, idx) => (
                            <div key={`${event.timestamp}-${event.event_type}-${idx}`} className={`rounded border px-3 py-2 text-xs ${severityClass(event.severity)}`}>
                                <div className="flex items-center justify-between gap-3">
                                    <span className="font-semibold uppercase">{event.event_type}</span>
                                    <span>{formatTime(event.timestamp)}</span>
                                </div>
                                <div className="mt-1 text-[11px] opacity-90">
                                    GPU {event.gpu_index || 'unknown'}
                                    {event.code ? ` · code ${event.code}` : ''}
                                    {event.count ? ` · +${event.count.toFixed(0)}` : ''}
                                    {event.source ? ` · ${event.source}` : ''}
                                </div>
                            </div>
                        ))}
                        {events.length === 0 && <div className="text-xs text-muted-foreground">No GPU runtime events for the selected window.</div>}
                    </div>
                </section>
            </div>

            <section className="rounded-xl border border-border bg-card p-4">
                <div className="text-sm font-semibold">Cross-Resource Correlation</div>
                <div className="text-xs text-muted-foreground">Correlates GPU pressure/events with CPU IOWait, disk pressure, network utilization, and retransmit indicators.</div>
                <div className="grid grid-cols-1 lg:grid-cols-4 gap-3 mt-3 text-xs">
                    <div className="rounded border border-border bg-muted/30 px-3 py-2">
                        <div className="text-muted-foreground">Starvation Risk</div>
                        <div className="text-lg font-semibold">{formatNumber((correlationQuery.data?.scores.starvation_risk || 0) * 100, 1)}%</div>
                    </div>
                    <div className="rounded border border-border bg-muted/30 px-3 py-2">
                        <div className="text-muted-foreground">Communication Risk</div>
                        <div className="text-lg font-semibold">{formatNumber((correlationQuery.data?.scores.communication_risk || 0) * 100, 1)}%</div>
                    </div>
                    <div className="rounded border border-border bg-muted/30 px-3 py-2">
                        <div className="text-muted-foreground">Reliability Risk</div>
                        <div className="text-lg font-semibold">{formatNumber((correlationQuery.data?.scores.reliability_risk || 0) * 100, 1)}%</div>
                    </div>
                    <div className="rounded border border-border bg-muted/30 px-3 py-2">
                        <div className="text-muted-foreground">Overall Risk</div>
                        <div className="text-lg font-semibold">{formatNumber(correlationQuery.data?.scores.overall_risk_percent, 1)}%</div>
                    </div>
                </div>
                <ul className="mt-3 space-y-1 text-xs">
                    {(correlationQuery.data?.risks ?? []).map((risk, index) => (
                        <li key={`${risk}-${index}`} className="rounded border border-border bg-background px-3 py-2">{risk}</li>
                    ))}
                </ul>
            </section>
        </div>
    );
}
