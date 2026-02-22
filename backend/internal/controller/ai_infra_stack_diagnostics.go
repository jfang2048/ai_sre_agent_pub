package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/orchestration"
)

const (
	defaultAIInfraWorkloadLimit = 40
	maxAIInfraWorkloadLimit     = 200
	aiInfraRankLimit            = 8
)

const (
	aiInfraMeasurementMeasured = "measured"
	aiInfraMeasurementPartial  = "partial"
	aiInfraMeasurementMissing  = "missing"
)

const (
	aiInfraMethodDirect  = "direct"
	aiInfraMethodDerived = "derived"
	aiInfraMethodProxy   = "proxy"
	aiInfraMethodMissing = "missing"
)

type aiInfraStackDiagnosticsResponse struct {
	CollectorID       string                     `json:"collector_id,omitempty"`
	Cluster           string                     `json:"cluster,omitempty"`
	Namespace         string                     `json:"namespace,omitempty"`
	Service           string                     `json:"service,omitempty"`
	GeneratedAt       time.Time                  `json:"generated_at"`
	Summary           aiInfraStackSummary        `json:"summary"`
	Layers            []aiInfraLayerDiagnostics  `json:"layers"`
	WorkloadMappings  []aiInfraWorkloadMapping   `json:"workload_mappings,omitempty"`
	IncidentDrilldown []aiInfraIncidentDrilldown `json:"incident_drilldowns,omitempty"`
}

type aiInfraStackSummary struct {
	NodeCount          int     `json:"node_count"`
	WorkloadCount      int     `json:"workload_count"`
	LayerCount         int     `json:"layer_count"`
	CriticalLayers     int     `json:"critical_layers"`
	DegradedLayers     int     `json:"degraded_layers"`
	TopLayerID         string  `json:"top_layer_id,omitempty"`
	TopLayerTitle      string  `json:"top_layer_title,omitempty"`
	TopRisk            string  `json:"top_risk,omitempty"`
	CoveragePercent    float64 `json:"coverage_percent"`
	RootCauseFindings  int     `json:"root_cause_findings"`
	CriticalFindings   int     `json:"critical_findings"`
	DegradedFindings   int     `json:"degraded_findings"`
	CommunicationSkews int     `json:"communication_skews"`
	IncidentDrilldowns int     `json:"incident_drilldowns"`
	MeasuredCount      int     `json:"measurements_measured"`
	PartialCount       int     `json:"measurements_partial"`
	MissingCount       int     `json:"measurements_missing"`
	MethodDirectCount  int     `json:"methods_direct"`
	MethodDerivedCount int     `json:"methods_derived"`
	MethodProxyCount   int     `json:"methods_proxy"`
	MethodMissingCount int     `json:"methods_missing"`
}

type aiInfraLayerDiagnostics struct {
	ID               string                    `json:"id"`
	Title            string                    `json:"title"`
	Scope            string                    `json:"scope"`
	Score            float64                   `json:"score"`
	Severity         string                    `json:"severity"`
	CoveragePercent  float64                   `json:"coverage_percent"`
	Signals          map[string]float64        `json:"signals,omitempty"`
	TopRisks         []string                  `json:"top_risks,omitempty"`
	Sources          map[string]string         `json:"sources,omitempty"`
	Measurements     []aiInfraLayerMeasurement `json:"measurements,omitempty"`
	Domains          []aiInfraLayerDomain      `json:"domains,omitempty"`
	RankedEntities   []aiInfraRankedEntity     `json:"ranked_entities,omitempty"`
	Troubleshooting  []string                  `json:"troubleshooting,omitempty"`
	ObservabilityGap []string                  `json:"observability_gaps,omitempty"`
}

type aiInfraLayerMeasurement struct {
	Name   string `json:"name"`
	Metric string `json:"metric"`
	Source string `json:"source"`
	Status string `json:"status"`
	Method string `json:"method,omitempty"`
	Note   string `json:"note,omitempty"`
}

type aiInfraLayerDomain struct {
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	Score           float64            `json:"score"`
	Severity        string             `json:"severity"`
	CoveragePercent float64            `json:"coverage_percent"`
	Signals         map[string]float64 `json:"signals,omitempty"`
	Sources         map[string]string  `json:"sources,omitempty"`
	Notes           []string           `json:"notes,omitempty"`
}

type aiInfraRankedEntity struct {
	Kind     string  `json:"kind"`
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Score    float64 `json:"score"`
	Severity string  `json:"severity,omitempty"`
	Detail   string  `json:"detail,omitempty"`
}

type aiInfraWorkloadMapping struct {
	Cluster       string   `json:"cluster"`
	Namespace     string   `json:"namespace"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Service       string   `json:"service"`
	Path          string   `json:"path"`
	PodsRunning   int      `json:"pods_running"`
	PodsPending   int      `json:"pods_pending"`
	PodsFailed    int      `json:"pods_failed"`
	GPURequests   float64  `json:"gpu_requests,omitempty"`
	GPULimits     float64  `json:"gpu_limits,omitempty"`
	NodeCount     int      `json:"node_count"`
	ResolvedNodes int      `json:"resolved_nodes"`
	Nodes         []string `json:"nodes,omitempty"`
	RiskFlags     []string `json:"risk_flags,omitempty"`
	Bottleneck    string   `json:"bottleneck,omitempty"`
}

type aiInfraIncidentDrilldown struct {
	FindingID     string                        `json:"finding_id"`
	FindingTitle  string                        `json:"finding_title"`
	Category      string                        `json:"category"`
	Severity      string                        `json:"severity"`
	Confidence    float64                       `json:"confidence"`
	Workflow      string                        `json:"workflow"`
	AffectedNodes []string                      `json:"affected_nodes,omitempty"`
	Workloads     []aiInfraIncidentWorkloadHop  `json:"workloads,omitempty"`
	Placements    []aiInfraIncidentPlacementHop `json:"placements,omitempty"`
	Contention    []aiInfraIncidentSignal       `json:"contention,omitempty"`
	Triage        []string                      `json:"triage,omitempty"`
}

type aiInfraIncidentWorkloadHop struct {
	ID                string   `json:"id"`
	Cluster           string   `json:"cluster,omitempty"`
	Namespace         string   `json:"namespace,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	Name              string   `json:"name,omitempty"`
	Service           string   `json:"service,omitempty"`
	Severity          string   `json:"severity,omitempty"`
	Bottleneck        string   `json:"bottleneck,omitempty"`
	QueueDelaySeconds float64  `json:"queue_delay_seconds,omitempty"`
	PodsPending       int      `json:"pods_pending,omitempty"`
	PodsFailed        int      `json:"pods_failed,omitempty"`
	NodeCount         int      `json:"node_count,omitempty"`
	ResolvedNodes     int      `json:"resolved_nodes,omitempty"`
	GPURequests       float64  `json:"gpu_requests,omitempty"`
	Risks             []string `json:"risks,omitempty"`
	Reason            string   `json:"reason,omitempty"`
}

type aiInfraIncidentPlacementHop struct {
	WorkloadID        string             `json:"workload_id,omitempty"`
	NodeID            string             `json:"node_id,omitempty"`
	CollectorID       string             `json:"collector_id,omitempty"`
	Hostname          string             `json:"hostname,omitempty"`
	Cluster           string             `json:"cluster,omitempty"`
	Zone              string             `json:"zone,omitempty"`
	Score             float64            `json:"score"`
	Severity          string             `json:"severity,omitempty"`
	QueueDelaySeconds float64            `json:"queue_delay_seconds,omitempty"`
	Signals           map[string]float64 `json:"signals,omitempty"`
	Reason            string             `json:"reason,omitempty"`
}

type aiInfraIncidentSignal struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Source    string  `json:"source,omitempty"`
	Collector string  `json:"collector_id,omitempty"`
	Hostname  string  `json:"hostname,omitempty"`
}

type aiInfraSynthesisContext struct {
	CollectorID           string
	Cluster               string
	Namespace             string
	Service               string
	Nodes                 []*ingest.NodeSnapshot
	DataPath              dataPathDiagnosticsResponse
	KernelPath            kernelPathDiagnosticsResponse
	RootCause             rootCauseDiagnosticsResponse
	WorkloadPath          workloadPathDiagnosticsResponse
	TopPrograms           []ProgramStats
	GPUNodes              []*gpuobs.Node
	OrchestrationSnapshot *orchestration.Snapshot
	OrchestrationDiag     *orchestration.DiagnosticsSnapshot
}

func (c *Controller) handleAIInfraStackDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	collectorID := strings.TrimSpace(r.URL.Query().Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(r.URL.Query().Get("collector"))
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	workloadLimit := parseAIInfraWorkloadLimit(r.URL.Query().Get("workload_limit"))

	resp := c.buildAIInfraStackDiagnostics(collectorID, cluster, namespace, service, workloadLimit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func parseAIInfraWorkloadLimit(raw string) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultAIInfraWorkloadLimit
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return defaultAIInfraWorkloadLimit
	}
	if n > maxAIInfraWorkloadLimit {
		return maxAIInfraWorkloadLimit
	}
	return n
}

func (c *Controller) buildAIInfraStackDiagnostics(
	collectorID, cluster, namespace, service string,
	workloadLimit int,
) aiInfraStackDiagnosticsResponse {
	if workloadLimit <= 0 {
		workloadLimit = defaultAIInfraWorkloadLimit
	}
	if workloadLimit > maxAIInfraWorkloadLimit {
		workloadLimit = maxAIInfraWorkloadLimit
	}

	resp := aiInfraStackDiagnosticsResponse{
		CollectorID: collectorID,
		Cluster:     cluster,
		Namespace:   namespace,
		Service:     service,
		GeneratedAt: time.Now(),
		Summary:     aiInfraStackSummary{},
		Layers:      []aiInfraLayerDiagnostics{},
	}

	allNodes := []*ingest.NodeSnapshot{}
	if c.ingestStore != nil {
		allNodes = c.ingestStore.Snapshot()
	}
	nodes := filterIngestSnapshotsByCollector(allNodes, collectorID)

	workloadPath := workloadPathDiagnosticsResponse{
		Cluster:     cluster,
		Namespace:   namespace,
		Service:     service,
		GeneratedAt: time.Now(),
		Summary:     workloadPathDiagnosticsSummary{},
		Workloads:   []workloadPathDiagnosticsWorkload{},
	}
	if c.k8sManager != nil {
		workloadPath = buildWorkloadPathDiagnostics(cluster, namespace, service, workloadLimit, c.k8sManager.Snapshots(), nodes)
		resp.WorkloadMappings = aiInfraWorkloadMappings(workloadPath.Workloads, workloadLimit)
	}

	var orchestrationSnapshot *orchestration.Snapshot
	var orchestrationDiag *orchestration.DiagnosticsSnapshot
	if c.orchestrationManager != nil {
		snapshot := c.orchestrationManager.Snapshot()
		diag := c.orchestrationManager.Diagnostics()
		orchestrationSnapshot = &snapshot
		orchestrationDiag = &diag
	}

	ctx := aiInfraSynthesisContext{
		CollectorID:           collectorID,
		Cluster:               cluster,
		Namespace:             namespace,
		Service:               service,
		Nodes:                 nodes,
		DataPath:              c.buildDataPathDiagnostics(collectorID),
		KernelPath:            c.buildKernelPathDiagnostics(collectorID),
		RootCause:             c.buildRootCauseDiagnostics(collectorID),
		WorkloadPath:          workloadPath,
		TopPrograms:           c.aggregateTopProgramsFiltered(maxTopProgramsLimit, collectorID),
		GPUNodes:              filterGPUNodesByCollector(c.gpuStore, collectorID),
		OrchestrationSnapshot: orchestrationSnapshot,
		OrchestrationDiag:     orchestrationDiag,
	}

	resp.Layers = []aiInfraLayerDiagnostics{
		aiInfraComputeLayer(ctx),
		aiInfraOrchestrationLayer(ctx),
		aiInfraCommunicationLayer(ctx),
		aiInfraMemoryLayer(ctx),
		aiInfraDataPipelineLayer(ctx),
		aiInfraExecutionLayer(ctx),
		aiInfraReliabilityLayer(ctx),
		aiInfraServingLayer(ctx),
	}
	resp.IncidentDrilldown = aiInfraBuildIncidentDrilldowns(ctx, aiInfraRankLimit)

	resp.Summary.NodeCount = len(ctx.Nodes)
	resp.Summary.WorkloadCount = ctx.WorkloadPath.Summary.WorkloadCount
	resp.Summary.LayerCount = len(resp.Layers)
	resp.Summary.RootCauseFindings = ctx.RootCause.Summary.FindingCount
	resp.Summary.CriticalFindings = ctx.RootCause.Summary.CriticalFindings
	resp.Summary.DegradedFindings = ctx.RootCause.Summary.DegradedFindings
	resp.Summary.CommunicationSkews = ctx.WorkloadPath.Summary.CommunicationImbalanceWorkloads
	resp.Summary.IncidentDrilldowns = len(resp.IncidentDrilldown)

	coverageTotal := 0.0
	topLayer := aiInfraLayerDiagnostics{}
	for idx, layer := range resp.Layers {
		coverageTotal += layer.CoveragePercent
		switch layer.Severity {
		case "critical":
			resp.Summary.CriticalLayers++
		case "degraded":
			resp.Summary.DegradedLayers++
		}
		for _, measurement := range layer.Measurements {
			switch measurement.Status {
			case aiInfraMeasurementMeasured:
				resp.Summary.MeasuredCount++
			case aiInfraMeasurementPartial:
				resp.Summary.PartialCount++
			case aiInfraMeasurementMissing:
				resp.Summary.MissingCount++
			}
			method := strings.TrimSpace(measurement.Method)
			if method == "" {
				method = aiInfraInferMeasurementMethod(measurement)
			}
			switch method {
			case aiInfraMethodDirect:
				resp.Summary.MethodDirectCount++
			case aiInfraMethodDerived:
				resp.Summary.MethodDerivedCount++
			case aiInfraMethodProxy:
				resp.Summary.MethodProxyCount++
			case aiInfraMethodMissing:
				resp.Summary.MethodMissingCount++
			default:
				resp.Summary.MethodProxyCount++
			}
		}
		if idx == 0 || aiInfraLayerRank(layer) > aiInfraLayerRank(topLayer) {
			topLayer = layer
		}
	}
	if len(resp.Layers) > 0 {
		resp.Summary.CoveragePercent = clampRange(coverageTotal/float64(len(resp.Layers)), 0, 100)
		resp.Summary.TopLayerID = topLayer.ID
		resp.Summary.TopLayerTitle = topLayer.Title
		if len(topLayer.TopRisks) > 0 {
			resp.Summary.TopRisk = topLayer.TopRisks[0]
		}
	}

	return resp
}

func aiInfraLayerRank(layer aiInfraLayerDiagnostics) float64 {
	return float64(aiInfraSeverityRank(layer.Severity))*100.0 + layer.Score
}

func aiInfraSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "degraded":
		return 2
	case "healthy":
		return 1
	default:
		return 0
	}
}

