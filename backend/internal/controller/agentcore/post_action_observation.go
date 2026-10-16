package agent

import (
	"context"
	"strings"
	"time"
)

const maxPostActionObservations = 24

// observePostActionUntil performs read-only checks within a bounded window.
// A single good response, or repeated reads of one telemetry sample, cannot
// establish sustained recovery.
func observePostActionUntil(ctx context.Context, window, interval time.Duration, observe func(context.Context) PostActionValidationSummary) PostActionValidationSummary {
	if interval <= 0 {
		interval = time.Second
	}
	if window <= 0 {
		window = interval
	}
	observeCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	latest := PostActionValidationSummary{
		Verdict: ValidationVerdictInsufficientEvidence,
		Summary: "post-action observation was not completed",
	}
	var lastHealthyAt time.Time
	var lastScope string
	var confirmedEvidence []string
	var observedEvidence []string
	var contradicting []string
	var healthySampleTimes []time.Time
	stableSamples := 0
	attempts := 0
	budgetExhausted := false
	for observeCtx.Err() == nil {
		current := observe(observeCtx)
		if current.Verdict == "" {
			current.Verdict = ValidationVerdictInsufficientEvidence
		}
		attempts++
		observedEvidence = append(observedEvidence, current.SupportingEvidenceIDs...)
		observedEvidence = append(observedEvidence, current.ContradictingEvidenceIDs...)
		contradicting = append(contradicting, current.ContradictingEvidenceIDs...)
		latest = current
		if observeCtx.Err() != nil {
			break
		}
		if current.FallbackMode == "missing_pre_action_baseline" {
			break
		}
		if current.Verdict == ValidationVerdictConfirmed && current.AfterSnapshot != nil {
			health := current.AfterSnapshot.ServiceHealth
			scope := strings.TrimSpace(health.ScopeKey)
			if health.Available && health.Fresh && health.Healthy && scope != "" && !health.ObservedAt.IsZero() {
				if stableSamples == 0 || scope != lastScope {
					stableSamples = 1
					lastScope = scope
					lastHealthyAt = health.ObservedAt
					healthySampleTimes = []time.Time{health.ObservedAt}
					confirmedEvidence = append([]string(nil), current.SupportingEvidenceIDs...)
				} else if health.ObservedAt.After(lastHealthyAt) {
					stableSamples++
					lastHealthyAt = health.ObservedAt
					healthySampleTimes = append(healthySampleTimes, health.ObservedAt)
					confirmedEvidence = append(confirmedEvidence, current.SupportingEvidenceIDs...)
				}
				if stableSamples >= 2 {
					latest.ObservationCount = attempts
					latest.StableHealthySamples = stableSamples
					latest.HealthySampleTimes = append([]time.Time(nil), healthySampleTimes...)
					latest.SupportingEvidenceIDs = dedupeStrings(confirmedEvidence)
					latest.ObservationEvidenceIDs = dedupeStrings(observedEvidence)
					latest.ContradictingEvidenceIDs = dedupeStrings(contradicting)
					return latest
				}
			} else {
				stableSamples = 0
				lastHealthyAt = time.Time{}
				lastScope = ""
				healthySampleTimes = nil
				confirmedEvidence = nil
			}
		} else {
			stableSamples = 0
			lastHealthyAt = time.Time{}
			lastScope = ""
			healthySampleTimes = nil
			confirmedEvidence = nil
			if current.Verdict == ValidationVerdictContradicted {
				break
			}
		}
		if attempts >= maxPostActionObservations {
			budgetExhausted = true
			break
		}

		timer := time.NewTimer(interval)
		select {
		case <-observeCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}

	latest.ObservationCount = attempts
	latest.StableHealthySamples = stableSamples
	latest.HealthySampleTimes = append([]time.Time(nil), healthySampleTimes...)
	latest.ObservationEvidenceIDs = dedupeStrings(observedEvidence)
	latest.ContradictingEvidenceIDs = dedupeStrings(contradicting)
	if latest.Verdict == ValidationVerdictConfirmed {
		latest.SupportingEvidenceIDs = dedupeStrings(confirmedEvidence)
		latest.Verdict = ValidationVerdictInsufficientEvidence
		latest.Summary = "post-action evidence did not include two distinct, ordered healthy service samples within the observation window"
		if latest.Comparison != nil {
			latest.Comparison.MissingData = dedupeStrings(append(latest.Comparison.MissingData, "stable_recovery_samples"))
			latest.Comparison.Incomplete = true
		}
	}
	if ctx.Err() != nil {
		latest.FallbackMode = "observation_cancelled"
	} else if budgetExhausted {
		latest.FallbackMode = "observation_budget_exhausted"
	} else if latest.FallbackMode == "" && latest.Verdict != ValidationVerdictContradicted {
		latest.FallbackMode = "observation_window_exhausted"
	}
	return latest
}
