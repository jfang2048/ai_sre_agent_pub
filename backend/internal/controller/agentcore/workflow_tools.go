package agent

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/securityaudit"
	"go.uber.org/zap"
)

const workflowToolVersion = "v0.5.0"

// TopologyNode is a compact workflow topology node.
type TopologyNode struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Cluster   string  `json:"cluster,omitempty"`
	Namespace string  `json:"namespace,omitempty"`
	Status    string  `json:"status,omitempty"`
	Score     float64 `json:"score,omitempty"`
}

// TopologyEdge is a compact workflow topology edge.
type TopologyEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

// TopologySnapshot is the provider-neutral topology payload.
type TopologySnapshot struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Nodes       []TopologyNode `json:"nodes"`
	Edges       []TopologyEdge `json:"edges"`
	Summary     string         `json:"summary"`
	Source      string         `json:"source"`
}

// TopologyProvider supplies a topology snapshot to workflows.
type TopologyProvider interface {
	Snapshot(context.Context) TopologySnapshot
}

type workflowToolRequest struct {
	WorkflowID  string
	Workflow    string
	Stage       string
	CollectorID string
	Window      time.Duration
	Limit       int
	Query       map[string]string
	DryRun      bool
}

type workflowToolResult struct {
	Summary string
	Data    any
}

type workflowTool interface {
	Name() ToolName
	Version() string
	Description() string
	Deterministic() bool
	Unsafe() bool
	Run(context.Context, workflowToolRequest) (workflowToolResult, error)
}

type workflowToolManager struct {
	logger      *zap.Logger
	tools       map[ToolName]workflowTool
	descriptors []WorkflowToolDescriptor
}

func newWorkflowToolManager(logger *zap.Logger, tools ...workflowTool) *workflowToolManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	registry := make(map[ToolName]workflowTool, len(tools))
	descriptors := make([]WorkflowToolDescriptor, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		registry[tool.Name()] = tool
		descriptors = append(descriptors, WorkflowToolDescriptor{
			Name:          tool.Name(),
			Version:       tool.Version(),
			Description:   tool.Description(),
			Deterministic: tool.Deterministic(),
			Unsafe:        tool.Unsafe(),
		})
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].Name < descriptors[j].Name
	})
	return &workflowToolManager{
		logger:      logger.With(zap.String("component", "agent_workflow_tools")),
		tools:       registry,
		descriptors: descriptors,
	}
}

func (m *workflowToolManager) call(ctx context.Context, req workflowToolRequest, name ToolName) (WorkflowToolCall, workflowToolResult, error) {
	started := time.Now().UTC()
	call := WorkflowToolCall{
		ID:          newQueryID(),
		Tool:        name,
		ToolVersion: "unknown",
		Stage:       req.Stage,
		CollectorID: req.CollectorID,
		Window:      req.Window.String(),
		Query:       cloneStringMap(req.Query),
		Status:      "running",
		StartedAt:   started,
	}

	tool, ok := m.tools[name]
	if !ok {
		call.Status = "failed"
		call.ErrorMessage = fmt.Sprintf("tool %s not registered", name)
		call.CompletedAt = time.Now().UTC()
		return call, workflowToolResult{}, fmt.Errorf("tool %s not registered", name)
	}
	call.ToolVersion = tool.Version()

	result, err := tool.Run(ctx, req)
	call.CompletedAt = time.Now().UTC()
	if err != nil {
		call.Status = "failed"
		call.ErrorMessage = err.Error()
		return call, workflowToolResult{}, err
	}
	call.Status = "success"
	call.Summary = strings.TrimSpace(result.Summary)
	return call, result, nil
}

func (m *workflowToolManager) registry() []WorkflowToolDescriptor {
	if m == nil {
		return nil
	}
	out := make([]WorkflowToolDescriptor, len(m.descriptors))
	copy(out, m.descriptors)
	return out
}

type metricsToolData struct {
	CollectorID string
	Node        *ingest.NodeSnapshot
	History     []ingest.MetricHistorySample
	Fleet       []*ingest.NodeSnapshot
}

