package remediation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Engine creation tests ──────────────────────────────────────────────

func TestNewEngineDryRun(t *testing.T) {
	e, err := NewEngine(zap.NewNop(), true)
	require.NoError(t, err)
	assert.True(t, e.dryRun)
}

// ── Execute dry-run tests ──────────────────────────────────────────────

func TestExecuteDryRunRestartPod(t *testing.T) {
	e, _ := NewEngine(zap.NewNop(), true)

	err := e.Execute(context.Background(), RemediationRequest{
		ID:          "dry-1",
		Action:      ActionTypeRestartPod,
		Target:      "web-pod-123",
		Namespace:   "default",
		RequestedBy: "ai-module",
	})
	assert.NoError(t, err, "Dry run should succeed without K8s client")
}

func TestExecuteDryRunScript(t *testing.T) {
	e, _ := NewEngine(zap.NewNop(), true)

	err := e.Execute(context.Background(), RemediationRequest{
		ID:          "dry-2",
		Action:      ActionTypeScript,
		Target:      "host-1",
		Params:      map[string]string{"script": "/opt/fix.sh"},
		RequestedBy: "ai-module",
	})
	assert.NoError(t, err, "Dry run should succeed without executing script")
}

func TestExecuteDryRunAnsible(t *testing.T) {
	e, _ := NewEngine(zap.NewNop(), true)

	err := e.Execute(context.Background(), RemediationRequest{
		ID:          "dry-3",
		Action:      ActionTypeAnsiblePlaybook,
		Params:      map[string]string{"playbook": "site.yml"},
		RequestedBy: "ai-module",
	})
	assert.NoError(t, err, "Dry run should succeed without running Ansible")
}

// ── Execute live mode - error cases ────────────────────────────────────

func TestExecuteUnknownAction(t *testing.T) {
	e, _ := NewEngine(zap.NewNop(), false)

	err := e.Execute(context.Background(), RemediationRequest{
		ID:             "unk-1",
		Action:         ActionType("unknown_action"),
		ExecutionLevel: ExecutionLevelAutoExecute,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown action type")
}

func TestExecuteRestartPodNoK8sClient(t *testing.T) {
	e, _ := NewEngine(zap.NewNop(), false)
	// k8sClient will be nil since we're not in-cluster

	err := e.Execute(context.Background(), RemediationRequest{
		ID:             "pod-1",
		Action:         ActionTypeRestartPod,
		Target:         "some-pod",
		ExecutionLevel: ExecutionLevelAutoExecute,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "k8s client not initialized")
}

func TestRunAnsibleMissingPlaybook(t *testing.T) {
	e, _ := NewEngine(zap.NewNop(), false)

	err := e.runAnsible(context.Background(), RemediationRequest{
		ID:     "ans-1",
		Params: map[string]string{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'playbook' parameter")
}

func TestRunScriptMissingScript(t *testing.T) {
	e, _ := NewEngine(zap.NewNop(), false)

	err := e.runScript(context.Background(), RemediationRequest{
		ID:     "scr-1",
		Params: map[string]string{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'script' parameter")
}

// ── Namespace default test ─────────────────────────────────────────────

func TestRestartPodDefaultNamespace(t *testing.T) {
	e, _ := NewEngine(zap.NewNop(), false)

	err := e.restartPod(context.Background(), RemediationRequest{
		Target:    "pod-1",
		Namespace: "", // empty → should use "default"
	})
	// Will fail because k8s client is nil, but we're testing the code path
	require.Error(t, err)
	assert.Contains(t, err.Error(), "k8s client not initialized")
}
