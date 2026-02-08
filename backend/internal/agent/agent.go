package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
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
	// ErrActionNotFound indicates a requested action ID does not exist in pending actions.
	ErrActionNotFound = errors.New("agent action not found")
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

	RateLimitRPS float64 `json:"rate_limit_rps" yaml:"rate_limit_rps"`
	RateBurst    int     `json:"rate_burst" yaml:"rate_burst"`

	CircuitFailures int           `json:"circuit_failures" yaml:"circuit_failures"`
	CircuitCooldown time.Duration `json:"circuit_cooldown" yaml:"circuit_cooldown"`

	PlaybookFile string   `json:"playbook_file" yaml:"playbook_file"`
	DryRun       bool     `json:"dry_run" yaml:"dry_run"`
	AllowUnsafe  bool     `json:"allow_unsafe" yaml:"allow_unsafe"`
	Namespaces   []string `json:"namespaces" yaml:"namespaces"`
	ShellAllowed []string `json:"shell_allowed" yaml:"shell_allowed"`
}

// DefaultConfig returns conservative defaults with OpenAI as the primary provider.
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		Provider:        "openai",
		Model:           "gpt-4o-mini",
		BaseURL:         "https://api.openai.com/v1",
		Timeout:         20 * time.Second,
		MaxRetries:      2,
		RetryBase:       300 * time.Millisecond,
		RetryMax:        4 * time.Second,
		MaxQueryChars:   2048,
		RateLimitRPS:    2,
		RateBurst:       4,
		CircuitFailures: 5,
		CircuitCooldown: 30 * time.Second,
		PlaybookFile:    "./configs/agent_playbooks.yaml",
		DryRun:          true,
		AllowUnsafe:     false,
		Namespaces:      []string{"default"},
		ShellAllowed:    []string{"echo", "kubectl", "systemctl"},
	}
}

// QueryRequest is the API payload for /api/v1/agent/query.
type QueryRequest struct {
	Query  string `json:"query"`
	Node   string `json:"node,omitempty"`
	DryRun *bool  `json:"dry_run,omitempty"`
}

// QueryResponse is the API response for /api/v1/agent/query.
type QueryResponse struct {
	QueryID          string       `json:"query_id"`
	Node             string       `json:"node"`
	Summary          string       `json:"summary"`
	RootCause        string       `json:"root_cause"`
	Confidence       float64      `json:"confidence"`
	Findings         []string     `json:"findings"`
	Recommendations  []string     `json:"recommendations"`
	Actions          []ActionSpec `json:"actions"`
	Provider         string       `json:"provider"`
	Model            string       `json:"model"`
	UsedFallback     bool         `json:"used_fallback"`
	GPUContext       bool         `json:"gpu_context"`
	GeneratedAt      time.Time    `json:"generated_at"`
	TelemetryContext LLMSchema    `json:"telemetry_context"`
}

// ExecuteRequest is the API payload for /api/v1/agent/execute.
type ExecuteRequest struct {
	ActionID string `json:"action_id"`
	DryRun   *bool  `json:"dry_run,omitempty"`
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
	QueriesTotal         uint64
	QueriesSuccessTotal  uint64
	QueriesFailureTotal  uint64
	RateLimitedTotal     uint64
	LLMCallsTotal        uint64
	LLMFailuresTotal     uint64
	ActionsExecutedTotal uint64
	ActionsFailureTotal  uint64
	GPUAnalysisCount     uint64
	GPUAnalysisSumSec    float64
}

// QueryService orchestrates AGENT NL queries, LLM reasoning, and guarded actions.
type QueryService struct {
	cfg     Config
	logger  *zap.Logger
	store   *ingest.MemoryStore
	gpu     *gpuobs.Store
	client  llmClient
	runner  *PlaybookRunner
	limiter *rate.Limiter

	mu      sync.RWMutex
	pending map[string]pendingAction
	cb      circuitBreaker

	queriesTotal         atomic.Uint64
	queriesSuccessTotal  atomic.Uint64
	queriesFailureTotal  atomic.Uint64
	rateLimitedTotal     atomic.Uint64
	llmCallsTotal        atomic.Uint64
	llmFailuresTotal     atomic.Uint64
	actionsExecutedTotal atomic.Uint64
	actionsFailureTotal  atomic.Uint64
	gpuAnalysisCount     atomic.Uint64
	gpuAnalysisNanos     atomic.Uint64
}

type pendingAction struct {
	QueryID   string
	Action    ActionSpec
	CreatedAt time.Time
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

	runner, err := NewPlaybookRunner(runnerCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("new playbook runner: %w", err)
	}

	client := newLLMClient(cfg, logger)
	return &QueryService{
		cfg:     cfg,
		logger:  logger.With(zap.String("component", "agent_query_service")),
		store:   store,
		gpu:     gpu,
		client:  client,
		runner:  runner,
		limiter: rate.NewLimiter(rate.Limit(cfg.RateLimitRPS), cfg.RateBurst),
		pending: make(map[string]pendingAction),
	}, nil
}