type metricsQueryTool struct {
	store *ingest.MemoryStore
}

func (t *metricsQueryTool) Name() ToolName  { return ToolMetrics }
func (t *metricsQueryTool) Version() string { return workflowToolVersion }
func (t *metricsQueryTool) Description() string {
	return "Deterministic metric query across node/process/kernel series."
}
func (t *metricsQueryTool) Deterministic() bool { return true }
func (t *metricsQueryTool) Unsafe() bool        { return false }

func (t *metricsQueryTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: "ingest store unavailable", Data: metricsToolData{}}, nil
	}

	collectorID := strings.TrimSpace(req.CollectorID)
	fleet := t.store.Snapshot()
	if collectorID == "" {
		collectorID = pickCollectorFromSnapshot(fleet)
	}
	window := req.Window
	if window <= 0 {
		window = 30 * time.Minute
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 360
	}

	var node *ingest.NodeSnapshot
	if collectorID != "" {
		node = t.store.Node(collectorID)
	}
	history := t.store.MetricHistory(collectorID, time.Now().Add(-window), limit)
	summary := fmt.Sprintf("collector=%s history_samples=%d fleet_nodes=%d", collectorID, len(history), len(fleet))
	return workflowToolResult{
		Summary: summary,
		Data: metricsToolData{
			CollectorID: collectorID,
			Node:        node,
			History:     history,
			Fleet:       fleet,
		},
	}, nil
}

type logsToolData struct {
	Errors        uint64
	Warnings      uint64
	Total         uint64
	Snippets      []string
	Timeline      []logindex.TimelineBucket
	RecentDeploys []string
	SecurityHints []string
}

type logsQueryTool struct {
	index *logindex.Index
	store *ingest.MemoryStore
}

func (t *logsQueryTool) Name() ToolName  { return ToolLogs }
func (t *logsQueryTool) Version() string { return workflowToolVersion }
func (t *logsQueryTool) Description() string {
	return "Log search and timeline correlation against indexed or fingerprint data."
}
func (t *logsQueryTool) Deterministic() bool { return true }
func (t *logsQueryTool) Unsafe() bool        { return false }

