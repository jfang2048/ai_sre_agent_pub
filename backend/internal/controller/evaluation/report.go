package evaluation

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Evaluation v2 report persistence: report.json, summary.md, metrics.csv,
// cases.csv, failure_modes.csv and figures rendered by the standalone Python
// reporter (scripts/evaluation/render_report.py).

// persistV2Report writes the run artifacts into
// data/eval/system_performance/<run-id>/ and updates data/eval/reports/latest
// to point at the newest run.
func persistV2Report(repoRoot string, report SystemPerformanceReportV2, reportDirOverride string) (string, error) {
	runID := report.GeneratedAt.Format("20060102T150405Z")
	var reportDir string
	if strings.TrimSpace(reportDirOverride) != "" {
		reportDir = reportDirOverride
	} else {
		reportDir = filepath.Join(repoRoot, "data", "eval", "system_performance", runID)
	}
	if err := os.MkdirAll(filepath.Join(reportDir, "figures"), 0o755); err != nil {
		return "", err
	}

	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(reportDir, "report.json"), raw, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(reportDir, "summary.md"), []byte(renderV2Summary(report)), 0o644); err != nil {
		return "", err
	}
	if err := writeV2MetricsCSV(filepath.Join(reportDir, "metrics.csv"), report); err != nil {
		return "", err
	}
	if err := writeV2CasesCSV(filepath.Join(reportDir, "cases.csv"), report); err != nil {
		return "", err
	}
	if err := writeV2FailureModesCSV(filepath.Join(reportDir, "failure_modes.csv"), report); err != nil {
		return "", err
	}

	// update the "latest" pointer
	latestRoot := filepath.Join(repoRoot, "data", "eval", "reports")
	if err := os.MkdirAll(latestRoot, 0o755); err != nil {
		return "", err
	}
	latestFile := filepath.Join(latestRoot, "latest.txt")
	if err := os.WriteFile(latestFile, []byte(reportDir+"\n"), 0o644); err != nil {
		return "", err
	}

	renderV2Figures(repoRoot, reportDir)
	return reportDir, nil
}

// renderV2Figures invokes the standalone Python matplotlib reporter when
// available. Figure rendering is best-effort: a missing matplotlib must not
// fail the benchmark itself.
func renderV2Figures(repoRoot, reportDir string) {
	script := filepath.Join(repoRoot, "scripts", "evaluation", "render_report.py")
	if _, err := os.Stat(script); err != nil {
		fmt.Fprintf(os.Stderr, "evaluation: figure renderer not found at %s; skipping figures\n", script)
		return
	}
	cmd := exec.Command("python3", script, filepath.Join(reportDir, "report.json"), filepath.Join(reportDir, "figures"))
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "evaluation: figure rendering failed (%v): %s\n", err, strings.TrimSpace(string(output)))
		return
	}
}

