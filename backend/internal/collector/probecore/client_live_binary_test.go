package probecore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLiveProbeCoreBinaryEmitsProcessSchedulerAndBlockIODelayMetrics(t *testing.T) {
	binaryPath := resolveLiveProbeCoreBinaryPath(t)

	client, err := NewClient(Config{
		BinaryPath:         binaryPath,
		Collectors:         []string{"host", "process"},
		Interval:           250 * time.Millisecond,
		TopK:               16,
		WindowSamples:      6,
		QueueDepth:         16,
		Compression:        "none",
		GPUIntervalSamples: 5,
		StartupTimeout:     6 * time.Second,
		StaleAfter:         3 * time.Second,
		FrameMaxBytes:      8 * 1024 * 1024,
	}, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, client.Start(ctx))
	defer client.Stop()

	deadline := time.Now().Add(6 * time.Second)
	var metricNames map[string]struct{}
	for time.Now().Before(deadline) {
		batch, ok := client.Latest(3 * time.Second)
		if !ok || batch == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		metricNames = make(map[string]struct{}, len(batch.GetMetrics()))
		for _, metric := range batch.GetMetrics() {
			if metric == nil {
				continue
			}
			metricNames[metric.GetName()] = struct{}{}
		}
		if batch.GetSequence() >= 2 &&
			hasMetricName(metricNames, "probe_core_process_sched_wait_ratio") &&
			hasMetricName(metricNames, "probe_core_process_block_io_delay_seconds_total") &&
			hasMetricName(metricNames, "probe_core_process_socket_connections") {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}

	require.NotNil(t, metricNames, "live probe-core batch was never observed")
	require.Contains(t, metricNames, "probe_core_process_sched_wait_ratio")
	require.Contains(t, metricNames, "probe_core_process_block_io_delay_seconds_total")
	require.Contains(t, metricNames, "probe_core_process_socket_connections")
}

func hasMetricName(names map[string]struct{}, name string) bool {
	_, ok := names[name]
	return ok
}

func resolveLiveProbeCoreBinaryPath(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("SRE_PROBE_CORE_BINARY_PATH"); path != "" {
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			return path
		}
		t.Skipf("SRE_PROBE_CORE_BINARY_PATH is set but not usable: %s", path)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("unable to resolve working directory: %v", err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "build", "sre-probe-core")
		if stat, statErr := os.Stat(candidate); statErr == nil && !stat.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("live probe-core binary not found; build via `make build-probe-core` or set SRE_PROBE_CORE_BINARY_PATH")
	return ""
}
