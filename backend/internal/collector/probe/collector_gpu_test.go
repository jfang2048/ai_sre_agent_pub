package probe

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAppendGPUStatsFromPartsMinimalQuery(t *testing.T) {
	now := time.Now()
	labels := map[string]string{"gpu_id": "0"}
	metrics := []Metric{}

	parts := []string{
		"0",  // index
		"75", // utilization.gpu
		"40", // utilization.memory
		"8192",
		"2048",
		"6144",
		"67",  // temperature.gpu
		"120", // power.draw
		"300", // power.limit
	}
	appendGPUStatsFromParts(&metrics, parts, labels, now, "0")

	values := metricValues(metrics)
	assert.Equal(t, 75.0, values["node_gpu_utilization_sm_percent"])
	assert.Equal(t, 40.0, values["node_gpu_utilization_memory_percent"])
	assert.Equal(t, 8192.0, values["node_gpu_memory_total_mib"])
	assert.Equal(t, 2048.0, values["node_gpu_memory_used_mib"])
	assert.Equal(t, 67.0, values["node_gpu_temperature_celsius"])
	assert.Equal(t, 120.0, values["node_gpu_power_draw_watts"])
	assert.Equal(t, 300.0, values["node_gpu_power_limit_watts"])
}

func TestAppendGPUStatsFromPartsFullQuery(t *testing.T) {
	now := time.Now()
	labels := map[string]string{"gpu_id": "1"}
	metrics := []Metric{}

	parts := []string{
		"1",
		"60",
		"30",
		"16384",
		"4096",
		"12288",
		"62",
		"70",  // temperature.memory
		"210", // power.draw
		"350", // power.limit
		"1500",
		"1500",
		"5000", // memory clock
		"1200",
		"45",
		"4",
		"16",
		"2000",
		"2100",
		"320", // memory bus width
	}
	appendGPUStatsFromParts(&metrics, parts, labels, now, "1")

	values := metricValues(metrics)
	assert.Equal(t, 70.0, values["node_gpu_temperature_memory_celsius"])
	assert.Equal(t, 210.0, values["node_gpu_power_draw_watts"])
	assert.Equal(t, 350.0, values["node_gpu_power_limit_watts"])
	assert.Equal(t, 2000.0, values["node_gpu_pcie_rx_mb_s"])
	assert.Equal(t, 2100.0, values["node_gpu_pcie_tx_mb_s"])
	assert.Greater(t, values["node_gpu_memory_bandwidth_theoretical_gbs"], 0.0)
}

func metricValues(metrics []Metric) map[string]float64 {
	out := make(map[string]float64, len(metrics))
	for _, m := range metrics {
		out[m.Name] = m.Value
	}
	return out
}

func TestNormalizeGPUProcessNameFallsBackToBinaryName(t *testing.T) {
	raw := strings.Repeat("/opt/very/long/path/chrome-headless-shell --type=gpu-process --arg=value ", 8)

	name := normalizeGPUProcessName(raw, "does-not-exist")

	assert.Equal(t, "chrome-headless-shell", name)
}

func TestNormalizeGPUProcessNameCapsLength(t *testing.T) {
	name := normalizeGPUProcessName(strings.Repeat("trainer", 20), "does-not-exist")

	assert.LessOrEqual(t, len([]rune(name)), 64)
}
