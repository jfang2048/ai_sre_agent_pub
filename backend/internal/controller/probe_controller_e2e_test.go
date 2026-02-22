package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestProbeControllerWorkflowE2E(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.GRPCListenAddr = "127.0.0.1:0"
	cfg.ScrapeInterval = time.Hour
	cfg.ScrapeTimeout = 2 * time.Second
	cfg.Nodes = nil

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = ctrl.Start(ctx)
	if err != nil {
		if isNetPermissionError(err) {
			t.Skipf("Skipping due to listen permission error: %v", err)
		}
		require.NoError(t, err)
	}
	defer func() { _ = ctrl.Stop() }()

	conn, err := grpc.DialContext(
		ctx,
		ctrl.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := telemetryv1.NewTelemetryIngestClient(conn)
	stream, err := client.Push(ctx)
	require.NoError(t, err)

	batch := &telemetryv1.TelemetryBatch{
		BatchId: "batch-e2e-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-e2e",
			Hostname:    "node-e2e",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 66.6},
			{Name: "node_load1", Value: 1.25},
		},
		Processes: []*telemetryv1.ProcessSample{
			{Pid: 1234, Name: "trainer", CpuPercent: 66.6},
		},
	}

	require.NoError(t, stream.Send(batch))
	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-e2e-1", ack.BatchId)
	require.NoError(t, stream.CloseSend())

	httpClient := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + ctrl.ListenAddr()

	require.Eventually(t, func() bool {
		resp, err := httpClient.Get(baseURL + "/api/v1/fleet")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var payload struct {
			Count int `json:"count"`
			Nodes []struct {
				CollectorID string `json:"collector_id"`
			} `json:"nodes"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return false
		}
		if payload.Count < 1 {
			return false
		}
		for _, node := range payload.Nodes {
			if node.CollectorID == "collector-e2e" {
				return true
			}
		}
		return false
	}, 3*time.Second, 100*time.Millisecond)

	respNode, err := httpClient.Get(baseURL + "/api/v1/fleet/collector-e2e")
	require.NoError(t, err)
	defer respNode.Body.Close()
	require.Equal(t, http.StatusOK, respNode.StatusCode)

	var nodePayload struct {
		CollectorID string             `json:"collector_id"`
		Hostname    string             `json:"hostname"`
		Metrics     map[string]float64 `json:"metrics"`
	}
	require.NoError(t, json.NewDecoder(respNode.Body).Decode(&nodePayload))
	require.Equal(t, "collector-e2e", nodePayload.CollectorID)
	require.Equal(t, "node-e2e", nodePayload.Hostname)
	require.Equal(t, 66.6, nodePayload.Metrics["node_cpu_usage_percent"])

	respTrend, err := httpClient.Get(baseURL + "/api/v1/fleet/timeseries?collector_id=collector-e2e&window=15m&limit=20")
	require.NoError(t, err)
	defer respTrend.Body.Close()
	require.Equal(t, http.StatusOK, respTrend.StatusCode)

	var trendPayload struct {
		CollectorID    string             `json:"collector_id"`
		SampleCount    int                `json:"sample_count"`
		NumericSummary map[string]float64 `json:"numeric_summary"`
		Series         []struct {
			Key string `json:"key"`
		} `json:"series"`
	}
	require.NoError(t, json.NewDecoder(respTrend.Body).Decode(&trendPayload))
	require.Equal(t, "collector-e2e", trendPayload.CollectorID)
	require.GreaterOrEqual(t, trendPayload.SampleCount, 1)
	require.Equal(t, 66.6, trendPayload.NumericSummary["cpu_usage_percent"])

	hasCPUTrend := false
	for _, series := range trendPayload.Series {
		if strings.EqualFold(series.Key, "cpu_usage_percent") {
			hasCPUTrend = true
			break
		}
	}
	require.True(t, hasCPUTrend)
}

func TestProbeControllerTopProgramsFlowE2E(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.GRPCListenAddr = "127.0.0.1:0"
	cfg.ScrapeInterval = time.Hour
	cfg.ScrapeTimeout = 2 * time.Second
	cfg.Nodes = nil

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = ctrl.Start(ctx)
	if err != nil {
		if isNetPermissionError(err) {
			t.Skipf("Skipping due to listen permission error: %v", err)
		}
		require.NoError(t, err)
	}
	defer func() { _ = ctrl.Stop() }()

	conn, err := grpc.DialContext(
		ctx,
		ctrl.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := telemetryv1.NewTelemetryIngestClient(conn)
	stream, err := client.Push(ctx)
	require.NoError(t, err)

	batch := &telemetryv1.TelemetryBatch{
		BatchId: "batch-top-programs-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-top-programs",
			Hostname:    "node-top-programs",
		},
		Metrics: []*telemetryv1.Metric{
			{
				Name:  "rca_net_process_connections",
				Value: 18,
				Labels: []*telemetryv1.Label{
					{Key: "pid", Value: "4242"},
					{Key: "name", Value: "trainer-main"},
				},
			},
			{
				Name:  "rca_net_process_queued_bytes",
				Value: 4096,
				Labels: []*telemetryv1.Label{
					{Key: "pid", Value: "4242"},
				},
			},
		},
		Processes: []*telemetryv1.ProcessSample{
			{Pid: 4242, Name: "trainer-main", CpuPercent: 93.5, RssBytes: 2 * 1024 * 1024 * 1024},
		},
		Logs: []*telemetryv1.LogFingerprint{
			{Fingerprint: "trainer-main-err", Example: "trainer-main[4242]: ERROR timeout while checkpointing", Count: 4},
		},
	}

	require.NoError(t, stream.Send(batch))
	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-top-programs-1", ack.BatchId)
	require.NoError(t, stream.CloseSend())

	httpClient := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + ctrl.ListenAddr()

	require.Eventually(t, func() bool {
		resp, err := httpClient.Get(baseURL + "/api/v1/top/programs?limit=5")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}

		var payload TopProgramsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return false
		}
		for _, p := range payload.Programs {
			if p.Name == "trainer-main" {
				return true
			}
		}
		return false
	}, 3*time.Second, 100*time.Millisecond)
}

func TestControllerIngestRecoversAfterInvalidStreamE2E(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.GRPCListenAddr = "127.0.0.1:0"
	cfg.ScrapeInterval = time.Hour
	cfg.ScrapeTimeout = 2 * time.Second
	cfg.Nodes = nil

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = ctrl.Start(ctx)
	if err != nil {
		if isNetPermissionError(err) {
			t.Skipf("Skipping due to listen permission error: %v", err)
		}
		require.NoError(t, err)
	}
	defer func() { _ = ctrl.Stop() }()

	conn, err := grpc.DialContext(
		ctx,
		ctrl.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := telemetryv1.NewTelemetryIngestClient(conn)

	// First stream sends invalid payload and should be rejected.
	invalidStream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, invalidStream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-invalid",
			Hostname:    "node-invalid",
		},
	}))
	_, err = invalidStream.Recv()
	require.Error(t, err)
	require.NoError(t, invalidStream.CloseSend())

	// A fresh stream should still ingest valid telemetry after the rejection.
	validStream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, validStream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-after-invalid",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-recovered",
			Hostname:    "node-recovered",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 23.5},
		},
	}))
	ack, err := validStream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-after-invalid", ack.BatchId)
	require.NoError(t, validStream.CloseSend())

	httpClient := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + ctrl.ListenAddr()
	require.Eventually(t, func() bool {
		resp, err := httpClient.Get(baseURL + "/api/v1/fleet/collector-recovered")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 3*time.Second, 100*time.Millisecond)
}

func TestControllerSustainedIngestStatsAndSummaryE2E(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.GRPCListenAddr = "127.0.0.1:0"
	cfg.ScrapeInterval = time.Hour
	cfg.ScrapeTimeout = 2 * time.Second
	cfg.Nodes = nil

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = ctrl.Start(ctx)
	if err != nil {
		if isNetPermissionError(err) {
			t.Skipf("Skipping due to listen permission error: %v", err)
		}
		require.NoError(t, err)
	}
	defer func() { _ = ctrl.Stop() }()

	conn, err := grpc.DialContext(
		ctx,
		ctrl.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := telemetryv1.NewTelemetryIngestClient(conn)
	stream, err := client.Push(ctx)
	require.NoError(t, err)

	const batches = 20
	for i := 0; i < batches; i++ {
		batchID := "batch-sustained-" + strconv.Itoa(i+1)
		cpu := 30.0 + float64(i)
		err := stream.Send(&telemetryv1.TelemetryBatch{
			BatchId: batchID,
			Collector: &telemetryv1.CollectorInfo{
				CollectorId: "collector-sustained",
				Hostname:    "node-sustained",
			},
			Metrics: []*telemetryv1.Metric{
				{Name: "node_cpu_usage_percent", Value: cpu},
				{Name: "node_load1", Value: 1 + float64(i)/10},
			},
		})
		require.NoError(t, err)
		ack, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, batchID, ack.BatchId)
	}
	require.NoError(t, stream.CloseSend())

	httpClient := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + ctrl.ListenAddr()

	require.Eventually(t, func() bool {
		resp, err := httpClient.Get(baseURL + "/api/v1/ingest/status")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var statusPayload struct {
			Stats struct {
				BatchesTotal  uint64 `json:"batches_total"`
				RejectedTotal uint64 `json:"rejected_total"`
				LastCollector string `json:"last_collector"`
			} `json:"stats"`
			FleetNodes int `json:"fleet_nodes"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&statusPayload); err != nil {
			return false
		}
		return statusPayload.Stats.BatchesTotal == batches &&
			statusPayload.Stats.RejectedTotal == 0 &&
			statusPayload.Stats.LastCollector == "collector-sustained" &&
			statusPayload.FleetNodes >= 1
	}, 3*time.Second, 100*time.Millisecond)

	require.Eventually(t, func() bool {
		resp, err := httpClient.Get(baseURL + "/api/v1/fleet/timeseries?collector_id=collector-sustained&window=15m&limit=200")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var trendPayload struct {
			SampleCount    int                `json:"sample_count"`
			NumericSummary map[string]float64 `json:"numeric_summary"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&trendPayload); err != nil {
			return false
		}
		latestCPU, ok := trendPayload.NumericSummary["cpu_usage_percent"]
		if !ok {
			return false
		}
		return trendPayload.SampleCount >= batches && latestCPU == 49.0
	}, 3*time.Second, 100*time.Millisecond)
}
