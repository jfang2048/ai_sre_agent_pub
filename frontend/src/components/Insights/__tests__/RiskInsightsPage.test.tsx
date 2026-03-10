import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import RiskInsightsPage from '../RiskInsightsPage';
import { fetchFleetNodes } from '@/api/trends';
import { fetchPotentialRiskFindings } from '@/api/agentWorkflows';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
}));

vi.mock('@/api/agentWorkflows', async (importOriginal) => ({
    ...(await importOriginal<typeof import('@/api/agentWorkflows')>()),
    fetchPotentialRiskFindings: vi.fn(),
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchPotentialRiskFindingsMock = vi.mocked(fetchPotentialRiskFindings);

describe('RiskInsightsPage', () => {
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

        const findingsResponse = {
            findings: [
                {
                    id: 'risk-1',
                    collector_id: 'collector-a',
                    risk_summary: 'joint risk high | lead signal CPU usage on node/collector-a current 92 baseline 61 delta 50%',
                    time_window: '45m0s',
                    scope: 'node/collector-a',
                    confidence_score: 0.86,
                    contributing_signals: [
                        {
                            name: 'CPU usage',
                            scope: 'node',
                            entity: 'collector-a',
                            severity: 'high',
                            current: 92,
                            baseline: 61,
                            delta_percent: 50,
                            score: 0.15,
                            evidence: ['latest=92 baseline=61', 'delta=50%'],
                        },
                    ],
                    suggested_investigation_steps: [
                        'validate CPU usage on node/collector-a: current 92 vs baseline 61',
                    ],
                    correlations: [
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
                    generated_at: '2026-02-25T00:05:00Z',
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:05:00Z',
        };

        fetchPotentialRiskFindingsMock.mockImplementation(async (query) => {
            if (query?.refresh === false) {
                return findingsResponse;
            }
            return findingsResponse;
        });
    });

    it('renders ranked risks, evidence, trends, and correlation details', async () => {
        renderWithClient(<RiskInsightsPage />);

        expect(await screen.findByText('Risk Insights')).toBeInTheDocument();
        expect(await screen.findByText('Ranked Potential Risks')).toBeInTheDocument();
        expect(await screen.findByText('Evidence Breakdown')).toBeInTheDocument();
        expect(await screen.findByText('Trend Graph')).toBeInTheDocument();
        expect(await screen.findByText('Correlation Details')).toBeInTheDocument();
        expect(await screen.findByText('Suggested Investigation Steps')).toBeInTheDocument();
        expect((await screen.findAllByText(/CPU usage/)).length).toBeGreaterThan(0);
    });

    it('updates query with collector and window selectors', async () => {
        const user = userEvent.setup();
        renderWithClient(<RiskInsightsPage />);

        await screen.findByText(/validate CPU usage on node\/collector-a/i);
        const collectorSelect = screen.getAllByRole('combobox')[0];
        await act(async () => {
            await user.selectOptions(collectorSelect, 'collector-a');
        });

        const windowSelect = screen.getAllByRole('combobox')[1];
        await act(async () => {
            await user.selectOptions(windowSelect, '1h');
        });

        await waitFor(() => {
            expect(fetchPotentialRiskFindingsMock).toHaveBeenLastCalledWith(
                expect.objectContaining({
                    collectorId: 'collector-a',
                    window: '1h',
                }),
            );
        });
    });

    it('shows a loading state instead of an empty-state placeholder while fresh findings are still generating', async () => {
        let resolveLiveFindings: ((value: Awaited<ReturnType<typeof fetchPotentialRiskFindings>>) => void) | undefined;

        fetchPotentialRiskFindingsMock.mockImplementation((query) => {
            if (query?.refresh === false) {
                return Promise.resolve({
                    findings: [],
                    count: 0,
                    timestamp: '2026-02-25T00:04:00Z',
                });
            }
            return new Promise((resolve) => {
                resolveLiveFindings = resolve;
            });
        });

        renderWithClient(<RiskInsightsPage />);

        expect((await screen.findAllByText('Generating latest risk findings...')).length).toBeGreaterThan(0);
        expect(screen.queryByText('No potential risks generated yet.')).not.toBeInTheDocument();

        resolveLiveFindings?.({
            findings: [
                {
                    id: 'risk-live-1',
                    collector_id: 'collector-a',
                    risk_summary: 'joint risk medium | lead signal CPU usage on node/collector-a current 88 baseline 54 delta 63%',
                    time_window: '45m0s',
                    scope: 'node/collector-a',
                    confidence_score: 0.74,
                    contributing_signals: [
                        {
                            name: 'CPU usage',
                            scope: 'node',
                            entity: 'collector-a',
                            severity: 'high',
                            current: 88,
                            baseline: 54,
                            delta_percent: 63,
                            score: 0.12,
                            evidence: ['latest=88 baseline=54'],
                        },
                    ],
                    suggested_investigation_steps: ['validate CPU usage on node/collector-a'],
                    correlations: [],
                    series: [
                        {
                            key: 'cpu_pressure',
                            display: 'CPU usage',
                            unit: 'percent',
                            latest: 88,
                            baseline: 54,
                            acceleration: 1.9,
                            points: [
                                { timestamp: '2026-02-25T00:04:00Z', value: 79 },
                                { timestamp: '2026-02-25T00:05:00Z', value: 88 },
                            ],
                        },
                    ],
                    generated_at: '2026-02-25T00:05:00Z',
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:05:00Z',
        });

        expect(await screen.findByText(/joint risk medium/i)).toBeInTheDocument();
    });
});
