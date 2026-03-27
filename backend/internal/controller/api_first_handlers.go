package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/agent"
	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidents"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/securityaudit"
)

const (
	controllerAuditRetention = 4000
)

var controllerRunSeq atomic.Uint64

// ControllerAuditRecord is an immutable API-first audit row.
type ControllerAuditRecord struct {
	ID           string            `json:"id"`
	Actor        string            `json:"actor"`
	Action       string            `json:"action"`
	Resource     string            `json:"resource"`
	Status       string            `json:"status"`
	Input        map[string]string `json:"input,omitempty"`
	Output       string            `json:"output,omitempty"`
	Evidence     []string          `json:"evidence,omitempty"`
	OccurredAt   time.Time         `json:"occurred_at"`
	WorkflowID   string            `json:"workflow_id,omitempty"`
	CollectorID  string            `json:"collector_id,omitempty"`
	IncidentID   string            `json:"incident_id,omitempty"`
	ApprovalGate bool              `json:"approval_gate"`
}

// APIRunStatus captures the lifecycle of a controller-managed workflow run.
type APIRunStatus string

const (
	APIRunQueued    APIRunStatus = "queued"
	APIRunRunning   APIRunStatus = "running"
	APIRunCompleted APIRunStatus = "completed"
	APIRunFailed    APIRunStatus = "failed"
	APIRunStopped   APIRunStatus = "stopped"
)

// APIRun is the API-first investigation run record.
type APIRun struct {
	RunID           string       `json:"run_id"`
	WorkflowType    string       `json:"workflow_type"`
	Status          APIRunStatus `json:"status"`
	CollectorID     string       `json:"collector_id,omitempty"`
	Trigger         string       `json:"trigger,omitempty"`
	DryRun          bool         `json:"dry_run"`
	RequestedAt     time.Time    `json:"requested_at"`
	StartedAt       *time.Time   `json:"started_at,omitempty"`
	CompletedAt     *time.Time   `json:"completed_at,omitempty"`
	WorkflowID      string       `json:"workflow_id,omitempty"`
	IncidentID      string       `json:"incident_id,omitempty"`
	RiskLevel       string       `json:"risk_level,omitempty"`
	RiskScore       float64      `json:"risk_score,omitempty"`
	Summary         string       `json:"summary,omitempty"`
	Recommendations []string     `json:"recommendations,omitempty"`
	Evidence        []string     `json:"evidence,omitempty"`
	ErrorMessage    string       `json:"error_message,omitempty"`
}

type apiAgentRunState struct {
	run    APIRun
	cancel context.CancelFunc
}

type apiRunRequest struct {
	WorkflowType string `json:"workflow_type"`
	CollectorID  string `json:"collector_id,omitempty"`
	Window       string `json:"window,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Trigger      string `json:"trigger,omitempty"`
	DryRun       *bool  `json:"dry_run,omitempty"`
}

type apiIncidentIntakeRequest struct {
	ID                 string            `json:"id"`
	Title              string            `json:"title"`
	Service            string            `json:"service"`
	Severity           string            `json:"severity"`
	CollectorID        string            `json:"collector_id,omitempty"`
	StartsAt           time.Time         `json:"starts_at,omitempty"`
	EndsAt             time.Time         `json:"ends_at,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	Annotations        map[string]string `json:"annotations,omitempty"`
	StartInvestigation *bool             `json:"start_investigation,omitempty"`
	WorkflowType       string            `json:"workflow_type,omitempty"`
	Window             string            `json:"window,omitempty"`
	DryRun             *bool             `json:"dry_run,omitempty"`
}

type apiActionRequest struct {
	Kind          string `json:"kind"`
	AlertID       string `json:"alert_id,omitempty"`
	IncidentID    string `json:"incident_id,omitempty"`
	ActionID      string `json:"action_id"`
	DryRun        *bool  `json:"dry_run,omitempty"`
	ApprovalToken string `json:"approval_token,omitempty"`
	RollbackID    string `json:"rollback_id,omitempty"`
}

type apiApproveRequest struct {
	ApprovalToken string `json:"approval_token"`
	Reason        string `json:"reason,omitempty"`
}

