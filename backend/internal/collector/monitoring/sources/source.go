package sources

import (
	"context"
	"sync"
	"time"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
)

// ProcessConfig configures the process source
type ProcessConfig struct {
	Enabled          bool          `yaml:"enabled"`
	ScanInterval     time.Duration `yaml:"scan_interval"`
	EnablePerProcess bool          `yaml:"enable_per_process"`
	EnableOpenFiles  bool          `yaml:"enable_open_files"`
	EnableIO         bool          `yaml:"enable_io"`
	TopNProcesses    int           `yaml:"top_n"`
}

// EBPFConfig configures the eBPF source
type EBPFConfig struct {
	Enabled      bool   `yaml:"enabled"`
	ProgramsPath string `yaml:"programs_path"`

	// What to monitor
	Syscalls bool `yaml:"syscalls"`
	Network  bool `yaml:"network"`
	Process  bool `yaml:"process"`
	IO       bool `yaml:"io"`
}

// ProcConfig configures the proc source
type ProcConfig struct {
	Enabled bool `yaml:"enabled"`
}

// HardwareConfig configures the hardware source
type HardwareConfig struct {
	Enabled         bool   `yaml:"enabled"`
	IncludeCPUInfo  bool   `yaml:"include_cpu_info"`
	IncludeMemInfo  bool   `yaml:"include_mem_info"`
	IncludeDiskInfo bool   `yaml:"include_disk_info"`
	IncludeNetwork  bool   `yaml:"include_network"`
	IncludeSmart    bool   `yaml:"include_smart"`
	ScanInterval    string `yaml:"scan_interval"` // duration string
}

// GPUConfig configures the GPU source
type GPUConfig struct {
	Enabled       bool   `yaml:"enabled"`
	IncludeNVIDIA bool   `yaml:"include_nvidia"`
	IncludeAMD    bool   `yaml:"include_amd"`
	IncludeIntel  bool   `yaml:"include_intel"`
	CollectClocks bool   `yaml:"collect_clocks"`
	CollectPower  bool   `yaml:"collect_power"`
	CollectTemp   bool   `yaml:"collect_temp"`
	CollectPcie   bool   `yaml:"collect_pcie"`
	ScanInterval  string `yaml:"scan_interval"` // duration string
}

// KubernetesConfig configures the Kubernetes source
type KubernetesConfig struct {
	Enabled        bool   `yaml:"enabled"`
	InCluster      bool   `yaml:"in_cluster"`
	IncludePods    bool   `yaml:"include_pods"`
	IncludeNodes   bool   `yaml:"include_nodes"`
	IncludePV      bool   `yaml:"include_pv"`
	KubeconfigPath string `yaml:"kubeconfig_path"`
	ScanInterval   string `yaml:"scan_interval"` // duration string
}

// Source is the interface for all monitoring sources
type Source interface {
	Name() string
	Start(ctx context.Context) error
	Stop() error
	Status() SourceStatus
}

// MetricSource is a source that produces metrics
type MetricSource interface {
	Source
	Collect(ctx context.Context) (*proto.MetricBatch, error)
}

// EventSource is a source that produces events
type EventSource interface {
	Source
	Events() <-chan Event
}

// Metric represents a collected metric
type Metric struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"` // gauge, counter, histogram
	Value     float64           `json:"value"`
	Timestamp time.Time         `json:"timestamp"`
	Labels    map[string]string `json:"labels"`
	Source    string            `json:"source"`
}

// Event represents a significant occurrence
type Event struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Message   string                 `json:"message"`
	Severity  string                 `json:"severity"`
	Source    string                 `json:"source"`
	Labels    map[string]string      `json:"labels"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// SourceStatus represents the status of a data source
type SourceStatus struct {
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Running   bool      `json:"running"`
	Healthy   bool      `json:"healthy"`
	LastError string    `json:"last_error,omitempty"`
	LastSeen  time.Time `json:"last_seen"`
}

// BaseSource provides common functionality for all sources
type BaseSource struct {
	name      string
	enabled   bool
	running   bool
	healthy   bool
	lastError string
	lastSeen  time.Time
	mu        sync.RWMutex
}

func (b *BaseSource) Name() string {
	return b.name
}

func (b *BaseSource) Status() SourceStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return SourceStatus{
		Name:      b.name,
		Enabled:   b.enabled,
		Running:   b.running,
		Healthy:   b.healthy,
		LastError: b.lastError,
		LastSeen:  b.lastSeen,
	}
}

func (b *BaseSource) setStatus(running, healthy bool, lastError string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.running = running
	b.healthy = healthy
	b.lastError = lastError
	b.lastSeen = time.Now()
}
