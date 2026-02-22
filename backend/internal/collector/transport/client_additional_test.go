package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// TestNormalizeConfig validates config normalization
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

// TestNormalizeEndpoints validates endpoint normalization
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

// TestErrorError validates Error string formatting
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

// TestErrorUnwrap validates error unwrapping
func TestErrorUnwrap(t *testing.T) {
	originalErr := errors.New("original error")
	transportErr := &Error{
		Kind: ErrorKindReceive,
		Err:  originalErr,
	}

	unwrapped := errors.Unwrap(transportErr)
	require.Equal(t, originalErr, unwrapped)
}

// TestValidateBatch validates batch validation
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

// TestDecodeBatchPayload validates payload decoding
func TestDecodeBatchPayload(t *testing.T) {
	// Create a valid batch
	batch := &telemetryv1.TelemetryBatch{
		BatchId:   "test-batch-1",
		Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-1"},
		Metrics:   []*telemetryv1.Metric{{Name: "cpu", Value: 80.0}},
	}

	payload, err := proto.Marshal(batch)
	require.NoError(t, err)

	// Decode
	decoded, err := decodeBatchPayload(payload)
	require.NoError(t, err)
	require.Equal(t, "test-batch-1", decoded.BatchId)
	require.Equal(t, "collector-1", decoded.Collector.CollectorId)
}

// TestClientStatsUpdate validates stats updates
func TestClientStatsUpdate(t *testing.T) {
	var stats clientStats

	stats.update("localhost:9090", 100*time.Millisecond, 50*time.Millisecond, true)

	require.Equal(t, "localhost:9090", stats.lastEndpointValue())
	require.Equal(t, 100.0, stats.lastSendMsValue())
	require.Equal(t, 50.0, stats.lastAckMsValue())
	require.True(t, stats.lastCompressedValue())
}

// TestClientStatsBumpErr validates error bumping
func TestClientStatsBumpErr(t *testing.T) {
	var stats clientStats

	// Bump with transport error
	stats.bumpErr(&Error{Kind: ErrorKindDial})
	require.Equal(t, uint64(1), stats.lastErrsValue())
	require.Equal(t, "dial", stats.lastErrorKindValue())

	// Bump with unknown error
	stats.bumpErr(errors.New("unknown"))
	require.Equal(t, uint64(2), stats.lastErrsValue())
	require.Equal(t, "unknown", stats.lastErrorKindValue())
}

// TestClientStatsBumpRetry validates retry bumping
func TestClientStatsBumpRetry(t *testing.T) {
	var stats clientStats

	stats.bumpRetry(1)
	require.Equal(t, uint64(1), stats.lastRetriesValue())

	stats.bumpRetry(5)
	require.Equal(t, uint64(6), stats.lastRetriesValue())
}

// TestClientDrain validates spool draining
func TestClientDrain(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	// Create spool with data
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

	// Drain should process the spool
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

// TestClientDrainCanceledContext validates drain cancellation
func TestClientDrainCanceledContext(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}}
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
	cancel() // Cancel immediately

	err = client.Drain(ctx, sp, func([]byte) (string, error) { return "", nil })
	require.Error(t, err)
	require.Contains(t, err.Error(), "drain aborted")
}

