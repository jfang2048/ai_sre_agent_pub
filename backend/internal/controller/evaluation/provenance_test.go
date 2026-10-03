package evaluation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
	"github.com/stretchr/testify/require"
)

func testV2Provenance(cfg V2ScoringConfig) V2Provenance {
	hash, _ := v2JSONFingerprint("test input")
	files := []V2FileFingerprint{{Path: "fixture/input", SHA256: hash}}
	manifestHash, _ := v2JSONFingerprint(files)
	scoringHash, _ := v2JSONFingerprint(cfg)
	policyHash, _ := v2JSONFingerprint(defaultV2RegressionPolicy())
	return V2Provenance{
		SchemaVersion:          v2ProvenanceSchema,
		EvaluatorSource:        "repository-source",
		CaseDefinitionsSHA256:  hash,
		IncidentInputsSHA256:   hash,
		ScoringConfigSHA256:    scoringHash,
		KnowledgeCorpusSHA256:  manifestHash,
		EvaluatorSHA256:        manifestHash,
		RegressionPolicySHA256: policyHash,
		KnowledgeFiles:         append([]V2FileFingerprint(nil), files...),
		EvaluatorFiles:         append([]V2FileFingerprint(nil), files...),
	}
}

func provenanceFixture(t *testing.T) (string, []V2Case, map[string]eval.IncidentCase) {
	t.Helper()
	root := t.TempDir()
	writeProvenanceFixture(t, root, "eval_data/knowledge/cases/runbook.md", "check memory pressure\n")
	writeProvenanceFixture(t, root, "backend/internal/controller/evaluation/outcome.go", "package evaluation\n// scoring rule\n")
	writeProvenanceFixture(t, root, "backend/internal/controller/eval/seed.go", "package eval\n// telemetry fixture\n")
	cases := []V2Case{{ID: "case-a", TaskType: TaskTypeDiagnose, IncidentCaseID: "incident-a", GroundTruth: V2GroundTruth{RequiredEvidence: []string{"memory"}}}}
	incidents := map[string]eval.IncidentCase{"incident-a": {ID: "incident-a", Query: "why is memory high?", Scenario: eval.TelemetryScenario{MetricSeries: []eval.MetricSeriesSpec{{Name: "memory", Value: 95}}}}}
	return root, cases, incidents
}