func (t *logsQueryTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	window := req.Window
	if window <= 0 {
		window = 30 * time.Minute
	}
	collectorID := strings.TrimSpace(req.CollectorID)
	queryText := strings.TrimSpace(req.Query["text"])
	if queryText == "" {
		queryText = strings.TrimSpace(req.Query["query"])
	}

	if t != nil && t.index != nil {
		search := t.index.Search(logindex.SearchQuery{
			CollectorID: collectorID,
			Text:        queryText,
			Since:       time.Now().Add(-window),
			Until:       time.Now().UTC(),
			Limit:       maxInt(req.Limit, 120),
		})

		data := logsToolData{
			Snippets: make([]string, 0, minInt(10, len(search.Entries))),
			Timeline: append([]logindex.TimelineBucket(nil), search.Timeline...),
		}
		for _, entry := range search.Entries {
			data.Total += entry.Count
			lvl := strings.ToLower(strings.TrimSpace(entry.Level))
			switch lvl {
			case "error", "fatal", "critical":
				data.Errors += entry.Count
			case "warn", "warning":
				data.Warnings += entry.Count
			default:
				if inferLogSeverity(entry.Message) == "error" {
					data.Errors += entry.Count
				} else if inferLogSeverity(entry.Message) == "warn" {
					data.Warnings += entry.Count
				}
			}
			if len(data.Snippets) < 10 {
				data.Snippets = append(data.Snippets, truncateString(strings.TrimSpace(entry.Message), 180))
			}
			low := strings.ToLower(entry.Message)
			if looksLikeDeployChange(low) {
				data.RecentDeploys = append(data.RecentDeploys, truncateString(strings.TrimSpace(entry.Message), 180))
			}
			if looksSecurityRelated(low) {
				data.SecurityHints = append(data.SecurityHints, truncateString(strings.TrimSpace(entry.Message), 180))
			}
		}

		data.RecentDeploys = dedupeStrings(data.RecentDeploys)
		if len(data.RecentDeploys) > 6 {
			data.RecentDeploys = data.RecentDeploys[:6]
		}
		data.SecurityHints = dedupeStrings(data.SecurityHints)
		if len(data.SecurityHints) > 6 {
			data.SecurityHints = data.SecurityHints[:6]
		}
		return workflowToolResult{
			Summary: fmt.Sprintf("log_index entries=%d errors=%d warnings=%d", search.Returned, data.Errors, data.Warnings),
			Data:    data,
		}, nil
	}

	// Fallback: use latest ingested log fingerprints.
	data := logsToolData{Snippets: []string{}}
	if t != nil && t.store != nil {
		node := t.store.Node(collectorID)
		if node != nil {
			for _, fingerprint := range node.Logs {
				if fingerprint == nil {
					continue
				}
				line := strings.TrimSpace(fingerprint.Example)
				if queryText != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(queryText)) {
					continue
				}
				count := fingerprint.Count
				if count == 0 {
					count = 1
				}
				data.Total += count
				sev := inferLogSeverity(line)
				if sev == "error" {
					data.Errors += count
				} else if sev == "warn" {
					data.Warnings += count
				}
				if len(data.Snippets) < 10 {
					data.Snippets = append(data.Snippets, truncateString(line, 180))
				}
				low := strings.ToLower(line)
				if looksLikeDeployChange(low) {
					data.RecentDeploys = append(data.RecentDeploys, truncateString(line, 180))
				}
				if looksSecurityRelated(low) {
					data.SecurityHints = append(data.SecurityHints, truncateString(line, 180))
				}
			}
		}
	}
	data.RecentDeploys = dedupeStrings(data.RecentDeploys)
	data.SecurityHints = dedupeStrings(data.SecurityHints)
	return workflowToolResult{
		Summary: fmt.Sprintf("fingerprint logs errors=%d warnings=%d", data.Errors, data.Warnings),
		Data:    data,
	}, nil
}

type topologyToolData struct {
	Snapshot TopologySnapshot
}

type topologyQueryTool struct {
	provider TopologyProvider
	store    *ingest.MemoryStore
}

func (t *topologyQueryTool) Name() ToolName  { return ToolTopology }
func (t *topologyQueryTool) Version() string { return workflowToolVersion }
func (t *topologyQueryTool) Description() string {
	return "Topology mapping for node/pod/service/process relationships."
}
func (t *topologyQueryTool) Deterministic() bool { return true }
func (t *topologyQueryTool) Unsafe() bool        { return false }

func (t *topologyQueryTool) Run(ctx context.Context, req workflowToolRequest) (workflowToolResult, error) {
	if t != nil && t.provider != nil {
		snapshot := t.provider.Snapshot(ctx)
		if snapshot.GeneratedAt.IsZero() {
			snapshot.GeneratedAt = time.Now().UTC()
		}
		if snapshot.Summary == "" {
			snapshot.Summary = fmt.Sprintf("topology nodes=%d edges=%d", len(snapshot.Nodes), len(snapshot.Edges))
		}
		return workflowToolResult{
			Summary: snapshot.Summary,
			Data: topologyToolData{
				Snapshot: snapshot,
			},
		}, nil
	}

	nodes := []TopologyNode{}
	edges := []TopologyEdge{}
	if t != nil && t.store != nil {
		fleet := t.store.Snapshot()
		for _, node := range fleet {
			if node == nil {
				continue
			}
			nodeID := firstNonEmpty(node.CollectorID, node.Hostname)
			nodes = append(nodes, TopologyNode{
				ID:     nodeID,
				Name:   firstNonEmpty(node.Hostname, node.CollectorID),
				Type:   "node",
				Status: "observed",
			})
			for _, process := range topProcessResources(node, 4) {
				procID := fmt.Sprintf("%s:%s", nodeID, process.Key)
				nodes = append(nodes, TopologyNode{
					ID:     procID,
					Name:   processDisplayName(process),
					Type:   "process",
					Status: "observed",
					Score:  processPressureScore(process),
				})
				edges = append(edges, TopologyEdge{Source: nodeID, Target: procID, Kind: "runs"})
			}
		}
	}

	snapshot := TopologySnapshot{
		GeneratedAt: time.Now().UTC(),
		Nodes:       nodes,
		Edges:       edges,
		Summary:     fmt.Sprintf("derived topology nodes=%d edges=%d", len(nodes), len(edges)),
		Source:      "ingest-derived",
	}
	return workflowToolResult{Summary: snapshot.Summary, Data: topologyToolData{Snapshot: snapshot}}, nil
}

