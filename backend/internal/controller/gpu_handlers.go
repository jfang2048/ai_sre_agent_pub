package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (c *Controller) registerGPUHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/gpu/nodes", c.withCORS(c.handleGPUNodes))
	mux.HandleFunc("/api/v1/gpu/nodes/", c.withCORS(c.handleGPUNodesByID))
	mux.HandleFunc("/api/v1/gpu/timeline", c.withCORS(c.handleGPUTimeline))
	mux.HandleFunc("/api/v1/gpu/process-timeline", c.withCORS(c.handleGPUProcessTimeline))
	mux.HandleFunc("/api/v1/gpu/events", c.withCORS(c.handleGPUEvents))
	mux.HandleFunc("/api/v1/gpu/processes", c.withCORS(c.handleGPUProcesses))
	mux.HandleFunc("/api/v1/gpu/correlation", c.withCORS(c.handleGPUCorrelation))

	// Kubernetes-friendly summary for external controllers / schedulers.
	mux.HandleFunc("/api/v1/k8s/gpu/nodes", c.withCORS(c.handleK8sGPUNodes))
}

func (c *Controller) handleGPUNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.gpuStore == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"nodes": []interface{}{}})
		return
	}

	nodes := c.gpuStore.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"nodes":     nodes,
		"count":     len(nodes),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleGPUNodesByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.gpuStore == nil {
		http.Error(w, "gpu store disabled", http.StatusNotFound)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/gpu/nodes/")
	if id == "" {
		http.Error(w, "collector id required", http.StatusBadRequest)
		return
	}

	node := c.gpuStore.Node(id)
	if node == nil {
		http.Error(w, "collector not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(node)
}

func (c *Controller) handleK8sGPUNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.gpuStore == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	type k8sGPUDevice struct {
		Index   string  `json:"index"`
		UUID    string  `json:"uuid,omitempty"`
		Name    string  `json:"name,omitempty"`
		MemMiB  float64 `json:"memory_total_mib,omitempty"`
		UsedMiB float64 `json:"memory_used_mib,omitempty"`
		UtilPct float64 `json:"utilization_sm_percent,omitempty"`
	}
	type k8sGPUNode struct {
		Node   string         `json:"node"`
		GPUs   []k8sGPUDevice `json:"gpus"`
		SeenAt time.Time      `json:"seen_at"`
	}

	nodes := c.gpuStore.Snapshot()
	items := make([]k8sGPUNode, 0, len(nodes))
	for _, n := range nodes {
		gpus := make([]k8sGPUDevice, 0, len(n.GPUs))
		for _, dev := range n.GPUs {
			gpus = append(gpus, k8sGPUDevice{
				Index:   dev.GPUIndex,
				UUID:    dev.UUID,
				Name:    dev.Name,
				MemMiB:  dev.MemTotalMiB,
				UsedMiB: dev.MemUsedMiB,
				UtilPct: dev.UtilSMPercent,
			})
		}
		items = append(items, k8sGPUNode{
			Node:   n.Hostname,
			GPUs:   gpus,
			SeenAt: n.LastSeen,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": "sre-agent/v1",
		"kind":       "GPUNodeList",
		"items":      items,
	})
}

func (c *Controller) handleGPUTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.gpuStore == nil {
		http.Error(w, "gpu store disabled", http.StatusNotFound)
		return
	}

	query := r.URL.Query()
	collectorID := strings.TrimSpace(query.Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(query.Get("collector"))
	}
	if collectorID == "" {
		collectorID = c.defaultGPUCollectorID()
	}
	gpuID := strings.TrimSpace(query.Get("gpu_id"))
	if gpuID == "" {
		http.Error(w, "gpu_id is required", http.StatusBadRequest)
		return
	}

	metric := strings.TrimSpace(query.Get("metric"))
	if metric == "" {
		metric = "node_gpu_utilization_sm_percent"
	}
	window := parseTrendWindow(query.Get("window"))
	limit := parseGPULimit(query.Get("limit"), 240, 2000)
	since := time.Now().Add(-window)
	points := c.gpuStore.DeviceMetricTimeline(collectorID, gpuID, metric, since, limit)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"collector_id": collectorID,
		"gpu_id":       gpuID,
		"metric":       metric,
		"window":       window.String(),
		"count":        len(points),
		"points":       points,
		"timestamp":    time.Now(),
	})
}

