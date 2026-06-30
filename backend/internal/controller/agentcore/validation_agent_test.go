package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type staticWorkflowTool struct {
	name    ToolName
	summary string
	data    any
}

func (t *staticWorkflowTool) Name() ToolName      { return t.name }
func (t *staticWorkflowTool) Version() string     { return "test" }
func (t *staticWorkflowTool) Description() string { return "static validation test tool" }
func (t *staticWorkflowTool) Deterministic() bool { return true }
func (t *staticWorkflowTool) Unsafe() bool        { return false }
func (t *staticWorkflowTool) Run(context.Context, workflowToolRequest) (workflowToolResult, error) {
	return workflowToolResult{Summary: t.summary, Data: t.data}, nil
}

func TestBuildAnalysisHandoffProducesStructuredValidationTargets(t *testing.T) {
	now := time.Now().UTC()
	state := &workflowState{
		collectorID: "collector-a",
		trigger:     "incident_alert",
		now:         now,
		incident: IncidentSynthesis{
			Summary:       "checkout latency spike after rollout",
			Confidence:    0.82,
			ImpactedScope: []string{"service:checkout", "dependency:payments"},
		},
		risk: JointRiskAssessment{RiskLevel: "high"},
		telemetryQuality: PromptTelemetryQuality{
			State:      "fresh",
			Confidence: 0.91,
			BlindSpots: []string{"trace_sampling"},
		},
		evidence: []RCAEvidence{{ID: "ev-1"}, {ID: "ev-2"}},
		hypotheses: []RCAHypothesis{{
			ID:                       "hyp-1",
			Title:                    "checkout rollout regression",
			Description:              "latency and retries increased after the new revision",
			Confidence:               0.86,
			EvidenceIDs:              []string{"ev-1"},
			ContradictingEvidenceIDs: []string{"ev-weak-1"},
		}},
		recommendation: []WorkflowRecommendation{{
			ID:       "rec-1",
			Priority: "high",
			Summary:  "Validate the rollout before broader remediation",
			Details:  "compare the active revision with the previous release",
		}, {
			ID:               "rec-2",
			Category:         "probable_containment",
			Priority:         "high",
			Summary:          "Rollback checkout-v2 if the rollout is confirmed",
			Details:          "revert only the checkout deployment after approval",
			Safe:             false,
			DryRunDefault:    true,
			RequiresApproval: true,
			Reversible:       true,
			RollbackHint:     "restore checkout:v1",
		}},
		changeLinks: []RCAChangeLink{{
			ChangeID:         "chg-1",
			Category:         "deployment",
			Summary:          "checkout rollout checkout-v2",
			ImpactSummary:    "latency increased immediately after rollout",
			CorrelationScore: 0.88,
		}},
	}

	handoff := buildAnalysisHandoff(state)
	require.Equal(t, "analysis_agent", handoff.Agent)
	require.Equal(t, "collector-a", handoff.CollectorID)
	require.NotEmpty(t, handoff.ImpactedScope)
	require.NotEmpty(t, handoff.BlindSpots)
	require.NotEmpty(t, handoff.HypothesisPackets)
	require.NotEmpty(t, handoff.BoundedActionCandidates)
	require.NotNil(t, handoff.BoundedActionCandidates[0].ActionContract)
	require.Equal(t, normalizeValidationCategory(handoff.BoundedActionCandidates[0].Category), handoff.BoundedActionCandidates[0].ActionContract.ExecutionCategory)
	require.Equal(t, handoff.BoundedActionCandidates[0].ActionContract.ActuatorSafetyTier, handoff.BoundedActionCandidates[0].ActuatorSafetyTier)
	require.NotEmpty(t, handoff.RankedSuspectedCauses)
	require.NotEmpty(t, handoff.SupportingEvidenceIDs)
	require.NotEmpty(t, handoff.SuggestedValidationTargets)

	targetTypes := map[ValidationTargetType]bool{}
	for _, target := range handoff.SuggestedValidationTargets {
		targetTypes[target.Type] = true
	}
	require.True(t, targetTypes[ValidationTargetHypothesis])
	require.True(t, targetTypes[ValidationTargetChangeCorrelation])
	require.True(t, targetTypes[ValidationTargetRecommendation])
	require.True(t, targetTypes[ValidationTargetContradiction])
	require.True(t, targetTypes[ValidationTargetRemediation])
}

