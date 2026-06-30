package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWorkflowToolContractsGovernRoutingAndAutoSelection(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	manager := newWorkflowToolManager(
		zap.NewNop(),
		&mockTool{name: ToolMetrics, deterministic: true},
		&mockTool{name: ToolProfiling, deterministic: true, unsafe: true},
	)
	manager.cfg = cfg

	metricsDesc, ok := workflowToolDescriptorByName(manager.registry(), ToolMetrics)
	require.True(t, ok)
	require.NoError(t, validateWorkflowToolContract(metricsDesc.Contract))
	require.True(t, metricsDesc.Contract.EligibleForAutoSelection)
	require.Contains(t, metricsDesc.Contract.EvidenceProduced, "metric_baseline")
	require.NotEmpty(t, metricsDesc.Contract.PreferredQueryHints)
	require.NotEmpty(t, metricsDesc.Contract.FreshnessSensitivity)
	require.NotEmpty(t, metricsDesc.Contract.ScopeSensitivity)

	profilingDesc, ok := workflowToolDescriptorByName(manager.registry(), ToolProfiling)
	require.True(t, ok)
	require.NoError(t, validateWorkflowToolContract(profilingDesc.Contract))
	require.False(t, profilingDesc.Contract.EligibleForAutoSelection)
	require.False(t, workflowToolContractAllowsStage(profilingDesc.Contract, "llm_analysis"))

	call, _, err := manager.call(context.Background(), workflowToolRequest{
		WorkflowID: "wf-contract",
		Workflow:   "rca",
		Stage:      "llm_analysis",
		DryRun:     true,
		Query:      map[string]string{"reason": "model wants profiling"},
	}, ToolProfiling)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not allowed")
	require.Equal(t, "blocked", call.InvocationStatus)

	call, _, err = manager.call(context.Background(), workflowToolRequest{
		WorkflowID:  "wf-contract",
		Workflow:    "rca",
		Stage:       adaptiveRuntimeStage,
		CollectorID: "collector-a",
		DryRun:      true,
	}, ToolMetrics)
	require.NoError(t, err)
	require.NotEmpty(t, call.ToolContract)
	require.Equal(t, WorkflowToolOutcomeReadOnlySuccess, call.Outcome)
}

func TestAdaptiveToolRankingUsesSceneGapsAndPolicyEligibility(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-net", Window: 5 * time.Minute})
	state.sceneClassification = SceneClassification{
		SceneFamily:     SceneFamilyNetworkConnectivity,
		Confidence:      0.52,
		MissingEvidence: []string{"dns health", "network timeout counter evidence"},
	}
	state.evidenceGapState = EvidenceGapState{
		SceneFamily:             SceneFamilyNetworkConnectivity,
		MissingEvidence:         []string{"dns health"},
		EvidenceGoalsStillUnmet: []string{"network timeout counter evidence"},
	}

	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybrid
	candidates := rankAdaptiveToolCandidates(state, cfg, map[ToolName]int{})
	require.NotEmpty(t, candidates)
	require.True(t, candidates[0].Contract.ReadOnly)
	require.True(t, candidates[0].Contract.EligibleForAutoSelection)
	require.NotContains(t, []ToolName{ToolProfiling, ToolRemediation}, candidates[0].Tool)
	require.Greater(t, candidates[0].Score.Total, 0.20)
	require.Greater(t, candidates[0].Score.PolicyEligibility, 0.0)
}

func TestAdaptiveQueryShapingUsesScopeHintsAndFreshness(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-net", Window: 40 * time.Minute})
	state.adaptiveScopeHints = []string{"service:checkout", "pod:checkout-1"}

	desc, ok := workflowToolDescriptorByName(engine.tools.registry(), ToolConnectivityCheck)
	require.True(t, ok)
	query := adaptiveQueryForTool(state, desc.Contract, []string{"dns health"})
	require.Equal(t, "service:checkout,pod:checkout-1", query["scope"])
	require.Equal(t, "20m0s", query["window"])
	require.Contains(t, query["query_hints"], "network/dns/latency")
}

