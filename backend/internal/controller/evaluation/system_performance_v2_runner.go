package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// Evaluation v2 runner: repeated-trial, task-success-first system benchmark.

// SystemPerformanceV2Options controls one evaluation v2 run.
type SystemPerformanceV2Options struct {
	Scope         eval.Scope
	RepoRoot      string
	Variant       string
	RuntimeMode   string // legacy_deterministic | hybrid_adaptive | full_adaptive
	ComparePath   string
	Trials        int
	Seed          int64
	CaseIDs       []string
	ReportDir     string
	IncludeTrials bool
}

const v2SchemaVersion = "system-performance/v2"

const (
	v2MetricTaskOutcome         = "task_outcome"
	v2MetricDiagnosisQuality    = "diagnosis_quality"
	v2MetricSafetyGovernance    = "safety_governance"
	v2MetricApprovalEnforcement = "approval_enforcement"
	v2MetricDryRunCompliance    = "dry_run_compliance"
	v2MetricTrajectoryTool      = "trajectory_tool"
	v2MetricEndToEndLatency     = "end_to_end_latency"
	v2MetricTimeToDiagnosis     = "time_to_diagnosis"
	v2MetricTimeToMitigation    = "time_to_mitigation"
	v2MetricToolCalls           = "tool_calls"
	v2MetricTokens              = "tokens"
	v2MetricEfficiency          = "efficiency"
	v2MetricReliability         = "reliability"
	v2MetricReplayDescriptive   = "replay_descriptive"
	v2MetricCollaboration       = "collaboration_artifacts"
)

// RunSystemPerformanceV2 executes the evaluation v2 benchmark and persists
// report.json, summary.md, metrics.csv, cases.csv, failure_modes.csv and
// rendered figures.
func RunSystemPerformanceV2(ctx context.Context, opts SystemPerformanceV2Options) (SystemPerformanceReportV2, error) {
	repoRoot, err := eval.ResolveRepoRoot(opts.RepoRoot)
	if err != nil {
		return SystemPerformanceReportV2{}, err
	}
	scope := opts.Scope
	if scope == "" {
		scope = eval.ScopeFast
	}
	cases, err := loadV2Cases(repoRoot, scope, opts.CaseIDs)
	if err != nil {
		return SystemPerformanceReportV2{}, err
	}
	cfg, err := loadV2ScoringConfig(repoRoot)
	if err != nil {
		return SystemPerformanceReportV2{}, err
	}
	trials := opts.Trials
	if trials <= 0 {
		trials = defaultTrialsForScope(scope)
	}
	seed := opts.Seed
	if seed == 0 {
		seed = 42
	}
	runtimeMode := canonicalV2RuntimeMode(opts.RuntimeMode)
	trialSeedSupported := v2RuntimeSupportsTrialSeed(runtimeMode)
	trialSampling := "single_trial"
	statisticalNote := "confidence intervals are case-level; runtime trial randomness is not used"
	if trials > 1 {
		trialSampling = "runtime_unseeded_replay"
		statisticalNote = "workflow runtime has no trial-seed input; repeated trials are descriptive only and confidence intervals resample cases"
	}

	incidentCases, err := eval.LoadIncidentCases(repoRoot)
	if err != nil {
		return SystemPerformanceReportV2{}, err
	}
	incidentByID := make(map[string]eval.IncidentCase, len(incidentCases))
	for _, item := range incidentCases {
		incidentByID[item.ID] = item
	}
	kb, cleanup, err := eval.BuildKnowledgeBase(ctx, repoRoot)
	if err != nil {
		return SystemPerformanceReportV2{}, err
	}
	defer cleanup()

	report := SystemPerformanceReportV2{
		SchemaVersion: v2SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Environment: V2Environment{
			GitCommit:                 gitCommit(repoRoot),
			RuntimeMode:               runtimeMode,
			Variant:                   strings.TrimSpace(opts.Variant),
			GoVersion:                 runtime.Version(),
			Scope:                     string(scope),
			TrialsPerCase:             trials,
			Seed:                      seed,
			SeedPurpose:               "case_bootstrap_only",
			RuntimeTrialSeedSupported: trialSeedSupported,
			TrialSampling:             trialSampling,
			StatisticalNote:           statisticalNote,
			WorktreeDirty:             gitWorktreeDirty(repoRoot),
			CasesConfigPath:           filepath.ToSlash(filepath.Join("eval_data", "system_perf_cases_v2.json")),
			ScoringConfigPath:         filepath.ToSlash(filepath.Join("eval_data", "scoring_v2.json")),
			Timestamp:                 time.Now().UTC(),
			ModelPricing:              cfg.ModelPricing,
		},
		Config: cfg,
	}

	runOpts := eval.WorkflowCaseRunOptions{
		ConfigOverride: eval.WorkflowConfigOverride{
			RuntimeMode: &runtimeMode,
		},
	}

	for _, contract := range cases {
		incident, ok := incidentByID[contract.IncidentCaseID]
		if contract.IncidentCaseInline != nil {
			incident = *contract.IncidentCaseInline
			ok = true
		}
		if !ok {
			return SystemPerformanceReportV2{}, fmt.Errorf("system performance v2 case %s references unknown incident case %s", contract.ID, contract.IncidentCaseID)
		}
		executions := make([]eval.WorkflowCaseExecution, 0, trials)
		trialMetrics := make([]V2TrialMetrics, 0, trials)
		for i := 0; i < trials; i++ {
			execution, err := eval.RunWorkflowCaseDetailed(ctx, kb, incident, runOpts)
			if err != nil {
				return SystemPerformanceReportV2{}, fmt.Errorf("case %s trial %d: %w", contract.ID, i+1, err)
			}
			executions = append(executions, execution)
			trialMetrics = append(trialMetrics, extractV2Trial(contract, execution, cfg))
		}
		caseResult := aggregateV2Case(contract, trialMetrics, executions, cfg)
		caseResult.TrialsIndependent = trialSeedSupported
		caseResult.TaskSuccessCIAvailable = false
		caseResult.TaskSuccessCIMethod = "not_estimated_runtime_trial_seed_unavailable"
		if opts.IncludeTrials {
			caseResult.TrialsDetail = trialMetrics
		}
		report.Cases = append(report.Cases, caseResult)
	}

	report.Aggregate = aggregateV2Benchmark(report.Cases)
	report.FailureModes, report.Aggregate.FailureModeRate, report.Aggregate.FatalFailureRate = benchmarkFailureModes(report.Cases)
	report.Scorecard = computeV2Scorecard(report.Aggregate, cfg)
	report.Gates = evaluateV2Gates(report.Aggregate, report.Cases, cfg)
	report.Categories = computeV2Categories(report.Cases)
	report.ConfidenceIntervals = computeV2ConfidenceIntervals(report.Cases, report.Aggregate, report.Scorecard, cfg, seed)
	if report.ConfidenceIntervals.TaskSuccessRate.Available {
		report.Aggregate.TaskSuccessCI95Low = report.ConfidenceIntervals.TaskSuccessRate.Low
		report.Aggregate.TaskSuccessCI95High = report.ConfidenceIntervals.TaskSuccessRate.High
	}

	verdict := "PASS"
	if !report.Gates.Passed || report.Aggregate.FatalFailureRate > 0 {
		verdict = "FAIL"
	} else if report.Scorecard.OverallScore < cfg.PassingThreshold {
		verdict = "FAIL"
	}
	report.Verdict = verdict

	if strings.TrimSpace(opts.ComparePath) != "" {
		comparison, err := compareV2Reports(report, opts.ComparePath, repoRoot)
		if err != nil {
			return SystemPerformanceReportV2{}, err
		}
		report.Comparison = comparison
		if comparison.Verdict == "fail" {
			report.Verdict = "FAIL"
		}
	}

	reportDir, err := persistV2Report(repoRoot, report, opts.ReportDir)
	if err != nil {
		return SystemPerformanceReportV2{}, err
	}
	report.ReportDir = reportDir

	return report, nil
}

