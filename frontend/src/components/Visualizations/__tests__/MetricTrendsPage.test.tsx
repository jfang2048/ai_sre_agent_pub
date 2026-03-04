import React from 'react';
import { describe, expect, it, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import MetricTrendsPage from '../MetricTrendsPage';
import { fetchFleetNode, fetchFleetNodes, fetchFleetTimeseries } from '@/api/trends';

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
    return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

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
            series: [{
                key: 'cpu_usage_percent',
                display: 'CPU Usage',
                unit: 'percent',
                latest: 54.3,
                min: 40,
                max: 60,
                avg: 50,
                change_pct: 3,
                spike_count: 0,
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
});
