package agent

import "testing"

import "github.com/stretchr/testify/require"

func TestShouldDeferExpensiveAnalysisWhenCollectorProtectsHost(t *testing.T) {
	metrics := map[string]float64{
		"collector_protection_mode_severity":    2,
		"collector_protection_spool_fill_ratio": 0.6,
	}

	require.True(t, shouldDeferExpensiveAnalysis(metrics))
	require.Equal(t, "Monitoring agent is load-shedding to protect the host", monitoringProtectionFinding(metrics))
}

func TestShouldNotDeferExpensiveAnalysisWhenCollectorIsCalm(t *testing.T) {
	metrics := map[string]float64{
		"collector_protection_mode_severity":    0,
		"collector_protection_signal_pressure":  1,
		"collector_protection_spool_fill_ratio": 0.1,
	}

	require.False(t, shouldDeferExpensiveAnalysis(metrics))
	require.Equal(t, "", monitoringProtectionFinding(metrics))
}