func filterIngestSnapshotsByCollector(nodes []*ingest.NodeSnapshot, collectorID string) []*ingest.NodeSnapshot {
	if collectorID == "" {
		filtered := make([]*ingest.NodeSnapshot, 0, len(nodes))
		for _, node := range nodes {
			if node != nil {
				filtered = append(filtered, node)
			}
		}
		return filtered
	}
	filtered := make([]*ingest.NodeSnapshot, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.CollectorID == collectorID {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func filterGPUNodesByCollector(store *gpuobs.Store, collectorID string) []*gpuobs.Node {
	if store == nil {
		return nil
	}
	snapshot := store.Snapshot()
	if collectorID == "" {
		return snapshot
	}
	out := make([]*gpuobs.Node, 0, len(snapshot))
	for _, node := range snapshot {
		if node == nil {
			continue
		}
		if node.CollectorID == collectorID {
			out = append(out, node)
		}
	}
	return out
}

func aiInfraWorkloadMappings(workloads []workloadPathDiagnosticsWorkload, limit int) []aiInfraWorkloadMapping {
	if limit <= 0 {
		limit = defaultAIInfraWorkloadLimit
	}
	if len(workloads) == 0 {
		return nil
	}
	rows := make([]aiInfraWorkloadMapping, 0, aiInfraMinInt(limit, len(workloads)))
	for _, workload := range workloads {
		nodes := make([]string, 0, len(workload.Nodes))
		for _, node := range workload.Nodes {
			name := strings.TrimSpace(node.Hostname)
			if name == "" {
				name = strings.TrimSpace(node.NodeName)
			}
			if name == "" {
				continue
			}
			nodes = append(nodes, name)
		}
		rows = append(rows, aiInfraWorkloadMapping{
			Cluster:       workload.Cluster,
			Namespace:     workload.Namespace,
			Kind:          workload.Kind,
			Name:          workload.Name,
			Service:       workload.Service,
			Path:          "workload -> pod -> node -> device",
			PodsRunning:   workload.PodsRunning,
			PodsPending:   workload.PodsPending,
			PodsFailed:    workload.PodsFailed,
			GPURequests:   workload.GPURequests,
			GPULimits:     workload.GPULimits,
			NodeCount:     workload.NodeCount,
			ResolvedNodes: workload.ResolvedNodes,
			Nodes:         nodes,
			RiskFlags:     workload.Risks,
			Bottleneck:    workload.Bottleneck,
		})
		if len(rows) >= limit {
			break
		}
	}
	return rows
}

func aiInfraComputeLayer(ctx aiInfraSynthesisContext) aiInfraLayerDiagnostics {
	layer := aiInfraLayerDiagnostics{
		ID:             "compute_virtualization",
		Title:          "Compute abstraction and device virtualization",
		Scope:          "node+workload",
		Signals:        map[string]float64{},
		Sources:        map[string]string{},
		RankedEntities: []aiInfraRankedEntity{},
		Troubleshooting: []string{
			"Check GPU-starvation candidates (low GPU utilization + high network/storage pressure).",
			"Compare shared-device ratio against MIG enablement before increasing time-sliced tenancy.",
			"Drill down to top GPU processes to confirm whether occupancy is compute-bound or feed-bound.",
		},
	}

	networkByCollector := map[string]float64{}
	storageByCollector := map[string]float64{}
	for _, row := range ctx.DataPath.Network.Rankings {
		networkByCollector[row.CollectorID] = row.Score
	}
	for _, row := range ctx.DataPath.Storage.Rankings {
		storageByCollector[row.CollectorID] = row.Score
	}

	nodeCount := len(ctx.Nodes)
	gpuUtilCount := 0
	avgGPUUtil := 0.0
	lowGPUWithPathPressure := 0
	gpuBusyNodes := 0
	for _, node := range ctx.Nodes {
		if node == nil || node.Metrics == nil {
			continue
		}
		gpuUtil := metricValueOr(node.Metrics, "node_gpu_utilization_sm_avg_percent", "node_gpu_utilization_percent")
		if gpuUtil > 0 {
			gpuUtilCount++
			avgGPUUtil += gpuUtil
			if gpuUtil >= 90 {
				gpuBusyNodes++
			}
			if gpuUtil < 60 && (networkByCollector[node.CollectorID] >= 3 || storageByCollector[node.CollectorID] >= 3) {
				lowGPUWithPathPressure++
			}
		}
	}
	if gpuUtilCount > 0 {
		avgGPUUtil /= float64(gpuUtilCount)
	}

	totalDevices := 0
	memoryPressureDevices := 0
	sharedDevices := 0
	migEnabledDevices := 0
	pcieObservedDevices := 0
	for _, node := range ctx.GPUNodes {
		if node == nil {
			continue
		}
		for _, dev := range node.GPUs {
			totalDevices++
			if dev.MemTotalMiB > 0 {
				if (dev.MemUsedMiB/dev.MemTotalMiB)*100.0 >= 90 {
					memoryPressureDevices++
				}
			}
			if dev.ProcessCount >= 2 || dev.ContextCount >= 2 {
				sharedDevices++
			}
			if dev.MigEnabled >= 0.5 {
				migEnabledDevices++
			}
			if dev.PCIERxMBs > 0 || dev.PCIETxMBs > 0 {
				pcieObservedDevices++
			}
		}
	}

	gpuRequestedCards := 0.0
	gpuSliceWorkloads := 0
	gpuPartitionAssignments := 0
	gpuTimeSliceProxyWorkloads := 0
	orchWorkloadsObserved := 0
	if ctx.OrchestrationSnapshot != nil {
		for _, workload := range ctx.OrchestrationSnapshot.Workloads {
			requestedGPU := workload.Spec.Requested.GPUCards
			if requestedGPU > 0 {
				orchWorkloadsObserved++
				gpuRequestedCards += requestedGPU
				if workload.Spec.MaxPartitions > 1 {
					gpuTimeSliceProxyWorkloads++
				}
				if len(workload.Assignments) > 0 {
					gpuSliceWorkloads++
					gpuPartitionAssignments += len(workload.Assignments)
				}
			}
		}
	}
	gpuSliceDensity := 0.0
	if gpuRequestedCards > 0 {
		gpuSliceDensity = float64(gpuPartitionAssignments) / gpuRequestedCards
	}

	gpuPrograms := aiInfraTopProgramsByCategory(ctx.TopPrograms, "gpu")
	for _, program := range gpuPrograms {
		weight := program.GPUUtilSMPct + program.GPUMemMiB/1024.0
		if weight <= 0 {
			weight = program.Score
		}
		label := strings.TrimSpace(program.Name)
		if label == "" {
			label = program.PID
		}
		if label == "" {
			continue
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "process",
			ID:       strings.TrimSpace(program.PID),
			Label:    label,
			Score:    weight,
			Severity: pressureSeverity(weight / 3.0),
			Detail:   strings.TrimSpace(program.Hostname),
		})
		if len(layer.RankedEntities) >= aiInfraRankLimit {
			break
		}
	}

	workloadPressureRatio := 0.0
	if ctx.WorkloadPath.Summary.WorkloadCount > 0 {
		workloadPressureRatio = float64(ctx.WorkloadPath.Summary.CriticalWorkloads) / float64(ctx.WorkloadPath.Summary.WorkloadCount)
	}

	score := clamp01(float64(lowGPUWithPathPressure)/float64(aiInfraMaxInt(nodeCount, 1)))*3.0 +
		clamp01(float64(memoryPressureDevices)/float64(aiInfraMaxInt(totalDevices, 1)))*2.5 +
		clamp01(float64(sharedDevices)/float64(aiInfraMaxInt(totalDevices, 1)))*1.5 +
		clamp01(workloadPressureRatio)*2.5 +
		clamp01(gpuSliceDensity/2.0)*0.5

	measurements := []aiInfraLayerMeasurement{
		{
			Name:   "GPU utilization per node",
			Metric: "node_gpu_utilization_sm_avg_percent",
			Source: diagnosticMetricSource("node_gpu_utilization_sm_avg_percent"),
			Status: aiInfraStatusFromCoverage(gpuUtilCount, nodeCount),
		},
		{
			Name:   "GPU memory pressure per device",
			Metric: "gpuobs.mem_used_mib / mem_total_mib",
			Source: "nvidia-smi / NVML / DCGM",
			Status: aiInfraStatusFromCoverage(totalDevices, totalDevices),
		},
		{
			Name:   "GPU sharing (MIG partitions)",
			Metric: "node_gpu_mig_enabled",
			Source: "nvidia-smi MIG inventory",
			Status: aiInfraStatusFromCoverage(totalDevices, totalDevices),
			Note:   "If MIG is disabled fleet-wide, telemetry stays measured with zero enabled partitions.",
		},
		{
			Name:   "GPU sharing (MPS/time-slicing)",
			Metric: "runtime tenant share counters",
			Source: "/api/v1/orchestration/workloads (partition proxy) + runtime counters (if integrated)",
			Status: aiInfraStatusFromCoverage(gpuTimeSliceProxyWorkloads, aiInfraMaxInt(orchWorkloadsObserved, 1)),
			Note:   "Current signal is derived from scheduler partitioning hints (MaxPartitions) and assignment fanout.",
		},
		{
			Name:   "Job-level device slice occupancy",
			Metric: "workload.spec.requested.gpu_cards + assignment.partitions",
			Source: "/api/v1/orchestration/workloads",
			Status: aiInfraStatusFromCoverage(gpuSliceWorkloads, aiInfraMaxInt(orchWorkloadsObserved, 1)),
			Note:   "Tracks requested GPU cards against realized assignment partitions as a practical slicing proxy.",
		},
		{
			Name:   "TPU/NPU utilization",
			Metric: "vendor-specific accelerator counters",
			Source: "not integrated",
			Status: aiInfraMeasurementMissing,
		},
	}
	deviceTopologyScore := clamp01((100.0-avgGPUUtil)/100.0)*3.5 +
		clamp01(float64(lowGPUWithPathPressure)/float64(aiInfraMaxInt(nodeCount, 1)))*4.0 +
		clamp01(float64(aiInfraMaxInt(totalDevices-pcieObservedDevices, 0))/float64(aiInfraMaxInt(totalDevices, 1)))*2.5
	sharingPartitionScore := clamp01(float64(sharedDevices)/float64(aiInfraMaxInt(totalDevices, 1)))*3.0 +
		clamp01(float64(aiInfraMaxInt(sharedDevices-migEnabledDevices, 0))/float64(aiInfraMaxInt(totalDevices, 1)))*3.0 +
		clamp01(gpuSliceDensity/2.0)*3.0 +
		clamp01(float64(gpuTimeSliceProxyWorkloads)/float64(aiInfraMaxInt(orchWorkloadsObserved, 1)))*1.0
	heterogeneousCoverageScore := 7.0 +
		clamp01(float64(aiInfraMaxInt(totalDevices-pcieObservedDevices, 0))/float64(aiInfraMaxInt(totalDevices, 1)))*3.0
	layer.Domains = []aiInfraLayerDomain{
		{
			ID:              "device_topology_occupancy",
			Title:           "Device topology and occupancy",
			Score:           clampRange(deviceTopologyScore, 0, 10),
			Severity:        pressureSeverity(deviceTopologyScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(gpuUtilCount, nodeCount)}, {Status: aiInfraStatusFromCoverage(totalDevices, totalDevices)}}),
			Signals: map[string]float64{
				"avg_gpu_util_percent":             avgGPUUtil,
				"gpu_low_with_path_pressure_nodes": float64(lowGPUWithPathPressure),
				"gpu_busy_nodes":                   float64(gpuBusyNodes),
				"gpu_pcie_observed_devices":        float64(pcieObservedDevices),
				"gpu_devices_total":                float64(totalDevices),
			},
			Sources: map[string]string{
				"avg_gpu_util_percent":      diagnosticMetricSource("node_gpu_utilization_sm_avg_percent"),
				"gpu_pcie_observed_devices": "nvidia-smi / NVML",
			},
		},
		{
			ID:              "gpu_sharing_and_slices",
			Title:           "GPU sharing and slice density",
			Score:           clampRange(sharingPartitionScore, 0, 10),
			Severity:        pressureSeverity(sharingPartitionScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(totalDevices, totalDevices)}, {Status: aiInfraStatusFromCoverage(gpuSliceWorkloads, aiInfraMaxInt(orchWorkloadsObserved, 1))}}),
			Signals: map[string]float64{
				"gpu_shared_devices":             float64(sharedDevices),
				"gpu_mig_enabled_devices":        float64(migEnabledDevices),
				"gpu_slice_density":              gpuSliceDensity,
				"gpu_partition_assignments":      float64(gpuPartitionAssignments),
				"gpu_time_slice_proxy_workloads": float64(gpuTimeSliceProxyWorkloads),
			},
			Sources: map[string]string{
				"gpu_shared_devices":             "nvidia-smi process/context tables",
				"gpu_mig_enabled_devices":        "nvidia-smi MIG inventory",
				"gpu_slice_density":              "/api/v1/orchestration/workloads",
				"gpu_partition_assignments":      "/api/v1/orchestration/workloads",
				"gpu_time_slice_proxy_workloads": "/api/v1/orchestration/workloads",
			},
		},
		{
			ID:              "heterogeneous_accelerator_coverage",
			Title:           "Heterogeneous accelerator coverage",
			Score:           clampRange(heterogeneousCoverageScore, 0, 10),
			Severity:        pressureSeverity(heterogeneousCoverageScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraMeasurementMissing}, {Status: aiInfraStatusFromCoverage(totalDevices, totalDevices)}}),
			Signals: map[string]float64{
				"gpu_devices_total":       float64(totalDevices),
				"gpu_util_nodes":          float64(gpuUtilCount),
				"tpu_npu_signals_present": 0,
			},
			Sources: map[string]string{
				"gpu_devices_total":       "nvidia-smi / NVML / DCGM",
				"tpu_npu_signals_present": "not integrated",
			},
			Notes: dedupeStrings([]string{
				"TPU/NPU counters are currently unavailable; coverage remains intentionally marked as missing.",
			}),
		},
	}

	layer.Score = clampRange(score, 0, 10)
	layer.Severity = pressureSeverity(layer.Score)
	measurements = aiInfraClassifyMeasurementMethods(measurements)
	layer.Measurements = measurements
	layer.CoveragePercent = aiInfraCoveragePercent(measurements)
	layer.Signals = map[string]float64{
		"node_count":                          float64(nodeCount),
		"gpu_util_nodes":                      float64(gpuUtilCount),
		"avg_gpu_util_percent":                avgGPUUtil,
		"gpu_busy_nodes":                      float64(gpuBusyNodes),
		"gpu_low_with_path_pressure_nodes":    float64(lowGPUWithPathPressure),
		"gpu_devices_total":                   float64(totalDevices),
		"gpu_memory_pressure_devices":         float64(memoryPressureDevices),
		"gpu_shared_devices":                  float64(sharedDevices),
		"gpu_mig_enabled_devices":             float64(migEnabledDevices),
		"gpu_pcie_observed_devices":           float64(pcieObservedDevices),
		"gpu_requested_cards":                 gpuRequestedCards,
		"gpu_slice_workloads":                 float64(gpuSliceWorkloads),
		"gpu_partition_assignments":           float64(gpuPartitionAssignments),
		"gpu_slice_density":                   gpuSliceDensity,
		"gpu_time_slice_proxy_workloads":      float64(gpuTimeSliceProxyWorkloads),
		"critical_workload_ratio":             workloadPressureRatio,
		"workloads_gpu_starvation_risk_count": float64(ctx.WorkloadPath.Summary.GPUStarvationRiskWorkloads),
	}
	layer.Sources = map[string]string{
		"avg_gpu_util_percent":                diagnosticMetricSource("node_gpu_utilization_sm_avg_percent"),
		"gpu_memory_pressure_devices":         "nvidia-smi / NVML / DCGM",
		"gpu_shared_devices":                  "nvidia-smi process/context tables",
		"gpu_mig_enabled_devices":             "nvidia-smi MIG inventory",
		"gpu_slice_density":                   "/api/v1/orchestration/workloads",
		"workloads_gpu_starvation_risk_count": "/api/v1/diagnostics/workload-path",
	}

	risks := []string{}
	if lowGPUWithPathPressure > 0 {
		risks = append(risks, "GPU starvation risk: low GPU occupancy while storage/network pressure is elevated.")
	}
	if memoryPressureDevices > 0 {
		risks = append(risks, "GPU memory pressure: one or more devices exceed 90% memory usage.")
	}
	if sharedDevices > 0 && migEnabledDevices == 0 {
		risks = append(risks, "Shared accelerators detected without MIG partition visibility.")
	}
	if gpuSliceDensity >= 1.8 {
		risks = append(risks, "High GPU slice density suggests aggressive partitioning and potential per-job contention.")
	}
	if totalDevices == 0 {
		risks = append(risks, "No GPU device telemetry found in current scope.")
	}
	layer.TopRisks = dedupeStrings(risks)
	layer.ObservabilityGap = dedupeStrings([]string{
		"MPS and explicit time-slicing counters are not integrated in this release.",
		"TPU/NPU telemetry is not yet wired into the controller aggregation path.",
	})
	return layer
}

func aiInfraOrchestrationLayer(ctx aiInfraSynthesisContext) aiInfraLayerDiagnostics {
	layer := aiInfraLayerDiagnostics{
		ID:             "orchestration_runtime",
		Title:          "Orchestration and runtime",
		Scope:          "cluster+workload",
		Signals:        map[string]float64{},
		Sources:        map[string]string{},
		RankedEntities: []aiInfraRankedEntity{},
		Troubleshooting: []string{
			"Inspect queue depth and queue-delay hotspots before changing priority classes.",
			"Validate workload placement spread and pending/failing pods in the same namespace window.",
			"Use orchestration events to verify whether self-healing requeue loops are stabilizing or thrashing.",
		},
	}

	queueDepth := 0
	deferredWorkloads := 0
	failedWorkloads := 0
	runningWorkloads := 0
	avgQueueDelaySeconds := 0.0
	queueDelayCount := 0
	queueDelayExpected := 0
	preemptionLikeEvents := 0
	sloViolationsActive := 0
	incidentLinkCandidates := 0

	if ctx.OrchestrationSnapshot != nil {
		metrics := ctx.OrchestrationSnapshot.Metrics
		queueDepth = metrics.QueueDepth
		deferredWorkloads = metrics.DeferredWorkloads
		failedWorkloads = metrics.FailedWorkloads
		runningWorkloads = metrics.RunningWorkloads
		queueDelayExpected = len(ctx.OrchestrationSnapshot.Workloads)
		for _, workload := range ctx.OrchestrationSnapshot.Workloads {
			if workload.QueueDelaySeconds > 0 {
				avgQueueDelaySeconds += workload.QueueDelaySeconds
				queueDelayCount++
			}
			if workload.State == orchestration.WorkloadStateQueued || workload.State == orchestration.WorkloadStateDeferred {
				score := workload.QueueDelaySeconds
				if score <= 0 {
					score = 1
				}
				layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
					Kind:     "workload",
					ID:       workload.Spec.ID,
					Label:    workload.Spec.Service,
					Score:    score,
					Severity: pressureSeverity(score / 60.0),
					Detail:   string(workload.State),
				})
			}
		}
		for _, event := range ctx.OrchestrationSnapshot.Events {
			action := strings.ToLower(strings.TrimSpace(event.Action))
			if strings.Contains(action, "requeue") || strings.Contains(action, "remediation") {
				preemptionLikeEvents++
			}
		}
	}
	if queueDelayCount > 0 {
		avgQueueDelaySeconds /= float64(queueDelayCount)
	}

	if ctx.OrchestrationDiag != nil {
		sloViolationsActive = ctx.OrchestrationDiag.Metrics.SLOViolationsActive
	}
	if len(ctx.RootCause.Findings) > 0 && len(ctx.WorkloadPath.Workloads) > 0 {
		for _, finding := range ctx.RootCause.Findings {
			if len(aiInfraMatchWorkloadsForFinding(finding, ctx.WorkloadPath.Workloads)) > 0 {
				incidentLinkCandidates++
			}
		}
	}

	totalPods := 0
	pendingPods := 0
	failedPods := 0
	tenantPodShares := map[string]float64{}
	tenantWorkloadCounts := map[string]int{}
	totalTenantPods := 0.0
	for _, workload := range ctx.WorkloadPath.Workloads {
		totalPods += workload.PodsTotal
		pendingPods += workload.PodsPending
		failedPods += workload.PodsFailed
		tenant := strings.TrimSpace(workload.Namespace)
		if tenant == "" {
			tenant = "default"
		}
		podWeight := float64(aiInfraMaxInt(workload.PodsTotal, 1))
		tenantPodShares[tenant] += podWeight
		tenantWorkloadCounts[tenant]++
		totalTenantPods += podWeight
	}

	tenantValues := make([]float64, 0, len(tenantPodShares))
	for tenant, pods := range tenantPodShares {
		tenantValues = append(tenantValues, pods)
		podShare := 0.0
		if totalTenantPods > 0 {
			podShare = pods / totalTenantPods
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "tenant",
			ID:       tenant,
			Label:    tenant,
			Score:    pods,
			Severity: pressureSeverity(podShare * 10.0),
			Detail:   "pod_share=" + strconv.FormatFloat(podShare, 'f', 2, 64) + " workloads=" + strconv.Itoa(tenantWorkloadCounts[tenant]),
		})
	}
	tenantFairness := aiInfraJainFairnessIndex(tenantValues)
	tenantTopShare := aiInfraTopShare(tenantValues)
	tenantIsolationPressure := clamp01((tenantTopShare - 0.45) / 0.55)

	score := clamp01(float64(queueDepth)/40.0)*2.0 +
		clamp01(avgQueueDelaySeconds/120.0)*2.0 +
		clamp01(float64(failedWorkloads)/10.0)*2.0 +
		clamp01(float64(pendingPods)/float64(aiInfraMaxInt(totalPods, 1)))*2.0 +
		clamp01(float64(sloViolationsActive)/8.0)*1.2 +
		clamp01((1.0-tenantFairness)/0.35)*0.8

	measurements := []aiInfraLayerMeasurement{
		{
			Name:   "Job queue depth",
			Metric: "orchestrator.queue_depth",
			Source: "/api/v1/orchestration/status",
			Status: aiInfraStatusFromBool(ctx.OrchestrationSnapshot != nil),
		},
		{
			Name:   "Queue delay",
			Metric: "workload.queue_delay_seconds",
			Source: "/api/v1/orchestration/workloads",
			Status: aiInfraStatusFromCoverage(queueDelayExpected, queueDelayExpected),
		},
		{
			Name:   "Placement mapping (workload->pod->node)",
			Metric: "k8s workload-node mapping",
			Source: "/api/v1/diagnostics/workload-path",
			Status: aiInfraStatusFromBool(ctx.WorkloadPath.Summary.WorkloadCount > 0),
		},
		{
			Name:   "Preemption/recovery events",
			Metric: "orchestrator healing events",
			Source: "/api/v1/orchestration/events",
			Status: aiInfraStatusFromBool(ctx.OrchestrationSnapshot != nil),
			Note:   "Events are derived from orchestrator requeue/remediation actions.",
		},
		{
			Name:   "Fairness and isolation scoring",
			Metric: "namespace pod-share jain_fairness_index",
			Source: "/api/v1/diagnostics/workload-path",
			Status: aiInfraStatusFromCoverage(len(tenantPodShares), 2),
			Note:   "Jain fairness index is computed from namespace pod-share in the selected workload scope.",
		},
		{
			Name:   "Incident workflow linkage",
			Metric: "incident->workload->placement correlation coverage",
			Source: "/api/v1/diagnostics/root-cause + /api/v1/diagnostics/workload-path + /api/v1/orchestration/workloads",
			Status: aiInfraStatusFromCoverage(incidentLinkCandidates, aiInfraMaxInt(len(ctx.RootCause.Findings), 1)),
			Note:   "Validates drilldown from finding to affected workload and placement evidence.",
		},
	}
	schedulerQueueScore := clamp01(float64(queueDepth)/40.0)*5.0 + clamp01(avgQueueDelaySeconds/120.0)*5.0
	placementFairnessScore := clamp01(float64(pendingPods)/float64(aiInfraMaxInt(totalPods, 1)))*3.0 +
		clamp01((1.0-tenantFairness)/0.35)*4.0 +
		clamp01(tenantIsolationPressure)*3.0
	incidentLinkDeficit := 0.0
	if len(ctx.RootCause.Findings) > 0 {
		incidentLinkDeficit = 1.0 - clamp01(float64(incidentLinkCandidates)/float64(len(ctx.RootCause.Findings)))
	}
	incidentLinkScore := clamp01(incidentLinkDeficit)*6.0 +
		clamp01(float64(preemptionLikeEvents)/20.0)*2.0 +
		clamp01(float64(sloViolationsActive)/8.0)*2.0
	layer.Domains = []aiInfraLayerDomain{
		{
			ID:              "scheduler_queue",
			Title:           "Scheduler queue and delay",
			Score:           clampRange(schedulerQueueScore, 0, 10),
			Severity:        pressureSeverity(schedulerQueueScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromBool(ctx.OrchestrationSnapshot != nil)}, {Status: aiInfraStatusFromCoverage(queueDelayExpected, queueDelayExpected)}}),
			Signals: map[string]float64{
				"queue_depth":             float64(queueDepth),
				"avg_queue_delay_seconds": avgQueueDelaySeconds,
				"deferred_workloads":      float64(deferredWorkloads),
			},
			Sources: map[string]string{
				"queue_depth":             "/api/v1/orchestration/status",
				"avg_queue_delay_seconds": "/api/v1/orchestration/workloads",
			},
		},
		{
			ID:              "placement_fairness",
			Title:           "Placement fairness and isolation",
			Score:           clampRange(placementFairnessScore, 0, 10),
			Severity:        pressureSeverity(placementFairnessScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(len(tenantPodShares), 2)}, {Status: aiInfraStatusFromBool(ctx.WorkloadPath.Summary.WorkloadCount > 0)}}),
			Signals: map[string]float64{
				"tenant_fairness_index":  tenantFairness,
				"tenant_top_share":       tenantTopShare,
				"tenant_isolation_score": tenantIsolationPressure,
				"pending_pods":           float64(pendingPods),
				"failed_pods":            float64(failedPods),
			},
			Sources: map[string]string{
				"tenant_fairness_index": "/api/v1/diagnostics/workload-path",
				"tenant_top_share":      "/api/v1/diagnostics/workload-path",
				"pending_pods":          "k8s api /api/v1/pods (status.phase)",
			},
		},
		{
			ID:              "incident_runtime_linkage",
			Title:           "Incident to placement linkage",
			Score:           clampRange(incidentLinkScore, 0, 10),
			Severity:        pressureSeverity(incidentLinkScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(incidentLinkCandidates, aiInfraMaxInt(len(ctx.RootCause.Findings), 1))}, {Status: aiInfraStatusFromBool(ctx.OrchestrationSnapshot != nil)}}),
			Signals: map[string]float64{
				"incident_linked_findings": float64(incidentLinkCandidates),
				"preemption_like_events":   float64(preemptionLikeEvents),
				"slo_violations_active":    float64(sloViolationsActive),
			},
			Sources: map[string]string{
				"incident_linked_findings": "/api/v1/diagnostics/root-cause + /api/v1/diagnostics/workload-path",
				"preemption_like_events":   "/api/v1/orchestration/events",
				"slo_violations_active":    "/api/v1/orchestration/diagnostics",
			},
		},
	}

	layer.Score = clampRange(score, 0, 10)
	layer.Severity = pressureSeverity(layer.Score)
	measurements = aiInfraClassifyMeasurementMethods(measurements)
	layer.Measurements = measurements
	layer.CoveragePercent = aiInfraCoveragePercent(measurements)
	layer.Signals = map[string]float64{
		"queue_depth":              float64(queueDepth),
		"running_workloads":        float64(runningWorkloads),
		"deferred_workloads":       float64(deferredWorkloads),
		"failed_workloads":         float64(failedWorkloads),
		"avg_queue_delay_seconds":  avgQueueDelaySeconds,
		"slo_violations_active":    float64(sloViolationsActive),
		"pending_pods":             float64(pendingPods),
		"failed_pods":              float64(failedPods),
		"preemption_like_events":   float64(preemptionLikeEvents),
		"tenant_count":             float64(len(tenantPodShares)),
		"tenant_fairness_index":    tenantFairness,
		"tenant_top_share":         tenantTopShare,
		"tenant_isolation_score":   tenantIsolationPressure,
		"incident_linked_findings": float64(incidentLinkCandidates),
	}
	layer.Sources = map[string]string{
		"queue_depth":              "/api/v1/orchestration/status",
		"avg_queue_delay_seconds":  "/api/v1/orchestration/workloads",
		"pending_pods":             "k8s api /api/v1/pods (status.phase)",
		"failed_pods":              "k8s api /api/v1/pods (status.phase)",
		"slo_violations_active":    "/api/v1/orchestration/diagnostics",
		"tenant_fairness_index":    "/api/v1/diagnostics/workload-path",
		"tenant_top_share":         "/api/v1/diagnostics/workload-path",
		"incident_linked_findings": "/api/v1/diagnostics/root-cause + /api/v1/diagnostics/workload-path",
	}

	risks := []string{}
	if queueDepth > 0 && avgQueueDelaySeconds >= 60 {
		risks = append(risks, "Scheduling queueing delay is elevated for queued/deferred workloads.")
	}
	if pendingPods > 0 || failedPods > 0 {
		risks = append(risks, "Pod scheduling failures are visible in current workload scope.")
	}
	if sloViolationsActive > 0 {
		risks = append(risks, "Active SLO violations indicate placement or capacity mismatch.")
	}
	if preemptionLikeEvents > 0 {
		risks = append(risks, "Self-healing requeue events are active; verify stability before scaling.")
	}
	if len(tenantPodShares) >= 2 && tenantFairness < 0.78 {
		risks = append(risks, "Tenant workload distribution is imbalanced; fairness and isolation risk are rising.")
	}
	if len(tenantPodShares) >= 2 && tenantTopShare >= 0.65 {
		risks = append(risks, "One tenant dominates pod placement in the current scope.")
	}
	if len(ctx.RootCause.Findings) > 0 && incidentLinkCandidates == 0 {
		risks = append(risks, "Root-cause findings cannot currently be linked to scoped workloads/placements.")
	}
	if ctx.OrchestrationSnapshot == nil {
		risks = append(risks, "Orchestration manager is disabled; runtime scheduling visibility is reduced.")
	}
	layer.TopRisks = dedupeStrings(risks)
	layer.ObservabilityGap = dedupeStrings([]string{
		"Slurm/Volcano/Kueue/Ray-native event streams are not integrated in this release.",
		"Fairness scoring currently uses workload-share heuristics and lacks scheduler-native quota signals.",
	})
	aiInfraSortRankedEntities(layer.RankedEntities)
	if len(layer.RankedEntities) > aiInfraRankLimit {
		layer.RankedEntities = layer.RankedEntities[:aiInfraRankLimit]
	}
	return layer
}

