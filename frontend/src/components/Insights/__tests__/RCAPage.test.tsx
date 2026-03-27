import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import RCAPage from '../RCAPage';
import { fetchFleetNodes } from '@/api/trends';
import {
    fetchAgentTrace,
    fetchJointRiskReports,
    fetchRCAWorkflowReports,
    fetchWorkflowAuditRecords,
} from '@/api/agentWorkflows';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
}));

vi.mock('@/api/agentWorkflows', () => ({
    fetchAgentTrace: vi.fn(),
    fetchJointRiskReports: vi.fn(),
    fetchRCAWorkflowReports: vi.fn(),
    fetchWorkflowAuditRecords: vi.fn(),
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchAgentTraceMock = vi.mocked(fetchAgentTrace);
const fetchRCAWorkflowReportsMock = vi.mocked(fetchRCAWorkflowReports);
const fetchJointRiskReportsMock = vi.mocked(fetchJointRiskReports);
const fetchWorkflowAuditRecordsMock = vi.mocked(fetchWorkflowAuditRecords);

describe('RCAPage', () => {
    beforeEach(() => {
        vi.clearAllMocks();

        fetchFleetNodesMock.mockResolvedValue({
            nodes: [
                {
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    updated_at: '2026-02-25T00:00:00Z',
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:00:00Z',
        });

        fetchRCAWorkflowReportsMock.mockResolvedValue({
            reports: [
                {
                    workflow_id: 'wf-rca-1',
                    pipeline_version: 'v0.7-workflow-pipeline',
                    incident_id: 'inc-wf-rca-1',
                    trace_id: 'wf-rca-1',
                    status: 'open',
                    collector_id: 'collector-a',
                    trigger: 'incident_alert',
                    generated_at: '2026-02-25T00:05:00Z',
                    synthesized_incident: {
                        incident_id: 'inc-wf-rca-1',
                        summary: 'HIGH; incident grouped from CPU usage, IO latency p99; candidate cluster: cpu scheduling contention',
                        grouped_signals: [
                            {
                                signal_id: 'cpu_pressure',
                                signal_type: 'CPU usage',
                                source: 'metrics',
                                scope: 'node',
                                entity: 'collector-a',
                                severity: 'high',
                                score: 0.82,
                                summary: 'CPU usage deviated 50.0% from baseline on collector-a',
                                evidence_ids: ['ev-cpu-1'],
                                last_observed: '2026-02-25T00:05:00Z',
                            },
                        ],
                        impacted_scope: ['node/collector-a', 'process/checkout-api'],
                        time_window: { start: '2026-02-24T23:20:00Z', end: '2026-02-25T00:05:00Z' },
                        severity: 'high',
                        confidence: 0.82,
                        candidate_root_cause_cluster: 'cpu scheduling contention',
                        correlation_reasons: ['CPU and IO move together'],
                        top_offenders: ['checkout-api'],
                        timeline_transitions: ['anomaly_detection=completed'],
                        topology_neighborhood: ['node/collector-a'],
                    },
                    context: {
                        collector_id: 'collector-a',
                        window: '45m0s',
                        incident_summary: 'HIGH incident',
                        impacted_scope: ['node/collector-a'],
                        top_metrics: {
                            node_cpu_usage_percent: 91,
                        },
                        gpu_summary: {},
                        top_processes: ['checkout-api'],
                        kernel_signals: ['CPU usage', 'IO latency p99'],
                        trace_summary: ['runtime spike'],
                        recent_deploys: ['deploy checkout-v2'],
                        security_findings: ['weak permissions indicators observed'],
                        topology_summary: 'kubernetes topology nodes=12 edges=30',
                    },
                    anomalies: ['CPU usage on collector-a (50.0% delta)'],
                    correlations: [
                        {
                            id: 'co-1',
                            scope: 'node',
                            entity: 'collector-a',
                            signals: ['CPU usage', 'IO latency p99'],
                            coefficient: 0.72,
                            summary: 'CPU and IO move together',
                        },
                    ],
                    hypotheses: [
                        {
                            id: 'h-cpu',
                            rank: 1,
                            title: 'cpu scheduling contention',
                            confidence: 0.82,
                            description: 'cpu pressure accelerated beyond baseline',
                            evidence_ids: ['ev-cpu-1'],
                        },
                    ],
                    evidence: [
                        {
                            id: 'ev-cpu-1',
                            kind: 'metric_delta',
                            source: 'metrics_query',
                            scope: 'node',
                            entity: 'collector-a',
                            summary: 'CPU moved from baseline 61 to 91',
                            metric_name: 'CPU usage',
                            value: 91,
                            baseline: 61,
                            delta: 49,
                            timestamp: '2026-02-25T00:05:00Z',
                        },
                    ],
                    recommendations: [
                        {
                            id: 'rca-check-1',
                            category: 'immediate_investigation',
                            priority: 'high',
                            summary: 'Validate hypothesis: cpu scheduling contention',
                            details: 'inspect run queue and throttling',
                            checks: ['inspect run queue'],
                            safe: true,
                            dry_run_default: true,
                            requires_approval: false,
                            rationale: 'top-ranked hypothesis needs explicit verification',
                            expected_impact: 'converts the current leading explanation into verified evidence',
                            risk_level: 'high',
                            confidence: 0.82,
                            reversible: true,
                            rollback_hint: 'read-only',
                        },
                    ],
                    proposed_actions: [
                        {
                            id: 'rca-check-1',
                            recommendation_id: 'rca-check-1',
                            category: 'immediate_investigation',
                            risk_reference: 'wf-rca-1',
                            command_preview: 'Validate hypothesis: cpu scheduling contention',
                            impact_scope: 'collector-a',
                            risk_level: 'high',
                            rationale: 'top-ranked hypothesis needs explicit verification',
                            expected_impact: 'converts the current leading explanation into verified evidence',
                            confidence: 0.82,
                            evidence_ids: ['ev-cpu-1'],
                            rollback_plan: 'read-only',
                            approval_required: false,
                            dry_run_plan: 'inspect run queue and throttling',
                            audit_intent: 'Validate hypothesis: cpu scheduling contention',
                            collector_id: 'collector-a',
                            workflow_id: 'wf-rca-1',
                            policy: {
                                status: 'allowed',
                                reason: 'read-only or low-impact action',
                                requires_approval: false,
                                dry_run_required: true,
                                rollback_required: false,
                            },
                            proposed_at: '2026-02-25T00:05:00Z',
                            status: 'proposed',
                        },
                    ],
                    agent_loop: {
                        mode: 'plan_act_verify',
                        iterations: 1,
                        replans: 0,
                        steps_planned: 4,
                        steps_executed: 4,
                        steps_verified: 1,
                        completed: false,
                        stop_reason: 'plan executed with unresolved verification gaps',
                        plan_steps: [
                            {
                                id: 'plan-metrics',
                                order: 1,
                                iteration: 1,
                                title: 'Collect metrics evidence',
                                objective: 'Validate pressure deltas',
                                tool: 'metrics_query',
                                required: true,
                                tool_version: 'v0.7.0',
                                status: 'completed',
                                result_summary: 'history samples loaded',
                                verified: true,
                                verification_note: 'metrics verified with 40 samples',
                                evidence_ids: ['ev-plan-metrics'],
                                started_at: '2026-02-25T00:05:00Z',
                                completed_at: '2026-02-25T00:05:00Z',
                            },
                            {
                                id: 'plan-knowledge',
                                order: 2,
                                iteration: 1,
                                title: 'Retrieve relevant runbooks and prior cases',
                                objective: 'Fetch auxiliary knowledge',
                                tool: 'runbook_retrieval',
                                required: false,
                                status: 'completed',
                                result_summary: 'runbook retrieval returned no matching evidence',
                                verified: false,
                                verification_note: 'runbook retrieval returned no matching evidence',
                                superseded_by: 'plan-knowledge-recheck',
                                evidence_ids: [],
                                started_at: '2026-02-25T00:05:00Z',
                                completed_at: '2026-02-25T00:05:01Z',
                            },
                        ],
                        plan_revisions: [],
                    },
                    structured_report: {
                        symptoms: ['CPU usage on collector-a (50.0% delta)'],
                        timeline: [
                            {
                                timestamp: '2026-02-25T00:05:00Z',
                                phase: 'anomaly_detection',
                                summary: 'signals detected',
                            },
                        ],
                        scope: ['node/collector-a'],
                        most_likely_cause: 'cpu scheduling contention',
                        supporting_signals: ['CPU usage (delta 50.0%)'],
                        disconfirming_signals: ['TCP retransmit ratio remained near baseline'],
                        confidence: 0.82,
                        unresolved_gaps: ['RAG retrieval did not return corroborating evidence'],
                        recommended_next_steps: ['Validate hypothesis: cpu scheduling contention'],
                        safe_remediations: [],
                    },
                    stages: [
                        {
                            name: 'anomaly_detection',
                            status: 'completed',
                            summary: 'signals detected',
                            started_at: '2026-02-25T00:05:00Z',
                            completed_at: '2026-02-25T00:05:00Z',
                        },
                    ],
                    tool_calls: [
                        {
                            id: 'tool-1',
                            tool: 'metrics_query',
                            stage: 'anomaly_detection',
                            status: 'success',
                            summary: 'history samples loaded',
                            started_at: '2026-02-25T00:05:00Z',
                            completed_at: '2026-02-25T00:05:00Z',
                        },
                    ],
                    reproducibility: {
                        pipeline: 'v0.7-workflow-pipeline',
                        deterministic: 'true',
                    },
                    unresolved_gaps: ['RAG retrieval did not return corroborating evidence'],
                    limitations: [],
                    insights: {
                        enabled: false,
                        provider: 'openai',
                        model: 'gpt-4o-mini',
                        api_key_env: 'SRE_AGENT_LLM_API_KEY',
                        api_key_configured: false,
                        mode: 'disabled',
                    },
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:05:00Z',
        });

        fetchJointRiskReportsMock.mockResolvedValue({
            reports: [
                {
                    workflow_id: 'wf-risk-1',
                    pipeline_version: 'v0.7-workflow-pipeline',
                    collector_id: 'collector-a',
                    scope: 'node',
                    window: '45m0s',
                    generated_at: '2026-02-25T00:05:00Z',
                    risk_score: 0.8,
                    risk_level: 'high',
                    summary: 'joint risk high',
                    actionable_why: 'co-occurred',
                    signals: [],
                    cooccurrences: [],
                    scope_risks: [],
                    series: [
                        {
                            key: 'cpu_pressure',
                            display: 'CPU usage',
                            unit: 'percent',
                            latest: 91,
                            baseline: 61,
                            acceleration: 1.8,
                            points: [
                                { timestamp: '2026-02-25T00:04:00Z', value: 84 },
                                { timestamp: '2026-02-25T00:05:00Z', value: 91 },
                            ],
                        },
                    ],
                    recommendations: [],
                    stages: [],
                    tool_calls: [],
                    insights: {
                        enabled: false,
                        provider: 'openai',
                        model: 'gpt-4o-mini',
                        api_key_env: 'SRE_AGENT_LLM_API_KEY',
                        api_key_configured: false,
                        mode: 'disabled',
                    },
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:05:00Z',
        });

        fetchWorkflowAuditRecordsMock.mockResolvedValue({
            records: [
                {
                    id: 'audit-1',
                    workflow_id: 'wf-rca-1',
                    workflow_type: 'rca',
                    stage: 'recommendation_generation',
                    action: 'recommendation.generated',
                    dry_run: true,
                    requires_approval: false,
                    approved: true,
                    status: 'success',
                    output_summary: 'Validate hypothesis: cpu scheduling contention',
                    timestamp: '2026-02-25T00:05:00Z',
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:05:00Z',
        });
        fetchAgentTraceMock.mockResolvedValue({
            trace: {
                trace_id: 'wf-rca-1',
                workflow_type: 'rca',
                collector_id: 'collector-a',
                started_at: '2026-02-25T00:05:00Z',
                completed_at: '2026-02-25T00:06:00Z',
                status: 'open',
                final_risk_score: 0.82,
                summary: 'cpu scheduling contention',
                hypothesis_updates: [
                    {
                        timestamp: '2026-02-25T00:05:30Z',
                        hypothesis_id: 'h-cpu',
                        action: 'confidence_increased',
                        old_confidence: 0.7,
                        new_confidence: 0.82,
                        reason: 'supported by metrics evidence',
                    },
                ],
            },
            timestamp: '2026-02-25T00:06:00Z',
        });
    });

    it('renders structured RCA drilldowns with evidence and audit records', async () => {
        renderWithClient(<RCAPage />);

        expect(await screen.findByText('RCA Workflow')).toBeInTheDocument();
        expect(await screen.findByText('RCA Time Series Context')).toBeInTheDocument();
        expect(await screen.findByText('Ranked Hypotheses')).toBeInTheDocument();
        expect(await screen.findByText('Evidence & Recommendations')).toBeInTheDocument();
        expect(await screen.findByText('Structured RCA Report')).toBeInTheDocument();
        expect(await screen.findByText(/Plan → Act → Verify Trace/)).toBeInTheDocument();
        expect(await screen.findByText('Workflow & Tool Trace')).toBeInTheDocument();
        expect(await screen.findByText('Action Audit')).toBeInTheDocument();
        expect((await screen.findAllByText(/cpu scheduling contention/i)).length).toBeGreaterThan(0);
        expect(await screen.findByText(/CPU moved from baseline 61 to 91/)).toBeInTheDocument();
        expect(await screen.findByText(/Verification gaps remain/)).toBeInTheDocument();
        expect(await screen.findByText(/completed=false/)).toBeInTheDocument();
        expect(await screen.findByText(/planned=4 · executed=4 · verified=1/)).toBeInTheDocument();
        expect(await screen.findByText(/Synthesized Incident/)).toBeInTheDocument();
        expect(await screen.findByText(/Proposed Actions/)).toBeInTheDocument();
        expect(await screen.findByText(/policy=allowed/i)).toBeInTheDocument();
        expect(await screen.findByText(/Hypothesis Updates/)).toBeInTheDocument();
        expect(await screen.findByText(/^required$/i)).toBeInTheDocument();
        expect(await screen.findByText(/^optional$/i)).toBeInTheDocument();
        expect(await screen.findByText(/superseded by: plan-knowledge-recheck/)).toBeInTheDocument();
        expect(await screen.findByText(/evidence: ev-plan-metrics/)).toBeInTheDocument();
    });
});
