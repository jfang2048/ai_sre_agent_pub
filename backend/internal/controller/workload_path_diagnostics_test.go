package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/k8sview"
	"github.com/stretchr/testify/assert"
)

func TestBuildWorkloadPathDiagnosticsRanksCrossLayerRisks(t *testing.T) {
	now := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	clusterSnapshots := []k8sview.ClusterSnapshot{
		{
			Name: "cluster-a",
			Nodes: []k8sview.NodeSummary{
				{
					Name:    "k8s-node-a",
					Cluster: "cluster-a",
					Observed: k8sview.ObservedSignals{
						CollectorID: "collector-a",
						Hostname:    "node-a",
					},
				},
				{
					Name:    "k8s-node-b",
					Cluster: "cluster-a",
					Observed: k8sview.ObservedSignals{
						CollectorID: "collector-b",
						Hostname:    "node-b",
					},
				},
			},
			Workloads: []k8sview.WorkloadSummary{
				{
					Cluster:           "cluster-a",
					Namespace:         "ml",
					Kind:              "StatefulSet",
					Name:              "trainer-a",
					Service:           "trainer",
					PodsTotal:         8,
					PodsRunning:       8,
					ContainerRestarts: 3,
					GPURequests:       16,
					GPULimits:         16,
					Nodes:             []string{"k8s-node-a", "k8s-node-b"},
					AvgNodeGPUPercent: 36,
				},
				{
					Cluster:           "cluster-a",
					Namespace:         "serving",
					Kind:              "Deployment",
					Name:              "inference-api",
					Service:           "inference-api",
					PodsTotal:         4,
					PodsRunning:       4,
					GPURequests:       0,
					Nodes:             []string{"k8s-node-b"},
					AvgNodeGPUPercent: 0,
				},
			},
		},
	}

	ingestSnapshots := []*ingest.NodeSnapshot{
		{
			CollectorID: "collector-a",
			Hostname:    "node-a",
			UpdatedAt:   now,
			Metrics: map[string]float64{
				"node_cpu_usage_percent":                       78,
				"node_memory_MemTotal_bytes":                   256 * 1024 * 1024 * 1024,
				"node_memory_Used_bytes":                       210 * 1024 * 1024 * 1024,
				"node_load1":                                   19,
				"node_gpu_utilization_sm_avg_percent":          34,
				"node_cpu_iowait_percent":                      17,
				"node_procs_blocked":                           5,
				"node_network_utilization_peak_percent":        94,
				"node_tcp_retransmit_ratio":                    0.025,
				"node_softnet_dropped_per_second":              140,
				"node_network_interface_tx_queue_fill_percent": 82,
				"node_rdma_congestion_events_per_second":       42,
				"node_rdma_port_transmit_bytes_per_second":     2.4e10,
				"node_rdma_port_receive_bytes_per_second":      6.0e9,
				"node_disk_utilization_peak_percent":           93,
				"node_disk_queue_depth_total":                  88,
				"node_disk_request_latency_p99_seconds":        0.062,
				"node_pressure_io_full_avg10":                  14,
				"node_dataloader_prefetch_stall_ratio":         0.22,
				"node_checkpoint_write_latency_p99_seconds":    0.240,
				"node_cache_hit_ratio":                         0.55,
				"node_memory_Dirty_bytes":                      2 * 1024 * 1024 * 1024,
				"node_memory_Writeback_bytes":                  384 * 1024 * 1024,
				"node_vmstat_pgpgout_per_second":               180000,
				"node_vmstat_nr_dirtied_per_second":            120000,
				"node_vmstat_nr_written_per_second":            80000,
			},
		},
		{
			CollectorID: "collector-b",
			Hostname:    "node-b",
			UpdatedAt:   now,
			Metrics: map[string]float64{
				"node_cpu_usage_percent":                   52,
				"node_memory_MemTotal_bytes":               256 * 1024 * 1024 * 1024,
				"node_memory_Used_bytes":                   140 * 1024 * 1024 * 1024,
				"node_load1":                               8,
				"node_gpu_utilization_sm_avg_percent":      41,
				"node_cpu_iowait_percent":                  8,
				"node_procs_blocked":                       2,
				"node_network_utilization_peak_percent":    48,
				"node_tcp_retransmit_ratio":                0.004,
				"node_softnet_dropped_per_second":          10,
				"node_rdma_port_transmit_bytes_per_second": 2.0e9,
				"node_rdma_port_receive_bytes_per_second":  1.0e9,
				"node_disk_utilization_peak_percent":       48,
				"node_disk_queue_depth_total":              12,
				"node_disk_request_latency_p99_seconds":    0.008,
				"node_pressure_io_full_avg10":              2,
			},
		},
	}

	resp := buildWorkloadPathDiagnostics("cluster-a", "", "", 20, clusterSnapshots, ingestSnapshots)
	assert.Equal(t, "cluster-a", resp.Cluster)
	assert.Equal(t, 2, resp.Summary.WorkloadCount)
	assert.GreaterOrEqual(t, resp.Summary.MultiNodeWorkloads, 1)
	assert.GreaterOrEqual(t, resp.Summary.TelemetryCoveredWorkloads, 1)

	if assert.NotEmpty(t, resp.Workloads) {
		top := resp.Workloads[0]
		assert.Equal(t, "trainer", top.Service)
		assert.Equal(t, 2, top.NodeCount)
		assert.Equal(t, 2, top.ResolvedNodes)
		assert.InDelta(t, 100.0, top.TelemetryCoveragePct, 0.001)
		assert.NotEmpty(t, top.Bottleneck)
		assert.NotEmpty(t, top.Nodes)
		assert.Contains(t, top.Risks, "gpu_starvation_due_to_io_or_network")
		assert.Contains(t, top.Risks, "communication_imbalance")
		assert.Contains(t, top.Risks, "cross_node_spread")
		assert.NotEmpty(t, top.TopNetworkStage)
		assert.NotEmpty(t, top.TopStorageStage)
		assert.NotEmpty(t, top.Signals)
		assert.NotEmpty(t, top.Sources)
	}
}