func (c *Controller) handleGPUProcessTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.gpuStore == nil {
		http.Error(w, "gpu store disabled", http.StatusNotFound)
		return
	}

	query := r.URL.Query()
	collectorID := strings.TrimSpace(query.Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(query.Get("collector"))
	}
	if collectorID == "" {
		collectorID = c.defaultGPUCollectorID()
	}
	gpuID := strings.TrimSpace(query.Get("gpu_id"))
	if gpuID == "" {
		http.Error(w, "gpu_id is required", http.StatusBadRequest)
		return
	}
	pid := strings.TrimSpace(query.Get("pid"))
	if pid == "" {
		http.Error(w, "pid is required", http.StatusBadRequest)
		return
	}
	metric := strings.TrimSpace(query.Get("metric"))
	if metric == "" {
		metric = "node_gpu_process_sm_util_percent"
	}
	window := parseTrendWindow(query.Get("window"))
	limit := parseGPULimit(query.Get("limit"), 240, 2000)
	since := time.Now().Add(-window)
	points := c.gpuStore.ProcessMetricTimeline(collectorID, gpuID, pid, metric, since, limit)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"collector_id": collectorID,
		"gpu_id":       gpuID,
		"pid":          pid,
		"metric":       metric,
		"window":       window.String(),
		"count":        len(points),
		"points":       points,
		"timestamp":    time.Now(),
	})
}

func (c *Controller) handleGPUEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.gpuStore == nil {
		http.Error(w, "gpu store disabled", http.StatusNotFound)
		return
	}

	query := r.URL.Query()
	collectorID := strings.TrimSpace(query.Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(query.Get("collector"))
	}
	if collectorID == "" {
		collectorID = c.defaultGPUCollectorID()
	}
	gpuID := strings.TrimSpace(query.Get("gpu_id"))
	severity := strings.TrimSpace(query.Get("severity"))
	window := parseTrendWindow(query.Get("window"))
	limit := parseGPULimit(query.Get("limit"), 200, 2000)
	since := time.Now().Add(-window)
	events := c.gpuStore.Events(collectorID, gpuID, since, limit, severity)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"collector_id": collectorID,
		"gpu_id":       gpuID,
		"severity":     severity,
		"window":       window.String(),
		"count":        len(events),
		"events":       events,
		"timestamp":    time.Now(),
	})
}

func (c *Controller) handleGPUProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.gpuStore == nil {
		http.Error(w, "gpu store disabled", http.StatusNotFound)
		return
	}

	query := r.URL.Query()
	collectorID := strings.TrimSpace(query.Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(query.Get("collector"))
	}
	if collectorID == "" {
		collectorID = c.defaultGPUCollectorID()
	}
	gpuID := strings.TrimSpace(query.Get("gpu_id"))
	if gpuID == "" {
		http.Error(w, "gpu_id is required", http.StatusBadRequest)
		return
	}
	sortBy := strings.TrimSpace(query.Get("sort_by"))
	limit := parseGPULimit(query.Get("limit"), 20, 200)
	processes := c.gpuStore.RankedProcesses(collectorID, gpuID, sortBy, limit)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"collector_id": collectorID,
		"gpu_id":       gpuID,
		"sort_by":      sortBy,
		"count":        len(processes),
		"processes":    processes,
		"timestamp":    time.Now(),
	})
}

