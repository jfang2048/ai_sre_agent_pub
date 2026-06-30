// Package probe implements the root cause analysis meta-collector.
// This integrates CPU, memory, I/O, and network RCA collectors.
package probe

import (
	"sync"
	"time"
)

// RootCauseConfig holds configuration for root cause analysis collection
type RootCauseConfig struct {
	Enabled        bool
	TopProcesses   int
	TopConnections int
	TopFiles       int
	TopRegions     int
	DetailedSmaps  bool // Enable detailed memory region analysis
	CollectCPU     bool
	CollectMemory  bool
	CollectIO      bool
	CollectNetwork bool
}

// DefaultRootCauseConfig returns the default RCA configuration
func DefaultRootCauseConfig() RootCauseConfig {
	return RootCauseConfig{
		Enabled:        true,
		TopProcesses:   20,
		TopConnections: 10,
		TopFiles:       5,
		TopRegions:     5,
		DetailedSmaps:  false, // Expensive, disabled by default
		CollectCPU:     true,
		CollectMemory:  true,
		CollectIO:      true,
		CollectNetwork: true,
	}
}

// RootCauseCollector is the main RCA collector that coordinates subsystem collectors
type RootCauseCollector struct {
	mu sync.RWMutex

	config RootCauseConfig

	cpuCollector     *CPURootCauseCollector
	memoryCollector  *MemoryRootCauseCollector
	ioCollector      *IORootCauseCollector
	networkCollector *NetworkRootCauseCollector

	// Last collection stats
	lastCollectDuration time.Duration
	lastCollectMetrics  int
}

// NewRootCauseCollector creates a new root cause analysis collector
func NewRootCauseCollector(config RootCauseConfig) *RootCauseCollector {
	rca := &RootCauseCollector{
		config: config,
	}

	if config.CollectCPU {
		rca.cpuCollector = NewCPURootCauseCollector(config.TopProcesses)
	}

	if config.CollectMemory {
		rca.memoryCollector = NewMemoryRootCauseCollector(
			config.TopProcesses,
			config.TopRegions,
			config.DetailedSmaps,
		)
	}

	if config.CollectIO {
		rca.ioCollector = NewIORootCauseCollector(
			config.TopProcesses,
			config.TopFiles,
		)
	}

	if config.CollectNetwork {
		rca.networkCollector = NewNetworkRootCauseCollector(
			config.TopProcesses,
			config.TopConnections,
		)
	}

	return rca
}

// Collect gathers all root cause analysis metrics
func (c *RootCauseCollector) Collect(now time.Time) ([]Metric, error) {
	if !c.config.Enabled {
		return nil, nil
	}

	start := time.Now()

	var allMetrics []Metric

	// Collect CPU RCA
	if c.cpuCollector != nil {
		if metrics, err := c.cpuCollector.Collect(now); err == nil {
			allMetrics = append(allMetrics, metrics...)
		}
	}

	// Collect Memory RCA
	if c.memoryCollector != nil {
		if metrics, err := c.memoryCollector.Collect(now); err == nil {
			allMetrics = append(allMetrics, metrics...)
		}
	}

	// Collect I/O RCA
	if c.ioCollector != nil {
		if metrics, err := c.ioCollector.Collect(now); err == nil {
			allMetrics = append(allMetrics, metrics...)
		}
	}

	// Collect Network RCA
	if c.networkCollector != nil {
		if metrics, err := c.networkCollector.Collect(now); err == nil {
			allMetrics = append(allMetrics, metrics...)
		}
	}

	// Track collection stats
	c.mu.Lock()
	c.lastCollectDuration = time.Since(start)
	c.lastCollectMetrics = len(allMetrics)
	c.mu.Unlock()

	// Add meta metrics about RCA collection
	allMetrics = append(allMetrics, Metric{
		Name:      "rca_collection_duration_seconds",
		Type:      "gauge",
		Value:     c.lastCollectDuration.Seconds(),
		Timestamp: now,
	})

	allMetrics = append(allMetrics, Metric{
		Name:      "rca_metrics_collected",
		Type:      "gauge",
		Value:     float64(c.lastCollectMetrics),
		Timestamp: now,
	})

	return allMetrics, nil
}

// GetStats returns collection statistics
func (c *RootCauseCollector) GetStats() (duration time.Duration, metricCount int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastCollectDuration, c.lastCollectMetrics
}

