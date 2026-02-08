package analysis

func trendFromSamples(samples []MetricSample) string {
	if len(samples) < 3 {
		return "stable"
	}

	last := samples[len(samples)-1].Value
	prev := samples[len(samples)-2].Value
	prev2 := samples[len(samples)-3].Value

	delta1 := last - prev
	delta2 := prev - prev2

	if delta1 > 0 && delta2 > 0 {
		return "rising"
	}
	if delta1 < 0 && delta2 < 0 {
		return "falling"
	}
	return "stable"
}