func (c *Controller) registerAPIFirstHandlers(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/api/v1/controller/incidents/intake", c.withCORS(c.handleControllerIncidentIntake))
	mux.HandleFunc("/api/v1/controller/telemetry/metrics", c.withCORS(c.handleControllerTelemetryMetrics))
	mux.HandleFunc("/api/v1/controller/telemetry/logs", c.withCORS(c.handleControllerTelemetryLogs))
	mux.HandleFunc("/api/v1/controller/telemetry/security", c.withCORS(c.handleControllerTelemetrySecurity))
	mux.HandleFunc("/api/v1/controller/agent/runs", c.withCORS(c.handleControllerAgentRuns))
	mux.HandleFunc("/api/v1/controller/agent/runs/", c.withCORS(c.handleControllerAgentRunByID))
	mux.HandleFunc("/api/v1/controller/actions/dry-run", c.withCORS(c.handleControllerActionDryRun))
	mux.HandleFunc("/api/v1/controller/actions/approve", c.withCORS(c.handleControllerActionApprove))
	mux.HandleFunc("/api/v1/controller/actions/execute", c.withCORS(c.handleControllerActionExecute))
	mux.HandleFunc("/api/v1/controller/actions/rollback", c.withCORS(c.handleControllerActionRollback))
	mux.HandleFunc("/api/v1/controller/audit", c.withCORS(c.handleControllerAuditLog))
	mux.HandleFunc("/api/v1/controller/config/reload", c.withCORS(c.handleControllerConfigReload))
	mux.HandleFunc("/api/v1/controller/tools", c.withCORS(c.handleControllerToolRegistry))
	mux.HandleFunc("/api/v1/agent/proposed-actions", c.withCORS(c.handleAgentProposedActions))
	mux.HandleFunc("/api/v1/agent/trace/", c.withCORS(c.handleAgentTrace))
	mux.HandleFunc("/api/v1/health", c.withCORS(c.handleHealthJSON))
}

func (c *Controller) handleControllerIncidentIntake(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireActiveController(w) {
		return
	}

	var payload apiIncidentIntakeRequest
	if err := decodeStrictJSON(r, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.ID) == "" {
		payload.ID = fmt.Sprintf("intake-%d", time.Now().UnixNano())
	}
	if payload.StartsAt.IsZero() {
		payload.StartsAt = time.Now().UTC()
	}

	response := map[string]any{
		"intake_id":   payload.ID,
		"timestamp":   time.Now().UTC(),
		"context":     nil,
		"run":         nil,
		"accepted":    true,
		"workflow":    strings.TrimSpace(payload.WorkflowType),
		"collector":   strings.TrimSpace(payload.CollectorID),
		"service":     strings.TrimSpace(payload.Service),
		"alert_title": strings.TrimSpace(payload.Title),
	}

	if c.incidentCoordinator != nil {
		ctxBundle, err := c.incidentCoordinator.HandleExternalAlert(r.Context(), incidents.InputAlert{
			ID:          payload.ID,
			Title:       strings.TrimSpace(payload.Title),
			Service:     strings.TrimSpace(payload.Service),
			Severity:    strings.TrimSpace(payload.Severity),
			StartsAt:    payload.StartsAt,
			EndsAt:      payload.EndsAt,
			Labels:      payload.Labels,
			Annotations: payload.Annotations,
		})
		if err == nil {
			response["context"] = ctxBundle
		}
	}

	startInvestigation := true
	if payload.StartInvestigation != nil {
		startInvestigation = *payload.StartInvestigation
	}
	if startInvestigation && c.agentWorkflow != nil && strings.TrimSpace(payload.CollectorID) != "" {
		workflowType := strings.TrimSpace(payload.WorkflowType)
		if workflowType == "" {
			workflowType = "joint_risk"
		}
		runReq := apiRunRequest{
			WorkflowType: workflowType,
			CollectorID:  payload.CollectorID,
			Window:       payload.Window,
			Limit:        16,
			Trigger:      "incident_intake",
			DryRun:       payload.DryRun,
		}
		run, err := c.startAPIRun(r.Context(), runReq, r)
		if err == nil {
			response["run"] = run
		}
	}

	c.appendControllerAudit(r, ControllerAuditRecord{
		Action:      "incident_intake",
		Resource:    payload.ID,
		Status:      "success",
		CollectorID: strings.TrimSpace(payload.CollectorID),
		Input: map[string]string{
			"service":  strings.TrimSpace(payload.Service),
			"severity": strings.TrimSpace(payload.Severity),
		},
		Output: "incident accepted",
	})

	writeJSON(w, response)
}

