package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/change"
	"github.com/jfang2048/ai_sre_agent_pub/internal/store"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/utils"
	"go.uber.org/zap"
)

// SREManager manages SRE features (incidents, changes, SLOs)
type SREManager struct {
	logger        *zap.Logger
	incidentStore store.IncidentStore
	changeMgr     *change.ChangeManager
	history       *MetricsHistory

	// In-memory state
	mu              sync.RWMutex
	activeIncidents []*store.Incident
	activeChanges   []*change.Change
	sloViolations   []SLOViolation
}

// SLOViolation represents an SLO violation event
type SLOViolation struct {
	ID           string    `json:"id"`
	SLOName      string    `json:"slo_name"`
	SLOID        string    `json:"slo_id"`
	CurrentValue float64   `json:"current_value"`
	Target       float64   `json:"target"`
	Severity     string    `json:"severity"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time,omitempty"`
	Resolved     bool      `json:"resolved"`
}

// NewSREManager creates a new SRE manager
func NewSREManager(logger *zap.Logger) *SREManager {
	return &SREManager{
		logger:          logger.With(zap.String("component", "sre_manager")),
		incidentStore:   store.NewMemoryIncidentStore(logger),
		changeMgr:       change.NewChangeManager(logger),
		history:         NewMetricsHistory(4096), // Ring buffer (O(1) push), tuned for dashboards
		activeIncidents: make([]*store.Incident, 0),
		activeChanges:   make([]*change.Change, 0),
		sloViolations:   make([]SLOViolation, 0),
	}
}

// Start starts the SRE manager
func (m *SREManager) Start(ctx context.Context) error {
	m.logger.Info("starting SRE manager")
	return nil
}

// Stop stops the SRE manager
func (m *SREManager) Stop() error {
	m.logger.Info("stopping SRE manager")
	return nil
}

// RecordMetrics records metrics in history
func (m *SREManager) RecordMetrics(metrics map[string]float64) {
	m.history.Add(metrics)
}

// GetHistory returns the metrics history
func (m *SREManager) GetHistory() *MetricsHistory {
	return m.history
}

// Incident management methods

// CreateIncident creates a new incident
func (m *SREManager) CreateIncident(incident *store.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx := context.Background()
	if err := m.incidentStore.Add(ctx, incident); err != nil {
		return err
	}

	if incident.State != store.StateResolved && incident.State != store.StateClosed {
		m.activeIncidents = append(m.activeIncidents, incident)
	}

	m.logger.Info("incident created",
		zap.String("id", incident.ID),
		zap.String("severity", string(incident.Severity)))

	return nil
}

// GetIncidents returns all incidents
func (m *SREManager) GetIncidents() ([]*store.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ctx := context.Background()
	return m.incidentStore.List(ctx, store.IncidentFilter{Limit: 100})
}

// GetIncident returns a specific incident
func (m *SREManager) GetIncident(id string) (*store.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ctx := context.Background()
	return m.incidentStore.Get(ctx, id)
}

// UpdateIncident updates an incident
func (m *SREManager) UpdateIncident(incident *store.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx := context.Background()
	if err := m.incidentStore.Update(ctx, incident); err != nil {
		return err
	}

	// Update active incidents list
	if incident.State == store.StateResolved || incident.State == store.StateClosed {
		m.activeIncidents = filterIncidents(m.activeIncidents, incident.ID)
	}

	return nil
}

// ResolveIncident resolves an incident
func (m *SREManager) ResolveIncident(id, resolution string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx := context.Background()
	if err := m.incidentStore.RecordStateChange(ctx, id, store.StateResolved); err != nil {
		return err
	}

	incident, err := m.incidentStore.Get(ctx, id)
	if err != nil {
		return err
	}

	incident.Resolution = resolution
	m.incidentStore.Update(ctx, incident)

	m.activeIncidents = filterIncidents(m.activeIncidents, id)

	m.logger.Info("incident resolved", zap.String("id", id))
	return nil
}

// AssignIncidentCommander assigns an incident commander
func (m *SREManager) AssignIncidentCommander(incidentID, commander string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx := context.Background()
	return m.incidentStore.AssignIncidentCommander(ctx, incidentID, commander)
}

