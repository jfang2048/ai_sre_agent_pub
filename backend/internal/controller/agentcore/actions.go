package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

const (
	// ActionResultExecuted indicates an action was executed.
	ActionResultExecuted = "executed"
	// ActionResultDryRun indicates action execution was skipped because dry-run is enabled.
	ActionResultDryRun = "dry_run"
	// ActionResultSkipped indicates action execution was skipped by policy or idempotency.
	ActionResultSkipped = "skipped"
	// ActionResultFailed indicates action execution failed.
	ActionResultFailed = "failed"
	// ActionResultBlocked indicates action execution was blocked by policy or logic.
	ActionResultBlocked = "blocked"
)

// RunnerConfig controls how playbook actions are evaluated and executed.
type RunnerConfig struct {
	PlaybookFile         string
	DryRun               bool
	AllowUnsafe          bool
	DefaultNamespace     string
	AllowedNamespaces    []string
	AllowedShellCommands []string
	IdempotencyTTL       time.Duration
	MaxParallelActions   int
	ActionTimeout        time.Duration
}

// ActionSpec describes one remediating action.
type ActionSpec struct {
	ID               string            `json:"id" yaml:"id"`
	Type             string            `json:"type" yaml:"type"`
	NodeName         string            `json:"node_name,omitempty" yaml:"node_name,omitempty"`
	Target           string            `json:"target,omitempty" yaml:"target,omitempty"`
	Description      string            `json:"description,omitempty" yaml:"description,omitempty"`
	Command          string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args             []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Namespace        string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Resource         string            `json:"resource,omitempty" yaml:"resource,omitempty"`
	Name             string            `json:"name,omitempty" yaml:"name,omitempty"`
	Priority         string            `json:"priority,omitempty" yaml:"priority,omitempty"`
	Safe             bool              `json:"safe" yaml:"safe"`
	RequiresApproval bool              `json:"requires_approval,omitempty" yaml:"requires_approval,omitempty"`
	RollbackCommand  string            `json:"rollback_command,omitempty" yaml:"rollback_command,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	ApprovalRequired bool              `json:"approval_required,omitempty" yaml:"-"`
	ApprovalToken    string            `json:"approval_token,omitempty" yaml:"-"`
	ExpiresAt        time.Time         `json:"expires_at,omitempty" yaml:"-"`
}

// ActionResult captures the execution outcome for one action.
type ActionResult struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`
	Status          string                 `json:"status"`
	Output          string                 `json:"output,omitempty"`
	Error           string                 `json:"error,omitempty"`
	DryRun          bool                   `json:"dry_run"`
	IdempotencyKey  string                 `json:"idempotency_key"`
	IsRollback      bool                   `json:"is_rollback"`
	OperatorHandoff *OperatorActionHandoff `json:"operator_handoff,omitempty"`
	StartedAt       time.Time              `json:"started_at"`
	CompletedAt     time.Time              `json:"completed_at"`
}

// OperatorActionHandoff records what a human must decide and verify. It is a
// proposal, never an approval or evidence that a change took place.
type OperatorActionHandoff struct {
	Status             string   `json:"status,omitempty"`
	Proposal           string   `json:"proposal"`
	Scope              string   `json:"scope,omitempty"`
	Preconditions      []string `json:"preconditions,omitempty"`
	ExpectedImpact     string   `json:"expected_impact,omitempty"`
	BlastRadius        []string `json:"blast_radius,omitempty"`
	RollbackPlan       string   `json:"rollback_plan,omitempty"`
	Preflight          []string `json:"preflight"`
	StopConditions     []string `json:"stop_conditions,omitempty"`
	Verification       []string `json:"verification"`
	MissingInformation []string `json:"missing_information,omitempty"`
}

var errHumanOperatorRequired = errors.New("human operator required")

// Playbook defines a metric-based condition with one or more remediation actions.
type Playbook struct {
	ID         string              `yaml:"id"`
	Summary    string              `yaml:"summary"`
	Severity   string              `yaml:"severity"`
	Conditions []PlaybookCondition `yaml:"conditions"`
	Actions    []ActionSpec        `yaml:"actions"`
}

