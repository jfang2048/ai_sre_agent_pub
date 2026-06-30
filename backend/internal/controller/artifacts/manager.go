package artifacts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	"go.uber.org/zap"
)

type Manager struct {
	metadata MetadataStore
	payload  PayloadStore
	stores   map[string]PayloadStore
	gcCancel context.CancelFunc
	gcDone   chan struct{}
	mu       sync.RWMutex
	status   Status
	logger   *zap.Logger
}

func NewManager(ctx context.Context, cfg Config, logger *zap.Logger) (*Manager, Status, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = NormalizeConfig(cfg)
	status := Status{
		Enabled:            true,
		MetadataBackend:    cfg.MetadataBackend,
		MetadataPath:       cfg.MetadataPath,
		MetadataPersistent: MetadataPersistent(cfg.MetadataBackend),
		MetadataShared:     SharedMetadataBackend(cfg.MetadataBackend),
		PayloadBackend:     cfg.PayloadBackend,
		PayloadRootPath:    cfg.PayloadRootPath,
		PayloadShared:      cfg.PayloadShared,
		AddressingMode:     "stable_keys",
		LocalCacheActive:   cfg.PayloadBackend == "filesystem",
		GCEnabled:          cfg.GCEnabled,
		GCInterval:         cfg.GCInterval.String(),
		GCBatchSize:        cfg.GCBatchSize,
	}
	payload, err := newPayloadStore(cfg)
	if err != nil {
		status.LastError = err.Error()
		return nil, status, err
	}
	status.PayloadShared = cfg.PayloadShared || payload.SharedSurvivable()
	status.PayloadSharedSurvivable = payload.SharedSurvivable()
	status.PayloadContainer = payload.Container()
	status.PayloadPrefix = cfg.PayloadS3Prefix
	var metadata MetadataStore
	switch cfg.MetadataBackend {
	case "postgres":
		metadata, err = NewPostgresStore(ctx, cfg.MetadataPostgresDSN)
	case "memory":
		metadata = NewMemoryStore()
	default:
		metadata, err = NewBoltStore(cfg.MetadataPath)
	}
	if err != nil {
		status.LastError = err.Error()
		return nil, status, err
	}
	status.MetadataPath = metadataPathForStatus(cfg)
	status.PayloadRootPath = payload.RootPath()
	manager := &Manager{
		metadata: metadata,
		payload:  payload,
		stores:   buildPayloadStoreMap(cfg, payload, logger),
		status:   status,
		logger:   logger.With(zap.String("component", "artifact_manager")),
	}
	if cfg.GCEnabled {
		manager.startGC(cfg.GCInterval)
	}
	return manager, status, nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	if m.gcCancel != nil {
		m.gcCancel()
	}
	if m.gcDone != nil {
		<-m.gcDone
	}
	if m.metadata == nil {
		return nil
	}
	return m.metadata.Close()
}

