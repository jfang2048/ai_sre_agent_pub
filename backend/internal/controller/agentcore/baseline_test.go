package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaselineEngine_RecordAndDetectDrift(t *testing.T) {
	be := NewBaselineEngine(DefaultBaselineConfig())
	now := time.Now()

	// Seed baseline with stable values
	for i := 0; i < 20; i++ {
		ts := now.Add(-time.Duration(30-i) * time.Minute)
		be.RecordMetric("host-1", "cpu_usage", 40.0+float64(i%3), ts)
	}

	// Recent spike
	be.RecordMetric("host-1", "cpu_usage", 95.0, now.Add(-2*time.Minute))
	be.RecordMetric("host-1", "cpu_usage", 98.0, now.Add(-1*time.Minute))

	drifts := be.DetectDrift("host-1")
	// Should detect drift in CPU usage
	found := false
	for _, d := range drifts {
		if d.Metric == "cpu_usage" && d.Current > 90 {
			found = true
			assert.Equal(t, "host-1", d.CollectorID)
			assert.Equal(t, "metric", d.Dimension)
		}
	}
	assert.True(t, found, "should detect CPU drift")
}

func TestBaselineEngine_ProcessFrequencyDrift(t *testing.T) {
	be := NewBaselineEngine(DefaultBaselineConfig())
	now := time.Now()

	// Stable process spawn rate
	for i := 0; i < 20; i++ {
		ts := now.Add(-time.Duration(30-i) * time.Minute)
		be.RecordProcessFrequency("host-1", 10.0+float64(i%2), ts)
	}

	// Sudden burst
	be.RecordProcessFrequency("host-1", 150.0, now.Add(-1*time.Minute))

	drifts := be.DetectDrift("host-1")
	found := false
	for _, d := range drifts {
		if d.Dimension == "process_frequency" {
			found = true
			assert.True(t, d.Current > 100)
		}
	}
	assert.True(t, found, "should detect process frequency burst")
}

func TestBaselineEngine_SyscallRateDrift(t *testing.T) {
	be := NewBaselineEngine(DefaultBaselineConfig())
	now := time.Now()

	for i := 0; i < 20; i++ {
		ts := now.Add(-time.Duration(30-i) * time.Minute)
		be.RecordSyscallRate("host-1", "execve", 50.0+float64(i%5), ts)
	}

	be.RecordSyscallRate("host-1", "execve", 500.0, now.Add(-1*time.Minute))

	drifts := be.DetectDrift("host-1")
	found := false
	for _, d := range drifts {
		if d.Metric == "execve" {
			found = true
		}
	}
	assert.True(t, found, "should detect execve rate spike")
}

func TestBaselineEngine_NewPorts(t *testing.T) {
	be := NewBaselineEngine(DefaultBaselineConfig())

	be.RecordPortExposure("host-1", 80)
	be.RecordPortExposure("host-1", 443)
	be.RecordPortExposure("host-1", 22)

	newPorts := be.DetectNewPorts("host-1", []int{80, 443, 4444})
	assert.Equal(t, []int{4444}, newPorts)

	// Unknown host returns all ports as new
	newPorts = be.DetectNewPorts("unknown", []int{80})
	assert.Equal(t, []int{80}, newPorts)
}

func TestBaselineEngine_NewDestinations(t *testing.T) {
	be := NewBaselineEngine(DefaultBaselineConfig())

	be.RecordOutboundDestination("host-1", "10.0.0.1")
	be.RecordOutboundDestination("host-1", "10.0.0.2")

	newDests := be.DetectNewDestinations("host-1", []string{"10.0.0.1", "203.0.113.77"})
	assert.Equal(t, []string{"203.0.113.77"}, newDests)
}

func TestBaselineEngine_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")
	now := time.Now()

	// Create and populate
	be1 := NewBaselineEngine(DefaultBaselineConfig())
	for i := 0; i < 10; i++ {
		be1.RecordMetric("host-1", "cpu", float64(40+i), now.Add(-time.Duration(i)*time.Minute))
	}
	be1.RecordPortExposure("host-1", 80)
	require.NoError(t, be1.SaveBaseline(path))

	// Load into new engine
	be2 := NewBaselineEngine(DefaultBaselineConfig())
	require.NoError(t, be2.LoadBaseline(path))

	ids := be2.CollectorIDs()
	assert.Equal(t, []string{"host-1"}, ids)
}

func TestBaselineEngine_LoadNonexistent(t *testing.T) {
	be := NewBaselineEngine(DefaultBaselineConfig())
	err := be.LoadBaseline("/tmp/nonexistent-baseline-test.json")
	assert.NoError(t, err, "loading nonexistent file should not error")
}

func TestBaselineEngine_Ready(t *testing.T) {
	be := NewBaselineEngine(DefaultBaselineConfig())
	assert.False(t, be.Ready())

	now := time.Now()
	for i := 0; i < 5; i++ {
		be.RecordProcessFrequency("host-1", float64(10+i), now.Add(-time.Duration(i)*time.Minute))
	}
	assert.True(t, be.Ready())
}

func TestBaselineEngine_NoDriftOnStableData(t *testing.T) {
	be := NewBaselineEngine(DefaultBaselineConfig())
	now := time.Now()

	// All values are the same — no drift should be detected
	for i := 0; i < 30; i++ {
		ts := now.Add(-time.Duration(30-i) * time.Minute)
		be.RecordMetric("stable-host", "cpu", 50.0, ts)
	}

	drifts := be.DetectDrift("stable-host")
	assert.Empty(t, drifts, "stable data should produce no drift")
}

func TestBaselineEngine_BoundedSamples(t *testing.T) {
	cfg := DefaultBaselineConfig()
	cfg.MaxSamplesPerHost = 20
	be := NewBaselineEngine(cfg)
	now := time.Now()

	for i := 0; i < 50; i++ {
		be.RecordMetric("host-1", "test", float64(i), now.Add(time.Duration(i)*time.Second))
	}

	// Internal check via persistence
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	require.NoError(t, be.SaveBaseline(path))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Less(t, len(data), 5000, "bounded samples should keep file small")
}
