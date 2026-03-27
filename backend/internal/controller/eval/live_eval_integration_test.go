//go:build liveeval

package eval

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/stretchr/testify/require"
)

func TestGoldenEvaluationFastLiveJudge(t *testing.T) {
	if strings.TrimSpace(os.Getenv(analysis.EnvLLMAPIKey)) == "" {
		t.Skip("set SRE_AGENT_LLM_API_KEY to run live LLM-backed evaluation")
	}
	if strings.TrimSpace(os.Getenv(analysis.EnvLLMProvider)) == "" {
		t.Setenv(analysis.EnvLLMProvider, analysis.ProviderGoogle)
	}
	if strings.TrimSpace(os.Getenv(analysis.EnvLLMModel)) == "" {
		t.Setenv(analysis.EnvLLMModel, "gemini-flash-latest")
	}

	report, err := Run(context.Background(), RunOptions{
		Scope:             ScopeFast,
		JudgeExplanations: true,
		JudgeBatchSize:    5,
	})
	require.NoError(t, err)
	if liveJudgeQuotaExhausted(report.Anomaly.ExplanationJudge) {
		t.Skip("live LLM-backed evaluation skipped: provider quota exhausted")
	}
	require.True(t, report.Retrieval.CasesRun == report.Retrieval.CasesPassed, report.Text())
	require.True(t, report.Anomaly.CasesRun == report.Anomaly.CasesPassed, report.Text())
	require.True(t, report.Workflow.CasesRun == report.Workflow.CasesPassed, report.Text())
	require.NotNil(t, report.Anomaly.ExplanationJudge)
	require.Equal(t, report.Anomaly.CasesRun, report.Anomaly.ExplanationJudge.CasesRun, report.Text())
	require.GreaterOrEqual(t, report.Anomaly.ExplanationJudge.AgreementRate, 0.90, report.Text())
	require.GreaterOrEqual(t, report.Anomaly.ExplanationJudge.AverageScore, 0.85, report.Text())
}

func liveJudgeQuotaExhausted(report *AnomalyJudgeReport) bool {
	if report == nil || len(report.Cases) == 0 {
		return false
	}
	sawQuota := false
	for _, item := range report.Cases {
		errText := strings.ToLower(item.Error)
		if strings.TrimSpace(errText) == "" {
			continue
		}
		if strings.Contains(errText, "resource_exhausted") || strings.Contains(errText, "quota exceeded") {
			sawQuota = true
			continue
		}
		if strings.Contains(errText, "rate limit") {
			sawQuota = true
			continue
		}
		if !item.Passed {
			return false
		}
	}
	return sawQuota
}
