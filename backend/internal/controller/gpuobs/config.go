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

	// TimelineSamplesPerGPU caps in-memory per-GPU timeline points kept for API drilldowns.
	TimelineSamplesPerGPU int `yaml:"timeline_samples_per_gpu" json:"timeline_samples_per_gpu"`

	// TimelineSamplesPerProcess caps in-memory per-process timeline points kept per GPU process.
	TimelineSamplesPerProcess int `yaml:"timeline_samples_per_process" json:"timeline_samples_per_process"`

	// EventBufferPerNode caps in-memory recent GPU events stored per node.
	EventBufferPerNode int `yaml:"event_buffer_per_node" json:"event_buffer_per_node"`

	// RecentEventsInSnapshot controls how many latest events are embedded in /gpu/nodes snapshots.
	RecentEventsInSnapshot int `yaml:"recent_events_in_snapshot" json:"recent_events_in_snapshot"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:                   true,
		PersistDir:                "./data/gpu",
		FlushInterval:             10 * time.Second,
		Retention:                 7 * 24 * time.Hour,
		MaxProcessesPerGPU:        20,
		TimelineSamplesPerGPU:     720,
		TimelineSamplesPerProcess: 360,
		EventBufferPerNode:        1024,
		RecentEventsInSnapshot:    200,
	}
}
