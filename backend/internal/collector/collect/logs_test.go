package collect

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogCollectorCollectsFromFiles(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(logPath, []byte("hello world\nhello world\nwarning: cpu high\n"), 0o644))

	c := NewLogCollector([]string{logPath}, 10)
	c.readJournald = func(_ time.Duration, _ int) ([]string, error) {
		t.Fatalf("journald fallback should not be called when file logs exist")
		return nil, nil
	}

	logs := c.Collect(time.Now())
	require.NotEmpty(t, logs)

	total := uint64(0)
	for _, l := range logs {
		total += l.Count
	}
	assert.Equal(t, uint64(3), total)
}

func TestLogCollectorFallsBackToJournaldWhenFilesUnavailable(t *testing.T) {
	c := NewLogCollector([]string{"/path/does/not/exist.log"}, 10)
	c.readJournald = func(_ time.Duration, _ int) ([]string, error) {
		return []string{
			"2026-02-05T20:00:00Z host kubelet[123]: warning disk pressure",
			"2026-02-05T20:00:01Z host kubelet[123]: warning disk pressure",
			"2026-02-05T20:00:02Z host api[999]: error timeout",
		}, nil
	}

	logs := c.Collect(time.Now())
	require.NotEmpty(t, logs)

	total := uint64(0)
	hasWarning := false
	hasError := false
	for _, l := range logs {
		total += l.Count
		if l.Example != "" && (l.Example == "2026-02-05T20:00:00Z host kubelet[123]: warning disk pressure" || l.Example == "2026-02-05T20:00:01Z host kubelet[123]: warning disk pressure") {
			hasWarning = true
		}
		if l.Example == "2026-02-05T20:00:02Z host api[999]: error timeout" {
			hasError = true
		}
	}
	assert.Equal(t, uint64(3), total)
	assert.True(t, hasWarning)
	assert.True(t, hasError)
}
