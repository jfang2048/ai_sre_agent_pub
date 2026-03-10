package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestRunnerBlocksDangerousKubectlSubcommand(t *testing.T) {
	cfg := DefaultRunnerConfig()
	cfg.DryRun = false
	cfg.PlaybookFile = ""

	runner, err := NewPlaybookRunner(cfg, zap.NewNop())
	require.NoError(t, err)

	action := ActionSpec{
		ID:      "a-k8s-danger",
		Type:    "kubernetes",
		Command: "kubectl delete namespace production",
		Safe:    true,
	}

	results := runner.Execute(context.Background(), []ActionSpec{action}, false)
	require.Len(t, results, 1)
	require.Equal(t, ActionResultFailed, results[0].Status)
	require.Contains(t, results[0].Error, "blocked by policy")
}

func TestRunnerTimesOutLongRunningCommand(t *testing.T) {
	cfg := DefaultRunnerConfig()
	cfg.DryRun = false
	cfg.PlaybookFile = ""
	cfg.AllowedShellCommands = []string{"sleep"}
	cfg.ActionTimeout = 20 * time.Millisecond

	runner, err := NewPlaybookRunner(cfg, zap.NewNop())
	require.NoError(t, err)

	action := ActionSpec{
		ID:      "a-timeout",
		Type:    "shell",
		Command: "sleep 1",
		Safe:    true,
	}

	results := runner.Execute(context.Background(), []ActionSpec{action}, false)
	require.Len(t, results, 1)
	require.Equal(t, ActionResultFailed, results[0].Status)
	require.Contains(t, results[0].Error, "timed out")
}

func TestRunnerBlocksDangerousKubectlSubcommandWithLeadingFlags(t *testing.T) {
	cfg := DefaultRunnerConfig()
	cfg.DryRun = false
	cfg.PlaybookFile = ""

	runner, err := NewPlaybookRunner(cfg, zap.NewNop())
	require.NoError(t, err)

	action := ActionSpec{
		ID:      "a-k8s-danger-flagged",
		Type:    "kubernetes",
		Command: "kubectl -n prod exec pod/trainer -- ls /",
		Safe:    true,
	}

	results := runner.Execute(context.Background(), []ActionSpec{action}, false)
	require.Len(t, results, 1)
	require.Equal(t, ActionResultFailed, results[0].Status)
	require.Contains(t, results[0].Error, "blocked by policy")
}

func TestRunnerReloadReplacesPlaybooks(t *testing.T) {
	dir := t.TempDir()
	initialPath := filepath.Join(dir, "initial.yaml")
	reloadedPath := filepath.Join(dir, "reloaded.yaml")

	require.NoError(t, os.WriteFile(initialPath, []byte(`playbooks:
  - id: cpu-hot
    summary: cpu hot
    severity: P2
    conditions:
      - metric: node_cpu_usage_percent
        op: ">="
        threshold: 80
    actions:
      - type: shell
        command: echo cpu
        safe: true
`), 0o644))
	require.NoError(t, os.WriteFile(reloadedPath, []byte(`playbooks:
  - id: mem-hot
    summary: memory hot
    severity: P1
    conditions:
      - metric: node_memory_used_percent
        op: ">="
        threshold: 90
    actions:
      - type: shell
        command: echo mem
        safe: true
`), 0o644))

	cfg := DefaultRunnerConfig()
	cfg.PlaybookFile = initialPath

	runner, err := NewPlaybookRunner(cfg, zap.NewNop())
	require.NoError(t, err)

	initial := runner.ProposeFromMetrics("node-a", map[string]float64{"node_cpu_usage_percent": 91})
	require.Len(t, initial, 1)
	require.Equal(t, "echo cpu", initial[0].Command)

	cfg.PlaybookFile = reloadedPath
	require.NoError(t, runner.Reload(cfg))

	afterCPU := runner.ProposeFromMetrics("node-a", map[string]float64{"node_cpu_usage_percent": 91})
	require.Len(t, afterCPU, 0)
	afterMem := runner.ProposeFromMetrics("node-a", map[string]float64{"node_memory_used_percent": 95})
	require.Len(t, afterMem, 1)
	require.Equal(t, "echo mem", afterMem[0].Command)
}
