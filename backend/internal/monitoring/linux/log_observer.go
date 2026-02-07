package linux

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// LogObserver observes system logs from journald and syslog
type LogObserver struct {
	config *LogObserverConfig
	logger *zap.Logger

	journaldCmd *exec.Cmd
	syslogFile  *os.File

	logCh chan LogEntry

	mu      sync.RWMutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// LogObserverConfig configures log observation
type LogObserverConfig struct {
	Enabled bool `yaml:"enabled"`

	// Journald settings
	EnableJournald bool          `yaml:"enable_journald"`
	JournaldPath   string        `yaml:"journald_path"` // path to journalctl
	JournaldArgs   []string      `yaml:"journald_args"`
	JournaldSince  time.Duration `yaml:"journald_since"`

	// Syslog settings
	EnableSyslog     bool     `yaml:"enable_syslog"`
	SyslogPath       string   `yaml:"syslog_path"` // /var/log/syslog, /var/log/messages
	SyslogFollow     bool     `yaml:"syslog_follow"`
	SyslogCandidates []string `yaml:"syslog_candidates"`

	// Log filtering
	FilterUnits      []string `yaml:"filter_units"`      // systemd units
	FilterPriorities []string `yaml:"filter_priorities"` // emerg, alert, crit, err, warning, notice, info, debug
	FilterPatterns   []string `yaml:"filter_patterns"`   // substring matches
	ExcludePatterns  []string `yaml:"exclude_patterns"`

	// Rate limiting
	RateLimit  int           `yaml:"rate_limit"` // entries per second
	RateWindow time.Duration `yaml:"rate_window"`
}

// LogEntry represents a log entry
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Source    string                 `json:"source"`   // journald, syslog, kernel
	Priority  string                 `json:"priority"` // emerg, alert, crit, err, warning, notice, info, debug
	Facility  string                 `json:"facility"`
	Message   string                 `json:"message"`
	Unit      string                 `json:"unit,omitempty"` // systemd unit
	PID       int                    `json:"pid,omitempty"`
	Process   string                 `json:"process,omitempty"`
	Host      string                 `json:"host,omitempty"`
	Labels    map[string]string      `json:"labels,omitempty"`
	RawData   map[string]interface{} `json:"raw_data,omitempty"`
}

// LogPriority levels
const (
	PriorityEmerg   = "emerg"
	PriorityAlert   = "alert"
	PriorityCrit    = "crit"
	PriorityErr     = "err"
	PriorityWarning = "warning"
	PriorityNotice  = "notice"
	PriorityInfo    = "info"
	PriorityDebug   = "debug"
)

// NewLogObserver creates a new log observer
func NewLogObserver(config *LogObserverConfig, logger *zap.Logger) (*LogObserver, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("log observer is disabled")
	}

	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("log observer requires Linux")
	}

	return &LogObserver{
		config:  config,
		logger:  logger.With(zap.String("collector", "log_observer")),
		logCh:   make(chan LogEntry, 1000),
		running: false,
	}, nil
}

// Start starts the log observer
func (lo *LogObserver) Start(ctx context.Context) error {
	lo.mu.Lock()
	defer lo.mu.Unlock()

	if lo.running {
		return nil
	}

	ctx, lo.cancel = context.WithCancel(ctx)

	// Start journald observer
	if lo.config.EnableJournald {
		if err := lo.startJournald(ctx); err != nil {
			lo.logger.Warn("failed to start journald observer", zap.Error(err))
		}
	}

	// Start syslog observer
	if lo.config.EnableSyslog {
		if err := lo.startSyslog(ctx); err != nil {
			lo.logger.Warn("failed to start syslog observer", zap.Error(err))
		}
	}

	lo.running = true
	lo.logger.Info("log observer started")

	return nil
}

// Stop stops the log observer
func (lo *LogObserver) Stop() error {
	lo.mu.Lock()
	defer lo.mu.Unlock()

	if !lo.running {
		return nil
	}

	if lo.cancel != nil {
		lo.cancel()
	}

	lo.wg.Wait()

	// Stop journald
	if lo.journaldCmd != nil && lo.journaldCmd.Process != nil {
		lo.journaldCmd.Process.Kill()
		lo.journaldCmd.Wait()
	}

	// Close syslog file
	if lo.syslogFile != nil {
		lo.syslogFile.Close()
	}

	close(lo.logCh)
	lo.running = false
	lo.logger.Info("log observer stopped")

	return nil
}

// Logs returns the log entry channel
func (lo *LogObserver) Logs() <-chan LogEntry {
	return lo.logCh
}