func (c *Controller) handleControllerTelemetryMetrics(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	collectorID := strings.TrimSpace(firstNonEmpty(
		r.URL.Query().Get("collector_id"),
		r.URL.Query().Get("collector"),
		r.URL.Query().Get("node"),
	))
	window := parseDurationQuery(r, "window")
	if window <= 0 {
		window = 45 * time.Minute
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 240
	}
	if limit > 5000 {
		limit = 5000
	}

	if c.ingestStore == nil {
		http.Error(w, "ingest store unavailable", http.StatusServiceUnavailable)
		return
	}

	if collectorID == "" {
		nodes := c.ingestStore.Snapshot()
		writeJSON(w, map[string]any{
			"nodes":        nodes,
			"count":        len(nodes),
			"window":       window.String(),
			"timestamp":    time.Now().UTC(),
			"collector_id": "",
		})
		return
	}

	node := c.ingestStore.Node(collectorID)
	if node == nil {
		http.Error(w, "collector not found", http.StatusNotFound)
		return
	}
	history := c.metricHistorySamples(collectorID, time.Now().Add(-window), limit)
	writeJSON(w, map[string]any{
		"collector_id": collectorID,
		"node":         node,
		"history":      history,
		"count":        len(history),
		"window":       window.String(),
		"timestamp":    time.Now().UTC(),
	})
}

func (c *Controller) handleControllerTelemetryLogs(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	collectorID := strings.TrimSpace(firstNonEmpty(
		r.URL.Query().Get("collector_id"),
		r.URL.Query().Get("collector"),
		r.URL.Query().Get("node"),
	))
	queryText := strings.TrimSpace(firstNonEmpty(
		r.URL.Query().Get("q"),
		r.URL.Query().Get("query"),
		r.URL.Query().Get("text"),
	))
	window := parseDurationQuery(r, "window")
	if window <= 0 {
		window = 45 * time.Minute
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 120
	}
	if limit > 1000 {
		limit = 1000
	}

	if c.logIndex != nil {
		result := c.logIndex.Search(logindex.SearchQuery{
			CollectorID: collectorID,
			Text:        queryText,
			Since:       time.Now().Add(-window),
			Until:       time.Now().UTC(),
			Limit:       limit,
		})
		writeJSON(w, map[string]any{
			"collector_id": collectorID,
			"window":       window.String(),
			"result":       result,
			"timestamp":    time.Now().UTC(),
		})
		return
	}

	if c.ingestStore == nil {
		http.Error(w, "log index unavailable", http.StatusServiceUnavailable)
		return
	}
	node := c.ingestStore.Node(collectorID)
	if node == nil {
		http.Error(w, "collector not found", http.StatusNotFound)
		return
	}
	fingerprints := make([]any, 0, len(node.Logs))
	for _, item := range node.Logs {
		if item == nil {
			continue
		}
		line := strings.TrimSpace(item.Example)
		if queryText != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(queryText)) {
			continue
		}
		fingerprints = append(fingerprints, item)
		if len(fingerprints) >= limit {
			break
		}
	}
	writeJSON(w, map[string]any{
		"collector_id": collectorID,
		"window":       window.String(),
		"fingerprints": fingerprints,
		"count":        len(fingerprints),
		"timestamp":    time.Now().UTC(),
	})
}

func (c *Controller) handleControllerTelemetrySecurity(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	evaluator := securityaudit.NewEvaluator(c.ingestStore, c.logIndex)
	window := parseDurationQuery(r, "window")
	if window <= 0 {
		window = 45 * time.Minute
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 300
	}
	if limit > 2000 {
		limit = 2000
	}

	dashboard := evaluator.Dashboard(securityaudit.Options{
		CollectorID: strings.TrimSpace(firstNonEmpty(
			r.URL.Query().Get("collector_id"),
			r.URL.Query().Get("collector"),
			r.URL.Query().Get("node"),
		)),
		Severity: strings.TrimSpace(r.URL.Query().Get("severity")),
		Category: strings.TrimSpace(r.URL.Query().Get("category")),
		Window:   window,
		Limit:    limit,
	})
	writeJSON(w, dashboard)
}