// RenderV2Text prints a compact console view of the v2 report.
func RenderV2Text(report SystemPerformanceReportV2) string {
	agg := report.Aggregate
	rate := func(value float64) string { return fmt.Sprintf("%.1f%%", value*100) }
	measuredDimensions := make(map[string]bool, len(report.Scorecard.Measured))
	for _, dimension := range report.Scorecard.Measured {
		measuredDimensions[dimension] = true
	}
	score := func(dimension string, value float64) string {
		if !measuredDimensions[dimension] {
			return "N/A"
		}
		return fmt.Sprintf("%.3f", value)
	}
	supportedRate := func(metric string, value float64) string {
		if agg.MetricSupport[metric] <= 0 {
			return "N/A"
		}
		return rate(value)
	}
	supportedNumber := func(metric string, value float64, format string) string {
		if agg.MetricSupport[metric] <= 0 {
			return "N/A"
		}
		return fmt.Sprintf(format, value)
	}
	optRate := func(value *float64) string {
		if value == nil {
			return "N/A"
		}
		return rate(*value)
	}
	passAtK := "N/A (insufficient trials)"
	if agg.PassAtKAvailable {
		passAtK = rate(agg.PassAtK)
	}
	lines := []string{
		fmt.Sprintf("system_perf_v2: scope=%s runtime=%s variant=%s verdict=%s overall=%.3f generated_at=%s",
			report.Environment.Scope,
			report.Environment.RuntimeMode,
			firstNonEmpty(strings.TrimSpace(report.Environment.Variant), "default"),
			report.Verdict,
			report.Scorecard.OverallScore,
			report.GeneratedAt.Format("2006-01-02T15:04:05Z"),
		),
		fmt.Sprintf("task_success=%s (macro) pass@1=%s pass@k=%s", rate(agg.TaskSuccessRateMacro), rate(agg.PassAt1), passAtK),
		fmt.Sprintf("rca_entity: precision=%s recall=%s f1=%s recall@1=%s reasoning=%s",
			supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityPrecision), supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityRecall), supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityF1),
			supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityRecallAt1), supportedRate(v2MetricDiagnosisQuality, agg.RootCauseReasoningScore)),
		fmt.Sprintf("safety: unsafe=%s bypass=%s destructive=%s dry_run=%s no_regression=%s",
			supportedRate(v2MetricSafetyGovernance, agg.UnsafeActionRate), supportedRate(v2MetricSafetyGovernance, agg.ApprovalBypassRate), supportedRate(v2MetricSafetyGovernance, agg.DestructiveActionAttemptRate),
			supportedRate(v2MetricDryRunCompliance, agg.DryRunComplianceRate), optRate(agg.NoRegressionRate)),
		fmt.Sprintf("noop: false_positive=%s no_op_accuracy=%s unnecessary_actions=%s",
			rate(agg.FalsePositiveRate), rate(agg.NoOpAccuracy), rate(agg.UnnecessaryActionRate)),
		fmt.Sprintf("trajectory: useful=%s redundant=%s failed=%s info_gain=%s no_progress=%s premature=%s",
			supportedRate(v2MetricTrajectoryTool, agg.UsefulToolCallRate), supportedRate(v2MetricTrajectoryTool, agg.RedundantToolCallRate), supportedRate(v2MetricTrajectoryTool, agg.FailedToolCallRate),
			supportedRate(v2MetricTrajectoryTool, agg.ToolInformationGain), supportedRate(v2MetricTrajectoryTool, agg.NoProgressStepRate), supportedRate(v2MetricTrajectoryTool, agg.PrematureTerminationRate)),
		fmt.Sprintf("efficiency: p50=%s p95=%s p99=%s ttd_p50=%s tool_calls=%s tokens=%s",
			supportedNumber(v2MetricEndToEndLatency, agg.LatencyP50MS, "%.1fms"), supportedNumber(v2MetricEndToEndLatency, agg.LatencyP95MS, "%.1fms"), supportedNumber(v2MetricEndToEndLatency, agg.LatencyP99MS, "%.1fms"), supportedNumber(v2MetricTimeToDiagnosis, agg.TimeToDiagnosisP50MS, "%.1fms"), supportedNumber(v2MetricToolCalls, agg.MeanToolCalls, "%.1f"), supportedNumber(v2MetricTokens, agg.MeanTokens, "%.0f")),
		fmt.Sprintf("reliability (descriptive replay): stability=%s flaky=%s failure_mode=%s fatal=%s",
			supportedRate(v2MetricReplayDescriptive, agg.ReplayStability), supportedRate(v2MetricReplayDescriptive, agg.FlakyCaseRate), rate(agg.FailureModeRate), rate(agg.FatalFailureRate)),
		fmt.Sprintf("scorecard: outcome=%s diagnosis=%s safety=%s trajectory=%s efficiency=%s reliability=%s collaboration=%s",
			score(v2MetricTaskOutcome, report.Scorecard.TaskOutcome),
			score(v2MetricDiagnosisQuality, report.Scorecard.DiagnosisQuality),
			score(v2MetricSafetyGovernance, report.Scorecard.SafetyGovernance),
			score(v2MetricTrajectoryTool, report.Scorecard.TrajectoryTool),
			score(v2MetricEfficiency, report.Scorecard.Efficiency),
			score(v2MetricReliability, report.Scorecard.Reliability),
			score(v2MetricCollaboration, report.Scorecard.CollaborationArtifacts)),
	}
	if len(report.Gates.Fired) > 0 {
		lines = append(lines, "gates fired: "+strings.Join(report.Gates.Fired, ", "))
	}
	if report.Comparison != nil {
		lines = append(lines, fmt.Sprintf("compare: baseline=%s verdict=%s", report.Comparison.BaselinePath, report.Comparison.Verdict))
		for _, row := range report.Comparison.Rows {
			rel := ""
			if row.RelativeDeltaPct != nil {
				rel = fmt.Sprintf(" (%+.1f%%)", *row.RelativeDeltaPct)
			}
			lines = append(lines, fmt.Sprintf("  %-30s baseline=%.3f current=%.3f delta=%+.3f%s", row.Metric, row.Baseline, row.Current, row.AbsoluteDelta, rel))
		}
	}
	lines = append(lines, fmt.Sprintf("report_dir: %s", report.ReportDir))
	return strings.Join(lines, "\n")
}

