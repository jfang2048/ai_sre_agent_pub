import { beforeEach, describe, expect, it, vi } from 'vitest';

const { get } = vi.hoisted(() => ({
    get: vi.fn(),
}));

vi.mock('../client', () => ({
    api: {
        get,
    },
}));

import {
    fetchAgentTrace,
    fetchJointRiskReports,
    fetchProposedActions,
    fetchWorkflowAuditRecords,
    fetchWorkflowIncidentByID,
} from '../agentWorkflows';

describe('agent workflow api helpers', () => {
    beforeEach(() => {
        get.mockReset();
    });

    it('builds workflow query strings for report fetches', async () => {
        get.mockResolvedValue({ data: { reports: [], count: 0, timestamp: 'now' } });

        await fetchJointRiskReports({
            collectorId: 'collector-a',
            window: '15m',
            limit: 5,
            trigger: 'manual',
            status: 'open',
            dryRun: true,
            refresh: false,
        });

        expect(get).toHaveBeenCalledWith(
            '/agent/joint-risk?collector_id=collector-a&window=15m&limit=5&trigger=manual&status=open&dry_run=true&refresh=false',
        );
    });

    it('trims workflow ids for audit and incident fetches', async () => {
        get.mockResolvedValue({ data: { audit_records: [], count: 0, timestamp: 'now' } });
        await fetchWorkflowAuditRecords(25, '  workflow-123  ');
        expect(get).toHaveBeenCalledWith('/agent/workflow/audit?limit=25&workflow_id=workflow-123');

        get.mockResolvedValue({ data: { incident: { incident_id: 'incident-1' }, timestamp: 'now' } });
        await fetchWorkflowIncidentByID('  incident-1  ');
        expect(get).toHaveBeenCalledWith('/agent/workflow/incidents/incident-1');
    });

    it('rejects blank required ids', async () => {
        await expect(fetchWorkflowIncidentByID('   ')).rejects.toThrow('incident id is required');
        await expect(fetchAgentTrace('   ')).rejects.toThrow('trace id is required');
    });

    it('trims proposed-action status filters', async () => {
        get.mockResolvedValue({ data: { actions: [], count: 0, timestamp: 'now' } });
        await fetchProposedActions(10, '  proposed  ');
        expect(get).toHaveBeenCalledWith('/agent/proposed-actions?limit=10&status=proposed');
    });
});
