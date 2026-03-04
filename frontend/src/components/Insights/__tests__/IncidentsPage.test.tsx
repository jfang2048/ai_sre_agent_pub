import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import IncidentsPage from '../IncidentsPage';
import { fetchFleetNodes } from '@/api/trends';
import { fetchWorkflowIncidentByID, fetchWorkflowIncidents } from '@/api/agentWorkflows';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
}));

vi.mock('@/api/agentWorkflows', () => ({
    fetchWorkflowIncidents: vi.fn(),
    fetchWorkflowIncidentByID: vi.fn(),
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchWorkflowIncidentsMock = vi.mocked(fetchWorkflowIncidents);
const fetchWorkflowIncidentByIDMock = vi.mocked(fetchWorkflowIncidentByID);

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

describe('IncidentsPage', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        fetchFleetNodesMock.mockResolvedValue({
            nodes: [
                { collector_id: 'collector-a', hostname: 'node-a', updated_at: '2026-02-28T00:00:00Z' },
            ],
            count: 1,
            timestamp: '2026-02-28T00:00:00Z',
        });

        const incident = {
            incident_id: 'incident-1',
            workflow_id: 'wf-1',
            status: 'open',
            source: 'joint_risk',
            collector_id: 'collector-a',
            opened_at: '2026-02-28T00:00:00Z',
            closed_at: '',
            risk_level: 'high',
            risk_score: 0.81,
            summary: 'Combined risk is high because CPU+IO+port exposure co-occurred',
            most_likely_cause: 'noisy sidecar with unmanaged listener',
            confidence: 0.72,
            symptoms: ['cpu pressure', 'io latency growth'],
            timeline: [
                { timestamp: '2026-02-28T00:00:00Z', phase: 'detect', summary: 'joint risk crossed threshold' },
                { timestamp: '2026-02-28T00:01:00Z', phase: 'verify', summary: 'hypothesis verified with process evidence' },
            ],
            evidence: [
                {
                    id: 'e-1',
                    kind: 'metric',
                    source: 'metrics_query',
                    summary: 'cpu_usage 91 baseline 60',
                    metric_name: 'node_cpu_usage_percent',
                    value: 91,
                    baseline: 60,
                    delta: 31,
                    timestamp: '2026-02-28T00:01:00Z',
                },
            ],
            hypotheses: [
                {
                    id: 'h-1',
                    rank: 1,
                    title: 'sidecar endpoint exposure',
                    confidence: 0.76,
                    description: 'unmanaged listener on uncommon port',
                    evidence_ids: ['e-1'],
                },
            ],
            recommendations: [
                {
                    id: 'r-1',
                    priority: 'high',
                    summary: 'Validate ownership and close unmanaged listener',
                    details: 'check deployment and service selectors',
                    checks: ['ss -ltnp', 'kubectl get svc -A'],
                    safe: true,
                    dry_run_default: true,
                    requires_approval: false,
                    reversible: true,
                    rollback_hint: 'none',
                },
            ],
            agent_loop: {
                mode: 'plan_act_verify',
                iterations: 2,
                replans: 1,
                steps_planned: 6,
                steps_executed: 5,
                steps_verified: 5,
                completed: true,
                stop_reason: 'sufficient_evidence',
                plan_steps: [],
                plan_revisions: [],
            },
        };

        fetchWorkflowIncidentsMock.mockResolvedValue({
            incidents: [incident],
            count: 1,
            timestamp: '2026-02-28T00:02:00Z',
        });
        fetchWorkflowIncidentByIDMock.mockResolvedValue({
            incident,
            timestamp: '2026-02-28T00:02:00Z',
        });
    });

    it('renders incidents list, evidence, timeline, and recommendations', async () => {
        renderWithClient(<IncidentsPage />);

        expect(await screen.findByText('Incidents')).toBeInTheDocument();
        expect(await screen.findByText('Incident List')).toBeInTheDocument();
        expect(await screen.findByText('Incident Evidence')).toBeInTheDocument();
        expect(await screen.findByText('Incident Timeline')).toBeInTheDocument();
        expect(await screen.findByText('Recommended Actions')).toBeInTheDocument();
        expect((await screen.findAllByText(/Combined risk is high because CPU\+IO\+port exposure co-occurred/i)).length).toBeGreaterThan(0);
    });

    it('updates status filter and refetches incidents', async () => {
        const user = userEvent.setup();
        renderWithClient(<IncidentsPage />);

        await screen.findByText('node-a');
        const selects = screen.getAllByRole('combobox');
        await user.selectOptions(selects[1], 'open');

        expect(fetchWorkflowIncidentsMock).toHaveBeenCalled();
    });
});
