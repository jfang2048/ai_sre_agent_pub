package incidentmemory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// TimelineEvent captures a durable incident event.
type TimelineEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Phase     string    `json:"phase"`
	Summary   string    `json:"summary"`
}

// Hypothesis captures a compact root-cause hypothesis snapshot.
type Hypothesis struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Confidence  float64  `json:"confidence"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// ActionOutcome captures the result of one action attempted during an incident.
type ActionOutcome struct {
	ActionID        string    `json:"action_id,omitempty"`
	Action          string    `json:"action"`
	Status          string    `json:"status,omitempty"`
	Verification    string    `json:"verification,omitempty"`
	RollbackStatus  string    `json:"rollback_status,omitempty"`
	Success         bool      `json:"success"`
	Useful          bool      `json:"useful,omitempty"`
	ExecutedAt      time.Time `json:"executed_at,omitempty"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
	OperatorComment string    `json:"operator_comment,omitempty"`
}

// OperatorFeedback captures human feedback linked to an incident or action.
type OperatorFeedback struct {
	Actor     string    `json:"actor,omitempty"`
	Verdict   string    `json:"verdict,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// Record is the durable incident-memory artifact stored for future retrieval.
type Record struct {
	RecordID            string             `json:"record_id"`
	WorkflowID          string             `json:"workflow_id"`
	IncidentID          string             `json:"incident_id,omitempty"`
	WorkflowType        string             `json:"workflow_type"`
	CollectorID         string             `json:"collector_id,omitempty"`
	Status              string             `json:"status,omitempty"`
	Title               string             `json:"title"`
	Summary             string             `json:"summary"`
	RootCauseEntity     string             `json:"root_cause_entity,omitempty"`
	MostLikelyCause     string             `json:"most_likely_cause,omitempty"`
	ResolutionSummary   string             `json:"resolution_summary,omitempty"`
	VerificationSummary string             `json:"verification_summary,omitempty"`
	CausalPath          []string           `json:"causal_path,omitempty"`
	ImpactScope         []string           `json:"impact_scope,omitempty"`
	LessonsLearned      []string           `json:"lessons_learned,omitempty"`
	Signals             []string           `json:"signals,omitempty"`
	Actions             []string           `json:"actions,omitempty"`
	EvidenceIDs         []string           `json:"evidence_ids,omitempty"`
	Tags                []string           `json:"tags,omitempty"`
	Metadata            map[string]string  `json:"metadata,omitempty"`
	Timeline            []TimelineEvent    `json:"timeline,omitempty"`
	Hypotheses          []Hypothesis       `json:"hypotheses,omitempty"`
	ActionOutcomes      []ActionOutcome    `json:"action_outcomes,omitempty"`
	OperatorFeedback    []OperatorFeedback `json:"operator_feedback,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

// QueryOptions tunes lexical retrieval over incident-memory records.
type QueryOptions struct {
	Intent      string
	CollectorID string
	TopK        int
}

// Match is one scored incident-memory retrieval hit.
type Match struct {
	Record  Record   `json:"record"`
	Score   float64  `json:"score"`
	Snippet string   `json:"snippet"`
	Reasons []string `json:"reasons,omitempty"`
}

// Store persists incident-memory records under a local durable path.
type Store struct {
	rootPath string
	logger   *zap.Logger
}

// NewStore creates a JSON-backed incident-memory store.
func NewStore(rootPath string, logger *zap.Logger) *Store {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Store{
		rootPath: filepath.Join(strings.TrimSpace(rootPath), "incident_memory"),
		logger:   logger.With(zap.String("component", "incident_memory_store")),
	}
}

// Append persists one record and returns the file path.
func (s *Store) Append(record Record) (string, error) {
	if s == nil {
		return "", fmt.Errorf("incident memory store is nil")
	}
	if strings.TrimSpace(record.RecordID) == "" {
		record.RecordID = fmt.Sprintf("incident-%d", time.Now().UnixNano())
	}
	record.RecordID = sanitizeID(record.RecordID)
	record.Title = firstNonEmpty(strings.TrimSpace(record.Title), strings.TrimSpace(record.Summary), record.RecordID)
	record.CreatedAt = nonZeroTime(record.CreatedAt, time.Now().UTC())
	record.UpdatedAt = time.Now().UTC()
	record.CausalPath = dedupeStrings(record.CausalPath)
	record.ImpactScope = dedupeStrings(record.ImpactScope)
	record.LessonsLearned = dedupeStrings(record.LessonsLearned)
	record.Signals = dedupeStrings(record.Signals)
	record.Actions = dedupeStrings(record.Actions)
	record.EvidenceIDs = dedupeStrings(record.EvidenceIDs)
	record.Tags = dedupeStrings(record.Tags)
	record.Metadata = cloneStringMap(record.Metadata)

	if err := os.MkdirAll(s.rootPath, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(s.rootPath, record.RecordID+".json")
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// Query returns scored lexical matches for the provided free-text query.
func (s *Store) Query(query string, opts QueryOptions) []Match {
	if s == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	records, err := s.load()
	if err != nil {
		s.logger.Warn("failed to load incident-memory records", zap.Error(err))
		return nil
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = 4
	}
	now := time.Now().UTC()
	profile := buildQueryProfile(query)
	scored := make([]Match, 0, len(records))
	for _, record := range records {
		score, reasons := scoreRecord(record, profile, opts, now)
		if score <= 0 {
			continue
		}
		scored = append(scored, Match{
			Record:  record,
			Score:   score,
			Snippet: buildSnippet(record, reasons),
			Reasons: reasons,
		})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Record.UpdatedAt.After(scored[j].Record.UpdatedAt)
		}
		return scored[i].Score > scored[j].Score
	})
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

func (s *Store) load() ([]Record, error) {
	if s == nil || strings.TrimSpace(s.rootPath) == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.rootPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	records := make([]Record, 0, 32)
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
		var record Record
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

type queryProfile struct {
	tokens      []string
	signalHints []string
	actionHints []string
	changeHints []string
}

type scoreWeights struct {
	lexical   float64
	signal    float64
	action    float64
	changeSet float64
	collector float64
	trust     float64
	recency   float64
	feedback  float64
}

var signalHintKeywords = []string{
	"cpu", "memory", "oom", "latency", "timeout", "gpu", "disk", "io", "storage",
	"network", "retransmit", "security", "thermal", "pressure", "queue", "throughput",
}

var actionHintKeywords = []string{
	"rollback", "revert", "restart", "drain", "scale", "isolate", "disable", "enable",
	"throttle", "profile", "collect", "tune",
}

var changeHintKeywords = []string{
	"rollout", "deploy", "deployment", "release", "config", "driver", "feature", "flag",
	"canary", "kernel", "runtime", "upgrade", "image",
}

func buildQueryProfile(query string) queryProfile {
	tokens := tokenizeText(query)
	return queryProfile{
		tokens:      tokens,
		signalHints: filterHintTokens(tokens, signalHintKeywords),
		actionHints: filterHintTokens(tokens, actionHintKeywords),
		changeHints: filterHintTokens(tokens, changeHintKeywords),
	}
}

func filterHintTokens(tokens, keywords []string) []string {
	if len(tokens) == 0 || len(keywords) == 0 {
		return nil
	}
	out := make([]string, 0, len(tokens))
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		for _, keyword := range keywords {
			if token == keyword || strings.Contains(token, keyword) || strings.Contains(keyword, token) {
				if _, ok := seen[keyword]; ok {
					break
				}
				seen[keyword] = struct{}{}
				out = append(out, keyword)
				break
			}
		}
	}
	return out
}

func weightsForIntent(intent string) scoreWeights {
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case "runbook":
		return scoreWeights{lexical: 0.35, signal: 0.10, action: 0.18, changeSet: 0.05, collector: 0.05, trust: 0.17, recency: 0.05, feedback: 0.05}
	case "joint_risk":
		return scoreWeights{lexical: 0.40, signal: 0.18, action: 0.05, changeSet: 0.10, collector: 0.07, trust: 0.10, recency: 0.05, feedback: 0.05}
	default:
		return scoreWeights{lexical: 0.38, signal: 0.16, action: 0.10, changeSet: 0.08, collector: 0.08, trust: 0.10, recency: 0.05, feedback: 0.05}
	}
}

func scoreRecord(record Record, profile queryProfile, opts QueryOptions, now time.Time) (float64, []string) {
	if len(profile.tokens) == 0 {
		return 0, nil
	}
	text := recordSearchText(record)
	if strings.TrimSpace(text) == "" {
		return 0, nil
	}
	lexicalScore := tokenCoverage(profile.tokens, text)
	if lexicalScore <= 0 {
		return 0, nil
	}
	weights := weightsForIntent(opts.Intent)
	signalScore := tokenCoverage(profile.signalHints, text)
	actionScore := tokenCoverage(profile.actionHints, text)
	changeScore := tokenCoverage(profile.changeHints, text)
	collectorScore := 0.0
	if opts.CollectorID != "" && strings.EqualFold(strings.TrimSpace(record.CollectorID), strings.TrimSpace(opts.CollectorID)) {
		collectorScore = 1
	}
	trustScore := recordTrustScore(record)
	recencyScore := recordRecencyScore(record, now)
	feedbackScore := recordFeedbackScore(record)

	score := lexicalScore*weights.lexical +
		signalScore*weights.signal +
		actionScore*weights.action +
		changeScore*weights.changeSet +
		collectorScore*weights.collector +
		trustScore*weights.trust +
		recencyScore*weights.recency +
		feedbackScore*weights.feedback

	reasons := make([]string, 0, 6)
	if lexicalScore >= 0.45 {
		reasons = append(reasons, "summary and root-cause text match the current incident")
	}
	if collectorScore > 0 {
		reasons = append(reasons, "same collector")
	}
	if signalScore > 0 {
		reasons = append(reasons, "same signal family: "+strings.Join(profile.signalHints, ", "))
	}
	if changeScore > 0 {
		reasons = append(reasons, "same change pattern: "+strings.Join(profile.changeHints, ", "))
	}
	if actionScore > 0 {
		reasons = append(reasons, "same remediation pattern: "+strings.Join(profile.actionHints, ", "))
	}
	if trustScore >= 0.55 {
		reasons = append(reasons, "prior outcome was resolved and verified")
	}
	if feedbackScore >= 0.6 {
		reasons = append(reasons, "operator feedback marked the prior action useful")
	}
	if recencyScore >= 0.6 {
		reasons = append(reasons, "recent incident memory")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "lexical incident match")
	}
	return score, dedupeStrings(reasons)
}

