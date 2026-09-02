package agent

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
)

const workflowPipelineVersion = "v0.95-workflow-pipeline"

const (
	WorkflowRuntimeModeDeterministic = "deterministic"
	WorkflowRuntimeModeHybrid        = "hybrid"
	WorkflowRuntimeModeAdaptive      = "adaptive"
)

// WorkflowConfig governs deterministic workflow execution for joint-risk and RCA paths.
type WorkflowConfig struct {
	Enabled                             bool                    `json:"enabled" yaml:"enabled"`
	RuntimeMode                         string                  `json:"runtime_mode" yaml:"runtime_mode"`
	AdaptiveRuntimeEnabled              bool                    `json:"adaptive_runtime_enabled" yaml:"adaptive_runtime_enabled"`
	AutonomousToolSelectionEnabled      bool                    `json:"autonomous_tool_selection_enabled" yaml:"autonomous_tool_selection_enabled"`
	PlannerCriticEnabled                bool                    `json:"planner_critic_enabled" yaml:"planner_critic_enabled"`
	ToolExperienceMemoryEnabled         bool                    `json:"tool_experience_memory_enabled" yaml:"tool_experience_memory_enabled"`
	CheapFirstSelectionEnabled          bool                    `json:"cheap_first_selection_enabled" yaml:"cheap_first_selection_enabled"`
	DefaultWindow                       time.Duration           `json:"default_window" yaml:"default_window"`
	MaxWindow                           time.Duration           `json:"max_window" yaml:"max_window"`
	MaxSamples                          int                     `json:"max_samples" yaml:"max_samples"`
	MaxSignals                          int                     `json:"max_signals" yaml:"max_signals"`
	MaxHypotheses                       int                     `json:"max_hypotheses" yaml:"max_hypotheses"`
	MaxPlanIterations                   int                     `json:"max_plan_iterations" yaml:"max_plan_iterations"`
	MaxPlanSteps                        int                     `json:"max_plan_steps" yaml:"max_plan_steps"`
	AdaptiveMaxIterations               int                     `json:"adaptive_max_iterations" yaml:"adaptive_max_iterations"`
	AdaptiveMaxToolCalls                int                     `json:"adaptive_max_tool_calls" yaml:"adaptive_max_tool_calls"`
	AdaptiveMaxSameToolRetries          int                     `json:"adaptive_max_same_tool_retries" yaml:"adaptive_max_same_tool_retries"`
	AdaptiveMaxHypothesisRewrites       int                     `json:"adaptive_max_hypothesis_rewrites" yaml:"adaptive_max_hypothesis_rewrites"`
	AdaptiveMaxNoProgressRounds         int                     `json:"adaptive_max_no_progress_rounds" yaml:"adaptive_max_no_progress_rounds"`
	AdaptiveMaxPlateauRounds            int                     `json:"adaptive_max_plateau_rounds" yaml:"adaptive_max_plateau_rounds"`
	MaxNoProgressRounds                 int                     `json:"max_no_progress_rounds" yaml:"max_no_progress_rounds"`
	MaxUncertaintyPlateauRounds         int                     `json:"max_uncertainty_plateau_rounds" yaml:"max_uncertainty_plateau_rounds"`
	AdaptiveParallelReadOnlyLimit       int                     `json:"adaptive_parallel_read_only_limit" yaml:"adaptive_parallel_read_only_limit"`
	AdaptiveTimeBudget                  time.Duration           `json:"adaptive_time_budget" yaml:"adaptive_time_budget"`
	AuditRetention                      int                     `json:"audit_retention" yaml:"audit_retention"`
	IncidentRetention                   int                     `json:"incident_retention" yaml:"incident_retention"`
	DryRun                              bool                    `json:"dry_run" yaml:"dry_run"`
	RequireApproval                     bool                    `json:"require_approval" yaml:"require_approval"`
	AllowProfilingExec                  bool                    `json:"allow_profiling_exec" yaml:"allow_profiling_exec"`
	AllowRemediationExec                bool                    `json:"allow_remediation_exec" yaml:"allow_remediation_exec"`
	AllowSafeReversibleExec             bool                    `json:"allow_safe_reversible_exec" yaml:"allow_safe_reversible_exec"`
	AllowImpactingExec                  bool                    `json:"allow_impacting_exec" yaml:"allow_impacting_exec"`
	AutoEscalateOnHighRisk              bool                    `json:"auto_escalate_on_high_risk" yaml:"auto_escalate_on_high_risk"`
	ProfilingCommand                    string                  `json:"profiling_command" yaml:"profiling_command"`
	InsightsEnabled                     bool                    `json:"insights_enabled" yaml:"insights_enabled"`
	InsightsProvider                    string                  `json:"insights_provider" yaml:"insights_provider"`
	InsightsModel                       string                  `json:"insights_model" yaml:"insights_model"`
	InsightsAPIKeyEnv                   string                  `json:"insights_api_key_env" yaml:"insights_api_key_env"`
	LLMTimeout                          time.Duration           `json:"llm_timeout" yaml:"llm_timeout"`
	LLMRateLimitRPS                     float64                 `json:"llm_rate_limit_rps" yaml:"llm_rate_limit_rps"`
	LLMRateBurst                        int                     `json:"llm_rate_burst" yaml:"llm_rate_burst"`
	AdvancedReasoningEnabled            bool                    `json:"advanced_reasoning_enabled" yaml:"advanced_reasoning_enabled"`
	AdvancedReasoningTimeout            time.Duration           `json:"advanced_reasoning_timeout" yaml:"advanced_reasoning_timeout"`
	AdvancedReasoningMaxBranches        int                     `json:"advanced_reasoning_max_branches" yaml:"advanced_reasoning_max_branches"`
	AdvancedReasoningAmbiguityThreshold float64                 `json:"advanced_reasoning_ambiguity_threshold" yaml:"advanced_reasoning_ambiguity_threshold"`
	ReasoningTokenBudget                int                     `json:"reasoning_token_budget" yaml:"reasoning_token_budget"`
	MaxRefineIterations                 int                     `json:"max_refine_iterations" yaml:"max_refine_iterations"`
	RefineConfidenceThreshold           float64                 `json:"refine_confidence_threshold" yaml:"refine_confidence_threshold"`
	ReasoningSeverityPolicy             ReasoningSeverityPolicy `json:"reasoning_severity_policy" yaml:"reasoning_severity_policy"`
	DegradedModePolicy                  string                  `json:"degraded_mode_policy" yaml:"degraded_mode_policy"`
	HighRiskScoreThreshold              float64                 `json:"high_risk_score_threshold" yaml:"high_risk_score_threshold"`
	MediumRiskThreshold                 float64                 `json:"medium_risk_threshold" yaml:"medium_risk_threshold"`
	RequestDedupeTTL                    time.Duration           `json:"request_dedupe_ttl" yaml:"request_dedupe_ttl"`
	RequestDedupeEntries                int                     `json:"request_dedupe_entries" yaml:"request_dedupe_entries"`
	WorkflowDataPath                    string                  `json:"workflow_data_path" yaml:"workflow_data_path"`
	WorkflowStoreBackend                string                  `json:"workflow_store_backend" yaml:"workflow_store_backend"`
	WorkflowStorePath                   string                  `json:"workflow_store_path" yaml:"workflow_store_path"`
	WorkflowStorePostgresDSN            string                  `json:"-" yaml:"-"`
	ArtifactMetadataBackend             string                  `json:"artifact_metadata_backend" yaml:"artifact_metadata_backend"`
	ArtifactMetadataPath                string                  `json:"artifact_metadata_path" yaml:"artifact_metadata_path"`
	ArtifactMetadataPostgresDSN         string                  `json:"-" yaml:"-"`
	ArtifactPayloadBackend              string                  `json:"artifact_payload_backend" yaml:"artifact_payload_backend"`
	ArtifactPayloadRootPath             string                  `json:"artifact_payload_root_path" yaml:"artifact_payload_root_path"`
	ArtifactPayloadShared               bool                    `json:"artifact_payload_shared" yaml:"artifact_payload_shared"`
	ArtifactPayloadS3Endpoint           string                  `json:"artifact_payload_s3_endpoint" yaml:"artifact_payload_s3_endpoint"`
	ArtifactPayloadS3Region             string                  `json:"artifact_payload_s3_region" yaml:"artifact_payload_s3_region"`
	ArtifactPayloadS3Bucket             string                  `json:"artifact_payload_s3_bucket" yaml:"artifact_payload_s3_bucket"`
	ArtifactPayloadS3Prefix             string                  `json:"artifact_payload_s3_prefix" yaml:"artifact_payload_s3_prefix"`
	ArtifactPayloadS3AccessKey          string                  `json:"-" yaml:"-"`
	ArtifactPayloadS3SecretKey          string                  `json:"-" yaml:"-"`
	ArtifactPayloadS3SessionToken       string                  `json:"-" yaml:"-"`
	ArtifactPayloadS3PathStyle          bool                    `json:"artifact_payload_s3_path_style" yaml:"artifact_payload_s3_path_style"`
	ArtifactPayloadS3Insecure           bool                    `json:"artifact_payload_s3_insecure" yaml:"artifact_payload_s3_insecure"`
	ArtifactGCEnabled                   bool                    `json:"artifact_gc_enabled" yaml:"artifact_gc_enabled"`
	ArtifactGCInterval                  time.Duration           `json:"artifact_gc_interval" yaml:"artifact_gc_interval"`
	ArtifactGCBatchSize                 int                     `json:"artifact_gc_batch_size" yaml:"artifact_gc_batch_size"`
	AgentMessageProtocolEnabled         bool                    `json:"agent_message_protocol_enabled" yaml:"agent_message_protocol_enabled"`
	AgentMessageDir                     string                  `json:"agent_message_dir" yaml:"agent_message_dir"`
	AgentMessagePrettyJSON              bool                    `json:"agent_message_pretty_json" yaml:"agent_message_pretty_json"`
	AgentMessageHistoryLimit            int                     `json:"agent_message_history_limit" yaml:"agent_message_history_limit"`
	AgentMessageSchemaVersion           string                  `json:"agent_message_schema_version" yaml:"agent_message_schema_version"`
	ToolRetryCount                      int                     `json:"tool_retry_count" yaml:"tool_retry_count"`
	MaxTelemetryAge                     time.Duration           `json:"max_telemetry_age" yaml:"max_telemetry_age"`
	PolicyVersion                       string                  `json:"policy_version" yaml:"policy_version"`
	ActionRunner                        RunnerConfig            `json:"action_runner" yaml:"action_runner"`
	VerificationWindow                  time.Duration           `json:"verification_window" yaml:"verification_window"`
	AutoRollbackOnVerificationFailure   bool                    `json:"auto_rollback_on_verification_failure" yaml:"auto_rollback_on_verification_failure"`
	BehaviorMemory                      BehavioralMemoryConfig  `json:"behavior_memory" yaml:"behavior_memory"`
	AnalysisToValidationHandoffEnabled  bool                    `json:"analysis_to_validation_handoff_enabled" yaml:"analysis_to_validation_handoff_enabled"`
	ValidationAgentEnabled              bool                    `json:"validation_agent_enabled" yaml:"validation_agent_enabled"`
	ValidationMaxIterations             int                     `json:"validation_max_iterations" yaml:"validation_max_iterations"`
	ValidationMaxToolCalls              int                     `json:"validation_max_tool_calls" yaml:"validation_max_tool_calls"`
	ValidationTimeout                   time.Duration           `json:"validation_timeout" yaml:"validation_timeout"`
	ValidationReadOnlyOnly              bool                    `json:"validation_read_only_only" yaml:"validation_read_only_only"`
	ValidationConfidenceThreshold       float64                 `json:"validation_confidence_threshold" yaml:"validation_confidence_threshold"`
	ValidationDegradedFallback          string                  `json:"validation_degraded_fallback" yaml:"validation_degraded_fallback"`
	ValidationAllowExecCategories       []string                `json:"validation_allow_exec_categories" yaml:"validation_allow_exec_categories"`
	ValidationTargetLimit               int                     `json:"validation_target_limit" yaml:"validation_target_limit"`
	PostActionValidationWindow          time.Duration           `json:"post_action_validation_window" yaml:"post_action_validation_window"`
}

