package agent

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidents"
)

var (
	// ErrIncidentNotFound indicates the incident assessment does not exist.
	ErrIncidentNotFound = errors.New("incident not found")
	// ErrIncidentActionNotFound indicates the requested automation action is missing.
	ErrIncidentActionNotFound = errors.New("incident action not found")
	// ErrIncidentActionApprovalRequired indicates approval token is required.
	ErrIncidentActionApprovalRequired = errors.New("approval token required")
	// ErrIncidentActionApprovalInvalid indicates approval token mismatch.
	ErrIncidentActionApprovalInvalid = errors.New("approval token invalid")
	// ErrIncidentActionApprovalExpired indicates approval receipt is missing or expired.
	ErrIncidentActionApprovalExpired = errors.New("approval receipt expired or not found")
	// ErrIncidentActionNotReversible indicates rollback is not supported for this action.
	ErrIncidentActionNotReversible = errors.New("incident action is not reversible")
	// ErrIncidentRollbackNotFound indicates no rollback candidate exists.
	ErrIncidentRollbackNotFound = errors.New("incident rollback target not found")
)

// IncidentActionExecuteRequest holds execution controls for one incident action.
type IncidentActionExecuteRequest struct {
	DryRun        *bool  `json:"dry_run,omitempty"`
	ApprovalToken string `json:"approval_token,omitempty"`
	ApprovalID    string `json:"approval_id,omitempty"`
}

// IncidentActionRollbackRequest controls rollback behavior.
type IncidentActionRollbackRequest struct {
	DryRun        *bool  `json:"dry_run,omitempty"`
	ApprovalToken string `json:"approval_token,omitempty"`
	ApprovalID    string `json:"approval_id,omitempty"`
	RollbackID    string `json:"rollback_id,omitempty"`
}

// IncidentActionApproveRequest requests an approval receipt for a specific action.
type IncidentActionApproveRequest struct {
	ApprovalToken         string `json:"approval_token,omitempty"`
	OperatorJustification string `json:"operator_justification,omitempty"`
	ApprovedBy            string `json:"approved_by,omitempty"`
}

// IncidentActionApprovalRecord is a bounded in-memory approval receipt.
type IncidentActionApprovalRecord struct {
	ApprovalID            string    `json:"approval_id"`
	AlertID               string    `json:"alert_id"`
	ActionID              string    `json:"action_id"`
	ActionType            string    `json:"action_type"`
	ExecutionLevel        string    `json:"execution_level"`
	OperatorJustification string    `json:"operator_justification,omitempty"`
	ApprovedBy            string    `json:"approved_by,omitempty"`
	BlastRadius           string    `json:"blast_radius,omitempty"`
	EvidenceIDs           []string  `json:"evidence_ids,omitempty"`
	ApprovedAt            time.Time `json:"approved_at"`
	ExpiresAt             time.Time `json:"expires_at"`
	Status                string    `json:"status"`
}

// IncidentActionApproveResult reports the approval receipt details.
type IncidentActionApproveResult struct {
	ApprovalID            string              `json:"approval_id"`
	AlertID               string              `json:"alert_id"`
	ActionID              string              `json:"action_id"`
	ActionType            string              `json:"action_type"`
	EvidenceSchemaVersion string              `json:"evidence_schema_version,omitempty"`
	ExecutionLevel        string              `json:"execution_level"`
	OperatorJustification string              `json:"operator_justification,omitempty"`
	ApprovedBy            string              `json:"approved_by,omitempty"`
	BlastRadius           string              `json:"blast_radius,omitempty"`
	EvidenceIDs           []string            `json:"evidence_ids,omitempty"`
	NormalizedEvidence    []evidencev1.Record `json:"normalized_evidence,omitempty"`
	ApprovedAt            time.Time           `json:"approved_at"`
	ExpiresAt             time.Time           `json:"expires_at"`
	Status                string              `json:"status"`
	AuditID               string              `json:"audit_id,omitempty"`
}

