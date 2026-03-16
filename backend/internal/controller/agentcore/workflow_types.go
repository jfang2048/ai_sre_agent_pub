package agent

import (
	"sort"
	"strings"
	"time"

	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
)

const workflowPipelineVersion = "v0.7-workflow-pipeline"

// WorkflowConfig governs deterministic workflow execution for joint-risk and RCA paths.
type WorkflowConfig struct {
	Enabled                             bool          `json:"enabled" yaml:"enabled"`
	DefaultWindow                       time.Duration `json:"default_window" yaml:"default_window"`
	MaxWindow                           time.Duration `json:"max_window" yaml:"max_window"`
	MaxSamples                          int           `json:"max_samples" yaml:"max_samples"`
	MaxSignals                          int           `json:"max_signals" yaml:"max_signals"`
	MaxHypotheses                       int           `json:"max_hypotheses" yaml:"max_hypotheses"`
	MaxPlanIterations                   int           `json:"max_plan_iterations" yaml:"max_plan_iterations"`
	MaxPlanSteps                        int           `json:"max_plan_steps" yaml:"max_plan_steps"`
	AuditRetention                      int           `json:"audit_retention" yaml:"audit_retention"`
	IncidentRetention                   int           `json:"incident_retention" yaml:"incident_retention"`
	DryRun                              bool          `json:"dry_run" yaml:"dry_run"`
	RequireApproval                     bool          `json:"require_approval" yaml:"require_approval"`
	AllowProfilingExec                  bool          `json:"allow_profiling_exec" yaml:"allow_profiling_exec"`
	AllowRemediationExec                bool          `json:"allow_remediation_exec" yaml:"allow_remediation_exec"`
	AutoEscalateOnHighRisk              bool          `json:"auto_escalate_on_high_risk" yaml:"auto_escalate_on_high_risk"`
	ProfilingCommand                    string        `json:"profiling_command" yaml:"profiling_command"`
	InsightsEnabled                     bool          `json:"insights_enabled" yaml:"insights_enabled"`
	InsightsProvider                    string        `json:"insights_provider" yaml:"insights_provider"`
	InsightsModel                       string        `json:"insights_model" yaml:"insights_model"`
	InsightsAPIKeyEnv                   string        `json:"insights_api_key_env" yaml:"insights_api_key_env"`
	LLMTimeout                          time.Duration `json:"llm_timeout" yaml:"llm_timeout"`
	LLMRateLimitRPS                     float64       `json:"llm_rate_limit_rps" yaml:"llm_rate_limit_rps"`
	LLMRateBurst                        int           `json:"llm_rate_burst" yaml:"llm_rate_burst"`
	AdvancedReasoningEnabled            bool          `json:"advanced_reasoning_enabled" yaml:"advanced_reasoning_enabled"`
	AdvancedReasoningTimeout            time.Duration `json:"advanced_reasoning_timeout" yaml:"advanced_reasoning_timeout"`
	AdvancedReasoningMaxBranches        int           `json:"advanced_reasoning_max_branches" yaml:"advanced_reasoning_max_branches"`
	AdvancedReasoningAmbiguityThreshold float64       `json:"advanced_reasoning_ambiguity_threshold" yaml:"advanced_reasoning_ambiguity_threshold"`
	ReasoningTokenBudget                int           `json:"reasoning_token_budget" yaml:"reasoning_token_budget"`
	MaxRefineIterations                 int           `json:"max_refine_iterations" yaml:"max_refine_iterations"`
	RefineConfidenceThreshold           float64       `json:"refine_confidence_threshold" yaml:"refine_confidence_threshold"`
	ReasoningSeverityPolicy             ReasoningSeverityPolicy `json:"reasoning_severity_policy" yaml:"reasoning_severity_policy"`
	DegradedModePolicy                  string        `json:"degraded_mode_policy" yaml:"degraded_mode_policy"`
	HighRiskScoreThreshold              float64       `json:"high_risk_score_threshold" yaml:"high_risk_score_threshold"`
	MediumRiskThreshold                 float64       `json:"medium_risk_threshold" yaml:"medium_risk_threshold"`
	RequestDedupeTTL                    time.Duration `json:"request_dedupe_ttl" yaml:"request_dedupe_ttl"`
	RequestDedupeEntries                int           `json:"request_dedupe_entries" yaml:"request_dedupe_entries"`
}

// ReasoningSeverityPolicy controls which reasoning mode to use at each severity level.
// Valid modes: "single_pass", "plan_review_refine", "full_iterative".
type ReasoningSeverityPolicy struct {
	Critical string `json:"critical" yaml:"critical"`
	High     string `json:"high" yaml:"high"`
	Medium   string `json:"medium" yaml:"medium"`
	Low      string `json:"low" yaml:"low"`
}

// DefaultReasoningSeverityPolicy returns a safe default: iterative for critical/high, single pass for low.
func DefaultReasoningSeverityPolicy() ReasoningSeverityPolicy {
	return ReasoningSeverityPolicy{
		Critical: "full_iterative",
		High:     "plan_review_refine",
		Medium:   "plan_review_refine",
		Low:      "single_pass",
	}
}

// ReasoningModeForSeverity resolves the configured reasoning mode for a given severity.
func (p ReasoningSeverityPolicy) ReasoningModeForSeverity(severity string) string {
	var mode string
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		mode = p.Critical
	case "high":
		mode = p.High
	case "medium":
		mode = p.Medium
	case "low":
		mode = p.Low
	default:
		mode = p.Medium
	}
	switch mode {
	case "single_pass", "plan_review_refine", "full_iterative":
		return mode
	default:
		return "plan_review_refine"
	}
}

