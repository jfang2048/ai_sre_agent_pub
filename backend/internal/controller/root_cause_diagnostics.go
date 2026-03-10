package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

const (
	rootCauseMaxAffectedNodes = 6
	rootCauseMaxEvidence      = 12
	rootCauseAnomalyEvidence  = 4
)

type rootCauseDiagnosticsResponse struct {
	CollectorID string                `json:"collector_id,omitempty"`
	GeneratedAt time.Time             `json:"generated_at"`
	Summary     rootCauseSummary      `json:"summary"`
	Findings    []rootCauseFinding    `json:"findings"`
	DataPath    dataPathRCAJoinStatus `json:"data_path"`
}

type rootCauseSummary struct {
	NodeCount         int    `json:"node_count"`
	FindingCount      int    `json:"finding_count"`
	CriticalFindings  int    `json:"critical_findings"`
	DegradedFindings  int    `json:"degraded_findings"`
	TopFindingID      string `json:"top_finding_id,omitempty"`
	TopFindingSummary string `json:"top_finding_summary,omitempty"`
}

type dataPathRCAJoinStatus struct {
	NetworkCritical   int `json:"network_critical"`
	StorageCritical   int `json:"storage_critical"`
	ProbeCoreCritical int `json:"probe_core_critical"`
	TotalAnomalies    int `json:"total_anomalies"`
}

type rootCauseFinding struct {
	ID               string              `json:"id"`
	Category         string              `json:"category"`
	Severity         string              `json:"severity"`
	Confidence       float64             `json:"confidence"`
	Title            string              `json:"title"`
	Hypothesis       string              `json:"hypothesis"`
	Impact           string              `json:"impact"`
	AffectedNodes    []rootCauseNode     `json:"affected_nodes,omitempty"`
	CorrelatedSignal []string            `json:"correlated_signals,omitempty"`
	Evidence         []rootCauseEvidence `json:"evidence,omitempty"`
	Actions          []string            `json:"actions,omitempty"`
	Metadata         map[string]float64  `json:"metadata,omitempty"`
	Tags             map[string]string   `json:"tags,omitempty"`
}

type rootCauseNode struct {
	CollectorID string `json:"collector_id"`
	Hostname    string `json:"hostname"`
}

type rootCauseEvidence struct {
	CollectorID string  `json:"collector_id"`
	Hostname    string  `json:"hostname"`
	Signal      string  `json:"signal"`
	Value       float64 `json:"value"`
	Baseline    float64 `json:"baseline,omitempty"`
	ZScore      float64 `json:"z_score,omitempty"`
	Source      string  `json:"source,omitempty"`
	Note        string  `json:"note,omitempty"`
}

