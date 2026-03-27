package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/pkg/proto"
	metricspb "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
)

// Client is an LLM client
type Client interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	Complete(ctx context.Context, prompt string) (string, error)
	Analyze(ctx context.Context, data *AnalysisData) (*AnalysisResult, error)
}

// Provider is an LLM provider
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGoogle    Provider = "google"
	ProviderAzure     Provider = "azure"
)

// ChatRequest is a chat completion request
type ChatRequest struct {
	Messages    []Message  `json:"messages"`
	Model       string     `json:"model"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
	Temperature float64    `json:"temperature,omitempty"`
	Functions   []Function `json:"functions,omitempty"`
}

// Message is a chat message
type Message struct {
	Role    string `json:"role"` // system, user, assistant
	Content string `json:"content"`
}

// Function is a function definition for function calling
type Function struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ChatResponse is a chat completion response
type ChatResponse struct {
	Content      string        `json:"content"`
	Role         string        `json:"role"`
	FinishReason string        `json:"finish_reason"`
	Usage        Usage         `json:"usage"`
	FunctionCall *FunctionCall `json:"function_call,omitempty"`
}

// Usage represents token usage
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// FunctionCall represents a function call from the LLM
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// AnalysisData is data for LLM analysis
type AnalysisData struct {
	Metrics    []*metricspb.Metric `json:"metrics"`
	Logs       []string            `json:"logs"`
	Alerts     []*proto.Alert      `json:"alerts"`
	SLOs       []*proto.SLO        `json:"slos"`
	Context    string              `json:"context"`
	TimeWindow string              `json:"time_window"`
}

// AnalysisResult is the result of LLM analysis
type AnalysisResult struct {
	Summary         string            `json:"summary"`
	Issues          []string          `json:"issues"`
	Predictions     []Prediction      `json:"predictions"`
	Recommendations []string          `json:"recommendations"`
	Confidence      float64           `json:"confidence"`
	Reasoning       string            `json:"reasoning"`
	Metadata        map[string]string `json:"metadata"`
}

// Prediction represents a prediction
type Prediction struct {
	Type        string    `json:"type"`
	Description string    `json:"description"`
	WillHappen  bool      `json:"will_happen"`
	Confidence  float64   `json:"confidence"`
	TimeHorizon time.Time `json:"time_horizon"`
}

// Config configures an LLM client
type Config struct {
	Provider    Provider      `json:"provider"`
	Model       string        `json:"model"`
	APIKey      string        `json:"api_key"`
	BaseURL     string        `json:"base_url,omitempty"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	Timeout     time.Duration `json:"timeout"`
}

// OpenAIClient is an OpenAI client
type OpenAIClient struct {
	config Config
	client *http.Client
	logger *zap.Logger
}

// NewOpenAIClient creates a new OpenAI client
func NewOpenAIClient(config Config, logger *zap.Logger) *OpenAIClient {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	return &OpenAIClient{
		config: config,
		client: &http.Client{Timeout: config.Timeout},
		logger: logger.With(zap.String("component", "openai_client")),
	}
}

// Chat sends a chat completion request
func (c *OpenAIClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Set default model
	if req.Model == "" {
		req.Model = c.config.Model
	}

	// Make HTTP request to OpenAI API
	// In production, would use actual HTTP client
	c.logger.Debug("OpenAI chat request",
		zap.String("model", req.Model),
		zap.Int("messages", len(req.Messages)))

	return &ChatResponse{
		Content: "This is a simulated response",
		Role:    "assistant",
		Usage: Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}, nil
}

// Complete completes a prompt
func (c *OpenAIClient) Complete(ctx context.Context, prompt string) (string, error) {
	req := &ChatRequest{
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
	}

	resp, err := c.Chat(ctx, req)
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// Analyze analyzes data with the LLM
func (c *OpenAIClient) Analyze(ctx context.Context, data *AnalysisData) (*AnalysisResult, error) {
	// Build prompt from data
	prompt := c.buildAnalysisPrompt(data)

	// Call LLM
	response, err := c.Complete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM analysis failed: %w", err)
	}

	// Parse response into AnalysisResult
	return c.parseAnalysisResponse(response)
}