// RenderV2Table prints the headline metrics as a fixed-width two-column table.
func RenderV2Table(report SystemPerformanceReportV2) string {
	agg := report.Aggregate
	rate := func(value float64) string { return fmt.Sprintf("%.1f%%", value*100) }
	supportedRate := func(metric string, value float64) string {
		if agg.MetricSupport[metric] <= 0 {
			return "N/A"
		}
		return rate(value)
	}
	supportedNumber := func(metric string, value float64, format string) string {
		if agg.MetricSupport[metric] <= 0 {
			return "N/A"
		}
		return fmt.Sprintf(format, value)
	}
	optRate := func(value *float64) string {
		if value == nil {
			return "N/A"
		}
		return rate(*value)
	}
	passAtK := "N/A (insufficient trials)"
	if agg.PassAtKAvailable {
		passAtK = rate(agg.PassAtK)
	}
	rows := [][2]string{
		{"Verdict", report.Verdict},
		{"Overall Score", fmt.Sprintf("%.1f / 100", report.Scorecard.OverallScore*100)},
		{"", ""},
		{"Task Success (macro)", rate(agg.TaskSuccessRateMacro)},
		{"Task Success 95% CI", fmt.Sprintf("%s .. %s", rate(report.ConfidenceIntervals.TaskSuccessRate.Low), rate(report.ConfidenceIntervals.TaskSuccessRate.High))},
		{"Pass@1", rate(agg.PassAt1)},
		{"Pass@K", passAtK},
		{"", ""},
		{"RCA Entity Precision", supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityPrecision)},
		{"RCA Entity Recall", supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityRecall)},
		{"RCA Entity F1", supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityF1)},
		{"RCA Recall@1", supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityRecallAt1)},
		{"RCA Reasoning", supportedRate(v2MetricDiagnosisQuality, agg.RootCauseReasoningScore)},
		{"Fault Domain Accuracy", optRate(agg.FaultDomainAccuracy)},
		{"Propagation Chain", optRate(agg.PropagationChainScore)},
		{"", ""},
		{"Remediation Plan", optRate(agg.RemediationPlanCorrectness)},
		{"Recovery Success", optRate(agg.RecoverySuccessRate)},
		{"No-Regression", optRate(agg.NoRegressionRate)},
		{"", ""},
		{"Unsafe Action Rate", supportedRate(v2MetricSafetyGovernance, agg.UnsafeActionRate)},
		{"Approval Bypass Rate", supportedRate(v2MetricSafetyGovernance, agg.ApprovalBypassRate)},
		{"Destructive Attempts", supportedRate(v2MetricSafetyGovernance, agg.DestructiveActionAttemptRate)},
		{"Dry-Run Compliance", supportedRate(v2MetricDryRunCompliance, agg.DryRunComplianceRate)},
		{"False Positive Rate", rate(agg.FalsePositiveRate)},
		{"No-op Accuracy", rate(agg.NoOpAccuracy)},
		{"", ""},
		{"Useful Tool Calls", supportedRate(v2MetricTrajectoryTool, agg.UsefulToolCallRate)},
		{"Redundant Tool Calls", supportedRate(v2MetricTrajectoryTool, agg.RedundantToolCallRate)},
		{"Tool Information Gain", supportedRate(v2MetricTrajectoryTool, agg.ToolInformationGain)},
		{"No-Progress Steps", supportedRate(v2MetricTrajectoryTool, agg.NoProgressStepRate)},
		{"", ""},
		{"P50 Time to Diagnosis", supportedNumber(v2MetricTimeToDiagnosis, agg.TimeToDiagnosisP50MS, "%.1f ms (synthetic)")},
		{"P95 Time to Diagnosis", supportedNumber(v2MetricTimeToDiagnosis, agg.TimeToDiagnosisP95MS, "%.1f ms (synthetic)")},
		{"Avg Tool Calls", supportedNumber(v2MetricToolCalls, agg.MeanToolCalls, "%.1f")},
		{"Avg Tokens", supportedNumber(v2MetricTokens, agg.MeanTokens, "%.0f")},
		{"", ""},
		{"Replay Stability (descriptive)", supportedRate(v2MetricReplayDescriptive, agg.ReplayStability)},
		{"Flaky Case Rate (descriptive)", supportedRate(v2MetricReplayDescriptive, agg.FlakyCaseRate)},
		{"Failure Mode Rate", rate(agg.FailureModeRate)},
	}
	width := 0
	for _, row := range rows {
		if len(row[0]) > width {
			width = len(row[0])
		}
	}
	var lines []string
	lines = append(lines, "AI SRE Agent Evaluation", strings.Repeat("-", width+24))
	for _, row := range rows {
		if row[0] == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, fmt.Sprintf("%-*s  %s", width, row[0], row[1]))
	}
	if report.Comparison != nil {
		lines = append(lines, "", fmt.Sprintf("vs baseline (%s):", filepath.Base(report.Comparison.BaselinePath)))
		for _, row := range report.Comparison.Rows {
			rel := ""
			if row.RelativeDeltaPct != nil {
				rel = fmt.Sprintf(" (%+.1f%%)", *row.RelativeDeltaPct)
			}
			lines = append(lines, fmt.Sprintf("%-*s  %+.3f%s", width, row.Metric, row.AbsoluteDelta, rel))
		}
		lines = append(lines, fmt.Sprintf("%-*s  %s", width, "Regression verdict", report.Comparison.Verdict))
		if len(report.Comparison.FiredRules) > 0 {
			for _, rule := range report.Comparison.FiredRules {
				lines = append(lines, fmt.Sprintf("  - %s", rule))
			}
		}
	}
	if len(report.Gates.Fired) > 0 {
		lines = append(lines, "", fmt.Sprintf("%-*s  %s", width, "Gates fired", strings.Join(report.Gates.Fired, ", ")))
	}
	lines = append(lines, fmt.Sprintf("\nReport: %s", report.ReportDir))
	return strings.Join(lines, "\n")
}

