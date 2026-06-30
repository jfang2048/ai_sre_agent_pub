package agent

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBuildRiskSeriesIncludesServiceLatencyWhenPresent(t *testing.T) {
	base := time.Date(2026, 3, 23, 9, 0, 0, 0, time.UTC)
	history := []ingest.MetricHistorySample{
		{Timestamp: base.Add(-2 * time.Minute), Metrics: map[string]float64{"node_cpu_usage_percent": 42, "service_latency_p95_ms": 110}},
		{Timestamp: base.Add(-1 * time.Minute), Metrics: map[string]float64{"node_cpu_usage_percent": 44, "service_latency_p95_ms": 125}},
		{Timestamp: base, Metrics: map[string]float64{"node_cpu_usage_percent": 46, "service_latency_p95_ms": 150}},
	}

	series := buildRiskSeries(history, logsToolData{})
	latency := findSeriesByKey(series, "service_latency")
	require.NotNil(t, latency)
	require.Equal(t, "Service latency p95", latency.Display)
	require.Greater(t, latency.Latest, latency.Baseline)
}

func TestBehavioralBaselineUsesTSDBHistoryToDowngradeRecurringBurst(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralNode(store, "collector-build", "build-compile", now, 72)

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-build", recurringCPUHistory(now, 72, 3, false))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-build",
		Window:      45 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "cpu_pressure")
	require.Equal(t, "expected_recurring_burst", assessment.Classification)
	require.Greater(t, assessment.RecurrenceCount, 1)

	signal := findJointRiskSignal(report.Signals, "cpu_pressure")
	require.Equal(t, "expected_recurring_burst", signal.BehavioralClassification)
	require.False(t, signal.Triggered)
	require.Greater(t, history.callCount(), 0)
}

func TestBehavioralBaselineNovelSpikeStillTriggers(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralNode(store, "collector-api", "checkout-api", now, 96)

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-api", recurringCPUHistory(now, 96, 0, false))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-api",
		Window:      45 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "cpu_pressure")
	require.Contains(t, []string{"suspicious_deviation", "confirmed_anomaly"}, assessment.Classification)

	signal := findJointRiskSignal(report.Signals, "cpu_pressure")
	require.True(t, signal.Triggered)
	require.NotEqual(t, "expected_recurring_burst", signal.BehavioralClassification)
}

func TestBehavioralBaselineEscalatesRecurringBurstWhenLogsAgree(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralNode(store, "collector-deploy", "deploy-agent", now, 72)
	index.AddBatch([]logindex.RawEvent{{
		Timestamp:   now.Add(-2 * time.Minute),
		CollectorID: "collector-deploy",
		Hostname:    "collector-deploy-host",
		Service:     "deploy-agent",
		Process:     "deploy-agent",
		Level:       "error",
		Source:      "app",
		Message:     "deployment error retry budget exceeded",
		Count:       18,
	}})

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-deploy", recurringCPUHistory(now, 72, 3, false))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-deploy",
		Window:      45 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "cpu_pressure")
	require.Equal(t, "correlated_anomaly", assessment.Classification)
	require.Contains(t, assessment.CrossSignalSupport, "error_log_burst")

	signal := findJointRiskSignal(report.Signals, "cpu_pressure")
	require.True(t, signal.Triggered)
	require.Equal(t, "correlated_anomaly", signal.BehavioralClassification)
}

func TestBehavioralBaselineKeepsSparseHistoryVisible(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 12
	cfg.BehaviorMemory.MinRecurringBursts = 3
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralNode(store, "collector-sparse", "report-worker", now, 74)

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-sparse", sparseCPUHistory(now, 74))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-sparse",
		Window:      45 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "cpu_pressure")
	require.Equal(t, "suspicious_deviation", assessment.Classification)
	require.Contains(t, assessment.Explanation, "history")

	signal := findJointRiskSignal(report.Signals, "cpu_pressure")
	require.True(t, signal.Triggered)
	require.NotEqual(t, "expected_recurring_burst", signal.BehavioralClassification)
}

func TestBehavioralBaselineDoesNotLearnFromRepeatedLocalEvaluations(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralNode(store, "collector-discipline", "batch-worker", now, 72)

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-discipline", sparseCPUHistory(now, 72))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	for i := 0; i < 3; i++ {
		report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
			CollectorID: "collector-discipline",
			Window:      45 * time.Minute,
			Trigger:     "anomaly_repeat",
		})
		require.NoError(t, err)

		assessment := findBehavioralAssessment(report.BehavioralAssessments, "cpu_pressure")
		require.NotEqual(t, "expected_recurring_burst", assessment.Classification)
	}
	require.Greater(t, history.callCount(), 0)
}

func TestBehavioralBaselineCacheStaysBounded(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 2
	cfg.BehaviorMemory.CacheTTL = time.Hour

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	history := newStubMetricHistoryProvider()

	collectors := []struct {
		id      string
		service string
		cpu     float64
	}{
		{id: "collector-cache-1", service: "build-compile", cpu: 72},
		{id: "collector-cache-2", service: "build-compile", cpu: 74},
		{id: "collector-cache-3", service: "build-compile", cpu: 76},
	}
	for _, item := range collectors {
		seedBehavioralNode(store, item.id, item.service, now, item.cpu)
		history.setSamples(item.id, recurringCPUHistory(now, item.cpu, 2, false))
	}

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	for _, item := range collectors {
		_, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
			CollectorID: item.id,
			Window:      45 * time.Minute,
			Trigger:     "anomaly",
		})
		require.NoError(t, err)
	}

	require.Len(t, engine.behaviorMemory.cache, 2)
}