// IncidentActionExecutionResult reports execution status for one incident action.
type IncidentActionExecutionResult struct {
	AlertID               string              `json:"alert_id"`
	ActionID              string              `json:"action_id"`
	ActionType            string              `json:"action_type"`
	EvidenceSchemaVersion string              `json:"evidence_schema_version,omitempty"`
	Status                string              `json:"status"`
	Message               string              `json:"message"`
	DryRun                bool                `json:"dry_run"`
	ExecutionLevel        string              `json:"execution_level,omitempty"`
	Safe                  bool                `json:"safe"`
	RequiresApproval      bool                `json:"requires_approval"`
	Reversible            bool                `json:"reversible"`
	Preconditions         []string            `json:"preconditions,omitempty"`
	BlastRadius           string              `json:"blast_radius,omitempty"`
	IdempotencyNote       string              `json:"idempotency_note,omitempty"`
	Timeout               string              `json:"timeout,omitempty"`
	RollbackPlan          string              `json:"rollback_plan,omitempty"`
	EvidenceIDs           []string            `json:"evidence_ids,omitempty"`
	NormalizedEvidence    []evidencev1.Record `json:"normalized_evidence,omitempty"`
	OperatorJustification string              `json:"operator_justification,omitempty"`
	ApprovalID            string              `json:"approval_id,omitempty"`
	AuditID               string              `json:"audit_id,omitempty"`
	RollbackID            string              `json:"rollback_id,omitempty"`
	RollbackState         string              `json:"rollback_state,omitempty"`
	StartedAt             time.Time           `json:"started_at"`
	CompletedAt           time.Time           `json:"completed_at"`
}

// IncidentActionRollbackResult reports rollback attempt status.
type IncidentActionRollbackResult struct {
	AlertID               string              `json:"alert_id"`
	ActionID              string              `json:"action_id"`
	ActionType            string              `json:"action_type"`
	EvidenceSchemaVersion string              `json:"evidence_schema_version,omitempty"`
	Status                string              `json:"status"`
	Message               string              `json:"message"`
	DryRun                bool                `json:"dry_run"`
	ExecutionLevel        string              `json:"execution_level,omitempty"`
	BlastRadius           string              `json:"blast_radius,omitempty"`
	RollbackPlan          string              `json:"rollback_plan,omitempty"`
	ApprovalID            string              `json:"approval_id,omitempty"`
	RollbackID            string              `json:"rollback_id"`
	NormalizedEvidence    []evidencev1.Record `json:"normalized_evidence,omitempty"`
	AuditID               string              `json:"audit_id,omitempty"`
	StartedAt             time.Time           `json:"started_at"`
	CompletedAt           time.Time           `json:"completed_at"`
}

// IncidentActionAuditRecord is the immutable audit trail entry.
type IncidentActionAuditRecord struct {
	AuditID               string              `json:"audit_id"`
	AlertID               string              `json:"alert_id"`
	ActionID              string              `json:"action_id"`
	ActionType            string              `json:"action_type"`
	EvidenceSchemaVersion string              `json:"evidence_schema_version,omitempty"`
	Status                string              `json:"status"`
	Message               string              `json:"message"`
	DryRun                bool                `json:"dry_run"`
	ExecutionLevel        string              `json:"execution_level,omitempty"`
	Safe                  bool                `json:"safe"`
	RequiresApproval      bool                `json:"requires_approval"`
	Reversible            bool                `json:"reversible"`
	Preconditions         []string            `json:"preconditions,omitempty"`
	BlastRadius           string              `json:"blast_radius,omitempty"`
	IdempotencyNote       string              `json:"idempotency_note,omitempty"`
	Timeout               string              `json:"timeout,omitempty"`
	RollbackPlan          string              `json:"rollback_plan,omitempty"`
	EvidenceIDs           []string            `json:"evidence_ids,omitempty"`
	OperatorJustification string              `json:"operator_justification,omitempty"`
	ApprovalID            string              `json:"approval_id,omitempty"`
	ApprovedBy            string              `json:"approved_by,omitempty"`
	RollbackID            string              `json:"rollback_id,omitempty"`
	RollbackState         string              `json:"rollback_state,omitempty"` // prepared|completed|not_required
	NormalizedEvidence    []evidencev1.Record `json:"normalized_evidence,omitempty"`
	ExecutedAt            time.Time           `json:"executed_at"`
}