func TestAdaptiveRuntimeLoopPersistsBoundedDialogueArtifactsAndStop(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybrid
	cfg.AdaptiveMaxIterations = 1
	cfg.AdaptiveMaxToolCalls = 1
	cfg.AdaptiveMaxHypothesisRewrites = 1
	cfg.WorkflowDataPath = t.TempDir()
	cfg.WorkflowStorePath = filepath.Join(cfg.WorkflowDataPath, "workflow_runs.db")
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-adaptive", Window: 5 * time.Minute})
	run, err := engine.orchestrator.StartRun(context.Background(), state.workflowID, "rca", state.collectorID)
	require.NoError(t, err)
	state.durableRun = run
	state.incident = IncidentSynthesis{Summary: "network timeouts during rollout", Confidence: 0.42}
	state.sceneClassification = SceneClassification{
		SceneFamily:     SceneFamilyNetworkConnectivity,
		Confidence:      0.50,
		MissingEvidence: []string{"dns health"},
	}
	state.evidenceGapState = EvidenceGapState{
		SceneFamily:             SceneFamilyNetworkConnectivity,
		MissingEvidence:         []string{"dns health"},
		EvidenceGoalsStillUnmet: []string{"corroborating_knowledge"},
	}

	require.NoError(t, engine.stepAdaptiveRuntimeLoop(context.Background(), state))
	require.NotNil(t, state.adaptiveState)
	require.LessOrEqual(t, state.adaptiveState.ToolCalls, 1)
	require.NotEmpty(t, state.adaptiveDialogue)
	require.NotEmpty(t, state.adaptiveToolDecisions)
	require.NotEmpty(t, state.adaptiveArtifacts)
	require.True(t, adaptiveArtifactsContain(state.adaptiveArtifacts, WorkflowArtifactPlannerProposal))
	require.True(t, adaptiveArtifactsContain(state.adaptiveArtifacts, WorkflowArtifactStopDecision))
	require.True(t, adaptiveArtifactsContain(state.adaptiveArtifacts, WorkflowArtifactNormalizedToolResult))

	persisted, err := engine.orchestrator.GetRun(context.Background(), state.workflowID)
	require.NoError(t, err)
	require.NotNil(t, persisted.AdaptiveRuntime)
	require.NotEmpty(t, persisted.AdaptiveDialogue)
	require.NotEmpty(t, persisted.AdaptiveToolDecisions)
}

func TestModelDirectedUnsafeToolRequestStaysProposalOnly(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybrid
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-prof", Window: 5 * time.Minute})

	allowed, reason := state.recordModelDirectedToolDecision(context.Background(), LLMToolRequest{
		Tool:   ToolProfiling,
		Query:  map[string]string{"reason": "need profile"},
		Reason: "model wants a profiling capture",
	}, 1)
	require.False(t, allowed)
	require.Contains(t, reason, "automatic")
	require.NotEmpty(t, state.adaptiveToolDecisions)
	require.True(t, state.adaptiveToolDecisions[0].ProposalOnly)
	require.False(t, state.adaptiveToolDecisions[0].Executable)
	require.True(t, adaptiveArtifactsContain(state.adaptiveArtifacts, WorkflowArtifactToolDecision))
}

func TestNormalizeToolResultCapturesNextFamiliesAndScope(t *testing.T) {
	state := &workflowState{collectorID: "collector-a"}
	normalized := normalizeToolResult(ToolLogs, workflowToolResult{
		Summary: "error burst observed",
		Data: logsToolData{
			Errors:   4,
			Warnings: 2,
			Snippets: []string{"timeout contacting checkout-db"},
		},
	}, "call-1", state)
	require.NotNil(t, normalized)
	require.Equal(t, ToolLogs, normalized.Tool)
	require.Contains(t, normalized.StructuredFindings[0], "errors=")
	require.Contains(t, normalized.LikelyNextToolFamilies, "change")
	require.Contains(t, normalized.AffectedScope, "collector-a")
	require.True(t, normalized.NarrowsHypothesisSpace)
}