// PlaybookCondition is a single metric predicate.
type PlaybookCondition struct {
	Metric    string  `yaml:"metric"`
	Op        string  `yaml:"op"`
	Threshold float64 `yaml:"threshold"`
}

type playbookFile struct {
	Version   string     `yaml:"version"`
	Playbooks []Playbook `yaml:"playbooks"`
}

type idempotencyRecord struct {
	Result    ActionResult
	ExpiresAt time.Time
}

// PlaybookRunner proposes actions from telemetry and executes approved ones.
type PlaybookRunner struct {
	cfg               RunnerConfig
	logger            *zap.Logger
	playbooks         []Playbook
	allowedNamespaces map[string]struct{}
	allowedShell      map[string]struct{}

	mu       sync.RWMutex
	executed map[string]idempotencyRecord
}

// DefaultRunnerConfig returns a conservative default execution policy.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		PlaybookFile:         "./configs/agent_playbooks.yaml",
		DryRun:               true,
		AllowUnsafe:          false,
		DefaultNamespace:     "default",
		AllowedNamespaces:    []string{"default"},
		AllowedShellCommands: []string{"echo"},
		IdempotencyTTL:       30 * time.Minute,
		MaxParallelActions:   4,
		ActionTimeout:        15 * time.Second,
	}
}

// NewPlaybookRunner creates a new playbook runner and loads configured playbooks.
func NewPlaybookRunner(cfg RunnerConfig, logger *zap.Logger) (*PlaybookRunner, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.IdempotencyTTL <= 0 {
		cfg.IdempotencyTTL = 30 * time.Minute
	}
	if cfg.DefaultNamespace == "" {
		cfg.DefaultNamespace = "default"
	}
	if cfg.MaxParallelActions <= 0 {
		cfg.MaxParallelActions = 4
	}
	if cfg.ActionTimeout <= 0 {
		cfg.ActionTimeout = 15 * time.Second
	}

	runner := &PlaybookRunner{
		cfg:               cfg,
		logger:            logger.With(zap.String("component", "agent_playbook_runner")),
		allowedNamespaces: toSet(cfg.AllowedNamespaces),
		allowedShell:      toSet(cfg.AllowedShellCommands),
		executed:          make(map[string]idempotencyRecord),
	}
	if len(runner.allowedNamespaces) == 0 {
		runner.allowedNamespaces[cfg.DefaultNamespace] = struct{}{}
	}
	if len(runner.allowedShell) == 0 {
		runner.allowedShell["echo"] = struct{}{}
	}

	playbooks, err := LoadPlaybooks(cfg.PlaybookFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load playbooks: %w", err)
	}
	if err == nil {
		runner.playbooks = playbooks
	}
	return runner, nil
}

// Reload updates runtime-safe playbook runner settings and reloads playbooks from disk.
func (r *PlaybookRunner) Reload(cfg RunnerConfig) error {
	if r == nil {
		return nil
	}
	if cfg.IdempotencyTTL <= 0 {
		cfg.IdempotencyTTL = 30 * time.Minute
	}
	if cfg.DefaultNamespace == "" {
		cfg.DefaultNamespace = "default"
	}
	if cfg.MaxParallelActions <= 0 {
		cfg.MaxParallelActions = 4
	}
	if cfg.ActionTimeout <= 0 {
		cfg.ActionTimeout = 15 * time.Second
	}

	playbooks, err := LoadPlaybooks(cfg.PlaybookFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load playbooks: %w", err)
	}
	if len(cfg.AllowedNamespaces) == 0 {
		cfg.AllowedNamespaces = []string{cfg.DefaultNamespace}
	}
	if len(cfg.AllowedShellCommands) == 0 {
		cfg.AllowedShellCommands = []string{"echo"}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
	r.playbooks = playbooks
	r.allowedNamespaces = toSet(cfg.AllowedNamespaces)
	r.allowedShell = toSet(cfg.AllowedShellCommands)
	return nil
}

// LoadPlaybooks supports both legacy array format and wrapped playbook documents.
func LoadPlaybooks(path string) ([]Playbook, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var wrapped playbookFile
	if err := yaml.Unmarshal(data, &wrapped); err == nil && len(wrapped.Playbooks) > 0 {
		return normalizePlaybooks(wrapped.Playbooks), nil
	}

	var list []Playbook
	if err := yaml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse playbooks: %w", err)
	}
	return normalizePlaybooks(list), nil
}

