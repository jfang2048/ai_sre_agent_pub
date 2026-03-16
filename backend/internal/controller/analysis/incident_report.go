package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// IncidentClass groups incidents into deterministic RCA buckets.
type IncidentClass string

const (
	IncidentClassResourceSaturation   IncidentClass = "resource_saturation"
	IncidentClassIOBottleneck         IncidentClass = "io_bottleneck"
	IncidentClassCommunicationCongest IncidentClass = "communication_congestion"
	IncidentClassMemoryPressure       IncidentClass = "memory_pressure"
	IncidentClassSchedulingContention IncidentClass = "scheduling_contention"
	IncidentClassGPUStarvation        IncidentClass = "gpu_starvation"
	IncidentClassDataPipelineStall    IncidentClass = "data_pipeline_stall"
	IncidentClassStorageBottleneck    IncidentClass = "storage_bottleneck"
	IncidentClassUnknown              IncidentClass = "unknown"
)

// SignalEvidence is a normalized evidence row used in incident reports.
type SignalEvidence struct {
	Source   string  `json:"source"` // alert|anomaly|correlation|log
	Signal   string  `json:"signal"`
	Metric   string  `json:"metric,omitempty"`
	Value    float64 `json:"value,omitempty"`
	Expected float64 `json:"expected,omitempty"`
	Trend    string  `json:"trend,omitempty"`
	Details  string  `json:"details,omitempty"`
}

// IncidentReport is a structured, explainable AIOps output.
type IncidentReport struct {
	ID                 string           `json:"id"`
	NodeName           string           `json:"node_name"`
	Classification     IncidentClass    `json:"classification"`
	Severity           Severity         `json:"severity"`
	Status             string           `json:"status"`
	WhatHappened       string           `json:"what_happened"`
	ProbableCause      string           `json:"probable_cause"`
	Confidence         float64          `json:"confidence"`
	ImpactedComponents []string         `json:"impacted_components,omitempty"`
	SupportingSignals  []SignalEvidence `json:"supporting_signals,omitempty"`
	CorrelatedMetrics  []Correlation    `json:"correlated_metrics,omitempty"`
	RelatedAlertIDs    []string         `json:"related_alert_ids,omitempty"`
	RelatedRCAID       string           `json:"related_rca_id,omitempty"`
	SuggestedActions   []string         `json:"suggested_actions,omitempty"`
	PrimaryMetric      string           `json:"primary_metric,omitempty"`
	LogQuery           string           `json:"log_query,omitempty"`
	WindowStart        time.Time        `json:"window_start"`
	WindowEnd          time.Time        `json:"window_end"`
	GeneratedAt        time.Time        `json:"generated_at"`
}

