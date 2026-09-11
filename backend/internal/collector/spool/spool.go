package spool

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	spoolFileName          = "spool.log"
	offsetFileName         = "spool.offset"
	legacyBackupFileName   = "spool.log.v1.backup"
	migrationTempFileName  = "spool.log.v2.migrating"
	migrationMarkerName    = "spool.migrating"
	quarantineFileName     = "spool.quarantine.log"
	recordMagic            = "SPL2"
	recordVersion          = uint8(2)
	recordHeaderSize       = 16
	legacyHeaderSize       = 4
	defaultMaxPayloadBytes = int64(16 * 1024 * 1024)

	// headerSizeBytes remains package-local compatibility for older tests.
	headerSizeBytes = recordHeaderSize
)

var castagnoliTable = crc32.MakeTable(crc32.Castagnoli)

var (
	// ErrPayloadTooLarge indicates one payload cannot fit inside the bounded spool.
	ErrPayloadTooLarge = errors.New("spool payload exceeds max size")
	// ErrCorruptSegment indicates an unread record was torn or failed validation.
	ErrCorruptSegment = errors.New("spool unread record is truncated or corrupt")
	// ErrClosed indicates the spool has already been closed.
	ErrClosed            = errors.New("spool is closed")
	ErrMigrationCapacity = errors.New("spool v2 migration needs more configured capacity; v1 data preserved")
)

// FaultPoint identifies a crash boundary used by deterministic fault tests.
type FaultPoint string

const (
	FaultBeforeRecordWrite    FaultPoint = "before_record_write"
	FaultAfterRecordWrite     FaultPoint = "after_record_write"
	FaultBeforeDataSync       FaultPoint = "before_data_sync"
	FaultAfterDataSync        FaultPoint = "after_data_sync"
	FaultBeforeCursorPersist  FaultPoint = "before_cursor_persist"
	FaultAfterCursorPersist   FaultPoint = "after_cursor_persist"
	FaultBeforeMigrationSwap  FaultPoint = "before_migration_swap"
	FaultAfterMigrationBackup FaultPoint = "after_migration_backup"
)

// Snapshot captures bounded backlog and recovery state for metrics/UI surfaces.
type Snapshot struct {
	FormatVersion        uint8  `json:"format_version"`
	BacklogBytes         int64  `json:"backlog_bytes"`
	FileSizeBytes        int64  `json:"file_size_bytes"`
	MaxBytes             int64  `json:"max_bytes"`
	MaxPayloadBytes      int64  `json:"max_payload_bytes"`
	Offset               int64  `json:"offset"`
	EvictedRecords       uint64 `json:"evicted_records"`
	CorruptionRecoveries uint64 `json:"corruption_recoveries"`
	ChecksumFailures     uint64 `json:"checksum_failures"`
	TornTailRecoveries   uint64 `json:"torn_tail_recoveries"`
	Migrations           uint64 `json:"migrations"`
	FsyncFailures        uint64 `json:"fsync_failures"`
	QuarantinedRecords   uint64 `json:"quarantined_records"`
	DiscardedRecords     uint64 `json:"discarded_records"`
	LastRecoveryReason   string `json:"last_recovery_reason,omitempty"`
}

// Options controls spool bounds and deterministic fault injection. Sync interval
// fields are retained for configuration compatibility; v2 always syncs a record
// before Enqueue returns and always persists a committed cursor synchronously.
type Options struct {
	DataSyncInterval   time.Duration
	OffsetSyncInterval time.Duration
	MaxPayloadBytes    int64
	FaultInjector      func(FaultPoint) error
}

// Spool is a bounded, append-only v2 write-ahead log. A returned Enqueue means
// the complete record is on stable storage and may safely be sent.
type Spool struct {
	dir             string
	maxBytes        int64
	maxPayloadBytes int64
	options         Options
	offsetPath      string
	mu              sync.Mutex
	file            *os.File
	offset          int64

	evictedRecords       uint64
	corruptionRecoveries uint64
	checksumFailures     uint64
	tornTailRecoveries   uint64
	migrations           uint64
	fsyncFailures        uint64
	quarantinedRecords   uint64
	discardedRecords     uint64
	lastRecoveryReason   string
}

