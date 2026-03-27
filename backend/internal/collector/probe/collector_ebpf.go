// Package probe integrates the mandatory eBPF runtime module into metric
// collection, preserving existing metric contracts while exposing richer
// event/summary data for controller APIs.
package probe

import (
	"strconv"
	"time"

	ebpfcore "github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe/ebpf"
)

// EBPFConfig configures the mandatory eBPF runtime.
type EBPFConfig struct {
	Enabled     bool
	SocketPath  string
	Categories  []string
	MaxMsgBytes int
	RateWindow  time.Duration

	// Mandatory runtime controls.
	RingSize              int
	EventFlushLimit       int
	AllowedListenPorts    []int
	SyntheticPollInterval time.Duration
	LongLivedTCPThreshold time.Duration
}

// EBPFEvent is the normalized runtime event envelope.
type EBPFEvent = ebpfcore.Event

// EBPFSummary is the structured eBPF summary payload.
type EBPFSummary = ebpfcore.Summary

// EBPFCollector adapts ebpfcore.Collector to probe metric interfaces.
type EBPFCollector struct {
	cfg     EBPFConfig
	runtime *ebpfcore.Collector
}

// NewEBPFCollector creates a collector with default runtime options.
func NewEBPFCollector(socketPath string) *EBPFCollector {
	return NewEBPFCollectorWithConfig(EBPFConfig{
		Enabled:    true,
		SocketPath: socketPath,
	})
}

// NewEBPFCollectorWithConfig creates an explicit eBPF collector configuration.
func NewEBPFCollectorWithConfig(cfg EBPFConfig) *EBPFCollector {
	if !cfg.Enabled {
		return &EBPFCollector{cfg: cfg}
	}

	coreCfg := ebpfcore.Config{
		SocketPath:            cfg.SocketPath,
		MaxMessageBytes:       cfg.MaxMsgBytes,
		RingSize:              cfg.RingSize,
		EventFlushLimit:       cfg.EventFlushLimit,
		Categories:            cfg.Categories,
		AllowedListenPorts:    cfg.AllowedListenPorts,
		SyntheticPoll:         cfg.SyntheticPollInterval,
		LongLivedTCPThreshold: cfg.LongLivedTCPThreshold,
	}
	return &EBPFCollector{
		cfg:     cfg,
		runtime: ebpfcore.NewCollector(coreCfg),
	}
}

// Start starts eBPF event collection.
func (ec *EBPFCollector) Start() error {
	if ec == nil || ec.runtime == nil {
		return nil
	}
	return ec.runtime.Start()
}

// Stop stops eBPF event collection.
func (ec *EBPFCollector) Stop() {
	if ec == nil || ec.runtime == nil {
		return
	}
	ec.runtime.Stop()
}

// Events returns recent runtime events.
func (ec *EBPFCollector) Events(limit int) []EBPFEvent {
	if ec == nil || ec.runtime == nil {
		return nil
	}
	return ec.runtime.RecentEvents(limit)
}

// Summary returns structured runtime summary.
func (ec *EBPFCollector) Summary() EBPFSummary {
	if ec == nil || ec.runtime == nil {
		return EBPFSummary{}
	}
	return ec.runtime.Summary()
}

// GetMetrics converts core metric samples to probe metrics.
func (ec *EBPFCollector) GetMetrics(now time.Time) []Metric {
	if ec == nil || ec.runtime == nil {
		return nil
	}
	samples := ec.runtime.MetricSamples(now)
	out := make([]Metric, 0, len(samples)+32)

	eventsTotal := 0.0
	for _, sample := range samples {
		out = append(out, Metric{
			Name:      sample.Name,
			Type:      sample.Type,
			Value:     sample.Value,
			Labels:    sample.Labels,
			Timestamp: now,
		})
		if sample.Name == "node_ebpf_runtime_event" {
			eventsTotal += sample.Value
		}
	}

	summary := ec.runtime.Summary()
	out = append(out,
		Metric{
			Name:      "node_ebpf_events_total",
			Type:      "counter",
			Value:     float64(summary.EventCount),
			Timestamp: now,
		},
		Metric{
			Name:      "node_ebpf_events_rate",
			Type:      "gauge",
			Value:     eventsTotal / clampPositiveFloat(sampleWindowSeconds(ec.cfg.RateWindow), 1),
			Timestamp: now,
		},
		Metric{
			Name:      "node_ebpf_privilege_escalation_attempts_total",
			Type:      "counter",
			Value:     float64(summary.PrivilegeEscalationAttempts),
			Timestamp: now,
		},
		Metric{
			Name:      "node_ebpf_abnormal_bind_ports_count",
			Type:      "gauge",
			Value:     float64(len(summary.AbnormalBindPorts)),
			Timestamp: now,
		},
		Metric{
			Name:      "node_ebpf_long_lived_tcp_connections",
			Type:      "gauge",
			Value:     float64(len(summary.LongLivedTCPConnections)),
			Timestamp: now,
		},
		Metric{
			Name:      "node_ebpf_runtime_mode",
			Type:      "gauge",
			Value:     runtimeModeGauge(summary.RuntimeMode),
			Labels:    map[string]string{"mode": summary.RuntimeMode},
			Timestamp: now,
		},
	)

	for syscall, count := range summary.SyscallStatistics {
		out = append(out, Metric{
			Name:      "node_ebpf_syscall_statistics_total",
			Type:      "counter",
			Value:     float64(count),
			Labels:    map[string]string{"syscall": syscall},
			Timestamp: now,
		})
	}

	for _, ps := range summary.ProcessStats {
		labels := map[string]string{
			"pid":     strconv.Itoa(ps.PID),
			"process": ps.Comm,
		}
		out = append(out,
			Metric{
				Name:      "node_ebpf_process_resource_cpu_user_ms",
				Type:      "gauge",
				Value:     float64(ps.ResourceCPUUserMS),
				Labels:    labels,
				Timestamp: now,
			},
			Metric{
				Name:      "node_ebpf_process_resource_cpu_sys_ms",
				Type:      "gauge",
				Value:     float64(ps.ResourceCPUSysMS),
				Labels:    labels,
				Timestamp: now,
			},
			Metric{
				Name:      "node_ebpf_process_resource_rss_bytes",
				Type:      "gauge",
				Value:     float64(ps.ResourceRSSBytes),
				Labels:    labels,
				Timestamp: now,
			},
		)
		for syscall, count := range ps.Syscalls {
			out = append(out, Metric{
				Name:  "node_ebpf_process_events_total",
				Type:  "counter",
				Value: float64(count),
				Labels: map[string]string{
					"pid":     strconv.Itoa(ps.PID),
					"process": ps.Comm,
					"type":    syscall,
				},
				Timestamp: now,
			})
		}
	}

	return out
}

func sampleWindowSeconds(window time.Duration) float64 {
	if window <= 0 {
		return 10.0
	}
	return window.Seconds()
}

func clampPositiveFloat(v, fallback float64) float64 {
	if v <= 0 {
		return fallback
	}
	return v
}

func runtimeModeGauge(mode string) float64 {
	if mode == "libbpf_ringbuf" {
		return 1
	}
	return 0
}

// SecurityEvent remains for compatibility with legacy integrations.
type SecurityEvent struct {
	Timestamp int64  `json:"timestamp"`
	Type      string `json:"type"`
	PID       int    `json:"pid"`
	Comm      string `json:"comm"`
	Details   string `json:"details"`
}
