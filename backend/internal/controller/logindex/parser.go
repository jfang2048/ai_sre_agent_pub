// Package logindex provides enhanced log parsing capabilities for the native log indexing system.
// It supports multiple log formats including JSON, syslog (RFC3164, RFC5424), and common application logs.
package logindex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Parser represents a log parser that can convert raw log lines into structured fields.
type Parser interface {
	// Parse attempts to parse a log line and returns extracted fields.
	// Returns nil if the line doesn't match this parser's format.
	Parse(line string) map[string]string
}

// FormatDetector automatically detects the log format and selects the appropriate parser.
type FormatDetector struct {
	parsers []Parser
}

// NewFormatDetector creates a new format detector with default parsers.
func NewFormatDetector() *FormatDetector {
	return &FormatDetector{
		parsers: []Parser{
			&JSONParser{},
			&SyslogParser{},
			&CommonLogParser{},
			&CombinedLogParser{},
			&NginxErrorParser{},
			&ApacheErrorParser{},
			&GenericAppLogParser{},
		},
	}
}

// Detect attempts to parse a line using all registered parsers.
// Returns the first successful parse result.
func (d *FormatDetector) Detect(line string) map[string]string {
	for _, parser := range d.parsers {
		if fields := parser.Parse(line); fields != nil {
			return fields
		}
	}
	return nil
}

// AddParser adds a custom parser to the detector.
func (d *FormatDetector) AddParser(parser Parser) {
	d.parsers = append(d.parsers, parser)
}

// JSONParser parses JSON-formatted log lines.
type JSONParser struct{}

// Parse attempts to parse the line as JSON.
func (p *JSONParser) Parse(line string) map[string]string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
		return nil
	}

	var data map[string]interface{}
	if err := json.NewDecoder(strings.NewReader(line)).Decode(&data); err != nil {
		return nil
	}

	fields := make(map[string]string, len(data))
	flattenJSON("", data, fields)
	return fields
}

// flattenJSON recursively flattens nested JSON structures into dot-separated keys.
func flattenJSON(prefix string, data map[string]interface{}, out map[string]string) {
	for key, value := range data {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := value.(type) {
		case map[string]interface{}:
			flattenJSON(fullKey, v, out)
		case string:
			out[fullKey] = v
		case float64:
			out[fullKey] = formatFloat64(v)
		case bool:
			out[fullKey] = strconv.FormatBool(v)
		case nil:
			out[fullKey] = "null"
		default:
			out[fullKey] = fmt.Sprintf("%v", v)
		}
	}
}

