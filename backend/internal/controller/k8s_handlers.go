package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/k8sview"
)

const (
	defaultK8sTopLimit = 20
	maxK8sTopLimit     = 200
)

type k8sTopWorkload struct {
	Cluster           string    `json:"cluster"`
	Namespace         string    `json:"namespace"`
	Kind              string    `json:"kind"`
	Name              string    `json:"name"`
	Service           string    `json:"service"`
	PodsTotal         int       `json:"pods_total"`
	PodsRunning       int       `json:"pods_running"`
	PodsPending       int       `json:"pods_pending"`
	PodsFailed        int       `json:"pods_failed"`
	ContainerRestarts int64     `json:"container_restarts"`
	Score             float64   `json:"score"`
	Metric            string    `json:"metric"`
	GPURequests       float64   `json:"gpu_requests,omitempty"`
	GPULimits         float64   `json:"gpu_limits,omitempty"`
	NetworkPressure   float64   `json:"network_pressure,omitempty"`
	StoragePressure   float64   `json:"storage_pressure,omitempty"`
	Nodes             []string  `json:"nodes,omitempty"`
	LastObservedPodAt time.Time `json:"last_observed_pod_at,omitempty"`
}

type k8sTopNode struct {
	Cluster            string  `json:"cluster"`
	Name               string  `json:"name"`
	Ready              bool    `json:"ready"`
	Schedulable        bool    `json:"schedulable"`
	Zone               string  `json:"zone,omitempty"`
	CPUUsagePercent    float64 `json:"cpu_usage_percent,omitempty"`
	MemoryUsagePercent float64 `json:"memory_usage_percent,omitempty"`
	GPUUtilPercent     float64 `json:"gpu_util_percent,omitempty"`
	NetworkPressure    float64 `json:"network_pressure,omitempty"`
	StoragePressure    float64 `json:"storage_pressure,omitempty"`
	LogErrors          uint64  `json:"log_errors,omitempty"`
	LogWarnings        uint64  `json:"log_warnings,omitempty"`
	Score              float64 `json:"score"`
	Metric             string  `json:"metric"`
}

func (c *Controller) registerK8sHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/k8s/status", c.withCORS(c.handleK8sStatus))
	mux.HandleFunc("/api/v1/k8s/clusters", c.withCORS(c.handleK8sClusters))
	mux.HandleFunc("/api/v1/k8s/clusters/", c.withCORS(c.handleK8sClusterByName))
	mux.HandleFunc("/api/v1/k8s/topology", c.withCORS(c.handleK8sTopology))
	mux.HandleFunc("/api/v1/k8s/workloads/top", c.withCORS(c.handleK8sTopWorkloads))
	mux.HandleFunc("/api/v1/k8s/nodes/top", c.withCORS(c.handleK8sTopNodes))
}

func (c *Controller) handleK8sStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.k8sManager == nil {
		http.Error(w, "kubernetes integration disabled", http.StatusServiceUnavailable)
		return
	}

	status := c.k8sManager.Status()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (c *Controller) handleK8sClusters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.k8sManager == nil {
		http.Error(w, "kubernetes integration disabled", http.StatusServiceUnavailable)
		return
	}

	clusters := c.k8sManager.ClusterSummaries()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"clusters":     clusters,
		"count":        len(clusters),
		"generated_at": time.Now(),
	})
}

func (c *Controller) handleK8sClusterByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.k8sManager == nil {
		http.Error(w, "kubernetes integration disabled", http.StatusServiceUnavailable)
		return
	}

	name := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/v1/k8s/clusters/"))
	if name == "" {
		http.Error(w, "cluster name required", http.StatusBadRequest)
		return
	}
	snapshot, ok := c.k8sManager.ClusterSnapshot(name)
	if !ok {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (c *Controller) handleK8sTopology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.k8sManager == nil {
		http.Error(w, "kubernetes integration disabled", http.StatusServiceUnavailable)
		return
	}

	topology := c.k8sManager.ServiceMap()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(topology)
}

