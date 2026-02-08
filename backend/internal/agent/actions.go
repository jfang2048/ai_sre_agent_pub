package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	Metadata         map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// ActionResult captures the execution outcome for one action.
type ActionResult struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Status         string    `json:"status"`
	Output         string    `json:"output,omitempty"`
	Error          string    `json:"error,omitempty"`
	DryRun         bool      `json:"dry_run"`
	IdempotencyKey string    `json:"idempotency_key"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
}

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

	mu       sync.Mutex
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
		AllowedShellCommands: []string{"echo", "kubectl", "systemctl"},
		IdempotencyTTL:       30 * time.Minute,
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
	out := make([]ActionSpec, 0, len(r.playbooks))
	for _, pb := range r.playbooks {
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
	sem := make(chan struct{}, 4)

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
		result := buildActionResult(action, key, start, ActionResultSkipped, "", err, true)
		r.rememberResult(key, result)
		return result
	}

	if forceDryRun || r.cfg.DryRun {
		result := buildActionResult(action, key, start, ActionResultDryRun, "dry-run mode enabled", nil, true)
		r.rememberResult(key, result)
		return result
	}

	output, err := r.dispatch(ctx, action)
	status := ActionResultExecuted
	if err != nil {
		status = ActionResultFailed
	}
	result := buildActionResult(action, key, start, status, output, err, false)
	r.rememberResult(key, result)
	return result
}

func (r *PlaybookRunner) authorize(action ActionSpec) error {
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
	switch {
	case isShellAction(action.Type):
		return runShell(ctx, action)
	case isKubernetesAction(action.Type):
		return r.runKubernetes(ctx, action)
	default:
		return "", fmt.Errorf("unsupported action type: %s", action.Type)
	}
}

func runShell(ctx context.Context, action ActionSpec) (string, error) {
	cmdName, args, err := shellInvocation(action)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, cmdName, args...)
	raw, err := cmd.CombinedOutput()
	out := truncateOutput(string(raw))
	if err != nil {
		return out, fmt.Errorf("shell command failed: %w", err)
	}
	return out, nil
}

func (r *PlaybookRunner) runKubernetes(ctx context.Context, action ActionSpec) (string, error) {
	args, err := kubectlArgs(action, r.cfg.DefaultNamespace)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	raw, err := cmd.CombinedOutput()
	out := truncateOutput(string(raw))
	if err != nil {
		return out, fmt.Errorf("kubectl failed: %w", err)
	}
	return out, nil
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

func kubectlArgs(action ActionSpec, defaultNamespace string) ([]string, error) {
	namespace := strings.TrimSpace(action.Namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}

	if strings.TrimSpace(action.Command) != "" {
		parts := strings.Fields(action.Command)
		if len(parts) == 0 || parts[0] != "kubectl" {
			return nil, fmt.Errorf("kubernetes command must start with kubectl")
		}
		return append(parts[1:], action.Args...), nil
	}

	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "restart_pod":
		if action.Name == "" {
			return nil, fmt.Errorf("restart_pod requires name")
		}
		return []string{"-n", namespace, "delete", "pod", action.Name}, nil
	case "restart_deployment":
		if action.Name == "" {
			return nil, fmt.Errorf("restart_deployment requires name")
		}
		return []string{"-n", namespace, "rollout", "restart", "deployment/" + action.Name}, nil
	case "scale_deployment":
		if action.Name == "" {
			return nil, fmt.Errorf("scale_deployment requires name")
		}
		replicas := strings.TrimSpace(action.Metadata["replicas"])
		if replicas == "" {
			replicas = "1"
		}
		return []string{"-n", namespace, "scale", "deployment/" + action.Name, "--replicas", replicas}, nil
	case "kubernetes", "k8s":
		return nil, fmt.Errorf("generic kubernetes action requires command")
	default:
		return nil, fmt.Errorf("unsupported kubernetes action type %q", action.Type)
	}
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
	}
	return result
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
