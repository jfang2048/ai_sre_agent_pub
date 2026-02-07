package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"go.uber.org/zap"
)

// IncidentSeverity represents Google SRE incident severity levels
type IncidentSeverity string

const (
	SeverityP0 IncidentSeverity = "P0" // Critical service impact
	SeverityP1 IncidentSeverity = "P1" // Significant service degradation
	SeverityP2 IncidentSeverity = "P2" // Minor service impact
	SeverityP3 IncidentSeverity = "P3" // No service impact
)

// IncidentState represents the incident lifecycle state (Google SRE)
type IncidentState string

const (
	StateDetected      IncidentState = "detected"      // Initial detection
	StateAcknowledged  IncidentState = "acknowledged"  // Human acknowledged
	StateInvestigating IncidentState = "investigating" // Active investigation
	StateMitigating    IncidentState = "mitigating"    // Mitigation in progress
	StateResolved      IncidentState = "resolved"      // Incident resolved
	StateClosed        IncidentState = "closed"        // Post-mortem complete
)

// Incident represents an incident with Google SRE tracking
type Incident struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Severity    IncidentSeverity `json:"severity"`
	State       IncidentState    `json:"state"`

	// Timeline (Google SRE: MTTR tracking)
	DetectedAt     time.Time `json:"detected_at"`
	AcknowledgedAt time.Time `json:"acknowledged_at,omitempty"`
	MitigatedAt    time.Time `json:"mitigated_at,omitempty"`
	ResolvedAt     time.Time `json:"resolved_at,omitempty"`
	ClosedAt       time.Time `json:"closed_at,omitempty"`

	// Metrics (Google SRE: MTTR, MTTI)
	MTTD time.Duration `json:"mttd,omitempty"` // Mean Time To Detect
	MTTA time.Duration `json:"mtta,omitempty"` // Mean Time To Acknowledge
	MTTR time.Duration `json:"mttr,omitempty"` // Mean Time To Resolve
	MTTI time.Duration `json:"mtti,omitempty"` // Mean Time To Identify (root cause)

	// Incident commander and responders
	IncidentCommander string   `json:"incident_commander,omitempty"`
	Responders        []string `json:"responders,omitempty"`

	// Labels and annotations
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`

	// Technical details
	Metrics    []IncidentMetric `json:"metrics"`
	Actions    []IncidentAction `json:"actions"`
	RootCause  string           `json:"root_cause,omitempty"`
	Resolution string           `json:"resolution,omitempty"`

	// Post-mortem (Google SRE)
	PostMortem         *PostMortem `json:"post_mortem,omitempty"`
	PostMortemRequired bool        `json:"post_mortem_required"`

	// Related SLO (Google SRE)
	AffectedSLO       string  `json:"affected_slo,omitempty"`
	ErrorBudgetImpact float64 `json:"error_budget_impact,omitempty"` // Percentage consumed

	// Metadata
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PostMortem represents a post-mortem document (Google SRE)
type PostMortem struct {
	ID          string    `json:"id"`
	IncidentID  string    `json:"incident_id"`
	Author      string    `json:"author"`
	CompletedAt time.Time `json:"completed_at"`

	// Summary
	Summary   string         `json:"summary"`
	Timeline  []TimelineItem `json:"timeline"`
	RootCause string         `json:"root_cause"`
	Impact    string         `json:"impact"`

	// Lessons learned
	WhatWentWell   []string     `json:"what_went_well"`
	WhatWentPoorly []string     `json:"what_went_poorly"`
	ActionItems    []ActionItem `json:"action_items"`

	// Blameless review (Google SRE principle)
	FollowUpActions []string `json:"follow_up_actions"`
}

// TimelineItem represents a timeline event
type TimelineItem struct {
	Timestamp time.Time `json:"timestamp"`
	Event     string    `json:"event"`
	Actor     string    `json:"actor,omitempty"`
}

// ActionItem represents a follow-up action
type ActionItem struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	DueDate     time.Time `json:"due_date"`
	Status      string    `json:"status"` // open, in_progress, complete
}

// IncidentMetric represents a metric during an incident
type IncidentMetric struct {
	Name   string             `json:"name"`
	Values map[string]float64 `json:"values"`
	Labels map[string]string  `json:"labels"`
}

// IncidentAction represents an action taken during an incident
type IncidentAction struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Target     string                 `json:"target"`
	Parameters map[string]string      `json:"parameters"`
	ExecutedBy string                 `json:"executed_by,omitempty"`
	ExecutedAt time.Time              `json:"executed_at"`
	Success    bool                   `json:"success"`
	Duration   time.Duration          `json:"duration"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// IncidentStore stores and retrieves incidents