const incidentActionApprovalTTL = 30 * time.Minute

// ApproveIncidentAction validates an approval token and mints a bounded approval receipt.
func (e *Engine) ApproveIncidentAction(alertID, actionID string, req IncidentActionApproveRequest) (IncidentActionApproveResult, error) {
	alertID = strings.TrimSpace(alertID)
	actionID = strings.TrimSpace(actionID)
	if alertID == "" {
		return IncidentActionApproveResult{}, ErrIncidentNotFound
	}
	if actionID == "" {
		return IncidentActionApproveResult{}, ErrIncidentActionNotFound
	}

	e.mu.RLock()
	assessment, ok := e.incidentAssessments[alertID]
	if !ok {
		e.mu.RUnlock()
		return IncidentActionApproveResult{}, ErrIncidentNotFound
	}
	action, found := findIncidentAutomationAction(assessment.AutomationPlan.Actions, actionID)
	e.mu.RUnlock()
	if !found {
		return IncidentActionApproveResult{}, ErrIncidentActionNotFound
	}
	if err := validateIncidentApprovalToken(req.ApprovalToken); err != nil {
		return IncidentActionApproveResult{}, err
	}

	now := time.Now().UTC()
	record := IncidentActionApprovalRecord{
		ApprovalID:            fmt.Sprintf("incident-approval-%s-%d", action.ID, now.UnixNano()),
		AlertID:               alertID,
		ActionID:              action.ID,
		ActionType:            action.Type,
		ExecutionLevel:        firstNonEmpty(strings.TrimSpace(action.ExecutionLevel), incidentActionExecutionLevel(action)),
		OperatorJustification: strings.TrimSpace(req.OperatorJustification),
		ApprovedBy:            strings.TrimSpace(req.ApprovedBy),
		BlastRadius:           action.BlastRadius,
		EvidenceIDs:           append([]string(nil), action.EvidenceIDs...),
		ApprovedAt:            now,
		ExpiresAt:             now.Add(incidentActionApprovalTTL),
		Status:                "approved",
	}
	audit := e.recordIncidentActionApproval(alertID, action, record)
	return IncidentActionApproveResult{
		ApprovalID:            record.ApprovalID,
		AlertID:               record.AlertID,
		ActionID:              record.ActionID,
		ActionType:            record.ActionType,
		EvidenceSchemaVersion: audit.EvidenceSchemaVersion,
		ExecutionLevel:        record.ExecutionLevel,
		OperatorJustification: record.OperatorJustification,
		ApprovedBy:            record.ApprovedBy,
		BlastRadius:           record.BlastRadius,
		EvidenceIDs:           append([]string(nil), record.EvidenceIDs...),
		NormalizedEvidence:    evidencev1.CloneRecords(audit.NormalizedEvidence),
		ApprovedAt:            record.ApprovedAt,
		ExpiresAt:             record.ExpiresAt,
		Status:                record.Status,
		AuditID:               audit.AuditID,
	}, nil
}

