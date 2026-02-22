// Package controller provides analysis integration for the controller.
//
// This file extends the controller with root cause analysis capabilities
// by integrating the analysis engine and exposing analysis API endpoints.
package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

// AnalysisConfig configures the analysis subsystem
type AnalysisConfig struct {
	Enabled             bool          `yaml:"enabled"`
	Interval            time.Duration `yaml:"interval"`
	ThresholdAlerts     bool          `yaml:"threshold_alerts"`
	AnomalyDetection    bool          `yaml:"anomaly_detection"`
	CorrelationAnalysis bool          `yaml:"correlation_analysis"`
	LLMEnabled          bool          `yaml:"llm_enabled"`
	LLMProvider         string        `yaml:"llm_provider"`
	LLMModel            string        `yaml:"llm_model"`
}

// DefaultAnalysisConfig returns default analysis configuration
func DefaultAnalysisConfig() AnalysisConfig {
	return AnalysisConfig{
		Enabled:             true,
		Interval:            30 * time.Second,
		ThresholdAlerts:     true,
		AnomalyDetection:    true,
		CorrelationAnalysis: true,
		LLMEnabled:          false, // Requires API key
		LLMProvider:         "openai",
		LLMModel:            "gpt-4o-mini",
	}
}

// AnalysisExtension adds analysis capabilities to the controller
type AnalysisExtension struct {
	engine *analysis.Engine
	logger *zap.Logger
	config AnalysisConfig
	mu     sync.Mutex
	cache  map[string]evidenceCacheEntry
	store  *ingest.MemoryStore
}

type evidenceCacheEntry struct {
	pack      analysis.EvidencePack
	timestamp time.Time
}

type evidenceProvider struct {
	store *ingest.MemoryStore
}

func (p evidenceProvider) EvidenceForNode(nodeName string) (processes []analysis.ProcessSummary, logs []analysis.LogSummary) {
	if p.store == nil {
		return nil, nil
	}
	snapshot := p.store.Node(nodeName)
	if snapshot == nil {
		return nil, nil
	}
	return analysis.SummarizeProcesses(snapshot.Processes, 5), analysis.SummarizeLogs(snapshot.Logs, 5)
}

// NewAnalysisExtension creates a new analysis extension
func NewAnalysisExtension(cfg AnalysisConfig, logger *zap.Logger) (*AnalysisExtension, error) {
	engineCfg := analysis.Config{
		EnableThresholdAlerts:  cfg.ThresholdAlerts,
		EnableAnomalyDetection: cfg.AnomalyDetection,
		EnableCorrelation:      cfg.CorrelationAnalysis,
		EnableLLMAnalysis:      cfg.LLMEnabled,
		AnalysisInterval:       cfg.Interval,
		LLMProvider:            cfg.LLMProvider,
		LLMModel:               cfg.LLMModel,
	}

	engine, err := analysis.New(engineCfg, logger)
	if err != nil {
		return nil, err
	}

	// Try to initialize LLM client from environment
	if cfg.LLMEnabled {
		llmCfg := analysis.LLMClientConfig{
			Provider: cfg.LLMProvider,
			Model:    cfg.LLMModel,
			Timeout:  30 * time.Second,
		}
		llmClient, err := analysis.NewLLMClient(llmCfg, logger)
		if err != nil {
			logger.Warn("failed to initialize LLM client", zap.Error(err))
		} else if llmClient != nil {
			engine.SetLLMClient(llmClient)
		}
	}

	return &AnalysisExtension{
		engine: engine,
		logger: logger.With(zap.String("component", "analysis_ext")),
		config: cfg,
		cache:  make(map[string]evidenceCacheEntry),
	}, nil
}

func (a *AnalysisExtension) SetIngestStore(store *ingest.MemoryStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.store = store
	if a.engine != nil {
		a.engine.SetEvidenceProvider(evidenceProvider{store: store})
	}
}

// Engine returns the underlying analysis engine
func (a *AnalysisExtension) Engine() *analysis.Engine {
	return a.engine
}

// RegisterAnalysisHandlers registers analysis API endpoints
func (c *Controller) RegisterAnalysisHandlers(mux *http.ServeMux, ext *AnalysisExtension) {
	// Analysis API endpoints
	mux.HandleFunc("/api/v1/analysis/alerts", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		handleGetAlerts(w, r, ext)
	}))

	mux.HandleFunc("/api/v1/analysis/anomalies", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		handleGetAnomalies(w, r, ext)
	}))

	mux.HandleFunc("/api/v1/analysis/rca", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		handleGetRCA(w, r, ext)
	}))

	mux.HandleFunc("/api/v1/analysis/status", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		handleAnalysisStatus(w, r, ext)
	}))

	mux.HandleFunc("/api/v1/analysis/evidence/", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		handleGetEvidence(w, r, ext)
	}))

	c.logger.Info("analysis API endpoints registered")
}

