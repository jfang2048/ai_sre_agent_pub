import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Database, DollarSign, HardDrive, ShieldCheck } from 'lucide-react';
import {
    fetchFinOpsSignals,
    fetchHAStatus,
    type StorageStatus,
    fetchStorageStatus,
    updateStorageRetention,
} from '@/api/controlPlane';
import { formatBytes, formatCount, formatPercent } from '@/components/Visualizations/metricFormat';

const retentionUpdateTimeoutMs = 8000;

export default function OperationsControlPanel() {
    const queryClient = useQueryClient();
    const [nodeRetention, setNodeRetention] = useState('24h');
    const [historySamples, setHistorySamples] = useState('1440');
    const [notice, setNotice] = useState('');
    const [isApplyingRetention, setIsApplyingRetention] = useState(false);
    const hasSeededStorageRef = useRef(false);

    const haQuery = useQuery({
        queryKey: ['ha-status'],
        queryFn: fetchHAStatus,
        refetchInterval: 15000,
        retry: false,
    });

    const storageQuery = useQuery({
        queryKey: ['storage-status'],
        queryFn: fetchStorageStatus,
        refetchInterval: 15000,
        retry: false,
    });

    const finopsQuery = useQuery({
        queryKey: ['finops-signals'],
        queryFn: fetchFinOpsSignals,
        refetchInterval: 20000,
        retry: false,
    });

    useEffect(() => {
        if (hasSeededStorageRef.current) {
            return;
        }
        const currentRetention = storageQuery.data?.storage?.node_retention;
        const currentHistory = storageQuery.data?.storage?.history_samples_per_node;
        let seeded = false;
        if (currentRetention) {
            setNodeRetention(currentRetention);
            seeded = true;
        }
        if (typeof currentHistory === 'number' && currentHistory > 0) {
            setHistorySamples(String(currentHistory));
            seeded = true;
        }
        if (seeded) {
            hasSeededStorageRef.current = true;
        }
    }, [storageQuery.data?.storage?.history_samples_per_node, storageQuery.data?.storage?.node_retention]);

    const topWasteNodes = (finopsQuery.data?.nodes ?? []).slice(0, 5);

    const persistence = storageQuery.data?.storage.persistence;
    const tsdb = storageQuery.data?.tsdb;
    const standby = haQuery.data?.read_only;

    async function handleApplyRetention() {
        if (isApplyingRetention || standby) {
            return;
        }

        const trimmedRetention = nodeRetention.trim();
        const parsedHistory = Number.parseInt(historySamples, 10);
        if (!trimmedRetention) {
            setNotice('node_retention is required');
            return;
        }
        if (!Number.isFinite(parsedHistory) || parsedHistory <= 0) {
            setNotice('history_samples_per_node must be > 0');
            return;
        }

        setNotice('');
        setIsApplyingRetention(true);
        try {
            const response = await promiseWithTimeout(
                updateStorageRetention({
                    node_retention: trimmedRetention,
                    history_samples_per_node: parsedHistory,
                }),
                retentionUpdateTimeoutMs,
                'Retention update timed out.',
            );

            const normalizedStorage = response.storage ?? storageQuery.data?.storage;
            const normalizedTSDB = response.tsdb ?? storageQuery.data?.tsdb;
            const timestamp = response.timestamp || new Date().toISOString();
            const nextStatus: StorageStatus = {
                storage: normalizedStorage,
                tsdb: normalizedTSDB,
                timestamp,
            };
            queryClient.setQueryData(['storage-status'], nextStatus);

            if (normalizedStorage?.node_retention) {
                setNodeRetention(normalizedStorage.node_retention);
            }
            if (typeof normalizedStorage?.history_samples_per_node === 'number' && normalizedStorage.history_samples_per_node > 0) {
                setHistorySamples(String(normalizedStorage.history_samples_per_node));
            }
            hasSeededStorageRef.current = true;
            setNotice('Retention updated.');
            void queryClient.invalidateQueries({ queryKey: ['storage-status'] });
        } catch (error) {
            setNotice(formatControlError(error));
        } finally {
            setIsApplyingRetention(false);
        }
    }

    return (
        <div className="h-full w-full p-3 bg-card/50 flex flex-col gap-3" data-testid="operations-control-panel">
            <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                    <div className="text-sm font-semibold flex items-center gap-1">
                        <Database className="w-4 h-4 text-cyan-300" />
                        Retention, HA, and FinOps Control
                    </div>
                    <div className="text-[11px] text-muted-foreground">
                        Storage retention bounds, hot-standby state, and waste indicators from live telemetry.
                    </div>
                </div>
                <div className="flex items-center gap-2 text-[11px]">
                    <span className={`px-2 py-0.5 rounded border ${standby ? 'border-amber-400 text-amber-200' : 'border-emerald-400 text-emerald-200'}`}>
                        {standby ? 'standby read-only' : 'active'}
                    </span>
                    <span className={`px-2 py-0.5 rounded border ${persistence?.enabled ? 'border-cyan-400 text-cyan-200' : 'border-border text-muted-foreground'}`}>
                        {persistence?.enabled ? 'persistence on' : 'memory only'}
                    </span>
                    <span className={`px-2 py-0.5 rounded border ${tsdb?.enabled ? (tsdb?.healthy ? 'border-emerald-400 text-emerald-200' : 'border-amber-400 text-amber-200') : 'border-border text-muted-foreground'}`}>
                        {tsdb?.enabled ? `tsdb ${tsdb.mode}` : 'tsdb off'}
                    </span>
                </div>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-3 flex-1 min-h-0">
                <section className="rounded-lg border border-border bg-background/40 p-3 space-y-3">
                    <div className="flex items-center gap-1 text-xs uppercase tracking-wide text-muted-foreground">
                        <HardDrive className="w-3.5 h-3.5" />
                        Retention Settings
                    </div>
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                        <label className="text-xs text-muted-foreground">
                            Node retention
                            <input
                                className="mt-1 w-full rounded border border-border bg-background px-2 py-1 text-xs"
                                value={nodeRetention}
                                onChange={(event) => setNodeRetention(event.target.value)}
                                placeholder="24h"
                                disabled={isApplyingRetention}
                            />
                        </label>
                        <label className="text-xs text-muted-foreground">
                            History samples per node
                            <input
                                className="mt-1 w-full rounded border border-border bg-background px-2 py-1 text-xs"
                                value={historySamples}
                                onChange={(event) => setHistorySamples(event.target.value)}
                                placeholder="1440"
                                disabled={isApplyingRetention}
                                inputMode="numeric"
                            />
                        </label>
                    </div>
                    <div className="flex items-center gap-2">
                        <button
                            type="button"
                            className="rounded border border-border px-2.5 py-1 text-xs hover:bg-muted/60 disabled:opacity-60"
                            onClick={handleApplyRetention}
                            disabled={isApplyingRetention || standby}
                            title={standby ? 'Controller is in standby mode' : 'Apply retention'}
                        >
                            Apply retention
                        </button>
                        <span className="text-[11px] text-muted-foreground">
                            {standby ? 'Writes blocked in standby mode.' : 'Bounded update (node retention + history ring).'}
                        </span>
                    </div>
                    {notice && (
                        <div className="text-xs text-muted-foreground rounded border border-border bg-muted/30 px-2 py-1">
                            {notice}
                        </div>
                    )}

                    <div className="grid grid-cols-2 gap-2 text-xs">
                        <MetricChip label="Nodes" value={formatCount(storageQuery.data?.storage.nodes)} />
                        <MetricChip label="History samples" value={formatCount(storageQuery.data?.storage.history_samples)} />
                        <MetricChip label="Retention" value={storageQuery.data?.storage.node_retention || '—'} />
                        <MetricChip label="History/node" value={formatCount(storageQuery.data?.storage.history_samples_per_node)} />
                        <MetricChip label="DB size" value={formatBytes(persistence?.current_db_bytes)} />
                        <MetricChip label="Compactions" value={formatCount(persistence?.compactions)} />
                        <MetricChip label="TSDB bucket" value={tsdb?.bucket || '—'} />
                        <MetricChip label="TSDB queue" value={formatCount(tsdb?.queue_depth)} />
                    </div>
                    {tsdb?.enabled && (
                        <div className="rounded border border-border bg-background/50 px-2 py-2 text-[11px] text-muted-foreground space-y-1">
                            <div>Provider: {tsdb.provider} · Endpoint: {tsdb.endpoint || '—'}</div>
                            <div>Org/Bucket: {tsdb.org || '—'} / {tsdb.bucket || '—'}</div>
                            <div>Retention: {tsdb.retention || '—'} · Flush: {tsdb.flush_interval || '—'} · Query timeout: {tsdb.query_timeout || '—'}</div>
                            <div>Health poll: {tsdb.health_interval || '—'} · Backup dir: {tsdb.backup_directory || '—'}</div>
                            {tsdb.degraded_reason && <div className="text-amber-200">Degraded: {tsdb.degraded_reason}</div>}
                            {tsdb.last_health_error && <div className="text-amber-200">Health: {tsdb.last_health_error}</div>}
                            {tsdb.last_write_error && <div className="text-amber-200">Write: {tsdb.last_write_error}</div>}
                            {tsdb.last_query_error && <div className="text-amber-200">Query: {tsdb.last_query_error}</div>}
                        </div>
                    )}
                </section>

                <section className="rounded-lg border border-border bg-background/40 p-3 space-y-3 overflow-auto">
                    <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-1 text-xs uppercase tracking-wide text-muted-foreground">
                            <DollarSign className="w-3.5 h-3.5" />
                            FinOps Waste Indicators
                        </div>
                        <div className="text-[11px] text-muted-foreground">
                            avg score {formatPercent((finopsQuery.data?.summary.average_waste_score ?? 0) * 100, 0)}
                        </div>
                    </div>

                    <div className="grid grid-cols-3 gap-2 text-xs">
                        <MetricChip label="Nodes" value={formatCount(finopsQuery.data?.summary.nodes_analyzed)} />
                        <MetricChip label="CPU idle hints" value={formatCount(finopsQuery.data?.summary.idle_cpu_hints)} />
                        <MetricChip label="GPU waste hints" value={formatCount(finopsQuery.data?.summary.gpu_waste_hints)} />
                    </div>

                    <div className="space-y-2">
                        {topWasteNodes.map((node) => (
                            <article key={node.collector_id} className="rounded border border-border px-2 py-2">
                                <div className="flex items-center justify-between gap-2">
                                    <div className="text-xs font-medium truncate">{node.hostname || node.collector_id}</div>
                                    <div className="text-[11px] text-muted-foreground">
                                        score {formatPercent(node.potential_waste_score * 100, 0)}
                                    </div>
                                </div>
                                <div className="mt-1 text-[11px] text-muted-foreground flex flex-wrap gap-2">
                                    <span>CPU {formatPercent(node.cpu_usage_percent, 1)}</span>
                                    <span>Mem {formatPercent(node.memory_usage_percent, 1)}</span>
                                    <span>GPU {formatPercent(node.gpu_utilization_percent, 1)}</span>
                                </div>
                                <div className="mt-1 text-[11px] flex flex-wrap gap-1">
                                    {node.idle_cpu_hint && <HintTag label="idle cpu" />}
                                    {node.oversized_memory_hint && <HintTag label="oversized memory" />}
                                    {node.gpu_waste_hint && <HintTag label="gpu waste" />}
                                    {!node.idle_cpu_hint && !node.oversized_memory_hint && !node.gpu_waste_hint && (
                                        <span className="text-muted-foreground">no major waste hints</span>
                                    )}
                                </div>
                            </article>
                        ))}
                        {topWasteNodes.length === 0 && (
                            <div className="text-xs text-muted-foreground">No FinOps hints yet.</div>
                        )}
                    </div>
                </section>
            </div>

            <div className="text-[11px] text-muted-foreground flex items-center gap-1">
                <ShieldCheck className="w-3.5 h-3.5" />
                Standby mode keeps mutation APIs read-only; retention updates require active controller.
            </div>
        </div>
    );
}

