package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type CollectorProfileRequest struct {
	ProfileID          string        `json:"profile_id"`
	SceneFamily        SceneFamily   `json:"scene_family"`
	Reason             string        `json:"reason,omitempty"`
	AllowedModules     []string      `json:"allowed_modules,omitempty"`
	TargetScope        []string      `json:"target_scope,omitempty"`
	SamplingInterval   time.Duration `json:"sampling_interval,omitempty"`
	CollectionWindow   time.Duration `json:"collection_window,omitempty"`
	ProcessTopK        int           `json:"process_topk,omitempty"`
	LogBudget          int           `json:"log_budget,omitempty"`
	EventFilters       []string      `json:"event_filters,omitempty"`
	GPUDetailMode      string        `json:"gpu_detail_mode,omitempty"`
	MaxOverheadPercent float64       `json:"max_overhead_percent,omitempty"`
	TTL                time.Duration `json:"ttl,omitempty"`
	Replayable         bool          `json:"replayable,omitempty"`
}

type CollectorProfileStatus struct {
	ProfileID          string        `json:"profile_id,omitempty"`
	SceneFamily        SceneFamily   `json:"scene_family,omitempty"`
	State              string        `json:"state,omitempty"`
	AllowedModules     []string      `json:"allowed_modules,omitempty"`
	TargetScope        []string      `json:"target_scope,omitempty"`
	SamplingInterval   time.Duration `json:"sampling_interval,omitempty"`
	ProcessTopK        int           `json:"process_topk,omitempty"`
	LogBudget          int           `json:"log_budget,omitempty"`
	EventFilters       []string      `json:"event_filters,omitempty"`
	GPUDetailMode      string        `json:"gpu_detail_mode,omitempty"`
	MaxOverheadPercent float64       `json:"max_overhead_percent,omitempty"`
	ExpiresAt          time.Time     `json:"expires_at,omitempty"`
	Reason             string        `json:"reason,omitempty"`
}

type CollectorProfileApplier interface {
	ApplyRuntimeProfile(ctx context.Context, collectorID string, profile CollectorProfileRequest) (CollectorProfileStatus, error)
}

func (e *WorkflowEngine) SetCollectorProfileApplier(applier CollectorProfileApplier) {
	if e == nil {
		return
	}
	e.collectorProfileApplier = applier
}

func collectorProfileRequestFromPlan(plan CollectionPlan) CollectorProfileRequest {
	return CollectorProfileRequest{
		ProfileID:          fmt.Sprintf("%s-profile", plan.PlanID),
		SceneFamily:        plan.SceneFamily,
		Reason:             fmt.Sprintf("scene=%s round=%d", plan.SceneFamily, plan.RoundIndex),
		AllowedModules:     append([]string(nil), plan.TargetCollectorsOrModules...),
		TargetScope:        append([]string(nil), plan.TargetScope...),
		SamplingInterval:   plan.SamplingInterval,
		CollectionWindow:   plan.CollectionWindow,
		ProcessTopK:        plan.ProcessTopK,
		LogBudget:          plan.LogBudget,
		EventFilters:       append([]string(nil), plan.EventFilters...),
		GPUDetailMode:      plan.GPUDetailMode,
		MaxOverheadPercent: plan.MaxOverheadPercent,
		TTL:                plan.TTL,
		Replayable:         plan.Replayable,
	}
}

func defaultCollectorProfileStatus(plan CollectionPlan, reason string) CollectorProfileStatus {
	return CollectorProfileStatus{
		ProfileID:          fmt.Sprintf("%s-profile", plan.PlanID),
		SceneFamily:        plan.SceneFamily,
		State:              "requested_only",
		AllowedModules:     append([]string(nil), plan.TargetCollectorsOrModules...),
		TargetScope:        append([]string(nil), plan.TargetScope...),
		SamplingInterval:   plan.SamplingInterval,
		ProcessTopK:        plan.ProcessTopK,
		LogBudget:          plan.LogBudget,
		EventFilters:       append([]string(nil), plan.EventFilters...),
		GPUDetailMode:      plan.GPUDetailMode,
		MaxOverheadPercent: plan.MaxOverheadPercent,
		ExpiresAt:          time.Now().UTC().Add(plan.TTL),
		Reason:             strings.TrimSpace(reason),
	}
}