func formatFloat64(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// SyslogParser parses RFC3164 and RFC5424 syslog messages.
type SyslogParser struct {
	rfc5424Regex *regexp.Regexp
	rfc3164Regex *regexp.Regexp
}

// NewSyslogParser creates a new syslog parser.
func NewSyslogParser() *SyslogParser {
	return &SyslogParser{
		rfc5424Regex: regexp.MustCompile(`^\<(\d+)\>(\d+) (\S+) (\S+) (\S+) (\S+) (\S+) (.+)$`),
		rfc3164Regex: regexp.MustCompile(`^\<(\d+)\>(\w{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+(\S+)\s+(.+)$`),
	}
}

// Parse attempts to parse the line as syslog.
func (p *SyslogParser) Parse(line string) map[string]string {
	line = strings.TrimSpace(line)

	// Ensure regexes are initialized
	if p.rfc5424Regex == nil {
		p.rfc5424Regex = regexp.MustCompile(`^\<(\d+)\>(\d+) (\S+) (\S+) (\S+) (\S+) (\S+) (.+)$`)
	}
	if p.rfc3164Regex == nil {
		p.rfc3164Regex = regexp.MustCompile(`^\<(\d+)\>(\w{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+(\S+)\s+(.+)$`)
	}

	// Try RFC5424 first
	if matches := p.rfc5424Regex.FindStringSubmatch(line); matches != nil {
		return p.parseRFC5424(matches)
	}

	// Try RFC3164
	if matches := p.rfc3164Regex.FindStringSubmatch(line); matches != nil {
		return p.parseRFC3164(matches)
	}

	return nil
}

func (p *SyslogParser) parseRFC5424(matches []string) map[string]string {
	priority, _ := strconv.Atoi(matches[1])
	fields := map[string]string{
		"format":      "syslog-rfc5424",
		"priority":    strconv.Itoa(priority),
		"facility":    strconv.Itoa(priority / 8),
		"severity":    strconv.Itoa(priority % 8),
		"version":     matches[2],
		"timestamp":   matches[3],
		"hostname":    matches[4],
		"app_name":    matches[5],
		"procid":      matches[6],
		"msgid":       matches[7],
		"message":     matches[8],
		"service":     matches[5],
		"log.format":  "syslog",
		"log.version": "rfc5424",
	}
	return fields
}

func (p *SyslogParser) parseRFC3164(matches []string) map[string]string {
	priority, _ := strconv.Atoi(matches[1])
	fields := map[string]string{
		"format":      "syslog-rfc3164",
		"priority":    strconv.Itoa(priority),
		"facility":    strconv.Itoa(priority / 8),
		"severity":    strconv.Itoa(priority % 8),
		"timestamp":   matches[2],
		"hostname":    matches[3],
		"message":     matches[4],
		"log.format":  "syslog",
		"log.version": "rfc3164",
	}
	return fields
}

// CommonLogParser parses Apache/Nginx common log format.
type CommonLogParser struct {
	regex *regexp.Regexp
}

// NewCommonLogParser creates a new common log parser.
func NewCommonLogParser() *CommonLogParser {
	// Common Log Format: host ident authuser date request status bytes
	regex := regexp.MustCompile(`^(\S+) (\S+) (\S+) \[([^\]]+)\] "(\S+) (\S+) (\S+)" (\d+) (\d+|-)`)
	return &CommonLogParser{regex: regex}
}

// Parse attempts to parse the line as common log format.
func (p *CommonLogParser) Parse(line string) map[string]string {
	if p.regex == nil {
		p.regex = regexp.MustCompile(`^(\S+) (\S+) (\S+) \[([^\]]+)\] "(\S+) (\S+) (\S+)" (\d+) (\d+|-)`)
	}

	matches := p.regex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	statusCode, _ := strconv.Atoi(matches[8])
	byteSize := matches[9]
	if byteSize == "-" {
		byteSize = "0"
	}

	fields := map[string]string{
		"format":          "common-log",
		"remote_addr":     matches[1],
		"remote_user":     matches[3],
		"time_local":      matches[4],
		"request":         matches[5] + " " + matches[6] + " " + matches[7],
		"method":          matches[5],
		"request_uri":     matches[6],
		"protocol":        matches[7],
		"status":          matches[8],
		"status_code":     matches[8],
		"body_bytes_sent": byteSize,
		"log.format":      "http",
		"log.type":        "access",
	}

	// Categorize status
	if statusCode >= 500 {
		fields["level"] = "error"
		fields["severity"] = "5xx"
	} else if statusCode >= 400 {
		fields["level"] = "warn"
		fields["severity"] = "4xx"
	} else if statusCode >= 300 {
		fields["severity"] = "3xx"
	} else if statusCode >= 200 {
		fields["severity"] = "2xx"
	}

	return fields
}

// CombinedLogParser parses Apache/Nginx combined log format.
type CombinedLogParser struct {
	regex *regexp.Regexp
}

// NewCombinedLogParser creates a new combined log parser.
func NewCombinedLogParser() *CombinedLogParser {
	// Combined Log Format: common + "referer" "user-agent"
	regex := regexp.MustCompile(`^(\S+) (\S+) (\S+) \[([^\]]+)\] "(\S+) (\S+) (\S+)" (\d+) (\d+|-) "([^"]*)" "([^"]*)"`)
	return &CombinedLogParser{regex: regex}
}

// Parse attempts to parse the line as combined log format.
func (p *CombinedLogParser) Parse(line string) map[string]string {
	if p.regex == nil {
		p.regex = regexp.MustCompile(`^(\S+) (\S+) (\S+) \[([^\]]+)\] "(\S+) (\S+) (\S+)" (\d+) (\d+|-) "([^"]*)" "([^"]*)"`)
	}

	matches := p.regex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	// Start with common log fields
	fields := NewCommonLogParser().Parse(line)
	if fields == nil {
		return nil
	}

	fields["format"] = "combined-log"
	fields["http_referer"] = matches[10]
	fields["http_user_agent"] = matches[11]

	return fields
}

// NginxErrorParser parses nginx error log format.
type NginxErrorParser struct {
	regex *regexp.Regexp
}

// NewNginxErrorParser creates a new nginx error parser.
func NewNginxErrorParser() *NginxErrorParser {
	// 2024/02/24 14:30:00 [error] 12345#0: *12345 client denied by server
	regex := regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(\w+)\] (\d+)#(\d+): (\*?\d+) (.*)`)
	return &NginxErrorParser{regex: regex}
}

