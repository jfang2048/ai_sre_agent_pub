package analysis

import (
	"testing"
	"time"
)

func TestBuildStructuredRCA_CPUPressure(t *testing.T) {
	metrics := map[string]float64{
		"system.cpu.usage":    92.0,
		"system.load.1m":      8.5,
		"system.memory.usage": 45.0,
	}
	alerts := []*Alert{
		{
			ID:          "alert-1",
			MetricName:  "system.cpu.usage",
			NodeName:    "node-1",
			Severity:    SeverityCritical,
			Description: "CPU usage above 90%",
			CreatedAt:   time.Now().Add(-2 * time.Minute),
		},
	}
	anomalies := []Anomaly{
		{
			MetricName:  "system.cpu.usage",
			NodeName:    "node-1",
			CurrentVal:  92.0,
			ExpectedVal: 55.0,
			Score:       3.5,
			Direction:   "up",
			Reason:      "CPU usage significantly above baseline",
			DetectedAt:  time.Now().Add(-1 * time.Minute),
		},
	}

	rca := BuildStructuredRCA("node-1", alerts, anomalies, nil, metrics, nil, nil, nil, nil)

	if rca == nil {
		t.Fatal("expected non-nil structured RCA")
	}
	if rca.NodeName != "node-1" {
		t.Errorf("expected node-1, got %s", rca.NodeName)
	}
	if len(rca.Hypotheses) == 0 {
		t.Fatal("expected at least one hypothesis")
	}

	// The top hypothesis should be CPU-related
	topH := rca.Hypotheses[0]
	if topH.Category != "cpu" {
		t.Errorf("expected top hypothesis category 'cpu', got '%s'", topH.Category)
	}
	if topH.Confidence < 0.5 {
		t.Errorf("expected CPU hypothesis confidence > 0.5, got %.2f", topH.Confidence)
	}
	if len(topH.SupportingEvidence) == 0 {
		t.Error("expected supporting evidence for CPU hypothesis")
	}

	// Check ranking
	for i := 1; i < len(rca.Hypotheses); i++ {
		if rca.Hypotheses[i].Rank != i+1 {
			t.Errorf("hypothesis %d has wrong rank %d", i, rca.Hypotheses[i].Rank)
		}
		if rca.Hypotheses[i].Confidence > rca.Hypotheses[i-1].Confidence {
			t.Errorf("hypothesis %d has higher confidence than %d (not sorted)", i, i-1)
		}
	}

	// ImpactScope
	if rca.ImpactScope.AffectedDomains["cpu"] != "critical" {
		t.Errorf("expected cpu domain 'critical', got '%s'", rca.ImpactScope.AffectedDomains["cpu"])
	}

	// Timeline should contain the alert and anomaly events
	if len(rca.Timeline) == 0 {
		t.Error("expected timeline events")
	}

	// Summary should mention CPU
	if rca.Summary == "" {
		t.Error("expected non-empty summary")
	}

	// Related alert IDs
	if len(rca.RelatedAlertIDs) == 0 {
		t.Error("expected related alert IDs")
	}
}

func TestBuildStructuredRCA_MemoryPressure(t *testing.T) {
	metrics := map[string]float64{
		"system.memory.usage":        92.0,
		"node_memory_Used_bytes":     15000000000,
		"node_memory_MemTotal_bytes": 16000000000,
		"node_vmstat_pswpout":        50.0,
		"node_vmstat_oom_kill":       2.0,
	}

	rca := BuildStructuredRCA("node-mem", nil, nil, nil, metrics, nil, nil, nil, nil)

	if rca == nil {
		t.Fatal("expected non-nil structured RCA")
	}

	foundMemory := false
	for _, h := range rca.Hypotheses {
		if h.Category == "memory" {
			foundMemory = true
			if h.Confidence < 0.7 {
				t.Errorf("expected memory hypothesis confidence > 0.7 (has swap+oom), got %.2f", h.Confidence)
			}
			break
		}
	}
	if !foundMemory {
		t.Error("expected a memory hypothesis")
	}

	if rca.ImpactScope.AffectedDomains["memory"] != "critical" {
		t.Errorf("expected memory domain 'critical', got '%s'", rca.ImpactScope.AffectedDomains["memory"])
	}
}

func TestBuildStructuredRCA_MultiResourceSaturation(t *testing.T) {
	metrics := map[string]float64{
		"system.cpu.usage":           88.0,
		"system.memory.usage":        90.0,
		"system.disk.io.utilization": 85.0,
		"system.load.1m":             12.0,
	}

	rca := BuildStructuredRCA("node-multi", nil, nil, nil, metrics, nil, nil, nil, nil)

	if rca == nil {
		t.Fatal("expected non-nil structured RCA")
	}

	foundSystemic := false
	for _, h := range rca.Hypotheses {
		if h.Category == "systemic" {
			foundSystemic = true
			if h.Confidence < 0.7 {
				t.Errorf("expected systemic hypothesis confidence > 0.7, got %.2f", h.Confidence)
			}
			break
		}
	}
	if !foundSystemic {
		t.Error("expected a systemic (multi-resource) hypothesis")
	}
}

