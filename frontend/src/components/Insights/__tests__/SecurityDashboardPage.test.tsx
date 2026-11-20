import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import SecurityDashboardPage from '../SecurityDashboardPage';
import { fetchFleetNodes } from '@/api/trends';
import { fetchSecurityDashboard } from '@/api/security';
import { explainAudit, fetchAuditAnalysis, importAuditReport } from '@/api/securityAudit';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
}));

vi.mock('@/api/security', () => ({
    fetchSecurityDashboard: vi.fn(),
}));

vi.mock('@/api/securityAudit', () => ({
    fetchAuditAnalysis: vi.fn(),
    importAuditReport: vi.fn(),
    explainAudit: vi.fn(),
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchSecurityDashboardMock = vi.mocked(fetchSecurityDashboard);

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

    it('imports a report and lets the operator explain the resulting evidence', async () => {
        const user = userEvent.setup();
        const reportText = '{"version":"2.1.0","runs":[]}';
        const file = new File([reportText], 'scan.sarif', { type: 'application/json' });
        Object.defineProperty(file, 'text', { value: async () => reportText });
        vi.mocked(importAuditReport).mockResolvedValue({ imported: 1, updated: 0 });
        vi.mocked(fetchAuditAnalysis).mockResolvedValue({
            count: 1,
            records: [{
                id: 'audit-1', asset: 'prod/api', source: 'sarif', kind: 'finding',
                rule: 'RULE-1', severity: 'high', observed_at: '2026-02-28T00:02:00Z',
            }],
            correlations: [{ id: 'correlation-1', rule: 'related', asset: 'prod/api', summary: 'Related evidence', evidence_ids: ['audit-1'] }],
            anomalies: [{ asset: 'prod/api', bucket: '2026-02-28T00:00:00Z', observed: 0.21, baseline: 0.03, score: 6, samples: 18, evidence_ids: ['audit-1'] }],
        });
        vi.mocked(explainAudit).mockResolvedValue({ mode: 'rules', summary: 'Review the related evidence.', evidence_ids: ['audit-1'] });
        renderWithClient(<SecurityDashboardPage />);

        expect(await screen.findByRole('heading', { name: 'DevSecOps Audit' })).toBeInTheDocument();
        expect(fetchAuditAnalysis).not.toHaveBeenCalled();
        await act(async () => {
            await user.type(screen.getByRole('textbox', { name: 'Asset ID' }), 'prod/api');
            await user.upload(screen.getByLabelText('Report file (up to 4 MiB)'), file);
            await user.click(screen.getByRole('button', { name: 'Import report' }));
        });

        expect(await screen.findByText('1 imported · 0 updated')).toBeInTheDocument();
        expect(importAuditReport).toHaveBeenCalledWith({ asset: 'prod/api', format: 'sarif', report: reportText });
        expect(fetchAuditAnalysis).toHaveBeenCalledWith({ asset: 'prod/api', window: '168h' });
        expect(await screen.findByText('RULE-1')).toBeInTheDocument();
        expect(await screen.findByText('Related evidence')).toBeInTheDocument();
        expect(await screen.findByText(/Denial rate 21.0%/)).toBeInTheDocument();
        expect(screen.getAllByRole('link', { name: 'audit-1' })[0]).toHaveAttribute('href', '#security-audit-record-audit-1');
        expect(explainAudit).not.toHaveBeenCalled();
        await act(async () => {
            await user.click(screen.getByRole('button', { name: 'Explain audit' }));
        });
        expect(await screen.findByText('Review the related evidence.')).toBeInTheDocument();
        expect(explainAudit).toHaveBeenCalledWith({ asset: 'prod/api', window: '168h' });
    });

    it('applies selector filters and re-queries dashboard data', async () => {
        const user = userEvent.setup();
        renderWithClient(<SecurityDashboardPage />);

        await screen.findByText(/unexpected_ports=2/i);
        const selects = screen.getAllByRole('combobox');
        await act(async () => {
            await user.selectOptions(selects[0], 'collector-a');
        });
        await act(async () => {
            await user.selectOptions(selects[2], 'high');
        });

        await waitFor(() => {
            expect(fetchSecurityDashboardMock).toHaveBeenLastCalledWith(
                expect.objectContaining({
                    collectorId: 'collector-a',
                    severity: 'high',
                }),
            );
        });
    });
});
