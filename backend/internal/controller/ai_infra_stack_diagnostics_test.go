package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/orchestration"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleAIInfraStackDiagnosticsMethodGuard(t *testing.T) {
	ctrl := &Controller{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/ai-infra-stack", nil)
	w := httptest.NewRecorder()
	ctrl.handleAIInfraStackDiagnostics(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestBuildAIInfraStackDiagnosticsReturnsLayeredModel(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 82},
		{Name: "node_memory_MemTotal_bytes", Value: 256 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 232 * 1024 * 1024 * 1024},
		{Name: "node_memory_Dirty_bytes", Value: 2 * 1024 * 1024 * 1024},
		{Name: "node_memory_Writeback_bytes", Value: 384 * 1024 * 1024},
		{Name: "node_pressure_cpu_some_avg10", Value: 14},
		{Name: "node_pressure_io_full_avg10", Value: 12},
		{Name: "node_procs_running", Value: 18},
		{Name: "node_procs_blocked", Value: 5},
		{Name: "node_cpu_iowait_percent", Value: 16},
		{Name: "node_context_switches_total", Value: 1.6e8},
		{Name: "node_schedstat_running_seconds_total", Value: 42000},
		{Name: "node_schedstat_waiting_seconds_total", Value: 16000},
		{Name: "node_gpu_utilization_sm_avg_percent", Value: 41},
		{Name: "node_network_utilization_peak_percent", Value: 91},
		{Name: "node_tcp_retransmit_ratio", Value: 0.024},
		{Name: "node_softnet_dropped_per_second", Value: 96},
		{Name: "node_network_interface_tx_queue_fill_percent", Value: 78},
		{Name: "node_rdma_congestion_events_per_second", Value: 33},
		{Name: "node_disk_utilization_peak_percent", Value: 92},
		{Name: "node_disk_queue_depth_total", Value: 85},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.052},
		{Name: "node_nvme_utilization_peak_percent", Value: 88},
		{Name: "node_nvme_avg_request_latency_seconds", Value: 0.039},
		{Name: "node_storage_small_io_ratio", Value: 0.42},
		{Name: "node_object_storage_get_latency_p99_seconds", Value: 0.082},
		{Name: "node_object_storage_put_latency_p99_seconds", Value: 0.114},
		{Name: "node_checkpoint_write_latency_p99_seconds", Value: 0.226},
		{Name: "node_dataloader_prefetch_stall_ratio", Value: 0.24},
		{Name: "node_cache_hit_ratio", Value: 0.58},
	}, now)

	resp := ctrl.buildAIInfraStackDiagnostics("", "", "", "", 50)

	require.Len(t, resp.Layers, 8)
	assert.Equal(t, 8, resp.Summary.LayerCount)
	assert.Equal(t, 1, resp.Summary.NodeCount)
	assert.GreaterOrEqual(t, resp.Summary.CoveragePercent, 0.0)
	assert.LessOrEqual(t, resp.Summary.CoveragePercent, 100.0)
	assert.NotEmpty(t, resp.Summary.TopLayerID)
	assert.NotEmpty(t, resp.Summary.TopLayerTitle)
	assert.Equal(t, len(resp.IncidentDrilldown), resp.Summary.IncidentDrilldowns)
	assert.GreaterOrEqual(t, resp.Summary.MeasuredCount, 1)
	assert.GreaterOrEqual(t, resp.Summary.MethodDirectCount, 1)

	layerIDs := make([]string, 0, len(resp.Layers))
	for _, layer := range resp.Layers {
		layerIDs = append(layerIDs, layer.ID)
		assert.NotEmpty(t, layer.Severity)
		assert.GreaterOrEqual(t, layer.CoveragePercent, 0.0)
		assert.LessOrEqual(t, layer.CoveragePercent, 100.0)
		for _, measurement := range layer.Measurements {
			assert.NotEmpty(t, measurement.Method, "measurement method should be classified: %s", measurement.Name)
		}
	}
	assert.Equal(t, []string{
		"compute_virtualization",
		"orchestration_runtime",
		"communication_fabric",
		"memory_hierarchy",
		"data_pipeline",
		"execution_optimization",
		"reliability_sre",
		"serving_inference",
	}, layerIDs)

	compute := findAIInfraLayer(resp.Layers, "compute_virtualization")
	require.NotNil(t, compute)
	assert.NotEmpty(t, compute.Measurements)

	hasTPUGap := false
	for _, measurement := range compute.Measurements {
		if measurement.Name == "TPU/NPU utilization" {
			hasTPUGap = true
			assert.Equal(t, aiInfraMeasurementMissing, measurement.Status)
		}
	}
	assert.True(t, hasTPUGap)

	communication := findAIInfraLayer(resp.Layers, "communication_fabric")
	require.NotNil(t, communication)
	assert.NotEmpty(t, communication.Domains)
	domainIDs := make([]string, 0, len(communication.Domains))
	for _, domain := range communication.Domains {
		domainIDs = append(domainIDs, domain.ID)
	}
	assert.Contains(t, domainIDs, "in_node_interconnect")
	assert.Contains(t, domainIDs, "inter_node_fabric")
	assert.Contains(t, domainIDs, "collective_runtime")

	memory := findAIInfraLayer(resp.Layers, "memory_hierarchy")
	require.NotNil(t, memory)
	assert.NotEmpty(t, memory.Domains)

	reliability := findAIInfraLayer(resp.Layers, "reliability_sre")
	require.NotNil(t, reliability)
	assert.NotEmpty(t, reliability.Domains)
	reliabilityDomainIDs := make([]string, 0, len(reliability.Domains))
	for _, domain := range reliability.Domains {
		reliabilityDomainIDs = append(reliabilityDomainIDs, domain.ID)
	}
	assert.Contains(t, reliabilityDomainIDs, "sli_error_budget")
	assert.Contains(t, reliabilityDomainIDs, "fault_tolerance_recovery")
	assert.Contains(t, reliabilityDomainIDs, "incident_lifecycle_rca")

	serving := findAIInfraLayer(resp.Layers, "serving_inference")
	require.NotNil(t, serving)
	assert.NotEmpty(t, serving.Domains)
	servingDomainIDs := make([]string, 0, len(serving.Domains))
	for _, domain := range serving.Domains {
		servingDomainIDs = append(servingDomainIDs, domain.ID)
	}
	assert.Contains(t, servingDomainIDs, "queueing_tail_latency")
	assert.Contains(t, servingDomainIDs, "batching_model_placement")
	assert.Contains(t, servingDomainIDs, "kv_cache_pressure")
}

