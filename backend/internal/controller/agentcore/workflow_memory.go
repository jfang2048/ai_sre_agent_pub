package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidentmemory"
	"go.uber.org/zap"
)

// WorkflowMemoryActionOutcome captures the stored result of a remediation or other action.
type WorkflowMemoryActionOutcome struct {
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

// WorkflowMemoryOperatorFeedback captures optional human feedback about the incident or action path.
type WorkflowMemoryOperatorFeedback struct {
	Actor     string    `json:"actor,omitempty"`
	Verdict   string    `json:"verdict,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// WorkflowMemoryRecord captures a durable incident or action memory artifact.
type WorkflowMemoryRecord struct {
	RecordID            string                           `json:"record_id"`
	WorkflowID          string                           `json:"workflow_id"`
	IncidentID          string                           `json:"incident_id,omitempty"`
	WorkflowType        string                           `json:"workflow_type"`
	CollectorID         string                           `json:"collector_id,omitempty"`
	Status              string                           `json:"status,omitempty"`
	Title               string                           `json:"title"`
	Summary             string                           `json:"summary"`
	RootCauseEntity     string                           `json:"root_cause_entity,omitempty"`
	MostLikelyCause     string                           `json:"most_likely_cause,omitempty"`
	ResolutionSummary   string                           `json:"resolution_summary,omitempty"`
	VerificationSummary string                           `json:"verification_summary,omitempty"`
	CausalPath          []string                         `json:"causal_path,omitempty"`
	ImpactScope         []string                         `json:"impact_scope,omitempty"`
	LessonsLearned      []string                         `json:"lessons_learned,omitempty"`
	Signals             []string                         `json:"signals,omitempty"`
	Actions             []string                         `json:"actions,omitempty"`
	EvidenceIDs         []string                         `json:"evidence_ids,omitempty"`
	Tags                []string                         `json:"tags,omitempty"`
	Metadata            map[string]string                `json:"metadata,omitempty"`
	Timeline            []RCATimelineEvent               `json:"timeline,omitempty"`
	Hypotheses          []RCAHypothesis                  `json:"hypotheses,omitempty"`
	PlanSteps           []AgentPlanStep                  `json:"plan_steps,omitempty"`
	ActionOutcomes      []WorkflowMemoryActionOutcome    `json:"action_outcomes,omitempty"`
	OperatorFeedback    []WorkflowMemoryOperatorFeedback `json:"operator_feedback,omitempty"`
	CreatedAt           time.Time                        `json:"created_at"`
	UpdatedAt           time.Time                        `json:"updated_at"`
}

// WorkflowMemoryStore persists high-value workflow outcomes for retrieval.
type WorkflowMemoryStore struct {
	store *incidentmemory.Store
}

func NewWorkflowMemoryStore(rootPath string, logger *zap.Logger) *WorkflowMemoryStore {
	return &WorkflowMemoryStore{
		store: incidentmemory.NewStore(rootPath, logger),
	}
}

func (s *WorkflowMemoryStore) Append(record WorkflowMemoryRecord) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("workflow memory store is nil")
	}
	return s.store.Append(record.toIncidentMemory())
}

func (s *WorkflowMemoryStore) Query(query, intent, collectorID string, topK int) []RetrievedDocumentEvidence {
	if s == nil || s.store == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	matches := s.store.Query(query, incidentmemory.QueryOptions{
		Intent:      intent,
		CollectorID: collectorID,
		TopK:        topK,
	})
	out := make([]RetrievedDocumentEvidence, 0, len(matches))
	for idx, match := range matches {
		record := match.Record
		metadata := map[string]string{
			"workflow_id":       record.WorkflowID,
			"collector_id":      record.CollectorID,
			"incident_id":       record.IncidentID,
			"workflow_type":     record.WorkflowType,
			"status":            record.Status,
			"root_cause_entity": record.RootCauseEntity,
		}
		if len(match.Reasons) > 0 {
			metadata["match_reasons"] = strings.Join(match.Reasons, "; ")
		}
		if summary := strings.TrimSpace(record.VerificationSummary); summary != "" {
			metadata["verification_summary"] = truncateString(summary, 160)
		}
		out = append(out, RetrievedDocumentEvidence{
			EvidenceID:       fmt.Sprintf("ev-memory-%s-%02d", sanitizeID(record.RecordID), idx+1),
			DocID:            record.RecordID,
			ChunkID:          record.RecordID,
			Title:            record.Title,
			SourcePath:       filepath.Join("incident_memory", record.RecordID+".json"),
			SourceType:       "incident_memory",
			KnowledgeType:    "historical_incident",
			CaseType:         "historical_incident",
			Summary:          record.Summary,
			Snippet:          match.Snippet,
			Score:            match.Score,
			Evidence:         append([]string(nil), record.Actions...),
			LikelyCauses:     compactStrings(record.MostLikelyCause),
			RemediationSteps: append([]string(nil), record.Actions...),
			Signals:          append([]string(nil), record.Signals...),
			Tags:             append([]string(nil), record.Tags...),
			Metadata:         metadata,
		})
	}
	return out
}

func (r WorkflowMemoryRecord) toIncidentMemory() incidentmemory.Record {
	record := incidentmemory.Record{
		RecordID:            r.RecordID,
		WorkflowID:          r.WorkflowID,
		IncidentID:          r.IncidentID,
		WorkflowType:        r.WorkflowType,
		CollectorID:         r.CollectorID,
		Status:              r.Status,
		Title:               r.Title,
		Summary:             r.Summary,
		RootCauseEntity:     r.RootCauseEntity,
		MostLikelyCause:     r.MostLikelyCause,
		ResolutionSummary:   r.ResolutionSummary,
		VerificationSummary: r.VerificationSummary,
		CausalPath:          append([]string(nil), r.CausalPath...),
		ImpactScope:         append([]string(nil), r.ImpactScope...),
		LessonsLearned:      append([]string(nil), r.LessonsLearned...),
		Signals:             append([]string(nil), r.Signals...),
		Actions:             append([]string(nil), r.Actions...),
		EvidenceIDs:         append([]string(nil), r.EvidenceIDs...),
		Tags:                append([]string(nil), r.Tags...),
		Metadata:            cloneStringMap(r.Metadata),
		CreatedAt:           r.CreatedAt,
		UpdatedAt:           r.UpdatedAt,
	}
	if len(r.Timeline) > 0 {
		record.Timeline = make([]incidentmemory.TimelineEvent, 0, len(r.Timeline))
		for _, event := range r.Timeline {
			record.Timeline = append(record.Timeline, incidentmemory.TimelineEvent{
				Timestamp: event.Timestamp,
				Phase:     event.Phase,
				Summary:   event.Summary,
			})
		}
	}
	if len(r.Hypotheses) > 0 {
		record.Hypotheses = make([]incidentmemory.Hypothesis, 0, len(r.Hypotheses))
		for _, hypothesis := range r.Hypotheses {
			record.Hypotheses = append(record.Hypotheses, incidentmemory.Hypothesis{
				ID:          hypothesis.ID,
				Title:       hypothesis.Title,
				Confidence:  hypothesis.Confidence,
				EvidenceIDs: append([]string(nil), hypothesis.EvidenceIDs...),
			})
		}
	}
	if len(r.ActionOutcomes) > 0 {
		record.ActionOutcomes = make([]incidentmemory.ActionOutcome, 0, len(r.ActionOutcomes))
		for _, outcome := range r.ActionOutcomes {
			record.ActionOutcomes = append(record.ActionOutcomes, incidentmemory.ActionOutcome{
				ActionID:        outcome.ActionID,
				Action:          outcome.Action,
				Status:          outcome.Status,
				Verification:    outcome.Verification,
				RollbackStatus:  outcome.RollbackStatus,
				Success:         outcome.Success,
				Useful:          outcome.Useful,
				ExecutedAt:      outcome.ExecutedAt,
				CompletedAt:     outcome.CompletedAt,
				OperatorComment: outcome.OperatorComment,
			})
		}
	}
	if len(r.OperatorFeedback) > 0 {
		record.OperatorFeedback = make([]incidentmemory.OperatorFeedback, 0, len(r.OperatorFeedback))
		for _, feedback := range r.OperatorFeedback {
			record.OperatorFeedback = append(record.OperatorFeedback, incidentmemory.OperatorFeedback{
				Actor:     feedback.Actor,
				Verdict:   feedback.Verdict,
				Notes:     feedback.Notes,
				Timestamp: feedback.Timestamp,
			})
		}
	}
	return record
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func nonZeroTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}
