package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

const (
	kernelPathGiB = 1024 * 1024 * 1024
	kernelPathMiB = 1024 * 1024
)

type kernelPathDiagnosticsResponse struct {
	CollectorID string                       `json:"collector_id,omitempty"`
	GeneratedAt time.Time                    `json:"generated_at"`
	Summary     kernelPathDiagnosticsSummary `json:"summary"`
	Nodes       []kernelPathNodeDiagnostics  `json:"nodes"`
}

type kernelPathDiagnosticsSummary struct {
	NodeCount        int    `json:"node_count"`
	CriticalNodes    int    `json:"critical_nodes"`
	DegradedNodes    int    `json:"degraded_nodes"`
	TopStorageStage  string `json:"top_storage_stage,omitempty"`
	TopNetworkStage  string `json:"top_network_stage,omitempty"`
	TopBottleneckKey string `json:"top_bottleneck_key,omitempty"`
}

type kernelPathNodeDiagnostics struct {
	CollectorID     string                      `json:"collector_id"`
	Hostname        string                      `json:"hostname"`
	Storage         kernelPathDomainDiagnostics `json:"storage"`
	Network         kernelPathDomainDiagnostics `json:"network"`
	OverallSeverity string                      `json:"overall_severity"`
	Bottlenecks     []string                    `json:"bottlenecks,omitempty"`
}

type kernelPathDomainDiagnostics struct {
	Score    float64                      `json:"score"`
	Severity string                       `json:"severity"`
	TopStage string                       `json:"top_stage,omitempty"`
	Stages   []kernelPathStageDiagnostics `json:"stages"`
}

type kernelPathStageDiagnostics struct {
	Name     string             `json:"name"`
	Score    float64            `json:"score"`
	Severity string             `json:"severity"`
	Signals  map[string]float64 `json:"signals,omitempty"`
	Sources  map[string]string  `json:"sources,omitempty"`
	Notes    []string           `json:"notes,omitempty"`
}