func (c *Controller) handleControllerAgentRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
		collectorFilter := strings.TrimSpace(r.URL.Query().Get("collector_id"))
		limit := parseLimit(r)
		if limit <= 0 {
			limit = 100
		}
		if limit > 2000 {
			limit = 2000
		}

		c.apiMu.RLock()
		runs := make([]APIRun, 0, len(c.agentRuns))
		for _, state := range c.agentRuns {
			if state == nil {
				continue
			}
			run := state.run
			if statusFilter != "" && strings.ToLower(string(run.Status)) != statusFilter {
				continue
			}
			if collectorFilter != "" && !strings.EqualFold(run.CollectorID, collectorFilter) {
				continue
			}
			runs = append(runs, run)
		}
		c.apiMu.RUnlock()

		sort.Slice(runs, func(i, j int) bool {
			return runs[i].RequestedAt.After(runs[j].RequestedAt)
		})
		if len(runs) > limit {
			runs = runs[:limit]
		}
		writeJSON(w, map[string]any{
			"runs":      runs,
			"count":     len(runs),
			"timestamp": time.Now().UTC(),
		})
	case http.MethodPost:
		if !c.requireActiveController(w) {
			return
		}
		runReq := apiRunRequest{}
		if err := decodeStrictJSON(r, &runReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		run, err := c.startAPIRun(r.Context(), runReq, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, map[string]any{
			"run":       run,
			"timestamp": time.Now().UTC(),
		})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Controller) handleControllerAgentRunByID(w http.ResponseWriter, r *http.Request) {
	if c == nil {
		http.Error(w, "controller unavailable", http.StatusServiceUnavailable)
		return
	}
	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/controller/agent/runs/"), "/")
	if raw == "" {
		http.Error(w, "run id required", http.StatusBadRequest)
		return
	}
	if strings.HasSuffix(raw, "/stop") {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		runID := strings.Trim(strings.TrimSuffix(raw, "/stop"), "/")
		if runID == "" {
			http.Error(w, "run id required", http.StatusBadRequest)
			return
		}
		if !c.requireActiveController(w) {
			return
		}
		stopped, err := c.stopAPIRun(runID, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"run": stopped, "timestamp": time.Now().UTC()})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	c.apiMu.RLock()
	state := c.agentRuns[raw]
	c.apiMu.RUnlock()
	if state == nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"run": state.run, "timestamp": time.Now().UTC()})
}

func (c *Controller) handleControllerActionDryRun(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireActiveController(w) {
		return
	}
	result, err := c.executeAPIFirstAction(r, true, false)
	if err != nil {
		status := mapActionErrorStatus(err)
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, map[string]any{"result": result, "timestamp": time.Now().UTC()})
}

func (c *Controller) handleControllerActionExecute(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireActiveController(w) {
		return
	}
	result, err := c.executeAPIFirstAction(r, false, false)
	if err != nil {
		status := mapActionErrorStatus(err)
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, map[string]any{"result": result, "timestamp": time.Now().UTC()})
}

func (c *Controller) handleControllerActionRollback(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireActiveController(w) {
		return
	}
	result, err := c.executeAPIFirstAction(r, false, true)
	if err != nil {
		status := mapActionErrorStatus(err)
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, map[string]any{"result": result, "timestamp": time.Now().UTC()})
}

