package logindex

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/safeconv"
)

const (
	fieldCollector = "collector"
	fieldHost      = "host"
	fieldService   = "service"
	fieldProcess   = "process"
	fieldPID       = "pid"
	fieldLevel     = "level"
	fieldSource    = "source"
)

type segment struct {
	start      time.Time
	end        time.Time
	entries    []Entry
	tokenIndex map[string][]int
	fields     map[string]map[string][]int
	sizeBytes  int
}

type normalizedQuery struct {
	SearchQuery
	textLower    string
	textTokens   []string
	sortDesc     bool
	timelineStep time.Duration
}

// Index is a segmented, in-memory log index optimized for time-range search.
type Index struct {
	mu sync.RWMutex

	cfg Config
	now func() time.Time

	nextID   uint64
	segments []*segment

	entryCount int
	entryBytes int

	ingestedEvents uint64
	ingestedLines  uint64
	droppedEvents  uint64
	queriesTotal   uint64
	lastQueryAt    time.Time
}

// NewIndex creates a log index with normalized defaults.
func NewIndex(cfg Config) *Index {
	normalized := applyDefaults(cfg)
	return &Index{
		cfg: normalized,
		now: time.Now,
	}
}

// SetNowFunc overrides time source for deterministic tests.
func (i *Index) SetNowFunc(fn func() time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if fn == nil {
		i.now = time.Now
		return
	}
	i.now = fn
}

