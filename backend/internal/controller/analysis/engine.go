// Package analysis implements root cause analysis for the SRE controller.
//
// This package provides a layered analysis approach:
//   - Level 1: Threshold-based alerts (fast, deterministic)
//   - Level 2: Statistical anomaly detection (z-score, trend analysis)
//   - Level 3: Correlation analysis (cross-metric relationships)
//   - Level 4: LLM-enhanced analysis (optional, external API)
//
// Design Principles:
//   - Simple rules first, complex AI second
//   - Everything must be explainable
//   - Fail-safe: analysis failures don't break monitoring
//   - Environment variables for external API keys (never hardcoded)
package analysis

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Config holds analysis engine configuration
type Config struct {
	// Enable/disable analysis levels
	EnableThresholdAlerts  bool `yaml:"enable_threshold_alerts"`
	EnableAnomalyDetection bool `yaml:"enable_anomaly_detection"`
	EnableCorrelation      bool `yaml:"enable_correlation"`
	EnableLLMAnalysis      bool `yaml:"enable_llm_analysis"`

	// Thresholds for anomaly detection
	ZScoreThreshold    float64 `yaml:"zscore_threshold"`
	TrendWindowSize    int     `yaml:"trend_window_size"`
	CorrelationMinimum float64 `yaml:"correlation_minimum"`

	// Analysis intervals
	AnalysisInterval time.Duration `yaml:"analysis_interval"`
	RetentionPeriod  time.Duration `yaml:"retention_period"`

	// LLM Configuration (keys from environment)
	LLMProvider string        `yaml:"llm_provider"` // "openai", "anthropic", etc.
	LLMModel    string        `yaml:"llm_model"`
	LLMTimeout  time.Duration `yaml:"llm_timeout"`
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		EnableThresholdAlerts:  true,
		EnableAnomalyDetection: true,
		EnableCorrelation:      true,
		EnableLLMAnalysis:      false, // Disabled by default, requires API key

		ZScoreThreshold:    2.5,
		TrendWindowSize:    10,
		CorrelationMinimum: 0.7,

		AnalysisInterval: 30 * time.Second,
		RetentionPeriod:  1 * time.Hour,

		LLMProvider: "openai",
		LLMModel:    "gpt-4o-mini",
		LLMTimeout:  30 * time.Second,
	}
}

