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

func TestLoadConfigRejectsExternalMetricsShellControlOperators(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_EXT_METRICS_CMD", "echo ok; cat /etc/passwd")
	_, err := LoadConfig("")
	require.Error(t, err)
	require.ErrorContains(t, err, "external_metrics_cmd")
	require.ErrorContains(t, err, "shell control operators")
}

func TestLoadConfigAppliesProbeCoreEnvOverrides(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_ENABLED", "true")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_BINARY_PATH", "/usr/local/bin/sre-probe-core")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_COLLECTORS", "host,network,rdma,process")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_ARGS", "--foo=1,--bar=2")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_COMPRESSION", "gzip")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_QUEUE_DEPTH", "32")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_WINDOW_SAMPLES", "8")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_GPU_INTERVAL_SAMPLES", "3")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_STARTUP_TIMEOUT", "4s")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_STALE_AFTER", "12s")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_FRAME_MAX_BYTES", "1048576")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_FALLBACK_TO_GO", "false")

	cfg, err := LoadConfig("")
	require.NoError(t, err)
	require.True(t, cfg.ProbeCore.Enabled)
	require.Equal(t, "/usr/local/bin/sre-probe-core", cfg.ProbeCore.BinaryPath)
	require.Equal(t, []string{"host", "network", "rdma", "process"}, cfg.ProbeCore.Collectors)
	require.Equal(t, []string{"--foo=1", "--bar=2"}, cfg.ProbeCore.Args)
	require.Equal(t, "gzip", cfg.ProbeCore.Compression)
	require.Equal(t, 32, cfg.ProbeCore.QueueDepth)
	require.Equal(t, 8, cfg.ProbeCore.WindowSamples)
	require.Equal(t, 3, cfg.ProbeCore.GPUIntervalSamples)
	require.Equal(t, 4*time.Second, cfg.ProbeCore.StartupTimeout)
	require.Equal(t, 12*time.Second, cfg.ProbeCore.StaleAfter)
	require.Equal(t, 1048576, cfg.ProbeCore.FrameMaxBytes)
	require.False(t, cfg.ProbeCore.FallbackToGo)
}

func TestLoadConfigRejectsInvalidProbeCoreCollectors(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_ENABLED", "true")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_COLLECTORS", "host,not-real")
	_, err := LoadConfig("")
	require.Error(t, err)
	require.ErrorContains(t, err, "probe_core.collectors")
}

func TestLoadConfigAcceptsProbeCoreCollectorsAll(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_ENABLED", "true")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_COLLECTORS", "all")
	cfg, err := LoadConfig("")
	require.NoError(t, err)
	require.Equal(t, []string{"all"}, cfg.ProbeCore.Collectors)
}

func TestLoadConfigRejectsProbeCoreCollectorsArgsConflict(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_ENABLED", "true")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_COLLECTORS", "host,network")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_ARGS", "--collectors,host")
	_, err := LoadConfig("")
	require.Error(t, err)
	require.ErrorContains(t, err, "probe_core.collectors conflicts")
}

func TestLoadConfigRejectsInvalidProbeCoreCompression(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_ENABLED", "true")
	t.Setenv("SRE_COLLECTOR_PROBE_CORE_COMPRESSION", "brotli")
	_, err := LoadConfig("")
	require.Error(t, err)
	require.ErrorContains(t, err, "probe_core.compression")
}

func TestDefaultConfigUsesProbeCoreAsPrimaryPath(t *testing.T) {
	cfg := DefaultConfig()
	require.True(t, cfg.ProbeCore.Enabled)
	require.True(t, cfg.EBPF.Enabled)
	require.Equal(t, defaultEBPFSock, cfg.EBPF.SocketPath)
	require.Equal(t, defaultProbeCoreBinaryPath, cfg.ProbeCore.BinaryPath)
	require.Equal(t, []string{"--host-mode", "auto"}, cfg.ProbeCore.Args)
	require.True(t, cfg.ProbeCore.FallbackToGo)
	require.True(t, cfg.Security.Enabled)
	require.Equal(t, 5*time.Minute, cfg.Security.AuditInterval)
	require.Equal(t, 128, cfg.Security.RecentEventLimit)
}

func TestLoadConfigHonorsExplicitEBPFDisable(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_EBPF_ENABLED", "false")

	cfg, err := LoadConfig("")
	require.NoError(t, err)
	require.False(t, cfg.EBPF.Enabled)
}

func TestLoadConfigAppliesSecurityEnvOverrides(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_SECURITY_ENABLED", "true")
	t.Setenv("SRE_COLLECTOR_SECURITY_AUDIT_INTERVAL", "7m")
	t.Setenv("SRE_COLLECTOR_SECURITY_RECENT_EVENT_LIMIT", "64")
	t.Setenv("SRE_COLLECTOR_SECURITY_BASELINE_WARMUP_SAMPLES", "5")
	t.Setenv("SRE_COLLECTOR_SECURITY_MAX_WALK_ENTRIES", "1234")
	t.Setenv("SRE_COLLECTOR_SECURITY_LARGE_FILE_THRESHOLD_BYTES", "2097152")
	t.Setenv("SRE_COLLECTOR_SECURITY_RAPID_GROWTH_THRESHOLD_BYTES", "65536")

	cfg, err := LoadConfig("")
	require.NoError(t, err)
	require.True(t, cfg.Security.Enabled)
	require.Equal(t, 7*time.Minute, cfg.Security.AuditInterval)
	require.Equal(t, 64, cfg.Security.RecentEventLimit)
	require.Equal(t, 5, cfg.Security.BaselineWarmupSamples)
	require.Equal(t, 1234, cfg.Security.MaxWalkEntries)
	require.EqualValues(t, 2097152, cfg.Security.LargeFileThresholdBytes)
	require.EqualValues(t, 65536, cfg.Security.RapidGrowthThresholdB)
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