// WorkflowDurabilityStatus reports the active local durability posture of the
// workflow runtime. It describes the store that actually came up, not only the
// configured intent.
type WorkflowDurabilityStatus struct {
	Enabled              bool   `json:"enabled"`
	Backend              string `json:"backend"`
	ConfiguredBackend    string `json:"configured_backend,omitempty"`
	Persistent           bool   `json:"persistent"`
	LocalFirst           bool   `json:"local_first"`
	Shared               bool   `json:"shared"`
	Mode                 string `json:"mode"`
	FallbackActive       bool   `json:"fallback_active"`
	ApprovalStateBackend string `json:"approval_state_backend,omitempty"`
	IdempotencyBackend   string `json:"idempotency_backend,omitempty"`
	StorePath            string `json:"store_path,omitempty"`
	DataPath             string `json:"data_path,omitempty"`
	MessageDir           string `json:"message_dir,omitempty"`
	LastError            string `json:"last_error,omitempty"`
}

// ArtifactDurabilityStatus reports how workflow-adjacent artifacts are
// addressed and where their metadata and payloads live.
type ArtifactDurabilityStatus struct {
	Enabled                       bool   `json:"enabled"`
	MetadataBackend               string `json:"metadata_backend"`
	MetadataPersistent            bool   `json:"metadata_persistent"`
	MetadataShared                bool   `json:"metadata_shared"`
	PayloadBackend                string `json:"payload_backend"`
	PayloadRootPath               string `json:"payload_root_path,omitempty"`
	PayloadShared                 bool   `json:"payload_shared"`
	PayloadSharedSurvivable       bool   `json:"payload_shared_survivable"`
	PayloadContainer              string `json:"payload_container,omitempty"`
	PayloadPrefix                 string `json:"payload_prefix,omitempty"`
	AddressingMode                string `json:"addressing_mode"`
	LocalCacheActive              bool   `json:"local_cache_active"`
	GCEnabled                     bool   `json:"gc_enabled"`
	GCInterval                    string `json:"gc_interval,omitempty"`
	GCBatchSize                   int    `json:"gc_batch_size,omitempty"`
	GCLastRunAt                   string `json:"gc_last_run_at,omitempty"`
	GCArtifactsScanned            int    `json:"gc_artifacts_scanned,omitempty"`
	GCArtifactsDeleted            int    `json:"gc_artifacts_deleted,omitempty"`
	GCDeleteFailures              int    `json:"gc_delete_failures,omitempty"`
	GCOrphanedMetadata            int    `json:"gc_orphaned_metadata,omitempty"`
	MessageHistoryMetadataBackend string `json:"message_history_metadata_backend,omitempty"`
	EvidenceMetadataBackend       string `json:"evidence_metadata_backend,omitempty"`
	IncidentMemoryMetadataBackend string `json:"incident_memory_metadata_backend,omitempty"`
	RAGMetadataBackend            string `json:"rag_metadata_backend,omitempty"`
	RAGIndexBackend               string `json:"rag_index_backend,omitempty"`
	LastError                     string `json:"last_error,omitempty"`
}

// BehavioralMemoryConfig governs controller-side workload baseline lookups
// derived from the existing metric history provider.
type BehavioralMemoryConfig struct {
	Enabled            bool          `json:"enabled" yaml:"enabled"`
	LongWindow         time.Duration `json:"long_window" yaml:"long_window"`
	MinSamples         int           `json:"min_samples" yaml:"min_samples"`
	MinRecurringBursts int           `json:"min_recurring_bursts" yaml:"min_recurring_bursts"`
	CacheEntries       int           `json:"cache_entries" yaml:"cache_entries"`
	CacheTTL           time.Duration `json:"cache_ttl" yaml:"cache_ttl"`
}

