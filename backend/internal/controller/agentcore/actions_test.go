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
	require.Equal(t, ActionResultBlocked, results[0].Status)
	require.Contains(t, results[0].Error, "human operator required")
	require.NotNil(t, results[0].OperatorHandoff)
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
	require.Equal(t, ActionResultBlocked, results[0].Status)
	require.Contains(t, results[0].Error, "human operator required")
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
	require.Equal(t, ActionResultBlocked, results[0].Status)
	require.Contains(t, results[0].Error, "human operator required")
}

func TestRunnerHarnessRequiresHumanForOperationalCommands(t *testing.T) {
	cfg := DefaultRunnerConfig()
	cfg.PlaybookFile = ""
	cfg.DryRun = false
	cfg.AllowUnsafe = true
	cfg.AllowedShellCommands = []string{"reboot", "shutdown", "systemctl", "git", "kubectl", "bash", "sudo", "rm", "docker", "helm", "terraform", "iptables", "curl"}
	runner, err := NewPlaybookRunner(cfg, zap.NewNop())
	require.NoError(t, err)

	commands := []string{
		"reboot", "shutdown -h now", "systemctl restart api", "git switch previous",
		"git reset --hard HEAD~1", "kubectl get pods", "kubectl rollout restart deployment/api",
		"bash -c reboot", "sudo reboot", "rm -rf /tmp/data", "docker restart api",
		"helm rollback api 1", "terraform apply", "iptables -F", "curl https://example.invalid",
		"/bin/echo bypass",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			action := ActionSpec{Type: "shell", Command: command, Safe: true, Description: "operator proposal"}
			result := runner.Execute(context.Background(), []ActionSpec{action}, false)[0]
			require.Equal(t, ActionResultBlocked, result.Status)
			require.True(t, result.DryRun)
			require.Contains(t, result.Error, "human operator required")
			require.NotNil(t, result.OperatorHandoff)
			require.Equal(t, "operator proposal", result.OperatorHandoff.Proposal)
		})
	}
	for _, action := range []ActionSpec{
		{Type: "restart_pod", Namespace: "default", Name: "api-0", Safe: true},
		{Type: "scale_deployment", Namespace: "default", Name: "api", Safe: true},
		{Type: "kubernetes", Command: "kubectl get pods", Safe: true},
	} {
		result := runner.Execute(context.Background(), []ActionSpec{action}, false)[0]
		require.Equal(t, ActionResultBlocked, result.Status)
		require.NotNil(t, result.OperatorHandoff)
	}
}

func TestRunnerHarnessBlocksRollbackAndDryRunBypass(t *testing.T) {
	cfg := DefaultRunnerConfig()
	cfg.PlaybookFile = ""
	cfg.DryRun = false
	cfg.AllowUnsafe = true
	cfg.AllowedShellCommands = []string{"echo", "git", "systemctl"}
	runner, err := NewPlaybookRunner(cfg, zap.NewNop())
	require.NoError(t, err)

	action := ActionSpec{Type: "shell", Command: "echo prepared", RollbackCommand: "git switch prior", Safe: true}
	rollback := runner.ExecuteRollback(context.Background(), action, false)
	require.Equal(t, ActionResultBlocked, rollback.Status)
	require.True(t, rollback.IsRollback)
	require.NotNil(t, rollback.OperatorHandoff)

	blocked := ActionSpec{Type: "shell", Command: "systemctl reboot", Safe: true}
	result := runner.Execute(context.Background(), []ActionSpec{blocked}, true)[0]
	require.Equal(t, ActionResultBlocked, result.Status)
	require.NotNil(t, result.OperatorHandoff)

	// A blocked attempt must not be recorded as if it had executed.
	result = runner.Execute(context.Background(), []ActionSpec{blocked}, false)[0]
	require.Equal(t, ActionResultBlocked, result.Status)
	rollback = runner.ExecuteRollback(context.Background(), action, true)
	require.Equal(t, ActionResultBlocked, rollback.Status)
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
