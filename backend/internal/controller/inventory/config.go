package inventory

import "time"

// Config controls probe inventory behavior.
type Config struct {
	Enabled      bool          `yaml:"enabled" json:"enabled"`
	HeartbeatTTL time.Duration `yaml:"heartbeat_ttl" json:"heartbeat_ttl"`
}

// StaticProbe configures one statically known probe.
type StaticProbe struct {
	ID      string            `yaml:"id" json:"id"`
	Name    string            `yaml:"name" json:"name"`
	Address string            `yaml:"address" json:"address"`
	Labels  map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// DefaultConfig returns conservative defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:      true,
		HeartbeatTTL: 90 * time.Second,
	}
}