// DefaultBehavioralMemoryConfig returns conservative defaults that avoid early suppression.
func DefaultBehavioralMemoryConfig() BehavioralMemoryConfig {
	return BehavioralMemoryConfig{
		Enabled:            true,
		LongWindow:         14 * 24 * time.Hour,
		MinSamples:         12,
		MinRecurringBursts: 2,
		CacheEntries:       128,
		CacheTTL:           2 * time.Minute,
	}
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
		RuntimeMode:                         WorkflowRuntimeModeLegacyDeterministic,
		AdaptiveRuntimeEnabled:              false,
		AutonomousToolSelectionEnabled:      false,
		PlannerCriticEnabled:                false,
		ToolExperienceMemoryEnabled:         false,
		CheapFirstSelectionEnabled:          false,
		DefaultWindow:                       45 * time.Minute,
		MaxWindow:                           24 * time.Hour,
		MaxSamples:                          720,
		MaxSignals:                          20,
		MaxHypotheses:                       8,
		MaxPlanIterations:                   3,
		MaxPlanSteps:                        10,
		AdaptiveMaxIterations:               3,
		AdaptiveMaxToolCalls:                5,
		AdaptiveMaxSameToolRetries:          1,
		AdaptiveMaxHypothesisRewrites:       2,
		AdaptiveMaxNoProgressRounds:         2,
		AdaptiveMaxPlateauRounds:            2,
		MaxNoProgressRounds:                 2,
		MaxUncertaintyPlateauRounds:         2,
		AdaptiveParallelReadOnlyLimit:       2,
		AdaptiveTimeBudget:                  20 * time.Second,
		AuditRetention:                      2000,
		IncidentRetention:                   1000,
		DryRun:                              true,
		RequireApproval:                     true,
		AllowProfilingExec:                  false,
		AllowRemediationExec:                false,
		AllowSafeReversibleExec:             false,
		AllowImpactingExec:                  false,
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
		WorkflowDataPath:                    "data/agent/workflows",
		WorkflowStoreBackend:                "bbolt",
		WorkflowStorePath:                   "data/agent/workflow_runs.db",
		ArtifactMetadataBackend:             "workflow",
		ArtifactMetadataPath:                "data/agent/workflows/artifacts.db",
		ArtifactPayloadBackend:              "filesystem",
		ArtifactPayloadRootPath:             "data/agent/workflows",
		ArtifactPayloadShared:               false,
		ArtifactGCEnabled:                   true,
		ArtifactGCInterval:                  time.Hour,
		ArtifactGCBatchSize:                 128,
		AgentMessageProtocolEnabled:         true,
		AgentMessageDir:                     "data/agent/workflows/messages",
		AgentMessagePrettyJSON:              true,
		AgentMessageHistoryLimit:            200,
		AgentMessageSchemaVersion:           "agent-message/v1",
		ToolRetryCount:                      1,
		MaxTelemetryAge:                     2 * time.Minute,
		PolicyVersion:                       "workflow-policy/v1",
		ActionRunner:                        DefaultRunnerConfig(),
		VerificationWindow:                  2 * time.Minute,
		BehaviorMemory:                      DefaultBehavioralMemoryConfig(),
		AnalysisToValidationHandoffEnabled:  true,
		ValidationAgentEnabled:              true,
		ValidationMaxIterations:             8,
		ValidationMaxToolCalls:              12,
		ValidationTimeout:                   30 * time.Second,
		ValidationReadOnlyOnly:              true,
		ValidationConfidenceThreshold:       0.68,
		ValidationDegradedFallback:          "deterministic_report",
		ValidationAllowExecCategories:       []string{},
		ValidationTargetLimit:               6,
		PostActionValidationWindow:          10 * time.Minute,
	}
}

func normalizeWorkflowConfig(cfg WorkflowConfig) WorkflowConfig {
	def := DefaultWorkflowConfig()
	cfg.RuntimeMode = canonicalWorkflowRuntimeMode(firstNonEmpty(cfg.RuntimeMode, def.RuntimeMode))
	cfg.DefaultWindow = defaultDuration(cfg.DefaultWindow, def.DefaultWindow)
	cfg.MaxWindow = defaultDuration(cfg.MaxWindow, def.MaxWindow)
	cfg.MaxSamples = defaultInt(cfg.MaxSamples, def.MaxSamples)
	cfg.MaxSignals = defaultInt(cfg.MaxSignals, def.MaxSignals)
	cfg.MaxHypotheses = defaultInt(cfg.MaxHypotheses, def.MaxHypotheses)
	cfg.MaxPlanIterations = defaultInt(cfg.MaxPlanIterations, def.MaxPlanIterations)
	cfg.MaxPlanSteps = defaultInt(cfg.MaxPlanSteps, def.MaxPlanSteps)
	cfg.ToolExperienceMemoryEnabled = defaultBool(cfg.ToolExperienceMemoryEnabled, def.ToolExperienceMemoryEnabled)
	cfg.CheapFirstSelectionEnabled = defaultBool(cfg.CheapFirstSelectionEnabled, def.CheapFirstSelectionEnabled)
	cfg.AdaptiveMaxIterations = defaultInt(cfg.AdaptiveMaxIterations, def.AdaptiveMaxIterations)
	cfg.AdaptiveMaxToolCalls = defaultInt(cfg.AdaptiveMaxToolCalls, def.AdaptiveMaxToolCalls)
	cfg.AdaptiveMaxSameToolRetries = defaultInt(cfg.AdaptiveMaxSameToolRetries, def.AdaptiveMaxSameToolRetries)
	cfg.AdaptiveMaxHypothesisRewrites = defaultInt(cfg.AdaptiveMaxHypothesisRewrites, def.AdaptiveMaxHypothesisRewrites)
	cfg.AdaptiveMaxNoProgressRounds = defaultInt(cfg.AdaptiveMaxNoProgressRounds, def.AdaptiveMaxNoProgressRounds)
	cfg.AdaptiveMaxPlateauRounds = defaultInt(cfg.AdaptiveMaxPlateauRounds, def.AdaptiveMaxPlateauRounds)
	cfg.MaxNoProgressRounds = defaultInt(cfg.MaxNoProgressRounds, maxInt(def.MaxNoProgressRounds, cfg.AdaptiveMaxNoProgressRounds))
	cfg.MaxUncertaintyPlateauRounds = defaultInt(cfg.MaxUncertaintyPlateauRounds, maxInt(def.MaxUncertaintyPlateauRounds, cfg.AdaptiveMaxPlateauRounds))
	cfg.AdaptiveParallelReadOnlyLimit = defaultInt(cfg.AdaptiveParallelReadOnlyLimit, def.AdaptiveParallelReadOnlyLimit)
	cfg.AdaptiveRuntimeEnabled = runtimeModeEnablesAdaptiveRuntime(cfg)
	cfg.AutonomousToolSelectionEnabled = runtimeModeEnablesAutonomousToolSelection(cfg)
	cfg.PlannerCriticEnabled = runtimeModeEnablesPlannerCritic(cfg)
	cfg.AdaptiveTimeBudget = defaultDuration(cfg.AdaptiveTimeBudget, def.AdaptiveTimeBudget)
	cfg.AuditRetention = defaultInt(cfg.AuditRetention, def.AuditRetention)
	cfg.IncidentRetention = defaultInt(cfg.IncidentRetention, def.IncidentRetention)
	cfg.ProfilingCommand = defaultTrimmedString(cfg.ProfilingCommand, def.ProfilingCommand)
	cfg.InsightsProvider = defaultTrimmedString(cfg.InsightsProvider, def.InsightsProvider)
	cfg.InsightsModel = defaultTrimmedString(cfg.InsightsModel, def.InsightsModel)
	cfg.InsightsAPIKeyEnv = defaultTrimmedString(cfg.InsightsAPIKeyEnv, def.InsightsAPIKeyEnv)
	cfg.LLMTimeout = defaultDuration(cfg.LLMTimeout, def.LLMTimeout)
	cfg.LLMRateLimitRPS = defaultFloat(cfg.LLMRateLimitRPS, def.LLMRateLimitRPS)
	cfg.LLMRateBurst = defaultInt(cfg.LLMRateBurst, def.LLMRateBurst)
	cfg.AdvancedReasoningTimeout = defaultDuration(cfg.AdvancedReasoningTimeout, def.AdvancedReasoningTimeout)
	cfg.AdvancedReasoningMaxBranches = defaultInt(cfg.AdvancedReasoningMaxBranches, def.AdvancedReasoningMaxBranches)
	cfg.AdvancedReasoningAmbiguityThreshold = defaultUnitFloat(cfg.AdvancedReasoningAmbiguityThreshold, def.AdvancedReasoningAmbiguityThreshold)
	cfg.ReasoningTokenBudget = defaultInt(cfg.ReasoningTokenBudget, def.ReasoningTokenBudget)
	cfg.MaxRefineIterations = defaultInt(cfg.MaxRefineIterations, def.MaxRefineIterations)
	cfg.RefineConfidenceThreshold = defaultUnitFloat(cfg.RefineConfidenceThreshold, def.RefineConfidenceThreshold)
	if cfg.ReasoningSeverityPolicy.Critical == "" {
		cfg.ReasoningSeverityPolicy = def.ReasoningSeverityPolicy
	}
	cfg.DegradedModePolicy = normalizeEnum(cfg.DegradedModePolicy, def.DegradedModePolicy, "skip_reasoning", "deterministic_only", "wait_retry")
	cfg.HighRiskScoreThreshold = defaultUnitFloat(cfg.HighRiskScoreThreshold, def.HighRiskScoreThreshold)
	cfg.MediumRiskThreshold = defaultUnitFloat(cfg.MediumRiskThreshold, def.MediumRiskThreshold)
	if cfg.MediumRiskThreshold > cfg.HighRiskScoreThreshold {
		cfg.MediumRiskThreshold = cfg.HighRiskScoreThreshold * 0.75
	}
	cfg.RequestDedupeTTL = defaultDuration(cfg.RequestDedupeTTL, def.RequestDedupeTTL)
	cfg.RequestDedupeEntries = defaultInt(cfg.RequestDedupeEntries, def.RequestDedupeEntries)
	cfg.WorkflowDataPath = defaultTrimmedString(cfg.WorkflowDataPath, def.WorkflowDataPath)
	workflowDataOverridden := filepath.Clean(strings.TrimSpace(cfg.WorkflowDataPath)) != filepath.Clean(def.WorkflowDataPath)
	cfg.WorkflowStoreBackend = normalizeStoreBackend(cfg.WorkflowStoreBackend, def.WorkflowStoreBackend)
	cfg.WorkflowStorePath = defaultTrimmedString(cfg.WorkflowStorePath, def.WorkflowStorePath)
	cfg.ArtifactMetadataBackend = normalizeArtifactMetadataBackend(cfg.ArtifactMetadataBackend, cfg.WorkflowStoreBackend)
	cfg.ArtifactMetadataPath = defaultTrimmedString(cfg.ArtifactMetadataPath, def.ArtifactMetadataPath)
	if workflowDataOverridden && filepath.Clean(strings.TrimSpace(cfg.ArtifactMetadataPath)) == filepath.Clean(def.ArtifactMetadataPath) {
		cfg.ArtifactMetadataPath = filepath.Join(cfg.WorkflowDataPath, "artifacts.db")
	}
	if strings.TrimSpace(cfg.ArtifactMetadataPostgresDSN) == "" {
		cfg.ArtifactMetadataPostgresDSN = cfg.WorkflowStorePostgresDSN
	}
	cfg.ArtifactPayloadBackend = normalizeArtifactPayloadBackend(cfg.ArtifactPayloadBackend, def.ArtifactPayloadBackend)
	cfg.ArtifactPayloadRootPath = defaultTrimmedString(cfg.ArtifactPayloadRootPath, def.ArtifactPayloadRootPath)
	if cfg.ArtifactPayloadBackend == "filesystem" && workflowDataOverridden && filepath.Clean(strings.TrimSpace(cfg.ArtifactPayloadRootPath)) == filepath.Clean(def.ArtifactPayloadRootPath) {
		cfg.ArtifactPayloadRootPath = cfg.WorkflowDataPath
	}
	cfg.ArtifactPayloadS3Endpoint = strings.TrimSpace(cfg.ArtifactPayloadS3Endpoint)
	cfg.ArtifactPayloadS3Region = strings.TrimSpace(cfg.ArtifactPayloadS3Region)
	cfg.ArtifactPayloadS3Bucket = strings.TrimSpace(cfg.ArtifactPayloadS3Bucket)
	cfg.ArtifactPayloadS3Prefix = strings.Trim(strings.TrimSpace(cfg.ArtifactPayloadS3Prefix), "/")
	cfg.ArtifactGCInterval = defaultDuration(cfg.ArtifactGCInterval, def.ArtifactGCInterval)
	cfg.ArtifactGCBatchSize = defaultInt(cfg.ArtifactGCBatchSize, def.ArtifactGCBatchSize)
	cfg.AgentMessageDir = defaultTrimmedString(cfg.AgentMessageDir, def.AgentMessageDir)
	if workflowDataOverridden && filepath.Clean(strings.TrimSpace(cfg.AgentMessageDir)) == filepath.Clean(def.AgentMessageDir) {
		cfg.AgentMessageDir = filepath.Join(cfg.WorkflowDataPath, "messages")
	}
	cfg.AgentMessageHistoryLimit = defaultInt(cfg.AgentMessageHistoryLimit, def.AgentMessageHistoryLimit)
	cfg.AgentMessageSchemaVersion = defaultTrimmedString(cfg.AgentMessageSchemaVersion, def.AgentMessageSchemaVersion)
	if cfg.ToolRetryCount < 0 {
		cfg.ToolRetryCount = def.ToolRetryCount
	}
	cfg.MaxTelemetryAge = defaultDuration(cfg.MaxTelemetryAge, def.MaxTelemetryAge)
	cfg.PolicyVersion = defaultTrimmedString(cfg.PolicyVersion, def.PolicyVersion)
	cfg.VerificationWindow = defaultDuration(cfg.VerificationWindow, def.VerificationWindow)
	cfg.ValidationMaxIterations = defaultInt(cfg.ValidationMaxIterations, def.ValidationMaxIterations)
	cfg.ValidationMaxToolCalls = defaultInt(cfg.ValidationMaxToolCalls, def.ValidationMaxToolCalls)
	cfg.ValidationTimeout = defaultDuration(cfg.ValidationTimeout, def.ValidationTimeout)
	cfg.ValidationConfidenceThreshold = defaultUnitFloat(cfg.ValidationConfidenceThreshold, def.ValidationConfidenceThreshold)
	cfg.ValidationDegradedFallback = normalizeEnum(cfg.ValidationDegradedFallback, def.ValidationDegradedFallback, "deterministic_report", "skip_validation")
	cfg.ValidationTargetLimit = defaultInt(cfg.ValidationTargetLimit, def.ValidationTargetLimit)
	cfg.PostActionValidationWindow = defaultDuration(cfg.PostActionValidationWindow, def.PostActionValidationWindow)
	cfg.BehaviorMemory = normalizeBehaviorMemoryConfig(cfg.BehaviorMemory, def.BehaviorMemory)
	return cfg
}

func defaultDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultBool(value, fallback bool) bool {
	if !value {
		return fallback
	}
	return value
}

func defaultFloat(value, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultUnitFloat(value, fallback float64) float64 {
	if value <= 0 || value > 1 {
		return fallback
	}
	return value
}

func defaultTrimmedString(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func normalizeEnum(value, fallback string, allowed ...string) string {
	trimmed := strings.TrimSpace(value)
	for _, option := range allowed {
		if trimmed == option {
			return option
		}
	}
	return fallback
}

func normalizeStoreBackend(value, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "bbolt", "local", "embedded_bbolt":
		return "bbolt"
	case "postgres", "postgresql":
		return "postgres"
	case "memory", "in_memory":
		return "memory"
	default:
		return fallback
	}
}

func normalizeArtifactMetadataBackend(value, workflowStoreBackend string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "workflow":
		return workflowStoreBackend
	case "bbolt", "local", "embedded_bbolt":
		return "bbolt"
	case "postgres", "postgresql":
		return "postgres"
	case "memory", "in_memory":
		return "memory"
	default:
		return workflowStoreBackend
	}
}

func normalizeArtifactPayloadBackend(value, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "filesystem", "localfs":
		return "filesystem"
	case "s3":
		return "s3"
	default:
		return fallback
	}
}

func normalizeBehaviorMemoryConfig(cfg, fallback BehavioralMemoryConfig) BehavioralMemoryConfig {
	if !cfg.Enabled && cfg == (BehavioralMemoryConfig{}) {
		cfg = fallback
	}
	cfg.LongWindow = defaultDuration(cfg.LongWindow, fallback.LongWindow)
	cfg.MinSamples = defaultInt(cfg.MinSamples, fallback.MinSamples)
	cfg.MinRecurringBursts = defaultInt(cfg.MinRecurringBursts, fallback.MinRecurringBursts)
	cfg.CacheEntries = defaultInt(cfg.CacheEntries, fallback.CacheEntries)
	cfg.CacheTTL = defaultDuration(cfg.CacheTTL, fallback.CacheTTL)
	return cfg
}

// ToolName identifies explicit workflow tools.
type ToolName string

const (
	ToolMetrics            ToolName = "metrics_query"
	ToolLogs               ToolName = "logs_query"
	ToolChangeQuery        ToolName = "change_query"
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
	ToolDeploymentHistory  ToolName = "deployment_history_query"
	ToolConfigState        ToolName = "config_state_query"
	ToolMemoryPressure     ToolName = "memory_pressure_analysis"
	ToolConnectivityCheck  ToolName = "connectivity_check"
	ToolDNSCheck           ToolName = "dns_check"
	ToolServiceHealth      ToolName = "service_health_check"
	ToolKubernetesResource ToolName = "kubernetes_resource_query"
	ToolContainerRevision  ToolName = "container_revision_query"
	ToolStorageHealth      ToolName = "storage_health_query"
	ToolNetworkBlastRadius ToolName = "network_blast_radius_query"
	ToolActionOutcome      ToolName = "prior_action_outcome_retrieval"
)

