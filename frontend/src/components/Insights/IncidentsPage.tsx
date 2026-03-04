import React, { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { AlertOctagon, Clock3, ListChecks, Siren } from 'lucide-react';
import {
    CartesianGrid,
    Line,
    LineChart,
    ResponsiveContainer,
    Tooltip,
    XAxis,
    YAxis,
} from 'recharts';
import { fetchFleetNodes } from '@/api/trends';
import {
    fetchWorkflowIncidentByID,
    fetchWorkflowIncidents,
    type AgentIncidentReport,
} from '@/api/agentWorkflows';
import { formatCount, formatPercent } from '@/components/Visualizations/metricFormat';

function formatTS(value?: string): string {
    if (!value) {
        return 'n/a';
    }
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) {
        return value;
    }
    return parsed.toLocaleTimeString();
}

function statusBadge(status: string): string {
    switch (status.toLowerCase()) {
        case 'open':
        case 'active':
            return 'border-rose-500/40 bg-rose-500/15 text-rose-200';
        case 'closed':
        case 'resolved':
            return 'border-emerald-500/40 bg-emerald-500/15 text-emerald-200';
        default:
            return 'border-amber-500/40 bg-amber-500/15 text-amber-200';
    }
}

function riskTone(level: string): string {
    switch (level.toLowerCase()) {
        case 'critical':
            return 'text-rose-300';
        case 'high':
            return 'text-orange-300';
        case 'medium':
            return 'text-amber-300';
        default:
            return 'text-sky-300';
    }
}

function loopSummary(incident?: AgentIncidentReport): string {
    const loop = incident?.agent_loop;
    if (!loop) {
        return 'Plan/Act/Verify summary unavailable';
    }
    return `iterations=${loop.iterations}, replans=${loop.replans}, executed=${loop.steps_executed}, verified=${loop.steps_verified}, stop=${loop.stop_reason}`;
}