// AddBatch ingests and indexes a batch of raw events.
func (i *Index) AddBatch(events []RawEvent) int {
	if len(events) == 0 {
		return 0
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	now := i.now().UTC()
	accepted := 0
	for _, event := range events {
		entry, ok := Normalize(i.cfg, event, now)
		if !ok {
			i.droppedEvents++
			continue
		}
		entry.Timestamp = entry.Timestamp.UTC()

		if i.cfg.Retention > 0 {
			cutoff := now.Add(-i.cfg.Retention)
			if entry.Timestamp.Before(cutoff) {
				i.droppedEvents++
				continue
			}
		}

		seg := i.ensureSegmentLocked(entry.Timestamp)
		i.nextID++
		entry.ID = i.nextID

		position := len(seg.entries)
		seg.entries = append(seg.entries, entry)
		size := estimateEntrySize(entry)
		seg.sizeBytes += size
		i.entryCount++
		i.entryBytes += size
		i.ingestedEvents++
		i.ingestedLines += maxUint64(1, entry.Count)

		i.indexEntryLocked(seg, entry, position)
		accepted++
	}

	i.evictLocked(now)
	return accepted
}

// Search returns matching entries and aggregated analytics.
func (i *Index) Search(query SearchQuery) SearchResult {
	i.mu.RLock()
	nq := i.normalizeQueryLocked(query)

	matched := make([]Entry, 0, 1024)
	for _, seg := range i.segments {
		if seg == nil {
			continue
		}
		if seg.end.Before(nq.Since) || seg.start.After(nq.Until) {
			continue
		}
		candidates := candidatePositions(seg, nq)
		for _, pos := range candidates {
			if pos < 0 || pos >= len(seg.entries) {
				continue
			}
			entry := seg.entries[pos]
			if !matchesEntry(entry, nq) {
				continue
			}
			matched = append(matched, cloneEntry(entry))
		}
	}
	i.mu.RUnlock()

	sortEntries(matched, nq.sortDesc)
	total := len(matched)
	entries := paginateEntries(matched, nq.Offset, nq.Limit)

	result := SearchResult{
		GeneratedAt:      time.Now().UTC(),
		Query:            nq.SearchQuery,
		Total:            total,
		Returned:         len(entries),
		Entries:          entries,
		LevelCounts:      buildCountBuckets(groupByLevel(matched)),
		ServiceCounts:    buildCountBuckets(groupByService(matched)),
		CollectorCounts:  buildCountBuckets(groupByCollector(matched)),
		Timeline:         buildTimeline(matched, nq.timelineStep),
		Highlights:       buildHighlights(matched, i.cfg.MaxHighlights),
		MetricCorrelated: buildMetricCorrelations(matched, i.cfg.MaxCorrelations),
	}

	i.mu.Lock()
	i.queriesTotal++
	i.lastQueryAt = result.GeneratedAt
	i.mu.Unlock()

	return result
}

// Stats reports current index health and cumulative ingest/query counters.
func (i *Index) Stats() Stats {
	i.mu.RLock()
	defer i.mu.RUnlock()

	stats := Stats{
		Retention:         i.cfg.Retention.String(),
		SegmentDuration:   i.cfg.SegmentDuration.String(),
		Segments:          len(i.segments),
		Entries:           i.entryCount,
		IngestedEvents:    i.ingestedEvents,
		IngestedLines:     i.ingestedLines,
		DroppedEvents:     i.droppedEvents,
		QueriesTotal:      i.queriesTotal,
		LastQueryAt:       i.lastQueryAt,
		CurrentEntryBytes: i.entryBytes,
	}

	if len(i.segments) > 0 {
		first := i.segments[0]
		last := i.segments[len(i.segments)-1]
		if first != nil && len(first.entries) > 0 {
			stats.OldestEntryAt = first.entries[0].Timestamp
		}
		if last != nil && len(last.entries) > 0 {
			stats.LatestEntryAt = last.entries[len(last.entries)-1].Timestamp
		}
	}

	return stats
}

func applyDefaults(cfg Config) Config {
	defaults := DefaultConfig()
	if cfg.Retention <= 0 {
		cfg.Retention = defaults.Retention
	}
	if cfg.SegmentDuration <= 0 {
		cfg.SegmentDuration = defaults.SegmentDuration
	}
	if cfg.MaxSegments <= 0 {
		cfg.MaxSegments = defaults.MaxSegments
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = defaults.MaxEntries
	}
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = defaults.MaxMessageBytes
	}
	if cfg.MaxQueryLimit <= 0 {
		cfg.MaxQueryLimit = defaults.MaxQueryLimit
	}
	if cfg.MaxQueryOffset <= 0 {
		cfg.MaxQueryOffset = defaults.MaxQueryOffset
	}
	if cfg.MaxCorrelations <= 0 {
		cfg.MaxCorrelations = defaults.MaxCorrelations
	}
	if cfg.MaxHighlights <= 0 {
		cfg.MaxHighlights = defaults.MaxHighlights
	}
	if cfg.MaxTextTokens <= 0 {
		cfg.MaxTextTokens = defaults.MaxTextTokens
	}
	if cfg.MaxLabelsPerEntry <= 0 {
		cfg.MaxLabelsPerEntry = defaults.MaxLabelsPerEntry
	}
	if cfg.MaxMetricsPerEntry <= 0 {
		cfg.MaxMetricsPerEntry = defaults.MaxMetricsPerEntry
	}
	if cfg.MaxSearchWindow <= 0 {
		cfg.MaxSearchWindow = defaults.MaxSearchWindow
	}
	if cfg.DefaultSearchWindow <= 0 {
		cfg.DefaultSearchWindow = defaults.DefaultSearchWindow
	}
	if cfg.DefaultSearchLimit <= 0 {
		cfg.DefaultSearchLimit = defaults.DefaultSearchLimit
	}
	if cfg.DefaultTimelineWidth <= 0 {
		cfg.DefaultTimelineWidth = defaults.DefaultTimelineWidth
	}
	return cfg
}