// Severity represents alert severity levels
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Alert represents a generated alert
type Alert struct {
	ID          string            `json:"id"`
	NodeName    string            `json:"node_name"`
	MetricName  string            `json:"metric_name"`
	Severity    Severity          `json:"severity"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Value       float64           `json:"value"`
	Threshold   float64           `json:"threshold,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	ResolvedAt  *time.Time        `json:"resolved_at,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Anomaly represents a detected anomaly
type Anomaly struct {
	NodeName    string    `json:"node_name"`
	MetricName  string    `json:"metric_name"`
	Score       float64   `json:"score"`     // Z-score or similar
	Direction   string    `json:"direction"` // "up", "down", "spike"
	CurrentVal  float64   `json:"current_value"`
	ExpectedVal float64   `json:"expected_value"`
	DetectedAt  time.Time `json:"detected_at"`
	Reason      string    `json:"reason"`
}

// Correlation represents a correlation between metrics
type Correlation struct {
	MetricA     string    `json:"metric_a"`
	MetricB     string    `json:"metric_b"`
	Coefficient float64   `json:"coefficient"` // Pearson correlation
	Direction   string    `json:"direction"`   // "positive", "negative"
	Lag         int       `json:"lag"`         // Time lag in samples
	DetectedAt  time.Time `json:"detected_at"`
}

// RootCauseAnalysis represents a complete RCA result
type RootCauseAnalysis struct {
	ID                  string    `json:"id"`
	NodeName            string    `json:"node_name"`
	Symptom             string    `json:"symptom"`
	RootCause           string    `json:"root_cause"`
	Confidence          float64   `json:"confidence"`
	ContributingFactors []string  `json:"contributing_factors"`
	Recommendations     []string  `json:"recommendations"`
	RelatedAlerts       []string  `json:"related_alerts"`
	RelatedAnomalies    []Anomaly `json:"related_anomalies"`
	AnalyzedAt          time.Time `json:"analyzed_at"`
	AnalysisMethod      string    `json:"analysis_method"` // "rules", "statistical", "llm"
	LLMReport           string    `json:"llm_report,omitempty"`
}

// AnalysisResult is the output of an analysis cycle
type AnalysisResult struct {
	Timestamp    time.Time           `json:"timestamp"`
	Alerts       []Alert             `json:"alerts"`
	Anomalies    []Anomaly           `json:"anomalies"`
	Correlations []Correlation       `json:"correlations"`
	RCAs         []RootCauseAnalysis `json:"root_cause_analyses"`
}

// MetricSample represents a single metric data point
type MetricSample struct {
	Name      string
	Value     float64
	Timestamp time.Time
	Labels    map[string]string
}

// NodeData represents collected data for a node
type NodeData struct {
	NodeName  string
	Samples   map[string][]MetricSample // metric name -> samples (time-ordered)
	UpdatedAt time.Time
}

// Engine is the main analysis engine
type Engine struct {
	config Config
	logger *zap.Logger

	mu         sync.RWMutex
	nodeData   map[string]*NodeData
	alerts     map[string]*Alert // alert ID -> alert
	anomalies  []Anomaly
	rcaResults []RootCauseAnalysis

	// External analyzers (optional)
	llmClient LLMAnalyzer
	evidence  EvidenceProvider

	// Lifecycle
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
}

// LLMAnalyzer is the interface for LLM-based analysis
type LLMAnalyzer interface {
	Analyze(ctx context.Context, data AnalysisInput) (*LLMAnalysisResult, error)
}

// EvidenceProvider supplies optional logs/process summaries for LLM inputs.
type EvidenceProvider interface {
	EvidenceForNode(nodeName string) (processes []ProcessSummary, logs []LogSummary)
}

// AnalysisInput is input data for LLM analysis
type AnalysisInput struct {
	NodeName  string             `json:"node_name"`
	Metrics   map[string]float64 `json:"metrics"`
	Trends    map[string]string  `json:"trends"`
	Anomalies []string           `json:"anomalies"`
	Context   string             `json:"context"`
	Schema    LLMInputSchema     `json:"schema"`
}

// LLMAnalysisResult is the result from LLM analysis
type LLMAnalysisResult struct {
	Summary         string   `json:"summary"`
	RootCause       string   `json:"root_cause"`
	Confidence      float64  `json:"confidence"`
	Recommendations []string `json:"recommendations"`
}

// New creates a new analysis engine
func New(cfg Config, logger *zap.Logger) (*Engine, error) {
	if logger == nil {
		logger, _ = zap.NewProduction()
	}

	e := &Engine{
		config:     cfg,
		logger:     logger.With(zap.String("component", "analysis")),
		nodeData:   make(map[string]*NodeData),
		alerts:     make(map[string]*Alert),
		anomalies:  make([]Anomaly, 0),
		rcaResults: make([]RootCauseAnalysis, 0),
	}

	return e, nil
}

// SetLLMClient sets the LLM client for enhanced analysis
func (e *Engine) SetLLMClient(client LLMAnalyzer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.llmClient = client
	e.logger.Info("LLM client configured")
}

// SetEvidenceProvider sets an optional evidence provider for LLM inputs.
func (e *Engine) SetEvidenceProvider(provider EvidenceProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evidence = provider
}

// Start starts the analysis engine
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("engine already running")
	}
	e.running = true
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.mu.Unlock()

	e.logger.Info("analysis engine started",
		zap.Bool("thresholds", e.config.EnableThresholdAlerts),
		zap.Bool("anomaly", e.config.EnableAnomalyDetection),
		zap.Bool("correlation", e.config.EnableCorrelation),
		zap.Bool("llm", e.config.EnableLLMAnalysis))

	go e.analysisLoop()
	return nil
}

// Stop stops the analysis engine
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return nil
	}

	e.logger.Info("stopping analysis engine")
	if e.cancel != nil {
		e.cancel()
	}
	e.running = false
	return nil
}

// IngestMetrics ingests metrics data for a node
func (e *Engine) IngestMetrics(nodeName string, samples []MetricSample) {
	e.mu.Lock()
	defer e.mu.Unlock()

	data, exists := e.nodeData[nodeName]
	if !exists {
		data = &NodeData{
			NodeName: nodeName,
			Samples:  make(map[string][]MetricSample),
		}
		e.nodeData[nodeName] = data
	}

	// Append samples and maintain retention
	for _, sample := range samples {
		existing := data.Samples[sample.Name]
		existing = append(existing, sample)

		// Prune old samples
		cutoff := time.Now().Add(-e.config.RetentionPeriod)
		pruned := make([]MetricSample, 0)
		for _, s := range existing {
			if s.Timestamp.After(cutoff) {
				pruned = append(pruned, s)
			}
		}
		data.Samples[sample.Name] = pruned
	}
	data.UpdatedAt = time.Now()
}

