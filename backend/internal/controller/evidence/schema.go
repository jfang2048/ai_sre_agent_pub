package evidence

import "time"

// SchemaVersionV1 is the stable version marker for normalized controller evidence.
const SchemaVersionV1 = "ai_sre_agent/evidence/v1"

// Subject identifies the operational entity the evidence refers to.
type Subject struct {
	CollectorID string `json:"collector_id,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Service     string `json:"service,omitempty"`
	Job         string `json:"job,omitempty"`
	Cluster     string `json:"cluster,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Entity      string `json:"entity,omitempty"`
}

// Provenance describes where the evidence came from and how it was derived.
type Provenance struct {
	Source     string `json:"source,omitempty"`
	Tool       string `json:"tool,omitempty"`
	TrustClass string `json:"trust_class,omitempty"`
	WorkflowID string `json:"workflow_id,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`
}

// RawReference preserves links back to raw IDs, chunks, logs, or audits.
type RawReference struct {
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
	Value string `json:"value,omitempty"`
}

// Record is the versioned normalized evidence envelope used by RCA and audit paths.
type Record struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Category      string            `json:"category,omitempty"`
	Summary       string            `json:"summary"`
	Severity      string            `json:"severity,omitempty"`
	Status        string            `json:"status,omitempty"`
	Confidence    float64           `json:"confidence,omitempty"`
	Timestamp     time.Time         `json:"timestamp,omitempty"`
	Subject       *Subject          `json:"subject,omitempty"`
	MetricName    string            `json:"metric_name,omitempty"`
	Value         *float64          `json:"value,omitempty"`
	Baseline      *float64          `json:"baseline,omitempty"`
	DeltaPercent  *float64          `json:"delta_percent,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Provenance    *Provenance       `json:"provenance,omitempty"`
	RawReferences []RawReference    `json:"raw_references,omitempty"`
	DerivedFrom   []string          `json:"derived_from,omitempty"`
}

// Float64Ptr returns a stable pointer for optional numeric evidence fields.
func Float64Ptr(v float64) *float64 {
	return &v
}

// CloneRecords deep-copies evidence records for safe storage and retrieval.
func CloneRecords(in []Record) []Record {
	if len(in) == 0 {
		return nil
	}
	out := make([]Record, len(in))
	for i, rec := range in {
		out[i] = rec
		if rec.Subject != nil {
			subject := *rec.Subject
			out[i].Subject = &subject
		}
		if rec.Provenance != nil {
			provenance := *rec.Provenance
			out[i].Provenance = &provenance
		}
		if len(rec.Attributes) > 0 {
			out[i].Attributes = make(map[string]string, len(rec.Attributes))
			for key, value := range rec.Attributes {
				out[i].Attributes[key] = value
			}
		}
		if len(rec.RawReferences) > 0 {
			out[i].RawReferences = append([]RawReference(nil), rec.RawReferences...)
		}
		if len(rec.DerivedFrom) > 0 {
			out[i].DerivedFrom = append([]string(nil), rec.DerivedFrom...)
		}
		if rec.Value != nil {
			out[i].Value = Float64Ptr(*rec.Value)
		}
		if rec.Baseline != nil {
			out[i].Baseline = Float64Ptr(*rec.Baseline)
		}
		if rec.DeltaPercent != nil {
			out[i].DeltaPercent = Float64Ptr(*rec.DeltaPercent)
		}
	}
	return out
}
