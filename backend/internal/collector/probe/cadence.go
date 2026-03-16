package probe

import (
	"time"
)

const (
	defaultCompatExtendedInterval = 10 * time.Second
	defaultCompatHardwareInterval = 30 * time.Second
	defaultCompatDeepInterval     = 15 * time.Second
	defaultCompatKernelInterval   = 15 * time.Second
	defaultCompatRCAInterval      = 30 * time.Second
	defaultCompatGPUInterval      = 15 * time.Second

	compatMetricComponentLabelKey = "component"
	compatMetricPayloadRefreshed  = "collector_compat_payload_refreshed"
	compatMetricPayloadSuppressed = "collector_compat_payload_suppressed"
)

type compatibilitySamplingProfile struct {
	Enabled              bool
	ExtendedInterval     time.Duration
	HardwareInterval     time.Duration
	DeepInterval         time.Duration
	KernelEventsInterval time.Duration
	RCAInterval          time.Duration
	GPUInterval          time.Duration
}

type cachedMetricTier struct {
	lastCollected time.Time
	lastValue     []Metric
}

func WithAdaptiveSampling(baseInterval time.Duration) CollectorOption {
	return func(c *Collector) {
		c.compatSampling = deriveCompatibilitySamplingProfile(baseInterval)
	}
}

func deriveCompatibilitySamplingProfile(baseInterval time.Duration) compatibilitySamplingProfile {
	if baseInterval <= 0 {
		baseInterval = 5 * time.Second
	}
	return compatibilitySamplingProfile{
		Enabled:              true,
		ExtendedInterval:     maxDuration(2*baseInterval, defaultCompatExtendedInterval),
		HardwareInterval:     maxDuration(6*baseInterval, defaultCompatHardwareInterval),
		DeepInterval:         maxDuration(3*baseInterval, defaultCompatDeepInterval),
		KernelEventsInterval: maxDuration(3*baseInterval, defaultCompatKernelInterval),
		RCAInterval:          maxDuration(6*baseInterval, defaultCompatRCAInterval),
		GPUInterval:          maxDuration(3*baseInterval, defaultCompatGPUInterval),
	}
}

func shouldRefreshCompatibilityTier(now, last time.Time, interval time.Duration, anomalyTriggered bool, cached []Metric) bool {
	if anomalyTriggered {
		return true
	}
	if interval <= 0 || last.IsZero() || len(cached) == 0 {
		return true
	}
	return now.Sub(last) >= interval
}

func compatibilityElapsed(now, last time.Time, fallback float64) float64 {
	if !last.IsZero() {
		if elapsed := now.Sub(last).Seconds(); elapsed > 0 {
			return elapsed
		}
	}
	if fallback > 0 {
		return fallback
	}
	return 1
}

func cloneProbeMetrics(in []Metric, now time.Time) []Metric {
	if len(in) == 0 {
		return nil
	}
	out := make([]Metric, 0, len(in))
	for _, metric := range in {
		copied := metric
		copied.Timestamp = now
		if len(metric.Labels) > 0 {
			copied.Labels = cloneMetricLabels(metric.Labels)
		}
		out = append(out, copied)
	}
	return out
}

func cloneMetricLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func compatibilityCadenceMetrics(now time.Time, component string, interval time.Duration, cacheHit bool, lastCollected time.Time, anomalyTriggered bool) []Metric {
	labels := map[string]string{compatMetricComponentLabelKey: component}
	ageSeconds := 0.0
	if !lastCollected.IsZero() {
		ageSeconds = now.Sub(lastCollected).Seconds()
		if ageSeconds < 0 {
			ageSeconds = 0
		}
	}
	return []Metric{
		{
			Name:      "collector_compat_collection_interval_seconds",
			Type:      "gauge",
			Value:     interval.Seconds(),
			Labels:    cloneMetricLabels(labels),
			Timestamp: now,
		},
		{
			Name:      "collector_compat_collection_age_seconds",
			Type:      "gauge",
			Value:     ageSeconds,
			Labels:    cloneMetricLabels(labels),
			Timestamp: now,
		},
		{
			Name:      "collector_compat_collection_cache_hit",
			Type:      "gauge",
			Value:     boolToFloat(cacheHit),
			Labels:    cloneMetricLabels(labels),
			Timestamp: now,
		},
		{
			Name:      "collector_compat_collection_anomaly_triggered",
			Type:      "gauge",
			Value:     boolToFloat(anomalyTriggered),
			Labels:    labels,
			Timestamp: now,
		},
	}
}

func compatibilityPayloadStateMetrics(now time.Time, component string, refreshed bool, suppressed bool) []Metric {
	labels := map[string]string{compatMetricComponentLabelKey: component}
	return []Metric{
		{
			Name:      compatMetricPayloadRefreshed,
			Type:      "gauge",
			Value:     boolToFloat(refreshed),
			Labels:    cloneMetricLabels(labels),
			Timestamp: now,
		},
		{
			Name:      compatMetricPayloadSuppressed,
			Type:      "gauge",
			Value:     boolToFloat(suppressed),
			Labels:    cloneMetricLabels(labels),
			Timestamp: now,
		},
	}
}

