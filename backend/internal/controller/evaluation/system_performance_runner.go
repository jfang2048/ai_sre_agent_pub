package evaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// RunSystemPerformance executes the full end-to-end system scorecard on top of the
// repository's real workflow runtime and persists the resulting report.
func RunSystemPerformance(ctx context.Context, opts SystemPerformanceOptions) (SystemPerformanceReport, error) {
	repoRoot, err := eval.ResolveRepoRoot(opts.RepoRoot)
	if err != nil {
		return SystemPerformanceReport{}, err
	}
	scope := opts.Scope
	if scope == "" {
		scope = eval.ScopeFast
	}
	cases, err := loadSystemPerformanceCases(repoRoot)
	if err != nil {
		return SystemPerformanceReport{}, err
	}
	cases = filterSystemPerformanceCases(cases, scope, opts.CaseIDs)
	if len(cases) == 0 {
		return SystemPerformanceReport{}, fmt.Errorf("no system performance cases matched scope %s", scope)
	}
	incidentCases, err := eval.LoadIncidentCases(repoRoot)
	if err != nil {
		return SystemPerformanceReport{}, err
	}
	incidentByID := make(map[string]eval.IncidentCase, len(incidentCases))
	for _, item := range incidentCases {
		incidentByID[item.ID] = item
	}

	kb, cleanup, err := eval.BuildKnowledgeBase(ctx, repoRoot)
	if err != nil {
		return SystemPerformanceReport{}, err
	}
	defer cleanup()

	replayRuns := opts.ReplayRuns
	if replayRuns <= 0 {
		replayRuns = 2
	}
	runOpts := eval.WorkflowCaseRunOptions{
		ConfigOverride: eval.WorkflowConfigOverride{
			AgentMessageProtocolEnabled: opts.AgentMessageProtocolEnabled,
			ValidationAgentEnabled:      opts.ValidationAgentEnabled,
		},
	}

	report := SystemPerformanceReport{
		SchemaVersion: "system-performance/v1",
		GeneratedAt:   time.Now().UTC(),
		Scope:         scope,
		Variant:       systemPerformanceVariant(opts),
		ReplayRuns:    replayRuns,
		Cases:         make([]SystemPerformanceCaseResult, 0, len(cases)),
	}

	for _, item := range cases {
		incident, ok := incidentByID[item.IncidentCaseID]
		if !ok {
			return SystemPerformanceReport{}, fmt.Errorf("system performance case %s references unknown incident case %s", item.ID, item.IncidentCaseID)
		}
		executions := make([]eval.WorkflowCaseExecution, 0, replayRuns)
		for i := 0; i < replayRuns; i++ {
			execution, err := eval.RunWorkflowCaseDetailed(ctx, kb, incident, runOpts)
			if err != nil {
				return SystemPerformanceReport{}, err
			}
			executions = append(executions, execution)
		}
		caseResult := evaluateSystemPerformanceCase(item, executions)
		report.Cases = append(report.Cases, caseResult)
		if !caseResult.Passed {
			report.FailedCaseIDs = append(report.FailedCaseIDs, caseResult.ID)
		}
	}

	report.Metrics = aggregateSystemPerformanceMetrics(report.Cases)
	report.Scorecard = computeSystemPerformanceScorecard(report.Metrics)
	report.Passed = len(report.FailedCaseIDs) == 0

	latestPath, historyPath, err := persistSystemPerformanceReport(repoRoot, report)
	if err != nil {
		return SystemPerformanceReport{}, err
	}
	report.LatestPath = latestPath
	report.HistoryPath = historyPath

	if strings.TrimSpace(opts.ComparePath) != "" {
		baseline, err := loadSystemPerformanceReport(opts.ComparePath)
		if err != nil {
			return SystemPerformanceReport{}, err
		}
		report.Comparison = compareSystemPerformanceReports(report, baseline, opts.ComparePath)
		raw, err := report.JSON()
		if err != nil {
			return SystemPerformanceReport{}, err
		}
		if err := os.WriteFile(historyPath, raw, 0o644); err != nil {
			return SystemPerformanceReport{}, err
		}
		if err := os.WriteFile(latestPath, raw, 0o644); err != nil {
			return SystemPerformanceReport{}, err
		}
	}

	return report, nil
}

func loadSystemPerformanceCases(repoRoot string) ([]SystemPerformanceCase, error) {
	path := filepath.Join(repoRoot, "eval_data", "system_perf_cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file SystemPerformanceCaseFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(file.SchemaVersion) == "" {
		return nil, fmt.Errorf("%s missing schema_version", path)
	}
	return file.Cases, nil
}

func filterSystemPerformanceCases(cases []SystemPerformanceCase, scope eval.Scope, selectedIDs []string) []SystemPerformanceCase {
	selected := make(map[string]struct{}, len(selectedIDs))
	for _, item := range selectedIDs {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			selected[trimmed] = struct{}{}
		}
	}
	out := make([]SystemPerformanceCase, 0, len(cases))
	for _, item := range cases {
		if len(selected) > 0 {
			if _, ok := selected[item.ID]; !ok {
				continue
			}
		}
		if systemPerformanceSuiteAllowed(item.Suites, scope) {
			out = append(out, item)
		}
	}
	return out
}

func systemPerformanceSuiteAllowed(suites []string, scope eval.Scope) bool {
	if len(suites) == 0 {
		return true
	}
	normalized := make([]string, 0, len(suites))
	for _, item := range suites {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			normalized = append(normalized, item)
		}
	}
	switch scope {
	case eval.ScopeFast:
		return slices.Contains(normalized, string(eval.ScopeFast))
	case eval.ScopeRegression:
		return slices.Contains(normalized, string(eval.ScopeFast)) || slices.Contains(normalized, string(eval.ScopeRegression))
	case eval.ScopeBenchmark:
		return slices.Contains(normalized, string(eval.ScopeFast)) || slices.Contains(normalized, string(eval.ScopeRegression)) || slices.Contains(normalized, string(eval.ScopeBenchmark))
	default:
		return slices.Contains(normalized, string(eval.ScopeFast))
	}
}