// buildAnalysisPrompt builds an analysis prompt
func (c *OpenAIClient) buildAnalysisPrompt(data *AnalysisData) string {
	return fmt.Sprintf(`Analyze the following system data and provide:
1. Summary of current state
2. Identified issues (if any)
3. Predictions for next hour
4. Recommended actions

Context: %s
Metrics: %d data points
Logs: %d entries
Alerts: %d active

Provide analysis in JSON format.`,
		data.Context,
		len(data.Metrics),
		len(data.Logs),
		len(data.Alerts))
}

// parseAnalysisResponse parses LLM response
func (c *OpenAIClient) parseAnalysisResponse(response string) (*AnalysisResult, error) {
	// Try to parse as JSON
	var result AnalysisResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		// If not JSON, create a simple result
		return &AnalysisResult{
			Summary:    response,
			Confidence: 0.5,
		}, nil
	}
	return &result, nil
}

// AnthropicClient is an Anthropic Claude client
type AnthropicClient struct {
	config Config
	client *http.Client
	logger *zap.Logger
}

// NewAnthropicClient creates a new Anthropic client
func NewAnthropicClient(config Config, logger *zap.Logger) *AnthropicClient {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.anthropic.com/v1"
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	return &AnthropicClient{
		config: config,
		client: &http.Client{Timeout: config.Timeout},
		logger: logger.With(zap.String("component", "anthropic_client")),
	}
}

// Chat sends a chat completion request
func (c *AnthropicClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Anthropic uses a different API format
	// In production, would make actual HTTP request

	c.logger.Debug("Anthropic chat request",
		zap.String("model", req.Model),
		zap.Int("messages", len(req.Messages)))

	return &ChatResponse{
		Content: "This is a simulated response from Claude",
		Role:    "assistant",
		Usage: Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}, nil
}

// Complete completes a prompt
func (c *AnthropicClient) Complete(ctx context.Context, prompt string) (string, error) {
	req := &ChatRequest{
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
	}

	resp, err := c.Chat(ctx, req)
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// Analyze analyzes data with the LLM
func (c *AnthropicClient) Analyze(ctx context.Context, data *AnalysisData) (*AnalysisResult, error) {
	prompt := c.buildAnalysisPrompt(data)
	response, err := c.Complete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM analysis failed: %w", err)
	}

	return c.parseAnalysisResponse(response)
}

// buildAnalysisPrompt builds an analysis prompt for Claude
func (c *AnthropicClient) buildAnalysisPrompt(data *AnalysisData) string {
	return fmt.Sprintf(`You are an expert SRE analyzing system telemetry.

Context: %s
Metrics available: %d
Log entries: %d
Active alerts: %d

Analyze this data and provide:
1. Current system status summary
2. Any detected anomalies or issues
3. Predictions for the next hour
4. Recommended remediation actions

Be concise and focus on actionable insights.`,
		data.Context,
		len(data.Metrics),
		len(data.Logs),
		len(data.Alerts))
}

// parseAnalysisResponse parses LLM response
func (c *AnthropicClient) parseAnalysisResponse(response string) (*AnalysisResult, error) {
	// Try to parse as JSON
	var result AnalysisResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return &AnalysisResult{
			Summary:    response,
			Confidence: 0.5,
		}, nil
	}
	return &result, nil
}

// NewClient creates a new LLM client based on provider
func NewClient(config Config, logger *zap.Logger) (Client, error) {
	switch config.Provider {
	case ProviderOpenAI:
		return NewOpenAIClient(config, logger), nil
	case ProviderAnthropic:
		return NewAnthropicClient(config, logger), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", config.Provider)
	}
}
