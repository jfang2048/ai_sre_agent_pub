package logindex

import "time"

const (
	LevelDebug   = "debug"
	LevelInfo    = "info"
	LevelWarn    = "warn"
	LevelError   = "error"
	LevelFatal   = "fatal"
	LevelUnknown = "unknown"
)

const (
	defaultSearchWindow = 15 * time.Minute
)

// Config controls retention and query limits for the log index.
type Config struct {
	Retention            time.Duration
	SegmentDuration      time.Duration
	MaxSegments          int
	MaxEntries           int
	MaxMessageBytes      int
	MaxQueryLimit        int
	MaxQueryOffset       int
	MaxCorrelations      int
	MaxHighlights        int
	MaxTextTokens        int
	MaxLabelsPerEntry    int
	MaxMetricsPerEntry   int
	MaxSearchWindow      time.Duration
	DefaultSearchWindow  time.Duration
	DefaultSearchLimit   int
	DefaultTimelineWidth time.Duration
}

// DefaultConfig returns production-safe defaults for the native log subsystem.
func DefaultConfig() Config {
	return Config{
		Retention:            6 * time.Hour,
		SegmentDuration:      time.Minute,
		MaxSegments:          720,
		MaxEntries:           200000,
		MaxMessageBytes:      4096,
		MaxQueryLimit:        500,
		MaxQueryOffset:       20000,
		MaxCorrelations:      8,
		MaxHighlights:        20,
		MaxTextTokens:        10,
		MaxLabelsPerEntry:    24,
		MaxMetricsPerEntry:   12,
		MaxSearchWindow:      24 * time.Hour,
		DefaultSearchWindow:  defaultSearchWindow,
		DefaultSearchLimit:   150,
		DefaultTimelineWidth: time.Minute,
	}
}

// RawEvent is an unparsed event entering the native log pipeline.
type RawEvent struct {
	Timestamp      time.Time
	CollectorID    string
	Hostname       string
	Service        string
	Process        string
	PID            string
	Level          string
	Source         string
	Message        string
	Fingerprint    string
	Count          uint64
	Labels         map[string]string
	MetricSnapshot map[string]float64
}

// Entry is a normalized and indexed log record.
type Entry struct {
	ID          uint64             `json:"id"`
	Timestamp   time.Time          `json:"timestamp"`
	CollectorID string             `json:"collector_id,omitempty"`
	Hostname    string             `json:"hostname,omitempty"`
	Service     string             `json:"service,omitempty"`
	Process     string             `json:"process,omitempty"`
	PID         string             `json:"pid,omitempty"`
	Level       string             `json:"level,omitempty"`
	Source      string             `json:"source,omitempty"`
	Message     string             `json:"message"`
	Fingerprint string             `json:"fingerprint,omitempty"`
	Count       uint64             `json:"count"`
	Labels      map[string]string  `json:"labels,omitempty"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
}

// SearchQuery filters indexed logs.
type SearchQuery struct {
	Text        string    `json:"text,omitempty"`
	CollectorID string    `json:"collector_id,omitempty"`
	Hostname    string    `json:"hostname,omitempty"`
	Service     string    `json:"service,omitempty"`
	Process     string    `json:"process,omitempty"`
	PID         string    `json:"pid,omitempty"`
	Level       string    `json:"level,omitempty"`
	Source      string    `json:"source,omitempty"`
	Since       time.Time `json:"since,omitempty"`
	Until       time.Time `json:"until,omitempty"`
	Limit       int       `json:"limit,omitempty"`
	Offset      int       `json:"offset,omitempty"`
	Sort        string    `json:"sort,omitempty"`
	MinCount    uint64    `json:"min_count,omitempty"`
}

// CountBucket captures grouped count aggregations.
type CountBucket struct {
	Value string `json:"value"`
	Count uint64 `json:"count"`
}

// TimelineBucket captures time-aligned volume and severity counts.
type TimelineBucket struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Total    uint64    `json:"total"`
	Errors   uint64    `json:"errors"`
	Warnings uint64    `json:"warnings"`
}

// MetricCorrelation reports how errors move with selected metrics.
type MetricCorrelation struct {
	Metric         string  `json:"metric"`
	Samples        int     `json:"samples"`
	ErrorSamples   int     `json:"error_samples"`
	BaselineAvg    float64 `json:"baseline_avg"`
	ErrorAvg       float64 `json:"error_avg"`
	UpliftPercent  float64 `json:"uplift_percent"`
	AbsUpliftScore float64 `json:"abs_uplift_score"`
}

// SearchResult returns matches plus aggregates for exploration.
type SearchResult struct {
	GeneratedAt      time.Time           `json:"generated_at"`
	Query            SearchQuery         `json:"query"`
	Total            int                 `json:"total"`
	Returned         int                 `json:"returned"`
	Entries          []Entry             `json:"entries"`
	LevelCounts      []CountBucket       `json:"level_counts"`
	ServiceCounts    []CountBucket       `json:"service_counts"`
	CollectorCounts  []CountBucket       `json:"collector_counts"`
	Timeline         []TimelineBucket    `json:"timeline"`
	Highlights       []Entry             `json:"highlights"`
	MetricCorrelated []MetricCorrelation `json:"metric_correlated"`
}

// Stats summarizes index health and resource usage.
type Stats struct {
	Retention         string    `json:"retention"`
	SegmentDuration   string    `json:"segment_duration"`
	Segments          int       `json:"segments"`
	Entries           int       `json:"entries"`
	OldestEntryAt     time.Time `json:"oldest_entry_at,omitempty"`
	LatestEntryAt     time.Time `json:"latest_entry_at,omitempty"`
	IngestedEvents    uint64    `json:"ingested_events"`
	IngestedLines     uint64    `json:"ingested_lines"`
	DroppedEvents     uint64    `json:"dropped_events"`
	QueriesTotal      uint64    `json:"queries_total"`
	LastQueryAt       time.Time `json:"last_query_at,omitempty"`
	CurrentEntryBytes int       `json:"current_entry_bytes"`
}
