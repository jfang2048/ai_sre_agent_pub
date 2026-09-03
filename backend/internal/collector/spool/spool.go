package spool

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	spoolFileName   = "spool.log"
	offsetFileName  = "spool.offset"
	headerSizeBytes = 4
)

var (
	// ErrPayloadTooLarge indicates one payload cannot fit inside the bounded spool.
	ErrPayloadTooLarge = errors.New("spool payload exceeds max size")
	// ErrCorruptSegment indicates the unread tail was truncated or corrupted and was dropped.
	ErrCorruptSegment = errors.New("spool unread segment was truncated or corrupted")
)

// Snapshot captures spool backlog and recovery state for metrics/UI surfaces.
type Snapshot struct {
	BacklogBytes         int64  `json:"backlog_bytes"`
	FileSizeBytes        int64  `json:"file_size_bytes"`
	MaxBytes             int64  `json:"max_bytes"`
	Offset               int64  `json:"offset"`
	EvictedRecords       uint64 `json:"evicted_records"`
	CorruptionRecoveries uint64 `json:"corruption_recoveries"`
	LastRecoveryReason   string `json:"last_recovery_reason,omitempty"`
}

// Options controls spool durability tradeoffs.
type Options struct {
	DataSyncInterval   time.Duration
	OffsetSyncInterval time.Duration
}

// Spool provides a minimal persistent buffer for telemetry batches.
type Spool struct {
	dir        string
	maxBytes   int64
	options    Options
	offsetPath string
	mu         sync.Mutex
	file       *os.File
	offset     int64

	evictedRecords       uint64
	corruptionRecoveries uint64
	lastRecoveryReason   string
	lastDataSync         time.Time
	lastOffsetSync       time.Time
	dataDirty            bool
	offsetDirty          bool
}

// New creates a spool in the given directory.
func New(dir string, maxBytes int64) (*Spool, error) {
	return NewWithOptions(dir, maxBytes, Options{})
}

// NewWithOptions creates a spool in the given directory with explicit sync settings.
func NewWithOptions(dir string, maxBytes int64, options Options) (*Spool, error) {
	if dir == "" {
		return nil, fmt.Errorf("spool dir required")
	}
	if maxBytes <= 0 {
		maxBytes = 128 * 1024 * 1024
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, spoolFileName)
	offsetPath := filepath.Join(dir, offsetFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	offset := readOffset(offsetPath)
	if offset < 0 || offset > stat.Size() {
		offset = 0
		if err := writeOffset(offsetPath, offset); err != nil {
			_ = file.Close()
			return nil, err
		}
	}

	return &Spool{
		dir:            dir,
		maxBytes:       maxBytes,
		options:        options,
		offsetPath:     offsetPath,
		file:           file,
		offset:         offset,
		lastDataSync:   time.Now(),
		lastOffsetSync: time.Now(),
	}, nil
}

// Enqueue appends a batch to the spool while preserving the newest unread data.
func (s *Spool) Enqueue(payload []byte) error {
	if len(payload) > math.MaxUint32 {
		return ErrPayloadTooLarge
	}
	requiredBytes := int64(headerSizeBytes + len(payload))
	if requiredBytes > s.maxBytes {
		return ErrPayloadTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.compactLocked(requiredBytes); err != nil {
		return err
	}

	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	var header [headerSizeBytes]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload))) // #nosec G115 -- length is bounded by math.MaxUint32 above.
	if _, err := s.file.Write(header[:]); err != nil {
		return err
	}
	if _, err := s.file.Write(payload); err != nil {
		return err
	}
	s.dataDirty = true
	return s.flushDataLocked(false)
}

// Next returns the next payload without advancing offset.
func (s *Spool) Next() ([]byte, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stat, err := s.file.Stat()
	if err != nil {
		return nil, s.offset, err
	}
	size := stat.Size()
	if s.offset >= size {
		return nil, s.offset, nil
	}
	if size-s.offset < headerSizeBytes {
		return nil, s.offset, s.recoverCorruptionLocked("truncated_header")
	}

	header := make([]byte, headerSizeBytes)
	if _, err := s.file.ReadAt(header, s.offset); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, s.offset, s.recoverCorruptionLocked("truncated_header")
		}
		return nil, s.offset, err
	}

	length := int64(binary.LittleEndian.Uint32(header))
	if length <= 0 || length > s.maxBytes-headerSizeBytes {
		return nil, s.offset, s.recoverCorruptionLocked("invalid_record_length")
	}

	nextOffset := s.offset + headerSizeBytes + length
	if nextOffset > size {
		return nil, s.offset, s.recoverCorruptionLocked("truncated_payload")
	}

	payload := make([]byte, length)
	if _, err := s.file.ReadAt(payload, s.offset+headerSizeBytes); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, s.offset, s.recoverCorruptionLocked("truncated_payload")
		}
		return nil, s.offset, err
	}

	return payload, nextOffset, nil
}

// Commit advances the spool offset after successful delivery.
func (s *Spool) Commit(nextOffset int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if nextOffset < s.offset {
		return nil
	}
	s.offset = nextOffset
	s.offsetDirty = true
	return s.flushOffsetLocked(false)
}

// Stats returns current backlog and file size in bytes.
func (s *Spool) Stats() (backlogBytes int64, fileSizeBytes int64) {
	snapshot := s.Snapshot()
	return snapshot.BacklogBytes, snapshot.FileSizeBytes
}

