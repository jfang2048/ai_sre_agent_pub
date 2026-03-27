package predictive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

const (
	algorithmName    = "ewma+zscore+adaptive-threshold"
	algorithmVersion = "v0.7"
)

type Finding struct {
	PredictionID        string    `json:"prediction_id"`
	AssetID             string    `json:"asset_id"`
	Metric              string    `json:"metric"`
	PredictiveSLO       string    `json:"predictive_slo"`
	Title               string    `json:"title"`
	Summary             string    `json:"summary"`
	Forecast            string    `json:"forecast,omitempty"`
	Severity            string    `json:"severity"`
	Confidence          float64   `json:"confidence"`
	CurrentValue        float64   `json:"current_value"`
	BaselineValue       float64   `json:"baseline_value"`
	ForecastValue       float64   `json:"forecast_value,omitempty"`
	ThresholdValue      float64   `json:"threshold_value"`
	ZScore              float64   `json:"z_score,omitempty"`
	HazardClass         string    `json:"hazard_class"`
	ControlReference    string    `json:"control_reference"`
	Algorithm           string    `json:"algorithm"`
	AlgorithmVersion    string    `json:"algorithm_version"`
	EvidenceWindowStart time.Time `json:"evidence_window_start"`
	EvidenceWindowEnd   time.Time `json:"evidence_window_end"`
	AuditHash           string    `json:"audit_hash"`
}

type Options struct {
	Horizon               time.Duration
	CPUHighPercent        float64
	MemoryPressureRatio   float64
	NetRetransmitRatio    float64
	GPUSMHighPercent      float64
	GPUTemperatureHighC   float64
	GPUPowerDrawRatioHigh float64
	PCIELinkHighPercent   float64
	IOPressureHigh        float64
	MaxFindings           int
}

type metricSample struct {
	at    time.Time
	value float64
}

type signalRule struct {
	metric           string
	title            string
	predictiveSLO    string
	summary          string
	forecastTemplate string
	hazardClass      string
	controlReference string
	severity         string
	staticThreshold  float64
	zScoreThreshold  float64
	adaptiveSigma    float64
	minSamples       int
	confidenceBias   float64
	valueExtractor   func(map[string]float64) (float64, bool)
}

func DefaultOptions(horizon time.Duration, cpuHighPercent, memoryPressureRatio, netRetransmitRatio, gpuSMHighPercent float64) Options {
	if horizon <= 0 {
		horizon = 30 * time.Minute
	}
	if cpuHighPercent <= 0 {
		cpuHighPercent = 85
	}
	if memoryPressureRatio <= 0 {
		memoryPressureRatio = 0.85
	}
	if netRetransmitRatio <= 0 {
		netRetransmitRatio = 0.01
	}
	if gpuSMHighPercent <= 0 {
		gpuSMHighPercent = 85
	}
	return Options{
		Horizon:               horizon,
		CPUHighPercent:        cpuHighPercent,
		MemoryPressureRatio:   memoryPressureRatio,
		NetRetransmitRatio:    netRetransmitRatio,
		GPUSMHighPercent:      gpuSMHighPercent,
		GPUTemperatureHighC:   82,
		GPUPowerDrawRatioHigh: 92,
		PCIELinkHighPercent:   80,
		IOPressureHigh:        20,
		MaxFindings:           6,
	}
}

func Evaluate(assetID string, current map[string]float64, history []ingest.MetricHistorySample, opts Options) []Finding {
	rules := defaultRules(opts)
	findings := make([]Finding, 0, len(rules))
	now := time.Now().UTC()

	for _, rule := range rules {
		series := seriesForRule(rule, history)
		currentValue, ok := rule.valueExtractor(current)
		if !ok || !isFinite(currentValue) {
			continue
		}
		if len(series) > 0 {
			last := series[len(series)-1]
			if last.at.Equal(now) && last.value == currentValue {
				// already have an identical current sample
			} else {
				series = append(series, metricSample{at: now, value: currentValue})
			}
		} else {
			series = append(series, metricSample{at: now, value: currentValue})
		}
		finding, ok := evaluateRule(assetID, rule, series, opts.Horizon)
		if ok {
			findings = append(findings, finding)
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if severityRank(findings[i].Severity) != severityRank(findings[j].Severity) {
			return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
		}
		if findings[i].Confidence != findings[j].Confidence {
			return findings[i].Confidence > findings[j].Confidence
		}
		return findings[i].Metric < findings[j].Metric
	})
	if opts.MaxFindings > 0 && len(findings) > opts.MaxFindings {
		findings = findings[:opts.MaxFindings]
	}
	return findings
}

