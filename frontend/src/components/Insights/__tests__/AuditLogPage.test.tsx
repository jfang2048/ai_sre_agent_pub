import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import AuditLogPage from '../AuditLogPage';
import { fetchWorkflowAuditRecords } from '@/api/agentWorkflows';
import { fetchControllerAuditRecords, fetchControllerRuns, fetchControllerToolRegistry } from '@/api/controller';

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
                    version: 'v0.5',
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
        expect(await screen.findByText(/metrics_query v0.5/i)).toBeInTheDocument();
    });

    it('filters rows by source', async () => {
        const user = userEvent.setup();
        renderWithClient(<AuditLogPage />);
        await screen.findByText('Action Audit Log');

        const sourceSelect = screen.getByRole('combobox');
        await user.selectOptions(sourceSelect, 'controller');
        expect(screen.queryByText(/collect_signals\/tool_call/i)).not.toBeInTheDocument();
    });
});

