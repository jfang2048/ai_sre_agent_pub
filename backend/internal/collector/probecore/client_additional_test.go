package probecore

import (
	"errors"
	"testing"
	"time"

	probeipcv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/probeipc/v1"
	"github.com/stretchr/testify/require"
)

// TestNormalizeCompression validates compression normalization
func TestNormalizeCompression(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"", "none"},
		{"none", "none"},
		{"NONE", "none"},
		{"  none  ", "none"},
		{"gzip", "gzip"},
		{"GZIP", "gzip"},
		{"  GZIP  ", "gzip"},
		{"zlib", "none"}, // unsupported becomes none
		{"invalid", "none"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := normalizeCompression(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestNormalizeCollectors validates collector normalization
func TestNormalizeCollectors(t *testing.T) {
	testCases := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty input",
			input:    []string{},
			expected: nil,
		},
		{
			name:     "all returns nil",
			input:    []string{"all"},
			expected: nil,
		},
		{
			name:     "ALL returns nil",
			input:    []string{"ALL"},
			expected: nil,
		},
		{
			name:     "valid collectors ordered",
			input:    []string{"disk", "network", "ebpf"},
			expected: []string{"disk", "network", "ebpf"},
		},
		{
			name:     "collectors sorted to canonical order",
			input:    []string{"ebpf", "host", "disk"},
			expected: []string{"host", "disk", "ebpf"},
		},
		{
			name:     "deduplicates",
			input:    []string{"disk", "disk", "network"},
			expected: []string{"disk", "network"},
		},
		{
			name:     "trims whitespace",
			input:    []string{"  disk  ", "\tnetwork\n"},
			expected: []string{"disk", "network"},
		},
		{
			name:     "case insensitive",
			input:    []string{"DISK", "Network"},
			expected: []string{"disk", "network"},
		},
		{
			name:     "removes invalid collectors",
			input:    []string{"disk", "invalid", "network"},
			expected: []string{"disk", "network"},
		},
		{
			name:     "removes empty strings",
			input:    []string{"", "disk", "", "network"},
			expected: []string{"disk", "network"},
		},
		{
			name:     "all collectors",
			input:    []string{"host", "disk", "network", "rdma", "netlink", "ethtool", "perf", "ebpf", "gpu", "process"},
			expected: []string{"host", "disk", "network", "rdma", "netlink", "ethtool", "perf", "ebpf", "gpu", "process"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeCollectors(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestContainsCollectorsFlag validates flag detection
func TestContainsCollectorsFlag(t *testing.T) {
	testCases := []struct {
		name     string
		args     []string
		expected bool
	}{
		{
			name:     "no flag",
			args:     []string{"--interval", "1000"},
			expected: false,
		},
		{
			name:     "collectors flag",
			args:     []string{"--collectors", "disk,network"},
			expected: true,
		},
		{
			name:     "collectors with equals",
			args:     []string{"--collectors=disk,network"},
			expected: true,
		},
		{
			name:     "collectors with spaces and case",
			args:     []string{"  --Collectors  ", "disk,network"},
			expected: true,
		},
		{
			name:     "mixed args",
			args:     []string{"--interval", "1000", "--collectors=disk", "--topk", "10"},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := containsCollectorsFlag(tc.args)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestMaxInt validates max function
func TestMaxInt(t *testing.T) {
	testCases := []struct {
		name     string
		a, b     int
		expected int
	}{
		{"1_2", 1, 2, 2},
		{"5_3", 5, 3, 5},
		{"0_0", 0, 0, 0},
		{"neg_1", -1, 1, 1},
		{"neg_5", -5, -10, -5},
		{"equal", 100, 100, 100},
		{"large", 999, 1000, 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := maxInt(tc.a, tc.b)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestConfigValidate validates config validation
func TestConfigValidate(t *testing.T) {
	validConfig := Config{
		BinaryPath:         "/usr/bin/probe-core",
		Interval:           time.Second,
		TopK:               10,
		WindowSamples:      5,
		QueueDepth:         100,
		Compression:        "none",
		GPUIntervalSamples: 10,
		StartupTimeout:     10 * time.Second,
		StaleAfter:         30 * time.Second,
		FrameMaxBytes:      1024 * 1024,
	}

	testCases := []struct {
		name        string
		modifyCfg   func(Config) Config
		expectError bool
	}{
		{
			name:        "valid config",
			modifyCfg:   func(cfg Config) Config { return cfg },
			expectError: false,
		},
		{
			name: "empty binary path",
			modifyCfg: func(cfg Config) Config {
				cfg.BinaryPath = ""
				return cfg
			},
			expectError: true,
		},
		{
			name: "whitespace binary path",
			modifyCfg: func(cfg Config) Config {
				cfg.BinaryPath = "   "
				return cfg
			},
			expectError: true,
		},
		{
			name: "zero interval",
			modifyCfg: func(cfg Config) Config {
				cfg.Interval = 0
				return cfg
			},
			expectError: true,
		},
		{
			name: "negative interval",
			modifyCfg: func(cfg Config) Config {
				cfg.Interval = -1 * time.Second
				return cfg
			},
			expectError: true,
		},
		{
			name: "zero topk",
			modifyCfg: func(cfg Config) Config {
				cfg.TopK = 0
				return cfg
			},
			expectError: true,
		},
		{
			name: "zero window samples",
			modifyCfg: func(cfg Config) Config {
				cfg.WindowSamples = 0
				return cfg
			},
			expectError: true,
		},
		{
			name: "zero queue depth",
			modifyCfg: func(cfg Config) Config {
				cfg.QueueDepth = 0
				return cfg
			},
			expectError: true,
		},
		{
			name: "zero gpu interval samples",
			modifyCfg: func(cfg Config) Config {
				cfg.GPUIntervalSamples = 0
				return cfg
			},
			expectError: false,
		},
		{
			name: "zero startup timeout",
			modifyCfg: func(cfg Config) Config {
				cfg.StartupTimeout = 0
				return cfg
			},
			expectError: true,
		},
		{
			name: "zero stale after",
			modifyCfg: func(cfg Config) Config {
				cfg.StaleAfter = 0
				return cfg
			},
			expectError: true,
		},
		{
			name: "zero frame max bytes",
			modifyCfg: func(cfg Config) Config {
				cfg.FrameMaxBytes = 0
				return cfg
			},
			expectError: true,
		},
		{
			name: "invalid compression is normalized to none",
			modifyCfg: func(cfg Config) Config {
				cfg.Compression = "invalid"
				return cfg
			},
			expectError: false, // Gets normalized to "none"
		},
		{
			name: "invalid collector",
			modifyCfg: func(cfg Config) Config {
				cfg.Collectors = []string{"invalid_module"}
				return cfg
			},
			expectError: true,
		},
		{
			name: "collectors conflicts with args",
			modifyCfg: func(cfg Config) Config {
				cfg.Collectors = []string{"disk"}
				cfg.Args = []string{"--collectors=disk"}
				return cfg
			},
			expectError: true,
		},
		{
			name: "gzip compression valid",
			modifyCfg: func(cfg Config) Config {
				cfg.Compression = "gzip"
				return cfg
			},
			expectError: false,
		},
		{
			name: "empty collectors is valid",
			modifyCfg: func(cfg Config) Config {
				cfg.Collectors = []string{}
				return cfg
			},
			expectError: false,
		},
		{
			name: "all collectors is valid",
			modifyCfg: func(cfg Config) Config {
				cfg.Collectors = []string{"all"}
				return cfg
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.modifyCfg(validConfig)
			err := cfg.validate()

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestClientSetLastError validates last error tracking
func TestClientSetLastError(t *testing.T) {
	c := &Client{}

	// Clear error
	c.setLastError(nil)
	lastErr, _ := c.lastErr.Load().(string)
	require.Equal(t, "", lastErr)

	// Set error message
	testErr := errors.New("test error")
	c.setLastError(testErr)
	lastErr, _ = c.lastErr.Load().(string)
	require.Contains(t, lastErr, "test error")
}

// TestClientLatestFreshness validates latest freshness check
func TestClientLatestFreshness(t *testing.T) {
	c := &Client{}

	// No data yet
	batch, ok := c.Latest(time.Second)
	require.Nil(t, batch)
	require.False(t, ok)

	// Add stale data
	c.mu.Lock()
	c.latest = snapshot{
		batch:      &probeipcv1.ProbeBatch{Sequence: 1},
		receivedAt: time.Now().Add(-2 * time.Second),
	}
	c.mu.Unlock()

	batch, ok = c.Latest(time.Second)
	require.Nil(t, batch)
	require.False(t, ok)

	// Add fresh data
	c.mu.Lock()
	c.latest = snapshot{
		batch:      &probeipcv1.ProbeBatch{Sequence: 2},
		receivedAt: time.Now(),
	}
	c.mu.Unlock()

	batch, ok = c.Latest(time.Second)
	require.NotNil(t, batch)
	require.True(t, ok)
	require.Equal(t, uint32(2), batch.Sequence)
}

// TestClientStats validates stats reporting
func TestClientStats(t *testing.T) {
	c := &Client{}

	// Set some counters
	c.framesReceived.Add(10)
	c.decodeErrors.Add(2)
	c.crcFailures.Add(1)
	c.restarts.Add(3)
	c.lastSequence.Store(42)
	c.setLastError(errors.New("test error"))

	stats := c.Stats()

	require.Equal(t, uint64(10), stats.FramesReceived)
	require.Equal(t, uint64(2), stats.DecodeErrors)
	require.Equal(t, uint64(1), stats.CRCFailures)
	require.Equal(t, uint64(3), stats.Restarts)
	require.Equal(t, uint32(42), stats.LastSequence)
	require.Contains(t, stats.LastError, "test error")
}

// TestStatsEmptyClient validates empty client stats
func TestStatsEmptyClient(t *testing.T) {
	c := &Client{}

	stats := c.Stats()

	require.Equal(t, uint64(0), stats.FramesReceived)
	require.Equal(t, uint64(0), stats.DecodeErrors)
	require.Equal(t, uint64(0), stats.CRCFailures)
	require.Equal(t, uint64(0), stats.Restarts)
	require.Equal(t, uint32(0), stats.LastSequence)
	require.Equal(t, "", stats.LastError)
	require.True(t, stats.LastReceivedAt.IsZero())
}

// TestClientStopIdempotent validates stop idempotence
func TestClientStopIdempotent(t *testing.T) {
	c := &Client{}

	// Stop when not running should not panic
	c.Stop()
	c.Stop()
	c.Stop()

	require.False(t, c.running)
}

// TestClientLatestNoMaxAge validates latest without max age
func TestClientLatestNoMaxAge(t *testing.T) {
	c := &Client{}

	c.mu.Lock()
	c.latest = snapshot{
		batch:      &probeipcv1.ProbeBatch{Sequence: 1},
		receivedAt: time.Now().Add(-1 * time.Hour),
	}
	c.mu.Unlock()

	// No max age - should return even old data
	batch, ok := c.Latest(0)
	require.NotNil(t, batch)
	require.True(t, ok)

	// Negative max age - should return even old data
	batch, ok = c.Latest(-1 * time.Second)
	require.NotNil(t, batch)
	require.True(t, ok)
}
