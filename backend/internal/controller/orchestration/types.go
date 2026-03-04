package orchestration

import "time"

// WorkloadClass expresses latency sensitivity.
type WorkloadClass string

const (
	WorkloadClassRealtime WorkloadClass = "realtime"
	WorkloadClassBatch    WorkloadClass = "batch"
)

// PriorityClass indicates SLA urgency (P0 highest).
type PriorityClass string

const (
	PriorityP0 PriorityClass = "P0"
	PriorityP1 PriorityClass = "P1"
	PriorityP2 PriorityClass = "P2"
	PriorityP3 PriorityClass = "P3"
)

// WorkloadState tracks the lifecycle of a workload.
type WorkloadState string

const (
	WorkloadStateQueued    WorkloadState = "queued"
	WorkloadStateDeferred  WorkloadState = "deferred"
	WorkloadStateRunning   WorkloadState = "running"
	WorkloadStateFailed    WorkloadState = "failed"
	WorkloadStateCompleted WorkloadState = "completed"
)

// ResourceVector is a portable representation for heterogeneous capacity.
type ResourceVector struct {
	CPUCores    float64 `json:"cpu_cores,omitempty"`
	GPUCards    float64 `json:"gpu_cards,omitempty"`
	NPUSlices   float64 `json:"npu_slices,omitempty"`
	MemoryBytes float64 `json:"memory_bytes,omitempty"`
	NetworkMbps float64 `json:"network_mbps,omitempty"`
	StorageIOPS float64 `json:"storage_iops,omitempty"`
}

// UtilizationVector holds normalized [0,1] pressure signals.
type UtilizationVector struct {
	CPU     float64 `json:"cpu,omitempty"`
	GPU     float64 `json:"gpu,omitempty"`
	NPU     float64 `json:"npu,omitempty"`
	Memory  float64 `json:"memory,omitempty"`
	Network float64 `json:"network,omitempty"`
	Storage float64 `json:"storage,omitempty"`
}

// ModelCache tracks placement-local cache warmth for inference reuse.
type ModelCache struct {
	Model          string    `json:"model"`
	Warm           bool      `json:"warm"`
	EstimatedBytes float64   `json:"estimated_bytes,omitempty"`
	HitRate        float64   `json:"hit_rate,omitempty"`
	LastUsedAt     time.Time `json:"last_used_at"`
}

// ResourceNode represents an allocatable host in the global pool.
type ResourceNode struct {
	ID                  string                `json:"id"`
	Hostname            string                `json:"hostname,omitempty"`
	Cluster             string                `json:"cluster,omitempty"`
	Zone                string                `json:"zone,omitempty"`
	Labels              map[string]string     `json:"labels,omitempty"`
	Healthy             bool                  `json:"healthy"`
	LastSeen            time.Time             `json:"last_seen,omitempty"`
	TelemetryAgeSeconds float64               `json:"telemetry_age_seconds,omitempty"`
	Capacity            ResourceVector        `json:"capacity"`
	Reserved            ResourceVector        `json:"reserved"`
	Available           ResourceVector        `json:"available"`
	Utilization         UtilizationVector     `json:"utilization"`
	LatencyScore        float64               `json:"latency_score,omitempty"`
	ModelCaches         map[string]ModelCache `json:"model_caches,omitempty"`
}

// WorkloadSpec captures the desired state for one compute workload.
type WorkloadSpec struct {
	ID                  string         `json:"id,omitempty"`
	Service             string         `json:"service"`
	Model               string         `json:"model,omitempty"`
	Class               WorkloadClass  `json:"class,omitempty"`
	Priority            PriorityClass  `json:"priority,omitempty"`
	Requested           ResourceVector `json:"requested"`
	TargetConcurrency   int            `json:"target_concurrency,omitempty"`
	LatencySLOMs        int            `json:"latency_slo_ms,omitempty"`
	MinBatchSize        int            `json:"min_batch_size,omitempty"`
	MaxBatchSize        int            `json:"max_batch_size,omitempty"`
	NotBefore           *time.Time     `json:"not_before,omitempty"`
	Deadline            *time.Time     `json:"deadline,omitempty"`
	PreferredZones      []string       `json:"preferred_zones,omitempty"`
	PreferredClusters   []string       `json:"preferred_clusters,omitempty"`
	MaxPartitions       int            `json:"max_partitions,omitempty"`
	CacheReusePreferred bool           `json:"cache_reuse_preferred,omitempty"`
}

// Assignment is one partition placement decision.
type Assignment struct {
	WorkloadID         string         `json:"workload_id"`
	NodeID             string         `json:"node_id"`
	Zone               string         `json:"zone,omitempty"`
	Cluster            string         `json:"cluster,omitempty"`
	Partition          int            `json:"partition"`
	Reserved           ResourceVector `json:"reserved"`
	RouteWeight        float64        `json:"route_weight,omitempty"`
	EstimatedLatencyMs float64        `json:"estimated_latency_ms,omitempty"`
	Reason             string         `json:"reason,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
}

// Workload stores runtime state for one request.
type Workload struct {
	Spec              WorkloadSpec  `json:"spec"`
	State             WorkloadState `json:"state"`
	Reason            string        `json:"reason,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	QueueDelaySeconds float64       `json:"queue_delay_seconds,omitempty"`
	Assignments       []Assignment  `json:"assignments,omitempty"`
}

