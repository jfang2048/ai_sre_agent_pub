package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func buildAnalysisHandoff(state *workflowState) AnalysisHandoff {
	if state == nil {
		return AnalysisHandoff{Agent: "analysis_agent"}
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
	return AnalysisHandoff{
		Agent:                      "analysis_agent",
		CreatedAt:                  time.Now().UTC(),
		IncidentSummary:            firstNonEmpty(state.incident.Summary, strings.Join(state.rca.Anomalies, "; ")),
		CollectorID:                state.collectorID,
		Trigger:                    state.trigger,
		RiskLevel:                  state.risk.RiskLevel,
		Confidence:                 maxFloat(state.incident.Confidence, topHypothesisConfidence(state.hypotheses)),
		TelemetryQuality:           state.telemetryQuality,
		Hypotheses:                 append([]RCAHypothesis(nil), state.hypotheses...),
		RankedSuspectedCauses:      ranked,
		SupportingEvidenceIDs:      dedupeStrings(supporting),
		WeakEvidenceIDs:            dedupeStrings(weak),
		ContradictingEvidenceIDs:   dedupeStrings(contradicting),
		Recommendations:            append([]WorkflowRecommendation(nil), state.recommendation...),
		UnresolvedGaps:             unresolvedGapsFromState(state),
		ChangeLinks:                append([]RCAChangeLink(nil), state.changeLinks...),
		SuggestedValidationTargets: buildValidationTargetsFromState(state),
	}
}

func buildValidationTargetsFromState(state *workflowState) []ValidationTarget {
	if state == nil {
		return nil
	}
	targets := make([]ValidationTarget, 0, 8)
	for idx, hypothesis := range state.hypotheses {
		if idx >= 3 {
			break
		}
		targets = append(targets, ValidationTarget{
			ID:                       fmt.Sprintf("validate-hypothesis-%d", idx+1),
			Type:                     ValidationTargetHypothesis,
			Title:                    hypothesis.Title,
			Summary:                  hypothesis.Description,
			HypothesisID:             hypothesis.ID,
			Priority:                 priorityForConfidence(hypothesis.Confidence),
			Focus:                    classifyValidationFocus(strings.Join([]string{hypothesis.Title, hypothesis.Description}, " ")),
			SuggestedTools:           suggestedToolsForFocus(classifyValidationFocus(strings.Join([]string{hypothesis.Title, hypothesis.Description}, " ")), ValidationTargetHypothesis),
			SupportingEvidenceIDs:    append([]string(nil), hypothesis.EvidenceIDs...),
			ContradictingEvidenceIDs: append([]string(nil), hypothesis.ContradictingEvidenceIDs...),
		})
	}
	if len(state.changeLinks) > 0 {
		top := state.changeLinks[0]
		targets = append(targets, ValidationTarget{
			ID:                    "validate-change-correlation",
			Type:                  ValidationTargetChangeCorrelation,
			Title:                 firstNonEmpty(top.Summary, top.Category),
			Summary:               firstNonEmpty(top.ImpactSummary, top.HypothesisHint, top.Summary),
			Priority:              priorityForConfidence(top.CorrelationScore),
			Focus:                 "change",
			SuggestedTools:        suggestedToolsForFocus("change", ValidationTargetChangeCorrelation),
			SupportingEvidenceIDs: compactStrings(top.ChangeID),
		})
	}
	for idx, rec := range state.recommendation {
		if idx >= 2 {
			break
		}
		targets = append(targets, ValidationTarget{
			ID:                    fmt.Sprintf("validate-recommendation-%d", idx+1),
			Type:                  ValidationTargetRecommendation,
			Title:                 rec.Summary,
			Summary:               firstNonEmpty(rec.Details, rec.Rationale),
			RecommendationID:      rec.ID,
			Priority:              rec.Priority,
			Focus:                 classifyValidationFocus(strings.Join([]string{rec.Summary, rec.Details, rec.Rationale}, " ")),
			SuggestedTools:        suggestedToolsForFocus(classifyValidationFocus(strings.Join([]string{rec.Summary, rec.Details, rec.Rationale}, " ")), ValidationTargetRecommendation),
			SupportingEvidenceIDs: append([]string(nil), rec.EvidenceIDs...),
		})
	}
	if len(state.hypotheses) > 0 {
		targets = append(targets, ValidationTarget{
			ID:                       "validate-contradiction-search",
			Type:                     ValidationTargetContradiction,
			Title:                    firstNonEmpty(state.hypotheses[0].Title, state.incident.Summary),
			Summary:                  "search for evidence that weakens the leading explanation before finalizing recommendations",
			HypothesisID:             state.hypotheses[0].ID,
			Priority:                 "high",
			Focus:                    classifyValidationFocus(strings.Join([]string{state.hypotheses[0].Title, state.hypotheses[0].Description}, " ")),
			SuggestedTools:           suggestedToolsForFocus(classifyValidationFocus(strings.Join([]string{state.hypotheses[0].Title, state.hypotheses[0].Description}, " ")), ValidationTargetContradiction),
			SupportingEvidenceIDs:    append([]string(nil), state.hypotheses[0].EvidenceIDs...),
			ContradictingEvidenceIDs: append([]string(nil), state.hypotheses[0].ContradictingEvidenceIDs...),
		})
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
