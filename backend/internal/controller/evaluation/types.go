package evaluation

import (
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// ReplayOptions configures one deterministic replay run built on the golden fixtures.
type ReplayOptions struct {
	Scope    eval.Scope
	RepoRoot string
}

// ReplayReport summarizes two back-to-back evaluation runs and their stability.
type ReplayReport struct {
	GeneratedAt    time.Time          `json:"generated_at"`
	Scope          eval.Scope         `json:"scope"`
	Stable         bool               `json:"stable"`
	StabilityScore float64            `json:"stability_score"`
	First          eval.Report        `json:"first"`
	Second         eval.Report        `json:"second"`
	AnomalyDrift   map[string]float64 `json:"anomaly_drift,omitempty"`
	WorkflowDrift  map[string]float64 `json:"workflow_drift,omitempty"`
	RetrievalDrift map[string]float64 `json:"retrieval_drift,omitempty"`
}
