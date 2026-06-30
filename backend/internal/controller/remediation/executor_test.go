package remediation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Executor creation tests ───────────────────────────────────────────

func TestNewExecutor(t *testing.T) {
	e := NewExecutor(Config{DryRun: true, MaxConcurrent: 5}, zap.NewNop())
	require.NotNil(t, e)
	assert.True(t, e.config.DryRun)
}

// ── Dry-run execution tests ───────────────────────────────────────────

func TestExecutorDryRunScaleDeployment(t *testing.T) {
	e := NewExecutor(Config{DryRun: true, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:       "scale_deployment",
		Target:     "web-app",
		Parameters: map[string]interface{}{"replicas": float64(5)},
		Reason:     "high CPU",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.Message, "Dry run")
}

func TestExecutorDryRunRestartPod(t *testing.T) {
	e := NewExecutor(Config{DryRun: true, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:   "restart_pod",
		Target: "web-pod-1",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Len(t, result.Changes, 1)
}

// ── Live execution tests (stub implementations) ──────────────────────

func TestExecutorScaleDeployment(t *testing.T) {
	e := NewExecutor(Config{DryRun: false, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:       "scale_deployment",
		Target:     "api-server",
		Parameters: map[string]interface{}{"replicas": float64(3)},
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.Message, "Scaled api-server to 3 replicas")
}

func TestExecutorScaleDeploymentRejectsInvalidReplicaType(t *testing.T) {
	e := NewExecutor(Config{DryRun: false, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:       "scale_deployment",
		Target:     "api-server",
		Parameters: map[string]interface{}{"replicas": "three"},
	})
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Message, "invalid replicas parameter")
}

func TestExecutorRestartPod(t *testing.T) {
	e := NewExecutor(Config{DryRun: false, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:   "restart_pod",
		Target: "cache-pod",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.Message, "Restarted pod cache-pod")
}

func TestExecutorFailoverTraffic(t *testing.T) {
	e := NewExecutor(Config{DryRun: false, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:       "failover_traffic",
		Target:     "lb-primary",
		Parameters: map[string]interface{}{"region": "us-west-2"},
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.Message, "us-west-2")
}

func TestExecutorFailoverTrafficRejectsInvalidRegionType(t *testing.T) {
	e := NewExecutor(Config{DryRun: false, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:       "failover_traffic",
		Target:     "lb-primary",
		Parameters: map[string]interface{}{"region": 42},
	})
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Message, "invalid region parameter")
}

func TestExecutorClearCache(t *testing.T) {
	e := NewExecutor(Config{DryRun: false, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:   "clear_cache",
		Target: "redis-cluster",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestExecutorUnknownAction(t *testing.T) {
	e := NewExecutor(Config{DryRun: false, MaxConcurrent: 5}, zap.NewNop())

	result, err := e.Execute(context.Background(), &Action{
		Type:   "unknown",
		Target: "something",
	})
	require.NoError(t, err) // returns result with success=false, not an error
	assert.False(t, result.Success)
	assert.Contains(t, result.Message, "unknown action type")
}

// ── Concurrency limit tests ──────────────────────────────────────────

func TestExecutorConcurrencyLimit(t *testing.T) {
	e := NewExecutor(Config{MaxConcurrent: 0}, zap.NewNop()) // limit 0 = always at capacity

	_, err := e.Execute(context.Background(), &Action{
		Type:   "restart_pod",
		Target: "pod-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "concurrency limit reached")
}

// ── Rollback manager tests ──────────────────────────────────────────

func TestRollbackManagerRecord(t *testing.T) {
	rm := NewRollbackManager(zap.NewNop())

	action := &Action{Type: "scale_deployment", Target: "web-app"}
	result := &ActionResult{Success: true}
	rm.Record(action, result)

	history := rm.GetHistory("web-app")
	assert.Len(t, history, 1)
	assert.Equal(t, "scale_deployment", history[0].Action.Type)
}

func TestRollbackManagerEmptyHistory(t *testing.T) {
	rm := NewRollbackManager(zap.NewNop())

	err := rm.Rollback(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rollback history")
}

func TestRollbackManagerRollbackSuccess(t *testing.T) {
	rm := NewRollbackManager(zap.NewNop())

	rm.Record(&Action{Type: "scale_deployment", Target: "web-app"}, &ActionResult{Success: true})

	err := rm.Rollback(context.Background(), "web-app")
	require.NoError(t, err)
}

// ── Rollback recording from Execute ──────────────────────────────────

func TestExecutorRecordsRollbackOnSuccess(t *testing.T) {
	e := NewExecutor(Config{DryRun: false, MaxConcurrent: 5}, zap.NewNop())

	_, err := e.Execute(context.Background(), &Action{
		Type:        "restart_pod",
		Target:      "web-pod",
		CanRollback: true,
	})
	require.NoError(t, err)

	history := e.rollbackManager.GetHistory("web-pod")
	assert.Len(t, history, 1, "Successful rollback-eligible action should be recorded")
}
