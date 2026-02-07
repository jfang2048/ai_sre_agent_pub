package collector

import (
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config configures the push-first collector.
type Config struct {
	CollectorID            string            `yaml:"collector_id" json:"collector_id"`
	Hostname               string            `yaml:"hostname" json:"hostname"`
	Version                string            `yaml:"version" json:"version"`
	Labels                 map[string]string `yaml:"labels" json:"labels"`
	CollectionInterval     time.Duration     `yaml:"collection_interval" json:"collection_interval"`
	ControllerEndpoints    []string          `yaml:"controller_endpoints" json:"controller_endpoints"`
	MirrorSend             bool              `yaml:"mirror_send" json:"mirror_send"`
	SpoolDir               string            `yaml:"spool_dir" json:"spool_dir"`
	SpoolMaxBytes          int64             `yaml:"spool_max_bytes" json:"spool_max_bytes"`
	TopK                   int               `yaml:"topk" json:"topk"`
	LogPaths               []string          `yaml:"log_paths" json:"log_paths"`
	ShmEnabled             bool              `yaml:"shm_enabled" json:"shm_enabled"`
	ShmName                string            `yaml:"shm_name" json:"shm_name"`
	GrpcCompress           bool              `yaml:"grpc_compress" json:"grpc_compress"`
	Level                  int               `yaml:"level" json:"level"`
	ExternalMetricsCmd     string            `yaml:"external_metrics_cmd" json:"external_metrics_cmd"`
	ExternalMetricsTimeout time.Duration     `yaml:"external_metrics_timeout" json:"external_metrics_timeout"`
	EBPF                   EBPFConfig        `yaml:"ebpf" json:"ebpf"`
}

const (
	defaultInterval = 10 * time.Second
	defaultSpoolDir = "./data/collector/spool"
	defaultSpoolMax = int64(128 * 1024 * 1024)
	defaultTopK     = 10
	defaultShmName  = "/sre_collector_metrics"
	defaultEBPFSock = "/var/run/sre_collector_ebpf.sock"
)

// EBPFConfig mirrors probe.EBPFConfig but avoids an import cycle.
type EBPFConfig struct {
	Enabled     bool     `yaml:"enabled" json:"enabled"`
	SocketPath  string   `yaml:"socket_path" json:"socket_path"`
	Categories  []string `yaml:"categories" json:"categories"`
	MaxMsgBytes int      `yaml:"max_msg_bytes" json:"max_msg_bytes"`
}

// DefaultConfig provides baseline configuration values.
func DefaultConfig() Config {
	return Config{
		CollectionInterval:     defaultInterval,
		SpoolDir:               defaultSpoolDir,
		SpoolMaxBytes:          defaultSpoolMax,
		TopK:                   defaultTopK,
		ShmName:                defaultShmName,
		Level:                  2,
		ExternalMetricsTimeout: 500 * time.Millisecond,
		EBPF: EBPFConfig{
			Enabled:     false,
			SocketPath:  defaultEBPFSock,
			Categories:  []string{"sched", "io", "net", "mem", "gpu", "security", "syscall"},
			MaxMsgBytes: 65536,
		},
	}
}

// LoadConfig builds a Config using defaults, then optional file, then env overrides.
func LoadConfig(configPath string) Config {
	cfg := DefaultConfig()

	if configPath == "" {
		if envPath := os.Getenv("SRE_COLLECTOR_CONFIG"); envPath != "" {
			configPath = envPath
		} else if fileExists("./configs/collector.yaml") {
			configPath = "./configs/collector.yaml"
		} else if fileExists("/etc/sre-collector/config.yaml") {
			configPath = "/etc/sre-collector/config.yaml"
		}
	}

	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			_ = yaml.Unmarshal(data, &cfg)
		}
	}

	cfg = applyEnvOverrides(cfg)
	return cfg
}

// applyEnvOverrides overlays environment variables on top of the provided config.
func applyEnvOverrides(cfg Config) Config {

	if env, ok := os.LookupEnv("SRE_COLLECTOR_ID"); ok && env != "" {
		cfg.CollectorID = env
	}
	if env, ok := os.LookupEnv("SRE_COLLECTOR_HOSTNAME"); ok && env != "" {
		cfg.Hostname = env
	}
	if env, ok := os.LookupEnv("SRE_COLLECTOR_VERSION"); ok && env != "" {
		cfg.Version = env
	}

	if raw := os.Getenv("SRE_COLLECTOR_COLLECTION_INTERVAL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			cfg.CollectionInterval = parsed
		}
	}

	if raw := os.Getenv("SRE_COLLECTOR_CONTROLLER_ENDPOINTS"); raw != "" {
		cfg.ControllerEndpoints = splitCSV(raw)
	}

	if raw := os.Getenv("SRE_COLLECTOR_MIRROR_SEND"); raw == "1" || strings.EqualFold(raw, "true") {
		cfg.MirrorSend = true
	}

	if raw := os.Getenv("SRE_COLLECTOR_SPOOL_DIR"); raw != "" {
		cfg.SpoolDir = raw
	}

	if raw := os.Getenv("SRE_COLLECTOR_SPOOL_MAX_BYTES"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			cfg.SpoolMaxBytes = parsed
		}
	}

	if raw := os.Getenv("SRE_COLLECTOR_TOPK"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			cfg.TopK = parsed
		}
	}

	if raw := os.Getenv("SRE_COLLECTOR_LOG_PATHS"); raw != "" {
		cfg.LogPaths = splitCSV(raw)
	}

	if raw := os.Getenv("SRE_COLLECTOR_SHM_ENABLED"); raw == "1" || strings.EqualFold(raw, "true") {
		cfg.ShmEnabled = true
	}

	if raw := os.Getenv("SRE_COLLECTOR_SHM_NAME"); raw != "" {
		cfg.ShmName = raw
	}

	if raw := os.Getenv("SRE_COLLECTOR_GRPC_COMPRESS"); raw == "1" || strings.EqualFold(raw, "true") {
		cfg.GrpcCompress = true
	}

	if raw := os.Getenv("SRE_COLLECTOR_LEVEL"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			cfg.Level = parsed
		}
	}

	if raw := os.Getenv("SRE_COLLECTOR_EXT_METRICS_CMD"); raw != "" {
		cfg.ExternalMetricsCmd = raw
	}
	if raw := os.Getenv("SRE_COLLECTOR_EXT_METRICS_TIMEOUT"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			cfg.ExternalMetricsTimeout = parsed
		}
	}

	if raw := os.Getenv("SRE_COLLECTOR_EBPF_ENABLED"); raw == "1" || strings.EqualFold(raw, "true") {
		cfg.EBPF.Enabled = true
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_SOCKET_PATH"); raw != "" {
		cfg.EBPF.SocketPath = raw
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_CATEGORIES"); raw != "" {
		cfg.EBPF.Categories = splitCSV(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_MAX_MSG_BYTES"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			cfg.EBPF.MaxMsgBytes = v
		}
	}

	return cfg
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}
