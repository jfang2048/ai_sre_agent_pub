package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
)

func TestHandleRootCauseDiagnosticsGeneratesCrossLayerFindings(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-10 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)

	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 62},
		{Name: "node_cpu_iowait_percent", Value: 19},
		{Name: "node_load1", Value: 26},
		{Name: "node_procs_running", Value: 28},
		{Name: "node_procs_blocked", Value: 7},
		{Name: "node_pressure_cpu_some_avg10", Value: 24},
		{Name: "node_pressure_io_some_avg10", Value: 11},
		{Name: "node_memory_MemTotal_bytes", Value: 256 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 220 * 1024 * 1024 * 1024},
		{Name: "node_gpu_utilization_sm_avg_percent", Value: 42},
		{Name: "node_network_utilization_peak_percent", Value: 95},
		{Name: "node_tcp_retransmit_ratio", Value: 0.03},
		{Name: "node_tcp_retransmits_per_second", Value: 900},
		{Name: "node_softnet_dropped_per_second", Value: 120},
		{Name: "node_network_interface_tx_queue_fill_percent", Value: 82},
		{Name: "node_rdma_congestion_events_per_second", Value: 45},
		{Name: "node_rdma_pfc_pause_frames_per_second", Value: 540},
		{Name: "node_rdma_ecn_marked_ratio", Value: 0.02},
		{Name: "node_rdma_port_transmit_bytes_per_second", Value: 2.4e10},
		{Name: "node_rdma_port_receive_bytes_per_second", Value: 8.0e9},
		{Name: "node_disk_utilization_peak_percent", Value: 92},
		{Name: "node_disk_queue_depth_total", Value: 92},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.055},
		{Name: "node_pressure_io_full_avg10", Value: 15},
		{Name: "node_checkpoint_write_latency_p99_seconds", Value: 0.22},
		{Name: "node_dataloader_prefetch_stall_ratio", Value: 0.26},
		{Name: "node_cache_hit_ratio", Value: 0.56},
		{Name: "node_memory_Dirty_bytes", Value: 2 * 1024 * 1024 * 1024},
		{Name: "node_memory_Writeback_bytes", Value: 320 * 1024 * 1024},
		{Name: "node_vmstat_pgpgout_per_second", Value: 160000},
		{Name: "node_vmstat_pswpout_per_second", Value: 20},
		{Name: "collector_probe_core_client_available", Value: 1},
		{Name: "collector_probe_core_active", Value: 0},
		{Name: "collector_probe_core_fresh", Value: 0},
		{Name: "collector_probe_core_collector_selection_valid", Value: 0},
		{Name: "collector_probe_core_last_frame_age_seconds", Value: 120},
		{Name: "collector_probe_core_decode_errors_total", Value: 9},
		{Name: "collector_probe_core_crc_failures_total", Value: 4},
		{Name: "collector_probe_core_restarts_total", Value: 2},
		{
			Name:  "rca_net_process_queued_bytes",
			Value: 16 * 1024 * 1024,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "4321"},
				{Key: "process", Value: "trainer-worker"},
				{Key: "comm_pattern", Value: "nccl"},
			},
		},
		{
			Name:  "rca_net_process_connections",
			Value: 144,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "4321"},
				{Key: "process", Value: "trainer-worker"},
				{Key: "comm_pattern", Value: "nccl"},
			},
		},
		{
			Name:  "rca_cpu_process_sched_wait_ratio",
			Value: 1.18,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "4321"},
				{Key: "process", Value: "trainer-worker"},
				{Key: "comm_pattern", Value: "nccl"},
			},
		},
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

	store.StoreMetrics("collector-b", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 45},
		{Name: "node_memory_MemTotal_bytes", Value: 256 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 120 * 1024 * 1024 * 1024},
		{Name: "node_network_utilization_peak_percent", Value: 28},
		{Name: "node_rdma_port_transmit_bytes_per_second", Value: 1.2e9},
		{Name: "node_rdma_port_receive_bytes_per_second", Value: 1.1e9},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.002},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/root-cause", nil)
	w := httptest.NewRecorder()
	ctrl.handleRootCauseDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp rootCauseDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, 2, resp.Summary.NodeCount)
			assert.NotEmpty(t, resp.Findings)
			assert.NotEmpty(t, resp.Summary.TopFindingID)
			assert.NotEmpty(t, resp.Summary.TopFindingSummary)
			assert.Greater(t, resp.DataPath.TotalAnomalies, -1)
			assert.Greater(t, resp.DataPath.NetworkCritical, -1)
			assert.Greater(t, resp.DataPath.StorageCritical, -1)

			findingByID := make(map[string]rootCauseFinding, len(resp.Findings))
			for _, finding := range resp.Findings {
				findingByID[finding.ID] = finding
				assert.NotEmpty(t, finding.Hypothesis)
				assert.NotEmpty(t, finding.Actions)
				assert.NotEmpty(t, finding.Evidence)
			}

			assert.Contains(t, findingByID, "network_congestion_training_slowdown")
			assert.Contains(t, findingByID, "storage_latency_gpu_starvation")
			assert.Contains(t, findingByID, "scheduler_contention_tail_latency")
			assert.Contains(t, findingByID, "memory_pressure_io_amplification")
			assert.Contains(t, findingByID, "probe_core_runtime_path_degraded")
			assert.Contains(t, findingByID, "collective_runtime_queueing_contention")
			assert.Contains(t, findingByID, "cross_node_communication_imbalance")
		}
	}
}

