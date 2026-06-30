package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
	"go.uber.org/zap"
)

type failingReplayStore struct {
	DurableStore
}

func (failingReplayStore) RecordReplay(context.Context, string) (*DurableRun, error) {
	return nil, errors.New("replay audit write failed")
}

func TestBoltDurableStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bolt-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "workflow_runs.db")
	store, err := NewBoltDurableStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	runID := "run-123"
	run := &DurableRun{
		RunID:        runID,
		WorkflowType: "test-workflow",
		Status:       RunStatusRunning,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	// Save
	err = store.SaveRun(ctx, run)
	assert.NoError(t, err)

	// Load
	loaded, err := store.LoadRun(ctx, runID)
	assert.NoError(t, err)
	assert.Equal(t, runID, loaded.RunID)
	assert.Equal(t, "test-workflow", loaded.WorkflowType)

	// Append Event
	event := WorkflowEvent{
		EventID:   "evt-1",
		Type:      "test_event",
		Timestamp: time.Now().UTC(),
		Payload:   map[string]any{"foo": "bar"},
	}
	err = store.AppendEvent(ctx, runID, event)
	assert.NoError(t, err)

	// Reload and check event
	loaded, err = store.LoadRun(ctx, runID)
	assert.NoError(t, err)
	assert.Len(t, loaded.Events, 1)
	assert.Equal(t, "test_event", loaded.Events[0].Type)

	// List
	runs, err := store.ListRuns(ctx, RunListFilter{WorkflowType: "test-workflow", Limit: 10})
	assert.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, runID, runs[0].RunID)
}

func TestReplayRunIsAtomicAcrossBuiltInLocalStores(t *testing.T) {
	tests := []struct {
		name  string
		store func(t *testing.T) DurableStore
	}{
		{
			name: "memory",
			store: func(t *testing.T) DurableStore {
				return NewInMemoryDurableStore()
			},
		},
		{
			name: "bolt",
			store: func(t *testing.T) DurableStore {
				store, err := NewBoltDurableStore(filepath.Join(t.TempDir(), "workflow-runs.db"))
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, store.Close()) })
				return store
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator := NewDurableOrchestrator(tt.store(t), zap.NewNop())
			ctx := context.Background()
			_, err := orchestrator.StartRun(ctx, "run-concurrent-replay", "rca", "collector-a")
			require.NoError(t, err)

			const replayN = 24
			var wg sync.WaitGroup
			errs := make(chan error, replayN)
			for range replayN {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, replayErr := orchestrator.ReplayRun(ctx, "run-concurrent-replay")
					errs <- replayErr
				}()
			}
			wg.Wait()
			close(errs)
			for replayErr := range errs {
				require.NoError(t, replayErr)
			}

			run, err := orchestrator.GetRun(ctx, "run-concurrent-replay")
			require.NoError(t, err)
			require.Equal(t, replayN, run.ReplayCount)
			replayEvents := 0
			for _, event := range run.Events {
				if event.Type == "run_replayed" {
					replayEvents++
				}
			}
			require.Equal(t, replayN, replayEvents)
		})
	}
}

func TestReplayRunPropagatesAtomicStoreFailure(t *testing.T) {
	store := NewInMemoryDurableStore()
	orchestrator := NewDurableOrchestrator(store, zap.NewNop())
	_, err := orchestrator.StartRun(context.Background(), "run-replay-failure", "rca", "collector-a")
	require.NoError(t, err)

	failing := NewDurableOrchestrator(failingReplayStore{DurableStore: store}, zap.NewNop())
	_, err = failing.ReplayRun(context.Background(), "run-replay-failure")
	require.ErrorContains(t, err, "replay audit write failed")

	run, err := orchestrator.GetRun(context.Background(), "run-replay-failure")
	require.NoError(t, err)
	require.Zero(t, run.ReplayCount)
}

