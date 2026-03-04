import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import OperationsControlPanel from '../OperationsControlPanel';
import {
    fetchFinOpsSignals,
    fetchHAStatus,
    fetchStorageStatus,
    updateStorageRetention,
} from '@/api/controlPlane';

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

function renderWithClient(ui: React.ReactElement) {
    const client = new QueryClient({
        defaultOptions: {
            queries: { retry: false },
        },
    });
    return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

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
    });
});
