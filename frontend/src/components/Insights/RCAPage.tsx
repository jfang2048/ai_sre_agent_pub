import React, { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
    BrainCircuit,
    FileSearch,
    ListChecks,
    ShieldAlert,
    ShieldCheck,
    Workflow,
} from 'lucide-react';
import {
    CartesianGrid,
    Legend,
    Line,
    LineChart,
    ResponsiveContainer,
    Tooltip,
    XAxis,
    YAxis,
} from 'recharts';
import { fetchFleetNodes } from '@/api/trends';
import {
    fetchAgentTrace,
    fetchJointRiskReports,
    fetchRCAWorkflowReports,
    fetchWorkflowAuditRecords,
} from '@/api/agentWorkflows';
import {
    InvestigationEventsPanel,
    RetrievalDecisionPanel,
    TrendWatchPanel,
} from '@/components/Insights/InvestigationPanels';
import { buildPreferredRiskChartSeries, buildRiskChartData } from '@/components/Insights/riskChart';
import KnowledgeEvidencePanel from '@/components/Insights/KnowledgeEvidencePanel';
import { formatCount, formatPercent } from '@/components/Visualizations/metricFormat';

const CHART_COLORS = ['#38bdf8', '#34d399', '#f59e0b', '#fb7185', '#a78bfa'];

function loopStatusTone(completed?: boolean): string {
    return completed ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-200' : 'border-amber-500/30 bg-amber-500/10 text-amber-200';
}

