import React, { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchInferenceOverview, InferenceMeasurement, MonitoringFinding } from '@/api/inference';

function timeLabel(value?: string): string {
    if (!value || !Number.isFinite(Date.parse(value))) return 'No observation';
    return new Date(value).toLocaleString();
}
function statusLabel(status?: string): string {
    if (status === 'fresh') return 'Recent observations';
    if (status === 'stale') return 'Stale observations';
    return 'Scrape unavailable';
}
function Metric({ label, reading, unit, percent = false }: {
    label: string; reading?: InferenceMeasurement; unit: string; percent?: boolean;
}) {
    const valid = reading !== undefined && Number.isFinite(reading.value) && Number.isFinite(Date.parse(reading.observed_at));
    return <div className="min-w-0 rounded-lg border border-border bg-background p-3">
        <dt className="text-xs text-muted-foreground">{label}</dt>
        <dd className="mt-1 text-xl font-semibold tabular-nums">
            {valid ? `${reading.value.toLocaleString(undefined, { maximumFractionDigits: 2 })} ${unit}` : 'Unavailable'}
        </dd>
        {valid && percent && <meter className="inference-meter mt-2 h-2 w-full" min={0} max={100} value={reading.value} aria-label={label} />}
        <div className="mt-1 break-words text-xs text-muted-foreground">
            {valid ? timeLabel(reading.observed_at) : 'Not measured in the current window'}
            {valid && reading.window_seconds !== undefined && ` · ${reading.window_seconds.toFixed(0)}s interval`}
        </div>
    </div>;
}
function Findings({ findings }: { findings: MonitoringFinding[] }) {
    if (findings.length === 0) return <p className="text-xs text-muted-foreground">No advisory triggered by the available recent measurements. Missing signals are not a health verdict.</p>;
    return <ul className="space-y-3">
        {findings.map((finding) => <li key={finding.code} className="rounded-lg border border-border p-3">
            <p className="text-sm font-medium">{finding.severity === 'critical' ? 'Critical' : 'Advisory'} · {finding.summary}</p>
            <p className="mt-1 text-sm text-muted-foreground">{finding.recommendation}</p>
            <details className="mt-2 text-xs">
                <summary className="min-h-[44px] cursor-pointer py-3">Inspect measured evidence</summary>
                <ul className="space-y-1 break-words text-muted-foreground">{finding.evidence.map((evidence) => <li key={evidence}>{evidence}</li>)}</ul>
            </details>
        </li>)}
    </ul>;
}

