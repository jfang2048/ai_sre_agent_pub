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
	sort.SliceStable(state.hypotheses, func(i, j int) bool {
		if state.hypotheses[i].Confidence == state.hypotheses[j].Confidence {
			return state.hypotheses[i].Title < state.hypotheses[j].Title
		}
		return state.hypotheses[i].Confidence > state.hypotheses[j].Confidence
	})
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