type securityToolData struct {
	Score                    float64
	Findings                 []string
	SuspiciousPortCandidates []string
	WeakPermissionHints      []string
	CriticalFindings         int
	HighFindings             int
	MediumFindings           int
	LowFindings              int
	Categories               []string
	FindingIDs               []string
}

type ebpfToolData struct {
	CollectorID            string
	RuntimeEvents          []ingest.RuntimeSecurityEvent
	RuntimeEventSummaries  []string
	EvidenceIDs            []string
	ProcessGraph           ingest.ProcessGraphSnapshot
	NetworkBehaviorSummary ingest.NetworkBehaviorSummary
	SyscallStatistics      map[string]uint64
	BehaviorScore          float64
	EventRate              float64
}

type securityGraphNode struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type securityGraphEdge struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Relation   string `json:"relation"`
	EvidenceID string `json:"evidence_id,omitempty"`
}

type securityGraphToolData struct {
	CollectorID string
	Nodes       []securityGraphNode
	Edges       []securityGraphEdge
	Summary     string
}

type processLineageToolData struct {
	CollectorID string
	Nodes       []ingest.ProcessGraphNode
	Edges       []ingest.ProcessGraphEdge
	Paths       []string
	Summary     string
}

type securityCheckTool struct {
	store *ingest.MemoryStore
	index *logindex.Index
}

func (t *securityCheckTool) Name() ToolName  { return ToolSecurity }
func (t *securityCheckTool) Version() string { return workflowToolVersion }
func (t *securityCheckTool) Description() string {
	return "Security/misconfiguration checks from metrics and logs."
}
func (t *securityCheckTool) Deterministic() bool { return true }
func (t *securityCheckTool) Unsafe() bool        { return false }

func (t *securityCheckTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	collectorID := strings.TrimSpace(req.CollectorID)
	data := securityToolData{
		Findings:                 []string{},
		SuspiciousPortCandidates: []string{},
		WeakPermissionHints:      []string{},
		Categories:               []string{},
		FindingIDs:               []string{},
	}

	if t != nil && t.store != nil {
		window := maxDuration(req.Window, 30*time.Minute)
		evaluator := securityaudit.NewEvaluator(t.store, t.index)
		findings := evaluator.Findings(securityaudit.Options{
			CollectorID: collectorID,
			Window:      window,
			Limit:       maxInt(req.Limit, 48),
		})

		score := 0.0
		for _, finding := range findings {
			data.FindingIDs = append(data.FindingIDs, finding.ID)
			data.Categories = append(data.Categories, finding.Category)
			data.Findings = append(data.Findings, fmt.Sprintf("%s: %s", strings.ToUpper(string(finding.Severity)), finding.Summary))
			switch finding.Severity {
			case securityaudit.SeverityCritical:
				data.CriticalFindings++
				score += finding.Score * 1.4
			case securityaudit.SeverityHigh:
				data.HighFindings++
				score += finding.Score * 1.1
			case securityaudit.SeverityMedium:
				data.MediumFindings++
				score += finding.Score * 0.75
			default:
				data.LowFindings++
				score += finding.Score * 0.4
			}

			category := strings.ToLower(strings.TrimSpace(finding.Category))
			if strings.Contains(category, "network") {
				data.SuspiciousPortCandidates = append(data.SuspiciousPortCandidates, finding.Summary)
			}
			if strings.Contains(category, "filesystem") || strings.Contains(category, "permission") {
				data.WeakPermissionHints = append(data.WeakPermissionHints, finding.Summary)
			}
			data.SuspiciousPortCandidates = append(data.SuspiciousPortCandidates, finding.Evidence...)
			data.WeakPermissionHints = append(data.WeakPermissionHints, finding.Evidence...)
		}

		data.Categories = dedupeStrings(data.Categories)
		data.FindingIDs = dedupeStrings(data.FindingIDs)
		data.Findings = dedupeStrings(data.Findings)
		if len(findings) > 0 {
			data.Score = clamp01(score / float64(len(findings)))
		}
	}

	data.SuspiciousPortCandidates = dedupeStrings(data.SuspiciousPortCandidates)
	if len(data.SuspiciousPortCandidates) > 8 {
		data.SuspiciousPortCandidates = data.SuspiciousPortCandidates[:8]
	}
	data.WeakPermissionHints = dedupeStrings(data.WeakPermissionHints)
	if len(data.WeakPermissionHints) > 8 {
		data.WeakPermissionHints = data.WeakPermissionHints[:8]
	}

	return workflowToolResult{
		Summary: fmt.Sprintf("security score=%.2f findings=%d", data.Score, len(data.Findings)),
		Data:    data,
	}, nil
}

