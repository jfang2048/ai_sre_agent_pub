package ingest

import (
	"fmt"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMemoryStoreSoakBoundedRetention(t *testing.T) {
	cfg := StoreConfig{
		NodeRetention:         90 * time.Minute,
		HistorySamplesPerNode: 120,
		MaxNodes:              64,
	}
	store := NewMemoryStoreWithConfig(cfg, zap.NewNop())

	start := time.Now().UTC().Add(-30 * time.Minute)
	for i := 0; i < 128; i++ {
		collectorID := fmt.Sprintf("collector-%03d", i)
		seenAt := start.Add(time.Duration(i) * time.Second)
		store.UpsertCollector(&telemetryv1.CollectorInfo{
			CollectorId: collectorID,
			Hostname:    fmt.Sprintf("node-%03d", i),
		}, seenAt)

		for j := 0; j < 180; j++ {
			ts := seenAt.Add(time.Duration(j) * time.Second)
			store.StoreMetrics(collectorID, []*telemetryv1.Metric{
				{
					Name:              "node_cpu_usage_percent",
					Value:             float64((i+j)%100) + 0.5,
					TimestampUnixNano: ts.UnixNano(),
				},
				{
					Name:              "node_memory_Used_bytes",
					Value:             float64(8*1024*1024*1024 + j*4096),
					TimestampUnixNano: ts.UnixNano(),
				},
			}, ts)
		}
	}

	stats := store.Stats()
	require.LessOrEqual(t, stats.Nodes, 64)
	require.LessOrEqual(t, stats.HistorySeries, 64)
	require.LessOrEqual(t, stats.HistorySamples, 64*120)

	snapshot := store.Snapshot()
	require.NotEmpty(t, snapshot)
	for _, node := range snapshot {
		require.NotNil(t, node)
		require.LessOrEqual(t, len(store.MetricHistory(node.CollectorID, time.Time{}, 500)), 120)
	}
}

func BenchmarkMemoryStoreHighVolumeIngest(b *testing.B) {
	cfg := StoreConfig{
		NodeRetention:         6 * time.Hour,
		HistorySamplesPerNode: 720,
		MaxNodes:              4096,
	}
	store := NewMemoryStoreWithConfig(cfg, zap.NewNop())
	now := time.Now().UTC()
	collectors := 512
	for i := 0; i < collectors; i++ {
		id := fmt.Sprintf("bench-%03d", i)
		store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: id, Hostname: id}, now)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("bench-%03d", i%collectors)
		ts := now.Add(time.Duration(i) * time.Millisecond)
		store.StoreMetrics(id, []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: float64(i % 100), TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_Used_bytes", Value: float64(16*1024*1024*1024 + i), TimestampUnixNano: ts.UnixNano()},
			{Name: "node_network_total_receive_bytes_per_second", Value: float64(i % 100000), TimestampUnixNano: ts.UnixNano()},
			{Name: "node_disk_total_iops_per_second", Value: float64(i % 10000), TimestampUnixNano: ts.UnixNano()},
		}, ts)
	}
}