// Change management methods

// CreateChange creates a new change request
func (m *SREManager) CreateChange(chg *change.Change) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.changeMgr.RegisterChange(chg); err != nil {
		return err
	}

	m.activeChanges = append(m.activeChanges, chg)

	m.logger.Info("change created",
		zap.String("id", chg.ID),
		zap.String("title", chg.Title))

	return nil
}

// GetChanges returns all changes
func (m *SREManager) GetChanges() ([]*change.Change, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return all changes from manager
	var result []*change.Change
	for _, ch := range m.activeChanges {
		result = append(result, ch)
	}
	return result, nil
}

// GetChange returns a specific change
func (m *SREManager) GetChange(id string) (*change.Change, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.changeMgr.GetChange(id)
}

// ApproveChange approves a change request
func (m *SREManager) ApproveChange(changeID, approver string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.changeMgr.ApproveChange(changeID, approver)
}

// RejectChange rejects a change request
func (m *SREManager) RejectChange(changeID, rejector, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.changeMgr.RejectChange(changeID, rejector, reason)
}

// StartChange starts executing a change
func (m *SREManager) StartChange(ctx context.Context, changeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.changeMgr.StartChange(ctx, changeID)
}

// CompleteChange marks a change as complete
func (m *SREManager) CompleteChange(changeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.changeMgr.CompleteChange(changeID)
}

// RollbackChange rolls back a change
func (m *SREManager) RollbackChange(ctx context.Context, changeID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.changeMgr.RollbackChange(ctx, changeID, reason)
}

// UpdateCanaryMetrics updates canary deployment metrics
func (m *SREManager) UpdateCanaryMetrics(changeID string, metrics map[string]float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.changeMgr.UpdateCanaryMetrics(changeID, metrics)
}

// RecordSLOViolation records an SLO violation
func (m *SREManager) RecordSLOViolation(violation SLOViolation) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if violation.ID == "" {
		violation.ID = fmt.Sprintf("vio-%d", time.Now().UnixNano())
	}
	if violation.StartTime.IsZero() {
		violation.StartTime = time.Now()
	}

	m.sloViolations = append(m.sloViolations, violation)

	// Keep only last 100 violations
	if len(m.sloViolations) > 100 {
		m.sloViolations = m.sloViolations[1:]
	}
}

// GetSLOViolations returns SLO violations
func (m *SREManager) GetSLOViolations() []SLOViolation {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]SLOViolation, len(m.sloViolations))
	copy(result, m.sloViolations)
	return result
}

// GetMTTR calculates Mean Time To Resolve
func (m *SREManager) GetMTTR(severity store.IncidentSeverity) (time.Duration, error) {
	ctx := context.Background()
	return m.incidentStore.(store.IncidentStore).CalculateMTTR(ctx, store.IncidentFilter{Severity: severity})
}

// GetMTTA calculates Mean Time To Acknowledge
func (m *SREManager) GetMTTA(severity store.IncidentSeverity) (time.Duration, error) {
	ctx := context.Background()
	return m.incidentStore.(store.IncidentStore).CalculateMTTA(ctx, store.IncidentFilter{Severity: severity})
}

// GetChangeManager returns the change manager
func (m *SREManager) GetChangeManager() *change.ChangeManager {
	return m.changeMgr
}

// GetIncidentStore returns the incident store
func (m *SREManager) GetIncidentStore() store.IncidentStore {
	return m.incidentStore
}

// Helper function to filter incidents
func filterIncidents(incidents []*store.Incident, id string) []*store.Incident {
	result := make([]*store.Incident, 0)
	for _, inc := range incidents {
		if inc.ID != id {
			result = append(result, inc)
		}
	}
	return result
}

// HTTP Handlers for SRE API

// withCORS wraps a handler with CORS headers
func withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		h(w, r)
	}
}

