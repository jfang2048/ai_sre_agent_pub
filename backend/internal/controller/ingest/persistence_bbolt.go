package ingest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketNodes   = []byte("nodes")
	bucketHistory = []byte("history")
	bucketMeta    = []byte("meta")
	metaSavedAt   = []byte("saved_at_unix_nano")
)

// PersistenceStats summarizes embedded store behavior.
type PersistenceStats struct {
	Enabled         bool      `json:"enabled"`
	Path            string    `json:"path,omitempty"`
	CurrentDBBytes  int64     `json:"current_db_bytes,omitempty"`
	MaxDBBytes      int64     `json:"max_db_bytes,omitempty"`
	SyncInterval    string    `json:"sync_interval,omitempty"`
	LastSyncAt      time.Time `json:"last_sync_at,omitempty"`
	LastSyncError   string    `json:"last_sync_error,omitempty"`
	LastCompaction  time.Time `json:"last_compaction_at,omitempty"`
	Compactions     uint64    `json:"compactions"`
	CompactionEvery string    `json:"compaction_interval,omitempty"`
}

type boltPersistence struct {
	mu sync.Mutex

	cfg  PersistenceConfig
	db   *bolt.DB
	path string

	lastSyncAt     time.Time
	lastSyncErr    string
	lastCompactAt  time.Time
	compactions    uint64
	currentDBBytes int64
}

func newBoltPersistence(cfg PersistenceConfig) (*boltPersistence, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create persistence dir: %w", err)
	}
	db, err := bolt.Open(cfg.Path, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open persistence db: %w", err)
	}
	p := &boltPersistence{
		cfg:  cfg,
		db:   db,
		path: cfg.Path,
	}
	if err := p.ensureBuckets(); err != nil {
		_ = db.Close()
		return nil, err
	}
	p.refreshFileSize()
	return p, nil
}

func (p *boltPersistence) ensureBuckets() error {
	return p.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketNodes); err != nil {
			return fmt.Errorf("create nodes bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists(bucketHistory); err != nil {
			return fmt.Errorf("create history bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists(bucketMeta); err != nil {
			return fmt.Errorf("create meta bucket: %w", err)
		}
		return nil
	})
}

