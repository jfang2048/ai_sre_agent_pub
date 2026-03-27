package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"go.uber.org/zap"
)

type analysisLLMAdapter struct {
	client *analysis.LLMClient
}

func (a *analysisLLMAdapter) Provider() string { return a.client.Provider() }

func (a *analysisLLMAdapter) Model() string { return a.client.Model() }

func (a *analysisLLMAdapter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	prompt := strings.TrimSpace(systemPrompt)
	userPrompt = strings.TrimSpace(userPrompt)
	if prompt == "" {
		prompt = userPrompt
	} else if userPrompt != "" {
		prompt = prompt + "\n\n" + userPrompt
	}
	return a.client.CompletePrompt(ctx, prompt)
}

func canonicalLLMProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gemini":
		return analysis.ProviderGoogle
	case "":
		return "openai"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func llmDebugEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(analysis.EnvLLMDebug))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func logLLMRuntimeSelection(logger *zap.Logger, codePath, provider, keyEnv string, keyPresent bool, mode string) {
	if logger == nil || !llmDebugEnabled() {
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

func newCanonicalLLMAdapter(provider, model, baseURL, apiKey, keyEnv, codePath string, timeout time.Duration, logger *zap.Logger) (llmClient, error) {
	if strings.TrimSpace(keyEnv) == "" {
		keyEnv = analysis.EnvLLMAPIKey
	}
	client, err := analysis.NewLLMClient(analysis.LLMClientConfig{
		Provider:           provider,
		Model:              model,
		BaseURL:            baseURL,
		APIKey:             strings.TrimSpace(apiKey),
		APIKeyEnv:          strings.TrimSpace(keyEnv),
		CodePath:           strings.TrimSpace(codePath),
		DisableEnvOverride: true,
		Timeout:            timeout,
	}, logger)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("canonical llm client unavailable")
	}
	return &analysisLLMAdapter{client: client}, nil
}
