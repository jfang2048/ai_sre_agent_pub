package agent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
)

func buildNormalizedWorkflowEvidence(state *workflowState) []evidencev1.Record {
	if state == nil {
		return nil
	}

	records := make([]evidencev1.Record, 0, 48)
	seen := make(map[string]struct{}, 64)
	add := func(record evidencev1.Record) {
		record.ID = strings.TrimSpace(record.ID)
		if record.ID == "" {
			return
		}
		if _, ok := seen[record.ID]; ok {
			return
		}
		if record.SchemaVersion == "" {
			record.SchemaVersion = evidencev1.SchemaVersionV1
		}
		if record.Timestamp.IsZero() {
			record.Timestamp = state.now
		}
		seen[record.ID] = struct{}{}
		records = append(records, record)
	}

	if record := buildHostInventoryEvidence(state); record != nil {
		add(*record)
	}
	for _, record := range buildDeploymentChangeEvidence(state) {
		add(record)
	}
	for _, record := range buildTopologyEvidence(state) {
		add(record)
	}
	for _, record := range buildProcessSnapshotEvidence(state) {
		add(record)
	}
	for _, record := range buildSecurityEvidence(state) {
		add(record)
	}
	for _, record := range buildRuntimeEventEvidence(state) {
		add(record)
	}
	for _, record := range buildBehavioralAssessmentEvidence(state) {
		add(record)
	}
	for _, record := range buildAdaptiveBaselineEvidence(state) {
		add(record)
	}
	for _, record := range buildGPUEvidence(state) {
		add(record)
	}
	for _, record := range buildKnowledgeEvidence(state) {
		add(record)
	}
	for _, record := range buildProposedActionEvidence(state) {
		add(record)
	}
	for _, record := range buildRemediationEvidence(state) {
		add(record)
	}
	for _, record := range mapLegacyWorkflowEvidence(state) {
		add(record)
	}
	return records
}

func buildHostInventoryEvidence(state *workflowState) *evidencev1.Record {
	node := state.metricsData.Node
	if node == nil {
		return nil
	}
	attrs := map[string]string{
		"os":                     strings.TrimSpace(node.OS),
		"arch":                   strings.TrimSpace(node.Arch),
		"agent.version":          strings.TrimSpace(node.Version),
		"probe.source":           strings.TrimSpace(node.ProbeSource),
		"runtime.mode":           strings.TrimSpace(node.RuntimeMode),
		"runtime.mode.requested": strings.TrimSpace(node.RuntimeModeRequested),
		"runtime.degraded":       strconv.FormatBool(node.RuntimeModeDegraded),
		"runtime.containerized":  strconv.FormatBool(node.RuntimeContainerized),
		"metric.count":           strconv.Itoa(node.MetricCount),
		"log.count":              strconv.Itoa(node.LogCount),
	}
	for key, value := range map[string]string{
		"service.name":    node.Labels["service"],
		"job.id":          node.Labels["job"],
		"k8s.cluster":     node.Labels["cluster"],
		"k8s.namespace":   node.Labels["namespace"],
		"k8s.pod":         node.Labels["pod"],
		"k8s.node.name":   node.Labels["node"],
		"host.name.label": node.Labels["hostname"],
	} {
		value = strings.TrimSpace(value)
		if value != "" {
			attrs[key] = value
		}
	}
	return &evidencev1.Record{
		ID:         fmt.Sprintf("ev-inventory-%s", sanitizeID(firstNonEmpty(node.CollectorID, node.Hostname))),
		Kind:       "host_inventory",
		Category:   "inventory",
		Summary:    fmt.Sprintf("host inventory for %s (%s/%s)", firstNonEmpty(node.Hostname, node.CollectorID), firstNonEmpty(node.OS, "unknown-os"), firstNonEmpty(node.Arch, "unknown-arch")),
		Severity:   "info",
		Confidence: 1,
		Timestamp:  maxTime(node.UpdatedAt, node.LastSeen),
		Subject:    workflowEvidenceSubject(state, "node", firstNonEmpty(node.Hostname, node.CollectorID)),
		Attributes: attrs,
		Provenance: workflowEvidenceProvenance(state, "ingest", ToolMetrics, "trusted_structured"),
		RawReferences: []evidencev1.RawReference{
			{Kind: "collector_id", ID: node.CollectorID},
			{Kind: "last_batch_id", ID: node.LastBatchID},
		},
	}
}

