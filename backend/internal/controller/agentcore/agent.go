package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/signalinsights"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/safeconv"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

var (
	// ErrLLMTimeout indicates the LLM call exceeded configured timeout.
	ErrLLMTimeout = errors.New("agent llm timeout")
	// ErrRateLimited indicates AGENT query rate limiting rejected the request.
	ErrRateLimited = errors.New("agent query rate limited")
	// ErrCircuitOpen indicates the LLM circuit breaker is open.
	ErrCircuitOpen = errors.New("agent llm circuit open")
	// ErrBusy indicates AGENT query concurrency capacity is exhausted.
	ErrBusy = errors.New("agent query busy")
	// ErrActionNotFound indicates a requested action ID does not exist in pending actions.
	ErrActionNotFound = errors.New("agent action not found")
	// ErrActionExpired indicates a requested action expired from pending cache.
	ErrActionExpired = errors.New("agent action expired")
	// ErrApprovalRequired indicates execution needs an approval token.
	ErrApprovalRequired = errors.New("agent action approval token required")
	// ErrApprovalInvalid indicates a provided approval token was invalid.
	ErrApprovalInvalid = errors.New("agent action approval token invalid")
	// ErrStaleTelemetry indicates telemetry freshness is beyond configured threshold.
	ErrStaleTelemetry = errors.New("agent telemetry stale")
	// ErrNoTelemetry indicates insufficient telemetry to justify an LLM call.
	ErrNoTelemetry = errors.New("agent telemetry insufficient")
)

// Config configures AGENT query execution and action orchestration.
type Config struct {
	Enabled bool `json:"enabled" yaml:"enabled"`

	Provider             string        `json:"provider" yaml:"provider"`
	Model                string        `json:"model" yaml:"model"`
	BaseURL              string        `json:"base_url" yaml:"base_url"`
	APIKey               string        `json:"api_key" yaml:"api_key"`
	RAG                  bool          `json:"rag_enabled" yaml:"rag_enabled"`
	RAGPath              string        `json:"rag_index_path" yaml:"rag_index_path"`
	RAGTopK              int           `json:"rag_top_k" yaml:"rag_top_k"`
	RAGChars             int           `json:"rag_max_snippet_chars" yaml:"rag_max_snippet_chars"`
	RAGMaxQueryChars     int           `json:"rag_max_query_chars" yaml:"rag_max_query_chars"`
	RAGMaxFindings       int           `json:"rag_max_findings" yaml:"rag_max_findings"`
	RAGMinConfidence     float64       `json:"rag_min_confidence" yaml:"rag_min_confidence"`
	RAGDocs              []string      `json:"rag_docs" yaml:"rag_docs"`
	RAGDatasetPath       string        `json:"rag_dataset_path" yaml:"rag_dataset_path"`
	RAGChunkSize         int           `json:"rag_chunk_size" yaml:"rag_chunk_size"`
	RAGChunkOverlap      int           `json:"rag_chunk_overlap" yaml:"rag_chunk_overlap"`
	RAGChunkStrategy     string        `json:"rag_chunk_strategy" yaml:"rag_chunk_strategy"`
	RAGRetrievalMode     string        `json:"rag_retrieval_mode" yaml:"rag_retrieval_mode"`
	RAGEmbeddingProvider string        `json:"rag_embedding_provider" yaml:"rag_embedding_provider"`
	RAGEmbeddingModel    string        `json:"rag_embedding_model" yaml:"rag_embedding_model"`
	RAGEmbeddingBaseURL  string        `json:"rag_embedding_base_url" yaml:"rag_embedding_base_url"`
	RAGEmbeddingAPIKey   string        `json:"rag_embedding_api_key" yaml:"rag_embedding_api_key"`
	RAGVectorBackend     string        `json:"rag_vector_backend" yaml:"rag_vector_backend"`
	RAGVectorEndpoint    string        `json:"rag_vector_endpoint" yaml:"rag_vector_endpoint"`
	RAGVectorCollection  string        `json:"rag_vector_collection" yaml:"rag_vector_collection"`
	RAGVectorDatabase    string        `json:"rag_vector_database" yaml:"rag_vector_database"`
	RAGVectorToken       string        `json:"rag_vector_token" yaml:"rag_vector_token"`
	RAGVectorTimeout     time.Duration `json:"rag_vector_timeout" yaml:"rag_vector_timeout"`
	RAGRebuildPolicy     string        `json:"rag_rebuild_policy" yaml:"rag_rebuild_policy"`

	Timeout       time.Duration `json:"timeout" yaml:"timeout"`
	MaxRetries    int           `json:"max_retries" yaml:"max_retries"`
	RetryBase     time.Duration `json:"retry_base" yaml:"retry_base"`
	RetryMax      time.Duration `json:"retry_max" yaml:"retry_max"`
	MaxTokens     int           `json:"max_tokens" yaml:"max_tokens"`
	MaxQueryChars int           `json:"max_query_chars" yaml:"max_query_chars"`

	RateLimitRPS         float64 `json:"rate_limit_rps" yaml:"rate_limit_rps"`
	RateBurst            int     `json:"rate_burst" yaml:"rate_burst"`
	MaxConcurrentQueries int     `json:"max_concurrent_queries" yaml:"max_concurrent_queries"`

	CircuitFailures      int           `json:"circuit_failures" yaml:"circuit_failures"`
	CircuitCooldown      time.Duration `json:"circuit_cooldown" yaml:"circuit_cooldown"`
	AnalysisReuseEnabled bool          `json:"analysis_reuse_enabled" yaml:"analysis_reuse_enabled"`
	AnalysisReuseWindow  time.Duration `json:"analysis_reuse_window" yaml:"analysis_reuse_window"`
	AnalysisReuseMaxKeys int           `json:"analysis_reuse_max_keys" yaml:"analysis_reuse_max_keys"`

	PlaybookFile string   `json:"playbook_file" yaml:"playbook_file"`
	DryRun       bool     `json:"dry_run" yaml:"dry_run"`
	AllowUnsafe  bool     `json:"allow_unsafe" yaml:"allow_unsafe"`
	Namespaces   []string `json:"namespaces" yaml:"namespaces"`
	ShellAllowed []string `json:"shell_allowed" yaml:"shell_allowed"`

	MaxActionsPerQuery        int           `json:"max_actions_per_query" yaml:"max_actions_per_query"`
	PendingActionTTL          time.Duration `json:"pending_action_ttl" yaml:"pending_action_ttl"`
	MaxPendingActions         int           `json:"max_pending_actions" yaml:"max_pending_actions"`
	RequireApprovalToken      bool          `json:"require_approval_token" yaml:"require_approval_token"`
	MaxTelemetryAge           time.Duration `json:"max_telemetry_age" yaml:"max_telemetry_age"`
	AllowActionsOnStaleData   bool          `json:"allow_actions_on_stale_data" yaml:"allow_actions_on_stale_data"`
	SkipLLMOnStaleTelemetry   bool          `json:"skip_llm_on_stale_telemetry" yaml:"skip_llm_on_stale_telemetry"`
	SkipLLMOnNoTelemetry      bool          `json:"skip_llm_on_no_telemetry" yaml:"skip_llm_on_no_telemetry"`
	EventWebhookURL           string        `json:"event_webhook_url" yaml:"event_webhook_url"`
	EventWebhookToken         string        `json:"event_webhook_token" yaml:"event_webhook_token"`
	EventWebhookTimeout       time.Duration `json:"event_webhook_timeout" yaml:"event_webhook_timeout"`
	EventPublishRetries       int           `json:"event_publish_retries" yaml:"event_publish_retries"`
	EventRetryBackoff         time.Duration `json:"event_retry_backoff" yaml:"event_retry_backoff"`
	EventSlackWebhookURL      string        `json:"event_slack_webhook_url" yaml:"event_slack_webhook_url"`
	EventPagerDutyRoutingKey  string        `json:"event_pagerduty_routing_key" yaml:"event_pagerduty_routing_key"`
	EventPagerDutyEventsURL   string        `json:"event_pagerduty_events_url" yaml:"event_pagerduty_events_url"`
	ActionTimeout             time.Duration `json:"action_timeout" yaml:"action_timeout"`
	MaxParallelActionExec     int           `json:"max_parallel_action_exec" yaml:"max_parallel_action_exec"`
	ExplainabilityEvidenceMax int           `json:"explainability_evidence_max" yaml:"explainability_evidence_max"`
}

// DefaultConfig returns conservative defaults with OpenAI as the primary provider.
func DefaultConfig() Config {
	return Config{
		Enabled:                   true,
		Provider:                  "openai",
		Model:                     "gpt-4o-mini",
		BaseURL:                   "https://api.openai.com/v1",
		RAG:                       false,
		RAGPath:                   "data/agent/rag/index.json",
		RAGTopK:                   4,
		RAGChars:                  1000,
		RAGMaxQueryChars:          640,
		RAGMaxFindings:            6,
		RAGMinConfidence:          0.18,
		RAGDocs:                   nil,
		RAGDatasetPath:            "./dataset",
		RAGChunkSize:              900,
		RAGChunkOverlap:           120,
		RAGChunkStrategy:          "auto",
		RAGRetrievalMode:          "hybrid",
		RAGEmbeddingProvider:      "local",
		RAGEmbeddingModel:         "local-hash-64",
		RAGVectorBackend:          "local",
		RAGVectorCollection:       "ai_sre_agent_knowledge",
		RAGVectorTimeout:          5 * time.Second,
		RAGRebuildPolicy:          "manual",
		Timeout:                   20 * time.Second,
		MaxRetries:                2,
		RetryBase:                 300 * time.Millisecond,
		RetryMax:                  4 * time.Second,
		MaxTokens:                 900,
		MaxQueryChars:             2048,
		RateLimitRPS:              2,
		RateBurst:                 4,
		MaxConcurrentQueries:      16,
		CircuitFailures:           5,
		CircuitCooldown:           30 * time.Second,
		AnalysisReuseEnabled:      true,
		AnalysisReuseWindow:       45 * time.Second,
		AnalysisReuseMaxKeys:      256,
		PlaybookFile:              "./configs/agent_playbooks.yaml",
		DryRun:                    true,
		AllowUnsafe:               false,
		Namespaces:                []string{"default"},
		ShellAllowed:              []string{"echo", "kubectl", "systemctl"},
		MaxActionsPerQuery:        8,
		PendingActionTTL:          30 * time.Minute,
		MaxPendingActions:         2048,
		RequireApprovalToken:      true,
		MaxTelemetryAge:           2 * time.Minute,
		AllowActionsOnStaleData:   false,
		SkipLLMOnStaleTelemetry:   true,
		SkipLLMOnNoTelemetry:      true,
		EventWebhookTimeout:       2 * time.Second,
		EventPublishRetries:       1,
		EventRetryBackoff:         200 * time.Millisecond,
		EventPagerDutyEventsURL:   "https://events.pagerduty.com/v2/enqueue",
		ActionTimeout:             15 * time.Second,
		MaxParallelActionExec:     4,
		ExplainabilityEvidenceMax: 8,
	}
}

// QueryRequest is the API payload for /api/v1/agent/query.
type QueryRequest struct {
	Query  string `json:"query"`
	Node   string `json:"node,omitempty"`
	DryRun *bool  `json:"dry_run,omitempty"`
}

// Explainability captures deterministic evidence and data coverage for trust and debugging.
type Explainability struct {
	TopSignals                []MetricKV         `json:"top_signals,omitempty"`
	TrendSignals              map[string]string  `json:"trend_signals,omitempty"`
	Evidence                  []string           `json:"evidence,omitempty"`
	Limitations               []string           `json:"limitations,omitempty"`
	DataCoverage              ExplainabilityData `json:"data_coverage"`
	UsedDeterministicFallback bool               `json:"used_deterministic_fallback"`
}

// ExplainabilityData summarizes what data was available for this decision.
type ExplainabilityData struct {
	Metrics                int      `json:"metrics"`
	Processes              int      `json:"processes"`
	Logs                   int      `json:"logs"`
	Findings               int      `json:"findings"`
	Anomalies              int      `json:"anomalies"`
	TelemetryAgeSeconds    float64  `json:"telemetry_age_seconds"`
	TelemetryStale         bool     `json:"telemetry_stale"`
	TelemetryState         string   `json:"telemetry_state,omitempty"`
	TelemetryCoveragePct   float64  `json:"telemetry_coverage_percent,omitempty"`
	TelemetryConfidence    float64  `json:"telemetry_confidence,omitempty"`
	SourceMode             string   `json:"source_mode,omitempty"`
	RuntimeMode            string   `json:"runtime_mode,omitempty"`
	IngestDelaySeconds     float64  `json:"ingest_delay_seconds,omitempty"`
	MissingCriticalSignals []string `json:"missing_critical_signals,omitempty"`
	BlindSpots             []string `json:"blind_spots,omitempty"`
}

// QueryResponse is the API response for /api/v1/agent/query.
type QueryResponse struct {
	QueryID              string          `json:"query_id"`
	Node                 string          `json:"node"`
	Summary              string          `json:"summary"`
	RootCause            string          `json:"root_cause"`
	Confidence           float64         `json:"confidence"`
	Findings             []string        `json:"findings"`
	Recommendations      []string        `json:"recommendations"`
	Actions              []ActionSpec    `json:"actions"`
	Provider             string          `json:"provider"`
	Model                string          `json:"model"`
	UsedFallback         bool            `json:"used_fallback"`
	FallbackReason       string          `json:"fallback_reason,omitempty"`
	GPUContext           bool            `json:"gpu_context"`
	GeneratedAt          time.Time       `json:"generated_at"`
	ActionsExpireAt      time.Time       `json:"actions_expire_at,omitempty"`
	ActionsSuppressed    bool            `json:"actions_suppressed"`
	SuppressionReason    string          `json:"suppression_reason,omitempty"`
	Explainability       Explainability  `json:"explainability"`
	TelemetryContext     LLMSchema       `json:"telemetry_context"`
	RetrievedDocs        []rag.SearchHit `json:"retrieved_docs,omitempty"`
	RetrievalSummary     string          `json:"retrieval_summary,omitempty"`
	RetrievalEvidenceIDs []string        `json:"retrieval_evidence_ids,omitempty"`
	RetrievalConfidence  float64         `json:"retrieval_confidence,omitempty"`
	RetrievalIntent      string          `json:"retrieval_intent,omitempty"`
	RetrievalMode        string          `json:"retrieval_mode,omitempty"`
}