// RootCauseAnalysis represents a point-in-time analysis result
type RootCauseAnalysis struct {
	Timestamp time.Time                 `json:"timestamp"`
	CPU       *CPURootCauseAnalysis     `json:"cpu,omitempty"`
	Memory    *MemoryRootCauseAnalysis  `json:"memory,omitempty"`
	IO        *IORootCauseAnalysis      `json:"io,omitempty"`
	Network   *NetworkRootCauseAnalysis `json:"network,omitempty"`
}

// CPURootCauseAnalysis summarizes CPU root causes
type CPURootCauseAnalysis struct {
	OverallUsagePercent float64             `json:"overall_usage_percent"`
	TopProcesses        []CPUProcessSummary `json:"top_processes"`
	StateDistribution   map[string]int      `json:"state_distribution"`
	PotentialCauses     []string            `json:"potential_causes"`
}

// CPUProcessSummary is a summary of CPU usage for a process
type CPUProcessSummary struct {
	PID           int     `json:"pid"`
	Name          string  `json:"name"`
	CPUPercent    float64 `json:"cpu_percent"`
	UserPercent   float64 `json:"user_percent"`
	SystemPercent float64 `json:"system_percent"`
	State         string  `json:"state"`
	Threads       int     `json:"threads"`
	Wchan         string  `json:"wchan,omitempty"`
}

// MemoryRootCauseAnalysis summarizes memory root causes
type MemoryRootCauseAnalysis struct {
	TotalUsedBytes   uint64                 `json:"total_used_bytes"`
	TotalUsedPercent float64                `json:"total_used_percent"`
	SwapUsedBytes    uint64                 `json:"swap_used_bytes"`
	TopProcesses     []MemoryProcessSummary `json:"top_processes"`
	PotentialCauses  []string               `json:"potential_causes"`
}

// MemoryProcessSummary is a summary of memory usage for a process
type MemoryProcessSummary struct {
	PID         int     `json:"pid"`
	Name        string  `json:"name"`
	RSSBytes    uint64  `json:"rss_bytes"`
	RSSPercent  float64 `json:"rss_percent"`
	AnonBytes   uint64  `json:"anon_bytes"`
	SwapBytes   uint64  `json:"swap_bytes"`
	MajorFaults uint64  `json:"major_faults"`
	OOMScore    int     `json:"oom_score"`
}

// IORootCauseAnalysis summarizes I/O root causes
type IORootCauseAnalysis struct {
	TopDevices      []IODeviceSummary  `json:"top_devices"`
	TopProcesses    []IOProcessSummary `json:"top_processes"`
	PotentialCauses []string           `json:"potential_causes"`
}

// IODeviceSummary is a summary of I/O for a device
type IODeviceSummary struct {
	Device            string  `json:"device"`
	Utilization       float64 `json:"utilization_percent"`
	AvgReadLatencyMs  float64 `json:"avg_read_latency_ms"`
	AvgWriteLatencyMs float64 `json:"avg_write_latency_ms"`
	ReadBytesPerSec   float64 `json:"read_bytes_per_sec"`
	WriteBytesPerSec  float64 `json:"write_bytes_per_sec"`
}

// IOProcessSummary is a summary of I/O for a process
type IOProcessSummary struct {
	PID              int      `json:"pid"`
	Name             string   `json:"name"`
	ReadBytesPerSec  float64  `json:"read_bytes_per_sec"`
	WriteBytesPerSec float64  `json:"write_bytes_per_sec"`
	TopFiles         []string `json:"top_files,omitempty"`
}

// NetworkRootCauseAnalysis summarizes network root causes
type NetworkRootCauseAnalysis struct {
	TopInterfaces    []NetworkInterfaceSummary `json:"top_interfaces"`
	TopProcesses     []NetworkProcessSummary   `json:"top_processes"`
	ConnectionStates map[string]int            `json:"connection_states"`
	PotentialCauses  []string                  `json:"potential_causes"`
}

// NetworkInterfaceSummary is a summary for a network interface
type NetworkInterfaceSummary struct {
	Interface     string  `json:"interface"`
	RxBytesPerSec float64 `json:"rx_bytes_per_sec"`
	TxBytesPerSec float64 `json:"tx_bytes_per_sec"`
	Utilization   float64 `json:"utilization_percent,omitempty"`
	Errors        uint64  `json:"errors"`
	Drops         uint64  `json:"drops"`
}

// NetworkProcessSummary is a summary of network usage for a process
type NetworkProcessSummary struct {
	PID         int    `json:"pid"`
	Name        string `json:"name"`
	Connections int    `json:"connections"`
	ListenPorts []int  `json:"listen_ports,omitempty"`
}
