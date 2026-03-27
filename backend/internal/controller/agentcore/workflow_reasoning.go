package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type reasoningBudget struct {
	tokenBudget      int
	promptTokens     int
	completionTokens int
	calls            int
}

func newReasoningBudget(limit int) reasoningBudget {
	return reasoningBudget{tokenBudget: limit}
}

func (b reasoningBudget) promptUsage() int {
	return b.promptTokens
}

func (b reasoningBudget) completionUsage() int {
	return b.completionTokens
}

func (b reasoningBudget) exhausted() bool {
	return b.tokenBudget > 0 && b.promptTokens+b.completionTokens >= b.tokenBudget
}

func (b *reasoningBudget) reservePrompt(text string) bool {
	estimate := estimateReasoningTokens(text)
	if b.tokenBudget > 0 && b.promptTokens+b.completionTokens+estimate > b.tokenBudget {
		return false
	}
	b.promptTokens += estimate
	return true
}

func (b *reasoningBudget) recordCompletion(text string) {
	b.completionTokens += estimateReasoningTokens(text)
	b.calls++
}

func (b reasoningBudget) remaining() int {
	if b.tokenBudget <= 0 {
		return 0
	}
	r := b.tokenBudget - b.promptTokens - b.completionTokens
	if r < 0 {
		return 0
	}
	return r
}

func estimateReasoningTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	runes := len([]rune(text))
	if runes <= 0 {
		return 0
	}
	estimate := runes / 4
	if runes%4 != 0 {
		estimate++
	}
	return maxInt(estimate, 1)
}

func normalizeLLMAnalysisResult(state *workflowState, result *LLMAnalysisResult, review *LLMReasoningReview) {
	if result == nil {
		return
	}
	if len(result.RCAHypotheses) == 0 && len(result.Reasoning.Hypotheses) > 0 {
		result.RCAHypotheses = append([]LLMHypothesis(nil), result.Reasoning.Hypotheses...)
	}
	if len(result.NextSteps) == 0 && len(result.Reasoning.RecommendedNextChecks) > 0 {
		result.NextSteps = append([]string(nil), result.Reasoning.RecommendedNextChecks...)
	}
	if result.Confidence <= 0 && result.Reasoning.Confidence > 0 {
		result.Confidence = clamp01(result.Reasoning.Confidence)
	}
	if len(result.EvidenceCited) == 0 {
		for _, hypothesis := range result.RCAHypotheses {
			result.EvidenceCited = append(result.EvidenceCited, hypothesis.Evidence...)
		}
		result.EvidenceCited = dedupeStrings(result.EvidenceCited)
	}
	result.Reasoning = buildReasoningArtifact(state, result)
	if review != nil {
		review.Initial = normalizeReasoningArtifact(review.Initial)
		review.Final = normalizeReasoningArtifact(review.Final)
		result.Review = review
	}
}

func normalizeReasoningArtifact(artifact LLMReasoningArtifact) LLMReasoningArtifact {
	artifact.Plan = dedupeStrings(artifact.Plan)
	artifact.Hypotheses = cloneLLMHypotheses(artifact.Hypotheses)
	sort.SliceStable(artifact.Hypotheses, func(i, j int) bool {
		if artifact.Hypotheses[i].Confidence == artifact.Hypotheses[j].Confidence {
			return artifact.Hypotheses[i].Title < artifact.Hypotheses[j].Title
		}
		return artifact.Hypotheses[i].Confidence > artifact.Hypotheses[j].Confidence
	})
	artifact.Confidence = clamp01(artifact.Confidence)
	artifact.MissingEvidence = dedupeStrings(artifact.MissingEvidence)
	artifact.RecommendedNextChecks = dedupeStrings(artifact.RecommendedNextChecks)
	return artifact
}

func buildReasoningArtifact(state *workflowState, result *LLMAnalysisResult) LLMReasoningArtifact {
	artifact := normalizeReasoningArtifact(result.Reasoning)
	if len(artifact.Hypotheses) == 0 {
		artifact.Hypotheses = cloneLLMHypotheses(result.RCAHypotheses)
	}
	if artifact.Confidence <= 0 {
		topConfidence := 0.0
		incidentConfidence := 0.0
		riskScore := 0.0
		if state != nil {
			topConfidence = topHypothesisConfidence(state.hypotheses)
			incidentConfidence = state.incident.Confidence
			riskScore = state.risk.RiskScore
		}
		artifact.Confidence = clamp01(firstNonZero(result.Confidence, topConfidence, incidentConfidence, riskScore))
	}
	if len(artifact.Plan) == 0 {
		artifact.Plan = defaultReasoningPlan(state, result)
	}
	if strings.TrimSpace(artifact.ExpectedSLOImpact) == "" {
		artifact.ExpectedSLOImpact = defaultExpectedSLOImpact(state, artifact.Confidence)
	}
	if len(artifact.MissingEvidence) == 0 {
		artifact.MissingEvidence = unresolvedGapsFromState(state)
	}
	if len(artifact.RecommendedNextChecks) == 0 {
		artifact.RecommendedNextChecks = defaultRecommendedChecks(state, result)
	}
	return normalizeReasoningArtifact(artifact)
}

