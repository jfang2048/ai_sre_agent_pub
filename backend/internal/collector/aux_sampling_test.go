package collector

import (
	"context"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeProcessCollector struct {
	calls int
	out   []*telemetryv1.ProcessSample
}

func (f *fakeProcessCollector) Collect(time.Time) []*telemetryv1.ProcessSample {
	f.calls++
	return cloneProcessSamples(f.out)
}

type fakeLogCollector struct {
	calls int
	out   []*telemetryv1.LogFingerprint
}

func (f *fakeLogCollector) Collect(time.Time) []*telemetryv1.LogFingerprint {
	f.calls++
	return cloneLogFingerprints(f.out)
}

func TestCollectProcessFallbackWithCadenceCachesUntilIncident(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	cfg := DefaultConfig()
	cfg.CollectionInterval = 5 * time.Second
	cfg.ProbeCore.Interval = 1 * time.Second
	cfg.ProbeCore.HostProcFallbackIntervalSamples = 10

	fake := &fakeProcessCollector{
		out: []*telemetryv1.ProcessSample{{Pid: 101, Name: "trainer", CpuPercent: 73}},
	}
	c := &Collector{
		cfg:             cfg,
		currentInterval: cfg.CollectionInterval,
		compatProcTopK:  fake,
		promMetrics:     newRuntimePromMetrics(),
		logger:          zap.NewNop(),
	}

	processes, metrics := c.collectProcessFallbackWithCadence(now, cfg, protectionDecision{}, sourceCollection{compatibilityFallback: true})
	require.Len(t, processes, 1)
	require.Equal(t, 1, fake.calls)
	require.Equal(t, 10.0, metricValue(metrics, "collector_aux_collection_interval_seconds"))

	processes, metrics = c.collectProcessFallbackWithCadence(now.Add(5*time.Second), cfg, protectionDecision{}, sourceCollection{compatibilityFallback: true})
	require.Len(t, processes, 0)
	require.Equal(t, 1, fake.calls)
	require.Equal(t, 1.0, metricValue(metrics, "collector_aux_collection_cache_hit"))
	require.Equal(t, 1.0, metricValue(metrics, "collector_aux_payload_suppressed"))
	require.Equal(t, 0.0, metricValue(metrics, "collector_aux_payload_refreshed"))

	processes, metrics = c.collectProcessFallbackWithCadence(now.Add(10*time.Second), cfg, protectionDecision{Mode: protectionModeIncident, SignalPressure: 1}, sourceCollection{compatibilityFallback: true})
	require.Len(t, processes, 1)
	require.Equal(t, 2, fake.calls)
	require.Equal(t, 5.0, metricValue(metrics, "collector_aux_collection_interval_seconds"))
	require.Equal(t, 1.0, metricValue(metrics, "collector_aux_payload_refreshed"))
}

func TestCollectLogsWithCadenceCachesBetweenNormalCycles(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	cfg := DefaultConfig()
	cfg.CollectionInterval = 5 * time.Second

	fake := &fakeLogCollector{
		out: []*telemetryv1.LogFingerprint{{Fingerprint: "abc", Count: 4, Example: "timeout while syncing"}},
	}
	c := &Collector{
		cfg:             cfg,
		currentInterval: cfg.CollectionInterval,
		logTail:         fake,
		promMetrics:     newRuntimePromMetrics(),
		logger:          zap.NewNop(),
	}

	logs, metrics := c.collectLogsWithCadence(now, cfg, protectionDecision{})
	require.Len(t, logs, 1)
	require.Equal(t, 1, fake.calls)
	require.Equal(t, 15.0, metricValue(metrics, "collector_aux_collection_interval_seconds"))

	logs, metrics = c.collectLogsWithCadence(now.Add(5*time.Second), cfg, protectionDecision{})
	require.Len(t, logs, 0)
	require.Equal(t, 1, fake.calls)
	require.Equal(t, 1.0, metricValue(metrics, "collector_aux_collection_cache_hit"))
	require.Equal(t, 1.0, metricValue(metrics, "collector_aux_payload_suppressed"))
	require.Equal(t, 0.0, metricValue(metrics, "collector_aux_payload_refreshed"))

	logs, metrics = c.collectLogsWithCadence(now.Add(5*time.Second), cfg, protectionDecision{Mode: protectionModeIncident, SignalPressure: 1})
	require.Len(t, logs, 1)
	require.Equal(t, 2, fake.calls)
	require.Equal(t, 5.0, metricValue(metrics, "collector_aux_collection_interval_seconds"))
	require.Equal(t, 1.0, metricValue(metrics, "collector_aux_payload_refreshed"))
}

func TestCollectExternalMetricsWithCadenceCachesBetweenCycles(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	cfg := DefaultConfig()
	cfg.CollectionInterval = 5 * time.Second
	cfg.ExternalMetricsCmd = "/usr/local/bin/fake-metrics"
	cfg.ExternalMetricsTimeout = 100 * time.Millisecond

	calls := 0
	c := &Collector{
		cfg:             cfg,
		currentInterval: cfg.CollectionInterval,
		promMetrics:     newRuntimePromMetrics(),
		logger:          zap.NewNop(),
		externalFetch: func(context.Context, string, time.Duration) (extMetricPayload, error) {
			calls++
			return extMetricPayload{
				Metrics: []extMetric{{Name: "ext_queue_depth", Value: 12}},
			}, nil
		},
	}

	metrics := c.collectExternalMetricsWithCadence(context.Background(), now, cfg, protectionDecision{})
	require.Equal(t, 1, calls)
	require.Equal(t, 12.0, metricValue(metrics, "ext_queue_depth"))
	require.Equal(t, 30.0, metricValue(metrics, "collector_aux_collection_interval_seconds"))

	metrics = c.collectExternalMetricsWithCadence(context.Background(), now.Add(5*time.Second), cfg, protectionDecision{})
	require.Equal(t, 1, calls)
	require.Equal(t, 1.0, metricValue(metrics, "collector_aux_collection_cache_hit"))

	metrics = c.collectExternalMetricsWithCadence(context.Background(), now.Add(5*time.Second), cfg, protectionDecision{Mode: protectionModeIncident, SignalPressure: 1})
	require.Equal(t, 2, calls)
	require.Equal(t, 5.0, metricValue(metrics, "collector_aux_collection_interval_seconds"))
}

func TestCollectLogsWithCadenceCanKeepCachedPayloadsWhenSuppressionDisabled(t *testing.T) {
	now := time.Unix(1700000100, 0).UTC()
	cfg := DefaultConfig()
	cfg.CollectionInterval = 5 * time.Second
	cfg.SuppressCachedAuxPayloads = false

	fake := &fakeLogCollector{
		out: []*telemetryv1.LogFingerprint{{Fingerprint: "abc", Count: 4, Example: "timeout while syncing"}},
	}
	c := &Collector{
		cfg:             cfg,
		currentInterval: cfg.CollectionInterval,
		logTail:         fake,
		promMetrics:     newRuntimePromMetrics(),
		logger:          zap.NewNop(),
	}

	logs, _ := c.collectLogsWithCadence(now, cfg, protectionDecision{})
	require.Len(t, logs, 1)

	logs, metrics := c.collectLogsWithCadence(now.Add(5*time.Second), cfg, protectionDecision{})
	require.Len(t, logs, 1)
	require.Equal(t, 1.0, metricValue(metrics, "collector_aux_collection_cache_hit"))
	require.Equal(t, 0.0, metricValue(metrics, "collector_aux_payload_suppressed"))
}