func buildDeploymentChangeEvidence(state *workflowState) []evidencev1.Record {
	if len(state.logsData.RecentDeploys) == 0 && len(state.changeLinks) == 0 {
		return nil
	}
	out := make([]evidencev1.Record, 0, minInt(len(state.logsData.RecentDeploys)+len(state.changeLinks), 6))
	for idx, change := range state.changeLinks {
		if idx >= 4 {
			break
		}
		out = append(out, evidencev1.Record{
			ID:         firstNonEmpty(strings.TrimSpace(change.ChangeID), fmt.Sprintf("ev-change-%02d-%s", idx+1, sanitizeID(change.Summary))),
			Kind:       "change_event",
			Category:   firstNonEmpty(strings.TrimSpace(change.Category), "change"),
			Summary:    truncateString(change.Summary, 220),
			Severity:   riskLevelFromConfidence(change.CorrelationScore),
			Confidence: clamp01(change.CorrelationScore),
			Timestamp:  firstNonZeroTime(change.StartedAt, state.now),
			Subject:    workflowEvidenceSubject(state, firstNonEmpty(change.Scope, "service"), firstNonEmpty(change.Entity, state.collectorID, "fleet")),
			Attributes: map[string]string{
				"impact_summary":   strings.TrimSpace(change.ImpactSummary),
				"hypothesis_hint":  strings.TrimSpace(change.HypothesisHint),
				"source":           strings.TrimSpace(change.Source),
				"temporal_overlap": fmt.Sprintf("%.2f", change.TemporalAdjacency),
				"scope_overlap":    fmt.Sprintf("%.2f", change.ScopeOverlap),
			},
			Provenance: workflowEvidenceProvenance(state, "change_intel", ToolChangeQuery, "trusted_derived"),
		})
	}
	for idx, change := range state.logsData.RecentDeploys {
		if idx >= 4 {
			break
		}
		change = strings.TrimSpace(change)
		if change == "" {
			continue
		}
		out = append(out, evidencev1.Record{
			ID:         fmt.Sprintf("ev-deploy-%02d-%s", idx+1, sanitizeID(change)),
			Kind:       "deployment_change",
			Category:   "deployment",
			Summary:    truncateString(change, 220),
			Severity:   "medium",
			Confidence: 0.72,
			Timestamp:  state.now,
			Subject:    workflowEvidenceSubject(state, "service", firstNonEmpty(state.collectorID, "fleet")),
			Provenance: workflowEvidenceProvenance(state, "logs_recent_deploy", ToolLogs, "trusted_derived"),
			RawReferences: []evidencev1.RawReference{
				{Kind: "recent_deploy", Value: change},
			},
		})
	}
	return out
}

func buildAdaptiveBaselineEvidence(state *workflowState) []evidencev1.Record {
	if len(state.adaptiveBaselines) == 0 {
		return nil
	}
	out := make([]evidencev1.Record, 0, len(state.adaptiveBaselines))
	for _, insight := range state.adaptiveBaselines {
		out = append(out, evidencev1.Record{
			ID:         fmt.Sprintf("ev-adaptive-%s", sanitizeID(insight.Dimension+"-"+insight.Metric)),
			Kind:       "adaptive_baseline",
			Category:   "baseline",
			Summary:    insight.Explanation,
			Severity:   severityFromDelta(insight.DeltaPercent),
			Confidence: clamp01(absFloat(insight.DeltaPercent) / 100.0),
			Timestamp:  state.now,
			Subject:    workflowEvidenceSubject(state, "workload", firstNonEmpty(insight.Entity, state.collectorID, "fleet")),
			Attributes: map[string]string{
				"dimension":        insight.Dimension,
				"metric":           insight.Metric,
				"classification":   insight.Classification,
				"workload_class":   insight.WorkloadClass,
				"job":              insight.Job,
				"pod_uid":          insight.PodUID,
				"hardware_profile": insight.HardwareProfile,
			},
			Provenance: workflowEvidenceProvenance(state, "adaptive_baseline", ToolMetrics, "trusted_derived"),
		})
	}
	return out
}

