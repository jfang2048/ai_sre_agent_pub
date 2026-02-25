// Package controller implements the control node that aggregates metrics from probes.
//
// Terminology:
//   - Probe: The controlled-side data collector on monitored hosts
//   - Controller: This component - the central aggregation server
//   - Agent: The overall AI SRE system (NOT the probe component)
package controller

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	ctragent "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agent"
	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidents"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inventory"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/k8sview"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/orchestration"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/collections/ring"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/metrics/prom"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/middleware"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

var spaAssetRefRe = regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)

// NodeConfig represents a configured probe node
type NodeConfig struct {
	Name    string            `yaml:"name" json:"name"`
	Address string            `yaml:"address" json:"address"`
	Labels  map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// Config holds controller configuration
type Config struct {
	ListenAddr     string        `yaml:"listen"`
	GRPCListenAddr string        `yaml:"grpc_listen"` // For Push model
	ScrapeInterval time.Duration `yaml:"scrape_interval"`
	ScrapeTimeout  time.Duration `yaml:"scrape_timeout"`
	Nodes          []NodeConfig  `yaml:"nodes"`
	WebPath        string        `yaml:"web_path"`
	LogLevel       string        `yaml:"log_level"`
	Version        string        `yaml:"version"`

	Auth          AuthConfig           `yaml:"auth"`
	Analysis      AnalysisConfig       `yaml:"analysis"`
	Checks        ChecksConfig         `yaml:"checks"`
	Orchestration orchestration.Config `yaml:"orchestration"`
	Kubernetes    k8sview.Config       `yaml:"kubernetes"`
	Inventory     inventory.Config     `yaml:"inventory"`
	Agent         ctragent.Config      `yaml:"agent"`
	GPU           gpuobs.Config        `yaml:"gpu"`
	Incidents     incidents.Config     `yaml:"incidents"`
}

// DefaultConfig returns a default configuration
func DefaultConfig() Config {
	return Config{
		ListenAddr:     ":8080",
		GRPCListenAddr: ":9090",
		ScrapeInterval: 15 * time.Second,
		ScrapeTimeout:  10 * time.Second,
		Nodes:          []NodeConfig{},
		WebPath:        "./web",
		LogLevel:       "info",
		Version:        "v0.4",
		Auth:           DefaultAuthConfig(),
		Analysis:       DefaultAnalysisConfig(),
		Checks:         DefaultChecksConfig(),
		Orchestration:  orchestration.DefaultConfig(),
		Kubernetes:     k8sview.DefaultConfig(),
		Inventory:      inventory.DefaultConfig(),
		Agent:          ctragent.DefaultConfig(),
		GPU:            gpuobs.DefaultConfig(),
		Incidents:      incidents.DefaultConfig(),
	}
}

// NodeStatus represents the current status of a node
type NodeStatus struct {
	Name        string            `json:"name"`
	Address     string            `json:"address"`
	Labels      map[string]string `json:"labels,omitempty"`
	Healthy     bool              `json:"healthy"`
	LastScrape  time.Time         `json:"last_scrape"`
	LastError   string            `json:"last_error,omitempty"`
	MetricCount int               `json:"metric_count"`
}

// AgentMetric represents a metric scraped from an agent
type AgentMetric struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// HistorySample represents a snapshot of metrics at a point in time
type HistorySample struct {
	Timestamp time.Time          `json:"timestamp"`
	Metrics   map[string]float64 `json:"metrics"`
}

// NodeMetrics holds all metrics for a node
type NodeMetrics struct {
	NodeName    string        `json:"node_name"`
	Address     string        `json:"address"`
	Metrics     []AgentMetric `json:"metrics"`
	CollectedAt time.Time     `json:"collected_at"`
}

// Controller is the main controller server
type Controller struct {
	config Config
	logger *zap.Logger
	client *http.Client

	apiKey               string
	analysisExt          *AnalysisExtension
	agentEngine          *ctragent.Engine
	agentService         *agentcore.QueryService
	checks               *CheckManager
	orchestrationManager *orchestration.Manager
	k8sManager           *k8sview.Manager
	inventoryManager     *inventory.Manager
	ingestStore          *ingest.MemoryStore
	ingestServer         *ingest.Server
	logIndex             *logindex.Index
	gpuStore             *gpuobs.Store
	incidentOrchestrator *incidents.Orchestrator
	incidentCoordinator  *incidents.Coordinator
	grpcServer           *grpc.Server
	grpcListener         net.Listener
	httpListener         net.Listener

	actualHTTPAddr string
	actualGRPCAddr string

	// Node state
	mu          sync.RWMutex
	nodeStatus  map[string]*NodeStatus
	nodeMetrics map[string]*NodeMetrics
	nodeHistory map[string]*ring.Ring[HistorySample] // History per node (bounded)

	// Lifecycle
	server  *http.Server
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
}

// New creates a new controller
func New(cfg Config, logger *zap.Logger) (*Controller, error) {
	if logger == nil {
		logger, _ = zap.NewProduction()
	}

	c := &Controller{
		config: cfg,
		logger: logger.With(zap.String("component", "controller")),
		client: &http.Client{
			Timeout: cfg.ScrapeTimeout,
		},
		nodeStatus:  make(map[string]*NodeStatus),
		nodeMetrics: make(map[string]*NodeMetrics),
		nodeHistory: make(map[string]*ring.Ring[HistorySample]),
	}

	c.apiKey = strings.TrimSpace(ResolveAPIKey(cfg.Auth, c.logger))
	if cfg.Auth.Enabled && c.apiKey == "" {
		envVar := strings.TrimSpace(cfg.Auth.APIKeyEnv)
		if envVar == "" {
			envVar = defaultAPIKeyEnv
		}
		return nil, fmt.Errorf("controller auth is enabled but API key env var %q is empty", envVar)
	}
	c.initIngest()
	staticProbes := make([]inventory.StaticProbe, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		id := strings.TrimSpace(node.Name)
		if id == "" {
			id = strings.TrimSpace(node.Address)
		}
		if id == "" {
			continue
		}
		staticProbes = append(staticProbes, inventory.StaticProbe{
			ID:      id,
			Name:    strings.TrimSpace(node.Name),
			Address: strings.TrimSpace(node.Address),
			Labels:  node.Labels,
		})
	}
	c.inventoryManager = inventory.NewManager(cfg.Inventory, staticProbes, c.ingestStore, c.logger)
	if cfg.Orchestration.Enabled {
		c.orchestrationManager = orchestration.NewManager(cfg.Orchestration, c.ingestStore, c.logger)
	}
	if cfg.Kubernetes.Enabled {
		c.k8sManager = k8sview.NewManager(cfg.Kubernetes, c.ingestStore, c.logger)
	}

	if cfg.Analysis.Enabled {
		analysisExt, err := NewAnalysisExtension(cfg.Analysis, c.logger)
		if err != nil {
			return nil, err
		}
		c.analysisExt = analysisExt
		if c.ingestStore != nil {
			c.analysisExt.SetIngestStore(c.ingestStore)
		}
	}

	if cfg.Agent.Enabled {
		c.agentEngine = ctragent.New(cfg.Agent, c.ingestStore, nil, c.logger)
		if c.analysisExt != nil {
			c.agentEngine = ctragent.New(cfg.Agent, c.ingestStore, c.analysisExt.engine, c.logger)
		}

		queryCfg := agentcore.DefaultConfig()
		queryCfg.PlaybookFile = cfg.Agent.PolicyFile
		queryCfg.Timeout = cfg.Agent.LLMTimeout
		queryCfg.Model = nonEmptyString(os.Getenv("SRE_AGENT_LLM_MODEL"), cfg.Analysis.LLMModel, queryCfg.Model)
		queryCfg.Provider = nonEmptyString(os.Getenv("SRE_AGENT_LLM_PROVIDER"), cfg.Analysis.LLMProvider, queryCfg.Provider)
		queryCfg.BaseURL = nonEmptyString(os.Getenv("SRE_AGENT_LLM_BASE_URL"), queryCfg.BaseURL)
		queryCfg.APIKey = nonEmptyString(os.Getenv("SRE_AGENT_LLM_API_KEY"), os.Getenv("OPENAI_API_KEY"), os.Getenv("LLM_API_KEY"))
		queryCfg.DryRun = !cfg.Agent.LLMEnabled || parseBoolEnv("SRE_AGENT_DRY_RUN", true)
		if cfg.Agent.LLMEnabled {
			queryCfg.DryRun = parseBoolEnv("SRE_AGENT_DRY_RUN", queryCfg.DryRun)
		}
		queryCfg.RequireApprovalToken = parseBoolEnv("SRE_AGENT_REQUIRE_APPROVAL_TOKEN", queryCfg.RequireApprovalToken)
		queryCfg.PendingActionTTL = parseDurationEnv("SRE_AGENT_ACTION_APPROVAL_TTL", queryCfg.PendingActionTTL)
		queryCfg.MaxPendingActions = parseIntEnv("SRE_AGENT_MAX_PENDING_ACTIONS", queryCfg.MaxPendingActions)
		queryCfg.MaxActionsPerQuery = parseIntEnv("SRE_AGENT_MAX_ACTIONS_PER_QUERY", queryCfg.MaxActionsPerQuery)
		queryCfg.MaxConcurrentQueries = parseIntEnv("SRE_AGENT_MAX_CONCURRENT_QUERIES", queryCfg.MaxConcurrentQueries)
		queryCfg.MaxTelemetryAge = parseDurationEnv("SRE_AGENT_MAX_TELEMETRY_AGE", queryCfg.MaxTelemetryAge)
		queryCfg.AllowActionsOnStaleData = parseBoolEnv("SRE_AGENT_ALLOW_ACTIONS_ON_STALE_DATA", queryCfg.AllowActionsOnStaleData)
		queryCfg.SkipLLMOnStaleTelemetry = parseBoolEnv("SRE_AGENT_SKIP_LLM_ON_STALE_TELEMETRY", queryCfg.SkipLLMOnStaleTelemetry)
		queryCfg.SkipLLMOnNoTelemetry = parseBoolEnv("SRE_AGENT_SKIP_LLM_ON_NO_TELEMETRY", queryCfg.SkipLLMOnNoTelemetry)
		queryCfg.EventWebhookURL = nonEmptyString(os.Getenv("SRE_AGENT_EVENT_WEBHOOK_URL"), queryCfg.EventWebhookURL)
		queryCfg.EventWebhookToken = nonEmptyString(os.Getenv("SRE_AGENT_EVENT_WEBHOOK_TOKEN"), queryCfg.EventWebhookToken)
		queryCfg.EventWebhookTimeout = parseDurationEnv("SRE_AGENT_EVENT_WEBHOOK_TIMEOUT", queryCfg.EventWebhookTimeout)
		queryCfg.EventPublishRetries = parseIntEnv("SRE_AGENT_EVENT_PUBLISH_RETRIES", queryCfg.EventPublishRetries)
		queryCfg.EventRetryBackoff = parseDurationEnv("SRE_AGENT_EVENT_RETRY_BACKOFF", queryCfg.EventRetryBackoff)
		queryCfg.EventSlackWebhookURL = nonEmptyString(os.Getenv("SRE_AGENT_EVENT_SLACK_WEBHOOK_URL"), queryCfg.EventSlackWebhookURL)
		queryCfg.EventPagerDutyRoutingKey = nonEmptyString(os.Getenv("SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY"), queryCfg.EventPagerDutyRoutingKey)
		queryCfg.EventPagerDutyEventsURL = nonEmptyString(os.Getenv("SRE_AGENT_EVENT_PAGERDUTY_EVENTS_URL"), queryCfg.EventPagerDutyEventsURL)
		queryCfg.ActionTimeout = parseDurationEnv("SRE_AGENT_ACTION_TIMEOUT", queryCfg.ActionTimeout)
		queryCfg.MaxParallelActionExec = parseIntEnv("SRE_AGENT_MAX_PARALLEL_ACTION_EXEC", queryCfg.MaxParallelActionExec)
		queryCfg.ExplainabilityEvidenceMax = parseIntEnv("SRE_AGENT_EXPLAINABILITY_EVIDENCE_MAX", queryCfg.ExplainabilityEvidenceMax)
		if !cfg.Agent.LLMEnabled && os.Getenv("SRE_AGENT_LLM_ENABLED") == "" {
			queryCfg.Provider = "mock"
		}

		queryService, err := agentcore.NewQueryService(queryCfg, c.ingestStore, c.gpuStore, c.logger)
		if err != nil {
			c.logger.Warn("failed to initialize agent query service", zap.Error(err))
		} else {
			c.agentService = queryService
		}
	}

	// Incident context orchestrator (resource/monitoring/logging/Kubernetes)
	orchestrator, err := incidents.NewOrchestrator(cfg.Incidents, c.ingestStore, c.logger)
	if err != nil {
		return nil, err
	}
	c.incidentOrchestrator = orchestrator
	if cfg.Incidents.Enabled {
		var analysisEngine *analysis.Engine
		if c.analysisExt != nil {
			analysisEngine = c.analysisExt.engine
		}
		var sink func(incidents.AggregatedContext)
		if c.agentEngine != nil {
			sink = func(ctx incidents.AggregatedContext) { c.agentEngine.IngestIncidentContext(ctx) }
		}
		c.incidentCoordinator = incidents.NewCoordinator(cfg.Incidents, orchestrator, analysisEngine, sink, c.logger)
	}

	if cfg.Checks.Enabled {
		c.checks = NewCheckManager(cfg.Checks, c.logger, c.onCheckResult)
	}

	// Initialize node status from config
	for _, node := range cfg.Nodes {
		c.nodeStatus[node.Name] = &NodeStatus{
			Name:    node.Name,
			Address: node.Address,
			Labels:  node.Labels,
			Healthy: false,
		}
		c.nodeHistory[node.Name] = ring.New[HistorySample](256)
	}

	return c, nil
}

// Start starts the controller
func (c *Controller) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("controller already running")
	}
	c.running = true
	c.mu.Unlock()

	c.ctx, c.cancel = context.WithCancel(ctx)

	if c.analysisExt != nil {
		if err := c.analysisExt.engine.Start(c.ctx); err != nil {
			return err
		}
	}
	if c.agentEngine != nil {
		if err := c.agentEngine.Start(c.ctx); err != nil {
			return err
		}
	}
	if c.incidentCoordinator != nil {
		c.incidentCoordinator.Start(c.ctx)
	}

	if c.gpuStore != nil {
		if err := c.gpuStore.Start(); err != nil {
			return err
		}
	}

	if err := c.startIngest(); err != nil {
		return err
	}
	if c.k8sManager != nil {
		if err := c.k8sManager.Start(c.ctx); err != nil {
			return err
		}
	}
	if c.orchestrationManager != nil {
		if err := c.orchestrationManager.Start(c.ctx); err != nil {
			return err
		}
	}

	if c.checks != nil {
		if err := c.checks.Start(c.ctx); err != nil {
			return err
		}
	}

	// Initial scrape
	c.scrapeAll()

	// Start background scraper
	go c.scrapeLoop()

	// Setup HTTP server
	mux := http.NewServeMux()
	c.registerHandlers(mux)

	var handler http.Handler = mux
	if c.apiKey != "" {
		handler = middleware.APIKeyAuth(c.apiKey, mux)
		c.logger.Info("api key authentication enabled")
	}

	c.server = &http.Server{
		Addr:         c.config.ListenAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	ln, resolvedHTTP, err := listenWithFallback(c.config.ListenAddr, c.logger, "http")
	if err != nil {
		return err
	}
	c.httpListener = ln
	c.actualHTTPAddr = resolvedHTTP

	c.logger.Info("starting controller",
		zap.String("listen", c.ListenAddr()),
		zap.Int("nodes", len(c.config.Nodes)),
		zap.Duration("scrape_interval", c.config.ScrapeInterval))

	go func() {
		if err := c.server.Serve(ln); err != http.ErrServerClosed {
			c.logger.Error("server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop stops the controller
func (c *Controller) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	c.logger.Info("stopping controller")

	if c.cancel != nil {
		c.cancel()
	}

	c.stopIngest()

	if c.gpuStore != nil {
		c.gpuStore.Stop()
	}

	if c.analysisExt != nil {
		if err := c.analysisExt.engine.Stop(); err != nil {
			c.logger.Warn("failed to stop analysis engine", zap.Error(err))
		}
	}
	if c.agentEngine != nil {
		if err := c.agentEngine.Stop(); err != nil {
			c.logger.Warn("failed to stop agent engine", zap.Error(err))
		}
	}
	if c.incidentCoordinator != nil {
		c.incidentCoordinator.Stop()
	}
	if c.k8sManager != nil {
		c.k8sManager.Stop()
	}
	if c.orchestrationManager != nil {
		c.orchestrationManager.Stop()
	}

	if c.checks != nil {
		if err := c.checks.Stop(); err != nil {
			c.logger.Warn("failed to stop checks manager", zap.Error(err))
		}
	}

	if c.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.server.Shutdown(ctx)
	}
	if c.httpListener != nil {
		_ = c.httpListener.Close()
	}

	c.running = false
	return nil
}

// ListenAddr returns the actual HTTP listen address (after any fallback/ephemeral binding).
func (c *Controller) ListenAddr() string {
	if c.httpListener != nil {
		return c.httpListener.Addr().String()
	}
	return c.config.ListenAddr
}

// GRPCAddr returns the actual gRPC listen address (after any fallback/ephemeral binding).
func (c *Controller) GRPCAddr() string {
	if c.grpcListener != nil {
		return c.grpcListener.Addr().String()
	}
	return c.config.GRPCListenAddr
}

// AddNode dynamically adds a node
func (c *Controller) AddNode(node NodeConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.nodeStatus[node.Name]; exists {
		return fmt.Errorf("node %s already exists", node.Name)
	}

	c.nodeStatus[node.Name] = &NodeStatus{
		Name:    node.Name,
		Address: node.Address,
		Labels:  node.Labels,
		Healthy: false,
	}
	c.nodeHistory[node.Name] = ring.New[HistorySample](256)

	c.logger.Info("added node", zap.String("name", node.Name), zap.String("address", node.Address))
	return nil
}

// RemoveNode dynamically removes a node
func (c *Controller) RemoveNode(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.nodeStatus[name]; !exists {
		return fmt.Errorf("node %s not found", name)
	}

	delete(c.nodeStatus, name)
	delete(c.nodeMetrics, name)
	delete(c.nodeHistory, name)

	c.logger.Info("removed node", zap.String("name", name))
	return nil
}

// registerHandlers sets up HTTP routes
func (c *Controller) registerHandlers(mux *http.ServeMux) {
	// API endpoints with CORS
	mux.HandleFunc("/api/v1/nodes", c.withCORS(c.handleNodes))
	mux.HandleFunc("/api/v1/nodes/", c.withCORS(c.handleNodeByID))
	mux.HandleFunc("/api/v1/metrics", c.withCORS(c.handleAllMetrics))
	mux.HandleFunc("/api/v1/metrics/", c.withCORS(c.handleNodeMetrics))
	mux.HandleFunc("/api/v1/metrics/history", c.withCORS(c.handleHistory)) // New history endpoint
	mux.HandleFunc("/api/v1/top/programs", c.withCORS(c.handleTopPrograms))
	mux.HandleFunc("/api/v1/diagnostics/data-path", c.withCORS(c.handleDataPathDiagnostics))
	mux.HandleFunc("/api/v1/diagnostics/root-cause", c.withCORS(c.handleRootCauseDiagnostics))
	mux.HandleFunc("/api/v1/diagnostics/kernel-path", c.withCORS(c.handleKernelPathDiagnostics))
	mux.HandleFunc("/api/v1/diagnostics/workload-path", c.withCORS(c.handleWorkloadPathDiagnostics))
	mux.HandleFunc("/api/v1/diagnostics/rca-packet", c.withCORS(c.handleRCAPacketDiagnostics))
	mux.HandleFunc("/api/v1/diagnostics/ai-infra-stack", c.withCORS(c.handleAIInfraStackDiagnostics))
	mux.HandleFunc("/api/v1/topology", c.withCORS(c.handleTopology))
	mux.HandleFunc("/api/v1/status", c.withCORS(c.handleStatus))
	c.registerIngestHandlers(mux)
	if c.inventoryManager != nil {
		c.registerInventoryHandlers(mux)
	}
	if c.gpuStore != nil {
		c.registerGPUHandlers(mux)
	}
	if c.k8sManager != nil {
		c.registerK8sHandlers(mux)
	}

	if c.analysisExt != nil {
		c.RegisterAnalysisHandlers(mux, c.analysisExt)
	}
	if c.agentEngine != nil || c.agentService != nil {
		c.registerAgentHandlers(mux)
	}
	if c.checks != nil {
		c.RegisterCheckHandlers(mux)
	}
	if c.orchestrationManager != nil {
		c.registerOrchestrationHandlers(mux)
	}
	if c.incidentCoordinator != nil {
		mux.HandleFunc("/api/v1/incidents/alerts", c.withCORS(c.handleIncidentAlert))
	}

	// Health check
	mux.HandleFunc("/health", c.handleHealth)
	mux.HandleFunc("/healthz", c.handleHealth)

	// Prometheus metrics (aggregated from all nodes)
	mux.HandleFunc("/metrics", c.handlePrometheusMetrics)

	// Web UI static files
	c.setupWebUI(mux)
}

func (c *Controller) withCORS(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		handler(w, r)
	}
}

// handleNodes handles GET/POST /api/v1/nodes
func (c *Controller) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c.mu.RLock()
		nodes := make([]*NodeStatus, 0, len(c.nodeStatus))
		for _, status := range c.nodeStatus {
			nodes = append(nodes, status)
		}
		c.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": nodes,
			"count": len(nodes),
		})

	case http.MethodPost:
		var node NodeConfig
		if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}

		if node.Name == "" || node.Address == "" {
			http.Error(w, `{"error":"name and address are required"}`, http.StatusBadRequest)
			return
		}

		if err := c.AddNode(node); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusConflict)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "created", "name": node.Name})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleNodeByID handles GET/DELETE /api/v1/nodes/{id}
