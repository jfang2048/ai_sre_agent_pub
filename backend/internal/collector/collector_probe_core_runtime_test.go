package collector

import (
	"errors"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probecore"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestResolveProbeCoreModuleSelectionDefaultsToAll(t *testing.T) {
	modules, valid := resolveProbeCoreModuleSelection(ProbeCoreConfig{})
	require.True(t, valid)
	require.Equal(t, moduleSet(probeCoreCollectorModuleOrder), modules)
}

func TestResolveProbeCoreModuleSelectionFromCollectors(t *testing.T) {
	modules, valid := resolveProbeCoreModuleSelection(ProbeCoreConfig{
		Collectors: []string{"network", "process"},
	})
	require.True(t, valid)
	require.Equal(t, moduleSet([]string{"host", "network", "process"}), modules)
}

func TestResolveProbeCoreModuleSelectionFromArgs(t *testing.T) {
	modules, valid := resolveProbeCoreModuleSelection(ProbeCoreConfig{
		Args: []string{"--foo=1", "--collectors=rdma,network"},
	})
	require.True(t, valid)
	require.Equal(t, moduleSet([]string{"network", "rdma"}), modules)
}

func TestResolveProbeCoreModuleSelectionInvalidExplicitSelection(t *testing.T) {
	modules, valid := resolveProbeCoreModuleSelection(ProbeCoreConfig{
		Args: []string{"--collectors=not-real"},
	})
	require.False(t, valid)
	require.Empty(t, modules)
}

func TestAppendProbeCoreRuntimeMetricsEmitsModuleRequestedAndActive(t *testing.T) {
	now := time.Now()
	collector := &Collector{
		cfg: Config{
			ProbeCore: ProbeCoreConfig{
				Enabled:    true,
				Collectors: []string{"network", "rdma"},
				StaleAfter: 10 * time.Second,
			},
		},
		probeCore: &probecore.Client{},
	}
	metrics := make([]*telemetryv1.Metric, 0, 64)

	collector.appendProbeCoreRuntimeMetrics(now, "probe_core", collector.cfg.ProbeCore, &metrics)

	clientAvailable, ok := metricValueWithLabels(metrics, "collector_probe_core_client_available", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, clientAvailable)

	probeCoreActive, ok := metricValueWithLabels(metrics, "collector_probe_core_active", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, probeCoreActive)

	selectionValid, ok := metricValueWithLabels(metrics, "collector_probe_core_collector_selection_valid", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, selectionValid)

	requestedNetwork, ok := metricValueWithLabels(metrics, "collector_probe_core_collector_module_requested", map[string]string{"module": "network"})
	require.True(t, ok)
	require.Equal(t, 1.0, requestedNetwork)

	requestedHost, ok := metricValueWithLabels(metrics, "collector_probe_core_collector_module_requested", map[string]string{"module": "host"})
	require.True(t, ok)
	require.Equal(t, 0.0, requestedHost)

	activeRDMA, ok := metricValueWithLabels(metrics, "collector_probe_core_collector_module_active", map[string]string{"module": "rdma"})
	require.True(t, ok)
	require.Equal(t, 1.0, activeRDMA)
}

func TestAppendProbeCoreRuntimeMetricsSelectionInvalidWhenArgsMalformed(t *testing.T) {
	now := time.Now()
	collector := &Collector{
		cfg: Config{
			ProbeCore: ProbeCoreConfig{
				Enabled: true,
				Args:    []string{"--collectors=not-real"},
			},
		},
	}
	metrics := make([]*telemetryv1.Metric, 0, 32)

	collector.appendProbeCoreRuntimeMetrics(now, "go", collector.cfg.ProbeCore, &metrics)

	selectionValid, ok := metricValueWithLabels(metrics, "collector_probe_core_collector_selection_valid", nil)
	require.True(t, ok)
	require.Equal(t, 0.0, selectionValid)

	activeNetwork, ok := metricValueWithLabels(metrics, "collector_probe_core_collector_module_active", map[string]string{"module": "network"})
	require.True(t, ok)
	require.Equal(t, 0.0, activeNetwork)
}

