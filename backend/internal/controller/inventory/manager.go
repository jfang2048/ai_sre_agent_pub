package inventory

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

// SnapshotProvider links inventory to push-ingested telemetry.
type SnapshotProvider interface {
	Snapshot() []*ingest.NodeSnapshot
}

// Heartbeat registers or refreshes probe liveness.
type Heartbeat struct {
	ProbeID   string            `json:"probe_id"`
	Hostname  string            `json:"hostname,omitempty"`
	Address   string            `json:"address,omitempty"`
	Version   string            `json:"version,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp,omitempty"`
}

// Probe describes one probe inventory row.
type Probe struct {
	ID            string            `json:"id"`
	Name          string            `json:"name,omitempty"`
	Hostname      string            `json:"hostname,omitempty"`
	Address       string            `json:"address,omitempty"`
	Version       string            `json:"version,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Sources       []string          `json:"sources"`
	LastHeartbeat time.Time         `json:"last_heartbeat,omitempty"`
	LastTelemetry time.Time         `json:"last_telemetry,omitempty"`
	LastSeen      time.Time         `json:"last_seen,omitempty"`
	Healthy       bool              `json:"healthy"`
}

// Summary is a compact inventory status snapshot.
type Summary struct {
	Enabled       bool      `json:"enabled"`
	Total         int       `json:"total"`
	Healthy       int       `json:"healthy"`
	FromStatic    int       `json:"from_static"`
	FromTelemetry int       `json:"from_telemetry"`
	FromHeartbeat int       `json:"from_heartbeat"`
	HeartbeatTTL  string    `json:"heartbeat_ttl"`
	GeneratedAt   time.Time `json:"generated_at"`
}

type heartbeatRecord struct {
	data       Heartbeat
	receivedAt time.Time
}

// Manager maintains merged probe inventory from static config, telemetry, and optional heartbeat registration.
type Manager struct {
	cfg    Config
	store  SnapshotProvider
	logger *zap.Logger

	mu         sync.RWMutex
	static     map[string]StaticProbe
	heartbeats map[string]heartbeatRecord
}

// NewManager creates a new inventory manager.
func NewManager(cfg Config, static []StaticProbe, store SnapshotProvider, logger *zap.Logger) *Manager {
	if cfg.HeartbeatTTL <= 0 {
		cfg.HeartbeatTTL = DefaultConfig().HeartbeatTTL
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	staticMap := make(map[string]StaticProbe, len(static))
	for _, item := range static {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = strings.TrimSpace(item.Name)
		}
		if id == "" {
			id = strings.TrimSpace(item.Address)
		}
		if id == "" {
			continue
		}
		item.ID = id
		item.Name = firstNonEmpty(item.Name, id)
		item.Labels = cloneLabels(item.Labels)
		staticMap[id] = item
	}

	return &Manager{
		cfg:        cfg,
		store:      store,
		logger:     logger.With(zap.String("component", "probe_inventory")),
		static:     staticMap,
		heartbeats: make(map[string]heartbeatRecord),
	}
}

// UpsertHeartbeat registers probe heartbeat information.
func (m *Manager) UpsertHeartbeat(hb Heartbeat) bool {
	if !m.cfg.Enabled {
		return false
	}
	id := strings.TrimSpace(hb.ProbeID)
	if id == "" {
		return false
	}
	hb.ProbeID = id
	hb.Hostname = strings.TrimSpace(hb.Hostname)
	hb.Address = strings.TrimSpace(hb.Address)
	hb.Version = strings.TrimSpace(hb.Version)
	hb.Labels = cloneLabels(hb.Labels)

	receivedAt := time.Now()
	if hb.Timestamp.IsZero() {
		hb.Timestamp = receivedAt
	}

	m.mu.Lock()
	m.heartbeats[id] = heartbeatRecord{
		data:       hb,
		receivedAt: receivedAt,
	}
	m.mu.Unlock()
	return true
}

// List returns merged probe inventory sorted by id.
func (m *Manager) List() []Probe {
	probes := m.mergeProbes(time.Now())
	sort.Slice(probes, func(i, j int) bool { return probes[i].ID < probes[j].ID })
	return probes
}

// Get returns one probe by id.
func (m *Manager) Get(id string) (Probe, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Probe{}, false
	}
	for _, probe := range m.mergeProbes(time.Now()) {
		if probe.ID == id {
			return probe, true
		}
	}
	return Probe{}, false
}

// Summary returns inventory summary counters.
func (m *Manager) Summary() Summary {
	now := time.Now()
	probes := m.mergeProbes(now)
	summary := Summary{
		Enabled:      m.cfg.Enabled,
		Total:        len(probes),
		HeartbeatTTL: m.cfg.HeartbeatTTL.String(),
		GeneratedAt:  now,
	}
	for _, probe := range probes {
		if probe.Healthy {
			summary.Healthy++
		}
		hasStatic := false
		hasTelemetry := false
		hasHeartbeat := false
		for _, source := range probe.Sources {
			switch source {
			case "static":
				hasStatic = true
			case "telemetry":
				hasTelemetry = true
			case "heartbeat":
				hasHeartbeat = true
			}
		}
		if hasStatic {
			summary.FromStatic++
		}
		if hasTelemetry {
			summary.FromTelemetry++
		}
		if hasHeartbeat {
			summary.FromHeartbeat++
		}
	}
	return summary
}

func (m *Manager) mergeProbes(now time.Time) []Probe {
	if !m.cfg.Enabled {
		return []Probe{}
	}

	probes := map[string]*Probe{}
	sourceSet := map[string]map[string]struct{}{}

	ensure := func(id string) *Probe {
		if id == "" {
			return nil
		}
		existing := probes[id]
		if existing != nil {
			return existing
		}
		entry := &Probe{
			ID:     id,
			Name:   "",
			Labels: map[string]string{},
		}
		probes[id] = entry
		sourceSet[id] = map[string]struct{}{}
		return entry
	}
	addSource := func(id, source string) {
		if id == "" || source == "" {
			return
		}
		set := sourceSet[id]
		if set == nil {
			set = map[string]struct{}{}
			sourceSet[id] = set
		}
		set[source] = struct{}{}
	}
	mergeLabels := func(dst map[string]string, src map[string]string) map[string]string {
		if dst == nil {
			dst = map[string]string{}
		}
		for key, value := range src {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if _, exists := dst[key]; exists {
				continue
			}
			dst[key] = strings.TrimSpace(value)
		}
		return dst
	}

	m.mu.RLock()
	staticCopy := make(map[string]StaticProbe, len(m.static))
	for id, item := range m.static {
		staticCopy[id] = item
	}
	heartbeatCopy := make(map[string]heartbeatRecord, len(m.heartbeats))
	for id, hb := range m.heartbeats {
		heartbeatCopy[id] = hb
	}
	m.mu.RUnlock()

	for id, item := range staticCopy {
		entry := ensure(id)
		if entry == nil {
			continue
		}
		entry.Name = firstNonEmpty(strings.TrimSpace(item.Name), id)
		entry.Address = firstNonEmpty(strings.TrimSpace(item.Address), entry.Address)
		entry.Labels = mergeLabels(entry.Labels, item.Labels)
		addSource(id, "static")
	}

	if m.store != nil {
		for _, node := range m.store.Snapshot() {
			if node == nil {
				continue
			}
			id := strings.TrimSpace(node.CollectorID)
			if id == "" {
				continue
			}
			entry := ensure(id)
			if entry == nil {
				continue
			}
			entry.Hostname = firstNonEmpty(strings.TrimSpace(node.Hostname), entry.Hostname)
			entry.Name = firstNonEmpty(strings.TrimSpace(node.Hostname), entry.Name, id)
			entry.Version = firstNonEmpty(strings.TrimSpace(node.Version), entry.Version)
			entry.Labels = mergeLabels(entry.Labels, node.Labels)

			lastTelemetry := maxTime(node.LastSeen, node.UpdatedAt)
			if lastTelemetry.After(entry.LastTelemetry) {
				entry.LastTelemetry = lastTelemetry
			}
			addSource(id, "telemetry")
		}
	}

	for id, hb := range heartbeatCopy {
		entry := ensure(id)
		if entry == nil {
			continue
		}
		entry.Hostname = firstNonEmpty(hb.data.Hostname, entry.Hostname)
		entry.Name = firstNonEmpty(hb.data.Hostname, entry.Name, id)
		entry.Address = firstNonEmpty(hb.data.Address, entry.Address)
		entry.Version = firstNonEmpty(hb.data.Version, entry.Version)
		entry.Labels = mergeLabels(entry.Labels, hb.data.Labels)

		lastHeartbeat := hb.data.Timestamp
		if lastHeartbeat.IsZero() {
			lastHeartbeat = hb.receivedAt
		}
		if lastHeartbeat.After(entry.LastHeartbeat) {
			entry.LastHeartbeat = lastHeartbeat
		}
		addSource(id, "heartbeat")
	}

	out := make([]Probe, 0, len(probes))
	for id, entry := range probes {
		if entry == nil {
			continue
		}
		lastSeen := maxTime(entry.LastTelemetry, entry.LastHeartbeat)
		entry.LastSeen = lastSeen
		entry.Healthy = !lastSeen.IsZero() && now.Sub(lastSeen) <= m.cfg.HeartbeatTTL
		entry.Name = firstNonEmpty(entry.Name, entry.Hostname, entry.ID)

		sources := make([]string, 0, len(sourceSet[id]))
		for source := range sourceSet[id] {
			sources = append(sources, source)
		}
		sort.Strings(sources)
		entry.Sources = sources

		entry.Labels = cloneLabels(entry.Labels)
		out = append(out, *entry)
	}
	return out
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func maxTime(values ...time.Time) time.Time {
	var best time.Time
	for _, value := range values {
		if value.After(best) {
			best = value
		}
	}
	return best
}
