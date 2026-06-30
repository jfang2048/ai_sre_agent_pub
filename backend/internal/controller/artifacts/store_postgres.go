package artifacts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("artifact metadata postgres dsn is empty")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	store := &PostgresStore{db: db}
	if err := store.initSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) initSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workflow_artifacts (
			artifact_id TEXT PRIMARY KEY,
			artifact_type TEXT NOT NULL,
			owner_type TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			run_id TEXT NOT NULL DEFAULT '',
			collector_id TEXT NOT NULL DEFAULT '',
			cluster_name TEXT NOT NULL DEFAULT '',
			storage_backend TEXT NOT NULL,
			storage_container TEXT NOT NULL DEFAULT '',
			storage_key TEXT NOT NULL,
			content_type TEXT NOT NULL DEFAULT '',
			content_encoding TEXT NOT NULL DEFAULT '',
			size_bytes BIGINT NOT NULL DEFAULT 0,
			checksum TEXT NOT NULL DEFAULT '',
			retention_class TEXT NOT NULL DEFAULT '',
			expires_at TIMESTAMPTZ NULL,
			delete_after TIMESTAMPTZ NULL,
			pinned BOOLEAN NOT NULL DEFAULT FALSE,
			gc_state TEXT NOT NULL DEFAULT 'active',
			last_accessed_at TIMESTAMPTZ NULL,
			local_cache_path TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb
		)`,
		`ALTER TABLE workflow_artifacts ADD COLUMN IF NOT EXISTS storage_container TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE workflow_artifacts ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NULL`,
		`ALTER TABLE workflow_artifacts ADD COLUMN IF NOT EXISTS delete_after TIMESTAMPTZ NULL`,
		`ALTER TABLE workflow_artifacts ADD COLUMN IF NOT EXISTS pinned BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE workflow_artifacts ADD COLUMN IF NOT EXISTS gc_state TEXT NOT NULL DEFAULT 'active'`,
		`ALTER TABLE workflow_artifacts ADD COLUMN IF NOT EXISTS last_accessed_at TIMESTAMPTZ NULL`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_artifacts_run
			ON workflow_artifacts (run_id, artifact_type, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_artifacts_owner
			ON workflow_artifacts (owner_type, owner_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_artifacts_type
			ON workflow_artifacts (artifact_type, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_artifacts_gc
			ON workflow_artifacts (gc_state, pinned, delete_after, expires_at)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) Upsert(ctx context.Context, record *Record) error {
	if err := validateRecord(record); err != nil {
		return err
	}
	metadata, err := json.Marshal(cloneStringMap(record.Metadata))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO workflow_artifacts (
			artifact_id, artifact_type, owner_type, owner_id, run_id, collector_id, cluster_name,
			storage_backend, storage_container, storage_key, content_type, content_encoding, size_bytes, checksum,
			retention_class, expires_at, delete_after, pinned, gc_state, last_accessed_at,
			local_cache_path, created_at, updated_at, metadata
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (artifact_id) DO UPDATE SET
			artifact_type = EXCLUDED.artifact_type,
			owner_type = EXCLUDED.owner_type,
			owner_id = EXCLUDED.owner_id,
			run_id = EXCLUDED.run_id,
			collector_id = EXCLUDED.collector_id,
			cluster_name = EXCLUDED.cluster_name,
			storage_backend = EXCLUDED.storage_backend,
			storage_container = EXCLUDED.storage_container,
			storage_key = EXCLUDED.storage_key,
			content_type = EXCLUDED.content_type,
			content_encoding = EXCLUDED.content_encoding,
			size_bytes = EXCLUDED.size_bytes,
			checksum = EXCLUDED.checksum,
			retention_class = EXCLUDED.retention_class,
			expires_at = EXCLUDED.expires_at,
			delete_after = EXCLUDED.delete_after,
			pinned = EXCLUDED.pinned,
			gc_state = EXCLUDED.gc_state,
			last_accessed_at = EXCLUDED.last_accessed_at,
			local_cache_path = EXCLUDED.local_cache_path,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at,
			metadata = EXCLUDED.metadata
	`, record.ArtifactID, string(record.ArtifactType), string(record.OwnerType), record.OwnerID, record.RunID, record.CollectorID, record.ClusterName, record.StorageBackend, record.StorageContainer, record.StorageKey, record.ContentType, record.ContentEncoding, record.SizeBytes, record.Checksum, record.RetentionClass, nullableTime(record.ExpiresAt), nullableTime(record.DeleteAfter), record.Pinned, firstNonEmpty(record.GCState, GCStateActive), nullableTime(record.LastAccessedAt), record.LocalCachePath, record.CreatedAt, record.UpdatedAt, metadata)
	return err
}

