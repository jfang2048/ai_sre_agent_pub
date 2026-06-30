package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type ToolSelectionContext struct {
	Stage               string                    `json:"stage"`
	Objective           string                    `json:"objective"`
	IncidentSummary     string                    `json:"incident_summary,omitempty"`
	SceneFamily         SceneFamily               `json:"scene_family,omitempty"`
	CollectorID         string                    `json:"collector_id,omitempty"`
	Window              time.Duration             `json:"window,omitempty"`
	EvidenceGaps        []string                  `json:"evidence_gaps,omitempty"`
	Contradictions      []string                  `json:"contradictions,omitempty"`
	ScopeHints          []string                  `json:"scope_hints,omitempty"`
	PolicyPosture       string                    `json:"policy_posture,omitempty"`
	PriorToolYield      []AdaptiveToolYieldRecord `json:"prior_tool_yield,omitempty"`
	Budget              AdaptiveBudgetState       `json:"budget,omitempty"`
	PreferredFamilies   []string                  `json:"preferred_families,omitempty"`
	PreferredTools      []ToolName                `json:"preferred_tools,omitempty"`
	ReadOnlyOnly        bool                      `json:"read_only_only,omitempty"`
	AutonomousSelection bool                      `json:"autonomous_selection,omitempty"`
	Target              *ValidationTarget         `json:"target,omitempty"`
}

type CandidateScoreBreakdown struct {
	ObjectiveRelevance           float64 `json:"objective_relevance"`
	EvidenceGapCoverage          float64 `json:"evidence_gap_coverage"`
	ExpectedInformationGain      float64 `json:"expected_information_gain"`
	ContradictionResolution      float64 `json:"contradiction_resolution_value"`
	SkillFirstPriority           float64 `json:"skill_first_priority"`
	CostPenalty                  float64 `json:"cost_penalty"`
	RiskPenalty                  float64 `json:"risk_penalty"`
	FreshnessNeed                float64 `json:"freshness_need"`
	ScopeFit                     float64 `json:"scope_fit"`
	SceneCompatibility           float64 `json:"scene_compatibility"`
	DependencySatisfaction       float64 `json:"dependency_satisfaction"`
	PolicyEligibility            float64 `json:"policy_eligibility"`
	RepeatedToolPenalty          float64 `json:"repeated_tool_penalty"`
	LowYieldPenalty              float64 `json:"low_yield_penalty"`
	CheapFirstPreference         float64 `json:"cheap_first_preference"`
	BetterDiscriminatorAdvantage float64 `json:"better_discriminator_advantage"`
	ExperienceMemoryBias         float64 `json:"experience_memory_bias"`
}

type ToolCandidateScore struct {
	Total     float64                 `json:"total"`
	Breakdown CandidateScoreBreakdown `json:"breakdown"`
}

type ToolCandidate struct {
	Tool                   ToolName             `json:"tool"`
	Contract               ToolContract         `json:"contract"`
	LegacyContract         WorkflowToolContract `json:"legacy_contract"`
	Query                  map[string]string    `json:"query,omitempty"`
	Reason                 string               `json:"reason"`
	CoveredEvidenceGaps    []string             `json:"covered_evidence_gaps,omitempty"`
	ResolvedContradictions []string             `json:"resolved_contradictions,omitempty"`
	ExpectedEvidence       []string             `json:"expected_evidence,omitempty"`
	Score                  ToolCandidateScore   `json:"score"`
}

