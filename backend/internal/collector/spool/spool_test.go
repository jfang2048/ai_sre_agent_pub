package spool

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewSpool validates spool initialization
func TestNewSpool(t *testing.T) {
	testCases := []struct {
		name        string
		dir         string
		maxBytes    int64
		expectError bool
	}{
		{
			name:        "valid spool",
			dir:         "test-spool",
			maxBytes:    1024,
			expectError: false,
		},
		{
			name:        "empty dir uses default",
			dir:         "",
			maxBytes:    1024,
			expectError: true,
		},
		{
			name:        "zero max bytes uses default",
			dir:         "test-spool-default",
			maxBytes:    0,
			expectError: false,
		},
		{
			name:        "negative max bytes uses default",
			dir:         "test-spool-negative",
			maxBytes:    -100,
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			if tc.dir != "" {
				tc.dir = filepath.Join(tempDir, tc.dir)
			}

			spool, err := New(tc.dir, tc.maxBytes)

			if tc.expectError {
				require.Error(t, err)
				require.Nil(t, spool)
			} else {
				require.NoError(t, err)
				require.NotNil(t, spool)

				// Verify directory was created
				if tc.dir != "" {
					_, err := os.Stat(tc.dir)
					require.NoError(t, err, "spool directory should exist")
				}
			}
		})
	}
}

// TestSpoolEnqueue validates enqueue operations
func TestSpoolEnqueue(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	testCases := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "small payload",
			payload: []byte("test data"),
			wantErr: false,
		},
		{
			name:    "empty payload",
			payload: []byte{},
			wantErr: false,
		},
		{
			name:    "large payload",
			payload: make([]byte, 10*1024),
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := spool.Enqueue(tc.payload)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}

	// Verify data was written
	backlog, size := spool.Stats()
	require.Greater(t, size, int64(0), "file should have data")
	require.Greater(t, backlog, int64(0), "should have backlog")
}

// TestSpoolNext validates Next() operations
func TestSpoolNext(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Enqueue test data
	payload1 := []byte("first payload")
	payload2 := []byte("second payload")
	payload3 := []byte("third payload")

	err = spool.Enqueue(payload1)
	require.NoError(t, err)
	err = spool.Enqueue(payload2)
	require.NoError(t, err)
	err = spool.Enqueue(payload3)
	require.NoError(t, err)

	// Read first payload
	data, offset, err := spool.Next()
	require.NoError(t, err)
	require.Equal(t, payload1, data)
	require.Greater(t, offset, int64(0))

	// Read again without committing - should return same data
	data, offset, err = spool.Next()
	require.NoError(t, err)
	require.Equal(t, payload1, data, "Next() should return same data until committed")

	// Commit and read again
	err = spool.Commit(offset)
	require.NoError(t, err)

	data, offset, err = spool.Next()
	require.NoError(t, err)
	require.Equal(t, payload2, data, "After commit, should get next payload")
}

// TestSpoolNextEmpty validates Next() on empty spool
func TestSpoolNextEmpty(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Next on empty spool should return nil, offset, nil
	data, offset, err := spool.Next()
	require.NoError(t, err)
	require.Nil(t, data)
	require.Equal(t, int64(0), offset)
}

// TestSpoolCommit validates commit operations
func TestSpoolCommit(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	payload := []byte("test payload")
	err = spool.Enqueue(payload)
	require.NoError(t, err)

	// Read payload
	data, offset, err := spool.Next()
	require.NoError(t, err)
	require.Equal(t, payload, data)

	// Commit
	err = spool.Commit(offset)
	require.NoError(t, err)

	// Next should now return nil (empty)
	data, _, err = spool.Next()
	require.NoError(t, err)
	require.Nil(t, data)
}

// TestSpoolCommitLowerOffset validates committing lower offset
func TestSpoolCommitLowerOffset(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Enqueue data
	err = spool.Enqueue([]byte("test"))
	require.NoError(t, err)

	// Commit with lower offset should be no-op
	err = spool.Commit(0)
	require.NoError(t, err)

	// Should still have data
	data, _, err := spool.Next()
	require.NoError(t, err)
	require.NotNil(t, data)
}