func buildBehavioralAssessmentEvidence(state *workflowState) []evidencev1.Record {
	if state == nil || len(state.behavioralAssessments) == 0 {
		return nil
	}
	out := make([]evidencev1.Record, 0, len(state.behavioralAssessments))
	for _, assessment := range state.behavioralAssessments {
		if strings.TrimSpace(assessment.SignalID) == "" || strings.TrimSpace(assessment.Classification) == "" {
			continue
		}
		severity := "medium"
		switch assessment.Classification {
		case "expected_recurring_burst":
			severity = "info"
		case "correlated_anomaly":
			severity = "medium"
		case "confirmed_anomaly":
			severity = "high"
		}
		out = append(out, evidencev1.Record{
			ID:         fmt.Sprintf("ev-behavior-%s", sanitizeID(assessment.SignalID+"-"+assessment.EntityKey)),
			Kind:       "behavioral_memory_decision",
			Category:   "baseline",
			Summary:    assessment.Explanation,
			Severity:   severity,
			Confidence: clamp01(assessment.Confidence),
			Timestamp:  state.now,
			Subject:    workflowEvidenceSubject(state, "workload", firstNonEmpty(assessment.Entity, state.collectorID, "fleet")),
			Attributes: map[string]string{
				"signal_id":          assessment.SignalID,
				"classification":     assessment.Classification,
				"service":            assessment.Service,
				"workload_class":     assessment.WorkloadClass,
				"workload_role":      assessment.WorkloadRole,
				"temporal_bucket":    assessment.TemporalBucket,
				"recurrence_count":   strconv.Itoa(assessment.RecurrenceCount),
				"suppression_factor": fmt.Sprintf("%.2f", assessment.SuppressionFactor),
			},
			Provenance: workflowEvidenceProvenance(state, "behavior_memory", ToolMetrics, "trusted_derived"),
		})
	}
	return out
}

func severityFromDelta(delta float64) string {
	switch {
	case absFloat(delta) >= 60:
		return "high"
	case absFloat(delta) >= 30:
		return "medium"
	default:
		return "low"
	}
}

func buildTopologyEvidence(state *workflowState) []evidencev1.Record {
	snapshot := state.topoData.Snapshot
	if len(snapshot.Nodes) == 0 && len(snapshot.Edges) == 0 && strings.TrimSpace(snapshot.Summary) == "" {
		return nil
	}
	return []evidencev1.Record{{
		ID:         fmt.Sprintf("ev-topology-%s", sanitizeID(firstNonEmpty(state.collectorID, "fleet"))),
		Kind:       "topology_snapshot",
		Category:   "topology",
		Summary:    firstNonEmpty(snapshot.Summary, fmt.Sprintf("topology nodes=%d edges=%d", len(snapshot.Nodes), len(snapshot.Edges))),
		Severity:   "info",
		Confidence: 0.86,
		Timestamp:  firstNonZeroTime(snapshot.GeneratedAt, state.now),
		Subject:    workflowEvidenceSubject(state, "topology", firstNonEmpty(state.collectorID, "fleet")),
		Attributes: map[string]string{
			"node_count": strconv.Itoa(len(snapshot.Nodes)),
			"edge_count": strconv.Itoa(len(snapshot.Edges)),
			"source":     strings.TrimSpace(snapshot.Source),
		},
		Provenance: workflowEvidenceProvenance(state, firstNonEmpty(snapshot.Source, "topology"), ToolTopology, "trusted_structured"),
	}}
}

func buildProcessSnapshotEvidence(state *workflowState) []evidencev1.Record {
	if len(state.lineage.Nodes) == 0 && len(state.lineage.Edges) == 0 && len(state.lineage.Paths) == 0 {
		return nil
	}
	refs := make([]evidencev1.RawReference, 0, minInt(len(state.lineage.Paths), 4))
	for idx, path := range state.lineage.Paths {
		if idx >= 4 {
			break
		}
		refs = append(refs, evidencev1.RawReference{Kind: "lineage_path", Value: path})
	}
	return []evidencev1.Record{{
		ID:         fmt.Sprintf("ev-process-%s", sanitizeID(firstNonEmpty(state.collectorID, "fleet"))),
		Kind:       "process_snapshot",
		Category:   "runtime",
		Summary:    firstNonEmpty(state.lineage.Summary, fmt.Sprintf("process lineage nodes=%d edges=%d", len(state.lineage.Nodes), len(state.lineage.Edges))),
		Severity:   "medium",
		Confidence: 0.78,
		Timestamp:  state.now,
		Subject:    workflowEvidenceSubject(state, "process", firstNonEmpty(state.collectorID, "fleet")),
		Attributes: map[string]string{
			"node_count": strconv.Itoa(len(state.lineage.Nodes)),
			"edge_count": strconv.Itoa(len(state.lineage.Edges)),
			"path_count": strconv.Itoa(len(state.lineage.Paths)),
		},
		Provenance:    workflowEvidenceProvenance(state, "process_lineage", ToolProcessLineage, "trusted_derived"),
		RawReferences: refs,
	}}
}

