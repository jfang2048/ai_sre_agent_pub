package collector

import (
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestSuppressUnchangedLowChurnMetricsDropsStableCollectorStateBetweenRefreshes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LowChurnMetricsRefreshInterval = 2 * time.Minute
	c := &Collector{cfg: cfg}

	base := []*telemetryv1.Metric{
		{
			Name:              "collector_probe_source",
			Value:             1,
			TimestampUnixNano: time.Now().UnixNano(),
			Labels:            buildLabels(map[string]string{"source": "probe_core"}),
		},
		{
			Name:              "collector_self_cpu_percent",
			Value:             1.5,
			TimestampUnixNano: time.Now().UnixNano(),
		},
	}

	first := flattenMetrics(c.suppressUnchangedLowChurnMetrics(time.Unix(0, 0), base))
	require.Contains(t, first, "collector_probe_source")
	require.Equal(t, 0.0, first[collectorMetricsPartialUpdateMetric])
	require.Equal(t, 0.0, first[collectorMetricsSuppressedCount])

	second := flattenMetrics(c.suppressUnchangedLowChurnMetrics(time.Unix(0, 0).Add(30*time.Second), base))
	require.NotContains(t, second, "collector_probe_source")
	require.Contains(t, second, "collector_self_cpu_percent")
	require.Equal(t, 1.0, second[collectorMetricsPartialUpdateMetric])
	require.Equal(t, 1.0, second[collectorMetricsSuppressedCount])

	third := flattenMetrics(c.suppressUnchangedLowChurnMetrics(time.Unix(0, 0).Add(3*time.Minute), base))
	require.Contains(t, third, "collector_probe_source")
	require.Equal(t, 0.0, third[collectorMetricsPartialUpdateMetric])
	require.Equal(t, 0.0, third[collectorMetricsSuppressedCount])
}

func TestSuppressUnchangedLowChurnMetricsEmitsChangedValuesImmediately(t *testing.T) {
	cfg := DefaultConfig()
	c := &Collector{cfg: cfg}

	metric := func(source string) []*telemetryv1.Metric {
		return []*telemetryv1.Metric{{
			Name:              "collector_probe_source",
			Value:             1,
			TimestampUnixNano: time.Now().UnixNano(),
			Labels:            buildLabels(map[string]string{"source": source}),
		}}
	}

	first := flattenMetrics(c.suppressUnchangedLowChurnMetrics(time.Unix(0, 0), metric("probe_core")))
	require.Contains(t, first, "collector_probe_source")

	second := flattenMetrics(c.suppressUnchangedLowChurnMetrics(time.Unix(0, 0).Add(10*time.Second), metric("go")))
	require.Contains(t, second, "collector_probe_source")
	require.Equal(t, 0.0, second[collectorMetricsSuppressedCount])
}
