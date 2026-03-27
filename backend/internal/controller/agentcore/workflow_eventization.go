package agent

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/predictive"
)

type riskSeriesSpec struct {
	key      string
	display  string
	unit     string
	category string
	extract  func(map[string]float64) (float64, bool)
}

type riskSignalProfile struct {
	medium   float64
	high     float64
	weight   float64
	scope    string
	category string
}

func riskSeriesSpecs() []riskSeriesSpec {
	return []riskSeriesSpec{
		{key: "cpu_pressure", display: "CPU usage", unit: "percent", category: "runtime", extract: extractMetric("node_cpu_usage_percent")},
		{key: "memory_pressure", display: "Memory usage", unit: "percent", category: "runtime", extract: extractMemoryPercent},
		{key: "io_latency", display: "IO latency p99", unit: "milliseconds", category: "hardware", extract: extractIOLatencyMS},
		{key: "service_latency", display: "Service latency p95", unit: "milliseconds", category: "service", extract: extractMetricAny("service_latency_p95_ms", "service_latency_p99_ms", "service_request_latency_p95_ms", "service_request_latency_p99_ms")},
		{key: "io_pressure", display: "IO pressure full avg10", unit: "percent", category: "runtime", extract: extractMetric("node_pressure_io_full_avg10")},
		{key: "network_throughput", display: "Network throughput", unit: "bytes_per_second", category: "hardware", extract: extractNetworkThroughputBPS},
		{key: "retransmit_ratio", display: "TCP retransmit ratio", unit: "ratio", category: "hardware", extract: extractMetric("node_tcp_retransmit_ratio")},
		{key: "softnet_drop", display: "Softnet drops", unit: "count_per_second", category: "hardware", extract: extractMetric("node_softnet_dropped_per_second")},
		{key: "log_burst", display: "Error/warn burst", unit: "count", category: "service", extract: extractMetricAny("service_log_burst_count", "service_log_error_warn_count", "service_log_error_count")},
		{key: "gpu_utilization", display: "GPU utilization", unit: "percent", category: "runtime", extract: extractMetricAny("node_gpu_utilization_sm_avg_percent", "node_gpu_utilization_sm_percent", "node_gpu_utilization_percent")},
		{key: "gpu_temperature", display: "GPU temperature", unit: "celsius", category: "hardware", extract: extractMetricAny("node_gpu_temperature_peak_celsius", "node_gpu_temperature_max_celsius", "node_gpu_temperature_celsius")},
		{key: "gpu_memory_pressure", display: "GPU memory pressure", unit: "percent", category: "hardware", extract: extractMetricAny("node_gpu_memory_used_percent", "probe_core_gpu_memory_pressure_percent")},
		{key: "cpu_throttle_ratio", display: "CPU throttled ratio", unit: "ratio", category: "hardware", extract: extractMetricAny("probe_core_cgroup_cpu_throttled_ratio", "node_cpu_throttled_ratio")},
	}
}

func riskSignalProfiles() map[string]riskSignalProfile {
	return map[string]riskSignalProfile{
		"cpu_pressure":        {medium: 65, high: 88, weight: 0.16, scope: "node", category: "runtime"},
		"memory_pressure":     {medium: 72, high: 90, weight: 0.14, scope: "node", category: "runtime"},
		"memory_leak_rate":    {medium: 0.04, high: 0.16, weight: 0.12, scope: "process", category: "runtime"},
		"io_latency":          {medium: 20, high: 80, weight: 0.18, scope: "node", category: "hardware"},
		"service_latency":     {medium: 180, high: 400, weight: 0.16, scope: "service", category: "service"},
		"io_pressure":         {medium: 5, high: 20, weight: 0.12, scope: "node", category: "runtime"},
		"network_throughput":  {medium: 60 * 1024 * 1024, high: 150 * 1024 * 1024, weight: 0.10, scope: "node", category: "hardware"},
		"retransmit_ratio":    {medium: 0.005, high: 0.02, weight: 0.14, scope: "node", category: "hardware"},
		"softnet_drop":        {medium: 1, high: 20, weight: 0.08, scope: "node", category: "hardware"},
		"log_burst":           {medium: 8, high: 40, weight: 0.10, scope: "service", category: "service"},
		"gpu_utilization":     {medium: 75, high: 92, weight: 0.10, scope: "gpu", category: "runtime"},
		"gpu_temperature":     {medium: 80, high: 88, weight: 0.10, scope: "gpu", category: "hardware"},
		"gpu_memory_pressure": {medium: 82, high: 94, weight: 0.11, scope: "gpu", category: "hardware"},
		"cpu_throttle_ratio":  {medium: 0.05, high: 0.20, weight: 0.10, scope: "node", category: "hardware"},
	}
}