func TestBehavioralBaselineRecurringNetworkBurstDowngradesJointRiskSignal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralSeries(store, "collector-artifact-batch", "artifact-upload-worker", now, metricWindow(now, map[string][]float64{
		"node_network_total_receive_bytes_per_second":  {18 * 1024 * 1024, 22 * 1024 * 1024, 28 * 1024 * 1024, 36 * 1024 * 1024, 45 * 1024 * 1024, 52 * 1024 * 1024, 58 * 1024 * 1024},
		"node_network_total_transmit_bytes_per_second": {20 * 1024 * 1024, 24 * 1024 * 1024, 30 * 1024 * 1024, 38 * 1024 * 1024, 48 * 1024 * 1024, 56 * 1024 * 1024, 64 * 1024 * 1024},
		"node_cpu_usage_percent":                       {28, 30, 33, 37, 42, 46, 49},
		"node_memory_MemTotal_bytes":                   {float64(16 * 1024 * 1024 * 1024)},
		"node_memory_Used_bytes":                       {float64(7 * 1024 * 1024 * 1024), float64(7.2 * 1024 * 1024 * 1024), float64(7.4 * 1024 * 1024 * 1024), float64(7.5 * 1024 * 1024 * 1024), float64(7.6 * 1024 * 1024 * 1024), float64(7.7 * 1024 * 1024 * 1024), float64(7.8 * 1024 * 1024 * 1024)},
	}))

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-artifact-batch", recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"node_network_total_receive_bytes_per_second":  {17 * 1024 * 1024, 21 * 1024 * 1024, 27 * 1024 * 1024, 35 * 1024 * 1024, 44 * 1024 * 1024, 52 * 1024 * 1024, 58 * 1024 * 1024},
		"node_network_total_transmit_bytes_per_second": {19 * 1024 * 1024, 23 * 1024 * 1024, 29 * 1024 * 1024, 37 * 1024 * 1024, 47 * 1024 * 1024, 55 * 1024 * 1024, 63 * 1024 * 1024},
		"node_cpu_usage_percent":                       {27, 29, 32, 36, 41, 45, 48},
		"node_memory_MemTotal_bytes":                   {float64(16 * 1024 * 1024 * 1024)},
		"node_memory_Used_bytes":                       {float64(7 * 1024 * 1024 * 1024), float64(7.1 * 1024 * 1024 * 1024), float64(7.3 * 1024 * 1024 * 1024), float64(7.4 * 1024 * 1024 * 1024), float64(7.5 * 1024 * 1024 * 1024), float64(7.6 * 1024 * 1024 * 1024), float64(7.7 * 1024 * 1024 * 1024)},
	})))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-artifact-batch",
		Window:      45 * time.Minute,
		Trigger:     "scheduled_batch",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "network_throughput")
	require.Equal(t, "expected_recurring_burst", assessment.Classification)
	require.Contains(t, strings.ToLower(assessment.Explanation), "matches")

	signal := findJointRiskSignal(report.Signals, "network_throughput")
	require.Equal(t, "expected_recurring_burst", signal.BehavioralClassification)
	require.False(t, signal.Triggered)
}

func TestBehavioralBaselineGPUFaultEscalatesJointRiskSignal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralSeries(store, "collector-gpu-driver-fault", "trainer-gpu", now, metricWindow(now, map[string][]float64{
		"node_gpu_utilization_sm_avg_percent": {74, 82, 90, 95, 96, 97, 96},
		"node_gpu_memory_used_percent":        {70, 79, 87, 92, 94, 95, 94},
		"node_cpu_usage_percent":              {36, 40, 44, 48, 50, 52, 54},
		"node_memory_MemTotal_bytes":          {float64(32 * 1024 * 1024 * 1024)},
		"node_memory_Used_bytes":              {float64(12 * 1024 * 1024 * 1024), float64(12.2 * 1024 * 1024 * 1024), float64(12.4 * 1024 * 1024 * 1024), float64(12.6 * 1024 * 1024 * 1024), float64(12.8 * 1024 * 1024 * 1024), float64(13 * 1024 * 1024 * 1024), float64(13.2 * 1024 * 1024 * 1024)},
	}))
	index.AddBatch([]logindex.RawEvent{{
		Timestamp:   now.Add(-2 * time.Minute),
		CollectorID: "collector-gpu-driver-fault",
		Hostname:    "collector-gpu-driver-fault-host",
		Service:     "trainer-gpu",
		Process:     "trainer-gpu",
		Level:       "error",
		Source:      "app",
		Message:     "deployment warn timeout error NVRM Xid 79 GPU has fallen off the bus",
		Count:       3,
	}})

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-gpu-driver-fault", recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"node_gpu_utilization_sm_avg_percent": {74, 82, 90, 95, 96, 97, 96},
		"node_gpu_memory_used_percent":        {70, 79, 87, 92, 94, 95, 94},
		"node_cpu_usage_percent":              {35, 39, 43, 47, 49, 51, 53},
		"node_memory_MemTotal_bytes":          {float64(32 * 1024 * 1024 * 1024)},
		"node_memory_Used_bytes":              {float64(12 * 1024 * 1024 * 1024), float64(12.2 * 1024 * 1024 * 1024), float64(12.4 * 1024 * 1024 * 1024), float64(12.6 * 1024 * 1024 * 1024), float64(12.8 * 1024 * 1024 * 1024), float64(13 * 1024 * 1024 * 1024), float64(13.2 * 1024 * 1024 * 1024)},
	})))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-gpu-driver-fault",
		Window:      45 * time.Minute,
		Trigger:     "gpu_fault",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "gpu_utilization")
	require.Equal(t, "confirmed_anomaly", assessment.Classification)
	require.Contains(t, strings.ToLower(assessment.Explanation), "gpu fault")
	require.Contains(t, assessment.CrossSignalSupport, "gpu_fault_signal")

	signal := findJointRiskSignal(report.Signals, "gpu_utilization")
	require.True(t, signal.Triggered)
	require.Equal(t, "confirmed_anomaly", signal.BehavioralClassification)
}

