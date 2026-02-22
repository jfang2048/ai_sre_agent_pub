package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestTopProgramsBehaviorUIServesSPAAndSimpleFallback(t *testing.T) {
	webPath := t.TempDir()
	assetDir := filepath.Join(webPath, "assets")
	require.NoError(t, os.MkdirAll(assetDir, 0o755))

	const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>AI SRE Command Center</title>
  <script type="module" src="/assets/app.js"></script>
  <link rel="stylesheet" href="/assets/app.css">
</head>
<body>
  <div id="root"></div>
</body>
</html>`
	require.NoError(t, os.WriteFile(filepath.Join(webPath, "index.html"), []byte(indexHTML), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetDir, "app.js"), []byte(`console.log("ok");`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetDir, "app.css"), []byte(`body{margin:0;}`), 0o644))

	cfg := DefaultConfig()
	cfg.WebPath = webPath

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	spaReq := httptest.NewRequest(http.MethodGet, "/ui", nil)
	spaW := httptest.NewRecorder()
	mux.ServeHTTP(spaW, spaReq)

	assert.Equal(t, http.StatusOK, spaW.Code)
	spaBody := spaW.Body.String()
	assert.Contains(t, spaBody, "<title>AI SRE Command Center</title>")
	assert.Contains(t, spaBody, `<div id="root"></div>`)

	simpleReq := httptest.NewRequest(http.MethodGet, "/?simple=1", nil)
	simpleW := httptest.NewRecorder()
	mux.ServeHTTP(simpleW, simpleReq)

	assert.Equal(t, http.StatusOK, simpleW.Code)
	simpleBody := simpleW.Body.String()
	assert.Contains(t, simpleBody, "AI SRE Agent (simple)")
	assert.Contains(t, simpleBody, `<a href="/ui">Full UI</a>`)
}
