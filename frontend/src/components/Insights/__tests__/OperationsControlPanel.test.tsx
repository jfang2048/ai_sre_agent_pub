import React from 'react';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import OperationsControlPanel from '../OperationsControlPanel';
import {
    fetchAgentStatus,
    fetchControllerStatus,
    fetchFinOpsSignals,
    fetchHAStatus,
    fetchStorageStatus,
    updateStorageRetention,
} from '@/api/controlPlane';
import { fetchRAGStatus } from '@/api/rag';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/controlPlane', () => ({
    fetchAgentStatus: vi.fn(),
    fetchControllerStatus: vi.fn(),
    fetchHAStatus: vi.fn(),
    fetchStorageStatus: vi.fn(),
    fetchFinOpsSignals: vi.fn(),
    updateStorageRetention: vi.fn(),
}));

vi.mock('@/api/rag', () => ({
    fetchRAGStatus: vi.fn(),
}));

const fetchAgentStatusMock = vi.mocked(fetchAgentStatus);
const fetchControllerStatusMock = vi.mocked(fetchControllerStatus);
const fetchHAStatusMock = vi.mocked(fetchHAStatus);
const fetchStorageStatusMock = vi.mocked(fetchStorageStatus);
const fetchFinOpsSignalsMock = vi.mocked(fetchFinOpsSignals);
const updateStorageRetentionMock = vi.mocked(updateStorageRetention);
const fetchRAGStatusMock = vi.mocked(fetchRAGStatus);