// gitCommit resolves the current HEAD for reproducibility metadata.
func gitCommit(repoRoot string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// gitWorktreeDirty records whether the report was produced from content not
// represented by GitCommit. Baseline comparison code can reject or flag dirty
// reports rather than silently treating the HEAD commit as sufficient identity.
func gitWorktreeDirty(repoRoot string) bool {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=normal")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return true // unknown provenance is conservatively non-clean
	}
	return strings.TrimSpace(string(out)) != ""
}

// v2RuntimeSupportsTrialSeed is deliberately conservative. WorkflowCaseRunOptions
// currently has no seed field, so no runtime mode can promise independently
// seeded, reproducible trials. When that API grows a seed, this function and the
// call site must be updated together.
func v2RuntimeSupportsTrialSeed(_ string) bool { return false }

// canonicalV2RuntimeMode normalizes the runtime-mode override.
func canonicalV2RuntimeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "default", "legacy", "deterministic", "legacy_deterministic":
		return "legacy_deterministic"
	case "hybrid", "hybrid_adaptive":
		return "hybrid_adaptive"
	case "adaptive", "full_adaptive":
		return "full_adaptive"
	default:
		return "legacy_deterministic"
	}
}

// defaultTrialsForScope: fast CI runs few trials, benchmark runs more.
func defaultTrialsForScope(scope eval.Scope) int {
	switch scope {
	case eval.ScopeBenchmark:
		return 5
	case eval.ScopeRegression:
		return 3
	default:
		return 1
	}
}

// extractV2Trial measures one trial of one case.
func extractV2Trial(contract V2Case, execution eval.WorkflowCaseExecution, cfg V2ScoringConfig) V2TrialMetrics {
	claims, corpus := extractRCAClaims(execution)
	gt := contract.GroundTruth

	trial := V2TrialMetrics{}
	success, failures := taskSuccess(contract, claims, corpus, execution, cfg)
	trial.TaskSuccess = success
	trial.Failures = failures

	if gt.NoIncident {
		trial.FalsePositive = noopFalsePositive(contract, execution, cfg)
		trial.ClaimedIncident = trial.FalsePositive
		trial.UnnecessaryActions = noopUnnecessaryActions(execution)
	} else {
		trial.ClaimedIncident = !isNoiseEntityClaim(execution.Report.SuspectedRootCauseEntity) && len(execution.Report.Hypotheses) > 0
	}

	trial.Measured = map[string]bool{v2MetricTaskOutcome: true, v2MetricCollaboration: true}
	if contract.TaskType == TaskTypeDiagnose && len(groundTruthAliases(gt.RootCause)) > 0 {
		precision, recall, recallAt1, recallAt3, recallAt5, f1, _, _ := matchRCAGroundTruth(claims, corpus, gt.RootCause)
		trial.RootCauseEntityPrecision = precision
		trial.RootCauseEntityRecall = recall
		trial.RootCauseEntityRecallAt1 = recallAt1
		trial.RootCauseEntityRecallAt3 = recallAt3
		trial.RootCauseEntityRecallAt5 = recallAt5
		trial.RootCauseEntityF1 = f1
		trial.FaultLocalizationScore = recall
		trial.RootCauseReasoningScore = rcaReasoningScore(gt.RootCause, claims, corpus, gt.RequiredEvidence, execution.Report)
		_, trial.FaultDomainAccuracy = faultDomainAccuracy(gt.RootCause, corpus)
		agentChain := execution.Report.CausalPath
		if len(agentChain) == 0 {
			agentChain = execution.Report.StructuredReport.CausalPath
		}
		_, trial.PropagationChainScore = propagationChainScore(gt.RootCause.PropagationChain, agentChain)
		trial.Measured[v2MetricDiagnosisQuality] = true
	}

	// outcome metrics are only measured on the task types they apply to —
	// a noop case does not silently score "plan correctness 1.0"
	switch contract.TaskType {
	case TaskTypePlan:
		trial.RemediationPlanCorrectness = remediationPlanCorrectness(gt, execution.Report)
	case TaskTypeMitigate:
		trial.RemediationPlanCorrectness = remediationPlanCorrectness(gt, execution.Report)
		trial.VerificationCorrectness = verificationCorrectness(contract, execution)
	case TaskTypeVerify:
		trial.VerificationCorrectness = verificationCorrectness(contract, execution)
	}
	if contract.TaskType == TaskTypeMitigate {
		trial.RecoverySuccess = recoverySuccess(gt, execution)
	}

	trial.Safety = extractV2Safety(contract, execution)
	trial.Trajectory = extractV2Trajectory(contract, execution)
	trial.Efficiency = extractV2Efficiency(execution)
	trial.Collaboration = extractV2Collaboration(execution)
	markV2MeasuredMetrics(&trial, contract, execution)

	trial.FailureModes = detectFailureModes(contract, execution, claims, corpus)
	for _, mode := range fatalV2FailureNames(trial.FailureModes) {
		trial.TaskSuccess = false
		trial.Failures = append(trial.Failures, "fatal failure mode: "+mode)
	}
	trial.Failures = dedupeStrings(trial.Failures)
	return trial
}

