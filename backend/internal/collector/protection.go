package collector

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/transport"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/safeconv"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

type protectionMode int

const (
	protectionModeNormal protectionMode = iota
	protectionModeIncident
	protectionModePressure
	protectionModeCritical
)

type collectorSelfSample struct {
	CPUPercent     float64
	CPUTimeDelta   time.Duration
	CPUTimeTotal   time.Duration
	RSSBytes       uint64
	HeapAllocBytes uint64
	Goroutines     int
}

type transportPressureSnapshot struct {
	SendMs      float64
	AckMs       float64
	Errors      uint64
	Retries     uint64
	LastErrKind string
}

type hardwareAnomalySnapshot struct {
	CPU     float64
	Memory  float64
	Disk    float64
	GPU     float64
	Network float64
}

type protectionDecision struct {
	Mode                protectionMode
	SignalPressure      int
	BackpressureActive  bool
	BacklogRatio        float64
	CPUBudgetRatio      float64
	CPUTimeBudgetRatio  float64
	MemoryBudgetRatio   float64
	DisableLogs         bool
	DisableSecurity     bool
	DisableExternal     bool
	SkipProcessFallback bool
	MaxDrainRecords     int
	MaxDrainDuration    time.Duration
	Anomalies           hardwareAnomalySnapshot
}

type protectionGovernor struct {
	logger *zap.Logger

	mu           sync.Mutex
	lastSampleAt time.Time
	lastCPUTime  time.Duration
}

func newProtectionGovernor(logger *zap.Logger) *protectionGovernor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &protectionGovernor{
		logger: logger.With(zap.String("component", "collector_protection")),
	}
}

