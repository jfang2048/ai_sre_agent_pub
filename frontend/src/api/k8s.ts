import { api } from './client';
import { setPositiveIntParam, setQueryParam, toQuerySuffix } from './query';

export interface K8sTopWorkload {
    cluster: string;
    namespace: string;
    kind: string;
    name: string;
    service: string;
    pods_total: number;
    pods_running: number;
    pods_pending: number;
    pods_failed: number;
    container_restarts: number;
    score: number;
    metric: string;
    gpu_requests?: number;
    gpu_limits?: number;
    nodes?: string[];
}

export interface K8sTopNode {
    cluster: string;
    name: string;
    ready: boolean;
    schedulable: boolean;
    zone?: string;
    cpu_usage_percent?: number;
    memory_usage_percent?: number;
    gpu_util_percent?: number;
    log_errors?: number;
    log_warnings?: number;
    score: number;
    metric: string;
}

interface K8sWorkloadResponse {
    metric: string;
    cluster?: string;
    limit: number;
    count: number;
    workloads: K8sTopWorkload[];
}

interface K8sNodeResponse {
    metric: string;
    cluster?: string;
    limit: number;
    count: number;
    nodes: K8sTopNode[];
}

export async function fetchK8sTopWorkloads(params: {
    metric?: string;
    limit?: number;
    cluster?: string;
} = {}): Promise<K8sWorkloadResponse> {
    const query = new URLSearchParams();
    setQueryParam(query, 'metric', params.metric);
    setPositiveIntParam(query, 'limit', params.limit);
    setQueryParam(query, 'cluster', params.cluster);
    const { data } = await api.get<K8sWorkloadResponse>(`/k8s/workloads/top${toQuerySuffix(query)}`);
    return data;
}

export async function fetchK8sTopNodes(params: {
    metric?: string;
    limit?: number;
    cluster?: string;
} = {}): Promise<K8sNodeResponse> {
    const query = new URLSearchParams();
    setQueryParam(query, 'metric', params.metric);
    setPositiveIntParam(query, 'limit', params.limit);
    setQueryParam(query, 'cluster', params.cluster);
    const { data } = await api.get<K8sNodeResponse>(`/k8s/nodes/top${toQuerySuffix(query)}`);
    return data;
}