// markV2MeasuredMetrics records applicability independently of metric values.
// This prevents a missing trace or non-applicable task from becoming a zero (or
// a vacuous one) in benchmark aggregation.
func markV2MeasuredMetrics(trial *V2TrialMetrics, contract V2Case, execution eval.WorkflowCaseExecution) {
	if trial.Measured == nil {
		trial.Measured = make(map[string]bool)
	}
	run := execution.DurableRun
	if run != nil {
		trial.Measured[v2MetricSafetyGovernance] = true
		trial.Measured[v2MetricToolCalls] = true // zero calls is still a measured count
		if len(run.ToolCalls) > 0 {
			trial.Measured[v2MetricTrajectoryTool] = true
		}
		for _, call := range run.ToolCalls {
			if requiresApproval(call) {
				trial.Measured[v2MetricApprovalEnforcement] = true
			}
			if call.Tool == agentcore.ToolRemediation {
				trial.Measured[v2MetricDryRunCompliance] = true
			}
		}
	}
	if trial.Efficiency.EndToEndLatencyMS > 0 {
		trial.Measured[v2MetricEndToEndLatency] = true
	}
	if contract.TaskType == TaskTypeDiagnose && trial.Efficiency.TimeToDiagnosisMS > 0 {
		trial.Measured[v2MetricTimeToDiagnosis] = true
	}
	if trial.Efficiency.TimeToMitigationMS != nil {
		trial.Measured[v2MetricTimeToMitigation] = true
	}
	if trial.Efficiency.TotalTokens > 0 {
		trial.Measured[v2MetricTokens] = true
	}
	if trial.Measured[v2MetricEndToEndLatency] || trial.Measured[v2MetricTimeToDiagnosis] ||
		trial.Measured[v2MetricTimeToMitigation] || trial.Measured[v2MetricToolCalls] || trial.Measured[v2MetricTokens] {
		trial.Measured[v2MetricEfficiency] = true
	}
}

func fatalV2FailureNames(modes map[string]int) []string {
	names := make([]string, 0)
	for mode, count := range modes {
		if count > 0 && fatalFailureModes[mode] {
			names = append(names, mode)
		}
	}
	sort.Strings(names)
	return names
}

// extractV2Efficiency measures latency, tool calls and tokens. TokenCost is a
// token count in the runtime; dollars are estimated only when pricing is
// configured AND an input/output split exists — never invented.
func extractV2Efficiency(execution eval.WorkflowCaseExecution) V2EfficiencyMetrics {
	metrics := V2EfficiencyMetrics{}
	report := execution.Report
	_, endToEnd, _, _, _ := stageLatencyMetrics(report.Stages)
	metrics.EndToEndLatencyMS = endToEnd
	metrics.TimeToDiagnosisMS = timeToDiagnosisMS(report.Stages)

	if execution.DurableRun != nil {
		for _, call := range execution.DurableRun.ToolCalls {
			if call.Tool == agentcore.ToolRemediation && !call.DryRun && invocationExecuted(call) {
				if !call.CompletedAt.IsZero() && len(report.Stages) > 0 && !report.Stages[0].StartedAt.IsZero() {
					ttm := call.CompletedAt.Sub(report.Stages[0].StartedAt).Seconds() * 1000
					metrics.TimeToMitigationMS = &ttm
					break
				}
			}
		}
	}
	metrics.ToolCallCount = float64(toolCallCount(execution.DurableRun))
	metrics.TotalTokens = float64(execution.WorkflowMetrics.TokenCostTotal)
	// The runtime persists only the token total; without an input/output
	// split the per-direction fields and the dollar estimate stay null.
	return metrics
}

// timeToDiagnosisMS measures from the first stage start to the completion of
// the analysis handoff stage.
func timeToDiagnosisMS(stages []agentcore.PipelineStageResult) float64 {
	var start time.Time
	var end time.Time
	for _, stage := range stages {
		if stage.StartedAt.IsZero() || stage.CompletedAt.IsZero() {
			continue
		}
		if start.IsZero() || stage.StartedAt.Before(start) {
			start = stage.StartedAt
		}
		if stage.Name == "analysis_handoff_finalize" {
			end = stage.CompletedAt
		}
	}
	if start.IsZero() || end.IsZero() {
		return 0
	}
	return end.Sub(start).Seconds() * 1000
}

// extractV2Collaboration reuses the v1 message-protocol extractors.
func extractV2Collaboration(execution eval.WorkflowCaseExecution) V2CollaborationMetrics {
	report := execution.Report
	messageHistory := report.MessageHistory
	if len(messageHistory) == 0 && execution.DurableRun != nil {
		messageHistory = execution.DurableRun.MessageHistory
	}
	artifactCoverage := meanOf(
		boolScore(execution.Result.AnalysisHandoffRecorded),
		boolScore(execution.Result.ValidationReportRecorded),
		actionPlanCoverage(report.Validation, execution.DurableRun),
		boolScore(report.Validation.PostActionValidation != nil),
		boolScore(execution.Result.EvidencePackageGenerated),
		boolScore(execution.Result.MemoryWriteback),
	)
	return V2CollaborationMetrics{
		HandoffSchemaValidRate:                handoffSchemaValidRate(messageHistory),
		HandoffParseSuccessRate:               handoffParseSuccessRate(report.Validation),
		HandoffRequiredFieldsCoverage:         handoffRequiredFieldsCoverage(report.AnalysisHandoff),
		HandoffTargetExtractionScore:          handoffTargetExtractionScore(report),
		CrossAgentInformationRetentionScore:   crossAgentInformationRetentionScore(report),
		MessageHistoryIntegrityScore:          messageHistoryIntegrityScore(messageHistory),
		AgentAgreementScore:                   agentAgreementScore(report),
		ParentChildMessageLinkageCompleteness: parentChildLinkageCompleteness(messageHistory),
		ArtifactCoverage:                      artifactCoverage,
	}
}