func (i *Index) normalizeQueryLocked(query SearchQuery) normalizedQuery {
	now := i.now().UTC()
	nq := normalizedQuery{SearchQuery: query}

	nq.Text = strings.TrimSpace(query.Text)
	nq.textLower = strings.ToLower(nq.Text)
	nq.textTokens = tokenize(nq.textLower, i.cfg.MaxTextTokens)

	nq.CollectorID = normalizeFilter(query.CollectorID)
	nq.Hostname = normalizeFilter(query.Hostname)
	nq.Service = normalizeFilter(query.Service)
	nq.Process = normalizeFilter(query.Process)
	nq.PID = normalizeFilter(query.PID)
	nq.Level = normalizeLevel(query.Level)
	nq.Source = normalizeFilter(query.Source)

	nq.Limit = query.Limit
	if nq.Limit <= 0 {
		nq.Limit = i.cfg.DefaultSearchLimit
	}
	if nq.Limit > i.cfg.MaxQueryLimit {
		nq.Limit = i.cfg.MaxQueryLimit
	}

	nq.Offset = query.Offset
	if nq.Offset < 0 {
		nq.Offset = 0
	}
	if nq.Offset > i.cfg.MaxQueryOffset {
		nq.Offset = i.cfg.MaxQueryOffset
	}

	nq.Sort = strings.ToLower(strings.TrimSpace(query.Sort))
	nq.sortDesc = nq.Sort != "asc"
	if nq.Sort == "" {
		nq.Sort = "desc"
	}

	since := query.Since.UTC()
	until := query.Until.UTC()
	if until.IsZero() {
		until = now
	}
	if since.IsZero() {
		since = until.Add(-i.cfg.DefaultSearchWindow)
	}
	if since.After(until) {
		since, until = until, since
	}
	if window := until.Sub(since); window > i.cfg.MaxSearchWindow {
		since = until.Add(-i.cfg.MaxSearchWindow)
	}
	if i.cfg.Retention > 0 {
		retentionCutoff := now.Add(-i.cfg.Retention)
		if since.Before(retentionCutoff) {
			since = retentionCutoff
		}
	}
	nq.Since = since
	nq.Until = until

	nq.timelineStep = timelineStep(until.Sub(since), i.cfg.DefaultTimelineWidth)

	if nq.MinCount == 0 {
		nq.MinCount = query.MinCount
	}

	return nq
}

func (i *Index) ensureSegmentLocked(ts time.Time) *segment {
	if ts.IsZero() {
		ts = i.now().UTC()
	}
	start := ts.Truncate(i.cfg.SegmentDuration)
	end := start.Add(i.cfg.SegmentDuration)

	idx := sort.Search(len(i.segments), func(j int) bool {
		return !i.segments[j].start.Before(start)
	})
	if idx < len(i.segments) && i.segments[idx].start.Equal(start) {
		return i.segments[idx]
	}

	seg := &segment{
		start:      start,
		end:        end,
		entries:    make([]Entry, 0, 512),
		tokenIndex: make(map[string][]int),
		fields:     make(map[string]map[string][]int),
	}
	i.segments = append(i.segments, nil)
	copy(i.segments[idx+1:], i.segments[idx:])
	i.segments[idx] = seg
	return seg
}

func (i *Index) indexEntryLocked(seg *segment, entry Entry, position int) {
	addFieldIndex(seg, fieldCollector, entry.CollectorID, position)
	addFieldIndex(seg, fieldHost, entry.Hostname, position)
	addFieldIndex(seg, fieldService, entry.Service, position)
	addFieldIndex(seg, fieldProcess, entry.Process, position)
	addFieldIndex(seg, fieldPID, entry.PID, position)
	addFieldIndex(seg, fieldLevel, entry.Level, position)
	addFieldIndex(seg, fieldSource, entry.Source, position)

	seen := make(map[string]struct{}, 24)
	for _, token := range tokenize(buildSearchHaystack(entry), 48) {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		seg.tokenIndex[token] = append(seg.tokenIndex[token], position)
	}
}

func (i *Index) evictLocked(now time.Time) {
	for len(i.segments) > 0 {
		oldest := i.segments[0]
		if oldest == nil {
			i.segments = i.segments[1:]
			continue
		}

		evict := false
		if i.cfg.Retention > 0 {
			cutoff := now.Add(-i.cfg.Retention)
			if !oldest.end.After(cutoff) {
				evict = true
			}
		}
		if !evict && i.cfg.MaxSegments > 0 && len(i.segments) > i.cfg.MaxSegments {
			evict = true
		}
		if !evict && i.cfg.MaxEntries > 0 && i.entryCount > i.cfg.MaxEntries {
			evict = true
		}

		if !evict {
			break
		}

		i.entryCount -= len(oldest.entries)
		if i.entryCount < 0 {
			i.entryCount = 0
		}
		i.entryBytes -= oldest.sizeBytes
		if i.entryBytes < 0 {
			i.entryBytes = 0
		}
		i.segments = i.segments[1:]
	}
}

func addFieldIndex(seg *segment, field, value string, position int) {
	value = normalizeFilter(value)
	if value == "" {
		return
	}
	bucket, ok := seg.fields[field]
	if !ok {
		bucket = make(map[string][]int)
		seg.fields[field] = bucket
	}
	bucket[value] = append(bucket[value], position)
}