// TestSendCanceledContext validates send cancellation
func TestSendCanceledContext(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = client.Send(ctx, []byte("test"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "context canceled")
}

// TestApplyConfig validates config application
func TestApplyConfig(t *testing.T) {
	cfg := Config{
		Endpoints:   []string{"localhost:9090"},
		DialTimeout: 5 * time.Second,
	}

	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	// Apply new config
	newCfg := Config{
		Endpoints:   []string{"remote:9090", "backup:9090"},
		DialTimeout: 10 * time.Second,
		Mirror:      true,
	}

	err = client.ApplyConfig(newCfg)
	require.NoError(t, err)

	// Verify config was updated
	snapshotCfg, _ := client.snapshotConfigAndOrder()
	require.Equal(t, 2, len(snapshotCfg.Endpoints))
	require.Equal(t, "remote:9090", snapshotCfg.Endpoints[0])
	require.True(t, snapshotCfg.Mirror)
}

// TestApplyConfigEmptyEndpoints validates error on empty endpoints
func TestApplyConfigEmptyEndpoints(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	err = client.ApplyConfig(Config{Endpoints: []string{}})
	require.Error(t, err)
}

// TestSnapshotConfig validates config snapshot
func TestSnapshotConfig(t *testing.T) {
	cfg := Config{
		Endpoints:   []string{"localhost:9090"},
		DialTimeout: 15 * time.Second,
		RPCTimeout:  20 * time.Second,
		Compress:    true,
	}

	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	snapshot := client.snapshotConfig()
	require.Equal(t, 15*time.Second, snapshot.DialTimeout)
	require.Equal(t, 20*time.Second, snapshot.RPCTimeout)
	require.True(t, snapshot.Compress)
}

// TestSnapshotConfigAndOrder validates endpoint ordering
func TestSnapshotConfigAndOrder(t *testing.T) {
	cfg := Config{
		Endpoints: []string{"a:9090", "b:9090", "c:9090"},
	}

	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	// First call should start with index 0
	_, endpoints1 := client.snapshotConfigAndOrder()
	require.Equal(t, []string{"a:9090", "b:9090", "c:9090"}, endpoints1)

	// Second call should rotate (index 1)
	_, endpoints2 := client.snapshotConfigAndOrder()
	require.Equal(t, []string{"b:9090", "c:9090", "a:9090"}, endpoints2)

	// Third call should rotate (index 2)
	_, endpoints3 := client.snapshotConfigAndOrder()
	require.Equal(t, []string{"c:9090", "a:9090", "b:9090"}, endpoints3)

	// Fourth call should wrap back to index 0
	_, endpoints4 := client.snapshotConfigAndOrder()
	require.Equal(t, []string{"a:9090", "b:9090", "c:9090"}, endpoints4)
}

// TestSnapshotConfigAndOrderMirror validates mirror mode doesn't rotate
func TestSnapshotConfigAndOrderMirror(t *testing.T) {
	cfg := Config{
		Endpoints: []string{"a:9090", "b:9090"},
		Mirror:    true,
	}

	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	// Multiple calls should return same order in mirror mode
	_, endpoints1 := client.snapshotConfigAndOrder()
	_, endpoints2 := client.snapshotConfigAndOrder()
	_, endpoints3 := client.snapshotConfigAndOrder()

	require.Equal(t, endpoints1, endpoints2)
	require.Equal(t, endpoints2, endpoints3)
	require.Equal(t, []string{"a:9090", "b:9090"}, endpoints1)
}

// TestNewClientValidation validates New with various configs
func TestNewClientValidation(t *testing.T) {
	testCases := []struct {
		name        string
		cfg         Config
		expectError bool
	}{
		{
			name: "valid config",
			cfg: Config{
				Endpoints: []string{"localhost:9090"},
			},
			expectError: false,
		},
		{
			name:        "empty endpoints",
			cfg:         Config{Endpoints: []string{}},
			expectError: true,
		},
		{
			name:        "nil logger uses nop",
			cfg:         Config{Endpoints: []string{"localhost:9090"}},
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

// TestStatsAccessors validates stats accessor methods
func TestStatsAccessors(t *testing.T) {
	cfg := Config{Endpoints: []string{"localhost:9090"}}
	client, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	// Initial values
	require.Equal(t, 0.0, client.LastSendMs())
	require.Equal(t, 0.0, client.LastAckMs())
	require.Equal(t, uint64(0), client.LastErrs())
	require.Equal(t, uint64(0), client.LastRetries())
	require.Equal(t, "", client.LastEndpoint())
	require.False(t, client.LastCompressed())
	require.Equal(t, "", client.LastErrorKind())
}