// ExecuteRequest is the API payload for /api/v1/agent/execute.
type ExecuteRequest struct {
	ActionID      string `json:"action_id"`
	DryRun        *bool  `json:"dry_run,omitempty"`
	ApprovalToken string `json:"approval_token,omitempty"`
}

// ExecuteResponse is the API response for /api/v1/agent/execute.
type ExecuteResponse struct {
	QueryID    string       `json:"query_id,omitempty"`
	Action     ActionSpec   `json:"action"`
	Result     ActionResult `json:"result"`
	ExecutedAt time.Time    `json:"executed_at"`
}

// MetricsSnapshot is exported for controller Prometheus text rendering.
type MetricsSnapshot struct {
	QueriesTotal           uint64
	QueriesSuccessTotal    uint64
	QueriesFailureTotal    uint64
	RateLimitedTotal       uint64
	BusyRejectedTotal      uint64
	StaleTelemetryTotal    uint64
	LLMCallsTotal          uint64
	LLMFailuresTotal       uint64
	LLMBypassedStaleTotal  uint64
	LLMBypassedEmptyTotal  uint64
	AnalysisReusedTotal    uint64
	RAGSkippedContextTotal uint64
	RAGLowConfidenceTotal  uint64
	FallbackTotal          uint64
	ActionsSuppressedTotal uint64
	ActionsExecutedTotal   uint64
	ActionsFailureTotal    uint64
	EventsPublishedTotal   uint64
	EventsPublishFailTotal uint64
	ApprovalRequiredTotal  uint64
	ApprovalRejectedTotal  uint64
	PendingExpiredTotal    uint64
	PendingPrunedTotal     uint64
	GPUAnalysisCount       uint64
	GPUAnalysisSumSec      float64
}

// RuntimeStatus describes the current AGENT query-service operating mode.
type RuntimeStatus struct {
	Enabled                 bool            `json:"enabled"`
	AnalysisMode            string          `json:"analysis_mode"`
	Provider                string          `json:"provider"`
	Model                   string          `json:"model"`
	DryRun                  bool            `json:"dry_run"`
	RequireApprovalToken    bool            `json:"require_approval_token"`
	RAGAttached             bool            `json:"rag_attached"`
	SkipLLMOnStaleTelemetry bool            `json:"skip_llm_on_stale_telemetry"`
	SkipLLMOnNoTelemetry    bool            `json:"skip_llm_on_no_telemetry"`
	AnalysisReuseEnabled    bool            `json:"analysis_reuse_enabled"`
	AnalysisReuseWindow     string          `json:"analysis_reuse_window"`
	MaxTelemetryAge         string          `json:"max_telemetry_age"`
	Metrics                 MetricsSnapshot `json:"metrics"`
}

// QueryService orchestrates AGENT NL queries, LLM reasoning, and guarded actions.
type QueryService struct {
	cfg        Config
	logger     *zap.Logger
	store      *ingest.MemoryStore
	history    ingest.MetricHistoryProvider
	gpu        *gpuobs.Store
	rag        rag.KnowledgeBase
	client     llmClient
	runner     *PlaybookRunner
	limiter    *rate.Limiter
	eventHTTP  *http.Client
	querySlots chan struct{}

	mu                  sync.RWMutex
	pending             map[string]pendingAction
	recentAnalyses      map[string]recentAnalysis
	recentAnalysisOrder []string
	cb                  circuitBreaker

	queriesTotal           atomic.Uint64
	queriesSuccessTotal    atomic.Uint64
	queriesFailureTotal    atomic.Uint64
	rateLimitedTotal       atomic.Uint64
	busyRejectedTotal      atomic.Uint64
	staleTelemetryTotal    atomic.Uint64
	llmCallsTotal          atomic.Uint64
	llmFailuresTotal       atomic.Uint64
	llmBypassedStaleTotal  atomic.Uint64
	llmBypassedEmptyTotal  atomic.Uint64
	analysisReusedTotal    atomic.Uint64
	ragSkippedContextTotal atomic.Uint64
	ragLowConfidenceTotal  atomic.Uint64
	fallbackTotal          atomic.Uint64
	actionsSuppressedTotal atomic.Uint64
	actionsExecutedTotal   atomic.Uint64
	actionsFailureTotal    atomic.Uint64
	eventsPublishedTotal   atomic.Uint64
	eventsPublishFailTotal atomic.Uint64
	approvalRequiredTotal  atomic.Uint64
	approvalRejectedTotal  atomic.Uint64
	pendingExpiredTotal    atomic.Uint64
	pendingPrunedTotal     atomic.Uint64
	gpuAnalysisCount       atomic.Uint64
	gpuAnalysisNanos       atomic.Uint64
}

type pendingAction struct {
	QueryID          string
	Action           ActionSpec
	CreatedAt        time.Time
	ExpiresAt        time.Time
	ApprovalRequired bool
	ApprovalToken    string
}

type circuitBreaker struct {
	failures  int
	openUntil time.Time
}

type llmClient interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
	Provider() string
	Model() string
}

type llmPayload struct {
	Summary         string       `json:"summary"`
	RootCause       string       `json:"root_cause"`
	Confidence      float64      `json:"confidence"`
	Findings        []string     `json:"findings"`
	Recommendations []string     `json:"recommendations"`
	Actions         []ActionSpec `json:"actions"`
	Evidence        []string     `json:"evidence"`
	Limitations     []string     `json:"limitations"`
}

// NewQueryService creates a new AGENT service. It keeps core ingest isolated.
func NewQueryService(cfg Config, store *ingest.MemoryStore, gpu *gpuobs.Store, logger *zap.Logger) (*QueryService, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = normalizeConfig(cfg)

	runnerCfg := DefaultRunnerConfig()
	runnerCfg.PlaybookFile = cfg.PlaybookFile
	runnerCfg.DryRun = cfg.DryRun
	runnerCfg.AllowUnsafe = cfg.AllowUnsafe
	runnerCfg.AllowedNamespaces = cfg.Namespaces
	runnerCfg.AllowedShellCommands = cfg.ShellAllowed
	runnerCfg.MaxParallelActions = cfg.MaxParallelActionExec
	runnerCfg.ActionTimeout = cfg.ActionTimeout

	runner, err := NewPlaybookRunner(runnerCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("new playbook runner: %w", err)
	}

	var retriever rag.KnowledgeBase
	if cfg.RAG {
		ragCfg := rag.DefaultConfig()
		ragCfg.Enabled = true
		ragCfg.DatasetPath = strings.TrimSpace(cfg.RAGDatasetPath)
		ragCfg.SourcePaths = append([]string(nil), cfg.RAGDocs...)
		ragCfg.TopK = cfg.RAGTopK
		ragCfg.MaxSnippetChars = cfg.RAGChars
		ragCfg.ChunkSize = cfg.RAGChunkSize
		ragCfg.ChunkOverlap = cfg.RAGChunkOverlap
		ragCfg.ChunkStrategy = cfg.RAGChunkStrategy
		ragCfg.RetrievalMode = cfg.RAGRetrievalMode
		ragCfg.EmbeddingProvider = cfg.RAGEmbeddingProvider
		ragCfg.EmbeddingModel = cfg.RAGEmbeddingModel
		ragCfg.EmbeddingBaseURL = cfg.RAGEmbeddingBaseURL
		ragCfg.EmbeddingAPIKey = cfg.RAGEmbeddingAPIKey
		if strings.TrimSpace(cfg.RAGVectorBackend) != "" {
			ragCfg.VectorBackend = cfg.RAGVectorBackend
		}
		ragCfg.VectorEndpoint = strings.TrimSpace(cfg.RAGVectorEndpoint)
		if strings.TrimSpace(cfg.RAGVectorCollection) != "" {
			ragCfg.VectorCollection = cfg.RAGVectorCollection
		}
		ragCfg.VectorDatabase = strings.TrimSpace(cfg.RAGVectorDatabase)
		ragCfg.VectorToken = strings.TrimSpace(cfg.RAGVectorToken)
		if cfg.RAGVectorTimeout > 0 {
			ragCfg.VectorTimeout = cfg.RAGVectorTimeout
		}
		ragCfg.RebuildPolicy = cfg.RAGRebuildPolicy
		if strings.TrimSpace(cfg.RAGPath) != "" {
			ragCfg.IndexPath = strings.TrimSpace(cfg.RAGPath)
		}
		localRetriever, ragErr := rag.NewLocalRetriever(ragCfg, logger)
		if ragErr != nil {
			logger.Warn("failed to initialize local rag retriever; continuing without rag context", zap.Error(ragErr))
		} else {
			retriever = localRetriever
		}
	}

	client := newLLMClient(cfg, logger)
	return &QueryService{
		cfg:            cfg,
		logger:         logger.With(zap.String("component", "agent_query_service")),
		store:          store,
		history:        store,
		gpu:            gpu,
		rag:            retriever,
		client:         client,
		runner:         runner,
		limiter:        rate.NewLimiter(rate.Limit(cfg.RateLimitRPS), cfg.RateBurst),
		eventHTTP:      &http.Client{Timeout: cfg.EventWebhookTimeout},
		querySlots:     make(chan struct{}, cfg.MaxConcurrentQueries),
		pending:        make(map[string]pendingAction),
		recentAnalyses: make(map[string]recentAnalysis),
	}, nil
}

// SetRetriever overrides the configured knowledge base. Used by the controller to share one persistent index instance.
func (s *QueryService) SetRetriever(retriever rag.KnowledgeBase) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rag = retriever
}

// SetMetricHistoryProvider overrides trend-history reads while keeping node snapshots in the hot cache.
func (s *QueryService) SetMetricHistoryProvider(provider ingest.MetricHistoryProvider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = provider
}

// ReloadPlaybooks refreshes the guarded playbook set without rebuilding the full query service.
func (s *QueryService) ReloadPlaybooks(path string) error {
	if s == nil || s.runner == nil {
		return nil
	}
	cfg := s.runner.cfg
	cfg.PlaybookFile = strings.TrimSpace(path)
	return s.runner.Reload(cfg)
}

// Query analyzes telemetry using LLM plus deterministic playbook suggestions.
func (s *QueryService) Query(ctx context.Context, req QueryRequest) (QueryResponse, error) {
	if !s.limiter.Allow() {
		s.rateLimitedTotal.Add(1)
		return QueryResponse{}, ErrRateLimited
	}
	if !s.acquireQuerySlot() {
		s.busyRejectedTotal.Add(1)
		return QueryResponse{}, ErrBusy
	}
	defer s.releaseQuerySlot()

	queryID := newQueryID()
	cleanReq := normalizeQueryRequest(req, s.cfg.MaxQueryChars)
	s.queriesTotal.Add(1)

	promptIn, gpuEnabled := s.buildPromptInput(cleanReq)
	if promptIn.Query == "" {
		s.queriesFailureTotal.Add(1)
		return QueryResponse{}, fmt.Errorf("query is required")
	}
	promptIn.TelemetryStale = s.isTelemetryStale(promptIn)
	if promptIn.TelemetryStale {
		promptIn.TelemetryQuality.State = "stale"
		promptIn.TelemetryQuality.SafeToAct = false
	}
	noTelemetry := s.isTelemetryInsufficient(promptIn)
	if noTelemetry {
		promptIn.Findings = dedupeStrings(append(promptIn.Findings, "Telemetry snapshot is insufficient (no metrics/processes/logs)"))
		promptIn.TelemetryQuality.State = "unavailable"
		promptIn.TelemetryQuality.SafeToAct = false
	}
	if promptIn.TelemetryStale {
		s.staleTelemetryTotal.Add(1)
		promptIn.Findings = dedupeStrings(append(promptIn.Findings, fmt.Sprintf(
			"Telemetry snapshot is stale (age %.0fs > threshold %.0fs)",
			promptIn.TelemetryAgeSeconds,
			s.cfg.MaxTelemetryAge.Seconds(),
		)))
	}

	var llmOut llmPayload
	usedFallback := false
	fallbackReason := ""
	allowAnalysisReuse := s.shouldReuseAnalysis(promptIn, noTelemetry)
	analysisKey := ""
	if allowAnalysisReuse {
		analysisKey = s.analysisReuseKey(cleanReq, promptIn)
		if cached, ok := s.loadRecentAnalysis(analysisKey, time.Now().UTC()); ok {
			applyRecentAnalysis(&promptIn, cached)
			llmOut = cached.Payload
			s.analysisReusedTotal.Add(1)
			goto analysisComplete
		}
	}
	switch {
	case noTelemetry && s.cfg.SkipLLMOnNoTelemetry:
		s.llmBypassedEmptyTotal.Add(1)
		llmOut = fallbackPayload(promptIn, ErrNoTelemetry)
		usedFallback = true
		fallbackReason = ErrNoTelemetry.Error()
	case promptIn.TelemetryStale && s.cfg.SkipLLMOnStaleTelemetry:
		s.llmBypassedStaleTotal.Add(1)
		llmOut = fallbackPayload(promptIn, ErrStaleTelemetry)
		usedFallback = true
		fallbackReason = ErrStaleTelemetry.Error()
	default:
		s.attachRAGContext(&promptIn)
		llmOut, usedFallback, fallbackReason = s.runLLM(ctx, promptIn)
	}
	if allowAnalysisReuse && analysisKey != "" && !usedFallback {
		s.storeRecentAnalysis(analysisKey, recentAnalysisFromPrompt(promptIn, llmOut))
	}
analysisComplete:
	if usedFallback {
		s.fallbackTotal.Add(1)
	}
	proposed := s.runner.ProposeFromMetrics(promptIn.NodeName, promptIn.Metrics)
	actions := mergeActions(llmOut.Actions, proposed, queryID)
	if s.cfg.MaxActionsPerQuery > 0 && len(actions) > s.cfg.MaxActionsPerQuery {
		actions = actions[:s.cfg.MaxActionsPerQuery]
	}
	recommendations := sanitizeStrings(llmOut.Recommendations)
	actionsExpireAt := time.Time{}
	actionsSuppressed := false
	suppressionReason := ""
	if promptIn.TelemetryStale && !s.cfg.AllowActionsOnStaleData {
		actions = nil
		actionsSuppressed = true
		suppressionReason = "telemetry stale"
		s.actionsSuppressedTotal.Add(1)
		recommendations = dedupeStrings(append(recommendations, "Telemetry is stale; refresh data before executing remediation actions"))
	} else {
		actions, actionsExpireAt = s.cachePending(queryID, actions)
	}
	explainability := buildExplainability(promptIn, llmOut, usedFallback, gpuEnabled, s.cfg.ExplainabilityEvidenceMax)

	response := QueryResponse{
		QueryID:              queryID,
		Node:                 promptIn.NodeName,
		Summary:              llmOut.Summary,
		RootCause:            llmOut.RootCause,
		Confidence:           clampConfidence(llmOut.Confidence),
		Findings:             sanitizeStrings(nonEmpty(llmOut.Findings, promptIn.Findings)),
		Recommendations:      recommendations,
		Actions:              actions,
		Provider:             s.client.Provider(),
		Model:                s.client.Model(),
		UsedFallback:         usedFallback,
		FallbackReason:       fallbackReason,
		GPUContext:           gpuEnabled,
		GeneratedAt:          time.Now().UTC(),
		ActionsExpireAt:      actionsExpireAt,
		ActionsSuppressed:    actionsSuppressed,
		SuppressionReason:    suppressionReason,
		Explainability:       explainability,
		TelemetryContext:     BuildSchema(promptIn),
		RetrievedDocs:        append([]rag.SearchHit(nil), promptIn.RetrievedDocs...),
		RetrievalSummary:     promptIn.RetrievalSummary,
		RetrievalEvidenceIDs: append([]string(nil), promptIn.RetrievalEvidenceIDs...),
		RetrievalConfidence:  promptIn.RetrievalConfidence,
		RetrievalIntent:      promptIn.RetrievalIntent,
		RetrievalMode:        promptIn.RetrievalMode,
	}

	s.queriesSuccessTotal.Add(1)
	s.emitQueryEvent(response)
	return response, nil
}

