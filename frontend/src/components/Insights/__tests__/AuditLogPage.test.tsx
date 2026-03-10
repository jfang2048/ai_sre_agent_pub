import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import AuditLogPage from '../AuditLogPage';
import { fetchWorkflowAuditRecords } from '@/api/agentWorkflows';
import { fetchControllerAuditRecords, fetchControllerRuns, fetchControllerToolRegistry } from '@/api/controller';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/agentWorkflows', () => ({
    fetchWorkflowAuditRecords: vi.fn(),
}));

vi.mock('@/api/controller', () => ({
    fetchControllerAuditRecords: vi.fn(),
    fetchControllerRuns: vi.fn(),
    fetchControllerToolRegistry: vi.fn(),
}));

const fetchWorkflowAuditRecordsMock = vi.mocked(fetchWorkflowAuditRecords);
const fetchControllerAuditRecordsMock = vi.mocked(fetchControllerAuditRecords);
const fetchControllerRunsMock = vi.mocked(fetchControllerRuns);
const fetchControllerToolRegistryMock = vi.mocked(fetchControllerToolRegistry);

describe('AuditLogPage', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        fetchControllerAuditRecordsMock.mockResolvedValue({
            records: [
                {
                    id: 'audit-1',
                    actor: 'sre-user',
                    action: 'incident_intake',
                    resource: 'intake-1',
                    status: 'success',
                    input: { severity: 'high' },
                    output: 'incident accepted',
                    evidence: [],
                    occurred_at: '2026-02-28T00:00:00Z',
                    workflow_id: '',
                    collector_id: 'collector-a',
                    incident_id: '',
                    approval_gate: false,
                },
            ],
            count: 1,
            timestamp: '2026-02-28T00:00:01Z',
        });

        fetchWorkflowAuditRecordsMock.mockResolvedValue({
            records: [
                {
                    id: 'wf-audit-1',
                    workflow_id: 'wf-1',
                    workflow_type: 'joint_risk',
                    stage: 'collect_signals',
                    action: 'tool_call',
                    tool: 'metrics_query',
                    collector_id: 'collector-a',
                    dry_run: true,
                    requires_approval: false,
                    approved: false,
                    status: 'success',
                    input: { collector_id: 'collector-a' },
                    output_summary: 'history_samples=45',
                    error_message: '',
                    timestamp: '2026-02-28T00:00:02Z',
                },
            ],
            count: 1,
            timestamp: '2026-02-28T00:00:03Z',
        });

        fetchControllerRunsMock.mockResolvedValue({
            runs: [
                {
                    run_id: 'run-1',
                    workflow_type: 'joint_risk',
                    status: 'completed',
                    collector_id: 'collector-a',
                    trigger: 'manual',
                    dry_run: true,
                    requested_at: '2026-02-28T00:00:00Z',
                    started_at: '2026-02-28T00:00:01Z',
                    completed_at: '2026-02-28T00:00:03Z',
                    summary: 'joint risk high',
                },
            ],
            count: 1,
            timestamp: '2026-02-28T00:00:04Z',
        });

        fetchControllerToolRegistryMock.mockResolvedValue({
            tools: [
                {
                    name: 'metrics_query',
                    version: 'v0.6',
                    description: 'Query metrics and trend windows',
                    deterministic: true,
                    read_only: true,
                    requires_approval: false,
                    supports_dry_run: true,
                    supports_rollback: false,
                    input_schema: '{}',
                    output_schema: '{}',
                },
            ],
            count: 1,
            timestamp: '2026-02-28T00:00:05Z',
        });
    });

    it('renders merged audit events, runs, and registry details', async () => {
        renderWithClient(<AuditLogPage />);

        expect(await screen.findByText('Action Audit Log')).toBeInTheDocument();
        expect(await screen.findByText('Recent Audit Events')).toBeInTheDocument();
        expect(await screen.findByText('Controller Runs')).toBeInTheDocument();
        expect(await screen.findByText('Tool Registry')).toBeInTheDocument();
        expect(await screen.findByText(/incident_intake/i)).toBeInTheDocument();
        expect(await screen.findByText(/collect_signals\/tool_call/i)).toBeInTheDocument();
        expect(await screen.findByText(/metrics_query v0.6/i)).toBeInTheDocument();
    });

    it('filters rows by source', async () => {
        const user = userEvent.setup();
        renderWithClient(<AuditLogPage />);
        await screen.findByText(/collect_signals\/tool_call/i);
        await screen.findByText(/incident_intake/i);

        const sourceSelect = screen.getByRole('combobox');
        await act(async () => {
            await user.selectOptions(sourceSelect, 'controller');
        });
        await waitFor(() => {
            expect(screen.queryByText(/collect_signals\/tool_call/i)).not.toBeInTheDocument();
        });
    });
});