func (c *Controller) handleControllerActionApprove(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireActiveController(w) {
		return
	}
	var req apiApproveRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	provided := strings.TrimSpace(req.ApprovalToken)
	if provided == "" {
		http.Error(w, "approval_token is required", http.StatusPreconditionRequired)
		return
	}
	expected := strings.TrimSpace(os.Getenv("SRE_AGENT_APPROVAL_TOKEN"))
	if expected != "" && subtleTokenCompare(expected, provided) != 1 {
		c.appendControllerAudit(r, ControllerAuditRecord{
			Action:       "action_approve",
			Resource:     "approval-token",
			Status:       "forbidden",
			Output:       "approval token mismatch",
			ApprovalGate: true,
		})
		http.Error(w, "approval token invalid", http.StatusForbidden)
		return
	}
	approvalID := fmt.Sprintf("approval-%d", time.Now().UnixNano())
	c.appendControllerAudit(r, ControllerAuditRecord{
		Action:       "action_approve",
		Resource:     approvalID,
		Status:       "approved",
		Output:       "approval token accepted",
		ApprovalGate: true,
	})
	writeJSON(w, map[string]any{
		"approval_id": approvalID,
		"approved":    true,
		"expires_at":  time.Now().UTC().Add(30 * time.Minute),
		"timestamp":   time.Now().UTC(),
	})
}

func (c *Controller) handleControllerAuditLog(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}
	actorFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("actor")))
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))

	c.apiMu.RLock()
	records := append([]ControllerAuditRecord(nil), c.controllerAuditLog...)
	c.apiMu.RUnlock()

	filtered := make([]ControllerAuditRecord, 0, len(records))
	for _, record := range records {
		if actorFilter != "" && strings.ToLower(strings.TrimSpace(record.Actor)) != actorFilter {
			continue
		}
		if statusFilter != "" && strings.ToLower(strings.TrimSpace(record.Status)) != statusFilter {
			continue
		}
		filtered = append(filtered, record)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].OccurredAt.After(filtered[j].OccurredAt)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	writeJSON(w, map[string]any{
		"records":   filtered,
		"count":     len(filtered),
		"timestamp": time.Now().UTC(),
	})
}

func (c *Controller) handleControllerToolRegistry(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentWorkflow(w) {
		return
	}
	tools := c.agentWorkflow.ToolRegistry()
	writeJSON(w, agentcore.WorkflowToolRegistryResponse{
		Tools:     tools,
		Count:     len(tools),
		Timestamp: time.Now().UTC(),
	})
}

func (c *Controller) startAPIRun(ctx context.Context, req apiRunRequest, r *http.Request) (APIRun, error) {
	if c == nil || c.agentWorkflow == nil {
		return APIRun{}, fmt.Errorf("agent workflow engine disabled")
	}
	workflowType := strings.ToLower(strings.TrimSpace(req.WorkflowType))
	if workflowType == "" {
		workflowType = "joint_risk"
	}
	if workflowType != "joint_risk" && workflowType != "rca" {
		return APIRun{}, fmt.Errorf("unsupported workflow_type")
	}

	window := parseWorkflowWindow(req.Window)
	runID := fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), controllerRunSeq.Add(1))
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	requestedAt := time.Now().UTC()
	trigger := strings.TrimSpace(req.Trigger)
	if trigger == "" {
		trigger = "manual"
	}

	state := &apiAgentRunState{
		run: APIRun{
			RunID:        runID,
			WorkflowType: workflowType,
			Status:       APIRunQueued,
			CollectorID:  strings.TrimSpace(req.CollectorID),
			Trigger:      trigger,
			DryRun:       dryRun,
			RequestedAt:  requestedAt,
		},
	}

	runCtx := context.Background()
	if c.ctx != nil {
		runCtx = c.ctx
	}
	runCtx, cancel := context.WithCancel(runCtx)
	state.cancel = cancel

	c.apiMu.Lock()
	c.agentRuns[runID] = state
	c.apiMu.Unlock()

	go c.executeAPIRun(runCtx, state, agentcore.WorkflowRequest{
		WorkflowType: workflowType,
		CollectorID:  strings.TrimSpace(req.CollectorID),
		Window:       window,
		Limit:        req.Limit,
		Trigger:      trigger,
		DryRun:       req.DryRun,
	}, r)

	c.appendControllerAudit(r, ControllerAuditRecord{
		Action:      "agent_run_start",
		Resource:    runID,
		Status:      "queued",
		CollectorID: strings.TrimSpace(req.CollectorID),
		Input: map[string]string{
			"workflow_type": workflowType,
			"window":        window.String(),
			"trigger":       trigger,
			"dry_run":       fmt.Sprintf("%t", dryRun),
		},
		Output: runID,
	})
	return state.run, nil
}

