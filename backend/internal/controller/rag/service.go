package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Service is the persistent local-first knowledge base implementation.
type Service struct {
	cfg      Config
	logger   *zap.Logger
	embedder embedder
	vector   vectorBackend

	mu               sync.RWMutex
	index            *indexData
	stats            Stats
	vectorHealthy    bool
	vectorGeneration string
	vectorLastError  string
}

// NewService initializes a persistent RAG service.
func NewService(cfg Config, logger *zap.Logger) (*Service, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = ConfigFromEnv(cfg)
	service := &Service{
		cfg:      cfg,
		logger:   logger.With(zap.String("component", "rag_service")),
		embedder: newEmbedder(cfg, logger),
		stats: Stats{
			Enabled:           cfg.Enabled,
			DatasetPath:       cfg.DatasetPath,
			IndexPath:         cfg.IndexPath,
			StoragePath:       storagePath(cfg.IndexPath),
			CachePath:         cachePath(cfg.IndexPath),
			RetrievalMode:     cfg.RetrievalMode,
			EmbeddingProvider: "",
			EmbeddingModel:    "",
			VectorBackend:     cfg.VectorBackend,
			VectorCollection:  cfg.VectorCollection,
			ChunkSize:         cfg.ChunkSize,
			ChunkOverlap:      cfg.ChunkOverlap,
			MaxSnippetLen:     cfg.MaxSnippetChars,
		},
	}
	if service.embedder != nil {
		service.stats.EmbeddingProvider = service.embedder.Provider()
		service.stats.EmbeddingModel = service.embedder.Model()
	}
	if vectorBackend, err := newVectorBackend(cfg, logger); err != nil {
		service.vectorLastError = err.Error()
		service.stats.VectorLastError = err.Error()
		service.logger.Warn("rag vector backend unavailable; continuing with local retrieval fallback", zap.Error(err))
	} else {
		service.vector = vectorBackend
	}
	if !cfg.Enabled {
		return service, nil
	}

	if index, err := loadIndex(cfg.IndexPath); err == nil {
		service.index = index
		service.refreshStatsLocked(service.vectorLastError)
	} else if !errors.Is(err, os.ErrNotExist) {
		service.stats.LastError = err.Error()
	}

	needsBuild := service.index == nil
	if cfg.RebuildPolicy == "startup" || (cfg.RebuildPolicy == "if_missing" && needsBuild) {
		if _, err := service.Rebuild(context.Background()); err != nil && service.index == nil {
			return service, err
		}
	} else if service.index != nil {
		service.syncVectorBackend(context.Background(), service.index)
	}

	return service, nil
}

// NewLocalRetriever preserves the older constructor name used by the agent query service.
func NewLocalRetriever(cfg Config, logger *zap.Logger) (*Service, error) {
	return NewService(cfg, logger)
}

// Stats returns a snapshot of current index state.
func (s *Service) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneStats(s.stats)
}

// Search implements the Retriever interface for the agent query service.
func (s *Service) Search(ctx context.Context, query string, topK int) ([]Document, error) {
	result, err := s.Query(ctx, QueryRequest{Query: query, TopK: topK})
	if err != nil {
		return nil, err
	}
	docs := make([]Document, 0, len(result.Hits))
	for _, hit := range result.Hits {
		docs = append(docs, Document{
			ID:         hit.ChunkID,
			DocID:      hit.DocID,
			ChunkID:    hit.ChunkID,
			Path:       hit.SourcePath,
			SourcePath: hit.SourcePath,
			SourceType: hit.SourceType,
			Title:      hit.Title,
			Content:    hit.Content,
			Tags:       append([]string(nil), hit.Tags...),
			Metadata:   cloneMap(hit.Metadata),
			Timestamp:  cloneTime(hit.Timestamp),
		})
	}
	return docs, nil
}

// Query performs structured RAG retrieval.
func (s *Service) Query(ctx context.Context, req QueryRequest) (QueryResult, error) {
	started := time.Now()
	s.mu.RLock()
	index := s.index
	cfg := s.cfg
	embedder := s.embedder
	vector := s.vector
	vectorHealthy := s.vectorHealthy
	vectorGeneration := s.vectorGeneration
	s.mu.RUnlock()

	query := strings.TrimSpace(req.Query)
	if query == "" {
		return QueryResult{Query: "", RetrievalMode: cfg.RetrievalMode}, nil
	}
	if index == nil || len(index.Chunks) == 0 {
		return QueryResult{Query: query, RetrievalMode: cfg.RetrievalMode}, nil
	}
	topK := req.TopK
	if topK <= 0 {
		topK = cfg.TopK
	}
	lexicalScores := index.lexicalScores(tokenize(query))
	vectorScores := map[string]float64{}
	vectorErr := ""
	if strings.EqualFold(cfg.RetrievalMode, "lexical") {
		vectorScores = map[string]float64{}
	} else if vector != nil && vectorHealthy && embedder != nil && strings.TrimSpace(vectorGeneration) != "" {
		queryVector, err := embedder.EmbedQuery(ctx, query)
		if err == nil && len(queryVector) > 0 {
			vectorScores, err = vector.SearchScores(ctx, vectorSearchRequest{
				Collection: cfg.VectorCollection,
				Generation: vectorGeneration,
				Vector:     queryVector,
				Limit:      maxInt(topK*3, topK),
			})
		}
		if err != nil {
			vectorErr = err.Error()
			s.logger.Warn("rag external vector search failed; falling back to local vector index",
				zap.String("backend", vector.Name()),
				zap.Error(err))
			vectorScores = index.vectorScores(query, cfg, embedder)
		} else {
			s.updateVectorStatus(true, vectorGeneration, "")
		}
	} else {
		vectorScores = index.vectorScores(query, cfg, embedder)
	}
	if vectorErr != "" {
		s.updateVectorStatus(false, vectorGeneration, vectorErr)
	}
	result := index.buildQueryResult(query, cfg, topK, combineScores(cfg.RetrievalMode, lexicalScores, vectorScores))
	result.LatencyMS = time.Since(started).Milliseconds()
	return result, nil
}

