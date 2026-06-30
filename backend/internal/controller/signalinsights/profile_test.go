package signalinsights

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTierForMetric(t *testing.T) {
	require.Equal(t, TierRuntime, TierForMetric("cpu_usage_percent"))
	require.Equal(t, TierOperational, TierForMetric("memory_used_percent"))
	require.Equal(t, TierStructural, TierForMetric("probe_core_last_frame_age_ms"))
	require.Equal(t, TierEvent, TierForMetric("tcp_retransmit_ratio"))
}

func TestProfileFromValuesDetectsSustainedRise(t *testing.T) {
	profile := ProfileFromValues([]float64{30, 34, 38, 45, 51, 58}, 0)
	require.Equal(t, "rising", profile.Direction)
	require.True(t, profile.Sustained)
	require.Equal(t, "steady", profile.Pattern)
	require.Equal(t, "sustained rise", Summary(profile))
}

func TestProfileFromValuesDetectsOscillation(t *testing.T) {
	profile := ProfileFromValues([]float64{30, 48, 32, 52, 29, 50}, 0)
	require.Equal(t, "oscillating", profile.Pattern)
	require.Equal(t, "oscillating", Summary(profile))
}

func TestProfileFromValuesDetectsBurstiness(t *testing.T) {
	profile := ProfileFromValues([]float64{10, 11, 10.5, 28, 11, 10.8}, 1)
	require.Equal(t, "bursty", profile.Pattern)
	require.Greater(t, profile.BurstScore, 1.0)
}