func TestValidationTargetPlannerUsesStructuredFamiliesAndGaps(t *testing.T) {
	state := &workflowState{
		incident: IncidentSynthesis{ImpactedScope: []string{"service:checkout"}},
		telemetryQuality: PromptTelemetryQuality{
			BlindSpots: []string{"topology"},
		},
		evidence: []RCAEvidence{{
			ID:   "ev-change-1",
			Kind: "change_event",
		}},
		changeLinks: []RCAChangeLink{{Category: "deployment"}},
	}
	target := buildValidationTargetPlan(
		state,
		ValidationTargetHypothesis,
		"checkout rollout regression",
		"latency increased after checkout-v2",
		"hyp-1",
		"",
		"",
		"high",
		[]string{"ev-change-1"},
		nil,
		[]string{"deployment"},
	)

	require.Contains(t, target.ToolFamilies, "change")
	require.Contains(t, target.ToolFamilies, "config")
	require.Contains(t, target.SuggestedTools, ToolDeploymentHistory)
	require.Contains(t, target.SuggestedTools, ToolConfigState)
	require.Contains(t, target.EvidenceGaps, "temporal_overlap")
	require.Contains(t, target.EvidenceGaps, "topology")
}

func TestValidationAgentRespectsToolBudget(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.ValidationMaxIterations = 1
	cfg.ValidationMaxToolCalls = 1
	cfg.ValidationConfidenceThreshold = 0.95

	engine := &WorkflowEngine{
		cfg:   cfg,
		tools: newWorkflowToolManager(zap.NewNop(), &staticWorkflowTool{name: ToolMetrics, summary: "metrics show mild pressure"}),
	}
	state := &workflowState{
		engine:       engine,
		workflowID:   "wf-budget",
		workflowType: "rca",
		collectorID:  "collector-a",
		window:       15 * time.Minute,
		now:          time.Now().UTC(),
		toolCalls:    make([]WorkflowToolCall, 0, 4),
	}
	handoff := AnalysisHandoff{
		Agent:           "analysis_agent",
		IncidentSummary: "cpu pressure on checkout",
		SuggestedValidationTargets: []ValidationTarget{{
			ID:             "target-1",
			Type:           ValidationTargetHypothesis,
			Title:          "cpu saturation on checkout",
			SuggestedTools: []ToolName{ToolMetrics, ToolLogs},
		}},
	}

	report := newValidationActionAgent(cfg, zap.NewNop()).RunDecoded(context.Background(), state, handoff)
	require.Equal(t, ValidationActionReportSchemaVersion, report.SchemaVersion)
	require.Equal(t, state.workflowID, report.CorrelationID)
	require.Equal(t, 1, report.ToolCalls)
	require.Equal(t, 1, report.Iterations)
	require.Equal(t, "validation budget reached", report.StopReason)
	require.Len(t, report.Results, 1)
	require.Equal(t, "validation budget reached", report.Results[0].StopReason)
}

func TestValidationAgentContradictionSearchMarksHealthyObservationContradicted(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.ValidationMaxIterations = 4
	cfg.ValidationMaxToolCalls = 4

	engine := &WorkflowEngine{
		cfg: cfg,
		tools: newWorkflowToolManager(zap.NewNop(),
			&staticWorkflowTool{name: ToolServiceHealth, summary: "service healthy and returned no strong matches"},
		),
	}
	state := &workflowState{
		engine:       engine,
		workflowID:   "wf-contradiction",
		workflowType: "rca",
		collectorID:  "collector-a",
		window:       10 * time.Minute,
		now:          time.Now().UTC(),
		toolCalls:    make([]WorkflowToolCall, 0, 4),
	}
	handoff := AnalysisHandoff{
		Agent:           "analysis_agent",
		IncidentSummary: "possible network fault",
		SuggestedValidationTargets: []ValidationTarget{{
			ID:             "target-contradict",
			Type:           ValidationTargetContradiction,
			Title:          "network timeout hypothesis",
			Focus:          "network",
			SuggestedTools: []ToolName{ToolServiceHealth},
		}},
	}

	report := newValidationActionAgent(cfg, zap.NewNop()).RunDecoded(context.Background(), state, handoff)
	require.Len(t, report.Results, 1)
	require.Equal(t, ValidationVerdictContradicted, report.Results[0].Verdict)
	require.NotEmpty(t, report.ContradictionSummary)
	require.NotEmpty(t, report.Results[0].ContradictingEvidenceIDs)
}

