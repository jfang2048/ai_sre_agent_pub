package controller

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

const (
	dataPathTopProcessesDefault = 5
	dataPathAnomalyWindow       = 2 * time.Hour
	dataPathAnomalySamples      = 240
	dataPathAnomalyZThreshold   = 2.8
	dataPathAnomalyMinPoints    = 8
)

type dataPathDiagnosticsResponse struct {
	CollectorID string                      `json:"collector_id,omitempty"`
	GeneratedAt time.Time                   `json:"generated_at"`
	Summary     dataPathDiagnosticsSummary  `json:"summary"`
	Network     dataPathResourceDiagnostics `json:"network"`
	Storage     dataPathResourceDiagnostics `json:"storage"`
	ProbeCore   dataPathResourceDiagnostics `json:"probe_core"`
	DataPaths   []dataPathNodeModel         `json:"data_paths"`
}

type dataPathDiagnosticsSummary struct {
	NodeCount                   int `json:"node_count"`
	NetworkCritical             int `json:"network_critical"`
	NetworkDegraded             int `json:"network_degraded"`
	StorageCritical             int `json:"storage_critical"`
	StorageDegraded             int `json:"storage_degraded"`
	ProbeCoreCritical           int `json:"probe_core_critical"`
	ProbeCoreDegraded           int `json:"probe_core_degraded"`
	ProbeCoreFallbackNodes      int `json:"probe_core_fallback_nodes"`
	ProbeCoreInvalidConfigNodes int `json:"probe_core_invalid_config_nodes"`
	RuntimeNamespaceNodes       int `json:"runtime_namespace_nodes"`
	RuntimeLimitedNodes         int `json:"runtime_limited_nodes"`
	RuntimeDegradedNodes        int `json:"runtime_degraded_nodes"`
	TotalAnomalies              int `json:"total_anomalies"`
	CriticalDataPath            int `json:"critical_data_paths"`
}

type dataPathResourceDiagnostics struct {
	ClusterHealthScore float64               `json:"cluster_health_score"`
	Rankings           []resourcePressureRow `json:"rankings"`
	Anomalies          []resourceAnomaly     `json:"anomalies"`
	TopProcesses       []ProgramStats        `json:"top_processes,omitempty"`
}

type resourcePressureRow struct {
	CollectorID string             `json:"collector_id"`
	Hostname    string             `json:"hostname"`
	Score       float64            `json:"score"`
	Severity    string             `json:"severity"`
	Signals     map[string]float64 `json:"signals,omitempty"`
	Factors     []string           `json:"factors,omitempty"`
}

type resourceAnomaly struct {
	CollectorID string  `json:"collector_id"`
	Hostname    string  `json:"hostname"`
	Resource    string  `json:"resource"`
	Metric      string  `json:"metric"`
	Value       float64 `json:"value"`
	Baseline    float64 `json:"baseline"`
	ZScore      float64 `json:"z_score"`
	Severity    string  `json:"severity"`
}

type dataPathNodeModel struct {
	CollectorID     string   `json:"collector_id"`
	Hostname        string   `json:"hostname"`
	ComputeScore    float64  `json:"compute_score"`
	NetworkScore    float64  `json:"network_score"`
	StorageScore    float64  `json:"storage_score"`
	OverallScore    float64  `json:"overall_score"`
	Severity        string   `json:"severity"`
	Bottleneck      string   `json:"bottleneck"`
	BottleneckTip   []string `json:"bottleneck_tip,omitempty"`
	RuntimeMode     string   `json:"runtime_mode,omitempty"`
	RuntimeDegraded bool     `json:"runtime_degraded,omitempty"`
	RuntimeReasons  []string `json:"runtime_reasons,omitempty"`
}