func recordSearchText(record Record) string {
	parts := []string{
		record.Title,
		record.Summary,
		record.RootCauseEntity,
		record.MostLikelyCause,
		record.ResolutionSummary,
		record.VerificationSummary,
		strings.Join(record.CausalPath, " "),
		strings.Join(record.ImpactScope, " "),
		strings.Join(record.LessonsLearned, " "),
		strings.Join(record.Signals, " "),
		strings.Join(record.Actions, " "),
		strings.Join(record.Tags, " "),
	}
	for _, event := range record.Timeline {
		parts = append(parts, event.Phase, event.Summary)
	}
	for _, hypothesis := range record.Hypotheses {
		parts = append(parts, hypothesis.Title)
	}
	for _, outcome := range record.ActionOutcomes {
		parts = append(parts, outcome.Action, outcome.Status, outcome.Verification, outcome.OperatorComment, outcome.RollbackStatus)
	}
	for _, feedback := range record.OperatorFeedback {
		parts = append(parts, feedback.Verdict, feedback.Notes)
	}
	for key, value := range record.Metadata {
		parts = append(parts, key, value)
	}
	return strings.ToLower(strings.Join(compactStrings(parts...), " "))
}

func tokenCoverage(tokens []string, text string) float64 {
	if len(tokens) == 0 || strings.TrimSpace(text) == "" {
		return 0
	}
	matches := 0
	for _, token := range tokens {
		if strings.Contains(text, token) {
			matches++
		}
	}
	return float64(matches) / float64(len(tokens))
}