// renderV2Summary writes the operator-facing first screen of the report: it
// must answer "is the agent actually good?" without scrolling.
func renderV2Summary(report SystemPerformanceReportV2) string {
	agg := report.Aggregate
	scorecard := report.Scorecard
	ci := report.ConfidenceIntervals

	rate := func(value float64) string {
		return fmt.Sprintf("%.1f%%", value*100)
	}
	supportedRate := func(metric string, value float64) string {
		if agg.MetricSupport[metric] <= 0 {
			return "N/A"
		}
		return rate(value)
	}
	supportedNumber := func(metric string, value float64, format string) string {
		if agg.MetricSupport[metric] <= 0 {
			return "N/A"
		}
		return fmt.Sprintf(format, value)
	}
	optRate := func(value *float64) string {
		if value == nil {
			return "N/A"
		}
		return rate(*value)
	}
	passAtK := "N/A (insufficient trials)"
	if agg.PassAtKAvailable {
		passAtK = rate(agg.PassAtK)
	}
	var lines []string
	lines = append(lines, "# Evaluation Summary", "")
	lines = append(lines, fmt.Sprintf("Schema: %s", report.SchemaVersion))
	lines = append(lines, fmt.Sprintf("Runtime mode: %s | Variant: %s | Git: %s", report.Environment.RuntimeMode, firstNonEmpty(report.Environment.Variant, "default"), report.Environment.GitCommit))
	lines = append(lines, fmt.Sprintf("Scope: %s | Seed: %d | Generated: %s", report.Environment.Scope, report.Environment.Seed, report.GeneratedAt.Format(time.RFC3339)))
	lines = append(lines, "", fmt.Sprintf("Cases: %d", agg.CasesRun))
	lines = append(lines, fmt.Sprintf("Trials per case: %d", agg.TrialsPerCase))
	lines = append(lines, "", "| Metric | Value |")
	lines = append(lines, "|---|---|")
	lines = append(lines, fmt.Sprintf("| Task Success Rate | %s [95%% CI %s..%s] |", rate(agg.TaskSuccessRateMacro), rate(ci.TaskSuccessRate.Low), rate(ci.TaskSuccessRate.High)))
	lines = append(lines, fmt.Sprintf("| Pass@1 / Pass@%d | %s / %s |", report.Config.TaskSuccess.PassAtKAttempts, rate(agg.PassAt1), passAtK))
	lines = append(lines, fmt.Sprintf("| RCA Entity Recall / Precision / F1 | %s / %s / %s |", supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityRecall), supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityPrecision), supportedRate(v2MetricDiagnosisQuality, agg.RootCauseEntityF1)))
	lines = append(lines, fmt.Sprintf("| RCA Reasoning Score | %s |", supportedRate(v2MetricDiagnosisQuality, agg.RootCauseReasoningScore)))
	if agg.PropagationChainScore != nil {
		lines = append(lines, fmt.Sprintf("| Propagation Chain Score | %s |", rate(*agg.PropagationChainScore)))
	}
	if agg.FaultDomainAccuracy != nil {
		lines = append(lines, fmt.Sprintf("| Fault Domain Accuracy | %s |", rate(*agg.FaultDomainAccuracy)))
	}
	lines = append(lines, fmt.Sprintf("| Remediation Plan Correctness | %s |", optRate(agg.RemediationPlanCorrectness)))
	lines = append(lines, fmt.Sprintf("| Recovery Success | %s |", optRate(agg.RecoverySuccessRate)))
	lines = append(lines, fmt.Sprintf("| No-Regression Rate | %s |", optRate(agg.NoRegressionRate)))
	lines = append(lines, fmt.Sprintf("| Unsafe Action Rate | %s |", supportedRate(v2MetricSafetyGovernance, agg.UnsafeActionRate)))
	lines = append(lines, fmt.Sprintf("| Approval Bypass Rate | %s |", supportedRate(v2MetricSafetyGovernance, agg.ApprovalBypassRate)))
	lines = append(lines, fmt.Sprintf("| False Positive Rate (noop) | %s |", rate(agg.FalsePositiveRate)))
	lines = append(lines, fmt.Sprintf("| No-op Accuracy | %s |", rate(agg.NoOpAccuracy)))
	lines = append(lines, "", fmt.Sprintf("P50 Time to Diagnosis: %s", supportedNumber(v2MetricTimeToDiagnosis, agg.TimeToDiagnosisP50MS, "%.1f ms (synthetic)")))
	lines = append(lines, fmt.Sprintf("P95 Time to Diagnosis: %s", supportedNumber(v2MetricTimeToDiagnosis, agg.TimeToDiagnosisP95MS, "%.1f ms (synthetic)")))
	lines = append(lines, fmt.Sprintf("Average Tool Calls: %s", supportedNumber(v2MetricToolCalls, agg.MeanToolCalls, "%.1f")))
	lines = append(lines, fmt.Sprintf("Average Tokens: %s", supportedNumber(v2MetricTokens, agg.MeanTokens, "%.0f")))
	if agg.EstimatedCostUSD != nil {
		lines = append(lines, fmt.Sprintf("Estimated Cost: $%.4f", *agg.EstimatedCostUSD))
	} else {
		lines = append(lines, "Estimated Cost: N/A (no model pricing configured)")
	}
	lines = append(lines, "", fmt.Sprintf("Replay Stability (descriptive): %s | Flaky Cases: %s", supportedRate(v2MetricReplayDescriptive, agg.ReplayStability), supportedRate(v2MetricReplayDescriptive, agg.FlakyCaseRate)))
	lines = append(lines, fmt.Sprintf("Failure Mode Rate: %s | Fatal Failure Rate: %s", rate(agg.FailureModeRate), rate(agg.FatalFailureRate)))
	lines = append(lines, "", fmt.Sprintf("Overall Score: %.1f / 100", scorecard.OverallScore*100))
	lines = append(lines, fmt.Sprintf("Verdict: %s", report.Verdict))

	if len(report.Gates.Fired) > 0 {
		lines = append(lines, "", "Hard gates fired:", "```")
		for _, gate := range report.Gates.Fired {
			lines = append(lines, "- "+gate)
		}
		lines = append(lines, "```")
	}

	if report.Comparison != nil {
		lines = append(lines, "", "Baseline comparison:", "```")
		lines = append(lines, fmt.Sprintf("baseline: %s (generated %s)", report.Comparison.BaselinePath, report.Comparison.BaselineGeneratedAt.Format(time.RFC3339)))
		for _, row := range report.Comparison.Rows {
			rel := ""
			if row.RelativeDeltaPct != nil {
				rel = fmt.Sprintf(" (%+.1f%%)", *row.RelativeDeltaPct)
			}
			lines = append(lines, fmt.Sprintf("%-34s %+.3f%s", row.Metric, row.AbsoluteDelta, rel))
		}
		lines = append(lines, fmt.Sprintf("verdict: %s", report.Comparison.Verdict))
		if len(report.Comparison.FiredRules) > 0 {
			for _, rule := range report.Comparison.FiredRules {
				lines = append(lines, "- "+rule)
			}
		}
		lines = append(lines, "```")
	}

	// worst failures
	failed := worstCases(report)
	if len(failed) > 0 {
		lines = append(lines, "", "Worst cases:")
		for _, item := range failed {
			lines = append(lines, fmt.Sprintf("- %s (%s/%s): success %.0f%% — %s", item.ID, item.TaskType, item.IncidentType, item.TaskSuccessRate*100, firstFailure(item)))
		}
	}
	// worst incident categories
	if categories := worstCategories(report); len(categories) > 0 {
		lines = append(lines, "", "Worst incident categories:")
		for _, item := range categories {
			lines = append(lines, fmt.Sprintf("- %s: success %.0f%% (%d cases)", item.name, item.rate*100, item.support))
		}
	}
	// top failure modes
	if len(report.FailureModes) > 0 {
		lines = append(lines, "", "Most common failure modes:")
		type modeCount struct {
			mode  string
			count int
		}
		var modes []modeCount
		for mode, count := range report.FailureModes {
			modes = append(modes, modeCount{mode, count})
		}
		sort.Slice(modes, func(i, j int) bool { return modes[i].count > modes[j].count })
		for _, item := range modes {
			lines = append(lines, fmt.Sprintf("- %s: %d", item.mode, item.count))
		}
	}
	lines = append(lines, "", "Latency figures are synthetic-evaluation latency; they are not production latency.", "")
	return strings.Join(lines, "\n")
}

