package logindex

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// QueryParser parses search queries with boolean operators and advanced syntax.
type QueryParser struct {
	maxTokens int
}

// NewQueryParser creates a new query parser.
func NewQueryParser() *QueryParser {
	return &QueryParser{
		maxTokens: 32,
	}
}

// ParsedQuery represents a parsed query with structured components.
type ParsedQuery struct {
	Original     string
	TextTerms    []string
	NotTerms     []string
	FieldFilters map[string][]string
	Level        string
	Since        time.Time
	Until        time.Time
	TimeRange    *TimeRange
}

// TimeRange represents a time range constraint.
type TimeRange struct {
	From time.Time
	To   time.Time
}

// Parse parses a query string into structured components.
func (p *QueryParser) Parse(query string) (*ParsedQuery, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return &ParsedQuery{}, nil
	}

	parsed := &ParsedQuery{
		Original:     query,
		TextTerms:    make([]string, 0),
		NotTerms:     make([]string, 0),
		FieldFilters: make(map[string][]string),
	}

	// Tokenize while preserving quotes
	tokens, err := p.tokenize(query)
	if err != nil {
		return nil, fmt.Errorf("tokenization error: %w", err)
	}

	// Process tokens
	i := 0
	for i < len(tokens) {
		token := tokens[i]

		switch strings.ToUpper(token) {
		case "AND":
			i++
			continue

		case "OR":
			// OR is implicit in our search model
			i++
			continue

		case "NOT":
			if i+1 < len(tokens) {
				parsed.NotTerms = append(parsed.NotTerms, tokens[i+1])
				i += 2
			} else {
				i++
			}
			continue

		case "LEVEL":
			if i+1 < len(tokens) {
				parsed.Level = normalizeLevel(tokens[i+1])
				i += 2
			} else {
				i++
			}
			continue

		case "SINCE", "FROM", "AFTER":
			if i+1 < len(tokens) {
				if ts, ok := parseTimeToken(tokens[i+1]); ok {
					parsed.Since = ts
					i += 2
				} else {
					i++
				}
			} else {
				i++
			}
			continue

		case "UNTIL", "TO", "BEFORE":
			if i+1 < len(tokens) {
				if ts, ok := parseTimeToken(tokens[i+1]); ok {
					parsed.Until = ts
					i += 2
				} else {
					i++
				}
			} else {
				i++
			}
			continue
		}

		// Check for field:value pattern
		if strings.Contains(token, ":") && !strings.HasPrefix(token, "\"") {
			parts := strings.SplitN(token, ":", 2)
			if len(parts) == 2 && parts[0] != "" {
				field := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				if value != "" {
					parsed.FieldFilters[field] = append(parsed.FieldFilters[field], value)
					i++
					continue
				}
			}
		}

		// Regular text term
		parsed.TextTerms = append(parsed.TextTerms, token)
		i++
	}

	// Apply token limit
	if len(parsed.TextTerms) > p.maxTokens {
		parsed.TextTerms = parsed.TextTerms[:p.maxTokens]
	}
	if len(parsed.NotTerms) > p.maxTokens {
		parsed.NotTerms = parsed.NotTerms[:p.maxTokens]
	}

	return parsed, nil
}

// tokenize splits the query into tokens while respecting quoted strings.
func (p *QueryParser) tokenize(query string) ([]string, error) {
	tokens := make([]string, 0)
	current := strings.Builder{}
	inQuotes := false
	inEscape := false

	for _, r := range query {
		switch {
		case inEscape:
			current.WriteRune(r)
			inEscape = false

		case r == '\\':
			inEscape = true

		case r == '"':
			inQuotes = !inQuotes

		case r == ' ' || r == '\t':
			if inQuotes {
				current.WriteRune(r)
			} else if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}

		default:
			current.WriteRune(r)
		}
	}

	if inQuotes {
		return nil, fmt.Errorf("unclosed quote")
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens, nil
}