func aiInfraCommunicationLayer(ctx aiInfraSynthesisContext) aiInfraLayerDiagnostics {
	layer := aiInfraLayerDiagnostics{
		ID:             "communication_fabric",
		Title:          "Communication fabric",
		Scope:          "node+cluster",
		Signals:        map[string]float64{},
		Sources:        map[string]string{},
		RankedEntities: []aiInfraRankedEntity{},
		Troubleshooting: []string{
			"Prioritize nodes with high RDMA congestion, retransmits, or TX queue fill in the same interval.",
			"Validate congestion-domain boundaries and oversubscription around top-impacted workloads.",
			"Correlate communication imbalance risks with collective-library process hotspots.",
		},
	}

	topNetworkScore := 0.0
	networkScoreSum := 0.0
	txQueueSignalObserved := false
	rdmaSignalNodes := 0
	retransmitSignalNodes := 0
	txQueueSignalNodes := 0
	rdmaCongestedNodes := 0
	retransmitHotNodes := 0
	for _, row := range ctx.DataPath.Network.Rankings {
		networkScoreSum += row.Score
		if row.Score > topNetworkScore {
			topNetworkScore = row.Score
		}
		if value, ok := row.Signals["rdma_congestion_per_second"]; ok {
			rdmaSignalNodes++
			if value > 0 {
				rdmaCongestedNodes++
			}
		}
		if value, ok := row.Signals["tcp_retransmit_ratio"]; ok {
			retransmitSignalNodes++
			if value >= 0.01 {
				retransmitHotNodes++
			}
		}
		if _, ok := row.Signals["tx_queue_fill_percent"]; ok {
			txQueueSignalObserved = true
			txQueueSignalNodes++
		}
		label := strings.TrimSpace(row.Hostname)
		if label == "" {
			label = row.CollectorID
		}
		detail := "network pressure"
		if len(row.Factors) > 0 {
			detail = row.Factors[0]
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "node",
			ID:       row.CollectorID,
			Label:    label,
			Score:    row.Score,
			Severity: row.Severity,
			Detail:   detail,
		})
	}
	avgNetworkScore := 0.0
	networkNodeCount := len(ctx.DataPath.Network.Rankings)
	if networkNodeCount > 0 {
		avgNetworkScore = networkScoreSum / float64(networkNodeCount)
	}

	totalGPUDevices := 0
	pcieObservedDevices := 0
	for _, node := range ctx.GPUNodes {
		if node == nil {
			continue
		}
		for _, dev := range node.GPUs {
			totalGPUDevices++
			if dev.PCIERxMBs > 0 || dev.PCIETxMBs > 0 {
				pcieObservedDevices++
			}
		}
	}
	fabricNodeCount := aiInfraMaxInt(len(ctx.Nodes), 1)
	nvlinkSignalNodes := 0
	nvswitchSignalNodes := 0
	cxlFabricSignalNodes := 0
	for _, node := range ctx.Nodes {
		if node == nil || node.Metrics == nil {
			continue
		}
		metrics := node.Metrics
		if metricExists(metrics,
			"node_gpu_nvlink_tx_bytes_per_second",
			"node_gpu_nvlink_rx_bytes_per_second",
			"node_gpu_nvlink_utilization_percent",
		) {
			nvlinkSignalNodes++
		}
		if metricExists(metrics,
			"node_gpu_nvswitch_tx_bytes_per_second",
			"node_gpu_nvswitch_rx_bytes_per_second",
			"node_gpu_nvswitch_utilization_percent",
		) {
			nvswitchSignalNodes++
		}
		if metricExists(metrics,
			"node_cxl_bandwidth_bytes_per_second",
			"node_cxl_link_utilization_percent",
			"node_cxl_latency_p99_seconds",
		) {
			cxlFabricSignalNodes++
		}
	}

	collectivePatternCount := 0
	collectiveSchedSignalCount := 0
	collectiveSchedHotspots := 0
	processSocketSignalCount := 0
	processQueueHotspots := 0
	processConnHotspots := 0
	processQueuedBytesAvg := 0.0
	processQueueCount := 0
	networkPrograms := aiInfraTopProgramsByCategory(ctx.TopPrograms, "network")
	for _, program := range networkPrograms {
		pattern := strings.ToLower(strings.TrimSpace(program.CommPattern))
		isCollective := aiInfraIsCollectivePattern(pattern)
		if isCollective {
			collectivePatternCount++
			if program.SchedWaitRatio > 0 {
				collectiveSchedSignalCount++
				if program.SchedWaitRatio >= 0.6 {
					collectiveSchedHotspots++
				}
			}
		}

		hasSocketSignal := program.NetQueuedBytes > 0 || program.NetConnections > 0
		if hasSocketSignal {
			processSocketSignalCount++
		}
		if program.NetQueuedBytes > 0 {
			processQueuedBytesAvg += program.NetQueuedBytes
			processQueueCount++
			if program.NetQueuedBytes >= 1*1024*1024 {
				processQueueHotspots++
			}
		}
		if program.NetConnections >= 64 {
			processConnHotspots++
		}

		if !hasSocketSignal && !isCollective {
			continue
		}

		label := strings.TrimSpace(program.Name)
		if label == "" {
			label = strings.TrimSpace(program.PID)
		}
		if label == "" {
			label = "unknown"
		}
		entityID := strings.TrimSpace(program.PID)
		if entityID == "" {
			entityID = strings.TrimSpace(program.CollectorID) + "|" + strings.ToLower(label)
		}
		processScore := clamp01(program.NetQueuedBytes/(32.0*1024.0*1024.0))*5.0 +
			clamp01(float64(program.NetConnections)/256.0)*2.0 +
			clamp01(program.NetBytesPerSecond/(200.0*1024.0*1024.0))*2.0 +
			clamp01(program.SchedWaitRatio/1.0)*1.0
		if processScore <= 0 {
			processScore = clampRange(program.Score, 0, 10)
		}
		detailParts := make([]string, 0, 4)
		if host := strings.TrimSpace(program.Hostname); host != "" {
			detailParts = append(detailParts, host)
		}
		if pattern != "" {
			detailParts = append(detailParts, "comm="+pattern)
		}
		if program.NetQueuedBytes > 0 {
			detailParts = append(detailParts, "queued_mb="+strconv.FormatFloat(program.NetQueuedBytes/(1024.0*1024.0), 'f', 2, 64))
		}
		if program.NetConnections > 0 {
			detailParts = append(detailParts, "conns="+strconv.Itoa(program.NetConnections))
		}
		if program.SchedWaitRatio > 0 {
			detailParts = append(detailParts, "sched_wait="+strconv.FormatFloat(program.SchedWaitRatio, 'f', 2, 64))
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "process",
			ID:       entityID,
			Label:    label,
			Score:    processScore,
			Severity: pressureSeverity(processScore),
			Detail:   strings.Join(detailParts, ", "),
		})
	}
	if processQueueCount > 0 {
		processQueuedBytesAvg /= float64(processQueueCount)
	}

	for _, workload := range ctx.WorkloadPath.Workloads {
		if !containsString(workload.Risks, "communication_imbalance") {
			continue
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "workload",
			ID:       aiInfraWorkloadIdentity(workload),
			Label:    workload.Namespace + "/" + workload.Name,
			Score:    workload.NetworkScore,
			Severity: workload.Severity,
			Detail:   "communication_imbalance",
		})
	}

	nodeCount := aiInfraMaxInt(len(ctx.Nodes), 1)
	workloadCount := aiInfraMaxInt(ctx.WorkloadPath.Summary.WorkloadCount, 1)
	inNodeInterconnectScore := clamp01(float64(pcieObservedDevices) / float64(aiInfraMaxInt(totalGPUDevices, 1)))
	interNodeFabricScore := clamp01(avgNetworkScore / 8.0)
	collectiveSyncCostProxy := clamp01(float64(ctx.WorkloadPath.Summary.CommunicationImbalanceWorkloads)/float64(workloadCount))*0.6 +
		clamp01(float64(retransmitHotNodes)/float64(aiInfraMaxInt(networkNodeCount, 1)))*0.4
	collectivePatternCoverageGap := 1.0 - clamp01(float64(collectivePatternCount)/float64(aiInfraMaxInt(len(networkPrograms), 1)))
	collectiveQueuePressure := clamp01(float64(processQueueHotspots) / float64(aiInfraMaxInt(processSocketSignalCount, 1)))
	collectiveSchedPressure := clamp01(float64(collectiveSchedHotspots) / float64(aiInfraMaxInt(collectiveSchedSignalCount, 1)))
	score := clampRange(avgNetworkScore, 0, 8) +
		clamp01(float64(ctx.WorkloadPath.Summary.CommunicationImbalanceWorkloads)/float64(workloadCount))*1.2 +
		clamp01(float64(ctx.DataPath.Summary.NetworkCritical)/float64(nodeCount))*1.4 +
		clamp01(collectiveSyncCostProxy)*1.0 +
		collectiveQueuePressure*0.8 +
		collectiveSchedPressure*0.6

	measurements := []aiInfraLayerMeasurement{
		{
			Name:   "NIC counter pressure",
			Metric: "node_network_utilization_peak_percent",
			Source: diagnosticMetricSource("node_network_utilization_peak_percent"),
			Status: aiInfraStatusFromCoverage(networkNodeCount, aiInfraMaxInt(len(ctx.Nodes), 1)),
		},
		{
			Name:   "RDMA congestion",
			Metric: "node_rdma_congestion_events_per_second",
			Source: diagnosticMetricSource("node_rdma_congestion_events_per_second"),
			Status: aiInfraStatusFromCoverage(rdmaSignalNodes, networkNodeCount),
		},
		{
			Name:   "TCP retransmit and tail loss",
			Metric: "node_tcp_retransmit_ratio",
			Source: diagnosticMetricSource("node_tcp_retransmit_ratio"),
			Status: aiInfraStatusFromCoverage(retransmitSignalNodes, networkNodeCount),
		},
		{
			Name:   "In-node PCIe interconnect",
			Metric: "gpuobs.pcie_{rx,tx}_mb_s",
			Source: "nvidia-smi / NVML",
			Status: aiInfraStatusFromCoverage(totalGPUDevices, totalGPUDevices),
		},
		{
			Name:   "NVLink/NVSwitch interconnect",
			Metric: "node_gpu_nvlink_* + node_gpu_nvswitch_*",
			Source: "GPU driver counters (if exported)",
			Status: aiInfraStatusFromCoverage(nvlinkSignalNodes+nvswitchSignalNodes, fabricNodeCount),
		},
		{
			Name:   "CXL memory fabric counters",
			Metric: "node_cxl_*",
			Source: "platform counters (if exported)",
			Status: aiInfraStatusFromCoverage(cxlFabricSignalNodes, fabricNodeCount),
		},
		{
			Name:   "Collective library attribution",
			Metric: "process.comm_pattern (nccl/ucx/gloo/mpi)",
			Source: "process runtime labels",
			Status: aiInfraStatusFromCoverage(collectivePatternCount, aiInfraMaxInt(len(networkPrograms), 1)),
		},
		{
			Name:   "Per-process socket queue attribution",
			Metric: "top_programs.{net_queued_bytes,net_connections}",
			Source: "/api/v1/top/programs (rca_net_process_* from /proc/net/tcp + /proc/*/fd)",
			Status: aiInfraStatusFromCoverage(processSocketSignalCount, aiInfraMaxInt(len(networkPrograms), 1)),
		},
		{
			Name:   "Collective-worker scheduler wait",
			Metric: "top_programs.sched_wait_ratio (comm_pattern={nccl,ucx,gloo,mpi,rdma})",
			Source: "/api/v1/top/programs (rca_cpu_process_sched_wait_ratio from /proc/[pid]/schedstat)",
			Status: aiInfraStatusFromCoverage(collectiveSchedSignalCount, aiInfraMaxInt(collectivePatternCount, 1)),
		},
	}
	inNodeTelemetryGap := clamp01(float64(aiInfraMaxInt(totalGPUDevices-pcieObservedDevices, 0)) / float64(aiInfraMaxInt(totalGPUDevices, 1)))
	interconnectCoverageGap := clamp01(float64(aiInfraMaxInt(fabricNodeCount-(nvlinkSignalNodes+nvswitchSignalNodes), 0)) / float64(fabricNodeCount))
	cxlCoverageGap := clamp01(float64(aiInfraMaxInt(fabricNodeCount-cxlFabricSignalNodes, 0)) / float64(fabricNodeCount))
	inNodeDomainScore := inNodeTelemetryGap*4.0 + interconnectCoverageGap*4.0 + cxlCoverageGap*2.0
	interNodeDomainScore := clampRange(avgNetworkScore, 0, 6) +
		clamp01(float64(rdmaCongestedNodes)/float64(aiInfraMaxInt(networkNodeCount, 1)))*2.0 +
		clamp01(float64(retransmitHotNodes)/float64(aiInfraMaxInt(networkNodeCount, 1)))*2.0
	collectiveDomainScore := clamp01(float64(ctx.WorkloadPath.Summary.CommunicationImbalanceWorkloads)/float64(workloadCount))*4.0 +
		clamp01(float64(retransmitHotNodes)/float64(aiInfraMaxInt(networkNodeCount, 1)))*1.5 +
		collectiveQueuePressure*2.0 +
		collectiveSchedPressure*1.5 +
		collectivePatternCoverageGap*1.0
	layer.Domains = []aiInfraLayerDomain{
		{
			ID:              "in_node_interconnect",
			Title:           "In-node interconnect (PCIe/NVLink/NVSwitch/CXL)",
			Score:           clampRange(inNodeDomainScore, 0, 10),
			Severity:        pressureSeverity(inNodeDomainScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(totalGPUDevices, totalGPUDevices)}, {Status: aiInfraStatusFromCoverage(nvlinkSignalNodes+nvswitchSignalNodes, fabricNodeCount)}, {Status: aiInfraStatusFromCoverage(cxlFabricSignalNodes, fabricNodeCount)}}),
			Signals: map[string]float64{
				"gpu_pcie_observed_devices": float64(pcieObservedDevices),
				"gpu_devices_total":         float64(totalGPUDevices),
				"nvlink_signal_nodes":       float64(nvlinkSignalNodes),
				"nvswitch_signal_nodes":     float64(nvswitchSignalNodes),
				"cxl_fabric_signal_nodes":   float64(cxlFabricSignalNodes),
			},
			Sources: map[string]string{
				"gpu_pcie_observed_devices": "nvidia-smi / NVML",
				"nvlink_signal_nodes":       "GPU driver counters (if exported)",
				"cxl_fabric_signal_nodes":   "platform counters (if exported)",
			},
			Notes: dedupeStrings([]string{
				"Domain score combines observable interconnect pressure and visibility gaps.",
			}),
		},
		{
			ID:              "inter_node_fabric",
			Title:           "Inter-node fabric (RDMA/InfiniBand/RoCE/TCP)",
			Score:           clampRange(interNodeDomainScore, 0, 10),
			Severity:        pressureSeverity(interNodeDomainScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(networkNodeCount, aiInfraMaxInt(len(ctx.Nodes), 1))}, {Status: aiInfraStatusFromCoverage(rdmaSignalNodes, networkNodeCount)}, {Status: aiInfraStatusFromCoverage(retransmitSignalNodes, networkNodeCount)}}),
			Signals: map[string]float64{
				"network_avg_score":     avgNetworkScore,
				"rdma_congested_nodes":  float64(rdmaCongestedNodes),
				"retransmit_hot_nodes":  float64(retransmitHotNodes),
				"tx_queue_signal_nodes": float64(txQueueSignalNodes),
			},
			Sources: map[string]string{
				"network_avg_score":    "/api/v1/diagnostics/data-path",
				"rdma_congested_nodes": diagnosticMetricSource("node_rdma_congestion_events_per_second"),
				"retransmit_hot_nodes": diagnosticMetricSource("node_tcp_retransmit_ratio"),
			},
		},
		{
			ID:              "collective_runtime",
			Title:           "Collective runtime (NCCL/UCX/Gloo/MPI)",
			Score:           clampRange(collectiveDomainScore, 0, 10),
			Severity:        pressureSeverity(collectiveDomainScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(collectivePatternCount, aiInfraMaxInt(len(networkPrograms), 1))}, {Status: aiInfraStatusFromCoverage(ctx.WorkloadPath.Summary.CommunicationImbalanceWorkloads, aiInfraMaxInt(ctx.WorkloadPath.Summary.WorkloadCount, 1))}, {Status: aiInfraStatusFromCoverage(processSocketSignalCount, aiInfraMaxInt(len(networkPrograms), 1))}, {Status: aiInfraStatusFromCoverage(collectiveSchedSignalCount, aiInfraMaxInt(collectivePatternCount, 1))}}),
			Signals: map[string]float64{
				"communication_imbalance_workloads": float64(ctx.WorkloadPath.Summary.CommunicationImbalanceWorkloads),
				"collective_pattern_processes":      float64(collectivePatternCount),
				"process_socket_signal_processes":   float64(processSocketSignalCount),
				"process_queue_hotspots":            float64(processQueueHotspots),
				"collective_sched_wait_hotspots":    float64(collectiveSchedHotspots),
				"collective_pattern_coverage_gap":   collectivePatternCoverageGap,
				"collective_queue_pressure":         collectiveQueuePressure,
				"collective_sched_wait_pressure":    collectiveSchedPressure,
				"collective_sync_cost_proxy":        collectiveSyncCostProxy,
			},
			Sources: map[string]string{
				"communication_imbalance_workloads": "/api/v1/diagnostics/workload-path",
				"collective_pattern_processes":      "/api/v1/top/programs",
				"process_socket_signal_processes":   "/api/v1/top/programs",
				"process_queue_hotspots":            "/api/v1/top/programs",
				"collective_sched_wait_hotspots":    "/api/v1/top/programs",
				"collective_queue_pressure":         "/api/v1/top/programs",
				"collective_sched_wait_pressure":    "/api/v1/top/programs",
				"collective_sync_cost_proxy":        "/api/v1/diagnostics/workload-path + /api/v1/diagnostics/data-path",
			},
		},
	}

	layer.Score = clampRange(score, 0, 10)
	layer.Severity = pressureSeverity(layer.Score)
	measurements = aiInfraClassifyMeasurementMethods(measurements)
	layer.Measurements = measurements
	layer.CoveragePercent = aiInfraCoveragePercent(measurements)
	layer.Signals = map[string]float64{
		"network_avg_score":                 avgNetworkScore,
		"network_top_score":                 topNetworkScore,
		"network_critical_nodes":            float64(ctx.DataPath.Summary.NetworkCritical),
		"network_degraded_nodes":            float64(ctx.DataPath.Summary.NetworkDegraded),
		"rdma_congested_nodes":              float64(rdmaCongestedNodes),
		"retransmit_hot_nodes":              float64(retransmitHotNodes),
		"communication_imbalance_workloads": float64(ctx.WorkloadPath.Summary.CommunicationImbalanceWorkloads),
		"collective_pattern_processes":      float64(collectivePatternCount),
		"process_socket_signal_processes":   float64(processSocketSignalCount),
		"process_queue_hotspots":            float64(processQueueHotspots),
		"process_connection_hotspots":       float64(processConnHotspots),
		"process_queued_bytes_avg":          processQueuedBytesAvg,
		"collective_sched_wait_signals":     float64(collectiveSchedSignalCount),
		"collective_sched_wait_hotspots":    float64(collectiveSchedHotspots),
		"gpu_pcie_observed_devices":         float64(pcieObservedDevices),
		"gpu_devices_total":                 float64(totalGPUDevices),
		"tx_queue_signal_observed":          boolToFloat(txQueueSignalObserved),
		"rdma_signal_nodes":                 float64(rdmaSignalNodes),
		"retransmit_signal_nodes":           float64(retransmitSignalNodes),
		"tx_queue_signal_nodes":             float64(txQueueSignalNodes),
		"in_node_interconnect_score":        inNodeInterconnectScore,
		"inter_node_fabric_score":           interNodeFabricScore,
		"collective_sync_cost_proxy":        collectiveSyncCostProxy,
		"collective_pattern_coverage_gap":   collectivePatternCoverageGap,
		"collective_queue_pressure":         collectiveQueuePressure,
		"collective_sched_wait_pressure":    collectiveSchedPressure,
		"nvlink_signal_nodes":               float64(nvlinkSignalNodes),
		"nvswitch_signal_nodes":             float64(nvswitchSignalNodes),
		"cxl_fabric_signal_nodes":           float64(cxlFabricSignalNodes),
	}
	layer.Sources = map[string]string{
		"network_avg_score":                 "/api/v1/diagnostics/data-path",
		"rdma_congested_nodes":              diagnosticMetricSource("node_rdma_congestion_events_per_second"),
		"retransmit_hot_nodes":              diagnosticMetricSource("node_tcp_retransmit_ratio"),
		"communication_imbalance_workloads": "/api/v1/diagnostics/workload-path",
		"collective_pattern_processes":      "/api/v1/top/programs",
		"process_socket_signal_processes":   "/api/v1/top/programs",
		"process_queue_hotspots":            "/api/v1/top/programs",
		"process_connection_hotspots":       "/api/v1/top/programs",
		"process_queued_bytes_avg":          "/api/v1/top/programs",
		"collective_sched_wait_signals":     "/api/v1/top/programs",
		"collective_sched_wait_hotspots":    "/api/v1/top/programs",
		"in_node_interconnect_score":        "nvidia-smi / NVML + optional node_gpu_nvlink*/node_gpu_nvswitch* counters",
		"inter_node_fabric_score":           "/api/v1/diagnostics/data-path",
		"collective_sync_cost_proxy":        "/api/v1/diagnostics/workload-path + /api/v1/diagnostics/data-path",
		"collective_pattern_coverage_gap":   "/api/v1/top/programs",
		"collective_queue_pressure":         "/api/v1/top/programs",
		"collective_sched_wait_pressure":    "/api/v1/top/programs",
	}

	risks := []string{}
	if ctx.DataPath.Summary.NetworkCritical > 0 {
		risks = append(risks, "Critical network pressure exists on one or more nodes.")
	}
	if rdmaCongestedNodes > 0 {
		risks = append(risks, "RDMA congestion counters indicate fabric queue pressure.")
	}
	if retransmitHotNodes > 0 {
		risks = append(risks, "TCP retransmit ratio indicates packet loss or persistent tail congestion.")
	}
	if ctx.WorkloadPath.Summary.CommunicationImbalanceWorkloads > 0 {
		risks = append(risks, "Workload-level communication imbalance suggests collective skew across nodes.")
	}
	if processQueueHotspots > 0 {
		risks = append(risks, "Per-process socket queue backlog is elevated in communication-active workers.")
	}
	if collectiveSchedHotspots > 0 {
		risks = append(risks, "Collective workers show high scheduler wait ratio, suggesting sync or CPU-runqueue contention.")
	}
	if totalGPUDevices > 0 && nvlinkSignalNodes+nvswitchSignalNodes == 0 {
		risks = append(risks, "In-node fabric telemetry is limited to PCIe; NVLink/NVSwitch counters are absent.")
	}
	if collectivePatternCount == 0 {
		risks = append(risks, "Collective-library attribution is sparse in current process telemetry.")
	}
	layer.TopRisks = dedupeStrings(risks)
	layer.ObservabilityGap = dedupeStrings([]string{
		"NVLink/NVSwitch/CXL coverage depends on vendor exporter signals and may be absent on many fleets.",
		"Direct NCCL allreduce latency histograms are not captured in this release.",
	})
	aiInfraSortRankedEntities(layer.RankedEntities)
	if len(layer.RankedEntities) > aiInfraRankLimit {
		layer.RankedEntities = layer.RankedEntities[:aiInfraRankLimit]
	}
	return layer
}

