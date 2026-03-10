package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPlanActionsGeneratesUniqueIDs(t *testing.T) {
	now := time.Unix(1_700_000_000, 123)
	actions := planActions("node-a", []string{
		"High CPU utilization",
		"High memory utilization",
	}, nil, now)

	require.Len(t, actions, 2)
	require.NotEqual(t, actions[0].ID, actions[1].ID)
	require.Equal(t, ActionStatusProposed, actions[0].Status)
	require.Equal(t, ActionStatusProposed, actions[1].Status)
}

func TestReportsReturnsMostRecentFirst(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(10 * time.Second)
	t2 := t0.Add(20 * time.Second)

	e := &Engine{
		reports: map[string][]Report{
			"node-a": {
				{ID: "r-a0", GeneratedAt: t0},
				{ID: "r-a2", GeneratedAt: t2},
			},
			"node-b": {
				{ID: "r-b1", GeneratedAt: t1},
			},
		},
	}

	gotAll := e.Reports("")
	require.Len(t, gotAll, 3)
	require.Equal(t, "r-a2", gotAll[0].ID)
	require.Equal(t, "r-b1", gotAll[1].ID)
	require.Equal(t, "r-a0", gotAll[2].ID)

	gotNode := e.Reports("node-a")
	require.Len(t, gotNode, 2)
	require.Equal(t, "r-a2", gotNode[0].ID)
	require.Equal(t, "r-a0", gotNode[1].ID)
}

func TestActionsReturnsMostRecentlyUpdatedFirst(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(10 * time.Second)
	t2 := t0.Add(20 * time.Second)

	e := &Engine{
		actions: map[string]ActionDecision{
			"a0": {ID: "a0", NodeName: "node-a", Updated: t0},
			"a2": {ID: "a2", NodeName: "node-a", Updated: t2},
			"b1": {ID: "b1", NodeName: "node-b", Updated: t1},
		},
	}

	gotAll := e.Actions("")
	require.Len(t, gotAll, 3)
	require.Equal(t, "a2", gotAll[0].ID)
	require.Equal(t, "b1", gotAll[1].ID)
	require.Equal(t, "a0", gotAll[2].ID)

	gotNode := e.Actions("node-a")
	require.Len(t, gotNode, 2)
	require.Equal(t, "a2", gotNode[0].ID)
	require.Equal(t, "a0", gotNode[1].ID)
}

func TestUpdateActionNormalizesStatus(t *testing.T) {
	e := &Engine{
		actions: map[string]ActionDecision{
			"a1": {ID: "a1", Status: ActionStatusProposed, Updated: time.Unix(1_700_000_000, 0)},
		},
	}

	action, ok := e.UpdateAction("a1", "  COMPLETED ", "")
	require.True(t, ok)
	require.Equal(t, ActionStatusCompleted, action.Status)
}

func TestResolvePolicyFilePathFromBackendDir(t *testing.T) {
	repoDir := t.TempDir()
	backendDir := filepath.Join(repoDir, "backend")
	configsDir := filepath.Join(repoDir, "configs")
	require.NoError(t, os.MkdirAll(backendDir, 0o755))
	require.NoError(t, os.MkdirAll(configsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configsDir, "agent_playbooks.yaml"), []byte("version: v1\nplaybooks: []\n"), 0o644))

	wd, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		_ = os.Chdir(wd)
	}()
	require.NoError(t, os.Chdir(backendDir))

	resolved := resolvePolicyFilePath("./configs/agent_playbooks.yaml")
	require.Equal(t, filepath.Clean("../configs/agent_playbooks.yaml"), filepath.Clean(resolved))
}

func TestResolvePolicyFilePathReturnsConfiguredWhenMissing(t *testing.T) {
	missing := "./configs/does-not-exist.yaml"
	resolved := resolvePolicyFilePath(missing)
	require.Equal(t, missing, resolved)
}
