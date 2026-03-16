package remediation

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Playbook represents a remediation playbook
type Playbook struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Steps       []PlaybookStep `yaml:"steps"`
	Timeout     time.Duration  `yaml:"timeout"`
	OnFailure   string         `yaml:"on_failure"` // continue, stop, rollback
}

// PlaybookStep is a single step in a playbook
type PlaybookStep struct {
	Name       string                 `yaml:"name"`
	Type       string                 `yaml:"type"` // read, write, validate, notify
	Command    string                 `yaml:"command"`
	Parameters map[string]interface{} `yaml:"parameters"`
	Timeout    time.Duration          `yaml:"timeout"`
	OnFailure  string                 `yaml:"on_failure"`
}

// PlaybookRunner executes playbooks
type PlaybookRunner struct {
	executor *Executor
	logger   *zap.Logger
}

// NewPlaybookRunner creates a new playbook runner
func NewPlaybookRunner(executor *Executor, logger *zap.Logger) *PlaybookRunner {
	return &PlaybookRunner{
		executor: executor,
		logger:   logger.With(zap.String("component", "playbook")),
	}
}

// Run executes a playbook
func (pr *PlaybookRunner) Run(ctx context.Context, playbook *Playbook, dryRun bool) (*PlaybookResult, error) {
	pr.logger.Info("starting playbook",
		zap.String("name", playbook.Name),
		zap.Bool("dry_run", dryRun))

	result := &PlaybookResult{
		Playbook:  playbook.Name,
		StartedAt: time.Now(),
		Steps:     make([]StepResult, 0, len(playbook.Steps)),
	}

	// Set timeout if specified
	if playbook.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, playbook.Timeout)
		defer cancel()
	}

	for i, step := range playbook.Steps {
		pr.logger.Info("executing step",
			zap.String("playbook", playbook.Name),
			zap.String("step", step.Name),
			zap.Int("index", i))

		stepResult := pr.executeStep(ctx, step, dryRun)
		result.Steps = append(result.Steps, stepResult)

		if !stepResult.Success {
			pr.logger.Error("step failed",
				zap.String("step", step.Name),
				zap.String("error", stepResult.Error))

			// Handle failure based on configuration
			switch step.OnFailure {
			case "continue":
				continue
			case "stop", "":
				result.CompletedAt = time.Now()
				result.Success = false
				return result, fmt.Errorf("step %s failed: %s", step.Name, stepResult.Error)
			case "rollback":
				pr.rollback(ctx, result)
				result.CompletedAt = time.Now()
				result.Success = false
				return result, fmt.Errorf("step %s failed, rollback executed", step.Name)
			}
		}
	}

	result.CompletedAt = time.Now()
	result.Success = true
	return result, nil
}

// executeStep executes a single step
func (pr *PlaybookRunner) executeStep(ctx context.Context, step PlaybookStep, dryRun bool) StepResult {
	start := time.Now()
	result := StepResult{
		Step:     step.Name,
		Success:  false,
		Duration: 0,
	}

	// Set step timeout
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}

	// Execute based on step type
	switch step.Type {
	case "read":
		output, err := pr.executeRead(ctx, step)
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Output = output
		}
	case "write":
		action := &Action{
			Type:       step.Command,
			Target:     step.Parameters["target"].(string),
			Parameters: step.Parameters,
		}
		actionResult, execErr := pr.executor.Execute(ctx, action)
		if execErr != nil {
			result.Error = execErr.Error()
		} else {
			result.Success = actionResult.Success
			result.Changes = actionResult.Changes
		}
	case "validate":
		result.Success, result.Error = pr.executeValidate(ctx, step)
	case "notify":
		result.Success, result.Error = pr.executeNotify(ctx, step)
	default:
		result.Error = fmt.Sprintf("unknown step type: %s", step.Type)
	}

	result.Duration = time.Since(start)
	return result
}

// executeRead executes a read step
func (pr *PlaybookRunner) executeRead(ctx context.Context, step PlaybookStep) (string, error) {
	// In production, would execute command and capture output
	output := fmt.Sprintf("read output for %s", step.Command)
	return output, nil
}