func systemPerformanceVariant(opts SystemPerformanceOptions) string {
	if trimmed := strings.TrimSpace(opts.Variant); trimmed != "" {
		return trimmed
	}
	parts := make([]string, 0, 2)
	if opts.AgentMessageProtocolEnabled != nil {
		parts = append(parts, fmt.Sprintf("message_protocol=%t", *opts.AgentMessageProtocolEnabled))
	}
	if opts.ValidationAgentEnabled != nil {
		parts = append(parts, fmt.Sprintf("validation_agent=%t", *opts.ValidationAgentEnabled))
	}
	return firstNonEmpty(strings.Join(parts, ","), "default")
}

func evaluateSystemPerformanceCase(contract SystemPerformanceCase, executions []eval.WorkflowCaseExecution) SystemPerformanceCaseResult {
	primary := executions[0]
	artifacts := extractArtifactRefs(primary)
	metrics := extractPrimarySystemMetrics(contract, primary)
	mergeReplayMetrics(&metrics, executions)
	metrics.CostPerSuccessfulCase = 0
	if primary.Result.Passed {
		metrics.CostPerSuccessfulCase = metrics.TokenCost
	}
	scorecard := computeSystemPerformanceScorecard(metrics)
	failures := validateSystemPerformanceCase(contract, primary, metrics, artifacts)
	for _, item := range primary.Result.Failures {
		failures = append(failures, "workflow:"+item)
	}
	failures = dedupeStrings(failures)
	return SystemPerformanceCaseResult{
		ID:             contract.ID,
		IncidentType:   contract.IncidentType,
		IncidentCaseID: contract.IncidentCaseID,
		Description:    contract.Description,
		Artifacts:      artifacts,
		Metrics:        metrics,
		Scorecard:      scorecard,
		Failures:       failures,
		Passed:         len(failures) == 0,
	}
}

func extractArtifactRefs(execution eval.WorkflowCaseExecution) SystemPerformanceArtifactRefs {
	report := execution.Report
	return SystemPerformanceArtifactRefs{
		WorkflowID:                        report.WorkflowID,
		EvidencePackagePath:               strings.TrimSpace(report.EvidencePackagePath),
		MessageManifestPath:               strings.TrimSpace(report.MessageManifestPath),
		MessageHistory:                    append([]agentcore.AgentMessageRef(nil), report.MessageHistory...),
		LatestAnalysisHandoffMessage:      cloneAgentMessageRef(report.LatestAnalysisHandoffMessage),
		LatestValidationRequestMessage:    cloneAgentMessageRef(report.LatestValidationRequestMessage),
		LatestValidationResultMessage:     cloneAgentMessageRef(report.LatestValidationResultMessage),
		LatestActionDecisionMessage:       cloneAgentMessageRef(report.LatestActionDecisionMessage),
		LatestPostActionValidationMessage: cloneAgentMessageRef(report.LatestPostActionValidationMessage),
		LatestCompensationMessage:         cloneAgentMessageRef(report.LatestCompensationMessage),
	}
}

func extractPrimarySystemMetrics(contract SystemPerformanceCase, execution eval.WorkflowCaseExecution) SystemPerformanceMetrics {
	report := execution.Report
	run := execution.DurableRun
	validation := report.Validation
	stageLatencies, endToEndLatency, analysisLatency, stageValidationLatency, handoffSerializationLatency := stageLatencyMetrics(report.Stages)
	validationLatency := stageValidationLatency
	if !validation.CompletedAt.IsZero() && !validation.StartedAt.IsZero() {
		validationLatency = validation.CompletedAt.Sub(validation.StartedAt).Seconds() * 1000
	}
	toolLatency := totalToolLatency(run)
	messageHistory := report.MessageHistory
	if len(messageHistory) == 0 && run != nil {
		messageHistory = append([]agentcore.AgentMessageRef(nil), run.MessageHistory...)
	}
	return SystemPerformanceMetrics{
		RootCauseTop1Rate:                   boolScore(execution.Result.RootCauseTop1),
		RootCauseTopKRate:                   boolScore(execution.Result.RootCauseTop3),
		HypothesisSupportCorrectness:        hypothesisSupportCorrectness(report),
		ContradictionDetectionRate:          contradictionDetectionRate(validation),
		RecommendationValidationCorrectness: execution.Result.RecommendationCoverageWithRAG,
		RemediationVerdictCorrectness:       remediationVerdictCorrectness(contract, validation.PostActionValidation),
		FinalIncidentOutcomeCorrectness:     finalIncidentOutcomeCorrectness(contract, execution),

		AnalysisHandoffCoverage:        boolScore(execution.Result.AnalysisHandoffRecorded),
		ValidationReportCoverage:       boolScore(execution.Result.ValidationReportRecorded),
		ActionPlanCoverage:             actionPlanCoverage(validation, run),
		PostActionValidationCoverage:   boolScore(validation.PostActionValidation != nil),
		EvidencePackageCoverage:        boolScore(execution.Result.EvidencePackageGenerated),
		MemoryWritebackCoverage:        boolScore(execution.Result.MemoryWriteback),
		RollbackOrCompensationCoverage: rollbackCoverage(contract, validation, run),

		GovernanceCoverage:               execution.Result.GovernanceCoverage,
		ApprovalEnforcementRate:          approvalEnforcementRate(validation, run),
		DryRunCompliance:                 dryRunCompliance(contract, validation, run),
		ExecutionCategoryEnforcementRate: executionCategoryCoverage(validation, run),
		IdempotencyPreservationRate:      idempotencyRate(run),
		AuditCompleteness:                auditCompleteness(run),

		EndToEndLatencyMS:             endToEndLatency,
		PerStageLatencyMS:             stageLatencies,
		AnalysisAgentLatencyMS:        analysisLatency,
		ValidationAgentLatencyMS:      validationLatency,
		HandoffSerializationLatencyMS: handoffSerializationLatency,
		HandoffParseLatencyMS:         float64(validation.HandoffParseLatencyMS),
		ToolCallCount:                 float64(toolCallCount(run)),
		ToolLatencyMS:                 toolLatency,
		TokenCost:                     float64(execution.WorkflowMetrics.TokenCostTotal),

		ReplayStabilityScore:                  1,
		VerdictConsistency:                    1,
		MessageReproducibility:                collaborationMessageReproducibility(messageHistory, messageHistory),
		HandoffSchemaValidRate:                handoffSchemaValidRate(messageHistory),
		HandoffParseSuccessRate:               handoffParseSuccessRate(validation),
		HandoffRequiredFieldsCoverage:         handoffRequiredFieldsCoverage(report.AnalysisHandoff),
		HandoffTargetExtractionScore:          handoffTargetExtractionScore(report),
		CrossAgentInformationRetentionScore:   crossAgentInformationRetentionScore(report),
		MessageHistoryIntegrityScore:          messageHistoryIntegrityScore(messageHistory),
		AgentAgreementScore:                   agentAgreementScore(report),
		ParentChildMessageLinkageCompleteness: parentChildLinkageCompleteness(messageHistory),
	}
}

