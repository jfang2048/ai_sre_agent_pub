package evaluation

import (
	"time"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// SystemPerformanceOptions controls one end-to-end system-performance run.
type SystemPerformanceOptions struct {
	Scope                       eval.Scope
	RepoRoot                    string
	Variant                     string
	ComparePath                 string
	ReplayRuns                  int
	CaseIDs                     []string
	AgentMessageProtocolEnabled *bool
	ValidationAgentEnabled      *bool
}

// SystemPerformanceCaseFile is the on-disk schema for system-level evaluation inputs.
type SystemPerformanceCaseFile struct {
	SchemaVersion string                  `json:"schema_version"`
	Cases         []SystemPerformanceCase `json:"cases"`
}

// SystemPerformanceCase defines one end-to-end runtime contract for the full SRE system.
type SystemPerformanceCase struct {
	ID                                 string                                 `json:"id"`
	Suites                             []string                               `json:"suites"`
	Description                        string                                 `json:"description"`
	IncidentType                       string                                 `json:"incident_type"`
	IncidentCaseID                     string                                 `json:"incident_case_id"`
	SeededContext                      string                                 `json:"seeded_context,omitempty"`
	ExpectedRootCauseAny               []string                               `json:"expected_root_cause_any,omitempty"`
	ExpectedValidatedRecommendationAny []string                               `json:"expected_validated_recommendation_substrings,omitempty"`
	ExpectedGovernance                 SystemPerformanceGovernanceExpectation `json:"expected_governance_behavior,omitempty"`
	ExpectedPostAction                 SystemPerformancePostActionExpectation `json:"expected_post_action_outcome,omitempty"`
	ExpectedArtifacts                  SystemPerformanceArtifactExpectation   `json:"expected_artifact_coverage,omitempty"`
}

// SystemPerformanceGovernanceExpectation declares expected safety posture for one system case.
type SystemPerformanceGovernanceExpectation struct {
	RequireMessageProtocol     bool   `json:"require_message_protocol,omitempty"`
	RequireValidationAgent     bool   `json:"require_validation_agent,omitempty"`
	ExpectedValidationMode     string `json:"expected_validation_mode,omitempty"`
	ExpectedExecutionCategory  string `json:"expected_execution_category,omitempty"`
	RequireApprovalEnforcement bool   `json:"require_approval_enforcement,omitempty"`
	ExpectDryRun               bool   `json:"expect_dry_run,omitempty"`
}

// SystemPerformancePostActionExpectation declares the expected post-validation outcome.
type SystemPerformancePostActionExpectation struct {
	ExpectedVerdictAny []string `json:"expected_verdict_any,omitempty"`
	ExpectedFallback   string   `json:"expected_fallback_mode,omitempty"`
}

// SystemPerformanceArtifactExpectation declares which durable artifacts must exist.
type SystemPerformanceArtifactExpectation struct {
	RequireAnalysisHandoff        bool     `json:"require_analysis_handoff,omitempty"`
	RequireValidationReport       bool     `json:"require_validation_report,omitempty"`
	RequireActionPlan             bool     `json:"require_action_plan,omitempty"`
	RequirePostActionValidation   bool     `json:"require_post_action_validation,omitempty"`
	RequireEvidencePackage        bool     `json:"require_evidence_package,omitempty"`
	RequireMemoryWriteback        bool     `json:"require_memory_writeback,omitempty"`
	RequireRollbackOrCompensation bool     `json:"require_rollback_or_compensation,omitempty"`
	RequiredMessageTypes          []string `json:"required_message_types,omitempty"`
}

// SystemPerformanceArtifactRefs exposes the durable artifacts behind one case result.
type SystemPerformanceArtifactRefs struct {
	WorkflowID                        string                      `json:"workflow_id,omitempty"`
	EvidencePackagePath               string                      `json:"evidence_package_path,omitempty"`
	MessageManifestPath               string                      `json:"message_manifest_path,omitempty"`
	MessageHistory                    []agentcore.AgentMessageRef `json:"message_history,omitempty"`
	LatestAnalysisHandoffMessage      *agentcore.AgentMessageRef  `json:"latest_analysis_handoff_message,omitempty"`
	LatestValidationRequestMessage    *agentcore.AgentMessageRef  `json:"latest_validation_request_message,omitempty"`
	LatestValidationResultMessage     *agentcore.AgentMessageRef  `json:"latest_validation_result_message,omitempty"`
	LatestActionDecisionMessage       *agentcore.AgentMessageRef  `json:"latest_action_decision_message,omitempty"`
	LatestPostActionValidationMessage *agentcore.AgentMessageRef  `json:"latest_post_action_validation_message,omitempty"`
	LatestCompensationMessage         *agentcore.AgentMessageRef  `json:"latest_compensation_message,omitempty"`
}

// SystemPerformanceMetrics captures raw system-level measurements before dimension scoring.
type SystemPerformanceMetrics struct {
	RootCauseTop1Rate                   float64 `json:"root_cause_top1_rate"`
	RootCauseTopKRate                   float64 `json:"root_cause_topk_rate"`
	HypothesisSupportCorrectness        float64 `json:"hypothesis_support_correctness"`
	ContradictionDetectionRate          float64 `json:"contradiction_detection_rate"`
	RecommendationValidationCorrectness float64 `json:"recommendation_validation_correctness"`
	RemediationVerdictCorrectness       float64 `json:"remediation_verdict_correctness"`
	FinalIncidentOutcomeCorrectness     float64 `json:"final_incident_outcome_correctness"`

	AnalysisHandoffCoverage        float64 `json:"analysis_handoff_coverage"`
	ValidationReportCoverage       float64 `json:"validation_report_coverage"`
	ActionPlanCoverage             float64 `json:"action_plan_coverage"`
	PostActionValidationCoverage   float64 `json:"post_action_validation_coverage"`
	EvidencePackageCoverage        float64 `json:"evidence_package_coverage"`
	MemoryWritebackCoverage        float64 `json:"memory_writeback_coverage"`
	RollbackOrCompensationCoverage float64 `json:"rollback_or_compensation_coverage"`

	GovernanceCoverage               float64 `json:"governance_coverage"`
	ApprovalEnforcementRate          float64 `json:"approval_enforcement_rate"`
	DryRunCompliance                 float64 `json:"dry_run_compliance"`
	ExecutionCategoryEnforcementRate float64 `json:"execution_category_enforcement_rate"`
	IdempotencyPreservationRate      float64 `json:"idempotency_preservation_rate"`
	AuditCompleteness                float64 `json:"audit_completeness"`

	EndToEndLatencyMS             float64            `json:"end_to_end_latency_ms"`
	PerStageLatencyMS             map[string]float64 `json:"per_stage_latency_ms,omitempty"`
	AnalysisAgentLatencyMS        float64            `json:"analysis_agent_latency_ms"`
	ValidationAgentLatencyMS      float64            `json:"validation_agent_latency_ms"`
	HandoffSerializationLatencyMS float64            `json:"handoff_serialization_latency_ms"`
	HandoffParseLatencyMS         float64            `json:"handoff_parse_latency_ms"`
	ToolCallCount                 float64            `json:"tool_call_count"`
	ToolLatencyMS                 float64            `json:"tool_latency_ms"`
	TokenCost                     float64            `json:"token_cost"`
	CostPerSuccessfulCase         float64            `json:"cost_per_successful_case"`

	ReplayStabilityScore   float64 `json:"replay_stability_score"`
	RankingDrift           float64 `json:"ranking_drift"`
	ToolSelectionDrift     float64 `json:"tool_selection_drift"`
	VerdictConsistency     float64 `json:"verdict_consistency"`
	MessageReproducibility float64 `json:"message_reproducibility"`
	ValidationLoopDrift    float64 `json:"validation_loop_drift"`

	HandoffSchemaValidRate                float64 `json:"handoff_schema_valid_rate"`
	HandoffParseSuccessRate               float64 `json:"handoff_parse_success_rate"`
	HandoffRequiredFieldsCoverage         float64 `json:"handoff_required_fields_coverage"`
	HandoffTargetExtractionScore          float64 `json:"handoff_target_extraction_score"`
	CrossAgentInformationRetentionScore   float64 `json:"cross_agent_information_retention_score"`
	MessageHistoryIntegrityScore          float64 `json:"message_history_integrity_score"`
	AgentAgreementScore                   float64 `json:"agent_agreement_score"`
	ParentChildMessageLinkageCompleteness float64 `json:"parent_child_message_linkage_completeness"`
}

// SystemPerformanceScorecard maps raw metrics into the six required dimensions.
type SystemPerformanceScorecard struct {
	Correctness   float64 `json:"correctness"`
	Closure       float64 `json:"closure"`
	Governance    float64 `json:"governance"`
	Efficiency    float64 `json:"efficiency"`
	Stability     float64 `json:"stability"`
	Collaboration float64 `json:"collaboration"`
	OverallScore  float64 `json:"overall_score"`
}

// SystemPerformanceCaseResult reports one end-to-end system case.
type SystemPerformanceCaseResult struct {
	ID             string                        `json:"id"`
	IncidentType   string                        `json:"incident_type,omitempty"`
	IncidentCaseID string                        `json:"incident_case_id"`
	Description    string                        `json:"description"`
	Artifacts      SystemPerformanceArtifactRefs `json:"artifacts"`
	Metrics        SystemPerformanceMetrics      `json:"metrics"`
	Scorecard      SystemPerformanceScorecard    `json:"scorecard"`
	Failures       []string                      `json:"failures,omitempty"`
	Passed         bool                          `json:"passed"`
}

// SystemPerformanceComparison summarizes current-versus-baseline deltas.
type SystemPerformanceComparison struct {
	BaselinePath        string             `json:"baseline_path"`
	BaselineGeneratedAt time.Time          `json:"baseline_generated_at,omitempty"`
	ScoreDeltas         map[string]float64 `json:"score_deltas,omitempty"`
	LatencyDeltas       map[string]float64 `json:"latency_deltas,omitempty"`
	GovernanceDeltas    map[string]float64 `json:"governance_deltas,omitempty"`
	CollaborationDeltas map[string]float64 `json:"collaboration_deltas,omitempty"`
}

// SystemPerformanceReport is the persisted output of the system-level evaluator.
type SystemPerformanceReport struct {
	SchemaVersion string                        `json:"schema_version"`
	GeneratedAt   time.Time                     `json:"generated_at"`
	Scope         eval.Scope                    `json:"scope"`
	Variant       string                        `json:"variant,omitempty"`
	ReplayRuns    int                           `json:"replay_runs"`
	Cases         []SystemPerformanceCaseResult `json:"cases"`
	Metrics       SystemPerformanceMetrics      `json:"metrics"`
	Scorecard     SystemPerformanceScorecard    `json:"scorecard"`
	Comparison    *SystemPerformanceComparison  `json:"comparison,omitempty"`
	LatestPath    string                        `json:"latest_path,omitempty"`
	HistoryPath   string                        `json:"history_path,omitempty"`
	FailedCaseIDs []string                      `json:"failed_case_ids,omitempty"`
	Passed        bool                          `json:"passed"`
}
