package controller

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

func (c *Controller) registerEBPFHandlers(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/api/v1/ebpf/events", c.withCORS(c.handleEBPFEvents))
	mux.HandleFunc("/api/v1/ebpf/summary", c.withCORS(c.handleEBPFSummary))
}

func (c *Controller) handleEBPFEvents(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if c.ingestStore == nil {
		http.Error(w, "ingest store unavailable", http.StatusServiceUnavailable)
		return
	}

	collectorID := strings.TrimSpace(firstNonEmpty(
		r.URL.Query().Get("collector_id"),
		r.URL.Query().Get("collector"),
		r.URL.Query().Get("node"),
	))
	limit := parseLimit(r)
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	events := collectRuntimeSecurityEvents(c.ingestStore, collectorID, limit)
	writeJSON(w, map[string]any{
		"collector_id": collectorID,
		"events":       events,
		"count":        len(events),
		"timestamp":    time.Now().UTC(),
	})
}

func (c *Controller) handleEBPFSummary(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if c.ingestStore == nil {
		http.Error(w, "ingest store unavailable", http.StatusServiceUnavailable)
		return
	}

	collectorID := strings.TrimSpace(firstNonEmpty(
		r.URL.Query().Get("collector_id"),
		r.URL.Query().Get("collector"),
		r.URL.Query().Get("node"),
	))
	node := latestNodeSnapshot(c.ingestStore, collectorID)
	if node == nil {
		writeJSON(w, map[string]any{
			"collector_id": collectorID,
			"summary":      map[string]any{},
			"timestamp":    time.Now().UTC(),
		})
		return
	}

	recentEvents := append([]ingest.RuntimeSecurityEvent(nil), node.RuntimeSecurityEvents...)
	sort.Slice(recentEvents, func(i, j int) bool {
		return recentEvents[i].Timestamp.After(recentEvents[j].Timestamp)
	})
	if len(recentEvents) > 100 {
		recentEvents = recentEvents[:100]
	}

	findingsBySeverity := map[string]int{
		"critical": 0,
		"high":     0,
		"medium":   0,
		"low":      0,
	}
	findingsByCategory := map[string]int{}
	for _, event := range recentEvents {
		sev := strings.ToLower(strings.TrimSpace(event.Severity))
		if _, ok := findingsBySeverity[sev]; ok {
			findingsBySeverity[sev]++
		}
		cat := strings.ToLower(strings.TrimSpace(event.Category))
		if cat != "" {
			findingsByCategory[cat]++
		}
	}

	resourceStats := make([]map[string]any, 0, len(node.ProcessResources))
	for _, item := range node.ProcessResources {
		if item == nil {
			continue
		}
		resourceStats = append(resourceStats, map[string]any{
			"pid":                item.PID,
			"name":               item.Name,
			"signal_totals":      item.SignalTotals,
			"category_totals":    item.CategoryTotals,
			"signal_frequency":   item.SignalFrequency,
			"category_frequency": item.CategoryFrequency,
			"last_seen":          item.LastSeen,
		})
	}
	sort.Slice(resourceStats, func(i, j int) bool {
		left := processResourceScore(resourceStats[i])
		right := processResourceScore(resourceStats[j])
		return left > right
	})
	if len(resourceStats) > 64 {
		resourceStats = resourceStats[:64]
	}

	writeJSON(w, map[string]any{
		"collector_id": node.CollectorID,
		"hostname":     node.Hostname,
		"summary": map[string]any{
			"runtime_mode":             node.Metrics["node_ebpf_runtime_mode"],
			"syscall_statistics":       node.SyscallStatistics,
			"process_graph_snapshot":   node.ProcessGraphSnapshot,
			"network_behavior_summary": node.NetworkBehavior,
			"resource_stats":           resourceStats,
			"events_by_severity":       findingsBySeverity,
			"events_by_category":       findingsByCategory,
			"recent_events":            recentEvents,
		},
		"timestamp": time.Now().UTC(),
	})
}

func latestNodeSnapshot(store *ingest.MemoryStore, collectorID string) *ingest.NodeSnapshot {
	if store == nil {
		return nil
	}
	if collectorID != "" {
		return store.Node(collectorID)
	}
	nodes := store.Snapshot()
	var latest *ingest.NodeSnapshot
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if latest == nil || node.UpdatedAt.After(latest.UpdatedAt) {
			latest = node
		}
	}
	return latest
}

func collectRuntimeSecurityEvents(store *ingest.MemoryStore, collectorID string, limit int) []ingest.RuntimeSecurityEvent {
	if store == nil {
		return nil
	}
	nodes := store.Snapshot()
	out := make([]ingest.RuntimeSecurityEvent, 0, 256)
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if collectorID != "" && !strings.EqualFold(strings.TrimSpace(node.CollectorID), collectorID) {
			continue
		}
		out = append(out, node.RuntimeSecurityEvents...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func processResourceScore(item map[string]any) float64 {
	rawTotals, _ := item["category_totals"].(map[string]float64)
	score := 0.0
	for _, v := range rawTotals {
		score += v
	}
	if rawSignals, ok := item["signal_totals"].(map[string]float64); ok {
		for _, v := range rawSignals {
			score += v
		}
	}
	return score
}