// TestSpoolStats validates stats reporting
func TestSpoolStats(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Initial stats
	backlog, size := spool.Stats()
	require.Equal(t, int64(0), backlog)
	require.Equal(t, int64(0), size)

	// Enqueue data
	payload := []byte("test data for stats")
	err = spool.Enqueue(payload)
	require.NoError(t, err)

	// Stats after enqueue
	backlog, size = spool.Stats()
	require.Greater(t, size, int64(0))
	require.Greater(t, backlog, int64(0))

	// Read and commit
	_, offset, err := spool.Next()
	require.NoError(t, err)
	err = spool.Commit(offset)
	require.NoError(t, err)

	// Stats after commit should show no backlog
	backlog, size = spool.Stats()
	require.Equal(t, int64(0), backlog)
	require.Greater(t, size, int64(0)) // File size unchanged
}

// TestSpoolRotation validates automatic bounded compaction.
func TestSpoolRotation(t *testing.T) {
	tempDir := t.TempDir()
	maxSize := int64(512)

	spool, err := New(tempDir, maxSize)
	require.NoError(t, err)

	// Enqueue data until compaction.
	payload := make([]byte, 200)
	for i := 0; i < 10; i++ {
		err := spool.Enqueue(payload)
		require.NoError(t, err)
	}

	snapshot := spool.Snapshot()
	require.LessOrEqual(t, snapshot.FileSizeBytes, maxSize)
	require.Greater(t, snapshot.EvictedRecords, uint64(0))
}

// TestSpoolPersistence validates data persists across reopen
func TestSpoolPersistence(t *testing.T) {
	tempDir := t.TempDir()

	// Create spool and enqueue data
	spool1, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	payload := []byte("persistent data")
	err = spool1.Enqueue(payload)
	require.NoError(t, err)

	// "Close" first spool (just let it go out of scope)
	spool1 = nil

	// Reopen spool
	spool2, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Should be able to read the data
	data, offset, err := spool2.Next()
	require.NoError(t, err)
	require.Equal(t, payload, data)

	// Commit
	err = spool2.Commit(offset)
	require.NoError(t, err)
}

// TestSpoolOffsetPersistence validates offset persistence
func TestSpoolOffsetPersistence(t *testing.T) {
	tempDir := t.TempDir()

	// Create spool and enqueue data
	spool1, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	payload1 := []byte("first")
	payload2 := []byte("second")

	err = spool1.Enqueue(payload1)
	require.NoError(t, err)
	err = spool1.Enqueue(payload2)
	require.NoError(t, err)

	// Read and commit first
	_, offset1, err := spool1.Next()
	require.NoError(t, err)
	err = spool1.Commit(offset1)
	require.NoError(t, err)

	// Reopen spool
	spool2, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Should read second payload (offset persisted)
	data, _, err := spool2.Next()
	require.NoError(t, err)
	require.Equal(t, payload2, data)
}

// TestSpoolConcurrentAccess validates concurrent operations
func TestSpoolConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 10*1024*1024)
	require.NoError(t, err)

	const numGoroutines = 10
	const payloadsPerGoroutine = 20
	var wg sync.WaitGroup

	// Concurrent enqueues
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < payloadsPerGoroutine; j++ {
				payload := []byte{byte(id), byte(j)}
				err := spool.Enqueue(payload)
				require.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify some data was written
	backlog, size := spool.Stats()
	require.Greater(t, size, int64(0))
	require.Greater(t, backlog, int64(0))
}

// TestSpoolConcurrentReadWrite validates concurrent reads and writes
func TestSpoolConcurrentReadWrite(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 10*1024*1024)
	require.NoError(t, err)

	// Enqueue some initial data
	for i := 0; i < 10; i++ {
		payload := []byte{byte(i)}
		err := spool.Enqueue(payload)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	stopWrite := make(chan bool)
	stopRead := make(chan bool)

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 10
		for {
			select {
			case <-stopWrite:
				return
			default:
				payload := []byte{byte(i % 256)}
				err := spool.Enqueue(payload)
				require.NoError(t, err)
				i++
			}
		}
	}()

	// Reader goroutine
	wg.Add(1)
	readCount := 0
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopRead:
				return
			default:
				data, offset, err := spool.Next()
				require.NoError(t, err)
				if data != nil {
					err = spool.Commit(offset)
					require.NoError(t, err)
					readCount++
				}
			}
		}
	}()

	// Let them run briefly
	time.Sleep(10 * time.Millisecond)
	close(stopWrite)
	close(stopRead)
	wg.Wait()

	require.Greater(t, readCount, 0, "should have read some data")
}