// DefaultWorkflowConfig returns safe deterministic defaults.
func DefaultWorkflowConfig() WorkflowConfig {
	return WorkflowConfig{
		Enabled:                             true,
		DefaultWindow:                       45 * time.Minute,
		MaxWindow:                           24 * time.Hour,
		MaxSamples:                          720,
		MaxSignals:                          20,
		MaxHypotheses:                       8,
		MaxPlanIterations:                   3,
		MaxPlanSteps:                        10,
		AuditRetention:                      2000,
		IncidentRetention:                   1000,
		DryRun:                              true,
		RequireApproval:                     true,
		AllowProfilingExec:                  false,
		AllowRemediationExec:                false,
		AutoEscalateOnHighRisk:              true,
		ProfilingCommand:                    "perf record -F 99 -a -g -- sleep 30",
		InsightsEnabled:                     false,
		InsightsProvider:                    "openai",
		InsightsModel:                       "gpt-4o-mini",
		InsightsAPIKeyEnv:                   "SRE_AGENT_LLM_API_KEY",
		LLMTimeout:                          30 * time.Second,
		LLMRateLimitRPS:                     2.0,
		LLMRateBurst:                        2,
		AdvancedReasoningEnabled:            true,
		AdvancedReasoningTimeout:            45 * time.Second,
		AdvancedReasoningMaxBranches:        2,
		AdvancedReasoningAmbiguityThreshold: 0.12,
		ReasoningTokenBudget:                16000,
		MaxRefineIterations:                 3,
		RefineConfidenceThreshold:           0.70,
		ReasoningSeverityPolicy:             DefaultReasoningSeverityPolicy(),
		DegradedModePolicy:                  "deterministic_only",
		HighRiskScoreThreshold:              0.72,
		MediumRiskThreshold:                 0.45,
		RequestDedupeTTL:                    30 * time.Second,
		RequestDedupeEntries:                256,
	}
}

func normalizeWorkflowConfig(cfg WorkflowConfig) WorkflowConfig {
	def := DefaultWorkflowConfig()
	if cfg.DefaultWindow <= 0 {
		cfg.DefaultWindow = def.DefaultWindow
	}
	if cfg.MaxWindow <= 0 {
		cfg.MaxWindow = def.MaxWindow
	}
	if cfg.MaxSamples <= 0 {
		cfg.MaxSamples = def.MaxSamples
	}
	if cfg.MaxSignals <= 0 {
		cfg.MaxSignals = def.MaxSignals
	}
	if cfg.MaxHypotheses <= 0 {
		cfg.MaxHypotheses = def.MaxHypotheses
	}
	if cfg.MaxPlanIterations <= 0 {
		cfg.MaxPlanIterations = def.MaxPlanIterations
	}
	if cfg.MaxPlanSteps <= 0 {
		cfg.MaxPlanSteps = def.MaxPlanSteps
	}
	if cfg.AuditRetention <= 0 {
		cfg.AuditRetention = def.AuditRetention
	}
	if cfg.IncidentRetention <= 0 {
		cfg.IncidentRetention = def.IncidentRetention
	}
	if strings.TrimSpace(cfg.ProfilingCommand) == "" {
		cfg.ProfilingCommand = def.ProfilingCommand
	}
	if strings.TrimSpace(cfg.InsightsProvider) == "" {
		cfg.InsightsProvider = def.InsightsProvider
	}
	if strings.TrimSpace(cfg.InsightsModel) == "" {
		cfg.InsightsModel = def.InsightsModel
	}
	if strings.TrimSpace(cfg.InsightsAPIKeyEnv) == "" {
		cfg.InsightsAPIKeyEnv = def.InsightsAPIKeyEnv
	}
	if cfg.LLMTimeout <= 0 {
		cfg.LLMTimeout = def.LLMTimeout
	}
	if cfg.LLMRateLimitRPS <= 0 {
		cfg.LLMRateLimitRPS = def.LLMRateLimitRPS
	}
	if cfg.LLMRateBurst <= 0 {
		cfg.LLMRateBurst = def.LLMRateBurst
	}
	if cfg.AdvancedReasoningTimeout <= 0 {
		cfg.AdvancedReasoningTimeout = def.AdvancedReasoningTimeout
	}
	if cfg.AdvancedReasoningMaxBranches <= 0 {
		cfg.AdvancedReasoningMaxBranches = def.AdvancedReasoningMaxBranches
	}
	if cfg.AdvancedReasoningAmbiguityThreshold <= 0 || cfg.AdvancedReasoningAmbiguityThreshold > 1 {
		cfg.AdvancedReasoningAmbiguityThreshold = def.AdvancedReasoningAmbiguityThreshold
	}
	if cfg.ReasoningTokenBudget <= 0 {
		cfg.ReasoningTokenBudget = def.ReasoningTokenBudget
	}
	if cfg.MaxRefineIterations <= 0 {
		cfg.MaxRefineIterations = def.MaxRefineIterations
	}
	if cfg.RefineConfidenceThreshold <= 0 || cfg.RefineConfidenceThreshold > 1 {
		cfg.RefineConfidenceThreshold = def.RefineConfidenceThreshold
	}
	if cfg.ReasoningSeverityPolicy.Critical == "" {
		cfg.ReasoningSeverityPolicy = def.ReasoningSeverityPolicy
	}
	switch cfg.DegradedModePolicy {
	case "skip_reasoning", "deterministic_only", "wait_retry":
	default:
		cfg.DegradedModePolicy = def.DegradedModePolicy
	}
	if cfg.HighRiskScoreThreshold <= 0 || cfg.HighRiskScoreThreshold > 1 {
		cfg.HighRiskScoreThreshold = def.HighRiskScoreThreshold
	}
	if cfg.MediumRiskThreshold <= 0 || cfg.MediumRiskThreshold > 1 {
		cfg.MediumRiskThreshold = def.MediumRiskThreshold
	}
	if cfg.MediumRiskThreshold > cfg.HighRiskScoreThreshold {
		cfg.MediumRiskThreshold = cfg.HighRiskScoreThreshold * 0.75
	}
	if cfg.RequestDedupeTTL <= 0 {
		cfg.RequestDedupeTTL = def.RequestDedupeTTL
	}
	if cfg.RequestDedupeEntries <= 0 {
		cfg.RequestDedupeEntries = def.RequestDedupeEntries
	}
	return cfg
}