func extractMetricAny(names ...string) func(map[string]float64) (float64, bool) {
	return func(metrics map[string]float64) (float64, bool) {
		if metrics == nil {
			return 0, false
		}
		for _, name := range names {
			if value, ok := metrics[name]; ok {
				return value, true
			}
		}
		return 0, false
	}
}

func averageSlopePerMinute(points []RiskSeriesPoint) float64 {
	if len(points) < 2 {
		return 0
	}
	start := maxInt(1, len(points)-4)
	total := 0.0
	steps := 0
	for i := start; i < len(points); i++ {
		deltaMinutes := points[i].Timestamp.Sub(points[i-1].Timestamp).Minutes()
		if deltaMinutes <= 0 {
			continue
		}
		total += (points[i].Value - points[i-1].Value) / deltaMinutes
		steps++
	}
	if steps == 0 {
		return 0
	}
	return total / float64(steps)
}

func thresholdBreaches(points []RiskSeriesPoint, threshold float64) int {
	if threshold <= 0 {
		return 0
	}
	count := 0
	for _, point := range points {
		if point.Value >= threshold {
			count++
		}
	}
	return count
}

func trailingPersistence(points []RiskSeriesPoint, threshold float64, baseline float64) int {
	count := 0
	for i := len(points) - 1; i >= 0; i-- {
		value := points[i].Value
		if threshold > 0 {
			if value < threshold {
				break
			}
		} else if value <= baseline {
			break
		}
		count++
	}
	return count
}

func classifySeriesTrend(latest, baseline, slopePerMinute, accel float64, breaches, persistence int, profile riskSignalProfile) (string, bool) {
	delta := percentChange(baseline, latest)
	switch {
	case latest >= profile.high || (breaches >= 2 && persistence >= 2) || (delta >= 25 && slopePerMinute > 0):
		if accel > 0 && persistence >= 2 {
			return "worsening", true
		}
		return "rising", true
	case slopePerMinute > 0 && delta >= 12:
		return "rising", persistence >= 2 || breaches > 0
	case slopePerMinute < 0 && latest < baseline*0.95:
		return "recovering", false
	case math.Abs(accel) > maxFloat(math.Abs(baseline)*0.08, 1.0):
		return "volatile", breaches > 0
	default:
		return "stable", breaches > 0 && persistence >= 2
	}
}

func trendSeverity(series RiskSeries, profile riskSignalProfile) string {
	switch {
	case series.Latest >= profile.high || series.ThresholdBreaches >= 3:
		return "high"
	case series.Triggered || series.DeltaPercent >= 20 || series.PersistencePoints >= 2:
		return "medium"
	default:
		return "low"
	}
}

func trendConfidence(series RiskSeries, profile riskSignalProfile) float64 {
	score := 0.28
	score += clamp01(math.Abs(series.DeltaPercent)/80.0) * 0.28
	score += clamp01(math.Abs(series.SlopePerMinute)/maxFloat(profile.medium*0.05, 0.5)) * 0.18
	score += clamp01(float64(series.PersistencePoints)/4.0) * 0.16
	score += clamp01(float64(series.ThresholdBreaches)/4.0) * 0.10
	if series.Triggered {
		score += 0.08
	}
	return clamp01(score)
}

