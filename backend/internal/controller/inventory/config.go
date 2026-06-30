package inventory

import "time"

// Config controls probe inventory behavior.
type Config struct {
	Enabled      bool          `yaml:"enabled" json:"enabled"`
	HeartbeatTTL time.Duration `yaml:"heartbeat_ttl" json:"heartbeat_ttl"`
	TargetsFile  string        `yaml:"targets_file" json:"targets_file"`

	// StaticTargets is populated at startup from the legacy top-level controller
	// nodes list and the dedicated controller target inventory file.
	StaticTargets []StaticProbe `yaml:"-" json:"-"`
}

// StaticProbe configures one statically known probe.
type StaticProbe struct {
	ID       string            `yaml:"id" json:"id"`
	Name     string            `yaml:"name,omitempty" json:"name,omitempty"`
	Hostname string            `yaml:"hostname,omitempty" json:"hostname,omitempty"`
	Address  string            `yaml:"address,omitempty" json:"address,omitempty"`
	Port     int               `yaml:"port,omitempty" json:"port,omitempty"`
	Enabled  bool              `yaml:"enabled" json:"enabled"`
	Labels   map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Tags     []string          `yaml:"tags,omitempty" json:"tags,omitempty"`
	Auth     TargetAuth        `yaml:"auth,omitempty" json:"auth,omitempty"`
	Metadata map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// DefaultConfig returns conservative defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:      true,
		HeartbeatTTL: 90 * time.Second,
	}
}

// TargetAuth documents how the controller is expected to reach a known collector.
// It is descriptive metadata, not a secret-bearing runtime transport config.
type TargetAuth struct {
	Mode        string `yaml:"mode,omitempty" json:"mode,omitempty"`
	ServerName  string `yaml:"server_name,omitempty" json:"server_name,omitempty"`
	TokenEnv    string `yaml:"token_env,omitempty" json:"token_env,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}
