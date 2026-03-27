package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inventory"
)

type inventoryHeartbeatRequest struct {
	ProbeID   string            `json:"probe_id"`
	Hostname  string            `json:"hostname,omitempty"`
	Address   string            `json:"address,omitempty"`
	Version   string            `json:"version,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp,omitempty"`
}

func (c *Controller) registerInventoryHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/inventory/status", c.withCORS(c.handleInventoryStatus))
	mux.HandleFunc("/api/v1/inventory/probes", c.withCORS(c.handleInventoryProbes))
	mux.HandleFunc("/api/v1/inventory/probes/", c.withCORS(c.handleInventoryProbeByID))
	mux.HandleFunc("/api/v1/inventory/heartbeat", c.withCORS(c.handleInventoryHeartbeat))
}

func (c *Controller) handleInventoryStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.inventoryManager == nil {
		http.Error(w, "inventory disabled", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c.inventoryManager.Summary())
}

func (c *Controller) handleInventoryProbes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.inventoryManager == nil {
		http.Error(w, "inventory disabled", http.StatusServiceUnavailable)
		return
	}

	items := c.inventoryManager.List()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"probes":       items,
		"count":        len(items),
		"generated_at": time.Now(),
	})
}

func (c *Controller) handleInventoryProbeByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.inventoryManager == nil {
		http.Error(w, "inventory disabled", http.StatusServiceUnavailable)
		return
	}

	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/v1/inventory/probes/"))
	if id == "" {
		http.Error(w, "probe id required", http.StatusBadRequest)
		return
	}
	probe, ok := c.inventoryManager.Get(id)
	if !ok {
		http.Error(w, "probe not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(probe)
}

func (c *Controller) handleInventoryHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.inventoryManager == nil {
		http.Error(w, "inventory disabled", http.StatusServiceUnavailable)
		return
	}

	var req inventoryHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	req.ProbeID = strings.TrimSpace(req.ProbeID)
	if req.ProbeID == "" {
		http.Error(w, "probe_id is required", http.StatusBadRequest)
		return
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}

	ok := c.inventoryManager.UpsertHeartbeat(inventory.Heartbeat{
		ProbeID:   req.ProbeID,
		Hostname:  strings.TrimSpace(req.Hostname),
		Address:   strings.TrimSpace(req.Address),
		Version:   strings.TrimSpace(req.Version),
		Labels:    req.Labels,
		Timestamp: req.Timestamp,
	})
	if !ok {
		http.Error(w, "inventory heartbeat rejected", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "ok",
		"probe_id":    req.ProbeID,
		"received_at": time.Now(),
	})
}