func aiInfraMemoryLayer(ctx aiInfraSynthesisContext) aiInfraLayerDiagnostics {
	layer := aiInfraLayerDiagnostics{
		ID:             "memory_hierarchy",
		Title:          "Memory hierarchy and locality",
		Scope:          "node",
		Signals:        map[string]float64{},
		Sources:        map[string]string{},
		RankedEntities: []aiInfraRankedEntity{},
		Troubleshooting: []string{
			"Correlate DRAM pressure and page-cache writeback with iowait and NVMe latency spikes.",
			"Identify nodes where swap/pageout activity overlaps with GPU starvation symptoms.",
			"Confirm hot data is pinned to local NVMe/cache tiers before scaling compute.",
		},
	}

	nodeCount := aiInfraMaxInt(len(ctx.Nodes), 1)
	highDRAMNodes := 0
	pageCachePressureNodes := 0
	ioPressureNodes := 0
	swapPressureNodes := 0
	tierStallNodes := 0
	memoryMetricNodes := 0
	pageCacheMetricNodes := 0
	nvmeMetricNodes := 0
	objectMetricNodes := 0
	cxlMetricNodes := 0

	totalUsedPct := 0.0
	usedPctCount := 0

	for _, node := range ctx.Nodes {
		if node == nil || node.Metrics == nil {
			continue
		}
		metrics := node.Metrics
		hostname := strings.TrimSpace(node.Hostname)
		if hostname == "" {
			hostname = node.CollectorID
		}

		usedPct := 0.0
		if total := metricValueOr(metrics, "node_memory_MemTotal_bytes"); total > 0 {
			memoryMetricNodes++
			usedPct = clampRange((metricValueOr(metrics, "node_memory_Used_bytes")/total)*100.0, 0, 100)
			totalUsedPct += usedPct
			usedPctCount++
			if usedPct >= 90 {
				highDRAMNodes++
			}
		}

		dirty := metricValueOr(metrics, "node_memory_Dirty_bytes")
		writeback := metricValueOr(metrics, "node_memory_Writeback_bytes")
		ioFull := metricValueOr(metrics, "node_pressure_io_full_avg10")
		swapOut := metricValueOr(metrics, "node_vmstat_pswpout_per_second", "node_vmstat_pswpout")
		if metricExists(metrics, "node_memory_Dirty_bytes", "node_memory_Writeback_bytes", "node_pressure_io_full_avg10") {
			pageCacheMetricNodes++
		}
		if dirty >= 1024*1024*1024 || writeback >= 256*1024*1024 {
			pageCachePressureNodes++
		}
		if ioFull >= 5 {
			ioPressureNodes++
		}
		if swapOut > 0 {
			swapPressureNodes++
		}

		nvmeLat := metricValueOr(metrics, "node_nvme_avg_request_latency_seconds") * 1000.0
		if metricExists(metrics, "node_nvme_utilization_peak_percent", "node_nvme_avg_request_latency_seconds") {
			nvmeMetricNodes++
		}

		objectGet := metricValueOr(metrics, "node_object_storage_get_latency_p99_seconds") * 1000.0
		objectPut := metricValueOr(metrics, "node_object_storage_put_latency_p99_seconds") * 1000.0
		if metricExists(metrics, "node_object_storage_get_latency_p99_seconds", "node_object_storage_put_latency_p99_seconds") {
			objectMetricNodes++
		}
		if objectGet >= 70 || objectPut >= 100 || nvmeLat >= 35 {
			tierStallNodes++
		}

		for key := range metrics {
			if strings.Contains(strings.ToLower(key), "cxl") {
				cxlMetricNodes++
				break
			}
		}

		nodeScore := clamp01(usedPct/100.0)*2.0 +
			clamp01(ioFull/20.0)*2.0 +
			clamp01(dirty/(1024.0*1024.0*1024.0))*1.5 +
			clamp01(writeback/(512.0*1024.0*1024.0))*1.5 +
			clamp01(nvmeLat/60.0)*1.5 +
			clamp01(objectGet/120.0)*1.5
		if nodeScore <= 0 {
			continue
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "node",
			ID:       node.CollectorID,
			Label:    hostname,
			Score:    nodeScore,
			Severity: pressureSeverity(nodeScore),
			Detail:   "dram/page-cache/tier pressure",
		})
	}

	avgUsedPct := 0.0
	if usedPctCount > 0 {
		avgUsedPct = totalUsedPct / float64(usedPctCount)
	}

	totalGPUDevices := 0
	hbmObservedDevices := 0
	hbmPressureDevices := 0
	for _, gpuNode := range ctx.GPUNodes {
		if gpuNode == nil {
			continue
		}
		for _, dev := range gpuNode.GPUs {
			totalGPUDevices++
			if dev.MemTotalMiB > 0 {
				hbmObservedDevices++
				if (dev.MemUsedMiB/dev.MemTotalMiB)*100.0 >= 90 {
					hbmPressureDevices++
				}
			}
		}
	}
	memoryPrograms := aiInfraTopProgramsByCategory(ctx.TopPrograms, "memory")
	processMovementHotspots := 0
	for _, program := range memoryPrograms {
		label := strings.TrimSpace(program.Name)
		if label == "" {
			label = program.PID
		}
		if label == "" {
			continue
		}
		movementBps := program.NetBytesPerSecond + program.DiskReadBps + program.DiskWriteBps
		if movementBps > 0 {
			processMovementHotspots++
		}
		score := (float64(program.MemoryBytes) / (1024 * 1024 * 1024)) + (movementBps / (1024 * 1024))
		if score <= 0 {
			score = program.Score
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "process",
			ID:       strings.TrimSpace(program.PID),
			Label:    label,
			Score:    score,
			Severity: pressureSeverity(score / 8.0),
			Detail:   strings.TrimSpace(program.Hostname),
		})
	}
	processAttributionCount := len(ctx.TopPrograms)
	workloadAttributionCount := ctx.WorkloadPath.Summary.WorkloadCount

	score := clamp01(float64(highDRAMNodes)/float64(nodeCount))*2.0 +
		clamp01(float64(pageCachePressureNodes)/float64(nodeCount))*2.0 +
		clamp01(float64(ioPressureNodes)/float64(nodeCount))*2.0 +
		clamp01(float64(swapPressureNodes)/float64(nodeCount))*2.0 +
		clamp01(float64(tierStallNodes)/float64(nodeCount))*1.4 +
		clamp01(float64(processMovementHotspots)/float64(aiInfraMaxInt(len(ctx.TopPrograms), 1)))*0.6

	measurements := []aiInfraLayerMeasurement{
		{
			Name:   "GPU HBM utilization",
			Metric: "gpuobs.mem_used_mib / mem_total_mib",
			Source: "nvidia-smi / NVML",
			Status: aiInfraStatusFromCoverage(hbmObservedDevices, totalGPUDevices),
		},
		{
			Name:   "Host DRAM pressure",
			Metric: "node_memory_{Used,MemTotal}_bytes",
			Source: "/proc/meminfo",
			Status: aiInfraStatusFromCoverage(memoryMetricNodes, nodeCount),
		},
		{
			Name:   "Page cache and writeback",
			Metric: "node_memory_Dirty_bytes + node_memory_Writeback_bytes + node_pressure_io_full_avg10",
			Source: "/proc/meminfo + /proc/pressure/io",
			Status: aiInfraStatusFromCoverage(pageCacheMetricNodes, nodeCount),
		},
		{
			Name:   "CXL pooled memory",
			Metric: "node_cxl_*",
			Source: "not integrated",
			Status: aiInfraStatusFromBool(cxlMetricNodes > 0),
		},
		{
			Name:   "Local NVMe tier",
			Metric: "node_nvme_utilization_peak_percent + node_nvme_avg_request_latency_seconds",
			Source: diagnosticMetricSource("node_nvme_avg_request_latency_seconds"),
			Status: aiInfraStatusFromCoverage(nvmeMetricNodes, nodeCount),
		},
		{
			Name:   "Remote filesystem/object tier",
			Metric: "node_object_storage_{get,put}_latency_p99_seconds",
			Source: "collector pipeline counters (runtime attribution)",
			Status: aiInfraStatusFromCoverage(objectMetricNodes, nodeCount),
		},
		{
			Name:   "Per-process memory attribution",
			Metric: "top_programs.memory_bytes",
			Source: "/api/v1/top/programs",
			Status: aiInfraStatusFromCoverage(processAttributionCount, aiInfraMaxInt(len(ctx.TopPrograms), 1)),
		},
		{
			Name:   "Per-workload data-movement attribution",
			Metric: "workload_path.{compute,network,storage}_score",
			Source: "/api/v1/diagnostics/workload-path",
			Status: aiInfraStatusFromCoverage(workloadAttributionCount, workloadAttributionCount),
		},
	}
	hbmHostDomainScore := clamp01(float64(hbmPressureDevices)/float64(aiInfraMaxInt(totalGPUDevices, 1)))*5.0 +
		clamp01(float64(highDRAMNodes)/float64(nodeCount))*5.0
	pageCacheDomainScore := clamp01(float64(pageCachePressureNodes)/float64(nodeCount))*4.0 +
		clamp01(float64(ioPressureNodes)/float64(nodeCount))*4.0 +
		clamp01(float64(swapPressureNodes)/float64(nodeCount))*2.0
	storageTierDomainScore := clamp01(float64(tierStallNodes)/float64(nodeCount))*6.0 +
		clamp01(float64(aiInfraMaxInt(nodeCount-nvmeMetricNodes, 0))/float64(nodeCount))*2.0 +
		clamp01(float64(aiInfraMaxInt(nodeCount-objectMetricNodes, 0))/float64(nodeCount))*2.0
	layer.Domains = []aiInfraLayerDomain{
		{
			ID:              "hbm_to_host_dram",
			Title:           "HBM and host DRAM pressure",
			Score:           clampRange(hbmHostDomainScore, 0, 10),
			Severity:        pressureSeverity(hbmHostDomainScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(hbmObservedDevices, totalGPUDevices)}, {Status: aiInfraStatusFromCoverage(memoryMetricNodes, nodeCount)}}),
			Signals: map[string]float64{
				"hbm_pressure_devices":         float64(hbmPressureDevices),
				"hbm_observed_devices":         float64(hbmObservedDevices),
				"avg_host_memory_used_percent": avgUsedPct,
				"high_dram_nodes":              float64(highDRAMNodes),
			},
			Sources: map[string]string{
				"hbm_pressure_devices":         "nvidia-smi / NVML",
				"avg_host_memory_used_percent": diagnosticMetricSource("node_memory_Used_bytes"),
			},
		},
		{
			ID:              "page_cache_writeback",
			Title:           "Page cache, reclaim, and writeback",
			Score:           clampRange(pageCacheDomainScore, 0, 10),
			Severity:        pressureSeverity(pageCacheDomainScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(pageCacheMetricNodes, nodeCount)}, {Status: aiInfraStatusFromCoverage(memoryMetricNodes, nodeCount)}}),
			Signals: map[string]float64{
				"page_cache_pressure_nodes": float64(pageCachePressureNodes),
				"io_pressure_nodes":         float64(ioPressureNodes),
				"swap_pressure_nodes":       float64(swapPressureNodes),
			},
			Sources: map[string]string{
				"page_cache_pressure_nodes": diagnosticMetricSource("node_memory_Writeback_bytes"),
				"io_pressure_nodes":         diagnosticMetricSource("node_pressure_io_full_avg10"),
				"swap_pressure_nodes":       diagnosticMetricSource("node_vmstat_pswpout_per_second"),
			},
		},
		{
			ID:              "nvme_distributed_object_tiers",
			Title:           "NVMe and distributed/object tiers",
			Score:           clampRange(storageTierDomainScore, 0, 10),
			Severity:        pressureSeverity(storageTierDomainScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(nvmeMetricNodes, nodeCount)}, {Status: aiInfraStatusFromCoverage(objectMetricNodes, nodeCount)}, {Status: aiInfraStatusFromCoverage(cxlMetricNodes, nodeCount)}}),
			Signals: map[string]float64{
				"tier_stall_nodes":    float64(tierStallNodes),
				"nvme_metric_nodes":   float64(nvmeMetricNodes),
				"object_metric_nodes": float64(objectMetricNodes),
				"cxl_metric_nodes":    float64(cxlMetricNodes),
			},
			Sources: map[string]string{
				"tier_stall_nodes":    "node_object_storage_* + node_nvme_*",
				"nvme_metric_nodes":   diagnosticMetricSource("node_nvme_avg_request_latency_seconds"),
				"object_metric_nodes": "collector pipeline counters (runtime attribution)",
				"cxl_metric_nodes":    "platform counters (if exported)",
			},
		},
	}

	layer.Score = clampRange(score, 0, 10)
	layer.Severity = pressureSeverity(layer.Score)
	measurements = aiInfraClassifyMeasurementMethods(measurements)
	layer.Measurements = measurements
	layer.CoveragePercent = aiInfraCoveragePercent(measurements)
	layer.Signals = map[string]float64{
		"avg_host_memory_used_percent": avgUsedPct,
		"high_dram_nodes":              float64(highDRAMNodes),
		"page_cache_pressure_nodes":    float64(pageCachePressureNodes),
		"io_pressure_nodes":            float64(ioPressureNodes),
		"swap_pressure_nodes":          float64(swapPressureNodes),
		"tier_stall_nodes":             float64(tierStallNodes),
		"nvme_metric_nodes":            float64(nvmeMetricNodes),
		"object_metric_nodes":          float64(objectMetricNodes),
		"hbm_observed_devices":         float64(hbmObservedDevices),
		"hbm_pressure_devices":         float64(hbmPressureDevices),
		"gpu_devices_total":            float64(totalGPUDevices),
		"cxl_metric_nodes":             float64(cxlMetricNodes),
		"memory_processes_observed":    float64(processAttributionCount),
		"process_movement_hotspots":    float64(processMovementHotspots),
		"workload_attribution_count":   float64(workloadAttributionCount),
	}
	layer.Sources = map[string]string{
		"avg_host_memory_used_percent": diagnosticMetricSource("node_memory_Used_bytes"),
		"page_cache_pressure_nodes":    diagnosticMetricSource("node_memory_Writeback_bytes"),
		"io_pressure_nodes":            diagnosticMetricSource("node_pressure_io_full_avg10"),
		"swap_pressure_nodes":          diagnosticMetricSource("node_vmstat_pswpout_per_second"),
		"tier_stall_nodes":             "node_object_storage_* + node_nvme_*",
		"memory_processes_observed":    "/api/v1/top/programs",
		"workload_attribution_count":   "/api/v1/diagnostics/workload-path",
	}

	risks := []string{}
	if highDRAMNodes > 0 {
		risks = append(risks, "Host DRAM pressure exceeds safe headroom on one or more nodes.")
	}
	if pageCachePressureNodes > 0 {
		risks = append(risks, "Page-cache dirty/writeback backlog can amplify downstream I/O latency.")
	}
	if swapPressureNodes > 0 {
		risks = append(risks, "Swap activity detected; memory locality and reclaim pressure need immediate triage.")
	}
	if tierStallNodes > 0 {
		risks = append(risks, "Latency inflation across NVMe/object tiers can stall upstream compute pipelines.")
	}
	if tierStallNodes > 0 && processMovementHotspots > 0 {
		risks = append(risks, "Process-level data-movement hotspots overlap with memory-tier latency pressure.")
	}
	if cxlMetricNodes == 0 {
		risks = append(risks, "CXL memory-fabric telemetry is unavailable in current scope.")
	}
	layer.TopRisks = dedupeStrings(risks)
	layer.ObservabilityGap = dedupeStrings([]string{
		"NUMA page-locality attribution is partial and needs explicit per-process NUMA counters.",
		"CXL pooled-memory metrics are not integrated into default collectors.",
	})
	aiInfraSortRankedEntities(layer.RankedEntities)
	if len(layer.RankedEntities) > aiInfraRankLimit {
		layer.RankedEntities = layer.RankedEntities[:aiInfraRankLimit]
	}
	return layer
}