func TestBehavioralBaselineDeploymentLogBurstDowngradesJointRiskSignal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralSeries(store, "collector-deploy-log-burst", "deploy-agent", now, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {22, 24, 26, 30, 34, 36, 38},
		"memory_used_percent":    {48, 49, 50, 52, 53, 53, 54},
	}))
	seedLogTimelineEvents(index, "collector-deploy-log-burst", "deploy-agent", now, "warn", []uint64{2, 4, 6, 18, 26, 34, 38},
		"error warn timeout deployment rollout restart warming cache")

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-deploy-log-burst", recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"service_log_burst_count": {2, 3, 5, 17, 25, 35, 39},
		"node_cpu_usage_percent":  {22, 24, 26, 29, 33, 35, 37},
		"memory_used_percent":     {48, 49, 50, 51, 52, 53, 53},
	})))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-deploy-log-burst",
		Window:      45 * time.Minute,
		Trigger:     "deployment_rollout",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "log_burst")
	require.Equal(t, "expected_recurring_burst", assessment.Classification)
	require.Contains(t, strings.ToLower(assessment.Explanation), "similar spikes")

	signal := findJointRiskSignal(report.Signals, "log_burst")
	require.Equal(t, "expected_recurring_burst", signal.BehavioralClassification)
	require.False(t, signal.Triggered)
}

func TestBehavioralBaselineOOMKillEscalatesMemoryPressureJointRiskSignal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralSeries(store, "collector-oom-workflow", "api-memory-bound", now, metricWindow(now, map[string][]float64{
		"memory_used_percent":    {84, 88, 92, 95, 97, 98, 99},
		"node_cpu_usage_percent": {24, 26, 28, 31, 34, 35, 36},
	}))
	seedLogTimelineEvents(index, "collector-oom-workflow", "api-memory-bound", now, "error", []uint64{0, 0, 1, 1, 2, 2, 3},
		"deployment warn timeout error OOMKilled after memory cgroup hit limit")

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-oom-workflow", recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"memory_used_percent":    {60, 62, 64, 66, 67, 68, 69},
		"node_cpu_usage_percent": {22, 24, 25, 26, 27, 28, 29},
	})))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-oom-workflow",
		Window:      45 * time.Minute,
		Trigger:     "oom_kill",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "memory_pressure")
	require.Equal(t, "confirmed_anomaly", assessment.Classification)
	require.Contains(t, strings.ToLower(assessment.Explanation), "oom")
	require.Contains(t, assessment.CrossSignalSupport, "oom_kill_signal")

	signal := findJointRiskSignal(report.Signals, "memory_pressure")
	require.Equal(t, "confirmed_anomaly", signal.BehavioralClassification)
	require.True(t, signal.Triggered)
}

func TestBehavioralBaselineModerateCPUAndLatencyWithErrorsEscalatesJointRiskSignal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralSeries(store, "collector-corroborated-cpu", "checkout-api", now, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {44, 46, 49, 58, 63, 67, 69},
		"service_latency_p95_ms": {110, 120, 150, 190, 240, 320, 480},
		"memory_used_percent":    {56, 57, 58, 59, 60, 61, 62},
	}))
	seedLogTimelineEvents(index, "collector-corroborated-cpu", "checkout-api", now, "error", []uint64{1, 2, 3, 5, 8, 12, 16},
		"error warn timeout deployment checkout requests failing under load")

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-corroborated-cpu", recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {42, 45, 48, 54, 57, 59, 60},
		"service_latency_p95_ms": {100, 102, 104, 105, 106, 108, 110},
		"memory_used_percent":    {55, 56, 57, 58, 59, 60, 61},
	})))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-corroborated-cpu",
		Window:      45 * time.Minute,
		Trigger:     "request_failures",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "cpu_pressure")
	require.Equal(t, "confirmed_anomaly", assessment.Classification)
	require.Contains(t, strings.ToLower(assessment.Explanation), "current evidence")
	require.Contains(t, assessment.CrossSignalSupport, "error_log_burst")

	signal := findJointRiskSignal(report.Signals, "cpu_pressure")
	require.Equal(t, "confirmed_anomaly", signal.BehavioralClassification)
	require.True(t, signal.Triggered)

	latencySignal := findJointRiskSignal(report.Signals, "service_latency")
	require.True(t, latencySignal.Triggered)
}

