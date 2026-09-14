package evaluation

import (
	"sort"
	"strings"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// RCA evaluation v2: entity-level comparison against structured ground truth.
//
// Ground truth entities are fault *objects* ("checkout-api process",
// "memory", "database-primary") matched against the agent's ranked claims
// with alias/token-subset matching — never plain substring equality on whole
// sentences, and never a comparison against values the runtime generated.

// rcaEntityClaim is one ranked claim made by the agent about the root cause.
type rcaEntityClaim struct {
	Text string
	Rank int // 1-based rank in the agent's primary answer; 0 = supporting claim
}

// extractRCAClaims pulls the agent's ranked root-cause claims from a workflow
// report. The primary claims (rank 1..n) are what the agent presents as its
// answer; supporting claims include every hypothesis and causal-path entry.
// Fields that merely repeat the collector id are dropped — they are artifacts,
// not entity claims.
func extractRCAClaims(execution eval.WorkflowCaseExecution) (primary []rcaEntityClaim, supporting []string) {
	report := execution.Report
	collector := strings.ToLower(strings.TrimSpace(report.CollectorID))
	ranked := make([]rcaEntityClaim, 0, 8)
	seen := make(map[string]bool, 8)
	addRanked := func(text string, rank int) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || seen[strings.ToLower(trimmed)] {
			return
		}
		seen[strings.ToLower(trimmed)] = true
		ranked = append(ranked, rcaEntityClaim{Text: trimmed, Rank: rank})
	}
	// All "primary answer" fields are rank 1: they are parallel expressions
	// of the agent's single top answer, not a ranked list of alternatives.
	addPrimary := func(text string) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || trimmed == collector {
			return
		}
		if isNoiseEntityClaim(trimmed) {
			return
		}
		addRanked(trimmed, 1)
	}

	addPrimary(report.SuspectedRootCauseEntity)
	addPrimary(report.StructuredReport.MostLikelyCause)
	addPrimary(execution.Result.TopRootCause)
	addPrimary(report.StructuredReport.SuspectedRootCauseEntity)

	type rankedHypothesis struct {
		title string
		rank  int
	}
	hypotheses := make([]rankedHypothesis, 0, len(report.Hypotheses))
	for _, item := range report.Hypotheses {
		hypotheses = append(hypotheses, rankedHypothesis{title: item.Title, rank: item.Rank})
	}
	sort.SliceStable(hypotheses, func(i, j int) bool {
		return hypotheses[i].rank < hypotheses[j].rank
	})
	for _, item := range hypotheses {
		if item.rank == 1 {
			addPrimary(item.title)
		}
	}
	for _, item := range hypotheses {
		addRanked(item.title, item.rank)
	}

	// supporting corpus: everything else the agent said about the cause
	corpus := make([]string, 0, 16)
	for _, item := range report.Hypotheses {
		corpus = append(corpus, item.Title, item.Description)
	}
	for _, item := range report.CausalPath {
		corpus = append(corpus, item)
	}
	for _, item := range report.ImpactPath {
		corpus = append(corpus, item)
	}
	for _, item := range report.StructuredReport.CausalPath {
		corpus = append(corpus, item)
	}
	for _, item := range report.Anomalies {
		corpus = append(corpus, item)
	}
	for _, item := range report.Evidence {
		corpus = append(corpus, item.Entity, item.Summary, item.Snippet)
	}
	for _, item := range report.StructuredReport.SupportingSignals {
		corpus = append(corpus, item)
	}
	return ranked, corpus
}

// isNoiseEntityClaim drops agent claims that are boilerplate placeholders
// rather than actual entity names.
func isNoiseEntityClaim(text string) bool {
	lower := strings.ToLower(text)
	for _, noise := range []string{"insufficient evidence", "unknown", "n/a", "not identified", "none"} {
		if lower == noise {
			return true
		}
	}
	return false
}

