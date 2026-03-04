package agent

import (
	"sort"
	"strings"
	"time"
)

const workflowPipelineVersion = "v0.5-workflow-pipeline"

// WorkflowConfig governs deterministic workflow execution for joint-risk and RCA paths.
type WorkflowConfig struct {
	Enabled                bool          `json:"enabled" yaml:"enabled"`
	DefaultWindow          time.Duration `json:"default_window" yaml:"default_window"`
	MaxWindow              time.Duration `json:"max_window" yaml:"max_window"`
	MaxSamples             int           `json:"max_samples" yaml:"max_samples"`
	MaxSignals             int           `json:"max_signals" yaml:"max_signals"`
	MaxHypotheses          int           `json:"max_hypotheses" yaml:"max_hypotheses"`
	MaxPlanIterations      int           `json:"max_plan_iterations" yaml:"max_plan_iterations"`
	MaxPlanSteps           int           `json:"max_plan_steps" yaml:"max_plan_steps"`
	AuditRetention         int           `json:"audit_retention" yaml:"audit_retention"`
	IncidentRetention      int           `json:"incident_retention" yaml:"incident_retention"`
	DryRun                 bool          `json:"dry_run" yaml:"dry_run"`
	RequireApproval        bool          `json:"require_approval" yaml:"require_approval"`
	AllowProfilingExec     bool          `json:"allow_profiling_exec" yaml:"allow_profiling_exec"`
	AllowRemediationExec   bool          `json:"allow_remediation_exec" yaml:"allow_remediation_exec"`
	AutoEscalateOnHighRisk bool          `json:"auto_escalate_on_high_risk" yaml:"auto_escalate_on_high_risk"`
	ProfilingCommand       string        `json:"profiling_command" yaml:"profiling_command"`
	InsightsEnabled        bool          `json:"insights_enabled" yaml:"insights_enabled"`
	InsightsProvider       string        `json:"insights_provider" yaml:"insights_provider"`
	InsightsModel          string        `json:"insights_model" yaml:"insights_model"`
	InsightsAPIKeyEnv      string        `json:"insights_api_key_env" yaml:"insights_api_key_env"`
	LLMTimeout             time.Duration `json:"llm_timeout" yaml:"llm_timeout"`
	LLMRateLimitRPS        float64       `json:"llm_rate_limit_rps" yaml:"llm_rate_limit_rps"`
	LLMRateBurst           int           `json:"llm_rate_burst" yaml:"llm_rate_burst"`
	HighRiskScoreThreshold float64       `json:"high_risk_score_threshold" yaml:"high_risk_score_threshold"`
	MediumRiskThreshold    float64       `json:"medium_risk_threshold" yaml:"medium_risk_threshold"`
}