type ebpfQueryTool struct {
	store *ingest.MemoryStore
}

func (t *ebpfQueryTool) Name() ToolName  { return ToolEBPFQuery }
func (t *ebpfQueryTool) Version() string { return workflowToolVersion }
func (t *ebpfQueryTool) Description() string {
	return "eBPF runtime events, syscall statistics, process graph, and network behavior summary."
}
func (t *ebpfQueryTool) Deterministic() bool { return true }
func (t *ebpfQueryTool) Unsafe() bool        { return false }

func (t *ebpfQueryTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := ebpfToolData{
		RuntimeEvents:          []ingest.RuntimeSecurityEvent{},
		RuntimeEventSummaries:  []string{},
		EvidenceIDs:            []string{},
		SyscallStatistics:      map[string]uint64{},
		NetworkBehaviorSummary: ingest.NetworkBehaviorSummary{},
	}
	if t == nil || t.store == nil {
		return workflowToolResult{
			Summary: "ebpf runtime unavailable",
			Data:    data,
		}, nil
	}

	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		snapshot := t.store.Snapshot()
		if len(snapshot) > 0 {
			sort.Slice(snapshot, func(i, j int) bool {
				return snapshot[i].UpdatedAt.After(snapshot[j].UpdatedAt)
			})
			node = snapshot[0]
		}
	}
	if node == nil {
		return workflowToolResult{
			Summary: "ebpf runtime unavailable",
			Data:    data,
		}, nil
	}

	data.CollectorID = node.CollectorID
	data.ProcessGraph = node.ProcessGraphSnapshot
	data.NetworkBehaviorSummary = node.NetworkBehavior
	data.SyscallStatistics = cloneUint64Map(node.SyscallStatistics)
	data.RuntimeEvents = append(data.RuntimeEvents, node.RuntimeSecurityEvents...)
	sort.Slice(data.RuntimeEvents, func(i, j int) bool {
		return data.RuntimeEvents[i].Timestamp.After(data.RuntimeEvents[j].Timestamp)
	})
	if len(data.RuntimeEvents) > 80 {
		data.RuntimeEvents = data.RuntimeEvents[:80]
	}
	for _, event := range data.RuntimeEvents {
		data.EvidenceIDs = append(data.EvidenceIDs, event.EvidenceID)
		data.RuntimeEventSummaries = append(data.RuntimeEventSummaries,
			fmt.Sprintf("%s %s severity=%s pid=%s", event.Category, event.Type, event.Severity, event.PID))
	}
	data.EvidenceIDs = dedupeStrings(data.EvidenceIDs)
	data.RuntimeEventSummaries = dedupeStrings(data.RuntimeEventSummaries)
	if len(data.RuntimeEventSummaries) > 12 {
		data.RuntimeEventSummaries = data.RuntimeEventSummaries[:12]
	}

	totalEvents := len(data.RuntimeEvents)
	if totalEvents > 0 {
		behaviorScore := 0.0
		for _, event := range data.RuntimeEvents {
			switch strings.ToLower(strings.TrimSpace(event.Severity)) {
			case "critical":
				behaviorScore += 0.20
			case "high":
				behaviorScore += 0.12
			case "medium":
				behaviorScore += 0.06
			default:
				behaviorScore += 0.03
			}
		}
		data.BehaviorScore = clamp01(behaviorScore / float64(totalEvents))
		data.EventRate = float64(totalEvents) / maxFloatValue(req.Window.Minutes(), 1.0)
	}

	return workflowToolResult{
		Summary: fmt.Sprintf("ebpf events=%d syscalls=%d behavior_score=%.2f", len(data.RuntimeEvents), len(data.SyscallStatistics), data.BehaviorScore),
		Data:    data,
	}, nil
}

