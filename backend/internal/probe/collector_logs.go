// Package probe implements log and event collection (Level 4).
// This includes kernel ring buffer (dmesg), systemd journal, and log files.
package probe

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
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
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Source    string    `json:"source"`
	Message   string    `json:"message"`
	Facility  string    `json:"facility,omitempty"`
	Hostname  string    `json:"hostname"`
}

// LogBatch represents a batch of log entries for transmission
type LogBatch struct {
	Entries   []LogEntry `json:"entries"`
	BatchID   string     `json:"batch_id"`
	Hostname  string     `json:"hostname"`
	Timestamp time.Time  `json:"timestamp"`
	Count     int        `json:"count"`
}

// LogCollector collects logs from various sources
type LogCollector struct {
	hostname string

	// Kernel message stats
	mu          sync.Mutex
	kmsgCounts  map[LogLevel]uint64
	lastKmsgSeq uint64

	// Log buffer for push model
	buffer    []LogEntry
	bufferMu  sync.Mutex
	maxBuffer int

	// Push configuration
	pushEnabled  bool
	pushEndpoint string
	pushInterval time.Duration
	pushClient   *http.Client

	// Control
	ctx    chan struct{}
	closed bool
}

// NewLogCollector creates a new log collector
func NewLogCollector(hostname string) *LogCollector {
	return &LogCollector{
		hostname:     hostname,
		kmsgCounts:   make(map[LogLevel]uint64),
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
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.pushEnabled = true
	lc.pushEndpoint = endpoint
	lc.pushInterval = interval
}

// Start starts the log collector background tasks
func (lc *LogCollector) Start() {
	go lc.kmsgReader()

	if lc.pushEnabled {
		go lc.pushLoop()
	}
}

// Stop stops the log collector
func (lc *LogCollector) Stop() {
	lc.mu.Lock()
	if !lc.closed {
		close(lc.ctx)
		lc.closed = true
	}
	lc.mu.Unlock()
}

// GetMetrics returns kernel message count metrics
func (lc *LogCollector) GetMetrics(now time.Time) []Metric {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	var metrics []Metric
	for level, count := range lc.kmsgCounts {
		metrics = append(metrics, Metric{
			Name:      "node_kmsg_messages_total",
			Type:      "counter",
			Value:     float64(count),
			Labels:    map[string]string{"level": level.String()},
			Timestamp: now,
		})
	}

	return metrics
}

// kmsgReader reads from /dev/kmsg (kernel ring buffer)
func (lc *LogCollector) kmsgReader() {
	// Try to open /dev/kmsg (requires CAP_SYSLOG or root)
	f, err := os.Open("/dev/kmsg")
	if err != nil {
		// Fall back to reading /var/log/kern.log or dmesg command
		lc.readDmesgFallback()
		return
	}
	defer f.Close()

	// Set non-blocking to avoid blocking forever
	// Note: In production, we'd use epoll/select
	reader := bufio.NewReader(f)

	for {
		select {
		case <-lc.ctx:
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return
		}

		entry := lc.parseKmsg(line)
		if entry != nil {
			lc.recordKmsg(entry)
		}
	}
}

// parseKmsg parses a kernel message line
// Format: <priority>,sequence,timestamp,flags;message
func (lc *LogCollector) parseKmsg(line string) *LogEntry {
	// Parse priority (first field before comma)
	idx := strings.Index(line, ",")
	if idx == -1 {
		return nil
	}

	priorityStr := strings.TrimPrefix(line[:idx], "<")
	priorityStr = strings.TrimSuffix(priorityStr, ">")

	priority, err := strconv.Atoi(priorityStr)
	if err != nil {
		// Try alternate format
		priority = 6 // Default to info
	}

	level := LogLevel(priority & 7)
	facility := priority >> 3

	// Find the message (after semicolon)
	msgIdx := strings.Index(line, ";")
	var message string
	if msgIdx != -1 {
		message = strings.TrimSpace(line[msgIdx+1:])
	} else {
		message = strings.TrimSpace(line)
	}

	return &LogEntry{
		Timestamp: time.Now(),
		Level:     level.String(),
		Source:    "kmsg",
		Message:   message,
		Facility:  fmt.Sprintf("%d", facility),
		Hostname:  lc.hostname,
	}
}

// recordKmsg records a kernel message
func (lc *LogCollector) recordKmsg(entry *LogEntry) {
	lc.mu.Lock()

	// Update counts
	level := LogLevelInfo
	switch entry.Level {
	case "emerg":
		level = LogLevelEmerg
	case "alert":
		level = LogLevelAlert
	case "crit":
		level = LogLevelCrit
	case "err":
		level = LogLevelErr
	case "warn":
		level = LogLevelWarn
	case "notice":
		level = LogLevelNotice
	case "info":
		level = LogLevelInfo
	case "debug":
		level = LogLevelDebug
	}
	lc.kmsgCounts[level]++

	lc.mu.Unlock()

	// Add to buffer if push enabled
	if lc.pushEnabled {
		lc.bufferMu.Lock()
		if len(lc.buffer) < lc.maxBuffer {
			lc.buffer = append(lc.buffer, *entry)
		}
		lc.bufferMu.Unlock()
	}
}

// readDmesgFallback reads kernel messages using dmesg fallback
func (lc *LogCollector) readDmesgFallback() {
	// Read /var/log/kern.log if available
	paths := []string{
		"/var/log/kern.log",
		"/var/log/messages",
		"/var/log/syslog",
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			go lc.tailLogFile(path, "kernel")
			return
		}
	}
}

// tailLogFile tails a log file and records entries
func (lc *LogCollector) tailLogFile(path, source string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	// Seek to end
	f.Seek(0, io.SeekEnd)

	reader := bufio.NewReader(f)

	for {
		select {
		case <-lc.ctx:
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return
		}

		entry := lc.parseLogLine(line, source)
		if entry != nil && lc.pushEnabled {
			lc.bufferMu.Lock()
			if len(lc.buffer) < lc.maxBuffer {
				lc.buffer = append(lc.buffer, *entry)
			}
			lc.bufferMu.Unlock()
		}
	}
}

// parseLogLine parses a syslog-style log line
func (lc *LogCollector) parseLogLine(line, source string) *LogEntry {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	// Simple parsing - just record the line
	level := "info"
	lineLower := strings.ToLower(line)
	if strings.Contains(lineLower, "error") || strings.Contains(lineLower, "fail") {
		level = "err"
	} else if strings.Contains(lineLower, "warn") {
		level = "warn"
	} else if strings.Contains(lineLower, "crit") {
		level = "crit"
	}

	return &LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Source:    source,
		Message:   line,
		Hostname:  lc.hostname,
	}
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
		Entries:   entries,
		BatchID:   fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(entries)),
		Hostname:  lc.hostname,
		Timestamp: time.Now(),
		Count:     len(entries),
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
		// In production, we'd implement retry logic
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
