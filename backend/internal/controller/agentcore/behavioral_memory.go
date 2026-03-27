package agent

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

const behaviorHistoryLimit = 4096

type BehavioralMemoryRequest struct {
	CollectorID string
	Node        *ingest.NodeSnapshot
	Fleet       []*ingest.NodeSnapshot
	Series      []RiskSeries
	Logs        logsToolData
	Security    securityToolData
	EBPF        ebpfToolData
	ChangeLinks []RCAChangeLink
	Now         time.Time
}

type BehavioralMemoryStore struct {
	cfg     BehavioralMemoryConfig
	logger  *zap.Logger
	history ingest.MetricHistoryProvider

	mu    sync.Mutex
	cache map[string]behaviorHistoryCacheEntry
}

type behaviorHistoryCacheEntry struct {
	fetchedAt time.Time
	samples   []ingest.MetricHistorySample
}

type behaviorIdentity struct {
	key           string
	entity        string
	service       string
	workloadClass string
	workloadRole  string
	collectorID   string
}

type behaviorSignalObservation struct {
	SignalID          string
	Current           float64
	ShortBaseline     float64
	Timestamp         time.Time
	PersistencePoints int
	Medium            float64
	High              float64
	Points            []RiskSeriesPoint
}

type behaviorSignalContext struct {
	AnomalySupport []string
	ExpectedHints  []string
	PeerSignals    []string
}

type behaviorSignalStats struct {
	SampleCount      int
	LongBaseline     float64
	TemporalBaseline float64
	DeviationLongPct float64
	RecurrenceCount  int
	TemporalMatches  int
	TemporalBucket   string
	HighWater        float64
	ZScore           float64
	Threshold        float64
}

func NewBehavioralMemoryStore(cfg BehavioralMemoryConfig, logger *zap.Logger) *BehavioralMemoryStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &BehavioralMemoryStore{
		cfg:    normalizeBehavioralMemoryConfig(cfg),
		logger: logger.With(zap.String("component", "behavior_baseline")),
		cache:  make(map[string]behaviorHistoryCacheEntry, 32),
	}
}

func normalizeBehavioralMemoryConfig(cfg BehavioralMemoryConfig) BehavioralMemoryConfig {
	def := DefaultBehavioralMemoryConfig()
	if cfg.LongWindow <= 0 {
		cfg.LongWindow = def.LongWindow
	}
	if cfg.MinSamples <= 0 {
		cfg.MinSamples = def.MinSamples
	}
	if cfg.MinRecurringBursts <= 0 {
		cfg.MinRecurringBursts = def.MinRecurringBursts
	}
	if cfg.CacheEntries <= 0 {
		cfg.CacheEntries = def.CacheEntries
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = def.CacheTTL
	}
	return cfg
}

func (s *BehavioralMemoryStore) SetHistoryProvider(provider ingest.MetricHistoryProvider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = provider
	if len(s.cache) > 0 {
		s.cache = make(map[string]behaviorHistoryCacheEntry, minInt(len(s.cache), 32))
	}
}

func (s *BehavioralMemoryStore) Evaluate(req BehavioralMemoryRequest) []BehavioralSignalAssessment {
	if s == nil || !s.cfg.Enabled || req.Node == nil {
		return nil
	}
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	identity := deriveBehaviorIdentity(req.CollectorID, req.Node)
	if identity.key == "" {
		return nil
	}

	observations := collectBehaviorObservations(req, now)
	if len(observations) == 0 {
		return nil
	}

	currentWindowStart := earliestObservationTimestamp(observations)
	historical := s.historySamples(identity.collectorID, now)
	out := make([]BehavioralSignalAssessment, 0, len(observations))
	for _, obs := range observations {
		ctx := buildBehaviorSignalContext(obs.SignalID, observations, req, identity)
		stats := buildBehaviorStats(obs, historical, currentWindowStart)
		out = append(out, assessBehaviorSignal(s.cfg, identity, obs, stats, ctx))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].SuppressionFactor == out[j].SuppressionFactor {
			return out[i].SignalID < out[j].SignalID
		}
		return out[i].SuppressionFactor > out[j].SuppressionFactor
	})
	return out
}

func collectBehaviorObservations(req BehavioralMemoryRequest, now time.Time) []behaviorSignalObservation {
	profiles := riskSignalProfiles()
	out := make([]behaviorSignalObservation, 0, len(req.Series)+1)
	for _, item := range req.Series {
		profile, ok := profiles[item.Key]
		if !ok || len(item.Points) == 0 {
			continue
		}
		out = append(out, behaviorSignalObservation{
			SignalID:          item.Key,
			Current:           item.Latest,
			ShortBaseline:     item.Baseline,
			Timestamp:         item.Points[len(item.Points)-1].Timestamp,
			PersistencePoints: item.PersistencePoints,
			Medium:            profile.medium,
			High:              profile.high,
			Points:            append([]RiskSeriesPoint(nil), item.Points...),
		})
	}
	if req.EBPF.BehaviorScore > 0 || req.EBPF.EventRate > 0 {
		out = append(out, behaviorSignalObservation{
			SignalID:          "ebpf_behavior_anomaly",
			Current:           req.EBPF.BehaviorScore,
			ShortBaseline:     0.1,
			Timestamp:         now,
			PersistencePoints: maxInt(int(math.Round(req.EBPF.EventRate)), 1),
			Medium:            0.25,
			High:              0.60,
		})
	}
	return out
}

