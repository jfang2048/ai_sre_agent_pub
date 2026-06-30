package agent

import "time"

type InvestigationBudgetState struct {
	MaxOverheadPercent float64 `json:"max_overhead_percent,omitempty"`
	RemainingBytes     int64   `json:"remaining_bytes,omitempty"`
	RemainingEvents    int     `json:"remaining_events,omitempty"`
	RemainingRounds    int     `json:"remaining_rounds,omitempty"`
}

type CollectionPlan struct {
	PlanID                    string        `json:"plan_id"`
	SceneFamily               SceneFamily   `json:"scene_family"`
	RoundIndex                int           `json:"round_index"`
	TargetScope               []string      `json:"target_scope,omitempty"`
	TargetCollectorsOrModules []string      `json:"target_collectors_or_modules,omitempty"`
	SamplingInterval          time.Duration `json:"sampling_interval"`
	CollectionWindow          time.Duration `json:"collection_window"`
	ProcessTopK               int           `json:"process_topk,omitempty"`
	LogBudget                 int           `json:"log_budget,omitempty"`
	EventFilters              []string      `json:"event_filters,omitempty"`
	GPUDetailMode             string        `json:"gpu_detail_mode,omitempty"`
	MaxOverheadPercent        float64       `json:"max_overhead_percent,omitempty"`
	MaxBytes                  int64         `json:"max_bytes,omitempty"`
	MaxEvents                 int           `json:"max_events,omitempty"`
	EvidenceGoals             []string      `json:"evidence_goals,omitempty"`
	StopConditions            []string      `json:"stop_conditions,omitempty"`
	TTL                       time.Duration `json:"ttl"`
	Replayable                bool          `json:"replayable"`
}

type CollectionPlanSummary struct {
	PlanID                    string        `json:"plan_id,omitempty"`
	SceneFamily               SceneFamily   `json:"scene_family,omitempty"`
	RoundIndex                int           `json:"round_index,omitempty"`
	TargetScope               []string      `json:"target_scope,omitempty"`
	TargetCollectorsOrModules []string      `json:"target_collectors_or_modules,omitempty"`
	SamplingInterval          time.Duration `json:"sampling_interval,omitempty"`
	CollectionWindow          time.Duration `json:"collection_window,omitempty"`
	ProcessTopK               int           `json:"process_topk,omitempty"`
	LogBudget                 int           `json:"log_budget,omitempty"`
	GPUDetailMode             string        `json:"gpu_detail_mode,omitempty"`
	MaxOverheadPercent        float64       `json:"max_overhead_percent,omitempty"`
	MaxBytes                  int64         `json:"max_bytes,omitempty"`
	MaxEvents                 int           `json:"max_events,omitempty"`
	TTL                       time.Duration `json:"ttl,omitempty"`
}

type RecollectionResult struct {
	PlanID         string                  `json:"plan_id,omitempty"`
	SceneFamily    SceneFamily             `json:"scene_family,omitempty"`
	RoundIndex     int                     `json:"round_index,omitempty"`
	AppliedModules []string                `json:"applied_modules,omitempty"`
	EvidenceRefs   []string                `json:"evidence_refs,omitempty"`
	ObservedBytes  int64                   `json:"observed_bytes,omitempty"`
	ObservedEvents int                     `json:"observed_events,omitempty"`
	RemainingGaps  []string                `json:"remaining_gaps,omitempty"`
	StopReason     string                  `json:"stop_reason,omitempty"`
	Converged      bool                    `json:"converged,omitempty"`
	ProfileStatus  *CollectorProfileStatus `json:"profile_status,omitempty"`
	CompletedAt    time.Time               `json:"completed_at,omitempty"`
}

type EvidenceGapState struct {
	SceneFamily             SceneFamily              `json:"scene_family,omitempty"`
	MissingEvidence         []string                 `json:"missing_evidence,omitempty"`
	EvidenceGoalsStillUnmet []string                 `json:"evidence_goals_still_unmet,omitempty"`
	RemainingBudget         InvestigationBudgetState `json:"remaining_budget,omitempty"`
	Confidence              float64                  `json:"confidence,omitempty"`
	UpdatedAt               time.Time                `json:"updated_at,omitempty"`
}

type EscalationDecision struct {
	Escalate             bool      `json:"escalate"`
	Reason               string    `json:"reason,omitempty"`
	Confidence           float64   `json:"confidence,omitempty"`
	RiskyNextAction      bool      `json:"risky_next_action,omitempty"`
	WeakRollback         bool      `json:"weak_rollback,omitempty"`
	EvidencePackageReady bool      `json:"evidence_package_ready,omitempty"`
	ArtifactPackage      []string  `json:"artifact_package,omitempty"`
	DecidedAt            time.Time `json:"decided_at,omitempty"`
}

func summarizeCollectionPlan(plan CollectionPlan) CollectionPlanSummary {
	return CollectionPlanSummary{
		PlanID:                    plan.PlanID,
		SceneFamily:               plan.SceneFamily,
		RoundIndex:                plan.RoundIndex,
		TargetScope:               append([]string(nil), plan.TargetScope...),
		TargetCollectorsOrModules: append([]string(nil), plan.TargetCollectorsOrModules...),
		SamplingInterval:          plan.SamplingInterval,
		CollectionWindow:          plan.CollectionWindow,
		ProcessTopK:               plan.ProcessTopK,
		LogBudget:                 plan.LogBudget,
		GPUDetailMode:             plan.GPUDetailMode,
		MaxOverheadPercent:        plan.MaxOverheadPercent,
		MaxBytes:                  plan.MaxBytes,
		MaxEvents:                 plan.MaxEvents,
		TTL:                       plan.TTL,
	}
}