func trendSummary(series RiskSeries, severity string) (string, string) {
	summary := fmt.Sprintf("%s is %s: latest %.3f vs baseline %.3f (delta %.1f%%, slope %.3f/minute)",
		series.Display,
		series.Trend,
		series.Latest,
		series.Baseline,
		series.DeltaPercent,
		series.SlopePerMinute,
	)
	low := strings.ToLower(series.Key)
	switch {
	case strings.Contains(low, "memory"):
		return summary, "Validate top RSS processes and reclaim pressure before memory exhaustion becomes an outage."
	case strings.Contains(low, "io") || strings.Contains(low, "disk"):
		return summary, "Compare disk latency with queue depth and identify the process or device driving the IO backlog."
	case strings.Contains(low, "retransmit") || strings.Contains(low, "softnet"):
		return summary, "Check packet loss, softnet drops, and recent connection churn before treating latency as app-only."
	case strings.Contains(low, "gpu"):
		return summary, "Confirm whether device pressure is thermal, memory, or feeder-related before changing workload placement."
	case strings.Contains(low, "cpu"):
		if severity == "high" {
			return summary, "Check run queue depth, throttling, and recent rollout CPU requests before scaling blindly."
		}
		return summary, "Compare CPU growth with blocked tasks and the hottest processes to separate real contention from transient spikes."
	default:
		return summary, "Verify the trend against top offenders and recent changes before escalating."
	}
}

func predictiveSeriesKey(metric string) string {
	switch metric {
	case "node_cpu_usage_percent":
		return "cpu_pressure"
	case "node_memory_Used_bytes":
		return "memory_pressure"
	case "service_latency_p95_ms", "service_latency_p99_ms", "service_request_latency_p95_ms", "service_request_latency_p99_ms":
		return "service_latency"
	case "node_pressure_io_full_avg10":
		return "io_pressure"
	case "node_network_receive_bytes_per_second", "node_network_transmit_bytes_per_second", "node_network_total_receive_bytes_per_second", "node_network_total_transmit_bytes_per_second":
		return "network_throughput"
	case "node_tcp_retransmit_ratio":
		return "retransmit_ratio"
	case "node_gpu_temperature_celsius", "node_gpu_temperature_max_celsius", "node_gpu_temperature_peak_celsius":
		return "gpu_temperature"
	case "node_gpu_utilization_sm_avg_percent":
		return "gpu_utilization"
	default:
		return ""
	}
}

func trendDisplayForPredictiveMetric(metric string) string {
	switch predictiveSeriesKey(metric) {
	case "cpu_pressure":
		return "CPU usage"
	case "memory_pressure":
		return "Memory usage"
	case "service_latency":
		return "Service latency p95"
	case "io_pressure":
		return "IO pressure full avg10"
	case "network_throughput":
		return "Network throughput"
	case "retransmit_ratio":
		return "TCP retransmit ratio"
	case "gpu_temperature":
		return "GPU temperature"
	case "gpu_memory_pressure":
		return "GPU memory pressure"
	case "gpu_utilization":
		return "GPU utilization"
	default:
		return metric
	}
}

func seriesCategory(key string) string {
	if profile, ok := riskSignalProfiles()[key]; ok {
		return profile.category
	}
	return "runtime"
}

