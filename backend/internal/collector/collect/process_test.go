package collect

import (
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

// TestNewProcessCollector validates collector creation
func TestNewProcessCollector(t *testing.T) {
	testCases := []struct {
		name     string
		topK     int
		expected int
	}{
		{"positive topK", 5, 5},
		{"zero topK defaults to 10", 0, 10},
		{"negative topK defaults to 10", -1, 10},
		{"large topK", 1000, 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewProcessCollector(tc.topK)
			require.NotNil(t, c)
			require.Equal(t, tc.expected, c.topK)
			require.NotNil(t, c.lastCPU)
			require.NotNil(t, c.lastIO)
			require.False(t, c.lastTime.IsZero())
		})
	}
}

// TestProcessCollectorCollect validates process collection
func TestProcessCollectorCollect(t *testing.T) {
	c := NewProcessCollector(10)

	// First collection
	now := time.Now()
	samples1 := c.Collect(now)

	// Samples may be empty on systems without /proc, but should not panic
	require.NotNil(t, samples1)

	// Verify structure of returned samples
	for _, sample := range samples1 {
		require.Greater(t, sample.Pid, int32(0))
		require.NotEmpty(t, sample.Name)
		require.GreaterOrEqual(t, sample.CpuPercent, 0.0)
		require.GreaterOrEqual(t, sample.RssBytes, uint64(0))
	}
}

// TestProcessCollectorCollectMultiple validates multiple collections
func TestProcessCollectorCollectMultiple(t *testing.T) {
	c := NewProcessCollector(5)

	// First collection
	samples1 := c.Collect(time.Now())
	require.NotNil(t, samples1)

	// Second collection (after a small delay)
	time.Sleep(10 * time.Millisecond)
	samples2 := c.Collect(time.Now())
	require.NotNil(t, samples2)

	// TopK should be respected
	require.LessOrEqual(t, len(samples2), 5)
}

// TestProcessCollectorTopK validates topK limit
func TestProcessCollectorTopK(t *testing.T) {
	topKValues := []int{1, 3, 5, 10}

	for _, topK := range topKValues {
		t.Run("topK", func(t *testing.T) {
			c := NewProcessCollector(topK)
			samples := c.Collect(time.Now())

			// Should not return more than topK samples
			require.LessOrEqual(t, len(samples), topK)
		})
	}
}

// TestParseUint validates uint parsing
func TestParseUint(t *testing.T) {
	testCases := []struct {
		input    string
		expected uint64
	}{
		{"0", 0},
		{"123", 123},
		{"18446744073709551615", 18446744073709551615}, // Max uint64
		{"invalid", 0},
		{"", 0},
		{"-1", 0},
		{"12.3", 12},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := parseUint(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestParseStat validates stat parsing
func TestParseStat(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "valid stat line",
			input:    "1 (init) S 0 1 0 0 -1 4194560 667 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0",
			expected: []string{"1", "(init)", "S", "0", "1", "0", "0", "-1", "4194560", "667", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "single value",
			input:    "123",
			expected: []string{"123"},
		},
		{
			name:     "multiple spaces",
			input:    "1   2    3",
			expected: []string{"1", "2", "3"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseStat(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestProcessCollectorStateTracking validates state tracking between collections
func TestProcessCollectorStateTracking(t *testing.T) {
	c := NewProcessCollector(10)

	// Initial state
	require.NotNil(t, c.lastCPU)
	require.NotNil(t, c.lastIO)
	require.Zero(t, c.lastTotal)

	// First collection
	c.Collect(time.Now())

	// State should be updated after first collection
	require.NotZero(t, c.lastTime)

	// Second collection
	c.Collect(time.Now())

	// State should continue to be updated
	require.True(t, c.lastTime.After(time.Now().Add(-time.Second)))
}

// TestProcessCollectorEmptyResults validates handling of empty results
func TestProcessCollectorEmptyResults(t *testing.T) {
	c := NewProcessCollector(10)

	samples := c.Collect(time.Now())

	// Should return empty slice instead of nil when no processes found
	require.NotNil(t, samples)
}

// TestProcessCollectorConcurrentCollect validates concurrent collection
func TestProcessCollectorConcurrentCollect(t *testing.T) {
	c := NewProcessCollector(10)

	const numGoroutines = 10
	results := make(chan []*telemetryv1.ProcessSample, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			results <- c.Collect(time.Now())
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		samples := <-results
		require.NotNil(t, samples)
		// TopK should always be respected
		require.LessOrEqual(t, len(samples), 10)
	}
}