func aiInfraDataPipelineLayer(ctx aiInfraSynthesisContext) aiInfraLayerDiagnostics {
	layer := aiInfraLayerDiagnostics{
		ID:             "data_pipeline",
		Title:          "Training and inference data pipeline",
		Scope:          "workload+node",
		Signals:        map[string]float64{},
		Sources:        map[string]string{},
		RankedEntities: []aiInfraRankedEntity{},
		Troubleshooting: []string{
			"Prioritize workloads with GPU-starvation risk and correlate with prefetch stalls.",
			"Reduce small-file amplification and checkpoint burst overlap on hot nodes.",
			"Confirm cache-hit recovery after moving datasets to local NVMe tiers.",
		},
	}

	prefetchNodes := 0
	smallIONodes := 0
	cacheHitNodes := 0
	checkpointNodes := 0
	objectLatencyNodes := 0
	shardSignalNodes := 0
	shardImbalanceNodes := 0
	loaderQueueSignalNodes := 0
	loaderQueuePressureNodes := 0
	nodeCount := aiInfraMaxInt(len(ctx.Nodes), 1)

	prefetchAvg := 0.0
	smallIOAvg := 0.0
	cacheHitAvg := 0.0
	checkpointAvg := 0.0
	shardImbalanceAvg := 0.0
	loaderQueueDepthAvg := 0.0
	counts := map[string]int{"prefetch": 0, "smallio": 0, "cache": 0, "checkpoint": 0}

	for _, node := range ctx.Nodes {
		if node == nil || node.Metrics == nil {
			continue
		}
		metrics := node.Metrics
		prefetch := metricValueOr(metrics, "node_dataloader_prefetch_stall_ratio")
		smallIO := metricValueOr(metrics, "node_storage_small_io_ratio")
		cacheHit := metricValueOr(metrics, "node_cache_hit_ratio")
		checkpoint := metricValueOr(metrics, "node_checkpoint_write_latency_p99_seconds") * 1000.0
		shardSkew := metricValueOr(metrics, "node_dataset_shard_imbalance_ratio")
		loaderQueueDepth := metricValueOr(metrics, "node_dataloader_queue_depth")

		if metricExists(metrics, "node_dataloader_prefetch_stall_ratio") {
			prefetchNodes++
			prefetchAvg += prefetch
			counts["prefetch"]++
		}
		if metricExists(metrics, "node_storage_small_io_ratio") {
			smallIONodes++
			smallIOAvg += smallIO
			counts["smallio"]++
		}
		if metricExists(metrics, "node_cache_hit_ratio") {
			cacheHitNodes++
			cacheHitAvg += cacheHit
			counts["cache"]++
		}
		if metricExists(metrics, "node_checkpoint_write_latency_p99_seconds") {
			checkpointNodes++
			checkpointAvg += checkpoint
			counts["checkpoint"]++
		}
		if metricExists(metrics, "node_object_storage_get_latency_p99_seconds", "node_object_storage_put_latency_p99_seconds") {
			objectLatencyNodes++
		}
		if metricExists(metrics, "node_dataset_shard_imbalance_ratio") {
			shardSignalNodes++
			shardImbalanceAvg += shardSkew
			if shardSkew >= 0.2 {
				shardImbalanceNodes++
			}
		}
		if metricExists(metrics, "node_dataloader_queue_depth") {
			loaderQueueSignalNodes++
			loaderQueueDepthAvg += loaderQueueDepth
			if loaderQueueDepth >= 8 {
				loaderQueuePressureNodes++
			}
		}
	}

	if counts["prefetch"] > 0 {
		prefetchAvg /= float64(counts["prefetch"])
	}
	if counts["smallio"] > 0 {
		smallIOAvg /= float64(counts["smallio"])
	}
	if counts["cache"] > 0 {
		cacheHitAvg /= float64(counts["cache"])
	}
	if counts["checkpoint"] > 0 {
		checkpointAvg /= float64(counts["checkpoint"])
	}
	if shardSignalNodes > 0 {
		shardImbalanceAvg /= float64(shardSignalNodes)
	}
	if loaderQueueSignalNodes > 0 {
		loaderQueueDepthAvg /= float64(loaderQueueSignalNodes)
	}

	for _, workload := range ctx.WorkloadPath.Workloads {
		if containsString(workload.Risks, "gpu_starvation_due_to_io_or_network") || workload.Bottleneck == "storage" {
			layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
				Kind:     "workload",
				ID:       aiInfraWorkloadIdentity(workload),
				Label:    workload.Namespace + "/" + workload.Name,
				Score:    workload.StorageScore,
				Severity: workload.Severity,
				Detail:   strings.Join(workload.Risks, ","),
			})
		}
	}
	for _, row := range ctx.DataPath.Storage.Rankings {
		if row.Signals["dataloader_prefetch_stall_ratio"] <= 0 && row.Signals["small_io_ratio"] <= 0 && row.Signals["checkpoint_write_latency_p99_ms"] <= 0 {
			continue
		}
		label := strings.TrimSpace(row.Hostname)
		if label == "" {
			label = row.CollectorID
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "node",
			ID:       row.CollectorID,
			Label:    label,
			Score:    row.Score,
			Severity: row.Severity,
			Detail:   "prefetch/small-io/checkpoint pressure",
		})
	}

	workloadCount := aiInfraMaxInt(ctx.WorkloadPath.Summary.WorkloadCount, 1)
	score := clamp01(prefetchAvg/0.30)*2.0 +
		clamp01(smallIOAvg/0.50)*2.0 +
		clamp01((1.0-cacheHitAvg)/0.40)*2.0 +
		clamp01(checkpointAvg/220.0)*1.6 +
		clamp01(float64(ctx.WorkloadPath.Summary.GPUStarvationRiskWorkloads)/float64(workloadCount))*1.6 +
		clamp01(shardImbalanceAvg/0.35)*0.8 +
		clamp01(loaderQueueDepthAvg/16.0)*0.8

	measurements := []aiInfraLayerMeasurement{
		{
			Name:   "Prefetch efficiency",
			Metric: "node_dataloader_prefetch_stall_ratio",
			Source: diagnosticMetricSource("node_dataloader_prefetch_stall_ratio"),
			Status: aiInfraStatusFromCoverage(prefetchNodes, nodeCount),
		},
		{
			Name:   "Cache hit ratio",
			Metric: "node_cache_hit_ratio",
			Source: diagnosticMetricSource("node_cache_hit_ratio"),
			Status: aiInfraStatusFromCoverage(cacheHitNodes, nodeCount),
		},
		{
			Name:   "Small-file overhead",
			Metric: "node_storage_small_io_ratio",
			Source: diagnosticMetricSource("node_storage_small_io_ratio"),
			Status: aiInfraStatusFromCoverage(smallIONodes, nodeCount),
		},
		{
			Name:   "Checkpoint burst latency",
			Metric: "node_checkpoint_write_latency_p99_seconds",
			Source: diagnosticMetricSource("node_checkpoint_write_latency_p99_seconds"),
			Status: aiInfraStatusFromCoverage(checkpointNodes, nodeCount),
		},
		{
			Name:   "Dataset shard distribution",
			Metric: "node_dataset_shard_imbalance_ratio",
			Source: diagnosticMetricSource("node_dataset_shard_imbalance_ratio"),
			Status: aiInfraStatusFromCoverage(shardSignalNodes, nodeCount),
		},
		{
			Name:   "Data-loader queue depth",
			Metric: "node_dataloader_queue_depth",
			Source: diagnosticMetricSource("node_dataloader_queue_depth"),
			Status: aiInfraStatusFromCoverage(loaderQueueSignalNodes, nodeCount),
		},
	}

	layer.Score = clampRange(score, 0, 10)
	layer.Severity = pressureSeverity(layer.Score)
	measurements = aiInfraClassifyMeasurementMethods(measurements)
	layer.Measurements = measurements
	layer.CoveragePercent = aiInfraCoveragePercent(measurements)
	layer.Signals = map[string]float64{
		"prefetch_stall_ratio_avg":      prefetchAvg,
		"small_io_ratio_avg":            smallIOAvg,
		"cache_hit_ratio_avg":           cacheHitAvg,
		"checkpoint_latency_p99_ms_avg": checkpointAvg,
		"prefetch_nodes":                float64(prefetchNodes),
		"small_io_nodes":                float64(smallIONodes),
		"cache_hit_nodes":               float64(cacheHitNodes),
		"checkpoint_nodes":              float64(checkpointNodes),
		"object_latency_nodes":          float64(objectLatencyNodes),
		"gpu_starvation_risk_workloads": float64(ctx.WorkloadPath.Summary.GPUStarvationRiskWorkloads),
		"shard_imbalance_ratio_avg":     shardImbalanceAvg,
		"shard_signal_nodes":            float64(shardSignalNodes),
		"shard_imbalance_nodes":         float64(shardImbalanceNodes),
		"dataloader_queue_depth_avg":    loaderQueueDepthAvg,
		"dataloader_queue_nodes":        float64(loaderQueueSignalNodes),
		"dataloader_queue_hot_nodes":    float64(loaderQueuePressureNodes),
	}
	layer.Sources = map[string]string{
		"prefetch_stall_ratio_avg":      diagnosticMetricSource("node_dataloader_prefetch_stall_ratio"),
		"small_io_ratio_avg":            diagnosticMetricSource("node_storage_small_io_ratio"),
		"cache_hit_ratio_avg":           diagnosticMetricSource("node_cache_hit_ratio"),
		"checkpoint_latency_p99_ms_avg": diagnosticMetricSource("node_checkpoint_write_latency_p99_seconds"),
		"gpu_starvation_risk_workloads": "/api/v1/diagnostics/workload-path",
		"shard_imbalance_ratio_avg":     diagnosticMetricSource("node_dataset_shard_imbalance_ratio"),
		"dataloader_queue_depth_avg":    diagnosticMetricSource("node_dataloader_queue_depth"),
	}

	risks := []string{}
	if prefetchAvg >= 0.15 {
		risks = append(risks, "Prefetch stalls are likely starving accelerator input pipelines.")
	}
	if smallIOAvg >= 0.35 {
		risks = append(risks, "Small-file overhead is amplifying metadata and syscall pressure.")
	}
	if cacheHitAvg > 0 && cacheHitAvg < 0.70 {
		risks = append(risks, "Cache-hit ratio is low; hot data is not staying near compute.")
	}
	if checkpointAvg >= 120 {
		risks = append(risks, "Checkpoint write bursts are colliding with live data-loader traffic.")
	}
	if ctx.WorkloadPath.Summary.GPUStarvationRiskWorkloads > 0 {
		risks = append(risks, "Workloads already flagged for GPU starvation due to pipeline pressure.")
	}
	if shardSignalNodes > 0 && shardImbalanceAvg >= 0.2 {
		risks = append(risks, "Dataset shard distribution is imbalanced across active nodes.")
	}
	if loaderQueueSignalNodes > 0 && loaderQueueDepthAvg >= 8 {
		risks = append(risks, "Data-loader queue depth is elevated and may increase step jitter.")
	}
	layer.TopRisks = dedupeStrings(risks)
	layer.ObservabilityGap = dedupeStrings([]string{
		"Shard and queue-depth metrics depend on runtime/exporter instrumentation and may be absent.",
	})
	aiInfraSortRankedEntities(layer.RankedEntities)
	if len(layer.RankedEntities) > aiInfraRankLimit {
		layer.RankedEntities = layer.RankedEntities[:aiInfraRankLimit]
	}
	return layer
}

func aiInfraExecutionLayer(ctx aiInfraSynthesisContext) aiInfraLayerDiagnostics {
	layer := aiInfraLayerDiagnostics{
		ID:             "execution_optimization",
		Title:          "Execution and runtime efficiency",
		Scope:          "node+process",
		Signals:        map[string]float64{},
		Sources:        map[string]string{},
		RankedEntities: []aiInfraRankedEntity{},
		Troubleshooting: []string{
			"Correlate CPU pressure, runnable tasks, and blocked tasks before tuning thread pools.",
			"Use per-process CPU ranking to isolate operator or runtime hotspots.",
			"Validate improvements with the same window in trends and process breakdown.",
		},
	}

	nodeCount := aiInfraMaxInt(len(ctx.Nodes), 1)
	cpuPressureAvg := 0.0
	blockedAvg := 0.0
	runningAvg := 0.0
	iowaitAvg := 0.0
	schedWaitRatioAvg := 0.0
	pressureCount := 0
	blockedCount := 0
	runningCount := 0
	iowaitCount := 0
	schedRatioCount := 0
	contextSwitchMetricNodes := 0
	schedStatNodes := 0
	processSchedWaitSignals := 0
	processBlockDelaySignals := 0
	processSchedWaitAvg := 0.0
	processBlockDelayAvg := 0.0
	kernelLaunchMetricNodes := 0
	operatorHotspotMetricNodes := 0
	graphModeMetricNodes := 0
	memoryPlanningMetricNodes := 0
	kernelLaunchAvgMs := 0.0
	operatorHotspotAvg := 0.0
	graphExecutionAvg := 0.0
	memoryReplanAvg := 0.0

	for _, node := range ctx.Nodes {
		if node == nil || node.Metrics == nil {
			continue
		}
		metrics := node.Metrics
		hostname := strings.TrimSpace(node.Hostname)
		if hostname == "" {
			hostname = node.CollectorID
		}

		cpuPressure := metricValueOr(metrics, "node_pressure_cpu_some_avg10")
		blocked := metricValueOr(metrics, "node_procs_blocked")
		running := metricValueOr(metrics, "node_procs_running")
		iowait := metricValueOr(metrics, "node_cpu_iowait_percent")
		schedWait := metricValueOr(metrics, "node_schedstat_waiting_seconds_total")
		schedRun := metricValueOr(metrics, "node_schedstat_running_seconds_total")
		kernelLaunchMs := metricValueOr(metrics, "node_runtime_kernel_launch_overhead_ms")
		operatorHotspot := metricValueOr(metrics, "node_runtime_operator_hotspot_ratio")
		graphExecution := metricValueOr(metrics, "node_runtime_graph_execution_ratio")
		memoryReplan := metricValueOr(metrics, "node_runtime_memory_planning_reallocs_per_second", "node_runtime_memory_replan_events_per_second")

		if metricExists(metrics, "node_pressure_cpu_some_avg10") {
			cpuPressureAvg += cpuPressure
			pressureCount++
		}
		if metricExists(metrics, "node_procs_blocked") {
			blockedAvg += blocked
			blockedCount++
		}
		if metricExists(metrics, "node_procs_running") {
			runningAvg += running
			runningCount++
		}
		if metricExists(metrics, "node_cpu_iowait_percent") {
			iowaitAvg += iowait
			iowaitCount++
		}
		if metricExists(metrics, "node_schedstat_waiting_seconds_total", "node_schedstat_running_seconds_total") {
			schedStatNodes++
			ratio := 0.0
			if schedRun > 0 {
				ratio = schedWait / schedRun
			}
			schedWaitRatioAvg += ratio
			schedRatioCount++
		}
		if metricExists(metrics, "node_context_switches_total") {
			contextSwitchMetricNodes++
		}
		if metricExists(metrics, "node_runtime_kernel_launch_overhead_ms") {
			kernelLaunchMetricNodes++
			kernelLaunchAvgMs += kernelLaunchMs
		}
		if metricExists(metrics, "node_runtime_operator_hotspot_ratio") {
			operatorHotspotMetricNodes++
			operatorHotspotAvg += operatorHotspot
		}
		if metricExists(metrics, "node_runtime_graph_execution_ratio") {
			graphModeMetricNodes++
			graphExecutionAvg += graphExecution
		}
		if metricExists(metrics, "node_runtime_memory_planning_reallocs_per_second", "node_runtime_memory_replan_events_per_second") {
			memoryPlanningMetricNodes++
			memoryReplanAvg += memoryReplan
		}

		nodeScore := clamp01(cpuPressure/20.0)*2.5 +
			clamp01(blocked/6.0)*2.0 +
			clamp01(iowait/20.0)*1.5 +
			clamp01(schedWait/(schedRun+1.0))*2.0 +
			clamp01(running/24.0)*2.0
		if nodeScore <= 0 {
			continue
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "node",
			ID:       node.CollectorID,
			Label:    hostname,
			Score:    nodeScore,
			Severity: pressureSeverity(nodeScore),
			Detail:   "scheduler pressure",
		})
	}

	if pressureCount > 0 {
		cpuPressureAvg /= float64(pressureCount)
	}
	if blockedCount > 0 {
		blockedAvg /= float64(blockedCount)
	}
	if runningCount > 0 {
		runningAvg /= float64(runningCount)
	}
	if iowaitCount > 0 {
		iowaitAvg /= float64(iowaitCount)
	}
	if schedRatioCount > 0 {
		schedWaitRatioAvg /= float64(schedRatioCount)
	}
	if kernelLaunchMetricNodes > 0 {
		kernelLaunchAvgMs /= float64(kernelLaunchMetricNodes)
	}
	if operatorHotspotMetricNodes > 0 {
		operatorHotspotAvg /= float64(operatorHotspotMetricNodes)
	}
	if graphModeMetricNodes > 0 {
		graphExecutionAvg /= float64(graphModeMetricNodes)
	}
	if memoryPlanningMetricNodes > 0 {
		memoryReplanAvg /= float64(memoryPlanningMetricNodes)
	}

	cpuPrograms := aiInfraTopProgramsByCategory(ctx.TopPrograms, "cpu")
	for _, program := range cpuPrograms {
		label := strings.TrimSpace(program.Name)
		if label == "" {
			label = program.PID
		}
		if label == "" {
			continue
		}
		if program.SchedWaitRatio > 0 {
			processSchedWaitSignals++
			processSchedWaitAvg += program.SchedWaitRatio
		}
		if program.BlockIODelaySecondsPerSecond > 0 {
			processBlockDelaySignals++
			processBlockDelayAvg += program.BlockIODelaySecondsPerSecond
		}
		processScore := program.CPUPercent +
			(clampRange(program.SchedWaitRatio, 0, 5) * 20.0) +
			(clampRange(program.BlockIODelaySecondsPerSecond, 0, 2) * 25.0)
		detailParts := make([]string, 0, 3)
		if host := strings.TrimSpace(program.Hostname); host != "" {
			detailParts = append(detailParts, host)
		}
		if program.SchedWaitRatio > 0 {
			detailParts = append(detailParts, "sched_wait_ratio="+strconv.FormatFloat(program.SchedWaitRatio, 'f', 2, 64))
		}
		if program.BlockIODelaySecondsPerSecond > 0 {
			detailParts = append(detailParts, "blkio_delay_sps="+strconv.FormatFloat(program.BlockIODelaySecondsPerSecond, 'f', 2, 64))
		}
		if processScore <= 0 {
			processScore = program.CPUPercent
		}
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "process",
			ID:       strings.TrimSpace(program.PID),
			Label:    label,
			Score:    processScore,
			Severity: pressureSeverity(processScore / 20.0),
			Detail:   strings.Join(detailParts, " "),
		})
		if len(layer.RankedEntities) >= aiInfraRankLimit {
			break
		}
	}
	if processSchedWaitSignals > 0 {
		processSchedWaitAvg /= float64(processSchedWaitSignals)
	}
	if processBlockDelaySignals > 0 {
		processBlockDelayAvg /= float64(processBlockDelaySignals)
	}
	processSignalExpected := aiInfraMaxInt(len(cpuPrograms), 1)

	score := clamp01(cpuPressureAvg/20.0)*2.5 +
		clamp01(blockedAvg/6.0)*2.0 +
		clamp01(iowaitAvg/20.0)*1.5 +
		clamp01(schedWaitRatioAvg/1.0)*2.0 +
		clamp01(runningAvg/24.0)*1.2 +
		clamp01(processSchedWaitAvg/1.0)*0.4 +
		clamp01(processBlockDelayAvg/0.5)*0.4 +
		clamp01(kernelLaunchAvgMs/5.0)*0.3 +
		clamp01(operatorHotspotAvg/0.40)*0.3 +
		clamp01(memoryReplanAvg/20.0)*0.2
	observedTaskStateNodes := aiInfraMaxInt(runningCount, blockedCount)

	measurements := []aiInfraLayerMeasurement{
		{
			Name:   "Scheduler pressure",
			Metric: "node_pressure_cpu_some_avg10",
			Source: diagnosticMetricSource("node_pressure_cpu_some_avg10"),
			Status: aiInfraStatusFromCoverage(pressureCount, nodeCount),
		},
		{
			Name:   "Runnable/blocked tasks",
			Metric: "node_procs_running + node_procs_blocked",
			Source: diagnosticMetricSource("node_procs_blocked"),
			Status: aiInfraStatusFromCoverage(observedTaskStateNodes, nodeCount),
		},
		{
			Name:   "Scheduler wait ratio",
			Metric: "node_schedstat_waiting_seconds_total / node_schedstat_running_seconds_total",
			Source: diagnosticMetricSource("node_schedstat_waiting_seconds_total"),
			Status: aiInfraStatusFromCoverage(schedStatNodes, nodeCount),
		},
		{
			Name:   "Context switch pressure",
			Metric: "node_context_switches_total",
			Source: "/proc/stat",
			Status: aiInfraStatusFromCoverage(contextSwitchMetricNodes, nodeCount),
		},
		{
			Name:   "Per-process scheduler wait ratio",
			Metric: "top_programs.sched_wait_ratio",
			Source: "/api/v1/top/programs",
			Status: aiInfraStatusFromCoverage(processSchedWaitSignals, processSignalExpected),
		},
		{
			Name:   "Per-process block I/O delay growth",
			Metric: "top_programs.block_io_delay_seconds_per_second",
			Source: "/api/v1/top/programs",
			Status: aiInfraStatusFromCoverage(processBlockDelaySignals, processSignalExpected),
		},
		{
			Name:   "Kernel launch overhead",
			Metric: "node_runtime_kernel_launch_overhead_ms",
			Source: "runtime exporter traces (if integrated)",
			Status: aiInfraStatusFromCoverage(kernelLaunchMetricNodes, nodeCount),
		},
		{
			Name:   "Operator hotspot ratio",
			Metric: "node_runtime_operator_hotspot_ratio",
			Source: "runtime exporter traces (if integrated)",
			Status: aiInfraStatusFromCoverage(operatorHotspotMetricNodes, nodeCount),
		},
		{
			Name:   "Graph vs eager execution",
			Metric: "node_runtime_graph_execution_ratio",
			Source: "runtime exporter traces (if integrated)",
			Status: aiInfraStatusFromCoverage(graphModeMetricNodes, nodeCount),
		},
		{
			Name:   "Memory planning symptoms",
			Metric: "node_runtime_memory_planning_reallocs_per_second",
			Source: "runtime exporter traces (if integrated)",
			Status: aiInfraStatusFromCoverage(memoryPlanningMetricNodes, nodeCount),
		},
	}

	layer.Score = clampRange(score, 0, 10)
	layer.Severity = pressureSeverity(layer.Score)
	measurements = aiInfraClassifyMeasurementMethods(measurements)
	layer.Measurements = measurements
	layer.CoveragePercent = aiInfraCoveragePercent(measurements)
	layer.Signals = map[string]float64{
		"cpu_pressure_avg10":          cpuPressureAvg,
		"procs_running_avg":           runningAvg,
		"procs_blocked_avg":           blockedAvg,
		"cpu_iowait_avg_percent":      iowaitAvg,
		"sched_wait_run_ratio":        schedWaitRatioAvg,
		"context_switch_metric_nodes": float64(contextSwitchMetricNodes),
		"schedstat_nodes":             float64(schedStatNodes),
		"process_sched_wait_ratio":    processSchedWaitAvg,
		"process_sched_wait_signals":  float64(processSchedWaitSignals),
		"process_block_io_delay_sps":  processBlockDelayAvg,
		"process_block_io_signals":    float64(processBlockDelaySignals),
		"kernel_launch_overhead_ms":   kernelLaunchAvgMs,
		"operator_hotspot_ratio":      operatorHotspotAvg,
		"graph_execution_ratio":       graphExecutionAvg,
		"memory_replan_events":        memoryReplanAvg,
		"kernel_launch_nodes":         float64(kernelLaunchMetricNodes),
		"operator_hotspot_nodes":      float64(operatorHotspotMetricNodes),
		"graph_mode_nodes":            float64(graphModeMetricNodes),
		"memory_planning_nodes":       float64(memoryPlanningMetricNodes),
		"node_count":                  float64(nodeCount),
	}
	layer.Sources = map[string]string{
		"cpu_pressure_avg10":         diagnosticMetricSource("node_pressure_cpu_some_avg10"),
		"procs_running_avg":          diagnosticMetricSource("node_procs_running"),
		"procs_blocked_avg":          diagnosticMetricSource("node_procs_blocked"),
		"cpu_iowait_avg_percent":     diagnosticMetricSource("node_cpu_iowait_percent"),
		"sched_wait_run_ratio":       diagnosticMetricSource("node_schedstat_waiting_seconds_total"),
		"process_sched_wait_ratio":   "/api/v1/top/programs",
		"process_block_io_delay_sps": "/api/v1/top/programs",
		"kernel_launch_overhead_ms":  "runtime exporter traces (if integrated)",
		"operator_hotspot_ratio":     "runtime exporter traces (if integrated)",
		"graph_execution_ratio":      "runtime exporter traces (if integrated)",
		"memory_replan_events":       "runtime exporter traces (if integrated)",
	}

	risks := []string{}
	if cpuPressureAvg >= 10 || blockedAvg >= 2 {
		risks = append(risks, "Scheduler contention may be increasing operator tail latency.")
	}
	if iowaitAvg >= 8 {
		risks = append(risks, "Execution threads are blocked by I/O wait.")
	}
	if schedWaitRatioAvg >= 0.5 {
		risks = append(risks, "Scheduler wait time is large relative to running time.")
	}
	if processSchedWaitSignals > 0 && processSchedWaitAvg >= 0.5 {
		risks = append(risks, "Process-level scheduler wait ratio is high for top CPU workloads.")
	}
	if processBlockDelaySignals > 0 && processBlockDelayAvg >= 0.10 {
		risks = append(risks, "Per-process block I/O delay growth suggests execution stalls at the storage layer.")
	}
	if kernelLaunchMetricNodes > 0 && kernelLaunchAvgMs >= 2 {
		risks = append(risks, "Kernel launch overhead is elevated in runtime traces.")
	}
	if operatorHotspotMetricNodes > 0 && operatorHotspotAvg >= 0.35 {
		risks = append(risks, "Operator hotspot ratio indicates concentrated execution bottlenecks.")
	}
	if memoryPlanningMetricNodes > 0 && memoryReplanAvg >= 10 {
		risks = append(risks, "Memory planning reallocation churn is visible in runtime traces.")
	}
	if pressureCount == 0 {
		risks = append(risks, "CPU pressure metrics are sparse in current telemetry scope.")
	}
	layer.TopRisks = dedupeStrings(risks)
	layer.ObservabilityGap = dedupeStrings([]string{
		"Kernel/operator/graph/memory-planning traces require runtime exporters and are optional.",
		"Compiler-specific optimization diagnostics are not inferred unless explicit runtime traces are present.",
	})
	aiInfraSortRankedEntities(layer.RankedEntities)
	if len(layer.RankedEntities) > aiInfraRankLimit {
		layer.RankedEntities = layer.RankedEntities[:aiInfraRankLimit]
	}
	return layer
}

