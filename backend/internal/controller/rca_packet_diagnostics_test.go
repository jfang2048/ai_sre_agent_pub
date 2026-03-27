package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
)

func TestHandleRCAPacketDiagnosticsReturnsMarkdownPayload(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-3 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "collector-a",
		Hostname:    "node-a",
	}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 74},
		{Name: "node_memory_MemTotal_bytes", Value: 128 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 101 * 1024 * 1024 * 1024},
		{Name: "node_network_utilization_peak_percent", Value: 93},
		{Name: "node_tcp_retransmit_ratio", Value: 0.019},
		{Name: "node_softnet_dropped_per_second", Value: 85},
		{Name: "node_rdma_congestion_events_per_second", Value: 24},
		{Name: "node_rdma_pfc_pause_frames_per_second", Value: 115},
		{Name: "node_disk_utilization_peak_percent", Value: 90},
		{Name: "node_disk_queue_depth_total", Value: 72},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.041},
		{Name: "node_pressure_io_full_avg10", Value: 8},
		{Name: "node_checkpoint_write_latency_p99_seconds", Value: 0.17},
		{Name: "collector_probe_core_client_available", Value: 1},
		{Name: "collector_probe_core_active", Value: 1},
		{Name: "collector_probe_core_fresh", Value: 1},
		{Name: "collector_probe_core_collector_selection_valid", Value: 1},
		{Name: "collector_probe_core_last_frame_age_seconds", Value: 2},
	}, now)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/diagnostics/rca-packet?collector_id=collector-a&cluster=cluster-a&namespace=ml&service=trainer&sort_key=network&sort_direction=asc&workload_limit=15",
		nil,
	)
	w := httptest.NewRecorder()
	ctrl.handleRCAPacketDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp rcaPacketDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-a", resp.CollectorID)
			assert.Equal(t, "cluster-a", resp.Cluster)
			assert.Equal(t, "ml", resp.Namespace)
			assert.Equal(t, "trainer", resp.Service)
			assert.Equal(t, "network", resp.SortKey)
			assert.Equal(t, "asc", resp.SortDirection)
			assert.Equal(t, "json", resp.Format)
			assert.Equal(t, 15, resp.WorkloadLimit)
			assert.True(t, strings.HasSuffix(resp.FileName, ".md"))
			assert.Contains(t, resp.Markdown, "# AI SRE RCA Packet")
			assert.Contains(t, resp.Markdown, "Scope: cluster=cluster-a, namespace=ml, service=trainer")
			assert.Contains(t, resp.Markdown, "## Root Cause Summary")
			assert.Contains(t, resp.Markdown, "## Kernel Path Snapshot")
			assert.Contains(t, resp.Markdown, "## Resource Pressure Snapshot")
			assert.Contains(t, resp.Markdown, "# Workload Path Handoff")
			assert.Len(t, resp.PacketSHA256, 64)
			assert.Equal(t, len([]byte(resp.Markdown)), resp.ContentBytes)
			assert.Empty(t, resp.PacketSignature)
			assert.Empty(t, resp.PacketSignatureAlgorithm)
			assert.Empty(t, resp.PacketSignatureKeyID)
			sum := sha256.Sum256([]byte(resp.Markdown))
			assert.Equal(t, hex.EncodeToString(sum[:]), resp.PacketSHA256)
			assert.Equal(t, "/api/v1/diagnostics/root-cause", resp.SourceMetadata.RootCauseEndpoint)
			assert.GreaterOrEqual(t, resp.Summary.NetworkRanked, 1)
			assert.GreaterOrEqual(t, resp.Summary.StorageRanked, 1)
		}
	}
}

func TestHandleRCAPacketDiagnosticsNormalizesQueryDefaults(t *testing.T) {
	ctrl := &Controller{}
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/diagnostics/rca-packet?sort_key=bad&sort_direction=bad&workload_limit=-1",
		nil,
	)
	w := httptest.NewRecorder()
	ctrl.handleRCAPacketDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp rcaPacketDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, rcaPacketDefaultSortKey, resp.SortKey)
			assert.Equal(t, rcaPacketDefaultSortDirection, resp.SortDirection)
			assert.Equal(t, rcaPacketDefaultFormat, resp.Format)
			assert.Equal(t, defaultWorkloadPathLimit, resp.WorkloadLimit)
		}
	}
}

func TestHandleRCAPacketDiagnosticsIncludesSignatureWhenConfigured(t *testing.T) {
	t.Setenv("SRE_RCA_PACKET_SIGNING_KEY", "unit-test-signing-key")
	t.Setenv("SRE_RCA_PACKET_SIGNING_KEY_ID", "primary")

	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-3 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "collector-a",
		Hostname:    "node-a",
	}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 65},
		{Name: "node_memory_MemTotal_bytes", Value: 64 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 40 * 1024 * 1024 * 1024},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rca-packet?collector_id=collector-a", nil)
	w := httptest.NewRecorder()
	ctrl.handleRCAPacketDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp rcaPacketDiagnosticsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "hmac-sha256", resp.PacketSignatureAlgorithm)
			assert.Equal(t, "primary", resp.PacketSignatureKeyID)
			assert.Len(t, resp.PacketSignature, 64)
			mac := hmac.New(sha256.New, []byte("unit-test-signing-key"))
			_, _ = mac.Write([]byte(resp.Markdown))
			expected := hex.EncodeToString(mac.Sum(nil))
			assert.Equal(t, expected, resp.PacketSignature)
		}
	}
}

func TestHandleRCAPacketDiagnosticsMarkdownFormatAndDownload(t *testing.T) {
	t.Setenv("SRE_RCA_PACKET_SIGNING_KEY", "markdown-signing-key")
	t.Setenv("SRE_RCA_PACKET_SIGNING_KEY_ID", "rollover-2026q1")

	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-3 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "collector-a",
		Hostname:    "node-a",
	}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 74},
		{Name: "node_memory_MemTotal_bytes", Value: 128 * 1024 * 1024 * 1024},
		{Name: "node_memory_Used_bytes", Value: 101 * 1024 * 1024 * 1024},
	}, now)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/diagnostics/rca-packet?collector_id=collector-a&format=markdown&download=1",
		nil,
	)
	w := httptest.NewRecorder()
	ctrl.handleRCAPacketDiagnostics(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		assert.Equal(t, "text/markdown; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment; filename=")
		assert.Len(t, w.Header().Get("X-AI-SRE-Packet-SHA256"), 64)
		assert.NotEmpty(t, w.Header().Get("X-AI-SRE-Packet-Bytes"))
		assert.Len(t, w.Header().Get("X-AI-SRE-Packet-Signature"), 64)
		assert.Equal(t, "hmac-sha256", w.Header().Get("X-AI-SRE-Packet-Signature-Algorithm"))
		assert.Equal(t, "rollover-2026q1", w.Header().Get("X-AI-SRE-Packet-Signature-Key-ID"))
		body := w.Body.String()
		assert.Contains(t, body, "# AI SRE RCA Packet")
	}
}

func TestHandleRCAPacketDiagnosticsRejectsUnsupportedMethod(t *testing.T) {
	ctrl := &Controller{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/rca-packet", nil)
	w := httptest.NewRecorder()
	ctrl.handleRCAPacketDiagnostics(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
