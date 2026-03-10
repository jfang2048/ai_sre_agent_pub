package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// StructuredRCA is a structured, evidence-based root cause analysis output.
//
// Why this structure exists:
// The original RootCauseAnalysis produced a single root cause string with a flat
// confidence score. Real engineering diagnosis requires ranked hypotheses, each
// with independent evidence chains, contradictory signals, and impact scoping.
// This structure makes RCA output machine-consumable by the guarded action
// planning path and human-auditable (supporting vs contradictory evidence is explicit).
type StructuredRCA struct {
	ID        string    `json:"id"`
	NodeName  string    `json:"node_name"`
	CreatedAt time.Time `json:"created_at"`

	// Summary is a one-line human-readable description of the situation.
	Summary string `json:"summary"`

	// Hypotheses are ranked by confidence (highest first). Multiple hypotheses
	// are generated because real incidents often have ambiguous signals. Each
	// hypothesis carries its own evidence chain so the engineer can evaluate
	// which explanation best fits the observed behavior.
	Hypotheses []RCAHypothesis `json:"hypotheses"`

	// ImpactScope describes which subsystems are affected and how severely.
	ImpactScope RCAImpactScope `json:"impact_scope"`

	// Timeline records the ordered sequence of observed events.
	Timeline []RCATimelineEvent `json:"timeline,omitempty"`

	// RecommendedNextSteps are prioritized investigation or remediation steps.
	RecommendedNextSteps []string `json:"recommended_next_steps"`

	// AnalysisMethod records how this RCA was produced (rules, statistical, llm).
	AnalysisMethod string `json:"analysis_method"`

	// RelatedAlertIDs cross-references active alerts that contributed to this RCA.
	RelatedAlertIDs []string `json:"related_alert_ids,omitempty"`

	// SourceRCAID links back to the original flat RootCauseAnalysis if one exists.
	SourceRCAID string `json:"source_rca_id,omitempty"`
}

// RCAHypothesis is a single ranked root cause hypothesis.
//
// Each hypothesis is an independent explanation for the observed symptoms.
// The separation of supporting and contradictory evidence forces the analysis
// pipeline to explicitly acknowledge uncertainty — a hypothesis with high
// supporting evidence but also significant contradictory evidence should not
// be blindly trusted.
type RCAHypothesis struct {
	// Rank is 1-indexed (1 = most likely).
	Rank int `json:"rank"`

	// Title is a short description of the hypothesized root cause.
	Title string `json:"title"`

	// Mechanism explains the causal chain: what triggered what, and why.
	Mechanism string `json:"mechanism"`

	// Confidence is 0.0–1.0. Computed from evidence strength minus contradictions.
	Confidence float64 `json:"confidence"`

	// Category classifies the hypothesis (cpu, memory, io, network, gpu, scheduler, systemic).
	Category string `json:"category"`

	// SupportingEvidence lists signals that are consistent with this hypothesis.
	SupportingEvidence []RCAEvidence `json:"supporting_evidence"`

	// ContradictoryEvidence lists signals that weaken this hypothesis.
	ContradictoryEvidence []RCAEvidence `json:"contradictory_evidence,omitempty"`

	// Recommendations are specific to this hypothesis.
	Recommendations []string `json:"recommendations"`
}

// RCAEvidence is a single piece of evidence used in hypothesis evaluation.
type RCAEvidence struct {
	// Source is where this evidence came from (metric, alert, anomaly, correlation, log, process).
	Source string `json:"source"`

	// Signal is the specific metric or signal name.
	Signal string `json:"signal"`

	// Value is the observed value.
	Value float64 `json:"value"`

	// Baseline is the expected/normal value (0 if unknown).
	Baseline float64 `json:"baseline,omitempty"`

	// Deviation is how far the value is from baseline, as a ratio.
	Deviation float64 `json:"deviation,omitempty"`

	// Interpretation explains what this evidence means for the hypothesis.
	Interpretation string `json:"interpretation"`
}