func defaultReasoningPlan(state *workflowState, result *LLMAnalysisResult) []string {
	plan := make([]string, 0, 6)
	if state != nil {
		for _, step := range state.planSteps {
			if len(plan) >= 3 {
				break
			}
			summary := firstNonEmpty(step.Title, step.Objective)
			if strings.TrimSpace(summary) != "" {
				plan = append(plan, summary)
			}
		}
	}
	for _, step := range result.NextSteps {
		if len(plan) >= 5 {
			break
		}
		if strings.TrimSpace(step) != "" {
			plan = append(plan, step)
		}
	}
	if len(plan) == 0 {
		plan = append(plan,
			"Confirm the strongest telemetry evidence against the incident window",
			"Prefer safe read-only checks before remediation planning",
		)
	}
	return dedupeStrings(plan)
}

func defaultRecommendedChecks(state *workflowState, result *LLMAnalysisResult) []string {
	checks := append([]string{}, result.NextSteps...)
	if state != nil {
		for _, event := range state.investigationEvents {
			checks = append(checks, event.RecommendedChecks...)
			if len(checks) >= 6 {
				break
			}
		}
	}
	if len(checks) == 0 {
		checks = append(checks, "Review the highest-confidence hypothesis against current metrics, logs, and retrieved runbooks")
	}
	return dedupeStrings(checks)
}

func defaultExpectedSLOImpact(state *workflowState, confidence float64) string {
	if state == nil {
		if confidence >= 0.7 {
			return "Likely to degrade latency, throughput, or job success objectives if the incident is left unchecked."
		}
		return "Current evidence suggests limited immediate SLO impact, but more verification is required."
	}
	scope := firstNonEmpty(state.collectorID, state.incident.CandidateRootCauseCluster, state.risk.Scope, "fleet")
	switch strings.ToLower(strings.TrimSpace(firstNonEmpty(state.risk.RiskLevel, state.incident.Severity))) {
	case "critical", "high":
		return fmt.Sprintf("High likelihood of user-visible latency, error-rate, or job-completion SLO impact on %s if the incident is left unchecked.", scope)
	case "medium":
		return fmt.Sprintf("Moderate likelihood of near-term latency or throughput SLO erosion on %s if the trend persists.", scope)
	default:
		if confidence >= 0.6 {
			return fmt.Sprintf("Localized performance degradation is plausible on %s, but hard SLO breach risk remains limited with current evidence.", scope)
		}
		return fmt.Sprintf("Current evidence suggests a low immediate SLO impact on %s; continue verification before acting.", scope)
	}
}

func stubExpectedSLOImpact(bundle ContextBundle) string {
	scope := firstNonEmpty(bundle.Scope, bundle.CollectorID, "fleet")
	switch strings.ToLower(strings.TrimSpace(bundle.RiskLevel)) {
	case "critical", "high":
		return fmt.Sprintf("Likely to degrade user-visible latency, throughput, or job success SLOs on %s if it continues.", scope)
	case "medium":
		return fmt.Sprintf("Could erode latency or throughput SLOs on %s if the trend persists.", scope)
	default:
		return fmt.Sprintf("No immediate SLO breach is implied on %s, but continued monitoring is required.", scope)
	}
}

func stubMissingEvidence(bundle ContextBundle) []string {
	missing := make([]string, 0, 4)
	if len(bundle.RetrievedDocs) == 0 && len(bundle.RetrievedRunbooks) == 0 && len(bundle.RetrievedCases) == 0 {
		missing = append(missing, "No corroborating runbook or similar-case knowledge has been attached yet")
	}
	if len(bundle.RuntimeSecurityEvents) == 0 && len(bundle.StructuredSecurityFindings) == 0 {
		missing = append(missing, "Runtime security or integrity evidence is absent")
	}
	if len(bundle.LogExcerpts) == 0 {
		missing = append(missing, "No log excerpts are available for temporal correlation")
	}
	return dedupeStrings(missing)
}

