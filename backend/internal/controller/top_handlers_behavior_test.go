package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTopProgramsBehaviorAPIIncludesPerCategoryAndUnifiedReport(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	store.StoreProcesses("collector-a", []*telemetryv1.ProcessSample{
		{Pid: 11, Name: "cpu-heavy", CpuPercent: 97, RssBytes: 256 * 1024 * 1024},
		{Pid: 22, Name: "loggy", CpuPercent: 12, RssBytes: 128 * 1024 * 1024},
	}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "rca_net_process_connections", Value: 70, Labels: []*telemetryv1.Label{{Key: "pid", Value: "22"}, {Key: "name", Value: "loggy"}}},
	}, now)
	store.StoreLogs("collector-a", []*telemetryv1.LogFingerprint{
		{Fingerprint: "l1", Count: 4, Example: "loggy[22]: ERROR timeout"},
	}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/top/programs?limit=10", nil)
	w := httptest.NewRecorder()
	ctrl.handleTopPrograms(w, req)

	var resp TopProgramsResponse
	require.NoError(t, json.NewDecoder(w.Result().Body).Decode(&resp))
	require.NotEmpty(t, resp.Programs)
	assert.Contains(t, resp.ByCategory, "cpu")
	assert.Contains(t, resp.ByCategory, "logs")
	if assert.NotNil(t, resp.Report.TopOverall) {
		assert.NotEmpty(t, resp.Report.TopOverall.Name)
	}
	if assert.NotNil(t, resp.Report.MostProblematic) {
		assert.Equal(t, "loggy", resp.Report.MostProblematic.Name)
	}
	assert.NotEmpty(t, resp.Report.Hotspots)
}

func TestTopProgramsBehaviorUIContainsSummaryAndCategoryPanels(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	webPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "web")

	cfg := DefaultConfig()
	cfg.WebPath = webPath

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Unified Program Hotspot Report")
	assert.Contains(t, body, "Top Programs by Resource and Logs")
	assert.Contains(t, body, "Most Resource-Intensive / Problematic Programs")
}