func normalizeFilter(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func candidatePositions(seg *segment, query normalizedQuery) []int {
	constraints := make([][]int, 0, 8+len(query.textTokens))

	addFieldConstraint := func(field, value string) {
		if value == "" {
			return
		}
		fieldMap, ok := seg.fields[field]
		if !ok {
			constraints = append(constraints, []int{})
			return
		}
		constraints = append(constraints, fieldMap[value])
	}

	addFieldConstraint(fieldCollector, query.CollectorID)
	addFieldConstraint(fieldHost, query.Hostname)
	addFieldConstraint(fieldService, query.Service)
	addFieldConstraint(fieldProcess, query.Process)
	addFieldConstraint(fieldPID, query.PID)
	addFieldConstraint(fieldLevel, query.Level)
	addFieldConstraint(fieldSource, query.Source)

	for _, token := range query.textTokens {
		constraints = append(constraints, seg.tokenIndex[token])
	}

	if len(constraints) == 0 {
		all := make([]int, len(seg.entries))
		for i := range seg.entries {
			all[i] = i
		}
		return all
	}

	sort.Slice(constraints, func(i, j int) bool {
		return len(constraints[i]) < len(constraints[j])
	})

	if len(constraints[0]) == 0 {
		return nil
	}

	candidate := append([]int(nil), constraints[0]...)
	for _, constraint := range constraints[1:] {
		if len(constraint) == 0 {
			return nil
		}
		candidate = intersectSortedPositions(candidate, constraint)
		if len(candidate) == 0 {
			return nil
		}
	}

	return candidate
}

func intersectSortedPositions(left, right []int) []int {
	out := make([]int, 0, minInt(len(left), len(right)))
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		switch {
		case left[i] == right[j]:
			out = append(out, left[i])
			i++
			j++
		case left[i] < right[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func matchesEntry(entry Entry, query normalizedQuery) bool {
	if entry.Timestamp.Before(query.Since) || entry.Timestamp.After(query.Until) {
		return false
	}
	if query.MinCount > 0 && entry.Count < query.MinCount {
		return false
	}
	if query.CollectorID != "" && normalizeFilter(entry.CollectorID) != query.CollectorID {
		return false
	}
	if query.Hostname != "" && normalizeFilter(entry.Hostname) != query.Hostname {
		return false
	}
	if query.Service != "" && normalizeFilter(entry.Service) != query.Service {
		return false
	}
	if query.Process != "" && normalizeFilter(entry.Process) != query.Process {
		return false
	}
	if query.PID != "" && normalizeFilter(entry.PID) != query.PID {
		return false
	}
	if query.Level != "" && normalizeLevel(entry.Level) != query.Level {
		return false
	}
	if query.Source != "" && normalizeFilter(entry.Source) != query.Source {
		return false
	}
	if query.textLower != "" {
		haystack := strings.ToLower(buildSearchHaystack(entry))
		if !strings.Contains(haystack, query.textLower) {
			for _, token := range query.textTokens {
				if !strings.Contains(haystack, token) {
					return false
				}
			}
		}
	}
	return true
}

func tokenize(text string, limit int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 32
	}

	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		case r == '_', r == '.', r == '-', r == '/':
			return r
		default:
			return ' '
		}
	}, text)

	parts := strings.Fields(mapped)
	if len(parts) == 0 {
		return nil
	}

	out := make([]string, 0, minInt(len(parts), limit))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		out = append(out, part)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func buildSearchHaystack(entry Entry) string {
	parts := []string{entry.Message, entry.Service, entry.Process, entry.Source, entry.PID, entry.Hostname, entry.CollectorID, entry.Level}
	for k, v := range entry.Labels {
		parts = append(parts, k, v)
	}
	return strings.Join(parts, " ")
}

func sortEntries(entries []Entry, desc bool) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Timestamp.Equal(entries[j].Timestamp) {
			if desc {
				return entries[i].ID > entries[j].ID
			}
			return entries[i].ID < entries[j].ID
		}
		if desc {
			return entries[i].Timestamp.After(entries[j].Timestamp)
		}
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
}

