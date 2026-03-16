package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidents"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/predictive"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"go.uber.org/zap"
)

type Config struct {
	Enabled                  bool           `yaml:"enabled"`
	Interval                 time.Duration  `yaml:"interval"`
	MaxReports               int            `yaml:"max_reports"`
	MaxActions               int            `yaml:"max_actions"`
	PredictHorizon           time.Duration  `yaml:"predict_horizon"`
	SuppressUnchangedReports bool           `yaml:"suppress_unchanged_reports"`
	ReportRefreshInterval    time.Duration  `yaml:"report_refresh_interval"`
	PredictiveLogCooldown    time.Duration  `yaml:"predictive_log_cooldown"`
	PersistDir               string         `yaml:"persist_dir"`
	RAGEnabled               bool           `yaml:"rag_enabled"`
	RAGPaths                 []string       `yaml:"rag_paths"`
	RAGSourcePaths           []string       `yaml:"rag_source_paths"`
	RAGDatasetPath           string         `yaml:"rag_dataset_path"`
	RAGIndexPath             string         `yaml:"rag_index_path"`
	RAGTopK                  int            `yaml:"rag_top_k"`
	RAGMaxSnippets           int            `yaml:"rag_max_snippets"`
	RAGMaxChars              int            `yaml:"rag_max_chars"`
	RAGMaxQueryChars         int            `yaml:"rag_max_query_chars"`
	RAGMaxFindings           int            `yaml:"rag_max_findings"`
	RAGMinConfidence         float64        `yaml:"rag_min_confidence"`
	RAGChunkSize             int            `yaml:"rag_chunk_size"`
	RAGChunkOverlap          int            `yaml:"rag_chunk_overlap"`
	RAGChunkStrategy         string         `yaml:"rag_chunk_strategy"`
	RAGRetrievalMode         string         `yaml:"rag_retrieval_mode"`
	RAGEmbeddingProvider     string         `yaml:"rag_embedding_provider"`
	RAGEmbeddingModel        string         `yaml:"rag_embedding_model"`
	RAGEmbeddingBaseURL      string         `yaml:"rag_embedding_base_url"`
	RAGEmbeddingAPIKey       string         `yaml:"rag_embedding_api_key"`
	RAGVectorBackend         string         `yaml:"rag_vector_backend"`
	RAGVectorEndpoint        string         `yaml:"rag_vector_endpoint"`
	RAGVectorCollection      string         `yaml:"rag_vector_collection"`
	RAGVectorDatabase        string         `yaml:"rag_vector_database"`
	RAGVectorToken           string         `yaml:"rag_vector_token"`
	RAGVectorTimeout         time.Duration  `yaml:"rag_vector_timeout"`
	RAGRebuildPolicy         string         `yaml:"rag_rebuild_policy"`
	LLMEnabled               bool           `yaml:"llm_enabled"`
	LLMTimeout               time.Duration  `yaml:"llm_timeout"`
	PolicyFile               string         `yaml:"policy_file"`
	Playbooks                []PlaybookRule `yaml:"playbooks"`
	Signals                  SignalRules    `yaml:"signals"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:                  true,
		Interval:                 30 * time.Second,
		MaxReports:               50,
		MaxActions:               200,
		PredictHorizon:           30 * time.Minute,
		SuppressUnchangedReports: true,
		ReportRefreshInterval:    3 * time.Minute,
		PredictiveLogCooldown:    5 * time.Minute,
		PersistDir:               "./data/agent",
		RAGEnabled:               false,
		RAGPaths:                 []string{},
		RAGDatasetPath:           "./dataset",
		RAGIndexPath:             "./data/agent/rag/index.json",
		RAGTopK:                  4,
		RAGMaxSnippets:           4,
		RAGMaxChars:              1200,
		RAGMaxQueryChars:         640,
		RAGMaxFindings:           6,
		RAGMinConfidence:         0.18,
		RAGChunkSize:             900,
		RAGChunkOverlap:          120,
		RAGChunkStrategy:         "auto",
		RAGRetrievalMode:         "hybrid",
		RAGEmbeddingProvider:     "local",
		RAGEmbeddingModel:        "local-hash-64",
		RAGVectorBackend:         "local",
		RAGVectorCollection:      "ai_sre_agent_knowledge",
		RAGVectorTimeout:         5 * time.Second,
		RAGRebuildPolicy:         "manual",
		LLMEnabled:               false,
		LLMTimeout:               20 * time.Second,
		PolicyFile:               "./configs/agent_playbooks.yaml",
		Signals: SignalRules{
			CPUHighPercent:        85,
			MemoryPressureRatio:   0.85,
			SwapActivityMin:       1,
			DiskIOHigh:            50,
			NetSaturationBytesSec: 200_000_000,
			GPUSMHighPercent:      85,
		},
	}
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

type Report struct {
	ID          string                       `json:"id"`
	NodeName    string                       `json:"node_name"`
	GeneratedAt time.Time                    `json:"generated_at"`
	Summary     string                       `json:"summary"`
	Findings    []string                     `json:"findings"`
	Forecasts   []string                     `json:"forecasts"`
	Predictions []predictive.Finding         `json:"predictions,omitempty"`
	Actions     []ActionDecision             `json:"actions"`
	Evidence    analysis.EvidencePack        `json:"evidence"`
	RCAs        []analysis.RootCauseAnalysis `json:"rcas"`
	LLM         *LLMInsight                  `json:"llm,omitempty"`
}

type ActionDecision struct {
	NodeName string    `json:"node_name"`
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Reason   string    `json:"reason"`
	Priority string    `json:"priority"`
	Safe     bool      `json:"safe"`
	Status   string    `json:"status"`
	Note     string    `json:"note,omitempty"`
	Created  time.Time `json:"created_at"`
	Updated  time.Time `json:"updated_at"`
}

type LLMInsight struct {
	Summary         string   `json:"summary"`
	RootCause       string   `json:"root_cause"`
	Confidence      float64  `json:"confidence"`
	Recommendations []string `json:"recommendations"`
	ContextSnippets []string `json:"context_snippets,omitempty"`
}

type ReportEngineStatus struct {
	Enabled                      bool   `json:"enabled"`
	SuppressUnchangedReports     bool   `json:"suppress_unchanged_reports"`
	ReportRefreshInterval        string `json:"report_refresh_interval"`
	PredictiveLogCooldown        string `json:"predictive_log_cooldown"`
	ReportsStored                int    `json:"reports_stored"`
	ActionsStored                int    `json:"actions_stored"`
	ReportSuppressedTotal        uint64 `json:"report_suppressed_total"`
	ReportRefreshedTotal         uint64 `json:"report_refreshed_total"`
	PredictiveLogSuppressedTotal uint64 `json:"predictive_log_suppressed_total"`
	ActionDryRunTotal            uint64 `json:"action_dry_run_total"`
	ActionExecuteTotal           uint64 `json:"action_execute_total"`
	ActionBlockedTotal           uint64 `json:"action_blocked_total"`
}

type Engine struct {
	cfg      Config
	logger   *zap.Logger
	store    *ingest.MemoryStore
	analysis *analysis.Engine
	llm      *analysis.LLMClient
	policies []PlaybookRule

	mu      sync.RWMutex
	reports map[string][]Report
	actions map[string]ActionDecision
	// Incident contexts and assessments keyed by alert ID
	incidentContexts             map[string]incidents.AggregatedContext
	incidentAssessments          map[string]IncidentAssessment
	incidentActionAudits         map[string][]IncidentActionAuditRecord
	incidentActionApprovals      map[string]IncidentActionApprovalRecord
	predictiveLogSeen            map[string]time.Time
	reportSuppressedTotal        uint64
	reportRefreshedTotal         uint64
	predictiveLogSuppressedTotal uint64
	actionDryRunTotal            uint64
	actionExecuteTotal           uint64
	actionBlockedTotal           uint64
	ctx                          context.Context
	cancel                       context.CancelFunc
	running                      bool

	persist Store
	rag     rag.KnowledgeBase
}

func (e *Engine) SetKnowledgeBase(kb rag.KnowledgeBase) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rag = kb
}

func New(cfg Config, store *ingest.MemoryStore, analysisEngine *analysis.Engine, logger *zap.Logger) *Engine {
	if logger == nil {
		logger, _ = zap.NewProduction()
	}
	def := DefaultConfig()
	if cfg.Interval <= 0 {
		cfg.Interval = def.Interval
	}
	if cfg.MaxReports <= 0 {
		cfg.MaxReports = def.MaxReports
	}
	if cfg.MaxActions <= 0 {
		cfg.MaxActions = def.MaxActions
	}
	if cfg.PredictHorizon <= 0 {
		cfg.PredictHorizon = def.PredictHorizon
	}
	if cfg.ReportRefreshInterval <= 0 {
		cfg.ReportRefreshInterval = def.ReportRefreshInterval
	}
	if cfg.PredictiveLogCooldown <= 0 {
		cfg.PredictiveLogCooldown = def.PredictiveLogCooldown
	}
	return &Engine{
		cfg:                     cfg,
		logger:                  logger.With(zap.String("component", "agent_engine")),
		store:                   store,
		analysis:                analysisEngine,
		reports:                 make(map[string][]Report),
		actions:                 make(map[string]ActionDecision),
		incidentContexts:        make(map[string]incidents.AggregatedContext),
		incidentAssessments:     make(map[string]IncidentAssessment),
		incidentActionAudits:    make(map[string][]IncidentActionAuditRecord),
		incidentActionApprovals: make(map[string]IncidentActionApprovalRecord),
		predictiveLogSeen:       make(map[string]time.Time),
	}
}

func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("agent engine already running")
	}
	e.running = true
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.mu.Unlock()

	e.initPersist()
	e.initRAG()
	e.initLLM()
	e.loadPersisted()
	e.loadPolicies()

	e.logger.Info("agent engine started", zap.Duration("interval", e.cfg.Interval))
	go e.loop()
	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return nil
	}
	e.running = false
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *Engine) loop() {
	ticker := time.NewTicker(e.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.generateAll()
		}
	}
}

func (e *Engine) generateAll() {
	if e.store == nil {
		return
	}
	nodes := e.store.Snapshot()
	for _, node := range nodes {
		report, ok := e.GenerateReport(node.CollectorID)
		if !ok {
			continue
		}
		e.storeReport(report)
	}
}

func (e *Engine) GenerateReport(nodeName string) (Report, bool) {
	if e.store == nil {
		return Report{}, false
	}
	snapshot := e.store.Node(nodeName)
	if snapshot == nil {
		return Report{}, false
	}

	var metrics map[string]float64
	var rcas []analysis.RootCauseAnalysis
	if e.analysis != nil {
		metrics = e.analysis.GetNodeMetricsSnapshot(nodeName)
		rcas = e.analysis.GetRCAs()
	}
	if metrics == nil {
		metrics = snapshot.Metrics
	}

	processes := analysis.SummarizeProcesses(snapshot.Processes, 5)
	logs := analysis.SummarizeLogs(snapshot.Logs, 5)
	evidence := analysis.BuildEvidencePack(nodeName, metrics, nil, nil, "agent report", processes, logs)
	now := time.Now()

	historyWindow := maxDuration(e.cfg.PredictHorizon*2, 30*time.Minute)
	historyLimit := maxPositiveInt(120, int(historyWindow/maxDuration(e.cfg.Interval, 5*time.Second))+4)
	history := e.store.MetricHistory(nodeName, time.Now().Add(-historyWindow), historyLimit)

	findings, forecasts, predictions := analyzeSignals(nodeName, metrics, history, e.cfg.Signals, e.cfg.PredictHorizon)
	if protectionFinding := monitoringProtectionFinding(metrics); protectionFinding != "" {
		findings = mergeUniqueStrings(findings, []string{protectionFinding})
	}
	for _, prediction := range predictions {
		if !e.shouldLogPredictiveFinding(now, prediction) {
			continue
		}
		e.logger.Info("predictive early warning",
			zap.String("prediction_id", prediction.PredictionID),
			zap.String("asset_id", prediction.AssetID),
			zap.String("metric", prediction.Metric),
			zap.String("predictive_slo", prediction.PredictiveSLO),
			zap.String("hazard_class", prediction.HazardClass),
			zap.String("control_reference", prediction.ControlReference),
			zap.String("algorithm_version", prediction.AlgorithmVersion),
			zap.String("severity", prediction.Severity),
			zap.Float64("confidence", prediction.Confidence),
			zap.String("audit_hash", prediction.AuditHash),
		)
	}

	policyActions := applyPolicies(nodeName, metrics, e.policies, now)
	actions := policyActions
	if len(actions) == 0 {
		actions = planActions(nodeName, findings, forecasts, now)
	}

	summary := "Normal"
	if len(findings) > 0 {
		summary = findings[0]
	}

	var llmInsight *LLMInsight
	if shouldDeferExpensiveAnalysis(metrics) {
		forecasts = mergeUniqueStrings(forecasts, []string{
			"Deferred RAG/LLM enrichment while the collector is shedding optional work to protect the host.",
		})
	} else {
		llmInsight = e.llmInsight(nodeName, metrics, evidence, findings, forecasts)
	}
	if llmInsight != nil && llmInsight.Summary != "" {
		summary = llmInsight.Summary
	}

	report := Report{
		ID:          fmt.Sprintf("report-%s-%d", nodeName, now.UnixNano()),
		NodeName:    nodeName,
		GeneratedAt: now,
		Summary:     summary,
		Findings:    findings,
		Forecasts:   forecasts,
		Predictions: predictions,
		Actions:     actions,
		Evidence:    evidence,
		RCAs:        filterRCAs(rcas, nodeName),
		LLM:         llmInsight,
	}

	return report, true
}

func (e *Engine) Reports(nodeName string) []Report {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Report, 0)
	if nodeName == "" {
		for _, list := range e.reports {
			out = append(out, list...)
		}
	} else {
		out = append(out, e.reports[nodeName]...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GeneratedAt.After(out[j].GeneratedAt)
	})
	return out
}

func (e *Engine) LatestReport(nodeName string) (Report, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if nodeName == "" {
		return Report{}, false
	}
	list := e.reports[nodeName]
	if len(list) == 0 {
		return Report{}, false
	}
	return list[len(list)-1], true
}

func (e *Engine) LatestReports(limit int) []Report {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Report, 0)
	for _, list := range e.reports {
		if len(list) > 0 {
			out = append(out, list[len(list)-1])
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GeneratedAt.After(out[j].GeneratedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (e *Engine) Actions(nodeName string) []ActionDecision {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]ActionDecision, 0, len(e.actions))
	for _, action := range e.actions {
		if nodeName == "" || action.NodeName == nodeName {
			out = append(out, action)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}

func (e *Engine) UpdateAction(id, status, note string) (ActionDecision, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	action, ok := e.actions[id]
	if !ok {
		return ActionDecision{}, false
	}
	if status != "" {
		action.Status = NormalizeActionStatus(status)
	}
	if note != "" {
		action.Note = note
	}
	action.Updated = time.Now()
	e.actions[id] = action
	if e.persist != nil {
		_ = e.persist.UpdateAction(id, action.Status, action.Note)
	}
	return action, true
}

func analyzeSignals(assetID string, metrics map[string]float64, history []ingest.MetricHistorySample, rules SignalRules, horizon time.Duration) ([]string, []string, []predictive.Finding) {
	findings := make([]string, 0, 4)
	forecasts := make([]string, 0, 4)
	predictions := predictive.Evaluate(assetID, metrics, history, predictive.DefaultOptions(
		horizon,
		rules.CPUHighPercent,
		rules.MemoryPressureRatio,
		0.01,
		rules.GPUSMHighPercent,
	))
	predictiveFindings, predictiveForecasts := predictive.Summaries(predictions)

	cpu := metrics["node_cpu_usage_percent"]
	if rules.CPUHighPercent > 0 && cpu > rules.CPUHighPercent {
		findings = append(findings, "High CPU utilization")
		forecasts = append(forecasts, fmt.Sprintf("If CPU remains above %.0f%%, latency risk within %s", rules.CPUHighPercent, horizon))
	}

	memUsed := metrics["node_memory_Used_bytes"]
	memTotal := metrics["node_memory_MemTotal_bytes"]
	if memTotal > 0 && rules.MemoryPressureRatio > 0 && memUsed/memTotal > rules.MemoryPressureRatio {
		findings = append(findings, "High memory utilization")
		forecasts = append(forecasts, fmt.Sprintf("Memory pressure likely within %s", horizon))
	}

	swapOut := metrics["node_vmstat_pswpout"]
	if swapOut > rules.SwapActivityMin {
		findings = append(findings, "Swap activity detected")
	}

	diskIO := metrics["node_disk_io_now"]
	if diskIO > rules.DiskIOHigh {
		findings = append(findings, "High disk IO in progress")
	}

	netRx := metrics["node_network_receive_bytes_per_second"]
	netTx := metrics["node_network_transmit_bytes_per_second"]
	if (rules.NetSaturationBytesSec > 0 && netRx > rules.NetSaturationBytesSec) ||
		(rules.NetSaturationBytesSec > 0 && netTx > rules.NetSaturationBytesSec) {
		findings = append(findings, "Network throughput saturated")
	}

	gpuUtil := metrics["node_gpu_utilization_sm_avg_percent"]
	if rules.GPUSMHighPercent > 0 && gpuUtil > rules.GPUSMHighPercent {
		findings = append(findings, "GPU saturation detected")
	}

	findings = mergeUniqueStrings(findings, predictiveFindings)
	forecasts = mergeUniqueStrings(forecasts, predictiveForecasts)

	if len(findings) == 0 {
		findings = append(findings, "No critical anomalies detected")
	}

	return findings, forecasts, predictions
}

func planActions(nodeName string, findings []string, forecasts []string, now time.Time) []ActionDecision {
	actions := make([]ActionDecision, 0, 4)
	newActionID := func(existing int) string {
		return fmt.Sprintf("action-%d-%d", now.UnixNano(), existing)
	}

	for _, finding := range findings {
		switch finding {
		case "High CPU utilization":
			actions = append(actions, ActionDecision{
				NodeName: nodeName,
				ID:       newActionID(len(actions)),
				Type:     "scale-out-suggest",
				Reason:   "CPU saturation",
				Priority: "high",
				Safe:     true,
				Status:   ActionStatusProposed,
				Created:  now,
				Updated:  now,
			})
		case "High memory utilization":
			actions = append(actions, ActionDecision{
				NodeName: nodeName,
				ID:       newActionID(len(actions)),
				Type:     "restart-leak-suspect",
				Reason:   "Memory pressure",
				Priority: "medium",
				Safe:     true,
				Status:   ActionStatusProposed,
				Created:  now,
				Updated:  now,
			})
		case "Memory exhaustion risk rising":
			actions = append(actions, ActionDecision{
				NodeName: nodeName,
				ID:       newActionID(len(actions)),
				Type:     "inspect-memory-headroom",
				Reason:   "Predictive memory pressure",
				Priority: "high",
				Safe:     true,
				Status:   ActionStatusProposed,
				Created:  now,
				Updated:  now,
			})
		case "GPU thermal runaway risk detected":
			actions = append(actions, ActionDecision{
				NodeName: nodeName,
				ID:       newActionID(len(actions)),
				Type:     "inspect-gpu-cooling",
				Reason:   "Predictive GPU thermal drift",
				Priority: "high",
				Safe:     true,
				Status:   ActionStatusProposed,
				Created:  now,
				Updated:  now,
			})
		case "GPU power envelope risk rising":
			actions = append(actions, ActionDecision{
				NodeName: nodeName,
				ID:       newActionID(len(actions)),
				Type:     "review-gpu-power-policy",
				Reason:   "Predictive GPU power anomaly",
				Priority: "medium",
				Safe:     true,
				Status:   ActionStatusProposed,
				Created:  now,
				Updated:  now,
			})
		case "PCIe saturation risk rising":
			actions = append(actions, ActionDecision{
				NodeName: nodeName,
				ID:       newActionID(len(actions)),
				Type:     "inspect-pcie-feed-path",
				Reason:   "Predictive PCIe pressure",
				Priority: "medium",
				Safe:     true,
				Status:   ActionStatusProposed,
				Created:  now,
				Updated:  now,
			})
		case "Network jitter risk rising":
			actions = append(actions, ActionDecision{
				NodeName: nodeName,
				ID:       newActionID(len(actions)),
				Type:     "inspect-network-fabric",
				Reason:   "Predictive network jitter",
				Priority: "medium",
				Safe:     true,
				Status:   ActionStatusProposed,
				Created:  now,
				Updated:  now,
			})
		case "IO pressure risk rising":
			actions = append(actions, ActionDecision{
				NodeName: nodeName,
				ID:       newActionID(len(actions)),
				Type:     "inspect-storage-path",
				Reason:   "Predictive IO pressure",
				Priority: "medium",
				Safe:     true,
				Status:   ActionStatusProposed,
				Created:  now,
				Updated:  now,
			})
		}
	}

	if len(actions) == 0 && len(forecasts) > 0 {
		actions = append(actions, ActionDecision{
			NodeName: nodeName,
			ID:       newActionID(len(actions)),
			Type:     "capacity-review",
			Reason:   "Forecasted risk",
			Priority: "low",
			Safe:     true,
			Status:   ActionStatusProposed,
			Created:  now,
			Updated:  now,
		})
	}

	return actions
}

func monitoringProtectionFinding(metrics map[string]float64) string {
	if shouldDeferExpensiveAnalysis(metrics) {
		return "Monitoring agent is load-shedding to protect the host"
	}
	if metrics["collector_protection_mode_severity"] >= 1 {
		return "Monitoring agent detected rising host pressure"
	}
	return ""
}

func shouldDeferExpensiveAnalysis(metrics map[string]float64) bool {
	if len(metrics) == 0 {
		return false
	}
	if metrics["collector_protection_mode_severity"] >= 2 {
		return true
	}
	if metrics["collector_protection_signal_pressure"] >= 3 {
		return true
	}
	if metrics["collector_protection_spool_fill_ratio"] >= 0.5 {
		return true
	}
	if metrics["collector_protection_cpu_budget_ratio"] >= 1.0 {
		return true
	}
	if metrics["collector_protection_memory_budget_ratio"] >= 1.0 {
		return true
	}
	return false
}

func mergeUniqueStrings(base []string, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	for _, item := range base {
		if item == "" {
			continue
		}
		seen[item] = struct{}{}
	}
	for _, item := range extra {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		base = append(base, item)
		seen[item] = struct{}{}
	}
	return base
}

func maxDuration(values ...time.Duration) time.Duration {
	var out time.Duration
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}

func filterRCAs(rcas []analysis.RootCauseAnalysis, nodeName string) []analysis.RootCauseAnalysis {
	if len(rcas) == 0 {
		return nil
	}
	out := make([]analysis.RootCauseAnalysis, 0)
	for _, rca := range rcas {
		if rca.NodeName == nodeName {
			out = append(out, rca)
		}
	}
	return out
}

func (e *Engine) initPersist() {
	if e.cfg.PersistDir == "" {
		return
	}
	store, err := NewFileStore(e.cfg.PersistDir, e.logger)
	if err != nil {
		e.logger.Warn("failed to initialize agent store", zap.Error(err))
		return
	}
	e.persist = store
}

func (e *Engine) loadPersisted() {
	if e.persist == nil {
		return
	}
	reports, _ := e.persist.LoadReports()
	actions, _ := e.persist.LoadActions()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, report := range reports {
		list := append(e.reports[report.NodeName], report)
		if len(list) > e.cfg.MaxReports {
			list = list[len(list)-e.cfg.MaxReports:]
		}
		e.reports[report.NodeName] = list
	}
	for _, action := range actions {
		e.actions[action.ID] = action
	}
}

func (e *Engine) loadPolicies() {
	rules := e.cfg.Playbooks
	if e.cfg.PolicyFile != "" {
		policyPath := resolvePolicyFilePath(e.cfg.PolicyFile)
		if loaded, err := loadPlaybooks(policyPath); err == nil {
			rules = loaded
		} else {
			e.logger.Warn("failed to load policy file",
				zap.String("configured_path", e.cfg.PolicyFile),
				zap.String("resolved_path", policyPath),
				zap.Error(err))
		}
	}
	e.policies = rules
	if len(rules) > 0 {
		e.logger.Info("agent playbooks loaded", zap.Int("count", len(rules)))
	}
}

// resolvePolicyFilePath keeps policy loading resilient when the process starts
// from subdirectories (for example `backend/` during tests).
func resolvePolicyFilePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	candidates := []string{filepath.Clean(path)}
	if !filepath.IsAbs(path) {
		trimmed := strings.TrimPrefix(filepath.Clean(path), "."+string(filepath.Separator))
		if trimmed != "" {
			parent := "."
			for i := 0; i < 6; i++ {
				parent = filepath.Join(parent, "..")
				candidates = append(candidates, filepath.Clean(filepath.Join(parent, trimmed)))
			}
		}
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
	}
	return path
}

func (e *Engine) initRAG() {
	if e.rag != nil {
		return
	}
	if !e.cfg.RAGEnabled {
		return
	}
	ragCfg := rag.DefaultConfig()
	ragCfg.Enabled = true
	ragCfg.DatasetPath = strings.TrimSpace(e.cfg.RAGDatasetPath)
	ragCfg.SourcePaths = append(ragCfg.SourcePaths, e.cfg.RAGPaths...)
	ragCfg.SourcePaths = append(ragCfg.SourcePaths, e.cfg.RAGSourcePaths...)
	ragCfg.IndexPath = strings.TrimSpace(e.cfg.RAGIndexPath)
	if e.cfg.RAGTopK > 0 {
		ragCfg.TopK = e.cfg.RAGTopK
	} else if e.cfg.RAGMaxSnippets > 0 {
		ragCfg.TopK = e.cfg.RAGMaxSnippets
	}
	if e.cfg.RAGMaxChars > 0 {
		ragCfg.MaxSnippetChars = e.cfg.RAGMaxChars
	}
	if e.cfg.RAGChunkSize > 0 {
		ragCfg.ChunkSize = e.cfg.RAGChunkSize
	}
	if e.cfg.RAGChunkOverlap > 0 {
		ragCfg.ChunkOverlap = e.cfg.RAGChunkOverlap
	}
	if strings.TrimSpace(e.cfg.RAGChunkStrategy) != "" {
		ragCfg.ChunkStrategy = e.cfg.RAGChunkStrategy
	}
	if strings.TrimSpace(e.cfg.RAGRetrievalMode) != "" {
		ragCfg.RetrievalMode = e.cfg.RAGRetrievalMode
	}
	if strings.TrimSpace(e.cfg.RAGEmbeddingProvider) != "" {
		ragCfg.EmbeddingProvider = e.cfg.RAGEmbeddingProvider
	}
	if strings.TrimSpace(e.cfg.RAGEmbeddingModel) != "" {
		ragCfg.EmbeddingModel = e.cfg.RAGEmbeddingModel
	}
	ragCfg.EmbeddingBaseURL = strings.TrimSpace(e.cfg.RAGEmbeddingBaseURL)
	ragCfg.EmbeddingAPIKey = strings.TrimSpace(e.cfg.RAGEmbeddingAPIKey)
	if strings.TrimSpace(e.cfg.RAGVectorBackend) != "" {
		ragCfg.VectorBackend = e.cfg.RAGVectorBackend
	}
	ragCfg.VectorEndpoint = strings.TrimSpace(e.cfg.RAGVectorEndpoint)
	if strings.TrimSpace(e.cfg.RAGVectorCollection) != "" {
		ragCfg.VectorCollection = e.cfg.RAGVectorCollection
	}
	ragCfg.VectorDatabase = strings.TrimSpace(e.cfg.RAGVectorDatabase)
	ragCfg.VectorToken = strings.TrimSpace(e.cfg.RAGVectorToken)
	if e.cfg.RAGVectorTimeout > 0 {
		ragCfg.VectorTimeout = e.cfg.RAGVectorTimeout
	}
	if strings.TrimSpace(e.cfg.RAGRebuildPolicy) != "" {
		ragCfg.RebuildPolicy = e.cfg.RAGRebuildPolicy
	}
	index, err := rag.NewService(ragCfg, e.logger)
	if err != nil {
		e.logger.Warn("rag index build failed", zap.Error(err))
		return
	}
	e.rag = index
}

func (e *Engine) initLLM() {
	if !e.cfg.LLMEnabled {
		return
	}
	llmCfg := analysis.LLMClientConfig{
		Timeout: e.cfg.LLMTimeout,
	}
	client, err := analysis.NewLLMClient(llmCfg, e.logger)
	if err != nil {
		e.logger.Warn("agent llm client init failed", zap.Error(err))
		return
	}
	e.llm = client
}

func (e *Engine) llmInsight(nodeName string, metrics map[string]float64, evidence analysis.EvidencePack, findings, forecasts []string) *LLMInsight {
	if e.llm == nil {
		return nil
	}
	contextText := "Agent report enrichment"
	snippets := e.ragSnippets(findings, forecasts)
	if len(snippets) > 0 {
		contextText = contextText + "\nContext snippets:\n" + strings.Join(snippets, "\n---\n")
	}
	input := analysis.AnalysisInput{
		NodeName:  nodeName,
		Metrics:   metrics,
		Trends:    nil,
		Anomalies: findings,
		Context:   contextText,
		Schema:    analysis.BuildLLMSchemaForAgent(nodeName, metrics, findings, forecasts, evidence, snippets),
	}
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := e.cfg.LLMTimeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := e.llm.Analyze(ctx, input)
	if err != nil || result == nil {
		if err != nil {
			e.logger.Warn("agent llm analyze failed", zap.Error(err))
		}
		return nil
	}
	return &LLMInsight{
		Summary:         result.Summary,
		RootCause:       result.RootCause,
		Confidence:      result.Confidence,
		Recommendations: result.Recommendations,
		ContextSnippets: snippets,
	}
}

func (e *Engine) ragSnippets(findings, forecasts []string) []string {
	if e.rag == nil {
		return nil
	}
	query := compactAgentRAGQuery(findings, forecasts, e.cfg.RAGMaxFindings, e.cfg.RAGMaxQueryChars)
	if strings.TrimSpace(query) == "" {
		return nil
	}
	result, err := e.rag.Query(context.Background(), rag.QueryRequest{
		Query: query,
		TopK:  maxPositiveInt(e.cfg.RAGTopK, e.cfg.RAGMaxSnippets, 4),
	})
	if err != nil {
		e.logger.Debug("agent engine rag retrieval failed", zap.Error(err))
		return nil
	}
	if len(result.Hits) > 0 && result.Confidence < e.cfg.RAGMinConfidence {
		e.logger.Debug("agent engine rag retrieval suppressed due to low confidence",
			zap.Float64("confidence", result.Confidence),
			zap.Float64("min_confidence", e.cfg.RAGMinConfidence),
		)
		return nil
	}
	out := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		text := strings.TrimSpace(hit.Snippet)
		if text == "" {
			text = strings.TrimSpace(hit.Content)
		}
		if text == "" {
			continue
		}
		out = append(out, fmt.Sprintf("[%s] %s", firstNonEmpty(hit.Title, hit.SourcePath), text))
	}
	return out
}

func compactAgentRAGQuery(findings, forecasts []string, maxItems, maxChars int) string {
	parts := make([]string, 0, maxPositiveInt(maxItems, 1))
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
	seen := make(map[string]struct{}, len(findings)+len(forecasts))
	filteredFindings := make([]string, 0, len(findings))
	for _, item := range findings {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "no critical anomalies detected") ||
			strings.HasPrefix(lower, "telemetry snapshot is ") ||
			strings.Contains(lower, "telemetry freshness is degraded") {
			continue
		}
		filteredFindings = append(filteredFindings, trimmed)
	}
	for _, item := range append(filteredFindings, forecasts...) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		if !appendPart(item) {
			break
		}
		if maxItems > 0 && len(parts) >= maxItems {
			break
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (e *Engine) pruneActions() {
	if e.cfg.MaxActions <= 0 || len(e.actions) <= e.cfg.MaxActions {
		return
	}
	actions := make([]ActionDecision, 0, len(e.actions))
	for _, action := range e.actions {
		actions = append(actions, action)
	}
	sort.Slice(actions, func(i, j int) bool {
		return actions[i].Updated.After(actions[j].Updated)
	})
	keep := make(map[string]ActionDecision, e.cfg.MaxActions)
	for i := 0; i < len(actions) && i < e.cfg.MaxActions; i++ {
		keep[actions[i].ID] = actions[i]
	}
	e.actions = keep
}