// startJournald starts the journald observer using journalctl
func (lo *LogObserver) startJournald(ctx context.Context) error {
	journalctlPath := lo.config.JournaldPath
	if journalctlPath == "" {
		// Try to find journalctl
		path, err := exec.LookPath("journalctl")
		if err != nil {
			return fmt.Errorf("journalctl not found: %w", err)
		}
		journalctlPath = path
	}

	args := []string{
		"-f",      // follow
		"-n", "0", // don't show old entries
		"-o", "json", // output format
	}

	// Add since time if specified
	if lo.config.JournaldSince > 0 {
		args = append(args, "--since", lo.config.JournaldSince.String())
	}

	// Add unit filters
	for _, unit := range lo.config.FilterUnits {
		args = append(args, "-u", unit)
	}

	// Add priority filters
	for _, priority := range lo.config.FilterPriorities {
		args = append(args, "-p", priority)
	}

	// Add custom args
	args = append(args, lo.config.JournaldArgs...)

	lo.journaldCmd = exec.CommandContext(ctx, journalctlPath, args...)

	stdout, err := lo.journaldCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := lo.journaldCmd.Start(); err != nil {
		return fmt.Errorf("failed to start journalctl: %w", err)
	}

	lo.wg.Add(1)
	go lo.readJournald(ctx, stdout)

	lo.logger.Info("journald observer started", zap.String("path", journalctlPath))

	return nil
}

// startSyslog starts the syslog file observer
func (lo *LogObserver) startSyslog(ctx context.Context) error {
	syslogPath := lo.config.SyslogPath
	if syslogPath == "" {
		// Try common syslog locations
		candidates := lo.config.SyslogCandidates
		if len(candidates) == 0 {
			candidates = []string{
				"/var/log/syslog",
				"/var/log/messages",
				"/var/log/system.log",
			}
		}
		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				syslogPath = path
				break
			}
		}
	}

	if syslogPath == "" {
		return fmt.Errorf("syslog file not found")
	}

	file, err := os.Open(syslogPath)
	if err != nil {
		return fmt.Errorf("failed to open syslog: %w", err)
	}

	lo.syslogFile = file

	// Seek to end if following
	if lo.config.SyslogFollow {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			lo.logger.Warn("failed to seek to end of syslog", zap.Error(err))
		}
	}

	lo.wg.Add(1)
	go lo.readSyslog(ctx, file)

	lo.logger.Info("syslog observer started", zap.String("path", syslogPath))

	return nil
}

// readJournald reads and parses journald output
func (lo *LogObserver) readJournald(ctx context.Context, stdout io.ReadCloser) {
	defer lo.wg.Done()
	defer stdout.Close()

	scanner := bufio.NewScanner(stdout)
	rateLimiter := newRateLimiter(lo.config.RateLimit, lo.config.RateWindow)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				lo.logger.Error("error reading journald", zap.Error(err))
			}
			return
		}

		line := scanner.Bytes()
		entry, err := lo.parseJournaldEntry(line)
		if err != nil {
			lo.logger.Debug("failed to parse journald entry", zap.Error(err))
			continue
		}

		if lo.shouldIncludeLog(&entry) {
			if rateLimiter.Allow() {
				select {
				case lo.logCh <- entry:
				default:
					lo.logger.Warn("log channel full, dropping entry")
				}
			}
		}
	}
}

// readSyslog reads and parses syslog output
func (lo *LogObserver) readSyslog(ctx context.Context, file *os.File) {
	defer lo.wg.Done()

	scanner := bufio.NewScanner(file)
	rateLimiter := newRateLimiter(lo.config.RateLimit, lo.config.RateWindow)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				lo.logger.Error("error reading syslog", zap.Error(err))
			}

			// If following and EOF, wait and retry
			if lo.config.SyslogFollow {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return
		}

		line := scanner.Text()
		entry := lo.parseSyslogLine(line)

		if lo.shouldIncludeLog(&entry) {
			if rateLimiter.Allow() {
				select {
				case lo.logCh <- entry:
				default:
					lo.logger.Warn("log channel full, dropping entry")
				}
			}
		}
	}
}

