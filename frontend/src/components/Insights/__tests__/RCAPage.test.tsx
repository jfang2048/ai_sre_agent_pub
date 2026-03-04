import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import RCAPage from '../RCAPage';
import { fetchFleetNodes } from '@/api/trends';
import {
    fetchJointRiskReports,
    fetchRCAWorkflowReports,
    fetchWorkflowAuditRecords,
} from '@/api/agentWorkflows';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
}));

vi.mock('@/api/agentWorkflows', () => ({
    fetchJointRiskReports: vi.fn(),
    fetchRCAWorkflowReports: vi.fn(),
    fetchWorkflowAuditRecords: vi.fn(),
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchRCAWorkflowReportsMock = vi.mocked(fetchRCAWorkflowReports);
const fetchJointRiskReportsMock = vi.mocked(fetchJointRiskReports);
const fetchWorkflowAuditRecordsMock = vi.mocked(fetchWorkflowAuditRecords);

function renderWithClient(ui: React.ReactElement) {
    const client = new QueryClient({
        defaultOptions: {
            queries: {
                retry: false,
            },
        },
        logger: {
            log: console.log,
            warn: console.warn,
            error: () => {},
        },
    });
    return render(
        <div style={{ width: 1600, height: 1000 }}>
            <QueryClientProvider client={client}>{ui}</QueryClientProvider>
        </div>,
    );
}

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
                    pipeline_version: 'v0.5-workflow-pipeline',
                    incident_id: 'inc-wf-rca-1',
                    status: 'open',
                    collector_id: 'collector-a',
                    trigger: 'incident_alert',
                    generated_at: '2026-02-25T00:05:00Z',
                    context: {
                        collector_id: 'collector-a',
                        window: '45m0s',
                        top_metrics: {
                            node_cpu_usage_percent: 91,
                        },
                        top_processes: ['checkout-api'],
                        kernel_signals: ['CPU usage', 'IO latency p99'],
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
                            priority: 'high',
                            summary: 'Validate hypothesis: cpu scheduling contention',
                            details: 'inspect run queue and throttling',
                            checks: ['inspect run queue'],
                            safe: true,
                            dry_run_default: true,
                            requires_approval: false,
                            reversible: true,
                            rollback_hint: 'read-only',
                        },
                    ],
                    agent_loop: {
                        mode: 'plan_act_verify',
                        iterations: 1,
                        replans: 0,
                        steps_planned: 4,
                        steps_executed: 4,
                        steps_verified: 4,
                        completed: true,
                        stop_reason: 'all planned steps executed',
                        plan_steps: [
                            {
                                id: 'plan-metrics',
                                order: 1,
                                iteration: 1,
                                title: 'Collect metrics evidence',
                                objective: 'Validate pressure deltas',
                                tool: 'metrics_query',
                                tool_version: 'v0.5.0',
                                status: 'completed',
                                result_summary: 'history samples loaded',
                                verified: true,
                                verification_note: 'metrics verified with 40 samples',
                                evidence_ids: ['ev-plan-metrics'],
                                started_at: '2026-02-25T00:05:00Z',
                                completed_at: '2026-02-25T00:05:00Z',
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
                        pipeline: 'v0.5-workflow-pipeline',
                        deterministic: 'true',
                    },
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
                    pipeline_version: 'v0.5-workflow-pipeline',
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
    });
});
