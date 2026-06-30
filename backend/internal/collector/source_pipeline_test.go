package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probecore"
	probeipcv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/probeipc/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeProbeCoreRuntime struct {
	startErr error
	batch    *probeipcv1.ProbeBatch
	ok       bool
	started  int
	stopped  int
}

func (f *fakeProbeCoreRuntime) Start(context.Context) error {
	f.started++
	return f.startErr
}

func (f *fakeProbeCoreRuntime) Stop() {
	f.stopped++
}

func (f *fakeProbeCoreRuntime) Latest(time.Duration) (*probeipcv1.ProbeBatch, bool) {
	return f.batch, f.ok
}

func (f *fakeProbeCoreRuntime) Stats() probecore.Stats {
	return probecore.Stats{}
}

type fakeCompatibilityProbe struct {
	batch      *probe.MetricBatch
	collectErr error
	started    int
	stopped    int
	collected  int
}

func (f *fakeCompatibilityProbe) Start() {
	f.started++
}

func (f *fakeCompatibilityProbe) Stop() {
	f.stopped++
}

func (f *fakeCompatibilityProbe) Collect() (*probe.MetricBatch, error) {
	f.collected++
	if f.collectErr != nil {
		return nil, f.collectErr
	}
	return f.batch, nil
}

func TestSourcePipelineUsesProbeCoreWhenFresh(t *testing.T) {
	primary := &fakeProbeCoreRuntime{
		batch: &probeipcv1.ProbeBatch{
			Metrics: []*probeipcv1.Metric{{Name: "probe_core_cpu_usage_percent", Value: 87.5}},
		},
		ok: true,
	}
	compat := &fakeCompatibilityProbe{
		batch: &probe.MetricBatch{
			Metrics: []probe.Metric{{Name: "node_cpu_usage_percent", Value: 12}},
		},
	}
	pipeline := newSourcePipeline(primary, compat, zap.NewNop())
	cfg := DefaultConfig()

	require.NoError(t, pipeline.Start(context.Background(), cfg))

	data, err := pipeline.Collect(time.Now(), cfg)
	require.NoError(t, err)
	require.Equal(t, "probe_core", data.source)
	require.True(t, data.primaryExpected)
	require.True(t, data.primaryHealthy)
	require.False(t, data.compatibilityFallback)
	require.NotEmpty(t, data.metrics)
	require.Zero(t, compat.started)
	require.Zero(t, compat.collected)
}

func TestSourcePipelineActivatesCompatibilityFallbackWhenProbeCoreStartFails(t *testing.T) {
	primary := &fakeProbeCoreRuntime{startErr: errors.New("boom")}
	compat := &fakeCompatibilityProbe{
		batch: &probe.MetricBatch{
			Metrics: []probe.Metric{{Name: "node_cpu_usage_percent", Value: 12}},
		},
	}
	pipeline := newSourcePipeline(primary, compat, zap.NewNop())
	cfg := DefaultConfig()
	cfg.ProbeCore.FallbackToGo = true

	require.NoError(t, pipeline.Start(context.Background(), cfg))

	data, err := pipeline.Collect(time.Now(), cfg)
	require.NoError(t, err)
	require.Equal(t, "go", data.source)
	require.True(t, data.primaryExpected)
	require.False(t, data.primaryHealthy)
	require.True(t, data.compatibilityFallback)
	require.Equal(t, "probe_core_start_failed", data.fallbackReason)
	require.Equal(t, 1, compat.started)
	require.Equal(t, 1, compat.collected)
}

func TestSourcePipelineActivatesCompatibilityFallbackWhenProbeCoreTurnsStale(t *testing.T) {
	primary := &fakeProbeCoreRuntime{}
	compat := &fakeCompatibilityProbe{
		batch: &probe.MetricBatch{
			Metrics: []probe.Metric{{Name: "node_cpu_usage_percent", Value: 12}},
		},
	}
	pipeline := newSourcePipeline(primary, compat, zap.NewNop())
	cfg := DefaultConfig()
	cfg.ProbeCore.FallbackToGo = true

	require.NoError(t, pipeline.Start(context.Background(), cfg))

	data, err := pipeline.Collect(time.Now(), cfg)
	require.NoError(t, err)
	require.Equal(t, "go", data.source)
	require.True(t, data.primaryExpected)
	require.False(t, data.primaryHealthy)
	require.True(t, data.compatibilityFallback)
	require.Equal(t, "probe_core_stale", data.fallbackReason)
	require.Equal(t, 1, compat.started)
	require.Equal(t, 1, compat.collected)
}

func TestSourcePipelineFailsWhenProbeCoreIsUnavailableAndFallbackDisabled(t *testing.T) {
	primary := &fakeProbeCoreRuntime{startErr: errors.New("boom")}
	pipeline := newSourcePipeline(primary, nil, zap.NewNop())
	cfg := DefaultConfig()
	cfg.ProbeCore.FallbackToGo = false

	err := pipeline.Start(context.Background(), cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "start probe-core primary source")
}

func TestSourcePipelineMarksProbeCoreExpectedWhenClientIsUnavailable(t *testing.T) {
	compat := &fakeCompatibilityProbe{
		batch: &probe.MetricBatch{
			Metrics: []probe.Metric{{Name: "node_cpu_usage_percent", Value: 12}},
		},
	}
	pipeline := newSourcePipeline(nil, compat, zap.NewNop())
	cfg := DefaultConfig()
	cfg.ProbeCore.Enabled = true
	cfg.ProbeCore.FallbackToGo = true

	require.NoError(t, pipeline.Start(context.Background(), cfg))

	data, err := pipeline.Collect(time.Now(), cfg)
	require.NoError(t, err)
	require.True(t, data.primaryExpected)
	require.False(t, data.primaryHealthy)
	require.True(t, data.compatibilityFallback)
	require.Equal(t, "probe_core_unavailable", data.fallbackReason)
}
