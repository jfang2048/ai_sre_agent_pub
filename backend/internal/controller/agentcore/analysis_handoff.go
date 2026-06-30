package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func buildAnalysisHandoff(state *workflowState) AnalysisHandoff {
	if state == nil {
		return AnalysisHandoff{SchemaVersion: AnalysisHandoffSchemaVersion, Agent: "analysis_agent"}
	}
	supporting := make([]string, 0, len(state.evidence))
	weak := make([]string, 0, len(state.riskSignals))
	contradicting := make([]string, 0, len(state.hypotheses))
	for _, item := range state.evidence {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		supporting = append(supporting, item.ID)
	}
	for _, hypothesis := range state.hypotheses {
		supporting = append(supporting, hypothesis.EvidenceIDs...)
		contradicting = append(contradicting, hypothesis.ContradictingEvidenceIDs...)
	}
	for _, signal := range state.riskSignals {
		if signal.Triggered {
			continue
		}
		if signal.ID == "" {
			continue
		}
		weak = append(weak, signal.ID)
	}
	ranked := make([]string, 0, len(state.hypotheses))
	for _, hypothesis := range state.hypotheses {
		ranked = append(ranked, hypothesis.Title)
	}
	actionCandidates := buildActionCandidates(state)
	return AnalysisHandoff{
		SchemaVersion:            AnalysisHandoffSchemaVersion,
		Agent:                    "analysis_agent",
		IncidentID:               firstNonEmpty(state.rca.IncidentID, state.workflowID),
		CorrelationID:            state.workflowID,
		CreatedAt:                time.Now().UTC(),
		IncidentSummary:          firstNonEmpty(state.incident.Summary, strings.Join(state.rca.Anomalies, "; ")),
		CollectorID:              state.collectorID,
		Trigger:                  state.trigger,
		RiskLevel:                state.risk.RiskLevel,
		Confidence:               maxFloat(state.incident.Confidence, topHypothesisConfidence(state.hypotheses)),
		ImpactedScope:            impactedScopeFromState(state),
		TelemetryQuality:         state.telemetryQuality,
		BlindSpots:               dedupeStrings(append([]string{}, state.telemetryQuality.BlindSpots...)),
		Hypotheses:               append([]RCAHypothesis(nil), state.hypotheses...),
		HypothesisPackets:        buildHypothesisHandoffs(state),
		RankedSuspectedCauses:    ranked,
		SupportingEvidenceIDs:    dedupeStrings(supporting),
		WeakEvidenceIDs:          dedupeStrings(weak),
		ContradictingEvidenceIDs: dedupeStrings(contradicting),
		SecurityContext: ValidationSecurityContext{
			Score:             state.security.Score,
			Categories:        append([]string(nil), state.security.Categories...),
			FindingIDs:        append([]string(nil), state.security.FindingIDs...),
			Findings:          append([]string(nil), state.security.Findings...),
			CriticalFindings:  state.security.CriticalFindings,
			HighFindings:      state.security.HighFindings,
			SuspiciousTargets: append([]string(nil), state.security.SuspiciousPortCandidates...),
		},
		Recommendations:            append([]WorkflowRecommendation(nil), state.recommendation...),
		BoundedActionCandidates:    actionCandidates,
		UnresolvedGaps:             unresolvedGapsFromState(state),
		ChangeLinks:                append([]RCAChangeLink(nil), state.changeLinks...),
		SceneFamily:                state.sceneClassification.SceneFamily,
		SceneConfidence:            state.sceneClassification.Confidence,
		CandidateSubscenes:         append([]string(nil), state.sceneClassification.CandidateSubscenes...),
		MissingEvidence:            append([]string(nil), state.sceneClassification.MissingEvidence...),
		CollectionPlanSummary:      summarizeCollectionPlan(state.collectionPlan),
		RecollectionRound:          len(state.recollectionResults),
		RemainingBudget:            state.remainingBudget,
		EvidenceGoalsStillUnmet:    append([]string(nil), state.evidenceGapState.EvidenceGoalsStillUnmet...),
		SuggestedValidationTargets: buildValidationTargetsFromState(state),
	}
}

