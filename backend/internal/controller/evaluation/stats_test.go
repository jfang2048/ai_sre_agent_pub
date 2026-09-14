package evaluation

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWilsonIntervalKnownCases(t *testing.T) {
	low, high := wilsonInterval(0, 10)
	require.InDelta(t, 0, low, 1e-9)
	require.InDelta(t, 0.2775, high, 1e-3)

	low, high = wilsonInterval(10, 10)
	require.InDelta(t, 0.7225, low, 1e-3)
	require.InDelta(t, 1, high, 1e-9)

	low, high = wilsonInterval(5, 10)
	require.InDelta(t, 0.2366, low, 1e-3)
	require.InDelta(t, 0.7634, high, 1e-3)

	// zero trials must not panic
	low, high = wilsonInterval(0, 0)
	require.Equal(t, 0.0, low)
	require.Equal(t, 0.0, high)
}

func TestBootstrapCIDeterministicSeed(t *testing.T) {
	values := []float64{0.4, 0.6, 0.5, 0.7, 0.3, 0.55, 0.45, 0.65}
	low1, mean1, high1 := bootstrapCI(values, 42, 2000)
	low2, mean2, high2 := bootstrapCI(values, 42, 2000)
	require.Equal(t, low1, low2, "bootstrap must be reproducible for a fixed seed")
	require.Equal(t, mean1, mean2)
	require.Equal(t, high1, high2)
	require.LessOrEqual(t, low1, mean1)
	require.GreaterOrEqual(t, high1, mean1)

	// a single sample collapses to the sample itself
	low, mean, high := bootstrapCI([]float64{0.7}, 42, 100)
	require.Equal(t, 0.7, low)
	require.Equal(t, 0.7, mean)
	require.Equal(t, 0.7, high)
}

func TestPercentile(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	require.Equal(t, 3.0, percentile(values, 50))
	require.Equal(t, 5.0, percentile(values, 100))
	require.Equal(t, 1.0, percentile(values, 0))
	require.InDelta(t, 4.6, percentile(values, 90), 1e-9)
	require.Equal(t, 0.0, percentile(nil, 50))
}

func TestPassAtKEstimator(t *testing.T) {
	require.Equal(t, 1.0, passAtKEstimator(5, 5, 1))
	require.Equal(t, 0.0, passAtKEstimator(5, 0, 1))
	// 1 success in 5 trials, k=1: probability any single attempt succeeds
	require.InDelta(t, 0.2, passAtKEstimator(5, 1, 1), 1e-9)
	// k > n clamps to n: 1 success in 5 trials with k=10 means all 5 sampled -> success
	require.Equal(t, 1.0, passAtKEstimator(5, 1, 10))
	require.Equal(t, 0.0, passAtKEstimator(0, 0, 1))
	// never the naive successes/k: 2/5 with k=3 is 1 - C(3,3)/C(5,3) = 0.9
	value := passAtKEstimator(5, 2, 3)
	require.NotEqual(t, 0.4, value)
	require.InDelta(t, 0.9, value, 1e-9)
}

func TestMacroMeanIgnoresNaN(t *testing.T) {
	mean, support := macroMean([]float64{0.5, math.NaN(), 1.0})
	require.Equal(t, 2, support)
	require.InDelta(t, 0.75, mean, 1e-9)

	mean, support = macroMean(nil)
	require.Equal(t, 0, support)
	require.Equal(t, 0.0, mean)
}

func TestMeanOfIgnoresNaNInNumeratorAndDenominator(t *testing.T) {
	require.InDelta(t, 0.75, meanOf(0.5, math.NaN(), 1.0), 1e-9)
}

func TestMeanPtrSkipsNil(t *testing.T) {
	a := 0.4
	b := 0.8
	mean, support := meanPtr([]*float64{&a, nil, &b})
	require.Equal(t, 2, support)
	require.InDelta(t, 0.6, mean, 1e-9)

	mean, support = meanPtr([]*float64{nil})
	require.Equal(t, 0, support)
	require.Equal(t, 0.0, mean)
}

func TestRatioScores(t *testing.T) {
	require.Equal(t, 0.5, ratioScores(1, 2))
	require.Equal(t, 0.0, ratioScores(1, 0))
	require.Equal(t, 1.0, ratioScores(3, 3))
}