func (c *Controller) handleDataPathDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	collectorID := strings.TrimSpace(r.URL.Query().Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(r.URL.Query().Get("collector"))
	}

	resp := c.buildDataPathDiagnostics(collectorID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (c *Controller) buildDataPathDiagnostics(collectorID string) dataPathDiagnosticsResponse {
	resp := dataPathDiagnosticsResponse{
		CollectorID: collectorID,
		GeneratedAt: time.Now(),
		Summary:     dataPathDiagnosticsSummary{},
		Network: dataPathResourceDiagnostics{
			Rankings:  []resourcePressureRow{},
			Anomalies: []resourceAnomaly{},
		},
		Storage: dataPathResourceDiagnostics{
			Rankings:  []resourcePressureRow{},
			Anomalies: []resourceAnomaly{},
		},
		ProbeCore: dataPathResourceDiagnostics{
			Rankings:  []resourcePressureRow{},
			Anomalies: []resourceAnomaly{},
		},
		DataPaths: []dataPathNodeModel{},
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

	networkScoreTotal := 0.0
	storageScoreTotal := 0.0
	probeCoreScoreTotal := 0.0
	probeCoreConfiguredNodes := 0

	networkMetricNames := []string{
		"node_network_utilization_peak_percent",
		"node_network_capacity_utilization_percent",
		"node_tcp_retransmits_per_second",
		"node_tcp_retransmit_ratio",
		"node_softnet_dropped_per_second",
		"node_network_total_drop_per_second",
		"node_network_total_errs_per_second",
		"node_network_interface_tx_queue_fill_percent",
		"node_rdma_errors_per_second",
		"node_rdma_congestion_events_per_second",
		"node_rdma_port_errors_per_second",
		"node_rdma_port_congestion_events_per_second",
		"node_rdma_pfc_pause_frames_per_second",
		"node_rdma_ecn_marked_ratio",
	}
	storageMetricNames := []string{
		"node_disk_utilization_peak_percent",
		"node_disk_queue_depth_total",
		"node_disk_request_latency_p99_seconds",
		"node_pressure_io_full_avg10",
		"node_filesystem_space_pressure_percent",
		"node_nvme_utilization_peak_percent",
		"node_nvme_avg_request_latency_seconds",
		"node_storage_metadata_latency_p99_seconds",
		"node_storage_small_io_ratio",
		"node_object_storage_get_latency_p99_seconds",
		"node_object_storage_put_latency_p99_seconds",
		"node_checkpoint_write_latency_p99_seconds",
		"node_dataloader_prefetch_stall_ratio",
		"node_cache_hit_ratio",
	}
	probeCoreMetricNames := []string{
		"collector_probe_core_last_frame_age_seconds",
		"collector_probe_core_decode_errors_total",
		"collector_probe_core_crc_failures_total",
		"collector_probe_core_restarts_total",
	}

	for _, node := range filtered {
		hostname := strings.TrimSpace(node.Hostname)
		if hostname == "" {
			hostname = node.CollectorID
		}

		networkScore, networkSignals, networkFactors := networkPressureScore(node.Metrics)
		storageScore, storageSignals, storageFactors := storagePressureScore(node.Metrics)
		probeCoreScore, probeCoreSignals, probeCoreFactors, probeCoreConfigured := probeCorePressureScore(node)
		computeScore := computePressureScore(node.Metrics)
		overallScore := computeScore + networkScore + storageScore

		networkSeverity := pressureSeverity(networkScore)
		storageSeverity := pressureSeverity(storageScore)
		probeCoreSeverity := "not_configured"
		if probeCoreConfigured {
			probeCoreSeverity = pressureSeverity(probeCoreScore)
		}
		dataPathSeverity := pressureSeverity(overallScore / 2.0)

		if networkSeverity == "critical" {
			resp.Summary.NetworkCritical++
		} else if networkSeverity == "degraded" {
			resp.Summary.NetworkDegraded++
		}
		if storageSeverity == "critical" {
			resp.Summary.StorageCritical++
		} else if storageSeverity == "degraded" {
			resp.Summary.StorageDegraded++
		}
		if probeCoreSeverity == "critical" {
			resp.Summary.ProbeCoreCritical++
		} else if probeCoreSeverity == "degraded" {
			resp.Summary.ProbeCoreDegraded++
		}
		if probeCoreConfigured && !strings.EqualFold(strings.TrimSpace(node.ProbeSource), "probe_core") {
			resp.Summary.ProbeCoreFallbackNodes++
		}
		if probeCoreConfigured && probeCoreSignals["selection_valid"] < 0.5 {
			resp.Summary.ProbeCoreInvalidConfigNodes++
		}
		switch strings.ToLower(strings.TrimSpace(node.RuntimeMode)) {
		case "namespace":
			resp.Summary.RuntimeNamespaceNodes++
		case "limited":
			resp.Summary.RuntimeLimitedNodes++
		}
		if node.RuntimeModeDegraded {
			resp.Summary.RuntimeDegradedNodes++
		}
		if dataPathSeverity == "critical" {
			resp.Summary.CriticalDataPath++
		}

		resp.Network.Rankings = append(resp.Network.Rankings, resourcePressureRow{
			CollectorID: node.CollectorID,
			Hostname:    hostname,
			Score:       networkScore,
			Severity:    networkSeverity,
			Signals:     networkSignals,
			Factors:     networkFactors,
		})
		resp.Storage.Rankings = append(resp.Storage.Rankings, resourcePressureRow{
			CollectorID: node.CollectorID,
			Hostname:    hostname,
			Score:       storageScore,
			Severity:    storageSeverity,
			Signals:     storageSignals,
			Factors:     storageFactors,
		})
		resp.ProbeCore.Rankings = append(resp.ProbeCore.Rankings, resourcePressureRow{
			CollectorID: node.CollectorID,
			Hostname:    hostname,
			Score:       probeCoreScore,
			Severity:    probeCoreSeverity,
			Signals:     probeCoreSignals,
			Factors:     probeCoreFactors,
		})

		bottleneck := "compute"
		bottleneckTips := []string{"Inspect CPU/GPU saturation and scheduler pressure."}
		if networkScore >= computeScore && networkScore >= storageScore {
			bottleneck = "network"
			bottleneckTips = networkFactors
		}
		if storageScore >= computeScore && storageScore >= networkScore {
			bottleneck = "storage"
			bottleneckTips = storageFactors
		}
		resp.DataPaths = append(resp.DataPaths, dataPathNodeModel{
			CollectorID:     node.CollectorID,
			Hostname:        hostname,
			ComputeScore:    computeScore,
			NetworkScore:    networkScore,
			StorageScore:    storageScore,
			OverallScore:    overallScore,
			Severity:        dataPathSeverity,
			Bottleneck:      bottleneck,
			BottleneckTip:   bottleneckTips,
			RuntimeMode:     node.RuntimeMode,
			RuntimeDegraded: node.RuntimeModeDegraded,
			RuntimeReasons:  append([]string(nil), node.RuntimeReasons...),
		})

		networkScoreTotal += networkScore
		storageScoreTotal += storageScore
		if probeCoreConfigured {
			probeCoreScoreTotal += probeCoreScore
			probeCoreConfiguredNodes++
		}

		history := c.metricHistorySamples(node.CollectorID, time.Now().Add(-dataPathAnomalyWindow), dataPathAnomalySamples)
		for _, metricName := range networkMetricNames {
			if anomaly, ok := detectResourceAnomaly(history, "network", metricName, node.CollectorID, hostname); ok {
				resp.Network.Anomalies = append(resp.Network.Anomalies, anomaly)
			}
		}
		for _, metricName := range storageMetricNames {
			if anomaly, ok := detectResourceAnomaly(history, "storage", metricName, node.CollectorID, hostname); ok {
				resp.Storage.Anomalies = append(resp.Storage.Anomalies, anomaly)
			}
		}
		for _, metricName := range probeCoreMetricNames {
			if anomaly, ok := detectResourceAnomaly(history, "probe_core", metricName, node.CollectorID, hostname); ok {
				resp.ProbeCore.Anomalies = append(resp.ProbeCore.Anomalies, anomaly)
			}
		}
	}

	sort.Slice(resp.Network.Rankings, func(i, j int) bool {
		if resp.Network.Rankings[i].Score != resp.Network.Rankings[j].Score {
			return resp.Network.Rankings[i].Score > resp.Network.Rankings[j].Score
		}
		return resp.Network.Rankings[i].CollectorID < resp.Network.Rankings[j].CollectorID
	})
	sort.Slice(resp.Storage.Rankings, func(i, j int) bool {
		if resp.Storage.Rankings[i].Score != resp.Storage.Rankings[j].Score {
			return resp.Storage.Rankings[i].Score > resp.Storage.Rankings[j].Score
		}
		return resp.Storage.Rankings[i].CollectorID < resp.Storage.Rankings[j].CollectorID
	})
	sort.Slice(resp.ProbeCore.Rankings, func(i, j int) bool {
		leftConfigured := resp.ProbeCore.Rankings[i].Signals["configured"]
		rightConfigured := resp.ProbeCore.Rankings[j].Signals["configured"]
		if leftConfigured != rightConfigured {
			return leftConfigured > rightConfigured
		}
		if resp.ProbeCore.Rankings[i].Score != resp.ProbeCore.Rankings[j].Score {
			return resp.ProbeCore.Rankings[i].Score > resp.ProbeCore.Rankings[j].Score
		}
		return resp.ProbeCore.Rankings[i].CollectorID < resp.ProbeCore.Rankings[j].CollectorID
	})
	sort.Slice(resp.DataPaths, func(i, j int) bool {
		if resp.DataPaths[i].OverallScore != resp.DataPaths[j].OverallScore {
			return resp.DataPaths[i].OverallScore > resp.DataPaths[j].OverallScore
		}
		return resp.DataPaths[i].CollectorID < resp.DataPaths[j].CollectorID
	})
	sort.Slice(resp.Network.Anomalies, func(i, j int) bool {
		if resp.Network.Anomalies[i].ZScore != resp.Network.Anomalies[j].ZScore {
			return resp.Network.Anomalies[i].ZScore > resp.Network.Anomalies[j].ZScore
		}
		if resp.Network.Anomalies[i].CollectorID != resp.Network.Anomalies[j].CollectorID {
			return resp.Network.Anomalies[i].CollectorID < resp.Network.Anomalies[j].CollectorID
		}
		return resp.Network.Anomalies[i].Metric < resp.Network.Anomalies[j].Metric
	})
	sort.Slice(resp.Storage.Anomalies, func(i, j int) bool {
		if resp.Storage.Anomalies[i].ZScore != resp.Storage.Anomalies[j].ZScore {
			return resp.Storage.Anomalies[i].ZScore > resp.Storage.Anomalies[j].ZScore
		}
		if resp.Storage.Anomalies[i].CollectorID != resp.Storage.Anomalies[j].CollectorID {
			return resp.Storage.Anomalies[i].CollectorID < resp.Storage.Anomalies[j].CollectorID
		}
		return resp.Storage.Anomalies[i].Metric < resp.Storage.Anomalies[j].Metric
	})
	sort.Slice(resp.ProbeCore.Anomalies, func(i, j int) bool {
		if resp.ProbeCore.Anomalies[i].ZScore != resp.ProbeCore.Anomalies[j].ZScore {
			return resp.ProbeCore.Anomalies[i].ZScore > resp.ProbeCore.Anomalies[j].ZScore
		}
		if resp.ProbeCore.Anomalies[i].CollectorID != resp.ProbeCore.Anomalies[j].CollectorID {
			return resp.ProbeCore.Anomalies[i].CollectorID < resp.ProbeCore.Anomalies[j].CollectorID
		}
		return resp.ProbeCore.Anomalies[i].Metric < resp.ProbeCore.Anomalies[j].Metric
	})

	if len(resp.Network.Anomalies) > 20 {
		resp.Network.Anomalies = resp.Network.Anomalies[:20]
	}
	if len(resp.Storage.Anomalies) > 20 {
		resp.Storage.Anomalies = resp.Storage.Anomalies[:20]
	}
	if len(resp.ProbeCore.Anomalies) > 20 {
		resp.ProbeCore.Anomalies = resp.ProbeCore.Anomalies[:20]
	}

	resp.Network.ClusterHealthScore = resourceClusterHealth(networkScoreTotal, len(filtered))
	resp.Storage.ClusterHealthScore = resourceClusterHealth(storageScoreTotal, len(filtered))
	resp.ProbeCore.ClusterHealthScore = resourceClusterHealth(probeCoreScoreTotal, probeCoreConfiguredNodes)
	resp.Summary.TotalAnomalies = len(resp.Network.Anomalies) + len(resp.Storage.Anomalies) + len(resp.ProbeCore.Anomalies)

	programs := c.aggregateTopProgramsFiltered(maxTopProgramsLimit, collectorID)
	if len(programs) > 0 {
		byCategory := categorizeTopPrograms(programs, dataPathTopProcessesDefault)
		resp.Network.TopProcesses = byCategory["network"]
		resp.Storage.TopProcesses = byCategory["disk_io"]
		if len(resp.Storage.TopProcesses) == 0 {
			resp.Storage.TopProcesses = byCategory["disk"]
		}
	}

	return resp
}

func probeCorePressureScore(node *ingest.NodeSnapshot) (float64, map[string]float64, []string, bool) {
	if node == nil {
		return 0, map[string]float64{"configured": 0}, []string{"No probe-core telemetry available."}, false
	}
	metrics := node.Metrics
	configured := false
	if metrics != nil {
		if _, ok := metrics["collector_probe_core_client_available"]; ok {
			configured = true
		}
		if _, ok := metrics["collector_probe_core_active"]; ok {
			configured = true
		}
	}
	if strings.TrimSpace(node.ProbeSource) != "" || len(node.ProbeCoreModules) > 0 {
		configured = true
	}
	if !configured {
		return 0, map[string]float64{"configured": 0}, []string{"Probe-core runtime metrics are not published; collector may be Go-probe only."}, false
	}

	clientAvailable := metrics["collector_probe_core_client_available"]
	active := metrics["collector_probe_core_active"]
	fresh := metrics["collector_probe_core_fresh"]
	selectionValid := 1.0
	if v, ok := metrics["collector_probe_core_collector_selection_valid"]; ok {
		selectionValid = v
	}
	lastFrameAgeSec := metrics["collector_probe_core_last_frame_age_seconds"]
	decodeErrors := metrics["collector_probe_core_decode_errors_total"]
	crcFailures := metrics["collector_probe_core_crc_failures_total"]
	restarts := metrics["collector_probe_core_restarts_total"]

	source := strings.ToLower(strings.TrimSpace(node.ProbeSource))
	if source == "" {
		if active >= 0.5 {
			source = "probe_core"
		} else {
			source = "go"
		}
	}

	requestedModules, activeModules := probeCoreModuleLists(node.ProbeCoreModules)

	score := 0.0
	score += clamp01((1.0 - clamp01(clientAvailable))) * 3.0
	score += clamp01((1.0 - clamp01(active))) * 2.5
	score += clamp01((1.0 - clamp01(fresh))) * 2.0
	score += clamp01(lastFrameAgeSec/30.0) * 1.0
	score += clamp01((decodeErrors+crcFailures)/50.0) * 1.5
	score += clamp01(restarts/8.0) * 1.25
	if selectionValid < 0.5 {
		score += 3.0
	}
	if len(requestedModules) > 0 && len(activeModules) == 0 {
		score += 0.75
	}

	signals := map[string]float64{
		"configured":                   1,
		"client_available":             clientAvailable,
		"active":                       active,
		"fresh":                        fresh,
		"selection_valid":              selectionValid,
		"last_frame_age_seconds":       lastFrameAgeSec,
		"decode_errors_total":          decodeErrors,
		"crc_failures_total":           crcFailures,
		"restarts_total":               restarts,
		"requested_modules_count":      float64(len(requestedModules)),
		"active_modules_count":         float64(len(activeModules)),
		"source_is_probe_core_primary": boolToFloat(source == "probe_core"),
	}

	factors := make([]string, 0, 8)
	if source != "probe_core" {
		factors = append(factors, "Collector is not using probe-core as active source; running on the compatibility fallback path.")
	}
	if clientAvailable < 0.5 {
		factors = append(factors, "Probe-core client is unavailable; startup/runtime failure likely.")
	}
	if fresh < 0.5 && lastFrameAgeSec > 0 {
		factors = append(factors, "Latest probe-core frame is stale; IPC stream may be blocked.")
	}
	if selectionValid < 0.5 {
		factors = append(factors, "Probe-core module selection is invalid; check --collectors config.")
	}
	if decodeErrors+crcFailures > 0 {
		factors = append(factors, "Probe-core frame decode/CRC failures observed.")
	}
	if restarts > 0 {
		factors = append(factors, "Probe-core process restart count is non-zero.")
	}
	if len(requestedModules) > 0 {
		factors = append(factors, "Requested modules: "+strings.Join(requestedModules, ","))
	}
	if len(activeModules) > 0 {
		factors = append(factors, "Active modules: "+strings.Join(activeModules, ","))
	}
	if len(requestedModules) > 0 && !containsString(requestedModules, "network") && !containsString(requestedModules, "rdma") {
		factors = append(factors, "Module selection excludes network/RDMA collectors; fabric visibility is reduced.")
	}
	if len(factors) == 0 {
		factors = append(factors, "Probe-core runtime path is healthy.")
	}

	return score, signals, factors, true
}

func probeCoreModuleLists(modules map[string]*ingest.ProbeCoreModuleSample) ([]string, []string) {
	if len(modules) == 0 {
		return nil, nil
	}
	requested := make([]string, 0, len(modules))
	active := make([]string, 0, len(modules))
	for module, sample := range modules {
		if sample == nil {
			continue
		}
		name := strings.TrimSpace(module)
		if name == "" {
			name = strings.TrimSpace(sample.Module)
		}
		if name == "" {
			continue
		}
		if sample.Requested >= 0.5 {
			requested = append(requested, name)
		}
		if sample.Active >= 0.5 {
			active = append(active, name)
		}
	}
	sort.Strings(requested)
	sort.Strings(active)
	return requested, active
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func networkPressureScore(metrics map[string]float64) (float64, map[string]float64, []string) {
	if len(metrics) == 0 {
		return 0, map[string]float64{}, []string{"No network telemetry available."}
	}
	utilPeak := metricValueOr(metrics, "node_network_utilization_peak_percent", "node_network_capacity_utilization_percent")
	retransRatio := metrics["node_tcp_retransmit_ratio"]
	retransRate := metrics["node_tcp_retransmits_per_second"]
	softnetDropRate := metrics["node_softnet_dropped_per_second"]
	softnetSqueezedRate := metrics["node_softnet_times_squeezed_per_second"]
	rdmaErrorRate := metrics["node_rdma_errors_per_second"]
	rdmaCongRate := metrics["node_rdma_congestion_events_per_second"]
	rdmaPortErrorRate := metrics["node_rdma_port_errors_per_second"]
	rdmaPortCongRate := metrics["node_rdma_port_congestion_events_per_second"]
	rdmaPFCPauseRate := metrics["node_rdma_pfc_pause_frames_per_second"]
	rdmaECNMarkedRatio := metrics["node_rdma_ecn_marked_ratio"]
	totalDropRate := metrics["node_network_total_drop_per_second"]
	totalErrRate := metrics["node_network_total_errs_per_second"]
	txQueueFill := metrics["node_network_interface_tx_queue_fill_percent"]
	irqRate := metrics["node_network_interrupts_per_second"]
	rdmaTxBps := metrics["node_rdma_port_transmit_bytes_per_second"]
	rdmaRxBps := metrics["node_rdma_port_receive_bytes_per_second"]

	rdmaImbalanceRatio := 0.0
	if rdmaTxBps > 0 || rdmaRxBps > 0 {
		denom := math.Max(rdmaTxBps, rdmaRxBps)
		if denom > 0 {
			rdmaImbalanceRatio = math.Abs(rdmaTxBps-rdmaRxBps) / denom
		}
	}

	score := 0.0
	score += clamp01(utilPeak/100.0) * 2.5
	score += clamp01(retransRatio/0.02) * 1.5
	score += clamp01(retransRate/500.0) * 0.75
	score += clamp01(softnetDropRate/200.0) * 1.25
	score += clamp01(softnetSqueezedRate/100.0) * 0.75
	score += clamp01(totalDropRate/250.0) * 1.0
	score += clamp01(totalErrRate/50.0) * 0.75
	score += clamp01(txQueueFill/100.0) * 0.75
	score += clamp01((rdmaErrorRate+rdmaPortErrorRate)/50.0) * 1.5
	score += clamp01((rdmaCongRate+rdmaPortCongRate)/100.0) * 1.0
	score += clamp01(rdmaPFCPauseRate/500.0) * 1.0
	score += clamp01(rdmaECNMarkedRatio/0.02) * 0.75
	score += clamp01(rdmaImbalanceRatio/0.30) * 0.5
	score += clamp01(irqRate/50000.0) * 0.5

	signals := map[string]float64{
		"utilization_peak_percent":      utilPeak,
		"tcp_retransmit_ratio":          retransRatio,
		"tcp_retransmits_per_second":    retransRate,
		"softnet_dropped_per_second":    softnetDropRate,
		"softnet_squeezed_per_second":   softnetSqueezedRate,
		"total_drop_per_second":         totalDropRate,
		"total_errs_per_second":         totalErrRate,
		"tx_queue_fill_percent":         txQueueFill,
		"rdma_errors_per_second":        rdmaErrorRate + rdmaPortErrorRate,
		"rdma_congestion_per_second":    rdmaCongRate + rdmaPortCongRate,
		"rdma_pfc_pause_per_second":     rdmaPFCPauseRate,
		"rdma_ecn_marked_ratio":         rdmaECNMarkedRatio,
		"rdma_comm_imbalance_ratio":     rdmaImbalanceRatio,
		"network_interrupts_per_second": irqRate,
	}

	factors := []string{}
	if utilPeak >= 80 {
		factors = append(factors, "Peak NIC utilization above 80%.")
	}
	if retransRatio >= 0.01 {
		factors = append(factors, "TCP retransmit ratio above 1%.")
	}
	if softnetDropRate >= 20 {
		factors = append(factors, "Softnet drops indicate receive-path pressure.")
	}
	if rdmaErrorRate+rdmaPortErrorRate > 0 {
		factors = append(factors, "RDMA link/device errors are increasing.")
	}
	if rdmaCongRate+rdmaPortCongRate > 0 {
		factors = append(factors, "RDMA congestion counters are increasing.")
	}
	if rdmaPFCPauseRate > 0 {
		factors = append(factors, "PFC pause activity indicates lossless-fabric backpressure.")
	}
	if rdmaECNMarkedRatio >= 0.01 {
		factors = append(factors, "ECN marking ratio suggests active congestion control.")
	}
	if txQueueFill >= 70 {
		factors = append(factors, "NIC TX queue fill is high; likely egress bottleneck or oversubscription.")
	}
	if rdmaImbalanceRatio >= 0.20 {
		factors = append(factors, "RDMA transmit/receive imbalance suggests collective communication skew.")
	}
	if len(factors) == 0 {
		factors = append(factors, "Network telemetry is within expected range.")
	}

	return score, signals, factors
}

func storagePressureScore(metrics map[string]float64) (float64, map[string]float64, []string) {
	if len(metrics) == 0 {
		return 0, map[string]float64{}, []string{"No storage telemetry available."}
	}
	utilPeak := metrics["node_disk_utilization_peak_percent"]
	queueDepth := metrics["node_disk_queue_depth_total"]
	latP99ms := metrics["node_disk_request_latency_p99_seconds"] * 1000.0
	ioPressureFull := metrics["node_pressure_io_full_avg10"]
	spacePressure := metrics["node_filesystem_space_pressure_percent"]
	inodePressure := metrics["node_filesystem_inode_pressure_percent"]
	nvmeUtilPeak := metrics["node_nvme_utilization_peak_percent"]
	nvmeQueueDepth := metrics["node_nvme_queue_depth_total"]
	nvmeLatMs := metrics["node_nvme_avg_request_latency_seconds"] * 1000.0
	metadataOps := metrics["node_storage_metadata_ops_per_second"]
	metadataLatMs := metrics["node_storage_metadata_latency_p99_seconds"] * 1000.0
	smallIORatio := metrics["node_storage_small_io_ratio"]
	objectGetLatMs := metrics["node_object_storage_get_latency_p99_seconds"] * 1000.0
	objectPutLatMs := metrics["node_object_storage_put_latency_p99_seconds"] * 1000.0
	checkpointLatMs := metrics["node_checkpoint_write_latency_p99_seconds"] * 1000.0
	prefetchStallRatio := metrics["node_dataloader_prefetch_stall_ratio"]
	cacheHitRatio := metrics["node_cache_hit_ratio"]

	score := 0.0
	score += clamp01(utilPeak/100.0) * 2.5
	score += clamp01(queueDepth/64.0) * 1.5
	score += clamp01(latP99ms/50.0) * 2.0
	score += clamp01(ioPressureFull/20.0) * 1.5
	score += clamp01(spacePressure/100.0) * 1.0
	score += clamp01(inodePressure/100.0) * 0.5
	score += clamp01(nvmeUtilPeak/100.0) * 1.5
	score += clamp01(nvmeQueueDepth/64.0) * 0.75
	score += clamp01(nvmeLatMs/40.0) * 0.75
	score += clamp01(metadataOps/20000.0) * 0.25
	score += clamp01(metadataLatMs/30.0) * 1.0
	score += clamp01(smallIORatio/0.50) * 0.75
	score += clamp01(objectGetLatMs/80.0) * 0.75
	score += clamp01(objectPutLatMs/120.0) * 0.5
	score += clamp01(checkpointLatMs/200.0) * 1.0
	score += clamp01(prefetchStallRatio/0.30) * 1.0
	if cacheHitRatio > 0 {
		score += clamp01((1.0-cacheHitRatio)/0.40) * 0.75
	}

	signals := map[string]float64{
		"disk_utilization_peak_percent":     utilPeak,
		"disk_queue_depth_total":            queueDepth,
		"disk_latency_p99_ms":               latP99ms,
		"io_pressure_full_avg10":            ioPressureFull,
		"filesystem_space_pressure":         spacePressure,
		"filesystem_space_pressure_percent": spacePressure,
		"filesystem_inode_pressure":         inodePressure,
		"filesystem_inode_pressure_percent": inodePressure,
		"nvme_utilization_peak_percent":     nvmeUtilPeak,
		"nvme_queue_depth_total":            nvmeQueueDepth,
		"nvme_latency_ms":                   nvmeLatMs,
		"nvme_avg_request_latency_ms":       nvmeLatMs,
		"metadata_ops_per_second":           metadataOps,
		"metadata_latency_p99_ms":           metadataLatMs,
		"small_io_ratio":                    smallIORatio,
		"object_get_latency_p99_ms":         objectGetLatMs,
		"object_put_latency_p99_ms":         objectPutLatMs,
		"checkpoint_write_latency_p99_ms":   checkpointLatMs,
		"dataloader_prefetch_stall_ratio":   prefetchStallRatio,
		"cache_hit_ratio":                   cacheHitRatio,
	}

	factors := []string{}
	if utilPeak >= 85 {
		factors = append(factors, "Disk utilization peak above 85%.")
	}
	if latP99ms >= 20 {
		factors = append(factors, "Disk latency p99 is elevated.")
	}
	if ioPressureFull >= 5 {
		factors = append(factors, "Kernel reports sustained full IO pressure.")
	}
	if spacePressure >= 85 {
		factors = append(factors, "Filesystem space pressure above 85%.")
	}
	if nvmeUtilPeak >= 80 {
		factors = append(factors, "NVMe path is close to saturation.")
	}
	if metadataLatMs >= 15 {
		factors = append(factors, "Metadata path latency is elevated; check MDS/object index pressure.")
	}
	if smallIORatio >= 0.35 {
		factors = append(factors, "Small-IO ratio is high; batching/prefetch strategy may be inefficient.")
	}
	if objectGetLatMs >= 50 || objectPutLatMs >= 80 {
		factors = append(factors, "Object storage latency is elevated; validate cache tier and WAN path.")
	}
	if checkpointLatMs >= 120 {
		factors = append(factors, "Checkpoint write latency is elevated; inspect flush bursts and QoS isolation.")
	}
	if prefetchStallRatio >= 0.15 {
		factors = append(factors, "Dataloader prefetch stalls indicate pipeline starvation.")
	}
	if cacheHitRatio > 0 && cacheHitRatio < 0.70 {
		factors = append(factors, "Cache hit ratio is low; hot dataset is missing local cache/NVMe tier.")
	}
	if len(factors) == 0 {
		factors = append(factors, "Storage telemetry is within expected range.")
	}

	return score, signals, factors
}

func computePressureScore(metrics map[string]float64) float64 {
	if len(metrics) == 0 {
		return 0
	}
	cpu := metrics["node_cpu_usage_percent"]
	gpu := metrics["node_gpu_utilization_sm_avg_percent"]
	load1 := metrics["node_load1"]
	memoryUsedPercent := 0.0
	if total := metrics["node_memory_MemTotal_bytes"]; total > 0 {
		memoryUsedPercent = clampRange((metrics["node_memory_Used_bytes"]/total)*100.0, 0, 100)
	}

	score := 0.0
	score += clamp01(cpu/100.0) * 2.0
	score += clamp01(memoryUsedPercent/100.0) * 1.5
	score += clamp01(gpu/100.0) * 1.5
	score += clamp01(load1/32.0) * 1.0
	return score
}

func pressureSeverity(score float64) string {
	switch {
	case score >= 6:
		return "critical"
	case score >= 3:
		return "degraded"
	default:
		return "healthy"
	}
}

func resourceClusterHealth(scoreTotal float64, nodes int) float64 {
	if nodes <= 0 {
		return 100
	}
	avgScore := scoreTotal / float64(nodes)
	return clampRange(100.0-(avgScore*12.5), 0, 100)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampRange(v, minVal, maxVal float64) float64 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func detectResourceAnomaly(samples []ingest.MetricHistorySample, resource, metricName, collectorID, hostname string) (resourceAnomaly, bool) {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if sample.Metrics == nil {
			continue
		}
		if value, ok := sample.Metrics[metricName]; ok {
			values = append(values, value)
		}
	}
	if len(values) < dataPathAnomalyMinPoints {
		return resourceAnomaly{}, false
	}

	latest := values[len(values)-1]
	baselineValues := values[:len(values)-1]
	mean, std := meanStddev(baselineValues)
	if std < 1e-9 {
		if latest <= mean {
			return resourceAnomaly{}, false
		}
		if latest-mean < minAnomalyDelta(metricName) {
			return resourceAnomaly{}, false
		}
		return resourceAnomaly{
			CollectorID: collectorID,
			Hostname:    hostname,
			Resource:    resource,
			Metric:      metricName,
			Value:       latest,
			Baseline:    mean,
			ZScore:      0,
			Severity:    "degraded",
		}, true
	}

	z := (latest - mean) / std
	if z < dataPathAnomalyZThreshold {
		return resourceAnomaly{}, false
	}
	if latest-mean < minAnomalyDelta(metricName) {
		return resourceAnomaly{}, false
	}
	severity := "degraded"
	if z >= 4.0 {
		severity = "critical"
	}
	return resourceAnomaly{
		CollectorID: collectorID,
		Hostname:    hostname,
		Resource:    resource,
		Metric:      metricName,
		Value:       latest,
		Baseline:    mean,
		ZScore:      z,
		Severity:    severity,
	}, true
}

func minAnomalyDelta(metric string) float64 {
	switch metric {
	case "node_tcp_retransmit_ratio":
		return 0.002
	case "node_rdma_ecn_marked_ratio", "node_storage_small_io_ratio", "node_dataloader_prefetch_stall_ratio":
		return 0.01
	case "node_cache_hit_ratio":
		return 0.03
	case "node_disk_request_latency_p99_seconds", "node_nvme_avg_request_latency_seconds":
		return 0.001
	case "node_storage_metadata_latency_p99_seconds", "node_object_storage_get_latency_p99_seconds",
		"node_object_storage_put_latency_p99_seconds", "node_checkpoint_write_latency_p99_seconds":
		return 0.005
	default:
		return 1
	}
}

func meanStddev(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	mean := total / float64(len(values))
	if len(values) == 1 {
		return mean, 0
	}
	variance := 0.0
	for _, v := range values {
		delta := v - mean
		variance += delta * delta
	}
	variance /= float64(len(values))
	if variance < 0 {
		variance = 0
	}
	return mean, math.Sqrt(variance)
}