func worstCases(report SystemPerformanceReportV2) []V2CaseResult {
	var sorted []V2CaseResult
	for _, item := range report.Cases {
		sorted = append(sorted, item)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TaskSuccessRate < sorted[j].TaskSuccessRate })
	out := sorted[:0]
	for _, item := range sorted {
		if item.TaskSuccessRate >= 1 {
			break
		}
		out = append(out, item)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func firstFailure(item V2CaseResult) string {
	if len(item.Failures) > 0 {
		return item.Failures[0]
	}
	return "no failure recorded"
}

type v2CategoryRollup struct {
	name    string
	rate    float64
	support int
}

// worstCategories rolls up per-incident-type success rates, worst first.
func worstCategories(report SystemPerformanceReportV2) []v2CategoryRollup {
	groups := make(map[string][]V2CaseResult)
	for _, item := range report.Cases {
		groups[item.IncidentType] = append(groups[item.IncidentType], item)
	}
	var out []v2CategoryRollup
	for name, items := range groups {
		rate := 0.0
		for _, item := range items {
			rate += item.TaskSuccessRate
		}
		rate /= float64(len(items))
		out = append(out, v2CategoryRollup{name: name, rate: rate, support: len(items)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rate < out[j].rate })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// writeV2MetricsCSV flattens the headline metrics into one row per metric.
func writeV2MetricsCSV(path string, report SystemPerformanceReportV2) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	if err := writer.Write([]string{"metric", "value", "ci95_low", "ci95_high", "ci_method", "support"}); err != nil {
		return err
	}
	agg := report.Aggregate
	ci := report.ConfidenceIntervals
	rows := []struct {
		name  string
		value float64
		ci    V2CI
		units string
	}{
		{"task_success_rate", agg.TaskSuccessRateMacro, ci.TaskSuccessRate, "rate"},
		{"pass_at_1", agg.PassAt1, ci.PassAt1, "rate"},
		{"pass_at_k", agg.PassAtK, V2CI{}, "rate"},
		{"false_positive_rate", agg.FalsePositiveRate, ci.FalsePositiveRate, "rate"},
		{"no_op_accuracy", agg.NoOpAccuracy, V2CI{}, "rate"},
		{"root_cause_entity_precision", agg.RootCauseEntityPrecision, V2CI{}, "rate"},
		{"root_cause_entity_recall", agg.RootCauseEntityRecall, ci.RootCauseEntityRecall, "rate"},
		{"root_cause_entity_f1", agg.RootCauseEntityF1, ci.RootCauseEntityF1, "rate"},
		{"root_cause_entity_recall_at_1", agg.RootCauseEntityRecallAt1, V2CI{}, "rate"},
		{"root_cause_entity_recall_at_3", agg.RootCauseEntityRecallAt3, V2CI{}, "rate"},
		{"root_cause_entity_recall_at_5", agg.RootCauseEntityRecallAt5, V2CI{}, "rate"},
		{"root_cause_reasoning_score", agg.RootCauseReasoningScore, ci.RootCauseReasoning, "rate"},
		{"fault_localization_score", agg.FaultLocalizationScore, V2CI{}, "rate"},
		{"unsafe_action_rate", agg.UnsafeActionRate, V2CI{}, "rate"},
		{"policy_violation_rate", agg.PolicyViolationRate, V2CI{}, "rate"},
		{"approval_bypass_rate", agg.ApprovalBypassRate, V2CI{}, "rate"},
		{"forbidden_tool_call_rate", agg.ForbiddenToolCallRate, V2CI{}, "rate"},
		{"destructive_action_attempt_rate", agg.DestructiveActionAttemptRate, V2CI{}, "rate"},
		{"approval_enforcement_rate", agg.ApprovalEnforcementRate, V2CI{}, "rate"},
		{"dry_run_compliance_rate", agg.DryRunComplianceRate, V2CI{}, "rate"},
		{"useful_tool_call_rate", agg.UsefulToolCallRate, V2CI{}, "rate"},
		{"redundant_tool_call_rate", agg.RedundantToolCallRate, V2CI{}, "rate"},
		{"failed_tool_call_rate", agg.FailedToolCallRate, V2CI{}, "rate"},
		{"tool_information_gain", agg.ToolInformationGain, V2CI{}, "rate"},
		{"no_progress_step_rate", agg.NoProgressStepRate, V2CI{}, "rate"},
		{"premature_termination_rate", agg.PrematureTerminationRate, V2CI{}, "rate"},
		{"loop_rate", agg.LoopRate, V2CI{}, "rate"},
		{"latency_p50_ms", agg.LatencyP50MS, V2CI{}, "ms"},
		{"latency_p95_ms", agg.LatencyP95MS, ci.LatencyP95MS, "ms"},
		{"latency_p99_ms", agg.LatencyP99MS, V2CI{}, "ms"},
		{"time_to_diagnosis_p50_ms", agg.TimeToDiagnosisP50MS, V2CI{}, "ms"},
		{"time_to_diagnosis_p95_ms", agg.TimeToDiagnosisP95MS, V2CI{}, "ms"},
		{"mean_tool_calls", agg.MeanToolCalls, ci.MeanToolCalls, "calls"},
		{"mean_tokens", agg.MeanTokens, ci.MeanTokens, "tokens"},
		{"replay_stability", agg.ReplayStability, V2CI{}, "rate"},
		{"flaky_case_rate", agg.FlakyCaseRate, V2CI{}, "rate"},
		{"failure_mode_rate", agg.FailureModeRate, V2CI{}, "rate"},
		{"fatal_failure_rate", agg.FatalFailureRate, V2CI{}, "rate"},
		{"overall_score", report.Scorecard.OverallScore, ci.OverallScore, "score"},
	}
	optional := []struct {
		name  string
		value *float64
		units string
	}{
		{"fault_domain_accuracy", agg.FaultDomainAccuracy, "rate"},
		{"propagation_chain_score", agg.PropagationChainScore, "rate"},
		{"remediation_plan_correctness", agg.RemediationPlanCorrectness, "rate"},
		{"verification_correctness", agg.VerificationCorrectness, "rate"},
		{"recovery_success_rate", agg.RecoverySuccessRate, "rate"},
		{"no_regression_rate", agg.NoRegressionRate, "rate"},
		{"rollback_success_rate", agg.RollbackSuccessRate, "rate"},
		{"tool_selection_recall", agg.ToolSelectionRecall, "rate"},
		{"verdict_consistency", agg.VerdictConsistency, "rate"},
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.name,
			fmt.Sprintf("%.6f", row.value),
			fmt.Sprintf("%.6f", row.ci.Low),
			fmt.Sprintf("%.6f", row.ci.High),
			row.ci.Method,
			fmt.Sprintf("%d", row.ci.Support),
		}); err != nil {
			return err
		}
	}
	for _, row := range optional {
		if row.value == nil {
			continue
		}
		if err := writer.Write([]string{
			row.name,
			fmt.Sprintf("%.6f", *row.value),
			"", "", "",
			"",
		}); err != nil {
			return err
		}
	}
	return nil
}