// ToolName identifies explicit workflow tools.
type ToolName string

const (
	ToolMetrics            ToolName = "metrics_query"
	ToolLogs               ToolName = "logs_query"
	ToolTopology           ToolName = "topology_query"
	ToolSecurity           ToolName = "security_check"
	ToolKnowledge          ToolName = "knowledge_retrieval"
	ToolRAGQuery           ToolName = "rag_query"
	ToolHistoricalIncident ToolName = "historical_incident_retrieval"
	ToolRunbookRetrieval   ToolName = "runbook_retrieval"
	ToolSimilarCase        ToolName = "similar_case_retrieval"
	ToolEBPFQuery          ToolName = "trace_query"
	ToolGPU                ToolName = "gpu_query"
	ToolSecurityGraph      ToolName = "security_graph"
	ToolProcessLineage     ToolName = "process_lineage"
	ToolProfiling          ToolName = "profiling_trigger"
	ToolRemediation        ToolName = "remediation_action"
)

// WorkflowToolDescriptor is an explicit, versioned tool registry entry.
type WorkflowToolDescriptor struct {
	Name             ToolName `json:"name"`
	Version          string   `json:"version"`
	Description      string   `json:"description"`
	Purpose          string   `json:"purpose"`
	InputSchema      string   `json:"input_schema,omitempty"`
	OutputSchema     string   `json:"output_schema,omitempty"`
	Deterministic    bool     `json:"deterministic"`
	ReadOnly         bool     `json:"read_only"`
	RequiresApproval bool     `json:"requires_approval"`
	SupportsDryRun   bool     `json:"supports_dry_run"`
	SupportsRollback bool     `json:"supports_rollback"`
	SideEffects      string   `json:"side_effects,omitempty"`
	SafetyClass      string   `json:"safety_class,omitempty"`
	Unsafe           bool     `json:"unsafe,omitempty"`
}

// WorkflowRequest configures a workflow run.
type WorkflowRequest struct {
	WorkflowType string        `json:"workflow_type"`
	CollectorID  string        `json:"collector_id,omitempty"`
	Window       time.Duration `json:"window,omitempty"`
	Limit        int           `json:"limit,omitempty"`
	Trigger      string        `json:"trigger,omitempty"`
	DryRun       *bool         `json:"dry_run,omitempty"`
}

// PipelineStageResult provides deterministic stage execution details.
type PipelineStageResult struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// WorkflowToolCall captures a single tool invocation and output summary.
type WorkflowToolCall struct {
	ID           string            `json:"id"`
	Tool         ToolName          `json:"tool"`
	ToolVersion  string            `json:"tool_version,omitempty"`
	Stage        string            `json:"stage"`
	CollectorID  string            `json:"collector_id,omitempty"`
	Window       string            `json:"window,omitempty"`
	Query        map[string]string `json:"query,omitempty"`
	Status       string            `json:"status"`
	Summary      string            `json:"summary,omitempty"`
	StartedAt    time.Time         `json:"started_at"`
	CompletedAt  time.Time         `json:"completed_at"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

// WorkflowAuditRecord tracks audited tool/action events.
type WorkflowAuditRecord struct {
	ID               string            `json:"id"`
	TraceID          string            `json:"trace_id,omitempty"`
	WorkflowID       string            `json:"workflow_id"`
	WorkflowType     string            `json:"workflow_type"`
	Stage            string            `json:"stage"`
	Action           string            `json:"action"`
	Tool             ToolName          `json:"tool,omitempty"`
	CollectorID      string            `json:"collector_id,omitempty"`
	DryRun           bool              `json:"dry_run"`
	RequiresApproval bool              `json:"requires_approval"`
	Approved         bool              `json:"approved"`
	Status           string            `json:"status"`
	Input            map[string]string `json:"input,omitempty"`
	OutputSummary    string            `json:"output_summary,omitempty"`
	ErrorMessage     string            `json:"error_message,omitempty"`
	Timestamp        time.Time         `json:"timestamp"`
}

// WorkflowMetricsSnapshot exposes controller-side workflow telemetry for /metrics and status endpoints.
type WorkflowMetricsSnapshot struct {
	ReasoningStepsTotal       uint64  `json:"reasoning_steps_total"`
	ReasoningFailuresTotal    uint64  `json:"reasoning_failures_total"`
	ReasoningParseFailTotal   uint64  `json:"reasoning_parse_failures_total"`
	ReasoningValidFailTotal   uint64  `json:"reasoning_validation_failures_total"`
	ReasoningLLMErrorTotal    uint64  `json:"reasoning_llm_errors_total"`
	ReasoningBudgetExhTotal   uint64  `json:"reasoning_budget_exhausted_total"`
	AvgConfidence             float64 `json:"avg_confidence"`
	TokenCostTotal            uint64  `json:"token_cost_total"`
	TokenCostPerIncident      float64 `json:"token_cost_per_incident"`
	HallucinationProxyTotal   uint64  `json:"hallucination_proxy_total"`
	RetrievalHitsTotal        uint64  `json:"retrieval_hits_total"`
	RetrievalMissTotal        uint64  `json:"retrieval_miss_total"`
	ActionsExecutedTotal      uint64  `json:"actions_executed_total"`
	ActionsDryRunTotal        uint64  `json:"actions_dry_run_total"`
	ActionsBlockedTotal       uint64  `json:"actions_blocked_total"`
	WorkflowRunsTotal         uint64  `json:"workflow_runs_total"`
	WorkflowLatencySeconds    float64 `json:"workflow_latency_seconds_total"`
	IncidentRCARunsTotal      uint64  `json:"incident_rca_runs_total"`
	IncidentRCALatencySeconds float64 `json:"incident_rca_latency_seconds_total"`
}

// RiskSeriesPoint is a chart-friendly sample.
type RiskSeriesPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// RiskSeries bundles signal trend data for Joint Risk UI.
type RiskSeries struct {
	Key               string            `json:"key"`
	Display           string            `json:"display"`
	Unit              string            `json:"unit"`
	Category          string            `json:"category,omitempty"`
	Latest            float64           `json:"latest"`
	Baseline          float64           `json:"baseline"`
	DeltaPercent      float64           `json:"delta_percent,omitempty"`
	SlopePerMinute    float64           `json:"slope_per_minute,omitempty"`
	Acceleration      float64           `json:"acceleration"`
	ThresholdBreaches int               `json:"threshold_breaches,omitempty"`
	PersistencePoints int               `json:"persistence_points,omitempty"`
	Trend             string            `json:"trend,omitempty"`
	Triggered         bool              `json:"triggered,omitempty"`
	Forecast          string            `json:"forecast,omitempty"`
	ForecastValue     float64           `json:"forecast_value,omitempty"`
	ThresholdValue    float64           `json:"threshold_value,omitempty"`
	Points            []RiskSeriesPoint `json:"points"`
}

// TrendAssessment is the normalized single-signal trend or forecast summary used before RCA/LLM steps.
type TrendAssessment struct {
	ID                string    `json:"id"`
	SeriesKey         string    `json:"series_key"`
	Display           string    `json:"display"`
	Category          string    `json:"category,omitempty"`
	Scope             string    `json:"scope"`
	Entity            string    `json:"entity"`
	Trend             string    `json:"trend"`
	Severity          string    `json:"severity"`
	Confidence        float64   `json:"confidence"`
	DetectionMode     string    `json:"detection_mode,omitempty"`
	Latest            float64   `json:"latest"`
	Baseline          float64   `json:"baseline"`
	DeltaPercent      float64   `json:"delta_percent"`
	SlopePerMinute    float64   `json:"slope_per_minute,omitempty"`
	Acceleration      float64   `json:"acceleration,omitempty"`
	ThresholdBreaches int       `json:"threshold_breaches,omitempty"`
	PersistencePoints int       `json:"persistence_points,omitempty"`
	ThresholdValue    float64   `json:"threshold_value,omitempty"`
	Forecast          string    `json:"forecast,omitempty"`
	ForecastValue     float64   `json:"forecast_value,omitempty"`
	Triggered         bool      `json:"triggered"`
	Summary           string    `json:"summary"`
	OperatorHint      string    `json:"operator_hint,omitempty"`
	LastObservedAt    time.Time `json:"last_observed_at"`
}

// InvestigationEvent is the ranked event object promoted before retrieval and LLM analysis.
type InvestigationEvent struct {
	ID                string   `json:"id"`
	Category          string   `json:"category"`
	Severity          string   `json:"severity"`
	Confidence        float64  `json:"confidence"`
	Scope             string   `json:"scope"`
	Entity            string   `json:"entity"`
	Title             string   `json:"title"`
	Symptom           string   `json:"symptom"`
	ProbableCause     string   `json:"probable_cause,omitempty"`
	Summary           string   `json:"summary"`
	SupportingSignals []string `json:"supporting_signals,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
	RecommendedChecks []string `json:"recommended_checks,omitempty"`
	RetrievalHint     string   `json:"retrieval_hint,omitempty"`
}

