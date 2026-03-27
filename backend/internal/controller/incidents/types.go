package incidents

import "time"

// InputAlert is a normalized alert payload used by the orchestrator.
type InputAlert struct {
	ID          string
	Title       string
	Service     string
	Severity    string
	StartsAt    time.Time
	EndsAt      time.Time
	Labels      map[string]string
	Annotations map[string]string
}

// TimeWindow defines the query bounds.
type TimeWindow struct {
	Start time.Time
	End   time.Time
}

// ResourceRef represents a concrete resource in scope.
type ResourceRef struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Name   string            `json:"name"`
	Scope  string            `json:"scope,omitempty"` // e.g., region/cluster
	Labels map[string]string `json:"labels,omitempty"`
}

// ServiceImpact captures the services affected by an alert.
type ServiceImpact struct {
	Service      string        `json:"service"`
	Environment  string        `json:"environment,omitempty"`
	Dependencies []string      `json:"dependencies,omitempty"`
	BlastRadius  []string      `json:"blast_radius,omitempty"`
	Resources    []ResourceRef `json:"resources,omitempty"`
}

// MonitoringRequest expresses a metrics query for a context.
type MonitoringRequest struct {
	Services  []string
	Resources []ResourceRef
	Window    TimeWindow
	Keywords  []string
}

// LogRequest expresses a log query for a context.
type LogRequest struct {
	Services  []string
	Resources []ResourceRef
	Window    TimeWindow
	Keywords  []string
	Limit     int
}

// KubernetesRequest expresses a workload snapshot request.
type KubernetesRequest struct {
	Services  []string
	Resources []ResourceRef
	Namespace string
}

// MetricPoint is a single metric sample.
type MetricPoint struct {
	Timestamp time.Time         `json:"timestamp"`
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// MetricFinding summarizes a slice of metric evidence.
type MetricFinding struct {
	Scope       string        `json:"scope"` // node/service/resource identifier
	Query       string        `json:"query,omitempty"`
	Points      []MetricPoint `json:"points,omitempty"`
	Symptoms    []string      `json:"symptoms,omitempty"`
	AnomalyHint string        `json:"anomaly_hint,omitempty"`
}

// LogMatch represents a single log hit.
type LogMatch struct {
	Fingerprint string `json:"fingerprint"`
	Count       uint64 `json:"count"`
	Example     string `json:"example,omitempty"`
	Source      string `json:"source,omitempty"`
}

// LogFinding summarizes log evidence.
type LogFinding struct {
	Scope    string     `json:"scope"`
	Query    string     `json:"query,omitempty"`
	Matches  []LogMatch `json:"matches,omitempty"`
	Keywords []string   `json:"keywords,omitempty"`
}

// KubernetesFinding captures cluster/workload level context.
type KubernetesFinding struct {
	Cluster   string            `json:"cluster,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	Nodes     []string          `json:"nodes,omitempty"`
	Workloads map[string]string `json:"workloads,omitempty"` // workload -> status summary
}

// AggregatedContext is the final correlated bundle sent to the Agent.
type AggregatedContext struct {
	IncidentID     string             `json:"incident_id"`
	AlertID        string             `json:"alert_id"`
	Alert          InputAlert         `json:"alert"`
	Window         TimeWindow         `json:"window"`
	Services       []ServiceImpact    `json:"services,omitempty"`
	ResourceScope  []ResourceRef      `json:"resource_scope,omitempty"`
	Keywords       []string           `json:"keywords,omitempty"`
	Metrics        []MetricFinding    `json:"metrics,omitempty"`
	Logs           []LogFinding       `json:"logs,omitempty"`
	Kubernetes     *KubernetesFinding `json:"kubernetes,omitempty"`
	SuspectedCause []string           `json:"suspected_cause,omitempty"`
	GeneratedAt    time.Time          `json:"generated_at"`
	Notes          string             `json:"notes,omitempty"`
}
