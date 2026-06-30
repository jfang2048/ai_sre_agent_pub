package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
)

func buildIncidentActionAuditEvidence(action IncidentAutomationAction, audit IncidentActionAuditRecord) []evidencev1.Record {
	record := evidencev1.Record{
		ID:            fmt.Sprintf("ev-incident-action-%s", sanitizeIncidentActionID(audit.AuditID, action.ID)),
		SchemaVersion: evidencev1.SchemaVersionV1,
		Kind:          "remediation_action",
		Category:      "remediation",
		Summary:       firstNonEmpty(strings.TrimSpace(audit.Message), strings.TrimSpace(action.Description), strings.TrimSpace(action.Type)),
		Severity:      firstNonEmpty(strings.ToLower(strings.TrimSpace(action.ExecutionLevel)), "info"),
		Status:        strings.TrimSpace(audit.Status),
		Confidence:    1,
		Timestamp:     firstNonZeroIncidentActionTime(audit.ExecutedAt, time.Now().UTC()),
		Subject: &evidencev1.Subject{
			Scope:  firstNonEmpty(strings.TrimSpace(action.ExecutionLevel), "incident_action"),
			Entity: action.ID,
		},
		Attributes: map[string]string{
			"action_type":            strings.TrimSpace(action.Type),
			"safe":                   strconv.FormatBool(action.Safe),
			"requires_approval":      strconv.FormatBool(action.RequiresApproval),
			"reversible":             strconv.FormatBool(action.Reversible),
			"dry_run":                strconv.FormatBool(audit.DryRun),
			"blast_radius":           strings.TrimSpace(action.BlastRadius),
			"timeout":                strings.TrimSpace(action.Timeout),
			"rollback_state":         strings.TrimSpace(audit.RollbackState),
			"operator_justification": strings.TrimSpace(audit.OperatorJustification),
		},
		Provenance: &evidencev1.Provenance{
			Source:     "incident_action_audit",
			Tool:       "incident_action",
			TrustClass: "operator_gated",
			WorkflowID: strings.TrimSpace(audit.AlertID),
			TraceID:    strings.TrimSpace(audit.AuditID),
		},
		RawReferences: []evidencev1.RawReference{
			{Kind: "audit_id", ID: strings.TrimSpace(audit.AuditID)},
			{Kind: "approval_id", ID: strings.TrimSpace(audit.ApprovalID)},
			{Kind: "rollback_id", ID: strings.TrimSpace(audit.RollbackID)},
		},
		DerivedFrom: append([]string(nil), audit.EvidenceIDs...),
	}
	return []evidencev1.Record{record}
}

func sanitizeIncidentActionID(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		replacer := strings.NewReplacer("/", "-", " ", "-", ":", "-", ".", "-", "_", "-")
		value = replacer.Replace(strings.ToLower(value))
		return strings.Trim(value, "-")
	}
	return "unknown"
}

func firstNonZeroIncidentActionTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
