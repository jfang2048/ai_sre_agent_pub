package collector

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config configures the push-first collector.
type Config struct {
	CollectorID                         string            `yaml:"collector_id" json:"collector_id"`
	Hostname                            string            `yaml:"hostname" json:"hostname"`
	Version                             string            `yaml:"version" json:"version"`
	Labels                              map[string]string `yaml:"labels" json:"labels"`
	Deployment                          DeploymentConfig  `yaml:"deployment" json:"deployment"`
	PrivilegeProfile                    string            `yaml:"privilege_profile" json:"privilege_profile"`
	CollectionInterval                  time.Duration     `yaml:"collection_interval" json:"collection_interval"`
	ControllerEndpoints                 []string          `yaml:"controller_endpoints" json:"controller_endpoints"`
	MirrorSend                          bool              `yaml:"mirror_send" json:"mirror_send"`
	SpoolDir                            string            `yaml:"spool_dir" json:"spool_dir"`
	SpoolMaxBytes                       int64             `yaml:"spool_max_bytes" json:"spool_max_bytes"`
	SpoolSyncInterval                   time.Duration     `yaml:"spool_sync_interval" json:"spool_sync_interval"`
	SpoolOffsetSyncInterval             time.Duration     `yaml:"spool_offset_sync_interval" json:"spool_offset_sync_interval"`
	TopK                                int               `yaml:"topk" json:"topk"`
	LogPaths                            []string          `yaml:"log_paths" json:"log_paths"`
	ShmEnabled                          bool              `yaml:"shm_enabled" json:"shm_enabled"`
	ShmName                             string            `yaml:"shm_name" json:"shm_name"`
	RuntimeMode                         string            `yaml:"runtime_mode" json:"runtime_mode"`
	GrpcCompress                        bool              `yaml:"grpc_compress" json:"grpc_compress"`
	Level                               int               `yaml:"level" json:"level"`
	ExternalMetricsCmd                  string            `yaml:"external_metrics_cmd" json:"external_metrics_cmd"`
	ExternalMetricsTimeout              time.Duration     `yaml:"external_metrics_timeout" json:"external_metrics_timeout"`
	AdaptivePolling                     bool              `yaml:"adaptive_polling" json:"adaptive_polling"`
	MinCollectionInterval               time.Duration     `yaml:"min_collection_interval" json:"min_collection_interval"`
	MaxCollectionInterval               time.Duration     `yaml:"max_collection_interval" json:"max_collection_interval"`
	SuppressCachedAuxPayloads           bool              `yaml:"suppress_cached_aux_payloads" json:"suppress_cached_aux_payloads"`
	SuppressUnchangedProcessPayloads    bool              `yaml:"suppress_unchanged_process_payloads" json:"suppress_unchanged_process_payloads"`
	ProcessPayloadRefreshInterval       time.Duration     `yaml:"process_payload_refresh_interval" json:"process_payload_refresh_interval"`
	SuppressCachedCompatHardwareMetrics bool              `yaml:"suppress_cached_compat_hardware_metrics" json:"suppress_cached_compat_hardware_metrics"`
	SuppressUnchangedLowChurnMetrics    bool              `yaml:"suppress_unchanged_low_churn_metrics" json:"suppress_unchanged_low_churn_metrics"`
	LowChurnMetricsRefreshInterval      time.Duration     `yaml:"low_churn_metrics_refresh_interval" json:"low_churn_metrics_refresh_interval"`
	MetricsListenAddress                string            `yaml:"metrics_listen_address" json:"metrics_listen_address"`
	TracingJaegerEndpoint               string            `yaml:"tracing_jaeger_endpoint" json:"tracing_jaeger_endpoint"`
	Transport                           TransportConfig   `yaml:"transport" json:"transport"`
	EBPF                                EBPFConfig        `yaml:"ebpf" json:"ebpf"`
	ProbeCore                           ProbeCoreConfig   `yaml:"probe_core" json:"probe_core"`
	Security                            SecurityConfig    `yaml:"security" json:"security"`
	Protection                          ProtectionConfig  `yaml:"protection" json:"protection"`
	Hardware                            HardwareConfig    `yaml:"hardware" json:"hardware"`
}

