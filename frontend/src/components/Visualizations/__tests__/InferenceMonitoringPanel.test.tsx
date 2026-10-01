import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import InferenceMonitoringPanel from '../InferenceMonitoringPanel';
import {
    fetchInferenceOverview,
    InferenceEndpoint,
    InferenceGPU,
    InferenceMeasurement,
    InferenceModel,
    InferenceOverview,
    MonitoringFinding,
} from '@/api/inference';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/inference', () => ({ fetchInferenceOverview: vi.fn() }));

const fetchOverview = vi.mocked(fetchInferenceOverview);
const observedAt = '2026-10-01T12:00:00Z';
const advisory: MonitoringFinding = {
    code: 'queue_pressure',
    severity: 'warning',
    summary: 'Requests are waiting for execution.',
    recommendation: 'Compare queue depth with throughput before adjusting capacity.',
    evidence: [`requests_waiting=12 requests observed_at=${observedAt}`],
};

function reading(value: number, unit = 'requests'): InferenceMeasurement {
    return { value, unit, observed_at: observedAt };
}

function model(overrides: Partial<InferenceModel> = {}): InferenceModel {
    return {
        name: 'example-model', engine: '0', observed_at: observedAt, status: 'fresh',
        metrics: { requests_waiting: reading(12) }, findings: [advisory], ...overrides,
    };
}

function endpoint(overrides: Partial<InferenceEndpoint> = {}): InferenceEndpoint {
    return {
        collector_id: 'node-a', hostname: 'host-a', endpoint: 'serving', status: 'fresh',
        last_attempt_at: observedAt, last_success_at: observedAt, models: [model()], ...overrides,
    };
}

function gpu(overrides: Partial<InferenceGPU> = {}): InferenceGPU {
    return {
        collector_id: 'node-a', hostname: 'host-a', gpu_id: 'A', observed_at: observedAt,
        status: 'fresh', metrics: { memory_used_percent: reading(0, 'percent') }, findings: [], ...overrides,
    };
}

function report(overrides: Partial<InferenceOverview> = {}): InferenceOverview {
    return {
        generated_at: observedAt, stale_after_seconds: 120, capacity_limited: false,
        endpoints: [endpoint()], gpus: [], ...overrides,
    };
}

function metricCard(label: string) {
    const card = screen.getByText(label).closest('div');
    if (!card) throw new Error(`Missing metric card: ${label}`);
    return within(card);
}

