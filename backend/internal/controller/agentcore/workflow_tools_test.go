package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/changeintel"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockTool struct {
	name          ToolName
	runCount      int
	deterministic bool
	unsafe        bool
	result        workflowToolResult
	err           error
}

func (t *mockTool) Name() ToolName      { return t.name }
func (t *mockTool) Version() string     { return "v1" }
func (t *mockTool) Description() string { return "mock tool" }
func (t *mockTool) Deterministic() bool { return t.deterministic }
func (t *mockTool) Unsafe() bool        { return t.unsafe }
func (t *mockTool) Run(_ context.Context, _ workflowToolRequest) (workflowToolResult, error) {
	t.runCount++
	if t.err != nil {
		return workflowToolResult{}, t.err
	}
	if t.result.Summary != "" || t.result.Data != nil {
		return t.result, nil
	}
	return workflowToolResult{Summary: "mock success", Data: nil}, nil
}

func TestWorkflowToolManager_Idempotency(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockTool{name: "test_tool", deterministic: true}
	mgr := newWorkflowToolManager(logger, mock)

	req := workflowToolRequest{
		WorkflowID:     "wf-1",
		Workflow:       "rca",
		Stage:          "plan_act_verify_loop",
		IdempotencyKey: "key-1",
	}

	ctx := context.Background()

	// First call
	call1, res1, err := mgr.call(ctx, req, "test_tool")
	assert.NoError(t, err)
	assert.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call1.Status)
	assert.Equal(t, "success", call1.InvocationStatus)
	assert.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call1.Outcome)
	assert.Equal(t, 1, mock.runCount)

	// Second call with same key
	call2, res2, err := mgr.call(ctx, req, "test_tool")
	assert.NoError(t, err)
	assert.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call2.Status)
	assert.Equal(t, "skipped", call2.InvocationStatus)
	assert.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call2.Outcome)
	assert.Equal(t, 1, mock.runCount) // Should not increment
	assert.Equal(t, res1.Summary+" (cached)", call2.Summary)
	assert.Equal(t, res1.Data, res2.Data)

	// Third call with different key
	req.IdempotencyKey = "key-2"
	call3, _, err := mgr.call(ctx, req, "test_tool")
	assert.NoError(t, err)
	assert.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call3.Status)
	assert.Equal(t, "success", call3.InvocationStatus)
	assert.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call3.Outcome)
	assert.Equal(t, 2, mock.runCount)
}

func TestPostActionReadOnlyToolsAreSampledAfresh(t *testing.T) {
	mock := &mockTool{name: "sampled_read_only_tool", deterministic: true}
	mgr := newWorkflowToolManager(zap.NewNop(), mock)
	req := workflowToolRequest{WorkflowID: "wf-fresh", Workflow: "rca", Stage: "post_action_validation"}

	first, _, err := mgr.call(context.Background(), req, mock.Name())
	require.NoError(t, err)
	second, _, err := mgr.call(context.Background(), req, mock.Name())
	require.NoError(t, err)
	require.Equal(t, 2, mock.runCount)
	require.Equal(t, "success", first.InvocationStatus)
	require.Equal(t, "success", second.InvocationStatus)
	require.Empty(t, first.IdempotencyKey)
	require.Empty(t, second.IdempotencyKey)
	req.IdempotencyKey = "caller-supplied-key-must-not-cache-observations"
	_, _, err = mgr.call(context.Background(), req, mock.Name())
	require.NoError(t, err)
	_, _, err = mgr.call(context.Background(), req, mock.Name())
	require.NoError(t, err)
	require.Equal(t, 4, mock.runCount)

	req.IdempotencyKey = ""
	req.Stage = "plan_act_verify_loop"
	_, _, err = mgr.call(context.Background(), req, mock.Name())
	require.NoError(t, err)
	cached, _, err := mgr.call(context.Background(), req, mock.Name())
	require.NoError(t, err)
	require.Equal(t, 5, mock.runCount)
	require.Equal(t, "skipped", cached.InvocationStatus)
}

