package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newStore() *MemoryIncidentStore {
	return NewMemoryIncidentStore(zap.NewNop())
}

func seedIncident(t *testing.T, s *MemoryIncidentStore, id, title string, sev IncidentSeverity) *Incident {
	t.Helper()
	inc := &Incident{
		ID:       id,
		Title:    title,
		Severity: sev,
		Labels:   map[string]string{},
	}
	require.NoError(t, s.Add(context.Background(), inc))
	return inc
}

// ── CRUD tests ─────────────────────────────────────────────────────────

func TestAddAndGet(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "inc-1", "High CPU", SeverityP1)

	got, err := s.Get(context.Background(), "inc-1")
	require.NoError(t, err)
	assert.Equal(t, "High CPU", got.Title)
	assert.Equal(t, StateDetected, got.State, "Default state should be 'detected'")
	assert.False(t, got.CreatedAt.IsZero())
}

func TestAddAutoGeneratesID(t *testing.T) {
	s := newStore()
	inc := &Incident{Title: "No ID", Severity: SeverityP3, Labels: map[string]string{}}
	require.NoError(t, s.Add(context.Background(), inc))
	assert.NotEmpty(t, inc.ID, "Should auto-generate ID if empty")
}

func TestGetNotFound(t *testing.T) {
	s := newStore()
	_, err := s.Get(context.Background(), "nonexistent")
	require.Error(t, err)
}

func TestUpdate(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "inc-u", "Original", SeverityP2)

	inc, _ := s.Get(context.Background(), "inc-u")
	inc.Title = "Updated"
	require.NoError(t, s.Update(context.Background(), inc))

	got, _ := s.Get(context.Background(), "inc-u")
	assert.Equal(t, "Updated", got.Title)
}

func TestUpdateNotFound(t *testing.T) {
	s := newStore()
	err := s.Update(context.Background(), &Incident{ID: "nope"})
	require.Error(t, err)
}

func TestDelete(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "inc-d", "To Delete", SeverityP3)

	require.NoError(t, s.Delete(context.Background(), "inc-d"))
	_, err := s.Get(context.Background(), "inc-d")
	require.Error(t, err, "Should not be found after delete")
}

func TestDeleteNotFound(t *testing.T) {
	s := newStore()
	err := s.Delete(context.Background(), "nope")
	require.Error(t, err)
}

// ── PostMortem required for P0/P1 ─────────────────────────────────────

func TestPostMortemRequiredP0(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "inc-p0", "Critical", SeverityP0)

	inc, _ := s.Get(context.Background(), "inc-p0")
	assert.True(t, inc.PostMortemRequired, "P0 should require post-mortem")
}

func TestPostMortemNotRequiredP3(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "inc-p3", "Minor", SeverityP3)

	inc, _ := s.Get(context.Background(), "inc-p3")
	assert.False(t, inc.PostMortemRequired, "P3 should not require post-mortem")
}

// ── List with filters ──────────────────────────────────────────────────

func TestListFilterBySeverity(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "a", "P0 issue", SeverityP0)
	seedIncident(t, s, "b", "P2 issue", SeverityP2)
	seedIncident(t, s, "c", "Another P0", SeverityP0)

	result, err := s.List(context.Background(), IncidentFilter{Severity: SeverityP0})
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestListFilterByState(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "d", "Active", SeverityP1)
	s.RecordStateChange(context.Background(), "d", StateResolved)

	seedIncident(t, s, "e", "Open", SeverityP1)

	detected, _ := s.List(context.Background(), IncidentFilter{State: StateDetected})
	assert.Len(t, detected, 1)

	resolved, _ := s.List(context.Background(), IncidentFilter{State: StateResolved})
	assert.Len(t, resolved, 1)
}

func TestListWithLimit(t *testing.T) {
	s := newStore()
	for i := 0; i < 10; i++ {
		seedIncident(t, s, generateID(), "issue", SeverityP2)
	}

	result, _ := s.List(context.Background(), IncidentFilter{Limit: 3})
	assert.Len(t, result, 3)
}

// ── Search tests ──────────────────────────────────────────────────────

