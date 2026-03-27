package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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
	runs, err := store.ListRuns(ctx, "test-workflow", 10)
	assert.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, runID, runs[0].RunID)
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
				Query:    map[string]string{"action": "restart pod", "rollback": "scale back replica set"},
				Order:    1,
				Required: true,
				Status:   "planned",
			},
		},
	}
	require.NoError(t, orch.RecordPlanRevision(ctx, "run-governed", revision))
	require.NoError(t, orch.RecordStepState(ctx, "run-governed", "plan_act_verify_loop", revision.Steps[0]))
	require.NoError(t, orch.RecordToolCall(ctx, "run-governed", WorkflowToolCall{
		ID:             "tool-1",
		Tool:           ToolRemediation,
		Stage:          "plan_act_verify_loop",
		Actor:          "workflow-engine",
		Status:         "dry_run_success",
		RiskTag:        "execution",
		PolicyVersion:  "workflow-policy/v1",
		ApprovalState:  "pending",
		IdempotencyKey: "idem-1",
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
		Objective:   "verify remediation impact",
		Note:        "error rate did not recover",
		EvidenceIDs: []string{"ev-1"},
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
		Path:        filepath.Join(tmpDir, "evidence.json"),
		GeneratedAt: time.Now().UTC(),
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
		RankedSuspectedCauses: []string{
			"checkout rollout regression",
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
		Confidence: 0.78,
		StopReason: "support threshold reached",
	}))
	require.NoError(t, orch.AppendMemoryRecord(ctx, "run-governed", filepath.Join(tmpDir, "memory.json")))
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
	require.NotNil(t, persisted.Steps[0].Compensation)
	require.NotNil(t, persisted.EvidencePackage)
	require.NotNil(t, persisted.WorldModel)
	require.NotNil(t, persisted.AnalysisHandoff)
	require.NotNil(t, persisted.Validation)
	require.NotEmpty(t, persisted.ValidationLoops)
	require.NotEmpty(t, persisted.MemoryRecords)
	require.GreaterOrEqual(t, len(persisted.Events), 11)
}
