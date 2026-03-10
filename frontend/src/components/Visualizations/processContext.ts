import type { ProgramStats } from '@/api/topPrograms';

type ProcessContextShape = Partial<Pick<
    ProgramStats,
    'name' | 'pid' | 'collector_id' | 'hostname' | 'workload_class' | 'comm_pattern' | 'job' | 'pod_uid'
>>;

export function formatProcessContext(process: ProcessContextShape): string {
    const parts: string[] = [];
    if (process.workload_class) {
        parts.push(process.workload_class);
    }
    if (process.comm_pattern) {
        parts.push(process.comm_pattern);
    }
    if (process.job) {
        parts.push(`job:${process.job}`);
    }
    if (process.pod_uid) {
        parts.push(`pod:${process.pod_uid.slice(0, 8)}`);
    }
    return parts.join(' · ');
}

export function formatProcessContextSuffix(process: ProcessContextShape): string {
    const formatted = formatProcessContext(process);
    if (!formatted) {
        return '';
    }
    return ` · ${formatted}`;
}

export function buildProcessFilterHint(process: ProcessContextShape): string {
    return [process.name, process.pid, process.job, process.pod_uid].filter(Boolean).join(' ');
}

export function buildProcessSearchText(process: ProcessContextShape): string {
    return [
        process.name,
        process.pid,
        process.collector_id,
        process.hostname,
        process.workload_class,
        process.comm_pattern,
        process.job,
        process.pod_uid,
    ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
}
