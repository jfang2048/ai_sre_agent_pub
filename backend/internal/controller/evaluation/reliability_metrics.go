package evaluation

import (
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// Reliability / repeated-trial statistics: outcome-level stability, pass@k,
// percentile latency, and confidence intervals.

// replayStabilityScore is the mean pairwise agreement of task success across
// a case's trials (1 = every trial agreed, 0 = maximal disagreement).
func replayStabilityScore(successes []bool) float64 {
	n := len(successes)
	if n < 2 {
		return 1
	}
	agree := 0
	pairs := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			pairs++
			if successes[i] == successes[j] {
				agree++
			}
		}
	}
	if pairs == 0 {
		return 1
	}
	return float64(agree) / float64(pairs)
}

// verdictConsistencyScore is the mean pairwise agreement of post-action
// verdict strings across trials. Returns nil when no verdicts were recorded.
func verdictConsistencyScore(executions []eval.WorkflowCaseExecution) *float64 {
	var verdicts []string
	for _, execution := range executions {
		verdict := postActionVerdict(execution.Report.Validation.PostActionValidation)
		if strings.TrimSpace(verdict) == "" {
			return nil
		}
		verdicts = append(verdicts, verdict)
	}
	if len(verdicts) < 2 {
		score := 1.0
		return &score
	}
	agree := 0
	pairs := 0
	for i := 0; i < len(verdicts); i++ {
		for j := i + 1; j < len(verdicts); j++ {
			pairs++
			if strings.EqualFold(verdicts[i], verdicts[j]) {
				agree++
			}
		}
	}
	score := float64(agree) / float64(pairs)
	return &score
}

// rootCauseConsistencyScore is the mean pairwise agreement of the top root
// cause claim across trials.
func rootCauseConsistencyScore(executions []eval.WorkflowCaseExecution) float64 {
	if len(executions) < 2 {
		return 1
	}
	agree := 0
	pairs := 0
	for i := 0; i < len(executions); i++ {
		for j := i + 1; j < len(executions); j++ {
			pairs++
			a := strings.ToLower(strings.TrimSpace(executions[i].Result.TopRootCause))
			b := strings.ToLower(strings.TrimSpace(executions[j].Result.TopRootCause))
			if a == b {
				agree++
			}
		}
	}
	if pairs == 0 {
		return 1
	}
	return float64(agree) / float64(pairs)
}

// successMetrics computes success rate, pass@1 and pass@k for one case's
// trials using the unbiased repeated-trial estimator. pass@1 is the expected
// success probability of one attempt, not "at least one trial passed".
func successMetrics(successes []bool, k int) (rate float64, passAt1 float64, passAtK float64) {
	successful := 0
	for _, ok := range successes {
		if ok {
			successful++
		}
	}
	rate = ratioScores(successful, len(successes))
	passAt1 = passAtKEstimator(len(successes), successful, 1)
	passAtK = passAtKEstimator(len(successes), successful, k)
	return rate, passAt1, passAtK
}

// meanOf computes the mean of non-NaN values.
func meanOf(values ...float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	count := 0
	for _, value := range values {
		if !math.IsNaN(value) {
			sum += value
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// statisticBootstrapCI bootstraps an arbitrary case-level statistic. It is
// intentionally used only across cases: the current workflow runtime cannot
// accept a deterministic seed per trial, so repeated runs must not be treated
// as independent observations when constructing confidence intervals.
func statisticBootstrapCI(values []float64, seed int64, resamples int, statistic func([]float64) float64) (low, point, high float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	point = statistic(values)
	if len(values) == 1 {
		return point, point, point
	}
	if resamples <= 0 {
		resamples = 2000
	}
	// #nosec G404 -- reproducible bootstrap sampling is statistical, not security-sensitive.
	rng := rand.New(rand.NewSource(seed))
	sample := make([]float64, len(values))
	estimates := make([]float64, 0, resamples)
	for i := 0; i < resamples; i++ {
		for j := range sample {
			sample[j] = values[rng.Intn(len(values))]
		}
		estimates = append(estimates, statistic(sample))
	}
	sort.Float64s(estimates)
	return estimates[int(float64(resamples)*0.025)], point, estimates[int(float64(resamples)*0.975)]
}

// latencyPercentiles computes p50/p95/p99 over the pooled per-trial samples.
func latencyPercentiles(values []float64) (p50, p95, p99 float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	return percentile(values, 50), percentile(values, 95), percentile(values, 99)
}

// meanPtrValues returns the mean of non-nil pointer values plus the support.
func meanPtrValues(values []*float64) (float64, int) {
	return meanPtr(values)
}