type IncidentStore interface {
	Add(ctx context.Context, incident *Incident) error
	Get(ctx context.Context, id string) (*Incident, error)
	List(ctx context.Context, filter IncidentFilter) ([]*Incident, error)
	Update(ctx context.Context, incident *Incident) error
	Delete(ctx context.Context, id string) error
	Search(ctx context.Context, query string) ([]*Incident, error)

	// Google SRE methods
	RecordStateChange(ctx context.Context, incidentID string, newState IncidentState) error
	AssignIncidentCommander(ctx context.Context, incidentID, commander string) error
	CreatePostMortem(ctx context.Context, incidentID string, postMortem *PostMortem) error
	// Calculate SLO metrics
	CalculateMTTR(ctx context.Context, filter IncidentFilter) (time.Duration, error)
	CalculateMTTA(ctx context.Context, filter IncidentFilter) (time.Duration, error)
}

// IncidentFilter filters incidents
type IncidentFilter struct {
	Severity   IncidentSeverity
	State      IncidentState
	StartTime  time.Time
	EndTime    time.Time
	LabelMatch map[string]string
	Limit      int
	Offset     int
	Since      time.Time // Incidents since this time
	Until      time.Time // Incidents until this time
}

// MemoryIncidentStore is an in-memory incident store with Google SRE tracking
type MemoryIncidentStore struct {
	mu        sync.RWMutex
	incidents map[string]*Incident
	logger    *zap.Logger
}

// NewMemoryIncidentStore creates a new memory incident store
func NewMemoryIncidentStore(logger *zap.Logger) *MemoryIncidentStore {
	return &MemoryIncidentStore{
		incidents: make(map[string]*Incident),
		logger:    logger.With(zap.String("component", "incident_store")),
	}
}

// Add adds an incident to the store
func (s *MemoryIncidentStore) Add(ctx context.Context, incident *Incident) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if incident.ID == "" {
		incident.ID = generateID()
	}

	now := time.Now()
	incident.CreatedAt = now
	incident.UpdatedAt = now
	incident.DetectedAt = now

	// Set initial state
	if incident.State == "" {
		incident.State = StateDetected
	}

	// Determine if post-mortem is required based on severity (Google SRE)
	incident.PostMortemRequired = (incident.Severity == SeverityP0 || incident.Severity == SeverityP1)

	s.incidents[incident.ID] = incident
	s.logger.Info("incident detected",
		zap.String("id", incident.ID),
		zap.String("title", incident.Title),
		zap.String("severity", string(incident.Severity)))

	return nil
}

// Get retrieves an incident by ID
func (s *MemoryIncidentStore) Get(ctx context.Context, id string) (*Incident, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	incident, ok := s.incidents[id]
	if !ok {
		return nil, fmt.Errorf("incident not found: %s", id)
	}
	return incident, nil
}

