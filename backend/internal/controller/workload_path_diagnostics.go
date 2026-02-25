package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/k8sview"
)

const (
	defaultWorkloadPathLimit = 30
	maxWorkloadPathLimit     = 200
)

type workloadPathDiagnosticsResponse struct {
	Cluster     string                            `json:"cluster,omitempty"`
	Namespace   string                            `json:"namespace,omitempty"`
	Service     string                            `json:"service,omitempty"`
	GeneratedAt time.Time                         `json:"generated_at"`
	Summary     workloadPathDiagnosticsSummary    `json:"summary"`
	Workloads   []workloadPathDiagnosticsWorkload `json:"workloads"`
}

type workloadPathDiagnosticsSummary struct {
	WorkloadCount                   int    `json:"workload_count"`
	CriticalWorkloads               int    `json:"critical_workloads"`
	DegradedWorkloads               int    `json:"degraded_workloads"`
	TelemetryCoveredWorkloads       int    `json:"telemetry_covered_workloads"`
	MultiNodeWorkloads              int    `json:"multi_node_workloads"`
	GPUStarvationRiskWorkloads      int    `json:"gpu_starvation_risk_workloads"`
	CommunicationImbalanceWorkloads int    `json:"communication_imbalance_workloads"`
	TopBottleneck                   string `json:"top_bottleneck,omitempty"`
}

type workloadPathDiagnosticsWorkload struct {
	Cluster              string             `json:"cluster"`
	Namespace            string             `json:"namespace"`
	Kind                 string             `json:"kind"`
	Name                 string             `json:"name"`
	Service              string             `json:"service"`
	PodsTotal            int                `json:"pods_total"`
	PodsRunning          int                `json:"pods_running"`
	PodsPending          int                `json:"pods_pending"`
	PodsFailed           int                `json:"pods_failed"`
	ContainerRestarts    int64              `json:"container_restarts"`
	GPURequests          float64            `json:"gpu_requests,omitempty"`
	GPULimits            float64            `json:"gpu_limits,omitempty"`
	NodeCount            int                `json:"node_count"`
	ResolvedNodes        int                `json:"resolved_nodes"`
	TelemetryCoveragePct float64            `json:"telemetry_coverage_percent"`
	ComputeScore         float64            `json:"compute_score"`
	NetworkScore         float64            `json:"network_score"`
	StorageScore         float64            `json:"storage_score"`
	OverallScore         float64            `json:"overall_score"`
	Severity             string             `json:"severity"`
	Bottleneck           string             `json:"bottleneck"`
	TopStorageStage      string             `json:"top_storage_stage,omitempty"`
	TopNetworkStage      string             `json:"top_network_stage,omitempty"`
	Signals              map[string]float64 `json:"signals,omitempty"`
	Sources              map[string]string  `json:"sources,omitempty"`
	Risks                []string           `json:"risks,omitempty"`
	Reasons              []string           `json:"reasons,omitempty"`
	Nodes                []workloadPathNode `json:"nodes,omitempty"`
}

type workloadPathNode struct {
	NodeName           string             `json:"node_name"`
	CollectorID        string             `json:"collector_id,omitempty"`
	Hostname           string             `json:"hostname,omitempty"`
	TelemetryAvailable bool               `json:"telemetry_available"`
	ComputeScore       float64            `json:"compute_score"`
	NetworkScore       float64            `json:"network_score"`
	StorageScore       float64            `json:"storage_score"`
	OverallScore       float64            `json:"overall_score"`
	Severity           string             `json:"severity"`
	Bottleneck         string             `json:"bottleneck,omitempty"`
	TopStorageStage    string             `json:"top_storage_stage,omitempty"`
	TopNetworkStage    string             `json:"top_network_stage,omitempty"`
	Signals            map[string]float64 `json:"signals,omitempty"`
	Sources            map[string]string  `json:"sources,omitempty"`
	Reasons            []string           `json:"reasons,omitempty"`
}

