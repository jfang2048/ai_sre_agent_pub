package agent

import "time"

type ValidationTargetType string

const (
	ValidationTargetHypothesis        ValidationTargetType = "hypothesis_validation"
	ValidationTargetRecommendation    ValidationTargetType = "recommendation_validation"
	ValidationTargetChangeCorrelation ValidationTargetType = "change_correlation_validation"
	ValidationTargetRemediation       ValidationTargetType = "remediation_outcome_validation"
	ValidationTargetContradiction     ValidationTargetType = "contradiction_search"
)

type ValidationVerdict string

const (
	ValidationVerdictConfirmed            ValidationVerdict = "confirmed"
	ValidationVerdictContradicted         ValidationVerdict = "contradicted"
	ValidationVerdictPartiallySupported   ValidationVerdict = "partially_supported"
	ValidationVerdictInsufficientEvidence ValidationVerdict = "insufficient_evidence"
)

type AnalysisHandoff struct {
	Agent                      string                   `json:"agent"`
	CreatedAt                  time.Time                `json:"created_at"`
	IncidentSummary            string                   `json:"incident_summary"`
	CollectorID                string                   `json:"collector_id,omitempty"`
	Trigger                    string                   `json:"trigger,omitempty"`
	RiskLevel                  string                   `json:"risk_level,omitempty"`
	Confidence                 float64                  `json:"confidence"`
	TelemetryQuality           PromptTelemetryQuality   `json:"telemetry_quality"`
	Hypotheses                 []RCAHypothesis          `json:"hypotheses,omitempty"`
	RankedSuspectedCauses      []string                 `json:"ranked_suspected_causes,omitempty"`
	SupportingEvidenceIDs      []string                 `json:"supporting_evidence_ids,omitempty"`
	WeakEvidenceIDs            []string                 `json:"weak_evidence_ids,omitempty"`
	ContradictingEvidenceIDs   []string                 `json:"contradicting_evidence_ids,omitempty"`
	Recommendations            []WorkflowRecommendation `json:"recommendations,omitempty"`
	UnresolvedGaps             []string                 `json:"unresolved_gaps,omitempty"`
	ChangeLinks                []RCAChangeLink          `json:"change_links,omitempty"`
	SuggestedValidationTargets []ValidationTarget       `json:"suggested_validation_targets,omitempty"`
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
	SuggestedTools           []ToolName           `json:"suggested_tools,omitempty"`
	SupportingEvidenceIDs    []string             `json:"supporting_evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string             `json:"contradicting_evidence_ids,omitempty"`
	ExpectedSignals          []string             `json:"expected_signals,omitempty"`
}

type ValidationLoopRecord struct {
	Iteration                int               `json:"iteration"`
	TargetID                 string            `json:"target_id"`
	Tool                     ToolName          `json:"tool"`
	ToolCallID               string            `json:"tool_call_id,omitempty"`
	ToolReason               string            `json:"tool_reason"`
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
	Verdict                  ValidationVerdict    `json:"verdict"`
	Confidence               float64              `json:"confidence"`
	Summary                  string               `json:"summary"`
	SupportingEvidenceIDs    []string             `json:"supporting_evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string             `json:"contradicting_evidence_ids,omitempty"`
	ToolSequence             []ToolName           `json:"tool_sequence,omitempty"`
	StopReason               string               `json:"stop_reason,omitempty"`
}

type PostActionValidationSummary struct {
	Verdict                  ValidationVerdict `json:"verdict"`
	Summary                  string            `json:"summary"`
	BeforeRisk               float64           `json:"before_risk,omitempty"`
	AfterRisk                float64           `json:"after_risk,omitempty"`
	SupportingEvidenceIDs    []string          `json:"supporting_evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string          `json:"contradicting_evidence_ids,omitempty"`
}

type ValidationActionReport struct {
	Agent                      string                       `json:"agent"`
	Mode                       string                       `json:"mode"`
	StartedAt                  time.Time                    `json:"started_at"`
	CompletedAt                time.Time                    `json:"completed_at,omitempty"`
	Iterations                 int                          `json:"iterations"`
	ToolCalls                  int                          `json:"tool_calls"`
	TargetLimit                int                          `json:"target_limit"`
	ReadOnlyOnly               bool                         `json:"read_only_only"`
	Targets                    []ValidationTarget           `json:"targets,omitempty"`
	Results                    []ValidationTargetResult     `json:"results,omitempty"`
	LoopRecords                []ValidationLoopRecord       `json:"loop_records,omitempty"`
	ValidatedRecommendationIDs []string                     `json:"validated_recommendation_ids,omitempty"`
	RejectedRecommendationIDs  []string                     `json:"rejected_recommendation_ids,omitempty"`
	ContradictionSummary       []string                     `json:"contradiction_summary,omitempty"`
	ActionSummary              []string                     `json:"action_summary,omitempty"`
	UnresolvedUncertainty      []string                     `json:"unresolved_uncertainty,omitempty"`
	Confidence                 float64                      `json:"confidence"`
	StopReason                 string                       `json:"stop_reason,omitempty"`
	DegradedFallbackReason     string                       `json:"degraded_fallback_reason,omitempty"`
	PostActionValidation       *PostActionValidationSummary `json:"post_action_validation,omitempty"`
}
