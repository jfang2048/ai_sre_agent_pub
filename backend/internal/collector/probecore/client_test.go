package probecore

import (
	"bytes"
	"compress/gzip"
	"hash/crc32"
	"testing"
	"time"

	probeipcv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/probeipc/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

func TestDecodeFrameUncompressed(t *testing.T) {
	client, err := NewClient(Config{
		BinaryPath:         "/bin/echo",
		Interval:           time.Second,
		TopK:               10,
		WindowSamples:      6,
		QueueDepth:         16,
		Compression:        "none",
		GPUIntervalSamples: 5,
		StartupTimeout:     time.Second,
		StaleAfter:         5 * time.Second,
		FrameMaxBytes:      1024 * 1024,
	}, zap.NewNop())
	require.NoError(t, err)

	batch := &probeipcv1.ProbeBatch{
		CollectedAtUnixNano: time.Now().UnixNano(),
		Sequence:            7,
		Metrics: []*probeipcv1.Metric{
			{Name: "probe_core_cpu_usage_percent", Value: 72.5},
		},
	}
	frame := mustFrame(t, batch, probeipcv1.Compression_COMPRESSION_NONE)

	decoded, err := client.decodeFrame(frame)
	require.NoError(t, err)
	require.Equal(t, uint32(7), decoded.GetSequence())
	require.Len(t, decoded.GetMetrics(), 1)
	require.Equal(t, "probe_core_cpu_usage_percent", decoded.GetMetrics()[0].GetName())
}

func TestDecodeFrameGzip(t *testing.T) {
	client, err := NewClient(Config{
		BinaryPath:         "/bin/echo",
		Interval:           time.Second,
		TopK:               10,
		WindowSamples:      6,
		QueueDepth:         16,
		Compression:        "gzip",
		GPUIntervalSamples: 5,
		StartupTimeout:     time.Second,
		StaleAfter:         5 * time.Second,
		FrameMaxBytes:      1024 * 1024,
	}, zap.NewNop())
	require.NoError(t, err)

	batch := &probeipcv1.ProbeBatch{CollectedAtUnixNano: time.Now().UnixNano(), Sequence: 99}
	frame := mustFrame(t, batch, probeipcv1.Compression_COMPRESSION_GZIP)

	decoded, err := client.decodeFrame(frame)
	require.NoError(t, err)
	require.Equal(t, uint32(99), decoded.GetSequence())
}

func TestDecodeFrameCRCMismatch(t *testing.T) {
	client, err := NewClient(Config{
		BinaryPath:         "/bin/echo",
		Interval:           time.Second,
		TopK:               10,
		WindowSamples:      6,
		QueueDepth:         16,
		Compression:        "none",
		GPUIntervalSamples: 5,
		StartupTimeout:     time.Second,
		StaleAfter:         5 * time.Second,
		FrameMaxBytes:      1024 * 1024,
	}, zap.NewNop())
	require.NoError(t, err)

	batch := &probeipcv1.ProbeBatch{CollectedAtUnixNano: time.Now().UnixNano(), Sequence: 1}
	payload, err := proto.Marshal(batch)
	require.NoError(t, err)
	env := &probeipcv1.FrameEnvelope{
		Compression:  probeipcv1.Compression_COMPRESSION_NONE,
		Payload:      payload,
		PayloadCrc32: crc32.ChecksumIEEE(payload) + 1,
	}
	frame, err := proto.Marshal(env)
	require.NoError(t, err)

	_, err = client.decodeFrame(frame)
	require.Error(t, err)
	require.Contains(t, err.Error(), "crc mismatch")
}

func TestBuildArgs(t *testing.T) {
	client, err := NewClient(Config{
		BinaryPath:         "/usr/local/bin/sre-probe-core",
		Collectors:         []string{"process", "host", "rdma", "host", "network"},
		Args:               []string{"--extra-flag", "value"},
		Interval:           250 * time.Millisecond,
		TopK:               17,
		WindowSamples:      8,
		QueueDepth:         32,
		Compression:        "gzip",
		GPUIntervalSamples: 2,
		EBPFSocketPath:     "./data/collector/run/sre_collector_ebpf.sock",
		StartupTimeout:     time.Second,
		StaleAfter:         5 * time.Second,
		FrameMaxBytes:      1024 * 1024,
	}, zap.NewNop())
	require.NoError(t, err)

	args := client.buildArgs()
	require.Contains(t, args, "--interval-ms")
	require.Contains(t, args, "250")
	require.Contains(t, args, "--compression")
	require.Contains(t, args, "gzip")
	require.Contains(t, args, "--collectors")
	require.Contains(t, args, "host,network,rdma,process")
	require.Contains(t, args, "--ebpf-socket")
	require.Contains(t, args, "./data/collector/run/sre_collector_ebpf.sock")
	require.Contains(t, args, "--extra-flag")
}

func TestBuildArgsCollectorsAllOmitsFlag(t *testing.T) {
	client, err := NewClient(Config{
		BinaryPath:         "/usr/local/bin/sre-probe-core",
		Collectors:         []string{"all"},
		Interval:           time.Second,
		TopK:               10,
		WindowSamples:      6,
		QueueDepth:         16,
		Compression:        "none",
		GPUIntervalSamples: 5,
		StartupTimeout:     time.Second,
		StaleAfter:         5 * time.Second,
		FrameMaxBytes:      1024 * 1024,
	}, zap.NewNop())
	require.NoError(t, err)
	args := client.buildArgs()

	for i := 0; i < len(args); i++ {
		require.NotEqual(t, "--collectors", args[i])
	}
}

func TestNewClientRejectsInvalidCollectorModule(t *testing.T) {
	_, err := NewClient(Config{
		BinaryPath:         "/usr/local/bin/sre-probe-core",
		Collectors:         []string{"host", "invalid-module"},
		Interval:           time.Second,
		TopK:               10,
		WindowSamples:      6,
		QueueDepth:         16,
		Compression:        "none",
		GPUIntervalSamples: 5,
		StartupTimeout:     time.Second,
		StaleAfter:         5 * time.Second,
		FrameMaxBytes:      1024 * 1024,
	}, zap.NewNop())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported module")
}

func TestNewClientRejectsCollectorsArgsConflict(t *testing.T) {
	_, err := NewClient(Config{
		BinaryPath:         "/usr/local/bin/sre-probe-core",
		Collectors:         []string{"host", "network"},
		Args:               []string{"--collectors", "host"},
		Interval:           time.Second,
		TopK:               10,
		WindowSamples:      6,
		QueueDepth:         16,
		Compression:        "none",
		GPUIntervalSamples: 5,
		StartupTimeout:     time.Second,
		StaleAfter:         5 * time.Second,
		FrameMaxBytes:      1024 * 1024,
	}, zap.NewNop())
	require.Error(t, err)
	require.Contains(t, err.Error(), "collectors conflicts")
}

func mustFrame(t *testing.T, batch *probeipcv1.ProbeBatch, mode probeipcv1.Compression) []byte {
	t.Helper()
	payload, err := proto.Marshal(batch)
	require.NoError(t, err)
	encoded := payload
	if mode == probeipcv1.Compression_COMPRESSION_GZIP {
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		_, err = zw.Write(payload)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		encoded = compressed.Bytes()
	}
	env := &probeipcv1.FrameEnvelope{
		Compression:  mode,
		Payload:      encoded,
		PayloadCrc32: crc32.ChecksumIEEE(encoded),
	}
	frame, err := proto.Marshal(env)
	require.NoError(t, err)
	return frame
}
