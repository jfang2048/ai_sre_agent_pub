import { describe, expect, it } from 'vitest';
import type { TrendSeries } from '@/api/trends';
import { formatChartTime, prepareChartPoints } from '../metricChart';

describe('metric chart observations', () => {
    it('sorts by numeric elapsed time without altering the input', () => {
        const points = [
            { timestamp: '2026-02-21T00:10:00Z', value: 30 },
            { timestamp: '2026-02-21T00:00:00Z', value: 0 },
            { timestamp: '2026-02-21T00:01:00Z', value: 10, is_anomaly: true },
        ];

        const result = prepareChartPoints(points);

        expect(result.map((point) => point.value)).toEqual([0, 10, 30]);
        expect(result[1].timestamp - result[0].timestamp).toBe(60_000);
        expect(result[2].timestamp - result[1].timestamp).toBe(540_000);
        expect(result.map((point) => point.anomalyValue)).toEqual([null, 10, null]);
        expect(points[0].value).toBe(30);
    });

    it('rejects invalid timestamps and preserves missing/non-finite values as gaps', () => {
        const points = [
            { timestamp: 'invalid', value: 20, is_anomaly: true },
            { timestamp: '2026-02-21T00:00:00Z', value: NaN, is_anomaly: true },
            { timestamp: '2026-02-21T00:01:00Z', value: Infinity, is_anomaly: true },
            { timestamp: '2026-02-21T00:02:00Z', value: -Infinity },
            { timestamp: '2026-02-21T00:03:00Z', value: null },
            { timestamp: '2026-02-21T00:04:00Z' },
            { timestamp: '2026-02-21T00:05:00Z', value: 0, is_anomaly: true },
        ] as TrendSeries['points'];

        expect(prepareChartPoints(points).map(({ value, anomalyValue }) => ({ value, anomalyValue }))).toEqual([
            { value: null, anomalyValue: null },
            { value: null, anomalyValue: null },
            { value: null, anomalyValue: null },
            { value: null, anomalyValue: null },
            { value: null, anomalyValue: null },
            { value: 0, anomalyValue: 0 },
        ]);
    });

    it('formats seconds in a 24-hour clock and rejects invalid times', () => {
        const timestamp = new Date(2026, 1, 21, 13, 4, 7).getTime();

        expect(formatChartTime(timestamp)).toBe('13:04:07');
        expect(formatChartTime(NaN)).toBe('—');
        expect(prepareChartPoints([])).toEqual([]);
    });
});