func TestBuildWorkloadPathDiagnosticsFilterAndLimit(t *testing.T) {
	clusterSnapshots := []k8sview.ClusterSnapshot{
		{
			Name: "cluster-a",
			Workloads: []k8sview.WorkloadSummary{
				{Cluster: "cluster-a", Namespace: "ml", Kind: "Deployment", Name: "svc-a", Service: "svc-a"},
				{Cluster: "cluster-a", Namespace: "ml", Kind: "Deployment", Name: "svc-b", Service: "svc-b"},
			},
		},
		{
			Name: "cluster-b",
			Workloads: []k8sview.WorkloadSummary{
				{Cluster: "cluster-b", Namespace: "ml", Kind: "Deployment", Name: "svc-c", Service: "svc-c"},
			},
		},
	}

	resp := buildWorkloadPathDiagnostics("cluster-a", "ml", "", 1, clusterSnapshots, nil)
	assert.Equal(t, 1, resp.Summary.WorkloadCount)
	if assert.Len(t, resp.Workloads, 1) {
		assert.Equal(t, "cluster-a", resp.Workloads[0].Cluster)
		assert.Equal(t, "ml", resp.Workloads[0].Namespace)
	}
}

func TestHandleWorkloadPathDiagnosticsMethodAndDisabledGuard(t *testing.T) {
	ctrl := &Controller{}

	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/workload-path", nil)
	postW := httptest.NewRecorder()
	ctrl.handleWorkloadPathDiagnostics(postW, postReq)
	assert.Equal(t, http.StatusMethodNotAllowed, postW.Code)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/workload-path", nil)
	getW := httptest.NewRecorder()
	ctrl.handleWorkloadPathDiagnostics(getW, getReq)
	assert.Equal(t, http.StatusServiceUnavailable, getW.Code)
}

func TestHandleWorkloadPathDiagnosticsEnabledNoData(t *testing.T) {
	ctrl := &Controller{
		k8sManager: k8sview.NewManager(k8sview.Config{Enabled: true}, nil, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/workload-path", nil)
	w := httptest.NewRecorder()
	ctrl.handleWorkloadPathDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp workloadPathDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Zero(t, resp.Summary.WorkloadCount)
			assert.Empty(t, resp.Workloads)
		}
	}
}
