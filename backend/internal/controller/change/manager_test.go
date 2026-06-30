package change

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── RegisterChange tests ──────────────────────────────────────────────

func TestRegisterChangeAutoApproveLowRisk(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())

	err := cm.RegisterChange(&Change{
		ID:        "chg-001",
		Title:     "Update config",
		Tier:      Tier3,
		RiskLevel: RiskLevelLow,
	})
	require.NoError(t, err)

	c, err := cm.GetChange("chg-001")
	require.NoError(t, err)
	assert.Equal(t, ApprovalNoApproval, c.ApprovalStatus, "Low risk should auto-approve")
	assert.Equal(t, StatusPlanned, c.Status)
}

func TestRegisterChangeAutoApproveTier4(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())

	err := cm.RegisterChange(&Change{
		ID:        "chg-002",
		Title:     "Experimental deploy",
		Tier:      Tier4,
		RiskLevel: RiskLevelHigh,
	})
	require.NoError(t, err)

	c, _ := cm.GetChange("chg-002")
	assert.Equal(t, ApprovalNoApproval, c.ApprovalStatus, "Tier4 should auto-approve regardless of risk")
}

func TestRegisterChangePendingApprovalHighRisk(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())

	err := cm.RegisterChange(&Change{
		ID:        "chg-003",
		Title:     "Core infra change",
		Tier:      Tier1,
		RiskLevel: RiskLevelHigh,
	})
	require.NoError(t, err)

	c, _ := cm.GetChange("chg-003")
	assert.Equal(t, ApprovalPending, c.ApprovalStatus, "High risk Tier1 should require approval")
}

func TestRegisterChangeDuplicateReturnsError(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())

	require.NoError(t, cm.RegisterChange(&Change{ID: "chg-dup", Title: "first"}))
	err := cm.RegisterChange(&Change{ID: "chg-dup", Title: "second"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestRegisterChangeInitializesCanaryStatus(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())

	err := cm.RegisterChange(&Change{
		ID:           "chg-canary",
		Title:        "Canary deploy",
		Tier:         Tier4,
		RiskLevel:    RiskLevelLow,
		CanaryConfig: &CanaryConfig{InitialPercentage: 5, MaxPercentage: 100},
	})
	require.NoError(t, err)

	c, _ := cm.GetChange("chg-canary")
	require.NotNil(t, c.CanaryStatus)
	assert.Equal(t, CanaryNotStarted, c.CanaryStatus.Status)
	assert.Equal(t, 0, c.CanaryStatus.CurrentPercentage)
}

// ── Approve / Reject tests ────────────────────────────────────────────

func TestApproveChange(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "chg-a1", Tier: Tier1, RiskLevel: RiskLevelHigh})

	err := cm.ApproveChange("chg-a1", "alice")
	require.NoError(t, err)

	c, _ := cm.GetChange("chg-a1")
	assert.Equal(t, ApprovalApproved, c.ApprovalStatus)
	assert.Equal(t, "alice", c.ApprovedBy)
}

func TestApproveChangeNotPending(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "chg-a2", Tier: Tier4, RiskLevel: RiskLevelLow})

	err := cm.ApproveChange("chg-a2", "alice")
	require.Error(t, err, "Auto-approved changes can't be re-approved")
}

func TestApproveChangeNotFound(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	err := cm.ApproveChange("nonexistent", "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRejectChange(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "chg-r1", Tier: Tier1, RiskLevel: RiskLevelHigh})

	err := cm.RejectChange("chg-r1", "bob", "too risky")
	require.NoError(t, err)

	c, _ := cm.GetChange("chg-r1")
	assert.Equal(t, ApprovalRejected, c.ApprovalStatus)
	assert.Equal(t, StatusCancelled, c.Status)
}

// ── StartChange tests ─────────────────────────────────────────────────

func TestStartChangeRequiresApproval(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "chg-s1", Tier: Tier1, RiskLevel: RiskLevelHigh})

	err := cm.StartChange(context.Background(), "chg-s1")
	require.Error(t, err, "Pending approval should prevent start")
	assert.Contains(t, err.Error(), "not approved")
}

func TestStartChangeRejected(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "chg-s2", Tier: Tier1, RiskLevel: RiskLevelHigh})
	cm.RejectChange("chg-s2", "bob", "nope")

	err := cm.StartChange(context.Background(), "chg-s2")
	require.Error(t, err, "Rejected changes should not start")
	assert.Contains(t, err.Error(), "rejected")
}

// ── Complete / Rollback tests ──────────────────────────────────────────