func TestFreshPostActionRiskPathBypassesCollectSignalCache(t *testing.T) {
	readOnly := &mockTool{name: ToolMetrics, deterministic: true}
	unsafe := &mockTool{name: ToolRemediation, unsafe: true}
	mgr := newWorkflowToolManager(zap.NewNop(), readOnly, unsafe)
	state := &workflowState{
		engine:        &WorkflowEngine{tools: mgr},
		workflowID:    "wf-risk-sample",
		workflowType:  "joint_risk",
		freshReadOnly: true,
	}

	for range 2 {
		_, err := state.callToolAs(context.Background(), "collect_signals", "analysis_agent", ToolMetrics, nil)
		require.NoError(t, err)
	}
	require.Equal(t, 2, readOnly.runCount)
	for _, call := range state.toolCalls {
		require.Equal(t, "post_action_validation", call.Stage)
		require.Equal(t, "success", call.InvocationStatus)
		require.Empty(t, call.IdempotencyKey)
	}
	_, err := state.callToolAs(context.Background(), "collect_signals", "analysis_agent", ToolRemediation, map[string]string{"action": "restart"})
	require.ErrorContains(t, err, "read-only tools only")
	require.Zero(t, unsafe.runCount)
}

func TestFreshPostActionRiskDoesNotReuseJointRiskOrMetricResult(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), nil, nil, zap.NewNop())
	metrics := &mockTool{
		name:          ToolMetrics,
		deterministic: true,
		result:        workflowToolResult{Data: metricsToolData{CollectorID: "collector-a"}},
	}
	engine.tools = newWorkflowToolManager(zap.NewNop(), metrics)
	parent := &workflowState{workflowID: "wf-risk-sample", workflowType: "rca", collectorID: "collector-a"}
	_, firstIDs, err := engine.freshPostActionRisk(context.Background(), parent, time.Now().UTC())
	require.ErrorContains(t, err, "metrics are insufficient")
	_, secondIDs, err := engine.freshPostActionRisk(context.Background(), parent, time.Now().UTC())
	require.ErrorContains(t, err, "metrics are insufficient")
	require.Equal(t, 2, metrics.runCount)
	require.Len(t, firstIDs, 1)
	require.Len(t, secondIDs, 1)
	require.NotEqual(t, firstIDs[0], secondIDs[0])
	metricCalls := 0
	for _, call := range parent.toolCalls {
		if call.Tool == ToolMetrics && call.InvocationStatus == "success" {
			metricCalls++
		}
	}
	require.Equal(t, 2, metricCalls)
}

func TestPolicyEngine_Evaluate(t *testing.T) {
	engine := NewPolicyEngine(nil)
	engine.cfg = DefaultWorkflowConfig()
	engine.cfg.AllowImpactingExec = true
	engine.cfg.AllowRemediationExec = true
	engine.cfg.ValidationAllowExecCategories = []string{"probable_containment"}

	// Case 1: Read-only tool
	roTool := &mockTool{name: ToolMetrics, unsafe: false}
	decision := engine.Evaluate(workflowToolRequest{}, roTool)
	assert.Equal(t, "allowed", decision.Status)
	assert.False(t, decision.DryRunRequired)

	// Case 2: Unsafe tool without dry-run (requires approval)
	unsafeTool := &mockTool{name: ToolRemediation, unsafe: true}
	decision = engine.Evaluate(workflowToolRequest{DryRun: false, Query: map[string]string{"action": "restart", "rollback": "undo", "validation_category": "probable_containment"}}, unsafeTool)
	assert.Equal(t, "pending", decision.Status)
	assert.True(t, decision.RequiresApproval)

	// Case 3: Unsafe tool with dry-run
	decision = engine.Evaluate(workflowToolRequest{DryRun: true, Query: map[string]string{"action": "restart", "rollback": "undo", "validation_category": "probable_containment"}}, unsafeTool)
	assert.Equal(t, "allowed", decision.Status)
	assert.True(t, decision.DryRunRequired)

	// Case 4: Unsafe remediation without rollback guidance
	decision = engine.Evaluate(workflowToolRequest{DryRun: false, Query: map[string]string{"action": "restart", "validation_category": "probable_containment"}}, unsafeTool)
	assert.Equal(t, "blocked", decision.Status)
	assert.True(t, decision.RollbackRequired)
}