func recordTrustScore(record Record) float64 {
	score := 0.0
	status := strings.ToLower(strings.TrimSpace(record.Status))
	if strings.Contains(status, "resolved") || strings.Contains(status, "closed") {
		score += 0.25
	}
	if strings.TrimSpace(record.VerificationSummary) != "" {
		score += 0.20
	}
	if len(record.ActionOutcomes) == 0 {
		if strings.TrimSpace(record.ResolutionSummary) != "" {
			score += 0.10
		}
		return clamp01(score)
	}
	successes := 0.0
	useful := 0.0
	verified := 0.0
	for _, outcome := range record.ActionOutcomes {
		if outcome.Success {
			successes++
		}
		if outcome.Useful {
			useful++
		}
		if strings.TrimSpace(outcome.Verification) != "" || strings.EqualFold(strings.TrimSpace(outcome.Status), "verified") {
			verified++
		}
	}
	count := float64(len(record.ActionOutcomes))
	score += (successes / count) * 0.30
	score += (useful / count) * 0.15
	score += (verified / count) * 0.10
	return clamp01(score)
}

func recordFeedbackScore(record Record) float64 {
	if len(record.OperatorFeedback) == 0 {
		return 0
	}
	positive := 0.0
	negative := 0.0
	for _, feedback := range record.OperatorFeedback {
		text := strings.ToLower(strings.TrimSpace(feedback.Verdict + " " + feedback.Notes))
		switch {
		case containsAny(text, "useful", "worked", "resolved", "correct", "confirmed", "successful"):
			positive++
		case containsAny(text, "failed", "incorrect", "wrong", "harmful", "regressed", "not useful"):
			negative++
		}
	}
	if positive == 0 && negative == 0 {
		return 0
	}
	return positive / (positive + negative)
}