// GetAlerts returns current active alerts
func (e *Engine) GetAlerts() []Alert {
	e.mu.RLock()
	defer e.mu.RUnlock()

	alerts := make([]Alert, 0, len(e.alerts))
	for _, a := range e.alerts {
		alerts = append(alerts, *a)
	}

	// Sort by severity and time
	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].Severity != alerts[j].Severity {
			return severityOrder(alerts[i].Severity) < severityOrder(alerts[j].Severity)
		}
		return alerts[i].CreatedAt.After(alerts[j].CreatedAt)
	})

	return alerts
}

// GetAnomalies returns detected anomalies
func (e *Engine) GetAnomalies() []Anomaly {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]Anomaly, len(e.anomalies))
	copy(result, e.anomalies)
	return result
}

// GetRCAs returns root cause analyses
func (e *Engine) GetRCAs() []RootCauseAnalysis {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]RootCauseAnalysis, len(e.rcaResults))
	copy(result, e.rcaResults)
	return result
}

// GetNodeMetricsSnapshot returns a point-in-time snapshot of latest metric values.
func (e *Engine) GetNodeMetricsSnapshot(nodeName string) map[string]float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()

	data := e.nodeData[nodeName]
	if data == nil {
		return nil
	}

	snapshot := make(map[string]float64, len(data.Samples))
	for name, samples := range data.Samples {
		if len(samples) == 0 {
			continue
		}
		snapshot[name] = samples[len(samples)-1].Value
	}
	return snapshot
}

// GetNodeTrendsSnapshot returns a trend summary for each metric.
func (e *Engine) GetNodeTrendsSnapshot(nodeName string) map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	data := e.nodeData[nodeName]
	if data == nil {
		return nil
	}

	trends := make(map[string]string, len(data.Samples))
	for name, samples := range data.Samples {
		trends[name] = trendFromSamples(samples)
	}
	return trends
}

// analysisLoop runs periodic analysis
func (e *Engine) analysisLoop() {
	ticker := time.NewTicker(e.config.AnalysisInterval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.runAnalysis()
		}
	}
}

// runAnalysis performs a complete analysis cycle
func (e *Engine) runAnalysis() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Debug("running analysis cycle")

	// Clear previous anomalies (alerts persist until resolved)
	e.anomalies = make([]Anomaly, 0)

	for nodeName, data := range e.nodeData {
		// Level 1: Threshold checks
		if e.config.EnableThresholdAlerts {
			e.checkThresholds(nodeName, data)
		}

		// Level 2: Anomaly detection
		if e.config.EnableAnomalyDetection {
			e.detectAnomalies(nodeName, data)
		}
	}

	// Level 3: Cross-metric correlation
	if e.config.EnableCorrelation {
		e.analyzeCorrelations()
	}

	// Level 4: Root cause analysis
	e.performRCA()

	e.logger.Debug("analysis cycle complete",
		zap.Int("alerts", len(e.alerts)),
		zap.Int("anomalies", len(e.anomalies)))
}

// checkThresholds performs threshold-based alerting
func (e *Engine) checkThresholds(nodeName string, data *NodeData) {
	thresholds := getDefaultThresholds()

	for metricName, samples := range data.Samples {
		if len(samples) == 0 {
			continue
		}

		currentVal := samples[len(samples)-1].Value

		if threshold, ok := thresholds[metricName]; ok {
			alertID := fmt.Sprintf("%s:%s", nodeName, metricName)

			var severity Severity
			var violated bool
			var description string

			switch threshold.Direction {
			case "above":
				if currentVal > threshold.Critical {
					severity = SeverityCritical
					violated = true
					description = fmt.Sprintf("%s is critically high: %.2f > %.2f", metricName, currentVal, threshold.Critical)
				} else if currentVal > threshold.Warning {
					severity = SeverityWarning
					violated = true
					description = fmt.Sprintf("%s is elevated: %.2f > %.2f", metricName, currentVal, threshold.Warning)
				}
			case "below":
				if currentVal < threshold.Critical {
					severity = SeverityCritical
					violated = true
					description = fmt.Sprintf("%s is critically low: %.2f < %.2f", metricName, currentVal, threshold.Critical)
				} else if currentVal < threshold.Warning {
					severity = SeverityWarning
					violated = true
					description = fmt.Sprintf("%s is low: %.2f < %.2f", metricName, currentVal, threshold.Warning)
				}
			}

			if violated {
				if existing, exists := e.alerts[alertID]; exists {
					// Update existing alert
					existing.Value = currentVal
					existing.Severity = severity
					existing.Description = description
				} else {
					// Create new alert
					e.alerts[alertID] = &Alert{
						ID:          alertID,
						NodeName:    nodeName,
						MetricName:  metricName,
						Severity:    severity,
						Title:       threshold.Title,
						Description: description,
						Value:       currentVal,
						Threshold:   threshold.Warning,
						CreatedAt:   time.Now(),
					}
				}
			} else {
				// Resolve alert if it exists
				if existing, exists := e.alerts[alertID]; exists {
					now := time.Now()
					existing.ResolvedAt = &now
					// Keep resolved alerts briefly for visibility
					go func(id string) {
						time.Sleep(5 * time.Minute)
						e.mu.Lock()
						delete(e.alerts, id)
						e.mu.Unlock()
					}(alertID)
				}
			}
		}
	}
}

