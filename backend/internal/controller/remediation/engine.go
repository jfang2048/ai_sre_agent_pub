// Package remediation provides automated fix execution.
package remediation

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ActionType defines the type of remediation
type ActionType string

const (
	ActionTypeRestartPod      ActionType = "restart_pod"
	ActionTypeAnsiblePlaybook ActionType = "ansible_playbook"
	ActionTypeScript          ActionType = "script"
)

// ExecutionLevel controls the governance gate for remediation actions.
type ExecutionLevel string

const (
	// ExecutionLevelSuggestOnly logs the recommendation without taking action.
	ExecutionLevelSuggestOnly ExecutionLevel = "suggest_only"
	// ExecutionLevelDryRun validates and logs but does not execute.
	ExecutionLevelDryRun ExecutionLevel = "dry_run"
	// ExecutionLevelApprovalRequired requires explicit operator approval before execution.
	ExecutionLevelApprovalRequired ExecutionLevel = "approval_required"
	// ExecutionLevelAutoExecute executes immediately (use with extreme caution).
	ExecutionLevelAutoExecute ExecutionLevel = "auto_execute"
)

// RemediationRequest represents a request to fix an issue
type RemediationRequest struct {
	ID              string            `json:"id"`
	Action          ActionType        `json:"action"`
	Target          string            `json:"target"`              // pod name, host, etc.
	Namespace       string            `json:"namespace,omitempty"` // for K8s
	Params          map[string]string `json:"params,omitempty"`    // playbook path, args
	Reason          string            `json:"reason"`
	RequestedBy     string            `json:"requested_by"`
	ExecutionLevel  ExecutionLevel    `json:"execution_level,omitempty"`
	BlastRadius     string            `json:"blast_radius,omitempty"`
	RollbackAction  string            `json:"rollback_action,omitempty"`
	Timeout         string            `json:"timeout,omitempty"`
	IdempotencyNote string            `json:"idempotency_note,omitempty"`
	LinkedEvidence  []string          `json:"linked_evidence,omitempty"`
}

// Engine executes remediations
type Engine struct {
	logger           *zap.Logger
	k8sClient        *kubernetes.Clientset
	dryRun           bool
	allowedScriptDirs []string
}

// NewEngine creates a new remediation engine
func NewEngine(logger *zap.Logger, dryRun bool) (*Engine, error) {
	e := &Engine{
		logger: logger.With(zap.String("component", "remediation_engine")),
		dryRun: dryRun,
	}

	// Initialize K8s client (in-cluster or local)
	// We handle error gracefully as K8s might not be available
	config, err := rest.InClusterConfig()
	if err != nil {
		// Try local config or ignore if not needed immediately
		logger.Warn("failed to load in-cluster config, K8s actions may fail", zap.Error(err))
	} else {
		clientset, err := kubernetes.NewForConfig(config)
		if err == nil {
			e.k8sClient = clientset
		}
	}

	return e, nil
}

// Execute runs the remediation after governance checks.
func (e *Engine) Execute(ctx context.Context, req RemediationRequest) error {
	level := req.ExecutionLevel
	if level == "" {
		if e.dryRun {
			level = ExecutionLevelDryRun
		} else {
			level = ExecutionLevelApprovalRequired
		}
	}

	e.logger.Info("remediation request received",
		zap.String("id", req.ID),
		zap.String("action", string(req.Action)),
		zap.String("target", req.Target),
		zap.String("execution_level", string(level)),
		zap.String("blast_radius", req.BlastRadius),
		zap.Strings("linked_evidence", req.LinkedEvidence))

	switch level {
	case ExecutionLevelSuggestOnly:
		e.logger.Info("suggest_only: action logged but not executed", zap.String("id", req.ID))
		return nil
	case ExecutionLevelDryRun:
		e.logger.Info("dry_run: action validated but not executed", zap.String("id", req.ID))
		return nil
	case ExecutionLevelApprovalRequired:
		e.logger.Info("approval_required: action pending operator approval", zap.String("id", req.ID))
		return fmt.Errorf("action %s requires approval before execution", req.ID)
	case ExecutionLevelAutoExecute:
		// fall through to execute
	default:
		return fmt.Errorf("unknown execution level: %s", level)
	}

	switch req.Action {
	case ActionTypeRestartPod:
		return e.restartPod(ctx, req)
	case ActionTypeAnsiblePlaybook:
		return e.runAnsible(ctx, req)
	case ActionTypeScript:
		return e.runScript(ctx, req)
	default:
		return fmt.Errorf("unknown action type: %s", req.Action)
	}
}

