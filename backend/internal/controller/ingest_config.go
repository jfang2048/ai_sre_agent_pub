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
	Transport             IngestTransportConfig    `yaml:"transport"`
}

type IngestTransportConfig struct {
	AllowPlaintext      bool            `yaml:"allow_plaintext" json:"allow_plaintext"`
	TLS                 IngestTLSConfig `yaml:"tls" json:"tls"`
	AllowedCollectorIDs []string        `yaml:"allowed_collector_ids" json:"allowed_collector_ids"`
	AllowedClusterNames []string        `yaml:"allowed_cluster_names" json:"allowed_cluster_names"`
	AllowedPeerSubjects []string        `yaml:"allowed_peer_subjects" json:"allowed_peer_subjects"`
}

type IngestTLSConfig struct {
	Enabled           bool   `yaml:"enabled" json:"enabled"`
	RequireClientCert bool   `yaml:"require_client_cert" json:"require_client_cert"`
	CAFile            string `yaml:"ca_file" json:"ca_file"`
	CertFile          string `yaml:"cert_file" json:"cert_file"`
	KeyFile           string `yaml:"key_file" json:"key_file"`
}

// DefaultIngestConfig returns bounded single-node defaults.
func DefaultIngestConfig() IngestConfig {
	cfg := ingest.DefaultStoreConfig()
	return IngestConfig{
		NodeRetention:         cfg.NodeRetention,
		HistorySamplesPerNode: cfg.HistorySamplesPerNode,
		MaxNodes:              cfg.MaxNodes,
		Persistence:           cfg.Persistence,
		Transport: IngestTransportConfig{
			AllowPlaintext: true,
		},
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
