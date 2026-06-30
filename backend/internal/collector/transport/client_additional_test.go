package transport

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeConfig(t *testing.T) {
	testCases := []struct {
		name     string
		input    Config
		validate func(Config)
	}{
		{
			name:  "default timeouts",
			input: Config{Endpoints: []string{"localhost:9090"}},
			validate: func(cfg Config) {
				require.Equal(t, defaultDialTimeout, cfg.DialTimeout)
				require.Equal(t, defaultRPCTimeout, cfg.RPCTimeout)
				require.Equal(t, defaultTLSReloadFreq, cfg.TLS.ReloadInterval)
			},
		},
		{
			name: "custom timeouts preserved",
			input: Config{
				Endpoints:   []string{"localhost:9090"},
				DialTimeout: 5 * time.Second,
				RPCTimeout:  30 * time.Second,
				TLS:         TLSConfig{ReloadInterval: 60 * time.Second},
			},
			validate: func(cfg Config) {
				require.Equal(t, 5*time.Second, cfg.DialTimeout)
				require.Equal(t, 30*time.Second, cfg.RPCTimeout)
				require.Equal(t, 60*time.Second, cfg.TLS.ReloadInterval)
			},
		},
		{
			name: "zero timeouts become defaults",
			input: Config{
				Endpoints:   []string{"localhost:9090"},
				DialTimeout: 0,
				RPCTimeout:  0,
			},
			validate: func(cfg Config) {
				require.Equal(t, defaultDialTimeout, cfg.DialTimeout)
				require.Equal(t, defaultRPCTimeout, cfg.RPCTimeout)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := normalizeConfig(tc.input)
			tc.validate(cfg)
		})
	}
}

