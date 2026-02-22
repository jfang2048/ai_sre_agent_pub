package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ── trendFromSamples tests ─────────────────────────────────────────────

func TestTrendRising(t *testing.T) {
	samples := []MetricSample{
		{Value: 10, Timestamp: time.Now()},
		{Value: 20, Timestamp: time.Now()},
		{Value: 30, Timestamp: time.Now()},
	}
	assert.Equal(t, "rising", trendFromSamples(samples))
}

func TestTrendFalling(t *testing.T) {
	samples := []MetricSample{
		{Value: 30, Timestamp: time.Now()},
		{Value: 20, Timestamp: time.Now()},
		{Value: 10, Timestamp: time.Now()},
	}
	assert.Equal(t, "falling", trendFromSamples(samples))
}

func TestTrendStable(t *testing.T) {
	samples := []MetricSample{
		{Value: 50, Timestamp: time.Now()},
		{Value: 50, Timestamp: time.Now()},
		{Value: 50, Timestamp: time.Now()},
	}
	assert.Equal(t, "stable", trendFromSamples(samples))
}

func TestTrendOscillating(t *testing.T) {
	samples := []MetricSample{
		{Value: 10, Timestamp: time.Now()},
		{Value: 20, Timestamp: time.Now()},
		{Value: 15, Timestamp: time.Now()},
	}
	assert.Equal(t, "stable", trendFromSamples(samples), "Up then down = stable")
}

func TestTrendTooFewSamples(t *testing.T) {
	assert.Equal(t, "stable", trendFromSamples([]MetricSample{{Value: 10}}))
	assert.Equal(t, "stable", trendFromSamples(nil))
}

// ── buildLLMSchema tests ──────────────────────────────────────────────

func TestBuildLLMSchema(t *testing.T) {
	schema := buildLLMSchema(
		"web-1",
		map[string]float64{"cpu": 95},
		map[string]string{"cpu": "rising"},
		[]string{"high_cpu"},
		[]string{"cpu_spike"},
		"production",
		nil, nil,
	)

	assert.Equal(t, "v1", schema.SchemaVersion)
	assert.Equal(t, "web-1", schema.NodeName)
	assert.Equal(t, 95.0, schema.Metrics["cpu"])
	assert.Equal(t, "rising", schema.Trends["cpu"])
	assert.Contains(t, schema.Alerts, "high_cpu")
	assert.False(t, schema.GeneratedAt.IsZero())
}

func TestBuildLLMSchemaForAgent(t *testing.T) {
	schema := BuildLLMSchemaForAgent(
		"db-1",
		map[string]float64{"mem": 80},
		[]string{"memory pressure"},
		[]string{"OOM risk"},
		EvidencePack{Context: "baseline context"},
		[]string{"snippet-1", "snippet-2"},
	)

	assert.Equal(t, "db-1", schema.NodeName)
	assert.Contains(t, schema.Context, "snippet-1")
	assert.Contains(t, schema.Context, "snippet-2")
	assert.Contains(t, schema.Alerts, "memory pressure")
	assert.Contains(t, schema.Anomalies, "OOM risk")
}

func TestBuildLLMSchemaForAgentNoSnippets(t *testing.T) {
	schema := BuildLLMSchemaForAgent(
		"web-1",
		map[string]float64{},
		nil, nil,
		EvidencePack{Context: "base"},
		nil,
	)

	assert.Equal(t, "base", schema.Context, "No snippets should keep context as-is")
}
