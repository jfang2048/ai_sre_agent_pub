import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import IncidentAnalysisPage from '../IncidentAnalysisPage';
import {
    fetchAnalysisAnomalies,
    fetchAnalysisCorrelations,
    fetchAnalysisIncidents,
    fetchAnalysisStatus,
} from '@/api/analysis';
import {
    fetchFleetNode,
    fetchFleetNodes,
    fetchFleetTimeseries,
} from '@/api/trends';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/analysis', () => ({
    fetchAnalysisStatus: vi.fn(),
    fetchAnalysisIncidents: vi.fn(),
    fetchAnalysisAnomalies: vi.fn(),
    fetchAnalysisCorrelations: vi.fn(),
}));

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
    fetchFleetNode: vi.fn(),
    fetchFleetTimeseries: vi.fn(),
}));

vi.mock('@/components/Insights/K8sDrilldown', () => ({
    __esModule: true,
    default: () => <div data-testid="k8s-drilldown-mock">k8s-drilldown</div>,
}));

const fetchAnalysisStatusMock = vi.mocked(fetchAnalysisStatus);
const fetchAnalysisIncidentsMock = vi.mocked(fetchAnalysisIncidents);
const fetchAnalysisAnomaliesMock = vi.mocked(fetchAnalysisAnomalies);
const fetchAnalysisCorrelationsMock = vi.mocked(fetchAnalysisCorrelations);
const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchFleetNodeMock = vi.mocked(fetchFleetNode);
const fetchFleetTimeseriesMock = vi.mocked(fetchFleetTimeseries);

describe('IncidentAnalysisPage', () => {
    beforeEach(() => {
        vi.clearAllMocks();

        fetchFleetNodesMock.mockResolvedValue({
            nodes: [
                {
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    updated_at: '2026-02-25T00:00:00Z',
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:00:00Z',
        });

        fetchAnalysisStatusMock.mockResolvedValue({
            status: 'active',
            config: {
                threshold_alerts: true,
                anomaly_detection: true,
                correlation_analysis: true,
                llm_enabled: false,
                interval: '30s',
            },
            summary: {
                total_alerts: 2,
                critical: 1,
                warning: 1,
                anomalies: 3,
                rca_count: 1,
                correlations: 2,
                incidents: 1,
            },
            timestamp: '2026-02-25T00:00:00Z',
        });

        fetchAnalysisIncidentsMock.mockResolvedValue({
            incidents: [
                {
                    id: 'incident-1',
                    node_name: 'collector-a',
                    classification: 'communication_congestion',
                    severity: 'critical',
                    status: 'active',
                    what_happened: 'Communication congestion detected on collector-a',
                    probable_cause: 'Network path shows retransmissions and drops',
                    confidence: 0.84,
                    impacted_components: ['network', 'log-pipeline'],
                    supporting_signals: [
                        {
                            source: 'anomaly',
                            signal: 'network spike',
                            metric: 'node_network_receive_bytes_per_second',
                            value: 120000000,
                            expected: 18000000,
                        },
                    ],
                    primary_metric: 'node_network_receive_bytes_per_second',
                    log_query: '/api/v1/logs/search?collector_id=collector-a&q=timeout&limit=50',
                    window_start: '2026-02-25T00:00:00Z',
                    window_end: '2026-02-25T00:05:00Z',
                    generated_at: '2026-02-25T00:05:00Z',
                },
            ],
            count: 1,
            classification_count: {
                communication_congestion: 1,
            },
            timestamp: '2026-02-25T00:05:00Z',
        });

        fetchAnalysisAnomaliesMock.mockResolvedValue({
            anomalies: [
                {
                    node_name: 'collector-a',
                    metric_name: 'node_network_receive_bytes_per_second',
                    score: 4.2,
                    direction: 'up',
                    current_value: 120000000,
                    expected_value: 18000000,
                    detected_at: '2026-02-25T00:05:00Z',
                    reason: 'baseline_window=12 z=4.2 robust=3.8 threshold=2.5',
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:05:00Z',
        });

        fetchAnalysisCorrelationsMock.mockResolvedValue({
            correlations: [
                {
                    node_name: 'collector-a',
                    metric_a: 'node_network_receive_bytes_per_second',
                    metric_b: 'probe_core_network_tcp_retransmissions_per_sec',
                    coefficient: 0.88,
                    direction: 'positive',
                    lag: 0,
                    sample_count: 24,
                    detected_at: '2026-02-25T00:05:00Z',
                },
            ],
            count: 1,
            timestamp: '2026-02-25T00:05:00Z',
        });

        fetchFleetTimeseriesMock.mockResolvedValue({
            collector_id: 'collector-a',
            hostname: 'node-a',
            window: '1h',
            generated_at: '2026-02-25T00:05:00Z',
            sample_count: 2,
            numeric_summary: {},
            series: [
                {
                    key: 'cpu_usage_percent',
                    display: 'CPU Usage',
                    unit: 'percent',
                    latest: 62,
                    min: 55,
                    max: 62,
                    avg: 58.5,
                    change_pct: 10,
                    spike_count: 0,
                    points: [
                        { timestamp: '2026-02-25T00:04:00Z', value: 55 },
                        { timestamp: '2026-02-25T00:05:00Z', value: 62 },
                    ],
                },
                {
                    key: 'network_total_bytes_per_second',
                    display: 'Network Throughput',
                    unit: 'bytes_per_second',
                    latest: 125000000,
                    min: 42000000,
                    max: 125000000,
                    avg: 83500000,
                    change_pct: 197,
                    spike_count: 1,
                    points: [
                        { timestamp: '2026-02-25T00:04:00Z', value: 42000000 },
                        { timestamp: '2026-02-25T00:05:00Z', value: 125000000 },
                    ],
                },
            ],
        });

        fetchFleetNodeMock.mockResolvedValue({
            collector_id: 'collector-a',
            hostname: 'node-a',
            updated_at: '2026-02-25T00:05:00Z',
            metrics: {},
            processes: [
                {
                    pid: 123,
                    name: 'trainer',
                    cpu_percent: 82,
                    rss_bytes: 8 * 1024 * 1024 * 1024,
                    io_read_bps: 42000000,
                    io_write_bps: 28000000,
                },
            ],
        });
    });

    it('renders incidents, anomalies, and supports trend drilldown callback', async () => {
        const onOpenTrends = vi.fn();
        const user = userEvent.setup();
        renderWithClient(<IncidentAnalysisPage onOpenTrends={onOpenTrends} />);

        expect(await screen.findByText('Incident / Analysis')).toBeInTheDocument();
        expect(await screen.findByText('Incident Summaries')).toBeInTheDocument();
        expect(await screen.findByText('Communication congestion detected on collector-a')).toBeInTheDocument();
        expect(await screen.findByText('Anomaly Stream')).toBeInTheDocument();
        expect(await screen.findByText('Top Processes (CPU-ranked)')).toBeInTheDocument();
        expect(await screen.findByText('Correlation Graph')).toBeInTheDocument();
        expect(await screen.findByText('Kernel / eBPF RCA Signals')).toBeInTheDocument();
        expect(await screen.findByText('Kubernetes Topology Drilldown')).toBeInTheDocument();
        expect(await screen.findByText('trainer')).toBeInTheDocument();
        expect(screen.getByTestId('k8s-drilldown-mock')).toBeInTheDocument();

        await user.click(screen.getAllByRole('button', { name: 'Open trends' })[0]);
        expect(onOpenTrends).toHaveBeenCalledTimes(1);
        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            metricKey: 'network_rx_bytes_per_second',
        }));
    });
});
