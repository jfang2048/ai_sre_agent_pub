// Package analysis provides LLM integration for enhanced root cause analysis.
//
// API keys are loaded EXCLUSIVELY from environment variables:
//   - SRE_AGENT_LLM_API_KEY: API key for the LLM provider
//   - SRE_AGENT_LLM_PROVIDER: Provider name (openai, anthropic, google/gemini, local)
//   - SRE_AGENT_LLM_MODEL: Model to use (optional, has defaults)
//   - SRE_AGENT_LLM_BASE_URL: Custom base URL (optional, for proxies)
//
// SECURITY: API keys are NEVER logged, stored in configs, or exposed via API.
package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
)

// LLMClient implements LLMAnalyzer using external LLM APIs
type LLMClient struct {
	provider   string
	model      string
	apiKey     string
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
}

// LLMClientConfig holds configuration for the LLM client
type LLMClientConfig struct {
	Provider           string        `yaml:"provider"`
	Model              string        `yaml:"model"`
	BaseURL            string        `yaml:"base_url"`
	APIKey             string        `yaml:"-"`
	APIKeyEnv          string        `yaml:"api_key_env"`
	CodePath           string        `yaml:"code_path"`
	DisableEnvOverride bool          `yaml:"disable_env_override"`
	Timeout            time.Duration `yaml:"timeout"`
}

// Environment variable names for secure configuration
const (
	EnvLLMAPIKey   = "SRE_AGENT_LLM_API_KEY"
	EnvLLMProvider = "SRE_AGENT_LLM_PROVIDER"
	EnvLLMModel    = "SRE_AGENT_LLM_MODEL"
	EnvLLMBaseURL  = "SRE_AGENT_LLM_BASE_URL"
	EnvLLMDebug    = "SRE_AGENT_LLM_DEBUG"
)

// Provider constants
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderGoogle    = "google"
)

// Default models per provider
var defaultModels = map[string]string{
	ProviderOpenAI:    "gpt-4o-mini",
	ProviderAnthropic: "claude-3-haiku-20240307",
	ProviderGoogle:    "gemini-flash-latest",
}

// Default base URLs per provider
var defaultBaseURLs = map[string]string{
	ProviderOpenAI:    "https://api.openai.com/v1",
	ProviderAnthropic: "https://api.anthropic.com/v1",
	ProviderGoogle:    "https://generativelanguage.googleapis.com/v1beta",
}

// NewLLMClient creates a new LLM client from environment variables
// Returns nil if no API key is configured (LLM analysis disabled)
func NewLLMClient(cfg LLMClientConfig, logger *zap.Logger) (*LLMClient, error) {
	keyEnv := strings.TrimSpace(cfg.APIKeyEnv)
	if keyEnv == "" {
		keyEnv = EnvLLMAPIKey
	}
	codePath := strings.TrimSpace(cfg.CodePath)
	if codePath == "" {
		codePath = "analysis"
	}

	provider := resolveLLMSetting(cfg.Provider, EnvLLMProvider, cfg.DisableEnvOverride)
	if provider == "" {
		provider = ProviderOpenAI
	}
	rawProvider := provider
	provider = normalizeProvider(provider)
	defaultModel := defaultModels[provider]
	defaultBaseURL := defaultBaseURLs[provider]
	if isLocalProvider(rawProvider) {
		defaultModel = "llama3.1"
		defaultBaseURL = "http://localhost:11434/v1"
	}

	model := resolveLLMSetting(cfg.Model, EnvLLMModel, cfg.DisableEnvOverride)
	if model == "" {
		model = defaultModel
	}

	baseURL := resolveLLMSetting(cfg.BaseURL, EnvLLMBaseURL, cfg.DisableEnvOverride)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(keyEnv))
	}
	if apiKey == "" && !isLocalProvider(rawProvider) {
		logLLMRuntime(logger, codePath, "", keyEnv, false, "disabled")
		logger.Info("LLM API key not configured, LLM analysis disabled",
			zap.String("env_var", keyEnv))
		return nil, nil
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	client := &LLMClient{
		provider: provider,
		model:    model,
		apiKey:   apiKey,
		baseURL:  baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger.With(zap.String("component", "llm_client")),
	}

	logLLMRuntime(logger, codePath, provider, keyEnv, true, "live")

	// Log configuration (never log API key!)
	logger.Info("LLM client configured",
		zap.String("provider", provider),
		zap.String("model", model),
		zap.String("base_url", baseURL),
		zap.Duration("timeout", timeout))

	return client, nil
}