func (e *Engine) buildIncidentReports() {
	activeAlertsByNode := make(map[string][]*Alert)
	for _, alert := range e.alerts {
		if alert == nil || alert.ResolvedAt != nil {
			continue
		}
		activeAlertsByNode[alert.NodeName] = append(activeAlertsByNode[alert.NodeName], alert)
	}

	anomaliesByNode := make(map[string][]Anomaly)
	for _, anomaly := range e.anomalies {
		anomaliesByNode[anomaly.NodeName] = append(anomaliesByNode[anomaly.NodeName], anomaly)
	}

	correlationsByNode := make(map[string][]Correlation)
	for _, correlation := range e.correlations {
		correlationsByNode[correlation.NodeName] = append(correlationsByNode[correlation.NodeName], correlation)
	}

	latestRCAByNode := make(map[string]RootCauseAnalysis)
	for _, rca := range e.rcaResults {
		prev, ok := latestRCAByNode[rca.NodeName]
		if !ok || rca.AnalyzedAt.After(prev.AnalyzedAt) {
			latestRCAByNode[rca.NodeName] = rca
		}
	}

	nodeSet := make(map[string]struct{})
	for nodeName := range activeAlertsByNode {
		nodeSet[nodeName] = struct{}{}
	}
	for nodeName := range anomaliesByNode {
		nodeSet[nodeName] = struct{}{}
	}
	for nodeName := range correlationsByNode {
		nodeSet[nodeName] = struct{}{}
	}

	reports := make([]IncidentReport, 0, len(nodeSet))
	for nodeName := range nodeSet {
		data := e.nodeData[nodeName]
		if data == nil {
			continue
		}
		metrics := latestMetricSnapshot(data)
		trends := latestTrendSnapshot(data)
		alerts := activeAlertsByNode[nodeName]
		anomalies := anomaliesByNode[nodeName]
		correlations := correlationsByNode[nodeName]

		var processes []ProcessSummary
		var logs []LogSummary
		if e.evidence != nil {
			processes, logs = e.evidence.EvidenceForNode(nodeName)
		}

		classification, probableCause, baseConfidence, primaryMetric, actions := classifyIncident(metrics, trends, anomalies, correlations, logs)
		severity := deriveIncidentSeverity(alerts, anomalies)
		impacted := buildImpactedComponents(metrics, anomalies, processes, logs)
		supportingSignals, logIndicators := buildSupportingSignals(alerts, anomalies, correlations, logs, trends)

		if rca, ok := latestRCAByNode[nodeName]; ok {
			if probableCause == "" || probableCause == "insufficient correlated evidence" {
				probableCause = rca.RootCause
			}
			if len(actions) == 0 {
				actions = append(actions, rca.Recommendations...)
			}
		}

		windowStart, windowEnd := incidentWindow(alerts, anomalies)
		confidence := clamp01(baseConfidence + 0.03*float64(len(supportingSignals)))
		what := fmt.Sprintf("%s detected on %s (%d active alerts, %d anomalies, %d strong correlations)",
			incidentClassTitle(classification), nodeName, len(alerts), len(anomalies), len(correlations))

		relatedAlertIDs := make([]string, 0, len(alerts))
		for _, alert := range alerts {
			relatedAlertIDs = append(relatedAlertIDs, alert.ID)
		}
		sort.Strings(relatedAlertIDs)

		report := IncidentReport{
			ID:                 fmt.Sprintf("incident-%s-%d", nodeName, time.Now().UnixNano()),
			NodeName:           nodeName,
			Classification:     classification,
			Severity:           severity,
			Status:             "active",
			WhatHappened:       what,
			ProbableCause:      probableCause,
			Confidence:         confidence,
			ImpactedComponents: impacted,
			SupportingSignals:  supportingSignals,
			CorrelatedMetrics:  topCorrelations(correlations, 6),
			RelatedAlertIDs:    relatedAlertIDs,
			SuggestedActions:   uniqueStrings(actions),
			PrimaryMetric:      primaryMetric,
			LogQuery:           buildLogQuery(nodeName, logIndicators),
			WindowStart:        windowStart,
			WindowEnd:          windowEnd,
			GeneratedAt:        time.Now().UTC(),
		}
		if rca, ok := latestRCAByNode[nodeName]; ok {
			report.RelatedRCAID = rca.ID
		}
		reports = append(reports, report)
	}

	sort.Slice(reports, func(i, j int) bool {
		left := severityOrder(reports[i].Severity)
		right := severityOrder(reports[j].Severity)
		if left != right {
			return left < right
		}
		return reports[i].GeneratedAt.After(reports[j].GeneratedAt)
	})
	if len(reports) > 200 {
		reports = reports[:200]
	}
	e.incidentReports = reports
}

