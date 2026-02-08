// Package controller provides AI module integration.
//
// This file extends the controller with the dedicated AI analysis module.
package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/classifier"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"go.uber.org/zap"
)

// AIIntegration manages the AI module within the controller
type AIIntegration struct {
	module *ai.Module
	api    *ai.APIHandler
	logger *zap.Logger
}

// AIConfig configures the AI integration
type AIConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Workers       int    `yaml:"workers"`
	MLServiceAddr string `yaml:"ml_service_addr"`
	EnableML      bool   `yaml:"enable_ml"`
	IngestLogs    bool   `yaml:"ingest_logs"`
}

// NewAIIntegration creates a new AI integration
func NewAIIntegration(cfg AIConfig, logger *zap.Logger) (*AIIntegration, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	// Configure AI module
	aiCfg := ai.DefaultConfig()
	aiCfg.Workers = cfg.Workers
	aiCfg.EnableML = cfg.EnableML
	aiCfg.MLServiceAddr = cfg.MLServiceAddr
	aiCfg.Classifier = classifier.DefaultConfig()
	aiCfg.Classifier.EnableML = cfg.EnableML
	aiCfg.Classifier.MLServiceAddr = cfg.MLServiceAddr

	// Create module
	module, err := ai.New(aiCfg, logger)
	if err != nil {
		return nil, err
	}

	// Create API handler
	api := ai.NewAPIHandler(module, logger)

	return &AIIntegration{
		module: module,
		api:    api,
		logger: logger.With(zap.String("component", "ai_integration")),
	}, nil
}

// Start starts the AI module
func (i *AIIntegration) Start(ctx context.Context) error {
	return i.module.Start(ctx)
}

// Stop stops the AI module
func (i *AIIntegration) Stop() error {
	return i.module.Stop()
}

// RegisterHandlers registers AI API endpoints
func (c *Controller) RegisterAIHandlers(mux *http.ServeMux, integration *AIIntegration) {
	if integration == nil {
		return
	}
	integration.api.RegisterHandlers(mux, c.withCORS)
}

// FeedMetricsToAI feeds metrics to the AI module
func (c *Controller) FeedMetricsToAI(integration *AIIntegration, nodeName string, metrics []AgentMetric) {
	if integration == nil {
		return
	}

	// Convert metrics
	aiMetrics := make([]queue.MetricData, 0, len(metrics))
	for _, m := range metrics {
		aiMetrics = append(aiMetrics, queue.MetricData{
			Name:      m.Name,
			Value:     m.Value,
			Timestamp: m.Timestamp,
			Labels:    m.Labels,
		})
	}

	// Feed to AI module (async)
	// We use a background context to avoid blocking on ingest if the request context is cancelled
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := integration.module.IngestMetrics(ctx, nodeName, aiMetrics); err != nil {
			c.logger.Warn("failed to ingest metrics to AI",
				zap.String("node", nodeName),
				zap.Error(err))
		}
	}()
}
