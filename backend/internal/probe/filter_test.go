package probe

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── labelsToKey tests ──────────────────────────────────────────────────

func TestLabelsToKeyDeterministic(t *testing.T) {
	labels := map[string]string{"host": "node-1", "env": "prod"}
	k1 := labelsToKey(labels)
	k2 := labelsToKey(labels)
	assert.Equal(t, k1, k2)
}

func TestLabelsToKeyOrderIndependent(t *testing.T) {
	a := labelsToKey(map[string]string{"b": "2", "a": "1"})
	b := labelsToKey(map[string]string{"a": "1", "b": "2"})
	assert.Equal(t, a, b, "Should produce same key regardless of map insertion order")
}

func TestLabelsToKeyEmpty(t *testing.T) {
	assert.Equal(t, "", labelsToKey(map[string]string{}))
}

func TestLabelsToKeyNil(t *testing.T) {
	assert.Equal(t, "", labelsToKey(nil))
}

// ── NewMetricsFilter tests ─────────────────────────────────────────────

func TestNewMetricsFilterClampsAlpha(t *testing.T) {
	f1 := NewMetricsFilter(0)
	assert.Equal(t, 0.5, f1.alpha, "alpha <= 0 should default to 0.5")

	f2 := NewMetricsFilter(-1)
	assert.Equal(t, 0.5, f2.alpha, "negative alpha should default to 0.5")

	f3 := NewMetricsFilter(2.0)
	assert.Equal(t, 1.0, f3.alpha, "alpha > 1 should be clamped to 1.0")

	f4 := NewMetricsFilter(0.3)
	assert.Equal(t, 0.3, f4.alpha, "valid alpha should be preserved")
}

// ── Apply — nil safety ─────────────────────────────────────────────────

func TestApplyNilBatch(t *testing.T) {
	f := NewMetricsFilter(1.0)
	result := f.Apply(nil)
	assert.Nil(t, result)
}

// ── Apply — counter passthrough ────────────────────────────────────────

func TestApplyCounterPassthrough(t *testing.T) {
	f := NewMetricsFilter(0.5) // heavy smoothing
	now := time.Now()

	batch := &MetricBatch{
		CollectedAt: now,
		Hostname:    "test",
		Metrics: []Metric{
			{Name: "requests_total", Type: "counter", Value: 42},
			{Name: "requests_total", Type: "counter", Value: 100},
		},
	}

	result := f.Apply(batch)
	require.NotNil(t, result)
	assert.Len(t, result.Metrics, 2)
	// Counters should NOT be smoothed
	assert.Equal(t, 42.0, result.Metrics[0].Value)
	assert.Equal(t, 100.0, result.Metrics[1].Value)
}

// ── Apply — EMA smoothing on gauges ────────────────────────────────────

func TestApplyEMASmoothing(t *testing.T) {
	f := NewMetricsFilter(0.5) // alpha=0.5 → 50% EMA
	now := time.Now()

	// First sample: establishes baseline, value passthrough
	batch1 := &MetricBatch{
		CollectedAt: now,
		Hostname:    "test",
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 100}},
	}
	r1 := f.Apply(batch1)
	assert.InDelta(t, 100.0, r1.Metrics[0].Value, 0.01, "First sample should pass through")

	// Second sample: EMA = 0.5*80 + 0.5*100 = 90
	batch2 := &MetricBatch{
		CollectedAt: now.Add(time.Second),
		Hostname:    "test",
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 80}},
	}
	r2 := f.Apply(batch2)
	assert.InDelta(t, 90.0, r2.Metrics[0].Value, 0.01, "EMA should smooth toward 80")

	// Third sample: EMA = 0.5*80 + 0.5*90 = 85
	batch3 := &MetricBatch{
		CollectedAt: now.Add(2 * time.Second),
		Hostname:    "test",
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 80}},
	}
	r3 := f.Apply(batch3)
	assert.InDelta(t, 85.0, r3.Metrics[0].Value, 0.01, "EMA should converge")
}

func TestApplyNoSmoothingAlpha1(t *testing.T) {
	f := NewMetricsFilter(1.0) // alpha=1.0 → no smoothing
	now := time.Now()

	batch1 := &MetricBatch{
		CollectedAt: now,
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 100}},
	}
	f.Apply(batch1)

	batch2 := &MetricBatch{
		CollectedAt: now.Add(time.Second),
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 50}},
	}
	r2 := f.Apply(batch2)
	assert.InDelta(t, 50.0, r2.Metrics[0].Value, 0.01, "alpha=1.0 should pass raw value through")
}

// ── Apply — outlier rejection ──────────────────────────────────────────

func TestApplyOutlierRejection(t *testing.T) {
	f := NewMetricsFilter(1.0) // no smoothing, isolate outlier logic
	now := time.Now()

	// First: establish baseline at 100
	f.Apply(&MetricBatch{
		CollectedAt: now,
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 100}},
	})

	// Second: spike to 600 (6x previous → > 5x outlier limit)
	batch2 := &MetricBatch{
		CollectedAt: now.Add(time.Second),
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 600}},
	}
	r2 := f.Apply(batch2)
	assert.Less(t, r2.Metrics[0].Value, 600.0,
		"Outlier spike should be dampened (replaced by EMA or previous value)")
}

func TestApplyValidJumpNotRejected(t *testing.T) {
	f := NewMetricsFilter(1.0)
	now := time.Now()

	// First: baseline at 100
	f.Apply(&MetricBatch{
		CollectedAt: now,
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 100}},
	})

	// Second: jump to 400 (4x → within 5x limit)
	batch2 := &MetricBatch{
		CollectedAt: now.Add(time.Second),
		Metrics:     []Metric{{Name: "cpu", Type: "gauge", Value: 400}},
	}
	r2 := f.Apply(batch2)
	assert.InDelta(t, 400.0, r2.Metrics[0].Value, 0.01,
		"Jump within 5x limit should pass through")
}

// ── Apply — label isolation ────────────────────────────────────────────

func TestApplyIsolatesByLabels(t *testing.T) {
	f := NewMetricsFilter(0.5)
	now := time.Now()

	batch := &MetricBatch{
		CollectedAt: now,
		Metrics: []Metric{
			{Name: "cpu", Type: "gauge", Value: 100, Labels: map[string]string{"core": "0"}},
			{Name: "cpu", Type: "gauge", Value: 50, Labels: map[string]string{"core": "1"}},
		},
	}
	r := f.Apply(batch)
	require.Len(t, r.Metrics, 2)

	// First values should pass through (establishing baselines)
	assert.InDelta(t, 100.0, r.Metrics[0].Value, 0.01)
	assert.InDelta(t, 50.0, r.Metrics[1].Value, 0.01)
}

// ── Apply — metadata preservation ──────────────────────────────────────

func TestApplyPreservesMetadata(t *testing.T) {
	f := NewMetricsFilter(1.0)
	now := time.Now()

	batch := &MetricBatch{
		CollectedAt: now,
		Hostname:    "prod-1",
		Metrics: []Metric{
			{Name: "mem", Type: "gauge", Value: 80, Labels: map[string]string{"region": "us"}},
		},
	}

	r := f.Apply(batch)
	assert.Equal(t, "prod-1", r.Hostname)
	assert.Equal(t, now, r.CollectedAt)
	assert.Equal(t, "mem", r.Metrics[0].Name)
	assert.Equal(t, "gauge", r.Metrics[0].Type)
	assert.Equal(t, map[string]string{"region": "us"}, r.Metrics[0].Labels)
}
