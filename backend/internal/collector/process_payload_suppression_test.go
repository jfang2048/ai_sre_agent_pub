package collector

import (
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestSuppressUnchangedProcessPayloadDropsNearIdenticalProcessLists(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ProcessPayloadRefreshInterval = 2 * time.Minute

	c := &Collector{cfg: cfg}
	now := time.Now().UTC()
	processes := []*telemetryv1.ProcessSample{{
		Pid:        4128,
		Name:       "trainer",
		CpuPercent: 71.2,
		RssBytes:   9 * 1024 * 1024 * 1024,
		IoReadBps:  8.4 * 1024 * 1024,
		IoWriteBps: 3.2 * 1024 * 1024,
	}}

	out, metrics := c.suppressUnchangedProcessPayload(now, cloneProcessSamples(processes))
	require.Len(t, out, 1)
	require.Equal(t, 1.0, metricValueAny(metrics, collectorProcessPayloadRefreshedMetric))
	require.Equal(t, 0.0, metricValueAny(metrics, collectorProcessPayloadSuppressedMetric))

	nearIdentical := []*telemetryv1.ProcessSample{{
		Pid:        4128,
		Name:       "trainer",
		CpuPercent: 71.24,
		RssBytes:   processes[0].RssBytes + 8*1024*1024,
		IoReadBps:  processes[0].IoReadBps + 128*1024,
		IoWriteBps: processes[0].IoWriteBps + 64*1024,
	}}

	out, metrics = c.suppressUnchangedProcessPayload(now.Add(20*time.Second), nearIdentical)
	require.Nil(t, out)
	require.Equal(t, 0.0, metricValueAny(metrics, collectorProcessPayloadRefreshedMetric))
	require.Equal(t, 1.0, metricValueAny(metrics, collectorProcessPayloadSuppressedMetric))

	out, metrics = c.suppressUnchangedProcessPayload(now.Add(3*time.Minute), nearIdentical)
	require.Len(t, out, 1)
	require.Equal(t, 1.0, metricValueAny(metrics, collectorProcessPayloadRefreshedMetric))
	require.Equal(t, 0.0, metricValueAny(metrics, collectorProcessPayloadSuppressedMetric))
}

func TestSuppressUnchangedProcessPayloadEmitsImmediatelyWhenShapeChanges(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ProcessPayloadRefreshInterval = 2 * time.Minute

	c := &Collector{cfg: cfg}
	now := time.Now().UTC()

	initial := []*telemetryv1.ProcessSample{{
		Pid:        4128,
		Name:       "trainer",
		CpuPercent: 71.2,
		RssBytes:   9 * 1024 * 1024 * 1024,
	}}
	_, _ = c.suppressUnchangedProcessPayload(now, initial)

	changed := []*telemetryv1.ProcessSample{{
		Pid:        4128,
		Name:       "trainer",
		CpuPercent: 76.4,
		RssBytes:   11 * 1024 * 1024 * 1024,
	}}
	out, metrics := c.suppressUnchangedProcessPayload(now.Add(15*time.Second), changed)
	require.Len(t, out, 1)
	require.Equal(t, 1.0, metricValueAny(metrics, collectorProcessPayloadRefreshedMetric))
	require.Equal(t, 0.0, metricValueAny(metrics, collectorProcessPayloadSuppressedMetric))
}

func TestSuppressUnchangedProcessPayloadCanBeDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SuppressUnchangedProcessPayloads = false

	c := &Collector{cfg: cfg}
	now := time.Now().UTC()
	processes := []*telemetryv1.ProcessSample{{
		Pid:        4128,
		Name:       "trainer",
		CpuPercent: 71.2,
		RssBytes:   9 * 1024 * 1024 * 1024,
	}}

	out, metrics := c.suppressUnchangedProcessPayload(now, cloneProcessSamples(processes))
	require.Len(t, out, 1)
	require.Equal(t, 1.0, metricValueAny(metrics, collectorProcessPayloadRefreshedMetric))
	require.Equal(t, 0.0, metricValueAny(metrics, collectorProcessPayloadSuppressedMetric))

	out, metrics = c.suppressUnchangedProcessPayload(now.Add(10*time.Second), cloneProcessSamples(processes))
	require.Len(t, out, 1)
	require.Equal(t, 1.0, metricValueAny(metrics, collectorProcessPayloadRefreshedMetric))
	require.Equal(t, 0.0, metricValueAny(metrics, collectorProcessPayloadSuppressedMetric))
}
