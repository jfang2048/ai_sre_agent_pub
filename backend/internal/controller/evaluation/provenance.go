package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

const v2ProvenanceSchema = "evaluation-inputs/v1"

// These are fixture/source limits, not runtime data directories. In particular,
// data/, reports, caches, Git history, environment variables and credentials are
// never scanned. Exceeding a bound fails the run instead of hashing a subset.
const (
	v2FingerprintMaxFiles      = 1024
	v2FingerprintMaxFileBytes  = 4 << 20
	v2FingerprintMaxTotalBytes = 32 << 20
)

func captureV2Provenance(repoRoot string, cases []V2Case, incidents map[string]eval.IncidentCase, cfg V2ScoringConfig) (V2Provenance, error) {
	p := V2Provenance{SchemaVersion: v2ProvenanceSchema, EvaluatorSource: "repository-source"}
	// The loaded values are the inputs actually passed to the runner. Hashing
	// selected cases avoids invalidating a fast baseline for an unrelated case
	// added to a different scope. Preserve order: execution order is an input.
	var err error
	if p.CaseDefinitionsSHA256, err = v2JSONFingerprint(cases); err != nil {
		return p, err
	}
	type resolvedIncident struct {
		CaseID   string            `json:"case_id"`
		Incident eval.IncidentCase `json:"incident"`
	}
	resolved := make([]resolvedIncident, 0, len(cases))
	for _, c := range cases {
		incident, ok := incidents[c.IncidentCaseID]
		if c.IncidentCaseInline != nil {
			incident, ok = *c.IncidentCaseInline, true
		}
		if !ok {
			return p, fmt.Errorf("provenance: case %s references unknown incident %s", c.ID, c.IncidentCaseID)
		}
		resolved = append(resolved, resolvedIncident{CaseID: c.ID, Incident: incident})
	}
	if p.IncidentInputsSHA256, err = v2JSONFingerprint(resolved); err != nil {
		return p, err
	}
	if p.ScoringConfigSHA256, err = v2JSONFingerprint(cfg); err != nil {
		return p, err
	}
	policy, err := loadV2RegressionPolicy(repoRoot)
	if err != nil {
		return p, err
	}
	if p.RegressionPolicySHA256, err = v2JSONFingerprint(policy); err != nil {
		return p, err
	}
	if p.KnowledgeFiles, err = v2FingerprintFiles(repoRoot, []string{"eval_data/knowledge"}, false); err != nil {
		return p, err
	}
	if p.KnowledgeCorpusSHA256, err = v2JSONFingerprint(p.KnowledgeFiles); err != nil {
		return p, err
	}
	// eval holds the fixture generator and workflow harness; evaluation holds
	// extraction, scoring, statistical and regression rules. agentcore, rag and
	// other runtime packages are the candidate being measured, not the oracle.
	if p.EvaluatorFiles, err = v2FingerprintFiles(repoRoot, []string{
		"backend/internal/controller/eval",
		"backend/internal/controller/evaluation",
	}, true); err != nil {
		return p, err
	}
	p.EvaluatorSHA256, err = v2JSONFingerprint(p.EvaluatorFiles)
	return p, err
}

