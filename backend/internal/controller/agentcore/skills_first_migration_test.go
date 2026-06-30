package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func validWorkflowContractForTest(tool ToolName, readOnly bool) WorkflowToolContract {
	approval := WorkflowToolApprovalRequirement{}
	idempotency := WorkflowToolIdempotencySemantic{Scope: "tool+workflow", Reuse: "cache"}
	rollback := WorkflowToolRollbackSemantics{Semantics: "not_applicable_read_only"}
	replay := "replayable_when_source_window_and_artifact_refs_are_available"
	if !readOnly {
		approval.Required = true
		idempotency.Required = true
		rollback = WorkflowToolRollbackSemantics{Supported: true, Required: tool == ToolRemediation, Semantics: "rollback_or_compensation_required"}
		replay = "intent_replay_only; execution_result_history_is_not_replayed"
	}
	family := workflowToolCapabilityFamily(tool)
	if family == "" {
		family = "generic"
	}
	return WorkflowToolContract{
		SchemaVersion:              workflowToolContractSchemaVersion,
		ToolName:                   tool,
		Version:                    "v1",
		Purpose:                    "test contract",
		CapabilityFamily:           family,
		AllowedStages:              []string{"*"},
		AllowedRuntimeContexts:     []string{"rca"},
		InputSchema:                "{}",
		OutputSchema:               "{}",
		EvidenceConsumed:           []string{"incident_window"},
		EvidenceProduced:           []string{"tool_observation"},
		Determinism:                "deterministic",
		ReadOnly:                   readOnly,
		StateChanging:              !readOnly,
		SafetyClass:                map[bool]string{true: "read_only", false: "approval_gated"}[readOnly],
		Rollback:                   rollback,
		Idempotency:                idempotency,
		TimeoutBudget:              "10s",
		RetryPolicy:                WorkflowToolRetryPolicy{MaxAttempts: 1, Retryable: readOnly},
		Approval:                   approval,
		CostClass:                  map[bool]string{true: "low", false: "high"}[readOnly],
		ExpectedInformationGain:    0.5,
		EligibleForAutoSelection:   readOnly,
		FreshnessSensitivity:       "medium",
		ScopeSensitivity:           "collector_or_service",
		ReplaySemantics:            replay,
		ContractValidationVersion:  "v1",
		ExpectedInformationProfile: "test",
	}
}

func validRichContractForTest(tool ToolName, readOnly bool) ToolContract {
	return toolContractFromLegacy(validWorkflowContractForTest(tool, readOnly))
}

func TestToolContractRejectsMissingSchema(t *testing.T) {
	contract := validRichContractForTest(ToolMetrics, true)
	contract.InputSchema = ""
	require.ErrorContains(t, validateToolContract(contract), "input_schema")
}

func TestToolContractRejectsStateChangingAutonomy(t *testing.T) {
	contract := validRichContractForTest(ToolRemediation, false)
	contract.AutonomyEligibility = ToolAutonomyEligibilityAutonomousReadOnly
	contract.AutonomousSelectionEligible = ToolAutonomyEligibilityAutonomousReadOnly
	require.ErrorContains(t, validateToolContract(contract), "state-changing")
}

func TestToolContractRequiresApprovalForImpactfulAction(t *testing.T) {
	contract := validRichContractForTest(ToolProfiling, false)
	contract.ApprovalRequirement = WorkflowToolApprovalRequirement{}
	contract.ApprovalRequired = false
	contract.LegacyContract.Approval = WorkflowToolApprovalRequirement{}
	require.ErrorContains(t, validateToolContract(contract), "requires approval")
}

func TestToolContractRequiresRollbackForRemediation(t *testing.T) {
	contract := validRichContractForTest(ToolRemediation, false)
	contract.RollbackSemantics = WorkflowToolRollbackSemantics{Supported: false, Required: false}
	contract.LegacyContract.Rollback = WorkflowToolRollbackSemantics{Supported: false, Required: false}
	require.ErrorContains(t, validateToolContract(contract), "rollback")
}

func TestToolContractDefaultsLegacyFields(t *testing.T) {
	legacy := validWorkflowContractForTest(ToolLogs, true)
	contract := toolContractFromLegacy(legacy)
	require.Equal(t, legacy.Purpose, contract.Description)
	require.Equal(t, legacy.Approval.Required, contract.ApprovalRequired)
	require.Equal(t, legacy.Idempotency, contract.Idempotency)
	require.Equal(t, ToolAutonomyEligibilityAutonomousReadOnly, contract.AutonomyEligibility)
	require.NotEmpty(t, contract.QueryHints.ScopeKeys)
	require.NotEmpty(t, contract.LikelyFollowUpFamilies)
	require.NotEmpty(t, contract.ReplaySemantics)
}

