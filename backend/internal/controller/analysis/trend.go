package analysis

import "math"

func trendFromSamples(samples []MetricSample) string {
	if len(samples) < 3 {
		return "stable"
	}

	minValue := samples[0].Value
	maxValue := samples[0].Value
	total := 0.0
	for _, sample := range samples {
		total += sample.Value
		if sample.Value < minValue {
			minValue = sample.Value
		}
		if sample.Value > maxValue {
			maxValue = sample.Value
		}
	}
	scale := math.Max(math.Abs(total/float64(len(samples))), 1.0)
	epsilon := math.Max(scale*0.01, 0.01)

	positive := 0
	negative := 0
	for i := 1; i < len(samples); i++ {
		delta := samples[i].Value - samples[i-1].Value
		switch {
		case delta > epsilon:
			positive++
		case delta < -epsilon:
			negative++
		}
	}

	switch {
	case positive > 0 && negative == 0:
		return "rising"
	case negative > 0 && positive == 0:
		return "falling"
	default:
		return "stable"
	}
}
