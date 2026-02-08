package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

type Store interface {
	LoadReports() ([]Report, error)
	SaveReport(report Report) error
	LoadActions() ([]ActionDecision, error)
	SaveAction(action ActionDecision) error
	UpdateAction(id, status, note string) error
}

type FileStore struct {
	dir         string
	reportsPath string
	actionsPath string
	logger      *zap.Logger
	mu          sync.Mutex
}

func NewFileStore(dir string, logger *zap.Logger) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("persist dir is empty")
	}
	if logger == nil {
		logger, _ = zap.NewProduction()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create persist dir: %w", err)
	}
	return &FileStore{
		dir:         dir,
		reportsPath: filepath.Join(dir, "reports.jsonl"),
		actionsPath: filepath.Join(dir, "actions.jsonl"),
		logger:      logger.With(zap.String("component", "agent_store")),
	}, nil
}

func (s *FileStore) LoadReports() ([]Report, error) {
	return loadJSONL[Report](s.reportsPath)
}

func (s *FileStore) SaveReport(report Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendJSONL(s.reportsPath, report)
}

func (s *FileStore) LoadActions() ([]ActionDecision, error) {
	return loadJSONL[ActionDecision](s.actionsPath)
}

func (s *FileStore) SaveAction(action ActionDecision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendJSONL(s.actionsPath, action)
}

func (s *FileStore) UpdateAction(id, status, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	actions, err := loadJSONL[ActionDecision](s.actionsPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	updated := false
	for i := range actions {
		if actions[i].ID == id {
			if status != "" {
				actions[i].Status = status
			}
			if note != "" {
				actions[i].Note = note
			}
			actions[i].Updated = time.Now()
			updated = true
			break
		}
	}
	if !updated {
		return nil
	}
	return rewriteJSONL(s.actionsPath, actions)
}

func loadJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	out := make([]T, 0)
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			continue
		}
		out = append(out, item)
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func appendJSONL(path string, v any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(v)
}

func rewriteJSONL[T any](path string, items []T) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, item := range items {
		if err := enc.Encode(item); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
