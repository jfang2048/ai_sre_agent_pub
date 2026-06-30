package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func defaultRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime caller unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo root %q is not a directory", root)
	}
	return root, nil
}

func resolveRepoRoot(candidate string) (string, error) {
	if strings.TrimSpace(candidate) != "" {
		return filepath.Clean(candidate), nil
	}
	return defaultRepoRoot()
}

func loadRetrievalCases(repoRoot string) ([]RetrievalCase, error) {
	path := filepath.Join(repoRoot, "eval_data", "retrieval_cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file RetrievalCaseFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(file.SchemaVersion) == "" {
		return nil, fmt.Errorf("%s missing schema_version", path)
	}
	return file.Cases, nil
}

func loadIncidentCases(repoRoot string) ([]IncidentCase, error) {
	path := filepath.Join(repoRoot, "eval_data", "incident_cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file IncidentCaseFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(file.SchemaVersion) == "" {
		return nil, fmt.Errorf("%s missing schema_version", path)
	}
	return file.Cases, nil
}

func loadAnomalyCases(repoRoot string) ([]AnomalyCase, error) {
	path := filepath.Join(repoRoot, "eval_data", "anomaly_cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file AnomalyCaseFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(file.SchemaVersion) == "" {
		return nil, fmt.Errorf("%s missing schema_version", path)
	}
	return file.Cases, nil
}

func filterRetrievalCases(cases []RetrievalCase, scope Scope) []RetrievalCase {
	out := make([]RetrievalCase, 0, len(cases))
	for _, item := range cases {
		if suiteAllowed(item.Suites, scope) {
			out = append(out, item)
		}
	}
	return out
}

func filterIncidentCases(cases []IncidentCase, scope Scope) []IncidentCase {
	out := make([]IncidentCase, 0, len(cases))
	for _, item := range cases {
		if suiteAllowed(item.Suites, scope) {
			out = append(out, item)
		}
	}
	return out
}

func filterAnomalyCases(cases []AnomalyCase, scope Scope) []AnomalyCase {
	out := make([]AnomalyCase, 0, len(cases))
	for _, item := range cases {
		if suiteAllowed(item.Suites, scope) {
			out = append(out, item)
		}
	}
	return out
}

func suiteAllowed(suites []string, scope Scope) bool {
	if len(suites) == 0 {
		return true
	}
	normalized := make([]string, 0, len(suites))
	for _, item := range suites {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			normalized = append(normalized, item)
		}
	}
	switch scope {
	case ScopeFast:
		return slices.Contains(normalized, string(ScopeFast))
	case ScopeRegression:
		return slices.Contains(normalized, string(ScopeFast)) || slices.Contains(normalized, string(ScopeRegression))
	case ScopeBenchmark:
		return slices.Contains(normalized, string(ScopeFast)) || slices.Contains(normalized, string(ScopeRegression)) || slices.Contains(normalized, string(ScopeBenchmark))
	default:
		return slices.Contains(normalized, string(ScopeFast))
	}
}
