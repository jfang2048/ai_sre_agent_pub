package ingest

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"
)

const postgresInboxSchema = `
CREATE TABLE IF NOT EXISTS telemetry_ingest_receipts (
    identity TEXT PRIMARY KEY,
    schema_version SMALLINT NOT NULL,
    identity_kind TEXT NOT NULL,
    collector_id TEXT NOT NULL,
    producer_epoch TEXT NOT NULL DEFAULT '',
    producer_sequence NUMERIC(20,0) NOT NULL DEFAULT 0,
    batch_id TEXT NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL,
    payload BYTEA NOT NULL,
    state TEXT NOT NULL,
    revision BIGINT NOT NULL,
    apply_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ NULL,
    applied_at TIMESTAMPTZ NULL,
    attempts BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS telemetry_ingest_receipts_order_idx
    ON telemetry_ingest_receipts (accepted_at, identity);
CREATE INDEX IF NOT EXISTS telemetry_ingest_receipts_pending_idx
    ON telemetry_ingest_receipts (state, lease_expires_at, accepted_at);
CREATE INDEX IF NOT EXISTS telemetry_ingest_receipts_applied_idx
    ON telemetry_ingest_receipts (applied_at)
    WHERE state = 'applied';
CREATE TABLE IF NOT EXISTS telemetry_ingest_capacity (
    singleton SMALLINT PRIMARY KEY CHECK (singleton = 1),
    records BIGINT NOT NULL CHECK (records >= 0),
    payload_bytes BIGINT NOT NULL CHECK (payload_bytes >= 0)
);
INSERT INTO telemetry_ingest_capacity (singleton, records, payload_bytes)
SELECT 1, COUNT(*), COALESCE(SUM(octet_length(payload)), 0) FROM telemetry_ingest_receipts
ON CONFLICT (singleton) DO NOTHING;
`

type postgresInbox struct {
	cfg    InboxConfig
	db     *sql.DB
	owned  bool
	logger *zap.Logger

	duplicates atomic.Uint64
	replayed   atomic.Uint64
	pruned     atomic.Uint64
	lastGCNano atomic.Int64
	lastError  atomic.Value
}

// NewPostgresInbox opens and migrates a shared PostgreSQL ingest journal.
func NewPostgresInbox(ctx context.Context, cfg InboxConfig, logger *zap.Logger) (Inbox, error) {
	cfg = cfg.normalized()
	if strings.TrimSpace(cfg.PostgresDSN) == "" {
		return nil, fmt.Errorf("ingest inbox postgres DSN is empty")
	}
	db, err := sql.Open("pgx", cfg.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("open ingest inbox postgres: %w", err)
	}
	inbox, err := newPostgresInboxWithDB(ctx, cfg, db, logger, true)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return inbox, nil
}

// NewPostgresInboxWithDB supports deterministic SQL and multi-instance tests.
func NewPostgresInboxWithDB(ctx context.Context, cfg InboxConfig, db *sql.DB, logger *zap.Logger) (Inbox, error) {
	return newPostgresInboxWithDB(ctx, cfg.normalized(), db, logger, false)
}