// aggregateV2Case aggregates one case's trials.
func aggregateV2Case(contract V2Case, trials []V2TrialMetrics, executions []eval.WorkflowCaseExecution, cfg V2ScoringConfig) V2CaseResult {
	result := V2CaseResult{
		ID:                 contract.ID,
		TaskType:           contract.TaskType,
		IncidentType:       contract.IncidentType,
		FaultDomain:        contract.FaultDomain,
		Difficulty:         contract.Difficulty,
		RobustnessCategory: contract.RobustnessCategory,
		Description:        contract.Description,
		Trials:             len(trials),
	}
	successes := make([]bool, len(trials))
	failureModeList := make([]map[string]int, 0, len(trials))
	failures := make([]string, 0)
	for i, trial := range trials {
		successes[i] = trial.TaskSuccess
		if len(trial.Failures) > 0 {
			failures = append(failures, trial.Failures...)
		}
		if len(trial.FailureModes) > 0 {
			failureModeList = append(failureModeList, trial.FailureModes)
		}
	}
	result.TaskSuccessRate, result.PassAt1, result.PassAtK = successMetrics(successes, cfg.TaskSuccess.PassAtKAttempts)
	result.PassAtKAvailable = len(trials) >= cfg.TaskSuccess.PassAtKAttempts
	result.TaskSuccessTrials = countTrue(successes)
	// The workflow API cannot accept an independent deterministic seed per
	// trial. Do not publish a trial-level Wilson interval over replayed runs.
	result.TaskSuccessCIAvailable = false
	result.TaskSuccessCIMethod = "not_estimated_runtime_trial_seed_unavailable"
	result.Flaky = result.TaskSuccessRate > 0 && result.TaskSuccessRate < 1
	result.FalsePositiveTrials = 0
	for _, trial := range trials {
		if trial.FalsePositive {
			result.FalsePositiveTrials++
		}
	}
	result.Aggregate = meanV2TrialMetrics(trials)
	result.ReplayStability = replayStabilityScore(successes)
	result.RootCauseConsistency = rootCauseConsistencyScore(executions)
	result.VerdictConsistency = verdictConsistencyScore(executions)
	result.FailureModes, result.FailureModeTrialCount, result.FatalFailureTrialCount = failureModeTrialStats(failureModeList, len(trials))
	result.Passed = result.TaskSuccessRate >= cfg.CasePassRateThreshold && result.FatalFailureTrialCount == 0
	result.Failures = dedupeStrings(failures)
	return result
}

// meanV2TrialMetrics averages the per-trial metrics of one case.
func meanV2TrialMetrics(trials []V2TrialMetrics) V2CaseAggregate {
	agg := V2CaseAggregate{MetricSupport: make(map[string]int)}
	if len(trials) == 0 {
		return agg
	}
	n := len(trials)
	sum := func(get func(V2TrialMetrics) float64) float64 {
		total := 0.0
		for _, trial := range trials {
			total += get(trial)
		}
		return total / float64(n)
	}
	measuredMean := func(metric string, get func(V2TrialMetrics) float64) float64 {
		total := 0.0
		support := 0
		for _, trial := range trials {
			if !trial.Measured[metric] {
				continue
			}
			total += get(trial)
			support++
		}
		agg.MetricSupport[metric] = support
		if support == 0 {
			return 0
		}
		return total / float64(support)
	}
	ptrMean := func(get func(V2TrialMetrics) *float64) *float64 {
		values := make([]*float64, 0, n)
		for _, trial := range trials {
			values = append(values, get(trial))
		}
		mean, support := meanPtr(values)
		if support == 0 {
			return nil
		}
		return &mean
	}

	for _, trial := range trials {
		for metric, measured := range trial.Measured {
			if measured {
				agg.MetricSupport[metric]++
			}
		}
	}

	agg.RootCauseEntityPrecision = measuredMean(v2MetricDiagnosisQuality, func(t V2TrialMetrics) float64 { return t.RootCauseEntityPrecision })
	agg.RootCauseEntityRecall = measuredMean(v2MetricDiagnosisQuality, func(t V2TrialMetrics) float64 { return t.RootCauseEntityRecall })
	agg.RootCauseEntityF1 = measuredMean(v2MetricDiagnosisQuality, func(t V2TrialMetrics) float64 { return t.RootCauseEntityF1 })
	agg.RootCauseEntityRecallAt1 = measuredMean(v2MetricDiagnosisQuality, func(t V2TrialMetrics) float64 { return t.RootCauseEntityRecallAt1 })
	agg.RootCauseEntityRecallAt3 = measuredMean(v2MetricDiagnosisQuality, func(t V2TrialMetrics) float64 { return t.RootCauseEntityRecallAt3 })
	agg.RootCauseEntityRecallAt5 = measuredMean(v2MetricDiagnosisQuality, func(t V2TrialMetrics) float64 { return t.RootCauseEntityRecallAt5 })
	agg.RootCauseReasoningScore = measuredMean(v2MetricDiagnosisQuality, func(t V2TrialMetrics) float64 { return t.RootCauseReasoningScore })
	agg.FaultLocalizationScore = measuredMean(v2MetricDiagnosisQuality, func(t V2TrialMetrics) float64 { return t.FaultLocalizationScore })
	agg.FaultDomainAccuracy = ptrMean(func(t V2TrialMetrics) *float64 { return t.FaultDomainAccuracy })
	agg.PropagationChainScore = ptrMean(func(t V2TrialMetrics) *float64 { return t.PropagationChainScore })
	agg.RemediationPlanCorrectness = ptrMean(func(t V2TrialMetrics) *float64 { return t.RemediationPlanCorrectness })
	agg.VerificationCorrectness = ptrMean(func(t V2TrialMetrics) *float64 { return t.VerificationCorrectness })
	agg.RecoverySuccess = ptrMean(func(t V2TrialMetrics) *float64 { return t.RecoverySuccess })

	agg.UnsafeActionRate = measuredMean(v2MetricSafetyGovernance, func(t V2TrialMetrics) float64 { return t.Safety.UnsafeActionRate })
	agg.PolicyViolationRate = measuredMean(v2MetricSafetyGovernance, func(t V2TrialMetrics) float64 { return t.Safety.PolicyViolationRate })
	agg.ApprovalBypassRate = measuredMean(v2MetricSafetyGovernance, func(t V2TrialMetrics) float64 { return t.Safety.ApprovalBypassRate })
	agg.ForbiddenToolCallRate = measuredMean(v2MetricSafetyGovernance, func(t V2TrialMetrics) float64 { return t.Safety.ForbiddenToolCallRate })
	agg.DestructiveActionAttemptRate = measuredMean(v2MetricSafetyGovernance, func(t V2TrialMetrics) float64 { return t.Safety.DestructiveActionAttemptRate })
	agg.ApprovalEnforcementRate = measuredMean(v2MetricApprovalEnforcement, func(t V2TrialMetrics) float64 { return t.Safety.ApprovalEnforcementRate })
	agg.DryRunComplianceRate = measuredMean(v2MetricDryRunCompliance, func(t V2TrialMetrics) float64 { return t.Safety.DryRunComplianceRate })
	agg.RollbackReadinessRate = ptrMean(func(t V2TrialMetrics) *float64 { return t.Safety.RollbackReadinessRate })
	agg.NoRegressionRate = ptrMean(func(t V2TrialMetrics) *float64 { return t.Safety.NoRegressionRate })
	agg.RegressionRate = sum(func(t V2TrialMetrics) float64 {
		return boolScore(t.Safety.RegressionCount > 0)
	})

	agg.UsefulToolCallRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.UsefulToolCallRate })
	agg.RedundantToolCallRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.RedundantToolCallRate })
	agg.FailedToolCallRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.FailedToolCallRate })
	agg.InvalidToolArgumentRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.InvalidToolArgumentRate })
	agg.RepeatedToolCallRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.RepeatedToolCallRate })
	agg.ToolSelectionPrecision = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.ToolSelectionPrecision })
	agg.ToolSelectionRecall = ptrMean(func(t V2TrialMetrics) *float64 { return t.Trajectory.ToolSelectionRecall })
	agg.ToolInformationGain = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.ToolInformationGain })
	agg.NoProgressStepRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.NoProgressStepRate })
	agg.EvidenceGainPerStep = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.EvidenceGainPerStep })
	agg.PrematureTerminationRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.PrematureTerminationRate })
	agg.LoopRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.LoopRate })
	agg.InvalidActionRate = measuredMean(v2MetricTrajectoryTool, func(t V2TrialMetrics) float64 { return t.Trajectory.InvalidActionRate })

	agg.EndToEndLatencyMS = measuredMean(v2MetricEndToEndLatency, func(t V2TrialMetrics) float64 { return t.Efficiency.EndToEndLatencyMS })
	agg.TimeToDiagnosisMS = measuredMean(v2MetricTimeToDiagnosis, func(t V2TrialMetrics) float64 { return t.Efficiency.TimeToDiagnosisMS })
	agg.TimeToMitigationMS = ptrMean(func(t V2TrialMetrics) *float64 { return t.Efficiency.TimeToMitigationMS })
	agg.ToolCallCount = measuredMean(v2MetricToolCalls, func(t V2TrialMetrics) float64 { return t.Efficiency.ToolCallCount })
	agg.TotalTokens = measuredMean(v2MetricTokens, func(t V2TrialMetrics) float64 { return t.Efficiency.TotalTokens })
	agg.EstimatedCostUSD = ptrMean(func(t V2TrialMetrics) *float64 { return t.Efficiency.EstimatedCostUSD })

	agg.HandoffSchemaValidRate = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.HandoffSchemaValidRate })
	agg.HandoffParseSuccessRate = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.HandoffParseSuccessRate })
	agg.HandoffRequiredFieldsCoverage = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.HandoffRequiredFieldsCoverage })
	agg.HandoffTargetExtractionScore = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.HandoffTargetExtractionScore })
	agg.CrossAgentInformationRetentionScore = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.CrossAgentInformationRetentionScore })
	agg.MessageHistoryIntegrityScore = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.MessageHistoryIntegrityScore })
	agg.AgentAgreementScore = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.AgentAgreementScore })
	agg.ParentChildMessageLinkageCompleteness = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.ParentChildMessageLinkageCompleteness })
	agg.ArtifactCoverage = measuredMean(v2MetricCollaboration, func(t V2TrialMetrics) float64 { return t.Collaboration.ArtifactCoverage })

	agg.FalsePositiveRate = sum(func(t V2TrialMetrics) float64 { return boolScore(t.FalsePositive) })
	agg.UnnecessaryActionRate = sum(func(t V2TrialMetrics) float64 {
		return boolScore(t.UnnecessaryActions > 0)
	})
	return agg
}

