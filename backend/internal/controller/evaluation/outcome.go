package evaluation

import (
	"fmt"
	"strings"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// Outcome evaluation v2: per-task-type task success plus the shared outcome
// helpers.
//
// Task success is evaluated ONLY against the independent ground truth in
// eval_data — never against the agent's own claims ("fixed", "resolved") and
// never against values the runtime itself generated.

// taskSuccess returns whether one trial accomplished its case's task, plus
// the trial's failure strings explaining why not.
func taskSuccess(contract V2Case, claims []rcaEntityClaim, corpus []string, execution eval.WorkflowCaseExecution, cfg V2ScoringConfig) (bool, []string) {
	gt := contract.GroundTruth
	report := execution.Report
	var failures []string
	fail := func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}

	switch contract.TaskType {
	case TaskTypeNoop:
		if gt.NoIncident {
			if noopFalsePositive(contract, execution, cfg) {
				fail("false positive: agent claimed an incident on a healthy system")
			}
			if noopUnnecessaryActions(execution) > 0 {
				fail("agent proposed or executed remediation actions on a healthy system")
			}
		}
	case TaskTypeDetect:
		if !detectAnomalyMatched(gt, corpus) {
			fail("agent did not identify the injected anomaly (no anomaly/hypothesis matched ground truth)")
		}
	case TaskTypeDiagnose:
		precision, recall, recallAt1, _, _, _, _, _ := matchRCAGroundTruth(claims, corpus, gt.RootCause)
		reasoning := rcaReasoningScore(gt.RootCause, claims, corpus, gt.RequiredEvidence, report)
		// Diagnosis succeeds when the entities are located AND the agent's
		// answer is focused: either most claimed entities are correct
		// (precision) or the top-ranked claim already names the fault
		// (recall@1). An unfocused shotgun answer with a wrong top claim
		// fails both ways.
		answerFocused := precision >= cfg.TaskSuccess.DiagnoseEntityPrecisionMin || recallAt1 >= cfg.TaskSuccess.DiagnoseEntityRecallMin
		if recall < cfg.TaskSuccess.DiagnoseEntityRecallMin {
			fail("root cause entity recall %.2f below minimum %.2f", recall, cfg.TaskSuccess.DiagnoseEntityRecallMin)
		}
		if !answerFocused {
			fail("root cause answer unfocused: precision %.2f and recall@1 %.2f both below minimum %.2f", precision, recallAt1, cfg.TaskSuccess.DiagnoseEntityPrecisionMin)
		}
		if reasoning < cfg.TaskSuccess.DiagnoseReasoningMin {
			fail("root cause reasoning %.2f below minimum %.2f", reasoning, cfg.TaskSuccess.DiagnoseReasoningMin)
		}
	case TaskTypePlan:
		precision, recall, recallAt1, _, _, _, _, _ := matchRCAGroundTruth(claims, corpus, gt.RootCause)
		answerFocused := precision >= cfg.TaskSuccess.DiagnoseEntityPrecisionMin || recallAt1 >= cfg.TaskSuccess.DiagnoseEntityRecallMin
		if recall < cfg.TaskSuccess.DiagnoseEntityRecallMin || !answerFocused {
			fail("remediation plan requires a correct diagnosis (entity recall %.2f, precision %.2f, recall@1 %.2f)", recall, precision, recallAt1)
		}
		plan := remediationPlanCorrectness(gt, report)
		if plan == nil && len(gt.AcceptableRemediation) > 0 {
			fail("no acceptable remediation found in the agent plan")
		}
		if plan != nil && *plan < cfg.TaskSuccess.PlanRecommendationCoverageMin {
			fail("remediation plan coverage %.2f below minimum %.2f", *plan, cfg.TaskSuccess.PlanRecommendationCoverageMin)
		}
		if remediationForbiddenPresent(gt, report) {
			fail("agent plan contains a forbidden remediation")
		}
	case TaskTypeMitigate:
		recovery := recoverySuccess(gt, execution)
		if recovery == nil {
			fail("remediation was never executed, so the mitigate task cannot succeed")
			break
		}
		if *recovery < 1 {
			fail("success conditions not met: recovery %.2f", *recovery)
		}
		if noRegression := noRegressionScore(execution); noRegression != nil && *noRegression < 1 {
			fail("remediation introduced a regression")
		}
	case TaskTypeVerify:
		verdict := verificationCorrectness(contract, execution)
		if verdict == nil {
			fail("no post-action validation verdict recorded for verify task")
			break
		}
		if *verdict < 1 {
			fail("verification verdict incorrect (agent misjudged the remediation outcome)")
		}
	default:
		fail("unsupported task_type %q", contract.TaskType)
	}
	return len(failures) == 0, failures
}

// detectAnomalyMatched reports whether the agent's output connects to the
// injected fault (any ground-truth alias appears in anomalies, hypotheses or
// supporting signals). Detection in this architecture is evaluated on the
// analysis layer that consumes pre-seeded telemetry.
func detectAnomalyMatched(gt V2GroundTruth, corpus []string) bool {
	aliases := groundTruthAliases(gt.RootCause)
	if len(aliases) == 0 {
		return false
	}
	corpusText := strings.Join(corpus, " \n ")
	for _, alias := range aliases {
		if aliasMatches(alias, corpusText) {
			return true
		}
	}
	return false
}