describe('OperationsControlPanel', () => {
    beforeEach(() => {
        fetchHAStatusMock.mockResolvedValue({
            enabled: true,
            mode: 'active',
            active: true,
            read_only: false,
            timestamp: '2026-02-28T00:00:00Z',
        });
        fetchStorageStatusMock.mockResolvedValue({
            storage: {
                nodes: 3,
                history_series: 3,
                history_samples: 90,
                node_retention: '24h0m0s',
                history_samples_per_node: 1440,
                max_nodes: 5000,
                persistence: {
                    enabled: true,
                    current_db_bytes: 1234567,
                    compactions: 2,
                },
            },
            tsdb: {
                enabled: true,
                provider: 'influxdb',
                mode: 'memory-fallback',
                ready: true,
                healthy: false,
                fallback_to_memory: true,
                fallback_active: true,
                manage_bucket: false,
                endpoint: 'http://influxdb:8086',
                org: 'ai-sre-agent',
                bucket: 'controller_metrics',
                retention: '168h0m0s',
                flush_interval: '2s',
                query_timeout: '5s',
                health_interval: '30s',
                backup_directory: '/var/backups/influx',
                degraded_reason: 'tsdb unreachable',
            },
            timestamp: '2026-02-28T00:00:00Z',
        });
        fetchControllerStatusMock.mockResolvedValue({
            version: 'v0.7',
            uptime: 'running',
            total_nodes: 3,
            healthy_nodes: 3,
            scrape_interval: '15s',
            listen_address: ':8080',
            collector_coverage: {
                state: 'degraded',
                total_collectors: 3,
                fresh_collectors: 2,
                delayed_collectors: 0,
                stale_collectors: 1,
                degraded_collectors: 0,
                partial_collectors: 1,
                fallback_collectors: 1,
                backlog_collectors: 1,
                coverage_percent: 73,
                quality_hint: 'Fleet coverage is degraded: some collectors are partial, lagging, or running in fallback mode.',
            },
        });
        fetchRAGStatusMock.mockResolvedValue({
            enabled: true,
            ready: false,
            dataset_path: './dataset',
            index_path: './data/agent/rag/index.json',
            storage_path: './data/agent/rag',
            cache_path: './data/agent/rag/cache',
            doc_count: 0,
            chunk_count: 0,
            source_count: 0,
            quarantine_count: 0,
            retrieval_mode: 'hybrid',
            embedding_provider: 'local',
            chunk_size: 900,
            chunk_overlap: 120,
            max_snippet_len: 1000,
            last_error: 'index not ready',
        });
        fetchAgentStatusMock.mockResolvedValue({
            status: 'active',
            reports: 1,
            actions: 0,
            joint_risk_reports: 1,
            potential_risk_findings: 1,
            rca_workflow_reports: 1,
            timestamp: '2026-02-28T00:00:00Z',
            report_engine: {
                enabled: true,
                suppress_unchanged_reports: true,
                report_refresh_interval: '3m0s',
                predictive_log_cooldown: '5m0s',
                reports_stored: 1,
                actions_stored: 0,
                report_suppressed_total: 7,
                report_refreshed_total: 7,
                predictive_log_suppressed_total: 4,
            },
            query_service: {
                enabled: true,
                analysis_mode: 'deterministic_only',
                provider: 'mock',
                model: 'deterministic',
                dry_run: true,
                require_approval_token: true,
                rag_attached: true,
                skip_llm_on_stale_telemetry: true,
                skip_llm_on_no_telemetry: true,
                max_telemetry_age: '2m0s',
                metrics: {
                    StaleTelemetryTotal: 2,
                    LLMFailuresTotal: 1,
                    LLMBypassedStaleTotal: 3,
                    LLMBypassedEmptyTotal: 1,
                    FallbackTotal: 4,
                    ActionsSuppressedTotal: 2,
                    AnalysisReusedTotal: 0,
                    RAGSkippedContextTotal: 0,
                },
            },
        });
        fetchFinOpsSignalsMock.mockResolvedValue({
            summary: {
                nodes_analyzed: 2,
                idle_cpu_hints: 1,
                oversized_memory_hints: 1,
                gpu_waste_hints: 0,
                average_waste_score: 0.52,
            },
            count: 2,
            generated_at: '2026-02-28T00:00:00Z',
            nodes: [
                {
                    collector_id: 'collector-a',
                    hostname: 'node-a',
                    cpu_usage_percent: 12,
                    memory_usage_percent: 22,
                    gpu_utilization_percent: 18,
                    gpu_processes: 1,
                    idle_cpu_hint: true,
                    oversized_memory_hint: true,
                    gpu_waste_hint: false,
                    potential_waste_score: 0.7,
                },
            ],
        });
        updateStorageRetentionMock.mockResolvedValue({
            storage: {
                nodes: 3,
                history_series: 3,
                history_samples: 90,
                node_retention: '48h0m0s',
                history_samples_per_node: 2000,
                max_nodes: 5000,
                persistence: { enabled: true },
            },
            timestamp: '2026-02-28T00:00:00Z',
        });
    });

    it('renders ha, retention, and finops data', async () => {
        renderWithClient(<OperationsControlPanel />);

        await waitFor(() => expect(screen.getByText(/Retention, HA, and FinOps Control/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/^active$/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/persistence on/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/Health poll: 30s/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/2\/3 fresh · 73% coverage/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/Degraded: tsdb unreachable/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/memory fallback active/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/index not ready/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getAllByText(/deterministic-only/i).length).toBeGreaterThan(0));
        await waitFor(() => expect(screen.getByText(/Collector coverage is degraded; some nodes are stale, partial, or replaying backlog/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/AGENT query service is running in deterministic-only mode/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/reports suppressed 7/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByText(/node-a/i)).toBeInTheDocument());
    });

    it('submits retention updates', async () => {
        renderWithClient(<OperationsControlPanel />);
        await waitFor(() => expect(screen.getByText(/Apply retention/i)).toBeInTheDocument());

        const retentionInput = screen.getByLabelText(/Node retention/i);
        const historyInput = screen.getByLabelText(/History samples per node/i);
        await waitFor(() => expect(retentionInput).toHaveValue('24h0m0s'));
        fireEvent.change(retentionInput, { target: { value: '48h' } });
        fireEvent.change(historyInput, { target: { value: '2000' } });
        fireEvent.click(screen.getByRole('button', { name: /Apply retention/i }));

        await waitFor(() => {
            expect(updateStorageRetentionMock).toHaveBeenCalledWith({
                node_retention: '48h',
                history_samples_per_node: 2000,
            });
        });
        await waitFor(() => expect(screen.getByText('Retention updated.')).toBeInTheDocument());
        await waitFor(() => expect(screen.getByLabelText(/Node retention/i)).toHaveValue('48h0m0s'));
        await waitFor(() => expect(screen.getByLabelText(/History samples per node/i)).toHaveValue('2000'));
    });

    it('surfaces retention update failures and re-enables the form', async () => {
        updateStorageRetentionMock.mockRejectedValueOnce(new Error('Retention update timed out.'));

        renderWithClient(<OperationsControlPanel />);
        await waitFor(() => expect(screen.getByText(/Apply retention/i)).toBeInTheDocument());

        const retentionInput = screen.getByLabelText(/Node retention/i);
        const historyInput = screen.getByLabelText(/History samples per node/i);
        const applyButton = screen.getByRole('button', { name: /Apply retention/i });

        fireEvent.change(retentionInput, { target: { value: '48h' } });
        fireEvent.change(historyInput, { target: { value: '300' } });
        fireEvent.click(applyButton);

        await waitFor(() => expect(screen.getByText('Retention update timed out.')).toBeInTheDocument());
        await waitFor(() => expect(applyButton).not.toBeDisabled());
        expect(retentionInput).not.toBeDisabled();
        expect(historyInput).not.toBeDisabled();
    });

    it('prefers API error payloads over generic transport messages', async () => {
        updateStorageRetentionMock.mockRejectedValueOnce({
            message: 'Request failed with status code 400',
            response: {
                data: {
                    error: 'history_samples_per_node must be > 0',
                },
            },
        });

        renderWithClient(<OperationsControlPanel />);
        await waitFor(() => expect(screen.getByText(/Apply retention/i)).toBeInTheDocument());

        fireEvent.change(screen.getByLabelText(/Node retention/i), { target: { value: '48h' } });
        fireEvent.change(screen.getByLabelText(/History samples per node/i), { target: { value: '0' } });
        fireEvent.click(screen.getByRole('button', { name: /Apply retention/i }));

        await waitFor(() => expect(screen.getByText('history_samples_per_node must be > 0')).toBeInTheDocument());
    });
});