func aiInfraReliabilityLayer(ctx aiInfraSynthesisContext) aiInfraLayerDiagnostics {
	layer := aiInfraLayerDiagnostics{
		ID:             "reliability_sre",
		Title:          "Reliability, SLI/SLO, and incident response",
		Scope:          "cluster+workload",
		Signals:        map[string]float64{},
		Sources:        map[string]string{},
		RankedEntities: []aiInfraRankedEntity{},
		Troubleshooting: []string{
			"Start from critical findings and confirm evidence sources before mitigation.",
			"Track checkpoint health and workload restart loops as first-class reliability signals.",
			"Use orchestration remediation counters to separate true recovery from repeated churn.",
		},
	}

	sloViolations := 0
	failedWorkloads := 0
	remediationActions := 0
	remediationBlocked := 0
	runningWorkloadsSLO := 0
	completedWorkloads := 0
	schedulingAttempts := 0.0
	schedulingFailures := 0.0
	orchestrationEventCount := 0
	remediationEventCount := 0
	firstEventByWorkload := map[string]time.Time{}
	oldestEvent := time.Time{}
	newestEvent := time.Time{}
	timelineSpanSeconds := 0.0
	mttdProxySeconds := 0.0
	mttrProxySeconds := 0.0
	mttdSamples := 0
	mttrSamples := 0
	if ctx.OrchestrationDiag != nil {
		sloViolations = ctx.OrchestrationDiag.Metrics.SLOViolationsActive
		failedWorkloads = ctx.OrchestrationDiag.Metrics.FailedWorkloads
		remediationActions = int(ctx.OrchestrationDiag.Metrics.RemediationActionsTotal)
		remediationBlocked = int(ctx.OrchestrationDiag.Metrics.RemediationBlockedTotal)
		runningWorkloadsSLO = ctx.OrchestrationDiag.Metrics.RunningWorkloads
		completedWorkloads = ctx.OrchestrationDiag.Metrics.CompletedWorkloads
		schedulingAttempts = float64(ctx.OrchestrationDiag.Metrics.SchedulingAttemptsTotal)
		schedulingFailures = float64(ctx.OrchestrationDiag.Metrics.SchedulingFailuresTotal)
	}
	if ctx.OrchestrationSnapshot != nil {
		runningWorkloadsSLO = aiInfraMaxInt(runningWorkloadsSLO, ctx.OrchestrationSnapshot.Metrics.RunningWorkloads)
		completedWorkloads = aiInfraMaxInt(completedWorkloads, ctx.OrchestrationSnapshot.Metrics.CompletedWorkloads)
		if schedulingAttempts <= 0 {
			schedulingAttempts = float64(ctx.OrchestrationSnapshot.Metrics.SchedulingAttemptsTotal)
		}
		if schedulingFailures <= 0 {
			schedulingFailures = float64(ctx.OrchestrationSnapshot.Metrics.SchedulingFailuresTotal)
		}
		for _, event := range ctx.OrchestrationSnapshot.Events {
			if event.Timestamp.IsZero() {
				continue
			}
			orchestrationEventCount++
			if oldestEvent.IsZero() || event.Timestamp.Before(oldestEvent) {
				oldestEvent = event.Timestamp
			}
			if newestEvent.IsZero() || event.Timestamp.After(newestEvent) {
				newestEvent = event.Timestamp
			}
			action := strings.ToLower(strings.TrimSpace(event.Action))
			if strings.Contains(action, "requeue") || strings.Contains(action, "remediation") || strings.Contains(action, "recover") {
				remediationEventCount++
			}
			workloadID := strings.TrimSpace(event.WorkloadID)
			if workloadID == "" {
				continue
			}
			current := firstEventByWorkload[workloadID]
			if current.IsZero() || event.Timestamp.Before(current) {
				firstEventByWorkload[workloadID] = event.Timestamp
			}
		}
		if orchestrationEventCount >= 2 && newestEvent.After(oldestEvent) {
			timelineSpanSeconds = newestEvent.Sub(oldestEvent).Seconds()
		}
		mttdTotal := 0.0
		mttrTotal := 0.0
		for _, workload := range ctx.OrchestrationSnapshot.Workloads {
			workloadID := strings.TrimSpace(workload.Spec.ID)
			if workloadID == "" {
				continue
			}
			firstEvent, ok := firstEventByWorkload[workloadID]
			if !ok || firstEvent.IsZero() {
				continue
			}
			if !workload.CreatedAt.IsZero() && firstEvent.After(workload.CreatedAt) {
				mttdTotal += firstEvent.Sub(workload.CreatedAt).Seconds()
				mttdSamples++
			}
			if !workload.UpdatedAt.IsZero() &&
				workload.UpdatedAt.After(firstEvent) &&
				(workload.State == orchestration.WorkloadStateRunning || workload.State == orchestration.WorkloadStateCompleted) {
				mttrTotal += workload.UpdatedAt.Sub(firstEvent).Seconds()
				mttrSamples++
			}
		}
		if mttdSamples > 0 {
			mttdProxySeconds = mttdTotal / float64(mttdSamples)
		}
		if mttrSamples > 0 {
			mttrProxySeconds = mttrTotal / float64(mttrSamples)
		}
	}
	availabilitySLI := 1.0
	availabilityDenominator := float64(runningWorkloadsSLO + failedWorkloads + completedWorkloads)
	if availabilityDenominator > 0 {
		availabilitySLI = clampRange(1.0-(float64(failedWorkloads)/availabilityDenominator), 0, 1)
	}
	latencyComplianceSLI := 1.0
	if runningWorkloadsSLO > 0 {
		latencyComplianceSLI = clampRange(1.0-(float64(sloViolations)/float64(runningWorkloadsSLO)), 0, 1)
	}
	schedulingSuccessSLI := 1.0
	if schedulingAttempts > 0 {
		schedulingSuccessSLI = clampRange(1.0-(schedulingFailures/schedulingAttempts), 0, 1)
	}
	errorBudgetTargetSLI := 0.99
	errorBudgetBurnRate := 0.0
	errorBudgetRemainingPercent := 100.0
	if budgetRate := 1.0 - errorBudgetTargetSLI; budgetRate > 0 {
		errorBudgetBurnRate = (1.0 - latencyComplianceSLI) / budgetRate
		errorBudgetConsumedPercent := clampRange(errorBudgetBurnRate*100.0, 0, 1000)
		errorBudgetRemainingPercent = clampRange(100.0-errorBudgetConsumedPercent, 0, 100)
	}

	totalRestarts := int64(0)
	failedPods := 0
	for _, workload := range ctx.WorkloadPath.Workloads {
		totalRestarts += workload.ContainerRestarts
		failedPods += workload.PodsFailed
		if workload.PodsFailed > 0 || workload.ContainerRestarts > 0 || workload.Severity == "critical" {
			detail := "restarts=" + strconv.FormatInt(workload.ContainerRestarts, 10)
			if workload.PodsFailed > 0 {
				detail += " failed_pods=" + strconv.Itoa(workload.PodsFailed)
			}
			layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
				Kind:     "workload",
				ID:       aiInfraWorkloadIdentity(workload),
				Label:    workload.Namespace + "/" + workload.Name,
				Score:    workload.OverallScore + float64(workload.PodsFailed),
				Severity: workload.Severity,
				Detail:   detail,
			})
		}
	}

	checkpointObservedNodes := 0
	checkpointRiskNodes := 0
	for _, node := range ctx.Nodes {
		if node == nil || node.Metrics == nil {
			continue
		}
		if metricExists(node.Metrics, "node_checkpoint_write_latency_p99_seconds") {
			checkpointObservedNodes++
		}
		checkpointMs := metricValueOr(node.Metrics, "node_checkpoint_write_latency_p99_seconds") * 1000.0
		if checkpointMs >= 160 {
			checkpointRiskNodes++
		}
	}

	for _, finding := range ctx.RootCause.Findings {
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "finding",
			ID:       finding.ID,
			Label:    finding.Title,
			Score:    finding.Confidence * 10.0,
			Severity: finding.Severity,
			Detail:   finding.Hypothesis,
		})
	}

	workloadCount := aiInfraMaxInt(ctx.WorkloadPath.Summary.WorkloadCount, 1)
	totalWorkloads := ctx.WorkloadPath.Summary.WorkloadCount
	nodeCount := aiInfraMaxInt(len(ctx.Nodes), 1)
	score := clamp01(float64(ctx.RootCause.Summary.CriticalFindings)/4.0)*2.0 +
		clamp01(float64(ctx.RootCause.Summary.DegradedFindings)/6.0)*1.0 +
		clamp01(float64(sloViolations)/8.0)*2.0 +
		clamp01(float64(failedWorkloads)/6.0)*1.5 +
		clamp01(float64(totalRestarts)/float64(aiInfraMaxInt(workloadCount, 1)*10))*1.5 +
		clamp01(float64(checkpointRiskNodes)/float64(nodeCount))*1.2 +
		clamp01((100.0-errorBudgetRemainingPercent)/100.0)*0.8

	measurements := []aiInfraLayerMeasurement{
		{
			Name:   "SLI/SLO views",
			Metric: "orchestrator.{slo_violations_active,scheduling_attempts_total,scheduling_failures_total}",
			Source: "/api/v1/orchestration/diagnostics",
			Status: aiInfraStatusFromBool(ctx.OrchestrationDiag != nil),
		},
		{
			Name:   "Error budget burn proxy",
			Metric: "derived from latency_compliance_sli vs target_sli=0.99",
			Source: "/api/v1/orchestration/diagnostics",
			Status: aiInfraStatusFromCoverage(runningWorkloadsSLO, runningWorkloadsSLO),
		},
		{
			Name:   "RCA incident findings",
			Metric: "diagnostics.root_cause.findings",
			Source: "/api/v1/diagnostics/root-cause",
			Status: aiInfraMeasurementMeasured,
		},
		{
			Name:   "Checkpoint health",
			Metric: "node_checkpoint_write_latency_p99_seconds",
			Source: diagnosticMetricSource("node_checkpoint_write_latency_p99_seconds"),
			Status: aiInfraStatusFromCoverage(checkpointObservedNodes, nodeCount),
		},
		{
			Name:   "Worker crash/retry behavior",
			Metric: "pods_failed + container_restarts",
			Source: "k8s API workload status",
			Status: aiInfraStatusFromCoverage(totalWorkloads, totalWorkloads),
		},
		{
			Name:   "Incident timeline (MTTD/MTTR)",
			Metric: "orchestration events + workload timestamps",
			Source: "/api/v1/orchestration/status",
			Status: aiInfraStatusFromCoverage(orchestrationEventCount, orchestrationEventCount),
			Note:   "MTTD/MTTR are proxy values derived from workload created/updated times and healing events.",
		},
		{
			Name:   "MTTD/MTTR proxy samples",
			Metric: "mttd_proxy_seconds + mttr_proxy_seconds",
			Source: "/api/v1/orchestration/status",
			Status: aiInfraStatusFromCoverage(mttdSamples+mttrSamples, aiInfraMaxInt(orchestrationEventCount, 1)),
		},
	}
	sliErrorBudgetScore := clamp01((1.0-availabilitySLI)/0.05)*3.0 +
		clamp01((1.0-latencyComplianceSLI)/0.05)*4.0 +
		clamp01((100.0-errorBudgetRemainingPercent)/100.0)*3.0
	faultToleranceScore := clamp01(float64(failedWorkloads)/6.0)*3.0 +
		clamp01(float64(totalRestarts)/float64(aiInfraMaxInt(workloadCount, 1)*10))*2.5 +
		clamp01(float64(checkpointRiskNodes)/float64(nodeCount))*2.5 +
		clamp01(float64(remediationBlocked)/float64(aiInfraMaxInt(remediationActions, 1)))*2.0
	incidentLifecycleScore := clamp01(float64(ctx.RootCause.Summary.CriticalFindings)/4.0)*3.0 +
		clamp01(mttdProxySeconds/600.0)*2.5 +
		clamp01(mttrProxySeconds/1800.0)*2.5 +
		clamp01(float64(aiInfraMaxInt(orchestrationEventCount-remediationEventCount, 0))/float64(aiInfraMaxInt(orchestrationEventCount, 1)))*2.0
	layer.Domains = []aiInfraLayerDomain{
		{
			ID:              "sli_error_budget",
			Title:           "SLI compliance and error budget",
			Score:           clampRange(sliErrorBudgetScore, 0, 10),
			Severity:        pressureSeverity(sliErrorBudgetScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromBool(ctx.OrchestrationDiag != nil)}, {Status: aiInfraStatusFromCoverage(runningWorkloadsSLO, runningWorkloadsSLO)}}),
			Signals: map[string]float64{
				"availability_sli":       availabilitySLI,
				"latency_compliance_sli": latencyComplianceSLI,
				"scheduling_success_sli": schedulingSuccessSLI,
				"error_budget_remaining": errorBudgetRemainingPercent,
				"error_budget_burn_rate": errorBudgetBurnRate,
			},
			Sources: map[string]string{
				"availability_sli":       "/api/v1/orchestration/diagnostics",
				"latency_compliance_sli": "/api/v1/orchestration/diagnostics",
				"scheduling_success_sli": "/api/v1/orchestration/diagnostics",
				"error_budget_remaining": "/api/v1/orchestration/diagnostics",
				"error_budget_burn_rate": "/api/v1/orchestration/diagnostics",
			},
		},
		{
			ID:              "fault_tolerance_recovery",
			Title:           "Fault tolerance and recovery",
			Score:           clampRange(faultToleranceScore, 0, 10),
			Severity:        pressureSeverity(faultToleranceScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(checkpointObservedNodes, nodeCount)}, {Status: aiInfraStatusFromCoverage(totalWorkloads, totalWorkloads)}}),
			Signals: map[string]float64{
				"failed_workloads":          float64(failedWorkloads),
				"failed_pods":               float64(failedPods),
				"container_restarts_total":  float64(totalRestarts),
				"checkpoint_risk_nodes":     float64(checkpointRiskNodes),
				"remediation_actions_total": float64(remediationActions),
				"remediation_blocked_total": float64(remediationBlocked),
			},
			Sources: map[string]string{
				"failed_workloads":          "/api/v1/orchestration/diagnostics",
				"failed_pods":               "k8s api /api/v1/pods",
				"container_restarts_total":  "k8s api /api/v1/pods",
				"checkpoint_risk_nodes":     diagnosticMetricSource("node_checkpoint_write_latency_p99_seconds"),
				"remediation_actions_total": "/api/v1/orchestration/diagnostics",
				"remediation_blocked_total": "/api/v1/orchestration/diagnostics",
			},
		},
		{
			ID:              "incident_lifecycle_rca",
			Title:           "Incident lifecycle and RCA closure",
			Score:           clampRange(incidentLifecycleScore, 0, 10),
			Severity:        pressureSeverity(incidentLifecycleScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraMeasurementMeasured}, {Status: aiInfraStatusFromCoverage(orchestrationEventCount, orchestrationEventCount)}, {Status: aiInfraStatusFromCoverage(mttdSamples+mttrSamples, aiInfraMaxInt(orchestrationEventCount, 1))}}),
			Signals: map[string]float64{
				"critical_findings":        float64(ctx.RootCause.Summary.CriticalFindings),
				"degraded_findings":        float64(ctx.RootCause.Summary.DegradedFindings),
				"incident_timeline_events": float64(orchestrationEventCount),
				"incident_timeline_span_s": timelineSpanSeconds,
				"mttd_proxy_seconds":       mttdProxySeconds,
				"mttr_proxy_seconds":       mttrProxySeconds,
				"remediation_events":       float64(remediationEventCount),
			},
			Sources: map[string]string{
				"critical_findings":        "/api/v1/diagnostics/root-cause",
				"incident_timeline_events": "/api/v1/orchestration/status",
				"mttd_proxy_seconds":       "/api/v1/orchestration/status",
				"mttr_proxy_seconds":       "/api/v1/orchestration/status",
				"remediation_events":       "/api/v1/orchestration/status",
			},
		},
	}

	layer.Score = clampRange(score, 0, 10)
	layer.Severity = pressureSeverity(layer.Score)
	measurements = aiInfraClassifyMeasurementMethods(measurements)
	layer.Measurements = measurements
	layer.CoveragePercent = aiInfraCoveragePercent(measurements)
	layer.Signals = map[string]float64{
		"critical_findings":          float64(ctx.RootCause.Summary.CriticalFindings),
		"degraded_findings":          float64(ctx.RootCause.Summary.DegradedFindings),
		"slo_violations_active":      float64(sloViolations),
		"failed_workloads":           float64(failedWorkloads),
		"failed_pods":                float64(failedPods),
		"container_restarts_total":   float64(totalRestarts),
		"checkpoint_risk_nodes":      float64(checkpointRiskNodes),
		"checkpoint_observed_nodes":  float64(checkpointObservedNodes),
		"remediation_actions_total":  float64(remediationActions),
		"remediation_blocked_total":  float64(remediationBlocked),
		"availability_sli":           availabilitySLI,
		"latency_compliance_sli":     latencyComplianceSLI,
		"scheduling_success_sli":     schedulingSuccessSLI,
		"error_budget_remaining":     errorBudgetRemainingPercent,
		"error_budget_burn_rate":     errorBudgetBurnRate,
		"incident_timeline_events":   float64(orchestrationEventCount),
		"incident_timeline_span_sec": timelineSpanSeconds,
		"mttd_proxy_seconds":         mttdProxySeconds,
		"mttr_proxy_seconds":         mttrProxySeconds,
		"mttd_samples":               float64(mttdSamples),
		"mttr_samples":               float64(mttrSamples),
		"running_workloads":          float64(runningWorkloadsSLO),
		"completed_workloads":        float64(completedWorkloads),
		"scheduling_attempts_total":  schedulingAttempts,
		"scheduling_failures_total":  schedulingFailures,
		"remediation_events":         float64(remediationEventCount),
	}
	layer.Sources = map[string]string{
		"critical_findings":          "/api/v1/diagnostics/root-cause",
		"slo_violations_active":      "/api/v1/orchestration/diagnostics",
		"failed_pods":                "k8s api /api/v1/pods",
		"container_restarts_total":   "k8s api /api/v1/pods",
		"checkpoint_risk_nodes":      diagnosticMetricSource("node_checkpoint_write_latency_p99_seconds"),
		"availability_sli":           "/api/v1/orchestration/diagnostics",
		"latency_compliance_sli":     "/api/v1/orchestration/diagnostics",
		"scheduling_success_sli":     "/api/v1/orchestration/diagnostics",
		"error_budget_remaining":     "/api/v1/orchestration/diagnostics",
		"incident_timeline_span_sec": "/api/v1/orchestration/status",
		"mttd_proxy_seconds":         "/api/v1/orchestration/status",
		"mttr_proxy_seconds":         "/api/v1/orchestration/status",
	}

	risks := []string{}
	if ctx.RootCause.Summary.CriticalFindings > 0 {
		risks = append(risks, "Critical root-cause findings remain unresolved in current window.")
	}
	if sloViolations > 0 {
		risks = append(risks, "Active SLO violations are impacting reliability posture.")
	}
	if failedPods > 0 || totalRestarts > 0 {
		risks = append(risks, "Crash/restart behavior indicates unstable workloads.")
	}
	if checkpointRiskNodes > 0 {
		risks = append(risks, "Checkpoint latency risk can undermine recovery guarantees.")
	}
	if errorBudgetRemainingPercent < 60 {
		risks = append(risks, "Error budget burn proxy indicates reliability headroom is shrinking.")
	}
	if orchestrationEventCount > 0 && mttdProxySeconds >= 300 {
		risks = append(risks, "Detection proxy (MTTD) is elevated for healing events in this scope.")
	}
	if orchestrationEventCount > 0 && mttrProxySeconds >= 900 {
		risks = append(risks, "Recovery proxy (MTTR) is elevated for remediated workloads.")
	}
	if orchestrationEventCount == 0 {
		risks = append(risks, "No orchestration event timeline is available for incident sequencing.")
	}
	if ctx.OrchestrationDiag == nil {
		risks = append(risks, "No orchestration diagnostics available for error-budget style tracking.")
	}
	layer.TopRisks = dedupeStrings(risks)
	layer.ObservabilityGap = dedupeStrings([]string{
		"MTTD/MTTR values are runtime proxies and not persisted incident lifecycle fields.",
		"Error-budget computations use SLO proxy signals and require stronger SLI policy integration for production governance.",
	})
	aiInfraSortRankedEntities(layer.RankedEntities)
	if len(layer.RankedEntities) > aiInfraRankLimit {
		layer.RankedEntities = layer.RankedEntities[:aiInfraRankLimit]
	}
	return layer
}