func TestSearch(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "s1", "Database connection timeout", SeverityP1)
	seedIncident(t, s, "s2", "CPU spike on web server", SeverityP2)

	results, err := s.Search(context.Background(), "database")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "s1", results[0].ID)
}

func TestSearchCaseInsensitive(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "s-ci", "High CPU Alert", SeverityP2)

	results, _ := s.Search(context.Background(), "high cpu")
	assert.Len(t, results, 1, "Search should be case-insensitive")
}

func TestSearchNoMatch(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "s3", "Something", SeverityP3)

	results, _ := s.Search(context.Background(), "xyzzy")
	assert.Empty(t, results)
}

func TestSearchMidString(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "s4", "Error in database layer", SeverityP1)

	results, _ := s.Search(context.Background(), "database")
	assert.Len(t, results, 1, "Mid-string substring search should now work after bug fix")
}

// ── State transition tests ────────────────────────────────────────────

func TestRecordStateChangeAcknowledged(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "st1", "Ack test", SeverityP1)

	time.Sleep(1 * time.Millisecond) // ensure nonzero MTTD
	require.NoError(t, s.RecordStateChange(context.Background(), "st1", StateAcknowledged))

	inc, _ := s.Get(context.Background(), "st1")
	assert.Equal(t, StateAcknowledged, inc.State)
	assert.False(t, inc.AcknowledgedAt.IsZero())
	assert.Greater(t, inc.MTTD, time.Duration(0), "MTTD should be calculated on ack")
}

func TestRecordStateChangeClosed(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "st2", "Close test", SeverityP2)

	time.Sleep(1 * time.Millisecond)
	require.NoError(t, s.RecordStateChange(context.Background(), "st2", StateClosed))

	inc, _ := s.Get(context.Background(), "st2")
	assert.Equal(t, StateClosed, inc.State)
	assert.False(t, inc.ClosedAt.IsZero())
	assert.Greater(t, inc.MTTR, time.Duration(0), "MTTR should be calculated on close")
}

func TestRecordStateChangeNotFound(t *testing.T) {
	s := newStore()
	err := s.RecordStateChange(context.Background(), "nope", StateResolved)
	require.Error(t, err)
}

// ── AssignIncidentCommander tests ──────────────────────────────────────

func TestAssignIncidentCommander(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "ic1", "IC test", SeverityP0)

	require.NoError(t, s.AssignIncidentCommander(context.Background(), "ic1", "alice"))

	inc, _ := s.Get(context.Background(), "ic1")
	assert.Equal(t, "alice", inc.IncidentCommander)
	assert.False(t, inc.AcknowledgedAt.IsZero(), "Commander assignment should set ack time")
}

// ── CreatePostMortem tests ─────────────────────────────────────────────

func TestCreatePostMortem(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "pm1", "PM test", SeverityP0)

	pm := &PostMortem{
		Author:    "bob",
		Summary:   "Root cause was config change",
		RootCause: "Invalid configuration deployed",
	}
	require.NoError(t, s.CreatePostMortem(context.Background(), "pm1", pm))

	inc, _ := s.Get(context.Background(), "pm1")
	require.NotNil(t, inc.PostMortem)
	assert.Equal(t, "pm-pm1", inc.PostMortem.ID)
	assert.Equal(t, StateClosed, inc.State, "Post-mortem should close incident")
}

// ── CalculateMTTR tests ───────────────────────────────────────────────

func TestCalculateMTTR(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "mttr1", "Resolved", SeverityP1)
	time.Sleep(2 * time.Millisecond)
	s.RecordStateChange(context.Background(), "mttr1", StateResolved)

	mttr, err := s.CalculateMTTR(context.Background(), IncidentFilter{})
	require.NoError(t, err)
	assert.Greater(t, mttr, time.Duration(0))
}

func TestCalculateMTTRNoResolved(t *testing.T) {
	s := newStore()
	seedIncident(t, s, "mttr2", "Open", SeverityP1)

	mttr, err := s.CalculateMTTR(context.Background(), IncidentFilter{})
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), mttr, "No resolved incidents → 0 MTTR")
}
