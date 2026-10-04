package agent

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/causalgraph"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
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
	require.Equal(t, "service latency degradation", hypothesisTitleFromSignal("service_latency_p95_ms"))
	require.Equal(t, "storage io bottleneck", hypothesisTitleFromSignal("IO latency p99"))
	require.Equal(t, "network congestion or packet loss", hypothesisTitleFromSignal("TCP retransmit ratio"))
	require.Equal(t, "network congestion or packet loss", hypothesisTitleFromSignal("retransmit_ratio"))
}

func TestProcessAttributionRequiresCorroboratingPressure(t *testing.T) {
	t.Run("high CPU process is attributed when host CPU is high", func(t *testing.T) {
		state := &workflowState{
			metricsData: metricsToolData{Node: &ingest.NodeSnapshot{ProcessResources: map[string]*ingest.ProcessResourceSample{
				"checkout-api": {Name: "checkout-api", SignalValues: map[string]float64{"rca_cpu_process_percent": 96}, CategoryTotals: map[string]float64{"cpu": 96}},
			}}},
			riskSignals: []JointRiskSignal{{ID: "cpu_pressure", Triggered: true, Severity: "high", Score: 0.9}},
			logsData:    logsToolData{Snippets: []string{"checkout-api saturating cpu with run queue growth"}},
		}

		hypotheses := processAttributedHypotheses(state)
		require.Len(t, hypotheses, 1)
		require.Equal(t, "cpu scheduling contention in checkout-api", hypotheses[0].Title)
		require.GreaterOrEqual(t, hypotheses[0].Confidence, 0.9)
		require.Equal(t, "ev-process-cpu-checkout-api", processAttributionEvidence(state)[0].ID)
	})

	t.Run("database process needs both storage pressure and wait evidence", func(t *testing.T) {
		state := &workflowState{
			metricsData: metricsToolData{Node: &ingest.NodeSnapshot{ProcessResources: map[string]*ingest.ProcessResourceSample{
				"postgres": {Name: "postgres", CategoryTotals: map[string]float64{"disk_io": 100}},
			}}},
			riskSignals: []JointRiskSignal{{ID: "io_latency", Triggered: true, Severity: "high", Score: 0.9}},
			logsData:    logsToolData{Snippets: []string{"payment request timeout while waiting for checkout database connection"}},
		}

		hypotheses := processAttributedHypotheses(state)
		require.Len(t, hypotheses, 1)
		require.Equal(t, "database connection saturation in postgres", hypotheses[0].Title)
		require.Equal(t, "ev-process-database-postgres", processAttributionEvidence(state)[0].ID)

		state.logsData.Snippets = []string{"postgres checkpoint completed"}
		require.Empty(t, processAttributedHypotheses(state), "database process presence alone must not imply connection saturation")

		state.logsData.Snippets = []string{"payment timeout while waiting on checkout database connection"}
		state.metricsData.Node.ProcessResources["postgres"] = &ingest.ProcessResourceSample{Name: "postgres_exporter", CategoryTotals: map[string]float64{"disk_io": 100}}
		require.Empty(t, processAttributedHypotheses(state), "database metrics exporters are not the database process")
	})

	t.Run("low CPU severity does not promote process usage", func(t *testing.T) {
		state := &workflowState{
			metricsData: metricsToolData{Node: &ingest.NodeSnapshot{ProcessResources: map[string]*ingest.ProcessResourceSample{
				"checkout-api": {Name: "checkout-api", SignalValues: map[string]float64{"rca_cpu_process_percent": 96}, CategoryTotals: map[string]float64{"cpu": 96}},
			}}},
			riskSignals: []JointRiskSignal{{ID: "cpu_pressure", Triggered: true, Severity: "medium", Score: 0.9}},
		}

		require.Empty(t, processAttributedHypotheses(state))
	})
}

func TestDirectProcessAttributionOutranksCorrelatedHostSymptoms(t *testing.T) {
	state := &workflowState{
		engine: &WorkflowEngine{cfg: WorkflowConfig{MaxHypotheses: 8}},
		riskSignals: []JointRiskSignal{
			{ID: "cpu_pressure", Name: "CPU usage", Triggered: true, Severity: "high", Weight: 1, Score: 0.9},
			{ID: "io_latency", Name: "IO latency", Triggered: true, Severity: "high", Weight: 1, Score: 0.95},
		},
		hypotheses: []RCAHypothesis{
			{ID: "h-process-cpu-checkout-api", Title: "cpu scheduling contention in checkout-api", Confidence: 0.84},
			{ID: "h-service-latency", Title: "service latency degradation", Confidence: 0.93},
			{ID: "h-storage", Title: "storage io bottleneck", Confidence: 0.90},
		},
	}

	rerankHypotheses(state)
	require.Equal(t, "cpu scheduling contention in checkout-api", state.hypotheses[0].Title)
}

func TestFinalRankingUsesProcessEvidenceToBreakSymptomTie(t *testing.T) {
	state := &workflowState{
		incident:    IncidentSynthesis{Confidence: 0.40},
		riskSignals: []JointRiskSignal{{ID: "cpu_pressure", Triggered: true}},
		hypotheses: []RCAHypothesis{
			{ID: "h-process-cpu-checkout-api", Title: "cpu scheduling contention in checkout-api", Confidence: 0.30, EvidenceIDs: []string{"ev-process-cpu-checkout-api"}},
			{ID: "h-service-latency", Title: "service latency degradation", Confidence: 0.48},
		},
		evidence: []RCAEvidence{{ID: "ev-process-cpu-checkout-api", Kind: "process_resource", Entity: "checkout-api", Summary: "checkout-api process CPU usage 96%"}},
	}

	rankFinalHypotheses(state)
	require.Equal(t, "cpu scheduling contention in checkout-api", state.hypotheses[0].Title)
	require.LessOrEqual(t, state.hypotheses[0].Confidence, 0.49)
}

func TestFinalRankingRequiresMultiDomainRolloutBeforePromotingDistributedCause(t *testing.T) {
	state := &workflowState{
		incident: IncidentSynthesis{Confidence: 0.40},
		riskSignals: []JointRiskSignal{
			{ID: "cpu_pressure", Triggered: true},
			{ID: "io_latency", Triggered: true},
			{ID: "retransmit_ratio", Triggered: true},
		},
		changeLinks: []RCAChangeLink{{Category: "deployment", Summary: "recent rollout"}},
		hypotheses: []RCAHypothesis{
			{ID: "h-distributed", Title: "distributed resource contention", Confidence: 0.32},
			{ID: "h-network", Title: "network congestion or packet loss", Confidence: 0.48},
			{ID: "h-service", Title: "service latency degradation", Confidence: 0.45},
		},
	}

	require.True(t, hasLowConfidenceMultiDomainRollout(state))
	rankFinalHypotheses(state)
	require.Equal(t, "distributed resource contention across cpu and storage", state.hypotheses[0].Title)

	state.changeLinks = nil
	require.False(t, hasLowConfidenceMultiDomainRollout(state), "resource co-occurrence without rollout evidence must not promote distributed contention")
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