func (m *Manager) Status() Status {
	if m == nil {
		return Status{Enabled: false, MetadataBackend: "disabled", PayloadBackend: "disabled"}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Manager) Write(ctx context.Context, req WriteRequest) (*Record, error) {
	if m == nil || m.metadata == nil || m.payload == nil {
		return nil, fmt.Errorf("artifact manager is not initialized")
	}
	now := time.Now().UTC()
	record := &Record{
		ArtifactID:       firstNonEmpty(strings.TrimSpace(req.ArtifactID), generateArtifactID(req.ArtifactType, req.RunID, req.OwnerID)),
		ArtifactType:     req.ArtifactType,
		OwnerType:        req.OwnerType,
		OwnerID:          strings.TrimSpace(req.OwnerID),
		RunID:            strings.TrimSpace(req.RunID),
		CollectorID:      strings.TrimSpace(req.CollectorID),
		ClusterName:      strings.TrimSpace(req.ClusterName),
		StorageBackend:   m.payload.Backend(),
		StorageContainer: m.payload.Container(),
		StorageKey:       firstNonEmpty(strings.TrimSpace(req.StorageKey), stableStorageKey(req)),
		ContentType:      strings.TrimSpace(req.ContentType),
		ContentEncoding:  strings.TrimSpace(req.ContentEncoding),
		RetentionClass:   strings.TrimSpace(req.RetentionClass),
		ExpiresAt:        req.ExpiresAt.UTC(),
		DeleteAfter:      req.DeleteAfter.UTC(),
		Pinned:           req.Pinned,
		GCState:          GCStateActive,
		LastAccessedAt:   now,
		Metadata:         cloneStringMap(req.Metadata),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	applyDefaultRetention(record)
	if existing, err := m.metadata.Get(ctx, record.ArtifactID); err == nil && existing != nil {
		record.CreatedAt = existing.CreatedAt
	}
	writeResult, err := m.payload.Write(ctx, record.StorageKey, req.Payload)
	if err != nil {
		return nil, err
	}
	record.LocalCachePath = writeResult.LocalCachePath
	record.SizeBytes = writeResult.SizeBytes
	record.Checksum = writeResult.Checksum
	if err := validateRecord(record); err != nil {
		return nil, err
	}
	if err := m.metadata.Upsert(ctx, record); err != nil {
		return nil, err
	}
	return cloneRecord(record), nil
}

func (m *Manager) Get(ctx context.Context, artifactID string) (*Record, error) {
	if m == nil || m.metadata == nil {
		return nil, fmt.Errorf("artifact manager is not initialized")
	}
	return m.metadata.Get(ctx, artifactID)
}

func (m *Manager) List(ctx context.Context, filter Filter) ([]*Record, error) {
	if m == nil || m.metadata == nil {
		return nil, fmt.Errorf("artifact manager is not initialized")
	}
	return m.metadata.List(ctx, filter)
}

func (m *Manager) Read(ctx context.Context, artifactID string) ([]byte, *Record, error) {
	if m == nil {
		return nil, nil, fmt.Errorf("artifact manager is not initialized")
	}
	record, err := m.Get(ctx, artifactID)
	if err != nil {
		return nil, nil, err
	}
	store := m.payloadStoreForBackend(record.StorageBackend)
	if store == nil {
		return nil, record, fmt.Errorf("artifact payload backend %q is unavailable for %s", record.StorageBackend, artifactID)
	}
	payload, err := store.ReadRecord(ctx, record)
	if err != nil {
		return nil, record, fmt.Errorf("artifact payload missing for %s: %w", artifactID, err)
	}
	m.touchRecordAccess(ctx, record)
	return payload, record, nil
}

func (m *Manager) ReadRecord(ctx context.Context, record *Record) ([]byte, error) {
	if m == nil || m.payload == nil {
		return nil, fmt.Errorf("artifact manager is not initialized")
	}
	if record == nil {
		return nil, fmt.Errorf("artifact record is nil")
	}
	store := m.payloadStoreForBackend(record.StorageBackend)
	if store == nil {
		return nil, fmt.Errorf("artifact payload backend %q is unavailable for %s", record.StorageBackend, record.ArtifactID)
	}
	payload, err := store.ReadRecord(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("artifact payload missing for %s: %w", record.ArtifactID, err)
	}
	m.touchRecordAccess(ctx, record)
	return payload, nil
}

func (m *Manager) RunGC(ctx context.Context) (GCStats, error) {
	if m == nil || m.metadata == nil || m.payload == nil {
		return GCStats{}, fmt.Errorf("artifact manager is not initialized")
	}
	status := m.Status()
	if !status.GCEnabled {
		return GCStats{}, nil
	}
	now := time.Now().UTC()
	records, err := m.metadata.List(ctx, Filter{
		GCEligibleBefore: now,
		Limit:            maxInt(1, status.GCBatchSize),
	})
	if err != nil {
		return GCStats{}, err
	}
	stats := GCStats{LastRunAt: now, ArtifactsScanned: len(records)}
	for _, record := range records {
		if record == nil {
			continue
		}
		record.UpdatedAt = now
		store := m.payloadStoreForBackend(record.StorageBackend)
		if store == nil {
			record.GCState = GCStateDeleteFailed
			stats.DeleteFailures++
			if record.Metadata == nil {
				record.Metadata = map[string]string{}
			}
			record.Metadata["gc_last_error"] = truncateGCError(fmt.Errorf("payload backend %q is unavailable", record.StorageBackend))
			if upsertErr := m.metadata.Upsert(ctx, record); upsertErr != nil && err == nil {
				err = upsertErr
			}
			continue
		}
		switch err := store.DeleteRecord(ctx, record); {
		case err == nil:
			record.GCState = GCStateDeleted
			record.LocalCachePath = ""
			stats.ArtifactsDeleted++
		case errors.Is(err, ErrPayloadNotFound):
			record.GCState = GCStatePayloadMissing
			stats.OrphanedMetadata++
		default:
			record.GCState = GCStateDeleteFailed
			stats.DeleteFailures++
			if record.Metadata == nil {
				record.Metadata = map[string]string{}
			}
			record.Metadata["gc_last_error"] = truncateGCError(err)
		}
		if upsertErr := m.metadata.Upsert(ctx, record); upsertErr != nil && err == nil {
			err = upsertErr
		}
	}
	m.updateGCStatus(stats)
	return stats, err
}

type filesystemPayloadStore struct {
	rootPath string
}

func newFilesystemPayloadStore(root string) (*filesystemPayloadStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("artifact payload root path is empty")
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &filesystemPayloadStore{rootPath: root}, nil
}

func (s *filesystemPayloadStore) Backend() string        { return "filesystem" }
func (s *filesystemPayloadStore) RootPath() string       { return s.rootPath }
func (s *filesystemPayloadStore) Container() string      { return "" }
func (s *filesystemPayloadStore) SharedSurvivable() bool { return false }

func (s *filesystemPayloadStore) Write(_ context.Context, key string, payload []byte) (PayloadWriteResult, error) {
	key = normalizeStorageKey(key)
	target := filepath.Join(s.rootPath, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return PayloadWriteResult{}, err
	}
	if err := os.WriteFile(target, payload, 0o644); err != nil {
		return PayloadWriteResult{}, err
	}
	sum := sha256.Sum256(payload)
	return PayloadWriteResult{
		LocalCachePath: target,
		SizeBytes:      int64(len(payload)),
		Checksum:       hex.EncodeToString(sum[:]),
	}, nil
}

func (s *filesystemPayloadStore) Read(_ context.Context, key string) ([]byte, error) {
	key = normalizeStorageKey(key)
	payload, err := os.ReadFile(filepath.Join(s.rootPath, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrPayloadNotFound
	}
	return payload, err
}

func (s *filesystemPayloadStore) ReadRecord(ctx context.Context, record *Record) ([]byte, error) {
	if record == nil {
		return nil, fmt.Errorf("artifact record is nil")
	}
	return s.Read(ctx, record.StorageKey)
}

func (s *filesystemPayloadStore) Delete(_ context.Context, key string) error {
	key = normalizeStorageKey(key)
	err := os.Remove(filepath.Join(s.rootPath, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return ErrPayloadNotFound
	}
	return err
}

func (s *filesystemPayloadStore) DeleteRecord(ctx context.Context, record *Record) error {
	if record == nil {
		return fmt.Errorf("artifact record is nil")
	}
	return s.Delete(ctx, record.StorageKey)
}

func metadataPathForStatus(cfg Config) string {
	switch cfg.MetadataBackend {
	case "postgres":
		return ""
	default:
		return cfg.MetadataPath
	}
}

func newPayloadStore(cfg Config) (PayloadStore, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.PayloadBackend)) {
	case "s3":
		return newS3PayloadStore(cfg)
	default:
		return newFilesystemPayloadStore(cfg.PayloadRootPath)
	}
}

func buildPayloadStoreMap(cfg Config, primary PayloadStore, logger *zap.Logger) map[string]PayloadStore {
	stores := map[string]PayloadStore{}
	if primary != nil {
		stores[primary.Backend()] = primary
	}
	if primary == nil || primary.Backend() != "filesystem" {
		if fs, err := newFilesystemPayloadStore(cfg.PayloadRootPath); err == nil {
			stores[fs.Backend()] = fs
		} else if logger != nil && strings.TrimSpace(cfg.PayloadRootPath) != "" {
			logger.Debug("artifact filesystem payload fallback is unavailable", zap.Error(err))
		}
	}
	if primary == nil || primary.Backend() != "s3" {
		if strings.TrimSpace(cfg.PayloadS3Endpoint) != "" && strings.TrimSpace(cfg.PayloadS3Bucket) != "" &&
			strings.TrimSpace(cfg.PayloadS3AccessKey) != "" && strings.TrimSpace(cfg.PayloadS3SecretKey) != "" {
			if s3, err := newS3PayloadStore(cfg); err == nil {
				stores[s3.Backend()] = s3
			} else if logger != nil {
				logger.Debug("artifact s3 payload fallback is unavailable", zap.Error(err))
			}
		}
	}
	return stores
}

func normalizeStorageKey(key string) string {
	key = strings.ReplaceAll(strings.TrimSpace(key), "\\", "/")
	key = strings.TrimPrefix(key, "/")
	return filepath.Clean(key)
}

func stableStorageKey(req WriteRequest) string {
	ext := strings.TrimSpace(req.FileExtension)
	if ext == "" {
		ext = ".json"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	id := sanitizeID(firstNonEmpty(req.ArtifactID, generateArtifactID(req.ArtifactType, req.RunID, req.OwnerID)))
	switch req.ArtifactType {
	case ArtifactTypeEvidencePackage:
		return filepath.ToSlash(filepath.Join("evidence", firstNonEmpty(req.RunID, req.OwnerID), id+ext))
	case ArtifactTypeWorkflowMessage, ArtifactTypeWorkflowMessageIndex:
		return filepath.ToSlash(filepath.Join("messages", firstNonEmpty(req.RunID, req.OwnerID), id+ext))
	case ArtifactTypeIncidentChain:
		return filepath.ToSlash(filepath.Join("incident_chain", firstNonEmpty(req.RunID, req.OwnerID), id+ext))
	case ArtifactTypeIncidentMemoryRecord:
		return filepath.ToSlash(filepath.Join("incident_memory", id+ext))
	case ArtifactTypeRAGIndexMetadata:
		return filepath.ToSlash(filepath.Join("rag", firstNonEmpty(req.OwnerID, req.RunID, "default"), id+ext))
	default:
		return filepath.ToSlash(filepath.Join("artifacts", id+ext))
	}
}

func generateArtifactID(artifactType ArtifactType, runID, ownerID string) string {
	base := sanitizeID(firstNonEmpty(ownerID, runID, string(artifactType)))
	if base == "" {
		base = "artifact"
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UTC().UnixNano())
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizeID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-', r == '_', r == '.', r == '/', r == ' ':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneRecord(record *Record) *Record {
	if record == nil {
		return nil
	}
	copy := *record
	copy.Metadata = cloneStringMap(record.Metadata)
	return &copy
}

func applyDefaultRetention(record *Record) {
	if record == nil {
		return
	}
	class, ttl := DefaultRetentionForType(record.ArtifactType)
	if strings.TrimSpace(record.RetentionClass) == "" {
		record.RetentionClass = class
	}
	if record.ExpiresAt.IsZero() {
		record.ExpiresAt = record.CreatedAt.Add(ttl)
	}
	if record.DeleteAfter.IsZero() {
		record.DeleteAfter = record.ExpiresAt
	}
}

func (m *Manager) touchRecordAccess(ctx context.Context, record *Record) {
	if m == nil || m.metadata == nil || record == nil {
		return
	}
	record = cloneRecord(record)
	record.LastAccessedAt = time.Now().UTC()
	record.UpdatedAt = record.LastAccessedAt
	if err := m.metadata.Upsert(ctx, record); err != nil {
		m.logger.Debug("failed to update artifact last_accessed_at", zap.String("artifact_id", record.ArtifactID), zap.Error(err))
	}
}

func (m *Manager) startGC(interval time.Duration) {
	if m == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.gcCancel = cancel
	m.gcDone = make(chan struct{})
	go func() {
		defer close(m.gcDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := m.RunGC(context.Background()); err != nil {
					m.logger.Warn("artifact garbage collection failed", zap.Error(err))
				}
			}
		}
	}()
}

func (m *Manager) updateGCStatus(stats GCStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.GCLastRunAt = stats.LastRunAt.Format(time.RFC3339Nano)
	m.status.GCArtifactsScanned = stats.ArtifactsScanned
	m.status.GCArtifactsDeleted = stats.ArtifactsDeleted
	m.status.GCDeleteFailures = stats.DeleteFailures
	m.status.GCOrphanedMetadata = stats.OrphanedMetadata
}

func (m *Manager) payloadStoreForBackend(backend string) PayloadStore {
	if m == nil {
		return nil
	}
	if store := m.stores[strings.ToLower(strings.TrimSpace(backend))]; store != nil {
		return store
	}
	if m.payload != nil && strings.EqualFold(m.payload.Backend(), backend) {
		return m.payload
	}
	return nil
}

func truncateGCError(err error) string {
	if err == nil {
		return ""
	}
	const maxLen = 256
	msg := strings.TrimSpace(err.Error())
	if len(msg) <= maxLen {
		return msg
	}
	return msg[:maxLen]
}

type MemoryStore struct {
	items map[string]*Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]*Record)}
}