// executeValidate executes a validation step
func (pr *PlaybookRunner) executeValidate(ctx context.Context, step PlaybookStep) (bool, string) {
	// In production, would validate expected state
	return true, ""
}

// executeNotify executes a notification step
func (pr *PlaybookRunner) executeNotify(ctx context.Context, step PlaybookStep) (bool, string) {
	// In production, would send notification
	return true, ""
}

// rollback executes rollback for failed playbook
func (pr *PlaybookRunner) rollback(ctx context.Context, result *PlaybookResult) {
	pr.logger.Info("rolling back playbook", zap.String("playbook", result.Playbook))

	// Execute rollback in reverse order
	for i := len(result.Steps) - 1; i >= 0; i-- {
		step := result.Steps[i]
		if step.Success && len(step.Changes) > 0 {
			// Rollback this step
			pr.logger.Info("rolling back step", zap.String("step", step.Step))
		}
	}
}

// PlaybookResult is the result of a playbook execution
type PlaybookResult struct {
	Playbook    string       `json:"playbook"`
	Success     bool         `json:"success"`
	StartedAt   time.Time    `json:"started_at"`
	CompletedAt time.Time    `json:"completed_at"`
	Steps       []StepResult `json:"steps"`
}

// StepResult is the result of a step execution
type StepResult struct {
	Step     string           `json:"step"`
	Success  bool             `json:"success"`
	Duration time.Duration    `json:"duration"`
	Output   string           `json:"output,omitempty"`
	Error    string           `json:"error,omitempty"`
	Changes  []ResourceChange `json:"changes,omitempty"`
}

// LoadPlaybook loads a playbook from YAML
func LoadPlaybook(data []byte) (*Playbook, error) {
	var playbook Playbook
	if err := yaml.Unmarshal(data, &playbook); err != nil {
		return nil, fmt.Errorf("failed to parse playbook: %w", err)
	}
	return &playbook, nil
}

// Predefined playbooks

// ScaleUpPlaybook scales up a deployment
func ScaleUpPlaybook(replicas int) *Playbook {
	return &Playbook{
		Name:        "scale_up",
		Description: "Scale up deployment to handle increased load",
		Steps: []PlaybookStep{
			{
				Name:    "check_current_capacity",
				Type:    "read",
				Command: "kubectl get deployment",
				Parameters: map[string]interface{}{
					"output": "json",
				},
			},
			{
				Name:    "scale_replicas",
				Type:    "write",
				Command: "scale_deployment",
				Parameters: map[string]interface{}{
					"replicas": replicas,
				},
			},
			{
				Name:    "verify_scaling",
				Type:    "validate",
				Command: "check_replicas",
				Parameters: map[string]interface{}{
					"expected": replicas,
				},
			},
		},
		OnFailure: "rollback",
	}
}

// RestartPodPlaybook restarts a pod gracefully
func RestartPodPlaybook() *Playbook {
	return &Playbook{
		Name:        "restart_pod",
		Description: "Gracefully restart a pod",
		Steps: []PlaybookStep{
			{
				Name:    "drain_connections",
				Type:    "write",
				Command: "drain_pod",
			},
			{
				Name:    "delete_pod",
				Type:    "write",
				Command: "restart_pod",
			},
			{
				Name:    "wait_for_ready",
				Type:    "validate",
				Command: "check_pod_ready",
				Timeout: 5 * time.Minute,
			},
		},
	}
}

// FailoverPlaybook fails over traffic to another region
func FailoverPlaybook(region string) *Playbook {
	return &Playbook{
		Name:        "failover",
		Description: "Failover traffic to backup region",
		Steps: []PlaybookStep{
			{
				Name:    "check_target_health",
				Type:    "validate",
				Command: "health_check",
				Parameters: map[string]interface{}{
					"region": region,
				},
			},
			{
				Name:    "update_dns",
				Type:    "write",
				Command: "failover_traffic",
				Parameters: map[string]interface{}{
					"region": region,
				},
			},
			{
				Name:    "verify_failover",
				Type:    "validate",
				Command: "verify_traffic",
				Parameters: map[string]interface{}{
					"region": region,
				},
			},
			{
				Name:    "notify_team",
				Type:    "notify",
				Command: "send_alert",
			},
		},
		OnFailure: "rollback",
	}
}
