package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── normalizeProvider tests ────────────────────────────────────────────

func TestNormalizeProviderGemini(t *testing.T) {
	assert.Equal(t, ProviderGoogle, normalizeProvider("gemini"))
	assert.Equal(t, ProviderGoogle, normalizeProvider("Gemini"))
}

func TestNormalizeProviderLocal(t *testing.T) {
	assert.Equal(t, ProviderOpenAI, normalizeProvider("local"))
	assert.Equal(t, ProviderOpenAI, normalizeProvider("ollama"))
	assert.Equal(t, ProviderOpenAI, normalizeProvider("openai-compatible"))
}

func TestNormalizeProviderPassthrough(t *testing.T) {
	assert.Equal(t, "openai", normalizeProvider("openai"))
	assert.Equal(t, "anthropic", normalizeProvider("Anthropic"))
}

func TestNormalizeProviderTrimsWhitespace(t *testing.T) {
	assert.Equal(t, ProviderGoogle, normalizeProvider("  gemini  "))
}

// ── isLocalProvider tests ──────────────────────────────────────────────

func TestIsLocalProviderTrue(t *testing.T) {
	assert.True(t, isLocalProvider("local"))
	assert.True(t, isLocalProvider("ollama"))
	assert.True(t, isLocalProvider("openai-compatible"))
	assert.True(t, isLocalProvider("  LOCAL  "))
}

func TestIsLocalProviderFalse(t *testing.T) {
	assert.False(t, isLocalProvider("openai"))
	assert.False(t, isLocalProvider("anthropic"))
	assert.False(t, isLocalProvider("google"))
}

// ── formatMetrics tests ────────────────────────────────────────────────

func TestFormatMetricsEmpty(t *testing.T) {
	assert.Equal(t, "No metrics available", formatMetrics(nil))
	assert.Equal(t, "No metrics available", formatMetrics(map[string]float64{}))
}

func TestFormatMetricsWithData(t *testing.T) {
	result := formatMetrics(map[string]float64{"cpu": 95.5})
	assert.Contains(t, result, "cpu: 95.50")
}

// ── formatTrends tests ─────────────────────────────────────────────────

func TestFormatTrendsEmpty(t *testing.T) {
	assert.Equal(t, "No trend data available", formatTrends(nil))
}

func TestFormatTrendsWithData(t *testing.T) {
	result := formatTrends(map[string]string{"cpu": "increasing"})
	assert.Contains(t, result, "cpu: increasing")
}

// ── formatAnomalies tests ──────────────────────────────────────────────

func TestFormatAnomaliesEmpty(t *testing.T) {
	assert.Equal(t, "No anomalies detected", formatAnomalies(nil))
}

func TestFormatAnomaliesWithData(t *testing.T) {
	result := formatAnomalies([]string{"CPU spike detected", "Memory leak"})
	assert.Contains(t, result, "CPU spike detected")
	assert.Contains(t, result, "Memory leak")
}

// ── parseAnalysisResponse tests ────────────────────────────────────────

func TestParseAnalysisResponseStructured(t *testing.T) {
	response := `{"summary":"Service degradation due to config change","root_cause":"Configuration change caused cascade failure","confidence":0.85,"recommendations":["Rollback the configuration change","Increase monitoring"]}`

	client := &LLMClient{provider: ProviderOpenAI}
	result := client.parseAnalysisResponse(response)

	assert.Equal(t, "Service degradation due to config change", result.Summary)
	assert.Equal(t, "Configuration change caused cascade failure", result.RootCause)
	assert.InDelta(t, 0.85, result.Confidence, 0.01)
	assert.Len(t, result.Recommendations, 2)
}

func TestParseAnalysisResponseUnstructured(t *testing.T) {
	response := "The system is experiencing normal behavior with no issues detected."

	client := &LLMClient{provider: ProviderOpenAI}
	result := client.parseAnalysisResponse(response)

	assert.NotEmpty(t, result.Summary, "Should use response as summary even if unstructured")
}