func buildReasoningCritique(state *workflowState, result *LLMAnalysisResult, branches []LLMReasoningBranch) []string {
	critique := make([]string, 0, 8)
	if len(result.RCAHypotheses) == 0 && state.workflowType == "rca" {
		critique = append(critique, "The draft did not produce a ranked RCA hypothesis despite an incident candidate being present.")
	}
	if len(result.EvidenceCited) < 2 {
		critique = append(critique, "The draft cites too little evidence to justify a production-facing diagnosis.")
	}
	requiredPlanned, requiredVerified := summarizeRequiredPlanSteps(state.planSteps)
	if requiredPlanned > requiredVerified {
		critique = append(critique, fmt.Sprintf("%d required plan steps remain unverified and should lower confidence.", requiredPlanned-requiredVerified))
	}
	if len(branches) > 1 {
		critique = append(critique, "Competing branches remain plausible; the final answer must explain why one branch is preferred.")
	}
	for _, gap := range unresolvedGapsFromState(state) {
		critique = append(critique, gap)
		if len(critique) >= 6 {
			break
		}
	}
	if len(result.NextSteps) == 0 {
		critique = append(critique, "The draft omitted concrete operator checks, which weakens its operational usefulness.")
	}
	return dedupeStrings(critique)
}

func shouldUseReasoningBranches(state *workflowState, cfg WorkflowConfig) bool {
	if state == nil || state.workflowType != "rca" || !cfg.AdvancedReasoningEnabled || cfg.AdvancedReasoningMaxBranches <= 1 {
		return false
	}
	highSeverity := strings.EqualFold(state.risk.RiskLevel, "high") || strings.EqualFold(state.risk.RiskLevel, "critical") ||
		strings.EqualFold(state.incident.Severity, "high") || strings.EqualFold(state.incident.Severity, "critical")
	if !highSeverity && state.incident.Confidence < cfg.MediumRiskThreshold {
		return false
	}
	if len(state.hypotheses) >= 2 {
		gap := state.hypotheses[0].Confidence - state.hypotheses[1].Confidence
		if gap <= cfg.AdvancedReasoningAmbiguityThreshold {
			return true
		}
	}
	return len(unresolvedGapsFromState(state)) >= 2 && topHypothesisConfidence(state.hypotheses) < 0.78
}

func buildReasoningBranches(state *workflowState, limit int) []LLMReasoningBranch {
	if state == nil || limit <= 0 {
		return nil
	}
	branches := make([]LLMReasoningBranch, 0, limit)
	for index, hypothesis := range state.hypotheses {
		if len(branches) >= limit {
			break
		}
		branches = append(branches, LLMReasoningBranch{
			ID:         fmt.Sprintf("branch-%d", index+1),
			Focus:      truncateString(firstNonEmpty(hypothesis.Description, hypothesis.Title), 160),
			Hypothesis: hypothesis.Title,
			Confidence: clamp01(hypothesis.Confidence),
			Evidence:   append([]string(nil), hypothesis.EvidenceIDs...),
		})
	}
	if len(branches) == 0 {
		for index, event := range state.investigationEvents {
			if len(branches) >= limit {
				break
			}
			branches = append(branches, LLMReasoningBranch{
				ID:         fmt.Sprintf("event-branch-%d", index+1),
				Focus:      truncateString(firstNonEmpty(event.ProbableCause, event.Title), 160),
				Hypothesis: event.Title,
				Confidence: clamp01(event.Confidence),
				Evidence:   append([]string(nil), event.Evidence...),
			})
		}
	}
	return branches
}

func cloneLLMHypotheses(in []LLMHypothesis) []LLMHypothesis {
	if len(in) == 0 {
		return nil
	}
	out := make([]LLMHypothesis, 0, len(in))
	for _, hypothesis := range in {
		out = append(out, LLMHypothesis{
			Title:       hypothesis.Title,
			Confidence:  clamp01(hypothesis.Confidence),
			Evidence:    append([]string(nil), hypothesis.Evidence...),
			Description: hypothesis.Description,
		})
	}
	return out
}