// New creates a spool in the given directory.
func New(dir string, maxBytes int64) (*Spool, error) {
	return NewWithOptions(dir, maxBytes, Options{})
}

// NewWithOptions opens a v2 spool and crash-safely migrates unread v1 records.
func NewWithOptions(dir string, maxBytes int64, options Options) (*Spool, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("spool dir required")
	}
	if maxBytes <= 0 {
		maxBytes = 128 * 1024 * 1024
	}
	maxPayloadBytes := options.MaxPayloadBytes
	if maxPayloadBytes <= 0 {
		maxPayloadBytes = defaultMaxPayloadBytes
	}
	if maximum := maxBytes - recordHeaderSize; maxPayloadBytes > maximum {
		maxPayloadBytes = maximum
	}
	if maxPayloadBytes < 0 {
		maxPayloadBytes = 0
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create spool directory: %w", err)
	}
	s := &Spool{
		dir:             dir,
		maxBytes:        maxBytes,
		maxPayloadBytes: maxPayloadBytes,
		options:         options,
		offsetPath:      filepath.Join(dir, offsetFileName),
	}
	if err := s.recoverInterruptedMigration(); err != nil {
		return nil, err
	}
	if err := s.openOrMigrate(); err != nil {
		if s.file != nil {
			_ = s.file.Close()
		}
		return nil, err
	}
	return s, nil
}

// Enqueue appends and synchronizes a complete v2 record before returning.
func (s *Spool) Enqueue(payload []byte) error {
	if int64(len(payload)) > s.maxPayloadBytes {
		return ErrPayloadTooLarge
	}
	record := encodeRecord(payload)
	if int64(len(record)) > s.maxBytes {
		return ErrPayloadTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return ErrClosed
	}
	if err := s.compactLocked(int64(len(record))); err != nil {
		return err
	}
	if err := s.inject(FaultBeforeRecordWrite); err != nil {
		return err
	}
	start, err := s.file.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("seek spool end: %w", err)
	}
	if err := writeFull(s.file, record); err != nil {
		s.rollbackAppendLocked(start)
		return fmt.Errorf("append spool record: %w", err)
	}
	if err := s.inject(FaultAfterRecordWrite); err != nil {
		return err
	}
	if err := s.inject(FaultBeforeDataSync); err != nil {
		return err
	}
	if err := s.syncFile(s.file); err != nil {
		s.rollbackAppendLocked(start)
		return fmt.Errorf("sync spool record: %w", err)
	}
	if err := s.inject(FaultAfterDataSync); err != nil {
		return err
	}
	return nil
}

// Next returns the next payload without advancing a valid record's cursor.
func (s *Spool) Next() ([]byte, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil, s.offset, ErrClosed
	}

	stat, err := s.file.Stat()
	if err != nil {
		return nil, s.offset, err
	}
	if s.offset >= stat.Size() {
		return nil, s.offset, nil
	}

	header, payloadLength, nextOffset, err := s.readHeaderLocked(s.offset, stat.Size())
	if err != nil {
		return nil, s.offset, err
	}
	payload := make([]byte, payloadLength)
	if payloadLength > 0 {
		if _, err := s.file.ReadAt(payload, s.offset+recordHeaderSize); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, s.offset, s.recoverTornTailLocked("truncated_payload")
			}
			return nil, s.offset, err
		}
	}
	if recordChecksum(header, payload) != binary.LittleEndian.Uint32(header[12:16]) {
		s.checksumFailures++
		s.recordRecoveryLocked("checksum_mismatch")
		raw := append(append([]byte(nil), header...), payload...)
		if err := s.quarantineLocked(raw); err != nil {
			return nil, s.offset, err
		}
		if err := s.persistCursorLocked(nextOffset); err != nil {
			return nil, s.offset, err
		}
		s.offset = nextOffset
		s.quarantinedRecords++
		s.discardedRecords++
		return nil, s.offset, ErrCorruptSegment
	}
	return payload, nextOffset, nil
}

