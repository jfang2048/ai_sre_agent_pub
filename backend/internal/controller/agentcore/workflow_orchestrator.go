package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// RunStatus represents the durable state of a workflow run.
type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusSuspended RunStatus = "suspended"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

// WorkflowEvent records an auditable state transition or action in a durable run.
type WorkflowEvent struct {
	EventID   string         `json:"event_id"`
	Type      string         `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// DurablePolicyRecord captures the policy verdict that governed a tool or action.
type DurablePolicyRecord struct {
	Decision      ActionPolicyDecision `json:"decision"`
	PolicyVersion string               `json:"policy_version,omitempty"`
	RiskTag       string               `json:"risk_tag,omitempty"`
	EvaluatedAt   time.Time            `json:"evaluated_at"`
}

// DurableApprovalRecord captures the approval state for a step or action.
type DurableApprovalRecord struct {
	State       string     `json:"state"`
	Actor       string     `json:"actor,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	RequestedAt time.Time  `json:"requested_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

// DurableVerificationRecord captures post-action verification data.
type DurableVerificationRecord struct {
	Outcome      string    `json:"outcome"`
	Success      bool      `json:"success"`
	Objective    string    `json:"objective,omitempty"`
	Note         string    `json:"note,omitempty"`
	Window       string    `json:"window,omitempty"`
	PreviousRisk float64   `json:"previous_risk,omitempty"`
	CurrentRisk  float64   `json:"current_risk,omitempty"`
	EvidenceIDs  []string  `json:"evidence_ids,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
}

// DurableCompensationRecord captures rollback or compensating action state.
type DurableCompensationRecord struct {
	Status      string    `json:"status"`
	Summary     string    `json:"summary,omitempty"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// DurableStepRecord captures the persisted state of a plan/act/verify step.
type DurableStepRecord struct {
	StepID           string                     `json:"step_id"`
	Stage            string                     `json:"stage,omitempty"`
	Title            string                     `json:"title,omitempty"`
	Tool             ToolName                   `json:"tool,omitempty"`
	Query            map[string]string          `json:"query,omitempty"`
	Order            int                        `json:"order,omitempty"`
	Iteration        int                        `json:"iteration,omitempty"`
	Required         bool                       `json:"required"`
	Status           string                     `json:"status"`
	ResultSummary    string                     `json:"result_summary,omitempty"`
	ErrorMessage     string                     `json:"error_message,omitempty"`
	Verified         bool                       `json:"verified"`
	VerificationNote string                     `json:"verification_note,omitempty"`
	EvidenceIDs      []string                   `json:"evidence_ids,omitempty"`
	LastToolCallID   string                     `json:"last_tool_call_id,omitempty"`
	OriginalAction   *ActionSpec                `json:"original_action,omitempty"`
	Policy           *DurablePolicyRecord       `json:"policy,omitempty"`
	Approval         *DurableApprovalRecord     `json:"approval,omitempty"`
	Verification     *DurableVerificationRecord `json:"verification,omitempty"`
	Compensation     *DurableCompensationRecord `json:"compensation,omitempty"`
	StartedAt        time.Time                  `json:"started_at,omitempty"`
	CompletedAt      time.Time                  `json:"completed_at,omitempty"`
}

// DurableEvidencePackageRef points to a generated evidence package on disk.
type DurableEvidencePackageRef struct {
	Path        string    `json:"path"`
	GeneratedAt time.Time `json:"generated_at"`
}

// DurableWorldModel captures the environment topology and scope used by a run.
type DurableWorldModel struct {
	Summary         string           `json:"summary,omitempty"`
	Scope           []string         `json:"scope,omitempty"`
	DownstreamNodes []string         `json:"downstream_nodes,omitempty"`
	RecentChanges   []string         `json:"recent_changes,omitempty"`
	Topology        TopologySnapshot `json:"topology,omitempty"`
}

// DurableRun models a persistent workflow execution.
type DurableRun struct {
	RunID           string                     `json:"run_id"`
	WorkflowType    string                     `json:"workflow_type"`
	CollectorID     string                     `json:"collector_id"`
	Request         WorkflowRequest            `json:"request"`
	Status          RunStatus                  `json:"status"`
	CurrentStep     string                     `json:"current_step"`
	CurrentStage    string                     `json:"current_stage,omitempty"`
	Events          []WorkflowEvent            `json:"events"`
	ToolCalls       []WorkflowToolCall         `json:"tool_calls,omitempty"`
	PlanRevisions   []AgentPlanRevision        `json:"plan_revisions,omitempty"`
	Steps           []DurableStepRecord        `json:"steps,omitempty"`
	AnalysisHandoff *AnalysisHandoff           `json:"analysis_handoff,omitempty"`
	Validation      *ValidationActionReport    `json:"validation,omitempty"`
	ValidationLoops []ValidationLoopRecord     `json:"validation_loops,omitempty"`
	EvidencePackage *DurableEvidencePackageRef `json:"evidence_package,omitempty"`
	WorldModel      *DurableWorldModel         `json:"world_model,omitempty"`
	MemoryRecords   []string                   `json:"memory_records,omitempty"`
	ReplayCount     int                        `json:"replay_count,omitempty"`
	LastResumeAt    time.Time                  `json:"last_resume_at,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
	UpdatedAt       time.Time                  `json:"updated_at"`
	Result          []byte                     `json:"result,omitempty"`
	Error           string                     `json:"error,omitempty"`
	Context         map[string]any             `json:"context,omitempty"`
}

// DurableStore defines the persistence contract for runs.
type DurableStore interface {
	SaveRun(ctx context.Context, run *DurableRun) error
	LoadRun(ctx context.Context, runID string) (*DurableRun, error)
	AppendEvent(ctx context.Context, runID string, event WorkflowEvent) error
	ListRuns(ctx context.Context, workflowType string, limit int) ([]*DurableRun, error)
}

// InMemoryDurableStore is a local persistence implementation of DurableStore.
type InMemoryDurableStore struct {
	mu   sync.RWMutex
	runs map[string]*DurableRun
}

func NewInMemoryDurableStore() *InMemoryDurableStore {
	return &InMemoryDurableStore{
		runs: make(map[string]*DurableRun),
	}
}

func (s *InMemoryDurableStore) SaveRun(_ context.Context, run *DurableRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := &DurableRun{}
	b, _ := json.Marshal(run)
	_ = json.Unmarshal(b, out)
	s.runs[run.RunID] = out
	return nil
}

func (s *InMemoryDurableStore) LoadRun(_ context.Context, runID string) (*DurableRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run not found: %s", runID)
	}
	out := &DurableRun{}
	b, _ := json.Marshal(run)
	_ = json.Unmarshal(b, out)
	return out, nil
}

func (s *InMemoryDurableStore) AppendEvent(_ context.Context, runID string, event WorkflowEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	run.Events = append(run.Events, event)
	run.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *InMemoryDurableStore) ListRuns(_ context.Context, workflowType string, limit int) ([]*DurableRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*DurableRun, 0, len(s.runs))
	for _, run := range s.runs {
		if workflowType != "" && run.WorkflowType != workflowType {
			continue
		}
		cloned := &DurableRun{}
		b, _ := json.Marshal(run)
		_ = json.Unmarshal(b, cloned)
		out = append(out, cloned)
	}
	sortRuns(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// BoltDurableStore is a BoltDB-backed implementation of DurableStore.
type BoltDurableStore struct {
	db   *bolt.DB
	path string
}

func NewBoltDurableStore(path string) (*BoltDurableStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, bucketErr := tx.CreateBucketIfNotExists([]byte("runs"))
		return bucketErr
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &BoltDurableStore{db: db, path: path}, nil
}

func (s *BoltDurableStore) SaveRun(_ context.Context, run *DurableRun) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("runs"))
		payload, err := json.Marshal(run)
		if err != nil {
			return err
		}
		return b.Put([]byte(run.RunID), payload)
	})
}

func (s *BoltDurableStore) LoadRun(_ context.Context, runID string) (*DurableRun, error) {
	var run DurableRun
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("runs"))
		v := b.Get([]byte(runID))
		if v == nil {
			return fmt.Errorf("run not found: %s", runID)
		}
		return json.Unmarshal(v, &run)
	})
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *BoltDurableStore) AppendEvent(_ context.Context, runID string, event WorkflowEvent) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("runs"))
		v := b.Get([]byte(runID))
		if v == nil {
			return fmt.Errorf("run not found: %s", runID)
		}
		var run DurableRun
		if err := json.Unmarshal(v, &run); err != nil {
			return err
		}
		run.Events = append(run.Events, event)
		run.UpdatedAt = time.Now().UTC()
		payload, err := json.Marshal(run)
		if err != nil {
			return err
		}
		return b.Put([]byte(runID), payload)
	})
}

func (s *BoltDurableStore) ListRuns(_ context.Context, workflowType string, limit int) ([]*DurableRun, error) {
	var out []*DurableRun
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("runs"))
		return b.ForEach(func(_, v []byte) error {
			var run DurableRun
			if err := json.Unmarshal(v, &run); err != nil {
				return nil
			}
			if workflowType != "" && run.WorkflowType != workflowType {
				return nil
			}
			out = append(out, &run)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sortRuns(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *BoltDurableStore) Close() error {
	return s.db.Close()
}

// DurableOrchestrator wraps the pipeline steps in a persistent state machine.
type DurableOrchestrator struct {
	store  DurableStore
	logger *zap.Logger
}

func NewDurableOrchestrator(store DurableStore, logger *zap.Logger) *DurableOrchestrator {
	if store == nil {
		store = NewInMemoryDurableStore()
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &DurableOrchestrator{
		store:  store,
		logger: logger.With(zap.String("component", "durable_orchestrator")),
	}
}

// StartRun initializes and persists a new run.
func (o *DurableOrchestrator) StartRun(ctx context.Context, runID, workflowType, collectorID string) (*DurableRun, error) {
	return o.StartRunWithRequest(ctx, runID, workflowType, collectorID, WorkflowRequest{
		WorkflowType: workflowType,
		CollectorID:  collectorID,
	})
}

// StartRunWithRequest initializes and persists a new run together with its request contract.
func (o *DurableOrchestrator) StartRunWithRequest(ctx context.Context, runID, workflowType, collectorID string, req WorkflowRequest) (*DurableRun, error) {
	run := &DurableRun{
		RunID:           runID,
		WorkflowType:    workflowType,
		CollectorID:     collectorID,
		Request:         req,
		Status:          RunStatusRunning,
		CurrentStep:     "init",
		CurrentStage:    "init",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
		Events:          []WorkflowEvent{},
		ToolCalls:       []WorkflowToolCall{},
		PlanRevisions:   []AgentPlanRevision{},
		Steps:           []DurableStepRecord{},
		ValidationLoops: []ValidationLoopRecord{},
		Context:         make(map[string]any),
	}
	if err := o.store.SaveRun(ctx, run); err != nil {
		return nil, err
	}
	_ = o.LogEvent(ctx, runID, "run_started", map[string]any{
		"workflow_type": workflowType,
		"collector_id":  collectorID,
	})
	return run, nil
}

// GetRun loads a durable run by ID.
func (o *DurableOrchestrator) GetRun(ctx context.Context, runID string) (*DurableRun, error) {
	return o.store.LoadRun(ctx, runID)
}

// RecordStepTransition updates the current step and persists a stage transition event.
func (o *DurableOrchestrator) RecordStepTransition(ctx context.Context, runID, stepName string) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.CurrentStep = stepName
		run.CurrentStage = stepName
	})
}

// RecordPlanRevision persists a plan revision snapshot.
func (o *DurableOrchestrator) RecordPlanRevision(ctx context.Context, runID string, revision AgentPlanRevision) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.PlanRevisions = append(run.PlanRevisions, revision)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "plan_revision_recorded", map[string]any{
		"iteration": revision.Iteration,
		"reason":    revision.Reason,
		"steps":     len(revision.Steps),
	})
}

// RecordToolCall appends a governed tool invocation to the durable run.
func (o *DurableOrchestrator) RecordToolCall(ctx context.Context, runID string, call WorkflowToolCall) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.ToolCalls = append(run.ToolCalls, call)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "tool_call_recorded", map[string]any{
		"tool":            call.Tool,
		"status":          call.Status,
		"idempotency_key": call.IdempotencyKey,
		"approval_state":  call.ApprovalState,
		"policy_status":   call.Policy.Status,
		"policy_version":  call.PolicyVersion,
		"risk_tag":        call.RiskTag,
		"tool_call_id":    call.ID,
		"tool_call_stage": call.Stage,
		"tool_call_actor": call.Actor,
	})
}

// RecordStepState upserts a persisted plan step record.
func (o *DurableOrchestrator) RecordStepState(ctx context.Context, runID, stage string, step AgentPlanStep) error {
	if step.ID == "" {
		return nil
	}
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		record := durableStepFromPlanStep(stage, step)
		upsertDurableStep(&run.Steps, record)
	})
}

func (o *DurableOrchestrator) AttachAnalysisHandoff(ctx context.Context, runID string, handoff AnalysisHandoff) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		copy := handoff
		run.AnalysisHandoff = &copy
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "analysis_handoff_attached", map[string]any{
		"hypotheses":      len(handoff.Hypotheses),
		"recommendations": len(handoff.Recommendations),
		"targets":         len(handoff.SuggestedValidationTargets),
		"confidence":      handoff.Confidence,
	})
}

func (o *DurableOrchestrator) RecordValidationLoop(ctx context.Context, runID string, record ValidationLoopRecord) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.ValidationLoops = append(run.ValidationLoops, record)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "validation_loop_recorded", map[string]any{
		"iteration":   record.Iteration,
		"target_id":   record.TargetID,
		"tool":        record.Tool,
		"verdict":     record.Verdict,
		"tool_call":   record.ToolCallID,
		"stop_reason": record.StopReason,
	})
}

func (o *DurableOrchestrator) AttachValidationReport(ctx context.Context, runID string, report ValidationActionReport) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		copy := report
		run.Validation = &copy
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "validation_report_attached", map[string]any{
		"mode":        report.Mode,
		"targets":     len(report.Targets),
		"results":     len(report.Results),
		"iterations":  report.Iterations,
		"tool_calls":  report.ToolCalls,
		"stop_reason": report.StopReason,
		"confidence":  report.Confidence,
	})
}

// AttachPolicy records the policy decision for a persisted step.
func (o *DurableOrchestrator) AttachPolicy(ctx context.Context, runID, stepID string, record DurablePolicyRecord) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		step := ensureDurableStep(&run.Steps, stepID)
		step.Policy = &record
	})
}

// AttachApproval records the approval state for a persisted step.
func (o *DurableOrchestrator) AttachApproval(ctx context.Context, runID, stepID string, approval DurableApprovalRecord) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		step := ensureDurableStep(&run.Steps, stepID)
		step.Approval = &approval
	})
}

// RecordVerification persists post-action verification state.
func (o *DurableOrchestrator) RecordVerification(ctx context.Context, runID, stepID string, verification DurableVerificationRecord) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		step := ensureDurableStep(&run.Steps, stepID)
		step.Verification = &verification
		step.Verified = verification.Success
		step.VerificationNote = verification.Note
		step.EvidenceIDs = dedupeStrings(append(step.EvidenceIDs, verification.EvidenceIDs...))
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "verification_recorded", map[string]any{
		"step_id":      stepID,
		"outcome":      verification.Outcome,
		"success":      verification.Success,
		"current_risk": verification.CurrentRisk,
	})
}

// RecordCompensation persists rollback or compensation state.
func (o *DurableOrchestrator) RecordCompensation(ctx context.Context, runID, stepID string, compensation DurableCompensationRecord) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		step := ensureDurableStep(&run.Steps, stepID)
		step.Compensation = &compensation
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "compensation_recorded", map[string]any{
		"step_id": stepID,
		"status":  compensation.Status,
		"error":   compensation.Error,
	})
}

// AttachEvidencePackage persists the generated evidence package location.
func (o *DurableOrchestrator) AttachEvidencePackage(ctx context.Context, runID string, ref DurableEvidencePackageRef) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.EvidencePackage = &ref
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "evidence_package_attached", map[string]any{
		"path": ref.Path,
	})
}

// AttachWorldModel persists the world model snapshot used during workflow execution.
func (o *DurableOrchestrator) AttachWorldModel(ctx context.Context, runID string, world DurableWorldModel) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.WorldModel = &world
	})
}

// AppendMemoryRecord records a durable memory artifact for the run.
func (o *DurableOrchestrator) AppendMemoryRecord(ctx context.Context, runID, path string) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.MemoryRecords = dedupeStrings(append(run.MemoryRecords, path))
	})
}

// SuspendRun marks the run as awaiting an external approval or follow-up event.
func (o *DurableOrchestrator) SuspendRun(ctx context.Context, runID, reason string) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.Status = RunStatusSuspended
		run.Context["suspend_reason"] = stringsOrEmpty(reason)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "run_suspended", map[string]any{"reason": reason})
}

// CompleteRun marks the run as successfully completed.
func (o *DurableOrchestrator) CompleteRun(ctx context.Context, runID string, result any) error {
	run, err := o.store.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	run.Status = RunStatusCompleted
	run.UpdatedAt = time.Now().UTC()
	if result != nil {
		b, _ := json.Marshal(result)
		run.Result = b
	}
	if err := o.store.SaveRun(ctx, run); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "run_completed", nil)
}

// FailRun marks the run as failed.
func (o *DurableOrchestrator) FailRun(ctx context.Context, runID string, runErr error) error {
	run, err := o.store.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	run.Status = RunStatusFailed
	run.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		run.Error = runErr.Error()
	}
	if err := o.store.SaveRun(ctx, run); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "run_failed", map[string]any{"error": run.Error})
}

// LogEvent appends a structured event to the run's history.
func (o *DurableOrchestrator) LogEvent(ctx context.Context, runID, eventType string, payload map[string]any) error {
	if payload == nil {
		payload = make(map[string]any)
	}
	event := WorkflowEvent{
		EventID:   fmt.Sprintf("evt-%s-%d", runID, time.Now().UnixNano()),
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
	o.logger.Debug("workflow event", zap.String("run_id", runID), zap.String("type", eventType))
	return o.store.AppendEvent(ctx, runID, event)
}

// ResumeRun loads a suspended or running run from the store.
func (o *DurableOrchestrator) ResumeRun(ctx context.Context, runID string) (*DurableRun, error) {
	run, err := o.store.LoadRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != RunStatusRunning && run.Status != RunStatusSuspended {
		return nil, fmt.Errorf("cannot resume run in status: %s", run.Status)
	}
	run.LastResumeAt = time.Now().UTC()
	run.Status = RunStatusRunning
	if err := o.store.SaveRun(ctx, run); err != nil {
		return nil, err
	}
	_ = o.LogEvent(ctx, runID, "run_resumed", nil)
	return run, nil
}

// ReplayRun marks a durable run as replayed and returns the updated snapshot.
func (o *DurableOrchestrator) ReplayRun(ctx context.Context, runID string) (*DurableRun, error) {
	run, err := o.store.LoadRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	run.ReplayCount++
	run.UpdatedAt = time.Now().UTC()
	if err := o.store.SaveRun(ctx, run); err != nil {
		return nil, err
	}
	_ = o.LogEvent(ctx, runID, "run_replayed", map[string]any{"replay_count": run.ReplayCount})
	return run, nil
}

// FindToolCallByIdempotency searches recent runs for a prior successful tool invocation.
func (o *DurableOrchestrator) FindToolCallByIdempotency(ctx context.Context, key string) (*WorkflowToolCall, error) {
	key = stringsOrEmpty(key)
	if key == "" {
		return nil, nil
	}
	runs, err := o.store.ListRuns(ctx, "", 512)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		for idx := len(run.ToolCalls) - 1; idx >= 0; idx-- {
			call := run.ToolCalls[idx]
			if call.IdempotencyKey != key {
				continue
			}
			switch call.Status {
			case "success", "dry_run_success", "cached_success":
				copy := call
				return &copy, nil
			}
		}
	}
	return nil, nil
}

// ListRuns returns recent runs from the store.
func (o *DurableOrchestrator) ListRuns(ctx context.Context, workflowType string, limit int) ([]*DurableRun, error) {
	return o.store.ListRuns(ctx, workflowType, limit)
}

func (o *DurableOrchestrator) mutateRun(ctx context.Context, runID string, mutate func(*DurableRun)) error {
	run, err := o.store.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Context == nil {
		run.Context = make(map[string]any)
	}
	if mutate != nil {
		mutate(run)
	}
	run.UpdatedAt = time.Now().UTC()
	return o.store.SaveRun(ctx, run)
}

func durableStepFromPlanStep(stage string, step AgentPlanStep) DurableStepRecord {
	return DurableStepRecord{
		StepID:           step.ID,
		Stage:            stage,
		Title:            step.Title,
		Tool:             step.Tool,
		Query:            cloneStringMap(step.Query),
		Order:            step.Order,
		Iteration:        step.Iteration,
		Required:         step.Required,
		Status:           step.Status,
		ResultSummary:    step.ResultSummary,
		Verified:         step.Verified,
		VerificationNote: step.VerificationNote,
		EvidenceIDs:      append([]string(nil), step.EvidenceIDs...),
		OriginalAction:   cloneActionSpec(step.OriginalAction),
		StartedAt:        step.StartedAt,
		CompletedAt:      step.CompletedAt,
	}
}

func upsertDurableStep(steps *[]DurableStepRecord, record DurableStepRecord) {
	if steps == nil {
		return
	}
	for idx := range *steps {
		if (*steps)[idx].StepID != record.StepID {
			continue
		}
		mergeDurableStep(&(*steps)[idx], record)
		return
	}
	*steps = append(*steps, record)
}

func ensureDurableStep(steps *[]DurableStepRecord, stepID string) *DurableStepRecord {
	for idx := range *steps {
		if (*steps)[idx].StepID == stepID {
			return &(*steps)[idx]
		}
	}
	*steps = append(*steps, DurableStepRecord{StepID: stepID})
	return &(*steps)[len(*steps)-1]
}

func mergeDurableStep(dst *DurableStepRecord, src DurableStepRecord) {
	if dst == nil {
		return
	}
	if src.Stage != "" {
		dst.Stage = src.Stage
	}
	if src.Title != "" {
		dst.Title = src.Title
	}
	if src.Tool != "" {
		dst.Tool = src.Tool
	}
	if src.Query != nil {
		dst.Query = cloneStringMap(src.Query)
	}
	if src.Order != 0 {
		dst.Order = src.Order
	}
	if src.Iteration != 0 {
		dst.Iteration = src.Iteration
	}
	dst.Required = src.Required || dst.Required
	if src.Status != "" {
		dst.Status = src.Status
	}
	if src.ResultSummary != "" {
		dst.ResultSummary = src.ResultSummary
	}
	if src.ErrorMessage != "" {
		dst.ErrorMessage = src.ErrorMessage
	}
	dst.Verified = dst.Verified || src.Verified
	if src.VerificationNote != "" {
		dst.VerificationNote = src.VerificationNote
	}
	if len(src.EvidenceIDs) > 0 {
		dst.EvidenceIDs = dedupeStrings(append(dst.EvidenceIDs, src.EvidenceIDs...))
	}
	if src.LastToolCallID != "" {
		dst.LastToolCallID = src.LastToolCallID
	}
	if src.OriginalAction != nil {
		dst.OriginalAction = cloneActionSpec(src.OriginalAction)
	}
	if src.Policy != nil {
		dst.Policy = src.Policy
	}
	if src.Approval != nil {
		dst.Approval = src.Approval
	}
	if src.Verification != nil {
		dst.Verification = src.Verification
	}
	if src.Compensation != nil {
		dst.Compensation = src.Compensation
	}
	if !src.StartedAt.IsZero() {
		dst.StartedAt = src.StartedAt
	}
	if !src.CompletedAt.IsZero() {
		dst.CompletedAt = src.CompletedAt
	}
}

func cloneActionSpec(in *ActionSpec) *ActionSpec {
	if in == nil {
		return nil
	}
	copy := *in
	copy.Args = append([]string(nil), in.Args...)
	copy.Metadata = cloneStringMap(in.Metadata)
	return &copy
}

func sortRuns(runs []*DurableRun) {
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].UpdatedAt.Equal(runs[j].UpdatedAt) {
			return runs[i].RunID > runs[j].RunID
		}
		return runs[i].UpdatedAt.After(runs[j].UpdatedAt)
	})
}

func stringsOrEmpty(in string) string {
	return in
}