func buildTrendAssessments(collectorID string, series []RiskSeries, current map[string]float64, history []ingest.MetricHistorySample, window time.Duration, behavioral map[string]BehavioralSignalAssessment) []TrendAssessment {
	collector := firstNonEmpty(collectorID, "fleet")
	profiles := riskSignalProfiles()
	assessments := make([]TrendAssessment, 0, len(series)+4)
	indexByKey := make(map[string]int, len(series))

	for _, item := range series {
		profile, ok := profiles[item.Key]
		if !ok {
			continue
		}
		severity := trendSeverity(item, profile)
		if !item.Triggered && severity == "low" && math.Abs(item.DeltaPercent) < 10 {
			continue
		}
		summary, hint := trendSummary(item, severity)
		assessment := TrendAssessment{
			ID:                fmt.Sprintf("trend-%s", sanitizeID(item.Key)),
			SeriesKey:         item.Key,
			Display:           item.Display,
			Category:          firstNonEmpty(item.Category, profile.category),
			Scope:             profile.scope,
			Entity:            collector,
			Trend:             item.Trend,
			Severity:          severity,
			Confidence:        trendConfidence(item, profile),
			DetectionMode:     "series_slope",
			Latest:            item.Latest,
			Baseline:          item.Baseline,
			DeltaPercent:      item.DeltaPercent,
			SlopePerMinute:    item.SlopePerMinute,
			Acceleration:      item.Acceleration,
			ThresholdBreaches: item.ThresholdBreaches,
			PersistencePoints: item.PersistencePoints,
			ThresholdValue:    item.ThresholdValue,
			Triggered:         item.Triggered,
			Summary:           summary,
			OperatorHint:      hint,
			LastObservedAt:    item.Points[len(item.Points)-1].Timestamp,
		}
		if memory, ok := behavioral[item.Key]; ok {
			assessment.BehavioralClassification = memory.Classification
			assessment.SuppressionFactor = memory.SuppressionFactor
			assessment.BehavioralReason = memory.Explanation
			switch memory.Classification {
			case "expected_recurring_burst":
				assessment.Confidence = clamp01(assessment.Confidence * (1 - memory.SuppressionFactor))
				if assessment.Confidence < 0.45 {
					assessment.Triggered = false
				}
				assessment.Summary = truncateString(fmt.Sprintf("%s Historical memory classified this as an expected recurring burst: %s", assessment.Summary, memory.Explanation), 260)
				assessment.OperatorHint = memory.Explanation
				assessment.Severity = "low"
			case "correlated_anomaly":
				assessment.Confidence = clamp01(maxFloat(assessment.Confidence, 0.52+memory.Confidence*0.20))
				assessment.Triggered = true
				assessment.Summary = truncateString(fmt.Sprintf("%s Historical memory found recurring behavior, but current corroborating evidence keeps it incident-worthy: %s", assessment.Summary, memory.Explanation), 260)
				assessment.OperatorHint = memory.Explanation
				if severityRank(assessment.Severity) < severityRank("medium") {
					assessment.Severity = "medium"
				}
			case "confirmed_anomaly":
				assessment.Confidence = clamp01(maxFloat(assessment.Confidence, 0.55+memory.Confidence*0.25))
				assessment.Triggered = true
				assessment.Summary = truncateString(fmt.Sprintf("%s Historical memory confirms this is a real deviation: %s", assessment.Summary, memory.Explanation), 260)
			}
		}
		assessments = append(assessments, assessment)
		indexByKey[item.Key] = len(assessments) - 1
	}

	if len(current) > 0 && len(history) >= 4 {
		opts := predictive.DefaultOptions(minWorkflowDuration(window, 30*time.Minute), 85, 0.85, 0.01, 85)
		for _, finding := range predictive.Evaluate(collector, current, history, opts) {
			key := predictiveSeriesKey(finding.Metric)
			if key == "" {
				continue
			}
			if idx, ok := indexByKey[key]; ok {
				assessments[idx].Forecast = finding.Forecast
				assessments[idx].ForecastValue = finding.ForecastValue
				assessments[idx].ThresholdValue = maxFloat(assessments[idx].ThresholdValue, finding.ThresholdValue)
				assessments[idx].Confidence = maxFloat(assessments[idx].Confidence, clamp01(finding.Confidence))
				assessments[idx].Triggered = true
				assessments[idx].DetectionMode = "series_slope+predictive_forecast"
				if severityRank(finding.Severity) > severityRank(assessments[idx].Severity) {
					assessments[idx].Severity = strings.ToLower(strings.TrimSpace(finding.Severity))
				}
				if assessments[idx].Trend == "stable" {
					assessments[idx].Trend = "forecast_risk"
				}
				assessments[idx].Summary = truncateString(fmt.Sprintf("%s Forecast: %s", assessments[idx].Summary, finding.Forecast), 240)
				continue
			}
			assessment := TrendAssessment{
				ID:             fmt.Sprintf("trend-%s", sanitizeID(key)),
				SeriesKey:      key,
				Display:        trendDisplayForPredictiveMetric(finding.Metric),
				Category:       seriesCategory(key),
				Scope:          profiles[key].scope,
				Entity:         collector,
				Trend:          "forecast_risk",
				Severity:       strings.ToLower(strings.TrimSpace(finding.Severity)),
				Confidence:     clamp01(finding.Confidence),
				DetectionMode:  "predictive_forecast",
				Latest:         finding.CurrentValue,
				Baseline:       finding.BaselineValue,
				DeltaPercent:   percentChange(finding.BaselineValue, finding.CurrentValue),
				ThresholdValue: finding.ThresholdValue,
				Forecast:       finding.Forecast,
				ForecastValue:  finding.ForecastValue,
				Triggered:      true,
				Summary:        finding.Summary,
				OperatorHint:   finding.Forecast,
				LastObservedAt: finding.EvidenceWindowEnd,
			}
			assessments = append(assessments, assessment)
			indexByKey[key] = len(assessments) - 1
		}
	}

	sort.Slice(assessments, func(i, j int) bool {
		if assessments[i].Triggered != assessments[j].Triggered {
			return assessments[i].Triggered
		}
		if severityRank(assessments[i].Severity) != severityRank(assessments[j].Severity) {
			return severityRank(assessments[i].Severity) > severityRank(assessments[j].Severity)
		}
		if assessments[i].Confidence != assessments[j].Confidence {
			return assessments[i].Confidence > assessments[j].Confidence
		}
		return assessments[i].Display < assessments[j].Display
	})
	if len(assessments) > 10 {
		assessments = assessments[:10]
	}
	return assessments
}

