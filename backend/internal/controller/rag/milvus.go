package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
)

type milvusBackend struct {
	baseURL      string
	database     string
	token        string
	defaultColl  string
	client       *http.Client
	logger       *zap.Logger
	collectionMu sync.Mutex
	readyByName  map[string]bool
}

func newMilvusBackend(cfg Config, logger *zap.Logger) (*milvusBackend, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	timeout := cfg.VectorTimeout
	if timeout <= 0 {
		timeout = DefaultConfig().VectorTimeout
	}
	return &milvusBackend{
		baseURL:     strings.TrimRight(strings.TrimSpace(cfg.VectorEndpoint), "/"),
		database:    strings.TrimSpace(cfg.VectorDatabase),
		token:       strings.TrimSpace(cfg.VectorToken),
		defaultColl: strings.TrimSpace(cfg.VectorCollection),
		client:      &http.Client{Timeout: timeout},
		logger:      logger.With(zap.String("component", "rag_milvus")),
		readyByName: make(map[string]bool),
	}, nil
}

func (m *milvusBackend) Name() string { return "milvus" }

func (m *milvusBackend) Sync(ctx context.Context, req vectorSyncRequest) error {
	collection := firstNonEmptyString(req.Collection, m.defaultColl)
	chunks := make([]Chunk, 0, len(req.Chunks))
	dimension := 0
	for _, chunk := range req.Chunks {
		if len(chunk.Embedding) == 0 {
			continue
		}
		if dimension == 0 {
			dimension = len(chunk.Embedding)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return nil
	}
	if dimension == 0 {
		return fmt.Errorf("milvus sync requires at least one embedded chunk")
	}
	if err := m.ensureCollection(ctx, collection, dimension); err != nil {
		return err
	}

	for start := 0; start < len(chunks); start += 128 {
		end := minInt(start+128, len(chunks))
		data := make([]map[string]any, 0, end-start)
		for _, chunk := range chunks[start:end] {
			data = append(data, map[string]any{
				"chunk_id":    chunk.ChunkID,
				"doc_id":      chunk.DocID,
				"source_path": chunk.SourcePath,
				"source_type": chunk.SourceType,
				"title":       chunk.Title,
				"generation":  req.Generation,
				"embedding":   chunk.Embedding,
			})
		}
		payload := map[string]any{
			"collectionName": collection,
			"data":           data,
		}
		if m.database != "" {
			payload["dbName"] = m.database
		}
		if _, err := m.postJSON(ctx, "/v2/vectordb/entities/upsert", payload); err != nil {
			return fmt.Errorf("milvus upsert collection %q: %w", collection, err)
		}
	}
	return nil
}

func (m *milvusBackend) SearchScores(ctx context.Context, req vectorSearchRequest) (map[string]float64, error) {
	if len(req.Vector) == 0 {
		return map[string]float64{}, nil
	}
	collection := firstNonEmptyString(req.Collection, m.defaultColl)
	payload := map[string]any{
		"collectionName": collection,
		"annsField":      "embedding",
		"data":           [][]float32{req.Vector},
		"limit":          maxInt(req.Limit, 1),
		"outputFields":   []string{"chunk_id", "generation"},
	}
	if req.Generation != "" {
		payload["filter"] = fmt.Sprintf("generation == %q", req.Generation)
	}
	if m.database != "" {
		payload["dbName"] = m.database
	}
	body, err := m.postJSON(ctx, "/v2/vectordb/entities/search", payload)
	if err != nil {
		return nil, fmt.Errorf("milvus search collection %q: %w", collection, err)
	}
	return decodeMilvusSearchScores(body), nil
}

func (m *milvusBackend) ensureCollection(ctx context.Context, collection string, dimension int) error {
	m.collectionMu.Lock()
	ready := m.readyByName[collection]
	m.collectionMu.Unlock()
	if ready {
		return nil
	}

	payload := map[string]any{
		"collectionName":     collection,
		"dimension":          dimension,
		"metricType":         "COSINE",
		"primaryFieldName":   "chunk_id",
		"idType":             "VarChar",
		"vectorFieldName":    "embedding",
		"enableDynamicField": true,
		"autoID":             false,
	}
	if m.database != "" {
		payload["dbName"] = m.database
	}
	if _, err := m.postJSON(ctx, "/v2/vectordb/collections/create", payload); err != nil && !looksLikeMilvusAlreadyExists(err) {
		return fmt.Errorf("milvus ensure collection %q: %w", collection, err)
	}
	m.collectionMu.Lock()
	m.readyByName[collection] = true
	m.collectionMu.Unlock()
	return nil
}

func (m *milvusBackend) postJSON(ctx context.Context, path string, payload any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	if code := extractMilvusCode(decoded); code != 0 {
		msg := extractMilvusMessage(decoded)
		if msg == "" {
			msg = "milvus request failed"
		}
		return nil, fmt.Errorf("code %d: %s", code, msg)
	}
	return decoded, nil
}

func decodeMilvusSearchScores(body map[string]any) map[string]float64 {
	scores := map[string]float64{}
	data, ok := body["data"]
	if !ok {
		return scores
	}
	for _, hit := range flattenMilvusData(data) {
		chunkID := strings.TrimSpace(extractMilvusChunkID(hit))
		if chunkID == "" {
			continue
		}
		score := extractMilvusScore(hit)
		if score <= 0 {
			continue
		}
		if score > scores[chunkID] {
			scores[chunkID] = score
		}
	}
	return scores
}

func flattenMilvusData(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			switch nested := item.(type) {
			case map[string]any:
				out = append(out, nested)
			case []any:
				out = append(out, flattenMilvusData(nested)...)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{typed}
	default:
		return nil
	}
}

func extractMilvusChunkID(hit map[string]any) string {
	if entity, ok := hit["entity"].(map[string]any); ok {
		if value := milvusString(entity["chunk_id"]); value != "" {
			return value
		}
	}
	if value := milvusString(hit["chunk_id"]); value != "" {
		return value
	}
	if value := milvusString(hit["id"]); value != "" {
		return value
	}
	return ""
}

func extractMilvusScore(hit map[string]any) float64 {
	if value, ok := milvusFloat(hit["distance"]); ok {
		return value
	}
	if value, ok := milvusFloat(hit["score"]); ok {
		return value
	}
	return 0
}

func extractMilvusCode(body map[string]any) int {
	if body == nil {
		return 0
	}
	if value, ok := milvusFloat(body["code"]); ok {
		return int(value)
	}
	if value, ok := milvusFloat(body["status"]); ok {
		return int(value)
	}
	return 0
}

func extractMilvusMessage(body map[string]any) string {
	if body == nil {
		return ""
	}
	for _, key := range []string{"message", "msg", "reason"} {
		if value := milvusString(body[key]); value != "" {
			return value
		}
	}
	return ""
}

func looksLikeMilvusAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "collection exists")
}

func milvusString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func milvusFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