// detectAnomalies performs statistical anomaly detection
func (e *Engine) detectAnomalies(nodeName string, data *NodeData) {
	for metricName, samples := range data.Samples {
		if len(samples) < e.config.TrendWindowSize {
			continue
		}

		values := make([]float64, len(samples))
		for i, s := range samples {
			values[i] = s.Value
		}

		// Calculate statistics
		mean, stdDev := calculateStats(values)
		if stdDev == 0 {
			continue
		}

		currentVal := values[len(values)-1]
		zScore := (currentVal - mean) / stdDev

		if math.Abs(zScore) > e.config.ZScoreThreshold {
			direction := "up"
			if zScore < 0 {
				direction = "down"
			}

			e.anomalies = append(e.anomalies, Anomaly{
				NodeName:    nodeName,
				MetricName:  metricName,
				Score:       math.Abs(zScore),
				Direction:   direction,
				CurrentVal:  currentVal,
				ExpectedVal: mean,
				DetectedAt:  time.Now(),
				Reason:      fmt.Sprintf("Z-score %.2f exceeds threshold %.2f", zScore, e.config.ZScoreThreshold),
			})
		}

		// Detect trend changes
		if len(values) >= e.config.TrendWindowSize*2 {
			recentTrend := calculateTrend(values[len(values)-e.config.TrendWindowSize:])
			historicalTrend := calculateTrend(values[:len(values)-e.config.TrendWindowSize])

			trendChange := math.Abs(recentTrend - historicalTrend)
			if trendChange > 0.5 { // Significant trend change
				e.anomalies = append(e.anomalies, Anomaly{
					NodeName:   nodeName,
					MetricName: metricName,
					Score:      trendChange,
					Direction:  "trend_change",
					CurrentVal: currentVal,
					DetectedAt: time.Now(),
					Reason:     fmt.Sprintf("Trend changed from %.3f to %.3f", historicalTrend, recentTrend),
				})
			}
		}
	}
}

// analyzeCorrelations finds correlations between metrics
func (e *Engine) analyzeCorrelations() {
	// Collect all metric time series across nodes
	// For simplicity, focus on same-node correlations
	for _, data := range e.nodeData {
		metricNames := make([]string, 0, len(data.Samples))
		for name := range data.Samples {
			metricNames = append(metricNames, name)
		}

		// Pairwise correlation (limited to avoid O(n²) explosion)
		maxPairs := 20
		checked := 0
		for i := 0; i < len(metricNames) && checked < maxPairs; i++ {
			for j := i + 1; j < len(metricNames) && checked < maxPairs; j++ {
				samplesA := data.Samples[metricNames[i]]
				samplesB := data.Samples[metricNames[j]]

				if len(samplesA) < 10 || len(samplesB) < 10 {
					continue
				}

				// Align time series
				minLen := min(len(samplesA), len(samplesB))
				valuesA := make([]float64, minLen)
				valuesB := make([]float64, minLen)
				for k := 0; k < minLen; k++ {
					valuesA[k] = samplesA[len(samplesA)-minLen+k].Value
					valuesB[k] = samplesB[len(samplesB)-minLen+k].Value
				}

				corr := pearsonCorrelation(valuesA, valuesB)
				if math.Abs(corr) >= e.config.CorrelationMinimum {
					direction := "positive"
					if corr < 0 {
						direction = "negative"
					}
					// Log strong correlations (don't store all, just notable ones)
					e.logger.Debug("correlation detected",
						zap.String("metric_a", metricNames[i]),
						zap.String("metric_b", metricNames[j]),
						zap.Float64("coefficient", corr),
						zap.String("direction", direction))
				}
				checked++
			}
		}
	}
}

