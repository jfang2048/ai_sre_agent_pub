package logindex

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	processPrefixPattern = regexp.MustCompile(`^([A-Za-z0-9_.\-/@]+)(?:\[(\d+)\])?:\s*(.*)$`)
	bracketLevelPattern  = regexp.MustCompile(`^\[([A-Za-z]+)\]\s*(.*)$`)
)

type parsedFields struct {
	timestamp    time.Time
	hasTimestamp bool
	level        string
	service      string
	process      string
	pid          string
	source       string
	message      string
	labels       map[string]string
}

// Normalize converts an incoming raw event into a normalized entry.
func Normalize(cfg Config, event RawEvent, fallback time.Time) (Entry, bool) {
	message := strings.TrimSpace(event.Message)
	if message == "" {
		return Entry{}, false
	}

	parsed := parseMessage(message, fallback)

	entry := Entry{
		Timestamp:   pickTimestamp(event.Timestamp, parsed, fallback),
		CollectorID: normalizeIdentity(event.CollectorID),
		Hostname:    normalizeIdentity(event.Hostname),
		Service:     normalizeName(firstNonEmpty(event.Service, parsed.service)),
		Process:     normalizeName(firstNonEmpty(event.Process, parsed.process)),
		PID:         normalizePID(firstNonEmpty(event.PID, parsed.pid)),
		Level:       normalizeLevel(firstNonEmpty(event.Level, parsed.level)),
		Source:      normalizeSource(firstNonEmpty(event.Source, parsed.source)),
		Message:     normalizeMessage(firstNonEmpty(parsed.message, message), cfg.MaxMessageBytes),
		Fingerprint: strings.TrimSpace(event.Fingerprint),
		Count:       event.Count,
		Labels:      mergeLabels(cfg, event.Labels, parsed.labels),
		Metrics:     cloneMetrics(cfg, event.MetricSnapshot),
	}

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.Count == 0 {
		entry.Count = 1
	}
	if entry.Level == "" {
		entry.Level = normalizeLevel(deriveLevel(entry.Message))
	}
	if entry.Level == "" {
		entry.Level = LevelUnknown
	}
	if entry.Service == "" {
		entry.Service = deriveService(entry, parsed)
	}
	if entry.Process == "" {
		entry.Process = deriveProcess(entry.Message)
	}
	if entry.Message == "" {
		return Entry{}, false
	}

	return entry, true
}

func pickTimestamp(eventTS time.Time, parsed parsedFields, fallback time.Time) time.Time {
	if !eventTS.IsZero() {
		return eventTS
	}
	if parsed.hasTimestamp {
		return parsed.timestamp
	}
	if !fallback.IsZero() {
		return fallback
	}
	return time.Now()
}

func parseMessage(message string, fallback time.Time) parsedFields {
	if pf, ok := parseJSONMessage(message, fallback); ok {
		return pf
	}
	return parseTextMessage(message, fallback)
}

func parseJSONMessage(message string, fallback time.Time) (parsedFields, bool) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return parsedFields{}, false
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return parsedFields{}, false
	}

	pf := parsedFields{labels: make(map[string]string)}

	if ts, ok := extractTimestamp(obj, []string{"@timestamp", "timestamp", "time", "ts", "datetime"}, fallback); ok {
		pf.timestamp = ts
		pf.hasTimestamp = true
	}
	pf.level = stringifyFirst(obj, "level", "severity", "log_level", "lvl")
	pf.service = stringifyFirst(obj, "service", "app", "application", "unit", "logger")
	pf.process = stringifyFirst(obj, "process", "program", "comm", "command", "cmd", "name")
	pf.pid = stringifyFirst(obj, "pid", "process_id", "tgid")
	pf.source = stringifyFirst(obj, "source", "file", "path", "stream", "component")
	pf.message = stringifyFirst(obj, "message", "msg", "log", "error", "event")

	ignore := map[string]struct{}{
		"@timestamp": {}, "timestamp": {}, "time": {}, "ts": {}, "datetime": {},
		"level": {}, "severity": {}, "log_level": {}, "lvl": {},
		"service": {}, "app": {}, "application": {}, "unit": {}, "logger": {},
		"process": {}, "program": {}, "comm": {}, "command": {}, "cmd": {}, "name": {},
		"pid": {}, "process_id": {}, "tgid": {},
		"source": {}, "file": {}, "path": {}, "stream": {}, "component": {},
		"message": {}, "msg": {}, "log": {}, "error": {}, "event": {},
	}

	for k, v := range obj {
		if _, skip := ignore[k]; skip {
			continue
		}
		if value, ok := scalarString(v); ok {
			if key := normalizeLabelKey(k); key != "" {
				pf.labels[key] = value
			}
		}
	}

	return pf, true
}