func Summaries(findings []Finding) ([]string, []string) {
	if len(findings) == 0 {
		return nil, nil
	}
	alerts := make([]string, 0, len(findings))
	forecasts := make([]string, 0, len(findings))
	seenAlerts := make(map[string]struct{}, len(findings))
	seenForecasts := make(map[string]struct{}, len(findings))
	for _, finding := range findings {
		if finding.Title != "" {
			if _, ok := seenAlerts[finding.Title]; !ok {
				alerts = append(alerts, finding.Title)
				seenAlerts[finding.Title] = struct{}{}
			}
		}
		if finding.Forecast != "" {
			if _, ok := seenForecasts[finding.Forecast]; !ok {
				forecasts = append(forecasts, finding.Forecast)
				seenForecasts[finding.Forecast] = struct{}{}
			}
		}
	}
	return alerts, forecasts
}

func evaluateRule(assetID string, rule signalRule, series []metricSample, horizon time.Duration) (Finding, bool) {
	if len(series) < rule.minSamples {
		return Finding{}, false
	}
	values := make([]float64, 0, len(series))
	for _, sample := range series {
		if isFinite(sample.value) {
			values = append(values, sample.value)
		}
	}
	if len(values) < rule.minSamples {
		return Finding{}, false
	}

	currentValue := values[len(values)-1]
	baselineValues := values[:len(values)-1]
	if len(baselineValues) < rule.minSamples-1 {
		baselineValues = values
	}
	baseline, stddev := meanStddev(baselineValues)
	fast := ewma(values, 0.45)
	slow := ewma(values, 0.18)
	z := zscore(currentValue, baseline, stddev)
	adaptiveThreshold := rule.staticThreshold
	if stddev > 0 {
		adaptiveThreshold = math.Max(adaptiveThreshold, baseline+rule.adaptiveSigma*stddev)
	}
	if adaptiveThreshold <= 0 && baseline > 0 && stddev > 0 {
		adaptiveThreshold = baseline + rule.adaptiveSigma*stddev
	}

	step := averageStep(series)
	forecastValue := currentValue
	if step > 0 {
		stepsAhead := math.Max(1, float64(horizon)/float64(step))
		slope := averageSlope(values)
		forecastValue = currentValue + slope*stepsAhead
	}

	risingRisk := currentValue >= adaptiveThreshold || forecastValue >= rule.staticThreshold || (fast > slow && z >= rule.zScoreThreshold)
	if !risingRisk {
		return Finding{}, false
	}
	confidence := clamp01(0.35 + rule.confidenceBias + confidenceFromZScore(z) + confidenceFromTrend(fast, slow, currentValue) + confidenceFromSamples(len(values)))
	if confidence < 0.45 {
		return Finding{}, false
	}
	severity := rule.severity
	if currentValue >= rule.staticThreshold*1.05 || forecastValue >= rule.staticThreshold*1.1 {
		severity = "high"
	}
	if currentValue >= rule.staticThreshold*1.15 || z >= rule.zScoreThreshold+1.5 {
		severity = "critical"
	}

	windowStart := series[0].at.UTC()
	windowEnd := series[len(series)-1].at.UTC()
	predictionID := predictionID(assetID, rule.metric, windowEnd)
	summary := fmt.Sprintf(rule.summary, currentValue, baseline, z)
	forecast := fmt.Sprintf(rule.forecastTemplate, rule.staticThreshold, horizon.Round(time.Minute), forecastValue)

	finding := Finding{
		PredictionID:        predictionID,
		AssetID:             assetID,
		Metric:              rule.metric,
		PredictiveSLO:       rule.predictiveSLO,
		Title:               rule.title,
		Summary:             summary,
		Forecast:            forecast,
		Severity:            severity,
		Confidence:          confidence,
		CurrentValue:        currentValue,
		BaselineValue:       baseline,
		ForecastValue:       forecastValue,
		ThresholdValue:      rule.staticThreshold,
		ZScore:              z,
		HazardClass:         rule.hazardClass,
		ControlReference:    rule.controlReference,
		Algorithm:           algorithmName,
		AlgorithmVersion:    algorithmVersion,
		EvidenceWindowStart: windowStart,
		EvidenceWindowEnd:   windowEnd,
	}
	finding.AuditHash = auditHash(finding)
	return finding, true
}