// performRCA performs root cause analysis based on detected issues
func (e *Engine) performRCA() {
	// Group anomalies and alerts by node
	nodeIssues := make(map[string]struct {
		alerts    []string
		anomalies []Anomaly
	})

	for _, alert := range e.alerts {
		if alert.ResolvedAt == nil {
			issues := nodeIssues[alert.NodeName]
			issues.alerts = append(issues.alerts, alert.ID)
			nodeIssues[alert.NodeName] = issues
		}
	}

	for _, anomaly := range e.anomalies {
		issues := nodeIssues[anomaly.NodeName]
		issues.anomalies = append(issues.anomalies, anomaly)
		nodeIssues[anomaly.NodeName] = issues
	}

	// For each node with issues, perform RCA
	for nodeName, issues := range nodeIssues {
		if len(issues.alerts) == 0 && len(issues.anomalies) == 0 {
			continue
		}

		rca := e.analyzeRootCause(nodeName, issues.alerts, issues.anomalies)
		if rca != nil {
			// Keep only recent RCAs
			e.rcaResults = append(e.rcaResults, *rca)
			if len(e.rcaResults) > 50 {
				e.rcaResults = e.rcaResults[len(e.rcaResults)-50:]
			}
		}
	}
}

// analyzeRootCause performs rule-based root cause analysis
func (e *Engine) analyzeRootCause(nodeName string, alertIDs []string, anomalies []Anomaly) *RootCauseAnalysis {
	// Rule-based RCA patterns
	// These are heuristics based on common SRE patterns

	data := e.nodeData[nodeName]
	if data == nil {
		return nil
	}

	rca := &RootCauseAnalysis{
		ID:               fmt.Sprintf("rca-%s-%d", nodeName, time.Now().UnixNano()),
		NodeName:         nodeName,
		RelatedAlerts:    alertIDs,
		RelatedAnomalies: anomalies,
		AnalyzedAt:       time.Now(),
		AnalysisMethod:   "rules",
	}

	// Gather current values
	currentValues := make(map[string]float64)
	for name, samples := range data.Samples {
		if len(samples) > 0 {
			currentValues[name] = samples[len(samples)-1].Value
		}
	}
	trends := make(map[string]string, len(data.Samples))
	for name, samples := range data.Samples {
		trends[name] = trendFromSamples(samples)
	}

	// Pattern matching for root cause determination
	cpuHigh := currentValues["system.cpu.usage"] > 80 || currentValues["node_cpu_usage_percent"] > 80
	memUsed := currentValues["node_memory_Used_bytes"]
	memTotal := currentValues["node_memory_MemTotal_bytes"]
	memPercent := 0.0
	if memTotal > 0 {
		memPercent = (memUsed / memTotal) * 100.0
	}
	memHigh := currentValues["system.memory.usage"] > 85 || memPercent > 85
	ioHigh := currentValues["system.disk.io.utilization"] > 80 || currentValues["node_disk_io_now"] > 50
	netHigh := currentValues["system.network.tx.utilization"] > 80 || currentValues["system.network.rx.utilization"] > 80 ||
		currentValues["node_network_receive_bytes_per_second"] > 200000000 || currentValues["node_network_transmit_bytes_per_second"] > 200000000
	loadHigh := currentValues["system.load.1m"] > 4.0 || currentValues["node_load1"] > 4.0

	gpuUtilHigh := currentValues["node_gpu_utilization_sm_avg_percent"] > 90
	gpuUtilLow := currentValues["node_gpu_utilization_sm_avg_percent"] > 0 && currentValues["node_gpu_utilization_sm_avg_percent"] < 30
	gpuMemHigh := currentValues["node_gpu_memory_used_percent"] > 90
	gpuTempHigh := currentValues["node_gpu_temperature_max_celsius"] > 85
	gpuThrottleAny := currentValues["node_gpu_throttle_active_any"] > 0
	gpuThrottleThermal := currentValues["node_gpu_throttle_thermal_any"] > 0
	gpuThrottlePower := currentValues["node_gpu_throttle_power_any"] > 0
	gpuProcessCount := currentValues["node_gpu_process_total"]
	pcieRx := currentValues["node_gpu_pcie_rx_total_mb_s"]
	pcieTx := currentValues["node_gpu_pcie_tx_total_mb_s"]
	swapPressure := currentValues["node_vmstat_pswpout"] > 0 || currentValues["node_vmstat_pswpin"] > 0
	oomEvents := currentValues["node_vmstat_oom_kill"] > 0

	// Determine primary symptom and likely cause
	switch {
	case gpuThrottleThermal || (gpuTempHigh && gpuThrottleAny):
		rca.Symptom = "GPU thermal throttling detected"
		rca.RootCause = "GPU temperature or cooling limits causing clock throttling"
		rca.Confidence = 0.82
		rca.ContributingFactors = []string{
			"Thermal throttle active or GPU temperatures exceed safe thresholds",
			"Clock speeds reduced to protect hardware",
		}
		rca.Recommendations = []string{
			"Verify GPU cooling and airflow (fans, heatsinks, chassis)",
			"Reduce batch size or clock limits to lower heat output",
			"Check for dust buildup or failed fans",
		}

	case gpuThrottlePower:
		rca.Symptom = "GPU power throttling detected"
		rca.RootCause = "GPU power limit reached, reducing performance"
		rca.Confidence = 0.78
		rca.ContributingFactors = []string{
			"Power draw at or near limit",
			"Power throttle reason active",
		}
		rca.Recommendations = []string{
			"Review GPU power cap settings",
			"Consider higher power limit if thermals allow",
			"Reduce batch size or concurrency to stay under cap",
		}

	case gpuMemHigh && gpuUtilLow && gpuProcessCount > 1:
		rca.Symptom = "High GPU memory usage with low compute utilization"
		rca.RootCause = "Possible VRAM fragmentation or over-allocation by multiple processes"
		rca.Confidence = 0.74
		rca.ContributingFactors = []string{
			"GPU memory usage above 90%",
			"Low SM utilization indicates idle compute",
			"Multiple GPU processes contending for VRAM",
		}
		rca.Recommendations = []string{
			"Consolidate workloads to reduce GPU context churn",
			"Restart long-lived processes to defragment VRAM",
			"Right-size model/batch allocations per GPU",
		}

	case gpuUtilLow && (pcieRx > 8000 || pcieTx > 8000):
		rca.Symptom = "Low GPU utilization with high PCIe traffic"
		rca.RootCause = "Host-to-device transfer bottleneck (PCIe saturation)"
		rca.Confidence = 0.70
		rca.ContributingFactors = []string{
			"High PCIe throughput with low SM utilization",
			"Data transfer dominating GPU time",
		}
		rca.Recommendations = []string{
			"Increase input pipeline batching or prefetching",
			"Pin memory and optimize data loader to reduce transfers",
			"Verify PCIe link width/gen and avoid shared slots",
		}

	case gpuUtilLow && cpuHigh:
		rca.Symptom = "Low GPU utilization with high CPU pressure"
		rca.RootCause = "CPU-side data loader or preprocessing bottleneck"
		rca.Confidence = 0.68
		rca.ContributingFactors = []string{
			"GPU underutilized while CPU saturated",
			"Likely data pipeline or preprocessing overhead",
		}
		rca.Recommendations = []string{
			"Increase data loader workers or preprocessing parallelism",
			"Move preprocessing onto GPU where possible",
			"Profile CPU hot spots in the input pipeline",
		}

	case gpuUtilHigh && gpuMemHigh && !gpuThrottleAny:
		rca.Symptom = "GPU saturated at high utilization and memory usage"
		rca.RootCause = "Batch-size or concurrency saturation"
		rca.Confidence = 0.72
		rca.ContributingFactors = []string{
			"High SM utilization and VRAM usage",
			"System likely at capacity for current workload",
		}
		rca.Recommendations = []string{
			"Scale out workload across more GPUs",
			"Reduce batch size or increase GPU memory headroom",
			"Enable model parallelism or tensor parallel settings",
		}

	case gpuUtilLow && netHigh:
		rca.Symptom = "Low GPU utilization with high network pressure"
		rca.RootCause = "Network stall or upstream dependency latency"
		rca.Confidence = 0.65
		rca.ContributingFactors = []string{
			"GPU idle while network traffic is saturated",
			"Potential upstream latency or data fetch bottleneck",
		}
		rca.Recommendations = []string{
			"Inspect network latency and packet drops",
			"Cache or colocate data with GPU nodes",
			"Validate model serving dependencies",
		}

	case gpuUtilHigh && swapPressure:
		rca.Symptom = "High GPU utilization with swap pressure"
		rca.RootCause = "Host memory pressure impacting GPU pipeline"
		rca.Confidence = 0.62
		rca.ContributingFactors = []string{
			"Swap activity detected during GPU load",
			"Host memory pressure can slow data delivery",
		}
		rca.Recommendations = []string{
			"Increase host RAM or reduce in-memory caches",
			"Pin critical buffers to avoid swapping",
			"Review system memory allocation for inference services",
		}

	case gpuUtilHigh && oomEvents:
		rca.Symptom = "GPU workload coincides with OOM events"
		rca.RootCause = "Host memory exhaustion during GPU workload"
		rca.Confidence = 0.70
		rca.ContributingFactors = []string{
			"OOM killer triggered",
			"Host memory insufficient for GPU workload",
		}
		rca.Recommendations = []string{
			"Reduce batch size or model parallelism on host",
			"Increase host memory or tune kernel OOM settings",
			"Monitor memory allocations in inference processes",
		}

	case netHigh && !cpuHigh && !memHigh:
		rca.Symptom = "High network utilization"
		rca.RootCause = "Network saturation - bandwidth limit reached"
		rca.Confidence = 0.70
		rca.ContributingFactors = []string{
			"Network interface utilization is high",
			"May indicate external traffic spike or attack",
		}
		rca.Recommendations = []string{
			"Identify top network consumers",
			"Check for DDoS or traffic anomalies",
			"Consider bandwidth upgrade or traffic shaping",
		}

	case cpuHigh && loadHigh && !memHigh && !ioHigh:
		rca.Symptom = "High CPU utilization with elevated load"
		rca.RootCause = "CPU-bound workload - insufficient compute capacity or runaway process"
		rca.Confidence = 0.75
		rca.ContributingFactors = []string{
			"High CPU usage indicates compute pressure",
			"Elevated load average confirms processing backlog",
		}
		rca.Recommendations = []string{
			"Check for runaway processes consuming excessive CPU",
			"Review application code for inefficient algorithms",
			"Consider scaling horizontally if load is legitimate",
		}

	case memHigh && !cpuHigh:
		rca.Symptom = "High memory utilization"
		rca.RootCause = "Memory pressure - possible memory leak or undersized allocation"
		rca.Confidence = 0.70
		rca.ContributingFactors = []string{
			"Memory usage exceeds safe threshold",
			"Low CPU suggests workload is memory-bound",
		}
		rca.Recommendations = []string{
			"Identify processes with high memory consumption",
			"Check for memory leaks in long-running applications",
			"Consider increasing available memory or optimizing usage",
		}

	case ioHigh:
		rca.Symptom = "High disk I/O utilization"
		rca.RootCause = "I/O bottleneck - storage subsystem saturated"
		rca.Confidence = 0.72
		rca.ContributingFactors = []string{
			"Disk utilization at capacity",
			"May cause increased latency for all operations",
		}
		rca.Recommendations = []string{
			"Identify processes causing heavy I/O",
			"Consider using faster storage (SSD/NVMe)",
			"Review and optimize database queries if applicable",
			"Implement caching to reduce disk reads",
		}

	case cpuHigh && memHigh:
		rca.Symptom = "Both CPU and memory under pressure"
		rca.RootCause = "Resource saturation - system under heavy load"
		rca.Confidence = 0.80
		rca.ContributingFactors = []string{
			"Multiple resource types saturated",
			"Indicates overall capacity issue",
		}
		rca.Recommendations = []string{
			"Immediate: Identify and limit heavy processes",
			"Short-term: Scale resources vertically or horizontally",
			"Long-term: Review capacity planning",
		}

	case len(anomalies) > 3:
		rca.Symptom = "Multiple anomalies detected"
		rca.RootCause = "Systemic instability - multiple metrics behaving abnormally"
		rca.Confidence = 0.60
		rca.ContributingFactors = []string{
			fmt.Sprintf("%d metrics showing anomalous behavior", len(anomalies)),
			"Possible cascading issue or external factor",
		}
		rca.Recommendations = []string{
			"Review recent changes or deployments",
			"Check for external factors (traffic spike, attack)",
			"Consider rolling back recent changes",
		}

	default:
		// Generic analysis
		rca.Symptom = "Detected issues require investigation"
		rca.RootCause = "Unable to determine specific root cause from available data"
		rca.Confidence = 0.40
		rca.ContributingFactors = alertIDs
		rca.Recommendations = []string{
			"Review individual alerts and metrics",
			"Correlate with application logs",
			"Check recent system changes",
		}
	}

	if e.config.EnableLLMAnalysis && e.llmClient != nil {
		anomalyTexts := make([]string, 0, len(anomalies))
		for _, anomaly := range anomalies {
			if anomaly.Reason != "" {
				anomalyTexts = append(anomalyTexts, anomaly.Reason)
			} else {
				anomalyTexts = append(anomalyTexts, anomaly.MetricName)
			}
		}
		contextText := "RCA request from controller analysis engine"
		var processes []ProcessSummary
		var logs []LogSummary
		if e.evidence != nil {
			processes, logs = e.evidence.EvidenceForNode(nodeName)
		}
		input := AnalysisInput{
			NodeName:  nodeName,
			Metrics:   currentValues,
			Trends:    trends,
			Anomalies: anomalyTexts,
			Context:   contextText,
			Schema:    buildLLMSchema(nodeName, currentValues, trends, alertIDs, anomalyTexts, contextText, processes, logs),
		}
		ctx := e.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctx, e.config.LLMTimeout)
		defer cancel()
		if llmResult, err := e.llmClient.Analyze(ctx, input); err == nil && llmResult != nil {
			rca.AnalysisMethod = "llm"
			rca.Symptom = llmResult.Summary
			rca.RootCause = llmResult.RootCause
			rca.Confidence = llmResult.Confidence
			rca.Recommendations = llmResult.Recommendations
			rca.LLMReport = llmResult.Summary
		} else if err != nil {
			e.logger.Warn("llm analysis failed", zap.Error(err))
		}
	}

	return rca
}