func syncBaselineMetrics(be *BaselineEngine, collectorID string, history []ingest.MetricHistorySample, current map[string]float64, now time.Time) []BaselineDrift {
	if be == nil || strings.TrimSpace(collectorID) == "" {
		return nil
	}
	specs := riskSeriesSpecs()
	for _, sample := range history {
		for _, spec := range specs {
			value, ok := spec.extract(sample.Metrics)
			if !ok {
				continue
			}
			be.RecordMetric(collectorID, spec.key, value, sample.Timestamp)
		}
	}
	if len(current) > 0 {
		for _, spec := range specs {
			value, ok := spec.extract(current)
			if !ok {
				continue
			}
			be.RecordMetric(collectorID, spec.key, value, now)
		}
	}
	drifts := be.DetectDrift(collectorID)
	sort.Slice(drifts, func(i, j int) bool {
		if severityRank(drifts[i].Severity) != severityRank(drifts[j].Severity) {
			return severityRank(drifts[i].Severity) > severityRank(drifts[j].Severity)
		}
		return math.Abs(drifts[i].Deviation) > math.Abs(drifts[j].Deviation)
	})
	if len(drifts) > 6 {
		drifts = drifts[:6]
	}
	return drifts
}

func probableCauseForEvidence(items ...string) string {
	return hypothesisTitleFromSignal(strings.Join(items, " "))
}