func TestBoltDurableStoreLoadRunBackfillsLegacyCanonicalToolCallStatus(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bolt-legacy-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "workflow_runs.db")
	store, err := NewBoltDurableStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	legacy := DurableRun{
		RunID:        "legacy-run",
		WorkflowType: "rca",
		Status:       RunStatusCompleted,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		ToolCalls: []WorkflowToolCall{
			{
				ID:        "tool-1",
				Tool:      ToolMetrics,
				Stage:     "context_gathering",
				Status:    "success",
				StartedAt: time.Now().UTC(),
			},
		},
		Events: []WorkflowEvent{
			{
				EventID:   "evt-legacy",
				Type:      "tool_call_recorded",
				Timestamp: time.Now().UTC(),
				Payload: map[string]any{
					"tool":         string(ToolMetrics),
					"tool_call_id": "tool-1",
					"status":       "success",
				},
			},
			{
				EventID:   "evt-stage",
				Type:      "stage_completed",
				Timestamp: time.Now().UTC(),
				Payload: map[string]any{
					"stage":   "context_gathering",
					"summary": "collected baseline evidence",
				},
			},
		},
	}
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("runs")).Put([]byte(legacy.RunID), raw)
	}))

	loaded, err := store.LoadRun(context.Background(), legacy.RunID)
	require.NoError(t, err)
	require.Len(t, loaded.ToolCalls, 1)
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, loaded.ToolCalls[0].Status)
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, loaded.ToolCalls[0].Outcome)
	require.Equal(t, "success", loaded.ToolCalls[0].InvocationStatus)
	require.Len(t, loaded.Events, 2)
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, loaded.Events[0].Payload["status"])
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, loaded.Events[0].Payload["outcome"])
	require.Equal(t, "success", loaded.Events[0].Payload["invocation_status"])
	require.Equal(t, WorkflowToolOutcomeExecutedSuccess, loaded.Events[1].Payload["status"])
	require.Equal(t, WorkflowToolOutcomeExecutedSuccess, loaded.Events[1].Payload["outcome"])
	require.Equal(t, "completed", loaded.Events[1].Payload["detail_status"])

	var persisted DurableRun
	require.NoError(t, store.db.View(func(tx *bolt.Tx) error {
		payload := tx.Bucket([]byte("runs")).Get([]byte(legacy.RunID))
		return json.Unmarshal(payload, &persisted)
	}))
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, persisted.ToolCalls[0].Status)
	require.Equal(t, "success", persisted.ToolCalls[0].InvocationStatus)
}

func TestDurableOrchestrator_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orch-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "workflow_runs.db")
	store, err := NewBoltDurableStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	logger := zap.NewNop()
	orch := NewDurableOrchestrator(store, logger)

	ctx := context.Background()
	run, err := orch.StartRun(ctx, "run-456", "remediation", "coll-1")
	require.NoError(t, err)
	assert.Equal(t, RunStatusRunning, run.Status)

	err = orch.LogEvent(ctx, "run-456", "step_started", map[string]any{"step": "check_disk"})
	assert.NoError(t, err)

	// Close and Re-open (simulate restart)
	store.Close()
	store2, err := NewBoltDurableStore(dbPath)
	require.NoError(t, err)
	defer store2.Close()

	orch2 := NewDurableOrchestrator(store2, logger)
	resumed, err := orch2.ResumeRun(ctx, "run-456")
	require.NoError(t, err)
	assert.Equal(t, RunStatusRunning, resumed.Status)
	assert.Len(t, resumed.Events, 2)
	assert.Equal(t, "run_started", resumed.Events[0].Type)
	assert.Equal(t, "step_started", resumed.Events[1].Type)
}

func TestDurableOrchestratorRecordStageLifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orch-stage-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "workflow_runs.db")
	store, err := NewBoltDurableStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	orch := NewDurableOrchestrator(store, zap.NewNop())
	ctx := context.Background()

	_, err = orch.StartRun(ctx, "run-stage", "rca", "collector-a")
	require.NoError(t, err)
	require.NoError(t, orch.RecordStageStarted(ctx, "run-stage", "context_gathering"))
	require.NoError(t, orch.RecordStageCompleted(ctx, "run-stage", PipelineStageResult{
		Name:         "context_gathering",
		Status:       WorkflowToolOutcomeExecutedSuccess,
		Outcome:      WorkflowToolOutcomeExecutedSuccess,
		DetailStatus: "completed",
		Summary:      "collected baseline evidence",
	}))
	require.NoError(t, orch.RecordStageFailed(ctx, "run-stage", PipelineStageResult{
		Name:         "llm_analysis",
		Status:       WorkflowToolOutcomeExecutedFailure,
		Outcome:      WorkflowToolOutcomeExecutedFailure,
		DetailStatus: "failed",
		Summary:      "llm output rejected",
	}))

	run, err := orch.GetRun(ctx, "run-stage")
	require.NoError(t, err)
	require.Equal(t, "llm_analysis", run.CurrentStage)
	require.Equal(t, "llm_analysis", run.CurrentStep)
	require.Len(t, run.Events, 4)
	require.Equal(t, "run_started", run.Events[0].Type)
	require.Equal(t, "stage_started", run.Events[1].Type)
	require.Equal(t, "context_gathering", run.Events[1].Payload["stage"])
	require.Equal(t, "stage_completed", run.Events[2].Type)
	require.Equal(t, WorkflowToolOutcomeExecutedSuccess, run.Events[2].Payload["status"])
	require.Equal(t, "stage_failed", run.Events[3].Type)
	require.Equal(t, WorkflowToolOutcomeExecutedFailure, run.Events[3].Payload["status"])
	require.Equal(t, "llm output rejected", run.Events[3].Payload["summary"])
}

func TestDurableOrchestrator_AttachAgentMessagePersistsHistoryAndLatestRefs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orch-messages-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "workflow_runs.db")
	store, err := NewBoltDurableStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	orch := NewDurableOrchestrator(store, zap.NewNop())
	ctx := context.Background()

	run, err := orch.StartRunWithRequest(ctx, "run-msg", "rca", "collector-a", WorkflowRequest{
		WorkflowType: "rca",
		CollectorID:  "collector-a",
		Trigger:      "incident_alert",
	})
	require.NoError(t, err)
	require.Equal(t, RunStatusRunning, run.Status)

	manifestPath := filepath.Join(tmpDir, "history.json")
	analysisRef := AgentMessageRef{
		MessageID:    "msg-run-msg-0001",
		RunID:        "run-msg",
		WorkflowType: "rca",
		FromAgent:    "analysis_agent",
		ToAgent:      "validation_action_agent",
		MessageType:  AgentMessageTypeAnalysisHandoff,
		Sequence:     1,
		CreatedAt:    time.Now().UTC(),
		Path:         filepath.Join(tmpDir, "0001-analysis-handoff.json"),
		ContentHash:  "hash-1",
	}
	requestRef := AgentMessageRef{
		MessageID:         "msg-run-msg-0002",
		RunID:             "run-msg",
		WorkflowType:      "rca",
		FromAgent:         "workflow_runtime",
		ToAgent:           "validation_action_agent",
		MessageType:       AgentMessageTypeValidationRequest,
		Sequence:          2,
		CreatedAt:         time.Now().UTC(),
		ParentMessageID:   analysisRef.MessageID,
		PreviousMessageID: analysisRef.MessageID,
		Path:              filepath.Join(tmpDir, "0002-validation-request.json"),
		ContentHash:       "hash-2",
	}
	resultRef := AgentMessageRef{
		MessageID:         "msg-run-msg-0003",
		RunID:             "run-msg",
		WorkflowType:      "rca",
		FromAgent:         "validation_action_agent",
		ToAgent:           "workflow_runtime",
		MessageType:       AgentMessageTypeValidationResult,
		Sequence:          3,
		CreatedAt:         time.Now().UTC(),
		ParentMessageID:   requestRef.MessageID,
		PreviousMessageID: requestRef.MessageID,
		Path:              filepath.Join(tmpDir, "0003-validation-result.json"),
		ContentHash:       "hash-3",
	}
	manifestRef := &DurableArtifactRef{
		ArtifactID:     "msg-history-run-msg",
		ArtifactType:   "workflow_message_history",
		OwnerType:      "workflow_run",
		OwnerID:        "run-msg",
		RunID:          "run-msg",
		StorageBackend: "filesystem",
		StorageKey:     "messages/run-msg/history.json",
		LocalCachePath: manifestPath,
		Path:           manifestPath,
		ContentType:    "application/json",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	require.NoError(t, orch.AttachAgentMessage(ctx, "run-msg", analysisRef, manifestRef, 10))
	require.NoError(t, orch.AttachAgentMessage(ctx, "run-msg", requestRef, manifestRef, 10))
	require.NoError(t, orch.AttachAgentMessage(ctx, "run-msg", resultRef, manifestRef, 10))

	persisted, err := orch.GetRun(ctx, "run-msg")
	require.NoError(t, err)
	require.Equal(t, manifestPath, persisted.MessageManifestPath)
	require.NotNil(t, persisted.MessageHistoryArtifact)
	require.Equal(t, manifestRef.ArtifactID, persisted.MessageHistoryArtifact.ArtifactID)
	require.Len(t, persisted.MessageHistory, 3)
	require.NotNil(t, persisted.LatestAnalysisHandoffMessage)
	require.NotNil(t, persisted.LatestValidationRequestMessage)
	require.NotNil(t, persisted.LatestValidationResultMessage)
	require.Equal(t, analysisRef.MessageID, persisted.LatestAnalysisHandoffMessage.MessageID)
	require.Equal(t, requestRef.MessageID, persisted.LatestValidationRequestMessage.MessageID)
	require.Equal(t, resultRef.MessageID, persisted.LatestValidationResultMessage.MessageID)

	foundAgentMessageEvent := false
	for _, event := range persisted.Events {
		if event.Type != "agent_message_attached" {
			continue
		}
		foundAgentMessageEvent = true
		break
	}
	require.True(t, foundAgentMessageEvent)
}