func (c *Controller) handleRootCauseDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	collectorID := strings.TrimSpace(r.URL.Query().Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(r.URL.Query().Get("collector"))
	}

	resp := c.buildRootCauseDiagnostics(collectorID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (c *Controller) buildRootCauseDiagnostics(collectorID string) rootCauseDiagnosticsResponse {
	resp := rootCauseDiagnosticsResponse{
		CollectorID: collectorID,
		GeneratedAt: time.Now(),
		Summary:     rootCauseSummary{},
		Findings:    []rootCauseFinding{},
	}
	if c.ingestStore == nil {
		return resp
	}

	snapshots := c.ingestStore.Snapshot()
	filtered := make([]*ingest.NodeSnapshot, 0, len(snapshots))
	snapshotByCollector := make(map[string]*ingest.NodeSnapshot, len(snapshots))
	for _, node := range snapshots {
		if collectorID != "" && node.CollectorID != collectorID {
			continue
		}
		filtered = append(filtered, node)
		snapshotByCollector[node.CollectorID] = node
	}
	resp.Summary.NodeCount = len(filtered)
	if len(filtered) == 0 {
		return resp
	}

	dataPath := c.buildDataPathDiagnostics(collectorID)
	resp.DataPath = dataPathRCAJoinStatus{
		NetworkCritical:   dataPath.Summary.NetworkCritical,
		StorageCritical:   dataPath.Summary.StorageCritical,
		ProbeCoreCritical: dataPath.Summary.ProbeCoreCritical,
		TotalAnomalies:    dataPath.Summary.TotalAnomalies,
	}

	findings := make([]rootCauseFinding, 0, 6)
	topPrograms := c.aggregateTopProgramsFiltered(maxTopProgramsLimit, collectorID)
	if finding, ok := inferNetworkCongestionFinding(dataPath); ok {
		findings = append(findings, finding)
	}
	if finding, ok := inferStorageGPUStarvationFinding(dataPath, snapshotByCollector); ok {
		findings = append(findings, finding)
	}
	if finding, ok := inferSchedulerContentionFinding(filtered); ok {
		findings = append(findings, finding)
	}
	if finding, ok := inferMemoryIOAmplificationFinding(filtered); ok {
		findings = append(findings, finding)
	}
	if finding, ok := inferProbeCoreRuntimeFinding(dataPath); ok {
		findings = append(findings, finding)
	}
	if finding, ok := inferCollectiveRuntimeContentionFinding(topPrograms, dataPath); ok {
		findings = append(findings, finding)
	}
	if finding, ok := inferCrossNodeCommImbalanceFinding(filtered, dataPath); ok {
		findings = append(findings, finding)
	}

	sort.Slice(findings, func(i, j int) bool {
		leftRank := severityRank(findings[i].Severity)
		rightRank := severityRank(findings[j].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if findings[i].Confidence != findings[j].Confidence {
			return findings[i].Confidence > findings[j].Confidence
		}
		return findings[i].ID < findings[j].ID
	})

	resp.Findings = findings
	resp.Summary.FindingCount = len(findings)
	for _, finding := range findings {
		switch finding.Severity {
		case "critical":
			resp.Summary.CriticalFindings++
		case "degraded":
			resp.Summary.DegradedFindings++
		}
	}
	if len(findings) > 0 {
		resp.Summary.TopFindingID = findings[0].ID
		resp.Summary.TopFindingSummary = findings[0].Hypothesis
	}

	return resp
}

func inferNetworkCongestionFinding(dataPath dataPathDiagnosticsResponse) (rootCauseFinding, bool) {
	candidates := make([]resourcePressureRow, 0, len(dataPath.Network.Rankings))
	for _, row := range dataPath.Network.Rankings {
		if row.Severity == "healthy" {
			continue
		}
		if row.Signals["tcp_retransmit_ratio"] >= 0.01 ||
			row.Signals["softnet_dropped_per_second"] >= 20 ||
			row.Signals["tx_queue_fill_percent"] >= 70 ||
			row.Signals["rdma_congestion_per_second"] > 0 ||
			row.Signals["rdma_pfc_pause_per_second"] > 0 ||
			row.Signals["rdma_ecn_marked_ratio"] >= 0.01 {
			candidates = append(candidates, row)
		}
	}
	if len(candidates) == 0 {
		return rootCauseFinding{}, false
	}

	severity := "degraded"
	rdmaCongested := false
	for _, row := range candidates {
		if row.Severity == "critical" {
			severity = "critical"
		}
		if row.Signals["rdma_congestion_per_second"] > 0 || row.Signals["rdma_pfc_pause_per_second"] > 0 || row.Signals["rdma_ecn_marked_ratio"] >= 0.01 {
			rdmaCongested = true
		}
	}

	confidence := 0.62 + 0.06*float64(minIntLocal(len(candidates), 4)) + 0.04*float64(minIntLocal(len(dataPath.Network.Anomalies), 3))
	if rdmaCongested {
		confidence += 0.05
	}
	confidence = clampRange(confidence, 0.55, 0.98)

	finding := rootCauseFinding{
		ID:         "network_congestion_training_slowdown",
		Category:   "network",
		Severity:   severity,
		Confidence: confidence,
		Title:      "Network congestion is throttling inter-node communication",
		Hypothesis: "Transport congestion and retransmit pressure are increasing collective communication latency.",
		Impact:     "Distributed training/inference can stall on AllReduce and synchronization phases.",
		CorrelatedSignal: []string{
			"tcp_retransmit_ratio",
			"softnet_dropped_per_second",
			"tx_queue_fill_percent",
			"rdma_congestion_per_second",
			"rdma_pfc_pause_per_second",
			"rdma_ecn_marked_ratio",
		},
		Actions: []string{
			"Inspect congestion domains and oversubscribed links in the current cluster topology.",
			"Validate ECN/PFC settings and NIC TX queue behavior on the hottest nodes.",
			"Correlate NCCL/collective step latency with the same pressure window in Metric Trends.",
		},
		Metadata: map[string]float64{
			"candidate_nodes": float64(len(candidates)),
			"anomalies":       float64(len(dataPath.Network.Anomalies)),
		},
		Tags: map[string]string{
			"domain":   "fabric",
			"workflow": "collectives",
		},
	}
	addRowsAsEvidence(&finding, candidates, rootCauseMaxAffectedNodes, []string{
		"tcp_retransmit_ratio",
		"softnet_dropped_per_second",
		"tx_queue_fill_percent",
		"rdma_congestion_per_second",
		"rdma_pfc_pause_per_second",
		"rdma_ecn_marked_ratio",
	})
	addAnomalyEvidence(&finding, dataPath.Network.Anomalies, rootCauseAnomalyEvidence)
	return trimRootCauseFinding(finding), true
}

func inferStorageGPUStarvationFinding(dataPath dataPathDiagnosticsResponse, snapshots map[string]*ingest.NodeSnapshot) (rootCauseFinding, bool) {
	candidates := make([]resourcePressureRow, 0, len(dataPath.Storage.Rankings))
	gpuLowNodes := 0
	for _, row := range dataPath.Storage.Rankings {
		if row.Severity == "healthy" {
			continue
		}
		latency := row.Signals["disk_latency_p99_ms"]
		stall := row.Signals["dataloader_prefetch_stall_ratio"]
		checkpoint := row.Signals["checkpoint_write_latency_p99_ms"]
		if latency < 20 && stall < 0.15 && checkpoint < 120 {
			continue
		}
		candidates = append(candidates, row)

		snapshot := snapshots[row.CollectorID]
		if snapshot == nil || snapshot.Metrics == nil {
			continue
		}
		gpuUtil := metricValueOr(snapshot.Metrics, "node_gpu_utilization_sm_avg_percent", "node_gpu_utilization_percent")
		if gpuUtil > 0 && gpuUtil < 60 {
			gpuLowNodes++
		}
	}
	if len(candidates) == 0 {
		return rootCauseFinding{}, false
	}

	severity := "degraded"
	for _, row := range candidates {
		if row.Severity == "critical" && (row.Signals["dataloader_prefetch_stall_ratio"] >= 0.2 || row.Signals["disk_latency_p99_ms"] >= 30 || row.Signals["checkpoint_write_latency_p99_ms"] >= 160) {
			severity = "critical"
			break
		}
	}

	confidence := 0.60 + 0.07*float64(minIntLocal(len(candidates), 4)) + 0.04*float64(minIntLocal(len(dataPath.Storage.Anomalies), 3))
	if gpuLowNodes > 0 {
		confidence += 0.06
	}
	confidence = clampRange(confidence, 0.50, 0.97)

	finding := rootCauseFinding{
		ID:         "storage_latency_gpu_starvation",
		Category:   "storage",
		Severity:   severity,
		Confidence: confidence,
		Title:      "Storage path latency is starving GPU work queues",
		Hypothesis: "Dataset/checkpoint I/O latency and prefetch stalls are limiting GPU feed rate.",
		Impact:     "GPU accelerators wait on input data, reducing effective training/inference throughput.",
		CorrelatedSignal: []string{
			"disk_latency_p99_ms",
			"io_pressure_full_avg10",
			"dataloader_prefetch_stall_ratio",
			"checkpoint_write_latency_p99_ms",
			"cache_hit_ratio",
		},
		Actions: []string{
			"Stage hot datasets to local NVMe and validate cache locality per node.",
			"Isolate checkpoint bursts from read-heavy data-loader traffic (QoS/bandwidth partition).",
			"Reduce small-file read amplification with sharded/tarred dataset layouts.",
		},
		Metadata: map[string]float64{
			"candidate_nodes":   float64(len(candidates)),
			"gpu_low_nodes":     float64(gpuLowNodes),
			"storage_anomalies": float64(len(dataPath.Storage.Anomalies)),
		},
		Tags: map[string]string{
			"domain":   "storage",
			"workflow": "data_pipeline",
		},
	}
	addRowsAsEvidence(&finding, candidates, rootCauseMaxAffectedNodes, []string{
		"disk_latency_p99_ms",
		"io_pressure_full_avg10",
		"dataloader_prefetch_stall_ratio",
		"checkpoint_write_latency_p99_ms",
		"cache_hit_ratio",
	})
	for _, row := range candidates {
		snapshot := snapshots[row.CollectorID]
		if snapshot == nil || snapshot.Metrics == nil {
			continue
		}
		gpuUtil := metricValueOr(snapshot.Metrics, "node_gpu_utilization_sm_avg_percent", "node_gpu_utilization_percent")
		if gpuUtil <= 0 {
			continue
		}
		appendEvidence(&finding, rootCauseEvidence{
			CollectorID: row.CollectorID,
			Hostname:    row.Hostname,
			Signal:      "node_gpu_utilization_sm_avg_percent",
			Value:       gpuUtil,
			Source:      diagnosticMetricSource("node_gpu_utilization_sm_avg_percent"),
			Note:        "GPU utilization is low while storage pressure is elevated.",
		})
	}
	addAnomalyEvidence(&finding, dataPath.Storage.Anomalies, rootCauseAnomalyEvidence)
	return trimRootCauseFinding(finding), true
}

func inferMemoryIOAmplificationFinding(snapshots []*ingest.NodeSnapshot) (rootCauseFinding, bool) {
	type nodeMemorySignal struct {
		CollectorID string
		Hostname    string
		IOFull      float64
		DirtyBytes  float64
		Writeback   float64
		PageOut     float64
		SwapOut     float64
	}
	candidates := make([]nodeMemorySignal, 0, len(snapshots))
	for _, node := range snapshots {
		if node == nil || node.Metrics == nil {
			continue
		}
		ioFull := node.Metrics["node_pressure_io_full_avg10"]
		dirty := node.Metrics["node_memory_Dirty_bytes"]
		writeback := node.Metrics["node_memory_Writeback_bytes"]
		pageOut := node.Metrics["node_vmstat_pgpgout_per_second"]
		swapOut := metricValueOr(node.Metrics, "node_vmstat_pswpout_per_second", "node_vmstat_pswpout")
		if ioFull < 5 {
			continue
		}
		if dirty < 512*1024*1024 && writeback < 128*1024*1024 && pageOut < 50000 && swapOut <= 0 {
			continue
		}
		hostname := strings.TrimSpace(node.Hostname)
		if hostname == "" {
			hostname = node.CollectorID
		}
		candidates = append(candidates, nodeMemorySignal{
			CollectorID: node.CollectorID,
			Hostname:    hostname,
			IOFull:      ioFull,
			DirtyBytes:  dirty,
			Writeback:   writeback,
			PageOut:     pageOut,
			SwapOut:     swapOut,
		})
	}
	if len(candidates) == 0 {
		return rootCauseFinding{}, false
	}

	severity := "degraded"
	for _, node := range candidates {
		if node.IOFull >= 10 && (node.DirtyBytes >= 1024*1024*1024 || node.Writeback >= 256*1024*1024 || node.SwapOut > 0) {
			severity = "critical"
			break
		}
	}

	confidence := clampRange(0.58+0.08*float64(minIntLocal(len(candidates), 4)), 0.50, 0.95)
	finding := rootCauseFinding{
		ID:         "memory_pressure_io_amplification",
		Category:   "kernel_memory_io",
		Severity:   severity,
		Confidence: confidence,
		Title:      "Kernel memory/writeback pressure is amplifying I/O latency",
		Hypothesis: "Dirty/writeback backlog and reclaim churn are forcing expensive flush and page-out cycles.",
		Impact:     "Tail I/O latency rises and end-to-end pipeline throughput collapses under memory pressure.",
		CorrelatedSignal: []string{
			"node_pressure_io_full_avg10",
			"node_memory_Dirty_bytes",
			"node_memory_Writeback_bytes",
			"node_vmstat_pgpgout_per_second",
			"node_vmstat_pswpout_per_second",
		},
		Actions: []string{
			"Inspect page-cache dirty/writeback backlog and adjust writeback/dirty throttling policy.",
			"Reduce memory overcommit and check NUMA locality for data-loader and checkpoint workers.",
			"Correlate reclaim/page-out spikes with storage latency bursts in the same window.",
		},
		Metadata: map[string]float64{
			"candidate_nodes": float64(len(candidates)),
		},
		Tags: map[string]string{
			"domain": "kernel",
			"path":   "memory_to_io",
		},
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].IOFull + (candidates[i].Writeback / (1024 * 1024 * 1024))
		right := candidates[j].IOFull + (candidates[j].Writeback / (1024 * 1024 * 1024))
		if left != right {
			return left > right
		}
		return candidates[i].CollectorID < candidates[j].CollectorID
	})

	for _, node := range candidates {
		appendNode(&finding, rootCauseNode{CollectorID: node.CollectorID, Hostname: node.Hostname})
		appendEvidence(&finding, rootCauseEvidence{
			CollectorID: node.CollectorID,
			Hostname:    node.Hostname,
			Signal:      "node_pressure_io_full_avg10",
			Value:       node.IOFull,
			Source:      diagnosticMetricSource("node_pressure_io_full_avg10"),
		})
		if node.DirtyBytes > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "node_memory_Dirty_bytes",
				Value:       node.DirtyBytes,
				Source:      diagnosticMetricSource("node_memory_Dirty_bytes"),
			})
		}
		if node.Writeback > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "node_memory_Writeback_bytes",
				Value:       node.Writeback,
				Source:      diagnosticMetricSource("node_memory_Writeback_bytes"),
			})
		}
		if node.PageOut > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "node_vmstat_pgpgout_per_second",
				Value:       node.PageOut,
				Source:      diagnosticMetricSource("node_vmstat_pgpgout_per_second"),
			})
		}
		if node.SwapOut > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "node_vmstat_pswpout_per_second",
				Value:       node.SwapOut,
				Source:      diagnosticMetricSource("node_vmstat_pswpout_per_second"),
			})
		}
	}

	return trimRootCauseFinding(finding), true
}