func (p *boltPersistence) loadSnapshot() (map[string]*NodeSnapshot, map[string][]MetricHistorySample, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	nodes := make(map[string]*NodeSnapshot)
	history := make(map[string][]MetricHistorySample)

	if p.db == nil {
		return nodes, history, nil
	}

	err := p.db.View(func(tx *bolt.Tx) error {
		nodesBucket := tx.Bucket(bucketNodes)
		if nodesBucket != nil {
			if err := nodesBucket.ForEach(func(k, v []byte) error {
				if len(k) == 0 || len(v) == 0 {
					return nil
				}
				var node NodeSnapshot
				if err := json.Unmarshal(v, &node); err != nil {
					return nil
				}
				nodes[string(k)] = &node
				return nil
			}); err != nil {
				return err
			}
		}

		historyBucket := tx.Bucket(bucketHistory)
		if historyBucket != nil {
			if err := historyBucket.ForEach(func(k, v []byte) error {
				if len(k) == 0 || len(v) == 0 {
					return nil
				}
				var samples []MetricHistorySample
				if err := json.Unmarshal(v, &samples); err != nil {
					return nil
				}
				history[string(k)] = samples
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("load snapshot: %w", err)
	}
	return nodes, history, nil
}

func (p *boltPersistence) saveSnapshot(nodes map[string]*NodeSnapshot, history map[string][]MetricHistorySample) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db == nil {
		return nil
	}

	saveErr := p.db.Update(func(tx *bolt.Tx) error {
		nodesBucket := tx.Bucket(bucketNodes)
		historyBucket := tx.Bucket(bucketHistory)
		metaBucket := tx.Bucket(bucketMeta)
		if nodesBucket == nil || historyBucket == nil || metaBucket == nil {
			return fmt.Errorf("required persistence bucket missing")
		}

		if err := clearBucket(nodesBucket); err != nil {
			return fmt.Errorf("clear nodes bucket: %w", err)
		}
		if err := clearBucket(historyBucket); err != nil {
			return fmt.Errorf("clear history bucket: %w", err)
		}

		nodeKeys := make([]string, 0, len(nodes))
		for key := range nodes {
			nodeKeys = append(nodeKeys, key)
		}
		sort.Strings(nodeKeys)
		for _, key := range nodeKeys {
			node := nodes[key]
			if node == nil {
				continue
			}
			payload, err := json.Marshal(node)
			if err != nil {
				return fmt.Errorf("marshal node %q: %w", key, err)
			}
			if err := nodesBucket.Put([]byte(key), payload); err != nil {
				return fmt.Errorf("write node %q: %w", key, err)
			}
		}

		historyKeys := make([]string, 0, len(history))
		for key := range history {
			historyKeys = append(historyKeys, key)
		}
		sort.Strings(historyKeys)
		for _, key := range historyKeys {
			samples := history[key]
			payload, err := json.Marshal(samples)
			if err != nil {
				return fmt.Errorf("marshal history %q: %w", key, err)
			}
			if err := historyBucket.Put([]byte(key), payload); err != nil {
				return fmt.Errorf("write history %q: %w", key, err)
			}
		}

		now := time.Now().UTC().UnixNano()
		return metaBucket.Put(metaSavedAt, []byte(fmt.Sprintf("%d", now)))
	})

	if saveErr != nil {
		p.lastSyncErr = saveErr.Error()
		p.lastSyncAt = time.Now().UTC()
		return saveErr
	}

	p.lastSyncErr = ""
	p.lastSyncAt = time.Now().UTC()
	p.refreshFileSize()

	if err := p.maybeCompactLocked(false); err != nil {
		p.lastSyncErr = err.Error()
		return err
	}
	if p.cfg.MaxDBSizeBytes > 0 && p.currentDBBytes > p.cfg.MaxDBSizeBytes {
		err := fmt.Errorf("persistence db size %d exceeds configured max %d", p.currentDBBytes, p.cfg.MaxDBSizeBytes)
		p.lastSyncErr = err.Error()
		return err
	}
	return nil
}

func (p *boltPersistence) compactNow() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maybeCompactLocked(true)
}

func (p *boltPersistence) maybeCompactLocked(force bool) error {
	if p.db == nil {
		return nil
	}
	if !p.shouldCompactLocked(force) {
		return nil
	}

	tmpPath := p.path + ".compact"
	_ = os.Remove(tmpPath)
	dst, err := bolt.Open(tmpPath, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return fmt.Errorf("open compact db: %w", err)
	}
	if err := bolt.Compact(dst, p.db, p.cfg.CompactTxMaxSize); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("compact db: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close compact db: %w", err)
	}
	if err := p.db.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close old db: %w", err)
	}
	if err := os.Rename(tmpPath, p.path); err != nil {
		return fmt.Errorf("replace compact db: %w", err)
	}

	db, err := bolt.Open(p.path, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return fmt.Errorf("reopen compacted db: %w", err)
	}
	p.db = db
	if err := p.ensureBuckets(); err != nil {
		return err
	}

	p.lastCompactAt = time.Now().UTC()
	p.compactions++
	p.refreshFileSize()
	if p.cfg.MaxDBSizeBytes > 0 && p.currentDBBytes > p.cfg.MaxDBSizeBytes {
		return fmt.Errorf("compacted db size %d exceeds configured max %d", p.currentDBBytes, p.cfg.MaxDBSizeBytes)
	}
	return nil
}

func (p *boltPersistence) shouldCompactLocked(force bool) bool {
	if force {
		return true
	}
	if p.cfg.MaxDBSizeBytes > 0 && p.currentDBBytes > p.cfg.MaxDBSizeBytes {
		return true
	}
	if p.cfg.CompactionInterval <= 0 {
		return false
	}
	if p.lastCompactAt.IsZero() {
		return false
	}
	return time.Since(p.lastCompactAt) >= p.cfg.CompactionInterval
}

func (p *boltPersistence) refreshFileSize() {
	info, err := os.Stat(p.path)
	if err != nil {
		p.currentDBBytes = 0
		return
	}
	p.currentDBBytes = info.Size()
}

func (p *boltPersistence) stats() PersistenceStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := PersistenceStats{
		Enabled:         p != nil,
		Path:            p.path,
		CurrentDBBytes:  p.currentDBBytes,
		MaxDBBytes:      p.cfg.MaxDBSizeBytes,
		SyncInterval:    p.cfg.SyncInterval.String(),
		LastSyncAt:      p.lastSyncAt,
		LastSyncError:   p.lastSyncErr,
		LastCompaction:  p.lastCompactAt,
		Compactions:     p.compactions,
		CompactionEvery: p.cfg.CompactionInterval.String(),
	}
	return out
}

func (p *boltPersistence) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db == nil {
		return nil
	}
	err := p.db.Close()
	p.db = nil
	return err
}

func clearBucket(bucket *bolt.Bucket) error {
	cursor := bucket.Cursor()
	for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
		if err := bucket.Delete(k); err != nil {
			return err
		}
	}
	return nil
}