const (
	defaultInterval                = 5 * time.Second
	defaultProbeCoreInterval       = 1 * time.Second
	defaultSpoolDir                = "./data/collector/spool"
	defaultSpoolMax                = int64(128 * 1024 * 1024)
	defaultSpoolSyncInterval       = 1 * time.Second
	defaultSpoolOffsetSyncInterval = 1 * time.Second
	defaultTopK                    = 10
	defaultShmName                 = "/sre_collector_metrics"
	defaultEBPFSock                = "./data/collector/run/sre_collector_ebpf.sock"
	defaultProbeCoreBinaryPath     = "./build/sre-probe-core"
	defaultProbeCoreCompression    = "none"
	defaultProbeCoreFrameMaxBytes  = 8 * 1024 * 1024
	defaultSecurityLargeFileBytes  = int64(100 * 1024 * 1024)
	defaultSecurityRapidGrowthB    = int64(16 * 1024 * 1024)
	defaultHardwareRefreshInterval = 6 * time.Hour
	defaultPrivilegeProfile        = "deep-runtime"
)

const (
	PrivilegeProfileMinimal       = "minimal"
	PrivilegeProfileObservability = "observability"
	PrivilegeProfileDeepRuntime   = "deep-runtime"
	PrivilegeProfileGPU           = "gpu"
)

// TransportConfig defines gRPC transport runtime knobs.
type TransportConfig struct {
	DialTimeout    time.Duration       `yaml:"dial_timeout" json:"dial_timeout"`
	RPCTimeout     time.Duration       `yaml:"rpc_timeout" json:"rpc_timeout"`
	AllowPlaintext bool                `yaml:"allow_plaintext" json:"allow_plaintext"`
	TLS            TLSConfig           `yaml:"tls" json:"tls"`
	Auth           TransportAuthConfig `yaml:"auth" json:"auth"`
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

// TransportAuthConfig controls collector -> controller ingest authentication.
type TransportAuthConfig struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	BearerTokenEnv string `yaml:"bearer_token_env" json:"bearer_token_env"`
	BearerToken    string `yaml:"-" json:"-"`
}

// EBPFConfig mirrors probe.EBPFConfig but avoids an import cycle.
type EBPFConfig struct {
	Enabled               bool          `yaml:"enabled" json:"enabled"`
	SocketPath            string        `yaml:"socket_path" json:"socket_path"`
	Categories            []string      `yaml:"categories" json:"categories"`
	MaxMsgBytes           int           `yaml:"max_msg_bytes" json:"max_msg_bytes"`
	RingSize              int           `yaml:"ring_size" json:"ring_size"`
	EventFlushLimit       int           `yaml:"event_flush_limit" json:"event_flush_limit"`
	AllowedListenPorts    []int         `yaml:"allowed_listen_ports" json:"allowed_listen_ports"`
	SyntheticPollInterval time.Duration `yaml:"synthetic_poll_interval" json:"synthetic_poll_interval"`
	LongLivedTCPThreshold time.Duration `yaml:"long_lived_tcp_threshold" json:"long_lived_tcp_threshold"`
}

// ProbeCoreConfig controls the C++ probe-core IPC runtime.
type ProbeCoreConfig struct {
	Enabled                         bool          `yaml:"enabled" json:"enabled"`
	BinaryPath                      string        `yaml:"binary_path" json:"binary_path"`
	Collectors                      []string      `yaml:"collectors" json:"collectors"`
	Args                            []string      `yaml:"args" json:"args"`
	Interval                        time.Duration `yaml:"interval" json:"interval"`
	Compression                     string        `yaml:"compression" json:"compression"`
	QueueDepth                      int           `yaml:"queue_depth" json:"queue_depth"`
	WindowSamples                   int           `yaml:"window_samples" json:"window_samples"`
	ProcessIntervalSamples          int           `yaml:"process_interval_samples" json:"process_interval_samples"`
	HostProcFallbackIntervalSamples int           `yaml:"host_proc_fallback_interval_samples" json:"host_proc_fallback_interval_samples"`
	PressureIntervalSamples         int           `yaml:"pressure_interval_samples" json:"pressure_interval_samples"`
	NetlinkIntervalSamples          int           `yaml:"netlink_interval_samples" json:"netlink_interval_samples"`
	GPUIntervalSamples              int           `yaml:"gpu_interval_samples" json:"gpu_interval_samples"`
	StartupTimeout                  time.Duration `yaml:"startup_timeout" json:"startup_timeout"`
	StaleAfter                      time.Duration `yaml:"stale_after" json:"stale_after"`
	FrameMaxBytes                   int           `yaml:"frame_max_bytes" json:"frame_max_bytes"`
	Nice                            int           `yaml:"nice" json:"nice"`
	FallbackToGo                    bool          `yaml:"fallback_to_go" json:"fallback_to_go"`
	EmitRawAliasedMetrics           bool          `yaml:"emit_raw_aliased_metrics" json:"emit_raw_aliased_metrics"`
}