func classifyIncident(metrics map[string]float64, trends map[string]string, anomalies []Anomaly, correlations []Correlation, logs []LogSummary) (IncidentClass, string, float64, string, []string) {
	cpuUsage := maxMetric(metrics,
		"probe_core_cpu_usage_percent",
		"node_cpu_usage_percent",
		"system.cpu.usage",
	)
	memUsed := metric(metrics, "node_memory_Used_bytes")
	memTotal := metric(metrics, "node_memory_MemTotal_bytes")
	memPercent := metric(metrics, "memory_used_percent")
	if memPercent <= 0.0 && memTotal > 0.0 {
		memPercent = (memUsed / memTotal) * 100.0
	}
	swapActivity := maxMetric(metrics, "node_vmstat_pswpout", "node_vmstat_pswpin")
	oomEvents := maxMetric(metrics, "node_vmstat_oom_kill")

	diskUtil := maxMetric(metrics, "disk_utilization_peak_percent", "system.disk.io.utilization")
	diskLatency := maxMetric(metrics, "disk_request_latency_p99_ms", "disk_avg_request_latency_ms", "probe_core_disk_await_ms")
	if latencySeconds := maxMetric(metrics, "node_disk_request_latency_p99_seconds", "node_disk_avg_request_latency_seconds"); latencySeconds > 0 {
		diskLatency = math.Max(diskLatency, latencySeconds*1000.0)
	}
	diskQueueDepth := maxMetric(metrics, "disk_queue_depth_total", "probe_core_disk_queue_depth")
	ioPressure := maxMetric(metrics, "io_pressure_full_avg10", "probe_core_pressure_io_full_avg10")

	netRx := maxMetric(metrics, "node_network_receive_bytes_per_second", "probe_core_network_rx_bytes_per_sec")
	netTx := maxMetric(metrics, "node_network_transmit_bytes_per_second", "probe_core_network_tx_bytes_per_sec")
	netDrops := maxMetric(metrics, "probe_core_network_softnet_dropped_total", "probe_core_network_rx_drops_total", "probe_core_network_tx_drops_total")
	retransRate := maxMetric(metrics, "probe_core_network_tcp_retransmissions_per_sec")

	load1 := maxMetric(metrics, "node_load1", "system.load.1m")
	blockedTasks := maxMetric(metrics, "node_procs_blocked", "probe_core_sched_blocked_tasks")
	contextSwitchDelta := maxMetric(metrics, "probe_core_perf_context_switches_delta")

	gpuUtil := maxMetric(metrics, "node_gpu_utilization_sm_avg_percent")
	gpuProcesses := maxMetric(metrics, "node_gpu_process_total")
	pcieRx := maxMetric(metrics, "node_gpu_pcie_rx_total_mb_s")
	pcieTx := maxMetric(metrics, "node_gpu_pcie_tx_total_mb_s")
	backpressureQueue := maxMetric(metrics, "probe_core_backpressure_queue_depth")
	cpuTrend := trendAny(trends,
		"probe_core_cpu_usage_percent",
		"node_cpu_usage_percent",
		"system.cpu.usage",
	)
	memTrend := trendAny(trends, "memory_used_percent", "node_memory_Used_bytes", "node_memory_used_bytes")
	iowaitTrend := trendAny(trends, "node_cpu_iowait_percent")
	diskTrend := trendAny(trends, "disk_request_latency_p99_ms", "disk_avg_request_latency_ms", "probe_core_disk_await_ms", "node_disk_request_latency_p99_seconds", "node_disk_avg_request_latency_seconds")
	retransTrend := trendAny(trends, "probe_core_network_tcp_retransmissions_per_sec", "node_tcp_retransmit_ratio", "node_tcp_retransmits_per_second")
	gpuTrend := trendAny(trends, "node_gpu_utilization_sm_avg_percent")

	cpuHigh := cpuUsage >= 85.0
	memoryPressure := memPercent >= 88.0 || swapActivity > 0 || oomEvents > 0
	ioHigh := diskUtil >= 75.0 || diskLatency >= 50.0 || diskQueueDepth >= 20.0 || ioPressure >= 10.0
	netThroughput := netRx + netTx
	communicationCongestion := netThroughput >= 50*1024*1024 || netDrops > 0 || retransRate >= 0.5 || retransTrend == "rising" || hasCorrelation(correlations, "network", "latency")
	storageBottleneck := ioHigh && (diskQueueDepth >= 30.0 || diskLatency >= 80.0 || iowaitTrend == "rising" || diskTrend == "rising")
	schedulingContention := (cpuHigh && load1 >= 8.0) || blockedTasks >= 2.0 || contextSwitchDelta >= 100000.0
	gpuLowUtil := gpuProcesses > 0 && gpuUtil > 0 && gpuUtil < 30.0
	gpuStarvation := gpuLowUtil && (cpuHigh || ioHigh || communicationCongestion || pcieRx > 8000 || pcieTx > 8000 || cpuTrend == "rising" || diskTrend == "rising" || gpuTrend == "falling")
	dataPipelineStall := gpuLowUtil && (backpressureQueue > 0 || ioPressure >= 15.0 || communicationCongestion || hasLogKeyword(logs, "timeout"))
	retryStorm := memPercent >= 75.0 && memTrend == "rising" && (communicationCongestion || cpuTrend == "rising") && (hasLogKeyword(logs, "timeout") || hasLogKeyword(logs, "error"))
	capacityExhaustion := memPercent >= 80.0 && memTrend == "rising" && (swapActivity > 0 || oomEvents > 0 || hasLogKeyword(logs, "oom"))

	hotResourceCount := 0
	if cpuHigh {
		hotResourceCount++
	}
	if memoryPressure {
		hotResourceCount++
	}
	if ioHigh {
		hotResourceCount++
	}
	if communicationCongestion {
		hotResourceCount++
	}
	resourceSaturation := hotResourceCount >= 2

	switch {
	case gpuStarvation:
		return IncidentClassGPUStarvation,
			"GPU workers are allocated but host-side feeder stages are not keeping them busy",
			0.81,
			"node_gpu_utilization_sm_avg_percent",
			[]string{
				"profile the input pipeline and host preprocessing stages",
				"inspect storage and network headroom before scaling GPU count",
				"verify placement so feeder threads are not CPU-starved on the same node",
			}
	case dataPipelineStall:
		return IncidentClassDataPipelineStall,
			"the workload is stalling before compute stages, so the bottleneck is likely in the data path rather than in the workers",
			0.76,
			"probe_core_backpressure_queue_depth",
			[]string{
				"inspect producer/consumer queue depth and batch cadence",
				"check upstream service latency and retry behavior",
				"verify storage and network throughput headroom",
			}
	case storageBottleneck:
		return IncidentClassStorageBottleneck,
			"CPU wait, queue depth, and storage latency indicate the workload is blocked on disk rather than raw compute",
			0.84,
			"disk_request_latency_p99_ms",
			[]string{
				"inspect the hottest device and partition before scaling CPU",
				"reduce random IO and batch writes",
				"move the hot path or checkpoint workload to faster storage",
			}
	case ioHigh:
		return IncidentClassIOBottleneck,
			"disk IO throughput or latency is constraining workload progress",
			0.74,
			"disk_utilization_peak_percent",
			[]string{
				"inspect top IO consumers and flush behavior",
				"enable cache/warmup for read-heavy paths",
				"rebalance workload across storage devices",
			}
	case communicationCongestion:
		return IncidentClassCommunicationCongest,
			"network congestion signals are active, so timeouts are more likely to be transport-bound than application-only",
			0.78,
			"node_network_receive_bytes_per_second",
			[]string{
				"inspect retransmissions, drops, and link errors first",
				"rebalance noisy services and enforce QoS",
				"validate east-west traffic topology and MTU consistency",
			}
	case retryStorm:
		return IncidentClassMemoryPressure,
			"memory growth combined with timeout/error bursts suggests leak growth or retry amplification",
			0.82,
			"memory_used_percent",
			[]string{
				"inspect top memory consumers and timeout-heavy logs together",
				"verify whether retries are amplifying memory growth",
				"tighten memory requests/limits after confirming the hot process",
			}
	case memoryPressure || capacityExhaustion:
		return IncidentClassMemoryPressure,
			"memory headroom is exhausted or trending toward reclaim pressure",
			0.79,
			"memory_used_percent",
			[]string{
				"identify leaking or over-allocating processes",
				"check reclaim, swap, and OOM evidence before scaling blindly",
				"reduce cache pressure and avoid swap-heavy paths",
			}
	case schedulingContention:
		return IncidentClassSchedulingContention,
			"run-queue pressure indicates scheduler contention under CPU load",
			0.69,
			"node_load1",
			[]string{
				"inspect run queue and blocked task hotspots",
				"reduce CPU throttling and noisy-neighbor contention",
				"rebalance CPU requests/affinity across nodes",
			}
	case resourceSaturation:
		return IncidentClassResourceSaturation,
			"multiple core resources are simultaneously saturated",
			0.83,
			"node_cpu_usage_percent",
			[]string{
				"apply immediate load shedding or rate limiting",
				"scale out constrained services",
				"review capacity planning assumptions and burst margins",
			}
	default:
		if class, cause, metric := classifyFromAnomalySignals(anomalies); class != IncidentClassUnknown {
			return class, cause, 0.58, metric, []string{
				"inspect anomaly timeline around incident window",
				"correlate anomaly spikes with deployment or traffic changes",
			}
		}
		return IncidentClassUnknown, "insufficient correlated evidence", 0.40, "", []string{
			"collect longer baseline and refine signal coverage",
			"review raw metrics/logs for missing instrumentation",
		}
	}
}

