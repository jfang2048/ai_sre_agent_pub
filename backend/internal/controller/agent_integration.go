package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/agent"
	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
)

const (
	agentReportsPathPrefix   = "/api/v1/agent/reports/"
	agentActionsPathPrefix   = "/api/v1/agent/actions/"
	agentIncidentsPathPrefix = "/api/v1/agent/incidents/"
)

func (c *Controller) registerAgentHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/agent/query", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentQuery(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/execute", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentExecute(w, r)
	}))
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
	mux.HandleFunc("/api/v1/agent/joint-risk", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentJointRisk(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/potential-risks", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentPotentialRisks(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/rca", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentRCAWorkflow(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/workflow/audit", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentWorkflowAudit(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/workflow/incidents", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentWorkflowIncidents(w, r)
	}))
	mux.HandleFunc("/api/v1/agent/workflow/incidents/", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleAgentWorkflowIncidentByID(w, r)
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
	jointRiskReports := 0
	rcaWorkflowReports := 0
	potentialRiskFindings := 0
	if c.agentWorkflow != nil {
		jointRiskReports = len(c.agentWorkflow.JointRiskReports(1000, ""))
		rcaWorkflowReports = len(c.agentWorkflow.RCAReports(1000, ""))
		potentialRiskFindings = len(c.agentWorkflow.PotentialRiskFindings(1000, ""))
	}
	writeJSON(w, map[string]interface{}{
		"status":                  "active",
		"reports":                 len(c.agentEngine.Reports("")),
		"actions":                 len(c.agentEngine.Actions("")),
		"joint_risk_reports":      jointRiskReports,
		"potential_risk_findings": potentialRiskFindings,
		"rca_workflow_reports":    rcaWorkflowReports,
		"timestamp":               time.Now(),
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
	if !c.requireActiveController(w) {
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
	if !c.requireAgentEngine(w) {
		return
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, agentIncidentsPathPrefix), "/")
	if path == "" {
		http.Error(w, "alert id required", http.StatusBadRequest)
		return
	}

	if strings.HasSuffix(path, "/execute") && strings.Contains(path, "/actions/") {
		if !allowMethod(w, r, http.MethodPost) {
			return
		}
		c.handleAgentIncidentActionExecute(w, r, path)
		return
	}
	if strings.HasSuffix(path, "/rollback") && strings.Contains(path, "/actions/") {
		if !allowMethod(w, r, http.MethodPost) {
			return
		}
		c.handleAgentIncidentActionRollback(w, r, path)
		return
	}
	if strings.HasSuffix(path, "/actions/audit") {
		if !allowMethod(w, r, http.MethodGet) {
			return
		}
		c.handleAgentIncidentActionAudit(w, r, path)
		return
	}

	if !allowMethod(w, r, http.MethodGet) {
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

func (c *Controller) handleAgentIncidentActionExecute(w http.ResponseWriter, r *http.Request, path string) {
	if !c.requireActiveController(w) {
		return
	}
	alertID, actionID, err := parseIncidentActionExecutePath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req, err := parseIncidentActionExecutePayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := c.agentEngine.ExecuteIncidentAction(alertID, actionID, req)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, agent.ErrIncidentNotFound):
			status = http.StatusNotFound
		case errors.Is(err, agent.ErrIncidentActionNotFound):
			status = http.StatusNotFound
		case errors.Is(err, agent.ErrIncidentActionApprovalRequired):
			status = http.StatusPreconditionRequired
		case errors.Is(err, agent.ErrIncidentActionApprovalInvalid):
			status = http.StatusForbidden
		}
		http.Error(w, err.Error(), status)
		return
	}

	writeJSON(w, map[string]interface{}{
		"result":    result,
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentIncidentActionRollback(w http.ResponseWriter, r *http.Request, path string) {
	if !c.requireActiveController(w) {
		return
	}
	alertID, actionID, err := parseIncidentActionRollbackPath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req, err := parseIncidentActionRollbackPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := c.agentEngine.RollbackIncidentAction(alertID, actionID, req)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, agent.ErrIncidentNotFound):
			status = http.StatusNotFound
		case errors.Is(err, agent.ErrIncidentActionNotFound):
			status = http.StatusNotFound
		case errors.Is(err, agent.ErrIncidentActionNotReversible):
			status = http.StatusConflict
		case errors.Is(err, agent.ErrIncidentRollbackNotFound):
			status = http.StatusNotFound
		case errors.Is(err, agent.ErrIncidentActionApprovalRequired):
			status = http.StatusPreconditionRequired
		case errors.Is(err, agent.ErrIncidentActionApprovalInvalid):
			status = http.StatusForbidden
		}
		http.Error(w, err.Error(), status)
		return
	}

	writeJSON(w, map[string]interface{}{
		"result":    result,
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentIncidentActionAudit(w http.ResponseWriter, r *http.Request, path string) {
	alertID := strings.TrimSuffix(path, "/actions/audit")
	alertID = strings.Trim(alertID, "/")
	if alertID == "" || strings.Contains(alertID, "/") {
		http.Error(w, "invalid alert id", http.StatusBadRequest)
		return
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	records := c.agentEngine.IncidentActionAudits(alertID, limit)
	writeJSON(w, map[string]interface{}{
		"alert_id":  alertID,
		"records":   records,
		"count":     len(records),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleAgentJointRisk(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentWorkflow(w) {
		return
	}

	req := parseWorkflowRequest(r, "joint_risk")
	refresh := parseBoolQuery(r, "refresh", true)
	if refresh {
		if _, err := c.agentWorkflow.EvaluateJointRisk(r.Context(), req); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}

	limit := parseLimit(r)
	if limit <= 0 {
		limit = 12
	}
	if limit > 200 {
		limit = 200
	}
	reports := c.agentWorkflow.JointRiskReports(limit, req.CollectorID)
	writeJSON(w, agentcore.JointRiskListResponse{
		Reports:   reports,
		Count:     len(reports),
		Timestamp: time.Now().UTC(),
	})
}

func (c *Controller) handleAgentPotentialRisks(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentWorkflow(w) {
		return
	}

	req := parseWorkflowRequest(r, "joint_risk")
	refresh := parseBoolQuery(r, "refresh", true)
	if refresh {
		refreshReq := req
		if refreshReq.Limit <= 0 {
			refreshReq.Limit = 8
		}
		if err := c.agentWorkflow.RefreshPotentialRiskFindings(r.Context(), refreshReq); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}

	limit := parseLimit(r)
	if limit <= 0 {
		limit = 24
	}
	if limit > 300 {
		limit = 300
	}
	findings := c.agentWorkflow.PotentialRiskFindings(limit, req.CollectorID)
	writeJSON(w, agentcore.PotentialRiskResponse{
		Findings:  findings,
		Count:     len(findings),
		Timestamp: time.Now().UTC(),
	})
}

func (c *Controller) handleAgentRCAWorkflow(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentWorkflow(w) {
		return
	}

	req := parseWorkflowRequest(r, "rca")
	refresh := parseBoolQuery(r, "refresh", true)
	if refresh {
		if _, err := c.agentWorkflow.BuildRCAWorkflow(r.Context(), req); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}

	limit := parseLimit(r)
	if limit <= 0 {
		limit = 12
	}
	if limit > 200 {
		limit = 200
	}
	reports := c.agentWorkflow.RCAReports(limit, req.CollectorID)
	writeJSON(w, agentcore.RCAListResponse{
		Reports:   reports,
		Count:     len(reports),
		Timestamp: time.Now().UTC(),
	})
}

func (c *Controller) handleAgentWorkflowAudit(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentWorkflow(w) {
		return
	}

	limit := parseLimit(r)
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	workflowID := strings.TrimSpace(r.URL.Query().Get("workflow_id"))
	records := c.agentWorkflow.AuditRecords(limit, workflowID)
	writeJSON(w, agentcore.WorkflowAuditResponse{
		Records:   records,
		Count:     len(records),
		Timestamp: time.Now().UTC(),
	})
}

func (c *Controller) handleAgentWorkflowIncidents(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentWorkflow(w) {
		return
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	collectorID := strings.TrimSpace(firstNonEmpty(
		r.URL.Query().Get("collector_id"),
		r.URL.Query().Get("collector"),
		r.URL.Query().Get("node"),
	))
	incidents := c.agentWorkflow.IncidentReports(limit, status, collectorID)
	writeJSON(w, agentcore.AgentIncidentListResponse{
		Incidents: incidents,
		Count:     len(incidents),
		Timestamp: time.Now().UTC(),
	})
}

func (c *Controller) handleAgentWorkflowIncidentByID(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentWorkflow(w) {
		return
	}
	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/agent/workflow/incidents/"), "/")
	if raw == "" || strings.Contains(raw, "/") {
		http.Error(w, "incident id required", http.StatusBadRequest)
		return
	}
	report, ok := c.agentWorkflow.IncidentReport(raw)
	if !ok {
		http.Error(w, "incident not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{
		"incident":  report,
		"timestamp": time.Now().UTC(),
	})
}

func parseWorkflowRequest(r *http.Request, workflowType string) agentcore.WorkflowRequest {
	window := parseDurationQuery(r, "window")
	var dryRunPtr *bool
	if raw := strings.TrimSpace(r.URL.Query().Get("dry_run")); raw != "" {
		parsed := parseBoolString(raw, true)
		dryRunPtr = &parsed
	}
	return agentcore.WorkflowRequest{
		WorkflowType: workflowType,
		CollectorID:  strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("collector_id"), r.URL.Query().Get("collector"), r.URL.Query().Get("node"))),
		Window:       window,
		Limit:        parseLimit(r),
		Trigger:      strings.TrimSpace(r.URL.Query().Get("trigger")),
		DryRun:       dryRunPtr,
	}
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

func parseDurationQuery(r *http.Request, key string) time.Duration {
	if r == nil {
		return 0
	}
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func parseBoolQuery(r *http.Request, key string, fallback bool) bool {
	if r == nil {
		return fallback
	}
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	return parseBoolString(raw, fallback)
}

func parseBoolString(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
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

func parseIncidentActionExecutePath(path string) (string, string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[1] != "actions" || parts[3] != "execute" {
		return "", "", fmt.Errorf("invalid incident action path")
	}
	alertID := strings.TrimSpace(parts[0])
	actionID := strings.TrimSpace(parts[2])
	if alertID == "" {
		return "", "", fmt.Errorf("alert id required")
	}
	if actionID == "" {
		return "", "", fmt.Errorf("action id required")
	}
	return alertID, actionID, nil
}

func parseIncidentActionRollbackPath(path string) (string, string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[1] != "actions" || parts[3] != "rollback" {
		return "", "", fmt.Errorf("invalid incident rollback path")
	}
	alertID := strings.TrimSpace(parts[0])
	actionID := strings.TrimSpace(parts[2])
	if alertID == "" {
		return "", "", fmt.Errorf("alert id required")
	}
	if actionID == "" {
		return "", "", fmt.Errorf("action id required")
	}
	return alertID, actionID, nil
}

func parseIncidentActionExecutePayload(r *http.Request) (agent.IncidentActionExecuteRequest, error) {
	var payload struct {
		DryRun        *bool  `json:"dry_run"`
		ApprovalToken string `json:"approval_token"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		if err == io.EOF {
			return agent.IncidentActionExecuteRequest{}, nil
		}
		return agent.IncidentActionExecuteRequest{}, fmt.Errorf("invalid payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return agent.IncidentActionExecuteRequest{}, fmt.Errorf("invalid payload")
	}
	return agent.IncidentActionExecuteRequest{
		DryRun:        payload.DryRun,
		ApprovalToken: strings.TrimSpace(payload.ApprovalToken),
	}, nil
}

func parseIncidentActionRollbackPayload(r *http.Request) (agent.IncidentActionRollbackRequest, error) {
	var payload struct {
		DryRun        *bool  `json:"dry_run"`
		ApprovalToken string `json:"approval_token"`
		RollbackID    string `json:"rollback_id"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		if err == io.EOF {
			return agent.IncidentActionRollbackRequest{}, nil
		}
		return agent.IncidentActionRollbackRequest{}, fmt.Errorf("invalid payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return agent.IncidentActionRollbackRequest{}, fmt.Errorf("invalid payload")
	}
	return agent.IncidentActionRollbackRequest{
		DryRun:        payload.DryRun,
		ApprovalToken: strings.TrimSpace(payload.ApprovalToken),
		RollbackID:    strings.TrimSpace(payload.RollbackID),
	}, nil
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

func (c *Controller) requireAgentWorkflow(w http.ResponseWriter) bool {
	if c.agentWorkflow != nil {
		return true
	}
	http.Error(w, "agent workflow engine disabled", http.StatusServiceUnavailable)
	return false
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