func newPostgresInboxWithDB(ctx context.Context, cfg InboxConfig, db *sql.DB, logger *zap.Logger, owned bool) (*postgresInbox, error) {
	if db == nil {
		return nil, fmt.Errorf("ingest inbox postgres db is nil")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	var fsync, synchronousCommit string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('fsync'), current_setting('synchronous_commit')`).Scan(&fsync, &synchronousCommit); err != nil {
		return nil, fmt.Errorf("check ingest postgres durability: %w", err)
	}
	if fsync != "on" || synchronousCommit == "off" {
		return nil, fmt.Errorf("ingest postgres requires fsync=on and synchronous_commit enabled")
	}
	if _, err := db.ExecContext(ctx, postgresInboxSchema); err != nil {
		return nil, fmt.Errorf("migrate ingest inbox postgres schema: %w", err)
	}
	return &postgresInbox{
		cfg:    cfg,
		db:     db,
		owned:  owned,
		logger: logger.With(zap.String("component", "ingest_inbox")),
	}, nil
}

func (p *postgresInbox) Commit(ctx context.Context, receipt Receipt) (Receipt, bool, error) {
	if err := validateReceipt(receipt); err != nil {
		return Receipt{}, false, err
	}
	// validateReceipt fixes these initial counters at 1 and 0 respectively.
	revision := int64(receipt.Revision) // #nosec G115 -- validated bounded value
	attempts := int64(receipt.Attempts) // #nosec G115 -- validated bounded value
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Receipt{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize capacity reservations across controllers, without scanning or
	// rewriting the retained history on each incoming batch.
	var records, payloadBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT records, payload_bytes FROM telemetry_ingest_capacity WHERE singleton = 1 FOR UPDATE`).Scan(&records, &payloadBytes); err != nil {
		return Receipt{}, false, err
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO telemetry_ingest_receipts (
    identity, schema_version, identity_kind, collector_id, producer_epoch,
    producer_sequence, batch_id, accepted_at, payload, state, revision,
    apply_owner, lease_expires_at, applied_at, attempts, last_error
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'',NULL,NULL,$12,'')
ON CONFLICT (identity) DO NOTHING`,
		receipt.Identity, receipt.SchemaVersion, receipt.IdentityKind, receipt.CollectorID,
		receipt.ProducerEpoch, strconv.FormatUint(receipt.ProducerSequence, 10), receipt.BatchID, receipt.AcceptedAt,
		receipt.Payload, receipt.State, revision, attempts)
	if err != nil {
		p.recordError(err)
		return Receipt{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Receipt{}, false, err
	}
	created := rows == 1
	if created {
		if records >= int64(p.cfg.MaxRecords) || payloadBytes+int64(len(receipt.Payload)) > p.cfg.MaxBytes {
			return Receipt{}, false, ErrInboxFull
		}
		if _, err := tx.ExecContext(ctx, `UPDATE telemetry_ingest_capacity SET records = records + 1, payload_bytes = payload_bytes + $1 WHERE singleton = 1`, len(receipt.Payload)); err != nil {
			return Receipt{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		p.recordError(err)
		return Receipt{}, false, err
	}
	if created {
		return cloneReceipt(receipt), true, nil
	}
	p.duplicates.Add(1)
	stored, err := p.Get(ctx, receipt.Identity)
	if err == nil && !bytes.Equal(stored.Payload, receipt.Payload) {
		return Receipt{}, false, ErrIdentityConflict
	}
	return stored, false, err
}

func (p *postgresInbox) Get(ctx context.Context, identity string) (Receipt, error) {
	row := p.db.QueryRowContext(ctx, `
SELECT identity, schema_version, identity_kind, collector_id, producer_epoch,
       producer_sequence, batch_id, accepted_at, payload, state, revision,
       apply_owner, lease_expires_at, applied_at, attempts, last_error
FROM telemetry_ingest_receipts WHERE identity = $1`, identity)
	receipt, err := scanPostgresReceipt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Receipt{}, ErrReceiptNotFound
	}
	return receipt, err
}

func (p *postgresInbox) Claim(ctx context.Context, identity, owner string, now time.Time, lease time.Duration) (Receipt, bool, error) {
	row := p.db.QueryRowContext(ctx, `
UPDATE telemetry_ingest_receipts
SET state = 'applying',
    apply_owner = $2,
    lease_expires_at = $3,
    attempts = attempts + 1,
    revision = revision + 1
WHERE identity = $1
  AND (
      state = 'committed'
      OR (state = 'applying' AND (lease_expires_at IS NULL OR lease_expires_at <= $4))
  )
RETURNING identity, schema_version, identity_kind, collector_id, producer_epoch,
          producer_sequence, batch_id, accepted_at, payload, state, revision,
          apply_owner, lease_expires_at, applied_at, attempts, last_error`,
		identity, owner, now.UTC().Add(lease), now.UTC())
	receipt, err := scanPostgresReceipt(row)
	if err == nil {
		return receipt, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		p.recordError(err)
		return Receipt{}, false, err
	}
	receipt, err = p.Get(ctx, identity)
	return receipt, false, err
}

func (p *postgresInbox) Renew(ctx context.Context, identity, owner string, now time.Time, lease time.Duration) error {
	result, err := p.db.ExecContext(ctx, `UPDATE telemetry_ingest_receipts
SET lease_expires_at = $3 WHERE identity = $1 AND state = 'applying' AND apply_owner = $2`, identity, owner, now.Add(lease))
	return p.requireClaimUpdate(result, err)
}

func (p *postgresInbox) MarkApplied(ctx context.Context, identity, owner string, appliedAt time.Time) error {
	result, err := p.db.ExecContext(ctx, `
UPDATE telemetry_ingest_receipts
SET state = 'applied', applied_at = $3, apply_owner = '',
    lease_expires_at = NULL, last_error = '', revision = revision + 1
WHERE identity = $1 AND state = 'applying' AND apply_owner = $2`,
		identity, owner, appliedAt.UTC())
	return p.requireClaimUpdate(result, err)
}

func (p *postgresInbox) MarkFailed(ctx context.Context, identity, owner string, failure error) error {
	message := ""
	if failure != nil {
		message = failure.Error()
	}
	result, err := p.db.ExecContext(ctx, `
UPDATE telemetry_ingest_receipts
SET state = 'committed', apply_owner = '', lease_expires_at = NULL,
    last_error = $3, revision = revision + 1
WHERE identity = $1 AND state = 'applying' AND apply_owner = $2`,
		identity, owner, message)
	return p.requireClaimUpdate(result, err)
}

func (p *postgresInbox) requireClaimUpdate(result sql.Result, err error) error {
	if err != nil {
		p.recordError(err)
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (p *postgresInbox) Pending(ctx context.Context, now time.Time, limit int) ([]Receipt, error) {
	if limit <= 0 {
		limit = 256
	}
	rows, err := p.db.QueryContext(ctx, `
SELECT identity, schema_version, identity_kind, collector_id, producer_epoch,
       producer_sequence, batch_id, accepted_at, payload, state, revision,
       apply_owner, lease_expires_at, applied_at, attempts, last_error
FROM telemetry_ingest_receipts
WHERE state = 'committed'
   OR (state = 'applying' AND (lease_expires_at IS NULL OR lease_expires_at <= $1))
ORDER BY accepted_at ASC
LIMIT $2`, now.UTC(), limit)
	if err != nil {
		p.recordError(err)
		return nil, err
	}
	defer rows.Close()
	receipts := make([]Receipt, 0, limit)
	for rows.Next() {
		receipt, err := scanPostgresReceipt(rows)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	p.replayed.Add(uint64(len(receipts)))
	return receipts, nil
}

func (p *postgresInbox) Retained(ctx context.Context, after string, limit int) ([]Receipt, error) {
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	var accepted time.Time
	var identity string
	if after != "" {
		stamp, id, ok := strings.Cut(after, ":")
		if !ok {
			return nil, fmt.Errorf("invalid inbox cursor")
		}
		var err error
		accepted, err = time.Parse("20060102T150405.000000000Z", stamp)
		if err != nil {
			return nil, err
		}
		identity = id
	}
	rows, err := p.db.QueryContext(ctx, `SELECT identity, schema_version, identity_kind, collector_id, producer_epoch,
       producer_sequence, batch_id, accepted_at, payload, state, revision,
       apply_owner, lease_expires_at, applied_at, attempts, last_error
FROM telemetry_ingest_receipts WHERE (accepted_at, identity) > ($1, $2)
ORDER BY accepted_at, identity LIMIT $3`, accepted, identity, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Receipt, 0, limit)
	for rows.Next() {
		r, err := scanPostgresReceipt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (p *postgresInbox) Prune(ctx context.Context, cutoff time.Time, maxRecords int) (int, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT singleton FROM telemetry_ingest_capacity WHERE singleton = 1 FOR UPDATE`); err != nil {
		return 0, err
	}
	var removed, bytes int64
	err = tx.QueryRowContext(ctx, `WITH expired AS (
    DELETE FROM telemetry_ingest_receipts WHERE identity IN (
        SELECT identity FROM telemetry_ingest_receipts
        WHERE state = 'applied' AND applied_at < $1 ORDER BY applied_at LIMIT 1024
    ) RETURNING octet_length(payload) AS bytes
) SELECT COUNT(*), COALESCE(SUM(bytes), 0) FROM expired`, cutoff.UTC()).Scan(&removed, &bytes)
	if err != nil {
		return 0, err
	}
	if removed < 0 || bytes < 0 {
		return 0, fmt.Errorf("postgres inbox returned invalid prune totals: records=%d bytes=%d", removed, bytes)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE telemetry_ingest_capacity SET records = records - $1, payload_bytes = payload_bytes - $2 WHERE singleton = 1`, removed, bytes); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	p.pruned.Add(uint64(removed)) // #nosec G115 -- COUNT(*) is checked non-negative above
	p.lastGCNano.Store(time.Now().UTC().UnixNano())
	return int(removed), nil
}

func (p *postgresInbox) Stats(ctx context.Context) InboxStats {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stats := InboxStats{
		Enabled:       true,
		Backend:       InboxBackendPostgres,
		Shared:        true,
		Duplicates:    p.duplicates.Load(),
		Replayed:      p.replayed.Load(),
		Pruned:        p.pruned.Load(),
		Retention:     p.cfg.Retention.String(),
		MaxRecords:    p.cfg.MaxRecords,
		MaxBytes:      p.cfg.MaxBytes,
		LeaseDuration: p.cfg.Lease.String(),
	}
	if nanos := p.lastGCNano.Load(); nanos > 0 {
		stats.LastGCAt = time.Unix(0, nanos).UTC()
	}
	if value := p.lastError.Load(); value != nil {
		stats.LastError, _ = value.(string)
	}
	err := p.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE state = 'applied'),
       COUNT(*) FILTER (WHERE state <> 'applied'),
       COALESCE(SUM(octet_length(payload)), 0)
FROM telemetry_ingest_receipts`).Scan(&stats.Records, &stats.Applied, &stats.Pending, &stats.PayloadBytes)
	if err != nil {
		stats.Enabled = false
		stats.LastError = err.Error()
	}
	return stats
}