func TestWorkflowToolManagerValidatesRichContractBeforeRun(t *testing.T) {
	mock := &mockTool{name: ToolMetrics, deterministic: true}
	manager := newWorkflowToolManager(zap.NewNop(), mock)
	invalid := validRichContractForTest(ToolMetrics, true)
	invalid.InputSchema = ""
	manager.contracts = &ToolContractRegistry{
		contracts: []ToolContract{invalid},
		byName:    map[ToolName]ToolContract{ToolMetrics: invalid},
	}

	call, _, err := manager.call(context.Background(), workflowToolRequest{
		WorkflowID:  "wf-rich-contract",
		Workflow:    "rca",
		Stage:       adaptiveRuntimeStage,
		CollectorID: "collector-a",
		DryRun:      true,
		Query:       map[string]string{"query": "cpu pressure"},
	}, ToolMetrics)
	require.Error(t, err)
	require.Equal(t, "failed", call.InvocationStatus)
	require.Equal(t, 0, mock.runCount)
}

func TestRAGQueryIsReadOnlySkill(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	desc, ok := workflowToolDescriptorByName(engine.ToolRegistry(), ToolRAGQuery)
	require.True(t, ok)
	require.True(t, desc.ReadOnly)
	require.False(t, desc.RequiresApproval)
	require.Equal(t, "knowledge", desc.Contract.CapabilityFamily)
	require.True(t, desc.Contract.EligibleForAutoSelection)
	require.Equal(t, ToolAutonomyEligibilityAutonomousReadOnly, desc.RichContract.AutonomyEligibility)
}

func TestRAGUnavailableCreatesEvidenceGap(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	engine.ragQuery.memory = nil
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	state.incident = IncidentSynthesis{Summary: "cpu pressure without knowledge index"}
	result, err := engine.ragQuery.Run(context.Background(), workflowToolRequest{
		WorkflowID:  state.workflowID,
		Workflow:    "rca",
		Stage:       "context_gathering",
		CollectorID: state.collectorID,
		Query:       map[string]string{"query": "cpu pressure without knowledge index", "top_k": "4"},
		DryRun:      true,
	})
	require.NoError(t, err)
	data, ok := result.Data.(knowledgeToolData)
	require.True(t, ok)
	require.Empty(t, data.Hits)
	require.Contains(t, adaptiveEvidenceGaps(state), "corroborating_knowledge")
}

func TestKnowledgeRetrievalResultIsNormalized(t *testing.T) {
	state := &workflowState{collectorID: "collector-a"}
	normalized := normalizeToolResult(ToolRAGQuery, workflowToolResult{
		Summary: "knowledge_hits=1",
		Data: knowledgeToolData{
			Tool:        ToolRAGQuery,
			Hits:        []RetrievedDocumentEvidence{{EvidenceID: "ev-kb-1", Summary: "prior cpu pressure case"}},
			Summary:     "prior cpu pressure case",
			Confidence:  0.42,
			EvidenceIDs: []string{"ev-kb-1"},
		},
	}, "call-rag-1", state)
	require.NotNil(t, normalized)
	require.Equal(t, ToolRAGQuery, normalized.Tool)
	require.Contains(t, normalized.EvidenceIDs, "ev-kb-1")
	require.Contains(t, normalized.EvidenceIDs, "call-rag-1")
	require.Greater(t, normalized.ConfidenceDelta, 0.0)
	require.Equal(t, "bounded_ttl", normalized.Cacheability)
}

func TestWorkflowDoesNotStopOnlyBecauseRAGIsEmpty(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	state.incident = IncidentSynthesis{Summary: "checkout service latency after rollout with empty knowledge index"}
	require.NoError(t, engine.stepGatherRCAContext(context.Background(), state))
	require.NotEmpty(t, state.toolCalls)
	for _, call := range state.toolCalls {
		require.NotEqual(t, "failed", call.InvocationStatus)
	}
}

func TestRAGQueryRespectsTopKAndScopeBounds(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 30 * time.Minute})
	contract, ok := engine.tools.contracts.Get(ToolRAGQuery)
	require.True(t, ok)
	shaped := shapeToolQuery(state, contract, buildToolSelectionContext(state, adaptiveRuntimeStage, nil))
	require.Equal(t, "4", shaped.Query["top_k"])
	require.NotEmpty(t, shaped.Query["scope"])

	call, _, err := engine.tools.call(context.Background(), workflowToolRequest{
		WorkflowID:  state.workflowID,
		Workflow:    "rca",
		Stage:       adaptiveRuntimeStage,
		CollectorID: state.collectorID,
		Query:       map[string]string{"query": "cpu pressure and prior runbook evidence", "top_k": "6"},
		DryRun:      true,
	}, ToolRAGQuery)
	require.ErrorContains(t, err, "top_k")
	require.Equal(t, "failed", call.InvocationStatus)
}

