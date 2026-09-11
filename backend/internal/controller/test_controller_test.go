package controller

import (
	"path/filepath"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

// Every controller owns its journal. Tests use separate directories, preserving
// production durability and file locking instead of silently selecting memory.
func newTestController(t testing.TB, cfg Config, logger *zap.Logger) (*Controller, error) {
	t.Helper()
	if cfg.Ingest.Inbox.Path == "" || cfg.Ingest.Inbox.Path == ingest.DefaultInboxConfig().Path {
		cfg.Ingest.Inbox.Path = filepath.Join(t.TempDir(), "inbox.db")
	}
	c, err := New(cfg, logger)
	if c != nil {
		t.Cleanup(func() { _ = c.Stop() })
	}
	return c, err
}