// Threshold represents a metric threshold
type Threshold struct {
	Title     string
	Warning   float64
	Critical  float64
	Direction string // "above" or "below"
}

// getDefaultThresholds returns default threshold configurations
func getDefaultThresholds() map[string]Threshold {
	return map[string]Threshold{
		"system.cpu.usage": {
			Title:     "CPU Usage Alert",
			Warning:   75.0,
			Critical:  90.0,
			Direction: "above",
		},
		"system.memory.usage": {
			Title:     "Memory Usage Alert",
			Warning:   80.0,
			Critical:  95.0,
			Direction: "above",
		},
		"system.disk.usage": {
			Title:     "Disk Usage Alert",
			Warning:   80.0,
			Critical:  95.0,
			Direction: "above",
		},
		"system.load.1m": {
			Title:     "Load Average Alert",
			Warning:   4.0,
			Critical:  8.0,
			Direction: "above",
		},
		"system.disk.io.utilization": {
			Title:     "Disk I/O Utilization Alert",
			Warning:   70.0,
			Critical:  90.0,
			Direction: "above",
		},
		"system.memory.available": {
			Title:     "Low Available Memory",
			Warning:   1073741824, // 1GB
			Critical:  536870912,  // 512MB
			Direction: "below",
		},
		"node_gpu_utilization_sm_avg_percent": {
			Title:     "GPU SM Utilization High",
			Warning:   85.0,
			Critical:  95.0,
			Direction: "above",
		},
		"node_gpu_memory_used_percent": {
			Title:     "GPU Memory Usage High",
			Warning:   85.0,
			Critical:  95.0,
			Direction: "above",
		},
		"node_gpu_temperature_max_celsius": {
			Title:     "GPU Temperature High",
			Warning:   80.0,
			Critical:  90.0,
			Direction: "above",
		},
		"node_gpu_throttle_active_any": {
			Title:     "GPU Throttle Active",
			Warning:   1.0,
			Critical:  1.0,
			Direction: "above",
		},
		"node_gpu_power_draw_percent": {
			Title:     "GPU Power Draw High",
			Warning:   90.0,
			Critical:  98.0,
			Direction: "above",
		},
	}
}

// Helper functions

func severityOrder(s Severity) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

func calculateStats(values []float64) (mean, stdDev float64) {
	if len(values) == 0 {
		return 0, 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(len(values))

	variance := 0.0
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))
	stdDev = math.Sqrt(variance)

	return mean, stdDev
}

func calculateTrend(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}

	n := float64(len(values))
	sumX, sumY, sumXY, sumX2 := 0.0, 0.0, 0.0, 0.0

	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	denominator := n*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0
	}

	return (n*sumXY - sumX*sumY) / denominator
}

func pearsonCorrelation(x, y []float64) float64 {
	if len(x) != len(y) || len(x) < 2 {
		return 0
	}

	n := float64(len(x))
	sumX, sumY, sumXY, sumX2, sumY2 := 0.0, 0.0, 0.0, 0.0, 0.0

	for i := range x {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumX2 += x[i] * x[i]
		sumY2 += y[i] * y[i]
	}

	numerator := n*sumXY - sumX*sumY
	denominator := math.Sqrt((n*sumX2 - sumX*sumX) * (n*sumY2 - sumY*sumY))

	if denominator == 0 {
		return 0
	}

	return numerator / denominator
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