func (c *Controller) handleKernelPathDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	collectorID := strings.TrimSpace(r.URL.Query().Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(r.URL.Query().Get("collector"))
	}

	resp := c.buildKernelPathDiagnostics(collectorID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (c *Controller) buildKernelPathDiagnostics(collectorID string) kernelPathDiagnosticsResponse {
	resp := kernelPathDiagnosticsResponse{
		CollectorID: collectorID,
		GeneratedAt: time.Now(),
		Summary:     kernelPathDiagnosticsSummary{},
		Nodes:       []kernelPathNodeDiagnostics{},
	}
	if c.ingestStore == nil {
		return resp
	}

	snapshots := c.ingestStore.Snapshot()
	filtered := make([]*ingest.NodeSnapshot, 0, len(snapshots))
	for _, node := range snapshots {
		if collectorID != "" && node.CollectorID != collectorID {
			continue
		}
		filtered = append(filtered, node)
	}
	resp.Summary.NodeCount = len(filtered)
	if len(filtered) == 0 {
		return resp
	}

	storageStageTotals := map[string]float64{}
	storageStageCounts := map[string]int{}
	networkStageTotals := map[string]float64{}
	networkStageCounts := map[string]int{}
	bottleneckCounts := map[string]int{}

	for _, node := range filtered {
		if node == nil {
			continue
		}
		hostname := strings.TrimSpace(node.Hostname)
		if hostname == "" {
			hostname = node.CollectorID
		}

		storage := evaluateStorageKernelPath(node.Metrics)
		network := evaluateNetworkKernelPath(node.Metrics)
		overall := worseSeverity(storage.Severity, network.Severity)
		bottlenecks := make([]string, 0, 2)
		if storage.Severity != "healthy" && storage.TopStage != "" {
			key := "storage:" + storage.TopStage
			bottlenecks = append(bottlenecks, key)
			bottleneckCounts[key]++
		}
		if network.Severity != "healthy" && network.TopStage != "" {
			key := "network:" + network.TopStage
			bottlenecks = append(bottlenecks, key)
			bottleneckCounts[key]++
		}

		for _, stage := range storage.Stages {
			storageStageTotals[stage.Name] += stage.Score
			storageStageCounts[stage.Name]++
		}
		for _, stage := range network.Stages {
			networkStageTotals[stage.Name] += stage.Score
			networkStageCounts[stage.Name]++
		}

		resp.Nodes = append(resp.Nodes, kernelPathNodeDiagnostics{
			CollectorID:     node.CollectorID,
			Hostname:        hostname,
			Storage:         storage,
			Network:         network,
			OverallSeverity: overall,
			Bottlenecks:     bottlenecks,
		})

		switch overall {
		case "critical":
			resp.Summary.CriticalNodes++
		case "degraded":
			resp.Summary.DegradedNodes++
		}
	}

	sort.Slice(resp.Nodes, func(i, j int) bool {
		leftRank := kernelSeverityRank(resp.Nodes[i].OverallSeverity)
		rightRank := kernelSeverityRank(resp.Nodes[j].OverallSeverity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		leftScore := resp.Nodes[i].Storage.Score + resp.Nodes[i].Network.Score
		rightScore := resp.Nodes[j].Storage.Score + resp.Nodes[j].Network.Score
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return resp.Nodes[i].CollectorID < resp.Nodes[j].CollectorID
	})

	resp.Summary.TopStorageStage = topKernelStageByAverage(storageStageTotals, storageStageCounts)
	resp.Summary.TopNetworkStage = topKernelStageByAverage(networkStageTotals, networkStageCounts)
	resp.Summary.TopBottleneckKey = topBottleneck(bottleneckCounts)
	return resp
}

func evaluateStorageKernelPath(metrics map[string]float64) kernelPathDomainDiagnostics {
	stages := []kernelPathStageDiagnostics{
		storageStageSyscallVFS(metrics),
		storageStagePageCacheWriteback(metrics),
		storageStageBlockLayer(metrics),
		storageStageNVMeDevice(metrics),
		storageStageRemoteCheckpoint(metrics),
	}
	return finalizeKernelPathDomain(stages)
}

func evaluateNetworkKernelPath(metrics map[string]float64) kernelPathDomainDiagnostics {
	stages := []kernelPathStageDiagnostics{
		networkStageNIC(metrics),
		networkStageInterruptNAPI(metrics),
		networkStageSocketTCP(metrics),
		networkStageEgressQueue(metrics),
		networkStageRDMAFabric(metrics),
	}
	return finalizeKernelPathDomain(stages)
}

func finalizeKernelPathDomain(stages []kernelPathStageDiagnostics) kernelPathDomainDiagnostics {
	domain := kernelPathDomainDiagnostics{
		Stages: stages,
	}
	if len(stages) == 0 {
		domain.Severity = "healthy"
		return domain
	}
	topIdx := 0
	secondBest := 0.0
	for i, stage := range stages {
		if stage.Score > stages[topIdx].Score {
			secondBest = stages[topIdx].Score
			topIdx = i
		} else if stage.Score > secondBest {
			secondBest = stage.Score
		}
	}
	if stages[topIdx].Score > 0 {
		domain.TopStage = stages[topIdx].Name
		domain.Score = stages[topIdx].Score + 0.35*secondBest
	}
	domain.Severity = kernelDomainSeverity(domain.Score)
	return domain
}

func storageStageSyscallVFS(metrics map[string]float64) kernelPathStageDiagnostics {
	smallIO := metricValueOr(metrics, "node_storage_small_io_ratio")
	metadataOps := metricValueOr(metrics, "node_storage_metadata_ops_per_second")
	metadataLatMs := metricValueOr(metrics, "node_storage_metadata_latency_p99_seconds") * 1000.0
	score := clamp01(smallIO/0.45)*1.4 + clamp01(metadataOps/25000.0)*0.7 + clamp01(metadataLatMs/25.0)*1.6
	signals := compactPositiveSignals(map[string]float64{
		"small_io_ratio":          smallIO,
		"metadata_ops_per_second": metadataOps,
		"metadata_latency_p99_ms": metadataLatMs,
	})
	notes := make([]string, 0, 3)
	if smallIO >= 0.35 {
		notes = append(notes, "Small-file overhead is high at syscall/VFS path.")
	}
	if metadataLatMs >= 15 {
		notes = append(notes, "Metadata latency is elevated; check distributed metadata service pressure.")
	}
	return kernelPathStageDiagnostics{
		Name:     "syscall_vfs",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"small_io_ratio":          "node_storage_small_io_ratio",
			"metadata_ops_per_second": "node_storage_metadata_ops_per_second",
			"metadata_latency_p99_ms": "node_storage_metadata_latency_p99_seconds",
		}),
		Notes: notes,
	}
}

