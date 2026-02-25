package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidents"
)

// IncidentAssessment captures the Agent's judgement over an aggregated context.
type IncidentAssessment struct {
	AlertID         string                      `json:"alert_id"`
	IncidentID      string                      `json:"incident_id"`
	Service         string                      `json:"service"`
	Severity        string                      `json:"severity"`
	Summary         string                      `json:"summary"`
	LikelyCauses    []string                    `json:"likely_causes"`
	Recommendations []string                    `json:"recommendations"`
	Confidence      float64                     `json:"confidence"`
	GeneratedAt     time.Time                   `json:"generated_at"`
	Context         incidents.AggregatedContext `json:"context"`
}

// IngestIncidentContext stores the context and produces an assessment.
func (e *Engine) IngestIncidentContext(ctx incidents.AggregatedContext) IncidentAssessment {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.incidentContexts == nil {
		e.incidentContexts = make(map[string]incidents.AggregatedContext)
	}
	if e.incidentAssessments == nil {
		e.incidentAssessments = make(map[string]IncidentAssessment)
	}

	e.incidentContexts[ctx.AlertID] = ctx
	assessment := e.assessContextLocked(ctx)
	e.incidentAssessments[ctx.AlertID] = assessment
	return assessment
}

// IncidentAssessments returns recent assessments (most recent first).
func (e *Engine) IncidentAssessments(limit int) []IncidentAssessment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]IncidentAssessment, 0, len(e.incidentAssessments))
	for _, a := range e.incidentAssessments {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GeneratedAt.After(out[j].GeneratedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// IncidentAssessment fetches a single assessment by alert ID.
func (e *Engine) IncidentAssessment(alertID string) (IncidentAssessment, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	a, ok := e.incidentAssessments[alertID]
	return a, ok
}

// IncidentContexts returns recent raw contexts (most recent first).
func (e *Engine) IncidentContexts(limit int) []incidents.AggregatedContext {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]incidents.AggregatedContext, 0, len(e.incidentContexts))
	for _, c := range e.incidentContexts {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GeneratedAt.After(out[j].GeneratedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// IncidentContext fetches a raw context.
func (e *Engine) IncidentContext(alertID string) (incidents.AggregatedContext, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ctx, ok := e.incidentContexts[alertID]
	return ctx, ok
}

// assessContextLocked creates a concise judgement for the provided context.
// Caller must hold e.mu.
func (e *Engine) assessContextLocked(ctx incidents.AggregatedContext) IncidentAssessment {
	sev := ctx.Alert.Severity
	service := ctx.Alert.Service
	if service == "" && len(ctx.Services) > 0 {
		service = ctx.Services[0].Service
	}

	summary := fmt.Sprintf("Alert %s on %s (%s) covering %d metrics and %d log scopes",
		ctx.Alert.ID, service, sev, len(ctx.Metrics), len(ctx.Logs))

	likely := ctx.SuspectedCause
	if len(likely) == 0 {
		likely = deriveLikelyCauses(ctx)
	}
	recs := deriveRecommendations(sev, likely, ctx)

	conf := 0.55
	if len(likely) > 0 {
		conf = 0.72
	}
	if sev == "P0" || sev == "critical" {
		conf += 0.05
	}

	return IncidentAssessment{
		AlertID:         ctx.AlertID,
		IncidentID:      ctx.IncidentID,
		Service:         service,
		Severity:        sev,
		Summary:         summary,
		LikelyCauses:    likely,
		Recommendations: recs,
		Confidence:      conf,
		GeneratedAt:     time.Now(),
		Context:         ctx,
	}
}

func deriveLikelyCauses(ctx incidents.AggregatedContext) []string {
	out := make([]string, 0)
	for _, m := range ctx.Metrics {
		for _, s := range m.Symptoms {
			out = append(out, s)
		}
	}
	for _, lf := range ctx.Logs {
		for _, m := range lf.Matches {
			low := strings.ToLower(m.Example)
			switch {
			case strings.Contains(low, "timeout"):
				out = append(out, "timeout downstream dependency")
			case strings.Contains(low, "oom"):
				out = append(out, "out-of-memory")
			case strings.Contains(low, "throttle"):
				out = append(out, "throttling")
			}
		}
	}
	return dedupeStrings(out)
}

func deriveRecommendations(sev string, causes []string, ctx incidents.AggregatedContext) []string {
	recs := make([]string, 0)
	if len(causes) == 0 {
		recs = append(recs, "Review dashboards for the affected service and confirm blast radius.")
	}
	for _, c := range causes {
		switch {
		case strings.Contains(c, "CPU"):
			recs = append(recs, "Check top CPU consumers; consider scaling out.")
		case strings.Contains(c, "memory"):
			recs = append(recs, "Inspect memory spikes and recent deployments; restart leaking pods if safe.")
		case strings.Contains(c, "timeout"):
			recs = append(recs, "Validate downstream dependency availability and latency SLAs.")
		case strings.Contains(c, "throttle"):
			recs = append(recs, "Review throttling limits and pod requests/limits.")
		}
	}

	if sev == "P0" || strings.ToUpper(sev) == "CRITICAL" {
		recs = append(recs, "Page on-call and initiate incident bridge.")
	}
	if len(ctx.ResourceScope) > 0 {
		recs = append(recs, "Scope mitigation to resources: "+ctx.ResourceScope[0].Name)
	}

	return dedupeStrings(recs)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}