// ProposeFromMetrics evaluates playbooks against current metrics.
func (r *PlaybookRunner) ProposeFromMetrics(nodeName string, metrics map[string]float64) []ActionSpec {
	r.mu.RLock()
	playbooks := append([]Playbook(nil), r.playbooks...)
	r.mu.RUnlock()

	out := make([]ActionSpec, 0, len(playbooks))
	for _, pb := range playbooks {
		if !playbookMatches(pb, metrics) {
			continue
		}
		for i, action := range pb.Actions {
			candidate := normalizeAction(action)
			candidate.NodeName = nodeName
			if candidate.ID == "" {
				candidate.ID = fmt.Sprintf("%s-%d", sanitizeID(pb.ID), i+1)
			}
			if candidate.Description == "" {
				candidate.Description = pb.Summary
			}
			if candidate.Priority == "" {
				candidate.Priority = pb.Severity
			}
			out = append(out, candidate)
		}
	}
	return dedupeActionSpecs(out)
}

// Execute runs actions concurrently while enforcing idempotency and policy checks.
func (r *PlaybookRunner) Execute(ctx context.Context, actions []ActionSpec, forceDryRun bool) []ActionResult {
	if len(actions) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	results := make([]ActionResult, len(actions))
	var wg sync.WaitGroup
	sem := make(chan struct{}, r.cfg.MaxParallelActions)

	for i := range actions {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = r.executeOne(ctx, actions[idx], forceDryRun)
		}(i)
	}
	wg.Wait()
	return results
}

func (r *PlaybookRunner) executeOne(ctx context.Context, raw ActionSpec, forceDryRun bool) ActionResult {
	action := normalizeAction(raw)
	start := time.Now().UTC()
	key := idempotencyKey(action)

	if prev, ok := r.previousResult(key); ok {
		prev.Status = ActionResultSkipped
		prev.Output = "action already executed for this idempotency key"
		return prev
	}

	if err := r.authorize(action); err != nil {
		return buildActionResult(action, key, start, ActionResultBlocked, "", err, true)
	}

	if forceDryRun || r.cfg.DryRun {
		result := buildActionResult(action, key, start, ActionResultDryRun, "dry-run mode enabled", nil, true)
		r.rememberResult(key, result)
		return result
	}

	execCtx := ctx
	cancel := func() {}
	if r.cfg.ActionTimeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, r.cfg.ActionTimeout)
	}
	defer cancel()

	output, err := r.dispatch(execCtx, action)
	if err != nil && errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("action timed out after %s", r.cfg.ActionTimeout)
	}
	status := ActionResultExecuted
	if err != nil {
		status = ActionResultFailed
	}
	result := buildActionResult(action, key, start, status, output, err, false)
	r.rememberResult(key, result)
	return result
}

func (r *PlaybookRunner) authorize(action ActionSpec) error {
	if err := enforceActionHarness(action); err != nil {
		return err
	}
	if action.RequiresApproval {
		return fmt.Errorf("action requires explicit approval")
	}
	if !action.Safe && !r.cfg.AllowUnsafe {
		return fmt.Errorf("unsafe action blocked by policy")
	}
	if isKubernetesAction(action.Type) && !r.namespaceAllowed(action.Namespace) {
		return fmt.Errorf("namespace %q not allowed", action.Namespace)
	}
	if isShellAction(action.Type) {
		cmd, _, err := shellInvocation(action)
		if err != nil {
			return err
		}
		if !r.shellAllowed(cmd) {
			return fmt.Errorf("shell command %q not allowed", cmd)
		}
	}
	return nil
}

