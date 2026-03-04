// Package ai provides the AI analysis module for the SRE controller.
//
// Architecture:
//
//	┌─────────────────────────────────────────────────────────────────────┐
//	│                         AI MODULE                                   │
//	├─────────────────────────────────────────────────────────────────────┤
//	│                                                                     │
//	│   Data Ingestion                                                    │
//	│   ┌─────────┐    ┌─────────┐    ┌─────────┐                        │
//	│   │ gRPC    │───▶│ Queue   │───▶│ Worker  │                        │
//	│   │ Server  │    │(Memory) │    │ Pool    │                        │
//	│   │         │    │         │    │         │                        │
//	│   └─────────┘    └─────────┘    └────┬────┘                        │
//	│                                      │                              │
//	│   Analysis Pipeline                  ▼                              │
//	│   ┌─────────────────────────────────────────────────────────────┐  │
//	│   │                                                             │  │
//	│   │  ┌────────────┐   ┌───────────┐   ┌─────────────┐          │  │
//	│   │  │ Classifier │──▶│ Suggester │──▶│ Explainer   │          │  │
//	│   │  │ (Rules+ML) │   │ (Rules+ML)│   │ (LLM)       │          │  │
//	│   │  └────────────┘   └───────────┘   └─────────────┘          │  │
//	│   │                                                             │  │
//	│   └─────────────────────────────────────────────────────────────┘  │
//	│                                                                     │
//	│   Output: Alerts, Suggestions, Explanations                         │
//	│                                                                     │
//	└─────────────────────────────────────────────────────────────────────┘
//
// The module supports both rule-based analysis (fast, reliable) and ML-powered
// analysis (via Python service or external API).
package ai

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/classifier"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/suggester"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/remediation" // New import
	"go.uber.org/zap"
)

// Module is the main AI analysis module
type Module struct {
	config      Config
	logger      *zap.Logger
	queue       queue.Queue
	classifier  *classifier.Classifier
	suggester   *suggester.Suggester
	remediation *remediation.Engine // Remediation integration

	// ML Client
	mlClient *GRPCMLClient

	// Workers
	workers  int
	workerWg sync.WaitGroup

	// Results
	mu            sync.RWMutex
	recentResults []AnalysisResult
	maxResults    int

	// Lifecycle
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
}

// Config holds AI module configuration
type Config struct {
	// Queue configuration
	Queue queue.Config `yaml:"queue"`

	// Classifier configuration
	Classifier classifier.Config `yaml:"classifier"`

	// Processing configuration
	Workers        int           `yaml:"workers"`
	BatchSize      int           `yaml:"batch_size"`
	ProcessTimeout time.Duration `yaml:"process_timeout"`

	// ML service configuration
	MLServiceAddr string `yaml:"ml_service_addr"`
	EnableML      bool   `yaml:"enable_ml"`

	// Results retention
	// Results retention
	MaxResults int `yaml:"max_results"`

	// Security
	TLSClientConfig ClientConfig `yaml:"tls"`

	// Automation
	AutoRemediate bool `yaml:"auto_remediate"`
}

// DefaultConfig returns default AI module configuration
func DefaultConfig() Config {
	return Config{
		Queue:          queue.DefaultConfig(),
		Classifier:     classifier.DefaultConfig(),
		Workers:        4,
		BatchSize:      10,
		ProcessTimeout: 30 * time.Second,
		MLServiceAddr:  "localhost:50051",
		EnableML:       false,
		MaxResults:     1000,
	}
}

// AnalysisResult represents the result of analyzing a data point
type AnalysisResult struct {
	NodeName        string                      `json:"node_name"`
	Timestamp       time.Time                   `json:"timestamp"`
	Classifications []classifier.Classification `json:"classifications"`
	Suggestions     []suggester.Suggestion      `json:"suggestions"`
	Explanation     *suggester.Explanation      `json:"explanation,omitempty"`
	ProcessedAt     time.Time                   `json:"processed_at"`
	ProcessingTime  time.Duration               `json:"processing_time"`
}

