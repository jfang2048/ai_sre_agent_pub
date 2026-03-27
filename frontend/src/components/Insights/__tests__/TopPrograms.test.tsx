import React from 'react';
import { describe, expect, it } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import TopProgramsPanel from '../TopPrograms';
import { renderWithClient } from '@/test/utils';

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

describe('TopProgramsPanel', () => {
    it('renders rows with program names and metrics', async () => {
        renderWithClient(<TopProgramsPanel />);

        await waitFor(() => {
            expect(screen.getAllByText('cpu-burn').length).toBeGreaterThan(0);
            expect(screen.getAllByText('net-hog').length).toBeGreaterThan(0);
        });

        expect(screen.getByText(/92\.3%/)).toBeInTheDocument();
        expect(screen.getAllByText(/1.1 GB/).length).toBeGreaterThan(0);
    });

    it('shows category chips for resource classes', async () => {
        renderWithClient(<TopProgramsPanel />);
        await waitFor(() => expect(screen.getAllByText('cpu-burn').length).toBeGreaterThan(0));

        expect(screen.getAllByText(/^cpu$/i).length).toBeGreaterThan(0);
        expect(screen.getAllByText(/^network$/i).length).toBeGreaterThan(0);
    });
});