func TestHandleAIInfraStackDiagnosticsJSONResponse(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-time.Minute).Truncate(time.Second)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 33},
		{Name: "node_network_utilization_peak_percent", Value: 41},
		{Name: "node_disk_utilization_peak_percent", Value: 46},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/ai-infra-stack?collector_id=collector-a&workload_limit=999", nil)
	w := httptest.NewRecorder()
	ctrl.handleAIInfraStackDiagnostics(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp aiInfraStackDiagnosticsResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "collector-a", resp.CollectorID)
	assert.Equal(t, 8, len(resp.Layers))
	assert.Equal(t, 8, resp.Summary.LayerCount)
	assert.Equal(t, len(resp.IncidentDrilldown), resp.Summary.IncidentDrilldowns)
}

func TestAIInfraCommunicationLayerUsesProcessSocketSignals(t *testing.T) {
	ctx := aiInfraSynthesisContext{
		Nodes: []*ingest.NodeSnapshot{
			{CollectorID: "collector-a", Hostname: "node-a"},
		},
		DataPath: dataPathDiagnosticsResponse{
			Summary: dataPathDiagnosticsSummary{
				NetworkCritical: 1,
			},
			Network: dataPathResourceDiagnostics{
				Rankings: []resourcePressureRow{
					{
						CollectorID: "collector-a",
						Hostname:    "node-a",
						Score:       7.2,
						Severity:    "critical",
						Signals: map[string]float64{
							"rdma_congestion_per_second": 21,
							"tcp_retransmit_ratio":       0.028,
							"tx_queue_fill_percent":      82,
						},
						Factors: []string{"rdma queue pressure"},
					},
				},
			},
		},
		WorkloadPath: workloadPathDiagnosticsResponse{
			Summary: workloadPathDiagnosticsSummary{
				WorkloadCount:                   1,
				CommunicationImbalanceWorkloads: 1,
			},
			Workloads: []workloadPathDiagnosticsWorkload{
				{
					Namespace:    "ml",
					Name:         "trainer-a",
					Severity:     "critical",
					NetworkScore: 6.8,
					Risks: []string{
						"communication_imbalance",
					},
				},
			},
		},
		TopPrograms: []ProgramStats{
			{
				CollectorID:       "collector-a",
				Hostname:          "node-a",
				PID:               "101",
				Name:              "trainer-worker",
				CommPattern:       "nccl",
				NetBytesPerSecond: 120 * 1024 * 1024,
				NetQueuedBytes:    8 * 1024 * 1024,
				NetConnections:    128,
				SchedWaitRatio:    0.9,
				Categories:        []string{"network"},
				Score:             7.5,
			},
		},
	}

	layer := aiInfraCommunicationLayer(ctx)
	require.Equal(t, "communication_fabric", layer.ID)
	require.NotEmpty(t, layer.Measurements)
	require.NotEmpty(t, layer.RankedEntities)

	socketMeasurement := findAIInfraMeasurement(layer, "Per-process socket queue attribution")
	require.NotNil(t, socketMeasurement)
	assert.Equal(t, aiInfraMeasurementMeasured, socketMeasurement.Status)
	assert.Equal(t, aiInfraMethodDirect, socketMeasurement.Method)

	collectiveSchedMeasurement := findAIInfraMeasurement(layer, "Collective-worker scheduler wait")
	require.NotNil(t, collectiveSchedMeasurement)
	assert.Equal(t, aiInfraMeasurementMeasured, collectiveSchedMeasurement.Status)
	assert.Equal(t, aiInfraMethodDirect, collectiveSchedMeasurement.Method)

	assert.Greater(t, layer.Signals["process_queue_hotspots"], 0.0)
	assert.Greater(t, layer.Signals["collective_sched_wait_hotspots"], 0.0)
	assert.Contains(t, layer.TopRisks, "Per-process socket queue backlog is elevated in communication-active workers.")
	assert.Contains(t, layer.TopRisks, "Collective workers show high scheduler wait ratio, suggesting sync or CPU-runqueue contention.")
}

