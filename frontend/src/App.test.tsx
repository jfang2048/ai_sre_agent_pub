import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App.tsx';

vi.mock('@/store/dashboardStore', () => {
    let theme: 'dark' | 'light' = 'dark';
    return {
        __esModule: true,
        useDashboardStore: () => ({
            theme,
            setTheme: (nextTheme: 'dark' | 'light') => {
                theme = nextTheme;
            },
        }),
    };
});

vi.mock('@/components/Dashboard/Grid', () => ({
    __esModule: true,
    default: () => <div data-testid="dashboard-grid">dashboard-grid</div>,
}));

vi.mock('@/components/Search/NLQuery', () => ({
    __esModule: true,
    default: () => <div data-testid="nl-query">nl-query</div>,
}));

vi.mock('@/Agent', () => ({
    __esModule: true,
    default: () => <div data-testid="agent-tab">agent-tab</div>,
}));

vi.mock('@/components/Visualizations/DataPathDiagnosticsPage', () => ({
    __esModule: true,
    default: ({
        onOpenTrends,
    }: {
        onOpenTrends?: (intent: {
            metricKey: string;
            category: 'network';
            collectorId: string;
            windowSize: string;
            processFilter: string;
            triggerLabel: string;
        }) => void;
    }) => (
        <div>
            <div data-testid="diagnostics-page">diagnostics-page</div>
            <button
                type="button"
                onClick={() =>
                    onOpenTrends?.({
                        metricKey: 'tcp_retransmit_ratio',
                        category: 'network',
                        collectorId: 'collector-a',
                        windowSize: '30m',
                        processFilter: 'trainer',
                        triggerLabel: 'Network hotspot',
                    })
                }
            >
                open-trends
            </button>
            <button
                type="button"
                onClick={() =>
                    onOpenTrends?.({
                        metricKey: 'gpu_memory_used_total_mib',
                        category: 'network',
                        collectorId: 'collector-b',
                        windowSize: '15m',
                        processFilter: 'serving',
                        triggerLabel: 'Second navigation',
                    })
                }
            >
                open-trends-2
            </button>
        </div>
    ),
}));

vi.mock('@/components/Visualizations/MetricTrendsPage', () => ({
    __esModule: true,
    default: ({
        navigationIntent,
        onNavigationIntentConsumed,
    }: {
        navigationIntent?: Record<string, unknown> | null;
        onNavigationIntentConsumed?: () => void;
    }) => (
        <div>
            <div data-testid="trends-page">metric-trends-page</div>
            <div data-testid="trends-intent">
                {navigationIntent ? JSON.stringify(navigationIntent) : 'none'}
            </div>
            <button type="button" onClick={() => onNavigationIntentConsumed?.()}>
                consume-intent
            </button>
        </div>
    ),
}));

describe('App diagnostics-to-trends data flow', () => {
    beforeEach(() => {
        localStorage.clear();
    });

    it('passes trends navigation intent from diagnostics and clears it after consumption', async () => {
        const user = userEvent.setup();
        render(<App />);

        expect(screen.getByTestId('dashboard-grid')).toBeInTheDocument();

        await act(async () => {
            await user.click(screen.getByTitle('Data Path Diagnostics'));
        });
        expect(screen.getByTestId('diagnostics-page')).toBeInTheDocument();

        await act(async () => {
            await user.click(screen.getByRole('button', { name: 'open-trends' }));
        });

        await waitFor(() => {
            expect(screen.getByText('Metric Trends')).toBeInTheDocument();
        });
        expect(screen.getByTestId('trends-page')).toBeInTheDocument();

        const intentPayload = screen.getByTestId('trends-intent').textContent ?? '';
        const intent = JSON.parse(intentPayload) as {
            metricKey: string;
            collectorId: string;
            category: string;
            windowSize: string;
            processFilter: string;
            triggerLabel: string;
            requestToken: number;
        };

        expect(intent.metricKey).toBe('tcp_retransmit_ratio');
        expect(intent.collectorId).toBe('collector-a');
        expect(intent.category).toBe('network');
        expect(intent.windowSize).toBe('30m');
        expect(intent.processFilter).toBe('trainer');
        expect(intent.triggerLabel).toBe('Network hotspot');
        expect(intent.requestToken).toBeGreaterThan(0);

        await act(async () => {
            await user.click(screen.getByRole('button', { name: 'consume-intent' }));
        });
        await waitFor(() => {
            expect(screen.getByTestId('trends-intent')).toHaveTextContent('none');
        });
    });

    it('updates navigation intent and request token on repeated diagnostics actions', async () => {
        const user = userEvent.setup();
        render(<App />);

        await act(async () => {
            await user.click(screen.getByTitle('Data Path Diagnostics'));
        });
        expect(screen.getByTestId('diagnostics-page')).toBeInTheDocument();

        await act(async () => {
            await user.click(screen.getByRole('button', { name: 'open-trends' }));
        });
        await waitFor(() => {
            expect(screen.getByTestId('trends-page')).toBeInTheDocument();
        });

        const firstIntent = JSON.parse(screen.getByTestId('trends-intent').textContent ?? '{}') as {
            metricKey: string;
            collectorId: string;
            requestToken: number;
        };
        expect(firstIntent.metricKey).toBe('tcp_retransmit_ratio');
        expect(firstIntent.collectorId).toBe('collector-a');

        await act(async () => {
            await user.click(screen.getByTitle('Data Path Diagnostics'));
        });
        expect(screen.getByTestId('diagnostics-page')).toBeInTheDocument();

        await act(async () => {
            await user.click(screen.getByRole('button', { name: 'open-trends-2' }));
        });
        await waitFor(() => {
            expect(screen.getByTestId('trends-page')).toBeInTheDocument();
        });

        const secondIntent = JSON.parse(screen.getByTestId('trends-intent').textContent ?? '{}') as {
            metricKey: string;
            collectorId: string;
            processFilter: string;
            requestToken: number;
        };
        expect(secondIntent.metricKey).toBe('gpu_memory_used_total_mib');
        expect(secondIntent.collectorId).toBe('collector-b');
        expect(secondIntent.processFilter).toBe('serving');
        expect(secondIntent.requestToken).toBeGreaterThan(firstIntent.requestToken);
    });
});
