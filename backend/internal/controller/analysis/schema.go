package analysis

import (
	"strings"
	"time"
)

// LLMInputSchema is the canonical schema sent to LLMs.
type LLMInputSchema struct {
	SchemaVersion string             `json:"schema_version"`
	GeneratedAt   time.Time          `json:"generated_at"`
	NodeName      string             `json:"node_name"`
	Metrics       map[string]float64 `json:"metrics"`
	Trends        map[string]string  `json:"trends"`
	Alerts        []string           `json:"alerts"`
	Anomalies     []string           `json:"anomalies"`
	Context       string             `json:"context"`
	Evidence      EvidencePack       `json:"evidence"`
}

func buildLLMSchema(nodeName string, metrics map[string]float64, trends map[string]string, alerts []string, anomalies []string, context string, processes []ProcessSummary, logs []LogSummary) LLMInputSchema {
	return LLMInputSchema{
		SchemaVersion: "v1",
		GeneratedAt:   time.Now().UTC(),
		NodeName:      nodeName,
		Metrics:       metrics,
		Trends:        trends,
		Alerts:        alerts,
		Anomalies:     anomalies,
		Context:       context,
		Evidence:      BuildEvidencePack(nodeName, metrics, alerts, anomalies, context, processes, logs),
	}
}

// BuildLLMSchemaForAgent builds a schema for agent reports, optionally embedding context snippets.
func BuildLLMSchemaForAgent(nodeName string, metrics map[string]float64, findings []string, forecasts []string, evidence EvidencePack, snippets []string) LLMInputSchema {
	context := evidence.Context
	if len(snippets) > 0 {
		context = context + "\nContext snippets:\n" + strings.Join(snippets, "\n---\n")
	}
	return LLMInputSchema{
		SchemaVersion: "v1",
		GeneratedAt:   time.Now().UTC(),
		NodeName:      nodeName,
		Metrics:       metrics,
		Trends:        nil,
		Alerts:        findings,
		Anomalies:     forecasts,
		Context:       context,
		Evidence:      evidence,
	}
}
