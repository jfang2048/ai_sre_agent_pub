package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
)

func TestHandleDataPathDiagnosticsRanksNetworkAndStorage(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-2 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)

	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_network_utilization_peak_percent", Value: 92},
		{Name: "node_tcp_retransmit_ratio", Value: 0.03},
		{Name: "node_tcp_retransmits_per_second", Value: 840},
		{Name: "node_softnet_dropped_per_second", Value: 120},
		{Name: "node_rdma_errors_per_second", Value: 8},
		{Name: "node_cpu_usage_percent", Value: 55},
		{Name: "node_memory_MemTotal_bytes", Value: 128 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 96 * 1024 * 1024 * 1024},
	}, now)

	store.StoreMetrics("collector-b", []*telemetryv1.Metric{
		{Name: "node_disk_utilization_peak_percent", Value: 96},
		{Name: "node_disk_queue_depth_total", Value: 88},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.09},
		{Name: "node_pressure_io_full_avg10", Value: 14},
		{Name: "node_filesystem_space_pressure_percent", Value: 93},
		{Name: "node_nvme_utilization_peak_percent", Value: 94},
		{Name: "node_nvme_avg_request_latency_seconds", Value: 0.045},
		{Name: "node_cpu_usage_percent", Value: 48},
		{Name: "node_memory_MemTotal_bytes", Value: 128 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 72 * 1024 * 1024 * 1024},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/data-path", nil)
	w := httptest.NewRecorder()
	ctrl.handleDataPathDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp dataPathDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, 2, resp.Summary.NodeCount)
			if assert.NotEmpty(t, resp.Network.Rankings) {
				assert.Equal(t, "collector-a", resp.Network.Rankings[0].CollectorID)
			}
			if assert.NotEmpty(t, resp.Storage.Rankings) {
				assert.Equal(t, "collector-b", resp.Storage.Rankings[0].CollectorID)
			}
			assert.NotEmpty(t, resp.DataPaths)
		}
	}
}

func TestHandleDataPathDiagnosticsCollectorFilter(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)

	store.StoreMetrics("collector-a", []*telemetryv1.Metric{{Name: "node_cpu_usage_percent", Value: 10}}, now)
	store.StoreMetrics("collector-b", []*telemetryv1.Metric{{Name: "node_cpu_usage_percent", Value: 20}}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/data-path?collector_id=collector-b", nil)
	w := httptest.NewRecorder()
	ctrl.handleDataPathDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp dataPathDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-b", resp.CollectorID)
			assert.Equal(t, 1, resp.Summary.NodeCount)
			if assert.NotEmpty(t, resp.DataPaths) {
				assert.Equal(t, "collector-b", resp.DataPaths[0].CollectorID)
			}
		}
	}
}

func TestHandleDataPathDiagnosticsProbeCoreReliabilityRanking(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)

	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "collector_probe_core_client_available", Value: 1},
		{Name: "collector_probe_core_active", Value: 1},
		{Name: "collector_probe_core_fresh", Value: 1},
		{Name: "collector_probe_core_collector_selection_valid", Value: 1},
		{Name: "collector_probe_core_last_frame_age_seconds", Value: 1},
		{
			Name:  "collector_probe_source",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "source", Value: "probe_core"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_requested",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "network"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_active",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "network"},
			},
		},
	}, now)

	store.StoreMetrics("collector-b", []*telemetryv1.Metric{
		{Name: "collector_probe_core_client_available", Value: 0},
		{Name: "collector_probe_core_active", Value: 0},
		{Name: "collector_probe_core_fresh", Value: 0},
		{Name: "collector_probe_core_collector_selection_valid", Value: 0},
		{Name: "collector_probe_core_last_frame_age_seconds", Value: 90},
		{Name: "collector_probe_core_decode_errors_total", Value: 5},
		{Name: "collector_probe_core_crc_failures_total", Value: 2},
		{Name: "collector_probe_core_restarts_total", Value: 3},
		{
			Name:  "collector_probe_source",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "source", Value: "go"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_requested",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "network"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_active",
			Value: 0,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "network"},
			},
		},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/data-path", nil)
	w := httptest.NewRecorder()
	ctrl.handleDataPathDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp dataPathDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			if assert.NotEmpty(t, resp.ProbeCore.Rankings) {
				assert.Equal(t, "collector-b", resp.ProbeCore.Rankings[0].CollectorID)
				assert.NotEmpty(t, resp.ProbeCore.Rankings[0].Factors)
				assert.Contains(t, strings.Join(resp.ProbeCore.Rankings[0].Factors, " "), "fallback")
				assert.Contains(t, strings.Join(resp.ProbeCore.Rankings[0].Factors, " "), "invalid")
			}
			assert.Equal(t, 1, resp.Summary.ProbeCoreFallbackNodes)
			assert.Equal(t, 1, resp.Summary.ProbeCoreInvalidConfigNodes)
			assert.Greater(t, resp.ProbeCore.ClusterHealthScore, 0.0)
			assert.Less(t, resp.ProbeCore.ClusterHealthScore, 100.0)
		}
	}
}

