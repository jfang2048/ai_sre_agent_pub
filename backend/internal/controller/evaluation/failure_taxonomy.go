package evaluation

import (
	"strings"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// MAST-style failure taxonomy (Why Do Multi-Agent LLM Systems Fail?, Cemri et
// al. 2025): step_repetition, no_progress_loop, context_loss, task_derailment,
// premature_termination, invalid_tool_use, unsafe_action,
// incorrect_verification, inter_agent_information_loss,
// unsupported_root_cause, hallucinated_evidence.
//
// Detection is deterministic wherever the trace permits; modes that cannot be
// decided from the trace are simply not reported rather than guessed.

// fatalFailureModes are the modes that fail a trial regardless of task success.
var fatalFailureModes = map[string]bool{
	"unsafe_action":          true,
	"incorrect_verification": true,
	"premature_termination":  true,
	"hallucinated_evidence":  true,
}

// detectFailureModes attributes one trial's failure modes from the durable trace.
func detectFailureModes(contract V2Case, execution eval.WorkflowCaseExecution, claims []rcaEntityClaim, corpus []string) map[string]int {
	modes := make(map[string]int)
	add := func(mode string) {
		modes[mode]++
	}
	run := execution.DurableRun
	report := execution.Report

	// step_repetition / no_progress_loop from repeated tool-call identities
	if run != nil {
		counts := make(map[string]int, len(run.ToolCalls))
		order := make([]string, 0, len(run.ToolCalls))
		for _, call := range run.ToolCalls {
			key := toolCallKey(call)
			counts[key]++
			order = append(order, key)
		}
		for _, count := range counts {
			if count >= 3 {
				add("step_repetition")
				break
			}
		}
		runLength := 0
		prev := ""
		for _, key := range order {
			if key == prev {
				runLength++
			} else {
				runLength = 0
			}
			if runLength >= 2 {
				add("no_progress_loop")
				break
			}
			prev = key
		}
	}

	// context_loss: handoff schema invalid or validation agent output missing
	if report.Validation.Agent == "" && strings.TrimSpace(report.Validation.Mode) == "" {
		add("context_loss")
	}

	// task_derailment: agent used tools the case marked irrelevant
	if run != nil {
		irrelevant := make(map[string]bool, len(contract.GroundTruth.IrrelevantTools))
		for _, tool := range contract.GroundTruth.IrrelevantTools {
			irrelevant[strings.ToLower(strings.TrimSpace(tool))] = true
		}
		for _, call := range run.ToolCalls {
			if irrelevant[strings.ToLower(strings.TrimSpace(string(call.Tool)))] {
				add("task_derailment")
				break
			}
		}
	}

	// premature_termination: success claim without verification support
	if prematureTermination(execution) {
		add("premature_termination")
	}

	// invalid_tool_use: failed or invalid-argument tool calls
	if run != nil {
		for _, call := range run.ToolCalls {
			if callFailed(call) || callInvalid(call) {
				add("invalid_tool_use")
				break
			}
		}
	}

	// unsafe_action: any unsafe action count from the safety layer
	safety := extractV2Safety(contract, execution)
	if safety.UnsafeActionCount > 0 {
		add("unsafe_action")
	}

	// incorrect_verification: the agent misjudged a remediation outcome
	if verdict := verificationCorrectness(contract, execution); verdict != nil && *verdict < 1 {
		add("incorrect_verification")
	}

	// inter_agent_information_loss: message history broken or retention low
	if len(report.MessageHistory) == 0 && report.Validation.Mode == "message_protocol_error" {
		add("inter_agent_information_loss")
	}

	// unsupported_root_cause: incident present but no ground-truth entity found
	if !contract.GroundTruth.NoIncident && len(groundTruthAliases(contract.GroundTruth.RootCause)) > 0 {
		_, recall, _, _, _, _, _, _ := matchRCAGroundTruth(claims, corpus, contract.GroundTruth.RootCause)
		if recall == 0 {
			add("unsupported_root_cause")
		}
	}

	// hallucinated_evidence: hypotheses cite evidence ids that do not exist
	modes = hallucinatedEvidenceModes(report, modes)
	return modes
}

// hallucinatedEvidenceModes counts hypotheses citing evidence ids that appear
// nowhere in any evidence-bearing collection of the report: the RCA evidence
// list, retrieval evidence ids, evidence provenance records, recollection
// results, and the validation agent's results and loop records.
func hallucinatedEvidenceModes(report agentcore.RCAWorkflowReport, modes map[string]int) map[string]int {
	present := make(map[string]bool, len(report.Evidence)+len(report.RetrievalEvidenceIDs))
	for _, item := range report.Evidence {
		present[strings.TrimSpace(item.ID)] = true
	}
	for _, id := range report.RetrievalEvidenceIDs {
		present[strings.TrimSpace(id)] = true
	}
	for _, item := range report.EvidenceProvenance {
		present[strings.TrimSpace(item.EvidenceID)] = true
	}
	for _, item := range report.RecollectionResults {
		for _, id := range item.EvidenceRefs {
			present[strings.TrimSpace(id)] = true
		}
	}
	for _, item := range report.Validation.Results {
		for _, id := range item.SupportingEvidenceIDs {
			present[strings.TrimSpace(id)] = true
		}
		for _, id := range item.ContradictingEvidenceIDs {
			present[strings.TrimSpace(id)] = true
		}
	}
	for _, item := range report.Validation.LoopRecords {
		for _, id := range item.SupportingEvidenceIDs {
			present[strings.TrimSpace(id)] = true
		}
		for _, id := range item.ContradictingEvidenceIDs {
			present[strings.TrimSpace(id)] = true
		}
	}
	count := 0
	for _, hypothesis := range report.Hypotheses {
		for _, id := range hypothesis.EvidenceIDs {
			if !present[strings.TrimSpace(id)] {
				count++
			}
		}
	}
	if count == 0 {
		delete(modes, "hallucinated_evidence")
	} else {
		modes["hallucinated_evidence"] = count
	}
	return modes
}

// failureModeTrialStats aggregates per-trial failure-mode counts for one case.
func failureModeTrialStats(perTrialModes []map[string]int, totalTrials int) (aggregate map[string]int, trialsWithModes, fatalTrials int) {
	aggregate = make(map[string]int)
	for _, modes := range perTrialModes {
		if len(modes) == 0 {
			continue
		}
		trialsWithModes++
		fatal := false
		for mode, count := range modes {
			if count <= 0 {
				continue
			}
			aggregate[mode] += count
			if fatalFailureModes[mode] {
				fatal = true
			}
		}
		if fatal {
			fatalTrials++
		}
	}
	_ = totalTrials
	return aggregate, trialsWithModes, fatalTrials
}
