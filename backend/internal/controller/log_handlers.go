package controller

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
)

const (
	maxServiceLogIngestEntries = 5000
	maxServiceLogBodyBytes     = 4 << 20
)

type serviceLogsIngestRequest struct {
	CollectorID string            `json:"collector_id"`
	Hostname    string            `json:"hostname"`
	Service     string            `json:"service"`
	Source      string            `json:"source"`
	Labels      map[string]string `json:"labels"`
	Entries     []serviceLogEntry `json:"entries"`
}

type serviceLogEntry struct {
	Timestamp         string             `json:"timestamp"`
	TimestampUnix     int64              `json:"timestamp_unix"`
	TimestampUnixNano int64              `json:"timestamp_unix_nano"`
	Message           string             `json:"message"`
	Level             string             `json:"level"`
	Service           string             `json:"service"`
	Process           string             `json:"process"`
	PID               string             `json:"pid"`
	Source            string             `json:"source"`
	Fingerprint       string             `json:"fingerprint"`
	Count             uint64             `json:"count"`
	Labels            map[string]string  `json:"labels"`
	Metrics           map[string]float64 `json:"metrics"`
}

func (c *Controller) handleLogsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.logIndex == nil {
		http.Error(w, "log index disabled", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"stats":     c.logIndex.Stats(),
		"timestamp": time.Now().UTC(),
	})
}

func (c *Controller) handleLogsSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.logIndex == nil {
		http.Error(w, "log index disabled", http.StatusServiceUnavailable)
		return
	}

	queryParams := r.URL.Query()
	searchQuery := logindex.SearchQuery{
		Text:        firstNonEmpty(queryParams.Get("q"), queryParams.Get("text")),
		CollectorID: firstNonEmpty(queryParams.Get("collector_id"), queryParams.Get("collector")),
		Hostname:    queryParams.Get("hostname"),
		Service:     queryParams.Get("service"),
		Process:     queryParams.Get("process"),
		PID:         queryParams.Get("pid"),
		Level:       queryParams.Get("level"),
		Source:      queryParams.Get("source"),
		Sort:        queryParams.Get("sort"),
	}

	if since, ok := parseSearchTime(queryParams.Get("since")); ok {
		searchQuery.Since = since
	}
	if until, ok := parseSearchTime(queryParams.Get("until")); ok {
		searchQuery.Until = until
	}
	if window := strings.TrimSpace(queryParams.Get("window")); window != "" && searchQuery.Since.IsZero() {
		if duration, err := time.ParseDuration(window); err == nil && duration > 0 {
			until := searchQuery.Until
			if until.IsZero() {
				until = time.Now().UTC()
			}
			searchQuery.Since = until.Add(-duration)
		}
	}

	if limit, err := strconv.Atoi(strings.TrimSpace(queryParams.Get("limit"))); err == nil {
		searchQuery.Limit = limit
	}
	if offset, err := strconv.Atoi(strings.TrimSpace(queryParams.Get("offset"))); err == nil {
		searchQuery.Offset = offset
	}
	if minCount, err := strconv.ParseUint(strings.TrimSpace(queryParams.Get("min_count")), 10, 64); err == nil {
		searchQuery.MinCount = minCount
	}

	result := c.logIndex.Search(searchQuery)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (c *Controller) handleLogsIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.logIndex == nil {
		http.Error(w, "log index disabled", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxServiceLogBodyBytes)
	defer r.Body.Close()

	var request serviceLogsIngestRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request payload", http.StatusBadRequest)
		return
	}
	if len(request.Entries) == 0 {
		http.Error(w, "entries are required", http.StatusBadRequest)
		return
	}
	if len(request.Entries) > maxServiceLogIngestEntries {
		http.Error(w, "too many entries", http.StatusBadRequest)
		return
	}

	collectorID := strings.TrimSpace(request.CollectorID)
	if collectorID == "" {
		collectorID = strings.TrimSpace(r.URL.Query().Get("collector_id"))
	}
	hostname := strings.TrimSpace(request.Hostname)
	service := strings.TrimSpace(request.Service)
	source := strings.TrimSpace(request.Source)

	if collectorID == "" {
		switch {
		case service != "":
			collectorID = "service/" + sanitizeIdentity(service)
		case hostname != "":
			collectorID = "host/" + sanitizeIdentity(hostname)
		default:
			collectorID = "external"
		}
	}
	if source == "" {
		source = "service"
	}

	baseLabels := sanitizeLabels(request.Labels)
	baseLabels["pipeline"] = "service"

	now := time.Now().UTC()
	events := make([]logindex.RawEvent, 0, len(request.Entries))
	for _, item := range request.Entries {
		message := strings.TrimSpace(item.Message)
		if message == "" {
			continue
		}

		timestamp := now
		switch {
		case item.TimestampUnixNano > 0:
			timestamp = time.Unix(0, item.TimestampUnixNano).UTC()
		case item.TimestampUnix > 0:
			timestamp = parseUnixSeconds(item.TimestampUnix).UTC()
		case strings.TrimSpace(item.Timestamp) != "":
			if parsed, ok := parseSearchTime(item.Timestamp); ok {
				timestamp = parsed.UTC()
			}
		}

		labels := mergeLabels(baseLabels, sanitizeLabels(item.Labels))
		events = append(events, logindex.RawEvent{
			Timestamp:      timestamp,
			CollectorID:    collectorID,
			Hostname:       hostname,
			Service:        firstNonEmpty(item.Service, service),
			Process:        item.Process,
			PID:            item.PID,
			Level:          item.Level,
			Source:         firstNonEmpty(item.Source, source),
			Message:        message,
			Fingerprint:    strings.TrimSpace(item.Fingerprint),
			Count:          item.Count,
			Labels:         labels,
			MetricSnapshot: sanitizeMetrics(item.Metrics),
		})
	}

	accepted := c.logIndex.AddBatch(events)
	dropped := len(events) - accepted
	status := http.StatusAccepted
	if accepted == 0 {
		status = http.StatusBadRequest
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"collector_id": collectorID,
		"received":     len(request.Entries),
		"accepted":     accepted,
		"dropped":      dropped,
		"timestamp":    now,
	})
}

func parseSearchTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}

	if numeric, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return parseUnixSeconds(numeric), true
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func parseUnixSeconds(value int64) time.Time {
	switch {
	case value > 1e16:
		return time.Unix(0, value)
	case value > 1e12:
		return time.UnixMilli(value)
	default:
		return time.Unix(value, 0)
	}
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

func sanitizeLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	clean := make(map[string]string, len(labels))
	for key, value := range labels {
		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if len(key) > 64 {
			key = key[:64]
		}
		if len(value) > 128 {
			value = value[:128]
		}
		clean[key] = value
	}
	return clean
}

func mergeLabels(left, right map[string]string) map[string]string {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	merged := make(map[string]string, len(left)+len(right))
	for key, value := range left {
		merged[key] = value
	}
	for key, value := range right {
		merged[key] = value
	}
	return merged
}

func sanitizeMetrics(metrics map[string]float64) map[string]float64 {
	if len(metrics) == 0 {
		return nil
	}
	clean := make(map[string]float64, len(metrics))
	for key, value := range metrics {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if len(key) > 96 {
			key = key[:96]
		}
		clean[key] = value
	}
	if len(clean) == 0 {
		return nil
	}
	return clean
}