// normalizeText lowercases and collapses punctuation/whitespace into tokens.
func normalizeText(text string) []string {
	var tokens []string
	var current strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			current.WriteRune(r)
		default:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// aliasMatches reports whether the alias's tokens all appear (as a subset) in
// the text's tokens. Multi-token aliases require all tokens; single-token
// aliases match on presence. Aliases with zero tokens never match.
func aliasMatches(alias, text string) bool {
	aliasTokens := normalizeText(alias)
	if len(aliasTokens) == 0 {
		return false
	}
	textTokens := normalizeText(text)
	textSet := make(map[string]bool, len(textTokens))
	for _, item := range textTokens {
		textSet[item] = true
	}
	for _, item := range aliasTokens {
		if !textSet[item] {
			return false
		}
	}
	return true
}

// groundTruthAliases flattens entities + aliases + affected services into the
// acceptable match set for one case.
func groundTruthAliases(gt V2RootCauseGroundTruth) []string {
	aliases := make([]string, 0, len(gt.Entities)+len(gt.Aliases)+len(gt.AffectedServices))
	aliases = append(aliases, gt.Entities...)
	aliases = append(aliases, gt.Aliases...)
	aliases = append(aliases, gt.AffectedServices...)
	return aliases
}

// matchRCAGroundTruth computes entity precision / recall / recall@k between
// the agent's ranked claims and the structured ground truth.
//
// Entity semantics: each ground-truth entity is a fault object. An alias is an
// alternative phrasing of that object — an alias "belongs" to the entity that
// shares at least one token with it, so finding "memory pressure" locates the
// "memory" entity; it is NOT an additional entity the agent must name
// separately. Aliases that share no token with any entity confirm the
// root-cause family and count toward precision but not toward entity recall.
//
// Recall counts ground-truth entities found anywhere in the agent's ranked
// claims; precision counts agent primary claims that match an entity, an
// alias, or an affected service. Recall@k counts entities found by the first
// k ranked claims.
func matchRCAGroundTruth(claims []rcaEntityClaim, corpus []string, gt V2RootCauseGroundTruth) (precision, recall, recallAt1, recallAt3, recallAt5, f1 float64, gtMatched, gtTotal int) {
	if len(gt.Entities) == 0 && len(gt.Aliases) == 0 {
		return 0, 0, 0, 0, 0, 0, 0, 0
	}
	// full match pool: entities + aliases + affected services (for precision)
	pool := groundTruthAliases(gt)

	primaryTexts := make([]string, len(claims))
	for i, claim := range claims {
		primaryTexts[i] = claim.Text
	}

	primaryMatched := 0
	for _, text := range primaryTexts {
		if matchesAnyAlias(text, pool) {
			primaryMatched++
		}
	}

	// entity-level recall over the agent's ranked claims
	entityGroups := entityAliasGroups(gt)
	found := make([]bool, len(gt.Entities))
	for _, claim := range claims {
		for i, patterns := range entityGroups {
			if found[i] {
				continue
			}
			if matchesAnyAlias(claim.Text, patterns) {
				found[i] = true
			}
		}
	}

	gtTotal = len(gt.Entities)
	gtMatched = 0
	for _, item := range found {
		if item {
			gtMatched++
		}
	}

	if gtTotal == 0 {
		// no named entities: any alias match locates the root-cause family
		claimsText := strings.Join(primaryTexts, " \n ")
		if len(gt.Aliases) > 0 && matchesAnyAlias(claimsText, gt.Aliases) {
			recall = 1
		} else {
			recall = 0
		}
	} else {
		recall = ratioScores(gtMatched, gtTotal)
	}
	recallAt1 = recallAtKForEntities(gt, claims, 1)
	recallAt3 = recallAtKForEntities(gt, claims, 3)
	recallAt5 = recallAtKForEntities(gt, claims, 5)

	precision = ratioScores(primaryMatched, len(primaryTexts))
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return precision, recall, recallAt1, recallAt3, recallAt5, f1, gtMatched, gtTotal
}

// entityAliasGroups maps each ground-truth entity to its match patterns: the
// entity name plus every alias that shares at least one token with it.
func entityAliasGroups(gt V2RootCauseGroundTruth) [][]string {
	groups := make([][]string, len(gt.Entities))
	for i, entity := range gt.Entities {
		entityTokens := tokenSet(entity)
		patterns := []string{entity}
		for _, alias := range gt.Aliases {
			for token := range tokenSet(alias) {
				if entityTokens[token] {
					patterns = append(patterns, alias)
					break
				}
			}
		}
		groups[i] = patterns
	}
	return groups
}

// tokenSet lowercases and tokenizes text into a set.
func tokenSet(text string) map[string]bool {
	set := make(map[string]bool)
	for _, token := range normalizeText(text) {
		set[token] = true
	}
	return set
}

// matchesAnyAlias reports whether the text matches any alias in the pool.
func matchesAnyAlias(text string, pool []string) bool {
	for _, alias := range pool {
		if aliasMatches(alias, text) {
			return true
		}
	}
	return false
}

// recallAtKForEntities counts ground-truth entities found by the first k
// ranked claims using entity alias groups.
func recallAtKForEntities(gt V2RootCauseGroundTruth, claims []rcaEntityClaim, k int) float64 {
	if len(gt.Entities) == 0 {
		return 0
	}
	groups := entityAliasGroups(gt)
	found := make([]bool, len(gt.Entities))
	for _, claim := range claims {
		if claim.Rank <= 0 || claim.Rank > k {
			continue
		}
		for i, patterns := range groups {
			if found[i] {
				continue
			}
			if matchesAnyAlias(claim.Text, patterns) {
				found[i] = true
			}
		}
	}
	return ratioScores(countTrue(found), len(gt.Entities))
}

// rcaReasoningScore grades causal reasoning on a 0 / 0.5 / 1 scale:
//
//	1   = correct causal reasoning supported by evidence
//	0.5 = correct direction but incomplete causal explanation
//	0   = contradicts evidence / wrong cause
func rcaReasoningScore(gt V2RootCauseGroundTruth, claims []rcaEntityClaim, corpus []string, requiredEvidence []string, report agentcore.RCAWorkflowReport) float64 {
	_, recall, _, _, _, _, _, _ := matchRCAGroundTruth(claims, corpus, gt)
	if len(groundTruthAliases(gt)) == 0 {
		// no RCA ground truth (noop/healthy case): reasoning is neutral
		return 0.5
	}
	if recall == 0 {
		// wrong or missing cause
		return 0
	}
	// contradicting evidence on the top hypothesis invalidates the reasoning
	for _, item := range report.Hypotheses {
		if item.Rank == 1 && len(item.ContradictingEvidenceIDs) > 0 {
			return 0
		}
	}
	// evidence support: how many required evidence items appear in the corpus
	corpusText := strings.Join(corpus, " \n ")
	covered := 0
	for _, evidence := range requiredEvidence {
		if aliasMatches(evidence, corpusText) {
			covered++
		}
	}
	if len(requiredEvidence) > 0 && float64(covered)/float64(len(requiredEvidence)) >= 0.5 {
		return 1
	}
	return 0.5
}

// propagationChainScore compares the agent's ordered causal chain against the
// ground-truth propagation chain. It scores recall of chain stages plus
// relative-order consistency: finding "api gateway timeout" alone on a
// four-stage chain yields 0.25 * orderBonus, not full credit.
//
//	score = recall * (0.5 + 0.5 * orderConsistency)
func propagationChainScore(gtChain []string, agentChain []string) (float64, *float64) {
	if len(gtChain) == 0 {
		return 0, nil
	}
	if len(agentChain) == 0 {
		return 0, floatPtr(0)
	}
	matchedPos := make([]int, 0, len(gtChain))
	matchedCount := 0
	for _, stage := range gtChain {
		pos := -1
		for i, item := range agentChain {
			if aliasMatches(stage, item) {
				pos = i
				break
			}
		}
		if pos >= 0 {
			matchedCount++
			matchedPos = append(matchedPos, pos)
		}
	}
	recall := ratioScores(matchedCount, len(gtChain))
	orderConsistency := 1.0
	if len(matchedPos) >= 2 {
		consistentPairs := 0
		totalPairs := 0
		for i := 0; i < len(matchedPos); i++ {
			for j := i + 1; j < len(matchedPos); j++ {
				totalPairs++
				if matchedPos[i] < matchedPos[j] {
					consistentPairs++
				}
			}
		}
		orderConsistency = ratioScores(consistentPairs, totalPairs)
	}
	score := recall * (0.5 + 0.5*orderConsistency)
	return score, floatPtr(score)
}

// faultDomainAccuracy matches the ground-truth fault domain (and aliases)
// against the full agent corpus.
func faultDomainAccuracy(gt V2RootCauseGroundTruth, corpus []string) (float64, *float64) {
	if strings.TrimSpace(gt.FaultDomain) == "" {
		return 0, nil
	}
	aliases := []string{gt.FaultDomain}
	aliases = append(aliases, gt.FaultDomainAliases...)
	corpusText := strings.Join(corpus, " \n ")
	for _, alias := range aliases {
		if aliasMatches(alias, corpusText) {
			return 1, floatPtr(1)
		}
	}
	return 0, floatPtr(0)
}
