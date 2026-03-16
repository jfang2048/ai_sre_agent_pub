import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App.tsx';

async function click(user: ReturnType<typeof userEvent.setup>, target: HTMLElement) {
    await act(async () => {
        await user.click(target);
    });
}

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

vi.mock('@/components/Insights/IncidentAnalysisPage', () => ({
    __esModule: true,
    default: ({
        onOpenTrends,
    }: {
        onOpenTrends?: (intent: {
            metricKey: string;
            category: 'network';
            collectorId: string;
            windowSize: string;
            triggerLabel: string;
        }) => void;
    }) => (
        <div>
            <div data-testid="incident-analysis-page">incident-analysis-page</div>
            <button
                type="button"
                onClick={() =>
                    onOpenTrends?.({
                        metricKey: 'network_rx_bytes_per_second',
                        category: 'network',
                        collectorId: 'collector-c',
                        windowSize: '1h',
                        triggerLabel: 'Incident drilldown',
                    })
                }
            >
                open-trends-from-incident
            </button>
        </div>
    ),
}));

vi.mock('@/components/Insights/JointRiskPage', () => ({
    __esModule: true,
    default: () => <div data-testid="joint-risk-page">joint-risk-page</div>,
}));

vi.mock('@/components/Insights/KnowledgePage', () => ({
    __esModule: true,
    default: () => <div data-testid="knowledge-page">knowledge-page</div>,
}));

vi.mock('@/components/Insights/RiskInsightsPage', () => ({
    __esModule: true,
    default: () => <div data-testid="risk-insights-page">risk-insights-page</div>,
}));

vi.mock('@/components/Insights/RCAPage', () => ({
    __esModule: true,
    default: () => <div data-testid="rca-page">rca-page</div>,
}));

vi.mock('@/components/Insights/SecurityDashboardPage', () => ({
    __esModule: true,
    default: () => <div data-testid="security-dashboard-page">security-dashboard-page</div>,
}));

vi.mock('@/components/Insights/IncidentsPage', () => ({
    __esModule: true,
    default: () => <div data-testid="incidents-page">incidents-page</div>,
}));

vi.mock('@/components/Insights/AuditLogPage', () => ({
    __esModule: true,
    default: () => <div data-testid="audit-log-page">audit-log-page</div>,
}));

vi.mock('@/components/Insights/LogsPage', () => ({
    __esModule: true,
    default: () => <div data-testid="logs-page">logs-page</div>,
}));

vi.mock('@/components/Visualizations/GPUObservabilityPage', () => ({
    __esModule: true,
    default: () => <div data-testid="gpu-observability-page">gpu-observability-page</div>,
}));

describe('App diagnostics-to-trends data flow', () => {
    beforeEach(() => {
        localStorage.clear();
        window.history.replaceState({}, '', '/');
    });

    it('passes trends navigation intent from diagnostics and clears it after consumption', async () => {
        const user = userEvent.setup();
        render(<App />);

        expect(screen.getByTestId('dashboard-grid')).toBeInTheDocument();

        await click(user, screen.getByTitle('Data Path Diagnostics'));
        expect(screen.getByTestId('diagnostics-page')).toBeInTheDocument();

        await click(user, screen.getByRole('button', { name: 'open-trends' }));

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

        await click(user, screen.getByRole('button', { name: 'consume-intent' }));
        await waitFor(() => {
            expect(screen.getByTestId('trends-intent')).toHaveTextContent('none');
        });
    });

    it('updates navigation intent and request token on repeated diagnostics actions', async () => {
        const user = userEvent.setup();
        render(<App />);

        await click(user, screen.getByTitle('Data Path Diagnostics'));
        expect(screen.getByTestId('diagnostics-page')).toBeInTheDocument();

        await click(user, screen.getByRole('button', { name: 'open-trends' }));
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

        await click(user, screen.getByTitle('Data Path Diagnostics'));
        expect(screen.getByTestId('diagnostics-page')).toBeInTheDocument();

        await click(user, screen.getByRole('button', { name: 'open-trends-2' }));
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

    it('opens trends from incident analysis page drilldown', async () => {
        const user = userEvent.setup();
        render(<App />);

        await click(user, screen.getByTitle('Incident Analysis'));
        expect(screen.getByTestId('incident-analysis-page')).toBeInTheDocument();

        await click(user, screen.getByRole('button', { name: 'open-trends-from-incident' }));
        await waitFor(() => {
            expect(screen.getByTestId('trends-page')).toBeInTheDocument();
        });

        const intent = JSON.parse(screen.getByTestId('trends-intent').textContent ?? '{}') as {
            metricKey: string;
            collectorId: string;
            category: string;
            windowSize: string;
            triggerLabel: string;
            requestToken: number;
        };
        expect(intent.metricKey).toBe('network_rx_bytes_per_second');
        expect(intent.collectorId).toBe('collector-c');
        expect(intent.category).toBe('network');
        expect(intent.windowSize).toBe('1h');
        expect(intent.triggerLabel).toBe('Incident drilldown');
        expect(intent.requestToken).toBeGreaterThan(0);
    });

    it('navigates to risk, rca, security, incidents, audit, and logs pages', async () => {
        const user = userEvent.setup();
        render(<App />);

        await click(user, screen.getByTitle('Risk Insights'));
        expect(screen.getByTestId('risk-insights-page')).toBeInTheDocument();

        await click(user, screen.getByTitle('Joint Risk'));
        expect(screen.getByTestId('joint-risk-page')).toBeInTheDocument();

        await click(user, screen.getByTitle('RCA Workflow'));
        expect(screen.getByTestId('rca-page')).toBeInTheDocument();

        await click(user, screen.getByTitle('Security Dashboard'));
        expect(screen.getByTestId('security-dashboard-page')).toBeInTheDocument();

        await click(user, screen.getByTitle('Incidents'));
        expect(screen.getByTestId('incidents-page')).toBeInTheDocument();

        await click(user, screen.getByTitle('Audit Log'));
        expect(screen.getByTestId('audit-log-page')).toBeInTheDocument();

        await click(user, screen.getByTitle('Logs'));
        expect(screen.getByTestId('logs-page')).toBeInTheDocument();
    });

    it('loads the initial page from the URL and keeps the canonical page slug in sync', async () => {
        const user = userEvent.setup();
        window.history.replaceState({}, '', '/?page=security-dashboard');
        render(<App />);

        expect(screen.getByTestId('security-dashboard-page')).toBeInTheDocument();
        expect(window.location.search).toBe('?page=security-dashboard');

        await click(user, screen.getByTitle('Incidents'));
        expect(screen.getByTestId('incidents-page')).toBeInTheDocument();
        expect(window.location.search).toBe('?page=incidents');

        await click(user, screen.getByTitle('Dashboard'));
        expect(screen.getByTestId('dashboard-grid')).toBeInTheDocument();
        expect(window.location.search).toBe('');
    });
});