func (c *Controller) executeAPIRun(ctx context.Context, state *apiAgentRunState, req agentcore.WorkflowRequest, r *http.Request) {
	started := time.Now().UTC()
	c.apiMu.Lock()
	if state != nil {
		state.run.Status = APIRunRunning
		state.run.StartedAt = &started
	}
	c.apiMu.Unlock()

	var (
		summary         string
		recommendations []string
		evidence        []string
		workflowID      string
		incidentID      string
		riskLevel       string
		riskScore       float64
		err             error
	)

	switch req.WorkflowType {
	case "rca":
		var report agentcore.RCAWorkflowReport
		report, err = c.agentWorkflow.BuildRCAWorkflow(ctx, req)
		if err == nil {
			summary = report.StructuredReport.MostLikelyCause
			if summary == "" {
				if len(report.Anomalies) > 0 {
					summary = report.Anomalies[0]
				}
			}
			workflowID = report.WorkflowID
			incidentID = report.IncidentID
			riskLevel = "investigation"
			for _, rec := range report.Recommendations {
				recommendations = append(recommendations, rec.Summary)
			}
			for _, row := range report.Evidence {
				evidence = append(evidence, row.Summary)
			}
		}
	default:
		var report agentcore.JointRiskAssessment
		report, err = c.agentWorkflow.EvaluateJointRisk(ctx, req)
		if err == nil {
			summary = report.Summary
			workflowID = report.WorkflowID
			incidentID = report.IncidentID
			riskLevel = report.RiskLevel
			riskScore = report.RiskScore
			for _, rec := range report.Recommendations {
				recommendations = append(recommendations, rec.Summary)
			}
			for _, signal := range report.Signals {
				if signal.Triggered {
					evidence = append(evidence, signal.Name)
				}
			}
		}
	}

	recommendations = dedupeStringSlice(recommendations, 8)
	evidence = dedupeStringSlice(evidence, 8)
	completed := time.Now().UTC()

	c.apiMu.Lock()
	if state != nil {
		state.run.CompletedAt = &completed
		state.run.WorkflowID = workflowID
		state.run.IncidentID = incidentID
		state.run.RiskLevel = riskLevel
		state.run.RiskScore = riskScore
		state.run.Summary = strings.TrimSpace(summary)
		state.run.Recommendations = recommendations
		state.run.Evidence = evidence
		if err != nil {
			if errors.Is(err, context.Canceled) {
				state.run.Status = APIRunStopped
				state.run.ErrorMessage = "run cancelled"
			} else {
				state.run.Status = APIRunFailed
				state.run.ErrorMessage = err.Error()
			}
		} else {
			state.run.Status = APIRunCompleted
		}
	}
	c.apiMu.Unlock()

	status := "completed"
	if err != nil {
		status = "failed"
	}
	if errors.Is(err, context.Canceled) {
		status = "stopped"
	}
	c.appendControllerAudit(r, ControllerAuditRecord{
		Action:      "agent_run_finish",
		Resource:    state.run.RunID,
		Status:      status,
		CollectorID: state.run.CollectorID,
		WorkflowID:  workflowID,
		IncidentID:  incidentID,
		Output:      state.run.Summary,
		Evidence:    evidence,
	})
}

func (c *Controller) stopAPIRun(runID string, r *http.Request) (APIRun, error) {
	c.apiMu.Lock()
	state := c.agentRuns[runID]
	if state == nil {
		c.apiMu.Unlock()
		return APIRun{}, fmt.Errorf("run not found")
	}
	if state.cancel != nil {
		state.cancel()
	}
	now := time.Now().UTC()
	state.run.Status = APIRunStopped
	state.run.CompletedAt = &now
	run := state.run
	c.apiMu.Unlock()

	c.appendControllerAudit(r, ControllerAuditRecord{
		Action:      "agent_run_stop",
		Resource:    runID,
		Status:      "stopped",
		CollectorID: run.CollectorID,
		WorkflowID:  run.WorkflowID,
		IncidentID:  run.IncidentID,
		Output:      "cancellation requested",
	})
	return run, nil
}

