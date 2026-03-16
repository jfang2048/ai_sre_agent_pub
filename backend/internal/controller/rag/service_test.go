package rag

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDiscoverSourceUnitsFromRepositoryDataset(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = repositoryDatasetPath(t)
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")

	units, quarantine, err := discoverSourceUnits(cfg)
	require.NoError(t, err)
	require.NotEmpty(t, units)

	sourceTypes := map[string]int{}
	archiveUnitCount := 0
	for _, unit := range units {
		sourceTypes[unit.SourceType]++
		if strings.Contains(unit.SourcePath, "::") {
			archiveUnitCount++
		}
	}
	require.Equal(t, 0, sourceTypes["json"])
	require.Equal(t, 0, sourceTypes["jsonl"])
	require.Equal(t, 0, sourceTypes["csv"])
	require.Equal(t, 0, archiveUnitCount)
	require.NotEmpty(t, quarantine)
}

func TestChunkDocumentsPreservesLineage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ChunkSize = 80
	cfg.ChunkOverlap = 12
	cfg.ChunkStrategy = "line"

	doc := SourceDocument{
		DocID:      "doc-1",
		SourceKey:  "cases/runbook.md",
		SourcePath: "cases/runbook.md",
		SourceType: "markdown",
		Title:      "Latency Runbook",
		Content: strings.Repeat(
			"latency symptom\ncheck deployment\ninspect retries\nvalidate cache\n",
			8,
		),
	}

	chunks := chunkDocuments(doc, cfg)
	require.Greater(t, len(chunks), 1)
	require.Equal(t, "doc-1", chunks[0].DocID)
	require.Equal(t, "cases/runbook.md", chunks[0].SourcePath)
	require.Equal(t, 1, chunks[0].ChunkIndex)
	require.Less(t, chunks[0].OffsetStart, chunks[0].OffsetEnd)
	require.NotEmpty(t, chunks[0].ChunkID)
	require.Equal(t, cfg.ChunkStrategy, chunks[0].Strategy)
	require.GreaterOrEqual(t, chunks[1].OffsetStart, 0)
	require.Less(t, chunks[1].OffsetStart, chunks[0].OffsetEnd)
}

func TestServiceBuildQueryAndDocumentAreDeterministic(t *testing.T) {
	datasetDir := writeTestDataset(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")
	cfg.RebuildPolicy = "manual"

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)

	stats, err := service.Rebuild(context.Background())
	require.NoError(t, err)
	require.True(t, stats.Ready)
	require.Greater(t, stats.DocCount, 0)
	require.Greater(t, stats.ChunkCount, 0)
	require.NotEmpty(t, stats.KnowledgeTypes)
	require.NotEmpty(t, stats.CaseTypes)

	first, err := service.Query(context.Background(), QueryRequest{
		Query: "timeout deployment runbook cache",
		TopK:  4,
	})
	require.NoError(t, err)
	require.NotEmpty(t, first.Hits)
	require.NotEmpty(t, first.RetrievalEvidenceIDs)
	require.NotEmpty(t, first.NormalizedQuery)
	require.NotEmpty(t, first.Intent)
	require.NotEmpty(t, first.Hits[0].KnowledgeType)
	require.NotEmpty(t, first.Hits[0].CaseType)
	require.NotEmpty(t, first.Hits[0].Summary)

	second, err := service.Query(context.Background(), QueryRequest{
		Query: "timeout deployment runbook cache",
		TopK:  4,
	})
	require.NoError(t, err)
	require.Equal(t, first.RetrievalMode, second.RetrievalMode)
	require.Equal(t, first.Hits[0].ChunkID, second.Hits[0].ChunkID)
	require.Equal(t, first.RetrievalEvidenceIDs, second.RetrievalEvidenceIDs)
	require.InDelta(t, first.Hits[0].Score, second.Hits[0].Score, 1e-9)

	record, ok := service.Document(first.Hits[0].ChunkID)
	require.True(t, ok)
	require.NotEmpty(t, record.Document.DocID)
	require.NotEmpty(t, record.Chunks)
	require.NotEmpty(t, record.Document.KnowledgeType)
	require.NotEmpty(t, record.Document.RetrievalText)
	require.NotEmpty(t, record.Chunks[0].RetrievalText)
}

