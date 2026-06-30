export function setQueryParam(
    params: URLSearchParams,
    key: string,
    value?: string,
    { trim = true }: { trim?: boolean } = {},
) {
    if (typeof value !== 'string') {
        return;
    }
    const next = trim ? value.trim() : value;
    if (next) {
        params.set(key, next);
    }
}

export function appendQueryParam(
    params: URLSearchParams,
    key: string,
    value?: string,
    { trim = true }: { trim?: boolean } = {},
) {
    if (typeof value !== 'string') {
        return;
    }
    const next = trim ? value.trim() : value;
    if (next) {
        params.append(key, next);
    }
}

export function setPositiveIntParam(params: URLSearchParams, key: string, value?: number, { floor = false }: { floor?: boolean } = {}) {
    if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
        return;
    }
    const normalized = floor ? Math.floor(value) : value;
    params.set(key, String(normalized));
}

export function setNonNegativeIntParam(params: URLSearchParams, key: string, value?: number, { floor = false }: { floor?: boolean } = {}) {
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
        return;
    }
    const normalized = floor ? Math.floor(value) : value;
    params.set(key, String(normalized));
}

export function setBooleanParam(params: URLSearchParams, key: string, value?: boolean) {
    if (typeof value === 'boolean') {
        params.set(key, value ? 'true' : 'false');
    }
}

export function toQuerySuffix(params: URLSearchParams): string {
    const query = params.toString();
    return query ? `?${query}` : '';
}

export function requireTrimmedString(value: string, label: string): string {
    const trimmed = value.trim();
    if (!trimmed) {
        throw new Error(`${label} is required`);
    }
    return trimmed;
}
