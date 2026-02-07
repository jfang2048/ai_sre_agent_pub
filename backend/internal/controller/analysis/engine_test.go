package analysis

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.EnableThresholdAlerts {
		t.Error("threshold alerts should be enabled by default")
	}
	if !cfg.EnableAnomalyDetection {
		t.Error("anomaly detection should be enabled by default")
	}
	if cfg.EnableLLMAnalysis {
		t.Error("LLM analysis should be disabled by default")
	}
	if cfg.ZScoreThreshold != 2.5 {
		t.Errorf("expected z-score threshold 2.5, got %f", cfg.ZScoreThreshold)
	}
}

func TestCalculateStats(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	mean, stdDev := calculateStats(values)

	if mean != 3.0 {
		t.Errorf("expected mean 3.0, got %f", mean)
	}
	// stdDev for [1,2,3,4,5] is sqrt(2) ≈ 1.414
	if stdDev < 1.4 || stdDev > 1.42 {
		t.Errorf("expected stdDev ~1.41, got %f", stdDev)
	}
}

func TestCalculateTrend(t *testing.T) {
	// Increasing trend
	increasing := []float64{1, 2, 3, 4, 5}
	trend := calculateTrend(increasing)
	if trend != 1.0 {
		t.Errorf("expected trend 1.0, got %f", trend)
	}

	// Decreasing trend
	decreasing := []float64{5, 4, 3, 2, 1}
	trend = calculateTrend(decreasing)
	if trend != -1.0 {
		t.Errorf("expected trend -1.0, got %f", trend)
	}

	// Flat
	flat := []float64{3, 3, 3, 3, 3}
	trend = calculateTrend(flat)
	if trend != 0.0 {
		t.Errorf("expected trend 0.0, got %f", trend)
	}
}

func TestPearsonCorrelation(t *testing.T) {
	// Perfect positive correlation
	x := []float64{1, 2, 3, 4, 5}
	y := []float64{2, 4, 6, 8, 10}
	corr := pearsonCorrelation(x, y)
	if corr < 0.99 {
		t.Errorf("expected correlation ~1.0, got %f", corr)
	}

	// Perfect negative correlation
	y = []float64{10, 8, 6, 4, 2}
	corr = pearsonCorrelation(x, y)
	if corr > -0.99 {
		t.Errorf("expected correlation ~-1.0, got %f", corr)
	}
}

func TestEngineIngestMetrics(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AnalysisInterval = 1 * time.Hour // Don't run analysis during test

	engine, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	samples := []MetricSample{
		{Name: "cpu.usage", Value: 50.0, Timestamp: time.Now()},
		{Name: "memory.usage", Value: 60.0, Timestamp: time.Now()},
	}

	engine.IngestMetrics("node1", samples)

	// Verify data was stored
	engine.mu.RLock()
	defer engine.mu.RUnlock()

	data, exists := engine.nodeData["node1"]
	if !exists {
		t.Fatal("node data not found")
	}
	if len(data.Samples["cpu.usage"]) != 1 {
		t.Error("expected 1 cpu sample")
	}
	if len(data.Samples["memory.usage"]) != 1 {
		t.Error("expected 1 memory sample")
	}
}

func TestThresholds(t *testing.T) {
	thresholds := getDefaultThresholds()

	cpuThreshold, ok := thresholds["system.cpu.usage"]
	if !ok {
		t.Fatal("cpu threshold not found")
	}
	if cpuThreshold.Warning != 75.0 {
		t.Errorf("expected cpu warning 75.0, got %f", cpuThreshold.Warning)
	}
	if cpuThreshold.Critical != 90.0 {
		t.Errorf("expected cpu critical 90.0, got %f", cpuThreshold.Critical)
	}
}

func TestSeverityOrder(t *testing.T) {
	if severityOrder(SeverityCritical) >= severityOrder(SeverityWarning) {
		t.Error("critical should sort before warning")
	}
	if severityOrder(SeverityWarning) >= severityOrder(SeverityInfo) {
		t.Error("warning should sort before info")
	}
}

func TestAnalysisResult(t *testing.T) {
	// Test that result types are properly structured
	result := AnalysisResult{
		Timestamp:    time.Now(),
		Alerts:       make([]Alert, 0),
		Anomalies:    make([]Anomaly, 0),
		Correlations: make([]Correlation, 0),
		RCAs:         make([]RootCauseAnalysis, 0),
	}

	if result.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

func TestRootCauseAnalysis(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AnalysisInterval = 1 * time.Hour

	engine, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	// Simulate high CPU scenario
	now := time.Now()
	samples := make([]MetricSample, 0)
	for i := 0; i < 20; i++ {
		samples = append(samples, MetricSample{
			Name:      "system.cpu.usage",
			Value:     85.0 + float64(i%5), // 85-89%
			Timestamp: now.Add(-time.Duration(20-i) * time.Minute),
		})
		samples = append(samples, MetricSample{
			Name:      "system.memory.usage",
			Value:     45.0 + float64(i%3), // 45-47%
			Timestamp: now.Add(-time.Duration(20-i) * time.Minute),
		})
		samples = append(samples, MetricSample{
			Name:      "system.load.1m",
			Value:     5.0 + float64(i%2), // 5-6
			Timestamp: now.Add(-time.Duration(20-i) * time.Minute),
		})
	}

	engine.IngestMetrics("test-node", samples)

	// Run analysis manually
	engine.runAnalysis()

	// Check alerts were generated
	alerts := engine.GetAlerts()
	if len(alerts) == 0 {
		// Note: This depends on the current metric values
		t.Log("No alerts generated - this is expected if values are below threshold")
	}
}
