import { describe, expect, it } from 'vitest';

import {
    requireTrimmedString,
    setBooleanParam,
    setPositiveIntParam,
    setQueryParam,
    toQuerySuffix,
} from '../query';

describe('api query helpers', () => {
    it('builds a stable query suffix from normalized params', () => {
        const params = new URLSearchParams();
        setQueryParam(params, 'workflow_id', '  run-123  ');
        setPositiveIntParam(params, 'limit', 25);
        setBooleanParam(params, 'refresh', false);

        expect(toQuerySuffix(params)).toBe('?workflow_id=run-123&limit=25&refresh=false');
    });

    it('requires non-empty trimmed strings for path params', () => {
        expect(requireTrimmedString('  trace-1  ', 'trace id')).toBe('trace-1');
        expect(() => requireTrimmedString('   ', 'trace id')).toThrow('trace id is required');
    });
});
