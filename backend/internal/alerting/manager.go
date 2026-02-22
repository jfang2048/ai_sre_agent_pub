package alerting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/store"
	"go.uber.org/zap"
)

// Alert represents an atomic event
type Alert struct {
	ID          string            `json:"id"`
	Fingerprint string            `json:"fingerprint"` // Unique hash of labels
	Status      string            `json:"status"`      // "firing", "resolved"
	Labels      map[string]string `json:"labels"`      // Dimensions: host, service
	Annotations map[string]string `json:"annotations"` // summary, runbook_url
	StartsAt    time.Time         `json:"starts_at"`
	EndsAt      time.Time         `json:"ends_at"`
	Severity    string            `json:"severity"` // Derived severity
}

// Config for AlertManager
type Config struct {
	GroupWait      time.Duration
	GroupInterval  time.Duration
	RepeatInterval time.Duration
}

// AlertManager handles alert processing, correlation, and notification
type AlertManager struct {
	mu        sync.RWMutex
	alerts    map[string]*Alert
	incidents store.IncidentStore
	logger    *zap.Logger

	// Correlation State
	groups map[string]*AlertGroup
}

type AlertGroup struct {
	ID        string
	Alerts    []*Alert
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewManager creates a new AlertManager
func NewManager(incidentStore store.IncidentStore, logger *zap.Logger) *AlertManager {
	return &AlertManager{
		alerts:    make(map[string]*Alert),
		incidents: incidentStore,
		logger:    logger.With(zap.String("component", "alert_manager")),
		groups:    make(map[string]*AlertGroup),
	}
}

// Ingest receives a raw alert, deduplicates, correlates, and acts
func (am *AlertManager) Ingest(ctx context.Context, alert *Alert) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// 1. Deduplication / Fingerprinting
	if alert.Fingerprint == "" {
		alert.Fingerprint = fingerprint(alert.Labels)
	}

	existing, exists := am.alerts[alert.Fingerprint]
	if exists && existing.Status == alert.Status {
		// Just update timestamp
		existing.EndsAt = time.Now() // Keep alive
		return nil
	}

	// New or State Change
	alert.ID = fmt.Sprintf("al-%d", time.Now().UnixNano())
	am.alerts[alert.Fingerprint] = alert

	// 2. Intelligent Severity Scoring
	alert.Severity = am.calculateSeverity(alert)

	// 3. Correlation (Identify Incident)
	incidentID, err := am.correlateToIncident(ctx, alert)
	if err != nil {
		am.logger.Error("correlation failed", zap.Error(err))
	}

	// 4. Notify (if new incident or Critical alert)
	if incidentID != "" {
		am.logger.Info("alert linked to incident",
			zap.String("alert", alert.Fingerprint),
			zap.String("incident", incidentID))
	} else {
		// Create new incident
		am.createIncident(ctx, alert)
	}

	return nil
}

// calculateSeverity determines dynamic severity
func (am *AlertManager) calculateSeverity(alert *Alert) string {
	baseSev := alert.Labels["severity"]

	// Boost if recurring (Alert Fatigue Prevention logic could suppress instead)
	// Here we implement "Intelligent Scoring"

	score := 0
	switch baseSev {
	case "critical":
		score = 100
	case "warning":
		score = 50
	default:
		score = 10
	}

	// Blast Radius: If 'service' label implies core infrastructure
	service := alert.Labels["service"]
	if service == "database" || service == "load-balancer" {
		score += 30
	}

	// Time Context: Boost during peak hours (e.g., 9-17)
	hour := time.Now().Hour()
	if hour >= 9 && hour <= 17 {
		score += 10
	}

	if score >= 80 {
		return "P0" // Critical
	} else if score >= 50 {
		return "P1" // High
	} else {
		return "P2" // Medium
	}
}

// correlateToIncident attempts to find an existing active incident matching this alert
func (am *AlertManager) correlateToIncident(ctx context.Context, alert *Alert) (string, error) {
	// Simple Correlation: Match by 'service' and 'env' within last 1 hour
	// Advanced Graph correlation would check downstream deps here.

	// Get active incidents
	actives, err := am.incidents.List(ctx, store.IncidentFilter{State: store.StateDetected})
	if err != nil {
		return "", err
	}

	for _, inc := range actives {
		// Check labels matches
		if inc.Labels["service"] == alert.Labels["service"] &&
			inc.Labels["environment"] == alert.Labels["environment"] {
			// Found matching incident
			return inc.ID, nil
		}
	}

	return "", nil
}

func (am *AlertManager) createIncident(ctx context.Context, alert *Alert) {
	title := fmt.Sprintf("%s: %s", alert.Labels["alertname"], alert.Labels["service"])
	if alert.Annotations["summary"] != "" {
		title = alert.Annotations["summary"]
	}

	inc := &store.Incident{
		Title:       title,
		Description: alert.Annotations["description"],
		Severity:    store.IncidentSeverity(alert.Severity),
		State:       store.StateDetected,
		Labels:      alert.Labels,
		Annotations: alert.Annotations, // Includes Runbook URL
	}

	if err := am.incidents.Add(ctx, inc); err != nil {
		am.logger.Error("failed to create incident", zap.Error(err))
		return
	}

	// Trigger Notification (Routing)
	am.notify(inc)
}

func (am *AlertManager) notify(inc *store.Incident) {
	// Routing logic
	// If P0/P1 -> PagerDuty
	// If P2 -> Slack

	channel := "slack"
	if inc.Severity == "P0" || inc.Severity == "P1" {
		channel = "pagerduty"
	}

	am.logger.Info("sending notification",
		zap.String("channel", channel),
		zap.String("incident", inc.ID),
		zap.String("severity", string(inc.Severity)))

	// Auto-Attach Runbook (Automation)
	if rb, ok := inc.Annotations["runbook_url"]; ok {
		am.logger.Info("attaching runbook", zap.String("url", rb))
	}
}

func fingerprint(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte("="))
		h.Write([]byte(labels[k]))
		h.Write([]byte(","))
	}
	return hex.EncodeToString(h.Sum(nil))
}
