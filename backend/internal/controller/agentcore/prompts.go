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

const promptMetricLimit = 24

// PromptInput carries the telemetry context used to build an LLM prompt.
type PromptInput struct {
	Query                string
	NodeName             string
	Generated            time.Time
	TelemetryAgeSeconds  float64
	TelemetryStale       bool
	TelemetryQuality     PromptTelemetryQuality
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
	RetrievalIntent      string
	RetrievalMode        string
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

// PromptTelemetryQuality summarizes freshness and completeness of the evidence bundle.
type PromptTelemetryQuality struct {
	State               string    `json:"state"`
	Partial             bool      `json:"partial,omitempty"`
	CoveragePercent     float64   `json:"coverage_percent,omitempty"`
	Confidence          float64   `json:"confidence,omitempty"`
	SourceMode          string    `json:"source_mode,omitempty"`
	RuntimeMode         string    `json:"runtime_mode,omitempty"`
	LatestCollectionAt  time.Time `json:"latest_collection_at,omitempty"`
	LatestIngestAt      time.Time `json:"latest_ingest_at,omitempty"`
	QueryAt             time.Time `json:"query_at,omitempty"`
	FreshnessAgeSeconds float64   `json:"freshness_age_seconds,omitempty"`
	IngestDelaySeconds  float64   `json:"ingest_delay_seconds,omitempty"`
	MissingSignals      []string  `json:"missing_signals,omitempty"`
	BlindSpots          []string  `json:"blind_spots,omitempty"`
	SafeToAct           bool      `json:"safe_to_act"`
}

// LLMSchema models docs/reference/llm_schema.md for AGENT query calls.
type LLMSchema struct {
	SchemaVersion    string                 `json:"schema_version"`
	GeneratedAt      time.Time              `json:"generated_at"`
	NodeName         string                 `json:"node_name"`
	TelemetryQuality PromptTelemetryQuality `json:"telemetry_quality"`
	Metrics          map[string]float64     `json:"metrics"`
	Trends           map[string]string      `json:"trends,omitempty"`
	Alerts           []string               `json:"alerts,omitempty"`
	Anomalies        []string               `json:"anomalies,omitempty"`
	RAGContext       []string               `json:"rag_context,omitempty"`
	Context          string                 `json:"context"`
	Evidence         LLMEvidence            `json:"evidence"`
}

// LLMEvidence is the compact evidence bundle attached to the schema.
type LLMEvidence struct {
	SchemaVersion    string                 `json:"schema_version"`
	NodeName         string                 `json:"node_name"`
	TelemetryQuality PromptTelemetryQuality `json:"telemetry_quality"`
	Summary          map[string]float64     `json:"summary"`
	TopMetrics       []MetricKV             `json:"top_metrics"`
	GPU              map[string]float64     `json:"gpu"`
	Network          map[string]float64     `json:"network"`
	Disk             map[string]float64     `json:"disk"`
	Memory           map[string]float64     `json:"memory"`
	Processes        []PromptProcess        `json:"processes,omitempty"`
	Logs             []PromptLog            `json:"logs,omitempty"`
	Alerts           []string               `json:"alerts,omitempty"`
	Anomalies        []string               `json:"anomalies,omitempty"`
	Context          string                 `json:"context"`
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
	schema := buildPromptSchema(in)
	raw, _ := json.MarshalIndent(schema, "", "  ")
	ragBlock := "RAG context snippets: none"
	if len(in.RAGSnippets) > 0 {
		ragBlock = "RAG context snippets:\\n- " + strings.Join(in.RAGSnippets, "\\n- ")
	}
	if strings.TrimSpace(in.RetrievalSummary) != "" {
		ragBlock = ragBlock + "\\nRetrieval summary: " + in.RetrievalSummary
	}
	if strings.TrimSpace(in.RetrievalIntent) != "" || strings.TrimSpace(in.RetrievalMode) != "" {
		ragBlock = ragBlock + fmt.Sprintf(
			"\\nRetrieval routing: intent=%s mode=%s",
			firstNonEmpty(in.RetrievalIntent, "unknown"),
			firstNonEmpty(in.RetrievalMode, "unknown"),
		)
	}
	if len(in.RetrievedDocs) > 0 {
		lines := make([]string, 0, minInt(len(in.RetrievedDocs), 3))
		for _, doc := range in.RetrievedDocs {
			title := strings.TrimSpace(firstNonEmpty(doc.Title, doc.SourcePath))
			if title == "" {
				continue
			}
			parts := []string{fmt.Sprintf("%s/%s", firstNonEmpty(doc.KnowledgeType, "knowledge"), firstNonEmpty(doc.CaseType, "reference"))}
			if summary := strings.TrimSpace(doc.Summary); summary != "" {
				parts = append(parts, "summary="+summary)
			}
			if len(doc.LikelyCauses) > 0 {
				parts = append(parts, "causes="+strings.Join(limitPromptStrings(doc.LikelyCauses, 2), "; "))
			}
			if len(doc.RemediationSteps) > 0 {
				parts = append(parts, "steps="+strings.Join(limitPromptStrings(doc.RemediationSteps, 2), "; "))
			}
			lines = append(lines, fmt.Sprintf("- [%s] %s", title, strings.Join(parts, " | ")))
			if len(lines) >= 3 {
				break
			}
		}
		if len(lines) > 0 {
			ragBlock = ragBlock + "\\nRetrieved operational knowledge:\\n" + strings.Join(lines, "\\n")
		}
	}
	return strings.Join([]string{
		BuildAnomalyPrompt(in),
		BuildRCAPrompt(in),
		fmt.Sprintf(
			"Telemetry quality: state=%s age_seconds=%.0f stale=%t coverage=%.0f%% safe_to_act=%t",
			in.TelemetryQuality.State,
			in.TelemetryAgeSeconds,
			in.TelemetryStale,
			in.TelemetryQuality.CoveragePercent,
			in.TelemetryQuality.SafeToAct,
		),
		ragBlock,
		"Telemetry JSON (schema v1):",
		string(raw),
		"Output only JSON with actionable, low-risk guidance first.",
		"Every recommendation must be tied to evidence or clearly marked as a limitation.",
	}, "\n\n")
}

func limitPromptStrings(values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values
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
		SchemaVersion:    schemaVersion,
		NodeName:         in.NodeName,
		TelemetryQuality: in.TelemetryQuality,
		Summary:          summarizeMetrics(in.Metrics, 6),
		TopMetrics:       topMetrics(in.Metrics, 8),
		GPU:              filterMetricsByPrefix(in.Metrics, "node_gpu_"),
		Network:          filterMetricsByPrefix(in.Metrics, "node_network_"),
		Disk:             filterMetricsByPrefix(in.Metrics, "node_disk_"),
		Memory:           filterMetricsByPrefix(in.Metrics, "node_memory_"),
		Processes:        in.Processes,
		Logs:             in.Logs,
		Alerts:           in.Findings,
		Anomalies:        in.Anomalies,
		Context:          context,
	}

	return LLMSchema{
		SchemaVersion:    schemaVersion,
		GeneratedAt:      ts,
		NodeName:         in.NodeName,
		TelemetryQuality: in.TelemetryQuality,
		Metrics:          in.Metrics,
		Trends:           in.Trends,
		Alerts:           in.Findings,
		Anomalies:        in.Anomalies,
		RAGContext:       in.RAGSnippets,
		Context:          context,
		Evidence:         evidence,
	}
}

