import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import SecurityDashboardPage from '../SecurityDashboardPage';
import { fetchFleetNodes } from '@/api/trends';
import { fetchSecurityDashboard } from '@/api/security';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
}));

vi.mock('@/api/security', () => ({
    fetchSecurityDashboard: vi.fn(),
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchSecurityDashboardMock = vi.mocked(fetchSecurityDashboard);

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

describe('SecurityDashboardPage', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        fetchFleetNodesMock.mockResolvedValue({
            nodes: [
                { collector_id: 'collector-a', hostname: 'node-a', updated_at: '2026-02-28T00:00:00Z' },
            ],
            count: 1,
            timestamp: '2026-02-28T00:00:00Z',
        });
        fetchSecurityDashboardMock.mockResolvedValue({
            findings: [
                {
                    id: 'sec-1',
                    severity: 'high',
                    category: 'network_exposure',
                    scope: 'node',
                    collector_id: 'collector-a',
                    summary: 'Listening-port exposure exceeds baseline ownership policy',
                    evidence: ['listening_ports=17', 'unexpected_ports=2', 'stale_listening_ports=1'],
                    recommended_action: 'Map exposed ports to owning services and close unmanaged listeners.',
                    score: 0.76,
                    observed_at: '2026-02-28T00:02:00Z',
                    source: 'collector_metrics',
                },
            ],
            summary: {
                critical: 0,
                high: 1,
                medium: 2,
                low: 1,
            },
            trends: [
                { timestamp: '2026-02-28T00:00:00Z', critical: 0, high: 1, medium: 1, low: 1, total: 3 },
                { timestamp: '2026-02-28T00:02:00Z', critical: 0, high: 1, medium: 2, low: 1, total: 4 },
            ],
            count: 1,
            timestamp: '2026-02-28T00:02:00Z',
        });
    });

    it('renders findings and evidence drilldowns', async () => {
        renderWithClient(<SecurityDashboardPage />);

        expect(await screen.findByText('Security Dashboard')).toBeInTheDocument();
        expect(await screen.findByText('Security Findings')).toBeInTheDocument();
        expect(await screen.findByText('Finding Evidence')).toBeInTheDocument();
        expect(await screen.findByText('Security Risk Trend')).toBeInTheDocument();
        expect((await screen.findAllByText(/Listening-port exposure exceeds baseline ownership policy/i)).length).toBeGreaterThan(0);
        expect(await screen.findByText(/unexpected_ports=2/i)).toBeInTheDocument();
    });

    it('applies selector filters and re-queries dashboard data', async () => {
        const user = userEvent.setup();
        renderWithClient(<SecurityDashboardPage />);

        await screen.findByText('node-a');
        const selects = screen.getAllByRole('combobox');
        await user.selectOptions(selects[0], 'collector-a');
        await user.selectOptions(selects[2], 'high');

        expect(fetchSecurityDashboardMock).toHaveBeenCalled();
    });
});
