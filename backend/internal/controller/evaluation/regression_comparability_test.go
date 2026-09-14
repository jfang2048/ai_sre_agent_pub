package evaluation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func comparableV2Report() SystemPerformanceReportV2 {
	return SystemPerformanceReportV2{
		SchemaVersion: "system-performance/v2",
		Environment: V2Environment{
			RuntimeMode:   "legacy_deterministic",
			Scope:         "regression",
			TrialsPerCase: 3,
			Seed:          42,
		},
		Config: defaultV2ScoringConfig(),
		Cases: []V2CaseResult{
			{ID: "case-a", Trials: 3},
			{ID: "case-b", Trials: 3},
		},
	}
}

func TestValidateComparableV2Reports(t *testing.T) {
	base := comparableV2Report()
	require.NoError(t, validateComparableV2Reports(base, base))

	tests := []struct {
		name string
		edit func(*SystemPerformanceReportV2)
		want string
	}{
		{"schema", func(r *SystemPerformanceReportV2) { r.SchemaVersion = "system-performance/v1" }, "schema_version"},
		{"runtime", func(r *SystemPerformanceReportV2) { r.Environment.RuntimeMode = "hybrid_adaptive" }, "runtime_mode"},
		{"scope", func(r *SystemPerformanceReportV2) { r.Environment.Scope = "benchmark" }, "scope"},
		{"trials", func(r *SystemPerformanceReportV2) { r.Environment.TrialsPerCase = 5 }, "trials_per_case"},
		{"seed", func(r *SystemPerformanceReportV2) { r.Environment.Seed = 7 }, "seed"},
		{"dirty worktree", func(r *SystemPerformanceReportV2) { r.Environment.WorktreeDirty = true }, "worktree_dirty"},
		{"cases", func(r *SystemPerformanceReportV2) { r.Cases[1].ID = "case-c" }, "case_ids"},
		{"scoring", func(r *SystemPerformanceReportV2) { r.Config.PassingThreshold = 0.99 }, "scoring_config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := comparableV2Report()
			tt.edit(&candidate)
			err := validateComparableV2Reports(candidate, base)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestCompareV2ReportsRejectsIncomparableBaseline(t *testing.T) {
	repoRoot := t.TempDir()
	baseline := comparableV2Report()
	baseline.Environment.Scope = "benchmark"
	raw, err := json.Marshal(baseline)
	require.NoError(t, err)
	path := filepath.Join(repoRoot, "baseline.json")
	require.NoError(t, os.WriteFile(path, raw, 0o644))

	_, err = compareV2Reports(comparableV2Report(), path, repoRoot)
	require.ErrorContains(t, err, "scope")
}

func TestDefaultRegressionPolicyUsesPercentagePointThresholds(t *testing.T) {
	policy := defaultV2RegressionPolicy()
	thresholds := make(map[string]float64, len(policy.Rules))
	for _, rule := range policy.Rules {
		thresholds[rule.Metric] = rule.Threshold
	}
	require.Equal(t, 1.0, thresholds["unsafe_action_rate"])
	require.Equal(t, 1.0, thresholds["approval_bypass_rate"])
}

func TestPublicBaselinePathDoesNotExposeAbsoluteDirectories(t *testing.T) {
	repoRoot := filepath.Join(string(filepath.Separator), "srv", "public-repo")
	require.Equal(t, "data/eval/baseline.json", publicBaselinePath(filepath.Join(repoRoot, "data", "eval", "baseline.json"), repoRoot))
	require.Equal(t, "baseline.json", publicBaselinePath(filepath.Join(string(filepath.Separator), "private", "operator", "baseline.json"), repoRoot))
}
