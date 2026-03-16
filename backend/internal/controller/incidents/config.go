package incidents

import "time"

// Config drives the incident context orchestration pipeline.
// It defines upstream platform endpoints and local fallbacks that are used
// to enrich alerts with correlated evidence before sending them to the Agent.
type Config struct {
	Enabled bool `yaml:"enabled"`

	// How often the coordinator polls for new alerts (analysis engine) when
	// no explicit alert push is received.
	PollInterval time.Duration `yaml:"poll_interval"`

	// Time window padding around the alert start/end.
	WindowBefore time.Duration `yaml:"window_before"`
	WindowAfter  time.Duration `yaml:"window_after"`

	ResourcePlatform   ResourcePlatformConfig   `yaml:"resource_platform"`
	MonitoringPlatform MonitoringPlatformConfig `yaml:"monitoring_platform"`
	LoggingPlatform    LoggingPlatformConfig    `yaml:"logging_platform"`
	Kubernetes         KubernetesConfig         `yaml:"kubernetes"`
}

// ResourcePlatformConfig describes how to reach the resource management platform
// that maps business services to underlying resources.
type ResourcePlatformConfig struct {
	Endpoint    string        `yaml:"endpoint"`
	APITokenEnv string        `yaml:"api_token_env"`
	Timeout     time.Duration `yaml:"timeout"`

	// Optional static mappings provide a quick local fallback and work in tests.
	Static []StaticServiceMapping `yaml:"static"`
}

// MonitoringPlatformConfig describes the metrics/anomaly source.
type MonitoringPlatformConfig struct {
	Endpoint    string        `yaml:"endpoint"`
	APITokenEnv string        `yaml:"api_token_env"`
	Timeout     time.Duration `yaml:"timeout"`
	Step        time.Duration `yaml:"step"`
}

// LoggingPlatformConfig describes the logging platform.
type LoggingPlatformConfig struct {
	Endpoint    string        `yaml:"endpoint"`
	APITokenEnv string        `yaml:"api_token_env"`
	Timeout     time.Duration `yaml:"timeout"`
	DefaultSize int           `yaml:"default_size"`
}

// KubernetesConfig enables cluster/workload enrichment.
type KubernetesConfig struct {
	Enabled    bool          `yaml:"enabled"`
	InCluster  bool          `yaml:"in_cluster"`
	Kubeconfig string        `yaml:"kubeconfig"`
	Namespace  string        `yaml:"namespace"`
	Timeout    time.Duration `yaml:"timeout"`
}

// StaticServiceMapping ties a business service to resources and dependencies.
type StaticServiceMapping struct {
	Service       string        `yaml:"service"`
	Environment   string        `yaml:"environment"`
	Dependencies  []string      `yaml:"dependencies"`
	ResourceScope []ResourceRef `yaml:"resources"`
}

// DefaultConfig returns safe defaults; all platforms are optional and will
// fall back to in-memory/topology/ingest data when endpoints are not provided.
func DefaultConfig() Config {
	return Config{
		Enabled:      true,
		PollInterval: 10 * time.Second,
		WindowBefore: 10 * time.Minute,
		WindowAfter:  20 * time.Minute,
		ResourcePlatform: ResourcePlatformConfig{
			Timeout: 3 * time.Second,
			Static:  []StaticServiceMapping{},
		},
		MonitoringPlatform: MonitoringPlatformConfig{
			Timeout: 5 * time.Second,
			Step:    30 * time.Second,
		},
		LoggingPlatform: LoggingPlatformConfig{
			Timeout:     4 * time.Second,
			DefaultSize: 50,
		},
		Kubernetes: KubernetesConfig{
			Enabled:   false,
			Namespace: "default",
			Timeout:   4 * time.Second,
		},
	}
}