func TestValidationAgentRecommendationValidationUsesHistoricalAndRunbookEvidence(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.ValidationMaxIterations = 4
	cfg.ValidationMaxToolCalls = 4

	knowledge := knowledgeToolData{
		Tool:   ToolRunbookRetrieval,
		Intent: "runbook",
		Hits: []RetrievedDocumentEvidence{{
			EvidenceID: "ev-kb-1",
			Title:      "checkout rollback runbook",
			Score:      0.81,
		}},
		Summary:     "runbook hit matches the rollback recommendation",
		Confidence:  0.81,
		EvidenceIDs: []string{"ev-kb-1"},
	}
	engine := &WorkflowEngine{
		cfg: cfg,
		tools: newWorkflowToolManager(zap.NewNop(),
			&staticWorkflowTool{name: ToolRunbookRetrieval, summary: knowledge.Summary, data: knowledge},
		),
	}
	state := &workflowState{
		engine:       engine,
		workflowID:   "wf-rec",
		workflowType: "rca",
		collectorID:  "collector-a",
		window:       20 * time.Minute,
		now:          time.Now().UTC(),
		toolCalls:    make([]WorkflowToolCall, 0, 4),
	}
	handoff := AnalysisHandoff{
		Agent:           "analysis_agent",
		IncidentSummary: "checkout rollback candidate",
		SuggestedValidationTargets: []ValidationTarget{{
			ID:               "target-rec",
			Type:             ValidationTargetRecommendation,
			Title:            "validate rollback recommendation",
			RecommendationID: "rec-rollback",
			SuggestedTools:   []ToolName{ToolRunbookRetrieval},
		}},
	}

	report := newValidationActionAgent(cfg, zap.NewNop()).RunDecoded(context.Background(), state, handoff)
	require.Len(t, report.Results, 1)
	require.Equal(t, ValidationVerdictConfirmed, report.Results[0].Verdict)
	require.Contains(t, report.ValidatedRecommendationIDs, "rec-rollback")
}

func TestValidationAgentRanksCandidatesInsteadOfFollowingSuggestedToolOrder(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.RuntimeMode = WorkflowRuntimeModeHybridAdaptive
	cfg.ValidationMaxIterations = 1
	cfg.ValidationMaxToolCalls = 1

	engine := &WorkflowEngine{
		cfg: cfg,
		tools: newWorkflowToolManager(zap.NewNop(),
			&staticWorkflowTool{name: ToolLogs, summary: "broad log search returned mixed errors"},
			&staticWorkflowTool{name: ToolDNSCheck, summary: "dns failures confirmed", data: dnsCheckToolData{CollectorID: "collector-a", Hints: []string{"dns timeout"}}},
		),
	}
	state := &workflowState{
		engine:       engine,
		workflowID:   "wf-ranked-validation",
		workflowType: "rca",
		collectorID:  "collector-a",
		window:       15 * time.Minute,
		now:          time.Now().UTC(),
		toolCalls:    make([]WorkflowToolCall, 0, 4),
		sceneClassification: SceneClassification{
			SceneFamily: SceneFamilyNetworkConnectivity,
			Confidence:  0.45,
		},
		evidenceGapState: EvidenceGapState{
			SceneFamily:     SceneFamilyNetworkConnectivity,
			MissingEvidence: []string{"dns health"},
		},
	}
	handoff := AnalysisHandoff{
		Agent:           "analysis_agent",
		IncidentSummary: "dns failures on checkout",
		SuggestedValidationTargets: []ValidationTarget{{
			ID:             "target-ranked",
			Type:           ValidationTargetHypothesis,
			Title:          "dns resolution failures",
			Summary:        "validate dns and service health evidence",
			ReadOnly:       true,
			EvidenceGaps:   []string{"dns health"},
			ToolFamilies:   []string{"network", "service_health"},
			SuggestedTools: []ToolName{ToolLogs, ToolDNSCheck},
		}},
	}

	report := newValidationActionAgent(cfg, zap.NewNop()).RunDecoded(context.Background(), state, handoff)
	require.Len(t, report.Results, 1)
	require.NotEmpty(t, state.toolCalls)
	require.Equal(t, ToolDNSCheck, state.toolCalls[0].Tool)
}

