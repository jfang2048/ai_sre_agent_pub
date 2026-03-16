package logindex

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeParsesJSONAndTextFields(t *testing.T) {
	cfg := DefaultConfig()
	fallback := time.Date(2026, 2, 22, 10, 0, 0, 0, time.UTC)

	entry, ok := Normalize(cfg, RawEvent{
		CollectorID: "collector-a",
		Hostname:    "node-a",
		Message:     `{"timestamp":"2026-02-22T10:01:00Z","level":"ERROR","service":"checkout","process":"worker","pid":321,"message":"request timeout","source":"api"}`,
	}, fallback)
	require.True(t, ok)
	require.Equal(t, "collector-a", entry.CollectorID)
	require.Equal(t, "checkout", entry.Service)
	require.Equal(t, "worker", entry.Process)
	require.Equal(t, "321", entry.PID)
	require.Equal(t, LevelError, entry.Level)
	require.Equal(t, "request timeout", entry.Message)
	require.Equal(t, "api", entry.Source)
	require.Equal(t, time.Date(2026, 2, 22, 10, 1, 0, 0, time.UTC), entry.Timestamp)

	entry2, ok := Normalize(cfg, RawEvent{
		Message: "2026-02-22T10:02:00Z kubelet[77]: warning disk pressure",
	}, fallback)
	require.True(t, ok)
	require.Equal(t, "kubelet", entry2.Process)
	require.Equal(t, "77", entry2.PID)
	require.Equal(t, LevelWarn, entry2.Level)
	require.Equal(t, "warning disk pressure", entry2.Message)
}

func TestIndexSearchAndCorrelation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Retention = 2 * time.Hour
	cfg.MaxEntries = 10000
	idx := NewIndex(cfg)
	now := time.Date(2026, 2, 22, 11, 0, 0, 0, time.UTC)
	idx.SetNowFunc(func() time.Time { return now })

	accepted := idx.AddBatch([]RawEvent{
		{
			Timestamp:      now.Add(-3 * time.Minute),
			CollectorID:    "collector-a",
			Hostname:       "node-a",
			Service:        "checkout",
			Process:        "checkout-worker",
			Level:          "info",
			Message:        "request completed",
			Count:          3,
			MetricSnapshot: map[string]float64{"node_cpu_usage_percent": 42.0},
		},
		{
			Timestamp:      now.Add(-2 * time.Minute),
			CollectorID:    "collector-a",
			Hostname:       "node-a",
			Service:        "checkout",
			Process:        "checkout-worker",
			Level:          "error",
			Message:        "request timeout while calling db",
			Count:          2,
			MetricSnapshot: map[string]float64{"node_cpu_usage_percent": 91.0},
		},
		{
			Timestamp:      now.Add(-time.Minute),
			CollectorID:    "collector-b",
			Hostname:       "node-b",
			Service:        "payments",
			Process:        "payments-api",
			Level:          "warn",
			Message:        "slow downstream response",
			Count:          1,
			MetricSnapshot: map[string]float64{"node_cpu_usage_percent": 54.0},
		},
	})
	require.Equal(t, 3, accepted)

	result := idx.Search(SearchQuery{
		Text:        "timeout",
		CollectorID: "collector-a",
		Service:     "checkout",
		Since:       now.Add(-10 * time.Minute),
		Until:       now,
		Limit:       10,
	})
	require.Equal(t, 1, result.Total)
	require.Len(t, result.Entries, 1)
	require.Equal(t, LevelError, result.Entries[0].Level)
	require.NotEmpty(t, result.LevelCounts)
	require.NotEmpty(t, result.Timeline)
	require.NotEmpty(t, result.Highlights)

	correlated := idx.Search(SearchQuery{
		Since: now.Add(-10 * time.Minute),
		Until: now,
		Limit: 10,
	})
	require.NotEmpty(t, correlated.MetricCorrelated)
	foundCPU := false
	for _, item := range correlated.MetricCorrelated {
		if item.Metric == "node_cpu_usage_percent" {
			foundCPU = true
			require.Greater(t, item.UpliftPercent, 0.0)
		}
	}
	require.True(t, foundCPU)
}

func TestIndexRetentionDropsOldEvents(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Retention = time.Minute
	cfg.SegmentDuration = time.Minute
	idx := NewIndex(cfg)

	now := time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC)
	idx.SetNowFunc(func() time.Time { return now })

	accepted := idx.AddBatch([]RawEvent{
		{Timestamp: now.Add(-5 * time.Minute), Message: "old event", CollectorID: "c1"},
		{Timestamp: now.Add(-30 * time.Second), Message: "fresh event", CollectorID: "c1"},
	})
	require.Equal(t, 1, accepted)

	stats := idx.Stats()
	require.Equal(t, 1, stats.Entries)
	require.Equal(t, uint64(1), stats.DroppedEvents)

	result := idx.Search(SearchQuery{Since: now.Add(-2 * time.Minute), Until: now, Limit: 10})
	require.Equal(t, 1, result.Total)
	require.Equal(t, "fresh event", result.Entries[0].Message)
}