func applyCurrentProcessNice(nice int, logger *zap.Logger) {
	if nice == 0 {
		return
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	if err := unix.Setpriority(unix.PRIO_PROCESS, 0, nice); err != nil {
		logger.Debug("failed to lower collector scheduling priority",
			zap.Int("nice", nice),
			zap.Error(err),
		)
	}
}

func (m protectionMode) String() string {
	switch m {
	case protectionModeIncident:
		return "incident"
	case protectionModePressure:
		return "pressure"
	case protectionModeCritical:
		return "critical"
	default:
		return "normal"
	}
}

func (m protectionMode) Severity() float64 {
	switch m {
	case protectionModeIncident:
		return 1
	case protectionModePressure:
		return 2
	case protectionModeCritical:
		return 3
	default:
		return 0
	}
}

func (g *protectionGovernor) Sample(now time.Time) collectorSelfSample {
	var sample collectorSelfSample
	sample.CPUTimeTotal = readProcessCPUTime()
	sample.RSSBytes = readProcessRSSBytes()
	sample.Goroutines = runtime.NumGoroutine()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	sample.HeapAllocBytes = mem.Alloc

	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.lastSampleAt.IsZero() && sample.CPUTimeTotal >= g.lastCPUTime {
		sample.CPUTimeDelta = sample.CPUTimeTotal - g.lastCPUTime
		elapsed := now.Sub(g.lastSampleAt)
		if elapsed > 0 {
			sample.CPUPercent = float64(sample.CPUTimeDelta) / float64(elapsed) * 100.0
			if sample.CPUPercent < 0 {
				sample.CPUPercent = 0
			}
		}
	}
	g.lastSampleAt = now
	g.lastCPUTime = sample.CPUTimeTotal
	return sample
}

func (g *protectionGovernor) Decide(
	cfg ProtectionConfig,
	transportStats transportPressureSnapshot,
	spoolSnapshot spool.Snapshot,
	metrics []*telemetryv1.Metric,
	profile hardwareProfile,
	self collectorSelfSample,
) protectionDecision {
	if !cfg.Enabled {
		return protectionDecision{
			Mode:             protectionModeNormal,
			MaxDrainRecords:  maxPositiveInt(cfg.MaxDrainRecordsPerCycle, 1),
			MaxDrainDuration: cfg.MaxDrainDuration,
		}
	}
	thresholds := profile.Threshold
	anomalies := detectHardwareAnomalies(metrics, profile)
	backlogRatio := 0.0
	if spoolSnapshot.MaxBytes > 0 {
		backlogRatio = clamp01(float64(spoolSnapshot.BacklogBytes) / float64(spoolSnapshot.MaxBytes))
	}

	cpuBudgetRatio := ratioOrZero(self.CPUPercent, cfg.MaxCPUPercent)
	cpuTimeBudgetRatio := durationRatio(self.CPUTimeDelta, cfg.MaxCPUTimePerInterval)
	memoryBudgetRatio := ratioOrZero(float64(self.RSSBytes), float64(cfg.MemorySoftLimitBytes))
	backpressureActive := transportStats.Retries > 0 ||
		transportStats.Errors > 0 ||
		strings.TrimSpace(transportStats.LastErrKind) != ""

	pressureCount := anomalies.PressureCount()
	criticalSignals := anomalies.CriticalCount()
	mode := protectionModeNormal
	switch {
	case backlogRatio >= cfg.SpoolCriticalWatermarkRatio ||
		cpuTimeBudgetRatio >= 2.0 ||
		memoryBudgetRatio >= 1.25 ||
		criticalSignals >= 1:
		mode = protectionModeCritical
	case backlogRatio >= cfg.SpoolHighWatermarkRatio ||
		cpuBudgetRatio > 1.0 ||
		cpuTimeBudgetRatio > 1.0 ||
		memoryBudgetRatio > 1.0 ||
		backpressureActive ||
		pressureCount >= 2:
		mode = protectionModePressure
	case pressureCount >= 1:
		mode = protectionModeIncident
	}

	if self.CPUPercent >= thresholds.CPUCriticalPercent ||
		memoryPercentFromMetrics(metrics) >= thresholds.MemoryCriticalPercent {
		mode = protectionModeCritical
	}

	decision := protectionDecision{
		Mode:                mode,
		SignalPressure:      pressureCount,
		BackpressureActive:  backpressureActive,
		BacklogRatio:        backlogRatio,
		CPUBudgetRatio:      cpuBudgetRatio,
		CPUTimeBudgetRatio:  cpuTimeBudgetRatio,
		MemoryBudgetRatio:   memoryBudgetRatio,
		MaxDrainRecords:     maxPositiveInt(cfg.MaxDrainRecordsPerCycle, 1),
		MaxDrainDuration:    cfg.MaxDrainDuration,
		Anomalies:           anomalies,
		SkipProcessFallback: mode >= protectionModePressure,
	}

	if mode >= protectionModePressure {
		decision.DisableLogs = cfg.DisableLogsUnderPressure
		decision.DisableSecurity = cfg.DisableSecurityUnderPressure
		decision.DisableExternal = cfg.DisableExternalUnderPressure
	}
	if mode == protectionModeCritical {
		decision.MaxDrainRecords = maxPositiveInt(cfg.MaxDrainRecordsPerCycle/2, 1)
		if cfg.MaxDrainDuration > 250*time.Millisecond {
			decision.MaxDrainDuration = cfg.MaxDrainDuration / 2
		}
	}
	if decision.MaxDrainDuration <= 0 {
		decision.MaxDrainDuration = 250 * time.Millisecond
	}
	return decision
}

func detectHardwareAnomalies(metrics []*telemetryv1.Metric, profile hardwareProfile) hardwareAnomalySnapshot {
	thresholds := profile.Threshold
	cpuUsage := metricValueAny(metrics, "node_cpu_usage_percent", "probe_core_cpu_usage_percent")
	cpuIOWait := metricValueAny(metrics, "node_cpu_iowait_percent")
	runQueue := metricValueAny(metrics, "node_procs_running", "probe_core_sched_running_tasks")
	cpuThrottle := metricValueAny(metrics, "probe_core_cgroup_cpu_throttled_ratio")
	cpuScore := maxFloat(
		overThresholdScore(cpuUsage, thresholds.CPUBusyPercent, 0.35),
		overThresholdScore(cpuIOWait, 12, 1),
		overThresholdScore(cpuThrottle, 0.10, 1),
		overThresholdScore(runQueue, float64(maxPositiveInt(profile.CPU.Threads, 1)), 0.5),
	)

	memPercent := memoryPercentFromMetrics(metrics)
	memPressure := metricValueAny(metrics,
		"node_pressure_memory_some_avg10",
		"node_pressure_memory_full_avg10",
		"probe_core_pressure_memory_some_avg10",
		"probe_core_pressure_memory_full_avg10",
	)
	numaMiss := metricValueAny(metrics, "node_numa_miss_ratio_percent")
	memScore := maxFloat(
		overThresholdScore(memPercent, thresholds.MemoryPressurePercent, 0.25),
		overThresholdScore(memPressure, 10, 1),
		overThresholdScore(numaMiss, 5, 1),
	)

	diskLatency := metricValueAny(metrics, "node_disk_request_latency_p99_seconds", "node_disk_avg_request_latency_seconds")
	diskQueue := metricValueAny(metrics, "node_disk_queue_depth_total", "probe_core_disk_queue_depth")
	ioPressure := metricValueAny(metrics,
		"node_pressure_io_some_avg10",
		"node_pressure_io_full_avg10",
		"probe_core_pressure_io_some_avg10",
		"probe_core_pressure_io_full_avg10",
	)
	diskScore := maxFloat(
		overThresholdScore(diskLatency, thresholds.DiskLatencySeconds, 1),
		overThresholdScore(diskQueue, thresholds.DiskQueueDepth, 1),
		overThresholdScore(ioPressure, thresholds.IOPressurePercent, 1),
	)

	gpuUtil := metricValueAny(metrics, "node_gpu_utilization_sm_avg_percent")
	gpuProcesses := metricValueAny(metrics, "node_gpu_process_total")
	gpuMemPressure := metricValueAny(metrics, "node_gpu_memory_used_percent", "probe_core_gpu_memory_pressure_percent")
	gpuThrottle := metricValueAny(metrics, "node_gpu_throttle_active_any", "node_gpu_throttle_thermal_any", "node_gpu_throttle_power_any")
	gpuScore := maxFloat(
		scaledBoolScore(gpuProcesses > 0 && gpuUtil > 0 && gpuUtil < thresholds.GPULowUtilPercent, 0.45),
		overThresholdScore(gpuMemPressure, thresholds.GPUMemoryPressurePct, 0.5),
		boolScore(gpuThrottle > 0),
	)

	retransRatio := metricValueAny(metrics, "node_tcp_retransmit_ratio")
	retransmits := metricValueAny(metrics, "node_tcp_retransmits_per_second", "probe_core_network_tcp_retransmissions_per_sec")
	softnetDrops := metricValueAny(metrics, "node_softnet_dropped_per_second", "probe_core_network_softnet_dropped_total")
	netErrs := metricValueAny(metrics,
		"node_network_total_errs_per_second",
		"node_network_receive_errs_per_second",
		"node_network_transmit_errs_per_second",
	)
	rdmaCongestion := metricValueAny(metrics, "node_rdma_congestion_events_per_second")
	netScore := maxFloat(
		overThresholdScore(retransRatio, thresholds.NetworkRetransmitRatio, 1),
		overThresholdScore(retransmits, 0.5, 2),
		overThresholdScore(softnetDrops, thresholds.NetworkSoftnetDrops+1, 2),
		overThresholdScore(netErrs, 1, 1),
		overThresholdScore(rdmaCongestion, 10, 2),
	)

	return hardwareAnomalySnapshot{
		CPU:     cpuScore,
		Memory:  memScore,
		Disk:    diskScore,
		GPU:     gpuScore,
		Network: netScore,
	}
}

func (a hardwareAnomalySnapshot) PressureCount() int {
	count := 0
	for _, value := range []float64{a.CPU, a.Memory, a.Disk, a.GPU, a.Network} {
		if value >= 0.35 {
			count++
		}
	}
	return count
}

func (a hardwareAnomalySnapshot) CriticalCount() int {
	count := 0
	for _, value := range []float64{a.CPU, a.Memory, a.Disk, a.GPU, a.Network} {
		if value >= 0.85 {
			count++
		}
	}
	return count
}

func appendProtectionMetrics(now time.Time, decision protectionDecision, self collectorSelfSample, metrics *[]*telemetryv1.Metric) {
	if metrics == nil {
		return
	}
	ts := now.UnixNano()
	*metrics = append(*metrics,
		&telemetryv1.Metric{Name: "collector_self_cpu_percent", Value: self.CPUPercent, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_self_cpu_time_seconds_total", Value: self.CPUTimeTotal.Seconds(), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_self_rss_bytes", Value: float64(self.RSSBytes), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_self_heap_alloc_bytes", Value: float64(self.HeapAllocBytes), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_self_goroutines", Value: float64(self.Goroutines), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_mode_severity", Value: decision.Mode.Severity(), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_signal_pressure", Value: float64(decision.SignalPressure), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_backpressure_active", Value: boolToFloat(decision.BackpressureActive), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_spool_fill_ratio", Value: decision.BacklogRatio, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_cpu_budget_ratio", Value: decision.CPUBudgetRatio, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_cpu_time_budget_ratio", Value: decision.CPUTimeBudgetRatio, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_memory_budget_ratio", Value: decision.MemoryBudgetRatio, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_drain_records_limit", Value: float64(decision.MaxDrainRecords), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_protection_drain_duration_seconds", Value: decision.MaxDrainDuration.Seconds(), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_cpu_anomaly_score", Value: decision.Anomalies.CPU, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_memory_anomaly_score", Value: decision.Anomalies.Memory, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_disk_anomaly_score", Value: decision.Anomalies.Disk, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_gpu_anomaly_score", Value: decision.Anomalies.GPU, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_network_anomaly_score", Value: decision.Anomalies.Network, TimestampUnixNano: ts},
		&telemetryv1.Metric{
			Name:              "collector_protection_mode",
			Value:             1,
			TimestampUnixNano: ts,
			Labels:            buildLabels(map[string]string{"mode": decision.Mode.String()}),
		},
	)
	for component, enabled := range map[string]bool{
		"logs":             decision.DisableLogs,
		"security":         decision.DisableSecurity,
		"external":         decision.DisableExternal,
		"process_fallback": decision.SkipProcessFallback,
	} {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_protection_load_shed",
			Value:             boolToFloat(enabled),
			TimestampUnixNano: ts,
			Labels:            buildLabels(map[string]string{"component": component}),
		})
	}
}