func TestValidationAgentRunReadsAnalysisHandoffFromJSONArtifact(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.AgentMessageDir = t.TempDir()
	cfg.WorkflowDataPath = t.TempDir()
	cfg.AgentMessageProtocolEnabled = true
	cfg.ValidationMaxIterations = 2
	cfg.ValidationMaxToolCalls = 2

	store := NewAgentMessageStore(cfg, nil, zap.NewNop())
	engine := &WorkflowEngine{
		cfg:          cfg,
		logger:       zap.NewNop(),
		tools:        newWorkflowToolManager(zap.NewNop(), &staticWorkflowTool{name: ToolMetrics, summary: "metrics confirm the rollout hypothesis"}),
		messageStore: store,
	}
	state := &workflowState{
		engine:       engine,
		workflowID:   "wf-message-read",
		workflowType: "rca",
		collectorID:  "collector-a",
		window:       15 * time.Minute,
		now:          time.Now().UTC(),
		toolCalls:    make([]WorkflowToolCall, 0, 4),
		analysisHandoff: AnalysisHandoff{
			Agent:           "analysis_agent",
			IncidentSummary: "stale in-memory handoff should not be used",
		},
	}
	handoff := AnalysisHandoff{
		Agent:           "analysis_agent",
		CollectorID:     "collector-a",
		IncidentSummary: "checkout latency spike after rollout",
		SuggestedValidationTargets: []ValidationTarget{{
			ID:             "target-1",
			Type:           ValidationTargetHypothesis,
			Title:          "rollout regression",
			SuggestedTools: []ToolName{ToolMetrics},
		}},
	}

	analysisRef, manifestRef, err := store.Append(state.workflowID, state.workflowType, "analysis_agent", "validation_action_agent", AgentMessageTypeAnalysisHandoff, nil, AnalysisHandoffMessage{
		Handoff: handoff,
	}, "analysis handoff")
	require.NoError(t, err)
	requestRef, _, err := store.Append(state.workflowID, state.workflowType, "workflow_runtime", "validation_action_agent", AgentMessageTypeValidationRequest, &analysisRef, ValidationRequestMessage{
		AnalysisMessage: analysisRef,
		TargetLimit:     cfg.ValidationTargetLimit,
		ReadOnlyOnly:    cfg.ValidationReadOnlyOnly,
		RequestedAt:     time.Now().UTC(),
	}, "validation request")
	require.NoError(t, err)
	require.NotNil(t, manifestRef)
	state.messageManifestPath = firstNonEmpty(manifestRef.LocalCachePath, manifestRef.Path)
	state.analysisHandoffMessage = &analysisRef
	state.validationRequestMessage = &requestRef

	report := newValidationActionAgent(cfg, zap.NewNop()).Run(context.Background(), state)
	require.NotEqual(t, "message_protocol_error", report.Mode)
	require.Len(t, report.Targets, 1)
	require.Equal(t, "rollout regression", report.Targets[0].Title)
	require.Len(t, report.Results, 1)
	require.NotNil(t, report.SourceAnalysisMessage)
	require.NotNil(t, report.SourceValidationRequest)
	require.Equal(t, analysisRef.MessageID, report.SourceAnalysisMessage.MessageID)
	require.Equal(t, requestRef.MessageID, report.SourceValidationRequest.MessageID)
	require.Equal(t, filepath.Base(analysisRef.Path), filepath.Base(report.SourceAnalysisMessage.Path))

	raw, err := os.ReadFile(analysisRef.Path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "\"message_type\": \"analysis_handoff\"")
	require.Contains(t, string(raw), "\"rollout regression\"")
}

func TestValidationAgentRunRejectsUnexpectedMessageSchemaVersion(t *testing.T) {
	root := t.TempDir()

	badCfg := DefaultWorkflowConfig()
	badCfg.AgentMessageDir = root
	badCfg.AgentMessageProtocolEnabled = true
	badCfg.AgentMessageSchemaVersion = "agent-message/v0"
	badStore := NewAgentMessageStore(badCfg, nil, zap.NewNop())

	handoff := AnalysisHandoff{
		Agent:           "analysis_agent",
		CollectorID:     "collector-a",
		IncidentSummary: "checkout latency spike after rollout",
		SuggestedValidationTargets: []ValidationTarget{{
			ID:             "target-1",
			Type:           ValidationTargetHypothesis,
			Title:          "rollout regression",
			SuggestedTools: []ToolName{ToolMetrics},
		}},
	}
	analysisRef, _, err := badStore.Append("wf-schema-mismatch", "rca", "analysis_agent", "validation_action_agent", AgentMessageTypeAnalysisHandoff, nil, AnalysisHandoffMessage{
		Handoff: handoff,
	}, "analysis handoff")
	require.NoError(t, err)
	_, _, err = badStore.Append("wf-schema-mismatch", "rca", "workflow_runtime", "validation_action_agent", AgentMessageTypeValidationRequest, &analysisRef, ValidationRequestMessage{
		AnalysisMessage: analysisRef,
		RequestedAt:     time.Now().UTC(),
	}, "validation request")
	require.NoError(t, err)

	goodCfg := DefaultWorkflowConfig()
	goodCfg.AgentMessageDir = root
	goodCfg.AgentMessageProtocolEnabled = true
	goodCfg.AgentMessageSchemaVersion = "agent-message/v1"
	engine := &WorkflowEngine{
		cfg:          goodCfg,
		logger:       zap.NewNop(),
		tools:        newWorkflowToolManager(zap.NewNop(), &staticWorkflowTool{name: ToolMetrics, summary: "metrics confirm the rollout hypothesis"}),
		messageStore: NewAgentMessageStore(goodCfg, nil, zap.NewNop()),
	}
	state := &workflowState{
		engine:       engine,
		workflowID:   "wf-schema-mismatch",
		workflowType: "rca",
		collectorID:  "collector-a",
		now:          time.Now().UTC(),
	}

	report := newValidationActionAgent(goodCfg, zap.NewNop()).Run(context.Background(), state)
	require.Equal(t, "message_protocol_error", report.Mode)
	require.Equal(t, "analysis handoff message unavailable", report.StopReason)
	require.Contains(t, report.DegradedFallbackReason, "unexpected agent message schema")
}