func (c *Controller) handleWorkloadPathDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.k8sManager == nil {
		http.Error(w, "kubernetes integration disabled", http.StatusServiceUnavailable)
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	limit := parseWorkloadPathLimit(r.URL.Query().Get("limit"))

	var ingestSnapshots []*ingest.NodeSnapshot
	if c.ingestStore != nil {
		ingestSnapshots = c.ingestStore.Snapshot()
	}

	resp := buildWorkloadPathDiagnostics(cluster, namespace, service, limit, c.k8sManager.Snapshots(), ingestSnapshots)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func buildWorkloadPathDiagnostics(
	clusterFilter, namespaceFilter, serviceFilter string,
	limit int,
	clusterSnapshots []k8sview.ClusterSnapshot,
	ingestSnapshots []*ingest.NodeSnapshot,
) workloadPathDiagnosticsResponse {
	resp := workloadPathDiagnosticsResponse{
		Cluster:     clusterFilter,
		Namespace:   namespaceFilter,
		Service:     serviceFilter,
		GeneratedAt: time.Now(),
		Summary:     workloadPathDiagnosticsSummary{},
		Workloads:   []workloadPathDiagnosticsWorkload{},
	}

	if limit <= 0 {
		limit = defaultWorkloadPathLimit
	}
	if limit > maxWorkloadPathLimit {
		limit = maxWorkloadPathLimit
	}

	ingestByCollector, ingestByKey := indexIngestNodes(ingestSnapshots)
	workloads := make([]workloadPathDiagnosticsWorkload, 0, 256)

	for _, snapshot := range clusterSnapshots {
		if clusterFilter != "" && !strings.EqualFold(strings.TrimSpace(snapshot.Name), clusterFilter) {
			continue
		}
		nodeCollector := make(map[string]string, len(snapshot.Nodes))
		nodeHost := make(map[string]string, len(snapshot.Nodes))
		for _, node := range snapshot.Nodes {
			nodeKey := normalizedNodeKey(node.Name)
			if nodeKey == "" {
				continue
			}
			if collectorID := strings.TrimSpace(node.Observed.CollectorID); collectorID != "" {
				nodeCollector[nodeKey] = collectorID
			}
			if host := strings.TrimSpace(node.Observed.Hostname); host != "" {
				nodeHost[nodeKey] = host
			}
		}

		for _, workload := range snapshot.Workloads {
			if namespaceFilter != "" && !strings.EqualFold(strings.TrimSpace(workload.Namespace), namespaceFilter) {
				continue
			}
			if serviceFilter != "" && !strings.EqualFold(strings.TrimSpace(workload.Service), serviceFilter) {
				continue
			}
			row := evaluateWorkloadPath(workload, nodeCollector, nodeHost, ingestByCollector, ingestByKey)
			workloads = append(workloads, row)
		}
	}

	sort.Slice(workloads, func(i, j int) bool {
		leftRank := workloadSeverityRank(workloads[i].Severity)
		rightRank := workloadSeverityRank(workloads[j].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if workloads[i].OverallScore != workloads[j].OverallScore {
			return workloads[i].OverallScore > workloads[j].OverallScore
		}
		if workloads[i].Cluster != workloads[j].Cluster {
			return workloads[i].Cluster < workloads[j].Cluster
		}
		if workloads[i].Namespace != workloads[j].Namespace {
			return workloads[i].Namespace < workloads[j].Namespace
		}
		if workloads[i].Service != workloads[j].Service {
			return workloads[i].Service < workloads[j].Service
		}
		return workloads[i].Name < workloads[j].Name
	})
	if len(workloads) > limit {
		workloads = workloads[:limit]
	}

	resp.Workloads = workloads
	resp.Summary.WorkloadCount = len(workloads)

	bottleneckCounts := make(map[string]int, 8)
	for _, workload := range workloads {
		switch workload.Severity {
		case "critical":
			resp.Summary.CriticalWorkloads++
		case "degraded":
			resp.Summary.DegradedWorkloads++
		}
		if workload.ResolvedNodes > 0 {
			resp.Summary.TelemetryCoveredWorkloads++
		}
		if workload.NodeCount > 1 {
			resp.Summary.MultiNodeWorkloads++
		}
		if containsString(workload.Risks, "gpu_starvation_due_to_io_or_network") {
			resp.Summary.GPUStarvationRiskWorkloads++
		}
		if containsString(workload.Risks, "communication_imbalance") {
			resp.Summary.CommunicationImbalanceWorkloads++
		}
		if workload.Bottleneck != "" {
			bottleneckCounts[workload.Bottleneck]++
		}
	}
	resp.Summary.TopBottleneck = topBottleneck(bottleneckCounts)
	return resp
}

func evaluateWorkloadPath(
	workload k8sview.WorkloadSummary,
	nodeCollector map[string]string,
	nodeHost map[string]string,
	ingestByCollector map[string]*ingest.NodeSnapshot,
	ingestByKey map[string]*ingest.NodeSnapshot,
) workloadPathDiagnosticsWorkload {
	row := workloadPathDiagnosticsWorkload{
		Cluster:           workload.Cluster,
		Namespace:         workload.Namespace,
		Kind:              workload.Kind,
		Name:              workload.Name,
		Service:           workload.Service,
		PodsTotal:         workload.PodsTotal,
		PodsRunning:       workload.PodsRunning,
		PodsPending:       workload.PodsPending,
		PodsFailed:        workload.PodsFailed,
		ContainerRestarts: workload.ContainerRestarts,
		GPURequests:       workload.GPURequests,
		GPULimits:         workload.GPULimits,
		NodeCount:         len(workload.Nodes),
		Nodes:             make([]workloadPathNode, 0, len(workload.Nodes)),
		Risks:             []string{},
		Reasons:           []string{},
	}

	computeTotal := 0.0
	networkTotal := 0.0
	storageTotal := 0.0
	resolved := 0

	workloadSignalsTotal := make(map[string]float64, 16)
	workloadSignalsCount := make(map[string]int, 16)

	storageStageTotals := map[string]float64{}
	storageStageCounts := map[string]int{}
	networkStageTotals := map[string]float64{}
	networkStageCounts := map[string]int{}

	avgGPUUtilSum := 0.0
	avgGPUUtilCount := 0
	avgIOWaitSum := 0.0
	avgBlockedSum := 0.0
	rdmaMinNonZero := 0.0
	rdmaMax := 0.0

	for _, nodeName := range workload.Nodes {
		nodeRow := workloadPathNode{
			NodeName: strings.TrimSpace(nodeName),
			Severity: "unknown",
		}

		lookupKey := normalizedNodeKey(nodeName)
		collectorID := strings.TrimSpace(nodeCollector[lookupKey])
		if collectorID != "" {
			nodeRow.CollectorID = collectorID
		}

		snapshot := (*ingest.NodeSnapshot)(nil)
		if collectorID != "" {
			snapshot = ingestByCollector[collectorID]
		}
		if snapshot == nil {
			snapshot = ingestByKey[lookupKey]
		}
		if snapshot == nil {
			if host := normalizedNodeKey(nodeHost[lookupKey]); host != "" {
				snapshot = ingestByKey[host]
			}
		}

		if snapshot == nil || snapshot.Metrics == nil {
			nodeRow.Reasons = []string{"No matching ingest telemetry for Kubernetes node mapping."}
			row.Nodes = append(row.Nodes, nodeRow)
			continue
		}

		nodeRow.TelemetryAvailable = true
		nodeRow.CollectorID = snapshot.CollectorID
		nodeRow.Hostname = strings.TrimSpace(snapshot.Hostname)

		computeScore := computePressureScore(snapshot.Metrics)
		networkScore, networkSignals, networkFactors := networkPressureScore(snapshot.Metrics)
		storageScore, storageSignals, storageFactors := storagePressureScore(snapshot.Metrics)
		kernelStorage := evaluateStorageKernelPath(snapshot.Metrics)
		kernelNetwork := evaluateNetworkKernelPath(snapshot.Metrics)

		nodeRow.ComputeScore = computeScore
		nodeRow.NetworkScore = networkScore
		nodeRow.StorageScore = storageScore
		nodeRow.OverallScore = maxOf(computeScore, networkScore, storageScore)
		nodeRow.Severity = pressureSeverity(nodeRow.OverallScore)
		nodeRow.Bottleneck = dominantWorkloadBottleneck(computeScore, networkScore, storageScore)
		nodeRow.TopStorageStage = kernelStorage.TopStage
		nodeRow.TopNetworkStage = kernelNetwork.TopStage
		nodeRow.Reasons = dedupeStrings(append(
			[]string{},
			summaryFactorsForNode(nodeRow.Bottleneck, networkFactors, storageFactors)...,
		))

		nodeRow.Signals = compactPositiveSignals(map[string]float64{
			"cpu_usage_percent":               metricValueOr(snapshot.Metrics, "node_cpu_usage_percent"),
			"gpu_utilization_percent":         metricValueOr(snapshot.Metrics, "node_gpu_utilization_sm_avg_percent"),
			"cpu_iowait_percent":              metricValueOr(snapshot.Metrics, "node_cpu_iowait_percent"),
			"procs_blocked":                   metricValueOr(snapshot.Metrics, "node_procs_blocked"),
			"tcp_retransmit_ratio":            networkSignals["tcp_retransmit_ratio"],
			"softnet_dropped_per_second":      networkSignals["softnet_dropped_per_second"],
			"rdma_congestion_per_second":      networkSignals["rdma_congestion_per_second"],
			"disk_latency_p99_ms":             storageSignals["disk_latency_p99_ms"],
			"io_pressure_full_avg10":          storageSignals["io_pressure_full_avg10"],
			"dataloader_prefetch_stall_ratio": storageSignals["dataloader_prefetch_stall_ratio"],
			"checkpoint_write_latency_p99_ms": storageSignals["checkpoint_write_latency_p99_ms"],
		})
		nodeRow.Sources = workloadSignalSources(nodeRow.Signals)

		computeTotal += computeScore
		networkTotal += networkScore
		storageTotal += storageScore
		resolved++

		if gpuUtil := metricValueOr(snapshot.Metrics, "node_gpu_utilization_sm_avg_percent"); gpuUtil > 0 {
			avgGPUUtilSum += gpuUtil
			avgGPUUtilCount++
		}
		iowait := metricValueOr(snapshot.Metrics, "node_cpu_iowait_percent")
		blocked := metricValueOr(snapshot.Metrics, "node_procs_blocked")
		avgIOWaitSum += iowait
		avgBlockedSum += blocked

		rdmaTotal := metricValueOr(snapshot.Metrics, "node_rdma_port_transmit_bytes_per_second") +
			metricValueOr(snapshot.Metrics, "node_rdma_port_receive_bytes_per_second")
		if rdmaTotal > 0 {
			if rdmaMinNonZero == 0 || rdmaTotal < rdmaMinNonZero {
				rdmaMinNonZero = rdmaTotal
			}
			if rdmaTotal > rdmaMax {
				rdmaMax = rdmaTotal
			}
		}

		for _, stage := range kernelStorage.Stages {
			storageStageTotals[stage.Name] += stage.Score
			storageStageCounts[stage.Name]++
		}
		for _, stage := range kernelNetwork.Stages {
			networkStageTotals[stage.Name] += stage.Score
			networkStageCounts[stage.Name]++
		}
		accumulateWorkloadSignals(workloadSignalsTotal, workloadSignalsCount, nodeRow.Signals)

		row.Nodes = append(row.Nodes, nodeRow)
	}

	row.ResolvedNodes = resolved
	if row.NodeCount > 0 {
		row.TelemetryCoveragePct = clampRange((float64(resolved)/float64(row.NodeCount))*100.0, 0, 100)
	}

	if resolved > 0 {
		row.ComputeScore = computeTotal / float64(resolved)
		row.NetworkScore = networkTotal / float64(resolved)
		row.StorageScore = storageTotal / float64(resolved)
		row.OverallScore = maxOf(row.ComputeScore, row.NetworkScore, row.StorageScore)
		row.Severity = pressureSeverity(row.OverallScore)
	} else {
		// Fallback path when ingest snapshots are missing for the workload nodes.
		row.ComputeScore = clamp01(workload.AvgNodeCPUPercent/100.0)*2.0 +
			clamp01(workload.AvgNodeMemoryPct/100.0)*1.5 +
			clamp01(workload.AvgNodeGPUPercent/100.0)*1.5
		row.NetworkScore = 0
		row.StorageScore = 0
		row.OverallScore = row.ComputeScore
		row.Severity = "unknown"
	}
	row.Bottleneck = dominantWorkloadBottleneck(row.ComputeScore, row.NetworkScore, row.StorageScore)
	row.TopStorageStage = topKernelStageByAverage(storageStageTotals, storageStageCounts)
	row.TopNetworkStage = topKernelStageByAverage(networkStageTotals, networkStageCounts)
	row.Signals = averageWorkloadSignals(workloadSignalsTotal, workloadSignalsCount)
	if row.Signals == nil {
		row.Signals = compactPositiveSignals(map[string]float64{
			"pods_pending": float64(workload.PodsPending),
			"pods_failed":  float64(workload.PodsFailed),
		})
	}
	row.Sources = workloadSignalSources(row.Signals)

	avgGPUUtil := workload.AvgNodeGPUPercent
	if avgGPUUtilCount > 0 {
		avgGPUUtil = avgGPUUtilSum / float64(avgGPUUtilCount)
	}
	avgIOWait := 0.0
	avgBlocked := 0.0
	if resolved > 0 {
		avgIOWait = avgIOWaitSum / float64(resolved)
		avgBlocked = avgBlockedSum / float64(resolved)
	}

	if workload.GPURequests > 0 && avgGPUUtil > 0 && avgGPUUtil < 60 && (row.StorageScore >= 3 || row.NetworkScore >= 3) {
		row.Risks = append(row.Risks, "gpu_starvation_due_to_io_or_network")
	}
	if rdmaMinNonZero > 0 && rdmaMax > 0 && row.NodeCount > 1 {
		rdmaSkew := rdmaMax / rdmaMinNonZero
		if rdmaSkew >= 1.8 {
			row.Risks = append(row.Risks, "communication_imbalance")
		}
	}
	if row.StorageScore >= 3 && avgIOWait >= 10 {
		row.Risks = append(row.Risks, "storage_collapse_iowait")
	}
	if avgIOWait >= 12 && avgBlocked >= 3 {
		row.Risks = append(row.Risks, "scheduler_contention")
	}
	if workload.PodsPending > 0 || workload.PodsFailed > 0 {
		row.Risks = append(row.Risks, "k8s_scheduling_pressure")
	}
	if row.NodeCount > 1 && (row.NetworkScore >= 3 || containsString(row.Risks, "communication_imbalance")) {
		row.Risks = append(row.Risks, "cross_node_spread")
	}

	row.Reasons = append(row.Reasons, workloadStateReasons(workload)...)
	if row.TopNetworkStage != "" && row.NetworkScore >= 3 {
		row.Reasons = append(row.Reasons, "Network path pressure is elevated at kernel stage "+row.TopNetworkStage+".")
	}
	if row.TopStorageStage != "" && row.StorageScore >= 3 {
		row.Reasons = append(row.Reasons, "Storage path pressure is elevated at kernel stage "+row.TopStorageStage+".")
	}
	if containsString(row.Risks, "gpu_starvation_due_to_io_or_network") {
		row.Reasons = append(row.Reasons, "GPU demand is high while data/communication path pressure limits feed rate.")
	}
	if containsString(row.Risks, "communication_imbalance") {
		row.Reasons = append(row.Reasons, "Per-node RDMA throughput is imbalanced for this multi-node workload.")
	}
	row.Risks = dedupeStrings(row.Risks)
	row.Reasons = dedupeStrings(row.Reasons)

	sort.Slice(row.Nodes, func(i, j int) bool {
		leftRank := workloadSeverityRank(row.Nodes[i].Severity)
		rightRank := workloadSeverityRank(row.Nodes[j].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if row.Nodes[i].OverallScore != row.Nodes[j].OverallScore {
			return row.Nodes[i].OverallScore > row.Nodes[j].OverallScore
		}
		return row.Nodes[i].NodeName < row.Nodes[j].NodeName
	})

	return row
}

func parseWorkloadPathLimit(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultWorkloadPathLimit
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return defaultWorkloadPathLimit
	}
	if n > maxWorkloadPathLimit {
		return maxWorkloadPathLimit
	}
	return n
}

func workloadSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 4
	case "degraded":
		return 3
	case "healthy":
		return 2
	case "unknown":
		return 1
	default:
		return 0
	}
}

