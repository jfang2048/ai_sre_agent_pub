package agent

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// WorkflowEngine runs deterministic, tool-driven joint-risk and RCA workflows.
type WorkflowEngine struct {
	cfg        WorkflowConfig
	logger     *zap.Logger
	store      *ingest.MemoryStore
	logIndex   *logindex.Index
	topology   TopologyProvider
	tools      *workflowToolManager
	llm        llmClient
	llmLimiter *rate.Limiter

	processTree     *ProcessTree
	baseline        *BaselineEngine
	traceStore      *TraceStore
	proposedActions *ProposedActionStore

	mu          sync.RWMutex
	riskReports []JointRiskAssessment
	rcaReports  []RCAWorkflowReport
	incidents   []AgentIncidentReport
	audits      []WorkflowAuditRecord
}

// NewWorkflowEngine creates a deterministic workflow engine with explicit tools.
func NewWorkflowEngine(cfg WorkflowConfig, store *ingest.MemoryStore, idx *logindex.Index, topology TopologyProvider, logger *zap.Logger) *WorkflowEngine {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = WorkflowConfigFromEnv(cfg)
	cfg = normalizeWorkflowConfig(cfg)

	engine := &WorkflowEngine{
		cfg:             cfg,
		logger:          logger.With(zap.String("component", "agent_workflow_engine")),
		store:           store,
		logIndex:        idx,
		topology:        topology,
		processTree:     NewProcessTree(65536),
		baseline:        NewBaselineEngine(DefaultBaselineConfig()),
		traceStore:      NewTraceStore(500),
		proposedActions: NewProposedActionStore(200),
		riskReports:     make([]JointRiskAssessment, 0, 64),
		rcaReports:      make([]RCAWorkflowReport, 0, 64),
		incidents:       make([]AgentIncidentReport, 0, 64),
		audits:          make([]WorkflowAuditRecord, 0, cfg.AuditRetention),
		llmLimiter:      rate.NewLimiter(rate.Limit(cfg.LLMRateLimitRPS), cfg.LLMRateBurst),
	}

	engine.tools = newWorkflowToolManager(engine.logger,
		&metricsQueryTool{store: store},
		&logsQueryTool{index: idx, store: store},
		&topologyQueryTool{provider: topology, store: store},
		&securityCheckTool{store: store, index: idx},
		&ebpfQueryTool{store: store},
		&securityGraphTool{store: store},
		&processLineageTool{store: store},
		&profilingTriggerTool{cfg: cfg},
		&remediationActionTool{cfg: cfg},
	)
	engine.llm = newWorkflowLLMClient(cfg, logger)

	return engine
}