// Commit durably advances the cursor after the controller has acknowledged a batch.
func (s *Spool) Commit(nextOffset int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return ErrClosed
	}
	if nextOffset <= s.offset {
		return nil
	}
	stat, err := s.file.Stat()
	if err != nil {
		return err
	}
	if nextOffset > stat.Size() || !s.recordBoundaryLocked(nextOffset, stat.Size()) {
		return fmt.Errorf("invalid spool cursor %d", nextOffset)
	}
	if err := s.persistCursorLocked(nextOffset); err != nil {
		return err
	}
	s.offset = nextOffset
	return nil
}

// Stats returns current backlog and file size in bytes.
func (s *Spool) Stats() (backlogBytes int64, fileSizeBytes int64) {
	snapshot := s.Snapshot()
	return snapshot.BacklogBytes, snapshot.FileSizeBytes
}

// Snapshot returns spool backlog plus durability/recovery counters.
func (s *Spool) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := Snapshot{
		FormatVersion:        recordVersion,
		MaxBytes:             s.maxBytes,
		MaxPayloadBytes:      s.maxPayloadBytes,
		Offset:               s.offset,
		EvictedRecords:       s.evictedRecords,
		CorruptionRecoveries: s.corruptionRecoveries,
		ChecksumFailures:     s.checksumFailures,
		TornTailRecoveries:   s.tornTailRecoveries,
		Migrations:           s.migrations,
		FsyncFailures:        s.fsyncFailures,
		QuarantinedRecords:   s.quarantinedRecords,
		DiscardedRecords:     s.discardedRecords,
		LastRecoveryReason:   s.lastRecoveryReason,
	}
	if s.file == nil {
		return snapshot
	}
	stat, err := s.file.Stat()
	if err != nil {
		return snapshot
	}
	snapshot.FileSizeBytes = stat.Size()
	snapshot.BacklogBytes = stat.Size() - s.offset
	if snapshot.BacklogBytes < 0 {
		snapshot.BacklogBytes = 0
	}
	return snapshot
}