// Query analyzes telemetry using LLM plus deterministic playbook suggestions.
func (s *QueryService) Query(ctx context.Context, req QueryRequest) (QueryResponse, error) {
	if !s.limiter.Allow() {
		s.rateLimitedTotal.Add(1)
		return QueryResponse{}, ErrRateLimited
	}

	queryID := newQueryID()
	cleanReq := normalizeQueryRequest(req, s.cfg.MaxQueryChars)
	s.queriesTotal.Add(1)

	promptIn, gpuEnabled := s.buildPromptInput(cleanReq)
	if promptIn.Query == "" {
		s.queriesFailureTotal.Add(1)
		return QueryResponse{}, fmt.Errorf("query is required")
	}

	llmOut, usedFallback := s.runLLM(ctx, promptIn)
	proposed := s.runner.ProposeFromMetrics(promptIn.NodeName, promptIn.Metrics)
	actions := mergeActions(llmOut.Actions, proposed, queryID)
	s.cachePending(queryID, actions)

	response := QueryResponse{
		QueryID:          queryID,
		Node:             promptIn.NodeName,
		Summary:          llmOut.Summary,
		RootCause:        llmOut.RootCause,
		Confidence:       clampConfidence(llmOut.Confidence),
		Findings:         sanitizeStrings(nonEmpty(llmOut.Findings, promptIn.Findings)),
		Recommendations:  sanitizeStrings(llmOut.Recommendations),
		Actions:          actions,
		Provider:         s.client.Provider(),
		Model:            s.client.Model(),
		UsedFallback:     usedFallback,
		GPUContext:       gpuEnabled,
		GeneratedAt:      time.Now().UTC(),
		TelemetryContext: BuildSchema(promptIn),
	}

	s.queriesSuccessTotal.Add(1)
	return response, nil
}

// Execute runs a previously proposed action by ID.
func (s *QueryService) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResponse, error) {
	actionID := strings.TrimSpace(req.ActionID)
	if actionID == "" {
		return ExecuteResponse{}, fmt.Errorf("action_id is required")
	}

	entry, ok := s.loadPending(actionID)
	if !ok {
		return ExecuteResponse{}, ErrActionNotFound
	}

	forceDryRun := req.DryRun != nil && *req.DryRun
	results := s.runner.Execute(ctx, []ActionSpec{entry.Action}, forceDryRun)
	if len(results) == 0 {
		return ExecuteResponse{}, fmt.Errorf("no action result")
	}
	result := results[0]

	if result.Status == ActionResultFailed {
		s.actionsFailureTotal.Add(1)
	} else if result.Status == ActionResultExecuted || result.Status == ActionResultDryRun {
		s.actionsExecutedTotal.Add(1)
	}

	return ExecuteResponse{
		QueryID:    entry.QueryID,
		Action:     entry.Action,
		Result:     result,
		ExecutedAt: time.Now().UTC(),
	}, nil
}

// Metrics returns counters for controller /metrics export.
func (s *QueryService) Metrics() MetricsSnapshot {
	count := s.gpuAnalysisCount.Load()
	sumNanos := s.gpuAnalysisNanos.Load()
	return MetricsSnapshot{
		QueriesTotal:         s.queriesTotal.Load(),
		QueriesSuccessTotal:  s.queriesSuccessTotal.Load(),
		QueriesFailureTotal:  s.queriesFailureTotal.Load(),
		RateLimitedTotal:     s.rateLimitedTotal.Load(),
		LLMCallsTotal:        s.llmCallsTotal.Load(),
		LLMFailuresTotal:     s.llmFailuresTotal.Load(),
		ActionsExecutedTotal: s.actionsExecutedTotal.Load(),
		ActionsFailureTotal:  s.actionsFailureTotal.Load(),
		GPUAnalysisCount:     count,
		GPUAnalysisSumSec:    float64(sumNanos) / float64(time.Second),
	}
}

func (s *QueryService) buildPromptInput(req QueryRequest) (PromptInput, bool) {
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
		s.gpuAnalysisNanos.Add(uint64(time.Since(gpuStart)))
	}

	findings = append(findings, systemFindings(metrics)...)
	anomalies = append(anomalies, trendHints(s.store, nodeName)...)
	if len(findings) == 0 {
		findings = append(findings, "No critical anomalies detected")
	}

	return PromptInput{
		Query:      req.Query,
		NodeName:   nodeName,
		Generated:  time.Now().UTC(),
		Metrics:    metrics,
		Trends:     metricTrends(s.store, nodeName),
		Findings:   dedupeStrings(findings),
		Anomalies:  dedupeStrings(anomalies),
		Processes:  processes,
		Logs:       logs,
		ContextTag: strings.Join(contextBits, "; "),
	}, gpuEnabled
}

func (s *QueryService) runLLM(ctx context.Context, in PromptInput) (llmPayload, bool) {
	systemPrompt := BuildSystemPrompt()
	userPrompt := BuildUserPrompt(in)

	payload, err := s.callLLMWithRetry(ctx, systemPrompt, userPrompt)
	if err == nil {
		return payload, false
	}

	s.logger.Warn("llm call failed, using deterministic fallback", zap.Error(err))
	return fallbackPayload(in, err), true
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

func (s *QueryService) cachePending(queryID string, actions []ActionSpec) {
	if len(actions) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, action := range actions {
		s.pending[action.ID] = pendingAction{
			QueryID:   queryID,
			Action:    action,
			CreatedAt: time.Now().UTC(),
		}
	}
}

func (s *QueryService) loadPending(actionID string) (pendingAction, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.pending[actionID]
	return entry, ok
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
		Recommendations: []string{"Validate top processes and recent deploys", "Apply safe playbook actions first", fmt.Sprintf("LLM fallback reason: %v", err)},
		Actions:         nil,
	}
}

func mergeActions(primary []ActionSpec, secondary []ActionSpec, queryID string) []ActionSpec {
	all := append([]ActionSpec{}, primary...)
	all = append(all, secondary...)
	all = dedupeActionSpecs(all)
	out := make([]ActionSpec, 0, len(all))
	for i, action := range all {
		action = normalizeAction(action)
		if action.ID == "" {
			action.ID = fmt.Sprintf("%s-a%d", queryID, i+1)
		}
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