func TestToolExperienceMemoryInfluencesRanking(t *testing.T) {
	root := t.TempDir()
	store := NewToolExperienceMemoryStore(root, zap.NewNop())
	contract := WorkflowToolContract{
		ToolName:         ToolDNSCheck,
		CapabilityFamily: "service_health",
	}
	record := store.Observe(SceneFamilyNetworkConnectivity, "dns failures on checkout", []string{"dns health"}, contract, AdaptiveProgressAssessment{
		ConfidenceDelta:          0.18,
		EvidenceGapCoverageDelta: 1,
		Progress:                 true,
	}, &NormalizedToolResult{Summary: "dns failures confirmed", LikelyNextToolFamilies: []string{"logs"}})
	require.Equal(t, 1, record.ProgressCount)
	prior := store.Prior(SceneFamilyNetworkConnectivity, "dns failures on checkout", []string{"dns health"}, contract)
	require.Greater(t, prior, 0.0)
	require.FileExists(t, filepath.Join(root, "tool_experience.json"))
}

func TestAdaptiveStateReplayPreservesCheckpoint(t *testing.T) {
	orchestrator := NewDurableOrchestrator(NewInMemoryDurableStore(), zap.NewNop())
	run, err := orchestrator.StartRun(context.Background(), "wf-replay", "rca", "collector-a")
	require.NoError(t, err)
	require.Equal(t, "wf-replay", run.RunID)

	checkpoint := AdaptiveRuntimeState{
		SchemaVersion: adaptiveRuntimeSchemaVersion,
		RuntimeMode:   WorkflowRuntimeModeAdaptive,
		Objective:     "resolve uncertainty",
		Replay: AdaptiveReplayMetadata{
			Replayable:        true,
			ReplayPoint:       adaptiveRuntimeStage,
			LastCheckpointAt:  time.Now().UTC(),
			CheckpointVersion: adaptiveRuntimeSchemaVersion,
		},
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, orchestrator.RecordAdaptiveState(context.Background(), run.RunID, checkpoint))

	replayed, err := orchestrator.ReplayRun(context.Background(), run.RunID)
	require.NoError(t, err)
	require.Equal(t, 1, replayed.ReplayCount)
	require.NotNil(t, replayed.AdaptiveRuntime)
	require.Equal(t, WorkflowRuntimeModeAdaptive, replayed.AdaptiveRuntime.RuntimeMode)
	require.True(t, replayed.AdaptiveRuntime.Replay.Replayable)
}

func TestAdaptiveProgressAssessmentDetectsProgressAndPlateau(t *testing.T) {
	before := AdaptiveRuntimeState{
		ConfidenceScore:        0.40,
		RiskScore:              0.60,
		UnresolvedEvidenceGaps: []string{"logs", "dns"},
		ContradictionSet:       []string{"c1"},
	}
	after := AdaptiveRuntimeState{
		ConfidenceScore:        0.55,
		RiskScore:              0.52,
		UnresolvedEvidenceGaps: []string{"dns"},
	}
	progress := buildAdaptiveProgressAssessment(before, after, "call-1")
	require.True(t, progress.Progress)
	require.False(t, progress.Plateau)
	require.Greater(t, progress.ConfidenceDelta, 0.0)
	require.Equal(t, 1, progress.EvidenceGapCoverageDelta)
	require.Less(t, progress.ContradictionDelta, 0)

	plateau := buildAdaptiveProgressAssessment(after, after, "call-2")
	require.False(t, plateau.Progress)
	require.True(t, plateau.Plateau)
	require.True(t, strings.Contains(plateau.Summary, "no measurable progress"))
}

