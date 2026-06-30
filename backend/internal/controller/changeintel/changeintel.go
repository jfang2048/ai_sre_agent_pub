package changeintel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

// ChangeEvent is the normalized durable change record used by workflows.
type ChangeEvent struct {
	ChangeID       string            `json:"change_id"`
	Category       string            `json:"category"`
	Kind           string            `json:"kind,omitempty"`
	Summary        string            `json:"summary"`
	Description    string            `json:"description,omitempty"`
	Source         string            `json:"source,omitempty"`
	CollectorID    string            `json:"collector_id,omitempty"`
	Entity         string            `json:"entity,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Service        string            `json:"service,omitempty"`
	Status         string            `json:"status,omitempty"`
	RiskLevel      string            `json:"risk_level,omitempty"`
	RollbackHint   string            `json:"rollback_hint,omitempty"`
	StartedAt      time.Time         `json:"started_at"`
	CompletedAt    time.Time         `json:"completed_at,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	HypothesisHint string            `json:"hypothesis_hint,omitempty"`
}

// QueryOptions scopes change retrieval and correlation.
type QueryOptions struct {
	CollectorID     string
	IncidentSummary string
	WindowStart     time.Time
	WindowEnd       time.Time
	ScopeHints      []string
	Limit           int
}

// CorrelatedChange is a change event scored against the active incident.
type CorrelatedChange struct {
	Event             ChangeEvent `json:"event"`
	TemporalAdjacency float64     `json:"temporal_adjacency"`
	ScopeOverlap      float64     `json:"scope_overlap"`
	ChangeScore       float64     `json:"change_score"`
	ImpactSummary     string      `json:"impact_summary,omitempty"`
	HypothesisHints   []string    `json:"hypothesis_hints,omitempty"`
}

// QueryResult is the scored change-intel payload returned to workflows.
type QueryResult struct {
	Events      []CorrelatedChange `json:"events"`
	Summary     string             `json:"summary"`
	Categories  []string           `json:"categories,omitempty"`
	Strongest   *CorrelatedChange  `json:"strongest,omitempty"`
	QueryWindow string             `json:"query_window,omitempty"`
}

// Store persists normalized change events for future incident correlation.
type Store struct {
	rootPath string
	logger   *zap.Logger
}

// NewStore creates a JSON-backed change-intelligence store.
func NewStore(rootPath string, logger *zap.Logger) *Store {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Store{
		rootPath: filepath.Join(strings.TrimSpace(rootPath), "changeintel"),
		logger:   logger.With(zap.String("component", "changeintel_store")),
	}
}

