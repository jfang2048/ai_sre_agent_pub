package agent

import (
	"context"
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
			Summary:    "checkout latency spike after rollout",
			Confidence: 0.82,
		},
		risk: JointRiskAssessment{RiskLevel: "high"},
		telemetryQuality: PromptTelemetryQuality{
			State:      "fresh",
			Confidence: 0.91,
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
}

func TestSuggestedToolsForFocusRoutesValidationTargetsByFocus(t *testing.T) {
	require.Equal(t,
		[]ToolName{ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolLogs},
		suggestedToolsForFocus("change", ValidationTargetHypothesis),
	)
	require.Equal(t,
		[]ToolName{ToolRunbookRetrieval, ToolSimilarCase, ToolHistoricalIncident, ToolActionOutcome, ToolServiceHealth},
		suggestedToolsForFocus("resource", ValidationTargetRecommendation),
	)
	require.Equal(t,
		[]ToolName{ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery},
		suggestedToolsForFocus("security", ValidationTargetContradiction),
	)
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

	report := newValidationActionAgent(cfg, zap.NewNop()).Run(context.Background(), state, handoff)
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

	report := newValidationActionAgent(cfg, zap.NewNop()).Run(context.Background(), state, handoff)
	require.Len(t, report.Results, 1)
	require.Equal(t, ValidationVerdictContradicted, report.Results[0].Verdict)
	require.NotEmpty(t, report.ContradictionSummary)
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

	report := newValidationActionAgent(cfg, zap.NewNop()).Run(context.Background(), state, handoff)
	require.Len(t, report.Results, 1)
	require.Equal(t, ValidationVerdictPartiallySupported, report.Results[0].Verdict)
	require.Contains(t, report.ValidatedRecommendationIDs, "rec-rollback")
}
