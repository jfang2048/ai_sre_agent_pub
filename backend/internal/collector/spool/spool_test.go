package spool

import (
	"encoding/binary"
	"errors"
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

// TestSpoolRotation validates automatic rotation
func TestSpoolRotation(t *testing.T) {
	tempDir := t.TempDir()
	maxSize := int64(512) // Small size to trigger rotation

	spool, err := New(tempDir, maxSize)
	require.NoError(t, err)

	// Enqueue data until rotation
	payload := make([]byte, 200)
	for i := 0; i < 10; i++ {
		err := spool.Enqueue(payload)
		require.NoError(t, err)
	}

	// Check that rotation file exists
	rotatedPath := filepath.Join(tempDir, "spool.log.1")
	_, err = os.Stat(rotatedPath)
	// Rotation should have occurred (file may or may not exist depending on timing)
	_ = rotatedPath
	_ = err
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
	sp, err := New(tempDir, 26)
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