func dominantWorkloadBottleneck(computeScore, networkScore, storageScore float64) string {
	if networkScore >= computeScore && networkScore >= storageScore {
		return "network"
	}
	if storageScore >= computeScore && storageScore >= networkScore {
		return "storage"
	}
	return "compute"
}

func indexIngestNodes(snapshots []*ingest.NodeSnapshot) (map[string]*ingest.NodeSnapshot, map[string]*ingest.NodeSnapshot) {
	byCollector := make(map[string]*ingest.NodeSnapshot, len(snapshots))
	byKey := make(map[string]*ingest.NodeSnapshot, len(snapshots)*4)
	for _, snapshot := range snapshots {
		if snapshot == nil {
			continue
		}
		if collectorID := strings.TrimSpace(snapshot.CollectorID); collectorID != "" {
			byCollector[collectorID] = snapshot
		}
		for _, key := range workloadNodeLookupKeys(snapshot) {
			prev, exists := byKey[key]
			if exists && prev != nil && prev.UpdatedAt.After(snapshot.UpdatedAt) {
				continue
			}
			byKey[key] = snapshot
		}
	}
	return byCollector, byKey
}

func workloadNodeLookupKeys(snapshot *ingest.NodeSnapshot) []string {
	if snapshot == nil {
		return nil
	}
	keys := []string{
		strings.TrimSpace(snapshot.Hostname),
		strings.TrimSpace(snapshot.CollectorID),
		strings.TrimSpace(snapshot.Labels["node"]),
		strings.TrimSpace(snapshot.Labels["k8s_node"]),
		strings.TrimSpace(snapshot.Labels["kubernetes_node"]),
		strings.TrimSpace(snapshot.Labels["kubernetes.io/hostname"]),
	}
	set := make(map[string]struct{}, len(keys)*2)
	out := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		norm := normalizedNodeKey(key)
		if norm == "" {
			continue
		}
		if _, exists := set[norm]; exists {
			continue
		}
		set[norm] = struct{}{}
		out = append(out, norm)

		if idx := strings.Index(norm, "."); idx > 0 {
			short := norm[:idx]
			if short != "" {
				if _, exists := set[short]; !exists {
					set[short] = struct{}{}
					out = append(out, short)
				}
			}
		}
	}
	return out
}

func normalizedNodeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func summaryFactorsForNode(bottleneck string, networkFactors, storageFactors []string) []string {
	switch bottleneck {
	case "network":
		return trimFirstNNonEmpty(networkFactors, 3)
	case "storage":
		return trimFirstNNonEmpty(storageFactors, 3)
	default:
		out := trimFirstNNonEmpty(networkFactors, 2)
		out = append(out, trimFirstNNonEmpty(storageFactors, 2)...)
		return trimFirstNNonEmpty(out, 3)
	}
}

func trimFirstNNonEmpty(values []string, n int) []string {
	if n <= 0 || len(values) == 0 {
		return nil
	}
	out := make([]string, 0, minIntLocal(len(values), n))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		out = append(out, v)
		if len(out) >= n {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func accumulateWorkloadSignals(totals map[string]float64, counts map[string]int, signals map[string]float64) {
	if len(signals) == 0 {
		return
	}
	for key, value := range signals {
		totals[key] += value
		counts[key]++
	}
}

func averageWorkloadSignals(totals map[string]float64, counts map[string]int) map[string]float64 {
	if len(totals) == 0 {
		return nil
	}
	out := make(map[string]float64, len(totals))
	for key, total := range totals {
		count := counts[key]
		if count <= 0 {
			continue
		}
		out[key] = total / float64(count)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func workloadSignalSources(signals map[string]float64) map[string]string {
	if len(signals) == 0 {
		return nil
	}
	signalToMetric := map[string]string{
		"cpu_usage_percent":               "node_cpu_usage_percent",
		"gpu_utilization_percent":         "node_gpu_utilization_sm_avg_percent",
		"cpu_iowait_percent":              "node_cpu_iowait_percent",
		"procs_blocked":                   "node_procs_blocked",
		"tcp_retransmit_ratio":            "node_tcp_retransmit_ratio",
		"softnet_dropped_per_second":      "node_softnet_dropped_per_second",
		"rdma_congestion_per_second":      "node_rdma_congestion_events_per_second",
		"disk_latency_p99_ms":             "node_disk_request_latency_p99_seconds",
		"io_pressure_full_avg10":          "node_pressure_io_full_avg10",
		"dataloader_prefetch_stall_ratio": "node_dataloader_prefetch_stall_ratio",
		"checkpoint_write_latency_p99_ms": "node_checkpoint_write_latency_p99_seconds",
		"pods_pending":                    "k8s pod status counters",
		"pods_failed":                     "k8s pod status counters",
	}

	out := make(map[string]string, len(signals))
	for signal := range signals {
		metric := signalToMetric[signal]
		if metric == "" {
			continue
		}
		if metric == "k8s pod status counters" {
			out[signal] = "k8s api /api/v1/pods (status.phase)"
			continue
		}
		out[signal] = diagnosticMetricSource(metric)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func workloadStateReasons(workload k8sview.WorkloadSummary) []string {
	reasons := make([]string, 0, 3)
	if workload.PodsPending > 0 {
		reasons = append(reasons, "Pods are pending; check scheduling constraints and node capacity.")
	}
	if workload.PodsFailed > 0 {
		reasons = append(reasons, "Pods are failing; inspect crash loops and storage/network dependencies.")
	}
	if workload.ContainerRestarts > 0 {
		reasons = append(reasons, "Container restarts detected; correlate with node pressure and recent rollouts.")
	}
	return reasons
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, exists := seen[v]; exists {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
