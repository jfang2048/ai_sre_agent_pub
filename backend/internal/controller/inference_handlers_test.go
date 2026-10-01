package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inferenceobs"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestInferenceOverviewScopedMissingAndMethod(t *testing.T) {
	ctrl := &Controller{inferenceStore: inferenceobs.New(inferenceobs.DefaultConfig())}
	now := time.Now()
	for _, id := range []string{"a", "b"} {
		ctrl.inferenceStore.ProcessBatch(id, &telemetryv1.TelemetryBatch{
			Collector: &telemetryv1.CollectorInfo{CollectorId: id, Hostname: "synthetic-node"},
			Metrics:   []*telemetryv1.Metric{{Name: "node_inference_scrape_success", Value: 0, TimestampUnixNano: now.UnixNano(), Labels: []*telemetryv1.Label{{Key: "inference_endpoint", Value: "serving"}}}},
		}, now)
	}
	rec := httptest.NewRecorder()
	ctrl.handleInferenceOverview(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inference/overview?collector_id=a", nil))
	var report inferenceobs.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Endpoints) != 1 || report.Endpoints[0].CollectorID != "a" || report.Endpoints[0].Status != "unavailable" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("monitoring should not be cached")
	}
	rec = httptest.NewRecorder()
	ctrl.handleInferenceOverview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/inference/overview", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	(&Controller{}).handleInferenceOverview(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inference/overview", nil))
	if rec.Code != 200 {
		t.Fatalf("empty status %d", rec.Code)
	}
	var empty map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if a, ok := empty["endpoints"].([]any); !ok || len(a) != 0 {
		t.Fatalf("empty array required: %s", rec.Body.String())
	}
}

func TestInferenceProjectionRegisteredWithIngest(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.GRPCListenAddr = "127.0.0.1:0"
	ctrl, err := newTestController(t, cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ctrl.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer ctrl.Stop()
	conn, err := grpc.NewClient(ctrl.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stream, err := telemetryv1.NewTelemetryIngestClient(conn).Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	batch := &telemetryv1.TelemetryBatch{BatchId: "monitoring-integration", Collector: &telemetryv1.CollectorInfo{CollectorId: "test-monitor", Hostname: "synthetic"}, Metrics: []*telemetryv1.Metric{
		{Name: "node_inference_scrape_success", Value: 1, TimestampUnixNano: now.UnixNano(), Labels: []*telemetryv1.Label{{Key: "inference_endpoint", Value: "serving"}}},
		{Name: "node_inference_requests_waiting", Value: 12, TimestampUnixNano: now.UnixNano(), Labels: []*telemetryv1.Label{{Key: "inference_endpoint", Value: "serving"}, {Key: "model", Value: "test-model"}}},
	}}
	// Use the real server entry point so a missing processor registration fails.
	if err := stream.Send(batch); err != nil {
		t.Fatal(err)
	}
	ack, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if ack.BatchId != batch.BatchId {
		t.Fatalf("unexpected ACK: %v", ack)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	report := ctrl.inferenceStore.Snapshot("test-monitor", time.Now())
	if len(report.Endpoints) != 1 || len(report.Endpoints[0].Models) != 1 || len(report.Endpoints[0].Models[0].Findings) != 1 {
		t.Fatalf("projection did not receive batch: %+v", report)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ctrl.ListenAddr()+"/api/v1/inference/overview?collector_id=test-monitor", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var served inferenceobs.Report
	if err := json.NewDecoder(response.Body).Decode(&served); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(served.Endpoints) != 1 || served.Endpoints[0].CollectorID != "test-monitor" {
		t.Fatalf("HTTP route did not expose ingested projection: %+v", served)
	}
}

func TestInferenceConfigReloadRequiresRestart(t *testing.T) {
	current := DefaultConfig()
	c := &Controller{config: current, logger: zap.NewNop()}
	next := current
	next.Inference.WaitingWarning = 25
	report, err := c.ApplyRuntimeConfig(next)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range report.RestartRequired {
		if field == "inference" {
			found = true
		}
	}
	if !found || c.config.Inference.WaitingWarning != current.Inference.WaitingWarning {
		t.Fatalf("inference threshold changes must explicitly require restart: %+v", report)
	}
}