// Rebuild performs a full dataset rescan and index rebuild.
func (s *Service) Rebuild(ctx context.Context) (Stats, error) {
	index, err := s.buildIndex(ctx, false)
	if err != nil {
		s.mu.Lock()
		s.stats.LastError = err.Error()
		s.mu.Unlock()
		return s.Stats(), err
	}
	stats, err := s.persistIndex(index)
	if err != nil {
		return stats, err
	}
	s.syncVectorBackend(ctx, index)
	return s.Stats(), nil
}

// Update performs an incremental update by reusing unchanged sources from the existing index.
func (s *Service) Update(ctx context.Context) (Stats, error) {
	index, err := s.buildIndex(ctx, true)
	if err != nil {
		s.mu.Lock()
		s.stats.LastError = err.Error()
		s.mu.Unlock()
		return s.Stats(), err
	}
	stats, err := s.persistIndex(index)
	if err != nil {
		return stats, err
	}
	s.syncVectorBackend(ctx, index)
	return s.Stats(), nil
}

// Document returns a document and its chunks by document ID or chunk ID.
func (s *Service) Document(id string) (DocumentRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return DocumentRecord{}, false
	}
	return s.index.document(id)
}

func (s *Service) buildIndex(ctx context.Context, incremental bool) (*indexData, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	units, quarantine, err := discoverSourceUnits(s.cfg)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	existing := s.index
	s.mu.RUnlock()

	now := time.Now().UTC()
	docsByID := make(map[string]SourceDocument, 256)
	chunksByID := make(map[string]Chunk, 512)
	sourcesByKey := make(map[string]sourceRecord, len(units))

	for _, unit := range units {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if incremental && existing != nil {
			if previous, ok := existing.Sources[unit.SourceKey]; ok && previous.Signature == unit.Signature {
				sourcesByKey[unit.SourceKey] = previous
				for _, docID := range previous.DocIDs {
					if doc, ok := existing.Documents[docID]; ok {
						docsByID[docID] = doc
					}
				}
				for _, chunkID := range previous.ChunkIDs {
					if chunk, ok := existing.Chunks[chunkID]; ok {
						chunksByID[chunkID] = chunk
					}
				}
				continue
			}
		}

		documents, err := parseSourceUnit(unit)
		if err != nil {
			quarantine = append(quarantine, QuarantineRecord{
				Path:       unit.SourcePath,
				SourceType: unit.SourceType,
				Reason:     err.Error(),
			})
			continue
		}
		record := sourceRecord{
			SourceKey:  unit.SourceKey,
			SourcePath: unit.SourcePath,
			SourceType: unit.SourceType,
			Signature:  unit.Signature,
		}
		if len(documents) == 0 {
			sourcesByKey[unit.SourceKey] = record
			continue
		}
		pendingChunks := make([]Chunk, 0, len(documents)*2)
		for _, doc := range documents {
			docsByID[doc.DocID] = doc
			record.DocIDs = append(record.DocIDs, doc.DocID)
			chunks := chunkDocuments(doc, s.cfg)
			pendingChunks = append(pendingChunks, chunks...)
		}
		if err := s.embedChunks(ctx, pendingChunks); err != nil {
			return nil, err
		}
		for _, chunk := range pendingChunks {
			chunksByID[chunk.ChunkID] = chunk
			record.ChunkIDs = append(record.ChunkIDs, chunk.ChunkID)
		}
		sourcesByKey[unit.SourceKey] = record
	}

	docs := make([]SourceDocument, 0, len(docsByID))
	for _, doc := range docsByID {
		docs = append(docs, doc)
	}
	chunks := make([]Chunk, 0, len(chunksByID))
	for _, chunk := range chunksByID {
		chunks = append(chunks, chunk)
	}
	sources := make([]sourceRecord, 0, len(sourcesByKey))
	for _, source := range sourcesByKey {
		sources = append(sources, source)
	}
	builtAt := now
	if incremental && existing != nil && !existing.BuiltAt.IsZero() {
		builtAt = existing.BuiltAt
	}
	index := newIndexData(builtAt, docs, chunks, sources, quarantine)
	index.UpdatedAt = now
	return index, nil
}