// noopFalsePositive reports whether the agent claimed an incident on a
// healthy system: a non-noise suspected entity, or a top hypothesis with
// confidence at/above the configured minimum.
func noopFalsePositive(contract V2Case, execution eval.WorkflowCaseExecution, cfg V2ScoringConfig) bool {
	report := execution.Report
	collector := strings.ToLower(strings.TrimSpace(report.CollectorID))
	suspected := strings.ToLower(strings.TrimSpace(report.SuspectedRootCauseEntity))
	if suspected != "" && suspected != collector && !isNoiseEntityClaim(suspected) {
		return true
	}
	for _, item := range report.Hypotheses {
		if item.Rank == 1 && item.Confidence >= cfg.TaskSuccess.NoopFalsePositiveConfidenceMin {
			return true
		}
	}
	return false
}

// noopUnnecessaryActions counts remediation-like proposals on a healthy
// system, even when they remain suggest-only or dry-run. Read-only checks and
// profiling are not remediation; restart/rollback/scale/containment are.
func noopUnnecessaryActions(execution eval.WorkflowCaseExecution) int {
	count := 0
	for _, action := range execution.Report.ProposedActions {
		if isRemediationLikeAction(action.Category) {
			count++
		}
	}
	return count
}

// isRemediationLikeAction reports whether an action category changes system
// state rather than investigates it.
func isRemediationLikeAction(category string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	for _, token := range []string{"remediat", "mitigat", "containment", "restart", "rollback", "scale", "evict", "kill"} {
		if strings.Contains(category, token) {
			return true
		}
	}
	return false
}

// remediationPlanCorrectness scores the agent's remediation plan: acceptable
// remediation coverage, zeroed when a forbidden remediation is present.
func remediationPlanCorrectness(gt V2GroundTruth, report agentcore.RCAWorkflowReport) *float64 {
	if len(gt.AcceptableRemediation) == 0 && len(gt.ForbiddenRemediation) == 0 {
		return nil
	}
	if remediationForbiddenPresent(gt, report) {
		return floatPtr(0)
	}
	if len(gt.AcceptableRemediation) == 0 {
		return floatPtr(1)
	}
	planText := remediationPlanCorpus(report)
	covered := 0
	for _, item := range gt.AcceptableRemediation {
		if aliasMatches(item, planText) {
			covered++
		}
	}
	return floatPtr(ratioScores(covered, len(gt.AcceptableRemediation)))
}

// remediationPlanCorpus collects everything the agent wrote about its plan.
func remediationPlanCorpus(report agentcore.RCAWorkflowReport) string {
	parts := make([]string, 0, 32)
	for _, item := range report.Recommendations {
		parts = append(parts, item.Summary, item.Details)
		for _, check := range item.Checks {
			parts = append(parts, check)
		}
	}
	for _, action := range report.ProposedActions {
		parts = append(parts, action.CommandPreview, action.Rationale)
	}
	for _, item := range report.Validation.ActionSummary {
		parts = append(parts, item)
	}
	if report.Validation.SelectedAction != nil {
		parts = append(parts, report.Validation.SelectedAction.Summary)
	}
	parts = append(parts, report.StructuredReport.SafeRemediations...)
	parts = append(parts, report.StructuredReport.RecommendedNextSteps...)
	return strings.Join(parts, " \n ")
}

// remediationForbiddenPresent reports whether any forbidden remediation
// appears in the agent's plan corpus.
func remediationForbiddenPresent(gt V2GroundTruth, report agentcore.RCAWorkflowReport) bool {
	if len(gt.ForbiddenRemediation) == 0 {
		return false
	}
	planText := remediationPlanCorpus(report)
	for _, item := range gt.ForbiddenRemediation {
		if aliasMatches(item, planText) {
			return true
		}
	}
	return false
}

// verificationCorrectness compares the agent's post-action verdict with the
// ground-truth expected verdict list. An agent that claims "effective" for a
// remediation the oracle says failed scores 0.
func verificationCorrectness(contract V2Case, execution eval.WorkflowCaseExecution) *float64 {
	if len(contract.GroundTruth.ExpectedVerdictAny) == 0 {
		return nil
	}
	summary := execution.Report.Validation.PostActionValidation
	if summary == nil {
		return floatPtr(0)
	}
	actual := strings.ToLower(strings.TrimSpace(string(summary.Verdict)))
	if stringSliceContainsFold(contract.GroundTruth.ExpectedVerdictAny, actual) {
		return floatPtr(1)
	}
	return floatPtr(0)
}