func aiInfraServingLayer(ctx aiInfraSynthesisContext) aiInfraLayerDiagnostics {
	layer := aiInfraLayerDiagnostics{
		ID:             "serving_inference",
		Title:          "Serving and inference scheduling",
		Scope:          "service+route",
		Signals:        map[string]float64{},
		Sources:        map[string]string{},
		RankedEntities: []aiInfraRankedEntity{},
		Troubleshooting: []string{
			"Track inference workloads separately from training jobs when ranking tail-latency risk.",
			"Use route latency estimates and queue-delay signals before scaling replicas.",
			"Confirm batching hints and model placement after every remediation change.",
		},
	}

	inferenceWorkloads := make([]workloadPathDiagnosticsWorkload, 0)
	servingPendingPods := 0
	for _, workload := range ctx.WorkloadPath.Workloads {
		if aiInfraIsInferenceWorkload(workload) {
			inferenceWorkloads = append(inferenceWorkloads, workload)
			servingPendingPods += workload.PodsPending
		}
	}

	realtimeQueued := 0
	realtimeWorkloadCount := 0
	avgRealtimeQueueDelay := 0.0
	realtimeQueueCount := 0
	batchSuggestionCount := 0
	avgSuggestedBatch := 0.0
	routeLatencyCount := 0
	avgRouteLatency := 0.0
	routeTargetCount := 0
	routeCount := 0
	modelPlacementCount := 0
	kvCacheSignalNodes := 0
	kvCachePressureNodes := 0
	kvCacheUtilAvg := 0.0

	for _, node := range ctx.Nodes {
		if node == nil || node.Metrics == nil {
			continue
		}
		metrics := node.Metrics
		if !metricExists(metrics,
			"node_inference_kv_cache_utilization_percent",
			"node_kv_cache_pressure_percent",
			"node_kv_cache_evictions_per_second",
		) {
			continue
		}
		kvCacheSignalNodes++
		kvUtil := metricValueOr(metrics, "node_inference_kv_cache_utilization_percent", "node_kv_cache_pressure_percent")
		kvEvictions := metricValueOr(metrics, "node_kv_cache_evictions_per_second")
		kvCacheUtilAvg += kvUtil
		if kvUtil >= 85 || kvEvictions > 0 {
			kvCachePressureNodes++
		}
	}
	if kvCacheSignalNodes > 0 {
		kvCacheUtilAvg /= float64(kvCacheSignalNodes)
	}

	if ctx.OrchestrationSnapshot != nil {
		for _, workload := range ctx.OrchestrationSnapshot.Workloads {
			if workload.Spec.Model != "" {
				modelPlacementCount++
			}
			if workload.Spec.Class == orchestration.WorkloadClassRealtime {
				realtimeWorkloadCount++
				if workload.State == orchestration.WorkloadStateQueued || workload.State == orchestration.WorkloadStateDeferred {
					realtimeQueued++
				}
				if workload.QueueDelaySeconds > 0 {
					avgRealtimeQueueDelay += workload.QueueDelaySeconds
					realtimeQueueCount++
				}
			}
		}
		for _, route := range ctx.OrchestrationSnapshot.Routes {
			routeCount++
			if strings.TrimSpace(route.Model) != "" {
				modelPlacementCount++
			}
			for _, target := range route.Targets {
				routeTargetCount++
				if target.SuggestedBatchSize > 0 {
					avgSuggestedBatch += float64(target.SuggestedBatchSize)
					batchSuggestionCount++
				}
				if target.EstimatedLatencyMs > 0 {
					avgRouteLatency += target.EstimatedLatencyMs
					routeLatencyCount++
				}
				label := strings.TrimSpace(route.Service)
				if label == "" {
					label = route.Model
				}
				if label == "" {
					label = target.NodeID
				}
				if label == "" {
					continue
				}
				score := target.EstimatedLatencyMs
				if score <= 0 {
					score = float64(target.SuggestedBatchSize)
				}
				layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
					Kind:     "route_target",
					ID:       target.NodeID,
					Label:    label,
					Score:    score,
					Severity: pressureSeverity(score / 40.0),
					Detail:   "latency_ms=" + strconv.FormatFloat(target.EstimatedLatencyMs, 'f', 1, 64),
				})
			}
		}
	}
	if realtimeQueueCount > 0 {
		avgRealtimeQueueDelay /= float64(realtimeQueueCount)
	}
	if batchSuggestionCount > 0 {
		avgSuggestedBatch /= float64(batchSuggestionCount)
	}
	if routeLatencyCount > 0 {
		avgRouteLatency /= float64(routeLatencyCount)
	}

	for _, workload := range inferenceWorkloads {
		layer.RankedEntities = append(layer.RankedEntities, aiInfraRankedEntity{
			Kind:     "workload",
			ID:       aiInfraWorkloadIdentity(workload),
			Label:    workload.Namespace + "/" + workload.Name,
			Score:    workload.OverallScore + float64(workload.PodsPending),
			Severity: workload.Severity,
			Detail:   "inference workload",
		})
	}

	score := clamp01(float64(servingPendingPods)/float64(aiInfraMaxInt(len(inferenceWorkloads)*4, 1)))*2.0 +
		clamp01(float64(realtimeQueued)/8.0)*2.0 +
		clamp01(avgRealtimeQueueDelay/120.0)*2.0 +
		clamp01(avgRouteLatency/250.0)*2.0 +
		clamp01(float64(modelPlacementCount)/20.0)*1.0 +
		clamp01(kvCacheUtilAvg/95.0)*1.0
	modelSignalExpected := 0
	if ctx.OrchestrationSnapshot != nil {
		modelSignalExpected = len(ctx.OrchestrationSnapshot.Workloads) + routeCount
	}

	measurements := []aiInfraLayerMeasurement{
		{
			Name:   "Route latency estimate",
			Metric: "route_target.estimated_latency_ms",
			Source: "/api/v1/orchestration/routes",
			Status: aiInfraStatusFromCoverage(routeLatencyCount, routeTargetCount),
		},
		{
			Name:   "Batch-size hints",
			Metric: "route_target.suggested_batch_size",
			Source: "/api/v1/orchestration/routes",
			Status: aiInfraStatusFromCoverage(batchSuggestionCount, routeTargetCount),
		},
		{
			Name:   "Model placement visibility",
			Metric: "workload.spec.model + routing targets",
			Source: "/api/v1/orchestration/workloads + /api/v1/orchestration/routes",
			Status: aiInfraStatusFromCoverage(modelPlacementCount, modelSignalExpected),
		},
		{
			Name:   "Inference queue delay",
			Metric: "realtime workload queue_delay_seconds",
			Source: "/api/v1/orchestration/workloads",
			Status: aiInfraStatusFromCoverage(realtimeQueueCount, realtimeWorkloadCount),
		},
		{
			Name:   "KV-cache pressure",
			Metric: "node_inference_kv_cache_utilization_percent",
			Source: "runtime/serving exporter counters (if integrated)",
			Status: aiInfraStatusFromCoverage(kvCacheSignalNodes, aiInfraMaxInt(len(ctx.Nodes), 1)),
		},
	}
	queueTailScore := clamp01(float64(servingPendingPods)/float64(aiInfraMaxInt(len(inferenceWorkloads)*4, 1)))*3.0 +
		clamp01(float64(realtimeQueued)/8.0)*3.0 +
		clamp01(avgRealtimeQueueDelay/120.0)*2.0 +
		clamp01(avgRouteLatency/250.0)*2.0
	batchCoverageGap := 1.0 - clamp01(float64(batchSuggestionCount)/float64(aiInfraMaxInt(routeTargetCount, 1)))
	modelPlacementGap := 1.0 - clamp01(float64(modelPlacementCount)/float64(aiInfraMaxInt(modelSignalExpected, 1)))
	batchingPlacementScore := clamp01(batchCoverageGap)*4.0 +
		clamp01(modelPlacementGap)*3.0 +
		clamp01(avgRouteLatency/250.0)*3.0
	kvCacheScore := clamp01(kvCacheUtilAvg/95.0)*6.0 +
		clamp01(float64(kvCachePressureNodes)/float64(aiInfraMaxInt(kvCacheSignalNodes, 1)))*4.0
	layer.Domains = []aiInfraLayerDomain{
		{
			ID:              "queueing_tail_latency",
			Title:           "Queueing and tail latency",
			Score:           clampRange(queueTailScore, 0, 10),
			Severity:        pressureSeverity(queueTailScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(realtimeQueueCount, realtimeWorkloadCount)}, {Status: aiInfraStatusFromCoverage(routeLatencyCount, routeTargetCount)}}),
			Signals: map[string]float64{
				"inference_pending_pods":       float64(servingPendingPods),
				"realtime_queued_workloads":    float64(realtimeQueued),
				"avg_realtime_queue_delay_sec": avgRealtimeQueueDelay,
				"avg_route_latency_ms":         avgRouteLatency,
			},
			Sources: map[string]string{
				"inference_pending_pods":       "/api/v1/diagnostics/workload-path",
				"avg_realtime_queue_delay_sec": "/api/v1/orchestration/workloads",
				"avg_route_latency_ms":         "/api/v1/orchestration/routes",
			},
		},
		{
			ID:              "batching_model_placement",
			Title:           "Batching and model placement",
			Score:           clampRange(batchingPlacementScore, 0, 10),
			Severity:        pressureSeverity(batchingPlacementScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(batchSuggestionCount, routeTargetCount)}, {Status: aiInfraStatusFromCoverage(modelPlacementCount, modelSignalExpected)}}),
			Signals: map[string]float64{
				"batch_size_samples":      float64(batchSuggestionCount),
				"route_target_samples":    float64(routeTargetCount),
				"model_placement_signals": float64(modelPlacementCount),
				"model_signal_expected":   float64(modelSignalExpected),
				"batch_coverage_gap":      batchCoverageGap,
				"model_placement_gap":     modelPlacementGap,
			},
			Sources: map[string]string{
				"batch_size_samples":      "/api/v1/orchestration/routes",
				"model_placement_signals": "/api/v1/orchestration/workloads + /api/v1/orchestration/routes",
				"batch_coverage_gap":      "/api/v1/orchestration/routes",
				"model_placement_gap":     "/api/v1/orchestration/workloads + /api/v1/orchestration/routes",
			},
		},
		{
			ID:              "kv_cache_pressure",
			Title:           "KV-cache and inference memory pressure",
			Score:           clampRange(kvCacheScore, 0, 10),
			Severity:        pressureSeverity(kvCacheScore),
			CoveragePercent: aiInfraCoveragePercent([]aiInfraLayerMeasurement{{Status: aiInfraStatusFromCoverage(kvCacheSignalNodes, aiInfraMaxInt(len(ctx.Nodes), 1))}}),
			Signals: map[string]float64{
				"kv_cache_signal_nodes":    float64(kvCacheSignalNodes),
				"kv_cache_pressure_nodes":  float64(kvCachePressureNodes),
				"kv_cache_utilization_avg": kvCacheUtilAvg,
			},
			Sources: map[string]string{
				"kv_cache_signal_nodes":    "runtime/serving exporter counters (if integrated)",
				"kv_cache_pressure_nodes":  "runtime/serving exporter counters (if integrated)",
				"kv_cache_utilization_avg": "runtime/serving exporter counters (if integrated)",
			},
		},
	}

	layer.Score = clampRange(score, 0, 10)
	layer.Severity = pressureSeverity(layer.Score)
	measurements = aiInfraClassifyMeasurementMethods(measurements)
	layer.Measurements = measurements
	layer.CoveragePercent = aiInfraCoveragePercent(measurements)
	layer.Signals = map[string]float64{
		"inference_workloads":          float64(len(inferenceWorkloads)),
		"inference_pending_pods":       float64(servingPendingPods),
		"realtime_queued_workloads":    float64(realtimeQueued),
		"avg_realtime_queue_delay_sec": avgRealtimeQueueDelay,
		"avg_route_latency_ms":         avgRouteLatency,
		"avg_suggested_batch_size":     avgSuggestedBatch,
		"route_latency_samples":        float64(routeLatencyCount),
		"batch_size_samples":           float64(batchSuggestionCount),
		"route_target_samples":         float64(routeTargetCount),
		"realtime_workloads":           float64(realtimeWorkloadCount),
		"route_count":                  float64(routeCount),
		"model_placement_signals":      float64(modelPlacementCount),
		"kv_cache_signal_nodes":        float64(kvCacheSignalNodes),
		"kv_cache_pressure_nodes":      float64(kvCachePressureNodes),
		"kv_cache_utilization_avg":     kvCacheUtilAvg,
	}
	layer.Sources = map[string]string{
		"inference_workloads":          "/api/v1/diagnostics/workload-path",
		"avg_realtime_queue_delay_sec": "/api/v1/orchestration/workloads",
		"avg_route_latency_ms":         "/api/v1/orchestration/routes",
		"avg_suggested_batch_size":     "/api/v1/orchestration/routes",
		"kv_cache_utilization_avg":     "runtime/serving exporter counters (if integrated)",
	}

	risks := []string{}
	if len(inferenceWorkloads) == 0 && routeLatencyCount == 0 {
		risks = append(risks, "No measurable inference-serving workload in current scope.")
	}
	if servingPendingPods > 0 || realtimeQueued > 0 {
		risks = append(risks, "Inference queueing pressure can increase tail latency.")
	}
	if avgRouteLatency >= 120 {
		risks = append(risks, "Estimated route latency is elevated for serving targets.")
	}
	if batchSuggestionCount == 0 {
		risks = append(risks, "Batching signals are missing; throughput/tail-latency tradeoff is under-observed.")
	}
	if kvCachePressureNodes > 0 {
		risks = append(risks, "KV-cache pressure is elevated and may drive tail-latency spikes.")
	}
	layer.TopRisks = dedupeStrings(risks)
	layer.ObservabilityGap = dedupeStrings([]string{
		"KV-cache visibility depends on serving-runtime exporter counters and may be missing.",
		"Per-request token-level latency is not part of current ingestion schema.",
	})
	aiInfraSortRankedEntities(layer.RankedEntities)
	if len(layer.RankedEntities) > aiInfraRankLimit {
		layer.RankedEntities = layer.RankedEntities[:aiInfraRankLimit]
	}
	return layer
}

type aiInfraWorkloadMatch struct {
	Workload workloadPathDiagnosticsWorkload
	Score    float64
	Reason   string
}

func aiInfraBuildIncidentDrilldowns(ctx aiInfraSynthesisContext, limit int) []aiInfraIncidentDrilldown {
	if len(ctx.RootCause.Findings) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = aiInfraRankLimit
	}

	drilldowns := make([]aiInfraIncidentDrilldown, 0, aiInfraMinInt(limit, len(ctx.RootCause.Findings)))
	for _, finding := range ctx.RootCause.Findings {
		drilldown := aiInfraIncidentDrilldown{
			FindingID:     finding.ID,
			FindingTitle:  finding.Title,
			Category:      finding.Category,
			Severity:      finding.Severity,
			Confidence:    finding.Confidence,
			Workflow:      "incident -> workload -> placement -> contention",
			AffectedNodes: aiInfraFindingNodeLabels(finding),
			Contention:    aiInfraFindingContentionSignals(finding, 6),
			Triage: dedupeStrings(append([]string{
				"Start with finding evidence, then inspect mapped workloads and placement hops in the same time window.",
				"Validate the top placement node against process-level and kernel-path pressure before mitigation.",
			}, finding.Actions...)),
		}

		workloadMatches := aiInfraMatchWorkloadsForFinding(finding, ctx.WorkloadPath.Workloads)
		workloadHops := make([]aiInfraIncidentWorkloadHop, 0, aiInfraRankLimit)
		placementHops := make([]aiInfraIncidentPlacementHop, 0, aiInfraRankLimit)
		for _, match := range workloadMatches {
			if len(workloadHops) >= aiInfraRankLimit {
				break
			}
			workload := match.Workload
			orchestrated := aiInfraFindOrchestrationWorkloadForPath(ctx.OrchestrationSnapshot, workload)
			queueDelay := 0.0
			if orchestrated != nil {
				queueDelay = orchestrated.QueueDelaySeconds
			}

			workloadHops = append(workloadHops, aiInfraIncidentWorkloadHop{
				ID:                aiInfraWorkloadIdentity(workload),
				Cluster:           workload.Cluster,
				Namespace:         workload.Namespace,
				Kind:              workload.Kind,
				Name:              workload.Name,
				Service:           workload.Service,
				Severity:          workload.Severity,
				Bottleneck:        workload.Bottleneck,
				QueueDelaySeconds: queueDelay,
				PodsPending:       workload.PodsPending,
				PodsFailed:        workload.PodsFailed,
				NodeCount:         workload.NodeCount,
				ResolvedNodes:     workload.ResolvedNodes,
				GPURequests:       workload.GPURequests,
				Risks:             workload.Risks,
				Reason:            match.Reason,
			})

			placementHops = append(placementHops, aiInfraIncidentPlacementsForWorkload(finding, workload, orchestrated)...)
		}
		aiInfraSortIncidentPlacementHops(placementHops)
		if len(placementHops) > aiInfraRankLimit {
			placementHops = placementHops[:aiInfraRankLimit]
		}

		drilldown.Workloads = workloadHops
		drilldown.Placements = placementHops
		if len(drilldown.Workloads) == 0 && len(drilldown.Contention) == 0 {
			continue
		}
		drilldowns = append(drilldowns, drilldown)
		if len(drilldowns) >= limit {
			break
		}
	}
	if len(drilldowns) == 0 {
		return nil
	}
	return drilldowns
}

