package agent

type PlannerProposal struct {
	CurrentNextObjective string          `json:"current_next_objective"`
	CandidateHypotheses  []string        `json:"candidate_hypotheses,omitempty"`
	CandidateTools       []ToolCandidate `json:"candidate_tools,omitempty"`
	WhyCandidatesMatter  []string        `json:"why_each_candidate_matters,omitempty"`
	ExpectedEvidence     []string        `json:"expected_evidence,omitempty"`
	NextDirective        string          `json:"next_directive"`
	Selected             *ToolCandidate  `json:"selected,omitempty"`
}

func buildPlannerProposal(state *workflowState, context ToolSelectionContext, candidates []ToolCandidate) PlannerProposal {
	proposal := PlannerProposal{
		CurrentNextObjective: context.Objective,
		CandidateHypotheses:  rankedHypothesisTitles(state.hypotheses, 3),
		CandidateTools:       append([]ToolCandidate(nil), truncateToolCandidates(candidates, 5)...),
		ExpectedEvidence:     dedupeStrings(append([]string(nil), context.EvidenceGaps...)),
		NextDirective:        "gather_evidence",
	}
	for _, candidate := range proposal.CandidateTools {
		proposal.WhyCandidatesMatter = append(proposal.WhyCandidatesMatter, candidate.Reason)
	}
	if len(candidates) > 0 {
		selected := candidates[0]
		proposal.Selected = &selected
		proposal.ExpectedEvidence = dedupeStrings(append(proposal.ExpectedEvidence, selected.ExpectedEvidence...))
	}
	return proposal
}

func rankedHypothesisTitles(hypotheses []RCAHypothesis, limit int) []string {
	if len(hypotheses) == 0 || limit <= 0 {
		return nil
	}
	out := make([]string, 0, minInt(limit, len(hypotheses)))
	for idx, hypothesis := range hypotheses {
		if idx >= limit {
			break
		}
		out = append(out, firstNonEmpty(hypothesis.Title, hypothesis.Description, hypothesis.ID))
	}
	return out
}

func truncateToolCandidates(candidates []ToolCandidate, limit int) []ToolCandidate {
	if len(candidates) <= limit || limit <= 0 {
		return candidates
	}
	return candidates[:limit]
}