func mergeReplayMetrics(metrics *SystemPerformanceMetrics, executions []eval.WorkflowCaseExecution) {
	if metrics == nil || len(executions) < 2 {
		return
	}
	primary := executions[0]
	totalRankingDrift := 0.0
	totalToolDrift := 0.0
	totalVerdict := 0.0
	totalMessage := 0.0
	totalLoopDrift := 0.0
	totalStability := 0.0
	replays := 0.0
	for _, replay := range executions[1:] {
		replays++
		rankingDrift := rankingDrift(primary, replay)
		toolDrift := toolSelectionDrift(primary, replay)
		verdictConsistency := verdictConsistency(primary, replay)
		messageRepro := collaborationMessageReproducibility(primary.Report.MessageHistory, replay.Report.MessageHistory)
		loopDrift := validationLoopDrift(primary, replay)
		stability := average(1-rankingDrift, 1-toolDrift, verdictConsistency, messageRepro, 1-loopDrift)
		totalRankingDrift += rankingDrift
		totalToolDrift += toolDrift
		totalVerdict += verdictConsistency
		totalMessage += messageRepro
		totalLoopDrift += loopDrift
		totalStability += stability
	}
	metrics.RankingDrift = totalRankingDrift / replays
	metrics.ToolSelectionDrift = totalToolDrift / replays
	metrics.VerdictConsistency = totalVerdict / replays
	metrics.MessageReproducibility = totalMessage / replays
	metrics.ValidationLoopDrift = totalLoopDrift / replays
	metrics.ReplayStabilityScore = totalStability / replays
}

func validateSystemPerformanceCase(contract SystemPerformanceCase, execution eval.WorkflowCaseExecution, metrics SystemPerformanceMetrics, artifacts SystemPerformanceArtifactRefs) []string {
	failures := make([]string, 0, 8)
	if len(contract.ExpectedRootCauseAny) > 0 && !execution.Result.RootCauseTop3 {
		failures = append(failures, fmt.Sprintf("root cause top-3 did not match expected %v", contract.ExpectedRootCauseAny))
	}
	if len(contract.ExpectedValidatedRecommendationAny) > 0 && metrics.RecommendationValidationCorrectness < 0.5 {
		failures = append(failures, fmt.Sprintf("validated recommendation coverage %.2f below minimum 0.50", metrics.RecommendationValidationCorrectness))
	}
	if contract.ExpectedGovernance.RequireMessageProtocol && len(artifacts.MessageHistory) == 0 {
		failures = append(failures, "message protocol was required but no durable message history was recorded")
	}
	if contract.ExpectedGovernance.RequireValidationAgent && strings.TrimSpace(execution.Report.Validation.Agent) != "validation_action_agent" {
		failures = append(failures, "validation agent was required but the runtime did not persist the validation report")
	}
	if mode := strings.TrimSpace(contract.ExpectedGovernance.ExpectedValidationMode); mode != "" && !strings.EqualFold(strings.TrimSpace(execution.Report.Validation.Mode), mode) {
		failures = append(failures, fmt.Sprintf("validation mode %q did not match expected %q", execution.Report.Validation.Mode, mode))
	}
	if category := strings.TrimSpace(contract.ExpectedGovernance.ExpectedExecutionCategory); category != "" {
		actual := strings.TrimSpace(postActionCategory(execution.Report.Validation.PostActionValidation))
		if execution.Report.Validation.Governance != nil {
			actual = strings.TrimSpace(firstNonEmpty(execution.Report.Validation.Governance.ExecutionCategory, actual))
		}
		if !strings.EqualFold(actual, category) {
			failures = append(failures, fmt.Sprintf("execution category %q did not match expected %q", actual, category))
		}
	}
	if contract.ExpectedGovernance.RequireApprovalEnforcement && metrics.ApprovalEnforcementRate < 1 {
		failures = append(failures, fmt.Sprintf("approval enforcement rate %.2f below 1.00", metrics.ApprovalEnforcementRate))
	}
	if contract.ExpectedGovernance.ExpectDryRun && metrics.DryRunCompliance < 1 {
		failures = append(failures, fmt.Sprintf("dry-run compliance %.2f below 1.00", metrics.DryRunCompliance))
	}
	if verdicts := contract.ExpectedPostAction.ExpectedVerdictAny; len(verdicts) > 0 {
		actual := strings.ToLower(strings.TrimSpace(postActionVerdict(execution.Report.Validation.PostActionValidation)))
		if !stringSliceContainsFold(verdicts, actual) {
			failures = append(failures, fmt.Sprintf("post-action verdict %q did not match expected %v", actual, verdicts))
		}
	}
	if fallback := strings.TrimSpace(contract.ExpectedPostAction.ExpectedFallback); fallback != "" {
		actual := strings.TrimSpace(postActionFallback(execution.Report.Validation.PostActionValidation))
		if !strings.EqualFold(actual, fallback) {
			failures = append(failures, fmt.Sprintf("post-action fallback %q did not match expected %q", actual, fallback))
		}
	}
	if contract.ExpectedArtifacts.RequireAnalysisHandoff && metrics.AnalysisHandoffCoverage < 1 {
		failures = append(failures, "analysis handoff artifact missing")
	}
	if contract.ExpectedArtifacts.RequireValidationReport && metrics.ValidationReportCoverage < 1 {
		failures = append(failures, "validation report artifact missing")
	}
	if contract.ExpectedArtifacts.RequireActionPlan && metrics.ActionPlanCoverage < 1 {
		failures = append(failures, "action-plan artifact missing")
	}
	if contract.ExpectedArtifacts.RequirePostActionValidation && metrics.PostActionValidationCoverage < 1 {
		failures = append(failures, "post-action validation artifact missing")
	}
	if contract.ExpectedArtifacts.RequireEvidencePackage && metrics.EvidencePackageCoverage < 1 {
		failures = append(failures, "evidence package missing")
	}
	if contract.ExpectedArtifacts.RequireMemoryWriteback && metrics.MemoryWritebackCoverage < 1 {
		failures = append(failures, "memory write-back missing")
	}
	if contract.ExpectedArtifacts.RequireRollbackOrCompensation && metrics.RollbackOrCompensationCoverage < 1 {
		failures = append(failures, "rollback/compensation coverage missing")
	}
	for _, item := range contract.ExpectedArtifacts.RequiredMessageTypes {
		if !messageTypePresent(artifacts.MessageHistory, item) {
			failures = append(failures, fmt.Sprintf("required message type %q missing from message history", item))
		}
	}
	return failures
}