func storageStagePageCacheWriteback(metrics map[string]float64) kernelPathStageDiagnostics {
	dirtyBytes := metricValueOr(metrics, "node_memory_Dirty_bytes")
	writebackBytes := metricValueOr(metrics, "node_memory_Writeback_bytes")
	ioPressure := metricValueOr(metrics, "node_pressure_io_full_avg10")
	pageOut := metricValueOr(metrics, "node_vmstat_pgpgout_per_second")
	dirtied := metricValueOr(metrics, "node_vmstat_nr_dirtied_per_second")
	written := metricValueOr(metrics, "node_vmstat_nr_written_per_second")
	iowait := metricValueOr(metrics, "node_cpu_iowait_percent")
	dirtyGap := 0.0
	if dirtied > written {
		dirtyGap = dirtied - written
	}
	score := clamp01(dirtyBytes/(2.5*kernelPathGiB))*1.2 +
		clamp01(writebackBytes/(512.0*kernelPathMiB))*1.3 +
		clamp01(ioPressure/20.0)*1.6 +
		clamp01(pageOut/200000.0)*0.8 +
		clamp01(dirtyGap/120000.0)*0.8 +
		clamp01(iowait/25.0)*0.6
	signals := compactPositiveSignals(map[string]float64{
		"dirty_bytes":              dirtyBytes,
		"writeback_bytes":          writebackBytes,
		"io_pressure_full_avg10":   ioPressure,
		"vm_pgpgout_per_second":    pageOut,
		"vm_dirtied_pages_per_sec": dirtied,
		"vm_written_pages_per_sec": written,
		"cpu_iowait_percent":       iowait,
	})
	notes := make([]string, 0, 3)
	if ioPressure >= 8 {
		notes = append(notes, "Kernel reports sustained full I/O pressure in page-cache/writeback path.")
	}
	if dirtyGap > 0 {
		notes = append(notes, "Dirtying rate exceeds writeback rate; dirty backlog is accumulating.")
	}
	if iowait >= 10 {
		notes = append(notes, "CPU iowait is elevated alongside writeback pressure.")
	}
	return kernelPathStageDiagnostics{
		Name:     "page_cache_writeback",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"dirty_bytes":              "node_memory_Dirty_bytes",
			"writeback_bytes":          "node_memory_Writeback_bytes",
			"io_pressure_full_avg10":   "node_pressure_io_full_avg10",
			"vm_pgpgout_per_second":    "node_vmstat_pgpgout_per_second",
			"vm_dirtied_pages_per_sec": "node_vmstat_nr_dirtied_per_second",
			"vm_written_pages_per_sec": "node_vmstat_nr_written_per_second",
			"cpu_iowait_percent":       "node_cpu_iowait_percent",
		}),
		Notes: notes,
	}
}

func storageStageBlockLayer(metrics map[string]float64) kernelPathStageDiagnostics {
	queueDepth := metricValueOr(metrics, "node_disk_queue_depth_total")
	queueFill := metricValueOr(metrics, "node_disk_queue_depth_fill_percent")
	utilPeak := metricValueOr(metrics, "node_disk_utilization_peak_percent")
	latP99ms := metricValueOr(metrics, "node_disk_request_latency_p99_seconds") * 1000.0
	score := clamp01(queueDepth/96.0)*1.2 + clamp01(queueFill/100.0)*0.8 + clamp01(utilPeak/100.0)*1.3 + clamp01(latP99ms/80.0)*1.8
	signals := compactPositiveSignals(map[string]float64{
		"queue_depth_total":        queueDepth,
		"queue_depth_fill_percent": queueFill,
		"utilization_peak_percent": utilPeak,
		"latency_p99_ms":           latP99ms,
	})
	notes := make([]string, 0, 3)
	if queueDepth >= 48 || queueFill >= 60 {
		notes = append(notes, "Block layer queue pressure is elevated.")
	}
	if latP99ms >= 30 {
		notes = append(notes, "Tail disk latency is high at block layer.")
	}
	return kernelPathStageDiagnostics{
		Name:     "block_layer_device",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"queue_depth_total":        "node_disk_queue_depth_total",
			"queue_depth_fill_percent": "node_disk_queue_depth_fill_percent",
			"utilization_peak_percent": "node_disk_utilization_peak_percent",
			"latency_p99_ms":           "node_disk_request_latency_p99_seconds",
		}),
		Notes: notes,
	}
}

