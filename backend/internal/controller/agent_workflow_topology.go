package controller

import (
	"context"
	"fmt"
	"time"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/k8sview"
)

// k8sTopologyWorkflowProvider adapts Kubernetes service map snapshots for agent workflows.
type k8sTopologyWorkflowProvider struct {
	manager *k8sview.Manager
}

func (p k8sTopologyWorkflowProvider) Snapshot(_ context.Context) agentcore.TopologySnapshot {
	if p.manager == nil {
		return agentcore.TopologySnapshot{
			GeneratedAt: time.Now().UTC(),
			Summary:     "kubernetes topology unavailable",
			Source:      "none",
		}
	}

	graph := p.manager.ServiceMap()
	nodes := make([]agentcore.TopologyNode, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes = append(nodes, agentcore.TopologyNode{
			ID:        node.ID,
			Name:      node.Name,
			Type:      node.Type,
			Cluster:   node.Cluster,
			Namespace: node.Namespace,
			Status:    node.Status,
			Score:     node.Score,
		})
	}
	edges := make([]agentcore.TopologyEdge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		edges = append(edges, agentcore.TopologyEdge{Source: edge.Source, Target: edge.Target, Kind: edge.Kind})
	}

	generatedAt := graph.GeneratedAt.UTC()
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}

	return agentcore.TopologySnapshot{
		GeneratedAt: generatedAt,
		Nodes:       nodes,
		Edges:       edges,
		Summary:     fmt.Sprintf("kubernetes topology nodes=%d edges=%d", len(nodes), len(edges)),
		Source:      "kubernetes-service-map",
	}
}