func firstNonZero(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (e *WorkflowEngine) refineLLMAnalysis(ctx context.Context, state *workflowState, bundle ContextBundle, initial *LLMAnalysisResult, budget *reasoningBudget) (*LLMAnalysisResult, *LLMReasoningReview) {
	initialArtifact := buildReasoningArtifact(state, initial)
	review := &LLMReasoningReview{
		Mode:          "plan_review_refine",
		ReviewApplied: false,
		Initial:       initialArtifact,
		Final:         initialArtifact,
	}

	// C4: resolve reasoning mode from severity policy
	severity := resolveWorkflowSeverity(state)
	reasoningMode := e.cfg.ReasoningSeverityPolicy.ReasoningModeForSeverity(severity)

	if state == nil || state.workflowType != "rca" {
		review.Mode = "single_pass"
		review.SkippedReason = "advanced review only applies to RCA cold-path reasoning"
		if budget != nil {
			review.EstimatedPromptTokens = budget.promptUsage()
			review.EstimatedCompletionTokens = budget.completionUsage()
			review.Calls = budget.calls
		}
		return initial, review
	}
	if reasoningMode == "single_pass" {
		review.Mode = "single_pass"
		review.SkippedReason = "severity policy selected single_pass for " + severity
		if budget != nil {
			review.EstimatedPromptTokens = budget.promptUsage()
			review.EstimatedCompletionTokens = budget.completionUsage()
			review.Calls = budget.calls
		}
		return initial, review
	}
	if !e.cfg.AdvancedReasoningEnabled {
		review.SkippedReason = "advanced reasoning disabled"
		if budget != nil {
			review.EstimatedPromptTokens = budget.promptUsage()
			review.EstimatedCompletionTokens = budget.completionUsage()
			review.Calls = budget.calls
		}
		return initial, review
	}

	// C5: explicit degraded mode handling
	if e.llm == nil {
		review.Mode = "degraded"
		review.DegradedMode = true
		review.SkippedReason = "LLM client unavailable; degraded_mode_policy=" + e.cfg.DegradedModePolicy
		if e.cfg.DegradedModePolicy == "skip_reasoning" {
			return initial, review
		}
		// deterministic_only: use the deterministic fallback
		fallback := deterministicFallbackAnalysis(bundle)
		if fallback != nil {
			review.ReviewApplied = true
			review.Final = buildReasoningArtifact(state, fallback)
			return fallback, review
		}
		return initial, review
	}

	review.Mode = reasoningMode

	reasoningCtx := ctx
	timeout := e.cfg.AdvancedReasoningTimeout
	if timeout > 0 {
		var cancel context.CancelFunc
		reasoningCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var branches []LLMReasoningBranch
	if shouldUseReasoningBranches(state, e.cfg) {
		branches = buildReasoningBranches(state, e.cfg.AdvancedReasoningMaxBranches)
	}
	critique := buildReasoningCritique(state, initial, branches)
	review.Branches = append([]LLMReasoningBranch(nil), branches...)
	review.Critique = append([]string(nil), critique...)

	// C1: determine max iterations for the refine loop
	maxIterations := 1
	if reasoningMode == "full_iterative" {
		maxIterations = e.cfg.MaxRefineIterations
		if maxIterations <= 0 {
			maxIterations = 3
		}
	}
	confidenceThreshold := e.cfg.RefineConfidenceThreshold
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.70
	}

	current := initial
	for iter := 0; iter < maxIterations; iter++ {
		iterRecord := LLMReasoningIteration{
			Iteration:        iter + 1,
			ConfidenceBefore: current.Confidence,
		}

		currentArtifact := buildReasoningArtifact(state, current)
		systemPrompt := BuildWorkflowSystemPrompt()
		userPrompt := BuildWorkflowReviewPrompt(bundle, currentArtifact, critique, branches)
		if budget != nil && !budget.reservePrompt(systemPrompt+"\n"+userPrompt) {
			iterRecord.StopReason = "budget_exhausted"
			iterRecord.ConfidenceAfter = current.Confidence
			iterRecord.BudgetRemaining = budget.remaining()
			review.Iterations = append(review.Iterations, iterRecord)
			review.BudgetExhausted = true
			break
		}
		if e.llmLimiter != nil {
			if err := e.llmLimiter.Wait(reasoningCtx); err != nil {
				iterRecord.StopReason = "rate_limited"
				iterRecord.ConfidenceAfter = current.Confidence
				review.Iterations = append(review.Iterations, iterRecord)
				review.Critique = append(review.Critique, fmt.Sprintf("review rate limiter wait failed (iter %d): %s", iter+1, err.Error()))
				if e.telemetry != nil {
					e.telemetry.recordReasoningFailure()
				}
				break
			}
		}

		llmTimeout := e.cfg.LLMTimeout
		if llmTimeout <= 0 {
			llmTimeout = 30 * time.Second
		}
		callCtx, cancel := context.WithTimeout(reasoningCtx, llmTimeout)
		raw, err := e.llm.Complete(callCtx, systemPrompt, userPrompt)
		cancel()
		if budget != nil {
			budget.recordCompletion(raw)
		}
		if err != nil {
			iterRecord.StopReason = "llm_call_failed"
			iterRecord.ConfidenceAfter = current.Confidence
			if budget != nil {
				iterRecord.BudgetRemaining = budget.remaining()
			}
			review.Iterations = append(review.Iterations, iterRecord)
			review.Critique = append(review.Critique, fmt.Sprintf("review LLM call failed (iter %d): %s", iter+1, err.Error()))
			if e.telemetry != nil {
				e.telemetry.recordReasoningFailure()
			}
			// C5: on LLM failure during iteration, apply degraded mode policy
			if e.cfg.DegradedModePolicy == "deterministic_only" {
				review.DegradedMode = true
				fallback := deterministicFallbackAnalysis(bundle)
				if fallback != nil {
					review.ReviewApplied = true
					review.Final = buildReasoningArtifact(state, fallback)
					current = fallback
				}
			}
			break
		}

		result, parseErr := ParseLLMAnalysis(raw)
		if parseErr != nil {
			iterRecord.StopReason = "parse_failed"
			iterRecord.ConfidenceAfter = current.Confidence
			if budget != nil {
				iterRecord.BudgetRemaining = budget.remaining()
			}
			review.Iterations = append(review.Iterations, iterRecord)
			review.Critique = append(review.Critique, fmt.Sprintf("review parse failed (iter %d): %s", iter+1, parseErr.Error()))
			if e.telemetry != nil {
				e.telemetry.recordReasoningFailure()
				e.telemetry.recordHallucinationProxy()
			}
			break
		}
		if len(result.ToolRequests) > 0 {
			review.Critique = append(review.Critique, "review response attempted additional tool requests; they were ignored to keep the cold-path loop bounded")
			result.ToolRequests = nil
		}
		normalizeLLMAnalysisResult(state, &result, nil)
		if validErr := ValidateLLMAnalysis(result); validErr != nil {
			iterRecord.StopReason = "validation_failed"
			iterRecord.ConfidenceAfter = current.Confidence
			if budget != nil {
				iterRecord.BudgetRemaining = budget.remaining()
			}
			review.Iterations = append(review.Iterations, iterRecord)
			review.Critique = append(review.Critique, fmt.Sprintf("review validation failed (iter %d): %s", iter+1, validErr.Error()))
			if e.telemetry != nil {
				e.telemetry.recordReasoningFailure()
				e.telemetry.recordHallucinationProxy()
			}
			break
		}
		if safetyErr := ValidateLLMAnalysisAgainstBundle(bundle, result); safetyErr != nil {
			iterRecord.StopReason = "safety_failed"
			iterRecord.ConfidenceAfter = current.Confidence
			if budget != nil {
				iterRecord.BudgetRemaining = budget.remaining()
			}
			review.Iterations = append(review.Iterations, iterRecord)
			review.Critique = append(review.Critique, fmt.Sprintf("review safety validation failed (iter %d): %s", iter+1, safetyErr.Error()))
			if e.telemetry != nil {
				e.telemetry.recordReasoningFailure()
				e.telemetry.recordHallucinationProxy()
			}
			break
		}

		// Successful iteration
		iterRecord.ConfidenceAfter = result.Confidence
		iterRecord.Improved = result.Confidence > current.Confidence
		if budget != nil {
			iterRecord.BudgetRemaining = budget.remaining()
		}
		review.Iterations = append(review.Iterations, iterRecord)

		review.ReviewApplied = true
		current = &result
		review.Final = buildReasoningArtifact(state, current)
		if e.telemetry != nil {
			e.telemetry.recordReasoningStep(result.Confidence, estimateReasoningTokens(systemPrompt+"\n"+userPrompt), estimateReasoningTokens(raw))
		}

		// Check if confidence threshold is met
		if result.Confidence >= confidenceThreshold {
			break
		}

		// Rebuild critique for next iteration using refined result
		if iter+1 < maxIterations {
			critique = buildReasoningCritique(state, current, branches)
		}
	}

	if budget != nil {
		review.EstimatedPromptTokens = budget.promptUsage()
		review.EstimatedCompletionTokens = budget.completionUsage()
		review.Calls = budget.calls
	}
	return current, review
}

// resolveWorkflowSeverity derives the effective severity from workflow state.
func resolveWorkflowSeverity(state *workflowState) string {
	if state == nil {
		return "medium"
	}
	if state.risk.RiskLevel != "" {
		return strings.ToLower(state.risk.RiskLevel)
	}
	if state.risk.RiskScore >= 0.72 {
		return "critical"
	}
	if state.risk.RiskScore >= 0.45 {
		return "high"
	}
	if state.risk.RiskScore >= 0.20 {
		return "medium"
	}
	return "low"
}