// parseJournaldEntry parses a JSON journald entry
func (lo *LogObserver) parseJournaldEntry(data []byte) (LogEntry, error) {
	entry := LogEntry{
		Source:  "journald",
		Labels:  make(map[string]string),
		RawData: make(map[string]interface{}),
	}

	// Simple JSON parsing (for production, use encoding/json)
	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		// Parse KEY=VALUE format
		parts := bytes.SplitN(line, []byte{'='}, 2)
		if len(parts) != 2 {
			continue
		}

		key := string(parts[0])
		value := string(parts[1])

		switch key {
		case "__REALTIME_TIMESTAMP":
			// Microseconds since epoch
			var usec int64
			fmt.Sscanf(value, "%d", &usec)
			entry.Timestamp = time.Unix(usec/1000000, (usec%1000000)*1000)
		case "PRIORITY":
			priority := lo.priorityFromInt(value)
			entry.Priority = priority
		case "SYSLOG_IDENTIFIER":
			entry.Process = value
		case "_PID":
			fmt.Sscanf(value, "%d", &entry.PID)
		case "_SYSTEMD_UNIT":
			entry.Unit = value
		case "_HOSTNAME":
			entry.Host = value
		case "MESSAGE":
			entry.Message = value
		case "SYSLOG_FACILITY":
			entry.Facility = value
		default:
			entry.RawData[key] = value
		}
	}

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	return entry, nil
}

// parseSyslogLine parses a traditional syslog line
func (lo *LogObserver) parseSyslogLine(line string) LogEntry {
	// Try RFC3164 format: Mon Jan 2 15:04:05 hostname process[pid]: message
	// Or RFC5424 format: <priority>version timestamp hostname app-name procid msgid message

	if strings.HasPrefix(line, "<") {
		return lo.parseRFC5424(line)
	}

	return lo.parseRFC3164(line)
}

// parseRFC3164 parses RFC3164 format syslog
func (lo *LogObserver) parseRFC3164(line string) LogEntry {
	entry := LogEntry{
		Source: "syslog",
		Labels: make(map[string]string),
	}

	// Simple parsing - RFC3164 is quite flexible
	// Format: Mon Jan 2 15:04:05 hostname process[pid]: message

	parts := strings.Fields(line)
	if len(parts) < 3 {
		entry.Message = line
		entry.Timestamp = time.Now()
		return entry
	}

	// Try to parse timestamp
	timestampStr := strings.Join(parts[0:3], " ")
	timestamp, err := time.ParseInLocation("Jan 2 15:04:05", timestampStr, time.Local)
	if err != nil {
		entry.Timestamp = time.Now()
	} else {
		// Add current year
		entry.Timestamp = timestamp.AddDate(time.Now().Year(), 0, 0)
	}

	// Parse hostname and process
	if len(parts) >= 4 {
		entry.Host = parts[3]
	}

	// Find the message part
	msgIndex := strings.Index(line, ": ")
	if msgIndex > 0 {
		entry.Message = strings.TrimSpace(line[msgIndex+2:])

		// Try to extract process and PID
		beforeMsg := line[:msgIndex]
		lastSpace := strings.LastIndex(beforeMsg, " ")
		if lastSpace > 0 {
			processPart := beforeMsg[lastSpace+1:]
			if openBracket := strings.Index(processPart, "["); openBracket > 0 {
				entry.Process = processPart[:openBracket]
				if closeBracket := strings.Index(processPart, "]"); closeBracket > openBracket {
					pidStr := processPart[openBracket+1 : closeBracket]
					fmt.Sscanf(pidStr, "%d", &entry.PID)
				}
			} else {
				entry.Process = processPart
			}
		}
	} else {
		entry.Message = line
	}

	// Guess priority from message content
	entry.Priority = lo.guessPriority(entry.Message)

	return entry
}

// parseRFC5424 parses RFC5424 format syslog
func (lo *LogObserver) parseRFC5424(line string) LogEntry {
	entry := LogEntry{
		Source:    "syslog",
		Labels:    make(map[string]string),
		Timestamp: time.Now(),
	}

	// Format: <priority>version timestamp hostname app-name procid msgid message
	// Example: <34>1 2003-10-11T22:14:15.003Z mymachine.example.com su - ID47 - BOM'su root' failed for lonvart on /dev/pts/8

	if !strings.HasPrefix(line, "<") {
		entry.Message = line
		return entry
	}

	closeBracket := strings.Index(line, ">")
	if closeBracket < 0 {
		entry.Message = line
		return entry
	}

	// Parse priority
	priorityStr := line[1:closeBracket]
	var priority int
	fmt.Sscanf(priorityStr, "%d", &priority)
	entry.Priority = lo.priorityFromRFC5424(priority)

	// Skip version
	afterBracket := strings.TrimSpace(line[closeBracket+1:])
	parts := strings.Fields(afterBracket)

	if len(parts) >= 1 {
		// Parse timestamp
		if timestamp, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			entry.Timestamp = timestamp
		}
	}

	if len(parts) >= 2 {
		entry.Host = parts[1]
	}

	if len(parts) >= 3 {
		entry.Process = parts[2]
	}

	if len(parts) >= 4 && parts[3] != "-" {
		fmt.Sscanf(parts[3], "%d", &entry.PID)
	}

	// Find message part
	msgStart := strings.Index(line, " - ")
	if msgStart > 0 {
		entry.Message = strings.TrimSpace(line[msgStart+3:])
	} else {
		entry.Message = afterBracket
	}

	return entry
}

