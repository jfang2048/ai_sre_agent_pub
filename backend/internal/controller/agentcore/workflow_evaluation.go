package agent

import (
	"context"
	"fmt"
	"math"
	"time"
)

// WorkflowEvaluation provides a quality assessment of a completed agent workflow.
type WorkflowEvaluation struct {
	WorkflowID       string             `json:"workflow_id"`
	Type             string             `json:"type"`  // "rca", "remediation"
	Score            float64            `json:"score"` // 0.0 to 1.0
	Summary          string             `json:"summary"`
	Metrics          map[string]float64 `json:"metrics"`
	EvaluatedAt      time.Time          `json:"evaluated_at"`
	EvaluatorVersion string             `json:"evaluator_version"`
}

// EvaluateWorkflow performs an automated quality assessment of a completed workflow run.
func (e *WorkflowEngine) EvaluateWorkflow(ctx context.Context, workflowID string) (*WorkflowEvaluation, error) {
	if e.traceStore == nil {
		return nil, fmt.Errorf("trace store unavailable")
	}

	trace, ok := e.traceStore.GetTrace(workflowID)
	if !ok {
		return nil, fmt.Errorf("trace not found: %s", workflowID)
	}

	eval := &WorkflowEvaluation{
		WorkflowID:       workflowID,
		Type:             trace.WorkflowType,
		EvaluatedAt:      time.Now().UTC(),
		EvaluatorVersion: "v1",
		Metrics:          make(map[string]float64),
	}

	if trace.WorkflowType == "rca" {
		e.evaluateRCA(trace, eval)
	}

	// If there were remediations, evaluate them too
	if len(trace.PlanVersions) > 0 {
		e.evaluateRemediation(trace, eval)
	}

	return eval, nil
}

func (e *WorkflowEngine) evaluateRCA(trace *AgentTrace, eval *WorkflowEvaluation) {
	score := 0.0
	metrics := eval.Metrics

	// 1. Confidence check: how sure is the agent about the RCA?
	metrics["final_risk_score"] = trace.FinalRiskScore
	score += trace.FinalRiskScore * 0.4

	// 2. Evidence volume: how much data supported the conclusion?
	evidenceCount := float64(len(trace.NormalizedEvidence))
	metrics["evidence_count"] = evidenceCount
	score += math.Min(evidenceCount/10.0, 1.0) * 0.3

	// 3. Unresolved gaps penalty: how much was left unexplained?
	gapPenalty := float64(len(trace.UnresolvedGaps)) * 0.1
	metrics["gap_penalty"] = gapPenalty
	score -= gapPenalty

	// 4. Complexity/Breadth: did it consider multiple factors?
	hypothesisCount := float64(len(trace.HypothesisUpdates))
	metrics["hypothesis_count"] = hypothesisCount
	score += math.Min(hypothesisCount/5.0, 1.0) * 0.3

	eval.Score = clamp01(score)
	eval.Summary = fmt.Sprintf("RCA evaluation complete. Score: %.2f. Symptoms: %d, Evidence: %d, Hypotheses: %d",
		eval.Score, len(trace.Incident.GroupedSignals), len(trace.NormalizedEvidence), len(trace.HypothesisUpdates))
}

func (e *WorkflowEngine) evaluateRemediation(trace *AgentTrace, eval *WorkflowEvaluation) {
	// Simple success rate calculation across all remediation attempts
	totalSteps := 0
	verifiedSteps := 0
	failedSteps := 0

	for _, rev := range trace.PlanVersions {
		for _, step := range rev.Steps {
			if step.Status == "" || step.Status == "planned" {
				continue
			}
			totalSteps++
			if step.Verified {
				verifiedSteps++
			} else if step.Status == "failed" || step.Status == "verification_failed" {
				failedSteps++
			}
		}
	}

	if totalSteps > 0 {
		successRate := float64(verifiedSteps) / float64(totalSteps)
		eval.Metrics["remediation_success_rate"] = successRate
		eval.Metrics["remediation_failure_rate"] = float64(failedSteps) / float64(totalSteps)

		// Adjust overall score: 60% RCA quality, 40% Remediation success
		eval.Score = (eval.Score * 0.6) + (successRate * 0.4)
		eval.Summary += fmt.Sprintf(" | Remediation success rate: %.2f (%d/%d steps verified)",
			successRate, verifiedSteps, totalSteps)
	}
}