func inferSchedulerContentionFinding(snapshots []*ingest.NodeSnapshot) (rootCauseFinding, bool) {
	type nodeSchedSignal struct {
		CollectorID  string
		Hostname     string
		Load1        float64
		Running      float64
		Blocked      float64
		CPUUsage     float64
		IOWait       float64
		CPUPressure  float64
		IOPressure   float64
		CrossSignals float64
	}

	candidates := make([]nodeSchedSignal, 0, len(snapshots))
	crossSignalNodes := 0
	for _, node := range snapshots {
		if node == nil || node.Metrics == nil {
			continue
		}
		load1 := metricValueOr(node.Metrics, "node_load1")
		running := metricValueOr(node.Metrics, "node_procs_running")
		blocked := metricValueOr(node.Metrics, "node_procs_blocked")
		cpuUsage := metricValueOr(node.Metrics, "node_cpu_usage_percent")
		iowait := metricValueOr(node.Metrics, "node_cpu_iowait_percent")
		cpuPressure := metricValueOr(node.Metrics, "node_pressure_cpu_some_avg10")
		ioPressure := metricValueOr(node.Metrics, "node_pressure_io_some_avg10", "node_pressure_io_full_avg10")

		// Require scheduler pressure beyond normal background noise.
		if load1 < 8 && running < 8 && blocked < 2 && cpuPressure < 8 && iowait < 7 {
			continue
		}
		if blocked < 2 && cpuPressure < 10 && running < 12 && (iowait < 8 || ioPressure < 5) {
			continue
		}

		hostname := strings.TrimSpace(node.Hostname)
		if hostname == "" {
			hostname = node.CollectorID
		}
		crossSignals := 0.0
		if cpuPressure >= 12 {
			crossSignals++
		}
		if blocked >= 2 {
			crossSignals++
		}
		if iowait >= 8 || ioPressure >= 8 {
			crossSignals++
		}
		if crossSignals >= 2 {
			crossSignalNodes++
		}
		candidates = append(candidates, nodeSchedSignal{
			CollectorID:  node.CollectorID,
			Hostname:     hostname,
			Load1:        load1,
			Running:      running,
			Blocked:      blocked,
			CPUUsage:     cpuUsage,
			IOWait:       iowait,
			CPUPressure:  cpuPressure,
			IOPressure:   ioPressure,
			CrossSignals: crossSignals,
		})
	}
	if len(candidates) == 0 {
		return rootCauseFinding{}, false
	}

	severity := "degraded"
	for _, node := range candidates {
		if node.CPUPressure >= 20 ||
			node.Blocked >= 6 ||
			node.Running >= 20 ||
			node.IOWait >= 18 ||
			(node.Load1 >= 24 && node.CPUUsage >= 85) {
			severity = "critical"
			break
		}
	}

	confidence := clampRange(
		0.57+
			0.07*float64(minIntLocal(len(candidates), 4))+
			0.05*float64(minIntLocal(crossSignalNodes, 3)),
		0.50,
		0.96,
	)

	finding := rootCauseFinding{
		ID:         "scheduler_contention_tail_latency",
		Category:   "kernel_scheduler",
		Severity:   severity,
		Confidence: confidence,
		Title:      "Scheduler contention is amplifying tail latency",
		Hypothesis: "Run-queue depth, blocked tasks, and CPU/IO stall signals indicate scheduler contention across critical workers.",
		Impact:     "Training and inference steps become jittery as data-loader, networking, and control threads wait for CPU service.",
		CorrelatedSignal: []string{
			"node_load1",
			"node_procs_running",
			"node_procs_blocked",
			"node_cpu_iowait_percent",
			"node_pressure_cpu_some_avg10",
			"node_pressure_io_some_avg10",
		},
		Actions: []string{
			"Isolate IRQ and communication threads onto reserved CPU sets; keep training worker cores free from interrupt storms.",
			"Correlate run-queue spikes with iowait and storage pressure; separate checkpoint bursts from latency-sensitive traffic.",
			"Review pod/workload CPU limits and scheduling policy to reduce cross-tenant contention on hot nodes.",
		},
		Metadata: map[string]float64{
			"candidate_nodes":    float64(len(candidates)),
			"cross_signal_nodes": float64(crossSignalNodes),
		},
		Tags: map[string]string{
			"domain": "kernel_scheduler",
			"path":   "cpu_io_queueing",
		},
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].CPUPressure + candidates[i].IOWait + candidates[i].Blocked*2.0 + candidates[i].Running/4.0 + candidates[i].Load1/8.0
		right := candidates[j].CPUPressure + candidates[j].IOWait + candidates[j].Blocked*2.0 + candidates[j].Running/4.0 + candidates[j].Load1/8.0
		if left != right {
			return left > right
		}
		return candidates[i].CollectorID < candidates[j].CollectorID
	})

	for _, node := range candidates {
		appendNode(&finding, rootCauseNode{
			CollectorID: node.CollectorID,
			Hostname:    node.Hostname,
		})
		appendEvidence(&finding, rootCauseEvidence{
			CollectorID: node.CollectorID,
			Hostname:    node.Hostname,
			Signal:      "node_load1",
			Value:       node.Load1,
			Source:      diagnosticMetricSource("node_load1"),
		})
		appendEvidence(&finding, rootCauseEvidence{
			CollectorID: node.CollectorID,
			Hostname:    node.Hostname,
			Signal:      "node_procs_running",
			Value:       node.Running,
			Source:      diagnosticMetricSource("node_procs_running"),
		})
		if node.Blocked > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "node_procs_blocked",
				Value:       node.Blocked,
				Source:      diagnosticMetricSource("node_procs_blocked"),
			})
		}
		if node.IOWait > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "node_cpu_iowait_percent",
				Value:       node.IOWait,
				Source:      diagnosticMetricSource("node_cpu_iowait_percent"),
			})
		}
		if node.CPUPressure > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "node_pressure_cpu_some_avg10",
				Value:       node.CPUPressure,
				Source:      diagnosticMetricSource("node_pressure_cpu_some_avg10"),
			})
		}
		if node.IOPressure > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "node_pressure_io_some_avg10",
				Value:       node.IOPressure,
				Source:      diagnosticMetricSource("node_pressure_io_some_avg10"),
			})
		}
	}

	return trimRootCauseFinding(finding), true
}