func TestCandidateGenerationUsesRegistry(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	engine.tools = newWorkflowToolManager(zap.NewNop(), &mockTool{name: ToolName("custom_registry_skill"), deterministic: true})
	engine.tools.cfg = engine.cfg
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
	context.EvidenceGaps = []string{"tool observation"}
	candidates := generateToolCandidates(state, context)
	require.Len(t, candidates, 1)
	require.Equal(t, ToolName("custom_registry_skill"), candidates[0].Tool)
}

func TestPolicyIneligibleCandidateScoresZero(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	contract, ok := engine.tools.contracts.Get(ToolRemediation)
	require.True(t, ok)
	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
	context.ReadOnlyOnly = true
	score := scoreToolCandidate(state, context, contract, map[string]string{"query": "remediate cpu pressure"})
	require.Zero(t, score.Total)
	require.Zero(t, score.Breakdown.PolicyEligibility)
}

func TestRepeatedLowYieldSkillIsSuppressed(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	state.adaptiveNormalizedResults = []NormalizedToolResult{
		{Tool: ToolLogs, ResultQuality: "low", LowYieldSignal: true},
		{Tool: ToolLogs, ResultQuality: "low", LowYieldSignal: true},
	}
	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
	context.PreferredTools = []ToolName{ToolLogs}
	candidates := generateToolCandidates(state, context)
	for _, candidate := range candidates {
		require.NotEqual(t, ToolLogs, candidate.Tool)
	}
}

func TestCheapReadOnlySkillBeatsExpensiveStateChangingSkill(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
	context.ReadOnlyOnly = false
	context.EvidenceGaps = []string{"metric baseline"}
	metricsContract, ok := engine.tools.contracts.Get(ToolMetrics)
	require.True(t, ok)
	remediationContract, ok := engine.tools.contracts.Get(ToolRemediation)
	require.True(t, ok)
	metricsScore := scoreToolCandidate(state, context, metricsContract, map[string]string{"query": "metric baseline"})
	remediationScore := scoreToolCandidate(state, context, remediationContract, map[string]string{"query": "metric baseline"})
	require.Greater(t, metricsScore.Total, remediationScore.Total)
}

func TestNoSafeNextStepStopReasonIsPersisted(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybrid
	cfg.AdaptiveMaxIterations = 1
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	engine.tools = newWorkflowToolManager(zap.NewNop())
	engine.tools.cfg = cfg
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	require.NoError(t, engine.stepAdaptiveRuntimeLoop(context.Background(), state))
	require.NotNil(t, state.adaptiveState)
	require.Equal(t, string(AdaptiveStopReasonNoSafeNextStep), state.adaptiveState.StopReason)
	require.True(t, adaptiveArtifactsContain(state.adaptiveArtifacts, WorkflowArtifactStopDecision))
}

func TestMetricsResultNormalization(t *testing.T) {
	state := &workflowState{collectorID: "collector-a"}
	normalized := normalizeToolResult(ToolMetrics, workflowToolResult{
		Summary: "collector=collector-a history_samples=1",
		Data:    metricsToolData{CollectorID: "collector-a", History: []ingest.MetricHistorySample{{}}},
	}, "call-metrics-1", state)
	require.Contains(t, normalized.StructuredFindings, "history_samples=1")
	require.Contains(t, normalized.LikelyNextToolFamilies, "service_health")
	require.True(t, normalized.HypothesisSpaceNarrowed)
}