function MetricChip({ label, value }: { label: string; value: string }) {
    return (
        <div className="rounded border border-border bg-background/60 px-2 py-1">
            <div className="text-[10px] uppercase text-muted-foreground">{label}</div>
            <div className="font-medium">{value}</div>
        </div>
    );
}

function HintTag({ label }: { label: string }) {
    return (
        <span className="px-1.5 py-0.5 rounded border border-amber-300/40 text-amber-200 bg-amber-500/10">
            {label}
        </span>
    );
}

function formatControlError(error: unknown): string {
    const responseData = extractControlErrorPayload(error);
    if (typeof responseData === 'string' && responseData) {
        return responseData;
    }
    if (responseData && typeof responseData === 'object' && 'error' in responseData) {
        const message = responseData.error;
        if (typeof message === 'string' && message) {
            return message;
        }
    }
    if (error instanceof Error && error.message) {
        return error.message;
    }
    return 'Update failed.';
}

function extractControlErrorPayload(error: unknown): unknown {
    if (!error || typeof error !== 'object' || !('response' in error)) {
        return undefined;
    }
    const response = error.response;
    if (!response || typeof response !== 'object' || !('data' in response)) {
        return undefined;
    }
    return response.data;
}

async function promiseWithTimeout<T>(promise: Promise<T>, timeoutMs: number, message: string): Promise<T> {
    let timeoutID: ReturnType<typeof setTimeout> | undefined;
    try {
        return await Promise.race([
            promise,
            new Promise<T>((_, reject) => {
                timeoutID = setTimeout(() => {
                    reject(new Error(message));
                }, timeoutMs);
            }),
        ]);
    } finally {
        if (timeoutID) {
            clearTimeout(timeoutID);
        }
    }
}
