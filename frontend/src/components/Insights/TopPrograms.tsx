import React, { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchTopPrograms, ProgramStats, TopProgramsResponse } from '@/api/topPrograms';
import { Activity, AlertTriangle, Cpu, HardDrive, Network, Database, Gauge, Server, Wifi, MemoryStick, Bug } from 'lucide-react';
import clsx from 'clsx';

const categoryIcons: Record<string, JSX.Element> = {
    cpu: <Cpu className="w-4 h-4" />,
    memory: <Database className="w-4 h-4" />,
    disk: <HardDrive className="w-4 h-4" />,
    network: <Network className="w-4 h-4" />,
    gpu: <Gauge className="w-4 h-4" />,
    logs: <AlertTriangle className="w-4 h-4" />,
};

function formatBytes(bytes?: number) {
    if (!bytes) return '—';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

function formatRate(bytes?: number) {
    if (!bytes) return '—';
    return `${formatBytes(bytes)}/s`;
}

const severityColor = (score: number) => {
    if (score >= 6) return 'bg-rose-500/10 text-rose-300 border-rose-500/30';
    if (score >= 4) return 'bg-amber-500/10 text-amber-200 border-amber-500/30';
    return 'bg-emerald-500/10 text-emerald-200 border-emerald-500/30';
};

export default function TopProgramsPanel() {
    const { data, isLoading, error } = useQuery(['top-programs'], () => fetchTopPrograms(15), {
        refetchInterval: 30_000,
    });

    const programs = data?.programs ?? [];
    const summary = data?.summary;

    const hero = useMemo(() => {
        if (!programs.length) return null;
        const top = programs[0];
        return {
            headline: `${top.name} (${top.pid ?? '—'})`,
            host: top.hostname,
            score: top.score,
        };
    }, [programs]);

    const summaryCards = useMemo(() => buildSummary(summary, programs), [summary, programs]);

    return (
        <div className="h-full flex flex-col bg-card/60 backdrop-blur rounded-xl border border-border shadow-lg overflow-hidden">
            <div className="px-4 py-3 border-b border-border flex items-center gap-3">
                <Server className="w-4 h-4 text-primary" />
                <div className="flex-1">
                    <div className="text-sm font-semibold">Top Resource-Intensive / Erroring Programs</div>
                    <div className="text-xs text-muted-foreground">CPU · Memory · Disk IO · Network · GPU · Logs</div>
                </div>
                <div className={clsx('text-[11px] px-3 py-1 rounded-full border', severityColor(hero?.score ?? 0))}>
                    {hero ? `Top score: ${hero.score.toFixed(2)}` : 'No data'}
                </div>
            </div>

            {isLoading ? (
                <div className="flex-1 flex items-center justify-center text-muted-foreground text-sm">Loading…</div>
            ) : error ? (
                <div className="flex-1 flex items-center justify-center text-rose-300 text-sm">Failed to load top programs</div>
            ) : programs.length === 0 ? (
                <div className="flex-1 flex items-center justify-center text-muted-foreground text-sm">No programs reported yet</div>
            ) : (
                <div className="flex-1 overflow-auto">
                    <div className="grid grid-cols-2 lg:grid-cols-3 gap-3 px-4 pt-3 pb-2">
                        {summaryCards.map((card) => (
                            <div key={card.key} className="border border-border/70 rounded-lg px-3 py-2 bg-muted/30 flex items-center gap-2">
                                <div className="p-2 rounded-md bg-card border border-border/60 text-primary">{card.icon}</div>
                                <div className="flex-1 min-w-0">
                                    <div className="text-[11px] uppercase tracking-wide text-muted-foreground">{card.label}</div>
                                    <div className="text-sm font-semibold truncate">{card.program ?? '—'}</div>
                                    <div className="text-[11px] text-muted-foreground truncate">{card.value}</div>
                                </div>
                            </div>
                        ))}
                    </div>
                    <table className="w-full text-xs">
                        <thead className="sticky top-0 bg-card/90 backdrop-blur border-b border-border">
                            <tr className="text-muted-foreground">
                                <th className="text-left px-4 py-2">Program</th>
                                <th className="text-right px-2">CPU%</th>
                                <th className="text-right px-2">Mem</th>
                                <th className="text-right px-2">Disk (read / write)</th>
                                <th className="text-right px-2">Net (bps / queued / conns)</th>
                                <th className="text-right px-2">GPU (sm/mem/enc/ctx)</th>
                                <th className="text-right px-2">Logs</th>
                                <th className="text-right px-4">Score</th>
                            </tr>
                        </thead>
                        <tbody>
                            {programs.map((p) => (
                                <tr key={`${p.collector_id}-${p.pid}-${p.name}`} className="border-b border-border/60 hover:bg-muted/30">
                                    <td className="px-4 py-2">
                                        <div className="font-semibold text-foreground">{p.name}</div>
                                        <div className="text-[11px] text-muted-foreground">
                                            pid {p.pid ?? '—'} • {p.hostname}
                                        </div>
                                        <div className="flex flex-wrap gap-1 mt-1">
                                            {(p.categories ?? []).map((c) => (
                                                <span key={c} className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-muted text-[10px] text-foreground border border-border/70">
                                                    {categoryIcons[c] ?? <Activity className="w-3 h-3" />} {c}
                                                </span>
                                            ))}
                                        </div>
                                    </td>
                                    <td className="text-right px-2">{p.cpu_percent?.toFixed(1) ?? '—'}</td>
                                    <td className="text-right px-2">{formatBytes(p.memory_bytes)}</td>
                                    <td className="text-right px-2">
                                        <div>R {formatRate(p.disk_read_bps)}</div>
                                        <div className="text-[10px] text-muted-foreground">W {formatRate(p.disk_write_bps)}</div>
                                    </td>
                                    <td className="text-right px-2">
                                        {formatRate(p.net_bytes_per_second)}<br />
                                        <span className="text-[10px] text-muted-foreground">{formatBytes(p.net_queued_bytes)} / {p.net_connections ?? 0}c</span>
                                    </td>
                                    <td className="text-right px-2">
                                        {p.gpu_util_sm_percent ? `${p.gpu_util_sm_percent.toFixed(0)}%` : '—'} / {p.gpu_mem_mib ? `${p.gpu_mem_mib.toFixed(0)} MiB` : '—'} / {p.gpu_util_enc_percent ? `${p.gpu_util_enc_percent.toFixed(0)}%` : '—'} / {p.gpu_context_active ? p.gpu_context_active.toFixed(0) : '—'}
                                    </td>
                                    <td className="text-right px-2">
                                        {p.log_errors ?? 0}e / {p.log_warnings ?? 0}w
                                    </td>
                                    <td className="text-right px-4 font-semibold text-primary">{p.score.toFixed(2)}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
        </div>
    );
}

type SummaryCard = { key: string; label: string; value: string; program?: string; icon: JSX.Element };

function buildSummary(summary: TopProgramsResponse['summary'], programs: ProgramStats[]): SummaryCard[] {
    const fallback = (cat: string): ProgramStats | undefined => summary?.[cat] ?? programs.find((p) => p.categories?.includes(cat));
    const cards: SummaryCard[] = [
        makeCard('cpu', 'Top CPU', fallback('cpu'), p => `${p?.cpu_percent?.toFixed(1) ?? '—'}%`, <Cpu className="w-4 h-4" />),
        makeCard('memory', 'Top Memory', fallback('memory'), p => formatBytes(p?.memory_bytes), <MemoryStick className="w-4 h-4" />),
        makeCard('disk', 'Top Disk IO', fallback('disk'), p => `R ${formatRate(p?.disk_read_bps)} · W ${formatRate(p?.disk_write_bps)}`, <HardDrive className="w-4 h-4" />),
        makeCard('network', 'Top Network', fallback('network'), p => `${formatRate(p?.net_bytes_per_second)} / ${formatBytes(p?.net_queued_bytes)} / ${p?.net_connections ?? 0}c`, <Wifi className="w-4 h-4" />),
        makeCard('gpu', 'Top GPU', fallback('gpu'), p => `${p?.gpu_util_sm_percent ? `${p.gpu_util_sm_percent.toFixed(0)}%` : '—'} / ${p?.gpu_mem_mib ? `${p.gpu_mem_mib.toFixed(0)} MiB` : '—'}`, <Gauge className="w-4 h-4" />),
        makeCard('logs', 'Most Errors/Warnings', fallback('logs'), p => `${p?.log_errors ?? 0}e / ${p?.log_warnings ?? 0}w`, <Bug className="w-4 h-4" />),
    ];
    return cards.filter(Boolean);
}

function makeCard(key: string, label: string, p: ProgramStats | undefined, val: (p?: ProgramStats) => string, icon: JSX.Element): SummaryCard {
    return {
        key,
        label,
        program: p?.name,
        value: val(p),
        icon,
    };
}