func TestDurableOrchestrator_RecordsGovernedExecutionArtifacts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orch-governance-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "workflow_runs.db")
	store, err := NewBoltDurableStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	orch := NewDurableOrchestrator(store, zap.NewNop())
	ctx := context.Background()

	run, err := orch.StartRunWithRequest(ctx, "run-governed", "rca", "collector-a", WorkflowRequest{
		WorkflowType: "rca",
		CollectorID:  "collector-a",
		Trigger:      "incident_alert",
	})
	require.NoError(t, err)
	require.Equal(t, RunStatusRunning, run.Status)

	revision := AgentPlanRevision{
		Iteration: 1,
		Reason:    "initial plan",
		CreatedAt: time.Now().UTC(),
		Steps: []AgentPlanStep{
			{
				ID:       "step-1",
				Title:    "Restart checkout pod",
				Tool:     ToolRemediation,
				Query:    map[string]string{"action": "restart pod", "rollback": "scale back replica set", "validation_category": "probable_containment", "action_contract": encodeValidationActionContract(ValidationActionContract{ID: "contract-step-1", Intent: "restart_workload", Summary: "restart checkout pod", ExecutionCategory: "probable_containment", Target: ActionTargetRef{CollectorID: "collector-a", Scope: "service:checkout"}, Rollback: RollbackContract{Summary: "scale back replica set", Required: true, Reversible: true}})},
				Order:    1,
				Required: true,
				Status:   "planned",
			},
		},
	}
	require.NoError(t, orch.RecordPlanRevision(ctx, "run-governed", revision))
	require.NoError(t, orch.RecordStepState(ctx, "run-governed", "plan_act_verify_loop", revision.Steps[0]))
	require.NoError(t, orch.RecordToolCall(ctx, "run-governed", WorkflowToolCall{
		ID:                "tool-1",
		Tool:              ToolRemediation,
		Stage:             "plan_act_verify_loop",
		Actor:             "workflow-engine",
		Status:            "dry_run_success",
		RiskTag:           "execution",
		ExecutionCategory: "probable_containment",
		ActionIntent:      "restart_workload",
		PolicyVersion:     "workflow-policy/v1",
		ApprovalState:     "pending",
		IdempotencyKey:    "idem-1",
		Policy: ActionPolicyDecision{
			Status:           "pending",
			Reason:           "requires explicit approval",
			ExecutionLevel:   "approval_required",
			RequiresApproval: true,
			DryRunRequired:   true,
		},
		StartedAt:   time.Now().UTC(),
		CompletedAt: time.Now().UTC(),
	}))
	require.NoError(t, orch.AttachPolicy(ctx, "run-governed", "step-1", DurablePolicyRecord{
		Decision:      ActionPolicyDecision{Status: "pending", Reason: "requires explicit approval", ExecutionLevel: "approval_required", RequiresApproval: true},
		PolicyVersion: "workflow-policy/v1",
		RiskTag:       "execution",
		EvaluatedAt:   time.Now().UTC(),
	}))
	require.NoError(t, orch.AttachApproval(ctx, "run-governed", "step-1", DurableApprovalRecord{
		State:       "pending",
		Reason:      "operator approval required",
		RequestedAt: time.Now().UTC(),
	}))
	require.NoError(t, orch.RecordVerification(ctx, "run-governed", "step-1", DurableVerificationRecord{
		Outcome:     "unresolved",
		Success:     false,
		Verdict:     "contradicted",
		Objective:   "verify remediation impact",
		Note:        "error rate did not recover",
		EvidenceIDs: []string{"ev-1"},
		Comparison: &ValidationEffectComparison{
			Comparable: true,
			Incomplete: true,
			MissingData: []string{
				"logs",
			},
			RiskScore: ValidationFloatComparison{
				Available: true,
				Before:    0.72,
				After:     0.81,
				Delta:     0.09,
				Regressed: true,
				Note:      "lower risk is better",
			},
		},
		StartedAt:   time.Now().UTC(),
		CompletedAt: time.Now().UTC(),
	}))
	require.NoError(t, orch.RecordCompensation(ctx, "run-governed", "step-1", DurableCompensationRecord{
		Status:      "executed",
		Summary:     "rolled back restart sequence",
		StartedAt:   time.Now().UTC(),
		CompletedAt: time.Now().UTC(),
	}))
	require.NoError(t, orch.AttachEvidencePackage(ctx, "run-governed", DurableEvidencePackageRef{
		ArtifactID:     "evidence-run-governed",
		ArtifactType:   "evidence_package",
		OwnerType:      "workflow_run",
		OwnerID:        "run-governed",
		RunID:          "run-governed",
		StorageBackend: "filesystem",
		StorageKey:     "evidence/run-governed/package.json",
		LocalCachePath: filepath.Join(tmpDir, "evidence.json"),
		Path:           filepath.Join(tmpDir, "evidence.json"),
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}))
	require.NoError(t, orch.AttachWorldModel(ctx, "run-governed", DurableWorldModel{
		Summary:         "checkout depends on payments from collector-a",
		Scope:           []string{"node/collector-a", "service/checkout"},
		DownstreamNodes: []string{"service/payments"},
		RecentChanges:   []string{"deployment/checkout-v2"},
		Topology: TopologySnapshot{
			GeneratedAt: time.Now().UTC(),
			Nodes: []TopologyNode{
				{ID: "collector-a", Name: "collector-a", Type: "node"},
				{ID: "checkout", Name: "checkout", Type: "service"},
			},
			Edges: []TopologyEdge{
				{Source: "checkout", Target: "payments", Kind: "depends_on"},
			},
			Summary: "service dependency snapshot",
			Source:  "test",
		},
	}))
	require.NoError(t, orch.AttachAnalysisHandoff(ctx, "run-governed", AnalysisHandoff{
		Agent:           "analysis_agent",
		IncidentSummary: "checkout latency spike after rollout",
		CollectorID:     "collector-a",
		ImpactedScope:   []string{"service:checkout"},
		HypothesisPackets: []AnalysisHypothesisHandoff{
			{HypothesisID: "hyp-1", Title: "checkout rollout regression", Confidence: 0.82},
		},
		RankedSuspectedCauses: []string{
			"checkout rollout regression",
		},
		BoundedActionCandidates: []ValidationActionCandidate{
			{ID: "action-1", RecommendationID: "rec-1", Category: "probable_containment", Summary: "rollback checkout-v2"},
		},
		SuggestedValidationTargets: []ValidationTarget{
			{ID: "target-1", Type: ValidationTargetHypothesis, Title: "checkout rollout regression"},
		},
	}))
	require.NoError(t, orch.RecordValidationLoop(ctx, "run-governed", ValidationLoopRecord{
		Iteration:       1,
		TargetID:        "target-1",
		Tool:            ToolDeploymentHistory,
		ToolCallID:      "tool-2",
		ToolReason:      "validate rollout correlation first",
		Observation:     "deployment history matches the incident window",
		Verdict:         ValidationVerdictConfirmed,
		ConfidenceDelta: 0.22,
		Timestamp:       time.Now().UTC(),
	}))
	require.NoError(t, orch.AttachValidationReport(ctx, "run-governed", ValidationActionReport{
		Agent:        "validation_action_agent",
		Mode:         "bounded_react",
		StartedAt:    time.Now().UTC(),
		CompletedAt:  time.Now().UTC(),
		Iterations:   1,
		ToolCalls:    1,
		TargetLimit:  6,
		ReadOnlyOnly: true,
		Targets: []ValidationTarget{
			{ID: "target-1", Type: ValidationTargetHypothesis, Title: "checkout rollout regression"},
		},
		Results: []ValidationTargetResult{
			{TargetID: "target-1", TargetType: ValidationTargetHypothesis, Title: "checkout rollout regression", Verdict: ValidationVerdictConfirmed, Confidence: 0.78},
		},
		ActionCandidates: []ValidationActionCandidate{
			{ID: "action-1", RecommendationID: "rec-1", Category: "probable_containment", Summary: "rollback checkout-v2"},
		},
		SelectedActionContract: &ValidationActionContract{
			ID:                 "contract-step-1",
			Intent:             "restart_workload",
			ActionCategory:     "containment",
			Summary:            "restart checkout pod",
			ExecutionCategory:  "probable_containment",
			ValidationCategory: "probable_containment",
			TargetScope:        "service:checkout",
		},
		Governance: &ValidationGovernanceTrace{
			ActionCandidateID:  "action-1",
			ActionContractID:   "contract-step-1",
			ActionIntent:       "restart_workload",
			ActionCategory:     "containment",
			ExecutionCategory:  "probable_containment",
			ValidationCategory: "probable_containment",
			TargetScope:        "service:checkout",
			ApprovalState:      "pending",
			StepID:             "step-1",
			ToolCallID:         "tool-1",
			StepStatus:         "planned",
		},
		PostActionValidation: &PostActionValidationSummary{
			ActionID:          "contract-step-1",
			Verdict:           ValidationVerdictContradicted,
			Summary:           "post-action state regressed | risk 0.72 -> 0.81 | missing=logs",
			ExecutionCategory: "probable_containment",
			BeforeRisk:        0.72,
			AfterRisk:         0.81,
			Comparison: &ValidationEffectComparison{
				Comparable: true,
				Incomplete: true,
				MissingData: []string{
					"logs",
				},
			},
		},
		Confidence: 0.78,
		StopReason: "support threshold reached",
	}))
	require.NoError(t, orch.AppendMemoryRecord(ctx, "run-governed", DurableArtifactRef{
		ArtifactID:     "memory-run-governed",
		ArtifactType:   "incident_memory_record",
		OwnerType:      "incident_memory",
		OwnerID:        "run-governed",
		RunID:          "run-governed",
		StorageBackend: "filesystem",
		StorageKey:     "incident_memory/run-governed.json",
		LocalCachePath: filepath.Join(tmpDir, "memory.json"),
		Path:           filepath.Join(tmpDir, "memory.json"),
	}))
	require.NoError(t, orch.SuspendRun(ctx, "run-governed", "awaiting operator approval"))

	resumed, err := orch.ResumeRun(ctx, "run-governed")
	require.NoError(t, err)
	require.Equal(t, RunStatusRunning, resumed.Status)

	require.NoError(t, orch.CompleteRun(ctx, "run-governed", map[string]string{"status": "resolved"}))
	replayed, err := orch.ReplayRun(ctx, "run-governed")
	require.NoError(t, err)
	require.Equal(t, 1, replayed.ReplayCount)

	persisted, err := orch.GetRun(ctx, "run-governed")
	require.NoError(t, err)
	require.Equal(t, RunStatusCompleted, persisted.Status)
	require.Equal(t, "collector-a", persisted.Request.CollectorID)
	require.Len(t, persisted.PlanRevisions, 1)
	require.Len(t, persisted.ToolCalls, 1)
	require.Len(t, persisted.Steps, 1)
	require.NotNil(t, persisted.Steps[0].Policy)
	require.NotNil(t, persisted.Steps[0].Approval)
	require.NotNil(t, persisted.Steps[0].Verification)
	require.Equal(t, "contradicted", persisted.Steps[0].Verification.Verdict)
	require.NotNil(t, persisted.Steps[0].Verification.Comparison)
	require.NotNil(t, persisted.Steps[0].Compensation)
	require.NotNil(t, persisted.EvidencePackage)
	require.NotNil(t, persisted.WorldModel)
	require.NotEmpty(t, persisted.MemoryRecordArtifacts)
	require.NotNil(t, persisted.AnalysisHandoff)
	require.NotNil(t, persisted.Validation)
	require.NotEmpty(t, persisted.ValidationLoops)
	require.NotEmpty(t, persisted.AnalysisHandoff.HypothesisPackets)
	require.NotEmpty(t, persisted.Validation.ActionCandidates)
	require.NotNil(t, persisted.Validation.SelectedActionContract)
	require.NotNil(t, persisted.Validation.Governance)
	require.NotNil(t, persisted.Validation.PostActionValidation)
	require.NotEmpty(t, persisted.MemoryRecords)
	require.Equal(t, WorkflowToolOutcomePlannedOnly, persisted.ToolCalls[0].Outcome)
	require.Equal(t, "probable_containment", persisted.ToolCalls[0].ExecutionCategory)
	require.Equal(t, "restart_workload", persisted.ToolCalls[0].ActionIntent)
	require.Equal(t, "probable_containment", persisted.Steps[0].ExecutionCategory)
	require.NotNil(t, persisted.Steps[0].ActionContract)
	require.Equal(t, "contract-step-1", persisted.Validation.Governance.ActionContractID)
	require.Equal(t, "step-1", persisted.Validation.Governance.StepID)
	require.GreaterOrEqual(t, len(persisted.Events), 11)
}

