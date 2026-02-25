export function formatPercent(value?: number, digits = 1): string {
    if (typeof value !== 'number' || Number.isNaN(value)) {
        return '—';
    }
    return `${value.toFixed(digits)}%`;
}

export function formatCount(value?: number): string {
    if (typeof value !== 'number' || Number.isNaN(value)) {
        return '—';
    }
    return value.toLocaleString(undefined, { maximumFractionDigits: 0 });
}

export function formatBytes(value?: number): string {
    if (typeof value !== 'number' || Number.isNaN(value)) {
        return '—';
    }
    if (value <= 0) {
        return '0 B';
    }
    const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    const exp = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
    const scaled = value / (1024 ** exp);
    return `${scaled.toFixed(scaled >= 10 ? 0 : 1)} ${units[exp]}`;
}

export function formatRate(value?: number): string {
    const base = formatBytes(value);
    return base === '—' ? base : `${base}/s`;
}

export function formatMetricByUnit(value: number, unit: string): string {
    switch (unit) {
        case 'percent':
            return formatPercent(value);
        case 'bytes_per_second':
            return formatRate(value);
        case 'bytes':
            return formatBytes(value);
        case 'iops':
            return `${formatCount(value)} IOPS`;
        case 'milliseconds':
            return `${value.toFixed(2)} ms`;
        case 'pages_per_second':
            return `${formatCount(value)} pages/s`;
        case 'mib':
            return `${value.toFixed(0)} MiB`;
        case 'count':
            return formatCount(value);
        default:
            return Number.isFinite(value) ? value.toFixed(2) : '—';
    }
}