// ExecuteIncidentAction runs a guarded automation action for an incident.
func (e *Engine) ExecuteIncidentAction(alertID, actionID string, req IncidentActionExecuteRequest) (IncidentActionExecutionResult, error) {
	alertID = strings.TrimSpace(alertID)
	actionID = strings.TrimSpace(actionID)
	if alertID == "" {
		return IncidentActionExecutionResult{}, ErrIncidentNotFound
	}
	if actionID == "" {
		return IncidentActionExecutionResult{}, ErrIncidentActionNotFound
	}

	e.mu.RLock()
	assessment, ok := e.incidentAssessments[alertID]
	if !ok {
		e.mu.RUnlock()
		return IncidentActionExecutionResult{}, ErrIncidentNotFound
	}
	ctx := e.incidentContexts[alertID]
	action, found := findIncidentAutomationAction(assessment.AutomationPlan.Actions, actionID)
	e.mu.RUnlock()
	if !found {
		return IncidentActionExecutionResult{}, ErrIncidentActionNotFound
	}

	effectiveDryRun := action.DryRunDefault
	if req.DryRun != nil {
		effectiveDryRun = *req.DryRun
	}

	approval, err := e.resolveIncidentApproval(alertID, action, effectiveDryRun, req.ApprovalToken, req.ApprovalID)
	if err != nil {
		return IncidentActionExecutionResult{}, err
	}

	started := time.Now().UTC()
	status := "executed"
	message := ""

	if effectiveDryRun {
		status = "dry_run"
		message = "dry-run: " + action.Description
	} else {
		switch action.Type {
		case "diagnostic_check_metrics":
			message = executeMetricDiagnostic(ctx)
		case "diagnostic_check_logs":
			message = executeLogDiagnostic(ctx)
		case "diagnostic_check_kubernetes":
			message = executeKubernetesDiagnostic(ctx)
		case "diagnostic_rollout_health":
			message = executeRolloutDiagnostic(ctx)
		case "diagnostic_node_pressure":
			message = executeNodePressureDiagnostic(ctx)
		case "incident_bridge_checklist":
			message = "incident bridge checklist prepared: assign incident commander, communications lead, and mitigation owner"
		case "targeted_restart_candidate":
			status = "blocked"
			message = "targeted restart remains manual despite approval; review runbook and execute via controlled change process"
		case "restart_pod_controlled":
			if !incidentBoolEnv("SRE_AGENT_K8S_REMEDIATION_ENABLED", false) {
				status = "blocked"
				message = "controlled remediation disabled; set SRE_AGENT_K8S_REMEDIATION_ENABLED=true to allow approved execution"
			} else {
				message = executeControlledPodRestart(ctx)
			}
		default:
			status = "failed"
			message = "unsupported action type: " + action.Type
		}
	}

	completed := time.Now().UTC()
	result := IncidentActionExecutionResult{
		AlertID:               alertID,
		ActionID:              action.ID,
		ActionType:            action.Type,
		Status:                status,
		Message:               message,
		DryRun:                effectiveDryRun,
		ExecutionLevel:        firstNonEmpty(strings.TrimSpace(action.ExecutionLevel), incidentActionExecutionLevel(action)),
		Safe:                  action.Safe,
		RequiresApproval:      action.RequiresApproval,
		Reversible:            action.Reversible,
		Preconditions:         append([]string(nil), action.Preconditions...),
		BlastRadius:           action.BlastRadius,
		IdempotencyNote:       action.IdempotencyNote,
		Timeout:               action.Timeout,
		RollbackPlan:          action.RollbackPlan,
		EvidenceIDs:           append([]string(nil), action.EvidenceIDs...),
		OperatorJustification: firstNonEmpty(approval.OperatorJustification, action.OperatorJustification),
		ApprovalID:            approval.ApprovalID,
		StartedAt:             started,
		CompletedAt:           completed,
	}
	audit := e.recordIncidentActionResult(alertID, action, status, message, effectiveDryRun, completed, "", approval)
	result.AuditID = audit.AuditID
	result.RollbackID = audit.RollbackID
	result.RollbackState = audit.RollbackState
	result.EvidenceSchemaVersion = audit.EvidenceSchemaVersion
	result.NormalizedEvidence = evidencev1.CloneRecords(audit.NormalizedEvidence)
	return result, nil
}