func (c *Controller) executeAPIFirstAction(r *http.Request, forceDryRun bool, rollback bool) (any, error) {
	var req apiActionRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = "incident_action"
	}
	if strings.TrimSpace(req.ActionID) == "" {
		return nil, fmt.Errorf("action_id is required")
	}
	if forceDryRun {
		dry := true
		req.DryRun = &dry
	}

	switch kind {
	case "query_action":
		if rollback {
			return nil, fmt.Errorf("query_action rollback is not supported")
		}
		if c.agentService == nil {
			return nil, fmt.Errorf("agent query service disabled")
		}
		result, err := c.agentService.Execute(r.Context(), agentcore.ExecuteRequest{
			ActionID:      strings.TrimSpace(req.ActionID),
			DryRun:        req.DryRun,
			ApprovalToken: strings.TrimSpace(req.ApprovalToken),
		})
		status := "success"
		if err != nil {
			status = "failed"
		}
		c.appendControllerAudit(r, ControllerAuditRecord{
			Action:       map[bool]string{true: "query_action_dry_run", false: "query_action_execute"}[forceDryRun],
			Resource:     strings.TrimSpace(req.ActionID),
			Status:       status,
			Output:       firstNonEmpty(result.Result.Status, "n/a"),
			ApprovalGate: !forceDryRun,
		})
		return result, err
	default:
		if c.agentEngine == nil {
			return nil, fmt.Errorf("incident action engine disabled")
		}
		alertID := strings.TrimSpace(firstNonEmpty(req.AlertID, req.IncidentID))
		if alertID == "" {
			return nil, fmt.Errorf("alert_id is required for incident_action")
		}
		if rollback {
			result, err := c.agentEngine.RollbackIncidentAction(alertID, strings.TrimSpace(req.ActionID), agent.IncidentActionRollbackRequest{
				DryRun:        req.DryRun,
				ApprovalToken: strings.TrimSpace(req.ApprovalToken),
				RollbackID:    strings.TrimSpace(req.RollbackID),
			})
			status := "success"
			if err != nil {
				status = "failed"
			}
			c.appendControllerAudit(r, ControllerAuditRecord{
				Action:       "incident_action_rollback",
				Resource:     alertID + "/" + strings.TrimSpace(req.ActionID),
				Status:       status,
				IncidentID:   alertID,
				Output:       result.Status,
				ApprovalGate: true,
			})
			return result, err
		}
		result, err := c.agentEngine.ExecuteIncidentAction(alertID, strings.TrimSpace(req.ActionID), agent.IncidentActionExecuteRequest{
			DryRun:        req.DryRun,
			ApprovalToken: strings.TrimSpace(req.ApprovalToken),
		})
		status := "success"
		if err != nil {
			status = "failed"
		}
		c.appendControllerAudit(r, ControllerAuditRecord{
			Action:       map[bool]string{true: "incident_action_dry_run", false: "incident_action_execute"}[forceDryRun],
			Resource:     alertID + "/" + strings.TrimSpace(req.ActionID),
			Status:       status,
			IncidentID:   alertID,
			Output:       result.Status,
			ApprovalGate: !forceDryRun,
		})
		return result, err
	}
}

func mapActionErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	switch {
	case errors.Is(err, agentcore.ErrActionNotFound), errors.Is(err, agent.ErrIncidentActionNotFound), errors.Is(err, agent.ErrIncidentNotFound):
		return http.StatusNotFound
	case errors.Is(err, agentcore.ErrApprovalRequired), errors.Is(err, agent.ErrIncidentActionApprovalRequired):
		return http.StatusPreconditionRequired
	case errors.Is(err, agentcore.ErrApprovalInvalid), errors.Is(err, agent.ErrIncidentActionApprovalInvalid):
		return http.StatusForbidden
	case errors.Is(err, agent.ErrIncidentActionNotReversible):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func (c *Controller) appendControllerAudit(r *http.Request, record ControllerAuditRecord) {
	if c == nil {
		return
	}
	if r != nil {
		record.Actor = firstNonEmpty(strings.TrimSpace(r.Header.Get("X-SRE-Actor")), strings.TrimSpace(r.Header.Get("X-User")), "anonymous")
	}
	if record.Actor == "" {
		record.Actor = "controller"
	}
	if record.ID == "" {
		record.ID = fmt.Sprintf("audit-%d", time.Now().UnixNano())
	}
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	}
	record.Evidence = dedupeStringSlice(record.Evidence, 8)

	c.apiMu.Lock()
	c.controllerAuditLog = append(c.controllerAuditLog, record)
	if len(c.controllerAuditLog) > controllerAuditRetention {
		c.controllerAuditLog = c.controllerAuditLog[len(c.controllerAuditLog)-controllerAuditRetention:]
	}
	c.apiMu.Unlock()
}