// restartPod kills a pod to trigger a restart
func (e *Engine) restartPod(ctx context.Context, req RemediationRequest) error {
	if e.k8sClient == nil {
		return fmt.Errorf("k8s client not initialized")
	}

	ns := req.Namespace
	if ns == "" {
		ns = "default"
	}

	e.logger.Info("deleting pod to force restart",
		zap.String("namespace", ns),
		zap.String("pod", req.Target))

	// Delete pod with 0 grace period for immediate kill (force restart)
	// In reality, might want safe drain, but this is "auto-remediation" style
	return e.k8sClient.CoreV1().Pods(ns).Delete(ctx, req.Target, v1.DeleteOptions{})
}

// validateScriptPath ensures the path is absolute, exists, has no traversal, and
// contains no shell metacharacters. This prevents arbitrary code execution via
// crafted RemediationRequest parameters.
func validateScriptPath(label, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%s path is empty", label)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s path must be absolute, got %q", label, path)
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("%s path contains traversal sequence, got %q", label, path)
	}
	if strings.ContainsAny(path, "|;&><`()$\\\n\r") {
		return fmt.Errorf("%s path contains shell metacharacters, got %q", label, path)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%s path does not exist: %w", label, err)
	}
	return nil
}

// runAnsible runs an Ansible playbook
func (e *Engine) runAnsible(ctx context.Context, req RemediationRequest) error {
	playbook, ok := req.Params["playbook"]
	if !ok {
		return fmt.Errorf("missing 'playbook' parameter")
	}
	if err := validateScriptPath("playbook", playbook); err != nil {
		return err
	}

	inventory := req.Params["inventory"]
	if inventory == "" {
		inventory = "/etc/ansible/hosts"
	}
	if err := validateScriptPath("inventory", inventory); err != nil {
		return err
	}

	args := []string{playbook, "-i", inventory}
	if req.Target != "" {
		args = append(args, "--limit", req.Target)
	}

	cmd := exec.CommandContext(ctx, "ansible-playbook", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		e.logger.Error("ansible execution failed",
			zap.String("output", string(output)),
			zap.Error(err))
		return fmt.Errorf("ansible failed: %w", err)
	}

	e.logger.Info("ansible execution successful", zap.String("output", string(output)))
	return nil
}

// runScript runs a local script through the script allowlist.
func (e *Engine) runScript(ctx context.Context, req RemediationRequest) error {
	script, ok := req.Params["script"]
	if !ok {
		return fmt.Errorf("missing 'script' parameter")
	}
	if err := validateScriptPath("script", script); err != nil {
		return err
	}
	if err := e.validateScriptAllowlist(script); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("script failed: %w (output: %s)", err, output)
	}

	return nil
}

// validateScriptAllowlist checks that the script path is within an allowed directory.
func (e *Engine) validateScriptAllowlist(scriptPath string) error {
	if len(e.allowedScriptDirs) == 0 {
		return nil // no allowlist configured, allow all validated scripts
	}
	clean := filepath.Clean(scriptPath)
	for _, dir := range e.allowedScriptDirs {
		if strings.HasPrefix(clean, filepath.Clean(dir)+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("script %q is not in any allowed script directory: %v", scriptPath, e.allowedScriptDirs)
}