// Execute runs a previously proposed action by ID.
func (s *QueryService) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResponse, error) {
	actionID := strings.TrimSpace(req.ActionID)
	if actionID == "" {
		return ExecuteResponse{}, fmt.Errorf("action_id is required")
	}

	entry, ok, loadErr := s.loadPending(actionID)
	if loadErr != nil {
		return ExecuteResponse{}, loadErr
	}
	if !ok {
		return ExecuteResponse{}, ErrActionNotFound
	}

	forceDryRun := req.DryRun != nil && *req.DryRun
	effectiveDryRun := forceDryRun || s.cfg.DryRun
	approved, err := s.validateApprovalToken(req.ApprovalToken, entry, effectiveDryRun)
	if err != nil {
		s.approvalRejectedTotal.Add(1)
		return ExecuteResponse{}, err
	}

	action := entry.Action
	if approved || effectiveDryRun {
		action.RequiresApproval = false
	}

	results := s.runner.Execute(ctx, []ActionSpec{action}, forceDryRun)
	if len(results) == 0 {
		return ExecuteResponse{}, fmt.Errorf("no action result")
	}
	result := results[0]
	s.deletePending(actionID)

	if result.Status == ActionResultFailed {
		s.actionsFailureTotal.Add(1)
	} else if result.Status == ActionResultExecuted || result.Status == ActionResultDryRun {
		s.actionsExecutedTotal.Add(1)
	}

	response := ExecuteResponse{
		QueryID:    entry.QueryID,
		Action:     action,
		Result:     result,
		ExecutedAt: time.Now().UTC(),
	}
	s.emitExecuteEvent(response)
	return response, nil
}

// Metrics returns counters for controller /metrics export.
func (s *QueryService) Metrics() MetricsSnapshot {
	count := s.gpuAnalysisCount.Load()
	sumNanos := s.gpuAnalysisNanos.Load()
	return MetricsSnapshot{
		QueriesTotal:           s.queriesTotal.Load(),
		QueriesSuccessTotal:    s.queriesSuccessTotal.Load(),
		QueriesFailureTotal:    s.queriesFailureTotal.Load(),
		RateLimitedTotal:       s.rateLimitedTotal.Load(),
		BusyRejectedTotal:      s.busyRejectedTotal.Load(),
		StaleTelemetryTotal:    s.staleTelemetryTotal.Load(),
		LLMCallsTotal:          s.llmCallsTotal.Load(),
		LLMFailuresTotal:       s.llmFailuresTotal.Load(),
		LLMBypassedStaleTotal:  s.llmBypassedStaleTotal.Load(),
		LLMBypassedEmptyTotal:  s.llmBypassedEmptyTotal.Load(),
		AnalysisReusedTotal:    s.analysisReusedTotal.Load(),
		RAGSkippedContextTotal: s.ragSkippedContextTotal.Load(),
		RAGLowConfidenceTotal:  s.ragLowConfidenceTotal.Load(),
		FallbackTotal:          s.fallbackTotal.Load(),
		ActionsSuppressedTotal: s.actionsSuppressedTotal.Load(),
		ActionsExecutedTotal:   s.actionsExecutedTotal.Load(),
		ActionsFailureTotal:    s.actionsFailureTotal.Load(),
		EventsPublishedTotal:   s.eventsPublishedTotal.Load(),
		EventsPublishFailTotal: s.eventsPublishFailTotal.Load(),
		ApprovalRequiredTotal:  s.approvalRequiredTotal.Load(),
		ApprovalRejectedTotal:  s.approvalRejectedTotal.Load(),
		PendingExpiredTotal:    s.pendingExpiredTotal.Load(),
		PendingPrunedTotal:     s.pendingPrunedTotal.Load(),
		GPUAnalysisCount:       count,
		GPUAnalysisSumSec:      float64(sumNanos) / float64(time.Second),
	}
}

func (s *QueryService) Status() RuntimeStatus {
	if s == nil {
		return RuntimeStatus{Enabled: false, AnalysisMode: "unavailable"}
	}

	s.mu.RLock()
	ragAttached := s.rag != nil
	s.mu.RUnlock()

	mode := "llm_assisted"
	provider := s.client.Provider()
	if provider == "mock" || provider == "stub" || provider == "local_stub" {
		mode = "deterministic_only"
	}

	return RuntimeStatus{
		Enabled:                 true,
		AnalysisMode:            mode,
		Provider:                provider,
		Model:                   s.client.Model(),
		DryRun:                  s.cfg.DryRun,
		RequireApprovalToken:    s.cfg.RequireApprovalToken,
		RAGAttached:             ragAttached,
		SkipLLMOnStaleTelemetry: s.cfg.SkipLLMOnStaleTelemetry,
		SkipLLMOnNoTelemetry:    s.cfg.SkipLLMOnNoTelemetry,
		AnalysisReuseEnabled:    s.cfg.AnalysisReuseEnabled,
		AnalysisReuseWindow:     s.cfg.AnalysisReuseWindow.String(),
		MaxTelemetryAge:         s.cfg.MaxTelemetryAge.String(),
		Metrics:                 s.Metrics(),
	}
}

func (s *QueryService) emitQueryEvent(resp QueryResponse) {
	payload := map[string]any{
		"query_id":               resp.QueryID,
		"node":                   resp.Node,
		"provider":               resp.Provider,
		"model":                  resp.Model,
		"used_fallback":          resp.UsedFallback,
		"fallback_reason":        resp.FallbackReason,
		"actions_suppressed":     resp.ActionsSuppressed,
		"suppression_reason":     resp.SuppressionReason,
		"actions_count":          len(resp.Actions),
		"actions_expire_at":      resp.ActionsExpireAt,
		"telemetry_stale":        resp.Explainability.DataCoverage.TelemetryStale,
		"telemetry_age_sec":      resp.Explainability.DataCoverage.TelemetryAgeSeconds,
		"generated_at":           resp.GeneratedAt,
		"deterministic_fallback": resp.Explainability.UsedDeterministicFallback,
	}
	s.emitEvent("agent.query.completed", payload)
}

func (s *QueryService) emitExecuteEvent(resp ExecuteResponse) {
	payload := map[string]any{
		"query_id":       resp.QueryID,
		"action_id":      resp.Action.ID,
		"action_type":    resp.Action.Type,
		"action_safe":    resp.Action.Safe,
		"result_status":  resp.Result.Status,
		"result_dry_run": resp.Result.DryRun,
		"executed_at":    resp.ExecutedAt,
	}
	s.emitEvent("agent.execute.completed", payload)
}

type eventPublishTarget struct {
	Name         string
	URL          string
	Payload      []byte
	Headers      map[string]string
	ExpectStatus int
}

func (s *QueryService) emitEvent(eventType string, payload map[string]any) {
	envelope := map[string]any{
		"type":      eventType,
		"timestamp": time.Now().UTC(),
		"payload":   payload,
	}
	targets, err := s.buildEventTargets(eventType, payload, envelope)
	if err != nil {
		s.eventsPublishFailTotal.Add(1)
		s.logger.Warn("agent event target build failed", zap.String("type", eventType), zap.Error(err))
		return
	}
	if len(targets) == 0 {
		return
	}

	go func(publishTargets []eventPublishTarget) {
		for _, target := range publishTargets {
			if target.URL == "" {
				continue
			}
			if err := s.publishEventTarget(target); err != nil {
				s.eventsPublishFailTotal.Add(1)
				s.logger.Warn("agent event publish failed",
					zap.String("type", eventType),
					zap.String("target", target.Name),
					zap.String("url", target.URL),
					zap.Error(err))
				continue
			}
			s.eventsPublishedTotal.Add(1)
		}
	}(targets)
}

func (s *QueryService) buildEventTargets(eventType string, payload, envelope map[string]any) ([]eventPublishTarget, error) {
	targets := make([]eventPublishTarget, 0, 3)

	if webhookURL := strings.TrimSpace(s.cfg.EventWebhookURL); webhookURL != "" {
		raw, err := json.Marshal(envelope)
		if err != nil {
			return nil, err
		}
		headers := map[string]string{}
		if token := strings.TrimSpace(s.cfg.EventWebhookToken); token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		targets = append(targets, eventPublishTarget{
			Name:    "webhook",
			URL:     webhookURL,
			Payload: raw,
			Headers: headers,
		})
	}

	if slackURL := strings.TrimSpace(s.cfg.EventSlackWebhookURL); slackURL != "" {
		slackPayload := map[string]any{
			"text": s.formatSlackEventText(eventType, payload),
		}
		raw, err := json.Marshal(slackPayload)
		if err != nil {
			return nil, err
		}
		targets = append(targets, eventPublishTarget{
			Name:         "slack",
			URL:          slackURL,
			Payload:      raw,
			ExpectStatus: http.StatusOK,
		})
	}

	if routingKey := strings.TrimSpace(s.cfg.EventPagerDutyRoutingKey); routingKey != "" {
		pdURL := strings.TrimSpace(s.cfg.EventPagerDutyEventsURL)
		if pdURL == "" {
			pdURL = "https://events.pagerduty.com/v2/enqueue"
		}
		source := firstNonEmpty(stringFromAny(payload["node"]), "ai-sre-agent")
		summary := s.formatPagerDutySummary(eventType, payload)
		pdPayload := map[string]any{
			"routing_key":  routingKey,
			"event_action": "trigger",
			"payload": map[string]any{
				"summary":        summary,
				"severity":       s.pagerDutySeverity(eventType, payload),
				"source":         source,
				"custom_details": envelope,
			},
			"dedup_key": s.eventDedupKey(eventType, payload),
		}
		raw, err := json.Marshal(pdPayload)
		if err != nil {
			return nil, err
		}
		targets = append(targets, eventPublishTarget{
			Name:         "pagerduty",
			URL:          pdURL,
			Payload:      raw,
			ExpectStatus: http.StatusAccepted,
		})
	}

	return targets, nil
}

func (s *QueryService) publishEventTarget(target eventPublishTarget) error {
	timeout := s.cfg.EventWebhookTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	attempts := s.cfg.EventPublishRetries + 1
	if attempts <= 0 {
		attempts = 1
	}
	backoff := s.cfg.EventRetryBackoff
	if backoff <= 0 {
		backoff = 200 * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := s.postEventTarget(ctx, target)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == attempts-1 {
			break
		}
		wait := backoff << attempt
		if wait > 5*time.Second {
			wait = 5 * time.Second
		}
		timer := time.NewTimer(wait)
		<-timer.C
	}
	return lastErr
}

