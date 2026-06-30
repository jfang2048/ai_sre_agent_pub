package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type recollectionToolResult struct {
	Tool   ToolName
	Result workflowToolResult
}

func (e *WorkflowEngine) stepBroadObservation(ctx context.Context, state *workflowState) error {
	return e.stepCollectSignals(ctx, state)
}

func (e *WorkflowEngine) stepSceneClassification(_ context.Context, state *workflowState) error {
	state.sceneClassification = classifyScene(state)
	if state.sceneClassification.SceneFamily == "" {
		state.sceneClassification.SceneFamily = SceneFamilyResourceContention
	}
	state.evidenceGapState = buildEvidenceGapState(state, state.sceneClassification, state.remainingBudget)
	return nil
}

func (e *WorkflowEngine) stepCompileCollectionPlan(_ context.Context, state *workflowState) error {
	state.collectionPlan = compileCollectionPlan(state, len(state.recollectionResults)+1)
	state.remainingBudget = budgetStateFromPlan(state.collectionPlan, len(state.recollectionResults))
	state.evidenceGapState = buildEvidenceGapState(state, state.sceneClassification, state.remainingBudget)
	return nil
}

func (e *WorkflowEngine) stepTargetedRecollection(ctx context.Context, state *workflowState) error {
	state.recollectionToolResults = state.recollectionToolResults[:0]
	plan := state.collectionPlan
	if strings.TrimSpace(plan.PlanID) == "" {
		return nil
	}

	for rounds := 0; rounds < maxSceneRecollectionRounds; rounds++ {
		profileStatus := defaultCollectorProfileStatus(plan, "collector profile applier not configured")
		if e.collectorProfileApplier != nil {
			status, err := e.collectorProfileApplier.ApplyRuntimeProfile(ctx, state.collectorID, collectorProfileRequestFromPlan(plan))
			if err != nil {
				state.limitations = append(state.limitations, fmt.Sprintf("collector runtime profile apply failed: %v", err))
				profileStatus = defaultCollectorProfileStatus(plan, err.Error())
				profileStatus.State = "apply_failed"
			} else {
				profileStatus = status
			}
		}

		beforeCalls := len(state.toolCalls)
		beforeConfidence := topHypothesisConfidence(state.hypotheses)
		modules := modulesToTools(plan.TargetCollectorsOrModules)
		for _, tool := range modules {
			query := recollectionQueryForTool(state, plan, tool)
			result, err := state.callTool(ctx, "targeted_recollection", tool, query)
			if err != nil {
				state.limitations = append(state.limitations, fmt.Sprintf("%s recollection unavailable", tool))
				continue
			}
			state.applyToolResult(tool, result)
			state.recollectionToolResults = append(state.recollectionToolResults, recollectionToolResult{Tool: tool, Result: result})
		}

		state.evidenceGapState = buildEvidenceGapState(state, state.sceneClassification, budgetStateFromPlan(plan, len(state.recollectionResults)+1))
		state.remainingBudget = state.evidenceGapState.RemainingBudget
		newRefs := toolCallIDs(state.toolCalls[beforeCalls:])
		converged := recollectionConverged(state, beforeConfidence)
		result := RecollectionResult{
			PlanID:         plan.PlanID,
			SceneFamily:    plan.SceneFamily,
			RoundIndex:     plan.RoundIndex,
			AppliedModules: append([]string(nil), plan.TargetCollectorsOrModules...),
			EvidenceRefs:   newRefs,
			ObservedBytes:  minInt64(plan.MaxBytes, int64(len(newRefs))*4096),
			ObservedEvents: minInt(plan.MaxEvents, len(newRefs)*4),
			RemainingGaps:  append([]string(nil), state.evidenceGapState.MissingEvidence...),
			StopReason:     recollectionStopReason(converged, state.evidenceGapState),
			Converged:      converged,
			ProfileStatus:  &profileStatus,
			CompletedAt:    time.Now().UTC(),
		}
		state.recollectionResults = append(state.recollectionResults, result)
		state.sceneProfileStatus = &profileStatus
		if converged || rounds >= maxSceneRecollectionRounds-1 {
			break
		}
		plan = compileCollectionPlan(state, len(state.recollectionResults)+1)
		state.collectionPlan = plan
	}
	return nil
}

