package eval

import (
	"context"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
)

// ResolveRepoRoot returns the repository root used by the evaluation fixtures.
func ResolveRepoRoot(candidate string) (string, error) {
	return resolveRepoRoot(candidate)
}

// LoadIncidentCases loads the workflow evaluation fixtures from disk.
func LoadIncidentCases(repoRoot string) ([]IncidentCase, error) {
	return loadIncidentCases(repoRoot)
}

// FilterIncidentCases narrows incident cases to the requested evaluation scope.
func FilterIncidentCases(cases []IncidentCase, scope Scope) []IncidentCase {
	return filterIncidentCases(cases, scope)
}

// BuildKnowledgeBase constructs the real RAG knowledge base used by evaluation runs.
func BuildKnowledgeBase(ctx context.Context, repoRoot string) (rag.KnowledgeBase, func(), error) {
	return buildKnowledgeBase(ctx, repoRoot)
}

// RunWorkflowCaseDetailed executes one workflow case and exposes the resulting
// real runtime artifacts for higher-level evaluation modules.
func RunWorkflowCaseDetailed(ctx context.Context, kb rag.KnowledgeBase, item IncidentCase, opts WorkflowCaseRunOptions) (WorkflowCaseExecution, error) {
	return runWorkflowCaseDetailed(ctx, kb, item, opts)
}