// RouteTarget is one backend candidate in a routing plan.
type RouteTarget struct {
	NodeID             string  `json:"node_id"`
	Zone               string  `json:"zone,omitempty"`
	Cluster            string  `json:"cluster,omitempty"`
	Weight             float64 `json:"weight"`
	EstimatedLatencyMs float64 `json:"estimated_latency_ms,omitempty"`
	SuggestedBatchSize int     `json:"suggested_batch_size,omitempty"`
}

// RoutingPlan provides multi-model and service-aware traffic targets.
type RoutingPlan struct {
	Service     string        `json:"service"`
	Model       string        `json:"model,omitempty"`
	Class       WorkloadClass `json:"class,omitempty"`
	GeneratedAt time.Time     `json:"generated_at"`
	Targets     []RouteTarget `json:"targets"`
}

// HealingEvent records automatic recovery actions.
type HealingEvent struct {
	ID            string    `json:"id"`
	Timestamp     time.Time `json:"timestamp"`
	WorkloadID    string    `json:"workload_id"`
	Action        string    `json:"action"`
	Reason        string    `json:"reason"`
	PreviousNodes []string  `json:"previous_nodes,omitempty"`
}

// MetricsSnapshot is exported to /metrics and status APIs.
type MetricsSnapshot struct {
	ReconcilesTotal          uint64 `json:"reconciles_total"`
	SchedulingAttemptsTotal  uint64 `json:"scheduling_attempts_total"`
	SchedulingFailuresTotal  uint64 `json:"scheduling_failures_total"`
	BatchDeferralsTotal      uint64 `json:"batch_deferrals_total"`
	SelfHealActionsTotal     uint64 `json:"self_heal_actions_total"`
	RouteUpdatesTotal        uint64 `json:"route_updates_total"`
	SLOViolationsTotal       uint64 `json:"slo_violations_total"`
	SLOViolationsActive      int    `json:"slo_violations_active"`
	RemediationAttemptsTotal uint64 `json:"remediation_attempts_total"`
	RemediationActionsTotal  uint64 `json:"remediation_actions_total"`
	RemediationBlockedTotal  uint64 `json:"remediation_blocked_total"`
	QueueDepth               int    `json:"queue_depth"`
	RunningWorkloads         int    `json:"running_workloads"`
	DeferredWorkloads        int    `json:"deferred_workloads"`
	FailedWorkloads          int    `json:"failed_workloads"`
	CompletedWorkloads       int    `json:"completed_workloads"`
	AssignmentsTotal         int    `json:"assignments_total"`
}

// PolicySnapshot describes active SLO/remediation policy knobs.
type PolicySnapshot struct {
	SLOBreachRatio              float64 `json:"slo_breach_ratio"`
	SLOBreachConsecutive        int     `json:"slo_breach_consecutive"`
	AutoRemediationEnabled      bool    `json:"auto_remediation_enabled"`
	RemediationCooldown         string  `json:"remediation_cooldown"`
	MaxRemediationsPerReconcile int     `json:"max_remediations_per_reconcile"`
	MaxRemediationsPerWorkload  int     `json:"max_remediations_per_workload"`
	RemediationMinImprovement   float64 `json:"remediation_min_improvement"`
}

// RemediationGateCount tracks how often one remediation gate blocked an action.
type RemediationGateCount struct {
	Reason string `json:"reason"`
	Count  uint64 `json:"count"`
}

// SLOViolationSummary describes one running workload currently breaching SLO policy.
type SLOViolationSummary struct {
	WorkloadID          string        `json:"workload_id"`
	Service             string        `json:"service"`
	Model               string        `json:"model,omitempty"`
	Class               WorkloadClass `json:"class,omitempty"`
	Priority            PriorityClass `json:"priority,omitempty"`
	LatencySLOMs        int           `json:"latency_slo_ms"`
	EstimatedLatencyMs  float64       `json:"estimated_latency_ms"`
	BreachRatio         float64       `json:"breach_ratio"`
	ConsecutiveBreaches int           `json:"consecutive_breaches"`
	AssignedNodes       []string      `json:"assigned_nodes,omitempty"`
	LastUpdatedAt       time.Time     `json:"last_updated_at,omitempty"`
	Reason              string        `json:"reason,omitempty"`
}

// DiagnosticsSnapshot exposes policy effectiveness and blocked-action reasons.
type DiagnosticsSnapshot struct {
	GeneratedAt    time.Time              `json:"generated_at"`
	Policy         PolicySnapshot         `json:"policy"`
	Metrics        MetricsSnapshot        `json:"metrics"`
	BlockedReasons []RemediationGateCount `json:"blocked_reasons"`
	Violations     []SLOViolationSummary  `json:"violations"`
}

// Snapshot summarizes full orchestration state.
type Snapshot struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Nodes       []ResourceNode  `json:"nodes"`
	Workloads   []Workload      `json:"workloads"`
	Routes      []RoutingPlan   `json:"routes"`
	Events      []HealingEvent  `json:"events"`
	Metrics     MetricsSnapshot `json:"metrics"`
}