type securityGraphTool struct {
	store *ingest.MemoryStore
}

func (t *securityGraphTool) Name() ToolName  { return ToolSecurityGraph }
func (t *securityGraphTool) Version() string { return workflowToolVersion }
func (t *securityGraphTool) Description() string {
	return "Builds a deterministic security evidence graph from eBPF runtime events."
}
func (t *securityGraphTool) Deterministic() bool { return true }
func (t *securityGraphTool) Unsafe() bool        { return false }

func (t *securityGraphTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := securityGraphToolData{
		Nodes:   []securityGraphNode{},
		Edges:   []securityGraphEdge{},
		Summary: "security graph unavailable",
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}

	data.CollectorID = node.CollectorID
	nodeSet := map[string]securityGraphNode{}
	for _, event := range node.RuntimeSecurityEvents {
		pidNodeID := "process:" + firstNonEmpty(event.PID, "unknown")
		if _, ok := nodeSet[pidNodeID]; !ok {
			nodeSet[pidNodeID] = securityGraphNode{ID: pidNodeID, Kind: "process", Label: firstNonEmpty(event.PID, "unknown")}
		}
		if event.Port > 0 {
			portNodeID := fmt.Sprintf("port:%d", event.Port)
			if _, ok := nodeSet[portNodeID]; !ok {
				nodeSet[portNodeID] = securityGraphNode{ID: portNodeID, Kind: "port", Label: strconv.Itoa(event.Port)}
			}
			data.Edges = append(data.Edges, securityGraphEdge{
				Source:     pidNodeID,
				Target:     portNodeID,
				Relation:   "uses_port",
				EvidenceID: event.EvidenceID,
			})
		}
		if strings.TrimSpace(event.RemoteIP) != "" {
			ipNodeID := "ip:" + event.RemoteIP
			if _, ok := nodeSet[ipNodeID]; !ok {
				nodeSet[ipNodeID] = securityGraphNode{ID: ipNodeID, Kind: "remote_ip", Label: event.RemoteIP}
			}
			data.Edges = append(data.Edges, securityGraphEdge{
				Source:     pidNodeID,
				Target:     ipNodeID,
				Relation:   "connects_to",
				EvidenceID: event.EvidenceID,
			})
		}
		if strings.TrimSpace(event.Path) != "" {
			pathNodeID := "path:" + event.Path
			if _, ok := nodeSet[pathNodeID]; !ok {
				nodeSet[pathNodeID] = securityGraphNode{ID: pathNodeID, Kind: "path", Label: event.Path}
			}
			data.Edges = append(data.Edges, securityGraphEdge{
				Source:     pidNodeID,
				Target:     pathNodeID,
				Relation:   "touches_path",
				EvidenceID: event.EvidenceID,
			})
		}
	}
	for _, item := range nodeSet {
		data.Nodes = append(data.Nodes, item)
	}
	sort.Slice(data.Nodes, func(i, j int) bool {
		return data.Nodes[i].ID < data.Nodes[j].ID
	})
	sort.Slice(data.Edges, func(i, j int) bool {
		if data.Edges[i].Source == data.Edges[j].Source {
			return data.Edges[i].Target < data.Edges[j].Target
		}
		return data.Edges[i].Source < data.Edges[j].Source
	})
	data.Summary = fmt.Sprintf("security graph nodes=%d edges=%d", len(data.Nodes), len(data.Edges))
	return workflowToolResult{
		Summary: data.Summary,
		Data:    data,
	}, nil
}