func TestCompleteChangeNotInProgress(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "chg-c1", Tier: Tier4, RiskLevel: RiskLevelLow})

	err := cm.CompleteChange("chg-c1")
	require.Error(t, err, "Planned change can't be completed without starting")
	assert.Contains(t, err.Error(), "not in progress")
}

func TestRollbackChange(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "chg-rb", Tier: Tier4, RiskLevel: RiskLevelLow})

	err := cm.RollbackChange(context.Background(), "chg-rb", "bad deploy")
	require.NoError(t, err)

	c, _ := cm.GetChange("chg-rb")
	assert.Equal(t, StatusRolledBack, c.Status)
}

// ── Canary metrics check tests ────────────────────────────────────────

func TestCheckCanaryMetricsNilCriteria(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	passed, reason := cm.checkCanaryMetrics(map[string]float64{}, nil)
	assert.True(t, passed)
	assert.Empty(t, reason)
}

func TestCheckCanaryMetricsAllPass(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	passed, reason := cm.checkCanaryMetrics(
		map[string]float64{"availability": 99.95, "error_rate": 0.5, "latency_increase_pct": 10},
		&SuccessCriteria{MinAvailability: 99.9, MaxErrorRate: 1.0, MaxLatencyIncrease: 20},
	)
	assert.True(t, passed)
	assert.Empty(t, reason)
}

func TestCheckCanaryMetricsFailAvailability(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	passed, reason := cm.checkCanaryMetrics(
		map[string]float64{"availability": 98.0, "error_rate": 0.1, "latency_increase_pct": 5},
		&SuccessCriteria{MinAvailability: 99.9, MaxErrorRate: 1.0, MaxLatencyIncrease: 20},
	)
	assert.False(t, passed)
	assert.Contains(t, reason, "availability")
}

func TestCheckCanaryMetricsFailErrorRate(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	passed, reason := cm.checkCanaryMetrics(
		map[string]float64{"availability": 99.99, "error_rate": 5.0, "latency_increase_pct": 5},
		&SuccessCriteria{MinAvailability: 99.9, MaxErrorRate: 1.0, MaxLatencyIncrease: 20},
	)
	assert.False(t, passed)
	assert.Contains(t, reason, "error rate")
}

func TestCheckCanaryMetricsFailLatency(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	passed, reason := cm.checkCanaryMetrics(
		map[string]float64{"availability": 99.99, "error_rate": 0.1, "latency_increase_pct": 30},
		&SuccessCriteria{MinAvailability: 99.9, MaxErrorRate: 1.0, MaxLatencyIncrease: 20},
	)
	assert.False(t, passed)
	assert.Contains(t, reason, "latency")
}

// ── Query tests ───────────────────────────────────────────────────────

func TestGetChangesByStatus(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "a", Tier: Tier4, RiskLevel: RiskLevelLow})
	cm.RegisterChange(&Change{ID: "b", Tier: Tier4, RiskLevel: RiskLevelLow})

	planned := cm.GetChangesByStatus(StatusPlanned)
	assert.Len(t, planned, 2)

	completed := cm.GetChangesByStatus(StatusCompleted)
	assert.Len(t, completed, 0)
}

func TestGetPendingChanges(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "p1", Tier: Tier4, RiskLevel: RiskLevelLow})

	pending := cm.GetPendingChanges()
	assert.Len(t, pending, 1)
}

// ── UpdateCanaryMetrics tests ──────────────────────────────────────────

func TestUpdateCanaryMetrics(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{
		ID:           "chg-cm",
		Tier:         Tier4,
		RiskLevel:    RiskLevelLow,
		CanaryConfig: &CanaryConfig{InitialPercentage: 5, MaxPercentage: 100},
	})

	err := cm.UpdateCanaryMetrics("chg-cm", map[string]float64{
		"availability": 99.95,
		"error_rate":   0.2,
	})
	require.NoError(t, err)

	c, _ := cm.GetChange("chg-cm")
	assert.InDelta(t, 99.95, c.CanaryStatus.Metrics["availability"], 0.01)
}

func TestUpdateCanaryMetricsNotConfigured(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	cm.RegisterChange(&Change{ID: "chg-nc", Tier: Tier4, RiskLevel: RiskLevelLow})

	err := cm.UpdateCanaryMetrics("chg-nc", map[string]float64{"availability": 99.9})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "canary not configured")
}

// ── IsActiveChangeWindow boundary test ──────────────────────────────

func TestIsActiveChangeWindowReturnsBool(t *testing.T) {
	cm := NewChangeManager(zap.NewNop())
	// Just ensure it runs without panic — actual result depends on current time
	_ = cm.IsActiveChangeWindow()

	// Additional: verify that the function logic doesn't panic on boundary
	now := time.Now()
	_ = now.Weekday()
}
