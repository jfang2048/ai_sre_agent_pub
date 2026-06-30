package collect

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewShmCollector validates collector creation
func TestNewShmCollector(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected *ShmCollector
	}{
		{
			name:     "valid name",
			input:    "test_shm",
			expected: &ShmCollector{name: "test_shm", path: "/dev/shm/test_shm"},
		},
		{
			name:     "absolute path",
			input:    "/test_shm",
			expected: &ShmCollector{name: "/test_shm", path: "/dev/shm/test_shm"},
		},
		{
			name:     "name with leading/trailing whitespace",
			input:    "  test_shm  ",
			expected: &ShmCollector{name: "test_shm", path: "/dev/shm/test_shm"},
		},
		{
			name:     "empty string returns nil",
			input:    "",
			expected: nil,
		},
		{
			name:     "whitespace only returns nil",
			input:    "   ",
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := NewShmCollector(tc.input)
			if tc.expected == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, tc.expected.name, result.name)
				require.Equal(t, tc.expected.path, result.path)
			}
		})
	}
}

// TestShmCollectorCollectNil validates nil collector handling
func TestShmCollectorCollectNil(t *testing.T) {
	var c *ShmCollector
	metrics := c.Collect(time.Now())
	require.Nil(t, metrics)
}

// TestShmCollectorLastReadCountNil validates nil collector
func TestShmCollectorLastReadCountNil(t *testing.T) {
	var c *ShmCollector
	require.Equal(t, uint64(0), c.LastReadCount())
}

// TestShmCollectorLastErrorCountNil validates nil collector
func TestShmCollectorLastErrorCountNil(t *testing.T) {
	var c *ShmCollector
	require.Equal(t, uint64(0), c.LastErrorCount())
}

// TestShmCollectorCapacityNil validates nil collector
func TestShmCollectorCapacityNil(t *testing.T) {
	var c *ShmCollector
	require.Equal(t, uint64(0), c.Capacity())
}

// TestShmCollectorCloseNil validates nil collector close
func TestShmCollectorCloseNil(t *testing.T) {
	var c *ShmCollector
	// Should not panic
	c.Close()
}