func aggregateSystemPerformanceMetrics(cases []SystemPerformanceCaseResult) SystemPerformanceMetrics {
	if len(cases) == 0 {
		return SystemPerformanceMetrics{}
	}
	sum := SystemPerformanceMetrics{
		PerStageLatencyMS: make(map[string]float64),
	}
	successes := 0.0
	tokenCostTotal := 0.0
	for _, item := range cases {
		m := item.Metrics
		sum.RootCauseTop1Rate += m.RootCauseTop1Rate
		sum.RootCauseTopKRate += m.RootCauseTopKRate
		sum.HypothesisSupportCorrectness += m.HypothesisSupportCorrectness
		sum.ContradictionDetectionRate += m.ContradictionDetectionRate
		sum.RecommendationValidationCorrectness += m.RecommendationValidationCorrectness
		sum.RemediationVerdictCorrectness += m.RemediationVerdictCorrectness
		sum.FinalIncidentOutcomeCorrectness += m.FinalIncidentOutcomeCorrectness
		sum.AnalysisHandoffCoverage += m.AnalysisHandoffCoverage
		sum.ValidationReportCoverage += m.ValidationReportCoverage
		sum.ActionPlanCoverage += m.ActionPlanCoverage
		sum.PostActionValidationCoverage += m.PostActionValidationCoverage
		sum.EvidencePackageCoverage += m.EvidencePackageCoverage
		sum.MemoryWritebackCoverage += m.MemoryWritebackCoverage
		sum.RollbackOrCompensationCoverage += m.RollbackOrCompensationCoverage
		sum.GovernanceCoverage += m.GovernanceCoverage
		sum.ApprovalEnforcementRate += m.ApprovalEnforcementRate
		sum.DryRunCompliance += m.DryRunCompliance
		sum.ExecutionCategoryEnforcementRate += m.ExecutionCategoryEnforcementRate
		sum.IdempotencyPreservationRate += m.IdempotencyPreservationRate
		sum.AuditCompleteness += m.AuditCompleteness
		sum.EndToEndLatencyMS += m.EndToEndLatencyMS
		sum.AnalysisAgentLatencyMS += m.AnalysisAgentLatencyMS
		sum.ValidationAgentLatencyMS += m.ValidationAgentLatencyMS
		sum.HandoffSerializationLatencyMS += m.HandoffSerializationLatencyMS
		sum.HandoffParseLatencyMS += m.HandoffParseLatencyMS
		sum.ToolCallCount += m.ToolCallCount
		sum.ToolLatencyMS += m.ToolLatencyMS
		sum.TokenCost += m.TokenCost
		sum.ReplayStabilityScore += m.ReplayStabilityScore
		sum.RankingDrift += m.RankingDrift
		sum.ToolSelectionDrift += m.ToolSelectionDrift
		sum.VerdictConsistency += m.VerdictConsistency
		sum.MessageReproducibility += m.MessageReproducibility
		sum.ValidationLoopDrift += m.ValidationLoopDrift
		sum.HandoffSchemaValidRate += m.HandoffSchemaValidRate
		sum.HandoffParseSuccessRate += m.HandoffParseSuccessRate
		sum.HandoffRequiredFieldsCoverage += m.HandoffRequiredFieldsCoverage
		sum.HandoffTargetExtractionScore += m.HandoffTargetExtractionScore
		sum.CrossAgentInformationRetentionScore += m.CrossAgentInformationRetentionScore
		sum.MessageHistoryIntegrityScore += m.MessageHistoryIntegrityScore
		sum.AgentAgreementScore += m.AgentAgreementScore
		sum.ParentChildMessageLinkageCompleteness += m.ParentChildMessageLinkageCompleteness
		for stage, latency := range m.PerStageLatencyMS {
			sum.PerStageLatencyMS[stage] += latency
		}
		tokenCostTotal += m.TokenCost
		if item.Passed {
			successes++
		}
	}
	count := float64(len(cases))
	sum.RootCauseTop1Rate /= count
	sum.RootCauseTopKRate /= count
	sum.HypothesisSupportCorrectness /= count
	sum.ContradictionDetectionRate /= count
	sum.RecommendationValidationCorrectness /= count
	sum.RemediationVerdictCorrectness /= count
	sum.FinalIncidentOutcomeCorrectness /= count
	sum.AnalysisHandoffCoverage /= count
	sum.ValidationReportCoverage /= count
	sum.ActionPlanCoverage /= count
	sum.PostActionValidationCoverage /= count
	sum.EvidencePackageCoverage /= count
	sum.MemoryWritebackCoverage /= count
	sum.RollbackOrCompensationCoverage /= count
	sum.GovernanceCoverage /= count
	sum.ApprovalEnforcementRate /= count
	sum.DryRunCompliance /= count
	sum.ExecutionCategoryEnforcementRate /= count
	sum.IdempotencyPreservationRate /= count
	sum.AuditCompleteness /= count
	sum.EndToEndLatencyMS /= count
	sum.AnalysisAgentLatencyMS /= count
	sum.ValidationAgentLatencyMS /= count
	sum.HandoffSerializationLatencyMS /= count
	sum.HandoffParseLatencyMS /= count
	sum.ToolCallCount /= count
	sum.ToolLatencyMS /= count
	sum.TokenCost /= count
	sum.ReplayStabilityScore /= count
	sum.RankingDrift /= count
	sum.ToolSelectionDrift /= count
	sum.VerdictConsistency /= count
	sum.MessageReproducibility /= count
	sum.ValidationLoopDrift /= count
	sum.HandoffSchemaValidRate /= count
	sum.HandoffParseSuccessRate /= count
	sum.HandoffRequiredFieldsCoverage /= count
	sum.HandoffTargetExtractionScore /= count
	sum.CrossAgentInformationRetentionScore /= count
	sum.MessageHistoryIntegrityScore /= count
	sum.AgentAgreementScore /= count
	sum.ParentChildMessageLinkageCompleteness /= count
	for stage, latency := range sum.PerStageLatencyMS {
		sum.PerStageLatencyMS[stage] = latency / count
	}
	if successes > 0 {
		sum.CostPerSuccessfulCase = tokenCostTotal / successes
	}
	return sum
}

