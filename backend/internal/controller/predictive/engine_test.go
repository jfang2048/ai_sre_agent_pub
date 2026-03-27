package predictive

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/stretchr/testify/require"
)

func TestEvaluateDetectsPredictiveMemoryRisk(t *testing.T) {
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	history := make([]ingest.MetricHistorySample, 0, 8)
	total := 100.0
	for i, used := range []float64{60, 64, 68, 72, 76, 80, 84} {
		history = append(history, ingest.MetricHistorySample{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"node_memory_Used_bytes":     used,
				"node_memory_MemTotal_bytes": total,
			},
		})
	}
	current := map[string]float64{
		"node_memory_Used_bytes":     90,
		"node_memory_MemTotal_bytes": total,
	}

	findings := Evaluate("collector-a", current, history, DefaultOptions(20*time.Minute, 85, 0.85, 0.01, 85))
	require.NotEmpty(t, findings)

	var memoryFinding *Finding
	for i := range findings {
		if findings[i].Metric == "node_memory_used_percent" {
			memoryFinding = &findings[i]
			break
		}
	}
	require.NotNil(t, memoryFinding)
	require.Equal(t, "Memory exhaustion risk rising", memoryFinding.Title)
	require.GreaterOrEqual(t, memoryFinding.Confidence, 0.45)
	require.NotEmpty(t, memoryFinding.AuditHash)
}

func TestEvaluateDetectsGPUThermalRisk(t *testing.T) {
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	history := make([]ingest.MetricHistorySample, 0, 7)
	for i, temp := range []float64{68, 69, 70, 72, 74, 77, 80} {
		history = append(history, ingest.MetricHistorySample{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"node_gpu_temperature_celsius": temp,
			},
		})
	}
	current := map[string]float64{
		"node_gpu_temperature_celsius": 84,
	}

	findings := Evaluate("gpu-node-1", current, history, DefaultOptions(15*time.Minute, 85, 0.85, 0.01, 85))
	require.NotEmpty(t, findings)
	require.Equal(t, "node_gpu_temperature_celsius", findings[0].Metric)
	require.Contains(t, findings[0].Forecast, "GPU temperature")
}

func TestSummariesDeduplicatesOutput(t *testing.T) {
	findings := []Finding{
		{Title: "Memory exhaustion risk rising", Forecast: "Memory pressure could cross 85% soon"},
		{Title: "Memory exhaustion risk rising", Forecast: "Memory pressure could cross 85% soon"},
	}
	alerts, forecasts := Summaries(findings)
	require.Equal(t, []string{"Memory exhaustion risk rising"}, alerts)
	require.Equal(t, []string{"Memory pressure could cross 85% soon"}, forecasts)
}
