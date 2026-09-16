package ingest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

const (
	InboxBackendBbolt    = "bbolt"
	InboxBackendPostgres = "postgres"
	InboxBackendMemory   = "memory"

	defaultInboxPath       = "./data/controller/ingest/inbox.db"
	defaultInboxRetention  = 24 * time.Hour
	defaultInboxGCInterval = 5 * time.Minute
	defaultInboxMaxRecords = 100_000
	defaultInboxMaxBytes   = int64(256 << 20)
	maxInboxPayloadBytes   = 4 << 20
	defaultInboxLease      = 30 * time.Second
)

// ReceiptState is the durable application checkpoint for one accepted batch.
type ReceiptState string

const (
	ReceiptCommitted ReceiptState = "committed"
	ReceiptApplying  ReceiptState = "applying"
	ReceiptApplied   ReceiptState = "applied"
)

var (
	ErrReceiptNotFound  = errors.New("ingest receipt not found")
	ErrLeaseLost        = errors.New("ingest receipt lease lost")
	ErrInboxFull        = errors.New("ingest inbox reached its configured record bound")
	ErrIdentityConflict = errors.New("ingest identity was reused with a different payload")
)

// InboxConfig controls the raw ingest journal. PostgreSQL DSNs are environment
// only so a public configuration file cannot accidentally publish credentials.
type InboxConfig struct {
	Backend      string        `yaml:"backend" json:"backend"`
	Path         string        `yaml:"path" json:"path"`
	PostgresDSN  string        `yaml:"-" json:"-"`
	Retention    time.Duration `yaml:"retention" json:"retention"`
	GCInterval   time.Duration `yaml:"gc_interval" json:"gc_interval"`
	MaxRecords   int           `yaml:"max_records" json:"max_records"`
	MaxBytes     int64         `yaml:"max_bytes" json:"max_bytes"`
	Lease        time.Duration `yaml:"lease" json:"lease"`
	SingleWriter bool          `yaml:"single_writer" json:"single_writer"`
}

// DefaultInboxConfig returns durable, bounded single-node defaults.
func DefaultInboxConfig() InboxConfig {
	return InboxConfig{
		Backend:      InboxBackendBbolt,
		Path:         defaultInboxPath,
		Retention:    defaultInboxRetention,
		GCInterval:   defaultInboxGCInterval,
		MaxRecords:   defaultInboxMaxRecords,
		MaxBytes:     defaultInboxMaxBytes,
		Lease:        defaultInboxLease,
		SingleWriter: false,
	}
}

func (cfg InboxConfig) normalized() InboxConfig {
	defaults := DefaultInboxConfig()
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	if cfg.Backend == "" {
		cfg.Backend = defaults.Backend
	}
	if strings.TrimSpace(cfg.Path) == "" {
		cfg.Path = defaults.Path
	}
	if cfg.Retention <= 0 {
		cfg.Retention = defaults.Retention
	}
	if cfg.GCInterval <= 0 {
		cfg.GCInterval = defaults.GCInterval
	}
	if cfg.MaxRecords <= 0 {
		cfg.MaxRecords = defaults.MaxRecords
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaults.MaxBytes
	}
	if cfg.Lease <= 0 {
		cfg.Lease = defaults.Lease
	}
	return cfg
}

// Receipt is the raw durable ownership record used for replay and dedupe.
type Receipt struct {
	SchemaVersion    uint8        `json:"schema_version"`
	Identity         string       `json:"identity"`
	IdentityKind     string       `json:"identity_kind"`
	CollectorID      string       `json:"collector_id"`
	ProducerEpoch    string       `json:"producer_epoch,omitempty"`
	ProducerSequence uint64       `json:"producer_sequence,omitempty"`
	BatchID          string       `json:"batch_id"`
	AcceptedAt       time.Time    `json:"accepted_at"`
	Payload          []byte       `json:"payload"`
	State            ReceiptState `json:"state"`
	Revision         uint64       `json:"revision"`
	ApplyOwner       string       `json:"apply_owner,omitempty"`
	LeaseExpiresAt   time.Time    `json:"lease_expires_at,omitempty"`
	AppliedAt        time.Time    `json:"applied_at,omitempty"`
	Attempts         uint64       `json:"attempts"`
	LastError        string       `json:"last_error,omitempty"`
}

