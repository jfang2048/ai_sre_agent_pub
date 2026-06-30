import { describe, expect, it } from 'vitest';
import {
    buildProcessFilterHint,
    buildProcessSearchText,
    formatProcessContext,
    formatProcessContextSuffix,
} from '../processContext';

describe('processContext helpers', () => {
    const process = {
        name: 'python',
        pid: '4242',
        collector_id: 'collector-a',
        hostname: 'node-a',
        workload_class: 'training',
        comm_pattern: 'nccl',
        job: 'llama-train',
        pod_uid: '1234567890abcdef',
    };

    it('formats process context labels in a stable order', () => {
        expect(formatProcessContext(process)).toBe('training · nccl · job:llama-train · pod:12345678');
    });

    it('returns suffix with leading separator only when context exists', () => {
        expect(formatProcessContextSuffix(process)).toBe(' · training · nccl · job:llama-train · pod:12345678');
        expect(formatProcessContextSuffix({ name: 'bash' })).toBe('');
    });

    it('builds process filter hint from key identifiers', () => {
        expect(buildProcessFilterHint(process)).toBe('python 4242 llama-train 1234567890abcdef');
    });

    it('builds lowercased search text including topology and workload fields', () => {
        const searchText = buildProcessSearchText(process);
        expect(searchText).toContain('python');
        expect(searchText).toContain('collector-a');
        expect(searchText).toContain('training');
        expect(searchText).toContain('nccl');
    });
});