// SecurityConfig controls collector-side security telemetry and local baseline
// drift detection.
type SecurityConfig struct {
	Enabled                 bool          `yaml:"enabled" json:"enabled"`
	AuditInterval           time.Duration `yaml:"audit_interval" json:"audit_interval"`
	RecentEventLimit        int           `yaml:"recent_event_limit" json:"recent_event_limit"`
	BaselineWarmupSamples   int           `yaml:"baseline_warmup_samples" json:"baseline_warmup_samples"`
	MaxWalkEntries          int           `yaml:"max_walk_entries" json:"max_walk_entries"`
	LargeFileThresholdBytes int64         `yaml:"large_file_threshold_bytes" json:"large_file_threshold_bytes"`
	RapidGrowthThresholdB   int64         `yaml:"rapid_growth_threshold_bytes" json:"rapid_growth_threshold_bytes"`
}

// ProtectionConfig controls collector-side self-protection and load shedding.
type ProtectionConfig struct {
	Enabled                      bool          `yaml:"enabled" json:"enabled"`
	Nice                         int           `yaml:"nice" json:"nice"`
	MaxCPUPercent                float64       `yaml:"max_cpu_percent" json:"max_cpu_percent"`
	MaxCPUTimePerInterval        time.Duration `yaml:"max_cpu_time_per_interval" json:"max_cpu_time_per_interval"`
	MemorySoftLimitBytes         uint64        `yaml:"memory_soft_limit_bytes" json:"memory_soft_limit_bytes"`
	SpoolHighWatermarkRatio      float64       `yaml:"spool_high_watermark_ratio" json:"spool_high_watermark_ratio"`
	SpoolCriticalWatermarkRatio  float64       `yaml:"spool_critical_watermark_ratio" json:"spool_critical_watermark_ratio"`
	MaxDrainRecordsPerCycle      int           `yaml:"max_drain_records_per_cycle" json:"max_drain_records_per_cycle"`
	MaxDrainDuration             time.Duration `yaml:"max_drain_duration" json:"max_drain_duration"`
	DisableLogsUnderPressure     bool          `yaml:"disable_logs_under_pressure" json:"disable_logs_under_pressure"`
	DisableSecurityUnderPressure bool          `yaml:"disable_security_under_pressure" json:"disable_security_under_pressure"`
	DisableExternalUnderPressure bool          `yaml:"disable_external_under_pressure" json:"disable_external_under_pressure"`
}

// HardwareConfig controls cached hardware discovery.
type HardwareConfig struct {
	Enabled         bool          `yaml:"enabled" json:"enabled"`
	RefreshInterval time.Duration `yaml:"refresh_interval" json:"refresh_interval"`
}