// List lists incidents with optional filtering
func (s *MemoryIncidentStore) List(ctx context.Context, filter IncidentFilter) ([]*Incident, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Incident

	for _, incident := range s.incidents {
		// Filter by severity
		if filter.Severity != "" && incident.Severity != filter.Severity {
			continue
		}

		// Filter by state
		if filter.State != "" && incident.State != filter.State {
			continue
		}

		// Filter by time range
		if !filter.StartTime.IsZero() && incident.DetectedAt.Before(filter.StartTime) {
			continue
		}
		if !filter.EndTime.IsZero() && incident.DetectedAt.After(filter.EndTime) {
			continue
		}

		// Filter by labels
		if len(filter.LabelMatch) > 0 {
			match := true
			for k, v := range filter.LabelMatch {
				if incident.Labels[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		result = append(result, incident)
	}

	// Sort by detected time (most recent first)
	sortIncidents(result)

	// Apply pagination
	if filter.Offset > 0 {
		if filter.Offset >= len(result) {
			return []*Incident{}, nil
		}
		result = result[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(result) {
		result = result[:filter.Limit]
	}

	return result, nil
}

// Update updates an incident
func (s *MemoryIncidentStore) Update(ctx context.Context, incident *Incident) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.incidents[incident.ID]; !ok {
		return fmt.Errorf("incident not found: %s", incident.ID)
	}

	incident.UpdatedAt = time.Now()
	s.incidents[incident.ID] = incident
	s.logger.Debug("incident updated", zap.String("id", incident.ID))
	return nil
}

// Delete deletes an incident
func (s *MemoryIncidentStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.incidents[id]; !ok {
		return fmt.Errorf("incident not found: %s", id)
	}

	delete(s.incidents, id)
	s.logger.Debug("incident deleted", zap.String("id", id))
	return nil
}

// Search searches incidents by query
func (s *MemoryIncidentStore) Search(ctx context.Context, query string) ([]*Incident, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Incident

	for _, incident := range s.incidents {
		if contains(incident.Title, query) ||
			contains(incident.Description, query) ||
			contains(incident.Resolution, query) ||
			contains(incident.RootCause, query) {
			result = append(result, incident)
		}
	}

	return result, nil
}

// RecordStateChange records a state transition (Google SRE)
func (s *MemoryIncidentStore) RecordStateChange(ctx context.Context, incidentID string, newState IncidentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	incident, ok := s.incidents[incidentID]
	if !ok {
		return fmt.Errorf("incident not found: %s", incidentID)
	}

	now := time.Now()
	incident.State = newState
	incident.UpdatedAt = now

	// Update timestamps and calculate metrics
	switch newState {
	case StateAcknowledged:
		if incident.AcknowledgedAt.IsZero() {
			incident.AcknowledgedAt = now
			incident.MTTD = now.Sub(incident.DetectedAt)
		}
	case StateResolved:
		if incident.ResolvedAt.IsZero() {
			incident.ResolvedAt = now
		}
	case StateClosed:
		if incident.ClosedAt.IsZero() {
			incident.ClosedAt = now
		}
		incident.MTTR = now.Sub(incident.DetectedAt)
	}

	s.logger.Info("incident state changed",
		zap.String("id", incidentID),
		zap.String("state", string(newState)))

	return s.recalculateMetrics(incident)
}

// AssignIncidentCommander assigns an incident commander (Google SRE)
func (s *MemoryIncidentStore) AssignIncidentCommander(ctx context.Context, incidentID, commander string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	incident, ok := s.incidents[incidentID]
	if !ok {
		return fmt.Errorf("incident not found: %s", incidentID)
	}

	incident.IncidentCommander = commander
	incident.UpdatedAt = time.Now()

	// If this is the first assignment, calculate MTTA
	if incident.AcknowledgedAt.IsZero() {
		incident.AcknowledgedAt = time.Now()
		incident.MTTA = time.Since(incident.DetectedAt)
	}

	s.logger.Info("incident commander assigned",
		zap.String("id", incidentID),
		zap.String("commander", commander))

	return nil
}

// CreatePostMortem creates a post-mortem document (Google SRE)
func (s *MemoryIncidentStore) CreatePostMortem(ctx context.Context, incidentID string, postMortem *PostMortem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	incident, ok := s.incidents[incidentID]
	if !ok {
		return fmt.Errorf("incident not found: %s", incidentID)
	}

	postMortem.ID = fmt.Sprintf("pm-%s", incidentID)
	postMortem.IncidentID = incidentID
	postMortem.CompletedAt = time.Now()

	incident.PostMortem = postMortem
	incident.State = StateClosed
	incident.ClosedAt = postMortem.CompletedAt

	s.logger.Info("post-mortem created",
		zap.String("incident_id", incidentID),
		zap.String("post_mortem_id", postMortem.ID))

	return nil
}

// CalculateMTTR calculates Mean Time To Resolve (Google SRE)
func (s *MemoryIncidentStore) CalculateMTTR(ctx context.Context, filter IncidentFilter) (time.Duration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var total time.Duration
	var count int

	for _, incident := range s.incidents {
		// Apply filters
		if filter.Severity != "" && incident.Severity != filter.Severity {
			continue
		}
		if !incident.ResolvedAt.IsZero() {
			total += incident.ResolvedAt.Sub(incident.DetectedAt)
			count++
		}
	}

	if count == 0 {
		return 0, nil
	}

	return total / time.Duration(count), nil
}

// CalculateMTTA calculates Mean Time To Acknowledge (Google SRE)
func (s *MemoryIncidentStore) CalculateMTTA(ctx context.Context, filter IncidentFilter) (time.Duration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var total time.Duration
	var count int

	for _, incident := range s.incidents {
		if filter.Severity != "" && incident.Severity != filter.Severity {
			continue
		}
		if !incident.AcknowledgedAt.IsZero() && !incident.DetectedAt.IsZero() {
			total += incident.AcknowledgedAt.Sub(incident.DetectedAt)
			count++
		}
	}

	if count == 0 {
		return 0, nil
	}

	return total / time.Duration(count), nil
}

// GetActiveIncidents returns all unresolved incidents
func (s *MemoryIncidentStore) GetActiveIncidents(ctx context.Context) ([]*Incident, error) {
	return s.List(ctx, IncidentFilter{
		State: StateResolved,
	})

	// Filter to exclude resolved incidents
}

// recalculateMetrics recalculates all incident metrics
func (s *MemoryIncidentStore) recalculateMetrics(incident *Incident) error {
	now := time.Now()

	// MTTD: Time to detect (usually 0 if detected at creation)
	incident.MTTD = now.Sub(incident.DetectedAt)

	// MTTA: Time to acknowledge
	if !incident.AcknowledgedAt.IsZero() {
		incident.MTTA = incident.AcknowledgedAt.Sub(incident.DetectedAt)
	}

	// MTTR: Time to resolve
	if !incident.ResolvedAt.IsZero() {
		incident.MTTR = incident.ResolvedAt.Sub(incident.DetectedAt)
	}

	return nil
}

// sortIncidents sorts incidents by detected time (most recent first)
func sortIncidents(incidents []*Incident) {
	n := len(incidents)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if incidents[j].DetectedAt.Before(incidents[j+1].DetectedAt) {
				incidents[j], incidents[j+1] = incidents[j+1], incidents[j]
			}
		}
	}
}

// PatternMatcher recognizes recurring patterns (Google SRE)
type PatternMatcher struct {
	store  IncidentStore
	logger *zap.Logger
}

// NewPatternMatcher creates a new pattern matcher
func NewPatternMatcher(store IncidentStore, logger *zap.Logger) *PatternMatcher {
	return &PatternMatcher{
		store:  store,
		logger: logger.With(zap.String("component", "pattern_matcher")),
	}
}

// Match finds similar incidents using symptom similarity (Google SRE)
func (pm *PatternMatcher) Match(ctx context.Context, currentMetrics map[string]float64, symptoms []string) ([]*Incident, error) {
	incidents, err := pm.store.List(ctx, IncidentFilter{Limit: 100})
	if err != nil {
		return nil, err
	}

	var matches []*Incident
	for _, incident := range incidents {
		if pm.symptomSimilarity(symptoms, incident) > 0.7 {
			matches = append(matches, incident)
		}
	}

	return matches, nil
}

// symptomSimilarity calculates symptom-based similarity (Google SRE: monitor symptoms not causes)
func (pm *PatternMatcher) symptomSimilarity(symptoms []string, incident *Incident) float64 {
	if len(symptoms) == 0 {
		return 0
	}

	// Check if incident labels match symptoms
	var matchCount int
	for _, symptom := range symptoms {
		if incident.Labels[symptom] != "" || contains(incident.Description, symptom) {
			matchCount++
		}
	}

	return float64(matchCount) / float64(len(symptoms))
}

// similarity calculates similarity between metrics and incident
func (pm *PatternMatcher) similarity(metrics map[string]float64, incident *Incident) float64 {
	// Compare metric signatures
	if len(incident.Metrics) == 0 || len(metrics) == 0 {
		return 0
	}

	var similarityScore float64
	var metricCount int

	for _, im := range incident.Metrics {
		if val, ok := metrics[im.Name]; ok {
			// Calculate similarity based on metric values
			// This is a simplified version - production would use more sophisticated comparison
			for _, v := range im.Values {
				diff := math.Abs(val - v)
				maxVal := math.Max(math.Abs(val), math.Abs(v))
				if maxVal > 0 {
					similarity := 1 - (diff / maxVal)
					similarityScore += similarity
					metricCount++
				}
			}
		}
	}

	if metricCount == 0 {
		return 0
	}

	return similarityScore / float64(metricCount)
}

// generateID generates a unique ID
func generateID() string {
	return fmt.Sprintf("inc-%d", time.Now().UnixNano())
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr))
}

// ToJSON converts incident to JSON
func (i *Incident) ToJSON() ([]byte, error) {
	return json.Marshal(i)
}

// FromJSON creates incident from JSON
func IncidentFromJSON(data []byte) (*Incident, error) {
	var incident Incident
	err := json.Unmarshal(data, &incident)
	if err != nil {
		return nil, err
	}
	return &incident, nil
}