func v2JSONFingerprint(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal evaluation fingerprint: %w", err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func v2FingerprintFiles(repoRoot string, roots []string, sourceOnly bool) ([]V2FileFingerprint, error) {
	files := make([]V2FileFingerprint, 0)
	var total int64
	for _, root := range roots {
		filesBefore := len(files)
		rootInfo, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(root)))
		if err != nil || !rootInfo.IsDir() {
			return nil, fmt.Errorf("provenance root %s must be an accessible directory, not a symlink", root)
		}
		err = filepath.WalkDir(filepath.Join(repoRoot, filepath.FromSlash(root)), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return fmt.Errorf("read provenance root %s: %w", root, walkErr)
			}
			if entry.IsDir() {
				// Harness packages are flat. Do not collect nested runtime or
				// generated directories that happen to sit under the package.
				if sourceOnly && path != filepath.Join(repoRoot, filepath.FromSlash(root)) {
					return filepath.SkipDir
				}
				return nil
			}
			if sourceOnly && (!strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") || entry.Name() == "report.go" || entry.Name() == "system_performance.go") {
				return nil
			}
			relative, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("stat provenance input %s: %w", relative, err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("provenance input %s must be a regular file (symlinks are not supported)", relative)
			}
			if len(files) >= v2FingerprintMaxFiles || info.Size() > v2FingerprintMaxFileBytes {
				return fmt.Errorf("provenance input limit exceeded at %s", relative)
			}
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("read provenance input %s: %w", relative, err)
			}
			h := sha256.New()
			n, readErr := io.Copy(h, io.LimitReader(f, v2FingerprintMaxFileBytes+1))
			closeErr := f.Close()
			if readErr != nil {
				return fmt.Errorf("hash provenance input %s: %w", relative, readErr)
			}
			if closeErr != nil {
				return closeErr
			}
			total += n
			if n > v2FingerprintMaxFileBytes || total > v2FingerprintMaxTotalBytes {
				return fmt.Errorf("provenance input limit exceeded at %s", relative)
			}
			files = append(files, V2FileFingerprint{Path: relative, SHA256: hex.EncodeToString(h.Sum(nil))})
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(files) == filesBefore {
			return nil, fmt.Errorf("provenance has no input files in %s", root)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func validateV2Provenance(p V2Provenance) error {
	if p.SchemaVersion != v2ProvenanceSchema || p.EvaluatorSource != "repository-source" {
		return fmt.Errorf("missing or unsupported provenance schema/source; regenerate the baseline with this evaluator")
	}
	for _, item := range []struct{ name, value string }{
		{"case_definitions_sha256", p.CaseDefinitionsSHA256},
		{"incident_inputs_sha256", p.IncidentInputsSHA256},
		{"scoring_config_sha256", p.ScoringConfigSHA256},
		{"knowledge_corpus_sha256", p.KnowledgeCorpusSHA256},
		{"evaluator_sha256", p.EvaluatorSHA256},
		{"regression_policy_sha256", p.RegressionPolicySHA256},
	} {
		if !validV2SHA256(item.value) {
			return fmt.Errorf("missing or invalid provenance %s; regenerate the baseline", item.name)
		}
	}
	for _, item := range []struct {
		name, hash string
		files      []V2FileFingerprint
	}{
		{"knowledge_files", p.KnowledgeCorpusSHA256, p.KnowledgeFiles},
		{"evaluator_files", p.EvaluatorSHA256, p.EvaluatorFiles},
	} {
		if len(item.files) == 0 || len(item.files) > v2FingerprintMaxFiles {
			return fmt.Errorf("missing or invalid provenance %s", item.name)
		}
		previous := ""
		for _, f := range item.files {
			if f.Path <= previous || strings.Contains(f.Path, "\\") || filepath.IsAbs(f.Path) || filepath.ToSlash(filepath.Clean(f.Path)) != f.Path || strings.HasPrefix(f.Path, "../") || !validV2SHA256(f.SHA256) {
				return fmt.Errorf("invalid provenance %s manifest", item.name)
			}
			previous = f.Path
		}
		digest, err := v2JSONFingerprint(item.files)
		if err != nil || digest != item.hash {
			return fmt.Errorf("inconsistent provenance %s digest", item.name)
		}
	}
	return nil
}

func validV2SHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func differingV2Provenance(a, b V2Provenance) []string {
	var changed []string
	for _, item := range []struct{ name, a, b string }{
		{"case_definitions_sha256", a.CaseDefinitionsSHA256, b.CaseDefinitionsSHA256},
		{"incident_inputs_sha256", a.IncidentInputsSHA256, b.IncidentInputsSHA256},
		{"scoring_config_sha256", a.ScoringConfigSHA256, b.ScoringConfigSHA256},
		{"knowledge_corpus_sha256", a.KnowledgeCorpusSHA256, b.KnowledgeCorpusSHA256},
		{"evaluator_sha256", a.EvaluatorSHA256, b.EvaluatorSHA256},
		{"regression_policy_sha256", a.RegressionPolicySHA256, b.RegressionPolicySHA256},
	} {
		if item.a != item.b {
			changed = append(changed, item.name)
		}
	}
	return changed
}