// DefaultConfig provides baseline configuration values.
func DefaultConfig() Config {
	return Config{
		Deployment:                          DefaultDeploymentConfig(),
		PrivilegeProfile:                    defaultPrivilegeProfile,
		CollectionInterval:                  defaultInterval,
		ControllerEndpoints:                 []string{"localhost:9090"},
		SpoolDir:                            defaultSpoolDir,
		SpoolMaxBytes:                       defaultSpoolMax,
		SpoolSyncInterval:                   defaultSpoolSyncInterval,
		SpoolOffsetSyncInterval:             defaultSpoolOffsetSyncInterval,
		TopK:                                defaultTopK,
		ShmName:                             defaultShmName,
		RuntimeMode:                         "auto",
		Level:                               2,
		ExternalMetricsTimeout:              500 * time.Millisecond,
		AdaptivePolling:                     true,
		MinCollectionInterval:               1 * time.Second,
		MaxCollectionInterval:               20 * time.Second,
		SuppressCachedAuxPayloads:           true,
		SuppressUnchangedProcessPayloads:    true,
		ProcessPayloadRefreshInterval:       1 * time.Minute,
		SuppressCachedCompatHardwareMetrics: true,
		SuppressUnchangedLowChurnMetrics:    true,
		LowChurnMetricsRefreshInterval:      5 * time.Minute,
		Transport: TransportConfig{
			DialTimeout:    10 * time.Second,
			RPCTimeout:     10 * time.Second,
			AllowPlaintext: true,
			TLS: TLSConfig{
				ReloadInterval: 30 * time.Second,
			},
			Auth: TransportAuthConfig{
				Enabled:        false,
				BearerTokenEnv: "SRE_COLLECTOR_INGEST_TOKEN",
			},
		},
		EBPF: EBPFConfig{
			Enabled:               true,
			SocketPath:            defaultEBPFSock,
			Categories:            []string{"syscall", "process", "network", "file", "security", "resource"},
			MaxMsgBytes:           65536,
			RingSize:              2048,
			EventFlushLimit:       256,
			AllowedListenPorts:    []int{22, 53, 80, 443, 2379, 2380, 3000, 5432, 6443, 8080, 8443, 9090},
			SyntheticPollInterval: 10 * time.Second,
			LongLivedTCPThreshold: 5 * time.Minute,
		},
		ProbeCore: ProbeCoreConfig{
			Enabled:                         true,
			BinaryPath:                      defaultProbeCoreBinaryPath,
			Collectors:                      nil,
			Args:                            []string{"--host-mode", "auto"},
			Interval:                        defaultProbeCoreInterval,
			Compression:                     defaultProbeCoreCompression,
			QueueDepth:                      16,
			WindowSamples:                   6,
			ProcessIntervalSamples:          2,
			HostProcFallbackIntervalSamples: 10,
			PressureIntervalSamples:         3,
			NetlinkIntervalSamples:          2,
			GPUIntervalSamples:              1,
			StartupTimeout:                  3 * time.Second,
			StaleAfter:                      15 * time.Second,
			FrameMaxBytes:                   defaultProbeCoreFrameMaxBytes,
			Nice:                            10,
			FallbackToGo:                    true,
			EmitRawAliasedMetrics:           false,
		},
		Security: SecurityConfig{
			Enabled:                 true,
			AuditInterval:           5 * time.Minute,
			RecentEventLimit:        128,
			BaselineWarmupSamples:   3,
			MaxWalkEntries:          6000,
			LargeFileThresholdBytes: defaultSecurityLargeFileBytes,
			RapidGrowthThresholdB:   defaultSecurityRapidGrowthB,
		},
		Protection: ProtectionConfig{
			Enabled:                      true,
			Nice:                         10,
			MaxCPUPercent:                5,
			MaxCPUTimePerInterval:        250 * time.Millisecond,
			MemorySoftLimitBytes:         256 * 1024 * 1024,
			SpoolHighWatermarkRatio:      0.50,
			SpoolCriticalWatermarkRatio:  0.80,
			MaxDrainRecordsPerCycle:      8,
			MaxDrainDuration:             750 * time.Millisecond,
			DisableLogsUnderPressure:     true,
			DisableSecurityUnderPressure: true,
			DisableExternalUnderPressure: true,
		},
		Hardware: HardwareConfig{
			Enabled:         true,
			RefreshInterval: defaultHardwareRefreshInterval,
		},
	}
}

// LoadConfig builds a Config using defaults, then optional file, then env overrides.
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
	cfg = applyDeploymentDefaults(cfg)
	cfg = applyPrivilegeProfile(cfg)
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
	if fileExists("/etc/ai-sre-agent/collector.yaml") {
		return "/etc/ai-sre-agent/collector.yaml"
	}
	if fileExists("/etc/sre-collector/config.yaml") {
		return "/etc/sre-collector/config.yaml"
	}
	return ""
}

