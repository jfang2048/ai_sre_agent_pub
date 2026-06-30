package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"go.uber.org/zap"
)

type anomalyExplanationJudge interface {
	JudgeBatch(context.Context, []AnomalyCaseResult) ([]AnomalyJudgeCaseResult, error)
}

type llmAnomalyExplanationJudge struct {
	client *analysis.LLMClient
}

type anomalyJudgeDecision struct {
	ID        string  `json:"id,omitempty"`
	Pass      bool    `json:"pass"`
	Score     float64 `json:"score"`
	Rationale string  `json:"rationale"`
}

func runAnomalyJudgeSuite(ctx context.Context, cases []AnomalyCaseResult, opts RunOptions) (AnomalyJudgeReport, error) {
	client, err := analysis.NewLLMClient(analysis.LLMClientConfig{CodePath: "eval_judge"}, zap.NewNop())
	if err != nil {
		return AnomalyJudgeReport{}, err
	}
	if client == nil {
		return AnomalyJudgeReport{}, fmt.Errorf("llm judge requested but SRE_AGENT_LLM_API_KEY is not configured")
	}
	batchSize := opts.JudgeBatchSize
	if batchSize <= 0 {
		batchSize = 5
	}
	return runAnomalyJudgeSuiteWithJudge(ctx, cases, opts.JudgeCaseLimit, batchSize, llmAnomalyExplanationJudge{client: client})
}

func runAnomalyJudgeSuiteWithJudge(ctx context.Context, cases []AnomalyCaseResult, limit, batchSize int, judge anomalyExplanationJudge) (AnomalyJudgeReport, error) {
	report := AnomalyJudgeReport{
		Cases: make([]AnomalyJudgeCaseResult, 0, len(cases)),
	}
	if len(cases) == 0 {
		return report, nil
	}
	if limit > 0 && limit < len(cases) {
		cases = cases[:limit]
	}
	if batchSize <= 0 {
		batchSize = 1
	}

	totalScore := 0.0
	for start := 0; start < len(cases); start += batchSize {
		end := min(start+batchSize, len(cases))
		batch := cases[start:end]
		results, err := judge.JudgeBatch(ctx, batch)
		if err != nil {
			for _, item := range batch {
				result := AnomalyJudgeCaseResult{
					ID:                   item.ID,
					ExpectedLabel:        item.ExpectedLabel,
					PredictedLabel:       item.PredictedLabel,
					ExpectedDisposition:  item.ExpectedDisposition,
					PredictedDisposition: item.PredictedDisposition,
					Explanation:          item.Explanation,
					Error:                err.Error(),
				}
				report.Cases = append(report.Cases, result)
				report.CasesRun++
				report.FailedCaseIDs = append(report.FailedCaseIDs, result.ID)
			}
			continue
		}

		byID := make(map[string]AnomalyJudgeCaseResult, len(results))
		for _, result := range results {
			byID[result.ID] = result
		}
		for _, item := range batch {
			result, ok := byID[item.ID]
			if !ok {
				result = AnomalyJudgeCaseResult{
					ID:                   item.ID,
					ExpectedLabel:        item.ExpectedLabel,
					PredictedLabel:       item.PredictedLabel,
					ExpectedDisposition:  item.ExpectedDisposition,
					PredictedDisposition: item.PredictedDisposition,
					Explanation:          item.Explanation,
					Error:                "llm judge did not return a decision for this case",
				}
			}
			report.Cases = append(report.Cases, result)
			report.CasesRun++
			totalScore += result.Score
			if result.Passed {
				report.CasesPassed++
			} else {
				report.FailedCaseIDs = append(report.FailedCaseIDs, result.ID)
			}
		}
	}

	report.AgreementRate = float64(report.CasesPassed) / float64(report.CasesRun)
	report.AverageScore = totalScore / float64(report.CasesRun)
	return report, nil
}