// FeedMetricsToAnalysis feeds collected metrics to the analysis engine
func (c *Controller) FeedMetricsToAnalysis(ext *AnalysisExtension, nodeName string, metrics []AgentMetric) {
	samples := make([]analysis.MetricSample, 0, len(metrics))
	for _, m := range metrics {
		samples = append(samples, analysis.MetricSample{
			Name:      m.Name,
			Value:     m.Value,
			Timestamp: m.Timestamp,
			Labels:    m.Labels,
		})
	}
	ext.engine.IngestMetrics(nodeName, samples)
}

// handleGetAlerts handles GET /api/v1/analysis/alerts
func handleGetAlerts(w http.ResponseWriter, r *http.Request, ext *AnalysisExtension) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	alerts := ext.engine.GetAlerts()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"alerts":    alerts,
		"count":     len(alerts),
		"timestamp": time.Now(),
	})
}

// handleGetAnomalies handles GET /api/v1/analysis/anomalies
func handleGetAnomalies(w http.ResponseWriter, r *http.Request, ext *AnalysisExtension) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	anomalies := ext.engine.GetAnomalies()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"anomalies": anomalies,
		"count":     len(anomalies),
		"timestamp": time.Now(),
	})
}

// handleGetRCA handles GET /api/v1/analysis/rca
func handleGetRCA(w http.ResponseWriter, r *http.Request, ext *AnalysisExtension) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rcas := ext.engine.GetRCAs()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"root_cause_analyses": rcas,
		"count":               len(rcas),
		"timestamp":           time.Now(),
	})
}

// handleAnalysisStatus handles GET /api/v1/analysis/status
func handleAnalysisStatus(w http.ResponseWriter, r *http.Request, ext *AnalysisExtension) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	alerts := ext.engine.GetAlerts()
	anomalies := ext.engine.GetAnomalies()
	rcas := ext.engine.GetRCAs()

	// Count by severity
	criticalCount := 0
	warningCount := 0
	for _, a := range alerts {
		if a.ResolvedAt == nil {
			switch a.Severity {
			case analysis.SeverityCritical:
				criticalCount++
			case analysis.SeverityWarning:
				warningCount++
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "active",
		"config": map[string]interface{}{
			"threshold_alerts":     ext.config.ThresholdAlerts,
			"anomaly_detection":    ext.config.AnomalyDetection,
			"correlation_analysis": ext.config.CorrelationAnalysis,
			"llm_enabled":          ext.config.LLMEnabled,
			"interval":             ext.config.Interval.String(),
		},
		"summary": map[string]interface{}{
			"total_alerts": len(alerts),
			"critical":     criticalCount,
			"warning":      warningCount,
			"anomalies":    len(anomalies),
			"rca_count":    len(rcas),
		},
		"timestamp": time.Now(),
	})
}

// handleGetEvidence returns a compact evidence pack for a node.
func handleGetEvidence(w http.ResponseWriter, r *http.Request, ext *AnalysisExtension) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodeName := strings.TrimPrefix(r.URL.Path, "/api/v1/analysis/evidence/")
	if nodeName == "" {
		http.Error(w, "node name required", http.StatusBadRequest)
		return
	}

	pack, ok := ext.getEvidencePack(nodeName)
	if !ok {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pack)
}

func (ext *AnalysisExtension) getEvidencePack(nodeName string) (analysis.EvidencePack, bool) {
	const cacheTTL = 5 * time.Second

	ext.mu.Lock()
	if entry, ok := ext.cache[nodeName]; ok {
		if time.Since(entry.timestamp) < cacheTTL {
			ext.mu.Unlock()
			return entry.pack, true
		}
	}
	ext.mu.Unlock()

	metrics := ext.engine.GetNodeMetricsSnapshot(nodeName)
	if metrics == nil {
		return analysis.EvidencePack{}, false
	}

	alerts := ext.engine.GetAlerts()
	alertIDs := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		if alert.NodeName == nodeName && alert.ResolvedAt == nil {
			alertIDs = append(alertIDs, alert.ID)
		}
	}

	anomalies := ext.engine.GetAnomalies()
	anomalyTexts := make([]string, 0, len(anomalies))
	for _, anomaly := range anomalies {
		if anomaly.NodeName != nodeName {
			continue
		}
		if anomaly.Reason != "" {
			anomalyTexts = append(anomalyTexts, anomaly.Reason)
		} else {
			anomalyTexts = append(anomalyTexts, anomaly.MetricName)
		}
	}

	var processes []analysis.ProcessSummary
	var logs []analysis.LogSummary

	ext.mu.Lock()
	store := ext.store
	ext.mu.Unlock()

	if store != nil {
		if snapshot := store.Node(nodeName); snapshot != nil {
			processes = analysis.SummarizeProcesses(snapshot.Processes, 5)
			logs = analysis.SummarizeLogs(snapshot.Logs, 5)
		}
	}

	pack := analysis.BuildEvidencePack(nodeName, metrics, alertIDs, anomalyTexts, "evidence pack snapshot", processes, logs)

	ext.mu.Lock()
	ext.cache[nodeName] = evidenceCacheEntry{pack: pack, timestamp: time.Now()}
	ext.mu.Unlock()

	return pack, true
}
