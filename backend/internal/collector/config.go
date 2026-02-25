package collector

import (
	"fmt"
	"net"
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
	AdaptivePolling        bool              `yaml:"adaptive_polling" json:"adaptive_polling"`
	MinCollectionInterval  time.Duration     `yaml:"min_collection_interval" json:"min_collection_interval"`
	MaxCollectionInterval  time.Duration     `yaml:"max_collection_interval" json:"max_collection_interval"`
	MetricsListenAddress   string            `yaml:"metrics_listen_address" json:"metrics_listen_address"`
	TracingJaegerEndpoint  string            `yaml:"tracing_jaeger_endpoint" json:"tracing_jaeger_endpoint"`
	Transport              TransportConfig   `yaml:"transport" json:"transport"`
	EBPF                   EBPFConfig        `yaml:"ebpf" json:"ebpf"`
	ProbeCore              ProbeCoreConfig   `yaml:"probe_core" json:"probe_core"`
}

const (
	defaultInterval               = 10 * time.Second
	defaultSpoolDir               = "./data/collector/spool"
	defaultSpoolMax               = int64(128 * 1024 * 1024)
	defaultTopK                   = 10
	defaultShmName                = "/sre_collector_metrics"
	defaultEBPFSock               = "/var/run/sre_collector_ebpf.sock"
	defaultProbeCoreBinaryPath    = "./build/sre-probe-core"
	defaultProbeCoreCompression   = "none"
	defaultProbeCoreFrameMaxBytes = 8 * 1024 * 1024
)

// TransportConfig defines gRPC transport runtime knobs.
type TransportConfig struct {
	DialTimeout time.Duration `yaml:"dial_timeout" json:"dial_timeout"`
	RPCTimeout  time.Duration `yaml:"rpc_timeout" json:"rpc_timeout"`
	TLS         TLSConfig     `yaml:"tls" json:"tls"`
}

