package analysis

import (
	"sort"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

// EvidencePack is a compact, token-efficient payload for LLM use.
type EvidencePack struct {
	SchemaVersion string             `json:"schema_version"`
	NodeName      string             `json:"node_name"`
	Summary       map[string]float64 `json:"summary"`
	TopMetrics    []MetricSummary    `json:"top_metrics"`
	GPU           map[string]float64 `json:"gpu"`
	Network       map[string]float64 `json:"network"`
	Disk          map[string]float64 `json:"disk"`
	Memory        map[string]float64 `json:"memory"`
	Processes     []ProcessSummary   `json:"processes"`
	Logs          []LogSummary       `json:"logs"`
	Alerts        []string           `json:"alerts"`
	Anomalies     []string           `json:"anomalies"`
	Context       string             `json:"context"`
}

type MetricSummary struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type ProcessSummary struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_percent"`
	RSSBytes   uint64  `json:"rss_bytes"`
	IOReadBps  float64 `json:"io_read_bps"`
	IOWriteBps float64 `json:"io_write_bps"`
}

type LogSummary struct {
	Fingerprint string `json:"fingerprint"`
	Count       uint64 `json:"count"`
	Example     string `json:"example"`
}

// BuildEvidencePack builds a compact evidence pack from snapshots.
func BuildEvidencePack(nodeName string, metrics map[string]float64, alerts []string, anomalies []string, context string, processes []ProcessSummary, logs []LogSummary) EvidencePack {
	summary := pickMetrics(metrics, []string{
		"node_cpu_usage_percent",
		"node_load1",
		"node_memory_Used_bytes",
		"node_memory_MemTotal_bytes",
		"node_disk_read_bytes_per_second",
		"node_disk_written_bytes_per_second",
		"node_network_receive_bytes_per_second",
		"node_network_transmit_bytes_per_second",
	})

	gpu := pickMetrics(metrics, []string{
		"node_gpu_utilization_sm_avg_percent",
		"node_gpu_memory_used_percent",
		"node_gpu_temperature_max_celsius",
		"node_gpu_power_draw_total_watts",
		"node_gpu_throttle_active_any",
	})

	network := pickMetrics(metrics, []string{
		"node_network_receive_bytes_per_second",
		"node_network_transmit_bytes_per_second",
		"node_network_receive_errs_total",
		"node_network_transmit_errs_total",
	})

	disk := pickMetrics(metrics, []string{
		"node_disk_read_bytes_per_second",
		"node_disk_written_bytes_per_second",
		"node_disk_io_now",
	})

	memory := pickMetrics(metrics, []string{
		"node_memory_Used_bytes",
		"node_memory_MemTotal_bytes",
		"node_memory_MemAvailable_bytes",
		"node_vmstat_pswpout",
		"node_vmstat_oom_kill",
	})

	topMetrics := topN(metrics, 12)

	return EvidencePack{
		SchemaVersion: "v1",
		NodeName:      nodeName,
		Summary:       summary,
		TopMetrics:    topMetrics,
		GPU:           gpu,
		Network:       network,
		Disk:          disk,
		Memory:        memory,
		Processes:     processes,
		Logs:          logs,
		Alerts:        alerts,
		Anomalies:     anomalies,
		Context:       context,
	}
}

func pickMetrics(metrics map[string]float64, keys []string) map[string]float64 {
	out := make(map[string]float64, len(keys))
	for _, key := range keys {
		if value, ok := metrics[key]; ok {
			out[key] = value
		}
	}
	return out
}

func topN(metrics map[string]float64, n int) []MetricSummary {
	if n <= 0 || len(metrics) == 0 {
		return nil
	}
	items := make([]MetricSummary, 0, len(metrics))
	for name, value := range metrics {
		items = append(items, MetricSummary{Name: name, Value: value})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Value > items[j].Value
	})
	if len(items) > n {
		items = items[:n]
	}
	return items
}

func SummarizeProcesses(samples []*telemetryv1.ProcessSample, limit int) []ProcessSummary {
	if limit <= 0 {
		limit = 5
	}
	out := make([]ProcessSummary, 0, limit)
	for _, sample := range samples {
		out = append(out, ProcessSummary{
			PID:        sample.Pid,
			Name:       sample.Name,
			CPUPercent: sample.CpuPercent,
			RSSBytes:   sample.RssBytes,
			IOReadBps:  sample.IoReadBps,
			IOWriteBps: sample.IoWriteBps,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func SummarizeLogs(samples []*telemetryv1.LogFingerprint, limit int) []LogSummary {
	if limit <= 0 {
		limit = 5
	}
	out := make([]LogSummary, 0, limit)
	for _, sample := range samples {
		out = append(out, LogSummary{
			Fingerprint: sample.Fingerprint,
			Count:       sample.Count,
			Example:     sample.Example,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}
