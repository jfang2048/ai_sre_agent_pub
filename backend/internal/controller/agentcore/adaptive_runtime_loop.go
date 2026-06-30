package agent

type AdaptiveDirective string

const (
	AdaptiveDirectiveContinue      AdaptiveDirective = "continue"
	AdaptiveDirectiveStop          AdaptiveDirective = "stop"
	AdaptiveDirectiveBranch        AdaptiveDirective = "branch"
	AdaptiveDirectiveEscalate      AdaptiveDirective = "escalate"
	AdaptiveDirectiveProposeAction AdaptiveDirective = "propose_action"
	AdaptiveDirectiveExecuteAction AdaptiveDirective = "execute_action"
	AdaptiveDirectiveRollback      AdaptiveDirective = "rollback"
)

type AdaptiveStopReason string

const (
	AdaptiveStopReasonObjectiveSatisfied      AdaptiveStopReason = "objective_satisfied"
	AdaptiveStopReasonConfidenceSufficient    AdaptiveStopReason = "confidence_sufficient"
	AdaptiveStopReasonNoSafeNextStep          AdaptiveStopReason = "no_safe_next_step"
	AdaptiveStopReasonBudgetExhausted         AdaptiveStopReason = "budget_exhausted"
	AdaptiveStopReasonPolicyBlocked           AdaptiveStopReason = "policy_blocked"
	AdaptiveStopReasonApprovalRequired        AdaptiveStopReason = "approval_required"
	AdaptiveStopReasonContradictionUnresolved AdaptiveStopReason = "contradiction_unresolved"
	AdaptiveStopReasonEvidenceUnavailable     AdaptiveStopReason = "evidence_unavailable"
	AdaptiveStopReasonNoProgress              AdaptiveStopReason = "no_progress"
	AdaptiveStopReasonUncertaintyPlateau      AdaptiveStopReason = "uncertainty_plateau"
)

type AdaptiveBranchKind string

const (
	AdaptiveBranchNarrowScope                    AdaptiveBranchKind = "narrow_scope"
	AdaptiveBranchBroadenScope                   AdaptiveBranchKind = "broaden_scope"
	AdaptiveBranchCompareWithBaseline            AdaptiveBranchKind = "compare_with_baseline"
	AdaptiveBranchSwitchHypothesisFamily         AdaptiveBranchKind = "switch_hypothesis_family"
	AdaptiveBranchSwitchToChangeAnalysis         AdaptiveBranchKind = "switch_to_change_analysis"
	AdaptiveBranchSwitchToRuntimeAnalysis        AdaptiveBranchKind = "switch_to_runtime_analysis"
	AdaptiveBranchSwitchToDependencyAnalysis     AdaptiveBranchKind = "switch_to_dependency_analysis"
	AdaptiveBranchSwitchRecommendationValidation AdaptiveBranchKind = "switch_to_recommendation_validation"
)
