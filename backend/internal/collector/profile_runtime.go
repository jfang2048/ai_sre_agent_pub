package collector

import (
	"strings"
	"sync"
	"time"
)

type runtimeProfileRuntime struct {
	mu      sync.RWMutex
	profile RuntimeProfile
	expires time.Time
	now     func() time.Time
}

func newRuntimeProfileRuntime() *runtimeProfileRuntime {
	return &runtimeProfileRuntime{
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (r *runtimeProfileRuntime) apply(profile RuntimeProfile) RuntimeProfileStatus {
	if r == nil {
		return RuntimeProfileStatus{State: "disabled"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profile = normalizeRuntimeProfile(profile)
	if r.profile.TTL > 0 {
		r.expires = r.now().Add(r.profile.TTL)
	} else {
		r.expires = time.Time{}
	}
	return r.statusLocked()
}

func (r *runtimeProfileRuntime) clearIfExpired() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.profile.ProfileID == "" || r.expires.IsZero() || r.now().Before(r.expires) {
		return false
	}
	r.profile = RuntimeProfile{}
	r.expires = time.Time{}
	return true
}

func (r *runtimeProfileRuntime) snapshot() RuntimeProfileStatus {
	if r == nil {
		return RuntimeProfileStatus{State: "disabled"}
	}
	r.clearIfExpired()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statusLocked()
}

func (r *runtimeProfileRuntime) statusLocked() RuntimeProfileStatus {
	if strings.TrimSpace(r.profile.ProfileID) == "" {
		return RuntimeProfileStatus{State: "inactive"}
	}
	state := "active"
	if !r.expires.IsZero() && r.now().After(r.expires) {
		state = "expired"
	}
	return RuntimeProfileStatus{
		ProfileID:          r.profile.ProfileID,
		SceneFamily:        r.profile.SceneFamily,
		State:              state,
		Reason:             r.profile.Reason,
		AllowedModules:     append([]string(nil), r.profile.AllowedModules...),
		TargetScope:        append([]string(nil), r.profile.TargetScope...),
		SamplingInterval:   r.profile.SamplingInterval,
		CollectionWindow:   r.profile.CollectionWindow,
		ProcessTopK:        r.profile.ProcessTopK,
		LogBudget:          r.profile.LogBudget,
		EventFilters:       append([]string(nil), r.profile.EventFilters...),
		GPUDetailMode:      r.profile.GPUDetailMode,
		MaxOverheadPercent: r.profile.MaxOverheadPercent,
		ExpiresAt:          r.expires,
	}
}

func (r *runtimeProfileRuntime) activeProfile() (RuntimeProfile, bool) {
	if r == nil {
		return RuntimeProfile{}, false
	}
	r.clearIfExpired()
	r.mu.RLock()
	defer r.mu.RUnlock()
	if strings.TrimSpace(r.profile.ProfileID) == "" {
		return RuntimeProfile{}, false
	}
	return r.profile, true
}

func normalizeRuntimeProfile(profile RuntimeProfile) RuntimeProfile {
	profile.ProfileID = strings.TrimSpace(profile.ProfileID)
	profile.SceneFamily = strings.TrimSpace(profile.SceneFamily)
	profile.Reason = strings.TrimSpace(profile.Reason)
	profile.AllowedModules = compactStrings(profile.AllowedModules...)
	profile.TargetScope = compactStrings(profile.TargetScope...)
	profile.EventFilters = compactStrings(profile.EventFilters...)
	profile.GPUDetailMode = strings.TrimSpace(profile.GPUDetailMode)
	if profile.ProcessTopK < 0 {
		profile.ProcessTopK = 0
	}
	if profile.LogBudget < 0 {
		profile.LogBudget = 0
	}
	if profile.MaxOverheadPercent < 0 {
		profile.MaxOverheadPercent = 0
	}
	return profile
}
