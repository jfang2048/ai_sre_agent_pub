package remediation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── LoadPlaybook tests ─────────────────────────────────────────────────

func TestLoadPlaybook(t *testing.T) {
	yamlData := []byte(`
name: test-playbook
description: A test playbook
timeout: 30s
on_failure: rollback
steps:
  - name: check_status
    type: read
    command: "kubectl get pods"
    timeout: 10s
  - name: restart
    type: execute
    command: "kubectl rollout restart"
`)

	pb, err := LoadPlaybook(yamlData)
	require.NoError(t, err)
	assert.Equal(t, "test-playbook", pb.Name)
	assert.Equal(t, "A test playbook", pb.Description)
	assert.Equal(t, 30*time.Second, pb.Timeout)
	assert.Equal(t, "rollback", pb.OnFailure)
	assert.Len(t, pb.Steps, 2)
	assert.Equal(t, "check_status", pb.Steps[0].Name)
	assert.Equal(t, "read", pb.Steps[0].Type)
}

func TestLoadPlaybookInvalidYAML(t *testing.T) {
	_, err := LoadPlaybook([]byte(`{invalid: yaml: [}`))
	require.Error(t, err)
}

// ── Predefined playbook tests ──────────────────────────────────────────

func TestScaleUpPlaybook(t *testing.T) {
	pb := ScaleUpPlaybook(5)
	assert.Equal(t, "scale_up", pb.Name)
	assert.NotEmpty(t, pb.Steps)

	// Should contain the replica count in a step
	found := false
	for _, s := range pb.Steps {
		if s.Parameters != nil {
			if replicas, ok := s.Parameters["replicas"]; ok {
				assert.Equal(t, 5, replicas)
				found = true
			}
		}
	}
	assert.True(t, found, "Should have a step with replicas parameter")
}

func TestRestartPodPlaybook(t *testing.T) {
	pb := RestartPodPlaybook()
	assert.Equal(t, "restart_pod", pb.Name)
	assert.NotEmpty(t, pb.Steps)
}

func TestFailoverPlaybook(t *testing.T) {
	pb := FailoverPlaybook("us-east-1")
	assert.Equal(t, "failover", pb.Name)
	assert.NotEmpty(t, pb.Steps)

	// Should reference the target region
	found := false
	for _, s := range pb.Steps {
		if s.Parameters != nil {
			if region, ok := s.Parameters["region"]; ok {
				assert.Equal(t, "us-east-1", region)
				found = true
			}
		}
	}
	assert.True(t, found, "Should have a step with region parameter")
}

// ── PlaybookRunner creation tests ──────────────────────────────────────

func TestNewPlaybookRunner(t *testing.T) {
	executor := NewExecutor(Config{DryRun: true}, zap.NewNop())
	runner := NewPlaybookRunner(executor, zap.NewNop())
	require.NotNil(t, runner)
}