func defaultRules(opts Options) []signalRule {
	return []signalRule{
		{
			metric:           "node_gpu_temperature_celsius",
			title:            "GPU thermal runaway risk detected",
			predictiveSLO:    "gpu_thermal_headroom",
			summary:          "GPU temperature is drifting upward (current %.1fC, baseline %.1fC, z-score %.2f)",
			forecastTemplate: "GPU temperature could reach %.0fC risk boundary within %s (forecast %.1fC)",
			hazardClass:      "thermal_runaway",
			controlReference: "IEC-61508-predictive-warning",
			severity:         "warning",
			staticThreshold:  opts.GPUTemperatureHighC,
			zScoreThreshold:  2.2,
			adaptiveSigma:    1.8,
			minSamples:       6,
			confidenceBias:   0.08,
			valueExtractor:   directMetric("node_gpu_temperature_celsius"),
		},
		{
			metric:           "node_gpu_power_draw_ratio_percent",
			title:            "GPU power envelope risk rising",
			predictiveSLO:    "gpu_power_stability",
			summary:          "GPU power draw is approaching the enforced power limit (current %.1f%%, baseline %.1f%%, z-score %.2f)",
			forecastTemplate: "GPU power draw could cross %.0f%% of power cap within %s (forecast %.1f%%)",
			hazardClass:      "power_anomaly",
			controlReference: "ISO-55001-asset-health",
			severity:         "warning",
			staticThreshold:  opts.GPUPowerDrawRatioHigh,
			zScoreThreshold:  2.0,
			adaptiveSigma:    1.6,
			minSamples:       6,
			confidenceBias:   0.06,
			valueExtractor:   gpuPowerRatio,
		},
		{
			metric:           "node_gpu_pcie_link_utilization_percent",
			title:            "PCIe saturation risk rising",
			predictiveSLO:    "gpu_feed_path_headroom",
			summary:          "PCIe link pressure is climbing (current %.1f%%, baseline %.1f%%, z-score %.2f)",
			forecastTemplate: "PCIe utilization could cross %.0f%% within %s (forecast %.1f%%)",
			hazardClass:      "pcie_path_pressure",
			controlReference: "ISO-55001-throughput-control",
			severity:         "warning",
			staticThreshold:  opts.PCIELinkHighPercent,
			zScoreThreshold:  2.0,
			adaptiveSigma:    1.7,
			minSamples:       6,
			confidenceBias:   0.03,
			valueExtractor:   directMetric("node_gpu_pcie_link_utilization_percent"),
		},
		{
			metric:           "node_memory_used_percent",
			title:            "Memory exhaustion risk rising",
			predictiveSLO:    "memory_headroom",
			summary:          "Memory headroom is shrinking steadily (current %.1f%%, baseline %.1f%%, z-score %.2f)",
			forecastTemplate: "Memory pressure could cross %.0f%% within %s (forecast %.1f%%)",
			hazardClass:      "oom_precursor",
			controlReference: "IEC-61508-safe-degraded-mode",
			severity:         "warning",
			staticThreshold:  opts.MemoryPressureRatio * 100,
			zScoreThreshold:  1.8,
			adaptiveSigma:    1.5,
			minSamples:       6,
			confidenceBias:   0.07,
			valueExtractor:   memoryUsedPercent,
		},
		{
			metric:           "node_tcp_retransmit_ratio",
			title:            "Network jitter risk rising",
			predictiveSLO:    "fabric_delivery_quality",
			summary:          "TCP retransmit ratio is above its recent baseline (current %.4f, baseline %.4f, z-score %.2f)",
			forecastTemplate: "Retransmit ratio could exceed %.4f within %s (forecast %.4f)",
			hazardClass:      "network_jitter",
			controlReference: "SRE-predictive-slo-fabric",
			severity:         "warning",
			staticThreshold:  opts.NetRetransmitRatio,
			zScoreThreshold:  2.2,
			adaptiveSigma:    2.0,
			minSamples:       6,
			confidenceBias:   0.05,
			valueExtractor:   directMetric("node_tcp_retransmit_ratio"),
		},
		{
			metric:           "node_pressure_io_some_avg10",
			title:            "IO pressure risk rising",
			predictiveSLO:    "io_service_stability",
			summary:          "IO pressure is rising above steady-state expectations (current %.1f, baseline %.1f, z-score %.2f)",
			forecastTemplate: "IO pressure could exceed %.0f within %s (forecast %.1f)",
			hazardClass:      "io_degradation",
			controlReference: "SRE-predictive-slo-storage",
			severity:         "warning",
			staticThreshold:  opts.IOPressureHigh,
			zScoreThreshold:  2.0,
			adaptiveSigma:    1.7,
			minSamples:       6,
			confidenceBias:   0.03,
			valueExtractor:   directMetric("node_pressure_io_some_avg10"),
		},
		{
			metric:           "node_cpu_usage_percent",
			title:            "CPU saturation risk rising",
			predictiveSLO:    "compute_headroom",
			summary:          "CPU demand is climbing above its recent baseline (current %.1f%%, baseline %.1f%%, z-score %.2f)",
			forecastTemplate: "CPU utilization could exceed %.0f%% within %s (forecast %.1f%%)",
			hazardClass:      "compute_contention",
			controlReference: "SRE-predictive-slo-latency",
			severity:         "warning",
			staticThreshold:  opts.CPUHighPercent,
			zScoreThreshold:  1.9,
			adaptiveSigma:    1.5,
			minSamples:       6,
			confidenceBias:   0.04,
			valueExtractor:   directMetric("node_cpu_usage_percent"),
		},
	}
}

