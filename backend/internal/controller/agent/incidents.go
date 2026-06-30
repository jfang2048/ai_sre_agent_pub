package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidents"
)

const (
	maxSignalItems    = 24
	maxEvidenceItems  = 20
	maxCorrelationNum = 6
)

// IncidentAssessment captures the Agent's judgement over an aggregated context.
type IncidentAssessment struct {
	AlertID          string                      `json:"alert_id"`
	IncidentID       string                      `json:"incident_id"`
	Service          string                      `json:"service"`
	Severity         string                      `json:"severity"`
	Summary          string                      `json:"summary"`
	LikelyCauses     []string                    `json:"likely_causes"`
	Recommendations  []string                    `json:"recommendations"`
	Confidence       float64                     `json:"confidence"`
	GeneratedAt      time.Time                   `json:"generated_at"`
	Context          incidents.AggregatedContext `json:"context"`
	Workflow         []IncidentWorkflowStage     `json:"workflow"`
	Signals          []IncidentSignal            `json:"signals"`
	ContextSummary   IncidentContextSummary      `json:"context_summary"`
	Correlations     []IncidentCorrelation       `json:"correlations"`
	Diagnosis        IncidentDiagnosis           `json:"diagnosis"`
	Evidence         []IncidentEvidence          `json:"evidence"`
	NextActions      []RecommendationStep        `json:"next_actions"`
	Runbooks         []RunbookLink               `json:"runbooks"`
	AutomationPlan   IncidentAutomationPlan      `json:"automation_plan"`
	AssessmentSource string                      `json:"assessment_source"`
}

// IncidentWorkflowStage captures one deterministic Agent stage.
type IncidentWorkflowStage struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary"`
	GeneratedAt time.Time `json:"generated_at"`
}

// IncidentSignal is a normalized signal extracted for reasoning.
type IncidentSignal struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Scope     string    `json:"scope,omitempty"`
	Name      string    `json:"name"`
	Value     string    `json:"value,omitempty"`
	Severity  string    `json:"severity,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Details   string    `json:"details,omitempty"`
}

// ContextChange records a recent deploy/config drift hint.
type ContextChange struct {
	Source string `json:"source"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

// IncidentContextSummary is a compact scope/timeline envelope.
type IncidentContextSummary struct {
	Service         string          `json:"service"`
	WindowStart     time.Time       `json:"window_start"`
	WindowEnd       time.Time       `json:"window_end"`
	ServiceCount    int             `json:"service_count"`
	ResourceCount   int             `json:"resource_count"`
	MetricScopes    int             `json:"metric_scopes"`
	LogScopes       int             `json:"log_scopes"`
	KubernetesScope bool            `json:"kubernetes_scope"`
	RecentChanges   []ContextChange `json:"recent_changes,omitempty"`
}

