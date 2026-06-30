package artifacts

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type ArtifactType string

const (
	ArtifactTypeEvidencePackage      ArtifactType = "evidence_package"
	ArtifactTypeWorkflowMessage      ArtifactType = "workflow_message"
	ArtifactTypeWorkflowMessageIndex ArtifactType = "workflow_message_history"
	ArtifactTypeIncidentChain        ArtifactType = "incident_artifact_chain"
	ArtifactTypeIncidentMemoryRecord ArtifactType = "incident_memory_record"
	ArtifactTypeRAGIndexMetadata     ArtifactType = "rag_index_metadata"
)

const (
	RetentionClassEvidence       = "evidence_audit"
	RetentionClassMessageHistory = "workflow_history"
	RetentionClassIncidentMemory = "incident_memory"
	RetentionClassRAGMetadata    = "rag_metadata"
	RetentionClassWorkflowAux    = "workflow_aux"
)

const (
	GCStateActive         = "active"
	GCStateDeleted        = "deleted"
	GCStateDeleteFailed   = "delete_failed"
	GCStatePayloadMissing = "payload_missing"
)

type OwnerType string

const (
	OwnerTypeWorkflowRun    OwnerType = "workflow_run"
	OwnerTypeIncidentMemory OwnerType = "incident_memory"
	OwnerTypeRAG            OwnerType = "rag_index"
)

type Config struct {
	MetadataBackend       string
	MetadataPath          string
	MetadataPostgresDSN   string
	PayloadBackend        string
	PayloadRootPath       string
	PayloadShared         bool
	PayloadS3Endpoint     string
	PayloadS3Region       string
	PayloadS3Bucket       string
	PayloadS3Prefix       string
	PayloadS3AccessKey    string
	PayloadS3SecretKey    string
	PayloadS3SessionToken string
	PayloadS3PathStyle    bool
	PayloadS3Insecure     bool
	GCEnabled             bool
	GCInterval            time.Duration
	GCBatchSize           int
}