func TestEvaluateActuatorExecutionPolicyEnforcesSafetyTiers(t *testing.T) {
	cfg := DefaultWorkflowConfig()

	readOnly := evaluateActuatorExecutionPolicy(cfg, &ValidationActionContract{
		ExecutionCategory: "read_only_validation",
		ReadOnly:          true,
	}, false)
	require.Equal(t, "allowed", readOnly.Status)
	require.Equal(t, ActuatorSafetyTierReadOnly, readOnly.SafetyTier)
	require.True(t, readOnly.ExecutionEligible)

	safeReversible := evaluateActuatorExecutionPolicy(cfg, &ValidationActionContract{
		Intent:            "capture_profile",
		ActionCategory:    "diagnostic",
		ExecutionCategory: "profiling",
		Summary:           "capture short profiling snapshot",
		Safe:              true,
		DryRunDefault:     true,
		Rollback:          RollbackContract{Reversible: true},
	}, false)
	require.Equal(t, "proposal_only", safeReversible.Status)
	require.Equal(t, ActuatorSafetyTierSafeReversible, safeReversible.SafetyTier)
	require.True(t, safeReversible.ProposalOnly)

	cfg.AllowSafeReversibleExec = true
	safeReversible = evaluateActuatorExecutionPolicy(cfg, &ValidationActionContract{
		Intent:            "capture_profile",
		ActionCategory:    "diagnostic",
		ExecutionCategory: "profiling",
		Summary:           "capture short profiling snapshot",
		Safe:              true,
		DryRunDefault:     true,
		Rollback:          RollbackContract{Reversible: true},
	}, false)
	require.Equal(t, "allowed", safeReversible.Status)
	require.True(t, safeReversible.ExecutionEligible)
	require.True(t, safeReversible.DryRunRequired)

	cfg = DefaultWorkflowConfig()
	impacting := evaluateActuatorExecutionPolicy(cfg, &ValidationActionContract{
		Intent:            "restart_workload",
		ActionCategory:    "containment",
		ExecutionCategory: "probable_containment",
		Summary:           "restart checkout pods",
		Rollback:          RollbackContract{Summary: "restore previous replica set", Reversible: true},
	}, false)
	require.Equal(t, "proposal_only", impacting.Status)
	require.Equal(t, ActuatorSafetyTierImpacting, impacting.SafetyTier)
	require.True(t, impacting.ProposalOnly)

	cfg.AllowImpactingExec = true
	cfg.AllowRemediationExec = true
	cfg.ValidationAllowExecCategories = []string{"probable_containment"}
	impacting = evaluateActuatorExecutionPolicy(cfg, &ValidationActionContract{
		Intent:            "restart_workload",
		ActionCategory:    "containment",
		ExecutionCategory: "probable_containment",
		Summary:           "restart checkout pods",
		Rollback:          RollbackContract{Summary: "restore previous replica set", Reversible: true},
	}, false)
	require.Equal(t, "allowed", impacting.Status)
	require.Equal(t, "approval_required", impacting.ExecutionLevel)
	require.True(t, impacting.ExecutionEligible)
	require.True(t, impacting.RequiresApproval)

	destructive := evaluateActuatorExecutionPolicy(cfg, &ValidationActionContract{
		Intent:            "targeted_remediation",
		ActionCategory:    "remediation",
		ExecutionCategory: "medium_term_remediation",
		Summary:           "delete checkout deployment",
	}, false)
	require.Equal(t, "proposal_only", destructive.Status)
	require.Equal(t, ActuatorSafetyTierDestructive, destructive.SafetyTier)
	require.True(t, destructive.ProposalOnly)
	require.False(t, destructive.ExecutionEligible)
}