// IncidentCorrelation represents a cross-signal hypothesis.
type IncidentCorrelation struct {
	ID          string   `json:"id"`
	Summary     string   `json:"summary"`
	Score       float64  `json:"score"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// IncidentDiagnosis is the structured RCA result.
type IncidentDiagnosis struct {
	ProbableRootCause string   `json:"probable_root_cause"`
	Alternatives      []string `json:"alternatives,omitempty"`
	Confidence        float64  `json:"confidence"`
	BlastRadius       []string `json:"blast_radius,omitempty"`
}

// IncidentEvidence tracks traceable proof points used in diagnosis.
type IncidentEvidence struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Source     string    `json:"source"`
	Scope      string    `json:"scope,omitempty"`
	Summary    string    `json:"summary"`
	Confidence float64   `json:"confidence,omitempty"`
	Timestamp  time.Time `json:"timestamp,omitempty"`
}

// RunbookLink binds recommendations to concrete runbook material.
type RunbookLink struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Source string `json:"source"`
}

// RecommendationStep is an actionable next step.
type RecommendationStep struct {
	ID               string `json:"id"`
	Priority         string `json:"priority"`
	Summary          string `json:"summary"`
	Details          string `json:"details,omitempty"`
	Safe             bool   `json:"safe"`
	RequiresApproval bool   `json:"requires_approval"`
	Reversible       bool   `json:"reversible"`
	RunbookID        string `json:"runbook_id,omitempty"`
}

// IncidentAutomationAction is one guarded executable action.
type IncidentAutomationAction struct {
	ID                    string     `json:"id"`
	Type                  string     `json:"type"`
	Description           string     `json:"description"`
	ExecutionLevel        string     `json:"execution_level,omitempty"`
	Preconditions         []string   `json:"preconditions,omitempty"`
	Safe                  bool       `json:"safe"`
	RequiresApproval      bool       `json:"requires_approval"`
	Reversible            bool       `json:"reversible"`
	DryRunDefault         bool       `json:"dry_run_default"`
	BlastRadius           string     `json:"blast_radius,omitempty"`
	IdempotencyNote       string     `json:"idempotency_note,omitempty"`
	Timeout               string     `json:"timeout,omitempty"`
	RollbackPlan          string     `json:"rollback_plan,omitempty"`
	EvidenceIDs           []string   `json:"evidence_ids,omitempty"`
	OperatorJustification string     `json:"operator_justification,omitempty"`
	Guard                 string     `json:"guard,omitempty"`
	RunbookURL            string     `json:"runbook_url,omitempty"`
	LastStatus            string     `json:"last_status,omitempty"`
	LastMessage           string     `json:"last_message,omitempty"`
	LastExecutedAt        *time.Time `json:"last_executed_at,omitempty"`
}

// IncidentAutomationPlan collects executable checks/remediations.
type IncidentAutomationPlan struct {
	Enabled bool                       `json:"enabled"`
	Mode    string                     `json:"mode"`
	Actions []IncidentAutomationAction `json:"actions,omitempty"`
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
	if e.incidentActionAudits == nil {
		e.incidentActionAudits = make(map[string][]IncidentActionAuditRecord)
	}
	if e.incidentActionApprovals == nil {
		e.incidentActionApprovals = make(map[string]IncidentActionApprovalRecord)
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
	now := time.Now().UTC()
	sev := normalizeSeverity(ctx.Alert.Severity)
	service := ctx.Alert.Service
	if service == "" && len(ctx.Services) > 0 {
		service = ctx.Services[0].Service
	}
	if service == "" {
		service = "unknown-service"
	}

	likely := ctx.SuspectedCause
	if len(likely) == 0 {
		likely = deriveLikelyCauses(ctx)
	}
	changes := extractRecentChanges(ctx.Alert)
	signals := buildIncidentSignals(ctx, likely, sev, now, changes)
	evidence := buildIncidentEvidence(ctx, changes, now)
	correlations := deriveCorrelations(likely, changes, evidence)
	diagnosis := buildDiagnosis(service, sev, likely, correlations, ctx, evidence)
	runbooks := deriveRunbooks(ctx, likely)
	nextActions := deriveRecommendationSteps(sev, likely, ctx, runbooks)
	recs := recommendationsFromSteps(nextActions)
	automation := buildAutomationPlan(ctx, sev, runbooks, evidence, diagnosis)
	workflow := buildWorkflow(sev, ctx, diagnosis, correlations, evidence, nextActions, automation, now)
	contextSummary := buildContextSummary(ctx, service, changes)
	summary := fmt.Sprintf("%s incident on %s: %s", strings.ToUpper(sev), service, diagnosis.ProbableRootCause)
	if diagnosis.ProbableRootCause == "" {
		summary = fmt.Sprintf("%s incident on %s: context captured with %d metric scopes and %d log scopes",
			strings.ToUpper(sev), service, len(ctx.Metrics), len(ctx.Logs))
	}

	return IncidentAssessment{
		AlertID:          ctx.AlertID,
		IncidentID:       ctx.IncidentID,
		Service:          service,
		Severity:         sev,
		Summary:          summary,
		LikelyCauses:     likely,
		Recommendations:  recs,
		Confidence:       diagnosis.Confidence,
		GeneratedAt:      now,
		Context:          ctx,
		Workflow:         workflow,
		Signals:          signals,
		ContextSummary:   contextSummary,
		Correlations:     correlations,
		Diagnosis:        diagnosis,
		Evidence:         evidence,
		NextActions:      nextActions,
		Runbooks:         runbooks,
		AutomationPlan:   automation,
		AssessmentSource: "deterministic-agent-workflow",
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
			case strings.Contains(low, "connection refused"):
				out = append(out, "connection refused by dependency")
			case strings.Contains(low, "crashloopbackoff"), strings.Contains(low, "panic"):
				out = append(out, "repeated process crash")
			}
		}
	}
	for _, change := range extractRecentChanges(ctx.Alert) {
		if strings.Contains(strings.ToLower(change.Key), "deploy") || strings.Contains(strings.ToLower(change.Key), "version") {
			out = append(out, "recent deployment/configuration change")
		}
	}
	if len(out) == 0 {
		out = append(out, "insufficient evidence for deterministic root cause")
	}
	return dedupeStrings(out)
}

func recommendationsFromSteps(steps []RecommendationStep) []string {
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		if strings.TrimSpace(step.Summary) != "" {
			out = append(out, step.Summary)
		}
	}
	return dedupeStrings(out)
}

func deriveRecommendationSteps(sev string, causes []string, ctx incidents.AggregatedContext, runbooks []RunbookLink) []RecommendationStep {
	priority := severityToPriority(sev)
	steps := []RecommendationStep{
		{
			ID:         "confirm-blast-radius",
			Priority:   priority,
			Summary:    "Confirm blast radius using metrics, logs, and affected resources.",
			Details:    "Validate that symptoms are limited to the scoped service before mitigation.",
			Safe:       true,
			Reversible: true,
		},
	}

	for _, cause := range dedupeStrings(causes) {
		lower := strings.ToLower(cause)
		switch {
		case strings.Contains(lower, "cpu"):
			steps = append(steps, RecommendationStep{
				ID:         "cpu-hotspot-check",
				Priority:   priority,
				Summary:    "Inspect top CPU consumers and evaluate horizontal scale-out.",
				Details:    "Review process-level CPU and saturating threads before restart operations.",
				Safe:       true,
				Reversible: true,
				RunbookID:  findRunbookID(runbooks, "high-cpu"),
			})
		case strings.Contains(lower, "memory"), strings.Contains(lower, "oom"):
			steps = append(steps, RecommendationStep{
				ID:         "memory-leak-triage",
				Priority:   priority,
				Summary:    "Correlate memory pressure with recent rollout and leaking workloads.",
				Details:    "If safe, restart only confirmed leaking pods and monitor recovery slope.",
				Safe:       false,
				Reversible: true,
				RunbookID:  findRunbookID(runbooks, "high-gpu-memory"),
			})
		case strings.Contains(lower, "timeout"), strings.Contains(lower, "refused"):
			steps = append(steps, RecommendationStep{
				ID:         "dependency-latency-check",
				Priority:   priority,
				Summary:    "Validate downstream dependency availability and latency budget.",
				Details:    "Check recent error-rate changes and network saturation before failover.",
				Safe:       true,
				Reversible: true,
			})
		case strings.Contains(lower, "throttle"):
			steps = append(steps, RecommendationStep{
				ID:         "throttle-budget-review",
				Priority:   priority,
				Summary:    "Review throttling budgets and workload requests/limits.",
				Details:    "Tune limits conservatively and verify queue depth response.",
				Safe:       true,
				Reversible: true,
			})
		case strings.Contains(lower, "deployment"), strings.Contains(lower, "configuration"):
			steps = append(steps, RecommendationStep{
				ID:               "change-risk-check",
				Priority:         priority,
				Summary:          "Validate the latest deployment/configuration change impact.",
				Details:          "Compare pre/post-change errors and prepare rollback decision.",
				Safe:             false,
				RequiresApproval: true,
				Reversible:       true,
			})
		}
	}

	if sev == "p0" || sev == "critical" {
		steps = append(steps, RecommendationStep{
			ID:         "incident-bridge",
			Priority:   "P0",
			Summary:    "Page on-call responders and open incident bridge immediately.",
			Details:    "Track diagnosis confidence and mitigation impact every 5 minutes.",
			Safe:       true,
			Reversible: true,
		})
	}

	if len(ctx.ResourceScope) > 0 {
		steps = append(steps, RecommendationStep{
			ID:         "scope-target",
			Priority:   priority,
			Summary:    "Limit mitigation scope to the impacted resources first.",
			Details:    "First target: " + ctx.ResourceScope[0].Name,
			Safe:       true,
			Reversible: true,
		})
	}

	return dedupeRecommendationSteps(steps)
}

func buildIncidentSignals(ctx incidents.AggregatedContext, causes []string, severity string, now time.Time, changes []ContextChange) []IncidentSignal {
	signals := make([]IncidentSignal, 0, maxSignalItems)
	appendSignal := func(sig IncidentSignal) {
		if len(signals) >= maxSignalItems {
			return
		}
		signals = append(signals, sig)
	}

	appendSignal(IncidentSignal{
		ID:        "signal-alert",
		Source:    "alert",
		Scope:     ctx.Alert.Service,
		Name:      nonEmpty(ctx.Alert.Title, "incident alert"),
		Value:     nonEmpty(ctx.Alert.ID, ctx.AlertID),
		Severity:  severity,
		Timestamp: chooseNonZeroTime(ctx.Alert.StartsAt, now),
		Details:   "primary trigger",
	})

	for i, metric := range ctx.Metrics {
		appendSignal(IncidentSignal{
			ID:      fmt.Sprintf("signal-metric-%d", i+1),
			Source:  "metric",
			Scope:   metric.Scope,
			Name:    nonEmpty(metric.Query, "metric-symptom"),
			Value:   fmt.Sprintf("symptoms=%d", len(metric.Symptoms)),
			Details: nonEmpty(metric.AnomalyHint, strings.Join(metric.Symptoms, "; ")),
		})
	}

	for i, logFinding := range ctx.Logs {
		example := ""
		if len(logFinding.Matches) > 0 {
			example = truncateText(logFinding.Matches[0].Example, 120)
		}
		appendSignal(IncidentSignal{
			ID:      fmt.Sprintf("signal-log-%d", i+1),
			Source:  "log",
			Scope:   logFinding.Scope,
			Name:    nonEmpty(logFinding.Query, "log-pattern"),
			Value:   fmt.Sprintf("matches=%d", len(logFinding.Matches)),
			Details: example,
		})
	}

	for i, change := range changes {
		appendSignal(IncidentSignal{
			ID:      fmt.Sprintf("signal-change-%d", i+1),
			Source:  change.Source,
			Name:    change.Key,
			Value:   change.Value,
			Details: "recent change metadata",
		})
	}

	for i, cause := range causes {
		appendSignal(IncidentSignal{
			ID:      fmt.Sprintf("signal-cause-%d", i+1),
			Source:  "agent",
			Name:    "cause-hypothesis",
			Value:   cause,
			Details: "derived from correlated context",
		})
	}
	return signals
}

func buildContextSummary(ctx incidents.AggregatedContext, service string, changes []ContextChange) IncidentContextSummary {
	return IncidentContextSummary{
		Service:         service,
		WindowStart:     ctx.Window.Start,
		WindowEnd:       ctx.Window.End,
		ServiceCount:    len(ctx.Services),
		ResourceCount:   len(ctx.ResourceScope),
		MetricScopes:    len(ctx.Metrics),
		LogScopes:       len(ctx.Logs),
		KubernetesScope: ctx.Kubernetes != nil,
		RecentChanges:   changes,
	}
}

func buildIncidentEvidence(ctx incidents.AggregatedContext, changes []ContextChange, now time.Time) []IncidentEvidence {
	evidence := make([]IncidentEvidence, 0, maxEvidenceItems)
	appendEvidence := func(item IncidentEvidence) {
		if len(evidence) >= maxEvidenceItems {
			return
		}
		evidence = append(evidence, item)
	}

	appendEvidence(IncidentEvidence{
		ID:         "evidence-alert",
		Kind:       "alert",
		Source:     "analysis",
		Scope:      ctx.Alert.Service,
		Summary:    nonEmpty(ctx.Alert.Title, "incident alert fired"),
		Confidence: 0.85,
		Timestamp:  chooseNonZeroTime(ctx.Alert.StartsAt, now),
	})

	for i, metric := range ctx.Metrics {
		appendEvidence(IncidentEvidence{
			ID:         fmt.Sprintf("evidence-metric-%d", i+1),
			Kind:       "metric",
			Source:     "monitoring",
			Scope:      metric.Scope,
			Summary:    nonEmpty(metric.AnomalyHint, strings.Join(metric.Symptoms, "; ")),
			Confidence: 0.75,
			Timestamp:  now,
		})
	}

	for i, logFinding := range ctx.Logs {
		detail := ""
		if len(logFinding.Matches) > 0 {
			detail = truncateText(logFinding.Matches[0].Example, 140)
		}
		appendEvidence(IncidentEvidence{
			ID:         fmt.Sprintf("evidence-log-%d", i+1),
			Kind:       "log",
			Source:     "logindex",
			Scope:      logFinding.Scope,
			Summary:    nonEmpty(detail, fmt.Sprintf("%d grouped log matches", len(logFinding.Matches))),
			Confidence: 0.7,
			Timestamp:  now,
		})
	}

	for i, change := range changes {
		appendEvidence(IncidentEvidence{
			ID:         fmt.Sprintf("evidence-change-%d", i+1),
			Kind:       "change",
			Source:     change.Source,
			Summary:    fmt.Sprintf("%s=%s", change.Key, change.Value),
			Confidence: 0.62,
			Timestamp:  now,
		})
	}

	if ctx.Kubernetes != nil {
		appendEvidence(IncidentEvidence{
			ID:         "evidence-kubernetes",
			Kind:       "kubernetes",
			Source:     "k8s",
			Scope:      ctx.Kubernetes.Namespace,
			Summary:    fmt.Sprintf("workloads=%d nodes=%d", len(ctx.Kubernetes.Workloads), len(ctx.Kubernetes.Nodes)),
			Confidence: 0.68,
			Timestamp:  now,
		})
	}

	return evidence
}

func deriveCorrelations(causes []string, changes []ContextChange, evidence []IncidentEvidence) []IncidentCorrelation {
	correlations := make([]IncidentCorrelation, 0, maxCorrelationNum)
	hasCause := func(tokens ...string) bool {
		for _, cause := range causes {
			lower := strings.ToLower(cause)
			for _, token := range tokens {
				if strings.Contains(lower, token) {
					return true
				}
			}
		}
		return false
	}

	hasChange := len(changes) > 0
	if hasCause("cpu") && hasCause("timeout", "refused", "network") {
		correlations = append(correlations, IncidentCorrelation{
			ID:          "corr-cpu-latency",
			Summary:     "CPU saturation aligns with downstream latency/timeout symptoms.",
			Score:       0.79,
			EvidenceIDs: evidenceIDsByKinds(evidence, "metric", "log"),
		})
	}
	if hasCause("memory", "oom") {
		correlations = append(correlations, IncidentCorrelation{
			ID:          "corr-memory-instability",
			Summary:     "Memory pressure correlates with instability and process restarts.",
			Score:       0.77,
			EvidenceIDs: evidenceIDsByKinds(evidence, "metric", "log", "kubernetes"),
		})
	}
	if hasChange && hasCause("timeout", "oom", "throttle", "deployment", "configuration") {
		correlations = append(correlations, IncidentCorrelation{
			ID:          "corr-change-regression",
			Summary:     "Recent deployment/configuration change correlates with observed regressions.",
			Score:       0.74,
			EvidenceIDs: evidenceIDsByKinds(evidence, "change", "metric", "log"),
		})
	}
	if len(correlations) == 0 && len(causes) > 0 {
		correlations = append(correlations, IncidentCorrelation{
			ID:          "corr-primary",
			Summary:     "Primary hypothesis derived from dominant symptom cluster.",
			Score:       0.62,
			EvidenceIDs: evidenceIDsByKinds(evidence, "metric", "log"),
		})
	}
	if len(correlations) > maxCorrelationNum {
		correlations = correlations[:maxCorrelationNum]
	}
	return correlations
}

func buildDiagnosis(
	service string,
	severity string,
	causes []string,
	correlations []IncidentCorrelation,
	ctx incidents.AggregatedContext,
	evidence []IncidentEvidence,
) IncidentDiagnosis {
	probable := ""
	if len(causes) > 0 {
		probable = causes[0]
	}
	if probable == "" {
		probable = "insufficient evidence for deterministic root cause"
	}
	alternatives := make([]string, 0, 3)
	for _, cause := range causes[1:] {
		if len(alternatives) >= 3 {
			break
		}
		alternatives = append(alternatives, cause)
	}

	conf := 0.42
	conf += 0.07 * float64(len(causes))
	conf += 0.05 * float64(len(correlations))
	conf += 0.02 * float64(len(evidence))
	if severity == "p0" || severity == "critical" {
		conf += 0.04
	}
	conf = clamp(conf, 0.35, 0.95)

	blast := make([]string, 0, len(ctx.Services)+len(ctx.ResourceScope)+1)
	blast = append(blast, service)
	for _, impact := range ctx.Services {
		if impact.Service != "" {
			blast = append(blast, impact.Service)
		}
	}
	for _, resource := range ctx.ResourceScope {
		if resource.Name != "" {
			blast = append(blast, resource.Name)
		}
	}

	return IncidentDiagnosis{
		ProbableRootCause: probable,
		Alternatives:      dedupeStrings(alternatives),
		Confidence:        conf,
		BlastRadius:       dedupeStrings(blast),
	}
}

func deriveRunbooks(ctx incidents.AggregatedContext, causes []string) []RunbookLink {
	runbooks := make([]RunbookLink, 0, 6)
	appendRunbook := func(item RunbookLink) {
		if strings.TrimSpace(item.URL) == "" {
			return
		}
		if strings.TrimSpace(item.ID) == "" {
			item.ID = sanitizeToken(item.Title)
		}
		runbooks = append(runbooks, item)
	}

	annotationKeys := []string{"runbook", "runbook_url", "playbook", "wiki", "doc"}
	for _, key := range annotationKeys {
		if value := strings.TrimSpace(ctx.Alert.Annotations[key]); value != "" {
			appendRunbook(RunbookLink{
				ID:     sanitizeToken(key + "-" + value),
				Title:  fmt.Sprintf("Alert annotation %s", key),
				URL:    value,
				Source: "alert-annotation",
			})
		}
	}

	for _, cause := range causes {
		lower := strings.ToLower(cause)
		switch {
		case strings.Contains(lower, "cpu"):
			appendRunbook(RunbookLink{ID: "playbook-high-cpu", Title: "Playbook high-cpu", URL: "configs/agent_playbooks.yaml#high-cpu", Source: "policy"})
		case strings.Contains(lower, "memory"), strings.Contains(lower, "oom"):
			appendRunbook(RunbookLink{ID: "playbook-high-gpu-memory", Title: "Playbook high-gpu-memory", URL: "configs/agent_playbooks.yaml#high-gpu-memory", Source: "policy"})
		case strings.Contains(lower, "throttle"), strings.Contains(lower, "gpu"):
			appendRunbook(RunbookLink{ID: "playbook-high-gpu-sm", Title: "Playbook high-gpu-sm", URL: "configs/agent_playbooks.yaml#high-gpu-sm", Source: "policy"})
		}
	}

	return dedupeRunbooks(runbooks)
}

func buildAutomationPlan(
	ctx incidents.AggregatedContext,
	sev string,
	runbooks []RunbookLink,
	evidence []IncidentEvidence,
	diagnosis IncidentDiagnosis,
) IncidentAutomationPlan {
	blastRadius := incidentAutomationBlastRadius(ctx, diagnosis)
	evidenceIDs := incidentAutomationEvidenceIDs(evidence, 4)
	actions := []IncidentAutomationAction{
		{
			ID:                    incidentActionID(ctx.AlertID, "check-metrics"),
			Type:                  "diagnostic_check_metrics",
			Description:           "Re-evaluate scoped metric symptoms and anomaly hints.",
			ExecutionLevel:        "auto_execute",
			Preconditions:         []string{"collector and controller data are fresh", "incident scope still matches affected metrics"},
			Safe:                  true,
			Reversible:            true,
			DryRunDefault:         false,
			BlastRadius:           "read-only diagnostic scope",
			IdempotencyNote:       "read-only repeatable check",
			Timeout:               "30s",
			RollbackPlan:          "no rollback required",
			EvidenceIDs:           append([]string(nil), evidenceIDs...),
			OperatorJustification: "confirm the current metric picture before taking any production-changing action",
			Guard:                 "read-only check",
		},
		{
			ID:                    incidentActionID(ctx.AlertID, "check-logs"),
			Type:                  "diagnostic_check_logs",
			Description:           "Re-sample recent error logs for burst confirmation.",
			ExecutionLevel:        "auto_execute",
			Preconditions:         []string{"log scope still matches the affected service"},
			Safe:                  true,
			Reversible:            true,
			DryRunDefault:         false,
			BlastRadius:           "read-only diagnostic scope",
			IdempotencyNote:       "read-only repeatable check",
			Timeout:               "30s",
			RollbackPlan:          "no rollback required",
			EvidenceIDs:           append([]string(nil), evidenceIDs...),
			OperatorJustification: "validate whether failure bursts are still active before escalation",
			Guard:                 "read-only check",
		},
	}
	if ctx.Kubernetes != nil {
		actions = append(actions, IncidentAutomationAction{
			ID:                    incidentActionID(ctx.AlertID, "check-kubernetes"),
			Type:                  "diagnostic_check_kubernetes",
			Description:           "Validate workload status in scoped namespace.",
			ExecutionLevel:        "auto_execute",
			Preconditions:         []string{"kubernetes incident context is present"},
			Safe:                  true,
			Reversible:            true,
			DryRunDefault:         false,
			BlastRadius:           "read-only namespace scope",
			IdempotencyNote:       "read-only repeatable check",
			Timeout:               "45s",
			RollbackPlan:          "no rollback required",
			EvidenceIDs:           append([]string(nil), evidenceIDs...),
			OperatorJustification: "confirm workload health before attempting remediation",
			Guard:                 "read-only check",
		})
		actions = append(actions, IncidentAutomationAction{
			ID:                    incidentActionID(ctx.AlertID, "check-rollout-health"),
			Type:                  "diagnostic_rollout_health",
			Description:           "Inspect rollout status for impacted Kubernetes workloads.",
			ExecutionLevel:        "auto_execute",
			Preconditions:         []string{"kubernetes rollout metadata is available"},
			Safe:                  true,
			Reversible:            true,
			DryRunDefault:         false,
			BlastRadius:           "read-only namespace scope",
			IdempotencyNote:       "read-only repeatable check",
			Timeout:               "45s",
			RollbackPlan:          "no rollback required",
			EvidenceIDs:           append([]string(nil), evidenceIDs...),
			OperatorJustification: "check whether the incident aligns with a recent rollout before restarting anything",
			Guard:                 "read-only check",
		})
		actions = append(actions, IncidentAutomationAction{
			ID:                    incidentActionID(ctx.AlertID, "check-node-pressure"),
			Type:                  "diagnostic_node_pressure",
			Description:           "Analyze node pressure (CPU/memory/io/pending pods) in incident scope.",
			ExecutionLevel:        "auto_execute",
			Preconditions:         []string{"node scope is still relevant to the incident"},
			Safe:                  true,
			Reversible:            true,
			DryRunDefault:         false,
			BlastRadius:           "read-only node scope",
			IdempotencyNote:       "read-only repeatable check",
			Timeout:               "45s",
			RollbackPlan:          "no rollback required",
			EvidenceIDs:           append([]string(nil), evidenceIDs...),
			OperatorJustification: "verify node saturation before any workload move or restart",
			Guard:                 "read-only check",
		})
	}
	if len(ctx.ResourceScope) > 0 {
		unsafeAction := IncidentAutomationAction{
			ID:                    incidentActionID(ctx.AlertID, "targeted-restart"),
			Type:                  "targeted_restart_candidate",
			Description:           "Prepare targeted restart for affected workload (manual approval required).",
			ExecutionLevel:        "approval_required",
			Preconditions:         []string{"verify current blast radius", "run diagnostics first", "obtain operator approval"},
			Safe:                  false,
			RequiresApproval:      true,
			Reversible:            true,
			DryRunDefault:         true,
			BlastRadius:           blastRadius,
			IdempotencyNote:       "not guaranteed idempotent; repeat only after validating the previous restart outcome",
			Timeout:               "2m",
			RollbackPlan:          "revert to controlled rollout or restore previous healthy replica set if restart worsens impact",
			EvidenceIDs:           append([]string(nil), evidenceIDs...),
			OperatorJustification: "targeted restart changes live service state and should only proceed after scoped evidence review",
			Guard:                 "approval token + dry-run default",
		}
		if link := findRunbookURL(runbooks); link != "" {
			unsafeAction.RunbookURL = link
		}
		actions = append(actions, unsafeAction)
		if ctx.Kubernetes != nil {
			controlAction := IncidentAutomationAction{
				ID:                    incidentActionID(ctx.AlertID, "restart-pod-controlled"),
				Type:                  "restart_pod_controlled",
				Description:           "Optional controlled pod restart (disabled unless explicitly enabled).",
				ExecutionLevel:        "approval_required",
				Preconditions:         []string{"SRE_AGENT_K8S_REMEDIATION_ENABLED=true", "approval receipt is valid", "rollback target is known"},
				Safe:                  false,
				RequiresApproval:      true,
				Reversible:            true,
				DryRunDefault:         true,
				BlastRadius:           blastRadius,
				IdempotencyNote:       "repeat only after rollout status and replacement pod health are verified",
				Timeout:               "2m",
				RollbackPlan:          "roll back by restoring the previous stable pod or controller state",
				EvidenceIDs:           append([]string(nil), evidenceIDs...),
				OperatorJustification: "controlled restart is a last-mile mitigation and remains disabled unless operators explicitly enable it",
				Guard:                 "approval token + dry-run default + SRE_AGENT_K8S_REMEDIATION_ENABLED",
			}
			if link := findRunbookURL(runbooks); link != "" {
				controlAction.RunbookURL = link
			}
			actions = append(actions, controlAction)
		}
	}
	if sev == "p0" || sev == "critical" {
		actions = append(actions, IncidentAutomationAction{
			ID:                    incidentActionID(ctx.AlertID, "incident-bridge-checklist"),
			Type:                  "incident_bridge_checklist",
			Description:           "Generate incident bridge checklist for responders.",
			ExecutionLevel:        "auto_execute",
			Preconditions:         []string{"incident severity still warrants coordinated response"},
			Safe:                  true,
			Reversible:            true,
			DryRunDefault:         false,
			BlastRadius:           "coordination only",
			IdempotencyNote:       "repeatable coordination checklist",
			Timeout:               "30s",
			RollbackPlan:          "no rollback required",
			EvidenceIDs:           append([]string(nil), evidenceIDs...),
			OperatorJustification: "align responders before invasive changes",
			Guard:                 "read-only check",
		})
	}
	return IncidentAutomationPlan{
		Enabled: len(actions) > 0,
		Mode:    "guarded",
		Actions: actions,
	}
}

func incidentAutomationEvidenceIDs(evidence []IncidentEvidence, limit int) []string {
	if limit <= 0 || len(evidence) == 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for _, item := range evidence {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		out = append(out, item.ID)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func incidentAutomationBlastRadius(ctx incidents.AggregatedContext, diagnosis IncidentDiagnosis) string {
	if len(diagnosis.BlastRadius) > 0 {
		return strings.Join(diagnosis.BlastRadius, ", ")
	}
	if len(ctx.ResourceScope) > 0 {
		names := make([]string, 0, minInt(3, len(ctx.ResourceScope)))
		for _, item := range ctx.ResourceScope {
			target := strings.TrimSpace(firstNonEmpty(item.Name, item.ID))
			if target != "" {
				names = append(names, target)
			}
			if len(names) >= 3 {
				break
			}
		}
		if len(names) > 0 {
			return "scoped resources: " + strings.Join(names, ", ")
		}
	}
	if ctx.Alert.Service != "" {
		return "service scope: " + ctx.Alert.Service
	}
	return "incident-scoped production target"
}

func buildWorkflow(
	sev string,
	ctx incidents.AggregatedContext,
	diagnosis IncidentDiagnosis,
	correlations []IncidentCorrelation,
	evidence []IncidentEvidence,
	steps []RecommendationStep,
	automation IncidentAutomationPlan,
	now time.Time,
) []IncidentWorkflowStage {
	mkStage := func(name, status, summary string) IncidentWorkflowStage {
		return IncidentWorkflowStage{
			Name:        name,
			Status:      status,
			Summary:     summary,
			GeneratedAt: now,
		}
	}

	return []IncidentWorkflowStage{
		mkStage("incident_intake", "completed",
			fmt.Sprintf("ingested alert %s (%s)", nonEmpty(ctx.AlertID, ctx.Alert.ID), strings.ToUpper(sev))),
		mkStage("context_gathering", stageStatus(len(ctx.Metrics)+len(ctx.Logs) > 0),
			fmt.Sprintf("collected %d metric scopes and %d log scopes", len(ctx.Metrics), len(ctx.Logs))),
		mkStage("hypothesis_generation", stageStatus(len(correlations) > 0),
			fmt.Sprintf("ranked %d cross-signal hypotheses", len(correlations))),
		mkStage("evidence_collection", stageStatus(len(evidence) > 0),
			fmt.Sprintf("collected %d evidence records", len(evidence))),
		mkStage("recommendation_generation", stageStatus(len(steps) > 0),
			fmt.Sprintf("generated %d actionable recommendations", len(steps))),
		mkStage("guarded_execution", stageStatus(automation.Enabled && strings.TrimSpace(diagnosis.ProbableRootCause) != ""),
			fmt.Sprintf("prepared %d guarded automation actions", len(automation.Actions))),
	}
}

func extractRecentChanges(alert incidents.InputAlert) []ContextChange {
	changes := make([]ContextChange, 0, 10)
	appendChange := func(source, key, value string) {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return
		}
		changes = append(changes, ContextChange{
			Source: source,
			Key:    key,
			Value:  truncateText(value, 120),
		})
	}

	isChangeKey := func(key string) bool {
		lower := strings.ToLower(key)
		tokens := []string{"commit", "sha", "version", "image", "deploy", "release", "rollout", "change", "build", "revision", "config"}
		for _, token := range tokens {
			if strings.Contains(lower, token) {
				return true
			}
		}
		return false
	}

	for key, value := range alert.Labels {
		if isChangeKey(key) {
			appendChange("label", key, value)
		}
	}
	for key, value := range alert.Annotations {
		if isChangeKey(key) {
			appendChange("annotation", key, value)
		}
	}
	return dedupeChanges(changes)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v) == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(v))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

func dedupeChanges(in []ContextChange) []ContextChange {
	seen := make(map[string]struct{}, len(in))
	out := make([]ContextChange, 0, len(in))
	for _, change := range in {
		key := strings.ToLower(strings.TrimSpace(change.Source + ":" + change.Key + ":" + change.Value))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, change)
	}
	return out
}

func dedupeRunbooks(in []RunbookLink) []RunbookLink {
	seen := make(map[string]struct{}, len(in))
	out := make([]RunbookLink, 0, len(in))
	for _, item := range in {
		key := strings.ToLower(strings.TrimSpace(item.ID + "|" + item.URL))
		if key == "|" || key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func dedupeRecommendationSteps(in []RecommendationStep) []RecommendationStep {
	seen := make(map[string]struct{}, len(in))
	out := make([]RecommendationStep, 0, len(in))
	for _, step := range in {
		key := strings.ToLower(strings.TrimSpace(step.ID + "|" + step.Summary))
		if key == "|" || key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, step)
	}
	return out
}

func severityToPriority(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical", "p0":
		return "P0"
	case "high", "p1":
		return "P1"
	case "medium", "p2":
		return "P2"
	default:
		return "P3"
	}
}

func stageStatus(ok bool) string {
	if ok {
		return "completed"
	}
	return "partial"
}

func normalizeSeverity(sev string) string {
	normalized := strings.ToLower(strings.TrimSpace(sev))
	if normalized == "" {
		return "unknown"
	}
	return normalized
}

func findRunbookID(runbooks []RunbookLink, token string) string {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return ""
	}
	for _, runbook := range runbooks {
		hay := strings.ToLower(runbook.ID + " " + runbook.Title + " " + runbook.URL)
		if strings.Contains(hay, token) {
			return runbook.ID
		}
	}
	return ""
}

func findRunbookURL(runbooks []RunbookLink) string {
	if len(runbooks) == 0 {
		return ""
	}
	return runbooks[0].URL
}

func evidenceIDsByKinds(evidence []IncidentEvidence, kinds ...string) []string {
	if len(evidence) == 0 {
		return nil
	}
	allow := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		allow[strings.ToLower(strings.TrimSpace(kind))] = struct{}{}
	}
	out := make([]string, 0, len(evidence))
	for _, item := range evidence {
		_, ok := allow[strings.ToLower(item.Kind)]
		if ok {
			out = append(out, item.ID)
		}
	}
	return out
}

func incidentActionID(alertID, suffix string) string {
	base := sanitizeToken(alertID)
	if base == "" {
		base = "incident"
	}
	suffix = sanitizeToken(suffix)
	if suffix == "" {
		suffix = "action"
	}
	return base + "-" + suffix
}

func sanitizeToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	lastDash := false
	for _, r := range strings.ToLower(value) {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func chooseNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Now().UTC()
}

func truncateText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