func TestAppendSourcePipelineMetricsMarksCompatibilityFallback(t *testing.T) {
	now := time.Now()
	collector := &Collector{}
	metrics := make([]*telemetryv1.Metric, 0, 16)

	collector.appendSourcePipelineMetrics(now, sourceCollection{
		source:                "go",
		compatibilityFallback: true,
		fallbackReason:        "probe_core_stale",
		primaryExpected:       true,
		primaryHealthy:        false,
	}, &metrics)

	value, ok := metricValueWithLabels(metrics, "collector_primary_probe_core_expected", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_primary_probe_core_healthy", nil)
	require.True(t, ok)
	require.Equal(t, 0.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_compatibility_fallback_active", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_compatibility_fallback_reason", map[string]string{"reason": "probe_core_stale"})
	require.True(t, ok)
	require.Equal(t, 1.0, value)
}

func TestAppendRuntimeModeMetricsIncludesPrivilegeProfile(t *testing.T) {
	now := time.Now()
	collector := &Collector{
		cfg: Config{
			PrivilegeProfile: PrivilegeProfileObservability,
			EBPF:             EBPFConfig{Enabled: false},
			ProbeCore:        ProbeCoreConfig{Enabled: false},
			Security:         SecurityConfig{Enabled: false},
		},
		runtimeMode: collectorRuntimeInspection{
			RequestedMode: runtimeModeLimited,
			AppliedMode:   runtimeModeLimited,
		},
	}
	metrics := make([]*telemetryv1.Metric, 0, 32)

	collector.appendRuntimeModeMetrics(now, &metrics)

	value, ok := metricValueWithLabels(metrics, "collector_privilege_profile", map[string]string{"profile": PrivilegeProfileObservability})
	require.True(t, ok)
	require.Equal(t, 1.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_privilege_profile_feature_enabled", map[string]string{"feature": "runtime_security"})
	require.True(t, ok)
	require.Equal(t, 0.0, value)
}

func TestCollectPrimaryEBPFMetricsSkipsCompatibilityFallbackAndDuplicates(t *testing.T) {
	now := time.Now()
	collector := &Collector{
		ebpfRuntime: fakePrimaryEBPFRuntime{
			metrics: []probe.Metric{{Name: "node_ebpf_events_total", Value: 5, Timestamp: now}},
		},
	}

	metrics := collector.collectPrimaryEBPFMetrics(now, nil, false)
	require.Len(t, metrics, 1)

	metrics = collector.collectPrimaryEBPFMetrics(now, []*telemetryv1.Metric{{Name: "node_ebpf_events_total"}}, false)
	require.Empty(t, metrics)

	metrics = collector.collectPrimaryEBPFMetrics(now, nil, true)
	require.Empty(t, metrics)
}

func TestStartPrimaryEBPFRuntimeDegradesInsteadOfFailingCollector(t *testing.T) {
	collector := &Collector{
		cfg: Config{
			EBPF: EBPFConfig{Enabled: true},
		},
		logger:       zap.NewNop(),
		ebpfRuntime:  fakePrimaryEBPFRuntime{startErr: errors.New("operation not permitted")},
		ebpfExpected: true,
		runtimeMode: collectorRuntimeInspection{
			AppliedMode: runtimeModeHost,
			CanUseEBPF:  true,
		},
	}

	stop := collector.startPrimaryEBPFRuntime()
	require.Nil(t, stop)
	require.Nil(t, collector.ebpfRuntime)

	expected, healthy, reason := collector.ebpfRuntimeStatus()
	require.True(t, expected)
	require.False(t, healthy)
	require.Equal(t, "start_failed", reason)

	now := time.Now()
	metrics := make([]*telemetryv1.Metric, 0, 8)
	collector.appendEBPFRuntimeMetrics(now, &metrics)

	value, ok := metricValueWithLabels(metrics, "collector_primary_ebpf_expected", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_primary_ebpf_healthy", nil)
	require.True(t, ok)
	require.Equal(t, 0.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_primary_ebpf_reason", map[string]string{"reason": "start_failed"})
	require.True(t, ok)
	require.Equal(t, 1.0, value)
}

func moduleSet(modules []string) map[string]struct{} {
	out := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		out[module] = struct{}{}
	}
	return out
}

func metricValueWithLabels(metrics []*telemetryv1.Metric, name string, labels map[string]string) (float64, bool) {
	for _, metric := range metrics {
		if metric.Name != name {
			continue
		}
		if labelsMatch(metric.Labels, labels) {
			return metric.Value, true
		}
	}
	return 0, false
}

func labelsMatch(metricLabels []*telemetryv1.Label, expected map[string]string) bool {
	if len(expected) == 0 {
		return len(metricLabels) == 0
	}
	if len(metricLabels) != len(expected) {
		return false
	}
	for _, label := range metricLabels {
		expectedValue, ok := expected[label.Key]
		if !ok || expectedValue != label.Value {
			return false
		}
	}
	return true
}

type fakePrimaryEBPFRuntime struct {
	metrics  []probe.Metric
	startErr error
}

func (f fakePrimaryEBPFRuntime) Start() error {
	return f.startErr
}

func (f fakePrimaryEBPFRuntime) Stop() {}

func (f fakePrimaryEBPFRuntime) GetMetrics(time.Time) []probe.Metric {
	return f.metrics
}

func (f fakePrimaryEBPFRuntime) Summary() probe.EBPFSummary {
	return probe.EBPFSummary{}
}

func (f fakePrimaryEBPFRuntime) Events(limit int) []probe.EBPFEvent {
	return nil
}