func TestChangeQueryToolCorrelatesDerivedAndStoredChanges(t *testing.T) {
	now := time.Now().UTC()
	store := ingest.NewMemoryStore()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "gpu-a"}, now.Add(-10*time.Minute))
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{
			Name:  "node_cpu_usage_percent",
			Value: 84,
			Labels: []*telemetryv1.Label{
				{Key: "release.image", Value: "trainer:v2"},
				{Key: "nvidia.driver.version", Value: "550.54.15"},
			},
			TimestampUnixNano: now.UnixNano(),
		},
	}, now)

	index := logindex.NewIndex(logindex.DefaultConfig())
	index.AddBatch([]logindex.RawEvent{{
		CollectorID: "collector-a",
		Message:     "deployment completed for trainer-service image trainer:v2",
		Level:       "info",
		Timestamp:   now.Add(-5 * time.Minute),
		Count:       1,
	}})

	changeStore := changeintel.NewStore(t.TempDir(), zap.NewNop())
	_, err := changeStore.Append(changeintel.ChangeEvent{
		ChangeID:    "chg-flag",
		Category:    "feature_flag",
		Summary:     "canary flag enabled for trainer-service",
		CollectorID: "collector-a",
		Entity:      "trainer-service",
		Scope:       "service",
		StartedAt:   now.Add(-8 * time.Minute),
	})
	require.NoError(t, err)

	tool := &changeQueryTool{store: changeStore, index: index, nodes: store}
	result, err := tool.Run(context.Background(), workflowToolRequest{
		CollectorID: "collector-a",
		Window:      30 * time.Minute,
		Limit:       4,
		Query: map[string]string{
			"query":            "gpu timeout after rollout",
			"incident_summary": "gpu timeout after rollout",
			"scope":            "trainer-service",
		},
	})
	require.NoError(t, err)

	data, ok := result.Data.(changeToolData)
	require.True(t, ok)
	require.NotEmpty(t, data.Events)
	require.NotEmpty(t, data.Categories)
	require.NotNil(t, data.Strongest)
}

