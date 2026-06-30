package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	artifactstore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/artifacts"
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
	Outcome      string                      `json:"outcome"`
	Verdict      string                      `json:"verdict,omitempty"`
	Success      bool                        `json:"success"`
	Objective    string                      `json:"objective,omitempty"`
	Note         string                      `json:"note,omitempty"`
	Window       string                      `json:"window,omitempty"`
	PreviousRisk float64                     `json:"previous_risk,omitempty"`
	CurrentRisk  float64                     `json:"current_risk,omitempty"`
	EvidenceIDs  []string                    `json:"evidence_ids,omitempty"`
	Comparison   *ValidationEffectComparison `json:"comparison,omitempty"`
	StartedAt    time.Time                   `json:"started_at,omitempty"`
	CompletedAt  time.Time                   `json:"completed_at,omitempty"`
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
	StepID            string                     `json:"step_id"`
	Stage             string                     `json:"stage,omitempty"`
	Title             string                     `json:"title,omitempty"`
	Tool              ToolName                   `json:"tool,omitempty"`
	Query             map[string]string          `json:"query,omitempty"`
	Order             int                        `json:"order,omitempty"`
	Iteration         int                        `json:"iteration,omitempty"`
	Required          bool                       `json:"required"`
	Status            string                     `json:"status"`
	ResultSummary     string                     `json:"result_summary,omitempty"`
	ErrorMessage      string                     `json:"error_message,omitempty"`
	Verified          bool                       `json:"verified"`
	VerificationNote  string                     `json:"verification_note,omitempty"`
	EvidenceIDs       []string                   `json:"evidence_ids,omitempty"`
	LastToolCallID    string                     `json:"last_tool_call_id,omitempty"`
	ExecutionCategory string                     `json:"execution_category,omitempty"`
	OriginalAction    *ActionSpec                `json:"original_action,omitempty"`
	ActionContract    *ValidationActionContract  `json:"action_contract,omitempty"`
	Policy            *DurablePolicyRecord       `json:"policy,omitempty"`
	Approval          *DurableApprovalRecord     `json:"approval,omitempty"`
	Verification      *DurableVerificationRecord `json:"verification,omitempty"`
	Compensation      *DurableCompensationRecord `json:"compensation,omitempty"`
	StartedAt         time.Time                  `json:"started_at,omitempty"`
	CompletedAt       time.Time                  `json:"completed_at,omitempty"`
}

