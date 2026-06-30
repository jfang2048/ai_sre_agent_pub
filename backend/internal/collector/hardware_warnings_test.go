package collector

import (
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestDetectHardwareWarnings(t *testing.T) {
	profile := defaultHardwareProfile(time.Now())
	profile.Threshold = deriveHardwareThresholdProfile(profile)

	warnings := detectHardwareWarnings([]*telemetryv1.Metric{
		{Name: "probe_core_cgroup_cpu_throttled_ratio", Value: 0.21},
		{Name: "node_disk_request_latency_p99_seconds", Value: profile.Threshold.DiskLatencySeconds * 2},
		{Name: "node_tcp_retransmit_ratio", Value: profile.Threshold.NetworkRetransmitRatio * 2},
	}, profile)

	require.Len(t, warnings, 3)
	require.Equal(t, "cpu", warnings[0].domain)
	require.Equal(t, "throttled", warnings[0].reason)
	require.Equal(t, "disk", warnings[1].domain)
	require.Equal(t, "latency", warnings[1].reason)
	require.Equal(t, "network", warnings[2].domain)
	require.Equal(t, "retransmit", warnings[2].reason)
}

func TestAppendHardwareWarningMetricsAddsSummaryAndLabels(t *testing.T) {
	now := time.Unix(1700000300, 0).UTC()
	profile := defaultHardwareProfile(now)
	profile.Threshold = deriveHardwareThresholdProfile(profile)
	base := []*telemetryv1.Metric{
		{Name: "node_pressure_memory_some_avg10", Value: 15},
	}

	appendHardwareWarningMetrics(now, &base, base, profile)
	require.Equal(t, 1.0, metricValueAny(base, "collector_hardware_warning_total"))

	found := false
	for _, metric := range base {
		if metric == nil || metric.Name != collectorHardwareWarningMetric {
			continue
		}
		if collectorMetricLabelValue(metric, "domain") == "memory" && collectorMetricLabelValue(metric, "reason") == "pressure" {
			found = true
			break
		}
	}
	require.True(t, found)
}

func collectorMetricLabelValue(metric *telemetryv1.Metric, key string) string {
	for _, label := range metric.GetLabels() {
		if label != nil && label.GetKey() == key {
			return label.GetValue()
		}
	}
	return ""
}