func storageStageNVMeDevice(metrics map[string]float64) kernelPathStageDiagnostics {
	queueDepth := metricValueOr(metrics, "node_nvme_queue_depth_total")
	utilPeak := metricValueOr(metrics, "node_nvme_utilization_peak_percent")
	latencyMs := metricValueOr(metrics, "node_nvme_avg_request_latency_seconds") * 1000.0
	score := clamp01(queueDepth/96.0)*1.1 + clamp01(utilPeak/100.0)*1.4 + clamp01(latencyMs/60.0)*1.4
	signals := compactPositiveSignals(map[string]float64{
		"nvme_queue_depth_total":        queueDepth,
		"nvme_utilization_peak_percent": utilPeak,
		"nvme_avg_latency_ms":           latencyMs,
	})
	notes := make([]string, 0, 2)
	if utilPeak >= 85 || queueDepth >= 48 {
		notes = append(notes, "Local NVMe path is approaching saturation.")
	}
	return kernelPathStageDiagnostics{
		Name:     "nvme_device",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"nvme_queue_depth_total":        "node_nvme_queue_depth_total",
			"nvme_utilization_peak_percent": "node_nvme_utilization_peak_percent",
			"nvme_avg_latency_ms":           "node_nvme_avg_request_latency_seconds",
		}),
		Notes: notes,
	}
}

func storageStageRemoteCheckpoint(metrics map[string]float64) kernelPathStageDiagnostics {
	objectGetMs := metricValueOr(metrics, "node_object_storage_get_latency_p99_seconds") * 1000.0
	objectPutMs := metricValueOr(metrics, "node_object_storage_put_latency_p99_seconds") * 1000.0
	checkpointMs := metricValueOr(metrics, "node_checkpoint_write_latency_p99_seconds") * 1000.0
	prefetchStall := metricValueOr(metrics, "node_dataloader_prefetch_stall_ratio")
	cacheHit := metricValueOr(metrics, "node_cache_hit_ratio")
	cachePenalty := 0.0
	if cacheHit > 0 {
		cachePenalty = clamp01((1.0-cacheHit)/0.45) * 0.7
	}
	score := clamp01(objectGetMs/100.0)*0.9 + clamp01(objectPutMs/150.0)*0.7 + clamp01(checkpointMs/220.0)*1.2 + clamp01(prefetchStall/0.35)*1.2 + cachePenalty
	signals := compactPositiveSignals(map[string]float64{
		"object_get_latency_p99_ms":       objectGetMs,
		"object_put_latency_p99_ms":       objectPutMs,
		"checkpoint_write_latency_p99_ms": checkpointMs,
		"dataloader_prefetch_stall_ratio": prefetchStall,
		"cache_hit_ratio":                 cacheHit,
	})
	notes := make([]string, 0, 3)
	if checkpointMs >= 120 {
		notes = append(notes, "Checkpoint flush path is slow and can cause bursty write amplification.")
	}
	if prefetchStall >= 0.15 {
		notes = append(notes, "Data-loader prefetch stalls indicate upstream storage wait.")
	}
	return kernelPathStageDiagnostics{
		Name:     "remote_object_checkpoint",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"object_get_latency_p99_ms":       "node_object_storage_get_latency_p99_seconds",
			"object_put_latency_p99_ms":       "node_object_storage_put_latency_p99_seconds",
			"checkpoint_write_latency_p99_ms": "node_checkpoint_write_latency_p99_seconds",
			"dataloader_prefetch_stall_ratio": "node_dataloader_prefetch_stall_ratio",
			"cache_hit_ratio":                 "node_cache_hit_ratio",
		}),
		Notes: notes,
	}
}