func buildSecurityEvidence(state *workflowState) []evidencev1.Record {
	if len(state.security.StructuredFindings) == 0 {
		return nil
	}
	out := make([]evidencev1.Record, 0, len(state.security.StructuredFindings))
	for _, finding := range state.security.StructuredFindings {
		id := firstNonEmpty(strings.TrimSpace(finding.EvidenceID), strings.TrimSpace(finding.FindingID))
		if id == "" {
			continue
		}
		attrs := map[string]string{
			"category":           strings.TrimSpace(finding.Category),
			"recommended_action": strings.TrimSpace(finding.RecommendedAction),
		}
		out = append(out, evidencev1.Record{
			ID:         id,
			Kind:       "security_finding",
			Category:   "security",
			Summary:    firstNonEmpty(strings.TrimSpace(finding.Summary), strings.TrimSpace(finding.Description)),
			Severity:   strings.ToLower(strings.TrimSpace(finding.Severity)),
			Confidence: clamp01(finding.Confidence),
			Timestamp:  state.now,
			Subject:    workflowEvidenceSubject(state, firstNonEmpty(strings.TrimSpace(finding.Scope), "security"), firstNonEmpty(state.collectorID, finding.Source)),
			Attributes: attrs,
			Provenance: workflowEvidenceProvenance(state, firstNonEmpty(strings.TrimSpace(finding.Source), "security_check"), ToolSecurity, "trusted_derived"),
			RawReferences: []evidencev1.RawReference{
				{Kind: "finding_id", ID: strings.TrimSpace(finding.FindingID)},
				{Kind: "evidence_id", ID: strings.TrimSpace(finding.EvidenceID)},
			},
		})
	}
	return out
}

func buildRuntimeEventEvidence(state *workflowState) []evidencev1.Record {
	if len(state.ebpf.RuntimeEvents) == 0 {
		return nil
	}
	out := make([]evidencev1.Record, 0, minInt(len(state.ebpf.RuntimeEvents), 12))
	for idx, event := range state.ebpf.RuntimeEvents {
		if idx >= 12 {
			break
		}
		out = append(out, evidencev1.Record{
			ID:         firstNonEmpty(strings.TrimSpace(event.EvidenceID), fmt.Sprintf("ev-ebpf-%02d", idx+1)),
			Kind:       "runtime_event",
			Category:   firstNonEmpty(strings.TrimSpace(event.Category), "runtime"),
			Summary:    firstNonEmpty(strings.TrimSpace(event.Description), fmt.Sprintf("%s %s", event.Category, event.Type)),
			Severity:   strings.ToLower(strings.TrimSpace(event.Severity)),
			Confidence: clamp01(event.Confidence),
			Timestamp:  event.Timestamp,
			Subject:    workflowEvidenceSubject(state, firstNonEmpty(strings.TrimSpace(event.NodeScope), "node"), firstNonEmpty(strings.TrimSpace(event.PID), state.collectorID)),
			Attributes: map[string]string{
				"type":      strings.TrimSpace(event.Type),
				"remote_ip": strings.TrimSpace(event.RemoteIP),
				"path":      strings.TrimSpace(event.Path),
			},
			Provenance: workflowEvidenceProvenance(state, "runtime_security", ToolEBPFQuery, "trusted_runtime"),
			RawReferences: []evidencev1.RawReference{
				{Kind: "evidence_id", ID: strings.TrimSpace(event.EvidenceID)},
				{Kind: "pid", ID: strings.TrimSpace(event.PID)},
			},
		})
	}
	return out
}

func buildGPUEvidence(state *workflowState) []evidencev1.Record {
	if len(state.gpu.Metrics) == 0 && len(state.gpu.TopProcesses) == 0 {
		return nil
	}
	keys := make([]string, 0, len(state.gpu.Metrics))
	for key := range state.gpu.Metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs := map[string]string{
		"top_processes": strings.Join(state.gpu.TopProcesses, ", "),
		"bottleneck":    strings.TrimSpace(state.gpu.Bottleneck),
	}
	for _, key := range keys {
		if len(attrs) >= 8 {
			break
		}
		attrs[key] = fmt.Sprintf("%.2f", state.gpu.Metrics[key])
	}
	return []evidencev1.Record{{
		ID:         fmt.Sprintf("ev-gpu-snapshot-%s", sanitizeID(firstNonEmpty(state.collectorID, "fleet"))),
		Kind:       "gpu_snapshot",
		Category:   "gpu",
		Summary:    firstNonEmpty(state.gpu.Summary, "gpu telemetry snapshot collected"),
		Severity:   severityFromMetricCategory("gpu", maxGPUConfidence(state.gpu.Metrics)),
		Confidence: maxGPUConfidence(state.gpu.Metrics),
		Timestamp:  state.now,
		Subject:    workflowEvidenceSubject(state, "gpu", firstNonEmpty(state.collectorID, "fleet")),
		Attributes: attrs,
		Provenance: workflowEvidenceProvenance(state, "gpu_metrics", ToolGPU, "trusted_structured"),
	}}
}