func inferProbeCoreRuntimeFinding(dataPath dataPathDiagnosticsResponse) (rootCauseFinding, bool) {
	if dataPath.Summary.ProbeCoreCritical == 0 &&
		dataPath.Summary.ProbeCoreDegraded == 0 &&
		dataPath.Summary.ProbeCoreFallbackNodes == 0 &&
		dataPath.Summary.ProbeCoreInvalidConfigNodes == 0 {
		return rootCauseFinding{}, false
	}

	severity := "degraded"
	if dataPath.Summary.ProbeCoreCritical > 0 || dataPath.Summary.ProbeCoreInvalidConfigNodes > 0 {
		severity = "critical"
	}
	confidence := clampRange(
		0.62+
			0.08*float64(minIntLocal(dataPath.Summary.ProbeCoreCritical+dataPath.Summary.ProbeCoreDegraded, 4))+
			0.05*float64(minIntLocal(dataPath.Summary.ProbeCoreFallbackNodes, 3)),
		0.55,
		0.97,
	)

	finding := rootCauseFinding{
		ID:         "probe_core_runtime_path_degraded",
		Category:   "observability_runtime",
		Severity:   severity,
		Confidence: confidence,
		Title:      "Probe-core runtime path is degraded",
		Hypothesis: "Low-level telemetry path is falling back or producing stale/invalid frames.",
		Impact:     "Kernel/device observability fidelity is reduced, increasing uncertainty during incidents.",
		CorrelatedSignal: []string{
			"collector_probe_core_active",
			"collector_probe_core_fresh",
			"collector_probe_core_collector_selection_valid",
			"collector_probe_core_last_frame_age_seconds",
			"collector_probe_core_decode_errors_total",
			"collector_probe_core_crc_failures_total",
		},
		Actions: []string{
			"Check probe-core module selection and resolve invalid --collectors configuration.",
			"Inspect probe-core frame age/decode/CRC counters for IPC corruption or stalled streams.",
			"Restore probe-core active source on fallback nodes before relying on low-level RCA evidence.",
		},
		Metadata: map[string]float64{
			"critical_nodes":       float64(dataPath.Summary.ProbeCoreCritical),
			"degraded_nodes":       float64(dataPath.Summary.ProbeCoreDegraded),
			"fallback_nodes":       float64(dataPath.Summary.ProbeCoreFallbackNodes),
			"invalid_config_nodes": float64(dataPath.Summary.ProbeCoreInvalidConfigNodes),
		},
		Tags: map[string]string{
			"domain": "probe_core",
			"path":   "collector_runtime",
		},
	}

	for _, row := range dataPath.ProbeCore.Rankings {
		if row.Signals["configured"] < 0.5 || row.Severity == "healthy" || row.Severity == "not_configured" {
			continue
		}
		appendNode(&finding, rootCauseNode{
			CollectorID: row.CollectorID,
			Hostname:    row.Hostname,
		})
		for _, signal := range []string{"active", "fresh", "selection_valid", "last_frame_age_seconds", "decode_errors_total", "crc_failures_total"} {
			value, ok := row.Signals[signal]
			if !ok {
				continue
			}
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: row.CollectorID,
				Hostname:    row.Hostname,
				Signal:      signal,
				Value:       value,
				Source:      diagnosticMetricSource(signal),
			})
		}
	}
	addAnomalyEvidence(&finding, dataPath.ProbeCore.Anomalies, rootCauseAnomalyEvidence)
	return trimRootCauseFinding(finding), true
}

