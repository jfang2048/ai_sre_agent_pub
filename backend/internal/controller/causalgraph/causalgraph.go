package causalgraph

import (
	"sort"
	"strings"
)

// Node is a typed causal-graph vertex.
type Node struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Label    string            `json:"label"`
	Role     string            `json:"role,omitempty"`
	Score    float64           `json:"score,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Edge is a typed causal relationship or dependency edge.
type Edge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Kind   string  `json:"kind"`
	Weight float64 `json:"weight,omitempty"`
}

// Input is the unified graph built from topology, lineage, security, runtime, and change evidence.
type Input struct {
	Nodes        []Node   `json:"nodes"`
	Edges        []Edge   `json:"edges"`
	SymptomNodes []string `json:"symptom_nodes,omitempty"`
	ImpactScope  []string `json:"impact_scope,omitempty"`
}

// Candidate is one scored probable-cause candidate.
type Candidate struct {
	NodeID       string   `json:"node_id"`
	Label        string   `json:"label"`
	Kind         string   `json:"kind"`
	Score        float64  `json:"score"`
	Reasons      []string `json:"reasons,omitempty"`
	CausePath    []string `json:"cause_path,omitempty"`
	ImpactPath   []string `json:"impact_path,omitempty"`
	SymptomScore float64  `json:"symptom_score,omitempty"`
}

// Analysis is the explainable output of causal ranking.
type Analysis struct {
	Candidates               []Candidate `json:"candidates"`
	SuspectedRootCauseEntity string      `json:"suspected_root_cause_entity,omitempty"`
	CausePath                []string    `json:"cause_path,omitempty"`
	ImpactPath               []string    `json:"impact_path,omitempty"`
}

// Analyze ranks likely causes and derives an explainable cause path and impact path.
func Analyze(in Input) Analysis {
	if len(in.Nodes) == 0 {
		return Analysis{}
	}
	nodeByID := make(map[string]Node, len(in.Nodes))
	outbound := make(map[string][]Edge, len(in.Nodes))
	inbound := make(map[string][]Edge, len(in.Nodes))
	symptoms := make(map[string]struct{}, len(in.SymptomNodes))
	impactTargets := make(map[string]struct{}, len(in.ImpactScope))
	for _, node := range in.Nodes {
		nodeByID[node.ID] = node
	}
	for _, edge := range in.Edges {
		outbound[edge.Source] = append(outbound[edge.Source], edge)
		inbound[edge.Target] = append(inbound[edge.Target], edge)
	}
	for _, nodeID := range in.SymptomNodes {
		symptoms[nodeID] = struct{}{}
	}
	for _, item := range in.ImpactScope {
		impactTargets[item] = struct{}{}
	}

	candidates := make([]Candidate, 0, len(in.Nodes))
	for _, node := range in.Nodes {
		score, reasons, symptomScore := rankNode(node, outbound[node.ID], inbound[node.ID], symptoms)
		if score <= 0 {
			continue
		}
		candidate := Candidate{
			NodeID:       node.ID,
			Label:        firstNonEmpty(node.Label, node.ID),
			Kind:         node.Kind,
			Score:        score,
			Reasons:      reasons,
			SymptomScore: symptomScore,
		}
		if len(symptoms) > 0 {
			candidate.CausePath = renderPath(shortestPath(node.ID, symptoms, outbound), nodeByID)
		}
		if len(impactTargets) > 0 {
			candidate.ImpactPath = renderPath(shortestPath(node.ID, impactTargets, outbound), nodeByID)
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Label < candidates[j].Label
		}
		return candidates[i].Score > candidates[j].Score
	})
	if len(candidates) > 8 {
		candidates = candidates[:8]
	}
	analysis := Analysis{Candidates: candidates}
	if len(candidates) > 0 {
		analysis.SuspectedRootCauseEntity = candidates[0].Label
		analysis.CausePath = append([]string(nil), candidates[0].CausePath...)
		analysis.ImpactPath = append([]string(nil), candidates[0].ImpactPath...)
	}
	return analysis
}

func rankNode(node Node, outbound, inbound []Edge, symptoms map[string]struct{}) (float64, []string, float64) {
	score := clamp01(node.Score)
	reasons := make([]string, 0, 6)
	switch strings.ToLower(strings.TrimSpace(node.Kind)) {
	case "change":
		score += 0.35
		reasons = append(reasons, "change event is temporally adjacent and more likely causal than symptomatic")
	case "security", "runtime":
		score += 0.22
		reasons = append(reasons, "runtime/security evidence can represent a direct trigger")
	case "process":
		score += 0.12
		reasons = append(reasons, "process lineage can carry the triggering execution path")
	case "topology":
		score += 0.05
	}
	switch strings.ToLower(strings.TrimSpace(node.Role)) {
	case "cause":
		score += 0.10
		reasons = append(reasons, "node was explicitly tagged as a causal candidate")
	case "symptom":
		score -= 0.18
		reasons = append(reasons, "node is more likely a downstream symptom")
	}
	if len(outbound) > 0 {
		score += minFloat(0.16, float64(len(outbound))*0.04)
		reasons = append(reasons, "node has downstream impact edges")
	}
	symptomScore := 0.0
	for _, edge := range outbound {
		if _, ok := symptoms[edge.Target]; ok {
			symptomScore += maxFloat(edge.Weight, 0.20)
		}
	}
	if symptomScore > 0 {
		score += minFloat(0.22, symptomScore*0.22)
		reasons = append(reasons, "node leads to observed symptoms through the graph")
	}
	for _, edge := range inbound {
		if _, ok := symptoms[edge.Source]; ok {
			score -= 0.10
		}
	}
	return clamp01(score), dedupeStrings(reasons), clamp01(symptomScore)
}

func shortestPath(start string, targets map[string]struct{}, outbound map[string][]Edge) []string {
	if _, ok := targets[start]; ok {
		return []string{start}
	}
	type hop struct {
		Node string
		Path []string
	}
	queue := []hop{{Node: start, Path: []string{start}}}
	visited := map[string]struct{}{start: {}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range outbound[current.Node] {
			if _, ok := visited[edge.Target]; ok {
				continue
			}
			nextPath := append(append([]string(nil), current.Path...), edge.Target)
			if _, ok := targets[edge.Target]; ok {
				return nextPath
			}
			visited[edge.Target] = struct{}{}
			queue = append(queue, hop{Node: edge.Target, Path: nextPath})
		}
	}
	return nil
}

func renderPath(path []string, nodeByID map[string]Node) []string {
	if len(path) == 0 {
		return nil
	}
	out := make([]string, 0, len(path))
	for _, nodeID := range path {
		if node, ok := nodeByID[nodeID]; ok {
			out = append(out, firstNonEmpty(node.Label, node.ID))
			continue
		}
		out = append(out, nodeID)
	}
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
