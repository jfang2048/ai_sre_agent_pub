package rag

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

type vectorSyncRequest struct {
	Collection string
	Generation string
	Chunks     []Chunk
}

type vectorSearchRequest struct {
	Collection string
	Generation string
	Vector     []float32
	Limit      int
}

type vectorBackend interface {
	Name() string
	Sync(context.Context, vectorSyncRequest) error
	SearchScores(context.Context, vectorSearchRequest) (map[string]float64, error)
}

func newVectorBackend(cfg Config, logger *zap.Logger) (vectorBackend, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	switch strings.ToLower(strings.TrimSpace(cfg.VectorBackend)) {
	case "", "local":
		return nil, nil
	case "milvus":
		if strings.TrimSpace(cfg.VectorEndpoint) == "" {
			return nil, fmt.Errorf("rag vector backend %q requires vector_endpoint", cfg.VectorBackend)
		}
		return newMilvusBackend(cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported rag vector backend %q", cfg.VectorBackend)
	}
}
