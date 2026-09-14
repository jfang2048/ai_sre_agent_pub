package evaluation

import (
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// Evaluation v2 types. The v1 schema (system-performance/v1) is left untouched;
// v2 is additive and versioned as "system-performance/v2".

// TaskType classifies what one evaluation case actually asks the agent to do.
// Success is defined per task type — an artifact-complete run that does not
// accomplish the task must not score as successful.
type TaskType string

const (
	TaskTypeDetect   TaskType = "detect"
	TaskTypeDiagnose TaskType = "diagnose"
	TaskTypePlan     TaskType = "plan"
	TaskTypeMitigate TaskType = "mitigate"
	TaskTypeVerify   TaskType = "verify"
	TaskTypeNoop     TaskType = "noop"
)

// V2CaseFile is the on-disk schema for evaluation v2 system cases.
type V2CaseFile struct {
	SchemaVersion string   `json:"schema_version"`
	Cases         []V2Case `json:"cases"`
}

// V2Case defines one end-to-end system evaluation case with independent ground truth.
type V2Case struct {
	ID                 string             `json:"id"`
	Suites             []string           `json:"suites"`
	TaskType           TaskType           `json:"task_type"`
	IncidentType       string             `json:"incident_type"`
	FaultDomain        string             `json:"fault_domain,omitempty"`
	Difficulty         string             `json:"difficulty,omitempty"`
	RobustnessCategory string             `json:"robustness_category,omitempty"`
	Description        string             `json:"description"`
	IncidentCaseID     string             `json:"incident_case_id,omitempty"`
	IncidentCaseInline *eval.IncidentCase `json:"incident_case,omitempty"`
	GroundTruth        V2GroundTruth      `json:"ground_truth"`
}

// V2GroundTruth is the independent oracle for one case. It is defined in
// eval_data and never derived from agent or runtime output.
type V2GroundTruth struct {
	NoIncident             bool                   `json:"no_incident,omitempty"`
	ExpectedAnomalyPresent *bool                  `json:"expected_anomaly_present,omitempty"`
	RootCause              V2RootCauseGroundTruth `json:"root_cause,omitempty"`
	RequiredEvidence       []string               `json:"required_evidence,omitempty"`
	AcceptableRemediation  []string               `json:"acceptable_remediation,omitempty"`
	ForbiddenRemediation   []string               `json:"forbidden_remediation,omitempty"`
	// ExpectedToolsAny is a disjunction of tool-set conjunctions: any one of
	// the inner lists fully used counts as the expected tool path. Order is
	// intentionally not constrained.
	ExpectedToolsAny   [][]string           `json:"expected_tools_any,omitempty"`
	IrrelevantTools    []string             `json:"irrelevant_tools,omitempty"`
	ForbiddenTools     []string             `json:"forbidden_tools,omitempty"`
	SuccessConditions  []V2SuccessCondition `json:"success_conditions,omitempty"`
	ExpectedVerdictAny []string             `json:"expected_verdict_any,omitempty"`
	ExpectedFallback   string               `json:"expected_fallback_mode,omitempty"`
}

// V2RootCauseGroundTruth is the structured root-cause oracle: fault objects,
// not natural-language sentences.
type V2RootCauseGroundTruth struct {
	Entities           []string `json:"entities"`
	Aliases            []string `json:"aliases,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	FaultDomain        string   `json:"fault_domain,omitempty"`
	FaultDomainAliases []string `json:"fault_domain_aliases,omitempty"`
	PropagationChain   []string `json:"propagation_chain,omitempty"`
	AffectedServices   []string `json:"affected_services,omitempty"`
}

// V2SuccessCondition is one postcondition the evaluator checks independently
// of the agent's own "fixed" claim (mitigate/verify cases).
type V2SuccessCondition struct {
	Metric   string  `json:"metric"`
	Operator string  `json:"operator"` // "<", "<=", ">", ">=", "=="
	Value    float64 `json:"value"`
}

// V2SafetyMetrics counts and rates for the safety dimension. Pointer fields
// are null when the runtime did not exercise the relevant path (unmeasured),
// never silently 1.0.
type V2SafetyMetrics struct {
	ToolCallsTotal                int `json:"tool_calls_total"`
	RemediationCalls              int `json:"remediation_calls"`
	ExecutedRemediationCalls      int `json:"executed_remediation_calls"`
	UnsafeActionCount             int `json:"unsafe_action_count"`
	PolicyViolationCount          int `json:"policy_violation_count"`
	ApprovalBypassCount           int `json:"approval_bypass_count"`
	ForbiddenToolCallCount        int `json:"forbidden_tool_call_count"`
	DestructiveActionAttemptCount int `json:"destructive_action_attempt_count"`

	UnsafeActionRate             float64 `json:"unsafe_action_rate"`
	PolicyViolationRate          float64 `json:"policy_violation_rate"`
	ApprovalBypassRate           float64 `json:"approval_bypass_rate"`
	ForbiddenToolCallRate        float64 `json:"forbidden_tool_call_rate"`
	DestructiveActionAttemptRate float64 `json:"destructive_action_attempt_rate"`
	ApprovalEnforcementRate      float64 `json:"approval_enforcement_rate"`
	DryRunComplianceRate         float64 `json:"dry_run_compliance_rate"`

	RollbackReadinessRate *float64 `json:"rollback_readiness_rate,omitempty"`
	NoRegressionRate      *float64 `json:"no_regression_rate,omitempty"`
	RegressionCount       int      `json:"regression_count"`
}

// V2TrajectoryMetrics captures tool-use quality and investigation trajectory.
type V2TrajectoryMetrics struct {
	StepCount                int      `json:"step_count"`
	ToolCallCount            int      `json:"tool_call_count"`
	UsefulToolCallRate       float64  `json:"useful_tool_call_rate"`
	RedundantToolCallRate    float64  `json:"redundant_tool_call_rate"`
	FailedToolCallRate       float64  `json:"failed_tool_call_rate"`
	InvalidToolArgumentRate  float64  `json:"invalid_tool_argument_rate"`
	RepeatedToolCallRate     float64  `json:"repeated_tool_call_rate"`
	ToolSelectionPrecision   float64  `json:"tool_selection_precision"`
	ToolSelectionRecall      *float64 `json:"tool_selection_recall,omitempty"`
	ToolInformationGain      float64  `json:"tool_information_gain"`
	NoProgressStepRate       float64  `json:"no_progress_step_rate"`
	EvidenceGainPerStep      float64  `json:"evidence_gain_per_step"`
	PrematureTerminationRate float64  `json:"premature_termination_rate"`
	LoopRate                 float64  `json:"loop_rate"`
	InvalidActionRate        float64  `json:"invalid_action_rate"`
}

// V2EfficiencyMetrics captures latency and token consumption. TokenCost is
// deliberately renamed: the runtime persists token counts, not dollars.
type V2EfficiencyMetrics struct {
	EndToEndLatencyMS  float64  `json:"end_to_end_latency_ms"`
	TimeToDiagnosisMS  float64  `json:"time_to_diagnosis_ms"`
	TimeToMitigationMS *float64 `json:"time_to_mitigation_ms,omitempty"`
	ToolCallCount      float64  `json:"tool_call_count"`
	TotalTokens        float64  `json:"total_tokens"`
	// InputTokens/OutputTokens are null until the runtime persists the split;
	// EstimatedCostUSD is null unless model pricing is configured.
	InputTokens      *float64 `json:"input_tokens,omitempty"`
	OutputTokens     *float64 `json:"output_tokens,omitempty"`
	EstimatedCostUSD *float64 `json:"estimated_cost_usd,omitempty"`
}

// V2CollaborationMetrics reuses the v1 message-protocol extractors.
type V2CollaborationMetrics struct {
	HandoffSchemaValidRate                float64 `json:"handoff_schema_valid_rate"`
	HandoffParseSuccessRate               float64 `json:"handoff_parse_success_rate"`
	HandoffRequiredFieldsCoverage         float64 `json:"handoff_required_fields_coverage"`
	HandoffTargetExtractionScore          float64 `json:"handoff_target_extraction_score"`
	CrossAgentInformationRetentionScore   float64 `json:"cross_agent_information_retention_score"`
	MessageHistoryIntegrityScore          float64 `json:"message_history_integrity_score"`
	AgentAgreementScore                   float64 `json:"agent_agreement_score"`
	ParentChildMessageLinkageCompleteness float64 `json:"parent_child_message_linkage_completeness"`
	ArtifactCoverage                      float64 `json:"artifact_coverage"`
}

// V2TrialMetrics is one trial's raw measurement for one case.
type V2TrialMetrics struct {
	TaskSuccess        bool `json:"task_success"`
	ClaimedIncident    bool `json:"claimed_incident"`
	UnnecessaryActions int  `json:"unnecessary_actions"`
	FalsePositive      bool `json:"false_positive"`

	RootCauseEntityPrecision float64  `json:"root_cause_entity_precision"`
	RootCauseEntityRecall    float64  `json:"root_cause_entity_recall"`
	RootCauseEntityF1        float64  `json:"root_cause_entity_f1"`
	RootCauseEntityRecallAt1 float64  `json:"root_cause_entity_recall_at_1"`
	RootCauseEntityRecallAt3 float64  `json:"root_cause_entity_recall_at_3"`
	RootCauseEntityRecallAt5 float64  `json:"root_cause_entity_recall_at_5"`
	RootCauseReasoningScore  float64  `json:"root_cause_reasoning_score"`
	FaultDomainAccuracy      *float64 `json:"fault_domain_accuracy,omitempty"`
	PropagationChainScore    *float64 `json:"propagation_chain_score,omitempty"`
	FaultLocalizationScore   float64  `json:"fault_localization_score"`

	RemediationPlanCorrectness *float64 `json:"remediation_plan_correctness,omitempty"`
	VerificationCorrectness    *float64 `json:"verification_correctness,omitempty"`
	RecoverySuccess            *float64 `json:"recovery_success,omitempty"`

	Safety        V2SafetyMetrics        `json:"safety"`
	Trajectory    V2TrajectoryMetrics    `json:"trajectory"`
	Efficiency    V2EfficiencyMetrics    `json:"efficiency"`
	Collaboration V2CollaborationMetrics `json:"collaboration"`
	// Measured distinguishes a legitimate zero from a metric that did not
	// apply to this trial. Keys are stable metric/dimension identifiers.
	Measured map[string]bool `json:"measured,omitempty"`

	FailureModes map[string]int `json:"failure_modes,omitempty"`
	Failures     []string       `json:"failures,omitempty"`
}

// V2CaseResult aggregates all trials of one case plus per-case statistics.
type V2CaseResult struct {
	ID                 string   `json:"id"`
	TaskType           TaskType `json:"task_type"`
	IncidentType       string   `json:"incident_type"`
	FaultDomain        string   `json:"fault_domain,omitempty"`
	Difficulty         string   `json:"difficulty,omitempty"`
	RobustnessCategory string   `json:"robustness_category,omitempty"`
	Description        string   `json:"description"`
	Trials             int      `json:"trials"`

	TaskSuccessRate        float64 `json:"task_success_rate"`
	TaskSuccessTrials      int     `json:"task_success_trials"`
	FalsePositiveTrials    int     `json:"false_positive_trials"`
	TaskSuccessCI95Low     float64 `json:"task_success_ci95_low,omitempty"`
	TaskSuccessCI95High    float64 `json:"task_success_ci95_high,omitempty"`
	TaskSuccessCIAvailable bool    `json:"task_success_ci_available"`
	TaskSuccessCIMethod    string  `json:"task_success_ci_method,omitempty"`
	PassAt1                float64 `json:"pass_at_1"`
	PassAtK                float64 `json:"pass_at_k"`
	PassAtKAvailable       bool    `json:"pass_at_k_available"`
	Flaky                  bool    `json:"flaky"`
	TrialsIndependent      bool    `json:"trials_independent"`

	ReplayStability      float64  `json:"replay_stability"`
	RootCauseConsistency float64  `json:"root_cause_consistency"`
	VerdictConsistency   *float64 `json:"verdict_consistency,omitempty"`

	FailureModeTrialCount  int `json:"failure_mode_trial_count"`
	FatalFailureTrialCount int `json:"fatal_failure_trial_count"`

	Aggregate    V2CaseAggregate `json:"aggregate"`
	FailureModes map[string]int  `json:"failure_modes,omitempty"`
	Failures     []string        `json:"failures,omitempty"`
	Passed       bool            `json:"passed"`

	// Trials only persisted in the benchmark JSON when IncludeTrials is set,
	// to keep fast-scope reports small.
	TrialsDetail []V2TrialMetrics `json:"trials_detail,omitempty"`
}

// V2CaseAggregate holds the mean of each measured metric across trials.
type V2CaseAggregate struct {
	// MetricSupport counts contributing trials for each metric/dimension.
	MetricSupport map[string]int `json:"metric_support,omitempty"`

	RootCauseEntityPrecision float64  `json:"root_cause_entity_precision"`
	RootCauseEntityRecall    float64  `json:"root_cause_entity_recall"`
	RootCauseEntityF1        float64  `json:"root_cause_entity_f1"`
	RootCauseEntityRecallAt1 float64  `json:"root_cause_entity_recall_at_1"`
	RootCauseEntityRecallAt3 float64  `json:"root_cause_entity_recall_at_3"`
	RootCauseEntityRecallAt5 float64  `json:"root_cause_entity_recall_at_5"`
	RootCauseReasoningScore  float64  `json:"root_cause_reasoning_score"`
	FaultDomainAccuracy      *float64 `json:"fault_domain_accuracy,omitempty"`
	PropagationChainScore    *float64 `json:"propagation_chain_score,omitempty"`
	FaultLocalizationScore   float64  `json:"fault_localization_score"`

	RemediationPlanCorrectness *float64 `json:"remediation_plan_correctness,omitempty"`
	VerificationCorrectness    *float64 `json:"verification_correctness,omitempty"`
	RecoverySuccess            *float64 `json:"recovery_success,omitempty"`

	UnsafeActionRate             float64  `json:"unsafe_action_rate"`
	PolicyViolationRate          float64  `json:"policy_violation_rate"`
	ApprovalBypassRate           float64  `json:"approval_bypass_rate"`
	ForbiddenToolCallRate        float64  `json:"forbidden_tool_call_rate"`
	DestructiveActionAttemptRate float64  `json:"destructive_action_attempt_rate"`
	ApprovalEnforcementRate      float64  `json:"approval_enforcement_rate"`
	DryRunComplianceRate         float64  `json:"dry_run_compliance_rate"`
	RollbackReadinessRate        *float64 `json:"rollback_readiness_rate,omitempty"`
	NoRegressionRate             *float64 `json:"no_regression_rate,omitempty"`
	RegressionRate               float64  `json:"regression_rate"`

	UsefulToolCallRate       float64  `json:"useful_tool_call_rate"`
	RedundantToolCallRate    float64  `json:"redundant_tool_call_rate"`
	FailedToolCallRate       float64  `json:"failed_tool_call_rate"`
	InvalidToolArgumentRate  float64  `json:"invalid_tool_argument_rate"`
	RepeatedToolCallRate     float64  `json:"repeated_tool_call_rate"`
	ToolSelectionPrecision   float64  `json:"tool_selection_precision"`
	ToolSelectionRecall      *float64 `json:"tool_selection_recall,omitempty"`
	ToolInformationGain      float64  `json:"tool_information_gain"`
	NoProgressStepRate       float64  `json:"no_progress_step_rate"`
	EvidenceGainPerStep      float64  `json:"evidence_gain_per_step"`
	PrematureTerminationRate float64  `json:"premature_termination_rate"`
	LoopRate                 float64  `json:"loop_rate"`
	InvalidActionRate        float64  `json:"invalid_action_rate"`

	EndToEndLatencyMS  float64  `json:"end_to_end_latency_ms"`
	TimeToDiagnosisMS  float64  `json:"time_to_diagnosis_ms"`
	TimeToMitigationMS *float64 `json:"time_to_mitigation_ms,omitempty"`
	ToolCallCount      float64  `json:"tool_call_count"`
	TotalTokens        float64  `json:"total_tokens"`
	EstimatedCostUSD   *float64 `json:"estimated_cost_usd,omitempty"`

	HandoffSchemaValidRate                float64 `json:"handoff_schema_valid_rate"`
	HandoffParseSuccessRate               float64 `json:"handoff_parse_success_rate"`
	HandoffRequiredFieldsCoverage         float64 `json:"handoff_required_fields_coverage"`
	HandoffTargetExtractionScore          float64 `json:"handoff_target_extraction_score"`
	CrossAgentInformationRetentionScore   float64 `json:"cross_agent_information_retention_score"`
	MessageHistoryIntegrityScore          float64 `json:"message_history_integrity_score"`
	AgentAgreementScore                   float64 `json:"agent_agreement_score"`
	ParentChildMessageLinkageCompleteness float64 `json:"parent_child_message_linkage_completeness"`
	ArtifactCoverage                      float64 `json:"artifact_coverage"`

	FalsePositiveRate     float64 `json:"false_positive_rate"`
	UnnecessaryActionRate float64 `json:"unnecessary_action_rate"`
}

// V2Aggregate is the benchmark-wide aggregate of all case aggregates.
type V2Aggregate struct {
	CasesRun      int `json:"cases_run"`
	TrialsPerCase int `json:"trials_per_case"`
	TotalTrials   int `json:"total_trials"`
	// MetricSupport counts contributing cases. Aggregate values with zero
	// support are unmeasured and excluded from the scorecard.
	MetricSupport map[string]int `json:"metric_support,omitempty"`

	TaskSuccessRate      float64 `json:"task_success_rate"`       // micro
	TaskSuccessRateMacro float64 `json:"task_success_rate_macro"` // macro over cases
	TaskSuccessCI95Low   float64 `json:"task_success_ci95_low"`
	TaskSuccessCI95High  float64 `json:"task_success_ci95_high"`
	PassAt1              float64 `json:"pass_at_1"`
	PassAtK              float64 `json:"pass_at_k"`
	PassAtKAvailable     bool    `json:"pass_at_k_available"`

	FalsePositiveRate     float64 `json:"false_positive_rate"`
	Specificity           float64 `json:"specificity"`
	NoOpAccuracy          float64 `json:"no_op_accuracy"`
	UnnecessaryActionRate float64 `json:"unnecessary_action_rate"`

	RootCauseEntityPrecision float64  `json:"root_cause_entity_precision"`
	RootCauseEntityRecall    float64  `json:"root_cause_entity_recall"`
	RootCauseEntityF1        float64  `json:"root_cause_entity_f1"`
	RootCauseEntityRecallAt1 float64  `json:"root_cause_entity_recall_at_1"`
	RootCauseEntityRecallAt3 float64  `json:"root_cause_entity_recall_at_3"`
	RootCauseEntityRecallAt5 float64  `json:"root_cause_entity_recall_at_5"`
	RootCauseReasoningScore  float64  `json:"root_cause_reasoning_score"`
	FaultDomainAccuracy      *float64 `json:"fault_domain_accuracy,omitempty"`
	PropagationChainScore    *float64 `json:"propagation_chain_score,omitempty"`
	FaultLocalizationScore   float64  `json:"fault_localization_score"`

	RemediationPlanCorrectness *float64 `json:"remediation_plan_correctness,omitempty"`
	VerificationCorrectness    *float64 `json:"verification_correctness,omitempty"`
	RecoverySuccessRate        *float64 `json:"recovery_success_rate,omitempty"`
	RegressionRate             *float64 `json:"regression_rate,omitempty"`
	RollbackSuccessRate        *float64 `json:"rollback_success_rate,omitempty"`
	NoRegressionRate           *float64 `json:"no_regression_rate,omitempty"`
	PostActionHealthScore      *float64 `json:"post_action_health_score,omitempty"`

	UnsafeActionRate             float64 `json:"unsafe_action_rate"`
	PolicyViolationRate          float64 `json:"policy_violation_rate"`
	ApprovalBypassRate           float64 `json:"approval_bypass_rate"`
	ForbiddenToolCallRate        float64 `json:"forbidden_tool_call_rate"`
	DestructiveActionAttemptRate float64 `json:"destructive_action_attempt_rate"`
	ApprovalEnforcementRate      float64 `json:"approval_enforcement_rate"`
	DryRunComplianceRate         float64 `json:"dry_run_compliance_rate"`

	UsefulToolCallRate       float64  `json:"useful_tool_call_rate"`
	RedundantToolCallRate    float64  `json:"redundant_tool_call_rate"`
	FailedToolCallRate       float64  `json:"failed_tool_call_rate"`
	InvalidToolArgumentRate  float64  `json:"invalid_tool_argument_rate"`
	RepeatedToolCallRate     float64  `json:"repeated_tool_call_rate"`
	ToolSelectionPrecision   float64  `json:"tool_selection_precision"`
	ToolSelectionRecall      *float64 `json:"tool_selection_recall,omitempty"`
	ToolInformationGain      float64  `json:"tool_information_gain"`
	NoProgressStepRate       float64  `json:"no_progress_step_rate"`
	EvidenceGainPerStep      float64  `json:"evidence_gain_per_step"`
	PrematureTerminationRate float64  `json:"premature_termination_rate"`
	LoopRate                 float64  `json:"loop_rate"`
	InvalidActionRate        float64  `json:"invalid_action_rate"`

	LatencyP50MS          float64  `json:"latency_p50_ms"`
	LatencyP95MS          float64  `json:"latency_p95_ms"`
	LatencyP99MS          float64  `json:"latency_p99_ms"`
	TimeToDiagnosisP50MS  float64  `json:"time_to_diagnosis_p50_ms"`
	TimeToDiagnosisP95MS  float64  `json:"time_to_diagnosis_p95_ms"`
	TimeToMitigationP50MS *float64 `json:"time_to_mitigation_p50_ms,omitempty"`
	MeanToolCalls         float64  `json:"mean_tool_calls"`
	MeanTokens            float64  `json:"mean_tokens"`
	EstimatedCostUSD      *float64 `json:"estimated_cost_usd,omitempty"`

	ReplayStability      float64  `json:"replay_stability"`
	VerdictConsistency   *float64 `json:"verdict_consistency,omitempty"`
	RootCauseConsistency float64  `json:"root_cause_consistency"`
	FlakyCaseRate        float64  `json:"flaky_case_rate"`

	HandoffSchemaValidRate                float64 `json:"handoff_schema_valid_rate"`
	HandoffParseSuccessRate               float64 `json:"handoff_parse_success_rate"`
	HandoffRequiredFieldsCoverage         float64 `json:"handoff_required_fields_coverage"`
	HandoffTargetExtractionScore          float64 `json:"handoff_target_extraction_score"`
	CrossAgentInformationRetentionScore   float64 `json:"cross_agent_information_retention_score"`
	MessageHistoryIntegrityScore          float64 `json:"message_history_integrity_score"`
	AgentAgreementScore                   float64 `json:"agent_agreement_score"`
	ParentChildMessageLinkageCompleteness float64 `json:"parent_child_message_linkage_completeness"`
	ArtifactCoverage                      float64 `json:"artifact_coverage"`

	FailureModeRate  float64 `json:"failure_mode_rate"`
	FatalFailureRate float64 `json:"fatal_failure_rate"`
}

// V2ConfidenceIntervals holds case-level seeded bootstrap intervals for the
// headline metrics. Replayed trials are descriptive until the runtime accepts
// an independent deterministic trial seed.
type V2ConfidenceIntervals struct {
	TaskSuccessRate       V2CI `json:"task_success_rate"`
	PassAt1               V2CI `json:"pass_at_1"`
	RootCauseEntityF1     V2CI `json:"root_cause_entity_f1"`
	RootCauseEntityRecall V2CI `json:"root_cause_entity_recall"`
	RootCauseReasoning    V2CI `json:"root_cause_reasoning_score"`
	LatencyP95MS          V2CI `json:"latency_p95_ms"`
	MeanToolCalls         V2CI `json:"mean_tool_calls"`
	MeanTokens            V2CI `json:"mean_tokens"`
	FalsePositiveRate     V2CI `json:"false_positive_rate"`
	OverallScore          V2CI `json:"overall_score"`
}

// V2CI is one confidence interval.
type V2CI struct {
	Available  bool    `json:"available"`
	Low        float64 `json:"low"`
	Mean       float64 `json:"mean"`
	High       float64 `json:"high"`
	Method     string  `json:"method"`
	Support    int     `json:"support"`
	Limitation string  `json:"limitation,omitempty"`
}

// V2Scorecard is the weighted evaluation v2 scorecard. Dimensions absent from
// Measured are excluded from the weighted overall (weights are renormalized
// over measured dimensions); their numeric fields remain zero for JSON schema
// stability.
type V2Scorecard struct {
	TaskOutcome            float64        `json:"task_outcome"`
	DiagnosisQuality       float64        `json:"diagnosis_quality"`
	SafetyGovernance       float64        `json:"safety_governance"`
	TrajectoryTool         float64        `json:"trajectory_tool"`
	Efficiency             float64        `json:"efficiency"`
	Reliability            float64        `json:"reliability"`
	CollaborationArtifacts float64        `json:"collaboration_artifacts"`
	OverallScore           float64        `json:"overall_score"`
	OverallUncapped        float64        `json:"overall_uncapped,omitempty"`
	Measured               []string       `json:"measured"`
	Support                map[string]int `json:"support,omitempty"`
}

// V2GateCheck is one evaluated hard gate.
type V2GateCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// V2GateEvaluation is the result of the hard safety/outcome gates.
type V2GateEvaluation struct {
	Passed bool          `json:"passed"`
	Fired  []string      `json:"fired,omitempty"`
	Checks []V2GateCheck `json:"checks"`
}

// V2CategoryStats is one category grouping's macro/micro statistics.
type V2CategoryStats struct {
	Key     string             `json:"key"`
	Support int                `json:"support"`
	Macro   map[string]float64 `json:"macro"`
	Micro   map[string]float64 `json:"micro,omitempty"`
}

// V2ComparisonRow compares one metric between baseline and current.
type V2ComparisonRow struct {
	Metric           string   `json:"metric"`
	Baseline         float64  `json:"baseline"`
	Current          float64  `json:"current"`
	AbsoluteDelta    float64  `json:"absolute_delta"`
	RelativeDeltaPct *float64 `json:"relative_delta_pct,omitempty"`
	CI95Low          float64  `json:"ci95_low"`
	CI95High         float64  `json:"ci95_high"`
	Units            string   `json:"units,omitempty"`
}

// V2Comparison summarizes current-versus-baseline with regression policy verdict.
type V2Comparison struct {
	BaselinePath        string            `json:"baseline_path"`
	BaselineGeneratedAt time.Time         `json:"baseline_generated_at,omitempty"`
	Rows                []V2ComparisonRow `json:"rows"`
	Verdict             string            `json:"verdict"` // "pass", "warn", "fail"
	FiredRules          []string          `json:"fired_rules,omitempty"`
}

// V2Environment records everything needed to reproduce a run.
type V2Environment struct {
	GitCommit                 string          `json:"git_commit"`
	RuntimeMode               string          `json:"runtime_mode"`
	Variant                   string          `json:"variant,omitempty"`
	GoVersion                 string          `json:"go_version"`
	Scope                     string          `json:"scope"`
	TrialsPerCase             int             `json:"trials_per_case"`
	Seed                      int64           `json:"seed"`
	SeedPurpose               string          `json:"seed_purpose,omitempty"`
	RuntimeTrialSeedSupported bool            `json:"runtime_trial_seed_supported"`
	TrialSampling             string          `json:"trial_sampling"`
	StatisticalNote           string          `json:"statistical_note,omitempty"`
	WorktreeDirty             bool            `json:"worktree_dirty"`
	CasesConfigPath           string          `json:"cases_config_path"`
	ScoringConfigPath         string          `json:"scoring_config_path"`
	Timestamp                 time.Time       `json:"timestamp"`
	ModelPricing              *V2ModelPricing `json:"model_pricing,omitempty"`
}

// V2ModelPricing is optional model pricing used to derive EstimatedCostUSD.
type V2ModelPricing struct {
	Model               string  `json:"model"`
	InputUSDPerMillion  float64 `json:"input_usd_per_million_tokens"`
	OutputUSDPerMillion float64 `json:"output_usd_per_million_tokens"`
	Provider            string  `json:"provider,omitempty"`
	Note                string  `json:"note,omitempty"`
}

// SystemPerformanceReportV2 is the persisted "system-performance/v2" report.
type SystemPerformanceReportV2 struct {
	SchemaVersion       string                `json:"schema_version"`
	GeneratedAt         time.Time             `json:"generated_at"`
	Environment         V2Environment         `json:"environment"`
	Config              V2ScoringConfig       `json:"config"`
	Aggregate           V2Aggregate           `json:"aggregate"`
	ConfidenceIntervals V2ConfidenceIntervals `json:"confidence_intervals"`
	Categories          []V2CategoryStats     `json:"categories"`
	Cases               []V2CaseResult        `json:"cases"`
	FailureModes        map[string]int        `json:"failure_modes"`
	Comparison          *V2Comparison         `json:"comparison,omitempty"`
	Scorecard           V2Scorecard           `json:"scorecard"`
	Gates               V2GateEvaluation      `json:"gates"`
	Verdict             string                `json:"verdict"`
	ReportDir           string                `json:"report_dir,omitempty"`
}

// V2ScoringConfig is the weighted scorecard configuration (eval_data/scoring_v2.json).
type V2ScoringConfig struct {
	SchemaVersion         string             `json:"schema_version"`
	Weights               V2Weights          `json:"weights"`
	OutcomeCap            V2OutcomeCap       `json:"outcome_cap"`
	HardGates             []string           `json:"hard_gates"`
	TaskSuccess           V2TaskSuccessRules `json:"task_success"`
	Efficiency            V2EfficiencyConfig `json:"efficiency"`
	PassingThreshold      float64            `json:"passing_threshold"`
	CasePassRateThreshold float64            `json:"case_pass_rate_threshold"`
	ModelPricing          *V2ModelPricing    `json:"model_pricing,omitempty"`
}

// V2Weights maps scorecard dimensions to weights.
type V2Weights struct {
	TaskOutcome            float64 `json:"task_outcome"`
	DiagnosisQuality       float64 `json:"diagnosis_quality"`
	SafetyGovernance       float64 `json:"safety_governance"`
	TrajectoryTool         float64 `json:"trajectory_tool"`
	Efficiency             float64 `json:"efficiency"`
	Reliability            float64 `json:"reliability"`
	CollaborationArtifacts float64 `json:"collaboration_artifacts"`
}

// V2OutcomeCap prevents artifact-complete but task-failed runs from scoring
// "excellent": a case whose task failed is capped below the passing threshold,
// and the benchmark overall is capped when aggregate task success is too low.
type V2OutcomeCap struct {
	Enabled              bool    `json:"enabled"`
	AggregateSuccessMin  float64 `json:"aggregate_success_min"`
	AggregateCappedScore float64 `json:"aggregate_capped_score"`
}

// V2TaskSuccessRules holds per-task-type success thresholds.
type V2TaskSuccessRules struct {
	DiagnoseEntityRecallMin        float64 `json:"diagnose_entity_recall_min"`
	DiagnoseEntityPrecisionMin     float64 `json:"diagnose_entity_precision_min"`
	DiagnoseReasoningMin           float64 `json:"diagnose_reasoning_min"`
	PlanRecommendationCoverageMin  float64 `json:"plan_recommendation_coverage_min"`
	NoopFalsePositiveConfidenceMin float64 `json:"noop_false_positive_confidence_min"`
	PassAtKAttempts                int     `json:"pass_at_k_attempts"`
}

// V2EfficiencyConfig holds efficiency thresholds used by scoring functions.
type V2EfficiencyConfig struct {
	LatencyP95ThresholdMS         float64 `json:"latency_p95_threshold_ms"`
	TimeToDiagnosisP95ThresholdMS float64 `json:"time_to_diagnosis_p95_threshold_ms"`
	ToolCallsThreshold            float64 `json:"tool_calls_threshold"`
	TokensThreshold               float64 `json:"tokens_threshold"`
}

// V2RegressionPolicyFile is the on-disk regression policy (eval_data/regression_policy.json).
type V2RegressionPolicyFile struct {
	SchemaVersion string             `json:"schema_version"`
	Rules         []V2RegressionRule `json:"rules"`
}

// V2RegressionRule is one baseline-comparison rule.
type V2RegressionRule struct {
	Metric    string  `json:"metric"`
	Direction string  `json:"direction"` // "higher_is_better" | "lower_is_better"
	Type      string  `json:"type"`      // "absolute_delta" | "relative_delta" | "absolute_value"
	Threshold float64 `json:"threshold"`
	Severity  string  `json:"severity"` // "fail" | "warn"
}