func buildKnowledgeEvidence(state *workflowState) []evidencev1.Record {
	sets := [][]RetrievedDocumentEvidence{
		state.retrievedDocs,
		state.retrievedCases,
		state.retrievedRunbooks,
		state.similarPatterns,
	}
	out := make([]evidencev1.Record, 0, len(state.retrievedDocs)+len(state.retrievedCases)+len(state.retrievedRunbooks)+len(state.similarPatterns))
	for _, set := range sets {
		for _, hit := range set {
			out = append(out, evidencev1.Record{
				ID:         strings.TrimSpace(hit.EvidenceID),
				Kind:       "knowledge_hit",
				Category:   knowledgeCategory(hit),
				Summary:    firstNonEmpty(strings.TrimSpace(hit.Title), strings.TrimSpace(hit.Summary), strings.TrimSpace(hit.SourcePath)),
				Severity:   "info",
				Confidence: clamp01(hit.Score),
				Timestamp:  state.now,
				Subject:    workflowEvidenceSubject(state, "knowledge_base", strings.TrimSpace(hit.SourcePath)),
				Attributes: map[string]string{
					"source_type":    strings.TrimSpace(hit.SourceType),
					"knowledge_type": strings.TrimSpace(hit.KnowledgeType),
					"case_type":      strings.TrimSpace(hit.CaseType),
					"section_type":   strings.TrimSpace(hit.SectionType),
				},
				Provenance: workflowEvidenceProvenance(state, firstNonEmpty(strings.TrimSpace(hit.SourceType), "knowledge_base"), ToolKnowledge, "external_knowledge"),
				RawReferences: []evidencev1.RawReference{
					{Kind: "doc_id", ID: strings.TrimSpace(hit.DocID)},
					{Kind: "chunk_id", ID: strings.TrimSpace(hit.ChunkID)},
					{Kind: "source_path", Value: strings.TrimSpace(hit.SourcePath)},
				},
				DerivedFrom: dedupeStrings(append([]string(nil), hit.Evidence...)),
			})
		}
	}
	return out
}

func buildProposedActionEvidence(state *workflowState) []evidencev1.Record {
	if len(state.proposedActions) == 0 {
		return nil
	}
	out := make([]evidencev1.Record, 0, len(state.proposedActions))
	for _, action := range state.proposedActions {
		if action == nil {
			continue
		}
		out = append(out, evidencev1.Record{
			ID:         fmt.Sprintf("ev-action-plan-%s", sanitizeID(action.ID)),
			Kind:       "remediation_plan",
			Category:   "remediation",
			Summary:    firstNonEmpty(strings.TrimSpace(action.CommandPreview), strings.TrimSpace(action.Rationale), strings.TrimSpace(action.AuditIntent)),
			Severity:   strings.ToLower(strings.TrimSpace(action.RiskLevel)),
			Status:     strings.TrimSpace(action.Status),
			Confidence: clamp01(action.Confidence),
			Timestamp:  action.ProposedAt,
			Subject:    workflowEvidenceSubject(state, firstNonEmpty(strings.TrimSpace(action.ImpactScope), "remediation"), action.ID),
			Attributes: map[string]string{
				"execution_level":    strings.TrimSpace(action.ExecutionLevel),
				"execution_category": strings.TrimSpace(action.Category),
				"blast_radius":       strings.TrimSpace(action.BlastRadius),
				"approval_required":  strconv.FormatBool(action.ApprovalRequired),
				"policy_status":      strings.TrimSpace(action.Policy.Status),
			},
			Provenance: workflowEvidenceProvenance(state, "workflow_recommendation", ToolRemediation, "controller_generated"),
			RawReferences: []evidencev1.RawReference{
				{Kind: "action_id", ID: action.ID},
				{Kind: "recommendation_id", ID: action.RecommendationID},
			},
			DerivedFrom: append([]string(nil), action.EvidenceIDs...),
		})
	}
	return out
}

