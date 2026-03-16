package agent

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeSignalsIncludesPredictiveFindings(t *testing.T) {
	now := time.Date(2026, 3, 12, 12, 0, 0, 0, time.UTC)
	history := make([]ingest.MetricHistorySample, 0, 7)
	for i, temp := range []float64{66, 68, 70, 72, 74, 77, 80} {
		history = append(history, ingest.MetricHistorySample{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"node_gpu_temperature_celsius": temp,
			},
		})
	}

	findings, forecasts, predictions := analyzeSignals(
		"collector-gpu-a",
		map[string]float64{"node_gpu_temperature_celsius": 84},
		history,
		DefaultConfig().Signals,
		15*time.Minute,
	)

	require.Contains(t, findings, "GPU thermal runaway risk detected")
	require.NotEmpty(t, forecasts)
	require.NotEmpty(t, predictions)
}