func TestBuildStructuredRCA_GPUThrottle(t *testing.T) {
	metrics := map[string]float64{
		"node_gpu_temperature_max_celsius":    92.0,
		"node_gpu_throttle_thermal_any":       1.0,
		"node_gpu_utilization_sm_avg_percent": 60.0,
		"node_gpu_memory_used_percent":        40.0,
	}

	rca := BuildStructuredRCA("node-gpu", nil, nil, nil, metrics, nil, nil, nil, nil)

	if rca == nil {
		t.Fatal("expected non-nil structured RCA")
	}

	foundGPU := false
	for _, h := range rca.Hypotheses {
		if h.Category == "gpu" {
			foundGPU = true
			if h.Confidence < 0.8 {
				t.Errorf("expected GPU thermal throttle confidence > 0.8, got %.2f", h.Confidence)
			}
			break
		}
	}
	if !foundGPU {
		t.Error("expected a GPU throttle hypothesis")
	}
}

func TestBuildStructuredRCA_NoIssues(t *testing.T) {
	metrics := map[string]float64{
		"system.cpu.usage":    25.0,
		"system.memory.usage": 40.0,
		"system.load.1m":      1.5,
	}

	rca := BuildStructuredRCA("node-healthy", nil, nil, nil, metrics, nil, nil, nil, nil)

	if rca == nil {
		t.Fatal("expected non-nil structured RCA")
	}
	if len(rca.Hypotheses) != 0 {
		t.Errorf("expected 0 hypotheses for healthy node, got %d", len(rca.Hypotheses))
	}
	if rca.ImpactScope.AffectedDomains["cpu"] != "normal" {
		t.Errorf("expected cpu domain 'normal', got '%s'", rca.ImpactScope.AffectedDomains["cpu"])
	}
}

func TestBuildStructuredRCA_ContradictoryEvidence(t *testing.T) {
	// CPU high + IO high should yield CPU hypothesis with IO as contradictory evidence
	metrics := map[string]float64{
		"system.cpu.usage":           90.0,
		"system.disk.io.utilization": 85.0,
		"system.load.1m":             6.0,
	}

	rca := BuildStructuredRCA("node-contra", nil, nil, nil, metrics, nil, nil, nil, nil)

	if rca == nil {
		t.Fatal("expected non-nil structured RCA")
	}

	for _, h := range rca.Hypotheses {
		if h.Category == "cpu" {
			if len(h.ContradictoryEvidence) == 0 {
				t.Error("expected contradictory evidence (IO) for CPU hypothesis when IO is also high")
			}
			return
		}
	}
	t.Error("expected CPU hypothesis")
}

func TestBuildStructuredRCA_WithSourceRCA(t *testing.T) {
	sourceRCA := &RootCauseAnalysis{
		ID:             "rca-src-1",
		AnalysisMethod: "llm",
	}
	metrics := map[string]float64{
		"system.cpu.usage": 95.0,
		"system.load.1m":   10.0,
	}

	rca := BuildStructuredRCA("node-linked", nil, nil, nil, metrics, nil, nil, nil, sourceRCA)

	if rca == nil {
		t.Fatal("expected non-nil structured RCA")
	}
	if rca.SourceRCAID != "rca-src-1" {
		t.Errorf("expected source RCA ID 'rca-src-1', got '%s'", rca.SourceRCAID)
	}
	if rca.AnalysisMethod != "llm" {
		t.Errorf("expected analysis method 'llm', got '%s'", rca.AnalysisMethod)
	}
}

func TestClampConfidence(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{0.0, 0.1},
		{0.05, 0.1},
		{0.5, 0.5},
		{0.99, 0.98},
		{1.5, 0.98},
		{-0.5, 0.1},
	}
	for _, tt := range tests {
		got := clampConfidence(tt.input)
		if got != tt.want {
			t.Errorf("clampConfidence(%f) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestMaxOf(t *testing.T) {
	tests := []struct {
		input []float64
		want  float64
	}{
		{[]float64{1, 2, 3}, 3},
		{[]float64{5, 2, 1}, 5},
		{[]float64{0}, 0},
		{nil, 0},
	}
	for _, tt := range tests {
		got := maxOf(tt.input...)
		if got != tt.want {
			t.Errorf("maxOf(%v) = %f, want %f", tt.input, got, tt.want)
		}
	}
}