// DurableArtifactRef is the shared durable reference for workflow artifacts.
// Path remains as a legacy local-cache hint for filesystem payload backends.
type DurableArtifactRef struct {
	ArtifactID       string            `json:"artifact_id"`
	ArtifactType     string            `json:"artifact_type"`
	OwnerType        string            `json:"owner_type,omitempty"`
	OwnerID          string            `json:"owner_id,omitempty"`
	RunID            string            `json:"run_id,omitempty"`
	CollectorID      string            `json:"collector_id,omitempty"`
	ClusterName      string            `json:"cluster_name,omitempty"`
	StorageBackend   string            `json:"storage_backend"`
	StorageContainer string            `json:"storage_container,omitempty"`
	StorageKey       string            `json:"storage_key"`
	ContentType      string            `json:"content_type,omitempty"`
	ContentEncoding  string            `json:"content_encoding,omitempty"`
	SizeBytes        int64             `json:"size_bytes,omitempty"`
	Checksum         string            `json:"checksum,omitempty"`
	RetentionClass   string            `json:"retention_class,omitempty"`
	ExpiresAt        time.Time         `json:"expires_at,omitempty"`
	DeleteAfter      time.Time         `json:"delete_after,omitempty"`
	Pinned           bool              `json:"pinned,omitempty"`
	GCState          string            `json:"gc_state,omitempty"`
	LocalCachePath   string            `json:"local_cache_path,omitempty"`
	Path             string            `json:"path,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	CreatedAt        time.Time         `json:"created_at,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at,omitempty"`
}

type DurableEvidencePackageRef = DurableArtifactRef

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
	RunID                             string                     `json:"run_id"`
	WorkflowType                      string                     `json:"workflow_type"`
	CollectorID                       string                     `json:"collector_id"`
	Request                           WorkflowRequest            `json:"request"`
	Status                            RunStatus                  `json:"status"`
	CurrentStep                       string                     `json:"current_step"`
	CurrentStage                      string                     `json:"current_stage,omitempty"`
	Events                            []WorkflowEvent            `json:"events"`
	ToolCalls                         []WorkflowToolCall         `json:"tool_calls,omitempty"`
	PlanRevisions                     []AgentPlanRevision        `json:"plan_revisions,omitempty"`
	Steps                             []DurableStepRecord        `json:"steps,omitempty"`
	AnalysisHandoff                   *AnalysisHandoff           `json:"analysis_handoff,omitempty"`
	Validation                        *ValidationActionReport    `json:"validation,omitempty"`
	ValidationLoops                   []ValidationLoopRecord     `json:"validation_loops,omitempty"`
	AdaptiveRuntime                   *AdaptiveRuntimeState      `json:"adaptive_runtime,omitempty"`
	AdaptiveDialogue                  []AdaptiveDialogueTurn     `json:"adaptive_dialogue,omitempty"`
	AdaptiveToolDecisions             []AdaptiveToolDecision     `json:"adaptive_tool_decisions,omitempty"`
	AdaptiveArtifacts                 []AdaptiveArtifact         `json:"adaptive_artifacts,omitempty"`
	MessageManifestPath               string                     `json:"message_manifest_path,omitempty"`
	MessageHistoryArtifact            *DurableArtifactRef        `json:"message_history_artifact,omitempty"`
	MessageHistory                    []AgentMessageRef          `json:"message_history,omitempty"`
	ArtifactManifestPath              string                     `json:"artifact_manifest_path,omitempty"`
	ArtifactManifestArtifact          *DurableArtifactRef        `json:"artifact_manifest_artifact,omitempty"`
	LatestAnalysisHandoffMessage      *AgentMessageRef           `json:"latest_analysis_handoff_message,omitempty"`
	LatestValidationRequestMessage    *AgentMessageRef           `json:"latest_validation_request_message,omitempty"`
	LatestValidationResultMessage     *AgentMessageRef           `json:"latest_validation_result_message,omitempty"`
	LatestActionDecisionMessage       *AgentMessageRef           `json:"latest_action_decision_message,omitempty"`
	LatestPostActionValidationMessage *AgentMessageRef           `json:"latest_post_action_validation_message,omitempty"`
	LatestCompensationMessage         *AgentMessageRef           `json:"latest_compensation_message,omitempty"`
	EvidencePackage                   *DurableEvidencePackageRef `json:"evidence_package,omitempty"`
	WorldModel                        *DurableWorldModel         `json:"world_model,omitempty"`
	MemoryRecords                     []string                   `json:"memory_records,omitempty"`
	MemoryRecordArtifacts             []DurableArtifactRef       `json:"memory_record_artifacts,omitempty"`
	ReplayCount                       int                        `json:"replay_count,omitempty"`
	LastResumeAt                      time.Time                  `json:"last_resume_at,omitempty"`
	CreatedAt                         time.Time                  `json:"created_at"`
	UpdatedAt                         time.Time                  `json:"updated_at"`
	Result                            []byte                     `json:"result,omitempty"`
	Error                             string                     `json:"error,omitempty"`
	Context                           map[string]any             `json:"context,omitempty"`
}

func normalizeDurableRun(run *DurableRun) bool {
	if run == nil {
		return false
	}
	changed := false
	if run.MessageHistoryArtifact != nil && strings.TrimSpace(run.MessageManifestPath) != "" && strings.TrimSpace(run.MessageHistoryArtifact.LocalCachePath) == "" {
		run.MessageHistoryArtifact.LocalCachePath = run.MessageManifestPath
		run.MessageHistoryArtifact.Path = run.MessageManifestPath
		changed = true
	}
	for idx := range run.ToolCalls {
		if normalizeWorkflowToolCall(&run.ToolCalls[idx]) {
			changed = true
		}
	}
	for idx := range run.AdaptiveArtifacts {
		if normalizeAdaptiveArtifact(&run.AdaptiveArtifacts[idx]) {
			changed = true
		}
	}
	if run.AdaptiveRuntime != nil && strings.TrimSpace(run.AdaptiveRuntime.SchemaVersion) == "" {
		run.AdaptiveRuntime.SchemaVersion = adaptiveRuntimeSchemaVersion
		changed = true
	}
	for idx := range run.Events {
		if normalizeWorkflowEvent(&run.Events[idx], run) {
			changed = true
		}
	}
	return changed
}

func normalizeAdaptiveArtifact(artifact *AdaptiveArtifact) bool {
	if artifact == nil {
		return false
	}
	changed := false
	if strings.TrimSpace(artifact.SchemaVersion) == "" {
		artifact.SchemaVersion = adaptiveRuntimeSchemaVersion
		changed = true
	}
	if strings.TrimSpace(artifact.Version) == "" {
		artifact.Version = "v1"
		changed = true
	}
	if strings.TrimSpace(artifact.Status) == "" {
		artifact.Status = "recorded"
		changed = true
	}
	if strings.TrimSpace(artifact.ReplaySemantics) == "" {
		if artifact.Kind == WorkflowArtifactExecutionIntent {
			artifact.ReplaySemantics = "intent_only"
			artifact.Replayable = false
		} else {
			artifact.ReplaySemantics = "metadata reconstruction from adaptive runtime state, durable tool calls, and tool contracts; no tool execution"
			artifact.Replayable = true
		}
		changed = true
	}
	return changed
}

func normalizeWorkflowEvent(event *WorkflowEvent, run *DurableRun) bool {
	if event == nil {
		return false
	}
	if event.Payload == nil {
		event.Payload = make(map[string]any)
	}
	changed := false
	switch event.Type {
	case "tool_call_recorded":
		if normalizeToolCallRecordedEvent(event, run) {
			changed = true
		}
	case "stage_completed":
		if normalizeWorkflowEventStatus(event, WorkflowToolOutcomeExecutedSuccess, "completed") {
			changed = true
		}
	case "run_completed":
		if normalizeWorkflowEventStatus(event, WorkflowToolOutcomeExecutedSuccess, "completed") {
			changed = true
		}
	case "run_failed":
		if normalizeWorkflowEventStatus(event, WorkflowToolOutcomeExecutedFailure, "failed") {
			changed = true
		}
	}
	return changed
}

func normalizeToolCallRecordedEvent(event *WorkflowEvent, run *DurableRun) bool {
	if event == nil {
		return false
	}
	changed := false
	var call *WorkflowToolCall
	if run != nil {
		if id := strings.TrimSpace(anyString(event.Payload["tool_call_id"])); id != "" {
			for idx := range run.ToolCalls {
				if run.ToolCalls[idx].ID == id {
					call = &run.ToolCalls[idx]
					break
				}
			}
		}
	}
	status := ""
	invocation := strings.TrimSpace(anyString(event.Payload["invocation_status"]))
	if call != nil {
		status = workflowToolStoredOutcome(*call)
		invocation = workflowToolInvocationStatus(*call)
	} else {
		tool := ToolName(strings.TrimSpace(anyString(event.Payload["tool"])))
		rawStatus := firstNonEmpty(invocation, strings.TrimSpace(anyString(event.Payload["status"])))
		status = workflowToolCallOutcome(tool, rawStatus, nil, ActionPolicyDecision{})
	}
	if status != "" && eventPayloadString(event.Payload, "status") != status {
		event.Payload["status"] = status
		changed = true
	}
	if status != "" && eventPayloadString(event.Payload, "outcome") != status {
		event.Payload["outcome"] = status
		changed = true
	}
	if invocation != "" && eventPayloadString(event.Payload, "invocation_status") != invocation {
		event.Payload["invocation_status"] = invocation
		changed = true
	}
	return changed
}

func normalizeWorkflowEventStatus(event *WorkflowEvent, canonical, detail string) bool {
	if event == nil {
		return false
	}
	changed := false
	if canonical != "" && eventPayloadString(event.Payload, "status") != canonical {
		event.Payload["status"] = canonical
		changed = true
	}
	if canonical != "" && eventPayloadString(event.Payload, "outcome") != canonical {
		event.Payload["outcome"] = canonical
		changed = true
	}
	if detail != "" && eventPayloadString(event.Payload, "detail_status") != detail {
		event.Payload["detail_status"] = detail
		changed = true
	}
	return changed
}

func eventPayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	return strings.TrimSpace(anyString(payload[key]))
}

func anyString(v any) string {
	if v == nil {
		return ""
	}
	switch typed := v.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// DurableStore defines the persistence contract for runs.
type DurableStore interface {
	SaveRun(ctx context.Context, run *DurableRun) error
	LoadRun(ctx context.Context, runID string) (*DurableRun, error)
	AppendEvent(ctx context.Context, runID string, event WorkflowEvent) error
	RecordReplay(ctx context.Context, runID string) (*DurableRun, error)
	ListRuns(ctx context.Context, filter RunListFilter) ([]*DurableRun, error)
	FindReusableToolCallByIdempotency(ctx context.Context, key string) (*WorkflowToolCall, error)
}

// RunListFilter narrows run listing without forcing callers to scan the entire store.
type RunListFilter struct {
	WorkflowType string
	CollectorID  string
	Status       RunStatus
	Since        time.Time
	Until        time.Time
	Limit        int
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
	out := cloneDurableRun(run)
	if out == nil {
		return fmt.Errorf("run cannot be nil")
	}
	normalizeDurableRun(out)
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
	out := cloneDurableRun(run)
	if normalizeDurableRun(out) {
		s.runs[runID] = out
	}
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
	normalizeDurableRun(run)
	return nil
}

func (s *InMemoryDurableStore) RecordReplay(_ context.Context, runID string) (*DurableRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run not found: %s", runID)
	}
	recordRunReplay(run)
	return cloneDurableRun(run), nil
}

func (s *InMemoryDurableStore) ListRuns(_ context.Context, filter RunListFilter) ([]*DurableRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*DurableRun, 0, len(s.runs))
	for _, run := range s.runs {
		if !runMatchesFilter(run, filter) {
			continue
		}
		cloned := cloneDurableRun(run)
		if normalizeDurableRun(cloned) {
			s.runs[cloned.RunID] = cloned
		}
		out = append(out, cloned)
	}
	sortRuns(out)
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *InMemoryDurableStore) FindReusableToolCallByIdempotency(_ context.Context, key string) (*WorkflowToolCall, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	bestRun := latestMatchingToolCallRun(s.runs, key)
	if bestRun == nil {
		return nil, nil
	}
	for idx := len(bestRun.ToolCalls) - 1; idx >= 0; idx-- {
		call := bestRun.ToolCalls[idx]
		if call.IdempotencyKey != key {
			continue
		}
		if workflowToolCallReusable(call) {
			copy := call
			return &copy, nil
		}
	}
	return nil, nil
}

// BoltDurableStore is a BoltDB-backed implementation of DurableStore. It keeps
// workflow state on the controller data path for local-first restart recovery.
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
	toStore := *run
	normalizeDurableRun(&toStore)
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("runs"))
		payload, err := json.Marshal(&toStore)
		if err != nil {
			return err
		}
		return b.Put([]byte(toStore.RunID), payload)
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
	if normalizeDurableRun(&run) {
		if saveErr := s.SaveRun(context.Background(), &run); saveErr != nil {
			return nil, saveErr
		}
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
		normalizeDurableRun(&run)
		payload, err := json.Marshal(run)
		if err != nil {
			return err
		}
		return b.Put([]byte(runID), payload)
	})
}

func (s *BoltDurableStore) RecordReplay(_ context.Context, runID string) (*DurableRun, error) {
	var replayed *DurableRun
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("runs"))
		v := b.Get([]byte(runID))
		if v == nil {
			return fmt.Errorf("run not found: %s", runID)
		}
		var run DurableRun
		if err := json.Unmarshal(v, &run); err != nil {
			return err
		}
		recordRunReplay(&run)
		payload, err := json.Marshal(run)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(runID), payload); err != nil {
			return err
		}
		replayed = cloneDurableRun(&run)
		return nil
	})
	return replayed, err
}

func (s *BoltDurableStore) ListRuns(_ context.Context, filter RunListFilter) ([]*DurableRun, error) {
	var (
		out     []*DurableRun
		changed []*DurableRun
	)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("runs"))
		return b.ForEach(func(_, v []byte) error {
			var run DurableRun
			if err := json.Unmarshal(v, &run); err != nil {
				return nil
			}
			if normalizeDurableRun(&run) {
				copy := run
				changed = append(changed, &copy)
			}
			if !runMatchesFilter(&run, filter) {
				return nil
			}
			out = append(out, &run)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	for _, run := range changed {
		if saveErr := s.SaveRun(context.Background(), run); saveErr != nil {
			return nil, saveErr
		}
	}
	sortRuns(out)
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *BoltDurableStore) FindReusableToolCallByIdempotency(ctx context.Context, key string) (*WorkflowToolCall, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	runs, err := s.ListRuns(ctx, RunListFilter{Limit: 512})
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		for idx := len(run.ToolCalls) - 1; idx >= 0; idx-- {
			call := run.ToolCalls[idx]
			if call.IdempotencyKey != key {
				continue
			}
			if workflowToolCallReusable(call) {
				copy := call
				return &copy, nil
			}
		}
	}
	return nil, nil
}

func (s *BoltDurableStore) Close() error {
	return s.db.Close()
}

func cloneDurableRun(run *DurableRun) *DurableRun {
	if run == nil {
		return nil
	}
	out := &DurableRun{}
	b, _ := json.Marshal(run)
	_ = json.Unmarshal(b, out)
	return out
}

func recordRunReplay(run *DurableRun) {
	run.ReplayCount++
	run.UpdatedAt = time.Now().UTC()
	run.Events = append(run.Events, newWorkflowEvent(run.RunID, "run_replayed", map[string]any{
		"replay_count": run.ReplayCount,
		"semantics":    "metadata_only",
	}))
	normalizeDurableRun(run)
}

func durableArtifactRefFromRecord(record *artifactstore.Record) DurableArtifactRef {
	if record == nil {
		return DurableArtifactRef{}
	}
	return DurableArtifactRef{
		ArtifactID:       record.ArtifactID,
		ArtifactType:     string(record.ArtifactType),
		OwnerType:        string(record.OwnerType),
		OwnerID:          record.OwnerID,
		RunID:            record.RunID,
		CollectorID:      record.CollectorID,
		ClusterName:      record.ClusterName,
		StorageBackend:   record.StorageBackend,
		StorageContainer: record.StorageContainer,
		StorageKey:       record.StorageKey,
		ContentType:      record.ContentType,
		ContentEncoding:  record.ContentEncoding,
		SizeBytes:        record.SizeBytes,
		Checksum:         record.Checksum,
		RetentionClass:   record.RetentionClass,
		ExpiresAt:        record.ExpiresAt,
		DeleteAfter:      record.DeleteAfter,
		Pinned:           record.Pinned,
		GCState:          record.GCState,
		LocalCachePath:   record.LocalCachePath,
		Path:             record.LocalCachePath,
		Metadata:         cloneStringMap(record.Metadata),
		CreatedAt:        record.CreatedAt,
		UpdatedAt:        record.UpdatedAt,
	}
}

func cloneDurableArtifactRef(ref *DurableArtifactRef) *DurableArtifactRef {
	if ref == nil {
		return nil
	}
	copy := *ref
	copy.Metadata = cloneStringMap(ref.Metadata)
	return &copy
}

func runMatchesFilter(run *DurableRun, filter RunListFilter) bool {
	if run == nil {
		return false
	}
	if filter.WorkflowType != "" && run.WorkflowType != filter.WorkflowType {
		return false
	}
	if filter.CollectorID != "" && run.CollectorID != filter.CollectorID {
		return false
	}
	if filter.Status != "" && run.Status != filter.Status {
		return false
	}
	if !filter.Since.IsZero() && run.UpdatedAt.Before(filter.Since) {
		return false
	}
	if !filter.Until.IsZero() && run.CreatedAt.After(filter.Until) {
		return false
	}
	return true
}

func latestMatchingToolCallRun(runs map[string]*DurableRun, key string) *DurableRun {
	var best *DurableRun
	for _, run := range runs {
		if run == nil {
			continue
		}
		found := false
		for _, call := range run.ToolCalls {
			if call.IdempotencyKey == key {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if best == nil || run.UpdatedAt.After(best.UpdatedAt) {
			best = run
		}
	}
	return best
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
		RunID:                 runID,
		WorkflowType:          workflowType,
		CollectorID:           collectorID,
		Request:               req,
		Status:                RunStatusRunning,
		CurrentStep:           "init",
		CurrentStage:          "init",
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
		Events:                []WorkflowEvent{},
		ToolCalls:             []WorkflowToolCall{},
		PlanRevisions:         []AgentPlanRevision{},
		Steps:                 []DurableStepRecord{},
		ValidationLoops:       []ValidationLoopRecord{},
		AdaptiveDialogue:      []AdaptiveDialogueTurn{},
		AdaptiveToolDecisions: []AdaptiveToolDecision{},
		AdaptiveArtifacts:     []AdaptiveArtifact{},
		MessageHistory:        []AgentMessageRef{},
		Context:               make(map[string]any),
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

func (o *DurableOrchestrator) RecordStageStarted(ctx context.Context, runID, stageName string) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.CurrentStep = stageName
		run.CurrentStage = stageName
		appendWorkflowEvent(run, newWorkflowEvent(runID, "stage_started", map[string]any{
			"stage": stageName,
		}))
	})
}

func (o *DurableOrchestrator) RecordStageCompleted(ctx context.Context, runID string, stage PipelineStageResult) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.CurrentStep = stage.Name
		run.CurrentStage = stage.Name
		appendWorkflowEvent(run, newWorkflowEvent(runID, "stage_completed", map[string]any{
			"stage":         stage.Name,
			"status":        stage.Status,
			"outcome":       stage.Outcome,
			"detail_status": stage.DetailStatus,
			"summary":       stage.Summary,
		}))
	})
}

func (o *DurableOrchestrator) RecordStageFailed(ctx context.Context, runID string, stage PipelineStageResult) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.CurrentStep = stage.Name
		run.CurrentStage = stage.Name
		appendWorkflowEvent(run, newWorkflowEvent(runID, "stage_failed", map[string]any{
			"stage":         stage.Name,
			"status":        stage.Status,
			"outcome":       stage.Outcome,
			"detail_status": stage.DetailStatus,
			"summary":       stage.Summary,
		}))
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
	normalizeWorkflowToolCall(&call)
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.ToolCalls = append(run.ToolCalls, call)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "tool_call_recorded", map[string]any{
		"tool":               call.Tool,
		"status":             call.Status,
		"outcome":            workflowToolStoredOutcome(call),
		"invocation_status":  workflowToolInvocationStatus(call),
		"idempotency_key":    call.IdempotencyKey,
		"approval_state":     call.ApprovalState,
		"policy_status":      call.Policy.Status,
		"policy_version":     call.PolicyVersion,
		"risk_tag":           call.RiskTag,
		"execution_category": call.ExecutionCategory,
		"action_intent":      call.ActionIntent,
		"tool_call_id":       call.ID,
		"tool_call_stage":    call.Stage,
		"tool_call_actor":    call.Actor,
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

func (o *DurableOrchestrator) AttachAgentMessage(ctx context.Context, runID string, ref AgentMessageRef, manifestRef *DurableArtifactRef, historyLimit int) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		if manifestRef != nil {
			copy := *manifestRef
			run.MessageHistoryArtifact = &copy
			run.MessageManifestPath = firstNonEmpty(copy.LocalCachePath, copy.Path, run.MessageManifestPath)
		}
		run.MessageHistory = appendAgentMessageRef(run.MessageHistory, ref, historyLimit)
		switch ref.MessageType {
		case AgentMessageTypeAnalysisHandoff:
			run.LatestAnalysisHandoffMessage = cloneAgentMessageRef(&ref)
		case AgentMessageTypeValidationRequest:
			run.LatestValidationRequestMessage = cloneAgentMessageRef(&ref)
		case AgentMessageTypeValidationResult:
			run.LatestValidationResultMessage = cloneAgentMessageRef(&ref)
		case AgentMessageTypeActionDecision:
			run.LatestActionDecisionMessage = cloneAgentMessageRef(&ref)
		case AgentMessageTypePostActionValidation:
			run.LatestPostActionValidationMessage = cloneAgentMessageRef(&ref)
		case AgentMessageTypeCompensationResult:
			run.LatestCompensationMessage = cloneAgentMessageRef(&ref)
		}
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "agent_message_attached", map[string]any{
		"message_id":          ref.MessageID,
		"message_type":        ref.MessageType,
		"sequence":            ref.Sequence,
		"message_path":        firstNonEmpty(ref.LocalCachePath, ref.Path),
		"message_artifact_id": ref.ArtifactID,
		"message_storage_key": ref.StorageKey,
		"manifest_artifact_id": func() string {
			if manifestRef != nil {
				return manifestRef.ArtifactID
			}
			return ""
		}(),
		"manifest_storage_key": func() string {
			if manifestRef != nil {
				return manifestRef.StorageKey
			}
			return ""
		}(),
		"parent_id":    ref.ParentMessageID,
		"previous_id":  ref.PreviousMessageID,
		"content_hash": ref.ContentHash,
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
		"verdict":      verification.Verdict,
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
		"artifact_id": ref.ArtifactID,
		"storage_key": ref.StorageKey,
		"path":        firstNonEmpty(ref.LocalCachePath, ref.Path),
	})
}

// AttachArtifactManifest persists the standalone workflow artifact manifest location.
func (o *DurableOrchestrator) AttachArtifactManifest(ctx context.Context, runID string, ref DurableArtifactRef) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.ArtifactManifestArtifact = &ref
		run.ArtifactManifestPath = firstNonEmpty(ref.LocalCachePath, ref.Path)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "artifact_manifest_attached", map[string]any{
		"artifact_id": ref.ArtifactID,
		"storage_key": ref.StorageKey,
		"path":        firstNonEmpty(ref.LocalCachePath, ref.Path),
	})
}

// AttachWorldModel persists the world model snapshot used during workflow execution.
func (o *DurableOrchestrator) AttachWorldModel(ctx context.Context, runID string, world DurableWorldModel) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.WorldModel = &world
	})
}

// AppendMemoryRecord records a durable memory artifact for the run.
func (o *DurableOrchestrator) AppendMemoryRecord(ctx context.Context, runID string, ref DurableArtifactRef) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		if ref.ArtifactID != "" {
			run.MemoryRecords = dedupeStrings(append(run.MemoryRecords, ref.ArtifactID))
		}
		run.MemoryRecordArtifacts = appendUniqueMemoryArtifact(run.MemoryRecordArtifacts, ref)
	})
}

func appendUniqueMemoryArtifact(base []DurableArtifactRef, ref DurableArtifactRef) []DurableArtifactRef {
	if strings.TrimSpace(ref.ArtifactID) == "" {
		return base
	}
	for idx := range base {
		if base[idx].ArtifactID == ref.ArtifactID {
			base[idx] = ref
			return base
		}
	}
	return append(base, ref)
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
	event := newWorkflowEvent(runID, eventType, payload)
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

// ReplayRun records a metadata-only replay marker and returns the updated snapshot.
// It does not execute tools, actions, or stored execution intents.
func (o *DurableOrchestrator) ReplayRun(ctx context.Context, runID string) (*DurableRun, error) {
	return o.store.RecordReplay(ctx, runID)
}

// FindToolCallByIdempotency searches recent runs for a prior successful tool invocation.
func (o *DurableOrchestrator) FindToolCallByIdempotency(ctx context.Context, key string) (*WorkflowToolCall, error) {
	key = stringsOrEmpty(key)
	if key == "" {
		return nil, nil
	}
	return o.store.FindReusableToolCallByIdempotency(ctx, key)
}

// ListRuns returns recent runs from the store.
func (o *DurableOrchestrator) ListRuns(ctx context.Context, workflowType string, limit int) ([]*DurableRun, error) {
	return o.store.ListRuns(ctx, RunListFilter{
		WorkflowType: strings.TrimSpace(workflowType),
		Limit:        limit,
	})
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
	contract, _ := decodeValidationActionContract(step.Query["action_contract"])
	return DurableStepRecord{
		StepID:            step.ID,
		Stage:             stage,
		Title:             step.Title,
		Tool:              step.Tool,
		Query:             cloneStringMap(step.Query),
		Order:             step.Order,
		Iteration:         step.Iteration,
		Required:          step.Required,
		Status:            step.Status,
		ResultSummary:     step.ResultSummary,
		Verified:          step.Verified,
		VerificationNote:  step.VerificationNote,
		EvidenceIDs:       append([]string(nil), step.EvidenceIDs...),
		ExecutionCategory: executionCategoryFromToolQuery(step.Query),
		OriginalAction:    cloneActionSpec(step.OriginalAction),
		ActionContract:    cloneValidationActionContract(contract),
		StartedAt:         step.StartedAt,
		CompletedAt:       step.CompletedAt,
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
	if src.ExecutionCategory != "" {
		dst.ExecutionCategory = src.ExecutionCategory
	}
	if src.OriginalAction != nil {
		dst.OriginalAction = cloneActionSpec(src.OriginalAction)
	}
	if src.ActionContract != nil {
		dst.ActionContract = cloneValidationActionContract(src.ActionContract)
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

func newWorkflowEvent(runID, eventType string, payload map[string]any) WorkflowEvent {
	if payload == nil {
		payload = make(map[string]any)
	}
	return WorkflowEvent{
		EventID:   fmt.Sprintf("evt-%s-%d", runID, time.Now().UnixNano()),
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func appendWorkflowEvent(run *DurableRun, event WorkflowEvent) {
	if run == nil {
		return
	}
	run.Events = append(run.Events, event)
}