func inferCrossNodeCommImbalanceFinding(snapshots []*ingest.NodeSnapshot, dataPath dataPathDiagnosticsResponse) (rootCauseFinding, bool) {
	type throughputNode struct {
		CollectorID string
		Hostname    string
		BPS         float64
		Imbalance   float64
	}
	nodes := make([]throughputNode, 0, len(snapshots))
	minNonZero := 0.0
	maxValue := 0.0
	for _, snapshot := range snapshots {
		if snapshot == nil || snapshot.Metrics == nil {
			continue
		}
		tx := metricValueOr(snapshot.Metrics, "node_rdma_port_transmit_bytes_per_second")
		rx := metricValueOr(snapshot.Metrics, "node_rdma_port_receive_bytes_per_second")
		total := tx + rx
		if total <= 0 {
			continue
		}
		denom := tx
		if rx > denom {
			denom = rx
		}
		imbalance := 0.0
		if denom > 0 {
			imbalance = clampRange(abs(tx-rx)/denom, 0, 1)
		}
		hostname := strings.TrimSpace(snapshot.Hostname)
		if hostname == "" {
			hostname = snapshot.CollectorID
		}
		nodes = append(nodes, throughputNode{
			CollectorID: snapshot.CollectorID,
			Hostname:    hostname,
			BPS:         total,
			Imbalance:   imbalance,
		})
		if total > maxValue {
			maxValue = total
		}
		if minNonZero == 0 || total < minNonZero {
			minNonZero = total
		}
	}
	if len(nodes) < 2 || minNonZero <= 0 || maxValue <= 0 {
		return rootCauseFinding{}, false
	}

	throughputSkew := maxValue / minNonZero
	hotImbalanceNodes := 0
	for _, node := range nodes {
		if node.Imbalance >= 0.2 {
			hotImbalanceNodes++
		}
	}
	if throughputSkew < 1.8 && hotImbalanceNodes == 0 {
		return rootCauseFinding{}, false
	}

	severity := "degraded"
	if throughputSkew >= 3.0 || hotImbalanceNodes >= len(nodes)/2 {
		severity = "critical"
	}
	confidence := clampRange(0.56+0.07*clampRange((throughputSkew-1.0)/2.0, 0, 1)+0.04*float64(minIntLocal(hotImbalanceNodes, 3)), 0.5, 0.95)

	finding := rootCauseFinding{
		ID:         "cross_node_communication_imbalance",
		Category:   "network_topology",
		Severity:   severity,
		Confidence: confidence,
		Title:      "Cross-node communication imbalance is creating stragglers",
		Hypothesis: "Inter-node throughput distribution is uneven, indicating topology or placement skew.",
		Impact:     "Collective phases wait on stragglers, reducing cluster-wide step efficiency.",
		CorrelatedSignal: []string{
			"node_rdma_port_transmit_bytes_per_second",
			"node_rdma_port_receive_bytes_per_second",
			"rdma_comm_imbalance_ratio",
		},
		Actions: []string{
			"Check placement skew across racks/fabrics and rebalance high-traffic workloads.",
			"Validate per-link bandwidth allocation and congestion domain boundaries.",
			"Compare collective latency histogram across nodes to identify persistent stragglers.",
		},
		Metadata: map[string]float64{
			"throughput_skew_ratio":  throughputSkew,
			"hot_imbalance_nodes":    float64(hotImbalanceNodes),
			"network_critical_nodes": float64(dataPath.Summary.NetworkCritical),
		},
		Tags: map[string]string{
			"domain": "fabric_topology",
		},
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].BPS != nodes[j].BPS {
			return nodes[i].BPS > nodes[j].BPS
		}
		return nodes[i].CollectorID < nodes[j].CollectorID
	})
	for _, node := range nodes {
		appendNode(&finding, rootCauseNode{
			CollectorID: node.CollectorID,
			Hostname:    node.Hostname,
		})
		appendEvidence(&finding, rootCauseEvidence{
			CollectorID: node.CollectorID,
			Hostname:    node.Hostname,
			Signal:      "node_rdma_port_total_bytes_per_second",
			Value:       node.BPS,
			Source:      diagnosticMetricSource("node_rdma_port_transmit_bytes_per_second"),
			Note:        "Per-node RDMA throughput used for skew detection.",
		})
		if node.Imbalance > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: node.CollectorID,
				Hostname:    node.Hostname,
				Signal:      "rdma_comm_imbalance_ratio",
				Value:       node.Imbalance,
				Source:      diagnosticMetricSource("rdma_comm_imbalance_ratio"),
			})
		}
	}

	return trimRootCauseFinding(finding), true
}