// Validate checks invariants early so runtime loops can stay simple.
func (cfg Config) Validate() error {
	if normalizeDeploymentMode(cfg.Deployment.Mode) == "" {
		return fmt.Errorf("deployment.mode must be one of local-dev, standalone, cluster-lite, distributed")
	}
	if normalizePrivilegeProfile(cfg.PrivilegeProfile) == "" {
		return fmt.Errorf("privilege_profile must be one of minimal, observability, deep-runtime, gpu")
	}
	if cfg.CollectionInterval <= 0 {
		return fmt.Errorf("collection_interval must be > 0")
	}
	if cfg.MinCollectionInterval <= 0 {
		return fmt.Errorf("min_collection_interval must be > 0")
	}
	if cfg.MaxCollectionInterval < cfg.MinCollectionInterval {
		return fmt.Errorf("max_collection_interval must be >= min_collection_interval")
	}
	if cfg.LowChurnMetricsRefreshInterval <= 0 {
		return fmt.Errorf("low_churn_metrics_refresh_interval must be > 0")
	}
	if cfg.ProcessPayloadRefreshInterval <= 0 {
		return fmt.Errorf("process_payload_refresh_interval must be > 0")
	}
	if cfg.SpoolDir == "" {
		return fmt.Errorf("spool_dir is required")
	}
	if cfg.SpoolMaxBytes <= 0 {
		return fmt.Errorf("spool_max_bytes must be > 0")
	}
	if cfg.SpoolSyncInterval < 0 {
		return fmt.Errorf("spool_sync_interval must be >= 0")
	}
	if cfg.SpoolOffsetSyncInterval < 0 {
		return fmt.Errorf("spool_offset_sync_interval must be >= 0")
	}
	if cfg.TopK <= 0 {
		return fmt.Errorf("topk must be > 0")
	}
	cfg.RuntimeMode = normalizeCollectorRuntimeMode(cfg.RuntimeMode)
	if cfg.Level < 1 || cfg.Level > 5 {
		return fmt.Errorf("level must be between 1 and 5")
	}
	if cfg.RuntimeMode == "" {
		return fmt.Errorf("runtime_mode must be one of auto, host, namespace, limited")
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
	} else if !cfg.Transport.AllowPlaintext {
		return fmt.Errorf("transport plaintext is disabled but transport.tls.enabled=false")
	}
	if cfg.Transport.Auth.Enabled {
		if strings.TrimSpace(cfg.Transport.Auth.BearerTokenEnv) == "" {
			return fmt.Errorf("transport.auth.bearer_token_env is required when transport.auth.enabled=true")
		}
		if strings.TrimSpace(cfg.Transport.Auth.BearerToken) == "" {
			return fmt.Errorf("transport auth token env %q is empty", strings.TrimSpace(cfg.Transport.Auth.BearerTokenEnv))
		}
	}
	if strings.TrimSpace(cfg.EBPF.SocketPath) == "" {
		return fmt.Errorf("ebpf.socket_path is required")
	}
	if cfg.EBPF.MaxMsgBytes <= 0 {
		return fmt.Errorf("ebpf.max_msg_bytes must be > 0")
	}
	if cfg.EBPF.RingSize <= 0 {
		return fmt.Errorf("ebpf.ring_size must be > 0")
	}
	if cfg.EBPF.EventFlushLimit <= 0 {
		return fmt.Errorf("ebpf.event_flush_limit must be > 0")
	}
	if cfg.EBPF.SyntheticPollInterval <= 0 {
		return fmt.Errorf("ebpf.synthetic_poll_interval must be > 0")
	}
	if cfg.EBPF.LongLivedTCPThreshold <= 0 {
		return fmt.Errorf("ebpf.long_lived_tcp_threshold must be > 0")
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
		if cfg.ProbeCore.Interval <= 0 {
			return fmt.Errorf("probe_core.interval must be > 0")
		}
		if cfg.ProbeCore.WindowSamples <= 0 {
			return fmt.Errorf("probe_core.window_samples must be > 0")
		}
		if cfg.ProbeCore.ProcessIntervalSamples <= 0 {
			return fmt.Errorf("probe_core.process_interval_samples must be > 0")
		}
		if cfg.ProbeCore.HostProcFallbackIntervalSamples <= 0 {
			return fmt.Errorf("probe_core.host_proc_fallback_interval_samples must be > 0")
		}
		if cfg.ProbeCore.PressureIntervalSamples <= 0 {
			return fmt.Errorf("probe_core.pressure_interval_samples must be > 0")
		}
		if cfg.ProbeCore.NetlinkIntervalSamples <= 0 {
			return fmt.Errorf("probe_core.netlink_interval_samples must be > 0")
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
		if cfg.ProbeCore.Nice < -20 || cfg.ProbeCore.Nice > 19 {
			return fmt.Errorf("probe_core.nice must be between -20 and 19")
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
	if cfg.Security.AuditInterval <= 0 {
		return fmt.Errorf("security.audit_interval must be > 0")
	}
	if cfg.Security.RecentEventLimit <= 0 {
		return fmt.Errorf("security.recent_event_limit must be > 0")
	}
	if cfg.Security.BaselineWarmupSamples <= 0 {
		return fmt.Errorf("security.baseline_warmup_samples must be > 0")
	}
	if cfg.Security.MaxWalkEntries <= 0 {
		return fmt.Errorf("security.max_walk_entries must be > 0")
	}
	if cfg.Security.LargeFileThresholdBytes <= 0 {
		return fmt.Errorf("security.large_file_threshold_bytes must be > 0")
	}
	if cfg.Security.RapidGrowthThresholdB <= 0 {
		return fmt.Errorf("security.rapid_growth_threshold_bytes must be > 0")
	}
	if cfg.Protection.Nice < -20 || cfg.Protection.Nice > 19 {
		return fmt.Errorf("protection.nice must be between -20 and 19")
	}
	if cfg.Protection.MaxCPUPercent < 0 {
		return fmt.Errorf("protection.max_cpu_percent must be >= 0")
	}
	if cfg.Protection.MaxCPUTimePerInterval < 0 {
		return fmt.Errorf("protection.max_cpu_time_per_interval must be >= 0")
	}
	if cfg.Protection.SpoolHighWatermarkRatio < 0 || cfg.Protection.SpoolHighWatermarkRatio > 1 {
		return fmt.Errorf("protection.spool_high_watermark_ratio must be between 0 and 1")
	}
	if cfg.Protection.SpoolCriticalWatermarkRatio < 0 || cfg.Protection.SpoolCriticalWatermarkRatio > 1 {
		return fmt.Errorf("protection.spool_critical_watermark_ratio must be between 0 and 1")
	}
	if cfg.Protection.SpoolCriticalWatermarkRatio < cfg.Protection.SpoolHighWatermarkRatio {
		return fmt.Errorf("protection.spool_critical_watermark_ratio must be >= protection.spool_high_watermark_ratio")
	}
	if cfg.Protection.MaxDrainRecordsPerCycle <= 0 {
		return fmt.Errorf("protection.max_drain_records_per_cycle must be > 0")
	}
	if cfg.Protection.MaxDrainDuration <= 0 {
		return fmt.Errorf("protection.max_drain_duration must be > 0")
	}
	if cfg.Hardware.RefreshInterval <= 0 {
		return fmt.Errorf("hardware.refresh_interval must be > 0")
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
	if env := strings.TrimSpace(os.Getenv("SRE_COLLECTOR_PRIVILEGE_PROFILE")); env != "" {
		cfg.PrivilegeProfile = env
	}
	if env := strings.TrimSpace(os.Getenv("SRE_COLLECTOR_DEPLOYMENT_MODE")); env != "" {
		cfg.Deployment.Mode = env
	} else if env := strings.TrimSpace(os.Getenv("SRE_DEPLOYMENT_MODE")); env != "" {
		cfg.Deployment.Mode = env
	}
	if env := strings.TrimSpace(os.Getenv("SRE_COLLECTOR_CLUSTER_NAME")); env != "" {
		cfg.Deployment.ClusterName = env
	} else if env := strings.TrimSpace(os.Getenv("SRE_CLUSTER_NAME")); env != "" {
		cfg.Deployment.ClusterName = env
	}
	if env := strings.TrimSpace(os.Getenv("SRE_COLLECTOR_DATA_ROOT")); env != "" {
		cfg.Deployment.DataRoot = filepath.Clean(env)
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
	if raw := os.Getenv("SRE_COLLECTOR_SPOOL_SYNC_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SPOOL_SYNC_INTERVAL: %w", err)
		}
		cfg.SpoolSyncInterval = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_SPOOL_OFFSET_SYNC_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SPOOL_OFFSET_SYNC_INTERVAL: %w", err)
		}
		cfg.SpoolOffsetSyncInterval = parsed
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
	if raw := os.Getenv("SRE_COLLECTOR_RUNTIME_MODE"); raw != "" {
		cfg.RuntimeMode = raw
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
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_SUPPRESS_CACHED_AUX_PAYLOADS"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SUPPRESS_CACHED_AUX_PAYLOADS: %w", err)
		}
		cfg.SuppressCachedAuxPayloads = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_SUPPRESS_UNCHANGED_PROCESS_PAYLOADS"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SUPPRESS_UNCHANGED_PROCESS_PAYLOADS: %w", err)
		}
		cfg.SuppressUnchangedProcessPayloads = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROCESS_PAYLOAD_REFRESH_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROCESS_PAYLOAD_REFRESH_INTERVAL: %w", err)
		}
		cfg.ProcessPayloadRefreshInterval = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_SUPPRESS_CACHED_COMPAT_HARDWARE_METRICS"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SUPPRESS_CACHED_COMPAT_HARDWARE_METRICS: %w", err)
		}
		cfg.SuppressCachedCompatHardwareMetrics = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_SUPPRESS_UNCHANGED_LOW_CHURN_METRICS"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SUPPRESS_UNCHANGED_LOW_CHURN_METRICS: %w", err)
		}
		cfg.SuppressUnchangedLowChurnMetrics = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_LOW_CHURN_METRICS_REFRESH_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_LOW_CHURN_METRICS_REFRESH_INTERVAL: %w", err)
		}
		cfg.LowChurnMetricsRefreshInterval = parsed
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
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_ALLOW_PLAINTEXT"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_ALLOW_PLAINTEXT: %w", err)
		}
		cfg.Transport.AllowPlaintext = parsed
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
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_TRANSPORT_AUTH_ENABLED"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_TRANSPORT_AUTH_ENABLED: %w", err)
		}
		cfg.Transport.Auth.Enabled = parsed
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_COLLECTOR_TRANSPORT_AUTH_BEARER_TOKEN_ENV")); raw != "" {
		cfg.Transport.Auth.BearerTokenEnv = raw
	}
	if cfg.Transport.Auth.BearerTokenEnv == "" {
		cfg.Transport.Auth.BearerTokenEnv = "SRE_COLLECTOR_INGEST_TOKEN"
	}
	if cfg.Transport.Auth.Enabled {
		cfg.Transport.Auth.BearerToken = strings.TrimSpace(os.Getenv(cfg.Transport.Auth.BearerTokenEnv))
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
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_RING_SIZE"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_EBPF_RING_SIZE: %w", err)
		}
		cfg.EBPF.RingSize = v
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_EVENT_FLUSH_LIMIT"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_EBPF_EVENT_FLUSH_LIMIT: %w", err)
		}
		cfg.EBPF.EventFlushLimit = v
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_ALLOWED_LISTEN_PORTS"); raw != "" {
		parts := splitCSV(raw)
		ports := make([]int, 0, len(parts))
		for _, part := range parts {
			port, err := strconv.Atoi(part)
			if err != nil {
				return cfg, fmt.Errorf("parse SRE_COLLECTOR_EBPF_ALLOWED_LISTEN_PORTS: %w", err)
			}
			ports = append(ports, port)
		}
		cfg.EBPF.AllowedListenPorts = ports
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_SYNTHETIC_POLL_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_EBPF_SYNTHETIC_POLL_INTERVAL: %w", err)
		}
		cfg.EBPF.SyntheticPollInterval = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_EBPF_LONG_LIVED_TCP_THRESHOLD"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_EBPF_LONG_LIVED_TCP_THRESHOLD: %w", err)
		}
		cfg.EBPF.LongLivedTCPThreshold = parsed
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
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_INTERVAL: %w", err)
		}
		cfg.ProbeCore.Interval = parsed
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
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_PROCESS_INTERVAL_SAMPLES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_PROCESS_INTERVAL_SAMPLES: %w", err)
		}
		cfg.ProbeCore.ProcessIntervalSamples = v
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_HOST_PROC_FALLBACK_INTERVAL_SAMPLES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_HOST_PROC_FALLBACK_INTERVAL_SAMPLES: %w", err)
		}
		cfg.ProbeCore.HostProcFallbackIntervalSamples = v
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_PRESSURE_INTERVAL_SAMPLES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_PRESSURE_INTERVAL_SAMPLES: %w", err)
		}
		cfg.ProbeCore.PressureIntervalSamples = v
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_NETLINK_INTERVAL_SAMPLES"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_NETLINK_INTERVAL_SAMPLES: %w", err)
		}
		cfg.ProbeCore.NetlinkIntervalSamples = v
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
	if raw := os.Getenv("SRE_COLLECTOR_PROBE_CORE_NICE"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_NICE: %w", err)
		}
		cfg.ProbeCore.Nice = v
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_PROBE_CORE_FALLBACK_TO_GO"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_FALLBACK_TO_GO: %w", err)
		}
		cfg.ProbeCore.FallbackToGo = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_PROBE_CORE_EMIT_RAW_ALIASED_METRICS"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROBE_CORE_EMIT_RAW_ALIASED_METRICS: %w", err)
		}
		cfg.ProbeCore.EmitRawAliasedMetrics = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_SECURITY_ENABLED"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SECURITY_ENABLED: %w", err)
		}
		cfg.Security.Enabled = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_SECURITY_AUDIT_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SECURITY_AUDIT_INTERVAL: %w", err)
		}
		cfg.Security.AuditInterval = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_SECURITY_RECENT_EVENT_LIMIT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SECURITY_RECENT_EVENT_LIMIT: %w", err)
		}
		cfg.Security.RecentEventLimit = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_SECURITY_BASELINE_WARMUP_SAMPLES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SECURITY_BASELINE_WARMUP_SAMPLES: %w", err)
		}
		cfg.Security.BaselineWarmupSamples = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_SECURITY_MAX_WALK_ENTRIES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SECURITY_MAX_WALK_ENTRIES: %w", err)
		}
		cfg.Security.MaxWalkEntries = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_SECURITY_LARGE_FILE_THRESHOLD_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SECURITY_LARGE_FILE_THRESHOLD_BYTES: %w", err)
		}
		cfg.Security.LargeFileThresholdBytes = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_SECURITY_RAPID_GROWTH_THRESHOLD_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_SECURITY_RAPID_GROWTH_THRESHOLD_BYTES: %w", err)
		}
		cfg.Security.RapidGrowthThresholdB = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_PROTECTION_ENABLED"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_ENABLED: %w", err)
		}
		cfg.Protection.Enabled = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROTECTION_NICE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_NICE: %w", err)
		}
		cfg.Protection.Nice = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROTECTION_MAX_CPU_PERCENT"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_MAX_CPU_PERCENT: %w", err)
		}
		cfg.Protection.MaxCPUPercent = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROTECTION_MAX_CPU_TIME_PER_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_MAX_CPU_TIME_PER_INTERVAL: %w", err)
		}
		cfg.Protection.MaxCPUTimePerInterval = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROTECTION_MEMORY_SOFT_LIMIT_BYTES"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_MEMORY_SOFT_LIMIT_BYTES: %w", err)
		}
		cfg.Protection.MemorySoftLimitBytes = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROTECTION_SPOOL_HIGH_WATERMARK_RATIO"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_SPOOL_HIGH_WATERMARK_RATIO: %w", err)
		}
		cfg.Protection.SpoolHighWatermarkRatio = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROTECTION_SPOOL_CRITICAL_WATERMARK_RATIO"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_SPOOL_CRITICAL_WATERMARK_RATIO: %w", err)
		}
		cfg.Protection.SpoolCriticalWatermarkRatio = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROTECTION_MAX_DRAIN_RECORDS_PER_CYCLE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_MAX_DRAIN_RECORDS_PER_CYCLE: %w", err)
		}
		cfg.Protection.MaxDrainRecordsPerCycle = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_PROTECTION_MAX_DRAIN_DURATION"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_MAX_DRAIN_DURATION: %w", err)
		}
		cfg.Protection.MaxDrainDuration = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_PROTECTION_DISABLE_LOGS_UNDER_PRESSURE"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_DISABLE_LOGS_UNDER_PRESSURE: %w", err)
		}
		cfg.Protection.DisableLogsUnderPressure = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_PROTECTION_DISABLE_SECURITY_UNDER_PRESSURE"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_DISABLE_SECURITY_UNDER_PRESSURE: %w", err)
		}
		cfg.Protection.DisableSecurityUnderPressure = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_PROTECTION_DISABLE_EXTERNAL_UNDER_PRESSURE"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_PROTECTION_DISABLE_EXTERNAL_UNDER_PRESSURE: %w", err)
		}
		cfg.Protection.DisableExternalUnderPressure = parsed
	}
	if raw, ok := os.LookupEnv("SRE_COLLECTOR_HARDWARE_ENABLED"); ok {
		parsed, err := parseEnvBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_HARDWARE_ENABLED: %w", err)
		}
		cfg.Hardware.Enabled = parsed
	}
	if raw := os.Getenv("SRE_COLLECTOR_HARDWARE_REFRESH_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("parse SRE_COLLECTOR_HARDWARE_REFRESH_INTERVAL: %w", err)
		}
		cfg.Hardware.RefreshInterval = parsed
	}

	return cfg, nil
}