func (s *MemoryStore) Upsert(_ context.Context, record *Record) error {
	if err := validateRecord(record); err != nil {
		return err
	}
	s.items[record.ArtifactID] = cloneRecord(record)
	return nil
}

func (s *MemoryStore) Get(_ context.Context, artifactID string) (*Record, error) {
	record, ok := s.items[strings.TrimSpace(artifactID)]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return cloneRecord(record), nil
}

func (s *MemoryStore) List(_ context.Context, filter Filter) ([]*Record, error) {
	filter = normalizeFilter(filter)
	out := make([]*Record, 0, len(s.items))
	for _, record := range s.items {
		if !recordMatchesFilter(record, filter) {
			continue
		}
		out = append(out, cloneRecord(record))
	}
	sortRecords(out, filter.Limit)
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) Close() error { return nil }

type BoltStore struct {
	db   *bolt.DB
	path string
}

func NewBoltStore(path string) (*BoltStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("artifact metadata path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, bucketErr := tx.CreateBucketIfNotExists([]byte("artifacts"))
		return bucketErr
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &BoltStore{db: db, path: path}, nil
}

func (s *BoltStore) Upsert(_ context.Context, record *Record) error {
	if err := validateRecord(record); err != nil {
		return err
	}
	payload, err := jsonMarshal(record)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("artifacts")).Put([]byte(record.ArtifactID), payload)
	})
}

