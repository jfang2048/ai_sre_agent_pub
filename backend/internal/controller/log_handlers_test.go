package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/stretchr/testify/require"
)

func TestHandleLogsIngestAndSearch(t *testing.T) {
	idx := logindex.NewIndex(logindex.DefaultConfig())
	ctrl := &Controller{logIndex: idx}
	now := time.Now().UTC()

	payload := map[string]interface{}{
		"collector_id": "collector-a",
		"hostname":     "node-a",
		"service":      "checkout",
		"entries": []map[string]interface{}{
			{
				"timestamp": now.Add(-30 * time.Second).Format(time.RFC3339Nano),
				"level":     "error",
				"process":   "checkout-worker",
				"message":   "request timeout while contacting db",
				"count":     2,
				"metrics": map[string]float64{
					"node_cpu_usage_percent": 88.5,
				},
			},
			{
				"timestamp": now.Add(-20 * time.Second).Format(time.RFC3339Nano),
				"level":     "info",
				"process":   "checkout-worker",
				"message":   "request completed",
				"count":     1,
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	ingestReq := httptest.NewRequest(http.MethodPost, "/api/v1/logs/ingest", bytes.NewReader(body))
	ingestW := httptest.NewRecorder()
	ctrl.handleLogsIngest(ingestW, ingestReq)
	require.Equal(t, http.StatusAccepted, ingestW.Code)

	searchReq := httptest.NewRequest(http.MethodGet, "/api/v1/logs/search?q=timeout&service=checkout&collector_id=collector-a&window=1h", nil)
	searchW := httptest.NewRecorder()
	ctrl.handleLogsSearch(searchW, searchReq)
	require.Equal(t, http.StatusOK, searchW.Code)

	var result logindex.SearchResult
	require.NoError(t, json.NewDecoder(searchW.Body).Decode(&result))
	require.Equal(t, 1, result.Total)
	require.Len(t, result.Entries, 1)
	require.Equal(t, logindex.LevelError, result.Entries[0].Level)
	require.Equal(t, "checkout", result.Entries[0].Service)
	require.NotEmpty(t, result.LevelCounts)
}

func TestHandleLogsStatusAndMethodGuards(t *testing.T) {
	ctrl := &Controller{logIndex: logindex.NewIndex(logindex.DefaultConfig())}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/logs/status", nil)
	statusW := httptest.NewRecorder()
	ctrl.handleLogsStatus(statusW, statusReq)
	require.Equal(t, http.StatusOK, statusW.Code)

	searchReq := httptest.NewRequest(http.MethodPost, "/api/v1/logs/search", nil)
	searchW := httptest.NewRecorder()
	ctrl.handleLogsSearch(searchW, searchReq)
	require.Equal(t, http.StatusMethodNotAllowed, searchW.Code)

	ingestReq := httptest.NewRequest(http.MethodGet, "/api/v1/logs/ingest", nil)
	ingestW := httptest.NewRecorder()
	ctrl.handleLogsIngest(ingestW, ingestReq)
	require.Equal(t, http.StatusMethodNotAllowed, ingestW.Code)
}