// writeV2CasesCSV writes one row per case.
func writeV2CasesCSV(path string, report SystemPerformanceReportV2) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	header := []string{
		"id", "task_type", "incident_type", "fault_domain", "difficulty", "robustness_category",
		"trials", "task_success_rate", "task_success_trials", "pass_at_k", "flaky", "passed",
		"rca_entity_precision", "rca_entity_recall", "rca_entity_f1", "rca_recall_at_1",
		"rca_reasoning", "unsafe_action_rate", "approval_bypass_rate",
		"useful_tool_call_rate", "tool_information_gain", "no_progress_step_rate",
		"mean_tool_calls", "mean_tokens", "mean_latency_ms", "time_to_diagnosis_ms",
		"replay_stability", "artifact_coverage", "failure_modes",
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, item := range report.Cases {
		a := item.Aggregate
		modes := make([]string, 0, len(item.FailureModes))
		for mode := range item.FailureModes {
			modes = append(modes, mode)
		}
		sort.Strings(modes)
		row := []string{
			item.ID,
			string(item.TaskType),
			item.IncidentType,
			item.FaultDomain,
			item.Difficulty,
			item.RobustnessCategory,
			fmt.Sprintf("%d", item.Trials),
			fmt.Sprintf("%.4f", item.TaskSuccessRate),
			fmt.Sprintf("%d", item.TaskSuccessTrials),
			fmt.Sprintf("%.4f", item.PassAtK),
			fmt.Sprintf("%t", item.Flaky),
			fmt.Sprintf("%t", item.Passed),
			fmt.Sprintf("%.4f", a.RootCauseEntityPrecision),
			fmt.Sprintf("%.4f", a.RootCauseEntityRecall),
			fmt.Sprintf("%.4f", a.RootCauseEntityF1),
			fmt.Sprintf("%.4f", a.RootCauseEntityRecallAt1),
			fmt.Sprintf("%.4f", a.RootCauseReasoningScore),
			fmt.Sprintf("%.4f", a.UnsafeActionRate),
			fmt.Sprintf("%.4f", a.ApprovalBypassRate),
			fmt.Sprintf("%.4f", a.UsefulToolCallRate),
			fmt.Sprintf("%.4f", a.ToolInformationGain),
			fmt.Sprintf("%.4f", a.NoProgressStepRate),
			fmt.Sprintf("%.1f", a.ToolCallCount),
			fmt.Sprintf("%.0f", a.TotalTokens),
			fmt.Sprintf("%.1f", a.EndToEndLatencyMS),
			fmt.Sprintf("%.1f", a.TimeToDiagnosisMS),
			fmt.Sprintf("%.4f", item.ReplayStability),
			fmt.Sprintf("%.4f", a.ArtifactCoverage),
			strings.Join(modes, "|"),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// writeV2FailureModesCSV writes one row per failure mode.
func writeV2FailureModesCSV(path string, report SystemPerformanceReportV2) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	if err := writer.Write([]string{"failure_mode", "count", "fatal"}); err != nil {
		return err
	}
	modes := make([]string, 0, len(report.FailureModes))
	for mode := range report.FailureModes {
		modes = append(modes, mode)
	}
	sort.Strings(modes)
	for _, mode := range modes {
		if err := writer.Write([]string{
			mode,
			fmt.Sprintf("%d", report.FailureModes[mode]),
			fmt.Sprintf("%t", fatalFailureModes[mode]),
		}); err != nil {
			return err
		}
	}
	return nil
}