func TestNormalizeEndpoints(t *testing.T) {
	testCases := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "single endpoint",
			input:    []string{"localhost:9090"},
			expected: []string{"localhost:9090"},
		},
		{
			name:     "multiple endpoints",
			input:    []string{"localhost:9090", "remote:9090", "backup:9090"},
			expected: []string{"localhost:9090", "remote:9090", "backup:9090"},
		},
		{
			name:     "removes empty strings",
			input:    []string{"localhost:9090", "", "remote:9090"},
			expected: []string{"localhost:9090", "remote:9090"},
		},
		{
			name:     "trims whitespace",
			input:    []string{"  localhost:9090  ", "\tremote:9090\n"},
			expected: []string{"localhost:9090", "remote:9090"},
		},
		{
			name:     "deduplicates",
			input:    []string{"localhost:9090", "localhost:9090", "remote:9090"},
			expected: []string{"localhost:9090", "remote:9090"},
		},
		{
			name:     "empty input",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "all empty strings",
			input:    []string{"", "", ""},
			expected: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeEndpoints(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestErrorError(t *testing.T) {
	testCases := []struct {
		name     string
		err      *Error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "<nil>",
		},
		{
			name:     "error without endpoint",
			err:      &Error{Kind: ErrorKindConfig, Err: errors.New("config failed")},
			expected: "transport config error: config failed",
		},
		{
			name:     "error with endpoint",
			err:      &Error{Kind: ErrorKindDial, Endpoint: "localhost:9090", Attempt: 1, Err: errors.New("connection refused")},
			expected: "transport dial error (endpoint=localhost:9090, attempt=1): connection refused",
		},
		{
			name:     "error with all fields",
			err:      &Error{Kind: ErrorKindSend, Endpoint: "remote:9090", Attempt: 3, Err: errors.New("timeout")},
			expected: "transport send error (endpoint=remote:9090, attempt=3): timeout",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.err.Error()
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestErrorUnwrap(t *testing.T) {
	originalErr := errors.New("original error")
	transportErr := &Error{
		Kind: ErrorKindReceive,
		Err:  originalErr,
	}

	unwrapped := errors.Unwrap(transportErr)
	require.Equal(t, originalErr, unwrapped)
}

func TestValidateBatch(t *testing.T) {
	testCases := []struct {
		name    string
		batch   *telemetryv1.TelemetryBatch
		wantErr bool
	}{
		{
			name:    "nil batch",
			batch:   nil,
			wantErr: true,
		},
		{
			name:    "empty batch id",
			batch:   &telemetryv1.TelemetryBatch{BatchId: "  ", Collector: &telemetryv1.CollectorInfo{CollectorId: "test"}},
			wantErr: true,
		},
		{
			name:    "missing batch id",
			batch:   &telemetryv1.TelemetryBatch{Collector: &telemetryv1.CollectorInfo{CollectorId: "test"}},
			wantErr: true,
		},
		{
			name:    "nil collector",
			batch:   &telemetryv1.TelemetryBatch{BatchId: "batch-1"},
			wantErr: true,
		},
		{
			name:    "empty collector id",
			batch:   &telemetryv1.TelemetryBatch{BatchId: "batch-1", Collector: &telemetryv1.CollectorInfo{CollectorId: "  "}},
			wantErr: true,
		},
		{
			name:    "valid batch",
			batch:   &telemetryv1.TelemetryBatch{BatchId: "batch-1", Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-1"}},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBatch(tc.batch)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDecodeBatchPayload(t *testing.T) {
	batch := &telemetryv1.TelemetryBatch{
		BatchId:   "test-batch-1",
		Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-1"},
		Metrics:   []*telemetryv1.Metric{{Name: "cpu", Value: 80.0}},
	}

	payload, err := proto.Marshal(batch)
	require.NoError(t, err)

	decoded, err := decodeBatchPayload(payload)
	require.NoError(t, err)
	require.Equal(t, "test-batch-1", decoded.BatchId)
	require.Equal(t, "collector-1", decoded.Collector.CollectorId)
}

func TestDialReusesCachedConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	server := grpc.NewServer()
	defer server.Stop()
	go server.Serve(listener)

	client, err := New(Config{Endpoints: []string{listener.Addr().String()}, AllowPlaintext: true}, zap.NewNop())
	require.NoError(t, err)

	conn1, err := client.dial(context.Background(), listener.Addr().String())
	require.NoError(t, err)
	conn2, err := client.dial(context.Background(), listener.Addr().String())
	require.NoError(t, err)
	require.Same(t, conn1, conn2)
	require.NoError(t, client.Close())
}

func TestDrainWithOptionsStopsAfterBudget(t *testing.T) {
	client, err := New(Config{Endpoints: []string{"controller:9090"}, AllowPlaintext: true}, zap.NewNop())
	require.NoError(t, err)

	sp := newTestSpoolWithBatches(t,
		&telemetryv1.TelemetryBatch{
			BatchId:   "first-batch",
			Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-a"},
		},
		&telemetryv1.TelemetryBatch{
			BatchId:   "second-batch",
			Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-a"},
		},
	)

	attempted := 0
	err = client.DrainWithOptions(context.Background(), sp, func(payload []byte) (string, error) {
		attempted++
		var batch telemetryv1.TelemetryBatch
		require.NoError(t, proto.Unmarshal(payload, &batch))
		return batch.GetBatchId(), nil
	}, DrainOptions{MaxRecords: 1})
	require.NoError(t, err)
	require.Equal(t, 1, attempted)

	payload, _, err := sp.Next()
	require.NoError(t, err)
	require.NotNil(t, payload)
}

func TestClientStatsUpdate(t *testing.T) {
	var stats clientStats

	stats.update("localhost:9090", 100*time.Millisecond, 50*time.Millisecond, true)

	require.Equal(t, "localhost:9090", stats.lastEndpointValue())
	require.Equal(t, 100.0, stats.lastSendMsValue())
	require.Equal(t, 50.0, stats.lastAckMsValue())
	require.True(t, stats.lastCompressedValue())
}

func TestClientStatsBumpErr(t *testing.T) {
	var stats clientStats

	stats.bumpErr(&Error{Kind: ErrorKindDial})
	require.Equal(t, uint64(1), stats.lastErrsValue())
	require.Equal(t, "dial", stats.lastErrorKindValue())

	stats.bumpErr(errors.New("unknown"))
	require.Equal(t, uint64(2), stats.lastErrsValue())
	require.Equal(t, "unknown", stats.lastErrorKindValue())
}

func TestClientStatsBumpRetry(t *testing.T) {
	var stats clientStats

	stats.bumpRetry(1)
	require.Equal(t, uint64(1), stats.lastRetriesValue())

	stats.bumpRetry(5)
	require.Equal(t, uint64(6), stats.lastRetriesValue())
}

func TestClientDrain(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}, AllowPlaintext: true}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	tempDir := t.TempDir()
	sp, err := spool.New(tempDir, 1024*1024)
	require.NoError(t, err)

	batch := &telemetryv1.TelemetryBatch{
		BatchId:   "test-batch",
		Collector: &telemetryv1.CollectorInfo{CollectorId: "test"},
	}
	payload, _ := proto.Marshal(batch)
	err = sp.Enqueue(payload)
	require.NoError(t, err)

	sendCount := 0
	sendFunc := func(data []byte) (string, error) {
		sendCount++
		return "ack-batch-id", nil
	}

	ctx := context.Background()
	err = client.Drain(ctx, sp, sendFunc)
	require.NoError(t, err)
	require.Equal(t, 1, sendCount)
}

func TestClientDrainCanceledContext(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}, AllowPlaintext: true}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	tempDir := t.TempDir()
	sp, err := spool.New(tempDir, 1024*1024)
	require.NoError(t, err)

	batch := &telemetryv1.TelemetryBatch{
		BatchId:   "test-batch",
		Collector: &telemetryv1.CollectorInfo{CollectorId: "test"},
	}
	payload, _ := proto.Marshal(batch)
	sp.Enqueue(payload)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = client.Drain(ctx, sp, func([]byte) (string, error) { return "", nil })
	require.Error(t, err)
	require.Contains(t, err.Error(), "drain aborted")
}

func TestSendCanceledContext(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}, AllowPlaintext: true}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.Send(ctx, []byte("test"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "context canceled")
}

func TestApplyConfig(t *testing.T) {
	cfg := Config{
		Endpoints:      []string{"localhost:9090"},
		DialTimeout:    5 * time.Second,
		AllowPlaintext: true,
	}

	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	newCfg := Config{
		Endpoints:      []string{"remote:9090", "backup:9090"},
		DialTimeout:    10 * time.Second,
		Mirror:         true,
		AllowPlaintext: true,
	}

	err = client.ApplyConfig(newCfg)
	require.NoError(t, err)

	snapshotCfg, _ := client.snapshotConfigAndOrder()
	require.Equal(t, 2, len(snapshotCfg.Endpoints))
	require.Equal(t, "remote:9090", snapshotCfg.Endpoints[0])
	require.True(t, snapshotCfg.Mirror)
}

func TestApplyConfigEmptyEndpoints(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}, AllowPlaintext: true}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	err = client.ApplyConfig(Config{Endpoints: []string{}, AllowPlaintext: true})
	require.Error(t, err)
}