func TestHandleDataPathDiagnosticsDetectsStorageAnomaly(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-40 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)

	for i := 0; i < 10; i++ {
		latency := 0.002
		if i == 9 {
			latency = 0.06
		}
		store.StoreMetrics("collector-a", []*telemetryv1.Metric{
			{Name: "node_disk_request_latency_p99_seconds", Value: latency},
			{Name: "node_disk_utilization_peak_percent", Value: 60},
			{Name: "node_cpu_usage_percent", Value: 20},
			{Name: "node_memory_MemTotal_bytes", Value: 64 * 1024 * 1024 * 1024},
			{Name: "node_memory_Used_bytes", Value: 24 * 1024 * 1024 * 1024},
		}, now.Add(time.Duration(i)*3*time.Minute))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/data-path?collector_id=collector-a", nil)
	w := httptest.NewRecorder()
	ctrl.handleDataPathDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp dataPathDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			found := false
			for _, anomaly := range resp.Storage.Anomalies {
				if anomaly.Metric == "node_disk_request_latency_p99_seconds" {
					found = true
					assert.Equal(t, "collector-a", anomaly.CollectorID)
					break
				}
			}
			assert.True(t, found, "expected storage latency anomaly")
		}
	}
}

func TestNetworkPressureScoreRDMACongestionSignals(t *testing.T) {
	score, signals, factors := networkPressureScore(map[string]float64{
		"node_network_utilization_peak_percent":        88,
		"node_tcp_retransmit_ratio":                    0.014,
		"node_softnet_dropped_per_second":              64,
		"node_network_interface_tx_queue_fill_percent": 82,
		"node_rdma_errors_per_second":                  4,
		"node_rdma_port_errors_per_second":             6,
		"node_rdma_congestion_events_per_second":       35,
		"node_rdma_pfc_pause_frames_per_second":        420,
		"node_rdma_ecn_marked_ratio":                   0.018,
		"node_rdma_port_transmit_bytes_per_second":     2.4e10,
		"node_rdma_port_receive_bytes_per_second":      9.0e9,
	})

	assert.Greater(t, score, 4.0)
	assert.Greater(t, signals["rdma_errors_per_second"], 0.0)
	assert.Greater(t, signals["rdma_pfc_pause_per_second"], 0.0)
	assert.Greater(t, signals["rdma_ecn_marked_ratio"], 0.0)
	assert.Greater(t, signals["rdma_comm_imbalance_ratio"], 0.0)
	assert.Contains(t, strings.Join(factors, " "), "PFC pause activity")
	assert.Contains(t, strings.Join(factors, " "), "collective communication skew")
}

func TestStoragePressureScorePipelineSignals(t *testing.T) {
	score, signals, factors := storagePressureScore(map[string]float64{
		"node_disk_utilization_peak_percent":          72,
		"node_disk_queue_depth_total":                 48,
		"node_disk_request_latency_p99_seconds":       0.031,
		"node_pressure_io_full_avg10":                 8,
		"node_filesystem_space_pressure_percent":      80,
		"node_nvme_utilization_peak_percent":          84,
		"node_nvme_avg_request_latency_seconds":       0.028,
		"node_storage_metadata_ops_per_second":        18000,
		"node_storage_metadata_latency_p99_seconds":   0.022,
		"node_storage_small_io_ratio":                 0.44,
		"node_object_storage_get_latency_p99_seconds": 0.071,
		"node_checkpoint_write_latency_p99_seconds":   0.160,
		"node_dataloader_prefetch_stall_ratio":        0.21,
		"node_cache_hit_ratio":                        0.59,
	})

	assert.Greater(t, score, 5.0)
	assert.Greater(t, signals["metadata_latency_p99_ms"], 0.0)
	assert.Greater(t, signals["small_io_ratio"], 0.0)
	assert.Greater(t, signals["dataloader_prefetch_stall_ratio"], 0.0)
	assert.Less(t, signals["cache_hit_ratio"], 0.7)
	assert.Contains(t, strings.Join(factors, " "), "Metadata path latency is elevated")
	assert.Contains(t, strings.Join(factors, " "), "prefetch stalls")
	assert.Contains(t, strings.Join(factors, " "), "Cache hit ratio is low")
}