// WorkflowConfigFromEnv loads environment overrides for workflow behavior.
func WorkflowConfigFromEnv(base WorkflowConfig) WorkflowConfig {
	cfg := normalizeWorkflowConfig(base)
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ENABLED")); raw != "" {
		cfg.Enabled = parseBool(raw, cfg.Enabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_WINDOW")); raw != "" {
		cfg.DefaultWindow = parseDuration(raw, cfg.DefaultWindow)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_WINDOW")); raw != "" {
		cfg.MaxWindow = parseDuration(raw, cfg.MaxWindow)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_SAMPLES")); raw != "" {
		cfg.MaxSamples = parseInt(raw, cfg.MaxSamples)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_SIGNALS")); raw != "" {
		cfg.MaxSignals = parseInt(raw, cfg.MaxSignals)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_HYPOTHESES")); raw != "" {
		cfg.MaxHypotheses = parseInt(raw, cfg.MaxHypotheses)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_PLAN_ITERATIONS")); raw != "" {
		cfg.MaxPlanIterations = parseInt(raw, cfg.MaxPlanIterations)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MAX_PLAN_STEPS")); raw != "" {
		cfg.MaxPlanSteps = parseInt(raw, cfg.MaxPlanSteps)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_AUDIT_RETENTION")); raw != "" {
		cfg.AuditRetention = parseInt(raw, cfg.AuditRetention)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INCIDENT_RETENTION")); raw != "" {
		cfg.IncidentRetention = parseInt(raw, cfg.IncidentRetention)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_DRY_RUN")); raw != "" {
		cfg.DryRun = parseBool(raw, cfg.DryRun)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_REQUIRE_APPROVAL")); raw != "" {
		cfg.RequireApproval = parseBool(raw, cfg.RequireApproval)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ALLOW_PROFILING_EXEC")); raw != "" {
		cfg.AllowProfilingExec = parseBool(raw, cfg.AllowProfilingExec)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_ALLOW_REMEDIATION_EXEC")); raw != "" {
		cfg.AllowRemediationExec = parseBool(raw, cfg.AllowRemediationExec)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_AUTO_ESCALATE_HIGH_RISK")); raw != "" {
		cfg.AutoEscalateOnHighRisk = parseBool(raw, cfg.AutoEscalateOnHighRisk)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_PROFILING_COMMAND")); raw != "" {
		cfg.ProfilingCommand = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_ENABLED")); raw != "" {
		cfg.InsightsEnabled = parseBool(raw, cfg.InsightsEnabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_PROVIDER")); raw != "" {
		cfg.InsightsProvider = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_MODEL")); raw != "" {
		cfg.InsightsModel = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_INSIGHTS_API_KEY_ENV")); raw != "" {
		cfg.InsightsAPIKeyEnv = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_LLM_TIMEOUT")); raw != "" {
		cfg.LLMTimeout = parseDuration(raw, cfg.LLMTimeout)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_LLM_RATE_LIMIT_RPS")); raw != "" {
		cfg.LLMRateLimitRPS = parseFloat(raw, cfg.LLMRateLimitRPS)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_LLM_RATE_BURST")); raw != "" {
		cfg.LLMRateBurst = parseInt(raw, cfg.LLMRateBurst)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_HIGH_RISK_THRESHOLD")); raw != "" {
		cfg.HighRiskScoreThreshold = parseFloat(raw, cfg.HighRiskScoreThreshold)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_WORKFLOW_MEDIUM_RISK_THRESHOLD")); raw != "" {
		cfg.MediumRiskThreshold = parseFloat(raw, cfg.MediumRiskThreshold)
	}
	return normalizeWorkflowConfig(cfg)
}

// EvaluateJointRisk executes the deterministic joint-risk workflow.
func (e *WorkflowEngine) EvaluateJointRisk(ctx context.Context, req WorkflowRequest) (JointRiskAssessment, error) {
	if e == nil || !e.cfg.Enabled {
		return JointRiskAssessment{}, fmt.Errorf("workflow engine disabled")
	}

	state := e.newWorkflowState("joint_risk", req)
	pipeline := deterministicPipeline{
		name: "joint_risk",
		steps: []pipelineStep{
			{name: "collect_signals", run: e.stepCollectSignals},
			{name: "score_signals", run: e.stepScoreSignals},
			{name: "correlate_signals", run: e.stepCorrelateSignals},
			{name: "recommendation_generation", run: e.stepJointRiskRecommendations},
			{name: "llm_analysis", run: e.stepLLMAnalysis},
			{name: "finalize", run: e.stepFinalizeJointRisk},
		},
	}

	if err := pipeline.run(ctx, state); err != nil {
		return JointRiskAssessment{}, err
	}
	report := state.risk
	if e.shouldEscalateFromJointRisk(report) {
		rcaReq := WorkflowRequest{
			WorkflowType: "rca",
			CollectorID:  report.CollectorID,
			Window:       state.window,
			Limit:        state.limit,
			Trigger:      "joint_risk_escalation",
			DryRun:       &state.dryRun,
		}
		if rcaReport, err := e.BuildRCAWorkflow(ctx, rcaReq); err == nil {
			report.Escalated = true
			report.EscalationReason = "risk threshold crossed and multi-signal co-occurrence confirmed"
			report.IncidentID = rcaReport.IncidentID
		} else {
			report.EscalationReason = "escalation attempt failed"
		}
	}
	e.recordJointRisk(report)
	return report, nil
}

// BuildRCAWorkflow executes the deterministic RCA pipeline.
func (e *WorkflowEngine) BuildRCAWorkflow(ctx context.Context, req WorkflowRequest) (RCAWorkflowReport, error) {
	if e == nil || !e.cfg.Enabled {
		return RCAWorkflowReport{}, fmt.Errorf("workflow engine disabled")
	}

	state := e.newWorkflowState("rca", req)
	pipeline := deterministicPipeline{
		name: "rca",
		steps: []pipelineStep{
			{name: "anomaly_detection", run: e.stepCollectSignals},
			{name: "context_gathering", run: e.stepGatherRCAContext},
			{name: "plan_act_verify_loop", run: e.stepPlanActVerifyLoop},
			{name: "hypothesis_generation", run: e.stepGenerateHypotheses},
			{name: "evidence_collection", run: e.stepCollectEvidence},
			{name: "llm_analysis", run: e.stepLLMAnalysis},
			{name: "recommendation_generation", run: e.stepRCARecommendations},
			{name: "guarded_execution_plan", run: e.stepGuardedExecutionPlan},
			{name: "finalize", run: e.stepFinalizeRCA},
		},
	}

	if err := pipeline.run(ctx, state); err != nil {
		return RCAWorkflowReport{}, err
	}
	report := state.rca
	e.recordRCA(report)
	e.recordIncident(report)
	return report, nil
}

// JointRiskReports returns recent joint-risk reports.
func (e *WorkflowEngine) JointRiskReports(limit int, collectorID string) []JointRiskAssessment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]JointRiskAssessment, 0, len(e.riskReports))
	collectorID = strings.TrimSpace(collectorID)
	for _, report := range e.riskReports {
		if collectorID != "" && !strings.EqualFold(report.CollectorID, collectorID) {
			continue
		}
		out = append(out, report)
	}
	sortJointRiskReportsByTime(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// RCAReports returns recent structured RCA workflow reports.
func (e *WorkflowEngine) RCAReports(limit int, collectorID string) []RCAWorkflowReport {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]RCAWorkflowReport, 0, len(e.rcaReports))
	collectorID = strings.TrimSpace(collectorID)
	for _, report := range e.rcaReports {
		if collectorID != "" && !strings.EqualFold(report.CollectorID, collectorID) {
			continue
		}
		out = append(out, report)
	}
	sortRCAReportsByTime(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// AuditRecords returns recent tool/action audit records.
func (e *WorkflowEngine) AuditRecords(limit int, workflowID string) []WorkflowAuditRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()

	workflowID = strings.TrimSpace(workflowID)
	out := make([]WorkflowAuditRecord, 0, len(e.audits))
	for _, entry := range e.audits {
		if workflowID != "" && !strings.EqualFold(entry.WorkflowID, workflowID) {
			continue
		}
		out = append(out, entry)
	}
	sortAuditByTime(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ToolRegistry returns explicit versioned workflow tools.
func (e *WorkflowEngine) ToolRegistry() []WorkflowToolDescriptor {
	if e == nil || e.tools == nil {
		return nil
	}
	return e.tools.registry()
}

// IncidentReports returns recent agentic incident investigations.
func (e *WorkflowEngine) IncidentReports(limit int, status, collectorID string) []AgentIncidentReport {
	e.mu.RLock()
	defer e.mu.RUnlock()

	status = strings.ToLower(strings.TrimSpace(status))
	collectorID = strings.TrimSpace(collectorID)
	out := make([]AgentIncidentReport, 0, len(e.incidents))
	for _, report := range e.incidents {
		if status != "" && strings.ToLower(strings.TrimSpace(report.Status)) != status {
			continue
		}
		if collectorID != "" && !strings.EqualFold(report.CollectorID, collectorID) {
			continue
		}
		out = append(out, report)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].OpenedAt.After(out[j].OpenedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// IncidentReport fetches one incident record.
func (e *WorkflowEngine) IncidentReport(incidentID string) (AgentIncidentReport, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	incidentID = strings.TrimSpace(incidentID)
	if incidentID == "" {
		return AgentIncidentReport{}, false
	}
	for _, report := range e.incidents {
		if strings.EqualFold(report.IncidentID, incidentID) {
			return report, true
		}
	}
	return AgentIncidentReport{}, false
}

// RefreshPotentialRiskFindings runs proactive joint-risk scans and updates findings source reports.
func (e *WorkflowEngine) RefreshPotentialRiskFindings(ctx context.Context, req WorkflowRequest) error {
	if e == nil || !e.cfg.Enabled {
		return fmt.Errorf("workflow engine disabled")
	}
	req.WorkflowType = "joint_risk"
	if strings.TrimSpace(req.CollectorID) != "" {
		_, err := e.EvaluateJointRisk(ctx, req)
		return err
	}

	maxCollectors := req.Limit
	if maxCollectors <= 0 {
		maxCollectors = 8
	}
	if maxCollectors > 24 {
		maxCollectors = 24
	}
	collectors := e.collectorIDs(maxCollectors)
	if len(collectors) == 0 {
		_, err := e.EvaluateJointRisk(ctx, req)
		return err
	}

	var firstErr error
	for _, collectorID := range collectors {
		scanReq := req
		scanReq.CollectorID = collectorID
		if _, err := e.EvaluateJointRisk(ctx, scanReq); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// PotentialRiskFindings returns ranked proactive risk findings.
func (e *WorkflowEngine) PotentialRiskFindings(limit int, collectorID string) []PotentialRiskFinding {
	reports := e.JointRiskReports(0, collectorID)
	findings := make([]PotentialRiskFinding, 0, len(reports))
	for _, report := range reports {
		scope := topFindingScope(report.ScopeRisks, report.CollectorID)
		signals := make([]PotentialRiskSignal, 0, minInt(8, len(report.Signals)))
		for _, signal := range report.Signals {
			if !signal.Triggered {
				continue
			}
			signals = append(signals, PotentialRiskSignal{
				Name:         signal.Name,
				Scope:        signal.Scope,
				Entity:       signal.Entity,
				Severity:     signal.Severity,
				Current:      signal.Current,
				Baseline:     signal.Baseline,
				DeltaPercent: signal.DeltaPercent,
				Score:        signal.Score,
				Evidence:     append([]string{}, signal.Evidence...),
			})
			if len(signals) >= 8 {
				break
			}
		}
		suggestions := make([]string, 0, 8)
		for _, signal := range signals {
			suggestions = append(suggestions, fmt.Sprintf(
				"validate %s on %s/%s: current %.3f vs baseline %.3f (delta %.1f%%, score %.2f)",
				signal.Name,
				firstNonEmpty(signal.Scope, "node"),
				firstNonEmpty(signal.Entity, report.CollectorID, "fleet"),
				signal.Current,
				signal.Baseline,
				signal.DeltaPercent,
				signal.Score,
			))
			if len(suggestions) >= 4 {
				break
			}
		}
		for _, co := range report.Cooccurrences {
			suggestions = append(suggestions, fmt.Sprintf(
				"verify co-occurrence on %s/%s in %s: %s (corr %.2f, combined %.2f)",
				firstNonEmpty(co.Scope, "node"),
				firstNonEmpty(co.Entity, report.CollectorID, "fleet"),
				co.Window,
				strings.Join(co.Signals, "+"),
				co.Correlation,
				co.CombinedScore,
			))
			if len(suggestions) >= 6 {
				break
			}
		}
		for _, recommendation := range report.Recommendations {
			if strings.TrimSpace(recommendation.Summary) == "" {
				continue
			}
			suggestions = append(suggestions, recommendation.Summary)
			if len(suggestions) >= 6 {
				break
			}
		}
		confidence := clamp01(report.RiskScore + 0.03*float64(len(report.Cooccurrences)))
		riskSummary := strings.TrimSpace(report.Summary)
		if len(signals) > 0 {
			top := signals[0]
			riskSummary = fmt.Sprintf(
				"%s | lead signal: %s on %s/%s current %.3f baseline %.3f delta %.1f%%",
				firstNonEmpty(riskSummary, "potential latent risk detected"),
				top.Name,
				firstNonEmpty(top.Scope, "node"),
				firstNonEmpty(top.Entity, report.CollectorID, "fleet"),
				top.Current,
				top.Baseline,
				top.DeltaPercent,
			)
		}
		findings = append(findings, PotentialRiskFinding{
			ID:                          fmt.Sprintf("risk-%s", sanitizeID(report.WorkflowID)),
			CollectorID:                 report.CollectorID,
			RiskSummary:                 riskSummary,
			TimeWindow:                  report.Window,
			Scope:                       scope,
			ConfidenceScore:             confidence,
			ContributingSignals:         signals,
			SuggestedInvestigationSteps: dedupeStrings(suggestions),
			Correlations:                append([]JointRiskCooccurrence{}, report.Cooccurrences...),
			Series:                      append([]RiskSeries{}, report.Series...),
			GeneratedAt:                 report.GeneratedAt,
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].ConfidenceScore == findings[j].ConfidenceScore {
			return findings[i].GeneratedAt.After(findings[j].GeneratedAt)
		}
		return findings[i].ConfidenceScore > findings[j].ConfidenceScore
	})
	if limit > 0 && len(findings) > limit {
		findings = findings[:limit]
	}
	return findings
}

type pipelineStep struct {
	name string
	run  func(context.Context, *workflowState) error
}

type deterministicPipeline struct {
	name  string
	steps []pipelineStep
}

func (p deterministicPipeline) run(ctx context.Context, state *workflowState) error {
	for _, step := range p.steps {
		stage := PipelineStageResult{Name: step.name, Status: "running", StartedAt: time.Now().UTC()}
		err := step.run(ctx, state)
		stage.CompletedAt = time.Now().UTC()
		if err != nil {
			stage.Status = "failed"
			stage.Summary = truncateString(err.Error(), 220)
			state.stages = append(state.stages, stage)
			state.engine.audit(state.workflowID, state.workflowType, step.name, "stage.failed", "failed", state.collectorID, state.dryRun, state.engine.cfg.RequireApproval, false, nil, stage.Summary, err)
			return err
		}
		stage.Status = "completed"
		stage.Summary = state.stageSummary(step.name)
		state.stages = append(state.stages, stage)
		state.engine.audit(state.workflowID, state.workflowType, step.name, "stage.completed", "success", state.collectorID, state.dryRun, state.engine.cfg.RequireApproval, true, nil, stage.Summary, nil)
	}
	return nil
}

type workflowState struct {
	engine       *WorkflowEngine
	workflowType string
	workflowID   string
	collectorID  string
	window       time.Duration
	limit        int
	trigger      string
	dryRun       bool
	now          time.Time

	stages      []PipelineStageResult
	toolCalls   []WorkflowToolCall
	limitations []string

	metricsData metricsToolData
	logsData    logsToolData
	topoData    topologyToolData
	security    securityToolData
	ebpf        ebpfToolData
	secGraph    securityGraphToolData
	lineage     processLineageToolData
	profiling   profilingToolData

	riskSignals    []JointRiskSignal
	riskSeries     []RiskSeries
	cooccurrences  []JointRiskCooccurrence
	scopeRisks     []ScopeRisk
	recommendation []WorkflowRecommendation

	hypotheses []RCAHypothesis
	evidence   []RCAEvidence
	corr       []RCACorrelation

	planSteps      []AgentPlanStep
	planRevisions  []AgentPlanRevision
	planIterations int
	planReplans    int
	stepsExecuted  int
	stepsVerified  int
	planCompleted  bool
	planStopReason string

	risk JointRiskAssessment
	rca  RCAWorkflowReport

	llmAnalysis *LLMAnalysisResult
}

func (e *WorkflowEngine) newWorkflowState(workflowType string, req WorkflowRequest) *workflowState {
	window := req.Window
	if window <= 0 {
		window = e.cfg.DefaultWindow
	}
	if window > e.cfg.MaxWindow {
		window = e.cfg.MaxWindow
	}
	limit := req.Limit
	if limit <= 0 {
		limit = e.cfg.MaxSamples
	}
	if limit > e.cfg.MaxSamples {
		limit = e.cfg.MaxSamples
	}
	dryRun := e.cfg.DryRun
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}

	return &workflowState{
		engine:         e,
		workflowType:   workflowType,
		workflowID:     newQueryID(),
		collectorID:    strings.TrimSpace(req.CollectorID),
		window:         window,
		limit:          limit,
		trigger:        strings.TrimSpace(req.Trigger),
		dryRun:         dryRun,
		now:            time.Now().UTC(),
		stages:         make([]PipelineStageResult, 0, 8),
		toolCalls:      make([]WorkflowToolCall, 0, 12),
		limitations:    make([]string, 0, 8),
		riskSignals:    make([]JointRiskSignal, 0, 24),
		riskSeries:     make([]RiskSeries, 0, 16),
		cooccurrences:  make([]JointRiskCooccurrence, 0, 8),
		scopeRisks:     make([]ScopeRisk, 0, 16),
		recommendation: make([]WorkflowRecommendation, 0, 12),
		hypotheses:     make([]RCAHypothesis, 0, 12),
		evidence:       make([]RCAEvidence, 0, 24),
		corr:           make([]RCACorrelation, 0, 12),
		planSteps:      make([]AgentPlanStep, 0, 12),
		planRevisions:  make([]AgentPlanRevision, 0, 8),
		planIterations: 1,
	}
}

func (s *workflowState) stageSummary(name string) string {
	switch name {
	case "collect_signals", "anomaly_detection":
		return fmt.Sprintf("tools=%d signals=%d", len(s.toolCalls), len(s.riskSignals))
	case "score_signals":
		return fmt.Sprintf("series=%d score=%.2f", len(s.riskSeries), s.risk.RiskScore)
	case "correlate_signals":
		return fmt.Sprintf("cooccurrences=%d", len(s.cooccurrences))
	case "recommendation_generation":
		if s.workflowType == "joint_risk" {
			return fmt.Sprintf("recommendations=%d", len(s.recommendation))
		}
		return fmt.Sprintf("hypotheses=%d recommendations=%d", len(s.hypotheses), len(s.recommendation))
	case "context_gathering":
		metricCount := 0
		if s.metricsData.Node != nil {
			metricCount = len(s.metricsData.Node.Metrics)
		}
		return fmt.Sprintf("metrics=%d logs=%d", metricCount, len(s.logsData.Snippets))
	case "hypothesis_generation":
		return fmt.Sprintf("hypotheses=%d", len(s.hypotheses))
	case "evidence_collection":
		return fmt.Sprintf("evidence=%d", len(s.evidence))
	case "plan_act_verify_loop":
		return fmt.Sprintf("iterations=%d replans=%d executed=%d verified=%d", s.planIterations, s.planReplans, s.stepsExecuted, s.stepsVerified)
	case "guarded_execution_plan":
		return "guarded actions prepared"
	case "finalize":
		return "report materialized"
	default:
		return "completed"
	}
}

func (s *workflowState) callTool(ctx context.Context, stage string, tool ToolName, query map[string]string) (workflowToolResult, error) {
	request := workflowToolRequest{
		WorkflowID:  s.workflowID,
		Workflow:    s.workflowType,
		Stage:       stage,
		CollectorID: s.collectorID,
		Window:      s.window,
		Limit:       s.limit,
		Query:       query,
		DryRun:      s.dryRun,
	}
	call, result, err := s.engine.tools.call(ctx, request, tool)
	s.toolCalls = append(s.toolCalls, call)

	status := "success"
	summary := call.Summary
	if err != nil {
		status = "failed"
		summary = err.Error()
	}
	s.engine.audit(
		s.workflowID,
		s.workflowType,
		stage,
		"tool.call",
		status,
		s.collectorID,
		s.dryRun,
		s.engine.cfg.RequireApproval,
		status == "success",
		map[string]string{"tool": string(tool)},
		summary,
		err,
	)
	if err != nil {
		return workflowToolResult{}, err
	}
	return result, nil
}

func (e *WorkflowEngine) stepCollectSignals(ctx context.Context, state *workflowState) error {
	metricsResult, err := state.callTool(ctx, "collect_signals", ToolMetrics, nil)
	if err != nil {
		return err
	}
	metricsData, ok := metricsResult.Data.(metricsToolData)
	if !ok {
		return fmt.Errorf("metrics tool returned invalid payload")
	}
	state.metricsData = metricsData
	state.collectorID = firstNonEmpty(state.collectorID, metricsData.CollectorID)

	logsResult, err := state.callTool(ctx, "collect_signals", ToolLogs, map[string]string{"query": "error warn timeout deployment"})
	if err != nil {
		state.limitations = append(state.limitations, "log query tool unavailable")
	} else if logsData, ok := logsResult.Data.(logsToolData); ok {
		state.logsData = logsData
	}

	topologyResult, err := state.callTool(ctx, "collect_signals", ToolTopology, nil)
	if err != nil {
		state.limitations = append(state.limitations, "topology query unavailable")
	} else if topologyData, ok := topologyResult.Data.(topologyToolData); ok {
		state.topoData = topologyData
	}

	securityResult, err := state.callTool(ctx, "collect_signals", ToolSecurity, nil)
	if err != nil {
		state.limitations = append(state.limitations, "security tool unavailable")
	} else if securityData, ok := securityResult.Data.(securityToolData); ok {
		state.security = securityData
	}

	ebpfResult, err := state.callTool(ctx, "collect_signals", ToolEBPFQuery, nil)
	if err != nil {
		state.limitations = append(state.limitations, "ebpf query tool unavailable")
	} else if ebpfData, ok := ebpfResult.Data.(ebpfToolData); ok {
		state.ebpf = ebpfData
	}

	secGraphResult, err := state.callTool(ctx, "collect_signals", ToolSecurityGraph, nil)
	if err != nil {
		state.limitations = append(state.limitations, "security graph tool unavailable")
	} else if graphData, ok := secGraphResult.Data.(securityGraphToolData); ok {
		state.secGraph = graphData
	}

	lineageResult, err := state.callTool(ctx, "collect_signals", ToolProcessLineage, nil)
	if err != nil {
		state.limitations = append(state.limitations, "process lineage tool unavailable")
	} else if lineageData, ok := lineageResult.Data.(processLineageToolData); ok {
		state.lineage = lineageData
	}

	state.riskSeries = buildRiskSeries(state.metricsData.History, state.logsData)
	state.riskSignals = buildRiskSignals(state.collectorID, state.riskSeries, state.security, state.ebpf)
	if len(state.riskSignals) == 0 {
		state.limitations = append(state.limitations, "insufficient historical metrics for weighted risk scoring")
	}
	state.scopeRisks = buildScopeRisks(state.metricsData, state.riskSignals)
	state.cooccurrences = buildCooccurrences(state.riskSignals, state.riskSeries, state.window, state.collectorID)
	return nil
}

func (e *WorkflowEngine) stepScoreSignals(_ context.Context, state *workflowState) error {
	if len(state.riskSignals) == 0 {
		state.risk.RiskScore = 0
		state.risk.RiskLevel = "low"
		state.risk.Summary = "joint risk low: no significant co-occurring low-severity signals"
		state.risk.ActionableWhy = "insufficient active signals in the selected window"
		return nil
	}
	total := 0.0
	active := 0
	for _, signal := range state.riskSignals {
		total += signal.Score
		if signal.Triggered {
			active++
		}
	}
	if active >= 2 {
		total += 0.04 * float64(active-1)
	}
	if len(state.cooccurrences) > 0 {
		total += 0.06 * float64(len(state.cooccurrences))
	}
	if hasBehavioralAnomaly(state.riskSignals) && hasResourceAnomaly(state.riskSignals) {
		total += 0.09
	}
	total = clamp01(total)

	level := "low"
	if total >= e.cfg.HighRiskScoreThreshold {
		level = "high"
	} else if total >= e.cfg.MediumRiskThreshold {
		level = "medium"
	}

	summary := fmt.Sprintf("joint risk %s (score %.2f) across %d active signals", level, total, active)
	actionable := "no strong co-occurrence detected"
	if len(state.cooccurrences) > 0 {
		top := state.cooccurrences[0]
		actionable = fmt.Sprintf("combined risk is high because signals %s co-occurred within window %s on scope %s/%s",
			strings.Join(top.Signals, "+"), top.Window, top.Scope, top.Entity)
	}
	if len(state.cooccurrences) == 0 && active >= 3 {
		names := make([]string, 0, 3)
		for _, signal := range state.riskSignals {
			if !signal.Triggered {
				continue
			}
			names = append(names, signal.Name)
			if len(names) >= 3 {
				break
			}
		}
		actionable = fmt.Sprintf("combined risk is elevated because signals %s co-occurred within %s on scope node/%s",
			strings.Join(names, "+"), state.window.String(), firstNonEmpty(state.collectorID, "fleet"))
	}

	state.risk.RiskScore = total
	state.risk.RiskLevel = level
	state.risk.Summary = summary
	state.risk.ActionableWhy = actionable
	return nil
}

func (e *WorkflowEngine) stepCorrelateSignals(_ context.Context, state *workflowState) error {
	if len(state.cooccurrences) == 0 {
		state.cooccurrences = buildFallbackCooccurrence(state.riskSignals, state.window, state.collectorID)
	}
	return nil
}

func (e *WorkflowEngine) stepJointRiskRecommendations(ctx context.Context, state *workflowState) error {
	recs := make([]WorkflowRecommendation, 0, 8)
	topSignals := topTriggeredSignals(state.riskSignals, 4)
	for i, signal := range topSignals {
		recs = append(recs, WorkflowRecommendation{
			ID:               fmt.Sprintf("risk-check-%d", i+1),
			Priority:         priorityForSeverity(signal.Severity),
			Summary:          fmt.Sprintf("Inspect %s pressure on %s", signal.Name, signal.Entity),
			Details:          strings.Join(signal.Evidence, " "),
			Checks:           []string{"validate current metric against baseline", "confirm pressure source from top process list"},
			Safe:             true,
			DryRunDefault:    true,
			RequiresApproval: false,
			Reversible:       true,
			RollbackHint:     "read-only diagnostic check",
		})
	}

	if state.risk.RiskLevel == "high" || state.risk.RiskLevel == "medium" {
		profileResult, err := state.callTool(ctx, "recommendation_generation", ToolProfiling, map[string]string{
			"reason": state.risk.ActionableWhy,
		})
		if err == nil {
			if profile, ok := profileResult.Data.(profilingToolData); ok {
				state.profiling = profile
				recs = append(recs, WorkflowRecommendation{
					ID:               "risk-profile-1",
					Priority:         "medium",
					Summary:          "Trigger short CPU/profile capture for evidence",
					Details:          fmt.Sprintf("Command: %s", profile.Command),
					Checks:           []string{"confirm low-overhead profile window", "capture profile during active pressure period"},
					Safe:             false,
					DryRunDefault:    true,
					RequiresApproval: e.cfg.RequireApproval,
					Reversible:       true,
					RollbackHint:     "stop profiling command and discard capture",
				})
			}
		}
	}

	state.recommendation = dedupeRecommendations(recs)
	if len(state.recommendation) > 10 {
		state.recommendation = state.recommendation[:10]
	}

	for _, rec := range state.recommendation {
		e.audit(state.workflowID, state.workflowType, "recommendation_generation", "recommendation.generated", "success", state.collectorID, state.dryRun, rec.RequiresApproval, true,
			map[string]string{"recommendation_id": rec.ID, "priority": rec.Priority},
			rec.Summary,
			nil,
		)
	}
	return nil
}

func (e *WorkflowEngine) stepFinalizeJointRisk(_ context.Context, state *workflowState) error {
	mergeLLMIntoState(state)
	insights := e.insightsStatus()

	var contribSignals []ContributingSignal
	for _, sig := range state.riskSignals {
		if !sig.Triggered {
			continue
		}
		contribSignals = append(contribSignals, ContributingSignal{
			SignalID:   sig.ID,
			SignalType: sig.Scope,
			Value:      sig.Current,
			Weight:     sig.Weight,
			Source:     sig.Entity,
		})
	}

	var impactedScope []string
	for _, sr := range state.scopeRisks {
		impactedScope = append(impactedScope, fmt.Sprintf("%s/%s", sr.Scope, sr.Entity))
	}

	confidence := clamp01(state.risk.RiskScore * 0.8)
	if len(state.cooccurrences) > 0 {
		confidence = clamp01(confidence + 0.1)
	}
	if len(contribSignals) >= 3 {
		confidence = clamp01(confidence + 0.05)
	}

	var recToolCalls []string
	switch state.risk.RiskLevel {
	case "high":
		recToolCalls = []string{"profiling_trigger", "process_lineage", "ebpf_query"}
	case "medium":
		recToolCalls = []string{"metrics_query", "log_query"}
	}

	if e.baseline != nil && state.collectorID != "" {
		for _, d := range e.baseline.DetectDrift(state.collectorID) {
			contribSignals = append(contribSignals, ContributingSignal{
				SignalID:   fmt.Sprintf("baseline_drift_%s_%s", d.Dimension, d.Metric),
				SignalType: "baseline_drift",
				Value:      d.Current,
				Weight:     0.15,
				Source:     d.CollectorID,
			})
		}
	}

	if e.processTree != nil {
		for _, a := range e.processTree.DetectAbnormalLineage() {
			contribSignals = append(contribSignals, ContributingSignal{
				SignalID:   fmt.Sprintf("lineage_anomaly_pid_%d", a.PID),
				SignalType: "process_lineage",
				Value:      1.0,
				Weight:     0.2,
				Source:     a.Binary,
			})
		}
	}

	state.risk = JointRiskAssessment{
		WorkflowID:           state.workflowID,
		PipelineVersion:      workflowPipelineVersion,
		CollectorID:          state.collectorID,
		Scope:                "node",
		Window:               state.window.String(),
		GeneratedAt:          state.now,
		RiskScore:            state.risk.RiskScore,
		RiskLevel:            state.risk.RiskLevel,
		Summary:              state.risk.Summary,
		ActionableWhy:        state.risk.ActionableWhy,
		Signals:              topRiskSignals(state.riskSignals, e.cfg.MaxSignals),
		Cooccurrences:        state.cooccurrences,
		ScopeRisks:           state.scopeRisks,
		Series:               state.riskSeries,
		Recommendations:      state.recommendation,
		Stages:               append([]PipelineStageResult(nil), state.stages...),
		ToolCalls:            append([]WorkflowToolCall(nil), state.toolCalls...),
		Limitations:          dedupeStrings(state.limitations),
		Insights:             insights,
		LLMAnalysis:          state.llmAnalysis,
		ContributingSignals:  contribSignals,
		CorrelatedTimeWindow: &TimeWindow{Start: state.now.Add(-state.window), End: state.now},
		ImpactedScope:        impactedScope,
		Confidence:           confidence,
		RecommendedToolCalls: recToolCalls,
		Severity:             state.risk.RiskLevel,
	}
	if state.risk.ActionableWhy == "" {
		state.risk.ActionableWhy = "no actionable multi-signal co-occurrence found"
	}

	// Record trace
	if e.traceStore != nil {
		trace := &AgentTrace{
			TraceID:        state.workflowID,
			WorkflowType:   "joint_risk",
			CollectorID:    state.collectorID,
			StartedAt:      state.now,
			CompletedAt:    time.Now().UTC(),
			Status:         "completed",
			ToolCalls:      append([]WorkflowToolCall(nil), state.toolCalls...),
			Stages:         append([]PipelineStageResult(nil), state.stages...),
			FinalRiskScore: state.risk.RiskScore,
			Summary:        state.risk.Summary,
			RiskTimeline: []RiskTimelineEntry{
				{Timestamp: state.now, RiskScore: state.risk.RiskScore, RiskLevel: state.risk.RiskLevel, Source: "joint_risk"},
			},
		}
		e.traceStore.RecordTrace(trace)
	}

	// Generate proposed actions from recommendations
	if e.proposedActions != nil && len(state.recommendation) > 0 {
		actions := GenerateProposedActions(state.workflowID, state.collectorID, state.recommendation, state.risk.RiskScore)
		for _, a := range actions {
			e.proposedActions.RecordAction(a)
		}
	}

	return nil
}

func (e *WorkflowEngine) stepGatherRCAContext(_ context.Context, state *workflowState) error {
	metrics := map[string]float64{}
	if state.metricsData.Node != nil {
		metrics = cloneMetricMap(state.metricsData.Node.Metrics)
	}
	topMetrics := topMetricMap(metrics, 12)
	topProcesses := make([]string, 0, 6)
	if state.metricsData.Node != nil {
		for _, process := range topProcessResources(state.metricsData.Node, 6) {
			topProcesses = append(topProcesses, processDisplayName(process))
		}
	}

	kernelSignals := make([]string, 0, 8)
	for _, signal := range state.riskSignals {
		if !signal.Triggered {
			continue
		}
		if strings.Contains(strings.ToLower(signal.Name), "io") || strings.Contains(strings.ToLower(signal.Name), "retransmit") || strings.Contains(strings.ToLower(signal.Name), "cpu") {
			kernelSignals = append(kernelSignals, signal.Name)
		}
	}
	for _, summary := range state.ebpf.RuntimeEventSummaries {
		kernelSignals = append(kernelSignals, summary)
		if len(kernelSignals) >= 12 {
			break
		}
	}
	kernelSignals = dedupeStrings(kernelSignals)

	topoSummary := strings.TrimSpace(state.topoData.Snapshot.Summary)
	if topoSummary == "" {
		topoSummary = "topology unavailable"
	}

	state.rca.Context = RCAContext{
		CollectorID:      state.collectorID,
		Window:           state.window.String(),
		TopMetrics:       topMetrics,
		TopProcesses:     topProcesses,
		KernelSignals:    kernelSignals,
		RecentDeploys:    state.logsData.RecentDeploys,
		SecurityFindings: dedupeStrings(append([]string{}, state.security.Findings...)),
		TopologySummary:  topoSummary,
	}

	anomalies := make([]string, 0, len(state.riskSignals))
	for _, signal := range state.riskSignals {
		if signal.Triggered {
			anomalies = append(anomalies, fmt.Sprintf("%s on %s (%.1f%% delta)", signal.Name, signal.Entity, signal.DeltaPercent))
		}
	}
	if len(anomalies) == 0 {
		anomalies = append(anomalies, "no strong anomalies detected from weighted risk model")
	}
	state.rca.Anomalies = anomalies
	return nil
}

func (e *WorkflowEngine) stepPlanActVerifyLoop(ctx context.Context, state *workflowState) error {
	state.planSteps = buildInitialPlanSteps(state)
	if len(state.planSteps) == 0 {
		state.planCompleted = true
		state.planStopReason = "no plan steps generated"
		return nil
	}

	maxStepExec := maxInt(1, e.cfg.MaxPlanIterations*e.cfg.MaxPlanSteps)
	for idx := 0; idx < len(state.planSteps); idx++ {
		if state.stepsExecuted >= maxStepExec {
			state.planStopReason = "execution budget reached"
			break
		}

		step := &state.planSteps[idx]
		if step.Status == "completed" || step.Status == "failed" || step.Status == "skipped" {
			continue
		}
		step.Status = "running"
		step.StartedAt = time.Now().UTC()
		result, err := state.callTool(ctx, "plan_act_verify_loop", step.Tool, step.Query)
		step.CompletedAt = time.Now().UTC()
		step.ToolVersion = latestToolVersion(state.toolCalls)
		state.stepsExecuted++

		if err != nil {
			step.Status = "failed"
			step.Verified = false
			step.ResultSummary = truncateString(err.Error(), 220)
			step.VerificationNote = "tool execution failed"
			e.audit(
				state.workflowID,
				state.workflowType,
				"plan_act_verify_loop",
				"plan.step_failed",
				"failed",
				state.collectorID,
				state.dryRun,
				e.cfg.RequireApproval,
				false,
				map[string]string{"step_id": step.ID, "tool": string(step.Tool)},
				step.ResultSummary,
				err,
			)
			e.tryPlanRevision(state, step, "tool failure")
			continue
		}

		verified, note, evidenceIDs := verifyPlanStep(*step, result, state)
		step.Status = "completed"
		step.Verified = verified
		step.VerificationNote = note
		step.ResultSummary = truncateString(result.Summary, 220)
		step.EvidenceIDs = evidenceIDs
		if verified {
			state.stepsVerified++
		} else {
			e.tryPlanRevision(state, step, note)
		}

		stepStatus := "success"
		if !verified {
			stepStatus = "partial"
		}
		e.audit(
			state.workflowID,
			state.workflowType,
			"plan_act_verify_loop",
			"plan.step_verified",
			stepStatus,
			state.collectorID,
			state.dryRun,
			e.cfg.RequireApproval,
			verified,
			map[string]string{"step_id": step.ID, "tool": string(step.Tool)},
			truncateString(step.VerificationNote, 220),
			nil,
		)
	}

	state.planCompleted = state.stepsExecuted > 0
	if state.planStopReason == "" {
		state.planStopReason = "all planned steps executed"
	}
	return nil
}

func buildInitialPlanSteps(state *workflowState) []AgentPlanStep {
	if state == nil {
		return nil
	}
	steps := []AgentPlanStep{
		{
			ID:        "plan-metrics",
			Order:     1,
			Iteration: 1,
			Title:     "Collect metrics evidence",
			Objective: "Validate system/process/kernel pressure deltas against baseline.",
			Tool:      ToolMetrics,
			Query: map[string]string{
				"include": "system,process,kernel_ebpf",
			},
			Status: "pending",
		},
		{
			ID:        "plan-logs",
			Order:     2,
			Iteration: 1,
			Title:     "Correlate logs with anomaly window",
			Objective: "Confirm error/warn bursts and deployment correlation.",
			Tool:      ToolLogs,
			Query: map[string]string{
				"query": "error warn timeout deploy restart oom permission",
			},
			Status: "pending",
		},
		{
			ID:        "plan-topology",
			Order:     3,
			Iteration: 1,
			Title:     "Scope topology impact",
			Objective: "Map node/pod/service blast radius in current window.",
			Tool:      ToolTopology,
			Status:    "pending",
		},
		{
			ID:        "plan-security",
			Order:     4,
			Iteration: 1,
			Title:     "Check security/misconfiguration signals",
			Objective: "Confirm or discard security-related contributors.",
			Tool:      ToolSecurity,
			Status:    "pending",
		},
		{
			ID:        "plan-ebpf",
			Order:     5,
			Iteration: 1,
			Title:     "Query eBPF runtime behavior",
			Objective: "Collect syscall/process/network/file behavior anomalies from kernel-level telemetry.",
			Tool:      ToolEBPFQuery,
			Status:    "pending",
		},
		{
			ID:        "plan-security-graph",
			Order:     6,
			Iteration: 1,
			Title:     "Build security evidence graph",
			Objective: "Map process-to-port/IP/path edges and detect suspicious security graph links.",
			Tool:      ToolSecurityGraph,
			Status:    "pending",
		},
		{
			ID:        "plan-lineage",
			Order:     7,
			Iteration: 1,
			Title:     "Reconstruct process lineage",
			Objective: "Validate parent-child process chain and blast-radius lineage paths.",
			Tool:      ToolProcessLineage,
			Status:    "pending",
		},
	}
	if strings.EqualFold(state.risk.RiskLevel, "high") {
		steps = append(steps, AgentPlanStep{
			ID:        "plan-profile",
			Order:     len(steps) + 1,
			Iteration: 1,
			Title:     "Prepare bounded profile capture",
			Objective: "Collect extra runtime evidence when high-risk signals compound.",
			Tool:      ToolProfiling,
			Query: map[string]string{
				"reason": state.risk.ActionableWhy,
			},
			Status: "pending",
		})
	}
	return steps
}

func verifyPlanStep(step AgentPlanStep, result workflowToolResult, state *workflowState) (bool, string, []string) {
	evidenceIDs := []string{fmt.Sprintf("ev-%s", sanitizeID(step.ID))}
	switch step.Tool {
	case ToolMetrics:
		data, ok := result.Data.(metricsToolData)
		if !ok {
			return false, "invalid metrics payload", evidenceIDs
		}
		if data.Node == nil || len(data.History) < 3 {
			return false, "insufficient metric history for verification", evidenceIDs
		}
		if len(state.riskSignals) > 0 && len(topTriggeredSignals(state.riskSignals, 3)) == 0 {
			return false, "metric anomalies did not support current hypotheses", evidenceIDs
		}
		return true, fmt.Sprintf("metrics verified with %d samples", len(data.History)), evidenceIDs
	case ToolLogs:
		data, ok := result.Data.(logsToolData)
		if !ok {
			return false, "invalid log payload", evidenceIDs
		}
		if expectsLogBurst(state) && data.Errors+data.Warnings == 0 && len(data.Snippets) == 0 {
			return false, "log burst hypothesis not supported by current logs", evidenceIDs
		}
		if data.Errors+data.Warnings == 0 && len(data.Snippets) == 0 {
			return false, "no matching logs in selected window", evidenceIDs
		}
		return true, fmt.Sprintf("logs verified errors=%d warnings=%d", data.Errors, data.Warnings), evidenceIDs
	case ToolTopology:
		data, ok := result.Data.(topologyToolData)
		if !ok {
			return false, "invalid topology payload", evidenceIDs
		}
		if len(data.Snapshot.Nodes) == 0 && strings.TrimSpace(data.Snapshot.Summary) == "" {
			return false, "topology data unavailable", evidenceIDs
		}
		return true, fmt.Sprintf("topology verified nodes=%d edges=%d", len(data.Snapshot.Nodes), len(data.Snapshot.Edges)), evidenceIDs
	case ToolSecurity:
		data, ok := result.Data.(securityToolData)
		if !ok {
			return false, "invalid security payload", evidenceIDs
		}
		if len(data.Findings) == 0 {
			return true, "security check completed with no major findings", evidenceIDs
		}
		return true, fmt.Sprintf("security findings=%d", len(data.Findings)), evidenceIDs
	case ToolEBPFQuery:
		data, ok := result.Data.(ebpfToolData)
		if !ok {
			return false, "invalid ebpf payload", evidenceIDs
		}
		if len(data.RuntimeEvents) == 0 && len(data.SyscallStatistics) == 0 {
			return false, "no ebpf runtime evidence in selected window", evidenceIDs
		}
		return true, fmt.Sprintf("ebpf events=%d syscalls=%d", len(data.RuntimeEvents), len(data.SyscallStatistics)), append(evidenceIDs, data.EvidenceIDs...)
	case ToolSecurityGraph:
		data, ok := result.Data.(securityGraphToolData)
		if !ok {
			return false, "invalid security graph payload", evidenceIDs
		}
		if len(data.Nodes) == 0 || len(data.Edges) == 0 {
			return false, "security graph lacks actionable edges", evidenceIDs
		}
		return true, fmt.Sprintf("security graph nodes=%d edges=%d", len(data.Nodes), len(data.Edges)), evidenceIDs
	case ToolProcessLineage:
		data, ok := result.Data.(processLineageToolData)
		if !ok {
			return false, "invalid process lineage payload", evidenceIDs
		}
		if len(data.Nodes) == 0 || len(data.Edges) == 0 {
			return false, "process lineage unavailable", evidenceIDs
		}
		return true, fmt.Sprintf("lineage nodes=%d edges=%d", len(data.Nodes), len(data.Edges)), evidenceIDs
	case ToolProfiling:
		data, ok := result.Data.(profilingToolData)
		if !ok {
			return false, "invalid profiling payload", evidenceIDs
		}
		if strings.Contains(strings.ToLower(data.Mode), "blocked") {
			return false, "profiling plan blocked by policy", evidenceIDs
		}
		return true, data.Message, evidenceIDs
	case ToolRemediation:
		data, ok := result.Data.(remediationToolData)
		if !ok {
			return false, "invalid remediation payload", evidenceIDs
		}
		return true, data.Summary, evidenceIDs
	default:
		return true, "step completed", evidenceIDs
	}
}

func expectsLogBurst(state *workflowState) bool {
	if state == nil {
		return false
	}
	for _, signal := range state.riskSignals {
		if signal.Triggered && signal.ID == "log_burst" {
			return true
		}
	}
	return false
}

func (e *WorkflowEngine) tryPlanRevision(state *workflowState, failedStep *AgentPlanStep, reason string) bool {
	if e == nil || state == nil || failedStep == nil {
		return false
	}
	if state.planReplans >= maxInt(0, e.cfg.MaxPlanIterations-1) {
		return false
	}
	if len(state.planSteps) >= e.cfg.MaxPlanSteps {
		return false
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "verification gap"
	}

	next := AgentPlanStep{
		ID:        fmt.Sprintf("%s-r%d", failedStep.ID, state.planReplans+1),
		Order:     len(state.planSteps) + 1,
		Iteration: state.planIterations + 1,
		Status:    "pending",
	}
	switch failedStep.Tool {
	case ToolMetrics:
		next.Title = "Expand metrics window"
		next.Objective = "Re-check hypotheses using broader historical baseline."
		next.Tool = ToolMetrics
		next.Query = map[string]string{"mode": "extended", "window": "2h"}
	case ToolLogs:
		next.Title = "Broaden log search"
		next.Objective = "Look for disconfirming/supporting errors across services."
		next.Tool = ToolLogs
		next.Query = map[string]string{"query": "error warn panic timeout refused deploy rollback"}
	case ToolTopology:
		next.Title = "Rebuild topology mapping"
		next.Objective = "Refresh blast radius mapping from latest nodes/pods/services."
		next.Tool = ToolTopology
	case ToolSecurity:
		next.Title = "Collect remediation dry-run plan"
		next.Objective = "Prepare guarded remediation option with rollback details."
		next.Tool = ToolRemediation
		next.Query = map[string]string{"action": "targeted_restart_candidate", "scope": firstNonEmpty(state.collectorID, "fleet")}
	case ToolEBPFQuery:
		next.Title = "Expand eBPF query window"
		next.Objective = "Re-check behavior anomalies using a wider runtime window."
		next.Tool = ToolEBPFQuery
		next.Query = map[string]string{"window": "2h", "mode": "extended"}
	case ToolSecurityGraph:
		next.Title = "Rebuild security graph with broader scope"
		next.Objective = "Collect additional process-to-port/IP/path relationships."
		next.Tool = ToolSecurityGraph
	case ToolProcessLineage:
		next.Title = "Refresh process lineage snapshot"
		next.Objective = "Reconstruct lineage after latest telemetry refresh."
		next.Tool = ToolProcessLineage
	default:
		next.Title = "Capture bounded profile"
		next.Objective = "Gather additional runtime evidence for unresolved hypotheses."
		next.Tool = ToolProfiling
		next.Query = map[string]string{"reason": reason}
	}

	state.planReplans++
	state.planIterations++
	state.planSteps = append(state.planSteps, next)
	revision := AgentPlanRevision{
		Iteration: state.planIterations,
		Reason:    truncateString(reason, 220),
		CreatedAt: time.Now().UTC(),
		Steps:     append([]AgentPlanStep(nil), state.planSteps...),
	}
	state.planRevisions = append(state.planRevisions, revision)
	e.audit(
		state.workflowID,
		state.workflowType,
		"plan_act_verify_loop",
		"plan.revised",
		"success",
		state.collectorID,
		state.dryRun,
		e.cfg.RequireApproval,
		true,
		map[string]string{"failed_step": failedStep.ID, "new_step": next.ID},
		revision.Reason,
		nil,
	)
	return true
}

func (e *WorkflowEngine) stepGenerateHypotheses(_ context.Context, state *workflowState) error {
	hypotheses := make([]RCAHypothesis, 0, e.cfg.MaxHypotheses)
	active := topTriggeredSignals(state.riskSignals, e.cfg.MaxHypotheses)

	for _, signal := range active {
		title := hypothesisTitleFromSignal(signal.Name)
		confidence := clamp01(signal.Score * (1.0 / maxFloat(signal.Weight, 0.01)))
		description := fmt.Sprintf("%s inferred from %s trend acceleration %.3f and baseline delta %.1f%%",
			title, signal.Name, signal.Acceleration, signal.DeltaPercent)
		hypotheses = append(hypotheses, RCAHypothesis{
			ID:          fmt.Sprintf("h-%s", sanitizeID(signal.ID)),
			Title:       title,
			Confidence:  confidence,
			Description: description,
		})
	}

	if len(state.logsData.RecentDeploys) > 0 {
		hypotheses = append(hypotheses, RCAHypothesis{
			ID:          "h-recent-deploy",
			Title:       "recent deployment/regression",
			Confidence:  0.62,
			Description: "recent rollout/deployment signals co-occur with incident window",
		})
	}
	if len(state.security.Findings) > 0 {
		hypotheses = append(hypotheses, RCAHypothesis{
			ID:          "h-security",
			Title:       "security or permission misconfiguration",
			Confidence:  clamp01(0.45 + state.security.Score*0.4),
			Description: "security tool flagged weak permissions or suspicious network exposure",
		})
	}

	sort.Slice(hypotheses, func(i, j int) bool {
		if hypotheses[i].Confidence == hypotheses[j].Confidence {
			return hypotheses[i].Title < hypotheses[j].Title
		}
		return hypotheses[i].Confidence > hypotheses[j].Confidence
	})
	if len(hypotheses) > e.cfg.MaxHypotheses {
		hypotheses = hypotheses[:e.cfg.MaxHypotheses]
	}
	for i := range hypotheses {
		hypotheses[i].Rank = i + 1
	}
	state.hypotheses = hypotheses
	return nil
}

func (e *WorkflowEngine) stepCollectEvidence(_ context.Context, state *workflowState) error {
	evidence := make([]RCAEvidence, 0, 24)
	for _, signal := range topTriggeredSignals(state.riskSignals, 8) {
		evidence = append(evidence, RCAEvidence{
			ID:         fmt.Sprintf("ev-%s", sanitizeID(signal.ID)),
			Kind:       "metric_delta",
			Source:     "metrics_query",
			Scope:      signal.Scope,
			Entity:     signal.Entity,
			Summary:    fmt.Sprintf("%s moved from baseline %.3f to %.3f", signal.Name, signal.Baseline, signal.Current),
			MetricName: signal.Name,
			Value:      signal.Current,
			Baseline:   signal.Baseline,
			Delta:      signal.DeltaPercent,
			Timestamp:  signal.LastObservedAt,
		})
	}

	for idx, snippet := range state.logsData.Snippets {
		evidence = append(evidence, RCAEvidence{
			ID:        fmt.Sprintf("ev-log-%d", idx+1),
			Kind:      "log_snippet",
			Source:    "log_query",
			Scope:     "service",
			Entity:    firstNonEmpty(state.collectorID, "fleet"),
			Summary:   "recent log anomaly",
			Snippet:   snippet,
			Timestamp: state.now,
		})
		if idx >= 5 {
			break
		}
	}

	for idx, co := range state.cooccurrences {
		evidence = append(evidence, RCAEvidence{
			ID:        fmt.Sprintf("ev-corr-%d", idx+1),
			Kind:      "correlation",
			Source:    "joint_risk",
			Scope:     co.Scope,
			Entity:    co.Entity,
			Summary:   co.Explanation,
			Timestamp: state.now,
		})
		state.corr = append(state.corr, RCACorrelation{
			ID:          co.ID,
			Scope:       co.Scope,
			Entity:      co.Entity,
			Signals:     append([]string{}, co.Signals...),
			Coefficient: co.Correlation,
			Summary:     co.Explanation,
		})
	}

	for i, event := range state.ebpf.RuntimeEvents {
		evidence = append(evidence, RCAEvidence{
			ID:        firstNonEmpty(event.EvidenceID, fmt.Sprintf("ev-ebpf-%d", i+1)),
			Kind:      "runtime_security_event",
			Source:    "ebpf_query",
			Scope:     firstNonEmpty(event.NodeScope, "node"),
			Entity:    firstNonEmpty(event.PID, state.collectorID),
			Summary:   firstNonEmpty(event.Description, fmt.Sprintf("%s %s", event.Category, event.Type)),
			Snippet:   fmt.Sprintf("severity=%s confidence=%.2f category=%s type=%s", event.Severity, event.Confidence, event.Category, event.Type),
			Timestamp: event.Timestamp,
		})
		if i >= 11 {
			break
		}
	}

	assignEvidenceToHypotheses(state.hypotheses, evidence)
	state.evidence = evidence
	return nil
}

func (e *WorkflowEngine) stepRCARecommendations(_ context.Context, state *workflowState) error {
	recs := make([]WorkflowRecommendation, 0, 12)
	for _, hypothesis := range state.hypotheses {
		recs = append(recs, WorkflowRecommendation{
			ID:               fmt.Sprintf("rca-check-%d", hypothesis.Rank),
			Priority:         priorityForConfidence(hypothesis.Confidence),
			Summary:          fmt.Sprintf("Validate hypothesis: %s", hypothesis.Title),
			Details:          hypothesis.Description,
			Checks:           checksForHypothesis(hypothesis.Title),
			Safe:             true,
			DryRunDefault:    true,
			RequiresApproval: false,
			Reversible:       true,
			RollbackHint:     "read-only validation step",
		})
	}

	if len(state.hypotheses) == 0 {
		recs = append(recs, WorkflowRecommendation{
			ID:               "rca-check-fallback",
			Priority:         "medium",
			Summary:          "Expand evidence window and collect additional telemetry",
			Details:          "current evidence does not strongly support a single hypothesis",
			Checks:           []string{"increase window to 2h", "capture process-level CPU/IO counters", "refresh topology snapshot"},
			Safe:             true,
			DryRunDefault:    true,
			RequiresApproval: false,
			Reversible:       true,
			RollbackHint:     "none",
		})
	}

	state.recommendation = dedupeRecommendations(recs)
	if len(state.recommendation) > 12 {
		state.recommendation = state.recommendation[:12]
	}

	for _, rec := range state.recommendation {
		e.audit(state.workflowID, state.workflowType, "recommendation_generation", "recommendation.generated", "success", state.collectorID, state.dryRun, rec.RequiresApproval, true,
			map[string]string{"recommendation_id": rec.ID, "priority": rec.Priority},
			rec.Summary,
			nil,
		)
	}
	return nil
}

func (e *WorkflowEngine) stepGuardedExecutionPlan(ctx context.Context, state *workflowState) error {
	profileResult, err := state.callTool(ctx, "guarded_execution_plan", ToolProfiling, map[string]string{
		"reason": "rca evidence collection",
	})
	if err != nil {
		state.limitations = append(state.limitations, "profiling tool unavailable")
		return nil
	}
	profile, ok := profileResult.Data.(profilingToolData)
	if !ok {
		return nil
	}
	state.profiling = profile
	state.recommendation = append(state.recommendation, WorkflowRecommendation{
		ID:               "rca-profile-capture",
		Priority:         "medium",
		Summary:          "Capture bounded profile for final confirmation",
		Details:          fmt.Sprintf("Command: %s", profile.Command),
		Checks:           []string{"run only during active incident", "store artifact with incident ID"},
		Safe:             false,
		DryRunDefault:    true,
		RequiresApproval: e.cfg.RequireApproval,
		Reversible:       true,
		RollbackHint:     "stop capture and remove temporary profile files",
	})
	e.audit(state.workflowID, state.workflowType, "guarded_execution_plan", "guarded.action_planned", "success", state.collectorID, state.dryRun, e.cfg.RequireApproval, false,
		map[string]string{"command": profile.Command},
		profile.Message,
		nil,
	)

	action := "targeted_restart_candidate"
	if len(state.hypotheses) > 0 {
		action = remediationActionForHypothesis(state.hypotheses[0].Title)
	}
	remediationResult, remediationErr := state.callTool(ctx, "guarded_execution_plan", ToolRemediation, map[string]string{
		"action": action,
		"scope":  firstNonEmpty(state.collectorID, "fleet"),
	})
	if remediationErr != nil {
		state.limitations = append(state.limitations, "remediation planning unavailable")
		return nil
	}
	remediationData, ok := remediationResult.Data.(remediationToolData)
	if !ok {
		return nil
	}
	state.recommendation = append(state.recommendation, WorkflowRecommendation{
		ID:               "rca-remediation-plan",
		Priority:         "medium",
		Summary:          fmt.Sprintf("Guarded remediation plan: %s", remediationData.Action),
		Details:          remediationData.Summary,
		Checks:           []string{"run dry-run first", "require explicit approval", "execute single scoped change", "validate rollback plan before execution"},
		Safe:             false,
		DryRunDefault:    true,
		RequiresApproval: true,
		Reversible:       remediationData.Reversible,
		RollbackHint:     remediationData.RollbackPlan,
	})
	e.audit(state.workflowID, state.workflowType, "guarded_execution_plan", "guarded.remediation_planned", "success", state.collectorID, state.dryRun, true, false,
		map[string]string{"action": remediationData.Action},
		remediationData.Summary,
		nil,
	)
	return nil
}

func (e *WorkflowEngine) stepFinalizeRCA(_ context.Context, state *workflowState) error {
	mergeLLMIntoState(state)
	sort.Slice(state.hypotheses, func(i, j int) bool {
		if state.hypotheses[i].Confidence == state.hypotheses[j].Confidence {
			return state.hypotheses[i].Title < state.hypotheses[j].Title
		}
		return state.hypotheses[i].Confidence > state.hypotheses[j].Confidence
	})
	for i := range state.hypotheses {
		state.hypotheses[i].Rank = i + 1
	}
	if len(state.hypotheses) > e.cfg.MaxHypotheses {
		state.hypotheses = state.hypotheses[:e.cfg.MaxHypotheses]
	}

	for i := range state.hypotheses {
		if len(state.hypotheses[i].EvidenceIDs) > 10 {
			state.hypotheses[i].EvidenceIDs = state.hypotheses[i].EvidenceIDs[:10]
		}
	}

	repro := map[string]string{
		"pipeline":          workflowPipelineVersion,
		"workflow_type":     state.workflowType,
		"collector_id":      state.collectorID,
		"window":            state.window.String(),
		"dry_run":           strconv.FormatBool(state.dryRun),
		"tool_calls":        strconv.Itoa(len(state.toolCalls)),
		"deterministic":     "true",
		"timestamp_utc":     state.now.Format(time.RFC3339Nano),
		"insights_provider": e.cfg.InsightsProvider,
	}

	if strings.TrimSpace(state.trigger) == "" {
		state.trigger = "anomaly"
	}
	structured := buildStructuredRCAReport(state)
	incidentStatus := "open"
	if len(state.hypotheses) == 0 && strings.Contains(strings.ToLower(strings.Join(state.rca.Anomalies, " ")), "no strong anomalies") {
		incidentStatus = "closed"
	}
	incidentID := fmt.Sprintf("inc-%s", sanitizeID(state.workflowID))
	state.rca = RCAWorkflowReport{
		WorkflowID:      state.workflowID,
		PipelineVersion: workflowPipelineVersion,
		IncidentID:      incidentID,
		Status:          incidentStatus,
		CollectorID:     state.collectorID,
		Trigger:         state.trigger,
		GeneratedAt:     state.now,
		Context:         state.rca.Context,
		Anomalies:       append([]string{}, state.rca.Anomalies...),
		Correlations:    append([]RCACorrelation{}, state.corr...),
		Hypotheses:      append([]RCAHypothesis{}, state.hypotheses...),
		Evidence:        append([]RCAEvidence{}, state.evidence...),
		Recommendations: dedupeRecommendations(state.recommendation),
		AgentLoop: AgentLoopSummary{
			Mode:          "plan_act_verify",
			Iterations:    maxInt(state.planIterations, 1),
			Replans:       state.planReplans,
			StepsPlanned:  len(state.planSteps),
			StepsExecuted: state.stepsExecuted,
			StepsVerified: state.stepsVerified,
			Completed:     state.planCompleted,
			StopReason:    firstNonEmpty(state.planStopReason, "completed"),
			PlanSteps:     append([]AgentPlanStep{}, state.planSteps...),
			PlanRevisions: append([]AgentPlanRevision{}, state.planRevisions...),
		},
		StructuredReport: structured,
		Stages:           append([]PipelineStageResult{}, state.stages...),
		ToolCalls:        append([]WorkflowToolCall{}, state.toolCalls...),
		Reproducibility:  repro,
		Limitations:      dedupeStrings(state.limitations),
		Insights:         e.insightsStatus(),
		LLMAnalysis:      state.llmAnalysis,
	}
	return nil
}

func buildStructuredRCAReport(state *workflowState) RCAStructuredReport {
	if state == nil {
		return RCAStructuredReport{}
	}
	symptoms := dedupeStrings(append([]string{}, state.rca.Anomalies...))
	scope := []string{}
	for _, row := range state.scopeRisks {
		if strings.TrimSpace(row.Entity) == "" || strings.EqualFold(row.Entity, "n/a") {
			continue
		}
		scope = append(scope, fmt.Sprintf("%s/%s", row.Scope, row.Entity))
	}
	scope = dedupeStrings(scope)
	if len(scope) > 8 {
		scope = scope[:8]
	}

	mostLikely := "insufficient evidence"
	confidence := 0.35
	if len(state.hypotheses) > 0 {
		mostLikely = state.hypotheses[0].Title
		confidence = state.hypotheses[0].Confidence
	}
	supporting := []string{}
	for _, signal := range topTriggeredSignals(state.riskSignals, 6) {
		supporting = append(supporting, fmt.Sprintf("%s (delta %.1f%%)", signal.Name, signal.DeltaPercent))
	}
	disconfirming := []string{}
	for _, signal := range state.riskSignals {
		if signal.Triggered {
			continue
		}
		if signal.Score >= 0.04 {
			disconfirming = append(disconfirming, fmt.Sprintf("%s remained near baseline", signal.Name))
		}
	}
	if len(disconfirming) > 6 {
		disconfirming = disconfirming[:6]
	}

	timeline := make([]RCATimelineEvent, 0, len(state.stages)+len(state.planSteps))
	for _, stage := range state.stages {
		ts := stage.CompletedAt
		if ts.IsZero() {
			ts = stage.StartedAt
		}
		timeline = append(timeline, RCATimelineEvent{
			Timestamp: ts,
			Phase:     stage.Name,
			Summary:   truncateString(stage.Summary, 220),
		})
	}
	for _, step := range state.planSteps {
		ts := step.CompletedAt
		if ts.IsZero() {
			ts = step.StartedAt
		}
		timeline = append(timeline, RCATimelineEvent{
			Timestamp: ts,
			Phase:     "plan_step",
			Summary:   truncateString(fmt.Sprintf("%s [%s] %s", step.Title, step.Status, step.VerificationNote), 220),
		})
	}
	sort.Slice(timeline, func(i, j int) bool {
		return timeline[i].Timestamp.Before(timeline[j].Timestamp)
	})

	return RCAStructuredReport{
		Symptoms:             symptoms,
		Timeline:             timeline,
		Scope:                scope,
		MostLikelyCause:      mostLikely,
		SupportingSignals:    dedupeStrings(supporting),
		DisconfirmingSignals: dedupeStrings(disconfirming),
		Confidence:           clamp01(confidence),
	}
}

func (e *WorkflowEngine) recordJointRisk(report JointRiskAssessment) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.riskReports = append([]JointRiskAssessment{report}, e.riskReports...)
	if len(e.riskReports) > e.cfg.AuditRetention {
		e.riskReports = e.riskReports[:e.cfg.AuditRetention]
	}
}

func (e *WorkflowEngine) recordRCA(report RCAWorkflowReport) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rcaReports = append([]RCAWorkflowReport{report}, e.rcaReports...)
	if len(e.rcaReports) > e.cfg.AuditRetention {
		e.rcaReports = e.rcaReports[:e.cfg.AuditRetention]
	}
}

func (e *WorkflowEngine) recordIncident(report RCAWorkflowReport) {
	if e == nil {
		return
	}
	openedAt := report.GeneratedAt
	if openedAt.IsZero() {
		openedAt = time.Now().UTC()
	}
	mostLikely := strings.TrimSpace(report.StructuredReport.MostLikelyCause)
	if mostLikely == "" && len(report.Hypotheses) > 0 {
		mostLikely = report.Hypotheses[0].Title
	}
	confidence := report.StructuredReport.Confidence
	if confidence <= 0 && len(report.Hypotheses) > 0 {
		confidence = report.Hypotheses[0].Confidence
	}
	status := strings.TrimSpace(report.Status)
	if status == "" {
		status = "open"
	}
	summary := "agentic investigation generated"
	if len(report.Anomalies) > 0 {
		summary = truncateString(report.Anomalies[0], 220)
	}
	incidentID := strings.TrimSpace(report.IncidentID)
	if incidentID == "" {
		incidentID = fmt.Sprintf("inc-%s", sanitizeID(report.WorkflowID))
	}
	timeline := append([]RCATimelineEvent{}, report.StructuredReport.Timeline...)
	if len(timeline) == 0 {
		for _, stage := range report.Stages {
			eventTime := stage.CompletedAt
			if eventTime.IsZero() {
				eventTime = stage.StartedAt
			}
			timeline = append(timeline, RCATimelineEvent{
				Timestamp: eventTime,
				Phase:     stage.Name,
				Summary:   truncateString(stage.Summary, 220),
			})
		}
	}
	record := AgentIncidentReport{
		IncidentID:      incidentID,
		WorkflowID:      report.WorkflowID,
		Status:          status,
		Source:          firstNonEmpty(report.Trigger, "agentic_rca"),
		CollectorID:     report.CollectorID,
		OpenedAt:        openedAt,
		RiskLevel:       riskLevelFromConfidence(confidence),
		RiskScore:       clamp01(confidence),
		Summary:         summary,
		MostLikelyCause: mostLikely,
		Confidence:      confidence,
		Symptoms:        append([]string{}, report.Anomalies...),
		Timeline:        timeline,
		Evidence:        append([]RCAEvidence{}, report.Evidence...),
		Hypotheses:      append([]RCAHypothesis{}, report.Hypotheses...),
		Recommendations: append([]WorkflowRecommendation{}, report.Recommendations...),
		AgentLoop:       report.AgentLoop,
		LLMAnalysis:     report.LLMAnalysis,
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	updated := false
	for i := range e.incidents {
		if strings.EqualFold(e.incidents[i].IncidentID, record.IncidentID) {
			e.incidents[i] = record
			updated = true
			break
		}
	}
	if !updated {
		e.incidents = append([]AgentIncidentReport{record}, e.incidents...)
	}
	if len(e.incidents) > e.cfg.IncidentRetention {
		e.incidents = e.incidents[:e.cfg.IncidentRetention]
	}
}

func (e *WorkflowEngine) audit(
	workflowID, workflowType, stage, action, status, collectorID string,
	dryRun, requiresApproval, approved bool,
	input map[string]string,
	summary string,
	err error,
) {
	record := WorkflowAuditRecord{
		ID:               newQueryID(),
		WorkflowID:       workflowID,
		WorkflowType:     workflowType,
		Stage:            stage,
		Action:           action,
		CollectorID:      collectorID,
		DryRun:           dryRun,
		RequiresApproval: requiresApproval,
		Approved:         approved,
		Status:           status,
		Input:            cloneStringMap(input),
		OutputSummary:    truncateString(summary, 220),
		Timestamp:        time.Now().UTC(),
	}
	if err != nil {
		record.ErrorMessage = truncateString(err.Error(), 220)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.audits = append([]WorkflowAuditRecord{record}, e.audits...)
	if len(e.audits) > e.cfg.AuditRetention {
		e.audits = e.audits[:e.cfg.AuditRetention]
	}
}

func (e *WorkflowEngine) insightsStatus() WorkflowInsightsStatus {
	keyEnv := strings.TrimSpace(e.cfg.InsightsAPIKeyEnv)
	keyConfigured := false
	if keyEnv != "" {
		keyConfigured = strings.TrimSpace(os.Getenv(keyEnv)) != ""
	}
	mode := "disabled"
	if e.cfg.InsightsEnabled {
		mode = "active"
		if !keyConfigured {
			mode = "stub"
		}
	}
	return WorkflowInsightsStatus{
		Enabled:          e.cfg.InsightsEnabled,
		Provider:         e.cfg.InsightsProvider,
		Model:            e.cfg.InsightsModel,
		APIKeyEnv:        keyEnv,
		APIKeyConfigured: keyConfigured,
		Mode:             mode,
	}
}

func latestToolVersion(calls []WorkflowToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	return strings.TrimSpace(calls[len(calls)-1].ToolVersion)
}

func remediationActionForHypothesis(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	switch {
	case strings.Contains(title, "cpu"):
		return "scale_or_rebalance_cpu"
	case strings.Contains(title, "memory"):
		return "restart_memory_hotspot_workload"
	case strings.Contains(title, "io"), strings.Contains(title, "storage"):
		return "reduce_io_contention"
	case strings.Contains(title, "network"), strings.Contains(title, "retransmit"):
		return "isolate_network_hotspot"
	case strings.Contains(title, "security"):
		return "lockdown_misconfigured_workload"
	default:
		return "targeted_restart_candidate"
	}
}

func (e *WorkflowEngine) collectorIDs(limit int) []string {
	if e == nil || e.store == nil {
		return nil
	}
	nodes := e.store.Snapshot()
	type row struct {
		id        string
		updatedAt time.Time
	}
	rows := make([]row, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		id := strings.TrimSpace(node.CollectorID)
		if id == "" {
			continue
		}
		rows = append(rows, row{id: id, updatedAt: node.UpdatedAt})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].updatedAt.After(rows[j].updatedAt)
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]string, 0, len(rows))
	for _, item := range rows {
		out = append(out, item.id)
	}
	return out
}

// ProcessTree returns the runtime process graph.
func (e *WorkflowEngine) ProcessTree() *ProcessTree {
	if e == nil {
		return nil
	}
	return e.processTree
}

// BaselineEngine returns the behavioral baseline engine.
func (e *WorkflowEngine) BaselineEngine() *BaselineEngine {
	if e == nil {
		return nil
	}
	return e.baseline
}

// TraceStore returns the agent trace store.
func (e *WorkflowEngine) TraceStoreRef() *TraceStore {
	if e == nil {
		return nil
	}
	return e.traceStore
}

// ProposedActionStore returns the proposed action store.
func (e *WorkflowEngine) ProposedActionStoreRef() *ProposedActionStore {
	if e == nil {
		return nil
	}
	return e.proposedActions
}

func topFindingScope(scopes []ScopeRisk, collectorID string) string {
	bestScope := ""
	bestEntity := ""
	bestScore := -1.0
	for _, scope := range scopes {
		entity := strings.TrimSpace(scope.Entity)
		if entity == "" || strings.EqualFold(entity, "n/a") {
			continue
		}
		if scope.Score > bestScore {
			bestScore = scope.Score
			bestScope = strings.TrimSpace(scope.Scope)
			bestEntity = entity
		}
	}
	if bestScope != "" && bestEntity != "" {
		return fmt.Sprintf("%s/%s", bestScope, bestEntity)
	}
	if strings.TrimSpace(collectorID) != "" {
		return "node/" + collectorID
	}
	return "node/fleet"
}

func riskLevelFromConfidence(confidence float64) string {
	switch {
	case confidence >= 0.72:
		return "high"
	case confidence >= 0.45:
		return "medium"
	default:
		return "low"
	}
}

func hasBehavioralAnomaly(signals []JointRiskSignal) bool {
	for _, signal := range signals {
		if !signal.Triggered {
			continue
		}
		id := strings.ToLower(signal.ID)
		if strings.Contains(id, "ebpf") || strings.Contains(id, "security") || strings.Contains(id, "behavior") {
			return true
		}
	}
	return false
}

func hasResourceAnomaly(signals []JointRiskSignal) bool {
	for _, signal := range signals {
		if !signal.Triggered {
			continue
		}
		id := strings.ToLower(signal.ID)
		if strings.Contains(id, "cpu") ||
			strings.Contains(id, "memory") ||
			strings.Contains(id, "io") ||
			strings.Contains(id, "retransmit") ||
			strings.Contains(id, "softnet") {
			return true
		}
	}
	return false
}

func (e *WorkflowEngine) shouldEscalateFromJointRisk(report JointRiskAssessment) bool {
	if e == nil || !e.cfg.AutoEscalateOnHighRisk {
		return false
	}
	if !strings.EqualFold(report.RiskLevel, "high") {
		return false
	}
	if report.RiskScore < e.cfg.HighRiskScoreThreshold {
		return false
	}
	collectorID := strings.TrimSpace(report.CollectorID)
	if collectorID == "" {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, incident := range e.incidents {
		if !strings.EqualFold(incident.CollectorID, collectorID) {
			continue
		}
		if !strings.EqualFold(incident.Status, "open") {
			continue
		}
		if time.Since(incident.OpenedAt) <= 15*time.Minute {
			return false
		}
	}
	return true
}

func buildRiskSeries(history []ingest.MetricHistorySample, logs logsToolData) []RiskSeries {
	if len(history) == 0 {
		return []RiskSeries{}
	}

	type spec struct {
		key     string
		display string
		unit    string
		extract func(map[string]float64) (float64, bool)
	}

	specs := []spec{
		{key: "cpu_pressure", display: "CPU usage", unit: "percent", extract: extractMetric("node_cpu_usage_percent")},
		{key: "memory_pressure", display: "Memory usage", unit: "percent", extract: extractMemoryPercent},
		{key: "io_latency", display: "IO latency p99", unit: "milliseconds", extract: extractIOLatencyMS},
		{key: "io_pressure", display: "IO pressure full avg10", unit: "percent", extract: extractMetric("node_pressure_io_full_avg10")},
		{key: "retransmit_ratio", display: "TCP retransmit ratio", unit: "ratio", extract: extractMetric("node_tcp_retransmit_ratio")},
		{key: "softnet_drop", display: "Softnet drops", unit: "count_per_second", extract: extractMetric("node_softnet_dropped_per_second")},
	}

	out := make([]RiskSeries, 0, len(specs)+1)
	for _, item := range specs {
		points := make([]RiskSeriesPoint, 0, len(history))
		for _, sample := range history {
			value, ok := item.extract(sample.Metrics)
			if !ok {
				continue
			}
			points = append(points, RiskSeriesPoint{Timestamp: sample.Timestamp, Value: value})
		}
		if len(points) < 3 {
			continue
		}
		baseline := baselineValue(points)
		latest := points[len(points)-1].Value
		acc := acceleration(points)
		out = append(out, RiskSeries{
			Key:          item.key,
			Display:      item.display,
			Unit:         item.unit,
			Latest:       latest,
			Baseline:     baseline,
			Acceleration: acc,
			Points:       points,
		})
	}

	if len(logs.Timeline) > 0 {
		points := make([]RiskSeriesPoint, 0, len(logs.Timeline))
		for _, bucket := range logs.Timeline {
			value := float64(bucket.Errors + bucket.Warnings)
			points = append(points, RiskSeriesPoint{Timestamp: bucket.End, Value: value})
		}
		if len(points) >= 3 {
			out = append(out, RiskSeries{
				Key:          "log_burst",
				Display:      "Error/warn burst",
				Unit:         "count",
				Latest:       points[len(points)-1].Value,
				Baseline:     baselineValue(points),
				Acceleration: acceleration(points),
				Points:       points,
			})
		}
	}

	if memorySeries := findSeriesByKey(out, "memory_pressure"); memorySeries != nil && len(memorySeries.Points) >= 4 {
		ratePoints := make([]RiskSeriesPoint, 0, len(memorySeries.Points)-1)
		for i := 1; i < len(memorySeries.Points); i++ {
			prev := memorySeries.Points[i-1]
			curr := memorySeries.Points[i]
			dtMin := curr.Timestamp.Sub(prev.Timestamp).Minutes()
			if dtMin <= 0 {
				continue
			}
			rate := (curr.Value - prev.Value) / dtMin
			if rate < 0 {
				rate = 0
			}
			ratePoints = append(ratePoints, RiskSeriesPoint{
				Timestamp: curr.Timestamp,
				Value:     rate,
			})
		}
		if len(ratePoints) >= 3 {
			out = append(out, RiskSeries{
				Key:          "memory_leak_rate",
				Display:      "Memory leak rate",
				Unit:         "percent_per_minute",
				Latest:       ratePoints[len(ratePoints)-1].Value,
				Baseline:     baselineValue(ratePoints),
				Acceleration: acceleration(ratePoints),
				Points:       ratePoints,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func buildRiskSignals(collectorID string, series []RiskSeries, security securityToolData, ebpf ebpfToolData) []JointRiskSignal {
	type threshold struct {
		medium float64
		high   float64
		weight float64
		scope  string
	}
	thresholds := map[string]threshold{
		"cpu_pressure":     {medium: 65, high: 88, weight: 0.16, scope: "node"},
		"memory_pressure":  {medium: 72, high: 90, weight: 0.14, scope: "node"},
		"memory_leak_rate": {medium: 0.04, high: 0.16, weight: 0.12, scope: "process"},
		"io_latency":       {medium: 20, high: 80, weight: 0.18, scope: "node"},
		"io_pressure":      {medium: 5, high: 20, weight: 0.12, scope: "node"},
		"retransmit_ratio": {medium: 0.005, high: 0.02, weight: 0.14, scope: "node"},
		"softnet_drop":     {medium: 1, high: 20, weight: 0.08, scope: "node"},
		"log_burst":        {medium: 8, high: 40, weight: 0.10, scope: "service"},
	}

	out := make([]JointRiskSignal, 0, len(series)+1)
	for _, item := range series {
		t, ok := thresholds[item.Key]
		if !ok {
			continue
		}
		thrScore := thresholdScore(item.Latest, t.medium, t.high)
		deltaPct := percentChange(item.Baseline, item.Latest)
		deltaScore := clamp01(deltaPct / 60.0)
		accScore := clamp01(item.Acceleration / maxFloat(math.Abs(item.Baseline)*0.15, 1.0))
		score := t.weight * (0.55*thrScore + 0.30*deltaScore + 0.15*accScore)
		triggered := thrScore > 0 || deltaScore >= 0.4
		sev := signalSeverity(score, t.weight)
		evidence := []string{
			fmt.Sprintf("latest=%.3f baseline=%.3f", item.Latest, item.Baseline),
			fmt.Sprintf("delta=%.1f%% acceleration=%.3f", deltaPct, item.Acceleration),
		}
		lastTS := item.Points[len(item.Points)-1].Timestamp
		out = append(out, JointRiskSignal{
			ID:             item.Key,
			Name:           item.Display,
			Scope:          t.scope,
			Entity:         firstNonEmpty(collectorID, "fleet"),
			Severity:       sev,
			Weight:         t.weight,
			Current:        item.Latest,
			Baseline:       item.Baseline,
			DeltaPercent:   deltaPct,
			Acceleration:   item.Acceleration,
			Score:          score,
			Triggered:      triggered,
			Evidence:       evidence,
			LastObservedAt: lastTS,
		})
	}

	if security.Score > 0 {
		delta := security.Score * 100
		triggered := security.Score >= 0.2
		evidence := append([]string{}, security.Findings...)
		evidence = append(evidence, security.SuspiciousPortCandidates...)
		evidence = append(evidence, security.WeakPermissionHints...)
		if len(evidence) > 5 {
			evidence = evidence[:5]
		}
		out = append(out, JointRiskSignal{
			ID:             "security_exposure",
			Name:           "Security exposure",
			Scope:          "node",
			Entity:         firstNonEmpty(collectorID, "fleet"),
			Severity:       signalSeverity(security.Score*0.08, 0.08),
			Weight:         0.08,
			Current:        security.Score,
			Baseline:       0,
			DeltaPercent:   delta,
			Acceleration:   0,
			Score:          clamp01(security.Score * 0.08),
			Triggered:      triggered,
			Evidence:       evidence,
			LastObservedAt: time.Now().UTC(),
		})
	}

	if ebpf.BehaviorScore > 0 {
		evidence := append([]string{}, ebpf.RuntimeEventSummaries...)
		evidence = append(evidence, ebpf.EvidenceIDs...)
		if len(evidence) > 8 {
			evidence = evidence[:8]
		}
		weight := 0.16
		score := clamp01(ebpf.BehaviorScore * weight)
		out = append(out, JointRiskSignal{
			ID:             "ebpf_behavior_anomaly",
			Name:           "eBPF behavior anomaly",
			Scope:          "runtime",
			Entity:         firstNonEmpty(collectorID, "fleet"),
			Severity:       signalSeverity(score, weight),
			Weight:         weight,
			Current:        ebpf.BehaviorScore,
			Baseline:       0.1,
			DeltaPercent:   percentChange(0.1, ebpf.BehaviorScore),
			Acceleration:   ebpf.EventRate,
			Score:          score,
			Triggered:      ebpf.BehaviorScore >= 0.25 || ebpf.EventRate >= 2,
			Evidence:       evidence,
			LastObservedAt: time.Now().UTC(),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func buildScopeRisks(data metricsToolData, signals []JointRiskSignal) []ScopeRisk {
	out := make([]ScopeRisk, 0, 12)
	nodeEntity := firstNonEmpty(data.CollectorID, "fleet")
	nodeScore := 0.0
	nodeSignals := make([]string, 0, 6)
	for _, signal := range signals {
		if signal.Scope == "node" {
			nodeScore += signal.Score
			if signal.Triggered {
				nodeSignals = append(nodeSignals, signal.Name)
			}
		}
	}
	out = append(out, ScopeRisk{
		Scope:       "node",
		Entity:      nodeEntity,
		Score:       clamp01(nodeScore),
		TopSignals:  dedupeStrings(nodeSignals),
		Explanation: "node-level weighted risk from CPU/memory/IO/network/security signals",
	})

	processAdded := false
	if data.Node != nil {
		for _, process := range topProcessResources(data.Node, 4) {
			score := clamp01(processPressureScore(process) / 100.0)
			signals := []string{}
			for category, total := range process.CategoryTotals {
				if total > 0 {
					signals = append(signals, category)
				}
			}
			if process.LogErrors > 0 || process.LogWarnings > 0 {
				signals = append(signals, "logs")
			}
			out = append(out, ScopeRisk{
				Scope:       "process",
				Entity:      processDisplayName(process),
				Score:       score,
				TopSignals:  dedupeStrings(signals),
				Explanation: "process-level attribution from per-process CPU/IO/network/log counters",
			})
			processAdded = true
		}
	}
	if !processAdded {
		out = append(out, ScopeRisk{Scope: "process", Entity: "n/a", Score: 0, Explanation: "no process attribution available"})
	}

	podScores := aggregateScopeFromProcesses(data.Node, func(p *ingest.ProcessResourceSample) string {
		return strings.TrimSpace(p.PodUID)
	})
	if len(podScores) == 0 {
		out = append(out, ScopeRisk{Scope: "pod", Entity: "n/a", Score: 0, Explanation: "pod attribution unavailable"})
	} else {
		for _, row := range podScores {
			out = append(out, row)
		}
	}

	serviceScores := aggregateScopeFromProcesses(data.Node, func(p *ingest.ProcessResourceSample) string {
		return firstNonEmpty(strings.TrimSpace(p.Job), strings.TrimSpace(p.Name))
	})
	if len(serviceScores) == 0 {
		out = append(out, ScopeRisk{Scope: "service", Entity: "n/a", Score: 0, Explanation: "service attribution unavailable"})
	} else {
		for _, row := range serviceScores {
			row.Scope = "service"
			out = append(out, row)
		}
	}

	if len(data.Fleet) > 0 {
		clusterScore := 0.0
		hotNodes := []string{}
		for _, node := range data.Fleet {
			if node == nil {
				continue
			}
			score := simpleNodePressureScore(node.Metrics)
			clusterScore += score
			if score >= 0.45 {
				hotNodes = append(hotNodes, firstNonEmpty(node.CollectorID, node.Hostname))
			}
		}
		clusterScore = clusterScore / maxFloat(float64(len(data.Fleet)), 1)
		out = append(out, ScopeRisk{
			Scope:       "cluster",
			Entity:      "fleet",
			Score:       clamp01(clusterScore),
			TopSignals:  hotNodes,
			Explanation: "cluster risk aggregated across node pressure snapshots",
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope == out[j].Scope {
			return out[i].Score > out[j].Score
		}
		return out[i].Scope < out[j].Scope
	})
	if len(out) > 18 {
		out = out[:18]
	}
	return out
}

func aggregateScopeFromProcesses(node *ingest.NodeSnapshot, keyFn func(*ingest.ProcessResourceSample) string) []ScopeRisk {
	if node == nil || len(node.ProcessResources) == 0 || keyFn == nil {
		return nil
	}
	totals := map[string]float64{}
	counts := map[string]int{}
	signals := map[string][]string{}
	for _, process := range node.ProcessResources {
		if process == nil {
			continue
		}
		key := strings.TrimSpace(keyFn(process))
		if key == "" {
			continue
		}
		score := processPressureScore(process) / 100
		totals[key] += score
		counts[key]++
		for category, value := range process.CategoryTotals {
			if value > 0 {
				signals[key] = append(signals[key], category)
			}
		}
	}
	rows := make([]ScopeRisk, 0, len(totals))
	for entity, total := range totals {
		count := counts[entity]
		score := total / maxFloat(float64(count), 1)
		rows = append(rows, ScopeRisk{
			Scope:       "pod",
			Entity:      entity,
			Score:       clamp01(score),
			TopSignals:  dedupeStrings(signals[entity]),
			Explanation: "aggregated from attributed process signals",
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].Entity < rows[j].Entity
		}
		return rows[i].Score > rows[j].Score
	})
	if len(rows) > 4 {
		rows = rows[:4]
	}
	return rows
}

func buildCooccurrences(signals []JointRiskSignal, series []RiskSeries, window time.Duration, collectorID string) []JointRiskCooccurrence {
	active := topTriggeredSignals(signals, 8)
	if len(active) < 2 {
		return nil
	}
	valuesByKey := map[string][]float64{}
	for _, item := range series {
		vals := make([]float64, 0, len(item.Points))
		for _, point := range item.Points {
			vals = append(vals, point.Value)
		}
		valuesByKey[item.Key] = vals
	}

	out := make([]JointRiskCooccurrence, 0, 8)
	for i := 0; i < len(active); i++ {
		for j := i + 1; j < len(active); j++ {
			a := active[i]
			b := active[j]
			corr := pairCorrelation(valuesByKey[a.ID], valuesByKey[b.ID])
			if math.Abs(corr) < 0.35 {
				corr = fallbackSignalCorrelation(a, b)
			}
			if math.Abs(corr) < 0.30 {
				continue
			}
			combined := clamp01((a.Score + b.Score) * (1 + math.Abs(corr)*0.25))
			out = append(out, JointRiskCooccurrence{
				ID:            fmt.Sprintf("co-%s-%s", sanitizeID(a.ID), sanitizeID(b.ID)),
				Scope:         "node",
				Entity:        firstNonEmpty(collectorID, "fleet"),
				Window:        window.String(),
				Signals:       []string{a.Name, b.Name},
				Correlation:   corr,
				CombinedScore: combined,
				Explanation: fmt.Sprintf("%s and %s co-move in the same risk window (corr=%.2f)",
					a.Name, b.Name, corr),
				ActionableCause: fmt.Sprintf("combined risk is high because signals %s+%s co-occurred within window %s on scope node/%s",
					a.Name, b.Name, window.String(), firstNonEmpty(collectorID, "fleet")),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CombinedScore == out[j].CombinedScore {
			return out[i].ID < out[j].ID
		}
		return out[i].CombinedScore > out[j].CombinedScore
	})
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func buildFallbackCooccurrence(signals []JointRiskSignal, window time.Duration, collectorID string) []JointRiskCooccurrence {
	active := topTriggeredSignals(signals, 3)
	if len(active) < 2 {
		return nil
	}
	names := make([]string, 0, len(active))
	total := 0.0
	for _, signal := range active {
		names = append(names, signal.Name)
		total += signal.Score
	}
	return []JointRiskCooccurrence{{
		ID:            "co-fallback",
		Scope:         "node",
		Entity:        firstNonEmpty(collectorID, "fleet"),
		Window:        window.String(),
		Signals:       names,
		Correlation:   0.3,
		CombinedScore: clamp01(total),
		Explanation:   "multiple low-severity signals were active in the same window",
		ActionableCause: fmt.Sprintf("combined risk is elevated because signals %s co-occurred within window %s on scope node/%s",
			strings.Join(names, "+"), window.String(), firstNonEmpty(collectorID, "fleet")),
	}}
}

func topTriggeredSignals(signals []JointRiskSignal, limit int) []JointRiskSignal {
	rows := make([]JointRiskSignal, 0, len(signals))
	for _, signal := range signals {
		if signal.Triggered {
			rows = append(rows, signal)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Score > rows[j].Score
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func topRiskSignals(signals []JointRiskSignal, limit int) []JointRiskSignal {
	rows := append([]JointRiskSignal(nil), signals...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Score > rows[j].Score
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func topMetricMap(metrics map[string]float64, limit int) map[string]float64 {
	if len(metrics) == 0 {
		return map[string]float64{}
	}
	type kv struct {
		key   string
		value float64
	}
	rows := make([]kv, 0, len(metrics))
	for key, value := range metrics {
		rows = append(rows, kv{key: key, value: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		return math.Abs(rows[i].value) > math.Abs(rows[j].value)
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		out[row.key] = row.value
	}
	return out
}

func assignEvidenceToHypotheses(hypotheses []RCAHypothesis, evidence []RCAEvidence) {
	for idx := range hypotheses {
		ids := make([]string, 0, 6)
		title := strings.ToLower(hypotheses[idx].Title)
		for _, item := range evidence {
			summary := strings.ToLower(item.Summary)
			snippet := strings.ToLower(item.Snippet)
			metric := strings.ToLower(item.MetricName)
			if strings.Contains(summary, title) || strings.Contains(snippet, title) || strings.Contains(metric, title) {
				ids = append(ids, item.ID)
				continue
			}
			if strings.Contains(title, "cpu") && strings.Contains(metric, "cpu") {
				ids = append(ids, item.ID)
			} else if strings.Contains(title, "memory") && strings.Contains(metric, "memory") {
				ids = append(ids, item.ID)
			} else if strings.Contains(title, "io") && (strings.Contains(metric, "io") || strings.Contains(metric, "disk")) {
				ids = append(ids, item.ID)
			} else if strings.Contains(title, "network") && (strings.Contains(metric, "net") || strings.Contains(metric, "retransmit")) {
				ids = append(ids, item.ID)
			} else if strings.Contains(title, "deploy") && strings.Contains(snippet, "deploy") {
				ids = append(ids, item.ID)
			} else if strings.Contains(title, "security") && strings.Contains(summary, "security") {
				ids = append(ids, item.ID)
			}
		}
		if len(ids) > 0 {
			hypotheses[idx].EvidenceIDs = dedupeStrings(ids)
		}
	}
}

func dedupeRecommendations(in []WorkflowRecommendation) []WorkflowRecommendation {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]WorkflowRecommendation, 0, len(in))
	for _, item := range in {
		key := strings.TrimSpace(item.ID)
		if key == "" {
			key = strings.TrimSpace(item.Summary)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func hypothesisTitleFromSignal(signal string) string {
	low := strings.ToLower(signal)
	switch {
	case strings.Contains(low, "cpu"):
		return "cpu scheduling contention"
	case strings.Contains(low, "memory"):
		return "memory pressure and reclaim"
	case strings.Contains(low, "latency") || strings.Contains(low, "io"):
		return "storage io bottleneck"
	case strings.Contains(low, "retransmit") || strings.Contains(low, "softnet"):
		return "network congestion or packet loss"
	case strings.Contains(low, "log"):
		return "service-level error burst"
	case strings.Contains(low, "security"):
		return "security exposure or permission drift"
	default:
		return "distributed resource contention"
	}
}

func checksForHypothesis(title string) []string {
	low := strings.ToLower(title)
	switch {
	case strings.Contains(low, "cpu"):
		return []string{"inspect run queue and blocked tasks", "verify hottest processes and throttling", "compare recent deployment CPU requests"}
	case strings.Contains(low, "memory"):
		return []string{"inspect top RSS processes", "check swap/oom counters", "verify memory limits and working set"}
	case strings.Contains(low, "storage") || strings.Contains(low, "io"):
		return []string{"inspect disk queue depth and latency", "identify io-heavy processes", "check writeback/page-cache pressure"}
	case strings.Contains(low, "network"):
		return []string{"inspect retransmit/drop counters", "validate pod/service topology hops", "check recent connection churn"}
	case strings.Contains(low, "deploy"):
		return []string{"verify rollout status", "compare version-specific errors", "consider canary rollback in dry-run"}
	case strings.Contains(low, "security"):
		return []string{"review suspicious open ports", "verify permissions and policy changes", "check audit log anomalies"}
	default:
		return []string{"extend evidence window", "collect process + kernel signals", "validate topology blast radius"}
	}
}

func priorityForSeverity(severity string) string {
	switch strings.ToLower(severity) {
	case "high", "critical":
		return "high"
	case "medium", "warning":
		return "medium"
	default:
		return "low"
	}
}

func priorityForConfidence(confidence float64) string {
	switch {
	case confidence >= 0.75:
		return "high"
	case confidence >= 0.45:
		return "medium"
	default:
		return "low"
	}
}

func signalSeverity(score, weight float64) string {
	normalized := 0.0
	if weight > 0 {
		normalized = score / weight
	}
	switch {
	case normalized >= 0.75:
		return "high"
	case normalized >= 0.35:
		return "medium"
	default:
		return "low"
	}
}

func simpleNodePressureScore(metrics map[string]float64) float64 {
	cpu := metricValue(metrics, "node_cpu_usage_percent")
	mem := 0.0
	if total := metricValue(metrics, "node_memory_MemTotal_bytes"); total > 0 {
		used := metricValue(metrics, "node_memory_Used_bytes")
		if used <= 0 {
			avail := metricValue(metrics, "node_memory_MemAvailable_bytes")
			if avail > 0 {
				used = total - avail
			}
		}
		if used > 0 {
			mem = clampPercent(used / total * 100)
		}
	}
	ioLat := metricValue(metrics, "node_disk_request_latency_p99_seconds") * 1000
	retrans := metricValue(metrics, "node_tcp_retransmit_ratio")
	return clamp01(
		0.30*thresholdScore(cpu, 65, 88) +
			0.26*thresholdScore(mem, 72, 90) +
			0.24*thresholdScore(ioLat, 20, 80) +
			0.20*thresholdScore(retrans, 0.005, 0.02),
	)
}

func baselineValue(points []RiskSeriesPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	split := len(points) * 2 / 3
	if split <= 1 {
		split = len(points) - 1
	}
	if split <= 0 {
		return points[0].Value
	}
	sum := 0.0
	for i := 0; i < split; i++ {
		sum += points[i].Value
	}
	return sum / float64(split)
}

func acceleration(points []RiskSeriesPoint) float64 {
	if len(points) < 3 {
		return 0
	}
	last := points[len(points)-1]
	prev := points[len(points)-2]
	before := points[len(points)-3]
	step1 := last.Value - prev.Value
	step0 := prev.Value - before.Value
	return step1 - step0
}

func findSeriesByKey(series []RiskSeries, key string) *RiskSeries {
	for i := range series {
		if strings.EqualFold(strings.TrimSpace(series[i].Key), strings.TrimSpace(key)) {
			return &series[i]
		}
	}
	return nil
}

func pairCorrelation(a, b []float64) float64 {
	n := minInt(len(a), len(b))
	if n < 3 {
		return 0
	}
	a = a[len(a)-n:]
	b = b[len(b)-n:]

	meanA := 0.0
	meanB := 0.0
	for i := 0; i < n; i++ {
		meanA += a[i]
		meanB += b[i]
	}
	meanA /= float64(n)
	meanB /= float64(n)

	num := 0.0
	denA := 0.0
	denB := 0.0
	for i := 0; i < n; i++ {
		da := a[i] - meanA
		db := b[i] - meanB
		num += da * db
		denA += da * da
		denB += db * db
	}
	if denA <= 0 || denB <= 0 {
		return 0
	}
	return num / math.Sqrt(denA*denB)
}

func fallbackSignalCorrelation(a, b JointRiskSignal) float64 {
	if a.Triggered && b.Triggered {
		sameDirection := 1.0
		if (a.DeltaPercent < 0 && b.DeltaPercent > 0) || (a.DeltaPercent > 0 && b.DeltaPercent < 0) {
			sameDirection = -1.0
		}
		return 0.35 * sameDirection
	}
	return 0
}

func extractMetric(name string) func(map[string]float64) (float64, bool) {
	return func(metrics map[string]float64) (float64, bool) {
		if metrics == nil {
			return 0, false
		}
		v, ok := metrics[name]
		return v, ok
	}
}

func extractMemoryPercent(metrics map[string]float64) (float64, bool) {
	if metrics == nil {
		return 0, false
	}
	if v, ok := metrics["memory_used_percent"]; ok {
		return v, true
	}
	total, ok := metrics["node_memory_MemTotal_bytes"]
	if !ok || total <= 0 {
		return 0, false
	}
	if used, ok := metrics["node_memory_Used_bytes"]; ok {
		return clampPercent(used / total * 100), true
	}
	if avail, ok := metrics["node_memory_MemAvailable_bytes"]; ok {
		used := total - avail
		if used < 0 {
			used = 0
		}
		return clampPercent(used / total * 100), true
	}
	return 0, false
}

func extractIOLatencyMS(metrics map[string]float64) (float64, bool) {
	if metrics == nil {
		return 0, false
	}
	if v, ok := metrics["node_disk_request_latency_p99_seconds"]; ok {
		return v * 1000, true
	}
	if v, ok := metrics["node_disk_avg_request_latency_seconds"]; ok {
		return v * 1000, true
	}
	if v, ok := metrics["probe_core_disk_await_ms"]; ok {
		return v, true
	}
	return 0, false
}

func thresholdScore(value, medium, high float64) float64 {
	if high <= medium {
		if value >= high {
			return 1
		}
		return 0
	}
	if value <= medium {
		return 0
	}
	if value >= high {
		return 1
	}
	return (value - medium) / (high - medium)
}

func percentChange(base, current float64) float64 {
	if math.Abs(base) < 1e-9 {
		if math.Abs(current) < 1e-9 {
			return 0
		}
		if current > 0 {
			return 100
		}
		return -100
	}
	return (current - base) / math.Abs(base) * 100
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func parseBool(raw string, fallback bool) bool {
	norm := strings.ToLower(strings.TrimSpace(raw))
	switch norm {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func parseInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

func parseFloat(raw string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fallback
	}
	return value
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}
