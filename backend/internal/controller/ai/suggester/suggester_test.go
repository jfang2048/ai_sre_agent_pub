package suggester

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/classifier"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func makeDP(metrics map[string]float64, logs []queue.LogEntry) *queue.DataPoint {
	dp := &queue.DataPoint{
		NodeName:  "test-node",
		Timestamp: time.Now(),
	}
	for name, value := range metrics {
		dp.Metrics = append(dp.Metrics, queue.MetricData{Name: name, Value: value})
	}
	dp.Logs = logs
	return dp
}

// ── Suggest tests ──────────────────────────────────────────────────────

func TestSuggestCPUSaturationCritical(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	c := classifier.Classification{
		Category: classifier.CategoryCPUSaturation,
		Severity: classifier.SeverityCritical,
	}
	dp := makeDP(map[string]float64{"system.cpu.usage": 95}, nil)

	suggestions, err := s.Suggest(ctx, c, dp)
	require.NoError(t, err)
	require.NotEmpty(t, suggestions)

	// Should include "Scale Horizontally" and "Identify Runaway Process"
	titles := make([]string, len(suggestions))
	for i, sg := range suggestions {
		titles[i] = sg.Title
	}
	assert.Contains(t, titles, "Scale Horizontally")
	assert.Contains(t, titles, "Identify and Stop Runaway Process")
}

func TestSuggestMemoryPressureOOMKilled(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	c := classifier.Classification{
		Category: classifier.CategoryMemoryPressure,
		Severity: classifier.SeverityCritical,
	}
	dp := makeDP(
		map[string]float64{"system.memory.usage": 98},
		[]queue.LogEntry{{Message: "container OOMKilled", Level: "error"}},
	)

	suggestions, err := s.Suggest(ctx, c, dp)
	require.NoError(t, err)
	require.NotEmpty(t, suggestions)

	foundOOM := false
	foundKill := false
	for _, sg := range suggestions {
		if sg.Type == RemediationResourceLimit {
			foundOOM = true
			assert.InDelta(t, 0.90, sg.Confidence, 0.01)
		}
		if sg.Type == RemediationKillProcess {
			foundKill = true
		}
	}
	assert.True(t, foundOOM, "Should suggest increasing memory limits for OOMKilled")
	assert.True(t, foundKill, "Should suggest killing idle processes for high memory usage")
}

func TestSuggestDiskIOCleanup(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	c := classifier.Classification{
		Category: classifier.CategoryDiskIOBottleneck,
		Severity: classifier.SeverityCritical,
	}
	dp := makeDP(map[string]float64{"system.disk.usage": 92}, nil)

	suggestions, err := s.Suggest(ctx, c, dp)
	require.NoError(t, err)
	require.NotEmpty(t, suggestions)

	found := false
	for _, sg := range suggestions {
		if sg.Type == RemediationCleanup {
			found = true
			assert.Equal(t, "Clean Up Disk Space", sg.Title)
		}
	}
	assert.True(t, found, "Should suggest disk cleanup for high disk usage")
}

func TestSuggestCapacityIssue(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	c := classifier.Classification{
		Category: classifier.CategoryCapacityIssue,
		Severity: classifier.SeverityCritical,
	}
	dp := makeDP(map[string]float64{"system.disk.usage": 95}, nil)

	suggestions, err := s.Suggest(ctx, c, dp)
	require.NoError(t, err)
	require.NotEmpty(t, suggestions)
	assert.Equal(t, RemediationScale, suggestions[0].Type)
}

func TestSuggestUnknownCategoryReturnsEmpty(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	c := classifier.Classification{
		Category: classifier.CategoryUnknown,
		Severity: classifier.SeverityInfo,
	}
	dp := makeDP(nil, nil)

	suggestions, err := s.Suggest(ctx, c, dp)
	require.NoError(t, err)
	assert.Empty(t, suggestions, "Unknown category should produce no rule-based suggestions")
}

func TestSuggestSortedByConfidence(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	// Memory pressure with OOMKilled (0.90 confidence) + kill idle (0.75)
	c := classifier.Classification{
		Category: classifier.CategoryMemoryPressure,
		Severity: classifier.SeverityCritical,
	}
	dp := makeDP(
		map[string]float64{"system.memory.usage": 95},
		[]queue.LogEntry{{Message: "OOMKilled", Level: "error"}},
	)

	suggestions, err := s.Suggest(ctx, c, dp)
	require.NoError(t, err)
	require.True(t, len(suggestions) >= 2)

	// Should be sorted: highest confidence first
	for i := 1; i < len(suggestions); i++ {
		assert.GreaterOrEqual(t, suggestions[i-1].Confidence, suggestions[i].Confidence,
			"suggestions should be sorted by confidence descending")
	}
}

// ── Explain tests ──────────────────────────────────────────────────────

func TestExplainCPUSaturation(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	c := classifier.Classification{
		Category: classifier.CategoryCPUSaturation,
		Severity: classifier.SeverityCritical,
	}
	dp := makeDP(map[string]float64{"system.cpu.usage": 96, "system.load.1m": 12}, nil)

	explanation, err := s.Explain(ctx, c, dp)
	require.NoError(t, err)
	require.NotNil(t, explanation)

	assert.Contains(t, explanation.Summary, "CPU")
	assert.NotEmpty(t, explanation.WhatHappened)
	assert.NotEmpty(t, explanation.WhyHappened)
	assert.NotEmpty(t, explanation.NextSteps)
}

func TestExplainMemoryPressure(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	c := classifier.Classification{
		Category: classifier.CategoryMemoryPressure,
		Severity: classifier.SeverityCritical,
	}
	dp := makeDP(map[string]float64{"system.memory.usage": 98}, nil)

	explanation, err := s.Explain(ctx, c, dp)
	require.NoError(t, err)
	require.NotNil(t, explanation)

	assert.Contains(t, explanation.Summary, "Memory")
	assert.Contains(t, explanation.Impact, "OOM")
}

func TestExplainDefaultCategory(t *testing.T) {
	s := New(zap.NewNop())
	ctx := context.Background()

	c := classifier.Classification{
		Category:    classifier.CategoryExternalDependency,
		Description: "Redis timeout detected",
		Factors:     []string{"Redis response time > 5s"},
	}
	dp := makeDP(nil, nil)

	explanation, err := s.Explain(ctx, c, dp)
	require.NoError(t, err)
	require.NotNil(t, explanation)

	assert.Contains(t, explanation.WhatHappened, "Redis timeout")
	assert.True(t, strings.Contains(explanation.WhyHappened, "Redis response time"))
}

// ── Helper tests ───────────────────────────────────────────────────────

func TestSortByConfidence(t *testing.T) {
	suggestions := []Suggestion{
		{Title: "low", Confidence: 0.3},
		{Title: "high", Confidence: 0.95},
		{Title: "med", Confidence: 0.7},
	}

	sortByConfidence(suggestions)

	assert.Equal(t, "high", suggestions[0].Title)
	assert.Equal(t, "med", suggestions[1].Title)
	assert.Equal(t, "low", suggestions[2].Title)
}
