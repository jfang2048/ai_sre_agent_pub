import React from 'react';
import { Activity, FileSearch, Radar } from 'lucide-react';
import type { InvestigationEvent, RetrievalDecision, TrendAssessment } from '@/api/agentWorkflows';
import { formatPercent } from '@/components/Visualizations/metricFormat';

function severityTone(severity?: string) {
    switch ((severity || '').toLowerCase()) {
    case 'critical':
    case 'high':
        return 'border-rose-500/40 bg-rose-500/10 text-rose-200';
    case 'medium':
        return 'border-amber-500/40 bg-amber-500/10 text-amber-100';
    default:
        return 'border-sky-500/30 bg-sky-500/10 text-sky-100';
    }
}

function formatNumeric(value?: number) {
    if (typeof value !== 'number' || !Number.isFinite(value)) {
        return '—';
    }
    if (Math.abs(value) >= 1000) {
        return value.toLocaleString(undefined, { maximumFractionDigits: 1 });
    }
    return value.toFixed(2);
}

function PanelShell({
    icon,
    title,
    children,
}: {
    icon: React.ReactNode;
    title: string;
    children: React.ReactNode;
}) {
    return (
        <section className="rounded-lg border border-border bg-card p-4">
            <h3 className="mb-3 flex items-center gap-2 font-semibold">
                {icon}
                {title}
            </h3>
            {children}
        </section>
    );
}

function SeverityPill({ severity }: { severity?: string }) {
    return (
        <div className={`rounded-full border px-2 py-0.5 text-[11px] uppercase tracking-wide ${severityTone(severity)}`}>
            {severity}
        </div>
    );
}

export function TrendWatchPanel({
    title = 'Trend Watch',
    trends,
    emptyText,
}: {
    title?: string;
    trends?: TrendAssessment[];
    emptyText: string;
}) {
    const rows = (trends ?? []).slice(0, 6);
    return (
        <PanelShell icon={<Activity className="h-4 w-4 text-cyan-300" />} title={title}>
            {rows.length === 0 ? (
                <div className="text-sm text-muted-foreground">{emptyText}</div>
            ) : (
                <div className="space-y-2">
                    {rows.map((trend) => (
                        <article key={trend.id} className="rounded border border-border bg-background/50 p-3">
                            <div className="flex items-start justify-between gap-3">
                                <div>
                                    <div className="text-sm font-medium">{trend.display}</div>
                                    <div className="mt-1 text-xs text-muted-foreground">
                                        {trend.scope}/{trend.entity} · {trend.trend.replace(/_/g, ' ')} · confidence {formatPercent(trend.confidence * 100, 0)}
                                    </div>
                                </div>
                                <SeverityPill severity={trend.severity} />
                            </div>
                            <div className="mt-2 text-xs text-muted-foreground">{trend.summary}</div>
                            <div className="mt-2 grid grid-cols-2 gap-2 text-[11px] text-muted-foreground md:grid-cols-4">
                                <div>latest {formatNumeric(trend.latest)}</div>
                                <div>baseline {formatNumeric(trend.baseline)}</div>
                                <div>delta {formatPercent(trend.delta_percent, 1)}</div>
                                <div>slope {formatNumeric(trend.slope_per_minute)}/min</div>
                            </div>
                            {trend.forecast && (
                                <div className="mt-2 rounded border border-cyan-500/20 bg-cyan-500/5 px-2 py-1 text-[11px] text-cyan-100">
                                    Forecast: {trend.forecast}
                                </div>
                            )}
                            {trend.operator_hint && (
                                <div className="mt-2 text-[11px] text-muted-foreground">Operator hint: {trend.operator_hint}</div>
                            )}
                        </article>
                    ))}
                </div>
            )}
        </PanelShell>
    );
}

export function InvestigationEventsPanel({
    title = 'Investigation Events',
    events,
    emptyText,
}: {
    title?: string;
    events?: InvestigationEvent[];
    emptyText: string;
}) {
    const rows = (events ?? []).slice(0, 6);
    return (
        <PanelShell icon={<Radar className="h-4 w-4 text-amber-300" />} title={title}>
            {rows.length === 0 ? (
                <div className="text-sm text-muted-foreground">{emptyText}</div>
            ) : (
                <div className="space-y-3">
                    {rows.map((event) => (
                        <article key={event.id} className="rounded border border-border bg-background/50 p-3">
                            <div className="flex items-start justify-between gap-3">
                                <div>
                                    <div className="text-sm font-medium">{event.title}</div>
                                    <div className="mt-1 text-xs text-muted-foreground">
                                        {event.category.replace(/_/g, ' ')} · {event.scope}/{event.entity}
                                    </div>
                                </div>
                                <SeverityPill severity={event.severity} />
                            </div>
                            <div className="mt-2 text-xs text-muted-foreground">{event.summary}</div>
                            {event.probable_cause && (
                                <div className="mt-2 text-[11px] text-cyan-100">Probable cause: {event.probable_cause}</div>
                            )}
                            {(event.supporting_signals ?? []).length > 0 && (
                                <div className="mt-2 text-[11px] text-muted-foreground">
                                    Signals: {event.supporting_signals?.slice(0, 4).join(' · ')}
                                </div>
                            )}
                            {(event.recommended_checks ?? []).length > 0 && (
                                <div className="mt-2 text-[11px] text-muted-foreground">
                                    Checks: {event.recommended_checks?.slice(0, 3).join(' · ')}
                                </div>
                            )}
                        </article>
                    ))}
                </div>
            )}
        </PanelShell>
    );
}

export function RetrievalDecisionPanel({
    title = 'Retrieval Decisions',
    decisions,
    emptyText,
}: {
    title?: string;
    decisions?: RetrievalDecision[];
    emptyText: string;
}) {
    const rows = (decisions ?? []).slice(0, 6);
    return (
        <PanelShell icon={<FileSearch className="h-4 w-4 text-violet-300" />} title={title}>
            {rows.length === 0 ? (
                <div className="text-sm text-muted-foreground">{emptyText}</div>
            ) : (
                <div className="space-y-2">
                    {rows.map((decision, index) => (
                        <article key={`${decision.tool}-${decision.phase}-${index}`} className="rounded border border-border bg-background/50 p-3">
                            <div className="flex items-center justify-between gap-2">
                                <div className="text-sm font-medium">{decision.tool}</div>
                                <div className={`rounded-full border px-2 py-0.5 text-[11px] uppercase tracking-wide ${decision.skipped ? 'border-slate-500/30 bg-slate-500/10 text-slate-200' : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-200'}`}>
                                    {decision.skipped ? 'skipped' : 'used'}
                                </div>
                            </div>
                            <div className="mt-1 text-xs text-muted-foreground">
                                phase={decision.phase} · intent={decision.intent}
                            </div>
                            {decision.query && (
                                <div className="mt-2 rounded border border-violet-500/20 bg-violet-500/5 px-2 py-1 text-[11px] text-violet-100">
                                    query: {decision.query}
                                </div>
                            )}
                            {decision.skip_reason && (
                                <div className="mt-2 text-[11px] text-muted-foreground">reason: {decision.skip_reason}</div>
                            )}
                            {(decision.evidence_signals ?? []).length > 0 && (
                                <div className="mt-2 text-[11px] text-muted-foreground">
                                    evidence: {decision.evidence_signals?.slice(0, 5).join(' · ')}
                                </div>
                            )}
                        </article>
                    ))}
                </div>
            )}
        </PanelShell>
    );
}
