package collector

import (
	"math"
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

const (
	collectorMetricsPartialUpdateMetric = "collector_metrics_partial_update"
	collectorMetricsSuppressedCount     = "collector_metrics_suppressed_count"
)

type lowChurnMetricState struct {
	valueBits     uint64
	lastEmittedAt time.Time
}

type lowChurnSuppressionState struct {
	cache map[string]lowChurnMetricState
}

func (c *Collector) suppressUnchangedLowChurnMetrics(now time.Time, metrics []*telemetryv1.Metric) []*telemetryv1.Metric {
	if c == nil || len(metrics) == 0 {
		return metrics
	}
	cfg := c.configSnapshot()
	if !cfg.SuppressUnchangedLowChurnMetrics {
		return metrics
	}
	refreshInterval := cfg.LowChurnMetricsRefreshInterval
	if refreshInterval <= 0 {
		refreshInterval = 5 * time.Minute
	}
	if c.lowChurnState.cache == nil {
		c.lowChurnState.cache = make(map[string]lowChurnMetricState, 64)
	}

	out := make([]*telemetryv1.Metric, 0, len(metrics)+2)
	suppressed := 0
	for _, metric := range metrics {
		if !isLowChurnCollectorMetric(metric) {
			out = append(out, metric)
			continue
		}
		key := metricIdentityKey(metric)
		state, ok := c.lowChurnState.cache[key]
		valueBits := math.Float64bits(metric.GetValue())
		unchanged := ok && state.valueBits == valueBits
		staleRefresh := !ok || state.lastEmittedAt.IsZero() || now.Sub(state.lastEmittedAt) >= refreshInterval
		if unchanged && !staleRefresh {
			suppressed++
			continue
		}
		out = append(out, metric)
		c.lowChurnState.cache[key] = lowChurnMetricState{
			valueBits:     valueBits,
			lastEmittedAt: now,
		}
	}

	out = append(out,
		&telemetryv1.Metric{
			Name:              collectorMetricsPartialUpdateMetric,
			Value:             boolToFloat(suppressed > 0),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              collectorMetricsSuppressedCount,
			Value:             float64(suppressed),
			TimestampUnixNano: now.UnixNano(),
		},
	)
	return out
}

func metricIdentityKey(metric *telemetryv1.Metric) string {
	if metric == nil {
		return ""
	}
	var b strings.Builder
	b.Grow(128)
	b.WriteString(metric.GetName())
	for _, label := range metric.GetLabels() {
		if label == nil {
			continue
		}
		b.WriteString("|")
		b.WriteString(label.GetKey())
		b.WriteString("=")
		b.WriteString(label.GetValue())
	}
	return b.String()
}

func isLowChurnCollectorMetric(metric *telemetryv1.Metric) bool {
	if metric == nil {
		return false
	}
	switch metric.GetName() {
	case "collector_probe_source",
		"collector_runtime_mode",
		"collector_runtime_mode_requested",
		"collector_runtime_mode_degraded",
		"collector_runtime_containerized",
		"collector_runtime_capability_available",
		"collector_runtime_signal_coverage",
		"collector_primary_ebpf_expected",
		"collector_primary_ebpf_healthy",
		"collector_primary_probe_core_expected",
		"collector_primary_probe_core_healthy",
		"collector_compatibility_fallback_active",
		"collector_probe_core_client_available",
		"collector_probe_core_active",
		"collector_probe_core_collector_selection_valid",
		"collector_probe_core_collector_module_requested",
		"collector_probe_core_collector_module_active",
		"collector_hardware_cpu_sockets",
		"collector_hardware_cpu_cores",
		"collector_hardware_cpu_threads",
		"collector_hardware_cpu_numa_nodes",
		"collector_hardware_cpu_hybrid",
		"collector_hardware_storage_devices_total",
		"collector_hardware_network_interfaces_total",
		"collector_hardware_network_high_speed_interfaces_total",
		"collector_hardware_network_max_speed_mbps",
		"collector_hardware_network_rdma_capable",
		"collector_hardware_gpu_devices_total",
		"collector_hardware_storage_class_total",
		"collector_hardware_storage_profile",
		"collector_hardware_network_profile",
		"collector_hardware_gpu_profile":
		return true
	}
	return strings.HasPrefix(metric.GetName(), "collector_hardware_capability_") ||
		strings.HasPrefix(metric.GetName(), "collector_hardware_threshold_")
}