func executeMetricDiagnostic(ctx incidents.AggregatedContext) string {
	symptoms := 0
	for _, metric := range ctx.Metrics {
		symptoms += len(metric.Symptoms)
	}
	return fmt.Sprintf("metric diagnostic complete: metric_scopes=%d symptoms=%d", len(ctx.Metrics), symptoms)
}

func executeLogDiagnostic(ctx incidents.AggregatedContext) string {
	matches := 0
	for _, logs := range ctx.Logs {
		matches += len(logs.Matches)
	}
	return fmt.Sprintf("log diagnostic complete: log_scopes=%d grouped_matches=%d", len(ctx.Logs), matches)
}

func executeKubernetesDiagnostic(ctx incidents.AggregatedContext) string {
	if ctx.Kubernetes == nil {
		return "kubernetes diagnostic skipped: no kubernetes context in incident scope"
	}
	return fmt.Sprintf(
		"kubernetes diagnostic complete: namespace=%s workloads=%d nodes=%d",
		ctx.Kubernetes.Namespace,
		len(ctx.Kubernetes.Workloads),
		len(ctx.Kubernetes.Nodes),
	)
}

func executeRolloutDiagnostic(ctx incidents.AggregatedContext) string {
	if ctx.Kubernetes == nil {
		return "rollout diagnostic skipped: no kubernetes context in incident scope"
	}
	return fmt.Sprintf(
		"rollout diagnostic complete: namespace=%s tracked_workloads=%d",
		ctx.Kubernetes.Namespace,
		len(ctx.Kubernetes.Workloads),
	)
}

func executeNodePressureDiagnostic(ctx incidents.AggregatedContext) string {
	if ctx.Kubernetes == nil {
		return "node pressure diagnostic skipped: no kubernetes context in incident scope"
	}
	return fmt.Sprintf(
		"node pressure diagnostic complete: scoped_nodes=%d",
		len(ctx.Kubernetes.Nodes),
	)
}

func executeControlledPodRestart(ctx incidents.AggregatedContext) string {
	namespace := "default"
	if ctx.Kubernetes != nil && strings.TrimSpace(ctx.Kubernetes.Namespace) != "" {
		namespace = strings.TrimSpace(ctx.Kubernetes.Namespace)
	}
	target := "incident-scoped-workload"
	for _, resource := range ctx.ResourceScope {
		name := strings.TrimSpace(resource.Name)
		if name != "" {
			target = name
			break
		}
	}
	return fmt.Sprintf(
		"controlled remediation executed: restart request prepared for namespace=%s target=%s",
		namespace,
		target,
	)
}

func findIncidentAutomationAction(actions []IncidentAutomationAction, actionID string) (IncidentAutomationAction, bool) {
	for _, action := range actions {
		if action.ID == actionID {
			return action, true
		}
	}
	return IncidentAutomationAction{}, false
}

func validateIncidentApproval(requiresApproval, dryRun bool, approvalToken string) error {
	if !requiresApproval || dryRun {
		return nil
	}
	return validateIncidentApprovalToken(approvalToken)
}

func validateIncidentApprovalToken(approvalToken string) error {
	expected := strings.TrimSpace(os.Getenv("SRE_AGENT_APPROVAL_TOKEN"))
	provided := strings.TrimSpace(approvalToken)
	if expected == "" || provided == "" {
		return ErrIncidentActionApprovalRequired
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		return ErrIncidentActionApprovalInvalid
	}
	return nil
}

// IncidentActionAudits returns execution audit records for one incident.
func (e *Engine) IncidentActionAudits(alertID string, limit int) []IncidentActionAuditRecord {
	e.mu.RLock()
	records := append([]IncidentActionAuditRecord(nil), e.incidentActionAudits[alertID]...)
	e.mu.RUnlock()
	for i := range records {
		records[i].NormalizedEvidence = evidencev1.CloneRecords(records[i].NormalizedEvidence)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].ExecutedAt.After(records[j].ExecutedAt)
	})
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records
}