func recordRecencyScore(record Record, now time.Time) float64 {
	ts := nonZeroTime(record.UpdatedAt, record.CreatedAt)
	if ts.IsZero() {
		return 0
	}
	age := now.Sub(ts)
	if age <= 0 {
		return 1
	}
	days := age.Hours() / 24
	return 1 / (1 + days/30.0)
}

func buildSnippet(record Record, reasons []string) string {
	parts := []string{
		record.Summary,
		record.MostLikelyCause,
		record.ResolutionSummary,
		record.VerificationSummary,
	}
	if len(record.Actions) > 0 {
		parts = append(parts, "actions: "+strings.Join(record.Actions, "; "))
	}
	if len(record.CausalPath) > 0 {
		parts = append(parts, "causal_path: "+strings.Join(record.CausalPath, " -> "))
	}
	if len(reasons) > 0 {
		limit := minInt(len(reasons), 2)
		parts = append(parts, "match: "+strings.Join(reasons[:limit], "; "))
	}
	return truncateString(strings.Join(compactStrings(parts...), " | "), 320)
}

func containsAny(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && strings.Contains(text, candidate) {
			return true
		}
	}
	return false
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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

func tokenizeText(text string) []string {
	clean := strings.ToLower(strings.NewReplacer(",", " ", ".", " ", ":", " ", ";", " ", "\n", " ", "\t", " ").Replace(text))
	fields := strings.Fields(clean)
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field) < 3 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
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

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func nonZeroTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
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