func (s *Service) embedChunks(ctx context.Context, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Title+"\n"+chunk.Content)
	}
	vectors, err := s.embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		if s.embedder.Provider() != "local" {
			s.logger.Warn("rag embedding provider failed; retrying with local deterministic embeddings",
				zap.String("provider", s.embedder.Provider()),
				zap.Error(err))
			vectors, err = localEmbedder{}.EmbedDocuments(ctx, texts)
		}
	}
	if err != nil {
		return fmt.Errorf("embed rag chunks: %w", err)
	}
	for index := range chunks {
		if index < len(vectors) {
			chunks[index].Embedding = vectors[index]
		}
	}
	return nil
}

func (s *Service) persistIndex(index *indexData) (Stats, error) {
	if err := saveIndex(s.cfg.IndexPath, index); err != nil {
		return s.Stats(), err
	}
	if err := persistQuarantineManifest(s.cfg.IndexPath, index.Quarantine); err != nil {
		return s.Stats(), err
	}
	s.mu.Lock()
	s.index = index
	s.refreshStatsLocked("")
	s.mu.Unlock()
	return s.Stats(), nil
}

func (s *Service) syncVectorBackend(ctx context.Context, index *indexData) {
	if s == nil || index == nil {
		return
	}

	s.mu.RLock()
	vector := s.vector
	cfg := s.cfg
	currentError := s.vectorLastError
	s.mu.RUnlock()
	if vector == nil {
		s.updateVectorStatus(false, "", currentError)
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}
	generation := index.UpdatedAt.UTC().Format(time.RFC3339Nano)
	chunks := make([]Chunk, 0, len(index.Chunks))
	for _, chunk := range index.Chunks {
		chunks = append(chunks, chunk)
	}
	if err := vector.Sync(ctx, vectorSyncRequest{
		Collection: cfg.VectorCollection,
		Generation: generation,
		Chunks:     chunks,
	}); err != nil {
		s.logger.Warn("rag vector backend sync failed; continuing with local retrieval fallback",
			zap.String("backend", vector.Name()),
			zap.Error(err))
		s.updateVectorStatus(false, generation, err.Error())
		return
	}
	s.updateVectorStatus(true, generation, "")
}

func persistQuarantineManifest(indexPath string, records []QuarantineRecord) error {
	if err := os.MkdirAll(storagePath(indexPath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(quarantinePath(indexPath), raw, 0o644)
}

func (s *Service) refreshStatsLocked(lastError string) {
	s.stats.Enabled = s.cfg.Enabled
	s.stats.DatasetPath = s.cfg.DatasetPath
	s.stats.IndexPath = s.cfg.IndexPath
	s.stats.StoragePath = storagePath(s.cfg.IndexPath)
	s.stats.CachePath = cachePath(s.cfg.IndexPath)
	s.stats.RetrievalMode = s.cfg.RetrievalMode
	s.stats.VectorBackend = s.cfg.VectorBackend
	s.stats.VectorCollection = s.cfg.VectorCollection
	s.stats.VectorHealthy = s.vectorHealthy
	s.stats.VectorGeneration = s.vectorGeneration
	s.stats.VectorLastError = s.vectorLastError
	s.stats.ChunkSize = s.cfg.ChunkSize
	s.stats.ChunkOverlap = s.cfg.ChunkOverlap
	s.stats.MaxSnippetLen = s.cfg.MaxSnippetChars
	if s.embedder != nil {
		s.stats.EmbeddingProvider = s.embedder.Provider()
		s.stats.EmbeddingModel = s.embedder.Model()
	}
	s.stats.LastError = lastError
	if s.index == nil {
		s.stats.Ready = false
		s.stats.DocCount = 0
		s.stats.ChunkCount = 0
		s.stats.SourceCount = 0
		s.stats.QuarantineCount = 0
		s.stats.SourceTypes = nil
		return
	}
	s.stats.Ready = len(s.index.Chunks) > 0
	s.stats.DocCount = len(s.index.Documents)
	s.stats.ChunkCount = len(s.index.Chunks)
	s.stats.SourceCount = len(s.index.Sources)
	s.stats.QuarantineCount = len(s.index.Quarantine)
	s.stats.LastBuiltAt = s.index.BuiltAt
	s.stats.LastUpdatedAt = s.index.UpdatedAt
	s.stats.SourceTypes = make(map[string]int, len(s.index.sourceTypeCount))
	for key, value := range s.index.sourceTypeCount {
		s.stats.SourceTypes[key] = value
	}
}

func cloneStats(in Stats) Stats {
	out := in
	if in.SourceTypes != nil {
		out.SourceTypes = make(map[string]int, len(in.SourceTypes))
		for key, value := range in.SourceTypes {
			out.SourceTypes[key] = value
		}
	}
	return out
}

func (s *Service) updateVectorStatus(healthy bool, generation, lastError string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vectorHealthy = healthy
	if strings.TrimSpace(generation) != "" {
		s.vectorGeneration = generation
	}
	s.vectorLastError = strings.TrimSpace(lastError)
	s.refreshStatsLocked(s.stats.LastError)
}

func sortedSourceKeys(in map[string]sourceRecord) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