// WorkflowToolDescriptor is an explicit, versioned tool registry entry.
type WorkflowToolDescriptor struct {
	Name                    ToolName                `json:"name"`
	Version                 string                  `json:"version"`
	Description             string                  `json:"description"`
	Purpose                 string                  `json:"purpose"`
	CapabilityFamily        string                  `json:"capability_family,omitempty"`
	InputSchema             string                  `json:"input_schema,omitempty"`
	OutputSchema            string                  `json:"output_schema,omitempty"`
	Deterministic           bool                    `json:"deterministic"`
	ReadOnly                bool                    `json:"read_only"`
	RequiresApproval        bool                    `json:"requires_approval"`
	ApprovalRequired        bool                    `json:"approval_required"`
	SupportsDryRun          bool                    `json:"supports_dry_run"`
	SupportsRollback        bool                    `json:"supports_rollback"`
	SideEffects             string                  `json:"side_effects,omitempty"`
	SafetyClass             string                  `json:"safety_class,omitempty"`
	AutonomyEligibility     ToolAutonomyEligibility `json:"autonomy_eligibility,omitempty"`
	CostClass               string                  `json:"cost_class,omitempty"`
	FreshnessSensitivity    string                  `json:"freshness_sensitivity,omitempty"`
	ScopeSensitivity        string                  `json:"scope_sensitivity,omitempty"`
	ExpectedInformationGain float64                 `json:"expected_information_gain,omitempty"`
	PolicyStatus            string                  `json:"policy_status,omitempty"`
	LastResultQuality       string                  `json:"last_result_quality,omitempty"`
	LastLowYieldCount       int                     `json:"last_low_yield_count"`
	Unsafe                  bool                    `json:"unsafe,omitempty"`
	Contract                WorkflowToolContract    `json:"contract"`
	RichContract            ToolContract            `json:"rich_contract"`
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
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	Outcome      string    `json:"outcome,omitempty"`
	DetailStatus string    `json:"detail_status,omitempty"`
	Summary      string    `json:"summary"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at"`
}

// WorkflowToolCall captures a single tool invocation and output summary.
type WorkflowToolCall struct {
	ID                string               `json:"id"`
	Tool              ToolName             `json:"tool"`
	ToolVersion       string               `json:"tool_version,omitempty"`
	ToolContract      string               `json:"tool_contract,omitempty"`
	Stage             string               `json:"stage"`
	Actor             string               `json:"actor,omitempty"`
	CollectorID       string               `json:"collector_id,omitempty"`
	Window            string               `json:"window,omitempty"`
	Query             map[string]string    `json:"query,omitempty"`
	DryRun            bool                 `json:"dry_run,omitempty"`
	RiskTag           string               `json:"risk_tag,omitempty"`
	ExecutionCategory string               `json:"execution_category,omitempty"`
	ActionIntent      string               `json:"action_intent,omitempty"`
	Policy            ActionPolicyDecision `json:"policy,omitempty"`
	PolicyVersion     string               `json:"policy_version,omitempty"`
	ApprovalState     string               `json:"approval_state,omitempty"`
	IdempotencyKey    string               `json:"idempotency_key,omitempty"`
	Attempts          int                  `json:"attempts,omitempty"`
	Status            string               `json:"status"`
	InvocationStatus  string               `json:"invocation_status,omitempty"`
	Outcome           string               `json:"outcome,omitempty"`
	Summary           string               `json:"summary,omitempty"`
	ResultKind        string               `json:"result_kind,omitempty"`
	ResultPayload     string               `json:"result_payload,omitempty"`
	TimedOut          bool                 `json:"timed_out,omitempty"`
	StartedAt         time.Time            `json:"started_at"`
	CompletedAt       time.Time            `json:"completed_at"`
	ErrorMessage      string               `json:"error_message,omitempty"`
}

// WorkflowAuditRecord tracks audited tool/action events.
type WorkflowAuditRecord struct {
	ID                  string            `json:"id"`
	TraceID             string            `json:"trace_id,omitempty"`
	WorkflowID          string            `json:"workflow_id"`
	IncidentID          string            `json:"incident_id,omitempty"`
	CorrelationID       string            `json:"correlation_id,omitempty"`
	WorkflowType        string            `json:"workflow_type"`
	RuntimeMode         string            `json:"runtime_mode,omitempty"`
	Iteration           int               `json:"iteration,omitempty"`
	Stage               string            `json:"stage"`
	Action              string            `json:"action"`
	Objective           string            `json:"objective,omitempty"`
	Tool                ToolName          `json:"tool,omitempty"`
	SelectedSkill       ToolName          `json:"selected_skill,omitempty"`
	CandidateSkillCount int               `json:"candidate_skill_count,omitempty"`
	SelectedScore       string            `json:"selected_score,omitempty"`
	CollectorID         string            `json:"collector_id,omitempty"`
	DryRun              bool              `json:"dry_run"`
	RequiresApproval    bool              `json:"requires_approval"`
	Approved            bool              `json:"approved"`
	PolicyVerdict       string            `json:"policy_verdict,omitempty"`
	ApprovalState       string            `json:"approval_state,omitempty"`
	ExecutionCategory   string            `json:"execution_category,omitempty"`
	ActionIntent        string            `json:"action_intent,omitempty"`
	Status              string            `json:"status"`
	Outcome             string            `json:"outcome,omitempty"`
	DetailStatus        string            `json:"detail_status,omitempty"`
	ToolCallStatus      string            `json:"tool_call_status,omitempty"`
	Input               map[string]string `json:"input,omitempty"`
	InputArtifactIDs    []string          `json:"input_artifact_ids,omitempty"`
	OutputArtifactIDs   []string          `json:"output_artifact_ids,omitempty"`
	EvidenceIDs         []string          `json:"evidence_ids,omitempty"`
	StopReason          string            `json:"stop_reason,omitempty"`
	BranchKind          string            `json:"branch_kind,omitempty"`
	ResultQuality       string            `json:"result_quality,omitempty"`
	LowYield            bool              `json:"low_yield,omitempty"`
	ErrorClass          string            `json:"error_class,omitempty"`
	OutputSummary       string            `json:"output_summary,omitempty"`
	ErrorMessage        string            `json:"error_message,omitempty"`
	Timestamp           time.Time         `json:"timestamp"`
}

// WorkflowMetricsSnapshot exposes controller-side workflow telemetry for /metrics and status endpoints.
type WorkflowMetricsSnapshot struct {
	ReasoningStepsTotal       uint64                          `json:"reasoning_steps_total"`
	ReasoningFailuresTotal    uint64                          `json:"reasoning_failures_total"`
	ReasoningParseFailTotal   uint64                          `json:"reasoning_parse_failures_total"`
	ReasoningValidFailTotal   uint64                          `json:"reasoning_validation_failures_total"`
	ReasoningLLMErrorTotal    uint64                          `json:"reasoning_llm_errors_total"`
	ReasoningBudgetExhTotal   uint64                          `json:"reasoning_budget_exhausted_total"`
	AvgConfidence             float64                         `json:"avg_confidence"`
	TokenCostTotal            uint64                          `json:"token_cost_total"`
	TokenCostPerIncident      float64                         `json:"token_cost_per_incident"`
	HallucinationProxyTotal   uint64                          `json:"hallucination_proxy_total"`
	RetrievalHitsTotal        uint64                          `json:"retrieval_hits_total"`
	RetrievalMissTotal        uint64                          `json:"retrieval_miss_total"`
	ActionsExecutedTotal      uint64                          `json:"actions_executed_total"`
	ActionsDryRunTotal        uint64                          `json:"actions_dry_run_total"`
	ActionsBlockedTotal       uint64                          `json:"actions_blocked_total"`
	WorkflowRunsTotal         uint64                          `json:"workflow_runs_total"`
	WorkflowLatencySeconds    float64                         `json:"workflow_latency_seconds_total"`
	IncidentRCARunsTotal      uint64                          `json:"incident_rca_runs_total"`
	IncidentRCALatencySeconds float64                         `json:"incident_rca_latency_seconds_total"`
	VerificationsTotal        uint64                          `json:"verifications_total"`
	VerificationSuccessTotal  uint64                          `json:"verification_success_total"`
	VerificationFailureTotal  uint64                          `json:"verification_failure_total"`
	ApprovalsPendingTotal     uint64                          `json:"approvals_pending_total"`
	CompensationsTotal        uint64                          `json:"compensations_total"`
	EvidencePackagesTotal     uint64                          `json:"evidence_packages_total"`
	MemoryWritebacksTotal     uint64                          `json:"memory_writebacks_total"`
	SkillInvocations          []WorkflowMetricSample          `json:"skill_invocations,omitempty"`
	SkillLowYield             []WorkflowMetricSample          `json:"skill_low_yield,omitempty"`
	SkillPolicyBlocks         []WorkflowMetricSample          `json:"skill_policy_blocks,omitempty"`
	SkillApprovalRequired     []WorkflowMetricSample          `json:"skill_approval_required,omitempty"`
	SkillDurations            []WorkflowMetricHistogramSample `json:"skill_durations,omitempty"`
	SkillScores               []WorkflowMetricHistogramSample `json:"skill_scores,omitempty"`
	AdaptiveStops             []WorkflowMetricSample          `json:"adaptive_stops,omitempty"`
	RAGSkillCalls             []WorkflowMetricSample          `json:"rag_skill_calls,omitempty"`
	ArtifactPersistFailures   []WorkflowMetricSample          `json:"artifact_persist_failures,omitempty"`
	ReplayValidationFailures  []WorkflowMetricSample          `json:"replay_validation_failures,omitempty"`
}

type WorkflowMetricSample struct {
	Labels map[string]string `json:"labels,omitempty"`
	Value  uint64            `json:"value"`
}