// RetrievalDecision records how workflow evidence was converted into a retrieval request or skip decision.
type RetrievalDecision struct {
	Phase           string   `json:"phase"`
	Tool            string   `json:"tool"`
	Intent          string   `json:"intent"`
	Query           string   `json:"query,omitempty"`
	EvidenceSignals []string `json:"evidence_signals,omitempty"`
	Skipped         bool     `json:"skipped,omitempty"`
	SkipReason      string   `json:"skip_reason,omitempty"`
}

// JointRiskSignal represents one low-severity signal contribution.
type JointRiskSignal struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Scope          string    `json:"scope"`
	Entity         string    `json:"entity"`
	Severity       string    `json:"severity"`
	Weight         float64   `json:"weight"`
	Current        float64   `json:"current"`
	Baseline       float64   `json:"baseline"`
	DeltaPercent   float64   `json:"delta_percent"`
	Acceleration   float64   `json:"acceleration"`
	Score          float64   `json:"score"`
	Triggered      bool      `json:"triggered"`
	Evidence       []string  `json:"evidence,omitempty"`
	LastObservedAt time.Time `json:"last_observed_at"`
}

// JointRiskCooccurrence captures correlated signal groups within the same window.
type JointRiskCooccurrence struct {
	ID              string   `json:"id"`
	Scope           string   `json:"scope"`
	Entity          string   `json:"entity"`
	Window          string   `json:"window"`
	Signals         []string `json:"signals"`
	Correlation     float64  `json:"correlation"`
	CombinedScore   float64  `json:"combined_score"`
	Explanation     string   `json:"explanation"`
	ActionableCause string   `json:"actionable_cause"`
}

// ScopeRisk summarizes risk by process/node/pod/service/cluster dimensions.
type ScopeRisk struct {
	Scope       string   `json:"scope"`
	Entity      string   `json:"entity"`
	Score       float64  `json:"score"`
	TopSignals  []string `json:"top_signals,omitempty"`
	Explanation string   `json:"explanation"`
}

