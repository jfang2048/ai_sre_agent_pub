import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchK8sTopNodes, fetchK8sTopWorkloads } from '@/api/k8s';

type DrilldownMetric = 'pressure' | 'cpu' | 'memory' | 'gpu' | 'logs' | 'pending' | 'failed' | 'restarts';

const METRIC_OPTIONS: Array<{ value: DrilldownMetric; label: string }> = [
    { value: 'pressure', label: 'Pressure' },
    { value: 'cpu', label: 'CPU' },
    { value: 'memory', label: 'Memory' },
    { value: 'gpu', label: 'GPU' },
    { value: 'logs', label: 'Logs' },
    { value: 'pending', label: 'Pending Pods' },
    { value: 'failed', label: 'Failed Pods' },
    { value: 'restarts', label: 'Restarts' },
];

export default function K8sDrilldown() {
    const [metric, setMetric] = useState<DrilldownMetric>('pressure');
    const [cluster, setCluster] = useState('');

    const filterCluster = useMemo(() => cluster.trim(), [cluster]);

    const workloadsQuery = useQuery({
        queryKey: ['k8s-workloads-top', metric, filterCluster],
        queryFn: () => fetchK8sTopWorkloads({
            metric,
            limit: 15,
            cluster: filterCluster || undefined,
        }),
        refetchInterval: 10000,
        retry: false,
    });

    const nodesQuery = useQuery({
        queryKey: ['k8s-nodes-top', metric, filterCluster],
        queryFn: () => fetchK8sTopNodes({
            metric,
            limit: 12,
            cluster: filterCluster || undefined,
        }),
        refetchInterval: 10000,
        retry: false,
    });

    const disabled = workloadsQuery.isError && nodesQuery.isError;

    return (
        <div className="h-full w-full p-3 flex flex-col gap-3 bg-card/50">
            <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-2">
                <div>
                    <div className="text-sm font-semibold">Kubernetes Pod and Node Drill-down</div>
                    <div className="text-[11px] text-muted-foreground">
                        Read-only workload and node rankings linked to observed pressure signals.
                    </div>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                    <select
                        className="px-2 py-1 rounded-md bg-background border border-border text-xs"
                        value={metric}
                        onChange={(event) => setMetric(event.target.value as DrilldownMetric)}
                    >
                        {METRIC_OPTIONS.map((option) => (
                            <option key={option.value} value={option.value}>{option.label}</option>
                        ))}
                    </select>
                    <input
                        type="text"
                        className="px-2 py-1 rounded-md bg-background border border-border text-xs w-32"
                        value={cluster}
                        onChange={(event) => setCluster(event.target.value)}
                        placeholder="cluster (optional)"
                    />
                </div>
            </div>

            {disabled ? (
                <div className="flex-1 flex items-center justify-center text-sm text-amber-200 border border-amber-300/30 rounded-lg bg-amber-500/10 px-4">
                    Kubernetes integration is disabled or currently unreachable.
                </div>
            ) : (
                <div className="grid grid-cols-1 xl:grid-cols-2 gap-3 flex-1 min-h-0">
                    <div className="rounded-lg border border-border bg-background/50 p-2 overflow-auto">
                        <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-2">
                            Top Workloads
                        </div>
                        {workloadsQuery.isLoading ? (
                            <div className="text-xs text-muted-foreground py-6">Loading workloads…</div>
                        ) : (
                            <table className="w-full text-xs">
                                <thead className="sticky top-0 bg-background border-b border-border">
                                    <tr className="text-muted-foreground">
                                        <th className="text-left py-1 pr-2">Workload</th>
                                        <th className="text-right py-1 pr-2">Pods</th>
                                        <th className="text-right py-1 pr-2">Pending</th>
                                        <th className="text-right py-1">Score</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {(workloadsQuery.data?.workloads ?? []).map((item) => (
                                        <tr key={`${item.cluster}/${item.namespace}/${item.kind}/${item.name}`} className="border-b border-border/50">
                                            <td className="py-1 pr-2">
                                                <div className="font-medium truncate">{item.service || item.name}</div>
                                                <div className="text-[10px] text-muted-foreground truncate">
                                                    {item.cluster} · {item.namespace}
                                                </div>
                                            </td>
                                            <td className="py-1 pr-2 text-right">{item.pods_running}/{item.pods_total}</td>
                                            <td className="py-1 pr-2 text-right">{item.pods_pending}</td>
                                            <td className="py-1 text-right font-semibold text-primary">{item.score.toFixed(1)}</td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        )}
                    </div>

                    <div className="rounded-lg border border-border bg-background/50 p-2 overflow-auto">
                        <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-2">
                            Top Nodes
                        </div>
                        {nodesQuery.isLoading ? (
                            <div className="text-xs text-muted-foreground py-6">Loading nodes…</div>
                        ) : (
                            <table className="w-full text-xs">
                                <thead className="sticky top-0 bg-background border-b border-border">
                                    <tr className="text-muted-foreground">
                                        <th className="text-left py-1 pr-2">Node</th>
                                        <th className="text-right py-1 pr-2">CPU</th>
                                        <th className="text-right py-1 pr-2">GPU</th>
                                        <th className="text-right py-1">Score</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {(nodesQuery.data?.nodes ?? []).map((item) => (
                                        <tr key={`${item.cluster}/${item.name}`} className="border-b border-border/50">
                                            <td className="py-1 pr-2">
                                                <div className="font-medium truncate">{item.name}</div>
                                                <div className="text-[10px] text-muted-foreground truncate">
                                                    {item.cluster}{item.zone ? ` · ${item.zone}` : ''}
                                                </div>
                                            </td>
                                            <td className="py-1 pr-2 text-right">{(item.cpu_usage_percent ?? 0).toFixed(1)}%</td>
                                            <td className="py-1 pr-2 text-right">{(item.gpu_util_percent ?? 0).toFixed(1)}%</td>
                                            <td className="py-1 text-right font-semibold text-primary">{item.score.toFixed(1)}</td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        )}
                    </div>
                </div>
            )}
        </div>
    );
}
