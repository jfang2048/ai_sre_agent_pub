package collector

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probecore"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
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
