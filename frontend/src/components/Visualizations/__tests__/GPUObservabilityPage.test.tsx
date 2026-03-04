import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import GPUObservabilityPage from '../GPUObservabilityPage';
import {
    fetchGPUCorrelation,
    fetchGPUEvents,
    fetchGPUNodes,
    fetchGPUProcesses,
    fetchGPUProcessTimeline,
    fetchGPUTimeline,
} from '@/api/gpuObservability';

vi.mock('@/api/gpuObservability', () => ({
    fetchGPUNodes: vi.fn(),
    fetchGPUTimeline: vi.fn(),
    fetchGPUProcessTimeline: vi.fn(),
    fetchGPUEvents: vi.fn(),
    fetchGPUProcesses: vi.fn(),
    fetchGPUCorrelation: vi.fn(),
}));

const fetchGPUNodesMock = vi.mocked(fetchGPUNodes);
const fetchGPUTimelineMock = vi.mocked(fetchGPUTimeline);
const fetchGPUProcessTimelineMock = vi.mocked(fetchGPUProcessTimeline);
const fetchGPUEventsMock = vi.mocked(fetchGPUEvents);
const fetchGPUProcessesMock = vi.mocked(fetchGPUProcesses);
const fetchGPUCorrelationMock = vi.mocked(fetchGPUCorrelation);

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

describe('GPUObservabilityPage', () => {
    beforeEach(() => {
        vi.clearAllMocks();

        fetchGPUNodesMock.mockResolvedValue({
            count: 1,
            timestamp: '2026-02-24T00:00:00Z',
            nodes: [
                {
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    last_seen: '2026-02-24T00:00:00Z',
                    gpu_count: 1,
                    labels: {},
                    gpus: {
                        '0': {
                            gpu_index: '0',
                            name: 'NVIDIA H100',
                            util_sm_percent: 82,
                            mem_used_mib: 22100,
                            mem_total_mib: 81920,
                            pcie_link_util_percent: 46,
                            pcie_rx_mb_s: 9210,
                            pcie_tx_mb_s: 7430,
                            xid_errors_total: 2,
                            reset_events_total: 0,
                        },
                    },
                },
            ],
        });

        fetchGPUProcessesMock.mockResolvedValue({
            collector_id: 'collector-a',
            gpu_id: '0',
            sort_by: 'sm_util',
            count: 1,
            timestamp: '2026-02-24T00:00:00Z',
            processes: [
                {
                    pid: '321',
                    name: 'trainer',
                    util_sm_percent: 90,
                    mem_mib: 20480,
                    util_enc_percent: 5,
                    util_dec_percent: 0,
                    context_active: 1,
                },
            ],
        });

        fetchGPUTimelineMock.mockResolvedValue({
            collector_id: 'collector-a',
            gpu_id: '0',
            metric: 'node_gpu_utilization_sm_percent',
            window: '1h',
            count: 2,
            timestamp: '2026-02-24T00:00:00Z',
            points: [
                { timestamp: '2026-02-24T00:00:00Z', value: 75 },
                { timestamp: '2026-02-24T00:01:00Z', value: 82 },
            ],
        });

        fetchGPUProcessTimelineMock.mockResolvedValue({
            collector_id: 'collector-a',
            gpu_id: '0',
            pid: '321',
            metric: 'node_gpu_process_sm_util_percent',
            window: '1h',
            count: 2,
            timestamp: '2026-02-24T00:00:00Z',
            points: [
                { timestamp: '2026-02-24T00:00:00Z', value: 88 },
                { timestamp: '2026-02-24T00:01:00Z', value: 90 },
            ],
        });

        fetchGPUEventsMock.mockResolvedValue({
            collector_id: 'collector-a',
            gpu_id: '0',
            window: '1h',
            count: 1,
            timestamp: '2026-02-24T00:00:00Z',
            events: [
                {
                    timestamp: '2026-02-24T00:00:00Z',
                    event_type: 'xid',
                    severity: 'critical',
                    gpu_index: '0',
                    code: '43',
                    count: 1,
                },
            ],
        });

        fetchGPUCorrelationMock.mockResolvedValue({
            collector_id: 'collector-a',
            hostname: 'node-a',
            timestamp: '2026-02-24T00:00:00Z',
            risks: ['GPU starvation risk: low/medium GPU utilization while IO/network pressure is elevated.'],
            gpu: {
                gpu_count: 1,
                avg_util_sm_percent: 82,
                memory_pressure_percent: 27,
                avg_pcie_link_util_percent: 46,
                kernel_hotspot_peak_sm_util: 90,
                context_count_total: 4,
                throttle_active_devices: 0,
                xid_errors_total: 2,
                uvm_faults_total: 0,
                reset_events_total: 0,
            },
            host_pressure: {
                cpu_iowait_percent: 8,
                disk_utilization_peak_percent: 63,
                disk_latency_p99_ms: 28,
                network_utilization_peak_percent: 55,
                tcp_retransmit_ratio_percent: 1,
            },
            scores: {
                starvation_risk: 0.61,
                communication_risk: 0.44,
                reliability_risk: 0.16,
                overall_risk_percent: 44.3,
            },
        });
    });

    it('renders timelines, process table, events, and correlation cards', async () => {
        renderWithClient(<GPUObservabilityPage />);

        expect(await screen.findByText('GPU Observability')).toBeInTheDocument();
        expect(await screen.findByText('GPU Metric Timeline')).toBeInTheDocument();
        expect(await screen.findByText('Top GPU Processes')).toBeInTheDocument();
        expect(await screen.findByText('GPU Event Timeline')).toBeInTheDocument();
        expect(await screen.findByText('Cross-Resource Correlation')).toBeInTheDocument();

        expect(await screen.findByText('trainer')).toBeInTheDocument();
        expect(await screen.findByText('xid')).toBeInTheDocument();
        expect(await screen.findByText('Starvation Risk')).toBeInTheDocument();
    });
});