func TestKnowledgeRetrievalToolIncludesIncidentMemoryConfidence(t *testing.T) {
	memory := NewWorkflowMemoryStore(t.TempDir(), nil, zap.NewNop())
	_, err := memory.Append(WorkflowMemoryRecord{
		RecordID:            "incident-verified",
		WorkflowID:          "wf-1",
		IncidentID:          "inc-1",
		WorkflowType:        "rca",
		CollectorID:         "collector-a",
		Status:              "resolved",
		Title:               "GPU timeout after rollout",
		Summary:             "GPU jobs timed out after rollout until the driver rollback completed",
		MostLikelyCause:     "driver mismatch",
		ResolutionSummary:   "rolled back the driver change",
		VerificationSummary: "latency returned to baseline",
		Signals:             []string{"gpu", "timeout", "latency"},
		Actions:             []string{"rollback driver"},
		ActionOutcomes: []WorkflowMemoryActionOutcome{{
			Action:       "rollback driver",
			Status:       "verified",
			Verification: "latency returned to baseline",
			Success:      true,
			Useful:       true,
		}},
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	tool := &knowledgeRetrievalTool{
		name:        ToolHistoricalIncident,
		intent:      "historical_incident",
		description: "test historical incident retrieval",
		memory:      memory,
	}
	result, err := tool.Run(context.Background(), workflowToolRequest{
		WorkflowID:  "wf-1",
		Workflow:    "rca",
		Stage:       "context_gathering",
		CollectorID: "collector-a",
		Query: map[string]string{
			"query": "gpu timeout after rollout rollback driver latency",
		},
	})
	require.NoError(t, err)

	data, ok := result.Data.(knowledgeToolData)
	require.True(t, ok)
	require.NotEmpty(t, data.Hits)
	require.Greater(t, data.Confidence, 0.0)
	require.Contains(t, strings.ToLower(data.Summary), "incident memory")
	require.Equal(t, "incident_memory", data.Hits[0].SourceType)
	require.Contains(t, strings.ToLower(data.Hits[0].Metadata["match_reasons"]), "verified")
}

func TestWorkflowToolRegistryIncludesValidationActionTools(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	cfg := DefaultWorkflowConfig()
	cfg.WorkflowDataPath = t.TempDir()

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	descriptors := engine.ToolRegistry()

	found := map[ToolName]bool{}
	for _, desc := range descriptors {
		found[desc.Name] = true
	}

	require.True(t, found[ToolDeploymentHistory])
	require.True(t, found[ToolConfigState])
	require.True(t, found[ToolMemoryPressure])
	require.True(t, found[ToolConnectivityCheck])
	require.True(t, found[ToolDNSCheck])
	require.True(t, found[ToolServiceHealth])
	require.True(t, found[ToolKubernetesResource])
	require.True(t, found[ToolContainerRevision])
	require.True(t, found[ToolStorageHealth])
	require.True(t, found[ToolNetworkBlastRadius])
	require.True(t, found[ToolActionOutcome])
}

func TestWorkflowToolManagerCarriesValidationExecutionCategoryIntoToolCall(t *testing.T) {
	manager := newWorkflowToolManager(zap.NewNop(), &mockTool{name: ToolRemediation, deterministic: true, unsafe: true})
	contract := ValidationActionContract{
		ID:                "contract-1",
		Intent:            "rollback_revision",
		Summary:           "rollback checkout-v2",
		ExecutionCategory: "probable_containment",
		Target:            ActionTargetRef{CollectorID: "collector-a", Scope: "service:checkout"},
		Rollback:          RollbackContract{Summary: "restore checkout:v1", Required: true, Reversible: true},
	}
	call, _, err := manager.call(context.Background(), workflowToolRequest{
		WorkflowID:  "wf-1",
		Workflow:    "rca",
		Stage:       "guarded_execution_plan",
		Actor:       "validation_action_agent",
		CollectorID: "collector-a",
		DryRun:      true,
		Query: map[string]string{
			"action_contract":     encodeValidationActionContract(contract),
			"validation_category": "probable_containment",
		},
	}, ToolRemediation)
	require.NoError(t, err)
	require.Equal(t, "probable_containment", call.ExecutionCategory)
	require.Equal(t, "rollback_revision", call.ActionIntent)
	require.Equal(t, WorkflowToolOutcomeProposedOnly, call.Outcome)
}

func TestRemediationActionToolUsesTypedContractAndStaysPlannedWithoutExecutionSpec(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.DryRun = false
	tool := &remediationActionTool{cfg: cfg}
	contract := ValidationActionContract{
		ID:                "contract-2",
		Intent:            "rollback_revision",
		Summary:           "rollback checkout-v2",
		ExecutionCategory: "probable_containment",
		Target:            ActionTargetRef{CollectorID: "collector-a", Scope: "service:checkout"},
		Preconditions:     []string{"confirm rollout regression"},
		Rollback:          RollbackContract{Summary: "restore checkout:v1", Required: true, Reversible: true},
	}

	result, err := tool.Run(context.Background(), workflowToolRequest{
		WorkflowID:  "wf-2",
		Workflow:    "rca",
		Stage:       "guarded_execution_plan",
		CollectorID: "collector-a",
		DryRun:      false,
		Query: map[string]string{
			"action_contract":     encodeValidationActionContract(contract),
			"validation_category": "probable_containment",
		},
	})
	require.NoError(t, err)

	data, ok := result.Data.(remediationToolData)
	require.True(t, ok)
	require.Equal(t, "rollback_revision", data.ActionIntent)
	require.Equal(t, "probable_containment", data.ExecutionCategory)
	require.Equal(t, "proposal_only", data.Mode)
	require.Equal(t, "impacting", data.SafetyTier)
	require.True(t, data.ProposalOnly)
	require.False(t, data.ExecutionEligible)
	require.Equal(t, "restore checkout:v1", data.RollbackPlan)
	require.Nil(t, data.Contract.Execution)
	require.Nil(t, data.ExecutedAction)
}

func TestRemediationActionToolKeepsSafeReversibleActionsEligibleWhenPolicyAllows(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.DryRun = false
	cfg.AllowSafeReversibleExec = true
	tool := &remediationActionTool{cfg: cfg}
	contract := ValidationActionContract{
		ID:                 "contract-3",
		Intent:             "capture_profile",
		ActionCategory:     "diagnostic",
		Summary:            "capture profiling snapshot",
		ExecutionCategory:  "profiling",
		ValidationCategory: "profiling",
		DryRunDefault:      true,
		Safe:               true,
		Rollback:           RollbackContract{Summary: "stop profiler", Required: true, Reversible: true},
	}

	result, err := tool.Run(context.Background(), workflowToolRequest{
		WorkflowID:  "wf-3",
		Workflow:    "rca",
		Stage:       "guarded_execution_plan",
		CollectorID: "collector-a",
		Query: map[string]string{
			"action_contract":     encodeValidationActionContract(contract),
			"validation_category": "profiling",
		},
	})
	require.NoError(t, err)

	data, ok := result.Data.(remediationToolData)
	require.True(t, ok)
	require.Equal(t, "planned_only", data.Mode)
	require.Equal(t, "safe_reversible", data.SafetyTier)
	require.False(t, data.ProposalOnly)
	require.True(t, data.ExecutionEligible)
}

func TestRemediationToolReturnsHumanHandoffForUnsafeExecutionSpec(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.DryRun = false
	cfg.AllowSafeReversibleExec = true
	cfg.ActionRunner.AllowUnsafe = true
	cfg.ActionRunner.DryRun = false
	cfg.ActionRunner.PlaybookFile = ""
	cfg.ActionRunner.AllowedShellCommands = []string{"systemctl"}
	runner, err := NewPlaybookRunner(cfg.ActionRunner, zap.NewNop())
	require.NoError(t, err)
	tool := &remediationActionTool{cfg: cfg, runner: runner}
	contract := ValidationActionContract{
		ID:                 "operator-handoff",
		Intent:             "restart_service",
		Summary:            "restart service after diagnosis",
		ExecutionCategory:  "profiling",
		ActuatorSafetyTier: ActuatorSafetyTierSafeReversible,
		Target:             ActionTargetRef{Scope: "service:api"},
		Execution:          &ActionSpec{Type: "shell", Command: "systemctl restart api", Safe: true},
	}
	result, err := tool.Run(context.Background(), workflowToolRequest{
		WorkflowID: "wf-handoff", Workflow: "rca", Stage: "guarded_execution_plan",
		Query: map[string]string{"action_contract": encodeValidationActionContract(contract)},
	})
	require.NoError(t, err)
	data, ok := result.Data.(remediationToolData)
	require.True(t, ok)
	require.Equal(t, "proposal_only", data.Mode)
	require.Equal(t, ActuatorSafetyTierDestructive, data.SafetyTier)
	require.True(t, data.ProposalOnly)
	require.False(t, data.ExecutionEligible)
	require.NotNil(t, data.OperatorHandoff)
	require.Equal(t, "awaiting_human_execution", data.OperatorHandoff.Status)
	require.Contains(t, data.OperatorHandoff.MissingInformation, "exact production object and version")
	require.Equal(t, "proposal_only", workflowToolCallStatus(ToolRemediation, result, ActionPolicyDecision{}))
}

func TestValidationActionToolsReturnDeterministicSummaries(t *testing.T) {
	now := time.Now().UTC()
	store := ingest.NewMemoryStore()
	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "collector-validation-tools",
		Hostname:    "validation-host",
		Labels: []*telemetryv1.Label{
			{Key: "service", Value: "checkout"},
			{Key: "namespace", Value: "shop"},
			{Key: "release.image", Value: "checkout:v2"},
			{Key: "revision", Value: "42"},
			{Key: "pod_uid", Value: "pod-42"},
		},
	}, now)
	store.StoreMetrics("collector-validation-tools", []*telemetryv1.Metric{
		{Name: "node_memory_usage_percent", Value: 92, TimestampUnixNano: now.UnixNano()},
		{Name: "node_memory_available_bytes", Value: 512 * 1024 * 1024, TimestampUnixNano: now.UnixNano()},
		{Name: "node_tcp_retransmit_ratio", Value: 0.04, TimestampUnixNano: now.UnixNano()},
		{Name: "node_softnet_dropped_per_second", Value: 38, TimestampUnixNano: now.UnixNano()},
		{Name: "service_latency_p95_ms", Value: 1450, TimestampUnixNano: now.UnixNano()},
		{Name: "service_error_rate", Value: 0.08, TimestampUnixNano: now.UnixNano()},
		{Name: "rca_memory_process_rss_bytes", Value: 2.1 * 1024 * 1024 * 1024, TimestampUnixNano: now.UnixNano(), Labels: []*telemetryv1.Label{{Key: "name", Value: "checkout-api"}, {Key: "pod_uid", Value: "pod-42"}, {Key: "workload_class", Value: "deployment"}}},
	}, now)
	store.StoreLogs("collector-validation-tools", []*telemetryv1.LogFingerprint{
		{Fingerprint: "oom", Count: 2, Example: "OOMKilled after memory pressure on checkout-api"},
		{Fingerprint: "dns", Count: 1, Example: "lookup payments.service.local: no such host"},
	}, now)

	ctx := context.Background()
	req := workflowToolRequest{
		WorkflowID:  "wf-tools",
		Workflow:    "rca",
		Stage:       "validation_action_react_loop",
		CollectorID: "collector-validation-tools",
		Query:       map[string]string{"query": "checkout rollout validation"},
	}

	memResult, err := (&memoryPressureTool{store: store}).Run(ctx, req)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(memResult.Summary), "memory")

	healthResult, err := (&serviceHealthTool{store: store}).Run(ctx, req)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(healthResult.Summary), "healthy=")

	revisionResult, err := (&containerRevisionTool{store: store}).Run(ctx, req)
	require.NoError(t, err)
	require.Contains(t, revisionResult.Summary, "revision=42")
}

