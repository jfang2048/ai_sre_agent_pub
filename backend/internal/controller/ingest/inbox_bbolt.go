package ingest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
	"go.uber.org/zap"
)

var (
	bucketInboxReceipts = []byte("ingest_receipts_v1")
	bucketInboxMeta     = []byte("ingest_meta_v1")
	bucketInboxOrder    = []byte("ingest_order_v1")
	bucketInboxPending  = []byte("ingest_pending_v1")
	keyInboxSchema      = []byte("schema_version")
	keyInboxBytes       = []byte("payload_bytes")
)

type boltInbox struct {
	cfg    InboxConfig
	db     *bolt.DB
	logger *zap.Logger

	duplicates atomic.Uint64
	replayed   atomic.Uint64
	pruned     atomic.Uint64
	lastGCNano atomic.Int64
	lastError  atomic.Value
}

func newBoltInbox(cfg InboxConfig, logger *zap.Logger) (*boltInbox, error) {
	cfg = cfg.normalized()
	if logger == nil {
		logger = zap.NewNop()
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o750); err != nil {
		return nil, fmt.Errorf("create ingest inbox directory: %w", err)
	}
	db, err := bolt.Open(cfg.Path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open ingest inbox: %w", err)
	}
	inbox := &boltInbox{cfg: cfg, db: db, logger: logger.With(zap.String("component", "ingest_inbox"))}
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketInboxReceipts); err != nil {
			return err
		}
		if tx.Bucket(bucketInboxOrder) == nil {
			order, err := tx.CreateBucket(bucketInboxOrder)
			if err != nil {
				return err
			}
			if err := tx.Bucket(bucketInboxReceipts).ForEach(func(key, value []byte) error {
				r, err := unmarshalReceipt(value)
				if err != nil {
					return err
				}
				return order.Put([]byte(receiptOrder(r)), key)
			}); err != nil {
				return err
			}
		}
		if tx.Bucket(bucketInboxPending) == nil {
			pending, err := tx.CreateBucket(bucketInboxPending)
			if err != nil {
				return err
			}
			if err := tx.Bucket(bucketInboxReceipts).ForEach(func(key, value []byte) error {
				r, err := unmarshalReceipt(value)
				if err != nil {
					return err
				}
				if r.State != ReceiptApplied {
					return pending.Put([]byte(receiptOrder(r)), key)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		meta, err := tx.CreateBucketIfNotExists(bucketInboxMeta)
		if err != nil {
			return err
		}
		if version := meta.Get(keyInboxSchema); version != nil && string(version) != "1" {
			return fmt.Errorf("unsupported ingest inbox schema %q", version)
		}
		if meta.Get(keyInboxBytes) == nil {
			var bytes uint64
			if err := tx.Bucket(bucketInboxReceipts).ForEach(func(_, payload []byte) error {
				receipt, err := unmarshalReceipt(payload)
				bytes += uint64(len(receipt.Payload))
				return err
			}); err != nil {
				return err
			}
			if err := putInboxBytes(meta, bytes); err != nil {
				return err
			}
		}
		return meta.Put(keyInboxSchema, []byte("1"))
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize ingest inbox: %w", err)
	}
	return inbox, nil
}

func (b *boltInbox) Commit(ctx context.Context, receipt Receipt) (Receipt, bool, error) {
	if err := validateReceipt(receipt); err != nil {
		return Receipt{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, false, err
	}
	var stored Receipt
	created := false
	err := b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketInboxReceipts)
		if bucket == nil {
			return fmt.Errorf("ingest receipt bucket missing")
		}
		key := []byte(receipt.Identity)
		if payload := bucket.Get(key); payload != nil {
			existing, err := unmarshalReceipt(payload)
			if err != nil {
				return fmt.Errorf("decode existing receipt: %w", err)
			}
			stored = existing
			if !bytes.Equal(existing.Payload, receipt.Payload) {
				return ErrIdentityConflict
			}
			return nil
		}
		meta := tx.Bucket(bucketInboxMeta)
		bytes := inboxBytes(meta) + uint64(len(receipt.Payload))
		if bucket.Stats().KeyN >= b.cfg.MaxRecords || bytes > uint64(b.cfg.MaxBytes) {
			return ErrInboxFull
		}
		payload, err := marshalReceipt(receipt)
		if err != nil {
			return err
		}
		if err := bucket.Put(key, payload); err != nil {
			return err
		}
		if err := tx.Bucket(bucketInboxOrder).Put([]byte(receiptOrder(receipt)), key); err != nil {
			return err
		}
		if err := tx.Bucket(bucketInboxPending).Put([]byte(receiptOrder(receipt)), key); err != nil {
			return err
		}
		if err := putInboxBytes(meta, bytes); err != nil {
			return err
		}
		stored = receipt
		created = true
		return nil
	})
	if err != nil {
		b.recordError(err)
		return Receipt{}, false, err
	}
	if !created {
		b.duplicates.Add(1)
	}
	return cloneReceipt(stored), created, nil
}

