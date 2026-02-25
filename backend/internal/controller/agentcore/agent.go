package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
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

	Provider string `json:"provider" yaml:"provider"`
	Model    string `json:"model" yaml:"model"`
	BaseURL  string `json:"base_url" yaml:"base_url"`
	APIKey   string `json:"api_key" yaml:"api_key"`

	Timeout       time.Duration `json:"timeout" yaml:"timeout"`
	MaxRetries    int           `json:"max_retries" yaml:"max_retries"`
	RetryBase     time.Duration `json:"retry_base" yaml:"retry_base"`
	RetryMax      time.Duration `json:"retry_max" yaml:"retry_max"`
	MaxQueryChars int           `json:"max_query_chars" yaml:"max_query_chars"`

	RateLimitRPS         float64 `json:"rate_limit_rps" yaml:"rate_limit_rps"`
	RateBurst            int     `json:"rate_burst" yaml:"rate_burst"`
	MaxConcurrentQueries int     `json:"max_concurrent_queries" yaml:"max_concurrent_queries"`

	CircuitFailures int           `json:"circuit_failures" yaml:"circuit_failures"`
	CircuitCooldown time.Duration `json:"circuit_cooldown" yaml:"circuit_cooldown"`

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
		Timeout:                   20 * time.Second,
		MaxRetries:                2,
		RetryBase:                 300 * time.Millisecond,
		RetryMax:                  4 * time.Second,
		MaxQueryChars:             2048,
		RateLimitRPS:              2,
		RateBurst:                 4,
		MaxConcurrentQueries:      16,
		CircuitFailures:           5,
		CircuitCooldown:           30 * time.Second,
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
	Metrics             int     `json:"metrics"`
	Processes           int     `json:"processes"`
	Logs                int     `json:"logs"`
	Findings            int     `json:"findings"`
	Anomalies           int     `json:"anomalies"`
	TelemetryAgeSeconds float64 `json:"telemetry_age_seconds"`
	TelemetryStale      bool    `json:"telemetry_stale"`
}

