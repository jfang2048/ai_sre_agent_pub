package collect

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

// LogCollector tails log files and emits fingerprint counts.
type LogCollector struct {
	paths        []string
	offsets      map[string]int64
	maxEntries   int
	readJournald func(since time.Duration, maxLines int) ([]string, error)
}

// NewLogCollector creates a log collector.
func NewLogCollector(paths []string, maxEntries int) *LogCollector {
	if len(paths) == 0 {
		paths = defaultLogPaths()
	}
	if maxEntries <= 0 {
		maxEntries = 100
	}
	return &LogCollector{
		paths:        paths,
		offsets:      make(map[string]int64),
		maxEntries:   maxEntries,
		readJournald: defaultJournaldReader,
	}
}

// Collect returns fingerprinted log counts since last read.
func (c *LogCollector) Collect(now time.Time) []*telemetryv1.LogFingerprint {
	fingerprints := make(map[string]*telemetryv1.LogFingerprint)

	for _, path := range c.paths {
		c.collectFromPath(path, now, fingerprints)
	}
	if len(fingerprints) == 0 && c.readJournald != nil {
		c.collectFromJournald(now, fingerprints)
	}

	out := make([]*telemetryv1.LogFingerprint, 0, len(fingerprints))
	for _, entry := range fingerprints {
		out = append(out, entry)
	}
	if len(out) > c.maxEntries {
		out = out[:c.maxEntries]
	}
	return out
}

func (c *LogCollector) collectFromPath(path string, now time.Time, fingerprints map[string]*telemetryv1.LogFingerprint) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return
	}

	offset := c.offsets[path]
	if offset > stat.Size() {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		addFingerprint(strings.TrimSpace(scanner.Text()), now, fingerprints)
	}

	c.offsets[path] = stat.Size()
}

func (c *LogCollector) collectFromJournald(now time.Time, fingerprints map[string]*telemetryv1.LogFingerprint) {
	lines, err := c.readJournald(2*time.Minute, 400)
	if err != nil {
		return
	}
	for _, line := range lines {
		addFingerprint(strings.TrimSpace(line), now, fingerprints)
	}
}

func addFingerprint(line string, now time.Time, fingerprints map[string]*telemetryv1.LogFingerprint) {
	if line == "" {
		return
	}
	fingerprint := hashLine(line)
	entry := fingerprints[fingerprint]
	if entry == nil {
		entry = &telemetryv1.LogFingerprint{
			Fingerprint:       fingerprint,
			Count:             0,
			Example:           line,
			TimestampUnixNano: now.UnixNano(),
		}
		fingerprints[fingerprint] = entry
	}
	entry.Count++
	if entry.Example == "" {
		entry.Example = line
	}
}

func defaultJournaldReader(since time.Duration, maxLines int) ([]string, error) {
	if maxLines <= 0 {
		maxLines = 200
	}
	if since <= 0 {
		since = 2 * time.Minute
	}

	sinceArg := fmt.Sprintf("%d seconds ago", int(since.Seconds()))
	args := []string{
		"--no-pager",
		"--output=short-iso",
		"--since",
		sinceArg,
		"-n",
		fmt.Sprintf("%d", maxLines),
	}
	cmd := exec.Command("journalctl", args...)
	out, err := cmd.Output()
	if err != nil {
		// Best-effort fallback to user session journal.
		userArgs := append([]string{"--user"}, args...)
		out, err = exec.Command("journalctl", userArgs...).Output()
		if err != nil {
			return nil, err
		}
	}

	lines := strings.Split(string(out), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	return filtered, nil
}

func hashLine(line string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(line))
	return fmt.Sprintf("%x", h.Sum64())
}

func defaultLogPaths() []string {
	return []string{
		"/var/log/syslog",
		"/var/log/messages",
		"/var/log/kern.log",
	}
}