func TestBehavioralBaselinePeerReplicaOutlierPreventsBurstSuppression(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralSeries(store, "collector-checkout-a", "checkout-api", now, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {48, 52, 58, 74, 79, 82, 84},
		"service_latency_p95_ms": {95, 100, 110, 120, 118, 115, 112},
	}))
	seedBehavioralSeries(store, "collector-checkout-b", "checkout-api", now, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {28, 30, 31, 33, 34, 35, 36},
		"service_latency_p95_ms": {92, 94, 96, 98, 99, 100, 101},
	}))
	seedBehavioralSeries(store, "collector-checkout-c", "checkout-api", now, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {27, 29, 30, 32, 33, 34, 35},
		"service_latency_p95_ms": {91, 93, 95, 97, 98, 99, 100},
	}))

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-checkout-a", recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {46, 50, 56, 73, 78, 81, 83},
		"service_latency_p95_ms": {90, 96, 104, 112, 110, 108, 105},
	})))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-checkout-a",
		Window:      45 * time.Minute,
		Trigger:     "request_surge",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "cpu_pressure")
	require.Equal(t, "suspicious_deviation", assessment.Classification)
	require.Contains(t, assessment.CrossSignalSupport, "peer_outlier")
	require.Contains(t, strings.ToLower(assessment.Explanation), "peer")

	signal := findJointRiskSignal(report.Signals, "cpu_pressure")
	require.True(t, signal.Triggered)
	require.Equal(t, "suspicious_deviation", signal.BehavioralClassification)
}

func TestBehavioralBaselinePeerReplicaAgreementSupportsFleetWideBurst(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 45 * time.Minute
	cfg.BehaviorMemory.LongWindow = 14 * 24 * time.Hour
	cfg.BehaviorMemory.MinSamples = 8
	cfg.BehaviorMemory.MinRecurringBursts = 2
	cfg.BehaviorMemory.CacheEntries = 8

	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedBehavioralSeries(store, "collector-frontend-a", "frontend-api", now, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {48, 52, 58, 74, 79, 82, 84},
		"service_latency_p95_ms": {95, 100, 110, 120, 118, 115, 112},
	}))
	seedBehavioralSeries(store, "collector-frontend-b", "frontend-api", now, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {47, 51, 57, 73, 78, 81, 83},
		"service_latency_p95_ms": {94, 99, 108, 118, 116, 113, 111},
	}))
	seedBehavioralSeries(store, "collector-frontend-c", "frontend-api", now, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {49, 53, 59, 75, 80, 83, 85},
		"service_latency_p95_ms": {96, 101, 111, 121, 119, 116, 113},
	}))

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-frontend-a", recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {46, 50, 56, 73, 78, 81, 83},
		"service_latency_p95_ms": {90, 96, 104, 112, 110, 108, 105},
	})))

	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetMetricHistoryProvider(history)

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-frontend-a",
		Window:      45 * time.Minute,
		Trigger:     "request_surge",
	})
	require.NoError(t, err)

	assessment := findBehavioralAssessment(report.BehavioralAssessments, "cpu_pressure")
	require.Equal(t, "expected_recurring_burst", assessment.Classification)
	require.Contains(t, assessment.CrossSignalSupport, "peer_group_burst")
	require.Contains(t, strings.ToLower(assessment.Explanation), "peers")

	signal := findJointRiskSignal(report.Signals, "cpu_pressure")
	require.False(t, signal.Triggered)
	require.Equal(t, "expected_recurring_burst", signal.BehavioralClassification)
}