func persistSystemPerformanceReport(repoRoot string, report SystemPerformanceReport) (string, string, error) {
	root := filepath.Join(repoRoot, "data", "eval", "system_performance")
	historyDir := filepath.Join(root, "history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return "", "", err
	}
	stamp := report.GeneratedAt.Format("20060102T150405Z")
	historyPath := filepath.Join(historyDir, stamp+".json")
	latestPath := filepath.Join(root, "latest.json")
	report.LatestPath = latestPath
	report.HistoryPath = historyPath
	raw, err := report.JSON()
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(historyPath, raw, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(latestPath, raw, 0o644); err != nil {
		return "", "", err
	}
	return latestPath, historyPath, nil
}

func compareSystemPerformanceReports(current, baseline SystemPerformanceReport, baselinePath string) *SystemPerformanceComparison {
	return &SystemPerformanceComparison{
		BaselinePath:        baselinePath,
		BaselineGeneratedAt: baseline.GeneratedAt,
		ScoreDeltas: map[string]float64{
			"correctness":   current.Scorecard.Correctness - baseline.Scorecard.Correctness,
			"closure":       current.Scorecard.Closure - baseline.Scorecard.Closure,
			"governance":    current.Scorecard.Governance - baseline.Scorecard.Governance,
			"efficiency":    current.Scorecard.Efficiency - baseline.Scorecard.Efficiency,
			"stability":     current.Scorecard.Stability - baseline.Scorecard.Stability,
			"collaboration": current.Scorecard.Collaboration - baseline.Scorecard.Collaboration,
			"overall_score": current.Scorecard.OverallScore - baseline.Scorecard.OverallScore,
		},
		LatencyDeltas: map[string]float64{
			"end_to_end_latency_ms":            current.Metrics.EndToEndLatencyMS - baseline.Metrics.EndToEndLatencyMS,
			"analysis_agent_latency_ms":        current.Metrics.AnalysisAgentLatencyMS - baseline.Metrics.AnalysisAgentLatencyMS,
			"validation_agent_latency_ms":      current.Metrics.ValidationAgentLatencyMS - baseline.Metrics.ValidationAgentLatencyMS,
			"handoff_parse_latency_ms":         current.Metrics.HandoffParseLatencyMS - baseline.Metrics.HandoffParseLatencyMS,
			"handoff_serialization_latency_ms": current.Metrics.HandoffSerializationLatencyMS - baseline.Metrics.HandoffSerializationLatencyMS,
		},
		GovernanceDeltas: map[string]float64{
			"governance_coverage":                 current.Metrics.GovernanceCoverage - baseline.Metrics.GovernanceCoverage,
			"approval_enforcement_rate":           current.Metrics.ApprovalEnforcementRate - baseline.Metrics.ApprovalEnforcementRate,
			"dry_run_compliance":                  current.Metrics.DryRunCompliance - baseline.Metrics.DryRunCompliance,
			"execution_category_enforcement_rate": current.Metrics.ExecutionCategoryEnforcementRate - baseline.Metrics.ExecutionCategoryEnforcementRate,
		},
		CollaborationDeltas: map[string]float64{
			"handoff_schema_valid_rate":                 current.Metrics.HandoffSchemaValidRate - baseline.Metrics.HandoffSchemaValidRate,
			"handoff_parse_success_rate":                current.Metrics.HandoffParseSuccessRate - baseline.Metrics.HandoffParseSuccessRate,
			"cross_agent_information_retention_score":   current.Metrics.CrossAgentInformationRetentionScore - baseline.Metrics.CrossAgentInformationRetentionScore,
			"message_history_integrity_score":           current.Metrics.MessageHistoryIntegrityScore - baseline.Metrics.MessageHistoryIntegrityScore,
			"parent_child_message_linkage_completeness": current.Metrics.ParentChildMessageLinkageCompleteness - baseline.Metrics.ParentChildMessageLinkageCompleteness,
		},
	}
}

func stageLatencyMetrics(stages []agentcore.PipelineStageResult) (map[string]float64, float64, float64, float64, float64) {
	perStage := make(map[string]float64, len(stages))
	var start time.Time
	var end time.Time
	analysisLatency := 0.0
	validationLatency := 0.0
	handoffLatency := 0.0
	for _, stage := range stages {
		tsStart := stage.StartedAt
		tsEnd := stage.CompletedAt
		if tsStart.IsZero() || tsEnd.IsZero() {
			continue
		}
		latency := tsEnd.Sub(tsStart).Seconds() * 1000
		perStage[stage.Name] = latency
		if start.IsZero() || tsStart.Before(start) {
			start = tsStart
		}
		if end.IsZero() || tsEnd.After(end) {
			end = tsEnd
		}
		switch {
		case stage.Name == "analysis_handoff_finalize":
			handoffLatency = latency
			analysisLatency += latency
		case isValidationStage(stage.Name):
			validationLatency += latency
		default:
			analysisLatency += latency
		}
	}
	endToEnd := 0.0
	if !start.IsZero() && !end.IsZero() {
		endToEnd = end.Sub(start).Seconds() * 1000
	}
	return perStage, endToEnd, analysisLatency, validationLatency, handoffLatency
}

func isValidationStage(name string) bool {
	switch strings.TrimSpace(name) {
	case "validation_action_react_loop", "guarded_execution_plan", "post_action_validation":
		return true
	default:
		return false
	}
}

func toolCallCount(run *agentcore.DurableRun) int {
	if run == nil {
		return 0
	}
	return len(run.ToolCalls)
}

func totalToolLatency(run *agentcore.DurableRun) float64 {
	if run == nil {
		return 0
	}
	total := 0.0
	for _, call := range run.ToolCalls {
		if call.StartedAt.IsZero() || call.CompletedAt.IsZero() {
			continue
		}
		total += call.CompletedAt.Sub(call.StartedAt).Seconds() * 1000
	}
	return total
}

func hypothesisSupportCorrectness(report agentcore.RCAWorkflowReport) float64 {
	if len(report.Validation.Results) == 0 {
		if len(report.AnalysisHandoff.Hypotheses) > 0 {
			return 0
		}
		return 1
	}
	relevant := 0
	supported := 0
	for _, item := range report.Validation.Results {
		if strings.TrimSpace(item.HypothesisID) == "" {
			continue
		}
		relevant++
		if item.Verdict == agentcore.ValidationVerdictConfirmed || item.Verdict == agentcore.ValidationVerdictPartiallySupported {
			supported++
		}
	}
	if relevant == 0 {
		return boolScore(len(report.AnalysisHandoff.Hypotheses) == 0 || len(report.Validation.Results) > 0)
	}
	return float64(supported) / float64(relevant)
}

func contradictionDetectionRate(report agentcore.ValidationActionReport) float64 {
	if len(report.Results) == 0 {
		return 0
	}
	relevant := 0
	contradicted := 0
	for _, item := range report.Results {
		if item.TargetType != agentcore.ValidationTargetContradiction && len(item.ContradictingEvidenceIDs) == 0 {
			continue
		}
		relevant++
		if item.Verdict == agentcore.ValidationVerdictContradicted {
			contradicted++
		}
	}
	if relevant == 0 {
		if len(report.ContradictionSummary) > 0 {
			return 1
		}
		return 1
	}
	return float64(contradicted) / float64(relevant)
}

func remediationVerdictCorrectness(contract SystemPerformanceCase, summary *agentcore.PostActionValidationSummary) float64 {
	if summary == nil {
		return 0
	}
	if len(contract.ExpectedPostAction.ExpectedVerdictAny) == 0 {
		return 1
	}
	if stringSliceContainsFold(contract.ExpectedPostAction.ExpectedVerdictAny, string(summary.Verdict)) {
		return 1
	}
	return 0
}

func finalIncidentOutcomeCorrectness(contract SystemPerformanceCase, execution eval.WorkflowCaseExecution) float64 {
	score := 0.0
	score += boolScore(execution.Result.RootCauseTop3)
	score += boolScore(execution.Result.ValidationReportRecorded)
	score += boolScore(execution.Result.EvidencePackageGenerated)
	score += remediationVerdictCorrectness(contract, execution.Report.Validation.PostActionValidation)
	return score / 4
}

func actionPlanCoverage(report agentcore.ValidationActionReport, run *agentcore.DurableRun) float64 {
	if report.SelectedActionContract != nil || len(report.ActionSummary) > 0 {
		return 1
	}
	if run == nil {
		return 0
	}
	for _, step := range run.Steps {
		if step.ActionContract != nil || strings.TrimSpace(step.Stage) == "guarded_execution_plan" {
			return 1
		}
	}
	return 0
}

func rollbackCoverage(contract SystemPerformanceCase, report agentcore.ValidationActionReport, run *agentcore.DurableRun) float64 {
	requires := contract.ExpectedArtifacts.RequireRollbackOrCompensation
	if !requires {
		return 1
	}
	if report.CompensationMessage != nil {
		return 1
	}
	if run == nil {
		return 0
	}
	for _, step := range run.Steps {
		if step.Compensation != nil {
			return 1
		}
	}
	return 0
}

func approvalEnforcementRate(report agentcore.ValidationActionReport, run *agentcore.DurableRun) float64 {
	eligible := 0
	covered := 0
	if report.SelectedActionContract != nil && report.SelectedActionContract.RequiresApproval {
		eligible++
		if report.Governance != nil && strings.TrimSpace(report.Governance.ApprovalState) != "" {
			covered++
		}
	}
	if run != nil {
		for _, step := range run.Steps {
			if step.ActionContract == nil || !step.ActionContract.RequiresApproval {
				continue
			}
			eligible++
			if step.Approval != nil && strings.TrimSpace(step.Approval.State) != "" {
				covered++
			}
		}
	}
	if eligible == 0 {
		return 1
	}
	return float64(covered) / float64(eligible)
}

func dryRunCompliance(contract SystemPerformanceCase, report agentcore.ValidationActionReport, run *agentcore.DurableRun) float64 {
	expectDryRun := contract.ExpectedGovernance.ExpectDryRun
	if report.SelectedActionContract != nil && report.SelectedActionContract.DryRunDefault {
		expectDryRun = true
	}
	if !expectDryRun {
		return 1
	}
	eligible := 0
	compliant := 0
	if run != nil {
		for _, call := range run.ToolCalls {
			if call.Tool != agentcore.ToolRemediation {
				continue
			}
			eligible++
			if call.DryRun {
				compliant++
			}
		}
	}
	if eligible == 0 {
		return 1
	}
	return float64(compliant) / float64(eligible)
}

func executionCategoryCoverage(report agentcore.ValidationActionReport, run *agentcore.DurableRun) float64 {
	eligible := 0
	covered := 0
	if run != nil {
		for _, call := range run.ToolCalls {
			if call.Actor != "validation_action_agent" && !isValidationStage(call.Stage) {
				continue
			}
			eligible++
			if strings.TrimSpace(call.ExecutionCategory) != "" {
				covered++
			}
		}
		for _, step := range run.Steps {
			if step.ActionContract == nil && strings.TrimSpace(step.ExecutionCategory) == "" {
				continue
			}
			eligible++
			if strings.TrimSpace(step.ExecutionCategory) != "" {
				covered++
			}
		}
	}
	if report.Governance != nil {
		eligible++
		if strings.TrimSpace(report.Governance.ExecutionCategory) != "" {
			covered++
		}
	}
	if eligible == 0 {
		return 1
	}
	return float64(covered) / float64(eligible)
}

func idempotencyRate(run *agentcore.DurableRun) float64 {
	if run == nil || len(run.ToolCalls) == 0 {
		return 1
	}
	eligible := 0
	covered := 0
	for _, call := range run.ToolCalls {
		if call.Tool == "" {
			continue
		}
		eligible++
		if strings.TrimSpace(call.IdempotencyKey) != "" {
			covered++
		}
	}
	if eligible == 0 {
		return 1
	}
	return float64(covered) / float64(eligible)
}

func auditCompleteness(run *agentcore.DurableRun) float64 {
	if run == nil {
		return 0
	}
	components := []float64{
		boolScore(len(run.Events) > 0),
		boolScore(len(run.ToolCalls) > 0),
		boolScore(len(run.Steps) > 0),
		boolScore(run.Validation != nil),
	}
	return average(components...)
}

func handoffSchemaValidRate(history []agentcore.AgentMessageRef) float64 {
	relevant := 0
	valid := 0
	for _, ref := range history {
		if ref.MessageType != agentcore.AgentMessageTypeAnalysisHandoff && ref.MessageType != agentcore.AgentMessageTypeValidationRequest {
			continue
		}
		relevant++
		envelope, err := loadAgentMessageEnvelope(ref.Path)
		if err != nil {
			continue
		}
		if strings.TrimSpace(envelope.Header.SchemaVersion) != "" {
			valid++
		}
	}
	if relevant == 0 {
		return 0
	}
	return float64(valid) / float64(relevant)
}

func handoffParseSuccessRate(report agentcore.ValidationActionReport) float64 {
	if report.Mode == "message_protocol_error" {
		return 0
	}
	if report.SourceAnalysisMessage != nil {
		return 1
	}
	return 0
}

func handoffRequiredFieldsCoverage(handoff agentcore.AnalysisHandoff) float64 {
	fields := []float64{
		boolScore(strings.TrimSpace(handoff.Agent) != ""),
		boolScore(strings.TrimSpace(handoff.IncidentSummary) != ""),
		boolScore(!handoff.CreatedAt.IsZero()),
		boolScore(len(handoff.Hypotheses) > 0 || len(handoff.HypothesisPackets) > 0),
		boolScore(len(handoff.SuggestedValidationTargets) > 0),
		boolScore(len(handoff.BoundedActionCandidates) > 0 || len(handoff.Recommendations) > 0),
	}
	return average(fields...)
}

func handoffTargetExtractionScore(report agentcore.RCAWorkflowReport) float64 {
	total := len(report.AnalysisHandoff.SuggestedValidationTargets)
	if total == 0 {
		return 0
	}
	covered := 0
	targets := make(map[string]struct{}, len(report.Validation.Targets))
	for _, item := range report.Validation.Targets {
		targets[item.ID] = struct{}{}
	}
	for _, item := range report.AnalysisHandoff.SuggestedValidationTargets {
		if _, ok := targets[item.ID]; ok {
			covered++
		}
	}
	return float64(covered) / float64(total)
}

func crossAgentInformationRetentionScore(report agentcore.RCAWorkflowReport) float64 {
	parts := []float64{
		handoffTargetExtractionScore(report),
		agentAgreementScore(report),
	}
	if len(report.AnalysisHandoff.BoundedActionCandidates) > 0 {
		match := 0.0
		selectedID := ""
		if report.Validation.SelectedAction != nil {
			selectedID = report.Validation.SelectedAction.ID
		}
		if strings.TrimSpace(selectedID) == "" && report.Validation.Governance != nil {
			selectedID = report.Validation.Governance.ActionCandidateID
		}
		if strings.TrimSpace(selectedID) != "" {
			for _, item := range report.AnalysisHandoff.BoundedActionCandidates {
				if strings.EqualFold(item.ID, selectedID) {
					match = 1
					break
				}
			}
			parts = append(parts, match)
		}
	}
	return average(parts...)
}

func messageHistoryIntegrityScore(history []agentcore.AgentMessageRef) float64 {
	if len(history) == 0 {
		return 0
	}
	checks := 0.0
	passed := 0.0
	for idx, ref := range history {
		checks++
		if strings.TrimSpace(ref.Path) != "" {
			if _, err := os.Stat(ref.Path); err == nil {
				passed++
			}
		}
		checks++
		if ref.Sequence == idx+1 {
			passed++
		}
		checks++
		if idx == 0 && strings.TrimSpace(ref.PreviousMessageID) == "" {
			passed++
		}
		if idx > 0 && strings.TrimSpace(ref.PreviousMessageID) == history[idx-1].MessageID {
			passed++
		}
		envelope, err := loadAgentMessageEnvelope(ref.Path)
		checks++
		if err == nil && strings.EqualFold(strings.TrimSpace(envelope.Header.MessageID), strings.TrimSpace(ref.MessageID)) {
			passed++
		}
	}
	return passed / checks
}

func agentAgreementScore(report agentcore.RCAWorkflowReport) float64 {
	if len(report.AnalysisHandoff.HypothesisPackets) == 0 && len(report.AnalysisHandoff.Hypotheses) == 0 {
		return 1
	}
	topHypothesisID := ""
	if len(report.AnalysisHandoff.HypothesisPackets) > 0 {
		topHypothesisID = strings.TrimSpace(report.AnalysisHandoff.HypothesisPackets[0].HypothesisID)
	}
	if topHypothesisID == "" && len(report.AnalysisHandoff.Hypotheses) > 0 {
		topHypothesisID = strings.TrimSpace(report.AnalysisHandoff.Hypotheses[0].ID)
	}
	for _, item := range report.Validation.Results {
		if !strings.EqualFold(strings.TrimSpace(item.HypothesisID), topHypothesisID) {
			continue
		}
		switch item.Verdict {
		case agentcore.ValidationVerdictConfirmed:
			return 1
		case agentcore.ValidationVerdictPartiallySupported:
			return 0.7
		case agentcore.ValidationVerdictContradicted:
			return 0
		default:
			return 0.4
		}
	}
	return 0.5
}

func parentChildLinkageCompleteness(history []agentcore.AgentMessageRef) float64 {
	if len(history) == 0 {
		return 0
	}
	eligible := 0
	covered := 0
	for _, ref := range history {
		if ref.MessageType == agentcore.AgentMessageTypeAnalysisHandoff {
			continue
		}
		eligible++
		if strings.TrimSpace(ref.ParentMessageID) != "" {
			covered++
		}
	}
	if eligible == 0 {
		return 1
	}
	return float64(covered) / float64(eligible)
}

func rankingDrift(primary, replay eval.WorkflowCaseExecution) float64 {
	sameRoot := strings.EqualFold(strings.TrimSpace(primary.Result.TopRootCause), strings.TrimSpace(replay.Result.TopRootCause))
	sameRecommendation := strings.EqualFold(strings.TrimSpace(primary.Result.TopRecommendation), strings.TrimSpace(replay.Result.TopRecommendation))
	sameRootTop3 := primary.Result.RootCauseTop3 == replay.Result.RootCauseTop3
	return 1 - average(boolScore(sameRoot), boolScore(sameRecommendation), boolScore(sameRootTop3))
}

func toolSelectionDrift(primary, replay eval.WorkflowCaseExecution) float64 {
	return 1 - jaccard(toolSequence(primary.DurableRun), toolSequence(replay.DurableRun))
}

func verdictConsistency(primary, replay eval.WorkflowCaseExecution) float64 {
	return boolScore(strings.EqualFold(postActionVerdict(primary.Report.Validation.PostActionValidation), postActionVerdict(replay.Report.Validation.PostActionValidation)))
}

func collaborationMessageReproducibility(primary, replay []agentcore.AgentMessageRef) float64 {
	if len(primary) == 0 || len(replay) == 0 {
		return 0
	}
	primaryTypes := messageTypeSequence(primary)
	replayTypes := messageTypeSequence(replay)
	return jaccard(primaryTypes, replayTypes)
}

func validationLoopDrift(primary, replay eval.WorkflowCaseExecution) float64 {
	a := len(primary.Report.Validation.LoopRecords)
	b := len(replay.Report.Validation.LoopRecords)
	if a == 0 && b == 0 {
		return 0
	}
	maxCount := max(a, b)
	if maxCount == 0 {
		return 0
	}
	return float64(absInt(a-b)) / float64(maxCount)
}

func messageTypePresent(history []agentcore.AgentMessageRef, target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	for _, item := range history {
		if strings.TrimSpace(strings.ToLower(string(item.MessageType))) == target {
			return true
		}
	}
	return false
}

func messageTypeSequence(history []agentcore.AgentMessageRef) []string {
	out := make([]string, 0, len(history))
	for _, item := range history {
		out = append(out, string(item.MessageType))
	}
	return out
}

func toolSequence(run *agentcore.DurableRun) []string {
	if run == nil {
		return nil
	}
	out := make([]string, 0, len(run.ToolCalls))
	for _, call := range run.ToolCalls {
		out = append(out, string(call.Tool))
	}
	return out
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	left := make(map[string]struct{}, len(a))
	right := make(map[string]struct{}, len(b))
	for _, item := range a {
		left[strings.TrimSpace(strings.ToLower(item))] = struct{}{}
	}
	for _, item := range b {
		right[strings.TrimSpace(strings.ToLower(item))] = struct{}{}
	}
	intersection := 0
	union := len(left)
	for item := range right {
		if _, ok := left[item]; ok {
			intersection++
			continue
		}
		union++
	}
	if union == 0 {
		return 1
	}
	return float64(intersection) / float64(union)
}

func loadAgentMessageEnvelope(path string) (agentcore.AgentMessageEnvelope, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return agentcore.AgentMessageEnvelope{}, err
	}
	var envelope agentcore.AgentMessageEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return agentcore.AgentMessageEnvelope{}, err
	}
	if expected := strings.TrimSpace(envelope.Body.ContentHash); expected != "" {
		actual := agentMessageContentHash(envelope.Body.Payload)
		if actual != expected {
			return agentcore.AgentMessageEnvelope{}, fmt.Errorf("agent message content hash mismatch for %s", path)
		}
	}
	return envelope, nil
}

func agentMessageContentHash(payload []byte) string {
	normalized := payload
	if len(payload) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, payload); err == nil && compact.Len() > 0 {
			normalized = compact.Bytes()
		}
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

func cloneAgentMessageRef(ref *agentcore.AgentMessageRef) *agentcore.AgentMessageRef {
	if ref == nil {
		return nil
	}
	copy := *ref
	return &copy
}

func postActionVerdict(summary *agentcore.PostActionValidationSummary) string {
	if summary == nil {
		return ""
	}
	return string(summary.Verdict)
}

func postActionFallback(summary *agentcore.PostActionValidationSummary) string {
	if summary == nil {
		return ""
	}
	return strings.TrimSpace(summary.FallbackMode)
}

func postActionCategory(summary *agentcore.PostActionValidationSummary) string {
	if summary == nil {
		return ""
	}
	return strings.TrimSpace(summary.ExecutionCategory)
}

func boolScore(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, item := range values {
		key := strings.TrimSpace(item)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, item := range values {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringSliceContainsFold(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range values {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}
