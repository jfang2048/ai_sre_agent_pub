// Package probe collects kernel, journal, and configured file logs.
package probe

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nxadm/tail"
)

// LogLevel represents kernel log severity levels
type LogLevel int

const (
	LogLevelEmerg LogLevel = iota
	LogLevelAlert
	LogLevelCrit
	LogLevelErr
	LogLevelWarn
	LogLevelNotice
	LogLevelInfo
	LogLevelDebug
)

func (l LogLevel) String() string {
	switch l {
	case LogLevelEmerg:
		return "emerg"
	case LogLevelAlert:
		return "alert"
	case LogLevelCrit:
		return "crit"
	case LogLevelErr:
		return "err"
	case LogLevelWarn:
		return "warn"
	case LogLevelNotice:
		return "notice"
	case LogLevelInfo:
		return "info"
	case LogLevelDebug:
		return "debug"
	default:
		return "unknown"
	}
}

// LogEntry represents a single log entry
type LogEntry struct {
	Timestamp         time.Time         `json:"timestamp"`
	TimestampUnixNano int64             `json:"timestamp_unix_nano,omitempty"`
	Level             string            `json:"level"`
	Source            string            `json:"source"`
	Service           string            `json:"service,omitempty"`
	Message           string            `json:"message"`
	Hostname          string            `json:"hostname"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// LogBatch represents a batch of log entries for transmission, aligned with controller's ingest request
type LogBatch struct {
	CollectorID string     `json:"collector_id"`
	Hostname    string     `json:"hostname"`
	Source      string     `json:"source"`
	Service     string     `json:"service"`
	Entries     []LogEntry `json:"entries"`
	BatchID     string     `json:"batch_id"`
	Timestamp   time.Time  `json:"timestamp"`
	Count       int        `json:"count"`
}

// LogCollector tails configured sources and buffers push batches.
type LogCollector struct {
	hostname string

	buffer    []LogEntry
	bufferMu  sync.Mutex
	maxBuffer int

	pushEnabled  bool
	pushEndpoint string
	pushInterval time.Duration
	pushClient   *http.Client

	tails    []*tail.Tail
	tailSync sync.Mutex

	ctx    chan struct{}
	closed bool
}

// NewLogCollector creates a new log collector
func NewLogCollector(hostname string) *LogCollector {
	return &LogCollector{
		hostname:     hostname,
		buffer:       make([]LogEntry, 0, 1000),
		maxBuffer:    10000,
		pushInterval: 5 * time.Second,
		pushClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		ctx: make(chan struct{}),
	}
}

// EnablePush enables push-based log transmission
func (lc *LogCollector) EnablePush(endpoint string, interval time.Duration) {
	lc.bufferMu.Lock()
	defer lc.bufferMu.Unlock()
	lc.pushEnabled = true
	lc.pushEndpoint = endpoint
	lc.pushInterval = interval
}

// AddLogSource adds a file or glob pattern to watch
func (lc *LogCollector) AddLogSource(pattern string, service string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() {
			go lc.tailFile(match, service)
		}
	}

	return nil
}

// Start starts the log collector background tasks
func (lc *LogCollector) Start() {
	// Add default system logs -- Modified for local testing
	lc.AddLogSource("/tmp/logs/syslog", "syslog")
	lc.AddLogSource("/tmp/logs/kern.log", "kernel")
	lc.AddLogSource("/tmp/logs/messages", "messages")
	lc.AddLogSource("/tmp/logs/nginx/*.log", "nginx")
	lc.AddLogSource("/tmp/logs/mysql/*.log", "mysql")
	lc.AddLogSource("/tmp/logs/redis/*.log", "redis")

	go lc.pushLoop()
}

// Stop stops the log collector
func (lc *LogCollector) Stop() {
	lc.tailSync.Lock()
	if !lc.closed {
		close(lc.ctx)
		lc.closed = true
		for _, t := range lc.tails {
			t.Stop()
			t.Cleanup()
		}
	}
	lc.tailSync.Unlock()
}

// GetMetrics returns metrics about log ingestion
func (lc *LogCollector) GetMetrics(now time.Time) []Metric {
	return nil
}

func (lc *LogCollector) tailFile(path string, service string) {
	t, err := tail.TailFile(path, tail.Config{
		Follow:    true,
		ReOpen:    true,
		Location:  &tail.SeekInfo{Offset: 0, Whence: os.SEEK_END},
		MustExist: false,
		Logger:    tail.DiscardingLogger,
	})

	if err != nil {
		return
	}

	lc.tailSync.Lock()
	if lc.closed {
		lc.tailSync.Unlock()
		t.Stop()
		t.Cleanup()
		return
	}
	lc.tails = append(lc.tails, t)
	lc.tailSync.Unlock()

	for {
		select {
		case <-lc.ctx:
			return
		case line, ok := <-t.Lines:
			if !ok {
				return
			}
			if line.Err != nil {
				continue
			}

			entry := lc.parseLogLine(line.Text, path, service)
			if entry != nil {
				lc.bufferMu.Lock()
				if lc.pushEnabled && len(lc.buffer) < lc.maxBuffer {
					lc.buffer = append(lc.buffer, *entry)
				}
				lc.bufferMu.Unlock()
			}
		}
	}
}

// parseLogLine attempts to parse a structured JSON log line, falling back to simple text
func (lc *LogCollector) parseLogLine(line string, source string, service string) *LogEntry {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	entry := &LogEntry{
		Timestamp:         time.Now(),
		TimestampUnixNano: time.Now().UnixNano(),
		Source:            source,
		Service:           service,
		Hostname:          lc.hostname,
		Labels:            make(map[string]string),
	}

	// Try JSON parsing
	if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(line), &data); err == nil {
			// Extract known fields
			if msg, ok := data["message"].(string); ok {
				entry.Message = msg
				delete(data, "message")
			} else if msg, ok := data["msg"].(string); ok {
				entry.Message = msg
				delete(data, "msg")
			} else {
				entry.Message = line // fallback entire line if no message field
			}

			if lvl, ok := data["level"].(string); ok {
				entry.Level = strings.ToLower(lvl)
				delete(data, "level")
			} else if lvl, ok := data["severity"].(string); ok {
				entry.Level = strings.ToLower(lvl)
				delete(data, "severity")
			}

			if srv, ok := data["service"].(string); ok {
				entry.Service = srv
				delete(data, "service")
			}

			// Capture remaining flat non-object fields as labels
			for k, v := range data {
				switch val := v.(type) {
				case string:
					entry.Labels[k] = val
				case float64:
					entry.Labels[k] = strconv.FormatFloat(val, 'f', -1, 64)
				case bool:
					entry.Labels[k] = strconv.FormatBool(val)
				}
			}

			if entry.Level == "" {
				entry.Level = "info"
			}
			return entry
		}
	}

	// Simple text parsing
	entry.Message = line
	entry.Level = "info"
	lineLower := strings.ToLower(line)
	if strings.Contains(lineLower, "error") || strings.Contains(lineLower, "fail") || strings.Contains(lineLower, "exception") || strings.Contains(lineLower, "fatal") {
		entry.Level = "err"
	} else if strings.Contains(lineLower, "warn") {
		entry.Level = "warn"
	} else if strings.Contains(lineLower, "crit") {
		entry.Level = "crit"
	}

	return entry
}

// pushLoop periodically pushes buffered logs to controller
func (lc *LogCollector) pushLoop() {
	ticker := time.NewTicker(lc.pushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-lc.ctx:
			// Final flush
			lc.flushBuffer()
			return
		case <-ticker.C:
			lc.flushBuffer()
		}
	}
}

// flushBuffer sends buffered logs to controller
func (lc *LogCollector) flushBuffer() {
	lc.bufferMu.Lock()
	if len(lc.buffer) == 0 {
		lc.bufferMu.Unlock()
		return
	}

	// Take entries from buffer
	entries := make([]LogEntry, len(lc.buffer))
	copy(entries, lc.buffer)
	lc.buffer = lc.buffer[:0]
	lc.bufferMu.Unlock()

	// Create batch
	batch := LogBatch{
		CollectorID: "host/" + sanitizeIdentity(lc.hostname),
		Hostname:    lc.hostname,
		Source:      "probe",
		Service:     "system",
		Entries:     entries,
		BatchID:     fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(entries)),
		Timestamp:   time.Now(),
		Count:       len(entries),
	}

	// Serialize and compress
	data, err := json.Marshal(batch)
	if err != nil {
		return
	}

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(data)
	gz.Close()

	// Send to controller
	req, err := http.NewRequest("POST", lc.pushEndpoint, &compressed)
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := lc.pushClient.Do(req)
	if err != nil {
		// Log push failed - entries are lost
		return
	}
	resp.Body.Close()
}

// collectKernelEvents collects kernel event metrics (summary)
func (c *Collector) collectKernelEvents(now time.Time) ([]Metric, error) {
	var metrics []Metric

	// Read dmesg for recent errors (last boot)
	data, err := os.ReadFile("/var/log/dmesg")
	if err != nil {
		// Try alternative
		data, err = os.ReadFile("/var/log/kern.log")
		if err != nil {
			return metrics, nil // Not critical
		}
	}

	// Count messages by severity keywords
	counts := map[string]int{
		"error":   0,
		"warning": 0,
		"panic":   0,
		"oops":    0,
		"bug":     0,
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		lineLower := strings.ToLower(line)
		for keyword := range counts {
			if strings.Contains(lineLower, keyword) {
				counts[keyword]++
			}
		}
	}

	for keyword, count := range counts {
		metrics = append(metrics, Metric{
			Name:      "node_kernel_log_messages",
			Type:      "gauge",
			Value:     float64(count),
			Labels:    map[string]string{"type": keyword},
			Timestamp: now,
		})
	}

	return metrics, nil
}

func sanitizeIdentity(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	builder := strings.Builder{}
	builder.Grow(len(value))
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-', r == '_', r == '.', r == '/':
			builder.WriteRune(r)
		}
	}
	trimmed := strings.Trim(builder.String(), "-_/.")
	if trimmed == "" {
		return "unknown"
	}
	if len(trimmed) > 96 {
		return trimmed[:96]
	}
	return trimmed
}
