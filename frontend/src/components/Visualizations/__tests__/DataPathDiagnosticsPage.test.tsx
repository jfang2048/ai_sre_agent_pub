import React from 'react';
import { describe, expect, it, beforeEach } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import DataPathDiagnosticsPage from '../DataPathDiagnosticsPage';
import { fetchFleetNodes } from '@/api/trends';
import {
    fetchAIInfraStackDiagnostics,
    fetchDataPathDiagnostics,
    fetchKernelPathDiagnostics,
    fetchRCAPacketExport,
    fetchRootCauseDiagnostics,
    fetchWorkloadPathDiagnostics,
} from '@/api/dataPathDiagnostics';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/trends', () => ({
    fetchFleetNodes: vi.fn(),
}));

vi.mock('@/api/dataPathDiagnostics', () => ({
    fetchAIInfraStackDiagnostics: vi.fn(),
    fetchDataPathDiagnostics: vi.fn(),
    fetchRootCauseDiagnostics: vi.fn(),
    fetchKernelPathDiagnostics: vi.fn(),
    fetchWorkloadPathDiagnostics: vi.fn(),
    fetchRCAPacketExport: vi.fn(),
}));

vi.mock('../ResourceProcessBreakdownPanel', () => ({
    __esModule: true,
    default: () => <div data-testid="resource-process-breakdown-panel">Resource breakdown panel</div>,
}));

const fetchFleetNodesMock = vi.mocked(fetchFleetNodes);
const fetchAIInfraStackDiagnosticsMock = vi.mocked(fetchAIInfraStackDiagnostics);
const fetchDataPathDiagnosticsMock = vi.mocked(fetchDataPathDiagnostics);
const fetchRootCauseDiagnosticsMock = vi.mocked(fetchRootCauseDiagnostics);
const fetchKernelPathDiagnosticsMock = vi.mocked(fetchKernelPathDiagnostics);
const fetchWorkloadPathDiagnosticsMock = vi.mocked(fetchWorkloadPathDiagnostics);
const fetchRCAPacketExportMock = vi.mocked(fetchRCAPacketExport);

