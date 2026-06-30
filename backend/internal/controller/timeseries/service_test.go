package timeseries

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestServiceWritesBatchMetricsToInflux(t *testing.T) {
	var (
		mu     sync.Mutex
		writes []string
	)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.URL = "http://influx.test"
	cfg.WriteBatchSize = 2
	cfg.FlushInterval = 20 * time.Millisecond
	cfg.QueryTimeout = 200 * time.Millisecond

	service, err := NewService(cfg, ingest.NewMemoryStore(), zap.NewNop())
	require.NoError(t, err)
	service.client.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/health":
			return httpResponse(http.StatusOK, ""), nil
		case strings.HasPrefix(req.URL.Path, "/api/v2/write"):
			body, _ := io.ReadAll(req.Body)
			mu.Lock()
			writes = append(writes, string(body))
			mu.Unlock()
			return httpResponse(http.StatusNoContent, ""), nil
		default:
			return httpResponse(http.StatusNotFound, "missing"), nil
		}
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, service.Start(ctx))
	defer func() { require.NoError(t, service.Close()) }()

	now := time.Now().UTC()
	service.ProcessBatch("collector-a", &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 42},
			{Name: "node_network_receive_bytes_per_second", Value: 10},
			{Name: "node_network_receive_bytes_per_second", Value: 5},
			{Name: "ignored_metric", Value: 99},
		},
	}, now)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(writes) > 0
	}, time.Second, 20*time.Millisecond)

	mu.Lock()
	payload := strings.Join(writes, "\n")
	mu.Unlock()
	assert.Contains(t, payload, "metric=node_cpu_usage_percent value=42")
	assert.Contains(t, payload, "metric=node_network_receive_bytes_per_second value=15")
	assert.NotContains(t, payload, "ignored_metric")
}

func TestServiceMetricHistoryUsesInfluxQuery(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.URL = "http://influx.test"
	cfg.QueryTimeout = 200 * time.Millisecond

	service, err := NewService(cfg, ingest.NewMemoryStore(), zap.NewNop())
	require.NoError(t, err)
	service.client.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/health":
			return httpResponse(http.StatusOK, ""), nil
		case strings.HasPrefix(req.URL.Path, "/api/v2/query"):
			return httpResponse(http.StatusOK, strings.Join([]string{
				"#datatype,string,long,dateTime:RFC3339,double,string,string,string",
				"#group,false,false,false,false,true,true,true",
				"#default,_result,,,,,,",
				",result,table,_time,_value,collector_id,hostname,metric",
				",,0,2026-03-07T00:00:00Z,25,collector-a,node-a,node_cpu_usage_percent",
				",,0,2026-03-07T00:00:00Z,55,collector-a,node-a,node_cpu_iowait_percent",
				",,0,2026-03-07T00:01:00Z,80,collector-a,node-a,node_cpu_usage_percent",
			}, "\n")), nil
		default:
			return httpResponse(http.StatusNotFound, "missing"), nil
		}
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, service.Start(ctx))

	history := service.MetricHistory("collector-a", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), 10)
	require.Len(t, history, 2)
	assert.Equal(t, 25.0, history[0].Metrics["node_cpu_usage_percent"])
	assert.Equal(t, 55.0, history[0].Metrics["node_cpu_iowait_percent"])
	assert.Equal(t, 80.0, history[1].Metrics["node_cpu_usage_percent"])
}

func TestServiceMetricHistoryFallsBackWhenQueryFails(t *testing.T) {
	fallback := ingest.NewMemoryStore()
	now := time.Now().UTC()
	fallback.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	fallback.StoreMetrics("collector-a", []*telemetryv1.Metric{{Name: "node_cpu_usage_percent", Value: 61}}, now)

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.URL = "http://influx.test"
	cfg.QueryTimeout = 200 * time.Millisecond

	service, err := NewService(cfg, fallback, zap.NewNop())
	require.NoError(t, err)
	service.client.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/health":
			return httpResponse(http.StatusOK, ""), nil
		case strings.HasPrefix(req.URL.Path, "/api/v2/query"):
			return httpResponse(http.StatusBadGateway, "boom"), nil
		default:
			return httpResponse(http.StatusNotFound, "missing"), nil
		}
	})}
	history := service.MetricHistory("collector-a", now.Add(-time.Minute), 10)
	require.Len(t, history, 1)
	assert.Equal(t, 61.0, history[0].Metrics["node_cpu_usage_percent"])
	status := service.Status()
	assert.True(t, status.FallbackActive)
	assert.NotEmpty(t, status.LastQueryError)
}

func TestServicePeriodicHealthRecoversFromInitialFailure(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.URL = "http://influx.test"
	cfg.QueryTimeout = 200 * time.Millisecond
	cfg.HealthInterval = 20 * time.Millisecond
	cfg.FallbackToMemory = true

	service, err := NewService(cfg, ingest.NewMemoryStore(), zap.NewNop())
	require.NoError(t, err)

	var healthCalls int
	service.client.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/health":
			healthCalls++
			if healthCalls == 1 {
				return httpResponse(http.StatusServiceUnavailable, "down"), nil
			}
			return httpResponse(http.StatusOK, ""), nil
		default:
			return httpResponse(http.StatusNotFound, "missing"), nil
		}
	})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, service.Start(ctx))
	defer func() { require.NoError(t, service.Close()) }()

	require.Eventually(t, func() bool {
		status := service.Status()
		return status.Healthy && !status.FallbackActive && status.LastHealthError == ""
	}, time.Second, 20*time.Millisecond)
}

func TestConfigFromEnvParsesOverrides(t *testing.T) {
	t.Setenv("SRE_TSDB_ENABLED", "1")
	t.Setenv("SRE_TSDB_URL", "http://influxdb:8086")
	t.Setenv("SRE_TSDB_ORG", "demo")
	t.Setenv("SRE_TSDB_BUCKET", "metrics")
	t.Setenv("SRE_TSDB_RETENTION", "168h")
	t.Setenv("SRE_TSDB_WRITE_BATCH_SIZE", "1024")
	t.Setenv("SRE_TSDB_WRITE_QUEUE_SIZE", "64")
	t.Setenv("SRE_TSDB_FLUSH_INTERVAL", "3s")
	t.Setenv("SRE_TSDB_QUERY_TIMEOUT", "9s")
	t.Setenv("SRE_TSDB_HEALTH_INTERVAL", "11s")
	t.Setenv("SRE_TSDB_FALLBACK_TO_MEMORY", "0")
	t.Setenv("SRE_TSDB_MANAGE_BUCKET", "1")
	t.Setenv("SRE_TSDB_BACKUP_DIRECTORY", "/var/backups/influx")

	cfg := ConfigFromEnv(DefaultConfig())
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "http://influxdb:8086", cfg.URL)
	assert.Equal(t, "demo", cfg.Org)
	assert.Equal(t, "metrics", cfg.Bucket)
	assert.Equal(t, 168*time.Hour, cfg.Retention)
	assert.Equal(t, 1024, cfg.WriteBatchSize)
	assert.Equal(t, 64, cfg.WriteQueueSize)
	assert.Equal(t, 3*time.Second, cfg.FlushInterval)
	assert.Equal(t, 9*time.Second, cfg.QueryTimeout)
	assert.Equal(t, 11*time.Second, cfg.HealthInterval)
	assert.False(t, cfg.FallbackToMemory)
	assert.True(t, cfg.ManageBucket)
	assert.Equal(t, "/var/backups/influx", cfg.BackupDirectory)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func httpResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
