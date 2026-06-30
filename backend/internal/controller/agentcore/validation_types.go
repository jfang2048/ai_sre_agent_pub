package agent

import "time"

const (
	// AnalysisHandoffSchemaVersion tags the durable analysis handoff payload.
	AnalysisHandoffSchemaVersion = "agent-handoff/v1"
	// ValidationActionReportSchemaVersion tags the durable validation report payload.
	ValidationActionReportSchemaVersion = "validation-report/v1"
)

type ValidationTargetType string

const (
	ValidationTargetHypothesis          ValidationTargetType = "hypothesis_validation"
	ValidationTargetRecommendation      ValidationTargetType = "recommendation_validation"
	ValidationTargetChangeCorrelation   ValidationTargetType = "change_correlation_validation"
	ValidationTargetRemediation         ValidationTargetType = "remediation_outcome_validation"
	ValidationTargetContradiction       ValidationTargetType = "contradiction_search"
	ValidationTargetSceneClassification ValidationTargetType = "scene_classification_validation"
)

type ValidationVerdict string

const (
	ValidationVerdictConfirmed            ValidationVerdict = "confirmed"
	ValidationVerdictContradicted         ValidationVerdict = "contradicted"
	ValidationVerdictPartiallySupported   ValidationVerdict = "partially_supported"
	ValidationVerdictInsufficientEvidence ValidationVerdict = "insufficient_evidence"
)