func (r *PlaybookRunner) dispatch(ctx context.Context, action ActionSpec) (string, error) {
	// Enforce at the final dispatch boundary too; rollback and future callers
	// must not bypass the harness by skipping authorize.
	if err := enforceActionHarness(action); err != nil {
		return "", err
	}
	switch {
	case isShellAction(action.Type):
		return runShell(ctx, action)
	case isKubernetesAction(action.Type):
		return "", enforceActionHarness(action)
	default:
		return "", fmt.Errorf("unsupported action type: %s", action.Type)
	}
}

func runShell(ctx context.Context, action ActionSpec) (string, error) {
	cmdName, args, err := shellInvocation(action)
	if err != nil {
		return "", err
	}
	if err := enforceActionHarness(action); err != nil {
		return "", err
	}
	// These local fixtures do not need a subprocess, PATH, or shell expansion.
	if cmdName == "echo" {
		return truncateOutput(strings.Join(args, " ") + "\n"), nil
	}
	if cmdName == "sleep" {
		seconds, _ := strconv.ParseFloat(args[0], 64)
		timer := time.NewTimer(time.Duration(seconds * float64(time.Second)))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", nil
		}
	}
	return "", fmt.Errorf("%w: command %q is proposal-only", errHumanOperatorRequired, cmdName)
}

// enforceActionHarness is a non-overridable boundary for model-originated
// actions. Playbook safe flags, AllowUnsafe, approval tokens, and dry-run must
// never turn a production mutation into an executable action.
func enforceActionHarness(action ActionSpec) error {
	if isKubernetesAction(action.Type) {
		return fmt.Errorf("%w: Kubernetes commands and mutations are proposal-only", errHumanOperatorRequired)
	}
	if !isShellAction(action.Type) {
		return fmt.Errorf("unsupported action type %q", action.Type)
	}
	command, args, err := shellInvocation(action)
	if err != nil {
		return err
	}
	if command != filepath.Base(command) {
		return fmt.Errorf("%w: executable paths are not allowed", errHumanOperatorRequired)
	}
	switch command {
	case "echo":
		for _, arg := range args {
			if strings.ContainsAny(arg, "\x00\n\r") {
				return fmt.Errorf("invalid echo argument")
			}
		}
		return nil
	case "sleep":
		if len(args) != 1 {
			return fmt.Errorf("sleep requires one numeric duration")
		}
		seconds, err := strconv.ParseFloat(args[0], 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 60 {
			return fmt.Errorf("sleep duration must be between 0 and 60 seconds")
		}
		return nil
	default:
		return fmt.Errorf("%w: command %q is proposal-only", errHumanOperatorRequired, command)
	}
}

func shellInvocation(action ActionSpec) (string, []string, error) {
	command := strings.TrimSpace(action.Command)
	if command == "" {
		return "", nil, fmt.Errorf("shell action requires command")
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("shell command is empty")
	}
	args := append([]string{}, parts[1:]...)
	args = append(args, action.Args...)
	return parts[0], args, nil
}

func (r *PlaybookRunner) namespaceAllowed(namespace string) bool {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = r.cfg.DefaultNamespace
	}
	_, ok := r.allowedNamespaces[ns]
	return ok
}

func (r *PlaybookRunner) shellAllowed(command string) bool {
	_, ok := r.allowedShell[command]
	return ok
}

func playbookMatches(pb Playbook, metrics map[string]float64) bool {
	if len(pb.Conditions) == 0 {
		return false
	}
	for _, cond := range pb.Conditions {
		if !conditionMatches(cond, metrics) {
			return false
		}
	}
	return true
}