func TestBehavioralBaselinePracticalCases(t *testing.T) {
	now := time.Date(2026, 3, 23, 10, 30, 0, 0, time.UTC)
	cfg := DefaultBehavioralMemoryConfig()
	cfg.MinSamples = 8
	cfg.MinRecurringBursts = 2
	cfg.CacheEntries = 16
	cfg.CacheTTL = time.Hour

	cases := []struct {
		name               string
		collectorID        string
		service            string
		current            []ingest.MetricHistorySample
		history            []ingest.MetricHistorySample
		logs               logsToolData
		security           securityToolData
		ebpf               ebpfToolData
		changes            []RCAChangeLink
		signalID           string
		wantClass          string
		wantTriggered      bool
		wantReasonContains string
	}{
		{
			name:               "cpu build worker recurring burst",
			collectorID:        "collector-build-cases",
			service:            "build-compile-worker",
			current:            metricWindow(now, map[string][]float64{"node_cpu_usage_percent": {34, 36, 38, 72, 78, 82, 84}}),
			history:            recurringWindowHistory(now, 5, metricWindow(now, map[string][]float64{"node_cpu_usage_percent": {34, 36, 38, 71, 79, 83, 85}})),
			signalID:           "cpu_pressure",
			wantClass:          "expected_recurring_burst",
			wantTriggered:      false,
			wantReasonContains: "similar spikes",
		},
		{
			name:        "cpu traffic surge with acceptable latency stays bursty load",
			collectorID: "collector-traffic-surge",
			service:     "frontend-api",
			current: metricWindow(now, map[string][]float64{
				"node_cpu_usage_percent": {48, 52, 58, 74, 79, 82, 84},
				"service_latency_p95_ms": {95, 100, 110, 120, 118, 115, 112},
			}),
			history: recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
				"node_cpu_usage_percent": {46, 50, 56, 73, 78, 81, 83},
				"service_latency_p95_ms": {90, 96, 104, 112, 110, 108, 105},
			})),
			signalID:           "cpu_pressure",
			wantClass:          "expected_recurring_burst",
			wantTriggered:      false,
			wantReasonContains: "no corroborating",
		},
		{
			name:               "runaway cpu busy loop remains anomalous",
			collectorID:        "collector-busy-loop",
			service:            "checkout-api",
			current:            metricWindow(now, map[string][]float64{"node_cpu_usage_percent": {21, 23, 24, 88, 96, 99, 99}}),
			history:            recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{"node_cpu_usage_percent": {20, 22, 23, 24, 25, 24, 23}})),
			signalID:           "cpu_pressure",
			wantClass:          "confirmed_anomaly",
			wantTriggered:      true,
			wantReasonContains: "high-water",
		},
		{
			name:        "deployment temporary cpu spike is downgraded",
			collectorID: "collector-deploy-cpu",
			service:     "rollout-deploy-agent",
			current:     metricWindow(now, map[string][]float64{"node_cpu_usage_percent": {28, 30, 34, 62, 70, 74, 76}}),
			history:     recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{"node_cpu_usage_percent": {28, 31, 33, 61, 71, 75, 77}})),
			logs: logsToolData{
				RecentDeploys: []string{"deployment rollout started for deploy-agent"},
			},
			signalID:           "cpu_pressure",
			wantClass:          "expected_recurring_burst",
			wantTriggered:      false,
			wantReasonContains: "similar spikes",
		},
		{
			name:               "startup warmup memory increase is downgraded",
			collectorID:        "collector-startup-memory",
			service:            "startup-model-loader",
			current:            metricWindow(now, map[string][]float64{"memory_used_percent": {52, 58, 64, 72, 78, 79, 79}}),
			history:            recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{"memory_used_percent": {50, 56, 63, 71, 77, 78, 78}})),
			signalID:           "memory_pressure",
			wantClass:          "expected_recurring_burst",
			wantTriggered:      false,
			wantReasonContains: "similar spikes",
		},
		{
			name:               "gradual memory leak remains anomalous",
			collectorID:        "collector-memory-leak",
			service:            "api-service",
			current:            metricWindow(now, map[string][]float64{"memory_used_percent": {61, 66, 72, 79, 86, 92, 97}}),
			history:            recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{"memory_used_percent": {58, 59, 60, 61, 62, 63, 64}})),
			signalID:           "memory_leak_rate",
			wantClass:          "confirmed_anomaly",
			wantTriggered:      true,
			wantReasonContains: "memory leak",
		},
		{
			name:        "oom risk with kill signal escalates strongly",
			collectorID: "collector-oom-risk",
			service:     "api-memory-bound",
			current:     metricWindow(now, map[string][]float64{"memory_used_percent": {84, 88, 92, 95, 97, 98, 99}}),
			history:     recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{"memory_used_percent": {60, 62, 64, 66, 67, 68, 69}})),
			logs: logsToolData{
				Errors:   2,
				Snippets: []string{"container api-7d9d OOMKilled after memory cgroup hit limit"},
			},
			ebpf: ebpfToolData{
				BehaviorScore: 0.42,
				EventRate:     4,
			},
			signalID:           "memory_pressure",
			wantClass:          "confirmed_anomaly",
			wantTriggered:      true,
			wantReasonContains: "oom",
		},
		{
			name:        "node memory pressure with eviction symptoms is serious",
			collectorID: "collector-eviction",
			service:     "node-agent",
			current:     metricWindow(now, map[string][]float64{"memory_used_percent": {87, 90, 93, 95, 96, 97, 98}}),
			history:     recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{"memory_used_percent": {68, 69, 70, 71, 72, 73, 74}})),
			logs: logsToolData{
				Snippets: []string{"kubelet eviction manager: evicted pods due to node memory pressure"},
			},
			signalID:           "memory_pressure",
			wantClass:          "confirmed_anomaly",
			wantTriggered:      true,
			wantReasonContains: "eviction",
		},
		{
			name:        "expected gpu training workload is downgraded",
			collectorID: "collector-gpu-train",
			service:     "trainer-gpu",
			current: metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {74, 82, 90, 95, 96, 97, 96},
				"node_gpu_memory_used_percent":        {70, 79, 87, 92, 94, 95, 94},
			}),
			history: recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {73, 81, 89, 95, 96, 97, 96},
				"node_gpu_memory_used_percent":        {69, 78, 86, 92, 94, 95, 94},
			})),
			signalID:           "gpu_memory_pressure",
			wantClass:          "expected_recurring_burst",
			wantTriggered:      false,
			wantReasonContains: "similar spikes",
		},
		{
			name:        "unexpected gpu memory saturation is anomalous",
			collectorID: "collector-gpu-memory",
			service:     "serving-gpu",
			current: metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {44, 48, 52, 57, 60, 63, 64},
				"node_gpu_memory_used_percent":        {72, 82, 90, 95, 97, 98, 98},
			}),
			history: recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {35, 38, 40, 42, 44, 45, 46},
				"node_gpu_memory_used_percent":        {38, 40, 42, 43, 44, 45, 46},
			})),
			signalID:           "gpu_memory_pressure",
			wantClass:          "confirmed_anomaly",
			wantTriggered:      true,
			wantReasonContains: "high-water",
		},
		{
			name:        "gpu memory pinned while utilization stays low is suspicious retention",
			collectorID: "collector-gpu-pinned",
			service:     "model-serving-gpu",
			current: metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {12, 11, 10, 9, 8, 8, 7},
				"node_gpu_memory_used_percent":        {86, 89, 92, 95, 96, 96, 96},
			}),
			history: recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {24, 26, 28, 30, 29, 28, 27},
				"node_gpu_memory_used_percent":        {40, 42, 44, 46, 47, 48, 49},
			})),
			signalID:           "gpu_memory_pressure",
			wantClass:          "confirmed_anomaly",
			wantTriggered:      true,
			wantReasonContains: "pinned",
		},
		{
			name:        "gpu driver fault escalates even when utilization pattern is common",
			collectorID: "collector-gpu-fault",
			service:     "trainer-gpu",
			current: metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {74, 82, 90, 95, 96, 97, 96},
				"node_gpu_memory_used_percent":        {70, 79, 87, 92, 94, 95, 94},
			}),
			history: recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {74, 82, 90, 95, 96, 97, 96},
				"node_gpu_memory_used_percent":        {70, 79, 87, 92, 94, 95, 94},
			})),
			logs: logsToolData{
				Errors:   1,
				Snippets: []string{"NVRM: Xid 79, GPU has fallen off the bus"},
			},
			signalID:           "gpu_utilization",
			wantClass:          "confirmed_anomaly",
			wantTriggered:      true,
			wantReasonContains: "gpu fault",
		},
		{
			name:        "gpu profiling degradation escalates on latency and errors",
			collectorID: "collector-gpu-profile",
			service:     "inference-gpu",
			current: metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {72, 81, 88, 92, 94, 95, 95},
				"service_latency_p95_ms":              {120, 140, 180, 250, 310, 360, 420},
			}),
			history: recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
				"node_gpu_utilization_sm_avg_percent": {72, 81, 88, 92, 94, 95, 95},
				"service_latency_p95_ms":              {92, 94, 96, 98, 100, 102, 104},
			})),
			logs: logsToolData{
				Errors:   14,
				Timeline: logTimeline(now, []uint64{1, 1, 2, 5, 9, 14, 18}),
			},
			signalID:           "gpu_utilization",
			wantClass:          "correlated_anomaly",
			wantTriggered:      true,
			wantReasonContains: "current evidence",
		},
		{
			name:               "bursty workload with no downstream harm trends toward suppression",
			collectorID:        "collector-bursty-clean",
			service:            "batch-cleaner",
			current:            metricWindow(now, map[string][]float64{"node_cpu_usage_percent": {30, 34, 40, 68, 76, 82, 85}}),
			history:            recurringWindowHistory(now, 5, metricWindow(now, map[string][]float64{"node_cpu_usage_percent": {30, 34, 41, 69, 77, 83, 86}})),
			signalID:           "cpu_pressure",
			wantClass:          "expected_recurring_burst",
			wantTriggered:      false,
			wantReasonContains: "no corroborating",
		},
		{
			name:        "bursty workload with correlated harm still escalates",
			collectorID: "collector-bursty-harm",
			service:     "batch-cleaner",
			current: metricWindow(now, map[string][]float64{
				"node_cpu_usage_percent": {30, 34, 40, 68, 76, 82, 85},
				"service_latency_p95_ms": {95, 100, 108, 150, 220, 320, 430},
			}),
			history: recurringWindowHistory(now, 5, metricWindow(now, map[string][]float64{
				"node_cpu_usage_percent": {30, 34, 41, 69, 77, 83, 86},
				"service_latency_p95_ms": {92, 95, 98, 100, 102, 104, 106},
			})),
			logs: logsToolData{
				Errors:   12,
				Timeline: logTimeline(now, []uint64{0, 1, 2, 5, 8, 12, 16}),
			},
			signalID:           "cpu_pressure",
			wantClass:          "correlated_anomaly",
			wantTriggered:      true,
			wantReasonContains: "current evidence",
		},
		{
			name:        "backup upload network burst is downgraded",
			collectorID: "collector-artifact-upload",
			service:     "artifact-upload",
			current: metricWindow(now, map[string][]float64{
				"node_network_total_receive_bytes_per_second":  {8e6, 10e6, 12e6, 40e6, 58e6, 72e6, 85e6},
				"node_network_total_transmit_bytes_per_second": {6e6, 8e6, 10e6, 36e6, 54e6, 68e6, 82e6},
			}),
			history: recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
				"node_network_total_receive_bytes_per_second":  {8e6, 10e6, 12e6, 42e6, 60e6, 74e6, 86e6},
				"node_network_total_transmit_bytes_per_second": {6e6, 8e6, 10e6, 38e6, 56e6, 70e6, 84e6},
			})),
			signalID:           "network_throughput",
			wantClass:          "expected_recurring_burst",
			wantTriggered:      false,
			wantReasonContains: "similar spikes",
		},
		{
			name:        "deployment log burst is downgraded when historically common",
			collectorID: "collector-deploy-logs",
			service:     "deploy-agent",
			current:     metricWindow(now, map[string][]float64{"service_log_burst_count": {2, 4, 6, 18, 26, 34, 38}}),
			history:     recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{"service_log_burst_count": {2, 3, 5, 17, 25, 35, 39}})),
			logs: logsToolData{
				Warnings:      34,
				Timeline:      logTimeline(now, []uint64{2, 4, 6, 18, 26, 34, 38}),
				RecentDeploys: []string{"deployment rollout restarted deploy-agent pods"},
				Snippets:      []string{"startup complete", "migrating cache", "warming service"},
			},
			signalID:           "log_burst",
			wantClass:          "expected_recurring_burst",
			wantTriggered:      false,
			wantReasonContains: "similar spikes",
		},
		{
			name:        "moderate cpu deviation plus strong logs and latency evidence escalates",
			collectorID: "collector-moderate-corroborated",
			service:     "checkout-api",
			current: metricWindow(now, map[string][]float64{
				"node_cpu_usage_percent": {44, 46, 49, 58, 63, 67, 69},
				"service_latency_p95_ms": {110, 120, 150, 190, 240, 320, 480},
			}),
			history: recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
				"node_cpu_usage_percent": {42, 45, 48, 54, 57, 59, 60},
				"service_latency_p95_ms": {100, 102, 104, 105, 106, 108, 110},
			})),
			logs: logsToolData{
				Errors:   16,
				Timeline: logTimeline(now, []uint64{1, 2, 3, 5, 8, 12, 16}),
			},
			signalID:           "cpu_pressure",
			wantClass:          "correlated_anomaly",
			wantTriggered:      true,
			wantReasonContains: "current evidence",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			history := newStubMetricHistoryProvider()
			history.setSamples(tc.collectorID, tc.history)

			store := NewBehavioralMemoryStore(cfg, zap.NewNop())
			store.SetHistoryProvider(history)

			series := buildRiskSeries(tc.current, tc.logs)
			req := BehavioralMemoryRequest{
				CollectorID: tc.collectorID,
				Node:        newBehaviorNode(tc.collectorID, tc.service),
				Series:      series,
				Logs:        tc.logs,
				Security:    tc.security,
				EBPF:        tc.ebpf,
				ChangeLinks: tc.changes,
				Now:         now,
			}

			assessments := store.Evaluate(req)
			assessment := findBehavioralAssessment(assessments, tc.signalID)
			require.Equal(t, tc.wantClass, assessment.Classification)
			require.Contains(t, strings.ToLower(assessment.Explanation), strings.ToLower(tc.wantReasonContains))

			signal := findJointRiskSignal(buildRiskSignals(tc.collectorID, series, tc.security, tc.ebpf, behavioralAssessmentIndex(assessments)), tc.signalID)
			require.Equal(t, tc.wantClass, signal.BehavioralClassification)
			require.Equal(t, tc.wantTriggered, signal.Triggered)
		})
	}
}

