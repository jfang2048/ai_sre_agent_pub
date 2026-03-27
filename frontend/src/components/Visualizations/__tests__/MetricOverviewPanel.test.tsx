import React from 'react';
import { describe, expect, it, beforeEach } from 'vitest';
import { screen } from '@testing-library/react';
import MetricOverviewPanel from '../MetricOverviewPanel';
import { fetchControllerStatus } from '@/api/controlPlane';
import { fetchFleetTimeseries } from '@/api/trends';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/controlPlane', () => ({
    fetchControllerStatus: vi.fn(),
}));

vi.mock('@/api/trends', () => ({
    fetchFleetTimeseries: vi.fn(),
}));

const fetchControllerStatusMock = vi.mocked(fetchControllerStatus);
const fetchFleetTimeseriesMock = vi.mocked(fetchFleetTimeseries);

describe('MetricOverviewPanel data flow', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        fetchControllerStatusMock.mockResolvedValue({
            version: 'v0.7.0',
            uptime: '5m',
            total_nodes: 2,
            healthy_nodes: 2,
            scrape_interval: '5s',
            listen_address: '127.0.0.1:8080',
            collector_coverage: {
                state: 'fresh',
                total_collectors: 2,
                fresh_collectors: 2,
                delayed_collectors: 0,
                stale_collectors: 0,
                degraded_collectors: 0,
                partial_collectors: 0,
                fallback_collectors: 0,
                backlog_collectors: 0,
                coverage_percent: 100,
                quality_hint: 'Fleet telemetry coverage is healthy.',
            },
        });
    });

    it('renders live summary cards from fleet timeseries payload', async () => {
        fetchFleetTimeseriesMock.mockResolvedValue({
            collector_id: 'collector-a',
            hostname: 'node-a',
            window: '30m',
            generated_at: '2026-02-21T00:00:00Z',
            latest_at: '2026-02-21T00:00:00Z',
            sample_count: 2,
            telemetry_quality: {
                state: 'fresh',
                coverage_percent: 100,
                source_mode: 'probe_core',
                quality_hint: 'Telemetry freshness and coverage are currently healthy.',
            },
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
            operational_insights: [{
                key: 'capacity_exhaustion_risk',
                severity: 'advisory',
                summary: 'Memory headroom is shrinking over time, which is more consistent with a structural exhaustion risk than a one-off spike.',
                decision: 'Inspect top memory consumers and error logs now, before reclaim, swap, or OOM turns a slow burn into an outage.',
            }],
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
                trend: 'sustained rise',
                operational_hint: 'Sustained CPU pressure reduces scheduler headroom and usually reflects an upstream resource bottleneck, not just busy compute.',
                points: [
                    { timestamp: '2026-02-21T00:00:00Z', value: 64.2 },
                    { timestamp: '2026-02-21T00:01:00Z', value: 66.6 },
                ],
            }],
        });

        renderWithClient(<MetricOverviewPanel />, { width: 1400, height: 900 });

        expect(await screen.findByText('CPU Usage')).toBeInTheDocument();
        expect(screen.getByText('66.6%')).toBeInTheDocument();
        expect(screen.getByText('Memory Usage')).toBeInTheDocument();
        expect(screen.getByText(/8\.0 GB \/ 16 GB/)).toBeInTheDocument();
        expect(screen.getByText('Network RX')).toBeInTheDocument();
        expect(screen.getByText('2.0 KB/s')).toBeInTheDocument();
        expect(screen.getByText('Running Processes')).toBeInTheDocument();
        expect(screen.getByText('132')).toBeInTheDocument();
        expect(screen.getByText(/structural exhaustion risk/i)).toBeInTheDocument();
        expect(screen.getByText('sustained rise')).toBeInTheDocument();
    });

    it('renders degraded telemetry state instead of fake zero values', async () => {
        fetchFleetTimeseriesMock.mockResolvedValue({
            collector_id: 'collector-a',
            hostname: 'node-a',
            window: '30m',
            generated_at: '2026-02-21T00:00:00Z',
            latest_at: '2026-02-21T00:00:00Z',
            sample_count: 1,
            telemetry_quality: {
                state: 'degraded',
                coverage_percent: 40,
                source_mode: 'go',
                quality_hint: 'Telemetry coverage is degraded; treat flat or missing values as an observability warning, not a healthy zero.',
            },
            numeric_summary: {
                cpu_usage_percent: 12,
                load1: 0.5,
            },
            operational_insights: [],
            series: [],
        });

        renderWithClient(<MetricOverviewPanel />, { width: 1400, height: 900 });

        expect(await screen.findByText(/Telemetry degraded/i)).toBeInTheDocument();
        expect(screen.getAllByText('Degraded').length).toBeGreaterThan(0);
        expect(screen.queryByText('0%')).not.toBeInTheDocument();
    });

    it('shows fleet coverage degradation when collectors are stale or replaying backlog', async () => {
        fetchControllerStatusMock.mockResolvedValue({
            version: 'v0.7.0',
            uptime: '5m',
            total_nodes: 4,
            healthy_nodes: 3,
            scrape_interval: '5s',
            listen_address: '127.0.0.1:8080',
            collector_coverage: {
                state: 'stale',
                total_collectors: 4,
                fresh_collectors: 2,
                delayed_collectors: 0,
                stale_collectors: 1,
                degraded_collectors: 1,
                partial_collectors: 1,
                fallback_collectors: 1,
                backlog_collectors: 1,
                coverage_percent: 50,
                quality_hint: 'Fleet coverage is reduced because one collector is stale and another is still replaying backlog.',
            },
        });
        fetchFleetTimeseriesMock.mockResolvedValue({
            collector_id: 'collector-a',
            hostname: 'node-a',
            window: '30m',
            generated_at: '2026-02-21T00:00:00Z',
            latest_at: '2026-02-21T00:00:00Z',
            sample_count: 2,
            telemetry_quality: {
                state: 'fresh',
                coverage_percent: 100,
                source_mode: 'probe_core',
                quality_hint: 'Telemetry freshness and coverage are currently healthy.',
            },
            numeric_summary: {
                cpu_usage_percent: 44,
                memory_used_percent: 61,
            },
            operational_insights: [],
            series: [],
        });

        renderWithClient(<MetricOverviewPanel />, { width: 1400, height: 900 });

        expect(await screen.findByText(/Fleet coverage stale/i)).toBeInTheDocument();
        expect(screen.getByText('2/4 fresh')).toBeInTheDocument();
        expect(screen.getByText(/coverage 50% · partial 1 · backlog 1/i)).toBeInTheDocument();
    });

    it('surfaces API failures as an unavailable state', async () => {
        fetchFleetTimeseriesMock.mockRejectedValue(new Error('boom'));

        renderWithClient(<MetricOverviewPanel />, { width: 1400, height: 900 });

        expect(await screen.findByText('Metric overview unavailable')).toBeInTheDocument();
    });
});
