package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLoadUsesEnvConfigPathWhenNoArgument(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
	configYAML := `server:
  host: "127.0.0.1"
  port: 19099
  metrics_port: 19100
logging:
  level: "debug"
  format: "text"
monitoring: {}
`
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0o644))
	t.Setenv(envConfigPathKey, configPath)

	loader := NewLoader(zap.NewNop())
	cfg, err := loader.Load("")
	require.NoError(t, err)
	require.Equal(t, 19099, cfg.Server.Port)
	require.Equal(t, 19100, cfg.Server.MetricsPort)
	require.Equal(t, "debug", cfg.Logging.Level)
	require.Equal(t, "text", cfg.Logging.Format)
}

func TestGetDefaultConfigPathFindsConfigsDirectory(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		_ = os.Chdir(wd)
	}()

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "configs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "configs", "default.yaml"), []byte("server: {}\n"), 0o644))
	require.NoError(t, os.Chdir(tmp))

	require.Equal(t, "./configs/default.yaml", GetDefaultConfigPath())
}

func TestGetSLOConfigPathFindsConfigsDirectory(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		_ = os.Chdir(wd)
	}()

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "configs", "slo"), 0o755))
	require.NoError(t, os.Chdir(tmp))

	require.Equal(t, "./configs/slo", GetSLOConfigPath())
}
