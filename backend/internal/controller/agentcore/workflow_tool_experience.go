package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const workflowToolExperienceSchemaVersion = "ai_sre_agent/tool_experience/v1"
const workflowToolExperienceMaxRecords = 512

type ToolExperienceRecord struct {
	SchemaVersion                 string    `json:"schema_version"`
	Key                           string    `json:"key"`
	IncidentShape                 string    `json:"incident_shape,omitempty"`
	SceneFamily                   string    `json:"scene_family,omitempty"`
	HypothesisFamily              string    `json:"hypothesis_family,omitempty"`
	ObjectiveKey                  string    `json:"objective_key,omitempty"`
	GapKey                        string    `json:"gap_key,omitempty"`
	Tool                          ToolName  `json:"tool"`
	CapabilityFamily              string    `json:"capability_family,omitempty"`
	ToolSequence                  []string  `json:"tool_sequence,omitempty"`
	Attempts                      int       `json:"attempts"`
	ProgressCount                 int       `json:"progress_count"`
	PlateauCount                  int       `json:"plateau_count"`
	LowYieldCount                 int       `json:"low_yield_count,omitempty"`
	WastedSequenceCount           int       `json:"wasted_sequence_count,omitempty"`
	VerifiedRemediationPrecursors int       `json:"verified_remediation_precursors,omitempty"`
	AvgConfidenceDelta            float64   `json:"avg_confidence_delta,omitempty"`
	AvgGapReduction               float64   `json:"avg_gap_reduction,omitempty"`
	AvgRiskDelta                  float64   `json:"avg_risk_delta,omitempty"`
	LastNormalizedSummary         string    `json:"last_normalized_summary,omitempty"`
	LastLikelyNextFamilies        []string  `json:"last_likely_next_families,omitempty"`
	EffectiveQueryPatterns        []string  `json:"effective_query_patterns,omitempty"`
	LastResultQuality             string    `json:"last_result_quality,omitempty"`
	LastUpdatedAt                 time.Time `json:"last_updated_at"`
}

type ToolExperienceMemoryStore struct {
	mu      sync.RWMutex
	path    string
	logger  *zap.Logger
	records map[string]ToolExperienceRecord
}

func NewToolExperienceMemoryStore(rootPath string, logger *zap.Logger) *ToolExperienceMemoryStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	store := &ToolExperienceMemoryStore{
		path:    filepath.Join(strings.TrimSpace(rootPath), "tool_experience.json"),
		logger:  logger.With(zap.String("component", "workflow_tool_experience")),
		records: map[string]ToolExperienceRecord{},
	}
	store.load()
	return store
}

func (s *ToolExperienceMemoryStore) Prior(scene SceneFamily, objective string, gaps []string, contract WorkflowToolContract) float64 {
	if s == nil {
		return 0
	}
	key := toolExperienceKey(scene, objective, gaps, contract.ToolName)
	s.mu.RLock()
	record, ok := s.records[key]
	s.mu.RUnlock()
	if !ok || record.Attempts == 0 {
		return 0
	}
	progressRate := float64(record.ProgressCount) / float64(maxInt(record.Attempts, 1))
	plateauRate := float64(record.PlateauCount) / float64(maxInt(record.Attempts, 1))
	lowYieldRate := float64(record.LowYieldCount) / float64(maxInt(record.Attempts, 1))
	remediationPrecursorRate := float64(record.VerifiedRemediationPrecursors) / float64(maxInt(record.Attempts, 1))
	score := 0.18*progressRate + 0.12*record.AvgConfidenceDelta + 0.10*record.AvgGapReduction + 0.06*remediationPrecursorRate - 0.14*plateauRate - 0.08*record.AvgRiskDelta - 0.10*lowYieldRate
	if score > 0.18 {
		score = 0.18
	}
	if score < -0.18 {
		score = -0.18
	}
	return score
}

