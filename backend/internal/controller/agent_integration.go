package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/agent"
)

const (
	agentReportsPathPrefix   = "/api/v1/agent/reports/"
	agentActionsPathPrefix   = "/api/v1/agent/actions/"
	agentIncidentsPathPrefix = "/api/v1/agent/incidents/"
)

func (c *Controller) registerAgentHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/agent/reports", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentReports(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/reports/", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/latest") {
			c.handleAgentReportsNodeLatest(w, r)
			return
		}
		c.handleAgentReportsNode(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/reports/latest", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentReportsLatest(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/status", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentStatus(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/actions", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentActions(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/actions/", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentActionUpdate(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/incidents", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentIncidentAssessments(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/incidents/", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentIncidentByID(w, r)
	}))
}

func (c *Controller) handleAgentReports(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}
	limit := parseLimit(r)
	reports := c.agentEngine.Reports("")
	if limit > 0 && len(reports) > limit {
		reports = reports[:limit]
	}
	writeJSON(w, map[string]interface{}{
		"reports":   reports,
		"count":     len(reports),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentReportsNode(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}
	node, err := parseNodePath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reports := c.agentEngine.Reports(node)
	limit := parseLimit(r)
	if limit > 0 && len(reports) > limit {
		reports = reports[:limit]
	}
	writeJSON(w, map[string]interface{}{
		"reports":   reports,
		"count":     len(reports),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}
	writeJSON(w, map[string]interface{}{
		"status":    "active",
		"reports":   len(c.agentEngine.Reports("")),
		"actions":   len(c.agentEngine.Actions("")),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentReportsLatest(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}
	limit := parseLimit(r)
	reports := c.agentEngine.LatestReports(limit)
	writeJSON(w, map[string]interface{}{
		"reports":   reports,
		"count":     len(reports),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentReportsNodeLatest(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}
	node, err := parseNodePath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	report, ok := c.agentEngine.LatestReport(node)
	if !ok {
		http.Error(w, "report not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]interface{}{
		"report":    report,
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentActions(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}
	node := r.URL.Query().Get("node")
	actions := c.agentEngine.Actions(node)
	limit := parseLimit(r)
	if limit > 0 && len(actions) > limit {
		actions = actions[:limit]
	}
	writeJSON(w, map[string]interface{}{
		"actions":   actions,
		"count":     len(actions),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentActionUpdate(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost, http.MethodPatch) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}
	id, err := parseSimplePathSegment(r.URL.Path, agentActionsPathPrefix, "action id required")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	status, note, err := parseAgentActionUpdatePayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	action, ok := c.agentEngine.UpdateAction(id, status, note)
	if !ok {
		http.Error(w, "action not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]interface{}{
		"action":    action,
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentIncidentAssessments(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}
	limit := parseLimit(r)
	assessments := c.agentEngine.IncidentAssessments(limit)
	writeJSON(w, map[string]interface{}{
		"incidents": assessments,
		"count":     len(assessments),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentIncidentByID(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentEngine(w) {
		return
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, agentIncidentsPathPrefix), "/")
	if path == "" {
		http.Error(w, "alert id required", http.StatusBadRequest)
		return
	}

	if strings.HasSuffix(path, "/context") {
		alertID := strings.Trim(strings.TrimSuffix(path, "/context"), "/")
		if alertID == "" || strings.Contains(alertID, "/") {
			http.Error(w, "invalid alert id", http.StatusBadRequest)
			return
		}
		ctx, ok := c.agentEngine.IncidentContext(alertID)
		if !ok {
			http.Error(w, "incident context not found", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]interface{}{
			"context":   ctx,
			"timestamp": time.Now(),
		})
		return
	}
	if strings.Contains(path, "/") {
		http.Error(w, "invalid alert id", http.StatusBadRequest)
		return
	}

	assessment, ok := c.agentEngine.IncidentAssessment(path)
	if !ok {
		http.Error(w, "incident not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]interface{}{
		"incident":  assessment,
		"timestamp": time.Now(),
	})
}

func parseLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0
	}
	return limit
}

func parseNodePath(path string) (string, error) {
	node := strings.TrimPrefix(path, agentReportsPathPrefix)
	node = strings.Trim(node, "/")
	node = strings.TrimSuffix(node, "/latest")
	node = strings.Trim(node, "/")
	if node == "" {
		return "", fmt.Errorf("node required")
	}
	if strings.Contains(node, "/") {
		return "", fmt.Errorf("invalid node path")
	}
	return node, nil
}

func parseSimplePathSegment(path, prefix, missingErr string) (string, error) {
	value := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if value == "" {
		return "", fmt.Errorf("%s", missingErr)
	}
	if strings.Contains(value, "/") {
		return "", fmt.Errorf("invalid path segment")
	}
	return value, nil
}

func parseAgentActionUpdatePayload(r *http.Request) (string, string, error) {
	var payload struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return "", "", fmt.Errorf("invalid payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", "", fmt.Errorf("invalid payload")
	}

	status := agent.NormalizeActionStatus(payload.Status)
	note := strings.TrimSpace(payload.Note)
	if status == "" && note == "" {
		return "", "", fmt.Errorf("status or note required")
	}
	if status != "" && !agent.IsValidActionStatus(status) {
		return "", "", fmt.Errorf("invalid status")
	}
	return status, note, nil
}

func allowMethod(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, method := range allowed {
		if r.Method == method {
			return true
		}
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (c *Controller) requireAgentEngine(w http.ResponseWriter) bool {
	if c.agentEngine != nil {
		return true
	}
	http.Error(w, "agent engine disabled", http.StatusServiceUnavailable)
	return false
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