func aiInfraFindingNodeLabels(finding rootCauseFinding) []string {
	if len(finding.AffectedNodes) == 0 {
		return nil
	}
	out := make([]string, 0, len(finding.AffectedNodes))
	for _, node := range finding.AffectedNodes {
		label := strings.TrimSpace(node.Hostname)
		if label == "" {
			label = strings.TrimSpace(node.CollectorID)
		}
		if label == "" {
			continue
		}
		out = append(out, label)
	}
	return dedupeStrings(out)
}

func aiInfraFindingContentionSignals(finding rootCauseFinding, limit int) []aiInfraIncidentSignal {
	if limit <= 0 {
		limit = 4
	}
	signals := make([]aiInfraIncidentSignal, 0, aiInfraMinInt(limit, len(finding.Evidence)))
	for _, evidence := range finding.Evidence {
		if len(signals) >= limit {
			break
		}
		source := strings.TrimSpace(evidence.Source)
		if source == "" {
			source = diagnosticMetricSource(evidence.Signal)
		}
		signals = append(signals, aiInfraIncidentSignal{
			Name:      strings.TrimSpace(evidence.Signal),
			Value:     evidence.Value,
			Source:    source,
			Collector: strings.TrimSpace(evidence.CollectorID),
			Hostname:  strings.TrimSpace(evidence.Hostname),
		})
	}
	return signals
}

func aiInfraMatchWorkloadsForFinding(finding rootCauseFinding, workloads []workloadPathDiagnosticsWorkload) []aiInfraWorkloadMatch {
	if len(workloads) == 0 {
		return nil
	}
	domain := aiInfraFindingDomain(finding)
	affectedCollectorSet, affectedHostSet := aiInfraFindingNodeSets(finding)

	matches := make([]aiInfraWorkloadMatch, 0, len(workloads))
	for _, workload := range workloads {
		score := 0.0
		reasons := make([]string, 0, 4)
		switch domain {
		case "network":
			if workload.Bottleneck == "network" {
				score += 3.0
				reasons = append(reasons, "network bottleneck")
			}
			if containsString(workload.Risks, "communication_imbalance") || containsString(workload.Risks, "cross_node_spread") {
				score += 2.0
				reasons = append(reasons, "cross-node communication risk")
			}
		case "collective":
			if workload.Bottleneck == "network" {
				score += 2.5
				reasons = append(reasons, "network bottleneck")
			}
			if containsString(workload.Risks, "communication_imbalance") || containsString(workload.Risks, "cross_node_spread") {
				score += 2.5
				reasons = append(reasons, "collective communication imbalance")
			}
			if containsString(workload.Risks, "scheduler_contention") {
				score += 1.5
				reasons = append(reasons, "collective worker scheduler contention")
			}
			if workload.NetworkScore >= workload.StorageScore && workload.NetworkScore >= workload.ComputeScore && workload.NetworkScore >= 3.0 {
				score += 1.0
				reasons = append(reasons, "network-dominant pressure")
			}
			if workload.GPURequests > 0 {
				score += 0.5
				reasons = append(reasons, "accelerator collective path")
			}
		case "storage":
			if workload.Bottleneck == "storage" {
				score += 3.0
				reasons = append(reasons, "storage bottleneck")
			}
			if containsString(workload.Risks, "storage_collapse_iowait") || containsString(workload.Risks, "gpu_starvation_due_to_io_or_network") {
				score += 2.0
				reasons = append(reasons, "storage stall risk")
			}
		case "scheduler":
			if containsString(workload.Risks, "scheduler_contention") {
				score += 3.0
				reasons = append(reasons, "scheduler contention risk")
			}
			if containsString(workload.Risks, "k8s_scheduling_pressure") || workload.PodsPending > 0 {
				score += 2.0
				reasons = append(reasons, "scheduling pressure")
			}
		case "memory":
			if workload.StorageScore >= 3.0 {
				score += 1.5
				reasons = append(reasons, "io amplification proxy")
			}
			if containsString(workload.Risks, "gpu_starvation_due_to_io_or_network") {
				score += 1.5
				reasons = append(reasons, "memory/data-feed stall risk")
			}
		case "observability":
			if workload.ResolvedNodes == 0 {
				score += 1.0
				reasons = append(reasons, "telemetry mapping gap")
			}
		}

		if workload.Severity == "critical" {
			score += 1.0
		} else if workload.Severity == "degraded" {
			score += 0.5
		}
		if workload.PodsPending > 0 || workload.PodsFailed > 0 {
			score += 0.5
		}
		if aiInfraWorkloadIntersectsFindingNodes(workload, affectedCollectorSet, affectedHostSet) {
			score += 2.0
			reasons = append(reasons, "affected-node overlap")
		}
		if score <= 0 {
			continue
		}
		matches = append(matches, aiInfraWorkloadMatch{
			Workload: workload,
			Score:    score,
			Reason:   strings.Join(dedupeStrings(reasons), ", "),
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		leftRank := aiInfraSeverityRank(matches[i].Workload.Severity)
		rightRank := aiInfraSeverityRank(matches[j].Workload.Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if matches[i].Workload.OverallScore != matches[j].Workload.OverallScore {
			return matches[i].Workload.OverallScore > matches[j].Workload.OverallScore
		}
		return aiInfraWorkloadIdentity(matches[i].Workload) < aiInfraWorkloadIdentity(matches[j].Workload)
	})
	return matches
}

func aiInfraFindingDomain(finding rootCauseFinding) string {
	category := strings.ToLower(strings.TrimSpace(finding.Category))
	switch {
	case strings.Contains(category, "collective"):
		return "collective"
	case strings.Contains(category, "network"):
		return "network"
	case strings.Contains(category, "storage"):
		return "storage"
	case strings.Contains(category, "scheduler"):
		return "scheduler"
	case strings.Contains(category, "memory"), strings.Contains(category, "io"):
		return "memory"
	case strings.Contains(category, "observability"), strings.Contains(category, "probe"):
		return "observability"
	default:
		id := strings.ToLower(strings.TrimSpace(finding.ID))
		switch {
		case strings.Contains(id, "collective"), strings.Contains(id, "allreduce"), strings.Contains(id, "sync_cost"):
			return "collective"
		case strings.Contains(id, "network"), strings.Contains(id, "communication"):
			return "network"
		case strings.Contains(id, "storage"), strings.Contains(id, "checkpoint"):
			return "storage"
		case strings.Contains(id, "scheduler"), strings.Contains(id, "latency"):
			return "scheduler"
		case strings.Contains(id, "memory"), strings.Contains(id, "io"):
			return "memory"
		case strings.Contains(id, "probe"), strings.Contains(id, "observability"):
			return "observability"
		default:
			return "generic"
		}
	}
}

func aiInfraFindingNodeSets(finding rootCauseFinding) (map[string]struct{}, map[string]struct{}) {
	collectors := make(map[string]struct{}, len(finding.AffectedNodes))
	hosts := make(map[string]struct{}, len(finding.AffectedNodes))
	for _, node := range finding.AffectedNodes {
		if collector := strings.TrimSpace(node.CollectorID); collector != "" {
			collectors[strings.ToLower(collector)] = struct{}{}
		}
		if host := strings.TrimSpace(node.Hostname); host != "" {
			hosts[strings.ToLower(host)] = struct{}{}
		}
	}
	return collectors, hosts
}

func aiInfraWorkloadIntersectsFindingNodes(
	workload workloadPathDiagnosticsWorkload,
	affectedCollectors map[string]struct{},
	affectedHosts map[string]struct{},
) bool {
	if len(affectedCollectors) == 0 && len(affectedHosts) == 0 {
		return false
	}
	for _, node := range workload.Nodes {
		if collector := strings.ToLower(strings.TrimSpace(node.CollectorID)); collector != "" {
			if _, ok := affectedCollectors[collector]; ok {
				return true
			}
		}
		if host := strings.ToLower(strings.TrimSpace(node.Hostname)); host != "" {
			if _, ok := affectedHosts[host]; ok {
				return true
			}
		}
	}
	return false
}

func aiInfraFindOrchestrationWorkloadForPath(
	snapshot *orchestration.Snapshot,
	workload workloadPathDiagnosticsWorkload,
) *orchestration.Workload {
	if snapshot == nil || len(snapshot.Workloads) == 0 {
		return nil
	}
	service := strings.ToLower(strings.TrimSpace(workload.Service))
	name := strings.ToLower(strings.TrimSpace(workload.Name))
	namespace := strings.ToLower(strings.TrimSpace(workload.Namespace))

	var best *orchestration.Workload
	bestScore := 0.0
	for i := range snapshot.Workloads {
		candidate := &snapshot.Workloads[i]
		score := 0.0
		candidateService := strings.ToLower(strings.TrimSpace(candidate.Spec.Service))
		if service != "" && candidateService == service {
			score += 3.0
		}
		candidateID := strings.ToLower(strings.TrimSpace(candidate.Spec.ID))
		if name != "" && candidateID != "" && strings.Contains(candidateID, name) {
			score += 2.0
		}
		if namespace != "" && candidateID != "" && strings.Contains(candidateID, namespace) {
			score += 1.0
		}
		if workload.GPURequests > 0 && candidate.Spec.Requested.GPUCards > 0 {
			score += 1.0
		}
		if candidate.State == orchestration.WorkloadStateQueued || candidate.State == orchestration.WorkloadStateDeferred {
			score += 0.5
		}
		if score > bestScore {
			bestScore = score
			best = candidate
		}
	}
	if bestScore <= 0 {
		return nil
	}
	return best
}

func aiInfraIncidentPlacementsForWorkload(
	finding rootCauseFinding,
	workload workloadPathDiagnosticsWorkload,
	orchestrated *orchestration.Workload,
) []aiInfraIncidentPlacementHop {
	affectedCollectors, affectedHosts := aiInfraFindingNodeSets(finding)
	placements := make([]aiInfraIncidentPlacementHop, 0, aiInfraMaxInt(len(workload.Nodes), 1))
	queueDelay := 0.0
	nodeCollectorByName := make(map[string]string, len(workload.Nodes)*2)
	nodeHostByName := make(map[string]string, len(workload.Nodes)*2)
	for _, node := range workload.Nodes {
		collector := strings.TrimSpace(node.CollectorID)
		host := strings.TrimSpace(node.Hostname)
		for _, raw := range []string{node.NodeName, node.Hostname} {
			key := strings.ToLower(strings.TrimSpace(raw))
			if key == "" {
				continue
			}
			if collector != "" {
				nodeCollectorByName[key] = collector
			}
			if host != "" {
				nodeHostByName[key] = host
			}
		}
	}
	if orchestrated != nil {
		queueDelay = orchestrated.QueueDelaySeconds
	}

	if orchestrated != nil && len(orchestrated.Assignments) > 0 {
		for _, assignment := range orchestrated.Assignments {
			score := clamp01(assignment.EstimatedLatencyMs/220.0)*4.0 +
				clamp01(queueDelay/180.0)*2.0 +
				clamp01(assignment.Reserved.NetworkMbps/200000.0)*1.5 +
				clamp01(assignment.Reserved.StorageIOPS/250000.0)*1.0
			if _, ok := affectedHosts[strings.ToLower(strings.TrimSpace(assignment.NodeID))]; ok {
				score += 1.2
			}
			signals := compactPositiveSignals(map[string]float64{
				"estimated_latency_ms":  assignment.EstimatedLatencyMs,
				"reserved_gpu_cards":    assignment.Reserved.GPUCards,
				"reserved_network_mbps": assignment.Reserved.NetworkMbps,
				"reserved_storage_iops": assignment.Reserved.StorageIOPS,
			})
			nodeLookup := strings.ToLower(strings.TrimSpace(assignment.NodeID))
			collector := strings.TrimSpace(nodeCollectorByName[nodeLookup])
			host := aiInfraFirstNonEmpty(strings.TrimSpace(nodeHostByName[nodeLookup]), strings.TrimSpace(assignment.NodeID))
			placements = append(placements, aiInfraIncidentPlacementHop{
				WorkloadID:        aiInfraWorkloadIdentity(workload),
				NodeID:            strings.TrimSpace(assignment.NodeID),
				CollectorID:       collector,
				Hostname:          host,
				Cluster:           strings.TrimSpace(assignment.Cluster),
				Zone:              strings.TrimSpace(assignment.Zone),
				Score:             score,
				Severity:          pressureSeverity(score),
				QueueDelaySeconds: queueDelay,
				Signals:           signals,
				Reason:            aiInfraFirstNonEmpty(strings.TrimSpace(assignment.Reason), strings.TrimSpace(orchestrated.Reason)),
			})
		}
		if len(placements) > 0 {
			return placements
		}
	}

	for _, node := range workload.Nodes {
		nodeID := aiInfraFirstNonEmpty(strings.TrimSpace(node.NodeName), strings.TrimSpace(node.Hostname), strings.TrimSpace(node.CollectorID))
		score := node.OverallScore + clamp01(queueDelay/180.0)*2.0
		if collector := strings.ToLower(strings.TrimSpace(node.CollectorID)); collector != "" {
			if _, ok := affectedCollectors[collector]; ok {
				score += 1.2
			}
		}
		if host := strings.ToLower(strings.TrimSpace(node.Hostname)); host != "" {
			if _, ok := affectedHosts[host]; ok {
				score += 1.2
			}
		}
		placements = append(placements, aiInfraIncidentPlacementHop{
			WorkloadID:        aiInfraWorkloadIdentity(workload),
			NodeID:            nodeID,
			CollectorID:       strings.TrimSpace(node.CollectorID),
			Hostname:          strings.TrimSpace(node.Hostname),
			Cluster:           workload.Cluster,
			Score:             score,
			Severity:          node.Severity,
			QueueDelaySeconds: queueDelay,
			Signals:           node.Signals,
			Reason:            aiInfraFirstNonEmpty(aiInfraJoinFirstN(node.Reasons, 2, " | "), aiInfraJoinFirstN(workload.Reasons, 2, " | ")),
		})
	}
	return placements
}

func aiInfraSortIncidentPlacementHops(rows []aiInfraIncidentPlacementHop) {
	sort.Slice(rows, func(i, j int) bool {
		leftRank := aiInfraSeverityRank(rows[i].Severity)
		rightRank := aiInfraSeverityRank(rows[j].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		if rows[i].QueueDelaySeconds != rows[j].QueueDelaySeconds {
			return rows[i].QueueDelaySeconds > rows[j].QueueDelaySeconds
		}
		if rows[i].WorkloadID != rows[j].WorkloadID {
			return rows[i].WorkloadID < rows[j].WorkloadID
		}
		return rows[i].NodeID < rows[j].NodeID
	})
}

func aiInfraJoinFirstN(values []string, n int, sep string) string {
	items := dedupeStrings(values)
	if len(items) == 0 {
		return ""
	}
	if n > 0 && len(items) > n {
		items = items[:n]
	}
	return strings.Join(items, sep)
}

func aiInfraFirstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func aiInfraTopProgramsByCategory(programs []ProgramStats, category string) []ProgramStats {
	if len(programs) == 0 {
		return nil
	}
	category = strings.ToLower(strings.TrimSpace(category))
	out := make([]ProgramStats, 0, len(programs))
	for _, program := range programs {
		if aiInfraProgramMatchesCategory(program, category) {
			out = append(out, program)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].Score
		right := out[j].Score
		if category == "cpu" {
			left = out[i].CPUPercent
			right = out[j].CPUPercent
		}
		if category == "gpu" {
			left = out[i].GPUUtilSMPct + out[i].GPUMemMiB/1024.0
			right = out[j].GPUUtilSMPct + out[j].GPUMemMiB/1024.0
		}
		if left != right {
			return left > right
		}
		return strings.TrimSpace(out[i].Name) < strings.TrimSpace(out[j].Name)
	})
	return out
}

func aiInfraProgramMatchesCategory(program ProgramStats, category string) bool {
	for _, c := range program.Categories {
		if strings.EqualFold(strings.TrimSpace(c), category) {
			return true
		}
	}
	switch category {
	case "gpu":
		return program.GPUUtilSMPct > 0 || program.GPUMemMiB > 0 || program.GPUUtilMemPct > 0
	case "cpu":
		return program.CPUPercent > 0
	case "network":
		return program.NetBytesPerSecond > 0 || program.NetConnections > 0 || program.NetQueuedBytes > 0
	case "disk", "disk_io":
		return program.DiskReadBps > 0 || program.DiskWriteBps > 0
	case "memory":
		return program.MemoryBytes > 0
	default:
		return false
	}
}

func aiInfraIsCollectivePattern(pattern string) bool {
	value := strings.ToLower(strings.TrimSpace(pattern))
	if value == "" {
		return false
	}
	for _, token := range []string{"nccl", "ucx", "gloo", "mpi", "rdma"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func aiInfraWorkloadIdentity(workload workloadPathDiagnosticsWorkload) string {
	return strings.TrimSpace(strings.Join([]string{
		workload.Cluster,
		workload.Namespace,
		workload.Kind,
		workload.Name,
	}, "/"))
}

func aiInfraIsInferenceWorkload(workload workloadPathDiagnosticsWorkload) bool {
	text := strings.ToLower(strings.Join([]string{
		workload.Namespace,
		workload.Name,
		workload.Service,
		workload.Kind,
	}, " "))
	if strings.Contains(text, "inference") || strings.Contains(text, "serving") || strings.Contains(text, "gateway") || strings.Contains(text, "llm") {
		return true
	}
	if strings.Contains(text, "api") && workload.GPURequests > 0 {
		return true
	}
	return false
}

func aiInfraClassifyMeasurementMethods(measurements []aiInfraLayerMeasurement) []aiInfraLayerMeasurement {
	if len(measurements) == 0 {
		return measurements
	}
	for i := range measurements {
		if strings.TrimSpace(measurements[i].Method) != "" {
			continue
		}
		measurements[i].Method = aiInfraInferMeasurementMethod(measurements[i])
	}
	return measurements
}

func aiInfraInferMeasurementMethod(measurement aiInfraLayerMeasurement) string {
	if measurement.Status == aiInfraMeasurementMissing {
		return aiInfraMethodMissing
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		measurement.Metric,
		measurement.Source,
		measurement.Note,
	}, " ")))
	if text == "" {
		return aiInfraMethodDirect
	}
	if strings.Contains(text, "not integrated") ||
		strings.Contains(text, "if integrated") ||
		strings.Contains(text, "proxy") ||
		strings.Contains(text, "partition proxy") ||
		strings.Contains(text, "heuristic") ||
		strings.Contains(text, "suggested") ||
		strings.Contains(text, "estimated") {
		return aiInfraMethodProxy
	}
	if strings.Contains(text, "derived") ||
		strings.Contains(text, "computed") ||
		strings.Contains(text, "jain_fairness_index") ||
		strings.Contains(text, " / ") {
		return aiInfraMethodDerived
	}
	return aiInfraMethodDirect
}

func aiInfraCoveragePercent(measurements []aiInfraLayerMeasurement) float64 {
	if len(measurements) == 0 {
		return 0
	}
	total := 0.0
	for _, measurement := range measurements {
		switch measurement.Status {
		case aiInfraMeasurementMeasured:
			total += 1.0
		case aiInfraMeasurementPartial:
			total += 0.5
		}
	}
	return clampRange((total/float64(len(measurements)))*100.0, 0, 100)
}

func aiInfraJainFairnessIndex(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	sumSquares := 0.0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		sum += value
		sumSquares += value * value
	}
	if sum <= 0 || sumSquares <= 0 {
		return 0
	}
	n := float64(len(values))
	return clampRange((sum*sum)/(n*sumSquares), 0, 1)
}

func aiInfraTopShare(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	top := 0.0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		total += value
		if value > top {
			top = value
		}
	}
	if total <= 0 {
		return 0
	}
	return clampRange(top/total, 0, 1)
}

func metricExists(metrics map[string]float64, keys ...string) bool {
	if len(metrics) == 0 || len(keys) == 0 {
		return false
	}
	for _, key := range keys {
		if _, ok := metrics[key]; ok {
			return true
		}
	}
	return false
}

func aiInfraStatusFromCoverage(observed, expected int) string {
	if expected <= 0 {
		if observed > 0 {
			return aiInfraMeasurementMeasured
		}
		return aiInfraMeasurementMissing
	}
	if observed <= 0 {
		return aiInfraMeasurementMissing
	}
	if observed < expected {
		return aiInfraMeasurementPartial
	}
	return aiInfraMeasurementMeasured
}

func aiInfraStatusFromBool(ok bool) string {
	if ok {
		return aiInfraMeasurementMeasured
	}
	return aiInfraMeasurementMissing
}

func aiInfraSortRankedEntities(rows []aiInfraRankedEntity) {
	sort.Slice(rows, func(i, j int) bool {
		leftRank := aiInfraSeverityRank(rows[i].Severity)
		rightRank := aiInfraSeverityRank(rows[j].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Label < rows[j].Label
	})
}

func aiInfraMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func aiInfraMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