func (c *Controller) handleK8sTopWorkloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.k8sManager == nil {
		http.Error(w, "kubernetes integration disabled", http.StatusServiceUnavailable)
		return
	}

	metric := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("metric")))
	if metric == "" {
		metric = "pressure"
	}
	limit := parseK8sTopLimit(r.URL.Query().Get("limit"))
	clusterFilter := strings.TrimSpace(r.URL.Query().Get("cluster"))

	snapshots := c.k8sManager.Snapshots()
	out := make([]k8sTopWorkload, 0, 128)
	for _, snapshot := range snapshots {
		if clusterFilter != "" && snapshot.Name != clusterFilter {
			continue
		}
		for _, workload := range snapshot.Workloads {
			score := workloadScore(workload, metric)
			if score <= 0 {
				continue
			}
			out = append(out, k8sTopWorkload{
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
				Score:             score,
				Metric:            metric,
				GPURequests:       workload.GPURequests,
				GPULimits:         workload.GPULimits,
				NetworkPressure:   workload.AvgNodeNetwork,
				StoragePressure:   workload.AvgNodeStorage,
				Nodes:             append([]string(nil), workload.Nodes...),
				LastObservedPodAt: workload.LastObservedPodAt,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Cluster != out[j].Cluster {
			return out[i].Cluster < out[j].Cluster
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Service < out[j].Service
	})
	if len(out) > limit {
		out = out[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"metric":       metric,
		"cluster":      clusterFilter,
		"limit":        limit,
		"generated_at": time.Now(),
		"count":        len(out),
		"workloads":    out,
	})
}

func (c *Controller) handleK8sTopNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.k8sManager == nil {
		http.Error(w, "kubernetes integration disabled", http.StatusServiceUnavailable)
		return
	}

	metric := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("metric")))
	if metric == "" {
		metric = "pressure"
	}
	limit := parseK8sTopLimit(r.URL.Query().Get("limit"))
	clusterFilter := strings.TrimSpace(r.URL.Query().Get("cluster"))

	snapshots := c.k8sManager.Snapshots()
	out := make([]k8sTopNode, 0, 128)
	for _, snapshot := range snapshots {
		if clusterFilter != "" && snapshot.Name != clusterFilter {
			continue
		}
		for _, node := range snapshot.Nodes {
			score := nodeScore(node, metric)
			if score <= 0 {
				continue
			}
			out = append(out, k8sTopNode{
				Cluster:            node.Cluster,
				Name:               node.Name,
				Ready:              node.Ready,
				Schedulable:        node.Schedulable,
				Zone:               node.Zone,
				CPUUsagePercent:    node.Observed.CPUUsagePercent,
				MemoryUsagePercent: node.Observed.MemoryUsagePercent,
				GPUUtilPercent:     node.Observed.GPUUtilPercent,
				NetworkPressure:    node.Observed.NetworkPressure,
				StoragePressure:    node.Observed.StoragePressure,
				LogErrors:          node.Observed.LogErrors,
				LogWarnings:        node.Observed.LogWarnings,
				Score:              score,
				Metric:             metric,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Cluster != out[j].Cluster {
			return out[i].Cluster < out[j].Cluster
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > limit {
		out = out[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"metric":       metric,
		"cluster":      clusterFilter,
		"limit":        limit,
		"generated_at": time.Now(),
		"count":        len(out),
		"nodes":        out,
	})
}

func parseK8sTopLimit(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultK8sTopLimit
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return defaultK8sTopLimit
	}
	if n > maxK8sTopLimit {
		return maxK8sTopLimit
	}
	return n
}

func workloadScore(workload k8sview.WorkloadSummary, metric string) float64 {
	switch metric {
	case "cpu":
		return workload.AvgNodeCPUPercent
	case "memory":
		return workload.AvgNodeMemoryPct
	case "gpu":
		return workload.AvgNodeGPUPercent
	case "network":
		return workload.AvgNodeNetwork
	case "storage":
		return workload.AvgNodeStorage
	case "logs":
		return float64(workload.NodeLogErrors*2 + workload.NodeLogWarnings)
	case "pending":
		return float64(workload.PodsPending)
	case "failed":
		return float64(workload.PodsFailed)
	case "restarts":
		return float64(workload.ContainerRestarts)
	case "pressure":
		return maxOf(
			workload.AvgNodeCPUPercent,
			workload.AvgNodeMemoryPct,
			workload.AvgNodeGPUPercent,
			workload.AvgNodeNetwork,
			workload.AvgNodeStorage,
			float64(workload.NodeLogErrors)*5,
			float64(workload.NodeLogWarnings),
		)
	default:
		return maxOf(
			workload.AvgNodeCPUPercent,
			workload.AvgNodeMemoryPct,
			workload.AvgNodeGPUPercent,
			workload.AvgNodeNetwork,
			workload.AvgNodeStorage,
		)
	}
}

func nodeScore(node k8sview.NodeSummary, metric string) float64 {
	switch metric {
	case "cpu":
		return node.Observed.CPUUsagePercent
	case "memory":
		return node.Observed.MemoryUsagePercent
	case "gpu":
		return node.Observed.GPUUtilPercent
	case "network":
		return node.Observed.NetworkPressure
	case "storage":
		return node.Observed.StoragePressure
	case "logs":
		return float64(node.Observed.LogErrors*2 + node.Observed.LogWarnings)
	case "pressure":
		return maxOf(
			node.Observed.CPUUsagePercent,
			node.Observed.MemoryUsagePercent,
			node.Observed.GPUUtilPercent,
			node.Observed.NetworkPressure,
			node.Observed.StoragePressure,
			float64(node.Observed.LogErrors)*5,
			float64(node.Observed.LogWarnings),
		)
	default:
		return maxOf(
			node.Observed.CPUUsagePercent,
			node.Observed.MemoryUsagePercent,
			node.Observed.GPUUtilPercent,
			node.Observed.NetworkPressure,
			node.Observed.StoragePressure,
		)
	}
}

func maxOf(values ...float64) float64 {
	if len(values) == 0 {
		return 0
	}
	best := values[0]
	for i := 1; i < len(values); i++ {
		if values[i] > best {
			best = values[i]
		}
	}
	return best
}