func TestLogsResultNormalization(t *testing.T) {
	now := time.Now().UTC()
	index := logindex.NewIndex(logindex.DefaultConfig())
	require.Equal(t, 2, index.AddBatch([]logindex.RawEvent{
		{
			Timestamp:   now.Add(-2 * time.Minute),
			CollectorID: "collector-a",
			Level:       logindex.LevelError,
			Message:     "checkout timeout contacting database",
			Count:       4,
		},
		{
			Timestamp:   now.Add(-time.Minute),
			CollectorID: "collector-a",
			Level:       logindex.LevelWarn,
			Message:     "checkout retry budget nearly exhausted",
			Count:       2,
		},
	}))
	manager := newWorkflowToolManager(zap.NewNop(), &logsQueryTool{index: index})
	call, result, err := manager.call(context.Background(), workflowToolRequest{
		WorkflowID:  "wf-logs-normalization",
		Workflow:    "rca",
		Stage:       adaptiveRuntimeStage,
		CollectorID: "collector-a",
		Window:      5 * time.Minute,
		Query:       map[string]string{"query": "checkout"},
	}, ToolLogs)
	require.NoError(t, err)
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call.Outcome)

	state := &workflowState{collectorID: "collector-a"}
	normalized := normalizeToolResult(ToolLogs, result, call.ID, state)
	require.Contains(t, normalized.StructuredFindings, "errors=4")
	require.Contains(t, normalized.StructuredFindings, "warnings=2")
	require.Contains(t, normalized.EvidenceIDs, call.ID)
	require.Contains(t, normalized.LikelyNextToolFamilies, "change")
	require.Contains(t, normalized.AffectedScope, "collector-a")
	require.True(t, normalized.HypothesisSpaceNarrowed)
}

func TestRAGResultNormalization(t *testing.T) {
	TestKnowledgeRetrievalResultIsNormalized(t)
}

func TestGPUResultNormalization(t *testing.T) {
	state := &workflowState{collectorID: "collector-gpu"}
	normalized := normalizeToolResult(ToolGPU, workflowToolResult{
		Summary: "gpu memory pressure",
		Data:    gpuToolData{Summary: "gpu memory pressure", Metrics: map[string]float64{"gpu.mem": 95}},
	}, "call-gpu-1", state)
	require.Contains(t, normalized.AffectedScope, "collector-gpu")
	require.Contains(t, normalized.LikelyNextToolFamilies, "process")
	require.True(t, normalized.HypothesisSpaceNarrowed)
}

func TestRemediationResultNormalization(t *testing.T) {
	state := &workflowState{collectorID: "collector-a"}
	normalized := normalizeToolResult(ToolRemediation, workflowToolResult{
		Summary: "remediation dry run",
		Data: remediationToolData{
			Summary:      "remediation dry run",
			Mode:         "dry_run",
			RollbackPlan: "rollback ready",
			Contract:     ValidationActionContract{TargetScope: "service:checkout"},
		},
	}, "call-remediation-1", state)
	require.Greater(t, normalized.RemediationEligibilityDelta, 0.0)
	require.Contains(t, normalized.AffectedScope, "service:checkout")
}

func TestRawPayloadStoredAsEvidenceReference(t *testing.T) {
	state := &workflowState{collectorID: "collector-a"}
	normalized := normalizeToolResult(ToolName("legacy_tool"), workflowToolResult{
		Summary: strings.Repeat("raw-payload ", 80),
		Data:    strings.Repeat("oversized", 1024),
	}, "call-raw-1", state)
	require.LessOrEqual(t, len(normalized.Summary), 220)
	require.Contains(t, normalized.EvidenceIDs, "call-raw-1")
	require.NotContains(t, strings.Join(normalized.StructuredFindings, " "), strings.Repeat("oversized", 4))
}

func TestPlannerDoesNotExecute(t *testing.T) {
	mock := &mockTool{name: ToolMetrics, deterministic: true}
	manager := newWorkflowToolManager(zap.NewNop(), mock)
	contract, ok := manager.contracts.Get(ToolMetrics)
	require.True(t, ok)
	proposal := buildPlannerProposal(&workflowState{}, ToolSelectionContext{Objective: "collect metrics"}, []ToolCandidate{{Tool: ToolMetrics, Contract: contract, LegacyContract: contract.LegacyContract}})
	require.NotNil(t, proposal.Selected)
	require.Equal(t, 0, mock.runCount)
}

func TestCriticFlagsPrematureRemediation(t *testing.T) {
	contract := validRichContractForTest(ToolRemediation, false)
	report := critiquePlannerProposal(&workflowState{}, PlannerProposal{
		Selected: &ToolCandidate{Tool: ToolRemediation, Contract: contract, LegacyContract: contract.LegacyContract, Query: map[string]string{"query": "restart service", "scope": "service:checkout", "window": "5m"}},
	})
	require.True(t, report.PrematureRemediation)
	require.Contains(t, report.Summary, "premature remediation")
}

func TestVerifierDetectsNoProgress(t *testing.T) {
	before := AdaptiveRuntimeState{ConfidenceScore: 0.4, RiskScore: 0.5, UnresolvedEvidenceGaps: []string{"logs"}}
	after := before
	progress := buildAdaptiveProgressAssessment(before, after, "call-1")
	report := verifyAdaptiveProgress(before, after, &NormalizedToolResult{LowYieldSignal: true}, progress)
	require.Equal(t, "low_yield_plateau", report.ProgressClassification)
	require.Equal(t, AdaptiveDirectiveStop, report.Directive)
}