type AnalysisHypothesisHandoff struct {
	HypothesisID             string   `json:"hypothesis_id"`
	Rank                     int      `json:"rank"`
	Title                    string   `json:"title"`
	Summary                  string   `json:"summary,omitempty"`
	Confidence               float64  `json:"confidence"`
	SupportingEvidenceIDs    []string `json:"supporting_evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string `json:"contradicting_evidence_ids,omitempty"`
	ExpectedSignals          []string `json:"expected_signals,omitempty"`
	ToolFamilies             []string `json:"tool_families,omitempty"`
	Uncertainty              []string `json:"uncertainty,omitempty"`
}

type ValidationSecurityContext struct {
	Score             float64  `json:"score"`
	Categories        []string `json:"categories,omitempty"`
	FindingIDs        []string `json:"finding_ids,omitempty"`
	Findings          []string `json:"findings,omitempty"`
	CriticalFindings  int      `json:"critical_findings,omitempty"`
	HighFindings      int      `json:"high_findings,omitempty"`
	SuspiciousTargets []string `json:"suspicious_targets,omitempty"`
}

type ValidationActionCandidate struct {
	ID                  string                    `json:"id"`
	RecommendationID    string                    `json:"recommendation_id,omitempty"`
	Category            string                    `json:"category,omitempty"`
	ActionIntent        string                    `json:"action_intent,omitempty"`
	ActionCategory      string                    `json:"action_category,omitempty"`
	ActuatorSafetyTier  string                    `json:"actuator_safety_tier,omitempty"`
	Summary             string                    `json:"summary"`
	Scope               string                    `json:"scope,omitempty"`
	ExecutionLevel      string                    `json:"execution_level,omitempty"`
	Safe                bool                      `json:"safe"`
	DryRunDefault       bool                      `json:"dry_run_default"`
	RequiresApproval    bool                      `json:"requires_approval"`
	Reversible          bool                      `json:"reversible"`
	Preconditions       []string                  `json:"preconditions,omitempty"`
	RollbackHint        string                    `json:"rollback_hint,omitempty"`
	ExpectedImpact      string                    `json:"expected_impact,omitempty"`
	BlastRadiusEstimate int                       `json:"blast_radius_estimate,omitempty"`
	BlastRadiusScope    []string                  `json:"blast_radius_scope,omitempty"`
	RollbackContract    RollbackContract          `json:"rollback_contract,omitempty"`
	DiagnosticContract  *DiagnosticActionContract `json:"diagnostic_contract,omitempty"`
	ActionContract      *ValidationActionContract `json:"action_contract,omitempty"`
	Metadata            map[string]string         `json:"metadata,omitempty"`
	PrimaryTool         ToolName                  `json:"primary_tool,omitempty"`
	ValidationTarget    string                    `json:"validation_target,omitempty"`
	ValidationReason    string                    `json:"validation_reason,omitempty"`
}

type ValidationEvidenceSnapshot struct {
	Label                   string                    `json:"label"`
	CapturedAt              time.Time                 `json:"captured_at"`
	RiskScore               float64                   `json:"risk_score"`
	TriggeredSignals        []string                  `json:"triggered_signals,omitempty"`
	LogErrors               uint64                    `json:"log_errors"`
	LogWarnings             uint64                    `json:"log_warnings"`
	ServiceHealth           serviceHealthToolData     `json:"service_health"`
	MemoryPressure          memoryPressureToolData    `json:"memory_pressure"`
	Connectivity            connectivityCheckToolData `json:"connectivity"`
	Storage                 storageHealthToolData     `json:"storage"`
	Security                ValidationSecurityContext `json:"security"`
	ValidationConfidence    float64                   `json:"validation_confidence,omitempty"`
	RecommendationViability int                       `json:"recommendation_viability,omitempty"`
	EvidenceIDs             []string                  `json:"evidence_ids,omitempty"`
}

type ValidationFloatComparison struct {
	Available bool    `json:"available"`
	Before    float64 `json:"before,omitempty"`
	After     float64 `json:"after,omitempty"`
	Delta     float64 `json:"delta,omitempty"`
	Improved  bool    `json:"improved,omitempty"`
	Regressed bool    `json:"regressed,omitempty"`
	Note      string  `json:"note,omitempty"`
}

type ValidationIntComparison struct {
	Available bool   `json:"available"`
	Before    int64  `json:"before,omitempty"`
	After     int64  `json:"after,omitempty"`
	Delta     int64  `json:"delta,omitempty"`
	Improved  bool   `json:"improved,omitempty"`
	Regressed bool   `json:"regressed,omitempty"`
	Note      string `json:"note,omitempty"`
}

type ValidationBoolComparison struct {
	Available bool   `json:"available"`
	Before    bool   `json:"before,omitempty"`
	After     bool   `json:"after,omitempty"`
	Improved  bool   `json:"improved,omitempty"`
	Regressed bool   `json:"regressed,omitempty"`
	Note      string `json:"note,omitempty"`
}

type ValidationEffectComparison struct {
	Comparable              bool                      `json:"comparable"`
	Incomplete              bool                      `json:"incomplete,omitempty"`
	MissingData             []string                  `json:"missing_data,omitempty"`
	RiskScore               ValidationFloatComparison `json:"risk_score"`
	ServiceHealthy          ValidationBoolComparison  `json:"service_healthy"`
	ServiceLatencyMS        ValidationFloatComparison `json:"service_latency_ms"`
	ServiceErrorRate        ValidationFloatComparison `json:"service_error_rate"`
	LogErrors               ValidationIntComparison   `json:"log_errors"`
	LogWarnings             ValidationIntComparison   `json:"log_warnings"`
	TriggeredSignals        ValidationIntComparison   `json:"triggered_signals"`
	ValidationConfidence    ValidationFloatComparison `json:"validation_confidence"`
	RecommendationViability ValidationIntComparison   `json:"recommendation_viability"`
	SecurityScore           ValidationFloatComparison `json:"security_score"`
}

type ValidationStateDelta struct {
	RiskDelta                    float64 `json:"risk_delta"`
	LatencyDeltaMS               float64 `json:"latency_delta_ms"`
	ErrorRateDelta               float64 `json:"error_rate_delta"`
	LogErrorDelta                int64   `json:"log_error_delta"`
	LogWarningDelta              int64   `json:"log_warning_delta"`
	TriggeredSignalDelta         int     `json:"triggered_signal_delta"`
	HealthImproved               bool    `json:"health_improved"`
	SecurityImproved             bool    `json:"security_improved"`
	ValidationConfidenceDelta    float64 `json:"validation_confidence_delta,omitempty"`
	RecommendationViabilityDelta int     `json:"recommendation_viability_delta,omitempty"`
}

type AnalysisHandoff struct {
	SchemaVersion              string                      `json:"schema_version"`
	Agent                      string                      `json:"agent"`
	IncidentID                 string                      `json:"incident_id,omitempty"`
	CorrelationID              string                      `json:"correlation_id,omitempty"`
	CreatedAt                  time.Time                   `json:"created_at"`
	IncidentSummary            string                      `json:"incident_summary"`
	CollectorID                string                      `json:"collector_id,omitempty"`
	Trigger                    string                      `json:"trigger,omitempty"`
	RiskLevel                  string                      `json:"risk_level,omitempty"`
	Confidence                 float64                     `json:"confidence"`
	ImpactedScope              []string                    `json:"impacted_scope,omitempty"`
	TelemetryQuality           PromptTelemetryQuality      `json:"telemetry_quality"`
	BlindSpots                 []string                    `json:"blind_spots,omitempty"`
	Hypotheses                 []RCAHypothesis             `json:"hypotheses,omitempty"`
	HypothesisPackets          []AnalysisHypothesisHandoff `json:"hypothesis_packets,omitempty"`
	RankedSuspectedCauses      []string                    `json:"ranked_suspected_causes,omitempty"`
	SupportingEvidenceIDs      []string                    `json:"supporting_evidence_ids,omitempty"`
	WeakEvidenceIDs            []string                    `json:"weak_evidence_ids,omitempty"`
	ContradictingEvidenceIDs   []string                    `json:"contradicting_evidence_ids,omitempty"`
	SecurityContext            ValidationSecurityContext   `json:"security_context"`
	Recommendations            []WorkflowRecommendation    `json:"recommendations,omitempty"`
	BoundedActionCandidates    []ValidationActionCandidate `json:"bounded_action_candidates,omitempty"`
	UnresolvedGaps             []string                    `json:"unresolved_gaps,omitempty"`
	ChangeLinks                []RCAChangeLink             `json:"change_links,omitempty"`
	SceneFamily                SceneFamily                 `json:"scene_family,omitempty"`
	SceneConfidence            float64                     `json:"scene_confidence,omitempty"`
	CandidateSubscenes         []string                    `json:"candidate_subscenes,omitempty"`
	MissingEvidence            []string                    `json:"missing_evidence,omitempty"`
	CollectionPlanSummary      CollectionPlanSummary       `json:"collection_plan_summary,omitempty"`
	RecollectionRound          int                         `json:"recollection_round,omitempty"`
	RemainingBudget            InvestigationBudgetState    `json:"remaining_budget,omitempty"`
	EvidenceGoalsStillUnmet    []string                    `json:"evidence_goals_still_unmet,omitempty"`
	SuggestedValidationTargets []ValidationTarget          `json:"suggested_validation_targets,omitempty"`
}

type ValidationTarget struct {
	ID                       string               `json:"id"`
	Type                     ValidationTargetType `json:"type"`
	Title                    string               `json:"title"`
	Summary                  string               `json:"summary,omitempty"`
	HypothesisID             string               `json:"hypothesis_id,omitempty"`
	RecommendationID         string               `json:"recommendation_id,omitempty"`
	Priority                 string               `json:"priority,omitempty"`
	Focus                    string               `json:"focus,omitempty"`
	ImpactedScope            []string             `json:"impacted_scope,omitempty"`
	ToolFamilies             []string             `json:"tool_families,omitempty"`
	SuggestedTools           []ToolName           `json:"suggested_tools,omitempty"`
	EvidenceGaps             []string             `json:"evidence_gaps,omitempty"`
	ContradictionCandidates  []string             `json:"contradiction_candidates,omitempty"`
	ChangeCategories         []string             `json:"change_categories,omitempty"`
	SupportingEvidenceIDs    []string             `json:"supporting_evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string             `json:"contradicting_evidence_ids,omitempty"`
	ExpectedSignals          []string             `json:"expected_signals,omitempty"`
	ReadOnly                 bool                 `json:"read_only"`
	ExecutionCategory        string               `json:"execution_category,omitempty"`
	ActionCandidateID        string               `json:"action_candidate_id,omitempty"`
	PostAction               bool                 `json:"post_action,omitempty"`
	SceneFamily              SceneFamily          `json:"scene_family,omitempty"`
}