func (s *ToolExperienceMemoryStore) Observe(scene SceneFamily, objective string, gaps []string, contract WorkflowToolContract, progress AdaptiveProgressAssessment, normalized *NormalizedToolResult) ToolExperienceRecord {
	if s == nil {
		return ToolExperienceRecord{}
	}
	key := toolExperienceKey(scene, objective, gaps, contract.ToolName)
	s.mu.Lock()
	defer s.mu.Unlock()

	record := s.records[key]
	record.SchemaVersion = workflowToolExperienceSchemaVersion
	record.Key = key
	record.IncidentShape = toolExperienceIncidentShape(objective)
	record.SceneFamily = string(scene)
	record.HypothesisFamily = strings.TrimSpace(string(scene))
	record.ObjectiveKey = toolExperienceObjectiveKey(objective)
	record.GapKey = toolExperienceGapKey(gaps)
	record.Tool = contract.ToolName
	record.CapabilityFamily = contract.CapabilityFamily
	record.ToolSequence = dedupeStrings(append(record.ToolSequence, string(contract.ToolName)))
	record.Attempts++
	if progress.Progress {
		record.ProgressCount++
	}
	if progress.Plateau {
		record.PlateauCount++
		record.WastedSequenceCount++
	}
	record.AvgConfidenceDelta = rollingAverage(record.AvgConfidenceDelta, progress.ConfidenceDelta, record.Attempts)
	record.AvgGapReduction = rollingAverage(record.AvgGapReduction, float64(progress.EvidenceGapCoverageDelta), record.Attempts)
	record.AvgRiskDelta = rollingAverage(record.AvgRiskDelta, progress.RiskDelta, record.Attempts)
	if normalized != nil {
		record.LastNormalizedSummary = truncateString(normalized.Summary, 160)
		record.LastLikelyNextFamilies = append([]string(nil), normalized.LikelyNextToolFamilies...)
		record.LastResultQuality = strings.TrimSpace(normalized.ResultQuality)
		if normalized.LowYieldSignal {
			record.LowYieldCount++
		}
		record.EffectiveQueryPatterns = dedupeStrings(append(record.EffectiveQueryPatterns, normalized.RecommendedScopeRefinement...))
		record.EffectiveQueryPatterns = dedupeStrings(append(record.EffectiveQueryPatterns, normalized.LikelyNextChecks...))
		if normalized.RemediationEligibilityDelta > 0 {
			record.VerifiedRemediationPrecursors++
		}
	}
	record.LastUpdatedAt = time.Now().UTC()
	s.records[key] = record
	s.pruneLocked()
	s.persistLocked()
	return record
}

func (s *ToolExperienceMemoryStore) Snapshot() []ToolExperienceRecord {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolExperienceRecord, 0, len(s.records))
	for _, record := range s.records {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastUpdatedAt.After(out[j].LastUpdatedAt)
	})
	return out
}

func (s *ToolExperienceMemoryStore) load() {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var records []ToolExperienceRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to decode tool experience store", zap.Error(err))
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range records {
		if strings.TrimSpace(record.Key) == "" {
			continue
		}
		s.records[record.Key] = record
	}
}

func (s *ToolExperienceMemoryStore) persistLocked() {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return
	}
	out := make([]ToolExperienceRecord, 0, len(s.records))
	for _, record := range s.records {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastUpdatedAt.Equal(out[j].LastUpdatedAt) {
			return out[i].Key < out[j].Key
		}
		return out[i].LastUpdatedAt.After(out[j].LastUpdatedAt)
	})
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(s.path, raw, 0o644); err != nil && s.logger != nil {
		s.logger.Warn("failed to persist tool experience store", zap.Error(err))
	}
}

func (s *ToolExperienceMemoryStore) pruneLocked() {
	if len(s.records) <= workflowToolExperienceMaxRecords {
		return
	}
	records := make([]ToolExperienceRecord, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].LastUpdatedAt.Equal(records[j].LastUpdatedAt) {
			return records[i].Key < records[j].Key
		}
		return records[i].LastUpdatedAt.After(records[j].LastUpdatedAt)
	})
	keep := make(map[string]struct{}, workflowToolExperienceMaxRecords)
	for idx, record := range records {
		if idx >= workflowToolExperienceMaxRecords {
			break
		}
		keep[record.Key] = struct{}{}
	}
	for key := range s.records {
		if _, ok := keep[key]; ok {
			continue
		}
		delete(s.records, key)
	}
}

func toolExperienceKey(scene SceneFamily, objective string, gaps []string, tool ToolName) string {
	return strings.Join([]string{
		strings.TrimSpace(string(scene)),
		toolExperienceObjectiveKey(objective),
		toolExperienceGapKey(gaps),
		string(tool),
	}, "|")
}

func toolExperienceObjectiveKey(objective string) string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(objective)))
	if len(parts) > 6 {
		parts = parts[:6]
	}
	return strings.Join(parts, "_")
}

func toolExperienceGapKey(gaps []string) string {
	if len(gaps) == 0 {
		return "no_gap"
	}
	normalized := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		gap = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(gap, " ", "_")))
		if gap == "" {
			continue
		}
		normalized = append(normalized, gap)
	}
	sort.Strings(normalized)
	if len(normalized) > 3 {
		normalized = normalized[:3]
	}
	return strings.Join(normalized, ",")
}

func toolExperienceIncidentShape(objective string) string {
	return toolExperienceObjectiveKey(objective)
}

func rollingAverage(current, value float64, count int) float64 {
	if count <= 1 {
		return value
	}
	return ((current * float64(count-1)) + value) / float64(count)
}