func (p *postgresInbox) Close() error {
	if p == nil || p.db == nil || !p.owned {
		return nil
	}
	return p.db.Close()
}

type receiptScanner interface {
	Scan(...any) error
}

func scanPostgresReceipt(scanner receiptScanner) (Receipt, error) {
	var receipt Receipt
	var schemaVersion int64
	var producerSequence string
	var revision int64
	var attempts int64
	var leaseExpires sql.NullTime
	var appliedAt sql.NullTime
	err := scanner.Scan(
		&receipt.Identity,
		&schemaVersion,
		&receipt.IdentityKind,
		&receipt.CollectorID,
		&receipt.ProducerEpoch,
		&producerSequence,
		&receipt.BatchID,
		&receipt.AcceptedAt,
		&receipt.Payload,
		&receipt.State,
		&revision,
		&receipt.ApplyOwner,
		&leaseExpires,
		&appliedAt,
		&attempts,
		&receipt.LastError,
	)
	if err != nil {
		return Receipt{}, err
	}
	if schemaVersion < 0 || schemaVersion > math.MaxUint8 || revision < 0 || attempts < 0 {
		return Receipt{}, fmt.Errorf("invalid signed value in postgres ingest receipt")
	}
	receipt.SchemaVersion = uint8(schemaVersion)
	receipt.ProducerSequence, err = strconv.ParseUint(producerSequence, 10, 64)
	if err != nil {
		return Receipt{}, fmt.Errorf("invalid producer sequence in ingest receipt: %w", err)
	}
	receipt.Revision = uint64(revision)
	receipt.Attempts = uint64(attempts)
	if leaseExpires.Valid {
		receipt.LeaseExpiresAt = leaseExpires.Time
	}
	if appliedAt.Valid {
		receipt.AppliedAt = appliedAt.Time
	}
	return receipt, nil
}

func (p *postgresInbox) recordError(err error) {
	if err == nil || errors.Is(err, ErrReceiptNotFound) {
		return
	}
	p.lastError.Store(strings.TrimSpace(err.Error()))
}
