package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (c *Controller) registerGPUHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/gpu/nodes", c.withCORS(c.handleGPUNodes))
	mux.HandleFunc("/api/v1/gpu/nodes/", c.withCORS(c.handleGPUNodesByID))

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