func buildRemediationEvidence(state *workflowState) []evidencev1.Record {
	if len(state.planSteps) == 0 {
		return nil
	}
	out := make([]evidencev1.Record, 0, len(state.planSteps))
	for _, step := range state.planSteps {
		if step.Status == "" || step.Status == "planned" {
			continue
		}
		executionCategory := normalizeValidationCategory(step.Query["validation_category"])
		actuatorSafetyTier := normalizeActuatorSafetyTier(step.Query["safety_tier"])
		actionIntent := firstNonEmpty(step.Query["action_intent"], step.Query["action"])
		actionContractID := ""
		blastRadiusEstimate := ""
		rollbackRequired := "false"
		if step.ActionContract != nil {
			actionContractID = strings.TrimSpace(step.ActionContract.ID)
			executionCategory = normalizeValidationCategory(firstNonEmpty(step.ActionContract.ExecutionCategory, executionCategory))
			actuatorSafetyTier = normalizeActuatorSafetyTier(firstNonEmpty(step.ActionContract.ActuatorSafetyTier, actuatorSafetyTier))
			actionIntent = firstNonEmpty(step.ActionContract.Intent, actionIntent)
			if step.ActionContract.BlastRadiusEstimate > 0 {
				blastRadiusEstimate = strconv.Itoa(step.ActionContract.BlastRadiusEstimate)
			}
			rollbackRequired = strconv.FormatBool(step.ActionContract.Rollback.Required)
		}
		out = append(out, evidencev1.Record{
			ID:         fmt.Sprintf("ev-remediation-%s", sanitizeID(step.ID)),
			Kind:       "remediation_execution",
			Category:   "remediation",
			Summary:    fmt.Sprintf("Remediation %s: %s (%s)", step.ID, step.Tool, step.Status),
			Severity:   "info",
			Status:     step.Status,
			Confidence: 0.95,
			Timestamp:  state.now,
			Subject:    workflowEvidenceSubject(state, "remediation", step.ID),
			Attributes: map[string]string{
				"tool":                 string(step.Tool),
				"verified":             strconv.FormatBool(step.Verified),
				"verification_note":    step.VerificationNote,
				"result_summary":       step.ResultSummary,
				"execution_category":   executionCategory,
				"validation_category":  normalizeValidationCategory(firstNonEmpty(step.Query["validation_category"], executionCategory)),
				"actuator_safety_tier": actuatorSafetyTier,
				"action_category": firstNonEmpty(step.Query["action_category"], func() string {
					if step.ActionContract != nil {
						return step.ActionContract.ActionCategory
					}
					return ""
				}()),
				"action_intent":      actionIntent,
				"action_contract_id": actionContractID,
				"proposal_only":      strings.TrimSpace(step.Query["proposal_only"]),
				"execution_eligible": strings.TrimSpace(step.Query["execution_eligible"]),
				"approval_state":     strings.TrimSpace(step.Query["approval_state"]),
				"dry_run":            strings.TrimSpace(step.Query["dry_run"]),
				"target_scope":       strings.TrimSpace(step.Query["scope"]),
				"rollback_status":    strings.TrimSpace(step.Query["rollback_status"]),
				"rollback_summary":   strings.TrimSpace(step.Query["rollback_summary"]),
				"blast_radius":       blastRadiusEstimate,
				"rollback_required":  rollbackRequired,
			},
			Provenance: workflowEvidenceProvenance(state, "plan_act_verify_loop", ToolRemediation, remediationEvidenceTrustClass(step)),
		})
	}
	if state.validationReport.PostActionValidation != nil {
		summary := state.validationReport.PostActionValidation
		severity := "low"
		switch summary.Verdict {
		case ValidationVerdictConfirmed:
			severity = "info"
		case ValidationVerdictPartiallySupported:
			severity = "medium"
		case ValidationVerdictContradicted:
			severity = "high"
		}
		out = append(out, evidencev1.Record{
			ID:         fmt.Sprintf("ev-remediation-effect-%s", sanitizeID(firstNonEmpty(summary.ActionID, state.workflowID))),
			Kind:       "remediation_verification",
			Category:   "verification",
			Summary:    summary.Summary,
			Severity:   severity,
			Confidence: clamp01(maxFloat(state.validationReport.Confidence, state.analysisHandoff.Confidence)),
			Timestamp:  state.now,
			Subject:    workflowEvidenceSubject(state, "remediation", firstNonEmpty(summary.ActionID, state.workflowID)),
			Attributes: map[string]string{
				"execution_category": normalizeValidationCategory(summary.ExecutionCategory),
				"verdict":            string(summary.Verdict),
				"fallback_mode":      strings.TrimSpace(summary.FallbackMode),
				"before_risk":        fmt.Sprintf("%.2f", summary.BeforeRisk),
				"after_risk":         fmt.Sprintf("%.2f", summary.AfterRisk),
				"comparable":         strconv.FormatBool(summary.Comparison != nil && summary.Comparison.Comparable),
				"incomplete":         strconv.FormatBool(summary.Comparison != nil && summary.Comparison.Incomplete),
				"missing_data": func() string {
					if summary.Comparison == nil || len(summary.Comparison.MissingData) == 0 {
						return ""
					}
					return strings.Join(summary.Comparison.MissingData, ",")
				}(),
			},
			Provenance:  workflowEvidenceProvenance(state, "post_action_validation", ToolRemediation, "trusted_derived"),
			DerivedFrom: dedupeStrings(append([]string(nil), append(summary.SupportingEvidenceIDs, summary.ContradictingEvidenceIDs...)...)),
		})
	}
	return out
}