type WorkflowMetricHistogramSample struct {
	Labels map[string]string `json:"labels,omitempty"`
	Count  uint64            `json:"count"`
	Sum    float64           `json:"sum"`
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
	ID                       string    `json:"id"`
	SeriesKey                string    `json:"series_key"`
	Display                  string    `json:"display"`
	Category                 string    `json:"category,omitempty"`
	Scope                    string    `json:"scope"`
	Entity                   string    `json:"entity"`
	Trend                    string    `json:"trend"`
	Severity                 string    `json:"severity"`
	Confidence               float64   `json:"confidence"`
	DetectionMode            string    `json:"detection_mode,omitempty"`
	Latest                   float64   `json:"latest"`
	Baseline                 float64   `json:"baseline"`
	DeltaPercent             float64   `json:"delta_percent"`
	SlopePerMinute           float64   `json:"slope_per_minute,omitempty"`
	Acceleration             float64   `json:"acceleration,omitempty"`
	ThresholdBreaches        int       `json:"threshold_breaches,omitempty"`
	PersistencePoints        int       `json:"persistence_points,omitempty"`
	ThresholdValue           float64   `json:"threshold_value,omitempty"`
	Forecast                 string    `json:"forecast,omitempty"`
	ForecastValue            float64   `json:"forecast_value,omitempty"`
	Triggered                bool      `json:"triggered"`
	BehavioralClassification string    `json:"behavioral_classification,omitempty"`
	SuppressionFactor        float64   `json:"suppression_factor,omitempty"`
	BehavioralReason         string    `json:"behavioral_reason,omitempty"`
	Summary                  string    `json:"summary"`
	OperatorHint             string    `json:"operator_hint,omitempty"`
	LastObservedAt           time.Time `json:"last_observed_at"`
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
	ID                       string    `json:"id"`
	Name                     string    `json:"name"`
	Scope                    string    `json:"scope"`
	Entity                   string    `json:"entity"`
	Severity                 string    `json:"severity"`
	Weight                   float64   `json:"weight"`
	Current                  float64   `json:"current"`
	Baseline                 float64   `json:"baseline"`
	DeltaPercent             float64   `json:"delta_percent"`
	Acceleration             float64   `json:"acceleration"`
	OriginalScore            float64   `json:"original_score,omitempty"`
	Score                    float64   `json:"score"`
	Triggered                bool      `json:"triggered"`
	BehavioralClassification string    `json:"behavioral_classification,omitempty"`
	SuppressionFactor        float64   `json:"suppression_factor,omitempty"`
	SuppressionReason        string    `json:"suppression_reason,omitempty"`
	Evidence                 []string  `json:"evidence,omitempty"`
	LastObservedAt           time.Time `json:"last_observed_at"`
}