func trendAny(trends map[string]string, names ...string) string {
	for _, name := range names {
		if trend, ok := trends[name]; ok && trend != "" {
			return trend
		}
	}
	return ""
}

func classifyFromAnomalySignals(anomalies []Anomaly) (IncidentClass, string, string) {
	for _, anomaly := range anomalies {
		metric := strings.ToLower(anomaly.MetricName)
		switch {
		case strings.Contains(metric, "gpu"):
			return IncidentClassGPUStarvation, "GPU anomaly pattern detected", anomaly.MetricName
		case strings.Contains(metric, "network") || strings.Contains(metric, "retrans"):
			return IncidentClassCommunicationCongest, "network anomaly pattern detected", anomaly.MetricName
		case strings.Contains(metric, "disk") || strings.Contains(metric, "io"):
			return IncidentClassIOBottleneck, "storage anomaly pattern detected", anomaly.MetricName
		case strings.Contains(metric, "memory") || strings.Contains(metric, "swap") || strings.Contains(metric, "oom"):
			return IncidentClassMemoryPressure, "memory anomaly pattern detected", anomaly.MetricName
		case strings.Contains(metric, "cpu") || strings.Contains(metric, "sched") || strings.Contains(metric, "load"):
			return IncidentClassSchedulingContention, "scheduler/cpu anomaly pattern detected", anomaly.MetricName
		}
	}
	return IncidentClassUnknown, "", ""
}

