import { describe, expect, it } from 'vitest';
import { formatBytes, formatCount, formatMetricByUnit, formatPercent } from '../metricFormat';

describe('metric display validity', () => {
    it.each([NaN, Infinity, -Infinity])('does not display non-finite %s as a reading', value => {
        expect(formatBytes(value)).toBe('—');
        expect(formatCount(value)).toBe('—');
        expect(formatPercent(value)).toBe('—');
        for (const unit of ['percent', 'bytes_per_second', 'milliseconds', 'mib', 'count']) {
            expect(formatMetricByUnit(value, unit)).toBe('—');
        }
    });
    it('preserves observed zero', () => {
        expect(formatPercent(0)).toBe('0.0%');
        expect(formatBytes(0)).toBe('0 B');
        expect(formatCount(0)).toBe('0');
    });
});