// RetrievedDocumentEvidence captures a knowledge-base hit promoted into workflow evidence.
type RetrievedDocumentEvidence struct {
	EvidenceID       string            `json:"evidence_id"`
	DocID            string            `json:"doc_id"`
	ChunkID          string            `json:"chunk_id"`
	Title            string            `json:"title"`
	SourcePath       string            `json:"source_path"`
	SourceType       string            `json:"source_type"`
	KnowledgeType    string            `json:"knowledge_type,omitempty"`
	CaseType         string            `json:"case_type,omitempty"`
	Summary          string            `json:"summary,omitempty"`
	Snippet          string            `json:"snippet"`
	Score            float64           `json:"score"`
	Symptoms         []string          `json:"symptoms,omitempty"`
	Evidence         []string          `json:"evidence,omitempty"`
	LikelyCauses     []string          `json:"likely_causes,omitempty"`
	RemediationSteps []string          `json:"remediation_steps,omitempty"`
	Commands         []string          `json:"commands,omitempty"`
	Signals          []string          `json:"signals,omitempty"`
	SectionType      string            `json:"section_type,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// IncidentGroupedSignal is a weak signal promoted into an incident cluster.
type IncidentGroupedSignal struct {
	SignalID     string    `json:"signal_id"`
	SignalType   string    `json:"signal_type"`
	Source       string    `json:"source"`
	Scope        string    `json:"scope"`
	Entity       string    `json:"entity"`
	Severity     string    `json:"severity"`
	Score        float64   `json:"score"`
	Summary      string    `json:"summary"`
	EvidenceIDs  []string  `json:"evidence_ids,omitempty"`
	LastObserved time.Time `json:"last_observed,omitempty"`
}

// IncidentSynthesis is the grouped investigation object created before RCA.
type IncidentSynthesis struct {
	IncidentID                string                  `json:"incident_id"`
	Summary                   string                  `json:"summary"`
	GroupedSignals            []IncidentGroupedSignal `json:"grouped_signals"`
	ImpactedScope             []string                `json:"impacted_scope"`
	TimeWindow                TimeWindow              `json:"time_window"`
	Severity                  string                  `json:"severity"`
	Confidence                float64                 `json:"confidence"`
	CandidateRootCauseCluster string                  `json:"candidate_root_cause_cluster,omitempty"`
	CorrelationReasons        []string                `json:"correlation_reasons,omitempty"`
	TopOffenders              []string                `json:"top_offenders,omitempty"`
	TimelineTransitions       []string                `json:"timeline_transitions,omitempty"`
	TopologyNeighborhood      []string                `json:"topology_neighborhood,omitempty"`
}

// ContributingSignal describes a signal's contribution to risk scoring.
type ContributingSignal struct {
	SignalID   string  `json:"signal_id"`
	SignalType string  `json:"signal_type"`
	Value      float64 `json:"value"`
	Weight     float64 `json:"weight"`
	Source     string  `json:"source"`
}

// TimeWindow defines a correlated time range.
type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// JointRiskAssessment is the output for joint-risk workflow.
type JointRiskAssessment struct {
	WorkflowID              string                      `json:"workflow_id"`
	PipelineVersion         string                      `json:"pipeline_version"`
	CollectorID             string                      `json:"collector_id,omitempty"`
	Scope                   string                      `json:"scope"`
	Window                  string                      `json:"window"`
	GeneratedAt             time.Time                   `json:"generated_at"`
	RiskScore               float64                     `json:"risk_score"`
	RiskLevel               string                      `json:"risk_level"`
	Summary                 string                      `json:"summary"`
	ActionableWhy           string                      `json:"actionable_why"`
	Signals                 []JointRiskSignal           `json:"signals"`
	TrendAssessments        []TrendAssessment           `json:"trend_assessments,omitempty"`
	InvestigationEvents     []InvestigationEvent        `json:"investigation_events,omitempty"`
	Cooccurrences           []JointRiskCooccurrence     `json:"cooccurrences"`
	ScopeRisks              []ScopeRisk                 `json:"scope_risks"`
	Series                  []RiskSeries                `json:"series"`
	Recommendations         []WorkflowRecommendation    `json:"recommendations"`
	Stages                  []PipelineStageResult       `json:"stages"`
	ToolCalls               []WorkflowToolCall          `json:"tool_calls"`
	Escalated               bool                        `json:"escalated"`
	EscalationReason        string                      `json:"escalation_reason,omitempty"`
	IncidentID              string                      `json:"incident_id,omitempty"`
	Limitations             []string                    `json:"limitations,omitempty"`
	Insights                WorkflowInsightsStatus      `json:"insights"`
	LLMAnalysis             *LLMAnalysisResult          `json:"llm_analysis,omitempty"`
	ContributingSignals     []ContributingSignal        `json:"contributing_signals,omitempty"`
	CorrelatedTimeWindow    *TimeWindow                 `json:"correlated_time_window,omitempty"`
	ImpactedScope           []string                    `json:"impacted_scope,omitempty"`
	Confidence              float64                     `json:"confidence"`
	RecommendedToolCalls    []string                    `json:"recommended_tool_calls,omitempty"`
	Severity                string                      `json:"severity,omitempty"`
	RetrievedDocs           []RetrievedDocumentEvidence `json:"retrieved_docs,omitempty"`
	RetrievedCases          []RetrievedDocumentEvidence `json:"retrieved_cases,omitempty"`
	RetrievedRunbooks       []RetrievedDocumentEvidence `json:"retrieved_runbooks,omitempty"`
	SimilarIncidentPatterns []RetrievedDocumentEvidence `json:"similar_incident_patterns,omitempty"`
	RetrievalSummary        string                      `json:"retrieval_summary,omitempty"`
	RetrievalEvidenceIDs    []string                    `json:"retrieval_evidence_ids,omitempty"`
	RetrievalConfidence     float64                     `json:"retrieval_confidence,omitempty"`
	RetrievalDecisions      []RetrievalDecision         `json:"retrieval_decisions,omitempty"`
}

// RCAEvidence is a traceable proof item attached to hypotheses.
type RCAEvidence struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Source     string    `json:"source"`
	Scope      string    `json:"scope,omitempty"`
	Entity     string    `json:"entity,omitempty"`
	Summary    string    `json:"summary"`
	MetricName string    `json:"metric_name,omitempty"`
	Value      float64   `json:"value,omitempty"`
	Baseline   float64   `json:"baseline,omitempty"`
	Delta      float64   `json:"delta,omitempty"`
	Snippet    string    `json:"snippet,omitempty"`
	Timestamp  time.Time `json:"timestamp,omitempty"`
}

// RCAHypothesis is a ranked, explainable candidate root cause.
type RCAHypothesis struct {
	ID                       string   `json:"id"`
	Rank                     int      `json:"rank"`
	Title                    string   `json:"title"`
	Confidence               float64  `json:"confidence"`
	Description              string   `json:"description"`
	EvidenceIDs              []string `json:"evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string `json:"contradicting_evidence_ids,omitempty"`
	RecommendedActions       []string `json:"recommended_actions,omitempty"`
	RollbackStrategy         string   `json:"rollback_strategy,omitempty"`
}

