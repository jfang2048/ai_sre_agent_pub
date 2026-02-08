package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigAppliesFileAndEnvOverrides(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_TOPK", "7")
	t.Setenv("SRE_COLLECTOR_TLS_ENABLED", "true")
	t.Setenv("SRE_COLLECTOR_TLS_CA_FILE", "/tmp/ca.crt")
	t.Setenv("SRE_COLLECTOR_TLS_INSECURE_SKIP_VERIFY", "true")

	configFile := filepath.Join(t.TempDir(), "collector.yaml")
	yaml := `
collection_interval: 3s
controller_endpoints:
  - "127.0.0.1:9090"
spool_dir: "` + filepath.Join(t.TempDir(), "spool") + `"
spool_max_bytes: 4096
topk: 3
level: 2
transport:
  dial_timeout: 2s
  rpc_timeout: 4s
`
	require.NoError(t, os.WriteFile(configFile, []byte(yaml), 0o644))

	cfg, err := LoadConfig(configFile)
	require.NoError(t, err)
	require.Equal(t, 7, cfg.TopK) // env should win over file
	require.Equal(t, []string{"127.0.0.1:9090"}, cfg.ControllerEndpoints)
	require.Equal(t, 3*time.Second, cfg.CollectionInterval)
	require.True(t, cfg.Transport.TLS.Enabled)
	require.True(t, cfg.Transport.TLS.InsecureSkipVerify)
	require.Equal(t, "/tmp/ca.crt", cfg.Transport.TLS.CAFile)
}

func TestLoadConfigRejectsInvalidEndpoint(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "collector.yaml")
	yaml := `
controller_endpoints:
  - "invalid-endpoint"
spool_dir: "` + filepath.Join(t.TempDir(), "spool") + `"
level: 2
`
	require.NoError(t, os.WriteFile(configFile, []byte(yaml), 0o644))

	_, err := LoadConfig(configFile)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid controller endpoint")
}

func TestLoadConfigRejectsInvalidEnvValue(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_GRPC_RPC_TIMEOUT", "definitely-not-a-duration")
	_, err := LoadConfig("")
	require.Error(t, err)
	require.ErrorContains(t, err, "SRE_COLLECTOR_GRPC_RPC_TIMEOUT")
}

func FuzzSplitCSV(f *testing.F) {
	f.Add("a,b,c")
	f.Add(" a , b , c ")
	f.Add("")
	f.Add(",,,")

	f.Fuzz(func(t *testing.T, in string) {
		out := splitCSV(in)
		for _, value := range out {
			require.NotEmpty(t, strings.TrimSpace(value))
		}
	})
}