func TestHandleRootCauseDiagnosticsDetectsSchedulerContention(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-5 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 88},
		{Name: "node_cpu_iowait_percent", Value: 21},
		{Name: "node_load1", Value: 27},
		{Name: "node_procs_running", Value: 30},
		{Name: "node_procs_blocked", Value: 8},
		{Name: "node_pressure_cpu_some_avg10", Value: 26},
		{Name: "node_pressure_io_some_avg10", Value: 13},
		{Name: "node_memory_MemTotal_bytes", Value: 128 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 92 * 1024 * 1024 * 1024},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/root-cause?collector_id=collector-a", nil)
	w := httptest.NewRecorder()
	ctrl.handleRootCauseDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp rootCauseDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			findingByID := make(map[string]rootCauseFinding, len(resp.Findings))
			for _, finding := range resp.Findings {
				findingByID[finding.ID] = finding
			}
			finding, ok := findingByID["scheduler_contention_tail_latency"]
			if assert.True(t, ok) {
				assert.NotEmpty(t, finding.Evidence)
				assert.NotEmpty(t, finding.AffectedNodes)
				assert.Contains(t, finding.CorrelatedSignal, "node_cpu_iowait_percent")
				assert.Contains(t, finding.CorrelatedSignal, "node_pressure_cpu_some_avg10")
			}
		}
	}
}

func TestHandleRootCauseDiagnosticsCollectorFilter(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-5 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-b", Hostname: "node-b"}, now)

	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_network_utilization_peak_percent", Value: 92},
		{Name: "node_tcp_retransmit_ratio", Value: 0.02},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.04},
		{Name: "node_pressure_io_full_avg10", Value: 8},
		{Name: "node_memory_MemTotal_bytes", Value: 64 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 44 * 1024 * 1024 * 1024},
	}, now)

	store.StoreMetrics("collector-b", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 20},
		{Name: "node_memory_MemTotal_bytes", Value: 64 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 16 * 1024 * 1024 * 1024},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.002},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/root-cause?collector_id=collector-b", nil)
	w := httptest.NewRecorder()
	ctrl.handleRootCauseDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp rootCauseDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-b", resp.CollectorID)
			assert.Equal(t, 1, resp.Summary.NodeCount)
			for _, finding := range resp.Findings {
				for _, node := range finding.AffectedNodes {
					assert.Equal(t, "collector-b", node.CollectorID)
				}
				for _, evidence := range finding.Evidence {
					assert.Equal(t, "collector-b", evidence.CollectorID)
				}
			}
		}
	}
}

func TestHandleRootCauseDiagnosticsWithoutIngestStore(t *testing.T) {
	ctrl := &Controller{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/root-cause", nil)
	w := httptest.NewRecorder()

	ctrl.handleRootCauseDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp rootCauseDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Zero(t, resp.Summary.NodeCount)
			assert.Empty(t, resp.Findings)
		}
	}
}

func TestHandleRootCauseDiagnosticsRejectsUnsupportedMethod(t *testing.T) {
	ctrl := &Controller{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/root-cause", nil)
	w := httptest.NewRecorder()

	ctrl.handleRootCauseDiagnostics(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestInferCollectiveRuntimeContentionFinding(t *testing.T) {
	topPrograms := []ProgramStats{
		{
			CollectorID:       "collector-a",
			Hostname:          "node-a",
			PID:               "1001",
			Name:              "trainer-worker",
			CommPattern:       "nccl",
			NetQueuedBytes:    12 * 1024 * 1024,
			NetConnections:    128,
			SchedWaitRatio:    0.95,
			NetBytesPerSecond: 150 * 1024 * 1024,
		},
		{
			CollectorID:    "collector-b",
			Hostname:       "node-b",
			PID:            "1002",
			Name:           "trainer-helper",
			CommPattern:    "ucx",
			NetQueuedBytes: 3 * 1024 * 1024,
			NetConnections: 72,
			SchedWaitRatio: 0.62,
		},
		{
			CollectorID:    "collector-c",
			Hostname:       "node-c",
			PID:            "3001",
			Name:           "web-api",
			CommPattern:    "http",
			NetQueuedBytes: 18 * 1024 * 1024,
			NetConnections: 200,
			SchedWaitRatio: 1.2,
		},
	}
	dataPath := dataPathDiagnosticsResponse{
		Summary: dataPathDiagnosticsSummary{
			NetworkCritical: 1,
			NetworkDegraded: 1,
		},
		Network: dataPathResourceDiagnostics{
			Rankings: []resourcePressureRow{
				{
					CollectorID: "collector-a",
					Hostname:    "node-a",
					Signals: map[string]float64{
						"tcp_retransmit_ratio":       0.03,
						"tx_queue_fill_percent":      78,
						"rdma_congestion_per_second": 30,
					},
				},
				{
					CollectorID: "collector-b",
					Hostname:    "node-b",
					Signals: map[string]float64{
						"tcp_retransmit_ratio":  0.015,
						"tx_queue_fill_percent": 64,
					},
				},
			},
		},
	}

	finding, ok := inferCollectiveRuntimeContentionFinding(topPrograms, dataPath)
	if assert.True(t, ok) {
		assert.Equal(t, "collective_runtime_queueing_contention", finding.ID)
		assert.Equal(t, "collective_runtime", finding.Category)
		assert.NotEmpty(t, finding.AffectedNodes)
		assert.NotEmpty(t, finding.Evidence)
		assert.Contains(t, finding.CorrelatedSignal, "rca_net_process_queued_bytes")
		assert.Contains(t, finding.CorrelatedSignal, "rca_cpu_process_sched_wait_ratio")
		assert.Equal(t, "/proc/net/tcp + /proc/*/fd", diagnosticMetricSource("rca_net_process_queued_bytes"))
		assert.Equal(t, "/proc/[pid]/schedstat", diagnosticMetricSource("rca_cpu_process_sched_wait_ratio"))
	}
}
