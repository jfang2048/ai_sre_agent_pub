package evaluation

import (
	"math"
	"math/rand"
	"sort"
)

// Statistical primitives for evaluation v2.
//
// All estimators are deterministic for a fixed seed: the Wilson interval is
// closed-form and the bootstrap uses a local rand source seeded from the
// evaluation seed, so repeated runs reproduce identical confidence intervals.

// wilsonInterval returns the 95% Wilson score interval for a binomial
// proportion with `successes` out of `trials`. It never produces bounds
// outside [0,1] and is well-defined for 0 or N successes.
func wilsonInterval(successes, trials int) (low, high float64) {
	if trials <= 0 {
		return 0, 0
	}
	const z = 1.959963984540054 // 97.5th percentile of the standard normal
	phat := float64(successes) / float64(trials)
	n := float64(trials)
	denom := 1 + z*z/n
	center := (phat + z*z/(2*n)) / denom
	half := z * math.Sqrt(phat*(1-phat)/n+z*z/(4*n*n)) / denom
	low = center - half
	high = center + half
	if low < 0 {
		low = 0
	}
	if high > 1 {
		high = 1
	}
	return low, high
}

// bootstrapCI computes a 95% percentile bootstrap confidence interval for the
// mean of `values` (sampled at case level) using a fixed local seed so the
// result is reproducible. Returns (low, mean, high).
func bootstrapCI(values []float64, seed int64, resamples int) (low, mean, high float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(len(values))
	if len(values) == 1 {
		return mean, mean, mean
	}
	if resamples <= 0 {
		resamples = 2000
	}
	// #nosec G404 -- reproducible bootstrap sampling is statistical, not security-sensitive.
	rng := rand.New(rand.NewSource(seed))
	n := len(values)
	means := make([]float64, 0, resamples)
	for i := 0; i < resamples; i++ {
		s := 0.0
		for j := 0; j < n; j++ {
			s += values[rng.Intn(n)]
		}
		means = append(means, s/float64(n))
	}
	sort.Float64s(means)
	lo := means[int(float64(resamples)*0.025)]
	hi := means[int(float64(resamples)*0.975)]
	// no clamping here: bootstrapCI serves both rate metrics (already in
	// [0,1] by construction) and unbounded metrics (latency ms, tokens,
	// tool calls) where clamping would corrupt the interval.
	return lo, mean, hi
}

// percentile returns the p-th percentile (0..100) of a non-empty sample using
// the linear-interpolation (type 7) definition. Returns 0 for empty input.
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := (p / 100) * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sorted[lower]
	}
	weight := rank - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// passAtKEstimator is the unbiased repeated-trial pass@k estimator
// (Codex-style): 1 - C(n-c, k) / C(n, k), where n = trials, c = successful
// trials, k = evaluation attempts. It never divides successful runs by k.
func passAtKEstimator(trials, successes, k int) float64 {
	if trials <= 0 {
		return 0
	}
	if k <= 0 {
		k = 1
	}
	if k > trials {
		k = trials
	}
	if successes >= trials {
		return 1
	}
	if successes == 0 {
		return 0
	}
	// compute C(n-c, k) / C(n, k) as a product of ratios to avoid overflow
	ratio := 1.0
	for i := 0; i < k; i++ {
		ratio *= float64(trials-successes-i) / float64(trials-i)
		if ratio <= 0 {
			return 1
		}
	}
	if ratio > 1 {
		ratio = 1
	}
	return 1 - ratio
}

// ratioScores converts counts into a rate; returns 0 when the denominator is 0.
func ratioScores(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return clamp01(float64(numerator) / float64(denominator))
}

// macroMean averages per-case values, ignoring NaN and nil entries.
func macroMean(values []float64) (float64, int) {
	sum := 0.0
	count := 0
	for _, v := range values {
		if math.IsNaN(v) {
			continue
		}
		sum += v
		count++
	}
	if count == 0 {
		return 0, 0
	}
	return sum / float64(count), count
}

// meanPtr averages pointer-typed values, skipping nils. Returns (mean, support).
func meanPtr(values []*float64) (float64, int) {
	sum := 0.0
	count := 0
	for _, v := range values {
		if v == nil {
			continue
		}
		sum += *v
		count++
	}
	if count == 0 {
		return 0, 0
	}
	return sum / float64(count), count
}

func floatPtr(v float64) *float64 { return &v }