func conditionMatches(cond PlaybookCondition, metrics map[string]float64) bool {
	val, ok := metrics[cond.Metric]
	if !ok {
		return false
	}
	switch cond.Op {
	case ">":
		return val > cond.Threshold
	case ">=":
		return val >= cond.Threshold
	case "<":
		return val < cond.Threshold
	case "<=":
		return val <= cond.Threshold
	case "==":
		return val == cond.Threshold
	case "!=":
		return val != cond.Threshold
	default:
		return false
	}
}

func normalizePlaybooks(in []Playbook) []Playbook {
	out := make([]Playbook, 0, len(in))
	for _, pb := range in {
		if strings.TrimSpace(pb.ID) == "" || len(pb.Actions) == 0 {
			continue
		}
		pb.ID = strings.TrimSpace(pb.ID)
		pb.Summary = strings.TrimSpace(pb.Summary)
		pb.Severity = strings.TrimSpace(pb.Severity)
		for i := range pb.Actions {
			pb.Actions[i] = normalizeAction(pb.Actions[i])
			if pb.Actions[i].Priority == "" {
				pb.Actions[i].Priority = pb.Severity
			}
		}
		out = append(out, pb)
	}
	return out
}

func normalizeAction(action ActionSpec) ActionSpec {
	action.ID = strings.TrimSpace(action.ID)
	action.Type = strings.ToLower(strings.TrimSpace(action.Type))
	action.NodeName = strings.TrimSpace(action.NodeName)
	action.Target = strings.TrimSpace(action.Target)
	action.Description = strings.TrimSpace(action.Description)
	action.Command = strings.TrimSpace(action.Command)
	action.Namespace = strings.TrimSpace(action.Namespace)
	action.Resource = strings.TrimSpace(action.Resource)
	action.Name = strings.TrimSpace(action.Name)
	action.Priority = strings.TrimSpace(action.Priority)
	if action.Metadata == nil {
		action.Metadata = make(map[string]string)
	}
	return action
}

func dedupeActionSpecs(actions []ActionSpec) []ActionSpec {
	seen := make(map[string]struct{}, len(actions))
	out := make([]ActionSpec, 0, len(actions))
	for _, action := range actions {
		key := idempotencyKey(action)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, action)
	}
	return out
}

func buildActionResult(action ActionSpec, key string, started time.Time, status, output string, err error, dryRun bool) ActionResult {
	result := ActionResult{
		ID:             action.ID,
		Type:           action.Type,
		Status:         status,
		Output:         truncateOutput(output),
		DryRun:         dryRun,
		IdempotencyKey: key,
		StartedAt:      started,
		CompletedAt:    time.Now().UTC(),
	}
	if err != nil {
		result.Error = err.Error()
		if errors.Is(err, errHumanOperatorRequired) {
			result.OperatorHandoff = operatorHandoffForAction(action)
		}
	}
	return result
}

func operatorHandoffForAction(action ActionSpec) *OperatorActionHandoff {
	proposal := firstNonEmpty(action.Description, action.Command, action.Type)
	scope := strings.Join(compactStrings(action.Namespace, action.Resource, action.Name, action.Target, action.NodeName), "/")
	return &OperatorActionHandoff{
		Status:       "awaiting_human_execution",
		Proposal:     proposal,
		Scope:        scope,
		RollbackPlan: strings.TrimSpace(action.RollbackCommand),
		Preflight: []string{
			"Confirm the affected target, current telemetry, service health, and recent changes using read-only tools.",
			"Check replicas, disruption budget, dependencies, blast radius, backup or recovery point, rollback readiness, and operator authority where applicable.",
			"Define the observation window and service-level success and failure thresholds before any change.",
		},
		StopConditions: []string{
			"Do not proceed if the target, pre-action baseline, operator authority, or recovery path cannot be confirmed.",
			"Stop and assess rollback if service health worsens or the change exceeds the approved scope.",
		},
		Verification: []string{
			"After human execution, compare service health and relevant metrics with the pre-action baseline.",
			"Record the actual command, operator, outcome, observation window, and any rollback as external evidence.",
		},
	}
}

