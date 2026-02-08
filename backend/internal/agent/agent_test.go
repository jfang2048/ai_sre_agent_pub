package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestQueryReturnsResponseWithActions(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 92},
		{Name: "node_gpu_utilization_sm_avg_percent", Value: 95},
		{Name: "node_gpu_memory_used_total_mib", Value: 31000},
	}, now)
	store.StoreProcesses("collector-a", []*telemetryv1.ProcessSample{
		{Pid: 1001, Name: "trainer", CpuPercent: 97},
	}, now)

	gpuStore := gpuobs.New(gpuobs.DefaultConfig())
	gpuStore.ProcessBatch("collector-a", &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-a",
			Hostname:    "node-a",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_gpu_count", Value: 1},
			{Name: "node_gpu_utilization_sm_percent", Value: 96, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_memory_total_mib", Value: 32768, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_memory_used_mib", Value: 31800, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
		},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	service, err := NewQueryService(cfg, store, gpuStore, zap.NewNop())
	require.NoError(t, err)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "RCA for high GPU on fleet",
		Node:  "collector-a",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.QueryID)
	require.Equal(t, "collector-a", resp.Node)
	require.NotEmpty(t, resp.Summary)
	require.NotEmpty(t, resp.TelemetryContext.Evidence.GPU)
}

func TestQueryRateLimited(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.RateLimitRPS = 0.1
	cfg.RateBurst = 1

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)

	_, err = service.Query(context.Background(), QueryRequest{Query: "first"})
	require.NoError(t, err)

	_, err = service.Query(context.Background(), QueryRequest{Query: "second"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrRateLimited))
}

func TestExecuteMissingActionReturnsNotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)

	_, err = service.Execute(context.Background(), ExecuteRequest{ActionID: "missing"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrActionNotFound))
}
