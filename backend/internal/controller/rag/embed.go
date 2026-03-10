package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

const localEmbeddingDimensions = 64

type embedder interface {
	Provider() string
	Model() string
	Dimension() int
	EmbedDocuments(context.Context, []string) ([][]float32, error)
	EmbedQuery(context.Context, string) ([]float32, error)
}

func newEmbedder(cfg Config, logger *zap.Logger) embedder {
	if logger == nil {
		logger = zap.NewNop()
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.EmbeddingProvider))
	switch provider {
	case "", "local", "deterministic", "stub":
		return localEmbedder{}
	case "openai", "openai-compatible":
		if strings.TrimSpace(cfg.EmbeddingAPIKey) == "" || strings.TrimSpace(cfg.EmbeddingBaseURL) == "" {
			logger.Warn("rag external embeddings not fully configured; using deterministic local embeddings",
				zap.String("provider", provider))
			return localEmbedder{}
		}
		return &openAIEmbedder{
			baseURL: strings.TrimRight(strings.TrimSpace(cfg.EmbeddingBaseURL), "/"),
			apiKey:  strings.TrimSpace(cfg.EmbeddingAPIKey),
			model:   strings.TrimSpace(cfg.EmbeddingModel),
			http:    &http.Client{Timeout: 20 * time.Second},
		}
	default:
		logger.Warn("rag embedding provider unsupported; using deterministic local embeddings",
			zap.String("provider", provider))
		return localEmbedder{}
	}
}

type localEmbedder struct{}

func (localEmbedder) Provider() string { return "local" }
func (localEmbedder) Model() string    { return "local-hash-64" }
func (localEmbedder) Dimension() int   { return localEmbeddingDimensions }
func (e localEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return e.embedText(text), nil
}

func (e localEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, 0, len(texts))
	for _, text := range texts {
		embeddings = append(embeddings, e.embedText(text))
	}
	return embeddings, nil
}

func (localEmbedder) embedText(text string) []float32 {
	vector := make([]float32, localEmbeddingDimensions)
	for _, token := range tokenize(text) {
		if token == "" {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(token))
		sum := h.Sum64()
		index := int(sum % uint64(localEmbeddingDimensions))
		sign := float32(1.0)
		if (sum>>63)&1 == 1 {
			sign = -1.0
		}
		vector[index] += sign
	}
	return normalizeVector(vector)
}

type openAIEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func (e *openAIEmbedder) Provider() string {
	return "openai"
}

func (e *openAIEmbedder) Model() string {
	if strings.TrimSpace(e.model) == "" {
		return "text-embedding-3-small"
	}
	return e.model
}

func (e *openAIEmbedder) Dimension() int {
	return 0
}

func (e *openAIEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	results, err := e.EmbedDocuments(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("embedding provider returned no vectors")
	}
	return results[0], nil
}

func (e *openAIEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body := map[string]any{
		"model": e.Model(),
		"input": texts,
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(rawBody))
	if err != nil {
		return nil, fmt.Errorf("build embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute embedding request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("embedding request failed: status=%d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(payload.Data) != len(texts) {
		return nil, fmt.Errorf("embedding response length mismatch: want=%d got=%d", len(texts), len(payload.Data))
	}
	vectors := make([][]float32, len(payload.Data))
	for _, item := range payload.Data {
		if item.Index < 0 || item.Index >= len(vectors) {
			continue
		}
		vector := make([]float32, 0, len(item.Embedding))
		for _, value := range item.Embedding {
			vector = append(vector, float32(value))
		}
		vectors[item.Index] = normalizeVector(vector)
	}
	return vectors, nil
}

func normalizeVector(in []float32) []float32 {
	if len(in) == 0 {
		return in
	}
	sumSquares := 0.0
	for _, value := range in {
		sumSquares += float64(value * value)
	}
	if sumSquares == 0 {
		return in
	}
	scale := float32(1.0 / math.Sqrt(sumSquares))
	out := make([]float32, len(in))
	for i, value := range in {
		out[i] = value * scale
	}
	return out
}

func dotProduct(left, right []float32) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	total := 0.0
	for i := 0; i < limit; i++ {
		total += float64(left[i] * right[i])
	}
	return total
}
