package spool

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	spoolFileName   = "spool.log"
	offsetFileName  = "spool.offset"
	headerSizeBytes = 4
)

// Spool provides a minimal persistent buffer for telemetry batches.
type Spool struct {
	dir      string
	maxBytes int64
	mu       sync.Mutex
	file     *os.File
	offset   int64
}

// New creates a spool in the given directory.
func New(dir string, maxBytes int64) (*Spool, error) {
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
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	offset := readOffset(filepath.Join(dir, offsetFileName))
	return &Spool{dir: dir, maxBytes: maxBytes, file: file, offset: offset}, nil
}

// Enqueue appends a batch to the spool.
func (s *Spool) Enqueue(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.rotateIfNeeded(); err != nil {
		return err
	}

	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	header := make([]byte, headerSizeBytes)
	binary.LittleEndian.PutUint32(header, uint32(len(payload)))
	if _, err := s.file.Write(header); err != nil {
		return err
	}
	if _, err := s.file.Write(payload); err != nil {
		return err
	}
	return s.file.Sync()
}

// Next returns the next payload without advancing offset.
func (s *Spool) Next() ([]byte, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.file.Seek(s.offset, io.SeekStart); err != nil {
		return nil, s.offset, err
	}

	header := make([]byte, headerSizeBytes)
	n, err := io.ReadFull(s.file, header)
	if err != nil {
		if err == io.EOF || n == 0 {
			return nil, s.offset, nil
		}
		return nil, s.offset, err
	}

	length := int64(binary.LittleEndian.Uint32(header))
	if length <= 0 {
		return nil, s.offset, nil
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(s.file, payload); err != nil {
		return nil, s.offset, err
	}

	nextOffset := s.offset + headerSizeBytes + length
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
	return writeOffset(filepath.Join(s.dir, offsetFileName), s.offset)
}

// Stats returns current backlog and file size in bytes.
func (s *Spool) Stats() (backlogBytes int64, fileSizeBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stat, err := s.file.Stat()
	if err != nil {
		return 0, 0
	}
	fileSizeBytes = stat.Size()
	backlogBytes = fileSizeBytes - s.offset
	if backlogBytes < 0 {
		backlogBytes = 0
	}
	return backlogBytes, fileSizeBytes
}

func (s *Spool) rotateIfNeeded() error {
	stat, err := s.file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() <= s.maxBytes {
		return nil
	}

	oldPath := filepath.Join(s.dir, spoolFileName)
	rotated := filepath.Join(s.dir, "spool.log.1")
	_ = os.Remove(rotated)
	if err := os.Rename(oldPath, rotated); err != nil {
		return err
	}

	file, err := os.OpenFile(oldPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	s.file = file
	s.offset = 0
	return writeOffset(filepath.Join(s.dir, offsetFileName), s.offset)
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