// RCAImpactScope describes which subsystems are affected.
type RCAImpactScope struct {
	// AffectedDomains maps domain name to severity (critical, degraded, normal).
	AffectedDomains map[string]string `json:"affected_domains"`

	// PrimaryDomain is the most severely affected domain.
	PrimaryDomain string `json:"primary_domain"`

	// Blast radius estimation (node, pod, service, cluster).
	BlastRadius string `json:"blast_radius"`
}

// RCATimelineEvent is a single event in the incident timeline.
type RCATimelineEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	Signal      string    `json:"signal"`
	Description string    `json:"description"`
}

// BuildStructuredRCA constructs a structured RCA from the engine's current state.
// It generates multiple ranked hypotheses from the observed signal patterns,
// explicitly attaching supporting and contradictory evidence to each.
func BuildStructuredRCA(
	nodeName string,
	alerts []*Alert,
	anomalies []Anomaly,
	correlations []Correlation,
	metrics map[string]float64,
	trends map[string]string,
	processes []ProcessSummary,
	logs []LogSummary,
	sourceRCA *RootCauseAnalysis,
) *StructuredRCA {

	now := time.Now().UTC()
	rca := &StructuredRCA{
		ID:             fmt.Sprintf("srca-%s-%d", nodeName, now.UnixNano()),
		NodeName:       nodeName,
		CreatedAt:      now,
		AnalysisMethod: "rules",
	}
	if sourceRCA != nil {
		rca.SourceRCAID = sourceRCA.ID
		rca.AnalysisMethod = sourceRCA.AnalysisMethod
	}

	// Compute signal presence indicators
	signals := computeSignalPresence(metrics, anomalies)

	// Generate candidate hypotheses from signal patterns
	candidates := generateHypotheses(signals, metrics, anomalies, correlations, alerts, processes, logs)

	// Rank by confidence
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Confidence > candidates[j].Confidence
	})
	for i := range candidates {
		candidates[i].Rank = i + 1
	}

	// Keep top 5 hypotheses
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	rca.Hypotheses = candidates

	// Build impact scope
	rca.ImpactScope = buildImpactScope(signals, anomalies)

	// Build timeline from alerts and anomalies
	rca.Timeline = buildTimeline(alerts, anomalies)

	// Build summary
	rca.Summary = buildRCASummary(signals, candidates, nodeName)

	// Collect next steps from all hypotheses (deduplicated)
	nextSteps := make([]string, 0, 8)
	for _, h := range candidates {
		nextSteps = append(nextSteps, h.Recommendations...)
	}
	rca.RecommendedNextSteps = uniqueStrings(nextSteps)

	// Attach related alert IDs
	alertIDs := make([]string, 0, len(alerts))
	for _, a := range alerts {
		if a != nil && a.ResolvedAt == nil {
			alertIDs = append(alertIDs, a.ID)
		}
	}
	rca.RelatedAlertIDs = alertIDs

	return rca
}

// signalPresence tracks which resource domains show pressure.
type signalPresence struct {
	CPUHigh      bool
	MemHigh      bool
	IOHigh       bool
	NetHigh      bool
	GPUUtilHigh  bool
	GPUUtilLow   bool
	GPUMemHigh   bool
	GPUTempHigh  bool
	GPUThrottle  bool
	LoadHigh     bool
	SwapPressure bool
	OOMEvents    bool

	CPUValue     float64
	MemPercent   float64
	IOValue      float64
	LoadValue    float64
	GPUUtilValue float64
	GPUMemValue  float64
	GPUTempValue float64
	NetRxValue   float64
	NetTxValue   float64
}

