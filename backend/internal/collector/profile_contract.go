package collector

import "time"

type RuntimeProfile struct {
	ProfileID          string        `json:"profile_id"`
	SceneFamily        string        `json:"scene_family,omitempty"`
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

type RuntimeProfileStatus struct {
	ProfileID          string        `json:"profile_id,omitempty"`
	SceneFamily        string        `json:"scene_family,omitempty"`
	State              string        `json:"state,omitempty"`
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
	ExpiresAt          time.Time     `json:"expires_at,omitempty"`
}
