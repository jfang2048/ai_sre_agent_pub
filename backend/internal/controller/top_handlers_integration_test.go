package controller

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleTopProgramsReturnsJSON(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()
	store.StoreProcesses("c-1", []*telemetryv1.ProcessSample{
		{Pid: 1, Name: "demo", CpuPercent: 50},
	}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest("GET", "/api/v1/top/programs?limit=5", nil)
	w := httptest.NewRecorder()

	ctrl.handleTopPrograms(w, req)

	resp := w.Result()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var payload TopProgramsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	assert.Equal(t, 1, payload.Count)
	assert.NotEmpty(t, payload.GeneratedAt)
	assert.Contains(t, payload.ByCategory, "cpu")
	assert.NotNil(t, payload.Report.TopOverall)
	assert.Equal(t, "demo", payload.Report.TopOverall.Name)
}

func TestHandleTopProgramsLimitRobustness(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()
	store.StoreProcesses("c-1", []*telemetryv1.ProcessSample{
		{Pid: 1, Name: "one", CpuPercent: 30},
		{Pid: 2, Name: "two", CpuPercent: 20},
	}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest("GET", "/api/v1/top/programs?limit=99999", nil)
	w := httptest.NewRecorder()
	ctrl.handleTopPrograms(w, req)

	var payload TopProgramsResponse
	require.NoError(t, json.NewDecoder(w.Result().Body).Decode(&payload))
	assert.Equal(t, maxTopProgramsLimit, payload.Limit)
	assert.Equal(t, 2, payload.Count)

	req = httptest.NewRequest("GET", "/api/v1/top/programs?limit=bad", nil)
	w = httptest.NewRecorder()
	ctrl.handleTopPrograms(w, req)
	require.NoError(t, json.NewDecoder(w.Result().Body).Decode(&payload))
	assert.Equal(t, defaultTopProgramsLimit, payload.Limit)
}
