package ebpf

import "time"

// MetricSample is a minimal metric representation exported by the eBPF module.
type MetricSample struct {
	Name   string
	Type   string
	Value  float64
	Labels map[string]string
}

// Event is the normalized runtime behavior signal produced by the eBPF core.
// Every event carries evidence metadata so downstream RCA and security pipelines
// can reference immutable IDs.
type Event struct {
	EvidenceID  string            `json:"evidence_id"`
	Timestamp   time.Time         `json:"timestamp"`
	Category    string            `json:"category"`
	Type        string            `json:"type"`
	Scope       string            `json:"scope"`
	PID         int               `json:"pid,omitempty"`
	Container   string            `json:"container,omitempty"`
	Node        string            `json:"node,omitempty"`
	Comm        string            `json:"comm,omitempty"`
	Path        string            `json:"path,omitempty"`
	Port        int               `json:"port,omitempty"`
	RemoteIP    string            `json:"remote_ip,omitempty"`
	Severity    string            `json:"severity"`
	Confidence  float64           `json:"confidence"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ProcessStatsSnapshot is a compact per-process BPF-map style aggregate.
type ProcessStatsSnapshot struct {
	PID               int               `json:"pid"`
	Comm              string            `json:"comm,omitempty"`
	LastSeen          time.Time         `json:"last_seen"`
	Syscalls          map[string]uint64 `json:"syscalls,omitempty"`
	OpenCalls         uint64            `json:"open_calls"`
	ConnectCalls      uint64            `json:"connect_calls"`
	AcceptCalls       uint64            `json:"accept_calls"`
	BindCalls         uint64            `json:"bind_calls"`
	ExecCalls         uint64            `json:"exec_calls"`
	ForkCalls         uint64            `json:"fork_calls"`
	ExitCalls         uint64            `json:"exit_calls"`
	ResourceCPUUserMS uint64            `json:"resource_cpu_user_ms"`
	ResourceCPUSysMS  uint64            `json:"resource_cpu_sys_ms"`
	ResourceRSSBytes  uint64            `json:"resource_rss_bytes"`
}

// Summary is the structured /api/v1/ebpf/summary payload.
type Summary struct {
	GeneratedAt                 time.Time                    `json:"generated_at"`
	RuntimeMode                 string                       `json:"runtime_mode"`
	EventCount                  int                          `json:"event_count"`
	SyscallStatistics           map[string]uint64            `json:"syscall_statistics"`
	CategoryCounts              map[string]uint64            `json:"category_counts"`
	ProcessStats                []ProcessStatsSnapshot       `json:"process_stats"`
	FileAccessPatterns          map[string]uint64            `json:"file_access_patterns"`
	LongLivedTCPConnections     []map[string]string          `json:"long_lived_tcp_connections"`
	AbnormalBindPorts           map[int]uint64               `json:"abnormal_bind_ports"`
	PrivilegeEscalationAttempts uint64                       `json:"privilege_escalation_attempts"`
	NetworkCounters             map[string]uint64            `json:"network_counters"`
	RemoteScopeCounts           map[string]uint64            `json:"remote_scope_counts"`
	SensitivePathCounts         map[string]uint64            `json:"sensitive_path_counts"`
	ResourceStats               map[string]map[string]uint64 `json:"resource_stats"`
}