func networkStageNIC(metrics map[string]float64) kernelPathStageDiagnostics {
	utilPeak := metricValueOr(metrics, "node_network_utilization_peak_percent", "node_network_capacity_utilization_percent")
	drops := metricValueOr(metrics, "node_network_total_drop_per_second")
	errs := metricValueOr(metrics, "node_network_total_errs_per_second")
	score := clamp01(utilPeak/100.0)*1.8 + clamp01(drops/300.0)*1.0 + clamp01(errs/80.0)*0.8
	signals := compactPositiveSignals(map[string]float64{
		"utilization_peak_percent": utilPeak,
		"drops_per_second":         drops,
		"errors_per_second":        errs,
	})
	notes := make([]string, 0, 2)
	if utilPeak >= 80 {
		notes = append(notes, "NIC link utilization is high.")
	}
	return kernelPathStageDiagnostics{
		Name:     "nic_link",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"utilization_peak_percent": "node_network_utilization_peak_percent",
			"drops_per_second":         "node_network_total_drop_per_second",
			"errors_per_second":        "node_network_total_errs_per_second",
		}),
		Notes: notes,
	}
}

func networkStageInterruptNAPI(metrics map[string]float64) kernelPathStageDiagnostics {
	irq := metricValueOr(metrics, "node_network_interrupts_per_second")
	softnetDrop := metricValueOr(metrics, "node_softnet_dropped_per_second")
	softnetSqueezed := metricValueOr(metrics, "node_softnet_times_squeezed_per_second")
	score := clamp01(irq/70000.0)*0.8 + clamp01(softnetDrop/200.0)*1.7 + clamp01(softnetSqueezed/120.0)*1.1
	signals := compactPositiveSignals(map[string]float64{
		"interrupts_per_second":       irq,
		"softnet_dropped_per_second":  softnetDrop,
		"softnet_squeezed_per_second": softnetSqueezed,
	})
	notes := make([]string, 0, 2)
	if softnetDrop >= 20 || softnetSqueezed >= 20 {
		notes = append(notes, "Receive path shows NAPI budget pressure and packet handling backlog.")
	}
	return kernelPathStageDiagnostics{
		Name:     "interrupt_napi",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"interrupts_per_second":       "node_network_interrupts_per_second",
			"softnet_dropped_per_second":  "node_softnet_dropped_per_second",
			"softnet_squeezed_per_second": "node_softnet_times_squeezed_per_second",
		}),
		Notes: notes,
	}
}

func networkStageSocketTCP(metrics map[string]float64) kernelPathStageDiagnostics {
	retransRatio := metricValueOr(metrics, "node_tcp_retransmit_ratio")
	retransRate := metricValueOr(metrics, "node_tcp_retransmits_per_second")
	timewait := metricValueOr(metrics, "node_tcp_sockets_timewait")
	orphan := metricValueOr(metrics, "node_tcp_sockets_orphan")
	score := clamp01(retransRatio/0.02)*1.8 + clamp01(retransRate/900.0)*0.9 + clamp01(timewait/20000.0)*0.5 + clamp01(orphan/512.0)*0.6
	signals := compactPositiveSignals(map[string]float64{
		"tcp_retransmit_ratio":       retransRatio,
		"tcp_retransmits_per_second": retransRate,
		"tcp_sockets_timewait":       timewait,
		"tcp_sockets_orphan":         orphan,
	})
	notes := make([]string, 0, 2)
	if retransRatio >= 0.01 {
		notes = append(notes, "TCP retransmit ratio is high and can inflate tail latency.")
	}
	return kernelPathStageDiagnostics{
		Name:     "socket_tcp",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"tcp_retransmit_ratio":       "node_tcp_retransmit_ratio",
			"tcp_retransmits_per_second": "node_tcp_retransmits_per_second",
			"tcp_sockets_timewait":       "node_tcp_sockets_timewait",
			"tcp_sockets_orphan":         "node_tcp_sockets_orphan",
		}),
		Notes: notes,
	}
}

func networkStageEgressQueue(metrics map[string]float64) kernelPathStageDiagnostics {
	txQueueFill := metricValueOr(metrics, "node_network_interface_tx_queue_fill_percent")
	score := clamp01(txQueueFill/100.0) * 2.4
	signals := compactPositiveSignals(map[string]float64{
		"tx_queue_fill_percent": txQueueFill,
	})
	notes := make([]string, 0, 1)
	if txQueueFill >= 70 {
		notes = append(notes, "NIC egress queue pressure suggests congestion domain oversubscription.")
	}
	return kernelPathStageDiagnostics{
		Name:     "egress_queue",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"tx_queue_fill_percent": "node_network_interface_tx_queue_fill_percent",
		}),
		Notes: notes,
	}
}

