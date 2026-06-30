package agent

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/predictive"
	"github.com/stretchr/testify/require"
)

func TestSemanticReportFingerprintIgnoresVolatileIDs(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(30 * time.Second)

	reportA := Report{
		ID:          "report-a",
		NodeName:    "node-a",
		GeneratedAt: t0,
		Summary:     "Memory pressure is rising",
		Findings:    []string{"Memory pressure rising", "Swap activity detected"},
		Forecasts:   []string{"Memory exhaustion likely within 15m"},
		Predictions: []predictive.Finding{{
			PredictionID:  "pred-a",
			Metric:        "node_memory_Used_bytes",
			PredictiveSLO: "memory_headroom",
			Title:         "Predictive memory pressure",
			Severity:      "high",
			HazardClass:   "memory_pressure",
		}},
		Actions: []ActionDecision{{
			ID:       "action-a",
			Type:     "investigate",
			Reason:   "Inspect top RSS processes",
			Priority: "high",
			Status:   ActionStatusProposed,
			Safe:     true,
		}},
		Evidence: analysis.EvidencePack{
			Processes: []analysis.ProcessSummary{{PID: 101, Name: "postgres", CPUPercent: 37.1, RSSBytes: 1_700_000_000}},
			Logs:      []analysis.LogSummary{{Fingerprint: "oom-reclaim", Count: 18}},
		},
		RCAs: []analysis.RootCauseAnalysis{{
			ID:             "rca-a",
			Symptom:        "memory pressure",
			RootCause:      "working set growth",
			AnalysisMethod: "rules",
		}},
		LLM: &LLMInsight{
			Summary:    "Working set growth is the leading explanation.",
			RootCause:  "working set growth",
			Confidence: 0.84,
		},
	}

	reportB := Report{
		ID:          "report-b",
		NodeName:    "node-a",
		GeneratedAt: t1,
		Summary:     "Memory pressure is rising",
		Findings:    []string{"Swap activity detected", "Memory pressure rising"},
		Forecasts:   []string{"Memory exhaustion likely within 15m"},
		Predictions: []predictive.Finding{{
			PredictionID:  "pred-b",
			Metric:        "node_memory_Used_bytes",
			PredictiveSLO: "memory_headroom",
			Title:         "Predictive memory pressure",
			Severity:      "high",
			HazardClass:   "memory_pressure",
		}},
		Actions: []ActionDecision{{
			ID:       "action-b",
			Type:     "investigate",
			Reason:   "Inspect top RSS processes",
			Priority: "high",
			Status:   ActionStatusProposed,
			Safe:     true,
		}},
		Evidence: analysis.EvidencePack{
			Processes: []analysis.ProcessSummary{{PID: 101, Name: "postgres", CPUPercent: 38.9, RSSBytes: 1_760_000_000}},
			Logs:      []analysis.LogSummary{{Fingerprint: "oom-reclaim", Count: 21}},
		},
		RCAs: []analysis.RootCauseAnalysis{{
			ID:             "rca-b",
			Symptom:        "memory pressure",
			RootCause:      "working set growth",
			AnalysisMethod: "rules",
		}},
		LLM: &LLMInsight{
			Summary:    "Working set growth is the leading explanation.",
			RootCause:  "working set growth",
			Confidence: 0.83,
		},
	}

	require.Equal(t, semanticReportFingerprint(reportA), semanticReportFingerprint(reportB))
}

func TestStoreReportRefreshesUnchangedReport(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(30 * time.Second)

	engine := &Engine{
		cfg: Config{
			MaxReports:               4,
			MaxActions:               8,
			SuppressUnchangedReports: true,
			ReportRefreshInterval:    time.Minute,
			PredictiveLogCooldown:    5 * time.Minute,
		},
		reports:           make(map[string][]Report),
		actions:           make(map[string]ActionDecision),
		predictiveLogSeen: make(map[string]time.Time),
	}

	first := Report{
		ID:          "report-a",
		NodeName:    "node-a",
		GeneratedAt: t0,
		Summary:     "Disk latency trend is rising",
		Findings:    []string{"Disk latency trend is rising"},
		Actions: []ActionDecision{{
			ID:       "action-a",
			Type:     "investigate",
			Reason:   "Check disk queue depth",
			Priority: "medium",
			Status:   ActionStatusProposed,
			Safe:     true,
		}},
		Predictions: []predictive.Finding{{
			PredictionID:  "pred-a",
			Metric:        "node_disk_read_time_seconds_total",
			PredictiveSLO: "io_latency",
			Title:         "Predictive IO pressure",
			Severity:      "medium",
			HazardClass:   "storage_contention",
		}},
		Evidence: analysis.EvidencePack{
			Processes: []analysis.ProcessSummary{{PID: 77, Name: "postgres", CPUPercent: 19, RSSBytes: 800_000_000}},
		},
	}
	second := first
	second.ID = "report-b"
	second.GeneratedAt = t1
	second.Actions = []ActionDecision{{
		ID:       "action-b",
		Type:     "investigate",
		Reason:   "Check disk queue depth",
		Priority: "medium",
		Status:   ActionStatusProposed,
		Safe:     true,
	}}
	second.Predictions[0].PredictionID = "pred-b"

	firstDecision := engine.storeReport(first)
	secondDecision := engine.storeReport(second)

	require.True(t, firstDecision.persisted)
	require.False(t, firstDecision.suppressed)
	require.True(t, secondDecision.suppressed)
	require.True(t, secondDecision.refreshed)

	reports := engine.Reports("node-a")
	require.Len(t, reports, 1)
	require.Equal(t, "report-a", reports[0].ID)
	require.Equal(t, t1, reports[0].GeneratedAt)
	require.Equal(t, "action-a", reports[0].Actions[0].ID)

	status := engine.Status()
	require.Equal(t, uint64(1), status.ReportSuppressedTotal)
	require.Equal(t, uint64(1), status.ReportRefreshedTotal)
}

func TestShouldLogPredictiveFindingAppliesCooldown(t *testing.T) {
	engine := &Engine{
		cfg: Config{
			PredictiveLogCooldown: 2 * time.Minute,
		},
		predictiveLogSeen: make(map[string]time.Time),
	}
	finding := predictive.Finding{
		AssetID:          "node-a",
		Metric:           "node_tcp_retransmit_ratio",
		PredictiveSLO:    "fabric_delivery_quality",
		Title:            "Predictive network jitter",
		Severity:         "critical",
		HazardClass:      "network_jitter",
		ControlReference: "SRE-predictive-slo-fabric",
		CurrentValue:     0.08,
		ForecastValue:    0.11,
		BaselineValue:    0.01,
	}
	now := time.Unix(1_700_000_000, 0)

	require.True(t, engine.shouldLogPredictiveFinding(now, finding))
	require.False(t, engine.shouldLogPredictiveFinding(now.Add(30*time.Second), finding))
	require.True(t, engine.shouldLogPredictiveFinding(now.Add(3*time.Minute), finding))
	require.Equal(t, uint64(1), engine.Status().PredictiveLogSuppressedTotal)
}