func (e *WorkflowEngine) stepUpdateHypothesesFromRecollection(_ context.Context, state *workflowState) error {
	ensureInitialHypotheses(state)
	for _, item := range state.recollectionToolResults {
		updateHypothesesFromToolResult(state, AgentPlanStep{Tool: item.Tool}, item.Result)
	}
	rerankHypotheses(state)
	state.evidenceGapState = buildEvidenceGapState(state, state.sceneClassification, state.remainingBudget)
	state.escalationDecision = buildEscalationDecision(state)
	return nil
}

func modulesToTools(modules []string) []ToolName {
	tools := make([]ToolName, 0, len(modules))
	for _, module := range modules {
		switch strings.ToLower(strings.TrimSpace(module)) {
		case "metrics":
			tools = append(tools, ToolMetrics)
		case "logs":
			tools = append(tools, ToolLogs)
		case "change":
			tools = append(tools, ToolChangeQuery)
		case "config":
			tools = append(tools, ToolConfigState)
		case "connectivity":
			tools = append(tools, ToolConnectivityCheck)
		case "dns":
			tools = append(tools, ToolDNSCheck)
		case "storage":
			tools = append(tools, ToolStorageHealth)
		case "gpu":
			tools = append(tools, ToolGPU)
		case "security":
			tools = append(tools, ToolSecurity)
		case "security_graph":
			tools = append(tools, ToolSecurityGraph)
		case "process":
			tools = append(tools, ToolProcessLineage)
		case "runtime":
			tools = append(tools, ToolEBPFQuery)
		case "topology":
			tools = append(tools, ToolTopology)
		case "kubernetes":
			tools = append(tools, ToolKubernetesResource)
		case "container_revision":
			tools = append(tools, ToolContainerRevision)
		case "service_health":
			tools = append(tools, ToolServiceHealth)
		}
	}
	if len(tools) == 0 {
		return []ToolName{ToolMetrics, ToolLogs}
	}
	return dedupeToolNames(tools)
}

func recollectionQueryForTool(state *workflowState, plan CollectionPlan, tool ToolName) map[string]string {
	query := map[string]string{
		"query":                strings.Join(append([]string{state.incident.Summary}, plan.EvidenceGoals...), " "),
		"incident_summary":     state.incident.Summary,
		"scope":                strings.Join(plan.TargetScope, ","),
		"scene_family":         string(plan.SceneFamily),
		"round_index":          strconv.Itoa(plan.RoundIndex),
		"sampling_interval":    plan.SamplingInterval.String(),
		"collection_window":    plan.CollectionWindow.String(),
		"process_topk":         strconv.Itoa(plan.ProcessTopK),
		"log_budget":           strconv.Itoa(plan.LogBudget),
		"event_filters":        strings.Join(plan.EventFilters, ","),
		"gpu_detail_mode":      plan.GPUDetailMode,
		"max_overhead_percent": fmt.Sprintf("%.2f", plan.MaxOverheadPercent),
	}
	switch tool {
	case ToolLogs:
		query["query"] = strings.Join(plan.EventFilters, " ")
	case ToolChangeQuery:
		query["query"] = strings.Join(append(plan.EventFilters, strings.Fields(strings.ToLower(state.trigger))...), " ")
	case ToolConnectivityCheck, ToolDNSCheck, ToolNetworkBlastRadius:
		query["signals"] = "network,dns,latency"
	case ToolStorageHealth:
		query["signals"] = "io,disk,storage"
	case ToolGPU:
		query["signals"] = "gpu,cuda,collective"
	case ToolKubernetesResource, ToolContainerRevision:
		query["signals"] = "pod,container,rollout,revision"
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery:
		query["signals"] = "runtime,process,security"
	}
	return query
}