func transportSnapshotFromClient(client *transport.Client) transportPressureSnapshot {
	if client == nil {
		return transportPressureSnapshot{}
	}
	return transportPressureSnapshot{
		SendMs:      client.LastSendMs(),
		AckMs:       client.LastAckMs(),
		Errors:      client.LastErrs(),
		Retries:     client.LastRetries(),
		LastErrKind: client.LastErrorKind(),
	}
}

func readProcessCPUTime() time.Duration {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	return time.Duration(usage.Utime.Sec)*time.Second +
		time.Duration(usage.Utime.Usec)*time.Microsecond +
		time.Duration(usage.Stime.Sec)*time.Second +
		time.Duration(usage.Stime.Usec)*time.Microsecond
}

func readProcessRSSBytes() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return safeconv.MultiplyUint64(pages, safeconv.NonNegativeIntToUint64(os.Getpagesize()))
}

func ratioOrZero(value, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return value / limit
}

func durationRatio(value, limit time.Duration) float64 {
	if limit <= 0 {
		return 0
	}
	return float64(value) / float64(limit)
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func scaledBoolScore(value bool, score float64) float64 {
	if value {
		return score
	}
	return 0
}

func overThresholdScore(value, threshold, span float64) float64 {
	if threshold <= 0 {
		return 0
	}
	if value <= threshold {
		return 0
	}
	if span <= 0 {
		span = 1
	}
	return clamp01((value - threshold) / (threshold * span))
}

func maxFloat(values ...float64) float64 {
	best := 0.0
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
