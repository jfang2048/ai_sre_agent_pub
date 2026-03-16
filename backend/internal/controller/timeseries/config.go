package timeseries

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultProvider       = "influxdb"
	defaultURL            = "http://127.0.0.1:8086"
	defaultOrg            = "ai-sre-agent"
	defaultBucket         = "controller_metrics"
	defaultMeasurement    = "telemetry_metric"
	defaultRetention      = 7 * 24 * time.Hour
	defaultWriteBatchSize = 512
	defaultWriteQueueSize = 256
	defaultFlushInterval  = 2 * time.Second
	defaultQueryTimeout   = 5 * time.Second
	defaultHealthInterval = 30 * time.Second
)

// Config controls durable controller-side metric history storage.
type Config struct {
	Enabled          bool          `yaml:"enabled" json:"enabled"`
	Provider         string        `yaml:"provider" json:"provider"`
	URL              string        `yaml:"url" json:"url"`
	Org              string        `yaml:"org" json:"org"`
	Bucket           string        `yaml:"bucket" json:"bucket"`
	Token            string        `yaml:"token,omitempty" json:"token,omitempty"`
	Measurement      string        `yaml:"measurement" json:"measurement"`
	Retention        time.Duration `yaml:"retention" json:"retention"`
	WriteBatchSize   int           `yaml:"write_batch_size" json:"write_batch_size"`
	WriteQueueSize   int           `yaml:"write_queue_size" json:"write_queue_size"`
	FlushInterval    time.Duration `yaml:"flush_interval" json:"flush_interval"`
	QueryTimeout     time.Duration `yaml:"query_timeout" json:"query_timeout"`
	HealthInterval   time.Duration `yaml:"health_interval" json:"health_interval"`
	FallbackToMemory bool          `yaml:"fallback_to_memory" json:"fallback_to_memory"`
	ManageBucket     bool          `yaml:"manage_bucket" json:"manage_bucket"`
	BackupDirectory  string        `yaml:"backup_directory" json:"backup_directory"`
}

// Status reports runtime timeseries state and recent errors.
type Status struct {
	Enabled          bool      `json:"enabled"`
	Provider         string    `json:"provider"`
	Mode             string    `json:"mode"`
	Ready            bool      `json:"ready"`
	Healthy          bool      `json:"healthy"`
	FallbackToMemory bool      `json:"fallback_to_memory"`
	FallbackActive   bool      `json:"fallback_active"`
	ManageBucket     bool      `json:"manage_bucket"`
	Endpoint         string    `json:"endpoint,omitempty"`
	Org              string    `json:"org,omitempty"`
	Bucket           string    `json:"bucket,omitempty"`
	Measurement      string    `json:"measurement,omitempty"`
	Retention        string    `json:"retention,omitempty"`
	WriteBatchSize   int       `json:"write_batch_size,omitempty"`
	WriteQueueSize   int       `json:"write_queue_size,omitempty"`
	QueueDepth       int       `json:"queue_depth,omitempty"`
	FlushInterval    string    `json:"flush_interval,omitempty"`
	QueryTimeout     string    `json:"query_timeout,omitempty"`
	HealthInterval   string    `json:"health_interval,omitempty"`
	BackupDirectory  string    `json:"backup_directory,omitempty"`
	DroppedBatches   uint64    `json:"dropped_batches,omitempty"`
	LastWriteAt      time.Time `json:"last_write_at,omitempty"`
	LastWriteError   string    `json:"last_write_error,omitempty"`
	LastQueryAt      time.Time `json:"last_query_at,omitempty"`
	LastQueryError   string    `json:"last_query_error,omitempty"`
	LastHealthAt     time.Time `json:"last_health_at,omitempty"`
	LastHealthError  string    `json:"last_health_error,omitempty"`
	DegradedReason   string    `json:"degraded_reason,omitempty"`
}