// parseTimeToken accepts relative expressions such as "5m ago" and absolute timestamps.
func parseTimeToken(token string) (time.Time, bool) {
	token = strings.TrimSpace(token)

	// Try relative time
	if strings.HasSuffix(strings.ToLower(token), "ago") {
		prefix := strings.TrimSuffix(strings.ToLower(token), "ago")
		prefix = strings.TrimSpace(prefix)

		duration, err := time.ParseDuration(prefix)
		if err == nil {
			return time.Now().Add(-duration), true
		}

		parts := strings.Fields(prefix)
		if len(parts) == 2 {
			if val, err := strconv.Atoi(parts[0]); err == nil {
				unit := strings.ToLower(parts[1])
				switch {
				case strings.HasPrefix(unit, "s"):
					return time.Now().Add(-time.Duration(val) * time.Second), true
				case strings.HasPrefix(unit, "min"):
					return time.Now().Add(-time.Duration(val) * time.Minute), true
				case strings.HasPrefix(unit, "h"):
					return time.Now().Add(-time.Duration(val) * time.Hour), true
				case strings.HasPrefix(unit, "d"):
					return time.Now().Add(-time.Duration(val) * 24 * time.Hour), true
				case strings.HasPrefix(unit, "w"):
					return time.Now().Add(-time.Duration(val) * 7 * 24 * time.Hour), true
				}
			}
		}
	}

	return ParseTimestamp(token)
}

// ToSearchQuery converts a ParsedQuery to a SearchQuery.
func (pq *ParsedQuery) ToSearchQuery() SearchQuery {
	query := SearchQuery{
		Text:  strings.Join(pq.TextTerms, " "),
		Level: pq.Level,
		Since: pq.Since,
		Until: pq.Until,
	}

	for field, values := range pq.FieldFilters {
		if len(values) > 0 {
			value := values[0]
			switch strings.ToLower(field) {
			case "host", "hostname":
				query.Hostname = value
			case "service", "app":
				query.Service = value
			case "process", "proc":
				query.Process = value
			case "pid":
				query.PID = value
			case "source", "src":
				query.Source = value
			case "collector", "node":
				query.CollectorID = value
			}
		}
	}

	return query
}

// AdvancedQuery represents an advanced query with boolean logic.
type AdvancedQuery struct {
	And []AdvancedQuery
	Or  []AdvancedQuery
	Not *AdvancedQuery

	Text       string
	FieldName  string
	FieldValue string
	Wildcard   string
	RangeFrom  float64
	RangeTo    float64
}

// Eval evaluates an AdvancedQuery against a log entry.
func (aq *AdvancedQuery) Eval(entry Entry) bool {
	if aq == nil {
		return true
	}

	// AND: all must match
	if len(aq.And) > 0 {
		for _, child := range aq.And {
			if !child.Eval(entry) {
				return false
			}
		}
		return true
	}

	// OR: any must match
	if len(aq.Or) > 0 {
		for _, child := range aq.Or {
			if child.Eval(entry) {
				return true
			}
		}
		return false
	}

	// NOT: invert
	if aq.Not != nil {
		return !aq.Not.Eval(entry)
	}

	// Text search
	if aq.Text != "" {
		return strings.Contains(
			strings.ToLower(entry.Message),
			strings.ToLower(aq.Text),
		)
	}

	// Field equality
	if aq.FieldName != "" && aq.FieldValue != "" {
		fieldValue := aq.getFieldValue(entry, aq.FieldName)
		return strings.EqualFold(fieldValue, aq.FieldValue)
	}

	// Wildcard match
	if aq.Wildcard != "" {
		pattern := regexp.QuoteMeta(aq.Wildcard)
		pattern = "^" + strings.ReplaceAll(pattern, "*", ".*") + "$"
		if regex, err := regexp.Compile(pattern); err == nil {
			haystack := buildSearchHaystack(entry)
			return regex.MatchString(strings.ToLower(haystack))
		}
	}

	// Range query
	if aq.RangeFrom != 0 || aq.RangeTo != 0 {
		if aq.FieldName == "" {
			return false
		}
		valueStr := aq.getFieldValue(entry, aq.FieldName)
		if value, err := strconv.ParseFloat(valueStr, 64); err == nil {
			if aq.RangeFrom != 0 && value < aq.RangeFrom {
				return false
			}
			if aq.RangeTo != 0 && value > aq.RangeTo {
				return false
			}
			return true
		}
		return false
	}

	return true
}