func parseTextMessage(message string, fallback time.Time) parsedFields {
	pf := parsedFields{labels: make(map[string]string)}
	line := strings.TrimSpace(message)
	if line == "" {
		return pf
	}

	if ts, rest, ok := parseLeadingTimestamp(line, fallback); ok {
		pf.timestamp = ts
		pf.hasTimestamp = true
		line = strings.TrimSpace(rest)
	}

	if service := extractKV(line, "service"); service != "" {
		pf.service = service
	}
	if service := extractKV(line, "app"); service != "" && pf.service == "" {
		pf.service = service
	}
	if service := extractKV(line, "unit"); service != "" && pf.service == "" {
		pf.service = strings.TrimSuffix(service, ".service")
	}

	if process := extractKV(line, "process"); process != "" {
		pf.process = process
	}
	if process := extractKV(line, "comm"); process != "" && pf.process == "" {
		pf.process = process
	}
	if pid := extractKV(line, "pid"); pid != "" {
		pf.pid = pid
	}

	if level := extractKV(line, "level"); level != "" {
		pf.level = level
	}
	if level := extractKV(line, "severity"); level != "" && pf.level == "" {
		pf.level = level
	}
	if source := extractKV(line, "source"); source != "" {
		pf.source = source
	}

	if match := bracketLevelPattern.FindStringSubmatch(line); len(match) == 3 {
		if pf.level == "" {
			pf.level = match[1]
		}
		line = strings.TrimSpace(match[2])
	}

	if match := processPrefixPattern.FindStringSubmatch(line); len(match) == 4 {
		if pf.process == "" {
			pf.process = match[1]
		}
		if pf.pid == "" {
			pf.pid = match[2]
		}
		line = strings.TrimSpace(match[3])
	}

	if pf.level == "" {
		pf.level = deriveLevel(line)
	}
	pf.message = line

	return pf
}

func parseLeadingTimestamp(line string, fallback time.Time) (time.Time, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return time.Time{}, line, false
	}

	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return time.Time{}, line, false
	}

	candidates := []string{parts[0]}
	if len(parts) >= 2 {
		candidates = append(candidates, parts[0]+" "+parts[1])
	}
	if len(parts) >= 3 {
		candidates = append(candidates, parts[0]+" "+parts[1]+" "+parts[2])
	}

	for _, c := range candidates {
		if ts, ok := parseTimeCandidate(c, fallback); ok {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, c))
			return ts, rest, true
		}
	}

	return time.Time{}, line, false
}

func parseTimeCandidate(candidate string, fallback time.Time) (time.Time, bool) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return time.Time{}, false
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"Jan _2 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, candidate); err == nil {
			if layout == "Jan _2 15:04:05" {
				base := fallback
				if base.IsZero() {
					base = time.Now()
				}
				ts = time.Date(base.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), ts.Second(), 0, base.Location())
			}
			return ts, true
		}
	}
	return time.Time{}, false
}

func extractTimestamp(obj map[string]interface{}, keys []string, fallback time.Time) (time.Time, bool) {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		if ts, ok := parseTimestampAny(raw, fallback); ok {
			return ts, true
		}
	}
	return time.Time{}, false
}

func parseTimestampAny(raw interface{}, fallback time.Time) (time.Time, bool) {
	switch v := raw.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if ts, err := time.Parse(layout, strings.TrimSpace(v)); err == nil {
				return ts, true
			}
		}
	case float64:
		if v <= 0 {
			return time.Time{}, false
		}
		if v > 1e16 {
			return time.Unix(0, int64(v)), true
		}
		if v > 1e12 {
			return time.UnixMilli(int64(v)), true
		}
		return time.Unix(int64(v), 0), true
	case int64:
		if v > 0 {
			if v > 1e16 {
				return time.Unix(0, v), true
			}
			if v > 1e12 {
				return time.UnixMilli(v), true
			}
			return time.Unix(v, 0), true
		}
	}
	return time.Time{}, false
}

func stringifyFirst(obj map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		if value, ok := scalarString(raw); ok && value != "" {
			return value
		}
	}
	return ""
}

func scalarString(v interface{}) (string, bool) {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value), true
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", false
		}
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10), true
		}
		return strconv.FormatFloat(value, 'f', -1, 64), true
	case bool:
		if value {
			return "true", true
		}
		return "false", true
	case int:
		return strconv.Itoa(value), true
	case int64:
		return strconv.FormatInt(value, 10), true
	case uint64:
		return strconv.FormatUint(value, 10), true
	default:
		return "", false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeIdentity(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 160 {
		return value[:160]
	}
	return value
}

func normalizeName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "[](){}<>\"'`")
	if strings.HasSuffix(strings.ToLower(value), ".service") {
		value = strings.TrimSuffix(value, ".service")
	}
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func normalizePID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return ""
		}
	}
	if len(value) > 20 {
		return value[:20]
	}
	return value
}