export default function IncidentsPage() {
    const [selectedCollector, setSelectedCollector] = useState('');
    const [windowSize, setWindowSize] = useState('45m');
    const [statusFilter, setStatusFilter] = useState('');
    const [selectedIncidentID, setSelectedIncidentID] = useState('');

    const nodesQuery = useQuery({
        queryKey: ['incidents-page-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const incidentsQuery = useQuery({
        queryKey: ['workflow-incidents', selectedCollector, statusFilter, windowSize],
        queryFn: () =>
            fetchWorkflowIncidents({
                collectorId: selectedCollector || undefined,
                status: statusFilter || undefined,
                window: windowSize,
                limit: 40,
            }),
        refetchInterval: 15000,
    });

    const incidents = incidentsQuery.data?.incidents ?? [];
    useEffect(() => {
        if (incidents.length === 0) {
            setSelectedIncidentID('');
            return;
        }
        if (!selectedIncidentID || !incidents.some((incident) => incident.incident_id === selectedIncidentID)) {
            setSelectedIncidentID(incidents[0].incident_id);
        }
    }, [incidents, selectedIncidentID]);

    const incidentDetailQuery = useQuery({
        queryKey: ['workflow-incident-detail', selectedIncidentID],
        queryFn: () => fetchWorkflowIncidentByID(selectedIncidentID),
        enabled: selectedIncidentID.trim().length > 0,
        refetchInterval: 15000,
    });

    const selectedIncident = incidentDetailQuery.data?.incident ?? incidents.find((item) => item.incident_id === selectedIncidentID) ?? incidents[0];

    const timelineData = useMemo(() => {
        const timeline = selectedIncident?.timeline ?? [];
        return timeline.map((item, index) => ({
            timestamp: item.timestamp,
            order: index + 1,
            phase: item.phase,
            summary: item.summary,
        }));
    }, [selectedIncident?.timeline]);

    const openCount = incidents.filter((incident) => incident.status.toLowerCase() !== 'closed').length;
    const closedCount = incidents.filter((incident) => incident.status.toLowerCase() === 'closed').length;

    return (
        <div className="space-y-4" data-testid="incidents-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                        <h2 className="text-lg font-semibold flex items-center gap-2">
                            <Siren className="w-5 h-5 text-rose-300" />
                            Incidents
                        </h2>
                        <p className="text-xs text-muted-foreground mt-1">
                            Agentic incident investigations with timeline, evidence, hypotheses, and guarded recommendations.
                        </p>
                    </div>
                    <div className="flex items-center gap-2">
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={selectedCollector}
                            onChange={(event) => setSelectedCollector(event.target.value)}
                        >
                            <option value="">All collectors</option>
                            {(nodesQuery.data?.nodes ?? []).map((node) => (
                                <option key={node.collector_id} value={node.collector_id}>
                                    {node.hostname || node.collector_id}
                                </option>
                            ))}
                        </select>
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={statusFilter}
                            onChange={(event) => setStatusFilter(event.target.value)}
                        >
                            <option value="">All statuses</option>
                            <option value="open">Open</option>
                            <option value="investigating">Investigating</option>
                            <option value="closed">Closed</option>
                        </select>
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={windowSize}
                            onChange={(event) => setWindowSize(event.target.value)}
                        >
                            <option value="30m">30m</option>
                            <option value="45m">45m</option>
                            <option value="1h">1h</option>
                            <option value="2h">2h</option>
                        </select>
                    </div>
                </div>
            </section>

            <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <article className="rounded-lg border border-border bg-card p-3">
                    <div className="text-xs text-muted-foreground flex items-center gap-2">
                        <AlertOctagon className="w-4 h-4 text-rose-300" />
                        Open Incidents
                    </div>
                    <div className="text-xl font-semibold mt-1">{formatCount(openCount)}</div>
                </article>
                <article className="rounded-lg border border-border bg-card p-3">
                    <div className="text-xs text-muted-foreground flex items-center gap-2">
                        <Clock3 className="w-4 h-4 text-emerald-300" />
                        Closed Incidents
                    </div>
                    <div className="text-xl font-semibold mt-1">{formatCount(closedCount)}</div>
                </article>
                <article className="rounded-lg border border-border bg-card p-3">
                    <div className="text-xs text-muted-foreground flex items-center gap-2">
                        <ListChecks className="w-4 h-4 text-cyan-300" />
                        Selected Risk
                    </div>
                    <div className={`text-xl font-semibold mt-1 ${riskTone(selectedIncident?.risk_level ?? '')}`}>
                        {formatPercent((selectedIncident?.risk_score ?? 0) * 100, 1)}
                    </div>
                </article>
                <article className="rounded-lg border border-border bg-card p-3">
                    <div className="text-xs text-muted-foreground flex items-center gap-2">
                        <ListChecks className="w-4 h-4 text-violet-300" />
                        Confidence
                    </div>
                    <div className="text-xl font-semibold mt-1">{formatPercent((selectedIncident?.confidence ?? 0) * 100, 1)}</div>
                </article>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Incident List</h3>
                    {incidentsQuery.isLoading && <div className="text-sm text-muted-foreground">Loading incidents…</div>}
                    {incidentsQuery.isError && (
                        <div className="text-sm text-rose-300">Incident API unavailable.</div>
                    )}
                    {incidents.length === 0 && !incidentsQuery.isLoading && !incidentsQuery.isError && (
                        <div className="text-sm text-muted-foreground">No incidents in selected filters.</div>
                    )}
                    <div className="space-y-2">
                        {incidents.map((incident, index) => (
                            <button
                                key={incident.incident_id}
                                type="button"
                                onClick={() => setSelectedIncidentID(incident.incident_id)}
                                className={`w-full rounded border p-3 text-left transition-colors ${
                                    incident.incident_id === selectedIncident?.incident_id
                                        ? 'border-rose-300 bg-rose-300/10'
                                        : 'border-border bg-background/50 hover:bg-muted/40'
                                }`}
                            >
                                <div className="flex items-center justify-between gap-2">
                                    <div className="text-sm font-medium">
                                        #{index + 1} {incident.incident_id}
                                    </div>
                                    <span className={`rounded px-2 py-0.5 text-[10px] border ${statusBadge(incident.status)}`}>
                                        {incident.status}
                                    </span>
                                </div>
                                <div className="text-xs text-muted-foreground mt-1 line-clamp-2">{incident.summary}</div>
                                <div className="text-[11px] text-muted-foreground mt-1">
                                    risk={incident.risk_level}/{formatPercent(incident.risk_score * 100, 1)} · opened={formatTS(incident.opened_at)}
                                </div>
                            </button>
                        ))}
                    </div>
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Incident Evidence</h3>
                    {!selectedIncident && (
                        <div className="text-sm text-muted-foreground">Select an incident to inspect details.</div>
                    )}
                    {selectedIncident && (
                        <div className="space-y-3">
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{selectedIncident.summary}</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    cause={selectedIncident.most_likely_cause || 'n/a'} · collector={selectedIncident.collector_id || 'n/a'}
                                </div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    {loopSummary(selectedIncident)}
                                </div>
                            </article>
                            <div>
                                <h4 className="text-sm font-semibold mb-1">Ranked Hypotheses</h4>
                                <div className="space-y-1">
                                    {(selectedIncident.hypotheses ?? []).slice(0, 4).map((item) => (
                                        <div key={item.id} className="rounded border border-border bg-background/40 p-2 text-xs text-muted-foreground">
                                            #{item.rank} {item.title} · confidence={formatPercent(item.confidence * 100, 1)}
                                        </div>
                                    ))}
                                    {(selectedIncident.hypotheses ?? []).length === 0 && (
                                        <div className="text-xs text-muted-foreground">No hypotheses recorded.</div>
                                    )}
                                </div>
                            </div>
                            <div>
                                <h4 className="text-sm font-semibold mb-1">Evidence</h4>
                                <div className="space-y-1">
                                    {(selectedIncident.evidence ?? []).slice(0, 8).map((item) => (
                                        <div key={item.id} className="rounded border border-border bg-background/40 p-2 text-xs text-muted-foreground">
                                            {item.summary}
                                        </div>
                                    ))}
                                    {(selectedIncident.evidence ?? []).length === 0 && (
                                        <div className="text-xs text-muted-foreground">No evidence rows.</div>
                                    )}
                                </div>
                            </div>
                        </div>
                    )}
                </section>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4 min-h-[300px]">
                    <h3 className="font-semibold mb-2">Incident Timeline</h3>
                    {timelineData.length === 0 ? (
                        <div className="text-sm text-muted-foreground">No timeline events for selected incident.</div>
                    ) : (
                        <div className="h-[250px]">
                            <ResponsiveContainer width="100%" height="100%">
                                <LineChart data={timelineData}>
                                    <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.2)" />
                                    <XAxis dataKey="timestamp" hide />
                                    <YAxis dataKey="order" tick={{ fill: '#94a3b8', fontSize: 11 }} />
                                    <Tooltip
                                        formatter={(value: number) => [formatCount(value), 'order']}
                                        labelFormatter={(value) => new Date(String(value)).toLocaleTimeString()}
                                    />
                                    <Line type="monotone" dataKey="order" stroke="#22d3ee" dot={false} strokeWidth={2} />
                                </LineChart>
                            </ResponsiveContainer>
                        </div>
                    )}
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-2">Recommended Actions</h3>
                    <div className="space-y-2">
                        {(selectedIncident?.recommendations ?? []).slice(0, 8).map((item) => (
                            <article key={item.id} className="rounded border border-border bg-background/40 p-3">
                                <div className="text-sm font-medium">{item.summary}</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    priority={item.priority} · safe={String(item.safe)} · dry_run_default={String(item.dry_run_default)} · requires_approval={String(item.requires_approval)}
                                </div>
                            </article>
                        ))}
                        {(selectedIncident?.recommendations ?? []).length === 0 && (
                            <div className="text-sm text-muted-foreground">No recommended actions recorded.</div>
                        )}
                    </div>
                </section>
            </div>
        </div>
    );
}

