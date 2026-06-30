package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	adaptiveRuntimeSchemaVersion = "ai_sre_agent/adaptive_runtime/v1"
	adaptiveRuntimeStage         = "adaptive_runtime_loop"
)

type AdaptiveBudgetState struct {
	RemainingToolCalls          int    `json:"remaining_tool_calls"`
	RemainingIterations         int    `json:"remaining_iterations"`
	RemainingSameToolRetries    int    `json:"remaining_same_tool_retries"`
	RemainingHypothesisRewrites int    `json:"remaining_hypothesis_rewrites"`
	RemainingTokenBudget        int    `json:"remaining_token_budget"`
	RemainingTimeBudget         string `json:"remaining_time_budget,omitempty"`
}

type AdaptiveReplayMetadata struct {
	Replayable        bool      `json:"replayable"`
	ReplayPoint       string    `json:"replay_point,omitempty"`
	RecoveryHint      string    `json:"recovery_hint,omitempty"`
	LastCheckpointAt  time.Time `json:"last_checkpoint_at,omitempty"`
	CheckpointVersion string    `json:"checkpoint_version,omitempty"`
}

type AdaptiveEvidenceInventoryItem struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Source  string `json:"source,omitempty"`
	Summary string `json:"summary"`
}

type AdaptiveRuntimeState struct {
	SchemaVersion            string                          `json:"schema_version"`
	RunID                    string                          `json:"run_id,omitempty"`
	IncidentID               string                          `json:"incident_id,omitempty"`
	RuntimeMode              string                          `json:"runtime_mode"`
	Objective                string                          `json:"objective"`
	Subgoals                 []string                        `json:"subgoals,omitempty"`
	CurrentSceneFamily       SceneFamily                     `json:"current_scene_family,omitempty"`
	CurrentSceneConfidence   float64                         `json:"current_scene_confidence,omitempty"`
	EvidenceInventory        []AdaptiveEvidenceInventoryItem `json:"evidence_inventory,omitempty"`
	UnresolvedEvidenceGaps   []string                        `json:"unresolved_evidence_gaps,omitempty"`
	ActiveHypotheses         []RCAHypothesis                 `json:"active_hypotheses,omitempty"`
	CurrentHypotheses        []RCAHypothesis                 `json:"current_hypotheses,omitempty"`
	ContradictionSet         []string                        `json:"contradiction_set,omitempty"`
	ScopeHints               []string                        `json:"scope_hints,omitempty"`
	SceneClassification      SceneClassification             `json:"scene_classification,omitempty"`
	CurrentConfidence        float64                         `json:"current_confidence,omitempty"`
	ConfidenceScore          float64                         `json:"confidence_score"`
	CurrentRisk              float64                         `json:"current_risk,omitempty"`
	RiskScore                float64                         `json:"risk_score"`
	Budget                   AdaptiveBudgetState             `json:"budget"`
	RemainingToolBudget      int                             `json:"remaining_tool_budget,omitempty"`
	RemainingTokenBudget     int                             `json:"remaining_token_budget,omitempty"`
	RemainingTimeBudget      string                          `json:"remaining_time_budget,omitempty"`
	ExecutionPosture         string                          `json:"execution_posture"`
	ApprovalStatus           string                          `json:"approval_status"`
	Iteration                int                             `json:"iteration"`
	ToolCalls                int                             `json:"tool_calls"`
	HypothesisRewrites       int                             `json:"hypothesis_rewrites"`
	NoProgressRounds         int                             `json:"no_progress_rounds,omitempty"`
	UncertaintyPlateauRounds int                             `json:"uncertainty_plateau_rounds,omitempty"`
	ToolHistory              []string                        `json:"tool_history,omitempty"`
	ToolYieldHistory         []AdaptiveToolYieldRecord       `json:"tool_yield_history,omitempty"`
	LatestProgress           *AdaptiveProgressAssessment     `json:"latest_progress,omitempty"`
	HardStop                 bool                            `json:"hard_stop"`
	StopReason               string                          `json:"stop_reason,omitempty"`
	Replay                   AdaptiveReplayMetadata          `json:"replay"`
	UpdatedAt                time.Time                       `json:"updated_at"`
}