func (c *Controller) handleNodeByID(w http.ResponseWriter, r *http.Request) {
	// Extract node ID from path
	path := r.URL.Path
	nodeID := path[len("/api/v1/nodes/"):]
	if nodeID == "" {
		http.Error(w, `{"error":"node ID required"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		c.mu.RLock()
		status, exists := c.nodeStatus[nodeID]
		metrics := c.nodeMetrics[nodeID]
		c.mu.RUnlock()

		if !exists {
			http.Error(w, `{"error":"node not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  status,
			"metrics": metrics,
		})

	case http.MethodDelete:
		if err := c.RemoveNode(nodeID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": nodeID})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAllMetrics returns aggregated metrics from all nodes
func (c *Controller) handleAllMetrics(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	allMetrics := make(map[string]*NodeMetrics)
	for name, metrics := range c.nodeMetrics {
		allMetrics[name] = metrics
	}
	c.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"nodes":      allMetrics,
		"count":      len(allMetrics),
		"scraped_at": time.Now(),
	})
}

// handleNodeMetrics returns metrics for a specific node
func (c *Controller) handleNodeMetrics(w http.ResponseWriter, r *http.Request) {
	// Extract node ID from path
	path := r.URL.Path
	nodeID := path[len("/api/v1/metrics/"):]
	if nodeID == "" {
		http.Error(w, `{"error":"node ID required"}`, http.StatusBadRequest)
		return
	}

	c.mu.RLock()
	metrics, exists := c.nodeMetrics[nodeID]
	c.mu.RUnlock()

	if !exists {
		http.Error(w, `{"error":"node not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// handleHistory returns historical metrics
func (c *Controller) handleHistory(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	// Compatibility: the UI historically expected a flat array of samples.
	// We store a bounded history per node and allow selecting the node via query params.
	node := r.URL.Query().Get("node")
	var h *ring.Ring[HistorySample]
	if node != "" {
		h = c.nodeHistory[node]
	} else {
		for _, v := range c.nodeHistory {
			h = v
			break
		}
	}
	c.mu.RUnlock()

	var out []HistorySample
	if h == nil {
		out = []HistorySample{}
	} else {
		limit := 0
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
			limit = n
		}
		if limit > 0 {
			out = h.SliceLastN(limit)
		} else {
			out = h.SliceOldest()
		}
		if out == nil {
			out = []HistorySample{}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleIncidentAlert ingests an external alert and builds correlated context.
func (c *Controller) handleIncidentAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.incidentCoordinator == nil {
		http.Error(w, "incident orchestration disabled", http.StatusServiceUnavailable)
		return
	}

	var payload struct {
		ID          string            `json:"id"`
		Title       string            `json:"title"`
		Service     string            `json:"service"`
		Severity    string            `json:"severity"`
		StartsAt    time.Time         `json:"starts_at"`
		EndsAt      time.Time         `json:"ends_at"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	ctxBundle, err := c.incidentCoordinator.HandleExternalAlert(r.Context(), incidents.InputAlert{
		ID:          payload.ID,
		Title:       payload.Title,
		Service:     payload.Service,
		Severity:    payload.Severity,
		StartsAt:    payload.StartsAt,
		EndsAt:      payload.EndsAt,
		Labels:      payload.Labels,
		Annotations: payload.Annotations,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"context":   ctxBundle,
		"timestamp": time.Now(),
	})
}

// handleStatus returns controller status
func (c *Controller) handleStatus(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	healthyCount := 0
	for _, status := range c.nodeStatus {
		if status.Healthy {
			healthyCount++
		}
	}
	totalNodes := len(c.nodeStatus)
	c.mu.RUnlock()
	version := strings.TrimSpace(c.config.Version)
	if version == "" {
		version = "v0.4"
	}

	resp := map[string]interface{}{
		"version":         version,
		"uptime":          "running",
		"total_nodes":     totalNodes,
		"healthy_nodes":   healthyCount,
		"scrape_interval": c.config.ScrapeInterval.String(),
		"listen_address":  c.config.ListenAddr,
	}
	if c.logIndex != nil {
		logStats := c.logIndex.Stats()
		resp["logs"] = map[string]interface{}{
			"segments": logStats.Segments,
			"entries":  logStats.Entries,
			"queries":  logStats.QueriesTotal,
		}
	}
	if c.orchestrationManager != nil {
		resp["orchestration"] = c.orchestrationManager.Status()
	}
	if c.inventoryManager != nil {
		resp["inventory"] = c.inventoryManager.Summary()
	}
	if c.k8sManager != nil {
		resp["kubernetes"] = c.k8sManager.Status()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleHealth returns health status
func (c *Controller) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handlePrometheusMetrics returns aggregated metrics in Prometheus format
func (c *Controller) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// /metrics is a hot endpoint in real deployments. Buffering reduces syscall/write overhead.
	bw := bufio.NewWriterSize(w, 64*1024)
	defer func() { _ = bw.Flush() }()

	type nodeHealth struct {
		name    string
		address string
		healthy bool
	}
	type nodeMetricBatch struct {
		node    string
		metrics []AgentMetric
	}

	// Snapshot quickly under lock to avoid holding the lock while writing (scrapes can be frequent).
	c.mu.RLock()
	nodesTotal := len(c.nodeStatus)
	nodes := make([]nodeHealth, 0, len(c.nodeStatus))
	healthyCount := 0
	for _, status := range c.nodeStatus {
		if status == nil {
			continue
		}
		if status.Healthy {
			healthyCount++
		}
		nodes = append(nodes, nodeHealth{name: status.Name, address: status.Address, healthy: status.Healthy})
	}

	metricBatches := make([]nodeMetricBatch, 0, len(c.nodeMetrics))
	for nodeName, nm := range c.nodeMetrics {
		if nm == nil || len(nm.Metrics) == 0 {
			continue
		}
		// The scrape loop replaces the entire NodeMetrics pointer; it does not mutate the slice in place.
		// Copying the slice here would be wasted work on hot /metrics scrapes.
		metricBatches = append(metricBatches, nodeMetricBatch{node: nodeName, metrics: nm.Metrics})
	}
	gpuStore := c.gpuStore
	agentService := c.agentService
	ingestServer := c.ingestServer
	logIndex := c.logIndex
	inventoryManager := c.inventoryManager
	k8sManager := c.k8sManager
	c.mu.RUnlock()

	// Write controller meta metrics
	fmt.Fprintf(bw, "# HELP sre_controller_nodes_total Total number of configured nodes\n")
	fmt.Fprintf(bw, "# TYPE sre_controller_nodes_total gauge\n")
	fmt.Fprintf(bw, "sre_controller_nodes_total %d\n", nodesTotal)

	fmt.Fprintf(bw, "# HELP sre_controller_nodes_healthy Number of healthy nodes\n")
	fmt.Fprintf(bw, "# TYPE sre_controller_nodes_healthy gauge\n")
	fmt.Fprintf(bw, "sre_controller_nodes_healthy %d\n", healthyCount)

	// Write per-node health status
	fmt.Fprintf(bw, "# HELP sre_node_up Node health status (1=up, 0=down)\n")
	fmt.Fprintf(bw, "# TYPE sre_node_up gauge\n")
	for _, status := range nodes {
		healthy := 0
		if status.healthy {
			healthy = 1
		}
		fmt.Fprintf(bw, "sre_node_up{node=%q,address=%q} %d\n", status.name, status.address, healthy)
	}

	// Write aggregated metrics from all nodes (with node label)
	for _, batch := range metricBatches {
		for _, m := range batch.metrics {
			metricName := prom.SanitizeMetricName(m.Name)
			if metricName == "" {
				continue
			}

			var b strings.Builder
			b.Grow(64 + len(m.Labels)*24)
			b.WriteString("node=")
			b.WriteString(strconv.Quote(batch.node))

			for k, v := range m.Labels {
				k = prom.SanitizeLabelKey(k)
				if k == "" {
					continue
				}
				b.WriteByte(',')
				b.WriteString(k)
				b.WriteByte('=')
				b.WriteString(strconv.Quote(v))
			}

			fmt.Fprintf(bw, "%s{%s} %g\n", metricName, b.String(), m.Value)
		}
	}

	// Push-first GPU metrics (derived from ingested batches).
	if gpuStore != nil {
		fmt.Fprintf(bw, "# HELP node_gpu_utilization_sm_percent GPU SM utilization percent (latest)\n")
		fmt.Fprintf(bw, "# TYPE node_gpu_utilization_sm_percent gauge\n")
		fmt.Fprintf(bw, "# HELP node_gpu_memory_used_mib GPU memory used MiB (latest)\n")
		fmt.Fprintf(bw, "# TYPE node_gpu_memory_used_mib gauge\n")
		fmt.Fprintf(bw, "# HELP node_gpu_memory_total_mib GPU memory total MiB (latest)\n")
		fmt.Fprintf(bw, "# TYPE node_gpu_memory_total_mib gauge\n")

		gpuStore.ForEachDeviceLite(func(hostname, _ string, dev gpuobs.DeviceLite) {
			labels := fmt.Sprintf("node=%q,gpu_id=%q", hostname, dev.GPUIndex)
			if dev.UUID != "" {
				labels += fmt.Sprintf(",uuid=%q", dev.UUID)
			}
			if dev.Name != "" {
				labels += fmt.Sprintf(",name=%q", dev.Name)
			}
			fmt.Fprintf(bw, "node_gpu_utilization_sm_percent{%s} %g\n", labels, dev.UtilSMPct)
			fmt.Fprintf(bw, "node_gpu_memory_used_mib{%s} %g\n", labels, dev.MemUsedMiB)
			fmt.Fprintf(bw, "node_gpu_memory_total_mib{%s} %g\n", labels, dev.MemTotalMiB)
		})
	}

	if agentService != nil {
		stats := agentService.Metrics()
		fmt.Fprintf(bw, "# HELP agent_queries_total AGENT query requests received\n")
		fmt.Fprintf(bw, "# TYPE agent_queries_total counter\n")
		fmt.Fprintf(bw, "agent_queries_total %d\n", stats.QueriesTotal)

		fmt.Fprintf(bw, "# HELP agent_queries_success_total AGENT query requests completed successfully\n")
		fmt.Fprintf(bw, "# TYPE agent_queries_success_total counter\n")
		fmt.Fprintf(bw, "agent_queries_success_total %d\n", stats.QueriesSuccessTotal)

		fmt.Fprintf(bw, "# HELP agent_queries_failure_total AGENT query requests failed\n")
		fmt.Fprintf(bw, "# TYPE agent_queries_failure_total counter\n")
		fmt.Fprintf(bw, "agent_queries_failure_total %d\n", stats.QueriesFailureTotal)

		fmt.Fprintf(bw, "# HELP agent_queries_rate_limited_total AGENT query requests rejected by rate limiting\n")
		fmt.Fprintf(bw, "# TYPE agent_queries_rate_limited_total counter\n")
		fmt.Fprintf(bw, "agent_queries_rate_limited_total %d\n", stats.RateLimitedTotal)

		fmt.Fprintf(bw, "# HELP agent_queries_busy_rejected_total AGENT query requests rejected due to in-flight concurrency limit\n")
		fmt.Fprintf(bw, "# TYPE agent_queries_busy_rejected_total counter\n")
		fmt.Fprintf(bw, "agent_queries_busy_rejected_total %d\n", stats.BusyRejectedTotal)

		fmt.Fprintf(bw, "# HELP agent_queries_stale_telemetry_total AGENT queries where telemetry snapshot was stale\n")
		fmt.Fprintf(bw, "# TYPE agent_queries_stale_telemetry_total counter\n")
		fmt.Fprintf(bw, "agent_queries_stale_telemetry_total %d\n", stats.StaleTelemetryTotal)

		fmt.Fprintf(bw, "# HELP agent_llm_calls_total Total LLM calls from AGENT\n")
		fmt.Fprintf(bw, "# TYPE agent_llm_calls_total counter\n")
		fmt.Fprintf(bw, "agent_llm_calls_total %d\n", stats.LLMCallsTotal)

		fmt.Fprintf(bw, "# HELP agent_llm_failures_total Failed LLM calls from AGENT\n")
		fmt.Fprintf(bw, "# TYPE agent_llm_failures_total counter\n")
		fmt.Fprintf(bw, "agent_llm_failures_total %d\n", stats.LLMFailuresTotal)

		fmt.Fprintf(bw, "# HELP agent_llm_bypassed_stale_total AGENT queries that skipped LLM because telemetry was stale\n")
		fmt.Fprintf(bw, "# TYPE agent_llm_bypassed_stale_total counter\n")
		fmt.Fprintf(bw, "agent_llm_bypassed_stale_total %d\n", stats.LLMBypassedStaleTotal)

		fmt.Fprintf(bw, "# HELP agent_llm_bypassed_empty_total AGENT queries that skipped LLM because telemetry was insufficient\n")
		fmt.Fprintf(bw, "# TYPE agent_llm_bypassed_empty_total counter\n")
		fmt.Fprintf(bw, "agent_llm_bypassed_empty_total %d\n", stats.LLMBypassedEmptyTotal)

		fmt.Fprintf(bw, "# HELP agent_fallback_total AGENT queries completed via deterministic fallback\n")
		fmt.Fprintf(bw, "# TYPE agent_fallback_total counter\n")
		fmt.Fprintf(bw, "agent_fallback_total %d\n", stats.FallbackTotal)

		fmt.Fprintf(bw, "# HELP agent_actions_suppressed_total AGENT queries where actions were intentionally suppressed by policy\n")
		fmt.Fprintf(bw, "# TYPE agent_actions_suppressed_total counter\n")
		fmt.Fprintf(bw, "agent_actions_suppressed_total %d\n", stats.ActionsSuppressedTotal)

		fmt.Fprintf(bw, "# HELP agent_actions_executed_total AGENT actions executed or dry-run simulated\n")
		fmt.Fprintf(bw, "# TYPE agent_actions_executed_total counter\n")
		fmt.Fprintf(bw, "agent_actions_executed_total %d\n", stats.ActionsExecutedTotal)

		fmt.Fprintf(bw, "# HELP agent_actions_failure_total AGENT action execution failures\n")
		fmt.Fprintf(bw, "# TYPE agent_actions_failure_total counter\n")
		fmt.Fprintf(bw, "agent_actions_failure_total %d\n", stats.ActionsFailureTotal)

		fmt.Fprintf(bw, "# HELP agent_events_published_total AGENT webhook events successfully published\n")
		fmt.Fprintf(bw, "# TYPE agent_events_published_total counter\n")
		fmt.Fprintf(bw, "agent_events_published_total %d\n", stats.EventsPublishedTotal)

		fmt.Fprintf(bw, "# HELP agent_events_publish_fail_total AGENT webhook event publish attempts that failed\n")
		fmt.Fprintf(bw, "# TYPE agent_events_publish_fail_total counter\n")
		fmt.Fprintf(bw, "agent_events_publish_fail_total %d\n", stats.EventsPublishFailTotal)

		fmt.Fprintf(bw, "# HELP agent_action_approval_required_total AGENT actions requiring approval tokens\n")
		fmt.Fprintf(bw, "# TYPE agent_action_approval_required_total counter\n")
		fmt.Fprintf(bw, "agent_action_approval_required_total %d\n", stats.ApprovalRequiredTotal)

		fmt.Fprintf(bw, "# HELP agent_action_approval_rejected_total AGENT action executions rejected by approval policy\n")
		fmt.Fprintf(bw, "# TYPE agent_action_approval_rejected_total counter\n")
		fmt.Fprintf(bw, "agent_action_approval_rejected_total %d\n", stats.ApprovalRejectedTotal)

		fmt.Fprintf(bw, "# HELP agent_pending_actions_expired_total AGENT pending actions that expired before execution\n")
		fmt.Fprintf(bw, "# TYPE agent_pending_actions_expired_total counter\n")
		fmt.Fprintf(bw, "agent_pending_actions_expired_total %d\n", stats.PendingExpiredTotal)

		fmt.Fprintf(bw, "# HELP agent_pending_actions_pruned_total AGENT pending actions pruned by capacity control\n")
		fmt.Fprintf(bw, "# TYPE agent_pending_actions_pruned_total counter\n")
		fmt.Fprintf(bw, "agent_pending_actions_pruned_total %d\n", stats.PendingPrunedTotal)

		fmt.Fprintf(bw, "# HELP gpu_analysis_duration_seconds_total Total seconds spent enriching AGENT queries with GPU context\n")
		fmt.Fprintf(bw, "# TYPE gpu_analysis_duration_seconds_total counter\n")
		fmt.Fprintf(bw, "gpu_analysis_duration_seconds_total %g\n", stats.GPUAnalysisSumSec)
	}

	if c.orchestrationManager != nil {
		stats := c.orchestrationManager.Metrics()

		fmt.Fprintf(bw, "# HELP sre_orchestrator_reconciles_total Number of orchestration reconcile cycles\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_reconciles_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_reconciles_total %d\n", stats.ReconcilesTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_scheduling_attempts_total Number of workload scheduling attempts\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_scheduling_attempts_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_scheduling_attempts_total %d\n", stats.SchedulingAttemptsTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_scheduling_failures_total Number of failed workload scheduling attempts\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_scheduling_failures_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_scheduling_failures_total %d\n", stats.SchedulingFailuresTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_batch_deferrals_total Number of batch workloads deferred for peak shifting\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_batch_deferrals_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_batch_deferrals_total %d\n", stats.BatchDeferralsTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_self_heal_actions_total Number of self-healing requeue actions\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_self_heal_actions_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_self_heal_actions_total %d\n", stats.SelfHealActionsTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_route_updates_total Number of routing-plan updates\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_route_updates_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_route_updates_total %d\n", stats.RouteUpdatesTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_slo_violations_total Number of running workload SLO violations detected during reconcile\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_slo_violations_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_slo_violations_total %d\n", stats.SLOViolationsTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_slo_violations_active Number of workloads currently in SLO-violating state\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_slo_violations_active gauge\n")
		fmt.Fprintf(bw, "sre_orchestrator_slo_violations_active %d\n", stats.SLOViolationsActive)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_remediation_attempts_total Number of auto-remediation evaluations triggered by SLO violations\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_remediation_attempts_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_remediation_attempts_total %d\n", stats.RemediationAttemptsTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_remediation_actions_total Number of auto-remediation actions executed\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_remediation_actions_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_remediation_actions_total %d\n", stats.RemediationActionsTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_remediation_blocked_total Number of auto-remediation attempts blocked by policy gates\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_remediation_blocked_total counter\n")
		fmt.Fprintf(bw, "sre_orchestrator_remediation_blocked_total %d\n", stats.RemediationBlockedTotal)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_queue_depth Number of queued/deferred workloads\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_queue_depth gauge\n")
		fmt.Fprintf(bw, "sre_orchestrator_queue_depth %d\n", stats.QueueDepth)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_running_workloads Number of running workloads\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_running_workloads gauge\n")
		fmt.Fprintf(bw, "sre_orchestrator_running_workloads %d\n", stats.RunningWorkloads)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_failed_workloads Number of failed workloads\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_failed_workloads gauge\n")
		fmt.Fprintf(bw, "sre_orchestrator_failed_workloads %d\n", stats.FailedWorkloads)

		fmt.Fprintf(bw, "# HELP sre_orchestrator_assignments_total Number of active assignments\n")
		fmt.Fprintf(bw, "# TYPE sre_orchestrator_assignments_total gauge\n")
		fmt.Fprintf(bw, "sre_orchestrator_assignments_total %d\n", stats.AssignmentsTotal)
	}

	if ingestServer != nil {
		stats := ingestServer.Stats()
		fmt.Fprintf(bw, "# HELP sre_ingest_batches_total Total telemetry batches accepted by ingest\n")
		fmt.Fprintf(bw, "# TYPE sre_ingest_batches_total counter\n")
		fmt.Fprintf(bw, "sre_ingest_batches_total %d\n", stats.BatchesTotal)

		fmt.Fprintf(bw, "# HELP sre_ingest_rejected_total Total telemetry batches rejected by validation\n")
		fmt.Fprintf(bw, "# TYPE sre_ingest_rejected_total counter\n")
		fmt.Fprintf(bw, "sre_ingest_rejected_total %d\n", stats.RejectedTotal)

		fmt.Fprintf(bw, "# HELP sre_ingest_metrics_points_total Total metric points ingested\n")
		fmt.Fprintf(bw, "# TYPE sre_ingest_metrics_points_total counter\n")
		fmt.Fprintf(bw, "sre_ingest_metrics_points_total %d\n", stats.MetricsTotal)

		fmt.Fprintf(bw, "# HELP sre_ingest_process_samples_total Total process samples ingested\n")
		fmt.Fprintf(bw, "# TYPE sre_ingest_process_samples_total counter\n")
		fmt.Fprintf(bw, "sre_ingest_process_samples_total %d\n", stats.ProcessesTotal)

		fmt.Fprintf(bw, "# HELP sre_ingest_log_fingerprints_total Total log fingerprints ingested\n")
		fmt.Fprintf(bw, "# TYPE sre_ingest_log_fingerprints_total counter\n")
		fmt.Fprintf(bw, "sre_ingest_log_fingerprints_total %d\n", stats.LogsTotal)
	}

	if logIndex != nil {
		stats := logIndex.Stats()
		fmt.Fprintf(bw, "# HELP sre_log_index_segments Number of active log index segments\n")
		fmt.Fprintf(bw, "# TYPE sre_log_index_segments gauge\n")
		fmt.Fprintf(bw, "sre_log_index_segments %d\n", stats.Segments)

		fmt.Fprintf(bw, "# HELP sre_log_index_entries Number of active log entries retained for search\n")
		fmt.Fprintf(bw, "# TYPE sre_log_index_entries gauge\n")
		fmt.Fprintf(bw, "sre_log_index_entries %d\n", stats.Entries)

		fmt.Fprintf(bw, "# HELP sre_log_index_ingested_events_total Total log events accepted by the native log index\n")
		fmt.Fprintf(bw, "# TYPE sre_log_index_ingested_events_total counter\n")
		fmt.Fprintf(bw, "sre_log_index_ingested_events_total %d\n", stats.IngestedEvents)

		fmt.Fprintf(bw, "# HELP sre_log_index_ingested_lines_total Total log lines represented by accepted log events\n")
		fmt.Fprintf(bw, "# TYPE sre_log_index_ingested_lines_total counter\n")
		fmt.Fprintf(bw, "sre_log_index_ingested_lines_total %d\n", stats.IngestedLines)

		fmt.Fprintf(bw, "# HELP sre_log_index_dropped_events_total Total log events dropped by validation/retention constraints\n")
		fmt.Fprintf(bw, "# TYPE sre_log_index_dropped_events_total counter\n")
		fmt.Fprintf(bw, "sre_log_index_dropped_events_total %d\n", stats.DroppedEvents)

		fmt.Fprintf(bw, "# HELP sre_log_index_queries_total Total log search queries served\n")
		fmt.Fprintf(bw, "# TYPE sre_log_index_queries_total counter\n")
		fmt.Fprintf(bw, "sre_log_index_queries_total %d\n", stats.QueriesTotal)
	}

	if inventoryManager != nil {
		summary := inventoryManager.Summary()
		fmt.Fprintf(bw, "# HELP sre_inventory_probes_total Number of probes in merged inventory\n")
		fmt.Fprintf(bw, "# TYPE sre_inventory_probes_total gauge\n")
		fmt.Fprintf(bw, "sre_inventory_probes_total %d\n", summary.Total)

		fmt.Fprintf(bw, "# HELP sre_inventory_probes_healthy Number of healthy probes in merged inventory\n")
		fmt.Fprintf(bw, "# TYPE sre_inventory_probes_healthy gauge\n")
		fmt.Fprintf(bw, "sre_inventory_probes_healthy %d\n", summary.Healthy)
	}

	if k8sManager != nil {
		stats := k8sManager.Metrics()
		fmt.Fprintf(bw, "# HELP sre_k8s_refresh_total Number of Kubernetes snapshot refresh cycles\n")
		fmt.Fprintf(bw, "# TYPE sre_k8s_refresh_total counter\n")
		fmt.Fprintf(bw, "sre_k8s_refresh_total %d\n", stats.RefreshTotal)

		fmt.Fprintf(bw, "# HELP sre_k8s_refresh_failed_total Number of failed Kubernetes snapshot refresh cycles\n")
		fmt.Fprintf(bw, "# TYPE sre_k8s_refresh_failed_total counter\n")
		fmt.Fprintf(bw, "sre_k8s_refresh_failed_total %d\n", stats.RefreshFailedTotal)

		fmt.Fprintf(bw, "# HELP sre_k8s_clusters_configured Number of configured Kubernetes clusters\n")
		fmt.Fprintf(bw, "# TYPE sre_k8s_clusters_configured gauge\n")
		fmt.Fprintf(bw, "sre_k8s_clusters_configured %d\n", stats.ClustersConfigured)

		fmt.Fprintf(bw, "# HELP sre_k8s_clusters_healthy Number of healthy Kubernetes clusters\n")
		fmt.Fprintf(bw, "# TYPE sre_k8s_clusters_healthy gauge\n")
		fmt.Fprintf(bw, "sre_k8s_clusters_healthy %d\n", stats.ClustersHealthy)

		fmt.Fprintf(bw, "# HELP sre_k8s_nodes_total Number of discovered Kubernetes nodes\n")
		fmt.Fprintf(bw, "# TYPE sre_k8s_nodes_total gauge\n")
		fmt.Fprintf(bw, "sre_k8s_nodes_total %d\n", stats.NodesTotal)

		fmt.Fprintf(bw, "# HELP sre_k8s_workloads_total Number of discovered Kubernetes workloads\n")
		fmt.Fprintf(bw, "# TYPE sre_k8s_workloads_total gauge\n")
		fmt.Fprintf(bw, "sre_k8s_workloads_total %d\n", stats.WorkloadsTotal)
	}
}

// setupWebUI sets up static file serving for the web UI
func (c *Controller) setupWebUI(mux *http.ServeMux) {
	indexPath := c.config.WebPath + "/index.html"
	assetFS := http.FileServer(http.Dir(c.config.WebPath))
	spaReady := fileExists(indexPath) && spaAssetsAvailable(indexPath, c.config.WebPath)

	// Serve static assets under /assets/
	mux.Handle("/assets/", assetFS)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Always allow a minimal inline UI for quick troubleshooting.
		if r.URL.Query().Get("simple") == "1" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, simpleInlinePage())
			return
		}

		// Serve SPA for / and /ui when assets exist.
		if (r.URL.Path == "/" || r.URL.Path == "" || r.URL.Path == "/ui") && spaReady {
			http.ServeFile(w, r, indexPath)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/metrics" || strings.HasPrefix(r.URL.Path, "/health") {
			return
		}

		// Serve SPA entrypoint; fallback minimal HTML if missing.
		if spaReady {
			http.ServeFile(w, r, indexPath)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body>
<h3>AI SRE Agent</h3>
<p>Web UI assets missing or out-of-sync. Run: <code>npm -C frontend run build</code></p>
<p>API links:</p>
<ul>
  <li><a href="/api/v1/status">/api/v1/status</a></li>
  <li><a href="/api/v1/fleet">/api/v1/fleet</a></li>
  <li><a href="/api/v1/gpu/nodes">/api/v1/gpu/nodes</a></li>
  <li><a href="/api/v1/gpu/timeline?gpu_id=0&metric=node_gpu_utilization_sm_percent">/api/v1/gpu/timeline</a></li>
  <li><a href="/api/v1/gpu/events">/api/v1/gpu/events</a></li>
  <li><a href="/metrics">/metrics</a></li>
</ul>
</body></html>`)
	})
}

func simpleInlinePage() string {
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>AI SRE Agent (simple)</title>
<style>
body { font-family: monospace; margin: 16px; }
pre { background:#f6f8fa; padding:12px; white-space:pre-wrap; word-break:break-word; }
button { margin-right: 8px; margin-top: 8px; }
</style></head>
<body>
<h3>AI SRE Agent</h3>
<div>
  <p><a href="/ui">Full UI</a> (if assets are present)</p>
  <button onclick="loadStatus()">Status</button>
  <button onclick="loadFleet()">Fleet</button>
  <button onclick="loadGPU()">GPU</button>
  <button onclick="loadMetrics()">Metrics (text)</button>
</div>
<pre id="out">Click a button to load data.</pre>
<script>
async function fetchJSON(url){ const r = await fetch(url); if(!r.ok) throw new Error(r.status+' '+r.statusText); return r.json(); }
async function loadStatus(){ show(await fetchJSON('/api/v1/status')); }
async function loadFleet(){ show(await fetchJSON('/api/v1/fleet')); }
async function loadGPU(){ show(await fetchJSON('/api/v1/gpu/nodes')); }
async function loadMetrics(){ const r = await fetch('/metrics'); show(await r.text()); }
function show(data){ document.getElementById('out').textContent = typeof data === 'string' ? data : JSON.stringify(data,null,2); }
</script>
</body></html>`
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func spaAssetsAvailable(indexPath, webPath string) bool {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return false
	}
	matches := spaAssetRefRe.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		return false
	}
	for _, match := range matches {
		assetPath := strings.TrimPrefix(match[1], "/")
		if !fileExists(filepath.Join(webPath, assetPath)) {
			return false
		}
	}
	return true
}

func nonEmptyString(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseBoolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func parseIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

// scrapeLoop runs periodic scraping of all nodes
func (c *Controller) scrapeLoop() {
	ticker := time.NewTicker(c.config.ScrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.scrapeAll()
		}
	}
}

// scrapeAll scrapes metrics from all configured nodes
func (c *Controller) scrapeAll() {
	c.mu.RLock()
	nodes := make([]NodeConfig, 0, len(c.nodeStatus))
	for _, status := range c.nodeStatus {
		nodes = append(nodes, NodeConfig{
			Name:    status.Name,
			Address: status.Address,
			Labels:  status.Labels,
		})
	}
	c.mu.RUnlock()

	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(n NodeConfig) {
			defer wg.Done()
			c.scrapeNode(n)
		}(node)
	}
	wg.Wait()
}

// scrapeNode scrapes metrics from a single node
func (c *Controller) scrapeNode(node NodeConfig) {
	url := fmt.Sprintf("http://%s/api/v1/metrics", node.Address)

	req, err := http.NewRequestWithContext(c.ctx, "GET", url, nil)
	if err != nil {
		c.updateNodeStatus(node.Name, false, err.Error(), 0)
		return
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.updateNodeStatus(node.Name, false, err.Error(), 0)
		c.logger.Warn("scrape failed", zap.String("node", node.Name), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.updateNodeStatus(node.Name, false, fmt.Sprintf("HTTP %d", resp.StatusCode), 0)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.updateNodeStatus(node.Name, false, err.Error(), 0)
		return
	}

	// Parse the response
	var batch struct {
		Metrics     []AgentMetric `json:"metrics"`
		CollectedAt time.Time     `json:"collected_at"`
		Hostname    string        `json:"hostname"`
	}

	if err := json.Unmarshal(body, &batch); err != nil {
		c.updateNodeStatus(node.Name, false, err.Error(), 0)
		return
	}

	// Process for history (convert simple key-value for chart efficiency)
	// We only store metrics without labels or carefully select them to avoid explosion
	metricMap := make(map[string]float64, len(batch.Metrics))
	for _, m := range batch.Metrics {
		// For history/charts, we mostly care about base system metrics without high cardinality labels
		// Or we flat-map them if needed. Frontend uses 'system.cpu.usage', etc.
		if len(m.Labels) == 0 {
			metricMap[m.Name] = m.Value
		} else {
			// For some key metrics with device labels, we might want them.
			// But simpler to just store what fits in map for now.
			// If implicit name mapping is used in frontend, we honor that.
			// e.g. system.cpu.usage is scalar.
		}
	}

	// Update node metrics
	c.mu.Lock()
	c.nodeMetrics[node.Name] = &NodeMetrics{
		NodeName:    node.Name,
		Address:     node.Address,
		Metrics:     batch.Metrics,
		CollectedAt: time.Now(),
	}

	// Update history
	h := c.nodeHistory[node.Name]
	if h == nil {
		h = ring.New[HistorySample](256)
		c.nodeHistory[node.Name] = h
	}
	h.Push(HistorySample{
		Timestamp: time.Now(),
		Metrics:   metricMap,
	})
	c.mu.Unlock()

	if c.analysisExt != nil {
		c.FeedMetricsToAnalysis(c.analysisExt, node.Name, batch.Metrics)
	}

	c.updateNodeStatus(node.Name, true, "", len(batch.Metrics))
	c.logger.Debug("scrape completed", zap.String("node", node.Name), zap.Int("metrics", len(batch.Metrics)))
}

// updateNodeStatus updates the status of a node
func (c *Controller) updateNodeStatus(name string, healthy bool, lastError string, metricCount int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if status, exists := c.nodeStatus[name]; exists {
		status.Healthy = healthy
		status.LastScrape = time.Now()
		status.LastError = lastError
		status.MetricCount = metricCount
	}
}