// WorkflowRecommendation is a guarded next step or remediation.
type WorkflowRecommendation struct {
	ID                    string   `json:"id"`
	Category              string   `json:"category,omitempty"`
	Priority              string   `json:"priority"`
	Summary               string   `json:"summary"`
	Details               string   `json:"details,omitempty"`
	Scope                 string   `json:"scope,omitempty"`
	ExecutionLevel        string   `json:"execution_level,omitempty"`
	Checks                []string `json:"checks,omitempty"`
	Preconditions         []string `json:"preconditions,omitempty"`
	Safe                  bool     `json:"safe"`
	DryRunDefault         bool     `json:"dry_run_default"`
	RequiresApproval      bool     `json:"requires_approval"`
	ApprovalReason        string   `json:"approval_reason,omitempty"`
	Reversible            bool     `json:"reversible"`
	RollbackHint          string   `json:"rollback_hint,omitempty"`
	BlastRadius           string   `json:"blast_radius,omitempty"`
	IdempotencyNote       string   `json:"idempotency_note,omitempty"`
	Timeout               string   `json:"timeout,omitempty"`
	Rationale             string   `json:"rationale,omitempty"`
	OperatorJustification string   `json:"operator_justification,omitempty"`
	ExpectedImpact        string   `json:"expected_impact,omitempty"`
	RiskLevel             string   `json:"risk_level,omitempty"`
	Confidence            float64  `json:"confidence,omitempty"`
	EvidenceIDs           []string `json:"evidence_ids,omitempty"`
	RollbackConsideration string   `json:"rollback_consideration,omitempty"`
}

// ActionPolicyDecision is the explicit guardrail verdict for a proposed action.
type ActionPolicyDecision struct {
	Status            string   `json:"status"`
	Reason            string   `json:"reason"`
	ExecutionLevel    string   `json:"execution_level,omitempty"`
	RequiresApproval  bool     `json:"requires_approval"`
	DryRunRequired    bool     `json:"dry_run_required"`
	RollbackRequired  bool     `json:"rollback_required"`
	MissingConditions []string `json:"missing_conditions,omitempty"`
}

// RCACorrelation summarizes multi-signal cross-scope relationships.
type RCACorrelation struct {
	ID          string   `json:"id"`
	Scope       string   `json:"scope"`
	Entity      string   `json:"entity"`
	Signals     []string `json:"signals"`
	Coefficient float64  `json:"coefficient"`
	Summary     string   `json:"summary"`
}

// AgentPlanStep captures one plan/act/verify step.
type AgentPlanStep struct {
	ID               string            `json:"id"`
	Order            int               `json:"order"`
	Iteration        int               `json:"iteration"`
	Title            string            `json:"title"`
	Objective        string            `json:"objective"`
	Tool             ToolName          `json:"tool"`
	Required         bool              `json:"required"`
	ToolVersion      string            `json:"tool_version,omitempty"`
	Query            map[string]string `json:"query,omitempty"`
	Status           string            `json:"status"`
	ResultSummary    string            `json:"result_summary,omitempty"`
	Verified         bool              `json:"verified"`
	VerificationNote string            `json:"verification_note,omitempty"`
	EvidenceIDs      []string          `json:"evidence_ids,omitempty"`
	SupersededBy     string            `json:"superseded_by,omitempty"`
	StartedAt        time.Time         `json:"started_at,omitempty"`
	CompletedAt      time.Time         `json:"completed_at,omitempty"`
}

// AgentPlanRevision captures a plan revision snapshot.
type AgentPlanRevision struct {
	Iteration int             `json:"iteration"`
	Reason    string          `json:"reason"`
	CreatedAt time.Time       `json:"created_at"`
	Steps     []AgentPlanStep `json:"steps"`
}

// AgentLoopSummary reports plan/act/verify execution.
type AgentLoopSummary struct {
	Mode          string              `json:"mode"`
	Iterations    int                 `json:"iterations"`
	Replans       int                 `json:"replans"`
	StepsPlanned  int                 `json:"steps_planned"`
	StepsExecuted int                 `json:"steps_executed"`
	StepsVerified int                 `json:"steps_verified"`
	Completed     bool                `json:"completed"`
	StopReason    string              `json:"stop_reason"`
	PlanSteps     []AgentPlanStep     `json:"plan_steps"`
	PlanRevisions []AgentPlanRevision `json:"plan_revisions"`
}

// RCATimelineEvent records an ordered investigation timeline event.
type RCATimelineEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Phase     string    `json:"phase"`
	Summary   string    `json:"summary"`
}

// RCAStructuredReport is the explainable RCA packet.
type RCAStructuredReport struct {
	IncidentSummary      string             `json:"incident_summary,omitempty"`
	Symptoms             []string           `json:"symptoms"`
	Timeline             []RCATimelineEvent `json:"timeline"`
	Scope                []string           `json:"scope"`
	MostLikelyCause      string             `json:"most_likely_cause"`
	SupportingSignals    []string           `json:"supporting_signals"`
	DisconfirmingSignals []string           `json:"disconfirming_signals"`
	Confidence           float64            `json:"confidence"`
	UnresolvedGaps       []string           `json:"unresolved_gaps,omitempty"`
	RecommendedNextSteps []string           `json:"recommended_next_steps,omitempty"`
	SafeRemediations     []string           `json:"safe_remediations,omitempty"`
}