func remediationEvidenceTrustClass(step AgentPlanStep) string {
	switch strings.TrimSpace(step.Status) {
	case "executed", "verified", "partially_verified", "verification_failed", "reverted", "rollback_failed":
		return "trusted_executed"
	default:
		return "trusted_derived"
	}
}

func mapLegacyWorkflowEvidence(state *workflowState) []evidencev1.Record {
	if len(state.evidence) == 0 {
		return nil
	}
	out := make([]evidencev1.Record, 0, len(state.evidence))
	for _, item := range state.evidence {
		record := evidencev1.Record{
			ID:         strings.TrimSpace(item.ID),
			Kind:       strings.TrimSpace(item.Kind),
			Category:   categoryForLegacyEvidence(item),
			Summary:    strings.TrimSpace(item.Summary),
			Severity:   severityForLegacyEvidence(item),
			Confidence: confidenceForLegacyEvidence(item),
			Timestamp:  item.Timestamp,
			Subject:    workflowEvidenceSubject(state, item.Scope, item.Entity),
			MetricName: strings.TrimSpace(item.MetricName),
			Attributes: map[string]string{},
			Provenance: workflowEvidenceProvenance(state, item.Source, toolNameFromSource(item.Source), trustClassForLegacyEvidence(item)),
			RawReferences: []evidencev1.RawReference{
				{Kind: "legacy_evidence_id", ID: strings.TrimSpace(item.ID)},
			},
		}
		if strings.TrimSpace(item.Snippet) != "" {
			record.Attributes["snippet"] = truncateString(item.Snippet, 220)
		}
		if item.Value != 0 {
			record.Value = evidencev1.Float64Ptr(item.Value)
		}
		if item.Baseline != 0 {
			record.Baseline = evidencev1.Float64Ptr(item.Baseline)
		}
		if item.Delta != 0 {
			record.DeltaPercent = evidencev1.Float64Ptr(item.Delta)
		}
		out = append(out, record)
	}
	return out
}

func workflowEvidenceSubject(state *workflowState, scope, entity string) *evidencev1.Subject {
	subject := evidencev1.Subject{
		CollectorID: strings.TrimSpace(state.collectorID),
		Scope:       strings.TrimSpace(scope),
		Entity:      strings.TrimSpace(entity),
	}
	if node := state.metricsData.Node; node != nil {
		subject.Hostname = strings.TrimSpace(firstNonEmpty(node.Hostname, node.Labels["hostname"]))
		subject.Service = strings.TrimSpace(node.Labels["service"])
		subject.Job = strings.TrimSpace(node.Labels["job"])
		subject.Cluster = strings.TrimSpace(node.Labels["cluster"])
		subject.Namespace = strings.TrimSpace(node.Labels["namespace"])
	}
	if subject.CollectorID == "" && subject.Hostname == "" && subject.Service == "" && subject.Job == "" && subject.Scope == "" && subject.Entity == "" {
		return nil
	}
	return &subject
}

func workflowEvidenceProvenance(state *workflowState, source string, tool ToolName, trustClass string) *evidencev1.Provenance {
	provenance := evidencev1.Provenance{
		Source:     strings.TrimSpace(source),
		Tool:       string(tool),
		TrustClass: strings.TrimSpace(trustClass),
		WorkflowID: strings.TrimSpace(state.workflowID),
		TraceID:    strings.TrimSpace(state.workflowID),
	}
	if provenance.Source == "" && provenance.Tool == "" && provenance.WorkflowID == "" {
		return nil
	}
	return &provenance
}