type ToolSelectionDecision struct {
	DecisionID   string          `json:"decision_id"`
	Stage        string          `json:"stage"`
	Selected     *ToolCandidate  `json:"selected,omitempty"`
	Alternatives []ToolCandidate `json:"alternatives,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Branch       string          `json:"branch,omitempty"`
	StopReason   string          `json:"stop_reason,omitempty"`
}

func buildToolSelectionContext(state *workflowState, stage string, target *ValidationTarget) ToolSelectionContext {
	context := ToolSelectionContext{
		Stage:           firstNonEmpty(strings.TrimSpace(stage), adaptiveRuntimeStage),
		Objective:       adaptiveObjective(state),
		IncidentSummary: firstNonEmpty(state.incident.Summary, state.trigger),
		SceneFamily:     state.sceneClassification.SceneFamily,
		CollectorID:     state.collectorID,
		Window:          state.window,
		EvidenceGaps:    adaptiveEvidenceGaps(state),
		Contradictions:  adaptiveContradictions(state),
		ScopeHints:      append([]string(nil), state.adaptiveScopeHints...),
		PolicyPosture:   workflowPolicyPosture(state),
		PriorToolYield:  adaptiveYieldHistory(state),
		Budget: AdaptiveBudgetState{
			RemainingToolCalls:          maxInt(state.engine.cfg.AdaptiveMaxToolCalls-len(state.toolCalls), 0),
			RemainingIterations:         maxInt(state.engine.cfg.AdaptiveMaxIterations, 0),
			RemainingSameToolRetries:    state.engine.cfg.AdaptiveMaxSameToolRetries,
			RemainingHypothesisRewrites: state.engine.cfg.AdaptiveMaxHypothesisRewrites,
			RemainingTokenBudget:        state.engine.cfg.ReasoningTokenBudget,
		},
		PreferredFamilies:   append([]string(nil), state.adaptiveNextToolFamilyHints...),
		ReadOnlyOnly:        true,
		AutonomousSelection: runtimeModeEnablesAutonomousToolSelection(state.engine.cfg),
		Target:              target,
	}
	if target != nil {
		context.Objective = firstNonEmpty(target.Title, target.Summary, context.Objective)
		context.EvidenceGaps = dedupeStrings(append([]string{}, target.EvidenceGaps...))
		context.ScopeHints = dedupeStrings(append(context.ScopeHints, target.ImpactedScope...))
		context.PreferredFamilies = dedupeStrings(append(context.PreferredFamilies, target.ToolFamilies...))
		context.PreferredTools = append([]ToolName(nil), target.SuggestedTools...)
		context.ReadOnlyOnly = target.ReadOnly || state.engine.cfg.ValidationReadOnlyOnly
	}
	return context
}

func generateToolCandidates(state *workflowState, context ToolSelectionContext) []ToolCandidate {
	if state == nil || state.engine == nil || state.engine.tools == nil || state.engine.tools.contracts == nil {
		return nil
	}
	contracts := state.engine.tools.contracts.List()
	candidates := make([]ToolCandidate, 0, len(contracts))
	for _, contract := range contracts {
		if err := validateToolContract(contract); err != nil {
			continue
		}
		if !workflowToolContractAllowsStage(contract.LegacyContract, firstNonEmpty(context.Stage, adaptiveRuntimeStage)) {
			continue
		}
		if !toolContractAllowsRuntimeContext(contract, state) {
			continue
		}
		if context.ReadOnlyOnly && !contract.ReadOnly {
			continue
		}
		if context.AutonomousSelection && contract.AutonomousSelectionEligible == ToolAutonomyEligibilityNever {
			continue
		}
		if repeatedLowYieldSkillSuppressed(state, contract.Name) {
			continue
		}
		queryShape := shapeToolQuery(state, contract, context)
		score := scoreToolCandidate(state, context, contract, queryShape.Query)
		if score.Total <= 0 {
			continue
		}
		candidates = append(candidates, ToolCandidate{
			Tool:                   contract.Name,
			Contract:               contract,
			LegacyContract:         contract.LegacyContract,
			Query:                  queryShape.Query,
			Reason:                 toolCandidateReason(contract, score.Breakdown, context),
			CoveredEvidenceGaps:    candidateGapCoverage(contract, context.EvidenceGaps),
			ResolvedContradictions: candidateContradictions(contract, context.Contradictions),
			ExpectedEvidence:       append([]string(nil), contract.EvidenceProduced...),
			Score:                  score,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score.Total == candidates[j].Score.Total {
			return candidates[i].Tool < candidates[j].Tool
		}
		return candidates[i].Score.Total > candidates[j].Score.Total
	})
	return trimToolCandidatesForContext(state, context, candidates)
}

func repeatedLowYieldSkillSuppressed(state *workflowState, tool ToolName) bool {
	if state == nil {
		return false
	}
	lowYieldRepeats := 0
	for idx := len(state.adaptiveNormalizedResults) - 1; idx >= 0; idx-- {
		result := state.adaptiveNormalizedResults[idx]
		if result.Tool != tool {
			continue
		}
		if !result.LowYieldSignal && !strings.EqualFold(result.ResultQuality, "low") {
			break
		}
		lowYieldRepeats++
		if lowYieldRepeats >= 2 {
			return true
		}
	}
	return false
}

func scoreToolCandidate(state *workflowState, context ToolSelectionContext, contract ToolContract, query map[string]string) ToolCandidateScore {
	breakdown := CandidateScoreBreakdown{}
	coveredGaps := candidateGapCoverage(contract, context.EvidenceGaps)
	coveredContradictions := candidateContradictions(contract, context.Contradictions)
	breakdown.ObjectiveRelevance = clamp01(0.25 + 0.25*objectiveContractOverlap(context.Objective, contract))
	breakdown.EvidenceGapCoverage = coverageRatio(len(coveredGaps), len(context.EvidenceGaps))
	breakdown.ExpectedInformationGain = clamp01(contract.ExpectedInformationGain)
	breakdown.ContradictionResolution = coverageRatio(len(coveredContradictions), len(context.Contradictions))
	breakdown.CostPenalty = adaptiveCostPenalty(string(contract.CostClass))
	if contract.ReadOnly {
		breakdown.RiskPenalty = 0.02
	} else {
		breakdown.RiskPenalty = 0.30
	}
	breakdown.FreshnessNeed = adaptiveFreshnessFit(state, contract.LegacyContract)
	breakdown.ScopeFit = adaptiveScopeFit(state, contract.LegacyContract)
	breakdown.SceneCompatibility = adaptiveSceneCompatibility(context.SceneFamily, contract.LegacyContract)
	breakdown.DependencySatisfaction = dependencySatisfaction(context, contract)
	breakdown.SkillFirstPriority = skillFirstPriority(context, contract)
	breakdown.PolicyEligibility = candidatePolicyEligibility(state, context, contract, query)
	breakdown.RepeatedToolPenalty = repeatedToolPenalty(state, contract.Name)
	breakdown.LowYieldPenalty = lowYieldPenalty(state, contract.Name)
	cfg := DefaultWorkflowConfig()
	if state != nil && state.engine != nil {
		cfg = state.engine.cfg
	}
	breakdown.CheapFirstPreference = cheapFirstPreference(cfg, contract)
	breakdown.BetterDiscriminatorAdvantage = betterDiscriminatorAdvantage(state, context, contract)
	if state != nil && state.engine != nil && runtimeModeEnablesExperienceMemory(state.engine.cfg) && state.engine.toolExperience != nil {
		breakdown.ExperienceMemoryBias = clampExperienceBias(state.engine.toolExperience.Prior(context.SceneFamily, context.Objective, context.EvidenceGaps, contract.LegacyContract))
	}
	if breakdown.PolicyEligibility <= 0 {
		return ToolCandidateScore{Total: 0, Breakdown: breakdown}
	}

	total := 0.14*breakdown.ObjectiveRelevance +
		0.16*breakdown.EvidenceGapCoverage +
		0.14*breakdown.ExpectedInformationGain +
		0.08*breakdown.ContradictionResolution +
		0.08*breakdown.FreshnessNeed +
		0.08*breakdown.ScopeFit +
		0.08*breakdown.SceneCompatibility +
		0.06*breakdown.DependencySatisfaction +
		0.10*breakdown.SkillFirstPriority +
		0.10*breakdown.PolicyEligibility +
		0.07*breakdown.CheapFirstPreference +
		0.06*breakdown.BetterDiscriminatorAdvantage +
		0.06*breakdown.ExperienceMemoryBias -
		breakdown.CostPenalty -
		breakdown.RiskPenalty -
		breakdown.RepeatedToolPenalty -
		breakdown.LowYieldPenalty

	return ToolCandidateScore{
		Total:     clamp01(total),
		Breakdown: breakdown,
	}
}

func skillFirstPriority(context ToolSelectionContext, contract ToolContract) float64 {
	// "Skills" in the controller are represented by explicit, contract-backed
	// diagnostic tools and capability families. Prefer those concrete tools for
	// first-pass validation; use retrieval/RAG as support unless the current
	// target explicitly asks for runbook, prior-outcome, or knowledge evidence.
	if !isKnowledgeRetrievalTool(contract.Name) && contract.CapabilityFamily != ToolCapabilityFamilyKnowledge {
		return 1
	}
	if explicitlyNeedsKnowledge(context) {
		switch contract.Name {
		case ToolRunbookRetrieval, ToolHistoricalIncident, ToolSimilarCase, ToolActionOutcome:
			return 0.75
		default:
			return 0.45
		}
	}
	switch contract.Name {
	case ToolRAGQuery, ToolKnowledge:
		return 0.05
	default:
		return 0.20
	}
}

func isKnowledgeRetrievalTool(tool ToolName) bool {
	switch tool {
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		return true
	default:
		return false
	}
}

func explicitlyNeedsKnowledge(context ToolSelectionContext) bool {
	for _, family := range context.PreferredFamilies {
		if strings.EqualFold(strings.TrimSpace(family), string(ToolCapabilityFamilyKnowledge)) {
			return true
		}
	}
	text := strings.ToLower(strings.Join(append(append([]string{}, context.EvidenceGaps...), context.Objective, context.IncidentSummary), " "))
	for _, marker := range []string{"knowledge", "rag", "retrieval", "runbook", "prior outcome", "similar case", "historical incident"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func toolContractAllowsRuntimeContext(contract ToolContract, state *workflowState) bool {
	if len(contract.AllowedRuntimeContexts) == 0 {
		return true
	}
	contextName := ""
	if state != nil {
		contextName = strings.TrimSpace(state.workflowType)
	}
	if contextName == "" {
		return true
	}
	for _, allowed := range contract.AllowedRuntimeContexts {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" || allowed == "*" || strings.EqualFold(allowed, contextName) {
			return true
		}
	}
	return false
}

func workflowPolicyPosture(state *workflowState) string {
	if state == nil || state.engine == nil {
		return "unknown"
	}
	switch {
	case state.dryRun && state.engine.cfg.RequireApproval:
		return "dry_run_approval_required"
	case state.dryRun:
		return "dry_run"
	case state.engine.cfg.RequireApproval:
		return "live_approval_required"
	default:
		return "live"
	}
}

func buildToolSelectionDecision(state *workflowState, context ToolSelectionContext, candidates []ToolCandidate) ToolSelectionDecision {
	decision := ToolSelectionDecision{
		DecisionID: fmt.Sprintf("tool-selection-%s-%s-%d", sanitizeID(firstNonEmpty(context.Stage, adaptiveRuntimeStage)), sanitizeID(firstNonEmpty(context.Objective, "objective")), len(candidates)),
		Stage:      firstNonEmpty(context.Stage, adaptiveRuntimeStage),
	}
	if len(candidates) == 0 {
		decision.StopReason = string(AdaptiveStopReasonNoSafeNextStep)
		decision.Reason = "no policy-safe tool candidate satisfied the current evidence gaps"
		decision.Branch = string(AdaptiveDirectiveStop)
		return decision
	}
	decision.Alternatives = append([]ToolCandidate(nil), trimToolCandidatesForContext(state, context, candidates)...)
	selected := decision.Alternatives[0]
	decision.Selected = &selected
	decision.Reason = selected.Reason
	decision.Branch = string(AdaptiveDirectiveContinue)
	return decision
}

func trimToolCandidatesForContext(state *workflowState, context ToolSelectionContext, candidates []ToolCandidate) []ToolCandidate {
	limit := toolCandidateLimit(state, context)
	if limit <= 0 || len(candidates) <= limit {
		return candidates
	}
	return append([]ToolCandidate(nil), candidates[:limit]...)
}

func toolCandidateLimit(state *workflowState, context ToolSelectionContext) int {
	if state == nil || state.engine == nil {
		return 0
	}
	limit := state.engine.cfg.AdaptiveParallelReadOnlyLimit
	if limit <= 0 {
		return 0
	}
	if context.ReadOnlyOnly || context.Stage == adaptiveRuntimeStage || context.AutonomousSelection {
		return limit
	}
	return 0
}

func candidateGapCoverage(contract ToolContract, gaps []string) []string {
	needle := strings.ToLower(strings.Join(append(append([]string{}, contract.EvidenceProduced...), contract.Purpose, string(contract.CapabilityFamily)), " "))
	covered := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(gap), "_", " "))
		if normalized == "" {
			continue
		}
		for _, word := range strings.Fields(normalized) {
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

func candidateContradictions(contract ToolContract, contradictions []string) []string {
	needle := strings.ToLower(strings.Join(append(append([]string{}, contract.EvidenceProduced...), contract.Purpose, string(contract.CapabilityFamily)), " "))
	covered := make([]string, 0, len(contradictions))
	for _, contradiction := range contradictions {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(contradiction), "_", " "))
		if normalized == "" {
			continue
		}
		for _, word := range strings.Fields(normalized) {
			if len(word) < 4 {
				continue
			}
			if strings.Contains(needle, word) {
				covered = append(covered, contradiction)
				break
			}
		}
	}
	return dedupeStrings(covered)
}

func objectiveContractOverlap(objective string, contract ToolContract) float64 {
	objective = strings.ToLower(strings.TrimSpace(objective))
	if objective == "" {
		return 0.4
	}
	score := 0.0
	needle := strings.ToLower(strings.Join([]string{contract.Purpose, string(contract.CapabilityFamily), strings.Join(contract.EvidenceProduced, " ")}, " "))
	for _, word := range strings.Fields(objective) {
		if len(word) < 4 {
			continue
		}
		if strings.Contains(needle, word) {
			score += 0.12
		}
	}
	return clamp01(score)
}

func dependencySatisfaction(context ToolSelectionContext, contract ToolContract) float64 {
	if len(context.PreferredTools) > 0 {
		for _, tool := range context.PreferredTools {
			if tool == contract.Name {
				return 1
			}
		}
	}
	if len(context.PreferredFamilies) > 0 {
		for _, family := range context.PreferredFamilies {
			if strings.EqualFold(strings.TrimSpace(family), strings.TrimSpace(string(contract.CapabilityFamily))) {
				return 1
			}
		}
		if len(context.PreferredTools) > 0 {
			return 0.05
		}
		if len(context.EvidenceGaps) > 0 {
			return 0.10
		}
		return 0.20
	}
	if len(context.EvidenceGaps) == 0 {
		return 0.65
	}
	return 0.55
}

func candidatePolicyEligibility(state *workflowState, context ToolSelectionContext, contract ToolContract, query map[string]string) float64 {
	if context.ReadOnlyOnly && !contract.ReadOnly {
		return 0
	}
	if state == nil || state.engine == nil || state.engine.tools == nil {
		return 0
	}
	tool := state.engine.tools.tools[contract.Name]
	if tool == nil {
		return 0
	}
	decision := state.engine.tools.policy.Evaluate(workflowToolRequest{
		WorkflowID:  state.workflowID,
		Workflow:    state.workflowType,
		Stage:       firstNonEmpty(context.Stage, adaptiveRuntimeStage),
		Actor:       actorForWorkflowStage(firstNonEmpty(context.Stage, adaptiveRuntimeStage)),
		CollectorID: state.collectorID,
		Window:      state.window,
		Limit:       state.limit,
		Query:       query,
		DryRun:      state.dryRun,
	}, tool)
	switch {
	case decision.Status == "blocked":
		return 0
	case decision.ProposalOnly && context.AutonomousSelection:
		return 0
	case decision.RequiresApproval && context.ReadOnlyOnly:
		return 0
	case decision.ProposalOnly:
		return 0.10
	case decision.ExecutionEligible:
		return 1
	default:
		return 0.40
	}
}

func repeatedToolPenalty(state *workflowState, tool ToolName) float64 {
	if state == nil {
		return 0
	}
	count := 0
	for i := len(state.toolCalls) - 1; i >= 0; i-- {
		if state.toolCalls[i].Tool != tool {
			continue
		}
		count++
	}
	if count == 0 {
		return 0
	}
	return minFloat(0.18, 0.04*float64(count))
}

func lowYieldPenalty(state *workflowState, tool ToolName) float64 {
	if state == nil {
		return 0
	}
	penalty := 0.0
	for idx := len(state.adaptiveNormalizedResults) - 1; idx >= 0; idx-- {
		result := state.adaptiveNormalizedResults[idx]
		if result.Tool != tool {
			continue
		}
		if result.LowYieldSignal {
			penalty += 0.06
		}
		if strings.EqualFold(result.ResultQuality, "low") {
			penalty += 0.04
		}
	}
	return minFloat(0.20, penalty)
}

func cheapFirstPreference(cfg WorkflowConfig, contract ToolContract) float64 {
	if !runtimeModePrefersCheapFirst(cfg) {
		return 0
	}
	if !contract.ReadOnly {
		return -0.08
	}
	switch contract.CostClass {
	case ToolCostClassLow:
		return 1
	case ToolCostClassMedium:
		return 0.55
	default:
		return 0.20
	}
}

func betterDiscriminatorAdvantage(state *workflowState, context ToolSelectionContext, contract ToolContract) float64 {
	if contract.ReadOnly {
		return 0.30
	}
	if len(candidateGapCoverage(contract, context.EvidenceGaps)) == 0 {
		return -0.12
	}
	if state != nil && state.engine != nil && state.engine.tools != nil && state.engine.tools.contracts != nil {
		for _, other := range state.engine.tools.contracts.List() {
			if !other.ReadOnly {
				continue
			}
			if len(candidateGapCoverage(other, context.EvidenceGaps)) >= len(candidateGapCoverage(contract, context.EvidenceGaps)) &&
				adaptiveCostPenalty(string(other.CostClass)) <= adaptiveCostPenalty(string(contract.CostClass)) {
				return -0.18
			}
		}
	}
	return 0
}

func clampExperienceBias(value float64) float64 {
	if value > 0.20 {
		return 0.20
	}
	if value < -0.20 {
		return -0.20
	}
	return value
}

func coverageRatio(covered, total int) float64 {
	if total <= 0 {
		if covered > 0 {
			return 1
		}
		return 0.5
	}
	return clamp01(float64(covered) / float64(total))
}

func toolCandidateReason(contract ToolContract, breakdown CandidateScoreBreakdown, context ToolSelectionContext) string {
	switch {
	case breakdown.EvidenceGapCoverage > 0:
		return fmt.Sprintf("%s best covers the current evidence gaps with %s evidence", contract.Name, contract.CapabilityFamily)
	case breakdown.ContradictionResolution > 0:
		return fmt.Sprintf("%s is the strongest contradiction resolver for the current objective", contract.Name)
	case breakdown.CheapFirstPreference > 0.8:
		return fmt.Sprintf("%s is the cheapest strong discriminator that stays within controller policy", contract.Name)
	default:
		return fmt.Sprintf("%s improves %s coverage under %s", contract.Name, context.Objective, contract.CapabilityFamily)
	}
}

func adaptiveScoreFromCandidateScore(score ToolCandidateScore) AdaptiveToolScore {
	return AdaptiveToolScore{
		Relevance:          score.Breakdown.ObjectiveRelevance,
		ExpectedInfoGain:   score.Breakdown.ExpectedInformationGain,
		CostPenalty:        score.Breakdown.CostPenalty,
		RiskPenalty:        score.Breakdown.RiskPenalty,
		Availability:       score.Breakdown.PolicyEligibility,
		SceneCompatibility: score.Breakdown.SceneCompatibility,
		GapCoverage:        score.Breakdown.EvidenceGapCoverage,
		PolicyEligibility:  score.Breakdown.PolicyEligibility,
		FreshnessFit:       score.Breakdown.FreshnessNeed,
		ScopeFit:           score.Breakdown.ScopeFit,
		DependencyFit:      score.Breakdown.DependencySatisfaction,
		ExperiencePrior:    score.Breakdown.ExperienceMemoryBias,
		CheapFirstBias:     score.Breakdown.CheapFirstPreference,
		RepeatPenalty:      score.Breakdown.RepeatedToolPenalty + score.Breakdown.LowYieldPenalty,
		Total:              score.Total,
	}
}

func adaptiveCandidateFromToolCandidate(candidate ToolCandidate) adaptiveToolCandidate {
	return adaptiveToolCandidate{
		Tool:       candidate.Tool,
		Contract:   candidate.LegacyContract,
		Query:      cloneStringMap(candidate.Query),
		Reason:     candidate.Reason,
		GapCovered: append([]string(nil), candidate.CoveredEvidenceGaps...),
		Score:      adaptiveScoreFromCandidateScore(candidate.Score),
		Source:     "rich_contract_ranker",
	}
}
