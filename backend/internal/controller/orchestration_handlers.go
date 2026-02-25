package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/orchestration"
)

const (
	orchestrationWorkloadsPathPrefix = "/api/v1/orchestration/workloads/"
)

func (c *Controller) registerOrchestrationHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/orchestration/status", c.withCORS(c.handleOrchestrationStatus))
	mux.HandleFunc("/api/v1/orchestration/policy", c.withCORS(c.handleOrchestrationPolicy))
	mux.HandleFunc("/api/v1/orchestration/diagnostics", c.withCORS(c.handleOrchestrationDiagnostics))
	mux.HandleFunc("/api/v1/orchestration/resources", c.withCORS(c.handleOrchestrationResources))
	mux.HandleFunc("/api/v1/orchestration/workloads", c.withCORS(c.handleOrchestrationWorkloads))
	mux.HandleFunc("/api/v1/orchestration/workloads/", c.withCORS(c.handleOrchestrationWorkloadByID))
	mux.HandleFunc("/api/v1/orchestration/routes", c.withCORS(c.handleOrchestrationRoutes))
	mux.HandleFunc("/api/v1/orchestration/reconcile", c.withCORS(c.handleOrchestrationReconcile))
	mux.HandleFunc("/api/v1/orchestration/events", c.withCORS(c.handleOrchestrationEvents))
	c.logger.Info("orchestration API endpoints registered")
}

func (c *Controller) handleOrchestrationDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	diag := c.orchestrationManager.Diagnostics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"diagnostics": diag,
		"timestamp":   time.Now(),
	})
}

func (c *Controller) handleOrchestrationPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	policy := c.orchestrationManager.Policy()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"policy":    policy,
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleOrchestrationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	status := c.orchestrationManager.Status()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    status,
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleOrchestrationResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	resources := c.orchestrationManager.Resources()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"resources": resources,
		"count":     len(resources),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleOrchestrationWorkloads(w http.ResponseWriter, r *http.Request) {
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		state := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("state")))
		workloads := c.orchestrationManager.Workloads()
		if state != "" {
			filtered := make([]orchestration.Workload, 0, len(workloads))
			for _, workload := range workloads {
				if strings.EqualFold(string(workload.State), state) {
					filtered = append(filtered, workload)
				}
			}
			workloads = filtered
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"workloads": workloads,
			"count":     len(workloads),
			"timestamp": time.Now(),
		})

	case http.MethodPost:
		defer r.Body.Close()
		var spec orchestration.WorkloadSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		workload, err := c.orchestrationManager.Submit(spec)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"workload":  workload,
			"timestamp": time.Now(),
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Controller) handleOrchestrationWorkloadByID(w http.ResponseWriter, r *http.Request) {
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, orchestrationWorkloadsPathPrefix)
	path = strings.Trim(path, "/")
	if path == "" {
		http.Error(w, "workload ID required", http.StatusBadRequest)
		return
	}

	if strings.HasSuffix(path, "/complete") {
		id := strings.TrimSuffix(path, "/complete")
		id = strings.Trim(id, "/")
		if id == "" {
			http.Error(w, "workload ID required", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		workload, ok := c.orchestrationManager.Complete(id)
		if !ok {
			http.Error(w, "workload not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"workload":  workload,
			"timestamp": time.Now(),
		})
		return
	}

	id := path
	switch r.Method {
	case http.MethodGet:
		workload, ok := c.orchestrationManager.GetWorkload(id)
		if !ok {
			http.Error(w, "workload not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"workload":  workload,
			"timestamp": time.Now(),
		})

	case http.MethodDelete:
		if ok := c.orchestrationManager.Delete(id); !ok {
			http.Error(w, "workload not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "deleted",
			"workload_id": id,
			"timestamp":   time.Now(),
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Controller) handleOrchestrationRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	service := strings.TrimSpace(r.URL.Query().Get("service"))
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	routes := c.orchestrationManager.Routes(service, model)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"routes":    routes,
		"count":     len(routes),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleOrchestrationReconcile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	c.orchestrationManager.ReconcileNow()
	snapshot := c.orchestrationManager.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"snapshot":  snapshot,
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleOrchestrationEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.orchestrationManager == nil {
		http.Error(w, "orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	events := c.orchestrationManager.Events()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"events":    events,
		"count":     len(events),
		"timestamp": time.Now(),
	})
}
