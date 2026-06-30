package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
)

const ragDocPathPrefix = "/api/v1/rag/doc/"

func (c *Controller) registerRAGHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/rag/status", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleRAGStatus(w, r)
	}))
	mux.HandleFunc("/api/v1/rag/query", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleRAGQuery(w, r)
	}))
	mux.HandleFunc("/api/v1/rag/index/rebuild", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleRAGRebuild(w, r)
	}))
	mux.HandleFunc("/api/v1/rag/reindex", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleRAGRebuild(w, r)
	}))
	mux.HandleFunc("/api/v1/rag/index/update", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleRAGUpdate(w, r)
	}))
	mux.HandleFunc("/api/v1/rag/doc/", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		c.handleRAGDocument(w, r)
	}))
}

func (c *Controller) handleRAGStatus(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if c.ragService == nil {
		writeJSON(w, disabledRAGStatus())
		return
	}
	writeJSON(w, c.ragService.Stats())
}

func (c *Controller) handleRAGQuery(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireRAGService(w) {
		return
	}
	var payload rag.QueryRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		if err == io.EOF {
			http.Error(w, "query is required", http.StatusBadRequest)
			return
		}
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	result, err := c.ragService.Query(r.Context(), payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, result)
}

func (c *Controller) handleRAGRebuild(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireActiveController(w) || !c.requireRAGService(w) {
		return
	}
	stats, err := c.ragService.Rebuild(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, stats)
}

func (c *Controller) handleRAGUpdate(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !c.requireActiveController(w) || !c.requireRAGService(w) {
		return
	}
	stats, err := c.ragService.Update(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, stats)
}

func (c *Controller) handleRAGDocument(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !c.requireRAGService(w) {
		return
	}
	id, err := parseSimplePathSegment(r.URL.Path, ragDocPathPrefix, "document id required")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, ok := c.ragService.Document(id)
	if !ok {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}
	writeJSON(w, record)
}

func (c *Controller) requireRAGService(w http.ResponseWriter) bool {
	if c.ragService != nil {
		return true
	}
	http.Error(w, "rag service disabled", http.StatusServiceUnavailable)
	return false
}

func disabledRAGStatus() rag.Stats {
	cfg := rag.DefaultConfig()
	return rag.Stats{
		Enabled:           false,
		Ready:             false,
		DatasetPath:       cfg.DatasetPath,
		IndexPath:         cfg.IndexPath,
		StoragePath:       "data/agent/rag",
		CachePath:         "data/agent/rag/cache",
		RetrievalMode:     cfg.RetrievalMode,
		EmbeddingProvider: cfg.EmbeddingProvider,
		EmbeddingModel:    cfg.EmbeddingModel,
		ChunkSize:         cfg.ChunkSize,
		ChunkOverlap:      cfg.ChunkOverlap,
		MaxSnippetLen:     cfg.MaxSnippetChars,
		LastUpdatedAt:     time.Now().UTC(),
		LastError:         "rag service disabled",
	}
}
