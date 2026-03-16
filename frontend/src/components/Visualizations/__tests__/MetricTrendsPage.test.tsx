import React from 'react';
import { describe, expect, it, beforeEach } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import MetricTrendsPage from '../MetricTrendsPage';
import { fetchFleetNode, fetchFleetNodes, fetchFleetTimeseries } from '@/api/trends';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
    fetchFleetTimeseries: vi.fn(),
    fetchFleetNode: vi.fn(),
}));

vi.mock('../ResourceProcessBreakdownPanel', () => ({
    __esModule: true,
    default: () => <div data-testid="resource-process-breakdown-panel">Resource breakdown panel</div>,
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchFleetTimeseriesMock = vi.mocked(fetchFleetTimeseries);
const fetchFleetNodeMock = vi.mocked(fetchFleetNode);

describe('MetricTrendsPage data flow', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        fetchFleetNodesMock.mockResolvedValue({
            nodes: [
                { collector_id: 'collector-a', hostname: 'node-a', updated_at: '2026-02-21T00:00:00Z' },
                { collector_id: 'collector-b', hostname: 'node-b', updated_at: '2026-02-21T00:10:00Z' },
            ],
            count: 2,
            timestamp: '2026-02-21T00:10:00Z',
        });
        fetchFleetTimeseriesMock.mockResolvedValue({
            collector_id: 'collector-b',
            hostname: 'node-b',
            window: '1h',
            generated_at: '2026-02-21T00:10:00Z',
            latest_at: '2026-02-21T00:10:00Z',
            sample_count: 3,
            telemetry_quality: {
                state: 'fresh',
                coverage_percent: 100,
                source_mode: 'probe_core',
                quality_hint: 'Telemetry freshness and coverage are currently healthy.',
            },
            numeric_summary: {
                cpu_usage_percent: 54.3,
                load1: 1.2,
                memory_used_percent: 63.0,
                memory_used_bytes: 6 * 1024 * 1024 * 1024,
                memory_total_bytes: 16 * 1024 * 1024 * 1024,
                network_rx_bytes_per_second: 1000,
                network_tx_bytes_per_second: 2000,
                network_total_bytes_per_second: 3000,
                disk_read_bytes_per_second: 4000,
                disk_write_bytes_per_second: 5000,
                disk_total_iops_per_second: 900,
                disk_queue_depth_total: 2,
                disk_utilization_peak_percent: 65,
                filesystem_space_pressure_percent: 32,
                filesystem_inode_pressure_percent: 18,
                procs_running: 84,
                procs_blocked: 1,
            },
            operational_insights: [{
                key: 'storage_bottleneck_risk',
                severity: 'warning',
                summary: 'CPU wait and disk latency are rising together, which usually means the workload is blocked on storage rather than raw compute.',
                decision: 'Inspect the hottest device and partition, then verify which process is causing queue growth before scaling CPU.',
                evidence: ['cpu_iowait + disk latency coupling'],
            }],
            series: [{
                key: 'cpu_usage_percent',
                display: 'CPU Usage',
                unit: 'percent',
                tier: 'tier1_runtime',
                latest: 54.3,
                min: 40,
                max: 60,
                avg: 50,
                change_pct: 3,
                spike_count: 0,
                trend: 'sustained rise',
                pattern: 'steady',
                sustained: true,
                operational_hint: 'Sustained CPU pressure reduces scheduler headroom and usually reflects an upstream resource bottleneck, not just busy compute.',
                points: [
                    { timestamp: '2026-02-21T00:00:00Z', value: 52.0 },
                    { timestamp: '2026-02-21T00:05:00Z', value: 53.1 },
                    { timestamp: '2026-02-21T00:10:00Z', value: 54.3 },
                ],
            }],
        });
        fetchFleetNodeMock.mockResolvedValue({
            collector_id: 'collector-b',
            hostname: 'node-b',
            updated_at: '2026-02-21T00:10:00Z',
            metrics: { node_cpu_usage_percent: 54.3 },
            storage_devices: {},
            storage_partitions: {},
            filesystems: {},
        });
    });

    it('loads fleet trends and refetches when collector filter changes', async () => {
        renderWithClient(<MetricTrendsPage />);

        expect(await screen.findByText('Metric Trends')).toBeInTheDocument();
        await waitFor(() => {
            expect(fetchFleetTimeseriesMock).toHaveBeenCalledWith({
                collectorId: undefined,
                window: '1h',
                limit: 360,
            });
        });
        expect(await screen.findByText('Operational Interpretation')).toBeInTheDocument();
        expect(screen.getByText(/blocked on storage rather than raw compute/i)).toBeInTheDocument();
        expect(screen.getByText('Tier 1 runtime')).toBeInTheDocument();

        const collectorSelect = screen.getAllByRole('combobox')[0];
        fireEvent.change(collectorSelect, { target: { value: 'collector-b' } });

        await waitFor(() => {
            expect(fetchFleetTimeseriesMock).toHaveBeenCalledWith({
                collectorId: 'collector-b',
                window: '1h',
                limit: 360,
            });
        });
        expect(screen.getByText('CPU')).toBeInTheDocument();
    });

    it('shows stale telemetry status instead of treating missing metrics as zeros', async () => {
        fetchFleetTimeseriesMock.mockResolvedValueOnce({
            collector_id: 'collector-b',
            hostname: 'node-b',
            window: '1h',
            generated_at: '2026-02-21T00:10:00Z',
            latest_at: '2026-02-21T00:00:00Z',
            sample_count: 1,
            telemetry_quality: {
                state: 'stale',
                coverage_percent: 60,
                source_mode: 'go',
                quality_hint: 'Telemetry is stale enough to increase MTTR and false-RCA risk; refresh collector health before acting.',
            },
            numeric_summary: {
                cpu_usage_percent: 54.3,
                load1: 1.2,
            },
            operational_insights: [],
            series: [],
        });

        renderWithClient(<MetricTrendsPage />);

        expect(await screen.findByText(/Telemetry stale/i)).toBeInTheDocument();
        expect(screen.getAllByText('Stale').length).toBeGreaterThan(0);
        expect(screen.queryByText('0%')).not.toBeInTheDocument();
    });
});