// RCAContext captures gathered workflow context.
type RCAContext struct {
	CollectorID         string               `json:"collector_id,omitempty"`
	Window              string               `json:"window"`
	IncidentSummary     string               `json:"incident_summary,omitempty"`
	ImpactedScope       []string             `json:"impacted_scope,omitempty"`
	TopMetrics          map[string]float64   `json:"top_metrics"`
	TrendAssessments    []TrendAssessment    `json:"trend_assessments,omitempty"`
	InvestigationEvents []InvestigationEvent `json:"investigation_events,omitempty"`
	GPUSummary          map[string]float64   `json:"gpu_summary,omitempty"`
	TopProcesses        []string             `json:"top_processes"`
	KernelSignals       []string             `json:"kernel_signals"`
	TraceSummary        []string             `json:"trace_summary,omitempty"`
	RecentDeploys       []string             `json:"recent_deploys,omitempty"`
	SecurityFindings    []string             `json:"security_findings,omitempty"`
	TopologySummary     string               `json:"topology_summary,omitempty"`
	RetrievalSummary    string               `json:"retrieval_summary,omitempty"`
	RetrievalDecisions  []RetrievalDecision  `json:"retrieval_decisions,omitempty"`
}

// WorkflowInsightsStatus documents future LLM insight integration readiness.
type WorkflowInsightsStatus struct {
	Enabled          bool   `json:"enabled"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	APIKeyEnv        string `json:"api_key_env"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	Mode             string `json:"mode"`
}

// RCAWorkflowReport is the structured RCA workflow output.
type RCAWorkflowReport struct {
	WorkflowID              string                      `json:"workflow_id"`
	PipelineVersion         string                      `json:"pipeline_version"`
	EvidenceSchemaVersion   string                      `json:"evidence_schema_version,omitempty"`
	IncidentID              string                      `json:"incident_id"`
	TraceID                 string                      `json:"trace_id,omitempty"`
	Status                  string                      `json:"status"`
	CollectorID             string                      `json:"collector_id,omitempty"`
	Trigger                 string                      `json:"trigger"`
	GeneratedAt             time.Time                   `json:"generated_at"`
	SynthesizedIncident     IncidentSynthesis           `json:"synthesized_incident"`
	Context                 RCAContext                  `json:"context"`
	Anomalies               []string                    `json:"anomalies"`
	Correlations            []RCACorrelation            `json:"correlations"`
	Hypotheses              []RCAHypothesis             `json:"hypotheses"`
	Evidence                []RCAEvidence               `json:"evidence"`
	NormalizedEvidence      []evidencev1.Record         `json:"normalized_evidence,omitempty"`
	Recommendations         []WorkflowRecommendation    `json:"recommendations"`
	ProposedActions         []ProposedAction            `json:"proposed_actions,omitempty"`
	AgentLoop               AgentLoopSummary            `json:"agent_loop"`
	StructuredReport        RCAStructuredReport         `json:"structured_report"`
	Stages                  []PipelineStageResult       `json:"stages"`
	ToolCalls               []WorkflowToolCall          `json:"tool_calls"`
	Reproducibility         map[string]string           `json:"reproducibility"`
	UnresolvedGaps          []string                    `json:"unresolved_gaps,omitempty"`
	Limitations             []string                    `json:"limitations,omitempty"`
	Insights                WorkflowInsightsStatus      `json:"insights"`
	LLMAnalysis             *LLMAnalysisResult          `json:"llm_analysis,omitempty"`
	RetrievedDocs           []RetrievedDocumentEvidence `json:"retrieved_docs,omitempty"`
	RetrievedCases          []RetrievedDocumentEvidence `json:"retrieved_cases,omitempty"`
	RetrievedRunbooks       []RetrievedDocumentEvidence `json:"retrieved_runbooks,omitempty"`
	SimilarIncidentPatterns []RetrievedDocumentEvidence `json:"similar_incident_patterns,omitempty"`
	RetrievalSummary        string                      `json:"retrieval_summary,omitempty"`
	RetrievalEvidenceIDs    []string                    `json:"retrieval_evidence_ids,omitempty"`
	RetrievalConfidence     float64                     `json:"retrieval_confidence,omitempty"`
}

// AgentIncidentReport is a persisted incident investigation record.
type AgentIncidentReport struct {
	IncidentID            string                   `json:"incident_id"`
	WorkflowID            string                   `json:"workflow_id"`
	TraceID               string                   `json:"trace_id,omitempty"`
	EvidenceSchemaVersion string                   `json:"evidence_schema_version,omitempty"`
	Status                string                   `json:"status"`
	Source                string                   `json:"source"`
	CollectorID           string                   `json:"collector_id,omitempty"`
	OpenedAt              time.Time                `json:"opened_at"`
	ClosedAt              *time.Time               `json:"closed_at,omitempty"`
	RiskLevel             string                   `json:"risk_level"`
	RiskScore             float64                  `json:"risk_score"`
	Summary               string                   `json:"summary"`
	MostLikelyCause       string                   `json:"most_likely_cause"`
	Confidence            float64                  `json:"confidence"`
	SynthesizedIncident   IncidentSynthesis        `json:"synthesized_incident"`
	Symptoms              []string                 `json:"symptoms"`
	Timeline              []RCATimelineEvent       `json:"timeline"`
	Evidence              []RCAEvidence            `json:"evidence"`
	NormalizedEvidence    []evidencev1.Record      `json:"normalized_evidence,omitempty"`
	Hypotheses            []RCAHypothesis          `json:"hypotheses"`
	Recommendations       []WorkflowRecommendation `json:"recommendations"`
	ProposedActions       []ProposedAction         `json:"proposed_actions,omitempty"`
	AgentLoop             AgentLoopSummary         `json:"agent_loop"`
	UnresolvedGaps        []string                 `json:"unresolved_gaps,omitempty"`
	LLMAnalysis           *LLMAnalysisResult       `json:"llm_analysis,omitempty"`
}

// JointRiskListResponse wraps the joint-risk list API payload.
type JointRiskListResponse struct {
	Reports   []JointRiskAssessment `json:"reports"`
	Count     int                   `json:"count"`
	Timestamp time.Time             `json:"timestamp"`
}

