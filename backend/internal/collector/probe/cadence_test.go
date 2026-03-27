package probe

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeriveCompatibilitySamplingProfile(t *testing.T) {
	profile := deriveCompatibilitySamplingProfile(5 * time.Second)
	require.True(t, profile.Enabled)
	require.Equal(t, 10*time.Second, profile.ExtendedInterval)
	require.Equal(t, 30*time.Second, profile.HardwareInterval)
	require.Equal(t, 15*time.Second, profile.DeepInterval)
	require.Equal(t, 15*time.Second, profile.KernelEventsInterval)
	require.Equal(t, 30*time.Second, profile.RCAInterval)
	require.Equal(t, 15*time.Second, profile.GPUInterval)
}

func TestCollectCompatibilityTierUsesCacheAndRefreshesOnAnomaly(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	collector := &Collector{}
	cache := &cachedMetricTier{}
	calls := 0

	collect := func() ([]Metric, error) {
		calls++
		return []Metric{{
			Name:      "node_cpu_usage_percent",
			Type:      "gauge",
			Value:     float64(calls),
			Timestamp: now,
		}}, nil
	}

	first := collector.collectCompatibilityTier(now, "extended", 10*time.Second, false, false, cache, collect)
	require.Equal(t, 1.0, metricValue(first, "collector_compat_payload_refreshed"))
	require.Equal(t, 0.0, metricValue(first, "collector_compat_payload_suppressed"))
	require.Equal(t, 1, calls)
	require.Equal(t, 1.0, metricValue(first, "node_cpu_usage_percent"))
	require.Equal(t, 0.0, metricValue(first, "collector_compat_collection_cache_hit"))

	second := collector.collectCompatibilityTier(now.Add(5*time.Second), "extended", 10*time.Second, false, false, cache, collect)
	require.Equal(t, 1, calls)
	require.Equal(t, 1.0, metricValue(second, "node_cpu_usage_percent"))
	require.Equal(t, 1.0, metricValue(second, "collector_compat_collection_cache_hit"))
	require.Equal(t, 0.0, metricValue(second, "collector_compat_payload_refreshed"))
	require.Equal(t, 0.0, metricValue(second, "collector_compat_payload_suppressed"))

	third := collector.collectCompatibilityTier(now.Add(6*time.Second), "extended", 10*time.Second, true, false, cache, collect)
	require.Equal(t, 2, calls)
	require.Equal(t, 2.0, metricValue(third, "node_cpu_usage_percent"))
	require.Equal(t, 1.0, metricValue(third, "collector_compat_collection_anomaly_triggered"))
}

func TestCollectHardwareTierUsesSlowerCacheWindow(t *testing.T) {
	now := time.Unix(1700000200, 0).UTC()
	collector := &Collector{
		compatSampling: compatibilitySamplingProfile{
			Enabled:          true,
			HardwareInterval: 30 * time.Second,
		},
	}
	calls := 0
	collector.hardwareCache = cachedMetricTier{}

	metrics := collector.collectCompatibilityTier(
		now,
		"hardware",
		collector.compatSampling.HardwareInterval,
		false,
		true,
		&collector.hardwareCache,
		func() ([]Metric, error) {
			calls++
			return []Metric{{
				Name:      "node_thermal_zone_temp_celsius",
				Type:      "gauge",
				Value:     float64(60 + calls),
				Timestamp: now,
			}}, nil
		},
	)
	require.Equal(t, 1, calls)
	require.Equal(t, 61.0, metricValue(metrics, "node_thermal_zone_temp_celsius"))

	metrics = collector.collectCompatibilityTier(
		now.Add(20*time.Second),
		"hardware",
		collector.compatSampling.HardwareInterval,
		false,
		true,
		&collector.hardwareCache,
		func() ([]Metric, error) {
			calls++
			return nil, nil
		},
	)
	require.Equal(t, 1, calls)
	require.Equal(t, 1.0, metricValue(metrics, "collector_compat_collection_cache_hit"))
	require.Equal(t, 1.0, metricValue(metrics, "collector_compat_payload_suppressed"))
	require.Equal(t, 0.0, metricValue(metrics, "node_thermal_zone_temp_celsius"))
}

func TestCompatibilityAnomalyTriggered(t *testing.T) {
	metrics := []Metric{
		{Name: "node_cpu_usage_percent", Value: 92},
		{Name: "node_memory_Used_bytes", Value: 15 * 1024 * 1024 * 1024},
		{Name: "node_memory_MemTotal_bytes", Value: 16 * 1024 * 1024 * 1024},
	}
	require.True(t, compatibilityAnomalyTriggered(metrics))

	calm := []Metric{
		{Name: "node_cpu_usage_percent", Value: 23},
		{Name: "node_memory_Used_bytes", Value: 4 * 1024 * 1024 * 1024},
		{Name: "node_memory_MemTotal_bytes", Value: 16 * 1024 * 1024 * 1024},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.004},
	}
	require.False(t, compatibilityAnomalyTriggered(calm))
}
