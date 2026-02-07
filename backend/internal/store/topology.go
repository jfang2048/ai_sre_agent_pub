package store

import (
	"sync"
	"time"
)

// ServiceNode represents a service in the dependency graph
type ServiceNode struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"` // service, database, cache
	Metadata map[string]string `json:"metadata"`
	Status   string            `json:"status"` // healthy, degraded, critical
}

// ServiceLink represents a dependency between two services
type ServiceLink struct {
	Source      string    `json:"source"`
	Target      string    `json:"target"`
	RequestRate float64   `json:"request_rate"` // RPS
	ErrorRate   float64   `json:"error_rate"`   // Percentage
	LatencyP95  float64   `json:"latency_p95"`  // ms
	LastSeen    time.Time `json:"last_seen"`
}

// TopologyGraph represents the entire system view
type TopologyGraph struct {
	Nodes []ServiceNode `json:"nodes"`
	Links []ServiceLink `json:"links"`
}

// TopologyStore manages service dependencies
type TopologyStore struct {
	mu    sync.RWMutex
	nodes map[string]*ServiceNode
	links map[string]*ServiceLink // Key: "src->dst"
}

// NewTopologyStore creates a new store
func NewTopologyStore() *TopologyStore {
	return &TopologyStore{
		nodes: make(map[string]*ServiceNode),
		links: make(map[string]*ServiceLink),
	}
}

// RegisterNode updates or creates a service node
func (ts *TopologyStore) RegisterNode(name, nodeType string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, exists := ts.nodes[name]; !exists {
		ts.nodes[name] = &ServiceNode{
			Name:     name,
			Type:     nodeType,
			Metadata: make(map[string]string),
			Status:   "healthy",
		}
	}
}

// RecordInteraction updates the link statistics between services
func (ts *TopologyStore) RecordInteraction(src, dst string, latency float64, isError bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Ensure nodes exist (auto-discovery)
	if _, ok := ts.nodes[src]; !ok {
		ts.nodes[src] = &ServiceNode{Name: src, Type: "service", Status: "healthy"}
	}
	if _, ok := ts.nodes[dst]; !ok {
		ts.nodes[dst] = &ServiceNode{Name: dst, Type: "service", Status: "healthy"}
	}

	key := src + "->" + dst
	link, ok := ts.links[key]
	if !ok {
		link = &ServiceLink{Source: src, Target: dst}
		ts.links[key] = link
	}

	// Simple EMA for stats
	alpha := 0.1
	if link.LatencyP95 == 0 {
		link.LatencyP95 = latency
	} else {
		link.LatencyP95 = alpha*latency + (1-alpha)*link.LatencyP95
	}

	// Update error rate (simplified window)
	errVal := 0.0
	if isError {
		errVal = 1.0
	}
	if link.ErrorRate == 0 {
		link.ErrorRate = errVal
	} else {
		link.ErrorRate = alpha*errVal + (1-alpha)*link.ErrorRate
	}

	link.LastSeen = time.Now()
}

// GetGraph returns the current topology
func (ts *TopologyStore) GetGraph() TopologyGraph {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	g := TopologyGraph{
		Nodes: make([]ServiceNode, 0, len(ts.nodes)),
		Links: make([]ServiceLink, 0, len(ts.links)),
	}

	for _, n := range ts.nodes {
		g.Nodes = append(g.Nodes, *n)
	}
	for _, l := range ts.links {
		// Filter stale links > 1 hour
		if time.Since(l.LastSeen) < 1*time.Hour {
			g.Links = append(g.Links, *l)
		}
	}
	return g
}

// CalculateBlastRadius identifies downstream services affected by a failure in 'failedNode'
func (ts *TopologyStore) CalculateBlastRadius(failedNode string) ([]string, error) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	visited := make(map[string]bool)
	affected := make([]string, 0)

	// DFS to find all transitive dependencies where failedNode is upstream
	// Wait, blast radius of A failing: Who depends on A?
	// Links are Source -> Target (Source calls Target)
	// If A fails, Source fails if it calls A.
	// So we look for Links where Target == failedNode (Immediate Impact)
	// And recursively up.

	// 1. Build Reverse Graph (Target -> [Sources])
	revGraph := make(map[string][]string)
	for _, l := range ts.links {
		revGraph[l.Target] = append(revGraph[l.Target], l.Source)
	}

	var traverse func(node string)
	traverse = func(node string) {
		if visited[node] {
			return
		}
		visited[node] = true
		if node != failedNode {
			affected = append(affected, node)
		}

		for _, upstream := range revGraph[node] {
			traverse(upstream)
		}
	}

	traverse(failedNode)
	return affected, nil
}