func TestSnapshotConfig(t *testing.T) {
	cfg := Config{
		Endpoints:      []string{"localhost:9090"},
		DialTimeout:    15 * time.Second,
		RPCTimeout:     20 * time.Second,
		Compress:       true,
		AllowPlaintext: true,
	}

	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	snapshot := client.snapshotConfig()
	require.Equal(t, 15*time.Second, snapshot.DialTimeout)
	require.Equal(t, 20*time.Second, snapshot.RPCTimeout)
	require.True(t, snapshot.Compress)
}

func TestSnapshotConfigAndOrder(t *testing.T) {
	cfg := Config{
		Endpoints:      []string{"a:9090", "b:9090", "c:9090"},
		AllowPlaintext: true,
	}

	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	_, endpoints1 := client.snapshotConfigAndOrder()
	require.Equal(t, []string{"a:9090", "b:9090", "c:9090"}, endpoints1)

	_, endpoints2 := client.snapshotConfigAndOrder()
	require.Equal(t, []string{"b:9090", "c:9090", "a:9090"}, endpoints2)

	_, endpoints3 := client.snapshotConfigAndOrder()
	require.Equal(t, []string{"c:9090", "a:9090", "b:9090"}, endpoints3)

	_, endpoints4 := client.snapshotConfigAndOrder()
	require.Equal(t, []string{"a:9090", "b:9090", "c:9090"}, endpoints4)
}

func TestSnapshotConfigAndOrderMirror(t *testing.T) {
	cfg := Config{
		Endpoints:      []string{"a:9090", "b:9090"},
		Mirror:         true,
		AllowPlaintext: true,
	}

	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	_, endpoints1 := client.snapshotConfigAndOrder()
	_, endpoints2 := client.snapshotConfigAndOrder()
	_, endpoints3 := client.snapshotConfigAndOrder()

	require.Equal(t, endpoints1, endpoints2)
	require.Equal(t, endpoints2, endpoints3)
	require.Equal(t, []string{"a:9090", "b:9090"}, endpoints1)
}

func TestNewClientValidation(t *testing.T) {
	testCases := []struct {
		name        string
		cfg         Config
		expectError bool
	}{
		{
			name: "valid config",
			cfg: Config{
				Endpoints:      []string{"localhost:9090"},
				AllowPlaintext: true,
			},
			expectError: false,
		},
		{
			name:        "empty endpoints",
			cfg:         Config{Endpoints: []string{}, AllowPlaintext: true},
			expectError: true,
		},
		{
			name:        "nil logger uses nop",
			cfg:         Config{Endpoints: []string{"localhost:9090"}, AllowPlaintext: true},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var logger *zap.Logger
			if tc.name == "nil logger uses nop" {
				logger = nil
			} else {
				logger = zap.NewNop()
			}

			client, err := New(tc.cfg, logger)

			if tc.expectError {
				require.Error(t, err)
				require.Nil(t, client)
			} else {
				require.NoError(t, err)
				require.NotNil(t, client)
			}
		})
	}
}

func TestStatsAccessors(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}, AllowPlaintext: true}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	require.Equal(t, 0.0, client.LastSendMs())
	require.Equal(t, 0.0, client.LastAckMs())
	require.Equal(t, uint64(0), client.LastErrs())
	require.Equal(t, uint64(0), client.LastRetries())
	require.Equal(t, "", client.LastEndpoint())
	require.False(t, client.LastCompressed())
	require.Equal(t, "", client.LastErrorKind())
}