func (aq *AdvancedQuery) getFieldValue(entry Entry, field string) string {
	switch strings.ToLower(field) {
	case "message", "msg":
		return entry.Message
	case "host", "hostname":
		return entry.Hostname
	case "service", "app":
		return entry.Service
	case "process", "proc":
		return entry.Process
	case "pid":
		return entry.PID
	case "level", "severity":
		return entry.Level
	case "source", "src":
		return entry.Source
	case "collector", "node":
		return entry.CollectorID
	default:
		// Check labels
		if entry.Labels != nil {
			if val, ok := entry.Labels[field]; ok {
				return val
			}
		}
		return ""
	}
}

// QueryBuilder helps build advanced queries programmatically.
type QueryBuilder struct {
	root *AdvancedQuery
}

// NewQueryBuilder creates a new query builder.
func NewQueryBuilder() *QueryBuilder {
	return &QueryBuilder{
		root: &AdvancedQuery{},
	}
}

// WithText adds a text search term.
func (qb *QueryBuilder) WithText(text string) *QueryBuilder {
	if qb.root.Text == "" {
		qb.root.Text = text
	} else {
		qb.root.And = append(qb.root.And, AdvancedQuery{Text: text})
	}
	return qb
}

// WithField adds a field equality filter.
func (qb *QueryBuilder) WithField(field, value string) *QueryBuilder {
	qb.root.And = append(qb.root.And, AdvancedQuery{
		FieldName:  field,
		FieldValue: value,
	})
	return qb
}

// WithWildcard adds a wildcard search term.
func (qb *QueryBuilder) WithWildcard(pattern string) *QueryBuilder {
	qb.root.And = append(qb.root.And, AdvancedQuery{
		Wildcard: pattern,
	})
	return qb
}

// WithRange adds a numeric range filter.
func (qb *QueryBuilder) WithRange(field string, from, to float64) *QueryBuilder {
	qb.root.And = append(qb.root.And, AdvancedQuery{
		FieldName: field,
		RangeFrom: from,
		RangeTo:   to,
	})
	return qb
}

// WithLevel adds a log level filter.
func (qb *QueryBuilder) WithLevel(level string) *QueryBuilder {
	return qb.WithField("level", normalizeLevel(level))
}

// WithTimeRange adds a time range constraint.
func (qb *QueryBuilder) WithTimeRange(since, until time.Time) *QueryBuilder {
	// Time range is handled separately in SearchQuery
	return qb
}

// Not returns a new builder for NOT queries.
func (qb *QueryBuilder) Not() *QueryBuilder {
	return &QueryBuilder{
		root: &AdvancedQuery{
			Not: qb.root,
		},
	}
}

// Or returns a new builder for OR queries.
func (qb *QueryBuilder) Or(other *QueryBuilder) *QueryBuilder {
	return &QueryBuilder{
		root: &AdvancedQuery{
			Or: []AdvancedQuery{*qb.root, *other.root},
		},
	}
}

// Build returns the constructed advanced query.
func (qb *QueryBuilder) Build() *AdvancedQuery {
	return qb.root
}

// String returns a string representation of the query.
func (qb *QueryBuilder) String() string {
	if qb.root == nil {
		return ""
	}
	return qb.root.String()
}

