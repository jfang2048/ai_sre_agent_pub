package probe

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestCollectorWithLevel verifies level configuration
func TestCollectorWithLevel(t *testing.T) {
	testCases := []struct {
		name     string
		level    int
		expected int
	}{
		{"level 1", 1, 1},
		{"level 2", 2, 2},
		{"level 3", 3, 3},
		{"level 4", 4, 4},
		{"level 5", 5, 5},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewCollector(WithLevel(tc.level))
			if err != nil {
				t.Fatalf("NewCollector failed: %v", err)
			}
			if c.level != tc.expected {
				t.Errorf("expected level %d, got %d", tc.expected, c.level)
			}
		})
	}
}

// TestCollectorWithTopN verifies top N processes configuration
func TestCollectorWithTopN(t *testing.T) {
	testCases := []int{5, 10, 20, 50, 100}

	for _, topN := range testCases {
		t.Run(fmt.Sprintf("topN_%d", topN), func(t *testing.T) {
			c, err := NewCollector(WithLevel(1), WithTopNProcesses(topN))
			if err != nil {
				t.Fatalf("NewCollector failed: %v", err)
			}
			if c.topNProcesses != topN {
				t.Errorf("expected topN %d, got %d", topN, c.topNProcesses)
			}
		})
	}
}

// TestCollectorCollectIdempotent verifies multiple collects work
func TestCollectorCollectIdempotent(t *testing.T) {
	c, err := NewCollector(WithLevel(1))
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	// First collection
	batch1, err := c.Collect()
	if err != nil {
		t.Fatalf("First Collect failed: %v", err)
	}

	// Wait a bit to ensure timestamps differ
	time.Sleep(10 * time.Millisecond)

	// Second collection
	batch2, err := c.Collect()
	if err != nil {
		t.Fatalf("Second Collect failed: %v", err)
	}

	// Both should succeed
	if len(batch1.Metrics) == 0 {
		t.Error("First batch should have metrics")
	}

	if len(batch2.Metrics) == 0 {
		t.Error("Second batch should have metrics")
	}

	// Timestamps should be different
	if batch2.CollectedAt.Equal(batch1.CollectedAt) {
		t.Error("CollectedAt timestamps should differ between collections")
	}
}

