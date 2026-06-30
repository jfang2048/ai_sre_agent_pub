package controller

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

// AuthConfig controls controller HTTP and ingest authentication.
// Secrets and compatibility keys must always be provided via environment variables.
type AuthConfig struct {
	Enabled              bool   `yaml:"enabled"`
	AllowInsecureDisable bool   `yaml:"allow_insecure_disable"`
	Mode                 string `yaml:"mode"`
	TokenSecretEnv       string `yaml:"token_secret_env"`
	TokenIssuer          string `yaml:"token_issuer"`
	TokenAudience        string `yaml:"token_audience"`
	IngestAuthEnabled    bool   `yaml:"ingest_auth_enabled"`
	IngestTokenAudience  string `yaml:"ingest_token_audience"`
	APIKeyEnv            string `yaml:"api_key_env"`
	ReadAPIKeyEnv        string `yaml:"read_api_key_env"`
	ActionAPIKeyEnv      string `yaml:"action_api_key_env"`
}

const (
	defaultAPIKeyEnv      = "SRE_AGENT_CONTROLLER_API_KEY"
	defaultTokenSecretEnv = "SRE_AGENT_CONTROLLER_TOKEN_SECRET"
)

type ControllerAuthMode string

const (
	ControllerAuthModeDisabled ControllerAuthMode = "disabled"
	ControllerAuthModeToken    ControllerAuthMode = "token"
	ControllerAuthModeMixed    ControllerAuthMode = "mixed"
	ControllerAuthModeAPIKey   ControllerAuthMode = "api_key"
)

type ControllerAPIKeyMode string

const (
	ControllerAPIKeyModeDisabled ControllerAPIKeyMode = "disabled"
	ControllerAPIKeyModeShared   ControllerAPIKeyMode = "shared"
	ControllerAPIKeyModeSplit    ControllerAPIKeyMode = "split"
	ControllerAPIKeyModeReadOnly ControllerAPIKeyMode = "read_only"
)

type ResolvedAuthConfig struct {
	Enabled             bool
	Mode                ControllerAuthMode
	APIKeyMode          ControllerAPIKeyMode
	TokenSecret         string
	TokenSecretEnv      string
	TokenIssuer         string
	TokenAudience       string
	IngestAuthEnabled   bool
	IngestTokenAudience string
	ReadKey             string
	ActionKey           string
	ReadKeyEnv          string
	ActionKeyEnv        string
	LegacyAPIKeyEnv     string
	DeploymentMode      string
	InsecureOverride    bool
	LocalDevBypass      bool
}

// DefaultAuthConfig returns the default auth configuration.
func DefaultAuthConfig() AuthConfig {
	return AuthConfig{
		Enabled:             false,
		Mode:                string(ControllerAuthModeToken),
		TokenSecretEnv:      defaultTokenSecretEnv,
		TokenIssuer:         "ai-sre-agent-controller",
		TokenAudience:       "controller-api",
		IngestAuthEnabled:   true,
		IngestTokenAudience: "controller-ingest",
		APIKeyEnv:           defaultAPIKeyEnv,
	}
}

// ResolveAuthConfig loads effective controller auth configuration from environment variables.
func ResolveAuthConfig(cfg AuthConfig, logger *zap.Logger) (ResolvedAuthConfig, error) {
	if !cfg.Enabled {
		return ResolvedAuthConfig{
			Mode:                ControllerAuthModeDisabled,
			APIKeyMode:          ControllerAPIKeyModeDisabled,
			TokenSecretEnv:      normalizeTokenSecretEnv(cfg.TokenSecretEnv),
			TokenIssuer:         defaultTokenIssuer(strings.TrimSpace(cfg.TokenIssuer)),
			TokenAudience:       defaultTokenAudience(strings.TrimSpace(cfg.TokenAudience), "controller-api"),
			IngestTokenAudience: defaultTokenAudience(strings.TrimSpace(cfg.IngestTokenAudience), "controller-ingest"),
		}, nil
	}

	mode := normalizeAuthMode(cfg.Mode)
	if mode == "" {
		return ResolvedAuthConfig{}, fmt.Errorf("controller auth.mode must be one of token, mixed, api_key")
	}

	resolved := ResolvedAuthConfig{
		Enabled:             true,
		Mode:                mode,
		APIKeyMode:          ControllerAPIKeyModeDisabled,
		TokenSecretEnv:      normalizeTokenSecretEnv(cfg.TokenSecretEnv),
		TokenIssuer:         defaultTokenIssuer(strings.TrimSpace(cfg.TokenIssuer)),
		TokenAudience:       defaultTokenAudience(strings.TrimSpace(cfg.TokenAudience), "controller-api"),
		IngestAuthEnabled:   cfg.IngestAuthEnabled,
		IngestTokenAudience: defaultTokenAudience(strings.TrimSpace(cfg.IngestTokenAudience), "controller-ingest"),
	}

	if mode == ControllerAuthModeToken || mode == ControllerAuthModeMixed || resolved.IngestAuthEnabled {
		resolved.TokenSecret = strings.TrimSpace(os.Getenv(resolved.TokenSecretEnv))
		if resolved.TokenSecret == "" {
			return ResolvedAuthConfig{}, fmt.Errorf("controller auth requires token secret env var %q", resolved.TokenSecretEnv)
		}
	}

	if mode == ControllerAuthModeAPIKey || mode == ControllerAuthModeMixed {
		apiKeyMode, apiKeyResolved, err := resolveCompatibilityAPIKeys(cfg, mode == ControllerAuthModeAPIKey, logger)
		if err != nil {
			return ResolvedAuthConfig{}, err
		}
		resolved.APIKeyMode = apiKeyMode
		resolved.ReadKey = apiKeyResolved.ReadKey
		resolved.ActionKey = apiKeyResolved.ActionKey
		resolved.ReadKeyEnv = apiKeyResolved.ReadKeyEnv
		resolved.ActionKeyEnv = apiKeyResolved.ActionKeyEnv
		resolved.LegacyAPIKeyEnv = apiKeyResolved.LegacyAPIKeyEnv
	}

	return resolved, nil
}