// Parse attempts to parse the line as nginx error format.
func (p *NginxErrorParser) Parse(line string) map[string]string {
	if p.regex == nil {
		p.regex = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(\w+)\] (\d+)#(\d+): (\*?\d+) (.*)`)
	}

	matches := p.regex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	fields := map[string]string{
		"format":     "nginx-error",
		"time_local": matches[1],
		"level":      normalizeLevel(matches[2]),
		"pid":        matches[3],
		"tid":        matches[4],
		"cid":        matches[5],
		"message":    matches[6],
		"service":    "nginx",
		"log.format": "nginx",
		"log.type":   "error",
	}

	return fields
}

// ApacheErrorParser parses apache error log format.
type ApacheErrorParser struct {
	regex *regexp.Regexp
}

// NewApacheErrorParser creates a new apache error parser.
func NewApacheErrorParser() *ApacheErrorParser {
	// [Day Mon DD HH:MM:SS YYYY] [module:level] message
	regex := regexp.MustCompile(`^\[([^\]]+)\] \[([^\]]+)\] (.+)`)
	return &ApacheErrorParser{regex: regex}
}

// Parse attempts to parse the line as apache error format.
func (p *ApacheErrorParser) Parse(line string) map[string]string {
	if p.regex == nil {
		p.regex = regexp.MustCompile(`^\[([^\]]+)\] \[([^\]]+)\] (.+)`)
	}

	matches := p.regex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	timestamp := matches[1]
	moduleLevel := matches[2]
	message := matches[3]

	// Parse module:level
	parts := strings.Split(moduleLevel, ":")
	level := "error"
	module := "core"
	if len(parts) >= 2 {
		module = parts[0]
		level = normalizeLevel(parts[len(parts)-1])
	}

	fields := map[string]string{
		"format":     "apache-error",
		"timestamp":  timestamp,
		"module":     module,
		"level":      level,
		"message":    message,
		"service":    "apache",
		"log.format": "apache",
		"log.type":   "error",
	}

	return fields
}

// GenericAppLogParser parses common application log patterns.
type GenericAppLogParser struct {
	patterns []*regexp.Regexp
}

// NewGenericAppLogParser creates a new generic application log parser.
func NewGenericAppLogParser() *GenericAppLogParser {
	return &GenericAppLogParser{
		patterns: []*regexp.Regexp{
			// 2024-02-24 14:30:00 INFO module message
			regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?)\s+(\w+)\s+(\S+)\s+(.+)`),
			// INFO [module] message
			regexp.MustCompile(`^(\w+)\s+\[(\w+)\]\s+(.+)`),
			// [INFO] [module] message
			regexp.MustCompile(`^\[(\w+)\]\s+\[(\w+)\]\s+(.+)`),
			// module:INFO:message
			regexp.MustCompile(`^(\w+):(\w+):(.+)`),
			// [timestamp] level service message
			regexp.MustCompile(`^\[([^\]]+)\]\s+(\w+)\s+(\S+)\s+(.+)`),
		},
	}
}

// Parse attempts to parse the line using common application patterns.
func (p *GenericAppLogParser) Parse(line string) map[string]string {
	for _, regex := range p.patterns {
		if matches := regex.FindStringSubmatch(line); matches != nil {
			return p.parseMatches(matches, regex.String())
		}
	}
	return nil
}

func (p *GenericAppLogParser) parseMatches(matches []string, pattern string) map[string]string {
	fields := make(map[string]string)
	fields["format"] = "generic-app"
	fields["log.format"] = "application"

	// Different patterns have different capture groups
	switch {
	case len(matches) == 5 && regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}`).MatchString(matches[1]):
		// Pattern: timestamp level module message
		fields["timestamp"] = matches[1]
		fields["level"] = normalizeLevel(matches[2])
		fields["service"] = matches[3]
		fields["module"] = matches[3]
		fields["message"] = matches[4]

	case len(matches) == 4:
		// Pattern: level [module] message OR [level] [module] message
		fields["level"] = normalizeLevel(matches[1])
		fields["module"] = matches[2]
		fields["service"] = matches[2]
		fields["message"] = matches[3]

	case len(matches) == 5 && strings.HasPrefix(matches[1], "["):
		// Pattern: [timestamp] level service message
		fields["timestamp"] = matches[1]
		fields["level"] = normalizeLevel(matches[2])
		fields["service"] = matches[3]
		fields["module"] = matches[3]
		fields["message"] = matches[4]
	}

	return fields
}

// FieldExtractor extracts custom fields from log lines using regex patterns.
type FieldExtractor struct {
	patterns map[string]*regexp.Regexp
}