func buildInvestigationEvents(collectorID string, trends []TrendAssessment, cooccurrences []JointRiskCooccurrence, drifts []BaselineDrift) []InvestigationEvent {
	collector := firstNonEmpty(collectorID, "fleet")
	events := make([]InvestigationEvent, 0, len(trends)+len(cooccurrences)+len(drifts))

	for _, trend := range trends {
		if !trend.Triggered && trend.Confidence < 0.55 {
			continue
		}
		category := "trend_watch"
		if trend.Category == "hardware" {
			category = "hardware_warning"
		}
		probableCause := probableCauseForEvidence(trend.Display, trend.SeriesKey)
		events = append(events, InvestigationEvent{
			ID:                fmt.Sprintf("event-%s", sanitizeID(trend.ID)),
			Category:          category,
			Severity:          trend.Severity,
			Confidence:        trend.Confidence,
			Scope:             trend.Scope,
			Entity:            firstNonEmpty(trend.Entity, collector),
			Title:             fmt.Sprintf("%s trend: %s", strings.Title(strings.ReplaceAll(trend.Trend, "_", " ")), trend.Display),
			Symptom:           fmt.Sprintf("%s latest %.3f vs baseline %.3f", trend.Display, trend.Latest, trend.Baseline),
			ProbableCause:     probableCause,
			Summary:           trend.Summary,
			SupportingSignals: []string{trend.Display},
			Evidence:          dedupeStrings([]string{trend.Summary, trend.Forecast}),
			RecommendedChecks: checksForTrendAssessment(trend),
			RetrievalHint:     strings.TrimSpace(fmt.Sprintf("%s %s %s", trend.Display, trend.Trend, probableCause)),
		})
	}

	for _, co := range cooccurrences {
		probableCause := probableCauseForEvidence(strings.Join(co.Signals, " "))
		events = append(events, InvestigationEvent{
			ID:                fmt.Sprintf("event-%s", sanitizeID(co.ID)),
			Category:          "weak_signal_cluster",
			Severity:          priorityForConfidence(co.CombinedScore),
			Confidence:        clamp01(co.CombinedScore),
			Scope:             co.Scope,
			Entity:            firstNonEmpty(co.Entity, collector),
			Title:             fmt.Sprintf("Compound signal cluster: %s", strings.Join(co.Signals, " + ")),
			Symptom:           truncateString(co.Explanation, 180),
			ProbableCause:     probableCause,
			Summary:           co.ActionableCause,
			SupportingSignals: append([]string(nil), co.Signals...),
			Evidence: []string{
				fmt.Sprintf("correlation=%.2f combined_score=%.2f", co.Correlation, co.CombinedScore),
				co.Explanation,
			},
			RecommendedChecks: checksForSignalCluster(co),
			RetrievalHint:     strings.TrimSpace(fmt.Sprintf("%s %s", strings.Join(co.Signals, " "), probableCause)),
		})
	}

	for _, drift := range drifts {
		probableCause := probableCauseForEvidence(drift.Metric, drift.Dimension)
		events = append(events, InvestigationEvent{
			ID:                fmt.Sprintf("event-baseline-%s", sanitizeID(drift.Metric)),
			Category:          "baseline_drift",
			Severity:          firstNonEmpty(strings.ToLower(strings.TrimSpace(drift.Severity)), "medium"),
			Confidence:        clamp01(0.45 + math.Min(math.Abs(drift.Deviation), 3.0)*0.12),
			Scope:             drift.Dimension,
			Entity:            collector,
			Title:             fmt.Sprintf("Baseline drift on %s", strings.ReplaceAll(drift.Metric, "_", " ")),
			Symptom:           fmt.Sprintf("current %.3f baseline %.3f deviation %.2fσ", drift.Current, drift.Baseline, drift.Deviation),
			ProbableCause:     probableCause,
			Summary:           fmt.Sprintf("%s drifted away from the recent baseline for %s.", strings.ReplaceAll(drift.Metric, "_", " "), collector),
			SupportingSignals: []string{drift.Metric},
			Evidence: []string{
				fmt.Sprintf("baseline=%.3f current=%.3f percentile=%.0f", drift.Baseline, drift.Current, drift.Percentile),
			},
			RecommendedChecks: checksForHypothesis(probableCause),
			RetrievalHint:     strings.TrimSpace(fmt.Sprintf("%s baseline drift %s", drift.Metric, probableCause)),
		})
	}

	seen := make(map[string]struct{}, len(events))
	out := make([]InvestigationEvent, 0, len(events))
	for _, event := range events {
		key := strings.ToLower(strings.TrimSpace(event.Category + "|" + event.Title))
		if key == "|" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		event.Evidence = dedupeStrings(event.Evidence)
		event.SupportingSignals = dedupeStrings(event.SupportingSignals)
		event.RecommendedChecks = dedupeStrings(event.RecommendedChecks)
		out = append(out, event)
	}

	sort.Slice(out, func(i, j int) bool {
		if severityRank(out[i].Severity) != severityRank(out[j].Severity) {
			return severityRank(out[i].Severity) > severityRank(out[j].Severity)
		}
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].Title < out[j].Title
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func minWorkflowDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}