func TestValidationExecutionAllowedUsesExplicitCategories(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.ValidationAllowExecCategories = []string{"probable_containment"}

	require.True(t, validationExecutionAllowed(cfg, "probable_containment"))
	require.False(t, validationExecutionAllowed(cfg, "medium_term_remediation"))
	require.True(t, validationExecutionAllowed(cfg, "read_only_validation"))
}

func TestBuildPostActionValidationSummaryComparesBeforeAndAfterSnapshots(t *testing.T) {
	before := &ValidationEvidenceSnapshot{
		Label:      "before_action",
		CapturedAt: time.Now().UTC(),
		RiskScore:  0.82,
		LogErrors:  18,
		ServiceHealth: serviceHealthToolData{
			Healthy:   false,
			LatencyMS: 1600,
			ErrorRate: 0.12,
		},
		TriggeredSignals: []string{"cpu_usage", "service_latency"},
	}
	after := &ValidationEvidenceSnapshot{
		Label:      "after_action",
		CapturedAt: time.Now().UTC(),
		RiskScore:  0.34,
		LogErrors:  4,
		ServiceHealth: serviceHealthToolData{
			Healthy:   true,
			LatencyMS: 420,
			ErrorRate: 0.01,
		},
		TriggeredSignals: []string{"cpu_usage"},
	}

	summary := buildPostActionValidationSummary(before, after)
	require.Equal(t, ValidationVerdictConfirmed, summary.Verdict)
	require.NotNil(t, summary.Comparison)
	require.NotNil(t, summary.Delta)
	require.True(t, summary.Comparison.Comparable)
	require.True(t, summary.Comparison.RiskScore.Improved)
	require.True(t, summary.Comparison.ServiceHealthy.Improved)
	require.True(t, summary.Comparison.LogErrors.Improved)
	require.True(t, summary.Comparison.TriggeredSignals.Improved)
	require.True(t, summary.Delta.HealthImproved)
	require.Less(t, summary.Delta.RiskDelta, 0.0)
}

func TestSummarizeValidationEffectFallsBackToRiskOnlyWhenSnapshotsMissing(t *testing.T) {
	summary := summarizeValidationEffect(validationEffectInput{
		ActionID:          "action-1",
		ExecutionCategory: "probable_containment",
		BeforeRisk:        0.81,
		AfterRisk:         0.32,
		Resolved:          true,
		FallbackMode:      "risk_only",
		Note:              "post-action snapshots unavailable",
	})

	require.Equal(t, ValidationVerdictConfirmed, summary.Verdict)
	require.Equal(t, "action-1", summary.ActionID)
	require.Equal(t, "probable_containment", summary.ExecutionCategory)
	require.Equal(t, "risk_only", summary.FallbackMode)
	require.NotNil(t, summary.Comparison)
	require.NotNil(t, summary.Delta)
	require.True(t, summary.Comparison.Comparable)
	require.True(t, summary.Comparison.Incomplete)
	require.Contains(t, summary.Comparison.MissingData, "before_snapshot")
	require.Contains(t, summary.Comparison.MissingData, "after_snapshot")
	require.Less(t, summary.Delta.RiskDelta, 0.0)
}
