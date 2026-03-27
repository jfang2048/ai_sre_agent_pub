package predictive

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

func BenchmarkEvaluate(b *testing.B) {
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	history := make([]ingest.MetricHistorySample, 0, 120)
	for i := 0; i < 120; i++ {
		history = append(history, ingest.MetricHistorySample{
			Timestamp: now.Add(time.Duration(i) * 30 * time.Second),
			Metrics: map[string]float64{
				"node_cpu_usage_percent":                 55 + float64(i%7),
				"node_memory_Used_bytes":                 60 + float64(i)*0.2,
				"node_memory_MemTotal_bytes":             100,
				"node_tcp_retransmit_ratio":              0.002 + float64(i%5)*0.0002,
				"node_gpu_temperature_celsius":           65 + float64(i%10),
				"node_gpu_power_draw_watts":              220 + float64(i%8),
				"node_gpu_power_limit_watts":             300,
				"node_gpu_pcie_link_utilization_percent": 35 + float64(i%12),
				"node_pressure_io_some_avg10":            8 + float64(i%4),
			},
		})
	}
	current := map[string]float64{
		"node_cpu_usage_percent":                 88,
		"node_memory_Used_bytes":                 92,
		"node_memory_MemTotal_bytes":             100,
		"node_tcp_retransmit_ratio":              0.015,
		"node_gpu_temperature_celsius":           84,
		"node_gpu_power_draw_watts":              286,
		"node_gpu_power_limit_watts":             300,
		"node_gpu_pcie_link_utilization_percent": 86,
		"node_pressure_io_some_avg10":            22,
	}
	opts := DefaultOptions(20*time.Minute, 85, 0.85, 0.01, 85)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Evaluate("collector-a", current, history, opts)
	}
}
