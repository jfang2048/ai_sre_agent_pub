import React from 'react';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import OperationsControlPanel from '../OperationsControlPanel';
import {
    fetchFinOpsSignals,
    fetchHAStatus,
    fetchStorageStatus,
    updateStorageRetention,
} from '@/api/controlPlane';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/controlPlane', () => ({
    fetchHAStatus: vi.fn(),
    fetchStorageStatus: vi.fn(),
    fetchFinOpsSignals: vi.fn(),
    updateStorageRetention: vi.fn(),
}));

const fetchHAStatusMock = vi.mocked(fetchHAStatus);
const fetchStorageStatusMock = vi.mocked(fetchStorageStatus);
const fetchFinOpsSignalsMock = vi.mocked(fetchFinOpsSignals);
const updateStorageRetentionMock = vi.mocked(updateStorageRetention);

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
        await waitFor(() => expect(screen.getByText(/Degraded: tsdb unreachable/i)).toBeInTheDocument());
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