func incidentClassTitle(class IncidentClass) string {
	switch class {
	case IncidentClassResourceSaturation:
		return "Resource saturation"
	case IncidentClassIOBottleneck:
		return "I/O bottleneck"
	case IncidentClassCommunicationCongest:
		return "Communication congestion"
	case IncidentClassMemoryPressure:
		return "Memory pressure"
	case IncidentClassSchedulingContention:
		return "Scheduling contention"
	case IncidentClassGPUStarvation:
		return "GPU starvation"
	case IncidentClassDataPipelineStall:
		return "Data pipeline stall"
	case IncidentClassStorageBottleneck:
		return "Storage bottleneck"
	default:
		return "Unclassified incident"
	}
}

func deriveIncidentSeverity(alerts []*Alert, anomalies []Anomaly) Severity {
	highest := SeverityInfo
	for _, alert := range alerts {
		if alert == nil {
			continue
		}
		if alert.Severity == SeverityCritical {
			return SeverityCritical
		}
		if alert.Severity == SeverityWarning {
			highest = SeverityWarning
		}
	}
	if highest == SeverityInfo && len(anomalies) > 0 {
		highest = SeverityWarning
	}
	return highest
}

func buildImpactedComponents(metrics map[string]float64, anomalies []Anomaly, processes []ProcessSummary, logs []LogSummary) []string {
	components := make([]string, 0, 8)
	if maxMetric(metrics, "node_cpu_usage_percent", "probe_core_cpu_usage_percent") >= 70 || hasMetricFragment(anomalies, "cpu") {
		components = append(components, "cpu")
	}
	if maxMetric(metrics, "memory_used_percent", "node_memory_Used_bytes", "node_vmstat_pswpout", "node_vmstat_oom_kill") > 0 || hasMetricFragment(anomalies, "memory") {
		components = append(components, "memory")
	}
	if maxMetric(metrics, "disk_utilization_peak_percent", "probe_core_disk_await_ms", "disk_request_latency_p99_ms") > 0 || hasMetricFragment(anomalies, "disk") {
		components = append(components, "storage")
	}
	if maxMetric(metrics, "node_network_receive_bytes_per_second", "probe_core_network_tcp_retransmissions_per_sec") > 0 || hasMetricFragment(anomalies, "network") {
		components = append(components, "network")
	}
	if maxMetric(metrics, "node_gpu_utilization_sm_avg_percent", "node_gpu_memory_used_percent") > 0 || hasMetricFragment(anomalies, "gpu") {
		components = append(components, "gpu")
	}
	if maxMetric(metrics, "node_load1", "probe_core_sched_blocked_tasks") > 0 || hasMetricFragment(anomalies, "sched") {
		components = append(components, "scheduler")
	}
	if len(logs) > 0 {
		components = append(components, "log-pipeline")
	}
	for _, process := range processes {
		if process.CPUPercent < 20 && process.IOReadBps < 20*1024*1024 && process.IOWriteBps < 20*1024*1024 {
			continue
		}
		components = append(components, "process:"+process.Name)
		if len(components) >= 10 {
			break
		}
	}
	return uniqueStrings(components)
}

