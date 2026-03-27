package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/changeintel"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/securityaudit"
	"go.uber.org/zap"
)

const workflowToolVersion = "v0.7.0"

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
	WorkflowID     string
	Workflow       string
	Stage          string
	Actor          string
	StepID         string
	CollectorID    string
	Window         time.Duration
	Limit          int
	Query          map[string]string
	DryRun         bool
	IdempotencyKey string
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
	logger       *zap.Logger
	tools        map[ToolName]workflowTool
	descriptors  []WorkflowToolDescriptor
	policy       *PolicyEngine
	orchestrator *DurableOrchestrator
	cfg          WorkflowConfig
	callCache    map[string]workflowToolResult
	cacheMu      sync.RWMutex
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
		descriptors = append(descriptors, describeWorkflowTool(tool))
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].Name < descriptors[j].Name
	})
	return &workflowToolManager{
		logger:      logger.With(zap.String("component", "agent_workflow_tools")),
		tools:       registry,
		descriptors: descriptors,
		policy:      NewPolicyEngine(logger),
		callCache:   make(map[string]workflowToolResult),
	}
}

func describeWorkflowTool(tool workflowTool) WorkflowToolDescriptor {
	if tool == nil {
		return WorkflowToolDescriptor{}
	}
	desc := WorkflowToolDescriptor{
		Name:          tool.Name(),
		Version:       tool.Version(),
		Description:   tool.Description(),
		Deterministic: tool.Deterministic(),
		Unsafe:        tool.Unsafe(),
	}
	switch tool.Name() {
	case ToolMetrics:
		desc.Purpose = "Query node, process, kernel, and GPU metrics for the selected collector and history window."
		desc.InputSchema = `{"collector_id":"string","window":"duration","limit":"int","include":"string"}`
		desc.OutputSchema = `{"collector_id":"string","node":"NodeSnapshot","history":"[]MetricHistorySample","fleet":"[]NodeSnapshot"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolLogs:
		desc.Purpose = "Search indexed logs and fingerprints, compress bursts into snippets, and correlate with the incident window."
		desc.InputSchema = `{"collector_id":"string","query":"string","window":"duration","limit":"int"}`
		desc.OutputSchema = `{"errors":"uint64","warnings":"uint64","snippets":"[]string","timeline":"[]TimelineBucket","recent_deploys":"[]string","security_hints":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolChangeQuery:
		desc.Purpose = "Correlate recent deploys, config changes, runtime upgrades, driver changes, and feature flags with the incident window."
		desc.InputSchema = `{"collector_id":"string","window":"duration","scope":"string","incident_summary":"string"}`
		desc.OutputSchema = `{"events":"[]CorrelatedChange","summary":"string","categories":"[]string","strongest":"CorrelatedChange"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolDeploymentHistory:
		desc.Purpose = "Filter rollout and release history around the incident window and summarize the strongest deployment-like changes."
		desc.InputSchema = `{"collector_id":"string","window":"duration","scope":"string","incident_summary":"string"}`
		desc.OutputSchema = `{"events":"[]CorrelatedChange","summary":"string","categories":"[]string","strongest":"CorrelatedChange"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolConfigState:
		desc.Purpose = "Summarize runtime mode, revision labels, and config-related changes for the target workload."
		desc.InputSchema = `{"collector_id":"string","category":"string","scope":"string"}`
		desc.OutputSchema = `{"collector_id":"string","labels":"map[string]string","runtime_mode":"string","changes":"[]CorrelatedChange","summary":"string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolTopology:
		desc.Purpose = "Resolve topology proximity and blast radius across node, process, pod, and service relationships."
		desc.InputSchema = `{"collector_id":"string"}`
		desc.OutputSchema = `{"snapshot":"TopologySnapshot"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolSecurity:
		desc.Purpose = "Collect normalized security and misconfiguration findings from collector-side telemetry and logs."
		desc.InputSchema = `{"collector_id":"string","window":"duration","limit":"int"}`
		desc.OutputSchema = `{"score":"float64","findings":"[]string","structured_findings":"[]Finding"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolKnowledge, ToolRAGQuery:
		desc.Purpose = "Retrieve runbooks, architecture notes, and prior incident evidence from the local RAG knowledge base."
		desc.InputSchema = `{"query":"string","top_k":"int","intent":"string","knowledge_types":"[]string","case_types":"[]string"}`
		desc.OutputSchema = `{"hits":"[]RetrievedDocumentEvidence","summary":"string","confidence":"float64","evidence_ids":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolHistoricalIncident:
		desc.Purpose = "Retrieve prior incidents, known RCA patterns, and historical analogies that match the active signal mix."
		desc.InputSchema = `{"query":"string","top_k":"int","intent":"historical_incident"}`
		desc.OutputSchema = `{"hits":"[]RetrievedDocumentEvidence","summary":"string","confidence":"float64","evidence_ids":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolRunbookRetrieval:
		desc.Purpose = "Retrieve concrete runbook steps, troubleshooting guidance, and remediation procedures for the current issue."
		desc.InputSchema = `{"query":"string","top_k":"int","intent":"runbook"}`
		desc.OutputSchema = `{"hits":"[]RetrievedDocumentEvidence","summary":"string","confidence":"float64","evidence_ids":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolSimilarCase:
		desc.Purpose = "Retrieve similar cases and weak-signal escalation patterns to support potential issue and joint-risk interpretation."
		desc.InputSchema = `{"query":"string","top_k":"int","intent":"joint_risk"}`
		desc.OutputSchema = `{"hits":"[]RetrievedDocumentEvidence","summary":"string","confidence":"float64","evidence_ids":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolEBPFQuery:
		desc.Purpose = "Query kernel-level runtime behavior, syscall activity, process graphs, and connection patterns from eBPF-derived telemetry."
		desc.InputSchema = `{"collector_id":"string","window":"duration","mode":"string"}`
		desc.OutputSchema = `{"runtime_events":"[]RuntimeSecurityEvent","syscall_statistics":"map[string]uint64","process_graph":"ProcessGraphSnapshot","network_behavior_summary":"NetworkBehaviorSummary"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolMemoryPressure:
		desc.Purpose = "Summarize node and workload memory pressure, top RSS offenders, and OOM or eviction hints."
		desc.InputSchema = `{"collector_id":"string","signals":"string"}`
		desc.OutputSchema = `{"collector_id":"string","summary":"string","pressure_signals":"[]string","top_processes":"[]string","oom_hints":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolConnectivityCheck:
		desc.Purpose = "Check retransmits, softnet drops, and runtime network behavior to validate connectivity hypotheses."
		desc.InputSchema = `{"collector_id":"string","signals":"string"}`
		desc.OutputSchema = `{"collector_id":"string","summary":"string","healthy":"bool","retransmit_ratio":"float64","softnet_drops":"float64"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolDNSCheck:
		desc.Purpose = "Search logs and runtime events for DNS resolution failures, NXDOMAIN patterns, and resolver churn."
		desc.InputSchema = `{"collector_id":"string","signals":"string"}`
		desc.OutputSchema = `{"collector_id":"string","summary":"string","healthy":"bool","hints":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolServiceHealth:
		desc.Purpose = "Summarize service latency, error rate, restart, and request health around the active workload."
		desc.InputSchema = `{"collector_id":"string","scope":"string"}`
		desc.OutputSchema = `{"collector_id":"string","summary":"string","healthy":"bool","latency_ms":"float64","error_rate":"float64"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolGPU:
		desc.Purpose = "Summarize GPU utilization, memory pressure, PCIe throughput, and process attribution for the selected collector."
		desc.InputSchema = `{"collector_id":"string","window":"duration"}`
		desc.OutputSchema = `{"summary":"string","metrics":"map[string]float64","top_processes":"[]string","bottleneck":"string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolKubernetesResource:
		desc.Purpose = "Summarize pod, workload, service, and process identity for the selected collector."
		desc.InputSchema = `{"collector_id":"string","signals":"string"}`
		desc.OutputSchema = `{"collector_id":"string","summary":"string","namespace":"string","service":"string","workload":"string","pod_uid":"string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolContainerRevision:
		desc.Purpose = "Summarize image, revision, pod UID, and runtime mode to validate rollout and container hypotheses."
		desc.InputSchema = `{"collector_id":"string","signals":"string"}`
		desc.OutputSchema = `{"collector_id":"string","summary":"string","service":"string","revision":"string","image":"string","pod_uid":"string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolStorageHealth:
		desc.Purpose = "Summarize disk and filesystem pressure, queue depth, latency, and inode or capacity stress."
		desc.InputSchema = `{"collector_id":"string","signals":"string"}`
		desc.OutputSchema = `{"collector_id":"string","summary":"string","pressure":"bool","hot_devices":"[]string","hot_filesystems":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolNetworkBlastRadius:
		desc.Purpose = "Estimate likely downstream scope for a network- or service-health incident using topology and runtime data."
		desc.InputSchema = `{"collector_id":"string","signals":"string"}`
		desc.OutputSchema = `{"collector_id":"string","summary":"string","scope":"[]string","downstream":"[]string","blast_radius":"int"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolActionOutcome:
		desc.Purpose = "Retrieve prior verified remediation outcomes from durable incident memory."
		desc.InputSchema = `{"query":"string","top_k":"int","intent":"string"}`
		desc.OutputSchema = `{"hits":"[]RetrievedDocumentEvidence","summary":"string","confidence":"float64","evidence_ids":"[]string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolSecurityGraph:
		desc.Purpose = "Build a process-to-port/IP/path graph from runtime events for security and RCA correlation."
		desc.InputSchema = `{"collector_id":"string"}`
		desc.OutputSchema = `{"nodes":"[]securityGraphNode","edges":"[]securityGraphEdge","summary":"string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolProcessLineage:
		desc.Purpose = "Reconstruct parent-child process chains and hot process paths for RCA and containment scoping."
		desc.InputSchema = `{"collector_id":"string"}`
		desc.OutputSchema = `{"nodes":"[]ProcessGraphNode","edges":"[]ProcessGraphEdge","paths":"[]string","summary":"string"}`
		desc.ReadOnly = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	case ToolProfiling:
		desc.Purpose = "Prepare a bounded profiling capture plan for additional runtime evidence."
		desc.InputSchema = `{"reason":"string","collector_id":"string"}`
		desc.OutputSchema = `{"command":"string","mode":"string","requires_approval":"bool","dry_run":"bool","message":"string"}`
		desc.ReadOnly = false
		desc.RequiresApproval = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = true
		desc.SideEffects = "temporary profiling overhead and artifact generation"
		desc.SafetyClass = "guarded_execution"
	case ToolRemediation:
		desc.Purpose = "Generate a scoped action plan with dry-run expectations, approval gate, and rollback requirements."
		desc.InputSchema = `{"action":"string","scope":"string"}`
		desc.OutputSchema = `{"action":"string","summary":"string","mode":"string","rollback_plan":"string","requires_approval":"bool"}`
		desc.ReadOnly = false
		desc.RequiresApproval = true
		desc.SupportsDryRun = true
		desc.SupportsRollback = true
		desc.SideEffects = "proposes impactful actions; execution remains blocked behind approval"
		desc.SafetyClass = "approval_gated"
	default:
		desc.Purpose = tool.Description()
		desc.ReadOnly = !tool.Unsafe()
		desc.SupportsDryRun = true
		desc.SupportsRollback = false
		desc.SideEffects = "none"
		desc.SafetyClass = "read_only"
	}
	return desc
}

func (m *workflowToolManager) call(ctx context.Context, req workflowToolRequest, name ToolName) (WorkflowToolCall, workflowToolResult, error) {
	started := time.Now().UTC()
	call := WorkflowToolCall{
		ID:            newQueryID(),
		Tool:          name,
		ToolVersion:   "unknown",
		Stage:         req.Stage,
		Actor:         firstNonEmpty(req.Actor, "workflow-engine"),
		CollectorID:   req.CollectorID,
		Window:        req.Window.String(),
		Query:         cloneStringMap(req.Query),
		DryRun:        req.DryRun,
		PolicyVersion: firstNonEmpty(m.cfg.PolicyVersion, "workflow-policy/v1"),
		RiskTag:       riskTagForTool(name),
		Status:        "running",
		StartedAt:     started,
	}

	tool, ok := m.tools[name]
	if !ok {
		call.Status = "failed"
		call.ErrorMessage = fmt.Sprintf("tool %s not registered", name)
		call.CompletedAt = time.Now().UTC()
		return call, workflowToolResult{}, fmt.Errorf("tool %s not registered", name)
	}
	call.ToolVersion = tool.Version()
	autoKeyGenerated := false
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = stableWorkflowToolKey(name, req)
		autoKeyGenerated = true
	}
	call.IdempotencyKey = req.IdempotencyKey
	if err := validateWorkflowToolRequest(name, req); err != nil {
		call.Status = "failed"
		call.ErrorMessage = err.Error()
		call.CompletedAt = time.Now().UTC()
		return call, workflowToolResult{}, err
	}
	desc := describeWorkflowTool(tool)

	// 0. Policy check
	decision := m.policy.Evaluate(req, tool)
	call.Policy = decision
	call.ApprovalState = "not_required"
	if decision.Status == "blocked" {
		call.Status = "failed"
		call.ErrorMessage = fmt.Sprintf("policy block: %s", decision.Reason)
		call.CompletedAt = time.Now().UTC()
		return call, workflowToolResult{}, fmt.Errorf("policy block: %s", decision.Reason)
	}
	if decision.RequiresApproval && !req.DryRun {
		call.Status = "failed"
		call.ApprovalState = "pending"
		call.ErrorMessage = fmt.Sprintf("approval required: %s", decision.Reason)
		call.CompletedAt = time.Now().UTC()
		return call, workflowToolResult{}, errors.New(call.ErrorMessage)
	}
	if decision.DryRunRequired && !req.DryRun {
		req.DryRun = true
		call.DryRun = true
		call.ApprovalState = "dry_run_forced"
		if autoKeyGenerated {
			req.IdempotencyKey = stableWorkflowToolKey(name, req)
			call.IdempotencyKey = req.IdempotencyKey
		}
	}

	// 1. Idempotency check
	if req.IdempotencyKey != "" {
		m.cacheMu.RLock()
		cached, ok := m.callCache[req.IdempotencyKey]
		if ok {
			m.cacheMu.RUnlock()
			call.Status = "cached_success"
			call.Summary = cached.Summary + " (cached)"
			call.CompletedAt = time.Now().UTC()
			call.ResultKind = toolResultKind(name, cached.Data)
			call.ResultPayload = marshalToolResultPayload(cached.Data)
			return call, cached, nil
		}
		m.cacheMu.RUnlock()
		if !desc.ReadOnly && m.orchestrator != nil {
			if previous, lookupErr := m.orchestrator.FindToolCallByIdempotency(ctx, req.IdempotencyKey); lookupErr == nil && previous != nil {
				call.Status = "cached_success"
				call.Summary = firstNonEmpty(previous.Summary, "reused prior governed action outcome")
				call.CompletedAt = time.Now().UTC()
				call.ResultKind = previous.ResultKind
				call.ResultPayload = previous.ResultPayload
				call.ApprovalState = firstNonEmpty(previous.ApprovalState, call.ApprovalState)
				if previous.Policy.Status != "" {
					call.Policy = previous.Policy
				}
				if decoded, ok := decodeWorkflowToolPayload(name, previous.ResultPayload); ok {
					return call, workflowToolResult{Summary: call.Summary, Data: decoded}, nil
				}
				return call, workflowToolResult{Summary: call.Summary}, nil
			}
		}
	}

	// 2. Timeout enforcement
	timeout := toolTimeoutForName(name)
	maxAttempts := 1
	if desc.ReadOnly {
		maxAttempts += maxInt(m.cfg.ToolRetryCount, 0)
	}
	var (
		result workflowToolResult
		err    error
	)
	attemptsUsed := 0
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		result, err = tool.Run(runCtx, req)
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			call.TimedOut = true
		}
		cancel()
		attemptsUsed = attempt
		if err == nil || !shouldRetryWorkflowTool(desc, err, attempt, maxAttempts) {
			break
		}
	}
	call.Attempts = attemptsUsed
	call.CompletedAt = time.Now().UTC()
	if err != nil {
		call.Status = "failed"
		call.ErrorMessage = err.Error()
		return call, workflowToolResult{}, err
	}
	call.Status = "success"
	if decision.DryRunRequired {
		call.Status = "dry_run_success"
	}

	// 4. Cache for idempotency
	if req.IdempotencyKey != "" && err == nil {
		m.cacheMu.Lock()
		m.callCache[req.IdempotencyKey] = result
		m.cacheMu.Unlock()
	}

	call.Summary = strings.TrimSpace(result.Summary)
	call.ResultKind = toolResultKind(name, result.Data)
	call.ResultPayload = marshalToolResultPayload(result.Data)
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

func validateWorkflowToolRequest(name ToolName, req workflowToolRequest) error {
	if strings.TrimSpace(req.WorkflowID) == "" {
		return fmt.Errorf("workflow_id is required")
	}
	if strings.TrimSpace(req.Workflow) == "" {
		return fmt.Errorf("workflow type is required")
	}
	if strings.TrimSpace(req.Stage) == "" {
		return fmt.Errorf("workflow stage is required")
	}
	switch name {
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase:
		query := firstNonEmpty(req.Query["query"], req.Query["q"])
		if len(strings.TrimSpace(query)) < 8 {
			return fmt.Errorf("knowledge retrieval query is too short")
		}
	case ToolRemediation:
		if strings.TrimSpace(req.Query["action"]) == "" {
			return fmt.Errorf("remediation action is required")
		}
	}
	return nil
}

func stableWorkflowToolKey(name ToolName, req workflowToolRequest) string {
	payload := struct {
		Tool        ToolName          `json:"tool"`
		Workflow    string            `json:"workflow"`
		Stage       string            `json:"stage"`
		CollectorID string            `json:"collector_id"`
		Query       map[string]string `json:"query"`
		DryRun      bool              `json:"dry_run"`
	}{
		Tool:        name,
		Workflow:    req.Workflow,
		Stage:       req.Stage,
		CollectorID: req.CollectorID,
		Query:       cloneStringMap(req.Query),
		DryRun:      req.DryRun,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

func riskTagForTool(name ToolName) string {
	switch name {
	case ToolRemediation:
		return "execution"
	case ToolProfiling:
		return "diagnostic_execution"
	default:
		return "read_only"
	}
}

func toolTimeoutForName(name ToolName) time.Duration {
	switch name {
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase:
		return 30 * time.Second
	case ToolChangeQuery:
		return 20 * time.Second
	case ToolRemediation, ToolProfiling:
		return 20 * time.Second
	default:
		return 60 * time.Second
	}
}

func shouldRetryWorkflowTool(desc WorkflowToolDescriptor, err error, attempt, maxAttempts int) bool {
	if attempt >= maxAttempts || err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if desc.RequiresApproval {
		return false
	}
	return desc.ReadOnly
}

func toolResultKind(name ToolName, data any) string {
	if data == nil {
		return ""
	}
	return string(name)
}

func marshalToolResultPayload(data any) string {
	if data == nil {
		return ""
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeWorkflowToolPayload(name ToolName, payload string) (any, bool) {
	if strings.TrimSpace(payload) == "" {
		return nil, false
	}
	switch name {
	case ToolRemediation:
		var result ActionResult
		if err := json.Unmarshal([]byte(payload), &result); err == nil && result.Type != "" {
			return result, true
		}
		var plan remediationToolData
		if err := json.Unmarshal([]byte(payload), &plan); err == nil && (plan.Action != "" || plan.Mode != "") {
			return plan, true
		}
	case ToolProfiling:
		var result profilingToolData
		if err := json.Unmarshal([]byte(payload), &result); err == nil && result.Mode != "" {
			return result, true
		}
	}
	return nil, false
}

type metricsToolData struct {
	CollectorID string
	Node        *ingest.NodeSnapshot
	History     []ingest.MetricHistorySample
	Fleet       []*ingest.NodeSnapshot
}

type metricsQueryTool struct {
	store   *ingest.MemoryStore
	history ingest.MetricHistoryProvider
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
	historyProvider := t.history
	if historyProvider == nil {
		historyProvider = t.store
	}
	history := historyProvider.MetricHistory(collectorID, time.Now().Add(-window), limit)
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

type changeToolData struct {
	CollectorID string
	Events      []changeintel.CorrelatedChange
	Summary     string
	Categories  []string
	Strongest   *changeintel.CorrelatedChange
}

type configStateToolData struct {
	CollectorID string
	Labels      map[string]string
	RuntimeMode string
	Changes     []changeintel.CorrelatedChange
	Summary     string
}

type memoryPressureToolData struct {
	CollectorID      string
	Summary          string
	PressureSignals  []string
	TopProcesses     []string
	OOMHints         []string
	WorkingSetPct    float64
	AvailableBytes   float64
	MemoryLimitBytes float64
}

type connectivityCheckToolData struct {
	CollectorID        string
	Summary            string
	Healthy            bool
	RetransmitRatio    float64
	SoftnetDrops       float64
	UnexpectedOutbound uint64
}

type dnsCheckToolData struct {
	CollectorID string
	Summary     string
	Healthy     bool
	Hints       []string
}

type serviceHealthToolData struct {
	CollectorID string
	Summary     string
	Healthy     bool
	LatencyMS   float64
	ErrorRate   float64
	RestartLike bool
}

type kubernetesResourceToolData struct {
	CollectorID string
	Summary     string
	Namespace   string
	Service     string
	Workload    string
	PodUID      string
	Processes   []string
}

type containerRevisionToolData struct {
	CollectorID string
	Summary     string
	Service     string
	Revision    string
	Image       string
	PodUID      string
	RuntimeMode string
}

type storageHealthToolData struct {
	CollectorID     string
	Summary         string
	Pressure        bool
	HotDevices      []string
	HotFilesystems  []string
	AverageLatency  float64
	PeakUtilization float64
}

type networkBlastRadiusToolData struct {
	CollectorID string
	Summary     string
	Scope       []string
	Downstream  []string
	BlastRadius int
}

type knowledgeToolData struct {
	Tool        ToolName
	Intent      string
	Query       string
	Hits        []RetrievedDocumentEvidence
	Summary     string
	Confidence  float64
	EvidenceIDs []string
}

type gpuToolData struct {
	Summary      string
	Metrics      map[string]float64
	TopProcesses []string
	Bottleneck   string
	EvidenceIDs  []string
}

type knowledgeRetrievalTool struct {
	name           ToolName
	description    string
	intent         string
	knowledgeTypes []string
	caseTypes      []string
	kb             rag.KnowledgeBase
	memory         *WorkflowMemoryStore
}

func (t *knowledgeRetrievalTool) Name() ToolName {
	if t == nil || t.name == "" {
		return ToolKnowledge
	}
	return t.name
}
func (t *knowledgeRetrievalTool) Version() string { return workflowToolVersion }
func (t *knowledgeRetrievalTool) Description() string {
	if t == nil || strings.TrimSpace(t.description) == "" {
		return "Hybrid lexical plus embedding retrieval over the local dataset-backed knowledge base."
	}
	return t.description
}
func (t *knowledgeRetrievalTool) Deterministic() bool { return true }
func (t *knowledgeRetrievalTool) Unsafe() bool        { return false }

func (t *knowledgeRetrievalTool) Run(ctx context.Context, req workflowToolRequest) (workflowToolResult, error) {
	if t == nil {
		return workflowToolResult{Summary: "knowledge base unavailable", Data: knowledgeToolData{}}, nil
	}
	queryText := strings.TrimSpace(req.Query["query"])
	if queryText == "" {
		queryText = strings.TrimSpace(req.Query["q"])
	}
	if queryText == "" {
		queryText = strings.TrimSpace(strings.Join([]string{
			req.Workflow,
			req.Stage,
			req.CollectorID,
			req.Query["scope"],
			req.Query["signal"],
		}, " "))
	}
	topK := 4
	if raw := strings.TrimSpace(req.Query["top_k"]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			topK = parsed
		}
	}
	ragResult := rag.QueryResult{}
	if t.kb != nil {
		result, err := t.kb.Query(ctx, rag.QueryRequest{
			Query:          queryText,
			TopK:           topK,
			Intent:         t.intent,
			KnowledgeTypes: append([]string(nil), t.knowledgeTypes...),
			CaseTypes:      append([]string(nil), t.caseTypes...),
		})
		if err != nil {
			return workflowToolResult{}, err
		}
		ragResult = result
	}
	hits := make([]RetrievedDocumentEvidence, 0, len(ragResult.Hits)+topK)
	evidenceIDs := make([]string, 0, len(ragResult.Hits)+topK)
	for index, hit := range ragResult.Hits {
		evidenceID := fmt.Sprintf("ev-rag-%s-%02d", sanitizeID(firstNonEmpty(req.WorkflowID, req.Workflow, req.Stage, "kb")), index+1)
		hits = append(hits, RetrievedDocumentEvidence{
			EvidenceID:       evidenceID,
			DocID:            hit.DocID,
			ChunkID:          hit.ChunkID,
			Title:            hit.Title,
			SourcePath:       hit.SourcePath,
			SourceType:       hit.SourceType,
			KnowledgeType:    hit.KnowledgeType,
			CaseType:         hit.CaseType,
			Summary:          hit.Summary,
			Snippet:          hit.Snippet,
			Score:            hit.Score,
			Symptoms:         append([]string(nil), hit.Symptoms...),
			Evidence:         append([]string(nil), hit.Evidence...),
			LikelyCauses:     append([]string(nil), hit.LikelyCauses...),
			RemediationSteps: append([]string(nil), hit.RemediationSteps...),
			Commands:         append([]string(nil), hit.Commands...),
			Signals:          append([]string(nil), hit.Signals...),
			SectionType:      hit.SectionType,
			Tags:             append([]string(nil), hit.Tags...),
			Metadata:         cloneStringMap(hit.Metadata),
		})
		evidenceIDs = append(evidenceIDs, evidenceID)
	}
	memoryHits := []RetrievedDocumentEvidence(nil)
	if t.memory != nil {
		memoryHits = t.memory.Query(queryText, t.intent, req.CollectorID, topK)
		hits = mergeRetrievedDocumentEvidence(hits, memoryHits)
		for _, hit := range memoryHits {
			evidenceIDs = append(evidenceIDs, hit.EvidenceID)
		}
	}
	summaryParts := compactStrings(strings.TrimSpace(ragResult.Summary), memoryRetrievalSummary(memoryHits))
	summary := strings.Join(dedupeStrings(summaryParts), "; ")
	if summary == "" {
		summary = fmt.Sprintf("knowledge_hits=%d", len(hits))
	}
	return workflowToolResult{
		Summary: summary,
		Data: knowledgeToolData{
			Tool:        t.Name(),
			Intent:      t.intent,
			Query:       queryText,
			Hits:        hits,
			Summary:     summary,
			Confidence:  maxFloat(ragResult.Confidence, memoryRetrievalConfidence(memoryHits)),
			EvidenceIDs: evidenceIDs,
		},
	}, nil
}

func memoryRetrievalConfidence(hits []RetrievedDocumentEvidence) float64 {
	if len(hits) == 0 {
		return 0
	}
	return clamp01(hits[0].Score)
}

func memoryRetrievalSummary(hits []RetrievedDocumentEvidence) string {
	if len(hits) == 0 {
		return ""
	}
	top := hits[0]
	summary := fmt.Sprintf("incident memory hits=%d strongest=%s", len(hits), firstNonEmpty(top.Title, top.SourcePath, top.DocID))
	if reason := strings.TrimSpace(top.Metadata["match_reasons"]); reason != "" {
		summary += " (" + reason + ")"
	}
	return summary
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
		if search.Returned == 0 && strings.TrimSpace(queryText) != "" {
			search = t.index.Search(logindex.SearchQuery{
				CollectorID: collectorID,
				Text:        "",
				Since:       time.Now().Add(-window),
				Until:       time.Now().UTC(),
				Limit:       maxInt(req.Limit, 120),
			})
		}

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
		if search.Returned == 0 && t.store != nil {
			node := t.store.Node(collectorID)
			if node != nil {
				for _, fingerprint := range node.Logs {
					if fingerprint == nil {
						continue
					}
					line := strings.TrimSpace(fingerprint.Example)
					count := fingerprint.Count
					if count == 0 {
						count = 1
					}
					data.Total += count
					switch inferLogSeverity(line) {
					case "error":
						data.Errors += count
					case "warn":
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
				data.RecentDeploys = dedupeStrings(data.RecentDeploys)
				data.SecurityHints = dedupeStrings(data.SecurityHints)
			}
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

type changeQueryTool struct {
	store *changeintel.Store
	index *logindex.Index
	nodes *ingest.MemoryStore
}

func (t *changeQueryTool) Name() ToolName  { return ToolChangeQuery }
func (t *changeQueryTool) Version() string { return workflowToolVersion }
func (t *changeQueryTool) Description() string {
	return "Correlates recent operational changes with the active incident window."
}
func (t *changeQueryTool) Deterministic() bool { return true }
func (t *changeQueryTool) Unsafe() bool        { return false }

func (t *changeQueryTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := changeToolData{
		CollectorID: strings.TrimSpace(req.CollectorID),
		Summary:     "no recent operational changes correlated",
		Categories:  []string{},
	}
	if t == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}

	window := req.Window
	if window <= 0 {
		window = 45 * time.Minute
	}
	windowEnd := time.Now().UTC()
	windowStart := windowEnd.Add(-window)

	events := make([]changeintel.ChangeEvent, 0, 12)
	if t.store != nil {
		stored, err := t.store.Query(changeintel.QueryOptions{
			CollectorID: req.CollectorID,
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
		})
		if err == nil {
			events = append(events, stored...)
		}
	}
	if t.nodes != nil {
		if node := t.nodes.Node(strings.TrimSpace(req.CollectorID)); node != nil {
			events = append(events, changeintel.DeriveFromNode(node)...)
		}
	}
	if t.index != nil {
		search := t.index.Search(logindex.SearchQuery{
			CollectorID: strings.TrimSpace(req.CollectorID),
			Text:        "",
			Since:       windowStart,
			Until:       windowEnd,
			Limit:       maxInt(req.Limit, 48),
		})
		lines := make([]string, 0, len(search.Entries))
		for _, entry := range search.Entries {
			lines = append(lines, strings.TrimSpace(entry.Message))
		}
		events = append(events, changeintel.DeriveFromLogMessages(req.CollectorID, lines, windowEnd)...)
	}

	result := changeintel.Correlate(events, changeintel.QueryOptions{
		CollectorID:     req.CollectorID,
		IncidentSummary: firstNonEmpty(req.Query["incident_summary"], req.Query["query"]),
		WindowStart:     windowStart,
		WindowEnd:       windowEnd,
		ScopeHints:      compactStrings(req.Query["scope"], req.Query["entity"], req.CollectorID),
		Limit:           maxInt(req.Limit, 6),
	})
	data.Events = result.Events
	data.Summary = result.Summary
	data.Categories = result.Categories
	data.Strongest = result.Strongest
	return workflowToolResult{
		Summary: result.Summary,
		Data:    data,
	}, nil
}

type deploymentHistoryTool struct {
	store *changeintel.Store
	index *logindex.Index
	nodes *ingest.MemoryStore
}

func (t *deploymentHistoryTool) Name() ToolName  { return ToolDeploymentHistory }
func (t *deploymentHistoryTool) Version() string { return workflowToolVersion }
func (t *deploymentHistoryTool) Description() string {
	return "Deployment and release history filtered from the broader change correlation stream."
}
func (t *deploymentHistoryTool) Deterministic() bool { return true }
func (t *deploymentHistoryTool) Unsafe() bool        { return false }

func (t *deploymentHistoryTool) Run(ctx context.Context, req workflowToolRequest) (workflowToolResult, error) {
	base := (&changeQueryTool{store: t.store, index: t.index, nodes: t.nodes})
	result, err := base.Run(ctx, req)
	if err != nil {
		return workflowToolResult{}, err
	}
	data, ok := result.Data.(changeToolData)
	if !ok {
		return result, nil
	}
	filtered := make([]changeintel.CorrelatedChange, 0, len(data.Events))
	for _, event := range data.Events {
		category := strings.ToLower(strings.TrimSpace(event.Event.Category))
		if strings.Contains(category, "deploy") || strings.Contains(category, "release") || strings.Contains(category, "runtime") || strings.Contains(category, "driver") || strings.Contains(category, "image") {
			filtered = append(filtered, event)
		}
	}
	if len(filtered) == 0 {
		data.Events = nil
		data.Categories = nil
		data.Strongest = nil
		data.Summary = "no rollout or release history correlated with the incident window"
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	data.Events = filtered
	data.Categories = dedupeChangeCategories(filtered)
	data.Strongest = &filtered[0]
	data.Summary = fmt.Sprintf("deployment history matches=%d strongest=%s", len(filtered), firstNonEmpty(filtered[0].Event.Summary, filtered[0].Event.Category))
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}

type configStateTool struct {
	store   *ingest.MemoryStore
	changes *changeQueryTool
}

func (t *configStateTool) Name() ToolName  { return ToolConfigState }
func (t *configStateTool) Version() string { return workflowToolVersion }
func (t *configStateTool) Description() string {
	return "Runtime and config state summarizer for rollout and configuration validation."
}
func (t *configStateTool) Deterministic() bool { return true }
func (t *configStateTool) Unsafe() bool        { return false }

func (t *configStateTool) Run(ctx context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := configStateToolData{
		CollectorID: strings.TrimSpace(req.CollectorID),
		Labels:      map[string]string{},
		Summary:     "config state unavailable",
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	data.CollectorID = node.CollectorID
	for _, key := range []string{"service", "app", "job", "namespace", "pod", "pod_uid", "workload", "workload_class", "release.image", "revision", "version"} {
		if value := strings.TrimSpace(node.Labels[key]); value != "" {
			data.Labels[key] = value
		}
	}
	data.RuntimeMode = strings.TrimSpace(node.RuntimeMode)
	if t.changes != nil {
		result, err := t.changes.Run(ctx, req)
		if err == nil {
			if changeData, ok := result.Data.(changeToolData); ok {
				for _, event := range changeData.Events {
					category := strings.ToLower(strings.TrimSpace(event.Event.Category))
					if strings.Contains(category, "config") || strings.Contains(category, "flag") || strings.Contains(category, "driver") || strings.Contains(category, "runtime") || strings.Contains(category, "deploy") {
						data.Changes = append(data.Changes, event)
					}
				}
			}
		}
	}
	labelSummary := compactStrings(
		"value="+firstNonEmpty(data.Labels["service"], data.Labels["app"], data.Labels["job"]),
		"image="+data.Labels["release.image"],
		"revision="+firstNonEmpty(data.Labels["revision"], data.Labels["version"]),
		"runtime_mode="+data.RuntimeMode,
	)
	data.Summary = strings.Join(labelSummary, " ")
	if categories := dedupeChangeCategories(data.Changes); len(categories) > 0 {
		data.Summary = truncateString(strings.TrimSpace(data.Summary+" changes="+categories[0]), 220)
	}
	if strings.TrimSpace(data.Summary) == "" {
		data.Summary = "config state sampled from collector labels and runtime mode"
	}
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
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

type memoryPressureTool struct {
	store *ingest.MemoryStore
}

func (t *memoryPressureTool) Name() ToolName  { return ToolMemoryPressure }
func (t *memoryPressureTool) Version() string { return workflowToolVersion }
func (t *memoryPressureTool) Description() string {
	return "Memory pressure, OOM, and eviction-style evidence summarizer."
}
func (t *memoryPressureTool) Deterministic() bool { return true }
func (t *memoryPressureTool) Unsafe() bool        { return false }

func (t *memoryPressureTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := memoryPressureToolData{
		CollectorID:     strings.TrimSpace(req.CollectorID),
		Summary:         "memory pressure unavailable",
		PressureSignals: []string{},
		TopProcesses:    []string{},
		OOMHints:        []string{},
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	data.CollectorID = node.CollectorID
	data.WorkingSetPct = maxFloat(metricValue(node.Metrics, "node_memory_working_set_percent"), metricValue(node.Metrics, "node_memory_usage_percent"))
	data.AvailableBytes = metricValue(node.Metrics, "node_memory_available_bytes")
	data.MemoryLimitBytes = metricValue(node.Metrics, "node_memory_limit_bytes")
	for _, process := range topProcessResources(node, 4) {
		if process == nil {
			continue
		}
		if process.CategoryTotals["memory"] <= 0 {
			continue
		}
		data.TopProcesses = append(data.TopProcesses, processDisplayName(process))
	}
	for _, fp := range node.Logs {
		if fp == nil {
			continue
		}
		line := strings.ToLower(strings.TrimSpace(fp.Example))
		switch {
		case strings.Contains(line, "oom"), strings.Contains(line, "oomkilled"), strings.Contains(line, "out of memory"):
			data.OOMHints = append(data.OOMHints, truncateString(fp.Example, 160))
		case strings.Contains(line, "evict"), strings.Contains(line, "memory pressure"):
			data.PressureSignals = append(data.PressureSignals, truncateString(fp.Example, 160))
		}
	}
	if data.WorkingSetPct >= 90 {
		data.PressureSignals = append(data.PressureSignals, fmt.Sprintf("working_set=%.1f%%", data.WorkingSetPct))
	}
	data.TopProcesses = dedupeStrings(data.TopProcesses)
	data.OOMHints = dedupeStrings(data.OOMHints)
	data.PressureSignals = dedupeStrings(data.PressureSignals)
	data.Summary = fmt.Sprintf("memory working_set=%.1f%% top_processes=%d oom_hints=%d", data.WorkingSetPct, len(data.TopProcesses), len(data.OOMHints))
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}

type connectivityCheckTool struct {
	store *ingest.MemoryStore
}

func (t *connectivityCheckTool) Name() ToolName  { return ToolConnectivityCheck }
func (t *connectivityCheckTool) Version() string { return workflowToolVersion }
func (t *connectivityCheckTool) Description() string {
	return "Connectivity and packet-pressure check built from node metrics and runtime behavior."
}
func (t *connectivityCheckTool) Deterministic() bool { return true }
func (t *connectivityCheckTool) Unsafe() bool        { return false }

func (t *connectivityCheckTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := connectivityCheckToolData{
		CollectorID: strings.TrimSpace(req.CollectorID),
		Summary:     "connectivity telemetry unavailable",
		Healthy:     true,
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	data.CollectorID = node.CollectorID
	data.RetransmitRatio = maxFloat(metricValue(node.Metrics, "node_tcp_retransmit_ratio"), metricValue(node.Metrics, "node_network_tcp_retransmit_ratio"))
	data.SoftnetDrops = maxFloat(metricValue(node.Metrics, "node_softnet_dropped_per_second"), metricValue(node.Metrics, "node_network_softnet_dropped_per_second"))
	data.UnexpectedOutbound = node.NetworkBehavior.UnexpectedOutbound
	data.Healthy = data.RetransmitRatio < 0.02 && data.SoftnetDrops < 25 && data.UnexpectedOutbound == 0
	data.Summary = fmt.Sprintf("connectivity healthy=%t retransmit_ratio=%.3f softnet_drops=%.1f unexpected_outbound=%d", data.Healthy, data.RetransmitRatio, data.SoftnetDrops, data.UnexpectedOutbound)
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}

type dnsCheckTool struct {
	index *logindex.Index
	store *ingest.MemoryStore
}

func (t *dnsCheckTool) Name() ToolName  { return ToolDNSCheck }
func (t *dnsCheckTool) Version() string { return workflowToolVersion }
func (t *dnsCheckTool) Description() string {
	return "DNS failure and resolver-hint scan from logs and runtime metadata."
}
func (t *dnsCheckTool) Deterministic() bool { return true }
func (t *dnsCheckTool) Unsafe() bool        { return false }

func (t *dnsCheckTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := dnsCheckToolData{
		CollectorID: strings.TrimSpace(req.CollectorID),
		Summary:     "dns evidence unavailable",
		Healthy:     true,
		Hints:       []string{},
	}
	window := maxDuration(req.Window, 30*time.Minute)
	if t != nil && t.index != nil {
		search := t.index.Search(logindex.SearchQuery{
			CollectorID: strings.TrimSpace(req.CollectorID),
			Text:        "dns",
			Since:       time.Now().Add(-window),
			Until:       time.Now().UTC(),
			Limit:       maxInt(req.Limit, 48),
		})
		for _, entry := range search.Entries {
			line := strings.ToLower(strings.TrimSpace(entry.Message))
			if strings.Contains(line, "nxdomain") || strings.Contains(line, "dns") || strings.Contains(line, "resolver") || strings.Contains(line, "lookup ") {
				data.Hints = append(data.Hints, truncateString(entry.Message, 180))
			}
		}
	}
	if t != nil && t.store != nil && len(data.Hints) == 0 {
		if node := t.store.Node(strings.TrimSpace(req.CollectorID)); node != nil {
			for _, fp := range node.Logs {
				if fp == nil {
					continue
				}
				line := strings.ToLower(strings.TrimSpace(fp.Example))
				if strings.Contains(line, "nxdomain") || strings.Contains(line, "dns") || strings.Contains(line, "resolver") || strings.Contains(line, "lookup ") {
					data.Hints = append(data.Hints, truncateString(fp.Example, 180))
				}
			}
		}
	}
	data.Hints = dedupeStrings(data.Hints)
	data.Healthy = len(data.Hints) == 0
	data.Summary = fmt.Sprintf("dns healthy=%t hints=%d", data.Healthy, len(data.Hints))
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}

type serviceHealthTool struct {
	store *ingest.MemoryStore
}

func (t *serviceHealthTool) Name() ToolName  { return ToolServiceHealth }
func (t *serviceHealthTool) Version() string { return workflowToolVersion }
func (t *serviceHealthTool) Description() string {
	return "Service health summary for latency, errors, and restart-like behavior."
}
func (t *serviceHealthTool) Deterministic() bool { return true }
func (t *serviceHealthTool) Unsafe() bool        { return false }

func (t *serviceHealthTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := serviceHealthToolData{
		CollectorID: strings.TrimSpace(req.CollectorID),
		Summary:     "service health unavailable",
		Healthy:     true,
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	data.CollectorID = node.CollectorID
	data.LatencyMS = maxFloat(metricValue(node.Metrics, "service_latency_p95_ms"), metricValue(node.Metrics, "service_latency_p99_ms"))
	data.ErrorRate = maxFloat(metricValue(node.Metrics, "service_error_rate"), metricValue(node.Metrics, "service_request_error_rate"))
	restarts := maxFloat(metricValue(node.Metrics, "process_restart_count"), metricValue(node.Metrics, "container_restart_count"))
	data.RestartLike = restarts > 0
	data.Healthy = data.ErrorRate < 0.03 && data.LatencyMS < 1000 && !data.RestartLike
	data.Summary = fmt.Sprintf("service healthy=%t latency_ms=%.1f error_rate=%.3f restart_like=%t", data.Healthy, data.LatencyMS, data.ErrorRate, data.RestartLike)
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
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
	StructuredFindings       []workflowSecurityFinding
}

type workflowSecurityFinding struct {
	FindingID         string
	EvidenceID        string
	Category          string
	Severity          string
	Scope             string
	Summary           string
	Description       string
	RecommendedAction string
	Confidence        float64
	Source            string
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
		StructuredFindings:       []workflowSecurityFinding{},
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
			findingID := normalizeWorkflowSecurityEvidenceID(firstNonEmpty(strings.TrimSpace(finding.FindingID), strings.TrimSpace(finding.ID)))
			evidenceID := normalizeWorkflowSecurityEvidenceID(firstNonEmpty(strings.TrimSpace(finding.EvidenceID), findingID))
			data.FindingIDs = append(data.FindingIDs, findingID)
			data.Categories = append(data.Categories, finding.Category)
			data.Findings = append(data.Findings, fmt.Sprintf("%s: %s", strings.ToUpper(string(finding.Severity)), finding.Summary))
			data.StructuredFindings = append(data.StructuredFindings, workflowSecurityFinding{
				FindingID:         findingID,
				EvidenceID:        evidenceID,
				Category:          strings.TrimSpace(finding.Category),
				Severity:          string(finding.Severity),
				Scope:             strings.TrimSpace(finding.Scope),
				Summary:           strings.TrimSpace(finding.Summary),
				Description:       strings.TrimSpace(finding.Description),
				RecommendedAction: strings.TrimSpace(finding.RecommendedAction),
				Confidence:        finding.Confidence,
				Source:            strings.TrimSpace(finding.Source),
			})
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

			appendSecurityCategoryHints(&data, finding)
		}

		data.Categories = dedupeStrings(data.Categories)
		data.FindingIDs = dedupeStrings(data.FindingIDs)
		data.Findings = dedupeStrings(data.Findings)
		data.StructuredFindings = dedupeWorkflowSecurityFindings(data.StructuredFindings)
		if len(data.StructuredFindings) > 12 {
			data.StructuredFindings = data.StructuredFindings[:12]
		}
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

func appendSecurityCategoryHints(data *securityToolData, finding securityaudit.Finding) {
	if data == nil {
		return
	}

	category := strings.ToLower(strings.TrimSpace(finding.Category))
	switch {
	case strings.Contains(category, "network"):
		appendNonEmptySecurityHints(&data.SuspiciousPortCandidates, append([]string{finding.Summary}, finding.Evidence...)...)
	case strings.Contains(category, "filesystem"), strings.Contains(category, "permission"):
		appendNonEmptySecurityHints(&data.WeakPermissionHints, append([]string{finding.Summary}, finding.Evidence...)...)
	}
}

func appendNonEmptySecurityHints(dst *[]string, values ...string) {
	if dst == nil {
		return
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		*dst = append(*dst, value)
	}
}

func normalizeWorkflowSecurityEvidenceID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(id), "sec-") {
		return "sf-" + strings.TrimSpace(id[4:])
	}
	return id
}

func dedupeWorkflowSecurityFindings(in []workflowSecurityFinding) []workflowSecurityFinding {
	if len(in) == 0 {
		return nil
	}
	out := make([]workflowSecurityFinding, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, finding := range in {
		key := firstNonEmpty(strings.TrimSpace(finding.FindingID), strings.TrimSpace(finding.EvidenceID), strings.TrimSpace(finding.Category)+"|"+strings.TrimSpace(finding.Summary))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, finding)
	}
	return out
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

type gpuQueryTool struct {
	store *ingest.MemoryStore
}

func (t *gpuQueryTool) Name() ToolName  { return ToolGPU }
func (t *gpuQueryTool) Version() string { return workflowToolVersion }
func (t *gpuQueryTool) Description() string {
	return "Summarizes GPU telemetry from collector metrics for RCA and capacity contention analysis."
}
func (t *gpuQueryTool) Deterministic() bool { return true }
func (t *gpuQueryTool) Unsafe() bool        { return false }

func (t *gpuQueryTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := gpuToolData{
		Summary:      "gpu telemetry unavailable",
		Metrics:      map[string]float64{},
		TopProcesses: []string{},
		EvidenceIDs:  []string{},
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}

	for name, value := range node.Metrics {
		if strings.HasPrefix(name, "node_gpu_") {
			data.Metrics[name] = value
		}
	}
	for _, process := range topProcessResources(node, 6) {
		if process == nil {
			continue
		}
		if total := process.CategoryTotals["gpu"]; total <= 0 {
			continue
		}
		data.TopProcesses = append(data.TopProcesses, processDisplayName(process))
	}
	data.TopProcesses = dedupeStrings(data.TopProcesses)

	util := metricValue(data.Metrics, "node_gpu_utilization_sm_avg_percent")
	memPct := metricValue(data.Metrics, "node_gpu_memory_used_percent")
	pcieRx := metricValue(data.Metrics, "node_gpu_pcie_rx_total_mb_s")
	pcieTx := metricValue(data.Metrics, "node_gpu_pcie_tx_total_mb_s")
	switch {
	case util >= 85 && memPct >= 85:
		data.Bottleneck = "gpu compute and memory saturation"
	case memPct >= 90:
		data.Bottleneck = "gpu memory pressure"
	case pcieRx+pcieTx >= 4096:
		data.Bottleneck = "pcie bandwidth contention"
	case util >= 90:
		data.Bottleneck = "gpu compute saturation"
	default:
		data.Bottleneck = "no dominant gpu bottleneck"
	}
	if len(data.Metrics) > 0 {
		data.Summary = fmt.Sprintf("gpu metrics=%d top_processes=%d bottleneck=%s", len(data.Metrics), len(data.TopProcesses), data.Bottleneck)
	}
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
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

type kubernetesResourceTool struct {
	store *ingest.MemoryStore
}

func (t *kubernetesResourceTool) Name() ToolName  { return ToolKubernetesResource }
func (t *kubernetesResourceTool) Version() string { return workflowToolVersion }
func (t *kubernetesResourceTool) Description() string {
	return "Kubernetes workload and pod identity projection from collector labels and process metadata."
}
func (t *kubernetesResourceTool) Deterministic() bool { return true }
func (t *kubernetesResourceTool) Unsafe() bool        { return false }

func (t *kubernetesResourceTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := kubernetesResourceToolData{
		CollectorID: strings.TrimSpace(req.CollectorID),
		Summary:     "kubernetes resource identity unavailable",
		Processes:   []string{},
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	data.CollectorID = node.CollectorID
	data.Namespace = firstNonEmpty(node.Labels["namespace"], node.Labels["k8s.namespace"], node.Labels["kubernetes_namespace"])
	data.Service = firstNonEmpty(node.Labels["service"], node.Labels["app"], node.Labels["job"])
	data.Workload = firstNonEmpty(node.Labels["workload"], node.Labels["workload_class"], node.Labels["deployment"], node.Labels["statefulset"])
	data.PodUID = firstNonEmpty(node.Labels["pod_uid"], node.Labels["pod"], node.Labels["k8s.pod.uid"])
	for _, process := range topProcessResources(node, 5) {
		if process == nil {
			continue
		}
		data.Processes = append(data.Processes, processDisplayName(process))
		if data.Workload == "" {
			data.Workload = firstNonEmpty(process.WorkloadClass, process.Job)
		}
		if data.PodUID == "" {
			data.PodUID = process.PodUID
		}
	}
	data.Processes = dedupeStrings(data.Processes)
	data.Summary = truncateString(strings.Join(compactStrings(
		"namespace="+data.Namespace,
		"service="+data.Service,
		"workload="+data.Workload,
		"pod_uid="+data.PodUID,
	), " "), 220)
	if data.Summary == "" {
		data.Summary = "kubernetes resource identity sampled from labels and process metadata"
	}
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}

type containerRevisionTool struct {
	store *ingest.MemoryStore
}

func (t *containerRevisionTool) Name() ToolName  { return ToolContainerRevision }
func (t *containerRevisionTool) Version() string { return workflowToolVersion }
func (t *containerRevisionTool) Description() string {
	return "Container revision, image, and runtime mode sampler for rollout validation."
}
func (t *containerRevisionTool) Deterministic() bool { return true }
func (t *containerRevisionTool) Unsafe() bool        { return false }

func (t *containerRevisionTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := containerRevisionToolData{
		CollectorID: strings.TrimSpace(req.CollectorID),
		Summary:     "container revision state unavailable",
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	data.CollectorID = node.CollectorID
	data.Service = firstNonEmpty(node.Labels["service"], node.Labels["app"], node.Labels["job"])
	data.Revision = firstNonEmpty(node.Labels["revision"], node.Labels["version"], node.Labels["rollout_revision"])
	data.Image = firstNonEmpty(node.Labels["release.image"], node.Labels["image"], node.Labels["container_image"])
	data.PodUID = firstNonEmpty(node.Labels["pod_uid"], node.Labels["pod"], node.Labels["k8s.pod.uid"])
	data.RuntimeMode = node.RuntimeMode
	data.Summary = truncateString(strings.Join(compactStrings(
		"service="+data.Service,
		"revision="+data.Revision,
		"image="+data.Image,
		"pod_uid="+data.PodUID,
		"runtime_mode="+data.RuntimeMode,
	), " "), 220)
	if data.Summary == "" {
		data.Summary = "container revision sampled from labels and runtime mode"
	}
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}

type storageHealthTool struct {
	store *ingest.MemoryStore
}

func (t *storageHealthTool) Name() ToolName  { return ToolStorageHealth }
func (t *storageHealthTool) Version() string { return workflowToolVersion }
func (t *storageHealthTool) Description() string {
	return "Disk and filesystem pressure summarizer for storage-centric incidents."
}
func (t *storageHealthTool) Deterministic() bool { return true }
func (t *storageHealthTool) Unsafe() bool        { return false }

func (t *storageHealthTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := storageHealthToolData{
		CollectorID:    strings.TrimSpace(req.CollectorID),
		Summary:        "storage health unavailable",
		HotDevices:     []string{},
		HotFilesystems: []string{},
	}
	if t == nil || t.store == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(strings.TrimSpace(req.CollectorID))
	if node == nil {
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	data.CollectorID = node.CollectorID
	for _, sample := range node.StorageDevices {
		if sample == nil {
			continue
		}
		data.AverageLatency = maxFloat(data.AverageLatency, sample.AvgRequestLatencyMS)
		data.PeakUtilization = maxFloat(data.PeakUtilization, sample.UtilizationPercent)
		if sample.UtilizationPercent >= 85 || sample.QueueDepth >= 8 || sample.AvgRequestLatencyMS >= 20 {
			data.Pressure = true
			data.HotDevices = append(data.HotDevices, firstNonEmpty(sample.Device, sample.Partition))
		}
	}
	for mount, sample := range node.Filesystems {
		if sample == nil {
			continue
		}
		if sample.UsedPercent >= 90 || sample.FilesUsedPercent >= 90 {
			data.Pressure = true
			data.HotFilesystems = append(data.HotFilesystems, mount)
		}
	}
	data.HotDevices = dedupeStrings(data.HotDevices)
	data.HotFilesystems = dedupeStrings(data.HotFilesystems)
	data.Summary = fmt.Sprintf("storage pressure=%t hot_devices=%d hot_filesystems=%d peak_utilization=%.1f latency_ms=%.1f", data.Pressure, len(data.HotDevices), len(data.HotFilesystems), data.PeakUtilization, data.AverageLatency)
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}

type networkBlastRadiusTool struct {
	provider TopologyProvider
	store    *ingest.MemoryStore
}

func (t *networkBlastRadiusTool) Name() ToolName  { return ToolNetworkBlastRadius }
func (t *networkBlastRadiusTool) Version() string { return workflowToolVersion }
func (t *networkBlastRadiusTool) Description() string {
	return "Topology-aware downstream scope estimate for network- and service-health incidents."
}
func (t *networkBlastRadiusTool) Deterministic() bool { return true }
func (t *networkBlastRadiusTool) Unsafe() bool        { return false }

func (t *networkBlastRadiusTool) Run(ctx context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := networkBlastRadiusToolData{
		CollectorID: strings.TrimSpace(req.CollectorID),
		Summary:     "network blast radius unavailable",
		Scope:       []string{},
		Downstream:  []string{},
	}
	service := ""
	if t != nil && t.store != nil {
		if node := t.store.Node(strings.TrimSpace(req.CollectorID)); node != nil {
			data.CollectorID = node.CollectorID
			service = firstNonEmpty(node.Labels["service"], node.Labels["app"], node.Labels["job"], node.Hostname)
			data.Scope = compactStrings("collector:"+node.CollectorID, "service:"+service)
		}
	}
	if t != nil && t.provider != nil {
		snapshot := t.provider.Snapshot(ctx)
		for _, edge := range snapshot.Edges {
			if service == "" || (!strings.Contains(strings.ToLower(edge.Source), strings.ToLower(service)) && !strings.Contains(strings.ToLower(edge.Target), strings.ToLower(service))) {
				continue
			}
			if strings.Contains(strings.ToLower(edge.Source), strings.ToLower(service)) {
				data.Downstream = append(data.Downstream, edge.Target)
			} else {
				data.Downstream = append(data.Downstream, edge.Source)
			}
		}
	}
	data.Downstream = dedupeStrings(data.Downstream)
	data.BlastRadius = len(data.Downstream)
	data.Summary = fmt.Sprintf("network blast radius=%d scope=%d", data.BlastRadius, len(data.Scope))
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}

type actionOutcomeTool struct {
	memory *WorkflowMemoryStore
}

func (t *actionOutcomeTool) Name() ToolName  { return ToolActionOutcome }
func (t *actionOutcomeTool) Version() string { return workflowToolVersion }
func (t *actionOutcomeTool) Description() string {
	return "Prior verified remediation outcome retrieval from durable incident memory."
}
func (t *actionOutcomeTool) Deterministic() bool { return true }
func (t *actionOutcomeTool) Unsafe() bool        { return false }

func (t *actionOutcomeTool) Run(_ context.Context, req workflowToolRequest) (workflowToolResult, error) {
	queryText := strings.TrimSpace(firstNonEmpty(req.Query["query"], req.Query["q"]))
	if t == nil || t.memory == nil || queryText == "" {
		return workflowToolResult{
			Summary: "prior action outcomes unavailable",
			Data: knowledgeToolData{
				Tool:        ToolActionOutcome,
				Intent:      "prior_action_outcome",
				Query:       queryText,
				Hits:        nil,
				Summary:     "prior action outcomes unavailable",
				Confidence:  0,
				EvidenceIDs: nil,
			},
		}, nil
	}
	hits := t.memory.Query(queryText, "historical_incident", req.CollectorID, maxInt(parseIntFromString(req.Query["top_k"], 3), 1))
	filtered := make([]RetrievedDocumentEvidence, 0, len(hits))
	evidenceIDs := make([]string, 0, len(hits))
	for _, hit := range hits {
		reasons := strings.ToLower(strings.TrimSpace(hit.Metadata["match_reasons"]))
		verification := strings.ToLower(strings.TrimSpace(hit.Metadata["verification_summary"]))
		if !strings.Contains(reasons, "action") && !strings.Contains(reasons, "verified") && verification == "" {
			continue
		}
		filtered = append(filtered, hit)
		evidenceIDs = append(evidenceIDs, hit.EvidenceID)
	}
	summary := fmt.Sprintf("prior action outcomes hits=%d", len(filtered))
	if len(filtered) > 0 {
		summary = fmt.Sprintf("prior action outcomes hits=%d strongest=%s", len(filtered), firstNonEmpty(filtered[0].Title, filtered[0].SourcePath))
	}
	return workflowToolResult{
		Summary: summary,
		Data: knowledgeToolData{
			Tool:        ToolActionOutcome,
			Intent:      "prior_action_outcome",
			Query:       queryText,
			Hits:        filtered,
			Summary:     summary,
			Confidence:  memoryRetrievalConfidence(filtered),
			EvidenceIDs: evidenceIDs,
		},
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
	BlastRadius      int
	DownstreamNodes  []string
}

type remediationActionTool struct {
	cfg    WorkflowConfig
	runner *PlaybookRunner
}

func (t *remediationActionTool) Name() ToolName  { return ToolRemediation }
func (t *remediationActionTool) Version() string { return workflowToolVersion }
func (t *remediationActionTool) Description() string {
	return "Guarded remediation planner (dry-run, approval, rollback required)."
}
func (t *remediationActionTool) Deterministic() bool { return true }
func (t *remediationActionTool) Unsafe() bool        { return true }

func (t *remediationActionTool) Run(ctx context.Context, req workflowToolRequest) (workflowToolResult, error) {
	summary := "Remediation planning complete."
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
		if t.cfg.ActionRunner.AllowUnsafe || t.cfg.AllowRemediationExec {
			if t.runner != nil {
				// Actual execution
				action := ActionSpec{
					Type:            "shell", // default for now, can be parsed from req.Query
					Command:         req.Query["action"],
					Namespace:       req.Query["scope"],
					Safe:            false,
					ID:              fmt.Sprintf("rem-%d", time.Now().Unix()),
					RollbackCommand: req.Query["rollback"],
				}
				results := t.runner.Execute(ctx, []ActionSpec{action}, false)
				if len(results) > 0 {
					res := results[0]
					if res.Status == ActionResultExecuted {
						mode = "executed"
						summary = fmt.Sprintf("Successfully executed remediation: %s. Output: %s", action.Command, res.Output)
					} else {
						mode = "failed"
						summary = fmt.Sprintf("Remediation failed: %s. Error: %s", action.Command, res.Error)
						return workflowToolResult{Summary: summary, Data: res}, fmt.Errorf("remediation failed: %s", res.Error)
					}
					return workflowToolResult{Summary: summary, Data: res}, nil
				}
			}
			mode = "approved_execution_requested"
		} else {
			mode = "blocked_by_policy"
		}
	}
	summary = fmt.Sprintf("remediation plan for %s on %s (%s)", action, scope, mode)
	data := remediationToolData{
		Action:           action,
		Summary:          summary,
		DryRun:           req.DryRun || t.cfg.DryRun,
		RequiresApproval: true,
		Reversible:       true,
		RollbackPlan:     "capture pre-change baseline -> execute single scoped change -> revert workload/config to previous revision",
		Mode:             mode,
	}

	// Simple blast radius assessment if topology hints are provided
	if downstream := req.Query["downstream_nodes"]; downstream != "" {
		nodes := strings.Split(downstream, ",")
		data.BlastRadius = len(nodes)
		data.DownstreamNodes = nodes
		data.Summary += fmt.Sprintf(" | potential blast radius: %d downstream nodes", data.BlastRadius)
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

func dedupeChangeCategories(events []changeintel.CorrelatedChange) []string {
	if len(events) == 0 {
		return nil
	}
	categories := make([]string, 0, len(events))
	for _, event := range events {
		if category := strings.TrimSpace(event.Event.Category); category != "" {
			categories = append(categories, category)
		}
	}
	return dedupeStrings(categories)
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

// PolicyEngine enforces guardrails on tool execution.
type PolicyEngine struct {
	logger *zap.Logger
}

func NewPolicyEngine(logger *zap.Logger) *PolicyEngine {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PolicyEngine{logger: logger}
}

func (e *PolicyEngine) Evaluate(req workflowToolRequest, tool workflowTool) ActionPolicyDecision {
	desc := describeWorkflowTool(tool)

	// Read-only tools are always allowed
	if desc.ReadOnly {
		return ActionPolicyDecision{Status: "allowed", Reason: "read-only tool", ExecutionLevel: "read_only"}
	}

	if tool != nil && tool.Name() == ToolRemediation && strings.TrimSpace(req.Query["rollback"]) == "" {
		return ActionPolicyDecision{
			Status:            "blocked",
			Reason:            "impactful remediation requires rollback guidance",
			ExecutionLevel:    "suggest_only",
			RollbackRequired:  true,
			MissingConditions: []string{"rollback"},
		}
	}

	// Unsafe tools are allowed only as dry-run without explicit approval.
	if req.DryRun {
		return ActionPolicyDecision{
			Status:         "allowed",
			Reason:         "unsafe tool allowed in dry-run mode",
			ExecutionLevel: "dry_run_only",
			DryRunRequired: true,
		}
	}
	if desc.RequiresApproval {
		return ActionPolicyDecision{
			Status:           "pending",
			Reason:           "requires explicit approval",
			ExecutionLevel:   "approval_required",
			RequiresApproval: true,
			DryRunRequired:   true,
			RollbackRequired: desc.SupportsRollback,
		}
	}

	return ActionPolicyDecision{
		Status:         "allowed",
		Reason:         "unsafe tool defaults to dry-run",
		ExecutionLevel: "dry_run_only",
		DryRunRequired: true,
	}
}