// TestCollectorConcurrentCollect verifies thread safety
func TestCollectorConcurrentCollect(t *testing.T) {
	c, err := NewCollector(WithLevel(1))
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	const numGoroutines = 10
	const collectionsPerGoroutine = 5

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*collectionsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < collectionsPerGoroutine; j++ {
				batch, err := c.Collect()
				if err != nil {
					errors <- fmt.Errorf("collect failed: %w", err)
					return
				}
				if batch == nil {
					errors <- fmt.Errorf("batch is nil")
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("concurrent collection error: %v", err)
	}
}

// TestMetricValidation verifies metric structure
func TestMetricValidation(t *testing.T) {
	c, err := NewCollector(WithLevel(1))
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	batch, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// Validate all metrics
	for i, metric := range batch.Metrics {
		// Name validation
		if metric.Name == "" {
			t.Errorf("metric %d: name should not be empty", i)
			continue
		}

		if len(metric.Name) > 200 {
			t.Errorf("metric %d: name too long (%d chars)", i, len(metric.Name))
		}

		// Type validation
		if metric.Type != "gauge" && metric.Type != "counter" {
			t.Errorf("metric %d: invalid type '%s'", i, metric.Type)
		}

		// Value validation (NaN check)
		if metric.Value != metric.Value {
			t.Errorf("metric %d (%s): value is NaN", i, metric.Name)
		}

		// Timestamp validation
		if metric.Timestamp.IsZero() {
			t.Errorf("metric %d: timestamp should be set", i)
		}

		if metric.Timestamp.After(time.Now().Add(time.Minute)) {
			t.Errorf("metric %d: timestamp is in the future", i)
		}
	}
}

// TestCollectorLevel2CollectsExtendedMetrics verifies level 2 collection
func TestCollectorLevel2CollectsExtendedMetrics(t *testing.T) {
	c1, err := NewCollector(WithLevel(1))
	if err != nil {
		t.Fatalf("NewCollector level 1 failed: %v", err)
	}

	c2, err := NewCollector(WithLevel(2))
	if err != nil {
		t.Fatalf("NewCollector level 2 failed: %v", err)
	}

	batch1, _ := c1.Collect()
	batch2, _ := c2.Collect()

	// Level 2 should collect at least as many metrics as level 1
	if len(batch2.Metrics) < len(batch1.Metrics) {
		t.Errorf("level 2 should collect >= level 1 metrics, got %d vs %d",
			len(batch2.Metrics), len(batch1.Metrics))
	}
}

// TestMetricLabelHandling verifies label structure
func TestMetricLabelHandling(t *testing.T) {
	c, err := NewCollector(WithLevel(2))
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	batch, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// Check metrics with labels
	hasLabels := false
	for _, metric := range batch.Metrics {
		if metric.Labels != nil {
			hasLabels = true
			// Validate label keys and values
			for key, value := range metric.Labels {
				if key == "" {
					t.Errorf("metric %s: label key should not be empty", metric.Name)
				}
				if len(key) > 100 {
					t.Errorf("metric %s: label key too long (%d chars)", metric.Name, len(key))
				}
				if len(value) > 300 {
					t.Errorf("metric %s: label value too long (%d chars)", metric.Name, len(value))
				}
			}
		}
	}

	if !hasLabels {
		t.Log("no metrics with labels found at level 2")
	}
}

// TestCollectorStartStop verifies lifecycle management
func TestCollectorStartStop(t *testing.T) {
	c, err := NewCollector(WithLevel(1))
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	// Start should not panic
	c.Start()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Stop should be graceful
	c.Stop()

	// Give it time to stop
	time.Sleep(50 * time.Millisecond)
}

// TestCollectorStartStopIdempotent verifies multiple start/stop calls
func TestCollectorStartStopIdempotent(t *testing.T) {
	c, err := NewCollector(WithLevel(1))
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	// Multiple starts should not panic
	c.Start()
	c.Start()

	time.Sleep(50 * time.Millisecond)

	// Multiple stops should not panic
	c.Stop()
	c.Stop()

	time.Sleep(50 * time.Millisecond)
}

// TestCollectMetricsContainsExpectedNames verifies expected metrics are collected
func TestCollectMetricsContainsExpectedNames(t *testing.T) {
	c, err := NewCollector(WithLevel(1))
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	batch, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// Map of metric names for easy lookup
	metricMap := make(map[string]bool)
	for _, metric := range batch.Metrics {
		metricMap[metric.Name] = true
	}

	// Expected metrics at level 1 (use a subset that's more likely to exist)
	expectedMetrics := []string{
		"node_load1",
	}

	// Also check for some common metric patterns using strings.Contains
	hasCPUMetric := false
	hasMemoryMetric := false
	hasDiskMetric := false
	hasNetworkMetric := false

	for name := range metricMap {
		if stringsContains(name, "cpu") {
			hasCPUMetric = true
		}
		if stringsContains(name, "memory") || stringsContains(name, "mem") {
			hasMemoryMetric = true
		}
		if stringsContains(name, "disk") {
			hasDiskMetric = true
		}
		if stringsContains(name, "net") || stringsContains(name, "interface") {
			hasNetworkMetric = true
		}
	}

	for _, expected := range expectedMetrics {
		if !metricMap[expected] {
			t.Errorf("expected metric '%s' not found in batch (found %d metrics)", expected, len(metricMap))
		}
	}

	if !hasCPUMetric {
		t.Error("expected at least one CPU-related metric")
	}
	if !hasMemoryMetric {
		t.Error("expected at least one memory-related metric")
	}
	if !hasDiskMetric {
		t.Error("expected at least one disk-related metric")
	}
	if !hasNetworkMetric {
		t.Error("expected at least one network-related metric")
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr ||
		s[len(s)-len(substr):] == substr ||
		findInString(s, substr)))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