func networkStageRDMAFabric(metrics map[string]float64) kernelPathStageDiagnostics {
	rdmaErr := metricValueOr(metrics, "node_rdma_errors_per_second", "node_rdma_port_errors_per_second")
	rdmaCong := metricValueOr(metrics, "node_rdma_congestion_events_per_second", "node_rdma_port_congestion_events_per_second")
	pfcPause := metricValueOr(metrics, "node_rdma_pfc_pause_frames_per_second")
	ecnMarked := metricValueOr(metrics, "node_rdma_ecn_marked_ratio")
	tx := metricValueOr(metrics, "node_rdma_port_transmit_bytes_per_second")
	rx := metricValueOr(metrics, "node_rdma_port_receive_bytes_per_second")
	imbalance := 0.0
	denom := tx
	if rx > denom {
		denom = rx
	}
	if denom > 0 {
		imbalance = clampRange(abs(tx-rx)/denom, 0, 1)
	}
	score := clamp01(rdmaErr/40.0)*1.0 + clamp01(rdmaCong/120.0)*1.2 + clamp01(pfcPause/600.0)*1.1 + clamp01(ecnMarked/0.03)*0.9 + clamp01(imbalance/0.35)*0.8
	signals := compactPositiveSignals(map[string]float64{
		"rdma_errors_per_second":     rdmaErr,
		"rdma_congestion_per_second": rdmaCong,
		"rdma_pfc_pause_per_second":  pfcPause,
		"rdma_ecn_marked_ratio":      ecnMarked,
		"rdma_comm_imbalance_ratio":  imbalance,
	})
	notes := make([]string, 0, 3)
	if rdmaCong > 0 || pfcPause > 0 || ecnMarked >= 0.01 {
		notes = append(notes, "RDMA fabric congestion signals are active (PFC/ECN/CNP domain).")
	}
	if imbalance >= 0.2 {
		notes = append(notes, "RDMA transmit/receive imbalance suggests collective communication skew.")
	}
	return kernelPathStageDiagnostics{
		Name:     "rdma_fabric",
		Score:    score,
		Severity: kernelStageSeverity(score),
		Signals:  signals,
		Sources: signalSources(signals, map[string]string{
			"rdma_errors_per_second":     "node_rdma_errors_per_second",
			"rdma_congestion_per_second": "node_rdma_congestion_events_per_second",
			"rdma_pfc_pause_per_second":  "node_rdma_pfc_pause_frames_per_second",
			"rdma_ecn_marked_ratio":      "node_rdma_ecn_marked_ratio",
			"rdma_comm_imbalance_ratio":  "node_rdma_port_transmit_bytes_per_second",
		}),
		Notes: notes,
	}
}

func compactPositiveSignals(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		if v > 0 {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func signalSources(signals map[string]float64, signalToMetric map[string]string) map[string]string {
	if len(signals) == 0 {
		return nil
	}
	out := make(map[string]string, len(signals))
	for signal := range signals {
		metric, ok := signalToMetric[signal]
		if !ok || metric == "" {
			continue
		}
		out[signal] = diagnosticMetricSource(metric)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func kernelStageSeverity(score float64) string {
	switch {
	case score >= 2.2:
		return "critical"
	case score >= 1.0:
		return "degraded"
	default:
		return "healthy"
	}
}

func kernelDomainSeverity(score float64) string {
	switch {
	case score >= 3.8:
		return "critical"
	case score >= 1.8:
		return "degraded"
	default:
		return "healthy"
	}
}

func worseSeverity(a, b string) string {
	if kernelSeverityRank(a) >= kernelSeverityRank(b) {
		return a
	}
	return b
}

func kernelSeverityRank(severity string) int {
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

func topKernelStageByAverage(totals map[string]float64, counts map[string]int) string {
	topName := ""
	topAvg := 0.0
	for stage, total := range totals {
		count := counts[stage]
		if count <= 0 {
			continue
		}
		avg := total / float64(count)
		if avg <= 0 {
			continue
		}
		if avg > topAvg || (avg == topAvg && (topName == "" || stage < topName)) {
			topName = stage
			topAvg = avg
		}
	}
	return topName
}

func topBottleneck(counts map[string]int) string {
	topKey := ""
	topCount := 0
	for key, count := range counts {
		if count > topCount || (count == topCount && (topKey == "" || key < topKey)) {
			topKey = key
			topCount = count
		}
	}
	return topKey
}