func TestWorkflowToolManagerDowngradesImpactingValidationExecToProposalOnlyByDefault(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.DryRun = false
	cfg.ValidationAllowExecCategories = []string{"profiling"}

	manager := newWorkflowToolManager(zap.NewNop(), &remediationActionTool{cfg: cfg})
	manager.cfg = cfg

	call, result, err := manager.call(context.Background(), workflowToolRequest{
		WorkflowID:  "wf-validation-exec",
		Workflow:    "rca",
		Stage:       "guarded_execution_plan",
		Actor:       "validation_action_agent",
		CollectorID: "collector-a",
		DryRun:      false,
		Query: map[string]string{
			"action":              "restart checkout",
			"rollback":            "restore checkout:v1",
			"validation_category": "probable_containment",
		},
	}, ToolRemediation)
	require.NoError(t, err)
	require.Equal(t, WorkflowToolOutcomeProposedOnly, call.Status)
	require.Equal(t, "proposal_only", call.InvocationStatus)
	require.Equal(t, "proposal_only", call.Policy.Status)
	require.Equal(t, WorkflowToolOutcomeProposedOnly, call.Outcome)
	require.True(t, call.Policy.ProposalOnly)
	data, ok := result.Data.(remediationToolData)
	require.True(t, ok)
	require.Equal(t, "proposal_only", data.Mode)
	require.Equal(t, "impacting", data.SafetyTier)
}