// RollbackIncidentAction executes a guarded rollback for the latest reversible action.
func (e *Engine) RollbackIncidentAction(alertID, actionID string, req IncidentActionRollbackRequest) (IncidentActionRollbackResult, error) {
	alertID = strings.TrimSpace(alertID)
	actionID = strings.TrimSpace(actionID)
	if alertID == "" {
		return IncidentActionRollbackResult{}, ErrIncidentNotFound
	}
	if actionID == "" {
		return IncidentActionRollbackResult{}, ErrIncidentActionNotFound
	}

	e.mu.RLock()
	assessment, ok := e.incidentAssessments[alertID]
	if !ok {
		e.mu.RUnlock()
		return IncidentActionRollbackResult{}, ErrIncidentNotFound
	}
	action, found := findIncidentAutomationAction(assessment.AutomationPlan.Actions, actionID)
	audits := append([]IncidentActionAuditRecord(nil), e.incidentActionAudits[alertID]...)
	e.mu.RUnlock()
	if !found {
		return IncidentActionRollbackResult{}, ErrIncidentActionNotFound
	}
	if !action.Reversible {
		return IncidentActionRollbackResult{}, ErrIncidentActionNotReversible
	}

	effectiveDryRun := true
	if req.DryRun != nil {
		effectiveDryRun = *req.DryRun
	}
	approval, err := e.resolveIncidentApproval(alertID, action, effectiveDryRun, req.ApprovalToken, req.ApprovalID)
	if err != nil {
		return IncidentActionRollbackResult{}, err
	}

	targetAudit, ok := selectRollbackCandidate(audits, actionID, req.RollbackID)
	if !ok || strings.TrimSpace(targetAudit.RollbackID) == "" {
		return IncidentActionRollbackResult{}, ErrIncidentRollbackNotFound
	}

	started := time.Now().UTC()
	status := "rolled_back"
	message := "rollback completed for " + action.Type
	if effectiveDryRun {
		status = "dry_run"
		message = "dry-run rollback prepared for " + action.Type
	}
	if !effectiveDryRun && action.Type == "restart_pod_controlled" && !incidentBoolEnv("SRE_AGENT_K8S_REMEDIATION_ENABLED", false) {
		status = "blocked"
		message = "rollback blocked: controlled remediation is disabled in current runtime"
	}
	completed := time.Now().UTC()

	audit := e.recordIncidentActionResult(
		alertID,
		action,
		status,
		message,
		effectiveDryRun,
		completed,
		targetAudit.RollbackID,
		approval,
	)

	return IncidentActionRollbackResult{
		AlertID:               alertID,
		ActionID:              action.ID,
		ActionType:            action.Type,
		EvidenceSchemaVersion: audit.EvidenceSchemaVersion,
		Status:                status,
		Message:               message,
		DryRun:                effectiveDryRun,
		ExecutionLevel:        firstNonEmpty(strings.TrimSpace(action.ExecutionLevel), incidentActionExecutionLevel(action)),
		BlastRadius:           action.BlastRadius,
		RollbackPlan:          action.RollbackPlan,
		ApprovalID:            approval.ApprovalID,
		RollbackID:            targetAudit.RollbackID,
		NormalizedEvidence:    evidencev1.CloneRecords(audit.NormalizedEvidence),
		AuditID:               audit.AuditID,
		StartedAt:             started,
		CompletedAt:           completed,
	}, nil
}

func selectRollbackCandidate(records []IncidentActionAuditRecord, actionID, rollbackID string) (IncidentActionAuditRecord, bool) {
	rollbackID = strings.TrimSpace(rollbackID)
	if rollbackID != "" {
		for _, record := range records {
			if record.ActionID == actionID && record.RollbackID == rollbackID {
				return record, true
			}
		}
		return IncidentActionAuditRecord{}, false
	}
	var latest IncidentActionAuditRecord
	found := false
	for _, record := range records {
		if record.ActionID != actionID || strings.TrimSpace(record.RollbackID) == "" {
			continue
		}
		if !found || record.ExecutedAt.After(latest.ExecutedAt) {
			latest = record
			found = true
		}
	}
	return latest, found
}

func incidentBoolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func incidentActionExecutionLevel(action IncidentAutomationAction) string {
	if action.RequiresApproval || !action.Safe {
		return "approval_required"
	}
	if action.DryRunDefault {
		return "dry_run"
	}
	return "auto_execute"
}

func (e *Engine) resolveIncidentApproval(
	alertID string,
	action IncidentAutomationAction,
	dryRun bool,
	approvalToken string,
	approvalID string,
) (IncidentActionApprovalRecord, error) {
	if !action.RequiresApproval || dryRun {
		return IncidentActionApprovalRecord{}, nil
	}
	if approval, ok := e.lookupIncidentApproval(alertID, action.ID, approvalID); ok {
		return approval, nil
	}
	if strings.TrimSpace(approvalID) != "" {
		return IncidentActionApprovalRecord{}, ErrIncidentActionApprovalExpired
	}
	if err := validateIncidentApproval(action.RequiresApproval, dryRun, approvalToken); err != nil {
		return IncidentActionApprovalRecord{}, err
	}
	return IncidentActionApprovalRecord{}, nil
}

func (e *Engine) lookupIncidentApproval(alertID, actionID, approvalID string) (IncidentActionApprovalRecord, bool) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return IncidentActionApprovalRecord{}, false
	}
	now := time.Now().UTC()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.incidentActionApprovals == nil {
		e.incidentActionApprovals = make(map[string]IncidentActionApprovalRecord)
	}
	record, ok := e.incidentActionApprovals[approvalID]
	if !ok {
		return IncidentActionApprovalRecord{}, false
	}
	if record.AlertID != alertID || record.ActionID != actionID || record.ExpiresAt.Before(now) || record.Status != "approved" {
		delete(e.incidentActionApprovals, approvalID)
		return IncidentActionApprovalRecord{}, false
	}
	return record, true
}

func (e *Engine) recordIncidentActionResult(
	alertID string,
	action IncidentAutomationAction,
	status string,
	message string,
	dryRun bool,
	executedAt time.Time,
	rollbackID string,
	approval IncidentActionApprovalRecord,
) IncidentActionAuditRecord {
	e.mu.Lock()
	defer e.mu.Unlock()

	assessment, ok := e.incidentAssessments[alertID]
	if !ok {
		return IncidentActionAuditRecord{}
	}
	if e.incidentActionAudits == nil {
		e.incidentActionAudits = make(map[string][]IncidentActionAuditRecord)
	}

	rollbackState := "not_required"
	if action.Reversible {
		if strings.TrimSpace(rollbackID) == "" {
			rollbackID = fmt.Sprintf("rollback-%s-%d", action.ID, executedAt.UnixNano())
		}
		rollbackState = "prepared"
		if status == "rolled_back" {
			rollbackState = "completed"
		}
		if status == "blocked" || status == "failed" {
			rollbackState = "prepared"
		}
	}
	if strings.HasPrefix(status, "rolled_back") {
		rollbackState = "completed"
	}
	audit := IncidentActionAuditRecord{
		AuditID:               fmt.Sprintf("audit-%s-%d", action.ID, executedAt.UnixNano()),
		AlertID:               alertID,
		ActionID:              action.ID,
		ActionType:            action.Type,
		EvidenceSchemaVersion: evidencev1.SchemaVersionV1,
		Status:                status,
		Message:               message,
		DryRun:                dryRun,
		ExecutionLevel:        firstNonEmpty(strings.TrimSpace(action.ExecutionLevel), incidentActionExecutionLevel(action)),
		Safe:                  action.Safe,
		RequiresApproval:      action.RequiresApproval,
		Reversible:            action.Reversible,
		Preconditions:         append([]string(nil), action.Preconditions...),
		BlastRadius:           action.BlastRadius,
		IdempotencyNote:       action.IdempotencyNote,
		Timeout:               action.Timeout,
		RollbackPlan:          action.RollbackPlan,
		EvidenceIDs:           append([]string(nil), action.EvidenceIDs...),
		OperatorJustification: firstNonEmpty(approval.OperatorJustification, action.OperatorJustification),
		ApprovalID:            approval.ApprovalID,
		ApprovedBy:            approval.ApprovedBy,
		RollbackID:            rollbackID,
		RollbackState:         rollbackState,
		ExecutedAt:            executedAt,
	}
	audit.NormalizedEvidence = buildIncidentActionAuditEvidence(action, audit)
	switch {
	case dryRun:
		e.actionDryRunTotal++
	case status == "blocked":
		e.actionBlockedTotal++
	case status == "executed" || status == "rolled_back":
		e.actionExecuteTotal++
	}
	e.incidentActionAudits[alertID] = append(e.incidentActionAudits[alertID], audit)
	if len(e.incidentActionAudits[alertID]) > 200 {
		e.incidentActionAudits[alertID] = e.incidentActionAudits[alertID][len(e.incidentActionAudits[alertID])-200:]
	}

	for i := range assessment.AutomationPlan.Actions {
		if assessment.AutomationPlan.Actions[i].ID != action.ID {
			continue
		}
		assessment.AutomationPlan.Actions[i].LastStatus = status
		assessment.AutomationPlan.Actions[i].LastMessage = message
		execTime := executedAt
		assessment.AutomationPlan.Actions[i].LastExecutedAt = &execTime
		break
	}
	e.incidentAssessments[alertID] = assessment
	return audit
}