// InboxStats summarizes durable receipt retention and replay state.
type InboxStats struct {
	Enabled       bool      `json:"enabled"`
	Backend       string    `json:"backend"`
	Shared        bool      `json:"shared"`
	Path          string    `json:"path,omitempty"`
	Records       int64     `json:"records"`
	Pending       int64     `json:"pending"`
	Applied       int64     `json:"applied"`
	Duplicates    uint64    `json:"duplicates"`
	Replayed      uint64    `json:"replayed"`
	Pruned        uint64    `json:"pruned"`
	LastGCAt      time.Time `json:"last_gc_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	Retention     string    `json:"retention"`
	MaxRecords    int       `json:"max_records"`
	PayloadBytes  int64     `json:"payload_bytes"`
	MaxBytes      int64     `json:"max_bytes"`
	LeaseDuration string    `json:"lease_duration"`
}

// Inbox is the narrow durability boundary for raw ingest ownership.
type Inbox interface {
	Commit(context.Context, Receipt) (stored Receipt, created bool, err error)
	Get(context.Context, string) (Receipt, error)
	Claim(context.Context, string, string, time.Time, time.Duration) (Receipt, bool, error)
	Renew(context.Context, string, string, time.Time, time.Duration) error
	MarkApplied(context.Context, string, string, time.Time) error
	MarkFailed(context.Context, string, string, error) error
	Pending(context.Context, time.Time, int) ([]Receipt, error)
	Retained(context.Context, string, int) ([]Receipt, error)
	Prune(context.Context, time.Time, int) (int, error)
	Stats(context.Context) InboxStats
	Close() error
}

// OpenInbox opens the configured durable journal. It never silently falls back.
func OpenInbox(ctx context.Context, cfg InboxConfig, logger *zap.Logger) (Inbox, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg = cfg.normalized()
	switch cfg.Backend {
	case InboxBackendBbolt:
		return newBoltInbox(cfg, logger)
	case InboxBackendPostgres:
		return NewPostgresInbox(ctx, cfg, logger)
	case InboxBackendMemory:
		return newMemoryInbox(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported ingest inbox backend %q", cfg.Backend)
	}
}

func receiptFromBatch(batch *telemetryv1.TelemetryBatch, acceptedAt time.Time) (Receipt, error) {
	if batch == nil || batch.GetCollector() == nil {
		return Receipt{}, fmt.Errorf("batch and collector are required")
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(batch)
	if err != nil {
		return Receipt{}, fmt.Errorf("marshal ingest receipt: %w", err)
	}
	identity, kind := batchDedupeIdentity(batch)
	return Receipt{
		SchemaVersion:    1,
		Identity:         identity,
		IdentityKind:     kind,
		CollectorID:      strings.TrimSpace(batch.GetCollector().GetCollectorId()),
		ProducerEpoch:    strings.TrimSpace(batch.GetProducerEpoch()),
		ProducerSequence: batch.GetProducerSequence(),
		BatchID:          strings.TrimSpace(batch.GetBatchId()),
		AcceptedAt:       acceptedAt.UTC().Truncate(time.Microsecond),
		Payload:          payload,
		State:            ReceiptCommitted,
		Revision:         1,
	}, nil
}

func batchDedupeIdentity(batch *telemetryv1.TelemetryBatch) (string, string) {
	collectorID := ""
	if batch != nil && batch.GetCollector() != nil {
		collectorID = strings.TrimSpace(batch.GetCollector().GetCollectorId())
	}
	hash := sha256.New()
	writeIdentityField(hash, collectorID)
	if batch != nil && strings.TrimSpace(batch.GetProducerEpoch()) != "" && batch.GetProducerSequence() > 0 {
		writeIdentityField(hash, strings.TrimSpace(batch.GetProducerEpoch()))
		var sequence [8]byte
		binary.BigEndian.PutUint64(sequence[:], batch.GetProducerSequence())
		_, _ = hash.Write(sequence[:])
		return "producer:" + hex.EncodeToString(hash.Sum(nil)), "producer"
	}
	if batch != nil {
		writeIdentityField(hash, strings.TrimSpace(batch.GetBatchId()))
	}
	return "batch:" + hex.EncodeToString(hash.Sum(nil)), "batch"
}

type identityWriter interface {
	Write([]byte) (int, error)
}

func writeIdentityField(writer identityWriter, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value))) // #nosec G115 -- validated identifiers are bounded.
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func decodeReceiptBatch(receipt Receipt) (*telemetryv1.TelemetryBatch, error) {
	var batch telemetryv1.TelemetryBatch
	if err := proto.Unmarshal(receipt.Payload, &batch); err != nil {
		return nil, fmt.Errorf("decode durable ingest receipt %s: %w", receipt.Identity, err)
	}
	return &batch, nil
}

func newInboxOwner() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

// memoryInbox exists for isolated tests and explicit local development only.
type memoryInbox struct {
	mu           sync.Mutex
	cfg          InboxConfig
	receipts     map[string]Receipt
	duplicates   uint64
	replayed     uint64
	pruned       uint64
	lastGC       time.Time
	lastError    string
	payloadBytes int64
}

func newMemoryInbox(cfg InboxConfig) *memoryInbox {
	cfg = cfg.normalized()
	return &memoryInbox{cfg: cfg, receipts: make(map[string]Receipt)}
}

func (m *memoryInbox) Commit(_ context.Context, receipt Receipt) (Receipt, bool, error) {
	if err := validateReceipt(receipt); err != nil {
		return Receipt{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.receipts[receipt.Identity]; ok {
		if !bytes.Equal(existing.Payload, receipt.Payload) {
			return Receipt{}, false, ErrIdentityConflict
		}
		m.duplicates++
		return cloneReceipt(existing), false, nil
	}
	if len(m.receipts) >= m.cfg.MaxRecords || m.payloadBytes+int64(len(receipt.Payload)) > m.cfg.MaxBytes {
		return Receipt{}, false, ErrInboxFull
	}
	m.receipts[receipt.Identity] = cloneReceipt(receipt)
	m.payloadBytes += int64(len(receipt.Payload))
	return cloneReceipt(receipt), true, nil
}

func (m *memoryInbox) Get(_ context.Context, identity string) (Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.receipts[identity]
	if !ok {
		return Receipt{}, ErrReceiptNotFound
	}
	return cloneReceipt(receipt), nil
}

func (m *memoryInbox) Claim(_ context.Context, identity, owner string, now time.Time, lease time.Duration) (Receipt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.receipts[identity]
	if !ok {
		return Receipt{}, false, ErrReceiptNotFound
	}
	if receipt.State == ReceiptApplied || (receipt.State == ReceiptApplying && receipt.LeaseExpiresAt.After(now)) {
		return cloneReceipt(receipt), false, nil
	}
	receipt.State = ReceiptApplying
	receipt.ApplyOwner = owner
	receipt.LeaseExpiresAt = now.Add(lease)
	receipt.Attempts++
	receipt.Revision++
	m.receipts[identity] = receipt
	return cloneReceipt(receipt), true, nil
}

func (m *memoryInbox) Renew(_ context.Context, identity, owner string, now time.Time, lease time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.receipts[identity]
	if !ok {
		return ErrReceiptNotFound
	}
	if receipt.State != ReceiptApplying || receipt.ApplyOwner != owner {
		return ErrLeaseLost
	}
	receipt.LeaseExpiresAt = now.Add(lease)
	m.receipts[identity] = receipt
	return nil
}

func (m *memoryInbox) MarkApplied(_ context.Context, identity, owner string, appliedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.receipts[identity]
	if !ok {
		return ErrReceiptNotFound
	}
	if receipt.State != ReceiptApplying || receipt.ApplyOwner != owner {
		return ErrLeaseLost
	}
	receipt.State = ReceiptApplied
	receipt.AppliedAt = appliedAt.UTC()
	receipt.ApplyOwner = ""
	receipt.LeaseExpiresAt = time.Time{}
	receipt.LastError = ""
	receipt.Revision++
	m.receipts[identity] = receipt
	return nil
}

func (m *memoryInbox) MarkFailed(_ context.Context, identity, owner string, failure error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.receipts[identity]
	if !ok {
		return ErrReceiptNotFound
	}
	if receipt.State != ReceiptApplying || receipt.ApplyOwner != owner {
		return ErrLeaseLost
	}
	receipt.State = ReceiptCommitted
	receipt.ApplyOwner = ""
	receipt.LeaseExpiresAt = time.Time{}
	if failure != nil {
		receipt.LastError = failure.Error()
	}
	receipt.Revision++
	m.receipts[identity] = receipt
	return nil
}

func (m *memoryInbox) Pending(_ context.Context, now time.Time, limit int) ([]Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 256
	}
	result := make([]Receipt, 0, limit)
	for _, receipt := range m.receipts {
		if receipt.State == ReceiptCommitted || (receipt.State == ReceiptApplying && !receipt.LeaseExpiresAt.After(now)) {
			result = append(result, cloneReceipt(receipt))
			if len(result) >= limit {
				break
			}
		}
	}
	m.replayed += uint64(len(result))
	return result, nil
}

// receiptOrder is both a chronological cursor and a stable tie-breaker.
func receiptOrder(r Receipt) string {
	return r.AcceptedAt.UTC().Format("20060102T150405.000000000Z") + ":" + r.Identity
}

func (m *memoryInbox) Retained(_ context.Context, after string, limit int) ([]Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	var keys []string
	byKey := make(map[string]Receipt, len(m.receipts))
	for _, receipt := range m.receipts {
		key := receiptOrder(receipt)
		if key > after {
			keys = append(keys, key)
			byKey[key] = receipt
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]Receipt, 0, len(keys))
	for _, key := range keys {
		result = append(result, cloneReceipt(byKey[key]))
	}
	return result, nil
}

func (m *memoryInbox) Prune(_ context.Context, cutoff time.Time, maxRecords int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for identity, receipt := range m.receipts {
		if receipt.State == ReceiptApplied && !receipt.AppliedAt.IsZero() && receipt.AppliedAt.Before(cutoff) {
			delete(m.receipts, identity)
			m.payloadBytes -= int64(len(receipt.Payload))
			removed++
		}
	}
	m.pruned += uint64(removed)
	m.lastGC = time.Now().UTC()
	return removed, nil
}

func (m *memoryInbox) Stats(_ context.Context) InboxStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := InboxStats{
		Enabled:       true,
		Backend:       InboxBackendMemory,
		Duplicates:    m.duplicates,
		Replayed:      m.replayed,
		Pruned:        m.pruned,
		LastGCAt:      m.lastGC,
		LastError:     m.lastError,
		Retention:     m.cfg.Retention.String(),
		MaxRecords:    m.cfg.MaxRecords,
		LeaseDuration: m.cfg.Lease.String(),
		Records:       int64(len(m.receipts)),
		PayloadBytes:  m.payloadBytes,
		MaxBytes:      m.cfg.MaxBytes,
	}
	for _, receipt := range m.receipts {
		if receipt.State == ReceiptApplied {
			stats.Applied++
		} else {
			stats.Pending++
		}
	}
	return stats
}

func (m *memoryInbox) Close() error { return nil }

func cloneReceipt(receipt Receipt) Receipt {
	receipt.Payload = append([]byte(nil), receipt.Payload...)
	return receipt
}

func marshalReceipt(receipt Receipt) ([]byte, error) {
	return json.Marshal(receipt)
}

func unmarshalReceipt(payload []byte) (Receipt, error) {
	var receipt Receipt
	if err := json.Unmarshal(payload, &receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func validateReceipt(receipt Receipt) error {
	if receipt.SchemaVersion != 1 || receipt.Identity == "" || len(receipt.Identity) > 80 ||
		receipt.State != ReceiptCommitted || receipt.Revision != 1 || receipt.Attempts != 0 || receipt.AcceptedAt.IsZero() {
		return fmt.Errorf("invalid initial ingest receipt")
	}
	if len(receipt.Payload) == 0 || len(receipt.Payload) > maxInboxPayloadBytes {
		return fmt.Errorf("ingest receipt payload must be between 1 and %d bytes", maxInboxPayloadBytes)
	}
	return nil
}
