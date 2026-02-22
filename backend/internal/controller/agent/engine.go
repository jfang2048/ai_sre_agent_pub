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
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/incidents"
	"go.uber.org/zap"
)

type Config struct {
	Enabled        bool           `yaml:"enabled"`
	Interval       time.Duration  `yaml:"interval"`
	MaxReports     int            `yaml:"max_reports"`
	MaxActions     int            `yaml:"max_actions"`
	PredictHorizon time.Duration  `yaml:"predict_horizon"`
	PersistDir     string         `yaml:"persist_dir"`
	RAGEnabled     bool           `yaml:"rag_enabled"`
	RAGPaths       []string       `yaml:"rag_paths"`
	RAGMaxSnippets int            `yaml:"rag_max_snippets"`
	RAGMaxChars    int            `yaml:"rag_max_chars"`
	LLMEnabled     bool           `yaml:"llm_enabled"`
	LLMTimeout     time.Duration  `yaml:"llm_timeout"`
	PolicyFile     string         `yaml:"policy_file"`
	Playbooks      []PlaybookRule `yaml:"playbooks"`
	Signals        SignalRules    `yaml:"signals"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:        true,
		Interval:       30 * time.Second,
		MaxReports:     50,
		MaxActions:     200,
		PredictHorizon: 30 * time.Minute,
		PersistDir:     "./data/agent",
		RAGEnabled:     false,
		RAGPaths:       []string{"README.md", "docs", "configs"},
		RAGMaxSnippets: 4,
		RAGMaxChars:    1200,
		LLMEnabled:     false,
		LLMTimeout:     20 * time.Second,
		PolicyFile:     "./configs/agent_playbooks.yaml",
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

type Report struct {
	ID          string                       `json:"id"`
	NodeName    string                       `json:"node_name"`
	GeneratedAt time.Time                    `json:"generated_at"`
	Summary     string                       `json:"summary"`
	Findings    []string                     `json:"findings"`
	Forecasts   []string                     `json:"forecasts"`
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
	incidentContexts    map[string]incidents.AggregatedContext
	incidentAssessments map[string]IncidentAssessment
	ctx                 context.Context
	cancel              context.CancelFunc
	running             bool

	persist Store
	rag     *RAGIndex
}

func New(cfg Config, store *ingest.MemoryStore, analysisEngine *analysis.Engine, logger *zap.Logger) *Engine {
	if logger == nil {
		logger, _ = zap.NewProduction()
	}
	if cfg.MaxReports <= 0 {
		cfg.MaxReports = 50
	}
	if cfg.MaxActions <= 0 {
		cfg.MaxActions = 200
	}
	return &Engine{
		cfg:                 cfg,
		logger:              logger.With(zap.String("component", "agent_engine")),
		store:               store,
		analysis:            analysisEngine,
		reports:             make(map[string][]Report),
		actions:             make(map[string]ActionDecision),
		incidentContexts:    make(map[string]incidents.AggregatedContext),
		incidentAssessments: make(map[string]IncidentAssessment),
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
		e.mu.Lock()
		list := append(e.reports[node.CollectorID], report)
		if len(list) > e.cfg.MaxReports {
			list = list[len(list)-e.cfg.MaxReports:]
		}
		e.reports[node.CollectorID] = list
		for _, action := range report.Actions {
			e.actions[action.ID] = action
		}
		if e.cfg.MaxActions > 0 && len(e.actions) > e.cfg.MaxActions {
			e.pruneActions()
		}
		e.mu.Unlock()
		if e.persist != nil {
			_ = e.persist.SaveReport(report)
			for _, action := range report.Actions {
				_ = e.persist.SaveAction(action)
			}
		}
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

	findings, forecasts := analyzeSignals(metrics, e.cfg.Signals, e.cfg.PredictHorizon)

	now := time.Now()
	policyActions := applyPolicies(nodeName, metrics, e.policies, now)
	actions := policyActions
	if len(actions) == 0 {
		actions = planActions(nodeName, findings, forecasts, now)
	}

	summary := "Normal"
	if len(findings) > 0 {
		summary = findings[0]
	}

	llmInsight := e.llmInsight(nodeName, metrics, evidence, findings, forecasts)
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

func analyzeSignals(metrics map[string]float64, rules SignalRules, horizon time.Duration) ([]string, []string) {
	findings := make([]string, 0, 4)
	forecasts := make([]string, 0, 4)

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

	if len(findings) == 0 {
		findings = append(findings, "No critical anomalies detected")
	}

	return findings, forecasts
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
	if !e.cfg.RAGEnabled {
		return
	}
	index, err := BuildRAGIndex(e.cfg.RAGPaths, e.cfg.RAGMaxChars)
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
		contextText = contextText + "\nContext snippets:\n" + joinSnippets(snippets)
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
	query := append([]string{}, findings...)
	query = append(query, forecasts...)
	return e.rag.Search(query, e.cfg.RAGMaxSnippets)
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