type ValidationLoopRecord struct {
	Iteration                int               `json:"iteration"`
	TargetID                 string            `json:"target_id"`
	Tool                     ToolName          `json:"tool"`
	ToolCallID               string            `json:"tool_call_id,omitempty"`
	ToolReason               string            `json:"tool_reason"`
	PlannerNote              string            `json:"planner_note,omitempty"`
	Observation              string            `json:"observation"`
	Verdict                  ValidationVerdict `json:"verdict"`
	ConfidenceDelta          float64           `json:"confidence_delta,omitempty"`
	SupportingEvidenceIDs    []string          `json:"supporting_evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string          `json:"contradicting_evidence_ids,omitempty"`
	ChosenAction             string            `json:"chosen_action,omitempty"`
	StopReason               string            `json:"stop_reason,omitempty"`
	Timestamp                time.Time         `json:"timestamp"`
}

type ValidationTargetResult struct {
	TargetID                 string               `json:"target_id"`
	TargetType               ValidationTargetType `json:"target_type"`
	Title                    string               `json:"title"`
	HypothesisID             string               `json:"hypothesis_id,omitempty"`
	RecommendationID         string               `json:"recommendation_id,omitempty"`
	ActionCandidateID        string               `json:"action_candidate_id,omitempty"`
	Verdict                  ValidationVerdict    `json:"verdict"`
	Confidence               float64              `json:"confidence"`
	Summary                  string               `json:"summary"`
	EvidenceGaps             []string             `json:"evidence_gaps,omitempty"`
	SupportingEvidenceIDs    []string             `json:"supporting_evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string             `json:"contradicting_evidence_ids,omitempty"`
	ToolSequence             []ToolName           `json:"tool_sequence,omitempty"`
	StopReason               string               `json:"stop_reason,omitempty"`
}