func maxDuration(values ...time.Duration) time.Duration {
	best := time.Duration(0)
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func compatibilityAnomalyTriggered(metrics []Metric) bool {
	cpuUsage := metricValue(metrics, "node_cpu_usage_percent")
	cpuIOWait := metricValue(metrics, "node_cpu_iowait_percent")
	memoryUsed := metricValue(metrics, "node_memory_Used_bytes")
	memoryTotal := metricValue(metrics, "node_memory_MemTotal_bytes")
	memoryRatio := 0.0
	if memoryTotal > 0 && memoryUsed > 0 {
		memoryRatio = memoryUsed / memoryTotal
	}
	diskLatency := metricValue(metrics, "node_disk_request_latency_p99_seconds", "node_disk_avg_request_latency_seconds")
	diskUtil := metricValue(metrics, "node_disk_utilization_peak_percent")
	retransmits := metricValue(metrics, "node_tcp_retransmits_per_second")

	return cpuUsage >= 85 ||
		cpuIOWait >= 10 ||
		memoryRatio >= 0.85 ||
		diskLatency >= 0.03 ||
		diskUtil >= 70 ||
		retransmits >= 0.5
}

func metricValue(metrics []Metric, names ...string) float64 {
	for _, name := range names {
		for _, metric := range metrics {
			if metric.Name == name {
				return metric.Value
			}
		}
	}
	return 0
}

func (c *Collector) collectExtendedTier(now time.Time, fallbackElapsed float64, anomalyTriggered bool) []Metric {
	return c.collectCompatibilityTier(
		now,
		"extended",
		c.compatSampling.ExtendedInterval,
		anomalyTriggered,
		false,
		&c.extendedCache,
		func() ([]Metric, error) {
			return c.collectExtended(now, compatibilityElapsed(now, c.extendedCache.lastCollected, fallbackElapsed)), nil
		},
	)
}

func (c *Collector) collectHardwareTier(now time.Time, elapsed float64, anomalyTriggered bool) []Metric {
	return c.collectCompatibilityTier(
		now,
		"hardware",
		c.compatSampling.HardwareInterval,
		anomalyTriggered,
		c.suppressCachedHardwarePayloads,
		&c.hardwareCache,
		func() ([]Metric, error) {
			return c.collectHardwareSignals(now, elapsed), nil
		},
	)
}

func (c *Collector) collectDeepTier(now time.Time, anomalyTriggered bool) []Metric {
	return c.collectCompatibilityTier(
		now,
		"deep",
		c.compatSampling.DeepInterval,
		anomalyTriggered,
		false,
		&c.deepCache,
		func() ([]Metric, error) {
			return c.collectDeep(now, c.topNProcesses), nil
		},
	)
}

func (c *Collector) collectKernelEventsTier(now time.Time, anomalyTriggered bool) []Metric {
	return c.collectCompatibilityTier(
		now,
		"kernel_events",
		c.compatSampling.KernelEventsInterval,
		anomalyTriggered,
		false,
		&c.kernelCache,
		func() ([]Metric, error) {
			return c.collectKernelEvents(now)
		},
	)
}

func (c *Collector) collectRCATier(now time.Time, anomalyTriggered bool) []Metric {
	if c == nil || c.rcaCollector == nil {
		return nil
	}
	return c.collectCompatibilityTier(
		now,
		"rca",
		c.compatSampling.RCAInterval,
		anomalyTriggered,
		false,
		&c.rcaCache,
		func() ([]Metric, error) {
			return c.rcaCollector.Collect(now)
		},
	)
}

func (c *Collector) collectGPUTier(now time.Time, anomalyTriggered bool) []Metric {
	if c == nil || c.gpuCollector == nil {
		return nil
	}
	return c.collectCompatibilityTier(
		now,
		"gpu",
		c.compatSampling.GPUInterval,
		anomalyTriggered,
		false,
		&c.gpuCache,
		func() ([]Metric, error) {
			return c.gpuCollector.Collect(now)
		},
	)
}

func (c *Collector) collectCompatibilityTier(
	now time.Time,
	component string,
	interval time.Duration,
	anomalyTriggered bool,
	suppressCachedPayload bool,
	cache *cachedMetricTier,
	collect func() ([]Metric, error),
) []Metric {
	if c == nil || cache == nil || collect == nil {
		return nil
	}
	if shouldRefreshCompatibilityTier(now, cache.lastCollected, interval, anomalyTriggered, cache.lastValue) {
		fresh, err := collect()
		if err == nil {
			cache.lastCollected = now
			cache.lastValue = cloneProbeMetrics(fresh, now)
			metrics := cloneProbeMetrics(fresh, now)
			metrics = append(metrics, compatibilityCadenceMetrics(now, component, interval, false, cache.lastCollected, anomalyTriggered)...)
			return append(metrics, compatibilityPayloadStateMetrics(now, component, true, false)...)
		}
		if len(cache.lastValue) == 0 {
			metrics := compatibilityCadenceMetrics(now, component, interval, false, cache.lastCollected, anomalyTriggered)
			return append(metrics, compatibilityPayloadStateMetrics(now, component, false, false)...)
		}
	}
	metrics := compatibilityCadenceMetrics(now, component, interval, true, cache.lastCollected, anomalyTriggered)
	metrics = append(metrics, compatibilityPayloadStateMetrics(now, component, false, suppressCachedPayload)...)
	if suppressCachedPayload {
		return metrics
	}
	return append(cloneProbeMetrics(cache.lastValue, now), metrics...)
}