func idempotencyKey(action ActionSpec) string {
	payload := struct {
		Type      string            `json:"type"`
		NodeName  string            `json:"node_name"`
		Target    string            `json:"target"`
		Command   string            `json:"command"`
		Args      []string          `json:"args"`
		Namespace string            `json:"namespace"`
		Resource  string            `json:"resource"`
		Name      string            `json:"name"`
		Metadata  map[string]string `json:"metadata"`
	}{
		Type:      action.Type,
		NodeName:  action.NodeName,
		Target:    action.Target,
		Command:   action.Command,
		Args:      action.Args,
		Namespace: action.Namespace,
		Resource:  action.Resource,
		Name:      action.Name,
		Metadata:  action.Metadata,
	}

	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

// ExecuteRollback attempts to undo a previously successful action using its rollbackCommand.
func (r *PlaybookRunner) ExecuteRollback(ctx context.Context, action ActionSpec, dryRun bool) ActionResult {
	if r == nil {
		return ActionResult{Status: ActionResultFailed, Error: "runner not initialized"}
	}
	started := time.Now().UTC()
	key := idempotencyKey(action) + "-rollback"

	if action.RollbackCommand == "" {
		return buildActionResult(action, key, started, ActionResultSkipped, "No rollback command defined", nil, dryRun)
	}
	rollbackAction := action
	rollbackAction.Command = action.RollbackCommand
	rollbackAction.Description = "Rollback proposal: " + action.RollbackCommand
	if err := r.authorize(rollbackAction); err != nil {
		res := buildActionResult(rollbackAction, key, started, ActionResultBlocked, "", err, true)
		res.IsRollback = true
		return res
	}

	if dryRun || r.cfg.DryRun {
		return buildActionResult(action, key, started, ActionResultDryRun, "Dry run: would execute rollback: "+action.RollbackCommand, nil, true)
	}

	execCtx := ctx
	cancel := func() {}
	if r.cfg.ActionTimeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, r.cfg.ActionTimeout)
	}
	defer cancel()

	output, err := r.dispatch(execCtx, rollbackAction)
	res := buildActionResult(action, key, started, "", output, err, false)
	res.IsRollback = true
	if err != nil {
		res.Status = ActionResultFailed
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			res.Error = fmt.Sprintf("rollback timed out after %s", r.cfg.ActionTimeout)
		} else {
			res.Error = err.Error()
		}
	} else {
		res.Status = ActionResultExecuted
	}
	return res
}

func (r *PlaybookRunner) previousResult(key string) (ActionResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneExpiredLocked()

	record, ok := r.executed[key]
	if !ok {
		return ActionResult{}, false
	}
	return record.Result, true
}

func (r *PlaybookRunner) rememberResult(key string, result ActionResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneExpiredLocked()
	r.executed[key] = idempotencyRecord{
		Result:    result,
		ExpiresAt: time.Now().Add(r.cfg.IdempotencyTTL),
	}
}

func (r *PlaybookRunner) pruneExpiredLocked() {
	now := time.Now()
	for key, record := range r.executed {
		if now.After(record.ExpiresAt) {
			delete(r.executed, key)
		}
	}
}

func isShellAction(actionType string) bool {
	switch strings.ToLower(strings.TrimSpace(actionType)) {
	case "shell", "command":
		return true
	default:
		return false
	}
}

func isKubernetesAction(actionType string) bool {
	switch strings.ToLower(strings.TrimSpace(actionType)) {
	case "kubernetes", "k8s", "restart_pod", "restart_deployment", "scale_deployment":
		return true
	default:
		return false
	}
}

func truncateOutput(in string) string {
	const maxChars = 1024
	clean := strings.TrimSpace(in)
	if len(clean) <= maxChars {
		return clean
	}
	return clean[:maxChars]
}

func sanitizeID(in string) string {
	if in == "" {
		return "action"
	}
	mapped := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, in)
	return strings.Trim(mapped, "-")
}

func toSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out[trimmed] = struct{}{}
	}
	return out
}