func TestDurableOrchestratorFindToolCallByIdempotencyReusesProposalOnlyRemediation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orch-idempotency-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "workflow_runs.db")
	store, err := NewBoltDurableStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	orch := NewDurableOrchestrator(store, zap.NewNop())
	ctx := context.Background()

	_, err = orch.StartRun(ctx, "run-idem", "rca", "collector-a")
	require.NoError(t, err)

	call := WorkflowToolCall{
		ID:             "call-1",
		Tool:           ToolRemediation,
		Stage:          "guarded_execution_plan",
		IdempotencyKey: "idem-1",
		Status:         "proposal_only",
		Summary:        "proposal-only remediation for restart checkout",
		Policy: ActionPolicyDecision{
			Status:         "proposal_only",
			Reason:         "impacting actuator stays proposal-only until deterministic policy explicitly enables execution",
			ExecutionLevel: "suggest_only",
			SafetyTier:     ActuatorSafetyTierImpacting,
			ProposalOnly:   true,
		},
		ResultKind:    "remediation",
		ResultPayload: `{"mode":"proposal_only","proposal_only":true}`,
		StartedAt:     time.Now().UTC(),
		CompletedAt:   time.Now().UTC(),
	}
	require.NoError(t, orch.RecordToolCall(ctx, "run-idem", call))

	found, err := orch.FindToolCallByIdempotency(ctx, "idem-1")
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, WorkflowToolOutcomeProposedOnly, found.Status)
	require.Equal(t, "proposal_only", found.InvocationStatus)
	require.Equal(t, call.ID, found.ID)
}