func buildPromptSchema(in PromptInput) LLMSchema {
	schema := BuildSchema(in)
	schema.Metrics = compactMetricsForPrompt(in.Metrics, promptMetricLimit)
	schema.Evidence.TopMetrics = topMetrics(in.Metrics, 8)
	schema.Evidence.Summary = summarizeMetrics(in.Metrics, 6)
	return schema
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

func compactMetricsForPrompt(metrics map[string]float64, limit int) map[string]float64 {
	if len(metrics) == 0 {
		return nil
	}
	if limit <= 0 || len(metrics) <= limit {
		out := make(map[string]float64, len(metrics))
		for name, value := range metrics {
			out[name] = value
		}
		return out
	}

	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		left := metricPromptPriority(names[i])
		right := metricPromptPriority(names[j])
		if left == right {
			leftValue := abs(metrics[names[i]])
			rightValue := abs(metrics[names[j]])
			if leftValue == rightValue {
				return names[i] < names[j]
			}
			return leftValue > rightValue
		}
		return left < right
	})

	if len(names) > limit {
		names = names[:limit]
	}
	out := make(map[string]float64, len(names))
	for _, name := range names {
		out[name] = metrics[name]
	}
	return out
}

func metricPromptPriority(name string) int {
	switch {
	case name == "node_cpu_usage_percent",
		name == "node_cpu_iowait_percent",
		name == "node_disk_request_latency_p99_seconds",
		name == "node_disk_queue_depth_total",
		name == "node_network_retransmit_ratio_percent",
		name == "node_memory_usage_percent",
		name == "node_memory_Used_bytes",
		name == "node_memory_MemAvailable_bytes",
		name == "collector_spool_backlog_bytes",
		name == "collector_transport_errors_total",
		name == "collector_transport_retries_total",
		name == "collector_telemetry_missing_critical_signals_total":
		return 0
	case strings.HasPrefix(name, "node_pressure_"),
		strings.HasPrefix(name, "probe_core_pressure_"),
		strings.HasPrefix(name, "node_gpu_"):
		return 1
	case strings.HasPrefix(name, "node_cpu_"),
		strings.HasPrefix(name, "node_memory_"),
		strings.HasPrefix(name, "node_disk_"),
		strings.HasPrefix(name, "node_network_"):
		return 2
	case strings.HasPrefix(name, "collector_"):
		return 3
	default:
		return 4
	}
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