func (b *boltInbox) Get(ctx context.Context, identity string) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	err := b.db.View(func(tx *bolt.Tx) error {
		payload := tx.Bucket(bucketInboxReceipts).Get([]byte(identity))
		if payload == nil {
			return ErrReceiptNotFound
		}
		var err error
		receipt, err = unmarshalReceipt(payload)
		return err
	})
	return cloneReceipt(receipt), err
}

func (b *boltInbox) Claim(ctx context.Context, identity, owner string, now time.Time, lease time.Duration) (Receipt, bool, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, false, err
	}
	var receipt Receipt
	claimed := false
	err := b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketInboxReceipts)
		payload := bucket.Get([]byte(identity))
		if payload == nil {
			return ErrReceiptNotFound
		}
		var err error
		receipt, err = unmarshalReceipt(payload)
		if err != nil {
			return err
		}
		if receipt.State == ReceiptApplied ||
			(receipt.State == ReceiptApplying && receipt.LeaseExpiresAt.After(now)) {
			return nil
		}
		receipt.State = ReceiptApplying
		receipt.ApplyOwner = owner
		receipt.LeaseExpiresAt = now.UTC().Add(lease)
		receipt.Attempts++
		receipt.Revision++
		encoded, err := marshalReceipt(receipt)
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(identity), encoded); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil {
		b.recordError(err)
	}
	return cloneReceipt(receipt), claimed, err
}

func (b *boltInbox) Renew(ctx context.Context, identity, owner string, now time.Time, lease time.Duration) error {
	return b.updateClaimed(ctx, identity, owner, func(receipt *Receipt) { receipt.LeaseExpiresAt = now.Add(lease) })
}

func (b *boltInbox) MarkApplied(ctx context.Context, identity, owner string, appliedAt time.Time) error {
	return b.updateClaimed(ctx, identity, owner, func(receipt *Receipt) {
		receipt.State = ReceiptApplied
		receipt.AppliedAt = appliedAt.UTC()
		receipt.ApplyOwner = ""
		receipt.LeaseExpiresAt = time.Time{}
		receipt.LastError = ""
	})
}

func (b *boltInbox) MarkFailed(ctx context.Context, identity, owner string, failure error) error {
	return b.updateClaimed(ctx, identity, owner, func(receipt *Receipt) {
		receipt.State = ReceiptCommitted
		receipt.ApplyOwner = ""
		receipt.LeaseExpiresAt = time.Time{}
		if failure != nil {
			receipt.LastError = failure.Error()
		}
	})
}

func (b *boltInbox) updateClaimed(ctx context.Context, identity, owner string, update func(*Receipt)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketInboxReceipts)
		payload := bucket.Get([]byte(identity))
		if payload == nil {
			return ErrReceiptNotFound
		}
		receipt, err := unmarshalReceipt(payload)
		if err != nil {
			return err
		}
		if receipt.State != ReceiptApplying || receipt.ApplyOwner != owner {
			return ErrLeaseLost
		}
		update(&receipt)
		receipt.Revision++
		if receipt.State == ReceiptApplied {
			if err := tx.Bucket(bucketInboxPending).Delete([]byte(receiptOrder(receipt))); err != nil {
				return err
			}
		}
		encoded, err := marshalReceipt(receipt)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(identity), encoded)
	})
	if err != nil {
		b.recordError(err)
	}
	return err
}

func (b *boltInbox) Pending(ctx context.Context, now time.Time, limit int) ([]Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 256
	}
	result := make([]Receipt, 0, limit)
	err := b.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketInboxPending).Cursor()
		for _, key := cursor.First(); key != nil && len(result) < limit; _, key = cursor.Next() {
			payload := tx.Bucket(bucketInboxReceipts).Get(key)
			receipt, err := unmarshalReceipt(payload)
			if err != nil {
				return err
			}
			if receipt.State == ReceiptCommitted || (receipt.State == ReceiptApplying && !receipt.LeaseExpiresAt.After(now)) {
				result = append(result, receipt)
			}
		}
		return nil
	})
	if err != nil {
		b.recordError(err)
		return nil, err
	}
	b.replayed.Add(uint64(len(result)))
	return result, nil
}