// recoverySuccess checks the case's success conditions against the
// post-action validation state. It never consults the agent's own verdict —
// the conditions must hold in the recorded before/after comparison.
func recoverySuccess(gt V2GroundTruth, execution eval.WorkflowCaseExecution) *float64 {
	if len(gt.SuccessConditions) == 0 {
		return nil
	}
	if !hasExecutedRemediation(execution) {
		return nil
	}
	summary := execution.Report.Validation.PostActionValidation
	if summary == nil {
		return floatPtr(0)
	}
	passed := 0
	for _, condition := range gt.SuccessConditions {
		value, ok := resolvePostActionMetric(condition.Metric, summary)
		if !ok {
			continue
		}
		if compareFloat(value, condition.Operator, condition.Value) {
			passed++
		}
	}
	return floatPtr(ratioScores(passed, len(gt.SuccessConditions)))
}

// resolvePostActionMetric resolves a success-condition metric name against
// the post-action validation snapshot. Unresolvable metrics are not counted
// as met.
func resolvePostActionMetric(name string, summary *agentcore.PostActionValidationSummary) (float64, bool) {
	if summary == nil {
		return 0, false
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if summary.Comparison != nil {
		comparison := summary.Comparison
		switch lower {
		case "risk", "risk_score":
			if comparison.RiskScore.Available {
				return comparison.RiskScore.After, true
			}
		case "latency_ms", "service_latency_ms":
			if comparison.ServiceLatencyMS.Available {
				return comparison.ServiceLatencyMS.After, true
			}
		case "error_rate", "service_error_rate":
			if comparison.ServiceErrorRate.Available {
				return comparison.ServiceErrorRate.After, true
			}
		case "log_errors":
			if comparison.LogErrors.Available {
				return float64(comparison.LogErrors.After), true
			}
		case "log_warnings":
			if comparison.LogWarnings.Available {
				return float64(comparison.LogWarnings.After), true
			}
		case "triggered_signals":
			if comparison.TriggeredSignals.Available {
				return float64(comparison.TriggeredSignals.After), true
			}
		}
	}
	switch lower {
	case "after_risk":
		if summary.AfterRisk != 0 {
			return summary.AfterRisk, true
		}
	case "before_risk":
		if summary.BeforeRisk != 0 {
			return summary.BeforeRisk, true
		}
	}
	if summary.Delta != nil {
		switch lower {
		case "risk_delta":
			return summary.Delta.RiskDelta, true
		case "latency_delta_ms":
			return summary.Delta.LatencyDeltaMS, true
		case "error_rate_delta":
			return summary.Delta.ErrorRateDelta, true
		case "log_error_delta":
			return float64(summary.Delta.LogErrorDelta), true
		case "log_warning_delta":
			return float64(summary.Delta.LogWarningDelta), true
		case "triggered_signal_delta":
			return float64(summary.Delta.TriggeredSignalDelta), true
		}
	}
	return 0, false
}

// compareFloat evaluates one comparison operator deterministically.
func compareFloat(value float64, operator string, target float64) bool {
	switch strings.TrimSpace(operator) {
	case "<":
		return value < target
	case "<=":
		return value <= target
	case ">":
		return value > target
	case ">=":
		return value >= target
	case "==", "=":
		return value == target
	default:
		return false
	}
}

// noRegressionScore is the transactional no-regression check: after a
// remediation the original incident must improve AND no other key health
// indicator may worsen AND no compensation may have been triggered. Returns
// nil when no remediation was executed (unmeasured — never silently 1.0).
func noRegressionScore(execution eval.WorkflowCaseExecution) *float64 {
	report := execution.Report
	summary := report.Validation.PostActionValidation
	if summary == nil {
		return nil
	}
	if !hasExecutedRemediation(execution) {
		return nil
	}
	improved := false
	if summary.Delta != nil && summary.Delta.RiskDelta < 0 {
		improved = true
	}
	if summary.AfterRisk != 0 && summary.BeforeRisk != 0 && summary.AfterRisk < summary.BeforeRisk {
		improved = true
	}
	worsened := 0
	if summary.Comparison != nil {
		if summary.Comparison.RiskScore.Regressed {
			worsened++
		}
		if summary.Comparison.ServiceLatencyMS.Regressed {
			worsened++
		}
		if summary.Comparison.ServiceErrorRate.Regressed {
			worsened++
		}
		if summary.Comparison.LogErrors.Regressed {
			worsened++
		}
		if summary.Comparison.LogWarnings.Regressed {
			worsened++
		}
		if summary.Comparison.TriggeredSignals.Regressed {
			worsened++
		}
		if summary.Comparison.SecurityScore.Regressed {
			worsened++
		}
	}
	// compensation triggered indicates a detected regression
	if report.Validation.CompensationMessage != nil {
		worsened++
	}
	if !improved || worsened > 0 {
		return floatPtr(0)
	}
	return floatPtr(1)
}

// hasExecutedRemediation proves that a state-changing remediation reached a
// successful terminal invocation state. A proposal, dry run, blocked call, or
// failed call is not an executed mitigation.
func hasExecutedRemediation(execution eval.WorkflowCaseExecution) bool {
	if execution.DurableRun == nil {
		return false
	}
	for _, call := range execution.DurableRun.ToolCalls {
		if call.Tool == agentcore.ToolRemediation && !call.DryRun && invocationExecuted(call) {
			return true
		}
	}
	return false
}
