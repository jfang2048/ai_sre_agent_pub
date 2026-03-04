package agent

import (
	"sync"
	"time"
)

// AgentTrace captures the full replayable trace of an agent workflow execution.
type AgentTrace struct {
	TraceID           string                `json:"trace_id"`
	WorkflowType      string                `json:"workflow_type"`
	CollectorID       string                `json:"collector_id,omitempty"`
	StartedAt         time.Time             `json:"started_at"`
	CompletedAt       time.Time             `json:"completed_at,omitempty"`
	Status            string                `json:"status"`
	PlanVersions      []AgentPlanRevision   `json:"plan_versions,omitempty"`
	ToolCalls         []WorkflowToolCall    `json:"tool_calls,omitempty"`
	HypothesisUpdates []HypothesisUpdate    `json:"hypothesis_updates,omitempty"`
	RiskTimeline      []RiskTimelineEntry   `json:"risk_timeline,omitempty"`
	Stages            []PipelineStageResult `json:"stages,omitempty"`
	FinalRiskScore    float64               `json:"final_risk_score"`
	Summary           string                `json:"summary,omitempty"`
}

// HypothesisUpdate records a change to a hypothesis during workflow execution.
type HypothesisUpdate struct {
	Timestamp     time.Time `json:"timestamp"`
	HypothesisID  string    `json:"hypothesis_id"`
	Action        string    `json:"action"`
	OldConfidence float64   `json:"old_confidence,omitempty"`
	NewConfidence float64   `json:"new_confidence"`
	Reason        string    `json:"reason,omitempty"`
}

// RiskTimelineEntry records a risk score at a point in time.
type RiskTimelineEntry struct {
	Timestamp time.Time `json:"timestamp"`
	RiskScore float64   `json:"risk_score"`
	RiskLevel string    `json:"risk_level"`
	Source    string    `json:"source"`
}

// TraceStore provides bounded in-memory storage for agent traces.
type TraceStore struct {
	mu        sync.RWMutex
	traces    []*AgentTrace
	byID      map[string]*AgentTrace
	maxTraces int
}

// NewTraceStore creates a bounded trace store.
func NewTraceStore(maxTraces int) *TraceStore {
	if maxTraces <= 0 {
		maxTraces = 500
	}
	return &TraceStore{
		traces:    make([]*AgentTrace, 0, 64),
		byID:      make(map[string]*AgentTrace, 64),
		maxTraces: maxTraces,
	}
}

// RecordTrace stores or updates a trace.
func (ts *TraceStore) RecordTrace(trace *AgentTrace) {
	if trace == nil || trace.TraceID == "" {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if existing, ok := ts.byID[trace.TraceID]; ok {
		*existing = *trace
		return
	}

	if len(ts.traces) >= ts.maxTraces {
		evict := ts.traces[0]
		delete(ts.byID, evict.TraceID)
		ts.traces = ts.traces[1:]
	}
	ts.traces = append(ts.traces, trace)
	ts.byID[trace.TraceID] = trace
}

// GetTrace retrieves a trace by ID.
func (ts *TraceStore) GetTrace(traceID string) (*AgentTrace, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	t, ok := ts.byID[traceID]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// ListTraces returns the most recent traces, newest first.
func (ts *TraceStore) ListTraces(limit int) []*AgentTrace {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	n := len(ts.traces)
	if limit <= 0 || limit > n {
		limit = n
	}
	result := make([]*AgentTrace, limit)
	for i := 0; i < limit; i++ {
		cp := *ts.traces[n-1-i]
		result[i] = &cp
	}
	return result
}

// Size returns the number of stored traces.
func (ts *TraceStore) Size() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.traces)
}

// TracesByCollector returns traces for a specific collector.
func (ts *TraceStore) TracesByCollector(collectorID string, limit int) []*AgentTrace {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var result []*AgentTrace
	for i := len(ts.traces) - 1; i >= 0 && len(result) < limit; i-- {
		if ts.traces[i].CollectorID == collectorID {
			cp := *ts.traces[i]
			result = append(result, &cp)
		}
	}
	return result
}

// AppendToolCall adds a tool call to an existing trace.
func (ts *TraceStore) AppendToolCall(traceID string, tc WorkflowToolCall) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if t, ok := ts.byID[traceID]; ok {
		t.ToolCalls = append(t.ToolCalls, tc)
	}
}

// AppendRiskEntry adds a risk timeline entry to an existing trace.
func (ts *TraceStore) AppendRiskEntry(traceID string, entry RiskTimelineEntry) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if t, ok := ts.byID[traceID]; ok {
		t.RiskTimeline = append(t.RiskTimeline, entry)
	}
}

// AppendHypothesisUpdate adds a hypothesis update to an existing trace.
func (ts *TraceStore) AppendHypothesisUpdate(traceID string, hu HypothesisUpdate) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if t, ok := ts.byID[traceID]; ok {
		t.HypothesisUpdates = append(t.HypothesisUpdates, hu)
	}
}