func (s *PostgresStore) Get(ctx context.Context, artifactID string) (*Record, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT artifact_id, artifact_type, owner_type, owner_id, run_id, collector_id, cluster_name,
		       storage_backend, storage_container, storage_key, content_type, content_encoding, size_bytes, checksum,
		       retention_class, expires_at, delete_after, pinned, gc_state, last_accessed_at,
		       local_cache_path, created_at, updated_at, metadata
		  FROM workflow_artifacts
		 WHERE artifact_id = $1
	`, strings.TrimSpace(artifactID))
	return scanArtifactRow(row)
}

func (s *PostgresStore) List(ctx context.Context, filter Filter) ([]*Record, error) {
	filter = normalizeFilter(filter)
	query := `SELECT artifact_id, artifact_type, owner_type, owner_id, run_id, collector_id, cluster_name, storage_backend, storage_container, storage_key, content_type, content_encoding, size_bytes, checksum, retention_class, expires_at, delete_after, pinned, gc_state, last_accessed_at, local_cache_path, created_at, updated_at, metadata FROM workflow_artifacts`
	args := make([]any, 0, 6)
	where := make([]string, 0, 6)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.ArtifactID != "" {
		add("artifact_id = $%d", filter.ArtifactID)
	}
	if filter.ArtifactType != "" {
		add("artifact_type = $%d", string(filter.ArtifactType))
	}
	if filter.OwnerType != "" {
		add("owner_type = $%d", string(filter.OwnerType))
	}
	if filter.OwnerID != "" {
		add("owner_id = $%d", filter.OwnerID)
	}
	if filter.RunID != "" {
		add("run_id = $%d", filter.RunID)
	}
	if filter.CollectorID != "" {
		add("collector_id = $%d", filter.CollectorID)
	}
	if filter.ClusterName != "" {
		add("cluster_name = $%d", filter.ClusterName)
	}
	if !filter.IncludeDeleted {
		where = append(where, "gc_state <> 'deleted'")
	}
	if !filter.GCEligibleBefore.IsZero() {
		args = append(args, filter.GCEligibleBefore)
		idx := len(args)
		where = append(where, fmt.Sprintf("pinned = FALSE AND gc_state <> 'deleted' AND ((delete_after IS NOT NULL AND delete_after <= $%d) OR (expires_at IS NOT NULL AND expires_at <= $%d))", idx, idx))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Record, 0, maxInt(1, filter.Limit))
	for rows.Next() {
		record, err := scanArtifactRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *PostgresStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

type artifactScanner interface {
	Scan(...any) error
}

func scanArtifactRow(scanner artifactScanner) (*Record, error) {
	record, err := scanArtifact(scanner)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, err
	}
	return record, nil
}

func scanArtifactRows(scanner artifactScanner) (*Record, error) {
	return scanArtifactRow(scanner)
}

func scanArtifact(scanner artifactScanner) (*Record, error) {
	var (
		record       Record
		metaJSON     []byte
		typ          string
		ownerTyp     string
		expiresAt    sql.NullTime
		deleteAfter  sql.NullTime
		lastAccessed sql.NullTime
	)
	if err := scanner.Scan(&record.ArtifactID, &typ, &ownerTyp, &record.OwnerID, &record.RunID, &record.CollectorID, &record.ClusterName, &record.StorageBackend, &record.StorageContainer, &record.StorageKey, &record.ContentType, &record.ContentEncoding, &record.SizeBytes, &record.Checksum, &record.RetentionClass, &expiresAt, &deleteAfter, &record.Pinned, &record.GCState, &lastAccessed, &record.LocalCachePath, &record.CreatedAt, &record.UpdatedAt, &metaJSON); err != nil {
		return nil, err
	}
	record.ArtifactType = ArtifactType(typ)
	record.OwnerType = OwnerType(ownerTyp)
	if expiresAt.Valid {
		record.ExpiresAt = expiresAt.Time.UTC()
	}
	if deleteAfter.Valid {
		record.DeleteAfter = deleteAfter.Time.UTC()
	}
	if lastAccessed.Valid {
		record.LastAccessedAt = lastAccessed.Time.UTC()
	}
	if len(metaJSON) > 0 {
		if err := json.Unmarshal(metaJSON, &record.Metadata); err != nil {
			return nil, err
		}
	}
	return &record, nil
}

func nullableTime(ts time.Time) any {
	if ts.IsZero() {
		return nil
	}
	return ts.UTC()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
