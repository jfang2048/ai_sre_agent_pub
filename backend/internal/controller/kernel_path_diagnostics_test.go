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

func TestHandleKernelPathDiagnosticsStorageBottleneckAndSources(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-3 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)

	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_storage_small_io_ratio", Value: 0.48},
		{Name: "node_storage_metadata_ops_per_second", Value: 26000},
		{Name: "node_storage_metadata_latency_p99_seconds", Value: 0.030},
		{Name: "node_cpu_iowait_percent", Value: 14},
		{Name: "node_memory_Dirty_bytes", Value: 3 * 1024 * 1024 * 1024},
		{Name: "node_memory_Writeback_bytes", Value: 700 * 1024 * 1024},
		{Name: "node_pressure_io_full_avg10", Value: 14},
		{Name: "node_vmstat_pgpgout_per_second", Value: 250000},
		{Name: "node_vmstat_nr_dirtied_per_second", Value: 220000},
		{Name: "node_vmstat_nr_written_per_second", Value: 170000},
		{Name: "node_disk_queue_depth_total", Value: 90},
		{Name: "node_disk_queue_depth_fill_percent", Value: 75},
		{Name: "node_disk_utilization_peak_percent", Value: 95},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.080},
		{Name: "node_nvme_queue_depth_total", Value: 64},
		{Name: "node_nvme_utilization_peak_percent", Value: 92},
		{Name: "node_nvme_avg_request_latency_seconds", Value: 0.040},
		{Name: "node_object_storage_get_latency_p99_seconds", Value: 0.090},
		{Name: "node_object_storage_put_latency_p99_seconds", Value: 0.100},
		{Name: "node_checkpoint_write_latency_p99_seconds", Value: 0.220},
		{Name: "node_dataloader_prefetch_stall_ratio", Value: 0.22},
		{Name: "node_cache_hit_ratio", Value: 0.55},
	}, now)

	store.StoreMetrics("collector-b", []*telemetryv1.Metric{
		{Name: "node_storage_small_io_ratio", Value: 0.08},
		{Name: "node_disk_queue_depth_total", Value: 4},
		{Name: "node_disk_utilization_peak_percent", Value: 32},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.002},
		{Name: "node_network_utilization_peak_percent", Value: 20},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/kernel-path", nil)
	w := httptest.NewRecorder()
	ctrl.handleKernelPathDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp kernelPathDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, 2, resp.Summary.NodeCount)
			assert.GreaterOrEqual(t, resp.Summary.CriticalNodes, 1)
			assert.NotEmpty(t, resp.Summary.TopStorageStage)
			assert.True(t, strings.HasPrefix(resp.Summary.TopBottleneckKey, "storage:"))

			var nodeA *kernelPathNodeDiagnostics
			for i := range resp.Nodes {
				if resp.Nodes[i].CollectorID == "collector-a" {
					nodeA = &resp.Nodes[i]
					break
				}
			}
			if assert.NotNil(t, nodeA) {
				assert.Equal(t, "critical", nodeA.Storage.Severity)
				assert.NotEmpty(t, nodeA.Storage.TopStage)
				assert.Equal(t, "critical", nodeA.OverallSeverity)
				assert.NotEmpty(t, nodeA.Bottlenecks)

				foundTop := false
				foundPageCache := false
				for _, stage := range nodeA.Storage.Stages {
					if stage.Name == nodeA.Storage.TopStage {
						foundTop = true
						assert.NotEmpty(t, stage.Signals)
						assert.NotEmpty(t, stage.Sources)
					}
					if stage.Name == "page_cache_writeback" {
						foundPageCache = true
						assert.InDelta(t, 14.0, stage.Signals["cpu_iowait_percent"], 0.001)
						assert.NotEmpty(t, stage.Sources["cpu_iowait_percent"])
					}
				}
				assert.True(t, foundTop, "expected top storage stage details")
				assert.True(t, foundPageCache, "expected page_cache_writeback stage details")
			}
		}
	}
}

func TestHandleKernelPathDiagnosticsCollectorFilterAndMethodGuard(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-2 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)

	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_network_utilization_peak_percent", Value: 18},
		{Name: "node_softnet_dropped_per_second", Value: 2},
	}, now)

	store.StoreMetrics("collector-b", []*telemetryv1.Metric{
		{Name: "node_network_utilization_peak_percent", Value: 88},
		{Name: "node_softnet_dropped_per_second", Value: 120},
		{Name: "node_softnet_times_squeezed_per_second", Value: 90},
		{Name: "node_tcp_retransmit_ratio", Value: 0.02},
		{Name: "node_tcp_retransmits_per_second", Value: 700},
		{Name: "node_network_interface_tx_queue_fill_percent", Value: 80},
		{Name: "node_rdma_congestion_events_per_second", Value: 50},
		{Name: "node_rdma_pfc_pause_frames_per_second", Value: 400},
		{Name: "node_rdma_ecn_marked_ratio", Value: 0.016},
		{Name: "node_rdma_port_transmit_bytes_per_second", Value: 1.8e10},
		{Name: "node_rdma_port_receive_bytes_per_second", Value: 7.0e9},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/kernel-path?collector_id=collector-b", nil)
	w := httptest.NewRecorder()
	ctrl.handleKernelPathDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp kernelPathDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-b", resp.CollectorID)
			assert.Equal(t, 1, resp.Summary.NodeCount)
			if assert.Len(t, resp.Nodes, 1) {
				assert.Equal(t, "collector-b", resp.Nodes[0].CollectorID)
				assert.NotEmpty(t, resp.Nodes[0].Network.TopStage)
				assert.NotEmpty(t, resp.Nodes[0].Network.Stages)
			}
		}
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/kernel-path", nil)
	postW := httptest.NewRecorder()
	ctrl.handleKernelPathDiagnostics(postW, postReq)
	assert.Equal(t, http.StatusMethodNotAllowed, postW.Code)
}