func TestBehavioralBaselineIdentityIsolationAcrossEntities(t *testing.T) {
	now := time.Date(2026, 3, 23, 11, 0, 0, 0, time.UTC)
	cfg := DefaultBehavioralMemoryConfig()
	cfg.MinSamples = 8
	cfg.MinRecurringBursts = 2

	window := metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {32, 35, 40, 70, 78, 83, 86},
	})
	recurring := recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {32, 35, 40, 71, 79, 84, 87},
	}))
	novel := recurringWindowHistory(now, 4, metricWindow(now, map[string][]float64{
		"node_cpu_usage_percent": {22, 24, 26, 28, 30, 31, 32},
	}))

	history := newStubMetricHistoryProvider()
	history.setSamples("collector-build-a", recurring)
	history.setSamples("collector-build-b", novel)

	store := NewBehavioralMemoryStore(cfg, zap.NewNop())
	store.SetHistoryProvider(history)

	series := buildRiskSeries(window, logsToolData{})
	buildA := findBehavioralAssessment(store.Evaluate(BehavioralMemoryRequest{
		CollectorID: "collector-build-a",
		Node:        newBehaviorNode("collector-build-a", "build-compile-worker"),
		Series:      series,
		Now:         now,
	}), "cpu_pressure")
	buildB := findBehavioralAssessment(store.Evaluate(BehavioralMemoryRequest{
		CollectorID: "collector-build-b",
		Node:        newBehaviorNode("collector-build-b", "build-compile-worker"),
		Series:      series,
		Now:         now,
	}), "cpu_pressure")

	require.Equal(t, "expected_recurring_burst", buildA.Classification)
	require.NotEqual(t, "expected_recurring_burst", buildB.Classification)
}