func seriesForRule(rule signalRule, history []ingest.MetricHistorySample) []metricSample {
	series := make([]metricSample, 0, len(history))
	for _, sample := range history {
		value, ok := rule.valueExtractor(sample.Metrics)
		if !ok || !isFinite(value) {
			continue
		}
		series = append(series, metricSample{
			at:    sample.Timestamp.UTC(),
			value: value,
		})
	}
	return series
}

func directMetric(name string) func(map[string]float64) (float64, bool) {
	return func(metrics map[string]float64) (float64, bool) {
		value, ok := metrics[name]
		return value, ok
	}
}

func memoryUsedPercent(metrics map[string]float64) (float64, bool) {
	if value, ok := metrics["node_memory_used_percent"]; ok {
		return value, true
	}
	used, ok := metrics["node_memory_Used_bytes"]
	if !ok {
		used, ok = metrics["node_memory_used_bytes"]
	}
	total, totalOK := metrics["node_memory_MemTotal_bytes"]
	if !totalOK {
		total, totalOK = metrics["node_memory_total_bytes"]
	}
	if !ok || !totalOK || total <= 0 {
		return 0, false
	}
	return (used / total) * 100, true
}

func gpuPowerRatio(metrics map[string]float64) (float64, bool) {
	draw, ok := metrics["node_gpu_power_draw_watts"]
	limit, limitOK := metrics["node_gpu_power_limit_watts"]
	if !ok || !limitOK || limit <= 0 {
		return 0, false
	}
	return (draw / limit) * 100, true
}

func averageSlope(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var total float64
	for i := 1; i < len(values); i++ {
		total += values[i] - values[i-1]
	}
	return total / float64(len(values)-1)
}

func averageStep(series []metricSample) time.Duration {
	if len(series) < 2 {
		return 0
	}
	var total time.Duration
	var count int
	for i := 1; i < len(series); i++ {
		step := series[i].at.Sub(series[i-1].at)
		if step <= 0 {
			continue
		}
		total += step
		count++
	}
	if count == 0 {
		return 0
	}
	return total / time.Duration(count)
}

func confidenceFromZScore(z float64) float64 {
	if z <= 0 {
		return 0
	}
	return math.Min(0.35, z/8)
}

func confidenceFromTrend(fast, slow, current float64) float64 {
	if current <= 0 {
		return 0
	}
	delta := fast - slow
	if delta <= 0 {
		return 0
	}
	return math.Min(0.2, delta/current)
}

func confidenceFromSamples(samples int) float64 {
	if samples <= 0 {
		return 0
	}
	return math.Min(0.1, float64(samples)/100)
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "high":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

func predictionID(assetID, metric string, at time.Time) string {
	return fmt.Sprintf("pred-%s-%s-%d", sanitize(assetID), sanitize(metric), at.UnixNano())
}

func sanitize(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return string(out)
}

func auditHash(f Finding) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%0.4f|%s|%s",
		f.PredictionID,
		f.AssetID,
		f.Metric,
		f.PredictiveSLO,
		f.HazardClass,
		f.ControlReference,
		f.CurrentValue,
		f.EvidenceWindowStart.UTC().Format(time.RFC3339Nano),
		f.EvidenceWindowEnd.UTC().Format(time.RFC3339Nano),
	)))
	return hex.EncodeToString(sum[:16])
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

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
