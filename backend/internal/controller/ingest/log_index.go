package ingest

import (
	"math"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

var logMetricSnapshotKeys = []string{
	"node_cpu_usage_percent",
	"node_cpu_iowait_percent",
	"node_load1",
	"node_load5",
	"node_load15",
	"node_pressure_cpu_some_avg10",
	"node_pressure_io_some_avg10",
	"node_pressure_io_full_avg10",
	"node_network_total_receive_bytes_per_second",
	"node_network_total_transmit_bytes_per_second",
	"node_network_receive_bytes_per_second",
	"node_network_transmit_bytes_per_second",
	"node_disk_total_iops_per_second",
	"node_disk_queue_depth_total",
	"node_disk_utilization_peak_percent",
	"node_disk_avg_request_latency_seconds",
	"node_gpu_utilization_sm_avg_percent",
}

func (s *MemoryStore) indexLogFingerprintsLocked(node *NodeSnapshot, collectorID string, fingerprints []*telemetryv1.LogFingerprint, receivedAt time.Time) {
	if s == nil || s.logIndex == nil || len(fingerprints) == 0 {
		return
	}

	hostname := collectorID
	if node != nil && strings.TrimSpace(node.Hostname) != "" {
		hostname = strings.TrimSpace(node.Hostname)
	}
	serviceHint := ""
	sourceHint := "probe"
	if node != nil {
		serviceHint = firstNonEmpty(
			strings.TrimSpace(node.Labels["service"]),
			strings.TrimSpace(node.Labels["app"]),
			strings.TrimSpace(node.Labels["unit"]),
		)
		sourceHint = firstNonEmpty(
			strings.TrimSpace(node.Labels["log_source"]),
			strings.TrimSpace(node.Labels["source"]),
			sourceHint,
		)
	}

	metricSnapshot := buildLogMetricSnapshot(node)

	events := make([]logindex.RawEvent, 0, len(fingerprints))
	for _, fingerprint := range fingerprints {
		if fingerprint == nil {
			continue
		}
		message := strings.TrimSpace(fingerprint.Example)
		if message == "" {
			continue
		}

		timestamp := receivedAt
		if fingerprint.TimestampUnixNano > 0 {
			timestamp = time.Unix(0, fingerprint.TimestampUnixNano)
		}
		labels := map[string]string{
			"pipeline": "probe",
		}
		if fp := strings.TrimSpace(fingerprint.Fingerprint); fp != "" {
			labels["fingerprint"] = fp
		}
		if serviceHint != "" {
			labels["service_hint"] = serviceHint
		}

		event := logindex.RawEvent{
			Timestamp:      timestamp,
			CollectorID:    collectorID,
			Hostname:       hostname,
			Service:        serviceHint,
			Source:         sourceHint,
			Message:        message,
			Fingerprint:    strings.TrimSpace(fingerprint.Fingerprint),
			Count:          maxUint64(1, fingerprint.Count),
			Labels:         labels,
			MetricSnapshot: metricSnapshot,
		}
		events = append(events, event)
	}

	if len(events) > 0 {
		s.logIndex.AddBatch(events)
	}
}

func buildLogMetricSnapshot(node *NodeSnapshot) map[string]float64 {
	if node == nil || len(node.Metrics) == 0 {
		return nil
	}

	snapshot := make(map[string]float64, 12)
	for _, key := range logMetricSnapshotKeys {
		if value, ok := node.Metrics[key]; ok && finiteMetricValue(value) {
			snapshot[key] = value
		}
	}

	if _, ok := snapshot["memory_used_percent"]; !ok {
		total := node.Metrics["node_memory_MemTotal_bytes"]
		available := node.Metrics["node_memory_MemAvailable_bytes"]
		if total > 0 && available >= 0 && total >= available {
			usedPercent := ((total - available) / total) * 100.0
			if finiteMetricValue(usedPercent) {
				snapshot["memory_used_percent"] = usedPercent
			}
		}
	}

	rx := firstFinite(
		node.Metrics["node_network_total_receive_bytes_per_second"],
		node.Metrics["node_network_receive_bytes_per_second"],
	)
	tx := firstFinite(
		node.Metrics["node_network_total_transmit_bytes_per_second"],
		node.Metrics["node_network_transmit_bytes_per_second"],
	)
	if finiteMetricValue(rx) || finiteMetricValue(tx) {
		snapshot["network_total_bytes_per_second"] = maxZero(rx) + maxZero(tx)
	}

	if latencySeconds, ok := snapshot["node_disk_avg_request_latency_seconds"]; ok {
		snapshot["node_disk_avg_request_latency_ms"] = latencySeconds * 1000.0
	}

	if len(snapshot) == 0 {
		return nil
	}
	return snapshot
}

func finiteMetricValue(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func firstFinite(values ...float64) float64 {
	for _, value := range values {
		if finiteMetricValue(value) {
			return value
		}
	}
	return 0
}

func maxZero(value float64) float64 {
	if !finiteMetricValue(value) || value < 0 {
		return 0
	}
	return value
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