func computeSignalPresence(metrics map[string]float64, anomalies []Anomaly) signalPresence {
	s := signalPresence{}

	cpuUsage := maxOf(metrics["system.cpu.usage"], metrics["node_cpu_usage_percent"])
	s.CPUValue = cpuUsage
	s.CPUHigh = cpuUsage > 80

	memUsed := metrics["node_memory_Used_bytes"]
	memTotal := metrics["node_memory_MemTotal_bytes"]
	if memTotal > 0 {
		s.MemPercent = (memUsed / memTotal) * 100.0
	}
	s.MemHigh = metrics["system.memory.usage"] > 85 || s.MemPercent > 85

	s.IOValue = maxOf(metrics["system.disk.io.utilization"], metrics["node_disk_io_now"])
	s.IOHigh = metrics["system.disk.io.utilization"] > 80 || metrics["node_disk_io_now"] > 50

	s.NetRxValue = metrics["node_network_receive_bytes_per_second"]
	s.NetTxValue = metrics["node_network_transmit_bytes_per_second"]
	s.NetHigh = metrics["system.network.tx.utilization"] > 80 ||
		metrics["system.network.rx.utilization"] > 80 ||
		s.NetRxValue > 200000000 || s.NetTxValue > 200000000

	s.GPUUtilValue = metrics["node_gpu_utilization_sm_avg_percent"]
	s.GPUUtilHigh = s.GPUUtilValue > 90
	s.GPUUtilLow = s.GPUUtilValue > 0 && s.GPUUtilValue < 30
	s.GPUMemValue = metrics["node_gpu_memory_used_percent"]
	s.GPUMemHigh = s.GPUMemValue > 90
	s.GPUTempValue = metrics["node_gpu_temperature_max_celsius"]
	s.GPUTempHigh = s.GPUTempValue > 85
	s.GPUThrottle = metrics["node_gpu_throttle_active_any"] > 0 ||
		metrics["node_gpu_throttle_thermal_any"] > 0 ||
		metrics["node_gpu_throttle_power_any"] > 0

	s.LoadValue = maxOf(metrics["system.load.1m"], metrics["node_load1"])
	s.LoadHigh = s.LoadValue > 4.0

	s.SwapPressure = metrics["node_vmstat_pswpout"] > 0 || metrics["node_vmstat_pswpin"] > 0
	s.OOMEvents = metrics["node_vmstat_oom_kill"] > 0

	return s
}