func TestAIInfraBuildIncidentDrilldownsMapsWorkloadAndPlacement(t *testing.T) {
	now := time.Now().Add(-2 * time.Minute)
	ctx := aiInfraSynthesisContext{
		RootCause: rootCauseDiagnosticsResponse{
			Findings: []rootCauseFinding{
				{
					ID:         "network_congestion_training_slowdown",
					Category:   "network",
					Severity:   "critical",
					Confidence: 0.9,
					Title:      "Network congestion is throttling inter-node communication",
					AffectedNodes: []rootCauseNode{
						{CollectorID: "collector-a", Hostname: "node-a"},
					},
					Evidence: []rootCauseEvidence{
						{
							CollectorID: "collector-a",
							Hostname:    "node-a",
							Signal:      "tcp_retransmit_ratio",
							Value:       0.03,
							Source:      "/proc/net/snmp",
						},
					},
					Actions: []string{"Check congestion domains."},
				},
			},
		},
		WorkloadPath: workloadPathDiagnosticsResponse{
			Workloads: []workloadPathDiagnosticsWorkload{
				{
					Cluster:       "cluster-a",
					Namespace:     "ml",
					Kind:          "StatefulSet",
					Name:          "trainer-a",
					Service:       "trainer",
					Severity:      "critical",
					Bottleneck:    "network",
					NodeCount:     2,
					ResolvedNodes: 1,
					GPURequests:   16,
					Risks:         []string{"communication_imbalance", "cross_node_spread"},
					Nodes: []workloadPathNode{
						{
							NodeName:     "k8s-node-a",
							CollectorID:  "collector-a",
							Hostname:     "node-a",
							OverallScore: 7.3,
							Severity:     "critical",
							Signals: map[string]float64{
								"rdma_congestion_per_second": 42,
							},
							Reasons: []string{"queue pressure"},
						},
					},
					Reasons: []string{"network bottleneck"},
				},
			},
		},
		OrchestrationSnapshot: &orchestration.Snapshot{
			GeneratedAt: now,
			Workloads: []orchestration.Workload{
				{
					Spec: orchestration.WorkloadSpec{
						ID:      "ml-trainer-a",
						Service: "trainer",
						Requested: orchestration.ResourceVector{
							GPUCards: 16,
						},
					},
					State:             orchestration.WorkloadStateRunning,
					QueueDelaySeconds: 12.5,
					Reason:            "running",
					Assignments: []orchestration.Assignment{
						{
							WorkloadID:         "ml-trainer-a",
							NodeID:             "k8s-node-a",
							Cluster:            "cluster-a",
							Zone:               "zone-a",
							EstimatedLatencyMs: 35,
							Reason:             "best-fit placement",
							Reserved: orchestration.ResourceVector{
								GPUCards:    8,
								NetworkMbps: 120000,
							},
						},
					},
				},
			},
		},
	}

	drilldowns := aiInfraBuildIncidentDrilldowns(ctx, 4)
	require.Len(t, drilldowns, 1)
	drilldown := drilldowns[0]
	assert.Equal(t, "network_congestion_training_slowdown", drilldown.FindingID)
	require.NotEmpty(t, drilldown.Workloads)
	assert.Equal(t, "trainer-a", drilldown.Workloads[0].Name)
	require.NotEmpty(t, drilldown.Placements)
	assert.Equal(t, "collector-a", drilldown.Placements[0].CollectorID)
	assert.Equal(t, "k8s-node-a", drilldown.Placements[0].NodeID)
	require.NotEmpty(t, drilldown.Contention)
	assert.Equal(t, "tcp_retransmit_ratio", drilldown.Contention[0].Name)
}

