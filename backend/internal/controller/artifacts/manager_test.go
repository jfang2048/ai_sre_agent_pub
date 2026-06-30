package artifacts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestManagerWriteReadListStableKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manager, status, err := NewManager(context.Background(), Config{
		MetadataBackend: "bbolt",
		MetadataPath:    filepath.Join(root, "artifacts.db"),
		PayloadBackend:  "filesystem",
		PayloadRootPath: filepath.Join(root, "payloads"),
	}, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	require.Equal(t, "bbolt", status.MetadataBackend)
	require.Equal(t, "filesystem", status.PayloadBackend)
	require.True(t, status.MetadataPersistent)
	require.False(t, status.MetadataShared)

	evidence, err := manager.Write(context.Background(), WriteRequest{
		ArtifactID:    "evidence-run-a",
		ArtifactType:  ArtifactTypeEvidencePackage,
		OwnerType:     OwnerTypeWorkflowRun,
		OwnerID:       "run-a",
		RunID:         "run-a",
		CollectorID:   "collector-a",
		ContentType:   "application/json",
		FileExtension: ".json",
		Payload:       []byte(`{"run_id":"run-a","kind":"evidence"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "filesystem", evidence.StorageBackend)
	require.Equal(t, "evidence/run-a/evidence-run-a.json", evidence.StorageKey)
	require.NotEmpty(t, evidence.LocalCachePath)

	history, err := manager.Write(context.Background(), WriteRequest{
		ArtifactID:    "msg-history-run-a",
		ArtifactType:  ArtifactTypeWorkflowMessageIndex,
		OwnerType:     OwnerTypeWorkflowRun,
		OwnerID:       "run-a",
		RunID:         "run-a",
		ContentType:   "application/json",
		FileExtension: ".json",
		Payload:       []byte(`{"run_id":"run-a","messages":[]}`),
	})
	require.NoError(t, err)
	require.Equal(t, "messages/run-a/msg-history-run-a.json", history.StorageKey)

	loaded, err := manager.Get(context.Background(), evidence.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, evidence.StorageKey, loaded.StorageKey)
	require.Equal(t, evidence.RunID, loaded.RunID)

	items, err := manager.List(context.Background(), Filter{RunID: "run-a"})
	require.NoError(t, err)
	require.Len(t, items, 2)

	evidenceOnly, err := manager.List(context.Background(), Filter{ArtifactType: ArtifactTypeEvidencePackage})
	require.NoError(t, err)
	require.Len(t, evidenceOnly, 1)
	require.Equal(t, evidence.ArtifactID, evidenceOnly[0].ArtifactID)

	payload, record, err := manager.Read(context.Background(), evidence.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, evidence.ArtifactID, record.ArtifactID)
	require.JSONEq(t, `{"run_id":"run-a","kind":"evidence"}`, string(payload))
	require.NotZero(t, record.SizeBytes)
	require.NotEmpty(t, record.Checksum)
}

func TestManagerReadReturnsClearErrorWhenPayloadMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manager, _, err := NewManager(context.Background(), Config{
		MetadataBackend: "bbolt",
		MetadataPath:    filepath.Join(root, "artifacts.db"),
		PayloadBackend:  "filesystem",
		PayloadRootPath: filepath.Join(root, "payloads"),
	}, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	record, err := manager.Write(context.Background(), WriteRequest{
		ArtifactID:    "memory-incident-1",
		ArtifactType:  ArtifactTypeIncidentMemoryRecord,
		OwnerType:     OwnerTypeIncidentMemory,
		OwnerID:       "incident-1",
		RunID:         "run-incident-1",
		ContentType:   "application/json",
		FileExtension: ".json",
		Payload:       []byte(`{"record_id":"incident-1"}`),
	})
	require.NoError(t, err)
	require.NoError(t, os.Remove(record.LocalCachePath))

	payload, loaded, err := manager.Read(context.Background(), record.ArtifactID)
	require.Nil(t, payload)
	require.Error(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, record.ArtifactID, loaded.ArtifactID)
	require.Contains(t, err.Error(), "artifact payload missing")
}

func TestManagerRegistersS3PayloadMetadata(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	payloadStore, err := newS3PayloadStoreWithClient(Config{
		PayloadS3Bucket: "artifacts",
		PayloadS3Prefix: "controller-a",
	}, client)
	require.NoError(t, err)

	manager := &Manager{
		metadata: NewMemoryStore(),
		payload:  payloadStore,
		status: Status{
			Enabled:                 true,
			MetadataBackend:         "memory",
			PayloadBackend:          "s3",
			PayloadShared:           true,
			PayloadSharedSurvivable: true,
			PayloadContainer:        "artifacts",
			PayloadPrefix:           "controller-a",
			AddressingMode:          "stable_keys",
			GCEnabled:               true,
			GCBatchSize:             16,
		},
		logger: zap.NewNop(),
	}

	record, err := manager.Write(context.Background(), WriteRequest{
		ArtifactID:    "evidence-run-s3",
		ArtifactType:  ArtifactTypeEvidencePackage,
		OwnerType:     OwnerTypeWorkflowRun,
		OwnerID:       "run-s3",
		RunID:         "run-s3",
		FileExtension: ".json",
		ContentType:   "application/json",
		Payload:       []byte(`{"run_id":"run-s3","backend":"s3"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "s3", record.StorageBackend)
	require.Equal(t, "artifacts", record.StorageContainer)
	require.Equal(t, "evidence/run-s3/evidence-run-s3.json", record.StorageKey)
	require.Empty(t, record.LocalCachePath)
	require.Equal(t, RetentionClassEvidence, record.RetentionClass)

	payload, loaded, err := manager.Read(context.Background(), record.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, record.ArtifactID, loaded.ArtifactID)
	require.JSONEq(t, `{"run_id":"run-s3","backend":"s3"}`, string(payload))
}

func TestManagerRunGCDeletesExpiredArtifactsAndSkipsPinned(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	payloadStore, err := newS3PayloadStoreWithClient(Config{
		PayloadS3Bucket: "artifacts",
		PayloadS3Prefix: "gc",
	}, client)
	require.NoError(t, err)

	manager := &Manager{
		metadata: NewMemoryStore(),
		payload:  payloadStore,
		status: Status{
			Enabled:                 true,
			MetadataBackend:         "memory",
			PayloadBackend:          "s3",
			PayloadShared:           true,
			PayloadSharedSurvivable: true,
			PayloadContainer:        "artifacts",
			PayloadPrefix:           "gc",
			AddressingMode:          "stable_keys",
			GCEnabled:               true,
			GCBatchSize:             32,
		},
		logger: zap.NewNop(),
	}

	expired, err := manager.Write(context.Background(), WriteRequest{
		ArtifactID:    "evidence-expired",
		ArtifactType:  ArtifactTypeEvidencePackage,
		OwnerType:     OwnerTypeWorkflowRun,
		OwnerID:       "run-expired",
		RunID:         "run-expired",
		DeleteAfter:   time.Now().UTC().Add(-time.Hour),
		FileExtension: ".json",
		Payload:       []byte(`{"artifact":"expired"}`),
	})
	require.NoError(t, err)

	pinned, err := manager.Write(context.Background(), WriteRequest{
		ArtifactID:    "evidence-pinned",
		ArtifactType:  ArtifactTypeEvidencePackage,
		OwnerType:     OwnerTypeWorkflowRun,
		OwnerID:       "run-pinned",
		RunID:         "run-pinned",
		DeleteAfter:   time.Now().UTC().Add(-time.Hour),
		Pinned:        true,
		FileExtension: ".json",
		Payload:       []byte(`{"artifact":"pinned"}`),
	})
	require.NoError(t, err)

	orphaned, err := manager.Write(context.Background(), WriteRequest{
		ArtifactID:    "history-orphaned",
		ArtifactType:  ArtifactTypeWorkflowMessageIndex,
		OwnerType:     OwnerTypeWorkflowRun,
		OwnerID:       "run-orphaned",
		RunID:         "run-orphaned",
		DeleteAfter:   time.Now().UTC().Add(-time.Hour),
		FileExtension: ".json",
		Payload:       []byte(`{"artifact":"orphaned"}`),
	})
	require.NoError(t, err)
	delete(client.objects, "artifacts/gc/"+orphaned.StorageKey)

	failed, err := manager.Write(context.Background(), WriteRequest{
		ArtifactID:    "memory-delete-failed",
		ArtifactType:  ArtifactTypeIncidentMemoryRecord,
		OwnerType:     OwnerTypeIncidentMemory,
		OwnerID:       "incident-delete-failed",
		RunID:         "run-delete-failed",
		DeleteAfter:   time.Now().UTC().Add(-time.Hour),
		FileExtension: ".json",
		Payload:       []byte(`{"artifact":"delete_failed"}`),
	})
	require.NoError(t, err)
	client.deleteErrs["artifacts/gc/"+failed.StorageKey] = fmt.Errorf("backend delete failed")

	stats, err := manager.RunGC(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, stats.ArtifactsScanned)
	require.Equal(t, 1, stats.ArtifactsDeleted)
	require.Equal(t, 1, stats.OrphanedMetadata)
	require.Equal(t, 1, stats.DeleteFailures)

	expiredLoaded, err := manager.Get(context.Background(), expired.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, GCStateDeleted, expiredLoaded.GCState)

	pinnedLoaded, err := manager.Get(context.Background(), pinned.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, GCStateActive, pinnedLoaded.GCState)

	orphanedLoaded, err := manager.Get(context.Background(), orphaned.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, GCStatePayloadMissing, orphanedLoaded.GCState)

	failedLoaded, err := manager.Get(context.Background(), failed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, GCStateDeleteFailed, failedLoaded.GCState)

	status := manager.Status()
	require.NotEmpty(t, status.GCLastRunAt)
	require.Equal(t, 3, status.GCArtifactsScanned)
	require.Equal(t, 1, status.GCArtifactsDeleted)
	require.Equal(t, 1, status.GCOrphanedMetadata)
	require.Equal(t, 1, status.GCDeleteFailures)
}

func TestManagerReadDispatchesByRecordedBackend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filesystemStore, err := newFilesystemPayloadStore(filepath.Join(root, "payloads"))
	require.NoError(t, err)
	client := newFakeS3Client()
	s3Store, err := newS3PayloadStoreWithClient(Config{
		PayloadS3Bucket: "artifacts",
		PayloadS3Prefix: "dispatch",
	}, client)
	require.NoError(t, err)
	require.NoError(t, client.Put(context.Background(), "artifacts-secondary", "dispatch/messages/run-a/history.json", []byte(`{"backend":"s3"}`)))

	record := &Record{
		ArtifactID:       "history-run-a",
		ArtifactType:     ArtifactTypeWorkflowMessageIndex,
		OwnerType:        OwnerTypeWorkflowRun,
		OwnerID:          "run-a",
		RunID:            "run-a",
		StorageBackend:   "s3",
		StorageContainer: "artifacts-secondary",
		StorageKey:       "messages/run-a/history.json",
		ContentType:      "application/json",
		RetentionClass:   RetentionClassMessageHistory,
		GCState:          GCStateActive,
		CreatedAt:        time.Now().UTC().Add(-time.Minute),
		UpdatedAt:        time.Now().UTC().Add(-time.Minute),
		LastAccessedAt:   time.Now().UTC().Add(-time.Minute),
	}

	manager := &Manager{
		metadata: NewMemoryStore(),
		payload:  filesystemStore,
		stores: map[string]PayloadStore{
			"filesystem": filesystemStore,
			"s3":         s3Store,
		},
		status: Status{
			Enabled:         true,
			MetadataBackend: "memory",
			PayloadBackend:  "filesystem",
			AddressingMode:  "stable_keys",
		},
		logger: zap.NewNop(),
	}
	require.NoError(t, manager.metadata.Upsert(context.Background(), record))

	payload, loaded, err := manager.Read(context.Background(), record.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, "s3", loaded.StorageBackend)
	require.JSONEq(t, `{"backend":"s3"}`, string(payload))
}