type Record struct {
	ArtifactID       string            `json:"artifact_id"`
	ArtifactType     ArtifactType      `json:"artifact_type"`
	OwnerType        OwnerType         `json:"owner_type,omitempty"`
	OwnerID          string            `json:"owner_id,omitempty"`
	RunID            string            `json:"run_id,omitempty"`
	CollectorID      string            `json:"collector_id,omitempty"`
	ClusterName      string            `json:"cluster_name,omitempty"`
	StorageBackend   string            `json:"storage_backend"`
	StorageContainer string            `json:"storage_container,omitempty"`
	StorageKey       string            `json:"storage_key"`
	ContentType      string            `json:"content_type,omitempty"`
	ContentEncoding  string            `json:"content_encoding,omitempty"`
	SizeBytes        int64             `json:"size_bytes,omitempty"`
	Checksum         string            `json:"checksum,omitempty"`
	RetentionClass   string            `json:"retention_class,omitempty"`
	ExpiresAt        time.Time         `json:"expires_at,omitempty"`
	DeleteAfter      time.Time         `json:"delete_after,omitempty"`
	Pinned           bool              `json:"pinned,omitempty"`
	GCState          string            `json:"gc_state,omitempty"`
	LastAccessedAt   time.Time         `json:"last_accessed_at,omitempty"`
	LocalCachePath   string            `json:"local_cache_path,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type Filter struct {
	ArtifactID       string
	ArtifactType     ArtifactType
	OwnerType        OwnerType
	OwnerID          string
	RunID            string
	CollectorID      string
	ClusterName      string
	GCEligibleBefore time.Time
	IncludeDeleted   bool
	Limit            int
}

type Status struct {
	Enabled                 bool   `json:"enabled"`
	MetadataBackend         string `json:"metadata_backend"`
	MetadataPath            string `json:"metadata_path,omitempty"`
	MetadataPersistent      bool   `json:"metadata_persistent"`
	MetadataShared          bool   `json:"metadata_shared"`
	PayloadBackend          string `json:"payload_backend"`
	PayloadRootPath         string `json:"payload_root_path,omitempty"`
	PayloadShared           bool   `json:"payload_shared"`
	PayloadSharedSurvivable bool   `json:"payload_shared_survivable"`
	PayloadContainer        string `json:"payload_container,omitempty"`
	PayloadPrefix           string `json:"payload_prefix,omitempty"`
	AddressingMode          string `json:"addressing_mode"`
	LocalCacheActive        bool   `json:"local_cache_active"`
	GCEnabled               bool   `json:"gc_enabled"`
	GCInterval              string `json:"gc_interval,omitempty"`
	GCBatchSize             int    `json:"gc_batch_size,omitempty"`
	GCLastRunAt             string `json:"gc_last_run_at,omitempty"`
	GCArtifactsScanned      int    `json:"gc_artifacts_scanned,omitempty"`
	GCArtifactsDeleted      int    `json:"gc_artifacts_deleted,omitempty"`
	GCDeleteFailures        int    `json:"gc_delete_failures,omitempty"`
	GCOrphanedMetadata      int    `json:"gc_orphaned_metadata,omitempty"`
	LastError               string `json:"last_error,omitempty"`
}

type MetadataStore interface {
	Upsert(context.Context, *Record) error
	Get(context.Context, string) (*Record, error)
	List(context.Context, Filter) ([]*Record, error)
	Close() error
}

type PayloadWriteResult struct {
	LocalCachePath string
	SizeBytes      int64
	Checksum       string
}

type PayloadStore interface {
	Backend() string
	RootPath() string
	Container() string
	SharedSurvivable() bool
	Write(context.Context, string, []byte) (PayloadWriteResult, error)
	Read(context.Context, string) ([]byte, error)
	ReadRecord(context.Context, *Record) ([]byte, error)
	Delete(context.Context, string) error
	DeleteRecord(context.Context, *Record) error
}

type WriteRequest struct {
	ArtifactID      string
	ArtifactType    ArtifactType
	OwnerType       OwnerType
	OwnerID         string
	RunID           string
	CollectorID     string
	ClusterName     string
	StorageKey      string
	FileExtension   string
	ContentType     string
	ContentEncoding string
	RetentionClass  string
	ExpiresAt       time.Time
	DeleteAfter     time.Time
	Pinned          bool
	Metadata        map[string]string
	Payload         []byte
}

type GCStats struct {
	LastRunAt        time.Time `json:"last_run_at,omitempty"`
	ArtifactsScanned int       `json:"artifacts_scanned,omitempty"`
	ArtifactsDeleted int       `json:"artifacts_deleted,omitempty"`
	DeleteFailures   int       `json:"delete_failures,omitempty"`
	OrphanedMetadata int       `json:"orphaned_metadata,omitempty"`
}

var ErrPayloadNotFound = errors.New("artifact payload not found")

func NormalizeConfig(cfg Config) Config {
	switch strings.ToLower(strings.TrimSpace(cfg.MetadataBackend)) {
	case "", "bbolt", "local", "embedded_bbolt":
		cfg.MetadataBackend = "bbolt"
	case "postgres", "postgresql":
		cfg.MetadataBackend = "postgres"
	case "memory", "in_memory":
		cfg.MetadataBackend = "memory"
	default:
		cfg.MetadataBackend = "bbolt"
	}
	switch strings.ToLower(strings.TrimSpace(cfg.PayloadBackend)) {
	case "", "filesystem", "localfs":
		cfg.PayloadBackend = "filesystem"
	case "s3":
		cfg.PayloadBackend = "s3"
	default:
		cfg.PayloadBackend = "filesystem"
	}
	cfg.MetadataPath = strings.TrimSpace(cfg.MetadataPath)
	if cfg.MetadataPath != "" {
		cfg.MetadataPath = filepath.Clean(cfg.MetadataPath)
	}
	cfg.MetadataPostgresDSN = strings.TrimSpace(cfg.MetadataPostgresDSN)
	cfg.PayloadRootPath = strings.TrimSpace(cfg.PayloadRootPath)
	if cfg.PayloadRootPath != "" {
		cfg.PayloadRootPath = filepath.Clean(cfg.PayloadRootPath)
	}
	cfg.PayloadS3Endpoint = strings.TrimSpace(cfg.PayloadS3Endpoint)
	cfg.PayloadS3Region = strings.TrimSpace(cfg.PayloadS3Region)
	cfg.PayloadS3Bucket = strings.TrimSpace(cfg.PayloadS3Bucket)
	cfg.PayloadS3Prefix = strings.Trim(strings.TrimSpace(cfg.PayloadS3Prefix), "/")
	cfg.PayloadS3AccessKey = strings.TrimSpace(cfg.PayloadS3AccessKey)
	cfg.PayloadS3SecretKey = strings.TrimSpace(cfg.PayloadS3SecretKey)
	cfg.PayloadS3SessionToken = strings.TrimSpace(cfg.PayloadS3SessionToken)
	if cfg.GCBatchSize <= 0 {
		cfg.GCBatchSize = 128
	}
	if cfg.GCInterval <= 0 {
		cfg.GCInterval = time.Hour
	}
	return cfg
}

func SharedMetadataBackend(backend string) bool {
	return strings.EqualFold(strings.TrimSpace(backend), "postgres")
}

func MetadataPersistent(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "bbolt", "postgres":
		return true
	default:
		return false
	}
}

func SharedPayloadBackend(backend string) bool {
	return strings.EqualFold(strings.TrimSpace(backend), "s3")
}

func normalizeFilter(filter Filter) Filter {
	filter.ArtifactID = strings.TrimSpace(filter.ArtifactID)
	filter.OwnerID = strings.TrimSpace(filter.OwnerID)
	filter.RunID = strings.TrimSpace(filter.RunID)
	filter.CollectorID = strings.TrimSpace(filter.CollectorID)
	filter.ClusterName = strings.TrimSpace(filter.ClusterName)
	return filter
}

func validateRecord(record *Record) error {
	if record == nil {
		return fmt.Errorf("artifact record is nil")
	}
	if strings.TrimSpace(record.ArtifactID) == "" {
		return fmt.Errorf("artifact id is required")
	}
	if strings.TrimSpace(string(record.ArtifactType)) == "" {
		return fmt.Errorf("artifact type is required")
	}
	if strings.TrimSpace(record.StorageBackend) == "" {
		return fmt.Errorf("storage backend is required")
	}
	if record.GCState == "" {
		record.GCState = GCStateActive
	}
	if strings.TrimSpace(record.StorageKey) == "" {
		return fmt.Errorf("storage key is required")
	}
	return nil
}

func DefaultRetentionForType(artifactType ArtifactType) (string, time.Duration) {
	switch artifactType {
	case ArtifactTypeEvidencePackage:
		return RetentionClassEvidence, 30 * 24 * time.Hour
	case ArtifactTypeWorkflowMessage, ArtifactTypeWorkflowMessageIndex:
		return RetentionClassMessageHistory, 14 * 24 * time.Hour
	case ArtifactTypeIncidentMemoryRecord:
		return RetentionClassIncidentMemory, 90 * 24 * time.Hour
	case ArtifactTypeRAGIndexMetadata:
		return RetentionClassRAGMetadata, 30 * 24 * time.Hour
	default:
		return RetentionClassWorkflowAux, 14 * 24 * time.Hour
	}
}