// QueryResponse is the API response for /api/v1/agent/query.
type QueryResponse struct {
	QueryID           string         `json:"query_id"`
	Node              string         `json:"node"`
	Summary           string         `json:"summary"`
	RootCause         string         `json:"root_cause"`
	Confidence        float64        `json:"confidence"`
	Findings          []string       `json:"findings"`
	Recommendations   []string       `json:"recommendations"`
	Actions           []ActionSpec   `json:"actions"`
	Provider          string         `json:"provider"`
	Model             string         `json:"model"`
	UsedFallback      bool           `json:"used_fallback"`
	FallbackReason    string         `json:"fallback_reason,omitempty"`
	GPUContext        bool           `json:"gpu_context"`
	GeneratedAt       time.Time      `json:"generated_at"`
	ActionsExpireAt   time.Time      `json:"actions_expire_at,omitempty"`
	ActionsSuppressed bool           `json:"actions_suppressed"`
	SuppressionReason string         `json:"suppression_reason,omitempty"`
	Explainability    Explainability `json:"explainability"`
	TelemetryContext  LLMSchema      `json:"telemetry_context"`
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

// QueryService orchestrates AGENT NL queries, LLM reasoning, and guarded actions.
type QueryService struct {
	cfg        Config
	logger     *zap.Logger
	store      *ingest.MemoryStore
	gpu        *gpuobs.Store
	client     llmClient
	runner     *PlaybookRunner
	limiter    *rate.Limiter
	eventHTTP  *http.Client
	querySlots chan struct{}

	mu      sync.RWMutex
	pending map[string]pendingAction
	cb      circuitBreaker

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

	client := newLLMClient(cfg, logger)
	return &QueryService{
		cfg:        cfg,
		logger:     logger.With(zap.String("component", "agent_query_service")),
		store:      store,
		gpu:        gpu,
		client:     client,
		runner:     runner,
		limiter:    rate.NewLimiter(rate.Limit(cfg.RateLimitRPS), cfg.RateBurst),
		eventHTTP:  &http.Client{Timeout: cfg.EventWebhookTimeout},
		querySlots: make(chan struct{}, cfg.MaxConcurrentQueries),
		pending:    make(map[string]pendingAction),
	}, nil
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
	noTelemetry := s.isTelemetryInsufficient(promptIn)
	if noTelemetry {
		promptIn.Findings = dedupeStrings(append(promptIn.Findings, "Telemetry snapshot is insufficient (no metrics/processes/logs)"))
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
		llmOut, usedFallback, fallbackReason = s.runLLM(ctx, promptIn)
	}
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
		QueryID:           queryID,
		Node:              promptIn.NodeName,
		Summary:           llmOut.Summary,
		RootCause:         llmOut.RootCause,
		Confidence:        clampConfidence(llmOut.Confidence),
		Findings:          sanitizeStrings(nonEmpty(llmOut.Findings, promptIn.Findings)),
		Recommendations:   recommendations,
		Actions:           actions,
		Provider:          s.client.Provider(),
		Model:             s.client.Model(),
		UsedFallback:      usedFallback,
		FallbackReason:    fallbackReason,
		GPUContext:        gpuEnabled,
		GeneratedAt:       time.Now().UTC(),
		ActionsExpireAt:   actionsExpireAt,
		ActionsSuppressed: actionsSuppressed,
		SuppressionReason: suppressionReason,
		Explainability:    explainability,
		TelemetryContext:  BuildSchema(promptIn),
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
	telemetryAgeSec := 0.0
	processes := []PromptProcess{}
	logs := []PromptLog{}
	contextBits := []string{"Push-first in-memory snapshots from ingest store"}
	findings := []string{}
	anomalies := []string{}

	if node != nil {
		metrics = cloneMetricMap(node.Metrics)
		nodeName = firstNonEmpty(node.CollectorID, node.Hostname)
		if !node.UpdatedAt.IsZero() {
			telemetryAgeSec = now.Sub(node.UpdatedAt).Seconds()
			if telemetryAgeSec < 0 {
				telemetryAgeSec = 0
			}
		}
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
		s.gpuAnalysisNanos.Add(uint64(time.Since(gpuStart)))
	}

	findings = append(findings, systemFindings(metrics)...)
	anomalies = append(anomalies, trendHints(s.store, nodeName)...)
	if len(findings) == 0 {
		findings = append(findings, "No critical anomalies detected")
	}

	return PromptInput{
		Query:               req.Query,
		NodeName:            nodeName,
		Generated:           now,
		TelemetryAgeSeconds: telemetryAgeSec,
		Metrics:             metrics,
		Trends:              metricTrends(s.store, nodeName),
		Findings:            dedupeStrings(findings),
		Anomalies:           dedupeStrings(anomalies),
		Processes:           processes,
		Logs:                logs,
		ContextTag:          strings.Join(contextBits, "; "),
	}, gpuEnabled
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
		cfg.APIKey = firstNonEmpty(
			os.Getenv("SRE_AGENT_LLM_API_KEY"),
			os.Getenv("OPENAI_API_KEY"),
			os.Getenv("LLM_API_KEY"),
		)
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
	var tempMax float64
	var processCount float64
	idx := 0
	for _, gpu := range node.GPUs {
		utilSum += gpu.UtilSMPercent
		memUsedSum += gpu.MemUsedMiB
		memTotalSum += gpu.MemTotalMiB
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
	metrics["node_gpu_temperature_peak_celsius"] = tempMax
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

func trendHints(store *ingest.MemoryStore, collectorID string) []string {
	if store == nil || collectorID == "" {
		return nil
	}
	samples := store.MetricHistory(collectorID, time.Now().Add(-30*time.Minute), 12)
	if len(samples) < 3 {
		return nil
	}
	first := samples[0].Metrics["node_gpu_utilization_sm_avg_percent"]
	last := samples[len(samples)-1].Metrics["node_gpu_utilization_sm_avg_percent"]
	if last-first >= 15 {
		return []string{"GPU utilization trend is rising over the last 30m"}
	}
	return nil
}

func metricTrends(store *ingest.MemoryStore, collectorID string) map[string]string {
	if store == nil || collectorID == "" {
		return nil
	}
	samples := store.MetricHistory(collectorID, time.Now().Add(-30*time.Minute), 20)
	if len(samples) < 3 {
		return nil
	}
	trends := map[string]string{}
	evaluate := func(metric string) {
		start := samples[0].Metrics[metric]
		end := samples[len(samples)-1].Metrics[metric]
		delta := end - start
		switch {
		case delta > 5:
			trends[metric] = "rising"
		case delta < -5:
			trends[metric] = "falling"
		default:
			trends[metric] = "stable"
		}
	}
	evaluate("node_cpu_usage_percent")
	evaluate("node_gpu_utilization_sm_avg_percent")
	if len(trends) == 0 {
		return nil
	}
	return trends
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
			Metrics:             len(in.Metrics),
			Processes:           len(in.Processes),
			Logs:                len(in.Logs),
			Findings:            len(in.Findings),
			Anomalies:           len(in.Anomalies),
			TelemetryAgeSeconds: in.TelemetryAgeSeconds,
			TelemetryStale:      in.TelemetryStale,
		},
		UsedDeterministicFallback: usedFallback,
	}
}

func fallbackPayload(in PromptInput, err error) llmPayload {
	rootCause := "Insufficient LLM data"
	if len(in.Findings) > 0 {
		rootCause = in.Findings[0]
	}
	return llmPayload{
		Summary:         "Deterministic fallback analysis generated",
		RootCause:       rootCause,
		Confidence:      0.55,
		Findings:        in.Findings,
		Recommendations: []string{"Validate top processes and recent deploys", "Apply safe playbook actions first"},
		Actions:         nil,
		Evidence:        in.Findings,
		Limitations:     []string{fmt.Sprintf("LLM fallback reason: %v", err)},
	}
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
	return payload, nil
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
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "openai"
	}

	switch provider {
	case "mock":
		return mockClient{}
	case "ollama", "local":
		baseURL := cfg.BaseURL
		if baseURL == "" || strings.Contains(baseURL, "openai.com") {
			baseURL = "http://localhost:11434/v1"
		}
		model := cfg.Model
		if model == "" {
			model = "llama2"
		}
		return &chatClient{
			provider: "ollama",
			model:    model,
			baseURL:  strings.TrimRight(baseURL, "/"),
			http:     &http.Client{Timeout: cfg.Timeout},
		}
	default:
		if cfg.APIKey == "" {
			logger.Warn("LLM key is missing; AGENT will use mock client")
			return mockClient{}
		}
		model := cfg.Model
		if model == "" {
			model = "gpt-4o-mini"
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return &chatClient{
			provider: "openai",
			model:    model,
			baseURL:  strings.TrimRight(baseURL, "/"),
			apiKey:   cfg.APIKey,
			http:     &http.Client{Timeout: cfg.Timeout},
		}
	}
}