func (s *QueryService) postEventTarget(ctx context.Context, target eventPublishTarget) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(target.Payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ai-sre-agent/event-publisher")
	for key, value := range target.Headers {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		req.Header.Set(trimmedKey, value)
	}

	resp, err := s.eventHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if target.ExpectStatus > 0 {
		if resp.StatusCode != target.ExpectStatus {
			return fmt.Errorf("status %d (expected %d)", resp.StatusCode, target.ExpectStatus)
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (s *QueryService) formatSlackEventText(eventType string, payload map[string]any) string {
	switch eventType {
	case "agent.query.completed":
		return fmt.Sprintf(
			"AGENT query completed: query_id=%s node=%s fallback=%t suppressed=%t actions=%s",
			firstNonEmpty(stringFromAny(payload["query_id"]), "n/a"),
			firstNonEmpty(stringFromAny(payload["node"]), "n/a"),
			boolFromAny(payload["used_fallback"]),
			boolFromAny(payload["actions_suppressed"]),
			firstNonEmpty(stringFromAny(payload["actions_count"]), "0"),
		)
	case "agent.execute.completed":
		return fmt.Sprintf(
			"AGENT execute completed: query_id=%s action_id=%s type=%s status=%s dry_run=%t",
			firstNonEmpty(stringFromAny(payload["query_id"]), "n/a"),
			firstNonEmpty(stringFromAny(payload["action_id"]), "n/a"),
			firstNonEmpty(stringFromAny(payload["action_type"]), "n/a"),
			firstNonEmpty(stringFromAny(payload["result_status"]), "unknown"),
			boolFromAny(payload["result_dry_run"]),
		)
	default:
		return fmt.Sprintf("AGENT event: type=%s", eventType)
	}
}

func (s *QueryService) formatPagerDutySummary(eventType string, payload map[string]any) string {
	switch eventType {
	case "agent.query.completed":
		return fmt.Sprintf(
			"AGENT query completed on %s (fallback=%t suppressed=%t)",
			firstNonEmpty(stringFromAny(payload["node"]), "unknown-node"),
			boolFromAny(payload["used_fallback"]),
			boolFromAny(payload["actions_suppressed"]),
		)
	case "agent.execute.completed":
		return fmt.Sprintf(
			"AGENT execute completed: %s status=%s",
			firstNonEmpty(stringFromAny(payload["action_id"]), "unknown-action"),
			firstNonEmpty(stringFromAny(payload["result_status"]), "unknown"),
		)
	default:
		return "AGENT event"
	}
}

func (s *QueryService) pagerDutySeverity(eventType string, payload map[string]any) string {
	switch eventType {
	case "agent.execute.completed":
		if strings.EqualFold(stringFromAny(payload["result_status"]), string(ActionResultFailed)) {
			return "critical"
		}
		return "info"
	case "agent.query.completed":
		if boolFromAny(payload["used_fallback"]) || boolFromAny(payload["actions_suppressed"]) || boolFromAny(payload["telemetry_stale"]) {
			return "warning"
		}
		return "info"
	default:
		return "info"
	}
}

func (s *QueryService) eventDedupKey(eventType string, payload map[string]any) string {
	base := firstNonEmpty(stringFromAny(payload["action_id"]), stringFromAny(payload["query_id"]), strconv.FormatInt(time.Now().UnixNano(), 10))
	return fmt.Sprintf("%s:%s", eventType, base)
}

func boolFromAny(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		normalized := strings.TrimSpace(strings.ToLower(v))
		return normalized == "true" || normalized == "1" || normalized == "yes" || normalized == "on"
	default:
		return false
	}
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case int:
		return strconv.Itoa(v)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func (s *QueryService) acquireQuerySlot() bool {
	select {
	case s.querySlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *QueryService) releaseQuerySlot() {
	select {
	case <-s.querySlots:
	default:
	}
}

func (s *QueryService) isTelemetryStale(in PromptInput) bool {
	if s.cfg.MaxTelemetryAge <= 0 {
		return false
	}
	if in.TelemetryAgeSeconds <= 0 {
		return false
	}
	return in.TelemetryAgeSeconds > s.cfg.MaxTelemetryAge.Seconds()
}

func (s *QueryService) isTelemetryInsufficient(in PromptInput) bool {
	return len(in.Metrics) == 0 && len(in.Processes) == 0 && len(in.Logs) == 0
}

func (s *QueryService) buildPromptInput(req QueryRequest) (PromptInput, bool) {
	now := time.Now().UTC()
	node := s.pickNode(req.Node)
	metrics := map[string]float64{}
	nodeName := ""
	processes := []PromptProcess{}
	logs := []PromptLog{}
	contextBits := []string{"Push-first in-memory snapshots from ingest store"}
	findings := []string{}
	anomalies := []string{}

	if node != nil {
		metrics = cloneMetricMap(node.Metrics)
		nodeName = firstNonEmpty(node.CollectorID, node.Hostname)
		processes = summarizeProcesses(node.Processes, 5)
		logs = summarizeLogs(node.Logs, 5)
	}

	gpuEnabled := false
	gpuStart := time.Now()
	gpuNode := s.pickGPUNode(req.Node, node)
	if gpuNode != nil {
		gpuEnabled = true
		mergeGPUContext(metrics, gpuNode)
		findings = append(findings, gpuFindings(gpuNode)...)
		contextBits = append(contextBits, fmt.Sprintf("GPU snapshots from %s", gpuNode.LastSeen.UTC().Format(time.RFC3339)))
	}
	if gpuEnabled {
		s.gpuAnalysisCount.Add(1)
		s.gpuAnalysisNanos.Add(safeconv.NonNegativeInt64ToUint64(int64(time.Since(gpuStart))))
	}

	trends := metricTrends(s.history, nodeName)
	telemetryQuality := assessPromptTelemetryQuality(node, metrics, processes, logs, now, s.cfg.MaxTelemetryAge)
	findings = append(findings, systemFindings(metrics)...)
	findings = append(findings, operationalFindings(metrics, trends, logs)...)
	findings = append(findings, telemetryQualityFindings(telemetryQuality)...)
	anomalies = append(anomalies, trendHints(s.history, nodeName)...)
	if telemetryQuality.State != "" {
		contextBits = append(contextBits, fmt.Sprintf("Telemetry quality=%s coverage=%.0f%% source=%s", telemetryQuality.State, telemetryQuality.CoveragePercent, firstNonEmpty(telemetryQuality.SourceMode, "unknown")))
	}
	if len(findings) == 0 {
		findings = append(findings, "No critical anomalies detected")
	}
	findings = dedupeStrings(findings)
	return PromptInput{
		Query:               req.Query,
		NodeName:            nodeName,
		Generated:           now,
		TelemetryAgeSeconds: telemetryQuality.FreshnessAgeSeconds,
		TelemetryQuality:    telemetryQuality,
		Metrics:             metrics,
		Trends:              trends,
		Findings:            findings,
		Anomalies:           dedupeStrings(anomalies),
		Processes:           processes,
		Logs:                logs,
		ContextTag:          strings.Join(contextBits, "; "),
	}, gpuEnabled
}

func (s *QueryService) attachRAGContext(in *PromptInput) {
	if s == nil || in == nil {
		return
	}
	if len(in.RAGSnippets) > 0 || len(in.RetrievedDocs) > 0 {
		return
	}
	ragSnippets, ragResult := s.ragContext(in.Query, in.Findings, in.Anomalies)
	in.RAGSnippets = ragSnippets
	in.RetrievedDocs = append([]rag.SearchHit(nil), ragResult.Hits...)
	in.RetrievalSummary = ragResult.Summary
	in.RetrievalEvidenceIDs = append([]string(nil), ragResult.RetrievalEvidenceIDs...)
	in.RetrievalConfidence = ragResult.Confidence
	in.RetrievalIntent = ragResult.Intent
	in.RetrievalMode = ragResult.RetrievalMode
}

func (s *QueryService) runLLM(ctx context.Context, in PromptInput) (llmPayload, bool, string) {
	systemPrompt := BuildSystemPrompt()
	userPrompt := BuildUserPrompt(in)

	payload, err := s.callLLMWithRetry(ctx, systemPrompt, userPrompt)
	if err == nil {
		return payload, false, ""
	}

	s.logger.Warn("llm call failed, using deterministic fallback", zap.Error(err))
	return fallbackPayload(in, err), true, err.Error()
}

func (s *QueryService) callLLMWithRetry(ctx context.Context, systemPrompt, userPrompt string) (llmPayload, error) {
	if s.isCircuitOpen() {
		return llmPayload{}, ErrCircuitOpen
	}

	var lastErr error
	attempts := s.cfg.MaxRetries + 1
	for attempt := 0; attempt < attempts; attempt++ {
		s.llmCallsTotal.Add(1)
		out, err := s.callOnce(ctx, systemPrompt, userPrompt)
		if err == nil {
			s.recordLLMSuccess()
			return out, nil
		}

		s.llmFailuresTotal.Add(1)
		s.recordLLMFailure(err)
		lastErr = err
		if !shouldRetry(err) || attempt == attempts-1 {
			break
		}
		if waitErr := waitWithBackoff(ctx, attempt, s.cfg.RetryBase, s.cfg.RetryMax); waitErr != nil {
			return llmPayload{}, waitErr
		}
	}
	return llmPayload{}, lastErr
}

func (s *QueryService) callOnce(ctx context.Context, systemPrompt, userPrompt string) (llmPayload, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	raw, err := s.client.Complete(timeoutCtx, systemPrompt, userPrompt)
	if err != nil {
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return llmPayload{}, ErrLLMTimeout
		}
		return llmPayload{}, err
	}

	payload, err := parseLLMPayload(raw)
	if err != nil {
		return llmPayload{}, err
	}
	return payload, nil
}

func (s *QueryService) isCircuitOpen() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Now().Before(s.cb.openUntil)
}

func (s *QueryService) recordLLMSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cb.failures = 0
	s.cb.openUntil = time.Time{}
}

func (s *QueryService) recordLLMFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cb.failures++
	if s.cb.failures >= s.cfg.CircuitFailures {
		s.cb.failures = 0
		s.cb.openUntil = time.Now().Add(s.cfg.CircuitCooldown)
		s.logger.Warn("llm circuit opened", zap.Duration("cooldown", s.cfg.CircuitCooldown), zap.Error(err))
	}
}

func (s *QueryService) cachePending(queryID string, actions []ActionSpec) ([]ActionSpec, time.Time) {
	if len(actions) == 0 {
		return nil, time.Time{}
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.cfg.PendingActionTTL)
	if s.cfg.PendingActionTTL <= 0 {
		expiresAt = now.Add(30 * time.Minute)
	}

	out := make([]ActionSpec, 0, len(actions))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.prunePendingLocked(now)

	for _, raw := range actions {
		action := normalizeAction(raw)
		approvalRequired := s.actionApprovalRequired(action)
		token := ""
		if approvalRequired {
			token = newApprovalToken()
			s.approvalRequiredTotal.Add(1)
		}

		action.ApprovalRequired = approvalRequired
		action.ApprovalToken = token
		action.ExpiresAt = expiresAt

		s.pending[action.ID] = pendingAction{
			QueryID:          queryID,
			Action:           action,
			CreatedAt:        now,
			ExpiresAt:        expiresAt,
			ApprovalRequired: approvalRequired,
			ApprovalToken:    token,
		}
		out = append(out, action)
	}

	if pruned := s.prunePendingOverflowLocked(); pruned > 0 {
		s.pendingPrunedTotal.Add(uint64(pruned))
	}
	return out, expiresAt
}

func (s *QueryService) loadPending(actionID string) (pendingAction, bool, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.pending[actionID]
	if ok && now.After(entry.ExpiresAt) {
		delete(s.pending, actionID)
		s.pendingExpiredTotal.Add(1)
		s.prunePendingLocked(now)
		return pendingAction{}, false, ErrActionExpired
	}
	s.prunePendingLocked(now)
	if !ok {
		return pendingAction{}, false, nil
	}
	return entry, true, nil
}

func (s *QueryService) deletePending(actionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, actionID)
}

func (s *QueryService) validateApprovalToken(token string, entry pendingAction, effectiveDryRun bool) (bool, error) {
	if effectiveDryRun {
		return false, nil
	}
	if !entry.ApprovalRequired {
		return false, nil
	}
	if strings.TrimSpace(token) == "" {
		return false, ErrApprovalRequired
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(entry.ApprovalToken)) != 1 {
		return false, ErrApprovalInvalid
	}
	return true, nil
}

func (s *QueryService) actionApprovalRequired(action ActionSpec) bool {
	if action.RequiresApproval {
		return true
	}
	if !action.Safe {
		return true
	}
	return s.cfg.RequireApprovalToken && !s.cfg.DryRun
}

func (s *QueryService) prunePendingLocked(now time.Time) {
	for key, entry := range s.pending {
		if now.After(entry.ExpiresAt) {
			delete(s.pending, key)
			s.pendingExpiredTotal.Add(1)
		}
	}
}

func (s *QueryService) prunePendingOverflowLocked() int {
	max := s.cfg.MaxPendingActions
	if max <= 0 || len(s.pending) <= max {
		return 0
	}
	type slot struct {
		id        string
		createdAt time.Time
	}
	items := make([]slot, 0, len(s.pending))
	for id, entry := range s.pending {
		items = append(items, slot{id: id, createdAt: entry.CreatedAt})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].createdAt.Before(items[j].createdAt)
	})
	toDrop := len(items) - max
	for i := 0; i < toDrop; i++ {
		delete(s.pending, items[i].id)
	}
	return toDrop
}

func (s *QueryService) pickNode(selector string) *ingest.NodeSnapshot {
	if s.store == nil {
		return nil
	}
	needle := strings.TrimSpace(selector)
	if needle != "" {
		if node := s.store.Node(needle); node != nil {
			return node
		}
		for _, candidate := range s.store.Snapshot() {
			if candidate != nil && strings.EqualFold(candidate.Hostname, needle) {
				return candidate
			}
		}
	}
	nodes := s.store.Snapshot()
	if len(nodes) == 0 {
		return nil
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].UpdatedAt.After(nodes[j].UpdatedAt)
	})
	return nodes[0]
}

func (s *QueryService) pickGPUNode(selector string, node *ingest.NodeSnapshot) *gpuobs.Node {
	if s.gpu == nil {
		return nil
	}
	candidates := s.gpu.Snapshot()
	if len(candidates) == 0 {
		return nil
	}

	needle := strings.TrimSpace(selector)
	if needle == "" && node != nil {
		needle = firstNonEmpty(node.CollectorID, node.Hostname)
	}

	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if needle == "" {
			continue
		}
		if strings.EqualFold(candidate.CollectorID, needle) || strings.EqualFold(candidate.Hostname, needle) {
			return candidate
		}
	}
	return candidates[0]
}

