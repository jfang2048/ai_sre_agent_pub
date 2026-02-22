package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMetricsHistoryGetLastNReturnsDefensiveCopies(t *testing.T) {
	history := NewMetricsHistory(4)
	history.Add(map[string]float64{"cpu": 42})

	last := history.GetLastN(1)
	require.Len(t, last, 1)

	last[0].Metrics["cpu"] = 99

	again := history.GetLastN(1)
	require.Len(t, again, 1)
	require.Equal(t, 42.0, again[0].Metrics["cpu"])
}

func TestMetricsHistoryGetSinceReturnsDefensiveCopies(t *testing.T) {
	history := NewMetricsHistory(4)
	history.Add(map[string]float64{"memory": 128})

	since := time.Now().Add(-time.Minute)
	samples := history.GetSince(since)
	require.Len(t, samples, 1)

	samples[0].Metrics["memory"] = 999

	again := history.GetSince(since)
	require.Len(t, again, 1)
	require.Equal(t, 128.0, again[0].Metrics["memory"])
}