export default function InferenceMonitoringPanel({ collectorId = '' }: { collectorId?: string }) {
    const [target, setTarget] = useState('');
    const [modelKey, setModelKey] = useState('');
    const query = useQuery({ queryKey: ['inference-overview'], queryFn: () => fetchInferenceOverview(), refetchInterval: 10000, retry: false });
    const endpoints = query.data?.endpoints ?? [];
    const selected = endpoints.find((e) => JSON.stringify([e.collector_id, e.endpoint]) === target)
        ?? endpoints.find((e) => e.collector_id === collectorId) ?? endpoints[0];
    const models = selected?.models ?? [];
    const model = models.find((m) => JSON.stringify([m.name, m.engine ?? '']) === modelKey) ?? models[0];
    const gpuCollector = selected?.collector_id || collectorId || query.data?.gpus[0]?.collector_id;
    const gpus = (query.data?.gpus ?? []).filter((gpu) => gpu.collector_id === gpuCollector);
    const metrics = model?.metrics ?? {};
    const hasRecentModel = selected?.status === 'fresh' && model?.status === 'fresh';

    return <section className="min-w-0 rounded-xl border border-border bg-card p-4" aria-labelledby="inference-monitoring-title" data-testid="inference-monitoring-panel">
        <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
                <h2 id="inference-monitoring-title" className="text-base font-semibold">LLM serving &amp; GPU evidence</h2>
                <p className="mt-1 text-sm text-muted-foreground">Measured serving demand, response timing and same-node GPU signals.</p>
            </div>
            <button className="min-h-[44px] rounded-md border border-border px-3 text-sm" onClick={() => void query.refetch()} disabled={query.isFetching}>Refresh observations</button>
        </div>
        {query.isLoading ? <p className="mt-4 text-sm" role="status">Loading serving observations…</p>
            : query.isError ? <div className="mt-4" role="alert">
                <p>Unable to load monitoring evidence. Retained readings are not shown as current.</p>
                <button className="mt-2 min-h-[44px] rounded-md border border-border px-3 text-sm" onClick={() => void query.refetch()}>Retry monitoring</button>
            </div> : <>
                {query.data?.capacity_limited && <p className="mt-3 text-sm" role="status">Monitoring capacity was reached. Coverage is incomplete; inspect target cardinality and controller limits.</p>}
                {endpoints.length === 0 ? <p className="mt-4 text-sm text-muted-foreground">No serving targets observed. Configure inference_metrics.endpoints on the collector to read a vLLM /metrics endpoint. GPU observations remain available below.</p>
                    : <>
                        <div className="mt-4 grid min-w-0 gap-3 md:grid-cols-2">
                            <label className="min-w-0 text-xs text-muted-foreground">Serving target
                                <select aria-label="Serving target" className="mt-1 min-h-[44px] w-full min-w-0 rounded-md border border-border bg-background px-2 text-foreground"
                                    value={JSON.stringify([selected.collector_id, selected.endpoint])}
                                    onChange={(event) => { setTarget(event.target.value); setModelKey(''); }}>
                                    {endpoints.map((e) => <option key={JSON.stringify([e.collector_id, e.endpoint])} value={JSON.stringify([e.collector_id, e.endpoint])}>{e.hostname || e.collector_id} · {e.endpoint}</option>)}
                                </select>
                            </label>
                            {models.length > 0 && <label className="min-w-0 text-xs text-muted-foreground">Model and engine
                                <select aria-label="Model and engine" className="mt-1 min-h-[44px] w-full min-w-0 rounded-md border border-border bg-background px-2 text-foreground"
                                    value={JSON.stringify([model.name, model.engine ?? ''])} onChange={(event) => setModelKey(event.target.value)}>
                                    {models.map((m) => <option key={JSON.stringify([m.name, m.engine ?? ''])} value={JSON.stringify([m.name, m.engine ?? ''])}>{m.name}{m.engine ? ` · engine ${m.engine}` : ''}</option>)}
                                </select>
                            </label>}
                        </div>
                        <p className="mt-3 break-words text-xs text-muted-foreground">{statusLabel(selected.status)} · Last attempt: {timeLabel(selected.last_attempt_at)} · Last success: {timeLabel(selected.last_success_at)}</p>
                        {selected.status !== 'fresh' && <p className="mt-2 text-sm">Serving evidence is {selected.status}. Any retained readings below are historical, not current health.</p>}
                        {model && model.status === 'stale' && <p className="mt-2 text-sm">This model has stale observations even if the target is reachable.</p>}
                        {models.length === 0 ? <p className="mt-3 text-sm text-muted-foreground">No model measurements available from this target.</p> : <>
                            <dl className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
                                <Metric label="First-token delay · interval mean" reading={metrics.ttft_mean_seconds} unit="s" />
                                <Metric label="Generation throughput" reading={metrics.generation_tokens_per_second} unit="tokens/s" />
                                <Metric label="Waiting requests" reading={metrics.requests_waiting} unit="requests" />
                                <Metric label="KV-cache occupancy" reading={metrics.kv_cache_utilization_percent} unit="%" percent />
                                <Metric label="Streaming interval · interval mean" reading={metrics.itl_mean_seconds} unit="s" />
                                <Metric label="Request latency · interval mean" reading={metrics.e2e_mean_seconds} unit="s" />
                                <Metric label="Prompt throughput" reading={metrics.prompt_tokens_per_second} unit="tokens/s" />
                                <Metric label="Running requests" reading={metrics.requests_running} unit="requests" />
                            </dl>
                            <p className="my-3 text-xs text-muted-foreground">Rates need two samples. Interval means need completed observations; warmup, idle intervals and counter resets can leave them unavailable. Means are not p95. Streaming intervals are distinct from request-level time per output token.</p>
                            {hasRecentModel && <Findings findings={model.findings} />}
                        </>}
                    </>}
                <div className="mt-5 border-t border-border pt-4">
                    <h3 className="text-sm font-semibold">GPU context{gpus.length > 0 ? ` · ${gpus[0].hostname || gpus[0].collector_id}` : ''}</h3>
                    <p className="mt-1 text-xs text-muted-foreground">{selected ? 'Only GPUs reported by the selected target’s collector are shown. This is node-level context, not proof that a model uses a particular GPU.' : 'GPU evidence from the selected node. No model-to-device mapping is inferred.'}</p>
                    {gpus.length === 0 ? <p className="mt-3 text-sm text-muted-foreground">No GPU observations in this scope.</p> : <div className="mt-3 space-y-4">
                        {gpus.map((gpu) => <article key={gpu.gpu_id} className="min-w-0 rounded-lg border border-border p-3">
                            <h4 className="break-words text-sm font-medium">GPU {gpu.gpu_id} · {statusLabel(gpu.status)}</h4>
                            <dl className="my-3 grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
                                <Metric label="Allocated GPU memory" reading={gpu.metrics.memory_used_percent} unit="%" percent />
                                <Metric label="GPU compute activity" reading={gpu.metrics.utilization_sm_percent} unit="%" percent />
                                <Metric label="GPU temperature" reading={gpu.metrics.temperature_celsius} unit="°C" />
                            </dl>
                            {gpu.status === 'fresh' && <Findings findings={gpu.findings} />}
                        </article>)}
                    </div>}
                </div>
            </>}
    </section>;
}
