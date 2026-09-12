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
let trendsFixture: Awaited<ReturnType<typeof fetchFleetTimeseries>>;

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
        trendsFixture = {
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
        };
        fetchFleetTimeseriesMock.mockResolvedValue(trendsFixture);
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

    it('does not offer reassuring conclusions while trends are loading', () => {
        fetchFleetTimeseriesMock.mockReturnValue(new Promise(() => {}));

        renderWithClient(<MetricTrendsPage />);

        expect(screen.getByText('Loading trend curves...')).toBeInTheDocument();
        expect(screen.queryByText(/Trend data is available|No anomalies detected/i)).not.toBeInTheDocument();
        expect(screen.getByText(/Loading observations before interpretation/i)).toBeInTheDocument();
    });

    it('does not offer reassuring conclusions when trends fail to load', async () => {
        fetchFleetTimeseriesMock.mockRejectedValue(new Error('offline'));

        renderWithClient(<MetricTrendsPage />);

        expect(await screen.findByText('Unable to load time-series data.')).toBeInTheDocument();
        expect(screen.queryByText(/Trend data is available|No anomalies detected/i)).not.toBeInTheDocument();
        expect(screen.getByText(/Interpretation unavailable because telemetry could not be loaded/i)).toBeInTheDocument();
    });

    it('shows an explicit empty state instead of a healthy interpretation', async () => {
        fetchFleetTimeseriesMock.mockResolvedValue({
            ...trendsFixture,
            sample_count: 0,
            numeric_summary: {},
            series: [],
            operational_insights: [],
        });

        renderWithClient(<MetricTrendsPage />);

        expect(await screen.findByText('No observations in the selected window.')).toBeInTheDocument();
        expect(screen.queryByText(/Trend data is available|No anomalies detected/i)).not.toBeInTheDocument();
    });

    it.each(['stale', 'degraded', 'delayed', 'unavailable'])('qualifies %s telemetry even when samples remain', async (state) => {
        fetchFleetTimeseriesMock.mockResolvedValue({
            ...trendsFixture,
            telemetry_quality: { ...trendsFixture.telemetry_quality, state },
            operational_insights: [],
        });

        renderWithClient(<MetricTrendsPage />);

        expect(await screen.findByText(new RegExp(`Telemetry is ${state}; available observations do not establish current health`, 'i'))).toBeInTheDocument();
        expect(screen.queryByText(/No detected anomalies|No anomalies detected|Trend data is available/i)).not.toBeInTheDocument();
    });

    it('preserves zero while excluding invalid values from summaries and anomaly findings', async () => {
        fetchFleetTimeseriesMock.mockResolvedValue({
            ...trendsFixture,
            numeric_summary: { cpu_usage_percent: 0, memory_used_percent: Infinity },
            series: [{
                ...trendsFixture.series[0],
                points: [
                    { timestamp: 'invalid', value: 999, is_anomaly: true },
                    { timestamp: '2026-02-21T00:00:00Z', value: NaN, is_anomaly: true },
                ],
            }],
        });

        renderWithClient(<MetricTrendsPage />);

        expect(await screen.findByText('0.0%')).toBeInTheDocument();
        expect(screen.queryByText(/Infinity|NaN|999\.0%/)).not.toBeInTheDocument();
        expect(screen.getByText('No observations in the selected window.')).toBeInTheDocument();
    });

    it('uses neutral styling for rising pressure and positive change', async () => {
        renderWithClient(<MetricTrendsPage />);

        expect(await screen.findByText('sustained rise')).not.toHaveClass('text-emerald-200');
        expect(screen.getByText('Δ +3.0%')).not.toHaveClass('text-emerald-300');
    });
});