func (c *LLMClient) Provider() string { return c.provider }

func (c *LLMClient) Model() string { return c.model }

func logLLMRuntime(logger *zap.Logger, codePath, provider, keyEnv string, keyPresent bool, mode string) {
	if logger == nil || !parseDebugBool(os.Getenv(EnvLLMDebug)) {
		return
	}
	logger.Info("llm runtime selected",
		zap.String("code_path", strings.TrimSpace(codePath)),
		zap.String("provider", strings.TrimSpace(provider)),
		zap.String("api_key_env", strings.TrimSpace(keyEnv)),
		zap.Bool("api_key_present", keyPresent),
		zap.String("mode", strings.TrimSpace(mode)),
	)
}

func parseDebugBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func resolveLLMSetting(configValue, envName string, configWins bool) string {
	configValue = strings.TrimSpace(configValue)
	envValue := strings.TrimSpace(os.Getenv(envName))
	if configWins {
		if configValue != "" {
			return configValue
		}
		return envValue
	}
	if envValue != "" {
		return envValue
	}
	return configValue
}

// Analyze performs LLM-based analysis
func (c *LLMClient) Analyze(ctx context.Context, data AnalysisInput) (*LLMAnalysisResult, error) {
	prompt := c.buildAnalysisPrompt(data)

	c.logger.Debug("sending analysis request to LLM",
		zap.String("node", data.NodeName))

	response, err := c.CompletePrompt(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM API call failed: %w", err)
	}

	// Parse response
	result := c.parseAnalysisResponse(response)
	return result, nil
}

// CompletePrompt sends a raw prompt to the configured LLM provider and returns the raw text response.
func (c *LLMClient) CompletePrompt(ctx context.Context, prompt string) (string, error) {
	switch c.provider {
	case ProviderOpenAI:
		return c.callOpenAI(ctx, prompt)
	case ProviderAnthropic:
		return c.callAnthropic(ctx, prompt)
	case ProviderGoogle:
		return c.callGoogle(ctx, prompt)
	default:
		return "", fmt.Errorf("unsupported provider: %s", c.provider)
	}
}

// buildAnalysisPrompt creates a prompt for RCA analysis (ReAct-style scratchpad)
func (c *LLMClient) buildAnalysisPrompt(data AnalysisInput) string {
	schemaJSON, _ := json.Marshal(data.Schema)
	return fmt.Sprintf(`You are an SRE agent. Use ReAct-style reasoning: write brief steps with headings Thought:, Observation:, Action:, then end with Final:.
Use only the provided JSON as truth:
%s

Respond with JSON containing: summary, root_cause, confidence (0-1), recommendations (array of strings), actions (array of strings).`,
		string(schemaJSON))
}

// callOpenAI makes a request to OpenAI API
func (c *LLMClient) callOpenAI(ctx context.Context, prompt string) (string, error) {
	url := fmt.Sprintf("%s/chat/completions", c.baseURL)

	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are an expert SRE assistant for root cause analysis."},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
		"max_tokens":  1000,
	}

	return c.makeJSONRequest(ctx, url, reqBody, "Authorization", "Bearer "+c.apiKey)
}

// callAnthropic makes a request to Anthropic API
func (c *LLMClient) callAnthropic(ctx context.Context, prompt string) (string, error) {
	url := fmt.Sprintf("%s/messages", c.baseURL)

	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 1000,
	}

	return c.makeJSONRequestWithHeaders(ctx, url, reqBody, map[string]string{
		"x-api-key":         c.apiKey,
		"anthropic-version": "2023-06-01",
	})
}

// callGoogle makes a request to Google AI API
func (c *LLMClient) callGoogle(ctx context.Context, prompt string) (string, error) {
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.baseURL, normalizeGoogleModel(c.model), c.apiKey)

	reqBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": prompt},
				},
			},
		},
	}

	return c.makeJSONRequest(ctx, url, reqBody, "", "")
}

// makeJSONRequest makes a JSON HTTP request
func (c *LLMClient) makeJSONRequest(ctx context.Context, url string, body interface{}, authHeader, authValue string) (string, error) {
	headers := make(map[string]string)
	if authHeader != "" {
		headers[authHeader] = authValue
	}
	return c.makeJSONRequestWithHeaders(ctx, url, body, headers)
}

