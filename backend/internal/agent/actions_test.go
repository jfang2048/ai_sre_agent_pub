package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLoadPlaybooksSupportsWrappedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "playbooks.yaml")
	content := `version: v1
playbooks:
  - id: test-gpu
    summary: gpu hot
    severity: P1
    conditions:
      - metric: node_gpu_utilization_sm_avg_percent
        op: ">="
        threshold: 90
    actions:
      - type: restart_pod
        namespace: ml
        name: trainer-0
        safe: true
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	playbooks, err := LoadPlaybooks(path)
	require.NoError(t, err)
	require.Len(t, playbooks, 1)
	require.Equal(t, "test-gpu", playbooks[0].ID)
	require.Equal(t, "restart_pod", playbooks[0].Actions[0].Type)
}

func TestRunnerExecuteIsIdempotent(t *testing.T) {
	cfg := DefaultRunnerConfig()
	cfg.DryRun = false
	cfg.PlaybookFile = ""
	cfg.AllowedShellCommands = []string{"echo"}

	runner, err := NewPlaybookRunner(cfg, zap.NewNop())
	require.NoError(t, err)

	action := ActionSpec{
		ID:      "a1",
		Type:    "shell",
		Command: "echo hello",
		Safe:    true,
	}

	first := runner.Execute(context.Background(), []ActionSpec{action}, false)
	require.Len(t, first, 1)
	require.Equal(t, ActionResultExecuted, first[0].Status)
	require.Contains(t, first[0].Output, "hello")

	second := runner.Execute(context.Background(), []ActionSpec{action}, false)
	require.Len(t, second, 1)
	require.Equal(t, ActionResultSkipped, second[0].Status)
}

func TestRunnerBlocksDisallowedShellCommand(t *testing.T) {
	cfg := DefaultRunnerConfig()
	cfg.DryRun = false
	cfg.PlaybookFile = ""
	cfg.AllowedShellCommands = []string{"echo"}

	runner, err := NewPlaybookRunner(cfg, zap.NewNop())
	require.NoError(t, err)

	action := ActionSpec{
		ID:      "a2",
		Type:    "shell",
		Command: "bash -c whoami",
		Safe:    true,
	}

	results := runner.Execute(context.Background(), []ActionSpec{action}, false)
	require.Len(t, results, 1)
	require.Equal(t, ActionResultSkipped, results[0].Status)
	require.Contains(t, results[0].Error, "not allowed")
}
