import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import AIInsightsPanel from '../AIInsights';
import { fetchAgentStatus } from '@/api/controlPlane';
import {
    fetchProposedActions,
    fetchRCAWorkflowReports,
    fetchWorkflowIncidents,
} from '@/api/agentWorkflows';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/agentWorkflows', () => ({
    fetchProposedActions: vi.fn(),
    fetchRCAWorkflowReports: vi.fn(),
    fetchWorkflowIncidents: vi.fn(),
}));

vi.mock('@/api/controlPlane', () => ({
    fetchAgentStatus: vi.fn(),
}));

const fetchRCAWorkflowReportsMock = vi.mocked(fetchRCAWorkflowReports);
const fetchWorkflowIncidentsMock = vi.mocked(fetchWorkflowIncidents);
const fetchProposedActionsMock = vi.mocked(fetchProposedActions);
const fetchAgentStatusMock = vi.mocked(fetchAgentStatus);

describe('AIInsightsPanel', () => {
    beforeEach(() => {
        vi.clearAllMocks();

        fetchAgentStatusMock.mockResolvedValue({
            status: 'active',
            reports: 1,
            actions: 1,
            joint_risk_reports: 1,
            potential_risk_findings: 1,
            rca_workflow_reports: 1,
            timestamp: '2026-03-08T12:00:00Z',
            control_plane: {
                enabled: true,
                joint_risk_reports: 1,
                rca_reports: 1,
                incidents: 1,
                high_risk_reports: 1,
                latest_collector_id: 'collector-a',
                latest_joint_risk_at: '2026-03-08T11:58:00Z',
                latest_rca_at: '2026-03-08T12:00:00Z',
                latest_risk_level: 'high',
                triggered_trends: 2,
                investigation_events: 1,
                weak_signal_clusters: 1,
                retrieval_decisions: 2,
                retrieval_skipped: 1,
                recommendation_count: 3,
                top_event_title: 'Memory growth and disk wait are rising together',
                probable_cause: 'memory reclaim and io contention are amplifying each other',
                latest_incident_summary: 'checkout latency incident',
                top_recommendation: 'Validate the correlated disk and memory trend before containment',
                top_retrieval_intent: 'runbook',
                top_retrieval_query: 'memory pressure disk latency queue depth',
                top_retrieval_skip_reason: '',
            },
            query_service: {
                enabled: true,
                analysis_mode: 'deterministic_only',
                provider: 'stub',
                model: 'stub',
                dry_run: true,
                require_approval_token: true,
                rag_attached: false,
                skip_llm_on_stale_telemetry: true,
                skip_llm_on_no_telemetry: true,
                max_telemetry_age: '2m0s',
                metrics: {
                    StaleTelemetryTotal: 0,
                    LLMFailuresTotal: 0,
                    LLMBypassedStaleTotal: 0,
                    LLMBypassedEmptyTotal: 0,
                    FallbackTotal: 3,
                    ActionsSuppressedTotal: 0,
                    AnalysisReusedTotal: 1,
                    RAGSkippedContextTotal: 2,
                },
            },
        });

        fetchRCAWorkflowReportsMock.mockResolvedValue({
            reports: [
                {
                    workflow_id: 'wf-1',
                    pipeline_version: 'v0.7-workflow-pipeline',
                    incident_id: 'inc-1',
                    trace_id: 'trace-1',
                    status: 'open',
                    collector_id: 'collector-a',
                    trigger: 'incident_alert',
                    generated_at: '2026-03-08T12:00:00Z',
                    synthesized_incident: {
                        incident_id: 'inc-1',
                        summary: 'checkout latency and retry storm grouped into one incident',
                        grouped_signals: [],
                        impacted_scope: ['service/checkout'],
                        time_window: { start: '2026-03-08T11:30:00Z', end: '2026-03-08T12:00:00Z' },
                        severity: 'high',
                        confidence: 0.84,
                    },
                    context: {
                        window: '30m',
                        top_metrics: {},
                        top_processes: [],
                        kernel_signals: [],
                    },
                    anomalies: [],
                    correlations: [],
                    hypotheses: [
                        {
                            id: 'h-1',
                            rank: 1,
                            title: 'downstream checkout dependency saturation',
                            confidence: 0.84,
                            description: 'retry amplification under downstream latency',
                            evidence_ids: ['ev-1'],
                        },
                    ],
                    evidence: [],
                    recommendations: [
                        {
                            id: 'rec-1',
                            category: 'immediate_investigation',
                            priority: 'high',
                            summary: 'Inspect downstream error burst logs',
                            safe: true,
                            dry_run_default: true,
                            requires_approval: false,
                            reversible: true,
                        },
                    ],
                    proposed_actions: [],
                    agent_loop: {
                        mode: 'plan_act_verify',
                        iterations: 1,
                        replans: 0,
                        steps_planned: 3,
                        steps_executed: 3,
                        steps_verified: 2,
                        completed: true,
                        stop_reason: 'required evidence verified',
                        plan_steps: [],
                        plan_revisions: [],
                    },
                    structured_report: {
                        incident_summary: 'checkout latency incident',
                        symptoms: [],
                        timeline: [],
                        scope: ['service/checkout'],
                        most_likely_cause: 'downstream checkout dependency saturation',
                        supporting_signals: [],
                        disconfirming_signals: [],
                        confidence: 0.84,
                    },
                    stages: [],
                    tool_calls: [],
                    reproducibility: {},
                    insights: {
                        enabled: true,
                        provider: 'stub',
                        model: 'stub',
                        api_key_env: 'OPENAI_API_KEY',
                        api_key_configured: false,
                        mode: 'deterministic',
                    },
                },
            ],
            count: 1,
            timestamp: '2026-03-08T12:00:00Z',
        });

        fetchWorkflowIncidentsMock.mockResolvedValue({
            incidents: [
                {
                    incident_id: 'inc-1',
                    workflow_id: 'wf-1',
                    trace_id: 'trace-1',
                    status: 'investigating',
                    source: 'agent_workflow',
                    collector_id: 'collector-a',
                    opened_at: '2026-03-08T11:30:00Z',
                    risk_level: 'high',
                    risk_score: 82,
                    summary: 'checkout service degraded',
                    most_likely_cause: 'downstream checkout dependency saturation',
                    confidence: 0.84,
                    synthesized_incident: {
                        incident_id: 'inc-1',
                        summary: 'checkout latency and retry storm grouped into one incident',
                        grouped_signals: [],
                        impacted_scope: ['service/checkout'],
                        time_window: { start: '2026-03-08T11:30:00Z', end: '2026-03-08T12:00:00Z' },
                        severity: 'high',
                        confidence: 0.84,
                    },
                    symptoms: ['latency increase'],
                    timeline: [],
                    evidence: [],
                    hypotheses: [
                        {
                            id: 'h-1',
                            rank: 1,
                            title: 'downstream checkout dependency saturation',
                            confidence: 0.84,
                            description: 'retry amplification under downstream latency',
                            evidence_ids: ['ev-1'],
                        },
                    ],
                    recommendations: [],
                    agent_loop: {
                        mode: 'plan_act_verify',
                        iterations: 1,
                        replans: 0,
                        steps_planned: 3,
                        steps_executed: 3,
                        steps_verified: 2,
                        completed: true,
                        stop_reason: 'required evidence verified',
                        plan_steps: [],
                        plan_revisions: [],
                    },
                    unresolved_gaps: ['need packet capture if latency persists'],
                },
            ],
            count: 1,
            timestamp: '2026-03-08T12:00:00Z',
        });

        fetchProposedActionsMock.mockResolvedValue({
            actions: [
                {
                    id: 'act-1',
                    recommendation_id: 'rec-1',
                    category: 'probable_containment',
                    risk_reference: 'wf-1',
                    command_preview: 'Pause checkout rollout progression',
                    impact_scope: 'service/checkout',
                    risk_level: 'high',
                    expected_impact: 'reduces retry amplification and blast radius',
                    rollback_plan: 'resume rollout after dependency health recovers',
                    approval_required: true,
                    audit_intent: 'pause rollout',
                    collector_id: 'collector-a',
                    workflow_id: 'wf-1',
                    policy: {
                        status: 'allowed_with_approval',
                        reason: 'blast-radius reduction needs approval',
                        requires_approval: true,
                        dry_run_required: true,
                        rollback_required: true,
                    },
                    proposed_at: '2026-03-08T12:00:00Z',
                    status: 'proposed',
                },
            ],
            count: 1,
            timestamp: '2026-03-08T12:00:00Z',
        });
    });

    it('renders workflow-backed RCA, incidents, and proposed actions', async () => {
        renderWithClient(<AIInsightsPanel />);

        expect(await screen.findByText('Control-plane focus')).toBeInTheDocument();
        expect(await screen.findByText('Trend watch')).toBeInTheDocument();
        expect(await screen.findByText('Weak-signal fusion')).toBeInTheDocument();
        expect(await screen.findByText('Retrieval planning')).toBeInTheDocument();
        expect(await screen.findByText('Lead issue')).toBeInTheDocument();
        expect(await screen.findByText(/2 decisions · 1 skipped/)).toBeInTheDocument();
        expect((await screen.findAllByText(/checkout latency incident/)).length).toBeGreaterThan(0);
        expect(await screen.findByText('Latest RCA decisions')).toBeInTheDocument();
        expect(await screen.findByText(/Hypothesis: downstream checkout dependency saturation/)).toBeInTheDocument();
        expect(await screen.findByText('Workflow incidents')).toBeInTheDocument();
        expect(await screen.findByText('checkout service degraded')).toBeInTheDocument();
        expect(await screen.findByText(/Gap: need packet capture if latency persists/)).toBeInTheDocument();
        expect(await screen.findByText('Guarded proposed actions')).toBeInTheDocument();
        expect(await screen.findByText('Pause checkout rollout progression')).toBeInTheDocument();
        expect(await screen.findByText('allowed_with_approval')).toBeInTheDocument();
    });
});
