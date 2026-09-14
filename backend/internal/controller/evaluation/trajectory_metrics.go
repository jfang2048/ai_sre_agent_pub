package evaluation

import (
	"encoding/json"
	"strings"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// Trajectory / tool-use quality evaluation v2.
//
// ToolCallCount alone says nothing about whether the investigation was smart;
// these metrics measure call value: did each call add new evidence, was it
// redundant, did it fail, did it match the case's expected tool path?

// toolCallKey normalizes a tool call into a comparable identity.
func toolCallKey(call agentcore.WorkflowToolCall) string {
	query, err := json.Marshal(call.Query)
	if err != nil {
		query = []byte("{}")
	}
	return strings.ToLower(strings.TrimSpace(string(call.Tool))) + "|" + string(query)
}

// extractV2Trajectory extracts per-trial trajectory measurements.
func extractV2Trajectory(contract V2Case, execution eval.WorkflowCaseExecution) V2TrajectoryMetrics {
	metrics := V2TrajectoryMetrics{}
	gt := contract.GroundTruth
	run := execution.DurableRun
	if run == nil {
		return metrics
	}
	calls := run.ToolCalls
	total := len(calls)
	metrics.ToolCallCount = total
	metrics.StepCount = len(run.Steps)
	if total == 0 {
		return metrics
	}

	expectedTools := flattenExpectedTools(gt.ExpectedToolsAny)
	expectedSet := make(map[string]bool, len(expectedTools))
	for _, tool := range expectedTools {
		expectedSet[strings.ToLower(strings.TrimSpace(tool))] = true
	}
	irrelevantSet := make(map[string]bool, len(gt.IrrelevantTools))
	for _, tool := range gt.IrrelevantTools {
		irrelevantSet[strings.ToLower(strings.TrimSpace(tool))] = true
	}

	seen := make(map[string]int, total)
	usedTools := make(map[string]bool, total)
	useful := 0
	failed := 0
	invalid := 0
	repeated := 0
	redundant := 0
	noProgress := 0
	prevKey := ""
	for _, call := range calls {
		key := toolCallKey(call)
		seen[key]++
		usedTools[strings.ToLower(strings.TrimSpace(string(call.Tool)))] = true
		if seen[key] > 1 {
			repeated++
			noProgress++
		}
		if seen[key] > 1 && key == prevKey {
			redundant++
		}
		prevKey = key

		tool := strings.ToLower(strings.TrimSpace(string(call.Tool)))
		if callFailed(call) {
			failed++
		}
		if callInvalid(call) {
			invalid++
		}
		firstOccurrence := seen[key] == 1
		if expectedSet[tool] || (firstOccurrence && !callFailed(call) && !irrelevantSet[tool]) {
			useful++
		}
	}

	metrics.UsefulToolCallRate = ratioScores(useful, total)
	metrics.RedundantToolCallRate = ratioScores(redundant, total)
	metrics.FailedToolCallRate = ratioScores(failed, total)
	metrics.InvalidToolArgumentRate = ratioScores(invalid, total)
	metrics.RepeatedToolCallRate = ratioScores(repeated, total)
	metrics.ToolSelectionPrecision = ratioScores(useful, total)
	if len(gt.ExpectedToolsAny) > 0 {
		recall := expectedToolRecall(gt.ExpectedToolsAny, usedTools)
		metrics.ToolSelectionRecall = &recall
	}
	metrics.ToolInformationGain = ratioScores(len(seen), total)
	metrics.NoProgressStepRate = ratioScores(noProgress, total)
	metrics.EvidenceGainPerStep = ratioScores(len(seen), total)
	metrics.PrematureTerminationRate = boolScore(prematureTermination(execution))
	metrics.LoopRate = loopRate(execution, total)
	metrics.InvalidActionRate = ratioScores(invalid, total)
	return metrics
}

// flattenExpectedTools flattens the any-of tool sets into a single list.
func flattenExpectedTools(expected [][]string) []string {
	var out []string
	for _, alternative := range expected {
		out = append(out, alternative...)
	}
	return out
}

// expectedToolRecall uses any-of semantics: the best coverage over the
// alternative tool sets. Finding any one reasonable tool path counts.
func expectedToolRecall(expected [][]string, usedTools map[string]bool) float64 {
	best := 0.0
	for _, alternative := range expected {
		if len(alternative) == 0 {
			continue
		}
		used := 0
		for _, tool := range alternative {
			if usedTools[strings.ToLower(strings.TrimSpace(tool))] {
				used++
			}
		}
		if coverage := ratioScores(used, len(alternative)); coverage > best {
			best = coverage
		}
	}
	if len(expected) == 0 {
		return 0
	}
	return best
}

// callFailed reports whether a tool call failed or timed out.
func callFailed(call agentcore.WorkflowToolCall) bool {
	if call.TimedOut {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(call.InvocationStatus)) {
	case "failed", "error", "timeout", "invalid":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(call.Status)) {
	case "failed", "error", "timeout":
		return true
	}
	return false
}

// callInvalid reports whether a tool call carried invalid arguments.
func callInvalid(call agentcore.WorkflowToolCall) bool {
	switch strings.ToLower(strings.TrimSpace(call.InvocationStatus)) {
	case "invalid", "invalid_arguments", "invalid_args":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(call.Status)) {
	case "invalid", "invalid_arguments", "invalid_args":
		return true
	}
	return false
}

// prematureTermination reports whether the agent claimed a resolved/effective
// outcome without post-action verification support — the MAST "premature
// termination" mode.
func prematureTermination(execution eval.WorkflowCaseExecution) bool {
	summary := execution.Report.Validation.PostActionValidation
	if summary == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(string(summary.Verdict))) {
	case "confirmed", "partially_supported", "resolved", "verified_effective", "effective":
	default:
		return false
	}
	// a success claim needs supporting evidence or a comparison delta
	if len(summary.SupportingEvidenceIDs) > 0 {
		return false
	}
	if summary.Comparison != nil && summary.Comparison.Comparable {
		return false
	}
	return true
}

// loopRate normalizes validation loop iterations against tool calls.
func loopRate(execution eval.WorkflowCaseExecution, toolCalls int) float64 {
	loops := len(execution.Report.Validation.LoopRecords)
	if loops == 0 {
		return 0
	}
	denominator := toolCalls
	if denominator <= 0 {
		denominator = 1
	}
	return clamp01(float64(loops) / float64(denominator))
}