// Snapshot returns spool backlog plus recovery/eviction counters.
func (s *Spool) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	stat, err := s.file.Stat()
	if err != nil {
		return Snapshot{MaxBytes: s.maxBytes, Offset: s.offset}
	}
	fileSizeBytes := stat.Size()
	backlogBytes := fileSizeBytes - s.offset
	if backlogBytes < 0 {
		backlogBytes = 0
	}
	return Snapshot{
		BacklogBytes:         backlogBytes,
		FileSizeBytes:        fileSizeBytes,
		MaxBytes:             s.maxBytes,
		Offset:               s.offset,
		EvictedRecords:       s.evictedRecords,
		CorruptionRecoveries: s.corruptionRecoveries,
		LastRecoveryReason:   s.lastRecoveryReason,
	}
}

// Close flushes pending durability state and releases the file descriptor.
func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return nil
	}
	if err := s.flushDataLocked(true); err != nil {
		return err
	}
	if err := s.flushOffsetLocked(true); err != nil {
		return err
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *Spool) compactLocked(requiredBytes int64) error {
	stat, err := s.file.Stat()
	if err != nil {
		return err
	}
	if stat.Size()+requiredBytes <= s.maxBytes {
		return nil
	}

	backlog, err := s.readUnreadLocked(stat.Size())
	if err != nil {
		return err
	}
	kept, evicted, recovered, reason, err := trimUnreadToFit(backlog, s.maxBytes-requiredBytes, s.maxBytes)
	if err != nil {
		return err
	}
	if recovered {
		s.recordRecoveryLocked(reason)
	}
	if evicted > 0 {
		s.evictedRecords += uint64(evicted)
	}
	return s.rewriteLocked(kept)
}

func (s *Spool) readUnreadLocked(fileSize int64) ([]byte, error) {
	if s.offset >= fileSize {
		return nil, nil
	}
	backlog := make([]byte, fileSize-s.offset)
	if _, err := s.file.ReadAt(backlog, s.offset); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return backlog, nil
}

func trimUnreadToFit(data []byte, budget int64, maxBytes int64) ([]byte, int, bool, string, error) {
	if len(data) == 0 {
		return nil, 0, false, "", nil
	}

	frameEnds := make([]int, 0, 16)
	validEnd := 0
	recovered := false
	reason := ""
	for pos := 0; pos < len(data); {
		if len(data)-pos < headerSizeBytes {
			recovered = true
			reason = "truncated_header"
			break
		}
		length := int64(binary.LittleEndian.Uint32(data[pos : pos+headerSizeBytes]))
		if length <= 0 || length > maxBytes-headerSizeBytes {
			recovered = true
			reason = "invalid_record_length"
			break
		}
		end := pos + headerSizeBytes + int(length)
		if end > len(data) {
			recovered = true
			reason = "truncated_payload"
			break
		}
		frameEnds = append(frameEnds, end)
		validEnd = end
		pos = end
	}

	if validEnd == 0 {
		return nil, len(frameEnds), recovered, reason, nil
	}

	valid := data[:validEnd]
	if budget <= 0 {
		return nil, len(frameEnds), recovered, reason, nil
	}
	if int64(len(valid)) <= budget {
		return append([]byte(nil), valid...), 0, recovered, reason, nil
	}

	evicted := 0
	start := 0
	for _, end := range frameEnds {
		if int64(validEnd-start) <= budget {
			break
		}
		start = end
		evicted++
	}
	if start >= validEnd {
		return nil, len(frameEnds), recovered, reason, nil
	}
	return append([]byte(nil), valid[start:validEnd]...), evicted, recovered, reason, nil
}

func (s *Spool) rewriteLocked(backlog []byte) error {
	oldFile := s.file
	oldPath := filepath.Join(s.dir, spoolFileName)
	tmpPath := oldPath + ".tmp"

	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if len(backlog) > 0 {
		if _, err := tmpFile.Write(backlog); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := oldFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, oldPath); err != nil {
		return err
	}

	file, err := os.OpenFile(oldPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	s.file = file
	s.offset = 0
	s.dataDirty = false
	s.lastDataSync = time.Now()
	s.offsetDirty = true
	return s.flushOffsetLocked(true)
}

func (s *Spool) recoverCorruptionLocked(reason string) error {
	if err := s.file.Truncate(s.offset); err != nil {
		return err
	}
	s.dataDirty = true
	if err := s.flushDataLocked(true); err != nil {
		return err
	}
	s.recordRecoveryLocked(reason)
	return ErrCorruptSegment
}

func (s *Spool) recordRecoveryLocked(reason string) {
	s.corruptionRecoveries++
	s.lastRecoveryReason = reason
}

func readOffset(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var offset int64
	if _, err := fmt.Sscanf(string(data), "%d", &offset); err != nil {
		return 0
	}
	return offset
}

func writeOffset(path string, offset int64) error {
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", offset)), 0o644)
}

func (s *Spool) flushDataLocked(force bool) error {
	if s.file == nil || !s.dataDirty {
		return nil
	}
	if !force && s.options.DataSyncInterval > 0 && !s.lastDataSync.IsZero() &&
		time.Since(s.lastDataSync) < s.options.DataSyncInterval {
		return nil
	}
	if err := s.file.Sync(); err != nil {
		return err
	}
	s.dataDirty = false
	s.lastDataSync = time.Now()
	return nil
}

func (s *Spool) flushOffsetLocked(force bool) error {
	if !s.offsetDirty {
		return nil
	}
	if !force && s.options.OffsetSyncInterval > 0 && !s.lastOffsetSync.IsZero() &&
		time.Since(s.lastOffsetSync) < s.options.OffsetSyncInterval {
		return nil
	}
	if err := writeOffset(s.offsetPath, s.offset); err != nil {
		return err
	}
	s.offsetDirty = false
	s.lastOffsetSync = time.Now()
	return nil
}