func TestQueryIntentPrefersRunbooksAndStructuredKnowledge(t *testing.T) {
	datasetDir := writeTestDataset(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)
	_, err = service.Rebuild(context.Background())
	require.NoError(t, err)

	result, err := service.Query(context.Background(), QueryRequest{
		Query:  "how to troubleshoot deployment timeout cache credentials",
		Intent: "runbook",
		TopK:   3,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hits)
	require.Equal(t, "runbook", result.Intent)
	require.Equal(t, "runbook", result.Hits[0].KnowledgeType)
	require.NotEmpty(t, result.Hits[0].RemediationSteps)
	require.NotEmpty(t, result.Hits[0].Signals)
}

func TestQueryFiltersByKnowledgeTypeAndCaseType(t *testing.T) {
	datasetDir := writeTestDataset(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)
	_, err = service.Rebuild(context.Background())
	require.NoError(t, err)

	runbooks, err := service.Query(context.Background(), QueryRequest{
		Query:          "deployment timeout cache",
		Intent:         "runbook",
		KnowledgeTypes: []string{"runbook"},
		TopK:           4,
	})
	require.NoError(t, err)
	require.NotEmpty(t, runbooks.Hits)
	for _, hit := range runbooks.Hits {
		require.Equal(t, "runbook", hit.KnowledgeType)
	}

	incidents, err := service.Query(context.Background(), QueryRequest{
		Query:     "timeout after rollout",
		Intent:    "historical_incident",
		CaseTypes: []string{"historical_incident"},
		TopK:      4,
	})
	require.NoError(t, err)
	require.NotEmpty(t, incidents.Hits)
	for _, hit := range incidents.Hits {
		require.Equal(t, "historical_incident", hit.CaseType)
	}
}

func TestNewServiceQuarantinesInvalidIndexAndRebuildsWhenConfigured(t *testing.T) {
	datasetDir := writeTestDataset(t)
	indexPath := filepath.Join(t.TempDir(), "rag", "index.json")
	invalidIndex := `{
  "schema": "rag-v0.7",
  "built_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z",
  "documents": [
    {
      "doc_id": "doc-1",
      "source_key": "cases/runbook.md",
      "source_path": "cases/runbook.md",
      "source_type": "markdown",
      "title": "Runbook",
      "content": "content"
    }
  ],
  "chunks": [
    {
      "chunk_id": "chunk-1",
      "doc_id": "missing-doc",
      "source_key": "cases/runbook.md",
      "source_path": "cases/runbook.md",
      "source_type": "markdown",
      "title": "Runbook",
      "content": "content",
      "strategy": "markdown"
    }
  ],
  "sources": [
    {
      "source_key": "cases/runbook.md",
      "source_path": "cases/runbook.md",
      "source_type": "markdown",
      "signature": "sig",
      "doc_ids": ["doc-1"],
      "chunk_ids": ["chunk-1"]
    }
  ]
}`
	require.NoError(t, os.MkdirAll(filepath.Dir(indexPath), 0o755))
	require.NoError(t, os.WriteFile(indexPath, []byte(invalidIndex), 0o644))

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = datasetDir
	cfg.IndexPath = indexPath
	cfg.RebuildPolicy = "if_missing"

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)

	stats := service.Stats()
	require.True(t, stats.Ready)
	require.Contains(t, stats.LastError, "quarantined")

	entries, err := os.ReadDir(storagePath(indexPath))
	require.NoError(t, err)
	foundCorrupt := false
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "index.corrupt-") {
			foundCorrupt = true
			break
		}
	}
	require.True(t, foundCorrupt)
}

func writeTestDataset(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cases"), 0o755))

	runbook := `# Timeout Runbook

When payment requests time out after a deployment:
- inspect dependency retry rates
- compare rollout timestamps with latency spikes
- validate cache credentials and downstream DNS
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "cases", "timeout-runbook.md"), []byte(runbook), 0o644))

	jsonl := `{"id":"case-1","query":"deployment timeout","document":"Timeouts after rollout were fixed by reverting a bad cache credential change."}
{"id":"case-2","query":"dns failure","document":"A stale resolver entry caused intermittent upstream errors."}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "cases", "historical.jsonl"), []byte(jsonl), 0o644))

	csv := "\ufeffQuestion,LinkToAnswer\nHow to inspect retry rate?,Use the retry dashboard and deployment audit timeline.\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "cases", "faq.csv"), []byte(csv), 0o644))

	archivePath := filepath.Join(root, "support.zip")
	file, err := os.Create(archivePath)
	require.NoError(t, err)
	zipWriter := zip.NewWriter(file)
	entry, err := zipWriter.Create("manuals/cache-guide.txt")
	require.NoError(t, err)
	_, err = entry.Write([]byte("Cache credential rotation can trigger timeout spikes during deployment if clients keep stale secrets."))
	require.NoError(t, err)
	require.NoError(t, zipWriter.Close())
	require.NoError(t, file.Close())

	return root
}

func repositoryDatasetPath(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "dataset"))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	return path
}
