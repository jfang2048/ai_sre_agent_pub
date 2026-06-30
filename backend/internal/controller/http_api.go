package controller

// APIConfig controls controller HTTP API governance. These settings only apply
// to the controller HTTP surface; gRPC ingest stays on its existing push path.
type APIConfig struct {
	RateLimitEnabled       bool     `yaml:"rate_limit_enabled" json:"rate_limit_enabled"`
	RateLimitRPS           float64  `yaml:"rate_limit_rps" json:"rate_limit_rps"`
	RateLimitBurst         int      `yaml:"rate_limit_burst" json:"rate_limit_burst"`
	ActionRateLimitEnabled bool     `yaml:"action_rate_limit_enabled" json:"action_rate_limit_enabled"`
	ActionRateLimitRPS     float64  `yaml:"action_rate_limit_rps" json:"action_rate_limit_rps"`
	ActionRateLimitBurst   int      `yaml:"action_rate_limit_burst" json:"action_rate_limit_burst"`
	AuditMutations         bool     `yaml:"audit_mutations" json:"audit_mutations"`
	AllowedOrigins         []string `yaml:"allowed_origins" json:"allowed_origins,omitempty"`
}

// DefaultAPIConfig returns controller HTTP API defaults. Local-dev keeps rate
// limiting off by default; cluster packaging can turn it on explicitly.
func DefaultAPIConfig() APIConfig {
	return APIConfig{
		RateLimitEnabled:       false,
		RateLimitRPS:           20,
		RateLimitBurst:         40,
		ActionRateLimitEnabled: false,
		ActionRateLimitRPS:     5,
		ActionRateLimitBurst:   10,
		AuditMutations:         true,
	}
}

func normalizeAPIConfig(cfg APIConfig) APIConfig {
	def := DefaultAPIConfig()
	if cfg.RateLimitRPS <= 0 {
		cfg.RateLimitRPS = def.RateLimitRPS
	}
	if cfg.RateLimitBurst <= 0 {
		cfg.RateLimitBurst = def.RateLimitBurst
	}
	if cfg.ActionRateLimitRPS <= 0 {
		cfg.ActionRateLimitRPS = def.ActionRateLimitRPS
	}
	if cfg.ActionRateLimitBurst <= 0 {
		cfg.ActionRateLimitBurst = def.ActionRateLimitBurst
	}
	cfg.AllowedOrigins = normalizeAllowedOrigins(cfg.AllowedOrigins)
	return cfg
}

// NormalizeAPIConfigForRuntime applies controller API defaults after config/env
// loading. It is exported so the controller CLI can normalize a fully merged
// config before startup.
func NormalizeAPIConfigForRuntime(cfg APIConfig) APIConfig {
	return normalizeAPIConfig(cfg)
}