func paginateEntries(entries []Entry, offset, limit int) []Entry {
	if offset >= len(entries) {
		return []Entry{}
	}
	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	if end < offset {
		end = offset
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		return []Entry{}
	}
	out := make([]Entry, 0, end-offset)
	for _, entry := range entries[offset:end] {
		out = append(out, cloneEntry(entry))
	}
	return out
}

func groupByLevel(entries []Entry) map[string]uint64 {
	out := make(map[string]uint64)
	for _, entry := range entries {
		key := normalizeLevel(entry.Level)
		if key == "" {
			key = LevelUnknown
		}
		out[key] += maxUint64(1, entry.Count)
	}
	return out
}

func groupByService(entries []Entry) map[string]uint64 {
	out := make(map[string]uint64)
	for _, entry := range entries {
		key := strings.TrimSpace(entry.Service)
		if key == "" {
			key = "unknown"
		}
		out[key] += maxUint64(1, entry.Count)
	}
	return out
}

func groupByCollector(entries []Entry) map[string]uint64 {
	out := make(map[string]uint64)
	for _, entry := range entries {
		key := strings.TrimSpace(entry.CollectorID)
		if key == "" {
			key = "unknown"
		}
		out[key] += maxUint64(1, entry.Count)
	}
	return out
}

func buildCountBuckets(counts map[string]uint64) []CountBucket {
	if len(counts) == 0 {
		return []CountBucket{}
	}
	buckets := make([]CountBucket, 0, len(counts))
	for value, count := range counts {
		buckets = append(buckets, CountBucket{Value: value, Count: count})
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Count == buckets[j].Count {
			return buckets[i].Value < buckets[j].Value
		}
		return buckets[i].Count > buckets[j].Count
	})
	return buckets
}

func buildTimeline(entries []Entry, width time.Duration) []TimelineBucket {
	if len(entries) == 0 {
		return []TimelineBucket{}
	}
	if width <= 0 {
		width = time.Minute
	}

	tmp := make(map[time.Time]*TimelineBucket)
	for _, entry := range entries {
		start := entry.Timestamp.Truncate(width)
		bucket := tmp[start]
		if bucket == nil {
			bucket = &TimelineBucket{Start: start, End: start.Add(width)}
			tmp[start] = bucket
		}
		weight := maxUint64(1, entry.Count)
		bucket.Total += weight
		switch normalizeLevel(entry.Level) {
		case LevelError, LevelFatal:
			bucket.Errors += weight
		case LevelWarn:
			bucket.Warnings += weight
		}
	}

	keys := make([]time.Time, 0, len(tmp))
	for key := range tmp {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })

	out := make([]TimelineBucket, 0, len(keys))
	for _, key := range keys {
		out = append(out, *tmp[key])
	}
	return out
}

func buildHighlights(entries []Entry, maxHighlights int) []Entry {
	if len(entries) == 0 || maxHighlights <= 0 {
		return []Entry{}
	}

	highlights := make([]Entry, 0, minInt(len(entries), maxHighlights*2))
	for _, entry := range entries {
		if !isHighlight(entry) {
			continue
		}
		highlights = append(highlights, cloneEntry(entry))
	}
	if len(highlights) == 0 {
		return []Entry{}
	}

	sort.Slice(highlights, func(i, j int) bool {
		left := highlightScore(highlights[i])
		right := highlightScore(highlights[j])
		if left == right {
			if highlights[i].Timestamp.Equal(highlights[j].Timestamp) {
				return highlights[i].ID > highlights[j].ID
			}
			return highlights[i].Timestamp.After(highlights[j].Timestamp)
		}
		return left > right
	})

	if len(highlights) > maxHighlights {
		highlights = highlights[:maxHighlights]
	}
	return highlights
}

func isHighlight(entry Entry) bool {
	level := normalizeLevel(entry.Level)
	if level == LevelError || level == LevelFatal {
		return true
	}
	if entry.Count >= 5 {
		return true
	}
	lower := strings.ToLower(entry.Message)
	return strings.Contains(lower, "panic") || strings.Contains(lower, "oom") || strings.Contains(lower, "timeout")
}