// BehavioralSignalAssessment explains how persistent workload memory changed the
// anomaly decision for one signal.
type BehavioralSignalAssessment struct {
	SignalID              string   `json:"signal_id"`
	EntityKey             string   `json:"entity_key"`
	Entity                string   `json:"entity"`
	Service               string   `json:"service,omitempty"`
	WorkloadClass         string   `json:"workload_class,omitempty"`
	WorkloadRole          string   `json:"workload_role,omitempty"`
	Current               float64  `json:"current"`
	ShortTermBaseline     float64  `json:"short_term_baseline,omitempty"`
	LongTermBaseline      float64  `json:"long_term_baseline,omitempty"`
	TemporalBaseline      float64  `json:"temporal_baseline,omitempty"`
	DeviationFromShortPct float64  `json:"deviation_from_short_pct,omitempty"`
	DeviationFromLongPct  float64  `json:"deviation_from_long_pct,omitempty"`
	RecurrenceCount       int      `json:"recurrence_count,omitempty"`
	TemporalBucket        string   `json:"temporal_bucket,omitempty"`
	CrossSignalSupport    []string `json:"cross_signal_support,omitempty"`
	Classification        string   `json:"classification"`
	Confidence            float64  `json:"confidence"`
	SuppressionFactor     float64  `json:"suppression_factor,omitempty"`
	Explanation           string   `json:"explanation"`
	MemorySamples         int      `json:"memory_samples,omitempty"`
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
	WorkflowID              string                       `json:"workflow_id"`
	PipelineVersion         string                       `json:"pipeline_version"`
	CollectorID             string                       `json:"collector_id,omitempty"`
	EvidencePackagePath     string                       `json:"evidence_package_path,omitempty"`
	EvidencePackageArtifact *DurableArtifactRef          `json:"evidence_package_artifact,omitempty"`
	Scope                   string                       `json:"scope"`
	Window                  string                       `json:"window"`
	GeneratedAt             time.Time                    `json:"generated_at"`
	RiskScore               float64                      `json:"risk_score"`
	RiskLevel               string                       `json:"risk_level"`
	Summary                 string                       `json:"summary"`
	ActionableWhy           string                       `json:"actionable_why"`
	Signals                 []JointRiskSignal            `json:"signals"`
	TrendAssessments        []TrendAssessment            `json:"trend_assessments,omitempty"`
	InvestigationEvents     []InvestigationEvent         `json:"investigation_events,omitempty"`
	Cooccurrences           []JointRiskCooccurrence      `json:"cooccurrences"`
	ScopeRisks              []ScopeRisk                  `json:"scope_risks"`
	Series                  []RiskSeries                 `json:"series"`
	Recommendations         []WorkflowRecommendation     `json:"recommendations"`
	Stages                  []PipelineStageResult        `json:"stages"`
	ToolCalls               []WorkflowToolCall           `json:"tool_calls"`
	Escalated               bool                         `json:"escalated"`
	EscalationReason        string                       `json:"escalation_reason,omitempty"`
	IncidentID              string                       `json:"incident_id,omitempty"`
	Limitations             []string                     `json:"limitations,omitempty"`
	Insights                WorkflowInsightsStatus       `json:"insights"`
	LLMAnalysis             *LLMAnalysisResult           `json:"llm_analysis,omitempty"`
	ContributingSignals     []ContributingSignal         `json:"contributing_signals,omitempty"`
	CorrelatedTimeWindow    *TimeWindow                  `json:"correlated_time_window,omitempty"`
	ImpactedScope           []string                     `json:"impacted_scope,omitempty"`
	Confidence              float64                      `json:"confidence"`
	RecommendedToolCalls    []string                     `json:"recommended_tool_calls,omitempty"`
	Severity                string                       `json:"severity,omitempty"`
	TelemetryQuality        PromptTelemetryQuality       `json:"telemetry_quality"`
	RetrievedDocs           []RetrievedDocumentEvidence  `json:"retrieved_docs,omitempty"`
	RetrievedCases          []RetrievedDocumentEvidence  `json:"retrieved_cases,omitempty"`
	RetrievedRunbooks       []RetrievedDocumentEvidence  `json:"retrieved_runbooks,omitempty"`
	SimilarIncidentPatterns []RetrievedDocumentEvidence  `json:"similar_incident_patterns,omitempty"`
	RetrievalSummary        string                       `json:"retrieval_summary,omitempty"`
	RetrievalEvidenceIDs    []string                     `json:"retrieval_evidence_ids,omitempty"`
	RetrievalConfidence     float64                      `json:"retrieval_confidence,omitempty"`
	RetrievalDecisions      []RetrievalDecision          `json:"retrieval_decisions,omitempty"`
	ChangeLinks             []RCAChangeLink              `json:"change_links,omitempty"`
	BehavioralAssessments   []BehavioralSignalAssessment `json:"behavioral_assessments,omitempty"`
	AdaptiveBaselines       []AdaptiveBaselineInsight    `json:"adaptive_baselines,omitempty"`
	IncidentMemoryMatches   []RetrievedDocumentEvidence  `json:"incident_memory_matches,omitempty"`
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
	SafetyTier        string   `json:"safety_tier,omitempty"`
	ProposalOnly      bool     `json:"proposal_only,omitempty"`
	ExecutionEligible bool     `json:"execution_eligible,omitempty"`
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

// RCAChangeLink captures one operational change correlated with the incident.
type RCAChangeLink struct {
	ChangeID          string    `json:"change_id"`
	Category          string    `json:"category"`
	Summary           string    `json:"summary"`
	Source            string    `json:"source,omitempty"`
	Entity            string    `json:"entity,omitempty"`
	Scope             string    `json:"scope,omitempty"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	TemporalAdjacency float64   `json:"temporal_adjacency,omitempty"`
	ScopeOverlap      float64   `json:"scope_overlap,omitempty"`
	CorrelationScore  float64   `json:"correlation_score,omitempty"`
	ImpactSummary     string    `json:"impact_summary,omitempty"`
	HypothesisHint    string    `json:"hypothesis_hint,omitempty"`
}

// EvidenceProvenance describes where the final diagnosis was grounded.
type EvidenceProvenance struct {
	EvidenceID string  `json:"evidence_id"`
	SourceType string  `json:"source_type"`
	Source     string  `json:"source,omitempty"`
	Summary    string  `json:"summary"`
	Weight     float64 `json:"weight,omitempty"`
}

// UncertaintyComponent explains where uncertainty remains in the final RCA.
type UncertaintyComponent struct {
	Dimension string  `json:"dimension"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
}

// AdaptiveBaselineInsight captures workload-aware and hardware-aware baseline interpretation.
type AdaptiveBaselineInsight struct {
	Dimension       string  `json:"dimension"`
	Metric          string  `json:"metric"`
	Entity          string  `json:"entity,omitempty"`
	WorkloadClass   string  `json:"workload_class,omitempty"`
	Job             string  `json:"job,omitempty"`
	PodUID          string  `json:"pod_uid,omitempty"`
	HardwareProfile string  `json:"hardware_profile,omitempty"`
	Current         float64 `json:"current,omitempty"`
	Baseline        float64 `json:"baseline,omitempty"`
	DeltaPercent    float64 `json:"delta_percent,omitempty"`
	Classification  string  `json:"classification,omitempty"`
	Explanation     string  `json:"explanation,omitempty"`
}

// AgentPlanStep captures one plan/act/verify step.
type AgentPlanStep struct {
	ID               string                    `json:"id"`
	Order            int                       `json:"order"`
	Iteration        int                       `json:"iteration"`
	Title            string                    `json:"title"`
	Objective        string                    `json:"objective"`
	Tool             ToolName                  `json:"tool"`
	Required         bool                      `json:"required"`
	ToolVersion      string                    `json:"tool_version,omitempty"`
	Query            map[string]string         `json:"query,omitempty"`
	Status           string                    `json:"status"`
	ResultSummary    string                    `json:"result_summary,omitempty"`
	Verified         bool                      `json:"verified"`
	VerificationNote string                    `json:"verification_note,omitempty"`
	EvidenceIDs      []string                  `json:"evidence_ids,omitempty"`
	SupersededBy     string                    `json:"superseded_by,omitempty"`
	OriginalAction   *ActionSpec               `json:"original_action,omitempty"`
	ActionContract   *ValidationActionContract `json:"action_contract,omitempty"`
	StartedAt        time.Time                 `json:"started_at,omitempty"`
	CompletedAt      time.Time                 `json:"completed_at,omitempty"`
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
	IncidentSummary          string                      `json:"incident_summary,omitempty"`
	Symptoms                 []string                    `json:"symptoms"`
	Timeline                 []RCATimelineEvent          `json:"timeline"`
	Scope                    []string                    `json:"scope"`
	MostLikelyCause          string                      `json:"most_likely_cause"`
	SuspectedRootCauseEntity string                      `json:"suspected_root_cause_entity,omitempty"`
	CausalPath               []string                    `json:"causal_path,omitempty"`
	ImpactScope              []string                    `json:"impact_scope,omitempty"`
	SupportingSignals        []string                    `json:"supporting_signals"`
	DisconfirmingSignals     []string                    `json:"disconfirming_signals"`
	Confidence               float64                     `json:"confidence"`
	Uncertainty              []UncertaintyComponent      `json:"uncertainty,omitempty"`
	EvidenceProvenance       []EvidenceProvenance        `json:"evidence_provenance,omitempty"`
	ChangeLinks              []RCAChangeLink             `json:"change_links,omitempty"`
	IncidentMemoryMatches    []RetrievedDocumentEvidence `json:"incident_memory_matches,omitempty"`
	UnresolvedGaps           []string                    `json:"unresolved_gaps,omitempty"`
	RecommendedNextSteps     []string                    `json:"recommended_next_steps,omitempty"`
	SafeRemediations         []string                    `json:"safe_remediations,omitempty"`
}

// RCAContext captures gathered workflow context.
type RCAContext struct {
	CollectorID             string                       `json:"collector_id,omitempty"`
	Window                  string                       `json:"window"`
	IncidentSummary         string                       `json:"incident_summary,omitempty"`
	ImpactedScope           []string                     `json:"impacted_scope,omitempty"`
	TopMetrics              map[string]float64           `json:"top_metrics"`
	TrendAssessments        []TrendAssessment            `json:"trend_assessments,omitempty"`
	InvestigationEvents     []InvestigationEvent         `json:"investigation_events,omitempty"`
	GPUSummary              map[string]float64           `json:"gpu_summary,omitempty"`
	TopProcesses            []string                     `json:"top_processes"`
	KernelSignals           []string                     `json:"kernel_signals"`
	TraceSummary            []string                     `json:"trace_summary,omitempty"`
	RecentDeploys           []string                     `json:"recent_deploys,omitempty"`
	SecurityFindings        []string                     `json:"security_findings,omitempty"`
	RecentChanges           []RCAChangeLink              `json:"recent_changes,omitempty"`
	TopologySummary         string                       `json:"topology_summary,omitempty"`
	BehavioralAssessments   []BehavioralSignalAssessment `json:"behavioral_assessments,omitempty"`
	AdaptiveBaselines       []AdaptiveBaselineInsight    `json:"adaptive_baselines,omitempty"`
	TelemetryQuality        PromptTelemetryQuality       `json:"telemetry_quality"`
	RetrievalSummary        string                       `json:"retrieval_summary,omitempty"`
	RetrievalDecisions      []RetrievalDecision          `json:"retrieval_decisions,omitempty"`
	SceneClassification     SceneClassification          `json:"scene_classification,omitempty"`
	CollectionPlanSummary   CollectionPlanSummary        `json:"collection_plan_summary,omitempty"`
	RecollectionRound       int                          `json:"recollection_round,omitempty"`
	RemainingBudget         InvestigationBudgetState     `json:"remaining_budget,omitempty"`
	EvidenceGoalsStillUnmet []string                     `json:"evidence_goals_still_unmet,omitempty"`
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
	WorkflowID                        string                       `json:"workflow_id"`
	PipelineVersion                   string                       `json:"pipeline_version"`
	EvidenceSchemaVersion             string                       `json:"evidence_schema_version,omitempty"`
	IncidentID                        string                       `json:"incident_id"`
	TraceID                           string                       `json:"trace_id,omitempty"`
	EvidencePackagePath               string                       `json:"evidence_package_path,omitempty"`
	MessageManifestPath               string                       `json:"message_manifest_path,omitempty"`
	ArtifactManifestPath              string                       `json:"artifact_manifest_path,omitempty"`
	EvidencePackageArtifact           *DurableArtifactRef          `json:"evidence_package_artifact,omitempty"`
	MessageHistoryArtifact            *DurableArtifactRef          `json:"message_history_artifact,omitempty"`
	ArtifactManifestArtifact          *DurableArtifactRef          `json:"artifact_manifest_artifact,omitempty"`
	Status                            string                       `json:"status"`
	CollectorID                       string                       `json:"collector_id,omitempty"`
	Trigger                           string                       `json:"trigger"`
	GeneratedAt                       time.Time                    `json:"generated_at"`
	SynthesizedIncident               IncidentSynthesis            `json:"synthesized_incident"`
	Context                           RCAContext                   `json:"context"`
	Anomalies                         []string                     `json:"anomalies"`
	Correlations                      []RCACorrelation             `json:"correlations"`
	Hypotheses                        []RCAHypothesis              `json:"hypotheses"`
	Evidence                          []RCAEvidence                `json:"evidence"`
	NormalizedEvidence                []evidencev1.Record          `json:"normalized_evidence,omitempty"`
	Recommendations                   []WorkflowRecommendation     `json:"recommendations"`
	ProposedActions                   []ProposedAction             `json:"proposed_actions,omitempty"`
	AgentLoop                         AgentLoopSummary             `json:"agent_loop"`
	SuspectedRootCauseEntity          string                       `json:"suspected_root_cause_entity,omitempty"`
	CausalPath                        []string                     `json:"causal_path,omitempty"`
	ImpactPath                        []string                     `json:"impact_path,omitempty"`
	ImpactScope                       []string                     `json:"impact_scope,omitempty"`
	Uncertainty                       []UncertaintyComponent       `json:"uncertainty,omitempty"`
	EvidenceProvenance                []EvidenceProvenance         `json:"evidence_provenance,omitempty"`
	ChangeLinks                       []RCAChangeLink              `json:"change_links,omitempty"`
	BehavioralAssessments             []BehavioralSignalAssessment `json:"behavioral_assessments,omitempty"`
	AdaptiveBaselines                 []AdaptiveBaselineInsight    `json:"adaptive_baselines,omitempty"`
	IncidentMemoryMatches             []RetrievedDocumentEvidence  `json:"incident_memory_matches,omitempty"`
	AdaptiveRuntime                   *AdaptiveRuntimeState        `json:"adaptive_runtime,omitempty"`
	AdaptiveDialogue                  []AdaptiveDialogueTurn       `json:"adaptive_dialogue,omitempty"`
	AdaptiveToolDecisions             []AdaptiveToolDecision       `json:"adaptive_tool_decisions,omitempty"`
	AdaptiveArtifacts                 []AdaptiveArtifact           `json:"adaptive_artifacts,omitempty"`
	StructuredReport                  RCAStructuredReport          `json:"structured_report"`
	AnalysisHandoff                   AnalysisHandoff              `json:"analysis_handoff"`
	Validation                        ValidationActionReport       `json:"validation"`
	MessageHistory                    []AgentMessageRef            `json:"message_history,omitempty"`
	LatestAnalysisHandoffMessage      *AgentMessageRef             `json:"latest_analysis_handoff_message,omitempty"`
	LatestValidationRequestMessage    *AgentMessageRef             `json:"latest_validation_request_message,omitempty"`
	LatestValidationResultMessage     *AgentMessageRef             `json:"latest_validation_result_message,omitempty"`
	LatestActionDecisionMessage       *AgentMessageRef             `json:"latest_action_decision_message,omitempty"`
	LatestPostActionValidationMessage *AgentMessageRef             `json:"latest_post_action_validation_message,omitempty"`
	LatestCompensationMessage         *AgentMessageRef             `json:"latest_compensation_message,omitempty"`
	Stages                            []PipelineStageResult        `json:"stages"`
	ToolCalls                         []WorkflowToolCall           `json:"tool_calls"`
	Reproducibility                   map[string]string            `json:"reproducibility"`
	Artifacts                         WorkflowArtifactChain        `json:"artifacts"`
	UnresolvedGaps                    []string                     `json:"unresolved_gaps,omitempty"`
	Limitations                       []string                     `json:"limitations,omitempty"`
	Insights                          WorkflowInsightsStatus       `json:"insights"`
	LLMAnalysis                       *LLMAnalysisResult           `json:"llm_analysis,omitempty"`
	TelemetryQuality                  PromptTelemetryQuality       `json:"telemetry_quality"`
	RetrievedDocs                     []RetrievedDocumentEvidence  `json:"retrieved_docs,omitempty"`
	RetrievedCases                    []RetrievedDocumentEvidence  `json:"retrieved_cases,omitempty"`
	RetrievedRunbooks                 []RetrievedDocumentEvidence  `json:"retrieved_runbooks,omitempty"`
	SimilarIncidentPatterns           []RetrievedDocumentEvidence  `json:"similar_incident_patterns,omitempty"`
	RetrievalSummary                  string                       `json:"retrieval_summary,omitempty"`
	RetrievalEvidenceIDs              []string                     `json:"retrieval_evidence_ids,omitempty"`
	RetrievalConfidence               float64                      `json:"retrieval_confidence,omitempty"`
	SceneClassification               SceneClassification          `json:"scene_classification,omitempty"`
	CollectionPlan                    CollectionPlan               `json:"collection_plan,omitempty"`
	RecollectionResults               []RecollectionResult         `json:"recollection_results,omitempty"`
	EvidenceGapState                  EvidenceGapState             `json:"evidence_gap_state,omitempty"`
	EscalationDecision                EscalationDecision           `json:"escalation_decision,omitempty"`
}

// AgentIncidentReport is a persisted incident investigation record.
type AgentIncidentReport struct {
	IncidentID                        string                      `json:"incident_id"`
	WorkflowID                        string                      `json:"workflow_id"`
	TraceID                           string                      `json:"trace_id,omitempty"`
	EvidenceSchemaVersion             string                      `json:"evidence_schema_version,omitempty"`
	EvidencePackagePath               string                      `json:"evidence_package_path,omitempty"`
	MessageManifestPath               string                      `json:"message_manifest_path,omitempty"`
	ArtifactManifestPath              string                      `json:"artifact_manifest_path,omitempty"`
	EvidencePackageArtifact           *DurableArtifactRef         `json:"evidence_package_artifact,omitempty"`
	MessageHistoryArtifact            *DurableArtifactRef         `json:"message_history_artifact,omitempty"`
	ArtifactManifestArtifact          *DurableArtifactRef         `json:"artifact_manifest_artifact,omitempty"`
	Status                            string                      `json:"status"`
	Source                            string                      `json:"source"`
	CollectorID                       string                      `json:"collector_id,omitempty"`
	OpenedAt                          time.Time                   `json:"opened_at"`
	ClosedAt                          *time.Time                  `json:"closed_at,omitempty"`
	RiskLevel                         string                      `json:"risk_level"`
	RiskScore                         float64                     `json:"risk_score"`
	Summary                           string                      `json:"summary"`
	MostLikelyCause                   string                      `json:"most_likely_cause"`
	Confidence                        float64                     `json:"confidence"`
	SynthesizedIncident               IncidentSynthesis           `json:"synthesized_incident"`
	Symptoms                          []string                    `json:"symptoms"`
	Timeline                          []RCATimelineEvent          `json:"timeline"`
	Evidence                          []RCAEvidence               `json:"evidence"`
	NormalizedEvidence                []evidencev1.Record         `json:"normalized_evidence,omitempty"`
	Hypotheses                        []RCAHypothesis             `json:"hypotheses"`
	Recommendations                   []WorkflowRecommendation    `json:"recommendations"`
	ProposedActions                   []ProposedAction            `json:"proposed_actions,omitempty"`
	AgentLoop                         AgentLoopSummary            `json:"agent_loop"`
	AnalysisHandoff                   AnalysisHandoff             `json:"analysis_handoff"`
	Validation                        ValidationActionReport      `json:"validation"`
	MessageHistory                    []AgentMessageRef           `json:"message_history,omitempty"`
	LatestAnalysisHandoffMessage      *AgentMessageRef            `json:"latest_analysis_handoff_message,omitempty"`
	LatestValidationRequestMessage    *AgentMessageRef            `json:"latest_validation_request_message,omitempty"`
	LatestValidationResultMessage     *AgentMessageRef            `json:"latest_validation_result_message,omitempty"`
	LatestActionDecisionMessage       *AgentMessageRef            `json:"latest_action_decision_message,omitempty"`
	LatestPostActionValidationMessage *AgentMessageRef            `json:"latest_post_action_validation_message,omitempty"`
	LatestCompensationMessage         *AgentMessageRef            `json:"latest_compensation_message,omitempty"`
	SuspectedRootCauseEntity          string                      `json:"suspected_root_cause_entity,omitempty"`
	CausalPath                        []string                    `json:"causal_path,omitempty"`
	ImpactScope                       []string                    `json:"impact_scope,omitempty"`
	ChangeLinks                       []RCAChangeLink             `json:"change_links,omitempty"`
	IncidentMemoryMatches             []RetrievedDocumentEvidence `json:"incident_memory_matches,omitempty"`
	Artifacts                         WorkflowArtifactChain       `json:"artifacts"`
	UnresolvedGaps                    []string                    `json:"unresolved_gaps,omitempty"`
	SceneClassification               SceneClassification         `json:"scene_classification,omitempty"`
	CollectionPlan                    CollectionPlan              `json:"collection_plan,omitempty"`
	RecollectionResults               []RecollectionResult        `json:"recollection_results,omitempty"`
	EvidenceGapState                  EvidenceGapState            `json:"evidence_gap_state,omitempty"`
	EscalationDecision                EscalationDecision          `json:"escalation_decision,omitempty"`
	LLMAnalysis                       *LLMAnalysisResult          `json:"llm_analysis,omitempty"`
	TelemetryQuality                  PromptTelemetryQuality      `json:"telemetry_quality"`
}

// JointRiskListResponse wraps the joint-risk list API payload.
type JointRiskListResponse struct {
	Reports   []JointRiskAssessment `json:"reports"`
	Count     int                   `json:"count"`
	Timestamp time.Time             `json:"timestamp"`
}

// RCAListResponse wraps the RCA workflow list API payload.
type RCAListResponse struct {
	Reports   []RCAWorkflowReport   `json:"reports"`
	Summaries []RCAWorkflowListItem `json:"summaries,omitempty"`
	Count     int                   `json:"count"`
	Timestamp time.Time             `json:"timestamp"`
}

type RCAWorkflowListItem struct {
	WorkflowID           string   `json:"workflow_id"`
	IncidentID           string   `json:"incident_id"`
	CollectorID          string   `json:"collector_id,omitempty"`
	Status               string   `json:"status"`
	Summary              string   `json:"summary"`
	MostLikelyCause      string   `json:"most_likely_cause,omitempty"`
	Confidence           float64  `json:"confidence"`
	ArtifactManifestPath string   `json:"artifact_manifest_path,omitempty"`
	ArtifactCount        int      `json:"artifact_count"`
	ArtifactKinds        []string `json:"artifact_kinds,omitempty"`
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