func buildEvidenceGapState(state *workflowState, classification SceneClassification, budget InvestigationBudgetState) EvidenceGapState {
	goals := dedupeStrings(append([]string(nil), classification.MissingEvidence...))
	if len(state.retrievedDocs) == 0 {
		goals = append(goals, "corroborating_knowledge")
	}
	if len(state.hypotheses) == 0 || topHypothesisConfidence(state.hypotheses) < 0.60 {
		goals = append(goals, "stable_ranked_hypothesis")
	}
	return EvidenceGapState{
		SceneFamily:             classification.SceneFamily,
		MissingEvidence:         dedupeStrings(append([]string(nil), classification.MissingEvidence...)),
		EvidenceGoalsStillUnmet: dedupeStrings(goals),
		RemainingBudget:         budget,
		Confidence:              maxFloat(classification.Confidence, topHypothesisConfidence(state.hypotheses)),
		UpdatedAt:               time.Now().UTC(),
	}
}

func recollectionConverged(state *workflowState, beforeHypothesisConfidence float64) bool {
	if state == nil {
		return true
	}
	after := topHypothesisConfidence(state.hypotheses)
	switch {
	case len(state.evidenceGapState.MissingEvidence) == 0:
		return true
	case state.sceneClassification.Confidence >= 0.75 && after >= 0.72:
		return true
	case after-beforeHypothesisConfidence >= 0.10:
		return true
	default:
		return false
	}
}

func recollectionStopReason(converged bool, gaps EvidenceGapState) string {
	if converged {
		return "scene evidence converged"
	}
	if gaps.RemainingBudget.RemainingRounds == 0 {
		return "recollection budget exhausted"
	}
	if len(gaps.MissingEvidence) > 0 {
		return "evidence goals still unmet"
	}
	return "recollection completed"
}

func buildEscalationDecision(state *workflowState) EscalationDecision {
	if state == nil {
		return EscalationDecision{}
	}
	reasons := make([]string, 0, 4)
	riskyNextAction := false
	weakRollback := false

	if state.sceneClassification.Confidence < 0.55 {
		reasons = append(reasons, "scene confidence remained below 0.55")
	}
	if topHypothesisConfidence(state.hypotheses) < 0.60 {
		reasons = append(reasons, "top hypothesis confidence remained below 0.60")
	}
	if len(state.evidenceGapState.EvidenceGoalsStillUnmet) > 0 {
		reasons = append(reasons, "evidence goals still unmet")
	}
	if candidate := selectedGuardedActionCandidate(state); candidate != nil {
		contract := candidate.ActionContract
		if contract == nil && candidate.DiagnosticContract != nil {
			compiled := compileValidationActionContract(*candidate.DiagnosticContract, state.collectorID, candidate.BlastRadiusScope)
			contract = &compiled
		}
		if contract != nil {
			riskyNextAction = actuatorSafetyTierForContract(contract) != ActuatorSafetyTierReadOnly
			weakRollback = !contract.Rollback.Reversible && strings.TrimSpace(contract.Rollback.Summary) == ""
			if riskyNextAction && (contract.RequiresApproval || weakRollback) {
				reasons = append(reasons, "next action exceeds safe autonomous boundary")
			}
		}
	}

	artifactPackage := compactStrings(
		firstNonEmpty(artifactIDFromRef(state.analysisHandoffMessage)),
		firstNonEmpty(artifactIDFromRef(state.validationResultMessage)),
	)
	return EscalationDecision{
		Escalate:             len(reasons) > 0,
		Reason:               strings.Join(dedupeStrings(reasons), "; "),
		Confidence:           maxFloat(state.sceneClassification.Confidence, topHypothesisConfidence(state.hypotheses)),
		RiskyNextAction:      riskyNextAction,
		WeakRollback:         weakRollback,
		EvidencePackageReady: state.durableRun != nil,
		ArtifactPackage:      artifactPackage,
		DecidedAt:            time.Now().UTC(),
	}
}

func artifactIDFromRef(ref *AgentMessageRef) string {
	if ref == nil {
		return ""
	}
	return strings.TrimSpace(ref.ArtifactID)
}

func dedupeToolNames(in []ToolName) []ToolName {
	if len(in) == 0 {
		return nil
	}
	out := make([]ToolName, 0, len(in))
	seen := make(map[ToolName]struct{}, len(in))
	for _, item := range in {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func minInt64(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}
