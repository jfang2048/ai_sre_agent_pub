package agent

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestScoreToolCandidatePrefersCheapReadOnlyDiscriminator(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybridAdaptive
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 30 * time.Minute})
	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)

	logsContract, ok := engine.tools.contracts.Get(ToolLogs)
	require.True(t, ok)
	remediationContract, ok := engine.tools.contracts.Get(ToolRemediation)
	require.True(t, ok)

	logsScore := scoreToolCandidate(state, context, logsContract, shapeToolQuery(state, logsContract, context).Query)
	remediationScore := scoreToolCandidate(state, context, remediationContract, shapeToolQuery(state, remediationContract, context).Query)

	require.Greater(t, logsScore.Breakdown.CheapFirstPreference, remediationScore.Breakdown.CheapFirstPreference)
	require.Greater(t, logsScore.Total, remediationScore.Total)
}

func TestScoreToolCandidateAppliesRepeatedToolPenalty(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 30 * time.Minute})
	state.toolCalls = append(state.toolCalls,
		WorkflowToolCall{Tool: ToolLogs},
		WorkflowToolCall{Tool: ToolLogs},
	)
	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
	contract, ok := engine.tools.contracts.Get(ToolLogs)
	require.True(t, ok)

	score := scoreToolCandidate(state, context, contract, shapeToolQuery(state, contract, context).Query)
	require.Greater(t, score.Breakdown.RepeatedToolPenalty, 0.0)
}

func TestScoreToolCandidateAppliesLowYieldPenalty(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 30 * time.Minute})
	state.adaptiveNormalizedResults = append(state.adaptiveNormalizedResults, NormalizedToolResult{
		Tool:           ToolDNSCheck,
		ResultQuality:  "low",
		LowYieldSignal: true,
	})
	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
	contract, ok := engine.tools.contracts.Get(ToolDNSCheck)
	require.True(t, ok)

	score := scoreToolCandidate(state, context, contract, shapeToolQuery(state, contract, context).Query)
	require.Greater(t, score.Breakdown.LowYieldPenalty, 0.0)
}

func TestScoreToolCandidateCollapsesPolicyIneligibleCandidate(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.ValidationReadOnlyOnly = true
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 30 * time.Minute})
	target := ValidationTarget{Type: ValidationTargetRemediation, ReadOnly: false, ExecutionCategory: "probable_containment"}
	context := buildToolSelectionContext(state, "validation_action_react_loop", &target)
	context.ReadOnlyOnly = true
	contract, ok := engine.tools.contracts.Get(ToolRemediation)
	require.True(t, ok)

	score := scoreToolCandidate(state, context, contract, shapeToolQuery(state, contract, context).Query)
	require.Equal(t, 0.0, score.Breakdown.PolicyEligibility)
}

func TestGenerateToolCandidatesRanksEvidenceGapCoverage(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-net", Window: 20 * time.Minute})
	state.sceneClassification = SceneClassification{SceneFamily: SceneFamilyNetworkConnectivity, Confidence: 0.45}
	target := ValidationTarget{
		Type:         ValidationTargetHypothesis,
		Title:        "dns resolution failures",
		Summary:      "validate dns and service health evidence",
		ReadOnly:     true,
		EvidenceGaps: []string{"dns health", "service health"},
		ToolFamilies: []string{"network", "service_health"},
	}

	candidates := generateToolCandidates(state, buildToolSelectionContext(state, "validation_action_react_loop", &target))
	require.NotEmpty(t, candidates)
	require.Contains(t, []ToolName{ToolDNSCheck, ToolConnectivityCheck, ToolServiceHealth}, candidates[0].Tool)
	require.Greater(t, candidates[0].Score.Breakdown.EvidenceGapCoverage, 0.0)
}

