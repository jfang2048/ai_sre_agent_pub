package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	topologyFleetNodeID      = "fleet"
	maxTopologyProcessesHost = 6
)

type TopologyNode struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Type              string  `json:"type"` // fleet | host | process
	Status            string  `json:"status"`
	CollectorID       string  `json:"collector_id,omitempty"`
	Hostname          string  `json:"hostname,omitempty"`
	PID               string  `json:"pid,omitempty"`
	Score             float64 `json:"score,omitempty"`
	CPUPercent        float64 `json:"cpu_percent,omitempty"`
	MemoryBytes       uint64  `json:"memory_bytes,omitempty"`
	NetBytesPerSecond float64 `json:"net_bytes_per_second,omitempty"`
	DiskBytesPerSec   float64 `json:"disk_bytes_per_second,omitempty"`
	ProcessCount      int     `json:"process_count,omitempty"`
}

type TopologyLink struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Kind   string  `json:"kind"`
	Weight float64 `json:"weight,omitempty"`
}

type TopologySummary struct {
	HostCount     int `json:"host_count"`
	ProcessCount  int `json:"process_count"`
	CriticalHosts int `json:"critical_hosts"`
	DegradedHosts int `json:"degraded_hosts"`
}

type TopologyResponse struct {
	CollectorID string          `json:"collector_id,omitempty"`
	GeneratedAt time.Time       `json:"generated_at"`
	Summary     TopologySummary `json:"summary"`
	Nodes       []TopologyNode  `json:"nodes"`
	Links       []TopologyLink  `json:"links"`
}

