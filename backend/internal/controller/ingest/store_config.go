package ingest

import "time"

const (
	defaultNodeRetention          = 24 * time.Hour
	defaultHistorySamplesPerNode  = 1440
	defaultMaxNodes               = 5000
	defaultPersistencePath        = "./data/controller/ingest/store.db"
	defaultPersistenceSync        = 5 * time.Second
	defaultPersistenceCompaction  = 30 * time.Minute
	defaultPersistenceMaxDBBytes  = 512 * 1024 * 1024
	defaultPersistenceCompactStep = 8 * 1024 * 1024
)

// StoreConfig controls ingest store retention and persistence behavior.
type StoreConfig struct {
	NodeRetention         time.Duration     `yaml:"node_retention" json:"node_retention"`
	HistorySamplesPerNode int               `yaml:"history_samples_per_node" json:"history_samples_per_node"`
	MaxNodes              int               `yaml:"max_nodes" json:"max_nodes"`
	Persistence           PersistenceConfig `yaml:"persistence" json:"persistence"`
}

// PersistenceConfig controls the embedded on-disk store.
type PersistenceConfig struct {
	Enabled            bool          `yaml:"enabled" json:"enabled"`
	Path               string        `yaml:"path" json:"path"`
	SyncInterval       time.Duration `yaml:"sync_interval" json:"sync_interval"`
	CompactionInterval time.Duration `yaml:"compaction_interval" json:"compaction_interval"`
	MaxDBSizeBytes     int64         `yaml:"max_db_size_bytes" json:"max_db_size_bytes"`
	CompactTxMaxSize   int64         `yaml:"compact_tx_max_size" json:"compact_tx_max_size"`
}

// DefaultStoreConfig returns bounded defaults for single-node v0.6 deployments.
func DefaultStoreConfig() StoreConfig {
	return StoreConfig{
		NodeRetention:         defaultNodeRetention,
		HistorySamplesPerNode: defaultHistorySamplesPerNode,
		MaxNodes:              defaultMaxNodes,
		Persistence: PersistenceConfig{
			Enabled:            false,
			Path:               defaultPersistencePath,
			SyncInterval:       defaultPersistenceSync,
			CompactionInterval: defaultPersistenceCompaction,
			MaxDBSizeBytes:     defaultPersistenceMaxDBBytes,
			CompactTxMaxSize:   defaultPersistenceCompactStep,
		},
	}
}

func (cfg StoreConfig) normalized() StoreConfig {
	def := DefaultStoreConfig()
	if cfg.NodeRetention <= 0 {
		cfg.NodeRetention = def.NodeRetention
	}
	if cfg.HistorySamplesPerNode <= 0 {
		cfg.HistorySamplesPerNode = def.HistorySamplesPerNode
	}
	if cfg.MaxNodes <= 0 {
		cfg.MaxNodes = def.MaxNodes
	}

	if cfg.Persistence.Path == "" {
		cfg.Persistence.Path = def.Persistence.Path
	}
	if cfg.Persistence.SyncInterval <= 0 {
		cfg.Persistence.SyncInterval = def.Persistence.SyncInterval
	}
	if cfg.Persistence.CompactionInterval <= 0 {
		cfg.Persistence.CompactionInterval = def.Persistence.CompactionInterval
	}
	if cfg.Persistence.MaxDBSizeBytes <= 0 {
		cfg.Persistence.MaxDBSizeBytes = def.Persistence.MaxDBSizeBytes
	}
	if cfg.Persistence.CompactTxMaxSize <= 0 {
		cfg.Persistence.CompactTxMaxSize = def.Persistence.CompactTxMaxSize
	}
	return cfg
}
