import React from 'react';
import { describe, expect, it, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import MetricOverviewPanel from '../MetricOverviewPanel';
import { fetchFleetTimeseries } from '@/api/trends';

vi.mock('@/api/trends', () => ({
    fetchFleetTimeseries: vi.fn(),
}));

const fetchFleetTimeseriesMock = vi.mocked(fetchFleetTimeseries);

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
        <div style={{ width: 1400, height: 900 }}>
            <QueryClientProvider client={client}>{ui}</QueryClientProvider>
        </div>,
    );
}

describe('MetricOverviewPanel data flow', () => {
    beforeEach(() => {
        vi.clearAllMocks();
    });

    it('renders live summary cards from fleet timeseries payload', async () => {
        fetchFleetTimeseriesMock.mockResolvedValue({
            collector_id: 'collector-a',
            hostname: 'node-a',
            window: '30m',
            generated_at: '2026-02-21T00:00:00Z',
            latest_at: '2026-02-21T00:00:00Z',
            sample_count: 2,
            numeric_summary: {
                cpu_usage_percent: 66.6,
                load1: 1.25,
                memory_used_percent: 72.4,
                memory_used_bytes: 8 * 1024 * 1024 * 1024,
                memory_total_bytes: 16 * 1024 * 1024 * 1024,
                network_rx_bytes_per_second: 2048,
                network_tx_bytes_per_second: 1024,
                network_total_bytes_per_second: 3072,
                disk_read_bytes_per_second: 4096,
                disk_write_bytes_per_second: 8192,
                procs_running: 132,
                procs_blocked: 3,
            },
            series: [{
                key: 'cpu_usage_percent',
                display: 'CPU Usage',
                unit: 'percent',
                latest: 66.6,
                min: 40,
                max: 80,
                avg: 60,
                change_pct: 2,
                spike_count: 0,
                points: [
                    { timestamp: '2026-02-21T00:00:00Z', value: 64.2 },
                    { timestamp: '2026-02-21T00:01:00Z', value: 66.6 },
                ],
            }],
        });

        renderWithClient(<MetricOverviewPanel />);

        expect(await screen.findByText('CPU Usage')).toBeInTheDocument();
        expect(screen.getByText('66.6%')).toBeInTheDocument();
        expect(screen.getByText('Memory Usage')).toBeInTheDocument();
        expect(screen.getByText(/8\.0 GB \/ 16 GB/)).toBeInTheDocument();
        expect(screen.getByText('Network RX')).toBeInTheDocument();
        expect(screen.getByText('2.0 KB/s')).toBeInTheDocument();
        expect(screen.getByText('Running Processes')).toBeInTheDocument();
        expect(screen.getByText('132')).toBeInTheDocument();
    });

    it('surfaces API failures as an unavailable state', async () => {
        fetchFleetTimeseriesMock.mockRejectedValue(new Error('boom'));

        renderWithClient(<MetricOverviewPanel />);

        expect(await screen.findByText('Metric overview unavailable')).toBeInTheDocument();
    });
});
