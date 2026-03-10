package ingest

import (
	"path/filepath"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestMemoryStorePersistenceRoundTrip(t *testing.T) {
	cfg := DefaultStoreConfig()
	cfg.Persistence.Enabled = true
	cfg.Persistence.Path = filepath.Join(t.TempDir(), "ingest.db")
	cfg.Persistence.SyncInterval = 20 * time.Millisecond
	cfg.Persistence.CompactionInterval = time.Hour

	store := NewMemoryStoreWithConfig(cfg, nil)
	store.StartPersistence()

	now := time.Now().UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "collector-a",
		Hostname:    "node-a",
		Version:     "v0.6",
	}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 77.7},
	}, now)
	store.StoreBatchMeta("collector-a", &telemetryv1.TelemetryBatch{
		BatchId:          "batch-a",
		WallTimeUnixNano: now.UnixNano(),
	}, now)
	require.NoError(t, store.Close())

	reloaded := NewMemoryStoreWithConfig(cfg, nil)
	reloaded.StartPersistence()
	defer func() { _ = reloaded.Close() }()

	node := reloaded.Node("collector-a")
	require.NotNil(t, node)
	require.Equal(t, "node-a", node.Hostname)
	require.Equal(t, 77.7, node.Metrics["node_cpu_usage_percent"])
	require.Equal(t, "batch-a", node.LastBatchID)
}

func TestMemoryStoreSetRetentionPrunesHistory(t *testing.T) {
	cfg := DefaultStoreConfig()
	cfg.HistorySamplesPerNode = 16
	store := NewMemoryStoreWithConfig(cfg, nil)

	base := time.Now().UTC()
	for i := 0; i < 12; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		store.StoreMetrics("collector-a", []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: float64(i)},
		}, ts)
	}

	require.NoError(t, store.SetRetention(2*time.Hour, 4))
	history := store.MetricHistory("collector-a", time.Time{}, 100)
	require.Len(t, history, 4)
	require.Equal(t, 8.0, history[0].Metrics["node_cpu_usage_percent"])
	require.Equal(t, 11.0, history[3].Metrics["node_cpu_usage_percent"])
}