func (s *BoltStore) Get(_ context.Context, artifactID string) (*Record, error) {
	var record Record
	err := s.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket([]byte("artifacts")).Get([]byte(strings.TrimSpace(artifactID)))
		if value == nil {
			return sql.ErrNoRows
		}
		return jsonUnmarshal(value, &record)
	})
	if err != nil {
		return nil, err
	}
	return cloneRecord(&record), nil
}

func (s *BoltStore) List(_ context.Context, filter Filter) ([]*Record, error) {
	filter = normalizeFilter(filter)
	out := make([]*Record, 0, 32)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("artifacts")).ForEach(func(_, value []byte) error {
			var record Record
			if err := jsonUnmarshal(value, &record); err != nil {
				return nil
			}
			if !recordMatchesFilter(&record, filter) {
				return nil
			}
			out = append(out, &record)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sortRecords(out, filter.Limit)
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *BoltStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func recordMatchesFilter(record *Record, filter Filter) bool {
	if record == nil {
		return false
	}
	if filter.ArtifactID != "" && record.ArtifactID != filter.ArtifactID {
		return false
	}
	if filter.ArtifactType != "" && record.ArtifactType != filter.ArtifactType {
		return false
	}
	if filter.OwnerType != "" && record.OwnerType != filter.OwnerType {
		return false
	}
	if filter.OwnerID != "" && record.OwnerID != filter.OwnerID {
		return false
	}
	if filter.RunID != "" && record.RunID != filter.RunID {
		return false
	}
	if filter.CollectorID != "" && record.CollectorID != filter.CollectorID {
		return false
	}
	if filter.ClusterName != "" && record.ClusterName != filter.ClusterName {
		return false
	}
	if !filter.IncludeDeleted && record.GCState == GCStateDeleted {
		return false
	}
	if !filter.GCEligibleBefore.IsZero() {
		if record.Pinned {
			return false
		}
		if record.GCState == GCStateDeleted {
			return false
		}
		expired := (!record.DeleteAfter.IsZero() && !record.DeleteAfter.After(filter.GCEligibleBefore)) ||
			(!record.ExpiresAt.IsZero() && !record.ExpiresAt.After(filter.GCEligibleBefore))
		if !expired {
			return false
		}
	}
	return true
}

func sortRecords(records []*Record, limit int) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].UpdatedAt.Equal(records[j].UpdatedAt) {
			return records[i].ArtifactID < records[j].ArtifactID
		}
		return records[i].UpdatedAt.After(records[j].UpdatedAt)
	})
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
