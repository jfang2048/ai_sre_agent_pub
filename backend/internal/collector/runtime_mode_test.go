package collector

import (
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

func TestDetectCollectorRuntimeInspectionHostMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RuntimeMode = "auto"

	inspection := detectCollectorRuntimeInspectionWithDeps(cfg, runtimeInspectorDeps{
		stat: func(path string) (os.FileInfo, error) {
			switch path {
			case "/proc/stat", "/proc/meminfo", "/sys/kernel", "/sys/fs/bpf", "/sys/fs/cgroup":
				return fakeFileInfo{name: path}, nil
			default:
				return nil, os.ErrNotExist
			}
		},
		readlink: func(path string) (string, error) {
			return "pid:[4026531836]", nil
		},
		readFile: func(path string) ([]byte, error) {
			switch path {
			case "/proc/self/status":
				return []byte("CapEff:\t" + capEffHex(unix.CAP_BPF) + "\n"), nil
			case "/proc/1/cgroup", "/proc/self/cgroup":
				return []byte("0::/\n"), nil
			default:
				return nil, os.ErrNotExist
			}
		},
	})

	require.Equal(t, runtimeModeHost, inspection.AppliedMode)
	require.Equal(t, "kernel", inspection.ProbeCoreHostMode)
	require.True(t, inspection.CanUseEBPF)
	require.False(t, inspection.Degraded)
}

func TestDetectCollectorRuntimeInspectionFallsBackToNamespace(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RuntimeMode = "host"

	inspection := detectCollectorRuntimeInspectionWithDeps(cfg, runtimeInspectorDeps{
		stat: func(path string) (os.FileInfo, error) {
			switch path {
			case "/proc/stat", "/proc/meminfo", "/sys/fs/cgroup", "/.dockerenv":
				return fakeFileInfo{name: path}, nil
			default:
				return nil, os.ErrNotExist
			}
		},
		readlink: func(path string) (string, error) {
			if path == "/proc/self/ns/pid" {
				return "pid:[4026533000]", nil
			}
			return "pid:[4026531836]", nil
		},
		readFile: func(path string) ([]byte, error) {
			switch path {
			case "/proc/self/status":
				return []byte("CapEff:\t0000000000000000\n"), nil
			case "/proc/1/cgroup", "/proc/self/cgroup":
				return []byte("0::/kubepods.slice/pod123\n"), nil
			default:
				return nil, os.ErrNotExist
			}
		},
	})

	require.Equal(t, runtimeModeNamespace, inspection.AppliedMode)
	require.Equal(t, "proc", inspection.ProbeCoreHostMode)
	require.True(t, inspection.Containerized)
	require.False(t, inspection.CanUseEBPF)
	require.True(t, inspection.Degraded)
	require.Contains(t, inspection.Reasons, "requested_host_mode_unavailable")
	require.Contains(t, inspection.Reasons, "host_pid_namespace_unavailable")
}

func TestApplyProbeCoreHostModeOnlyOverridesAuto(t *testing.T) {
	hostInspection := collectorRuntimeInspection{ProbeCoreHostMode: "kernel"}
	require.Equal(t,
		[]string{"--host-mode", "kernel"},
		applyProbeCoreHostMode([]string{"--host-mode", "auto"}, hostInspection),
	)
	require.Equal(t,
		[]string{"--host-mode", "proc"},
		applyProbeCoreHostMode(nil, collectorRuntimeInspection{ProbeCoreHostMode: "proc"}),
	)
	require.Equal(t,
		[]string{"--host-mode", "kernel"},
		applyProbeCoreHostMode([]string{"--host-mode", "kernel"}, collectorRuntimeInspection{ProbeCoreHostMode: "proc"}),
	)
}

func TestAppendRuntimeModeMetricsEmitsCoverageAndReasons(t *testing.T) {
	collector := &Collector{
		runtimeMode: collectorRuntimeInspection{
			RequestedMode:       runtimeModeHost,
			AppliedMode:         runtimeModeNamespace,
			ProbeCoreHostMode:   "proc",
			Containerized:       true,
			HostPIDNamespace:    false,
			ProcVisible:         true,
			CgroupVisible:       true,
			KernelMountsVisible: false,
			BPFFSVisible:        false,
			CAPBPF:              false,
			CAPSysAdmin:         false,
			CanUseEBPF:          false,
			Degraded:            true,
			Reasons:             []string{"requested_host_mode_unavailable", "bpf_capability_unavailable"},
		},
	}
	now := time.Now()
	metrics := make([]*telemetryv1.Metric, 0, 32)

	collector.appendRuntimeModeMetrics(now, &metrics)

	value, ok := metricValueWithLabels(metrics, "collector_runtime_mode", map[string]string{"mode": runtimeModeNamespace})
	require.True(t, ok)
	require.Equal(t, 1.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_runtime_mode_degraded", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_runtime_signal_coverage", map[string]string{"signal": "ebpf"})
	require.True(t, ok)
	require.Equal(t, 0.0, value)

	value, ok = metricValueWithLabels(metrics, "collector_runtime_degraded_reason", map[string]string{"reason": "requested_host_mode_unavailable"})
	require.True(t, ok)
	require.Equal(t, 1.0, value)
}

func TestStartPrimaryEBPFRuntimeSkipsWhenCapabilitiesUnavailable(t *testing.T) {
	runtime := &countingPrimaryEBPFRuntime{}
	collector := &Collector{
		cfg: Config{
			EBPF: EBPFConfig{Enabled: true},
		},
		logger:       zap.NewNop(),
		ebpfRuntime:  runtime,
		ebpfExpected: true,
		runtimeMode: collectorRuntimeInspection{
			AppliedMode: runtimeModeNamespace,
			CanUseEBPF:  false,
		},
	}

	stop := collector.startPrimaryEBPFRuntime()
	require.Nil(t, stop)
	require.Zero(t, runtime.started)

	expected, healthy, reason := collector.ebpfRuntimeStatus()
	require.True(t, expected)
	require.False(t, healthy)
	require.Equal(t, "capability_unavailable", reason)
}

func TestLoadConfigAppliesRuntimeModeEnvOverride(t *testing.T) {
	t.Setenv("SRE_COLLECTOR_RUNTIME_MODE", "namespace")

	cfg, err := LoadConfig("")
	require.NoError(t, err)
	require.Equal(t, runtimeModeNamespace, cfg.RuntimeMode)
}

type fakeFileInfo struct {
	name string
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

type countingPrimaryEBPFRuntime struct {
	started int
}

func (f *countingPrimaryEBPFRuntime) Start() error {
	f.started++
	return errors.New("should not be called")
}

func (f *countingPrimaryEBPFRuntime) Stop() {}

func (f *countingPrimaryEBPFRuntime) GetMetrics(time.Time) []probe.Metric { return nil }

func (f *countingPrimaryEBPFRuntime) Summary() probe.EBPFSummary { return probe.EBPFSummary{} }

func (f *countingPrimaryEBPFRuntime) Events(limit int) []probe.EBPFEvent { return nil }

func capEffHex(capability int) string {
	return strconv.FormatUint(uint64(1)<<uint(capability), 16)
}