// Close flushes the log and releases its file descriptor.
func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	if err := s.syncFile(s.file); err != nil {
		return err
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *Spool) openOrMigrate() error {
	path := filepath.Join(s.dir, spoolFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open spool: %w", err)
	}
	s.file = file
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() > 0 && !fileStartsWithV2(file) {
		if err := file.Close(); err != nil {
			return err
		}
		s.file = nil
		if err := s.migrateV1(path); err != nil {
			return err
		}
		file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		s.file = file
		stat, err = file.Stat()
		if err != nil {
			return err
		}
	}

	validEnd, structuralOffset, reason, err := s.scanLocked(stat.Size())
	if err != nil {
		return err
	}
	if reason != "" && structuralOffset < 0 {
		if err := s.file.Truncate(validEnd); err != nil {
			return fmt.Errorf("truncate torn spool tail: %w", err)
		}
		if err := s.syncFile(s.file); err != nil {
			return err
		}
		s.tornTailRecoveries++
		s.recordRecoveryLocked(reason)
		stat, err = s.file.Stat()
		if err != nil {
			return err
		}
	}

	offset, ok := readOffset(s.offsetPath)
	if !ok || offset < 0 || offset > stat.Size() || !s.recordBoundaryLocked(offset, stat.Size()) {
		offset = 0
		if err := s.persistCursorLocked(offset); err != nil {
			return err
		}
	}
	s.offset = offset
	return nil
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
	kept, evicted, discarded, reason := trimV2ToFit(backlog, s.maxBytes-requiredBytes, s.maxPayloadBytes)
	if reason != "" {
		s.recordRecoveryLocked(reason)
	}
	if evicted > 0 {
		s.evictedRecords += uint64(evicted)
	}
	if discarded > 0 {
		s.discardedRecords += uint64(discarded)
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

func trimV2ToFit(data []byte, budget int64, maxPayload int64) ([]byte, int, int, string) {
	if len(data) == 0 || budget <= 0 {
		return nil, countCompleteRecords(data, maxPayload), 0, ""
	}
	ends := make([]int, 0, 16)
	validEnd := 0
	reason := ""
	for pos := 0; pos < len(data); {
		if len(data)-pos < recordHeaderSize {
			reason = "truncated_header"
			break
		}
		header := data[pos : pos+recordHeaderSize]
		length, ok := decodeHeader(header, maxPayload)
		if !ok {
			reason = "invalid_record_header"
			break
		}
		end := pos + recordHeaderSize + int(length)
		if end > len(data) {
			reason = "truncated_payload"
			break
		}
		ends = append(ends, end)
		validEnd = end
		pos = end
	}
	if validEnd == 0 {
		discarded := 0
		if len(data) > 0 && reason != "" {
			discarded = 1
		}
		return nil, 0, discarded, reason
	}
	valid := data[:validEnd]
	if int64(len(valid)) <= budget {
		return append([]byte(nil), valid...), 0, boolInt(validEnd < len(data)), reason
	}
	start := 0
	evicted := 0
	for _, end := range ends {
		if int64(validEnd-start) <= budget {
			break
		}
		start = end
		evicted++
	}
	if start >= validEnd {
		return nil, len(ends), boolInt(validEnd < len(data)), reason
	}
	return append([]byte(nil), valid[start:validEnd]...), evicted, boolInt(validEnd < len(data)), reason
}

func countCompleteRecords(data []byte, maxPayload int64) int {
	count := 0
	for pos := 0; pos+recordHeaderSize <= len(data); {
		length, ok := decodeHeader(data[pos:pos+recordHeaderSize], maxPayload)
		if !ok {
			break
		}
		end := pos + recordHeaderSize + int(length)
		if end > len(data) {
			break
		}
		count++
		pos = end
	}
	return count
}

func (s *Spool) rewriteLocked(backlog []byte) error {
	oldPath := filepath.Join(s.dir, spoolFileName)
	tmpPath := oldPath + ".rewrite"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}
	if err := writeFull(tmpFile, backlog); err != nil {
		cleanup()
		return err
	}
	if err := s.syncFile(tmpFile); err != nil {
		cleanup()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	// Persisting zero first can only cause duplicate replay if the swap fails.
	if err := s.persistCursorLocked(0); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, oldPath); err != nil {
		return err
	}
	// Once the pathname changes, never append to the old unlinked inode even if
	// the following directory sync/open fails. Recovery must reopen the journal.
	oldFile := s.file
	s.file = nil
	if oldFile != nil {
		_ = oldFile.Close()
	}
	if err := s.syncDirectory(); err != nil {
		return err
	}
	newFile, err := os.OpenFile(oldPath, os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	s.file = newFile
	s.offset = 0
	return nil
}

func (s *Spool) readHeaderLocked(offset, fileSize int64) ([]byte, int64, int64, error) {
	if fileSize-offset < recordHeaderSize {
		return nil, 0, offset, s.recoverTornTailLocked("truncated_header")
	}
	header := make([]byte, recordHeaderSize)
	if _, err := s.file.ReadAt(header, offset); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, 0, offset, s.recoverTornTailLocked("truncated_header")
		}
		return nil, 0, offset, err
	}
	length, ok := decodeHeader(header, s.maxPayloadBytes)
	if !ok {
		return nil, 0, offset, s.recoverStructuralCorruptionLocked("invalid_record_header", fileSize)
	}
	nextOffset := offset + recordHeaderSize + length
	if nextOffset > fileSize {
		return nil, 0, offset, s.recoverTornTailLocked("truncated_payload")
	}
	return header, length, nextOffset, nil
}

