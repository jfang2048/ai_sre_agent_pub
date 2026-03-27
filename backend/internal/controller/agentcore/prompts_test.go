package agent

import (
	"fmt"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"github.com/stretchr/testify/require"
)

func TestBuildSchemaIncludesEvidenceAndTrends(t *testing.T) {
	in := PromptInput{
		Query:     "Why is gpu hot?",
		NodeName:  "collector-a",
		Generated: time.Unix(1700000000, 0).UTC(),
		Metrics: map[string]float64{
			"node_cpu_usage_percent":              87.2,
			"node_gpu_utilization_sm_avg_percent": 94.3,
			"node_gpu_memory_used_total_mib":      28672,
		},
		Trends: map[string]string{
			"node_cpu_usage_percent":              "rising",
			"node_gpu_utilization_sm_avg_percent": "rising",
		},
		Findings:  []string{"GPU saturation detected"},
		Anomalies: []string{"GPU utilization trend is rising"},
	}

	schema := BuildSchema(in)
	require.Equal(t, "v1", schema.SchemaVersion)
	require.Equal(t, "collector-a", schema.NodeName)
	require.Equal(t, "rising", schema.Trends["node_cpu_usage_percent"])
	require.Equal(t, 94.3, schema.Evidence.GPU["node_gpu_utilization_sm_avg_percent"])
	require.NotEmpty(t, schema.Evidence.TopMetrics)
	require.Contains(t, schema.Evidence.Alerts, "GPU saturation detected")
}

func TestBuildUserPromptContainsFeynmanCueAndJSON(t *testing.T) {
	in := PromptInput{
		Query:    "RCA high cpu",
		NodeName: "node-a",
		Metrics: map[string]float64{
			"node_cpu_usage_percent": 91,
		},
	}
	prompt := BuildUserPrompt(in)
	require.Contains(t, prompt, "clogged pipe")
	require.Contains(t, prompt, "\"schema_version\": \"v1\"")
	require.Contains(t, prompt, "\"node_cpu_usage_percent\": 91")
}

func TestBuildUserPromptIncludesStructuredRetrievedKnowledge(t *testing.T) {
	in := PromptInput{
		Query:    "how do I troubleshoot rollout timeout",
		NodeName: "node-a",
		RAGSnippets: []string{
			"[runbook] rollout timeout :: summary=check cache credentials",
		},
		RetrievalSummary: "retrieved 2 knowledge hits across 2 documents (runbook=1, historical_incident=1)",
		RetrievalIntent:  "runbook",
		RetrievalMode:    "hybrid",
		RetrievedDocs: []rag.SearchHit{
			{
				Title:            "Timeout Runbook",
				SourcePath:       "cases/timeout-runbook.md",
				KnowledgeType:    "runbook",
				CaseType:         "runbook",
				Summary:          "Investigate retry rate, deployment timing, and cache credentials.",
				LikelyCauses:     []string{"stale cache credential after rollout"},
				RemediationSteps: []string{"inspect retry rate", "validate cache credentials"},
			},
		},
	}

	prompt := BuildUserPrompt(in)
	require.Contains(t, prompt, "Retrieved operational knowledge")
	require.Contains(t, prompt, "Timeout Runbook")
	require.Contains(t, prompt, "summary=Investigate retry rate, deployment timing, and cache credentials.")
	require.Contains(t, prompt, "causes=stale cache credential after rollout")
	require.Contains(t, prompt, "steps=inspect retry rate; validate cache credentials")
	require.Contains(t, prompt, "Retrieval routing: intent=runbook mode=hybrid")
}

func TestBuildPromptSchemaCompactsMetricMapForLLM(t *testing.T) {
	metrics := map[string]float64{
		"node_cpu_usage_percent":                91,
		"node_memory_usage_percent":             86,
		"node_disk_request_latency_p99_seconds": 0.048,
		"collector_spool_backlog_bytes":         8192,
	}
	for i := 0; i < 40; i++ {
		metrics[fmt.Sprintf("custom_metric_%02d", i)] = float64(i)
	}

	schema := buildPromptSchema(PromptInput{
		Query:    "why is the node slow",
		NodeName: "node-a",
		Metrics:  metrics,
	})

	require.Len(t, schema.Metrics, promptMetricLimit)
	require.Contains(t, schema.Metrics, "node_cpu_usage_percent")
	require.Contains(t, schema.Metrics, "node_memory_usage_percent")
	require.Contains(t, schema.Metrics, "node_disk_request_latency_p99_seconds")
	require.Contains(t, schema.Metrics, "collector_spool_backlog_bytes")
}

func TestBuildQueryServiceRAGRequestInfersIntentAndFilters(t *testing.T) {
	req := buildQueryServiceRAGRequest(
		"how to fix deployment timeout after rollout",
		[]string{"timeout spikes after deployment"},
		nil,
		6,
		6,
		640,
	)
	require.Equal(t, "runbook", req.Intent)
	require.Equal(t, 6, req.TopK)
	require.Contains(t, req.KnowledgeTypes, "runbook")
	require.Contains(t, req.CaseTypes, "runbook")

	req = buildQueryServiceRAGRequest(
		"similar incidents for gpu thermal runaway",
		[]string{"GPU temperature is rising"},
		nil,
		4,
		6,
		640,
	)
	require.Equal(t, "historical_incident", req.Intent)
	require.Contains(t, req.KnowledgeTypes, "historical_incident")
	require.Contains(t, req.CaseTypes, "historical_incident")

	req = buildQueryServiceRAGRequest(
		"why did latency rise after rollout",
		[]string{
			"No critical anomalies detected",
			"Telemetry snapshot is stale (age 420s > threshold 120s)",
			"Telemetry freshness is degraded because the collector is replaying backlog",
			"Disk I/O pressure is elevated",
			"Network retransmits or timeout bursts are active",
		},
		nil,
		4,
		4,
		240,
	)
	require.NotContains(t, req.Query, "No critical anomalies detected")
	require.NotContains(t, req.Query, "Telemetry snapshot is stale")
	require.NotContains(t, req.Query, "Telemetry freshness is degraded")
	require.Contains(t, req.Query, "Disk I/O pressure is elevated")
	require.Contains(t, req.Query, "Network retransmits or timeout bursts are active")
}

func TestBuildQueryServiceRAGRequestIncludesAnomalyHints(t *testing.T) {
	req := buildQueryServiceRAGRequest(
		"why is this node slow",
		[]string{"CPU utilization is above 85%"},
		[]string{"Memory usage is climbing steadily, which raises leak, cache growth, or retry amplification risk"},
		4,
		4,
		320,
	)
	require.Contains(t, req.Query, "CPU utilization is above 85%")
	require.Contains(t, req.Query, "Memory usage is climbing steadily")
}