// TLSConfig controls mTLS/client-auth behavior for collector -> controller traffic.
type TLSConfig struct {
	Enabled            bool          `yaml:"enabled" json:"enabled"`
	CAFile             string        `yaml:"ca_file" json:"ca_file"`
	CertFile           string        `yaml:"cert_file" json:"cert_file"`
	KeyFile            string        `yaml:"key_file" json:"key_file"`
	ServerName         string        `yaml:"server_name" json:"server_name"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
	ReloadInterval     time.Duration `yaml:"reload_interval" json:"reload_interval"`
}

// EBPFConfig mirrors probe.EBPFConfig but avoids an import cycle.
type EBPFConfig struct {
	Enabled     bool     `yaml:"enabled" json:"enabled"`
	SocketPath  string   `yaml:"socket_path" json:"socket_path"`
	Categories  []string `yaml:"categories" json:"categories"`
	MaxMsgBytes int      `yaml:"max_msg_bytes" json:"max_msg_bytes"`
}

// ProbeCoreConfig controls the C++ probe-core IPC runtime.
type ProbeCoreConfig struct {
	Enabled            bool          `yaml:"enabled" json:"enabled"`
	BinaryPath         string        `yaml:"binary_path" json:"binary_path"`
	Collectors         []string      `yaml:"collectors" json:"collectors"`
	Args               []string      `yaml:"args" json:"args"`
	Compression        string        `yaml:"compression" json:"compression"`
	QueueDepth         int           `yaml:"queue_depth" json:"queue_depth"`
	WindowSamples      int           `yaml:"window_samples" json:"window_samples"`
	GPUIntervalSamples int           `yaml:"gpu_interval_samples" json:"gpu_interval_samples"`
	StartupTimeout     time.Duration `yaml:"startup_timeout" json:"startup_timeout"`
	StaleAfter         time.Duration `yaml:"stale_after" json:"stale_after"`
	FrameMaxBytes      int           `yaml:"frame_max_bytes" json:"frame_max_bytes"`
	FallbackToGo       bool          `yaml:"fallback_to_go" json:"fallback_to_go"`
}

// DefaultConfig provides baseline configuration values.
func DefaultConfig() Config {
	return Config{
		CollectionInterval:     defaultInterval,
		ControllerEndpoints:    []string{"localhost:9090"},
		SpoolDir:               defaultSpoolDir,
		SpoolMaxBytes:          defaultSpoolMax,
		TopK:                   defaultTopK,
		ShmName:                defaultShmName,
		Level:                  2,
		ExternalMetricsTimeout: 500 * time.Millisecond,
		AdaptivePolling:        true,
		MinCollectionInterval:  2 * time.Second,
		MaxCollectionInterval:  30 * time.Second,
		Transport: TransportConfig{
			DialTimeout: 10 * time.Second,
			RPCTimeout:  10 * time.Second,
			TLS: TLSConfig{
				ReloadInterval: 30 * time.Second,
			},
		},
		EBPF: EBPFConfig{
			Enabled:     false,
			SocketPath:  defaultEBPFSock,
			Categories:  []string{"sched", "io", "net", "mem", "gpu", "security", "syscall"},
			MaxMsgBytes: 65536,
		},
		ProbeCore: ProbeCoreConfig{
			Enabled:            false,
			BinaryPath:         defaultProbeCoreBinaryPath,
			Collectors:         nil,
			Compression:        defaultProbeCoreCompression,
			QueueDepth:         16,
			WindowSamples:      6,
			GPUIntervalSamples: 5,
			StartupTimeout:     3 * time.Second,
			StaleAfter:         15 * time.Second,
			FrameMaxBytes:      defaultProbeCoreFrameMaxBytes,
			FallbackToGo:       true,
		},
	}
}

// LoadConfig builds a Config using defaults, then optional file, then env overrides.
// Think of this as a three-layer rain jacket: defaults are the inner lining, file config
// is the shell, and env vars are the emergency outer layer used during incidents.
func LoadConfig(configPath string) (Config, error) {
	cfg := DefaultConfig()
	resolvedPath := resolveConfigPath(configPath)

	if resolvedPath != "" {
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			return cfg, fmt.Errorf("read collector config %q: %w", resolvedPath, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse collector config %q: %w", resolvedPath, err)
		}
	}

	var err error
	cfg, err = applyEnvOverrides(cfg)
	if err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// resolveConfigPath chooses the config source by priority:
// explicit flag, env var, local file, then system path.
func resolveConfigPath(configPath string) string {
	if strings.TrimSpace(configPath) != "" {
		return configPath
	}
	if envPath := os.Getenv("SRE_COLLECTOR_CONFIG"); envPath != "" {
		return envPath
	}
	if fileExists("./configs/collector.yaml") {
		return "./configs/collector.yaml"
	}
	if fileExists("/etc/sre-collector/config.yaml") {
		return "/etc/sre-collector/config.yaml"
	}
	return ""
}

// Validate checks invariants early so runtime loops can stay simple.
func (cfg Config) Validate() error {
	if cfg.CollectionInterval <= 0 {
		return fmt.Errorf("collection_interval must be > 0")
	}
	if cfg.MinCollectionInterval <= 0 {
		return fmt.Errorf("min_collection_interval must be > 0")
	}
	if cfg.MaxCollectionInterval < cfg.MinCollectionInterval {
		return fmt.Errorf("max_collection_interval must be >= min_collection_interval")
	}
	if cfg.SpoolDir == "" {
		return fmt.Errorf("spool_dir is required")
	}
	if cfg.SpoolMaxBytes <= 0 {
		return fmt.Errorf("spool_max_bytes must be > 0")
	}
	if cfg.TopK <= 0 {
		return fmt.Errorf("topk must be > 0")
	}
	if cfg.Level < 1 || cfg.Level > 5 {
		return fmt.Errorf("level must be between 1 and 5")
	}
	if cfg.ExternalMetricsTimeout <= 0 {
		return fmt.Errorf("external_metrics_timeout must be > 0")
	}
	if strings.TrimSpace(cfg.ExternalMetricsCmd) != "" {
		if _, _, err := parseExternalMetricCommand(cfg.ExternalMetricsCmd); err != nil {
			return fmt.Errorf("external_metrics_cmd: %w", err)
		}
	}
	if len(cfg.ControllerEndpoints) == 0 {
		return fmt.Errorf("at least one controller endpoint is required")
	}
	for _, endpoint := range cfg.ControllerEndpoints {
		if strings.TrimSpace(endpoint) == "" {
			return fmt.Errorf("controller endpoint cannot be empty")
		}
		host, _, err := net.SplitHostPort(endpoint)
		if err != nil || strings.TrimSpace(host) == "" {
			return fmt.Errorf("invalid controller endpoint %q: expected host:port", endpoint)
		}
	}
	if cfg.Transport.DialTimeout <= 0 {
		return fmt.Errorf("transport.dial_timeout must be > 0")
	}
	if cfg.Transport.RPCTimeout <= 0 {
		return fmt.Errorf("transport.rpc_timeout must be > 0")
	}
	if cfg.Transport.TLS.Enabled {
		if cfg.Transport.TLS.CAFile == "" && !cfg.Transport.TLS.InsecureSkipVerify {
			return fmt.Errorf("transport.tls.ca_file is required unless insecure_skip_verify is true")
		}
		if (cfg.Transport.TLS.CertFile == "") != (cfg.Transport.TLS.KeyFile == "") {
			return fmt.Errorf("transport.tls.cert_file and transport.tls.key_file must be set together")
		}
		if cfg.Transport.TLS.ReloadInterval <= 0 {
			return fmt.Errorf("transport.tls.reload_interval must be > 0")
		}
	}
	if cfg.ProbeCore.Enabled {
		if strings.TrimSpace(cfg.ProbeCore.BinaryPath) == "" {
			return fmt.Errorf("probe_core.binary_path is required when probe_core.enabled=true")
		}
		switch strings.TrimSpace(strings.ToLower(cfg.ProbeCore.Compression)) {
		case "none", "gzip":
		default:
			return fmt.Errorf("probe_core.compression must be one of: none, gzip")
		}
		if cfg.ProbeCore.QueueDepth <= 0 {
			return fmt.Errorf("probe_core.queue_depth must be > 0")
		}
		if cfg.ProbeCore.WindowSamples <= 0 {
			return fmt.Errorf("probe_core.window_samples must be > 0")
		}
		if cfg.ProbeCore.GPUIntervalSamples <= 0 {
			return fmt.Errorf("probe_core.gpu_interval_samples must be > 0")
		}
		if cfg.ProbeCore.StartupTimeout <= 0 {
			return fmt.Errorf("probe_core.startup_timeout must be > 0")
		}
		if cfg.ProbeCore.StaleAfter <= 0 {
			return fmt.Errorf("probe_core.stale_after must be > 0")
		}
		if cfg.ProbeCore.FrameMaxBytes <= 0 {
			return fmt.Errorf("probe_core.frame_max_bytes must be > 0")
		}
	}
	for _, module := range cfg.ProbeCore.Collectors {
		normalized := strings.TrimSpace(strings.ToLower(module))
		if normalized == "" {
			continue
		}
		if !isValidProbeCoreCollectorModule(normalized) {
			return fmt.Errorf("probe_core.collectors contains unsupported module %q", module)
		}
	}
	if len(cfg.ProbeCore.Collectors) > 0 && probeCoreArgsContainCollectorsFlag(cfg.ProbeCore.Args) {
		return fmt.Errorf("probe_core.collectors conflicts with probe_core.args (--collectors); use only one")
	}
	return nil
}

// applyEnvOverrides overlays environment variables on top of the provided config.
func applyEnvOverrides(cfg Config) (Config, error) {

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
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_COLLECTION_INTERVAL: %w", err)
		}
		cfg.CollectionInterval = parsed
	}

	if raw := os.Getenv("SRE_COLLECTOR_CONTROLLER_ENDPOINTS"); raw != "" {
		cfg.ControllerEndpoints = splitCSV(raw)
	}

	if raw, ok := os.LookupEnv("SRE_COLLECTOR_MIRROR_SEND"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_MIRROR_SEND: %w", err)
		}
		cfg.MirrorSend = parsed
	}

	if raw := os.Getenv("SRE_COLLECTOR_SPOOL_DIR"); raw != "" {
		cfg.SpoolDir = raw
	}

	if raw := os.Getenv("SRE_COLLECTOR_SPOOL_MAX_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SPOOL_MAX_BYTES: %w", err)
		}
		cfg.SpoolMaxBytes = parsed
	}

	if raw := os.Getenv("SRE_COLLECTOR_TOPK"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_TOPK: %w", err)
		}
		cfg.TopK = parsed
	}

	if raw := os.Getenv("SRE_COLLECTOR_LOG_PATHS"); raw != "" {
		cfg.LogPaths = splitCSV(raw)
	}

	if raw, ok := os.LookupEnv("SRE_COLLECTOR_SHM_ENABLED"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SHM_ENABLED: %w", err)
		}
		cfg.ShmEnabled = parsed
	}

	if raw := os.Getenv("SRE_COLLECTOR_SHM_NAME"); raw != "" {
		cfg.ShmName = raw
	}

	if raw, ok := os.LookupEnv("SRE_COLLECTOR_GRPC_COMPRESS"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_GRPC_COMPRESS: %w", err)
		}
		cfg.GrpcCompress = parsed
	}

	if raw := os.Getenv("SRE_COLLECTOR_LEVEL"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_LEVEL: %w", err)
		}
		cfg.Level = parsed
	}

	if raw := os.Getenv("SRE_COLLECTOR_EXT_METRICS_CMD"); raw != "" {
		cfg.ExternalMetricsCmd = raw
	}
	if raw := os.Getenv("SRE_COLLECTOR_EXT_METRICS_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_EXT_METRICS_TIMEOUT: %w", err)
		}
		cfg.ExternalMetricsTimeout = parsed
	}

	if raw, ok := os.LookupEnv("SRE_COLLECTOR_ADAPTIVE_POLLING"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_ADAPTIVE_POLLING: %w", err)
		}
		cfg.AdaptivePolling = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_MIN_COLLECTION_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_MIN_COLLECTION_INTERVAL: %w", err)
		}
		cfg.MinCollectionInterval = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_MAX_COLLECTION_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_MAX_COLLECTION_INTERVAL: %w", err)
		}
		cfg.MaxCollectionInterval = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_METRICS_ADDR"); raw != "" {
		cfg.MetricsListenAddress = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_JAEGER_ENDPOINT"); raw != "" {
		cfg.TracingJaegerEndpoint = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_GRPC_DIAL_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_GRPC_DIAL_TIMEOUT: %w", err)
		}
		cfg.Transport.DialTimeout = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_GRPC_RPC_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_GRPC_RPC_TIMEOUT: %w", err)
		}
		cfg.Transport.RPCTimeout = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_TLS_ENABLED"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_TLS_ENABLED: %w", err)
		}
		cfg.Transport.TLS.Enabled = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_TLS_CA_FILE"); raw != "" {
		cfg.Transport.TLS.CAFile = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_TLS_CERT_FILE"); raw != "" {
		cfg.Transport.TLS.CertFile = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_TLS_KEY_FILE"); raw != "" {
		cfg.Transport.TLS.KeyFile = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_TLS_SERVER_NAME"); raw != "" {
		cfg.Transport.TLS.ServerName = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_TLS_INSECURE_SKIP_VERIFY"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_TLS_INSECURE_SKIP_VERIFY: %w", err)
		}
		cfg.Transport.TLS.InsecureSkipVerify = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_TLS_RELOAD_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_TLS_RELOAD_INTERVAL: %w", err)
		}
		cfg.Transport.TLS.ReloadInterval = parsed
	}

	if raw, ok := os.LookupEnv("SRE_COLLECTOR_EBPF_ENABLED"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_EBPF_ENABLED: %w", err)
		}
		cfg.EBPF.Enabled = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_SOCKET_PATH"); raw != "" {
		cfg.EBPF.SocketPath = raw
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_CATEGORIES"); raw != "" {
		cfg.EBPF.Categories = splitCSV(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_MAX_MSG_BYTES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_EBPF_MAX_MSG_BYTES: %w", err)
		}
		cfg.EBPF.MaxMsgBytes = v
	}

	if raw, ok := os.LookupEnv("SRE_COLLECTOR_PROBE_CORE_ENABLED"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_ENABLED: %w", err)
		}
		cfg.ProbeCore.Enabled = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_BINARY_PATH"); raw != "" {
		cfg.ProbeCore.BinaryPath = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_COLLECTORS"); raw != "" {
		cfg.ProbeCore.Collectors = splitCSV(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_ARGS"); raw != "" {
		cfg.ProbeCore.Args = splitCSV(raw)
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_COMPRESSION"); raw != "" {
		cfg.ProbeCore.Compression = strings.TrimSpace(strings.ToLower(raw))
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_QUEUE_DEPTH"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_QUEUE_DEPTH: %w", err)
		}
		cfg.ProbeCore.QueueDepth = v
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_WINDOW_SAMPLES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_WINDOW_SAMPLES: %w", err)
		}
		cfg.ProbeCore.WindowSamples = v
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_GPU_INTERVAL_SAMPLES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_GPU_INTERVAL_SAMPLES: %w", err)
		}
		cfg.ProbeCore.GPUIntervalSamples = v
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_STARTUP_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_STARTUP_TIMEOUT: %w", err)
		}
		cfg.ProbeCore.StartupTimeout = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_STALE_AFTER"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_STALE_AFTER: %w", err)
		}
		cfg.ProbeCore.StaleAfter = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_FRAME_MAX_BYTES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_FRAME_MAX_BYTES: %w", err)
		}
		cfg.ProbeCore.FrameMaxBytes = v
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_PROBE_CORE_FALLBACK_TO_GO"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_FALLBACK_TO_GO: %w", err)
		}
		cfg.ProbeCore.FallbackToGo = parsed
	}

	return cfg, nil
}

func parseEnvBool(raw string) (bool, error) {
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, err
	}
	return parsed, nil
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

func isValidProbeCoreCollectorModule(module string) bool {
	switch module {
	case "all", "host", "disk", "network", "rdma", "netlink", "ethtool", "perf", "ebpf", "gpu", "process":
		return true
	default:
		return false
	}
}

func probeCoreArgsContainCollectorsFlag(args []string) bool {
	for _, arg := range args {
		normalized := strings.TrimSpace(strings.ToLower(arg))
		if normalized == "--collectors" || strings.HasPrefix(normalized, "--collectors=") {
			return true
		}
	}
	return false
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