type PostActionValidationSummary struct {
	Verdict                  ValidationVerdict           `json:"verdict"`
	Summary                  string                      `json:"summary"`
	ActionID                 string                      `json:"action_id,omitempty"`
	ExecutionCategory        string                      `json:"execution_category,omitempty"`
	FallbackMode             string                      `json:"fallback_mode,omitempty"`
	BeforeRisk               float64                     `json:"before_risk,omitempty"`
	AfterRisk                float64                     `json:"after_risk,omitempty"`
	BeforeSnapshot           *ValidationEvidenceSnapshot `json:"before_snapshot,omitempty"`
	AfterSnapshot            *ValidationEvidenceSnapshot `json:"after_snapshot,omitempty"`
	Comparison               *ValidationEffectComparison `json:"comparison,omitempty"`
	Delta                    *ValidationStateDelta       `json:"delta,omitempty"`
	SupportingEvidenceIDs    []string                    `json:"supporting_evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string                    `json:"contradicting_evidence_ids,omitempty"`
}

type ValidationGovernanceTrace struct {
	ActionCandidateID  string   `json:"action_candidate_id,omitempty"`
	ActionContractID   string   `json:"action_contract_id,omitempty"`
	ActionSummary      string   `json:"action_summary,omitempty"`
	ActionIntent       string   `json:"action_intent,omitempty"`
	ActionCategory     string   `json:"action_category,omitempty"`
	TargetScope        string   `json:"target_scope,omitempty"`
	ExecutionCategory  string   `json:"execution_category,omitempty"`
	ValidationCategory string   `json:"validation_category,omitempty"`
	ActuatorSafetyTier string   `json:"actuator_safety_tier,omitempty"`
	ProposalOnly       bool     `json:"proposal_only,omitempty"`
	ExecutionEligible  bool     `json:"execution_eligible,omitempty"`
	DryRun             bool     `json:"dry_run,omitempty"`
	DryRunState        string   `json:"dry_run_state,omitempty"`
	RequiresApproval   bool     `json:"requires_approval,omitempty"`
	PolicyStatus       string   `json:"policy_status,omitempty"`
	PolicyReason       string   `json:"policy_reason,omitempty"`
	ApprovalState      string   `json:"approval_state,omitempty"`
	StepID             string   `json:"step_id,omitempty"`
	ToolCallID         string   `json:"tool_call_id,omitempty"`
	StepStatus         string   `json:"step_status,omitempty"`
	RollbackStatus     string   `json:"rollback_status,omitempty"`
	BlastRadiusNotes   []string `json:"blast_radius_notes,omitempty"`
}

type ValidationActionReport struct {
	SchemaVersion               string                       `json:"schema_version"`
	Agent                       string                       `json:"agent"`
	IncidentID                  string                       `json:"incident_id,omitempty"`
	CorrelationID               string                       `json:"correlation_id,omitempty"`
	Mode                        string                       `json:"mode"`
	StartedAt                   time.Time                    `json:"started_at"`
	CompletedAt                 time.Time                    `json:"completed_at,omitempty"`
	Iterations                  int                          `json:"iterations"`
	ToolCalls                   int                          `json:"tool_calls"`
	TargetLimit                 int                          `json:"target_limit"`
	ReadOnlyOnly                bool                         `json:"read_only_only"`
	Targets                     []ValidationTarget           `json:"targets,omitempty"`
	Results                     []ValidationTargetResult     `json:"results,omitempty"`
	LoopRecords                 []ValidationLoopRecord       `json:"loop_records,omitempty"`
	ValidatedRecommendationIDs  []string                     `json:"validated_recommendation_ids,omitempty"`
	RejectedRecommendationIDs   []string                     `json:"rejected_recommendation_ids,omitempty"`
	ContradictionSummary        []string                     `json:"contradiction_summary,omitempty"`
	ActionSummary               []string                     `json:"action_summary,omitempty"`
	ActionCandidates            []ValidationActionCandidate  `json:"action_candidates,omitempty"`
	SelectedAction              *ValidationActionCandidate   `json:"selected_action,omitempty"`
	SelectedDiagnosticContract  *DiagnosticActionContract    `json:"selected_diagnostic_contract,omitempty"`
	SelectedActionContract      *ValidationActionContract    `json:"selected_action_contract,omitempty"`
	Governance                  *ValidationGovernanceTrace   `json:"governance,omitempty"`
	SourceAnalysisMessage       *AgentMessageRef             `json:"source_analysis_message,omitempty"`
	SourceValidationRequest     *AgentMessageRef             `json:"source_validation_request_message,omitempty"`
	HandoffParseLatencyMS       int64                        `json:"handoff_parse_latency_ms,omitempty"`
	ResultMessage               *AgentMessageRef             `json:"result_message,omitempty"`
	ActionDecisionMessage       *AgentMessageRef             `json:"action_decision_message,omitempty"`
	PostActionValidationMessage *AgentMessageRef             `json:"post_action_validation_message,omitempty"`
	CompensationMessage         *AgentMessageRef             `json:"compensation_message,omitempty"`
	UnresolvedUncertainty       []string                     `json:"unresolved_uncertainty,omitempty"`
	Confidence                  float64                      `json:"confidence"`
	StopReason                  string                       `json:"stop_reason,omitempty"`
	DegradedFallbackReason      string                       `json:"degraded_fallback_reason,omitempty"`
	PostActionValidation        *PostActionValidationSummary `json:"post_action_validation,omitempty"`
}