func inferCollectiveRuntimeContentionFinding(topPrograms []ProgramStats, dataPath dataPathDiagnosticsResponse) (rootCauseFinding, bool) {
	type processSignal struct {
		CollectorID   string
		Hostname      string
		PID           string
		Name          string
		CommPattern   string
		QueuedBytes   float64
		Connections   int
		SchedWait     float64
		NetworkWeight float64
	}

	candidates := make([]processSignal, 0, len(topPrograms))
	queueHotspots := 0
	severeQueueHotspots := 0
	waitHotspots := 0
	severeWaitHotspots := 0
	connectionHotspots := 0
	maxQueuedBytes := 0.0

	for _, program := range topPrograms {
		pattern := strings.ToLower(strings.TrimSpace(program.CommPattern))
		if !aiInfraIsCollectivePattern(pattern) {
			continue
		}
		hasSignal := program.NetQueuedBytes > 0 || program.NetConnections > 0 || program.SchedWaitRatio > 0
		if !hasSignal {
			continue
		}

		if program.NetQueuedBytes >= 1*1024*1024 {
			queueHotspots++
		}
		if program.NetQueuedBytes >= 8*1024*1024 {
			severeQueueHotspots++
		}
		if program.SchedWaitRatio >= 0.6 {
			waitHotspots++
		}
		if program.SchedWaitRatio >= 1.0 {
			severeWaitHotspots++
		}
		if program.NetConnections >= 64 {
			connectionHotspots++
		}
		if program.NetQueuedBytes > maxQueuedBytes {
			maxQueuedBytes = program.NetQueuedBytes
		}

		candidates = append(candidates, processSignal{
			CollectorID:   strings.TrimSpace(program.CollectorID),
			Hostname:      strings.TrimSpace(program.Hostname),
			PID:           strings.TrimSpace(program.PID),
			Name:          strings.TrimSpace(program.Name),
			CommPattern:   pattern,
			QueuedBytes:   program.NetQueuedBytes,
			Connections:   program.NetConnections,
			SchedWait:     program.SchedWaitRatio,
			NetworkWeight: program.NetBytesPerSecond,
		})
	}

	if len(candidates) == 0 || (queueHotspots == 0 && waitHotspots == 0) {
		return rootCauseFinding{}, false
	}

	networkCorroborated := 0
	networkByCollector := make(map[string]resourcePressureRow, len(dataPath.Network.Rankings))
	for _, row := range dataPath.Network.Rankings {
		networkByCollector[row.CollectorID] = row
	}
	for _, candidate := range candidates {
		row, ok := networkByCollector[candidate.CollectorID]
		if !ok {
			continue
		}
		if row.Signals["tcp_retransmit_ratio"] >= 0.01 ||
			row.Signals["tx_queue_fill_percent"] >= 70 ||
			row.Signals["rdma_congestion_per_second"] > 0 {
			networkCorroborated++
		}
	}

	severity := "degraded"
	if severeQueueHotspots > 0 && severeWaitHotspots > 0 {
		severity = "critical"
	} else if queueHotspots >= 2 && waitHotspots >= 1 {
		severity = "critical"
	} else if networkCorroborated >= 2 && (queueHotspots > 0 || waitHotspots > 0) {
		severity = "critical"
	}

	confidence := clampRange(
		0.58+
			0.05*float64(minIntLocal(len(candidates), 4))+
			0.05*float64(minIntLocal(queueHotspots, 3))+
			0.05*float64(minIntLocal(waitHotspots, 3))+
			0.03*float64(minIntLocal(networkCorroborated, 3))+
			0.02*float64(minIntLocal(dataPath.Summary.NetworkCritical, 2)),
		0.52,
		0.98,
	)

	finding := rootCauseFinding{
		ID:         "collective_runtime_queueing_contention",
		Category:   "collective_runtime",
		Severity:   severity,
		Confidence: confidence,
		Title:      "Collective runtime queueing contention is degrading step latency",
		Hypothesis: "Communication-active workers show socket backlog and scheduler wait growth, indicating runtime-level contention during collective phases.",
		Impact:     "AllReduce/synchronization phases wait on contended workers, increasing step jitter and lowering throughput.",
		CorrelatedSignal: []string{
			"rca_net_process_queued_bytes",
			"rca_net_process_connections",
			"rca_cpu_process_sched_wait_ratio",
			"tcp_retransmit_ratio",
			"tx_queue_fill_percent",
		},
		Actions: []string{
			"Inspect top collective workers with high socket queue backlog and reduce communication fan-out where possible.",
			"Correlate process scheduler wait with CPU run-queue pressure on the same nodes before changing NCCL/UCX tuning.",
			"Rebalance collective-heavy workloads away from nodes with persistent TX queue or retransmit pressure.",
		},
		Metadata: map[string]float64{
			"candidate_processes":     float64(len(candidates)),
			"queue_hotspots":          float64(queueHotspots),
			"sched_wait_hotspots":     float64(waitHotspots),
			"connection_hotspots":     float64(connectionHotspots),
			"network_corroborated":    float64(networkCorroborated),
			"max_queued_bytes":        maxQueuedBytes,
			"network_critical_nodes":  float64(dataPath.Summary.NetworkCritical),
			"network_degraded_nodes":  float64(dataPath.Summary.NetworkDegraded),
			"network_anomaly_samples": float64(len(dataPath.Network.Anomalies)),
		},
		Tags: map[string]string{
			"domain":   "collective_runtime",
			"workflow": "process_to_fabric",
		},
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := clampRange(candidates[i].QueuedBytes/(1024*1024), 0, 1024) +
			float64(candidates[i].Connections)/16.0 +
			candidates[i].SchedWait*8.0 +
			candidates[i].NetworkWeight/(128.0*1024.0*1024.0)
		right := clampRange(candidates[j].QueuedBytes/(1024*1024), 0, 1024) +
			float64(candidates[j].Connections)/16.0 +
			candidates[j].SchedWait*8.0 +
			candidates[j].NetworkWeight/(128.0*1024.0*1024.0)
		if left != right {
			return left > right
		}
		if candidates[i].CollectorID != candidates[j].CollectorID {
			return candidates[i].CollectorID < candidates[j].CollectorID
		}
		return candidates[i].PID < candidates[j].PID
	})

	for _, candidate := range candidates {
		if candidate.CollectorID == "" {
			continue
		}
		hostname := candidate.Hostname
		if hostname == "" {
			hostname = candidate.CollectorID
		}
		processLabel := strings.TrimSpace(candidate.Name)
		if processLabel == "" {
			processLabel = candidate.PID
		}
		if processLabel == "" {
			processLabel = "unknown"
		}

		appendNode(&finding, rootCauseNode{
			CollectorID: candidate.CollectorID,
			Hostname:    hostname,
		})
		if candidate.QueuedBytes > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: candidate.CollectorID,
				Hostname:    hostname,
				Signal:      "rca_net_process_queued_bytes",
				Value:       candidate.QueuedBytes,
				Source:      diagnosticMetricSource("rca_net_process_queued_bytes"),
				Note:        "process=" + processLabel + " pid=" + candidate.PID + " comm=" + candidate.CommPattern,
			})
		}
		if candidate.Connections > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: candidate.CollectorID,
				Hostname:    hostname,
				Signal:      "rca_net_process_connections",
				Value:       float64(candidate.Connections),
				Source:      diagnosticMetricSource("rca_net_process_connections"),
				Note:        "process=" + processLabel + " pid=" + candidate.PID + " comm=" + candidate.CommPattern,
			})
		}
		if candidate.SchedWait > 0 {
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: candidate.CollectorID,
				Hostname:    hostname,
				Signal:      "rca_cpu_process_sched_wait_ratio",
				Value:       candidate.SchedWait,
				Source:      diagnosticMetricSource("rca_cpu_process_sched_wait_ratio"),
				Note:        "process=" + processLabel + " pid=" + candidate.PID + " comm=" + candidate.CommPattern,
			})
		}

		row, ok := networkByCollector[candidate.CollectorID]
		if !ok {
			continue
		}
		for _, signal := range []string{"tcp_retransmit_ratio", "tx_queue_fill_percent", "rdma_congestion_per_second"} {
			value, exists := row.Signals[signal]
			if !exists || value <= 0 {
				continue
			}
			appendEvidence(&finding, rootCauseEvidence{
				CollectorID: candidate.CollectorID,
				Hostname:    hostname,
				Signal:      signal,
				Value:       value,
				Source:      diagnosticMetricSource(signal),
				Note:        "network corroboration",
			})
		}
	}

	addAnomalyEvidence(&finding, dataPath.Network.Anomalies, 2)
	return trimRootCauseFinding(finding), true
}