// TestSpoolEmptyCommit validates commit on empty spool
func TestSpoolEmptyCommit(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Commit on empty spool should not error
	err = spool.Commit(0)
	require.NoError(t, err)
}

// TestSpoolMultipleCommits validates multiple commits
func TestSpoolMultipleCommits(t *testing.T) {
	tempDir := t.TempDir()
	spool, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Enqueue multiple payloads
	payloads := [][]byte{
		[]byte("first"),
		[]byte("second"),
		[]byte("third"),
		[]byte("fourth"),
	}

	for _, payload := range payloads {
		err := spool.Enqueue(payload)
		require.NoError(t, err)
	}

	// Read and commit all
	for i := 0; i < len(payloads); i++ {
		data, offset, err := spool.Next()
		require.NoError(t, err)
		require.Equal(t, payloads[i], data)
		err = spool.Commit(offset)
		require.NoError(t, err)
	}

	// Should be empty now
	data, _, err := spool.Next()
	require.NoError(t, err)
	require.Nil(t, data)
}

// TestSpoolRecoveryAfterCrash validates recovery after simulated crash
func TestSpoolRecoveryAfterCrash(t *testing.T) {
	tempDir := t.TempDir()

	// Create spool and add data
	spool1, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	err = spool1.Enqueue([]byte("data1"))
	require.NoError(t, err)
	err = spool1.Enqueue([]byte("data2"))
	require.NoError(t, err)

	// Read but don't commit (simulate crash)
	data, offset, err := spool1.Next()
	require.NoError(t, err)
	require.NotNil(t, data)

	// "Crash" - reopen spool
	spool2, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	// Should still be able to read first payload (not committed)
	data, offset, err = spool2.Next()
	require.NoError(t, err)
	require.NotNil(t, data)

	// Now commit and verify next
	err = spool2.Commit(offset)
	require.NoError(t, err)

	data, _, err = spool2.Next()
	require.NoError(t, err)
	require.NotNil(t, data)
}

func TestSpoolRecoversFromTruncatedTail(t *testing.T) {
	tempDir := t.TempDir()
	sp, err := New(tempDir, 1024*1024)
	require.NoError(t, err)

	require.NoError(t, sp.Enqueue([]byte("healthy")))
	data, offset, err := sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("healthy"), data)
	require.NoError(t, sp.Commit(offset))

	filePath := filepath.Join(tempDir, spoolFileName)
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	defer file.Close()

	header := make([]byte, headerSizeBytes)
	binary.LittleEndian.PutUint32(header, 16)
	_, err = file.Write(header[:2])
	require.NoError(t, err)

	data, _, err = sp.Next()
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCorruptSegment))
	require.Nil(t, data)

	data, _, err = sp.Next()
	require.NoError(t, err)
	require.Nil(t, data)

	snapshot := sp.Snapshot()
	require.Equal(t, uint64(1), snapshot.CorruptionRecoveries)
	require.Equal(t, "truncated_header", snapshot.LastRecoveryReason)
}

func TestSpoolEvictsOldestUnreadRecordsWhenFull(t *testing.T) {
	tempDir := t.TempDir()
	sp, err := New(tempDir, 48)
	require.NoError(t, err)

	require.NoError(t, sp.Enqueue([]byte("first")))
	require.NoError(t, sp.Enqueue([]byte("second")))
	require.NoError(t, sp.Enqueue([]byte("third")))

	data, offset, err := sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("second"), data)
	require.NoError(t, sp.Commit(offset))

	data, _, err = sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("third"), data)

	snapshot := sp.Snapshot()
	require.Equal(t, uint64(1), snapshot.EvictedRecords)
}