type stubMetricHistoryProvider struct {
	mu      sync.Mutex
	calls   int
	samples map[string][]ingest.MetricHistorySample
}

func newStubMetricHistoryProvider() *stubMetricHistoryProvider {
	return &stubMetricHistoryProvider{samples: map[string][]ingest.MetricHistorySample{}}
}

func (s *stubMetricHistoryProvider) setSamples(collectorID string, samples []ingest.MetricHistorySample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := append([]ingest.MetricHistorySample(nil), samples...)
	sort.Slice(cloned, func(i, j int) bool { return cloned[i].Timestamp.Before(cloned[j].Timestamp) })
	s.samples[collectorID] = cloned
}

func (s *stubMetricHistoryProvider) MetricHistory(collectorID string, since time.Time, limit int) []ingest.MetricHistorySample {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	rows := s.samples[collectorID]
	if len(rows) == 0 {
		return nil
	}
	out := make([]ingest.MetricHistorySample, 0, len(rows))
	for _, row := range rows {
		if !since.IsZero() && row.Timestamp.Before(since) {
			continue
		}
		out = append(out, cloneMetricHistorySample(row))
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (s *stubMetricHistoryProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func seedBehavioralNode(store *ingest.MemoryStore, collectorID, service string, now time.Time, cpu float64) {
	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: collectorID,
		Hostname:    collectorID + "-host",
		Labels: []*telemetryv1.Label{
			{Key: "service", Value: service},
		},
	}, now)
	store.StoreMetrics(collectorID, []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: cpu, TimestampUnixNano: now.UnixNano()},
		{Name: "node_memory_MemTotal_bytes", Value: float64(16 * 1024 * 1024 * 1024), TimestampUnixNano: now.UnixNano()},
		{Name: "node_memory_Used_bytes", Value: float64(8 * 1024 * 1024 * 1024), TimestampUnixNano: now.UnixNano()},
	}, now)
}

func seedBehavioralSeries(store *ingest.MemoryStore, collectorID, service string, now time.Time, samples []ingest.MetricHistorySample) {
	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: collectorID,
		Hostname:    collectorID + "-host",
		Labels: []*telemetryv1.Label{
			{Key: "service", Value: service},
		},
	}, now)
	for _, sample := range samples {
		metrics := make([]*telemetryv1.Metric, 0, len(sample.Metrics))
		for name, value := range sample.Metrics {
			metrics = append(metrics, &telemetryv1.Metric{
				Name:              name,
				Value:             value,
				TimestampUnixNano: sample.Timestamp.UnixNano(),
			})
		}
		store.StoreMetrics(collectorID, metrics, sample.Timestamp)
	}
}