func (b *boltInbox) Retained(ctx context.Context, after string, limit int) ([]Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	result := make([]Receipt, 0, limit)
	err := b.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketInboxOrder).Cursor()
		key, identity := cursor.Seek([]byte(after))
		if string(key) == after {
			key, identity = cursor.Next()
		}
		for ; key != nil && len(result) < limit; key, identity = cursor.Next() {
			receipt, err := unmarshalReceipt(tx.Bucket(bucketInboxReceipts).Get(identity))
			if err != nil {
				return err
			}
			result = append(result, receipt)
		}
		return nil
	})
	return result, err
}

func (b *boltInbox) Prune(ctx context.Context, cutoff time.Time, maxRecords int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if maxRecords <= 0 {
		maxRecords = b.cfg.MaxRecords
	}
	removed := 0
	err := b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketInboxReceipts)
		meta := tx.Bucket(bucketInboxMeta)
		bytes := inboxBytes(meta)
		keys := make([][]byte, 0)
		cursor := bucket.Cursor()
		for key, payload := cursor.First(); key != nil && len(keys) < 1024; key, payload = cursor.Next() {
			receipt, err := unmarshalReceipt(payload)
			if err != nil {
				return err
			}
			if receipt.State == ReceiptApplied && !receipt.AppliedAt.IsZero() && receipt.AppliedAt.Before(cutoff) {
				keys = append(keys, append([]byte(nil), key...))
				bytes -= uint64(len(receipt.Payload))
				if err := tx.Bucket(bucketInboxOrder).Delete([]byte(receiptOrder(receipt))); err != nil {
					return err
				}
			}
		}
		for _, key := range keys {
			if err := bucket.Delete(key); err != nil {
				return err
			}
			removed++
		}
		return putInboxBytes(meta, bytes)
	})
	b.lastGCNano.Store(time.Now().UTC().UnixNano())
	if err != nil {
		b.recordError(err)
		return 0, err
	}
	b.pruned.Add(uint64(removed))
	return removed, nil
}

func (b *boltInbox) Stats(ctx context.Context) InboxStats {
	stats := InboxStats{
		Enabled:       true,
		Backend:       InboxBackendBbolt,
		Path:          b.cfg.Path,
		Duplicates:    b.duplicates.Load(),
		Replayed:      b.replayed.Load(),
		Pruned:        b.pruned.Load(),
		Retention:     b.cfg.Retention.String(),
		MaxRecords:    b.cfg.MaxRecords,
		MaxBytes:      b.cfg.MaxBytes,
		LeaseDuration: b.cfg.Lease.String(),
	}
	if nanos := b.lastGCNano.Load(); nanos > 0 {
		stats.LastGCAt = time.Unix(0, nanos).UTC()
	}
	if value := b.lastError.Load(); value != nil {
		stats.LastError, _ = value.(string)
	}
	if err := ctx.Err(); err != nil {
		stats.LastError = err.Error()
		return stats
	}
	if err := b.db.View(func(tx *bolt.Tx) error {
		stats.PayloadBytes = int64(inboxBytes(tx.Bucket(bucketInboxMeta)))
		stats.Records = int64(tx.Bucket(bucketInboxReceipts).Stats().KeyN)
		stats.Pending = int64(tx.Bucket(bucketInboxPending).Stats().KeyN)
		stats.Applied = stats.Records - stats.Pending
		return nil
	}); err != nil {
		stats.Enabled = false
		stats.LastError = err.Error()
	}
	return stats
}

func (b *boltInbox) Close() error {
	if b == nil || b.db == nil {
		return nil
	}
	return b.db.Close()
}

func (b *boltInbox) recordError(err error) {
	if err == nil || errors.Is(err, ErrReceiptNotFound) {
		return
	}
	b.lastError.Store(strings.TrimSpace(err.Error()))
}

func inboxBytes(meta *bolt.Bucket) uint64 {
	value := meta.Get(keyInboxBytes)
	if len(value) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(value)
}

func putInboxBytes(meta *bolt.Bucket, bytes uint64) error {
	var value [8]byte
	binary.BigEndian.PutUint64(value[:], bytes)
	return meta.Put(keyInboxBytes, value[:])
}
