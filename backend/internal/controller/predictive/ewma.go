package predictive

func ewma(values []float64, alpha float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if alpha <= 0 {
		alpha = 0.25
	}
	if alpha > 1 {
		alpha = 1
	}
	avg := values[0]
	for _, value := range values[1:] {
		avg = alpha*value + (1-alpha)*avg
	}
	return avg
}