func addRowsAsEvidence(finding *rootCauseFinding, rows []resourcePressureRow, limit int, signalOrder []string) {
	if finding == nil {
		return
	}
	for _, row := range rows {
		appendNode(finding, rootCauseNode{
			CollectorID: row.CollectorID,
			Hostname:    row.Hostname,
		})
		for _, signal := range signalOrder {
			value, ok := row.Signals[signal]
			if !ok {
				continue
			}
			appendEvidence(finding, rootCauseEvidence{
				CollectorID: row.CollectorID,
				Hostname:    row.Hostname,
				Signal:      signal,
				Value:       value,
				Source:      diagnosticMetricSource(signal),
			})
		}
		if len(finding.AffectedNodes) >= limit {
			break
		}
	}
}

func addAnomalyEvidence(finding *rootCauseFinding, anomalies []resourceAnomaly, limit int) {
	if finding == nil || len(anomalies) == 0 || limit <= 0 {
		return
	}
	for _, anomaly := range anomalies {
		appendEvidence(finding, rootCauseEvidence{
			CollectorID: anomaly.CollectorID,
			Hostname:    anomaly.Hostname,
			Signal:      anomaly.Metric,
			Value:       anomaly.Value,
			Baseline:    anomaly.Baseline,
			ZScore:      anomaly.ZScore,
			Source:      diagnosticMetricSource(anomaly.Metric),
			Note:        fmt.Sprintf("Anomaly z-score %.2f", anomaly.ZScore),
		})
		if len(finding.Evidence) >= rootCauseMaxEvidence || limit <= 1 {
			break
		}
		limit--
	}
}

