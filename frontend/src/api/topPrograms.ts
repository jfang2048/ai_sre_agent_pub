import { api } from './client';

export interface ProgramStats {
    collector_id: string;
    hostname: string;
    pid?: string;
    name: string;
    workload_class?: string;
    job?: string;
    comm_pattern?: string;
    pod_uid?: string;
    cpu_percent?: number;
    memory_bytes?: number;
    disk_read_bps?: number;
    disk_write_bps?: number;
    sched_wait_ratio?: number;
    sched_wait_seconds_total?: number;
    sched_run_seconds_total?: number;
    block_io_delay_seconds_total?: number;
    block_io_delay_seconds_per_second?: number;
    net_bytes_per_second?: number;
    net_queued_bytes?: number;
    net_connections?: number;
    gpu_mem_mib?: number;
    gpu_util_sm_percent?: number;
    gpu_util_mem_percent?: number;
    log_errors?: number;
    log_warnings?: number;
    log_events?: number;
    disk_read_bytes_total?: number;
    disk_write_bytes_total?: number;
    disk_read_syscalls_total?: number;
    disk_write_syscalls_total?: number;
    category_totals?: Record<string, number>;
    category_frequency?: Record<string, number>;
    signal_values?: Record<string, number>;
    signal_totals?: Record<string, number>;
    signal_frequency?: Record<string, number>;
    categories?: string[];
    score: number;
}

export interface KernelSignal {
    name: string;
    unit?: string;
    source?: string;
    description?: string;
}

export interface ResourceCategoryPage {
    category: string;
    title: string;
    primary_metric: string;
    kernel_signals?: KernelSignal[];
    ranked: ProgramStats[];
}

export interface TopProgramsResponse {
    collector_id?: string;
    generated_at: string;
    limit: number;
    count: number;
    programs: ProgramStats[];
    summary?: Record<string, ProgramStats>;
    by_category?: Record<string, ProgramStats[]>;
    resource_pages?: Record<string, ResourceCategoryPage>;
}

export interface TopProgramsQuery {
    limit?: number;
    collectorId?: string;
}

export async function fetchTopPrograms(input: number | TopProgramsQuery = 15): Promise<TopProgramsResponse> {
    const query: TopProgramsQuery = typeof input === 'number' ? { limit: input } : input;
    const params = new URLSearchParams();
    params.set('limit', String(query.limit ?? 15));
    if (query.collectorId?.trim()) {
        params.set('collector_id', query.collectorId.trim());
    }

    const { data } = await api.get<TopProgramsResponse>(`/top/programs?${params.toString()}`);
    return data;
}