func generateHypotheses(
	s signalPresence,
	metrics map[string]float64,
	anomalies []Anomaly,
	correlations []Correlation,
	alerts []*Alert,
	processes []ProcessSummary,
	logs []LogSummary,
) []RCAHypothesis {

	hypotheses := make([]RCAHypothesis, 0, 8)

	// CPU saturation hypothesis
	if s.CPUHigh {
		supporting := []RCAEvidence{
			{Source: "metric", Signal: "cpu_usage", Value: s.CPUValue, Baseline: 50, Deviation: (s.CPUValue - 50) / 50, Interpretation: "CPU utilization significantly above normal baseline"},
		}
		if s.LoadHigh {
			supporting = append(supporting, RCAEvidence{Source: "metric", Signal: "load_average", Value: s.LoadValue, Baseline: 2, Deviation: (s.LoadValue - 2) / 2, Interpretation: "Load average confirms processing backlog"})
		}
		for _, p := range processes {
			if p.CPUPercent > 50 {
				supporting = append(supporting, RCAEvidence{Source: "process", Signal: p.Name, Value: p.CPUPercent, Interpretation: fmt.Sprintf("Process %s consuming %.1f%% CPU", p.Name, p.CPUPercent)})
			}
		}
		contradictory := make([]RCAEvidence, 0)
		if s.IOHigh {
			contradictory = append(contradictory, RCAEvidence{Source: "metric", Signal: "io_utilization", Value: s.IOValue, Interpretation: "IO pressure suggests the bottleneck may be IO-bound, not CPU-bound"})
		}

		confidence := 0.70
		if s.LoadHigh {
			confidence += 0.08
		}
		if s.IOHigh {
			confidence -= 0.05
		}
		if s.NetHigh {
			confidence -= 0.03
		}
		confidence = clampConfidence(confidence)

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:                 "CPU saturation from compute-bound workload",
			Mechanism:             "CPU utilization and run-queue depth indicate compute contention. Processes are competing for CPU time, causing scheduling delays and increased latency.",
			Confidence:            confidence,
			Category:              "cpu",
			SupportingEvidence:    supporting,
			ContradictoryEvidence: contradictory,
			Recommendations: []string{
				"Identify and investigate top CPU-consuming processes",
				"Consider scaling horizontally if workload is legitimate",
				"Review application code for inefficient CPU-bound loops",
			},
		})
	}

	// Memory pressure hypothesis
	if s.MemHigh {
		supporting := []RCAEvidence{
			{Source: "metric", Signal: "memory_usage_percent", Value: s.MemPercent, Baseline: 60, Deviation: (s.MemPercent - 60) / 60, Interpretation: "Memory utilization above safe threshold"},
		}
		if s.SwapPressure {
			supporting = append(supporting, RCAEvidence{Source: "metric", Signal: "swap_activity", Value: metrics["node_vmstat_pswpout"], Interpretation: "Active swap-out indicates memory exhaustion"})
		}
		if s.OOMEvents {
			supporting = append(supporting, RCAEvidence{Source: "metric", Signal: "oom_kill", Value: metrics["node_vmstat_oom_kill"], Interpretation: "OOM killer invocations confirm memory exhaustion"})
		}

		contradictory := make([]RCAEvidence, 0)
		if !s.CPUHigh && !s.IOHigh {
			// Memory could be high but stable (not actively causing issues)
		}

		confidence := 0.68
		if s.SwapPressure {
			confidence += 0.10
		}
		if s.OOMEvents {
			confidence += 0.12
		}
		confidence = clampConfidence(confidence)

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:                 "Memory pressure causing reclaim/swap overhead",
			Mechanism:             "Memory utilization has crossed safety thresholds, forcing the kernel into expensive reclaim paths. Swap activity and potential OOM kills indicate the working set exceeds physical memory.",
			Confidence:            confidence,
			Category:              "memory",
			SupportingEvidence:    supporting,
			ContradictoryEvidence: contradictory,
			Recommendations: []string{
				"Identify processes with highest RSS and check for memory leaks",
				"Review memory requests/limits for containerized workloads",
				"Consider increasing available memory or reducing cache pressure",
			},
		})
	}

	// IO bottleneck hypothesis
	if s.IOHigh {
		supporting := []RCAEvidence{
			{Source: "metric", Signal: "io_utilization", Value: s.IOValue, Baseline: 30, Deviation: (s.IOValue - 30) / 30, Interpretation: "Disk I/O utilization indicates storage subsystem saturation"},
		}
		contradictory := make([]RCAEvidence, 0)
		if s.MemHigh && s.SwapPressure {
			contradictory = append(contradictory, RCAEvidence{Source: "metric", Signal: "memory_pressure", Value: s.MemPercent, Interpretation: "IO pressure may be a secondary effect of memory reclaim/swap, not the root cause"})
		}

		confidence := 0.72
		if s.MemHigh && s.SwapPressure {
			confidence -= 0.08
		}
		confidence = clampConfidence(confidence)

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:                 "Storage I/O subsystem saturated",
			Mechanism:             "Disk utilization is at capacity, increasing latency for all IO operations. This can cascade into application-level timeouts and queue buildup.",
			Confidence:            confidence,
			Category:              "io",
			SupportingEvidence:    supporting,
			ContradictoryEvidence: contradictory,
			Recommendations: []string{
				"Identify top IO consumers with iotop/blktrace",
				"Move hot data to faster storage tier (SSD/NVMe)",
				"Implement read caching to reduce disk reads",
				"Review database query patterns for unnecessary IO",
			},
		})
	}

	// Network congestion hypothesis
	if s.NetHigh {
		supporting := []RCAEvidence{
			{Source: "metric", Signal: "network_rx_bps", Value: s.NetRxValue, Interpretation: "High inbound network throughput"},
			{Source: "metric", Signal: "network_tx_bps", Value: s.NetTxValue, Interpretation: "High outbound network throughput"},
		}
		contradictory := make([]RCAEvidence, 0)

		confidence := 0.65
		if !s.CPUHigh && !s.MemHigh {
			confidence += 0.08 // Network is likely the primary issue
		}
		confidence = clampConfidence(confidence)

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:                 "Network bandwidth saturation",
			Mechanism:             "Network interface throughput is approaching link capacity. This causes packet queueing, increased latency, and potential packet drops.",
			Confidence:            confidence,
			Category:              "network",
			SupportingEvidence:    supporting,
			ContradictoryEvidence: contradictory,
			Recommendations: []string{
				"Identify top network consumers",
				"Check for retransmissions and packet drops",
				"Consider bandwidth upgrade or traffic shaping/QoS",
				"Investigate for potential DDoS or traffic anomalies",
			},
		})
	}

	// GPU thermal throttling hypothesis
	if s.GPUThrottle && s.GPUTempHigh {
		supporting := []RCAEvidence{
			{Source: "metric", Signal: "gpu_temperature", Value: s.GPUTempValue, Baseline: 70, Deviation: (s.GPUTempValue - 70) / 70, Interpretation: "GPU temperature exceeds safe operating threshold"},
			{Source: "metric", Signal: "gpu_throttle_active", Value: 1, Interpretation: "GPU clock throttling is actively engaged"},
		}

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:                 "GPU thermal throttling reducing compute throughput",
			Mechanism:             "GPU temperature has exceeded thermal limits, causing the GPU to reduce clock speeds to prevent hardware damage. This directly reduces compute throughput.",
			Confidence:            0.85,
			Category:              "gpu",
			SupportingEvidence:    supporting,
			ContradictoryEvidence: nil,
			Recommendations: []string{
				"Verify GPU cooling and airflow (fans, heatsinks, chassis)",
				"Reduce batch size or clock limits to lower heat output",
				"Check for dust buildup or failed cooling components",
			},
		})
	}

	// GPU memory fragmentation hypothesis
	if s.GPUMemHigh && s.GPUUtilLow {
		gpuProcessCount := metrics["node_gpu_process_total"]
		supporting := []RCAEvidence{
			{Source: "metric", Signal: "gpu_memory_used_percent", Value: s.GPUMemValue, Interpretation: "GPU memory nearly exhausted"},
			{Source: "metric", Signal: "gpu_utilization", Value: s.GPUUtilValue, Interpretation: "GPU compute is underutilized despite high memory usage"},
		}
		if gpuProcessCount > 1 {
			supporting = append(supporting, RCAEvidence{Source: "metric", Signal: "gpu_process_count", Value: gpuProcessCount, Interpretation: "Multiple processes contending for GPU memory"})
		}

		confidence := 0.70
		if gpuProcessCount > 1 {
			confidence += 0.06
		}
		confidence = clampConfidence(confidence)

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:                 "GPU memory fragmentation or over-allocation",
			Mechanism:             "GPU memory is nearly exhausted while compute utilization remains low, indicating memory fragmentation or over-allocation by multiple processes.",
			Confidence:            confidence,
			Category:              "gpu",
			SupportingEvidence:    supporting,
			ContradictoryEvidence: nil,
			Recommendations: []string{
				"Consolidate GPU workloads to reduce context fragmentation",
				"Restart long-lived processes to defragment VRAM",
				"Right-size model/batch memory allocations",
			},
		})
	}

	// CPU-GPU pipeline imbalance hypothesis
	if s.GPUUtilLow && s.CPUHigh {
		supporting := []RCAEvidence{
			{Source: "metric", Signal: "gpu_utilization", Value: s.GPUUtilValue, Interpretation: "GPU is underutilized — waiting for data from host"},
			{Source: "metric", Signal: "cpu_usage", Value: s.CPUValue, Interpretation: "CPU is saturated — preprocessing bottleneck"},
		}

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:                 "CPU-side preprocessing bottleneck starving GPU",
			Mechanism:             "GPU compute is underutilized because the CPU-side data pipeline cannot feed data fast enough. The CPU is saturated with preprocessing/data loading work.",
			Confidence:            0.72,
			Category:              "gpu",
			SupportingEvidence:    supporting,
			ContradictoryEvidence: nil,
			Recommendations: []string{
				"Increase data loader parallelism (num_workers)",
				"Move preprocessing to GPU where possible",
				"Profile CPU hotspots in the input pipeline",
			},
		})
	}

	// Multi-resource saturation hypothesis
	hotCount := 0
	hotDomains := make([]string, 0, 4)
	if s.CPUHigh {
		hotCount++
		hotDomains = append(hotDomains, "CPU")
	}
	if s.MemHigh {
		hotCount++
		hotDomains = append(hotDomains, "memory")
	}
	if s.IOHigh {
		hotCount++
		hotDomains = append(hotDomains, "IO")
	}
	if s.NetHigh {
		hotCount++
		hotDomains = append(hotDomains, "network")
	}
	if hotCount >= 2 {
		supporting := make([]RCAEvidence, 0, hotCount)
		if s.CPUHigh {
			supporting = append(supporting, RCAEvidence{Source: "metric", Signal: "cpu_usage", Value: s.CPUValue, Interpretation: "CPU under pressure"})
		}
		if s.MemHigh {
			supporting = append(supporting, RCAEvidence{Source: "metric", Signal: "memory_percent", Value: s.MemPercent, Interpretation: "Memory under pressure"})
		}
		if s.IOHigh {
			supporting = append(supporting, RCAEvidence{Source: "metric", Signal: "io_utilization", Value: s.IOValue, Interpretation: "IO under pressure"})
		}
		if s.NetHigh {
			supporting = append(supporting, RCAEvidence{Source: "metric", Signal: "network_throughput", Value: s.NetRxValue + s.NetTxValue, Interpretation: "Network under pressure"})
		}

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:                 fmt.Sprintf("Multi-resource saturation (%s)", strings.Join(hotDomains, " + ")),
			Mechanism:             "Multiple resource domains are simultaneously saturated, indicating overall system capacity has been exceeded. This is likely a cascading failure or capacity planning issue.",
			Confidence:            0.80,
			Category:              "systemic",
			SupportingEvidence:    supporting,
			ContradictoryEvidence: nil,
			Recommendations: []string{
				"Apply immediate load shedding or rate limiting",
				"Scale resources vertically or horizontally",
				"Review recent deployments for workload changes",
				"Update capacity planning assumptions",
			},
		})
	}

	// Anomaly-based hypothesis (catch-all for detected anomalies)
	if len(anomalies) > 3 && len(hypotheses) == 0 {
		supporting := make([]RCAEvidence, 0, len(anomalies))
		for _, a := range anomalies {
			if len(supporting) >= 6 {
				break
			}
			supporting = append(supporting, RCAEvidence{
				Source:         "anomaly",
				Signal:         a.MetricName,
				Value:          a.CurrentVal,
				Baseline:       a.ExpectedVal,
				Deviation:      a.Score,
				Interpretation: a.Reason,
			})
		}

		hypotheses = append(hypotheses, RCAHypothesis{
			Title:              fmt.Sprintf("Systemic instability (%d anomalous metrics)", len(anomalies)),
			Mechanism:          "Multiple metrics are behaving anomalously without a clear single root cause. This may indicate an external factor such as a traffic change, deployment, or upstream dependency issue.",
			Confidence:         0.55,
			Category:           "systemic",
			SupportingEvidence: supporting,
			Recommendations: []string{
				"Review recent changes or deployments",
				"Check for external factors (traffic spike, upstream failures)",
				"Correlate anomaly onset time with change events",
			},
		})
	}

	// Add correlation-based evidence to relevant hypotheses
	for i := range hypotheses {
		for _, corr := range correlations {
			if math.Abs(corr.Coefficient) < 0.7 {
				continue
			}
			metricA := strings.ToLower(corr.MetricA)
			metricB := strings.ToLower(corr.MetricB)
			cat := strings.ToLower(hypotheses[i].Category)
			if strings.Contains(metricA, cat) || strings.Contains(metricB, cat) {
				hypotheses[i].SupportingEvidence = append(hypotheses[i].SupportingEvidence, RCAEvidence{
					Source:         "correlation",
					Signal:         fmt.Sprintf("%s ↔ %s", corr.MetricA, corr.MetricB),
					Value:          corr.Coefficient,
					Interpretation: fmt.Sprintf("Strong %s correlation (r=%.2f) between %s and %s", corr.Direction, corr.Coefficient, corr.MetricA, corr.MetricB),
				})
			}
		}
	}

	return hypotheses
}