func normalizePrivilegeProfile(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return defaultPrivilegeProfile
	case PrivilegeProfileMinimal, PrivilegeProfileObservability, PrivilegeProfileDeepRuntime, PrivilegeProfileGPU:
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func applyPrivilegeProfile(cfg Config) Config {
	profile := normalizePrivilegeProfile(cfg.PrivilegeProfile)
	if profile == "" {
		profile = defaultPrivilegeProfile
	}
	cfg.PrivilegeProfile = profile

	switch profile {
	case PrivilegeProfileMinimal:
		cfg.RuntimeMode = runtimeModeLimited
		cfg.EBPF.Enabled = false
		cfg.ProbeCore.Enabled = false
		cfg.Security.Enabled = false
		cfg.LogPaths = nil
	case PrivilegeProfileObservability:
		cfg.RuntimeMode = runtimeModeLimited
		cfg.EBPF.Enabled = false
		cfg.ProbeCore.Enabled = false
		cfg.Security.Enabled = false
	case PrivilegeProfileGPU:
		cfg.RuntimeMode = runtimeModeLimited
		cfg.EBPF.Enabled = false
		cfg.Security.Enabled = false
		cfg.ProbeCore.Enabled = true
		if len(cfg.ProbeCore.Collectors) == 0 || (len(cfg.ProbeCore.Collectors) == 1 && strings.EqualFold(cfg.ProbeCore.Collectors[0], "all")) {
			cfg.ProbeCore.Collectors = []string{"host", "network", "gpu"}
		}
	default:
		cfg.PrivilegeProfile = PrivilegeProfileDeepRuntime
	}
	return cfg
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