func (c *Controller) handleTopology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	collectorID := strings.TrimSpace(r.URL.Query().Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(r.URL.Query().Get("collector"))
	}

	nodes, links, summary := c.buildTopologyGraph(collectorID)
	resp := TopologyResponse{
		CollectorID: collectorID,
		GeneratedAt: time.Now(),
		Summary:     summary,
		Nodes:       nodes,
		Links:       links,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (c *Controller) buildTopologyGraph(collectorID string) ([]TopologyNode, []TopologyLink, TopologySummary) {
	if c.ingestStore == nil {
		return []TopologyNode{}, []TopologyLink{}, TopologySummary{}
	}

	snapshots := c.ingestStore.Snapshot()
	filtered := make([]*topologyHostSnapshot, 0, len(snapshots))
	for _, snap := range snapshots {
		if collectorID != "" && snap.CollectorID != collectorID {
			continue
		}
		name := strings.TrimSpace(snap.Hostname)
		if name == "" {
			name = snap.CollectorID
		}
		filtered = append(filtered, &topologyHostSnapshot{
			CollectorID: snap.CollectorID,
			Hostname:    name,
		})
	}

	programs := c.aggregateTopProgramsFiltered(maxTopProgramsLimit, collectorID)
	programsByCollector := make(map[string][]ProgramStats)
	for _, p := range programs {
		if p.CollectorID == "" {
			continue
		}
		programsByCollector[p.CollectorID] = append(programsByCollector[p.CollectorID], p)
	}
	for collector, rows := range programsByCollector {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Score != rows[j].Score {
				return rows[i].Score > rows[j].Score
			}
			if rows[i].Name != rows[j].Name {
				return rows[i].Name < rows[j].Name
			}
			return rows[i].PID < rows[j].PID
		})
		programsByCollector[collector] = rows
	}

	filteredMap := make(map[string]*topologyHostSnapshot, len(filtered))
	for _, host := range filtered {
		filteredMap[host.CollectorID] = host
	}
	for collector, rows := range programsByCollector {
		if _, ok := filteredMap[collector]; ok {
			continue
		}
		hostname := collector
		if len(rows) > 0 && strings.TrimSpace(rows[0].Hostname) != "" {
			hostname = strings.TrimSpace(rows[0].Hostname)
		}
		filtered = append(filtered, &topologyHostSnapshot{
			CollectorID: collector,
			Hostname:    hostname,
		})
		filteredMap[collector] = filtered[len(filtered)-1]
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Hostname != filtered[j].Hostname {
			return filtered[i].Hostname < filtered[j].Hostname
		}
		return filtered[i].CollectorID < filtered[j].CollectorID
	})

	nodes := make([]TopologyNode, 0, len(filtered)*4+1)
	links := make([]TopologyLink, 0, len(filtered)*4)
	summary := TopologySummary{}

	if len(filtered) > 0 {
		nodes = append(nodes, TopologyNode{
			ID:     topologyFleetNodeID,
			Name:   "Fleet",
			Type:   "fleet",
			Status: "healthy",
		})
	}

	hostIDByCollector := make(map[string]string, len(filtered))
	seenProcessNodes := make(map[string]struct{})

	for _, host := range filtered {
		hostPrograms := programsByCollector[host.CollectorID]
		hostStatus := "healthy"
		maxScore := 0.0
		if len(hostPrograms) > 0 {
			maxScore = hostPrograms[0].Score
			hostStatus = topologySeverityFromScore(maxScore)
		}

		hostNodeID := "host:" + host.CollectorID
		hostIDByCollector[host.CollectorID] = hostNodeID

		hostNode := TopologyNode{
			ID:           hostNodeID,
			Name:         host.Hostname,
			Type:         "host",
			Status:       hostStatus,
			CollectorID:  host.CollectorID,
			Hostname:     host.Hostname,
			Score:        maxScore,
			ProcessCount: len(hostPrograms),
		}
		nodes = append(nodes, hostNode)
		summary.HostCount++
		switch hostStatus {
		case "critical":
			summary.CriticalHosts++
		case "degraded":
			summary.DegradedHosts++
		}

		if len(filtered) > 0 {
			links = append(links, TopologyLink{
				Source: topologyFleetNodeID,
				Target: hostNodeID,
				Kind:   "collector",
				Weight: maxScore,
			})
		}

		topN := len(hostPrograms)
		if topN > maxTopologyProcessesHost {
			topN = maxTopologyProcessesHost
		}
		for i := 0; i < topN; i++ {
			p := hostPrograms[i]
			processNodeID := topologyProcessNodeID(p.CollectorID, p.PID, p.Name)

			if _, ok := seenProcessNodes[processNodeID]; !ok {
				nodes = append(nodes, TopologyNode{
					ID:                processNodeID,
					Name:              p.Name,
					Type:              "process",
					Status:            topologySeverityFromScore(p.Score),
					CollectorID:       p.CollectorID,
					Hostname:          host.Hostname,
					PID:               p.PID,
					Score:             p.Score,
					CPUPercent:        p.CPUPercent,
					MemoryBytes:       p.MemoryBytes,
					NetBytesPerSecond: p.NetBytesPerSecond,
					DiskBytesPerSec:   p.DiskReadBps + p.DiskWriteBps,
				})
				seenProcessNodes[processNodeID] = struct{}{}
				summary.ProcessCount++
			}

			links = append(links, TopologyLink{
				Source: hostNodeID,
				Target: processNodeID,
				Kind:   "hotspot",
				Weight: p.Score,
			})
		}
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(links, func(i, j int) bool {
		if links[i].Source != links[j].Source {
			return links[i].Source < links[j].Source
		}
		if links[i].Target != links[j].Target {
			return links[i].Target < links[j].Target
		}
		return links[i].Kind < links[j].Kind
	})

	return nodes, links, summary
}

type topologyHostSnapshot struct {
	CollectorID string
	Hostname    string
}

func topologySeverityFromScore(score float64) string {
	switch {
	case score >= 6:
		return "critical"
	case score >= 3:
		return "degraded"
	default:
		return "healthy"
	}
}

func topologyProcessNodeID(collectorID, pid, name string) string {
	pid = strings.TrimSpace(pid)
	if pid != "" {
		return "proc:" + collectorID + ":pid:" + pid
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, "/", "_")
	normalized = strings.ReplaceAll(normalized, ":", "_")
	if normalized == "" {
		normalized = "unknown"
	}
	return "proc:" + collectorID + ":name:" + normalized
}
