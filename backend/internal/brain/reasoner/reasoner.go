package reasoner

import (
	"context"
	"fmt"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/brain/llm"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/proto"
	metricspb "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
)

// Reasoner provides reasoning capabilities for incident analysis
type Reasoner struct {
	llmClient *llm.OpenAIClient
	logger    *zap.Logger
	cache     *ReasoningCache
}

// Config configures the reasoner
type Config struct {
	CacheTTL   time.Duration
	MaxRetries int
}

// NewReasoner creates a new reasoner
func NewReasoner(llmClient *llm.OpenAIClient, logger *zap.Logger) *Reasoner {
	return &Reasoner{
		llmClient: llmClient,
		logger:    logger.With(zap.String("component", "reasoner")),
		cache:     NewReasoningCache(1 * time.Hour),
	}
}

// AnalyzeIncident analyzes an incident and provides reasoning
func (r *Reasoner) AnalyzeIncident(ctx context.Context, incident *IncidentContext) (*IncidentAnalysis, error) {
	r.logger.Info("analyzing incident",
		zap.String("title", incident.Title),
		zap.Int("metrics", len(incident.Metrics)),
		zap.Int("logs", len(incident.Logs)))

	// Check cache
	cacheKey := incident.CacheKey()
	if cached := r.cache.Get(cacheKey); cached != nil {
		r.logger.Debug("using cached analysis")
		return cached, nil
	}

	// Build analysis prompt
	prompt := r.buildIncidentPrompt(incident)

	// Get LLM analysis
	response, err := r.llmClient.Complete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM analysis failed: %w", err)
	}

	// Parse response
	analysis := r.parseIncidentAnalysis(response, incident)

	// Cache result
	r.cache.Set(cacheKey, analysis)

	return analysis, nil
}

// GenerateActionPlan generates an action plan for remediation
func (r *Reasoner) GenerateActionPlan(ctx context.Context, situation *Situation) (*ActionPlan, error) {
	r.logger.Info("generating action plan",
		zap.String("situation", situation.Summary))

	prompt := r.buildActionPlanPrompt(situation)

	response, err := r.llmClient.Complete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("action plan generation failed: %w", err)
	}

	plan := r.parseActionPlan(response)
	return plan, nil
}

// ExplainDecision explains a decision made by the agent
func (r *Reasoner) ExplainDecision(ctx context.Context, decision *Decision) (*Explanation, error) {
	prompt := r.buildExplanationPrompt(decision)

	response, err := r.llmClient.Complete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("explanation generation failed: %w", err)
	}

	return &Explanation{
		DecisionID:  decision.ID,
		Reasoning:   response,
		GeneratedAt: time.Now(),
	}, nil
}

// IncidentContext provides context for incident analysis
type IncidentContext struct {
	Title       string
	Description string
	StartTime   time.Time
	Metrics     []*metricspb.Metric
	Logs        []string
	Alerts      []*proto.Alert
	SLOs        []*proto.SLO
	Environment string
}

// CacheKey generates a cache key for the incident
func (ic *IncidentContext) CacheKey() string {
	return fmt.Sprintf("%s-%d", ic.Title, ic.StartTime.Unix())
}

// IncidentAnalysis is the result of incident analysis
type IncidentAnalysis struct {
	Summary             string            `json:"summary"`
	RootCause           string            `json:"root_cause"`
	Impact              string            `json:"impact"`
	ContributingFactors []string          `json:"contributing_factors"`
	SuggestedActions    []string          `json:"suggested_actions"`
	Confidence          float64           `json:"confidence"`
	RelatedIncidents    []string          `json:"related_incidents"`
	Metadata            map[string]string `json:"metadata"`
}

// Situation represents a situation requiring action
type Situation struct {
	Summary      string
	Severity     string
	Metrics      []*metricspb.Metric
	Alerts       []*proto.Alert
	Constraints  []string
	Capabilities []string
	Environment  string
}

// ActionPlan is a remediation action plan
type ActionPlan struct {
	ID          string        `json:"id"`
	Description string        `json:"description"`
	Priority    string        `json:"priority"`
	Steps       []ActionStep  `json:"steps"`
	Eta         time.Duration `json:"eta"`
	Risk        string        `json:"risk"`
	Confidence  float64       `json:"confidence"`
}

// ActionStep is a step in an action plan
type ActionStep struct {
	Order       int                    `json:"order"`
	Description string                 `json:"description"`
	Type        string                 `json:"type"`
	Target      string                 `json:"target"`
	Parameters  map[string]interface{} `json:"parameters"`
	CanRollback bool                   `json:"can_rollback"`
}