func earliestObservationTimestamp(items []behaviorSignalObservation) time.Time {
	var earliest time.Time
	for _, item := range items {
		if len(item.Points) == 0 {
			continue
		}
		ts := item.Points[0].Timestamp.UTC()
		if earliest.IsZero() || ts.Before(earliest) {
			earliest = ts
		}
	}
	return earliest
}

func (s *BehavioralMemoryStore) historySamples(collectorID string, now time.Time) []ingest.MetricHistorySample {
	if s == nil || s.history == nil || strings.TrimSpace(collectorID) == "" {
		return nil
	}
	collectorID = strings.TrimSpace(collectorID)

	s.mu.Lock()
	if entry, ok := s.cache[collectorID]; ok && now.Sub(entry.fetchedAt) <= s.cfg.CacheTTL {
		s.mu.Unlock()
		return entry.samples
	}
	s.mu.Unlock()

	since := now.Add(-s.cfg.LongWindow)
	samples := s.history.MetricHistory(collectorID, since, behaviorHistoryLimit)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[collectorID] = behaviorHistoryCacheEntry{
		fetchedAt: now,
		samples:   samples,
	}
	s.pruneCacheLocked(now)
	return samples
}

func (s *BehavioralMemoryStore) pruneCacheLocked(now time.Time) {
	if s.cfg.CacheTTL > 0 {
		cutoff := now.Add(-s.cfg.CacheTTL)
		for key, entry := range s.cache {
			if entry.fetchedAt.Before(cutoff) {
				delete(s.cache, key)
			}
		}
	}
	if len(s.cache) <= s.cfg.CacheEntries {
		return
	}
	type row struct {
		key string
		at  time.Time
	}
	rows := make([]row, 0, len(s.cache))
	for key, entry := range s.cache {
		rows = append(rows, row{key: key, at: entry.fetchedAt})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	for len(rows) > s.cfg.CacheEntries {
		delete(s.cache, rows[0].key)
		rows = rows[1:]
	}
}

func deriveBehaviorIdentity(collectorID string, node *ingest.NodeSnapshot) behaviorIdentity {
	if node == nil {
		return behaviorIdentity{}
	}
	collectorID = strings.TrimSpace(firstNonEmpty(collectorID, node.CollectorID))
	top := topProcessResources(node, 1)
	var process *ingest.ProcessResourceSample
	if len(top) > 0 {
		process = top[0]
	}
	service := firstNonEmpty(
		valueOrEmpty(node.Labels, "service"),
		valueOrEmpty(node.Labels, "job"),
		stringIfProcess(process, func(p *ingest.ProcessResourceSample) string { return p.Job }),
		stringIfProcess(process, func(p *ingest.ProcessResourceSample) string { return p.Name }),
		node.Hostname,
		collectorID,
	)
	workloadClass := firstNonEmpty(
		stringIfProcess(process, func(p *ingest.ProcessResourceSample) string { return p.WorkloadClass }),
		valueOrEmpty(node.Labels, "workload_class"),
		valueOrEmpty(node.Labels, "role"),
	)
	workloadRole := inferWorkloadRole(service, workloadClass)
	entity := firstNonEmpty(service, node.Hostname, collectorID)
	parts := []string{"collector=" + strings.ToLower(strings.TrimSpace(collectorID))}
	if service != "" {
		parts = append(parts, "service="+strings.ToLower(strings.TrimSpace(service)))
	}
	if workloadRole != "" {
		parts = append(parts, "role="+strings.ToLower(strings.TrimSpace(workloadRole)))
	}
	return behaviorIdentity{
		key:           strings.Join(parts, "|"),
		entity:        entity,
		service:       service,
		workloadClass: workloadClass,
		workloadRole:  workloadRole,
		collectorID:   collectorID,
	}
}

func valueOrEmpty(values map[string]string, key string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[key])
}

func stringIfProcess(item *ingest.ProcessResourceSample, fn func(*ingest.ProcessResourceSample) string) string {
	if item == nil || fn == nil {
		return ""
	}
	return strings.TrimSpace(fn(item))
}

