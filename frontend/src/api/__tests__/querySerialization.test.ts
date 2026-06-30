import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fetchWorkflowAuditRecords, fetchPotentialRiskFindings } from '../agentWorkflows';
import { fetchDataPathDiagnostics } from '../dataPathDiagnostics';
import { fetchGPUEvents } from '../gpuObservability';
import { fetchK8sTopNodes } from '../k8s';
import { fetchLogs } from '../logs';
import { fetchSecurityDashboard } from '../security';
import { fetchTopPrograms } from '../topPrograms';
import { fetchFleetTimeseries } from '../trends';
import { api } from '../client';

vi.mock('../client', () => ({
    api: {
        get: vi.fn().mockResolvedValue({ data: {} }),
    },
}));

const getMock = vi.mocked(api.get);

describe('API query serialization', () => {
    beforeEach(() => {
        getMock.mockClear();
    });

    it('serializes trimmed collector filters for diagnostics endpoints', async () => {
        await fetchDataPathDiagnostics({ collectorId: ' collector-a ' });
        expect(getMock).toHaveBeenCalledWith('/diagnostics/data-path?collector_id=collector-a');
    });

    it('serializes workflow query flags without changing endpoint shape', async () => {
        await fetchPotentialRiskFindings({
            collectorId: 'collector-a',
            window: '1h',
            limit: 5,
            dryRun: false,
            refresh: true,
        });
        expect(getMock).toHaveBeenCalledWith('/agent/potential-risks?collector_id=collector-a&window=1h&limit=5&dry_run=false&refresh=true');
    });

    it('serializes repeated metric parameters for fleet timeseries', async () => {
        await fetchFleetTimeseries({
            collectorId: 'collector-a',
            window: '1h',
            limit: 3,
            metrics: [' cpu_usage ', 'memory_used', ''],
        });
        expect(getMock).toHaveBeenCalledWith('/fleet/timeseries?collector_id=collector-a&window=1h&limit=3&metric=cpu_usage&metric=memory_used');
    });

    it('serializes mixed log-search filters including offset zero', async () => {
        await fetchLogs({
            text: ' error ',
            collectorId: ' collector-a ',
            limit: 10,
            offset: 0,
            sort: 'desc',
        });
        expect(getMock).toHaveBeenCalledWith('/logs/search?q=error&collector_id=collector-a&limit=10&offset=0&sort=desc');
    });

    it('serializes workflow audit filters with trimmed workflow ids', async () => {
        await fetchWorkflowAuditRecords(50, ' wf-1 ');
        expect(getMock).toHaveBeenCalledWith('/agent/workflow/audit?limit=50&workflow_id=wf-1');
    });

    it('serializes top-program filters without emitting blank collector ids', async () => {
        await fetchTopPrograms({ limit: 20, collectorId: ' collector-a ' });
        expect(getMock).toHaveBeenCalledWith('/top/programs?limit=20&collector_id=collector-a');
    });

    it('serializes gpu event filters with trimmed values', async () => {
        await fetchGPUEvents({
            collectorId: ' collector-a ',
            gpuId: ' 0 ',
            severity: 'critical',
            window: '15m',
            limit: 5,
        });
        expect(getMock).toHaveBeenCalledWith('/gpu/events?collector_id=collector-a&gpu_id=0&severity=critical&window=15m&limit=5');
    });

    it('serializes k8s top-node filters', async () => {
        await fetchK8sTopNodes({ metric: 'cpu', limit: 10, cluster: ' prod-a ' });
        expect(getMock).toHaveBeenCalledWith('/k8s/nodes/top?metric=cpu&limit=10&cluster=prod-a');
    });

    it('serializes security dashboard filters', async () => {
        await fetchSecurityDashboard({ collectorId: ' collector-a ', category: ' runtime ', limit: 25 });
        expect(getMock).toHaveBeenCalledWith('/security/dashboard?collector_id=collector-a&category=runtime&limit=25');
    });
});