func buildValidationTargetsFromState(state *workflowState) []ValidationTarget {
	if state == nil {
		return nil
	}
	targets := make([]ValidationTarget, 0, 8)
	if state.sceneClassification.SceneFamily != "" {
		target := buildValidationTargetPlan(
			state,
			ValidationTargetSceneClassification,
			string(state.sceneClassification.SceneFamily),
			firstNonEmpty(state.incident.Summary, "validate the current scene classification against recollected evidence"),
			"",
			"",
			"",
			priorityForConfidence(state.sceneClassification.Confidence),
			nil,
			nil,
			nil,
		)
		target.ID = "validate-scene-classification"
		target.SceneFamily = state.sceneClassification.SceneFamily
		target.ToolFamilies = dedupeStrings(append(collectionPlanFamilies(state.collectionPlan), target.ToolFamilies...))
		target.EvidenceGaps = dedupeStrings(append([]string{}, state.sceneClassification.MissingEvidence...))
		target.SuggestedTools = modulesToTools(state.collectionPlan.TargetCollectorsOrModules)
		targets = append(targets, target)
	}
	if len(state.hypotheses) == 0 || len(state.metricsData.History) < 3 || len(state.telemetryQuality.BlindSpots) > 0 {
		target := buildValidationTargetPlan(
			state,
			ValidationTargetHypothesis,
			"rebuild missing baseline evidence",
			"collect enough current metrics and service health evidence before trusting prior incident analogues",
			"",
			"",
			"",
			"critical",
			nil,
			nil,
			nil,
		)
		target.ID = "validate-evidence-gap"
		target.ToolFamilies = dedupeStrings(append([]string{"metrics", "service_health", "logs"}, target.ToolFamilies...))
		target.EvidenceGaps = dedupeStrings(append([]string{"metric_baseline"}, target.EvidenceGaps...))
		target.SuggestedTools = prioritizeValidationTools(suggestedToolsForTargetFamilies(target.Type, target.ToolFamilies), target.EvidenceGaps)
		targets = append(targets, target)
	}
	for idx, hypothesis := range state.hypotheses {
		if idx >= 3 {
			break
		}
		target := buildValidationTargetPlan(
			state,
			ValidationTargetHypothesis,
			hypothesis.Title,
			hypothesis.Description,
			hypothesis.ID,
			"",
			"",
			priorityForConfidence(hypothesis.Confidence),
			hypothesis.EvidenceIDs,
			hypothesis.ContradictingEvidenceIDs,
			nil,
		)
		target.ID = fmt.Sprintf("validate-hypothesis-%d", idx+1)
		targets = append(targets, target)
	}
	if len(state.changeLinks) > 0 {
		top := state.changeLinks[0]
		target := buildValidationTargetPlan(
			state,
			ValidationTargetChangeCorrelation,
			firstNonEmpty(top.Summary, top.Category),
			firstNonEmpty(top.ImpactSummary, top.HypothesisHint, top.Summary),
			"",
			"",
			"",
			priorityForConfidence(top.CorrelationScore),
			compactStrings(top.ChangeID),
			nil,
			compactStrings(top.Category),
		)
		target.ID = "validate-change-correlation"
		targets = append(targets, target)
	}
	for idx, rec := range state.recommendation {
		if idx >= 2 {
			break
		}
		target := buildValidationTargetPlan(
			state,
			ValidationTargetRecommendation,
			rec.Summary,
			firstNonEmpty(rec.Details, rec.Rationale),
			"",
			rec.ID,
			"",
			rec.Priority,
			rec.EvidenceIDs,
			nil,
			compactStrings(rec.Category),
		)
		target.ID = fmt.Sprintf("validate-recommendation-%d", idx+1)
		targets = append(targets, target)
	}
	if len(state.hypotheses) > 0 {
		target := buildValidationTargetPlan(
			state,
			ValidationTargetContradiction,
			firstNonEmpty(state.hypotheses[0].Title, state.incident.Summary),
			"search for evidence that weakens the leading explanation before finalizing recommendations",
			state.hypotheses[0].ID,
			"",
			"",
			"high",
			state.hypotheses[0].EvidenceIDs,
			state.hypotheses[0].ContradictingEvidenceIDs,
			nil,
		)
		target.ID = "validate-contradiction-search"
		targets = append(targets, target)
	}
	for idx, candidate := range buildActionCandidates(state) {
		if idx >= 1 {
			break
		}
		target := buildValidationTargetPlan(
			state,
			ValidationTargetRemediation,
			candidate.Summary,
			firstNonEmpty(candidate.ExpectedImpact, candidate.RollbackHint),
			"",
			candidate.RecommendationID,
			candidate.ID,
			"high",
			nil,
			nil,
			compactStrings(candidate.Category),
		)
		target.ID = fmt.Sprintf("validate-action-%d", idx+1)
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(i, j int) bool {
		left := validationPriorityRank(targets[i].Priority)
		right := validationPriorityRank(targets[j].Priority)
		if left == right {
			return targets[i].ID < targets[j].ID
		}
		return left > right
	})
	return dedupeValidationTargets(targets)
}

func validationPriorityRank(priority string) int {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func dedupeValidationTargets(in []ValidationTarget) []ValidationTarget {
	if len(in) == 0 {
		return nil
	}
	out := make([]ValidationTarget, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, target := range in {
		key := firstNonEmpty(strings.TrimSpace(target.ID), string(target.Type)+"|"+strings.TrimSpace(target.Title))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, target)
	}
	return out
}