func knowledgeCategory(hit RetrievedDocumentEvidence) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(hit.KnowledgeType), "runbook"):
		return "runbook"
	case strings.EqualFold(strings.TrimSpace(hit.CaseType), "historical_incident"):
		return "historical_incident"
	case strings.EqualFold(strings.TrimSpace(hit.CaseType), "operational_qa"):
		return "operational_qa"
	default:
		return "knowledge"
	}
}

func maxGPUConfidence(metrics map[string]float64) float64 {
	if len(metrics) == 0 {
		return 0.55
	}
	high := 0.55
	for key, value := range metrics {
		lower := strings.ToLower(key)
		score := 0.55
		switch {
		case strings.Contains(lower, "temperature") && value >= 85:
			score = 0.92
		case strings.Contains(lower, "memory") && value >= 90:
			score = 0.88
		case strings.Contains(lower, "utilization") && value >= 90:
			score = 0.82
		case value > 0:
			score = 0.68
		}
		if score > high {
			high = score
		}
	}
	return clamp01(high)
}

func categoryForLegacyEvidence(item RCAEvidence) string {
	switch {
	case strings.Contains(item.Kind, "security"):
		return "security"
	case strings.Contains(item.Kind, "gpu"):
		return "gpu"
	case strings.Contains(item.Kind, "knowledge"):
		return "knowledge"
	case strings.Contains(item.Kind, "log"):
		return "logs"
	case strings.Contains(item.Kind, "correlation"):
		return "correlation"
	case strings.Contains(item.Kind, "signal"), item.MetricName != "":
		return categoryForMetricName(item.MetricName)
	default:
		return "runtime"
	}
}

func severityForLegacyEvidence(item RCAEvidence) string {
	switch item.Kind {
	case "near_baseline_signal":
		return "info"
	case "runtime_security_event":
		return "high"
	}
	if item.Delta >= 60 || item.Value >= 0.9 {
		return "high"
	}
	if item.Delta >= 25 || item.Value >= 0.5 {
		return "medium"
	}
	if item.Kind == "knowledge_retrieval" {
		return "info"
	}
	return "low"
}

func confidenceForLegacyEvidence(item RCAEvidence) float64 {
	switch item.Kind {
	case "knowledge_retrieval":
		return 0.72
	case "log_snippet":
		return 0.58
	case "correlation":
		return 0.74
	case "runtime_security_event":
		return 0.88
	case "near_baseline_signal":
		return 0.22
	}
	if item.Delta > 0 {
		return clamp01(item.Delta / 100)
	}
	if item.Value > 0 {
		return clamp01(item.Value)
	}
	return 0.5
}

func trustClassForLegacyEvidence(item RCAEvidence) string {
	switch item.Kind {
	case "log_snippet":
		return "untrusted_text"
	case "knowledge_retrieval":
		return "external_knowledge"
	case "runtime_security_event":
		return "trusted_runtime"
	default:
		return "trusted_structured"
	}
}

func toolNameFromSource(source string) ToolName {
	switch strings.TrimSpace(source) {
	case "metrics_query":
		return ToolMetrics
	case "logs_query":
		return ToolLogs
	case "knowledge_retrieval":
		return ToolKnowledge
	case "trace_query":
		return ToolEBPFQuery
	case "gpu_query":
		return ToolGPU
	case "joint_risk":
		return ToolRAGQuery
	default:
		return ToolName(strings.TrimSpace(source))
	}
}

func categoryForMetricName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(lower, "gpu"):
		return "gpu"
	case strings.Contains(lower, "disk"), strings.Contains(lower, "io"), strings.Contains(lower, "storage"), strings.Contains(lower, "filesystem"):
		return "storage"
	case strings.Contains(lower, "nic"), strings.Contains(lower, "network"), strings.Contains(lower, "tcp"), strings.Contains(lower, "retrans"):
		return "network"
	case strings.Contains(lower, "memory"), strings.Contains(lower, "swap"), strings.Contains(lower, "reclaim"):
		return "memory"
	case strings.Contains(lower, "cpu"), strings.Contains(lower, "load"), strings.Contains(lower, "sched"):
		return "cpu"
	default:
		return "runtime"
	}
}

func severityFromMetricCategory(category string, confidence float64) string {
	switch {
	case confidence >= 0.9:
		return "high"
	case confidence >= 0.75:
		return "medium"
	case category == "inventory":
		return "info"
	default:
		return "low"
	}
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func maxTime(values ...time.Time) time.Time {
	var max time.Time
	for _, value := range values {
		if value.After(max) {
			max = value
		}
	}
	return max
}