type AdaptiveDialogueTurn struct {
	TurnID         string    `json:"turn_id"`
	Iteration      int       `json:"iteration"`
	Role           string    `json:"role"`
	Producer       string    `json:"producer"`
	Consumer       string    `json:"consumer,omitempty"`
	Summary        string    `json:"summary"`
	Inputs         []string  `json:"inputs,omitempty"`
	Outputs        []string  `json:"outputs,omitempty"`
	ToolDecisionID string    `json:"tool_decision_id,omitempty"`
	ArtifactID     string    `json:"artifact_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type AdaptiveToolScore struct {
	Relevance          float64 `json:"relevance"`
	ExpectedInfoGain   float64 `json:"expected_information_gain"`
	CostPenalty        float64 `json:"cost_penalty"`
	RiskPenalty        float64 `json:"risk_penalty"`
	Availability       float64 `json:"availability"`
	SceneCompatibility float64 `json:"scene_compatibility"`
	GapCoverage        float64 `json:"gap_coverage"`
	PolicyEligibility  float64 `json:"policy_eligibility"`
	FreshnessFit       float64 `json:"freshness_fit"`
	ScopeFit           float64 `json:"scope_fit"`
	DependencyFit      float64 `json:"dependency_fit"`
	ExperiencePrior    float64 `json:"experience_prior"`
	CheapFirstBias     float64 `json:"cheap_first_bias"`
	RepeatPenalty      float64 `json:"repeat_penalty"`
	Total              float64 `json:"total"`
}

type AdaptiveToolDecision struct {
	DecisionID         string                      `json:"decision_id"`
	SchemaVersion      string                      `json:"schema_version"`
	Iteration          int                         `json:"iteration"`
	Tool               ToolName                    `json:"tool"`
	ToolContract       string                      `json:"tool_contract"`
	CapabilityFamily   string                      `json:"capability_family"`
	Reason             string                      `json:"reason"`
	EvidenceGapCovered []string                    `json:"evidence_gap_covered,omitempty"`
	ExpectedEvidence   []string                    `json:"expected_evidence,omitempty"`
	Query              map[string]string           `json:"query,omitempty"`
	Score              AdaptiveToolScore           `json:"score"`
	Policy             ActionPolicyDecision        `json:"policy,omitempty"`
	Executable         bool                        `json:"executable"`
	AutoSelected       bool                        `json:"auto_selected"`
	ProposalOnly       bool                        `json:"proposal_only,omitempty"`
	BlockedReason      string                      `json:"blocked_reason,omitempty"`
	ToolCallID         string                      `json:"tool_call_id,omitempty"`
	Outcome            string                      `json:"outcome,omitempty"`
	Progress           *AdaptiveProgressAssessment `json:"progress,omitempty"`
	NormalizedResult   *NormalizedToolResult       `json:"normalized_result,omitempty"`
	StopReason         string                      `json:"stop_reason,omitempty"`
	PlannerRole        string                      `json:"planner_role"`
	CriticRole         string                      `json:"critic_role"`
	ControllerGate     string                      `json:"controller_gate"`
	CreatedAt          time.Time                   `json:"created_at"`
}

type AdaptiveProgressAssessment struct {
	SchemaVersion            string  `json:"schema_version"`
	ToolCallID               string  `json:"tool_call_id,omitempty"`
	UncertaintyBefore        float64 `json:"uncertainty_before"`
	UncertaintyAfter         float64 `json:"uncertainty_after"`
	UncertaintyDelta         float64 `json:"uncertainty_delta"`
	ConfidenceBefore         float64 `json:"confidence_before"`
	ConfidenceAfter          float64 `json:"confidence_after"`
	ConfidenceDelta          float64 `json:"confidence_delta"`
	ContradictionsBefore     int     `json:"contradictions_before"`
	ContradictionsAfter      int     `json:"contradictions_after"`
	ContradictionDelta       int     `json:"contradiction_delta"`
	EvidenceGapsBefore       int     `json:"evidence_gaps_before"`
	EvidenceGapsAfter        int     `json:"evidence_gaps_after"`
	EvidenceGapCoverageDelta int     `json:"evidence_gap_coverage_delta"`
	RiskBefore               float64 `json:"risk_before"`
	RiskAfter                float64 `json:"risk_after"`
	RiskDelta                float64 `json:"risk_delta"`
	ActionEffectDelta        float64 `json:"action_effect_delta"`
	Progress                 bool    `json:"progress"`
	Plateau                  bool    `json:"plateau"`
	Summary                  string  `json:"summary"`
}

type NormalizedToolResult struct {
	SchemaVersion               string   `json:"schema_version"`
	Tool                        ToolName `json:"tool"`
	ToolCallID                  string   `json:"tool_call_id,omitempty"`
	Summary                     string   `json:"summary"`
	StructuredFindings          []string `json:"structured_findings,omitempty"`
	ConfidenceDelta             float64  `json:"confidence_delta,omitempty"`
	ContradictionDelta          float64  `json:"contradiction_delta,omitempty"`
	ConfidenceContribution      float64  `json:"confidence_contribution,omitempty"`
	ContradictionContribution   float64  `json:"contradiction_contribution,omitempty"`
	EvidenceIDs                 []string `json:"evidence_ids,omitempty"`
	AffectedScope               []string `json:"affected_scope,omitempty"`
	Freshness                   string   `json:"freshness,omitempty"`
	LikelyNextToolFamilies      []string `json:"likely_next_tool_families,omitempty"`
	LikelyNextChecks            []string `json:"likely_next_checks,omitempty"`
	RecommendedScopeRefinement  []string `json:"recommended_scope_refinement,omitempty"`
	RecommendedTimeWindowRefine string   `json:"recommended_time_window_refinement,omitempty"`
	ResultQuality               string   `json:"result_quality,omitempty"`
	LowYieldSignal              bool     `json:"low_yield_signal,omitempty"`
	Cacheability                string   `json:"cacheability,omitempty"`
	HypothesisSpaceNarrowed     bool     `json:"hypothesis_space_narrowed,omitempty"`
	NarrowsHypothesisSpace      bool     `json:"narrows_hypothesis_space,omitempty"`
	RemediationEligibilityDelta float64  `json:"remediation_eligibility_delta,omitempty"`
}

type AdaptiveArtifact struct {
	SchemaVersion   string               `json:"schema_version"`
	Version         string               `json:"version"`
	Kind            WorkflowArtifactKind `json:"kind"`
	ArtifactID      string               `json:"artifact_id"`
	RunID           string               `json:"run_id"`
	IncidentID      string               `json:"incident_id,omitempty"`
	CorrelationID   string               `json:"correlation_id,omitempty"`
	Producer        string               `json:"producer"`
	Consumer        string               `json:"consumer,omitempty"`
	Status          string               `json:"status"`
	Iteration       int                  `json:"iteration,omitempty"`
	InputArtifacts  []string             `json:"input_artifacts,omitempty"`
	OutputArtifacts []string             `json:"output_artifacts,omitempty"`
	Replayable      bool                 `json:"replayable"`
	ReplaySemantics string               `json:"replay_semantics"`
	Summary         string               `json:"summary"`
	Payload         map[string]any       `json:"payload,omitempty"`
	ProducedAt      time.Time            `json:"produced_at"`
}

type adaptiveToolCandidate struct {
	Tool       ToolName
	Contract   WorkflowToolContract
	Query      map[string]string
	Reason     string
	GapCovered []string
	Score      AdaptiveToolScore
	Source     string
}

type governedAdaptiveRuntime struct {
	cfg WorkflowConfig
}

func adaptiveRuntimeEnabled(cfg WorkflowConfig) bool {
	return runtimeModeEnablesAdaptiveRuntime(cfg)
}

func newGovernedAdaptiveRuntime(cfg WorkflowConfig) governedAdaptiveRuntime {
	return governedAdaptiveRuntime{cfg: normalizeWorkflowConfig(cfg)}
}

func (e *WorkflowEngine) stepAdaptiveRuntimeLoop(ctx context.Context, state *workflowState) error {
	if e == nil || state == nil || !adaptiveRuntimeEnabled(e.cfg) {
		return nil
	}
	runtime := newGovernedAdaptiveRuntime(e.cfg)
	return runtime.run(ctx, state)
}

func (r governedAdaptiveRuntime) run(ctx context.Context, state *workflowState) error {
	if state == nil || state.engine == nil || state.engine.tools == nil {
		return nil
	}
	started := time.Now().UTC()
	runCtx := ctx
	if r.cfg.AdaptiveTimeBudget > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, r.cfg.AdaptiveTimeBudget)
		defer cancel()
	}

	toolCalls := 0
	hypothesisRewrites := 0
	noProgressRounds := 0
	plateauRounds := 0
	sameToolCalls := map[ToolName]int{}
	state.recordAdaptiveArtifact(ctx, WorkflowArtifactObjectiveState, "observer", "planner", 0, "adaptive objective state initialized", map[string]any{
		"objective": adaptiveObjective(state),
		"mode":      r.cfg.RuntimeMode,
	})

	stopReason := ""
	for iteration := 1; iteration <= r.cfg.AdaptiveMaxIterations; iteration++ {
		if err := runCtx.Err(); err != nil {
			stopReason = string(AdaptiveStopReasonBudgetExhausted)
			break
		}
		if toolCalls >= r.cfg.AdaptiveMaxToolCalls {
			stopReason = string(AdaptiveStopReasonBudgetExhausted)
			break
		}

		runtimeState := buildAdaptiveRuntimeState(state, r.cfg, iteration, toolCalls, hypothesisRewrites, noProgressRounds, plateauRounds, started, "")
		state.recordAdaptiveState(ctx, runtimeState)
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactRuntimeState, "observer", "planner", iteration, "adaptive runtime state checkpointed", map[string]any{
			"objective":   runtimeState.Objective,
			"subgoals":    strings.Join(runtimeState.Subgoals, ","),
			"scope_hints": strings.Join(runtimeState.ScopeHints, ","),
			"confidence":  fmt.Sprintf("%.2f", runtimeState.ConfidenceScore),
			"risk":        fmt.Sprintf("%.2f", runtimeState.RiskScore),
			"tool_budget": strconv.Itoa(runtimeState.Budget.RemainingToolCalls),
			"time_budget": runtimeState.Budget.RemainingTimeBudget,
		})
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactEvidenceGapSet, "observer", "planner", iteration, "adaptive evidence gaps checkpointed", map[string]any{
			"gaps":       runtimeState.UnresolvedEvidenceGaps,
			"confidence": fmt.Sprintf("%.2f", runtimeState.ConfidenceScore),
		})

		selectionContext := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
		richCandidates := generateToolCandidates(state, selectionContext)
		selectionDecision := buildToolSelectionDecision(state, selectionContext, richCandidates)
		if selectionDecision.Selected == nil {
			stopReason = firstNonEmpty(selectionDecision.StopReason, string(AdaptiveStopReasonNoSafeNextStep))
			break
		}
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactToolCandidateScores, "planner", "critic", iteration, "ranked adaptive tool candidates", map[string]any{
			"selection_decision": selectionDecision,
			"candidates":         truncateToolCandidates(selectionDecision.Alternatives, 5),
		})
		proposal := buildPlannerProposal(state, selectionContext, selectionDecision.Alternatives)
		if proposal.Selected == nil {
			selected := *selectionDecision.Selected
			proposal.Selected = &selected
		}
		if proposal.Selected == nil {
			stopReason = string(AdaptiveStopReasonNoSafeNextStep)
			break
		}
		candidate := adaptiveCandidateFromToolCandidate(*proposal.Selected)
		if state.engine.telemetry != nil {
			state.engine.telemetry.recordSkillScore(candidate.Tool, candidate.Contract.CapabilityFamily, r.cfg.RuntimeMode, candidate.Score.Total)
		}
		state.recordAdaptiveDialogue(ctx, AdaptiveDialogueTurn{
			TurnID:    fmt.Sprintf("adaptive-%s-%02d-planner", sanitizeID(state.workflowID), iteration),
			Iteration: iteration,
			Role:      "planner",
			Producer:  "adaptive_planner",
			Consumer:  "adaptive_critic",
			Summary:   fmt.Sprintf("selected %s to address %s", candidate.Tool, strings.Join(candidate.GapCovered, ",")),
			Inputs:    runtimeState.UnresolvedEvidenceGaps,
			Outputs:   compactStrings(string(candidate.Tool), candidate.Reason),
			CreatedAt: time.Now().UTC(),
		})
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactPlannerProposal, "planner", "critic", iteration, candidate.Reason, map[string]any{
			"proposal":           proposal,
			"selection_decision": selectionDecision,
		})

		decision := buildAdaptiveToolDecision(state, candidate, iteration)
		decision.Policy = evaluateAdaptiveCandidatePolicy(state, candidate)
		decision.Executable, decision.ProposalOnly, decision.BlockedReason = adaptiveExecutionEligibility(candidate, decision.Policy, sameToolCalls[candidate.Tool], r.cfg)
		decision.AutoSelected = decision.Executable
		state.recordAdaptiveToolDecision(ctx, decision)
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactToolDecision, "planner", "policy_gate", iteration, decision.Reason, map[string]any{
			"decision_id": decision.DecisionID,
			"tool":        string(decision.Tool),
			"score":       fmt.Sprintf("%.3f", decision.Score.Total),
			"executable":  strconv.FormatBool(decision.Executable),
			"blocked":     decision.BlockedReason,
		})

		critiqueSummary := adaptiveCritiqueSummary(candidate, decision)
		branch := adaptiveBranch(decision)
		if runtimeModeEnablesPlannerCritic(r.cfg) {
			critique := critiquePlannerProposal(state, proposal)
			critiqueSummary = firstNonEmpty(critique.Summary, critiqueSummary)
			branch = firstNonEmpty(string(critique.RecommendedBranch), branch)
			state.recordAdaptiveDialogue(ctx, AdaptiveDialogueTurn{
				TurnID:         fmt.Sprintf("adaptive-%s-%02d-critic", sanitizeID(state.workflowID), iteration),
				Iteration:      iteration,
				Role:           "critic",
				Producer:       "adaptive_critic",
				Consumer:       "policy_gate",
				Summary:        critiqueSummary,
				Inputs:         compactStrings(decision.DecisionID, string(candidate.Tool)),
				Outputs:        compactStrings(decision.BlockedReason, decision.StopReason),
				ToolDecisionID: decision.DecisionID,
				CreatedAt:      time.Now().UTC(),
			})
			state.recordAdaptiveArtifact(ctx, WorkflowArtifactCritiqueReport, "critic", "policy_gate", iteration, critiqueSummary, map[string]any{
				"decision_id":     decision.DecisionID,
				"auto_selectable": strconv.FormatBool(candidate.Contract.EligibleForAutoSelection),
				"critique":        critique,
			})
			if critique.RecommendedBranch == AdaptiveBranchNarrowScope {
				state.adaptiveScopeHints = dedupeStrings(append(state.adaptiveScopeHints, candidate.GapCovered...))
			}
			if critique.StopReason != "" {
				stopReason = string(critique.StopReason)
				break
			}
		}
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactBranchDecision, "policy_gate", "executor", iteration, adaptiveBranchSummary(decision), map[string]any{
			"decision_id": decision.DecisionID,
			"branch":      branch,
		})

		if !decision.Executable {
			state.recordAdaptiveArtifact(ctx, WorkflowArtifactExecutionIntent, "policy_gate", "executor", iteration, firstNonEmpty(decision.BlockedReason, "adaptive candidate stayed proposal-only"), map[string]any{
				"tool":          string(decision.Tool),
				"proposal_only": "true",
				"policy_status": decision.Policy.Status,
			})
			if state.adaptiveState != nil {
				state.adaptiveState.markNoProgress(firstNonEmpty(decision.BlockedReason, "adaptive candidate stayed proposal-only"))
				state.recordAdaptiveState(ctx, *state.adaptiveState)
			}
			stopReason = string(adaptiveStopReasonForDecision(decision))
			break
		}

		beforeState := buildAdaptiveRuntimeState(state, r.cfg, iteration, toolCalls, hypothesisRewrites, noProgressRounds, plateauRounds, started, "")
		beforeUpdates := len(state.hypothesisUpdates)
		result, err := state.callToolAs(runCtx, adaptiveRuntimeStage, "adaptive_executor", candidate.Tool, candidate.Query, decision.DecisionID)
		toolCalls++
		sameToolCalls[candidate.Tool]++
		if len(state.toolCalls) > 0 {
			last := state.toolCalls[len(state.toolCalls)-1]
			decision.ToolCallID = last.ID
			decision.Outcome = firstNonEmpty(last.Outcome, last.Status)
			state.replaceAdaptiveToolDecision(ctx, decision)
		}
		if err != nil {
			state.limitations = append(state.limitations, fmt.Sprintf("adaptive tool %s failed: %s", candidate.Tool, err.Error()))
			if state.adaptiveState != nil {
				state.adaptiveState.markNoProgress(err.Error())
				state.recordAdaptiveState(ctx, *state.adaptiveState)
			}
			stopReason = string(AdaptiveStopReasonEvidenceUnavailable)
			continue
		}
		state.applyToolResult(candidate.Tool, result)
		updateHypothesesFromToolResult(state, AgentPlanStep{Tool: candidate.Tool}, result)
		rerankHypotheses(state)
		if len(state.hypothesisUpdates) > beforeUpdates {
			hypothesisRewrites++
			state.recordAdaptiveArtifact(ctx, WorkflowArtifactHypothesisRevision, "verifier", "planner", iteration, "hypotheses updated from adaptive tool result", map[string]any{
				"tool":    string(candidate.Tool),
				"updates": strconv.Itoa(len(state.hypothesisUpdates) - beforeUpdates),
			})
		}
		state.evidenceGapState = buildEvidenceGapState(state, state.sceneClassification, state.remainingBudget)
		afterState := buildAdaptiveRuntimeState(state, r.cfg, iteration, toolCalls, hypothesisRewrites, noProgressRounds, plateauRounds, started, "")
		progress := buildAdaptiveProgressAssessment(beforeState, afterState, decision.ToolCallID)
		decision.Progress = &progress
		decision.NormalizedResult = normalizeToolResult(candidate.Tool, result, decision.ToolCallID, state)
		state.replaceAdaptiveToolDecision(ctx, decision)
		state.applyNormalizedToolResult(decision.NormalizedResult)
		if decision.NormalizedResult != nil && decision.NormalizedResult.LowYieldSignal && state.engine.telemetry != nil {
			state.engine.telemetry.recordSkillLowYield(candidate.Tool, candidate.Contract.CapabilityFamily, r.cfg.RuntimeMode)
		}
		if state.adaptiveState != nil {
			state.adaptiveState.applyToolResult(decision.NormalizedResult)
			if len(state.hypothesisUpdates) > beforeUpdates {
				state.adaptiveState.applyHypothesisRevision(state.hypotheses, adaptiveContradictions(state))
			}
			state.adaptiveState.refineScope(decision.NormalizedResult.RecommendedScopeRefinement...)
			state.adaptiveState.refineWindow(decision.NormalizedResult.RecommendedTimeWindowRefine)
			state.adaptiveState.applyProgressAssessment(progress)
			state.adaptiveState.UnresolvedEvidenceGaps = append([]string(nil), adaptiveEvidenceGaps(state)...)
			state.adaptiveState.ContradictionSet = append([]string(nil), adaptiveContradictions(state)...)
			state.adaptiveState.CurrentConfidence = afterState.CurrentConfidence
			state.adaptiveState.ConfidenceScore = afterState.ConfidenceScore
			state.adaptiveState.CurrentRisk = afterState.CurrentRisk
			state.adaptiveState.RiskScore = afterState.RiskScore
			state.adaptiveState.Budget = afterState.Budget
			state.adaptiveState.RemainingToolBudget = afterState.RemainingToolBudget
			state.adaptiveState.RemainingTokenBudget = afterState.RemainingTokenBudget
			state.adaptiveState.RemainingTimeBudget = afterState.RemainingTimeBudget
			state.adaptiveState.Iteration = iteration
			state.adaptiveState.ToolCalls = afterState.ToolCalls
			state.adaptiveState.HypothesisRewrites = afterState.HypothesisRewrites
			state.adaptiveState.UpdatedAt = afterState.UpdatedAt
			state.recordAdaptiveState(ctx, *state.adaptiveState)
		}
		verifier := verifyAdaptiveProgress(beforeState, afterState, decision.NormalizedResult, progress)
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactNormalizedToolResult, "executor", "verifier", iteration, firstNonEmpty(decision.NormalizedResult.Summary, result.Summary), map[string]any{
			"normalized_result": decision.NormalizedResult,
		})
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactToolResultSummary, "executor", "verifier", iteration, firstNonEmpty(result.Summary, "adaptive tool result summarized"), map[string]any{
			"tool":      string(candidate.Tool),
			"tool_call": decision.ToolCallID,
			"outcome":   decision.Outcome,
		})
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactProgressAssessment, "verifier", "planner", iteration, progress.Summary, map[string]any{
			"tool_call":                   progress.ToolCallID,
			"confidence_delta":            fmt.Sprintf("%.3f", progress.ConfidenceDelta),
			"evidence_gap_coverage_delta": strconv.Itoa(progress.EvidenceGapCoverageDelta),
			"uncertainty_delta":           fmt.Sprintf("%.3f", progress.UncertaintyDelta),
			"progress":                    strconv.FormatBool(progress.Progress),
			"verifier":                    verifier,
		})
		state.recordAdaptiveArtifact(ctx, WorkflowArtifactVerificationDelta, "verifier", "planner", iteration, firstNonEmpty(result.Summary, "adaptive evidence applied"), map[string]any{
			"tool":       string(candidate.Tool),
			"tool_call":  decision.ToolCallID,
			"confidence": fmt.Sprintf("%.2f", topHypothesisConfidence(state.hypotheses)),
		})
		if state.engine != nil && runtimeModeEnablesExperienceMemory(state.engine.cfg) && state.engine.toolExperience != nil {
			experience := state.engine.toolExperience.Observe(state.sceneClassification.SceneFamily, adaptiveObjective(state), adaptiveEvidenceGaps(state), candidate.Contract, progress, decision.NormalizedResult)
			state.recordAdaptiveArtifact(ctx, WorkflowArtifactExperienceMemoryUpdate, "memory", "planner", iteration, "tool experience memory updated", map[string]any{
				"tool":              string(candidate.Tool),
				"attempts":          strconv.Itoa(experience.Attempts),
				"progress_count":    strconv.Itoa(experience.ProgressCount),
				"plateau_count":     strconv.Itoa(experience.PlateauCount),
				"avg_gap_reduction": fmt.Sprintf("%.2f", experience.AvgGapReduction),
			})
		}
		if state.adaptiveState != nil {
			noProgressRounds = state.adaptiveState.NoProgressRounds
			plateauRounds = state.adaptiveState.UncertaintyPlateauRounds
		} else if progress.Progress {
			noProgressRounds = 0
		} else {
			noProgressRounds++
		}
		if state.adaptiveState == nil {
			if progress.Plateau {
				plateauRounds++
			} else {
				plateauRounds = 0
			}
		}

		if hypothesisRewrites >= r.cfg.AdaptiveMaxHypothesisRewrites {
			stopReason = string(AdaptiveStopReasonBudgetExhausted)
			break
		}
		if verifier.Directive == AdaptiveDirectiveStop && verifier.StopReason != "" {
			stopReason = string(verifier.StopReason)
			break
		}
		if state.adaptiveState != nil {
			if stop, reason := state.adaptiveState.shouldStop(r.cfg); stop {
				stopReason = reason
				break
			}
		} else {
			if noProgressRounds >= adaptiveNoProgressLimit(r.cfg) {
				stopReason = string(AdaptiveStopReasonNoProgress)
				break
			}
			if plateauRounds >= adaptivePlateauLimit(r.cfg) {
				stopReason = string(AdaptiveStopReasonUncertaintyPlateau)
				break
			}
		}
		if adaptiveConverged(state, r.cfg) {
			stopReason = string(AdaptiveStopReasonConfidenceSufficient)
			break
		}
	}

	if stopReason == "" {
		stopReason = string(AdaptiveStopReasonBudgetExhausted)
	}
	finalState := buildAdaptiveRuntimeState(state, r.cfg, maxInt(1, len(state.adaptiveToolDecisions)), toolCalls, hypothesisRewrites, noProgressRounds, plateauRounds, started, stopReason)
	finalState.HardStop = true
	state.recordAdaptiveState(ctx, finalState)
	if state.engine.telemetry != nil {
		state.engine.telemetry.recordAdaptiveStop(stopReason, r.cfg.RuntimeMode)
	}
	state.recordAdaptiveArtifact(ctx, WorkflowArtifactStopReason, "policy_gate", "workflow_runtime", finalState.Iteration, stopReason, map[string]any{
		"tool_calls": strconv.Itoa(toolCalls),
		"rewrites":   strconv.Itoa(hypothesisRewrites),
	})
	state.recordAdaptiveArtifact(ctx, WorkflowArtifactStopDecision, "policy_gate", "workflow_runtime", finalState.Iteration, stopReason, map[string]any{
		"stop_reason": stopReason,
		"hard_stop":   strconv.FormatBool(finalState.HardStop),
	})
	return nil
}

func buildAdaptiveRuntimeState(state *workflowState, cfg WorkflowConfig, iteration, toolCalls, rewrites, noProgressRounds, plateauRounds int, started time.Time, stopReason string) AdaptiveRuntimeState {
	now := time.Now().UTC()
	remainingTime := ""
	if cfg.AdaptiveTimeBudget > 0 {
		deadline := started.Add(cfg.AdaptiveTimeBudget)
		if now.Before(deadline) {
			remainingTime = deadline.Sub(now).String()
		} else {
			remainingTime = "0s"
		}
	}
	gaps := adaptiveEvidenceGaps(state)
	confidence := maxFloat(maxFloat(state.sceneClassification.Confidence, topHypothesisConfidence(state.hypotheses)), state.retrievalConfidence)
	return AdaptiveRuntimeState{
		SchemaVersion:          adaptiveRuntimeSchemaVersion,
		RunID:                  firstNonEmpty(state.workflowID, "run"),
		IncidentID:             firstNonEmpty(state.rca.IncidentID, state.incident.IncidentID, fmt.Sprintf("inc-%s", sanitizeID(firstNonEmpty(state.workflowID, "run")))),
		RuntimeMode:            cfg.RuntimeMode,
		Objective:              adaptiveObjective(state),
		Subgoals:               adaptiveSubgoals(state),
		CurrentSceneFamily:     state.sceneClassification.SceneFamily,
		CurrentSceneConfidence: state.sceneClassification.Confidence,
		EvidenceInventory:      adaptiveEvidenceInventory(state),
		UnresolvedEvidenceGaps: gaps,
		ActiveHypotheses:       append([]RCAHypothesis(nil), state.hypotheses...),
		CurrentHypotheses:      append([]RCAHypothesis(nil), state.hypotheses...),
		ContradictionSet:       adaptiveContradictions(state),
		ScopeHints:             append([]string(nil), state.adaptiveScopeHints...),
		SceneClassification:    state.sceneClassification,
		CurrentConfidence:      clamp01(confidence),
		ConfidenceScore:        clamp01(confidence),
		CurrentRisk:            maxFloat(state.risk.RiskScore, state.incident.Confidence),
		RiskScore:              maxFloat(state.risk.RiskScore, state.incident.Confidence),
		Budget: AdaptiveBudgetState{
			RemainingToolCalls:          maxInt(cfg.AdaptiveMaxToolCalls-toolCalls, 0),
			RemainingIterations:         maxInt(cfg.AdaptiveMaxIterations-iteration, 0),
			RemainingSameToolRetries:    cfg.AdaptiveMaxSameToolRetries,
			RemainingHypothesisRewrites: maxInt(cfg.AdaptiveMaxHypothesisRewrites-rewrites, 0),
			RemainingTokenBudget:        cfg.ReasoningTokenBudget,
			RemainingTimeBudget:         remainingTime,
		},
		RemainingToolBudget:      maxInt(cfg.AdaptiveMaxToolCalls-toolCalls, 0),
		RemainingTokenBudget:     cfg.ReasoningTokenBudget,
		RemainingTimeBudget:      remainingTime,
		ExecutionPosture:         firstNonEmpty(map[bool]string{true: "dry_run", false: "live"}[state.dryRun], "dry_run"),
		ApprovalStatus:           map[bool]string{true: "approval_required", false: "approval_optional"}[state.engine.cfg.RequireApproval],
		Iteration:                iteration,
		ToolCalls:                toolCalls,
		HypothesisRewrites:       rewrites,
		NoProgressRounds:         noProgressRounds,
		UncertaintyPlateauRounds: plateauRounds,
		ToolHistory:              adaptiveToolHistory(state),
		ToolYieldHistory:         adaptiveYieldHistory(state),
		LatestProgress:           latestAdaptiveProgress(state),
		HardStop:                 strings.TrimSpace(stopReason) != "",
		StopReason:               stopReason,
		Replay: AdaptiveReplayMetadata{
			Replayable:        true,
			ReplayPoint:       adaptiveRuntimeStage,
			RecoveryHint:      "rebuild candidates from persisted objective, gaps, contracts, and durable tool-call history",
			LastCheckpointAt:  now,
			CheckpointVersion: adaptiveRuntimeSchemaVersion,
		},
		UpdatedAt: now,
	}
}

func adaptiveObjective(state *workflowState) string {
	if state == nil {
		return "resolve incident uncertainty with bounded read-only evidence"
	}
	return firstNonEmpty(state.incident.Summary, state.trigger, "resolve incident uncertainty with bounded read-only evidence")
}

func adaptiveSubgoals(state *workflowState) []string {
	if state == nil {
		return nil
	}
	subgoals := []string{}
	subgoals = append(subgoals, adaptiveEvidenceGaps(state)...)
	if len(state.hypotheses) > 0 && topHypothesisConfidence(state.hypotheses) < 0.75 {
		subgoals = append(subgoals, "raise top hypothesis confidence")
	}
	if len(state.adaptiveScopeHints) > 0 {
		subgoals = append(subgoals, "refine scope to "+strings.Join(state.adaptiveScopeHints, ","))
	}
	if len(state.adaptiveNextToolFamilyHints) > 0 {
		subgoals = append(subgoals, "prepare next checks in "+strings.Join(state.adaptiveNextToolFamilyHints, ","))
	}
	subgoals = dedupeStrings(subgoals)
	if len(subgoals) > 6 {
		subgoals = subgoals[:6]
	}
	return subgoals
}

func adaptiveEvidenceInventory(state *workflowState) []AdaptiveEvidenceInventoryItem {
	if state == nil {
		return nil
	}
	items := make([]AdaptiveEvidenceInventoryItem, 0, 16)
	for _, evidence := range state.evidence {
		items = append(items, AdaptiveEvidenceInventoryItem{ID: evidence.ID, Kind: evidence.Kind, Source: evidence.Source, Summary: truncateString(evidence.Summary, 160)})
		if len(items) >= 10 {
			break
		}
	}
	for _, call := range state.toolCalls {
		items = append(items, AdaptiveEvidenceInventoryItem{ID: call.ID, Kind: "tool_call", Source: string(call.Tool), Summary: truncateString(firstNonEmpty(call.Summary, call.Status), 160)})
		if len(items) >= 16 {
			break
		}
	}
	return items
}

func adaptiveEvidenceGaps(state *workflowState) []string {
	if state == nil {
		return nil
	}
	gaps := []string{}
	gaps = append(gaps, state.evidenceGapState.MissingEvidence...)
	gaps = append(gaps, state.evidenceGapState.EvidenceGoalsStillUnmet...)
	gaps = append(gaps, state.sceneClassification.MissingEvidence...)
	gaps = append(gaps, state.telemetryQuality.BlindSpots...)
	if state.llmAnalysis != nil {
		gaps = append(gaps, state.llmAnalysis.Reasoning.MissingEvidence...)
		gaps = append(gaps, state.llmAnalysis.Reasoning.RecommendedNextChecks...)
	}
	if len(state.retrievedDocs) == 0 {
		gaps = append(gaps, "corroborating_knowledge")
	}
	if len(state.hypotheses) == 0 || topHypothesisConfidence(state.hypotheses) < 0.60 {
		gaps = append(gaps, "stable_ranked_hypothesis")
	}
	return dedupeStrings(gaps)
}

func adaptiveContradictions(state *workflowState) []string {
	out := []string{}
	for _, hypothesis := range state.hypotheses {
		out = append(out, hypothesis.ContradictingEvidenceIDs...)
	}
	out = append(out, state.validationReport.ContradictionSummary...)
	return dedupeStrings(out)
}

func rankAdaptiveToolCandidates(state *workflowState, cfg WorkflowConfig, sameToolCalls map[ToolName]int) []adaptiveToolCandidate {
	if state == nil || state.engine == nil || state.engine.tools == nil {
		return nil
	}
	selectionContext := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
	richCandidates := generateToolCandidates(state, selectionContext)
	candidates := make([]adaptiveToolCandidate, 0, len(richCandidates))
	for _, candidate := range richCandidates {
		if sameToolCalls[candidate.Tool] > cfg.AdaptiveMaxSameToolRetries {
			continue
		}
		candidates = append(candidates, adaptiveToolCandidate{
			Tool:       candidate.Tool,
			Contract:   candidate.LegacyContract,
			Query:      cloneStringMap(candidate.Query),
			Reason:     candidate.Reason,
			GapCovered: append([]string(nil), candidate.CoveredEvidenceGaps...),
			Score:      adaptiveScoreFromCandidateScore(candidate.Score),
			Source:     "rich_contract_ranker",
		})
	}
	return candidates
}

func scoreAdaptiveToolCandidate(state *workflowState, contract WorkflowToolContract, gaps []string) AdaptiveToolScore {
	gapCoverage := float64(len(adaptiveGapCoverage(contract, gaps))) / maxFloat(float64(maxInt(len(gaps), 1)), 1)
	scene := adaptiveSceneCompatibility(state.sceneClassification.SceneFamily, contract)
	relevance := clamp01(0.35 + gapCoverage*0.45 + scene*0.20)
	costPenalty := adaptiveCostPenalty(contract.CostClass)
	riskPenalty := 0.05
	if !contract.ReadOnly {
		riskPenalty = 0.75
	}
	policyEligibility := 0.25
	if contract.EligibleForAutoSelection && contract.ReadOnly {
		policyEligibility = 1.0
	}
	if !workflowToolContractAllowsStage(contract, adaptiveRuntimeStage) {
		policyEligibility = 0.0
	}
	freshnessFit := adaptiveFreshnessFit(state, contract)
	scopeFit := adaptiveScopeFit(state, contract)
	dependencyFit := adaptiveDependencyFit(state, contract, gaps)
	experiencePrior := adaptiveExperiencePrior(state, contract, gaps)
	cheapFirstBias := adaptiveCheapFirstBias(contract)
	repeatPenalty := adaptiveRepeatPenalty(state, contract)
	total := clamp01(
		relevance*0.20 +
			contract.ExpectedInformationGain*0.18 +
			scene*0.10 +
			gapCoverage*0.14 +
			policyEligibility*0.10 +
			freshnessFit*0.08 +
			scopeFit*0.08 +
			dependencyFit*0.05 +
			experiencePrior*0.12 +
			cheapFirstBias*0.05 +
			0.10 -
			costPenalty -
			riskPenalty*0.12 -
			repeatPenalty,
	)
	return AdaptiveToolScore{
		Relevance:          relevance,
		ExpectedInfoGain:   contract.ExpectedInformationGain,
		CostPenalty:        costPenalty,
		RiskPenalty:        riskPenalty,
		Availability:       1,
		SceneCompatibility: scene,
		GapCoverage:        gapCoverage,
		PolicyEligibility:  policyEligibility,
		FreshnessFit:       freshnessFit,
		ScopeFit:           scopeFit,
		DependencyFit:      dependencyFit,
		ExperiencePrior:    experiencePrior,
		CheapFirstBias:     cheapFirstBias,
		RepeatPenalty:      repeatPenalty,
		Total:              total,
	}
}

func adaptiveFreshnessFit(state *workflowState, contract WorkflowToolContract) float64 {
	switch contract.FreshnessSensitivity {
	case "high":
		if len(state.toolCalls) == 0 {
			return 1
		}
		last := state.toolCalls[len(state.toolCalls)-1]
		if time.Since(last.CompletedAt) > 2*time.Minute {
			return 1
		}
		return 0.6
	case "medium":
		return 0.75
	default:
		return 0.55
	}
}

func adaptiveScopeFit(state *workflowState, contract WorkflowToolContract) float64 {
	scopeHints := strings.ToLower(strings.Join(state.adaptiveScopeHints, " "))
	switch contract.ScopeSensitivity {
	case "collector_or_service":
		if strings.TrimSpace(state.collectorID) != "" || strings.Contains(scopeHints, "service") {
			return 1
		}
	case "workload_or_revision":
		if strings.Contains(scopeHints, "service") || strings.Contains(scopeHints, "pod") || strings.Contains(scopeHints, "revision") {
			return 1
		}
	case "topology_or_graph":
		if strings.Contains(scopeHints, "service") || len(state.topoData.Snapshot.Nodes) > 0 {
			return 0.9
		}
	case "objective_or_evidence_gap":
		return 0.8
	case "explicit_target_scope_required":
		if len(state.adaptiveScopeHints) > 0 {
			return 0.9
		}
		return 0.3
	default:
		if strings.TrimSpace(state.collectorID) != "" {
			return 0.8
		}
	}
	return 0.45
}

func adaptiveDependencyFit(state *workflowState, contract WorkflowToolContract, gaps []string) float64 {
	if len(state.adaptiveNextToolFamilyHints) > 0 {
		for _, family := range state.adaptiveNextToolFamilyHints {
			if strings.EqualFold(strings.TrimSpace(family), strings.TrimSpace(contract.CapabilityFamily)) {
				return 1
			}
		}
	}
	if len(gaps) == 0 {
		return 0.5
	}
	return 0.65
}

func adaptiveExperiencePrior(state *workflowState, contract WorkflowToolContract, gaps []string) float64 {
	if state == nil || state.engine == nil || state.engine.toolExperience == nil {
		return 0
	}
	return state.engine.toolExperience.Prior(state.sceneClassification.SceneFamily, adaptiveObjective(state), gaps, contract)
}

func adaptiveCheapFirstBias(contract WorkflowToolContract) float64 {
	if !contract.ReadOnly {
		return -0.10
	}
	switch contract.CostClass {
	case "low":
		return 1
	case "medium":
		return 0.65
	default:
		return 0.20
	}
}

func adaptiveRepeatPenalty(state *workflowState, contract WorkflowToolContract) float64 {
	if state == nil {
		return 0
	}
	count := 0
	for i := len(state.toolCalls) - 1; i >= 0; i-- {
		if state.toolCalls[i].Tool != contract.ToolName {
			continue
		}
		count++
	}
	if count == 0 {
		return 0
	}
	return minFloat(0.12, 0.03*float64(count))
}

func adaptiveGapCoverage(contract WorkflowToolContract, gaps []string) []string {
	covered := []string{}
	needle := strings.ToLower(strings.Join(append(append([]string{}, contract.EvidenceProduced...), contract.Purpose, contract.CapabilityFamily), " "))
	for _, gap := range gaps {
		words := strings.Fields(strings.ToLower(strings.ReplaceAll(gap, "_", " ")))
		for _, word := range words {
			if len(word) < 4 {
				continue
			}
			if strings.Contains(needle, word) {
				covered = append(covered, gap)
				break
			}
		}
	}
	return dedupeStrings(covered)
}

func adaptiveSceneCompatibility(scene SceneFamily, contract WorkflowToolContract) float64 {
	family := strings.ToLower(contract.CapabilityFamily)
	switch scene {
	case SceneFamilyChangeInduced, SceneFamilyDeploymentRollout:
		if family == "change" || family == "logs" || family == "kubernetes" {
			return 1
		}
	case SceneFamilyNetworkConnectivity:
		if family == "service_health" || family == "topology" || strings.Contains(strings.Join(contract.EvidenceProduced, ","), "network") {
			return 1
		}
	case SceneFamilyStorageIO:
		if strings.Contains(strings.Join(contract.EvidenceProduced, ","), "storage") || family == "telemetry" {
			return 1
		}
	case SceneFamilyGPUInference, SceneFamilyGPUTrainingOrCollective:
		if strings.Contains(strings.Join(contract.EvidenceProduced, ","), "gpu") || family == "telemetry" {
			return 1
		}
	case SceneFamilySecurityOrProcessAnomaly:
		if family == "runtime_security" {
			return 1
		}
	}
	if contract.ReadOnly {
		return 0.45
	}
	return 0.10
}

func adaptiveCostPenalty(cost string) float64 {
	switch strings.ToLower(strings.TrimSpace(cost)) {
	case "high":
		return 0.22
	case "medium":
		return 0.12
	default:
		return 0.04
	}
}

func adaptiveQueryForTool(state *workflowState, contract WorkflowToolContract, gaps []string) map[string]string {
	cfg := WorkflowConfig{}
	if state != nil && state.engine != nil {
		cfg = state.engine.cfg
	}
	shaped := shapeToolQuery(state, toolContractFromLegacy(contract), ToolSelectionContext{
		Stage:               adaptiveRuntimeStage,
		Objective:           adaptiveObjective(state),
		IncidentSummary:     adaptiveObjective(state),
		SceneFamily:         state.sceneClassification.SceneFamily,
		CollectorID:         state.collectorID,
		Window:              state.window,
		EvidenceGaps:        append([]string(nil), gaps...),
		Contradictions:      adaptiveContradictions(state),
		ScopeHints:          append([]string(nil), state.adaptiveScopeHints...),
		PreferredFamilies:   append([]string(nil), state.adaptiveNextToolFamilyHints...),
		ReadOnlyOnly:        contract.ReadOnly,
		AutonomousSelection: runtimeModeEnablesAutonomousToolSelection(cfg),
	})
	return shaped.Query
}

func adaptiveQueryScope(state *workflowState, contract WorkflowToolContract) string {
	if len(state.adaptiveScopeHints) > 0 {
		return strings.Join(state.adaptiveScopeHints, ",")
	}
	switch contract.ScopeSensitivity {
	case "workload_or_revision":
		if impacted := impactedScopeFromState(state); len(impacted) > 0 {
			return strings.Join(impacted, ",")
		}
	case "topology_or_graph":
		if len(state.topoData.Snapshot.Nodes) > 0 {
			return strings.Join(compactStrings(state.collectorID, state.incident.Summary), ",")
		}
	}
	return firstNonEmpty(state.collectorID, "fleet")
}

func adaptiveQueryWindow(state *workflowState, contract WorkflowToolContract) string {
	base := state.window
	if base <= 0 {
		base = 30 * time.Minute
	}
	switch contract.FreshnessSensitivity {
	case "high":
		base = maxDuration(5*time.Minute, base/2)
	case "medium":
		base = maxDuration(10*time.Minute, base*2/3)
	default:
		base = minDuration(90*time.Minute, base)
	}
	return base.String()
}

func adaptiveToolReason(contract WorkflowToolContract, gaps []string) string {
	covered := adaptiveGapCoverage(contract, gaps)
	if len(covered) > 0 {
		return fmt.Sprintf("%s can reduce uncertainty for %s", contract.ToolName, strings.Join(covered, ", "))
	}
	return fmt.Sprintf("%s provides %s evidence for the current objective", contract.ToolName, contract.CapabilityFamily)
}

func buildAdaptiveToolDecision(state *workflowState, candidate adaptiveToolCandidate, iteration int) AdaptiveToolDecision {
	return AdaptiveToolDecision{
		DecisionID:         fmt.Sprintf("adaptive-decision-%s-%02d-%s", sanitizeID(state.workflowID), iteration, sanitizeID(string(candidate.Tool))),
		SchemaVersion:      adaptiveRuntimeSchemaVersion,
		Iteration:          iteration,
		Tool:               candidate.Tool,
		ToolContract:       workflowToolContractID(candidate.Contract),
		CapabilityFamily:   candidate.Contract.CapabilityFamily,
		Reason:             candidate.Reason,
		EvidenceGapCovered: append([]string(nil), candidate.GapCovered...),
		ExpectedEvidence:   append([]string(nil), candidate.Contract.EvidenceProduced...),
		Query:              cloneStringMap(candidate.Query),
		Score:              candidate.Score,
		PlannerRole:        "planner",
		CriticRole:         "critic",
		ControllerGate:     "workflowToolManager.call",
		CreatedAt:          time.Now().UTC(),
	}
}

func evaluateAdaptiveCandidatePolicy(state *workflowState, candidate adaptiveToolCandidate) ActionPolicyDecision {
	if state == nil || state.engine == nil || state.engine.tools == nil {
		return ActionPolicyDecision{Status: "blocked", Reason: "tool manager unavailable"}
	}
	tool := state.engine.tools.tools[candidate.Tool]
	if tool == nil {
		return ActionPolicyDecision{Status: "blocked", Reason: "tool not registered"}
	}
	if state.engine.tools.policy != nil {
		state.engine.tools.policy.cfg = state.engine.cfg
		return state.engine.tools.policy.Evaluate(workflowToolRequest{
			WorkflowID:  state.workflowID,
			Workflow:    state.workflowType,
			Stage:       adaptiveRuntimeStage,
			Actor:       "adaptive_policy_gate",
			CollectorID: state.collectorID,
			Window:      state.window,
			Limit:       state.limit,
			Query:       candidate.Query,
			DryRun:      state.dryRun,
		}, tool)
	}
	return ActionPolicyDecision{Status: "allowed", Reason: "policy engine unavailable for read-only fallback", ExecutionEligible: candidate.Contract.ReadOnly}
}

func adaptiveExecutionEligibility(candidate adaptiveToolCandidate, policy ActionPolicyDecision, sameToolCalls int, cfg WorkflowConfig) (bool, bool, string) {
	if sameToolCalls > cfg.AdaptiveMaxSameToolRetries {
		return false, true, "same-tool adaptive retry budget exhausted"
	}
	if !candidate.Contract.EligibleForAutoSelection {
		return false, true, "tool contract disallows automatic model selection"
	}
	if !candidate.Contract.ReadOnly {
		return false, true, "state-changing tools cannot run from adaptive auto-selection"
	}
	if policy.Status != "" && policy.Status != "allowed" {
		return false, policy.ProposalOnly || policy.RequiresApproval, firstNonEmpty(policy.Reason, "policy did not allow execution")
	}
	if policy.RequiresApproval || policy.DryRunRequired && !candidate.Contract.ReadOnly {
		return false, true, firstNonEmpty(policy.Reason, "approval or dry-run gate required")
	}
	return true, false, ""
}

func adaptiveCritiqueSummary(candidate adaptiveToolCandidate, decision AdaptiveToolDecision) string {
	if decision.BlockedReason != "" {
		return fmt.Sprintf("critic rejected %s for automatic execution: %s", candidate.Tool, decision.BlockedReason)
	}
	if len(candidate.GapCovered) == 0 {
		return fmt.Sprintf("critic allowed %s but noted weak explicit gap coverage", candidate.Tool)
	}
	return fmt.Sprintf("critic allowed %s because it is read-only, contract-eligible, and covers %s", candidate.Tool, strings.Join(candidate.GapCovered, ", "))
}

func adaptiveConverged(state *workflowState, cfg WorkflowConfig) bool {
	threshold := cfg.RefineConfidenceThreshold
	if threshold <= 0 {
		threshold = 0.70
	}
	return topHypothesisConfidence(state.hypotheses) >= threshold && len(adaptiveEvidenceGaps(state)) == 0
}

func adaptiveCandidateScorePayload(candidates []adaptiveToolCandidate) map[string]any {
	type row struct {
		Tool             string   `json:"tool"`
		CapabilityFamily string   `json:"capability_family"`
		Reason           string   `json:"reason"`
		GapCovered       []string `json:"gap_covered,omitempty"`
		Total            float64  `json:"total"`
		InfoGain         float64  `json:"expected_information_gain"`
		ExperiencePrior  float64  `json:"experience_prior"`
		FreshnessFit     float64  `json:"freshness_fit"`
		ScopeFit         float64  `json:"scope_fit"`
		CheapFirstBias   float64  `json:"cheap_first_bias"`
		RepeatPenalty    float64  `json:"repeat_penalty"`
	}
	out := make([]row, 0, minInt(len(candidates), 5))
	for _, candidate := range candidates {
		out = append(out, row{
			Tool:             string(candidate.Tool),
			CapabilityFamily: candidate.Contract.CapabilityFamily,
			Reason:           candidate.Reason,
			GapCovered:       append([]string(nil), candidate.GapCovered...),
			Total:            candidate.Score.Total,
			InfoGain:         candidate.Score.ExpectedInfoGain,
			ExperiencePrior:  candidate.Score.ExperiencePrior,
			FreshnessFit:     candidate.Score.FreshnessFit,
			ScopeFit:         candidate.Score.ScopeFit,
			CheapFirstBias:   candidate.Score.CheapFirstBias,
			RepeatPenalty:    candidate.Score.RepeatPenalty,
		})
		if len(out) >= 5 {
			break
		}
	}
	return map[string]any{"candidates": out}
}

func adaptiveBranch(decision AdaptiveToolDecision) string {
	switch {
	case decision.Executable:
		return "execute_tool"
	case decision.ProposalOnly:
		return "proposal_only"
	case decision.BlockedReason != "":
		return "blocked"
	default:
		return "skip"
	}
}

func adaptiveBranchSummary(decision AdaptiveToolDecision) string {
	branch := adaptiveBranch(decision)
	if decision.BlockedReason != "" {
		return fmt.Sprintf("%s: %s", branch, decision.BlockedReason)
	}
	return fmt.Sprintf("%s: %s", branch, decision.Tool)
}

func adaptiveStopReasonForDecision(decision AdaptiveToolDecision) AdaptiveStopReason {
	switch {
	case decision.Policy.RequiresApproval:
		return AdaptiveStopReasonApprovalRequired
	case decision.Policy.Status == "blocked":
		return AdaptiveStopReasonPolicyBlocked
	case strings.Contains(strings.ToLower(decision.BlockedReason), "approval"):
		return AdaptiveStopReasonApprovalRequired
	case strings.Contains(strings.ToLower(decision.BlockedReason), "policy"):
		return AdaptiveStopReasonPolicyBlocked
	case strings.Contains(strings.ToLower(decision.BlockedReason), "retry"):
		return AdaptiveStopReasonNoProgress
	default:
		return AdaptiveStopReasonNoSafeNextStep
	}
}

func buildAdaptiveProgressAssessment(before, after AdaptiveRuntimeState, toolCallID string) AdaptiveProgressAssessment {
	uncertaintyBefore := clamp01(1 - before.ConfidenceScore)
	uncertaintyAfter := clamp01(1 - after.ConfidenceScore)
	assessment := AdaptiveProgressAssessment{
		SchemaVersion:            adaptiveRuntimeSchemaVersion,
		ToolCallID:               strings.TrimSpace(toolCallID),
		UncertaintyBefore:        uncertaintyBefore,
		UncertaintyAfter:         uncertaintyAfter,
		UncertaintyDelta:         uncertaintyAfter - uncertaintyBefore,
		ConfidenceBefore:         before.ConfidenceScore,
		ConfidenceAfter:          after.ConfidenceScore,
		ConfidenceDelta:          after.ConfidenceScore - before.ConfidenceScore,
		ContradictionsBefore:     len(before.ContradictionSet),
		ContradictionsAfter:      len(after.ContradictionSet),
		ContradictionDelta:       len(after.ContradictionSet) - len(before.ContradictionSet),
		EvidenceGapsBefore:       len(before.UnresolvedEvidenceGaps),
		EvidenceGapsAfter:        len(after.UnresolvedEvidenceGaps),
		EvidenceGapCoverageDelta: len(before.UnresolvedEvidenceGaps) - len(after.UnresolvedEvidenceGaps),
		RiskBefore:               before.RiskScore,
		RiskAfter:                after.RiskScore,
		RiskDelta:                after.RiskScore - before.RiskScore,
	}
	assessment.ActionEffectDelta = -assessment.RiskDelta
	assessment.Progress = assessment.ConfidenceDelta > 0.001 ||
		assessment.UncertaintyDelta < -0.001 ||
		assessment.EvidenceGapCoverageDelta > 0 ||
		assessment.ContradictionDelta < 0 ||
		assessment.ActionEffectDelta > 0.001
	assessment.Plateau = !assessment.Progress
	if assessment.Progress {
		assessment.Summary = "adaptive step reduced uncertainty or improved evidence posture"
	} else {
		assessment.Summary = "adaptive step produced no measurable progress"
	}
	return assessment
}

func normalizeToolResult(tool ToolName, result workflowToolResult, toolCallID string, state *workflowState) *NormalizedToolResult {
	out := &NormalizedToolResult{
		SchemaVersion: workflowToolContractSchemaVersion,
		Tool:          tool,
		ToolCallID:    toolCallID,
		Summary:       truncateString(firstNonEmpty(result.Summary, "tool result summarized"), 220),
		Freshness:     "recent",
		ResultQuality: "medium",
		Cacheability:  "bounded_ttl",
	}
	switch data := result.Data.(type) {
	case metricsToolData:
		out.StructuredFindings = compactStrings(fmt.Sprintf("history_samples=%d", len(data.History)))
		out.ConfidenceContribution = 0.08
		out.AffectedScope = compactStrings(firstNonEmpty(data.CollectorID, state.collectorID))
		out.LikelyNextToolFamilies = []string{"service_health", "logs"}
		out.LikelyNextChecks = []string{"compare top metrics against recent baseline", "narrow to the highest-delta entity"}
		out.NarrowsHypothesisSpace = len(data.History) > 0
	case logsToolData:
		out.StructuredFindings = compactStrings(fmt.Sprintf("errors=%d", data.Errors), fmt.Sprintf("warnings=%d", data.Warnings))
		out.ConfidenceContribution = 0.07
		out.AffectedScope = compactStrings(state.collectorID)
		out.LikelyNextToolFamilies = []string{"change", "service_health"}
		out.LikelyNextChecks = compactStrings("filter logs to rollout or timeout keywords", "confirm affected workload scope")
		out.NarrowsHypothesisSpace = len(data.Snippets) > 0
	case changeToolData:
		out.StructuredFindings = compactStrings(data.Summary)
		out.ConfidenceContribution = 0.09
		out.AffectedScope = compactStrings(data.CollectorID)
		out.LikelyNextToolFamilies = []string{"kubernetes", "service_health"}
		out.LikelyNextChecks = compactStrings("verify rollout adjacency against impacted service", "compare revision and config drift")
		out.NarrowsHypothesisSpace = len(data.Events) > 0
	case topologyToolData:
		out.StructuredFindings = compactStrings(data.Snapshot.Summary)
		out.ContradictionContribution = -0.04
		out.AffectedScope = compactStrings(state.collectorID)
		out.LikelyNextToolFamilies = []string{"service_health", "network"}
		out.LikelyNextChecks = compactStrings("limit scope to one-hop neighbors", "confirm blast radius before remediation")
	case securityToolData:
		out.StructuredFindings = append([]string(nil), data.Findings...)
		out.ConfidenceContribution = 0.06
		out.ContradictionContribution = 0.10
		out.AffectedScope = compactStrings(state.collectorID)
		out.LikelyNextToolFamilies = []string{"runtime_security", "topology"}
		out.LikelyNextChecks = compactStrings("validate suspicious target and process lineage", "compare against policy baseline")
		out.NarrowsHypothesisSpace = len(data.Findings) > 0
	case ebpfToolData:
		out.StructuredFindings = compactStrings(fmt.Sprintf("runtime_events=%d", len(data.RuntimeEvents)))
		out.ConfidenceContribution = 0.07
		out.ContradictionContribution = 0.08
		out.AffectedScope = compactStrings(state.collectorID)
		out.LikelyNextToolFamilies = []string{"runtime_security", "logs"}
		out.LikelyNextChecks = compactStrings("confirm syscall or runtime-event anomaly", "cross-check affected process lineage")
		out.NarrowsHypothesisSpace = len(data.RuntimeEvents) > 0
	case knowledgeToolData:
		out.StructuredFindings = compactStrings(data.Summary)
		out.ConfidenceContribution = minFloat(0.12, data.Confidence)
		out.EvidenceIDs = append([]string(nil), data.EvidenceIDs...)
		out.AffectedScope = compactStrings(state.collectorID)
		out.LikelyNextToolFamilies = []string{"metrics", "service_health", "change"}
		out.LikelyNextChecks = compactStrings("compare current evidence against retrieved prior case", "reuse verified action outcomes if present")
		out.NarrowsHypothesisSpace = len(data.Hits) > 0
	case connectivityCheckToolData:
		out.StructuredFindings = compactStrings(data.Summary)
		out.ConfidenceContribution = 0.08
		out.AffectedScope = compactStrings(data.CollectorID)
		out.LikelyNextToolFamilies = []string{"service_health", "topology"}
		out.LikelyNextChecks = compactStrings("validate retransmits against impacted downstream", "confirm DNS if healthy=false")
		out.NarrowsHypothesisSpace = true
	case dnsCheckToolData:
		out.StructuredFindings = append([]string(nil), data.Hints...)
		out.ConfidenceContribution = 0.08
		out.AffectedScope = compactStrings(data.CollectorID)
		out.LikelyNextToolFamilies = []string{"service_health", "logs"}
		out.LikelyNextChecks = compactStrings("search for resolver errors in logs", "compare with dependency health")
		out.NarrowsHypothesisSpace = len(data.Hints) > 0
	case serviceHealthToolData:
		out.StructuredFindings = compactStrings(data.Summary)
		out.ConfidenceContribution = 0.09
		out.AffectedScope = compactStrings(data.CollectorID)
		out.LikelyNextToolFamilies = []string{"metrics", "change"}
		out.LikelyNextChecks = compactStrings("compare service latency/error rate before remediation", "verify impacted scope")
		out.NarrowsHypothesisSpace = true
	case memoryPressureToolData:
		out.StructuredFindings = append([]string(nil), data.PressureSignals...)
		out.ConfidenceContribution = 0.09
		out.AffectedScope = compactStrings(data.CollectorID)
		out.LikelyNextToolFamilies = []string{"process", "service_health"}
		out.LikelyNextChecks = compactStrings("identify top RSS offenders", "validate reclaim or OOM pattern")
		out.NarrowsHypothesisSpace = len(data.PressureSignals) > 0
	case storageHealthToolData:
		out.StructuredFindings = compactStrings(data.Summary)
		out.ConfidenceContribution = 0.09
		out.AffectedScope = compactStrings(data.CollectorID)
		out.LikelyNextToolFamilies = []string{"metrics", "change"}
		out.LikelyNextChecks = compactStrings("validate IO pressure against rollout or config change", "scope to affected filesystem")
		out.NarrowsHypothesisSpace = data.Pressure
	case gpuToolData:
		out.StructuredFindings = compactStrings(data.Summary)
		out.ConfidenceContribution = 0.09
		out.AffectedScope = compactStrings(state.collectorID)
		out.LikelyNextToolFamilies = []string{"process", "logs"}
		out.LikelyNextChecks = compactStrings("confirm top GPU process attribution", "compare collective vs inference signals")
		out.NarrowsHypothesisSpace = len(data.Metrics) > 0
	case kubernetesResourceToolData:
		out.StructuredFindings = compactStrings(data.Summary, data.Service, data.Workload, data.PodUID)
		out.ConfidenceContribution = 0.06
		out.AffectedScope = compactStrings(data.Namespace, data.Service, data.Workload)
		out.LikelyNextToolFamilies = []string{"change", "service_health"}
		out.LikelyNextChecks = compactStrings("bind tool scope to workload or revision", "confirm pod or service identity")
		out.NarrowsHypothesisSpace = true
	case containerRevisionToolData:
		out.StructuredFindings = compactStrings(data.Summary, data.Revision, data.Image)
		out.ConfidenceContribution = 0.07
		out.AffectedScope = compactStrings(data.Service, data.Revision)
		out.LikelyNextToolFamilies = []string{"change", "kubernetes"}
		out.LikelyNextChecks = compactStrings("compare image or revision drift", "confirm rollout adjacency")
		out.NarrowsHypothesisSpace = strings.TrimSpace(data.Revision) != ""
	case networkBlastRadiusToolData:
		out.StructuredFindings = compactStrings(data.Summary)
		out.ContradictionContribution = -0.05
		out.AffectedScope = append([]string(nil), data.Scope...)
		out.LikelyNextToolFamilies = []string{"service_health", "remediation"}
		out.LikelyNextChecks = compactStrings("limit proposed mitigation to blast radius", "verify downstream scope before action")
		out.NarrowsHypothesisSpace = true
	case remediationToolData:
		out.StructuredFindings = compactStrings(data.Summary, data.Mode, data.RollbackPlan)
		out.RemediationEligibilityDelta = map[string]float64{"proposal_only": -0.10, "planned_only": 0.0, "dry_run": 0.05, "executed": 0.15}[strings.TrimSpace(data.Mode)]
		out.AffectedScope = compactStrings(firstNonEmpty(data.Contract.Target.Scope, data.Contract.TargetScope, state.collectorID))
		out.LikelyNextToolFamilies = []string{"verification", "service_health"}
		out.LikelyNextChecks = compactStrings("capture before/after health", "verify rollback readiness")
	default:
		out.StructuredFindings = compactStrings(out.Summary)
		out.AffectedScope = compactStrings(state.collectorID)
		out.LikelyNextChecks = compactStrings("review result summary before selecting the next tool")
	}
	out.EvidenceIDs = dedupeStrings(append(out.EvidenceIDs, compactStrings(toolCallID)...))
	out.StructuredFindings = dedupeStrings(out.StructuredFindings)
	out.AffectedScope = dedupeStrings(out.AffectedScope)
	out.LikelyNextToolFamilies = dedupeStrings(out.LikelyNextToolFamilies)
	out.LikelyNextChecks = dedupeStrings(out.LikelyNextChecks)
	out.HypothesisSpaceNarrowed = out.NarrowsHypothesisSpace
	return finalizeNormalizedToolResult(state, out)
}

func latestAdaptiveProgress(state *workflowState) *AdaptiveProgressAssessment {
	if state == nil {
		return nil
	}
	for idx := len(state.adaptiveToolDecisions) - 1; idx >= 0; idx-- {
		if state.adaptiveToolDecisions[idx].Progress != nil {
			copy := *state.adaptiveToolDecisions[idx].Progress
			return &copy
		}
	}
	return nil
}

func (state *workflowState) applyNormalizedToolResult(result *NormalizedToolResult) {
	if state == nil || result == nil {
		return
	}
	state.adaptiveNormalizedResults = append(state.adaptiveNormalizedResults, *result)
	state.adaptiveScopeHints = dedupeStrings(append(state.adaptiveScopeHints, result.AffectedScope...))
	state.adaptiveScopeHints = dedupeStrings(append(state.adaptiveScopeHints, result.RecommendedScopeRefinement...))
	state.adaptiveNextToolFamilyHints = dedupeStrings(append(state.adaptiveNextToolFamilyHints, result.LikelyNextToolFamilies...))
}

func (state *workflowState) recordModelDirectedToolDecision(ctx context.Context, request LLMToolRequest, round int) (bool, string) {
	if state == nil || state.engine == nil || state.engine.tools == nil {
		return false, "adaptive runtime unavailable"
	}
	desc, ok := workflowToolDescriptorByName(state.engine.tools.registry(), request.Tool)
	if !ok {
		return false, "tool is not registered"
	}
	contract := desc.Contract
	if err := validateWorkflowToolContract(contract); err != nil {
		return false, err.Error()
	}
	candidate := adaptiveToolCandidate{
		Tool:       request.Tool,
		Contract:   contract,
		Query:      cloneStringMap(request.Query),
		Reason:     firstNonEmpty(request.Reason, fmt.Sprintf("model requested %s", request.Tool)),
		GapCovered: adaptiveGapCoverage(contract, adaptiveEvidenceGaps(state)),
		Score:      scoreAdaptiveToolCandidate(state, contract, adaptiveEvidenceGaps(state)),
		Source:     "llm_tool_request",
	}
	decision := buildAdaptiveToolDecision(state, candidate, maxInt(round, 1))
	decision.DecisionID = fmt.Sprintf("adaptive-model-decision-%s-%02d-%s", sanitizeID(state.workflowID), maxInt(round, 1), sanitizeID(string(request.Tool)))
	decision.Policy = evaluateAdaptiveCandidatePolicy(state, candidate)
	decision.Executable, decision.ProposalOnly, decision.BlockedReason = adaptiveExecutionEligibility(candidate, decision.Policy, 0, state.engine.cfg)
	decision.AutoSelected = decision.Executable
	state.recordAdaptiveToolDecision(ctx, decision)
	state.recordAdaptiveDialogue(ctx, AdaptiveDialogueTurn{
		TurnID:         fmt.Sprintf("adaptive-model-%s-%02d-%s", sanitizeID(state.workflowID), maxInt(round, 1), sanitizeID(string(request.Tool))),
		Iteration:      maxInt(round, 1),
		Role:           "planner",
		Producer:       "llm_analysis",
		Consumer:       "policy_gate",
		Summary:        fmt.Sprintf("model requested %s: %s", request.Tool, firstNonEmpty(request.Reason, "no reason supplied")),
		Inputs:         adaptiveEvidenceGaps(state),
		Outputs:        compactStrings(decision.DecisionID, decision.BlockedReason),
		ToolDecisionID: decision.DecisionID,
		CreatedAt:      time.Now().UTC(),
	})
	state.recordAdaptiveArtifact(ctx, WorkflowArtifactToolDecision, "llm_analysis", "policy_gate", maxInt(round, 1), decision.Reason, map[string]any{
		"decision_id": decision.DecisionID,
		"tool":        string(decision.Tool),
		"source":      "llm_tool_request",
		"blocked":     decision.BlockedReason,
	})
	if !decision.Executable {
		return false, firstNonEmpty(decision.BlockedReason, "model-requested tool failed contract or policy eligibility")
	}
	return true, ""
}

func workflowToolDescriptorByName(descriptors []WorkflowToolDescriptor, name ToolName) (WorkflowToolDescriptor, bool) {
	for _, desc := range descriptors {
		if desc.Name == name {
			return desc, true
		}
	}
	return WorkflowToolDescriptor{}, false
}

func (state *workflowState) recordAdaptiveState(ctx context.Context, snapshot AdaptiveRuntimeState) {
	if state == nil {
		return
	}
	copy := snapshot
	state.adaptiveState = &copy
	if state.durableRun != nil {
		state.durableRun.AdaptiveRuntime = &copy
	}
	if state.engine != nil && state.engine.orchestrator != nil && state.durableRun != nil {
		_ = state.engine.orchestrator.RecordAdaptiveState(ctx, state.workflowID, copy)
	}
}

func (state *workflowState) recordAdaptiveDialogue(ctx context.Context, turn AdaptiveDialogueTurn) {
	if state == nil {
		return
	}
	state.adaptiveDialogue = append(state.adaptiveDialogue, turn)
	if state.durableRun != nil {
		state.durableRun.AdaptiveDialogue = append(state.durableRun.AdaptiveDialogue, turn)
	}
	if state.engine != nil && state.engine.orchestrator != nil && state.durableRun != nil {
		_ = state.engine.orchestrator.AppendAdaptiveDialogue(ctx, state.workflowID, turn)
	}
}

func (state *workflowState) recordAdaptiveToolDecision(ctx context.Context, decision AdaptiveToolDecision) {
	if state == nil {
		return
	}
	state.adaptiveToolDecisions = append(state.adaptiveToolDecisions, decision)
	if state.durableRun != nil {
		state.durableRun.AdaptiveToolDecisions = append(state.durableRun.AdaptiveToolDecisions, decision)
	}
	if state.engine != nil && state.engine.orchestrator != nil && state.durableRun != nil {
		_ = state.engine.orchestrator.AppendAdaptiveToolDecision(ctx, state.workflowID, decision)
	}
}

func (state *workflowState) replaceAdaptiveToolDecision(ctx context.Context, decision AdaptiveToolDecision) {
	if state == nil {
		return
	}
	for idx := range state.adaptiveToolDecisions {
		if state.adaptiveToolDecisions[idx].DecisionID == decision.DecisionID {
			state.adaptiveToolDecisions[idx] = decision
			if state.engine != nil && state.engine.orchestrator != nil && state.durableRun != nil {
				_ = state.engine.orchestrator.ReplaceAdaptiveToolDecision(ctx, state.workflowID, decision)
			}
			return
		}
	}
	state.recordAdaptiveToolDecision(ctx, decision)
}

func (state *workflowState) recordAdaptiveArtifact(ctx context.Context, kind WorkflowArtifactKind, producer, consumer string, iteration int, summary string, payload map[string]any) AdaptiveArtifact {
	inputArtifacts := []string{}
	if len(state.adaptiveArtifacts) > 0 {
		inputArtifacts = append(inputArtifacts, state.adaptiveArtifacts[len(state.adaptiveArtifacts)-1].ArtifactID)
	}
	replayable := kind != WorkflowArtifactExecutionIntent
	replaySemantics := "replay from adaptive runtime state, durable tool calls, and tool contracts"
	if !replayable {
		replaySemantics = "intent_only"
	}
	artifact := AdaptiveArtifact{
		SchemaVersion:   adaptiveRuntimeSchemaVersion,
		Version:         "v1",
		Kind:            kind,
		ArtifactID:      fmt.Sprintf("%s-%s-%02d-v1", sanitizeID(firstNonEmpty(state.workflowID, "run")), sanitizeID(string(kind)), maxInt(iteration, 1)),
		RunID:           firstNonEmpty(state.workflowID, "run"),
		IncidentID:      fmt.Sprintf("inc-%s", sanitizeID(firstNonEmpty(state.workflowID, "run"))),
		CorrelationID:   firstNonEmpty(state.workflowID, "run"),
		Producer:        producer,
		Consumer:        consumer,
		Status:          "recorded",
		Iteration:       iteration,
		InputArtifacts:  inputArtifacts,
		Replayable:      replayable,
		ReplaySemantics: replaySemantics,
		Summary:         truncateString(summary, 220),
		Payload:         payload,
		ProducedAt:      time.Now().UTC(),
	}
	state.adaptiveArtifacts = append(state.adaptiveArtifacts, artifact)
	if state.durableRun != nil {
		state.durableRun.AdaptiveArtifacts = append(state.durableRun.AdaptiveArtifacts, artifact)
	}
	if state.engine != nil && state.engine.orchestrator != nil && state.durableRun != nil {
		_ = state.engine.orchestrator.AppendAdaptiveArtifact(ctx, state.workflowID, artifact)
	}
	return artifact
}

func (o *DurableOrchestrator) RecordAdaptiveState(ctx context.Context, runID string, snapshot AdaptiveRuntimeState) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		copy := snapshot
		run.AdaptiveRuntime = &copy
	})
}

func (o *DurableOrchestrator) AppendAdaptiveDialogue(ctx context.Context, runID string, turn AdaptiveDialogueTurn) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.AdaptiveDialogue = append(run.AdaptiveDialogue, turn)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "adaptive_dialogue_recorded", map[string]any{
		"turn_id":   turn.TurnID,
		"iteration": turn.Iteration,
		"role":      turn.Role,
		"summary":   turn.Summary,
	})
}

func (o *DurableOrchestrator) AppendAdaptiveToolDecision(ctx context.Context, runID string, decision AdaptiveToolDecision) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.AdaptiveToolDecisions = append(run.AdaptiveToolDecisions, decision)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "adaptive_tool_decision_recorded", map[string]any{
		"decision_id": decision.DecisionID,
		"iteration":   decision.Iteration,
		"tool":        decision.Tool,
		"executable":  decision.Executable,
		"score":       decision.Score.Total,
	})
}

func (o *DurableOrchestrator) ReplaceAdaptiveToolDecision(ctx context.Context, runID string, decision AdaptiveToolDecision) error {
	return o.mutateRun(ctx, runID, func(run *DurableRun) {
		for idx := range run.AdaptiveToolDecisions {
			if run.AdaptiveToolDecisions[idx].DecisionID == decision.DecisionID {
				run.AdaptiveToolDecisions[idx] = decision
				return
			}
		}
		run.AdaptiveToolDecisions = append(run.AdaptiveToolDecisions, decision)
	})
}

func (o *DurableOrchestrator) AppendAdaptiveArtifact(ctx context.Context, runID string, artifact AdaptiveArtifact) error {
	if err := o.mutateRun(ctx, runID, func(run *DurableRun) {
		run.AdaptiveArtifacts = append(run.AdaptiveArtifacts, artifact)
	}); err != nil {
		return err
	}
	return o.LogEvent(ctx, runID, "adaptive_artifact_recorded", map[string]any{
		"artifact_id": artifact.ArtifactID,
		"kind":        artifact.Kind,
		"iteration":   artifact.Iteration,
	})
}