// NewFieldExtractor creates a new field extractor.
func NewFieldExtractor() *FieldExtractor {
	return &FieldExtractor{
		patterns: make(map[string]*regexp.Regexp),
	}
}

// AddPattern adds a field extraction pattern.
// The pattern should have a named capture group for the field value.
func (e *FieldExtractor) AddPattern(fieldName, pattern string) error {
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid regex pattern for %s: %w", fieldName, err)
	}
	e.patterns[fieldName] = regex
	return nil
}

// Extract extracts fields from a log line using configured patterns.
func (e *FieldExtractor) Extract(line string) map[string]string {
	if len(e.patterns) == 0 {
		return nil
	}

	fields := make(map[string]string)
	for fieldName, regex := range e.patterns {
		if matches := regex.FindStringSubmatch(line); matches != nil {
			// Look for named capture groups
			subexpNames := regex.SubexpNames()
			for i, name := range subexpNames {
				if i > 0 && i < len(matches) && name != "" {
					fields[name] = matches[i]
				}
			}
			// If no named groups, use first capture
			if len(matches) > 1 {
				fields[fieldName] = matches[1]
			}
		}
	}

	if len(fields) == 0 {
		return nil
	}
	return fields
}

// ParseTimestamp attempts to parse various timestamp formats.
func ParseTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)

	// Try Unix timestamps
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		switch {
		case seconds > 1e16:
			return time.Unix(0, seconds), true
		case seconds > 1e12:
			return time.UnixMilli(seconds), true
		default:
			return time.Unix(seconds, 0), true
		}
	}

	// Try common timestamp formats
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.RFC1123,
		time.RFC1123Z,
		"2006-01-02T15:04:05.999999999Z0700",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05Z0700",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04:05.999",
		"2006/01/02",
		"Jan 02 15:04:05",
		"Jan 02 15:04:05 2006",
		"_2 Jan 2006 15:04:05",
	}

	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}

	return time.Time{}, false
}

// ExtractLevel attempts to extract log level from text.
func ExtractLevel(text string) string {
	text = strings.ToLower(text)

	// Common level patterns - check in order of specificity
	levelPatterns := []struct {
		pattern string
		level   string
	}{
		{"failed", LevelError},
		{"failure", LevelError},
		{"timeout", LevelError},
		{"exception", LevelError},
		{"fatal", LevelFatal},
		{"emerg", LevelError},
		{"emergency", LevelError},
		{"crit", LevelError},
		{"critical", LevelError},
		{"error", LevelError},
		{"err", LevelError},
		{"warning", LevelWarn},
		{"warn", LevelWarn},
		{"high memory", LevelWarn},
		{"degraded", LevelWarn},
		{"notice", LevelInfo},
		{"info", LevelInfo},
		{"success", LevelInfo},
		{"completed", LevelInfo},
		{"processed", LevelInfo},
		{"debug", LevelDebug},
		{"trace", LevelDebug},
	}

	for _, lp := range levelPatterns {
		if strings.Contains(text, lp.pattern) {
			return lp.level
		}
	}

	// Check for numeric severity (syslog style)
	if strings.Contains(text, "sev=") || strings.Contains(text, "severity=") {
		parts := strings.Fields(text)
		for _, part := range parts {
			if strings.HasPrefix(part, "sev=") || strings.HasPrefix(part, "severity=") {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) == 2 {
					if sev, err := strconv.Atoi(kv[1]); err == nil {
						return severityToLevel(sev)
					}
				}
			}
		}
	}

	return ""
}

func severityToLevel(severity int) string {
	switch {
	case severity == 0 || severity == 1 || severity == 2:
		return LevelError
	case severity == 3:
		return LevelError
	case severity == 4:
		return LevelWarn
	case severity == 5, severity == 6:
		return LevelInfo
	default:
		return LevelDebug
	}
}

// SafeSubString extracts a substring safely.
func SafeSubString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	// Try to break at word boundary
	if idx := strings.LastIndex(s[:maxLen], " "); idx > maxLen/2 {
		return s[:idx] + "..."
	}

	return s[:maxLen] + "..."
}

// TruncateValue truncates a value to maximum length.
func TruncateValue(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

// SanitizeField sanitizes a field name.
func SanitizeField(field string) string {
	field = strings.TrimSpace(field)
	field = strings.ToLower(field)

	// Replace non-alphanumeric (except underscore, dot) with underscore
	result := bytes.Buffer{}
	result.Grow(len(field))

	for _, r := range field {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			result.WriteRune(r)
		case r == '_' || r == '.':
			result.WriteRune(r)
		default:
			result.WriteRune('_')
		}
	}

	return result.String()
}
