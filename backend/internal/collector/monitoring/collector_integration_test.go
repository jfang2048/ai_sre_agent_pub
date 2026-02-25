package monitoring

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/monitoring/sources"
	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeMetricSource struct {
	name    string
	status  sources.SourceStatus
	batch   *proto.MetricBatch
	err     error
	collect atomic.Int32
}

func (f *fakeMetricSource) Name() string { return f.name }
func (f *fakeMetricSource) Start(context.Context) error {
	return nil
}
func (f *fakeMetricSource) Stop() error { return nil }
func (f *fakeMetricSource) Status() sources.SourceStatus {
	status := f.status
	if status.Name == "" {
		status.Name = f.name
	}
	return status
}
func (f *fakeMetricSource) Collect(context.Context) (*proto.MetricBatch, error) {
	f.collect.Add(1)
	return f.batch, f.err
}

func TestCollectorCollectOnceAggregatesHealthySources(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&fakeMetricSource{
		name: "src-a",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{
					Name:   "node_cpu_usage_percent",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Points: []*proto.MetricPoint{{Value: 70}},
				},
			},
		},
	})
	collector.RegisterSource(&fakeMetricSource{
		name: "src-b",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{
					Name:   "node_network_receive_bytes_per_second",
					Type:   proto.MetricType_METRIC_TYPE_COUNTER,
					Points: []*proto.MetricPoint{{Value: 1234}},
					Labels: []*proto.MetricLabel{{Key: "device", Value: "eth0"}},
				},
			},
		},
	})
	collector.RegisterSource(&fakeMetricSource{
		name: "src-error",
		err:  errors.New("collect failed"),
	})

	metrics, err := collector.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 2)

	got := map[string]sources.Metric{}
	for _, metric := range metrics {
		got[metric.Name] = metric
	}

	require.Equal(t, "src-a", got["node_cpu_usage_percent"].Source)
	require.Equal(t, 70.0, got["node_cpu_usage_percent"].Value)
	require.Equal(t, "METRIC_TYPE_GAUGE", got["node_cpu_usage_percent"].Type)

	require.Equal(t, "src-b", got["node_network_receive_bytes_per_second"].Source)
	require.Equal(t, 1234.0, got["node_network_receive_bytes_per_second"].Value)
	require.Equal(t, "eth0", got["node_network_receive_bytes_per_second"].Labels["device"])
}

func TestCollectorCollectOnceReturnsErrorWhenAllSourcesFail(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&fakeMetricSource{
		name: "src-a",
		err:  errors.New("proc unavailable"),
	})
	collector.RegisterSource(&fakeMetricSource{
		name: "src-b",
		err:  errors.New("process unavailable"),
	})

	metrics, err := collector.CollectOnce(context.Background())
	require.Error(t, err)
	require.Nil(t, metrics)
	require.Contains(t, err.Error(), "2/2 sources failed")
}

func TestCollectorStartEmitsMetricsToChannel(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 2 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	src := &fakeMetricSource{
		name: "src-live",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{
					Name:   "node_disk_total_iops_per_second",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Points: []*proto.MetricPoint{{Value: 55}},
				},
			},
		},
	}
	collector.RegisterSource(src)

	metricCh := make(chan sources.Metric, 8)
	collector.SetMetricChannel(metricCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, collector.Start(ctx))
	defer func() { _ = collector.Stop() }()

	select {
	case metric := <-metricCh:
		require.Equal(t, "node_disk_total_iops_per_second", metric.Name)
		require.Equal(t, 55.0, metric.Value)
		require.Equal(t, "src-live", metric.Source)
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("timed out waiting for collector metric")
	}

	require.GreaterOrEqual(t, src.collect.Load(), int32(1))
}

func TestCollectorStopIsIdempotent(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 2 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, collector.Start(ctx))

	require.NotPanics(t, func() {
		_ = collector.Stop()
	})
	require.NotPanics(t, func() {
		_ = collector.Stop()
	})
}

func TestCollectorCanRestartAfterStop(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 2 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	src := &fakeMetricSource{
		name: "src-restart",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{
					Name:   "node_pressure_cpu_some_avg10",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Points: []*proto.MetricPoint{{Value: 12.5}},
				},
			},
		},
	}
	collector.RegisterSource(src)

	metricCh := make(chan sources.Metric, 16)
	collector.SetMetricChannel(metricCh)

	ctx1, cancel1 := context.WithCancel(context.Background())
	require.NoError(t, collector.Start(ctx1))
	select {
	case <-metricCh:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("timed out waiting for first-run metric")
	}
	cancel1()
	require.NoError(t, collector.Stop())

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	require.NoError(t, collector.Start(ctx2))
	defer func() { _ = collector.Stop() }()

	select {
	case <-metricCh:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("timed out waiting for second-run metric after restart")
	}

	require.GreaterOrEqual(t, src.collect.Load(), int32(2))
}