func buildSupportingSignals(alerts []*Alert, anomalies []Anomaly, correlations []Correlation, logs []LogSummary, trends map[string]string) ([]SignalEvidence, []string) {
	signals := make([]SignalEvidence, 0, 16)

	for _, alert := range alerts {
		signals = append(signals, SignalEvidence{
			Source:   "alert",
			Signal:   alert.Title,
			Metric:   alert.MetricName,
			Value:    alert.Value,
			Expected: alert.Threshold,
			Trend:    trends[alert.MetricName],
			Details:  alert.Description,
		})
		if len(signals) >= 6 {
			break
		}
	}

	sort.Slice(anomalies, func(i, j int) bool {
		return anomalies[i].Score > anomalies[j].Score
	})
	for _, anomaly := range anomalies {
		signals = append(signals, SignalEvidence{
			Source:   "anomaly",
			Signal:   anomaly.Reason,
			Metric:   anomaly.MetricName,
			Value:    anomaly.CurrentVal,
			Expected: anomaly.ExpectedVal,
			Trend:    trends[anomaly.MetricName],
			Details:  fmt.Sprintf("score=%.2f direction=%s", anomaly.Score, anomaly.Direction),
		})
		if len(signals) >= 10 {
			break
		}
	}

	for _, correlation := range topCorrelations(correlations, 3) {
		signals = append(signals, SignalEvidence{
			Source:  "correlation",
			Signal:  fmt.Sprintf("%s ↔ %s", correlation.MetricA, correlation.MetricB),
			Metric:  correlation.MetricA,
			Value:   correlation.Coefficient,
			Details: fmt.Sprintf("direction=%s samples=%d", correlation.Direction, correlation.SampleCount),
		})
	}

	logIndicators := make([]string, 0, 4)
	for _, summary := range logs {
		if summary.Count == 0 {
			continue
		}
		lower := strings.ToLower(summary.Example)
		keyword := ""
		switch {
		case strings.Contains(lower, "timeout"):
			keyword = "timeout"
		case strings.Contains(lower, "oom"):
			keyword = "oom"
		case strings.Contains(lower, "throttle"):
			keyword = "throttle"
		case strings.Contains(lower, "refused"):
			keyword = "refused"
		case strings.Contains(lower, "stall"):
			keyword = "stall"
		case strings.Contains(lower, "error"):
			keyword = "error"
		}
		if keyword == "" {
			continue
		}
		logIndicators = append(logIndicators, keyword)
		signals = append(signals, SignalEvidence{
			Source:  "log",
			Signal:  summary.Fingerprint,
			Value:   float64(summary.Count),
			Details: summary.Example,
		})
		if len(signals) >= 14 {
			break
		}
	}

	return signals, uniqueStrings(logIndicators)
}

func incidentWindow(alerts []*Alert, anomalies []Anomaly) (time.Time, time.Time) {
	end := time.Now().UTC()
	start := end.Add(-30 * time.Minute)
	for _, alert := range alerts {
		if alert == nil || alert.CreatedAt.IsZero() {
			continue
		}
		if alert.CreatedAt.Before(start) {
			start = alert.CreatedAt
		}
	}
	for _, anomaly := range anomalies {
		if anomaly.DetectedAt.IsZero() {
			continue
		}
		if anomaly.DetectedAt.Before(start) {
			start = anomaly.DetectedAt
		}
	}
	return start.UTC(), end
}

