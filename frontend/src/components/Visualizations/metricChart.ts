import type { TrendSeries } from '@/api/trends';

// Keep observation time numeric. Clock strings are labels, not categories.
export function prepareChartPoints(points: TrendSeries['points']) {
    return points.map(point => {
        const value = Number.isFinite(point.value) ? point.value : null;
        return {
            timestamp: new Date(point.timestamp).getTime(),
            value,
            anomalyValue: point.is_anomaly ? value : null,
        };
    }).filter(point => Number.isFinite(point.timestamp))
        .sort((a, b) => a.timestamp - b.timestamp);
}

export function formatChartTime(timestamp: number): string {
    if (!Number.isFinite(timestamp)) return '—';
    return new Date(timestamp).toLocaleTimeString([], {
        hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
    });
}