// priorityFromInt converts journald priority integer to string
func (lo *LogObserver) priorityFromInt(p string) string {
	priorities := map[string]string{
		"0": PriorityEmerg,
		"1": PriorityAlert,
		"2": PriorityCrit,
		"3": PriorityErr,
		"4": PriorityWarning,
		"5": PriorityNotice,
		"6": PriorityInfo,
		"7": PriorityDebug,
	}
	if prio, ok := priorities[p]; ok {
		return prio
	}
	return PriorityInfo
}

// priorityFromRFC5424 converts RFC5424 priority to string
func (lo *LogObserver) priorityFromRFC5424(p int) string {
	// Priority = facility * 8 + severity
	severity := p & 0x07
	return lo.priorityFromInt(fmt.Sprintf("%d", severity))
}

// guessPriority tries to guess priority from message content
func (lo *LogObserver) guessPriority(message string) string {
	msg := strings.ToLower(message)

	switch {
	case strings.Contains(msg, "emerg") || strings.Contains(msg, "emergency"):
		return PriorityEmerg
	case strings.Contains(msg, "alert"):
		return PriorityAlert
	case strings.Contains(msg, "crit") || strings.Contains(msg, "critical"):
		return PriorityCrit
	case strings.Contains(msg, "err") || strings.Contains(msg, "error") || strings.Contains(msg, "fail"):
		return PriorityErr
	case strings.Contains(msg, "warn") || strings.Contains(msg, "warning"):
		return PriorityWarning
	case strings.Contains(msg, "notice"):
		return PriorityNotice
	case strings.Contains(msg, "debug") || strings.Contains(msg, "trace"):
		return PriorityDebug
	default:
		return PriorityInfo
	}
}

// shouldIncludeLog filters log entries based on configuration
func (lo *LogObserver) shouldIncludeLog(entry *LogEntry) bool {
	// Check exclude patterns first
	for _, pattern := range lo.config.ExcludePatterns {
		if strings.Contains(entry.Message, pattern) {
			return false
		}
	}

	// Check include patterns
	if len(lo.config.FilterPatterns) > 0 {
		for _, pattern := range lo.config.FilterPatterns {
			if strings.Contains(entry.Message, pattern) {
				return true
			}
		}
		return false
	}

	// Check priority filter
	if len(lo.config.FilterPriorities) > 0 {
		priorityMatch := false
		for _, priority := range lo.config.FilterPriorities {
			if entry.Priority == priority {
				priorityMatch = true
				break
			}
		}
		if !priorityMatch {
			return false
		}
	}

	return true
}

// GetStats returns observer statistics
func (lo *LogObserver) GetStats() map[string]interface{} {
	lo.mu.RLock()
	defer lo.mu.RUnlock()

	stats := make(map[string]interface{})
	stats["running"] = lo.running
	stats["journald_enabled"] = lo.config.EnableJournald
	stats["syslog_enabled"] = lo.config.EnableSyslog
	stats["syslog_path"] = lo.config.SyslogPath

	return stats
}

// rateLimiter limits the rate of log entries
type rateLimiter struct {
	limit   int
	window  time.Duration
	tokens  int
	lastRef time.Time
	mu      sync.Mutex
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	if limit <= 0 {
		limit = 100 // default limit
	}
	if window <= 0 {
		window = time.Second // default window
	}
	return &rateLimiter{
		limit:   limit,
		window:  window,
		tokens:  limit,
		lastRef: time.Now(),
	}
}

func (rl *rateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRef)

	// Refill tokens based on elapsed time
	tokensToAdd := int(elapsed / rl.window * time.Duration(rl.limit))
	rl.tokens += tokensToAdd
	if rl.tokens > rl.limit {
		rl.tokens = rl.limit
	}
	rl.lastRef = now

	if rl.tokens > 0 {
		rl.tokens--
		return true
	}
	return false
}