// makeJSONRequestWithHeaders makes a JSON HTTP request with custom headers
func (c *LLMClient) makeJSONRequestWithHeaders(ctx context.Context, url string, body interface{}, headers map[string]string) (string, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Extract content based on provider response format
	return c.extractContent(respBody)
}

// extractContent extracts the text content from provider-specific response format
func (c *LLMClient) extractContent(respBody []byte) (string, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	switch c.provider {
	case ProviderOpenAI:
		if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if message, ok := choice["message"].(map[string]interface{}); ok {
					if content, ok := message["content"].(string); ok {
						return content, nil
					}
				}
			}
		}

	case ProviderAnthropic:
		if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
			if block, ok := content[0].(map[string]interface{}); ok {
				if text, ok := block["text"].(string); ok {
					return text, nil
				}
			}
		}

	case ProviderGoogle:
		if candidates, ok := result["candidates"].([]interface{}); ok && len(candidates) > 0 {
			if candidate, ok := candidates[0].(map[string]interface{}); ok {
				if content, ok := candidate["content"].(map[string]interface{}); ok {
					if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
						if part, ok := parts[0].(map[string]interface{}); ok {
							if text, ok := part["text"].(string); ok {
								return text, nil
							}
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("failed to extract content from response")
}

// parseAnalysisResponse parses the LLM response into structured result
func (c *LLMClient) parseAnalysisResponse(response string) *LLMAnalysisResult {
	result := &LLMAnalysisResult{
		Summary:    "Unable to parse LLM response",
		Confidence: 0.0,
	}

	// Try to parse as JSON
	var parsed struct {
		Summary         string   `json:"summary"`
		RootCause       string   `json:"root_cause"`
		Confidence      float64  `json:"confidence"`
		Recommendations []string `json:"recommendations"`
	}

	if err := json.Unmarshal([]byte(response), &parsed); err == nil {
		result.Summary = parsed.Summary
		result.RootCause = parsed.RootCause
		result.Confidence = parsed.Confidence
		result.Recommendations = parsed.Recommendations
	} else if candidate := extractStructuredJSON(response); candidate != "" {
		if err := json.Unmarshal([]byte(candidate), &parsed); err == nil {
			result.Summary = parsed.Summary
			result.RootCause = parsed.RootCause
			result.Confidence = parsed.Confidence
			result.Recommendations = parsed.Recommendations
			return result
		}
	}

	if result.Summary == "Unable to parse LLM response" && result.RootCause == "" && result.Confidence == 0 && len(result.Recommendations) == 0 {
		// Fallback: use raw response as summary
		result.Summary = response
		result.Confidence = 0.5
	}

	return result
}

var fencedJSONRE = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

func extractStructuredJSON(response string) string {
	response = strings.TrimSpace(response)
	if response == "" {
		return ""
	}
	if match := fencedJSONRE.FindStringSubmatch(response); len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	start := strings.IndexByte(response, '{')
	end := strings.LastIndexByte(response, '}')
	if start >= 0 && end > start {
		return strings.TrimSpace(response[start : end+1])
	}
	return ""
}

func normalizeProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case "gemini":
		return ProviderGoogle
	case "local", "ollama", "openai-compatible":
		return ProviderOpenAI
	default:
		return normalized
	}
}

func normalizeGoogleModel(model string) string {
	return strings.TrimPrefix(strings.TrimSpace(model), "models/")
}

func isLocalProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "local", "ollama", "openai-compatible":
		return true
	default:
		return false
	}
}

// Helper functions for prompt formatting

func formatMetrics(metrics map[string]float64) string {
	if len(metrics) == 0 {
		return "No metrics available"
	}

	result := ""
	for name, value := range metrics {
		result += fmt.Sprintf("- %s: %.2f\n", name, value)
	}
	return result
}

func formatTrends(trends map[string]string) string {
	if len(trends) == 0 {
		return "No trend data available"
	}

	result := ""
	for name, trend := range trends {
		result += fmt.Sprintf("- %s: %s\n", name, trend)
	}
	return result
}

func formatAnomalies(anomalies []string) string {
	if len(anomalies) == 0 {
		return "No anomalies detected"
	}

	result := ""
	for _, a := range anomalies {
		result += fmt.Sprintf("- %s\n", a)
	}
	return result
}
