package collector

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestProtectionGovernorDecideLoadShedsUnderPressure(t *testing.T) {
	gov := newProtectionGovernor(zap.NewNop())
	cfg := DefaultConfig().Protection

	decision := gov.Decide(
		cfg,
		transportPressureSnapshot{Retries: 1, LastErrKind: "retry_exhausted"},
		spool.Snapshot{BacklogBytes: 90, MaxBytes: 100},
		[]*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 96},
			{Name: "node_pressure_memory_some_avg10", Value: 12},
		},
		defaultHardwareProfile(time.Time{}),
		collectorSelfSample{
			CPUPercent:     12,
			CPUTimeDelta:   300 * time.Millisecond,
			RSSBytes:       cfg.MemorySoftLimitBytes + 1,
			HeapAllocBytes: 32 * 1024 * 1024,
			Goroutines:     8,
		},
	)

	require.Equal(t, protectionModeCritical, decision.Mode)
	require.True(t, decision.DisableLogs)
	require.True(t, decision.DisableSecurity)
	require.True(t, decision.DisableExternal)
	require.True(t, decision.SkipProcessFallback)
	require.LessOrEqual(t, decision.MaxDrainRecords, cfg.MaxDrainRecordsPerCycle)
	require.Greater(t, decision.SignalPressure, 0)
}

func TestProtectionGovernorDecideIncidentKeepsOptionalCollectors(t *testing.T) {
	gov := newProtectionGovernor(zap.NewNop())
	cfg := DefaultConfig().Protection

	decision := gov.Decide(
		cfg,
		transportPressureSnapshot{},
		spool.Snapshot{BacklogBytes: 0, MaxBytes: 1024},
		[]*telemetryv1.Metric{
			{Name: "node_gpu_process_total", Value: 2},
			{Name: "node_gpu_utilization_sm_avg_percent", Value: 15},
		},
		defaultHardwareProfile(time.Time{}),
		collectorSelfSample{
			CPUPercent:     1,
			CPUTimeDelta:   20 * time.Millisecond,
			RSSBytes:       32 * 1024 * 1024,
			HeapAllocBytes: 8 * 1024 * 1024,
			Goroutines:     4,
		},
	)

	require.Equal(t, protectionModeIncident, decision.Mode)
	require.False(t, decision.DisableLogs)
	require.False(t, decision.DisableSecurity)
	require.False(t, decision.DisableExternal)
	require.False(t, decision.SkipProcessFallback)
	require.Equal(t, cfg.MaxDrainRecordsPerCycle, decision.MaxDrainRecords)
}
