package controller

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRAGHandlersRespondWithStructuredResults(t *testing.T) {
	service := newTestRAGService(t)
	controller := &Controller{
		config:     DefaultConfig(),
		logger:     zap.NewNop(),
		ragService: service,
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/rag/status", nil)
	statusResp := httptest.NewRecorder()
	controller.handleRAGStatus(statusResp, statusReq)
	require.Equal(t, http.StatusOK, statusResp.Code)

	var status rag.Stats
	require.NoError(t, json.Unmarshal(statusResp.Body.Bytes(), &status))
	require.True(t, status.Ready)
	require.Greater(t, status.DocCount, 0)

	queryReq := httptest.NewRequest(http.MethodPost, "/api/v1/rag/query", bytes.NewBufferString(`{"query":"timeout deployment runbook","top_k":4}`))
	queryResp := httptest.NewRecorder()
	controller.handleRAGQuery(queryResp, queryReq)
	require.Equal(t, http.StatusOK, queryResp.Code)

	var result rag.QueryResult
	require.NoError(t, json.Unmarshal(queryResp.Body.Bytes(), &result))
	require.NotEmpty(t, result.Hits)
	require.NotEmpty(t, result.RetrievalMode)

	docReq := httptest.NewRequest(http.MethodGet, "/api/v1/rag/doc/"+result.Hits[0].ChunkID, nil)
	docResp := httptest.NewRecorder()
	controller.handleRAGDocument(docResp, docReq)
	require.Equal(t, http.StatusOK, docResp.Code)

	var document rag.DocumentRecord
	require.NoError(t, json.Unmarshal(docResp.Body.Bytes(), &document))
	require.Equal(t, result.Hits[0].ChunkID, document.RequestedID)
	require.NotEmpty(t, document.Chunks)

	updateReq := httptest.NewRequest(http.MethodPost, "/api/v1/rag/index/update", nil)
	updateResp := httptest.NewRecorder()
	controller.handleRAGUpdate(updateResp, updateReq)
	require.Equal(t, http.StatusOK, updateResp.Code)

	rebuildReq := httptest.NewRequest(http.MethodPost, "/api/v1/rag/index/rebuild", nil)
	rebuildResp := httptest.NewRecorder()
	controller.handleRAGRebuild(rebuildResp, rebuildReq)
	require.Equal(t, http.StatusOK, rebuildResp.Code)
}

func TestRAGStatusDisabledWithoutService(t *testing.T) {
	controller := &Controller{config: DefaultConfig(), logger: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag/status", nil)
	resp := httptest.NewRecorder()

	controller.handleRAGStatus(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	var status rag.Stats
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &status))
	require.False(t, status.Enabled)
	require.False(t, status.Ready)
}

func newTestRAGService(t *testing.T) rag.KnowledgeBase {
	t.Helper()

	datasetDir := filepath.Join(t.TempDir(), "dataset")
	require.NoError(t, os.MkdirAll(filepath.Join(datasetDir, "cases"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(datasetDir, "cases", "runbook.md"), []byte("# Timeout Runbook\nvalidate cache credentials during deployments\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(datasetDir, "cases", "cases.jsonl"), []byte("{\"id\":\"case-1\",\"query\":\"timeout deployment\",\"document\":\"Cache credential regressions caused post-deploy timeouts.\"}\n"), 0o644))

	archivePath := filepath.Join(datasetDir, "manual.zip")
	file, err := os.Create(archivePath)
	require.NoError(t, err)
	zipWriter := zip.NewWriter(file)
	entry, err := zipWriter.Create("docs/cache-guide.txt")
	require.NoError(t, err)
	_, err = entry.Write([]byte("Cache guide: deployment rollouts should verify rotated credentials before traffic shift."))
	require.NoError(t, err)
	require.NoError(t, zipWriter.Close())
	require.NoError(t, file.Close())

	cfg := rag.DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")

	service, err := rag.NewService(cfg, zap.NewNop())
	require.NoError(t, err)
	_, err = service.Rebuild(context.Background())
	require.NoError(t, err)
	return service
}