// New creates a new AI module
func New(cfg Config, logger *zap.Logger) (*Module, error) {
	if logger == nil {
		logger, _ = zap.NewDevelopment()
	}

	// Create queue
	q, err := queue.NewQueue(cfg.Queue, logger)
	if err != nil {
		return nil, err
	}

	// Create classifier
	cls := classifier.New(cfg.Classifier, logger)

	// Create suggester
	sug := suggester.New(logger)

	m := &Module{
		config:        cfg,
		logger:        logger.With(zap.String("component", "ai_module")),
		queue:         q,
		classifier:    cls,
		suggester:     sug,
		workers:       cfg.Workers,
		recentResults: make([]AnalysisResult, 0),
		maxResults:    cfg.MaxResults,
	}

	// Initialize ML Client
	if cfg.EnableML {
		// Use TLS config from module config
		cfg.TLSClientConfig.Address = cfg.MLServiceAddr

		client, err := NewGRPCMLClient(cfg.TLSClientConfig, logger)
		if err != nil {
			logger.Warn("failed to create ML client, falling back to rules", zap.Error(err))
		} else {
			m.mlClient = client
			cls.SetMLClient(client)
		}
	}

	if m.workers <= 0 {
		m.workers = 4
	}

	return m, nil
}

// SetRemediationEngine sets the remediation engine
func (m *Module) SetRemediationEngine(engine *remediation.Engine) {
	m.remediation = engine
}

// Start starts the AI module
func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = true
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()

	m.logger.Info("starting AI module",
		zap.Int("workers", m.workers),
		zap.Bool("ml_enabled", m.config.EnableML))

	// Start worker pool
	for i := 0; i < m.workers; i++ {
		m.workerWg.Add(1)
		go m.worker(i)
	}

	return nil
}

// Stop stops the AI module
func (m *Module) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = false
	m.mu.Unlock()

	m.logger.Info("stopping AI module")

	// Cancel context
	if m.cancel != nil {
		m.cancel()
	}

	// Close queue
	m.queue.Close()

	// Wait for workers
	m.workerWg.Wait()

	m.logger.Info("AI module stopped")
	return nil
}

// Ingest adds data to the analysis queue
func (m *Module) Ingest(ctx context.Context, data *queue.DataPoint) error {
	return m.queue.Enqueue(ctx, data)
}

// IngestMetrics is a convenience method for ingesting metrics
func (m *Module) IngestMetrics(ctx context.Context, nodeName string, metrics []queue.MetricData) error {
	data := &queue.DataPoint{
		NodeName:  nodeName,
		Timestamp: time.Now(),
		Metrics:   metrics,
	}
	return m.Ingest(ctx, data)
}

// IngestLogs is a convenience method for ingesting logs
func (m *Module) IngestLogs(ctx context.Context, nodeName string, logs []queue.LogEntry) error {
	data := &queue.DataPoint{
		NodeName:  nodeName,
		Timestamp: time.Now(),
		Logs:      logs,
	}
	return m.Ingest(ctx, data)
}

// Analyze performs synchronous analysis (bypass queue)
func (m *Module) Analyze(ctx context.Context, data *queue.DataPoint) (*AnalysisResult, error) {
	return m.processDataPoint(ctx, data)
}

// GetRecentResults returns recent analysis results
func (m *Module) GetRecentResults(limit int) []AnalysisResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 || limit > len(m.recentResults) {
		limit = len(m.recentResults)
	}

	// Return most recent first
	results := make([]AnalysisResult, limit)
	for i := 0; i < limit; i++ {
		results[i] = m.recentResults[len(m.recentResults)-1-i]
	}
	return results
}

// GetResultsByNode returns results for a specific node
func (m *Module) GetResultsByNode(nodeName string) []AnalysisResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []AnalysisResult
	for _, r := range m.recentResults {
		if r.NodeName == nodeName {
			results = append(results, r)
		}
	}
	return results
}

// Stats returns module statistics
func (m *Module) Stats() ModuleStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var queueStats queue.QueueStats
	if mq, ok := m.queue.(*queue.MemoryQueue); ok {
		queueStats = mq.Stats()
	} else {
		queueStats.Current = m.queue.Len()
	}

	// Count issues by severity
	issuesBySeverity := make(map[string]int)
	for _, r := range m.recentResults {
		for _, c := range r.Classifications {
			issuesBySeverity[string(c.Severity)]++
		}
	}

	return ModuleStats{
		Running:          m.running,
		Workers:          m.workers,
		QueueLength:      queueStats.Current,
		QueueCapacity:    queueStats.Capacity,
		TotalProcessed:   queueStats.Dequeued,
		TotalDropped:     queueStats.Dropped,
		ResultsStored:    len(m.recentResults),
		IssuesBySeverity: issuesBySeverity,
	}
}