func buildImpactScope(s signalPresence, anomalies []Anomaly) RCAImpactScope {
	domains := make(map[string]string)

	if s.CPUHigh {
		if s.CPUValue > 90 {
			domains["cpu"] = "critical"
		} else {
			domains["cpu"] = "degraded"
		}
	} else {
		domains["cpu"] = "normal"
	}

	if s.MemHigh {
		if s.OOMEvents || s.MemPercent > 95 {
			domains["memory"] = "critical"
		} else {
			domains["memory"] = "degraded"
		}
	} else {
		domains["memory"] = "normal"
	}

	if s.IOHigh {
		if s.IOValue > 90 {
			domains["storage"] = "critical"
		} else {
			domains["storage"] = "degraded"
		}
	} else {
		domains["storage"] = "normal"
	}

	if s.NetHigh {
		domains["network"] = "degraded"
	} else {
		domains["network"] = "normal"
	}

	if s.GPUThrottle || s.GPUUtilHigh || s.GPUMemHigh {
		domains["gpu"] = "degraded"
		if s.GPUThrottle && s.GPUTempHigh {
			domains["gpu"] = "critical"
		}
	} else if s.GPUUtilValue > 0 {
		domains["gpu"] = "normal"
	}

	if s.LoadHigh {
		if s.LoadValue > 16 {
			domains["scheduler"] = "critical"
		} else {
			domains["scheduler"] = "degraded"
		}
	} else {
		domains["scheduler"] = "normal"
	}

	// Find primary domain
	primary := ""
	for domain, severity := range domains {
		if severity == "critical" {
			primary = domain
			break
		}
	}
	if primary == "" {
		for domain, severity := range domains {
			if severity == "degraded" {
				primary = domain
				break
			}
		}
	}

	// Estimate blast radius
	blastRadius := "node"
	criticalCount := 0
	for _, sev := range domains {
		if sev == "critical" {
			criticalCount++
		}
	}
	if criticalCount >= 3 {
		blastRadius = "cluster"
	} else if criticalCount >= 2 {
		blastRadius = "service"
	}

	return RCAImpactScope{
		AffectedDomains: domains,
		PrimaryDomain:   primary,
		BlastRadius:     blastRadius,
	}
}