func inferWorkloadRole(service, workloadClass string) string {
	combined := strings.ToLower(strings.TrimSpace(strings.Join([]string{service, workloadClass}, " ")))
	switch {
	case strings.Contains(combined, "build"), strings.Contains(combined, "compile"), strings.Contains(combined, "bazel"), strings.Contains(combined, "maven"), strings.Contains(combined, "gradle"):
		return "build_compile"
	case strings.Contains(combined, "backup"), strings.Contains(combined, "artifact"), strings.Contains(combined, "upload"), strings.Contains(combined, "batch"), strings.Contains(combined, "snapshot"):
		return "batch_io"
	case strings.Contains(combined, "deploy"), strings.Contains(combined, "rollout"), strings.Contains(combined, "release"), strings.Contains(combined, "startup"), strings.Contains(combined, "init"):
		return "deployment"
	case strings.Contains(combined, "train"), strings.Contains(combined, "model"), strings.Contains(combined, "inference"), strings.Contains(combined, "gpu"):
		return "model_runtime"
	default:
		return "general_service"
	}
}

func buildBehaviorSignalContext(signalID string, observations []behaviorSignalObservation, req BehavioralMemoryRequest, identity behaviorIdentity) behaviorSignalContext {
	ctx := behaviorSignalContext{
		AnomalySupport: make([]string, 0, 6),
		ExpectedHints:  make([]string, 0, 4),
	}
	otherActive := 0
	for _, obs := range observations {
		if obs.SignalID == signalID {
			continue
		}
		if obs.Current >= obs.Medium || absFloat(percentChange(obs.ShortBaseline, obs.Current)) >= 25 {
			otherActive++
		}
	}
	if otherActive >= 2 {
		ctx.AnomalySupport = append(ctx.AnomalySupport, "multi_signal_agreement")
	}
	incidentLogBurst := logsSuggestIncidentBurst(req.Logs)
	if !incidentLogBurst && !hasDeploymentContext(req.Logs, req.ChangeLinks) {
		for _, obs := range observations {
			if obs.SignalID == "log_burst" && obs.Current >= obs.Medium {
				incidentLogBurst = true
				break
			}
		}
	}
	if !incidentLogBurst {
		incidentLogBurst = changeLinksSuggestLogBurst(req.ChangeLinks)
	}
	if signalID != "log_burst" && incidentLogBurst {
		ctx.AnomalySupport = append(ctx.AnomalySupport, "error_log_burst")
	}
	serviceLatencyActive := false
	for _, obs := range observations {
		if obs.SignalID == "service_latency" && (obs.Current >= obs.Medium || absFloat(percentChange(obs.ShortBaseline, obs.Current)) >= 25) {
			serviceLatencyActive = true
			break
		}
	}
	if signalID != "service_latency" && serviceLatencyActive {
		ctx.AnomalySupport = append(ctx.AnomalySupport, "service_latency_regression")
	}
	for _, support := range criticalLogSupports(req.Logs) {
		ctx.AnomalySupport = append(ctx.AnomalySupport, support)
	}
	if signalID == "gpu_memory_pressure" && gpuMemoryPinned(observations) {
		ctx.AnomalySupport = append(ctx.AnomalySupport, "gpu_memory_pinned")
	}
	if signalID != "ebpf_behavior_anomaly" && req.EBPF.BehaviorScore >= 0.25 {
		ctx.AnomalySupport = append(ctx.AnomalySupport, "runtime_behavior_agreement")
	}
	if req.Security.Score >= 0.25 {
		ctx.AnomalySupport = append(ctx.AnomalySupport, "security_findings")
	}
	if hasDeploymentContext(req.Logs, req.ChangeLinks) {
		ctx.ExpectedHints = append(ctx.ExpectedHints, "recent_deploy_context")
	}
	if signalExpectedByRole(identity.workloadRole, signalID) {
		ctx.ExpectedHints = append(ctx.ExpectedHints, "role_affinity")
	}
	if hasHealthyLoadShape(signalID, observations) {
		ctx.ExpectedHints = append(ctx.ExpectedHints, "healthy_load_shape")
	}
	ctx.PeerSignals = append(ctx.PeerSignals, peerSignalsForSignal(signalID, observations, req, identity)...)
	ctx.AnomalySupport = dedupeStrings(ctx.AnomalySupport)
	ctx.ExpectedHints = dedupeStrings(ctx.ExpectedHints)
	ctx.PeerSignals = dedupeStrings(ctx.PeerSignals)
	return ctx
}

