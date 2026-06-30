import { api } from './client';

export interface OrchestrationPolicy {
    slo_breach_ratio: number;
    slo_breach_consecutive: number;
    auto_remediation_enabled: boolean;
    remediation_cooldown: string;
    max_remediations_per_reconcile: number;
    max_remediations_per_workload: number;
    remediation_min_improvement: number;
}

export interface OrchestrationMetrics {
    reconciles_total: number;
    scheduling_attempts_total: number;
    scheduling_failures_total: number;
    batch_deferrals_total: number;
    self_heal_actions_total: number;
    route_updates_total: number;
    slo_violations_total: number;
    slo_violations_active: number;
    remediation_attempts_total: number;
    remediation_actions_total: number;
    remediation_blocked_total: number;
    queue_depth: number;
    running_workloads: number;
    deferred_workloads: number;
    failed_workloads: number;
    completed_workloads: number;
    assignments_total: number;
}

export interface RemediationGateReason {
    reason: string;
    count: number;
}

export interface SLOViolationSummary {
    workload_id: string;
    service: string;
    model?: string;
    class?: string;
    priority?: string;
    latency_slo_ms: number;
    estimated_latency_ms: number;
    breach_ratio: number;
    consecutive_breaches: number;
    assigned_nodes?: string[];
    last_updated_at?: string;
    reason?: string;
}

export interface OrchestrationDiagnostics {
    generated_at: string;
    policy: OrchestrationPolicy;
    metrics: OrchestrationMetrics;
    blocked_reasons: RemediationGateReason[];
    violations: SLOViolationSummary[];
}

interface OrchestrationDiagnosticsResponse {
    diagnostics: OrchestrationDiagnostics;
    timestamp: string;
}

export async function fetchOrchestrationDiagnostics(): Promise<OrchestrationDiagnostics> {
    const { data } = await api.get<OrchestrationDiagnosticsResponse>('/orchestration/diagnostics');
    return data.diagnostics;
}