func (c *Controller) appendInternalControllerAudit(record ControllerAuditRecord) {
	c.appendControllerAudit(nil, record)
}

func parseWorkflowWindow(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 45 * time.Minute
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return 45 * time.Minute
	}
	return parsed
}

func subtleTokenCompare(expected, provided string) int {
	if len(expected) != len(provided) {
		return 0
	}
	if expected == provided {
		return 1
	}
	return 0
}

func dedupeStringSlice(values []string, limit int) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (c *Controller) handleAgentProposedActions(w http.ResponseWriter, r *http.Request) {
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
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

	store := c.agentWorkflow.ProposedActionStoreRef()
	if store == nil {
		writeJSON(w, map[string]any{
			"actions":   []any{},
			"count":     0,
			"timestamp": time.Now().UTC(),
		})
		return
	}

	actions := store.ListActions(limit, statusFilter)
	writeJSON(w, map[string]any{
		"actions":   actions,
		"count":     len(actions),
		"timestamp": time.Now().UTC(),
	})
}

func (c *Controller) handleAgentTrace(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireAgentWorkflow(w) {
		return
	}

	traceID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/agent/trace/"), "/")
	store := c.agentWorkflow.TraceStoreRef()
	if store == nil {
		http.Error(w, "trace store unavailable", http.StatusServiceUnavailable)
		return
	}

	if traceID == "" {
		// List traces
		limit := parseLimit(r)
		if limit <= 0 {
			limit = 50
		}
		traces := store.ListTraces(limit)
		writeJSON(w, map[string]any{
			"traces":    traces,
			"count":     len(traces),
			"timestamp": time.Now().UTC(),
		})
		return
	}

	trace, ok := store.GetTrace(traceID)
	if !ok {
		http.Error(w, "trace not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{
		"trace":     trace,
		"timestamp": time.Now().UTC(),
	})
}
func (c *Controller) handleHealthJSON(w http.ResponseWriter, r *http.Request) {
	demoEnv := strings.TrimSpace(os.Getenv("SRE_AGENT_DEMO_MODE"))
	demoMode := demoEnv == "true" || demoEnv == "1"

	llmMode := resolveLLMMode()
	agentReady := c.agentWorkflow != nil
	baselineReady := false
	ebpfLoaded := false

	if agentReady {
		if be := c.agentWorkflow.BaselineEngine(); be != nil {
			baselineReady = be.Ready()
		}
		ebpfLoaded = c.detectEBPFPresence()
	}

	writeJSON(w, map[string]any{
		"status":         "healthy",
		"ebpf_loaded":    ebpfLoaded,
		"baseline_ready": baselineReady,
		"llm_mode":       llmMode,
		"demo_mode":      demoMode,
		"agent_ready":    agentReady,
	})
}

func resolveLLMMode() string {
	enabledRaw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_ENABLED"))
	if enabledRaw == "" {
		enabledRaw = strings.TrimSpace(os.Getenv("SRE_AGENT_LLM_ENABLED"))
	}
	provider := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_PROVIDER"))
	if provider == "" {
		provider = strings.TrimSpace(os.Getenv("SRE_AGENT_LLM_PROVIDER"))
	}
	if provider == "gemini" {
		provider = "google"
	}
	if parseBoolString(enabledRaw, false) {
		if provider == "" {
			return "unknown"
		}
		return provider
	}
	if strings.Contains(provider, "stub") || strings.Contains(provider, "mock") {
		return "stub"
	}
	return "disabled"
}

func (c *Controller) detectEBPFPresence() bool {
	if c.ingestStore == nil {
		return false
	}
	for _, node := range c.ingestStore.Snapshot() {
		if node == nil {
			continue
		}
		if len(node.RuntimeSecurityEvents) > 0 {
			return true
		}
		if len(node.SyscallStatistics) > 0 {
			return true
		}
	}
	return false
}