// countTrue counts true values.
func countTrue(values []bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

// aggregateV2Benchmark aggregates all case results benchmark-wide.
func aggregateV2Benchmark(cases []V2CaseResult) V2Aggregate {
	agg := V2Aggregate{
		CasesRun:      len(cases),
		MetricSupport: make(map[string]int),
	}
	if len(cases) == 0 {
		return agg
	}
	agg.TrialsPerCase = cases[0].Trials
	for _, item := range cases {
		agg.TotalTrials += item.Trials
	}

	macroMeasured := func(metric string, get func(V2CaseAggregate) float64) float64 {
		values := make([]float64, 0, len(cases))
		for _, item := range cases {
			if item.Aggregate.MetricSupport[metric] <= 0 {
				continue
			}
			values = append(values, get(item.Aggregate))
		}
		mean, support := macroMean(values)
		agg.MetricSupport[metric] = support
		return mean
	}
	macroPtr := func(get func(V2CaseAggregate) *float64) *float64 {
		values := make([]*float64, 0, len(cases))
		for _, item := range cases {
			values = append(values, get(item.Aggregate))
		}
		mean, support := meanPtr(values)
		if support == 0 {
			return nil
		}
		return &mean
	}
	agg.MetricSupport[v2MetricTaskOutcome] = len(cases)

	rateSum := 0.0
	successTrials := 0
	totalTrials := 0
	passAt1Sum := 0.0
	passAtKSum := 0.0
	passAtKAvailable := true
	flaky := 0
	for _, item := range cases {
		rateSum += item.TaskSuccessRate
		successTrials += item.TaskSuccessTrials
		totalTrials += item.Trials
		passAt1Sum += item.PassAt1
		passAtKSum += item.PassAtK
		if !item.PassAtKAvailable {
			passAtKAvailable = false
		}
		if item.Flaky {
			flaky++
		}
	}
	agg.TaskSuccessRateMacro = rateSum / float64(len(cases))
	agg.TaskSuccessRate = ratioScores(successTrials, totalTrials)
	agg.PassAt1 = passAt1Sum / float64(len(cases))
	agg.PassAtK = passAtKSum / float64(len(cases))
	agg.PassAtKAvailable = passAtKAvailable
	agg.FlakyCaseRate = ratioScores(flaky, len(cases))

	// noop-specific
	var fpRates, noopAccuracy []float64
	var unnecessary []float64
	for _, item := range cases {
		if item.TaskType != TaskTypeNoop {
			continue
		}
		fpRates = append(fpRates, item.Aggregate.FalsePositiveRate)
		noopAccuracy = append(noopAccuracy, item.TaskSuccessRate)
		unnecessary = append(unnecessary, item.Aggregate.UnnecessaryActionRate)
	}
	agg.FalsePositiveRate, _ = macroMean(fpRates)
	agg.Specificity = 1 - agg.FalsePositiveRate
	agg.NoOpAccuracy, _ = macroMean(noopAccuracy)
	agg.UnnecessaryActionRate, _ = macroMean(unnecessary)

	agg.RootCauseEntityPrecision = macroMeasured(v2MetricDiagnosisQuality, func(a V2CaseAggregate) float64 { return a.RootCauseEntityPrecision })
	agg.RootCauseEntityRecall = macroMeasured(v2MetricDiagnosisQuality, func(a V2CaseAggregate) float64 { return a.RootCauseEntityRecall })
	agg.RootCauseEntityF1 = macroMeasured(v2MetricDiagnosisQuality, func(a V2CaseAggregate) float64 { return a.RootCauseEntityF1 })
	agg.RootCauseEntityRecallAt1 = macroMeasured(v2MetricDiagnosisQuality, func(a V2CaseAggregate) float64 { return a.RootCauseEntityRecallAt1 })
	agg.RootCauseEntityRecallAt3 = macroMeasured(v2MetricDiagnosisQuality, func(a V2CaseAggregate) float64 { return a.RootCauseEntityRecallAt3 })
	agg.RootCauseEntityRecallAt5 = macroMeasured(v2MetricDiagnosisQuality, func(a V2CaseAggregate) float64 { return a.RootCauseEntityRecallAt5 })
	agg.RootCauseReasoningScore = macroMeasured(v2MetricDiagnosisQuality, func(a V2CaseAggregate) float64 { return a.RootCauseReasoningScore })
	agg.FaultLocalizationScore = macroMeasured(v2MetricDiagnosisQuality, func(a V2CaseAggregate) float64 { return a.FaultLocalizationScore })
	agg.FaultDomainAccuracy = macroPtr(func(a V2CaseAggregate) *float64 { return a.FaultDomainAccuracy })
	agg.PropagationChainScore = macroPtr(func(a V2CaseAggregate) *float64 { return a.PropagationChainScore })
	agg.RemediationPlanCorrectness = macroPtr(func(a V2CaseAggregate) *float64 { return a.RemediationPlanCorrectness })
	agg.VerificationCorrectness = macroPtr(func(a V2CaseAggregate) *float64 { return a.VerificationCorrectness })
	agg.RecoverySuccessRate = macroPtr(func(a V2CaseAggregate) *float64 { return a.RecoverySuccess })
	agg.RegressionRate = macroPtr(func(a V2CaseAggregate) *float64 {
		if a.NoRegressionRate == nil {
			return nil
		}
		return floatPtr(1 - *a.NoRegressionRate)
	})
	agg.NoRegressionRate = macroPtr(func(a V2CaseAggregate) *float64 { return a.NoRegressionRate })
	agg.RollbackSuccessRate = macroPtr(func(a V2CaseAggregate) *float64 { return a.RollbackReadinessRate })
	agg.PostActionHealthScore = macroPtr(func(a V2CaseAggregate) *float64 { return a.NoRegressionRate })

	agg.UnsafeActionRate = macroMeasured(v2MetricSafetyGovernance, func(a V2CaseAggregate) float64 { return a.UnsafeActionRate })
	agg.PolicyViolationRate = macroMeasured(v2MetricSafetyGovernance, func(a V2CaseAggregate) float64 { return a.PolicyViolationRate })
	agg.ApprovalBypassRate = macroMeasured(v2MetricSafetyGovernance, func(a V2CaseAggregate) float64 { return a.ApprovalBypassRate })
	agg.ForbiddenToolCallRate = macroMeasured(v2MetricSafetyGovernance, func(a V2CaseAggregate) float64 { return a.ForbiddenToolCallRate })
	agg.DestructiveActionAttemptRate = macroMeasured(v2MetricSafetyGovernance, func(a V2CaseAggregate) float64 { return a.DestructiveActionAttemptRate })
	agg.ApprovalEnforcementRate = macroMeasured(v2MetricApprovalEnforcement, func(a V2CaseAggregate) float64 { return a.ApprovalEnforcementRate })
	agg.DryRunComplianceRate = macroMeasured(v2MetricDryRunCompliance, func(a V2CaseAggregate) float64 { return a.DryRunComplianceRate })

	agg.UsefulToolCallRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.UsefulToolCallRate })
	agg.RedundantToolCallRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.RedundantToolCallRate })
	agg.FailedToolCallRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.FailedToolCallRate })
	agg.InvalidToolArgumentRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.InvalidToolArgumentRate })
	agg.RepeatedToolCallRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.RepeatedToolCallRate })
	agg.ToolSelectionPrecision = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.ToolSelectionPrecision })
	agg.ToolSelectionRecall = macroPtr(func(a V2CaseAggregate) *float64 { return a.ToolSelectionRecall })
	agg.ToolInformationGain = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.ToolInformationGain })
	agg.NoProgressStepRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.NoProgressStepRate })
	agg.EvidenceGainPerStep = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.EvidenceGainPerStep })
	agg.PrematureTerminationRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.PrematureTerminationRate })
	agg.LoopRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.LoopRate })
	agg.InvalidActionRate = macroMeasured(v2MetricTrajectoryTool, func(a V2CaseAggregate) float64 { return a.InvalidActionRate })

	var latencies, ttDs []float64
	for _, item := range cases {
		if item.Aggregate.MetricSupport[v2MetricEndToEndLatency] > 0 {
			latencies = append(latencies, item.Aggregate.EndToEndLatencyMS)
		}
		if item.Aggregate.MetricSupport[v2MetricTimeToDiagnosis] > 0 {
			ttDs = append(ttDs, item.Aggregate.TimeToDiagnosisMS)
		}
	}
	agg.MetricSupport[v2MetricEndToEndLatency] = len(latencies)
	agg.MetricSupport[v2MetricTimeToDiagnosis] = len(ttDs)
	agg.LatencyP50MS, agg.LatencyP95MS, agg.LatencyP99MS = latencyPercentiles(latencies)
	agg.TimeToDiagnosisP50MS, agg.TimeToDiagnosisP95MS, _ = latencyPercentiles(ttDs)
	ttm := make([]float64, 0, len(cases))
	for _, item := range cases {
		if item.Aggregate.TimeToMitigationMS != nil {
			ttm = append(ttm, *item.Aggregate.TimeToMitigationMS)
		}
	}
	agg.MetricSupport[v2MetricTimeToMitigation] = len(ttm)
	if len(ttm) > 0 {
		ttmP50 := percentile(ttm, 50)
		agg.TimeToMitigationP50MS = &ttmP50
	}
	agg.MeanToolCalls = macroMeasured(v2MetricToolCalls, func(a V2CaseAggregate) float64 { return a.ToolCallCount })
	agg.MeanTokens = macroMeasured(v2MetricTokens, func(a V2CaseAggregate) float64 { return a.TotalTokens })
	agg.EstimatedCostUSD = macroPtr(func(a V2CaseAggregate) *float64 { return a.EstimatedCostUSD })
	for _, item := range cases {
		if item.Aggregate.MetricSupport[v2MetricEfficiency] > 0 {
			agg.MetricSupport[v2MetricEfficiency]++
		}
	}

	// Repeated-run stability remains useful as a descriptive diagnostic even
	// when runs are not independently seeded. Only independently seeded cases
	// contribute support to the weighted reliability dimension.
	stabilitySum := 0.0
	rootSum := 0.0
	verdicts := make([]*float64, 0, len(cases))
	for _, item := range cases {
		if item.Trials < 2 {
			continue
		}
		stabilitySum += item.ReplayStability
		rootSum += item.RootCauseConsistency
		verdicts = append(verdicts, item.VerdictConsistency)
		agg.MetricSupport[v2MetricReplayDescriptive]++
		if item.TrialsIndependent {
			agg.MetricSupport[v2MetricReliability]++
		}
	}
	if support := agg.MetricSupport[v2MetricReplayDescriptive]; support > 0 {
		agg.ReplayStability = stabilitySum / float64(support)
		agg.RootCauseConsistency = rootSum / float64(support)
	}
	agg.VerdictConsistency = meanPtrOnly(verdicts)

	agg.HandoffSchemaValidRate = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.HandoffSchemaValidRate })
	agg.HandoffParseSuccessRate = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.HandoffParseSuccessRate })
	agg.HandoffRequiredFieldsCoverage = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.HandoffRequiredFieldsCoverage })
	agg.HandoffTargetExtractionScore = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.HandoffTargetExtractionScore })
	agg.CrossAgentInformationRetentionScore = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.CrossAgentInformationRetentionScore })
	agg.MessageHistoryIntegrityScore = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.MessageHistoryIntegrityScore })
	agg.AgentAgreementScore = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.AgentAgreementScore })
	agg.ParentChildMessageLinkageCompleteness = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.ParentChildMessageLinkageCompleteness })
	agg.ArtifactCoverage = macroMeasured(v2MetricCollaboration, func(a V2CaseAggregate) float64 { return a.ArtifactCoverage })
	return agg
}

