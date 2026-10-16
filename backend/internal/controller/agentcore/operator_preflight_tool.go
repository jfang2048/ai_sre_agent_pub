package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

// operatorPreflightData is evidence for a human decision, not permission to
// execute or a claim that a proposed change will restore the service.
type operatorPreflightData struct {
	CollectorID     string                     `json:"collector_id"`
	ObservedAt      time.Time                  `json:"observed_at,omitempty"`
	AgeSeconds      float64                    `json:"age_seconds,omitempty"`
	Fresh           bool                       `json:"fresh"`
	HealthAvailable bool                       `json:"health_available"`
	Identity        kubernetesResourceToolData `json:"identity"`
	Revision        containerRevisionToolData  `json:"revision"`
	Health          serviceHealthToolData      `json:"health"`
	Impact          networkBlastRadiusToolData `json:"impact"`
	EvidenceGaps    []string                   `json:"evidence_gaps"`
	OperatorChecks  []string                   `json:"operator_checks"`
	Summary         string                     `json:"summary"`
}

type operatorPreflightTool struct {
	store    *ingest.MemoryStore
	topology TopologyProvider
	maxAge   time.Duration
}

func (t *operatorPreflightTool) Name() ToolName  { return ToolOperatorPreflight }
func (t *operatorPreflightTool) Version() string { return workflowToolVersion }
func (t *operatorPreflightTool) Description() string {
	return "Read-only operator preflight with freshness and evidence gaps."
}
func (t *operatorPreflightTool) Deterministic() bool { return true }
func (t *operatorPreflightTool) Unsafe() bool        { return false }

func (t *operatorPreflightTool) Run(ctx context.Context, req workflowToolRequest) (workflowToolResult, error) {
	data := operatorPreflightData{
		CollectorID:  strings.TrimSpace(req.CollectorID),
		EvidenceGaps: []string{},
		OperatorChecks: []string{
			"Confirm target identity, dependencies, and current permissions; check replicas and disruption budget when applicable.",
			"Confirm rollback readiness and compare post-action service health with this baseline.",
		},
	}
	if err := ctx.Err(); err != nil {
		return workflowToolResult{}, err
	}
	if t == nil || t.store == nil || data.CollectorID == "" {
		data.EvidenceGaps = append(data.EvidenceGaps, "collector identity or telemetry store unavailable")
		data.Summary = "operator preflight unavailable: no collector snapshot"
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}
	node := t.store.Node(data.CollectorID)
	if node == nil {
		data.EvidenceGaps = append(data.EvidenceGaps, "collector snapshot unavailable")
		data.Summary = "operator preflight unavailable: no collector snapshot"
		return workflowToolResult{Summary: data.Summary, Data: data}, nil
	}

	identity, err := (&kubernetesResourceTool{store: t.store}).Run(ctx, req)
	if err != nil {
		return workflowToolResult{}, err
	}
	data.Identity, _ = identity.Data.(kubernetesResourceToolData)
	revision, err := (&containerRevisionTool{store: t.store}).Run(ctx, req)
	if err != nil {
		return workflowToolResult{}, err
	}
	data.Revision, _ = revision.Data.(containerRevisionToolData)
	health, err := (&serviceHealthTool{store: t.store, maxAge: t.maxAge}).Run(ctx, req)
	if err != nil {
		return workflowToolResult{}, err
	}
	data.Health, _ = health.Data.(serviceHealthToolData)
	data.ObservedAt = data.Health.ObservedAt
	if !data.ObservedAt.IsZero() {
		data.AgeSeconds = maxFloat(time.Since(data.ObservedAt).Seconds(), 0)
	}
	data.Fresh = data.Health.Fresh
	data.HealthAvailable = data.Health.Available
	if !data.Fresh {
		data.EvidenceGaps = append(data.EvidenceGaps, "service metric snapshot is missing a timestamp or is stale")
	}
	if t.topology != nil {
		impact, err := (&networkBlastRadiusTool{provider: t.topology, store: t.store}).Run(ctx, req)
		if err != nil {
			return workflowToolResult{}, err
		}
		data.Impact, _ = impact.Data.(networkBlastRadiusToolData)
		if len(data.Impact.Downstream) > 16 {
			data.Impact.Downstream = data.Impact.Downstream[:16]
		}
	} else {
		data.EvidenceGaps = append(data.EvidenceGaps, "dependency topology unavailable")
	}
	if data.Identity.Service == "" && data.Identity.Workload == "" {
		data.EvidenceGaps = append(data.EvidenceGaps, "service or workload identity unavailable")
	}
	if data.Revision.Revision == "" && data.Revision.Image == "" {
		data.EvidenceGaps = append(data.EvidenceGaps, "deployed revision or image unavailable")
	}
	if !data.HealthAvailable {
		data.Health.Healthy = false
		data.EvidenceGaps = append(data.EvidenceGaps, "service health metrics unavailable or incomplete")
	}
	if data.Identity.Namespace != "" || data.Identity.Workload != "" {
		data.EvidenceGaps = append(data.EvidenceGaps, "live replicas and disruption budget require separate operator verification")
	}
	data.Summary = fmt.Sprintf("operator preflight collector=%s fresh=%t health_available=%t evidence_gaps=%d", data.CollectorID, data.Fresh, data.HealthAvailable, len(data.EvidenceGaps))
	return workflowToolResult{Summary: data.Summary, Data: data}, nil
}