// DefaultConfig returns conservative controller-side TSDB defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:          false,
		Provider:         defaultProvider,
		URL:              defaultURL,
		Org:              defaultOrg,
		Bucket:           defaultBucket,
		Measurement:      defaultMeasurement,
		Retention:        defaultRetention,
		WriteBatchSize:   defaultWriteBatchSize,
		WriteQueueSize:   defaultWriteQueueSize,
		FlushInterval:    defaultFlushInterval,
		QueryTimeout:     defaultQueryTimeout,
		HealthInterval:   defaultHealthInterval,
		FallbackToMemory: true,
		ManageBucket:     false,
	}
}

func normalizeConfig(cfg Config) Config {
	def := DefaultConfig()
	cfg.Provider = normalizeOption(cfg.Provider, def.Provider, defaultProvider)
	if strings.TrimSpace(cfg.URL) == "" {
		cfg.URL = def.URL
	}
	if strings.TrimSpace(cfg.Org) == "" {
		cfg.Org = def.Org
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		cfg.Bucket = def.Bucket
	}
	if strings.TrimSpace(cfg.Measurement) == "" {
		cfg.Measurement = def.Measurement
	}
	if cfg.Retention < 0 {
		cfg.Retention = def.Retention
	}
	if cfg.WriteBatchSize <= 0 {
		cfg.WriteBatchSize = def.WriteBatchSize
	}
	if cfg.WriteQueueSize <= 0 {
		cfg.WriteQueueSize = def.WriteQueueSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = def.FlushInterval
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = def.QueryTimeout
	}
	if cfg.HealthInterval <= 0 {
		cfg.HealthInterval = def.HealthInterval
	}
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.Org = strings.TrimSpace(cfg.Org)
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.Measurement = strings.TrimSpace(cfg.Measurement)
	cfg.BackupDirectory = strings.TrimSpace(cfg.BackupDirectory)
	return cfg
}

// ConfigFromEnv applies environment overrides without requiring the controller config loader.
func ConfigFromEnv(base Config) Config {
	cfg := normalizeConfig(base)
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_ENABLED")); raw != "" {
		cfg.Enabled = parseBool(raw, cfg.Enabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_PROVIDER")); raw != "" {
		cfg.Provider = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_URL")); raw != "" {
		cfg.URL = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_ORG")); raw != "" {
		cfg.Org = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_BUCKET")); raw != "" {
		cfg.Bucket = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_TOKEN")); raw != "" {
		cfg.Token = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_MEASUREMENT")); raw != "" {
		cfg.Measurement = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_RETENTION")); raw != "" {
		cfg.Retention = parseDuration(raw, cfg.Retention)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_WRITE_BATCH_SIZE")); raw != "" {
		cfg.WriteBatchSize = parseInt(raw, cfg.WriteBatchSize)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_WRITE_QUEUE_SIZE")); raw != "" {
		cfg.WriteQueueSize = parseInt(raw, cfg.WriteQueueSize)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_FLUSH_INTERVAL")); raw != "" {
		cfg.FlushInterval = parseDuration(raw, cfg.FlushInterval)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_QUERY_TIMEOUT")); raw != "" {
		cfg.QueryTimeout = parseDuration(raw, cfg.QueryTimeout)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_HEALTH_INTERVAL")); raw != "" {
		cfg.HealthInterval = parseDuration(raw, cfg.HealthInterval)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_FALLBACK_TO_MEMORY")); raw != "" {
		cfg.FallbackToMemory = parseBool(raw, cfg.FallbackToMemory)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_MANAGE_BUCKET")); raw != "" {
		cfg.ManageBucket = parseBool(raw, cfg.ManageBucket)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_TSDB_BACKUP_DIRECTORY")); raw != "" {
		cfg.BackupDirectory = raw
	}
	return normalizeConfig(cfg)
}

func normalizeOption(value, fallback string, allowed ...string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, option := range allowed {
		if normalized == option {
			return normalized
		}
	}
	return fallback
}

func parseBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseInt(raw string, fallback int) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