func TestAdaptiveRuntimeLoopStopsOnNoProgress(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybrid
	cfg.AdaptiveMaxIterations = 2
	cfg.AdaptiveMaxToolCalls = 2
	cfg.AdaptiveMaxNoProgressRounds = 1
	cfg.WorkflowDataPath = t.TempDir()
	cfg.WorkflowStorePath = filepath.Join(cfg.WorkflowDataPath, "workflow_runs.db")
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	engine.tools = newWorkflowToolManager(zap.NewNop(), &mockTool{name: ToolName("test_tool"), deterministic: true, result: workflowToolResult{Summary: "no-op"}})
	engine.tools.cfg = cfg

	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-no-progress", Window: 5 * time.Minute})
	state.incident = IncidentSynthesis{Summary: "ambiguous issue"}
	state.sceneClassification = SceneClassification{SceneFamily: SceneFamilyResourceContention, Confidence: 0.35, MissingEvidence: []string{"metric baseline"}}
	state.evidenceGapState = EvidenceGapState{SceneFamily: SceneFamilyResourceContention, MissingEvidence: []string{"metric baseline"}}

	require.NoError(t, engine.stepAdaptiveRuntimeLoop(context.Background(), state))
	require.NotNil(t, state.adaptiveState)
	require.Equal(t, "no_progress", state.adaptiveState.StopReason)
	require.True(t, adaptiveArtifactsContain(state.adaptiveArtifacts, WorkflowArtifactStopDecision))
}

func TestAdaptiveRuntimeLoopStopsAfterMaxToolCalls(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybridAdaptive
	cfg.AdaptiveRuntimeEnabled = true
	cfg.AdaptiveMaxIterations = 3
	cfg.AdaptiveMaxToolCalls = 1
	cfg.WorkflowDataPath = t.TempDir()
	cfg.WorkflowStorePath = filepath.Join(cfg.WorkflowDataPath, "workflow_runs.db")
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())

	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-budget", Window: 5 * time.Minute})
	state.incident = IncidentSynthesis{Summary: "dns failures on checkout"}
	state.sceneClassification = SceneClassification{SceneFamily: SceneFamilyNetworkConnectivity, Confidence: 0.42, MissingEvidence: []string{"dns health"}}
	state.evidenceGapState = EvidenceGapState{SceneFamily: SceneFamilyNetworkConnectivity, MissingEvidence: []string{"dns health"}}

	require.NoError(t, engine.stepAdaptiveRuntimeLoop(context.Background(), state))
	require.NotNil(t, state.adaptiveState)
	require.LessOrEqual(t, state.adaptiveState.ToolCalls, 1)
}

func TestAdaptiveRuntimeStateShouldStopOnPlateauThreshold(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.MaxUncertaintyPlateauRounds = 2
	state := AdaptiveRuntimeState{
		Budget:                   AdaptiveBudgetState{RemainingToolCalls: 2},
		RemainingToolBudget:      2,
		UncertaintyPlateauRounds: 2,
	}
	stop, reason := state.shouldStop(cfg)
	require.True(t, stop)
	require.Equal(t, "uncertainty_plateau", reason)
}

func TestFinalizeNormalizedToolResultMarksLowYieldAndRefinements(t *testing.T) {
	state := &workflowState{collectorID: "collector-a"}
	result := finalizeNormalizedToolResult(state, &NormalizedToolResult{
		Tool:       ToolLogs,
		ToolCallID: "call-1",
		Summary:    "no findings",
		Freshness:  "recent",
	})
	require.NotNil(t, result)
	require.True(t, result.LowYieldSignal)
	require.Equal(t, "low", result.ResultQuality)
	require.Equal(t, "short_ttl", result.Cacheability)
	require.Equal(t, "10m", result.RecommendedTimeWindowRefine)
	require.Contains(t, result.RecommendedScopeRefinement, "collector-a")
}

func TestAdaptiveStopReasonForDecisionPrefersPolicyAndApprovalSemantics(t *testing.T) {
	require.Equal(t, AdaptiveStopReasonApprovalRequired, adaptiveStopReasonForDecision(AdaptiveToolDecision{
		Policy: ActionPolicyDecision{RequiresApproval: true},
	}))
	require.Equal(t, AdaptiveStopReasonPolicyBlocked, adaptiveStopReasonForDecision(AdaptiveToolDecision{
		Policy: ActionPolicyDecision{Status: "blocked"},
	}))
}

func adaptiveArtifactsContain(artifacts []AdaptiveArtifact, kind WorkflowArtifactKind) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			return true
		}
	}
	return false
}
