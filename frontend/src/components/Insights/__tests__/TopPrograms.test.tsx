import React from 'react';
import { describe, expect, it } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import TopProgramsPanel from '../TopPrograms';

// Mock API module
vi.mock('@/api/topPrograms', () => ({
    fetchTopPrograms: vi.fn().mockResolvedValue({
        generated_at: '2025-02-04T00:00:00Z',
        limit: 15,
        count: 2,
        programs: [
            {
                collector_id: 'c-1',
                hostname: 'node-1',
                pid: '100',
                name: 'cpu-burn',
                cpu_percent: 92.3,
                memory_bytes: 1_200_000_000,
                score: 6.2,
                categories: ['cpu', 'memory'],
            },
            {
                collector_id: 'c-1',
                hostname: 'node-1',
                pid: '200',
                name: 'net-hog',
                net_bytes_per_second: 50_000_000,
                net_connections: 80,
                score: 5.0,
                categories: ['network'],
            },
        ],
    }),
}));

function renderWithClient(ui: React.ReactElement) {
    const client = new QueryClient();
    return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe('TopProgramsPanel', () => {
    it('renders rows with program names and metrics', async () => {
        renderWithClient(<TopProgramsPanel />);

        await waitFor(() => {
            expect(screen.getByText('cpu-burn')).toBeInTheDocument();
            expect(screen.getByText('net-hog')).toBeInTheDocument();
        });

        expect(screen.getByText(/92\.3%/)).toBeInTheDocument();
        expect(screen.getByText(/1.1 GB/)).toBeInTheDocument();
    });

    it('shows category chips for resource classes', async () => {
        renderWithClient(<TopProgramsPanel />);
        await waitFor(() => screen.getByText('cpu-burn'));

        expect(screen.getByText(/cpu/i)).toBeInTheDocument();
        expect(screen.getByText(/network/i)).toBeInTheDocument();
    });
});
