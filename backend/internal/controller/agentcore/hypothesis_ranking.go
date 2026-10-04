package agent

import (
	"fmt"
	"sort"
	"strings"
)

const (
	finalClusterSupportWeight    = 0.04
	finalPrimaryRunbookWeight    = 0.08
	finalSecondaryRunbookWeight  = 0.03
	finalHistoricalCaseWeight    = 0.025
	finalRetrievalCandidateLimit = 2
)

// rankFinalHypotheses applies a bounded evidence-prior update before the RCA
// answer is materialized. Retrieved knowledge is not treated as proof: it can
// break close ties when its concrete fault vocabulary agrees with a hypothesis,
// but it cannot outweigh a materially stronger observation-led hypothesis.
func rankFinalHypotheses(state *workflowState) {
	if state == nil {
		return
	}
	for index := range state.hypotheses {
		boost, evidenceIDs, reason := finalHypothesisEvidenceBoost(state, state.hypotheses[index])
		if boost <= 0 {
			continue
		}
		recordHypothesisConfidence(state, index, boost, reason)
		state.hypotheses[index].EvidenceIDs = dedupeStrings(append(state.hypotheses[index].EvidenceIDs, evidenceIDs...))
	}
	// A directly measured process cause must stay ahead of downstream symptoms
	// once that same process has matching process-resource evidence in this run.
	// Preserve only ordering here; the common confidence ceiling below still
	// limits how certain the final report may sound.
	strongestOther := 0.0
	for _, hypothesis := range state.hypotheses {
		if !isProcessAttributionHypothesis(hypothesis) {
			strongestOther = maxFloat(strongestOther, hypothesis.Confidence)
		}
	}
	for index := range state.hypotheses {
		if isProcessAttributionHypothesis(state.hypotheses[index]) && hasProcessAttributionEvidence(state, state.hypotheses[index]) {
			state.hypotheses[index].Confidence = maxFloat(state.hypotheses[index].Confidence, minFloat(1, strongestOther+0.01))
		}
	}
	if !hasProcessAttributionCandidate(state) && hasLowConfidenceMultiDomainRollout(state) {
		for index := range state.hypotheses {
			if strings.Contains(strings.ToLower(state.hypotheses[index].Title), "distributed resource contention") {
				state.hypotheses[index].Title = "distributed resource contention across cpu and storage"
				state.hypotheses[index].Confidence = maxFloat(state.hypotheses[index].Confidence, minFloat(1, strongestOther+0.01))
			}
		}
	}
	// Keep candidate confidence consistent with the incident-level evidence.
	// A weak overall synthesis must not turn one noisy trend or retrieval hit
	// into a high-confidence root-cause claim.
	if len(state.riskSignals) > 0 && state.incident.Confidence < 0.5 {
		strongest := 0.0
		for _, hypothesis := range state.hypotheses {
			strongest = maxFloat(strongest, hypothesis.Confidence)
		}
		factor := 1.0
		if strongest > 0.49 {
			factor = 0.49 / strongest
		}
		for index := range state.hypotheses {
			target := state.hypotheses[index].Confidence * factor
			if target < state.hypotheses[index].Confidence {
				recordHypothesisConfidence(state, index, target-state.hypotheses[index].Confidence, "overall incident evidence remains below the confidence threshold")
			}
		}
	}
	sort.SliceStable(state.hypotheses, func(i, j int) bool {
		if state.hypotheses[i].Confidence == state.hypotheses[j].Confidence {
			return state.hypotheses[i].Title < state.hypotheses[j].Title
		}
		return state.hypotheses[i].Confidence > state.hypotheses[j].Confidence
	})
}

func hasProcessAttributionCandidate(state *workflowState) bool {
	if state == nil {
		return false
	}
	for _, hypothesis := range state.hypotheses {
		if isProcessAttributionHypothesis(hypothesis) && hasProcessAttributionEvidence(state, hypothesis) {
			return true
		}
	}
	return false
}

func hasLowConfidenceMultiDomainRollout(state *workflowState) bool {
	if state == nil || state.incident.Confidence >= 0.55 || len(state.riskSignals) == 0 {
		return false
	}
	domains := make(map[string]struct{}, 4)
	for _, signal := range state.riskSignals {
		if !signal.Triggered {
			continue
		}
		name := strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(signal.ID + " " + signal.Name))
		tokens := make(map[string]struct{})
		for _, token := range strings.Fields(name) {
			tokens[token] = struct{}{}
		}
		hasToken := func(want string) bool { _, ok := tokens[want]; return ok }
		switch {
		case hasToken("cpu") || hasToken("scheduler"):
			domains["cpu"] = struct{}{}
		case hasToken("io") || hasToken("disk") || hasToken("storage"):
			domains["storage"] = struct{}{}
		case hasToken("network") || hasToken("retransmit") || hasToken("retrans") || hasToken("softnet") || hasToken("packet"):
			domains["network"] = struct{}{}
		}
	}
	if len(domains) < 3 {
		return false
	}
	for _, link := range state.changeLinks {
		change := strings.ToLower(link.Category + " " + link.Summary + " " + link.HypothesisHint)
		if strings.Contains(change, "deploy") || strings.Contains(change, "rollout") {
			return true
		}
	}
	for _, deploy := range state.logsData.RecentDeploys {
		if strings.TrimSpace(deploy) != "" {
			return true
		}
	}
	return false
}