// ModuleStats contains module statistics
type ModuleStats struct {
	Running          bool           `json:"running"`
	Workers          int            `json:"workers"`
	QueueLength      int            `json:"queue_length"`
	QueueCapacity    int            `json:"queue_capacity"`
	TotalProcessed   int64          `json:"total_processed"`
	TotalDropped     int64          `json:"total_dropped"`
	ResultsStored    int            `json:"results_stored"`
	IssuesBySeverity map[string]int `json:"issues_by_severity"`
}

// ============================================================================
// Workers
// ============================================================================

// worker processes data from the queue
func (m *Module) worker(id int) {
	defer m.workerWg.Done()

	m.logger.Debug("worker started", zap.Int("id", id))

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Debug("worker stopping", zap.Int("id", id))
			return
		default:
		}

		// Get batch from queue
		batch, err := m.queue.DequeueBatch(m.ctx, m.config.BatchSize)
		if err != nil {
			if err == queue.ErrQueueClosed || err == context.Canceled {
				return
			}
			m.logger.Warn("dequeue error", zap.Error(err))
			continue
		}

		// Process batch
		for _, data := range batch {
			result, err := m.processDataPoint(m.ctx, data)
			if err != nil {
				m.logger.Warn("processing error",
					zap.String("node", data.NodeName),
					zap.Error(err))
				continue
			}

			// Store result
			m.storeResult(result)
		}
	}
}

// processDataPoint analyzes a single data point
func (m *Module) processDataPoint(ctx context.Context, data *queue.DataPoint) (*AnalysisResult, error) {
	start := time.Now()

	// Step 1: Classify
	classifications, err := m.classifier.Classify(ctx, data)
	if err != nil {
		return nil, err
	}

	// Step 2: Generate suggestions for each classification
	var suggestions []suggester.Suggestion
	for _, c := range classifications {
		sugs, err := m.suggester.Suggest(ctx, c, data)
		if err != nil {
			m.logger.Warn("suggestion error", zap.Error(err))
			continue
		}
		suggestions = append(suggestions, sugs...)
	}

	// Step 3: Generate explanation for primary issue
	var explanation *suggester.Explanation
	if len(classifications) > 0 {
		exp, err := m.suggester.Explain(ctx, classifications[0], data)
		if err != nil {
			m.logger.Warn("explanation error", zap.Error(err))
		} else {
			explanation = exp
		}
	}

	// Step 4: Auto-Remediation (if enabled)
	if m.config.AutoRemediate && m.remediation != nil && len(suggestions) > 0 {
		// Only remediate high confidence, high severity issues automatically
		// Implementation policy: Pick the first high-confidence suggestion
		for _, sug := range suggestions {
			if sug.Confidence > 0.9 && sug.Type != "" {
				// Map suggestion type to action type
				actionType := remediation.ActionType(string(sug.Type))

				req := remediation.RemediationRequest{
					ID:          fmt.Sprintf("auto-%d", time.Now().UnixNano()),
					Action:      actionType,
					Target:      data.NodeName, // Simplification
					Reason:      sug.Reasoning,
					RequestedBy: "ai-module",
				}

				// Execute async to not block analysis
				go func(r remediation.RemediationRequest) {
					if err := m.remediation.Execute(context.Background(), r); err != nil {
						m.logger.Error("auto-remediation failed", zap.Error(err))
					}
				}(req)
				break // Do one remediation at a time per analysis
			}
		}
	}

	result := &AnalysisResult{
		NodeName:        data.NodeName,
		Timestamp:       data.Timestamp,
		Classifications: classifications,
		Suggestions:     suggestions,
		Explanation:     explanation,
		ProcessedAt:     time.Now(),
		ProcessingTime:  time.Since(start),
	}

	return result, nil
}

// storeResult stores an analysis result
func (m *Module) storeResult(result *AnalysisResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recentResults = append(m.recentResults, *result)

	// Trim to max size
	if len(m.recentResults) > m.maxResults {
		m.recentResults = m.recentResults[len(m.recentResults)-m.maxResults:]
	}

	// Log significant issues
	for _, c := range result.Classifications {
		if c.Severity == classifier.SeverityCritical || c.Severity == classifier.SeverityError {
			m.logger.Info("issue detected",
				zap.String("node", result.NodeName),
				zap.String("category", string(c.Category)),
				zap.String("severity", string(c.Severity)),
				zap.Float64("confidence", c.Confidence),
				zap.String("description", c.Description))
		}
	}
}