func peerSignalsForSignal(signalID string, observations []behaviorSignalObservation, req BehavioralMemoryRequest, identity behaviorIdentity) []string {
	if len(req.Fleet) < 2 || strings.TrimSpace(identity.service) == "" {
		return nil
	}
	if signalID == "memory_leak_rate" || signalID == "ebpf_behavior_anomaly" || signalID == "log_burst" {
		return nil
	}
	spec, ok := riskSeriesSpecByKey(signalID)
	if !ok {
		return nil
	}
	var current *behaviorSignalObservation
	for idx := range observations {
		if observations[idx].SignalID == signalID {
			current = &observations[idx]
			break
		}
	}
	if current == nil {
		return nil
	}
	peers := make([]float64, 0, len(req.Fleet))
	elevatedPeers := 0
	for _, node := range req.Fleet {
		if node == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(node.CollectorID), strings.TrimSpace(identity.collectorID)) {
			continue
		}
		peerIdentity := deriveBehaviorIdentity(node.CollectorID, node)
		if !strings.EqualFold(strings.TrimSpace(peerIdentity.service), strings.TrimSpace(identity.service)) {
			continue
		}
		value, ok := spec.extract(node.Metrics)
		if !ok {
			continue
		}
		peers = append(peers, value)
		if value >= maxFloat(current.Medium*0.85, current.ShortBaseline*0.85) {
			elevatedPeers++
		}
	}
	if len(peers) < 2 {
		return nil
	}
	sort.Float64s(peers)
	median := peers[len(peers)/2]
	if len(peers)%2 == 0 {
		median = (peers[len(peers)/2-1] + peers[len(peers)/2]) / 2
	}
	deviationPeers := absFloat(percentChange(median, current.Current))
	out := make([]string, 0, 1)
	if median > 0 && current.Current >= maxFloat(current.Medium, median*1.35) && deviationPeers >= 25 {
		out = append(out, "peer_outlier")
	} else if current.Current >= current.Medium*0.85 && deviationPeers <= 18 && elevatedPeers >= maxInt(1, len(peers)/2) {
		out = append(out, "peer_group_burst")
	}
	return out
}

func hasDeploymentContext(logs logsToolData, changes []RCAChangeLink) bool {
	if len(logs.RecentDeploys) > 0 {
		return true
	}
	for _, change := range changes {
		switch strings.ToLower(strings.TrimSpace(change.Category)) {
		case "deployment", "feature_flag", "config", "driver":
			return true
		}
	}
	return false
}

func changeLinksSuggestLogBurst(changes []RCAChangeLink) bool {
	for _, change := range changes {
		text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
			change.Category,
			change.Summary,
			change.ImpactSummary,
			change.HypothesisHint,
		}, " ")))
		if text == "" {
			continue
		}
		if change.CorrelationScore < 0.2 && change.TemporalAdjacency < 0.2 {
			continue
		}
		if strings.Contains(text, "error") || strings.Contains(text, "fail") || strings.Contains(text, "retry") || strings.Contains(text, "timeout") {
			return true
		}
	}
	return false
}

func signalExpectedByRole(role, signalID string) bool {
	switch role {
	case "build_compile":
		return signalID == "cpu_pressure" || signalID == "memory_pressure"
	case "batch_io":
		return signalID == "io_pressure" || signalID == "io_latency" || signalID == "retransmit_ratio" || signalID == "softnet_drop" || signalID == "network_throughput"
	case "deployment":
		return signalID == "log_burst" || signalID == "ebpf_behavior_anomaly" || signalID == "cpu_pressure" || signalID == "memory_pressure" || signalID == "service_latency"
	case "model_runtime":
		return signalID == "gpu_utilization" || signalID == "gpu_memory_pressure" || signalID == "memory_pressure"
	default:
		return false
	}
}

func hasHealthyLoadShape(signalID string, observations []behaviorSignalObservation) bool {
	if signalID != "cpu_pressure" && signalID != "network_throughput" {
		return false
	}
	cpuActive := false
	networkActive := false
	latencyRegressing := false
	for _, obs := range observations {
		active := obs.Current >= obs.Medium || absFloat(percentChange(obs.ShortBaseline, obs.Current)) >= 25
		switch obs.SignalID {
		case "cpu_pressure":
			cpuActive = active
		case "network_throughput":
			networkActive = active
		case "service_latency":
			latencyRegressing = active
		}
	}
	return cpuActive && networkActive && !latencyRegressing
}

func signalLikelyDeployWarmup(signalID string) bool {
	switch signalID {
	case "cpu_pressure", "memory_pressure", "log_burst", "ebpf_behavior_anomaly", "service_latency":
		return true
	default:
		return false
	}
}

func criticalLogSupports(logs logsToolData) []string {
	text := strings.ToLower(strings.Join(append(append([]string{}, logs.Snippets...), logs.SecurityHints...), " "))
	if strings.TrimSpace(text) == "" {
		return nil
	}
	out := make([]string, 0, 4)
	if strings.Contains(text, "oomkilled") || strings.Contains(text, "oom killed") || strings.Contains(text, "out of memory") || strings.Contains(text, "oom killer") {
		out = append(out, "oom_kill_signal")
	}
	if strings.Contains(text, "evict") || strings.Contains(text, "eviction") || strings.Contains(text, "memory pressure") {
		out = append(out, "node_eviction_signal")
	}
	if strings.Contains(text, "xid") || strings.Contains(text, "page retirement") || strings.Contains(text, "gpu has fallen off the bus") || strings.Contains(text, "gpu reset") || strings.Contains(text, "nvrm") || strings.Contains(text, "nvml") {
		out = append(out, "gpu_fault_signal")
	}
	if strings.Contains(text, "crashloop") || strings.Contains(text, "restart loop") || strings.Contains(text, "back-off restarting") {
		out = append(out, "restart_loop_signal")
	}
	return dedupeStrings(out)
}

