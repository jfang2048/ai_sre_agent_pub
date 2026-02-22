package gpuobs

import "time"

// Config controls GPU observability aggregation and persistence in the controller.
type Config struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	// PersistDir stores the latest per-node snapshot and optional history.
	// Relative paths are resolved against the controller working directory.
	PersistDir string `yaml:"persist_dir" json:"persist_dir"`

	// FlushInterval controls how often snapshots are flushed to disk.
	FlushInterval time.Duration `yaml:"flush_interval" json:"flush_interval"`

	// Retention controls how long history JSONL files are kept (best-effort).
	Retention time.Duration `yaml:"retention" json:"retention"`

	// MaxProcessesPerGPU caps stored per-process GPU records to limit cardinality.
	MaxProcessesPerGPU int `yaml:"max_processes_per_gpu" json:"max_processes_per_gpu"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:            true,
		PersistDir:         "./data/gpu",
		FlushInterval:      10 * time.Second,
		Retention:          7 * 24 * time.Hour,
		MaxProcessesPerGPU: 20,
	}
}