// Append persists one normalized change event.
func (s *Store) Append(event ChangeEvent) (string, error) {
	if s == nil {
		return "", fmt.Errorf("changeintel store is nil")
	}
	if strings.TrimSpace(event.ChangeID) == "" {
		event.ChangeID = fmt.Sprintf("chg-%d", time.Now().UnixNano())
	}
	event.ChangeID = sanitizeID(event.ChangeID)
	event.Summary = firstNonEmpty(strings.TrimSpace(event.Summary), strings.TrimSpace(event.Description), event.ChangeID)
	if event.StartedAt.IsZero() {
		event.StartedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(s.rootPath, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(s.rootPath, event.ChangeID+".json")
	raw, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// Query returns stored change events that overlap the requested collector/window.
func (s *Store) Query(opts QueryOptions) ([]ChangeEvent, error) {
	if s == nil || strings.TrimSpace(s.rootPath) == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.rootPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	events := make([]ChangeEvent, 0, 32)
	err := filepath.WalkDir(s.rootPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var event ChangeEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil
		}
		if opts.CollectorID != "" && strings.TrimSpace(event.CollectorID) != "" && !strings.EqualFold(strings.TrimSpace(event.CollectorID), strings.TrimSpace(opts.CollectorID)) {
			return nil
		}
		events = append(events, event)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

// DeriveFromNode extracts operationally meaningful change evidence from labels and telemetry metadata.
func DeriveFromNode(node *ingest.NodeSnapshot) []ChangeEvent {
	if node == nil {
		return nil
	}
	events := make([]ChangeEvent, 0, 8)
	recordTime := nonZeroTime(node.LastCollectionAt, nonZeroTime(node.UpdatedAt, time.Now().UTC()))
	keys := make([]string, 0, len(node.Labels))
	for key := range node.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := node.Labels[key]
		keyLower := strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		category, hint := classifyLabelChange(keyLower)
		if category == "" {
			continue
		}
		events = append(events, ChangeEvent{
			ChangeID:       fmt.Sprintf("label-%s-%s", sanitizeID(node.CollectorID), sanitizeID(key)),
			Category:       category,
			Kind:           "label_change",
			Summary:        fmt.Sprintf("%s changed to %s", key, value),
			Description:    fmt.Sprintf("node label %s=%s was present during the incident window", key, value),
			Source:         "node_labels",
			CollectorID:    node.CollectorID,
			Entity:         firstNonEmpty(node.Hostname, node.CollectorID),
			Scope:          "node",
			Status:         "observed",
			RiskLevel:      inferRiskFromCategory(category),
			RollbackHint:   defaultRollbackHint(category),
			StartedAt:      recordTime,
			Labels:         map[string]string{key: value},
			Metadata:       map[string]string{"label_key": key, "label_value": value},
			HypothesisHint: hint,
		})
	}
	return dedupeEvents(events)
}

// DeriveFromLogMessages extracts recent deploy/config/driver/flag changes from log text.
func DeriveFromLogMessages(collectorID string, lines []string, observedAt time.Time) []ChangeEvent {
	events := make([]ChangeEvent, 0, len(lines))
	for idx, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		category, hint := classifyLogChange(line)
		if category == "" {
			continue
		}
		events = append(events, ChangeEvent{
			ChangeID:       fmt.Sprintf("log-%s-%02d", sanitizeID(collectorID), idx+1),
			Category:       category,
			Kind:           "log_change",
			Summary:        truncateString(line, 180),
			Description:    line,
			Source:         "log_index",
			CollectorID:    collectorID,
			Entity:         firstNonEmpty(collectorID, "fleet"),
			Scope:          "service",
			Status:         "observed",
			RiskLevel:      inferRiskFromCategory(category),
			RollbackHint:   defaultRollbackHint(category),
			StartedAt:      nonZeroTime(observedAt, time.Now().UTC()),
			HypothesisHint: hint,
		})
	}
	return dedupeEvents(events)
}

// Correlate scores change events against the incident window and scope.
func Correlate(events []ChangeEvent, opts QueryOptions) QueryResult {
	if len(events) == 0 {
		return QueryResult{
			Summary:     "no recent operational changes correlated with the incident window",
			QueryWindow: renderWindow(opts.WindowStart, opts.WindowEnd),
		}
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 6
	}
	scopeHints := make(map[string]struct{}, len(opts.ScopeHints)+2)
	for _, hint := range opts.ScopeHints {
		hint = strings.ToLower(strings.TrimSpace(hint))
		if hint != "" {
			scopeHints[hint] = struct{}{}
		}
	}
	if opts.CollectorID != "" {
		scopeHints[strings.ToLower(strings.TrimSpace(opts.CollectorID))] = struct{}{}
	}
	queryText := strings.ToLower(strings.TrimSpace(opts.IncidentSummary))
	scored := make([]CorrelatedChange, 0, len(events))
	for _, event := range dedupeEvents(events) {
		temporal := temporalAdjacency(event, opts.WindowStart, opts.WindowEnd)
		scope := scopeOverlap(event, scopeHints)
		semantic := semanticOverlap(event, queryText)
		score := clamp01(0.55*temporal + 0.30*scope + 0.15*semantic)
		if score <= 0 {
			continue
		}
		hints := compactStrings(event.HypothesisHint)
		if semantic == 0 && len(hints) == 0 {
			hints = append(hints, hypothesisHintForCategory(event.Category))
		}
		scored = append(scored, CorrelatedChange{
			Event:             event,
			TemporalAdjacency: temporal,
			ScopeOverlap:      scope,
			ChangeScore:       score,
			ImpactSummary:     buildImpactSummary(event, temporal, scope),
			HypothesisHints:   dedupeStrings(hints),
		})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].ChangeScore == scored[j].ChangeScore {
			return scored[i].Event.StartedAt.After(scored[j].Event.StartedAt)
		}
		return scored[i].ChangeScore > scored[j].ChangeScore
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	categories := make([]string, 0, len(scored))
	for _, item := range scored {
		categories = append(categories, item.Event.Category)
	}
	result := QueryResult{
		Events:      scored,
		Categories:  dedupeStrings(categories),
		QueryWindow: renderWindow(opts.WindowStart, opts.WindowEnd),
	}
	if len(scored) == 0 {
		result.Summary = "recent operational changes were observed but none aligned strongly with the incident scope or timing"
		return result
	}
	result.Strongest = &scored[0]
	result.Summary = fmt.Sprintf("%d recent changes correlated; strongest=%s (%s, score %.2f)", len(scored), scored[0].Event.Summary, scored[0].Event.Category, scored[0].ChangeScore)
	return result
}

func classifyLabelChange(key string) (string, string) {
	switch {
	case strings.Contains(key, "driver"), strings.Contains(key, "cuda"), strings.Contains(key, "nvidia"):
		return "driver", "driver or runtime mismatch"
	case strings.Contains(key, "image"), strings.Contains(key, "version"), strings.Contains(key, "release"), strings.Contains(key, "rollout"):
		return "deployment", "recent deployment/regression"
	case strings.Contains(key, "feature"), strings.Contains(key, "flag"), strings.Contains(key, "experiment"), strings.Contains(key, "canary"):
		return "feature_flag", "feature-flag or experiment regression"
	case strings.Contains(key, "config"), strings.Contains(key, "setting"), strings.Contains(key, "limit"), strings.Contains(key, "profile"):
		return "config", "configuration drift or parameter regression"
	case strings.Contains(key, "kernel"), strings.Contains(key, "firmware"), strings.Contains(key, "runtime"), strings.Contains(key, "cgroup"):
		return "infrastructure", "node runtime or infrastructure regression"
	default:
		return "", ""
	}
}

func classifyLogChange(line string) (string, string) {
	low := strings.ToLower(strings.TrimSpace(line))
	switch {
	case containsAny(low, "deploy", "deployment", "rollout", "release", "rollback", "image:"):
		return "deployment", "recent deployment/regression"
	case containsAny(low, "driver", "cuda", "nvidia", "firmware"):
		return "driver", "driver or runtime mismatch"
	case containsAny(low, "feature flag", "flag enabled", "flag disabled", "canary", "experiment"):
		return "feature_flag", "feature-flag or experiment regression"
	case containsAny(low, "config", "configuration", "env var", "tuned", "updated setting"):
		return "config", "configuration drift or parameter regression"
	case containsAny(low, "kernel", "node reboot", "daemonset", "cni", "network policy", "runtime upgrade"):
		return "infrastructure", "node runtime or infrastructure regression"
	default:
		return "", ""
	}
}

func hypothesisHintForCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "deployment":
		return "recent deployment/regression"
	case "config":
		return "configuration drift or parameter regression"
	case "driver":
		return "driver or runtime mismatch"
	case "feature_flag":
		return "feature-flag or experiment regression"
	case "infrastructure":
		return "node runtime or infrastructure regression"
	default:
		return "change-linked regression"
	}
}

