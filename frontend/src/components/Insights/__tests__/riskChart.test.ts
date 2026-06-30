import { describe, expect, it } from 'vitest';
import { buildPreferredRiskChartSeries, buildRiskChartData } from '../riskChart';
import type { RiskSeries } from '@/api/agentWorkflows';

const COLORS = ['#1', '#2', '#3', '#4'];

function makeSeries(key: string, display = key, points: Array<[string, number]> = []): RiskSeries {
    return {
        key,
        display,
        unit: '%',
        latest: 0,
        baseline: 0,
        acceleration: 0,
        points: points.map(([timestamp, value]) => ({ timestamp, value })),
    };
}

describe('riskChart helpers', () => {
    it('prefers configured series keys without repeated linear scans', () => {
        const lines = buildPreferredRiskChartSeries(
            [
                makeSeries('log_burst'),
                makeSeries('io_latency'),
                makeSeries('cpu_pressure'),
                makeSeries('memory_pressure'),
            ],
            ['cpu_pressure', 'memory_pressure', 'io_latency'],
            3,
            'risk',
            COLORS,
        );

        expect(lines.map((line) => line.series.key)).toEqual([
            'cpu_pressure',
            'memory_pressure',
            'io_latency',
        ]);
        expect(lines.map((line) => line.dataKey)).toEqual(['risk_0', 'risk_1', 'risk_2']);
    });

    it('builds chronologically sorted chart rows', () => {
        const lines = buildPreferredRiskChartSeries(
            [
                makeSeries('cpu_pressure', 'CPU', [
                    ['2026-03-11T10:01:00Z', 3],
                    ['2026-03-11T10:02:00Z', 5],
                ]),
                makeSeries('io_latency', 'IO', [
                    ['2026-03-11T10:00:00Z', 1],
                    ['2026-03-11T10:02:00Z', 8],
                ]),
            ],
            ['cpu_pressure', 'io_latency'],
            2,
            'risk',
            COLORS,
        );

        const rows = buildRiskChartData(lines);
        expect(rows).toHaveLength(3);
        expect(rows[0].timestamp).toBe('2026-03-11T10:00:00Z');
        expect(rows[1].timestamp).toBe('2026-03-11T10:01:00Z');
        expect(rows[2].timestamp).toBe('2026-03-11T10:02:00Z');
        expect(rows[2].risk_0).toBe(5);
        expect(rows[2].risk_1).toBe(8);
    });
});
