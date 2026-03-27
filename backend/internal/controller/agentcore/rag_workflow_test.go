package agent

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestJointRiskWorkflowIncludesRetrievedKnowledge(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-rag-risk")

	engine := NewWorkflowEngine(DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	engine.SetKnowledgeBase(newWorkflowTestKnowledgeBase(t))

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-rag-risk",
		Window:      50 * time.Minute,
		Trigger:     "rag-test",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.RetrievedDocs)
	require.NotEmpty(t, report.RetrievedRunbooks)
	require.NotEmpty(t, report.RetrievalSummary)
	require.NotEmpty(t, report.RetrievalEvidenceIDs)
	require.Contains(t, report.Recommendations[0].Summary+stringsFromRecommendations(report.Recommendations), "retrieved")
	toolNames := workflowToolNames(report.ToolCalls)
	require.Contains(t, toolNames, string(ToolSimilarCase))
	require.Contains(t, toolNames, string(ToolRunbookRetrieval))
}

func TestRCAWorkflowIncludesRetrievedKnowledgeEvidence(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-rag-rca")

	engine := NewWorkflowEngine(DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	engine.SetKnowledgeBase(newWorkflowTestKnowledgeBase(t))

	report, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-rag-rca",
		Window:      50 * time.Minute,
		Trigger:     "rag-test",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.RetrievedDocs)
	require.NotEmpty(t, report.RetrievedCases)
	require.NotEmpty(t, report.RetrievedRunbooks)
	require.NotEmpty(t, report.RetrievalSummary)
	require.NotEmpty(t, report.RetrievalEvidenceIDs)

	foundKnowledgeEvidence := false
	for _, evidence := range report.Evidence {
		if evidence.Kind == "knowledge_retrieval" {
			foundKnowledgeEvidence = true
			break
		}
	}
	require.True(t, foundKnowledgeEvidence)
}

func TestPotentialRiskFindingsCarryRetrievedKnowledge(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-rag-potential")

	engine := NewWorkflowEngine(DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	engine.SetKnowledgeBase(newWorkflowTestKnowledgeBase(t))

	require.NoError(t, engine.RefreshPotentialRiskFindings(context.Background(), WorkflowRequest{
		CollectorID: "collector-rag-potential",
		Window:      50 * time.Minute,
		Limit:       1,
		Trigger:     "rag-test",
	}))

	findings := engine.PotentialRiskFindings(10, "collector-rag-potential")
	require.NotEmpty(t, findings)
	require.NotEmpty(t, findings[0].RetrievedDocs)
	require.NotEmpty(t, findings[0].RetrievedRunbooks)
	require.NotEmpty(t, findings[0].RetrievalSummary)
	require.NotEmpty(t, findings[0].RetrievalEvidenceIDs)
}

func newWorkflowTestKnowledgeBase(t *testing.T) rag.KnowledgeBase {
	t.Helper()

	datasetDir := filepath.Join(t.TempDir(), "dataset")
	require.NoError(t, os.MkdirAll(filepath.Join(datasetDir, "cases"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(datasetDir, "cases", "incident-runbook.md"), []byte(`# Deployment Timeout Runbook

Investigate timeout bursts after deployment by checking retry rate, cache credentials, and rollout timing.
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(datasetDir, "cases", "history.jsonl"), []byte(
		"{\"id\":\"hist-1\",\"query\":\"timeout after rollout\",\"document\":\"A cache credential regression caused post-deploy timeout spikes until the secret rotation completed.\"}\n",
	), 0o644))

	archivePath := filepath.Join(datasetDir, "manual.zip")
	file, err := os.Create(archivePath)
	require.NoError(t, err)
	zipWriter := zip.NewWriter(file)
	entry, err := zipWriter.Create("guides/cache.txt")
	require.NoError(t, err)
	_, err = entry.Write([]byte("Cache guides recommend validating rotated credentials before shifting traffic after a deployment."))
	require.NoError(t, err)
	require.NoError(t, zipWriter.Close())
	require.NoError(t, file.Close())

	cfg := rag.DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")

	service, err := rag.NewService(cfg, zap.NewNop())
	require.NoError(t, err)
	_, err = service.Rebuild(context.Background())
	require.NoError(t, err)
	return service
}

func workflowToolNames(calls []WorkflowToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, string(call.Tool))
	}
	return out
}

func stringsFromRecommendations(recs []WorkflowRecommendation) string {
	out := ""
	for _, rec := range recs {
		out += " " + rec.Summary + " " + rec.Details
	}
	return out
}