func findAIInfraLayer(layers []aiInfraLayerDiagnostics, id string) *aiInfraLayerDiagnostics {
	for i := range layers {
		if layers[i].ID == id {
			return &layers[i]
		}
	}
	return nil
}

func findAIInfraMeasurement(layer aiInfraLayerDiagnostics, name string) *aiInfraLayerMeasurement {
	for i := range layer.Measurements {
		if layer.Measurements[i].Name == name {
			return &layer.Measurements[i]
		}
	}
	return nil
}

func TestAIInfraStatusFromCoverage(t *testing.T) {
	assert.Equal(t, aiInfraMeasurementMissing, aiInfraStatusFromCoverage(0, 10))
	assert.Equal(t, aiInfraMeasurementPartial, aiInfraStatusFromCoverage(4, 10))
	assert.Equal(t, aiInfraMeasurementMeasured, aiInfraStatusFromCoverage(10, 10))
	assert.Equal(t, aiInfraMeasurementMeasured, aiInfraStatusFromCoverage(1, 0))
	assert.Equal(t, aiInfraMeasurementMissing, aiInfraStatusFromCoverage(0, 0))
}

func TestAIInfraJainFairnessIndex(t *testing.T) {
	assert.InDelta(t, 1.0, aiInfraJainFairnessIndex([]float64{10, 10, 10}), 1e-9)
	assert.Less(t, aiInfraJainFairnessIndex([]float64{30, 0, 0}), 0.5)
	assert.Equal(t, 0.0, aiInfraJainFairnessIndex(nil))
}