func (s *Spool) recoverTornTailLocked(reason string) error {
	if err := s.file.Truncate(s.offset); err != nil {
		return err
	}
	if err := s.syncFile(s.file); err != nil {
		return err
	}
	s.tornTailRecoveries++
	s.recordRecoveryLocked(reason)
	return ErrCorruptSegment
}

func (s *Spool) recoverStructuralCorruptionLocked(reason string, fileSize int64) error {
	raw, err := s.readUnreadLocked(fileSize)
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		if err := s.quarantineLocked(raw); err != nil {
			return err
		}
		s.quarantinedRecords++
		s.discardedRecords++
	}
	if err := s.file.Truncate(s.offset); err != nil {
		return err
	}
	if err := s.syncFile(s.file); err != nil {
		return err
	}
	s.recordRecoveryLocked(reason)
	return ErrCorruptSegment
}

func (s *Spool) recordRecoveryLocked(reason string) {
	s.corruptionRecoveries++
	s.lastRecoveryReason = reason
}

func (s *Spool) quarantineLocked(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	path := filepath.Join(s.dir, quarantineFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open spool quarantine: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	entrySize := int64(8 + len(raw))
	if stat.Size()+entrySize > s.maxBytes {
		if err := file.Truncate(0); err != nil {
			return err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	var prefix [8]byte
	binary.LittleEndian.PutUint64(prefix[:], uint64(len(raw))) // #nosec G115 -- raw is bounded by maxBytes.
	if err := writeFull(file, prefix[:]); err != nil {
		return err
	}
	if err := writeFull(file, raw); err != nil {
		return err
	}
	return s.syncFile(file)
}

func (s *Spool) rollbackAppendLocked(start int64) {
	if s.file == nil || start < 0 {
		return
	}
	if err := s.file.Truncate(start); err != nil {
		return
	}
	_ = s.syncFile(s.file)
}

func (s *Spool) scanLocked(fileSize int64) (validEnd, structuralOffset int64, reason string, err error) {
	structuralOffset = -1
	for pos := int64(0); pos < fileSize; {
		if fileSize-pos < recordHeaderSize {
			return pos, -1, "truncated_header", nil
		}
		header := make([]byte, recordHeaderSize)
		if _, err := s.file.ReadAt(header, pos); err != nil {
			return pos, -1, "", err
		}
		length, ok := decodeHeader(header, s.maxPayloadBytes)
		if !ok {
			return pos, pos, "invalid_record_header", nil
		}
		end := pos + recordHeaderSize + length
		if end > fileSize {
			return pos, -1, "truncated_payload", nil
		}
		pos = end
		validEnd = pos
	}
	return validEnd, -1, "", nil
}

func (s *Spool) recordBoundaryLocked(target, fileSize int64) bool {
	if target == 0 {
		return true
	}
	for pos := int64(0); pos < fileSize; {
		if fileSize-pos < recordHeaderSize {
			return false
		}
		header := make([]byte, recordHeaderSize)
		if _, err := s.file.ReadAt(header, pos); err != nil {
			return false
		}
		length, ok := decodeHeader(header, s.maxPayloadBytes)
		if !ok {
			return false
		}
		pos += recordHeaderSize + length
		if pos == target {
			return true
		}
		if pos > target {
			return false
		}
	}
	return false
}

func (s *Spool) persistCursorLocked(offset int64) error {
	if err := s.inject(FaultBeforeCursorPersist); err != nil {
		return err
	}
	if err := atomicWriteFile(s.offsetPath, []byte(strconv.FormatInt(offset, 10)+"\n"), 0o600, s.syncFile, s.syncDirectory); err != nil {
		return fmt.Errorf("persist spool cursor: %w", err)
	}
	if err := s.inject(FaultAfterCursorPersist); err != nil {
		return err
	}
	return nil
}

func (s *Spool) migrateV1(path string) error {
	legacy, err := os.Open(path)
	if err != nil {
		return err
	}
	stat, err := legacy.Stat()
	if err != nil {
		_ = legacy.Close()
		return err
	}
	offset, ok := readOffset(s.offsetPath)
	if !ok || offset < 0 || offset > stat.Size() {
		offset = 0
	}
	if stat.Size()-offset > s.maxBytes {
		_ = legacy.Close()
		return ErrMigrationCapacity
	}
	unread := make([]byte, stat.Size()-offset)
	if len(unread) > 0 {
		if _, err := legacy.ReadAt(unread, offset); err != nil && !errors.Is(err, io.EOF) {
			_ = legacy.Close()
			return err
		}
	}
	if err := legacy.Close(); err != nil {
		return err
	}

	payloads, tornReason, err := decodeLegacyRecords(unread, s.maxPayloadBytes)
	if err != nil {
		return fmt.Errorf("decode legacy spool: %w", err)
	}
	var migratedBytes int64
	for _, payload := range payloads {
		migratedBytes += int64(recordHeaderSize + len(payload))
	}
	if migratedBytes > s.maxBytes {
		return ErrMigrationCapacity
	}
	if tornReason != "" {
		s.tornTailRecoveries++
		s.recordRecoveryLocked("v1_" + tornReason)
	}
	tmpPath := filepath.Join(s.dir, migrationTempFileName)
	_ = os.Remove(tmpPath)
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, payload := range payloads {
		if err := writeFull(tmp, encodeRecord(payload)); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := s.syncFile(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	markerPath := filepath.Join(s.dir, migrationMarkerName)
	if err := atomicWriteFile(markerPath, []byte("v1-to-v2\n"), 0o600, s.syncFile, s.syncDirectory); err != nil {
		return err
	}
	if err := s.inject(FaultBeforeMigrationSwap); err != nil {
		return err
	}
	backupPath := filepath.Join(s.dir, legacyBackupFileName)
	_ = os.Remove(backupPath)
	if err := os.Rename(path, backupPath); err != nil {
		return err
	}
	if err := s.syncDirectory(); err != nil {
		return err
	}
	if err := s.inject(FaultAfterMigrationBackup); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := s.syncDirectory(); err != nil {
		return err
	}
	if err := s.persistCursorLocked(0); err != nil {
		return err
	}
	if err := removeMigrationArtifacts(s.dir); err != nil {
		return err
	}
	s.migrations++
	return nil
}

func (s *Spool) recoverInterruptedMigration() error {
	path := filepath.Join(s.dir, spoolFileName)
	backupPath := filepath.Join(s.dir, legacyBackupFileName)
	tmpPath := filepath.Join(s.dir, migrationTempFileName)
	markerPath := filepath.Join(s.dir, migrationMarkerName)
	backupExists := fileExists(backupPath)
	tmpExists := fileExists(tmpPath)
	markerExists := fileExists(markerPath)
	spoolExists := fileExists(path)
	if !backupExists && !tmpExists && !markerExists {
		return nil
	}

	if !spoolExists {
		switch {
		case tmpExists && backupExists:
			if err := os.Rename(tmpPath, path); err != nil {
				return fmt.Errorf("finish spool migration swap: %w", err)
			}
			if err := s.syncDirectory(); err != nil {
				return err
			}
			if err := s.persistCursorLocked(0); err != nil {
				return err
			}
			if err := removeMigrationArtifacts(s.dir); err != nil {
				return err
			}
			s.migrations++
			return nil
		case backupExists:
			if err := os.Rename(backupPath, path); err != nil {
				return fmt.Errorf("restore legacy spool backup: %w", err)
			}
			if err := s.syncDirectory(); err != nil {
				return err
			}
			_ = os.Remove(tmpPath)
			_ = os.Remove(markerPath)
			return s.syncDirectory()
		case tmpExists:
			if err := os.Rename(tmpPath, path); err != nil {
				return err
			}
			if err := s.syncDirectory(); err != nil {
				return err
			}
			if err := s.persistCursorLocked(0); err != nil {
				return err
			}
			_ = os.Remove(markerPath)
			s.migrations++
			return s.syncDirectory()
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	isV2 := fileStartsWithV2(file)
	_ = file.Close()
	if isV2 && (backupExists || markerExists) {
		if err := s.persistCursorLocked(0); err != nil {
			return err
		}
		if err := removeMigrationArtifacts(s.dir); err != nil {
			return err
		}
		s.migrations++
		return nil
	}
	// The original v1 log is still authoritative. Remove only derived artifacts.
	_ = os.Remove(tmpPath)
	_ = os.Remove(markerPath)
	if backupExists {
		_ = os.Remove(backupPath)
	}
	return s.syncDirectory()
}

func removeMigrationArtifacts(dir string) error {
	for _, name := range []string{legacyBackupFileName, migrationTempFileName, migrationMarkerName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func decodeLegacyRecords(data []byte, maxPayload int64) ([][]byte, string, error) {
	payloads := make([][]byte, 0, 16)
	for pos := 0; pos < len(data); {
		if len(data)-pos < legacyHeaderSize {
			return payloads, "truncated_header", nil
		}
		length := int64(binary.LittleEndian.Uint32(data[pos : pos+legacyHeaderSize]))
		if length > maxPayload {
			return nil, "", fmt.Errorf("invalid v1 record length %d", length)
		}
		end := pos + legacyHeaderSize + int(length)
		if end > len(data) {
			return payloads, "truncated_payload", nil
		}
		payloads = append(payloads, append([]byte(nil), data[pos+legacyHeaderSize:end]...))
		pos = end
	}
	return payloads, "", nil
}

func encodeRecord(payload []byte) []byte {
	record := make([]byte, recordHeaderSize+len(payload))
	copy(record[:4], recordMagic)
	record[4] = recordVersion
	record[5] = 0
	binary.LittleEndian.PutUint16(record[6:8], recordHeaderSize)
	binary.LittleEndian.PutUint32(record[8:12], uint32(len(payload))) // #nosec G115 -- caller enforces a uint32-sized bound.
	copy(record[recordHeaderSize:], payload)
	binary.LittleEndian.PutUint32(record[12:16], recordChecksum(record[:recordHeaderSize], payload))
	return record
}

func decodeHeader(header []byte, maxPayload int64) (int64, bool) {
	if len(header) != recordHeaderSize || string(header[:4]) != recordMagic || header[4] != recordVersion {
		return 0, false
	}
	if binary.LittleEndian.Uint16(header[6:8]) != recordHeaderSize {
		return 0, false
	}
	length := int64(binary.LittleEndian.Uint32(header[8:12]))
	return length, length <= maxPayload
}

func recordChecksum(header, payload []byte) uint32 {
	checksum := crc32.Update(0, castagnoliTable, header[:12])
	return crc32.Update(checksum, castagnoliTable, payload)
}

func readOffset(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return offset, err == nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode, syncFile func(*os.File) error, syncDir func() error) error {
	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmpPath)
	}
	if err := writeFull(file, data); err != nil {
		cleanup()
		return err
	}
	if err := syncFile(file); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDir()
}

func (s *Spool) syncFile(file *os.File) error {
	if err := file.Sync(); err != nil {
		s.fsyncFailures++
		return err
	}
	return nil
}

func (s *Spool) syncDirectory() error {
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		s.fsyncFailures++
		return err
	}
	return nil
}

func (s *Spool) inject(point FaultPoint) error {
	if s.options.FaultInjector == nil {
		return nil
	}
	if err := s.options.FaultInjector(point); err != nil {
		return fmt.Errorf("spool fault at %s: %w", point, err)
	}
	return nil
}

func fileStartsWithV2(file *os.File) bool {
	var magic [4]byte
	if _, err := file.ReadAt(magic[:], 0); err != nil {
		return false
	}
	return string(magic[:]) == recordMagic
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
