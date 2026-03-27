import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import IncidentsPage from '../IncidentsPage';
import { fetchFleetNodes } from '@/api/trends';
import { fetchWorkflowIncidentByID, fetchWorkflowIncidents } from '@/api/agentWorkflows';
import { renderWithClient } from '@/test/utils';

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
            synthesized_incident: {
                incident_id: 'incident-1',
                summary: 'HIGH; incident grouped from CPU pressure, IO latency; candidate cluster: noisy sidecar with unmanaged listener',
                grouped_signals: [
                    {
                        signal_id: 'cpu',
                        signal_type: 'cpu pressure',
                        source: 'metrics',
                        scope: 'node',
                        entity: 'collector-a',
                        severity: 'high',
                        score: 0.81,
                        summary: 'cpu pressure on collector-a',
                        evidence_ids: ['e-1'],
                        last_observed: '2026-02-28T00:00:00Z',
                    },
                ],
                impacted_scope: ['node/collector-a', 'process/checkout-api'],
                time_window: { start: '2026-02-27T23:15:00Z', end: '2026-02-28T00:00:00Z' },
                severity: 'high',
                confidence: 0.72,
                candidate_root_cause_cluster: 'noisy sidecar with unmanaged listener',
            },
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
                    category: 'immediate_investigation',
                    priority: 'high',
                    summary: 'Validate ownership and close unmanaged listener',
                    details: 'check deployment and service selectors',
                    checks: ['ss -ltnp', 'kubectl get svc -A'],
                    safe: true,
                    dry_run_default: true,
                    requires_approval: false,
                    rationale: 'the top hypothesis points to an unmanaged listener',
                    expected_impact: 'confirms whether the open port belongs to the workload',
                    risk_level: 'high',
                    reversible: true,
                    rollback_hint: 'none',
                },
            ],
            proposed_actions: [
                {
                    id: 'r-1',
                    recommendation_id: 'r-1',
                    category: 'immediate_investigation',
                    risk_reference: 'wf-1',
                    command_preview: 'Validate ownership and close unmanaged listener',
                    impact_scope: 'collector-a',
                    risk_level: 'high',
                    rationale: 'the top hypothesis points to an unmanaged listener',
                    expected_impact: 'confirms whether the open port belongs to the workload',
                    confidence: 0.72,
                    evidence_ids: ['e-1'],
                    rollback_plan: 'none',
                    approval_required: false,
                    dry_run_plan: 'check deployment and service selectors',
                    audit_intent: 'Validate ownership and close unmanaged listener',
                    collector_id: 'collector-a',
                    workflow_id: 'wf-1',
                    policy: {
                        status: 'allowed',
                        reason: 'read-only or low-impact action',
                        requires_approval: false,
                        dry_run_required: true,
                        rollback_required: false,
                    },
                    proposed_at: '2026-02-28T00:01:00Z',
                    status: 'proposed',
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
            unresolved_gaps: ['RAG retrieval did not return corroborating evidence'],
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
        expect(await screen.findByText('Proposed Actions')).toBeInTheDocument();
        expect(await screen.findByText('Grouped Signals')).toBeInTheDocument();
        expect((await screen.findAllByText(/Combined risk is high because CPU\+IO\+port exposure co-occurred/i)).length).toBeGreaterThan(0);
    });

    it('updates status filter and refetches incidents', async () => {
        const user = userEvent.setup();
        renderWithClient(<IncidentsPage />);

        expect((await screen.findAllByText(/Combined risk is high because CPU\+IO\+port exposure co-occurred/i)).length).toBeGreaterThan(0);
        const selects = screen.getAllByRole('combobox');
        await act(async () => {
            await user.selectOptions(selects[1], 'open');
        });

        await waitFor(() => {
            expect(fetchWorkflowIncidentsMock).toHaveBeenLastCalledWith(
                expect.objectContaining({
                    status: 'open',
                }),
            );
        });
    });
});