func TestAIInfraTopShare(t *testing.T) {
	assert.InDelta(t, 0.7, aiInfraTopShare([]float64{7, 3}), 1e-9)
	assert.InDelta(t, 1.0, aiInfraTopShare([]float64{9}), 1e-9)
	assert.Equal(t, 0.0, aiInfraTopShare(nil))
}

func TestAIInfraInferMeasurementMethod(t *testing.T) {
	assert.Equal(t, aiInfraMethodMissing, aiInfraInferMeasurementMethod(aiInfraLayerMeasurement{
		Status: aiInfraMeasurementMissing,
	}))
	assert.Equal(t, aiInfraMethodProxy, aiInfraInferMeasurementMethod(aiInfraLayerMeasurement{
		Status: aiInfraMeasurementPartial,
		Metric: "runtime tenant share counters",
		Source: "not integrated",
	}))
	assert.Equal(t, aiInfraMethodDerived, aiInfraInferMeasurementMethod(aiInfraLayerMeasurement{
		Status: aiInfraMeasurementMeasured,
		Metric: "derived from latency_compliance_sli",
		Source: "/api/v1/orchestration/diagnostics",
	}))
	assert.Equal(t, aiInfraMethodDirect, aiInfraInferMeasurementMethod(aiInfraLayerMeasurement{
		Status: aiInfraMeasurementMeasured,
		Metric: "node_rdma_congestion_events_per_second",
		Source: "/sys/class/infiniband/*/ports/*/hw_counters",
	}))
}

func TestAIInfraFindingDomainCollectiveRuntime(t *testing.T) {
	assert.Equal(t, "collective", aiInfraFindingDomain(rootCauseFinding{
		ID:       "collective_runtime_queueing_contention",
		Category: "collective_runtime",
	}))
	assert.Equal(t, "collective", aiInfraFindingDomain(rootCauseFinding{
		ID:       "nccl_allreduce_sync_cost",
		Category: "",
	}))
}

func TestAIInfraMatchWorkloadsForFindingCollectiveRuntime(t *testing.T) {
	finding := rootCauseFinding{
		ID:       "collective_runtime_queueing_contention",
		Category: "collective_runtime",
		AffectedNodes: []rootCauseNode{
			{CollectorID: "collector-a", Hostname: "node-a"},
		},
	}
	workloads := []workloadPathDiagnosticsWorkload{
		{
			Cluster:      "cluster-a",
			Namespace:    "ml",
			Kind:         "StatefulSet",
			Name:         "trainer-a",
			Severity:     "critical",
			Bottleneck:   "network",
			ComputeScore: 2.8,
			NetworkScore: 7.1,
			StorageScore: 2.4,
			GPURequests:  8,
			Risks: []string{
				"communication_imbalance",
				"cross_node_spread",
				"scheduler_contention",
			},
			Nodes: []workloadPathNode{
				{CollectorID: "collector-a", Hostname: "node-a"},
			},
		},
		{
			Cluster:      "cluster-a",
			Namespace:    "ml",
			Kind:         "StatefulSet",
			Name:         "trainer-b",
			Severity:     "degraded",
			Bottleneck:   "storage",
			ComputeScore: 2.2,
			NetworkScore: 1.5,
			StorageScore: 5.9,
			GPURequests:  8,
			Risks: []string{
				"storage_collapse_iowait",
			},
			Nodes: []workloadPathNode{
				{CollectorID: "collector-b", Hostname: "node-b"},
			},
		},
	}

	matches := aiInfraMatchWorkloadsForFinding(finding, workloads)
	require.NotEmpty(t, matches)
	assert.Equal(t, "trainer-a", matches[0].Workload.Name)
	assert.Contains(t, matches[0].Reason, "collective communication imbalance")
	assert.Contains(t, matches[0].Reason, "affected-node overlap")
}