// DefaultWorkflowConfig returns safe deterministic defaults.
func DefaultWorkflowConfig() WorkflowConfig {
	return WorkflowConfig{
		Enabled:                true,
		DefaultWindow:          45 * time.Minute,
		MaxWindow:              24 * time.Hour,
		MaxSamples:             720,
		MaxSignals:             20,
		MaxHypotheses:          8,
		MaxPlanIterations:      3,
		MaxPlanSteps:           10,
		AuditRetention:         2000,
		IncidentRetention:      1000,
		DryRun:                 true,
		RequireApproval:        true,
		AllowProfilingExec:     false,
		AllowRemediationExec:   false,
		AutoEscalateOnHighRisk: true,
		ProfilingCommand:       "perf record -F 99 -a -g -- sleep 30",
		InsightsEnabled:        false,
		InsightsProvider:       "openai",
		InsightsModel:          "gpt-4o-mini",
		InsightsAPIKeyEnv:      "SRE_AGENT_LLM_API_KEY",
		LLMTimeout:             30 * time.Second,
		LLMRateLimitRPS:        2.0,
		LLMRateBurst:           2,
		HighRiskScoreThreshold: 0.72,
		MediumRiskThreshold:    0.45,
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
	if cfg.HighRiskScoreThreshold <= 0 || cfg.HighRiskScoreThreshold > 1 {
		cfg.HighRiskScoreThreshold = def.HighRiskScoreThreshold
	}
	if cfg.MediumRiskThreshold <= 0 || cfg.MediumRiskThreshold > 1 {
		cfg.MediumRiskThreshold = def.MediumRiskThreshold
	}
	if cfg.MediumRiskThreshold > cfg.HighRiskScoreThreshold {
		cfg.MediumRiskThreshold = cfg.HighRiskScoreThreshold * 0.75
	}
	return cfg
}

// ToolName identifies explicit workflow tools.
type ToolName string

const (
	ToolMetrics        ToolName = "metrics_query"
	ToolLogs           ToolName = "log_query"
	ToolTopology       ToolName = "topology_query"
	ToolSecurity       ToolName = "security_check"
	ToolEBPFQuery      ToolName = "ebpf_query"
	ToolSecurityGraph  ToolName = "security_graph"
	ToolProcessLineage ToolName = "process_lineage"
	ToolProfiling      ToolName = "profiling_trigger"
	ToolRemediation    ToolName = "remediation_action"
)

// WorkflowToolDescriptor is an explicit, versioned tool registry entry.
type WorkflowToolDescriptor struct {
	Name          ToolName `json:"name"`
	Version       string   `json:"version"`
	Description   string   `json:"description"`
	Deterministic bool     `json:"deterministic"`
	Unsafe        bool     `json:"unsafe"`
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

// RiskSeriesPoint is a chart-friendly sample.
type RiskSeriesPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// RiskSeries bundles signal trend data for Joint Risk UI.
type RiskSeries struct {
	Key          string            `json:"key"`
	Display      string            `json:"display"`
	Unit         string            `json:"unit"`
	Latest       float64           `json:"latest"`
	Baseline     float64           `json:"baseline"`
	Acceleration float64           `json:"acceleration"`
	Points       []RiskSeriesPoint `json:"points"`
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
	WorkflowID           string                   `json:"workflow_id"`
	PipelineVersion      string                   `json:"pipeline_version"`
	CollectorID          string                   `json:"collector_id,omitempty"`
	Scope                string                   `json:"scope"`
	Window               string                   `json:"window"`
	GeneratedAt          time.Time                `json:"generated_at"`
	RiskScore            float64                  `json:"risk_score"`
	RiskLevel            string                   `json:"risk_level"`
	Summary              string                   `json:"summary"`
	ActionableWhy        string                   `json:"actionable_why"`
	Signals              []JointRiskSignal        `json:"signals"`
	Cooccurrences        []JointRiskCooccurrence  `json:"cooccurrences"`
	ScopeRisks           []ScopeRisk              `json:"scope_risks"`
	Series               []RiskSeries             `json:"series"`
	Recommendations      []WorkflowRecommendation `json:"recommendations"`
	Stages               []PipelineStageResult    `json:"stages"`
	ToolCalls            []WorkflowToolCall       `json:"tool_calls"`
	Escalated            bool                     `json:"escalated"`
	EscalationReason     string                   `json:"escalation_reason,omitempty"`
	IncidentID           string                   `json:"incident_id,omitempty"`
	Limitations          []string                 `json:"limitations,omitempty"`
	Insights             WorkflowInsightsStatus   `json:"insights"`
	LLMAnalysis          *LLMAnalysisResult       `json:"llm_analysis,omitempty"`
	ContributingSignals  []ContributingSignal     `json:"contributing_signals,omitempty"`
	CorrelatedTimeWindow *TimeWindow              `json:"correlated_time_window,omitempty"`
	ImpactedScope        []string                 `json:"impacted_scope,omitempty"`
	Confidence           float64                  `json:"confidence"`
	RecommendedToolCalls []string                 `json:"recommended_tool_calls,omitempty"`
	Severity             string                   `json:"severity,omitempty"`
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
	ID               string   `json:"id"`
	Priority         string   `json:"priority"`
	Summary          string   `json:"summary"`
	Details          string   `json:"details,omitempty"`
	Scope            string   `json:"scope,omitempty"`
	Checks           []string `json:"checks,omitempty"`
	Safe             bool     `json:"safe"`
	DryRunDefault    bool     `json:"dry_run_default"`
	RequiresApproval bool     `json:"requires_approval"`
	Reversible       bool     `json:"reversible"`
	RollbackHint     string   `json:"rollback_hint,omitempty"`
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
	ToolVersion      string            `json:"tool_version,omitempty"`
	Query            map[string]string `json:"query,omitempty"`
	Status           string            `json:"status"`
	ResultSummary    string            `json:"result_summary,omitempty"`
	Verified         bool              `json:"verified"`
	VerificationNote string            `json:"verification_note,omitempty"`
	EvidenceIDs      []string          `json:"evidence_ids,omitempty"`
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
	Symptoms             []string           `json:"symptoms"`
	Timeline             []RCATimelineEvent `json:"timeline"`
	Scope                []string           `json:"scope"`
	MostLikelyCause      string             `json:"most_likely_cause"`
	SupportingSignals    []string           `json:"supporting_signals"`
	DisconfirmingSignals []string           `json:"disconfirming_signals"`
	Confidence           float64            `json:"confidence"`
}

// RCAContext captures gathered workflow context.
type RCAContext struct {
	CollectorID      string             `json:"collector_id,omitempty"`
	Window           string             `json:"window"`
	TopMetrics       map[string]float64 `json:"top_metrics"`
	TopProcesses     []string           `json:"top_processes"`
	KernelSignals    []string           `json:"kernel_signals"`
	RecentDeploys    []string           `json:"recent_deploys,omitempty"`
	SecurityFindings []string           `json:"security_findings,omitempty"`
	TopologySummary  string             `json:"topology_summary,omitempty"`
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
	WorkflowID       string                   `json:"workflow_id"`
	PipelineVersion  string                   `json:"pipeline_version"`
	IncidentID       string                   `json:"incident_id"`
	Status           string                   `json:"status"`
	CollectorID      string                   `json:"collector_id,omitempty"`
	Trigger          string                   `json:"trigger"`
	GeneratedAt      time.Time                `json:"generated_at"`
	Context          RCAContext               `json:"context"`
	Anomalies        []string                 `json:"anomalies"`
	Correlations     []RCACorrelation         `json:"correlations"`
	Hypotheses       []RCAHypothesis          `json:"hypotheses"`
	Evidence         []RCAEvidence            `json:"evidence"`
	Recommendations  []WorkflowRecommendation `json:"recommendations"`
	AgentLoop        AgentLoopSummary         `json:"agent_loop"`
	StructuredReport RCAStructuredReport      `json:"structured_report"`
	Stages           []PipelineStageResult    `json:"stages"`
	ToolCalls        []WorkflowToolCall       `json:"tool_calls"`
	Reproducibility  map[string]string        `json:"reproducibility"`
	Limitations      []string                 `json:"limitations,omitempty"`
	Insights         WorkflowInsightsStatus   `json:"insights"`
	LLMAnalysis      *LLMAnalysisResult       `json:"llm_analysis,omitempty"`
}

// AgentIncidentReport is a persisted incident investigation record.
type AgentIncidentReport struct {
	IncidentID      string                   `json:"incident_id"`
	WorkflowID      string                   `json:"workflow_id"`
	Status          string                   `json:"status"`
	Source          string                   `json:"source"`
	CollectorID     string                   `json:"collector_id,omitempty"`
	OpenedAt        time.Time                `json:"opened_at"`
	ClosedAt        *time.Time               `json:"closed_at,omitempty"`
	RiskLevel       string                   `json:"risk_level"`
	RiskScore       float64                  `json:"risk_score"`
	Summary         string                   `json:"summary"`
	MostLikelyCause string                   `json:"most_likely_cause"`
	Confidence      float64                  `json:"confidence"`
	Symptoms        []string                 `json:"symptoms"`
	Timeline        []RCATimelineEvent       `json:"timeline"`
	Evidence        []RCAEvidence            `json:"evidence"`
	Hypotheses      []RCAHypothesis          `json:"hypotheses"`
	Recommendations []WorkflowRecommendation `json:"recommendations"`
	AgentLoop       AgentLoopSummary         `json:"agent_loop"`
	LLMAnalysis     *LLMAnalysisResult       `json:"llm_analysis,omitempty"`
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
	ID                          string                  `json:"id"`
	CollectorID                 string                  `json:"collector_id,omitempty"`
	RiskSummary                 string                  `json:"risk_summary"`
	TimeWindow                  string                  `json:"time_window"`
	Scope                       string                  `json:"scope"`
	ConfidenceScore             float64                 `json:"confidence_score"`
	ContributingSignals         []PotentialRiskSignal   `json:"contributing_signals"`
	SuggestedInvestigationSteps []string                `json:"suggested_investigation_steps"`
	Correlations                []JointRiskCooccurrence `json:"correlations,omitempty"`
	Series                      []RiskSeries            `json:"series,omitempty"`
	GeneratedAt                 time.Time               `json:"generated_at"`
}

// PotentialRiskResponse wraps proactive risk findings.
type PotentialRiskResponse struct {
	Findings  []PotentialRiskFinding `json:"findings"`
	Count     int                    `json:"count"`
	Timestamp time.Time              `json:"timestamp"`
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