func TestCollectorStartTwiceDoesNotSpawnDuplicateLoops(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	src := &fakeMetricSource{
		name: "src-single-loop",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{
					Name:   "node_load1",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Points: []*proto.MetricPoint{{Value: 1}},
				},
			},
		},
	}
	collector.RegisterSource(src)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, collector.Start(ctx))
	require.NoError(t, collector.Start(ctx))
	defer func() { _ = collector.Stop() }()

	time.Sleep(60 * time.Millisecond)
	count := src.collect.Load()

	// With a single collection loop at 5ms interval, expected collects are bounded.
	// A duplicated loop roughly doubles this count and would violate this ceiling.
	require.LessOrEqual(t, count, int32(20))
	require.GreaterOrEqual(t, count, int32(6))
}

func TestCollectorCollectOnceSkipsNilMetricEntriesAndLabels(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&fakeMetricSource{
		name: "src-nil-safe",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				nil,
				{
					Name:   "node_disk_total_iops_per_second",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Points: []*proto.MetricPoint{{Value: 88}},
					Labels: []*proto.MetricLabel{
						nil,
						{Key: "device", Value: "nvme0n1"},
					},
				},
			},
		},
	})

	metrics, err := collector.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "node_disk_total_iops_per_second", metrics[0].Name)
	require.Equal(t, 88.0, metrics[0].Value)
	require.Equal(t, "nvme0n1", metrics[0].Labels["device"])
}

func TestCollectorCollectOnceSkipsNilMetricPoints(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&fakeMetricSource{
		name: "src-point-nil-safe",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{
					Name: "node_cpu_usage_percent",
					Type: proto.MetricType_METRIC_TYPE_GAUGE,
					Points: []*proto.MetricPoint{
						nil,
						{Value: 73},
					},
				},
			},
		},
	})

	metrics, err := collector.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "node_cpu_usage_percent", metrics[0].Name)
	require.Equal(t, 73.0, metrics[0].Value)
}

func TestCollectorCollectOnceSkipsMetricsWithEmptyName(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&fakeMetricSource{
		name: "src-empty-name",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{Name: "", Type: proto.MetricType_METRIC_TYPE_GAUGE, Points: []*proto.MetricPoint{{Value: 1}}},
				{Name: "   ", Type: proto.MetricType_METRIC_TYPE_GAUGE, Points: []*proto.MetricPoint{{Value: 2}}},
				{Name: "node_load1", Type: proto.MetricType_METRIC_TYPE_GAUGE, Points: []*proto.MetricPoint{{Value: 3}}},
			},
		},
	})

	metrics, err := collector.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "node_load1", metrics[0].Name)
	require.Equal(t, 3.0, metrics[0].Value)
}

func TestCollectorCollectOnceSkipsNonFiniteMetricValues(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&fakeMetricSource{
		name: "src-non-finite",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{Name: "node_cpu_usage_percent", Type: proto.MetricType_METRIC_TYPE_GAUGE, Points: []*proto.MetricPoint{{Value: math.NaN()}}},
				{Name: "node_load1", Type: proto.MetricType_METRIC_TYPE_GAUGE, Points: []*proto.MetricPoint{{Value: math.Inf(1)}}},
				{Name: "node_memory_MemAvailable_bytes", Type: proto.MetricType_METRIC_TYPE_GAUGE, Points: []*proto.MetricPoint{{Value: 1024}}},
			},
		},
	})

	metrics, err := collector.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "node_memory_MemAvailable_bytes", metrics[0].Name)
	require.Equal(t, 1024.0, metrics[0].Value)
}

func TestCollectorCollectOnceSkipsMetricsWithoutPoints(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&fakeMetricSource{
		name: "src-empty-points",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{Name: "node_cpu_usage_percent", Type: proto.MetricType_METRIC_TYPE_GAUGE},
				{Name: "node_load1", Type: proto.MetricType_METRIC_TYPE_GAUGE, Points: []*proto.MetricPoint{{Value: 7.5}}},
			},
		},
	})

	metrics, err := collector.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "node_load1", metrics[0].Name)
	require.Equal(t, 7.5, metrics[0].Value)
}

func TestCollectorCollectOnceTrimsAndFiltersLabelKeys(t *testing.T) {
	collector, err := NewCollector(&Config{
		ScrapeInterval: 5 * time.Millisecond,
	}, zap.NewNop())
	require.NoError(t, err)

	collector.RegisterSource(&fakeMetricSource{
		name: "src-label-keys",
		batch: &proto.MetricBatch{
			Metrics: []*proto.Metric{
				{
					Name:   "node_network_receive_bytes_per_second",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Points: []*proto.MetricPoint{{Value: 333}},
					Labels: []*proto.MetricLabel{
						{Key: " device ", Value: "eth0"},
						{Key: "   ", Value: "bad"},
					},
				},
			},
		},
	})

	metrics, err := collector.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "eth0", metrics[0].Labels["device"])
	require.NotContains(t, metrics[0].Labels, " device ")
	require.NotContains(t, metrics[0].Labels, "   ")
}