type processLineageTool struct {
	store *ingest.MemoryStore
}

func (t *processLineageTool) Name() ToolName  { return ToolProcessLineage }
func (t *processLineageTool) Version() string { return workflowToolVersion }
func (t *processLineageTool) Description() string {
	return "Returns deterministic process lineage and top process graph paths."
}
func (t *processLineageTool) Deterministic() bool { return true }
func (t *processLineageTool) Unsafe() bool        { return false }

func (t *processLineageTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := processLineageToolData{
		Nodes:   []ingest.ProcessGraphNode{},
		Edges:   []ingest.ProcessGraphEdge{},
		Paths:   []string{},
		Summary: "process lineage unavailable",
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}

	data.CollectorID = node.CollectorID
	data.Nodes = append(data.Nodes, node.ProcessGraphSnapshot.Nodes...)
	data.Edges = append(data.Edges, node.ProcessGraphSnapshot.Edges...)
	for _, edge := range data.Edges {
		data.Paths = append(data.Paths, fmt.Sprintf("%s -> %s (%s)", edge.Source, edge.Target, edge.Kind))
	}
	data.Paths = dedupeStrings(data.Paths)
	if len(data.Paths) > 16 {
		data.Paths = data.Paths[:16]
	}
	data.Summary = fmt.Sprintf("process lineage nodes=%d edges=%d", len(data.Nodes), len(data.Edges))
	return workflowToolResult{
		Summary: data.Summary,
		Data:    data,
	}, nil
}

type profilingToolData struct {
	Command          string
	Mode             string
	RequiresApproval bool
	DryRun           bool
	Executed         bool
	Message          string
}

type profilingTriggerTool struct {
	cfg WorkflowConfig
}

func (t *profilingTriggerTool) Name() ToolName  { return ToolProfiling }
func (t *profilingTriggerTool) Version() string { return workflowToolVersion }
func (t *profilingTriggerTool) Description() string {
	return "Bounded profiling trigger contract (dry-run by default)."
}
func (t *profilingTriggerTool) Deterministic() bool { return true }
func (t *profilingTriggerTool) Unsafe() bool        { return true }

func (t *profilingTriggerTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	command := strings.TrimSpace(t.cfg.ProfilingCommand)
	if command == "" {
		command = "perf record -F 99 -a -g -- sleep 30"
	}

	mode := "planned"
	message := "profiling trigger prepared"
	if req.DryRun || t.cfg.DryRun {
		mode = "dry_run"
		message = "profiling trigger prepared in dry-run mode"
	} else if !t.cfg.AllowProfilingExec {
		mode = "guarded"
		message = "profiling execution blocked by policy"
	}

	return workflowToolResult{
		Summary: message,
		Data: profilingToolData{
			Command:          command,
			Mode:             mode,
			RequiresApproval: t.cfg.RequireApproval,
			DryRun:           req.DryRun || t.cfg.DryRun,
			Executed:         false,
			Message:          message,
		},
	}, nil
}

type remediationToolData struct {
	Action           string
	Summary          string
	DryRun           bool
	RequiresApproval bool
	Reversible       bool
	RollbackPlan     string
	Mode             string
}

type remediationActionTool struct {
	cfg WorkflowConfig
}

func (t *remediationActionTool) Name() ToolName  { return ToolRemediation }
func (t *remediationActionTool) Version() string { return workflowToolVersion }
func (t *remediationActionTool) Description() string {
	return "Guarded remediation planner (dry-run, approval, rollback required)."
}
func (t *remediationActionTool) Deterministic() bool { return true }
func (t *remediationActionTool) Unsafe() bool        { return true }