func TestSpoolV2RecordHasMagicVersionAndCRC32C(t *testing.T) {
	tempDir := t.TempDir()
	sp, err := New(tempDir, 1024*1024)
	require.NoError(t, err)
	require.NoError(t, sp.Enqueue([]byte("durable")))

	raw, err := os.ReadFile(filepath.Join(tempDir, spoolFileName))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(raw), recordHeaderSize)
	require.Equal(t, recordMagic, string(raw[:4]))
	require.Equal(t, recordVersion, raw[4])
	require.Equal(t, uint16(recordHeaderSize), binary.LittleEndian.Uint16(raw[6:8]))
	require.Equal(t, uint32(len("durable")), binary.LittleEndian.Uint32(raw[8:12]))
	require.Equal(t, binary.LittleEndian.Uint32(raw[12:16]), recordChecksum(raw[:recordHeaderSize], raw[recordHeaderSize:]))
}

func TestSpoolChecksumFailureQuarantinesOnlyCorruptRecord(t *testing.T) {
	tempDir := t.TempDir()
	sp, err := New(tempDir, 1024*1024)
	require.NoError(t, err)
	for _, payload := range [][]byte{[]byte("first"), []byte("second"), []byte("third")} {
		require.NoError(t, sp.Enqueue(payload))
	}

	first, firstEnd, err := sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("first"), first)
	require.NoError(t, sp.Commit(firstEnd))

	file, err := os.OpenFile(filepath.Join(tempDir, spoolFileName), os.O_RDWR, 0o600)
	require.NoError(t, err)
	_, err = file.WriteAt([]byte{'X'}, firstEnd+recordHeaderSize)
	require.NoError(t, err)
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())

	payload, _, err := sp.Next()
	require.ErrorIs(t, err, ErrCorruptSegment)
	require.Nil(t, payload)

	payload, _, err = sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("third"), payload)
	snapshot := sp.Snapshot()
	require.Equal(t, uint64(1), snapshot.ChecksumFailures)
	require.Equal(t, uint64(1), snapshot.QuarantinedRecords)
	require.Equal(t, uint64(1), snapshot.DiscardedRecords)
	quarantine, err := os.Stat(filepath.Join(tempDir, quarantineFileName))
	require.NoError(t, err)
	require.LessOrEqual(t, quarantine.Size(), snapshot.MaxBytes)
}

func TestSpoolMigratesOnlyUnreadV1Records(t *testing.T) {
	tempDir := t.TempDir()
	payloads := [][]byte{[]byte("acked"), []byte("pending-a"), []byte("pending-b")}
	firstEnd := writeLegacySpool(t, tempDir, payloads)
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, offsetFileName), []byte(fmt.Sprintf("%d", firstEnd)), 0o600))

	sp, err := New(tempDir, 1024*1024)
	require.NoError(t, err)
	require.Equal(t, uint64(1), sp.Snapshot().Migrations)
	for _, want := range payloads[1:] {
		got, next, err := sp.Next()
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.NoError(t, sp.Commit(next))
	}
	got, _, err := sp.Next()
	require.NoError(t, err)
	require.Nil(t, got)
	for _, name := range []string{legacyBackupFileName, migrationTempFileName, migrationMarkerName} {
		_, err := os.Stat(filepath.Join(tempDir, name))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestSpoolMigrationPreservesV1WhenV2WouldExceedBound(t *testing.T) {
	dir := t.TempDir()
	payloads := [][]byte{[]byte("pending1"), []byte("pending2")}
	writeLegacySpool(t, dir, payloads)
	path := filepath.Join(dir, spoolFileName)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	_, err = New(dir, 40)
	require.ErrorIs(t, err, ErrMigrationCapacity)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	sp, err := New(dir, 64)
	require.NoError(t, err)
	defer sp.Close()
	for _, expected := range payloads {
		payload, next, err := sp.Next()
		require.NoError(t, err)
		require.Equal(t, expected, payload)
		require.NoError(t, sp.Commit(next))
	}
}

func TestSpoolMigrationResumesAfterCrashBetweenBackupAndSwap(t *testing.T) {
	tempDir := t.TempDir()
	writeLegacySpool(t, tempDir, [][]byte{[]byte("still-owned")})
	crash := errors.New("simulated crash")
	_, err := NewWithOptions(tempDir, 1024*1024, Options{
		FaultInjector: func(point FaultPoint) error {
			if point == FaultAfterMigrationBackup {
				return crash
			}
			return nil
		},
	})
	require.ErrorIs(t, err, crash)
	require.FileExists(t, filepath.Join(tempDir, legacyBackupFileName))
	require.FileExists(t, filepath.Join(tempDir, migrationTempFileName))

	sp, err := New(tempDir, 1024*1024)
	require.NoError(t, err)
	payload, _, err := sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("still-owned"), payload)
	require.Equal(t, uint64(1), sp.Snapshot().Migrations)
}

