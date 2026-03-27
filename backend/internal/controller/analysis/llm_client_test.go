package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
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

func TestDefaultGoogleModelUsesCurrentAlias(t *testing.T) {
	assert.Equal(t, "gemini-flash-latest", defaultModels[ProviderGoogle])
}

func TestNewLLMClientUsesConfiguredAPIKeyEnv(t *testing.T) {
	t.Setenv("CUSTOM_LLM_KEY_ENV", "present")
	t.Setenv(EnvLLMProvider, "")
	t.Setenv(EnvLLMModel, "")

	client, err := NewLLMClient(LLMClientConfig{
		Provider:  "gemini",
		Model:     "gemini-flash-latest",
		APIKeyEnv: "CUSTOM_LLM_KEY_ENV",
		Timeout:   5 * time.Second,
	}, zap.NewNop())

	assert.NoError(t, err)
	if assert.NotNil(t, client) {
		assert.Equal(t, ProviderGoogle, client.Provider())
		assert.Equal(t, "gemini-flash-latest", client.Model())
	}
}

func TestNewLLMClientAllowsLocalProviderWithoutAPIKey(t *testing.T) {
	t.Setenv(EnvLLMAPIKey, "")
	t.Setenv(EnvLLMProvider, "")
	t.Setenv(EnvLLMModel, "")
	t.Setenv(EnvLLMBaseURL, "")

	client, err := NewLLMClient(LLMClientConfig{
		Provider: "ollama",
		Timeout:  5 * time.Second,
	}, zap.NewNop())

	assert.NoError(t, err)
	if assert.NotNil(t, client) {
		assert.Equal(t, ProviderOpenAI, client.Provider())
		assert.Equal(t, "llama3.1", client.Model())
		assert.Equal(t, "http://localhost:11434/v1", client.baseURL)
	}
}

func TestNormalizeGoogleModel(t *testing.T) {
	assert.Equal(t, "gemini-2.0-flash", normalizeGoogleModel("models/gemini-2.0-flash"))
	assert.Equal(t, "gemini-flash-latest", normalizeGoogleModel("gemini-flash-latest"))
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

func TestParseAnalysisResponseStructuredWithinFencedJSON(t *testing.T) {
	response := "Thought: inspect rollout context.\nFinal:\n```json\n{\"summary\":\"Transient deployment warmup latency\",\"root_cause\":\"Recent rollout warmed caches and briefly raised p95 latency\",\"confidence\":0.91,\"recommendations\":[\"Watch p95 until caches stabilize\"]}\n```"

	client := &LLMClient{provider: ProviderGoogle}
	result := client.parseAnalysisResponse(response)

	assert.Equal(t, "Transient deployment warmup latency", result.Summary)
	assert.Equal(t, "Recent rollout warmed caches and briefly raised p95 latency", result.RootCause)
	assert.InDelta(t, 0.91, result.Confidence, 0.01)
	assert.Equal(t, []string{"Watch p95 until caches stabilize"}, result.Recommendations)
}