func logsSuggestIncidentBurst(logs logsToolData) bool {
	if logs.Errors >= 3 {
		return true
	}
	text := strings.ToLower(strings.Join(append(append([]string{}, logs.Snippets...), logs.SecurityHints...), " "))
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, token := range []string{
		"error", "fail", "failed", "failure", "timeout", "timed out", "retry", "exception",
		"panic", "oom", "evict", "xid", "gpu reset", "restart loop", "crashloop", "back-off restarting",
		"connection reset", "permission denied",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func gpuMemoryPinned(observations []behaviorSignalObservation) bool {
	memHigh := false
	utilLow := false
	for _, obs := range observations {
		switch obs.SignalID {
		case "gpu_memory_pressure":
			memHigh = obs.Current >= obs.High
		case "gpu_utilization":
			utilLow = obs.Current > 0 && obs.Current <= 25
		}
	}
	return memHigh && utilLow
}

func buildBehaviorStats(obs behaviorSignalObservation, history []ingest.MetricHistorySample, currentWindowStart time.Time) behaviorSignalStats {
	points := historicalSignalPoints(obs.SignalID, history, currentWindowStart)
	stats := behaviorSignalStats{
		SampleCount:    len(points),
		TemporalBucket: timeBucketLabel(obs.Timestamp),
	}
	if len(points) == 0 {
		return stats
	}
	stats.LongBaseline = meanPoints(points)
	stats.HighWater = maxPointValue(points)
	stddev := stddevPoints(points, stats.LongBaseline)
	stats.Threshold = recurrenceThreshold(obs, stats.LongBaseline, 0, stddev)
	if stddev > 1e-9 {
		stats.ZScore = (obs.Current - stats.LongBaseline) / stddev
	}
	stats.DeviationLongPct = percentChange(stats.LongBaseline, obs.Current)

	bucketID := timeBucketID(obs.Timestamp)
	temporal := filterTemporalPoints(points, bucketID)
	if len(temporal) > 0 {
		stats.TemporalBaseline = meanPoints(temporal)
		stats.TemporalMatches = countBurstRuns(temporal, recurrenceThreshold(obs, stats.LongBaseline, stats.TemporalBaseline, stddev), 6*time.Hour)
		stats.Threshold = recurrenceThreshold(obs, stats.LongBaseline, stats.TemporalBaseline, stddev)
	}
	stats.RecurrenceCount = countBurstRuns(points, stats.Threshold, 90*time.Minute)
	return stats
}

func historicalSignalPoints(signalID string, history []ingest.MetricHistorySample, cutoff time.Time) []RiskSeriesPoint {
	if len(history) == 0 {
		return nil
	}
	if signalID == "memory_leak_rate" {
		base := historicalSignalPoints("memory_pressure", history, cutoff)
		if len(base) < 2 {
			return nil
		}
		out := make([]RiskSeriesPoint, 0, len(base)-1)
		for i := 1; i < len(base); i++ {
			prev := base[i-1]
			curr := base[i]
			dtMin := curr.Timestamp.Sub(prev.Timestamp).Minutes()
			if dtMin <= 0 {
				continue
			}
			rate := (curr.Value - prev.Value) / dtMin
			if rate < 0 {
				rate = 0
			}
			out = append(out, RiskSeriesPoint{Timestamp: curr.Timestamp, Value: rate})
		}
		return out
	}
	spec, ok := riskSeriesSpecByKey(signalID)
	if !ok {
		return nil
	}
	out := make([]RiskSeriesPoint, 0, len(history))
	for _, sample := range history {
		if !cutoff.IsZero() && !sample.Timestamp.Before(cutoff) {
			continue
		}
		value, ok := spec.extract(sample.Metrics)
		if !ok {
			continue
		}
		out = append(out, RiskSeriesPoint{
			Timestamp: sample.Timestamp.UTC(),
			Value:     value,
		})
	}
	return out
}

func riskSeriesSpecByKey(key string) (riskSeriesSpec, bool) {
	for _, spec := range riskSeriesSpecs() {
		if spec.key == key {
			return spec, true
		}
	}
	return riskSeriesSpec{}, false
}

func filterTemporalPoints(points []RiskSeriesPoint, bucketID int) []RiskSeriesPoint {
	out := make([]RiskSeriesPoint, 0, len(points)/8+1)
	for _, point := range points {
		if timeBucketID(point.Timestamp) == bucketID {
			out = append(out, point)
		}
	}
	return out
}

func recurrenceThreshold(obs behaviorSignalObservation, longBaseline, temporalBaseline, stddev float64) float64 {
	threshold := obs.Medium
	if longBaseline > 0 && stddev > 0 {
		threshold = maxFloat(threshold, longBaseline+0.8*stddev)
	}
	if temporalBaseline > 0 {
		threshold = maxFloat(threshold, temporalBaseline*0.92)
	}
	if obs.Current > 0 {
		threshold = minFloat(obs.Current, maxFloat(threshold, obs.Current*0.75))
	}
	return threshold
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func countBurstRuns(points []RiskSeriesPoint, threshold float64, minGap time.Duration) int {
	if len(points) == 0 || threshold <= 0 {
		return 0
	}
	count := 0
	lastBurstAt := time.Time{}
	inBurst := false
	for _, point := range points {
		active := point.Value >= threshold
		if !active {
			inBurst = false
			continue
		}
		if inBurst {
			continue
		}
		if lastBurstAt.IsZero() || point.Timestamp.Sub(lastBurstAt) >= minGap {
			count++
		}
		lastBurstAt = point.Timestamp
		inBurst = true
	}
	return count
}

func assessBehaviorSignal(cfg BehavioralMemoryConfig, identity behaviorIdentity, obs behaviorSignalObservation, stats behaviorSignalStats, ctx behaviorSignalContext) BehavioralSignalAssessment {
	deviationShort := percentChange(obs.ShortBaseline, obs.Current)
	supportWeight := anomalySupportWeight(ctx.AnomalySupport)
	corroborated := hasCorroboratingIncidentEvidence(ctx.AnomalySupport)
	criticalSupport := hasCriticalFaultEvidence(ctx.AnomalySupport)
	healthyLoadShape := containsBehaviorSupport(ctx.ExpectedHints, "healthy_load_shape")
	deployWarmupShape := containsBehaviorSupport(ctx.ExpectedHints, "recent_deploy_context") && signalLikelyDeployWarmup(obs.SignalID)
	recurringScore := 0.0
	if stats.RecurrenceCount >= cfg.MinRecurringBursts {
		recurringScore += 0.30
	}
	if stats.TemporalMatches >= maxInt(1, cfg.MinRecurringBursts-1) {
		recurringScore += 0.22
	}
	if stats.TemporalBaseline > 0 && absFloat(percentChange(stats.TemporalBaseline, obs.Current)) <= 18 {
		recurringScore += 0.20
	}
	if stats.HighWater > 0 && obs.Current <= stats.HighWater*1.10 {
		recurringScore += 0.15
	}
	if len(ctx.ExpectedHints) > 0 {
		recurringScore += 0.05 * float64(len(ctx.ExpectedHints))
	}
	if containsBehaviorSupport(ctx.PeerSignals, "peer_group_burst") {
		recurringScore += 0.12
	}
	recurringScore = clamp01(recurringScore)

	anomalyScore := supportWeight
	switch {
	case math.Abs(stats.ZScore) >= 3.5:
		anomalyScore += 0.24
	case math.Abs(stats.ZScore) >= 2.5:
		anomalyScore += 0.12
	}
	if stats.HighWater > 0 && obs.Current >= stats.HighWater*1.15 {
		anomalyScore += 0.24
	}
	switch {
	case absFloat(stats.DeviationLongPct) >= 100:
		anomalyScore += 0.18
	case absFloat(stats.DeviationLongPct) >= 60:
		anomalyScore += 0.10
	}
	if obs.PersistencePoints >= 3 {
		switch {
		case obs.Current >= obs.High:
			anomalyScore += 0.10
		default:
			anomalyScore += 0.04
		}
	}
	if containsBehaviorSupport(ctx.PeerSignals, "peer_outlier") {
		anomalyScore += 0.14
	}
	anomalyScore = clamp01(anomalyScore)
	extremeDeviation := (stats.HighWater > 0 && obs.Current > stats.HighWater*1.15) || absFloat(stats.DeviationLongPct) >= 80

	classification := "suspicious_deviation"
	confidence := clamp01(0.35 + maxFloat(recurringScore, anomalyScore)*0.45)
	suppression := 0.0

	switch {
	case criticalSupport:
		classification = "confirmed_anomaly"
		confidence = clamp01(0.58 + anomalyScore*0.30)
	case corroborated && supportWeight >= 0.28 && (obs.Current >= obs.Medium || absFloat(stats.DeviationLongPct) >= 25 || stats.HighWater == 0 || obs.Current >= stats.HighWater*1.10):
		if extremeDeviation {
			classification = "confirmed_anomaly"
			confidence = clamp01(0.50 + anomalyScore*0.35)
		} else {
			classification = "correlated_anomaly"
			confidence = clamp01(0.50 + anomalyScore*0.30)
		}
	case stats.SampleCount < cfg.MinSamples:
		classification = "suspicious_deviation"
		confidence = 0.35
	case stats.RecurrenceCount >= cfg.MinRecurringBursts &&
		stats.TemporalMatches >= 1 &&
		!corroborated &&
		!containsBehaviorSupport(ctx.PeerSignals, "peer_outlier") &&
		obs.SignalID != "memory_leak_rate" &&
		obs.Current <= maxFloat(stats.HighWater*1.10, stats.Threshold*1.15):
		classification = "expected_recurring_burst"
		suppression = clamp01(0.25 + recurringScore*0.55)
		confidence = clamp01(0.45 + recurringScore*0.35)
	case anomalyScore >= 0.40 ||
		extremeDeviation ||
		absFloat(stats.DeviationLongPct) >= 80:
		classification = "confirmed_anomaly"
		confidence = clamp01(0.48 + anomalyScore*0.35)
	}
	if classification == "confirmed_anomaly" && !corroborated && !criticalSupport && !containsBehaviorSupport(ctx.PeerSignals, "peer_outlier") {
		switch {
		case healthyLoadShape:
			classification = "suspicious_deviation"
			confidence = clamp01(maxFloat(confidence, 0.48))
		case deployWarmupShape && obs.PersistencePoints <= 3:
			classification = "suspicious_deviation"
			confidence = clamp01(maxFloat(confidence, 0.42))
		}
	}

	return BehavioralSignalAssessment{
		SignalID:              obs.SignalID,
		EntityKey:             identity.key,
		Entity:                identity.entity,
		Service:               identity.service,
		WorkloadClass:         identity.workloadClass,
		WorkloadRole:          identity.workloadRole,
		Current:               obs.Current,
		ShortTermBaseline:     obs.ShortBaseline,
		LongTermBaseline:      stats.LongBaseline,
		TemporalBaseline:      stats.TemporalBaseline,
		DeviationFromShortPct: deviationShort,
		DeviationFromLongPct:  stats.DeviationLongPct,
		RecurrenceCount:       stats.RecurrenceCount,
		TemporalBucket:        stats.TemporalBucket,
		CrossSignalSupport:    dedupeStrings(append(append([]string{}, ctx.AnomalySupport...), ctx.PeerSignals...)),
		Classification:        classification,
		Confidence:            confidence,
		SuppressionFactor:     suppression,
		Explanation:           explainBehaviorAssessment(identity, obs, stats, classification, ctx, cfg.MinSamples),
		MemorySamples:         stats.SampleCount,
	}
}

func explainBehaviorAssessment(identity behaviorIdentity, obs behaviorSignalObservation, stats behaviorSignalStats, classification string, ctx behaviorSignalContext, minSamples int) string {
	entity := firstNonEmpty(identity.service, identity.entity)
	switch classification {
	case "expected_recurring_burst":
		if containsBehaviorSupport(ctx.PeerSignals, "peer_group_burst") {
			return fmt.Sprintf("%s matches %d similar spikes from TSDB history for %s around %s, and same-service peers are under the same healthy burst now.",
				obs.SignalID, stats.RecurrenceCount, entity, stats.TemporalBucket)
		}
		return fmt.Sprintf("%s matches %d similar spikes from TSDB history for %s around %s and has no corroborating error, runtime, or latency regression now.",
			obs.SignalID, stats.RecurrenceCount, entity, stats.TemporalBucket)
	case "correlated_anomaly":
		return fmt.Sprintf("%s would otherwise resemble prior behavior for %s, but current evidence from %s makes this burst incident-worthy.",
			obs.SignalID, entity, supportSummary(ctx.AnomalySupport))
	case "confirmed_anomaly":
		if containsBehaviorSupport(ctx.AnomalySupport, "oom_kill_signal") {
			return fmt.Sprintf("%s is accompanied by OOM or kill signals for %s, so the detector keeps it as a confirmed anomaly instead of a warmup-style burst.",
				obs.SignalID, entity)
		}
		if containsBehaviorSupport(ctx.AnomalySupport, "node_eviction_signal") {
			return fmt.Sprintf("%s is now coupled with eviction-style memory pressure on %s, which is an infrastructure incident rather than a harmless burst.",
				obs.SignalID, entity)
		}
		if containsBehaviorSupport(ctx.AnomalySupport, "gpu_fault_signal") {
			return fmt.Sprintf("%s matches prior utilization on %s, but current GPU fault evidence makes it a confirmed anomaly.",
				obs.SignalID, entity)
		}
		if containsBehaviorSupport(ctx.AnomalySupport, "gpu_memory_pinned") {
			return fmt.Sprintf("%s stays high while GPU utilization remains low on %s, which looks like pinned GPU memory or a stuck workload.",
				obs.SignalID, entity)
		}
		if obs.SignalID == "memory_leak_rate" {
			return fmt.Sprintf("%s shows sustained memory leak growth for %s and remains materially outside the long-window baseline.",
				obs.SignalID, entity)
		}
		if len(ctx.AnomalySupport) > 0 {
			return fmt.Sprintf("%s would otherwise resemble prior behavior for %s, but current evidence from %s makes this burst incident-worthy.",
				obs.SignalID, entity, supportSummary(ctx.AnomalySupport))
		}
		if stats.HighWater > 0 {
			return fmt.Sprintf("%s is above the TSDB high-water mark for %s and is materially outside the long-window baseline.",
				obs.SignalID, entity)
		}
		return fmt.Sprintf("%s has no comparable TSDB history for %s and is large enough to keep as a real anomaly.",
			obs.SignalID, entity)
	default:
		if containsBehaviorSupport(ctx.ExpectedHints, "healthy_load_shape") {
			return fmt.Sprintf("%s looks load-driven for %s: CPU and network rose together without the error or latency harm that would justify a confirmed incident, so the detector downgrades it and keeps it visible.",
				obs.SignalID, entity)
		}
		if containsBehaviorSupport(ctx.ExpectedHints, "recent_deploy_context") && signalLikelyDeployWarmup(obs.SignalID) {
			return fmt.Sprintf("%s lines up with recent deploy or startup activity on %s; without corroborating harm the detector keeps it visible but downgraded instead of confirming a regression.",
				obs.SignalID, entity)
		}
		if stats.SampleCount < minSamples {
			return fmt.Sprintf("not enough TSDB history for %s on %s yet; keeping the burst visible instead of suppressing it.",
				obs.SignalID, entity)
		}
		if containsBehaviorSupport(ctx.PeerSignals, "peer_outlier") {
			return fmt.Sprintf("%s is above baseline for %s and is isolated to this replica relative to same-service peers, so the burst stays visible.",
				obs.SignalID, entity)
		}
		return fmt.Sprintf("%s is above baseline for %s, but the historical match is incomplete and there is no strong corroborating evidence either way.",
			obs.SignalID, entity)
	}
}

func anomalySupportWeight(support []string) float64 {
	total := 0.0
	for _, item := range support {
		switch strings.TrimSpace(item) {
		case "oom_kill_signal", "node_eviction_signal", "gpu_fault_signal":
			total += 0.36
		case "error_log_burst":
			total += 0.30
		case "service_latency_regression":
			total += 0.30
		case "runtime_behavior_agreement":
			total += 0.28
		case "security_findings":
			total += 0.28
		case "gpu_memory_pinned", "restart_loop_signal":
			total += 0.26
		case "multi_signal_agreement":
			total += 0.18
		default:
			total += 0.12
		}
	}
	return clamp01(total)
}

func hasCorroboratingIncidentEvidence(support []string) bool {
	for _, item := range support {
		switch strings.TrimSpace(item) {
		case "error_log_burst", "service_latency_regression", "runtime_behavior_agreement", "security_findings", "oom_kill_signal", "node_eviction_signal", "gpu_fault_signal", "gpu_memory_pinned", "restart_loop_signal":
			return true
		}
	}
	return false
}

func hasCriticalFaultEvidence(support []string) bool {
	for _, item := range support {
		switch strings.TrimSpace(item) {
		case "oom_kill_signal", "node_eviction_signal", "gpu_fault_signal":
			return true
		}
	}
	return false
}

func containsBehaviorSupport(items []string, want string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}

func supportSummary(items []string) string {
	if len(items) == 0 {
		return "correlated evidence"
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		switch strings.TrimSpace(item) {
		case "oom_kill_signal":
			labels = append(labels, "oom/kill signals")
		case "node_eviction_signal":
			labels = append(labels, "eviction pressure")
		case "gpu_fault_signal":
			labels = append(labels, "gpu fault signals")
		case "gpu_memory_pinned":
			labels = append(labels, "gpu memory pinned pattern")
		case "restart_loop_signal":
			labels = append(labels, "restart-loop signals")
		case "error_log_burst":
			labels = append(labels, "error-log burst")
		case "service_latency_regression":
			labels = append(labels, "service latency regression")
		case "runtime_behavior_agreement":
			labels = append(labels, "runtime behavior agreement")
		case "security_findings":
			labels = append(labels, "security findings")
		case "multi_signal_agreement":
			labels = append(labels, "multi-signal agreement")
		default:
			labels = append(labels, item)
		}
	}
	return strings.Join(dedupeStrings(labels), ", ")
}

func meanPoints(points []RiskSeriesPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	sum := 0.0
	for _, point := range points {
		sum += point.Value
	}
	return sum / float64(len(points))
}

func stddevPoints(points []RiskSeriesPoint, mean float64) float64 {
	if len(points) < 2 {
		return 0
	}
	sum := 0.0
	for _, point := range points {
		delta := point.Value - mean
		sum += delta * delta
	}
	return math.Sqrt(sum / float64(len(points)-1))
}

func maxPointValue(points []RiskSeriesPoint) float64 {
	maxValue := 0.0
	for _, point := range points {
		if point.Value > maxValue {
			maxValue = point.Value
		}
	}
	return maxValue
}

func timeBucketID(ts time.Time) int {
	ts = ts.UTC()
	return ts.Hour()
}

func timeBucketLabel(ts time.Time) string {
	ts = ts.UTC()
	return fmt.Sprintf("%02d:00 UTC", ts.Hour())
}

func behavioralAssessmentIndex(items []BehavioralSignalAssessment) map[string]BehavioralSignalAssessment {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]BehavioralSignalAssessment, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.SignalID) == "" {
			continue
		}
		out[item.SignalID] = item
	}
	return out
}
