package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestHandler(t *testing.T) *APIHandler {
	t.Helper()
	cfg := DefaultConfig()
	m, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	return NewAPIHandler(m, zap.NewNop())
}

// ── handleAnalyze tests ───────────────────────────────────────────────

func TestHandleAnalyzeSuccess(t *testing.T) {
	h := newTestHandler(t)

	body, _ := json.Marshal(AnalyzeRequest{
		NodeName: "web-1",
		Metrics: []MetricDataAPI{
			{Name: "system.cpu.usage", Value: 96},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/analyze", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleAnalyze(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp AnalyzeResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotNil(t, resp.Result)
	assert.Equal(t, "web-1", resp.Result.NodeName)
}

func TestHandleAnalyzeWrongMethod(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/analyze", nil)
	rec := httptest.NewRecorder()
	h.handleAnalyze(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandleAnalyzeInvalidJSON(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/analyze", bytes.NewReader([]byte("not-json")))
	rec := httptest.NewRecorder()
	h.handleAnalyze(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── handleResults tests ───────────────────────────────────────────────

func TestHandleResultsEmpty(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/results", nil)
	rec := httptest.NewRecorder()
	h.handleResults(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp ResultsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Count)
}

func TestHandleResultsWithLimit(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/results?limit=5", nil)
	rec := httptest.NewRecorder()
	h.handleResults(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleResultsWrongMethod(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/results", nil)
	rec := httptest.NewRecorder()
	h.handleResults(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// ── handleStats tests ─────────────────────────────────────────────────

func TestHandleStats(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/stats", nil)
	rec := httptest.NewRecorder()
	h.handleStats(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp StatsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.False(t, resp.Stats.Running, "Module should not be running")
}

func TestHandleStatsWrongMethod(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/stats", nil)
	rec := httptest.NewRecorder()
	h.handleStats(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// ── handleIngest tests ────────────────────────────────────────────────

func TestHandleIngestSuccess(t *testing.T) {
	h := newTestHandler(t)

	body, _ := json.Marshal(IngestRequest{
		NodeName: "db-1",
		Metrics: []MetricDataAPI{
			{Name: "system.memory.usage", Value: 65},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/ingest", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleIngest(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleIngestWrongMethod(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/ingest", nil)
	rec := httptest.NewRecorder()
	h.handleIngest(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// ── convertRequest tests ──────────────────────────────────────────────

func TestConvertRequest(t *testing.T) {
	h := newTestHandler(t)

	req := AnalyzeRequest{
		NodeName: "web-1",
		Metrics: []MetricDataAPI{
			{Name: "cpu", Value: 80, Timestamp: "2026-01-01T00:00:00Z"},
		},
		Logs: []LogEntryAPI{
			{Message: "error occurred", Level: "error", Timestamp: "2026-01-01T00:00:00Z"},
		},
		K8sContext: &K8sMetadataAPI{
			Namespace: "default",
			PodName:   "web-pod-123",
		},
		Context: map[string]string{"env": "prod"},
	}

	dp := h.convertRequest(req)
	assert.Equal(t, "web-1", dp.NodeName)
	assert.Len(t, dp.Metrics, 1)
	assert.Equal(t, 80.0, dp.Metrics[0].Value)
	assert.Len(t, dp.Logs, 1)
	assert.NotNil(t, dp.K8sContext)
	assert.Equal(t, "default", dp.K8sContext.Namespace)
	assert.Equal(t, "prod", dp.Context["env"])
}

func TestConvertRequestNilK8s(t *testing.T) {
	h := newTestHandler(t)

	req := AnalyzeRequest{NodeName: "web-1"}
	dp := h.convertRequest(req)
	assert.Nil(t, dp.K8sContext)
}