func buildLogQuery(nodeName string, keywords []string) string {
	if len(keywords) == 0 {
		return fmt.Sprintf("/api/v1/logs/search?collector_id=%s&limit=50", nodeName)
	}
	return fmt.Sprintf("/api/v1/logs/search?collector_id=%s&q=%s&limit=50", nodeName, keywords[0])
}

func latestMetricSnapshot(data *NodeData) map[string]float64 {
	out := make(map[string]float64, len(data.Samples))
	for name, samples := range data.Samples {
		if len(samples) == 0 {
			continue
		}
		out[name] = samples[len(samples)-1].Value
	}
	return out
}

func latestTrendSnapshot(data *NodeData) map[string]string {
	out := make(map[string]string, len(data.Samples))
	for name, samples := range data.Samples {
		out[name] = trendFromSamples(samples)
	}
	return out
}

func metric(metrics map[string]float64, key string) float64 {
	value, ok := metrics[key]
	if !ok {
		return 0
	}
	return value
}

func maxMetric(metrics map[string]float64, keys ...string) float64 {
	maxValue := 0.0
	found := false
	for _, key := range keys {
		if value, ok := metrics[key]; ok {
			if !found || value > maxValue {
				maxValue = value
				found = true
			}
		}
	}
	if !found {
		return 0.0
	}
	return maxValue
}

func hasMetricFragment(anomalies []Anomaly, fragment string) bool {
	fragment = strings.ToLower(fragment)
	for _, anomaly := range anomalies {
		if strings.Contains(strings.ToLower(anomaly.MetricName), fragment) {
			return true
		}
	}
	return false
}

func topCorrelations(correlations []Correlation, limit int) []Correlation {
	if limit <= 0 {
		return nil
	}
	out := make([]Correlation, len(correlations))
	copy(out, correlations)
	sort.Slice(out, func(i, j int) bool {
		left := math.Abs(out[i].Coefficient)
		right := math.Abs(out[j].Coefficient)
		if left != right {
			return left > right
		}
		return out[i].DetectedAt.After(out[j].DetectedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func hasCorrelation(correlations []Correlation, keywordA, keywordB string) bool {
	for _, correlation := range correlations {
		left := strings.ToLower(correlation.MetricA)
		right := strings.ToLower(correlation.MetricB)
		if (strings.Contains(left, keywordA) && strings.Contains(right, keywordB)) ||
			(strings.Contains(left, keywordB) && strings.Contains(right, keywordA)) {
			if math.Abs(correlation.Coefficient) >= 0.55 {
				return true
			}
		}
	}
	return false
}

func hasLogKeyword(logs []LogSummary, keyword string) bool {
	keyword = strings.ToLower(keyword)
	for _, summary := range logs {
		if strings.Contains(strings.ToLower(summary.Example), keyword) {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 0.99 {
		return 0.99
	}
	return v
}

func correlationCandidates(samples map[string][]MetricSample) []string {
	candidates := make([]string, 0, len(samples))
	for name, series := range samples {
		if len(series) < 4 {
			continue
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "|pid=") || strings.Contains(lower, "|pod=") || strings.Contains(lower, "|proc=") {
			candidates = append(candidates, name)
			continue
		}
		if strings.Contains(lower, "cpu") ||
			strings.Contains(lower, "memory") ||
			strings.Contains(lower, "disk") ||
			strings.Contains(lower, "io") ||
			strings.Contains(lower, "network") ||
			strings.Contains(lower, "ebpf") ||
			strings.Contains(lower, "load") ||
			strings.Contains(lower, "gpu") ||
			strings.Contains(lower, "pressure") ||
			strings.Contains(lower, "syscall") ||
			strings.Contains(lower, "sched") ||
			strings.Contains(lower, "queue") ||
			strings.Contains(lower, "latency") {
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)
	if len(candidates) > 24 {
		candidates = candidates[:24]
	}
	return candidates
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