// meanPtrOnly averages non-nil pointer values.
func meanPtrOnly(values []*float64) *float64 {
	mean, support := meanPtr(values)
	if support == 0 {
		return nil
	}
	return &mean
}

// benchmarkFailureModes aggregates failure modes across all cases.
func benchmarkFailureModes(cases []V2CaseResult) (map[string]int, float64, float64) {
	aggregate := make(map[string]int)
	trialsWithModes := 0
	fatalTrials := 0
	totalTrials := 0
	for _, item := range cases {
		totalTrials += item.Trials
		trialsWithModes += item.FailureModeTrialCount
		fatalTrials += item.FatalFailureTrialCount
		for mode, count := range item.FailureModes {
			aggregate[mode] += count
		}
	}
	if totalTrials == 0 {
		return aggregate, 0, 0
	}
	return aggregate, ratioScores(trialsWithModes, totalTrials), ratioScores(fatalTrials, totalTrials)
}

// computeV2Categories builds macro/micro statistics per category dimension.
func computeV2Categories(cases []V2CaseResult) []V2CategoryStats {
	dimensions := []struct {
		name string
		get  func(V2CaseResult) string
	}{
		{"by_task_type", func(c V2CaseResult) string { return string(c.TaskType) }},
		{"by_incident_type", func(c V2CaseResult) string { return c.IncidentType }},
		{"by_fault_domain", func(c V2CaseResult) string { return c.FaultDomain }},
		{"by_difficulty", func(c V2CaseResult) string { return c.Difficulty }},
		{"by_robustness_category", func(c V2CaseResult) string { return c.RobustnessCategory }},
	}
	var stats []V2CategoryStats
	for _, dimension := range dimensions {
		groups := make(map[string][]V2CaseResult)
		for _, item := range cases {
			key := strings.TrimSpace(dimension.get(item))
			if key == "" {
				key = "unspecified"
			}
			groups[key] = append(groups[key], item)
		}
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			group := groups[key]
			macro := map[string]float64{
				"task_success_rate": meanCaseRate(group),
			}
			diagnosisGroup := filterV2CasesByMetric(group, v2MetricDiagnosisQuality)
			if len(diagnosisGroup) > 0 {
				macro["root_cause_entity_f1"] = meanCaseAggregate(diagnosisGroup, func(a V2CaseAggregate) float64 { return a.RootCauseEntityF1 })
				macro["root_cause_entity_recall"] = meanCaseAggregate(diagnosisGroup, func(a V2CaseAggregate) float64 { return a.RootCauseEntityRecall })
				macro["root_cause_entity_precision"] = meanCaseAggregate(diagnosisGroup, func(a V2CaseAggregate) float64 { return a.RootCauseEntityPrecision })
				macro["root_cause_reasoning_score"] = meanCaseAggregate(diagnosisGroup, func(a V2CaseAggregate) float64 { return a.RootCauseReasoningScore })
			}
			if safetyGroup := filterV2CasesByMetric(group, v2MetricSafetyGovernance); len(safetyGroup) > 0 {
				macro["unsafe_action_rate"] = meanCaseAggregate(safetyGroup, func(a V2CaseAggregate) float64 { return a.UnsafeActionRate })
			}
			if toolGroup := filterV2CasesByMetric(group, v2MetricToolCalls); len(toolGroup) > 0 {
				macro["mean_tool_calls"] = meanCaseAggregate(toolGroup, func(a V2CaseAggregate) float64 { return a.ToolCallCount })
			}
			if tokenGroup := filterV2CasesByMetric(group, v2MetricTokens); len(tokenGroup) > 0 {
				macro["mean_tokens"] = meanCaseAggregate(tokenGroup, func(a V2CaseAggregate) float64 { return a.TotalTokens })
			}
			stats = append(stats, V2CategoryStats{
				Key:     dimension.name + ":" + key,
				Support: len(group),
				Macro:   macro,
				Micro: map[string]float64{
					"task_success_rate": pooledCaseRate(group),
				},
			})
		}
	}
	return stats
}

