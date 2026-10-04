package agent

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/causalgraph"
	"github.com/stretchr/testify/require"
)

func TestClassifySeriesTrendKeepsBelowThresholdWorkloadAsWatchOnly(t *testing.T) {
	profile := riskSignalProfiles()["cpu_pressure"]
	trend, triggered := classifySeriesTrend(53.2, 38, 0.8, 0, 0, 0, profile)

	require.Equal(t, "rising", trend)
	require.False(t, triggered, "a relative increase below the medium threshold is not an active incident")
}

func TestClassifySeriesTrendMarksRecoveredSpikeInactive(t *testing.T) {
	profile := riskSignalProfiles()["cpu_pressure"]
	trend, triggered := classifySeriesTrend(30, 28.58, -8.5, 0, 5, 0, profile)

	require.Equal(t, "recovering", trend)
	require.False(t, triggered, "old breaches must not keep a recovered transient active")
}

func TestTrendSeverityDoesNotTreatMediumBreachesAsHigh(t *testing.T) {
	profile := riskSignalProfiles()["service_latency"]
	severity := trendSeverity(RiskSeries{
		Latest:            196,
		ThresholdBreaches: 11,
		PersistencePoints: 11,
		Triggered:         true,
	}, profile)

	require.Equal(t, "medium", severity, "the breach counter uses the medium threshold")
}

func TestMemoryLeakRateThresholdsMatchPercentagePointsPerMinute(t *testing.T) {
	profile := riskSignalProfiles()["memory_leak_rate"]
	require.Equal(t, 1.0, profile.medium)
	require.Equal(t, 4.0, profile.high)
}

func TestServiceLatencyIsNotMappedToStorageRootCause(t *testing.T) {
	require.Equal(t, "service latency degradation", hypothesisTitleFromSignal("Service latency p95"))
	require.Equal(t, "storage io bottleneck", hypothesisTitleFromSignal("IO latency p99"))
}

func TestMemoryHypothesisIncludesSpecificReadOnlyValidationSteps(t *testing.T) {
	checks := checksForHypothesis("memory pressure and reclaim")
	require.Contains(t, checks, "cat /proc/pressure/memory")
	require.Contains(t, checks, "capture heap profile for the leading process")
}

func TestBuildRiskSignalsDoesNotTriggerLargeRelativeRiseBelowMedium(t *testing.T) {
	series := []RiskSeries{{
		Key:          "cpu_pressure",
		Latest:       53.2,
		Baseline:     38,
		DeltaPercent: 40,
		Triggered:    false,
		Points:       []RiskSeriesPoint{{Timestamp: time.Now()}},
	}}
	signals := buildRiskSignals("node-a", series, securityToolData{}, ebpfToolData{}, nil)

	require.Len(t, signals, 1)
	require.False(t, signals[0].Triggered)
}

func TestMetricsEvidenceBoostRequiresTriggeredSignalInSameDomain(t *testing.T) {
	state := &workflowState{riskSignals: []JointRiskSignal{{
		ID:        "service_latency",
		Name:      "Service latency",
		Triggered: true,
		Score:     0.2,
	}}}

	require.False(t, hasTriggeredRiskSignalLike(state, "cpu"))
	require.False(t, hasTriggeredRiskSignalLike(state, "io"))
	require.True(t, hasTriggeredRiskSignalLike(state, "service_latency"))
}

func TestFinalRankingCapsCandidatesBelowLowIncidentConfidence(t *testing.T) {
	state := &workflowState{
		incident:    IncidentSynthesis{Confidence: 0.25},
		riskSignals: []JointRiskSignal{{ID: "cpu_pressure", Triggered: true, Score: 0.1}},
		hypotheses: []RCAHypothesis{
			{ID: "cpu", Title: "cpu scheduling contention", Confidence: 0.8},
			{ID: "memory", Title: "memory pressure and reclaim", Confidence: 0.6},
		},
	}

	rankFinalHypotheses(state)

	require.Equal(t, "cpu", state.hypotheses[0].ID)
	require.Equal(t, "memory", state.hypotheses[1].ID)
	require.InDelta(t, 0.49, state.hypotheses[0].Confidence, 0.000001)
	require.InDelta(t, 0.3675, state.hypotheses[1].Confidence, 0.000001)
}

func TestContainmentRecommendationRequiresSufficientIncidentConfidence(t *testing.T) {
	state := &workflowState{incident: IncidentSynthesis{
		Confidence:     0.25,
		Severity:       "medium",
		ImpactedScope:  []string{"checkout"},
		GroupedSignals: []IncidentGroupedSignal{{Severity: "medium"}},
	}, retrievedRunbooks: []RetrievedDocumentEvidence{{
		DocID: "memory", Title: "Memory Pressure Runbook", Summary: "inspect memory pressure and reclaim",
	}}}

	for _, recommendation := range buildRCARecommendations(state) {
		require.NotEqual(t, "probable_containment", recommendation.Category)
		require.NotEqual(t, "medium_term_remediation", recommendation.Category)
	}

	state.incident.Severity = "high"
	state.incident.GroupedSignals = []IncidentGroupedSignal{{Severity: "high"}}
	for _, recommendation := range buildRCARecommendations(state) {
		if recommendation.Category == "medium_term_remediation" {
			return
		}
	}
	t.Fatal("high-severity incidents should retain guarded runbook guidance")
}

func TestStructuredRCADoesNotClaimRootCauseBelowConfidenceThreshold(t *testing.T) {
	state := &workflowState{
		collectorID: "collector-a",
		incident:    IncidentSynthesis{Confidence: 0.25},
		riskSignals: []JointRiskSignal{{ID: "cpu_pressure", Triggered: true, Score: 0.1}},
		hypotheses:  []RCAHypothesis{{ID: "cpu", Title: "cpu scheduling contention", Confidence: 0.49}},
		causal:      causalgraph.Analysis{SuspectedRootCauseEntity: "node-a"},
	}

	report := buildStructuredRCAReport(state)

	require.Empty(t, report.SuspectedRootCauseEntity)
}