func defaultRollbackHint(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "deployment":
		return "roll back to the previous release or image revision"
	case "config":
		return "restore the previous configuration snapshot"
	case "driver":
		return "revert to the previously known-good driver/runtime version"
	case "feature_flag":
		return "disable the flag or return the canary to the prior state"
	case "infrastructure":
		return "revert the node/runtime change or drain the affected node"
	default:
		return "restore the last known-good operational state"
	}
}

func inferRiskFromCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "driver", "infrastructure":
		return "high"
	case "deployment", "config", "feature_flag":
		return "medium"
	default:
		return "low"
	}
}

func temporalAdjacency(event ChangeEvent, start, end time.Time) float64 {
	if event.StartedAt.IsZero() || start.IsZero() || end.IsZero() {
		return 0.4
	}
	if !event.StartedAt.Before(start) && !event.StartedAt.After(end) {
		return 1.0
	}
	var delta time.Duration
	if event.StartedAt.Before(start) {
		delta = start.Sub(event.StartedAt)
	} else {
		delta = event.StartedAt.Sub(end)
	}
	switch {
	case delta <= 15*time.Minute:
		return 0.9
	case delta <= time.Hour:
		return 0.7
	case delta <= 4*time.Hour:
		return 0.45
	default:
		return 0.2
	}
}

func scopeOverlap(event ChangeEvent, scopeHints map[string]struct{}) float64 {
	if len(scopeHints) == 0 {
		return 0.4
	}
	score := 0.0
	for _, item := range []string{event.CollectorID, event.Entity, event.Scope, event.Service, event.Summary} {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		for hint := range scopeHints {
			if strings.Contains(item, hint) || strings.Contains(hint, item) {
				score = maxFloat(score, 1.0)
			}
		}
	}
	if score > 0 {
		return score
	}
	return 0.2
}

func semanticOverlap(event ChangeEvent, query string) float64 {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0.3
	}
	text := strings.ToLower(strings.Join([]string{event.Category, event.Summary, event.Description, event.HypothesisHint}, " "))
	matches := 0
	for _, token := range strings.Fields(query) {
		if len(token) < 3 {
			continue
		}
		if strings.Contains(text, token) {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}
	return clamp01(float64(matches) / 4.0)
}

func buildImpactSummary(event ChangeEvent, temporal, scope float64) string {
	return fmt.Sprintf("%s via %s (temporal=%.2f scope=%.2f)", firstNonEmpty(event.Summary, event.Category), firstNonEmpty(event.Scope, "operational_change"), temporal, scope)
}

func dedupeEvents(events []ChangeEvent) []ChangeEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]ChangeEvent, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		key := firstNonEmpty(strings.TrimSpace(event.ChangeID), strings.TrimSpace(event.Summary))
		if key == "" {
			continue
		}
		key = strings.ToLower(key)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, event)
	}
	return out
}

func sanitizeID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	out := make([]rune, 0, len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return strings.Trim(strings.ToLower(string(out)), "-")
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(needle))) {
			return true
		}
	}
	return false
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func truncateString(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func nonZeroTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func renderWindow(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	return start.UTC().Format(time.RFC3339) + "/" + end.UTC().Format(time.RFC3339)
}