func normalizeAuthMode(raw string) ControllerAuthMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ControllerAuthModeToken):
		return ControllerAuthModeToken
	case string(ControllerAuthModeMixed):
		return ControllerAuthModeMixed
	case string(ControllerAuthModeAPIKey):
		return ControllerAuthModeAPIKey
	default:
		return ""
	}
}

func normalizeTokenSecretEnv(raw string) string {
	if value := strings.TrimSpace(raw); value != "" {
		return value
	}
	return defaultTokenSecretEnv
}

func defaultTokenIssuer(raw string) string {
	if value := strings.TrimSpace(raw); value != "" {
		return value
	}
	return "ai-sre-agent-controller"
}

func defaultTokenAudience(raw string, fallback string) string {
	if value := strings.TrimSpace(raw); value != "" {
		return value
	}
	return fallback
}

func resolveCompatibilityAPIKeys(cfg AuthConfig, required bool, logger *zap.Logger) (ControllerAPIKeyMode, ResolvedAuthConfig, error) {
	readEnv := strings.TrimSpace(cfg.ReadAPIKeyEnv)
	actionEnv := strings.TrimSpace(cfg.ActionAPIKeyEnv)
	sharedEnv := strings.TrimSpace(cfg.APIKeyEnv)
	if sharedEnv == "" {
		sharedEnv = defaultAPIKeyEnv
	}

	resolved := ResolvedAuthConfig{
		LegacyAPIKeyEnv: sharedEnv,
	}
	if readEnv == "" && actionEnv == "" {
		sharedKey := strings.TrimSpace(os.Getenv(sharedEnv))
		if sharedKey == "" {
			if required {
				return ControllerAPIKeyModeDisabled, ResolvedAuthConfig{}, fmt.Errorf("controller auth compatibility mode requires API key env var %q", sharedEnv)
			}
			return ControllerAPIKeyModeDisabled, resolved, nil
		}
		resolved.ReadKey = sharedKey
		resolved.ActionKey = sharedKey
		resolved.ReadKeyEnv = sharedEnv
		resolved.ActionKeyEnv = sharedEnv
		return ControllerAPIKeyModeShared, resolved, nil
	}

	if readEnv != "" {
		resolved.ReadKeyEnv = readEnv
		resolved.ReadKey = strings.TrimSpace(os.Getenv(readEnv))
		if resolved.ReadKey == "" {
			return ControllerAPIKeyModeDisabled, ResolvedAuthConfig{}, fmt.Errorf("controller auth compatibility read API key env var %q is empty", readEnv)
		}
	}
	if actionEnv != "" {
		resolved.ActionKeyEnv = actionEnv
		resolved.ActionKey = strings.TrimSpace(os.Getenv(actionEnv))
		if resolved.ActionKey == "" {
			return ControllerAPIKeyModeDisabled, ResolvedAuthConfig{}, fmt.Errorf("controller auth compatibility action API key env var %q is empty", actionEnv)
		}
	}

	switch {
	case resolved.ReadKey != "" && resolved.ActionKey != "":
		return ControllerAPIKeyModeSplit, resolved, nil
	case resolved.ReadKey != "":
		return ControllerAPIKeyModeReadOnly, resolved, nil
	case resolved.ActionKey != "":
		resolved.ReadKey = resolved.ActionKey
		if resolved.ReadKeyEnv == "" {
			resolved.ReadKeyEnv = resolved.ActionKeyEnv
		}
		return ControllerAPIKeyModeShared, resolved, nil
	default:
		if logger != nil {
			logger.Warn("controller auth compatibility mode enabled but no effective api key credentials were resolved",
				zap.String("shared_env", sharedEnv),
				zap.String("read_env", readEnv),
				zap.String("action_env", actionEnv))
		}
		if required {
			return ControllerAPIKeyModeDisabled, ResolvedAuthConfig{}, fmt.Errorf("controller auth compatibility mode is enabled but no API credentials were resolved")
		}
		return ControllerAPIKeyModeDisabled, resolved, nil
	}
}
