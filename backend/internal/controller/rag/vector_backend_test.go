package rag

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func requireLocalTCPBind(t *testing.T) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("skipping due to listen permission error: %v", err)
		}
		t.Fatalf("listen: %v", err)
	}
	_ = listener.Close()
}

func newLocalTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("skipping due to listen permission error: %v", err)
		}
		require.NoError(t, err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	return server
}

func TestServiceUsesMilvusVectorBackendWhenConfigured(t *testing.T) {
	requireLocalTCPBind(t)

	datasetDir := writeTestDataset(t)

	var mu sync.Mutex
	createCalls := 0
	upsertCalls := 0
	searchCalls := 0
	firstChunkID := ""
	lastSearchPayload := map[string]any{}

	server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)

		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v2/vectordb/collections/create":
			createCalls++
			writeMilvusJSON(w, map[string]any{"code": 0})
		case "/v2/vectordb/entities/upsert":
			upsertCalls++
			if data, ok := payload["data"].([]any); ok && len(data) > 0 {
				if entity, ok := data[0].(map[string]any); ok {
					if chunkID, ok := entity["chunk_id"].(string); ok && firstChunkID == "" {
						firstChunkID = chunkID
					}
				}
			}
			writeMilvusJSON(w, map[string]any{"code": 0})
		case "/v2/vectordb/entities/search":
			searchCalls++
			lastSearchPayload = payload
			writeMilvusJSON(w, map[string]any{
				"code": 0,
				"data": []any{
					[]any{
						map[string]any{
							"entity":   map[string]any{"chunk_id": firstChunkID},
							"distance": 0.97,
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")
	cfg.RebuildPolicy = "manual"
	cfg.RetrievalMode = "vector"
	cfg.VectorBackend = "milvus"
	cfg.VectorEndpoint = server.URL
	cfg.VectorCollection = "sre_rag_chunks"

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)

	stats, err := service.Rebuild(context.Background())
	require.NoError(t, err)
	require.True(t, stats.VectorHealthy)
	require.NotEmpty(t, stats.VectorGeneration)

	result, err := service.Query(context.Background(), QueryRequest{
		Query: "timeout deployment cache rollback",
		TopK:  1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hits)

	mu.Lock()
	defer mu.Unlock()
	require.Greater(t, createCalls, 0)
	require.Greater(t, upsertCalls, 0)
	require.Greater(t, searchCalls, 0)
	require.Equal(t, firstChunkID, result.Hits[0].ChunkID)
	require.Equal(t, cfg.VectorCollection, lastSearchPayload["collectionName"])
	require.Contains(t, lastSearchPayload["filter"], stats.VectorGeneration)
}

func TestServiceFallsBackToLocalVectorWhenMilvusSearchFails(t *testing.T) {
	requireLocalTCPBind(t)

	datasetDir := writeTestDataset(t)

	server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/vectordb/collections/create", "/v2/vectordb/entities/upsert":
			writeMilvusJSON(w, map[string]any{"code": 0})
		case "/v2/vectordb/entities/search":
			http.Error(w, "milvus unavailable", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")
	cfg.RebuildPolicy = "manual"
	cfg.RetrievalMode = "vector"
	cfg.VectorBackend = "milvus"
	cfg.VectorEndpoint = server.URL

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)

	stats, err := service.Rebuild(context.Background())
	require.NoError(t, err)
	require.True(t, stats.VectorHealthy)

	result, err := service.Query(context.Background(), QueryRequest{
		Query: "cache credential timeout",
		TopK:  2,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hits)

	stats = service.Stats()
	require.False(t, stats.VectorHealthy)
	require.Contains(t, stats.VectorLastError, "milvus")
}

func TestServiceFallsBackToLocalWhenMilvusConfigIncomplete(t *testing.T) {
	datasetDir := writeTestDataset(t)

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")
	cfg.RebuildPolicy = "manual"
	cfg.RetrievalMode = "hybrid"
	cfg.VectorBackend = "milvus"
	cfg.VectorEndpoint = ""

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)

	stats, err := service.Rebuild(context.Background())
	require.NoError(t, err)
	require.False(t, stats.VectorHealthy)
	require.Contains(t, stats.VectorLastError, "vector_endpoint")

	result, err := service.Query(context.Background(), QueryRequest{Query: "dns timeout deployment", TopK: 3})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hits)
}

func writeMilvusJSON(w http.ResponseWriter, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
