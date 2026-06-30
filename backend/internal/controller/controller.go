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
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/timeseries"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/collections/ring"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/metrics/prom"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/release"
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
	ListenAddr     string           `yaml:"listen"`
	GRPCListenAddr string           `yaml:"grpc_listen"` // For Push model
	ScrapeInterval time.Duration    `yaml:"scrape_interval"`
	ScrapeTimeout  time.Duration    `yaml:"scrape_timeout"`
	Nodes          []NodeConfig     `yaml:"nodes"`
	WebPath        string           `yaml:"web_path"`
	LogLevel       string           `yaml:"log_level"`
	Version        string           `yaml:"version"`
	Deployment     DeploymentConfig `yaml:"deployment"`
	API            APIConfig        `yaml:"api"`

	Auth          AuthConfig           `yaml:"auth"`
	Analysis      AnalysisConfig       `yaml:"analysis"`
	Checks        ChecksConfig         `yaml:"checks"`
	Ingest        IngestConfig         `yaml:"ingest"`
	TSDB          TSDBConfig           `yaml:"tsdb"`
	Orchestration orchestration.Config `yaml:"orchestration"`
	Kubernetes    k8sview.Config       `yaml:"kubernetes"`
	Inventory     inventory.Config     `yaml:"inventory"`
	Agent         ctragent.Config      `yaml:"agent"`
	GPU           gpuobs.Config        `yaml:"gpu"`
	Incidents     incidents.Config     `yaml:"incidents"`
	HA            HAConfig             `yaml:"ha"`
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
		Version:        release.EffectiveVersion(),
		Deployment:     DefaultDeploymentConfig(),
		API:            DefaultAPIConfig(),
		Auth:           DefaultAuthConfig(),
		Analysis:       DefaultAnalysisConfig(),
		Checks:         DefaultChecksConfig(),
		Ingest:         DefaultIngestConfig(),
		TSDB:           DefaultTSDBConfig(),
		Orchestration:  orchestration.DefaultConfig(),
		Kubernetes:     k8sview.DefaultConfig(),
		Inventory:      inventory.DefaultConfig(),
		Agent:          ctragent.DefaultConfig(),
		GPU:            gpuobs.DefaultConfig(),
		Incidents:      incidents.DefaultConfig(),
		HA:             DefaultHAConfig(),
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

// Controller keeps ingest, hot state, workflow runtime, HTTP API, and optional
// integrations in one process. Those subsystems share fate with this binary.
// HA adds leader election and routing. It does not split hot-state ownership or
// durable workflow ownership into separate services.
type Controller struct {
	config Config
	logger *zap.Logger
	client *http.Client

	auth                 ResolvedAuthConfig
	analysisExt          *AnalysisExtension
	agentEngine          *ctragent.Engine
	agentService         *agentcore.QueryService
	agentWorkflow        *agentcore.WorkflowEngine
	ragService           rag.KnowledgeBase
	checks               *CheckManager
	orchestrationManager *orchestration.Manager
	k8sManager           *k8sview.Manager
	inventoryManager     *inventory.Manager
	ingestStore          *ingest.MemoryStore
	metricHistory        ingest.MetricHistoryProvider
	ingestServer         *ingest.Server
	timeseriesService    *timeseries.Service
	logIndex             *logindex.Index
	gpuStore             *gpuobs.Store
	incidentOrchestrator *incidents.Orchestrator
	incidentCoordinator  *incidents.Coordinator
	grpcServer           *grpc.Server
	grpcListener         net.Listener
	httpListener         net.Listener
	haCoordinator        haCoordinator

	actualHTTPAddr string
	actualGRPCAddr string

	// Node state
	mu          sync.RWMutex
	nodeStatus  map[string]*NodeStatus
	nodeMetrics map[string]*NodeMetrics
	nodeHistory map[string]*ring.Ring[HistorySample] // History per node (bounded)

	apiMu              sync.RWMutex
	agentRuns          map[string]*apiAgentRunState
	controllerAuditLog []ControllerAuditRecord
	configReloadMu     sync.RWMutex
	configReloader     func(context.Context, string) (RuntimeConfigReloadReport, error)
	lastConfigReload   RuntimeConfigReloadReport
	authCounters       controllerAuthCounters

	// Lifecycle
	server  *http.Server
	ctx     context.Context
	cancel  context.CancelFunc
	running bool

	leaderMu     sync.Mutex
	leaderCtx    context.Context
	leaderCancel context.CancelFunc
	leaderActive bool
	lastHAState  HAState
}