// AppendStage adds a pipeline stage result to an existing trace.
func (ts *TraceStore) AppendStage(traceID string, stage PipelineStageResult) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if t, ok := ts.byID[traceID]; ok {
		t.Stages = append(t.Stages, stage)
	}
}

// ----- ProposedAction -----

// ProposedAction represents a remediation action proposed by the agent.
type ProposedAction struct {
	ID               string    `json:"id"`
	RiskReference    string    `json:"risk_reference"`
	CommandPreview   string    `json:"command_preview"`
	ImpactScope      string    `json:"impact_scope"`
	RiskLevel        string    `json:"risk_level"`
	RollbackPlan     string    `json:"rollback_plan"`
	ApprovalRequired bool      `json:"approval_required"`
	DryRunPlan       string    `json:"dry_run_plan,omitempty"`
	AuditIntent      string    `json:"audit_intent"`
	CollectorID      string    `json:"collector_id,omitempty"`
	WorkflowID       string    `json:"workflow_id,omitempty"`
	ProposedAt       time.Time `json:"proposed_at"`
	Status           string    `json:"status"`
}

// ProposedActionStore provides bounded in-memory storage for proposed actions.
type ProposedActionStore struct {
	mu         sync.RWMutex
	actions    []*ProposedAction
	byID       map[string]*ProposedAction
	maxActions int
}

// NewProposedActionStore creates a bounded action store.
func NewProposedActionStore(maxActions int) *ProposedActionStore {
	if maxActions <= 0 {
		maxActions = 200
	}
	return &ProposedActionStore{
		actions:    make([]*ProposedAction, 0, 32),
		byID:       make(map[string]*ProposedAction, 32),
		maxActions: maxActions,
	}
}

// RecordAction stores a proposed action.
func (pas *ProposedActionStore) RecordAction(action *ProposedAction) {
	if action == nil || action.ID == "" {
		return
	}
	pas.mu.Lock()
	defer pas.mu.Unlock()

	if existing, ok := pas.byID[action.ID]; ok {
		*existing = *action
		return
	}
	if len(pas.actions) >= pas.maxActions {
		evict := pas.actions[0]
		delete(pas.byID, evict.ID)
		pas.actions = pas.actions[1:]
	}
	pas.actions = append(pas.actions, action)
	pas.byID[action.ID] = action
}

// GetAction retrieves an action by ID.
func (pas *ProposedActionStore) GetAction(id string) (*ProposedAction, bool) {
	pas.mu.RLock()
	defer pas.mu.RUnlock()
	a, ok := pas.byID[id]
	if !ok {
		return nil, false
	}
	cp := *a
	return &cp, true
}

// ListActions returns actions, newest first, optionally filtered by status.
func (pas *ProposedActionStore) ListActions(limit int, status string) []*ProposedAction {
	pas.mu.RLock()
	defer pas.mu.RUnlock()

	var result []*ProposedAction
	for i := len(pas.actions) - 1; i >= 0 && (limit <= 0 || len(result) < limit); i-- {
		if status != "" && pas.actions[i].Status != status {
			continue
		}
		cp := *pas.actions[i]
		result = append(result, &cp)
	}
	return result
}

// UpdateStatus updates the status of an action.
func (pas *ProposedActionStore) UpdateStatus(id, status string) bool {
	pas.mu.Lock()
	defer pas.mu.Unlock()
	a, ok := pas.byID[id]
	if !ok {
		return false
	}
	a.Status = status
	return true
}

// Size returns the number of stored actions.
func (pas *ProposedActionStore) Size() int {
	pas.mu.RLock()
	defer pas.mu.RUnlock()
	return len(pas.actions)
}

// GenerateProposedActions creates proposed actions from workflow recommendations.
func GenerateProposedActions(workflowID, collectorID string, recs []WorkflowRecommendation, riskScore float64) []*ProposedAction {
	now := time.Now()
	riskLevel := riskLevelFromScore(riskScore)

	actions := make([]*ProposedAction, 0, len(recs))
	for _, rec := range recs {
		actions = append(actions, &ProposedAction{
			ID:               rec.ID,
			RiskReference:    workflowID,
			CommandPreview:   rec.Summary,
			ImpactScope:      rec.Scope,
			RiskLevel:        riskLevel,
			RollbackPlan:     rec.RollbackHint,
			ApprovalRequired: rec.RequiresApproval,
			DryRunPlan:       rec.Summary,
			AuditIntent:      rec.Summary,
			CollectorID:      collectorID,
			WorkflowID:       workflowID,
			ProposedAt:       now,
			Status:           "proposed",
		})
	}
	return actions
}

func riskLevelFromScore(score float64) string {
	if score >= 70 {
		return "high"
	}
	if score >= 40 {
		return "medium"
	}
	return "low"
}