func highlightScore(entry Entry) float64 {
	severity := 0.0
	switch normalizeLevel(entry.Level) {
	case LevelFatal:
		severity = 5
	case LevelError:
		severity = 4
	case LevelWarn:
		severity = 2
	default:
		severity = 1
	}
	countBoost := math.Log1p(float64(maxUint64(1, entry.Count)))
	return severity*10 + countBoost
}

func buildMetricCorrelations(entries []Entry, maxCorrelations int) []MetricCorrelation {
	if len(entries) == 0 || maxCorrelations <= 0 {
		return []MetricCorrelation{}
	}

	type aggregate struct {
		samples      int
		errorSamples int
		sum          float64
		errorSum     float64
	}

	metrics := make(map[string]*aggregate)
	for _, entry := range entries {
		if len(entry.Metrics) == 0 {
			continue
		}
		weight := safeconv.Uint64ToInt(maxUint64(1, entry.Count))
		isError := normalizeLevel(entry.Level) == LevelError || normalizeLevel(entry.Level) == LevelFatal
		for key, value := range entry.Metrics {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			agg := metrics[key]
			if agg == nil {
				agg = &aggregate{}
				metrics[key] = agg
			}
			agg.samples = safeconv.AddInts(agg.samples, weight)
			agg.sum += value * float64(weight)
			if isError {
				agg.errorSamples = safeconv.AddInts(agg.errorSamples, weight)
				agg.errorSum += value * float64(weight)
			}
		}
	}

	out := make([]MetricCorrelation, 0, len(metrics))
	for metric, agg := range metrics {
		if agg == nil || agg.samples < 4 || agg.errorSamples < 2 {
			continue
		}
		baseline := agg.sum / float64(agg.samples)
		errorAvg := agg.errorSum / float64(agg.errorSamples)
		uplift := 0.0
		if math.Abs(baseline) > 1e-9 {
			uplift = (errorAvg - baseline) / math.Abs(baseline) * 100
		} else if math.Abs(errorAvg) > 1e-9 {
			uplift = math.Copysign(100, errorAvg)
		}
		absScore := math.Abs(uplift) * math.Log1p(float64(agg.errorSamples))
		out = append(out, MetricCorrelation{
			Metric:         metric,
			Samples:        agg.samples,
			ErrorSamples:   agg.errorSamples,
			BaselineAvg:    baseline,
			ErrorAvg:       errorAvg,
			UpliftPercent:  uplift,
			AbsUpliftScore: absScore,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].AbsUpliftScore == out[j].AbsUpliftScore {
			if out[i].ErrorSamples == out[j].ErrorSamples {
				return out[i].Metric < out[j].Metric
			}
			return out[i].ErrorSamples > out[j].ErrorSamples
		}
		return out[i].AbsUpliftScore > out[j].AbsUpliftScore
	})
	if len(out) > maxCorrelations {
		out = out[:maxCorrelations]
	}
	return out
}

func timelineStep(window, defaultStep time.Duration) time.Duration {
	if defaultStep <= 0 {
		defaultStep = time.Minute
	}
	switch {
	case window <= time.Hour:
		return time.Minute
	case window <= 3*time.Hour:
		return 2 * time.Minute
	case window <= 6*time.Hour:
		return 5 * time.Minute
	case window <= 12*time.Hour:
		return 10 * time.Minute
	default:
		return 15 * time.Minute
	}
}

func estimateEntrySize(entry Entry) int {
	size := 64 + len(entry.Message) + len(entry.CollectorID) + len(entry.Hostname) + len(entry.Service) + len(entry.Process) + len(entry.Source) + len(entry.PID) + len(entry.Level) + len(entry.Fingerprint)
	for key, value := range entry.Labels {
		size += len(key) + len(value) + 8
	}
	size += len(entry.Metrics) * 24
	return size
}

func cloneEntry(entry Entry) Entry {
	copy := entry
	if len(entry.Labels) > 0 {
		copy.Labels = make(map[string]string, len(entry.Labels))
		for key, value := range entry.Labels {
			copy.Labels[key] = value
		}
	}
	if len(entry.Metrics) > 0 {
		copy.Metrics = make(map[string]float64, len(entry.Metrics))
		for key, value := range entry.Metrics {
			copy.Metrics[key] = value
		}
	}
	return copy
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
