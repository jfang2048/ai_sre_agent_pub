package collector

import (
	"context"
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

const (
	defaultLogCollectionCadence   = 15 * time.Second
	defaultExternalMetricsCadence = 30 * time.Second
	auxMetricComponentLabelKey    = "component"
	auxMetricProcessFallback      = "process_fallback"
	auxMetricLogs                 = "logs"
	auxMetricExternal             = "external"
	auxMetricPayloadRefreshed     = "collector_aux_payload_refreshed"
	auxMetricPayloadSuppressed    = "collector_aux_payload_suppressed"
)

type auxiliaryCollectionState struct {
	processFallback cachedProcessSamples
	logs            cachedLogFingerprints
	external        cachedTelemetryMetrics
}

type cachedProcessSamples struct {
	lastCollected time.Time
	lastValue     []*telemetryv1.ProcessSample
}

type cachedLogFingerprints struct {
	lastCollected time.Time
	lastValue     []*telemetryv1.LogFingerprint
}

type cachedTelemetryMetrics struct {
	lastCollected time.Time
	lastValue     []*telemetryv1.Metric
}

func (c *Collector) collectProcessFallbackWithCadence(now time.Time, cfg Config, decision protectionDecision, probeData sourceCollection) ([]*telemetryv1.ProcessSample, []*telemetryv1.Metric) {
	processes := probeData.processes
	if len(processes) > 0 || !probeData.compatibilityFallback || c.compatProcTopK == nil {
		return processes, nil
	}

	interval := effectiveAuxiliaryInterval(derivedProcessFallbackInterval(cfg), c.effectiveCollectionInterval(cfg), decision)
	if decision.SkipProcessFallback {
		return nil, append(
			auxiliaryCadenceMetrics(now, auxMetricProcessFallback, interval, false, c.auxState.processFallback.lastCollected),
			auxiliaryPayloadStateMetrics(now, auxMetricProcessFallback, false, false)...,
		)
	}
	if cached, cacheHit := c.auxState.processFallback.get(now, interval); cacheHit {
		metrics := auxiliaryCadenceMetrics(now, auxMetricProcessFallback, interval, true, c.auxState.processFallback.lastCollected)
		suppressed := cfg.SuppressCachedAuxPayloads
		metrics = append(metrics, auxiliaryPayloadStateMetrics(now, auxMetricProcessFallback, false, suppressed)...)
		if suppressed {
			return nil, metrics
		}
		return cached, metrics
	}

	processes = cloneProcessSamples(c.compatProcTopK.Collect(now))
	c.auxState.processFallback.set(now, processes)
	return cloneProcessSamples(processes), append(
		auxiliaryCadenceMetrics(now, auxMetricProcessFallback, interval, false, c.auxState.processFallback.lastCollected),
		auxiliaryPayloadStateMetrics(now, auxMetricProcessFallback, true, false)...,
	)
}

func (c *Collector) collectLogsWithCadence(now time.Time, cfg Config, decision protectionDecision) ([]*telemetryv1.LogFingerprint, []*telemetryv1.Metric) {
	if c.logTail == nil {
		return nil, nil
	}
	interval := effectiveAuxiliaryInterval(derivedLogCollectionInterval(cfg), c.effectiveCollectionInterval(cfg), decision)
	if decision.DisableLogs {
		return nil, append(
			auxiliaryCadenceMetrics(now, auxMetricLogs, interval, false, c.auxState.logs.lastCollected),
			auxiliaryPayloadStateMetrics(now, auxMetricLogs, false, false)...,
		)
	}
	if cached, cacheHit := c.auxState.logs.get(now, interval); cacheHit {
		metrics := auxiliaryCadenceMetrics(now, auxMetricLogs, interval, true, c.auxState.logs.lastCollected)
		suppressed := cfg.SuppressCachedAuxPayloads
		metrics = append(metrics, auxiliaryPayloadStateMetrics(now, auxMetricLogs, false, suppressed)...)
		if suppressed {
			return nil, metrics
		}
		return cached, metrics
	}

	logs := cloneLogFingerprints(c.logTail.Collect(now))
	c.auxState.logs.set(now, logs)
	return cloneLogFingerprints(logs), append(
		auxiliaryCadenceMetrics(now, auxMetricLogs, interval, false, c.auxState.logs.lastCollected),
		auxiliaryPayloadStateMetrics(now, auxMetricLogs, true, false)...,
	)
}

func (c *Collector) collectExternalMetricsWithCadence(ctx context.Context, now time.Time, cfg Config, decision protectionDecision) []*telemetryv1.Metric {
	if strings.TrimSpace(cfg.ExternalMetricsCmd) == "" {
		return nil
	}
	interval := effectiveAuxiliaryInterval(derivedExternalMetricsInterval(cfg), c.effectiveCollectionInterval(cfg), decision)
	if decision.DisableExternal {
		return auxiliaryCadenceMetrics(now, auxMetricExternal, interval, false, c.auxState.external.lastCollected)
	}
	if cached, cacheHit := c.auxState.external.get(now, interval); cacheHit {
		return append(cloneTelemetryMetrics(cached), auxiliaryCadenceMetrics(now, auxMetricExternal, interval, true, c.auxState.external.lastCollected)...)
	}

	metrics := c.runExternalMetrics(ctx)
	c.auxState.external.set(now, metrics)
	return append(cloneTelemetryMetrics(metrics), auxiliaryCadenceMetrics(now, auxMetricExternal, interval, false, c.auxState.external.lastCollected)...)
}

func (c *Collector) effectiveCollectionInterval(cfg Config) time.Duration {
	if c == nil {
		return cfg.CollectionInterval
	}
	if interval := c.intervalSnapshot(); interval > 0 {
		return interval
	}
	return cfg.CollectionInterval
}

func derivedProcessFallbackInterval(cfg Config) time.Duration {
	base := cfg.CollectionInterval
	if cfg.ProbeCore.Interval > 0 && cfg.ProbeCore.HostProcFallbackIntervalSamples > 0 {
		probeCadence := time.Duration(cfg.ProbeCore.HostProcFallbackIntervalSamples) * cfg.ProbeCore.Interval
		if probeCadence > base {
			base = probeCadence
		}
	}
	if base <= 0 {
		base = defaultInterval
	}
	return base
}

func derivedLogCollectionInterval(cfg Config) time.Duration {
	base := cfg.CollectionInterval * 3
	if base < defaultLogCollectionCadence {
		base = defaultLogCollectionCadence
	}
	return base
}

func derivedExternalMetricsInterval(cfg Config) time.Duration {
	base := cfg.CollectionInterval * 6
	if base < defaultExternalMetricsCadence {
		base = defaultExternalMetricsCadence
	}
	return base
}

func effectiveAuxiliaryInterval(base, collectionInterval time.Duration, decision protectionDecision) time.Duration {
	if base <= 0 {
		base = collectionInterval
	}
	if collectionInterval <= 0 {
		collectionInterval = defaultInterval
	}
	switch decision.Mode {
	case protectionModeIncident:
		if collectionInterval < base {
			return collectionInterval
		}
	case protectionModePressure, protectionModeCritical:
		if collectionInterval > base {
			return collectionInterval
		}
	}
	if decision.SignalPressure > 0 && collectionInterval < base {
		return collectionInterval
	}
	return base
}

func auxiliaryCadenceMetrics(now time.Time, component string, interval time.Duration, cacheHit bool, lastCollected time.Time) []*telemetryv1.Metric {
	labels := buildLabels(map[string]string{auxMetricComponentLabelKey: component})
	ageSeconds := 0.0
	if !lastCollected.IsZero() {
		ageSeconds = now.Sub(lastCollected).Seconds()
		if ageSeconds < 0 {
			ageSeconds = 0
		}
	}
	return []*telemetryv1.Metric{
		{
			Name:              "collector_aux_collection_interval_seconds",
			Value:             interval.Seconds(),
			TimestampUnixNano: now.UnixNano(),
			Labels:            labels,
		},
		{
			Name:              "collector_aux_collection_age_seconds",
			Value:             ageSeconds,
			TimestampUnixNano: now.UnixNano(),
			Labels:            labels,
		},
		{
			Name:              "collector_aux_collection_cache_hit",
			Value:             boolToFloat(cacheHit),
			TimestampUnixNano: now.UnixNano(),
			Labels:            labels,
		},
	}
}

func auxiliaryPayloadStateMetrics(now time.Time, component string, refreshed bool, suppressed bool) []*telemetryv1.Metric {
	labels := buildLabels(map[string]string{auxMetricComponentLabelKey: component})
	return []*telemetryv1.Metric{
		{
			Name:              auxMetricPayloadRefreshed,
			Value:             boolToFloat(refreshed),
			TimestampUnixNano: now.UnixNano(),
			Labels:            labels,
		},
		{
			Name:              auxMetricPayloadSuppressed,
			Value:             boolToFloat(suppressed),
			TimestampUnixNano: now.UnixNano(),
			Labels:            labels,
		},
	}
}

func (c *cachedProcessSamples) get(now time.Time, interval time.Duration) ([]*telemetryv1.ProcessSample, bool) {
	if c.lastCollected.IsZero() || interval <= 0 {
		return nil, false
	}
	if age := now.Sub(c.lastCollected); age >= 0 && age < interval {
		return cloneProcessSamples(c.lastValue), true
	}
	return nil, false
}

func (c *cachedProcessSamples) set(now time.Time, samples []*telemetryv1.ProcessSample) {
	c.lastCollected = now
	c.lastValue = cloneProcessSamples(samples)
}

func (c *cachedLogFingerprints) get(now time.Time, interval time.Duration) ([]*telemetryv1.LogFingerprint, bool) {
	if c.lastCollected.IsZero() || interval <= 0 {
		return nil, false
	}
	if age := now.Sub(c.lastCollected); age >= 0 && age < interval {
		return cloneLogFingerprints(c.lastValue), true
	}
	return nil, false
}

func (c *cachedLogFingerprints) set(now time.Time, logs []*telemetryv1.LogFingerprint) {
	c.lastCollected = now
	c.lastValue = cloneLogFingerprints(logs)
}

func (c *cachedTelemetryMetrics) get(now time.Time, interval time.Duration) ([]*telemetryv1.Metric, bool) {
	if c.lastCollected.IsZero() || interval <= 0 {
		return nil, false
	}
	if age := now.Sub(c.lastCollected); age >= 0 && age < interval {
		return cloneTelemetryMetrics(c.lastValue), true
	}
	return nil, false
}

func (c *cachedTelemetryMetrics) set(now time.Time, metrics []*telemetryv1.Metric) {
	c.lastCollected = now
	c.lastValue = cloneTelemetryMetrics(metrics)
}

func cloneProcessSamples(in []*telemetryv1.ProcessSample) []*telemetryv1.ProcessSample {
	if len(in) == 0 {
		return nil
	}
	out := make([]*telemetryv1.ProcessSample, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		copyItem := *item
		out = append(out, &copyItem)
	}
	return out
}

func cloneLogFingerprints(in []*telemetryv1.LogFingerprint) []*telemetryv1.LogFingerprint {
	if len(in) == 0 {
		return nil
	}
	out := make([]*telemetryv1.LogFingerprint, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		copyItem := *item
		out = append(out, &copyItem)
	}
	return out
}

func cloneTelemetryMetrics(in []*telemetryv1.Metric) []*telemetryv1.Metric {
	if len(in) == 0 {
		return nil
	}
	out := make([]*telemetryv1.Metric, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		copyItem := *item
		if len(item.Labels) > 0 {
			copyItem.Labels = make([]*telemetryv1.Label, 0, len(item.Labels))
			for _, label := range item.Labels {
				if label == nil {
					continue
				}
				copyLabel := *label
				copyItem.Labels = append(copyItem.Labels, &copyLabel)
			}
		}
		out = append(out, &copyItem)
	}
	return out
}
