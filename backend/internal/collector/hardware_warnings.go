package collector

import (
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

const collectorHardwareWarningMetric = "collector_hardware_warning"

type hardwareWarning struct {
	domain string
	reason string
	signal string
	score  float64
}

func appendHardwareWarningMetrics(now time.Time, metrics *[]*telemetryv1.Metric, observed []*telemetryv1.Metric, profile hardwareProfile) {
	if metrics == nil {
		return
	}
	warnings := detectHardwareWarnings(observed, profile)
	ts := now.UnixNano()
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_hardware_warning_total",
		Value:             float64(len(warnings)),
		TimestampUnixNano: ts,
	})
	for _, warning := range warnings {
		labels := map[string]string{
			"domain": warning.domain,
			"reason": warning.reason,
		}
		if signal := strings.TrimSpace(warning.signal); signal != "" {
			labels["signal"] = signal
		}
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              collectorHardwareWarningMetric,
			Value:             warning.score,
			TimestampUnixNano: ts,
			Labels:            buildLabels(labels),
		})
	}
}

func detectHardwareWarnings(metrics []*telemetryv1.Metric, profile hardwareProfile) []hardwareWarning {
	thresholds := profile.Threshold
	warnings := make([]hardwareWarning, 0, 5)

	cpuThrottle := metricValueAny(metrics, "probe_core_cgroup_cpu_throttled_ratio")
	cpuIOWait := metricValueAny(metrics, "node_cpu_iowait_percent")
	cpuUsage := metricValueAny(metrics, "node_cpu_usage_percent", "probe_core_cpu_usage_percent")
	runQueue := metricValueAny(metrics, "node_procs_running", "probe_core_sched_running_tasks")
	switch {
	case cpuThrottle >= 0.10:
		warnings = append(warnings, hardwareWarning{domain: "cpu", reason: "throttled", signal: "probe_core_cgroup_cpu_throttled_ratio", score: clamp01(cpuThrottle)})
	case cpuIOWait >= 12:
		warnings = append(warnings, hardwareWarning{domain: "cpu", reason: "iowait", signal: "node_cpu_iowait_percent", score: overThresholdScore(cpuIOWait, 12, 1)})
	case cpuUsage >= thresholds.CPUBusyPercent && runQueue >= float64(maxPositiveInt(profile.CPU.Threads, 1)):
		warnings = append(warnings, hardwareWarning{domain: "cpu", reason: "contention", signal: "node_cpu_usage_percent", score: overThresholdScore(cpuUsage, thresholds.CPUBusyPercent, 0.35)})
	}

	memPressure := metricValueAny(metrics,
		"node_pressure_memory_some_avg10",
		"node_pressure_memory_full_avg10",
		"probe_core_pressure_memory_some_avg10",
		"probe_core_pressure_memory_full_avg10",
	)
	numaMiss := metricValueAny(metrics, "node_numa_miss_ratio_percent")
	memPercent := memoryPercentFromMetrics(metrics)
	switch {
	case numaMiss >= 5:
		warnings = append(warnings, hardwareWarning{domain: "memory", reason: "numa_imbalance", signal: "node_numa_miss_ratio_percent", score: overThresholdScore(numaMiss, 5, 1)})
	case memPressure >= 10:
		warnings = append(warnings, hardwareWarning{domain: "memory", reason: "pressure", signal: "node_pressure_memory_some_avg10", score: overThresholdScore(memPressure, 10, 1)})
	case memPercent >= thresholds.MemoryPressurePercent:
		warnings = append(warnings, hardwareWarning{domain: "memory", reason: "capacity", signal: "node_memory_Used_bytes", score: overThresholdScore(memPercent, thresholds.MemoryPressurePercent, 0.25)})
	}

	diskLatency := metricValueAny(metrics, "node_disk_request_latency_p99_seconds", "node_disk_avg_request_latency_seconds")
	diskQueue := metricValueAny(metrics, "node_disk_queue_depth_total", "probe_core_disk_queue_depth")
	ioPressure := metricValueAny(metrics,
		"node_pressure_io_some_avg10",
		"node_pressure_io_full_avg10",
		"probe_core_pressure_io_some_avg10",
		"probe_core_pressure_io_full_avg10",
	)
	switch {
	case diskLatency >= thresholds.DiskLatencySeconds:
		warnings = append(warnings, hardwareWarning{domain: "disk", reason: "latency", signal: "node_disk_request_latency_p99_seconds", score: overThresholdScore(diskLatency, thresholds.DiskLatencySeconds, 1)})
	case diskQueue >= thresholds.DiskQueueDepth:
		warnings = append(warnings, hardwareWarning{domain: "disk", reason: "queue_depth", signal: "node_disk_queue_depth_total", score: overThresholdScore(diskQueue, thresholds.DiskQueueDepth, 1)})
	case ioPressure >= thresholds.IOPressurePercent:
		warnings = append(warnings, hardwareWarning{domain: "disk", reason: "io_pressure", signal: "node_pressure_io_some_avg10", score: overThresholdScore(ioPressure, thresholds.IOPressurePercent, 1)})
	}

	retransRatio := metricValueAny(metrics, "node_tcp_retransmit_ratio")
	softnetDrops := metricValueAny(metrics, "node_softnet_dropped_per_second", "probe_core_network_softnet_dropped_total")
	netErrs := metricValueAny(metrics,
		"node_network_total_errs_per_second",
		"node_network_receive_errs_per_second",
		"node_network_transmit_errs_per_second",
	)
	rdmaCongestion := metricValueAny(metrics, "node_rdma_congestion_events_per_second")
	switch {
	case rdmaCongestion >= 10:
		warnings = append(warnings, hardwareWarning{domain: "network", reason: "rdma_congestion", signal: "node_rdma_congestion_events_per_second", score: overThresholdScore(rdmaCongestion, 10, 2)})
	case softnetDrops > thresholds.NetworkSoftnetDrops:
		warnings = append(warnings, hardwareWarning{domain: "network", reason: "softnet_drop", signal: "node_softnet_dropped_per_second", score: overThresholdScore(softnetDrops, thresholds.NetworkSoftnetDrops+1, 2)})
	case retransRatio >= thresholds.NetworkRetransmitRatio:
		warnings = append(warnings, hardwareWarning{domain: "network", reason: "retransmit", signal: "node_tcp_retransmit_ratio", score: overThresholdScore(retransRatio, thresholds.NetworkRetransmitRatio, 1)})
	case netErrs >= 1:
		warnings = append(warnings, hardwareWarning{domain: "network", reason: "errors", signal: "node_network_total_errs_per_second", score: overThresholdScore(netErrs, 1, 1)})
	}

	gpuThrottle := metricValueAny(metrics, "node_gpu_throttle_active_any", "node_gpu_throttle_thermal_any", "node_gpu_throttle_power_any")
	gpuMemPressure := metricValueAny(metrics, "node_gpu_memory_used_percent", "probe_core_gpu_memory_pressure_percent")
	gpuUtil := metricValueAny(metrics, "node_gpu_utilization_sm_avg_percent")
	gpuProcesses := metricValueAny(metrics, "node_gpu_process_total")
	switch {
	case gpuThrottle > 0:
		warnings = append(warnings, hardwareWarning{domain: "gpu", reason: "throttle", signal: "node_gpu_throttle_active_any", score: clamp01(gpuThrottle)})
	case gpuMemPressure >= thresholds.GPUMemoryPressurePct:
		warnings = append(warnings, hardwareWarning{domain: "gpu", reason: "memory_pressure", signal: "node_gpu_memory_used_percent", score: overThresholdScore(gpuMemPressure, thresholds.GPUMemoryPressurePct, 0.5)})
	case gpuProcesses > 0 && gpuUtil > 0 && gpuUtil < thresholds.GPULowUtilPercent:
		warnings = append(warnings, hardwareWarning{domain: "gpu", reason: "low_util", signal: "node_gpu_utilization_sm_avg_percent", score: 0.45})
	}

	return warnings
}
