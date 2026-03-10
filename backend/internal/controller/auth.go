package controller

import (
	"os"

	"go.uber.org/zap"
)

// AuthConfig controls API authentication for the controller.
// API keys must always be provided via environment variables.
type AuthConfig struct {
	Enabled   bool   `yaml:"enabled"`
	APIKeyEnv string `yaml:"api_key_env"`
}

const defaultAPIKeyEnv = "SRE_AGENT_CONTROLLER_API_KEY"

// DefaultAuthConfig returns the default auth configuration.
func DefaultAuthConfig() AuthConfig {
	return AuthConfig{
		Enabled:   false,
		APIKeyEnv: defaultAPIKeyEnv,
	}
}

// ResolveAPIKey loads the API key from environment variables.
func ResolveAPIKey(cfg AuthConfig, logger *zap.Logger) string {
	if !cfg.Enabled {
		return ""
	}

	envVar := cfg.APIKeyEnv
	if envVar == "" {
		envVar = defaultAPIKeyEnv
	}

	apiKey := os.Getenv(envVar)
	if apiKey == "" {
		if logger != nil {
			logger.Warn("api key authentication enabled but env var is empty",
				zap.String("env_var", envVar))
		}
		return ""
	}

	return apiKey
}
