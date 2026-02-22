package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/monitoring"
	"github.com/jfang2048/ai_sre_agent_pub/internal/monitoring/sources"
	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type runOnceSource struct {
	name  string
	batch *proto.MetricBatch
}

func (s *runOnceSource) Name() string { return s.name }
func (s *runOnceSource) Start(context.Context) error {
	return nil
}
func (s *runOnceSource) Stop() error { return nil }
func (s *runOnceSource) Status() sources.SourceStatus {
	return sources.SourceStatus{Name: s.name, Enabled: true, Running: true, Healthy: true}
}
func (s *runOnceSource) Collect(context.Context) (*proto.MetricBatch, error) {
	return s.batch, nil
}

func newAssessmentTestAgent(t *testing.T) *Agent {
	t.Helper()

	collector, err := monitoring.NewCollector(&monitoring.Config{}, zap.NewNop())
	require.NoError(t, err)

	return &Agent{
		logger:       zap.NewNop(),
		collector:    collector,
		stateMachine: NewMachine(zap.NewNop()),
	}
}

func TestGenerateAssessmentSetsReasoningForElevatedMemory(t *testing.T) {
	agent := newAssessmentTestAgent(t)

	assessment := agent.generateAssessment([]sources.Metric{
		{Name: "system.memory.total", Value: 100},
		{Name: "system.memory.used", Value: 85},
	})

	require.True(t, assessment.Healthy)
	require.Contains(t, assessment.Issues, "Memory elevated")
	require.True(t, strings.Contains(strings.ToLower(assessment.Reasoning), "memory"))
}

func TestGenerateAssessmentSetsReasoningForCriticalLoad(t *testing.T) {
	agent := newAssessmentTestAgent(t)

	assessment := agent.generateAssessment([]sources.Metric{
		{Name: "system.load.1m", Value: 12},
	})

	require.False(t, assessment.Healthy)
	require.Contains(t, assessment.Issues, "Load high")
	require.True(t, strings.Contains(strings.ToLower(assessment.Reasoning), "load"))
}

func TestMachineRejectsInvalidStateTransition(t *testing.T) {
	m := NewMachine(zap.NewNop())
	require.Equal(t, StateStopped, m.Current())

	m.Transition(StateRunning)
	require.Equal(t, StateStopped, m.Current())
}

func TestMetricsHistoryCopiesInputMap(t *testing.T) {
	history := NewMetricsHistory(4)
	input := map[string]float64{"cpu": 42}

	history.Add(input)
	input["cpu"] = 99

	points := history.GetMetricHistory("cpu", time.Now().Add(-time.Minute))
	require.Len(t, points, 1)
	require.Equal(t, 42.0, points[0].Value)
}

func TestSanitizeWebRelativePathRejectsTraversal(t *testing.T) {
	_, ok := sanitizeWebRelativePath("/../../etc/passwd")
	require.False(t, ok)
}

func TestSanitizeWebRelativePathDefaultsIndex(t *testing.T) {
	path, ok := sanitizeWebRelativePath("/")
	require.True(t, ok)
	require.Equal(t, "index.html", path)
}

func TestGetLatestMetricsReturnsDefensiveLabelCopies(t *testing.T) {
	agent := &Agent{
		latestMetrics: []sources.Metric{
			{
				Name:   "node_network_receive_bytes_per_second",
				Value:  1200,
				Labels: map[string]string{"device": "eth0"},
			},
		},
	}

	first := agent.GetLatestMetrics()
	require.Len(t, first, 1)
	first[0].Labels["device"] = "lo"
	first[0].Labels["new"] = "mutated"

	second := agent.GetLatestMetrics()
	require.Len(t, second, 1)
	require.Equal(t, "eth0", second[0].Labels["device"])
	require.NotContains(t, second[0].Labels, "new")
}

func TestRunOnceStoresDefensiveMetricsSnapshot(t *testing.T) {
	collector, err := monitoring.NewCollector(&monitoring.Config{}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&runOnceSource{
		name: "run-once-src",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{
					Name:   "node_network_receive_bytes_per_second",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Points: []*proto.MetricPoint{{Value: 42}},
					Labels: []*proto.MetricLabel{{Key: "device", Value: "eth0"}},
				},
			},
		},
	})

	agent := &Agent{
		collector: collector,
	}

	metrics, err := agent.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)

	metrics[0].Labels["device"] = "mutated"
	latest := agent.GetLatestMetrics()
	require.Len(t, latest, 1)
	require.Equal(t, "eth0", latest[0].Labels["device"])
}

func TestSetLatestMetricsRebuildsIndexesAndLatestValues(t *testing.T) {
	agent := &Agent{
		latestIndex:  map[string]int{"stale": 9},
		latestValues: map[string]float64{"stale": 99},
	}

	agent.setLatestMetrics([]sources.Metric{
		{Name: "node_cpu_usage_percent", Value: 31.5},
		{Name: "node_network_receive_bytes_per_second", Value: 2048, Labels: map[string]string{"device": "eth0"}},
	})

	require.Len(t, agent.latestMetrics, 2)
	require.Equal(t, map[string]int{
		metricKey("node_cpu_usage_percent", nil):                                                0,
		metricKey("node_network_receive_bytes_per_second", map[string]string{"device": "eth0"}): 1,
	}, agent.latestIndex)
	require.Equal(t, map[string]float64{
		metricKey("node_cpu_usage_percent", nil):                                                31.5,
		metricKey("node_network_receive_bytes_per_second", map[string]string{"device": "eth0"}): 2048,
	}, agent.latestValues)
}

func TestSetLatestMetricsDeduplicatesByMetricKeyKeepingLatestValue(t *testing.T) {
	agent := &Agent{}

	agent.setLatestMetrics([]sources.Metric{
		{Name: "node_cpu_usage_percent", Value: 20},
		{Name: "node_cpu_usage_percent", Value: 55},
		{Name: "node_network_receive_bytes_per_second", Value: 100, Labels: map[string]string{"device": "eth0"}},
		{Name: "node_network_receive_bytes_per_second", Value: 900, Labels: map[string]string{"device": "eth0"}},
	})

	require.Len(t, agent.latestMetrics, 2)
	require.Equal(t, 55.0, agent.latestValues[metricKey("node_cpu_usage_percent", nil)])
	require.Equal(t, 900.0, agent.latestValues[metricKey("node_network_receive_bytes_per_second", map[string]string{"device": "eth0"})])
}