// RCAListResponse wraps the RCA workflow list API payload.
type RCAListResponse struct {
	Reports   []RCAWorkflowReport `json:"reports"`
	Count     int                 `json:"count"`
	Timestamp time.Time           `json:"timestamp"`
}

// WorkflowAuditResponse wraps workflow audit entries.
type WorkflowAuditResponse struct {
	Records   []WorkflowAuditRecord `json:"records"`
	Count     int                   `json:"count"`
	Timestamp time.Time             `json:"timestamp"`
}

// AgentIncidentListResponse wraps incident list payloads.
type AgentIncidentListResponse struct {
	Incidents []AgentIncidentReport `json:"incidents"`
	Count     int                   `json:"count"`
	Timestamp time.Time             `json:"timestamp"`
}

// WorkflowToolRegistryResponse wraps explicit tool registry payloads.
type WorkflowToolRegistryResponse struct {
	Tools     []WorkflowToolDescriptor `json:"tools"`
	Count     int                      `json:"count"`
	Timestamp time.Time                `json:"timestamp"`
}

// PotentialRiskSignal is the condensed weak-signal evidence row.
type PotentialRiskSignal struct {
	Name         string   `json:"name"`
	Scope        string   `json:"scope"`
	Entity       string   `json:"entity"`
	Severity     string   `json:"severity"`
	Current      float64  `json:"current"`
	Baseline     float64  `json:"baseline"`
	DeltaPercent float64  `json:"delta_percent"`
	Score        float64  `json:"score"`
	Evidence     []string `json:"evidence,omitempty"`
}

// PotentialRiskFinding is the proactive risk output consumed by the Risk Insights page.
type PotentialRiskFinding struct {
	ID                          string                      `json:"id"`
	CollectorID                 string                      `json:"collector_id,omitempty"`
	RiskSummary                 string                      `json:"risk_summary"`
	TimeWindow                  string                      `json:"time_window"`
	Scope                       string                      `json:"scope"`
	ConfidenceScore             float64                     `json:"confidence_score"`
	ContributingSignals         []PotentialRiskSignal       `json:"contributing_signals"`
	TrendAssessments            []TrendAssessment           `json:"trend_assessments,omitempty"`
	InvestigationEvents         []InvestigationEvent        `json:"investigation_events,omitempty"`
	SuggestedInvestigationSteps []string                    `json:"suggested_investigation_steps"`
	Correlations                []JointRiskCooccurrence     `json:"correlations,omitempty"`
	Series                      []RiskSeries                `json:"series,omitempty"`
	GeneratedAt                 time.Time                   `json:"generated_at"`
	RetrievedDocs               []RetrievedDocumentEvidence `json:"retrieved_docs,omitempty"`
	RetrievedCases              []RetrievedDocumentEvidence `json:"retrieved_cases,omitempty"`
	RetrievedRunbooks           []RetrievedDocumentEvidence `json:"retrieved_runbooks,omitempty"`
	SimilarIncidentPatterns     []RetrievedDocumentEvidence `json:"similar_incident_patterns,omitempty"`
	RetrievalSummary            string                      `json:"retrieval_summary,omitempty"`
	RetrievalEvidenceIDs        []string                    `json:"retrieval_evidence_ids,omitempty"`
	RetrievalConfidence         float64                     `json:"retrieval_confidence,omitempty"`
	RetrievalDecisions          []RetrievalDecision         `json:"retrieval_decisions,omitempty"`
}

// PotentialRiskResponse wraps proactive risk findings.
type PotentialRiskResponse struct {
	Findings  []PotentialRiskFinding `json:"findings"`
	Count     int                    `json:"count"`
	Timestamp time.Time              `json:"timestamp"`
}

// WorkflowControlPlaneSummary is a compact operator-facing summary of the latest eventized workflow state.
type WorkflowControlPlaneSummary struct {
	Enabled                bool      `json:"enabled"`
	JointRiskReports       int       `json:"joint_risk_reports"`
	RCAReports             int       `json:"rca_reports"`
	Incidents              int       `json:"incidents"`
	HighRiskReports        int       `json:"high_risk_reports"`
	LatestCollectorID      string    `json:"latest_collector_id,omitempty"`
	LatestJointRiskAt      time.Time `json:"latest_joint_risk_at,omitempty"`
	LatestRCAAt            time.Time `json:"latest_rca_at,omitempty"`
	LatestRiskLevel        string    `json:"latest_risk_level,omitempty"`
	TriggeredTrends        int       `json:"triggered_trends"`
	InvestigationEvents    int       `json:"investigation_events"`
	WeakSignalClusters     int       `json:"weak_signal_clusters"`
	RetrievalDecisions     int       `json:"retrieval_decisions"`
	RetrievalSkipped       int       `json:"retrieval_skipped"`
	RecommendationCount    int       `json:"recommendation_count"`
	TopEventTitle          string    `json:"top_event_title,omitempty"`
	ProbableCause          string    `json:"probable_cause,omitempty"`
	LatestIncidentSummary  string    `json:"latest_incident_summary,omitempty"`
	TopRecommendation      string    `json:"top_recommendation,omitempty"`
	TopRetrievalIntent     string    `json:"top_retrieval_intent,omitempty"`
	TopRetrievalQuery      string    `json:"top_retrieval_query,omitempty"`
	TopRetrievalSkipReason string    `json:"top_retrieval_skip_reason,omitempty"`
}

func sortJointRiskReportsByTime(in []JointRiskAssessment) {
	sort.Slice(in, func(i, j int) bool {
		return in[i].GeneratedAt.After(in[j].GeneratedAt)
	})
}

func sortRCAReportsByTime(in []RCAWorkflowReport) {
	sort.Slice(in, func(i, j int) bool {
		return in[i].GeneratedAt.After(in[j].GeneratedAt)
	})
}

func sortAuditByTime(in []WorkflowAuditRecord) {
	sort.Slice(in, func(i, j int) bool {
		return in[i].Timestamp.After(in[j].Timestamp)
	})
}