func writeProvenanceFixture(t *testing.T, root, path, content string) {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestV2ProvenanceReproducibleAndExcludesRuntimeAndGeneratedFiles(t *testing.T) {
	root, cases, incidents := provenanceFixture(t)
	cfg := defaultV2ScoringConfig()
	first, err := captureV2Provenance(root, cases, incidents, cfg)
	require.NoError(t, err)
	require.NoError(t, validateV2Provenance(first))
	// Different absolute paths and file modification times are not evidence.
	otherRoot, otherCases, otherIncidents := provenanceFixture(t)
	second, err := captureV2Provenance(otherRoot, otherCases, otherIncidents, cfg)
	require.NoError(t, err)
	require.Equal(t, first, second)

	for _, path := range []string{
		"backend/internal/controller/agentcore/workflow.go",
		"backend/internal/controller/rag/retrieval.go",
		"backend/internal/controller/evaluation/zz_private_debug_test.go",
		"backend/internal/controller/evaluation/report.go",
		"backend/internal/controller/evaluation/generated/report.json",
		"data/private-runtime/snapshot.json",
		"build/reports/report.json",
	} {
		writeProvenanceFixture(t, root, path, "not benchmark evidence")
	}
	second, err = captureV2Provenance(root, cases, incidents, cfg)
	require.NoError(t, err)
	require.Equal(t, first, second)
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	require.NotContains(t, string(raw), root)
	require.NotContains(t, string(raw), "private")
}

func TestV2ProvenanceRejectsChangedEvidenceWithUnchangedCaseIDs(t *testing.T) {
	for _, mutation := range []struct {
		name, field string
		edit        func(*testing.T, string, []V2Case, map[string]eval.IncidentCase, *V2ScoringConfig)
	}{
		{"oracle", "case_definitions_sha256", func(_ *testing.T, _ string, cases []V2Case, _ map[string]eval.IncidentCase, _ *V2ScoringConfig) {
			cases[0].GroundTruth.RequiredEvidence = []string{"cpu"}
		}},
		{"telemetry", "incident_inputs_sha256", func(_ *testing.T, _ string, _ []V2Case, incidents map[string]eval.IncidentCase, _ *V2ScoringConfig) {
			incident := incidents["incident-a"]
			incident.Scenario.MetricSeries[0].Value = 5
			incidents["incident-a"] = incident
		}},
		{"scoring", "scoring_config", func(_ *testing.T, _ string, _ []V2Case, _ map[string]eval.IncidentCase, cfg *V2ScoringConfig) {
			cfg.PassingThreshold = .99
		}},
		{"knowledge", "knowledge_corpus_sha256", func(t *testing.T, root string, _ []V2Case, _ map[string]eval.IncidentCase, _ *V2ScoringConfig) {
			writeProvenanceFixture(t, root, "eval_data/knowledge/cases/runbook.md", "changed runbook with same path")
		}},
		{"evaluator", "evaluator_sha256", func(t *testing.T, root string, _ []V2Case, _ map[string]eval.IncidentCase, _ *V2ScoringConfig) {
			writeProvenanceFixture(t, root, "backend/internal/controller/evaluation/outcome.go", "package evaluation\n// changed scoring rule\n")
		}},
		{"harness", "evaluator_sha256", func(t *testing.T, root string, _ []V2Case, _ map[string]eval.IncidentCase, _ *V2ScoringConfig) {
			writeProvenanceFixture(t, root, "backend/internal/controller/eval/seed.go", "package eval\n// changed generator\n")
		}},
		{"policy", "regression_policy_sha256", func(t *testing.T, root string, _ []V2Case, _ map[string]eval.IncidentCase, _ *V2ScoringConfig) {
			policy := defaultV2RegressionPolicy()
			policy.Rules[0].Threshold = -1
			raw, err := json.Marshal(policy)
			require.NoError(t, err)
			writeProvenanceFixture(t, root, "eval_data/regression_policy.json", string(raw))
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			root, cases, incidents := provenanceFixture(t)
			cfg := defaultV2ScoringConfig()
			before, err := captureV2Provenance(root, cases, incidents, cfg)
			require.NoError(t, err)
			baseline := comparableV2Report()
			baseline.Environment.Provenance = before
			mutation.edit(t, root, cases, incidents, &cfg)
			after, err := captureV2Provenance(root, cases, incidents, cfg)
			require.NoError(t, err)
			candidate := comparableV2Report()
			candidate.Config = cfg
			candidate.Environment.Provenance = after
			require.Equal(t, sortedV2CaseIDs(baseline.Cases), sortedV2CaseIDs(candidate.Cases))
			require.ErrorContains(t, validateComparableV2Reports(candidate, baseline), mutation.field)
		})
	}
}

func TestV2ProvenanceUsesSelectedResolvedInputs(t *testing.T) {
	root, cases, incidents := provenanceFixture(t)
	cfg := defaultV2ScoringConfig()
	before, err := captureV2Provenance(root, cases, incidents, cfg)
	require.NoError(t, err)
	incidents["unselected"] = eval.IncidentCase{ID: "unselected", Query: "unrelated"}
	after, err := captureV2Provenance(root, cases, incidents, cfg)
	require.NoError(t, err)
	require.Equal(t, before, after)

	inline := incidents["incident-a"]
	inline.Query = "different actual input"
	cases[0].IncidentCaseInline = &inline
	after, err = captureV2Provenance(root, cases, incidents, cfg)
	require.NoError(t, err)
	require.NotEqual(t, before.IncidentInputsSHA256, after.IncidentInputsSHA256)
	cases[0].IncidentCaseInline = nil
	delete(incidents, "incident-a")
	_, err = captureV2Provenance(root, cases, incidents, cfg)
	require.ErrorContains(t, err, "unknown incident")
}

func TestV2ProvenanceRejectsMissingOrInconsistentEvidence(t *testing.T) {
	for _, edit := range []func(*V2Provenance){
		func(p *V2Provenance) { p.CaseDefinitionsSHA256 = "" },
		func(p *V2Provenance) { p.IncidentInputsSHA256 = "" },
		func(p *V2Provenance) { p.ScoringConfigSHA256 = "" },
		func(p *V2Provenance) { p.KnowledgeCorpusSHA256 = "" },
		func(p *V2Provenance) { p.EvaluatorSHA256 = "" },
		func(p *V2Provenance) { p.RegressionPolicySHA256 = "" },
		func(p *V2Provenance) { p.KnowledgeFiles = nil },
		func(p *V2Provenance) { p.EvaluatorFiles = nil },
		func(p *V2Provenance) { p.EvaluatorFiles[0].SHA256 = strings.Repeat("a", 64) },
		func(p *V2Provenance) { p.EvaluatorFiles[0].Path = "/private/path" },
	} {
		p := testV2Provenance(defaultV2ScoringConfig())
		edit(&p)
		require.Error(t, validateV2Provenance(p))
	}
}

func TestV2ProvenanceRejectsSymlinksAndOversizedInputs(t *testing.T) {
	t.Run("missing harness sources", func(t *testing.T) {
		root, cases, incidents := provenanceFixture(t)
		require.NoError(t, os.Remove(filepath.Join(root, "backend/internal/controller/eval/seed.go")))
		_, err := captureV2Provenance(root, cases, incidents, defaultV2ScoringConfig())
		require.ErrorContains(t, err, "no input files")
	})
	t.Run("symlink", func(t *testing.T) {
		root, cases, incidents := provenanceFixture(t)
		external := filepath.Join(t.TempDir(), "external.md")
		require.NoError(t, os.WriteFile(external, []byte("private"), 0o600))
		require.NoError(t, os.Symlink(external, filepath.Join(root, "eval_data/knowledge/linked.md")))
		_, err := captureV2Provenance(root, cases, incidents, defaultV2ScoringConfig())
		require.ErrorContains(t, err, "symlinks")
	})
	t.Run("oversized", func(t *testing.T) {
		root, cases, incidents := provenanceFixture(t)
		file, err := os.Create(filepath.Join(root, "eval_data/knowledge/oversized.md"))
		require.NoError(t, err)
		require.NoError(t, file.Truncate(v2FingerprintMaxFileBytes+1))
		require.NoError(t, file.Close())
		_, err = captureV2Provenance(root, cases, incidents, defaultV2ScoringConfig())
		require.ErrorContains(t, err, "limit exceeded")
	})
}

func TestV2ComparisonRejectsPolicyChangedAfterReport(t *testing.T) {
	root := t.TempDir()
	report := comparableV2Report()
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	writeProvenanceFixture(t, root, "baseline.json", string(raw))
	policy := defaultV2RegressionPolicy()
	policy.Rules[0].Threshold = -1
	raw, err = json.Marshal(policy)
	require.NoError(t, err)
	writeProvenanceFixture(t, root, "eval_data/regression_policy.json", string(raw))
	_, err = compareV2Reports(report, "baseline.json", root)
	require.ErrorContains(t, err, "policy changed")
}

func TestV2ProvenanceIncludesRepositoryFixtureAndEvaluatorSources(t *testing.T) {
	root, err := eval.ResolveRepoRoot("")
	require.NoError(t, err)
	cases, err := loadV2Cases(root, eval.ScopeFast, nil)
	require.NoError(t, err)
	incidents, err := eval.LoadIncidentCases(root)
	require.NoError(t, err)
	byID := make(map[string]eval.IncidentCase)
	for _, incident := range incidents {
		byID[incident.ID] = incident
	}
	cfg, err := loadV2ScoringConfig(root)
	require.NoError(t, err)
	p, err := captureV2Provenance(root, cases, byID, cfg)
	require.NoError(t, err)
	require.NoError(t, validateV2Provenance(p))
	paths := make([]string, 0, len(p.EvaluatorFiles))
	for _, f := range p.EvaluatorFiles {
		paths = append(paths, f.Path)
		require.NotContains(t, f.Path, "_test.go")
	}
	require.Contains(t, paths, "backend/internal/controller/evaluation/outcome.go")
	require.Contains(t, paths, "backend/internal/controller/evaluation/system_performance_v2_runner.go")
	require.Contains(t, paths, "backend/internal/controller/eval/seed.go")
	require.NotEmpty(t, p.KnowledgeFiles)
}