describe('InferenceMonitoringPanel', () => {
    beforeEach(() => {
        fetchOverview.mockReset();
        fetchOverview.mockResolvedValue(report());
    });

    it('keeps measured zero distinct from absent or invalid measurements', async () => {
        fetchOverview.mockResolvedValue(report({ endpoints: [endpoint({ models: [model({
            metrics: {
                requests_waiting: reading(0),
                kv_cache_utilization_percent: reading(0, 'percent'),
                generation_tokens_per_second: reading(NaN, 'tokens/s'),
                ttft_mean_seconds: { ...reading(42, 'seconds'), observed_at: 'invalid' },
            }, findings: [],
        })] })] }));

        renderWithClient(<InferenceMonitoringPanel />);

        await screen.findByLabelText('Serving target');
        expect(metricCard('Waiting requests').getByText('0 requests')).toBeInTheDocument();
        expect(metricCard('KV-cache occupancy').getByText('0 %')).toBeInTheDocument();
        // This jsdom/ARIA query version does not map native meter to its role.
        const meter = screen.getByLabelText('KV-cache occupancy');
        expect(meter.tagName).toBe('METER');
        expect(meter).toHaveAttribute('value', '0');
        for (const label of ['First-token delay · interval mean', 'Generation throughput', 'Running requests']) {
            expect(metricCard(label).getByText('Unavailable')).toBeInTheDocument();
            expect(metricCard(label).getByText('Not measured in the current window')).toBeInTheDocument();
        }
        expect(screen.queryByText(/NaN|Infinity/)).not.toBeInTheDocument();
        expect(screen.getByText(/Missing signals are not a health verdict/)).toBeInTheDocument();
    });

    it('shows loading without an empty-target verdict until the request finishes', async () => {
        let resolve!: (value: InferenceOverview) => void;
        fetchOverview.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
        renderWithClient(<InferenceMonitoringPanel />);

        expect(screen.getByRole('status')).toHaveTextContent('Loading serving observations');
        expect(screen.getByRole('button', { name: 'Refresh observations' })).toBeDisabled();
        expect(screen.queryByText(/No serving targets observed/)).not.toBeInTheDocument();

        await act(async () => { resolve(report()); });
        expect(await screen.findByLabelText('Serving target')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Refresh observations' })).toBeEnabled();
    });

    it('recovers from an API failure through the retry control', async () => {
        fetchOverview.mockRejectedValueOnce(new Error('request failed')).mockResolvedValueOnce(report());
        renderWithClient(<InferenceMonitoringPanel />);

        expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load monitoring evidence');
        expect(screen.queryByLabelText('Serving target')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Retry monitoring' }));

        expect(await screen.findByLabelText('Serving target')).toBeInTheDocument();
        expect(fetchOverview).toHaveBeenCalledTimes(2);
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('hides retained readings after a failed refresh instead of presenting them as current', async () => {
        fetchOverview.mockResolvedValueOnce(report()).mockRejectedValueOnce(new Error('refresh failed'));
        renderWithClient(<InferenceMonitoringPanel />);

        expect(await screen.findByText('12 requests')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Refresh observations' }));

        expect(await screen.findByRole('alert')).toHaveTextContent('Retained readings are not shown as current');
        expect(screen.queryByText('12 requests')).not.toBeInTheDocument();
        expect(screen.queryByText(advisory.recommendation)).not.toBeInTheDocument();
    });

    it.each(['stale', 'unavailable'] as const)('labels %s target readings as historical and suppresses its model advisories', async (status) => {
        fetchOverview.mockResolvedValue(report({ endpoints: [endpoint({ status })] }));
        renderWithClient(<InferenceMonitoringPanel />);

        expect(await screen.findByText(/retained readings below are historical, not current health/)).toBeInTheDocument();
        expect(screen.getByText('12 requests')).toBeInTheDocument();
        expect(screen.queryByText(advisory.recommendation)).not.toBeInTheDocument();
        expect(screen.queryByText(/No advisory triggered/)).not.toBeInTheDocument();
    });

    it('does not let a reachable endpoint make a stale model advisory current', async () => {
        fetchOverview.mockResolvedValue(report({ endpoints: [endpoint({ models: [model({ status: 'stale' })] })] }));
        renderWithClient(<InferenceMonitoringPanel />);

        expect(await screen.findByText('This model has stale observations even if the target is reachable.')).toBeInTheDocument();
        expect(screen.getByText('12 requests')).toBeInTheDocument();
        expect(screen.queryByText(advisory.recommendation)).not.toBeInTheDocument();
    });

    it('keeps target and engine selection available without GPUs and resets the model on target change', async () => {
        fetchOverview.mockResolvedValue(report({ endpoints: [
            endpoint({ models: [model({ engine: '0' }), model({ engine: '1', metrics: { requests_waiting: reading(23) }, findings: [] })] }),
            endpoint({ collector_id: 'node-b', hostname: 'host-b', models: [model({ name: 'other-model', engine: '9', metrics: { requests_waiting: reading(34) }, findings: [] })] }),
        ] }));
        renderWithClient(<InferenceMonitoringPanel collectorId="gpu-only-node" />);

        const targetSelect = await screen.findByRole('combobox', { name: 'Serving target' });
        const modelSelect = screen.getByRole('combobox', { name: 'Model and engine' });
        expect(targetSelect).toHaveValue(JSON.stringify(['node-a', 'serving']));
        expect(screen.getByText('No GPU observations in this scope.')).toBeInTheDocument();
        fireEvent.change(modelSelect, { target: { value: JSON.stringify(['example-model', '1']) } });
        expect(screen.getByText('23 requests')).toBeInTheDocument();
        expect(screen.queryByText('12 requests')).not.toBeInTheDocument();

        fireEvent.change(targetSelect, { target: { value: JSON.stringify(['node-b', 'serving']) } });
        expect(screen.getByRole('combobox', { name: 'Model and engine' })).toHaveValue(JSON.stringify(['other-model', '9']));
        expect(screen.getByText('34 requests')).toBeInTheDocument();
        expect(fetchOverview).toHaveBeenCalledWith();
    });

    it('scopes GPU evidence to the selected serving collector and excludes stale GPU advisories', async () => {
        fetchOverview.mockResolvedValue(report({
            endpoints: [endpoint(), endpoint({ collector_id: 'node-b', hostname: 'host-b' })],
            gpus: [gpu(), gpu({ collector_id: 'node-b', hostname: 'host-b', gpu_id: 'B', status: 'stale', findings: [{ ...advisory, summary: 'Stale GPU warning', recommendation: 'Old GPU recommendation' }] })],
        }));
        renderWithClient(<InferenceMonitoringPanel collectorId="node-b" />);

        const targetSelect = await screen.findByRole('combobox', { name: 'Serving target' });
        expect(targetSelect).toHaveValue(JSON.stringify(['node-b', 'serving']));
        expect(screen.getByRole('heading', { name: 'GPU B · Stale observations' })).toBeInTheDocument();
        expect(screen.queryByRole('heading', { name: /GPU A ·/ })).not.toBeInTheDocument();
        expect(screen.queryByText('Old GPU recommendation')).not.toBeInTheDocument();
        expect(screen.getByText(/not proof that a model uses a particular GPU/)).toBeInTheDocument();

        fireEvent.change(targetSelect, { target: { value: JSON.stringify(['node-a', 'serving']) } });
        expect(screen.getByRole('heading', { name: 'GPU A · Recent observations' })).toBeInTheDocument();
        expect(screen.queryByRole('heading', { name: /GPU B ·/ })).not.toBeInTheDocument();
        expect(metricCard('Allocated GPU memory').getByText('0 %')).toBeInTheDocument();
    });

    it('still shows scoped GPU measurements when no serving endpoint has been observed', async () => {
        fetchOverview.mockResolvedValue(report({ endpoints: [], gpus: [gpu(), gpu({ collector_id: 'node-b', gpu_id: 'B' })] }));
        renderWithClient(<InferenceMonitoringPanel collectorId="node-b" />);

        expect(await screen.findByText(/No serving targets observed/)).toBeInTheDocument();
        expect(screen.getByRole('heading', { name: 'GPU B · Recent observations' })).toBeInTheDocument();
        expect(screen.queryByRole('heading', { name: /GPU A ·/ })).not.toBeInTheDocument();
        expect(screen.queryByRole('combobox', { name: 'Serving target' })).not.toBeInTheDocument();
    });

    it('exposes the measured evidence and collection limit alongside actionable advisories', async () => {
        fetchOverview.mockResolvedValue(report({ capacity_limited: true, endpoints: [endpoint({ models: [model({ metrics: {
            requests_waiting: reading(12),
            ttft_mean_seconds: { ...reading(2.5, 'seconds'), window_seconds: 30 },
        } })] })] }));
        renderWithClient(<InferenceMonitoringPanel />);

        expect(await screen.findByText(/Monitoring capacity was reached/)).toBeInTheDocument();
        expect(screen.getByText(advisory.recommendation)).toBeInTheDocument();
        expect(metricCard('First-token delay · interval mean').getByText('2.5 s')).toBeInTheDocument();
        expect(metricCard('First-token delay · interval mean').getByText(/30s interval/)).toBeInTheDocument();
        expect(screen.getByText(/Means are not p95/)).toBeInTheDocument();
        const evidenceToggle = screen.getByText('Inspect measured evidence');
        fireEvent.click(evidenceToggle);
        await waitFor(() => expect(evidenceToggle.closest('details')).toHaveAttribute('open'));
        expect(screen.getByText(advisory.evidence[0])).toBeVisible();
    });
});