// Decision represents a decision made by the agent
type Decision struct {
	ID         string
	Type       string
	Action     string
	Target     string
	Reasoning  string
	Metrics    []*metricspb.Metric
	Confidence float64
	Timestamp  time.Time
}

// Explanation explains a decision
type Explanation struct {
	DecisionID  string    `json:"decision_id"`
	Reasoning   string    `json:"reasoning"`
	Alternative []string  `json:"alternative"`
	GeneratedAt time.Time `json:"generated_at"`
}

// buildIncidentPrompt builds a prompt for incident analysis
func (r *Reasoner) buildIncidentPrompt(incident *IncidentContext) string {
	return fmt.Sprintf(`You are an expert SRE analyzing an incident.

Incident: %s
Description: %s
Started: %s
Environment: %s

Available Data:
- Metrics: %d data points
- Log entries: %d
- Active alerts: %d
- SLOs affected: %d

Please analyze this incident and provide:
1. A clear summary
2. Likely root cause
3. Impact assessment
4. Contributing factors
5. Suggested remediation actions
6. Confidence level (0-1)

Format your response as JSON.`,
		incident.Title,
		incident.Description,
		incident.StartTime.Format(time.RFC3339),
		incident.Environment,
		len(incident.Metrics),
		len(incident.Logs),
		len(incident.Alerts),
		len(incident.SLOs))
}

// buildActionPlanPrompt builds a prompt for action plan generation
func (r *Reasoner) buildActionPlanPrompt(situation *Situation) string {
	return fmt.Sprintf(`You are an expert SRE creating an action plan.

Situation: %s
Severity: %s
Environment: %s

Constraints: %v
Available capabilities: %v

Generate a step-by-step action plan to resolve this situation.
Include:
1. Priority level
2. Ordered steps
3. Target for each step
4. Estimated time
5. Risk level
6. Confidence

Format as JSON.`,
		situation.Summary,
		situation.Severity,
		situation.Environment,
		situation.Constraints,
		situation.Capabilities)
}

// buildExplanationPrompt builds a prompt for decision explanation
func (r *Reasoner) buildExplanationPrompt(decision *Decision) string {
	return fmt.Sprintf(`Explain the following SRE agent decision:

Decision Type: %s
Action: %s
Target: %s
Initial Reasoning: %s
Confidence: %.2f
Timestamp: %s

Provide:
1. Detailed explanation of why this decision was made
2. Alternative approaches that were considered
3. Why this approach was chosen over alternatives`,
		decision.Type,
		decision.Action,
		decision.Target,
		decision.Reasoning,
		decision.Confidence,
		decision.Timestamp.Format(time.RFC3339))
}

// parseIncidentAnalysis parses LLM response into incident analysis
func (r *Reasoner) parseIncidentAnalysis(response string, incident *IncidentContext) *IncidentAnalysis {
	return &IncidentAnalysis{
		Summary:    response,
		Confidence: 0.8,
		Metadata: map[string]string{
			"incident_title": incident.Title,
			"analyzed_at":    time.Now().Format(time.RFC3339),
		},
	}
}

// parseActionPlan parses LLM response into action plan
func (r *Reasoner) parseActionPlan(response string) *ActionPlan {
	return &ActionPlan{
		ID:          fmt.Sprintf("plan-%d", time.Now().UnixNano()),
		Description: response,
		Priority:    "medium",
		Steps: []ActionStep{
			{
				Order:       1,
				Description: response,
				Type:        "remediate",
			},
		},
		Eta:        30 * time.Minute,
		Risk:       "medium",
		Confidence: 0.75,
	}
}

// ReasoningCache caches reasoning results
type ReasoningCache struct {
	items map[string]*IncidentAnalysis
	ttl   time.Duration
}

// NewReasoningCache creates a new reasoning cache
func NewReasoningCache(ttl time.Duration) *ReasoningCache {
	return &ReasoningCache{
		items: make(map[string]*IncidentAnalysis),
		ttl:   ttl,
	}
}

// Get gets an item from cache
func (c *ReasoningCache) Get(key string) *IncidentAnalysis {
	return c.items[key]
}

// Set sets an item in cache
func (c *ReasoningCache) Set(key string, analysis *IncidentAnalysis) {
	c.items[key] = analysis
}

// Clear clears the cache
func (c *ReasoningCache) Clear() {
	c.items = make(map[string]*IncidentAnalysis)
}