func (aq *AdvancedQuery) String() string {
	if aq == nil {
		return ""
	}

	var builder strings.Builder

	if len(aq.And) > 0 {
		parts := make([]string, len(aq.And))
		for i, child := range aq.And {
			parts[i] = child.String()
		}
		builder.WriteString("(")
		builder.WriteString(strings.Join(parts, " AND "))
		builder.WriteString(")")
	}

	if len(aq.Or) > 0 {
		parts := make([]string, len(aq.Or))
		for i, child := range aq.Or {
			parts[i] = child.String()
		}
		if builder.Len() > 0 {
			builder.WriteString(" ")
		}
		builder.WriteString("(")
		builder.WriteString(strings.Join(parts, " OR "))
		builder.WriteString(")")
	}

	if aq.Not != nil {
		if builder.Len() > 0 {
			builder.WriteString(" ")
		}
		builder.WriteString("NOT ")
		builder.WriteString(aq.Not.String())
	}

	if aq.Text != "" {
		if builder.Len() > 0 {
			builder.WriteString(" ")
		}
		builder.WriteString(aq.Text)
	}

	if aq.FieldName != "" && aq.FieldValue != "" {
		if builder.Len() > 0 {
			builder.WriteString(" ")
		}
		builder.WriteString(aq.FieldName)
		builder.WriteString(":")
		builder.WriteString(aq.FieldValue)
	}

	if aq.Wildcard != "" {
		if builder.Len() > 0 {
			builder.WriteString(" ")
		}
		builder.WriteString(aq.Wildcard)
	}

	return builder.String()
}

// QueryOptimizer optimizes queries for better performance.
type QueryOptimizer struct{}

// NewQueryOptimizer creates a new query optimizer.
func NewQueryOptimizer() *QueryOptimizer {
	return &QueryOptimizer{}
}

// Optimize optimizes a search query for better performance.
func (qo *QueryOptimizer) Optimize(query SearchQuery) SearchQuery {
	optimized := query

	// Normalize time range
	if !optimized.Since.IsZero() && !optimized.Until.IsZero() {
		if optimized.Since.After(optimized.Until) {
			optimized.Since, optimized.Until = optimized.Until, optimized.Since
		}
	}

	// Apply reasonable defaults
	if optimized.Since.IsZero() && optimized.Until.IsZero() {
		optimized.Until = time.Now().UTC()
		optimized.Since = optimized.Until.Add(-15 * time.Minute)
	} else if optimized.Until.IsZero() {
		optimized.Until = time.Now().UTC()
	}

	// Normalize text search
	optimized.Text = strings.TrimSpace(optimized.Text)
	if optimized.Text != "" {
		// Extract quoted phrases
		quoted := extractQuotedPhrases(optimized.Text)
		if len(quoted) > 0 {
			optimized.Text = strings.Join(quoted, " ")
		}
	}

	// Normalize level
	optimized.Level = normalizeLevel(optimized.Level)

	return optimized
}

// extractQuotedPhrases extracts quoted phrases from text.
func extractQuotedPhrases(text string) []string {
	var phrases []string
	inQuotes := false
	current := strings.Builder{}

	for _, r := range text {
		switch r {
		case '"':
			if inQuotes {
				if current.Len() > 0 {
					phrases = append(phrases, current.String())
					current.Reset()
				}
			}
			inQuotes = !inQuotes
		default:
			if inQuotes {
				current.WriteRune(r)
			}
		}
	}

	return phrases
}

// EstimateSelectivity estimates how selective a query is (0.0 to 1.0).
// Lower values indicate more selective queries.
func (qo *QueryOptimizer) EstimateSelectivity(query SearchQuery) float64 {
	selectivity := 1.0

	// Time range reduces selectivity
	if !query.Since.IsZero() || !query.Until.IsZero() {
		window := query.Until.Sub(query.Since)
		// Assume 24h window as baseline
		if window < 24*time.Hour {
			selectivity *= float64(window.Hours()) / 24.0
		}
	}

	// Level filter reduces selectivity
	if query.Level != "" {
		selectivity *= 0.3 // Assume 30% of logs are any given level
	}

	// Service filter reduces selectivity
	if query.Service != "" {
		selectivity *= 0.2 // Assume 20% of logs per service
	}

	// Hostname filter reduces selectivity
	if query.Hostname != "" {
		selectivity *= 0.1 // Assume 10% of logs per host
	}

	// Text search further reduces selectivity
	if query.Text != "" {
		selectivity *= 0.1 // Assume 10% contain any given text
	}

	// Clamp between 0.001 and 1.0
	if selectivity < 0.001 {
		selectivity = 0.001
	}
	if selectivity > 1.0 {
		selectivity = 1.0
	}

	return selectivity
}