func buildTimeline(alerts []*Alert, anomalies []Anomaly) []RCATimelineEvent {
	events := make([]RCATimelineEvent, 0, len(alerts)+len(anomalies))

	for _, a := range alerts {
		if a == nil || a.ResolvedAt != nil {
			continue
		}
		events = append(events, RCATimelineEvent{
			Timestamp:   a.CreatedAt,
			Signal:      a.MetricName,
			Description: a.Description,
		})
	}

	for _, a := range anomalies {
		events = append(events, RCATimelineEvent{
			Timestamp:   a.DetectedAt,
			Signal:      a.MetricName,
			Description: fmt.Sprintf("Anomaly detected: %s (score=%.2f, direction=%s)", a.MetricName, a.Score, a.Direction),
		})
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	if len(events) > 20 {
		events = events[:20]
	}

	return events
}

func buildRCASummary(s signalPresence, hypotheses []RCAHypothesis, nodeName string) string {
	if len(hypotheses) == 0 {
		return fmt.Sprintf("No significant issues detected on %s", nodeName)
	}
	top := hypotheses[0]
	return fmt.Sprintf("%s on %s (confidence: %.0f%%, %d hypotheses evaluated)",
		top.Title, nodeName, top.Confidence*100, len(hypotheses))
}

func maxOf(values ...float64) float64 {
	m := 0.0
	for _, v := range values {
		if v > m {
			m = v
		}
	}
	return m
}

func clampConfidence(v float64) float64 {
	if v < 0.1 {
		return 0.1
	}
	if v > 0.98 {
		return 0.98
	}
	return v
}