func (t *remediationActionTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	action := strings.TrimSpace(req.Query["action"])
	if action == "" {
		action = "validate_and_hold"
	}
	scope := strings.TrimSpace(req.Query["scope"])
	if scope == "" {
		scope = firstNonEmpty(req.CollectorID, "fleet")
	}
	mode := "dry_run"
	if !req.DryRun && !t.cfg.DryRun {
		if t.cfg.AllowRemediationExec {
			mode = "approved_execution_required"
		} else {
			mode = "blocked_by_policy"
		}
	}
	summary := fmt.Sprintf("remediation plan for %s on %s (%s)", action, scope, mode)
	data := remediationToolData{
		Action:           action,
		Summary:          summary,
		DryRun:           req.DryRun || t.cfg.DryRun,
		RequiresApproval: true,
		Reversible:       true,
		RollbackPlan:     "capture pre-change baseline -> execute single scoped change -> revert workload/config to previous revision",
		Mode:             mode,
	}
	return workflowToolResult{Summary: summary, Data: data}, nil
}

func pickCollectorFromSnapshot(nodes []*ingest.NodeSnapshot) string {
	if len(nodes) == 0 {
		return ""
	}
	sort.Slice(nodes, func(i, j int) bool {
		left := nodes[i]
		right := nodes[j]
		if left == nil || right == nil {
			return left != nil
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})
	if nodes[0] == nil {
		return ""
	}
	return nodes[0].CollectorID
}

func topProcessResources(node *ingest.NodeSnapshot, limit int) []*ingest.ProcessResourceSample {
	if node == nil || len(node.ProcessResources) == 0 {
		return nil
	}
	rows := make([]*ingest.ProcessResourceSample, 0, len(node.ProcessResources))
	for _, item := range node.ProcessResources {
		if item == nil {
			continue
		}
		rows = append(rows, item)
	}
	sort.Slice(rows, func(i, j int) bool {
		return processPressureScore(rows[i]) > processPressureScore(rows[j])
	})
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func processPressureScore(item *ingest.ProcessResourceSample) float64 {
	if item == nil {
		return 0
	}
	score := 0.0
	for category, total := range item.CategoryTotals {
		switch category {
		case "cpu":
			score += total * 0.004
		case "memory":
			score += total / (1024 * 1024 * 1024) * 0.08
		case "disk", "disk_io":
			score += total / (1024 * 1024) * 0.002
		case "network":
			score += total / (1024 * 1024) * 0.002
		case "gpu":
			score += total * 0.004
		default:
			score += math.Abs(total) * 0.0005
		}
	}
	score += float64(item.LogErrors) * 0.2
	score += float64(item.LogWarnings) * 0.08
	return score
}

func processDisplayName(item *ingest.ProcessResourceSample) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(item.Name, item.Key, item.PID, "unknown-process")
}

func inferLogSeverity(line string) string {
	low := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.Contains(low, "error"), strings.Contains(low, "fatal"), strings.Contains(low, "panic"), strings.Contains(low, "critical"):
		return "error"
	case strings.Contains(low, "warn"), strings.Contains(low, "deprecated"):
		return "warn"
	default:
		return "info"
	}
}

func looksLikeDeployChange(line string) bool {
	return strings.Contains(line, "deploy") || strings.Contains(line, "rollout") || strings.Contains(line, "release") || strings.Contains(line, "image:") || strings.Contains(line, "version")
}

func looksSecurityRelated(line string) bool {
	return strings.Contains(line, "permission") || strings.Contains(line, "unauthorized") || strings.Contains(line, "forbidden") || strings.Contains(line, "privileged") || strings.Contains(line, "world-writable") || strings.Contains(line, "open port")
}

func metricValue(metrics map[string]float64, key string) float64 {
	if metrics == nil {
		return 0
	}
	if v, ok := metrics[key]; ok {
		return v
	}
	return 0
}

func maxDuration(values ...time.Duration) time.Duration {
	best := time.Duration(0)
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func parseBoolFromString(raw string, fallback bool) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func parseIntFromString(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneUint64Map(in map[string]uint64) map[string]uint64 {
	if len(in) == 0 {
		return map[string]uint64{}
	}
	out := make(map[string]uint64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func maxFloatValue(value, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	return value
}

func truncateString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit])
}
