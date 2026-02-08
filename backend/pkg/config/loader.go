package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config holds the complete agent configuration
type Config struct {
	Server     ServerConfig     `yaml:"server" mapstructure:"server"`
	Logging    LoggingConfig    `yaml:"logging" mapstructure:"logging"`
	Monitoring MonitoringConfig `yaml:"monitoring" mapstructure:"monitoring"`
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Host        string `yaml:"host" mapstructure:"host"`
	Port        int    `yaml:"port" mapstructure:"port"`
	MetricsPort int    `yaml:"metrics_port" mapstructure:"metrics_port"`
	EnablePprof bool   `yaml:"enable_pprof" mapstructure:"enable_pprof"`
	WebPath     string `yaml:"web_path" mapstructure:"web_path"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level" mapstructure:"level"`
	Format string `yaml:"format" mapstructure:"format"`
	Output string `yaml:"output" mapstructure:"output"`
}

// MonitoringConfig holds monitoring configuration (delegated to internal types)
type MonitoringConfig map[string]interface{}

// Loader loads and manages configuration
type Loader struct {
	v      *viper.Viper
	config *Config
	logger *zap.Logger
}

// NewLoader creates a new configuration loader
func NewLoader(logger *zap.Logger) *Loader {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Loader{
		v:      viper.New(),
		logger: logger.With(zap.String("component", "config_loader")),
	}
}

// Load loads the configuration from file and environment
func (l *Loader) Load(configPath string) (*Config, error) {
	l.v.SetConfigName("default")
	l.v.SetConfigType("yaml")

	// Set default paths
	if configPath != "" {
		l.v.SetConfigFile(configPath)
	} else {
		// Try default locations
		l.v.AddConfigPath(".")
		l.v.AddConfigPath("./config")
		l.v.AddConfigPath("/etc/sre-collector")
	}

	// Read environment variables
	l.v.SetEnvPrefix("SRE_AGENT")
	l.v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	l.v.AutomaticEnv()

	// Read config file
	if err := l.v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		l.logger.Info("no config file found, using defaults")
	} else {
		l.logger.Info("loaded config file", zap.String("file", l.v.ConfigFileUsed()))
	}

	// Unmarshal config
	config := &Config{}
	if err := l.v.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	l.config = config
	return config, nil
}

// LoadFromEnv loads configuration from environment variables only
func (l *Loader) LoadFromEnv() (*Config, error) {
	l.v.SetEnvPrefix("SRE_AGENT")
	l.v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	l.v.AutomaticEnv()

	config := &Config{}
	if err := l.v.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	l.config = config
	return config, nil
}

// Get returns the loaded configuration
func (l *Loader) Get() *Config {
	return l.config
}

// Override overrides a config value
func (l *Loader) Override(key string, value interface{}) {
	l.v.Set(key, value)
	if l.config != nil {
		if err := l.v.Unmarshal(l.config); err != nil {
			l.logger.Warn("failed to update config after override",
				zap.String("key", key),
				zap.Error(err))
		}
	}
}

// MustLoad loads config or panics
func MustLoad(configPath string, logger *zap.Logger) *Config {
	loader := NewLoader(logger)
	config, err := loader.Load(configPath)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
	return config
}

// GetDefaultConfigPath returns the default config file path
func GetDefaultConfigPath() string {
	// Check common locations
	paths := []string{
		"./config/default.yaml",
		"/etc/sre-collector/config.yaml",
		"./default.yaml",
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

// GetSLOConfigPath returns the SLO config directory path
func GetSLOConfigPath() string {
	if path := os.Getenv("SRE_AGENT_SLO_CONFIG_PATH"); path != "" {
		return path
	}

	// Check common locations
	paths := []string{
		"./config/slo",
		"/etc/sre-collector/slo",
	}

	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}

	return ""
}

// LoadSLOConfigs loads all SLO configuration files from a directory
func LoadSLOConfigs(dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("SLO config directory not specified")
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("failed to glob SLO configs: %w", err)
	}

	return files, nil
}