func TestSpoolCursorFailureReplaysInsteadOfLosingAcknowledgedRecord(t *testing.T) {
	tempDir := t.TempDir()
	failCursor := false
	sp, err := NewWithOptions(tempDir, 1024*1024, Options{
		FaultInjector: func(point FaultPoint) error {
			if failCursor && point == FaultBeforeCursorPersist {
				return errors.New("cursor fsync crash")
			}
			return nil
		},
	})
	require.NoError(t, err)
	require.NoError(t, sp.Enqueue([]byte("acked-remains-owned")))
	payload, next, err := sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("acked-remains-owned"), payload)
	failCursor = true
	require.Error(t, sp.Commit(next))
	failCursor = false

	reopened, err := New(tempDir, 1024*1024)
	require.NoError(t, err)
	payload, _, err = reopened.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("acked-remains-owned"), payload)
}

func TestSpoolCrashBoundariesNeverExposePartialRecord(t *testing.T) {
	points := []FaultPoint{
		FaultBeforeRecordWrite,
		FaultAfterRecordWrite,
		FaultBeforeDataSync,
		FaultAfterDataSync,
	}
	for _, point := range points {
		t.Run(string(point), func(t *testing.T) {
			tempDir := t.TempDir()
			armed := false
			sp, err := NewWithOptions(tempDir, 1024*1024, Options{
				FaultInjector: func(current FaultPoint) error {
					if armed && current == point {
						return errors.New("simulated crash")
					}
					return nil
				},
			})
			require.NoError(t, err)
			armed = true
			require.Error(t, sp.Enqueue([]byte("identity-1")))

			reopened, err := New(tempDir, 1024*1024)
			require.NoError(t, err)
			payload, _, err := reopened.Next()
			require.NoError(t, err)
			if point == FaultBeforeRecordWrite {
				require.Nil(t, payload)
			} else {
				require.Equal(t, []byte("identity-1"), payload)
			}
		})
	}
}

func TestSpoolTenThousandUniqueRecordsAcrossRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping durability volume test in short mode")
	}
	tempDir := t.TempDir()
	const records = 10_000
	const maxBytes = int64(2 * 1024 * 1024)
	sp, err := New(tempDir, maxBytes)
	require.NoError(t, err)
	for i := 0; i < records; i++ {
		require.NoError(t, sp.Enqueue([]byte(fmt.Sprintf("epoch-a-%05d", i))))
	}
	require.LessOrEqual(t, sp.Snapshot().FileSizeBytes, maxBytes)
	require.NoError(t, sp.Close())

	seen := make(map[string]struct{}, records)
	for len(seen) < records {
		sp, err = New(tempDir, maxBytes)
		require.NoError(t, err)
		for i := 0; i < 137; i++ {
			payload, next, err := sp.Next()
			require.NoError(t, err)
			if payload == nil {
				break
			}
			identity := string(payload)
			_, duplicate := seen[identity]
			require.False(t, duplicate, "materialized identity twice: %s", identity)
			seen[identity] = struct{}{}
			require.NoError(t, sp.Commit(next))
		}
		require.LessOrEqual(t, sp.Snapshot().FileSizeBytes, maxBytes)
		require.NoError(t, sp.Close())
	}
	require.Len(t, seen, records)
}

func writeLegacySpool(t *testing.T, dir string, payloads [][]byte) int64 {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(dir, spoolFileName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	require.NoError(t, err)
	var firstEnd int64
	for index, payload := range payloads {
		var header [legacyHeaderSize]byte
		binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
		_, err = file.Write(header[:])
		require.NoError(t, err)
		_, err = file.Write(payload)
		require.NoError(t, err)
		if index == 0 {
			firstEnd = int64(legacyHeaderSize + len(payload))
		}
	}
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())
	return firstEnd
}
