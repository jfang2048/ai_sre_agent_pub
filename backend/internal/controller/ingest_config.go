package controller

import (
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

// IngestConfig controls ingest-store retention and persistence bounds.
type IngestConfig struct {
	NodeRetention         time.Duration            `yaml:"node_retention"`
	HistorySamplesPerNode int                      `yaml:"history_samples_per_node"`
	MaxNodes              int                      `yaml:"max_nodes"`
	Persistence           ingest.PersistenceConfig `yaml:"persistence"`
}

// DefaultIngestConfig returns bounded single-node defaults.
func DefaultIngestConfig() IngestConfig {
	cfg := ingest.DefaultStoreConfig()
	return IngestConfig{
		NodeRetention:         cfg.NodeRetention,
		HistorySamplesPerNode: cfg.HistorySamplesPerNode,
		MaxNodes:              cfg.MaxNodes,
		Persistence:           cfg.Persistence,
	}
}

func (cfg IngestConfig) storeConfig() ingest.StoreConfig {
	return ingest.StoreConfig{
		NodeRetention:         cfg.NodeRetention,
		HistorySamplesPerNode: cfg.HistorySamplesPerNode,
		MaxNodes:              cfg.MaxNodes,
		Persistence:           cfg.Persistence,
	}
}
