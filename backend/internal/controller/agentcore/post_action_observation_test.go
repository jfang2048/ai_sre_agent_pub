package agent

import (
	"context"
	"slices"
	"testing"
	"time"
)

func confirmedPostActionSample(observedAt time.Time) PostActionValidationSummary {
	return PostActionValidationSummary{
		Verdict: ValidationVerdictConfirmed,
		AfterSnapshot: &ValidationEvidenceSnapshot{
			ServiceHealth: serviceHealthToolData{
				ObservedAt: observedAt,
				Healthy:    true,
				Available:  true,
				Fresh:      true,
				ScopeKey:   "service-a",
			},
		},
	}
}

func TestObservePostActionUntilRequiresTwoDistinctConfirmedSamples(t *testing.T) {
	base := time.Now().UTC()
	calls := 0
	summary := observePostActionUntil(context.Background(), 500*time.Millisecond, time.Millisecond, func(context.Context) PostActionValidationSummary {
		calls++
		switch calls {
		case 1:
			return PostActionValidationSummary{Verdict: ValidationVerdictInsufficientEvidence, SupportingEvidenceIDs: []string{"stale-observation"}}
		case 2:
			sample := confirmedPostActionSample(base)
			sample.SupportingEvidenceIDs = []string{"healthy-first"}
			return sample
		default:
			sample := confirmedPostActionSample(base.Add(time.Second))
			sample.SupportingEvidenceIDs = []string{"healthy-second"}
			return sample
		}
	})
	if summary.Verdict != ValidationVerdictConfirmed {
		t.Fatalf("two distinct healthy samples should confirm recovery, got %q", summary.Verdict)
	}
	if calls != 3 {
		t.Fatalf("expected one retry for insufficient evidence and two confirmed samples, got %d observations", calls)
	}
	if summary.StableHealthySamples != 2 || len(summary.HealthySampleTimes) != 2 {
		t.Fatalf("confirmed report must retain the two distinct service sample times: %+v", summary)
	}
	if slices.Contains(summary.SupportingEvidenceIDs, "stale-observation") || !slices.Contains(summary.ObservationEvidenceIDs, "stale-observation") {
		t.Fatalf("stale observations must remain auditable but cannot support confirmed recovery: %+v", summary)
	}
}

func TestObservePostActionUntilDoesNotCountRepeatedSampleTwice(t *testing.T) {
	base := time.Now().UTC()
	calls := 0
	summary := observePostActionUntil(context.Background(), 150*time.Millisecond, time.Millisecond, func(context.Context) PostActionValidationSummary {
		calls++
		return confirmedPostActionSample(base)
	})
	if summary.Verdict != ValidationVerdictInsufficientEvidence {
		t.Fatalf("one repeated telemetry sample cannot establish stable recovery, got %q", summary.Verdict)
	}
	if calls < 2 {
		t.Fatalf("expected the loop to retry within its window, got %d observation", calls)
	}
}

func TestObservePostActionUntilStopsUnconfirmedAtDeadline(t *testing.T) {
	calls := 0
	summary := observePostActionUntil(context.Background(), 150*time.Millisecond, 10*time.Millisecond, func(context.Context) PostActionValidationSummary {
		calls++
		return PostActionValidationSummary{Verdict: ValidationVerdictPartiallySupported}
	})
	if summary.Verdict == ValidationVerdictConfirmed {
		t.Fatal("a timeout without confirmed samples must not claim recovery")
	}
	if calls < 2 {
		t.Fatalf("expected the loop to retry before its deadline, got %d observation", calls)
	}
}

func TestObservePostActionUntilHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	summary := observePostActionUntil(ctx, time.Second, time.Millisecond, func(context.Context) PostActionValidationSummary {
		calls++
		cancel()
		return confirmedPostActionSample(time.Now().UTC())
	})
	if summary.Verdict != ValidationVerdictInsufficientEvidence {
		t.Fatalf("cancellation after one sample must not claim recovery, got %q", summary.Verdict)
	}
	if calls != 1 {
		t.Fatalf("cancellation must prevent extra observations, got %d", calls)
	}
}

func TestObservePostActionUntilStopsWhenBaselineCannotBeRecovered(t *testing.T) {
	calls := 0
	summary := observePostActionUntil(context.Background(), time.Second, time.Millisecond, func(context.Context) PostActionValidationSummary {
		calls++
		return PostActionValidationSummary{
			Verdict:      ValidationVerdictInsufficientEvidence,
			FallbackMode: "missing_pre_action_baseline",
		}
	})
	if calls != 1 || summary.Verdict != ValidationVerdictInsufficientEvidence {
		t.Fatalf("a missing pre-action baseline cannot be fixed by polling after the action: calls=%d verdict=%q", calls, summary.Verdict)
	}
}

func TestObservePostActionUntilStopsAtReadOnlyQueryBudget(t *testing.T) {
	calls := 0
	summary := observePostActionUntil(context.Background(), time.Second, time.Millisecond, func(context.Context) PostActionValidationSummary {
		calls++
		return PostActionValidationSummary{Verdict: ValidationVerdictInsufficientEvidence}
	})
	if calls != maxPostActionObservations || summary.FallbackMode != "observation_budget_exhausted" {
		t.Fatalf("post-action queries must stop at the fixed budget: calls=%d fallback=%q", calls, summary.FallbackMode)
	}
}
