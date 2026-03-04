package ai

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Module creation tests ──────────────────────────────────────────────

func TestNewModuleDefaults(t *testing.T) {
	cfg := DefaultConfig()
	m, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, m)

	stats := m.Stats()
	assert.False(t, stats.Running, "Module should not be running until Start is called")
	assert.Equal(t, 0, stats.ResultsStored)
}

// ── Synchronous Analyze tests ──────────────────────────────────────────

func TestAnalyzeCPUSaturation(t *testing.T) {
	cfg := DefaultConfig()
	m, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	dp := &queue.DataPoint{
		NodeName:  "web-1",
		Timestamp: time.Now(),
		Metrics: []queue.MetricData{
			{Name: "system.cpu.usage", Value: 96},
			{Name: "system.load.1m", Value: 12},
		},
	}

	result, err := m.Analyze(context.Background(), dp)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "web-1", result.NodeName)
	assert.NotEmpty(t, result.Classifications, "Should classify CPU saturation")
	assert.NotEmpty(t, result.Suggestions, "Should generate suggestions")
}

func TestAnalyzeNormalMetrics(t *testing.T) {
	cfg := DefaultConfig()
	m, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	dp := &queue.DataPoint{
		NodeName:  "web-2",
		Timestamp: time.Now(),
		Metrics: []queue.MetricData{
			{Name: "system.cpu.usage", Value: 30},
			{Name: "system.memory.usage", Value: 40},
		},
	}

	result, err := m.Analyze(context.Background(), dp)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Classifications, "Normal metrics should produce no classifications")
	assert.Empty(t, result.Suggestions, "Normal metrics should produce no suggestions")
}

func TestAnalyzeWithOOMKilledLog(t *testing.T) {
	cfg := DefaultConfig()
	m, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	dp := &queue.DataPoint{
		NodeName:  "db-1",
		Timestamp: time.Now(),
		Metrics:   []queue.MetricData{{Name: "system.memory.usage", Value: 98}},
		Logs: []queue.LogEntry{
			{Message: "container OOMKilled", Level: "error"},
		},
	}

	result, err := m.Analyze(context.Background(), dp)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Classifications)
	assert.NotNil(t, result.Explanation, "Should generate explanation for primary issue")
}

// ── GetRecentResults tests ─────────────────────────────────────────────

func TestGetRecentResultsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	m, _ := New(cfg, zap.NewNop())

	results := m.GetRecentResults(10)
	assert.Empty(t, results)
}

func TestGetRecentResultsWithLimit(t *testing.T) {
	cfg := DefaultConfig()
	m, _ := New(cfg, zap.NewNop())

	// Analyze a few data points to store results
	for i := 0; i < 5; i++ {
		dp := &queue.DataPoint{
			NodeName:  "node",
			Timestamp: time.Now(),
			Metrics:   []queue.MetricData{{Name: "system.cpu.usage", Value: float64(90 + i)}},
		}
		result, _ := m.Analyze(context.Background(), dp)
		m.storeResult(result)
	}

	results := m.GetRecentResults(3)
	assert.Len(t, results, 3, "Should return limited results")

	// Most recent first
	allResults := m.GetRecentResults(0)
	assert.Len(t, allResults, 5, "Limit 0 should return all")
}

// ── GetResultsByNode tests ─────────────────────────────────────────────

func TestGetResultsByNode(t *testing.T) {
	cfg := DefaultConfig()
	m, _ := New(cfg, zap.NewNop())

	// Analyze for different nodes
	for _, node := range []string{"web-1", "db-1", "web-1"} {
		dp := &queue.DataPoint{
			NodeName:  node,
			Timestamp: time.Now(),
			Metrics:   []queue.MetricData{{Name: "system.cpu.usage", Value: 95}},
		}
		result, _ := m.Analyze(context.Background(), dp)
		m.storeResult(result)
	}

	webResults := m.GetResultsByNode("web-1")
	assert.Len(t, webResults, 2)

	dbResults := m.GetResultsByNode("db-1")
	assert.Len(t, dbResults, 1)

	missingResults := m.GetResultsByNode("nonexistent")
	assert.Empty(t, missingResults)
}

// ── Stats tests ────────────────────────────────────────────────────────

func TestStatsAfterAnalysis(t *testing.T) {
	cfg := DefaultConfig()
	m, _ := New(cfg, zap.NewNop())

	dp := &queue.DataPoint{
		NodeName:  "web-1",
		Timestamp: time.Now(),
		Metrics:   []queue.MetricData{{Name: "system.cpu.usage", Value: 96}},
	}
	result, _ := m.Analyze(context.Background(), dp)
	m.storeResult(result)

	stats := m.Stats()
	assert.Equal(t, 1, stats.ResultsStored)
	assert.NotEmpty(t, stats.IssuesBySeverity, "Should track severity distribution")
}

// ── IngestMetrics convenience method test ──────────────────────────────

func TestIngestMetrics(t *testing.T) {
	cfg := DefaultConfig()
	m, _ := New(cfg, zap.NewNop())

	err := m.IngestMetrics(context.Background(), "web-1", []queue.MetricData{
		{Name: "system.cpu.usage", Value: 50},
	})
	require.NoError(t, err)

	stats := m.Stats()
	assert.Equal(t, 1, stats.QueueLength, "Should have queued one data point")
}
