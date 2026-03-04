package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fleetResponse struct {
	Nodes     []fleetNode `json:"nodes"`
	Count     int         `json:"count"`
	Timestamp time.Time   `json:"timestamp"`
}

type fleetNode struct {
	CollectorID string `json:"collector_id"`
	Hostname    string `json:"hostname"`
}

func TestHandleFleetReturnsStableShapeWithoutStore(t *testing.T) {
	ctrl := &Controller{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet", nil)
	w := httptest.NewRecorder()

	ctrl.handleFleet(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp fleetResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Count)
	assert.Empty(t, resp.Nodes)
	assert.False(t, resp.Timestamp.IsZero())
}

func TestHandleFleetCountsAllCollectors(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-c", Hostname: "node-c"}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet", nil)
	w := httptest.NewRecorder()

	ctrl.handleFleet(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp fleetResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, 3, resp.Count)
	require.Len(t, resp.Nodes, 3)

	ids := map[string]struct{}{}
	for _, node := range resp.Nodes {
		ids[node.CollectorID] = struct{}{}
	}
	assert.Contains(t, ids, "collector-a")
	assert.Contains(t, ids, "collector-b")
	assert.Contains(t, ids, "collector-c")
	assert.False(t, resp.Timestamp.IsZero())
}

func TestHandleIngestStatusIncludesFleetNodeCount(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)

	idx := logindex.NewIndex(logindex.DefaultConfig())
	store.AttachLogIndex(idx)
	server := ingest.NewServer(store, zap.NewNop())

	ctrl := &Controller{
		ingestStore:  store,
		ingestServer: server,
		logIndex:     idx,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ingest/status", nil)
	w := httptest.NewRecorder()

	ctrl.handleIngestStatus(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		FleetNodes int                    `json:"fleet_nodes"`
		Stats      map[string]interface{} `json:"stats"`
		Logs       map[string]interface{} `json:"logs"`
		Timestamp  time.Time              `json:"timestamp"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 2, resp.FleetNodes)
	assert.NotEmpty(t, resp.Stats)
	assert.NotEmpty(t, resp.Logs)
	assert.False(t, resp.Timestamp.IsZero())
}

func TestHandleStorageStatusIncludesRetentionAndFederationHints(t *testing.T) {
	store := ingest.NewMemoryStoreWithConfig(ingest.StoreConfig{
		NodeRetention:         12 * time.Hour,
		HistorySamplesPerNode: 120,
		MaxNodes:              256,
	}, zap.NewNop())
	ctrl := &Controller{ingestStore: store}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/storage/status", nil)
	w := httptest.NewRecorder()
	ctrl.handleStorageStatus(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Storage struct {
			NodeRetention         string `json:"node_retention"`
			HistorySamplesPerNode int    `json:"history_samples_per_node"`
			Federation            struct {
				PartitionKey string `json:"partition_key"`
				ShardKey     string `json:"shard_key"`
			} `json:"federation"`
		} `json:"storage"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "12h0m0s", resp.Storage.NodeRetention)
	assert.Equal(t, 120, resp.Storage.HistorySamplesPerNode)
	assert.Equal(t, "collector_id", resp.Storage.Federation.PartitionKey)
	assert.Equal(t, "collector_id", resp.Storage.Federation.ShardKey)
}

func TestHandleStorageRetentionUpdatesStore(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}

	body := []byte(`{"node_retention":"48h","history_samples_per_node":300}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/retention", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ctrl.handleStorageRetention(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/storage/retention", nil)
	wGet := httptest.NewRecorder()
	ctrl.handleStorageRetention(wGet, reqGet)
	require.Equal(t, http.StatusOK, wGet.Code)

	var resp struct {
		Storage struct {
			NodeRetention         string `json:"node_retention"`
			HistorySamplesPerNode int    `json:"history_samples_per_node"`
		} `json:"storage"`
	}
	require.NoError(t, json.NewDecoder(wGet.Body).Decode(&resp))
	assert.Equal(t, "48h0m0s", resp.Storage.NodeRetention)
	assert.Equal(t, 300, resp.Storage.HistorySamplesPerNode)
}

func TestHandleStorageRetentionRejectedInStandbyMode(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{
		ingestStore: store,
		config: Config{
			HA: HAConfig{
				Enabled: true,
				Mode:    "standby",
			},
		},
	}

	body := []byte(`{"node_retention":"48h","history_samples_per_node":300}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/retention", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ctrl.handleStorageRetention(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleFinOpsSignalsUsesNodeHints(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 12, TimestampUnixNano: now.UnixNano()},
		{Name: "node_memory_Used_bytes", Value: 3 * 1024 * 1024 * 1024, TimestampUnixNano: now.UnixNano()},
		{Name: "node_memory_MemTotal_bytes", Value: 16 * 1024 * 1024 * 1024, TimestampUnixNano: now.UnixNano()},
		{Name: "node_gpu_utilization_sm_avg_percent", Value: 18, TimestampUnixNano: now.UnixNano()},
		{Name: "node_gpu_process_total", Value: 2, TimestampUnixNano: now.UnixNano()},
	}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/finops/signals", nil)
	w := httptest.NewRecorder()
	ctrl.handleFinOpsSignals(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Summary struct {
			NodesAnalyzed int `json:"nodes_analyzed"`
			IdleCPUHints  int `json:"idle_cpu_hints"`
			GPUWasteHints int `json:"gpu_waste_hints"`
		} `json:"summary"`
		Nodes []struct {
			CollectorID         string  `json:"collector_id"`
			IdleCPUHint         bool    `json:"idle_cpu_hint"`
			OversizedMemoryHint bool    `json:"oversized_memory_hint"`
			GPUWasteHint        bool    `json:"gpu_waste_hint"`
			PotentialWasteScore float64 `json:"potential_waste_score"`
		} `json:"nodes"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, 1, resp.Summary.NodesAnalyzed)
	require.Equal(t, 1, resp.Summary.IdleCPUHints)
	require.Equal(t, 1, resp.Summary.GPUWasteHints)
	require.Len(t, resp.Nodes, 1)
	assert.Equal(t, "collector-a", resp.Nodes[0].CollectorID)
	assert.True(t, resp.Nodes[0].IdleCPUHint)
	assert.True(t, resp.Nodes[0].OversizedMemoryHint)
	assert.True(t, resp.Nodes[0].GPUWasteHint)
	assert.Greater(t, resp.Nodes[0].PotentialWasteScore, 0.9)
}