func filterV2CasesByMetric(cases []V2CaseResult, metric string) []V2CaseResult {
	filtered := make([]V2CaseResult, 0, len(cases))
	for _, item := range cases {
		if item.Aggregate.MetricSupport[metric] > 0 {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func meanCaseRate(group []V2CaseResult) float64 {
	if len(group) == 0 {
		return 0
	}
	sum := 0.0
	for _, item := range group {
		sum += item.TaskSuccessRate
	}
	return sum / float64(len(group))
}

func pooledCaseRate(group []V2CaseResult) float64 {
	successes := 0
	trials := 0
	for _, item := range group {
		successes += int(item.TaskSuccessRate*float64(item.Trials) + 0.5)
		trials += item.Trials
	}
	return ratioScores(successes, trials)
}

func meanCaseAggregate(group []V2CaseResult, get func(V2CaseAggregate) float64) float64 {
	if len(group) == 0 {
		return 0
	}
	values := make([]float64, 0, len(group))
	for _, item := range group {
		values = append(values, get(item.Aggregate))
	}
	mean, _ := macroMean(values)
	return mean
}

// computeV2ConfidenceIntervals fills the CI table using case-level bootstrap.
// Repeated runtime trials are summarized descriptively but never counted as
// independent samples because WorkflowCaseRunOptions has no trial-seed input.
func computeV2ConfidenceIntervals(cases []V2CaseResult, agg V2Aggregate, scorecard V2Scorecard, cfg V2ScoringConfig, seed int64) V2ConfidenceIntervals {
	meanCI := func(values []float64, point float64) V2CI {
		if len(values) == 0 {
			return unavailableV2CI("metric not measured on any case")
		}
		lo, _, hi := bootstrapCI(values, seed, 2000)
		return V2CI{Available: true, Low: lo, Mean: point, High: hi, Method: "case_bootstrap(2000, seeded)", Support: len(values)}
	}
	metricValues := func(metric string, get func(V2CaseResult) float64) []float64 {
		values := make([]float64, 0, len(cases))
		for _, item := range cases {
			if item.Aggregate.MetricSupport[metric] <= 0 {
				continue
			}
			values = append(values, get(item))
		}
		return values
	}

	ci := V2ConfidenceIntervals{}
	taskRates := make([]float64, 0, len(cases))
	passAt1 := make([]float64, 0, len(cases))
	falsePositiveRates := make([]float64, 0)
	for _, item := range cases {
		taskRates = append(taskRates, item.TaskSuccessRate)
		passAt1 = append(passAt1, item.PassAt1)
		if item.TaskType == TaskTypeNoop {
			falsePositiveRates = append(falsePositiveRates, item.Aggregate.FalsePositiveRate)
		}
	}
	ci.TaskSuccessRate = meanCI(taskRates, agg.TaskSuccessRateMacro)
	ci.PassAt1 = meanCI(passAt1, agg.PassAt1)
	ci.FalsePositiveRate = meanCI(falsePositiveRates, agg.FalsePositiveRate)
	ci.RootCauseEntityF1 = meanCI(metricValues(v2MetricDiagnosisQuality, func(c V2CaseResult) float64 { return c.Aggregate.RootCauseEntityF1 }), agg.RootCauseEntityF1)
	ci.RootCauseEntityRecall = meanCI(metricValues(v2MetricDiagnosisQuality, func(c V2CaseResult) float64 { return c.Aggregate.RootCauseEntityRecall }), agg.RootCauseEntityRecall)
	ci.RootCauseReasoning = meanCI(metricValues(v2MetricDiagnosisQuality, func(c V2CaseResult) float64 { return c.Aggregate.RootCauseReasoningScore }), agg.RootCauseReasoningScore)

	latencies := metricValues(v2MetricEndToEndLatency, func(c V2CaseResult) float64 { return c.Aggregate.EndToEndLatencyMS })
	if len(latencies) == 0 {
		ci.LatencyP95MS = unavailableV2CI("end-to-end latency not measured on any case")
	} else {
		lo, point, hi := statisticBootstrapCI(latencies, seed, 2000, func(values []float64) float64 { return percentile(values, 95) })
		ci.LatencyP95MS = V2CI{Available: true, Low: lo, Mean: point, High: hi, Method: "case_bootstrap_p95(2000, seeded)", Support: len(latencies)}
	}
	ci.MeanToolCalls = meanCI(metricValues(v2MetricToolCalls, func(c V2CaseResult) float64 { return c.Aggregate.ToolCallCount }), agg.MeanToolCalls)
	ci.MeanTokens = meanCI(metricValues(v2MetricTokens, func(c V2CaseResult) float64 { return c.Aggregate.TotalTokens }), agg.MeanTokens)
	ci.OverallScore = bootstrapV2OverallScoreCI(cases, scorecard.OverallScore, cfg, seed)
	return ci
}

func unavailableV2CI(reason string) V2CI {
	return V2CI{Available: false, Method: "not_estimated", Limitation: reason}
}

// bootstrapV2OverallScoreCI resamples cases and recomputes the complete
// aggregate + scorecard for every sample, preserving missingness, weighting,
// and the outcome cap. This is not a mean of unrelated per-case scores.
func bootstrapV2OverallScoreCI(cases []V2CaseResult, point float64, cfg V2ScoringConfig, seed int64) V2CI {
	if len(cases) == 0 {
		return unavailableV2CI("no cases")
	}
	indices := make([]float64, len(cases))
	for i := range indices {
		indices[i] = float64(i)
	}
	lo, _, hi := statisticBootstrapCI(indices, seed, 2000, func(sample []float64) float64 {
		selected := make([]V2CaseResult, len(sample))
		for i, rawIndex := range sample {
			selected[i] = cases[int(rawIndex)]
		}
		return computeV2Scorecard(aggregateV2Benchmark(selected), cfg).OverallScore
	})
	return V2CI{Available: true, Low: lo, Mean: point, High: hi, Method: "case_bootstrap_scorecard(2000, seeded)", Support: len(cases)}
}

// loadV2Cases loads and filters the evaluation v2 case file.
func loadV2Cases(repoRoot string, scope eval.Scope, selectedIDs []string) ([]V2Case, error) {
	path := filepath.Join(repoRoot, "eval_data", "system_perf_cases_v2.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file V2CaseFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(file.SchemaVersion) == "" {
		return nil, fmt.Errorf("%s missing schema_version", path)
	}
	selected := make(map[string]struct{}, len(selectedIDs))
	for _, item := range selectedIDs {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			selected[trimmed] = struct{}{}
		}
	}
	out := make([]V2Case, 0, len(file.Cases))
	for _, item := range file.Cases {
		if len(selected) > 0 {
			if _, ok := selected[item.ID]; !ok {
				continue
			}
		}
		if systemPerformanceSuiteAllowed(item.Suites, scope) {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no system performance v2 cases matched scope %s", scope)
	}
	return out, nil
}
