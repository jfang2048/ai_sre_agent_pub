package agent

import (
	"testing"
	"time"

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
