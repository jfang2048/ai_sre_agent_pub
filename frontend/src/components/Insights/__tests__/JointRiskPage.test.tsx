import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import JointRiskPage from '../JointRiskPage';
import { fetchFleetNodes } from '@/api/trends';
import { fetchJointRiskReports } from '@/api/agentWorkflows';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
}));

vi.mock('@/api/agentWorkflows', () => ({
    fetchJointRiskReports: vi.fn(),
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchJointRiskReportsMock = vi.mocked(fetchJointRiskReports);

describe('JointRiskPage', () => {
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

        fetchJointRiskReportsMock.mockResolvedValue({
            reports: [
                {
                    workflow_id: 'wf-risk-1',
                    pipeline_version: 'v0.6-workflow-pipeline',
                    collector_id: 'collector-a',
                    scope: 'node',
                    window: '45m0s',
                    generated_at: '2026-02-25T00:05:00Z',
                    risk_score: 0.82,
                    risk_level: 'high',
                    summary: 'joint risk high across 4 active signals',
                    actionable_why: 'combined risk is high because signals CPU+IO co-occurred within window 45m on scope node/collector-a',
                    signals: [
                        {
                            id: 'cpu_pressure',
                            name: 'CPU usage',
                            scope: 'node',
                            entity: 'collector-a',
                            severity: 'high',
                            weight: 0.16,
                            current: 92,
                            baseline: 61,
                            delta_percent: 50,
                            acceleration: 2.1,
                            score: 0.15,
                            triggered: true,
                            evidence: ['latest=92 baseline=61'],
                            last_observed_at: '2026-02-25T00:05:00Z',
                        },
                    ],
                    cooccurrences: [
                        {
                            id: 'co-1',
                            scope: 'node',
                            entity: 'collector-a',
                            window: '45m0s',
                            signals: ['CPU usage', 'IO latency p99'],
                            correlation: 0.72,
                            combined_score: 0.65,
                            explanation: 'CPU and IO move together',
                            actionable_cause: 'combined risk is high because signals CPU usage+IO latency p99 co-occurred within window 45m0s on scope node/collector-a',
                        },
                    ],
                    scope_risks: [
                        {
                            scope: 'node',
                            entity: 'collector-a',
                            score: 0.82,
                            top_signals: ['CPU usage', 'IO latency p99'],
                            explanation: 'node-level weighted risk',
                        },
                    ],
                    series: [
                        {
                            key: 'cpu_pressure',
                            display: 'CPU usage',
                            unit: 'percent',
                            latest: 92,
                            baseline: 61,
                            acceleration: 2.1,
                            points: [
                                { timestamp: '2026-02-25T00:04:00Z', value: 85 },
                                { timestamp: '2026-02-25T00:05:00Z', value: 92 },
                            ],
                        },
                    ],
                    recommendations: [
                        {
                            id: 'risk-check-1',
                            priority: 'high',
                            summary: 'Inspect CPU pressure on collector-a',
                            details: 'latest=92 baseline=61',
                            checks: ['validate baseline'],
                            safe: true,
                            dry_run_default: true,
                            requires_approval: false,
                            reversible: true,
                            rollback_hint: 'read-only check',
                        },
                    ],
                    stages: [
                        {
                            name: 'collect_signals',
                            status: 'completed',
                            summary: 'tools=4 signals=4',
                            started_at: '2026-02-25T00:05:00Z',
                            completed_at: '2026-02-25T00:05:00Z',
                        },
                    ],
                    tool_calls: [
                        {
                            id: 'tool-1',
                            tool: 'metrics_query',
                            stage: 'collect_signals',
                            status: 'success',
                            summary: 'collector=collector-a history_samples=40',
                            started_at: '2026-02-25T00:05:00Z',
                            completed_at: '2026-02-25T00:05:00Z',
                        },
                    ],
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
    });

    it('renders time-series and ranked risk drilldowns', async () => {
        renderWithClient(<JointRiskPage />);

        expect(await screen.findByText('Joint Risk')).toBeInTheDocument();
        expect(await screen.findByText('Joint Risk Time Series')).toBeInTheDocument();
        expect(await screen.findByText('Ranked Signals')).toBeInTheDocument();
        expect(await screen.findByText('Correlation Drilldowns')).toBeInTheDocument();
        expect(await screen.findByText('Scope Breakdown')).toBeInTheDocument();
        expect(await screen.findByText('Recommendations')).toBeInTheDocument();
        expect((await screen.findAllByText(/CPU usage/)).length).toBeGreaterThan(0);
        expect(await screen.findByText(/co-occurred within window/)).toBeInTheDocument();
    });

    it('updates query inputs via collector/window selectors', async () => {
        const user = userEvent.setup();
        renderWithClient(<JointRiskPage />);

        await screen.findByText(/co-occurred within window/);
        const collectorSelect = screen.getAllByRole('combobox')[0];
        await act(async () => {
            await user.selectOptions(collectorSelect, 'collector-a');
        });

        const windowSelect = screen.getAllByRole('combobox')[1];
        await act(async () => {
            await user.selectOptions(windowSelect, '1h');
        });

        await waitFor(() => {
            expect(fetchJointRiskReportsMock).toHaveBeenLastCalledWith(
                expect.objectContaining({
                    collectorId: 'collector-a',
                    window: '1h',
                }),
            );
        });
    });
});