func normalizeSource(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "[](){}<>\"'`")
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func normalizeMessage(message string, maxBytes int) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if maxBytes > 0 && len(message) > maxBytes {
		return message[:maxBytes]
	}
	return message
}

func normalizeLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "debug", "dbg", "trace", "verbose":
		return LevelDebug
	case "info", "notice", "information":
		return LevelInfo
	case "warn", "warning", "deprecated":
		return LevelWarn
	case "err", "error", "critical", "crit":
		return LevelError
	case "fatal", "panic", "emerg", "alert":
		return LevelFatal
	case "":
		return ""
	default:
		return LevelUnknown
	}
}

func deriveLevel(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "panic"),
		strings.Contains(lower, "fatal"),
		strings.Contains(lower, "emerg"):
		return LevelFatal
	case strings.Contains(lower, "error"),
		strings.Contains(lower, "exception"),
		strings.Contains(lower, "failed"),
		strings.Contains(lower, "critical"):
		return LevelError
	case strings.Contains(lower, "warn"),
		strings.Contains(lower, "deprecated"):
		return LevelWarn
	case strings.Contains(lower, "debug"),
		strings.Contains(lower, "trace"):
		return LevelDebug
	default:
		return LevelInfo
	}
}

func deriveService(entry Entry, parsed parsedFields) string {
	if entry.Process != "" {
		return entry.Process
	}
	if parsed.process != "" {
		return normalizeName(parsed.process)
	}
	if v, ok := entry.Labels["service"]; ok {
		return normalizeName(v)
	}
	if v, ok := entry.Labels["app"]; ok {
		return normalizeName(v)
	}
	return ""
}

func deriveProcess(message string) string {
	line := strings.TrimSpace(message)
	if line == "" {
		return ""
	}
	if match := processPrefixPattern.FindStringSubmatch(line); len(match) == 4 {
		return normalizeName(match[1])
	}
	if idx := strings.Index(line, ":"); idx > 0 {
		prefix := strings.TrimSpace(line[:idx])
		if slash := strings.LastIndex(prefix, "/"); slash >= 0 && slash+1 < len(prefix) {
			prefix = prefix[slash+1:]
		}
		if prefix != "" {
			return normalizeName(prefix)
		}
	}
	return ""
}

func mergeLabels(cfg Config, sources ...map[string]string) map[string]string {
	limit := cfg.MaxLabelsPerEntry
	if limit <= 0 {
		limit = 24
	}

	merged := make(map[string]string)
	for _, labels := range sources {
		if len(labels) == 0 {
			continue
		}
		for key, value := range labels {
			normalizedKey := normalizeLabelKey(key)
			if normalizedKey == "" {
				continue
			}
			normalizedValue := strings.TrimSpace(value)
			if normalizedValue == "" {
				continue
			}
			if len(normalizedValue) > 128 {
				normalizedValue = normalizedValue[:128]
			}
			merged[normalizedKey] = normalizedValue
			if len(merged) >= limit {
				return merged
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func normalizeLabelKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		return ""
	}
	if len(key) > 64 {
		key = key[:64]
	}
	builder := strings.Builder{}
	builder.Grow(len(key))
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '_', r == '-', r == '.':
			builder.WriteRune(r)
		}
	}
	result := strings.Trim(builder.String(), "-_.")
	if result == "" {
		return ""
	}
	return result
}

func cloneMetrics(cfg Config, metrics map[string]float64) map[string]float64 {
	if len(metrics) == 0 {
		return nil
	}

	limit := cfg.MaxMetricsPerEntry
	if limit <= 0 {
		limit = 12
	}

	keys := make([]string, 0, len(metrics))
	for key, value := range metrics {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}

	out := make(map[string]float64, len(keys))
	for _, key := range keys {
		out[key] = metrics[key]
	}
	return out
}

func extractKV(line, key string) string {
	if line == "" || key == "" {
		return ""
	}
	lower := strings.ToLower(line)
	target := strings.ToLower(strings.TrimSpace(key)) + "="
	idx := strings.Index(lower, target)
	if idx < 0 {
		return ""
	}
	start := idx + len(target)
	if start >= len(line) {
		return ""
	}

	rest := line[start:]
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return ""
	}

	if rest[0] == '"' || rest[0] == '\'' {
		quote := rest[0]
		rest = rest[1:]
		if end := strings.IndexByte(rest, quote); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
		return strings.TrimSpace(rest)
	}

	end := len(rest)
	for i, ch := range rest {
		if ch == ' ' || ch == '\t' || ch == ',' || ch == ';' {
			end = i
			break
		}
	}
	return strings.Trim(rest[:end], "[](){}<>\"'`")
}