func recurringCPUHistory(now time.Time, burst float64, recurringDays int, withLatency bool) []ingest.MetricHistorySample {
	samples := make([]ingest.MetricHistorySample, 0, (recurringDays+1)*7)
	for day := recurringDays; day >= 0; day-- {
		ts := now.Add(-time.Duration(day) * 24 * time.Hour)
		samples = append(samples, cpuBurstWindow(ts, burst, withLatency)...)
	}
	return samples
}

func sparseCPUHistory(now time.Time, burst float64) []ingest.MetricHistorySample {
	samples := cpuBurstWindow(now, burst, false)
	samples = append(samples,
		ingest.MetricHistorySample{Timestamp: now.Add(-72 * time.Hour), Metrics: map[string]float64{"node_cpu_usage_percent": 34}},
		ingest.MetricHistorySample{Timestamp: now.Add(-48 * time.Hour), Metrics: map[string]float64{"node_cpu_usage_percent": 35}},
	)
	sort.Slice(samples, func(i, j int) bool { return samples[i].Timestamp.Before(samples[j].Timestamp) })
	return samples
}

func cpuBurstWindow(now time.Time, burst float64, withLatency bool) []ingest.MetricHistorySample {
	values := []float64{34, 35, 36, 38, burst - 2, burst - 1, burst}
	out := make([]ingest.MetricHistorySample, 0, len(values))
	for i, value := range values {
		ts := now.Add(time.Duration(i-len(values)+1) * time.Minute)
		metrics := map[string]float64{
			"node_cpu_usage_percent":     value,
			"node_memory_MemTotal_bytes": float64(16 * 1024 * 1024 * 1024),
			"node_memory_Used_bytes":     float64((8 + i) * 1024 * 1024 * 1024 / 1),
		}
		if withLatency {
			metrics["service_latency_p95_ms"] = 320 + float64(i*10)
		}
		out = append(out, ingest.MetricHistorySample{Timestamp: ts, Metrics: metrics})
	}
	return out
}

func cloneMetricHistorySample(in ingest.MetricHistorySample) ingest.MetricHistorySample {
	out := ingest.MetricHistorySample{
		Timestamp: in.Timestamp,
		Metrics:   map[string]float64{},
	}
	for key, value := range in.Metrics {
		out.Metrics[key] = value
	}
	return out
}

func findBehavioralAssessment(items []BehavioralSignalAssessment, signalID string) BehavioralSignalAssessment {
	for _, item := range items {
		if item.SignalID == signalID {
			return item
		}
	}
	return BehavioralSignalAssessment{}
}

func findJointRiskSignal(items []JointRiskSignal, signalID string) JointRiskSignal {
	for _, item := range items {
		if item.ID == signalID {
			return item
		}
	}
	return JointRiskSignal{}
}

func newBehaviorNode(collectorID, service string) *ingest.NodeSnapshot {
	return &ingest.NodeSnapshot{
		CollectorID: collectorID,
		Hostname:    collectorID + "-host",
		Labels: map[string]string{
			"service": service,
		},
	}
}

func metricWindow(now time.Time, values map[string][]float64) []ingest.MetricHistorySample {
	length := 0
	for _, seq := range values {
		if len(seq) > length {
			length = len(seq)
		}
	}
	out := make([]ingest.MetricHistorySample, 0, length)
	for i := 0; i < length; i++ {
		metrics := map[string]float64{}
		for key, seq := range values {
			if i >= len(seq) {
				continue
			}
			metrics[key] = seq[i]
		}
		ts := now.Add(time.Duration(i-length+1) * time.Minute)
		out = append(out, ingest.MetricHistorySample{Timestamp: ts, Metrics: metrics})
	}
	return out
}

func recurringWindowHistory(now time.Time, recurringDays int, window []ingest.MetricHistorySample) []ingest.MetricHistorySample {
	out := make([]ingest.MetricHistorySample, 0, len(window)*(recurringDays+1))
	for day := recurringDays; day >= 0; day-- {
		shift := -time.Duration(day) * 24 * time.Hour
		for _, sample := range window {
			clone := cloneMetricHistorySample(sample)
			clone.Timestamp = clone.Timestamp.Add(shift)
			out = append(out, clone)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}

func logTimeline(now time.Time, counts []uint64) []logindex.TimelineBucket {
	out := make([]logindex.TimelineBucket, 0, len(counts))
	for i, count := range counts {
		end := now.Add(time.Duration(i-len(counts)+1) * time.Minute)
		out = append(out, logindex.TimelineBucket{
			End:      end,
			Errors:   count,
			Warnings: 0,
		})
	}
	return out
}

func seedLogTimelineEvents(index *logindex.Index, collectorID, service string, now time.Time, level string, counts []uint64, message string) {
	if index == nil {
		return
	}
	events := make([]logindex.RawEvent, 0, len(counts))
	for i, count := range counts {
		if count == 0 {
			continue
		}
		ts := now.Add(time.Duration(i-len(counts)+1) * time.Minute)
		events = append(events, logindex.RawEvent{
			Timestamp:   ts,
			CollectorID: collectorID,
			Hostname:    collectorID + "-host",
			Service:     service,
			Process:     service,
			Level:       level,
			Source:      "app",
			Message:     message,
			Count:       count,
		})
	}
	index.AddBatch(events)
}