export default function RCAPage() {
    const [selectedCollector, setSelectedCollector] = useState('');
    const [windowSize, setWindowSize] = useState('45m');

    const nodesQuery = useQuery({
        queryKey: ['rca-nodes'],
        queryFn: fetchFleetNodes,
        refetchInterval: 30000,
    });

    const cachedRCAQuery = useQuery({
        queryKey: ['rca-workflows', selectedCollector, windowSize, 'cached'],
        queryFn: () => fetchRCAWorkflowReports({
            collectorId: selectedCollector || undefined,
            window: windowSize,
            limit: 8,
            refresh: false,
        }),
        refetchInterval: 15000,
    });

    const rcaQuery = useQuery({
        queryKey: ['rca-workflows', selectedCollector, windowSize, 'refresh'],
        queryFn: () => fetchRCAWorkflowReports({
            collectorId: selectedCollector || undefined,
            window: windowSize,
            limit: 8,
            refresh: true,
        }),
        refetchInterval: 60000,
    });

    const reports = rcaQuery.data?.reports ?? cachedRCAQuery.data?.reports ?? [];
    const selectedReport = reports[0];
    const isPrimingReport = reports.length === 0
        && (rcaQuery.isLoading || rcaQuery.isFetching || cachedRCAQuery.isLoading);
    const reportStatusText = isPrimingReport
        ? 'Generating latest RCA workflow report...'
        : rcaQuery.isFetching
            ? 'Refreshing RCA workflow report...'
            : '';

    const riskSeriesQuery = useQuery({
        queryKey: ['rca-risk-series', selectedCollector, windowSize],
        queryFn: () => fetchJointRiskReports({
            collectorId: selectedCollector || undefined,
            window: windowSize,
            limit: 1,
            refresh: false,
        }),
        refetchInterval: 15000,
    });

    const auditQuery = useQuery({
        queryKey: ['rca-audit', selectedReport?.workflow_id],
        queryFn: () => fetchWorkflowAuditRecords(30, selectedReport?.workflow_id ?? ''),
        enabled: Boolean(selectedReport?.workflow_id),
        refetchInterval: 15000,
    });

    const traceQuery = useQuery({
        queryKey: ['rca-trace', selectedReport?.trace_id ?? selectedReport?.workflow_id],
        queryFn: () => fetchAgentTrace(selectedReport?.trace_id ?? selectedReport?.workflow_id ?? ''),
        enabled: Boolean((selectedReport?.trace_id ?? selectedReport?.workflow_id)?.trim()),
        refetchInterval: 15000,
    });

    const chartSeries = useMemo(() => {
        return buildPreferredRiskChartSeries(
            riskSeriesQuery.data?.reports?.[0]?.series ?? [],
            ['cpu_pressure', 'io_latency', 'retransmit_ratio', 'log_burst'],
            4,
            'rca',
            CHART_COLORS,
        );
    }, [riskSeriesQuery.data?.reports]);

    const chartData = useMemo(() => buildRiskChartData(chartSeries), [chartSeries]);

    return (
        <div className="space-y-4" data-testid="rca-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                        <h2 className="text-lg font-semibold flex items-center gap-2">
                            <BrainCircuit className="w-5 h-5 text-cyan-300" />
                            RCA Workflow
                        </h2>
                        <p className="text-xs text-muted-foreground mt-1">
                            Deterministic pipeline: anomaly detection → context gathering → hypotheses → evidence → guarded remediation plan.
                        </p>
                        {reportStatusText && <p className="text-xs text-muted-foreground mt-2">{reportStatusText}</p>}
                    </div>
                    <div className="flex items-center gap-2">
                        <select
                            className="rounded border border-border bg-background px-2 py-1 text-sm"
                            value={selectedCollector}
                            onChange={(event) => setSelectedCollector(event.target.value)}
                        >
                            <option value="">Latest node</option>
                            {(nodesQuery.data?.nodes ?? []).map((node) => (
                                <option key={node.collector_id} value={node.collector_id}>
                                    {node.hostname || node.collector_id}
                                </option>
                            ))}
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
                <SummaryCard label="Incident" value={isPrimingReport ? 'Loading...' : selectedReport?.incident_id ?? 'n/a'} detail={selectedReport?.status ?? 'n/a'} />
                <SummaryCard label="Trigger" value={isPrimingReport ? 'Loading...' : selectedReport?.trigger ?? 'n/a'} detail="workflow entrypoint" />
                <SummaryCard label="Hypotheses" value={isPrimingReport ? 'Loading...' : formatCount(selectedReport?.hypotheses?.length)} detail="ranked root causes" />
                <SummaryCard label="Evidence" value={isPrimingReport ? 'Loading...' : formatCount(selectedReport?.evidence?.length)} detail="metrics/log/probe context" />
            </div>

            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                        <div className="text-sm font-semibold">Investigation headline</div>
                        <div className="mt-1 text-sm text-muted-foreground">
                            {isPrimingReport
                                ? 'Generating control-plane summary...'
                                : selectedReport?.structured_report?.incident_summary
                                    || selectedReport?.synthesized_incident?.summary
                                    || selectedReport?.context?.incident_summary
                                    || 'No structured incident summary yet.'}
                        </div>
                    </div>
                    {!isPrimingReport && selectedReport?.context?.investigation_events?.[0] && (
                        <div className="rounded-lg border border-cyan-500/30 bg-cyan-500/10 px-3 py-2 text-sm text-cyan-100">
                            <div className="font-medium">{selectedReport.context.investigation_events[0].title}</div>
                            {selectedReport.context.investigation_events[0].probable_cause && (
                                <div className="mt-1 text-xs text-cyan-50/90">Probable cause: {selectedReport.context.investigation_events[0].probable_cause}</div>
                            )}
                        </div>
                    )}
                </div>
            </section>

            <section className="rounded-lg border border-border bg-card p-4 min-h-[320px]">
                <div className="text-sm font-semibold mb-2 flex items-center gap-2">
                    <Workflow className="w-4 h-4" />
                    RCA Time Series Context
                </div>
                {isPrimingReport ? (
                    <div className="text-sm text-muted-foreground">Generating RCA time-series context...</div>
                ) : chartData.length === 0 ? (
                    <div className="text-sm text-muted-foreground">No contextual series available.</div>
                ) : (
                    <div className="h-[280px]">
                        <ResponsiveContainer width="100%" height="100%">
                            <LineChart data={chartData} margin={{ top: 8, right: 20, left: 0, bottom: 0 }}>
                                <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.2)" />
                                <XAxis dataKey="timestamp" hide />
                                <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} />
                                <Tooltip
                                    formatter={(value: number, name: string) => [formatValue(Number(value)), name]}
                                    labelFormatter={(value) => new Date(String(value)).toLocaleTimeString()}
                                />
                                <Legend />
                                {chartSeries.map((item) => (
                                    <Line
                                        key={item.dataKey}
                                        type="monotone"
                                        dataKey={item.dataKey}
                                        name={item.label}
                                        stroke={item.color}
                                        dot={false}
                                        strokeWidth={2}
                                    />
                                ))}
                            </LineChart>
                        </ResponsiveContainer>
                    </div>
                )}
            </section>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <InvestigationEventsPanel
                    title="Investigation Events"
                    events={selectedReport?.context?.investigation_events}
                    emptyText={isPrimingReport ? 'Generating investigation events...' : 'No investigation events were promoted into this RCA context.'}
                />
                <TrendWatchPanel
                    title="Trend and Forecast Watch"
                    trends={selectedReport?.context?.trend_assessments}
                    emptyText={isPrimingReport ? 'Generating trend watch...' : 'No trend assessments were attached to this RCA context.'}
                />
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Structured RCA Report</h3>
                    {isPrimingReport && (
                        <div className="text-sm text-muted-foreground mb-4">Generating latest RCA report...</div>
                    )}
                    {!isPrimingReport && selectedReport?.synthesized_incident && (
                        <div className="space-y-2 mb-4">
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">Synthesized Incident</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    {selectedReport.synthesized_incident.summary}
                                </div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    severity={selectedReport.synthesized_incident.severity} · confidence {formatPercent((selectedReport.synthesized_incident.confidence ?? 0) * 100, 1)}
                                </div>
                                {(selectedReport.synthesized_incident.impacted_scope ?? []).length > 0 && (
                                    <div className="text-xs text-muted-foreground mt-1">
                                        scope: {selectedReport.synthesized_incident.impacted_scope.join(' · ')}
                                    </div>
                                )}
                                {(selectedReport.synthesized_incident.grouped_signals ?? []).length > 0 && (
                                    <div className="text-xs text-muted-foreground mt-2">
                                        grouped signals: {selectedReport.synthesized_incident.grouped_signals.slice(0, 4).map((item) => item.signal_type).join(' · ')}
                                    </div>
                                )}
                            </article>
                        </div>
                    )}
                    {isPrimingReport ? (
                        <div className="text-sm text-muted-foreground">Generating structured RCA report...</div>
                    ) : !selectedReport?.structured_report ? (
                        <div className="text-sm text-muted-foreground">No structured report available.</div>
                    ) : (
                        <div className="space-y-2 mb-4">
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">Most likely cause</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    {selectedReport.structured_report.most_likely_cause || 'n/a'} · confidence {formatPercent((selectedReport.structured_report.confidence ?? 0) * 100, 1)}
                                </div>
                            </article>
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">Supporting signals</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    {(selectedReport.structured_report.supporting_signals ?? []).join(' · ') || 'n/a'}
                                </div>
                            </article>
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">Disconfirming signals</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    {(selectedReport.structured_report.disconfirming_signals ?? []).join(' · ') || 'none'}
                                </div>
                            </article>
                            {(selectedReport.structured_report.unresolved_gaps ?? []).length > 0 && (
                                <article className="rounded border border-border bg-background/50 p-3">
                                    <div className="text-sm font-medium">Unresolved gaps</div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        {(selectedReport.structured_report.unresolved_gaps ?? []).join(' · ')}
                                    </div>
                                </article>
                            )}
                        </div>
                    )}

                    <h3 className="font-semibold mb-3 flex items-center gap-2"><FileSearch className="w-4 h-4" /> Ranked Hypotheses</h3>
                    {isPrimingReport ? (
                        <div className="text-sm text-muted-foreground">Generating ranked hypotheses...</div>
                    ) : (selectedReport?.hypotheses ?? []).length === 0 ? (
                        <div className="text-sm text-muted-foreground">No hypotheses produced yet.</div>
                    ) : (
                        <div className="space-y-2">
                            {(selectedReport?.hypotheses ?? []).map((hypothesis) => (
                                <article key={hypothesis.id} className="rounded border border-border bg-background/50 p-3">
                                    <div className="flex items-center justify-between gap-2">
                                        <div className="text-sm font-medium">#{hypothesis.rank} {hypothesis.title}</div>
                                        <div className="text-xs text-muted-foreground">{formatPercent(hypothesis.confidence * 100, 0)}</div>
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">{hypothesis.description}</div>
                                    {(hypothesis.evidence_ids ?? []).length > 0 && (
                                        <div className="text-xs text-muted-foreground mt-1">Evidence: {(hypothesis.evidence_ids ?? []).slice(0, 4).join(' · ')}</div>
                                    )}
                                    {(hypothesis.contradicting_evidence_ids ?? []).length > 0 && (
                                        <div className="text-xs text-muted-foreground mt-1">Contradicting: {(hypothesis.contradicting_evidence_ids ?? []).slice(0, 4).join(' · ')}</div>
                                    )}
                                </article>
                            ))}
                        </div>
                    )}
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Plan → Act → Verify Trace</h3>
                    {isPrimingReport ? (
                        <div className="text-sm text-muted-foreground">Generating agent loop trace...</div>
                    ) : !selectedReport?.agent_loop ? (
                        <div className="text-sm text-muted-foreground">No agent loop trace available.</div>
                    ) : (
                        <div className="space-y-2 mb-4">
                            <article className={`rounded border p-3 ${loopStatusTone(selectedReport.agent_loop.completed)}`}>
                                <div className="flex items-center justify-between gap-2">
                                    <div className="text-sm font-medium">
                                        {selectedReport.agent_loop.completed ? (
                                            <span className="inline-flex items-center gap-2"><ShieldCheck className="h-4 w-4" /> Required evidence verified</span>
                                        ) : (
                                            <span className="inline-flex items-center gap-2"><ShieldAlert className="h-4 w-4" /> Verification gaps remain</span>
                                        )}
                                    </div>
                                    <div className="text-[11px] uppercase tracking-wide opacity-80">
                                        completed={String(selectedReport.agent_loop.completed)}
                                    </div>
                                </div>
                                <div className="text-sm font-medium mt-2">
                                    iterations={selectedReport.agent_loop.iterations} · replans={selectedReport.agent_loop.replans}
                                </div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    planned={selectedReport.agent_loop.steps_planned} · executed={selectedReport.agent_loop.steps_executed} · verified={selectedReport.agent_loop.steps_verified} · stop={selectedReport.agent_loop.stop_reason}
                                </div>
                            </article>
                            {(selectedReport.agent_loop.plan_steps ?? []).slice(0, 8).map((step) => (
                                <article key={step.id} className="rounded border border-border bg-background/50 p-3">
                                    <div className="flex items-start justify-between gap-2">
                                        <div className="text-sm font-medium">#{step.order} {step.title}</div>
                                        <div className="flex items-center gap-1 text-[11px]">
                                            <span className={`rounded border px-2 py-0.5 ${step.required ? 'border-rose-500/30 bg-rose-500/10 text-rose-200' : 'border-sky-500/30 bg-sky-500/10 text-sky-200'}`}>
                                                {step.required ? 'required' : 'optional'}
                                            </span>
                                            {step.superseded_by && (
                                                <span className="rounded border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-amber-200">
                                                    superseded
                                                </span>
                                            )}
                                        </div>
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                        {step.tool}
                                        {step.tool_version ? ` (${step.tool_version})` : ''}
                                        {' · '}
                                        {step.status}
                                        {' · '}
                                        verified={String(step.verified)}
                                    </div>
                                    {step.evidence_ids && step.evidence_ids.length > 0 && (
                                        <div className="text-xs text-muted-foreground mt-1">
                                            evidence: {step.evidence_ids.slice(0, 4).join(' · ')}
                                        </div>
                                    )}
                                    {step.verification_note && <div className="text-xs text-muted-foreground mt-1">{step.verification_note}</div>}
                                    {step.superseded_by && (
                                        <div className="text-xs text-muted-foreground mt-1">superseded by: {step.superseded_by}</div>
                                    )}
                                </article>
                            ))}
                        </div>
                    )}

                    <h3 className="font-semibold mb-3 flex items-center gap-2"><ListChecks className="w-4 h-4" /> Evidence & Recommendations</h3>
                    <div className="space-y-2 mb-4">
                        {isPrimingReport && <div className="text-sm text-muted-foreground">Generating evidence and recommendations...</div>}
                        {(selectedReport?.evidence ?? []).slice(0, 8).map((evidence) => (
                            <article key={evidence.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{evidence.kind}: {evidence.summary}</div>
                                {(evidence.metric_name || evidence.snippet) && (
                                    <div className="text-xs text-muted-foreground mt-1">{evidence.metric_name || evidence.snippet}</div>
                                )}
                            </article>
                        ))}
                    </div>
                    <div className="space-y-2">
                        {(selectedReport?.recommendations ?? []).slice(0, 6).map((rec) => (
                            <article key={rec.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{rec.summary}</div>
                                {rec.details && <div className="text-xs text-muted-foreground mt-1">{rec.details}</div>}
                                <div className="text-[11px] text-muted-foreground mt-1">
                                    category={rec.category || 'n/a'} · risk={rec.risk_level || rec.priority} · confidence={formatPercent((rec.confidence ?? 0) * 100, 0)}
                                </div>
                                <div className="text-[11px] text-muted-foreground mt-1">
                                    safe={String(rec.safe)} · dry_run_default={String(rec.dry_run_default)} · approval={String(rec.requires_approval)}
                                </div>
                                {rec.rationale && <div className="text-xs text-muted-foreground mt-1">why: {rec.rationale}</div>}
                                {rec.expected_impact && <div className="text-xs text-muted-foreground mt-1">impact: {rec.expected_impact}</div>}
                                {rec.approval_reason && <div className="text-xs text-muted-foreground mt-1">approval: {rec.approval_reason}</div>}
                                {rec.rollback_consideration && <div className="text-xs text-muted-foreground mt-1">rollback: {rec.rollback_consideration}</div>}
                            </article>
                        ))}
                    </div>
                    <h3 className="font-semibold mb-3">Proposed Actions</h3>
                    <div className="space-y-2">
                        {isPrimingReport && <div className="text-sm text-muted-foreground">Generating proposed actions...</div>}
                        {(selectedReport?.proposed_actions ?? []).slice(0, 6).map((action) => (
                            <article key={action.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{action.command_preview}</div>
                                <div className="text-xs text-muted-foreground mt-1">
                                    policy={action.policy?.status ?? 'n/a'} · risk={action.risk_level} · approval_required={String(action.approval_required)}
                                </div>
                                {action.rationale && <div className="text-xs text-muted-foreground mt-1">why: {action.rationale}</div>}
                                {action.expected_impact && <div className="text-xs text-muted-foreground mt-1">impact: {action.expected_impact}</div>}
                                {action.rollback_plan && <div className="text-xs text-muted-foreground mt-1">rollback: {action.rollback_plan}</div>}
                            </article>
                        ))}
                        {(selectedReport?.proposed_actions ?? []).length === 0 && (
                            <div className="text-sm text-muted-foreground">No proposed actions recorded.</div>
                        )}
                    </div>
                </section>
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Workflow & Tool Trace</h3>
                    <div className="text-xs text-muted-foreground mb-2">
                        {isPrimingReport
                            ? 'Generating workflow and tool trace...'
                            : (selectedReport?.stages ?? []).map((stage) => `${stage.name}:${stage.status}`).join(' → ') || 'No stages yet.'}
                    </div>
                    <div className="space-y-2">
                        {(selectedReport?.tool_calls ?? []).slice(0, 8).map((call) => (
                            <article key={call.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{call.tool} · {call.stage}</div>
                                <div className="text-xs text-muted-foreground">{call.status} · {call.summary || call.error_message || 'n/a'}</div>
                            </article>
                        ))}
                    </div>
                    <h4 className="text-sm font-semibold mt-4 mb-2">Hypothesis Updates</h4>
                    <div className="space-y-2">
                        {(traceQuery.data?.trace?.hypothesis_updates ?? []).slice(0, 6).map((update, index) => (
                            <article key={`${update.hypothesis_id}-${update.timestamp}-${index}`} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{update.hypothesis_id} · {update.action}</div>
                                <div className="text-xs text-muted-foreground">
                                    {formatPercent((update.old_confidence ?? 0) * 100, 0)} → {formatPercent((update.new_confidence ?? 0) * 100, 0)}
                                </div>
                                {update.reason && <div className="text-xs text-muted-foreground mt-1">{update.reason}</div>}
                            </article>
                        ))}
                        {(traceQuery.data?.trace?.hypothesis_updates ?? []).length === 0 && (
                            <div className="text-sm text-muted-foreground">No hypothesis updates recorded.</div>
                        )}
                    </div>
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Action Audit</h3>
                    {(isPrimingReport || auditQuery.isLoading) && <div className="text-sm text-muted-foreground">Loading audit records...</div>}
                    {!isPrimingReport && !auditQuery.isLoading && (auditQuery.data?.records ?? []).length === 0 && (
                        <div className="text-sm text-muted-foreground">No workflow audit records yet.</div>
                    )}
                    <div className="space-y-2">
                        {(auditQuery.data?.records ?? []).slice(0, 10).map((record) => (
                            <article key={record.id} className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{record.action} ({record.status})</div>
                                <div className="text-xs text-muted-foreground">
                                    {record.stage} · dry_run={String(record.dry_run)} · approval={String(record.requires_approval)}
                                </div>
                            </article>
                        ))}
                    </div>
                </section>
            </div>

            <RetrievalDecisionPanel
                title="Knowledge Retrieval Decisions"
                decisions={selectedReport?.context?.retrieval_decisions}
                emptyText={isPrimingReport
                    ? 'Generating retrieval decisions...'
                    : 'No retrieval decision was recorded for this RCA workflow.'}
            />

            <KnowledgeEvidencePanel
                title="RCA Knowledge Evidence"
                summary={selectedReport?.retrieval_summary || selectedReport?.context?.retrieval_summary}
                confidence={selectedReport?.retrieval_confidence}
                evidenceIDs={selectedReport?.retrieval_evidence_ids}
                docs={(selectedReport?.retrieved_docs ?? []).map((doc) => ({
                    id: doc.evidence_id,
                    title: doc.title,
                    source_path: doc.source_path,
                    source_type: doc.source_type,
                    knowledge_type: doc.knowledge_type,
                    case_type: doc.case_type,
                    summary: doc.summary,
                    snippet: doc.snippet,
                    score: doc.score,
                    likely_causes: doc.likely_causes,
                    remediation_steps: doc.remediation_steps,
                    commands: doc.commands,
                    signals: doc.signals,
                    tags: doc.tags,
                }))}
                emptyText={isPrimingReport
                    ? 'Generating RCA knowledge evidence...'
                    : 'No retrieved knowledge evidence was attached to this RCA report.'}
                testId="rca-knowledge-evidence"
            />
        </div>
    );
}

function SummaryCard({ label, value, detail }: { label: string; value: string; detail: string }) {
    return (
        <article className="rounded-lg border border-border bg-card p-3">
            <div className="text-xs text-muted-foreground">{label}</div>
            <div className="mt-2 text-lg font-semibold">{value}</div>
            <div className="text-xs text-muted-foreground">{detail}</div>
        </article>
    );
}

function formatValue(value: number): string {
    if (!Number.isFinite(value)) {
        return '—';
    }
    if (Math.abs(value) >= 1000) {
        return value.toLocaleString(undefined, { maximumFractionDigits: 1 });
    }
    return value.toFixed(2);
}