func (j llmAnomalyExplanationJudge) JudgeBatch(ctx context.Context, items []AnomalyCaseResult) ([]AnomalyJudgeCaseResult, error) {
	raw, err := j.client.CompletePrompt(ctx, buildAnomalyJudgePrompt(items))
	if err != nil {
		return nil, err
	}
	decisions, err := parseAnomalyJudgeDecision(raw)
	if err != nil {
		return nil, err
	}
	results := make([]AnomalyJudgeCaseResult, 0, len(items))
	for _, item := range items {
		decision, ok := decisions[item.ID]
		if !ok {
			results = append(results, AnomalyJudgeCaseResult{
				ID:                   item.ID,
				ExpectedLabel:        item.ExpectedLabel,
				PredictedLabel:       item.PredictedLabel,
				ExpectedDisposition:  item.ExpectedDisposition,
				PredictedDisposition: item.PredictedDisposition,
				Explanation:          item.Explanation,
				Error:                "llm judge did not return a decision for this case",
			})
			continue
		}
		results = append(results, AnomalyJudgeCaseResult{
			ID:                   item.ID,
			ExpectedLabel:        item.ExpectedLabel,
			PredictedLabel:       item.PredictedLabel,
			ExpectedDisposition:  item.ExpectedDisposition,
			PredictedDisposition: item.PredictedDisposition,
			Explanation:          item.Explanation,
			Passed:               decision.Pass,
			Score:                clampJudgeScore(decision.Score),
			Rationale:            strings.TrimSpace(decision.Rationale),
		})
	}
	return results, nil
}

func buildAnomalyJudgePrompt(items []AnomalyCaseResult) string {
	cases := make([]map[string]any, 0, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		cases = append(cases, map[string]any{
			"id":                    item.ID,
			"expected_label":        item.ExpectedLabel,
			"expected_disposition":  item.ExpectedDisposition,
			"expected_triggered":    item.ExpectedTriggered,
			"predicted_label":       item.PredictedLabel,
			"predicted_disposition": item.PredictedDisposition,
			"predicted_triggered":   item.PredictedTriggered,
			"explanation":           item.Explanation,
			"cross_signal_support":  item.CrossSignalSupport,
		})
		ids = append(ids, item.ID)
	}
	caseJSON, _ := json.MarshalIndent(cases, "", "  ")

	return fmt.Sprintf(`You are grading anomaly-classification explanations.
Return JSON only with this exact shape:
{"results":[{"id":"case-id","pass":true,"score":0.0,"rationale":"short explanation"}]}

Grade whether each actual result is consistent with its contract.
- Pass if the predicted label and disposition fit the contract and the explanation supports that decision.
- Fail if the explanation contradicts the contract or does not justify the chosen label or disposition.
- Be strict on contradictions, but allow wording differences.
- Return one result for each case id.
- Case ids: %s

Cases:
%s
`, strings.Join(ids, ", "), string(caseJSON))
}

var anomalyJudgeJSONRE = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*\\}|\\[.*\\])\\s*```")

func parseAnomalyJudgeDecision(raw string) (map[string]anomalyJudgeDecision, error) {
	if decisions, err := unmarshalJudgeDecisions(raw); err == nil {
		return decisions, nil
	}
	if match := anomalyJudgeJSONRE.FindStringSubmatch(raw); len(match) == 2 {
		if decisions, err := unmarshalJudgeDecisions(strings.TrimSpace(match[1])); err == nil {
			return decisions, nil
		}
	}
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start >= 0 && end > start {
		if decisions, err := unmarshalJudgeDecisions(strings.TrimSpace(raw[start : end+1])); err == nil {
			return decisions, nil
		}
	}
	return nil, fmt.Errorf("could not parse anomaly judge response")
}

func unmarshalJudgeDecisions(raw string) (map[string]anomalyJudgeDecision, error) {
	type wrapped struct {
		Results []anomalyJudgeDecision `json:"results"`
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("empty response")
	}

	var bundle wrapped
	if err := json.Unmarshal([]byte(trimmed), &bundle); err == nil && len(bundle.Results) > 0 {
		return indexJudgeDecisions(bundle.Results)
	}

	var many []anomalyJudgeDecision
	if err := json.Unmarshal([]byte(trimmed), &many); err == nil && len(many) > 0 {
		return indexJudgeDecisions(many)
	}

	var one anomalyJudgeDecision
	if err := json.Unmarshal([]byte(trimmed), &one); err == nil {
		if strings.TrimSpace(one.ID) == "" {
			one.ID = "single"
		}
		return map[string]anomalyJudgeDecision{one.ID: one}, nil
	}

	return nil, fmt.Errorf("unrecognized anomaly judge payload")
}

func indexJudgeDecisions(items []anomalyJudgeDecision) (map[string]anomalyJudgeDecision, error) {
	out := make(map[string]anomalyJudgeDecision, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, fmt.Errorf("anomaly judge response is missing case id")
		}
		item.Score = clampJudgeScore(item.Score)
		out[id] = item
	}
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampJudgeScore(score float64) float64 {
	switch {
	case score < 0:
		return 0
	case score > 1:
		return 1
	default:
		return score
	}
}
