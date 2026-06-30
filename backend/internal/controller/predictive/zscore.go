package predictive

import "math"

func meanStddev(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	mean := sum / float64(len(values))
	if len(values) == 1 {
		return mean, 0
	}
	var variance float64
	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}
	variance /= float64(len(values))
	return mean, math.Sqrt(variance)
}

func zscore(value, mean, stddev float64) float64 {
	if stddev <= 0 {
		return 0
	}
	return (value - mean) / stddev
}