func appendNode(finding *rootCauseFinding, node rootCauseNode) {
	if finding == nil {
		return
	}
	for _, existing := range finding.AffectedNodes {
		if existing.CollectorID == node.CollectorID {
			return
		}
	}
	if len(finding.AffectedNodes) >= rootCauseMaxAffectedNodes {
		return
	}
	finding.AffectedNodes = append(finding.AffectedNodes, node)
}

func appendEvidence(finding *rootCauseFinding, evidence rootCauseEvidence) {
	if finding == nil {
		return
	}
	for _, existing := range finding.Evidence {
		if existing.CollectorID == evidence.CollectorID &&
			existing.Signal == evidence.Signal &&
			existing.Note == evidence.Note {
			return
		}
	}
	if len(finding.Evidence) >= rootCauseMaxEvidence {
		return
	}
	finding.Evidence = append(finding.Evidence, evidence)
}

func trimRootCauseFinding(finding rootCauseFinding) rootCauseFinding {
	if len(finding.AffectedNodes) > rootCauseMaxAffectedNodes {
		finding.AffectedNodes = finding.AffectedNodes[:rootCauseMaxAffectedNodes]
	}
	if len(finding.Evidence) > rootCauseMaxEvidence {
		finding.Evidence = finding.Evidence[:rootCauseMaxEvidence]
	}
	return finding
}

func severityRank(severity string) int {
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

func minIntLocal(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func diagnosticMetricSource(metric string) string {
	key := strings.ToLower(strings.TrimSpace(metric))
	switch {
	case strings.Contains(key, "rca_net_process_"):
		return "/proc/net/tcp + /proc/*/fd"
	case strings.Contains(key, "rca_cpu_process_sched_wait"), strings.Contains(key, "rca_cpu_process_sched_run"):
		return "/proc/[pid]/schedstat"
	case strings.Contains(key, "rdma"):
		return "/sys/class/infiniband/*/ports/*/{counters,hw_counters,state,rate}"
	case strings.Contains(key, "pressure_cpu"):
		return "/proc/pressure/cpu"
	case strings.Contains(key, "tcp_retransmit"):
		return "/proc/net/snmp"
	case strings.Contains(key, "softnet"):
		return "/proc/net/softnet_stat"
	case strings.Contains(key, "load1"), strings.Contains(key, "load5"), strings.Contains(key, "load15"):
		return "/proc/loadavg"
	case strings.Contains(key, "procs_running"), strings.Contains(key, "procs_blocked"), strings.Contains(key, "cpu_iowait"):
		return "/proc/stat"
	case strings.Contains(key, "schedstat"):
		return "/proc/schedstat"
	case strings.Contains(key, "tx_queue_fill"):
		return "/sys/class/net/<iface>/tx_queue_len + /proc/net/dev"
	case strings.Contains(key, "network_utilization"):
		return "/proc/net/dev + /sys/class/net/<iface>/speed"
	case strings.Contains(key, "disk_"), strings.Contains(key, "nvme_"):
		return "/proc/diskstats"
	case strings.Contains(key, "pressure_io"):
		return "/proc/pressure/io"
	case strings.Contains(key, "dirty"), strings.Contains(key, "writeback"):
		return "/proc/meminfo"
	case strings.Contains(key, "vmstat_pg"), strings.Contains(key, "vm_pgpg"), strings.Contains(key, "vmstat_pswp"):
		return "/proc/vmstat"
	case strings.Contains(key, "dataloader_prefetch"), strings.Contains(key, "checkpoint_write"), strings.Contains(key, "cache_hit"):
		return "collector pipeline counters (runtime attribution)"
	case strings.Contains(key, "probe_core"), key == "active", key == "fresh", key == "selection_valid", key == "decode_errors_total", key == "crc_failures_total", key == "last_frame_age_seconds":
		return "probe-core IPC runtime counters"
	case strings.Contains(key, "gpu_utilization"):
		return "nvidia-smi / NVML / DCGM"
	default:
		return "collector aggregated metric"
	}
}