// TestReadU32 validates uint32 reading with wraparound
func TestReadU32(t *testing.T) {
	testCases := []struct {
		name     string
		buf      []byte
		pos      uint64
		capacity uint64
		expected uint32
	}{
		{
			name:     "read at position 0",
			buf:      []byte{0x01, 0x02, 0x03, 0x04, 0xFF},
			pos:      0,
			capacity: 1024,
			expected: 0x04030201,
		},
		{
			name:     "read at position 1",
			buf:      []byte{0xFF, 0x01, 0x02, 0x03, 0x04},
			pos:      1,
			capacity: 1024,
			expected: 0x04030201,
		},
		{
			name:     "wraparound at capacity boundary",
			buf:      []byte{0x01, 0x02, 0x03, 0x04},
			pos:      3,
			capacity: 4,
			expected: 0x03020104,
		},
		{
			name:     "all zeros",
			buf:      []byte{0x00, 0x00, 0x00, 0x00},
			pos:      0,
			capacity: 1024,
			expected: 0x00000000,
		},
		{
			name:     "max uint32",
			buf:      []byte{0xFF, 0xFF, 0xFF, 0xFF},
			pos:      0,
			capacity: 1024,
			expected: 0xFFFFFFFF,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := readU32(tc.buf, tc.pos, tc.capacity)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestReadBytes validates byte reading with wraparound
func TestReadBytes(t *testing.T) {
	testCases := []struct {
		name     string
		buf      []byte
		pos      uint64
		capacity uint64
		length   uint64
		expected []byte
	}{
		{
			name:     "read at position 0",
			buf:      []byte{0x01, 0x02, 0x03, 0x04, 0x05},
			pos:      0,
			capacity: 1024,
			length:   4,
			expected: []byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			name:     "read with offset",
			buf:      []byte{0xFF, 0x01, 0x02, 0x03, 0x04},
			pos:      1,
			capacity: 1024,
			length:   4,
			expected: []byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			name:     "wraparound at boundary",
			buf:      []byte{0x03, 0x04, 0x01, 0x02},
			pos:      2,
			capacity: 4,
			length:   4,
			expected: []byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			name:     "zero length",
			buf:      []byte{0x01, 0x02, 0x03},
			pos:      0,
			capacity: 1024,
			length:   0,
			expected: []byte{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := readBytes(tc.buf, tc.pos, tc.capacity, tc.length)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestMathFromBytes validates float64 from bytes conversion
func TestMathFromBytes(t *testing.T) {
	testCases := []struct {
		name  string
		input float64
	}{
		{"zero", 0.0},
		{"positive", 42.5},
		{"negative", -17.3},
		{"large", 1.7976931348623157e+308}, // Max float64
		{"small", 2.2250738585072014e-308}, // Smallest positive float64
		{"pi", math.Pi},
		{"nan", math.NaN()},
		{"inf", math.Inf(1)},
		{"neg inf", math.Inf(-1)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bytes := make([]byte, 8)
			binary.LittleEndian.PutUint64(bytes, math.Float64bits(tc.input))
			result := mathFromBytes(bytes)

			if math.IsNaN(tc.input) {
				require.True(t, math.IsNaN(result))
			} else {
				require.Equal(t, tc.input, result)
			}
		})
	}
}

// TestDecodeMetricValid validates valid metric decoding
func TestDecodeMetricValid(t *testing.T) {
	// Construct a valid metric payload:
	// type (1) + name_len (2) + name (4) + value (8) + timestamp (8) + label_count (2) + label1 (2+3+2+3)
	payload := []byte{
		0x00,       // metric type
		0x04, 0x00, // name length = 4
		't', 'e', 's', 't', // name = "test"
	}

	// Add value (42.5 as float64)
	valueBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(valueBytes, math.Float64bits(42.5))
	payload = append(payload, valueBytes...)

	// Add timestamp (8 bytes, ignored)
	payload = append(payload, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...)

	// Add label count = 1
	payload = append(payload, 0x01, 0x00)

	// Add label: key_len=3, key="foo", val_len=3, val="bar"
	payload = append(payload, 0x03, 0x00, 'f', 'o', 'o', 0x03, 0x00, 'b', 'a', 'r')

	metric, ok := decodeMetric(payload, time.Now())
	require.True(t, ok)
	require.NotNil(t, metric)
	require.Equal(t, "test", metric.Name)
	require.Equal(t, 42.5, metric.Value)
	require.NotEmpty(t, metric.Labels)

	// Check for source label
	hasSource := false
	for _, label := range metric.Labels {
		if label.Key == "source" && label.Value == "shm" {
			hasSource = true
			break
		}
	}
	require.True(t, hasSource)

	// Check for custom label
	hasFoo := false
	for _, label := range metric.Labels {
		if label.Key == "foo" && label.Value == "bar" {
			hasFoo = true
			break
		}
	}
	require.True(t, hasFoo)
}

// TestDecodeMetricInvalidPayloads validates invalid payload handling
func TestDecodeMetricInvalidPayloads(t *testing.T) {
	testCases := []struct {
		name    string
		payload []byte
	}{
		{"empty payload", []byte{}},
		{"too short for header", []byte{0x00, 0x01}},
		{"too short for name", []byte{0x00, 0x05, 0x00, 'a', 'b'}},
		{"too short for value", []byte{0x00, 0x03, 0x00, 'a', 'b', 'c'}},
		{"name length exceeds payload", []byte{0x00, 0xFF, 0xFF, 'a'}},
		{"label count exceeds payload", []byte{0x00, 0x01, 0x00, 'a', 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			metric, ok := decodeMetric(tc.payload, time.Now())
			require.False(t, ok)
			require.Nil(t, metric)
		})
	}
}

// TestDecodeMetricMultipleLabels validates multiple labels
func TestDecodeMetricMultipleLabels(t *testing.T) {
	// Construct metric with 2 labels
	payload := []byte{0x00, 0x04, 0x00, 't', 'e', 's', 't'} // type + name

	// value
	valueBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(valueBytes, math.Float64bits(1.0))
	payload = append(payload, valueBytes...)
	payload = append(payload, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...) // timestamp

	// label_count = 2
	payload = append(payload, 0x02, 0x00)

	// label1: key="host", val="localhost"
	payload = append(payload, 0x04, 0x00, 'h', 'o', 's', 't', 0x09, 0x00)
	payload = append(payload, []byte("localhost")...)

	// label2: key="region", val="us-west"
	payload = append(payload, 0x06, 0x00, 'r', 'e', 'g', 'i', 'o', 'n', 0x06, 0x00)
	payload = append(payload, []byte("us-west")...)

	metric, ok := decodeMetric(payload, time.Now())
	require.True(t, ok)
	require.NotNil(t, metric)

	// Should have 3 labels (2 custom + 1 source)
	require.Equal(t, 3, len(metric.Labels))
}

// TestDecodeMetricWithZeroLabelCount validates zero label count
func TestDecodeMetricWithZeroLabelCount(t *testing.T) {
	payload := []byte{0x00, 0x04, 0x00, 't', 'e', 's', 't'} // type + name

	// value
	valueBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(valueBytes, math.Float64bits(123.45))
	payload = append(payload, valueBytes...)
	payload = append(payload, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...) // timestamp

	// label_count = 0
	payload = append(payload, 0x00, 0x00)

	metric, ok := decodeMetric(payload, time.Now())
	require.True(t, ok)
	require.NotNil(t, metric)

	// Should have only source label
	require.Equal(t, 1, len(metric.Labels))
	require.Equal(t, "source", metric.Labels[0].Key)
	require.Equal(t, "shm", metric.Labels[0].Value)
}

// TestDecodeMetricLabelWithEmptyKeyValue validates label handling
func TestDecodeMetricLabelWithEmptyKeyValue(t *testing.T) {
	payload := []byte{0x00, 0x04, 0x00, 't', 'e', 's', 't'} // type + name

	// value
	valueBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(valueBytes, math.Float64bits(1.0))
	payload = append(payload, valueBytes...)
	payload = append(payload, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...) // timestamp

	// label_count = 1
	payload = append(payload, 0x01, 0x00)

	// label with empty key and value
	payload = append(payload, 0x00, 0x00, 0x00, 0x00)

	metric, ok := decodeMetric(payload, time.Now())
	require.True(t, ok)
	require.NotNil(t, metric)

	// Should still decode successfully with empty label
	hasEmptyLabel := false
	for _, label := range metric.Labels {
		if label.Key == "" && label.Value == "" {
			hasEmptyLabel = true
			break
		}
	}
	require.True(t, hasEmptyLabel)
}