func TestWorkflowToolCallStatusPreservesPlannedOnlyRemediationMode(t *testing.T) {
	status := workflowToolCallStatus(ToolRemediation, workflowToolResult{
		Summary: "remediation plan created",
		Data: remediationToolData{
			Mode: "planned_only",
		},
	}, ActionPolicyDecision{Status: "allowed"})
	require.Equal(t, "planned_only", status)
}

func TestWorkflowToolManagerSetsReadOnlyOutcomeSemantics(t *testing.T) {
	manager := newWorkflowToolManager(zap.NewNop(), &mockTool{name: "metrics_like", deterministic: true})

	call, _, err := manager.call(context.Background(), workflowToolRequest{
		WorkflowID: "wf-ro",
		Workflow:   "rca",
		Stage:      "context_gathering",
	}, "metrics_like")
	require.NoError(t, err)
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call.Status)
	require.Equal(t, "success", call.InvocationStatus)
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call.Outcome)

	manager = newWorkflowToolManager(zap.NewNop(), &mockTool{name: "metrics_like", deterministic: true, err: context.DeadlineExceeded})
	call, _, err = manager.call(context.Background(), workflowToolRequest{
		WorkflowID: "wf-ro-fail",
		Workflow:   "rca",
		Stage:      "context_gathering",
	}, "metrics_like")
	require.Error(t, err)
	require.Equal(t, WorkflowToolOutcomeReadOnlyFailure, call.Status)
	require.Equal(t, "failed", call.InvocationStatus)
	require.Equal(t, WorkflowToolOutcomeReadOnlyFailure, call.Outcome)
}