func TestScoreToolCandidatePrioritizesSkillsOverGenericRAG(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-app", Window: 20 * time.Minute})
	target := ValidationTarget{
		Type:         ValidationTargetRecommendation,
		Title:        "validate service health before picking remediation",
		Summary:      "prefer current diagnostic tools over retrieved background context",
		ReadOnly:     true,
		EvidenceGaps: []string{"service_health", "metric_baseline", "log_context"},
		ToolFamilies: []string{"service_health", "metrics", "logs"},
	}
	context := buildToolSelectionContext(state, "validation_action_react_loop", &target)

	serviceContract, ok := engine.tools.contracts.Get(ToolServiceHealth)
	require.True(t, ok)
	ragContract, ok := engine.tools.contracts.Get(ToolRAGQuery)
	require.True(t, ok)

	serviceScore := scoreToolCandidate(state, context, serviceContract, shapeToolQuery(state, serviceContract, context).Query)
	ragScore := scoreToolCandidate(state, context, ragContract, shapeToolQuery(state, ragContract, context).Query)

	require.Greater(t, serviceScore.Breakdown.SkillFirstPriority, ragScore.Breakdown.SkillFirstPriority)
	require.Greater(t, serviceScore.Total, ragScore.Total)
}

func TestScoreToolCandidateAllowsExplicitRunbookNeed(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-app", Window: 20 * time.Minute})
	target := ValidationTarget{
		Type:         ValidationTargetRecommendation,
		Title:        "validate runbook alignment",
		Summary:      "confirm the recommendation has a matching prior outcome",
		ReadOnly:     true,
		EvidenceGaps: []string{"runbook_alignment", "prior_outcome_match"},
		ToolFamilies: []string{"knowledge"},
	}
	context := buildToolSelectionContext(state, "validation_action_react_loop", &target)

	runbookContract, ok := engine.tools.contracts.Get(ToolRunbookRetrieval)
	require.True(t, ok)
	ragContract, ok := engine.tools.contracts.Get(ToolRAGQuery)
	require.True(t, ok)

	runbookScore := scoreToolCandidate(state, context, runbookContract, shapeToolQuery(state, runbookContract, context).Query)
	ragScore := scoreToolCandidate(state, context, ragContract, shapeToolQuery(state, ragContract, context).Query)

	require.Greater(t, runbookScore.Breakdown.SkillFirstPriority, ragScore.Breakdown.SkillFirstPriority)
	require.Greater(t, runbookScore.Total, ragScore.Total)
}

func TestGenerateToolCandidatesHonorsAdaptiveReadOnlyCandidateLimit(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybridAdaptive
	cfg.AdaptiveParallelReadOnlyLimit = 2
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-net", Window: 20 * time.Minute})
	state.sceneClassification = SceneClassification{SceneFamily: SceneFamilyNetworkConnectivity, Confidence: 0.45}
	target := ValidationTarget{
		Type:         ValidationTargetHypothesis,
		Title:        "dns resolution failures",
		Summary:      "validate dns and service health evidence",
		ReadOnly:     true,
		EvidenceGaps: []string{"dns health", "service health", "recent_logs"},
		ToolFamilies: []string{"network", "service_health", "logs"},
	}

	candidates := generateToolCandidates(state, buildToolSelectionContext(state, "validation_action_react_loop", &target))
	require.Len(t, candidates, 2)
}

func TestGenerateToolCandidatesRespectsStageEligibility(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybridAdaptive
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 20 * time.Minute})

	candidates := generateToolCandidates(state, buildToolSelectionContext(state, adaptiveRuntimeStage, nil))
	require.NotEmpty(t, candidates)
	for _, candidate := range candidates {
		require.NotEqual(t, ToolProfiling, candidate.Tool)
		require.NotEqual(t, ToolRemediation, candidate.Tool)
	}
}

func TestBuildToolSelectionDecisionStopsWhenNoCandidatesRemain(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 20 * time.Minute})
	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)

	decision := buildToolSelectionDecision(state, context, nil)
	require.Nil(t, decision.Selected)
	require.Equal(t, string(AdaptiveStopReasonNoSafeNextStep), decision.StopReason)
}