// RegisterSREHandlers registers SRE API endpoints
func (m *SREManager) RegisterSREHandlers(mux *http.ServeMux) {
	// Incident endpoints
	mux.HandleFunc("/api/v1/incidents", withCORS(m.handleIncidents))
	mux.HandleFunc("/api/v1/incidents/", withCORS(m.handleIncidentByID))

	// Change endpoints
	mux.HandleFunc("/api/v1/changes", withCORS(m.handleChanges))
	mux.HandleFunc("/api/v1/changes/", withCORS(m.handleChangeByID))

	// SLO endpoints
	mux.HandleFunc("/api/v1/slo/violations", withCORS(m.handleSLOViolations))
	mux.HandleFunc("/api/v1/slo/summary", withCORS(m.handleSLOSummary))

	// Metrics history endpoint
	mux.HandleFunc("/api/v1/metrics/history", withCORS(m.handleMetricsHistory))

	// Dashboard summary endpoint
	mux.HandleFunc("/api/v1/dashboard", withCORS(m.handleDashboard))
}

// handleIncidents handles incident list and creation
func (m *SREManager) handleIncidents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		incidents, err := m.GetIncidents()
		if err != nil {
			utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		utils.WriteJSON(w, http.StatusOK, incidents)

	case "POST":
		var incident store.Incident
		if err := json.NewDecoder(r.Body).Decode(&incident); err != nil {
			utils.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}

		if incident.ID == "" {
			incident.ID = fmt.Sprintf("inc-%d", time.Now().UnixNano())
		}
		if incident.CreatedAt.IsZero() {
			incident.CreatedAt = time.Now()
		}
		if incident.DetectedAt.IsZero() {
			incident.DetectedAt = time.Now()
		}
		if incident.State == "" {
			incident.State = store.StateDetected
		}

		if err := m.CreateIncident(&incident); err != nil {
			utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		utils.WriteJSON(w, http.StatusCreated, incident)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleIncidentByID handles single incident operations
func (m *SREManager) handleIncidentByID(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path
	id := r.URL.Path[len("/api/v1/incidents/"):]
	if id == "" {
		http.Error(w, "Incident ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		incident, err := m.GetIncident(id)
		if err != nil {
			utils.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "incident not found"})
			return
		}
		utils.WriteJSON(w, http.StatusOK, incident)

	case "PUT":
		var incident store.Incident
		if err := json.NewDecoder(r.Body).Decode(&incident); err != nil {
			utils.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		incident.ID = id

		if err := m.UpdateIncident(&incident); err != nil {
			utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		utils.WriteJSON(w, http.StatusOK, incident)

	case "POST":
		// Handle actions
		action := r.URL.Query().Get("action")
		switch action {
		case "resolve":
			var req struct {
				Resolution string `json:"resolution"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				utils.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}
			if err := m.ResolveIncident(id, req.Resolution); err != nil {
				utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "resolved"})

		case "assign":
			var req struct {
				Commander string `json:"commander"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				utils.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}
			if err := m.AssignIncidentCommander(id, req.Commander); err != nil {
				utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "assigned"})

		default:
			http.Error(w, "Unknown action", http.StatusBadRequest)
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleChanges handles change list and creation
func (m *SREManager) handleChanges(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		changes, err := m.GetChanges()
		if err != nil {
			utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		utils.WriteJSON(w, http.StatusOK, changes)

	case "POST":
		var chg change.Change
		if err := json.NewDecoder(r.Body).Decode(&chg); err != nil {
			utils.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}

		if chg.ID == "" {
			chg.ID = fmt.Sprintf("chg-%d", time.Now().UnixNano())
		}

		if err := m.CreateChange(&chg); err != nil {
			utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		utils.WriteJSON(w, http.StatusCreated, chg)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleChangeByID handles single change operations
func (m *SREManager) handleChangeByID(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path
	id := r.URL.Path[len("/api/v1/changes/"):]
	if id == "" {
		http.Error(w, "Change ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		chg, err := m.GetChange(id)
		if err != nil {
			utils.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "change not found"})
			return
		}
		utils.WriteJSON(w, http.StatusOK, chg)

	case "POST":
		// Handle actions
		action := r.URL.Query().Get("action")
		ctx := context.Background()

		switch action {
		case "approve":
			var req struct {
				Approver string `json:"approver"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				utils.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}
			if err := m.ApproveChange(id, req.Approver); err != nil {
				utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "approved"})

		case "reject":
			var req struct {
				Rejector string `json:"rejector"`
				Reason   string `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				utils.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}
			if err := m.RejectChange(id, req.Rejector, req.Reason); err != nil {
				utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "rejected"})

		case "start":
			if err := m.StartChange(ctx, id); err != nil {
				utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})

		case "complete":
			if err := m.CompleteChange(id); err != nil {
				utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "completed"})

		case "rollback":
			var req struct {
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				utils.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}
			if err := m.RollbackChange(ctx, id, req.Reason); err != nil {
				utils.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "rolled_back"})

		default:
			http.Error(w, "Unknown action", http.StatusBadRequest)
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSLOViolations returns SLO violations
func (m *SREManager) handleSLOViolations(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	violations := m.GetSLOViolations()
	utils.WriteJSON(w, http.StatusOK, violations)
}

// handleSLOSummary returns SLO summary
func (m *SREManager) handleSLOSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	summary := map[string]interface{}{
		"violations_count": len(m.sloViolations),
		"active_incidents": len(m.activeIncidents),
		"active_changes":   len(m.activeChanges),
	}

	// Add MTTR/MTTA if available
	if mttr, err := m.GetMTTR(store.SeverityP0); err == nil && mttr > 0 {
		summary["mttr_p0_minutes"] = mttr.Minutes()
	}
	if mtta, err := m.GetMTTA(store.SeverityP0); err == nil && mtta > 0 {
		summary["mtta_p0_minutes"] = mtta.Minutes()
	}

	utils.WriteJSON(w, http.StatusOK, summary)
}

// handleMetricsHistory returns metrics history for charts
func (m *SREManager) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	query := r.URL.Query()

	// Get duration (default: 1 hour)
	durationStr := query.Get("duration")
	duration := time.Hour
	if durationStr != "" {
		if d, err := time.ParseDuration(durationStr); err == nil {
			duration = d
		}
	}

	// Get metric names (optional)
	metricNames := query["metric"]

	// Get samples
	since := time.Now().Add(-duration)
	if len(metricNames) == 0 {
		// Return all samples
		samples := m.history.GetSince(since)
		utils.WriteJSON(w, http.StatusOK, samples)
		return
	}

	// Return specific metrics
	result := make(map[string][]MetricPoint)
	for _, name := range metricNames {
		result[name] = m.history.GetMetricHistory(name, since)
	}

	utils.WriteJSON(w, http.StatusOK, result)
}

// handleDashboard returns dashboard summary
func (m *SREManager) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	dashboard := map[string]interface{}{
		"active_incidents": m.activeIncidents,
		"active_changes":   m.activeChanges,
		"slo_violations":   m.sloViolations,
		"incident_stats": map[string]int{
			"p0": countIncidentsBySeverity(m.activeIncidents, store.SeverityP0),
			"p1": countIncidentsBySeverity(m.activeIncidents, store.SeverityP1),
			"p2": countIncidentsBySeverity(m.activeIncidents, store.SeverityP2),
			"p3": countIncidentsBySeverity(m.activeIncidents, store.SeverityP3),
		},
		"change_stats": map[string]int{
			"planned":     countChangesByStatus(m.activeChanges, change.StatusPlanned),
			"in_progress": countChangesByStatus(m.activeChanges, change.StatusInProgress),
			"completed":   countChangesByStatus(m.activeChanges, change.StatusCompleted),
			"rolled_back": countChangesByStatus(m.activeChanges, change.StatusRolledBack),
		},
	}

	utils.WriteJSON(w, http.StatusOK, dashboard)
}

func countIncidentsBySeverity(incidents []*store.Incident, severity store.IncidentSeverity) int {
	count := 0
	for _, inc := range incidents {
		if inc.Severity == severity {
			count++
		}
	}
	return count
}

func countChangesByStatus(changes []*change.Change, status change.ChangeStatus) int {
	count := 0
	for _, ch := range changes {
		if ch.Status == status {
			count++
		}
	}
	return count
}
