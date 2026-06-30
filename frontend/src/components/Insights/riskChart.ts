import type { RiskSeries } from '@/api/agentWorkflows';

export interface RiskChartLine {
    dataKey: string;
    label: string;
    color: string;
    series: RiskSeries;
}

export function buildPreferredRiskChartSeries(
    series: RiskSeries[],
    preferred: string[],
    maxLines: number,
    dataKeyPrefix: string,
    colors: string[],
): RiskChartLine[] {
    if (series.length === 0 || maxLines <= 0) {
        return [];
    }

    const byKey = new Map(series.map((item) => [item.key, item] as const));
    const selected: RiskSeries[] = [];
    const seen = new Set<string>();

    for (const key of preferred) {
        const match = byKey.get(key);
        if (!match || seen.has(match.key)) {
            continue;
        }
        selected.push(match);
        seen.add(match.key);
        if (selected.length >= maxLines) {
            break;
        }
    }

    if (selected.length < maxLines) {
        for (const item of series) {
            if (seen.has(item.key)) {
                continue;
            }
            selected.push(item);
            seen.add(item.key);
            if (selected.length >= maxLines) {
                break;
            }
        }
    }

    return selected.map((item, index) => ({
        dataKey: `${dataKeyPrefix}_${index}`,
        label: item.display,
        color: colors[index % colors.length],
        series: item,
    }));
}

export function buildRiskChartData(chartSeries: RiskChartLine[]): Array<Record<string, number | string>> {
    if (chartSeries.length === 0) {
        return [];
    }

    const rowsByTimestamp = new Map<string, Record<string, number | string>>();
    for (const item of chartSeries) {
        for (const point of item.series.points) {
            const row = rowsByTimestamp.get(point.timestamp) ?? { timestamp: point.timestamp };
            row[item.dataKey] = point.value;
            rowsByTimestamp.set(point.timestamp, row);
        }
    }

    const rows = Array.from(rowsByTimestamp.values());
    rows.sort((left, right) => {
        const a = String(left.timestamp);
        const b = String(right.timestamp);
        if (a < b) {
            return -1;
        }
        if (a > b) {
            return 1;
        }
        return 0;
    });
    return rows;
}
