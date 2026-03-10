package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
)

const schemaVersion = "v1"

// PromptInput carries the telemetry context used to build an LLM prompt.
type PromptInput struct {
	Query                string
	NodeName             string
	Generated            time.Time
	TelemetryAgeSeconds  float64
	TelemetryStale       bool
	Metrics              map[string]float64
	Trends               map[string]string
	Findings             []string
	Anomalies            []string
	Processes            []PromptProcess
	Logs                 []PromptLog
	ContextTag           string
	RAGSnippets          []string
	RetrievedDocs        []rag.SearchHit
	RetrievalSummary     string
	RetrievalEvidenceIDs []string
	RetrievalConfidence  float64
}

// PromptProcess is a compact process summary sent to the LLM.
type PromptProcess struct {
	PID       int32   `json:"pid"`
	Name      string  `json:"name"`
	CPU       float64 `json:"cpu_percent"`
	RSSBytes  uint64  `json:"rss_bytes"`
	IOReadBPS float64 `json:"io_read_bps"`
	IOWrtBPS  float64 `json:"io_write_bps"`
}

// PromptLog is a compact log fingerprint summary sent to the LLM.
type PromptLog struct {
	Fingerprint string `json:"fingerprint"`
	Count       uint64 `json:"count"`
	Example     string `json:"example"`
}

// LLMSchema models docs/reference/llm_schema.md for AGENT query calls.
type LLMSchema struct {
	SchemaVersion string             `json:"schema_version"`
	GeneratedAt   time.Time          `json:"generated_at"`
	NodeName      string             `json:"node_name"`
	Metrics       map[string]float64 `json:"metrics"`
	Trends        map[string]string  `json:"trends,omitempty"`
	Alerts        []string           `json:"alerts,omitempty"`
	Anomalies     []string           `json:"anomalies,omitempty"`
	RAGContext    []string           `json:"rag_context,omitempty"`
	Context       string             `json:"context"`
	Evidence      LLMEvidence        `json:"evidence"`
}

// LLMEvidence is the compact evidence bundle attached to the schema.
type LLMEvidence struct {
	SchemaVersion string             `json:"schema_version"`
	NodeName      string             `json:"node_name"`
	Summary       map[string]float64 `json:"summary"`
	TopMetrics    []MetricKV         `json:"top_metrics"`
	GPU           map[string]float64 `json:"gpu"`
	Network       map[string]float64 `json:"network"`
	Disk          map[string]float64 `json:"disk"`
	Memory        map[string]float64 `json:"memory"`
	Processes     []PromptProcess    `json:"processes,omitempty"`
	Logs          []PromptLog        `json:"logs,omitempty"`
	Alerts        []string           `json:"alerts,omitempty"`
	Anomalies     []string           `json:"anomalies,omitempty"`
	Context       string             `json:"context"`
}

// MetricKV is a metric/value pair used for top metric ranking.
type MetricKV struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// BuildSystemPrompt defines strict contract and safety boundaries for the LLM.
func BuildSystemPrompt() string {
	return "You are a senior SRE. Use only provided telemetry facts. " +
		"Never invent metrics or command outputs. " +
		"Return strict JSON with fields: summary, root_cause, confidence, findings, recommendations, actions, evidence, limitations."
}

// BuildUserPrompt builds a concise but explicit RCA/anomaly prompt.
func BuildUserPrompt(in PromptInput) string {
	schema := BuildSchema(in)
	raw, _ := json.MarshalIndent(schema, "", "  ")
	ragBlock := "RAG context snippets: none"
	if len(in.RAGSnippets) > 0 {
		ragBlock = "RAG context snippets:\\n- " + strings.Join(in.RAGSnippets, "\\n- ")
	}
	if strings.TrimSpace(in.RetrievalSummary) != "" {
		ragBlock = ragBlock + "\\nRetrieval summary: " + in.RetrievalSummary
	}
	return strings.Join([]string{
		BuildAnomalyPrompt(in),
		BuildRCAPrompt(in),
		fmt.Sprintf("Telemetry freshness: age_seconds=%.0f stale=%t", in.TelemetryAgeSeconds, in.TelemetryStale),
		ragBlock,
		"Telemetry JSON (schema v1):",
		string(raw),
		"Output only JSON with actionable, low-risk guidance first.",
		"Every recommendation must be tied to evidence or clearly marked as a limitation.",
	}, "\n\n")
}

// BuildAnomalyPrompt frames anomaly detection in plain language.
func BuildAnomalyPrompt(in PromptInput) string {
	return fmt.Sprintf(
		"Question: %q\nExplain anomalies simply. Example style: \"CPU at 90%% is like a clogged pipe; flow backs up.\"",
		in.Query,
	)
}

// BuildRCAPrompt asks for concrete root-cause and remediations.
func BuildRCAPrompt(in PromptInput) string {
	return fmt.Sprintf(
		"Telemetry shows pressure on node %q. Identify likely blockers, rank confidence, and suggest safe fixes first.",
		in.NodeName,
	)
}

// BuildSchema assembles the schema consumed by AGENT LLM calls.
func BuildSchema(in PromptInput) LLMSchema {
	ts := in.Generated
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	context := strings.TrimSpace(in.ContextTag)
	if context == "" {
		context = "AGENT query RCA and anomaly analysis"
	}

	evidence := LLMEvidence{
		SchemaVersion: schemaVersion,
		NodeName:      in.NodeName,
		Summary:       summarizeMetrics(in.Metrics, 6),
		TopMetrics:    topMetrics(in.Metrics, 8),
		GPU:           filterMetricsByPrefix(in.Metrics, "node_gpu_"),
		Network:       filterMetricsByPrefix(in.Metrics, "node_network_"),
		Disk:          filterMetricsByPrefix(in.Metrics, "node_disk_"),
		Memory:        filterMetricsByPrefix(in.Metrics, "node_memory_"),
		Processes:     in.Processes,
		Logs:          in.Logs,
		Alerts:        in.Findings,
		Anomalies:     in.Anomalies,
		Context:       context,
	}

	return LLMSchema{
		SchemaVersion: schemaVersion,
		GeneratedAt:   ts,
		NodeName:      in.NodeName,
		Metrics:       in.Metrics,
		Trends:        in.Trends,
		Alerts:        in.Findings,
		Anomalies:     in.Anomalies,
		RAGContext:    in.RAGSnippets,
		Context:       context,
		Evidence:      evidence,
	}
}

func topMetrics(metrics map[string]float64, limit int) []MetricKV {
	items := make([]MetricKV, 0, len(metrics))
	for name, value := range metrics {
		items = append(items, MetricKV{Name: name, Value: value})
	}
	sort.Slice(items, func(i, j int) bool {
		return abs(items[i].Value) > abs(items[j].Value)
	})
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func summarizeMetrics(metrics map[string]float64, limit int) map[string]float64 {
	items := topMetrics(metrics, limit)
	out := make(map[string]float64, len(items))
	for _, item := range items {
		out[item.Name] = item.Value
	}
	return out
}

func filterMetricsByPrefix(metrics map[string]float64, prefix string) map[string]float64 {
	out := make(map[string]float64)
	for name, value := range metrics {
		if strings.HasPrefix(name, prefix) {
			out[name] = value
		}
	}
	return out
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