func normalizeConfig(cfg Config) Config {
	def := DefaultConfig()
	if cfg.Provider == "" {
		cfg.Provider = def.Provider
	}
	if cfg.Model == "" {
		cfg.Model = def.Model
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = def.BaseURL
	}
	if strings.TrimSpace(cfg.RAGPath) == "" {
		cfg.RAGPath = def.RAGPath
	}
	if cfg.RAGTopK <= 0 {
		cfg.RAGTopK = def.RAGTopK
	}
	if cfg.RAGChars <= 0 {
		cfg.RAGChars = def.RAGChars
	}
	if cfg.RAGMaxQueryChars <= 0 {
		cfg.RAGMaxQueryChars = def.RAGMaxQueryChars
	}
	if cfg.RAGMaxFindings <= 0 {
		cfg.RAGMaxFindings = def.RAGMaxFindings
	}
	if cfg.RAGMinConfidence < 0 || cfg.RAGMinConfidence > 1 {
		cfg.RAGMinConfidence = def.RAGMinConfidence
	}
	if strings.TrimSpace(cfg.RAGDatasetPath) == "" {
		cfg.RAGDatasetPath = def.RAGDatasetPath
	}
	if cfg.RAGChunkSize <= 0 {
		cfg.RAGChunkSize = def.RAGChunkSize
	}
	if cfg.RAGChunkOverlap < 0 {
		cfg.RAGChunkOverlap = def.RAGChunkOverlap
	}
	if cfg.RAGChunkOverlap >= cfg.RAGChunkSize {
		cfg.RAGChunkOverlap = def.RAGChunkOverlap
	}
	if strings.TrimSpace(cfg.RAGChunkStrategy) == "" {
		cfg.RAGChunkStrategy = def.RAGChunkStrategy
	}
	if strings.TrimSpace(cfg.RAGRetrievalMode) == "" {
		cfg.RAGRetrievalMode = def.RAGRetrievalMode
	}
	if strings.TrimSpace(cfg.RAGEmbeddingProvider) == "" {
		cfg.RAGEmbeddingProvider = def.RAGEmbeddingProvider
	}
	if strings.TrimSpace(cfg.RAGEmbeddingModel) == "" {
		cfg.RAGEmbeddingModel = def.RAGEmbeddingModel
	}
	if strings.TrimSpace(cfg.RAGVectorBackend) == "" {
		cfg.RAGVectorBackend = def.RAGVectorBackend
	}
	if strings.TrimSpace(cfg.RAGVectorCollection) == "" {
		cfg.RAGVectorCollection = def.RAGVectorCollection
	}
	if cfg.RAGVectorTimeout <= 0 {
		cfg.RAGVectorTimeout = def.RAGVectorTimeout
	}
	if strings.TrimSpace(cfg.RAGRebuildPolicy) == "" {
		cfg.RAGRebuildPolicy = def.RAGRebuildPolicy
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = def.Timeout
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = def.MaxRetries
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = def.RetryBase
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = def.RetryMax
	}
	if cfg.MaxQueryChars <= 0 {
		cfg.MaxQueryChars = def.MaxQueryChars
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = def.MaxTokens
	}
	if cfg.RateLimitRPS <= 0 {
		cfg.RateLimitRPS = def.RateLimitRPS
	}
	if cfg.RateBurst <= 0 {
		cfg.RateBurst = def.RateBurst
	}
	if cfg.MaxConcurrentQueries <= 0 {
		cfg.MaxConcurrentQueries = def.MaxConcurrentQueries
	}
	if cfg.CircuitFailures <= 0 {
		cfg.CircuitFailures = def.CircuitFailures
	}
	if cfg.CircuitCooldown <= 0 {
		cfg.CircuitCooldown = def.CircuitCooldown
	}
	if cfg.AnalysisReuseWindow <= 0 {
		cfg.AnalysisReuseWindow = def.AnalysisReuseWindow
	}
	if cfg.AnalysisReuseMaxKeys <= 0 {
		cfg.AnalysisReuseMaxKeys = def.AnalysisReuseMaxKeys
	}
	if cfg.PlaybookFile == "" {
		cfg.PlaybookFile = def.PlaybookFile
	}
	if len(cfg.Namespaces) == 0 {
		cfg.Namespaces = def.Namespaces
	}
	if len(cfg.ShellAllowed) == 0 {
		cfg.ShellAllowed = def.ShellAllowed
	}
	if cfg.MaxActionsPerQuery <= 0 {
		cfg.MaxActionsPerQuery = def.MaxActionsPerQuery
	}
	if cfg.PendingActionTTL <= 0 {
		cfg.PendingActionTTL = def.PendingActionTTL
	}
	if cfg.MaxPendingActions <= 0 {
		cfg.MaxPendingActions = def.MaxPendingActions
	}
	if cfg.MaxTelemetryAge <= 0 {
		cfg.MaxTelemetryAge = def.MaxTelemetryAge
	}
	if cfg.EventWebhookTimeout <= 0 {
		cfg.EventWebhookTimeout = def.EventWebhookTimeout
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_EVENT_PUBLISH_RETRIES")); raw != "" && cfg.EventPublishRetries == 0 {
		if parsed, err := strconv.Atoi(raw); err == nil {
			cfg.EventPublishRetries = parsed
		}
	}
	if cfg.EventPublishRetries < 0 {
		cfg.EventPublishRetries = def.EventPublishRetries
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_EVENT_RETRY_BACKOFF")); raw != "" && cfg.EventRetryBackoff == 0 {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			cfg.EventRetryBackoff = parsed
		}
	}
	if cfg.EventRetryBackoff <= 0 {
		cfg.EventRetryBackoff = def.EventRetryBackoff
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_ENABLED")); raw != "" {
		cfg.RAG = raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes") || strings.EqualFold(raw, "on")
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_INDEX_PATH")); raw != "" {
		cfg.RAGPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_TOP_K")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			cfg.RAGTopK = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_MAX_SNIPPET_CHARS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			cfg.RAGChars = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_MAX_QUERY_CHARS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			cfg.RAGMaxQueryChars = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_MAX_FINDINGS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			cfg.RAGMaxFindings = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_MIN_CONFIDENCE")); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			cfg.RAGMinConfidence = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_DATASET_PATH")); raw != "" {
		cfg.RAGDatasetPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_CHUNK_SIZE")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			cfg.RAGChunkSize = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_CHUNK_OVERLAP")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			cfg.RAGChunkOverlap = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_CHUNK_STRATEGY")); raw != "" {
		cfg.RAGChunkStrategy = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_RETRIEVAL_MODE")); raw != "" {
		cfg.RAGRetrievalMode = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_EMBEDDING_PROVIDER")); raw != "" {
		cfg.RAGEmbeddingProvider = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_EMBEDDING_MODEL")); raw != "" {
		cfg.RAGEmbeddingModel = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_EMBEDDING_BASE_URL")); raw != "" {
		cfg.RAGEmbeddingBaseURL = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_EMBEDDING_API_KEY")); raw != "" {
		cfg.RAGEmbeddingAPIKey = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_BACKEND")); raw != "" {
		cfg.RAGVectorBackend = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_ENDPOINT")); raw != "" {
		cfg.RAGVectorEndpoint = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_COLLECTION")); raw != "" {
		cfg.RAGVectorCollection = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_DATABASE")); raw != "" {
		cfg.RAGVectorDatabase = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_TOKEN")); raw != "" {
		cfg.RAGVectorToken = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			cfg.RAGVectorTimeout = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_REBUILD_POLICY")); raw != "" {
		cfg.RAGRebuildPolicy = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_ANALYSIS_REUSE_ENABLED")); raw != "" {
		cfg.AnalysisReuseEnabled = raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes") || strings.EqualFold(raw, "on")
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_ANALYSIS_REUSE_WINDOW")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			cfg.AnalysisReuseWindow = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_ANALYSIS_REUSE_MAX_KEYS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			cfg.AnalysisReuseMaxKeys = parsed
		}
	}
	for _, raw := range []string{
		strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_SOURCE_PATHS")),
		strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_DOC_PATHS")),
	} {
		if raw == "" {
			continue
		}
		cfg.RAGDocs = splitCommaList(raw)
		break
	}
	if cfg.EventWebhookURL == "" {
		cfg.EventWebhookURL = strings.TrimSpace(os.Getenv("SRE_AGENT_EVENT_WEBHOOK_URL"))
	}
	if cfg.EventWebhookToken == "" {
		cfg.EventWebhookToken = strings.TrimSpace(os.Getenv("SRE_AGENT_EVENT_WEBHOOK_TOKEN"))
	}
	if cfg.EventSlackWebhookURL == "" {
		cfg.EventSlackWebhookURL = strings.TrimSpace(os.Getenv("SRE_AGENT_EVENT_SLACK_WEBHOOK_URL"))
	}
	if cfg.EventPagerDutyRoutingKey == "" {
		cfg.EventPagerDutyRoutingKey = strings.TrimSpace(os.Getenv("SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY"))
	}
	if cfg.EventPagerDutyEventsURL == "" {
		cfg.EventPagerDutyEventsURL = firstNonEmpty(
			strings.TrimSpace(os.Getenv("SRE_AGENT_EVENT_PAGERDUTY_EVENTS_URL")),
			def.EventPagerDutyEventsURL,
		)
	}
	if cfg.ActionTimeout <= 0 {
		cfg.ActionTimeout = def.ActionTimeout
	}
	if cfg.MaxParallelActionExec <= 0 {
		cfg.MaxParallelActionExec = def.MaxParallelActionExec
	}
	if cfg.ExplainabilityEvidenceMax <= 0 {
		cfg.ExplainabilityEvidenceMax = def.ExplainabilityEvidenceMax
	}
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv("SRE_AGENT_LLM_API_KEY"))
	}
	return cfg
}

func normalizeQueryRequest(req QueryRequest, maxChars int) QueryRequest {
	req.Query = strings.TrimSpace(stripControls(req.Query))
	req.Node = strings.TrimSpace(stripControls(req.Node))
	if maxChars > 0 && len(req.Query) > maxChars {
		req.Query = req.Query[:maxChars]
	}
	return req
}

func stripControls(in string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' && r != '\r' {
			return -1
		}
		return r
	}, in)
}

func newQueryID() string {
	return fmt.Sprintf("q-%d", time.Now().UnixNano())
}

type recentAnalysis struct {
	StoredAt             time.Time
	Payload              llmPayload
	RAGSnippets          []string
	RetrievedDocs        []rag.SearchHit
	RetrievalSummary     string
	RetrievalEvidenceIDs []string
	RetrievalConfidence  float64
	RetrievalIntent      string
	RetrievalMode        string
}

func (s *QueryService) shouldReuseAnalysis(in PromptInput, noTelemetry bool) bool {
	if s == nil {
		return false
	}
	return s.cfg.AnalysisReuseEnabled &&
		s.cfg.AnalysisReuseWindow > 0 &&
		!in.TelemetryStale &&
		!noTelemetry
}

