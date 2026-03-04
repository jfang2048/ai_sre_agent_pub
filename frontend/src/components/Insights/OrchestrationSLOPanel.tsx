import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, Ban, RefreshCw, ShieldCheck, Timer } from 'lucide-react';
import { fetchOrchestrationDiagnostics } from '@/api/orchestration';

function formatRatio(value?: number): string {
    if (!value || !Number.isFinite(value)) {
        return '0.00x';
    }
    return `${value.toFixed(2)}x`;
}

export default function OrchestrationSLOPanel() {
    const { data, isLoading, isError } = useQuery({
        queryKey: ['orchestration-diagnostics'],
        queryFn: () => fetchOrchestrationDiagnostics(),
        refetchInterval: 10000,
    });

    const metrics = data?.metrics;
    const policy = data?.policy;
    const blockedReasons = data?.blocked_reasons ?? [];
    const violations = data?.violations ?? [];

    if (isLoading) {
        return (
            <div className="h-full w-full flex items-center justify-center text-sm text-muted-foreground">
                Loading orchestration diagnostics...
            </div>
        );
    }

    if (isError || !data || !metrics || !policy) {
        return (
            <div className="h-full w-full flex items-center justify-center text-sm text-rose-300">
                Orchestration diagnostics unavailable
            </div>
        );
    }

    return (
        <div className="h-full w-full p-3 flex flex-col gap-3 bg-card/50">
            <div className="flex items-center justify-between">
                <div>
                    <div className="text-sm font-semibold">Orchestration SLO and Remediation</div>
                    <div className="text-[11px] text-muted-foreground">
                        Live SLO health, remediation actions, and blocked safety gates.
                    </div>
                </div>
                <div className="text-[11px] text-muted-foreground">
                    {new Date(data.generated_at).toLocaleTimeString()}
                </div>
            </div>

            <div className="grid grid-cols-2 xl:grid-cols-4 gap-2">
                <div className="rounded-md border border-border bg-background/60 px-3 py-2">
                    <div className="text-[10px] uppercase text-muted-foreground flex items-center gap-1">
                        <AlertTriangle className="w-3 h-3" /> Active Violations
                    </div>
                    <div className="text-lg font-semibold text-amber-200">{metrics.slo_violations_active}</div>
                    <div className="text-[11px] text-muted-foreground">total {metrics.slo_violations_total}</div>
                </div>
                <div className="rounded-md border border-border bg-background/60 px-3 py-2">
                    <div className="text-[10px] uppercase text-muted-foreground flex items-center gap-1">
                        <ShieldCheck className="w-3 h-3" /> Remediation Actions
                    </div>
                    <div className="text-lg font-semibold text-emerald-200">{metrics.remediation_actions_total}</div>
                    <div className="text-[11px] text-muted-foreground">attempts {metrics.remediation_attempts_total}</div>
                </div>
                <div className="rounded-md border border-border bg-background/60 px-3 py-2">
                    <div className="text-[10px] uppercase text-muted-foreground flex items-center gap-1">
                        <Ban className="w-3 h-3" /> Blocked
                    </div>
                    <div className="text-lg font-semibold text-rose-200">{metrics.remediation_blocked_total}</div>
                    <div className="text-[11px] text-muted-foreground">safety gate blocks</div>
                </div>
                <div className="rounded-md border border-border bg-background/60 px-3 py-2">
                    <div className="text-[10px] uppercase text-muted-foreground flex items-center gap-1">
                        <RefreshCw className="w-3 h-3" /> Queue and Running
                    </div>
                    <div className="text-lg font-semibold text-sky-200">{metrics.queue_depth} / {metrics.running_workloads}</div>
                    <div className="text-[11px] text-muted-foreground">queued / running</div>
                </div>
            </div>

            <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
                <span className="px-2 py-1 rounded border border-border bg-background/40">
                    breach ratio {formatRatio(policy.slo_breach_ratio)}
                </span>
                <span className="px-2 py-1 rounded border border-border bg-background/40">
                    consecutive {policy.slo_breach_consecutive}
                </span>
                <span className="px-2 py-1 rounded border border-border bg-background/40">
                    cooldown {policy.remediation_cooldown}
                </span>
                <span className="px-2 py-1 rounded border border-border bg-background/40">
                    cycle limit {policy.max_remediations_per_reconcile}
                </span>
                <span className="px-2 py-1 rounded border border-border bg-background/40">
                    workload limit {policy.max_remediations_per_workload}
                </span>
                <span className="px-2 py-1 rounded border border-border bg-background/40">
                    min improvement {(policy.remediation_min_improvement * 100).toFixed(0)}%
                </span>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-3 flex-1 min-h-0">
                <div className="rounded-lg border border-border bg-background/50 p-2 overflow-auto">
                    <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-2">
                        Blocked Gate Reasons
                    </div>
                    {blockedReasons.length === 0 ? (
                        <div className="text-xs text-muted-foreground py-4">No blocked reasons recorded.</div>
                    ) : (
                        <table className="w-full text-xs">
                            <thead className="sticky top-0 bg-background border-b border-border">
                                <tr className="text-muted-foreground">
                                    <th className="text-left py-1 pr-2">Reason</th>
                                    <th className="text-right py-1">Count</th>
                                </tr>
                            </thead>
                            <tbody>
                                {blockedReasons.map((item) => (
                                    <tr key={item.reason} className="border-b border-border/50">
                                        <td className="py-1 pr-2">{item.reason}</td>
                                        <td className="py-1 text-right font-semibold">{item.count}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    )}
                </div>

                <div className="rounded-lg border border-border bg-background/50 p-2 overflow-auto">
                    <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-2">
                        Active SLO Violations
                    </div>
                    {violations.length === 0 ? (
                        <div className="text-xs text-muted-foreground py-4">No active violating workloads.</div>
                    ) : (
                        <table className="w-full text-xs">
                            <thead className="sticky top-0 bg-background border-b border-border">
                                <tr className="text-muted-foreground">
                                    <th className="text-left py-1 pr-2">Workload</th>
                                    <th className="text-right py-1 pr-2">Latency</th>
                                    <th className="text-right py-1 pr-2">Breach</th>
                                    <th className="text-right py-1">Consecutive</th>
                                </tr>
                            </thead>
                            <tbody>
                                {violations.slice(0, 20).map((item) => (
                                    <tr key={item.workload_id} className="border-b border-border/50">
                                        <td className="py-1 pr-2">
                                            <div className="font-medium truncate">{item.service}</div>
                                            <div className="text-[10px] text-muted-foreground truncate flex items-center gap-1">
                                                <Timer className="w-3 h-3" />
                                                {item.workload_id}
                                            </div>
                                        </td>
                                        <td className="py-1 pr-2 text-right">
                                            {item.estimated_latency_ms.toFixed(1)} / {item.latency_slo_ms}ms
                                        </td>
                                        <td className="py-1 pr-2 text-right text-amber-200">{formatRatio(item.breach_ratio)}</td>
                                        <td className="py-1 text-right font-semibold">{item.consecutive_breaches}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    )}
                </div>
            </div>
        </div>
    );
}
