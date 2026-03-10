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
	require.Greater(t, sourceTypes["json"], 0)
	require.Greater(t, sourceTypes["jsonl"], 0)
	require.Greater(t, sourceTypes["csv"], 0)
	require.Greater(t, archiveUnitCount, 0)
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

	first, err := service.Query(context.Background(), QueryRequest{
		Query: "timeout deployment runbook cache",
		TopK:  4,
	})
	require.NoError(t, err)
	require.NotEmpty(t, first.Hits)
	require.NotEmpty(t, first.RetrievalEvidenceIDs)

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