describe('DataPathDiagnosticsPage drilldown wiring', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        window.history.replaceState({}, '', '/');
        fetchFleetNodesMock.mockResolvedValue({
            nodes: [{ collector_id: 'collector-a', hostname: 'node-a', updated_at: '2026-02-19T00:00:00Z' }],
            count: 1,
            timestamp: '2026-02-19T00:00:00Z',
        });
        fetchAIInfraStackDiagnosticsMock.mockResolvedValue({
            collector_id: '',
            cluster: '',
            namespace: '',
            service: '',
            generated_at: '2026-02-19T00:00:00Z',
            summary: {
                node_count: 1,
                workload_count: 1,
                layer_count: 8,
                critical_layers: 2,
                degraded_layers: 3,
                top_layer_id: 'communication_fabric',
                top_layer_title: 'Communication fabric',
                top_risk: 'RDMA congestion counters indicate fabric queue pressure.',
                coverage_percent: 64,
                root_cause_findings: 1,
                critical_findings: 1,
                degraded_findings: 0,
                communication_skews: 1,
                incident_drilldowns: 1,
                measurements_measured: 2,
                measurements_partial: 0,
                measurements_missing: 1,
                methods_direct: 1,
                methods_derived: 0,
                methods_proxy: 1,
                methods_missing: 1,
            },
            layers: [{
                id: 'communication_fabric',
                title: 'Communication fabric',
                scope: 'node+cluster',
                score: 7.1,
                severity: 'critical',
                coverage_percent: 70,
                signals: {
                    network_critical_nodes: 1,
                    rdma_congested_nodes: 1,
                },
                top_risks: ['RDMA congestion counters indicate fabric queue pressure.'],
                measurements: [{
                    name: 'RDMA congestion',
                    metric: 'node_rdma_congestion_events_per_second',
                    source: '/sys/class/infiniband/*/ports/*/hw_counters',
                    status: 'measured',
                    method: 'direct',
                }, {
                    name: 'In-node PCIe interconnect',
                    metric: 'gpuobs.pcie_{rx,tx}_mb_s',
                    source: 'nvidia-smi / NVML',
                    status: 'missing',
                    method: 'missing',
                }],
                domains: [{
                    id: 'in_node_interconnect',
                    title: 'In-node interconnect (PCIe/NVLink/NVSwitch/CXL)',
                    score: 6.1,
                    severity: 'degraded',
                    coverage_percent: 66,
                    signals: {
                        gpu_devices_total: 8,
                        nvlink_signal_nodes: 0,
                    },
                }, {
                    id: 'inter_node_fabric',
                    title: 'Inter-node fabric (RDMA/InfiniBand/RoCE/TCP)',
                    score: 8.3,
                    severity: 'critical',
                    coverage_percent: 100,
                    signals: {
                        rdma_congested_nodes: 1,
                        retransmit_hot_nodes: 1,
                    },
                }],
                ranked_entities: [{
                    kind: 'node',
                    id: 'collector-a',
                    label: 'node-a',
                    score: 7.4,
                    severity: 'critical',
                    detail: 'network pressure',
                }],
            }, {
                id: 'serving_inference',
                title: 'Serving and inference scheduling',
                scope: 'service+route',
                score: 1.2,
                severity: 'healthy',
                coverage_percent: 20,
                signals: {
                    inference_workloads: 1,
                },
                top_risks: ['No measurable inference-serving workload in current scope.'],
                measurements: [{
                    name: 'KV-cache pressure',
                    metric: 'runtime kv-cache occupancy',
                    source: 'not integrated',
                    status: 'missing',
                    method: 'proxy',
                }],
                ranked_entities: [],
            }],
            workload_mappings: [{
                cluster: 'cluster-a',
                namespace: 'ml',
                kind: 'StatefulSet',
                name: 'trainer-a',
                service: 'trainer',
                path: 'workload -> pod -> node -> device',
                pods_running: 8,
                pods_pending: 0,
                pods_failed: 0,
                gpu_requests: 16,
                gpu_limits: 16,
                node_count: 2,
                resolved_nodes: 1,
                nodes: ['node-a'],
                risk_flags: ['communication_imbalance'],
                bottleneck: 'network',
            }],
            incident_drilldowns: [{
                finding_id: 'network_congestion_training_slowdown',
                finding_title: 'Network congestion is throttling inter-node communication',
                category: 'network',
                severity: 'critical',
                confidence: 0.84,
                workflow: 'incident -> workload -> placement -> contention',
                affected_nodes: ['node-a'],
                contention: [{
                    name: 'tcp_retransmit_ratio',
                    value: 0.025,
                    source: '/proc/net/snmp',
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                }],
                workloads: [{
                    id: 'cluster-a/ml/StatefulSet/trainer-a',
                    cluster: 'cluster-a',
                    namespace: 'ml',
                    kind: 'StatefulSet',
                    name: 'trainer-a',
                    service: 'trainer',
                    severity: 'critical',
                    bottleneck: 'network',
                    queue_delay_seconds: 11.2,
                    pods_pending: 0,
                    pods_failed: 0,
                    node_count: 2,
                    resolved_nodes: 1,
                    gpu_requests: 16,
                    risks: ['communication_imbalance'],
                    reason: 'network bottleneck, affected-node overlap',
                }],
                placements: [{
                    workload_id: 'cluster-a/ml/StatefulSet/trainer-a',
                    node_id: 'k8s-node-a',
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    cluster: 'cluster-a',
                    zone: 'zone-a',
                    score: 7.6,
                    severity: 'critical',
                    queue_delay_seconds: 11.2,
                    signals: {
                        rdma_congestion_per_second: 55,
                        tcp_retransmit_ratio: 0.03,
                    },
                    reason: 'queue pressure in RDMA path',
                }],
                triage: ['Confirm fabric queue depth', 'Correlate with workload placement'],
            }],
        });
        fetchDataPathDiagnosticsMock.mockResolvedValue({
            collector_id: '',
            generated_at: '2026-02-19T00:00:00Z',
            summary: {
                node_count: 1,
                network_critical: 0,
                network_degraded: 1,
                storage_critical: 1,
                storage_degraded: 0,
                probe_core_critical: 1,
                probe_core_degraded: 0,
                probe_core_fallback_nodes: 1,
                probe_core_invalid_config_nodes: 1,
                runtime_namespace_nodes: 1,
                runtime_limited_nodes: 0,
                runtime_degraded_nodes: 1,
                total_anomalies: 2,
                critical_data_paths: 1,
            },
            network: {
                cluster_health_score: 82.1,
                rankings: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    score: 4.2,
                    severity: 'degraded',
                    signals: {
                        tcp_retransmit_ratio: 0.025,
                    },
                }],
                anomalies: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    resource: 'network',
                    metric: 'node_tcp_retransmit_ratio',
                    value: 0.025,
                    baseline: 0.005,
                    z_score: 3.6,
                    severity: 'degraded',
                }],
                top_processes: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    pid: '1234',
                    name: 'nccl-worker',
                    net_bytes_per_second: 10_000_000,
                    net_connections: 8,
                    score: 4.1,
                }],
            },
            storage: {
                cluster_health_score: 61.2,
                rankings: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    score: 7.9,
                    severity: 'critical',
                    signals: {
                        latency_p99_ms: 45.8,
                    },
                }],
                anomalies: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    resource: 'storage',
                    metric: 'node_disk_request_latency_p99_seconds',
                    value: 0.041,
                    baseline: 0.009,
                    z_score: 4.4,
                    severity: 'critical',
                }],
                top_processes: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    pid: '5678',
                    name: 'checkpoint-writer',
                    disk_read_bps: 2_000_000,
                    disk_write_bps: 20_000_000,
                    score: 7.2,
                }],
            },
            probe_core: {
                cluster_health_score: 43.0,
                rankings: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    score: 8.4,
                    severity: 'critical',
                    signals: {
                        last_frame_age_seconds: 19.5,
                        selection_valid: 0,
                    },
                    factors: [
                        'Collector is not using probe-core as active source; running on Go fallback path.',
                        'Probe-core module selection is invalid; check --collectors config.',
                    ],
                }],
                anomalies: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    resource: 'probe_core',
                    metric: 'collector_probe_core_last_frame_age_seconds',
                    value: 19.5,
                    baseline: 2.0,
                    z_score: 4.1,
                    severity: 'critical',
                }],
                top_processes: [],
            },
            data_paths: [{
                collector_id: 'collector-a',
                hostname: 'node-a',
                compute_score: 1.2,
                network_score: 4.2,
                storage_score: 7.9,
                overall_score: 13.3,
                severity: 'critical',
                bottleneck: 'storage',
                bottleneck_tip: ['High storage latency.'],
                runtime_mode: 'namespace',
                runtime_degraded: true,
                runtime_reasons: ['host_pid_namespace_unavailable'],
            }],
        });
        fetchRootCauseDiagnosticsMock.mockResolvedValue({
            collector_id: '',
            generated_at: '2026-02-19T00:00:00Z',
            summary: {
                node_count: 1,
                finding_count: 1,
                critical_findings: 1,
                degraded_findings: 0,
                top_finding_id: 'network_congestion_training_slowdown',
                top_finding_summary: 'Transport congestion and retransmit pressure are increasing collective communication latency.',
            },
            findings: [{
                id: 'network_congestion_training_slowdown',
                category: 'network',
                severity: 'critical',
                confidence: 0.84,
                title: 'Network congestion is throttling inter-node communication',
                hypothesis: 'Transport congestion and retransmit pressure are increasing collective communication latency.',
                impact: 'Distributed training/inference can stall on synchronization phases.',
                affected_nodes: [{ collector_id: 'collector-a', hostname: 'node-a' }],
                correlated_signals: ['tcp_retransmit_ratio', 'softnet_dropped_per_second'],
                actions: ['Inspect congestion domains.'],
            }],
            data_path: {
                network_critical: 1,
                storage_critical: 1,
                probe_core_critical: 1,
                total_anomalies: 2,
            },
        });
        fetchKernelPathDiagnosticsMock.mockResolvedValue({
            collector_id: '',
            generated_at: '2026-02-19T00:00:00Z',
            summary: {
                node_count: 1,
                critical_nodes: 1,
                degraded_nodes: 0,
                top_storage_stage: 'block_layer_device',
                top_network_stage: 'rdma_fabric',
                top_bottleneck_key: 'storage:block_layer_device',
            },
            nodes: [{
                collector_id: 'collector-a',
                hostname: 'node-a',
                overall_severity: 'critical',
                bottlenecks: ['storage:block_layer_device', 'network:rdma_fabric'],
                storage: {
                    score: 6.1,
                    severity: 'critical',
                    top_stage: 'block_layer_device',
                    stages: [],
                },
                network: {
                    score: 3.9,
                    severity: 'critical',
                    top_stage: 'rdma_fabric',
                    stages: [],
                },
            }],
        });
        fetchWorkloadPathDiagnosticsMock.mockResolvedValue({
            cluster: '',
            namespace: '',
            service: '',
            generated_at: '2026-02-19T00:00:00Z',
            summary: {
                workload_count: 1,
                critical_workloads: 1,
                degraded_workloads: 0,
                telemetry_covered_workloads: 1,
                multi_node_workloads: 1,
                gpu_starvation_risk_workloads: 1,
                communication_imbalance_workloads: 1,
                top_bottleneck: 'network',
            },
            workloads: [{
                cluster: 'cluster-a',
                namespace: 'ml',
                kind: 'StatefulSet',
                name: 'trainer-a',
                service: 'trainer',
                pods_total: 8,
                pods_running: 8,
                pods_pending: 0,
                pods_failed: 0,
                container_restarts: 2,
                gpu_requests: 16,
                gpu_limits: 16,
                node_count: 2,
                resolved_nodes: 1,
                telemetry_coverage_percent: 50,
                compute_score: 1.6,
                network_score: 5.3,
                storage_score: 3.8,
                overall_score: 5.3,
                severity: 'critical',
                bottleneck: 'network',
                top_storage_stage: 'block_layer_device',
                top_network_stage: 'rdma_fabric',
                signals: {
                    rdma_congestion_per_second: 42,
                    tcp_retransmit_ratio: 0.02,
                },
                sources: {
                    rdma_congestion_per_second: '/sys/class/infiniband/*/ports/*/counters',
                    tcp_retransmit_ratio: '/proc/net/snmp',
                },
                risks: ['cross_node_spread', 'communication_imbalance'],
                reasons: ['Network path pressure is elevated at kernel stage rdma_fabric.'],
                nodes: [{
                    node_name: 'k8s-node-a',
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    telemetry_available: true,
                    compute_score: 1.2,
                    network_score: 5.6,
                    storage_score: 3.1,
                    overall_score: 5.6,
                    severity: 'critical',
                    bottleneck: 'network',
                    top_storage_stage: 'block_layer_device',
                    top_network_stage: 'rdma_fabric',
                    signals: {
                        tcp_retransmit_ratio: 0.03,
                        rdma_congestion_per_second: 55,
                    },
                    sources: {
                        tcp_retransmit_ratio: '/proc/net/snmp',
                        rdma_congestion_per_second: '/sys/class/infiniband/*/ports/*/counters',
                    },
                    reasons: ['Queue pressure is rising in RDMA path.'],
                }],
            }],
        });
        fetchRCAPacketExportMock.mockResolvedValue({
            collector_id: '',
            cluster: '',
            namespace: '',
            service: '',
            sort_key: 'severity',
            sort_direction: 'desc',
            format: 'json',
            workload_limit: 30,
            generated_at: '2026-02-19T00:00:00Z',
            file_name: 'ai-sre-rca-packet_test.md',
            markdown: '# AI SRE RCA Packet\n\n## Root Cause Summary\n\n## Kernel Path Snapshot\n\n## Resource Pressure Snapshot\n\n# Workload Path Handoff\n',
            packet_sha256: '35ed8d8f85bb3df7de4f6f17e471f596f4e5a500d2fce4d1007f52f8a4e4ed9d',
            content_bytes: 118,
            summary: {
                root_cause_findings: 1,
                critical_findings: 1,
                degraded_findings: 0,
                kernel_nodes: 1,
                workloads: 1,
                network_ranked: 1,
                storage_ranked: 1,
                probe_core_ranked: 1,
            },
            source_metadata: {
                data_path_endpoint: '/api/v1/diagnostics/data-path',
                kernel_path_endpoint: '/api/v1/diagnostics/kernel-path',
                root_cause_endpoint: '/api/v1/diagnostics/root-cause',
                workload_path_endpoint: '/api/v1/diagnostics/workload-path',
            },
        });
    });

    it('maps network pressure signals to trend metric and category', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(screen.getAllByText('node-a').length).toBeGreaterThan(0));

        const networkSection = screen.getByText('Network Pressure Ranking').closest('section');
        expect(networkSection).toBeTruthy();
        const openButton = within(networkSection as HTMLElement).getAllByRole('button', { name: 'Open trends' })[0];
        await userEvent.click(openButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'network',
            metricKey: 'network_tx_bytes_per_second',
        }));
    });

    it('renders ai infra stack layers with measurement statuses', async () => {
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(screen.getByText('AI Infra Stack Layers')).toBeInTheDocument());
        expect(screen.getByTestId('resource-process-breakdown-panel')).toBeInTheDocument();
        await waitFor(() => expect(screen.getAllByText(/Communication fabric/i).length).toBeGreaterThan(0));
        expect(screen.getByText('SRE Reliability Snapshot')).toBeInTheDocument();
        expect(screen.getByText('Measurement Source Snapshot')).toBeInTheDocument();
        expect(screen.getByText(/Method legend:/i)).toBeInTheDocument();
        expect(screen.getByText('Layer Domain Decomposition')).toBeInTheDocument();
        expect(screen.getAllByText(/Inter-node fabric \(RDMA\/InfiniBand\/RoCE\/TCP\)/i).length).toBeGreaterThan(0);
        expect(screen.getByText(/workload\s*->\s*pod\s*->\s*node\s*->\s*device/i)).toBeInTheDocument();
        expect(screen.getAllByText('measured').length).toBeGreaterThan(0);
        expect(screen.getAllByText('missing').length).toBeGreaterThan(0);
    });

    it('renders incident-to-placement drilldowns and opens trends from placement hops', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(screen.getByText(/Incident → Workload → Placement Drilldowns/i)).toBeInTheDocument());
        expect(screen.getByText(/network bottleneck, affected-node overlap/i)).toBeInTheDocument();
        const findingCard = screen.getAllByText('Network congestion is throttling inter-node communication')[0]?.closest('article');
        expect(findingCard).toBeTruthy();
        const placementButton = within(findingCard as HTMLElement).getByRole('button', { name: 'Open trends' });
        await userEvent.click(placementButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'network',
            metricKey: 'network_rx_bytes_per_second',
        }));
    });

    it('opens contention trends from incident drilldown cards', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(screen.getByText(/Incident → Workload → Placement Drilldowns/i)).toBeInTheDocument());
        const findingCard = screen.getAllByText('Network congestion is throttling inter-node communication')[0]?.closest('article');
        expect(findingCard).toBeTruthy();

        const openButton = within(findingCard as HTMLElement).getByRole('button', { name: 'Open contention trend' });
        await userEvent.click(openButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'network',
            metricKey: 'network_tx_bytes_per_second',
            triggerLabel: 'Incident contention (network_congestion_training_slowdown)',
        }));
    });

    it('opens trends from ai infra domain decomposition rows', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(screen.getByText('Layer Domain Decomposition')).toBeInTheDocument());
        const domainSectionTitle = screen.getByText('Layer Domain Decomposition');
        const domainSection = domainSectionTitle.parentElement;
        expect(domainSection).toBeTruthy();
        const openButton = within(domainSection as HTMLElement).getAllByRole('button', { name: 'Open trends' })[0];
        await userEvent.click(openButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            category: 'network',
            metricKey: 'network_rx_bytes_per_second',
        }));
    });

    it('normalizes storage anomaly metrics before opening trends', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(screen.getByText('STORAGE node_disk_request_latency_p99_seconds')).toBeInTheDocument());

        const anomalyCard = screen.getByText('STORAGE node_disk_request_latency_p99_seconds').parentElement;
        expect(anomalyCard).toBeTruthy();
        const openButton = within(anomalyCard as HTMLElement).getByRole('button', { name: 'Open trends' });
        await userEvent.click(openButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'disk_io',
            metricKey: 'disk_request_latency_p99_ms',
        }));
    });

    it('maps probe-core reliability signals to probe-core trend metrics', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(screen.getAllByText('node-a').length).toBeGreaterThan(0));

        const probeCoreSection = screen.getByText('Probe-core Reliability Ranking').closest('section');
        expect(probeCoreSection).toBeTruthy();
        const openButton = within(probeCoreSection as HTMLElement).getByRole('button', { name: 'Open trends' });
        await userEvent.click(openButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'network',
            metricKey: 'probe_core_last_frame_age_ms',
        }));
    });

    it('passes process filter hints for process hotspot trace actions', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(screen.getByText('Top Network Processes')).toBeInTheDocument());

        const networkSection = screen.getByText('Network Pressure Ranking').closest('section');
        expect(networkSection).toBeTruthy();
        const traceButton = within(networkSection as HTMLElement).getByRole('button', { name: 'Trace in trends' });
        await userEvent.click(traceButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'network',
            metricKey: 'network_rx_bytes_per_second',
            processFilter: 'nccl-worker 1234',
        }));
    });

    it('renders root-cause findings with severity and confidence', async () => {
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(fetchRootCauseDiagnosticsMock).toHaveBeenCalled());
        await waitFor(() => expect(screen.getAllByText(/Network congestion is throttling inter-node communication/i).length).toBeGreaterThan(0));
        expect(screen.getByText(/Confidence 84% · network/i)).toBeInTheDocument();
    });

    it('opens trends from root-cause finding cards', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(fetchRootCauseDiagnosticsMock).toHaveBeenCalled());
        await waitFor(() => expect(screen.getAllByText(/Network congestion is throttling inter-node communication/i).length).toBeGreaterThan(0));
        const rootCauseSection = screen.getByText('Cross-Layer Root Cause Findings').closest('section');
        expect(rootCauseSection).toBeTruthy();
        const findingCard = within(rootCauseSection as HTMLElement)
            .getByText('Network congestion is throttling inter-node communication')
            .closest('article');
        expect(findingCard).toBeTruthy();

        const openButton = within(findingCard as HTMLElement).getByRole('button', { name: 'Open trends' });
        await userEvent.click(openButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'network',
            metricKey: 'network_tx_bytes_per_second',
            triggerLabel: 'Root cause (network_congestion_training_slowdown)',
        }));
    });

    it('maps collective-runtime root-cause findings to process-aware trend metrics', async () => {
        fetchRootCauseDiagnosticsMock.mockResolvedValueOnce({
            collector_id: '',
            generated_at: '2026-02-19T00:00:00Z',
            summary: {
                node_count: 1,
                finding_count: 1,
                critical_findings: 1,
                degraded_findings: 0,
                top_finding_id: 'collective_runtime_queueing_contention',
                top_finding_summary: 'Communication-active workers show socket backlog and scheduler wait growth.',
            },
            data_path: {
                network_critical: 1,
                storage_critical: 0,
                probe_core_critical: 0,
                total_anomalies: 1,
            },
            findings: [{
                id: 'collective_runtime_queueing_contention',
                category: 'collective_runtime',
                severity: 'critical',
                confidence: 0.9,
                title: 'Collective runtime queueing contention is degrading step latency',
                hypothesis: 'Communication-active workers show socket backlog and scheduler wait growth.',
                impact: 'Synchronization phases wait on contended workers.',
                correlated_signals: ['rca_net_process_queued_bytes', 'rca_net_process_connections', 'rca_cpu_process_sched_wait_ratio'],
                affected_nodes: [{ collector_id: 'collector-a', hostname: 'node-a' }],
                evidence: [{
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    signal: 'rca_net_process_queued_bytes',
                    value: 8388608,
                    source: '/proc/net/tcp + /proc/*/fd',
                }],
                actions: ['Inspect top collective workers with high socket queue backlog.'],
            }],
        });

        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(fetchRootCauseDiagnosticsMock).toHaveBeenCalled());
        await waitFor(() => expect(screen.getAllByText(/Collective runtime queueing contention is degrading step latency/i).length).toBeGreaterThan(0));
        const rootCauseSection = screen.getByText('Cross-Layer Root Cause Findings').closest('section');
        expect(rootCauseSection).toBeTruthy();
        const findingCard = within(rootCauseSection as HTMLElement)
            .getByText('Collective runtime queueing contention is degrading step latency')
            .closest('article');
        expect(findingCard).toBeTruthy();

        const openButton = within(findingCard as HTMLElement).getByRole('button', { name: 'Open trends' });
        await userEvent.click(openButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'network',
            metricKey: 'network_tx_bytes_per_second',
            triggerLabel: 'Root cause (collective_runtime_queueing_contention)',
        }));
    });

    it('renders kernel-path diagnostics panel with stage hints', async () => {
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(fetchKernelPathDiagnosticsMock).toHaveBeenCalled());
        const kernelSection = screen.getByText('Linux Kernel Path Diagnostics').closest('section');
        expect(kernelSection).toBeTruthy();
        await waitFor(() => expect(within(kernelSection as HTMLElement).queryByText(/Loading kernel-path diagnostics/i)).not.toBeInTheDocument());
        expect(kernelSection as HTMLElement).toHaveTextContent(/Top storage stage block layer device/i);
        expect(kernelSection as HTMLElement).toHaveTextContent(/Top network stage rdma fabric/i);
        expect(kernelSection as HTMLElement).toHaveTextContent(/storage:block_layer_device/i);
    });

    it('renders workload-path diagnostics panel with mapping and risks', async () => {
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
        const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
        expect(workloadSection).toBeTruthy();
        await waitFor(() => expect(within(workloadSection as HTMLElement).queryByText(/Loading workload-path diagnostics/i)).not.toBeInTheDocument());
        expect(workloadSection as HTMLElement).toHaveTextContent(/ml\/trainer-a/i);
        expect(workloadSection as HTMLElement).toHaveTextContent(/Top bottleneck network/i);
        expect(workloadSection as HTMLElement).toHaveTextContent(/cross node spread/i);
    });

    it('maps workload-path bottleneck to trends drilldown metric/category', async () => {
        const onOpenTrends = vi.fn();
        renderWithClient(<DataPathDiagnosticsPage onOpenTrends={onOpenTrends} />);

        await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
        const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
        expect(workloadSection).toBeTruthy();
        await waitFor(() => expect(within(workloadSection as HTMLElement).queryByText(/Loading workload-path diagnostics/i)).not.toBeInTheDocument());
        const openButton = within(workloadSection as HTMLElement).getAllByRole('button', { name: 'Open trends' })[0];
        await userEvent.click(openButton);

        expect(onOpenTrends).toHaveBeenCalledWith(expect.objectContaining({
            collectorId: 'collector-a',
            category: 'network',
            metricKey: 'network_rx_bytes_per_second',
            triggerLabel: 'Workload path (ml/trainer-a)',
        }));
    });

    it('hydrates workload filters from URL and passes them to API query', async () => {
        window.history.replaceState({}, '', '/?workload_cluster=cluster-a&workload_namespace=ml&workload_service=trainer');
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalledWith(expect.objectContaining({
            cluster: 'cluster-a',
            namespace: 'ml',
            service: 'trainer',
            limit: 30,
        })));
    });

    it('hydrates workload sort controls from URL and applies ordering', async () => {
        fetchWorkloadPathDiagnosticsMock.mockResolvedValueOnce({
            cluster: '',
            namespace: '',
            service: '',
            generated_at: '2026-02-19T00:00:00Z',
            summary: {
                workload_count: 2,
                critical_workloads: 1,
                degraded_workloads: 1,
                telemetry_covered_workloads: 2,
                multi_node_workloads: 1,
                gpu_starvation_risk_workloads: 1,
                communication_imbalance_workloads: 1,
                top_bottleneck: 'network',
            },
            workloads: [
                {
                    cluster: 'cluster-a',
                    namespace: 'ml',
                    kind: 'StatefulSet',
                    name: 'trainer-a',
                    service: 'trainer',
                    pods_total: 8,
                    pods_running: 8,
                    pods_pending: 0,
                    pods_failed: 0,
                    container_restarts: 2,
                    node_count: 2,
                    resolved_nodes: 2,
                    telemetry_coverage_percent: 50,
                    compute_score: 1.6,
                    network_score: 5.3,
                    storage_score: 3.8,
                    overall_score: 5.3,
                    severity: 'critical',
                    bottleneck: 'network',
                },
                {
                    cluster: 'cluster-a',
                    namespace: 'ml',
                    kind: 'Deployment',
                    name: 'trainer-b',
                    service: 'trainer',
                    pods_total: 8,
                    pods_running: 8,
                    pods_pending: 0,
                    pods_failed: 0,
                    container_restarts: 0,
                    node_count: 1,
                    resolved_nodes: 1,
                    telemetry_coverage_percent: 95,
                    compute_score: 1.0,
                    network_score: 2.1,
                    storage_score: 1.4,
                    overall_score: 2.1,
                    severity: 'degraded',
                    bottleneck: 'network',
                },
            ],
        });
        window.history.replaceState({}, '', '/?workload_sort_key=network&workload_sort_direction=asc');
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
        const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
        expect(workloadSection).toBeTruthy();
        await waitFor(() => expect(within(workloadSection as HTMLElement).queryByText(/Loading workload-path diagnostics/i)).not.toBeInTheDocument());
        expect(within(workloadSection as HTMLElement).getByLabelText('Workload sort key')).toHaveValue('network');
        expect(within(workloadSection as HTMLElement).getByRole('button', { name: 'Ascending' })).toBeInTheDocument();

        const rows = within(workloadSection as HTMLElement).getAllByRole('row');
        expect(rows[1]).toHaveTextContent('ml/trainer-b');
    });

    it('persists workload filters into URL on apply and clears on reset', async () => {
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
        const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
        expect(workloadSection).toBeTruthy();
        const clusterInput = within(workloadSection as HTMLElement).getByPlaceholderText('Cluster');
        const namespaceInput = within(workloadSection as HTMLElement).getByPlaceholderText('Namespace');
        const serviceInput = within(workloadSection as HTMLElement).getByPlaceholderText('Service');

        await act(async () => {
            fireEvent.change(clusterInput, { target: { value: 'cluster-z' } });
            fireEvent.change(namespaceInput, { target: { value: 'serving' } });
            fireEvent.change(serviceInput, { target: { value: 'inference' } });
            fireEvent.click(within(workloadSection as HTMLElement).getByRole('button', { name: 'Apply' }));
        });

        expect(window.location.search).toContain('workload_cluster=cluster-z');
        expect(window.location.search).toContain('workload_namespace=serving');
        expect(window.location.search).toContain('workload_service=inference');

        await act(async () => {
            fireEvent.click(within(workloadSection as HTMLElement).getByRole('button', { name: 'Reset' }));
        });
        expect(window.location.search).not.toContain('workload_cluster=');
        expect(window.location.search).not.toContain('workload_namespace=');
        expect(window.location.search).not.toContain('workload_service=');
    });

    it('expands workload rows to show per-node mapping details', async () => {
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
        const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
        expect(workloadSection).toBeTruthy();
        await waitFor(() => expect(within(workloadSection as HTMLElement).queryByText(/Loading workload-path diagnostics/i)).not.toBeInTheDocument());
        expect(within(workloadSection as HTMLElement).queryByText(/Per-node path mapping/i)).not.toBeInTheDocument();

        await act(async () => {
            fireEvent.click(within(workloadSection as HTMLElement).getByRole('button', { name: 'Show details' }));
        });

        expect(within(workloadSection as HTMLElement).getByText(/Per-node path mapping/i)).toBeInTheDocument();
        expect(within(workloadSection as HTMLElement).getByText(/collector collector-a/i)).toBeInTheDocument();
        expect(within(workloadSection as HTMLElement).getByText(/sources .*\/proc\/net\/snmp/i)).toBeInTheDocument();
        expect(within(workloadSection as HTMLElement).getByText(/Queue pressure is rising in RDMA path/i)).toBeInTheDocument();
    });

    it('shows scope-link status when clipboard is unavailable', async () => {
        const originalClipboard = Object.getOwnPropertyDescriptor(window.navigator, 'clipboard');
        Object.defineProperty(window.navigator, 'clipboard', { value: undefined, configurable: true });
        try {
            renderWithClient(<DataPathDiagnosticsPage />);
            await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
            const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
            expect(workloadSection).toBeTruthy();
            await act(async () => {
                fireEvent.click(within(workloadSection as HTMLElement).getByRole('button', { name: 'Copy scope link' }));
            });
            expect(within(workloadSection as HTMLElement).getByText(/Scope link clipboard unavailable/i)).toBeInTheDocument();
        } finally {
            if (originalClipboard) {
                Object.defineProperty(window.navigator, 'clipboard', originalClipboard);
            } else {
                delete (window.navigator as unknown as { clipboard?: unknown }).clipboard;
            }
        }
    });

    it('sorts workload rows by selected sort key and direction', async () => {
        fetchWorkloadPathDiagnosticsMock.mockResolvedValueOnce({
            cluster: '',
            namespace: '',
            service: '',
            generated_at: '2026-02-19T00:00:00Z',
            summary: {
                workload_count: 2,
                critical_workloads: 1,
                degraded_workloads: 1,
                telemetry_covered_workloads: 2,
                multi_node_workloads: 1,
                gpu_starvation_risk_workloads: 1,
                communication_imbalance_workloads: 1,
                top_bottleneck: 'network',
            },
            workloads: [
                {
                    cluster: 'cluster-a',
                    namespace: 'ml',
                    kind: 'StatefulSet',
                    name: 'trainer-a',
                    service: 'trainer',
                    pods_total: 8,
                    pods_running: 8,
                    pods_pending: 0,
                    pods_failed: 0,
                    container_restarts: 2,
                    node_count: 2,
                    resolved_nodes: 2,
                    telemetry_coverage_percent: 50,
                    compute_score: 1.6,
                    network_score: 5.3,
                    storage_score: 3.8,
                    overall_score: 5.3,
                    severity: 'critical',
                    bottleneck: 'network',
                },
                {
                    cluster: 'cluster-a',
                    namespace: 'ml',
                    kind: 'Deployment',
                    name: 'trainer-b',
                    service: 'trainer',
                    pods_total: 8,
                    pods_running: 8,
                    pods_pending: 0,
                    pods_failed: 0,
                    container_restarts: 0,
                    node_count: 1,
                    resolved_nodes: 1,
                    telemetry_coverage_percent: 95,
                    compute_score: 1.0,
                    network_score: 2.1,
                    storage_score: 1.4,
                    overall_score: 2.1,
                    severity: 'degraded',
                    bottleneck: 'network',
                },
            ],
        });
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
        const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
        expect(workloadSection).toBeTruthy();
        await waitFor(() => expect(within(workloadSection as HTMLElement).queryByText(/Loading workload-path diagnostics/i)).not.toBeInTheDocument());

        let rows = within(workloadSection as HTMLElement).getAllByRole('row');
        expect(rows[1]).toHaveTextContent('ml/trainer-a');

        await act(async () => {
            fireEvent.change(within(workloadSection as HTMLElement).getByLabelText('Workload sort key'), { target: { value: 'coverage' } });
        });
        expect(window.location.search).toContain('workload_sort_key=coverage');
        expect(window.location.search).not.toContain('workload_sort_direction=');
        await waitFor(() => {
            rows = within(workloadSection as HTMLElement).getAllByRole('row');
            expect(rows[1]).toHaveTextContent('ml/trainer-b');
        });

        await act(async () => {
            fireEvent.click(within(workloadSection as HTMLElement).getByRole('button', { name: 'Descending' }));
        });
        expect(window.location.search).toContain('workload_sort_key=coverage');
        expect(window.location.search).toContain('workload_sort_direction=asc');
        await waitFor(() => {
            rows = within(workloadSection as HTMLElement).getAllByRole('row');
            expect(rows[1]).toHaveTextContent('ml/trainer-a');
        });
    });

    it('copies workload handoff markdown to clipboard', async () => {
        const writeText = vi.fn().mockResolvedValue(undefined);
        const originalClipboard = Object.getOwnPropertyDescriptor(window.navigator, 'clipboard');
        Object.defineProperty(window.navigator, 'clipboard', {
            value: { writeText },
            configurable: true,
        });
        try {
            renderWithClient(<DataPathDiagnosticsPage />);
            await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
            const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
            expect(workloadSection).toBeTruthy();
            await act(async () => {
                fireEvent.click(within(workloadSection as HTMLElement).getByRole('button', { name: 'Copy handoff markdown' }));
            });
            await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
            expect(String(writeText.mock.calls[0][0])).toContain('# Workload Path Handoff');
            expect(within(workloadSection as HTMLElement).getByText(/Handoff markdown copied/i)).toBeInTheDocument();
        } finally {
            if (originalClipboard) {
                Object.defineProperty(window.navigator, 'clipboard', originalClipboard);
            } else {
                delete (window.navigator as unknown as { clipboard?: unknown }).clipboard;
            }
        }
    });

    it('copies combined RCA packet markdown to clipboard', async () => {
        const writeText = vi.fn().mockResolvedValue(undefined);
        const originalClipboard = Object.getOwnPropertyDescriptor(window.navigator, 'clipboard');
        Object.defineProperty(window.navigator, 'clipboard', {
            value: { writeText },
            configurable: true,
        });
        try {
            renderWithClient(<DataPathDiagnosticsPage />);
            await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
            await waitFor(() => expect(fetchRootCauseDiagnosticsMock).toHaveBeenCalled());
            const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
            expect(workloadSection).toBeTruthy();
            await act(async () => {
                fireEvent.click(within(workloadSection as HTMLElement).getByRole('button', { name: 'Copy RCA packet' }));
            });
            await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
            const payload = String(writeText.mock.calls[0][0]);
            expect(payload).toContain('# AI SRE RCA Packet');
            expect(payload).toContain('## Root Cause Summary');
            expect(payload).toContain('## Kernel Path Snapshot');
            expect(payload).toContain('## Resource Pressure Snapshot');
            expect(payload).toContain('# Workload Path Handoff');
            expect(fetchRCAPacketExportMock).toHaveBeenCalledWith(expect.objectContaining({
                sortKey: 'severity',
                sortDirection: 'desc',
                workloadLimit: 30,
            }));
            expect(within(workloadSection as HTMLElement).getByText(/RCA packet copied/i)).toBeInTheDocument();
        } finally {
            if (originalClipboard) {
                Object.defineProperty(window.navigator, 'clipboard', originalClipboard);
            } else {
                delete (window.navigator as unknown as { clipboard?: unknown }).clipboard;
            }
        }
    });

    it('downloads combined RCA packet markdown', async () => {
        const createObjectUrl = vi.fn().mockReturnValue('blob:rca-packet');
        const revokeObjectUrl = vi.fn();
        const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
        const originalCreateObjectURL = Object.getOwnPropertyDescriptor(URL, 'createObjectURL');
        const originalRevokeObjectURL = Object.getOwnPropertyDescriptor(URL, 'revokeObjectURL');
        Object.defineProperty(URL, 'createObjectURL', {
            value: createObjectUrl,
            configurable: true,
        });
        Object.defineProperty(URL, 'revokeObjectURL', {
            value: revokeObjectUrl,
            configurable: true,
        });
        try {
            renderWithClient(<DataPathDiagnosticsPage />);
            await waitFor(() => expect(fetchWorkloadPathDiagnosticsMock).toHaveBeenCalled());
            await waitFor(() => expect(fetchRootCauseDiagnosticsMock).toHaveBeenCalled());
            const workloadSection = screen.getByText('Kubernetes Workload Path Diagnostics').closest('section');
            expect(workloadSection).toBeTruthy();

            await act(async () => {
                fireEvent.click(within(workloadSection as HTMLElement).getByRole('button', { name: 'Download RCA packet' }));
            });

            expect(createObjectUrl).toHaveBeenCalledTimes(1);
            expect(createObjectUrl.mock.calls[0][0]).toBeInstanceOf(Blob);
            expect(fetchRCAPacketExportMock).toHaveBeenCalledWith(expect.objectContaining({
                sortKey: 'severity',
                sortDirection: 'desc',
                workloadLimit: 30,
            }));
            expect(clickSpy).toHaveBeenCalledTimes(1);
            expect(revokeObjectUrl).toHaveBeenCalledWith('blob:rca-packet');
            expect(within(workloadSection as HTMLElement).getByText(/RCA packet downloaded/i)).toBeInTheDocument();
        } finally {
            clickSpy.mockRestore();
            if (originalCreateObjectURL) {
                Object.defineProperty(URL, 'createObjectURL', originalCreateObjectURL);
            } else {
                delete (URL as unknown as { createObjectURL?: unknown }).createObjectURL;
            }
            if (originalRevokeObjectURL) {
                Object.defineProperty(URL, 'revokeObjectURL', originalRevokeObjectURL);
            } else {
                delete (URL as unknown as { revokeObjectURL?: unknown }).revokeObjectURL;
            }
        }
    });

    it('disables drilldown actions when no trends callback is provided', async () => {
        renderWithClient(<DataPathDiagnosticsPage />);

        await waitFor(() => expect(screen.getAllByRole('button', { name: 'Open trends' }).length).toBeGreaterThan(0));

        const firstOpenButton = screen.getAllByRole('button', { name: 'Open trends' })[0];
        expect(firstOpenButton).toBeDisabled();
    });

    it('renders runtime mode context for constrained collectors', async () => {
        renderWithClient(<DataPathDiagnosticsPage />);

        expect(await screen.findByText(/Runtime degraded: 1/i)).toBeInTheDocument();
        expect(screen.getByText('namespace')).toBeInTheDocument();
        expect(screen.getByText(/host_pid_namespace_unavailable/i)).toBeInTheDocument();
    });
});