func (e *Engine) recordIncidentActionApproval(
	alertID string,
	action IncidentAutomationAction,
	approval IncidentActionApprovalRecord,
) IncidentActionAuditRecord {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.incidentActionAudits == nil {
		e.incidentActionAudits = make(map[string][]IncidentActionAuditRecord)
	}
	if e.incidentActionApprovals == nil {
		e.incidentActionApprovals = make(map[string]IncidentActionApprovalRecord)
	}
	e.incidentActionApprovals[approval.ApprovalID] = approval

	audit := IncidentActionAuditRecord{
		AuditID:               fmt.Sprintf("audit-%s-approval-%d", action.ID, approval.ApprovedAt.UnixNano()),
		AlertID:               alertID,
		ActionID:              action.ID,
		ActionType:            action.Type,
		EvidenceSchemaVersion: evidencev1.SchemaVersionV1,
		Status:                "approved",
		Message:               firstNonEmpty(approval.OperatorJustification, "approval receipt issued"),
		DryRun:                false,
		ExecutionLevel:        firstNonEmpty(strings.TrimSpace(action.ExecutionLevel), incidentActionExecutionLevel(action)),
		Safe:                  action.Safe,
		RequiresApproval:      action.RequiresApproval,
		Reversible:            action.Reversible,
		Preconditions:         append([]string(nil), action.Preconditions...),
		BlastRadius:           action.BlastRadius,
		IdempotencyNote:       action.IdempotencyNote,
		Timeout:               action.Timeout,
		RollbackPlan:          action.RollbackPlan,
		EvidenceIDs:           append([]string(nil), action.EvidenceIDs...),
		OperatorJustification: approval.OperatorJustification,
		ApprovalID:            approval.ApprovalID,
		ApprovedBy:            approval.ApprovedBy,
		RollbackState:         "prepared",
		ExecutedAt:            approval.ApprovedAt,
	}
	audit.NormalizedEvidence = buildIncidentActionAuditEvidence(action, audit)
	e.incidentActionAudits[alertID] = append(e.incidentActionAudits[alertID], audit)
	if len(e.incidentActionAudits[alertID]) > 200 {
		e.incidentActionAudits[alertID] = e.incidentActionAudits[alertID][len(e.incidentActionAudits[alertID])-200:]
	}
	return audit
}
