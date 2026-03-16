package signalinsights

import (
	"math"
	"strings"
)

const (
	TierRuntime     = "tier1_runtime"
	TierOperational = "tier2_operational"
	TierStructural  = "tier3_structural"
	TierEvent       = "tier4_event"
)

type Profile struct {
	Direction    string
	Pattern      string
	Sustained    bool
	ChangePct    float64
	Slope        float64
	Acceleration float64
	Volatility   float64
	BurstScore   float64
}

func TierForMetric(metric string) string {
	name := strings.ToLower(strings.TrimSpace(metric))
	switch {
	case strings.Contains(name, "security"),
		strings.Contains(name, "finding"),
		strings.Contains(name, "retrans"),
		strings.Contains(name, "drop"),
		strings.Contains(name, "rdma_error"),
		strings.Contains(name, "rdma_congestion"):
		return TierEvent
	case strings.Contains(name, "probe_core"),
		strings.Contains(name, "numa"),
		strings.Contains(name, "filesystem"),
		strings.Contains(name, "fd_usage"):
		return TierStructural
	case strings.Contains(name, "memory"),
		strings.Contains(name, "network_rx"),
		strings.Contains(name, "network_tx"),
		strings.Contains(name, "disk_read"),
		strings.Contains(name, "disk_write"),
		strings.Contains(name, "gpu_memory"),
		strings.Contains(name, "pagecache"),
		strings.Contains(name, "pgpg"),
		strings.Contains(name, "written_pages"),
		strings.Contains(name, "dirtied_pages"):
		return TierOperational
	default:
		return TierRuntime
	}
}

func ProfileFromValues(values []float64, spikeCount int) Profile {
	out := Profile{
		Direction: "stable",
		Pattern:   "steady",
	}
	if len(values) == 0 {
		return out
	}

	first := values[0]
	last := values[len(values)-1]
	out.ChangePct = percentChange(first, last)
	if len(values) == 1 {
		return out
	}

	mean, stddev := meanStddev(values)
	scale := math.Max(math.Abs(mean), 1.0)
	out.Volatility = stddev / scale
	out.Slope = calculateSlope(values)
	normalizedSlope := out.Slope / scale

	if len(values) >= 4 {
		midpoint := len(values) / 2
		left := values[:midpoint]
		right := values[midpoint:]
		out.Acceleration = (calculateSlope(right) - calculateSlope(left)) / scale
	}

	minValue := values[0]
	maxValue := values[0]
	signChanges := 0
	trendAligned := 0
	lastSign := 0
	slopeSign := sign(normalizedSlope, 0.015)
	for i := 1; i < len(values); i++ {
		if values[i] < minValue {
			minValue = values[i]
		}
		if values[i] > maxValue {
			maxValue = values[i]
		}
		diff := values[i] - values[i-1]
		diffSign := sign(diff/scale, 0.012)
		if diffSign != 0 && lastSign != 0 && diffSign != lastSign {
			signChanges++
		}
		if diffSign != 0 {
			lastSign = diffSign
		}
		if slopeSign != 0 && diffSign == slopeSign {
			trendAligned++
		}
	}

	normalizedRange := (maxValue - minValue) / scale
	switch {
	case normalizedSlope >= 0.03 || out.ChangePct >= 8:
		out.Direction = "rising"
	case normalizedSlope <= -0.03 || out.ChangePct <= -8:
		out.Direction = "falling"
	default:
		out.Direction = "stable"
	}

	if out.Direction != "stable" && len(values) >= 4 {
		needed := int(math.Ceil(float64(len(values)-1) * 0.6))
		out.Sustained = trendAligned >= needed
	}

	switch {
	case spikeCount == 0 && signChanges >= 3 && signChanges*2 >= len(values)-1 && normalizedRange >= 0.12:
		out.Pattern = "oscillating"
	case spikeCount > 0 || out.Volatility >= 0.3 || (!out.Sustained && normalizedRange >= 0.25):
		out.Pattern = "bursty"
	default:
		out.Pattern = "steady"
	}

	out.BurstScore = math.Max(float64(spikeCount), normalizedRange*10.0+out.Volatility*4.0)
	return out
}

func Summary(profile Profile) string {
	switch {
	case profile.Pattern == "oscillating":
		return "oscillating"
	case profile.Sustained && profile.Direction == "rising":
		return "sustained rise"
	case profile.Sustained && profile.Direction == "falling":
		return "sustained drop"
	case profile.Pattern == "bursty" && profile.Direction == "rising":
		return "bursty rise"
	case profile.Pattern == "bursty" && profile.Direction == "falling":
		return "bursty drop"
	case profile.Pattern == "bursty":
		return "bursty"
	default:
		return profile.Direction
	}
}

func Direction(profile Profile) string {
	if profile.Pattern == "oscillating" {
		return "stable"
	}
	if profile.Pattern == "bursty" && !profile.Sustained && math.Abs(profile.ChangePct) < 15 {
		return "stable"
	}
	return profile.Direction
}

func meanStddev(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	mean := total / float64(len(values))
	sumSq := 0.0
	for _, value := range values {
		delta := value - mean
		sumSq += delta * delta
	}
	return mean, math.Sqrt(sumSq / float64(len(values)))
}

func calculateSlope(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	n := float64(len(values))
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumXX := 0.0
	for idx, value := range values {
		x := float64(idx)
		sumX += x
		sumY += value
		sumXY += x * value
		sumXX += x * x
	}
	denominator := (n * sumXX) - (sumX * sumX)
	if math.Abs(denominator) < 1e-9 {
		return 0
	}
	return ((n * sumXY) - (sumX * sumY)) / denominator
}

func percentChange(first, last float64) float64 {
	baseline := math.Abs(first)
	if baseline < 1e-9 {
		if math.Abs(last) < 1e-9 {
			return 0
		}
		return 100
	}
	return (last - first) / baseline * 100
}

func sign(value, epsilon float64) int {
	switch {
	case value > epsilon:
		return 1
	case value < -epsilon:
		return -1
	default:
		return 0
	}
}