// New creates a new controller
func New(cfg Config, logger *zap.Logger) (*Controller, error) {
	if logger == nil {
		logger, _ = zap.NewProduction()
	}
	rawMode := strings.TrimSpace(cfg.Deployment.Mode)
	if rawMode != "" && normalizeControllerDeploymentMode(rawMode) == "" {
		return nil, fmt.Errorf("unsupported deployment.mode %q", rawMode)
	}
	if normalized := normalizeControllerDeploymentMode(cfg.Deployment.Mode); normalized != "" {
		cfg.Deployment.Mode = normalized
	} else {
		cfg.Deployment.Mode = defaultDeploymentMode
	}
	cfg.API = normalizeAPIConfig(cfg.API)

	c := &Controller{
		config: cfg,
		logger: logger.With(zap.String("component", "controller")),
		client: &http.Client{
			Timeout: cfg.ScrapeTimeout,
		},
		nodeStatus:  make(map[string]*NodeStatus),
		nodeMetrics: make(map[string]*NodeMetrics),
		nodeHistory: make(map[string]*ring.Ring[HistorySample]),
		agentRuns:   make(map[string]*apiAgentRunState),
	}

	resolvedAuth, err := ResolveAuthConfig(cfg.Auth, c.logger)
	if err != nil {
		return nil, err
	}
	resolvedAuth, err = enforceControllerSecurityPosture(cfg, resolvedAuth, c.logger)
	if err != nil {
		return nil, err
	}
	c.auth = resolvedAuth
	if err := c.initIngest(); err != nil {
		return nil, err
	}
	haCoord, err := newHACoordinator(cfg.HA, c.logger)
	if err != nil {
		return nil, err
	}
	c.haCoordinator = haCoord
	staticProbes := make([]inventory.StaticProbe, 0, len(cfg.Nodes)+len(cfg.Inventory.StaticTargets))
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
			Enabled: true,
			Labels:  node.Labels,
		})
	}
	staticProbes = append(staticProbes, cfg.Inventory.StaticTargets...)
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
		c.rebuildIngestServer()
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
		queryCfg.APIKey = nonEmptyString(os.Getenv("SRE_AGENT_LLM_API_KEY"))
		queryCfg.RAG = parseBoolEnv("SRE_AGENT_RAG_ENABLED", cfg.Agent.RAGEnabled)
		queryCfg.RAGPath = nonEmptyString(os.Getenv("SRE_AGENT_RAG_INDEX_PATH"), cfg.Agent.RAGIndexPath, queryCfg.RAGPath)
		queryCfg.RAGTopK = parseIntEnv("SRE_AGENT_RAG_TOP_K", maxPositiveInt(cfg.Agent.RAGTopK, cfg.Agent.RAGMaxSnippets, queryCfg.RAGTopK))
		queryCfg.RAGChars = parseIntEnv("SRE_AGENT_RAG_MAX_SNIPPET_CHARS", maxPositiveInt(cfg.Agent.RAGMaxChars, queryCfg.RAGChars))
		queryCfg.RAGMaxQueryChars = parseIntEnv("SRE_AGENT_RAG_MAX_QUERY_CHARS", maxPositiveInt(cfg.Agent.RAGMaxQueryChars, queryCfg.RAGMaxQueryChars))
		queryCfg.RAGMaxFindings = parseIntEnv("SRE_AGENT_RAG_MAX_FINDINGS", maxPositiveInt(cfg.Agent.RAGMaxFindings, queryCfg.RAGMaxFindings))
		queryCfg.RAGMinConfidence = parseFloatEnv("SRE_AGENT_RAG_MIN_CONFIDENCE", cfg.Agent.RAGMinConfidence)
		queryCfg.RAGDatasetPath = nonEmptyString(os.Getenv("SRE_AGENT_RAG_DATASET_PATH"), cfg.Agent.RAGDatasetPath, queryCfg.RAGDatasetPath)
		queryCfg.RAGChunkSize = parseIntEnv("SRE_AGENT_RAG_CHUNK_SIZE", maxPositiveInt(cfg.Agent.RAGChunkSize, queryCfg.RAGChunkSize))
		queryCfg.RAGChunkOverlap = parseIntEnv("SRE_AGENT_RAG_CHUNK_OVERLAP", maxPositiveInt(cfg.Agent.RAGChunkOverlap, queryCfg.RAGChunkOverlap))
		queryCfg.RAGChunkStrategy = nonEmptyString(os.Getenv("SRE_AGENT_RAG_CHUNK_STRATEGY"), cfg.Agent.RAGChunkStrategy, queryCfg.RAGChunkStrategy)
		queryCfg.RAGRetrievalMode = nonEmptyString(os.Getenv("SRE_AGENT_RAG_RETRIEVAL_MODE"), cfg.Agent.RAGRetrievalMode, queryCfg.RAGRetrievalMode)
		queryCfg.RAGEmbeddingProvider = nonEmptyString(os.Getenv("SRE_AGENT_RAG_EMBEDDING_PROVIDER"), cfg.Agent.RAGEmbeddingProvider, queryCfg.RAGEmbeddingProvider)
		queryCfg.RAGEmbeddingModel = nonEmptyString(os.Getenv("SRE_AGENT_RAG_EMBEDDING_MODEL"), cfg.Agent.RAGEmbeddingModel, queryCfg.RAGEmbeddingModel)
		queryCfg.RAGEmbeddingBaseURL = nonEmptyString(os.Getenv("SRE_AGENT_RAG_EMBEDDING_BASE_URL"), cfg.Agent.RAGEmbeddingBaseURL, queryCfg.RAGEmbeddingBaseURL)
		queryCfg.RAGEmbeddingAPIKey = nonEmptyString(os.Getenv("SRE_AGENT_RAG_EMBEDDING_API_KEY"), cfg.Agent.RAGEmbeddingAPIKey, queryCfg.RAGEmbeddingAPIKey)
		queryCfg.RAGVectorBackend = nonEmptyString(os.Getenv("SRE_AGENT_RAG_VECTOR_BACKEND"), cfg.Agent.RAGVectorBackend, queryCfg.RAGVectorBackend)
		queryCfg.RAGVectorEndpoint = nonEmptyString(os.Getenv("SRE_AGENT_RAG_VECTOR_ENDPOINT"), cfg.Agent.RAGVectorEndpoint, queryCfg.RAGVectorEndpoint)
		queryCfg.RAGVectorCollection = nonEmptyString(os.Getenv("SRE_AGENT_RAG_VECTOR_COLLECTION"), cfg.Agent.RAGVectorCollection, queryCfg.RAGVectorCollection)
		queryCfg.RAGVectorDatabase = nonEmptyString(os.Getenv("SRE_AGENT_RAG_VECTOR_DATABASE"), cfg.Agent.RAGVectorDatabase, queryCfg.RAGVectorDatabase)
		queryCfg.RAGVectorToken = nonEmptyString(os.Getenv("SRE_AGENT_RAG_VECTOR_TOKEN"), cfg.Agent.RAGVectorToken, queryCfg.RAGVectorToken)
		queryCfg.RAGVectorTimeout = parseDurationEnv("SRE_AGENT_RAG_VECTOR_TIMEOUT", maxPositiveDuration(cfg.Agent.RAGVectorTimeout, queryCfg.RAGVectorTimeout))
		queryCfg.RAGRebuildPolicy = nonEmptyString(os.Getenv("SRE_AGENT_RAG_REBUILD_POLICY"), cfg.Agent.RAGRebuildPolicy, queryCfg.RAGRebuildPolicy)
		queryCfg.RAGDocs = append(queryCfg.RAGDocs, cfg.Agent.RAGPaths...)
		queryCfg.RAGDocs = append(queryCfg.RAGDocs, cfg.Agent.RAGSourcePaths...)
		queryCfg.RAGDocs = dedupeStrings(queryCfg.RAGDocs)
		for _, raw := range []string{
			strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_SOURCE_PATHS")),
			strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_DOC_PATHS")),
		} {
			if raw == "" {
				continue
			}
			parts := strings.Split(raw, ",")
			docs := make([]string, 0, len(parts))
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				docs = append(docs, part)
			}
			if len(docs) > 0 {
				queryCfg.RAGDocs = docs
				break
			}
		}
		queryCfg.DryRun = !cfg.Agent.LLMEnabled || parseBoolEnv("SRE_AGENT_DRY_RUN", true)
		if cfg.Agent.LLMEnabled {
			queryCfg.DryRun = parseBoolEnv("SRE_AGENT_DRY_RUN", queryCfg.DryRun)
		}
		queryCfg.RequireApprovalToken = parseBoolEnv("SRE_AGENT_REQUIRE_APPROVAL_TOKEN", queryCfg.RequireApprovalToken)
		queryCfg.PendingActionTTL = parseDurationEnv("SRE_AGENT_ACTION_APPROVAL_TTL", queryCfg.PendingActionTTL)
		queryCfg.MaxPendingActions = parseIntEnv("SRE_AGENT_MAX_PENDING_ACTIONS", queryCfg.MaxPendingActions)
		queryCfg.MaxActionsPerQuery = parseIntEnv("SRE_AGENT_MAX_ACTIONS_PER_QUERY", queryCfg.MaxActionsPerQuery)
		queryCfg.MaxConcurrentQueries = parseIntEnv("SRE_AGENT_MAX_CONCURRENT_QUERIES", queryCfg.MaxConcurrentQueries)
		queryCfg.MaxQueryChars = parseIntEnv("SRE_AGENT_LLM_MAX_QUERY_CHARS", queryCfg.MaxQueryChars)
		queryCfg.MaxTokens = parseIntEnv("SRE_AGENT_LLM_MAX_TOKENS", queryCfg.MaxTokens)
		queryCfg.MaxRetries = parseIntEnv("SRE_AGENT_LLM_MAX_RETRIES", queryCfg.MaxRetries)
		queryCfg.RetryBase = parseDurationEnv("SRE_AGENT_LLM_RETRY_BASE", queryCfg.RetryBase)
		queryCfg.RetryMax = parseDurationEnv("SRE_AGENT_LLM_RETRY_MAX", queryCfg.RetryMax)
		queryCfg.RateLimitRPS = parseFloatEnv("SRE_AGENT_LLM_RATE_LIMIT_RPS", queryCfg.RateLimitRPS)
		queryCfg.RateBurst = parseIntEnv("SRE_AGENT_LLM_RATE_BURST", queryCfg.RateBurst)
		queryCfg.Timeout = parseDurationEnv("SRE_AGENT_LLM_TIMEOUT", queryCfg.Timeout)
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

		if queryCfg.RAG {
			ragCfg := rag.DefaultConfig()
			ragCfg.Enabled = true
			ragCfg.DatasetPath = queryCfg.RAGDatasetPath
			ragCfg.SourcePaths = append([]string(nil), queryCfg.RAGDocs...)
			ragCfg.IndexPath = queryCfg.RAGPath
			ragCfg.TopK = queryCfg.RAGTopK
			ragCfg.MaxSnippetChars = queryCfg.RAGChars
			ragCfg.ChunkSize = queryCfg.RAGChunkSize
			ragCfg.ChunkOverlap = queryCfg.RAGChunkOverlap
			ragCfg.ChunkStrategy = queryCfg.RAGChunkStrategy
			ragCfg.RetrievalMode = queryCfg.RAGRetrievalMode
			ragCfg.EmbeddingProvider = queryCfg.RAGEmbeddingProvider
			ragCfg.EmbeddingModel = queryCfg.RAGEmbeddingModel
			ragCfg.EmbeddingBaseURL = queryCfg.RAGEmbeddingBaseURL
			ragCfg.EmbeddingAPIKey = queryCfg.RAGEmbeddingAPIKey
			ragCfg.VectorBackend = queryCfg.RAGVectorBackend
			ragCfg.VectorEndpoint = queryCfg.RAGVectorEndpoint
			ragCfg.VectorCollection = queryCfg.RAGVectorCollection
			ragCfg.VectorDatabase = queryCfg.RAGVectorDatabase
			ragCfg.VectorToken = queryCfg.RAGVectorToken
			ragCfg.VectorTimeout = queryCfg.RAGVectorTimeout
			ragCfg.RebuildPolicy = queryCfg.RAGRebuildPolicy
			sharedRAG, ragErr := rag.NewService(ragCfg, c.logger)
			if ragErr != nil {
				c.logger.Warn("failed to initialize shared rag service", zap.Error(ragErr))
			} else {
				c.ragService = sharedRAG
				if c.agentEngine != nil {
					c.agentEngine.SetKnowledgeBase(sharedRAG)
				}
				queryCfg.RAG = false
			}
		}

		queryService, err := agentcore.NewQueryService(queryCfg, c.ingestStore, c.gpuStore, c.logger)
		if err != nil {
			c.logger.Warn("failed to initialize agent query service", zap.Error(err))
		} else {
			c.agentService = queryService
			if c.ragService != nil {
				c.agentService.SetRetriever(c.ragService)
			}
			if c.metricHistory != nil {
				c.agentService.SetMetricHistoryProvider(c.metricHistory)
			}
		}

		var topologyProvider agentcore.TopologyProvider
		if c.k8sManager != nil {
			topologyProvider = k8sTopologyWorkflowProvider{manager: c.k8sManager}
		}
		c.agentWorkflow = agentcore.NewWorkflowEngine(
			workflowConfigForController(cfg),
			c.ingestStore,
			c.logIndex,
			topologyProvider,
			c.logger,
		)
		if c.ragService != nil {
			c.agentWorkflow.SetKnowledgeBase(c.ragService)
		}
		if c.metricHistory != nil {
			c.agentWorkflow.SetMetricHistoryProvider(c.metricHistory)
		}

		// Demo mode: seed synthetic telemetry so agent endpoints return non-empty results
		if parseBoolEnv("SRE_AGENT_DEMO_MODE", false) {
			c.logger.Info("demo mode enabled; seeding synthetic telemetry data")
			agentcore.SeedDemoData(c.ingestStore, c.logIndex)
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

	if err := validateControllerDeploymentPosture(c); err != nil {
		return nil, err
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
	if c.timeseriesService != nil {
		if err := c.timeseriesService.Start(c.ctx); err != nil {
			return err
		}
	}

	if c.gpuStore != nil {
		if err := c.gpuStore.Start(); err != nil {
			return err
		}
	}

	if c.k8sManager != nil {
		if err := c.k8sManager.Start(c.ctx); err != nil {
			return err
		}
	}
	if c.haCoordinator != nil {
		if c.config.HA.AdvertiseHTTP == "" {
			c.config.HA.AdvertiseHTTP = c.config.ListenAddr
		}
		if c.config.HA.AdvertiseGRPC == "" {
			c.config.HA.AdvertiseGRPC = c.config.GRPCListenAddr
		}
		if err := c.haCoordinator.Start(c.ctx, c.onHAStateChange); err != nil {
			return err
		}
	}
	if err := c.startIngest(); err != nil {
		c.deactivateLeaderResponsibilities()
		if c.haCoordinator != nil {
			if stopErr := c.haCoordinator.Stop(context.Background()); stopErr != nil {
				c.logger.Warn("failed to stop ha coordinator after ingest start error", zap.Error(stopErr))
			}
		}
		return err
	}

	// Setup HTTP server
	mux := http.NewServeMux()
	c.registerHandlers(mux)

	var handler http.Handler = mux
	handler = c.wrapHTTPHandler(handler)
	if c.auth.Enabled {
		handler = c.wrapHTTPAuthentication(handler)
		c.logger.Info("controller api authentication enabled",
			zap.String("mode", string(c.auth.Mode)),
			zap.String("api_key_mode", string(c.auth.APIKeyMode)),
			zap.String("token_secret_env", c.auth.TokenSecretEnv),
			zap.String("read_key_env", c.auth.ReadKeyEnv),
			zap.String("action_key_env", c.auth.ActionKeyEnv),
			zap.Bool("ingest_auth_enabled", c.auth.IngestAuthEnabled))
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

	c.deactivateLeaderResponsibilities()
	if c.haCoordinator != nil {
		if err := c.haCoordinator.Stop(context.Background()); err != nil {
			c.logger.Warn("failed to stop ha coordinator", zap.Error(err))
		}
	}
	c.stopIngest()
	if c.ingestStore != nil {
		if err := c.ingestStore.Close(); err != nil {
			c.logger.Warn("failed to close ingest store persistence", zap.Error(err))
		}
	}
	if c.timeseriesService != nil {
		if err := c.timeseriesService.Close(); err != nil {
			c.logger.Warn("failed to close timeseries service", zap.Error(err))
		}
	}

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
	if c.agentWorkflow != nil {
		if err := c.agentWorkflow.Close(); err != nil {
			c.logger.Warn("failed to close workflow durable store", zap.Error(err))
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

func (c *Controller) onHAStateChange(state HAState) {
	if c == nil {
		return
	}
	prev := c.lastHAState
	c.lastHAState = state

	if prev.Role == state.Role && prev.Active == state.Active && prev.LeaderID == state.LeaderID && prev.LastError == state.LastError {
		return
	}

	c.logger.Info("controller ha state changed",
		zap.String("backend", state.Backend),
		zap.String("role", string(state.Role)),
		zap.Bool("active", state.Active),
		zap.Bool("read_only", state.ReadOnly),
		zap.String("leader_id", state.LeaderID),
		zap.String("last_error", state.LastError),
	)

	c.appendInternalControllerAudit(ControllerAuditRecord{
		Action:   "ha_role_transition",
		Resource: firstNonEmpty(state.NodeID, c.config.ListenAddr),
		Status:   map[bool]string{true: "active", false: "standby"}[state.Active],
		Output:   fmt.Sprintf("role=%s leader=%s backend=%s error=%s", state.Role, state.LeaderID, state.Backend, state.LastError),
	})

	if state.Active {
		if err := c.activateLeaderResponsibilities(); err != nil {
			c.logger.Error("failed to activate leader responsibilities", zap.Error(err))
		}
		return
	}
	c.deactivateLeaderResponsibilities()
}

func (c *Controller) activateLeaderResponsibilities() error {
	if c == nil {
		return nil
	}

	c.leaderMu.Lock()
	if c.leaderActive {
		c.leaderMu.Unlock()
		return nil
	}
	leaderCtx, leaderCancel := context.WithCancel(c.ctx)
	c.leaderCtx = leaderCtx
	c.leaderCancel = leaderCancel
	c.leaderActive = true
	c.leaderMu.Unlock()

	started := make([]func(), 0, 8)
	rollback := func() {
		for i := len(started) - 1; i >= 0; i-- {
			started[i]()
		}
		c.leaderMu.Lock()
		c.leaderActive = false
		c.leaderCtx = nil
		c.leaderCancel = nil
		c.leaderMu.Unlock()
	}

	if c.analysisExt != nil {
		if err := c.analysisExt.engine.Start(leaderCtx); err != nil {
			rollback()
			return err
		}
		started = append(started, func() { _ = c.analysisExt.engine.Stop() })
	}
	if c.agentEngine != nil {
		if err := c.agentEngine.Start(leaderCtx); err != nil {
			rollback()
			return err
		}
		started = append(started, func() { _ = c.agentEngine.Stop() })
	}
	if c.incidentCoordinator != nil {
		c.incidentCoordinator.Start(leaderCtx)
		started = append(started, func() { c.incidentCoordinator.Stop() })
	}
	if c.orchestrationManager != nil {
		if err := c.orchestrationManager.Start(leaderCtx); err != nil {
			rollback()
			return err
		}
		started = append(started, func() { c.orchestrationManager.Stop() })
	}
	if c.checks != nil {
		if err := c.checks.Start(leaderCtx); err != nil {
			rollback()
			return err
		}
		started = append(started, func() { _ = c.checks.Stop() })
	}

	c.scrapeAll(leaderCtx)
	go c.scrapeLoop(leaderCtx)
	return nil
}

func (c *Controller) deactivateLeaderResponsibilities() {
	if c == nil {
		return
	}

	c.leaderMu.Lock()
	if !c.leaderActive {
		c.leaderMu.Unlock()
		return
	}
	cancel := c.leaderCancel
	c.leaderCancel = nil
	c.leaderCtx = nil
	c.leaderActive = false
	c.leaderMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if c.analysisExt != nil {
		if err := c.analysisExt.engine.Stop(); err != nil {
			c.logger.Warn("failed to stop analysis engine on leadership loss", zap.Error(err))
		}
	}
	if c.agentEngine != nil {
		if err := c.agentEngine.Stop(); err != nil {
			c.logger.Warn("failed to stop agent engine on leadership loss", zap.Error(err))
		}
	}
	if c.incidentCoordinator != nil {
		c.incidentCoordinator.Stop()
	}
	if c.orchestrationManager != nil {
		c.orchestrationManager.Stop()
	}
	if c.checks != nil {
		if err := c.checks.Stop(); err != nil {
			c.logger.Warn("failed to stop checks manager on leadership loss", zap.Error(err))
		}
	}
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
	mux.HandleFunc("/api/v1/ha/status", c.withCORS(c.handleHAStatus))
	c.registerIngestHandlers(mux)
	if c.inventoryManager != nil {
		c.registerInventoryHandlers(mux)
	}
	if c.gpuStore != nil {
		c.registerGPUHandlers(mux)
	}
	// Always expose K8s API routes; handlers degrade to disabled/empty payloads when integration is off.
	c.registerK8sHandlers(mux)

	if c.analysisExt != nil {
		c.RegisterAnalysisHandlers(mux, c.analysisExt)
	}
	if c.agentEngine != nil || c.agentService != nil {
		c.registerAgentHandlers(mux)
	}
	c.registerRAGHandlers(mux)
	if c.checks != nil {
		c.RegisterCheckHandlers(mux)
	}
	if c.orchestrationManager != nil {
		c.registerOrchestrationHandlers(mux)
	}
	if c.incidentCoordinator != nil {
		mux.HandleFunc("/api/v1/incidents/alerts", c.withCORS(c.handleIncidentAlert))
	}
	c.registerSecurityHandlers(mux)
	c.registerEBPFHandlers(mux)
	c.registerAPIFirstHandlers(mux)

	// Health check
	mux.HandleFunc("/health", c.handleHealth)
	mux.HandleFunc("/healthz", c.handleHealth)
	mux.HandleFunc("/readyz", c.handleReady)

	// Prometheus metrics (aggregated from all nodes)
	mux.HandleFunc("/metrics", c.handlePrometheusMetrics)

	// Web UI static files
	c.setupWebUI(mux)
}

func (c *Controller) withCORS(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			if !c.isAllowedCORSOrigin(origin, r) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", canonicalOrigin(origin))
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Authorization, X-API-Key")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
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
		if !c.requireActiveController(w) {
			return
		}
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
		if !c.requireActiveController(w) {
			return
		}
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
	if !c.requireActiveController(w) {
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
		version = release.EffectiveVersion()
	}
	posture := c.deploymentPostureStatus()

	resp := map[string]interface{}{
		"version":         version,
		"uptime":          "running",
		"total_nodes":     totalNodes,
		"healthy_nodes":   healthyCount,
		"scrape_interval": c.config.ScrapeInterval.String(),
		"listen_address":  c.config.ListenAddr,
		"ha": map[string]interface{}{
			"enabled":                          c.haState().Enabled,
			"backend":                          c.haState().Backend,
			"mode":                             c.haState().Mode,
			"role":                             c.haState().Role,
			"active":                           c.haState().Active,
			"read_only":                        c.haState().ReadOnly,
			"node_id":                          c.haState().NodeID,
			"leader_id":                        c.haState().LeaderID,
			"leader_http":                      c.haState().LeaderHTTP,
			"leader_grpc":                      c.haState().LeaderGRPC,
			"last_transition_at":               c.haState().LastTransitionAt,
			"transition_count":                 c.haState().TransitionCount,
			"allow_follower_read":              c.haState().AllowFollowerRead,
			"write_sensitive_requests_guarded": c.haState().Enabled,
			"write_sensitive_requests_blocked": c.haState().Enabled && !c.haState().Active,
			"grpc_ingest_writes_guarded":       c.grpcIngestWritesGuarded(),
			"grpc_ingest_writes_blocked":       c.grpcIngestWritesBlocked(),
		},
		"deployment": map[string]interface{}{
			"mode":              c.config.Deployment.Mode,
			"cluster_name":      c.config.Deployment.ClusterName,
			"data_root":         c.config.Deployment.DataRoot,
			"external_url":      c.config.Deployment.ExternalURL,
			"insecure_override": posture.InsecureOverride,
			"production_like":   posture.ProductionLike,
			"degraded":          posture.Degraded,
			"degraded_reasons":  posture.Reasons,
		},
		"api": map[string]interface{}{
			"rate_limit_enabled":        c.config.API.RateLimitEnabled,
			"rate_limit_rps":            c.config.API.RateLimitRPS,
			"rate_limit_burst":          c.config.API.RateLimitBurst,
			"action_rate_limit_enabled": c.config.API.ActionRateLimitEnabled,
			"action_rate_limit_rps":     c.config.API.ActionRateLimitRPS,
			"action_rate_limit_burst":   c.config.API.ActionRateLimitBurst,
			"audit_mutations":           c.config.API.AuditMutations,
			"cors": map[string]interface{}{
				"mode":               c.corsMode(),
				"allowed_origins":    c.allowedCORSOriginsForStatus(),
				"same_origin_only":   c.corsSameOriginOnly(),
				"local_dev_defaults": c.corsUsesLocalDevDefaults(),
			},
		},
		"auth": map[string]interface{}{
			"enabled":                        c.auth.Enabled,
			"mode":                           c.auth.Mode,
			"api_key_mode":                   c.auth.APIKeyMode,
			"token_secret_env":               c.auth.TokenSecretEnv,
			"token_issuer":                   c.auth.TokenIssuer,
			"token_audience":                 c.auth.TokenAudience,
			"ingest_auth_enabled":            c.auth.IngestAuthEnabled,
			"ingest_token_audience":          c.auth.IngestTokenAudience,
			"read_key_env":                   c.auth.ReadKeyEnv,
			"action_key_env":                 c.auth.ActionKeyEnv,
			"deployment_mode":                c.auth.DeploymentMode,
			"insecure_override":              c.auth.InsecureOverride,
			"local_dev_exception":            c.auth.LocalDevBypass,
			"compatibility_api_keys_enabled": c.auth.APIKeyMode != ControllerAPIKeyModeDisabled,
			"anonymous_http_allowed":         !c.auth.Enabled,
			"anonymous_ingest_allowed":       !c.auth.Enabled || !c.auth.IngestAuthEnabled,
			"http_authentication_failures":   c.authCounters.httpAuthnFailures.Load(),
			"http_authorization_failures":    c.authCounters.httpAuthzFailures.Load(),
			"ingest_authentication_failures": func() uint64 {
				if c.ingestServer == nil {
					return 0
				}
				return c.ingestServer.Stats().AuthnRejectedTotal
			}(),
			"ingest_authorization_failures": func() uint64 {
				if c.ingestServer == nil {
					return 0
				}
				return c.ingestServer.Stats().AuthzRejectedTotal
			}(),
		},
		"durability": map[string]interface{}{
			"workflow":  posture.Workflow,
			"artifacts": posture.Artifacts,
			"ingest":    posture.Ingest,
			"hot_state": map[string]interface{}{
				"ingest_in_process":       true,
				"shared_across_failover":  false,
				"owner":                   "controller_process",
				"reconstruction_required": true,
			},
		},
		"ingest_transport": c.ingestTransportStatus(),
	}
	if c.ingestStore != nil {
		resp["collector_coverage"] = buildFleetCoverageSummary(c.ingestStore.Snapshot(), time.Now().UTC())
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
	if c.timeseriesService != nil {
		tsdb := c.timeseriesService.Status()
		resp["tsdb"] = map[string]interface{}{
			"enabled":         tsdb.Enabled,
			"provider":        tsdb.Provider,
			"mode":            tsdb.Mode,
			"ready":           tsdb.Ready,
			"healthy":         tsdb.Healthy,
			"fallback_active": tsdb.FallbackActive,
			"bucket":          tsdb.Bucket,
			"retention":       tsdb.Retention,
		}
	}
	resp["config_reload"] = c.configReloadStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleHealth returns health status
func (c *Controller) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (c *Controller) handleReady(w http.ResponseWriter, r *http.Request) {
	state := c.haState()
	ready := c.ingestStore != nil
	if state.Enabled && !state.Active && !state.AllowFollowerRead {
		ready = false
	}
	if !ready {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
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

		fmt.Fprintf(bw, "# HELP agent_rag_skipped_context_total AGENT queries that skipped retrieval because operational symptom context was too weak\n")
		fmt.Fprintf(bw, "# TYPE agent_rag_skipped_context_total counter\n")
		fmt.Fprintf(bw, "agent_rag_skipped_context_total %d\n", stats.RAGSkippedContextTotal)

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

	if c.agentWorkflow != nil {
		stats := c.agentWorkflow.Metrics()
		fmt.Fprintf(bw, "# HELP agent_reasoning_steps_total Total cold-path reasoning steps attempted by the workflow engine\n")
		fmt.Fprintf(bw, "# TYPE agent_reasoning_steps_total counter\n")
		fmt.Fprintf(bw, "agent_reasoning_steps_total %d\n", stats.ReasoningStepsTotal)

		fmt.Fprintf(bw, "# HELP agent_reasoning_failures_total Workflow reasoning attempts that failed or were rejected by guardrails\n")
		fmt.Fprintf(bw, "# TYPE agent_reasoning_failures_total counter\n")
		fmt.Fprintf(bw, "agent_reasoning_failures_total %d\n", stats.ReasoningFailuresTotal)

		fmt.Fprintf(bw, "# HELP agent_reasoning_parse_failures_total Workflow reasoning parse failures\n")
		fmt.Fprintf(bw, "# TYPE agent_reasoning_parse_failures_total counter\n")
		fmt.Fprintf(bw, "agent_reasoning_parse_failures_total %d\n", stats.ReasoningParseFailTotal)

		fmt.Fprintf(bw, "# HELP agent_reasoning_validation_failures_total Workflow reasoning validation failures\n")
		fmt.Fprintf(bw, "# TYPE agent_reasoning_validation_failures_total counter\n")
		fmt.Fprintf(bw, "agent_reasoning_validation_failures_total %d\n", stats.ReasoningValidFailTotal)

		fmt.Fprintf(bw, "# HELP agent_reasoning_llm_errors_total Workflow reasoning LLM errors\n")
		fmt.Fprintf(bw, "# TYPE agent_reasoning_llm_errors_total counter\n")
		fmt.Fprintf(bw, "agent_reasoning_llm_errors_total %d\n", stats.ReasoningLLMErrorTotal)

		fmt.Fprintf(bw, "# HELP agent_reasoning_budget_exhausted_total Workflow reasoning attempts that exhausted the iteration budget\n")
		fmt.Fprintf(bw, "# TYPE agent_reasoning_budget_exhausted_total counter\n")
		fmt.Fprintf(bw, "agent_reasoning_budget_exhausted_total %d\n", stats.ReasoningBudgetExhTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_actions_executed_total Workflow remediation actions executed\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_actions_executed_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_actions_executed_total %d\n", stats.ActionsExecutedTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_actions_dry_run_total Workflow remediation actions in dry-run mode\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_actions_dry_run_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_actions_dry_run_total %d\n", stats.ActionsDryRunTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_actions_blocked_total Workflow remediation actions blocked\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_actions_blocked_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_actions_blocked_total %d\n", stats.ActionsBlockedTotal)

		fmt.Fprintf(bw, "# HELP agent_avg_confidence Average confidence emitted by cold-path reasoning results\n")
		fmt.Fprintf(bw, "# TYPE agent_avg_confidence gauge\n")
		fmt.Fprintf(bw, "agent_avg_confidence %g\n", stats.AvgConfidence)

		fmt.Fprintf(bw, "# HELP agent_token_cost_total Estimated reasoning tokens consumed by workflow analysis\n")
		fmt.Fprintf(bw, "# TYPE agent_token_cost_total counter\n")
		fmt.Fprintf(bw, "agent_token_cost_total %d\n", stats.TokenCostTotal)

		fmt.Fprintf(bw, "# HELP agent_token_cost_per_incident Estimated reasoning tokens consumed per RCA incident\n")
		fmt.Fprintf(bw, "# TYPE agent_token_cost_per_incident gauge\n")
		fmt.Fprintf(bw, "agent_token_cost_per_incident %g\n", stats.TokenCostPerIncident)

		fmt.Fprintf(bw, "# HELP agent_hallucination_proxy_total Proxy counter for LLM outputs rejected by parse, validation, or safety checks\n")
		fmt.Fprintf(bw, "# TYPE agent_hallucination_proxy_total counter\n")
		fmt.Fprintf(bw, "agent_hallucination_proxy_total %d\n", stats.HallucinationProxyTotal)

		fmt.Fprintf(bw, "# HELP agent_retrieval_hits_total Retrieved knowledge hits accepted into workflow evidence\n")
		fmt.Fprintf(bw, "# TYPE agent_retrieval_hits_total counter\n")
		fmt.Fprintf(bw, "agent_retrieval_hits_total %d\n", stats.RetrievalHitsTotal)

		fmt.Fprintf(bw, "# HELP agent_retrieval_miss_total Retrieval calls that returned no useful knowledge hits\n")
		fmt.Fprintf(bw, "# TYPE agent_retrieval_miss_total counter\n")
		fmt.Fprintf(bw, "agent_retrieval_miss_total %d\n", stats.RetrievalMissTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_latency_seconds_total Total time spent running deterministic workflow pipelines\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_latency_seconds_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_latency_seconds_total %g\n", stats.WorkflowLatencySeconds)

		fmt.Fprintf(bw, "# HELP agent_incident_rca_latency_seconds_total Total time spent running RCA workflow pipelines\n")
		fmt.Fprintf(bw, "# TYPE agent_incident_rca_latency_seconds_total counter\n")
		fmt.Fprintf(bw, "agent_incident_rca_latency_seconds_total %g\n", stats.IncidentRCALatencySeconds)

		fmt.Fprintf(bw, "# HELP agent_workflow_verifications_total Total workflow verification checks executed\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_verifications_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_verifications_total %d\n", stats.VerificationsTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_verification_success_total Successful workflow verification checks\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_verification_success_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_verification_success_total %d\n", stats.VerificationSuccessTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_verification_failure_total Failed workflow verification checks\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_verification_failure_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_verification_failure_total %d\n", stats.VerificationFailureTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_approvals_pending_total Workflow steps paused pending approval\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_approvals_pending_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_approvals_pending_total %d\n", stats.ApprovalsPendingTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_compensations_total Workflow compensations or rollbacks attempted\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_compensations_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_compensations_total %d\n", stats.CompensationsTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_evidence_packages_total Workflow evidence packages generated\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_evidence_packages_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_evidence_packages_total %d\n", stats.EvidencePackagesTotal)

		fmt.Fprintf(bw, "# HELP agent_workflow_memory_writebacks_total Workflow memories persisted for future retrieval\n")
		fmt.Fprintf(bw, "# TYPE agent_workflow_memory_writebacks_total counter\n")
		fmt.Fprintf(bw, "agent_workflow_memory_writebacks_total %d\n", stats.MemoryWritebacksTotal)

		writeCounterSamples(bw, "sre_agent_skill_invocations_total", "Skill calls by result.", []string{"skill", "family", "status", "mode"}, stats.SkillInvocations)
		writeHistogramSamples(bw, "sre_agent_skill_duration_seconds", "Skill call latency.", []string{"skill", "family", "mode"}, stats.SkillDurations)
		writeCounterSamples(bw, "sre_agent_skill_low_yield_total", "Low-yield skill calls.", []string{"skill", "family", "mode"}, stats.SkillLowYield)
		writeCounterSamples(bw, "sre_agent_skill_policy_block_total", "Policy-blocked skill attempts.", []string{"skill", "reason", "mode"}, stats.SkillPolicyBlocks)
		writeCounterSamples(bw, "sre_agent_skill_approval_required_total", "Approval-gated skill attempts.", []string{"skill", "mode"}, stats.SkillApprovalRequired)
		writeHistogramSamples(bw, "sre_agent_skill_score", "Candidate score distribution.", []string{"skill", "family", "mode"}, stats.SkillScores)
		writeCounterSamples(bw, "sre_agent_adaptive_stop_total", "Adaptive runtime stop reasons.", []string{"reason", "mode"}, stats.AdaptiveStops)
		writeCounterSamples(bw, "sre_agent_rag_skill_calls_total", "RAG/knowledge skill calls.", []string{"intent", "status"}, stats.RAGSkillCalls)
		writeCounterSamples(bw, "sre_agent_artifact_persist_failures_total", "Artifact write failures.", []string{"kind"}, stats.ArtifactPersistFailures)
		writeCounterSamples(bw, "sre_agent_replay_validation_failures_total", "Replay validation failures.", []string{"reason"}, stats.ReplayValidationFailures)
	}

	if c.agentEngine != nil {
		stats := c.agentEngine.Status()
		fmt.Fprintf(bw, "# HELP agent_action_dry_run_total Incident action executions completed in dry-run mode\n")
		fmt.Fprintf(bw, "# TYPE agent_action_dry_run_total counter\n")
		fmt.Fprintf(bw, "agent_action_dry_run_total %d\n", stats.ActionDryRunTotal)

		fmt.Fprintf(bw, "# HELP agent_action_execute_total Incident action executions that changed state or completed rollback\n")
		fmt.Fprintf(bw, "# TYPE agent_action_execute_total counter\n")
		fmt.Fprintf(bw, "agent_action_execute_total %d\n", stats.ActionExecuteTotal)

		fmt.Fprintf(bw, "# HELP agent_action_blocked_total Incident action executions blocked by approval or runtime policy\n")
		fmt.Fprintf(bw, "# TYPE agent_action_blocked_total counter\n")
		fmt.Fprintf(bw, "agent_action_blocked_total %d\n", stats.ActionBlockedTotal)
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

func writeCounterSamples(w io.Writer, name, help string, labelNames []string, samples []agentcore.WorkflowMetricSample) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	if len(samples) == 0 {
		fmt.Fprintf(w, "%s%s 0\n", name, prometheusLabels(defaultMetricLabels(labelNames)))
		return
	}
	for _, sample := range samples {
		fmt.Fprintf(w, "%s%s %d\n", name, prometheusLabels(sample.Labels), sample.Value)
	}
}

func writeHistogramSamples(w io.Writer, name, help string, labelNames []string, samples []agentcore.WorkflowMetricHistogramSample) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s histogram\n", name)
	if len(samples) == 0 {
		labels := defaultMetricLabels(labelNames)
		fmt.Fprintf(w, "%s_count%s 0\n", name, prometheusLabels(labels))
		fmt.Fprintf(w, "%s_sum%s 0\n", name, prometheusLabels(labels))
		return
	}
	for _, sample := range samples {
		fmt.Fprintf(w, "%s_count%s %d\n", name, prometheusLabels(sample.Labels), sample.Count)
		fmt.Fprintf(w, "%s_sum%s %g\n", name, prometheusLabels(sample.Labels), sample.Sum)
	}
}

func defaultMetricLabels(labelNames []string) map[string]string {
	labels := make(map[string]string, len(labelNames))
	for _, name := range labelNames {
		labels[name] = "unknown"
	}
	return labels
}

func prometheusLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	order := []string{"skill", "family", "status", "mode", "reason", "intent", "kind"}
	seen := map[string]bool{}
	parts := make([]string, 0, len(labels))
	for _, key := range order {
		value, ok := labels[key]
		if !ok {
			continue
		}
		seen[key] = true
		parts = append(parts, fmt.Sprintf(`%s=%q`, key, sanitizePrometheusLabel(value)))
	}
	for key, value := range labels {
		if seen[key] {
			continue
		}
		parts = append(parts, fmt.Sprintf(`%s=%q`, key, sanitizePrometheusLabel(value)))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func sanitizePrometheusLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.ReplaceAll(value, `"`, `\"`)
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

func maxPositiveInt(values ...int) int {
	best := 0
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func maxPositiveDuration(values ...time.Duration) time.Duration {
	var best time.Duration
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
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

func parseFloatEnv(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

// scrapeLoop runs periodic scraping of all nodes
func (c *Controller) scrapeLoop(ctx context.Context) {
	ticker := time.NewTicker(c.config.ScrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.scrapeAll(ctx)
		}
	}
}

// scrapeAll scrapes metrics from all configured nodes
func (c *Controller) scrapeAll(ctx context.Context) {
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
			c.scrapeNode(ctx, n)
		}(node)
	}
	wg.Wait()
}

// scrapeNode scrapes metrics from a single node
func (c *Controller) scrapeNode(ctx context.Context, node NodeConfig) {
	url := fmt.Sprintf("http://%s/api/v1/metrics", node.Address)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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
			// Skip labeled series here to keep chart snapshots low-cardinality.
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