func TestRuntimeStopsOnUncertaintyPlateau(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.AdaptiveMaxPlateauRounds = 1
	state := AdaptiveRuntimeState{Budget: AdaptiveBudgetState{RemainingToolCalls: 1}, RemainingToolBudget: 1, UncertaintyPlateauRounds: 1}
	stop, reason := state.shouldStop(cfg)
	require.True(t, stop)
	require.Equal(t, "uncertainty_plateau", reason)
}

func TestExperienceMemoryBiasIsCapped(t *testing.T) {
	require.Equal(t, 0.20, clampExperienceBias(10))
	require.Equal(t, -0.20, clampExperienceBias(-10))
}

func TestOldDurableRunDecodesAfterSkillMigration(t *testing.T) {
	raw := []byte(`{"run_id":"old-run","workflow_type":"rca","collector_id":"collector-a","status":"completed","current_step":"finalize","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","tool_calls":[{"id":"call-1","tool":"metrics_query","stage":"collect_signals","status":"success"}]}`)
	var run DurableRun
	require.NoError(t, json.Unmarshal(raw, &run))
	require.True(t, normalizeDurableRun(&run))
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, run.ToolCalls[0].Outcome)
}

func TestAdaptiveArtifactVersionedFieldsDecodeWithDefaults(t *testing.T) {
	run := DurableRun{
		RunID: "run-adaptive",
		AdaptiveArtifacts: []AdaptiveArtifact{{
			Kind:       WorkflowArtifactToolDecision,
			ArtifactID: "artifact-1",
			RunID:      "run-adaptive",
		}},
	}
	require.True(t, normalizeDurableRun(&run))
	require.Equal(t, adaptiveRuntimeSchemaVersion, run.AdaptiveArtifacts[0].SchemaVersion)
	require.Equal(t, "v1", run.AdaptiveArtifacts[0].Version)
	require.True(t, run.AdaptiveArtifacts[0].Replayable)
	require.NotEmpty(t, run.AdaptiveArtifacts[0].ReplaySemantics)
}

func TestReplayBookkeepingPreservesRecordedSideEffects(t *testing.T) {
	ctx := context.Background()
	orchestrator := NewDurableOrchestrator(NewInMemoryDurableStore(), zap.NewNop())

	_, err := orchestrator.StartRun(ctx, "run-side-effect-replay", "rca", "collector-a")
	require.NoError(t, err)
	require.NoError(t, orchestrator.RecordToolCall(ctx, "run-side-effect-replay", WorkflowToolCall{
		ID:                "call-remediation-1",
		Tool:              ToolRemediation,
		Stage:             "guarded_execution_plan",
		CollectorID:       "collector-a",
		ExecutionCategory: "probable_containment",
		ActionIntent:      "restart_workload",
		Status:            WorkflowToolOutcomeExecutedSuccess,
		InvocationStatus:  "success",
		Outcome:           WorkflowToolOutcomeExecutedSuccess,
		Summary:           "historical remediation result",
		StartedAt:         time.Now().UTC(),
		CompletedAt:       time.Now().UTC(),
	}))

	before, err := orchestrator.GetRun(ctx, "run-side-effect-replay")
	require.NoError(t, err)
	require.Len(t, before.ToolCalls, 1)

	replayed, err := orchestrator.ReplayRun(ctx, "run-side-effect-replay")
	require.NoError(t, err)
	require.Equal(t, 1, replayed.ReplayCount)

	after, err := orchestrator.GetRun(ctx, "run-side-effect-replay")
	require.NoError(t, err)
	require.Len(t, after.ToolCalls, 1)
	require.Equal(t, before.ToolCalls[0], after.ToolCalls[0])

	eventCounts := map[string]int{}
	for _, event := range after.Events {
		eventCounts[event.Type]++
	}
	require.Equal(t, 1, eventCounts["tool_call_recorded"])
	require.Equal(t, 1, eventCounts["run_replayed"])
	require.Equal(t, "metadata_only", after.Events[len(after.Events)-1].Payload["semantics"])
}

func TestArtifactChainContainsSkillDecisionAndNormalizedResult(t *testing.T) {
	artifacts := []AdaptiveArtifact{
		{Kind: WorkflowArtifactToolDecision, ArtifactID: "decision"},
		{Kind: WorkflowArtifactNormalizedToolResult, ArtifactID: "normalized"},
	}
	require.True(t, adaptiveArtifactsContain(artifacts, WorkflowArtifactToolDecision))
	require.True(t, adaptiveArtifactsContain(artifacts, WorkflowArtifactNormalizedToolResult))
}