func (c *Controller) handleGPUCorrelation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.gpuStore == nil {
		http.Error(w, "gpu store disabled", http.StatusNotFound)
		return
	}

	query := r.URL.Query()
	collectorID := strings.TrimSpace(query.Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(query.Get("collector"))
	}
	if collectorID == "" {
		collectorID = c.defaultGPUCollectorID()
	}
	if collectorID == "" {
		http.Error(w, "collector_id is required", http.StatusBadRequest)
		return
	}

	gpuNode := c.gpuStore.Node(collectorID)
	if gpuNode == nil {
		http.Error(w, "collector not found", http.StatusNotFound)
		return
	}
	var ingestNodeMetrics map[string]float64
	if c.ingestStore != nil {
		if node := c.ingestStore.Node(collectorID); node != nil {
			ingestNodeMetrics = node.Metrics
		}
	}

	var (
		gpuUtilSum     float64
		gpuMemUsedSum  float64
		gpuMemTotalSum float64
		pcieUtilSum    float64
		pcieUtilCount  float64
		throttleCount  int
		xidTotal       float64
		uvmTotal       float64
		resetTotal     float64
		hotspotPeak    float64
		contextTotal   float64
	)
	for _, dev := range gpuNode.GPUs {
		gpuUtilSum += dev.UtilSMPercent
		gpuMemUsedSum += dev.MemUsedMiB
		gpuMemTotalSum += dev.MemTotalMiB
		if dev.PCIELinkUtilPercent > 0 {
			pcieUtilSum += dev.PCIELinkUtilPercent
			pcieUtilCount++
		}
		if dev.ThrottleActive > 0 {
			throttleCount++
		}
		xidTotal += dev.XidErrorsTotal
		uvmTotal += dev.UVMFaultsTotal
		resetTotal += dev.ResetEventsTotal
		contextTotal += dev.ContextCount
		if dev.KernelHotspotPeakSMUtilPercent > hotspotPeak {
			hotspotPeak = dev.KernelHotspotPeakSMUtilPercent
		}
	}

	gpuCount := maxFloat(float64(len(gpuNode.GPUs)), 1)
	gpuUtilAvg := gpuUtilSum / gpuCount
	memPressure := 0.0
	if gpuMemTotalSum > 0 {
		memPressure = (gpuMemUsedSum / gpuMemTotalSum) * 100
	}
	pcieUtilAvg := 0.0
	if pcieUtilCount > 0 {
		pcieUtilAvg = pcieUtilSum / pcieUtilCount
	}

	cpuIOWait := metricValueOr(ingestNodeMetrics, "node_cpu_iowait_percent")
	diskUtil := metricValueOr(ingestNodeMetrics, "node_disk_utilization_peak_percent")
	netUtil := metricValueOr(ingestNodeMetrics, "node_network_utilization_peak_percent")
	tcpRetrans := metricValueOr(ingestNodeMetrics, "node_tcp_retransmit_ratio") * 100.0
	diskLatencyMs := metricValueOr(ingestNodeMetrics, "node_disk_request_latency_p99_seconds") * 1000.0

	starvationScore := clamp01((45.0-gpuUtilAvg)/45.0)*0.45 +
		clamp01(diskUtil/100.0)*0.25 +
		clamp01(netUtil/100.0)*0.20 +
		clamp01(cpuIOWait/100.0)*0.10
	commScore := clamp01(netUtil/100.0)*0.35 +
		clamp01(pcieUtilAvg/100.0)*0.30 +
		clamp01(tcpRetrans/10.0)*0.20 +
		clamp01(diskLatencyMs/100.0)*0.15
	reliabilityScore := clamp01((xidTotal+resetTotal*2.0)/20.0)*0.70 +
		clamp01(float64(throttleCount)/gpuCount)*0.20 +
		clamp01(uvmTotal/20.0)*0.10

	risks := make([]string, 0, 4)
	if starvationScore >= 0.55 {
		risks = append(risks, "GPU starvation risk: low/medium GPU utilization while IO/network pressure is elevated.")
	}
	if commScore >= 0.55 {
		risks = append(risks, "Communication pipeline risk: NIC/PCIe pressure and retransmits suggest feed/collective bottlenecks.")
	}
	if reliabilityScore >= 0.45 {
		risks = append(risks, "Reliability risk: recent Xid/reset/throttle/uvm activity indicates unstable GPU runtime conditions.")
	}
	if len(risks) == 0 {
		risks = append(risks, "No major GPU-correlation risk above configured thresholds in the selected window.")
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"collector_id": collectorID,
		"hostname":     gpuNode.Hostname,
		"gpu": map[string]any{
			"gpu_count":                   len(gpuNode.GPUs),
			"avg_util_sm_percent":         gpuUtilAvg,
			"memory_pressure_percent":     memPressure,
			"avg_pcie_link_util_percent":  pcieUtilAvg,
			"kernel_hotspot_peak_sm_util": hotspotPeak,
			"context_count_total":         contextTotal,
			"throttle_active_devices":     throttleCount,
			"xid_errors_total":            xidTotal,
			"uvm_faults_total":            uvmTotal,
			"reset_events_total":          resetTotal,
		},
		"host_pressure": map[string]any{
			"cpu_iowait_percent":               cpuIOWait,
			"disk_utilization_peak_percent":    diskUtil,
			"disk_latency_p99_ms":              diskLatencyMs,
			"network_utilization_peak_percent": netUtil,
			"tcp_retransmit_ratio_percent":     tcpRetrans,
		},
		"scores": map[string]any{
			"starvation_risk":      starvationScore,
			"communication_risk":   commScore,
			"reliability_risk":     reliabilityScore,
			"overall_risk_percent": (starvationScore*0.45 + commScore*0.30 + reliabilityScore*0.25) * 100.0,
		},
		"risks":     risks,
		"timestamp": time.Now(),
	})
}

func parseGPULimit(raw string, fallback, max int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}

func (c *Controller) defaultGPUCollectorID() string {
	if c.gpuStore == nil {
		return ""
	}
	nodes := c.gpuStore.Snapshot()
	if len(nodes) == 0 {
		return ""
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].LastSeen.After(nodes[j].LastSeen)
	})
	return nodes[0].CollectorID
}