func (s *QueryService) analysisReuseKey(req QueryRequest, in PromptInput) string {
	if s == nil {
		return ""
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	if query == "" {
		return ""
	}
	schema := buildPromptSchema(in)
	hasher := fnv.New64a()
	writeHashString := func(part string) {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}

	writeHashString(query)
	writeHashString(strings.ToLower(strings.TrimSpace(firstNonEmpty(in.NodeName, req.Node))))
	writeHashString(schema.TelemetryQuality.State)
	writeHashString(firstNonEmpty(schema.TelemetryQuality.SourceMode, "unknown"))
	writeHashString(firstNonEmpty(schema.TelemetryQuality.RuntimeMode, "unknown"))
	writeHashString(fmt.Sprintf("partial=%t", schema.TelemetryQuality.Partial))
	writeHashString(fmt.Sprintf("coverage=%.0f", math.Round(schema.TelemetryQuality.CoveragePercent)))
	writeHashString(fmt.Sprintf("confidence=%.2f", math.Round(schema.TelemetryQuality.Confidence*100)/100))

	metricNames := make([]string, 0, len(schema.Metrics))
	for name := range schema.Metrics {
		metricNames = append(metricNames, name)
	}
	sort.Strings(metricNames)
	for _, name := range metricNames {
		writeHashString(fmt.Sprintf("%s=%.4f", name, quantizeAnalysisMetricValue(name, schema.Metrics[name])))
	}

	for _, finding := range limitPromptStrings(schema.Alerts, 8) {
		writeHashString(strings.TrimSpace(finding))
	}
	for _, anomaly := range limitPromptStrings(schema.Anomalies, 6) {
		writeHashString(strings.TrimSpace(anomaly))
	}
	for _, proc := range limitPromptProcesses(schema.Evidence.Processes, 3) {
		writeHashString(fmt.Sprintf("proc=%s cpu=%.1f rss=%d", strings.ToLower(strings.TrimSpace(proc.Name)), math.Round(proc.CPU*10)/10, bucketUint64(proc.RSSBytes, 64*1024*1024)))
	}
	for _, log := range limitPromptLogs(schema.Evidence.Logs, 3) {
		writeHashString(fmt.Sprintf("log=%s count=%d", strings.ToLower(strings.TrimSpace(log.Fingerprint)), bucketUint64(log.Count, 5)))
	}
	return fmt.Sprintf("%s:%x", firstNonEmpty(schema.NodeName, "fleet"), hasher.Sum64())
}

func quantizeAnalysisMetricValue(name string, value float64) float64 {
	switch {
	case strings.Contains(name, "percent"):
		return math.Round(value*10) / 10
	case strings.Contains(name, "ratio"):
		return math.Round(value*1000) / 1000
	case strings.Contains(name, "latency") || strings.Contains(name, "_seconds"):
		return math.Round(value*1000) / 1000
	case strings.Contains(name, "bytes"):
		return math.Round(value / float64(16*1024*1024))
	case strings.Contains(name, "per_second") || strings.Contains(name, "_bps") || strings.Contains(name, "iops"):
		return math.Round(value / 1024)
	default:
		return math.Round(value*10) / 10
	}
}

func bucketUint64(value uint64, bucket uint64) uint64 {
	if bucket == 0 {
		return value
	}
	return value / bucket
}

func limitPromptProcesses(values []PromptProcess, limit int) []PromptProcess {
	if len(values) == 0 {
		return nil
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values
}

func limitPromptLogs(values []PromptLog, limit int) []PromptLog {
	if len(values) == 0 {
		return nil
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values
}

func recentAnalysisFromPrompt(in PromptInput, payload llmPayload) recentAnalysis {
	return recentAnalysis{
		StoredAt:             time.Now().UTC(),
		Payload:              payload,
		RAGSnippets:          append([]string(nil), in.RAGSnippets...),
		RetrievedDocs:        append([]rag.SearchHit(nil), in.RetrievedDocs...),
		RetrievalSummary:     in.RetrievalSummary,
		RetrievalEvidenceIDs: append([]string(nil), in.RetrievalEvidenceIDs...),
		RetrievalConfidence:  in.RetrievalConfidence,
		RetrievalIntent:      in.RetrievalIntent,
		RetrievalMode:        in.RetrievalMode,
	}
}

func applyRecentAnalysis(in *PromptInput, cached recentAnalysis) {
	if in == nil {
		return
	}
	in.RAGSnippets = append([]string(nil), cached.RAGSnippets...)
	in.RetrievedDocs = append([]rag.SearchHit(nil), cached.RetrievedDocs...)
	in.RetrievalSummary = cached.RetrievalSummary
	in.RetrievalEvidenceIDs = append([]string(nil), cached.RetrievalEvidenceIDs...)
	in.RetrievalConfidence = cached.RetrievalConfidence
	in.RetrievalIntent = cached.RetrievalIntent
	in.RetrievalMode = cached.RetrievalMode
}

func (s *QueryService) loadRecentAnalysis(key string, now time.Time) (recentAnalysis, bool) {
	if s == nil || strings.TrimSpace(key) == "" {
		return recentAnalysis{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneRecentAnalysesLocked(now)
	entry, ok := s.recentAnalyses[key]
	if !ok {
		return recentAnalysis{}, false
	}
	return entry, true
}

func (s *QueryService) storeRecentAnalysis(key string, entry recentAnalysis) {
	if s == nil || strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneRecentAnalysesLocked(time.Now().UTC())
	if _, exists := s.recentAnalyses[key]; !exists {
		s.recentAnalysisOrder = append(s.recentAnalysisOrder, key)
	}
	s.recentAnalyses[key] = entry
	if maxKeys := s.cfg.AnalysisReuseMaxKeys; maxKeys > 0 {
		for len(s.recentAnalysisOrder) > maxKeys {
			evict := s.recentAnalysisOrder[0]
			s.recentAnalysisOrder = s.recentAnalysisOrder[1:]
			delete(s.recentAnalyses, evict)
		}
	}
}

func (s *QueryService) pruneRecentAnalysesLocked(now time.Time) {
	if s == nil || len(s.recentAnalyses) == 0 {
		return
	}
	ttl := s.cfg.AnalysisReuseWindow
	if ttl <= 0 {
		s.recentAnalyses = make(map[string]recentAnalysis)
		s.recentAnalysisOrder = nil
		return
	}
	filtered := s.recentAnalysisOrder[:0]
	for _, key := range s.recentAnalysisOrder {
		entry, ok := s.recentAnalyses[key]
		if !ok {
			continue
		}
		if now.Sub(entry.StoredAt) > ttl {
			delete(s.recentAnalyses, key)
			continue
		}
		filtered = append(filtered, key)
	}
	s.recentAnalysisOrder = filtered
}

func newApprovalToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "appr-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return "appr-" + fmt.Sprintf("%x", buf[:])
}

func summarizeProcesses(in []*telemetryv1.ProcessSample, limit int) []PromptProcess {
	if len(in) == 0 {
		return nil
	}
	out := make([]PromptProcess, 0, len(in))
	for _, process := range in {
		if process == nil {
			continue
		}
		out = append(out, PromptProcess{
			PID:       process.Pid,
			Name:      strings.TrimSpace(process.Name),
			CPU:       process.CpuPercent,
			RSSBytes:  process.RssBytes,
			IOReadBPS: process.IoReadBps,
			IOWrtBPS:  process.IoWriteBps,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CPU > out[j].CPU })
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func summarizeLogs(in []*telemetryv1.LogFingerprint, limit int) []PromptLog {
	if len(in) == 0 {
		return nil
	}
	out := make([]PromptLog, 0, len(in))
	for _, fp := range in {
		if fp == nil {
			continue
		}
		out = append(out, PromptLog{
			Fingerprint: strings.TrimSpace(fp.Fingerprint),
			Count:       fp.Count,
			Example:     strings.TrimSpace(fp.Example),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func cloneMetricMap(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeGPUContext(metrics map[string]float64, node *gpuobs.Node) {
	if node == nil {
		return
	}
	metrics["node_gpu_count"] = float64(node.GPUCount)
	if len(node.GPUs) == 0 {
		return
	}
	var utilSum float64
	var memUsedSum float64
	var memTotalSum float64
	var pcieRxSum float64
	var pcieTxSum float64
	var tempMax float64
	var processCount float64
	idx := 0
	for _, gpu := range node.GPUs {
		utilSum += gpu.UtilSMPercent
		memUsedSum += gpu.MemUsedMiB
		memTotalSum += gpu.MemTotalMiB
		pcieRxSum += gpu.PCIERxMBs
		pcieTxSum += gpu.PCIETxMBs
		processCount += gpu.ProcessCount
		if gpu.TempC > tempMax {
			tempMax = gpu.TempC
		}
		if gpu.UtilSMPercent > metrics["node_gpu_utilization_sm_peak_percent"] {
			metrics["node_gpu_utilization_sm_peak_percent"] = gpu.UtilSMPercent
		}
		metrics[fmt.Sprintf("node_gpu_%d_memory_used_mib", idx)] = gpu.MemUsedMiB
		idx++
	}
	metrics["node_gpu_utilization_sm_avg_percent"] = utilSum / float64(len(node.GPUs))
	metrics["node_gpu_memory_used_total_mib"] = memUsedSum
	metrics["node_gpu_memory_total_mib"] = memTotalSum
	if memTotalSum > 0 {
		metrics["node_gpu_memory_used_percent"] = (memUsedSum / memTotalSum) * 100.0
	}
	metrics["node_gpu_temperature_peak_celsius"] = tempMax
	metrics["node_gpu_pcie_rx_total_mb_s"] = pcieRxSum
	metrics["node_gpu_pcie_tx_total_mb_s"] = pcieTxSum
	metrics["node_gpu_process_total"] = processCount
	metrics["node_gpu_process_count_total"] = processCount
}

func gpuFindings(node *gpuobs.Node) []string {
	if node == nil {
		return nil
	}
	findings := make([]string, 0, len(node.GPUs)+2)
	for _, gpu := range node.GPUs {
		if gpu.UtilSMPercent >= 90 {
			findings = append(findings, fmt.Sprintf("GPU %s SM utilization is %.1f%%", gpu.GPUIndex, gpu.UtilSMPercent))
		}
		if gpu.MemTotalMiB > 0 && gpu.MemUsedMiB/gpu.MemTotalMiB >= 0.9 {
			findings = append(findings, fmt.Sprintf("GPU %s memory pressure is high", gpu.GPUIndex))
		}
	}
	return dedupeStrings(findings)
}

func systemFindings(metrics map[string]float64) []string {
	findings := make([]string, 0, 4)
	if metrics["node_cpu_usage_percent"] >= 85 {
		findings = append(findings, "CPU utilization is above 85%")
	}
	memUsed := metrics["node_memory_Used_bytes"]
	memTotal := metrics["node_memory_MemTotal_bytes"]
	if memTotal > 0 && memUsed/memTotal >= 0.85 {
		findings = append(findings, "Memory utilization is above 85%")
	}
	if metrics["node_disk_io_now"] >= 50 {
		findings = append(findings, "Disk I/O pressure is elevated")
	}
	return findings
}

func operationalFindings(metrics map[string]float64, trends map[string]string, logs []PromptLog) []string {
	findings := make([]string, 0, 6)

	cpuTrend := trendValue(trends, "node_cpu_usage_percent")
	memTrend := trendValue(trends, "node_memory_Used_bytes", "node_memory_used_bytes")
	iowaitTrend := trendValue(trends, "node_cpu_iowait_percent")
	diskTrend := trendValue(trends, "node_disk_request_latency_p99_seconds", "node_disk_avg_request_latency_seconds")
	retransTrend := trendValue(trends, "node_tcp_retransmit_ratio", "node_tcp_retransmits_per_second")

	cpuIOWait := metricFirstValue(metrics, "node_cpu_iowait_percent")
	diskLatencyMs := metricFirstValue(metrics, "node_disk_request_latency_p99_seconds", "node_disk_avg_request_latency_seconds") * 1000
	diskQueueDepth := metricFirstValue(metrics, "node_disk_queue_depth_total")
	gpuUtil := metricFirstValue(metrics, "node_gpu_utilization_sm_avg_percent")
	gpuProcesses := metricFirstValue(metrics, "node_gpu_process_total")
	retransRatio := metricFirstValue(metrics, "node_tcp_retransmit_ratio")
	retransmits := metricFirstValue(metrics, "node_tcp_retransmits_per_second")
	memPercent := metricPercent(metrics, "node_memory_Used_bytes", "node_memory_MemTotal_bytes", "node_memory_used_bytes", "node_memory_total_bytes")
	securityFindings := metricFirstValue(metrics, "node_security_findings_total")
	probeCoreActive := metricFirstValue(metrics, "collector_probe_core_active")
	probeCoreAgeMs := metricFirstValue(metrics, "collector_probe_core_last_frame_age_seconds") * 1000

	switch {
	case (cpuIOWait >= 10 || iowaitTrend == "rising") && (diskLatencyMs >= 40 || diskQueueDepth >= 8 || diskTrend == "rising"):
		findings = append(findings, "CPU wait and disk latency are rising together, which points to a storage bottleneck rather than pure CPU saturation")
	}

	switch {
	case gpuProcesses > 0 && gpuUtil > 0 && gpuUtil < 35 && (cpuTrend == "rising" || diskTrend == "rising" || retransTrend == "rising"):
		findings = append(findings, "GPU workers are underutilized while host-side pressure is increasing, which suggests feeder starvation or placement contention")
	}

	switch {
	case memPercent >= 80 && memTrend == "rising" && hasPromptLogKeyword(logs, "timeout", "error", "oom"):
		findings = append(findings, "Memory growth is being reinforced by error or timeout activity, which looks more like leak or retry amplification than a one-off spike")
	case memPercent >= 80 && memTrend == "rising":
		findings = append(findings, "Memory headroom is shrinking steadily, which points to structural capacity exhaustion rather than a transient spike")
	}

	switch {
	case retransRatio >= 0.02 || retransmits >= 0.5 || retransTrend == "rising" || hasPromptLogKeyword(logs, "timeout", "refused"):
		findings = append(findings, "Network retransmits or timeout bursts are active, which suggests congestion or packet loss instead of application-only latency")
	}

	if securityFindings > 0 {
		findings = append(findings, "Security findings are active in the same window, so process and network symptoms should be correlated before assuming benign load")
	}
	if probeCoreActive == 0 || probeCoreAgeMs >= 5000 {
		findings = append(findings, "Host telemetry freshness is degraded, so flat or missing metrics may reflect stale collection instead of a healthy system")
	}

	return dedupeStrings(findings)
}

type promptCriticalSignal struct {
	Label   string
	Metrics []string
}

var promptCriticalSignals = []promptCriticalSignal{
	{Label: "cpu pressure", Metrics: []string{"node_cpu_usage_percent"}},
	{Label: "memory pressure", Metrics: []string{"node_memory_Used_bytes", "node_memory_used_bytes", "node_memory_MemTotal_bytes", "node_memory_total_bytes"}},
	{Label: "network throughput", Metrics: []string{"node_network_total_receive_bytes_per_second", "node_network_receive_bytes_per_second", "node_network_total_transmit_bytes_per_second", "node_network_transmit_bytes_per_second"}},
	{Label: "storage activity", Metrics: []string{"node_disk_total_read_bytes_per_second", "node_disk_read_bytes_per_second", "node_disk_total_written_bytes_per_second", "node_disk_written_bytes_per_second", "node_disk_request_latency_p99_seconds"}},
	{Label: "telemetry integrity", Metrics: []string{"collector_probe_core_fresh", "collector_probe_core_active"}},
}

func assessPromptTelemetryQuality(
	node *ingest.NodeSnapshot,
	metrics map[string]float64,
	processes []PromptProcess,
	logs []PromptLog,
	queryAt time.Time,
	staleAfter time.Duration,
) PromptTelemetryQuality {
	quality := PromptTelemetryQuality{
		State:   "unavailable",
		QueryAt: queryAt,
	}
	if node != nil {
		quality.SourceMode = strings.TrimSpace(node.ProbeSource)
		quality.RuntimeMode = strings.TrimSpace(node.RuntimeMode)
		quality.LatestCollectionAt = node.LastCollectionAt
		quality.LatestIngestAt = node.LastIngestAt
	}

	if len(metrics) == 0 && len(processes) == 0 && len(logs) == 0 {
		quality.BlindSpots = []string{"no metrics, process samples, or logs are attached to this query"}
		quality.Confidence = 0.15
		return quality
	}

	reference := quality.LatestCollectionAt
	if reference.IsZero() && node != nil {
		reference = node.UpdatedAt
	}
	if reference.IsZero() {
		reference = queryAt
	}
	quality.FreshnessAgeSeconds = maxFloat64(0, queryAt.Sub(reference).Seconds())
	if !quality.LatestCollectionAt.IsZero() && !quality.LatestIngestAt.IsZero() {
		quality.IngestDelaySeconds = maxFloat64(0, quality.LatestIngestAt.Sub(quality.LatestCollectionAt).Seconds())
	}

	quality.MissingSignals = promptMissingSignals(metrics)
	quality.Partial = len(quality.MissingSignals) > 0
	quality.CoveragePercent = promptCoveragePercent(quality.MissingSignals)
	quality.BlindSpots = promptBlindSpots(node, metrics, processes, logs, quality)

	delayThreshold := 90 * time.Second
	staleThreshold := staleAfter
	if staleThreshold <= 0 {
		staleThreshold = 2 * time.Minute
	}
	switch {
	case quality.FreshnessAgeSeconds >= staleThreshold.Seconds():
		quality.State = "stale"
	case len(quality.BlindSpots) > 0 || quality.Partial:
		quality.State = "degraded"
	case quality.FreshnessAgeSeconds >= delayThreshold.Seconds():
		quality.State = "delayed"
	default:
		quality.State = "fresh"
	}
	quality.SafeToAct = quality.State == "fresh" && !quality.Partial && len(quality.BlindSpots) == 0
	quality.Confidence = promptTelemetryConfidence(quality)
	return quality
}

func promptMissingSignals(metrics map[string]float64) []string {
	missing := make([]string, 0, len(promptCriticalSignals))
	for _, signal := range promptCriticalSignals {
		present := true
		for _, name := range signal.Metrics {
			if _, ok := metrics[name]; ok {
				present = true
				goto nextSignal
			}
			present = false
		}
	nextSignal:
		if !present {
			missing = append(missing, signal.Label)
		}
	}
	return missing
}

func promptCoveragePercent(missing []string) float64 {
	total := len(promptCriticalSignals)
	if total == 0 {
		return 100
	}
	covered := total - len(missing)
	if covered < 0 {
		covered = 0
	}
	return float64(covered) / float64(total) * 100
}

func promptBlindSpots(
	node *ingest.NodeSnapshot,
	metrics map[string]float64,
	processes []PromptProcess,
	logs []PromptLog,
	quality PromptTelemetryQuality,
) []string {
	blindSpots := make([]string, 0, 8)
	if len(processes) == 0 {
		blindSpots = append(blindSpots, "process attribution is missing")
	}
	if len(logs) == 0 {
		blindSpots = append(blindSpots, "log evidence is missing")
	}
	if quality.IngestDelaySeconds >= 30 {
		blindSpots = append(blindSpots, "telemetry replay or transport delay is elevated")
	}
	if metricFirstValue(metrics, "collector_spool_backlog_bytes") > 0 {
		blindSpots = append(blindSpots, "collector replay backlog is still draining")
	}
	if probeFresh := metricFirstValue(metrics, "collector_probe_core_fresh"); probeFresh > 0 && probeFresh < 1 {
		blindSpots = append(blindSpots, "probe-core freshness is degraded")
	}
	if probeActive := metricFirstValue(metrics, "collector_probe_core_active"); probeActive == 0 && metricFirstValue(metrics, "collector_probe_core_client_available") >= 1 {
		blindSpots = append(blindSpots, "probe-core client is available but inactive")
	}
	if node != nil {
		if node.RuntimeModeDegraded {
			blindSpots = append(blindSpots, "collector runtime mode is degraded")
		}
		for _, reason := range node.RuntimeReasons {
			if strings.TrimSpace(reason) != "" {
				blindSpots = append(blindSpots, strings.ReplaceAll(reason, "_", " "))
			}
		}
		if source := strings.TrimSpace(node.ProbeSource); source != "" && !strings.EqualFold(source, "probe_core") {
			blindSpots = append(blindSpots, fmt.Sprintf("collector is using %s compatibility source", source))
		}
	}
	return dedupeStrings(blindSpots)
}

func promptTelemetryConfidence(quality PromptTelemetryQuality) float64 {
	confidence := 1.0
	confidence -= (100 - clampPercent(quality.CoveragePercent)) / 100 * 0.45
	switch quality.State {
	case "delayed":
		confidence -= 0.1
	case "degraded":
		confidence -= 0.2
	case "stale":
		confidence -= 0.35
	case "unavailable":
		confidence -= 0.6
	}
	if quality.IngestDelaySeconds >= 30 {
		confidence -= 0.1
	}
	if confidence < 0.1 {
		return 0.1
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}

func telemetryQualityFindings(quality PromptTelemetryQuality) []string {
	findings := make([]string, 0, 4)
	switch quality.State {
	case "unavailable":
		findings = append(findings, "Observability coverage is currently unavailable, so this RCA has major blind spots.")
	case "stale":
		findings = append(findings, "Observability data is stale enough to increase MTTR risk and false-RCA risk.")
	case "delayed":
		findings = append(findings, "Observability data is delayed, so fast-moving symptoms may already have shifted.")
	case "degraded":
		findings = append(findings, "Observability coverage is degraded, so missing or flat signals may reflect the pipeline rather than the workload.")
	}
	if len(quality.MissingSignals) > 0 {
		findings = append(findings, "Missing critical signals: "+strings.Join(quality.MissingSignals, ", "))
	}
	return dedupeStrings(findings)
}

func trendHints(history ingest.MetricHistoryProvider, collectorID string) []string {
	if history == nil || collectorID == "" {
		return nil
	}
	samples := history.MetricHistory(collectorID, time.Now().Add(-30*time.Minute), 12)
	if len(samples) < 3 {
		return nil
	}
	hints := make([]string, 0, 4)
	gpuProfile, ok := historyMetricProfile(samples, "node_gpu_utilization_sm_avg_percent")
	if ok && gpuProfile.Direction == "falling" && gpuProfile.Sustained {
		if cpuProfile, ok := historyMetricProfile(samples, "node_cpu_usage_percent"); ok && cpuProfile.Direction == "rising" {
			hints = append(hints, "GPU utilization is falling while CPU demand is rising, which points to feeder or placement contention instead of pure compute saturation")
		} else {
			hints = append(hints, "GPU utilization is falling over the last 30m")
		}
	}
	if memProfile, ok := historyMetricProfile(samples, "node_memory_Used_bytes", "node_memory_used_bytes"); ok && memProfile.Direction == "rising" && memProfile.Sustained {
		hints = append(hints, "Memory usage is climbing steadily, which raises leak, cache growth, or retry amplification risk")
	}
	if retransProfile, ok := historyMetricProfile(samples, "node_tcp_retransmit_ratio", "node_tcp_retransmits_per_second"); ok && (retransProfile.Pattern == "bursty" || retransProfile.Direction == "rising") {
		hints = append(hints, "Network retransmits are rising, which suggests congestion or packet loss rather than application-only latency")
	}
	return dedupeStrings(hints)
}

func metricTrends(history ingest.MetricHistoryProvider, collectorID string) map[string]string {
	if history == nil || collectorID == "" {
		return nil
	}
	samples := history.MetricHistory(collectorID, time.Now().Add(-30*time.Minute), 20)
	if len(samples) < 3 {
		return nil
	}
	trends := map[string]string{}
	evaluate := func(metric string, aliases ...string) {
		profile, ok := historyMetricProfile(samples, append([]string{metric}, aliases...)...)
		if !ok {
			return
		}
		trends[metric] = signalinsights.Direction(profile)
	}
	evaluate("node_cpu_usage_percent")
	evaluate("node_gpu_utilization_sm_avg_percent")
	evaluate("node_memory_Used_bytes", "node_memory_used_bytes")
	evaluate("node_tcp_retransmit_ratio", "node_tcp_retransmits_per_second")
	if len(trends) == 0 {
		return nil
	}
	return trends
}

func metricFirstValue(metrics map[string]float64, names ...string) float64 {
	for _, name := range names {
		if value, ok := metrics[name]; ok {
			return value
		}
	}
	return 0
}

func metricPercent(metrics map[string]float64, usedPrimary, totalPrimary, usedAlias, totalAlias string) float64 {
	used := metricFirstValue(metrics, usedPrimary, usedAlias)
	total := metricFirstValue(metrics, totalPrimary, totalAlias)
	if total <= 0 {
		return 0
	}
	return used / total * 100
}

func trendValue(trends map[string]string, names ...string) string {
	for _, name := range names {
		if trend, ok := trends[name]; ok && trend != "" {
			return trend
		}
	}
	return ""
}

func hasPromptLogKeyword(logs []PromptLog, keywords ...string) bool {
	for _, log := range logs {
		text := strings.ToLower(log.Fingerprint + " " + log.Example)
		for _, keyword := range keywords {
			if strings.Contains(text, strings.ToLower(keyword)) {
				return true
			}
		}
	}
	return false
}

func historyMetricProfile(samples []ingest.MetricHistorySample, names ...string) (signalinsights.Profile, bool) {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		value, ok := historyMetricValue(sample.Metrics, names...)
		if !ok {
			continue
		}
		values = append(values, value)
	}
	if len(values) < 3 {
		return signalinsights.Profile{}, false
	}
	return signalinsights.ProfileFromValues(values, 0), true
}

func historyMetricValue(metrics map[string]float64, names ...string) (float64, bool) {
	for _, name := range names {
		value, ok := metrics[name]
		if ok {
			return value, true
		}
	}
	return 0, false
}

func (s *QueryService) ragContext(query string, findings, anomalies []string) ([]string, rag.QueryResult) {
	if s == nil || s.rag == nil {
		return nil, rag.QueryResult{}
	}
	topK := s.cfg.RAGTopK
	if topK <= 0 {
		topK = 4
	}
	if !shouldAttachQueryServiceRAG(query, findings, anomalies) {
		s.ragSkippedContextTotal.Add(1)
		return nil, rag.QueryResult{}
	}
	req := buildQueryServiceRAGRequest(query, findings, anomalies, topK, s.cfg.RAGMaxFindings, s.cfg.RAGMaxQueryChars)
	if strings.TrimSpace(req.Query) == "" {
		s.ragSkippedContextTotal.Add(1)
		return nil, rag.QueryResult{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), minDuration(2*time.Second, s.cfg.Timeout))
	defer cancel()
	result, err := s.rag.Query(ctx, req)
	if err != nil {
		s.logger.Debug("rag retrieval failed", zap.Error(err))
		return nil, rag.QueryResult{}
	}
	if len(result.Hits) > 0 && result.Confidence < s.cfg.RAGMinConfidence {
		s.ragLowConfidenceTotal.Add(1)
		result.Hits = nil
		result.RetrievalEvidenceIDs = nil
		result.Summary = fmt.Sprintf(
			"%s; retrieval suppressed because confidence %.2f is below minimum %.2f",
			firstNonEmpty(result.Summary, "knowledge hits were low confidence"),
			result.Confidence,
			s.cfg.RAGMinConfidence,
		)
		return nil, result
	}
	out := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		text := renderQueryServiceRAGSnippet(hit)
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, truncateOutput(text))
	}
	return dedupeStrings(out), result
}

func (s *QueryService) ragSnippets(query string, findings, anomalies []string) []string {
	snippets, _ := s.ragContext(query, findings, anomalies)
	return snippets
}

func buildQueryServiceRAGRequest(query string, findings, anomalies []string, topK, maxFindings, maxChars int) rag.QueryRequest {
	combined := compactRAGQueryText(query, findings, anomalies, maxFindings, maxChars)
	intent, knowledgeTypes, caseTypes := inferQueryServiceRAGIntent(combined)

	return rag.QueryRequest{
		Query:          combined,
		TopK:           topK,
		Intent:         intent,
		KnowledgeTypes: knowledgeTypes,
		CaseTypes:      caseTypes,
	}
}

func compactRAGQueryText(query string, findings, anomalies []string, maxSignals, maxChars int) string {
	capHint := 2
	if maxSignals > 0 {
		capHint = maxSignals + 1
	}
	parts := make([]string, 0, capHint)
	currentLen := 0
	appendPart := func(part string) bool {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}
		projected := currentLen + len(part)
		if len(parts) > 0 {
			projected++
		}
		if maxChars > 0 && projected > maxChars {
			return false
		}
		parts = append(parts, part)
		currentLen = projected
		return true
	}
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		if maxChars > 0 && (len(findings) > 0 || len(anomalies) > 0) {
			queryBudget := maxChars / 2
			if queryBudget < 32 {
				queryBudget = maxChars
			}
			if len(trimmed) > queryBudget {
				trimmed = strings.TrimSpace(trimmed[:queryBudget])
			}
		}
		if !appendPart(trimmed) && maxChars > 0 {
			appendPart(strings.TrimSpace(trimmed[:minInt(len(trimmed), maxChars)]))
		}
	}
	signals := meaningfulRetrievalSignals(findings, anomalies)
	seen := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		signal = strings.TrimSpace(signal)
		if signal == "" {
			continue
		}
		if _, ok := seen[signal]; ok {
			continue
		}
		seen[signal] = struct{}{}
		if !appendPart(signal) {
			break
		}
		if maxSignals > 0 && len(parts) >= maxSignals+1 {
			break
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func inferQueryServiceRAGIntent(query string) (string, []string, []string) {
	combinedLower := strings.ToLower(strings.TrimSpace(query))
	intent := "rca"
	knowledgeTypes := []string{"historical_incident", "runbook", "question_pattern"}
	caseTypes := []string{"historical_incident", "runbook", "operational_qa"}

	switch {
	case containsAny(combinedLower, "how to", "steps", "runbook", "playbook", "troubleshoot", "fix", "remediate", "排查", "处理", "修复"):
		intent = "runbook"
		knowledgeTypes = []string{"runbook", "question_pattern"}
		caseTypes = []string{"runbook", "operational_qa"}
	case containsAny(combinedLower, "similar", "history", "prior", "previous", "incident", "postmortem", "案例", "历史"):
		intent = "historical_incident"
		knowledgeTypes = []string{"historical_incident", "question_pattern"}
		caseTypes = []string{"historical_incident", "operational_qa"}
	case containsAny(combinedLower, "recommend", "next step", "action", "should i", "建议", "下一步"):
		intent = "recommendation"
		knowledgeTypes = []string{"runbook", "historical_incident", "question_pattern"}
		caseTypes = []string{"runbook", "historical_incident", "operational_qa"}
	case containsAny(combinedLower, "security", "malware", "permission", "credential", "certificate", "cve", "权限", "安全", "证书"):
		intent = "security"
		knowledgeTypes = []string{"security_reference", "runbook", "historical_incident"}
		caseTypes = []string{"security_event", "runbook", "historical_incident"}
	case containsAny(combinedLower, "risk", "weak signal", "co-occur", "joint", "latent", "风险", "弱信号", "关联"):
		intent = "joint_risk"
	}
	return intent, knowledgeTypes, caseTypes
}

func shouldAttachQueryServiceRAG(query string, findings, anomalies []string) bool {
	if len(meaningfulRetrievalSignals(findings, anomalies)) > 0 {
		return true
	}
	return queryHasOperationalSignalKeywords(query)
}

func meaningfulRetrievalSignals(findings, anomalies []string) []string {
	out := make([]string, 0, len(findings)+len(anomalies))
	out = append(out, filterFindingsForRetrieval(findings)...)
	out = append(out, filterAnomaliesForRetrieval(anomalies)...)
	return dedupeStrings(out)
}

func filterFindingsForRetrieval(findings []string) []string {
	if len(findings) == 0 {
		return nil
	}
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		trimmed := strings.TrimSpace(finding)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		switch {
		case strings.Contains(lower, "no critical anomalies detected"):
			continue
		case strings.HasPrefix(lower, "telemetry snapshot is "):
			continue
		case strings.Contains(lower, "telemetry freshness is degraded"):
			continue
		case strings.Contains(lower, "observability coverage is "):
			continue
		case strings.Contains(lower, "missing critical signals:"):
			continue
		case strings.Contains(lower, "host telemetry freshness is degraded"):
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func filterAnomaliesForRetrieval(anomalies []string) []string {
	if len(anomalies) == 0 {
		return nil
	}
	out := make([]string, 0, len(anomalies))
	for _, anomaly := range anomalies {
		trimmed := strings.TrimSpace(anomaly)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func queryHasOperationalSignalKeywords(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return false
	}
	return containsAny(
		lower,
		"cpu", "memory", "oom", "disk", "i/o", "io", "latency", "timeout", "retrans", "network",
		"packet", "drop", "throughput", "gpu", "thermal", "throttle", "nvme", "sata", "nic",
		"rollout", "deployment", "restart", "crashloop", "eviction", "load", "slow", "queue",
		"backlog", "security", "certificate", "credential", "risk", "congestion", "leak",
	)
}

func renderQueryServiceRAGSnippet(hit rag.SearchHit) string {
	labelParts := []string{firstNonEmpty(hit.KnowledgeType, "knowledge")}
	if hit.CaseType != "" && hit.CaseType != hit.KnowledgeType {
		labelParts = append(labelParts, hit.CaseType)
	}
	label := strings.Join(labelParts, "/")
	title := firstNonEmpty(hit.Title, hit.SourcePath)

	bodyParts := make([]string, 0, 6)
	if summary := strings.TrimSpace(hit.Summary); summary != "" {
		bodyParts = append(bodyParts, "summary="+summary)
	}
	if len(hit.LikelyCauses) > 0 {
		bodyParts = append(bodyParts, "causes="+strings.Join(limitStrings(hit.LikelyCauses, 2), "; "))
	}
	if len(hit.RemediationSteps) > 0 {
		bodyParts = append(bodyParts, "steps="+strings.Join(limitStrings(hit.RemediationSteps, 2), "; "))
	}
	if len(hit.Signals) > 0 {
		bodyParts = append(bodyParts, "signals="+strings.Join(limitStrings(hit.Signals, 4), ", "))
	}
	if len(hit.Commands) > 0 {
		bodyParts = append(bodyParts, "commands="+strings.Join(limitStrings(hit.Commands, 2), "; "))
	}
	if len(bodyParts) == 0 {
		text := strings.TrimSpace(firstNonEmpty(hit.Snippet, hit.Content))
		if text == "" {
			return ""
		}
		bodyParts = append(bodyParts, text)
	}
	return fmt.Sprintf("[%s] %s :: %s", label, title, strings.Join(bodyParts, " | "))
}

func limitStrings(values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	values = append([]string(nil), values...)
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values
}

func containsAny(text string, values ...string) bool {
	text = strings.ToLower(text)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func splitCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func buildExplainability(in PromptInput, out llmPayload, usedFallback bool, gpuEnabled bool, evidenceLimit int) Explainability {
	evidence := append([]string{}, sanitizeStrings(out.Evidence)...)
	evidence = append(evidence, sanitizeStrings(in.Findings)...)
	evidence = dedupeStrings(evidence)
	if evidenceLimit > 0 && len(evidence) > evidenceLimit {
		evidence = evidence[:evidenceLimit]
	}

	limitations := append([]string{}, sanitizeStrings(out.Limitations)...)
	if usedFallback {
		limitations = append(limitations, "LLM path unavailable; deterministic fallback was used")
	}
	if len(in.Metrics) == 0 {
		limitations = append(limitations, "No node metrics were available in the current telemetry slice")
	}
	if len(in.Processes) == 0 {
		limitations = append(limitations, "No process samples were attached to this query")
	}
	if !gpuEnabled {
		limitations = append(limitations, "GPU context was unavailable or not selected for this query")
	}
	if in.TelemetryStale {
		limitations = append(limitations, fmt.Sprintf("Telemetry age %.0fs exceeded stale threshold", in.TelemetryAgeSeconds))
	}
	if len(in.TelemetryQuality.BlindSpots) > 0 {
		limitations = append(limitations, sanitizeStrings(in.TelemetryQuality.BlindSpots)...)
	}
	if len(in.TelemetryQuality.MissingSignals) > 0 {
		limitations = append(limitations, "Missing critical signals: "+strings.Join(in.TelemetryQuality.MissingSignals, ", "))
	}
	limitations = dedupeStrings(limitations)
	if evidenceLimit > 0 && len(limitations) > evidenceLimit {
		limitations = limitations[:evidenceLimit]
	}

	return Explainability{
		TopSignals:   topMetrics(in.Metrics, 6),
		TrendSignals: in.Trends,
		Evidence:     evidence,
		Limitations:  limitations,
		DataCoverage: ExplainabilityData{
			Metrics:                len(in.Metrics),
			Processes:              len(in.Processes),
			Logs:                   len(in.Logs),
			Findings:               len(in.Findings),
			Anomalies:              len(in.Anomalies),
			TelemetryAgeSeconds:    in.TelemetryAgeSeconds,
			TelemetryStale:         in.TelemetryStale,
			TelemetryState:         in.TelemetryQuality.State,
			TelemetryCoveragePct:   in.TelemetryQuality.CoveragePercent,
			TelemetryConfidence:    in.TelemetryQuality.Confidence,
			SourceMode:             in.TelemetryQuality.SourceMode,
			RuntimeMode:            in.TelemetryQuality.RuntimeMode,
			IngestDelaySeconds:     in.TelemetryQuality.IngestDelaySeconds,
			MissingCriticalSignals: append([]string(nil), in.TelemetryQuality.MissingSignals...),
			BlindSpots:             append([]string(nil), in.TelemetryQuality.BlindSpots...),
		},
		UsedDeterministicFallback: usedFallback,
	}
}

func fallbackPayload(in PromptInput, err error) llmPayload {
	rootCause := "Insufficient LLM data"
	if len(in.Findings) > 0 {
		rootCause = in.Findings[0]
	}
	confidence := 0.55
	if in.TelemetryQuality.Confidence > 0 {
		confidence = math.Min(confidence, in.TelemetryQuality.Confidence*0.8)
	}
	return llmPayload{
		Summary:         "Deterministic fallback analysis generated",
		RootCause:       rootCause,
		Confidence:      confidence,
		Findings:        in.Findings,
		Recommendations: fallbackRecommendations(in),
		Actions:         nil,
		Evidence:        in.Findings,
		Limitations:     []string{fmt.Sprintf("LLM fallback reason: %v", err)},
	}
}

func fallbackRecommendations(in PromptInput) []string {
	recommendations := make([]string, 0, 4)
	joinedFindings := strings.ToLower(strings.Join(in.Findings, " | "))

	switch {
	case strings.Contains(joinedFindings, "storage bottleneck"):
		recommendations = append(recommendations,
			"Inspect the hottest disk/device and the top IO-heavy process before scaling CPU",
			"Check queue depth and tail latency around the same time window",
		)
	case strings.Contains(joinedFindings, "feeder starvation") || strings.Contains(joinedFindings, "gpu workers are underutilized"):
		recommendations = append(recommendations,
			"Inspect storage, network, and data-loader stages before changing GPU capacity",
			"Check whether placement is starving feeder threads on the same node",
		)
	case strings.Contains(joinedFindings, "retry amplification") || strings.Contains(joinedFindings, "capacity exhaustion"):
		recommendations = append(recommendations,
			"Inspect top memory consumers together with timeout/error logs before changing limits",
			"Verify whether retries are amplifying memory growth",
		)
	case strings.Contains(joinedFindings, "network retransmits") || strings.Contains(joinedFindings, "packet loss"):
		recommendations = append(recommendations,
			"Inspect retransmits, drops, and timeout-heavy dependencies before tuning application timeouts",
			"Validate MTU, noisy east-west traffic, and link health on the affected path",
		)
	}

	if !in.TelemetryQuality.SafeToAct {
		recommendations = append(recommendations,
			"Verify collector health, replay backlog, and telemetry freshness before taking disruptive remediation actions",
		)
	}
	if len(in.TelemetryQuality.MissingSignals) > 0 {
		recommendations = append(recommendations,
			"Confirm missing critical signals ("+strings.Join(in.TelemetryQuality.MissingSignals, ", ")+") before treating blank charts as healthy zeros",
		)
	}

	recommendations = append(recommendations, "Apply safe playbook actions first")
	return dedupeStrings(recommendations)
}

func mergeActions(primary []ActionSpec, secondary []ActionSpec, queryID string) []ActionSpec {
	all := append([]ActionSpec{}, primary...)
	all = append(all, secondary...)
	all = dedupeActionSpecs(all)
	out := make([]ActionSpec, 0, len(all))
	for i, action := range all {
		action = normalizeAction(action)
		// Always issue query-scoped IDs to avoid collisions across queries.
		action.ID = fmt.Sprintf("%s-a%d", queryID, i+1)
		out = append(out, action)
	}
	return out
}

func parseLLMPayload(raw string) (llmPayload, error) {
	jsonBlock := extractJSONObject(raw)
	if jsonBlock == "" {
		return llmPayload{}, fmt.Errorf("llm response did not contain json payload")
	}
	var payload llmPayload
	if err := json.Unmarshal([]byte(jsonBlock), &payload); err != nil {
		return llmPayload{}, fmt.Errorf("decode llm payload: %w", err)
	}
	payload.Summary = strings.TrimSpace(payload.Summary)
	payload.RootCause = strings.TrimSpace(payload.RootCause)
	payload.Recommendations = sanitizeStrings(payload.Recommendations)
	payload.Findings = sanitizeStrings(payload.Findings)
	payload.Evidence = sanitizeStrings(payload.Evidence)
	payload.Limitations = sanitizeStrings(payload.Limitations)
	if err := validateLLMPayload(payload); err != nil {
		return llmPayload{}, err
	}
	return payload, nil
}

func validateLLMPayload(payload llmPayload) error {
	if strings.TrimSpace(payload.Summary) == "" {
		return fmt.Errorf("llm payload missing summary")
	}
	if strings.TrimSpace(payload.RootCause) == "" {
		return fmt.Errorf("llm payload missing root_cause")
	}
	if payload.Confidence < 0 || payload.Confidence > 1 {
		return fmt.Errorf("llm payload confidence out of range")
	}
	return nil
}

func extractJSONObject(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		return trimmed
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(trimmed[start : end+1])
	}
	return ""
}

func sanitizeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		clean := strings.TrimSpace(stripControls(item))
		if clean == "" {
			continue
		}
		out = append(out, clean)
	}
	return dedupeStrings(out)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(item))
	}
	return out
}

func nonEmpty(primary []string, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func clampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func shouldRetry(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, ErrCircuitOpen)
}

func waitWithBackoff(ctx context.Context, attempt int, base, max time.Duration) error {
	wait := backoffForAttempt(attempt, base, max)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func backoffForAttempt(attempt int, base, max time.Duration) time.Duration {
	wait := base << attempt
	if wait > max {
		wait = max
	}
	// Pseudo-jitter without shared RNG state.
	jitter := time.Duration(time.Now().UnixNano()%int64(wait/2+1)) - wait/4
	wait += jitter
	if wait < base {
		return base
	}
	return wait
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type chatClient struct {
	provider  string
	model     string
	baseURL   string
	apiKey    string
	maxTokens int
	http      *http.Client
	systemMsg string
}

func (c *chatClient) Provider() string { return c.provider }
func (c *chatClient) Model() string    { return c.model }

func (c *chatClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	body := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.1,
	}
	if c.maxTokens > 0 {
		body["max_tokens"] = c.maxTokens
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm api status %d: %s", resp.StatusCode, string(payload))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", err
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("llm response has no choices")
	}
	return decoded.Choices[0].Message.Content, nil
}

type mockClient struct{}

func (m mockClient) Provider() string { return "mock" }
func (m mockClient) Model() string    { return "deterministic" }

func (m mockClient) Complete(_ context.Context, _, _ string) (string, error) {
	return `{"summary":"Mock AGENT analysis","root_cause":"Likely resource saturation","confidence":0.61,"findings":["High CPU/GPU pressure"],"recommendations":["Inspect top programs","Apply safe playbook action"],"actions":[{"type":"shell","command":"echo agent-mock-remediation","safe":true,"priority":"P3","description":"Emit mock remediation marker"}]}`, nil
}

func newLLMClient(cfg Config, logger *zap.Logger) llmClient {
	provider := canonicalLLMProvider(cfg.Provider)
	if provider == "" {
		provider = "openai"
	}

	switch provider {
	case "mock", "stub", "local_stub":
		logLLMRuntimeSelection(logger, "agent_query", provider, analysis.EnvLLMAPIKey, false, "mock")
		return mockClient{}
	case "jimmynight":
		model := strings.TrimSpace(cfg.Model)
		if model == "" || model == "gpt-4o-mini" {
			model = "jimmynight-sre-default"
		}
		baseURL := strings.TrimSpace(cfg.BaseURL)
		if baseURL == "" {
			baseURL = strings.TrimSpace(os.Getenv("SRE_AGENT_JIMMYNIGHT_BASE_URL"))
		}
		if baseURL == "" {
			logger.Warn("JimmyNight provider selected but no base URL configured; using stub provider")
			logLLMRuntimeSelection(logger, "agent_query", provider, analysis.EnvLLMAPIKey, false, "mock")
			return mockClient{}
		}
		if strings.TrimSpace(cfg.APIKey) == "" {
			logger.Warn("JimmyNight provider selected but API key is missing; using stub provider")
			logLLMRuntimeSelection(logger, "agent_query", provider, analysis.EnvLLMAPIKey, false, "mock")
			return mockClient{}
		}
		logLLMRuntimeSelection(logger, "agent_query", provider, analysis.EnvLLMAPIKey, true, "live")
		return &chatClient{
			provider:  "jimmynight",
			model:     model,
			baseURL:   strings.TrimRight(baseURL, "/"),
			apiKey:    cfg.APIKey,
			maxTokens: cfg.MaxTokens,
			http:      &http.Client{Timeout: cfg.Timeout},
		}
	default:
		client, err := newCanonicalLLMAdapter(provider, cfg.Model, cfg.BaseURL, cfg.APIKey, analysis.EnvLLMAPIKey, "agent_query", cfg.Timeout, logger)
		if err != nil {
			logger.Warn("LLM provider initialization failed; AGENT will use mock client",
				zap.Error(err), zap.String("provider", provider))
			logLLMRuntimeSelection(logger, "agent_query", provider, analysis.EnvLLMAPIKey, strings.TrimSpace(cfg.APIKey) != "", "mock")
			return mockClient{}
		}
		return client
	}
}
