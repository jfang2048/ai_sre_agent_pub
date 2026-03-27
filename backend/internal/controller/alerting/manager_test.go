package alerting

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── fingerprint tests ──────────────────────────────────────────────────────

func TestFingerprintDeterministic(t *testing.T) {
	labels := map[string]string{"service": "api", "host": "node-1"}
	fp1 := fingerprint(labels)
	fp2 := fingerprint(labels)
	assert.Equal(t, fp1, fp2, "same labels should produce identical fingerprint")
}

func TestFingerprintOrderIndependent(t *testing.T) {
	a := fingerprint(map[string]string{"b": "2", "a": "1"})
	b := fingerprint(map[string]string{"a": "1", "b": "2"})
	assert.Equal(t, a, b, "label insertion order must not affect fingerprint")
}

func TestFingerprintDiffersForDifferentLabels(t *testing.T) {
	fp1 := fingerprint(map[string]string{"service": "api"})
	fp2 := fingerprint(map[string]string{"service": "database"})
	assert.NotEqual(t, fp1, fp2)
}

func TestFingerprintEmptyLabels(t *testing.T) {
	fp := fingerprint(map[string]string{})
	assert.NotEmpty(t, fp, "empty labels should still produce a fingerprint (empty hash)")
}

// ── calculateSeverity tests ────────────────────────────────────────────────

func TestCalculateSeverityCriticalInfra(t *testing.T) {
	am := NewManager(store.NewMemoryIncidentStore(zap.NewNop()), zap.NewNop())
	alert := &Alert{
		Labels: map[string]string{
			"severity": "critical",
			"service":  "database",
		},
	}
	sev := am.calculateSeverity(alert)
	assert.Equal(t, "P0", sev, "critical+database should be P0")
}

func TestCalculateSeverityWarningNonInfra(t *testing.T) {
	am := NewManager(store.NewMemoryIncidentStore(zap.NewNop()), zap.NewNop())
	alert := &Alert{
		Labels: map[string]string{
			"severity": "warning",
			"service":  "frontend",
		},
	}
	sev := am.calculateSeverity(alert)
	assert.Contains(t, []string{"P1", "P2"}, sev, "warning+frontend should be P1 or P2 depending on time of day")
}

func TestCalculateSeverityUnknownSeverityLabel(t *testing.T) {
	am := NewManager(store.NewMemoryIncidentStore(zap.NewNop()), zap.NewNop())
	alert := &Alert{
		Labels: map[string]string{
			"severity": "info",
			"service":  "micro-svc",
		},
	}
	sev := am.calculateSeverity(alert)
	assert.Equal(t, "P2", sev, "unknown severity + normal service should be P2")
}

// ── Ingest tests ──────────────────────────────────────────────────────────

func TestIngestCreatesIncident(t *testing.T) {
	incStore := store.NewMemoryIncidentStore(zap.NewNop())
	am := NewManager(incStore, zap.NewNop())
	ctx := context.Background()

	alert := &Alert{
		Status: "firing",
		Labels: map[string]string{
			"alertname":   "HighCPU",
			"service":     "api",
			"environment": "staging",
			"severity":    "warning",
		},
		Annotations: map[string]string{
			"summary":     "CPU is too high",
			"description": "staging api is burning CPU",
		},
		StartsAt: time.Now(),
	}

	err := am.Ingest(ctx, alert)
	require.NoError(t, err)

	// Alert should have an ID and fingerprint assigned
	assert.NotEmpty(t, alert.ID, "Ingest should assign an ID")
	assert.NotEmpty(t, alert.Fingerprint, "Ingest should compute a fingerprint")

	// An incident should have been created in the store
	incidents, err := incStore.List(ctx, store.IncidentFilter{})
	require.NoError(t, err)
	assert.Len(t, incidents, 1, "Exactly one incident should be created")
	assert.Equal(t, "CPU is too high", incidents[0].Title, "Incident should use annotation summary as title")
}

func TestIngestDeduplicatesSameStatus(t *testing.T) {
	incStore := store.NewMemoryIncidentStore(zap.NewNop())
	am := NewManager(incStore, zap.NewNop())
	ctx := context.Background()

	alert := &Alert{
		Status: "firing",
		Labels: map[string]string{"service": "api"},
	}

	err := am.Ingest(ctx, alert)
	require.NoError(t, err)

	// Ingest the same alert again (same labels, same status)
	alert2 := &Alert{
		Status: "firing",
		Labels: map[string]string{"service": "api"},
	}
	err = am.Ingest(ctx, alert2)
	require.NoError(t, err)

	// Should still only have one incident (deduplication)
	incidents, err := incStore.List(ctx, store.IncidentFilter{})
	require.NoError(t, err)
	assert.Len(t, incidents, 1, "Deduplication should prevent a second incident")
}

func TestIngestStateChangeCreatesNewAlertEntry(t *testing.T) {
	incStore := store.NewMemoryIncidentStore(zap.NewNop())
	am := NewManager(incStore, zap.NewNop())
	ctx := context.Background()

	// First: firing
	alert1 := &Alert{
		Status: "firing",
		Labels: map[string]string{"service": "api"},
	}
	err := am.Ingest(ctx, alert1)
	require.NoError(t, err)
	firstID := alert1.ID

	// Second: resolved (state change)
	alert2 := &Alert{
		Status: "resolved",
		Labels: map[string]string{"service": "api"},
	}
	err = am.Ingest(ctx, alert2)
	require.NoError(t, err)

	assert.NotEqual(t, firstID, alert2.ID, "State change should assign a new ID")
}

func TestIngestCorrelatesExistingIncident(t *testing.T) {
	incStore := store.NewMemoryIncidentStore(zap.NewNop())
	am := NewManager(incStore, zap.NewNop())
	ctx := context.Background()

	// Create a first alert to generate an incident
	err := am.Ingest(ctx, &Alert{
		Status: "firing",
		Labels: map[string]string{
			"service":     "api",
			"environment": "prod",
			"alertname":   "HighCPU",
		},
	})
	require.NoError(t, err)

	// Ingest a second alert for the same service+env with a different alertname
	err = am.Ingest(ctx, &Alert{
		Status: "firing",
		Labels: map[string]string{
			"service":     "api",
			"environment": "prod",
			"alertname":   "HighMemory",
		},
	})
	require.NoError(t, err)

	// Should correlate to the same incident (same service+environment)
	incidents, err := incStore.List(ctx, store.IncidentFilter{})
	require.NoError(t, err)
	assert.Len(t, incidents, 1, "Second alert should correlate to existing incident, not create a new one")
}