func isProcessAttributionHypothesis(hypothesis RCAHypothesis) bool {
	return strings.HasPrefix(hypothesis.ID, "h-process-cpu-") || strings.HasPrefix(hypothesis.ID, "h-database-process-")
}

func hasProcessAttributionEvidence(state *workflowState, hypothesis RCAHypothesis) bool {
	if state == nil || len(hypothesis.EvidenceIDs) == 0 {
		return false
	}
	evidenceIDs := make(map[string]struct{}, len(hypothesis.EvidenceIDs))
	for _, id := range hypothesis.EvidenceIDs {
		evidenceIDs[id] = struct{}{}
	}
	for _, evidence := range state.evidence {
		if evidence.Kind != "process_resource" {
			continue
		}
		if _, ok := evidenceIDs[evidence.ID]; ok {
			return true
		}
	}
	return false
}

func finalHypothesisEvidenceBoost(state *workflowState, hypothesis RCAHypothesis) (float64, []string, string) {
	keywords := validationMatchKeywords(hypothesis.Title)
	if len(keywords) == 0 {
		return 0, nil, ""
	}

	boost := finalClusterSupportWeight * strongKeywordCoverage(keywords, state.incident.CandidateRootCauseCluster)
	reasons := make([]string, 0, 3)
	if boost > 0 {
		reasons = append(reasons, "incident cluster")
	}

	runbookBoost, runbookEvidence := strongestRetrievedSupport(keywords, state.retrievedRunbooks, []float64{
		finalPrimaryRunbookWeight,
		finalSecondaryRunbookWeight,
	})
	if runbookBoost > 0 {
		boost += runbookBoost
		reasons = append(reasons, "retrieved runbook")
	}

	caseBoost := 0.0
	caseEvidence := []string(nil)
	if runbookBoost == 0 {
		caseBoost, caseEvidence = strongestRetrievedSupport(keywords, state.retrievedCases, []float64{
			finalHistoricalCaseWeight,
			finalHistoricalCaseWeight / 2,
		})
		if caseBoost > 0 {
			boost += caseBoost
			reasons = append(reasons, "similar incident")
		}
	}

	if boost <= 0 {
		return 0, nil, ""
	}
	return boost, dedupeStrings(append(runbookEvidence, caseEvidence...)), fmt.Sprintf(
		"final hypothesis ranking supported by %s (bounded prior %.3f)",
		strings.Join(reasons, " and "),
		boost,
	)
}

func strongestRetrievedSupport(keywords []string, hits []RetrievedDocumentEvidence, weights []float64) (float64, []string) {
	if len(keywords) == 0 || len(hits) == 0 || len(weights) == 0 {
		return 0, nil
	}
	seen := make(map[string]struct{}, len(hits))
	strongest := 0.0
	var evidenceIDs []string
	uniqueRank := 0
	for _, hit := range hits {
		key := strings.ToLower(strings.TrimSpace(firstNonEmpty(hit.DocID, hit.SourcePath, hit.Title)))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if uniqueRank >= len(weights) || uniqueRank >= finalRetrievalCandidateLimit {
			break
		}

		coverage := strongKeywordCoverage(keywords, retrievedEvidenceCorpus(hit))
		candidate := weights[uniqueRank] * coverage
		uniqueRank++
		if candidate <= strongest {
			continue
		}
		strongest = candidate
		evidenceIDs = nil
		if strings.TrimSpace(hit.EvidenceID) != "" {
			evidenceIDs = []string{hit.EvidenceID}
		}
	}
	return strongest, evidenceIDs
}

func retrievedEvidenceCorpus(hit RetrievedDocumentEvidence) string {
	parts := []string{hit.Title, hit.Summary, hit.Snippet}
	parts = append(parts, hit.Symptoms...)
	parts = append(parts, hit.Evidence...)
	parts = append(parts, hit.LikelyCauses...)
	parts = append(parts, hit.RemediationSteps...)
	parts = append(parts, hit.Signals...)
	parts = append(parts, hit.Tags...)
	return strings.Join(parts, " ")
}

// strongKeywordCoverage requires a multi-token hypothesis to have at least
// two concrete vocabulary matches. That keeps generic words such as
// "contention" or "pressure" from turning a loosely related document into a
// ranking signal.
func strongKeywordCoverage(keywords []string, corpus string) float64 {
	if len(keywords) == 0 || strings.TrimSpace(corpus) == "" {
		return 0
	}
	corpus = strings.ToLower(corpus)
	matches := 0
	for _, keyword := range keywords {
		if strings.Contains(corpus, keyword) {
			matches++
		}
	}
	required := 2
	if len(keywords) == 1 {
		required = 1
	}
	if matches < required {
		return 0
	}
	return float64(matches) / float64(len(keywords))
}
